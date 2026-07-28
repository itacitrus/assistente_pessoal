package main

import (
	"testing"
	"time"
)

// Referência fixa de "agora" pros testes puros de seleção.
var bfNow = time.Date(2026, 7, 27, 21, 0, 0, 0, time.UTC)

func bfCand(id string, user int64, ago time.Duration, fromMe bool) backfillCandidate {
	return backfillCandidate{
		MsgID:     id,
		UserID:    user,
		Timestamp: bfNow.Add(-ago),
		FromMe:    fromMe,
	}
}

func selectedIDs(sel []backfillCandidate) map[string]bool {
	m := make(map[string]bool, len(sel))
	for _, c := range sel {
		m[c.MsgID] = true
	}
	return m
}

// Cada regra de descarte/seleção, num só cenário.
func TestSelectBackfill_AppliesAllGuards(t *testing.T) {
	wm := map[int64]time.Time{1: bfNow.Add(-2 * time.Hour)} // marca d'água user 1
	cands := []backfillCandidate{
		bfCand("keep1", 1, 1*time.Hour, false),   // depois da marca, dentro de 24h -> keep
		bfCand("old-wm", 1, 3*time.Hour, false),  // antes da marca (já tratado) -> drop
		bfCand("too-old", 1, 25*time.Hour, false),// fora das 24h -> drop
		bfCand("future", 1, -10*time.Minute, false), // 10min no futuro (> skew) -> drop
		bfCand("fromme", 1, 30*time.Minute, true),   // do próprio bot -> drop
		bfCand("dup", 1, 40*time.Minute, false),     // duplicata por MsgID
		bfCand("dup", 1, 40*time.Minute, false),     // -> mantém uma só
	}

	sel, dropped := selectBackfill(cands, wm, bfNow, 24*time.Hour, 30)

	got := selectedIDs(sel)
	want := map[string]bool{"keep1": true, "dup": true}
	if len(got) != len(want) {
		t.Fatalf("selecionados = %v, quero %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("esperava %q selecionado, não veio", id)
		}
	}
	// dedup: "dup" aparece exatamente uma vez no slice de saída.
	count := 0
	for _, c := range sel {
		if c.MsgID == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dup deveria aparecer 1x, apareceu %d", count)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, quero 0 (cap não estourou)", dropped)
	}
}

// Marca d'água é POR usuário: mesma idade de mensagem, resultado diferente.
func TestSelectBackfill_WatermarkIsPerUser(t *testing.T) {
	wm := map[int64]time.Time{
		1: bfNow.Add(-2 * time.Hour),  // user 1: fronteira recente
		2: bfNow.Add(-10 * time.Hour), // user 2: fronteira antiga
	}
	cands := []backfillCandidate{
		bfCand("u1", 1, 3*time.Hour, false), // antes da marca do u1 -> drop
		bfCand("u2", 2, 3*time.Hour, false), // depois da marca do u2 -> keep
	}

	sel, _ := selectBackfill(cands, wm, bfNow, 24*time.Hour, 30)

	got := selectedIDs(sel)
	if got["u1"] {
		t.Errorf("u1 não deveria ser selecionado (antes da marca do usuário 1)")
	}
	if !got["u2"] {
		t.Errorf("u2 deveria ser selecionado (depois da marca do usuário 2)")
	}
}

// Usuário sem marca d'água (nunca teve mensagem): passa dentro da janela.
func TestSelectBackfill_NoWatermarkPassesWithinWindow(t *testing.T) {
	wm := map[int64]time.Time{} // vazio
	cands := []backfillCandidate{
		bfCand("recent", 7, 1*time.Hour, false),  // keep
		bfCand("ancient", 7, 30*time.Hour, false), // fora das 24h -> drop
	}

	sel, _ := selectBackfill(cands, wm, bfNow, 24*time.Hour, 30)

	got := selectedIDs(sel)
	if !got["recent"] {
		t.Errorf("mensagem recente de usuário sem marca deveria passar")
	}
	if got["ancient"] {
		t.Errorf("mensagem fora das 24h não deveria passar mesmo sem marca")
	}
}

// Skew pequeno de relógio (futuro dentro da tolerância) é aceito.
func TestSelectBackfill_AcceptsSmallClockSkew(t *testing.T) {
	cands := []backfillCandidate{
		bfCand("nearfuture", 1, -2*time.Minute, false), // 2min no futuro, < skew -> keep
	}
	sel, _ := selectBackfill(cands, map[int64]time.Time{}, bfNow, 24*time.Hour, 30)
	if !selectedIDs(sel)["nearfuture"] {
		t.Errorf("mensagem levemente no futuro (skew) deveria ser aceita")
	}
}

// Teto: mantém as N mais recentes, em ordem cronológica, e reporta o descarte.
func TestSelectBackfill_CapsKeepingMostRecentChronological(t *testing.T) {
	cands := []backfillCandidate{
		bfCand("a", 1, 5*time.Hour, false),
		bfCand("c", 1, 1*time.Hour, false),
		bfCand("b", 1, 3*time.Hour, false),
	}
	sel, dropped := selectBackfill(cands, map[int64]time.Time{}, bfNow, 24*time.Hour, 2)

	if dropped != 1 {
		t.Fatalf("dropped = %d, quero 1", dropped)
	}
	if len(sel) != 2 {
		t.Fatalf("len(sel) = %d, quero 2", len(sel))
	}
	// Mantém as 2 mais recentes (b, c) e devolve em ordem cronológica (b antes de c).
	if sel[0].MsgID != "b" || sel[1].MsgID != "c" {
		t.Errorf("ordem = [%s %s], quero [b c]", sel[0].MsgID, sel[1].MsgID)
	}
}

// Saída sempre em ordem cronológica (mais antiga primeiro), pra alimentar o
// handleMessage na ordem em que o usuário mandou.
func TestSelectBackfill_SortsChronologically(t *testing.T) {
	cands := []backfillCandidate{
		bfCand("late", 1, 1*time.Hour, false),
		bfCand("early", 1, 4*time.Hour, false),
		bfCand("mid", 1, 2*time.Hour, false),
	}
	sel, _ := selectBackfill(cands, map[int64]time.Time{}, bfNow, 24*time.Hour, 30)
	if len(sel) != 3 {
		t.Fatalf("len(sel) = %d, quero 3", len(sel))
	}
	want := []string{"early", "mid", "late"}
	for i, id := range want {
		if sel[i].MsgID != id {
			t.Errorf("sel[%d] = %s, quero %s", i, sel[i].MsgID, id)
		}
	}
}

func TestSelectBackfill_EmptyInput(t *testing.T) {
	sel, dropped := selectBackfill(nil, map[int64]time.Time{}, bfNow, 24*time.Hour, 30)
	if len(sel) != 0 || dropped != 0 {
		t.Errorf("entrada vazia: sel=%v dropped=%d, quero vazio/0", sel, dropped)
	}
}
