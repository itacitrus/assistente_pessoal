package main

import (
	"context"
	"strings"
	"testing"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// convergenceAgent monta um Agent com DB real e providers fake.
func convergenceAgent(t *testing.T) *Agent {
	t.Helper()
	db := setupTestDB(t)
	return &Agent{
		db:             db,
		cfg:            &Config{OutputGuardMode: "log"},
		perms:          NewPermissionManager(db),
		audit:          NewAuditLog(db),
		snapshotWriter: noopSnapshotWriter{},
	}
}

// flattenLLMMessages junta todo o texto das mensagens pra assercao.
func flattenLLMMessages(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		for _, c := range m.Content {
			sb.WriteString(c.Text)
			sb.WriteString(" ")
		}
	}
	return sb.String()
}

// O caminho operacional tinha que passar a usar a abstracao — antes falava
// direto com o SDK Anthropic, e por isso nao dava pra trocar de provider.
func TestRunUsaProviderInjetado(t *testing.T) {
	a := convergenceAgent(t)
	fake := &fakeChat{name: "op", reply: "tudo certo por aqui"}
	a.chat = fake
	u := seedUser(t, a.db, "5561910000001", "Ana", "")

	out, err := a.Run(context.Background(), u, "oi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls == 0 {
		t.Fatal("Run nao passou pelo ChatProvider injetado")
	}
	if out != "tudo certo por aqui" {
		t.Fatalf("resposta = %q", out)
	}
}

// Usuario com superficie de saude vai pro provider clinico MESMO nao sendo
// idoso — e o caso do filho que cadastra o remedio da mae.
func TestRunAdultoComDependenteVaiProClinico(t *testing.T) {
	a := convergenceAgent(t)
	op := &fakeChat{name: "op", reply: "op respondeu"}
	cl := &fakeChat{name: "clinico", reply: "clinico respondeu"}
	a.chat, a.companionChat = op, cl

	guardian := seedUser(t, a.db, "5561910000002", "Filho", "")
	dependent := seedUser(t, a.db, "5561910000003", "Mae", UserTypeIdoso)
	if _, err := a.db.LinkFamily(guardian.ID, dependent.ID, "filho"); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Run(context.Background(), guardian, "oi", nil); err != nil {
		t.Fatal(err)
	}
	if cl.calls == 0 || op.calls != 0 {
		t.Fatalf("roteamento errado: clinico=%d operacional=%d", cl.calls, op.calls)
	}
}

func TestRunUsuarioSemSaudeVaiProOperacional(t *testing.T) {
	a := convergenceAgent(t)
	op := &fakeChat{name: "op", reply: "op"}
	cl := &fakeChat{name: "clinico", reply: "clinico"}
	a.chat, a.companionChat = op, cl
	u := seedUser(t, a.db, "5561910000004", "Ana", "")

	if _, err := a.Run(context.Background(), u, "que horas e minha reuniao", nil); err != nil {
		t.Fatal(err)
	}
	if op.calls == 0 || cl.calls != 0 {
		t.Fatalf("roteamento errado: operacional=%d clinico=%d", op.calls, cl.calls)
	}
}

// Provider SEM vision recebe a imagem como texto pre-descrito — o modelo de
// chat da DeepSeek recusa imagem com HTTP 400.
func TestRunImagemViraDescricaoQuandoProviderNaoTemVision(t *testing.T) {
	a := convergenceAgent(t)
	fake := &fakeChat{name: "op", reply: "vi a foto", vision: false}
	a.chat = fake
	vis := &fakeVision{desc: "uma caixa de remedio"}
	a.vision = vis
	u := seedUser(t, a.db, "5561910000005", "Ana", "")

	_, err := a.Run(context.Background(), u, "", []ImageAttachment{{Data: []byte("x"), Mime: "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	if vis.calls == 0 {
		t.Fatal("VisionProvider nao foi acionado pro provider sem vision")
	}
	if !strings.Contains(flattenLLMMessages(fake.lastRequest.Messages), "uma caixa de remedio") {
		t.Fatalf("descricao nao foi injetada como texto: %q", flattenLLMMessages(fake.lastRequest.Messages))
	}
}

// Provider COM vision recebe a imagem nativa e nao chama o VisionProvider.
func TestRunImagemVaiNativaQuandoProviderTemVision(t *testing.T) {
	a := convergenceAgent(t)
	fake := &fakeChat{name: "op", reply: "vi a foto", vision: true}
	a.chat = fake
	vis := &fakeVision{desc: "nao deveria ser chamado"}
	a.vision = vis
	u := seedUser(t, a.db, "5561910000006", "Ana", "")

	_, err := a.Run(context.Background(), u, "o que e isso", []ImageAttachment{{Data: []byte("x"), Mime: "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	if vis.calls != 0 {
		t.Fatal("provider com vision nativa nao deveria pre-descrever a imagem")
	}
	var found bool
	for _, m := range fake.lastRequest.Messages {
		for _, c := range m.Content {
			if c.Type == "image" && c.ImageData != "" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("bloco de imagem nao chegou ao provider com vision nativa")
	}
}

// O breaker precisa estar ativo no loop: sem ele a call repetida queima as 8
// iteracoes e o usuario recebe o fallback generico.
func TestRunLoopCortaToolRepetida(t *testing.T) {
	a := convergenceAgent(t)
	fake := &fakeChat{
		name: "op", alwaysCallTool: "buscar_agenda", toolArgs: `{"data":"2026-08-24"}`,
	}
	a.chat = fake
	u := seedUser(t, a.db, "5561910000007", "Ana", "")

	out, err := a.Run(context.Background(), u, "o que tenho hoje", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("mesmo cortando, o turno tem que devolver algo ao usuario")
	}
	// Com o breaker, o handler nao e re-executado; o loop ainda percorre as
	// iteracoes, mas nenhuma chamada extra de tool acontece.
	if !containsToolBreakerRelief(fake.lastRequest.Messages) {
		t.Fatal("o corte do breaker nao chegou ao modelo como tool_result")
	}
}

func containsToolBreakerRelief(msgs []llm.Message) bool {
	for _, m := range msgs {
		for _, c := range m.Content {
			if c.Type == "tool_result" && strings.Contains(c.ToolResult, "Chamada repetida") {
				return true
			}
		}
	}
	return false
}

// A contabilidade de token tem que ser gravada em todo turno.
func TestRunGravaUsoDeToken(t *testing.T) {
	a := convergenceAgent(t)
	a.chat = &fakeChat{name: "deepseek", reply: "ok"}
	u := seedUser(t, a.db, "5561910000008", "Ana", "")

	if _, err := a.Run(context.Background(), u, "oi", nil); err != nil {
		t.Fatal(err)
	}
	sums, err := a.db.SumLLMUsageSince(timeMinusHour())
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) == 0 {
		t.Fatal("nenhum consumo de token gravado")
	}
	if sums["deepseek|deepseek-model"].InputTokens == 0 {
		t.Fatalf("consumo nao atribuido ao provider certo: %+v", sums)
	}
}

// Sem provider injetado, o caminho Anthropic legado continua existindo (os
// testes antigos dependem dele).
func TestRunSemProviderCaiNoCaminhoLegado(t *testing.T) {
	a := convergenceAgent(t)
	a.chat, a.companionChat = nil, nil
	u := seedUser(t, a.db, "5561910000009", "Ana", "")
	// Sem provider E sem client Anthropic: erro claro, nunca panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("caminho legado nao pode dar panic: %v", r)
		}
	}()
	_, err := a.Run(context.Background(), u, "oi", nil)
	if err == nil {
		t.Fatal("sem nenhum LLM configurado, Run tem que devolver erro")
	}
	if !strings.Contains(err.Error(), "nenhum LLM configurado") {
		t.Fatalf("erro pouco claro: %v", err)
	}
}
