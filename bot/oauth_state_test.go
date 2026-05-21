package main

import (
	"errors"
	"testing"
	"time"
)

func makeOAuthStateUser(t *testing.T, db *DB, phone string) *User {
	t.Helper()
	u := &User{
		PhoneNumber:        phone,
		Name:               "Teste",
		GoogleCalendarID:   phone + "@gmail.com",
		DailySummaryTime:   "07:00",
		WeeklySummaryDay:   "sunday",
		WeeklySummaryTime:  "20:00",
		ReminderBefore:     "1h",
		AutoConfirmTimeout: "2h",
	}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func TestOAuthState_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	u := makeOAuthStateUser(t, db, "5511900000001")

	token, err := db.CreateOAuthState(u.ID, oauthStateTTL)
	if err != nil {
		t.Fatalf("CreateOAuthState: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	gotUser, err := db.ConsumeOAuthState(token)
	if err != nil {
		t.Fatalf("ConsumeOAuthState: %v", err)
	}
	if gotUser != u.ID {
		t.Fatalf("user mismatch: got %d, want %d", gotUser, u.ID)
	}
}

func TestOAuthState_SingleUse(t *testing.T) {
	db := setupTestDB(t)
	u := makeOAuthStateUser(t, db, "5511900000002")

	token, _ := db.CreateOAuthState(u.ID, oauthStateTTL)
	if _, err := db.ConsumeOAuthState(token); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	// Segundo resgate do mesmo token deve falhar (single-use).
	if _, err := db.ConsumeOAuthState(token); !errors.Is(err, ErrOAuthStateUsed) {
		t.Fatalf("second consume err = %v, want ErrOAuthStateUsed", err)
	}
}

func TestOAuthState_Expired(t *testing.T) {
	db := setupTestDB(t)
	u := makeOAuthStateUser(t, db, "5511900000003")

	// TTL negativo => ja nasce expirado.
	token, _ := db.CreateOAuthState(u.ID, -time.Minute)
	if _, err := db.ConsumeOAuthState(token); !errors.Is(err, ErrOAuthStateExpired) {
		t.Fatalf("consume err = %v, want ErrOAuthStateExpired", err)
	}
}

func TestOAuthState_NotFound(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.ConsumeOAuthState("nao-existe"); !errors.Is(err, ErrOAuthStateNotFound) {
		t.Fatalf("consume err = %v, want ErrOAuthStateNotFound", err)
	}
}

func TestOAuthState_BindsToCorrectUser(t *testing.T) {
	db := setupTestDB(t)
	a := makeOAuthStateUser(t, db, "5511900000004")
	b := makeOAuthStateUser(t, db, "5511900000005")

	tokenA, _ := db.CreateOAuthState(a.ID, oauthStateTTL)
	tokenB, _ := db.CreateOAuthState(b.ID, oauthStateTTL)

	gotB, err := db.ConsumeOAuthState(tokenB)
	if err != nil || gotB != b.ID {
		t.Fatalf("tokenB resolved to %d (err=%v), want %d", gotB, err, b.ID)
	}
	gotA, err := db.ConsumeOAuthState(tokenA)
	if err != nil || gotA != a.ID {
		t.Fatalf("tokenA resolved to %d (err=%v), want %d", gotA, err, a.ID)
	}
}
