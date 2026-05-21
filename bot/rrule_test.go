package main

import (
	"strings"
	"testing"
	"time"
)

// =========================================================================
// ParseRRULE
// =========================================================================

func TestParseRRULE_HappyPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"daily 1x", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0"},
		{"daily 2x", "FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0"},
		{"weekly", "FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=9;BYMINUTE=0"},
		{"monthly", "FREQ=MONTHLY;BYHOUR=10;BYMINUTE=30"},
		{"with prefix", "RRULE:FREQ=DAILY;BYHOUR=8;BYMINUTE=0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr, err := ParseRRULE(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rr == nil {
				t.Fatal("expected non-nil RRule")
			}
		})
	}
}

func TestParseRRULE_RejectsBareFreq(t *testing.T) {
	_, err := ParseRRULE("FREQ=DAILY")
	if err == nil {
		t.Fatal("expected error for missing BYHOUR")
	}
	if !strings.Contains(err.Error(), "BYHOUR") {
		t.Fatalf("error should mention BYHOUR, got: %v", err)
	}
}

func TestParseRRULE_RejectsEmpty(t *testing.T) {
	if _, err := ParseRRULE(""); err == nil {
		t.Fatal("expected error for empty rrule")
	}
	if _, err := ParseRRULE("   "); err == nil {
		t.Fatal("expected error for whitespace-only rrule")
	}
}

func TestParseRRULE_RejectsUnsupportedFreq(t *testing.T) {
	cases := []string{
		"FREQ=YEARLY;BYHOUR=8",
		"FREQ=HOURLY;BYHOUR=8",
		"FREQ=MINUTELY;BYHOUR=8",
		"FREQ=SECONDLY;BYHOUR=8",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, err := ParseRRULE(c); err == nil {
				t.Fatal("expected error for unsupported freq")
			}
		})
	}
}

func TestParseRRULE_GarbageInputErrs(t *testing.T) {
	_, err := ParseRRULE("isso nao eh rrule")
	if err == nil {
		t.Fatal("expected error for garbage input")
	}
}

// =========================================================================
// ExpandOccurrences
// =========================================================================

func TestExpandOccurrences_DailyWithinWindow(t *testing.T) {
	loc := BRT()
	startDate := time.Date(2026, 5, 9, 0, 0, 0, 0, loc)
	sched := &MedicationSchedule{
		RRULE:     "FREQ=DAILY;BYHOUR=14;BYMINUTE=0",
		StartDate: startDate,
	}
	// Janela cobrindo 14:00 BRT exato.
	target := time.Date(2026, 5, 9, 14, 0, 0, 0, loc)
	windowStart := target.Add(-30 * time.Second)
	windowEnd := target.Add(30 * time.Second)

	occs, err := ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(occs) != 1 {
		t.Fatalf("expected 1 occurrence, got %d (occs=%v)", len(occs), occs)
	}
	// Tem que ser 14:00 BRT.
	got := occs[0].In(loc)
	if got.Hour() != 14 || got.Minute() != 0 {
		t.Fatalf("expected 14:00 BRT, got %v", got)
	}
}

func TestExpandOccurrences_OutsideWindow_Empty(t *testing.T) {
	loc := BRT()
	sched := &MedicationSchedule{
		RRULE:     "FREQ=DAILY;BYHOUR=14;BYMINUTE=0",
		StartDate: time.Date(2026, 5, 9, 0, 0, 0, 0, loc),
	}
	// Janela sem nenhuma ocorrencia (10:00..10:01 BRT).
	windowStart := time.Date(2026, 5, 9, 10, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 5, 9, 10, 1, 0, 0, loc)
	occs, err := ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 0 {
		t.Fatalf("expected 0, got %d", len(occs))
	}
}

func TestExpandOccurrences_RespectsEndDate(t *testing.T) {
	loc := BRT()
	endDate := time.Date(2026, 5, 8, 0, 0, 0, 0, loc)
	sched := &MedicationSchedule{
		RRULE:     "FREQ=DAILY;BYHOUR=14;BYMINUTE=0",
		StartDate: time.Date(2026, 5, 1, 0, 0, 0, 0, loc),
		EndDate:   &endDate,
	}
	// Procuramos em 09/05 (dia depois de end_date).
	windowStart := time.Date(2026, 5, 9, 13, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 5, 9, 15, 0, 0, 0, loc)
	occs, _ := ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if len(occs) != 0 {
		t.Fatalf("expected 0 (end_date past), got %d", len(occs))
	}
	// Na vespera (08/05) deve achar.
	windowStart = time.Date(2026, 5, 8, 13, 0, 0, 0, loc)
	windowEnd = time.Date(2026, 5, 8, 15, 0, 0, 0, loc)
	occs, _ = ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if len(occs) != 1 {
		t.Fatalf("expected 1 (within end_date), got %d", len(occs))
	}
}

func TestExpandOccurrences_TimezoneFidelity(t *testing.T) {
	// Caso classico do bug RRULE+timezone: se passamos loc=UTC mas usuario
	// quer 8h locais, BYHOUR=8 deve ser interpretado no fuso passado.
	brt := BRT()
	utc := time.UTC

	sched := &MedicationSchedule{
		RRULE:     "FREQ=DAILY;BYHOUR=8;BYMINUTE=0",
		StartDate: time.Date(2026, 5, 9, 0, 0, 0, 0, brt),
	}
	// Procuramos a ocorrencia no instante 11:00 UTC = 08:00 BRT (UTC-3).
	target := time.Date(2026, 5, 9, 11, 0, 0, 0, utc)
	windowStart := target.Add(-30 * time.Second)
	windowEnd := target.Add(30 * time.Second)

	occs, err := ExpandOccurrences(sched, windowStart, windowEnd, brt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(occs) != 1 {
		t.Fatalf("expected 1 occurrence in BRT 8h window, got %d", len(occs))
	}
}

func TestExpandOccurrences_WeeklyByDay(t *testing.T) {
	loc := BRT()
	// 2026-05-11 eh segunda. Regra: toda segunda 9h.
	sched := &MedicationSchedule{
		RRULE:     "FREQ=WEEKLY;BYDAY=MO;BYHOUR=9;BYMINUTE=0",
		StartDate: time.Date(2026, 5, 11, 0, 0, 0, 0, loc),
	}
	target := time.Date(2026, 5, 11, 9, 0, 0, 0, loc) // segunda
	windowStart := target.Add(-30 * time.Second)
	windowEnd := target.Add(30 * time.Second)
	occs, _ := ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if len(occs) != 1 {
		t.Fatalf("expected 1 occ on monday 9h, got %d", len(occs))
	}
	// Mesma janela na terca: nada.
	target = time.Date(2026, 5, 12, 9, 0, 0, 0, loc) // terca
	windowStart = target.Add(-30 * time.Second)
	windowEnd = target.Add(30 * time.Second)
	occs, _ = ExpandOccurrences(sched, windowStart, windowEnd, loc)
	if len(occs) != 0 {
		t.Fatalf("expected 0 occ on tuesday, got %d", len(occs))
	}
}

func TestExpandOccurrences_NilSchedule(t *testing.T) {
	_, err := ExpandOccurrences(nil, time.Now(), time.Now().Add(time.Minute), BRT())
	if err == nil {
		t.Fatal("expected error for nil schedule")
	}
}

func TestExpandOccurrences_DefaultLocWhenNil(t *testing.T) {
	loc := BRT()
	sched := &MedicationSchedule{
		RRULE:     "FREQ=DAILY;BYHOUR=14;BYMINUTE=0",
		StartDate: time.Date(2026, 5, 9, 0, 0, 0, 0, loc),
	}
	target := time.Date(2026, 5, 9, 14, 0, 0, 0, loc)
	windowStart := target.Add(-30 * time.Second)
	windowEnd := target.Add(30 * time.Second)

	// Loc=nil deve cair em BRT default e bater.
	occs, err := ExpandOccurrences(sched, windowStart, windowEnd, nil)
	if err != nil {
		t.Fatalf("expand with nil loc: %v", err)
	}
	if len(occs) != 1 {
		t.Fatalf("expected 1 occ, got %d", len(occs))
	}
}

// =========================================================================
// DescribeRRULE
// =========================================================================

func TestDescribeRRULE(t *testing.T) {
	cases := []struct {
		in   string
		want string // substring esperada
	}{
		{"FREQ=DAILY;BYHOUR=8;BYMINUTE=0", "todos os dias às 8h"},
		{"FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0", "8h e 20h"},
		{"FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0", "8h, 14h e 20h"},
		{"FREQ=WEEKLY;BYDAY=MO;BYHOUR=9;BYMINUTE=0", "segunda"},
		{"FREQ=MONTHLY;BYHOUR=10;BYMINUTE=0", "todo mês"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := DescribeRRULE(c.in)
			if !strings.Contains(got, c.want) {
				t.Fatalf("DescribeRRULE(%q) = %q, expected to contain %q", c.in, got, c.want)
			}
		})
	}
}

func TestDescribeRRULE_FallbackOnGarbage(t *testing.T) {
	in := "isso nao eh rrule"
	got := DescribeRRULE(in)
	if got != in {
		t.Fatalf("expected fallback to input, got %q", got)
	}
}

func TestDescribeRRULE_DailyInterval(t *testing.T) {
	got := DescribeRRULE("FREQ=DAILY;INTERVAL=3;BYHOUR=8;BYMINUTE=0")
	if !strings.Contains(got, "a cada 3 dias") {
		t.Fatalf("expected 'a cada 3 dias' in %q", got)
	}
}
