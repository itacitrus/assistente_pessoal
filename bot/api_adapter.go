package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================================================================
// Fase 2 (web/UI) — adapter Store -> *DB + AuditLog + WhatsApp send
// =========================================================================
//
// O api package eh isolado e nao conhece o tipo *DB. Este adapter implementa
// api.Store delegando pra *DB e pros callbacks de audit e envio. Toda a
// traducao de erros internos -> sentinels publicas (api.ErrNotFound etc)
// vive aqui.
//
// Reuso: BuildDependentStatus eh a mesma funcao usada pelo chat (tools_family.go).
// O adapter chama-a, depois converte DependentStatusReport -> api.StatusResponse.

type apiAdapter struct {
	db         *DB
	audit      *AuditLog
	report     llm.ReportProvider
	sendMsg    func(phone, text string) error
}

// newAPIAdapter constroi o adapter. Mantido pequeno — main.go cria, monta
// e injeta no NewServer.
func newAPIAdapter(db *DB, audit *AuditLog, report llm.ReportProvider, sendMsg func(phone, text string) error) *apiAdapter {
	return &apiAdapter{
		db:      db,
		audit:   audit,
		report:  report,
		sendMsg: sendMsg,
	}
}

// userToAPI converte *User do main pra api.User. GoogleConnected eh derivado
// — frontend nao precisa do refresh token cifrado.
func userToAPI(u *User) *api.User {
	if u == nil {
		return nil
	}
	return &api.User{
		ID:                       u.ID,
		PhoneNumber:              u.PhoneNumber,
		Name:                     u.Name,
		Type:                     string(u.Type),
		DailySummaryTime:         u.DailySummaryTime,
		WeeklySummaryDay:         u.WeeklySummaryDay,
		WeeklySummaryTime:        u.WeeklySummaryTime,
		ReminderBefore:           u.ReminderBefore,
		AutoConfirmTimeout:       u.AutoConfirmTimeout,
		InactivityThresholdHours: u.InactivityThresholdHours,
		GoogleConnected:          u.GoogleCredentials != "",
		IsActive:                 u.IsActive,
		CreatedAt:                u.CreatedAt,
	}
}

// linkToAPI converte FamilyLink do main pra api.FamilyLink. Hidrata
// consent_status via lookup adicional — caller que ja tem o consent pode
// preencher via linkToAPIWithConsent.
func linkToAPI(fl *FamilyLink) *api.FamilyLink {
	if fl == nil {
		return nil
	}
	return &api.FamilyLink{
		ID:           fl.ID,
		GuardianID:   fl.GuardianID,
		DependentID:  fl.DependentID,
		Relationship: fl.Relationship,
		Notify: api.Notify{
			OnMedicationMiss: fl.Notify.OnMedicationMiss,
			OnInactivity:     fl.Notify.OnInactivity,
			OnSevereSignal:   fl.Notify.OnSevereSignal,
		},
		ConsentStatus: ConsentActive, // default — caller pode sobrescrever
		CreatedAt:     fl.CreatedAt,
	}
}

// --- api.Store impl ---

func (a *apiAdapter) GetUserByPhone(ctx context.Context, phone string) (*api.User, error) {
	u, err := a.db.GetUserByPhone(phone)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, api.ErrNotFound
		}
		return nil, err
	}
	return userToAPI(u), nil
}

func (a *apiAdapter) GetUserByID(ctx context.Context, id int64) (*api.User, error) {
	u, err := a.db.GetUserByID(id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, api.ErrNotFound
		}
		return nil, err
	}
	return userToAPI(u), nil
}

func (a *apiAdapter) CreatePendingSession(ctx context.Context, userID int64, ip, userAgent string) (int64, string, error) {
	sess, plaintext, err := a.db.CreatePendingSession(userID, ip, userAgent)
	if err != nil {
		return 0, "", err
	}
	return sess.ID, plaintext, nil
}

func (a *apiAdapter) ActivateSession(ctx context.Context, plaintext string) (int64, int64, error) {
	sess, err := a.db.ActivateSession(plaintext)
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return 0, 0, api.ErrNotFound
		case errors.Is(err, ErrSessionExpired):
			return 0, 0, api.ErrSessionExpired
		case errors.Is(err, ErrSessionInvalid):
			return 0, 0, api.ErrSessionInvalid
		default:
			return 0, 0, err
		}
	}
	return sess.UserID, sess.ID, nil
}

func (a *apiAdapter) GetActiveSessionByToken(ctx context.Context, plaintext string) (int64, int64, error) {
	sess, err := a.db.GetActiveSessionByToken(plaintext)
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionNotFound):
			return 0, 0, api.ErrNotFound
		case errors.Is(err, ErrSessionExpired):
			return 0, 0, api.ErrSessionExpired
		case errors.Is(err, ErrSessionInvalid):
			return 0, 0, api.ErrSessionInvalid
		default:
			return 0, 0, err
		}
	}
	return sess.ID, sess.UserID, nil
}

func (a *apiAdapter) TouchSession(ctx context.Context, sessionID int64) error {
	if err := a.db.TouchSession(sessionID); err != nil {
		log.Printf("api adapter: touch session %d failed: %v", sessionID, err)
		return err
	}
	return nil
}

func (a *apiAdapter) RevokeSession(ctx context.Context, sessionID int64) error {
	return a.db.RevokeSession(sessionID)
}

func (a *apiAdapter) CountRecentLoginAttempts(ctx context.Context, phone string, window time.Duration) (int, error) {
	return a.db.CountRecentLoginAttempts(phone, window)
}

func (a *apiAdapter) CountRecentLoginAttemptsByIP(ctx context.Context, ip string, window time.Duration) (int, error) {
	return a.db.CountRecentLoginAttemptsByIP(ip, window)
}

func (a *apiAdapter) RecordLoginAttempt(ctx context.Context, phone, ip string) error {
	return a.db.RecordLoginAttempt(phone, ip)
}

// UpdateUserPreferences atualiza apenas os campos presentes no patch (pointer
// non-nil). Audit log eh feito pelo handler — adapter nao decide auditar.
func (a *apiAdapter) UpdateUserPreferences(ctx context.Context, userID int64, p api.PreferencesPatch) (*api.User, error) {
	current, err := a.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	// Compoe campos novos.
	name := current.Name
	if p.Name != nil {
		name = strings.TrimSpace(*p.Name)
	}
	dst := current.DailySummaryTime
	if p.DailySummaryTime != nil {
		dst = *p.DailySummaryTime
	}
	wsd := current.WeeklySummaryDay
	if p.WeeklySummaryDay != nil {
		wsd = strings.ToLower(*p.WeeklySummaryDay)
	}
	wst := current.WeeklySummaryTime
	if p.WeeklySummaryTime != nil {
		wst = *p.WeeklySummaryTime
	}
	rb := current.ReminderBefore
	if p.ReminderBefore != nil {
		rb = *p.ReminderBefore
	}
	act := current.AutoConfirmTimeout
	if p.AutoConfirmTimeout != nil {
		act = *p.AutoConfirmTimeout
	}
	thresh := current.InactivityThresholdHours
	if p.InactivityThresholdHours != nil {
		thresh = *p.InactivityThresholdHours
	}
	_, err = a.db.conn.Exec(`
		UPDATE users
		   SET name = ?,
		       daily_summary_time = ?,
		       weekly_summary_day = ?,
		       weekly_summary_time = ?,
		       reminder_before = ?,
		       auto_confirm_timeout = ?,
		       inactivity_threshold_hours = ?
		 WHERE id = ?`,
		name, dst, wsd, wst, rb, act, thresh, userID)
	if err != nil {
		return nil, fmt.Errorf("update user preferences: %w", err)
	}
	updated, err := a.db.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	return userToAPI(updated), nil
}

// CreateDependent cria User tipo idoso (se phone novo) + family_link.
// Reusa LinkFamily — single source of truth pra constraints.
func (a *apiAdapter) CreateDependent(ctx context.Context, guardianID int64, req api.CreateDependentRequest) (*api.User, *api.FamilyLink, error) {
	existing, err := a.db.GetUserByPhone(req.Phone)
	switch {
	case err == nil:
		// Phone ja cadastrado — politica desta fase: nao reusamos. UI deve
		// orientar a vincular caso queira (futuro).
		return nil, nil, fmt.Errorf("%w: %s", api.ErrConflict, existing.PhoneNumber)
	case errors.Is(err, ErrUserNotFound):
		// Esperado — vamos criar.
	default:
		return nil, nil, err
	}

	dep := &User{
		PhoneNumber: req.Phone,
		Name:        strings.TrimSpace(req.Name),
		Type:        UserTypeIdoso,
	}
	if err := a.db.CreateUser(dep); err != nil {
		return nil, nil, fmt.Errorf("create dependent user: %w", err)
	}
	if err := a.db.SetUserType(dep.ID, UserTypeIdoso); err != nil {
		return nil, nil, fmt.Errorf("set type idoso: %w", err)
	}
	dep.Type = UserTypeIdoso

	link, err := a.db.LinkFamily(guardianID, dep.ID, strings.TrimSpace(req.Relationship))
	if err != nil {
		return nil, nil, fmt.Errorf("link family: %w", err)
	}

	if a.audit != nil {
		_ = a.audit.LogFamilyLinkCreated(guardianID, guardianID, dep.ID, req.Relationship)
	}

	return userToAPI(dep), linkToAPI(link), nil
}

// ListDependents agrega Get + consent.
func (a *apiAdapter) ListDependents(ctx context.Context, guardianID int64) ([]api.DependentSummary, error) {
	deps, err := a.db.GetDependents(guardianID)
	if err != nil {
		return nil, err
	}
	out := make([]api.DependentSummary, 0, len(deps))
	for _, fl := range deps {
		if fl.Other == nil {
			continue
		}
		consent, _ := a.db.GetDependentConsent(fl.GuardianID, fl.DependentID)
		l := linkToAPI(&fl)
		l.ConsentStatus = consent
		out = append(out, api.DependentSummary{
			User: *userToAPI(fl.Other),
			Link: *l,
		})
	}
	return out, nil
}

// UpdateDependent eh delegado pra UpdateUserPreferences depois de validar
// guardia. Subset de campos editaveis fica garantido pelo struct DependentPatch.
func (a *apiAdapter) UpdateDependent(ctx context.Context, guardianID, dependentID int64, p api.DependentPatch) (*api.User, error) {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrNotFound
	}
	patch := api.PreferencesPatch{
		Name:                     p.Name,
		DailySummaryTime:         p.DailySummaryTime,
		WeeklySummaryDay:         p.WeeklySummaryDay,
		WeeklySummaryTime:        p.WeeklySummaryTime,
		ReminderBefore:           p.ReminderBefore,
		InactivityThresholdHours: p.InactivityThresholdHours,
	}
	return a.UpdateUserPreferences(ctx, dependentID, patch)
}

func (a *apiAdapter) UpdateNotifyPrefs(ctx context.Context, guardianID, linkID int64, p api.NotifyPatch) (*api.FamilyLink, error) {
	current, err := a.getFamilyLinkRaw(linkID)
	if err != nil {
		return nil, err
	}
	if current.GuardianID != guardianID {
		return nil, api.ErrNotFound
	}
	before := current.Notify
	prefs := current.Notify
	if p.OnMedicationMiss != nil {
		prefs.OnMedicationMiss = *p.OnMedicationMiss
	}
	if p.OnInactivity != nil {
		prefs.OnInactivity = *p.OnInactivity
	}
	if p.OnSevereSignal != nil {
		prefs.OnSevereSignal = *p.OnSevereSignal
	}
	if err := a.db.UpdateNotifyPreferences(linkID, prefs); err != nil {
		return nil, err
	}
	if a.audit != nil {
		_ = a.audit.LogFamilyNotifyPrefsUpdated(guardianID, linkID, before, prefs)
	}
	current.Notify = prefs
	out := linkToAPI(current)
	consent, _ := a.db.GetDependentConsent(current.GuardianID, current.DependentID)
	out.ConsentStatus = consent
	return out, nil
}

func (a *apiAdapter) GetFamilyLink(ctx context.Context, linkID int64) (*api.FamilyLink, error) {
	fl, err := a.getFamilyLinkRaw(linkID)
	if err != nil {
		return nil, err
	}
	out := linkToAPI(fl)
	consent, _ := a.db.GetDependentConsent(fl.GuardianID, fl.DependentID)
	out.ConsentStatus = consent
	return out, nil
}

// getFamilyLinkRaw faz query direta — reuso comum pelos handlers.
func (a *apiAdapter) getFamilyLinkRaw(linkID int64) (*FamilyLink, error) {
	fl := &FamilyLink{}
	err := a.db.conn.QueryRow(`
		SELECT id, guardian_id, dependent_id, relationship,
		       notify_on_medication_miss, notify_on_inactivity, notify_on_severe_signal,
		       created_at
		FROM family_links WHERE id = ?`,
		linkID,
	).Scan(
		&fl.ID, &fl.GuardianID, &fl.DependentID, &fl.Relationship,
		&fl.Notify.OnMedicationMiss, &fl.Notify.OnInactivity, &fl.Notify.OnSevereSignal,
		&fl.CreatedAt,
	)
	if err != nil {
		// sql.ErrNoRows -> not found.
		if strings.Contains(err.Error(), "no rows") {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("get family link raw: %w", err)
	}
	return fl, nil
}

func (a *apiAdapter) IsGuardianOf(ctx context.Context, guardianID, dependentID int64) (bool, error) {
	return a.db.IsGuardianOf(guardianID, dependentID)
}

func (a *apiAdapter) GetDependentConsent(ctx context.Context, guardianID, dependentID int64) (string, error) {
	return a.db.GetDependentConsent(guardianID, dependentID)
}

// BuildDependentStatus delega pra mesma BuildDependentStatus que o chat usa,
// depois converte pro shape api.StatusResponse.
func (a *apiAdapter) BuildDependentStatus(ctx context.Context, guardianID, dependentID int64, days int) (*api.StatusResponse, error) {
	dep, err := a.db.GetUserByID(dependentID)
	if err != nil {
		return nil, err
	}
	report, err := BuildDependentStatus(ctx, a.db, a.report, dep, days)
	if err != nil {
		return nil, err
	}
	resp := &api.StatusResponse{
		Dependent:         api.DependentRef{ID: dep.ID, Name: dep.Name},
		Days:              report.Days,
		DaysSinceLastTalk: report.DaysSinceLastTalk,
		Medication: api.MedicationStats{
			Scheduled:     report.Medication.Scheduled,
			Taken:         report.Medication.Taken,
			Missed:        report.Medication.Missed,
			Skipped:       report.Medication.Skipped,
			Pending:       report.Medication.Pending,
			AdherenceFrac: report.Medication.AdherenceFrac,
		},
		ProactiveAttempts: api.ProactiveStats{
			Last7d:    report.ProactiveAttempts.Last7d,
			LastAcked: report.ProactiveAttempts.LastAcked,
		},
		Synthesis: api.SynthesisSummary{
			Tendencia:               report.Synthesis.Tendencia,
			Resumo:                  report.Synthesis.Resumo,
			NivelPreocupacao:        report.Synthesis.NivelPreocupacao,
			Comparacao:              report.Synthesis.Comparacao,
			PontoDeAtencao:          report.Synthesis.PontoDeAtencao,
			RecomendacoesCarinhosas: report.Synthesis.RecomendacoesCarinhosas,
		},
	}
	if report.LastUserMessageAt.Valid {
		t := report.LastUserMessageAt.Time
		resp.LastUserMessageAt = &t
	}
	if report.ProactiveAttempts.LastAttemptAt.Valid {
		t := report.ProactiveAttempts.LastAttemptAt.Time
		resp.ProactiveAttempts.LastAttemptAt = &t
	}
	for _, alert := range report.AlertsOpen {
		resp.AlertsOpen = append(resp.AlertsOpen, api.AlertSummary{
			ID:         alert.ID,
			PolicyName: alert.PolicyName,
			Severity:   alert.Severity,
			Status:     alert.Status,
			CreatedAt:  alert.CreatedAt,
		})
	}
	for _, snap := range report.Snapshots {
		resp.Snapshots = append(resp.Snapshots, api.SnapshotPoint{
			Date:          snap.SnapshotDate.Format("2006-01-02"),
			Humor:         snap.HumorScore,
			Energia:       snap.EnergiaScore,
			Sociabilidade: snap.SociabilidadeScore,
			Autocuidado:   snap.AutocuidadoScore,
			Confidence:    snap.Confidence,
		})
	}
	return resp, nil
}

// GetTimeline retorna pontos da timeline. Inclui confidence < 3 — UI decide
// como renderizar.
func (a *apiAdapter) GetTimeline(ctx context.Context, dependentID int64, days int) ([]api.SnapshotPoint, error) {
	if days <= 0 {
		days = 90
	}
	now := time.Now().UTC()
	from := now.Add(-time.Duration(days) * 24 * time.Hour)
	snaps, err := a.db.GetSnapshotsForUserDateRange(dependentID, from, now)
	if err != nil {
		return nil, err
	}
	out := make([]api.SnapshotPoint, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, api.SnapshotPoint{
			Date:          s.SnapshotDate.Format("2006-01-02"),
			Humor:         s.HumorScore,
			Energia:       s.EnergiaScore,
			Sociabilidade: s.SociabilidadeScore,
			Autocuidado:   s.AutocuidadoScore,
			Confidence:    s.Confidence,
		})
	}
	return out, nil
}

// Audit eh thin wrapper — preserva contract de "fire and forget" (api package
// nao se importa com falha de log; nos logamos no warn level).
func (a *apiAdapter) Audit(ctx context.Context, userID int64, action, target, details string) {
	if a.audit == nil {
		return
	}
	if err := a.audit.Log(userID, action, target, details); err != nil {
		log.Printf("api adapter audit %s: %v", action, err)
	}
}

// SendMagicLink delega pro Handler.SendTextToPhone via callback.
func (a *apiAdapter) SendMagicLink(ctx context.Context, phone, message string) error {
	if a.sendMsg == nil {
		return errors.New("send magic link: sendMsg callback nao configurado")
	}
	return a.sendMsg(phone, message)
}
