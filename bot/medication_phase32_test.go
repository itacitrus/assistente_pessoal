package main

import (
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Fase 3.2: require_confirmation + status unknown + mensagens agrupadas
// =========================================================================

// TestMarkStaleNoConfirmDosesUnknown: remedio que NAO exige confirmacao, dose
// pendente passada a tolerancia -> vira 'unknown'. Remedio que exige fica como
// esta (o sweeper nao mexe nele — quem cuida eh a escalacao).
func TestMarkStaleNoConfirmDosesUnknown(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "A")[0]

	noConfirm := &Medication{UserID: user.ID, Name: "Vitamina D", Dose: "1 gota", RequireConfirmation: false}
	if err := db.CreateMedication(noConfirm); err != nil {
		t.Fatal(err)
	}
	requireConfirm := &Medication{UserID: user.ID, Name: "Losartana", Dose: "50mg", RequireConfirmation: true}
	if err := db.CreateMedication(requireConfirm); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	stale := now.Add(-31 * time.Minute) // > tolerancia default (30)
	for _, m := range []*Medication{noConfirm, requireConfirm} {
		if err := db.CreateIntakeLogIfAbsent(m.ID, stale, IntakePending); err != nil {
			t.Fatal(err)
		}
	}

	n, err := db.MarkStaleNoConfirmDosesUnknown(now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 dose marked unknown, got %d", n)
	}

	nc, _ := db.ListIntakeLogsForMedication(noConfirm.ID, 1)
	if nc[0].Status != IntakeUnknown {
		t.Fatalf("no-confirm dose should be unknown, got %s", nc[0].Status)
	}
	rc, _ := db.ListIntakeLogsForMedication(requireConfirm.ID, 1)
	if rc[0].Status != IntakePending {
		t.Fatalf("require-confirm dose must stay pending (escalation owns it), got %s", rc[0].Status)
	}
}

// TestMarkStaleNoConfirmDosesUnknown_RespectsWindow: antes da tolerancia, nada.
func TestMarkStaleNoConfirmDosesUnknown_RespectsWindow(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "A")[0]
	m := &Medication{UserID: user.ID, Name: "Vitamina D", RequireConfirmation: false}
	if err := db.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.CreateIntakeLogIfAbsent(m.ID, now.Add(-5*time.Minute), IntakePending); err != nil {
		t.Fatal(err)
	}
	n, err := db.MarkStaleNoConfirmDosesUnknown(now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("dose still within tolerance must not be marked, got %d", n)
	}
}

// TestBuildMedicationReminderMessage_Grouped: varios remedios -> uma mensagem
// listando todos; pergunta de confirmacao some quando nenhum exige.
func TestBuildMedicationReminderMessage_Grouped(t *testing.T) {
	meds := []*Medication{
		{Name: "Losartana", Dose: "50mg", RequireConfirmation: true},
		{Name: "Aradois", Dose: "1 comprimido", RequireConfirmation: false},
	}
	msg := buildMedicationReminderMessage("João da Silva", meds, true)
	if !strings.Contains(msg, "Losartana") || !strings.Contains(msg, "Aradois") {
		t.Fatalf("grouped message should list all meds: %q", msg)
	}
	if !strings.Contains(msg, "Pode confirmar quando tomar?") {
		t.Fatalf("should ask confirmation when any med requires it: %q", msg)
	}

	// Nenhum exige confirmacao -> sem cobranca.
	noAsk := buildMedicationReminderMessage("João", meds, false)
	if strings.Contains(noAsk, "confirmar") {
		t.Fatalf("must not ask confirmation when none required: %q", noAsk)
	}
}

// TestProcessPendings_GroupedNudge: dois remedios no mesmo horario geram UM
// cutucao listando ambos (nao dois cutucoes).
func TestProcessPendings_GroupedNudge(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m1, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	m2, _ := mkMedForUser(t, db, user, "Aradois", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	mkMedicationPending(t, db, user, m1, testSched, "medication_default")
	mkMedicationPending(t, db, user, m2, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	pendings, _ := db.GetActiveMedicationPendings()
	eng.ProcessPendings(testNudgeAt.Add(time.Minute), pendings)

	if len(notif.Sent()) != 1 {
		t.Fatalf("expected exactly 1 grouped nudge, got %d", len(notif.Sent()))
	}
	body := notif.Sent()[0].Body
	if !strings.Contains(body, "Losartana") || !strings.Contains(body, "Aradois") {
		t.Fatalf("grouped nudge should mention both meds: %q", body)
	}
	if strings.Contains(strings.ToLower(body), "família") {
		t.Fatalf("nudge must not mention family: %q", body)
	}
}

// TestProcessPendings_GroupedFamily: no deadline, o guardiao recebe UMA
// mensagem listando os dois remedios nao confirmados, e ambas as doses ficam
// 'escalated'.
func TestProcessPendings_GroupedFamily(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha")
	idosa, filha := users[0], users[1]
	if _, err := db.LinkFamily(filha.ID, idosa.ID, "filha"); err != nil {
		t.Fatal(err)
	}
	m1, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	m2, _ := mkMedForUser(t, db, idosa, "Aradois", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	mkMedicationPending(t, db, idosa, m1, testSched, "medication_default")
	mkMedicationPending(t, db, idosa, m2, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	pendings, _ := db.GetActiveMedicationPendings()
	eng.ProcessPendings(testDeadline.Add(time.Minute), pendings)

	guardianMsgs := 0
	for _, s := range notif.Sent() {
		if s.Recipient != nil && s.Recipient.ID == filha.ID {
			guardianMsgs++
			if !strings.Contains(s.Body, "Losartana") || !strings.Contains(s.Body, "Aradois") {
				t.Errorf("grouped family msg should list both meds: %q", s.Body)
			}
			if !strings.Contains(strings.ToLower(s.Body), "não confirm") {
				t.Errorf("family msg should say 'não confirm...': %q", s.Body)
			}
			if !strings.Contains(strings.ToLower(s.Body), "não oriento") {
				t.Errorf("family msg should keep safety phrase 'não oriento': %q", s.Body)
			}
		}
	}
	if guardianMsgs != 1 {
		t.Fatalf("expected exactly 1 grouped family message, got %d", guardianMsgs)
	}

	for _, m := range []*Medication{m1, m2} {
		logs, _ := db.ListIntakeLogsForMedication(m.ID, 1)
		if logs[0].Status != IntakeEscalated {
			t.Fatalf("med %s dose should be escalated, got %s", m.Name, logs[0].Status)
		}
	}
}
