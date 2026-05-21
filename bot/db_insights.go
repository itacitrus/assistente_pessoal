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
