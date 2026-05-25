package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Rate-limit do botao "Atualizar" manual do painel: 1x/dia por (user, scope).
// scope eh "insights" (titular) ou "dependent:{id}" (relatorio de familia).
// Compara last_refresh_at contra a meia-noite local (dayStartUTC) — assim o
// limite reseta no virar do dia do usuario, nao numa janela rolante de 24h.

// ManualRefreshAllowed retorna true se o usuario ainda nao acionou o refresh de
// `scope` desde dayStartUTC (meia-noite local convertida pra UTC).
func (db *DB) ManualRefreshAllowed(userID int64, scope string, dayStartUTC time.Time) (bool, error) {
	var last time.Time
	err := db.conn.QueryRow(
		`SELECT last_refresh_at FROM manual_refreshes WHERE user_id = ? AND scope = ?`,
		userID, scope,
	).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("manual refresh allowed: %w", err)
	}
	return last.UTC().Before(dayStartUTC), nil
}

// MarkManualRefresh registra (upsert) que o usuario acionou o refresh de `scope`
// em `now`. Chamado APOS o refresh concluir com sucesso — falha nao queima a
// cota do dia.
func (db *DB) MarkManualRefresh(userID int64, scope string, now time.Time) error {
	_, err := db.conn.Exec(`
		INSERT INTO manual_refreshes (user_id, scope, last_refresh_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, scope) DO UPDATE SET last_refresh_at = excluded.last_refresh_at`,
		userID, scope, now.UTC(),
	)
	if err != nil {
		return fmt.Errorf("mark manual refresh: %w", err)
	}
	return nil
}
