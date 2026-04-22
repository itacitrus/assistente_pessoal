package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrUserNotFound          = errors.New("user not found")
	ErrNoPendingConfirmation = errors.New("no pending confirmation")
)

type User struct {
	ID                 int64
	PhoneNumber        string
	Name               string
	GoogleCalendarID   string
	GoogleCredentials  string
	DailySummaryTime   string
	WeeklySummaryDay   string
	WeeklySummaryTime  string
	ReminderBefore     string
	AutoConfirmTimeout string
	IsActive           bool
	CreatedAt          time.Time
}

type PendingConfirmation struct {
	ID        int64
	UserID    int64
	EventData string
	Status    string
	CreatedAt time.Time
	PhoneNumber string
	UserName    string
}

type DB struct {
	conn *sql.DB
}

func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id                   INTEGER PRIMARY KEY AUTOINCREMENT,
		phone_number         TEXT UNIQUE NOT NULL,
		name                 TEXT NOT NULL,
		google_calendar_id   TEXT NOT NULL DEFAULT '',
		google_credentials   TEXT NOT NULL DEFAULT '',
		daily_summary_time   TEXT NOT NULL DEFAULT '07:00',
		weekly_summary_day   TEXT NOT NULL DEFAULT 'sunday',
		weekly_summary_time  TEXT NOT NULL DEFAULT '20:00',
		reminder_before      TEXT NOT NULL DEFAULT '1h',
		auto_confirm_timeout TEXT NOT NULL DEFAULT '2h',
		is_active            INTEGER NOT NULL DEFAULT 1,
		created_at           DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS pending_confirmations (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		event_data TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sent_reminders (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		event_id   TEXT NOT NULL,
		sent_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, event_id)
	);

	CREATE TABLE IF NOT EXISTS action_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     INTEGER NOT NULL REFERENCES users(id),
		action      TEXT NOT NULL,
		target_user TEXT,
		details     TEXT NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS calendar_permissions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		grantor_id INTEGER NOT NULL REFERENCES users(id),
		grantee_id INTEGER NOT NULL REFERENCES users(id),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(grantor_id, grantee_id)
	);

	CREATE TABLE IF NOT EXISTS pending_permission_requests (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		requester_id INTEGER NOT NULL REFERENCES users(id),
		target_id    INTEGER NOT NULL REFERENCES users(id),
		event_data   TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'pending',
		created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS user_memories (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		category   TEXT NOT NULL,
		key        TEXT NOT NULL,
		value      TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, category, key)
	);

	CREATE TABLE IF NOT EXISTS conversation_history (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_travel_periods (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL REFERENCES users(id),
		start_date    TEXT NOT NULL,
		end_date      TEXT NOT NULL,
		timezone      TEXT NOT NULL,
		location_name TEXT NOT NULL DEFAULT '',
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_user_travel_periods_user_date
		ON user_travel_periods(user_id, start_date, end_date);
	`
	if _, err := db.conn.Exec(schema); err != nil {
		return err
	}

	// Additive migrations. SQLite has no "ADD COLUMN IF NOT EXISTS", so we
	// ignore duplicate-column errors and let anything else bubble up.
	additive := []string{
		// calendar_event_id links a travel period to the all-day "✈️ Viagem"
		// marker event on the user's calendar, so we can delete the marker
		// when the period is canceled.
		`ALTER TABLE user_travel_periods ADD COLUMN calendar_event_id TEXT NOT NULL DEFAULT ''`,
		// reauth_notified_at tracks when the user last received an automatic
		// reauth-link message. NULL means never notified or already reauthorized.
		// Used to rate-limit the per-minute scheduler from spamming.
		`ALTER TABLE users ADD COLUMN reauth_notified_at DATETIME`,
	}
	for _, stmt := range additive {
		if _, err := db.conn.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("additive migration %q: %w", stmt, err)
		}
	}
	return nil
}

func (db *DB) AddConversationMessage(userID int64, role, content string) error {
	_, err := db.conn.Exec(
		`INSERT INTO conversation_history (user_id, role, content) VALUES (?, ?, ?)`,
		userID, role, content)
	if err != nil {
		return err
	}
	// Keep only last 20 messages per user
	db.conn.Exec(`DELETE FROM conversation_history WHERE user_id = ? AND id NOT IN (
		SELECT id FROM conversation_history WHERE user_id = ? ORDER BY created_at DESC LIMIT 50
	)`, userID, userID)
	return nil
}

func (db *DB) GetConversationHistory(userID int64, limit int) ([]ConversationMessage, error) {
	rows, err := db.conn.Query(
		`SELECT role, content, created_at FROM conversation_history
		 WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

type ConversationMessage struct {
	Role      string
	Content   string
	CreatedAt time.Time
}

func (db *DB) SearchConversationHistory(userID int64, query string, limit int) ([]ConversationMessage, error) {
	rows, err := db.conn.Query(
		`SELECT role, content, created_at FROM conversation_history
		 WHERE user_id = ? AND content LIKE ? ORDER BY created_at DESC LIMIT ?`,
		userID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

type UserMemory struct {
	Category string
	Key      string
	Value    string
}

func (db *DB) SaveMemory(userID int64, category, key, value string) error {
	_, err := db.conn.Exec(
		`INSERT INTO user_memories (user_id, category, key, value) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, category, key) DO UPDATE SET value = ?, updated_at = CURRENT_TIMESTAMP`,
		userID, category, key, value, value)
	return err
}

func (db *DB) GetMemories(userID int64, category string) ([]UserMemory, error) {
	query := `SELECT category, key, value FROM user_memories WHERE user_id = ?`
	args := []any{userID}
	if category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY category, key`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.Category, &m.Key, &m.Value); err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

func (db *DB) SearchMemories(userID int64, query string) ([]UserMemory, error) {
	rows, err := db.conn.Query(
		`SELECT category, key, value FROM user_memories
		 WHERE user_id = ? AND (key LIKE ? OR value LIKE ? OR category LIKE ?)
		 ORDER BY updated_at DESC LIMIT 20`,
		userID, "%"+query+"%", "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.Category, &m.Key, &m.Value); err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

func (db *DB) DeleteMemory(userID int64, category, key string) error {
	_, err := db.conn.Exec(
		`DELETE FROM user_memories WHERE user_id = ? AND category = ? AND key = ?`,
		userID, category, key)
	return err
}

func (db *DB) CreateUser(u *User) error {
	result, err := db.conn.Exec(
		`INSERT INTO users (phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.PhoneNumber, u.Name, u.GoogleCalendarID, u.GoogleCredentials,
		defaultStr(u.DailySummaryTime, "07:00"),
		defaultStr(u.WeeklySummaryDay, "sunday"),
		defaultStr(u.WeeklySummaryTime, "20:00"),
		defaultStr(u.ReminderBefore, "1h"),
		defaultStr(u.AutoConfirmTimeout, "2h"),
	)
	if err != nil {
		return err
	}
	u.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetUserByPhone(phone string) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE phone_number = ?`, phone,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return u, err
}

func (db *DB) ListActiveUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUserCredentials(userID int64, encryptedCredentials string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET google_credentials = ?, reauth_notified_at = NULL WHERE id = ?`,
		encryptedCredentials, userID)
	return err
}

func (db *DB) GetReauthNotifiedAt(userID int64) (*time.Time, error) {
	var notifiedAt sql.NullTime
	err := db.conn.QueryRow(
		`SELECT reauth_notified_at FROM users WHERE id = ?`, userID,
	).Scan(&notifiedAt)
	if err != nil {
		return nil, err
	}
	if !notifiedAt.Valid {
		return nil, nil
	}
	t := notifiedAt.Time
	return &t, nil
}

func (db *DB) SetReauthNotifiedAt(userID int64, t time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE users SET reauth_notified_at = ? WHERE id = ?`, t.UTC(), userID,
	)
	return err
}

func (db *DB) CreatePendingConfirmation(pc *PendingConfirmation) error {
	db.conn.Exec(`UPDATE pending_confirmations SET status = 'cancelled' WHERE user_id = ? AND status = 'pending'`, pc.UserID)

	result, err := db.conn.Exec(
		`INSERT INTO pending_confirmations (user_id, event_data) VALUES (?, ?)`,
		pc.UserID, pc.EventData)
	if err != nil {
		return err
	}
	pc.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetPendingConfirmation(userID int64) (*PendingConfirmation, error) {
	pc := &PendingConfirmation{}
	err := db.conn.QueryRow(
		`SELECT id, user_id, event_data, status, created_at
		 FROM pending_confirmations WHERE user_id = ? AND status = 'pending'
		 ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingConfirmation
	}
	return pc, err
}

func (db *DB) ResolvePendingConfirmation(id int64, status string) error {
	_, err := db.conn.Exec(
		`UPDATE pending_confirmations SET status = ? WHERE id = ?`, status, id)
	return err
}

func (db *DB) GetExpiredPendingConfirmations(userID int64, timeout time.Duration) ([]PendingConfirmation, error) {
	cutoff := time.Now().UTC().Add(-timeout)
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		 u.phone_number, u.name
		 FROM pending_confirmations pc
		 JOIN users u ON u.id = pc.user_id
		 WHERE pc.status = 'pending' AND pc.user_id = ? AND pc.created_at <= ?`, userID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingConfirmation
	for rows.Next() {
		var pc PendingConfirmation
		if err := rows.Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
			&pc.PhoneNumber, &pc.UserName); err != nil {
			return nil, err
		}
		results = append(results, pc)
	}
	return results, rows.Err()
}

func (db *DB) HasSentReminder(userID int64, eventID string) (bool, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM sent_reminders WHERE user_id = ? AND event_id = ?`,
		userID, eventID).Scan(&count)
	return count > 0, err
}

func (db *DB) MarkReminderSent(userID int64, eventID string) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO sent_reminders (user_id, event_id) VALUES (?, ?)`,
		userID, eventID)
	return err
}

func (db *DB) GetUserByName(name string) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE name = ? AND is_active = 1 LIMIT 1`, name,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return u, err
}

func (db *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	return u, err
}

// PermissionRequest represents a pending cross-user calendar access request.
type PermissionRequest struct {
	ID            int64
	RequesterID   int64
	TargetID      int64
	EventData     string
	Status        string
	CreatedAt     time.Time
	RequesterName  string
	RequesterPhone string
	TargetName    string
	TargetPhone   string
}

func (db *DB) CreatePermissionRequest(req *PermissionRequest) error {
	// Cancel any previous pending request from same requester to same target
	db.conn.Exec(
		`UPDATE pending_permission_requests SET status = 'cancelled'
		 WHERE requester_id = ? AND target_id = ? AND status = 'pending'`,
		req.RequesterID, req.TargetID)

	result, err := db.conn.Exec(
		`INSERT INTO pending_permission_requests (requester_id, target_id, event_data, status)
		 VALUES (?, ?, ?, 'pending')`,
		req.RequesterID, req.TargetID, req.EventData)
	if err != nil {
		return err
	}
	req.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetPendingPermissionRequest(targetID int64) (*PermissionRequest, error) {
	req := &PermissionRequest{}
	err := db.conn.QueryRow(
		`SELECT ppr.id, ppr.requester_id, ppr.target_id, ppr.event_data, ppr.status, ppr.created_at,
		 ru.name, ru.phone_number, tu.name, tu.phone_number
		 FROM pending_permission_requests ppr
		 JOIN users ru ON ru.id = ppr.requester_id
		 JOIN users tu ON tu.id = ppr.target_id
		 WHERE ppr.target_id = ? AND ppr.status = 'pending'
		 ORDER BY ppr.created_at DESC LIMIT 1`, targetID,
	).Scan(&req.ID, &req.RequesterID, &req.TargetID, &req.EventData, &req.Status, &req.CreatedAt,
		&req.RequesterName, &req.RequesterPhone, &req.TargetName, &req.TargetPhone)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingConfirmation
	}
	return req, err
}

func (db *DB) ResolvePermissionRequest(id int64, status string) error {
	_, err := db.conn.Exec(
		`UPDATE pending_permission_requests SET status = ? WHERE id = ?`, status, id)
	return err
}

func (db *DB) GetExpiredPermissionRequests(timeout time.Duration) ([]PermissionRequest, error) {
	cutoff := time.Now().UTC().Add(-timeout)
	rows, err := db.conn.Query(
		`SELECT ppr.id, ppr.requester_id, ppr.target_id, ppr.event_data, ppr.status, ppr.created_at,
		 ru.name, ru.phone_number, tu.name, tu.phone_number
		 FROM pending_permission_requests ppr
		 JOIN users ru ON ru.id = ppr.requester_id
		 JOIN users tu ON tu.id = ppr.target_id
		 WHERE ppr.status = 'pending' AND ppr.created_at <= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PermissionRequest
	for rows.Next() {
		var req PermissionRequest
		if err := rows.Scan(&req.ID, &req.RequesterID, &req.TargetID, &req.EventData, &req.Status, &req.CreatedAt,
			&req.RequesterName, &req.RequesterPhone, &req.TargetName, &req.TargetPhone); err != nil {
			return nil, err
		}
		results = append(results, req)
	}
	return results, rows.Err()
}

func defaultStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
