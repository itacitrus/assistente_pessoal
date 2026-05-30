package main

import (
	"strings"
	"testing"
)

func TestAppendCompanionContinuationPart(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}

	u := &User{PhoneNumber: "5511988887777", Name: "Dona Maria", Type: UserTypeIdoso}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 1. Bot nunca falou -> nao injeta.
	if got := a.appendCompanionContinuationPart(nil, u); len(got) != 0 {
		t.Fatalf("sem fala do bot: esperava 0 parts, got %d", len(got))
	}

	// 2. Bot acabou de falar -> injeta marcador de continuacao.
	if err := db.AddConversationMessage(u.ID, "assistant", "Bom dia, Dona Maria!"); err != nil {
		t.Fatalf("AddConversationMessage: %v", err)
	}
	got := a.appendCompanionContinuationPart(nil, u)
	if len(got) != 1 {
		t.Fatalf("continuacao imediata: esperava 1 part, got %d", len(got))
	}
	if !strings.Contains(got[0].Text, "CONTINUAÇÃO") {
		t.Errorf("part sem marcador de continuacao: %q", got[0].Text)
	}

	// 3. Fala antiga (backdated alem da janela) -> nao injeta.
	if _, err := db.conn.Exec(
		`UPDATE conversation_history SET created_at = datetime('now','-30 minutes')
		 WHERE user_id = ? AND role = 'assistant'`, u.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if got := a.appendCompanionContinuationPart(nil, u); len(got) != 0 {
		t.Fatalf("fala antiga: esperava 0 parts, got %d", len(got))
	}

	// 4. Nao-idoso -> nunca injeta (mesmo com fala recente).
	c := &User{PhoneNumber: "5511977776666", Name: "Chefe", Type: UserTypeComum}
	if err := db.CreateUser(c); err != nil {
		t.Fatalf("CreateUser comum: %v", err)
	}
	db.AddConversationMessage(c.ID, "assistant", "ok")
	if got := a.appendCompanionContinuationPart(nil, c); len(got) != 0 {
		t.Fatalf("nao-idoso: esperava 0 parts, got %d", len(got))
	}
}
