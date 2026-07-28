package main

import (
	"testing"
	"time"
)

// insertConvAt grava uma row de conversation_history com created_at explícito
// (UTC, layout do SQLite) — precisamos de carimbos controlados pra testar a
// marca d'água, já que AddConversationMessage usa CURRENT_TIMESTAMP.
func insertConvAt(t *testing.T, db *DB, userID int64, role, content, createdAt string) {
	t.Helper()
	_, err := db.conn.Exec(
		`INSERT INTO conversation_history (user_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		userID, role, content, createdAt)
	if err != nil {
		t.Fatalf("insert conversation_history: %v", err)
	}
}

func TestLastInboundAt_ReturnsMaxUserMessage(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	insertConvAt(t, db, u.ID, "user", "oi", "2026-07-27 10:00:00")
	insertConvAt(t, db, u.ID, "assistant", "olá!", "2026-07-27 11:00:00") // mais recente, mas assistant
	insertConvAt(t, db, u.ID, "user", "tudo bem?", "2026-07-27 10:30:00") // MAX das role='user'

	got, ok, err := db.LastInboundAt(u.ID)
	if err != nil {
		t.Fatalf("LastInboundAt: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, quero true")
	}
	want := time.Date(2026, 7, 27, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("marca d'água = %s, quero %s (deve ignorar a msg do assistant)", got, want)
	}
}

func TestLastInboundAt_NoMessages(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	_, ok, err := db.LastInboundAt(u.ID)
	if err != nil {
		t.Fatalf("LastInboundAt: %v", err)
	}
	if ok {
		t.Errorf("ok = true, quero false (usuário sem mensagens)")
	}
}

func TestLastInboundAt_OnlyAssistantMessages(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	insertConvAt(t, db, u.ID, "assistant", "lembrete", "2026-07-27 09:00:00")

	_, ok, err := db.LastInboundAt(u.ID)
	if err != nil {
		t.Fatalf("LastInboundAt: %v", err)
	}
	if ok {
		t.Errorf("ok = true, quero false (só há mensagens do assistant)")
	}
}
