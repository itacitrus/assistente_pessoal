package main

import (
	"strings"
	"testing"
	"time"
)

// A antiga appendCompanionContinuationPart virou o campo ContinuationOK do
// TurnContext, renderizado dentro do bloco único [AGORA] (renderAgoraPart).
// Estes testes cobrem o mesmo comportamento pela nova costura.
func TestTurnContextContinuation(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}

	u := &User{PhoneNumber: "5511988887777", Name: "Dona Maria", Type: UserTypeIdoso}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// 1. Bot nunca falou -> sem continuação.
	tc := a.newTurnContext(u, time.Now())
	if tc.ContinuationOK {
		t.Fatal("sem fala do bot: ContinuationOK deveria ser false")
	}

	// 2. Bot acabou de falar -> continuação ativa e renderizada no [AGORA].
	if err := db.AddConversationMessage(u.ID, "assistant", "Bom dia, Dona Maria!"); err != nil {
		t.Fatalf("AddConversationMessage: %v", err)
	}
	tc = a.newTurnContext(u, time.Now())
	if !tc.ContinuationOK {
		t.Fatal("fala recente do bot: ContinuationOK deveria ser true")
	}
	agora := renderAgoraPart(tc, firstName(u.Name))
	if !strings.Contains(agora, "NÃO recomece com saudação") {
		t.Errorf("[AGORA] deveria carregar o aviso de continuação, got %q", agora)
	}
	if !strings.Contains(agora, "Dona") {
		t.Errorf("[AGORA] deveria citar o primeiro nome, got %q", agora)
	}

	// 3. Fala antiga (além da janela) -> sem continuação.
	if _, err := db.conn.Exec(
		`UPDATE conversation_history SET created_at = datetime('now','-30 minutes')
		 WHERE user_id = ? AND role = 'assistant'`, u.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	tc = a.newTurnContext(u, time.Now())
	if tc.ContinuationOK {
		t.Fatal("fala antiga: ContinuationOK deveria ser false")
	}

	// 4. Não-idoso -> nunca computa continuação nem recebe [AGORA].
	c := &User{PhoneNumber: "5511977776666", Name: "Chefe", Type: UserTypeComum}
	if err := db.CreateUser(c); err != nil {
		t.Fatalf("CreateUser comum: %v", err)
	}
	db.AddConversationMessage(c.ID, "assistant", "ok")
	tc = a.newTurnContext(c, time.Now())
	if tc.ContinuationOK {
		t.Fatal("não-idoso: ContinuationOK deveria ser false")
	}
	if parts := a.appendCompanionAgoraPart(nil, c, tc); len(parts) != 0 {
		t.Fatal("não-idoso não deveria receber [AGORA]")
	}
}
