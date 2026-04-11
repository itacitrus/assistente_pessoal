package main

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt(t *testing.T) {
	prompt := buildSystemPrompt("Waldyr")
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

func TestBuildToolDefinitions(t *testing.T) {
	tools := buildToolDefinitions()
	if len(tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{"buscar_agenda", "criar_evento", "editar_evento", "cancelar_evento", "buscar_historico", "criar_evento_outro_usuario", "convidar_participante", "salvar_memoria", "buscar_memoria", "gerar_link_meet", "convidar_externo"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
