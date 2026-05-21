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
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
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

// calendarReader eh o subset de CalendarClient que o adapter precisa pra
// ler a agenda do proprio usuario. Mantido como interface (nao *CalendarClient
// concreto) pra permitir fake em testes sem OAuth real. *CalendarClient
// satisfaz isto.
type calendarReader interface {
	ListEvents(ctx context.Context, refreshToken, calendarID string, start, end time.Time) ([]CalendarEvent, error)
}

type apiAdapter struct {
	db      *DB
	audit   *AuditLog
	report  llm.ReportProvider
	cal     calendarReader
	encKey  string
	sendMsg func(phone, text string) error
}

// newAPIAdapter constroi o adapter. Mantido pequeno — main.go cria, monta
// e injeta no NewServer. cal + encKey habilitam leitura do Google Calendar
// do proprio usuario (GET /me/agenda, /me/insights).
func newAPIAdapter(db *DB, audit *AuditLog, report llm.ReportProvider, cal calendarReader, encKey string, sendMsg func(phone, text string) error) *apiAdapter {
	return &apiAdapter{
		db:      db,
		audit:   audit,
		report:  report,
		cal:     cal,
		encKey:  encKey,
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
	// Slices nao-nil sempre: Go serializa slice nil como `null`, o que quebra
	// o frontend que espera array. Inicializamos vazios e os loops abaixo
	// fazem append.
	resp := &api.StatusResponse{
		Dependent:         api.DependentRef{ID: dep.ID, Name: dep.Name},
		Days:              report.Days,
		DaysSinceLastTalk: report.DaysSinceLastTalk,
		AlertsOpen:        []api.AlertSummary{},
		Snapshots:         []api.SnapshotPoint{},
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
	if resp.Synthesis.RecomendacoesCarinhosas == nil {
		resp.Synthesis.RecomendacoesCarinhosas = []string{}
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

// =========================================================================
// Me / agenda + insights (Fase 6 web/UI — visao do proprio usuario)
// =========================================================================

// agendaWindowDays eh a janela futura de UpcomingEvents (proximos N dias).
const agendaWindowDays = 14

// agendaUpcomingMax eh o teto de eventos retornados em /me/agenda.upcoming.
const agendaUpcomingMax = 10

// UpcomingEvents le o Google Calendar do proprio usuario (proximos
// agendaWindowDays, ordenado por start asc, no maximo agendaUpcomingMax).
// Se o usuario nao tem Google conectado, retorna lista vazia (nao eh erro —
// o handler ja sinaliza google_connected via user.GoogleConnected).
func (a *apiAdapter) UpcomingEvents(ctx context.Context, userID int64) ([]api.AgendaEvent, error) {
	user, err := a.db.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, api.ErrNotFound
		}
		return nil, err
	}
	if user.GoogleCredentials == "" {
		return []api.AgendaEvent{}, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, a.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt credentials: %w", err)
	}
	now := time.Now()
	end := now.Add(agendaWindowDays * 24 * time.Hour)
	events, err := a.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, now, end)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	out := agendaEventsToAPI(events)
	if len(out) > agendaUpcomingMax {
		out = out[:agendaUpcomingMax]
	}
	return out, nil
}

// agendaEventsToAPI converte CalendarEvent -> api.AgendaEvent. ListEvents ja
// devolve ordenado por startTime asc (OrderBy("startTime")). Detecta all-day
// pela ausencia de hora (meia-noite BRT) — heuristica consistente com como
// parseEventTimes preenche eventos de Date puro.
func agendaEventsToAPI(events []CalendarEvent) []api.AgendaEvent {
	out := make([]api.AgendaEvent, 0, len(events))
	for _, ev := range events {
		item := api.AgendaEvent{
			ID:       ev.ID,
			Title:    ev.Title,
			Start:    ev.Start,
			AllDay:   isAllDayEvent(ev),
			Location: ev.Location,
		}
		if !ev.End.IsZero() {
			endCopy := ev.End
			item.End = &endCopy
		}
		out = append(out, item)
	}
	return out
}

// isAllDayEvent detecta eventos de dia inteiro. Google retorna esses com o
// campo Date (sem hora); parseEventTimes os carrega como meia-noite na BRT.
// Aniversarios (eventType="birthday") tambem sao all-day por construcao.
func isAllDayEvent(ev CalendarEvent) bool {
	if ev.EventType == "birthday" {
		return true
	}
	if ev.Start.IsZero() {
		return false
	}
	local := ev.Start.In(BRT())
	return local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0
}

// RecentActivity le as entradas relevantes (allowlist api.IsRelevantActivity)
// mais recentes do action_log do usuario, ate `limit` itens, com label PT-BR
// amigavel. Delega pra ActivityHistory — mesmo filtro, single source of truth.
func (a *apiAdapter) RecentActivity(ctx context.Context, userID int64, limit int) ([]api.ActivityItem, error) {
	if limit <= 0 {
		limit = 8
	}
	return a.ActivityHistory(ctx, userID, limit)
}

// ActivityHistory le o historico das acoes relevantes (allowlist) do usuario,
// mais recentes primeiro, ate `limit` itens. O filtro de relevancia eh feito
// em SQL (IN (...)) pra nao trazer ruido do banco e respeitar o LIMIT sobre o
// conjunto ja filtrado.
func (a *apiAdapter) ActivityHistory(ctx context.Context, userID int64, limit int) ([]api.ActivityItem, error) {
	if limit <= 0 {
		limit = 50
	}
	placeholders, args := relevantActivitySQLArgs()
	args = append([]any{userID}, args...)
	args = append(args, limit)
	query := `
		SELECT action, details, created_at
		  FROM action_log
		 WHERE user_id = ?
		   AND action IN (` + placeholders + `)
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`
	rows, err := a.db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query activity history: %w", err)
	}
	defer rows.Close()

	out := make([]api.ActivityItem, 0, limit)
	for rows.Next() {
		var action, details string
		var at time.Time
		if err := rows.Scan(&action, &details, &at); err != nil {
			return nil, fmt.Errorf("scan activity history: %w", err)
		}
		out = append(out, api.ActivityItem{
			Action: action,
			Label:  enrichActivityLabel(action, details),
			At:     at.UTC(),
		})
	}
	return out, rows.Err()
}

// enrichActivityLabel monta o label PT-BR e, para acoes de evento, anexa o
// titulo do evento (ex: "Criou evento: Dentista") usando o que ja esta gravado
// em action_log.details. Sem isso, varias linhas viram "Criou evento" generico
// e indistinguivel. O details tem dois formatos historicos:
//   - estruturado (criar_evento): "title=Dentista|user_msg=...|date_source=..."
//   - texto cru (editar/cancelar/gerar_meet/etc): "Reunião com André"
func enrichActivityLabel(action, details string) string {
	base := activityLabelPT(action)
	if !isEventActivity(action) {
		return base
	}
	title := eventTitleFromDetails(details)
	if title == "" {
		return base
	}
	return base + ": " + title
}

// eventActivityActions sao as acoes cujo details carrega um titulo de evento
// que vale a pena exibir junto do label.
var eventActivityActions = map[string]struct{}{
	"criar_evento":          {},
	"editar_evento":         {},
	"cancelar_evento":       {},
	"gerar_meet":            {},
	"convidar_participante": {},
}

func isEventActivity(action string) bool {
	_, ok := eventActivityActions[action]
	return ok
}

// eventTitleFromDetails extrai o titulo do evento do blob de details. Trata o
// formato estruturado ("title=X|...") e o texto cru. Retorna "" quando nao da
// pra inferir um titulo curto e legivel.
func eventTitleFromDetails(details string) string {
	details = strings.TrimSpace(details)
	if details == "" {
		return ""
	}
	if idx := strings.Index(details, "title="); idx >= 0 {
		rest := details[idx+len("title="):]
		if pipe := strings.IndexByte(rest, '|'); pipe >= 0 {
			rest = rest[:pipe]
		}
		return truncateTitle(strings.TrimSpace(rest))
	}
	// Texto cru: so aceitamos se nao parecer um blob estruturado (sem pipes).
	if strings.ContainsRune(details, '|') {
		return ""
	}
	return truncateTitle(details)
}

// truncateTitle limita o titulo exibido a um tamanho razoavel pra nao estourar
// o layout (o front tambem trunca via CSS, isto eh defesa em profundidade).
func truncateTitle(s string) string {
	const max = 80
	if runes := []rune(s); len(runes) > max {
		return strings.TrimSpace(string(runes[:max])) + "…"
	}
	return s
}

// relevantActivitySQLArgs devolve a lista de placeholders "?,?,..." e os args
// correspondentes (as acoes do allowlist), pra montar o IN (...) de forma
// segura (sem string interpolation de valores).
func relevantActivitySQLArgs() (string, []any) {
	actions := api.RelevantActivityActions()
	placeholders := make([]byte, 0, len(actions)*2)
	args := make([]any, 0, len(actions))
	for i, act := range actions {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, act)
	}
	return string(placeholders), args
}

// activityLabelPT mapeia action -> descricao PT-BR amigavel. Reusa
// actionLabelsPT (audit.go); fallback eh a propria action quando nao mapeada.
func activityLabelPT(action string) string {
	if label := actionLabelsPT[action]; label != "" {
		return label
	}
	return action
}

// AgendaInsightsData monta o input do sub-agente de insights: eventos do
// periodo retroativo (`days`) + proximos agendaWindowDays + contagem de
// atividade por tipo de acao no periodo. Tolera ausencia de Google — nesse
// caso so popula activity_counts (HasEnoughData decide se vale gerar).
func (a *apiAdapter) AgendaInsightsData(ctx context.Context, userID int64, days int) (synthesis.AgendaInsightsInput, error) {
	if days <= 0 {
		days = 30
	}
	user, err := a.db.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return synthesis.AgendaInsightsInput{}, api.ErrNotFound
		}
		return synthesis.AgendaInsightsInput{}, err
	}

	in := synthesis.AgendaInsightsInput{
		UserName:        firstName(user.Name),
		PeriodDays:      days,
		GoogleConnected: user.GoogleCredentials != "",
	}

	now := time.Now()
	if in.GoogleConnected {
		refreshToken, derr := Decrypt(user.GoogleCredentials, a.encKey)
		if derr != nil {
			return synthesis.AgendaInsightsInput{}, fmt.Errorf("decrypt credentials: %w", derr)
		}
		pastStart := now.Add(-time.Duration(days) * 24 * time.Hour)
		past, perr := a.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, pastStart, now)
		if perr != nil {
			return synthesis.AgendaInsightsInput{}, fmt.Errorf("list past events: %w", perr)
		}
		future, ferr := a.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, now, now.Add(agendaWindowDays*24*time.Hour))
		if ferr != nil {
			return synthesis.AgendaInsightsInput{}, fmt.Errorf("list future events: %w", ferr)
		}
		in.PastEvents = eventsToLite(past)
		in.UpcomingEvents = eventsToLite(future)
	}

	counts, err := a.activityCounts(ctx, userID, now.Add(-time.Duration(days)*24*time.Hour), now)
	if err != nil {
		return synthesis.AgendaInsightsInput{}, err
	}
	in.ActivityCounts = counts
	return in, nil
}

// firstName (medication.go) extrai o primeiro nome — reusado aqui pra montar
// o user_name do input de insights sem expor nome completo ao modelo.

// eventsToLite converte CalendarEvent -> synthesis.AgendaEventLite (so
// title/start/all_day — sem location/attendees pra reduzir superficie).
func eventsToLite(events []CalendarEvent) []synthesis.AgendaEventLite {
	out := make([]synthesis.AgendaEventLite, 0, len(events))
	for _, ev := range events {
		out = append(out, synthesis.AgendaEventLite{
			Title:  ev.Title,
			Start:  ev.Start,
			AllDay: isAllDayEvent(ev),
		})
	}
	return out
}

// activityCounts agrega action_log do usuario por tipo de acao no intervalo.
func (a *apiAdapter) activityCounts(ctx context.Context, userID int64, from, to time.Time) ([]synthesis.ActivityCount, error) {
	rows, err := a.db.conn.QueryContext(ctx, `
		SELECT action, COUNT(*) AS n
		  FROM action_log
		 WHERE user_id = ? AND created_at BETWEEN ? AND ?
		 GROUP BY action
		 ORDER BY n DESC`, userID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("query activity counts: %w", err)
	}
	defer rows.Close()

	var out []synthesis.ActivityCount
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return nil, fmt.Errorf("scan activity count: %w", err)
		}
		out = append(out, synthesis.ActivityCount{Action: action, Count: n})
	}
	return out, rows.Err()
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

// SendWhatsApp envia uma mensagem WhatsApp generica reusando o mesmo callback
// de envio do magic link (Handler.SendTextToPhone). Mantido separado de
// SendMagicLink na interface para deixar a intencao explicita no call site.
func (a *apiAdapter) SendWhatsApp(ctx context.Context, phone, message string) error {
	if a.sendMsg == nil {
		return errors.New("send whatsapp: sendMsg callback nao configurado")
	}
	return a.sendMsg(phone, message)
}
