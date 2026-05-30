package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// fakeReportProvider eh um stub de llm.ReportProvider que devolve JSON fixo.
// Usado nos tests do tool/handler pra evitar chamada real ao Sonnet.
type fakeReportProvider struct {
	out      synthesis.ReportOutput
	err      error
	captured llm.ReportRequest
}

func (f *fakeReportProvider) Synthesize(_ context.Context, req llm.ReportRequest) (llm.ReportResponse, error) {
	f.captured = req
	if f.err != nil {
		return llm.ReportResponse{}, f.err
	}
	b, _ := json.Marshal(f.out)
	return llm.ReportResponse{Text: string(b)}, nil
}

func (f *fakeReportProvider) Name() string { return "fake" }

// makeAgentForFamily cria um *Agent minimo pra exercitar handleStatusDependente.
func makeAgentForFamily(db *DB, report llm.ReportProvider) *Agent {
	return &Agent{
		db:    db,
		audit: NewAuditLog(db),
		// chat/companion/etc nao usados pelo handler.
		report: report,
	}
}

func TestStatusDependente_NotGuardian(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	// SEM LinkFamily — Caio NAO eh guardian de Antonia.

	rp := &fakeReportProvider{}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{"dependent_id": elder.ID})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err (msg natural), got %v", err)
	}
	if !strings.Contains(out, "não tem autorização") {
		t.Errorf("expected unauthorized msg, got: %s", out)
	}
}

func TestStatusDependente_RevokedConsent(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDependentConsent(guardian.ID, elder.ID, ConsentRevoked); err != nil {
		t.Fatal(err)
	}

	rp := &fakeReportProvider{}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{"dependent_id": elder.ID})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out, "revogou o consentimento") {
		t.Errorf("expected revoked-consent msg, got: %s", out)
	}
	// Confirmar que NUNCA chamou o synthesize provider.
	if rp.captured.UserPrompt != "" {
		t.Errorf("synthesize should NOT have been called, captured: %s", rp.captured.UserPrompt)
	}
}

func TestStatusDependente_AuthorizedReturnsReport(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}

	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		Comparacao:       "humor 4 estavel",
		HumorRecente:     "tem aparecido tom leve",
		Resumo:           "Sua mae tem estado bem na maioria dos dias.",
		NivelPreocupacao: "tranquilo",
	}}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{
		"dependent_id": elder.ID,
		"days":         7,
	})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out, "Status de Antonia") {
		t.Errorf("expected status header, got: %s", out)
	}
	if !strings.Contains(out, "Tendência: estavel") {
		t.Errorf("expected tendencia line, got: %s", out)
	}
}

func TestStatusDependente_ResolvesByName(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia da Silva", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
	}}
	agent := makeAgentForFamily(db, rp)

	// Match fuzzy por substring "antonia".
	params, _ := json.Marshal(map[string]any{"dependent_name": "antonia"})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out, "Status de Antonia da Silva") {
		t.Errorf("expected resolution by name, got: %s", out)
	}
}

// TestStatusDependente_ResolvesByRelationship blinda o caso do responsavel que
// se refere ao dependente pelo PARENTESCO ("meu pai") em vez do nome — antes o
// bot dizia "não tenho o nome salvo" mesmo com o parentesco gravado no vínculo.
func TestStatusDependente_ResolvesByRelationship(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Fábio Vivo", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "pai"); err != nil {
		t.Fatal(err)
	}
	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
	}}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{"dependent_name": "meu pai"})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out, "Status de Fábio Vivo") {
		t.Errorf("expected resolution by relationship 'pai', got: %s", out)
	}
}

func TestStatusDependente_NoIdentifierReturnsErrorMsg(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	rp := &fakeReportProvider{}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(out, "informe") || !strings.Contains(out, "dependent_id") {
		t.Errorf("expected hint about missing identifier, got: %s", out)
	}
}

func TestStatusDependente_DependentNotFound(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	rp := &fakeReportProvider{}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{"dependent_id": 9999})
	out, err := handleStatusDependente(context.Background(), agent, guardian, params)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "não encontrei") {
		t.Errorf("expected 'não encontrei' msg, got: %s", out)
	}
}

// BuildDependentStatus agora LE a sintese persistida em vez de gerar on-demand
// (a geracao foi movida pra RegenerateDependentSynthesis). Sem sintese
// persistida, devolve placeholder + SynthesisStale=true (pro caller regerar).
func TestBuildDependentStatus_ReadsPersistedSynthesis(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}

	// Sem persistida -> placeholder + stale.
	rep, err := BuildDependentStatus(context.Background(), db, nil, elder, 14)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if rep.SynthesisAvailable {
		t.Error("expected SynthesisAvailable=false quando nao ha persistida")
	}
	if !rep.SynthesisStale {
		t.Error("expected SynthesisStale=true quando nao ha persistida")
	}

	// Com persistida -> serve ela, available=true.
	want := synthesis.ReportOutput{Tendencia: "estavel", NivelPreocupacao: "tranquilo", Resumo: "Tudo certo."}
	if err := db.UpsertDependentSynthesis(elder.ID, 14, want, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rep, err = BuildDependentStatus(context.Background(), db, nil, elder, 14)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.SynthesisAvailable {
		t.Error("expected SynthesisAvailable=true com persistida")
	}
	if rep.Synthesis.Tendencia != "estavel" {
		t.Errorf("expected tendencia persistida, got %s", rep.Synthesis.Tendencia)
	}
}

func TestRegenerateDependentSynthesis_DegradesWhenSynthesizeFails(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	if _, err := db.LinkFamily(guardian.ID, elder.ID, "filho_de"); err != nil {
		t.Fatal(err)
	}
	rp := &fakeReportProvider{err: errors.New("api boom")}
	err := RegenerateDependentSynthesis(context.Background(), db, rp, elder, 14)
	if err == nil {
		t.Fatal("expected error when Sonnet fails")
	}
	// Audit synthesis_failed registrado e NADA persistido.
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM action_log WHERE user_id = ? AND action = 'synthesis_failed'`, elder.ID).Scan(&n)
	if n == 0 {
		t.Error("expected synthesis_failed in audit_log")
	}
	if _, gErr := db.GetDependentSynthesis(elder.ID); !errors.Is(gErr, ErrSynthesisNotFound) {
		t.Errorf("nao deveria persistir em falha, got %v", gErr)
	}
}

// Observabilidade: report nil (e por extensao qualquer falha pre-Synthesize) agora
// audita synthesis_failed em vez de retornar mudo — foi o que mascarou a quebra em
// prod (painel congelava sem rastro).
func TestRegenerateDependentSynthesis_AuditsNilReport(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "222")
	if err := RegenerateDependentSynthesis(context.Background(), db, nil, elder, 14); err == nil {
		t.Fatal("expected error with nil report")
	}
	var n int
	db.conn.QueryRow(`SELECT COUNT(*) FROM action_log WHERE user_id = ? AND action = 'synthesis_failed'`, elder.ID).Scan(&n)
	if n == 0 {
		t.Error("report nil deveria auditar synthesis_failed")
	}
}

func TestListDependentSynthesisTargets(t *testing.T) {
	db := setupTestDB(t)
	a := makeElder(t, db, "A", "111")
	b := makeElder(t, db, "B", "222")
	out := synthesis.ReportOutput{Tendencia: "estavel", NivelPreocupacao: "tranquilo", Resumo: "x"}
	if err := db.UpsertDependentSynthesis(a.ID, 14, out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDependentSynthesis(b.ID, 30, out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	targets, err := db.ListDependentSynthesisTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("esperava 2 alvos, got %d", len(targets))
	}
	byID := map[int64]int{}
	for _, tg := range targets {
		byID[tg.DependentID] = tg.Days
	}
	if byID[a.ID] != 14 || byID[b.ID] != 30 {
		t.Errorf("days nao preservado: %v", byID)
	}
}

// Frescor de calendario: sintese de um dia anterior fica stale mesmo SEM snapshot
// novo (antes congelava para sempre quando os snapshots paravam de avancar).
func TestBuildDependentStatus_CalendarStaleness(t *testing.T) {
	db := setupTestDB(t)
	elder := makeElder(t, db, "Antonia", "222")
	// Snapshot de 3 dias atras: GetLatestSnapshotInferredAt retorna ok, mas a sintese
	// (mais recente que o snapshot) NAO fica stale pelo caminho de snapshot — isola
	// o caminho de calendario.
	threeDaysAgo := time.Now().UTC().Add(-72 * time.Hour)
	if _, err := db.conn.Exec(
		`INSERT INTO psych_state_daily (user_id, snapshot_date, humor_score, confidence, inferred_at)
		 VALUES (?, ?, ?, ?, ?)`,
		elder.ID, threeDaysAgo.Format("2006-01-02"), 4, 3, threeDaysAgo); err != nil {
		t.Fatal(err)
	}
	out := synthesis.ReportOutput{Tendencia: "estavel", NivelPreocupacao: "tranquilo", Resumo: "x"}

	// (a) sintese de ONTEM -> stale por calendario.
	if err := db.UpsertDependentSynthesis(elder.ID, 14, out, time.Now().UTC().Add(-26*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, err := BuildDependentStatus(context.Background(), db, nil, elder, 14)
	if err != nil {
		t.Fatal(err)
	}
	if !st.SynthesisStale {
		t.Error("sintese de ontem deveria ser stale por calendario")
	}

	// (b) sintese de HOJE -> nao stale.
	if err := db.UpsertDependentSynthesis(elder.ID, 14, out, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	st2, err := BuildDependentStatus(context.Background(), db, nil, elder, 14)
	if err != nil {
		t.Fatal(err)
	}
	if st2.SynthesisStale {
		t.Error("sintese de hoje nao deveria ser stale")
	}
}

func TestRegenerateDependentSynthesis_PersistsAndAudits(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "Tudo certo.",
	}}
	if err := RegenerateDependentSynthesis(context.Background(), db, rp, elder, 14); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetDependentSynthesis(elder.ID)
	if err != nil {
		t.Fatalf("expected persisted synthesis, got %v", err)
	}
	if stored.Report.Tendencia != "estavel" {
		t.Errorf("persisted tendencia = %s", stored.Report.Tendencia)
	}
	entries, _ := db.conn.Query(`SELECT action FROM action_log WHERE user_id = ? AND action = 'synthesis_executed'`, elder.ID)
	defer entries.Close()
	if !entries.Next() {
		t.Error("expected synthesis_executed in audit_log")
	}
}

func TestBuildDependentStatus_PopulatesDaysSinceLastTalk(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	db.MarkUserMessageReceived(elder.ID, twoDaysAgo)
	got, _ := db.GetUserByID(elder.ID)

	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
	}}
	rep, err := BuildDependentStatus(context.Background(), db, rp, got, 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DaysSinceLastTalk < 1 || rep.DaysSinceLastTalk > 3 {
		t.Errorf("expected ~2 days, got %d", rep.DaysSinceLastTalk)
	}
}

func TestStatusDependente_AuditsConsult(t *testing.T) {
	db := setupTestDB(t)
	guardian := makeGuardian(t, db, "Caio", "111")
	elder := makeElder(t, db, "Antonia", "222")
	db.LinkFamily(guardian.ID, elder.ID, "filho_de")
	rp := &fakeReportProvider{out: synthesis.ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
	}}
	agent := makeAgentForFamily(db, rp)

	params, _ := json.Marshal(map[string]any{"dependent_id": elder.ID})
	if _, err := handleStatusDependente(context.Background(), agent, guardian, params); err != nil {
		t.Fatal(err)
	}

	rows, _ := db.conn.Query(`SELECT action FROM action_log WHERE user_id = ? AND action = 'status_dependente_consulted'`, guardian.ID)
	defer rows.Close()
	if !rows.Next() {
		t.Error("expected status_dependente_consulted in audit_log for guardian")
	}
}

func TestPickDependentByName(t *testing.T) {
	deps := []FamilyLink{
		{Other: &User{Name: "Antonia da Silva"}},
		{Other: &User{Name: "Joaquim Santos"}},
	}
	if got := pickDependentByName(deps, "antonia"); got == nil || got.Name != "Antonia da Silva" {
		t.Errorf("substring match failed: %+v", got)
	}
	if got := pickDependentByName(deps, "joa"); got == nil || got.Name != "Joaquim Santos" {
		t.Errorf("prefix match failed: %+v", got)
	}
	if got := pickDependentByName(deps, "xyz"); got != nil {
		t.Errorf("expected nil for no match, got %+v", got)
	}
	if got := pickDependentByName(nil, "antonia"); got != nil {
		t.Errorf("expected nil for empty deps, got %+v", got)
	}
}

func TestNormalizePhoneFamily(t *testing.T) {
	cases := map[string]string{
		"+55 (61) 99999-9999": "5561999999999",
		"55 61 99999-9999":    "5561999999999",
		"5561999999999":       "5561999999999",
	}
	for in, want := range cases {
		if got := normalizePhoneFamily(in); got != want {
			t.Errorf("normalize %q: got %q, want %q", in, got, want)
		}
	}
}

func TestToSynthesisAlerts_StripsMessage(t *testing.T) {
	alerts := []FamilyAlert{
		{
			ID: 1, PolicyName: "severe_signal", Severity: "warn",
			Message: "ela esta passando por algo serio", // NUNCA deve sair daqui
		},
	}
	out := toSynthesisAlerts(alerts)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	// O struct synthesis.Alert nem tem campo Message — defesa de design.
	if out[0].PolicyName != "severe_signal" {
		t.Errorf("PolicyName not propagated")
	}
}
