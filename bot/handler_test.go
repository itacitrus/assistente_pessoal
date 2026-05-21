package main

import "testing"

// persistOutbound centraliza no transporte a gravacao de TODA mensagem enviada
// ao cliente em conversation_history. Esses testes travam o comportamento que
// faltava: o bug original era um lembrete de medicacao enviado sem entrar no
// historico, fazendo o LLM perder o contexto da propria fala ao receber a
// resposta do usuario.

func lastAssistantMessage(t *testing.T, db *DB, userID int64) (string, bool) {
	t.Helper()
	msgs, err := db.GetConversationHistory(userID, 30)
	if err != nil {
		t.Fatalf("GetConversationHistory: %v", err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return msgs[i].Content, true
		}
	}
	return "", false
}

func TestPersistOutbound_SavesAssistantTurn(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db) // PhoneNumber 5511999999999
	h := &Handler{db: db}

	h.persistOutbound(u.PhoneNumber, "Hora do Aradois, pode confirmar quando tomar?")

	got, ok := lastAssistantMessage(t, db, u.ID)
	if !ok {
		t.Fatal("expected an assistant turn in conversation_history, found none")
	}
	if got != "Hora do Aradois, pode confirmar quando tomar?" {
		t.Fatalf("unexpected stored content: %q", got)
	}
}

func TestPersistOutbound_MatchesBRNinthDigitVariant(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db) // stored as 5511999999999 (with 9th digit)
	h := &Handler{db: db}

	// WhatsApp delivered the number without the leading 9 — normalizeBRPhone
	// must still resolve it to the same user.
	h.persistOutbound("551199999999", "lembrete sem o nono digito")

	if _, ok := lastAssistantMessage(t, db, u.ID); !ok {
		t.Fatal("expected the message to resolve to the user via BR 9th-digit variant")
	}
}

func TestPersistOutbound_UnknownPhoneIsIgnored(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)
	h := &Handler{db: db}

	h.persistOutbound("5521888888888", "mensagem a numero sem cadastro")

	if _, ok := lastAssistantMessage(t, db, u.ID); ok {
		t.Fatal("message to an unregistered number must not be persisted to any user")
	}
}

func TestPersistOutbound_EmptyTextIsIgnored(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)
	h := &Handler{db: db}

	h.persistOutbound(u.PhoneNumber, "")

	if _, ok := lastAssistantMessage(t, db, u.ID); ok {
		t.Fatal("empty outbound text must not create a history row")
	}
}
