package main

import (
	"fmt"
	"strings"
	"time"
)

type AuditEntry struct {
	ID         int64
	UserID     int64
	Action     string
	TargetUser string
	Details    string
	CreatedAt  time.Time
}

type AuditLog struct {
	db *DB
}

func NewAuditLog(db *DB) *AuditLog {
	return &AuditLog{db: db}
}

func (a *AuditLog) Log(userID int64, action, targetUser, details string) error {
	_, err := a.db.conn.Exec(
		`INSERT INTO action_log (user_id, action, target_user, details) VALUES (?, ?, ?, ?)`,
		userID, action, targetUser, details)
	return err
}

func (a *AuditLog) Query(userID int64, start, end time.Time) ([]AuditEntry, error) {
	rows, err := a.db.conn.Query(
		`SELECT id, user_id, action, COALESCE(target_user, ''), details, created_at
		 FROM action_log
		 WHERE user_id = ? AND created_at BETWEEN ? AND ?
		 ORDER BY created_at ASC`,
		userID, start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.UserID, &e.Action, &e.TargetUser, &e.Details, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

var actionLabelsPT = map[string]string{
	"criar_evento":                "Criou evento",
	"editar_evento":               "Editou evento",
	"cancelar_evento":             "Cancelou evento",
	"gerar_meet":                  "Gerou link do Meet",
	"convidar_participante":       "Convidou participante",
	"consultar_agenda":            "Consultou agenda",
	"confirmar":                   "Confirmou",
	"negar":                       "Negou",
	"auto_confirm":                "Auto-confirmou",
	"grant_access":                "Concedeu acesso",
	"grant_access_once":           "Concedeu acesso (pontual)",
	"revoke_access":               "Revogou acesso",
	"deny_access":                 "Negou acesso",
	"permission_request":          "Solicitou acesso",
	"consultar_log":               "Consultou histórico",
	"family_link_created":         "Cadastrou familiar",
	"family_link_removed":         "Removeu familiar",
	"family_notify_prefs_updated": "Atualizou alertas de familiar",
	"user_type_changed":           "Mudou tipo de usuário",
	// Fase 3 (idosos): medicacao + escalacao.
	"medication_created":           "Cadastrou medicamento",
	"medication_edited":            "Editou medicamento",
	"medication_canceled":          "Cancelou medicamento",
	"medication_taken":             "Tomou medicamento",
	"medication_skipped":           "Pulou dose",
	"medication_missed":            "Dose não tomada",
	"medication_escalated":         "Escalou medicamento pra família",
	"medication_reminder_sent":     "Lembrete de medicamento enviado",
	"prescription_image_processed": "Processou foto de receita",
	// Fase 4 (idosos): companion + proatividade.
	"alertar_familia":           "Alertou família (sinal sério)",
	"pausar_proatividade":       "Pausou proatividade",
	"proactive_attempt_sent":    "Tentou puxar conversa",
	"companion_provider_switch": "Trocou provider do companion",
	"comentar_imagem":           "Comentou imagem recebida",
	"comentar_link":             "Comentou link recebido",
	"comentar_link_rejected":    "Rejeitou link fora da allowlist",
	"comentar_link_error":       "Erro ao buscar Open Graph",
	"snapshot_updated":          "Snapshot psicológico atualizado",
	"safety_net_fired":          "Safety net disparou alerta",
	// Fase 5 (idosos): relatorio longitudinal + alertas + snapshots.
	"status_dependente_consulted":     "Consultou status do dependente",
	"timeline_consulted":              "Consultou timeline do dependente",
	"synthesis_executed":              "Síntese gerada",
	"synthesis_failed":                "Falha na síntese",
	"psych_snapshot_written":          "Snapshot psicológico escrito",
	"psych_snapshot_failed":           "Falha ao escrever snapshot",
	"safety_alert_from_writer":        "Alerta de segurança disparado pelo writer",
	"inactivity_escalation_triggered": "Escalou inatividade para família",
	// Fase 2 (web/UI): autenticacao via magic link e gestao de preferencias.
	"web_login_requested":      "Solicitou link de acesso ao painel",
	"web_login_succeeded":      "Entrou no painel",
	"web_login_failed":         "Falha em login no painel",
	"web_session_revoked":      "Saiu do painel",
	"user_preferences_updated": "Atualizou preferências",
	"me_insights_generated":    "Gerou insights da agenda",
}

// LogAlertarFamilia registra a chamada da tool alertar_familia.
// userID = idoso (sujeito do alerta).
// sentTo / failedFor = nomes dos guardians notificados / falhos.
// suppressed = true quando o cooldown bloqueou o reenvio.
func (a *AuditLog) LogAlertarFamilia(userID int64, severity, category, reason string, sentTo, failedFor []string, suppressed bool) error {
	details := fmt.Sprintf(
		"severity=%s|category=%s|reason=%s|sent_to=%s|failed_for=%s|suppressed=%t",
		severity, category, sanitizeAuditReason(reason),
		strings.Join(sentTo, ","), strings.Join(failedFor, ","),
		suppressed,
	)
	target := strings.Join(sentTo, ",")
	return a.Log(userID, "alertar_familia", target, details)
}

// LogProactiveAttemptSent registra que Lurch puxou conversa por inatividade.
func (a *AuditLog) LogProactiveAttemptSent(userID int64, hoursIdle int, attemptID int64, message string) error {
	details := fmt.Sprintf(
		"hours_idle=%d|attempt_id=%d|message=%s",
		hoursIdle, attemptID, sanitizeAuditReason(message),
	)
	return a.Log(userID, "proactive_attempt_sent", "", details)
}

// LogCompanionProviderSwitch registra qual provider foi escolhido para um
// turno do companion. Util pra observar shadow mode ou canary (Fase 4 §4.5.11).
func (a *AuditLog) LogCompanionProviderSwitch(userID int64, providerName string) error {
	return a.Log(userID, "companion_provider_switch", providerName, "")
}

// sanitizeAuditReason normaliza um valor de campo livre pra encaixar no
// blob pipe-separated do action_log.details. Tira | e quebras de linha.
func sanitizeAuditReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "|", "/")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 300 {
		s = s[:300] + "...[trunc]"
	}
	return s
}

// LogCriarEvento registra criacao de evento com campos estruturados para
// observabilidade da regra de data implicita. Details armazena um blob
// pipe-separado: "title=...|user_msg=...|date_source=...|claude_date=...|claude_time=...|resolved_start=...|adjusted=...".
func (a *AuditLog) LogCriarEvento(userID int64, title, userMsgSnippet, dateSource, claudeDate, claudeTime, resolvedStart string, adjusted bool) error {
	snippet := userMsgSnippet
	if runes := []rune(snippet); len(runes) > 120 {
		snippet = string(runes[:120])
	}
	details := fmt.Sprintf(
		"title=%s|user_msg=%s|date_source=%s|claude_date=%s|claude_time=%s|resolved_start=%s|adjusted=%t",
		title, snippet, dateSource, claudeDate, claudeTime, resolvedStart, adjusted,
	)
	_, err := a.db.conn.Exec(
		`INSERT INTO action_log (user_id, action, target_user, details) VALUES (?, ?, ?, ?)`,
		userID, "criar_evento", "", details)
	return err
}

// LogFamilyLinkCreated registra a criacao de um vinculo familiar.
// userID = ator (quem solicitou a criacao); pode ser o guardian ou um admin.
// Em geral, sera o guardian; mas mantemos generico pra futuro fluxo de
// admin/CS criando vinculos manualmente.
func (a *AuditLog) LogFamilyLinkCreated(userID, guardianID, dependentID int64, relationship string) error {
	details := fmt.Sprintf(
		"guardian_id=%d|dependent_id=%d|relationship=%s",
		guardianID, dependentID, relationship,
	)
	return a.Log(userID, "family_link_created", "", details)
}

// LogFamilyLinkRemoved registra a remocao de um vinculo familiar.
func (a *AuditLog) LogFamilyLinkRemoved(userID, guardianID, dependentID int64) error {
	details := fmt.Sprintf("guardian_id=%d|dependent_id=%d", guardianID, dependentID)
	return a.Log(userID, "family_link_removed", "", details)
}

// LogFamilyNotifyPrefsUpdated registra mudanca de preferencias de notificacao
// de um vinculo familiar.
func (a *AuditLog) LogFamilyNotifyPrefsUpdated(userID, linkID int64, before, after FamilyNotifyPrefs) error {
	details := fmt.Sprintf(
		"link_id=%d|before=med:%t,inat:%t,sig:%t|after=med:%t,inat:%t,sig:%t",
		linkID,
		before.OnMedicationMiss, before.OnInactivity, before.OnSevereSignal,
		after.OnMedicationMiss, after.OnInactivity, after.OnSevereSignal,
	)
	return a.Log(userID, "family_notify_prefs_updated", "", details)
}

// LogUserTypeChanged registra mudanca de tipo de usuario.
// userID       = ator (em geral, o proprio user; em fluxo admin pode ser outro).
// targetUserID = quem teve o tipo mudado.
func (a *AuditLog) LogUserTypeChanged(userID, targetUserID int64, before, after UserType) error {
	details := fmt.Sprintf("target_user_id=%d|before=%s|after=%s", targetUserID, before, after)
	return a.Log(userID, "user_type_changed", "", details)
}

func FormatAuditLog(userName string, entries []AuditEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("%s, nenhuma ação registrada nesse período.", userName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Histórico de ações de %s:\n\n", userName))

	for _, e := range entries {
		timeStr := e.CreatedAt.Format("02/01 15:04")
		label := actionLabelsPT[e.Action]
		if label == "" {
			label = e.Action
		}
		line := fmt.Sprintf("  %s — %s", timeStr, label)
		if e.TargetUser != "" {
			line += fmt.Sprintf(" (agenda de %s)", e.TargetUser)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}
