package main

import (
	"reflect"
	"testing"
	"time"
)

// Gates do P-transporte: dedup intra-janela de coalescing (B5). Escopo
// estrito — só dentro da mesma janela; repetição entre turnos não passa aqui.
func TestDedupCoalescedTexts(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"double-tap exato", []string{"Cortar cabelo segunda-feira 8:00", "Cortar cabelo segunda-feira 8:00"}, []string{"Cortar cabelo segunda-feira 8:00"}},
		{"case e whitespace", []string{"Cortar cabelo", "  cortar Cabelo "}, []string{"Cortar cabelo"}},
		{"whitespace interno e NBSP", []string{"cortar  cabelo", "cortar cabelo"}, []string{"cortar  cabelo"}},
		{"multi-parte distinta preservada", []string{"Bom dia", "Tudo bem?", "Pronto"}, []string{"Bom dia", "Tudo bem?", "Pronto"}},
		{"repeticao nao-adjacente (seen-set)", []string{"agendar dentista", "que horas são?", "agendar dentista"}, []string{"agendar dentista", "que horas são?"}},
		{"vazios e espacos descartados", []string{"", "   ", "oi", "oi"}, []string{"oi"}},
		{"eventos diferentes na janela preservados", []string{"marcar reunião 10h", "marcar reunião 14h"}, []string{"marcar reunião 10h", "marcar reunião 14h"}},
		{"um so passa direto", []string{"oi"}, []string{"oi"}},
		{"tudo whitespace devolve original", []string{"", "  "}, []string{"", "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dedupCoalescedTexts(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("dedupCoalescedTexts(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestConversationCreatedAtScansAsUTC trava o comportamento do driver que o
// grounding temporal assume: com o DSN atual (sem _loc), o modernc/sqlite
// parseia DATETIME DEFAULT CURRENT_TIMESTAMP para time.Time em UTC. Se um DSN
// futuro mudar isso, este teste quebra ANTES de os carimbos saírem 3h errados.
func TestConversationCreatedAtScansAsUTC(t *testing.T) {
	db := setupTestDB(t)
	u := &User{PhoneNumber: "5511900001111", Name: "Probe", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.AddConversationMessage(u.ID, "user", "probe"); err != nil {
		t.Fatalf("AddConversationMessage: %v", err)
	}
	msgs, err := db.GetConversationHistory(u.ID, 1)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetConversationHistory: %v (%d msgs)", err, len(msgs))
	}
	got := msgs[0].CreatedAt
	if got.IsZero() {
		t.Fatal("created_at deveria ser escaneado, veio zero")
	}
	if got.Location() != time.UTC {
		t.Fatalf("created_at deveria escanear como UTC, veio %v", got.Location())
	}
	if d := time.Since(got); d < -2*time.Minute || d > 2*time.Minute {
		t.Fatalf("created_at deveria ser ~agora em UTC (drift %v) — se o drift for ~3h, o driver mudou de fuso", d)
	}
}
