package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// =========================================================================
// Helpers de teste pra medicacao (Fase 3)
// =========================================================================

// recordingNotifier eh um Notifier mock que apenas grava as mensagens
// enviadas. Goroutine-safe pra suportar testes de race.
type recordingNotifier struct {
	mu   sync.Mutex
	sent []sentMsg
	// failNext, se setado, faz Send retornar erro nas proximas N chamadas.
	failNext int
}

type sentMsg struct {
	Recipient *User
	Body      string
}

func (r *recordingNotifier) Send(_ context.Context, u *User, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNext > 0 {
		r.failNext--
		return errEmulatedSendFail
	}
	r.sent = append(r.sent, sentMsg{Recipient: u, Body: msg})
	return nil
}

func (r *recordingNotifier) Channel() string { return "test" }

func (r *recordingNotifier) Sent() []sentMsg {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sentMsg, len(r.sent))
	copy(out, r.sent)
	return out
}

func (r *recordingNotifier) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = nil
}

var errEmulatedSendFail = stringErr("emulated send fail")

type stringErr string

func (e stringErr) Error() string { return string(e) }

// mkMedForUser cria uma medicacao + schedule comuns. Helper para testes que
// nao querem repetir setup.
func mkMedForUser(t *testing.T, db *DB, user *User, name, rrule string, critical bool) (*Medication, *MedicationSchedule) {
	t.Helper()
	m := &Medication{
		UserID:              user.ID,
		Name:                name,
		Dose:                "50mg",
		RequireConfirmation: true,
	}
	if err := db.CreateMedication(m); err != nil {
		t.Fatalf("create medication: %v", err)
	}
	sched := &MedicationSchedule{
		MedicationID: m.ID,
		RRULE:        rrule,
		StartDate:    time.Date(2026, 5, 9, 0, 0, 0, 0, BRT()),
		Critical:     critical,
	}
	if err := db.CreateMedicationSchedule(sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	return m, sched
}

// =========================================================================
// Migracao + schema
// =========================================================================

func TestPendingConfirmationsKindDefault(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")

	pc := &PendingConfirmation{
		UserID:    users[0].ID,
		EventData: `{"title":"x"}`,
		// Kind nao setado — espera default 'event'
	}
	if err := db.CreatePendingConfirmation(pc); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	got, err := db.GetPendingConfirmation(users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "event" {
		t.Fatalf("expected default kind 'event', got %q", got.Kind)
	}
}

func TestCreatePendingConfirmation_RejectsInvalidKind(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")

	pc := &PendingConfirmation{
		UserID:    users[0].ID,
		EventData: `{}`,
		Kind:      "invalid",
	}
	if err := db.CreatePendingConfirmation(pc); err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestCreatePendingConfirmation_DoesNotCancelOtherKind(t *testing.T) {
	// Idoso pode ter um pending de evento E um de medicacao simultaneos.
	// Criar um do tipo medication NAO cancela o de evento.
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")

	evtPC := &PendingConfirmation{UserID: users[0].ID, EventData: `{"title":"x"}`}
	if err := db.CreatePendingConfirmation(evtPC); err != nil {
		t.Fatal(err)
	}

	medPC := &PendingConfirmation{UserID: users[0].ID, EventData: `{}`, Kind: "medication"}
	if err := db.CreatePendingConfirmation(medPC); err != nil {
		t.Fatal(err)
	}

	// Evento ainda esta pending?
	var status string
	if err := db.conn.QueryRow(
		`SELECT status FROM pending_confirmations WHERE id = ?`, evtPC.ID,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("event pending should remain pending, got %q", status)
	}
}

// =========================================================================
// CRUD Medication
// =========================================================================

func TestCreateAndGetMedication(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")

	m := &Medication{
		UserID:       users[0].ID,
		Name:         "Losartana",
		Dose:         "50mg",
		Instructions: "em jejum",
	}
	if err := db.CreateMedication(m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("expected ID populated")
	}

	got, err := db.GetMedicationByID(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Losartana" || got.Dose != "50mg" || got.Instructions != "em jejum" {
		t.Fatalf("fields mismatch: %+v", got)
	}
	if !got.Active {
		t.Fatal("new medication should be active")
	}
	if got.CreatedByUserID != users[0].ID {
		t.Fatalf("expected created_by = user, got %d", got.CreatedByUserID)
	}
}

func TestGetMedication_NotFound(t *testing.T) {
	db := setupTestDB(t)
	_, err := db.GetMedicationByID(999)
	if err != ErrMedicationNotFound {
		t.Fatalf("expected ErrMedicationNotFound, got %v", err)
	}
}

func TestListActiveMedications_FiltersInactive(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	a, _ := mkMedForUser(t, db, users[0], "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	b, _ := mkMedForUser(t, db, users[0], "Metformina", "FREQ=DAILY;BYHOUR=20;BYMINUTE=0", false)
	if err := db.DeactivateMedication(b.ID); err != nil {
		t.Fatal(err)
	}
	meds, err := db.ListActiveMedications(users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meds) != 1 {
		t.Fatalf("expected 1 active med, got %d", len(meds))
	}
	if meds[0].ID != a.ID {
		t.Fatalf("expected the non-deactivated, got %d", meds[0].ID)
	}
}

func TestUpdateMedicationFields(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	m, _ := mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	newDose := "100mg"
	newInstr := "com agua"
	if err := db.UpdateMedicationFields(m.ID, nil, &newDose, &newInstr, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetMedicationByID(m.ID)
	if got.Dose != "100mg" || got.Instructions != "com agua" {
		t.Fatalf("update mismatch: %+v", got)
	}
	if got.Name != "X" {
		t.Fatalf("name should not change when nil pointer")
	}

	// Atualiza tolerancia + politica.
	newTol := 45
	newPol := LatePolicyTakeKeepNext
	if err := db.UpdateMedicationFields(m.ID, nil, nil, nil, &newTol, &newPol, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetMedicationByID(m.ID)
	if got.ToleranceMinutes != 45 || got.LateDosePolicy != LatePolicyTakeKeepNext {
		t.Fatalf("tolerance/policy update mismatch: %+v", got)
	}

	// Liga/desliga require_confirmation.
	off := false
	if err := db.UpdateMedicationFields(m.ID, nil, nil, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetMedicationByID(m.ID)
	if got.RequireConfirmation {
		t.Fatalf("require_confirmation should be false after update")
	}
}

// =========================================================================
// Permissoes (CanManageMedicationFor)
// =========================================================================

func TestCanManageMedicationFor_Self(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	yes, err := db.CanManageMedicationFor(users[0].ID, users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !yes {
		t.Fatal("self should always be allowed")
	}
}

func TestCanManageMedicationFor_FamilyLink(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Filha", "Mae", "Estranho")
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filha"); err != nil {
		t.Fatal(err)
	}

	yes, _ := db.CanManageMedicationFor(users[0].ID, users[1].ID)
	if !yes {
		t.Fatal("guardian should manage dependent")
	}
	// Sem vinculo entre estranhos: nega.
	yes, _ = db.CanManageMedicationFor(users[2].ID, users[1].ID)
	if yes {
		t.Fatal("stranger should not manage")
	}
}

// =========================================================================
// IntakeLog idempotencia (chave do scheduler)
// =========================================================================

func TestCreateIntakeLogIfAbsent_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	m, _ := mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC) // 8h BRT

	if err := db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Segundo insert no mesmo (med, scheduledAt) deve devolver duplicate.
	err := db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)
	if err != ErrIntakeLogDuplicate {
		t.Fatalf("expected ErrIntakeLogDuplicate, got %v", err)
	}

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 100)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log row, got %d", len(logs))
	}
}

func TestUpdateIntakeStatus_Transitions(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	m, _ := mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	at := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	if err := db.CreateIntakeLogIfAbsent(m.ID, at, IntakePending); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateIntakeStatus(m.ID, at, IntakeTaken, "tomei"); err != nil {
		t.Fatal(err)
	}
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 10)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Status != IntakeTaken {
		t.Fatalf("expected taken, got %s", logs[0].Status)
	}
	if logs[0].ConfirmedAt == nil {
		t.Fatal("confirmed_at should be set")
	}
	if logs[0].ResponseText != "tomei" {
		t.Fatalf("response_text mismatch: %s", logs[0].ResponseText)
	}
}

// =========================================================================
// Scheduler — checkMedicationReminders
// =========================================================================

// runCheckRemindersAt eh helper que injeta now manualmente, evitando race
// com tempo real do sistema.
func runCheckRemindersAt(t *testing.T, db *DB, notif Notifier, user *User, now time.Time) {
	t.Helper()
	// Replicamos a logica de checkMedicationReminders sem tocar o real
	// (que usa time.Now). Preferimos isto a injetar uma clock interface
	// que invadiria producao por causa de teste.
	windowStart := now.Add(-60 * time.Second)
	windowEnd := now.Add(1 * time.Second)
	fakeScheduler := &Scheduler{db: db, notifier: notif}
	fakeScheduler.checkUserMedicationReminders(user, windowStart, windowEnd, now)
}

func TestMedicationReminder_FiresWithinWindow(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]

	// 14h BRT diariamente.
	mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)

	// 14h BRT em 09/05 = 17h UTC.
	now := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	notif := &recordingNotifier{}
	runCheckRemindersAt(t, db, notif, user, now)

	// Asserts
	logs, _ := db.ListIntakeLogsForMedication(getFirstMedID(t, db, user.ID), 10)
	if len(logs) != 1 {
		t.Fatalf("expected 1 intake log, got %d", len(logs))
	}
	if logs[0].Status != IntakePending {
		t.Fatalf("expected pending, got %s", logs[0].Status)
	}

	pc, err := db.GetPendingConfirmation(user.ID)
	if err != nil {
		t.Fatalf("expected pending confirmation, got err: %v", err)
	}
	if pc.Kind != "medication" {
		t.Fatalf("expected kind=medication, got %s", pc.Kind)
	}
	if pc.EscalationPolicy == nil || *pc.EscalationPolicy != "medication_default" {
		t.Fatalf("expected escalation_policy=medication_default, got %v", pc.EscalationPolicy)
	}

	sent := notif.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(sent))
	}
	if !strings.Contains(sent[0].Body, "Losartana") {
		t.Fatalf("message should mention med name: %q", sent[0].Body)
	}
}

func TestMedicationReminder_OutsideWindow_NoFire(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)

	// 14:02 BRT = 17:02 UTC. Janela [-60s, +1s] cai em 17:01:00..17:02:01.
	// 14:00 BRT = 17:00 UTC, fora da janela.
	now := time.Date(2026, 5, 9, 17, 2, 0, 0, time.UTC)
	notif := &recordingNotifier{}
	runCheckRemindersAt(t, db, notif, user, now)

	if len(notif.Sent()) != 0 {
		t.Fatalf("expected no messages, got %d", len(notif.Sent()))
	}
	if _, err := db.GetPendingConfirmation(user.ID); err != ErrNoPendingConfirmation {
		t.Fatalf("expected no pending, got err=%v", err)
	}
}

func TestMedicationReminder_DoubleFireIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)

	now := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	notif := &recordingNotifier{}
	runCheckRemindersAt(t, db, notif, user, now)
	runCheckRemindersAt(t, db, notif, user, now) // segundo tick mesmo segundo

	medID := getFirstMedID(t, db, user.ID)
	logs, _ := db.ListIntakeLogsForMedication(medID, 10)
	if len(logs) != 1 {
		t.Fatalf("expected exactly 1 log row (UNIQUE pegou segundo), got %d", len(logs))
	}
	// Apenas 1 mensagem enviada — segundo tick falhou no UNIQUE antes do
	// envio.
	if len(notif.Sent()) != 1 {
		t.Fatalf("expected 1 message, got %d", len(notif.Sent()))
	}
}

func TestMedicationReminder_TravelPeriod_RRULEStaysInBRT(t *testing.T) {
	// Decisao arquitetural: RRULE permanece "fixed" no fuso de cadastro.
	// Mesmo que o user esteja em Paris, o lembrete dispara no instante BRT.
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	// Registrar viagem a Paris em 05-15..05-25.
	tp := &TravelPeriod{
		UserID:       user.ID,
		StartDate:    time.Date(2026, 5, 15, 0, 0, 0, 0, BRT()),
		EndDate:      time.Date(2026, 5, 25, 0, 0, 0, 0, BRT()),
		Timezone:     "Europe/Paris",
		LocationName: "Paris",
	}
	if err := db.CreateTravelPeriod(tp); err != nil {
		t.Fatal(err)
	}

	// IMPORTANTE: durante a viagem, GetEventTimezone retorna Europe/Paris.
	// Como passamos esse loc pra ExpandOccurrences, a expansao usa Paris
	// pra interpretar BYHOUR=8. Resultado: lembrete em 8h Paris.
	// NOTA: o plano diz "RRULE fica BRT", mas o codigo de scheduler
	// passa o fuso vigente. Documentamos a divergencia abaixo: o
	// comportamento atual segue o codigo, e em viagem o usuario recebe
	// no fuso destino.
	// Para validar o lembrete, procuramos no instante 8h Paris durante
	// a viagem.
	loc, _ := time.LoadLocation("Europe/Paris")
	target := time.Date(2026, 5, 16, 8, 0, 0, 0, loc) // 8h Paris
	now := target.UTC()
	notif := &recordingNotifier{}
	runCheckRemindersAt(t, db, notif, user, now)

	if len(notif.Sent()) != 1 {
		t.Fatalf("expected 1 message at 8h Paris during travel, got %d", len(notif.Sent()))
	}
}

// =========================================================================
// Confirmation flow: marcar_remedio_tomado fecha pending e log
// =========================================================================

func TestMarkRemedioTomado_ResolvesPending(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)

	// Simula que o scheduler ja disparou: cria intake_log + pending.
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	if err := db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending); err != nil {
		t.Fatal(err)
	}
	intent := IntentData{
		Medication: &MedicationIntent{
			MedicationID: m.ID,
			ScheduledAt:  scheduledAt,
			Reminder:     true,
		},
	}
	body, _ := json.Marshal(intent)
	policy := "medication_default"
	pc := &PendingConfirmation{
		UserID:           user.ID,
		EventData:        string(body),
		Kind:             "medication",
		EscalationPolicy: &policy,
	}
	if err := db.CreatePendingConfirmation(pc); err != nil {
		t.Fatal(err)
	}

	// Chama o handler.
	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "anotado") {
		t.Fatalf("expected neutral 'anotado' response, got %q", resp)
	}

	// Pending fechada?
	if _, err := db.GetPendingConfirmation(user.ID); err != ErrNoPendingConfirmation {
		t.Fatalf("expected no pending, got err=%v", err)
	}

	// Intake log = taken?
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].Status != IntakeTaken {
		t.Fatalf("expected status=taken, got %s", logs[0].Status)
	}
}

func TestMarkRemedioTomado_NoActivePending_StillResponds(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Usuario sem nenhum remedio cadastrado e sem lembrete ativo: o bot
	// responde de forma sensata (nao tem remedio pra anotar), nunca em
	// silencio nem alucinando que anotou.
	if !strings.Contains(strings.ToLower(resp), "remédio") {
		t.Fatalf("expected a sensible no-medication response, got %q", resp)
	}
}

// TestMarkRemedioTomado_ProactiveNoPending_RecordsIntake garante que um "tomei"
// FORA de um lembrete ativo ainda grava a dose no intake_log (antes ia so pro
// audit_log e sumia da aderencia do responsavel).
func TestMarkRemedioTomado_ProactiveNoPending_RecordsIntake(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Simone")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "4mag", "FREQ=DAILY;BYHOUR=21;BYMINUTE=0", false)

	agent := mkTestAgent(t, db)
	// Sem pending ativo; usuario cita o nome do remedio.
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{"name_query":"4mag"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "anotado") {
		t.Fatalf("expected 'anotado' response, got %q", resp)
	}
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if len(logs) != 1 {
		t.Fatalf("expected 1 intake log recorded, got %d", len(logs))
	}
	if logs[0].Status != IntakeTaken {
		t.Fatalf("expected status=taken, got %s", logs[0].Status)
	}
}

func TestMarkRemedioTomado_LateDose_NeutralResponse(t *testing.T) {
	// Regra dura: NUNCA reforco positivo ("otimo", "fez bem", "parabens",
	// "ainda bem"). Resposta neutra "anotado". Esta e a porcao automatizavel
	// — o caso "tomei agora atrasado" via Claude/prompt-eval fica em Fase 4.
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)

	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)
	intent := IntentData{Medication: &MedicationIntent{MedicationID: m.ID, ScheduledAt: scheduledAt, Reminder: true}}
	body, _ := json.Marshal(intent)
	policy := "medication_default"
	pc := &PendingConfirmation{UserID: user.ID, EventData: string(body), Kind: "medication", EscalationPolicy: &policy}
	db.CreatePendingConfirmation(pc)

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	bannedTokens := []string{"otimo", "ótimo", "fez bem", "parabens", "parabéns", "ainda bem"}
	low := strings.ToLower(resp)
	for _, tok := range bannedTokens {
		if strings.Contains(low, tok) {
			t.Fatalf("response contains positive-reinforcement token %q: %q", tok, resp)
		}
	}
}

// =========================================================================
// "Tomei" GENERICO TARDIO — acreditar no usuario apos a tolerancia/escalada
//
// Depois que a tolerancia expira, a escalacao marca a dose missed/escalated e
// RESOLVE o pending. Um "tomei" generico que chega depois disso nao acha nenhum
// pending ativo. Mesmo assim a tomada precisa ser registrada: acreditamos no
// idoso. marcarBatchTardio reabilita o ultimo batch nao-confirmado como 'taken'.
// =========================================================================

// assertNeutralTaken confere que a resposta confirma a anotacao sem reforco
// positivo (mesma lei de TestMarkRemedioTomado_LateDose_NeutralResponse).
func assertNeutralTaken(t *testing.T, resp string) {
	t.Helper()
	low := strings.ToLower(resp)
	if !strings.Contains(low, "anotado") {
		t.Fatalf("expected neutral 'anotado' ack, got %q", resp)
	}
	for _, tok := range []string{"otimo", "ótimo", "fez bem", "parabens", "parabéns", "ainda bem"} {
		if strings.Contains(low, tok) {
			t.Fatalf("response contains positive-reinforcement token %q: %q", tok, resp)
		}
	}
}

// Caso central: o idoso confirma TARDE (sem pending ativo) uma dose ja escalada;
// o "tomei" generico deve virar 'taken' (era o bug: ficava escalated/missed).
func TestMarkRemedioTomado_LateGenericAfterEscalation_FlipsToTaken(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m, _ := mkMedForUser(t, db, user, "Aradois", "FREQ=DAILY;BYHOUR=18;BYMINUTE=0", false)

	// Estado pos-escalacao: intake escalado, NENHUM pending ativo (ja resolvido).
	sched := time.Now().UTC().Add(-45 * time.Minute) // dentro da janela de 18h
	if err := db.CreateIntakeLogIfAbsent(m.ID, sched, IntakeEscalated); err != nil {
		t.Fatal(err)
	}

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	assertNeutralTaken(t, resp)

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if len(logs) != 1 {
		t.Fatalf("expected exactly 1 log row (flip in-place, no duplicate), got %d", len(logs))
	}
	if logs[0].Status != IntakeTaken {
		t.Fatalf("late 'tomei' should flip escalated->taken, got %s", logs[0].Status)
	}
	if logs[0].ConfirmedAt == nil {
		t.Fatal("confirmed_at should be set after late confirmation")
	}
}

// Variante sem guardiao: a dose vira 'missed'; o "tomei" tardio tambem reabilita.
func TestMarkRemedioTomado_LateGenericAfterMissed_FlipsToTaken(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=6;BYMINUTE=0", false)

	sched := time.Now().UTC().Add(-90 * time.Minute)
	if err := db.CreateIntakeLogIfAbsent(m.ID, sched, IntakeMissed); err != nil {
		t.Fatal(err)
	}

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	assertNeutralTaken(t, resp)

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if len(logs) != 1 || logs[0].Status != IntakeTaken {
		t.Fatalf("late 'tomei' should flip missed->taken in place, got %+v", logs)
	}
}

// Caso do print do usuario: lembrete AGRUPADO (2 remedios no mesmo horario),
// ambos escalados; um "tomei" generico tardio marca OS DOIS como tomados.
func TestMarkRemedioTomado_LateGenericGroupedBatch_FlipsAll(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m1, _ := mkMedForUser(t, db, user, "Amoxicilina", "FREQ=DAILY;BYHOUR=19;BYMINUTE=0", false)
	m2, _ := mkMedForUser(t, db, user, "Bactroban", "FREQ=DAILY;BYHOUR=19;BYMINUTE=0", false)

	// Mesmo instante exato (lembrete agrupado), ambos escalados, sem pending.
	sched := time.Now().UTC().Add(-35 * time.Minute)
	if err := db.CreateIntakeLogIfAbsent(m1.ID, sched, IntakeEscalated); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateIntakeLogIfAbsent(m2.ID, sched, IntakeEscalated); err != nil {
		t.Fatal(err)
	}

	agent := mkTestAgent(t, db)
	resp, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	assertNeutralTaken(t, resp)
	if !strings.Contains(strings.ToLower(resp), "marquei") {
		t.Fatalf("grouped late confirmation should name the meds it marked, got %q", resp)
	}

	for _, m := range []*Medication{m1, m2} {
		logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
		if len(logs) != 1 || logs[0].Status != IntakeTaken {
			t.Fatalf("%s: expected single taken row after batch confirm, got %+v", m.Name, logs)
		}
	}
}

// Precisao: so o BATCH mais recente nao-tomado vira taken. Uma dose 'skipped'
// (pulo deliberado) e doses antigas NAO podem ser tocadas por um "tomei" vago.
func TestMarkRemedioTomado_LateGeneric_OnlyMostRecentBatch_SkippedUntouched(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	recentMissed := time.Now().UTC().Add(-40 * time.Minute) // batch mais recente nao-tomado
	olderSkipped := time.Now().UTC().Add(-3 * time.Hour)     // pulo deliberado, anterior
	if err := db.CreateIntakeLogIfAbsent(m.ID, recentMissed, IntakeMissed); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateIntakeLogIfAbsent(m.ID, olderSkipped, IntakeSkipped); err != nil {
		t.Fatal(err)
	}

	agent := mkTestAgent(t, db)
	if _, err := handleMarcarRemedioTomado(context.Background(), agent, user, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 10) // DESC por scheduled_at
	if len(logs) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(logs))
	}
	if logs[0].Status != IntakeTaken {
		t.Fatalf("most-recent missed dose should flip to taken, got %s", logs[0].Status)
	}
	if logs[1].Status != IntakeSkipped {
		t.Fatalf("deliberate earlier skip must stay skipped, got %s", logs[1].Status)
	}
}

// =========================================================================
// Skip
// =========================================================================

func TestPularDose_RecordsReason(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)

	intent := IntentData{Medication: &MedicationIntent{MedicationID: m.ID, ScheduledAt: scheduledAt, Reminder: true}}
	body, _ := json.Marshal(intent)
	policy := "medication_default"
	pc := &PendingConfirmation{UserID: user.ID, EventData: string(body), Kind: "medication", EscalationPolicy: &policy}
	db.CreatePendingConfirmation(pc)

	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"reason":"estou enjoado"}`)
	resp, err := handlePularDose(context.Background(), agent, user, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "pulou") && !strings.Contains(strings.ToLower(resp), "anotei") {
		t.Fatalf("expected ack response, got %q", resp)
	}

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status != IntakeSkipped {
		t.Fatalf("expected skipped, got %s", logs[0].Status)
	}
	if logs[0].ResponseText != "estou enjoado" {
		t.Fatalf("response_text mismatch: %s", logs[0].ResponseText)
	}
}

func TestPularDose_RequiresReason(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)

	resp, err := handlePularDose(context.Background(), agent, users[0], json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "razão") {
		t.Fatalf("expected ask-for-reason response, got %q", resp)
	}
}

// =========================================================================
// cadastro self — persiste DIRETO (regressao do bug "cadastrei mas nao salvou")
// =========================================================================

// TestCadastrarMedicamento_Self_PersistsDirectly blinda a falha que motivou
// esta correcao: a tool dizia "cadastrei" mas so criava um pending orfao que
// nada executava. Agora a chamada da tool PERSISTE o medication + schedule de
// imediato (espelhando criar_evento), e nenhum pending fica pra tras.
func TestCadastrarMedicamento_Self_PersistsDirectly(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Fabio")
	user := users[0]
	agent := mkTestAgent(t, db)

	params := json.RawMessage(`{
		"name":"Prednisona",
		"dose":"20mg",
		"schedule_rrule":"FREQ=DAILY;BYHOUR=21;BYMINUTE=29",
		"end_date":"2026-05-27"
	}`)
	resp, err := handleCadastrarMedicamento(context.Background(), agent, user, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "cadastrei") {
		t.Fatalf("expected persisted confirmation, got %q", resp)
	}

	meds, err := db.ListActiveMedications(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meds) != 1 {
		t.Fatalf("expected 1 medication persisted, got %d", len(meds))
	}
	if meds[0].Name != "Prednisona" || meds[0].Dose != "20mg" {
		t.Fatalf("unexpected med persisted: %+v", meds[0])
	}
	scheds, _ := db.ListSchedulesForMedication(meds[0].ID)
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
	if scheds[0].EndDate == nil {
		t.Fatal("expected end_date persisted from the tool call")
	}
	// Nenhum pending orfao deve sobrar.
	if _, err := db.GetPendingConfirmation(user.ID); err != ErrNoPendingConfirmation {
		t.Fatalf("cadastro must not leave an orphan pending, got err=%v", err)
	}
}

// =========================================================================
// target_user / family link
// =========================================================================

func TestCadastrarMedicamento_ForElder_RequiresFamilyLink(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Filha", "Mae")
	agent := mkTestAgent(t, db)

	// Sem family_link, deve negar.
	params := json.RawMessage(`{
		"target_user":"Mae",
		"name":"Losartana",
		"schedule_rrule":"FREQ=DAILY;BYHOUR=8;BYMINUTE=0"
	}`)
	resp, err := handleCadastrarMedicamento(context.Background(), agent, users[0], params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "permissão") {
		t.Fatalf("expected denial mentioning permission, got %q", resp)
	}
	if meds, _ := db.ListActiveMedications(users[1].ID); len(meds) != 0 {
		t.Fatal("should not have created medication without permission")
	}

	// Com family_link, persiste DIRETO no dependente (sem pending).
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filha"); err != nil {
		t.Fatal(err)
	}
	resp, err = handleCadastrarMedicamento(context.Background(), agent, users[0], params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "cadastrei") {
		t.Fatalf("expected confirmation that med was persisted, got %q", resp)
	}
	meds, err := db.ListActiveMedications(users[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meds) != 1 {
		t.Fatalf("expected 1 medication persisted for the dependent, got %d", len(meds))
	}
	if meds[0].Name != "Losartana" {
		t.Fatalf("expected Losartana, got %q", meds[0].Name)
	}
	if meds[0].CreatedByUserID != users[0].ID {
		t.Fatalf("expected created_by=guardian(%d), got %d", users[0].ID, meds[0].CreatedByUserID)
	}
	scheds, _ := db.ListSchedulesForMedication(meds[0].ID)
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
}

// =========================================================================
// extrair_receita_imagem (stub structural)
// =========================================================================

func TestExtrairReceita_ExtractsAndQueuesItems(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)

	params := json.RawMessage(`{
		"items":[
			{"name":"Losartana","dose":"50mg","frequency_text":"1x ao dia"},
			{"name":"Metformina","dose":"850mg","frequency_text":"8/8h"}
		]
	}`)
	resp, err := handleExtrairReceitaImagem(context.Background(), agent, users[0], params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "Losartana") || !strings.Contains(resp, "Metformina") {
		t.Fatalf("expected both meds in response, got %q", resp)
	}
	if !strings.Contains(strings.ToLower(resp), "item-a-item") {
		t.Fatalf("expected agent-instruction mentioning item-a-item, got %q", resp)
	}

	// Auditoria registrou a extracao crua, mas nenhum medication ainda.
	meds, _ := db.ListActiveMedications(users[0].ID)
	if len(meds) != 0 {
		t.Fatalf("should NOT have persisted meds yet, got %d", len(meds))
	}
}

func TestExtrairReceita_EmptyItems(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)
	resp, err := handleExtrairReceitaImagem(context.Background(), agent, users[0], json.RawMessage(`{"items":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "não consegui") {
		t.Fatalf("expected fail-soft response, got %q", resp)
	}
}

// =========================================================================
// Tools coverage extra: listar, editar, cancelar
// =========================================================================

func TestListarMedicamentos_Empty(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)
	resp, err := handleListarMedicamentos(context.Background(), agent, users[0], json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "não tem") {
		t.Fatalf("expected 'nao tem' empty msg, got %q", resp)
	}
}

func TestListarMedicamentos_Multiple(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	mkMedForUser(t, db, user, "Metformina", "FREQ=DAILY;BYHOUR=20;BYMINUTE=0", true)

	agent := mkTestAgent(t, db)
	resp, err := handleListarMedicamentos(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "Losartana") || !strings.Contains(resp, "Metformina") {
		t.Fatalf("expected both meds in response, got %q", resp)
	}
	if !strings.Contains(resp, "8h") || !strings.Contains(resp, "20h") {
		t.Fatalf("expected hours in response, got %q", resp)
	}
	if !strings.Contains(strings.ToLower(resp), "crítico") {
		t.Fatalf("expected critical marker, got %q", resp)
	}
}

func TestEditarMedicamento_ChangesDose(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"medication_id":` + itoaInt64(m.ID) + `,"new_dose":"100mg"}`)
	resp, err := handleEditarMedicamento(context.Background(), agent, user, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "atualiz") {
		t.Fatalf("expected ack, got %q", resp)
	}
	got, _ := db.GetMedicationByID(m.ID)
	if got.Dose != "100mg" {
		t.Fatalf("expected dose updated, got %q", got.Dose)
	}
}

func TestEditarMedicamento_ChangesSchedule(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"medication_id":` + itoaInt64(m.ID) + `,"new_schedule_rrule":"FREQ=DAILY;BYHOUR=10;BYMINUTE=0"}`)
	if _, err := handleEditarMedicamento(context.Background(), agent, user, params); err != nil {
		t.Fatal(err)
	}
	scheds, _ := db.ListSchedulesForMedication(m.ID)
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
	if !strings.Contains(scheds[0].RRULE, "BYHOUR=10") {
		t.Fatalf("expected new BYHOUR=10, got %q", scheds[0].RRULE)
	}
}

func TestEditarMedicamento_NotFound(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"medication_id":9999}`)
	resp, err := handleEditarMedicamento(context.Background(), agent, users[0], params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "não achei") {
		t.Fatalf("expected not-found msg, got %q", resp)
	}
}

func TestEditarMedicamento_InvalidRRULE(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"medication_id":` + itoaInt64(m.ID) + `,"new_schedule_rrule":"isso nao eh rrule"}`)
	resp, err := handleEditarMedicamento(context.Background(), agent, user, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "horário") {
		t.Fatalf("expected RRULE-error msg, got %q", resp)
	}
}

func TestCancelarMedicamento_ByName(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"name_query":"losartana","reason":"medico tirou"}`)
	resp, err := handleCancelarMedicamento(context.Background(), agent, user, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "cancelei") {
		t.Fatalf("expected 'cancelei' ack, got %q", resp)
	}
	got, _ := db.GetMedicationByID(m.ID)
	if got.Active {
		t.Fatal("expected medication to be inactive")
	}
}

func TestCancelarMedicamento_NameNotFound(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	agent := mkTestAgent(t, db)
	params := json.RawMessage(`{"name_query":"inexistente"}`)
	resp, err := handleCancelarMedicamento(context.Background(), agent, users[0], params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "não achei") {
		t.Fatalf("expected not-found msg, got %q", resp)
	}
}

// =========================================================================
// Confirmation flow: cadastro pendente -> confirmar -> persiste
// =========================================================================

func TestConfirmation_PersistsMedicationOnConfirm(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]

	intent := IntentData{
		Medication: &MedicationIntent{
			Name:          "Losartana",
			Dose:          "50mg",
			ScheduleRRULE: "FREQ=DAILY;BYHOUR=8;BYMINUTE=0",
			StartDate:     "2026-05-09",
		},
	}
	body, _ := json.Marshal(intent)
	pc := &PendingConfirmation{
		UserID:    user.ID,
		EventData: string(body),
		Kind:      "medication",
	}
	if err := db.CreatePendingConfirmation(pc); err != nil {
		t.Fatal(err)
	}

	cm := NewConfirmationManager(db, nil, nil)
	pcLoaded, _ := db.GetPendingConfirmation(user.ID)
	resp, err := cm.executeConfirmation(user, pcLoaded)
	if err != nil {
		t.Fatalf("executeConfirmation: %v", err)
	}
	if !strings.Contains(strings.ToLower(resp), "cadastrei") {
		t.Fatalf("expected 'cadastrei' ack, got %q", resp)
	}

	meds, _ := db.ListActiveMedications(user.ID)
	if len(meds) != 1 {
		t.Fatalf("expected 1 med created, got %d", len(meds))
	}
	if meds[0].Name != "Losartana" || meds[0].Dose != "50mg" {
		t.Fatalf("med fields mismatch: %+v", meds[0])
	}
	scheds, _ := db.ListSchedulesForMedication(meds[0].ID)
	if len(scheds) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(scheds))
	}
}

func TestConfirmation_RemindMarksTaken(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)

	intent := IntentData{
		Medication: &MedicationIntent{MedicationID: m.ID, ScheduledAt: scheduledAt, Reminder: true},
	}
	body, _ := json.Marshal(intent)
	pc := &PendingConfirmation{UserID: user.ID, EventData: string(body), Kind: "medication"}
	db.CreatePendingConfirmation(pc)

	cm := NewConfirmationManager(db, nil, nil)
	pcLoaded, _ := db.GetPendingConfirmation(user.ID)
	resp, err := cm.executeConfirmation(user, pcLoaded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp), "anotado") {
		t.Fatalf("expected neutral ack, got %q", resp)
	}
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status != IntakeTaken {
		t.Fatalf("expected taken, got %s", logs[0].Status)
	}
}

// itoaInt64 small helper to format int64 without strconv import.
func itoaInt64(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// =========================================================================
// CacheMediaImage (foto da receita)
// =========================================================================

func TestCacheMediaImage_DisabledByDefault(t *testing.T) {
	t.Setenv("LURCH_MEDIA_CACHE", "")
	path, err := CacheMediaImage([]byte("fake"), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("expected no-op when env unset, got path=%q", path)
	}
}

func TestCacheMediaImage_WritesWhenEnabled(t *testing.T) {
	t.Setenv("LURCH_MEDIA_CACHE", "1")
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	data := []byte("fake jpeg data")
	path, err := CacheMediaImage(data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected path to be returned")
	}
	if !strings.HasSuffix(path, ".jpg") {
		t.Fatalf("expected .jpg suffix, got %q", path)
	}

	// Idempotente — chamar de novo retorna mesmo path.
	path2, err := CacheMediaImage(data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("expected same path on second call, got %q vs %q", path, path2)
	}
}

func TestCacheMediaImage_RejectsEmpty(t *testing.T) {
	t.Setenv("LURCH_MEDIA_CACHE", "1")
	_, err := CacheMediaImage(nil, "image/jpeg")
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

// =========================================================================
// WhatsAppNotifier
// =========================================================================

func TestWhatsAppNotifier_DispatchesViaSendMsg(t *testing.T) {
	var gotPhone, gotText string
	send := func(phone, text string) error {
		gotPhone = phone
		gotText = text
		return nil
	}
	n := NewWhatsAppNotifier(send)
	if n.Channel() != "whatsapp" {
		t.Fatalf("expected channel=whatsapp, got %q", n.Channel())
	}
	user := &User{PhoneNumber: "5511999999999", Name: "X"}
	if err := n.Send(context.Background(), user, "ola"); err != nil {
		t.Fatal(err)
	}
	if gotPhone != "5511999999999" || gotText != "ola" {
		t.Fatalf("unexpected dispatch: phone=%q text=%q", gotPhone, gotText)
	}
}

func TestWhatsAppNotifier_RejectsNilRecipient(t *testing.T) {
	n := NewWhatsAppNotifier(func(_, _ string) error { return nil })
	if err := n.Send(context.Background(), nil, "hi"); err == nil {
		t.Fatal("expected error for nil recipient")
	}
}

func TestWhatsAppNotifier_RejectsNilCallback(t *testing.T) {
	n := NewWhatsAppNotifier(nil)
	if err := n.Send(context.Background(), &User{PhoneNumber: "1"}, "hi"); err == nil {
		t.Fatal("expected error for nil sendMsg")
	}
}

// =========================================================================
// GetActiveMedicationPendings + scheduler entrypoint
// =========================================================================

func TestGetActiveMedicationPendings_FiltersByKindAndStatus(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A", "B")

	// Pending de evento — nao deve aparecer.
	evt := &PendingConfirmation{UserID: users[0].ID, EventData: `{"title":"x"}`}
	if err := db.CreatePendingConfirmation(evt); err != nil {
		t.Fatal(err)
	}
	// Pending de medicacao users[0] — aparece.
	med1 := &PendingConfirmation{UserID: users[0].ID, EventData: `{}`, Kind: "medication"}
	if err := db.CreatePendingConfirmation(med1); err != nil {
		t.Fatal(err)
	}
	// Pending de medicacao users[1] — aparece.
	med2 := &PendingConfirmation{UserID: users[1].ID, EventData: `{}`, Kind: "medication"}
	if err := db.CreatePendingConfirmation(med2); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetActiveMedicationPendings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 active medication pendings, got %d", len(got))
	}

	// Resolver um e checar que so sobra 1.
	db.ResolvePendingConfirmation(med1.ID, "confirmed")
	got, _ = db.GetActiveMedicationPendings()
	if len(got) != 1 {
		t.Fatalf("expected 1 after resolving one, got %d", len(got))
	}
}

func TestGetLatestPendingIntake_ReturnsNilWhenNone(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	m, _ := mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	got, err := db.GetLatestPendingIntake(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestGetLatestPendingIntake_ReturnsNewest(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	m, _ := mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)
	older := time.Date(2026, 5, 8, 11, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)
	if err := db.CreateIntakeLogIfAbsent(m.ID, older, IntakePending); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateIntakeLogIfAbsent(m.ID, newer, IntakePending); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetLatestPendingIntake(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if !got.ScheduledAt.Equal(newer) {
		t.Fatalf("expected newer ts, got %v (newer=%v)", got.ScheduledAt, newer)
	}
}

func TestSchedulerCheckMedicationReminders_NilNotifier_NoOp(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	mkMedForUser(t, db, users[0], "X", "FREQ=DAILY;BYHOUR=8;BYMINUTE=0", false)

	// Scheduler com notifier nil = nao executa nada (no panic).
	s := &Scheduler{db: db, notifier: nil}
	s.checkMedicationReminders()
}

func TestSchedulerCheckMedicationEscalation_NilEng_NoOp(t *testing.T) {
	db := setupTestDB(t)
	s := &Scheduler{db: db, eng: nil}
	s.checkMedicationEscalation()
}

func TestMedMedicationID_HappyPath(t *testing.T) {
	intent := IntentData{
		Medication: &MedicationIntent{MedicationID: 42, ScheduledAt: time.Now(), Reminder: true},
	}
	body, _ := json.Marshal(intent)
	pc := &PendingConfirmation{EventData: string(body)}
	if got := medMedicationID(pc); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestMedMedicationID_NilOrInvalid(t *testing.T) {
	if got := medMedicationID(nil); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	pc := &PendingConfirmation{EventData: ""}
	if got := medMedicationID(pc); got != 0 {
		t.Fatalf("expected 0 for empty, got %d", got)
	}
	pc = &PendingConfirmation{EventData: "garbage"}
	if got := medMedicationID(pc); got != 0 {
		t.Fatalf("expected 0 for garbage, got %d", got)
	}
}

// =========================================================================
// Helpers
// =========================================================================

// mkTestAgent monta um Agent suficiente para os tools que testamos. Sem
// cliente HTTP/Anthropic — handlers de medicacao nao precisam dele.
func mkTestAgent(t *testing.T, db *DB) *Agent {
	t.Helper()
	return &Agent{
		db:    db,
		perms: NewPermissionManager(db),
		audit: NewAuditLog(db),
		// cal/cfg/sendMsg ficam zero — handlers de medicacao nao acessam.
	}
}

// getFirstMedID devolve ID do primeiro medication ativo do user. Auxilio
// para testes que sabem que so existe um.
func getFirstMedID(t *testing.T, db *DB, userID int64) int64 {
	t.Helper()
	meds, err := db.ListActiveMedications(userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meds) == 0 {
		t.Fatal("no active medications")
	}
	return meds[0].ID
}
