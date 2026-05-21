package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Fase 3.1: tolerancia + politica de dose atrasada
// =========================================================================

func TestCreateMedication_DefaultsToleranceAndPolicy(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m := &Medication{UserID: user.ID, Name: "Losartana", Dose: "50mg"}
	if err := db.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMedicationByID(m.ID)
	if got.ToleranceMinutes != DefaultToleranceMinutes {
		t.Fatalf("expected default tolerance %d, got %d", DefaultToleranceMinutes, got.ToleranceMinutes)
	}
	if got.LateDosePolicy != LatePolicyConsultDoctor {
		t.Fatalf("expected default policy consult_doctor, got %q", got.LateDosePolicy)
	}
}

func TestValidateLateDosePolicy(t *testing.T) {
	if p, err := ValidateLateDosePolicy(""); err != nil || p != LatePolicyConsultDoctor {
		t.Fatalf("empty should default to consult_doctor, got %q err %v", p, err)
	}
	for _, ok := range []string{"consult_doctor", "skip", "take_keep_next", "take_recalculate"} {
		if _, err := ValidateLateDosePolicy(ok); err != nil {
			t.Fatalf("%q should be valid: %v", ok, err)
		}
	}
	if _, err := ValidateLateDosePolicy("nonsense"); err == nil {
		t.Fatal("invalid policy should error")
	}
}

func TestShiftRRULEHours_Daily(t *testing.T) {
	// 8h,14h,20h deslocado +1h05 -> 9h05,15h05,21h05.
	out, err := shiftRRULEHours("FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0", 65*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "BYHOUR=9,15,21") || !strings.Contains(out, "BYMINUTE=5") {
		t.Fatalf("unexpected shifted rrule: %q", out)
	}
	if !strings.Contains(out, "FREQ=DAILY") {
		t.Fatalf("should preserve FREQ: %q", out)
	}
}

func TestShiftRRULEHours_WeeklyPreservesDays(t *testing.T) {
	out, err := shiftRRULEHours("FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=9;BYMINUTE=0", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FREQ=WEEKLY") {
		t.Fatalf("should preserve FREQ=WEEKLY: %q", out)
	}
	if !strings.Contains(out, "BYDAY=MO,WE") {
		t.Fatalf("should preserve BYDAY: %q", out)
	}
	if !strings.Contains(out, "BYHOUR=9") || !strings.Contains(out, "BYMINUTE=30") {
		t.Fatalf("unexpected shift: %q", out)
	}
}

func TestBuildMedicationPolicyPrompt(t *testing.T) {
	meds := []Medication{
		{Name: "Losartana", LateDosePolicy: LatePolicyConsultDoctor},
		{Name: "Metformina", LateDosePolicy: LatePolicySkip},
		{Name: "AAS", LateDosePolicy: LatePolicyTakeRecalculate},
	}
	block := buildMedicationPolicyPrompt(meds)
	if !strings.Contains(block, "Metformina") || !strings.Contains(block, "AAS") {
		t.Fatalf("block should list configured meds: %q", block)
	}
	if strings.Contains(block, "Losartana") {
		t.Fatalf("consult_doctor med should be omitted: %q", block)
	}
	if !strings.Contains(strings.ToLower(block), "recomendação do responsável") {
		t.Fatalf("block must frame as guardian recommendation: %q", block)
	}

	// Sem politicas configuradas -> bloco vazio.
	none := buildMedicationPolicyPrompt([]Medication{
		{Name: "X", LateDosePolicy: LatePolicyConsultDoctor},
	})
	if none != "" {
		t.Fatalf("no configured policies should yield empty block, got %q", none)
	}
}

// take_recalculate: tomar atrasado reancorra os horarios e avisa o titular.
func TestMarcarRemedioTomado_RecalculateReschedules(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]

	m := &Medication{
		UserID:         user.ID,
		Name:           "Losartana",
		Dose:           "50mg",
		LateDosePolicy: LatePolicyTakeRecalculate,
	}
	if err := db.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	sched := &MedicationSchedule{
		MedicationID: m.ID,
		RRULE:        "FREQ=DAILY;BYHOUR=8;BYMINUTE=0",
		StartDate:    time.Date(2026, 5, 9, 0, 0, 0, 0, BRT()),
	}
	if err := db.CreateMedicationSchedule(sched); err != nil {
		t.Fatal(err)
	}

	// Pending agendado ~70min atras (tomou atrasado).
	scheduledAt := time.Now().UTC().Add(-70 * time.Minute)
	mkMedicationPending(t, db, user, m, scheduledAt, "medication_default")

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "reagend") {
		t.Fatalf("response should mention rescheduling: %q", resp)
	}
	if !strings.Contains(strings.ToLower(resp), "painel") {
		t.Fatalf("response should mention reverting via painel: %q", resp)
	}

	// RRULE deve ter mudado (deslocado ~70min de 8h00).
	scheds, _ := db.ListSchedulesForMedication(m.ID)
	if scheds[0].RRULE == "FREQ=DAILY;BYHOUR=8;BYMINUTE=0" {
		t.Fatalf("schedule rrule should have shifted, still %q", scheds[0].RRULE)
	}
}

// take_keep_next NAO reagenda — so registra.
func TestMarcarRemedioTomado_KeepNextDoesNotReschedule(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m := &Medication{
		UserID:         user.ID,
		Name:           "Losartana",
		LateDosePolicy: LatePolicyTakeKeepNext,
	}
	if err := db.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	orig := "FREQ=DAILY;BYHOUR=8;BYMINUTE=0"
	if err := db.CreateMedicationSchedule(&MedicationSchedule{
		MedicationID: m.ID, RRULE: orig, StartDate: time.Date(2026, 5, 9, 0, 0, 0, 0, BRT()),
	}); err != nil {
		t.Fatal(err)
	}
	mkMedicationPending(t, db, user, m, time.Now().UTC().Add(-70*time.Minute), "medication_default")

	agent := mkTestAgent(t, db)
	if _, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	scheds, _ := db.ListSchedulesForMedication(m.ID)
	if scheds[0].RRULE != orig {
		t.Fatalf("keep_next must not reschedule, rrule changed to %q", scheds[0].RRULE)
	}
}

// adiar_remedio grava deferred_until sem marcar como tomado.
func TestAdiarRemedio_SetsDeferredWithoutTaking(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, time.Now().UTC(), "medication_default")

	agent := mkTestAgent(t, db)
	if _, err := handleAdiarRemedio(context.Background(), agent, user, json.RawMessage(`{"daqui_minutos": 30}`)); err != nil {
		t.Fatal(err)
	}

	loaded, _ := loadPCByID(t, db, pc.ID)
	if loaded.DeferredUntil == nil {
		t.Fatal("deferred_until should be set")
	}
	if loaded.Status != "pending" {
		t.Fatalf("adiar must NOT resolve the pending, status=%s", loaded.Status)
	}
	// intake nao deve estar 'taken'.
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status == IntakeTaken {
		t.Fatal("adiar must not mark intake as taken")
	}
}
