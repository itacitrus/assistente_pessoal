package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDailySummary_WithEvents(t *testing.T) {
	events := []CalendarEvent{
		{Title: "Standup", Start: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 10, 9, 30, 0, 0, time.UTC)},
		{Title: "Almoco com cliente", Start: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 10, 13, 0, 0, 0, time.UTC)},
	}

	result := FormatDailySummary("Waldyr", events, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	if result == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(result, "Standup") || !strings.Contains(result, "Almoco") {
		t.Fatalf("summary should contain event titles, got: %s", result)
	}
	if !strings.Contains(result, "09:00") {
		t.Fatalf("summary should contain formatted times, got: %s", result)
	}
}

func TestFormatDailySummary_NoEvents(t *testing.T) {
	result := FormatDailySummary("Waldyr", nil, time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	if !strings.Contains(result, "livre") && !strings.Contains(result, "Nenhum") {
		t.Fatalf("should indicate no events, got: %s", result)
	}
}

func TestFormatWeeklySummary(t *testing.T) {
	events := []CalendarEvent{
		{Title: "Reuniao segunda", Start: time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)},
		{Title: "Reuniao terca", Start: time.Date(2026, 4, 14, 14, 0, 0, 0, time.UTC)},
	}

	result := FormatWeeklySummary("Waldyr", events, time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC))
	if result == "" {
		t.Fatal("expected non-empty summary")
	}
}

// TestFormatReminder_IsDateExplicit: o lembrete é o artefato mais relido do
// histórico — precisa carregar a própria data (absoluta PRIMEIRO, relativa
// como anotação) para nunca mais ser temporalmente realocado (B2).
func TestFormatReminder_IsDateExplicit(t *testing.T) {
	ev := CalendarEvent{
		Title: "Reuniao com CEO",
		Start: time.Date(2026, 4, 10, 15, 0, 0, 0, time.UTC), // sexta
	}
	// now no mesmo dia → rótulo HOJE como anotação após a data absoluta.
	now := time.Date(2026, 4, 10, 14, 0, 0, 0, time.UTC)
	result := FormatReminder(ev, now)
	for _, want := range []string{"Reuniao com CEO", "15:00", "Sexta, 10/04 (HOJE)"} {
		if !strings.Contains(result, want) {
			t.Fatalf("reminder deveria conter %q, got: %s", want, result)
		}
	}

	// now um dia antes → AMANHÃ.
	result = FormatReminder(ev, now.AddDate(0, 0, -1))
	if !strings.Contains(result, "Sexta, 10/04 (AMANHÃ)") {
		t.Fatalf("reminder deveria anotar AMANHÃ, got: %s", result)
	}

	// Outro dia → só a data absoluta, sem anotação relativa.
	result = FormatReminder(ev, now.AddDate(0, 0, -5))
	if !strings.Contains(result, "Sexta, 10/04 às 15:00") || strings.Contains(result, "(HOJE)") || strings.Contains(result, "(AMANHÃ)") {
		t.Fatalf("sem rotulo relativo fora de hoje/amanha, got: %s", result)
	}
}

func TestFormatEventList_ShowsEventTypeAndMaster(t *testing.T) {
	events := []CalendarEvent{
		// 1. Native birthday (recurring, all-day, eventType=birthday)
		{
			ID:               "bday-master_20260417",
			Title:            "Aniversário Rogério",
			Start:            time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC),
			End:              time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
			EventType:        "birthday",
			RecurringEventID: "bday-master",
		},
		// 2. Fake birthday (recurring, timed at midnight, eventType=default)
		{
			ID:               "fake-master_20260505T030000Z",
			Title:            "Aniversário Daniel",
			Start:            time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
			End:              time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
			EventType:        "default",
			RecurringEventID: "fake-master",
		},
		// 3. Regular single event (not recurring, eventType=default)
		{
			ID:        "one-off-123",
			Title:     "Reunião com dentista",
			Start:     time.Date(2026, 5, 5, 14, 0, 0, 0, time.UTC),
			End:       time.Date(2026, 5, 5, 15, 0, 0, 0, time.UTC),
			EventType: "default",
		},
	}

	out := FormatEventList(events)

	// Native birthday line must show [type:birthday] and [master:...]
	if !strings.Contains(out, "Aniversário Rogério [id:bday-master_20260417] [type:birthday] [master:bday-master]") {
		t.Errorf("native birthday formatting missing expected markers in:\n%s", out)
	}

	// Fake birthday line must show [master:...] but NOT [type:...]
	if !strings.Contains(out, "Aniversário Daniel [id:fake-master_20260505T030000Z] [master:fake-master]") {
		t.Errorf("fake birthday should show master only, got:\n%s", out)
	}
	if strings.Contains(out, "Daniel [id:fake-master_20260505T030000Z] [type:") {
		t.Errorf("fake birthday must NOT have [type:default] suffix, got:\n%s", out)
	}

	// One-off event: neither marker
	if !strings.Contains(out, "Reunião com dentista [id:one-off-123]\n") {
		t.Errorf("one-off event should have no type/master suffix, got:\n%s", out)
	}
}

func TestRelativeDayLabel(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, brt)

	cases := []struct {
		name      string
		eventTime time.Time
		want      string
	}{
		{"mesmo dia retorna HOJE", time.Date(2026, 4, 16, 15, 0, 0, 0, brt), "HOJE"},
		{"mesmo dia mais cedo retorna HOJE", time.Date(2026, 4, 16, 6, 0, 0, 0, brt), "HOJE"},
		{"proximo dia retorna AMANHA", time.Date(2026, 4, 17, 5, 0, 0, 0, brt), "AMANHÃ"},
		{"2 dias no futuro retorna vazio", time.Date(2026, 4, 18, 10, 0, 0, 0, brt), ""},
		{"ontem retorna vazio", time.Date(2026, 4, 15, 10, 0, 0, 0, brt), ""},
		{"travessia meia-noite: evento amanha 00:30 vs agora 23:59", time.Date(2026, 4, 17, 0, 30, 0, 0, brt), "AMANHÃ"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeDayLabel(tc.eventTime, now)
			if got != tc.want {
				t.Fatalf("relativeDayLabel = %q, queria %q", got, tc.want)
			}
		})
	}
}

func TestFormatEventCreated_RelativeLabel(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	ev := CalendarEvent{
		Title: "Reuniao com OTC",
		Start: time.Date(2026, 4, 16, 9, 0, 0, 0, brt),
		End:   time.Date(2026, 4, 16, 10, 0, 0, 0, brt),
	}
	// Mesmo dia → data absoluta primeiro, HOJE como anotação.
	now := time.Date(2026, 4, 16, 8, 0, 0, 0, brt)
	out := FormatEventCreated(ev, now)
	if !strings.Contains(out, "Reuniao com OTC") {
		t.Fatalf("output deveria conter titulo, got: %s", out)
	}
	if !strings.Contains(out, "Quinta, 16/04 (HOJE) às 09:00") {
		t.Fatalf("output deveria ser absoluto-primeiro com anotacao relativa, got: %s", out)
	}
	// Outro dia → sem anotação relativa.
	out = FormatEventCreated(ev, now.AddDate(0, 0, -10))
	if !strings.Contains(out, "Quinta, 16/04 às 09:00") || strings.Contains(out, "(HOJE)") {
		t.Fatalf("fora de hoje/amanha nao pode ter rotulo relativo, got: %s", out)
	}
}

// TestFormatHistoryTurn_RelativePrefixes prova o helper de grounding: criado
// em UTC (driver), exibido no fuso local; hoje/ontem/amanhã/dia-antigo/zero.
func TestFormatHistoryTurn_RelativePrefixes(t *testing.T) {
	loc := BRT()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, loc) // terça

	cases := []struct {
		name    string
		created time.Time
		want    string
	}{
		{"hoje", time.Date(2026, 6, 9, 12, 12, 0, 0, time.UTC), "[hoje 09:12] oi"},     // 12:12Z = 09:12 BRT
		{"ontem", time.Date(2026, 6, 8, 17, 0, 0, 0, time.UTC), "[ontem 14:00] oi"},    // 17:00Z = 14:00 BRT
		{"amanha", time.Date(2026, 6, 10, 11, 0, 0, 0, time.UTC), "[amanhã 08:00] oi"}, // defensivo
		{"semana passada", time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC), "[qua 03/06 10:00] oi"},
		{"zero", time.Time{}, "oi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatHistoryTurn("oi", tc.created, now, loc); got != tc.want {
				t.Fatalf("formatHistoryTurn = %q, queria %q", got, tc.want)
			}
		})
	}

	// Fuso de viagem: carimbo na hora local do destino, não BRT.
	lis, _ := time.LoadLocation("Europe/Lisbon")
	created := time.Date(2026, 6, 9, 9, 0, 0, 0, time.UTC) // 10:00 em Lisboa (CEST)
	nowLis := time.Date(2026, 6, 9, 15, 0, 0, 0, lis)
	if got := formatHistoryTurn("oi", created, nowLis, lis); got != "[hoje 10:00] oi" {
		t.Fatalf("carimbo em fuso de viagem = %q, queria \"[hoje 10:00] oi\"", got)
	}
}

func TestStripLeadingStamp(t *testing.T) {
	cases := map[string]string{
		"[hoje 09:12] Bom dia, Fábio!":              "Bom dia, Fábio!",
		"[ontem 14:00] oi":                          "oi",
		"[ter 03/06 10:00] tudo bem?":               "tudo bem?",
		"[hoje 10:00] [hoje 09:55] oi":              "oi", // eco composto
		"Bom dia! [hoje 09:12] é meio-dia":          "Bom dia! [hoje 09:12] é meio-dia", // só no início
		"sem carimbo":                               "sem carimbo",
		"[amanhã 08:00] lembrete":                   "lembrete",
	}
	for in, want := range cases {
		if got := stripLeadingStamp(in); got != want {
			t.Errorf("stripLeadingStamp(%q)=%q, want %q", in, got, want)
		}
	}
}

// daysBetween precisa sobreviver a DST em fuso de viagem: no spring-forward
// de Lisboa o dia tem 23h — divisão por 24h truncaria "ontem" para "[hoje]".
func TestFormatHistoryTurn_DSTTransitionInTravelTZ(t *testing.T) {
	lis, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Skip("tzdata Europe/Lisbon indisponível")
	}
	// Spring forward em Lisboa: 2026-03-29 01:00 → 02:00.
	created := time.Date(2026, 3, 28, 22, 0, 0, 0, lis) // sábado à noite
	now := time.Date(2026, 3, 29, 10, 0, 0, 0, lis)     // domingo de manhã (dia de 23h)
	if got := formatHistoryTurn("oi", created.UTC(), now, lis); got != "[ontem 22:00] oi" {
		t.Fatalf("ontem através do spring-forward = %q, queria \"[ontem 22:00] oi\"", got)
	}
	// Span de 2 dias contendo a transição → carimbo absoluto, não "ontem".
	now2 := time.Date(2026, 3, 30, 10, 0, 0, 0, lis) // segunda
	if got := formatHistoryTurn("oi", created.UTC(), now2, lis); got != "[sáb 28/03 22:00] oi" {
		t.Fatalf("2 dias com DST no meio = %q, queria \"[sáb 28/03 22:00] oi\"", got)
	}
}
