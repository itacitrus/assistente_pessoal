package main

import (
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

	expired, err := db.GetExpiredPendingConfirmations(user.ID, 0*time.Second)
	if err != nil {
		t.Fatalf("GetExpiredPendingConfirmations failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
}
