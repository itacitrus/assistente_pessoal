package main

import (
	"strings"
	"testing"
)

func TestBuildHaikuSystemPrompt(t *testing.T) {
	prompt := buildHaikuSystemPrompt("Waldyr")
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "Waldyr") {
		t.Fatal("prompt should contain user name")
	}
	if !strings.Contains(prompt, "ESCALE") {
		t.Fatal("prompt should contain escalation instruction")
	}
}

func TestBuildSonnetSystemPrompt(t *testing.T) {
	prompt := buildSonnetSystemPrompt("Waldyr")
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "Waldyr") {
		t.Fatal("prompt should contain user name")
	}
	if !strings.Contains(prompt, "ferramentas") {
		t.Fatal("prompt should mention tools")
	}
}

func TestIsEscalation(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`{"escalate": true, "reason": "complex"}`, true},
		{`{"escalate": false}`, false},
		{`Ola, como posso ajudar?`, false},
		{`{"escalate": true}`, true},
		{`  {"escalate": true, "reason": "test"}  `, true},
	}

	for _, tt := range tests {
		got := isEscalation(tt.input)
		if got != tt.expected {
			t.Errorf("isEscalation(%q) = %v, want %v", tt.input, got, tt.expected)
		}
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
	if len(tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{"buscar_agenda", "criar_evento", "editar_evento", "cancelar_evento", "buscar_historico", "criar_evento_outro_usuario", "gerar_link_meet"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
