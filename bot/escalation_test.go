package main

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Aliases pra reduzir verbosidade ao declarar campos opcionais em scans.
type sqlNullStringT struct{ sql.NullString }
type sqlNullTimeT struct{ sql.NullTime }
type sqlNullInt64T struct{ sql.NullInt64 }

// =========================================================================
// Modelo de escalacao (Fase 3.1): cadencia dirigida pela tolerancia.
//
// deadline = scheduled_at + tolerance_minutes (default 30). Dentro da janela,
// no maximo UM lembrete gentil (no meio da janela, ou no horario que o idoso
// disse via adiar_remedio). No deadline, se nao confirmado, a familia eh
// avisada EM SEGREDO com mensagem verdadeira; a dose vira "nao confirmada".
// =========================================================================

// mkMedicationPending cria um pending kind=medication completo com payload
// MedicationIntent e retorna o pc populado.
func mkMedicationPending(t *testing.T, db *DB, user *User, m *Medication, scheduledAt time.Time, policyName string) *PendingConfirmation {
	t.Helper()
	intent := IntentData{
		Medication: &MedicationIntent{
			MedicationID: m.ID,
			ScheduledAt:  scheduledAt,
			Reminder:     true,
		},
	}
	body, _ := json.Marshal(intent)
	policy := policyName
	pc := &PendingConfirmation{
		UserID:           user.ID,
		EventData:        string(body),
		Kind:             "medication",
		EscalationPolicy: &policy,
	}
	if err := db.CreatePendingConfirmation(pc); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if err := db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending); err != nil {
		t.Fatalf("create intake log: %v", err)
	}
	return pc
}

// Janela padrao usada nos testes: agendado 17h UTC (14h BRT), tolerancia 30min
// (default de CreateMedication). Logo: nudge gentil ~17h15, deadline 17h30.
var (
	testSched    = time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	testNudgeAt  = testSched.Add(15 * time.Minute) // tolerance/2
	testDeadline = testSched.Add(30 * time.Minute)
)

// =========================================================================
// Mensagens: seguranca farmacologica + sem ameaca de familia + verdade
// =========================================================================

func TestEscalationMessages_Safe(t *testing.T) {
	user := &User{ID: 1, Name: "Antonia da Silva"}
	med := &Medication{ID: 1, Name: "Losartana", Dose: "50mg"}

	// Termos PROIBIDOS — orientacao positiva pra tomar atrasado.
	bannedRegex := regexp.MustCompile(
		`(?i)(ainda d[áa] tempo|compensa a dose|^[^.!?]*(?:^|[^o])\s*tome agora|n[ãa]o esque[çc]a de tomar)`,
	)
	tomeAgora := regexp.MustCompile(`(?i)tome agora`)
	naoTomeAgora := regexp.MustCompile(`(?i)n[ãa]o\s+tome\s+agora`)
	familiaRegex := regexp.MustCompile(`(?i)fam[ií]lia`)

	checkSafe := func(t *testing.T, label, msg string) {
		if bannedRegex.MatchString(msg) {
			t.Errorf("%s: contains BANNED token: %q", label, msg)
		}
		if tomeAgora.MatchString(msg) && !naoTomeAgora.MatchString(msg) {
			t.Errorf("%s: 'tome agora' must be preceded by 'nao': %q", label, msg)
		}
	}

	ec := EscalationContext{User: user, Medication: med, ScheduledAt: testSched, Recipient: user}

	// Lembrete gentil: seguro E nunca menciona familia (sem ameaca).
	nudge := gentleNudgeMsg(ec)
	checkSafe(t, "gentleNudgeMsg", nudge)
	if familiaRegex.MatchString(nudge) {
		t.Errorf("gentleNudgeMsg must NOT mention familia (no threat): %q", nudge)
	}

	// Mensagem a familia (sem adiamento): verdadeira, sem afirmar "nao respondeu".
	fam := familyMissMsg(ec)
	checkSafe(t, "familyMissMsg", fam)
	if regexp.MustCompile(`(?i)(n[ãa]o respondeu|v[áa]rias tentativas)`).MatchString(fam) {
		t.Errorf("familyMissMsg must NOT falsely claim 'nao respondeu': %q", fam)
	}
	if !regexp.MustCompile(`(?i)n[ãa]o confirm`).MatchString(fam) {
		t.Errorf("familyMissMsg should say 'nao confirmada/confirmei': %q", fam)
	}
	if !regexp.MustCompile(`(?i)n[ãa]o oriento`).MatchString(fam) {
		t.Errorf("familyMissMsg should contain 'nao oriento': %q", fam)
	}

	// Com adiamento: reflete que o idoso disse que tomaria mais tarde.
	deferred := testSched.Add(40 * time.Minute)
	ecDef := ec
	ecDef.DeferredUntil = &deferred
	famDef := familyMissMsg(ecDef)
	checkSafe(t, "familyMissMsg(deferred)", famDef)
	if !regexp.MustCompile(`(?i)mais tarde`).MatchString(famDef) {
		t.Errorf("familyMissMsg(deferred) should mention 'mais tarde': %q", famDef)
	}
	if regexp.MustCompile(`(?i)n[ãa]o respondeu`).MatchString(famDef) {
		t.Errorf("familyMissMsg(deferred) must NOT claim 'nao respondeu': %q", famDef)
	}
}

// =========================================================================
// Lembrete gentil unico dentro da tolerancia
// =========================================================================

// setLastUserMessageAt seta last_user_message_at direto no banco (sem o flip de
// proactive), pra simular engajamento recente do idoso nos testes de supressao.
func setLastUserMessageAt(t *testing.T, db *DB, userID int64, ts time.Time) {
	t.Helper()
	if _, err := db.conn.Exec(`UPDATE users SET last_user_message_at = ? WHERE id = ?`, ts.UTC(), userID); err != nil {
		t.Fatalf("set last_user_message_at: %v", err)
	}
}

// Engajamento recente do idoso (respondeu logo apos o lembrete) SUPRIME o cutucao
// gentil — cutucar quem acabou de responder soa como "ignorei sua resposta".
func TestEscalation_EngagementSuppressesNudge(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Fabio", "Filha")
	user, guard := users[0], users[1]
	m, _ := mkMedForUser(t, db, user, "Aradois", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// Respondeu 2min apos o lembrete (17:02). No tick do cutucao (17:16), o
	// engajamento tem ~14min < grace (15min = tolerance/2) -> NAO cutuca.
	setLastUserMessageAt(t, db, user.ID, testSched.Add(2*time.Minute))
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testNudgeAt.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 0 {
		t.Fatalf("engajamento recente deveria suprimir o cutucao, got %d", len(notif.Sent()))
	}

	// Mas a familia AINDA eh avisada no deadline — engajar != confirmar a tomada.
	if _, err := db.LinkFamily(guard.ID, user.ID, "filha"); err != nil {
		t.Fatal(err)
	}
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	eng.HandlePending(testDeadline.Add(time.Minute), pcLoaded)
	gotGuardian := false
	for _, s := range notif.Sent() {
		if s.Recipient != nil && s.Recipient.ID == guard.ID {
			gotGuardian = true
		}
	}
	if !gotGuardian {
		t.Fatalf("familia deve ser avisada no deadline mesmo com engajamento")
	}
}

// Sem engajamento APOS o lembrete (ultima fala foi antes do horario da dose), o
// cutucao gentil sai normalmente.
func TestEscalation_StaleEngagementStillNudges(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m, _ := mkMedForUser(t, db, user, "Aradois", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// Falou 10min ANTES do lembrete -> nao conta como engajamento desta dose.
	setLastUserMessageAt(t, db, user.ID, testSched.Add(-10*time.Minute))
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testNudgeAt.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("sem engajamento apos o lembrete, o cutucao deve sair, got %d", len(notif.Sent()))
	}
}

// Corrida: um "tomei" resolveu o pending (confirmed) e marcou a dose (taken) ANTES
// do tick de escalacao processar o snapshot que tinha lido como 'pending'. A
// escalacao NAO pode avisar a familia nem regredir a dose taken->escalated.
func TestEscalation_TakenWinsRaceNoFalseAlert(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha")
	idosa, guard := users[0], users[1]
	if _, err := db.LinkFamily(guard.ID, idosa.ID, "filha"); err != nil {
		t.Fatal(err)
	}
	m, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, idosa, m, testSched, "medication_default")

	// O tick de escalacao leu o pending enquanto ainda estava 'pending' (snapshot).
	stale, _ := loadPCByID(t, db, pc.ID)

	// "Tomei" concorrente resolve: dose taken (incondicional) + pending confirmed.
	if err := db.UpdateIntakeStatus(m.ID, testSched, IntakeTaken, "tomei"); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// Escalacao processa o snapshot stale no deadline.
	eng.HandlePending(testDeadline.Add(time.Minute), stale)

	for _, s := range notif.Sent() {
		if s.Recipient != nil && s.Recipient.ID == guard.ID {
			t.Fatalf("familia nao deveria ser avisada apos 'tomei' concorrente: %q", s.Body)
		}
	}
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if len(logs) == 0 || logs[0].Status != IntakeTaken {
		t.Fatalf("dose deveria continuar taken, got %v", logs)
	}
	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "confirmed" {
		t.Fatalf("pending deveria continuar confirmed, got %s", status)
	}
}

// O cutucao gentil eh pulado se o pending foi resolvido entre a leitura do batch e
// o envio (re-check em sendNudges).
func TestEscalation_NudgeSkippedIfResolved(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Fabio")[0]
	m, _ := mkMedForUser(t, db, user, "Aradois", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")
	stale, _ := loadPCByID(t, db, pc.ID)

	if err := db.UpdateIntakeStatus(m.ID, testSched, IntakeTaken, "tomei"); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		t.Fatal(err)
	}

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	eng.HandlePending(testNudgeAt.Add(time.Minute), stale)
	if len(notif.Sent()) != 0 {
		t.Fatalf("nao deveria cutucar dose ja resolvida, got %d", len(notif.Sent()))
	}
}

func TestNudgeEngagementGrace(t *testing.T) {
	cases := []struct {
		tol  int
		want time.Duration
	}{
		{30, 15 * time.Minute}, // default: tol/2
		{10, 5 * time.Minute},  // critico curto: tol/2 (nunca passa o deadline)
		{60, 20 * time.Minute}, // longo: teto de 20min
		{0, 15 * time.Minute},  // invalido -> usa default 30 -> 15
	}
	for _, c := range cases {
		if got := nudgeEngagementGrace(c.tol); got != c.want {
			t.Errorf("nudgeEngagementGrace(%d) = %v, want %v", c.tol, got, c.want)
		}
	}
}

func TestEscalation_SingleGentleNudgeWithinTolerance(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// Antes do meio da janela: nada.
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testSched.Add(5*time.Minute), pcLoaded)
	if len(notif.Sent()) != 0 {
		t.Fatalf("should not nudge before mid-window, got %d", len(notif.Sent()))
	}

	// No meio da janela: UM lembrete gentil.
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	eng.HandlePending(testNudgeAt.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("expected exactly 1 gentle nudge, got %d", len(notif.Sent()))
	}
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	if pcLoaded.AttemptNumber != 1 {
		t.Fatalf("expected attempt=1 after nudge, got %d", pcLoaded.AttemptNumber)
	}

	// Mais tarde, ainda dentro da janela: NAO repete a cobranca.
	eng.HandlePending(testSched.Add(20*time.Minute), pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("must not nudge twice within window, got %d", len(notif.Sent()))
	}
}

// =========================================================================
// Familia so eh avisada no deadline — nunca antes
// =========================================================================

func TestEscalation_NoFamilyBeforeDeadline(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha")
	idosa := users[0]
	if _, err := db.LinkFamily(users[1].ID, idosa.ID, "filha"); err != nil {
		t.Fatal(err)
	}
	m, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, idosa, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// Ate antes do deadline, so pode haver o nudge ao proprio idoso (nunca familia).
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testDeadline.Add(-time.Minute), pcLoaded)
	for _, s := range notif.Sent() {
		if s.Recipient != nil && s.Recipient.ID == users[1].ID {
			t.Fatalf("guardian must NOT be notified before deadline")
		}
	}
	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "pending" {
		t.Fatalf("pending should still be open before deadline, got %s", status)
	}
}

// =========================================================================
// No deadline: familia avisada em segredo, dose nao confirmada
// =========================================================================

func TestEscalation_NotifiesFamilyAtDeadline(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha", "Filho")
	idosa := users[0]
	for _, g := range []*User{users[1], users[2]} {
		if _, err := db.LinkFamily(g.ID, idosa.ID, "filho"); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, idosa, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testDeadline.Add(time.Minute), pcLoaded)

	// Uma mensagem secreta por guardiao.
	guardianMsgs := 0
	for _, s := range notif.Sent() {
		if s.Recipient != nil && (s.Recipient.ID == users[1].ID || s.Recipient.ID == users[2].ID) {
			guardianMsgs++
			if !strings.Contains(s.Body, "Idosa") {
				t.Errorf("guardian msg should mention idoso name: %q", s.Body)
			}
			if strings.Contains(strings.ToLower(s.Body), "não respondeu") {
				t.Errorf("guardian msg must not claim 'nao respondeu': %q", s.Body)
			}
		}
	}
	if guardianMsgs != 2 {
		t.Fatalf("expected 2 guardian messages, got %d", guardianMsgs)
	}

	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status != IntakeEscalated {
		t.Fatalf("expected intake status=escalated, got %s", logs[0].Status)
	}
	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "escalated" {
		t.Fatalf("expected pending.status=escalated, got %s", status)
	}
}

// =========================================================================
// Adiamento ("vou tomar mais tarde"): nudge no horario dito, nao antes
// =========================================================================

func TestEscalation_DeferralShiftsNudge(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Antonia")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	// Idoso disse que toma as 17h25 UTC (dentro da janela de 30min).
	deferred := testSched.Add(25 * time.Minute)
	if err := db.SetPendingDeferredUntil(pc.ID, deferred); err != nil {
		t.Fatal(err)
	}

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// No meio da janela (17h16) ainda nao cutuca — respeita o horario dito.
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	if pcLoaded.DeferredUntil == nil {
		t.Fatal("deferred_until should have been persisted/loaded")
	}
	eng.HandlePending(testNudgeAt.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 0 {
		t.Fatalf("should not nudge before stated time, got %d", len(notif.Sent()))
	}

	// Depois do horario dito: UM lembrete gentil.
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	eng.HandlePending(deferred.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("expected gentle nudge at stated time, got %d", len(notif.Sent()))
	}
}

// =========================================================================
// Sem guardian = missed, sem alerta
// =========================================================================

func TestEscalation_NoGuardianMarksMissed(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "Solitaria")[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testDeadline.Add(time.Minute), pcLoaded)

	if len(notif.Sent()) != 0 {
		t.Fatalf("expected no messages without guardian at deadline, got %d", len(notif.Sent()))
	}
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status != IntakeMissed {
		t.Fatalf("expected status=missed without guardian, got %s", logs[0].Status)
	}
	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "missed" {
		t.Fatalf("expected pending.status=missed, got %s", status)
	}
}

// =========================================================================
// Idempotencia: ticks simultaneos no nudge nao duplicam
// =========================================================================

func TestEscalation_ConcurrentNudgeNoDouble(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "A")[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	now := testNudgeAt.Add(time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pcLocal, _ := loadPCByID(t, db, pc.ID)
			eng.HandlePending(now, pcLocal)
		}()
	}
	wg.Wait()

	escs, _ := db.ListEscalationsForPending(pc.ID)
	if len(escs) != 1 {
		t.Fatalf("expected exactly 1 escalation row (UNIQUE), got %d", len(escs))
	}
}

// =========================================================================
// Restart no meio: nudge num engine, deadline em outro
// =========================================================================

func TestEscalation_SurvivesRestart(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha")
	idosa := users[0]
	if _, err := db.LinkFamily(users[1].ID, idosa.ID, "filha"); err != nil {
		t.Fatal(err)
	}
	m, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, idosa, m, testSched, "medication_default")

	notif := &recordingNotifier{}

	// Engine 1: nudge no meio da janela.
	eng1 := NewEscalationEngine(db, notif)
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng1.HandlePending(testNudgeAt.Add(time.Minute), pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("expected 1 nudge, got %d", len(notif.Sent()))
	}

	// RESTART: engine novo, sem estado em memoria. No deadline -> familia.
	eng2 := NewEscalationEngine(db, notif)
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	eng2.HandlePending(testDeadline.Add(time.Minute), pcLoaded)

	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "escalated" {
		t.Fatalf("expected escalated after restart+deadline, got %s", status)
	}
}

// =========================================================================
// HandlePending edge cases
// =========================================================================

func TestHandlePending_NoPolicy_NoOp(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")

	pc := &PendingConfirmation{
		UserID:    users[0].ID,
		EventData: `{"title":"x"}`,
		Kind:      "event",
	}
	if err := db.CreatePendingConfirmation(pc); err != nil {
		t.Fatal(err)
	}
	pcLoaded, _ := db.GetPendingConfirmation(users[0].ID)

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	eng.HandlePending(time.Now(), pcLoaded)

	if len(notif.Sent()) != 0 {
		t.Fatalf("no policy = no escalation, got %d sends", len(notif.Sent()))
	}
}

func TestHandlePending_UnknownPolicy_LogsAndReturns(t *testing.T) {
	db := setupTestDB(t)
	user := mkUsers(t, db, "A")[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	pc := mkMedicationPending(t, db, user, m, testSched, "policy_inexistente")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(testDeadline.Add(time.Minute), pcLoaded)

	if len(notif.Sent()) != 0 {
		t.Fatalf("unknown policy: should not send")
	}
}

// =========================================================================
// Helpers
// =========================================================================

// loadPCByID busca um pending por id (mais determinista que pegar o ultimo
// do user nos testes).
func loadPCByID(t *testing.T, db *DB, id int64) (*PendingConfirmation, error) {
	t.Helper()
	pc := &PendingConfirmation{}
	var kind, policy sqlNullStringT
	var lastAttempt, deferred sqlNullTimeT
	var attempt sqlNullInt64T
	err := db.conn.QueryRow(
		`SELECT id, user_id, event_data, status, created_at,
		        kind, escalation_policy, last_attempt_at, attempt_number, deferred_until
		 FROM pending_confirmations WHERE id = ?`, id,
	).Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
		&kind, &policy, &lastAttempt, &attempt, &deferred)
	if err != nil {
		t.Fatalf("load pc: %v", err)
	}
	fillPendingExtras(pc, kind.NullString, policy.NullString, lastAttempt.NullTime, attempt.NullInt64, deferred.NullTime)
	return pc, nil
}
