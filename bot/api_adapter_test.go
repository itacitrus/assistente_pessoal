package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// mkAdapter cria um apiAdapter completo + sink pra magic links + audit log.
// Reusa setupTestDB do package; nao precisamos de Sonnet (BuildDependentStatus
// degrada a "indeterminado" quando report=nil).
func mkAdapter(t *testing.T) (*apiAdapter, *DB, *[]string) {
	t.Helper()
	db := setupTestDB(t)
	audit := NewAuditLog(db)
	var sent []string
	send := func(phone, msg string) error {
		sent = append(sent, phone+"::"+msg)
		return nil
	}
	return newAPIAdapter(db, audit, nil /*report*/, nil /*cal*/, "" /*encKey*/, send), db, &sent
}

// =========================================================================
// User mapping
// =========================================================================

func TestAdapter_GetUserByPhone_NotFound(t *testing.T) {
	a, _, _ := mkAdapter(t)
	_, err := a.GetUserByPhone(context.Background(), "55119000000")
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want api.ErrNotFound", err)
	}
}

func TestAdapter_GetUserByID_RoundTrip(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria")
	u, err := a.GetUserByID(context.Background(), users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Maria" {
		t.Fatalf("name = %q", u.Name)
	}
	if u.Type == "" {
		t.Fatal("type vazio — userToAPI nao mapeou")
	}
	// mkUsers gera GoogleCredentials="x", o que faz GoogleConnected=true.
	// Aqui validamos que a propagacao acontece (true porque "x" != "").
	if !u.GoogleConnected {
		t.Fatal("GoogleConnected deveria ser true (refresh token nao vazio)")
	}
}

// =========================================================================
// Sessions adapter
// =========================================================================

func TestAdapter_CreateAndActivateSession(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria")
	sessID, plaintext, err := a.CreatePendingSession(context.Background(), users[0].ID, "1.1.1.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if sessID == 0 || plaintext == "" {
		t.Fatal("sessao mal criada")
	}
	uid, sid, err := a.ActivateSession(context.Background(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if uid != users[0].ID || sid != sessID {
		t.Fatalf("ids mismatch: uid=%d sid=%d", uid, sid)
	}
}

func TestAdapter_ActivateSession_MapsExpiredErr(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria")
	_, plaintext, _ := a.CreatePendingSession(context.Background(), users[0].ID, "", "")
	// Forca expiracao.
	_, err := db.conn.Exec(`UPDATE web_sessions SET expires_at = ?`,
		time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = a.ActivateSession(context.Background(), plaintext)
	if !errors.Is(err, api.ErrSessionExpired) {
		t.Fatalf("err = %v, want api.ErrSessionExpired", err)
	}
}

func TestAdapter_ActivateSession_MapsNotFound(t *testing.T) {
	a, _, _ := mkAdapter(t)
	_, _, err := a.ActivateSession(context.Background(), "no-such-token")
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want api.ErrNotFound", err)
	}
}

func TestAdapter_TouchAndRevoke(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria")
	_, plaintext, _ := a.CreatePendingSession(context.Background(), users[0].ID, "", "")
	_, sid, err := a.ActivateSession(context.Background(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.TouchSession(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeSession(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	// Apos revoke, lookup falha.
	_, _, err = a.GetActiveSessionByToken(context.Background(), plaintext)
	if !errors.Is(err, api.ErrSessionInvalid) {
		t.Fatalf("err = %v, want api.ErrSessionInvalid", err)
	}
}

// =========================================================================
// Rate limit adapter
// =========================================================================

func TestAdapter_RateLimitCounters(t *testing.T) {
	a, _, _ := mkAdapter(t)
	if err := a.RecordLoginAttempt(context.Background(), "5511999999999", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := a.RecordLoginAttempt(context.Background(), "5511999999999", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	n, err := a.CountRecentLoginAttempts(context.Background(), "5511999999999", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	n, err = a.CountRecentLoginAttemptsByIP(context.Background(), "1.1.1.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("ip count = %d, want 2", n)
	}
}

// =========================================================================
// UpdateUserPreferences (delta application)
// =========================================================================

func TestAdapter_UpdateUserPreferences_OnlyUpdatesPresentFields(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria")
	// Pega o estado real persistido (defaults aplicados pelo CreateUser).
	current, err := a.GetUserByID(context.Background(), users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	originalDaily := current.DailySummaryTime
	newName := "Maria Atualizada"
	updated, err := a.UpdateUserPreferences(context.Background(), users[0].ID, api.PreferencesPatch{
		Name: &newName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != newName {
		t.Fatalf("name nao foi atualizado: %q", updated.Name)
	}
	if updated.DailySummaryTime != originalDaily {
		t.Fatalf("daily nao deveria ter mudado: original=%q got=%q", originalDaily, updated.DailySummaryTime)
	}
}

// =========================================================================
// Family CRUD
// =========================================================================

func TestAdapter_CreateDependent_HappyPath(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep, link, err := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name:         "Vovo Maria",
		Phone:        "5511777777777",
		Relationship: "mae",
	})
	if err != nil {
		t.Fatal(err)
	}
	if dep.Type != "idoso" {
		t.Fatalf("type = %q, want idoso", dep.Type)
	}
	if link.GuardianID != users[0].ID || link.DependentID != dep.ID {
		t.Fatalf("link mal montado: %+v", link)
	}
}

func TestAdapter_CreateDependent_PhoneInUse(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao", "Existente")
	// Existente ocupa o phone.
	existingPhone := users[1].PhoneNumber
	_, _, err := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name:         "Vovo Maria",
		Phone:        existingPhone,
		Relationship: "mae",
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Fatalf("err = %v, want api.ErrConflict", err)
	}
}

func TestAdapter_ListDependents(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	_, _, err := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo Maria", Phone: "5511777777777", Relationship: "mae",
	})
	if err != nil {
		t.Fatal(err)
	}
	deps, err := a.ListDependents(context.Background(), users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 {
		t.Fatalf("got %d deps, want 1", len(deps))
	}
	if deps[0].User.Name != "Vovo Maria" {
		t.Fatalf("name = %q", deps[0].User.Name)
	}
	if deps[0].Link.ConsentStatus == "" {
		t.Fatal("consent_status nao preenchido")
	}
}

func TestAdapter_UpdateDependent_RequiresGuardian(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao", "Outro")
	dep, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	newName := "Atualizada"
	_, err := a.UpdateDependent(context.Background(), users[1].ID, dep.ID, api.DependentPatch{
		Name: &newName,
	})
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want api.ErrNotFound (nao guardian)", err)
	}
}

func TestAdapter_UpdateDependent_ChangePhone(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	newPhone := "5511888888888"
	updated, err := a.UpdateDependent(context.Background(), users[0].ID, dep.ID, api.DependentPatch{
		Phone: &newPhone,
	})
	if err != nil {
		t.Fatalf("UpdateDependent phone: %v", err)
	}
	if updated.PhoneNumber != newPhone {
		t.Fatalf("phone = %q, want %q", updated.PhoneNumber, newPhone)
	}
	// O lookup por telefone novo resolve pro dependente; o antigo some.
	if u, gerr := db.GetUserByPhone(newPhone); gerr != nil || u.ID != dep.ID {
		t.Fatalf("GetUserByPhone(novo) = %v, %v", u, gerr)
	}
	if _, gerr := db.GetUserByPhone("5511777777777"); !errors.Is(gerr, ErrUserNotFound) {
		t.Fatalf("telefone antigo ainda resolve: %v", gerr)
	}
}

func TestAdapter_UpdateDependent_PhoneConflict(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep1, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	_, _, _ = a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovô2", Phone: "5511888888888", Relationship: "pai",
	})
	taken := "5511888888888"
	_, err := a.UpdateDependent(context.Background(), users[0].ID, dep1.ID, api.DependentPatch{
		Phone: &taken,
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Fatalf("err = %v, want api.ErrConflict", err)
	}
	// Idempotente: trocar pro proprio numero atual nao deve dar conflito.
	same := "5511777777777"
	if _, err := a.UpdateDependent(context.Background(), users[0].ID, dep1.ID, api.DependentPatch{
		Phone: &same,
	}); err != nil {
		t.Fatalf("mesmo numero deveria ser no-op, got %v", err)
	}
}

func TestAdapter_UpdateNotify_HappyPath(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	_, link, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	off := false
	updated, err := a.UpdateNotifyPrefs(context.Background(), users[0].ID, link.ID, api.NotifyPatch{
		OnInactivity: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Notify.OnInactivity {
		t.Fatal("OnInactivity deveria ter virado false")
	}
}

func TestAdapter_GetFamilyLink_NotFound(t *testing.T) {
	a, _, _ := mkAdapter(t)
	_, err := a.GetFamilyLink(context.Background(), 9999)
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want api.ErrNotFound", err)
	}
}

func TestAdapter_IsGuardianOfAndConsent(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	ok, err := a.IsGuardianOf(context.Background(), users[0].ID, dep.ID)
	if err != nil || !ok {
		t.Fatalf("IsGuardianOf = (%v, %v)", ok, err)
	}
	consent, err := a.GetDependentConsent(context.Background(), users[0].ID, dep.ID)
	if err != nil || consent != ConsentActive {
		t.Fatalf("consent = (%q, %v), want active", consent, err)
	}
}

// =========================================================================
// BuildDependentStatus + GetTimeline
// =========================================================================

func TestAdapter_BuildDependentStatus_Degrades(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	resp, err := a.BuildDependentStatus(context.Background(), users[0].ID, dep.ID, 14)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Days != 14 {
		t.Fatalf("days = %d", resp.Days)
	}
	if resp.Synthesis.Tendencia != "indeterminado" {
		t.Fatalf("sem report client, tendencia = %q, want indeterminado", resp.Synthesis.Tendencia)
	}
}

func TestAdapter_GetTimeline_Empty(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Joao")
	dep, _, _ := a.CreateDependent(context.Background(), users[0].ID, api.CreateDependentRequest{
		Name: "Vovo", Phone: "5511777777777", Relationship: "mae",
	})
	pts, err := a.GetTimeline(context.Background(), dep.ID, 90)
	if err != nil {
		t.Fatal(err)
	}
	if pts == nil {
		// Slice vazio ok, nil tambem ok.
	}
	if len(pts) != 0 {
		t.Fatalf("got %d snapshots, want 0", len(pts))
	}
}

// =========================================================================
// Send magic link
// =========================================================================

func TestAdapter_SendMagicLink_DelegatesToCallback(t *testing.T) {
	a, _, sent := mkAdapter(t)
	if err := a.SendMagicLink(context.Background(), "5511999999999", "msg"); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || !strings.Contains((*sent)[0], "5511999999999::msg") {
		t.Fatalf("send nao delegou: %v", *sent)
	}
}

func TestAdapter_SendMagicLink_NoCallbackErr(t *testing.T) {
	db := setupTestDB(t)
	a := newAPIAdapter(db, NewAuditLog(db), nil, nil, "", nil)
	err := a.SendMagicLink(context.Background(), "x", "y")
	if err == nil {
		t.Fatal("err nil sem callback configurado")
	}
}

// Audit nao panica quando AuditLog eh nil (defensivo).
func TestAdapter_AuditNilSafe(t *testing.T) {
	db := setupTestDB(t)
	a := newAPIAdapter(db, nil, nil, nil, "", nil)
	a.Audit(context.Background(), 1, "noop", "", "")
}

// TestEventTitleFromDetails cobre os dois formatos historicos de
// action_log.details (estruturado "title=...|" e texto cru) alem dos casos em
// que nao da pra inferir titulo.
func TestEventTitleFromDetails(t *testing.T) {
	cases := []struct {
		name    string
		details string
		want    string
	}{
		{"estruturado", "title=Dentista|user_msg=6 de junho|date_source=explicit", "Dentista"},
		{"estruturado_so_title", "title=Reunião BMJ", "Reunião BMJ"},
		{"texto_cru", "Reunião com André", "Reunião com André"},
		{"texto_cru_aniversario", "🎂 Aniversário da Tia Monica (aniversario)", "🎂 Aniversário da Tia Monica (aniversario)"},
		{"vazio", "", ""},
		{"blob_sem_title", "severity=high|category=mood", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eventTitleFromDetails(c.details); got != c.want {
				t.Fatalf("eventTitleFromDetails(%q) = %q, want %q", c.details, got, c.want)
			}
		})
	}
}

// TestEnrichActivityLabel garante que acoes de evento ganham o titulo e acoes
// nao-evento ficam com o label base intacto.
func TestEnrichActivityLabel(t *testing.T) {
	if got := enrichActivityLabel("criar_evento", "title=Dentista|date_source=explicit"); got != "Criou evento: Dentista" {
		t.Fatalf("criar_evento enrich = %q", got)
	}
	if got := enrichActivityLabel("cancelar_evento", "Jantar com Waldyr"); got != "Cancelou evento: Jantar com Waldyr" {
		t.Fatalf("cancelar_evento enrich = %q", got)
	}
	// Acao de evento sem titulo inferivel cai no label base.
	if got := enrichActivityLabel("criar_evento", ""); got != "Criou evento" {
		t.Fatalf("criar_evento sem titulo = %q", got)
	}
	// Acao nao-evento nunca recebe sufixo, mesmo com details preenchido.
	if got := enrichActivityLabel("family_link_created", "Fábio de Freitas"); got != "Cadastrou familiar" {
		t.Fatalf("family_link_created enrich = %q", got)
	}
}

// TestTruncateTitle valida o corte defensivo de titulos longos.
func TestTruncateTitle(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := truncateTitle(long)
	if r := []rune(got); len(r) > 81 { // 80 + reticencia
		t.Fatalf("truncateTitle nao limitou: len=%d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateTitle deveria terminar com reticencia: %q", got)
	}
	if got := truncateTitle("curto"); got != "curto" {
		t.Fatalf("truncateTitle alterou titulo curto: %q", got)
	}
}
