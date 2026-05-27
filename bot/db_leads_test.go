package main

import (
	"testing"
	"time"
)

func TestUpsertLeadDoesNotOverwriteNameGuess(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511999990000"

	if err := db.UpsertLead(phone, "Kenya"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Segundo upsert com palpite diferente NAO deve sobrescrever o primeiro.
	if err := db.UpsertLead(phone, "Outro Nome"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	lead, err := db.GetLead(phone)
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if lead.NameGuess != "Kenya" {
		t.Errorf("name_guess overwritten: got %q want %q", lead.NameGuess, "Kenya")
	}
	if lead.Status != LeadStatusChatting {
		t.Errorf("status: got %q want %q", lead.Status, LeadStatusChatting)
	}
}

func TestUpsertLeadFillsEmptyNameGuessLater(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511999990001"

	if err := db.UpsertLead(phone, ""); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := db.UpsertLead(phone, "Kenya"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	lead, err := db.GetLead(phone)
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if lead.NameGuess != "Kenya" {
		t.Errorf("name_guess not filled: got %q want %q", lead.NameGuess, "Kenya")
	}
}

func TestLeadMessagesOrderAndPrune(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511999990002"
	if err := db.UpsertLead(phone, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Insere 55 turnos; o prune deve manter so os ultimos 50.
	for i := 0; i < 55; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := db.AddLeadMessage(phone, role, msgContent(i)); err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}
	msgs, err := db.GetLeadMessages(phone, 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 50 {
		t.Fatalf("expected 50 messages after prune, got %d", len(msgs))
	}
	// Ordem cronologica (mais antigo primeiro): o primeiro retido eh o turno 5.
	if msgs[0].Content != msgContent(5) {
		t.Errorf("oldest retained: got %q want %q", msgs[0].Content, msgContent(5))
	}
	if msgs[len(msgs)-1].Content != msgContent(54) {
		t.Errorf("newest: got %q want %q", msgs[len(msgs)-1].Content, msgContent(54))
	}
}

func TestCountLeadMessagesSince(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511999990003"
	if err := db.UpsertLead(phone, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.AddLeadMessage(phone, "user", "oi"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	n, err := db.CountLeadMessagesSince(phone, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("count recent: got %d want 3", n)
	}
	// Janela no futuro: nada conta.
	n, err = db.CountLeadMessagesSince(phone, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("count future: %v", err)
	}
	if n != 0 {
		t.Errorf("count future: got %d want 0", n)
	}
}

func TestMarkLeadConverted(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511999990004"
	if err := db.UpsertLead(phone, "Kenya"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := db.MarkLeadConverted(phone); err != nil {
		t.Fatalf("mark converted: %v", err)
	}
	lead, err := db.GetLead(phone)
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if lead.Status != LeadStatusConverted {
		t.Errorf("status: got %q want %q", lead.Status, LeadStatusConverted)
	}
}

func msgContent(i int) string {
	return "msg-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
