package main

import (
	"database/sql"
	"errors"
	"fmt"
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
	`
	_, err := db.conn.Exec(schema)
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
		`UPDATE users SET google_credentials = ? WHERE id = ?`,
		encryptedCredentials, userID)
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

func defaultStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
