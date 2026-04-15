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

func TestFormatReminder(t *testing.T) {
	ev := CalendarEvent{
		Title: "Reuniao com CEO",
		Start: time.Date(2026, 4, 10, 15, 0, 0, 0, time.UTC),
	}
	result := FormatReminder(ev)
	if !strings.Contains(result, "Reuniao com CEO") {
		t.Fatalf("reminder should contain event title, got: %s", result)
	}
	if !strings.Contains(result, "15:00") {
		t.Fatalf("reminder should contain time, got: %s", result)
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
