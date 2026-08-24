package main

import (
	"strings"
	"testing"
)

// Vazamento de chines e limitacao DECLARADA pelo fornecedor (paper do R1:
// "optimized for Chinese and English... may result in language mixing").
func TestDetectI6CJK(t *testing.T) {
	if len(detectI6CJK("Bom dia! Ja tomou o remedio?")) != 0 {
		t.Fatal("texto em PT-BR limpo nao pode violar I6")
	}
	v := detectI6CJK("Bom dia! 好的 ja tomou o remedio?")
	if len(v) == 0 {
		t.Fatal("caractere Han deveria violar I6")
	}
	if v[0].Invariant != "I6" {
		t.Fatalf("invariante = %q, quer I6", v[0].Invariant)
	}
}

// Acento portugues e emoji sao legitimos e nao podem virar falso positivo.
func TestDetectI6NaoPegaAcentoNemEmoji(t *testing.T) {
	for _, s := range []string{
		"Já tomou? Coração, atenção, informação!",
		"Tudo bem 😊 bom dia ☀️",
		"Não esqueça: às 14h30 — ok?",
	} {
		if v := detectI6CJK(s); len(v) != 0 {
			t.Errorf("falso positivo em %q: %+v", s, v)
		}
	}
}

func TestDetectI6PegaKanaEHangul(t *testing.T) {
	for _, s := range []string{"ok ひらがな", "ok カタカナ", "ok 한글"} {
		if len(detectI6CJK(s)) == 0 {
			t.Errorf("nao detectou CJK em %q", s)
		}
	}
}

// O guard tem que limpar CJK MESMO em modo log — e o default em prod.
func TestGuardRemoveCJKEmModoLog(t *testing.T) {
	a := guardAgentLog()
	in := guardInput{User: guardUser, TC: tcAt(9, 0), UserMsg: "bom dia", Engine: "companion"}
	out, _ := a.guardOutput(in, "Bom dia, Fabio! 好的 Tudo certo com o remedio.", nil)
	if cjkRe.MatchString(out) {
		t.Fatalf("CJK sobreviveu ao guard em modo log: %q", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("guard nunca pode devolver vazio")
	}
}

// O envelope de markdown vale em qualquer modo, inclusive off.
func TestGuardSanitizaMarkdownEmModoOff(t *testing.T) {
	a := &Agent{cfg: &Config{OutputGuardMode: "off"}}
	in := guardInput{User: guardUser, TC: tcAt(9, 0), UserMsg: "o que tenho hoje", Engine: "operational"}
	out, _ := a.guardOutput(in, "Hoje: **14:00** reuniao", nil)
	if strings.Contains(out, "**") {
		t.Fatalf("markdown vazou em modo off: %q", out)
	}
}
