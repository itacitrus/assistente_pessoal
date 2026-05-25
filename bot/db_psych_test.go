package main

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

func makeElder(t *testing.T, db *DB, name, phone string) *User {
	t.Helper()
	u := &User{PhoneNumber: phone, Name: name, GoogleCalendarID: phone + "@g.com", GoogleCredentials: "x"}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser %s: %v", name, err)
	}
	if err := db.SetUserType(u.ID, UserTypeIdoso); err != nil {
		t.Fatalf("SetUserType: %v", err)
	}
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return got
}

func makeGuardian(t *testing.T, db *DB, name, phone string) *User {
	t.Helper()
	u := &User{PhoneNumber: phone, Name: name, GoogleCalendarID: phone + "@g.com", GoogleCredentials: "x"}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser %s: %v", name, err)
	}
	got, _ := db.GetUserByID(u.ID)
	return got
}

func TestUpsertPsychSnapshot_SameDayUpdates(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	today := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)

	s1 := synthesis.DailySnapshot{
		UserID: elder.ID, SnapshotDate: today,
		HumorScore: 3, Confidence: 2, NMessages: 3,
	}
	if err := db.UpsertPsychSnapshot(&s1); err != nil {
		t.Fatalf("Upsert s1: %v", err)
	}
	s2 := synthesis.DailySnapshot{
		UserID: elder.ID, SnapshotDate: today,
		HumorScore: 4, Confidence: 4, NMessages: 12,
		SinaisObservados: []string{"refinado"},
	}
	if err := db.UpsertPsychSnapshot(&s2); err != nil {
		t.Fatalf("Upsert s2: %v", err)
	}
	snaps, err := db.GetSnapshotsForUserDateRange(elder.ID, today, today)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 row, got %d", len(snaps))
	}
	if snaps[0].HumorScore != 4 {
		t.Errorf("HumorScore: %d", snaps[0].HumorScore)
	}
	if snaps[0].NMessages != 12 {
		t.Errorf("NMessages: %d", snaps[0].NMessages)
	}
	if len(snaps[0].SinaisObservados) != 1 || snaps[0].SinaisObservados[0] != "refinado" {
		t.Errorf("SinaisObservados not persisted: %+v", snaps[0].SinaisObservados)
	}
}

func TestUpsertPsychSnapshot_ZeroScoreBecomesNull(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	day := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)

	// Score 0 deve virar NULL no banco — refletindo "sem dado pra inferir".
	s := synthesis.DailySnapshot{
		UserID: elder.ID, SnapshotDate: day,
		HumorScore: 0, EnergiaScore: 3, Confidence: 1,
	}
	if err := db.UpsertPsychSnapshot(&s); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetSnapshot(elder.ID, day)
	if err != nil || got == nil {
		t.Fatalf("GetSnapshot: err=%v got=%v", err, got)
	}
	if got.HumorScore != 0 {
		t.Errorf("HumorScore should be 0/NULL, got %d", got.HumorScore)
	}
	if got.EnergiaScore != 3 {
		t.Errorf("EnergiaScore: %d", got.EnergiaScore)
	}
}

func TestGetSnapshot_NotFound(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	day := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)

	got, err := db.GetSnapshot(elder.ID, day)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestReviewAlert_AcknowledgesScopedAndIdempotent(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	other := makeElder(t, db, "Bento", "333")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	now := time.Now().UTC()
	esc, err := db.RecordSevereSignalEscalation(
		elder.ID, "severe_signal_safety_net", "warn", "x", guardian.ID, "whatsapp", now)
	if err != nil {
		t.Fatalf("record escalation: %v", err)
	}
	alertID := esc.ID

	open, _ := db.GetOpenAlertsForUser(elder.ID)
	if len(open) != 1 {
		t.Fatalf("expected 1 open alert, got %d", len(open))
	}

	// Dependente errado: nao casa (escopo).
	if ok, _ := db.ReviewAlert(alertID, other.ID, guardian.ID, "nope"); ok {
		t.Error("review with wrong dependent should not match")
	}

	// Revisa: marca acknowledged, some da lista.
	ok, err := db.ReviewAlert(alertID, elder.ID, guardian.ID, "liguei, está bem")
	if err != nil || !ok {
		t.Fatalf("review: ok=%v err=%v", ok, err)
	}
	open, _ = db.GetOpenAlertsForUser(elder.ID)
	if len(open) != 0 {
		t.Errorf("expected 0 open alerts after review, got %d", len(open))
	}

	// Idempotente: revisar de novo nao casa (ja nao esta aberto).
	if ok, _ := db.ReviewAlert(alertID, elder.ID, guardian.ID, "again"); ok {
		t.Error("second review should not match (already acknowledged)")
	}

	// Persistiu reviewer + nota.
	var by sql.NullInt64
	var note string
	if err := db.conn.QueryRow(
		`SELECT reviewed_by, review_note FROM escalations WHERE id = ?`, alertID,
	).Scan(&by, &note); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !by.Valid || by.Int64 != guardian.ID {
		t.Errorf("reviewed_by = %v, want %d", by, guardian.ID)
	}
	if note != "liguei, está bem" {
		t.Errorf("review_note = %q", note)
	}
}

func TestParseSnapshotDate_TolerantFormats(t *testing.T) {
	want := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	cases := map[string]bool{ // input -> deve casar com want
		"2026-05-25":                 true, // formato atual (writer)
		"2026-05-25T00:00:00Z":       true, // timestamp RFC3339 (linha antiga)
		"2026-05-25 00:00:00+00:00":  true, // timestamp com espaco (driver sqlite)
		"2026-05-25T13:45:00-03:00":  true, // com hora/fuso — so a data importa
		"":                           false,
		"lixo":                       false,
	}
	for in, shouldMatch := range cases {
		got := parseSnapshotDate(in)
		if shouldMatch {
			if !got.Equal(want) {
				t.Errorf("parseSnapshotDate(%q) = %s, want %s", in, got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		} else if !got.IsZero() {
			t.Errorf("parseSnapshotDate(%q) = %s, want zero", in, got.Format(time.RFC3339))
		}
	}
}

// TestGetSnapshotsForUserDateRange_RecoversTimestampDate garante que uma linha
// com snapshot_date gravado como timestamp completo (legado) ainda volta com a
// data correta — nao mais zerada (que virava "31/12" no painel).
func TestGetSnapshotsForUserDateRange_RecoversTimestampDate(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	// Insere direto com snapshot_date em formato timestamp, simulando linha antiga.
	_, err := db.conn.Exec(`
		INSERT INTO psych_state_daily (user_id, snapshot_date, humor_score, confidence)
		VALUES (?, ?, ?, ?)`, elder.ID, "2026-05-25T00:00:00Z", 4, 3)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	snaps, err := db.GetSnapshotsForUserDateRange(elder.ID, from, to)
	if err != nil {
		t.Fatalf("GetSnapshotsForUserDateRange: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	want := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	if !snaps[0].SnapshotDate.Equal(want) {
		t.Errorf("SnapshotDate = %s, want %s", snaps[0].SnapshotDate.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestGetSocialContextRiskMemos_OnlyRiskoPrefix(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")

	// Salva memos variados.
	db.SaveMemory(elder.ID, "social_context", "pessoa:dona_marta", "vizinha do 302")
	db.SaveMemory(elder.ID, "social_context", "evento:consulta", "consulta dia 15")
	db.SaveMemory(elder.ID, "social_context", "rotina:cha_noite", "cha noturno")
	db.SaveMemory(elder.ID, "social_context", "risco:queda_recente", "caiu na cozinha")
	db.SaveMemory(elder.ID, "social_context", "risco:isolamento", "tem ficado em casa")
	db.SaveMemory(elder.ID, "outras", "risco:falso", "nao deveria aparecer (categoria errada)")

	memos, err := db.GetSocialContextRiskMemos(elder.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memos) != 2 {
		t.Fatalf("expected 2 risco:* memos, got %d: %+v", len(memos), memos)
	}
	for _, m := range memos {
		if !strings.HasPrefix(m.Key, "risco:") {
			t.Errorf("non-risco memo leaked: %s", m.Key)
		}
	}
}

func TestGetMedicationStats7d_AggregatesByStatus(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	now := time.Now().UTC()

	// Cria medicamento + intake_log direto (mais simples que via tools).
	medID := insertMedicationForTest(t, db, elder.ID, "Losartana")
	insertIntakeForTest(t, db, medID, now.Add(-1*24*time.Hour), "taken")
	insertIntakeForTest(t, db, medID, now.Add(-2*24*time.Hour), "taken")
	insertIntakeForTest(t, db, medID, now.Add(-3*24*time.Hour), "missed")
	insertIntakeForTest(t, db, medID, now.Add(-4*24*time.Hour), "skipped")

	from := now.Add(-7 * 24 * time.Hour)
	to := now.Add(1 * time.Hour)
	stats, err := db.GetMedicationStats7d(elder.ID, from, to)
	if err != nil {
		t.Fatalf("GetMedicationStats7d: %v", err)
	}
	if stats.Taken != 2 {
		t.Errorf("Taken: %d", stats.Taken)
	}
	if stats.Missed != 1 {
		t.Errorf("Missed: %d", stats.Missed)
	}
	if stats.Skipped != 1 {
		t.Errorf("Skipped: %d", stats.Skipped)
	}
	if stats.Scheduled != 4 {
		t.Errorf("Scheduled: %d", stats.Scheduled)
	}
	expectedFrac := 2.0 / 4.0
	if stats.AdherenceFrac < expectedFrac-0.001 || stats.AdherenceFrac > expectedFrac+0.001 {
		t.Errorf("AdherenceFrac: %.3f", stats.AdherenceFrac)
	}
	if len(stats.MissedDoses) != 1 {
		t.Errorf("MissedDoses: %d", len(stats.MissedDoses))
	}
}

func TestGetProactiveAttemptsStats(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")

	now := time.Now().UTC()
	_, _ = db.RecordProactiveAttempt(elder.ID, "ola, tudo bem?")
	_, _ = db.RecordProactiveAttempt(elder.ID, "ainda por ai?")

	stats, err := db.GetProactiveAttemptsStats(elder.ID, now.Add(-7*24*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Last7d != 2 {
		t.Errorf("Last7d: %d", stats.Last7d)
	}
	if !stats.LastAttemptAt.Valid {
		t.Errorf("LastAttemptAt should be valid")
	}
	if stats.LastAcked {
		t.Errorf("LastAcked should be false (no reply)")
	}
}

func TestGetDependentConsent_DefaultsActive(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	consent, err := db.GetDependentConsent(guardian.ID, elder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if consent != ConsentActive {
		t.Errorf("expected active, got %s", consent)
	}
}

func TestSetDependentConsent_Revoke(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDependentConsent(guardian.ID, elder.ID, ConsentRevoked); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetDependentConsent(guardian.ID, elder.ID)
	if got != ConsentRevoked {
		t.Errorf("expected revoked, got %s", got)
	}
}

func TestSetDependentConsent_RejectsInvalid(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDependentConsent(guardian.ID, elder.ID, "weird"); err == nil {
		t.Fatal("expected error for invalid consent value")
	}
}

func TestGetDependentConsent_MissingLinkReturnsActive(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	// SEM LinkFamily.
	got, err := db.GetDependentConsent(guardian.ID, elder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != ConsentActive {
		t.Errorf("expected default active, got %s", got)
	}
}

func TestGetGuardiansForInactivity_FiltersByFlagAndConsent(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	g1 := makeGuardian(t, db, "Caio", "222")
	g2 := makeGuardian(t, db, "Beto", "333")

	link1, _ := db.LinkFamily(g1.ID, elder.ID, "filho_de")
	link2, _ := db.LinkFamily(g2.ID, elder.ID, "filha_de")
	_ = link1
	_ = link2

	// g2: notify_on_inactivity=false (via update).
	db.UpdateNotifyPreferences(link2.ID, FamilyNotifyPrefs{
		OnMedicationMiss: true,
		OnInactivity:     false,
		OnSevereSignal:   true,
	})

	out, err := db.GetGuardiansForInactivity(elder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 guardian (only g1), got %d", len(out))
	}
	if out[0].GuardianID != g1.ID {
		t.Errorf("expected g1, got %d", out[0].GuardianID)
	}

	// Agora revoga g1 — deve sumir tambem.
	db.SetDependentConsent(g1.ID, elder.ID, ConsentRevoked)
	out2, _ := db.GetGuardiansForInactivity(elder.ID)
	if len(out2) != 0 {
		t.Errorf("expected 0 (g1 revoked, g2 opted out), got %d", len(out2))
	}
}

func TestHasOpenInactivityEscalation_Idempotency(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	attemptID, err := db.RecordProactiveAttempt(elder.ID, "alo?")
	if err != nil {
		t.Fatal(err)
	}

	exists, _ := db.HasOpenInactivityEscalation(elder.ID, attemptID)
	if exists {
		t.Fatal("expected no escalation yet")
	}

	now := time.Now()
	if _, err := db.CreateInactivityEscalation(elder.ID, guardian.ID, attemptID, "warn", "x", now); err != nil {
		t.Fatal(err)
	}

	exists, _ = db.HasOpenInactivityEscalation(elder.ID, attemptID)
	if !exists {
		t.Fatal("expected escalation now")
	}
}

func TestUpdateEscalationStatus(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	attemptID, _ := db.RecordProactiveAttempt(elder.ID, "alo?")
	now := time.Now()
	escID, _ := db.CreateInactivityEscalation(elder.ID, guardian.ID, attemptID, "warn", "x", now)

	if err := db.UpdateEscalationStatus(escID, "failed"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateEscalationStatus(escID, "weird"); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestListUsersByType_FiltersIdosos(t *testing.T) {
	db := setupTestDB(t)
	makeElder(t, db, "Antonia", "111")
	makeElder(t, db, "Joaquim", "222")
	makeGuardian(t, db, "Caio", "333") // type=comum

	idosos, err := db.ListUsersByType(UserTypeIdoso)
	if err != nil {
		t.Fatal(err)
	}
	if len(idosos) != 2 {
		t.Errorf("expected 2 idosos, got %d", len(idosos))
	}
}

func TestGetOpenAlertsForUser(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	guardian := makeGuardian(t, db, "Caio", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")

	now := time.Now()
	// Cria 1 escalation pending (severe_signal) e 1 sent (inactivity).
	_, err := db.RecordSevereSignalEscalation(elder.ID, "severe_signal", "warn",
		"x", guardian.ID, "whatsapp", now)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, _ := db.RecordProactiveAttempt(elder.ID, "alo")
	_, _ = db.CreateInactivityEscalation(elder.ID, guardian.ID, attemptID, "warn", "y", now)

	alerts, err := db.GetOpenAlertsForUser(elder.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Ambas em status='sent' → contam como abertas.
	if len(alerts) != 2 {
		t.Errorf("expected 2 open alerts, got %d: %+v", len(alerts), alerts)
	}
}

func TestGetMessagesSinceForUser(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "111")
	db.AddConversationMessage(elder.ID, "user", "ola")
	db.AddConversationMessage(elder.ID, "assistant", "oi")

	msgs, err := db.GetMessagesSinceForUser(elder.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 msgs, got %d", len(msgs))
	}
}

// ===== helpers locais (intake/medication direto, sem tools) =====

func insertMedicationForTest(t *testing.T, db *DB, userID int64, name string) int64 {
	t.Helper()
	res, err := db.conn.Exec(`INSERT INTO medications (user_id, name, dose, instructions, active, created_by_user_id)
		VALUES (?, ?, '50mg', '', 1, ?)`, userID, name, userID)
	if err != nil {
		t.Fatalf("insert medication: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertIntakeForTest(t *testing.T, db *DB, medID int64, scheduledAt time.Time, status string) {
	t.Helper()
	_, err := db.conn.Exec(`INSERT INTO medication_intake_log (medication_id, scheduled_at, status)
		VALUES (?, ?, ?)`, medID, scheduledAt.UTC(), status)
	if err != nil {
		t.Fatalf("insert intake: %v", err)
	}
}
