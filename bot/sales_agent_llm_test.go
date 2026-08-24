package main

import (
	"context"
	"strings"
	"testing"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

func salesAgent(t *testing.T) *Agent {
	t.Helper()
	db := setupTestDB(t)
	return &Agent{db: db, cfg: &Config{}, audit: NewAuditLog(db)}
}

func TestSalesAgentUsaProvider(t *testing.T) {
	a := salesAgent(t)
	fake := &fakeChat{name: "sales", reply: "posso te ajudar a cuidar de quem voce ama"}
	a.salesChat = fake

	out, err := a.RunSalesAgent(context.Background(), "5561911110000", "Ana", "quanto custa?")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls == 0 {
		t.Fatal("sales nao passou pelo ChatProvider")
	}
	if out == "" {
		t.Fatal("resposta vazia")
	}
}

// Sem salesChat, cai no operacional em vez de quebrar o atendimento do lead.
func TestSalesAgentFallbackProChatOperacional(t *testing.T) {
	a := salesAgent(t)
	a.salesChat = nil
	op := &fakeChat{name: "op", reply: "oi!"}
	a.chat = op

	if _, err := a.RunSalesAgent(context.Background(), "5561911110001", "Ana", "oi"); err != nil {
		t.Fatal(err)
	}
	if op.calls == 0 {
		t.Fatal("sem salesChat deveria cair no chat operacional")
	}
}

func TestSalesAgentSemProviderDaErroClaro(t *testing.T) {
	a := salesAgent(t)
	a.salesChat, a.chat = nil, nil
	_, err := a.RunSalesAgent(context.Background(), "5561911110002", "Ana", "oi")
	if err == nil || !strings.Contains(err.Error(), "nenhum ChatProvider") {
		t.Fatalf("erro pouco claro: %v", err)
	}
}

// O loop de vendas nao passa pelo output-guard (nao tem TurnContext nem
// User), entao o envelope de markdown tem que ser aplicado ali mesmo.
func TestSalesAgentSanitizaMarkdown(t *testing.T) {
	a := salesAgent(t)
	a.salesChat = &fakeChat{name: "sales", reply: "O Zello custa **R$ 49** por mes"}

	out, err := a.RunSalesAgent(context.Background(), "5561911110003", "Ana", "quanto custa?")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "**") {
		t.Fatalf("markdown vazou pro WhatsApp: %q", out)
	}
}

func TestBuildLeadMessagesComecaComUser(t *testing.T) {
	msgs := buildLeadMessages([]LeadMessage{{Role: "assistant", Content: "oi, tudo bem?"}})
	if len(msgs) == 0 || msgs[0].Role != llm.RoleUser {
		t.Fatalf("conversa tem que comecar com role=user: %+v", msgs)
	}
}

func TestBuildLeadMessagesVazioGeraPlaceholder(t *testing.T) {
	msgs := buildLeadMessages(nil)
	if len(msgs) != 1 || msgs[0].Role != llm.RoleUser {
		t.Fatalf("historico vazio deveria virar um placeholder user: %+v", msgs)
	}
}

func TestSalesAgentGravaUsoDeToken(t *testing.T) {
	a := salesAgent(t)
	a.salesChat = &fakeChat{name: "deepseek", reply: "oi"}

	if _, err := a.RunSalesAgent(context.Background(), "5561911110004", "Ana", "oi"); err != nil {
		t.Fatal(err)
	}
	sums, err := a.db.SumLLMUsageSince(timeMinusHour())
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) == 0 {
		t.Fatal("consumo do agente de vendas nao foi contabilizado")
	}
}
