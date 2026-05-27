package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// fakeAnalysisProvider eh um stub de llm.AnalysisProvider — devolve JSON
// fixo. Permite testar snapshot_writer adapter sem chamar Haiku real.
type fakeAnalysisProvider struct {
	out      synthesis.SnapshotOutput
	err      error
	calls    int
	captured llm.AnalysisRequest
}

func (f *fakeAnalysisProvider) Analyze(_ context.Context, req llm.AnalysisRequest) (llm.AnalysisResponse, error) {
	f.calls++
	f.captured = req
	if f.err != nil {
		return llm.AnalysisResponse{}, f.err
	}
	b, _ := json.Marshal(f.out)
	return llm.AnalysisResponse{JSON: b}, nil
}
func (f *fakeAnalysisProvider) Name() string { return "fake" }

func TestSnapshotWriter_WritesPersistsSnapshot(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	db.AddConversationMessage(elder.ID, "user", "ola, tudo bem?")

	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{
		HumorScore: 4, Confidence: 3,
		HumorNuance:      "tom estavel",
		SinaisObservados: []string{"caminhada matinal"},
	}}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)

	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("MaybeUpdateSnapshot: %v", err)
	}
	if analysis.calls != 1 {
		t.Errorf("expected 1 analysis call, got %d", analysis.calls)
	}
	tz := db.GetEventTimezone(elder.ID, time.Now())
	today := time.Now().In(tz)
	dayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, tz)
	got, err := db.GetSnapshot(elder.ID, dayDate)
	if err != nil || got == nil {
		t.Fatalf("GetSnapshot: err=%v got=%v", err, got)
	}
	if got.HumorScore != 4 {
		t.Errorf("HumorScore: %d", got.HumorScore)
	}
}

func TestSnapshotWriter_SkipsForNonElder(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111") // type=comum
	db.AddConversationMessage(guardian.ID, "user", "msg")

	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{HumorScore: 4, Confidence: 3}}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)

	if err := w.MaybeUpdateSnapshot(context.Background(), guardian.ID); err != nil {
		t.Fatalf("expected no err for non-elder, got %v", err)
	}
	if analysis.calls != 0 {
		t.Errorf("expected 0 analysis calls (non-elder), got %d", analysis.calls)
	}
}

func TestSnapshotWriter_SkipsConsentRevokedForAllGuardians(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	g1 := makeGuardian(t, db, "Caio", "222")
	g2 := makeGuardian(t, db, "Beto", "333")
	db.LinkFamily(g1.ID, elder.ID, "filho_de")
	db.LinkFamily(g2.ID, elder.ID, "filha_de")
	db.SetDependentConsent(g1.ID, elder.ID, ConsentRevoked)
	db.SetDependentConsent(g2.ID, elder.ID, ConsentRevoked)
	db.AddConversationMessage(elder.ID, "user", "msg")

	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{HumorScore: 4, Confidence: 3}}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)

	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("expected no err, got %v", err)
	}
	if analysis.calls != 0 {
		t.Errorf("expected 0 analysis calls (all consent revoked), got %d", analysis.calls)
	}
}

func TestSnapshotWriter_RunsWhenAtLeastOneConsentActive(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	g1 := makeGuardian(t, db, "Caio", "222")
	g2 := makeGuardian(t, db, "Beto", "333")
	db.LinkFamily(g1.ID, elder.ID, "filho_de")
	db.LinkFamily(g2.ID, elder.ID, "filha_de")
	// Apenas g1 revoga.
	db.SetDependentConsent(g1.ID, elder.ID, ConsentRevoked)
	db.AddConversationMessage(elder.ID, "user", "msg")

	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{HumorScore: 4, Confidence: 3}}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)

	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("expected no err, got %v", err)
	}
	if analysis.calls != 1 {
		t.Errorf("expected 1 call (g2 still active), got %d", analysis.calls)
	}
}

func TestSnapshotWriter_NoMessagesNoCall(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	// SEM mensagens
	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{HumorScore: 4, Confidence: 3}}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)
	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("expected no err, got %v", err)
	}
	if analysis.calls != 0 {
		t.Errorf("expected 0 calls (no msgs), got %d", analysis.calls)
	}
}

func TestSnapshotWriter_NoAnalysisProvider(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	db.AddConversationMessage(elder.ID, "user", "msg")

	w := NewSnapshotWriter(db, NewAuditLog(db), nil, nil)
	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("expected no err, got %v", err)
	}
}

func TestSnapshotWriter_AuditOnFailure(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	db.AddConversationMessage(elder.ID, "user", "msg")

	// Output invalido (citacao literal) → ValidateSnapshotOutput rejeita.
	bad := synthesis.SnapshotOutput{
		HumorScore:       3,
		Confidence:       3,
		SinaisObservados: []string{`ela disse "estou cansada hoje"`},
	}
	analysis := &fakeAnalysisProvider{out: bad}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, nil)

	err := w.MaybeUpdateSnapshot(context.Background(), elder.ID)
	if err == nil {
		t.Fatal("expected validation error to bubble up")
	}
	rows, _ := db.conn.Query(`SELECT action FROM action_log WHERE user_id = ? AND action = 'psych_snapshot_failed'`, elder.ID)
	defer rows.Close()
	if !rows.Next() {
		t.Error("expected psych_snapshot_failed in audit_log")
	}
}

func TestSnapshotWriter_SafetyAlertFromWriter_PassesCategoryThrough(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	db.AddConversationMessage(elder.ID, "user", "to com dor no peito")

	analysis := &fakeAnalysisProvider{out: synthesis.SnapshotOutput{
		HumorScore: 2,
		Confidence: 3,
		EventosDia: []string{"queixa de dor toracica"},
		SafetyAlertNeeded: &synthesis.SafetyAlert{
			Severity:    "critical",
			Category:    "medico_fisico",
			Reason:      "dor toracica recorrente",
			Recommended: "considerar emergencia",
		},
	}}
	var sentMsgs []string
	sendMsg := func(phone, text string) error {
		sentMsgs = append(sentMsgs, phone+":"+text)
		return nil
	}
	w := NewSnapshotWriter(db, NewAuditLog(db), analysis, sendMsg)

	if err := w.MaybeUpdateSnapshot(context.Background(), elder.ID); err != nil {
		t.Fatalf("MaybeUpdateSnapshot: %v", err)
	}

	if len(sentMsgs) != 1 {
		t.Fatalf("expected 1 msg sent to guardian, got %d", len(sentMsgs))
	}
	// Audit log:
	rows, _ := db.conn.Query(`SELECT action, details FROM action_log WHERE user_id = ? AND action = 'safety_alert_from_writer'`, elder.ID)
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected safety_alert_from_writer in audit_log")
	}
	var act, details string
	rows.Scan(&act, &details)
	if !strings.Contains(details, "category=medico_fisico") {
		t.Errorf("expected category=medico_fisico in details, got: %s", details)
	}

	// Conferir que escalation row foi criada com policy_name=severe_signal_safety_net.
	escRows, _ := db.conn.Query(`SELECT policy_name FROM escalations WHERE user_id = ?`, elder.ID)
	defer escRows.Close()
	found := false
	for escRows.Next() {
		var p string
		escRows.Scan(&p)
		if p == "severe_signal_safety_net" {
			found = true
		}
	}
	if !found {
		t.Error("expected severe_signal_safety_net escalation row")
	}
}

func TestCountSessions(t *testing.T) {
	now := time.Now()
	msgs := []synthesis.ConversationMessage{
		{Timestamp: now},
		{Timestamp: now.Add(5 * time.Minute)},
		{Timestamp: now.Add(45 * time.Minute)}, // gap > 30min: nova sessao
		{Timestamp: now.Add(55 * time.Minute)},
		{Timestamp: now.Add(5 * time.Hour)}, // outra sessao
	}
	if got := countSessions(msgs); got != 3 {
		t.Errorf("expected 3 sessions, got %d", got)
	}
	if got := countSessions(nil); got != 0 {
		t.Errorf("expected 0 for nil, got %d", got)
	}
}

func TestEstimateDurationMinutes(t *testing.T) {
	now := time.Now()
	msgs := []synthesis.ConversationMessage{
		{Timestamp: now},
		{Timestamp: now.Add(5 * time.Minute)}, // sessao 1: 5min
		{Timestamp: now.Add(60 * time.Minute)},
		{Timestamp: now.Add(70 * time.Minute)}, // sessao 2: 10min
	}
	got := estimateDurationMinutes(msgs)
	if got != 15 {
		t.Errorf("expected 15min total, got %d", got)
	}
	if estimateDurationMinutes(nil) != 0 {
		t.Errorf("expected 0 for nil")
	}
	single := []synthesis.ConversationMessage{{Timestamp: now}}
	if estimateDurationMinutes(single) != 0 {
		t.Errorf("expected 0 for single msg")
	}
}

func TestFilterMessagesInLocalDay(t *testing.T) {
	tz := BRT()
	dayStart := time.Date(2026, 5, 9, 0, 0, 0, 0, tz)
	dayEnd := dayStart.Add(24 * time.Hour)

	msgs := []synthesis.ConversationMessage{
		{Timestamp: dayStart.Add(-2 * time.Hour)}, // dia anterior
		{Timestamp: dayStart.Add(2 * time.Hour)},  // dentro do dia
		{Timestamp: dayStart.Add(20 * time.Hour)}, // dentro
		{Timestamp: dayEnd.Add(2 * time.Hour)},    // proximo dia
	}
	out := filterMessagesInLocalDay(msgs, dayStart, tz)
	if len(out) != 2 {
		t.Errorf("expected 2, got %d", len(out))
	}
}

func TestSnapshotWriter_WithNowSetsClock(t *testing.T) {
	w := NewSnapshotWriter(nil, nil, nil, nil)
	fixed := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	w.withNow(func() time.Time { return fixed })
	if got := w.nowFunc(); !got.Equal(fixed) {
		t.Errorf("expected %v, got %v", fixed, got)
	}
	// nil → time.Now default.
	w.withNow(nil)
	if w.nowFunc == nil {
		t.Error("withNow(nil) should set default time.Now")
	}
}

func TestSplitMedicationIntake(t *testing.T) {
	in := []synthesis.MedicationIntake{
		{Status: "taken"},
		{Status: "taken"},
		{Status: "missed"},
		{Status: "skipped"},
		{Status: "pending"},
		{Status: "escalated"},
	}
	taken, missed := splitMedicationIntake(in)
	if len(taken) != 2 {
		t.Errorf("expected 2 taken, got %d", len(taken))
	}
	if len(missed) != 2 {
		t.Errorf("expected 2 missed (incluindo escalated), got %d", len(missed))
	}
}
