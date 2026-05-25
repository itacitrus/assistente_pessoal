package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// stubSnapshotWriter implementa SnapshotWriter pra testar o catchup sem
// chamar Haiku. Conta chamadas e devolve nil sempre.
type stubSnapshotWriter struct {
	calls   int
	lastUID int64
	lastDay time.Time
}

func (s *stubSnapshotWriter) MaybeUpdateSnapshot(_ context.Context, userID int64) error {
	s.calls++
	s.lastUID = userID
	return nil
}

func (s *stubSnapshotWriter) UpdateSnapshotForDay(_ context.Context, userID int64, day time.Time) error {
	s.calls++
	s.lastUID = userID
	s.lastDay = day
	return nil
}

// makeSchedulerForTest constroi um Scheduler minimo. cron desligado (nao
// inicia AddFunc) — chamamos os jobs diretamente.
func makeSchedulerForTest(db *DB, sendMsg func(phone, text string) error) *Scheduler {
	return &Scheduler{
		db:      db,
		sendMsg: sendMsg,
		nowFunc: time.Now,
	}
}

func TestCheckInactivityEscalation_NoDuplicateOnRestart(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	// Tentativa proativa ha 5h. Sem resposta.
	pastAttempt := time.Now().UTC().Add(-5 * time.Hour)
	res, err := db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'sent')`,
		elder.ID, pastAttempt, "ola?",
	)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, _ := res.LastInsertId()
	_ = attemptID
	// Threshold default = 4h, ja ultrapassou.

	sent := 0
	sendMsg := func(phone, text string) error {
		sent++
		return nil
	}
	s := makeSchedulerForTest(db, sendMsg)
	got, _ := db.GetUserByID(elder.ID)
	s.checkInactivityEscalationForElder(got)
	// Roda 3x — deve enviar so 1.
	s.checkInactivityEscalationForElder(got)
	s.checkInactivityEscalationForElder(got)

	if sent != 1 {
		t.Errorf("expected 1 send (idempotent), got %d", sent)
	}
	rows := countEscalationsForElder(t, db, elder.ID, "inactivity")
	if rows != 1 {
		t.Errorf("expected 1 escalation row, got %d", rows)
	}
}

func TestCheckInactivityEscalation_NoDispatchIfRecentReply(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	pastAttempt := time.Now().UTC().Add(-5 * time.Hour)
	db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'replied')`,
		elder.ID, pastAttempt, "ola?",
	)

	sent := 0
	s := makeSchedulerForTest(db, func(phone, text string) error { sent++; return nil })
	got, _ := db.GetUserByID(elder.ID)
	s.checkInactivityEscalationForElder(got)
	if sent != 0 {
		t.Errorf("expected 0 sends (already replied), got %d", sent)
	}
}

func TestCheckInactivityEscalation_NotifyFlagFalse(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	link, _ := db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	db.UpdateNotifyPreferences(link.ID, FamilyNotifyPrefs{
		OnMedicationMiss: true,
		OnInactivity:     false, // OPT-OUT
		OnSevereSignal:   true,
	})

	pastAttempt := time.Now().UTC().Add(-5 * time.Hour)
	db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'sent')`,
		elder.ID, pastAttempt, "ola?",
	)

	sent := 0
	s := makeSchedulerForTest(db, func(phone, text string) error { sent++; return nil })
	got, _ := db.GetUserByID(elder.ID)
	s.checkInactivityEscalationForElder(got)
	if sent != 0 {
		t.Errorf("expected 0 sends (notify_on_inactivity=false), got %d", sent)
	}
}

func TestCheckInactivityEscalation_RespectsConsentRevoked(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	db.SetDependentConsent(guardian.ID, elder.ID, ConsentRevoked)

	pastAttempt := time.Now().UTC().Add(-5 * time.Hour)
	db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'sent')`,
		elder.ID, pastAttempt, "ola?",
	)

	sent := 0
	s := makeSchedulerForTest(db, func(phone, text string) error { sent++; return nil })
	got, _ := db.GetUserByID(elder.ID)
	s.checkInactivityEscalationForElder(got)
	if sent != 0 {
		t.Errorf("expected 0 sends (consent revoked), got %d", sent)
	}
}

func TestCheckInactivityEscalation_AuditsTrigger(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	pastAttempt := time.Now().UTC().Add(-5 * time.Hour)
	db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'sent')`,
		elder.ID, pastAttempt, "ola?",
	)

	s := makeSchedulerForTest(db, func(phone, text string) error { return nil })
	// Injeta agent (com audit) para teste.
	s.agent = &Agent{db: db, audit: NewAuditLog(db)}

	got, _ := db.GetUserByID(elder.ID)
	s.checkInactivityEscalationForElder(got)

	rows, _ := db.conn.Query(`SELECT action FROM action_log WHERE user_id = ? AND action = 'inactivity_escalation_triggered'`, elder.ID)
	defer rows.Close()
	if !rows.Next() {
		t.Error("expected inactivity_escalation_triggered in audit_log")
	}
}

func TestRunDailyPsychSnapshotCatchup_FillsMissingDays(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	// Mensagens ontem (em UTC; o catchup le yesterday=now-24h).
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	db.conn.Exec(
		`INSERT INTO conversation_history (user_id, role, content, created_at)
		 VALUES (?, 'user', 'oi', ?)`, elder.ID, yesterday)

	stub := &stubSnapshotWriter{}
	SetSnapshotWriterForCatchup(stub)
	defer SetSnapshotWriterForCatchup(nil)

	s := makeSchedulerForTest(db, nil)
	s.runDailyPsychSnapshotCatchup()
	if stub.calls != 1 {
		t.Errorf("expected 1 call, got %d", stub.calls)
	}
	if stub.lastUID != elder.ID {
		t.Errorf("expected uid=%d, got %d", elder.ID, stub.lastUID)
	}
}

func TestRunDailyPsychSnapshotCatchup_BackfillsOlderGap(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	// Mensagem ha 3 dias, sem snapshot — buraco que o caminho de tempo real
	// nao cobre e que o catchup antigo (so "hoje") nunca recuperava.
	threeDaysAgo := time.Now().UTC().Add(-3 * 24 * time.Hour)
	db.conn.Exec(
		`INSERT INTO conversation_history (user_id, role, content, created_at)
		 VALUES (?, 'user', 'oi', ?)`, elder.ID, threeDaysAgo)

	stub := &stubSnapshotWriter{}
	SetSnapshotWriterForCatchup(stub)
	defer SetSnapshotWriterForCatchup(nil)

	s := makeSchedulerForTest(db, nil)
	s.runDailyPsychSnapshotCatchup()

	if stub.calls != 1 {
		t.Fatalf("expected 1 call (backfill 3-day gap), got %d", stub.calls)
	}
	if stub.lastUID != elder.ID {
		t.Errorf("expected uid=%d, got %d", elder.ID, stub.lastUID)
	}
	// Confirma que backfillamos o DIA CERTO (3 dias atras em local tz), nao hoje.
	tz := db.GetEventTimezone(elder.ID, threeDaysAgo)
	if tz == nil {
		tz = BRT()
	}
	local := threeDaysAgo.In(tz)
	want := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
	if !stub.lastDay.Equal(want) {
		t.Errorf("expected backfill day=%s, got %s", want.Format("2006-01-02"), stub.lastDay.Format("2006-01-02"))
	}
}

func TestRunDailyPsychSnapshotCatchup_SkipsWhenSnapshotExists(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	db.conn.Exec(`INSERT INTO conversation_history (user_id, role, content, created_at)
		 VALUES (?, 'user', 'oi', ?)`, elder.ID, yesterday)

	// Insere snapshot ja com confidence=3 (>= 2 threshold).
	tz := db.GetEventTimezone(elder.ID, yesterday)
	if tz == nil {
		tz = BRT()
	}
	localDay := yesterday.In(tz)
	dayDate := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 0, 0, 0, 0, tz)
	db.UpsertPsychSnapshot(&synthesis.DailySnapshot{
		UserID: elder.ID, SnapshotDate: dayDate,
		HumorScore: 4, Confidence: 3,
	})

	stub := &stubSnapshotWriter{}
	SetSnapshotWriterForCatchup(stub)
	defer SetSnapshotWriterForCatchup(nil)

	s := makeSchedulerForTest(db, nil)
	s.runDailyPsychSnapshotCatchup()
	if stub.calls != 0 {
		t.Errorf("expected 0 calls (snapshot exists), got %d", stub.calls)
	}
}

func TestRunDailyPsychSnapshotCatchup_SkipsRevokedConsent(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	db.SetDependentConsent(guardian.ID, elder.ID, ConsentRevoked)

	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	db.conn.Exec(`INSERT INTO conversation_history (user_id, role, content, created_at)
		 VALUES (?, 'user', 'oi', ?)`, elder.ID, yesterday)

	stub := &stubSnapshotWriter{}
	SetSnapshotWriterForCatchup(stub)
	defer SetSnapshotWriterForCatchup(nil)

	s := makeSchedulerForTest(db, nil)
	s.runDailyPsychSnapshotCatchup()
	if stub.calls != 0 {
		t.Errorf("expected 0 calls (consent revoked), got %d", stub.calls)
	}
}

func TestRunDailyPsychSnapshotCatchup_NoWriterInjected(t *testing.T) {
	resetPhase5State()
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	db.conn.Exec(`INSERT INTO conversation_history (user_id, role, content, created_at)
		 VALUES (?, 'user', 'oi', ?)`, elder.ID, yesterday)

	SetSnapshotWriterForCatchup(nil)
	s := makeSchedulerForTest(db, nil)
	// Nao panica nem erra — apenas loga e retorna.
	s.runDailyPsychSnapshotCatchup()
}

func TestShouldRunPhase5_Cooldown(t *testing.T) {
	resetPhase5State()
	if !shouldRunPhase5("test", 30*time.Minute) {
		t.Fatal("first call should run")
	}
	// Segunda chamada imediata: cooldown bloqueia.
	if shouldRunPhase5("test", 30*time.Minute) {
		t.Fatal("second call within cooldown should NOT run")
	}
}

func TestRelationshipPT(t *testing.T) {
	cases := map[string]string{
		"filho_de":  "mãe",
		"filha_de":  "mãe",
		"marido_de": "esposa",
		"esposa_de": "marido",
		"neto_de":   "avó",
		"weird":     "familiar",
	}
	for in, want := range cases {
		if got := relationshipPT(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

func TestHumanizeIdleHours(t *testing.T) {
	cases := map[int]string{
		0:  "menos de uma hora",
		1:  "1 hora",
		5:  "5 horas",
		24: "1 dia",
		48: "2 dias",
	}
	for h, want := range cases {
		if got := humanizeIdleHours(h); got != want {
			t.Errorf("%d: got %q, want %q", h, got, want)
		}
	}
}

func TestBuildInactivityEscalationMsg(t *testing.T) {
	elder := &User{Name: "Antonia"}
	link := &FamilyLink{
		Relationship: "filho_de",
		Other:        &User{Name: "Caio Silva"},
	}
	msg := buildInactivityEscalationMsg(elder, link, 5)
	if !strings.Contains(msg, "Caio") {
		t.Errorf("expected 'Caio' in msg: %s", msg)
	}
	if !strings.Contains(msg, "Antonia") {
		t.Errorf("expected 'Antonia' in msg: %s", msg)
	}
	if !strings.Contains(msg, "5 horas") {
		t.Errorf("expected '5 horas' in msg: %s", msg)
	}
}

// ===== helpers locais =====

func countEscalationsForElder(t *testing.T, db *DB, userID int64, policy string) int {
	t.Helper()
	var n int
	if err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM escalations WHERE user_id = ? AND policy_name = ?`,
		userID, policy,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
