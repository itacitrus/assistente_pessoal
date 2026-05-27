package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// newSalesTestAgent monta um Agent minimo para exercitar o provisionamento
// (sem chamar a LLM): so db, sendMsg e audit sao tocados nesse caminho.
func newSalesTestAgent(db *DB, sendMsg func(phone, text string) error) *Agent {
	return &Agent{db: db, sendMsg: sendMsg}
}

func TestProvisionLeadAccountCreatesUserAndSendsMagicLink(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511988887777"
	_ = db.UpsertLead(phone, "Kenya Silva")

	var sentTo, sentMsg string
	agent := newSalesTestAgent(db, func(p, m string) error { sentTo, sentMsg = p, m; return nil })

	result := agent.provisionLeadAccount(context.Background(), phone, "Kenya Silva")

	user, err := db.GetUserByPhone(phone)
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.Name != "Kenya Silva" {
		t.Errorf("name: got %q want %q", user.Name, "Kenya Silva")
	}
	// Self-signup nasce como titular (type comum, o default).
	if user.Type != UserTypeComum {
		t.Errorf("type: got %q want %q", user.Type, UserTypeComum)
	}
	lead, _ := db.GetLead(phone)
	if lead.Status != LeadStatusConverted {
		t.Errorf("lead status: got %q want converted", lead.Status)
	}
	if sentTo != phone {
		t.Errorf("welcome sent to %q want %q", sentTo, phone)
	}
	if !strings.Contains(sentMsg, "/auth/verify?token=") {
		t.Errorf("welcome missing magic link: %q", sentMsg)
	}
	if !strings.Contains(result, "criada") {
		t.Errorf("tool result should confirm creation, got %q", result)
	}
}

func TestProvisionLeadAccountIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511988887778"

	calls := 0
	agent := newSalesTestAgent(db, func(p, m string) error { calls++; return nil })

	_ = agent.provisionLeadAccount(context.Background(), phone, "Kenya")
	first, err := db.GetUserByPhone(phone)
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	result := agent.provisionLeadAccount(context.Background(), phone, "Kenya De Novo")
	if !strings.Contains(strings.ToLower(result), "já tem") {
		t.Errorf("second call should report existing account, got %q", result)
	}
	second, _ := db.GetUserByPhone(phone)
	if second.ID != first.ID {
		t.Errorf("account duplicated: ids %d vs %d", first.ID, second.ID)
	}
	if second.Name != "Kenya" {
		t.Errorf("name should not be overwritten: got %q", second.Name)
	}
}

func TestProvisionLeadAccountIdempotentAcrossPhoneVariants(t *testing.T) {
	db := setupTestDB(t)
	// Cadastra com 9o digito; segunda tentativa vem sem o 9 (variante WhatsApp).
	const with9 = "5511988887779"
	const without9 = "551188887779"

	agent := newSalesTestAgent(db, func(p, m string) error { return nil })
	_ = agent.provisionLeadAccount(context.Background(), with9, "Kenya")

	result := agent.provisionLeadAccount(context.Background(), without9, "Kenya")
	if !strings.Contains(strings.ToLower(result), "já tem") {
		t.Errorf("variant should be recognized as existing, got %q", result)
	}
}

func TestProvisionLeadAccountEmptyNameAsksForIt(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511988887780"
	agent := newSalesTestAgent(db, func(p, m string) error { return nil })

	result := agent.provisionLeadAccount(context.Background(), phone, "   ")
	if _, err := db.GetUserByPhone(phone); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("no account should be created without a name")
	}
	if !strings.Contains(strings.ToLower(result), "nome") {
		t.Errorf("result should ask for name, got %q", result)
	}
}

func TestProvisionLeadAccountWelcomeSendFailureStillCreates(t *testing.T) {
	db := setupTestDB(t)
	const phone = "5511988887781"
	agent := newSalesTestAgent(db, func(p, m string) error { return errors.New("whatsapp down") })

	result := agent.provisionLeadAccount(context.Background(), phone, "Kenya")
	if _, err := db.GetUserByPhone(phone); err != nil {
		t.Fatalf("account should be created even if welcome send fails: %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "criada") {
		t.Errorf("result should confirm creation, got %q", result)
	}
	if !strings.Contains(strings.ToLower(result), "site") {
		t.Errorf("result should guide to site login when link send fails, got %q", result)
	}
}
