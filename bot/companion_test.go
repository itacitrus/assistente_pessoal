package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================================================================
// Helpers comuns aos testes da Fase 4.
// =========================================================================

// mkIdoso cria um usuario tipo idoso pra teste com phone unico.
func mkIdoso(t *testing.T, db *DB, name string, threshold int) *User {
	t.Helper()
	u := &User{
		PhoneNumber:       "5561" + uniqueSuffix(t.Name(), name),
		Name:              name,
		GoogleCalendarID:  "x",
		GoogleCredentials: "x",
	}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create idoso: %v", err)
	}
	if err := db.SetUserType(u.ID, UserTypeIdoso); err != nil {
		t.Fatalf("set type idoso: %v", err)
	}
	if threshold > 0 {
		if _, err := db.conn.Exec(`UPDATE users SET inactivity_threshold_hours = ? WHERE id = ?`,
			threshold, u.ID); err != nil {
			t.Fatalf("set threshold: %v", err)
		}
	}
	// Refresh.
	g, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return g
}

// mkGuardian cria um guardian e linka com dependent. opt = NotifyOnSevereSignal.
func mkGuardian(t *testing.T, db *DB, dependent *User, name string, optInSevere bool) *User {
	t.Helper()
	g := &User{
		PhoneNumber:       "5561" + uniqueSuffix(t.Name(), "g_"+name),
		Name:              name,
		GoogleCalendarID:  "x",
		GoogleCredentials: "x",
	}
	if err := db.CreateUser(g); err != nil {
		t.Fatalf("create guardian: %v", err)
	}
	if _, err := db.LinkFamily(g.ID, dependent.ID, "filha"); err != nil {
		t.Fatalf("link family: %v", err)
	}
	if !optInSevere {
		// Pega link id e ajusta.
		var linkID int64
		if err := db.conn.QueryRow(
			`SELECT id FROM family_links WHERE guardian_id = ? AND dependent_id = ?`,
			g.ID, dependent.ID).Scan(&linkID); err != nil {
			t.Fatalf("get link: %v", err)
		}
		if err := db.UpdateNotifyPreferences(linkID, FamilyNotifyPrefs{
			OnMedicationMiss: true,
			OnInactivity:     true,
			OnSevereSignal:   false,
		}); err != nil {
			t.Fatalf("update prefs: %v", err)
		}
	}
	return g
}

// uniqueSuffix gera 11 digitos a partir do nome do teste — evita colisao
// entre testes que usam SQLite shared.
func uniqueSuffix(testName, name string) string {
	h := uint32(2166136261)
	for _, b := range []byte(testName + ":" + name) {
		h ^= uint32(b)
		h *= 16777619
	}
	out := ""
	for i := 0; i < 11; i++ {
		out += string(rune('0' + (h % 10)))
		h /= 10
		if h == 0 {
			h = uint32(2166136261) ^ uint32(i)
		}
	}
	return out
}

// fakeChat eh um stub de llm.ChatProvider — usado em TestPickChat.
type fakeChat struct {
	name string
}

func (f *fakeChat) Name() string                                                      { return f.name }
func (f *fakeChat) SupportsTools() bool                                                { return true }
func (f *fakeChat) SupportsVision() bool                                               { return false }
func (f *fakeChat) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

// =========================================================================
// 13.1 / 13.2 — Persona switch e nao-vazamento.
// =========================================================================

func TestBuildSystemPromptStable_SwitchByType(t *testing.T) {
	cases := []struct {
		name     string
		userType UserType
		contains string
	}{
		{"idoso", UserTypeIdoso, "amigo Zello"},
		{"comum", UserTypeComum, "REGRA SAGRADA DE DATA IMPLÍCITA"},
		{"responsavel", UserTypeResponsavel, "REGRA SAGRADA DE DATA IMPLÍCITA"},
		{"vazio (legacy)", "", "REGRA SAGRADA DE DATA IMPLÍCITA"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := &User{Name: "Joaquim", Type: c.userType}
			got := buildSystemPromptStable(u)
			if !strings.Contains(got, c.contains) {
				t.Fatalf("expected prompt to contain %q for type=%q, got prefix: %s",
					c.contains, c.userType, got[:min(200, len(got))])
			}
		})
	}
}

func TestCompanionPrompt_NotForOperationalUser(t *testing.T) {
	u := &User{Name: "Giovanni", Type: UserTypeComum}
	got := buildSystemPromptStable(u)
	if strings.Contains(got, "amigo Zello") {
		t.Fatalf("operational user got companion prompt — leaked")
	}
	if strings.Contains(got, "alertar_familia") {
		t.Fatalf("operational user got companion-only tool reference in stable prompt")
	}
}

func TestCompanionPrompt_HasCriticalElements(t *testing.T) {
	u := &User{Name: "Joaquim", Type: UserTypeIdoso}
	got := buildSystemPromptStable(u)
	requires := []string{
		"amigo Zello", "188", "192", "alertar_familia",
		"VALIDAR SEM PRENDER NA TRISTEZA",
		"PROTOCOLO DE RISCO CRÍTICO",
		"social_context",
		"risco:",
		"medico_fisico", "psicologico", "violencia", "negligencia",
	}
	for _, r := range requires {
		if !strings.Contains(got, r) {
			t.Errorf("companion prompt missing required element: %q", r)
		}
	}
	// Confirma que o prompt MENCIONA as girias proibidas (como instrucao
	// "NUNCA use ...") — nao queremos remover mencao, queremos que a regra
	// esteja la pra o modelo respeitar.
	mentioned := []string{"tamo junto", "saca", "valeu"}
	for _, b := range mentioned {
		if !strings.Contains(got, b) {
			t.Errorf("companion prompt should reference forbidden slang in NUNCA-rules: %q", b)
		}
	}
}

// =========================================================================
// 13.7 — Tregua manual respeitada.
// =========================================================================

func TestCheckInactivity_RespectsManualPause(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Joao", 4)

	// Pausa proatividade por 3 dias.
	if err := db.PauseProactive(u.ID, 3); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Setta last_user_message_at = 10h atras (passou threshold de 4h).
	tenAgo := time.Now().Add(-10 * time.Hour).UTC()
	if _, err := db.conn.Exec(
		`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		tenAgo, u.ID,
	); err != nil {
		t.Fatalf("update last: %v", err)
	}

	var sent []string
	sched := &Scheduler{
		db:      db,
		sendMsg: func(p, m string) error { sent = append(sent, m); return nil },
		nowFunc: time.Now,
		// agent nil pra evitar chamada Anthropic real — checkUserInactivity
		// nao chega a chamar RunProactive porque pause aborta antes.
	}
	// Refresh user
	u2, _ := db.GetUserByID(u.ID)
	sched.checkUserInactivity(u2, time.Now())

	if len(sent) != 0 {
		t.Fatalf("expected no message during paused period, got %d", len(sent))
	}
}

// =========================================================================
// 13.5 — Lock 4h sobrevive.
// =========================================================================

func TestCheckInactivity_LockPreventsDouble(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Maria", 4)

	// Insere uma tentativa recente.
	if _, err := db.RecordProactiveAttempt(u.ID, "oi maria"); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Setta idle threshold passado.
	if _, err := db.conn.Exec(
		`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		time.Now().Add(-10*time.Hour).UTC(), u.ID,
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	var sent []string
	sched := &Scheduler{
		db:      db,
		sendMsg: func(p, m string) error { sent = append(sent, m); return nil },
		nowFunc: time.Now,
	}
	u2, _ := db.GetUserByID(u.ID)
	sched.checkUserInactivity(u2, time.Now())

	if len(sent) != 0 {
		t.Fatalf("lock should block second attempt, got %d sent", len(sent))
	}
	// Nenhum proactive_attempt novo deve ter sido criado.
	var count int
	db.conn.QueryRow(`SELECT COUNT(*) FROM proactive_attempts WHERE user_id = ?`, u.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 attempt, got %d", count)
	}
}

// =========================================================================
// 13.11 — last_user_message_at atualizado.
// =========================================================================

func TestMarkUserMessageReceived_UpdatesTimestamp(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Pedro", 4)

	// Inicialmente null.
	got, err := db.GetUserByID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUserMessageAt != nil {
		t.Fatalf("expected nil last_user_message_at initially")
	}

	now := time.Now()
	if err := db.MarkUserMessageReceivedAndProactive(u.ID, now); err != nil {
		t.Fatalf("mark: %v", err)
	}

	got, err = db.GetUserByID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUserMessageAt == nil {
		t.Fatal("expected non-nil last_user_message_at after mark")
	}
	if got.LastUserMessageAt.Sub(now.UTC()).Abs() > time.Second {
		t.Fatalf("expected ~now, got %v", got.LastUserMessageAt)
	}
}

// =========================================================================
// 13.12 — proactive_attempts 'sent' -> 'replied' quando idoso responde.
// =========================================================================

func TestMarkUserMessageReceived_FlipsProactiveToReplied(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Ana", 4)

	attemptID, err := db.RecordProactiveAttempt(u.ID, "oi ana")
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := db.MarkUserMessageReceivedAndProactive(u.ID, time.Now()); err != nil {
		t.Fatalf("mark: %v", err)
	}

	var status string
	var repliedAt *time.Time
	var rep struct {
		valid bool
		t     time.Time
	}
	db.conn.QueryRow(`SELECT status, replied_at FROM proactive_attempts WHERE id = ?`, attemptID).
		Scan(&status, &rep.t)
	_ = repliedAt
	if status != "replied" {
		t.Fatalf("expected status=replied, got %q", status)
	}
}

// =========================================================================
// 13.10 — alertar_familia bloqueia em user.Type != idoso.
// =========================================================================

func TestAlertarFamilia_RejectedForNonElderly(t *testing.T) {
	u := &User{ID: 1, Type: UserTypeComum, Name: "Comum"}
	params, _ := json.Marshal(alertarFamiliaParams{
		Severity: "critical",
		Category: "psicologico",
		Reason:   "x",
	})
	res, err := handleAlertarFamilia(context.Background(), &Agent{}, u, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "so esta disponivel no modo companion") {
		t.Fatalf("expected rejection, got: %s", res)
	}
}

// =========================================================================
// 13.8 — alertar_familia opt-in guardians.
// =========================================================================

func TestAlertarFamilia_OnlyNotifiesOptedInGuardians(t *testing.T) {
	db := setupTestDB(t)
	elder := mkIdoso(t, db, "Joaquim", 24)
	g1 := mkGuardian(t, db, elder, "Marta", true)  // opt-in
	_ = mkGuardian(t, db, elder, "Paulo", false) // opt-out

	var sent []struct{ phone, msg string }
	agent := &Agent{
		db: db,
		sendMsg: func(p, m string) error {
			sent = append(sent, struct{ phone, msg string }{p, m})
			return nil
		},
		audit: NewAuditLog(db),
	}

	params, _ := json.Marshal(alertarFamiliaParams{
		Severity: "critical",
		Category: "psicologico",
		Reason:   "disse que nao vale mais a pena",
	})
	res, err := handleAlertarFamilia(context.Background(), agent, elder, params)
	if err != nil {
		t.Fatal(err)
	}

	if len(sent) != 1 {
		t.Fatalf("expected 1 send (opt-in only), got %d", len(sent))
	}
	if sent[0].phone != g1.PhoneNumber {
		t.Fatalf("notified wrong guardian: %s vs %s", sent[0].phone, g1.PhoneNumber)
	}
	if !strings.Contains(sent[0].msg, "URGENTE") {
		t.Fatalf("critical message missing URGENTE marker: %s", sent[0].msg)
	}
	var parsed AlertarFamiliaResult
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("result must be JSON AlertarFamiliaResult: %v", err)
	}
	if !contains(parsed.SentTo, "Marta") {
		t.Fatalf("result should list sent guardian, got: %v", parsed.SentTo)
	}
	if parsed.DiscloseToElder {
		t.Fatalf("psicologico must have disclose=false")
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}

// =========================================================================
// 13.8.1 — alertar_familia disclosurePolicy por categoria (table-driven).
// CRITICO — fronteira de confianca. Quebrar derruba a feature.
// =========================================================================

func TestAlertarFamilia_DisclosurePolicyByCategory(t *testing.T) {
	cases := []struct {
		name             string
		category         string
		wantDisclose     bool
		wantToneContains string
	}{
		{"medico fisico -> discloses", "medico_fisico", true, "192"},
		{"psicologico -> silence", "psicologico", false, "188"},
		{"violencia -> silence absoluto", "violencia", false, "monitorado"},
		{"negligencia -> silence", "negligencia", false, "vigilância"},
		{"outros -> default discreto", "outros", false, "discrição"},
		{"categoria invalida -> fallback outros", "qualquer_coisa", false, "discrição"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTestDB(t)
			elder := mkIdoso(t, db, "Idoso_"+tc.category, 24)
			mkGuardian(t, db, elder, "Marta", true)
			agent := &Agent{
				db:      db,
				sendMsg: func(p, m string) error { return nil },
				audit:   NewAuditLog(db),
			}
			params, _ := json.Marshal(alertarFamiliaParams{
				Severity: "critical",
				Category: tc.category,
				Reason:   "test",
			})
			res, err := handleAlertarFamilia(context.Background(), agent, elder, params)
			if err != nil {
				t.Fatal(err)
			}
			var parsed AlertarFamiliaResult
			if err := json.Unmarshal([]byte(res), &parsed); err != nil {
				t.Fatalf("result must be JSON: %v (raw=%s)", err, res)
			}
			if parsed.DiscloseToElder != tc.wantDisclose {
				t.Errorf("disclose_to_elder: want %v, got %v",
					tc.wantDisclose, parsed.DiscloseToElder)
			}
			combined := strings.ToLower(parsed.SuggestedTone + " " + parsed.Note)
			if !strings.Contains(combined, strings.ToLower(tc.wantToneContains)) {
				t.Errorf("tone/note should mention %q; got tone=%q note=%q",
					tc.wantToneContains, parsed.SuggestedTone, parsed.Note)
			}
		})
	}
}

// =========================================================================
// 13.8.2 — alertar_familia fallback sem `category`.
// =========================================================================

func TestAlertarFamilia_RequiresCategory(t *testing.T) {
	db := setupTestDB(t)
	elder := mkIdoso(t, db, "SemCategoria", 24)
	mkGuardian(t, db, elder, "Marta", true)
	agent := &Agent{
		db:      db,
		sendMsg: func(p, m string) error { return nil },
		audit:   NewAuditLog(db),
	}
	params, _ := json.Marshal(map[string]any{
		"severity": "critical",
		"reason":   "test",
		// category ausente
	})
	res, _ := handleAlertarFamilia(context.Background(), agent, elder, params)
	var parsed AlertarFamiliaResult
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}
	if parsed.DiscloseToElder {
		t.Fatal("missing category must default to discrete (no disclose)")
	}
}

// =========================================================================
// 13.9 — alertar_familia cooldown (critical=1h).
// =========================================================================

func TestAlertarFamilia_CooldownCriticalOneHour(t *testing.T) {
	db := setupTestDB(t)
	elder := mkIdoso(t, db, "Cooldown", 24)
	mkGuardian(t, db, elder, "Marta", true)
	var sent []string
	agent := &Agent{
		db:      db,
		sendMsg: func(p, m string) error { sent = append(sent, m); return nil },
		audit:   NewAuditLog(db),
	}
	params, _ := json.Marshal(alertarFamiliaParams{
		Severity: "critical",
		Category: "medico_fisico",
		Reason:   "queda",
	})
	if _, err := handleAlertarFamilia(context.Background(), agent, elder, params); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("expected 1 send first time, got %d", len(sent))
	}
	// Segundo dispatch dentro de 1h: sem reenvio.
	res, err := handleAlertarFamilia(context.Background(), agent, elder, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("cooldown should block second send, got %d total", len(sent))
	}
	var parsed AlertarFamiliaResult
	json.Unmarshal([]byte(res), &parsed)
	if !parsed.Cooldown {
		t.Fatal("expected cooldown=true on suppressed result")
	}
}

// =========================================================================
// pausar_proatividade — happy path + range clamp.
// =========================================================================

func TestPausarProatividade_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	elder := mkIdoso(t, db, "PausaTest", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	params, _ := json.Marshal(pausarProatividadeParams{Dias: 3})
	res, err := handlePausarProatividade(context.Background(), agent, elder, params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "3 dia") {
		t.Fatalf("expected confirmation mentioning 3 dias, got %s", res)
	}
	paused, _ := db.IsProactivePaused(elder.ID)
	if !paused {
		t.Fatal("expected user to be paused")
	}
}

func TestPausarProatividade_BlocksNonElderly(t *testing.T) {
	u := &User{ID: 1, Type: UserTypeComum, Name: "Comum"}
	params, _ := json.Marshal(pausarProatividadeParams{Dias: 3})
	res, _ := handlePausarProatividade(context.Background(), &Agent{}, u, params)
	if !strings.Contains(res, "modo companion") {
		t.Fatalf("expected guard rejection, got %s", res)
	}
}

func TestPausarProatividade_ClampsRange(t *testing.T) {
	db := setupTestDB(t)
	elder := mkIdoso(t, db, "Clamp", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	// Dias=0 -> clamp pra 1.
	params, _ := json.Marshal(pausarProatividadeParams{Dias: 0})
	if _, err := handlePausarProatividade(context.Background(), agent, elder, params); err != nil {
		t.Fatal(err)
	}
	paused, _ := db.IsProactivePaused(elder.ID)
	if !paused {
		t.Fatal("expected pause active even with Dias=0")
	}
}

// =========================================================================
// 13.15 — Provider switching: pickChat.
// =========================================================================

func TestPickChat_RoutesIdosoToCompanionProvider(t *testing.T) {
	fakeOp := &fakeChat{name: "anthropic"}
	fakeCompanion := &fakeChat{name: "deepseek"}
	a := &Agent{chat: fakeOp, companionChat: fakeCompanion}

	cases := []struct {
		userType UserType
		want     string
	}{
		{UserTypeIdoso, "deepseek"},
		{UserTypeComum, "anthropic"},
		{UserTypeResponsavel, "anthropic"},
		{"", "anthropic"},
	}
	for _, c := range cases {
		t.Run(string(c.userType), func(t *testing.T) {
			u := &User{Type: c.userType}
			got := a.pickChat(u)
			if got == nil {
				t.Fatal("pickChat returned nil")
			}
			if got.Name() != c.want {
				t.Fatalf("type=%q: want %s got %s", c.userType, c.want, got.Name())
			}
		})
	}
}

func TestPickChat_FallbackToOpWhenCompanionNil(t *testing.T) {
	a := &Agent{chat: &fakeChat{name: "anthropic"}, companionChat: nil}
	u := &User{Type: UserTypeIdoso}
	if a.pickChat(u).Name() != "anthropic" {
		t.Fatalf("nil companion should fall back to op chat")
	}
}

func TestPickChat_BothNilReturnsNil(t *testing.T) {
	a := &Agent{chat: nil, companionChat: nil}
	u := &User{Type: UserTypeIdoso}
	got := a.pickChat(u)
	if got != nil {
		t.Fatalf("both nil should return nil chat (legacy SDK path)")
	}
}

// =========================================================================
// 13.18 — Link allowlist (table-driven).
// =========================================================================

func TestLinkAllowlist_MatchHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		// Exatos
		{"globo.com", true},
		{"g1.globo.com", true},
		{"folha.uol.com.br", true},
		{"youtube.com", true},
		{"youtu.be", true},
		{"x.com", true},

		// Subdominios diretos -> permitido
		{"blog.globo.com", true},
		{"www.globo.com", true},
		{"m.facebook.com", true},

		// Negativos
		{"globo.com.br.evil.tk", false},
		{"random-blog.tk", false},
		{"evil.com", false},
		{"localhost", false},
		{"127.0.0.1", false},
		{"169.254.169.254", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			if got := llm.MatchHost(c.host); got != c.want {
				t.Fatalf("MatchHost(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestComentarLink_RejectsUnknownHost(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "LinkTest", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	params, _ := json.Marshal(comentarLinkParams{URL: "https://random-blog.tk/post"})
	res, _ := handleComentarLink(context.Background(), agent, u, params)
	if !strings.Contains(res, "não consigo abrir") {
		t.Fatalf("expected friendly rejection, got: %s", res)
	}
}

func TestComentarLink_BlocksLocalhost(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "SSRFTest", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	params, _ := json.Marshal(comentarLinkParams{URL: "http://localhost:6379/"})
	res, _ := handleComentarLink(context.Background(), agent, u, params)
	if !strings.Contains(res, "não consigo abrir") {
		t.Fatalf("SSRF attempt should be blocked, got: %s", res)
	}
}

func TestComentarLink_InvalidURL(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "InvUrl", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	params, _ := json.Marshal(comentarLinkParams{URL: "not-a-url"})
	res, _ := handleComentarLink(context.Background(), agent, u, params)
	if !strings.Contains(res, "URL invalida") {
		t.Fatalf("expected URL invalida msg, got %s", res)
	}
}

// =========================================================================
// disclosurePolicy completeness — todas as 5 categorias mapeadas.
// =========================================================================

func TestDisclosurePolicy_AllFiveCategoriesMapped(t *testing.T) {
	required := []string{"medico_fisico", "psicologico", "violencia", "negligencia", "outros"}
	for _, c := range required {
		if _, ok := disclosurePolicy[c]; !ok {
			t.Errorf("disclosurePolicy missing category: %q", c)
		}
	}
}

func TestDisclosurePolicy_OnlyMedicoFisicoDiscloses(t *testing.T) {
	// REGRA DURA: so medico_fisico tem Disclose=true. Mudar isso quebra
	// a feature inteira — fronteira de confianca.
	for k, v := range disclosurePolicy {
		if k == "medico_fisico" {
			if !v.Disclose {
				t.Errorf("medico_fisico must have Disclose=true")
			}
		} else {
			if v.Disclose {
				t.Errorf("category %q must have Disclose=false (psychological/violence/etc), got true", k)
			}
		}
	}
}

// =========================================================================
// SnapshotWriter no-op default.
// =========================================================================

func TestNoopSnapshotWriter_NeverErrors(t *testing.T) {
	w := noopSnapshotWriter{}
	if err := w.MaybeUpdateSnapshot(context.Background(), 1); err != nil {
		t.Fatalf("noop should never error: %v", err)
	}
}

// =========================================================================
// proactiveWindowAllowed — janela horaria 8h-21h.
// =========================================================================

func TestProactiveWindowAllowed(t *testing.T) {
	loc := BRT()
	cases := []struct {
		hour int
		want bool
	}{
		{0, false}, {1, false}, {7, false}, {8, true},
		{12, true}, {20, true}, {21, false}, {23, false},
	}
	for _, c := range cases {
		ts := time.Date(2026, 5, 9, c.hour, 0, 0, 0, loc)
		if got := proactiveWindowAllowed(ts, loc); got != c.want {
			t.Errorf("hour=%d: want %v got %v", c.hour, c.want, got)
		}
	}
}

// =========================================================================
// IsSignificantConversation — heuristica handler.
// =========================================================================

func TestIsSignificantConversation(t *testing.T) {
	t0 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		userTurns   int
		first, last time.Time
		alerts      int
		want        bool
	}{
		{"5 turnos", 5, t0, t0, 0, true},
		{"4 turnos sem duracao", 4, t0, t0, 0, false},
		{"2 turnos com 4min duracao", 2, t0, t0.Add(4 * time.Minute), 0, true},
		{"alertar_familia hoje", 1, t0, t0, 1, true},
		{"1 turno simples", 1, t0, t0, 0, false},
		{"2 turnos com 1min duracao", 2, t0, t0.Add(time.Minute), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isSignificantConversation(c.userTurns, c.first, c.last, c.alerts)
			if got != c.want {
				t.Errorf("want %v got %v", c.want, got)
			}
		})
	}
}

// =========================================================================
// Stub documentado dos prompt-evals da §13.14.
// Roda online (custa tokens). Marca skip por padrao.
// =========================================================================

func TestPromptEval_TomValidaEAbrePorta(t *testing.T) {
	t.Skip("prompt-eval stub — requires DEEPSEEK_API_KEY/ANTHROPIC_API_KEY + budget. " +
		"Roda manualmente conforme §13.14.1 do plano (eval por release, ~$0.05).")
}

func TestPromptEval_EventoFuturoVaiParaAgenda(t *testing.T) {
	t.Skip("prompt-eval stub — §13.14.2. Verifica que criar_evento eh chamada e " +
		"salvar_memoria(evento:*) nao redunda data/hora.")
}

// =========================================================================
// min helper (Go 1.21+ tem builtin, mas pra evitar conflitos).
// =========================================================================

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
