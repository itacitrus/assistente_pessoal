package main

import (
	"strings"
	"testing"
)

// O prompt proibe ** e ##, mas nao existia sanitizador — a Anthropic
// simplesmente obedecia. WhatsApp renderiza ** como asterisco literal.
func TestSanitizeForWhatsApp(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hoje: **14:00** reuniao", "Hoje: *14:00* reuniao"},
		{"## Agenda\nreuniao", "Agenda\nreuniao"},
		{"### Titulo", "Titulo"},
		{"texto normal", "texto normal"},
		{"*ja negrito*", "*ja negrito*"},
		{"__sublinhado__", "_sublinhado_"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeForWhatsApp(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, quer %q", c.in, got, c.want)
		}
	}
}

func TestSanitizePreservaQuebrasDeLinha(t *testing.T) {
	in := "Compromissos:\n- **14:00** contador\n- **17:30** escola"
	got := sanitizeForWhatsApp(in)
	if strings.Contains(got, "**") {
		t.Fatalf("sobrou markdown: %q", got)
	}
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("quebras de linha perdidas: %q", got)
	}
	if !strings.Contains(got, "*14:00*") {
		t.Fatalf("negrito nao converteu: %q", got)
	}
}

// Asterisco solto (multiplicacao, enfase ja valida) nao pode ser destruido.
func TestSanitizeNaoQuebraAsteriscoSolto(t *testing.T) {
	in := "2 * 3 = 6"
	if got := sanitizeForWhatsApp(in); got != in {
		t.Fatalf("sanitize(%q) = %q", in, got)
	}
}

// "#" no meio da frase nao e heading.
func TestSanitizeNaoTiraHashNoMeio(t *testing.T) {
	in := "o quarto #3 fica no fim"
	if got := sanitizeForWhatsApp(in); got != in {
		t.Fatalf("sanitize(%q) = %q", in, got)
	}
}
