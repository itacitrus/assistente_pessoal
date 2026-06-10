package main

import (
	"strings"
	"testing"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

func TestBuildSystemPromptStable(t *testing.T) {
	// Default (no Type) routes to operational persona.
	prompt := buildSystemPromptStable(&User{Name: "Waldyr"})
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "Waldyr") {
		t.Fatal("prompt should contain user name")
	}
	if !strings.Contains(prompt, "Ferramentas") {
		t.Fatal("prompt should mention tools")
	}
	if !strings.Contains(prompt, "NUNCA pergunte") {
		t.Fatal("prompt should tell agent to not ask unnecessary questions")
	}
	// Stable prompt MUST NOT contain time-varying content — that breaks caching.
	if strings.Contains(prompt, "Data/hora") {
		t.Fatal("stable prompt must not contain date/time (would invalidate cache)")
	}
}

func TestBuildSystemPromptDynamic(t *testing.T) {
	// now fixo (testabilidade era impossível com time.Now() interno):
	// 2026-06-09 07:20 BRT, segunda-feira de manhã.
	now := time.Date(2026, 6, 9, 7, 20, 0, 0, BRT())

	// Without pending permission
	out := buildSystemPromptDynamic(nil, now, BRT())
	if !strings.Contains(out, "Data/hora atual") {
		t.Fatal("dynamic prompt should contain current date/time")
	}
	if strings.Contains(out, "PERMISSAO PENDENTE") {
		t.Fatal("no permission context when pendingReq is nil")
	}

	// With pending permission
	req := &PermissionRequest{RequesterName: "Giovanni", EventData: `{"title":"reuniao"}`}
	out = buildSystemPromptDynamic(req, now, BRT())
	if !strings.Contains(out, "Giovanni") || !strings.Contains(out, "PERMISSAO PENDENTE") {
		t.Fatal("dynamic prompt with pending req should include requester name and marker")
	}
}

// TestBuildSystemPromptDynamic_LocalizedWeekdayAndPeriod trava o relógio do
// prompt em PT-BR + período computado em Go (contrato P0): o modelo lê, não
// deriva. Inglês ("Monday") era parte da cegueira temporal do B2/B3.
func TestBuildSystemPromptDynamic_LocalizedWeekdayAndPeriod(t *testing.T) {
	now := time.Date(2026, 6, 9, 7, 20, 0, 0, BRT())
	out := buildSystemPromptDynamic(nil, now, BRT())
	for _, want := range []string{"2026-06-09 07:20", "terça-feira", "manhã", "America/Sao_Paulo"} {
		if !strings.Contains(out, want) {
			t.Errorf("dynamic prompt deveria conter %q, got %q", want, out)
		}
	}
	if strings.Contains(out, "Monday") || strings.Contains(out, "Tuesday") {
		t.Errorf("dynamic prompt nao pode ter weekday em ingles: %q", out)
	}
	// Fuso de viagem: imprime o IANA realmente usado, hora local convertida.
	lis, _ := time.LoadLocation("Europe/Lisbon")
	out = buildSystemPromptDynamic(nil, now, lis)
	if !strings.Contains(out, "Europe/Lisbon") {
		t.Errorf("fuso de viagem deveria aparecer no relogio, got %q", out)
	}
}

func TestBuildMessages(t *testing.T) {
	history := []ConversationMessage{
		{Role: "user", Content: "oi"},
		{Role: "assistant", Content: "ola!"},
	}
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, BRT())
	msgs := buildMessages(history, "marca reuniao amanha", now, BRT())
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if string(msgs[0].Role) != "user" {
		t.Fatalf("expected first message role user, got %s", msgs[0].Role)
	}
	if string(msgs[1].Role) != "assistant" {
		t.Fatalf("expected second message role assistant, got %s", msgs[1].Role)
	}
	if string(msgs[2].Role) != "user" {
		t.Fatalf("expected third message role user, got %s", msgs[2].Role)
	}
}

// TestBuildMessages_StampsHistoryTimestamps é o gate load-bearing do B2: o
// lembrete dateless de ontem precisa chegar ao modelo com "[ontem 14:00]"
// (criado em UTC, exibido em BRT), e o turno atual NUNCA é carimbado.
func TestBuildMessages_StampsHistoryTimestamps(t *testing.T) {
	createdUTC := time.Date(2026, 6, 8, 17, 0, 0, 0, time.UTC) // 14:00 BRT de ontem
	history := []ConversationMessage{
		{Role: "assistant", Content: "Lembrete: *Reunião devs* começa às 15:00 (em 1 hora)", CreatedAt: createdUTC},
	}
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, BRT())
	msgs := buildMessages(history, "Amanhã 16h ir ao madre tereza", now, BRT())
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	got := msgs[0].Content[0].GetText()
	if !strings.HasPrefix(got, "[ontem 14:00] ") {
		t.Errorf("turno de ontem deveria comecar com [ontem 14:00], got %q", got)
	}
	cur := msgs[1].Content[0].GetText()
	if strings.HasPrefix(cur, "[") {
		t.Errorf("turno atual nao pode ser carimbado, got %q", cur)
	}
}

// TestDropCurrentTurnFromHistory cobre o S2: a última row do histórico é o
// próprio turno atual (persist-antes-de-Run) e não pode entrar duplicada.
func TestDropCurrentTurnFromHistory(t *testing.T) {
	hist := []ConversationMessage{
		{Role: "user", Content: "agendar dentista"},
		{Role: "assistant", Content: "marcado!"},
		{Role: "user", Content: "cortar cabelo segunda 8h"},
	}
	out := dropCurrentTurnFromHistory(hist, "cortar cabelo segunda 8h")
	if len(out) != 2 {
		t.Fatalf("ultima row (turno atual) deveria ser descartada, got %d rows", len(out))
	}
	// Repetição legítima em turno ANTERIOR é preservada: só a última row cai.
	hist2 := []ConversationMessage{
		{Role: "user", Content: "tomei"},
		{Role: "assistant", Content: "registrado"},
		{Role: "user", Content: "tomei"},
	}
	out2 := dropCurrentTurnFromHistory(hist2, "tomei")
	if len(out2) != 2 || out2[0].Content != "tomei" {
		t.Fatalf("apenas a ULTIMA ocorrencia deveria cair, got %+v", out2)
	}
	// Última row assistant → não mexe.
	hist3 := []ConversationMessage{{Role: "assistant", Content: "oi"}}
	if got := dropCurrentTurnFromHistory(hist3, "oi"); len(got) != 1 {
		t.Fatal("row assistant nao pode ser descartada")
	}
	// Conteúdo diferente → não mexe.
	if got := dropCurrentTurnFromHistory(hist, "outra coisa"); len(got) != 3 {
		t.Fatal("conteudo diferente nao pode ser descartado")
	}
}

func TestPersistedUserContent(t *testing.T) {
	cases := []struct {
		msg    string
		images int
		want   string
	}{
		{"oi", 0, "oi"},
		{"oi", 2, "oi"},
		{"", 1, "[imagem enviada]"},
		{"", 3, "[3 imagens enviadas]"},
		{"", 0, ""},
	}
	for _, c := range cases {
		if got := persistedUserContent(c.msg, c.images); got != c.want {
			t.Errorf("persistedUserContent(%q,%d)=%q, want %q", c.msg, c.images, got, c.want)
		}
	}
}

func TestMarkLastMessageForCache(t *testing.T) {
	msgs := []anthropic.Message{
		{Role: anthropic.RoleUser, Content: []anthropic.MessageContent{anthropic.NewTextMessageContent("a")}},
		{Role: anthropic.RoleAssistant, Content: []anthropic.MessageContent{anthropic.NewTextMessageContent("b")}},
		{Role: anthropic.RoleUser, Content: []anthropic.MessageContent{
			anthropic.NewTextMessageContent("c1"),
			anthropic.NewTextMessageContent("c2"),
		}},
	}

	// First pass: mark the last block of last message.
	markLastMessageForCache(msgs)
	if msgs[2].Content[1].CacheControl == nil {
		t.Fatal("expected cache_control on last block of last message")
	}
	if msgs[2].Content[0].CacheControl != nil {
		t.Fatal("expected no cache_control on other blocks of same message")
	}
	if msgs[0].Content[0].CacheControl != nil || msgs[1].Content[0].CacheControl != nil {
		t.Fatal("expected no cache_control on earlier messages")
	}

	// Second pass with a new message appended: prior breakpoint must be cleared,
	// new tail gets the breakpoint.
	msgs = append(msgs, anthropic.Message{
		Role:    anthropic.RoleAssistant,
		Content: []anthropic.MessageContent{anthropic.NewTextMessageContent("d")},
	})
	markLastMessageForCache(msgs)
	if msgs[2].Content[1].CacheControl != nil {
		t.Fatal("prior cache_control should have been cleared")
	}
	if msgs[3].Content[0].CacheControl == nil {
		t.Fatal("new tail should have cache_control")
	}
}

func TestBuildToolDefinitions(t *testing.T) {
	tools := buildToolDefinitions()
	// 16 originais (inclui conectar_agenda) + 9 da Fase 3/3.1 (medicacao +
	// receita + adiar_remedio + buscar_medicamento_catalogo) + 4 da Fase 4
	// (alertar_familia, pausar_proatividade, comentar_imagem, comentar_link) +
	// 2 da Fase 5 (status_dependente, listar_dependentes) = 31.
	if len(tools) != 31 {
		t.Fatalf("expected 31 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{
		// Originais
		"buscar_agenda", "conectar_agenda", "criar_evento", "editar_evento", "cancelar_evento",
		"buscar_historico", "criar_evento_outro_usuario", "convidar_participante",
		"salvar_memoria", "buscar_memoria", "gerar_link_meet", "convidar_externo",
		"registrar_viagem", "listar_viagens", "cancelar_viagem", "responder_permissao",
		// Fase 3 (idosos): medicacao
		"cadastrar_medicamento", "buscar_medicamento_catalogo", "listar_medicamentos",
		"editar_medicamento", "cancelar_medicamento", "marcar_remedio_tomado",
		"adiar_remedio", "pular_dose", "extrair_receita_imagem",
		// Fase 4 (idosos): companion + media
		"alertar_familia", "pausar_proatividade", "comentar_imagem", "comentar_link",
		// Fase 5 (idosos): relatorio longitudinal pra responsavel
		"status_dependente", "listar_dependentes",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
