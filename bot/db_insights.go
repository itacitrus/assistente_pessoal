package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInsightsNotFound sinaliza ausencia de insights persistidos pra (user, days).
var ErrInsightsNotFound = errors.New("agenda insights not found")

// GetAgendaInsights le o payload JSON dos insights persistidos de (userID,
// days). A (de)serializacao do api.InsightsResponse fica no adapter (que
// conhece o tipo do package api) — aqui guardamos bytes opacos.
func (db *DB) GetAgendaInsights(userID int64, days int) (payload string, generatedAt time.Time, err error) {
	var gen time.Time
	qerr := db.conn.QueryRow(`
		SELECT payload, generated_at
		  FROM user_agenda_insights
		 WHERE user_id = ? AND days = ?`, userID, days,
	).Scan(&payload, &gen)
	if errors.Is(qerr, sql.ErrNoRows) {
		return "", time.Time{}, ErrInsightsNotFound
	}
	if qerr != nil {
		return "", time.Time{}, fmt.Errorf("query agenda insights: %w", qerr)
	}
	return payload, gen.UTC(), nil
}

// AgendaInsightsTarget identifica um par (user, days) que ja tem insights
// persistidos — alvo do refresh diario agendado.
type AgendaInsightsTarget struct {
	UserID int64
	Days   int
}

// ListAgendaInsightsTargets retorna todos os pares (user, days) com insights
// ja persistidos. O refresh diario regenera so esses — mantem fresco o que ja
// existe sem gastar Sonnet com quem nunca abriu o painel (esses geram no
// primeiro acesso, pelo caminho read-stale do handler).
func (db *DB) ListAgendaInsightsTargets() ([]AgendaInsightsTarget, error) {
	rows, err := db.conn.Query(`SELECT user_id, days FROM user_agenda_insights`)
	if err != nil {
		return nil, fmt.Errorf("list agenda insights targets: %w", err)
	}
	defer rows.Close()
	var out []AgendaInsightsTarget
	for rows.Next() {
		var t AgendaInsightsTarget
		if err := rows.Scan(&t.UserID, &t.Days); err != nil {
			return nil, fmt.Errorf("scan target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpsertAgendaInsights grava (ou substitui) os insights de (userID, days).
func (db *DB) UpsertAgendaInsights(userID int64, days int, payload string, generatedAt time.Time) error {
	_, err := db.conn.Exec(`
		INSERT INTO user_agenda_insights (user_id, days, payload, generated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, days) DO UPDATE SET
			payload      = excluded.payload,
			generated_at = excluded.generated_at`,
		userID, days, payload, generatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert agenda insights: %w", err)
	}
	return nil
}
