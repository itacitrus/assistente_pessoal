package main

import (
	"strings"
	"testing"

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
	// Without pending permission
	out := buildSystemPromptDynamic(nil)
	if !strings.Contains(out, "Data/hora atual") {
		t.Fatal("dynamic prompt should contain current date/time")
	}
	if strings.Contains(out, "PERMISSAO PENDENTE") {
		t.Fatal("no permission context when pendingReq is nil")
	}

	// With pending permission
	req := &PermissionRequest{RequesterName: "Giovanni", EventData: `{"title":"reuniao"}`}
	out = buildSystemPromptDynamic(req)
	if !strings.Contains(out, "Giovanni") || !strings.Contains(out, "PERMISSAO PENDENTE") {
		t.Fatal("dynamic prompt with pending req should include requester name and marker")
	}
}

func TestBuildMessages(t *testing.T) {
	history := []ConversationMessage{
		{Role: "user", Content: "oi"},
		{Role: "assistant", Content: "ola!"},
	}
	msgs := buildMessages(history, "marca reuniao amanha")
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
	// 15 originais + 7 da Fase 3 (medicacao + receita) + 4 da Fase 4
	// (alertar_familia, pausar_proatividade, comentar_imagem, comentar_link)
	// + 1 da Fase 5 (status_dependente) = 27.
	if len(tools) != 27 {
		t.Fatalf("expected 27 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{
		// Originais
		"buscar_agenda", "criar_evento", "editar_evento", "cancelar_evento",
		"buscar_historico", "criar_evento_outro_usuario", "convidar_participante",
		"salvar_memoria", "buscar_memoria", "gerar_link_meet", "convidar_externo",
		"registrar_viagem", "listar_viagens", "cancelar_viagem", "responder_permissao",
		// Fase 3 (idosos): medicacao
		"cadastrar_medicamento", "listar_medicamentos", "editar_medicamento",
		"cancelar_medicamento", "marcar_remedio_tomado", "pular_dose",
		"extrair_receita_imagem",
		// Fase 4 (idosos): companion + media
		"alertar_familia", "pausar_proatividade", "comentar_imagem", "comentar_link",
		// Fase 5 (idosos): relatorio longitudinal pra responsavel
		"status_dependente",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
