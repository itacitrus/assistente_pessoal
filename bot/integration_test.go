package main

import (
	"testing"
	"time"
)

func TestConfirmationFlow(t *testing.T) {
	db := setupTestDB(t)
	encKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	fakeToken, _ := Encrypt("fake-refresh-token", encKey)
	db.CreateUser(&User{
		PhoneNumber:        "5511999999999",
		Name:               "Waldyr",
		GoogleCalendarID:   "waldyr@gmail.com",
		GoogleCredentials:  fakeToken,
		AutoConfirmTimeout: "2h",
	})

	user, _ := db.GetUserByPhone("5511999999999")

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

	pc, err := db.GetPendingConfirmation(user.ID)
	if err != nil {
		t.Fatalf("GetPendingConfirmation failed: %v", err)
	}
	if pc.Status != "pending" {
		t.Fatalf("expected status pending, got %s", pc.Status)
	}

	denyMsg, err := cm.Deny(user)
	if err != nil {
		t.Fatalf("Deny failed: %v", err)
	}
	if denyMsg == "" {
		t.Fatal("expected deny message")
	}

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

	expired, err := db.GetExpiredPendingConfirmations(user.ID, 0*time.Second)
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
