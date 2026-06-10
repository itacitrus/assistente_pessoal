package main

import (
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Issue 4 — engajamento social no prompt operacional (responsável/comum).
// =========================================================================

func TestOperationalPromptHasSocialEngagement(t *testing.T) {
	op := buildSystemPromptStableOperational("Lucas")
	for _, s := range []string{"PAPO, DESABAFO E FOFOCA", "carimbe", "fofoca"} {
		if !strings.Contains(op, s) {
			t.Errorf("prompt operacional faltando %q (engajamento social)", s)
		}
	}
	// Não pode vazar a persona idoso pro operacional.
	if strings.Contains(op, "amigo Zello") {
		t.Error("prompt operacional não deveria conter persona companion 'amigo Zello'")
	}
}

// =========================================================================
// Issue 1 — proatividade não-repetitiva + back-off.
// =========================================================================

func TestHasUnansweredProactive(t *testing.T) {
	if hasUnansweredProactive(nil) {
		t.Error("nil → false")
	}
	if !hasUnansweredProactive([]ProactiveAttempt{{Status: "replied"}, {Status: "sent"}}) {
		t.Error("alguma 'sent' → true")
	}
	if hasUnansweredProactive([]ProactiveAttempt{{Status: "replied"}, {Status: "failed"}}) {
		t.Error("só replied/failed → false")
	}
}

func TestProactiveAvoidRepeatHint(t *testing.T) {
	db := setupTestDB(t)
	idoso := mkIdoso(t, db, "Dona Rosa", 0)

	// Sem puxadas → sem hint.
	if h := proactiveAvoidRepeatHint(db, idoso.ID); h != "" {
		t.Errorf("sem puxadas o hint deveria ser vazio, got %q", h)
	}

	if _, err := db.RecordProactiveAttempt(idoso.ID, "Oi Rosa, e aquele friozinho gostoso de maio?"); err != nil {
		t.Fatalf("record proactive: %v", err)
	}
	h := proactiveAvoidRepeatHint(db, idoso.ID)
	if !strings.Contains(h, "friozinho") {
		t.Errorf("hint deveria listar a puxada anterior, got %q", h)
	}
	if !strings.Contains(h, "NÃO repita") {
		t.Errorf("hint deveria instruir a não repetir o gancho, got %q", h)
	}
}

// =========================================================================
// Issue 2 — consciência de último contato do dia.
// =========================================================================

func TestUpcomingMedRemindersToday(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	idoso := mkIdoso(t, db, "Dona Ines", 0)
	mkMedForUser(t, db, idoso, "Losartana", "FREQ=DAILY;BYHOUR=20;BYMINUTE=0", false)
	loc := BRT()

	// 15h local → o lembrete das 20h ainda vem.
	afternoon := time.Date(2026, 5, 22, 15, 0, 0, 0, loc)
	rem := a.upcomingMedRemindersToday(idoso, afternoon)
	if len(rem) != 1 {
		t.Fatalf("esperava 1 lembrete restante às 15h, got %d", len(rem))
	}
	if rem[0].at.In(loc).Hour() != 20 {
		t.Errorf("lembrete restante deveria ser 20h, got %v", rem[0].at.In(loc))
	}

	// 21h local → já passou; nada restante.
	night := time.Date(2026, 5, 22, 21, 0, 0, 0, loc)
	if r := a.upcomingMedRemindersToday(idoso, night); len(r) != 0 {
		t.Fatalf("após o lembrete não deveria sobrar nada, got %d", len(r))
	}
}

// O antigo [CONTEXTO DO DIA] (gate >=14h) virou parte do bloco único [AGORA],
// que agora roda em TODAS as horas — o gate matinal era exatamente o buraco
// que deixou o "boa noite, descanse bem" passar às 07:20 (B3).
func TestAgoraPartDayContext(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	idoso := mkIdoso(t, db, "Seu Bento", 0)
	mkMedForUser(t, db, idoso, "Aradois", "FREQ=DAILY;BYHOUR=19;BYMINUTE=0", false)
	loc := BRT()

	// 10h DA MANHÃ com lembrete às 19h → injeta SIM (gate removido): é
	// exatamente o cenário do B3 — o bloco precisa proibir despedida noturna.
	morning := time.Date(2026, 5, 22, 10, 0, 0, 0, loc)
	parts := a.appendCompanionAgoraPart(nil, idoso, a.newTurnContext(idoso, morning))
	if len(parts) != 1 {
		t.Fatalf("de manhã o [AGORA] também deve existir, got %d parts", len(parts))
	}
	if !strings.Contains(parts[0].Text, "MANHÃ") || !strings.Contains(parts[0].Text, "bom dia") {
		t.Errorf("[AGORA] matinal deveria asseverar MANHÃ e a saudação correta, got %q", parts[0].Text)
	}
	if !strings.Contains(parts[0].Text, "19:00") || !strings.Contains(parts[0].Text, "NÃO é o último contato") {
		t.Errorf("[AGORA] deveria listar o lembrete 19h e negar último contato, got %q", parts[0].Text)
	}

	// 16h, lembrete às 19h pendente → avisa que NÃO é o último contato.
	afternoon := time.Date(2026, 5, 22, 16, 0, 0, 0, loc)
	parts = a.appendCompanionAgoraPart(nil, idoso, a.newTurnContext(idoso, afternoon))
	if len(parts) != 1 {
		t.Fatalf("esperava 1 parte [AGORA] às 16h, got %d", len(parts))
	}
	if !strings.Contains(parts[0].Text, "NÃO é o último contato") || !strings.Contains(parts[0].Text, "19:00") {
		t.Errorf("deveria avisar lembrete 19h e que não é o último contato, got %q", parts[0].Text)
	}

	// 20h, após o último lembrete → noite sem pendência libera boa-noite.
	night := time.Date(2026, 5, 22, 20, 0, 0, 0, loc)
	parts = a.appendCompanionAgoraPart(nil, idoso, a.newTurnContext(idoso, night))
	if len(parts) != 1 || !strings.Contains(parts[0].Text, "Não há mais lembretes") {
		t.Fatalf("após último lembrete deveria liberar boa-noite, got %+v", parts)
	}

	// Não-idoso nunca recebe.
	comum := &User{ID: idoso.ID, Type: UserTypeComum}
	if parts := a.appendCompanionAgoraPart(nil, comum, a.newTurnContext(comum, afternoon)); len(parts) != 0 {
		t.Fatalf("não-idoso não deveria receber [AGORA]")
	}
}

// TestRenderAgoraPart_PeriodGreetings trava as asserções de período do [AGORA]:
// manhã proíbe despedida noturna (B3), noite permite, madrugada herda as
// despedidas da noite (insone à 00:30 recebe "boa noite" legítimo).
func TestRenderAgoraPart_PeriodGreetings(t *testing.T) {
	loc := BRT()
	mk := func(h, m int) *TurnContext {
		now := time.Date(2026, 6, 9, h, m, 0, 0, loc)
		return &TurnContext{Now: now, Loc: loc, Period: periodOfDay(h)}
	}

	morning := renderAgoraPart(mk(7, 20), "Simone")
	if !strings.Contains(morning, "MANHÃ") || !strings.Contains(morning, `"bom dia"`) {
		t.Errorf("manhã deveria asseverar a saudação bom dia, got %q", morning)
	}
	if !strings.Contains(morning, "NÃO combina com este horário") {
		t.Errorf("manhã deveria vetar despedida de fim de dia, got %q", morning)
	}

	evening := renderAgoraPart(mk(21, 0), "Simone")
	if !strings.Contains(evening, "NOITE") || !strings.Contains(evening, "boa noite") {
		t.Errorf("noite deveria liberar boa noite, got %q", evening)
	}
	if strings.Contains(evening, "NÃO combina com este horário") {
		t.Errorf("noite não pode vetar a própria despedida, got %q", evening)
	}

	dawn := renderAgoraPart(mk(0, 30), "Simone")
	if !strings.Contains(dawn, "MADRUGADA") {
		t.Errorf("madrugada deveria ser asseverada, got %q", dawn)
	}
	if !strings.Contains(dawn, "descanse bem") || !strings.Contains(dawn, "natural agora") {
		t.Errorf("madrugada herda despedidas da noite, got %q", dawn)
	}
	if !strings.Contains(dawn, `NUNCA cumprimente com "bom dia"`) {
		t.Errorf("madrugada proíbe bom dia/boa tarde como abertura, got %q", dawn)
	}
}

func TestPeriodOfDayBoundaries(t *testing.T) {
	cases := map[int]string{
		0: "madrugada", 4: "madrugada",
		5: "manhã", 11: "manhã",
		12: "tarde", 17: "tarde",
		18: "noite", 23: "noite",
	}
	for h, want := range cases {
		if got := periodOfDay(h); got != want {
			t.Errorf("periodOfDay(%d)=%q, want %q", h, got, want)
		}
	}
}
