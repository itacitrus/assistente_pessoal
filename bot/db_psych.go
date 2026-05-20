package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// =========================================================================
// Fase 5 — psych_state_daily + helpers longitudinais
// =========================================================================
//
// Concentra todos os helpers de DB que a Fase 5 introduz. Mantido em arquivo
// proprio pra preservar coesao por feature (mesma estrategia de db_medication
// e db_severe_signal).
//
// Tabela psych_state_daily eh declarada em db.go (additive). Aqui vivem:
//   - UpsertPsychSnapshot
//   - GetPsychSnapshot / GetPsychSnapshots / GetSnapshotsForUserDateRange
//   - GetMedicationStats7d
//   - GetProactiveAttemptsStats / GetLatestProactiveAttempt
//   - GetOpenAlertsForUser (Alert struct local)
//   - GetSocialContextRiskMemos (filtra `key LIKE 'risco:%'`)
//   - GetMessagesSinceForUser
//   - GetMedicationIntakeOnDay
//   - GetAlertsOnDay
//   - GetUsersWithMessagesOnDay
//   - HasOpenInactivityEscalation / CreateInactivityEscalation /
//     UpdateEscalationStatus
//   - GetDependentConsent / SetDependentConsent
//   - ListUsersByType

// FamilyAlert eh a forma rica de uma escalation (com message, status). Usado
// pelo BuildDependentStatus pra mostrar alertas em aberto pro responsavel.
//
// Diferente de SevereSignalEscalation (db_severe_signal.go), inclui Message
// (texto efetivo enviado ao guardian) e mantem tipo desacoplado pra permitir
// adicionar campos sem quebrar callers do severe_signal.
type FamilyAlert struct {
	ID         int64     `json:"id"`
	PolicyName string    `json:"policy_name"`
	Severity   string    `json:"severity"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"`
}

// ProactiveAttemptsStats eh agregado dos ultimos 7d (Fase 5). Usado pelo
// BuildDependentStatus pra mostrar ao responsavel como Lurch tem tentado
// puxar conversa.
type ProactiveAttemptsStats struct {
	Last7d        int          `json:"last_7d"`
	LastAttemptAt sql.NullTime `json:"-"`
	LastAcked     bool         `json:"last_acked"`
}

// =========================================================================
// psych_state_daily
// =========================================================================

// UpsertPsychSnapshot insere ou atualiza a linha do dia. Multiplos triggers
// no mesmo (user, snapshot_date) atualizam — NAO criam linha nova.
//
// Score 0 vira NULL (via nullIfZero) — caller emite 0 quando nao da pra
// inferir. O banco preserva o estado "sem dado" semanticamente distinto de
// "muito baixo".
func (db *DB) UpsertPsychSnapshot(s *synthesis.DailySnapshot) error {
	if s == nil {
		return errors.New("UpsertPsychSnapshot: nil snapshot")
	}
	sinaisJSON, err := marshalStringSlice(s.SinaisObservados)
	if err != nil {
		return fmt.Errorf("marshal sinais: %w", err)
	}
	eventosJSON, err := marshalStringSlice(s.EventosDia)
	if err != nil {
		return fmt.Errorf("marshal eventos: %w", err)
	}
	_, err = db.conn.Exec(`
		INSERT INTO psych_state_daily (
			user_id, snapshot_date, humor_score, humor_nuance,
			energia_score, sociabilidade_score, autocuidado_score,
			sinais_observados, eventos_dia, n_conversations, n_messages,
			duration_minutes, confidence, inferred_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, snapshot_date) DO UPDATE SET
			humor_score         = excluded.humor_score,
			humor_nuance        = excluded.humor_nuance,
			energia_score       = excluded.energia_score,
			sociabilidade_score = excluded.sociabilidade_score,
			autocuidado_score   = excluded.autocuidado_score,
			sinais_observados   = excluded.sinais_observados,
			eventos_dia         = excluded.eventos_dia,
			n_conversations     = excluded.n_conversations,
			n_messages          = excluded.n_messages,
			duration_minutes    = excluded.duration_minutes,
			confidence          = excluded.confidence,
			inferred_at         = CURRENT_TIMESTAMP`,
		s.UserID, s.SnapshotDate.Format("2006-01-02"),
		nullIfZero(s.HumorScore), s.HumorNuance,
		nullIfZero(s.EnergiaScore), nullIfZero(s.SociabilidadeScore), nullIfZero(s.AutocuidadoScore),
		sinaisJSON, eventosJSON,
		s.NConversations, s.NMessages, s.DurationMinutes, s.Confidence,
	)
	if err != nil {
		return fmt.Errorf("upsert psych snapshot: %w", err)
	}
	return nil
}

// GetSnapshotsForUserDateRange retorna snapshots ordenados DESC por
// snapshot_date no intervalo [from, to] inclusivo. Datas sao comparadas
// como string YYYY-MM-DD — caller eh responsavel pelo fuso ao montar
// from/to.
func (db *DB) GetSnapshotsForUserDateRange(userID int64, from, to time.Time) ([]synthesis.DailySnapshot, error) {
	rows, err := db.conn.Query(`
		SELECT user_id, snapshot_date, humor_score, humor_nuance,
		       energia_score, sociabilidade_score, autocuidado_score,
		       sinais_observados, eventos_dia,
		       n_conversations, n_messages, duration_minutes, confidence
		FROM psych_state_daily
		WHERE user_id = ? AND snapshot_date BETWEEN ? AND ?
		ORDER BY snapshot_date DESC`,
		userID, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("get snapshots: %w", err)
	}
	defer rows.Close()
	var out []synthesis.DailySnapshot
	for rows.Next() {
		var s synthesis.DailySnapshot
		var dateStr string
		var hum, ene, soc, aut sql.NullInt64
		var sinais, eventos string
		if err := rows.Scan(
			&s.UserID, &dateStr, &hum, &s.HumorNuance,
			&ene, &soc, &aut,
			&sinais, &eventos,
			&s.NConversations, &s.NMessages, &s.DurationMinutes, &s.Confidence,
		); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		if d, err := time.Parse("2006-01-02", dateStr); err == nil {
			s.SnapshotDate = d
		}
		if hum.Valid {
			s.HumorScore = int(hum.Int64)
		}
		if ene.Valid {
			s.EnergiaScore = int(ene.Int64)
		}
		if soc.Valid {
			s.SociabilidadeScore = int(soc.Int64)
		}
		if aut.Valid {
			s.AutocuidadoScore = int(aut.Int64)
		}
		_ = json.Unmarshal([]byte(sinais), &s.SinaisObservados)
		_ = json.Unmarshal([]byte(eventos), &s.EventosDia)
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSnapshot retorna a linha de psych_state_daily pra (user, date), ou nil
// se nao existe. Usado pelo writer pra fazer update incremental.
func (db *DB) GetSnapshot(userID int64, date time.Time) (*synthesis.DailySnapshot, error) {
	snaps, err := db.GetSnapshotsForUserDateRange(userID, date, date)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, nil
	}
	return &snaps[0], nil
}

// nullIfZero converte 0 em sql.NullInt64{Valid:false} (=> NULL no banco).
// Usado pra distinguir "nao foi possivel inferir" (0/NULL) de "muito baixo" (1).
func nullIfZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// marshalStringSlice serializa []string como JSON string. Slice nil vira "[]"
// (default coluna), nao "null" — preserva semantica do banco.
func marshalStringSlice(items []string) (string, error) {
	if items == nil {
		return "[]", nil
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// =========================================================================
// medication stats (7d)
// =========================================================================

// GetMedicationStats7d agrega medication_intake_log no intervalo [from, to].
// Retorna shape MedicationStats compativel com synthesis.MedicationStats —
// o Synthesize transforma em medicationStatsW antes de enviar ao Sonnet.
//
// Usa coluna scheduled_at em UTC. Caller fornece from/to em UTC ou local;
// SQLite faz comparacao lexicografica que funciona pra DATETIME ISO.
func (db *DB) GetMedicationStats7d(userID int64, from, to time.Time) (synthesis.MedicationStats, error) {
	var s synthesis.MedicationStats

	rows, err := db.conn.Query(`
		SELECT l.status, COUNT(*) FROM medication_intake_log l
		JOIN medications m ON m.id = l.medication_id
		WHERE m.user_id = ? AND l.scheduled_at BETWEEN ? AND ?
		GROUP BY l.status`, userID, from.UTC(), to.UTC())
	if err != nil {
		return s, fmt.Errorf("med stats by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return s, fmt.Errorf("scan med stats: %w", err)
		}
		s.Scheduled += count
		switch status {
		case "taken":
			s.Taken = count
		case "missed", "escalated":
			s.Missed += count
		case "skipped":
			s.Skipped = count
		case "pending":
			s.Pending = count
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}

	if s.Scheduled > 0 {
		s.AdherenceFrac = float64(s.Taken) / float64(s.Scheduled)
	}

	missRows, err := db.conn.Query(`
		SELECT m.name, l.scheduled_at FROM medication_intake_log l
		JOIN medications m ON m.id = l.medication_id
		WHERE m.user_id = ? AND l.status IN ('missed','escalated')
		  AND l.scheduled_at BETWEEN ? AND ?
		ORDER BY l.scheduled_at DESC LIMIT 5`,
		userID, from.UTC(), to.UTC())
	if err != nil {
		return s, fmt.Errorf("missed doses query: %w", err)
	}
	defer missRows.Close()
	for missRows.Next() {
		var md synthesis.MissedDose
		if err := missRows.Scan(&md.MedicationName, &md.ScheduledAt); err != nil {
			return s, fmt.Errorf("scan missed dose: %w", err)
		}
		s.MissedDoses = append(s.MissedDoses, md)
	}
	return s, missRows.Err()
}

// =========================================================================
// proactive attempts stats
// =========================================================================

// GetProactiveAttemptsStats agrega proactive_attempts no intervalo [from, to].
// Retorna contagem 7d + ultima tentativa + se a ultima foi respondida.
func (db *DB) GetProactiveAttemptsStats(userID int64, from, to time.Time) (ProactiveAttemptsStats, error) {
	var stats ProactiveAttemptsStats
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM proactive_attempts
		WHERE user_id = ? AND attempted_at BETWEEN ? AND ?`,
		userID, from.UTC(), to.UTC(),
	).Scan(&stats.Last7d)
	if err != nil {
		return stats, fmt.Errorf("count proactive: %w", err)
	}

	var lastAt sql.NullTime
	var lastStatus sql.NullString
	err = db.conn.QueryRow(`
		SELECT attempted_at, status FROM proactive_attempts
		WHERE user_id = ? ORDER BY attempted_at DESC LIMIT 1`, userID,
	).Scan(&lastAt, &lastStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return stats, fmt.Errorf("last proactive: %w", err)
	}
	stats.LastAttemptAt = lastAt
	if lastStatus.Valid {
		stats.LastAcked = lastStatus.String == "replied"
	}
	return stats, nil
}

// GetLatestProactiveAttempt retorna a ultima tentativa proativa do user,
// ou nil se nao existe. Usado por checkInactivityEscalation.
func (db *DB) GetLatestProactiveAttempt(userID int64) (*ProactiveAttempt, error) {
	pa, err := db.GetLastProactiveAttempt(userID)
	if errors.Is(err, ErrNoProactiveAttempt) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pa, nil
}

// =========================================================================
// open alerts (escalations)
// =========================================================================

// GetOpenAlertsForUser retorna escalations em aberto pro user (status pending
// ou sent) — qualquer policy_name. Inclui medicacao + severe_signal +
// inactivity. Usado pra mostrar ao responsavel quantos alertas estao
// pendentes sem terem sido reconhecidos.
//
// Status canonico do schema (Fase 3): 'pending'|'sent'|'acknowledged'|
// 'escalated_to_family'|'failed'. Open = nao acked nem fechado.
func (db *DB) GetOpenAlertsForUser(userID int64) ([]FamilyAlert, error) {
	rows, err := db.conn.Query(`
		SELECT id, policy_name, severity, COALESCE(details, ''), created_at, status
		FROM escalations
		WHERE COALESCE(user_id, 0) = ? AND status IN ('pending','sent')
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("get open alerts: %w", err)
	}
	defer rows.Close()
	var out []FamilyAlert
	for rows.Next() {
		var a FamilyAlert
		if err := rows.Scan(&a.ID, &a.PolicyName, &a.Severity, &a.Message, &a.CreatedAt, &a.Status); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// =========================================================================
// social_context risco memos
// =========================================================================

// GetSocialContextRiskMemos retorna memos com category='social_context' E
// key LIKE 'risco:%'. Esta eh a fronteira de privacidade — APENAS memos
// com prefixo `risco:` atravessam pra alimentar o snapshot writer. Memos
// com `pessoa:`, `evento:`, `rotina:` etc. ficam confinados ao companion.
func (db *DB) GetSocialContextRiskMemos(userID int64, limit int) ([]synthesis.Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.conn.Query(`
		SELECT key, value FROM user_memories
		WHERE user_id = ? AND category = 'social_context' AND key LIKE 'risco:%'
		ORDER BY updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get risk memos: %w", err)
	}
	defer rows.Close()
	var out []synthesis.Memory
	for rows.Next() {
		var m synthesis.Memory
		if err := rows.Scan(&m.Key, &m.Value); err != nil {
			return nil, fmt.Errorf("scan risk memo: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// =========================================================================
// messages + medication intake by day
// =========================================================================

// GetMessagesSinceForUser retorna mensagens de conversation_history mais
// recentes que `since`, ordenadas ASC por created_at. Usado pelo catchup pra
// montar a janela de mensagens do dia que alimenta o writer.
func (db *DB) GetMessagesSinceForUser(userID int64, since time.Time) ([]synthesis.ConversationMessage, error) {
	rows, err := db.conn.Query(`
		SELECT role, content, created_at FROM conversation_history
		WHERE user_id = ? AND created_at >= ?
		ORDER BY created_at ASC`, userID, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("get messages since: %w", err)
	}
	defer rows.Close()
	var out []synthesis.ConversationMessage
	for rows.Next() {
		var m synthesis.ConversationMessage
		if err := rows.Scan(&m.Role, &m.Text, &m.Timestamp); err != nil {
			return nil, fmt.Errorf("scan msg: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMedicationIntakeOnDay retorna doses agendadas+status no calendar day
// (em fuso `tz`) do user. Tz=nil cai pra UTC.
func (db *DB) GetMedicationIntakeOnDay(userID int64, day time.Time, tz *time.Location) ([]synthesis.MedicationIntake, error) {
	if tz == nil {
		tz = time.UTC
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
	dayEnd := dayStart.Add(24 * time.Hour)
	rows, err := db.conn.Query(`
		SELECT m.name, l.scheduled_at, l.status FROM medication_intake_log l
		JOIN medications m ON m.id = l.medication_id
		WHERE m.user_id = ? AND l.scheduled_at >= ? AND l.scheduled_at < ?`,
		userID, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		return nil, fmt.Errorf("med intake on day: %w", err)
	}
	defer rows.Close()
	var out []synthesis.MedicationIntake
	for rows.Next() {
		var i synthesis.MedicationIntake
		if err := rows.Scan(&i.MedicationName, &i.ScheduledAt, &i.Status); err != nil {
			return nil, fmt.Errorf("scan intake: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GetAlertsOnDay retorna escalations criadas no dia [day em tz local]. Usado
// pelo writer pra detectar se ja houve alertar_familia hoje, evitando
// disparar safety_alert_needed redundante.
func (db *DB) GetAlertsOnDay(userID int64, day time.Time, tz *time.Location) ([]synthesis.Alert, error) {
	if tz == nil {
		tz = time.UTC
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
	dayEnd := dayStart.Add(24 * time.Hour)
	rows, err := db.conn.Query(`
		SELECT policy_name, COALESCE(severity, ''), created_at FROM escalations
		WHERE COALESCE(user_id, 0) = ? AND created_at >= ? AND created_at < ?`,
		userID, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		return nil, fmt.Errorf("alerts on day: %w", err)
	}
	defer rows.Close()
	var out []synthesis.Alert
	for rows.Next() {
		var a synthesis.Alert
		if err := rows.Scan(&a.PolicyName, &a.Severity, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan alert on day: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetUsersWithMessagesOnDay retorna idosos ativos que trocaram pelo menos
// 1 mensagem no calendar day (em UTC, com janela ampla). Usado pelo job de
// catchup pra varrer pendencias.
func (db *DB) GetUsersWithMessagesOnDay(day time.Time) ([]User, error) {
	pad := 14 * time.Hour
	dayStart := day.UTC().Add(-pad)
	dayEnd := day.UTC().Add(24*time.Hour + pad)
	rows, err := db.conn.Query(`
		SELECT DISTINCT u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
			u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
			u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at,
			u.type, u.last_user_message_at,
			u.inactivity_threshold_hours, u.proactive_paused_until
		FROM users u
		JOIN conversation_history h ON h.user_id = u.id
		WHERE u.type = 'idoso' AND u.is_active = 1
		  AND h.created_at BETWEEN ? AND ?`, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("users with messages on day: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		var thresh sql.NullInt64
		var paused sql.NullTime
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg, &thresh, &paused); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		scanUserExtras(&u, ut, lastMsg)
		scanUserPhase4(&u, thresh, paused)
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListUsersByType retorna idosos/responsaveis/comuns ativos. Usado pelo
// scheduler de checkInactivityEscalation pra varrer todos os idosos.
func (db *DB) ListUsersByType(t UserType) ([]User, error) {
	rows, err := db.conn.Query(`
		SELECT id, phone_number, name, google_calendar_id, google_credentials,
			daily_summary_time, weekly_summary_day, weekly_summary_time,
			reminder_before, auto_confirm_timeout, is_active, created_at,
			type, last_user_message_at,
			inactivity_threshold_hours, proactive_paused_until
		FROM users
		WHERE type = ? AND is_active = 1`, string(t))
	if err != nil {
		return nil, fmt.Errorf("list users by type: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		var thresh sql.NullInt64
		var paused sql.NullTime
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg, &thresh, &paused); err != nil {
			return nil, fmt.Errorf("scan user by type: %w", err)
		}
		scanUserExtras(&u, ut, lastMsg)
		scanUserPhase4(&u, thresh, paused)
		out = append(out, u)
	}
	return out, rows.Err()
}

// =========================================================================
// inactivity escalations
// =========================================================================

// HasOpenInactivityEscalation eh o lock idempotente do checkInactivityEscalation.
// Match por (user_id, policy_name=inactivity, proactive_attempt_id) — restart
// no meio do fluxo nao duplica disparo pro mesmo (user, attempt).
func (db *DB) HasOpenInactivityEscalation(userID int64, attemptID int64) (bool, error) {
	var n int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM escalations
		WHERE COALESCE(user_id, 0) = ?
		  AND policy_name = 'inactivity'
		  AND COALESCE(proactive_attempt_id, 0) = ?
		  AND status IN ('pending','sent','acknowledged')`,
		userID, attemptID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("has open inactivity: %w", err)
	}
	return n > 0, nil
}

// CreateInactivityEscalation cria uma row de escalation pra inactivity
// (Fase 5). Reusa a tabela de escalations da Fase 3 — usa pending_confirmation_id=0
// como sentinel (mesmo padrao do severe_signal).
//
// Status inicial 'sent' (tabela esta acoplada a sequencia de envio do scheduler).
// Caller pode chamar UpdateEscalationStatus depois pra reflitir falha de canal.
func (db *DB) CreateInactivityEscalation(userID, guardianID, attemptID int64, severity, details string, now time.Time) (int64, error) {
	// Constroi attemptKey unico (mesma estrategia do severe_signal: timestamp
	// em segundos. Resolucao 1s eh suficiente — dedup real vem do
	// HasOpenInactivityEscalation acima).
	attemptKey := int(now.UTC().Unix() % 2147483647)
	res, err := db.conn.Exec(`
		INSERT INTO escalations
		(pending_confirmation_id, policy_name, attempt_number, scheduled_for,
		 status, notifier_used, recipient_user_id, sent_at,
		 user_id, severity, details, proactive_attempt_id)
		VALUES (0, 'inactivity', ?, ?, 'sent', 'whatsapp', ?, ?, ?, ?, ?, ?)`,
		attemptKey, now.UTC(), guardianID, now.UTC(),
		userID, severity, details, attemptID,
	)
	if err != nil {
		// Retry com nanosec se colidiu na unique key.
		if strings.Contains(err.Error(), "UNIQUE") {
			attemptKey = int(now.UTC().UnixNano() % 2147483647)
			res, err = db.conn.Exec(`
				INSERT INTO escalations
				(pending_confirmation_id, policy_name, attempt_number, scheduled_for,
				 status, notifier_used, recipient_user_id, sent_at,
				 user_id, severity, details, proactive_attempt_id)
				VALUES (0, 'inactivity', ?, ?, 'sent', 'whatsapp', ?, ?, ?, ?, ?, ?)`,
				attemptKey, now.UTC(), guardianID, now.UTC(),
				userID, severity, details, attemptID,
			)
			if err != nil {
				return 0, fmt.Errorf("create inactivity escalation (retry): %w", err)
			}
		} else {
			return 0, fmt.Errorf("create inactivity escalation: %w", err)
		}
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// UpdateEscalationStatus atualiza o campo status (e sent_at quando vira 'sent').
// Status valido (CHECK): pending|sent|acknowledged|escalated_to_family|failed.
func (db *DB) UpdateEscalationStatus(escID int64, status string) error {
	allowed := map[string]bool{
		"pending": true, "sent": true, "acknowledged": true,
		"escalated_to_family": true, "failed": true,
	}
	if !allowed[status] {
		return fmt.Errorf("invalid escalation status: %q", status)
	}
	_, err := db.conn.Exec(`UPDATE escalations SET status = ? WHERE id = ?`, status, escID)
	return err
}

// =========================================================================
// dependent consent
// =========================================================================

// ConsentActive | ConsentRevoked sao os valores aceitos. Banco tem default
// 'active' — vinculos pre-Fase-5 sao consultaveis. Idoso revoga via tool
// futura (Fase 6+) ou via fluxo de admin.
const (
	ConsentActive  = "active"
	ConsentRevoked = "revoked"
)

// GetDependentConsent retorna o status de consentimento do dependente sobre
// relatorio agregado pro guardian (linkID = id do family_links). Retorna
// "active" como default quando o link nao existe (defesa em profundidade —
// caller deve ja ter validado IsGuardianOf).
func (db *DB) GetDependentConsent(guardianID, dependentID int64) (string, error) {
	var status sql.NullString
	err := db.conn.QueryRow(`
		SELECT dependent_consent_status FROM family_links
		WHERE guardian_id = ? AND dependent_id = ?`,
		guardianID, dependentID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ConsentActive, nil
	}
	if err != nil {
		return "", fmt.Errorf("get consent: %w", err)
	}
	if !status.Valid || status.String == "" {
		return ConsentActive, nil
	}
	return status.String, nil
}

// SetDependentConsent atualiza o status. Aceita apenas 'active' e 'revoked'.
func (db *DB) SetDependentConsent(guardianID, dependentID int64, status string) error {
	if status != ConsentActive && status != ConsentRevoked {
		return fmt.Errorf("invalid consent status: %q (use active|revoked)", status)
	}
	res, err := db.conn.Exec(`
		UPDATE family_links SET dependent_consent_status = ?
		WHERE guardian_id = ? AND dependent_id = ?`,
		status, guardianID, dependentID)
	if err != nil {
		return fmt.Errorf("set consent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFamilyLinkNotFound
	}
	return nil
}

// =========================================================================
// guardians filtered by inactivity
// =========================================================================

// GetGuardiansForInactivity eh um wrapper sobre GetGuardians (Fase 1) que
// filtra apenas vinculos com NotifyOnInactivity=true E
// dependent_consent_status='active'. Mantido como funcao nomeada (em vez
// de filtro inline) pra clareza no scheduler.
func (db *DB) GetGuardiansForInactivity(dependentID int64) ([]FamilyLink, error) {
	all, err := db.GetGuardians(dependentID)
	if err != nil {
		return nil, err
	}
	out := make([]FamilyLink, 0, len(all))
	for _, fl := range all {
		if !fl.Notify.OnInactivity {
			continue
		}
		// Consent revoked eh final — guardian nao recebe nada.
		consent, _ := db.GetDependentConsent(fl.GuardianID, fl.DependentID)
		if consent == ConsentRevoked {
			continue
		}
		out = append(out, fl)
	}
	return out, nil
}
