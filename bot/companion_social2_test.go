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

func TestAppendCompanionDayContextPart(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	idoso := mkIdoso(t, db, "Seu Bento", 0)
	mkMedForUser(t, db, idoso, "Aradois", "FREQ=DAILY;BYHOUR=19;BYMINUTE=0", false)
	loc := BRT()

	// Antes das 14h → não injeta (evita ruído de manhã).
	morning := time.Date(2026, 5, 22, 10, 0, 0, 0, loc)
	if parts := a.appendCompanionDayContextPart(nil, idoso, morning); len(parts) != 0 {
		t.Fatalf("antes das 14h não deveria injetar contexto, got %d", len(parts))
	}

	// 16h, lembrete às 19h pendente → avisa que NÃO é o último contato.
	afternoon := time.Date(2026, 5, 22, 16, 0, 0, 0, loc)
	parts := a.appendCompanionDayContextPart(nil, idoso, afternoon)
	if len(parts) != 1 {
		t.Fatalf("esperava 1 parte de contexto às 16h, got %d", len(parts))
	}
	if !strings.Contains(parts[0].Text, "não é o último contato") || !strings.Contains(parts[0].Text, "19:00") {
		t.Errorf("deveria avisar lembrete 19h e que não é o último contato, got %q", parts[0].Text)
	}

	// 20h, após o último lembrete → libera boa-noite.
	night := time.Date(2026, 5, 22, 20, 0, 0, 0, loc)
	parts = a.appendCompanionDayContextPart(nil, idoso, night)
	if len(parts) != 1 || !strings.Contains(parts[0].Text, "Não há mais lembretes") {
		t.Fatalf("após último lembrete deveria liberar boa-noite, got %+v", parts)
	}

	// Não-idoso nunca recebe.
	comum := &User{ID: idoso.ID, Type: UserTypeComum}
	if parts := a.appendCompanionDayContextPart(nil, comum, afternoon); len(parts) != 0 {
		t.Fatalf("não-idoso não deveria receber contexto do dia")
	}
}
