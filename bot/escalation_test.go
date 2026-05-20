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
// Helpers especificos da escalacao
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
	// Tambem cria intake_log inicial pra simular fluxo real.
	if err := db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending); err != nil {
		t.Fatalf("create intake log: %v", err)
	}
	return pc
}

// =========================================================================
// 11.14.1 — DoNotPushLateDose (regra dura)
// =========================================================================

func TestEscalationMessages_DoNotPushLateDose(t *testing.T) {
	user := &User{ID: 1, Name: "Antonia da Silva"}
	med := &Medication{ID: 1, Name: "Losartana", Dose: "50mg"}
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC) // 14h BRT

	// Termos PROIBIDOS — orientacao positiva pra tomar atrasado.
	// IMPORTANTE: "tome agora" eh proibido em forma afirmativa, mas
	// "nao tome agora" (negativa) eh PERMITIDO e ate desejado. Por isso
	// o regex pega "tome agora" sem o "nao" antes (lookbehind via
	// alternativa simples — Go regexp nao suporta lookbehind).
	bannedRegex := regexp.MustCompile(
		`(?i)(ainda d[áa] tempo|compensa a dose|^[^.!?]*(?:^|[^o])\s*tome agora|n[ãa]o esque[çc]a de tomar)`,
	)
	// Verificacao auxiliar: a substring "tome agora" so eh aceitavel
	// quando precedida por "nao" (com ou sem acento).
	tomeAgora := regexp.MustCompile(`(?i)tome agora`)
	naoTomeAgora := regexp.MustCompile(`(?i)n[ãa]o\s+tome\s+agora`)

	wantAnotarRegex := regexp.MustCompile(`(?i)(anotar|anotei|n[ãa]o tomada)`)
	wantMedFamRegex := regexp.MustCompile(`(?i)(m[ée]dico|fam[ií]lia)`)

	checkSafe := func(t *testing.T, label, msg string) {
		if bannedRegex.MatchString(msg) {
			t.Errorf("%s: contains BANNED token: %q", label, msg)
		}
		if tomeAgora.MatchString(msg) && !naoTomeAgora.MatchString(msg) {
			t.Errorf("%s: 'tome agora' must be preceded by 'nao': %q", label, msg)
		}
	}

	for _, polName := range []string{"medication_default", "medication_critical"} {
		pol, ok := escalationPolicies[polName]
		if !ok {
			t.Fatalf("policy %q not found", polName)
		}
		for attempt := 1; attempt <= pol.MaxAttempts; attempt++ {
			ctx := EscalationContext{
				User:          user,
				Medication:    med,
				ScheduledAt:   scheduledAt,
				AttemptNumber: attempt,
				Recipient:     user,
			}
			msg := pol.EscalationMsg(ctx)
			checkSafe(t, polName+" attempt "+itoa(attempt), msg)
		}
		// Mensagem na ultima tentativa: deve sinalizar "anotar" + medico/familia.
		ctxFinal := EscalationContext{
			User:          user,
			Medication:    med,
			ScheduledAt:   scheduledAt,
			AttemptNumber: pol.MaxAttempts,
			Recipient:     user,
		}
		msgFinal := pol.EscalationMsg(ctxFinal)
		if !wantAnotarRegex.MatchString(msgFinal) {
			t.Errorf("%s final attempt should mention 'anotar/anotei/nao tomada': %q", polName, msgFinal)
		}
		if !wantMedFamRegex.MatchString(msgFinal) {
			t.Errorf("%s final attempt should mention medico/familia: %q", polName, msgFinal)
		}
	}

	// finalFamilyMsg: NAO orienta dose tardia, e cita "nao oriento" + medico.
	famCtx := EscalationContext{
		User:              user,
		Medication:        med,
		ScheduledAt:       scheduledAt,
		AttemptNumber:     4,
		Recipient:         &User{ID: 2, Name: "Maria"},
		IsFinalEscalation: true,
	}
	famMsg := finalFamilyMsg(famCtx)
	checkSafe(t, "finalFamilyMsg", famMsg)
	if !regexp.MustCompile(`(?i)n[ãa]o oriento`).MatchString(famMsg) {
		t.Errorf("finalFamilyMsg should contain 'nao oriento': %q", famMsg)
	}
	if !regexp.MustCompile(`(?i)m[ée]dico`).MatchString(famMsg) {
		t.Errorf("finalFamilyMsg should mention 'medico': %q", famMsg)
	}
}

// itoa pequeno helper pra formatar inteiros sem importar strconv.
func itoa(i int) string {
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
// 11.4 — Restart no meio de escalacao
// =========================================================================

func TestEscalation_SurvivesRestart(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)

	pc := mkMedicationPending(t, db, user, m, scheduledAt, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	t0 := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)
	// Attempt 1
	pcLoaded, _ := db.GetPendingConfirmation(user.ID)
	eng.HandlePending(t0, pcLoaded)
	pcLoaded, _ = db.GetPendingConfirmation(user.ID)
	if pcLoaded.AttemptNumber != 1 {
		t.Fatalf("expected attempt 1 after first call, got %d", pcLoaded.AttemptNumber)
	}

	// Attempt 2 — t0 + 5min.
	t5 := t0.Add(5 * time.Minute)
	eng.HandlePending(t5, pcLoaded)
	pcLoaded, _ = db.GetPendingConfirmation(user.ID)
	if pcLoaded.AttemptNumber != 2 {
		t.Fatalf("expected attempt 2 after 5min, got %d", pcLoaded.AttemptNumber)
	}

	// Simula RESTART: novo engine, sem estado em memoria.
	eng2 := NewEscalationEngine(db, notif)
	t11 := t0.Add(11 * time.Minute)
	pcLoaded2, _ := db.GetPendingConfirmation(user.ID)
	eng2.HandlePending(t11, pcLoaded2)
	pcLoaded2, _ = db.GetPendingConfirmation(user.ID)
	if pcLoaded2.AttemptNumber != 3 {
		t.Fatalf("expected attempt 3 after restart, got %d", pcLoaded2.AttemptNumber)
	}

	escs, _ := db.ListEscalationsForPending(pc.ID)
	if len(escs) != 3 {
		t.Fatalf("expected 3 escalation rows total, got %d", len(escs))
	}
	if len(notif.Sent()) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(notif.Sent()))
	}
}

// =========================================================================
// 11.5 — Race condition: ticks simultaneos
// =========================================================================

func TestEscalation_ConcurrentTicksNoDouble(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	pc := mkMedicationPending(t, db, user, m, scheduledAt, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	now := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)

	// 2 goroutines tentando handle simultaneo.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pcLocal, _ := db.GetPendingConfirmation(user.ID)
			eng.HandlePending(now, pcLocal)
		}()
	}
	wg.Wait()

	// UNIQUE em (pc, attempt, recipient) garante exatamente 1 row.
	escs, _ := db.ListEscalationsForPending(pc.ID)
	uniqueAttempts := map[int]bool{}
	for _, e := range escs {
		uniqueAttempts[e.AttemptNumber] = true
	}
	if len(uniqueAttempts) != 1 {
		t.Fatalf("expected 1 unique attempt_number, got %v", uniqueAttempts)
	}
	if !uniqueAttempts[1] {
		t.Fatalf("expected attempt_number=1, got %v", uniqueAttempts)
	}
	// Rows totais: pode ser 1 (se uma goroutine perdeu o UNIQUE) ou
	// 2 com o mesmo attempt num — mas SQLite UNIQUE rejeita o segundo,
	// entao deve ser 1.
	if len(escs) != 1 {
		t.Fatalf("expected exactly 1 escalation row, got %d", len(escs))
	}
}

// =========================================================================
// 11.7 — Critico vs default
// =========================================================================

func TestEscalationPolicy_CriticalUsesShorterInterval(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Antonia")
	user := users[0]
	m1, _ := mkMedForUser(t, db, user, "DefaultMed", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	m2, _ := mkMedForUser(t, db, user, "CriticalMed", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", true)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)

	pc1 := mkMedicationPending(t, db, user, m1, scheduledAt, "medication_default")
	pc2 := mkMedicationPending(t, db, user, m2, scheduledAt, "medication_critical")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// T0: attempt 1 para ambos.
	t0 := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)
	pc1Loaded, _ := loadPCByID(t, db, pc1.ID)
	pc2Loaded, _ := loadPCByID(t, db, pc2.ID)
	eng.HandlePending(t0, pc1Loaded)
	eng.HandlePending(t0, pc2Loaded)

	// T0+3min: critical pode avancar (interval 3min); default ainda nao
	// (interval 5min).
	t3 := t0.Add(3 * time.Minute)
	pc1Loaded, _ = loadPCByID(t, db, pc1.ID)
	pc2Loaded, _ = loadPCByID(t, db, pc2.ID)
	eng.HandlePending(t3, pc1Loaded)
	eng.HandlePending(t3, pc2Loaded)

	pc1After, _ := loadPCByID(t, db, pc1.ID)
	pc2After, _ := loadPCByID(t, db, pc2.ID)
	if pc1After.AttemptNumber != 1 {
		t.Fatalf("default should still be at attempt 1 at +3min, got %d", pc1After.AttemptNumber)
	}
	if pc2After.AttemptNumber != 2 {
		t.Fatalf("critical should be at attempt 2 at +3min, got %d", pc2After.AttemptNumber)
	}

	// T0+5min: default avanca para 2.
	t5 := t0.Add(5 * time.Minute)
	pc1Loaded, _ = loadPCByID(t, db, pc1.ID)
	eng.HandlePending(t5, pc1Loaded)
	pc1After, _ = loadPCByID(t, db, pc1.ID)
	if pc1After.AttemptNumber != 2 {
		t.Fatalf("default should be at attempt 2 at +5min, got %d", pc1After.AttemptNumber)
	}
}

// =========================================================================
// 11.8 — Familia avisada apos N tentativas
// =========================================================================

func TestEscalation_NotifiesFamilyAfterMaxAttempts(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa", "Filha", "Filho")
	idosa := users[0]
	for _, g := range []*User{users[1], users[2]} {
		if _, err := db.LinkFamily(g.ID, idosa.ID, "filho"); err != nil {
			t.Fatal(err)
		}
	}

	m, _ := mkMedForUser(t, db, idosa, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	pc := mkMedicationPending(t, db, idosa, m, scheduledAt, "medication_default")

	// Avanca attempt manualmente: 3 tentativas pra esgotar default.
	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	tn := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		pcLoaded, _ := loadPCByID(t, db, pc.ID)
		eng.HandlePending(tn, pcLoaded)
		tn = tn.Add(5 * time.Minute)
	}

	notif.Reset() // descarta msgs ao proprio user
	// Attempt 4 (esgotou default) -> escala pra familia.
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(tn, pcLoaded)

	sent := notif.Sent()
	if len(sent) != 2 {
		t.Fatalf("expected 2 messages (one per guardian), got %d", len(sent))
	}
	for _, m := range sent {
		if !strings.Contains(m.Body, "Idosa") {
			t.Errorf("guardian msg should mention idoso name: %q", m.Body)
		}
	}

	escs, _ := db.ListEscalationsForPending(pc.ID)
	// 3 attempts pro user + 2 pros guardians = 5 rows
	if len(escs) != 5 {
		t.Fatalf("expected 5 escalation rows, got %d", len(escs))
	}

	// intake_log = escalated.
	logs, _ := db.ListIntakeLogsForMedication(m.ID, 5)
	if logs[0].Status != IntakeEscalated {
		t.Fatalf("expected status=escalated, got %s", logs[0].Status)
	}

	// pending = escalated.
	var status string
	db.conn.QueryRow(`SELECT status FROM pending_confirmations WHERE id = ?`, pc.ID).Scan(&status)
	if status != "escalated" {
		t.Fatalf("expected pending.status=escalated, got %s", status)
	}
}

// =========================================================================
// 11.9 — Sem guardian = missed, sem alerta
// =========================================================================

func TestEscalation_NoGuardianMarksMissed(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Solitaria")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "Losartana", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	pc := mkMedicationPending(t, db, user, m, scheduledAt, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	// 3 attempts (esgota default).
	tn := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		pcLoaded, _ := loadPCByID(t, db, pc.ID)
		eng.HandlePending(tn, pcLoaded)
		tn = tn.Add(5 * time.Minute)
	}

	notif.Reset()
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(tn, pcLoaded)

	if len(notif.Sent()) != 0 {
		t.Fatalf("expected no extra messages without guardian, got %d", len(notif.Sent()))
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

func TestHandlePending_TooSoon_NoOp(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	pc := mkMedicationPending(t, db, user, m, scheduledAt, "medication_default")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)

	t0 := time.Date(2026, 5, 9, 17, 1, 0, 0, time.UTC)
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(t0, pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("first call should send")
	}

	// Logo depois (1min) — interval default eh 5min, deve nao enviar.
	t1 := t0.Add(1 * time.Minute)
	pcLoaded, _ = loadPCByID(t, db, pc.ID)
	eng.HandlePending(t1, pcLoaded)
	if len(notif.Sent()) != 1 {
		t.Fatalf("second call within interval should NOT send (got %d sends)", len(notif.Sent()))
	}
}

func TestHandlePending_UnknownPolicy_LogsAndReturns(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A")
	user := users[0]
	m, _ := mkMedForUser(t, db, user, "X", "FREQ=DAILY;BYHOUR=14;BYMINUTE=0", false)
	scheduledAt := time.Date(2026, 5, 9, 17, 0, 0, 0, time.UTC)
	pc := mkMedicationPending(t, db, user, m, scheduledAt, "policy_inexistente")

	notif := &recordingNotifier{}
	eng := NewEscalationEngine(db, notif)
	pcLoaded, _ := loadPCByID(t, db, pc.ID)
	eng.HandlePending(time.Now(), pcLoaded)

	if len(notif.Sent()) != 0 {
		t.Fatalf("unknown policy: should not send")
	}
}

// =========================================================================
// Helpers
// =========================================================================

// loadPCByID busca um pending por id (a versao publica busca pelo user e
// pega o ultimo — usar por id eh mais determinista nos testes).
func loadPCByID(t *testing.T, db *DB, id int64) (*PendingConfirmation, error) {
	t.Helper()
	pc := &PendingConfirmation{}
	var kind, policy sqlNullStringT
	var lastAttempt sqlNullTimeT
	var attempt sqlNullInt64T
	err := db.conn.QueryRow(
		`SELECT id, user_id, event_data, status, created_at,
		        kind, escalation_policy, last_attempt_at, attempt_number
		 FROM pending_confirmations WHERE id = ?`, id,
	).Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
		&kind, &policy, &lastAttempt, &attempt)
	if err != nil {
		t.Fatalf("load pc: %v", err)
	}
	fillPendingExtras(pc, kind.NullString, policy.NullString, lastAttempt.NullTime, attempt.NullInt64)
	return pc, nil
}
