package main

import (
	"strings"
	"testing"
)

func TestParseIntentResponse_CreateEvent(t *testing.T) {
	raw := `{
		"intent": "criar_evento",
		"data": {
			"title": "Reuniao com Joao",
			"date": "2026-04-11",
			"time": "15:00",
			"duration_minutes": 60
		},
		"confirmation_message": "Agendei Reuniao com Joao para 11/04 as 15h. Confirma?"
	}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "criar_evento" {
		t.Fatalf("expected criar_evento, got %s", result.Intent)
	}
	if result.Data.Title != "Reuniao com Joao" {
		t.Fatalf("expected title Reuniao com Joao, got %s", result.Data.Title)
	}
	if result.Data.Date != "2026-04-11" {
		t.Fatalf("expected date 2026-04-11, got %s", result.Data.Date)
	}
	if result.Data.DurationMinutes != 60 {
		t.Fatalf("expected duration 60, got %d", result.Data.DurationMinutes)
	}
}

func TestParseIntentResponse_ConsultarAgenda(t *testing.T) {
	raw := `{
		"intent": "consultar_agenda",
		"data": {
			"start_date": "2026-04-10",
			"end_date": "2026-04-10"
		},
		"confirmation_message": "Aqui estao os compromissos de hoje."
	}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "consultar_agenda" {
		t.Fatalf("expected consultar_agenda, got %s", result.Intent)
	}
	if result.Data.StartDate != "2026-04-10" {
		t.Fatalf("expected start_date 2026-04-10, got %s", result.Data.StartDate)
	}
}

func TestParseIntentResponse_Confirmar(t *testing.T) {
	raw := `{"intent": "confirmar", "data": {}, "confirmation_message": "Ok, confirmado!"}`

	result, err := ParseIntentResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ParseIntentResponse failed: %v", err)
	}
	if result.Intent != "confirmar" {
		t.Fatalf("expected confirmar, got %s", result.Intent)
	}
}

func TestBuildIntentPrompt(t *testing.T) {
	prompt := BuildIntentPrompt("Waldyr", "")
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "Waldyr") {
		t.Fatal("prompt should contain user name")
	}
	if !strings.Contains(prompt, "criar_evento") {
		t.Fatal("prompt should contain intent instructions")
	}
}
