package main

import (
	"strings"
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
	if !strings.Contains(result, "Criou evento") {
		t.Fatalf("should contain translated action, got: %s", result)
	}
}
