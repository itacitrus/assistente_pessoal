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
	"criar_evento":       "Criou evento",
	"editar_evento":      "Editou evento",
	"cancelar_evento":    "Cancelou evento",
	"consultar_agenda":   "Consultou agenda",
	"confirmar":          "Confirmou",
	"negar":              "Negou",
	"auto_confirm":       "Auto-confirmou",
	"grant_access":       "Concedeu acesso",
	"grant_access_once":  "Concedeu acesso (pontual)",
	"revoke_access":      "Revogou acesso",
	"deny_access":        "Negou acesso",
	"permission_request": "Solicitou acesso",
	"consultar_log":      "Consultou historico",
}

// LogCriarEvento registra criacao de evento com campos estruturados para
// observabilidade da regra de data implicita. Details armazena um blob
// pipe-separado: "title=...|user_msg=...|date_source=...|claude_date=...|claude_time=...|resolved_start=...|adjusted=...".
func (a *AuditLog) LogCriarEvento(userID int64, title, userMsgSnippet, dateSource, claudeDate, claudeTime, resolvedStart string, adjusted bool) error {
	snippet := userMsgSnippet
	if len(snippet) > 120 {
		snippet = snippet[:120]
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

func FormatAuditLog(userName string, entries []AuditEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("%s, nenhuma acao registrada nesse periodo.", userName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Historico de acoes de %s:\n\n", userName))

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
