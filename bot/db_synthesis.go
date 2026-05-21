package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// StoredSynthesis eh a sintese longitudinal persistida de um dependente. O
// payload jah vem desserializado pra synthesis.ReportOutput.
type StoredSynthesis struct {
	DependentID int64
	Days        int
	Report      synthesis.ReportOutput
	GeneratedAt time.Time
}

// ErrSynthesisNotFound sinaliza ausencia de sintese persistida (dependente
// novo, sem geracao ainda). Caller decide o fallback (placeholder + regen).
var ErrSynthesisNotFound = errors.New("dependent synthesis not found")

// GetDependentSynthesis le a sintese persistida do dependente. Retorna
// ErrSynthesisNotFound quando ainda nao ha nenhuma.
func (db *DB) GetDependentSynthesis(dependentID int64) (*StoredSynthesis, error) {
	var (
		days        int
		payload     string
		generatedAt time.Time
	)
	err := db.conn.QueryRow(`
		SELECT days, payload, generated_at
		  FROM dependent_synthesis
		 WHERE dependent_id = ?`, dependentID,
	).Scan(&days, &payload, &generatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSynthesisNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query dependent synthesis: %w", err)
	}
	var report synthesis.ReportOutput
	if err := json.Unmarshal([]byte(payload), &report); err != nil {
		// Payload corrompido — trata como ausente pra forcar regen.
		return nil, ErrSynthesisNotFound
	}
	return &StoredSynthesis{
		DependentID: dependentID,
		Days:        days,
		Report:      report,
		GeneratedAt: generatedAt.UTC(),
	}, nil
}

// GetLatestSnapshotInferredAt devolve o inferred_at mais recente de qualquer
// snapshot do usuario. ok=false quando nao ha nenhum snapshot. Usado pra
// decidir se a sintese persistida ficou "stale" (gerada antes do dado novo).
func (db *DB) GetLatestSnapshotInferredAt(userID int64) (t time.Time, ok bool, err error) {
	var inferred sql.NullTime
	qerr := db.conn.QueryRow(
		`SELECT MAX(inferred_at) FROM psych_state_daily WHERE user_id = ?`, userID,
	).Scan(&inferred)
	if qerr != nil {
		return time.Time{}, false, fmt.Errorf("latest snapshot inferred_at: %w", qerr)
	}
	if !inferred.Valid {
		return time.Time{}, false, nil
	}
	return inferred.Time.UTC(), true, nil
}

// UpsertDependentSynthesis grava (ou substitui) a sintese do dependente.
func (db *DB) UpsertDependentSynthesis(dependentID int64, days int, report synthesis.ReportOutput, generatedAt time.Time) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal synthesis: %w", err)
	}
	_, err = db.conn.Exec(`
		INSERT INTO dependent_synthesis (dependent_id, days, payload, generated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(dependent_id) DO UPDATE SET
			days         = excluded.days,
			payload      = excluded.payload,
			generated_at = excluded.generated_at`,
		dependentID, days, string(payload), generatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert dependent synthesis: %w", err)
	}
	return nil
}
