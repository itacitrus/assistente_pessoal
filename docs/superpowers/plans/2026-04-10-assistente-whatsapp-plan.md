# Assistente WhatsApp + Google Calendar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a multi-user WhatsApp bot (Go/whatsmeow) that creates, queries, edits, and cancels Google Calendar events from voice/text messages, with scheduled notifications.

**Architecture:** Two Docker containers — a Go bot (whatsmeow + scheduler + orchestration) and a Python FastAPI transcription service (AssemblyAI). SQLite for state. Claude API for NLP intent extraction. Terraform for AWS EC2 deployment.

**Tech Stack:** Go 1.22+, whatsmeow, robfig/cron, Google Calendar API, go-anthropic, modernc.org/sqlite, Python 3.11+, FastAPI, AssemblyAI SDK, Terraform, Docker Compose

**Spec:** `docs/superpowers/specs/2026-04-10-assistente-whatsapp-design.md`

---

## File Map

```
assistente_pessoal/
├── bot/
│   ├── main.go              # Entry point: CLI (run/add-user), init whatsmeow, scheduler, OAuth server
│   ├── config.go            # Config struct loaded from env vars
│   ├── db.go                # SQLite schema init, user CRUD, confirmation CRUD
│   ├── handler.go           # whatsmeow event handler: route audio/text, reject unknown numbers
│   ├── transcription.go     # HTTP client to call transcription-api
│   ├── claude.go            # Claude API client: intent extraction prompt + JSON parsing
│   ├── calendar.go          # Google Calendar API: CRUD events, list by date range, OAuth helpers
│   ├── orchestrator.go      # Pipeline: transcribe → claude → action → respond
│   ├── confirmation.go      # Pending confirmations: create, resolve, auto-confirm check
│   ├── scheduler.go         # Cron jobs: reminders, daily summary, weekly summary, auto-confirm
│   ├── formatter.go         # Format calendar events as WhatsApp-friendly text
│   ├── crypto.go            # AES-256-GCM encrypt/decrypt for refresh tokens
│   ├── permissions.go       # Cross-user calendar delegation
│   ├── audit.go             # Action audit log
│   ├── watchdog.go          # WhatsApp connection monitor
│   ├── go.mod
│   ├── go.sum
│   ├── Dockerfile
│   ├── crypto_test.go       # Tests live in bot/ (same package main)
│   ├── db_test.go
│   ├── claude_test.go
│   ├── formatter_test.go
│   ├── permissions_test.go
│   ├── audit_test.go
│   └── integration_test.go
│
├── transcription/
│   ├── main.py              # FastAPI app with POST /transcribe
│   ├── requirements.txt
│   └── Dockerfile
│
├── terraform/
│   ├── main.tf
│   ├── variables.tf
│   ├── outputs.tf
│   └── cloud-init.yaml
│
├── scripts/
│   ├── assistente-bot.service  # Systemd unit
│   └── backup.sh               # SQLite backup to S3
│
├── docker-compose.yml
├── .env.example
└── .gitignore
```

---

## Task 1: Project Scaffolding + Go Module Init

**Files:**
- Create: `bot/go.mod`, `bot/config.go`, `bot/main.go`, `.env.example`, `.gitignore`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
mkdir -p bot
cd bot
go mod init github.com/giovannirambo/assistente_pessoal/bot
```

- [ ] **Step 2: Create .gitignore**

Create `.gitignore` at repo root:

```gitignore
# Env
.env

# Go
bot/bot
bot/assistente_pessoal

# Python
transcription/__pycache__/
transcription/*.pyc
transcription/.venv/

# SQLite
*.db
*.db-wal
*.db-shm

# Terraform
terraform/.terraform/
terraform/*.tfstate
terraform/*.tfstate.backup
terraform/*.tfvars

# OS
.DS_Store
```

- [ ] **Step 3: Create .env.example**

```env
# Google Calendar OAuth App (create at console.cloud.google.com)
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GOOGLE_REDIRECT_URI=http://localhost:8080/oauth/callback

# Claude API
ANTHROPIC_API_KEY=

# AssemblyAI
ASSEMBLYAI_API_KEY=

# Encryption key for storing Google refresh tokens (generate with: openssl rand -hex 32)
ENCRYPTION_KEY=

# Scheduler defaults
DEFAULT_DAILY_SUMMARY_TIME=07:00
DEFAULT_WEEKLY_SUMMARY_DAY=sunday
DEFAULT_WEEKLY_SUMMARY_TIME=20:00
DEFAULT_REMINDER_BEFORE=1h
DEFAULT_AUTO_CONFIRM_TIMEOUT=2h

# Transcription service URL (docker-compose internal)
TRANSCRIPTION_URL=http://transcription:8000
```

- [ ] **Step 4: Create config.go**

```go
// bot/config.go
package main

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURI  string
	AnthropicAPIKey    string
	AssemblyAIAPIKey   string
	EncryptionKey      string
	TranscriptionURL   string

	DefaultDailySummaryTime  string
	DefaultWeeklySummaryDay  string
	DefaultWeeklySummaryTime string
	DefaultReminderBefore    time.Duration
	DefaultAutoConfirmTimeout time.Duration
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("GOOGLE_REDIRECT_URI"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		AssemblyAIAPIKey:   os.Getenv("ASSEMBLYAI_API_KEY"),
		EncryptionKey:      os.Getenv("ENCRYPTION_KEY"),
		TranscriptionURL:   os.Getenv("TRANSCRIPTION_URL"),

		DefaultDailySummaryTime:  envOrDefault("DEFAULT_DAILY_SUMMARY_TIME", "07:00"),
		DefaultWeeklySummaryDay:  envOrDefault("DEFAULT_WEEKLY_SUMMARY_DAY", "sunday"),
		DefaultWeeklySummaryTime: envOrDefault("DEFAULT_WEEKLY_SUMMARY_TIME", "20:00"),
	}

	if cfg.TranscriptionURL == "" {
		cfg.TranscriptionURL = "http://localhost:8000"
	}
	if cfg.GoogleRedirectURI == "" {
		cfg.GoogleRedirectURI = "http://localhost:8080/oauth/callback"
	}

	var err error
	cfg.DefaultReminderBefore, err = time.ParseDuration(envOrDefault("DEFAULT_REMINDER_BEFORE", "1h"))
	if err != nil {
		return nil, fmt.Errorf("invalid DEFAULT_REMINDER_BEFORE: %w", err)
	}
	cfg.DefaultAutoConfirmTimeout, err = time.ParseDuration(envOrDefault("DEFAULT_AUTO_CONFIRM_TIMEOUT", "2h"))
	if err != nil {
		return nil, fmt.Errorf("invalid DEFAULT_AUTO_CONFIRM_TIMEOUT: %w", err)
	}

	// Validate required fields
	required := map[string]string{
		"GOOGLE_CLIENT_ID":     cfg.GoogleClientID,
		"GOOGLE_CLIENT_SECRET": cfg.GoogleClientSecret,
		"ANTHROPIC_API_KEY":    cfg.AnthropicAPIKey,
		"ENCRYPTION_KEY":       cfg.EncryptionKey,
	}
	for name, val := range required {
		if val == "" {
			return nil, fmt.Errorf("required env var %s is not set", name)
		}
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 5: Create minimal main.go with CLI skeleton**

```go
// bot/main.go
package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: bot <command>")
		fmt.Println("Commands:")
		fmt.Println("  run        Start the WhatsApp bot")
		fmt.Println("  add-user   Add a new user")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runBot()
	case "add-user":
		addUser()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runBot() {
	log.Println("Bot starting... (not yet implemented)")
}

func addUser() {
	log.Println("Add user... (not yet implemented)")
}
```

- [ ] **Step 6: Install initial dependencies**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go get go.mau.fi/whatsmeow@latest
go get go.mau.fi/whatsmeow/store/sqlstore@latest
go get modernc.org/sqlite@latest
go get github.com/robfig/cron/v3@latest
go get github.com/liushuangls/go-anthropic/v2@latest
go get google.golang.org/api/calendar/v3@latest
go get golang.org/x/oauth2@latest
go get golang.org/x/oauth2/google@latest
go mod tidy
```

- [ ] **Step 7: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0, no errors.

- [ ] **Step 8: Commit**

```bash
git add bot/go.mod bot/go.sum bot/main.go bot/config.go .env.example .gitignore
git commit -m "feat: scaffold Go bot with config and CLI skeleton"
```

---

## Task 2: Crypto Module (AES-256-GCM)

**Files:**
- Create: `bot/crypto.go`, `bot/crypto_test.go`

- [ ] **Step 1: Write the failing test**

Create `bot/crypto_test.go`:

```go
package main

import (
	"encoding/hex"
	"testing"


)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// 32 bytes hex = 64 chars
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	plaintext := "ya29.a0AfH6SMBx-refresh-token-here"

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if encrypted == plaintext {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	key2 := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	encrypted, err := Encrypt("secret", key1)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	_, err = Decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	_, err := Encrypt("secret", "short-key")
	if err == nil {
		t.Fatal("expected error for invalid key length")
	}
}

func _ () { _ = hex.DecodeString } // keep import
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestEncrypt
```

Expected: FAIL — `Encrypt` not defined.

- [ ] **Step 3: Implement crypto.go**

```go
// bot/crypto.go
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext using AES-256-GCM with the given hex-encoded key.
func Encrypt(plaintext, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext using AES-256-GCM.
func Decrypt(encoded, hexKey string) (string, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return "", fmt.Errorf("key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestEncrypt
go test ./bot/ -v -run TestDecrypt
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add bot/crypto.go bot/crypto_test.go
git commit -m "feat: add AES-256-GCM encrypt/decrypt for token storage"
```

---

## Task 3: Database Layer (SQLite)

**Files:**
- Create: `bot/db.go`, `bot/db_test.go`

- [ ] **Step 1: Write failing tests for DB operations**

Create `bot/db_test.go`:

```go
package main

import (
	"os"
	"testing"
	"time"


)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	path := t.TempDir() + "/test.db"
	db, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndGetUser(t *testing.T) {
	db := setupTestDB(t)

	user := &User{
		PhoneNumber:     "5511999999999",
		Name:            "Waldyr",
		GoogleCalendarID: "waldyr@gmail.com",
		GoogleCredentials: "encrypted-token",
		DailySummaryTime: "07:00",
		WeeklySummaryDay: "sunday",
		WeeklySummaryTime: "20:00",
		ReminderBefore:   "1h",
		AutoConfirmTimeout: "2h",
	}

	err := db.CreateUser(user)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected user ID to be set")
	}

	got, err := db.GetUserByPhone("5511999999999")
	if err != nil {
		t.Fatalf("GetUserByPhone failed: %v", err)
	}
	if got.Name != "Waldyr" {
		t.Fatalf("expected name Waldyr, got %s", got.Name)
	}
	if got.GoogleCalendarID != "waldyr@gmail.com" {
		t.Fatalf("expected calendar waldyr@gmail.com, got %s", got.GoogleCalendarID)
	}
}

func TestGetUserByPhoneNotFound(t *testing.T) {
	db := setupTestDB(t)

	_, err := db.GetUserByPhone("0000000000")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestListActiveUsers(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "A", GoogleCalendarID: "a@g.com", GoogleCredentials: "x"})
	db.CreateUser(&User{PhoneNumber: "222", Name: "B", GoogleCalendarID: "b@g.com", GoogleCredentials: "x"})

	users, err := db.ListActiveUsers()
	if err != nil {
		t.Fatalf("ListActiveUsers failed: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestCreateAndResolvePendingConfirmation(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "A", GoogleCalendarID: "a@g.com", GoogleCredentials: "x"})
	user, _ := db.GetUserByPhone("111")

	pc := &PendingConfirmation{
		UserID:    user.ID,
		EventData: `{"title":"Reuniao","date":"2026-04-11","time":"15:00","duration_minutes":60}`,
	}

	err := db.CreatePendingConfirmation(pc)
	if err != nil {
		t.Fatalf("CreatePendingConfirmation failed: %v", err)
	}

	got, err := db.GetPendingConfirmation(user.ID)
	if err != nil {
		t.Fatalf("GetPendingConfirmation failed: %v", err)
	}
	if got.EventData != pc.EventData {
		t.Fatalf("event data mismatch")
	}

	err = db.ResolvePendingConfirmation(got.ID, "confirmed")
	if err != nil {
		t.Fatalf("ResolvePendingConfirmation failed: %v", err)
	}

	_, err = db.GetPendingConfirmation(user.ID)
	if err != ErrNoPendingConfirmation {
		t.Fatalf("expected ErrNoPendingConfirmation after resolve, got %v", err)
	}
}

func TestGetExpiredPendingConfirmations(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "A", GoogleCalendarID: "a@g.com", GoogleCredentials: "x"})
	user, _ := db.GetUserByPhone("111")

	pc := &PendingConfirmation{
		UserID:    user.ID,
		EventData: `{"title":"Test"}`,
	}
	db.CreatePendingConfirmation(pc)

	// With a 0-second timeout, everything is expired
	expired, err := db.GetExpiredPendingConfirmations(0 * time.Second)
	if err != nil {
		t.Fatalf("GetExpiredPendingConfirmations failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
}

func _ () { _ = os.TempDir } // keep import
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestCreate
```

Expected: FAIL — `DB`, `User`, etc. not defined.

- [ ] **Step 3: Implement db.go**

```go
// bot/db.go
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
	// Joined fields (populated by GetExpired)
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
	// Cancel any existing pending confirmation for this user
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

func (db *DB) GetExpiredPendingConfirmations(timeout time.Duration) ([]PendingConfirmation, error) {
	cutoff := time.Now().Add(-timeout)
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		 u.phone_number, u.name
		 FROM pending_confirmations pc
		 JOIN users u ON u.id = pc.user_id
		 WHERE pc.status = 'pending' AND pc.created_at < ?`, cutoff)
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run "TestCreate|TestGet|TestList"
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add bot/db.go bot/db_test.go
git commit -m "feat: add SQLite database layer with users and pending confirmations"
```

---

## Task 4: Claude API Client (Intent Extraction)

**Files:**
- Create: `bot/claude.go`, `bot/claude_test.go`

- [ ] **Step 1: Write test for prompt building and response parsing**

Create `bot/claude_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"


)

func TestParseIntentResponse_CreateEvent(t *testing.T) {
	raw := `{
		"intent": "criar_evento",
		"data": {
			"title": "Reuniao com Joao",
			"date": "2026-04-11",
			"time": "15:00",
			"duration_minutes": 60
		},
		"confirmation_message": "Agendei Reuniao com Joao para 11/04 as 15h. Confirma?"
	}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "criar_evento" {
		t.Fatalf("expected criar_evento, got %s", result.Intent)
	}
	if result.Data.Title != "Reuniao com Joao" {
		t.Fatalf("expected title Reuniao com Joao, got %s", result.Data.Title)
	}
	if result.Data.Date != "2026-04-11" {
		t.Fatalf("expected date 2026-04-11, got %s", result.Data.Date)
	}
	if result.Data.DurationMinutes != 60 {
		t.Fatalf("expected duration 60, got %d", result.Data.DurationMinutes)
	}
}

func TestParseIntentResponse_ConsultarAgenda(t *testing.T) {
	raw := `{
		"intent": "consultar_agenda",
		"data": {
			"start_date": "2026-04-10",
			"end_date": "2026-04-10"
		},
		"confirmation_message": "Aqui estao os compromissos de hoje."
	}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "consultar_agenda" {
		t.Fatalf("expected consultar_agenda, got %s", result.Intent)
	}
	if result.Data.StartDate != "2026-04-10" {
		t.Fatalf("expected start_date 2026-04-10, got %s", result.Data.StartDate)
	}
}

func TestParseIntentResponse_Confirmar(t *testing.T) {
	raw := `{"intent": "confirmar", "data": {}, "confirmation_message": "Ok, confirmado!"}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "confirmar" {
		t.Fatalf("expected confirmar, got %s", result.Intent)
	}
}

func TestBuildIntentPrompt(t *testing.T) {
	prompt := BuildIntentPrompt("Waldyr", "marcar reuniao com Joao amanha as 15h")
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	// Should contain the user name and the message
	if !containsStr(prompt, "Waldyr") {
		t.Fatal("prompt should contain user name")
	}
	if !containsStr(prompt, "marcar reuniao") {
		t.Fatal("prompt should contain the message")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func _ () { _ = json.Marshal } // keep import
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestParseIntent
```

Expected: FAIL.

- [ ] **Step 3: Implement claude.go**

```go
// bot/claude.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

type IntentResult struct {
	Intent              string     `json:"intent"`
	Data                IntentData `json:"data"`
	ConfirmationMessage string     `json:"confirmation_message"`
}

type IntentData struct {
	// criar_evento
	Title           string `json:"title,omitempty"`
	Date            string `json:"date,omitempty"`
	Time            string `json:"time,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`

	// consultar_agenda
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	// editar_evento / cancelar_evento
	SearchQuery string          `json:"search_query,omitempty"`
	Changes     json.RawMessage `json:"changes,omitempty"`
}

type ClaudeClient struct {
	client *anthropic.Client
}

func NewClaudeClient(apiKey string) *ClaudeClient {
	return &ClaudeClient{
		client: anthropic.NewClient(apiKey),
	}
}

func BuildIntentPrompt(userName, message string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e um assistente de agenda. Analise a mensagem do usuario %s e retorne APENAS um JSON valido.

Data/hora atual: %s

Intencoes possiveis:
- criar_evento: extraia title, date (YYYY-MM-DD), time (HH:MM), duration_minutes (default: 60)
- consultar_agenda: extraia start_date (YYYY-MM-DD), end_date (YYYY-MM-DD)
- editar_evento: extraia search_query (texto para encontrar o evento), changes (objeto com campos a alterar)
- cancelar_evento: extraia search_query
- confirmar: o usuario esta confirmando uma acao pendente
- negar: o usuario esta negando uma acao pendente

Responda APENAS com JSON, sem markdown, sem explicacao:
{"intent": "...", "data": {...}, "confirmation_message": "mensagem amigavel para o usuario em portugues"}

Mensagem do usuario: %s`, userName, now, message)
}

func ParseIntentResponse(raw []byte) (*IntentResult, error) {
	// Strip markdown code fences if present
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var result IntentResult
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("parse intent JSON: %w (raw: %s)", err, s)
	}
	return &result, nil
}

func (c *ClaudeClient) ExtractIntent(ctx context.Context, userName, message string) (*IntentResult, error) {
	prompt := BuildIntentPrompt(userName, message)

	resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaude3Dot5Sonnet20241022,
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{
				Role:    anthropic.RoleUser,
				Content: []anthropic.MessageContent{{Type: "text", Text: &prompt}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude API: %w", err)
	}

	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("claude returned empty response")
	}

	text := resp.Content[0].GetText()
	return ParseIntentResponse([]byte(text))
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run "TestParseIntent|TestBuildIntent"
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add bot/claude.go bot/claude_test.go
git commit -m "feat: add Claude API client for intent extraction"
```

---

## Task 5: Google Calendar Client

**Files:**
- Create: `bot/calendar.go`

- [ ] **Step 1: Implement calendar.go**

```go
// bot/calendar.go
package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarClient struct {
	oauthConfig *oauth2.Config
}

type CalendarEvent struct {
	ID       string
	Title    string
	Start    time.Time
	End      time.Time
	Location string
}

func NewCalendarClient(clientID, clientSecret, redirectURI string) *CalendarClient {
	return &CalendarClient{
		oauthConfig: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Scopes:       []string{calendar.CalendarEventsScope},
			Endpoint:     google.Endpoint,
		},
	}
}

func (c *CalendarClient) AuthURL(state string) string {
	return c.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

func (c *CalendarClient) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return c.oauthConfig.Exchange(ctx, code)
}

func (c *CalendarClient) serviceForUser(ctx context.Context, refreshToken string) (*calendar.Service, error) {
	token := &oauth2.Token{RefreshToken: refreshToken}
	tokenSource := c.oauthConfig.TokenSource(ctx, token)
	return calendar.NewService(ctx, option.WithTokenSource(tokenSource))
}

func (c *CalendarClient) CreateEvent(ctx context.Context, refreshToken, calendarID string, ev CalendarEvent) (*CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	event := &calendar.Event{
		Summary:  ev.Title,
		Location: ev.Location,
		Start: &calendar.EventDateTime{
			DateTime: ev.Start.Format(time.RFC3339),
			TimeZone: "America/Sao_Paulo",
		},
		End: &calendar.EventDateTime{
			DateTime: ev.End.Format(time.RFC3339),
			TimeZone: "America/Sao_Paulo",
		},
	}

	created, err := svc.Events.Insert(calendarID, event).Do()
	if err != nil {
		return nil, fmt.Errorf("insert event: %w", err)
	}

	return &CalendarEvent{
		ID:    created.Id,
		Title: created.Summary,
		Start: ev.Start,
		End:   ev.End,
	}, nil
}

func (c *CalendarClient) ListEvents(ctx context.Context, refreshToken, calendarID string, start, end time.Time) ([]CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	events, err := svc.Events.List(calendarID).
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	var result []CalendarEvent
	for _, item := range events.Items {
		ev := CalendarEvent{
			ID:       item.Id,
			Title:    item.Summary,
			Location: item.Location,
		}
		if item.Start.DateTime != "" {
			ev.Start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
		}
		if item.End.DateTime != "" {
			ev.End, _ = time.Parse(time.RFC3339, item.End.DateTime)
		}
		result = append(result, ev)
	}
	return result, nil
}

func (c *CalendarClient) DeleteEvent(ctx context.Context, refreshToken, calendarID, eventID string) error {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}
	return svc.Events.Delete(calendarID, eventID).Do()
}

func (c *CalendarClient) UpdateEvent(ctx context.Context, refreshToken, calendarID, eventID string, ev CalendarEvent) error {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}

	event := &calendar.Event{
		Summary:  ev.Title,
		Location: ev.Location,
	}
	if !ev.Start.IsZero() {
		event.Start = &calendar.EventDateTime{
			DateTime: ev.Start.Format(time.RFC3339),
			TimeZone: "America/Sao_Paulo",
		}
	}
	if !ev.End.IsZero() {
		event.End = &calendar.EventDateTime{
			DateTime: ev.End.Format(time.RFC3339),
			TimeZone: "America/Sao_Paulo",
		}
	}

	_, err = svc.Events.Patch(calendarID, eventID, event).Do()
	return err
}

func (c *CalendarClient) FindEvent(ctx context.Context, refreshToken, calendarID, query string) (*CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	// Search in the next 30 days
	now := time.Now()
	events, err := svc.Events.List(calendarID).
		TimeMin(now.Add(-24*time.Hour).Format(time.RFC3339)).
		TimeMax(now.Add(30*24*time.Hour).Format(time.RFC3339)).
		Q(query).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(1).
		Do()
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}

	if len(events.Items) == 0 {
		return nil, fmt.Errorf("nenhum evento encontrado para: %s", query)
	}

	item := events.Items[0]
	ev := &CalendarEvent{
		ID:       item.Id,
		Title:    item.Summary,
		Location: item.Location,
	}
	if item.Start.DateTime != "" {
		ev.Start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
	}
	if item.End.DateTime != "" {
		ev.End, _ = time.Parse(time.RFC3339, item.End.DateTime)
	}
	return ev, nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add bot/calendar.go
git commit -m "feat: add Google Calendar client with OAuth2 and CRUD operations"
```

---

## Task 6: Event Formatter

**Files:**
- Create: `bot/formatter.go`, `bot/formatter_test.go`

- [ ] **Step 1: Write failing tests**

Create `bot/formatter_test.go`:

```go
package main

import (
	"testing"
	"time"


)

func TestFormatDailySummary_WithEvents(t *testing.T) {
	events := []CalendarEvent{
		{Title: "Standup", Start: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 10, 9, 30, 0, 0, time.UTC)},
		{Title: "Almoço com cliente", Start: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 10, 13, 0, 0, 0, time.UTC)},
	}

	result := FormatDailySummary("Waldyr", events, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	if result == "" {
		t.Fatal("expected non-empty summary")
	}
	if !stringContains(result, "Standup") || !stringContains(result, "Almoço") {
		t.Fatalf("summary should contain event titles, got: %s", result)
	}
	if !stringContains(result, "09:00") {
		t.Fatalf("summary should contain formatted times, got: %s", result)
	}
}

func TestFormatDailySummary_NoEvents(t *testing.T) {
	result := FormatDailySummary("Waldyr", nil, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	if !stringContains(result, "livre") && !stringContains(result, "Nenhum") {
		t.Fatalf("should indicate no events, got: %s", result)
	}
}

func TestFormatWeeklySummary(t *testing.T) {
	events := []CalendarEvent{
		{Title: "Reuniao segunda", Start: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)},
		{Title: "Reuniao terca", Start: time.Date(2026, 4, 14, 14, 0, 0, 0, time.UTC)},
	}

	result := FormatWeeklySummary("Waldyr", events, time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC))
	if result == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestFormatReminder(t *testing.T) {
	ev := CalendarEvent{
		Title: "Reuniao com CEO",
		Start: time.Date(2026, 4, 10, 15, 0, 0, 0, time.UTC),
	}
	result := FormatReminder(ev)
	if !stringContains(result, "Reuniao com CEO") {
		t.Fatalf("reminder should contain event title, got: %s", result)
	}
	if !stringContains(result, "15:00") {
		t.Fatalf("reminder should contain time, got: %s", result)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestFormat
```

Expected: FAIL.

- [ ] **Step 3: Implement formatter.go**

```go
// bot/formatter.go
package main

import (
	"fmt"
	"strings"
	"time"
)

var weekdaysPT = map[time.Weekday]string{
	time.Sunday:    "Domingo",
	time.Monday:    "Segunda",
	time.Tuesday:   "Terca",
	time.Wednesday: "Quarta",
	time.Thursday:  "Quinta",
	time.Friday:    "Sexta",
	time.Saturday:  "Sabado",
}

func FormatDailySummary(userName string, events []CalendarEvent, date time.Time) string {
	dayStr := date.Format("02/01/2006")
	weekday := weekdaysPT[date.Weekday()]

	if len(events) == 0 {
		return fmt.Sprintf("Bom dia, %s! Sua agenda de %s (%s) esta livre. Nenhum compromisso hoje.", userName, weekday, dayStr)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Bom dia, %s! Sua agenda de %s (%s):\n\n", userName, weekday, dayStr))
	for _, ev := range events {
		startStr := ev.Start.Format("15:04")
		endStr := ev.End.Format("15:04")
		sb.WriteString(fmt.Sprintf("  %s - %s: %s\n", startStr, endStr, ev.Title))
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d compromisso(s)", len(events)))
	return sb.String()
}

func FormatWeeklySummary(userName string, events []CalendarEvent, weekStart time.Time) string {
	weekEndDate := weekStart.AddDate(0, 0, 6)

	if len(events) == 0 {
		return fmt.Sprintf("Boa noite, %s! Sua semana de %s a %s esta livre.",
			userName, weekStart.Format("02/01"), weekEndDate.Format("02/01"))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Boa noite, %s! Agenda da semana (%s a %s):\n\n",
		userName, weekStart.Format("02/01"), weekEndDate.Format("02/01")))

	// Group by day
	currentDay := ""
	for _, ev := range events {
		dayKey := ev.Start.Format("02/01")
		weekday := weekdaysPT[ev.Start.Weekday()]
		if dayKey != currentDay {
			if currentDay != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("*%s %s*\n", weekday, dayKey))
			currentDay = dayKey
		}
		sb.WriteString(fmt.Sprintf("  %s: %s\n", ev.Start.Format("15:04"), ev.Title))
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d compromisso(s) na semana", len(events)))
	return sb.String()
}

func FormatReminder(ev CalendarEvent) string {
	return fmt.Sprintf("Lembrete: *%s* comeca as %s (em 1 hora)",
		ev.Title, ev.Start.Format("15:04"))
}

func FormatEventCreated(ev CalendarEvent) string {
	weekday := weekdaysPT[ev.Start.Weekday()]
	return fmt.Sprintf("Evento criado: *%s*\n%s, %s as %s",
		ev.Title, weekday, ev.Start.Format("02/01"), ev.Start.Format("15:04"))
}

func FormatEventList(events []CalendarEvent) string {
	if len(events) == 0 {
		return "Nenhum compromisso encontrado nesse periodo."
	}

	var sb strings.Builder
	currentDay := ""
	for _, ev := range events {
		dayKey := ev.Start.Format("02/01")
		weekday := weekdaysPT[ev.Start.Weekday()]
		if dayKey != currentDay {
			if currentDay != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("*%s %s*\n", weekday, dayKey))
			currentDay = dayKey
		}
		sb.WriteString(fmt.Sprintf("  %s - %s: %s\n", ev.Start.Format("15:04"), ev.End.Format("15:04"), ev.Title))
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestFormat
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add bot/formatter.go bot/formatter_test.go
git commit -m "feat: add WhatsApp message formatters for calendar events"
```

---

## Task 7: Transcription HTTP Client (Go side)

**Files:**
- Create: `bot/transcription.go`

- [ ] **Step 1: Implement transcription.go**

```go
// bot/transcription.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

type TranscriptionClient struct {
	baseURL    string
	httpClient *http.Client
}

type TranscriptionResponse struct {
	Text string `json:"text"`
}

func NewTranscriptionClient(baseURL string) *TranscriptionClient {
	return &TranscriptionClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // transcription can take a while
		},
	}
}

func (c *TranscriptionClient) Transcribe(audioData []byte, filename string) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("write audio data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", c.baseURL+"/transcribe", &buf)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("transcription request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("transcription failed (status %d): %s", resp.StatusCode, body)
	}

	var result TranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.Text, nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add bot/transcription.go
git commit -m "feat: add HTTP client for transcription service"
```

---

## Task 8: WhatsApp Handler + Orchestrator

**Files:**
- Create: `bot/handler.go`, `bot/orchestrator.go`, `bot/confirmation.go`

- [ ] **Step 1: Implement handler.go**

```go
// bot/handler.go
package main

import (
	"context"
	"fmt"
	"log"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type Handler struct {
	client       *whatsmeow.Client
	db           *DB
	orchestrator *Orchestrator
}

func NewHandler(client *whatsmeow.Client, db *DB, orchestrator *Orchestrator) *Handler {
	return &Handler{
		client:       client,
		db:           db,
		orchestrator: orchestrator,
	}
}

func (h *Handler) HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		h.handleMessage(v)
	}
}

func (h *Handler) handleMessage(msg *events.Message) {
	// Extract sender phone number (without @s.whatsapp.net)
	sender := msg.Info.Sender.User

	// Look up user
	user, err := h.db.GetUserByPhone(sender)
	if err == ErrUserNotFound {
		h.sendText(msg.Info.Sender, "Nao te conheço ainda. Peca ao administrador para te cadastrar.")
		return
	}
	if err != nil {
		log.Printf("Error looking up user %s: %v", sender, err)
		return
	}
	if !user.IsActive {
		return
	}

	ctx := context.Background()

	// Determine message content
	var text string
	var isAudio bool

	if audioMsg := msg.Message.GetAudioMessage(); audioMsg != nil {
		isAudio = true
		audioData, err := h.downloadAudio(ctx, audioMsg)
		if err != nil {
			log.Printf("Error downloading audio from %s: %v", sender, err)
			h.sendText(msg.Info.Sender, "Nao consegui baixar o audio. Tente novamente.")
			return
		}
		text, err = h.orchestrator.transcription.Transcribe(audioData, "audio.ogg")
		if err != nil {
			log.Printf("Error transcribing audio from %s: %v", sender, err)
			h.sendText(msg.Info.Sender, "Nao consegui transcrever o audio. Tente novamente.")
			return
		}
	} else if textMsg := msg.Message.GetConversation(); textMsg != "" {
		text = textMsg
	} else if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		text = extMsg.GetText()
	}

	if text == "" {
		return // ignore non-text, non-audio messages
	}

	_ = isAudio // used for logging later if needed
	log.Printf("[%s] %s: %s", user.Name, sender, text)

	// Process through orchestrator
	response, err := h.orchestrator.Process(ctx, user, text)
	if err != nil {
		log.Printf("Error processing message from %s: %v", sender, err)
		h.sendText(msg.Info.Sender, "Ocorreu um erro ao processar sua mensagem. Tente novamente.")
		return
	}

	if response != "" {
		h.sendText(msg.Info.Sender, response)
	}
}

func (h *Handler) downloadAudio(ctx context.Context, audioMsg *waE2E.AudioMessage) ([]byte, error) {
	return h.client.Download(audioMsg)
}

func (h *Handler) sendText(to types.JID, text string) {
	_, err := h.client.SendMessage(context.Background(), to, &waE2E.Message{
		Conversation: &text,
	})
	if err != nil {
		log.Printf("Error sending message to %s: %v", to.User, err)
	}
}

func (h *Handler) SendTextToPhone(phone, text string) error {
	jid := types.NewJID(phone, types.DefaultUserServer)
	_, err := h.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: &text,
	})
	return err
}

func _ () { _ = fmt.Sprintf } // keep import
```

- [ ] **Step 2: Implement confirmation.go**

```go
// bot/confirmation.go
package main

import (
	"encoding/json"
	"fmt"
	"time"
)

type ConfirmationManager struct {
	db  *DB
	cal *CalendarClient
	cfg *Config
}

func NewConfirmationManager(db *DB, cal *CalendarClient, cfg *Config) *ConfirmationManager {
	return &ConfirmationManager{db: db, cal: cal, cfg: cfg}
}

func (cm *ConfirmationManager) CreatePending(user *User, intentData IntentData, confirmMsg string) (string, error) {
	eventJSON, err := json.Marshal(intentData)
	if err != nil {
		return "", fmt.Errorf("marshal event data: %w", err)
	}

	pc := &PendingConfirmation{
		UserID:    user.ID,
		EventData: string(eventJSON),
	}
	if err := cm.db.CreatePendingConfirmation(pc); err != nil {
		return "", fmt.Errorf("save pending: %w", err)
	}

	return confirmMsg, nil
}

func (cm *ConfirmationManager) Confirm(user *User) (string, error) {
	pc, err := cm.db.GetPendingConfirmation(user.ID)
	if err == ErrNoPendingConfirmation {
		return "Nao ha nenhuma acao pendente para confirmar.", nil
	}
	if err != nil {
		return "", err
	}

	return cm.executeConfirmation(user, pc)
}

func (cm *ConfirmationManager) Deny(user *User) (string, error) {
	pc, err := cm.db.GetPendingConfirmation(user.ID)
	if err == ErrNoPendingConfirmation {
		return "Nao ha nenhuma acao pendente para cancelar.", nil
	}
	if err != nil {
		return "", err
	}

	if err := cm.db.ResolvePendingConfirmation(pc.ID, "cancelled"); err != nil {
		return "", err
	}
	return "Ok, cancelado!", nil
}

func (cm *ConfirmationManager) executeConfirmation(user *User, pc *PendingConfirmation) (string, error) {
	var data IntentData
	if err := json.Unmarshal([]byte(pc.EventData), &data); err != nil {
		return "", fmt.Errorf("unmarshal event data: %w", err)
	}

	// Decrypt Google credentials
	refreshToken, err := Decrypt(user.GoogleCredentials, cm.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	// Parse date and time
	startTime, err := time.ParseInLocation("2006-01-02 15:04", data.Date+" "+data.Time, time.Local)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}

	duration := time.Duration(data.DurationMinutes) * time.Minute
	if data.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}

	ev := CalendarEvent{
		Title: data.Title,
		Start: startTime,
		End:   startTime.Add(duration),
	}

	created, err := cm.cal.CreateEvent(nil, refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		return "", fmt.Errorf("create calendar event: %w", err)
	}

	if err := cm.db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		return "", err
	}

	return FormatEventCreated(*created), nil
}
```

- [ ] **Step 3: Implement orchestrator.go**

```go
// bot/orchestrator.go
package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

type Orchestrator struct {
	claude        *ClaudeClient
	cal           *CalendarClient
	transcription *TranscriptionClient
	db            *DB
	cfg           *Config
	confirm       *ConfirmationManager
}

func NewOrchestrator(claude *ClaudeClient, cal *CalendarClient, transcription *TranscriptionClient, db *DB, cfg *Config) *Orchestrator {
	o := &Orchestrator{
		claude:        claude,
		cal:           cal,
		transcription: transcription,
		db:            db,
		cfg:           cfg,
	}
	o.confirm = NewConfirmationManager(db, cal, cfg)
	return o
}

func (o *Orchestrator) Process(ctx context.Context, user *User, message string) (string, error) {
	// Extract intent via Claude
	intent, err := o.claude.ExtractIntent(ctx, user.Name, message)
	if err != nil {
		return "", fmt.Errorf("extract intent: %w", err)
	}

	log.Printf("[%s] Intent: %s", user.Name, intent.Intent)

	switch intent.Intent {
	case "criar_evento":
		return o.confirm.CreatePending(user, intent.Data, intent.ConfirmationMessage)

	case "consultar_agenda":
		return o.handleConsulta(ctx, user, intent)

	case "editar_evento":
		return o.handleEditar(ctx, user, intent)

	case "cancelar_evento":
		return o.handleCancelar(ctx, user, intent)

	case "confirmar":
		return o.confirm.Confirm(user)

	case "negar":
		return o.confirm.Deny(user)

	default:
		return intent.ConfirmationMessage, nil
	}
}

func (o *Orchestrator) handleConsulta(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	loc := time.Now().Location()
	startDate, err := time.ParseInLocation("2006-01-02", intent.Data.StartDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse start_date: %w", err)
	}
	endDate, err := time.ParseInLocation("2006-01-02", intent.Data.EndDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse end_date: %w", err)
	}
	// End date should be end of day
	endDate = endDate.Add(24*time.Hour - time.Second)

	events, err := o.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("list events: %w", err)
	}

	return FormatEventList(events), nil
}

func (o *Orchestrator) handleEditar(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", err
	}

	ev, err := o.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, intent.Data.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	// Apply changes from intent data
	updated := *ev
	if intent.Data.Title != "" {
		updated.Title = intent.Data.Title
	}
	if intent.Data.Date != "" && intent.Data.Time != "" {
		loc := time.Now().Location()
		newStart, _ := time.ParseInLocation("2006-01-02 15:04", intent.Data.Date+" "+intent.Data.Time, loc)
		duration := ev.End.Sub(ev.Start)
		updated.Start = newStart
		updated.End = newStart.Add(duration)
	}

	if err := o.cal.UpdateEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID, updated); err != nil {
		return "", fmt.Errorf("update event: %w", err)
	}

	return fmt.Sprintf("Evento *%s* atualizado com sucesso!", ev.Title), nil
}

func (o *Orchestrator) handleCancelar(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", err
	}

	ev, err := o.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, intent.Data.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	if err := o.cal.DeleteEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID); err != nil {
		return "", fmt.Errorf("delete event: %w", err)
	}

	return fmt.Sprintf("Evento *%s* cancelado.", ev.Title), nil
}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 5: Commit**

```bash
git add bot/handler.go bot/orchestrator.go bot/confirmation.go
git commit -m "feat: add WhatsApp message handler, orchestrator, and confirmation flow"
```

---

## Task 9: Scheduler (Cron Jobs)

**Files:**
- Create: `bot/scheduler.go`

- [ ] **Step 1: Implement scheduler.go**

```go
// bot/scheduler.go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron    *cron.Cron
	db      *DB
	cal     *CalendarClient
	cfg     *Config
	sendMsg func(phone, text string) error
}

func NewScheduler(db *DB, cal *CalendarClient, cfg *Config, sendMsg func(phone, text string) error) *Scheduler {
	return &Scheduler{
		cron:    cron.New(cron.WithLocation(time.Local)),
		db:      db,
		cal:     cal,
		cfg:     cfg,
		sendMsg: sendMsg,
	}
}

func (s *Scheduler) Start() {
	// Check reminders every minute
	s.cron.AddFunc("* * * * *", s.checkReminders)

	// Check auto-confirm every minute
	s.cron.AddFunc("* * * * *", s.checkAutoConfirm)

	// Daily summaries — check every minute, send at each user's configured time
	s.cron.AddFunc("* * * * *", s.checkDailySummaries)

	// Weekly summaries — check every minute, send at each user's configured day/time
	s.cron.AddFunc("* * * * *", s.checkWeeklySummaries)

	s.cron.Start()
	log.Println("Scheduler started")
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) checkReminders() {
	users, err := s.db.ListActiveUsers()
	if err != nil {
		log.Printf("Scheduler: error listing users: %v", err)
		return
	}

	for _, user := range users {
		s.checkUserReminders(&user)
	}
}

func (s *Scheduler) checkUserReminders(user *User) {
	reminderDuration, err := time.ParseDuration(user.ReminderBefore)
	if err != nil {
		reminderDuration = time.Hour
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
	if err != nil {
		return
	}

	now := time.Now()
	windowStart := now.Add(reminderDuration - 30*time.Second)
	windowEnd := now.Add(reminderDuration + 30*time.Second)

	ctx := context.Background()
	events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, windowStart, windowEnd)
	if err != nil {
		log.Printf("Scheduler: error listing events for %s: %v", user.Name, err)
		return
	}

	for _, ev := range events {
		sent, _ := s.db.HasSentReminder(user.ID, ev.ID)
		if sent {
			continue
		}

		msg := FormatReminder(ev)
		if err := s.sendMsg(user.PhoneNumber, msg); err != nil {
			log.Printf("Scheduler: error sending reminder to %s: %v", user.Name, err)
			continue
		}
		s.db.MarkReminderSent(user.ID, ev.ID)
		log.Printf("Scheduler: sent reminder to %s for %s", user.Name, ev.Title)
	}
}

func (s *Scheduler) checkAutoConfirm() {
	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		timeout, err := time.ParseDuration(user.AutoConfirmTimeout)
		if err != nil {
			timeout = s.cfg.DefaultAutoConfirmTimeout
		}

		expired, err := s.db.GetExpiredPendingConfirmations(timeout)
		if err != nil {
			continue
		}

		for _, pc := range expired {
			if pc.UserID != user.ID {
				continue
			}

			cm := NewConfirmationManager(s.db, s.cal, s.cfg)
			msg, err := cm.executeConfirmation(&user, &pc)
			if err != nil {
				log.Printf("Scheduler: auto-confirm error for %s: %v", user.Name, err)
				s.db.ResolvePendingConfirmation(pc.ID, "error")
				continue
			}

			autoMsg := fmt.Sprintf("Confirmei automaticamente:\n\n%s", msg)
			s.sendMsg(user.PhoneNumber, autoMsg)
			log.Printf("Scheduler: auto-confirmed event for %s", user.Name)
		}
	}
}

func (s *Scheduler) checkDailySummaries() {
	now := time.Now()
	currentTime := now.Format("15:04")

	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		if user.DailySummaryTime != currentTime {
			continue
		}
		// Only send at the exact minute (avoid duplicate sends)
		if now.Second() > 30 {
			continue
		}

		refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
		if err != nil {
			continue
		}

		ctx := context.Background()
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		dayEnd := dayStart.Add(24*time.Hour - time.Second)

		events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, dayStart, dayEnd)
		if err != nil {
			log.Printf("Scheduler: error getting daily events for %s: %v", user.Name, err)
			continue
		}

		msg := FormatDailySummary(user.Name, events, dayStart)
		s.sendMsg(user.PhoneNumber, msg)
		log.Printf("Scheduler: sent daily summary to %s (%d events)", user.Name, len(events))
	}
}

func (s *Scheduler) checkWeeklySummaries() {
	now := time.Now()
	currentTime := now.Format("15:04")
	currentDay := now.Weekday().String()

	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		if user.WeeklySummaryTime != currentTime {
			continue
		}
		if !stringsEqualFold(user.WeeklySummaryDay, currentDay) {
			continue
		}
		if now.Second() > 30 {
			continue
		}

		refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
		if err != nil {
			continue
		}

		ctx := context.Background()
		// Next 7 days starting tomorrow
		weekStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		weekEnd := weekStart.AddDate(0, 0, 7)

		events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, weekStart, weekEnd)
		if err != nil {
			log.Printf("Scheduler: error getting weekly events for %s: %v", user.Name, err)
			continue
		}

		msg := FormatWeeklySummary(user.Name, events, weekStart)
		s.sendMsg(user.PhoneNumber, msg)
		log.Printf("Scheduler: sent weekly summary to %s (%d events)", user.Name, len(events))
	}
}

func stringsEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add bot/scheduler.go
git commit -m "feat: add scheduler for reminders, daily/weekly summaries, and auto-confirm"
```

---

## Task 10: Main Entry Point (Wire Everything Together)

**Files:**
- Modify: `bot/main.go`

- [ ] **Step 1: Implement the full main.go**

Replace `bot/main.go` with:

```go
// bot/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: bot <command>")
		fmt.Println("Commands:")
		fmt.Println("  run        Start the WhatsApp bot")
		fmt.Println("  add-user   Add a new user")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runBot()
	case "add-user":
		addUser()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runBot() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize app database
	db, err := NewDB("data/bot.db")
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	// Initialize whatsmeow SQLite store
	dbLog := waLog.Stdout("Database", "WARN", true)
	container, err := sqlstore.New("sqlite", "file:data/whatsmeow.db?_pragma=foreign_keys(1)", dbLog)
	if err != nil {
		log.Fatalf("Failed to init whatsmeow store: %v", err)
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		log.Fatalf("Failed to get device: %v", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	waClient := whatsmeow.NewClient(deviceStore, clientLog)

	// Initialize services
	claude := NewClaudeClient(cfg.AnthropicAPIKey)
	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	transcription := NewTranscriptionClient(cfg.TranscriptionURL)
	orchestrator := NewOrchestrator(claude, cal, transcription, db, cfg)

	handler := NewHandler(waClient, db, orchestrator)
	waClient.AddEventHandler(handler.HandleEvent)

	// Connect to WhatsApp
	if waClient.Store.ID == nil {
		// New device — show QR code
		qrChan, _ := waClient.GetQRChannel(context.Background())
		err = waClient.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("QR Code — scan with WhatsApp:")
				fmt.Println(evt.Code)
			} else {
				log.Printf("QR event: %s", evt.Event)
			}
		}
	} else {
		err = waClient.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
	}
	log.Println("WhatsApp connected")

	// Start scheduler
	scheduler := NewScheduler(db, cal, cfg, handler.SendTextToPhone)
	scheduler.Start()
	defer scheduler.Stop()

	// Start OAuth callback server
	go startOAuthServer(cal, db, cfg)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	waClient.Disconnect()
}

func startOAuthServer(cal *CalendarClient, db *DB, cfg *Config) {
	http.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state") // state = phone number

		if code == "" || state == "" {
			http.Error(w, "Missing code or state", http.StatusBadRequest)
			return
		}

		token, err := cal.ExchangeCode(r.Context(), code)
		if err != nil {
			log.Printf("OAuth exchange error: %v", err)
			http.Error(w, "OAuth exchange failed", http.StatusInternalServerError)
			return
		}

		user, err := db.GetUserByPhone(state)
		if err != nil {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}

		encrypted, err := Encrypt(token.RefreshToken, cfg.EncryptionKey)
		if err != nil {
			http.Error(w, "Encryption failed", http.StatusInternalServerError)
			return
		}

		if err := db.UpdateUserCredentials(user.ID, encrypted); err != nil {
			http.Error(w, "Failed to save credentials", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "Google Calendar autorizado com sucesso para %s! Pode fechar esta janela.", user.Name)
		log.Printf("OAuth completed for %s (%s)", user.Name, state)
	})

	log.Println("OAuth callback server listening on :8080")
	http.ListenAndServe(":8080", nil)
}

func addUser() {
	fs := flag.NewFlagSet("add-user", flag.ExitOnError)
	phone := fs.String("phone", "", "Phone number (e.g. 5511999999999)")
	name := fs.String("name", "", "User name")
	calendarID := fs.String("calendar", "", "Google Calendar email")
	fs.Parse(os.Args[2:])

	if *phone == "" || *name == "" || *calendarID == "" {
		fmt.Println("Usage: bot add-user --phone=5511... --name=Name --calendar=email@gmail.com")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := NewDB("data/bot.db")
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	user := &User{
		PhoneNumber:      *phone,
		Name:             *name,
		GoogleCalendarID: *calendarID,
		GoogleCredentials: "", // will be set via OAuth
	}

	if err := db.CreateUser(user); err != nil {
		log.Fatalf("Failed to create user: %v", err)
	}

	// Generate OAuth URL
	cal := NewCalendarClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURI)
	authURL := cal.AuthURL(*phone) // phone as state

	fmt.Printf("User %s created (ID: %d)\n", *name, user.ID)
	fmt.Printf("\nSend this link to %s to authorize Google Calendar:\n%s\n", *name, authURL)
}
```

- [ ] **Step 2: Create data directory placeholder**

```bash
mkdir -p /Users/giovanni/Documents/GitHub/assistente_pessoal/bot/data
touch /Users/giovanni/Documents/GitHub/assistente_pessoal/bot/data/.gitkeep
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git add bot/main.go bot/data/.gitkeep
git commit -m "feat: wire up main entry point with WhatsApp, scheduler, and OAuth server"
```

---

## Task 11: Transcription API (Python/FastAPI)

**Files:**
- Create: `transcription/main.py`, `transcription/requirements.txt`, `transcription/Dockerfile`

- [ ] **Step 1: Create requirements.txt**

```
fastapi==0.115.0
uvicorn[standard]==0.30.0
assemblyai>=0.30.0
python-multipart>=0.0.9
```

- [ ] **Step 2: Implement main.py**

```python
# transcription/main.py
import os
import tempfile

import assemblyai as aai
from fastapi import FastAPI, UploadFile, File, HTTPException

app = FastAPI(title="Transcription API")

aai.settings.api_key = os.environ.get("ASSEMBLYAI_API_KEY", "")


@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    if not aai.settings.api_key:
        raise HTTPException(status_code=500, detail="ASSEMBLYAI_API_KEY not configured")

    # Save uploaded file to temp location
    suffix = os.path.splitext(file.filename or "audio.ogg")[1]
    with tempfile.NamedTemporaryFile(delete=False, suffix=suffix) as tmp:
        content = await file.read()
        tmp.write(content)
        tmp_path = tmp.name

    try:
        config = aai.TranscriptionConfig(
            language_code="pt",
            speech_models=["universal-3-pro"],
        )
        transcriber = aai.Transcriber()
        transcript = transcriber.transcribe(tmp_path, config=config)

        if transcript.status == aai.TranscriptStatus.error:
            raise HTTPException(status_code=500, detail=f"Transcription failed: {transcript.error}")

        return {"text": transcript.text or ""}
    finally:
        os.unlink(tmp_path)


@app.get("/health")
async def health():
    return {"status": "ok"}
```

- [ ] **Step 3: Create Dockerfile**

```dockerfile
# transcription/Dockerfile
FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY main.py .

EXPOSE 8000

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
```

- [ ] **Step 4: Test locally (quick smoke test)**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/transcription
pip install -r requirements.txt
python -c "from main import app; print('FastAPI app loaded OK')"
```

Expected: "FastAPI app loaded OK"

- [ ] **Step 5: Commit**

```bash
git add transcription/
git commit -m "feat: add FastAPI transcription service wrapping AssemblyAI"
```

---

## Task 12: Docker Compose + Bot Dockerfile

**Files:**
- Create: `bot/Dockerfile`, `docker-compose.yml`

- [ ] **Step 1: Create bot Dockerfile**

```dockerfile
# bot/Dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -o /bot .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
ENV TZ=America/Sao_Paulo

COPY --from=builder /bot /bot

ENTRYPOINT ["/bot"]
CMD ["run"]
```

- [ ] **Step 2: Create docker-compose.yml**

```yaml
# docker-compose.yml
services:
  bot:
    build: ./bot
    container_name: assistente-bot
    restart: unless-stopped
    env_file: .env
    volumes:
      - bot-data:/app/data
    depends_on:
      transcription:
        condition: service_healthy
    environment:
      - TRANSCRIPTION_URL=http://transcription:8000
    ports:
      - "127.0.0.1:8080:8080"  # OAuth callback (localhost only)
    working_dir: /app

  transcription:
    build: ./transcription
    container_name: assistente-transcription
    restart: unless-stopped
    env_file: .env
    healthcheck:
      test: ["CMD", "python", "-c", "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')"]
      interval: 10s
      timeout: 5s
      retries: 3

volumes:
  bot-data:
```

- [ ] **Step 3: Verify docker-compose config is valid**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
docker compose config --quiet 2>&1 || echo "docker compose not installed or config invalid"
```

Expected: no output (valid config) or docker compose not installed.

- [ ] **Step 4: Commit**

```bash
git add bot/Dockerfile docker-compose.yml
git commit -m "feat: add Docker setup with bot and transcription services"
```

---

## Task 13: Terraform IaC

**Files:**
- Create: `terraform/main.tf`, `terraform/variables.tf`, `terraform/outputs.tf`, `terraform/cloud-init.yaml`

- [ ] **Step 1: Create variables.tf**

```hcl
# terraform/variables.tf

variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "sa-east-1" # São Paulo
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.small"
}

variable "admin_ip" {
  description = "Admin IP for SSH access (CIDR, e.g. 1.2.3.4/32)"
  type        = string
}

variable "key_name" {
  description = "Name of existing EC2 Key Pair for SSH"
  type        = string
}

variable "repo_url" {
  description = "Git repository URL to clone"
  type        = string
  default     = "https://github.com/giovannirambo/assistente_pessoal.git"
}
```

- [ ] **Step 2: Create main.tf**

```hcl
# terraform/main.tf

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

data "aws_ami" "amazon_linux" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-*-x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

resource "aws_security_group" "bot" {
  name_prefix = "assistente-bot-"
  description = "Security group for WhatsApp bot"

  ingress {
    description = "SSH from admin"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.admin_ip]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_eip" "bot" {
  domain = "vpc"

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_instance" "bot" {
  ami                    = data.aws_ami.amazon_linux.id
  instance_type          = var.instance_type
  key_name               = var.key_name
  vpc_security_group_ids = [aws_security_group.bot.id]

  root_block_device {
    volume_size = 20
    volume_type = "gp3"
  }

  user_data = file("${path.module}/cloud-init.yaml")

  tags = {
    Name = "assistente-bot"
  }
}

resource "aws_eip_association" "bot" {
  instance_id   = aws_instance.bot.id
  allocation_id = aws_eip.bot.id
}
```

- [ ] **Step 3: Create outputs.tf**

```hcl
# terraform/outputs.tf

output "public_ip" {
  description = "Public IP of the bot instance"
  value       = aws_eip.bot.public_ip
}

output "ssh_command" {
  description = "SSH command to connect"
  value       = "ssh -i ~/.ssh/${var.key_name}.pem ec2-user@${aws_eip.bot.public_ip}"
}

output "ssh_tunnel_oauth" {
  description = "SSH tunnel for OAuth callback"
  value       = "ssh -L 8080:localhost:8080 -i ~/.ssh/${var.key_name}.pem ec2-user@${aws_eip.bot.public_ip}"
}
```

- [ ] **Step 4: Create cloud-init.yaml**

```yaml
# terraform/cloud-init.yaml
#cloud-config
package_update: true
packages:
  - docker
  - git

runcmd:
  # Start Docker
  - systemctl enable docker
  - systemctl start docker
  - usermod -aG docker ec2-user

  # Install Docker Compose plugin
  - mkdir -p /usr/local/lib/docker/cli-plugins
  - curl -SL "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64" -o /usr/local/lib/docker/cli-plugins/docker-compose
  - chmod +x /usr/local/lib/docker/cli-plugins/docker-compose

  # Set timezone
  - timedatectl set-timezone America/Sao_Paulo

  # Create app directory
  - mkdir -p /opt/assistente
  - chown ec2-user:ec2-user /opt/assistente
```

- [ ] **Step 5: Verify Terraform config is valid**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/terraform
terraform fmt -check . 2>&1 || echo "terraform not installed or format issues"
```

- [ ] **Step 6: Commit**

```bash
git add terraform/
git commit -m "feat: add Terraform IaC for AWS EC2 deployment"
```

---

## Task 14: Integration Test (Local End-to-End)

**Files:**
- Create: `bot/integration_test.go`

- [ ] **Step 1: Write integration test for the orchestrator pipeline**

This test mocks external APIs (Claude, Calendar) and verifies the full flow.

```go
// bot/integration_test.go
package main

import (
	"testing"
	"time"


)

func TestConfirmationFlow(t *testing.T) {
	db := setupTestDB(t)
	encKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Create a user with fake encrypted credentials
	fakeToken, _ := Encrypt("fake-refresh-token", encKey)
	db.CreateUser(&User{
		PhoneNumber:        "5511999999999",
		Name:               "Waldyr",
		GoogleCalendarID:   "waldyr@gmail.com",
		GoogleCredentials:  fakeToken,
		AutoConfirmTimeout: "2h",
	})

	user, _ := db.GetUserByPhone("5511999999999")

	// Create pending confirmation
	intentData := IntentData{
		Title:           "Reuniao com CEO",
		Date:            "2026-04-11",
		Time:            "15:00",
		DurationMinutes: 60,
	}

	cfg := &Config{EncryptionKey: encKey}
	cm := NewConfirmationManager(db, nil, cfg)

	msg, err := cm.CreatePending(user, intentData, "Agendar Reuniao com CEO para 11/04 as 15h?")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if msg == "" {
		t.Fatal("expected confirmation message")
	}

	// Verify pending exists
	pc, err := db.GetPendingConfirmation(user.ID)
	if err != nil {
		t.Fatalf("GetPendingConfirmation failed: %v", err)
	}
	if pc.Status != "pending" {
		t.Fatalf("expected status pending, got %s", pc.Status)
	}

	// Deny the confirmation
	denyMsg, err := cm.Deny(user)
	if err != nil {
		t.Fatalf("Deny failed: %v", err)
	}
	if denyMsg == "" {
		t.Fatal("expected deny message")
	}

	// Verify it's resolved
	_, err = db.GetPendingConfirmation(user.ID)
	if err != ErrNoPendingConfirmation {
		t.Fatalf("expected ErrNoPendingConfirmation, got %v", err)
	}
}

func TestAutoConfirmExpiry(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{
		PhoneNumber:      "111",
		Name:             "Test",
		GoogleCalendarID: "t@g.com",
		GoogleCredentials: "x",
	})
	user, _ := db.GetUserByPhone("111")

	db.CreatePendingConfirmation(&PendingConfirmation{
		UserID:    user.ID,
		EventData: `{"title":"Test","date":"2026-04-11","time":"10:00","duration_minutes":30}`,
	})

	// With 0 timeout, everything is expired immediately
	expired, err := db.GetExpiredPendingConfirmations(0 * time.Second)
	if err != nil {
		t.Fatalf("GetExpiredPendingConfirmations failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0].UserName != "Test" {
		t.Fatalf("expected user name Test, got %s", expired[0].UserName)
	}
}
```

- [ ] **Step 2: Run the integration tests**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run "TestConfirmation|TestAutoConfirm"
```

Expected: all PASS.

- [ ] **Step 3: Run all tests together**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add bot/integration_test.go
git commit -m "test: add integration tests for confirmation flow and auto-confirm"
```

---

## Task 15: Final Wiring — CLAUDE.md + README Skeleton

**Files:**
- Create: `CLAUDE.md`

- [ ] **Step 1: Create CLAUDE.md with project conventions**

```markdown
# Assistente Pessoal WhatsApp + Google Calendar

## Architecture

Two services orchestrated via Docker Compose:
- `bot/` — Go service (whatsmeow + scheduler + orchestration)
- `transcription/` — Python/FastAPI service (AssemblyAI transcription)

## Development

```bash
# Run Go tests
go test ./bot/ -v

# Build bot locally
cd bot && go build -o bot .

# Run transcription service locally
cd transcription && uvicorn main:app --reload

# Docker Compose
docker compose up --build
```

## Key Patterns

- All external API calls (Claude, Google Calendar, AssemblyAI) are in dedicated files
- User credentials are encrypted with AES-256-GCM before storing in SQLite
- Pending confirmations auto-confirm after user-configurable timeout (default: 2h)
- Scheduler runs cron checks every minute, respects per-user timezone/preferences

## Deploy

```bash
cd terraform
terraform init
terraform apply -var="admin_ip=YOUR_IP/32" -var="key_name=YOUR_KEY"
```
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: add CLAUDE.md with project conventions and dev commands"
```

---

## Task 16: Audit Log

**Files:**
- Create: `bot/audit.go`, `bot/audit_test.go`

- [ ] **Step 1: Write failing tests**

Create `bot/audit_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestLogAction(t *testing.T) {
	db := setupTestDB(t)
	db.CreateUser(&User{PhoneNumber: "111", Name: "Waldyr", GoogleCalendarID: "w@g.com", GoogleCredentials: "x"})
	user, _ := db.GetUserByPhone("111")

	audit := NewAuditLog(db)
	err := audit.Log(user.ID, "criar_evento", "", `{"title":"Reuniao","date":"2026-04-11"}`)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	// Log a cross-user action
	err = audit.Log(user.ID, "criar_evento", "Andre", `{"title":"Sync","date":"2026-04-12"}`)
	if err != nil {
		t.Fatalf("Log cross-user failed: %v", err)
	}

	entries, err := audit.Query(user.ID, time.Now().Add(-1*time.Hour), time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].TargetUser != "Andre" {
		t.Fatalf("expected target_user Andre, got %s", entries[1].TargetUser)
	}
}

func TestFormatAuditLog(t *testing.T) {
	entries := []AuditEntry{
		{Action: "criar_evento", Details: `{"title":"Reuniao"}`, CreatedAt: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)},
		{Action: "cancelar_evento", TargetUser: "Andre", Details: `{"title":"Sync"}`, CreatedAt: time.Date(2026, 4, 10, 14, 0, 0, 0, time.UTC)},
	}

	result := FormatAuditLog("Waldyr", entries)
	if result == "" {
		t.Fatal("expected non-empty audit log")
	}
	if !stringContains(result, "criar_evento") {
		t.Fatalf("should contain action name, got: %s", result)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestLogAction
```

Expected: FAIL — `AuditLog` not defined.

- [ ] **Step 3: Implement audit.go**

```go
// bot/audit.go
package main

import (
	"fmt"
	"strings"
	"time"
)

type AuditEntry struct {
	ID         int64
	UserID     int64
	Action     string
	TargetUser string
	Details    string
	CreatedAt  time.Time
}

type AuditLog struct {
	db *DB
}

func NewAuditLog(db *DB) *AuditLog {
	return &AuditLog{db: db}
}

func (a *AuditLog) Log(userID int64, action, targetUser, details string) error {
	_, err := a.db.conn.Exec(
		`INSERT INTO action_log (user_id, action, target_user, details) VALUES (?, ?, ?, ?)`,
		userID, action, targetUser, details)
	return err
}

func (a *AuditLog) Query(userID int64, start, end time.Time) ([]AuditEntry, error) {
	rows, err := a.db.conn.Query(
		`SELECT id, user_id, action, COALESCE(target_user, ''), details, created_at
		 FROM action_log
		 WHERE user_id = ? AND created_at BETWEEN ? AND ?
		 ORDER BY created_at ASC`,
		userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.TargetUser, &e.Details, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

var actionLabelsPT = map[string]string{
	"criar_evento":    "Criou evento",
	"editar_evento":   "Editou evento",
	"cancelar_evento": "Cancelou evento",
	"consultar_agenda":"Consultou agenda",
	"confirmar":       "Confirmou",
	"negar":           "Negou",
	"auto_confirm":    "Auto-confirmou",
	"grant_access":    "Concedeu acesso",
	"revoke_access":   "Revogou acesso",
}

func FormatAuditLog(userName string, entries []AuditEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("%s, nenhuma acao registrada nesse periodo.", userName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Historico de acoes de %s:\n\n", userName))

	for _, e := range entries {
		timeStr := e.CreatedAt.Format("02/01 15:04")
		label := actionLabelsPT[e.Action]
		if label == "" {
			label = e.Action
		}
		line := fmt.Sprintf("  %s — %s", timeStr, label)
		if e.TargetUser != "" {
			line += fmt.Sprintf(" (agenda de %s)", e.TargetUser)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: Add action_log table to db.go migrate()**

In `bot/db.go`, the `action_log` table must be in the `migrate()` schema. Add after the `sent_reminders` table:

```sql
CREATE TABLE IF NOT EXISTS action_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    action      TEXT NOT NULL,
    target_user TEXT,
    details     TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run "TestLogAction|TestFormatAudit"
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add bot/audit.go bot/audit_test.go bot/db.go
git commit -m "feat: add audit log for all bot actions with query support"
```

---

## Task 17: Calendar Delegation (Cross-User Permissions)

**Files:**
- Create: `bot/permissions.go`, `bot/permissions_test.go`
- Modify: `bot/db.go` (add migration), `bot/orchestrator.go` (handle target_user), `bot/claude.go` (update prompt + IntentData), `bot/main.go` (add CLI commands)

- [ ] **Step 1: Write failing tests**

Create `bot/permissions_test.go`:

```go
package main

import (
	"testing"
)

func TestGrantAndCheckPermission(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "Andre", GoogleCalendarID: "andre@g.com", GoogleCredentials: "x"})
	db.CreateUser(&User{PhoneNumber: "222", Name: "Waldyr", GoogleCalendarID: "waldyr@g.com", GoogleCredentials: "x"})

	andre, _ := db.GetUserByPhone("111")
	waldyr, _ := db.GetUserByPhone("222")

	perm := NewPermissionManager(db)

	// Initially no permission
	allowed, err := perm.CanScheduleFor(waldyr.ID, andre.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor failed: %v", err)
	}
	if allowed {
		t.Fatal("expected no permission initially")
	}

	// Grant: Andre authorizes Waldyr
	err = perm.Grant(andre.ID, waldyr.ID)
	if err != nil {
		t.Fatalf("Grant failed: %v", err)
	}

	// Now Waldyr can schedule for Andre
	allowed, err = perm.CanScheduleFor(waldyr.ID, andre.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor failed: %v", err)
	}
	if !allowed {
		t.Fatal("expected permission after grant")
	}

	// But Andre cannot schedule for Waldyr (unidirectional)
	allowed, err = perm.CanScheduleFor(andre.ID, waldyr.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor failed: %v", err)
	}
	if allowed {
		t.Fatal("permission should be unidirectional")
	}

	// Revoke
	err = perm.Revoke(andre.ID, waldyr.ID)
	if err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	allowed, err = perm.CanScheduleFor(waldyr.ID, andre.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor failed: %v", err)
	}
	if allowed {
		t.Fatal("expected no permission after revoke")
	}
}

func TestListGranteesForUser(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "Andre", GoogleCalendarID: "a@g.com", GoogleCredentials: "x"})
	db.CreateUser(&User{PhoneNumber: "222", Name: "Waldyr", GoogleCalendarID: "w@g.com", GoogleCredentials: "x"})
	db.CreateUser(&User{PhoneNumber: "333", Name: "Giovanni", GoogleCalendarID: "g@g.com", GoogleCredentials: "x"})

	andre, _ := db.GetUserByPhone("111")
	waldyr, _ := db.GetUserByPhone("222")
	giovanni, _ := db.GetUserByPhone("333")

	perm := NewPermissionManager(db)
	perm.Grant(andre.ID, waldyr.ID)   // Andre authorizes Waldyr
	perm.Grant(giovanni.ID, waldyr.ID) // Giovanni authorizes Waldyr

	// Waldyr can schedule for Andre and Giovanni
	targets, err := perm.ListTargetsFor(waldyr.ID)
	if err != nil {
		t.Fatalf("ListTargetsFor failed: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}

func TestResolveTargetUser(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{PhoneNumber: "111", Name: "Andre", GoogleCalendarID: "a@g.com", GoogleCredentials: "x"})
	db.CreateUser(&User{PhoneNumber: "222", Name: "Waldyr", GoogleCalendarID: "w@g.com", GoogleCredentials: "x"})

	perm := NewPermissionManager(db)

	// Resolve by name
	user, err := perm.ResolveByName("Andre")
	if err != nil {
		t.Fatalf("ResolveByName failed: %v", err)
	}
	if user.PhoneNumber != "111" {
		t.Fatalf("expected phone 111, got %s", user.PhoneNumber)
	}

	// Not found
	_, err = perm.ResolveByName("Inexistente")
	if err == nil {
		t.Fatal("expected error for unknown name")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run TestGrant
```

Expected: FAIL — `PermissionManager` not defined.

- [ ] **Step 3: Implement permissions.go**

```go
// bot/permissions.go
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrTargetUserNotFound = errors.New("target user not found")

type PermissionManager struct {
	db *DB
}

func NewPermissionManager(db *DB) *PermissionManager {
	return &PermissionManager{db: db}
}

func (p *PermissionManager) Grant(grantorID, granteeID int64) error {
	_, err := p.db.conn.Exec(
		`INSERT OR IGNORE INTO calendar_permissions (grantor_id, grantee_id) VALUES (?, ?)`,
		grantorID, granteeID)
	return err
}

func (p *PermissionManager) Revoke(grantorID, granteeID int64) error {
	_, err := p.db.conn.Exec(
		`DELETE FROM calendar_permissions WHERE grantor_id = ? AND grantee_id = ?`,
		grantorID, granteeID)
	return err
}

// CanScheduleFor checks if grantee can create events on grantor's calendar.
func (p *PermissionManager) CanScheduleFor(granteeID, grantorID int64) (bool, error) {
	var count int
	err := p.db.conn.QueryRow(
		`SELECT COUNT(*) FROM calendar_permissions WHERE grantor_id = ? AND grantee_id = ?`,
		grantorID, granteeID).Scan(&count)
	return count > 0, err
}

// ListTargetsFor returns all users that grantee can schedule for.
func (p *PermissionManager) ListTargetsFor(granteeID int64) ([]User, error) {
	rows, err := p.db.conn.Query(
		`SELECT u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
		 u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
		 u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at
		 FROM calendar_permissions cp
		 JOIN users u ON u.id = cp.grantor_id
		 WHERE cp.grantee_id = ?`, granteeID)
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

// ResolveByName finds a user by name (case-insensitive).
func (p *PermissionManager) ResolveByName(name string) (*User, error) {
	u := &User{}
	err := p.db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE LOWER(name) = LOWER(?)`, name,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTargetUserNotFound
	}
	return u, err
}

func FormatAccessList(userName string, targets []User) string {
	if len(targets) == 0 {
		return fmt.Sprintf("%s nao tem permissao para agendar na agenda de ninguem.", userName)
	}

	var names []string
	for _, u := range targets {
		names = append(names, u.Name)
	}
	return fmt.Sprintf("%s pode agendar na agenda de: %s", userName, strings.Join(names, ", "))
}
```

- [ ] **Step 4: Add calendar_permissions table to db.go migrate()**

In `bot/db.go`, add to the `migrate()` schema after `sent_reminders`:

```sql
CREATE TABLE IF NOT EXISTS calendar_permissions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    grantor_id   INTEGER NOT NULL REFERENCES users(id),
    grantee_id   INTEGER NOT NULL REFERENCES users(id),
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(grantor_id, grantee_id)
);
```

- [ ] **Step 5: Update IntentData in claude.go**

Add field to `IntentData` struct:

```go
// Add to IntentData struct in claude.go
TargetUser string `json:"target_user,omitempty"`
```

Update `BuildIntentPrompt` to include the `target_user` instruction and `consultar_log` intent:

```go
func BuildIntentPrompt(userName, message string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e um assistente de agenda. Analise a mensagem do usuario %s e retorne APENAS um JSON valido.

Data/hora atual: %s

Intencoes possiveis:
- criar_evento: extraia title, date (YYYY-MM-DD), time (HH:MM), duration_minutes (default: 60). Se o usuario mencionar a agenda de outra pessoa, extraia target_user com o nome.
- consultar_agenda: extraia start_date (YYYY-MM-DD), end_date (YYYY-MM-DD)
- editar_evento: extraia search_query (texto para encontrar o evento), changes (objeto com campos a alterar)
- cancelar_evento: extraia search_query
- confirmar: o usuario esta confirmando uma acao pendente
- negar: o usuario esta negando uma acao pendente
- consultar_log: o usuario quer ver o historico de acoes. Extraia start_date, end_date.

Responda APENAS com JSON, sem markdown, sem explicacao:
{"intent": "...", "data": {...}, "confirmation_message": "mensagem amigavel para o usuario em portugues"}

Mensagem do usuario: %s`, userName, now, message)
}
```

- [ ] **Step 6: Update orchestrator.go to handle target_user and consultar_log**

Add to `Orchestrator` struct:

```go
type Orchestrator struct {
	claude        *ClaudeClient
	cal           *CalendarClient
	transcription *TranscriptionClient
	db            *DB
	cfg           *Config
	confirm       *ConfirmationManager
	perms         *PermissionManager
	audit         *AuditLog
}
```

Update `NewOrchestrator`:

```go
func NewOrchestrator(claude *ClaudeClient, cal *CalendarClient, transcription *TranscriptionClient, db *DB, cfg *Config) *Orchestrator {
	o := &Orchestrator{
		claude:        claude,
		cal:           cal,
		transcription: transcription,
		db:            db,
		cfg:           cfg,
		perms:         NewPermissionManager(db),
		audit:         NewAuditLog(db),
	}
	o.confirm = NewConfirmationManager(db, cal, cfg)
	return o
}
```

Update the `Process` method's `criar_evento` case to handle cross-user:

```go
case "criar_evento":
	if intent.Data.TargetUser != "" {
		return o.handleCrossUserCreate(ctx, user, intent)
	}
	o.audit.Log(user.ID, "criar_evento", "", intent.Data.Title)
	return o.confirm.CreatePending(user, intent.Data, intent.ConfirmationMessage)

// ... add new case:
case "consultar_log":
	return o.handleConsultarLog(ctx, user, intent)
```

Add new methods:

```go
func (o *Orchestrator) handleCrossUserCreate(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	targetUser, err := o.perms.ResolveByName(intent.Data.TargetUser)
	if err == ErrTargetUserNotFound {
		return fmt.Sprintf("Nao encontrei o usuario %s.", intent.Data.TargetUser), nil
	}
	if err != nil {
		return "", err
	}

	allowed, err := o.perms.CanScheduleFor(user.ID, targetUser.ID)
	if err != nil {
		return "", err
	}
	if !allowed {
		return fmt.Sprintf("Voce nao tem permissao para agendar na agenda de %s.", targetUser.Name), nil
	}

	// Store target info in confirmation for later execution
	intent.Data.TargetUser = targetUser.Name
	o.audit.Log(user.ID, "criar_evento", targetUser.Name, intent.Data.Title)
	return o.confirm.CreatePending(user, intent.Data, intent.ConfirmationMessage)
}

func (o *Orchestrator) handleConsultarLog(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	loc := time.Now().Location()
	startDate, err := time.ParseInLocation("2006-01-02", intent.Data.StartDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse start_date: %w", err)
	}
	endDate, err := time.ParseInLocation("2006-01-02", intent.Data.EndDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse end_date: %w", err)
	}
	endDate = endDate.Add(24*time.Hour - time.Second)

	entries, err := o.audit.Query(user.ID, startDate, endDate)
	if err != nil {
		return "", err
	}

	o.audit.Log(user.ID, "consultar_log", "", fmt.Sprintf("%s a %s", intent.Data.StartDate, intent.Data.EndDate))
	return FormatAuditLog(user.Name, entries), nil
}
```

- [ ] **Step 7: Update confirmation.go to handle cross-user event creation**

In `executeConfirmation`, when `data.TargetUser` is set, create the event on both calendars:

```go
func (cm *ConfirmationManager) executeConfirmation(user *User, pc *PendingConfirmation) (string, error) {
	var data IntentData
	if err := json.Unmarshal([]byte(pc.EventData), &data); err != nil {
		return "", fmt.Errorf("unmarshal event data: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, cm.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	startTime, err := time.ParseInLocation("2006-01-02 15:04", data.Date+" "+data.Time, time.Local)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}

	duration := time.Duration(data.DurationMinutes) * time.Minute
	if data.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}

	ev := CalendarEvent{
		Title: data.Title,
		Start: startTime,
		End:   startTime.Add(duration),
	}

	// Create on user's own calendar
	created, err := cm.cal.CreateEvent(nil, refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		return "", fmt.Errorf("create calendar event: %w", err)
	}

	// If cross-user, also create on target's calendar
	if data.TargetUser != "" {
		perm := NewPermissionManager(cm.db)
		targetUser, err := perm.ResolveByName(data.TargetUser)
		if err == nil {
			targetToken, err := Decrypt(targetUser.GoogleCredentials, cm.cfg.EncryptionKey)
			if err == nil {
				cm.cal.CreateEvent(nil, targetToken, targetUser.GoogleCalendarID, ev)
			}
		}
	}

	if err := cm.db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		return "", err
	}

	msg := FormatEventCreated(*created)
	if data.TargetUser != "" {
		msg += fmt.Sprintf("\n(Tambem adicionado na agenda de %s)", data.TargetUser)
	}
	return msg, nil
}
```

- [ ] **Step 8: Add CLI commands to main.go**

Add `grant-access`, `revoke-access`, `list-access` to the CLI switch in `main.go`:

```go
case "grant-access":
	grantAccess()
case "revoke-access":
	revokeAccess()
case "list-access":
	listAccess()
```

Implement:

```go
func grantAccess() {
	fs := flag.NewFlagSet("grant-access", flag.ExitOnError)
	from := fs.String("from", "", "Phone of grantor (calendar owner)")
	to := fs.String("to", "", "Phone of grantee (who gets access)")
	fs.Parse(os.Args[2:])

	if *from == "" || *to == "" {
		fmt.Println("Usage: bot grant-access --from=5511... --to=5522...")
		os.Exit(1)
	}

	db, err := NewDB("data/bot.db")
	if err != nil { log.Fatalf("DB error: %v", err) }
	defer db.Close()

	grantor, err := db.GetUserByPhone(*from)
	if err != nil { log.Fatalf("Grantor not found: %v", err) }
	grantee, err := db.GetUserByPhone(*to)
	if err != nil { log.Fatalf("Grantee not found: %v", err) }

	perm := NewPermissionManager(db)
	if err := perm.Grant(grantor.ID, grantee.ID); err != nil {
		log.Fatalf("Grant failed: %v", err)
	}

	audit := NewAuditLog(db)
	audit.Log(grantor.ID, "grant_access", grantee.Name, fmt.Sprintf("granted to %s", grantee.Name))

	fmt.Printf("%s agora pode agendar na agenda de %s\n", grantee.Name, grantor.Name)
}

func revokeAccess() {
	fs := flag.NewFlagSet("revoke-access", flag.ExitOnError)
	from := fs.String("from", "", "Phone of grantor")
	to := fs.String("to", "", "Phone of grantee")
	fs.Parse(os.Args[2:])

	if *from == "" || *to == "" {
		fmt.Println("Usage: bot revoke-access --from=5511... --to=5522...")
		os.Exit(1)
	}

	db, err := NewDB("data/bot.db")
	if err != nil { log.Fatalf("DB error: %v", err) }
	defer db.Close()

	grantor, err := db.GetUserByPhone(*from)
	if err != nil { log.Fatalf("Grantor not found: %v", err) }
	grantee, err := db.GetUserByPhone(*to)
	if err != nil { log.Fatalf("Grantee not found: %v", err) }

	perm := NewPermissionManager(db)
	if err := perm.Revoke(grantor.ID, grantee.ID); err != nil {
		log.Fatalf("Revoke failed: %v", err)
	}

	audit := NewAuditLog(db)
	audit.Log(grantor.ID, "revoke_access", grantee.Name, fmt.Sprintf("revoked from %s", grantee.Name))

	fmt.Printf("Acesso de %s a agenda de %s revogado\n", grantee.Name, grantor.Name)
}

func listAccess() {
	fs := flag.NewFlagSet("list-access", flag.ExitOnError)
	phone := fs.String("user", "", "Phone of user to check")
	fs.Parse(os.Args[2:])

	if *phone == "" {
		fmt.Println("Usage: bot list-access --user=5511...")
		os.Exit(1)
	}

	db, err := NewDB("data/bot.db")
	if err != nil { log.Fatalf("DB error: %v", err) }
	defer db.Close()

	user, err := db.GetUserByPhone(*phone)
	if err != nil { log.Fatalf("User not found: %v", err) }

	perm := NewPermissionManager(db)
	targets, err := perm.ListTargetsFor(user.ID)
	if err != nil { log.Fatalf("List failed: %v", err) }

	fmt.Println(FormatAccessList(user.Name, targets))
}
```

- [ ] **Step 9: Run all tests**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal
go test ./bot/ -v -run "TestGrant|TestList|TestResolve"
```

Expected: all PASS.

- [ ] **Step 10: Commit**

```bash
git add bot/permissions.go bot/permissions_test.go bot/audit.go bot/audit_test.go bot/claude.go bot/orchestrator.go bot/confirmation.go bot/db.go bot/main.go
git commit -m "feat: add cross-user calendar permissions and audit log"
```

---

## Task 18: Production Hardening

**Files:**
- Create: `bot/watchdog.go`, `terraform/modules/monitoring/main.tf`, `scripts/backup.sh`, `scripts/assistente-bot.service`

- [ ] **Step 1: Create WhatsApp connection watchdog**

```go
// bot/watchdog.go
package main

import (
	"log"
	"time"

	"go.mau.fi/whatsmeow"
)

type Watchdog struct {
	client  *whatsmeow.Client
	sendMsg func(phone, text string) error
	adminPhone string
	interval   time.Duration
}

func NewWatchdog(client *whatsmeow.Client, sendMsg func(phone, text string) error, adminPhone string) *Watchdog {
	return &Watchdog{
		client:     client,
		sendMsg:    sendMsg,
		adminPhone: adminPhone,
		interval:   5 * time.Minute,
	}
}

func (w *Watchdog) Start() {
	go func() {
		consecutiveFails := 0
		for {
			time.Sleep(w.interval)

			if !w.client.IsConnected() {
				consecutiveFails++
				log.Printf("Watchdog: WhatsApp disconnected (attempt %d)", consecutiveFails)

				err := w.client.Connect()
				if err != nil {
					log.Printf("Watchdog: reconnect failed: %v", err)
					if consecutiveFails >= 3 && w.adminPhone != "" {
						// Try to alert admin (may fail if disconnected)
						log.Printf("Watchdog: ALERT — 3 consecutive reconnect failures")
					}
					continue
				}

				log.Println("Watchdog: reconnected successfully")
				consecutiveFails = 0
			} else {
				consecutiveFails = 0
			}
		}
	}()
	log.Println("Watchdog started (interval: 5m)")
}
```

- [ ] **Step 2: Create systemd service file**

Create `scripts/assistente-bot.service`:

```ini
# scripts/assistente-bot.service
# Copy to /etc/systemd/system/assistente-bot.service on EC2

[Unit]
Description=Assistente Pessoal WhatsApp Bot
After=docker.service
Requires=docker.service

[Service]
Type=simple
WorkingDirectory=/opt/assistente
ExecStart=/usr/bin/docker compose up
ExecStop=/usr/bin/docker compose down
Restart=always
RestartSec=10
User=ec2-user

# Restart limits
StartLimitIntervalSec=300
StartLimitBurst=5

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=assistente-bot

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 3: Create SQLite backup script**

Create `scripts/backup.sh`:

```bash
#!/bin/bash
# scripts/backup.sh — Daily SQLite backup to S3
# Run via cron: 0 3 * * * /opt/assistente/scripts/backup.sh

set -euo pipefail

BACKUP_DIR="/tmp/assistente-backup"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
S3_BUCKET="${S3_BACKUP_BUCKET:-assistente-backups}"
DATA_DIR="/opt/assistente/data"

mkdir -p "$BACKUP_DIR"

# SQLite online backup (safe while bot is running)
sqlite3 "$DATA_DIR/bot.db" ".backup '$BACKUP_DIR/bot-$TIMESTAMP.db'"
sqlite3 "$DATA_DIR/whatsmeow.db" ".backup '$BACKUP_DIR/whatsmeow-$TIMESTAMP.db'"

# Compress
tar -czf "$BACKUP_DIR/backup-$TIMESTAMP.tar.gz" -C "$BACKUP_DIR" \
    "bot-$TIMESTAMP.db" "whatsmeow-$TIMESTAMP.db"

# Upload to S3
aws s3 cp "$BACKUP_DIR/backup-$TIMESTAMP.tar.gz" \
    "s3://$S3_BUCKET/backups/backup-$TIMESTAMP.tar.gz"

# Cleanup local
rm -rf "$BACKUP_DIR"

# Cleanup old S3 backups (keep 30 days)
aws s3 ls "s3://$S3_BUCKET/backups/" | \
    awk '{print $4}' | \
    sort | \
    head -n -30 | \
    xargs -I {} aws s3 rm "s3://$S3_BUCKET/backups/{}"

echo "Backup completed: backup-$TIMESTAMP.tar.gz"
```

- [ ] **Step 4: Add Docker log rotation to docker-compose.yml**

Update both services in `docker-compose.yml` to add logging config:

```yaml
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "5"
```

- [ ] **Step 5: Add CloudWatch and S3 to Terraform**

Add to `terraform/main.tf`:

```hcl
# S3 bucket for backups
resource "aws_s3_bucket" "backups" {
  bucket_prefix = "assistente-backups-"
  force_destroy = true

  tags = {
    Name = "assistente-backups"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "backups" {
  bucket = aws_s3_bucket.backups.id

  rule {
    id     = "expire-old-backups"
    status = "Enabled"
    expiration {
      days = 30
    }
  }
}

# IAM role for EC2 (S3 access + CloudWatch)
resource "aws_iam_role" "bot" {
  name_prefix = "assistente-bot-"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "bot" {
  role = aws_iam_role.bot.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["s3:PutObject", "s3:GetObject", "s3:ListBucket", "s3:DeleteObject"]
        Resource = [aws_s3_bucket.backups.arn, "${aws_s3_bucket.backups.arn}/*"]
      },
      {
        Effect = "Allow"
        Action = ["cloudwatch:PutMetricData", "logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_instance_profile" "bot" {
  name_prefix = "assistente-bot-"
  role        = aws_iam_role.bot.name
}

# EC2 Auto Recovery
resource "aws_cloudwatch_metric_alarm" "auto_recovery" {
  alarm_name          = "assistente-bot-auto-recovery"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "StatusCheckFailed_System"
  namespace           = "AWS/EC2"
  period              = 60
  statistic           = "Maximum"
  threshold           = 0

  dimensions = {
    InstanceId = aws_instance.bot.id
  }

  alarm_actions = ["arn:aws:automate:${var.aws_region}:ec2:recover"]
}

# SNS topic for alerts
resource "aws_sns_topic" "alerts" {
  name = "assistente-bot-alerts"
}

resource "aws_sns_topic_subscription" "email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

# High CPU alarm
resource "aws_cloudwatch_metric_alarm" "high_cpu" {
  alarm_name          = "assistente-bot-high-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/EC2"
  period              = 300
  statistic           = "Average"
  threshold           = 80

  dimensions = {
    InstanceId = aws_instance.bot.id
  }

  alarm_actions = [aws_sns_topic.alerts.arn]
}
```

Update EC2 instance to use IAM profile:

```hcl
# Add to aws_instance.bot:
  iam_instance_profile = aws_iam_instance_profile.bot.name
```

Add variable:

```hcl
# Add to variables.tf:
variable "alert_email" {
  description = "Email for CloudWatch alerts"
  type        = string
}
```

Add output:

```hcl
# Add to outputs.tf:
output "backup_bucket" {
  description = "S3 bucket for backups"
  value       = aws_s3_bucket.backups.id
}
```

- [ ] **Step 6: Update cloud-init.yaml for backup cron and CloudWatch agent**

Add to `runcmd` in `terraform/cloud-init.yaml`:

```yaml
  # Install CloudWatch agent
  - yum install -y amazon-cloudwatch-agent
  - |
    cat > /opt/aws/amazon-cloudwatch-agent/etc/amazon-cloudwatch-agent.json << 'CWEOF'
    {
      "metrics": {
        "metrics_collected": {
          "disk": { "measurement": ["used_percent"], "resources": ["*"] },
          "mem": { "measurement": ["mem_used_percent"] }
        }
      },
      "logs": {
        "logs_collected": {
          "files": {
            "collect_list": [{
              "file_path": "/var/log/messages",
              "log_group_name": "assistente-bot",
              "log_stream_name": "{instance_id}/syslog"
            }]
          }
        }
      }
    }
    CWEOF
  - systemctl enable amazon-cloudwatch-agent
  - systemctl start amazon-cloudwatch-agent

  # Install sqlite3 for backups
  - yum install -y sqlite

  # Setup backup cron
  - echo "0 3 * * * ec2-user /opt/assistente/scripts/backup.sh >> /var/log/assistente-backup.log 2>&1" > /etc/cron.d/assistente-backup

  # Enable systemd service
  - cp /opt/assistente/scripts/assistente-bot.service /etc/systemd/system/
  - systemctl daemon-reload
  - systemctl enable assistente-bot
```

- [ ] **Step 7: Wire watchdog into main.go**

Add after scheduler start in `runBot()`:

```go
// Start watchdog
adminPhone := os.Getenv("ADMIN_PHONE")
watchdog := NewWatchdog(waClient, handler.SendTextToPhone, adminPhone)
watchdog.Start()
```

- [ ] **Step 8: Verify it compiles**

```bash
cd /Users/giovanni/Documents/GitHub/assistente_pessoal/bot
go build -o /dev/null .
```

Expected: exits 0.

- [ ] **Step 9: Commit**

```bash
git add bot/watchdog.go scripts/ docker-compose.yml terraform/
git commit -m "feat: add production hardening — watchdog, backups, monitoring, systemd"
```

---

## Summary of Tasks

| # | Task | Focus |
|---|---|---|
| 1 | Project scaffolding | Go module, config, CLI skeleton, .env, .gitignore |
| 2 | Crypto module | AES-256-GCM encrypt/decrypt + tests |
| 3 | Database layer | SQLite schema, user/confirmation CRUD + tests |
| 4 | Claude API client | Intent extraction prompt, JSON parsing + tests |
| 5 | Google Calendar client | OAuth2, CRUD events, find/list |
| 6 | Event formatter | WhatsApp-friendly message formatting + tests |
| 7 | Transcription client | HTTP client to call Python service |
| 8 | Handler + Orchestrator | WhatsApp message routing, pipeline, confirmations |
| 9 | Scheduler | Cron: reminders, daily/weekly summaries, auto-confirm |
| 10 | Main entry point | Wire everything: whatsmeow, scheduler, OAuth, CLI |
| 11 | Transcription API | Python FastAPI + AssemblyAI + Dockerfile |
| 12 | Docker Compose | Dockerfiles, compose config |
| 13 | Terraform IaC | EC2, SG, EIP, cloud-init |
| 14 | Integration tests | End-to-end confirmation flow tests |
| 15 | CLAUDE.md | Project conventions and dev docs |
| 16 | Audit log | Action logging + WhatsApp query + tests |
| 17 | Calendar delegation | Cross-user permissions, CLI management + tests |
| 18 | Production hardening | Watchdog, systemd, backups, CloudWatch, auto-recovery |
