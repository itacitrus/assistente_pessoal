package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// =========================================================================
// Proactive attempts (Fase 4 — companion + proatividade)
// =========================================================================
//
// Lurch puxa conversa com idoso quando ele fica calado. O lock anti-rajada
// vive em DB (sobrevive a restart): cada disparo cria uma row em
// proactive_attempts. checkInactivity consulta HasRecentProactiveAttempt
// antes de tentar de novo. MarkUserMessageReceived flipa 'sent' -> 'replied'
// quando o idoso volta.

// ProactiveAttempt eh uma tentativa registrada de Lurch puxar conversa.
// Persistida em proactive_attempts. Status enuma: 'sent' | 'failed' |
// 'replied' | 'ignored'.
type ProactiveAttempt struct {
	ID          int64
	UserID      int64
	AttemptedAt time.Time
	MessageSent string
	Status      string
	RepliedAt   *time.Time
}

// MarkUserMessageReceivedAndProactive estende MarkUserMessageReceived da
// Fase 1 com o side-effect de flipar proactive_attempts pendente para
// 'replied'. Idempotente, transacional. Quando o idoso responde a uma
// puxada de conversa do Lurch, queremos saber que houve resposta — Fase 5
// usa pra escalation por inatividade prolongada.
//
// Diferente de MarkUserMessageReceived (Fase 1), que so atualiza
// last_user_message_at, esta versao tambem mexe em proactive_attempts.
// Mantemos as duas funcoes pra preservar API da Fase 1 e segregar
// responsabilidade.
func (db *DB) MarkUserMessageReceivedAndProactive(userID int64, ts time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		ts.UTC(), userID,
	)
	if err != nil {
		return fmt.Errorf("update last_user_message_at: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}

	// Flipa proactive_attempts 'sent' (sem reply) -> 'replied' nas ultimas
	// 12h. Janela de 12h e generosa: idoso pode ler de manha o que Lurch
	// mandou de noite e responder so depois.
	_, err = tx.Exec(
		`UPDATE proactive_attempts
		 SET status = 'replied', replied_at = ?
		 WHERE user_id = ? AND status = 'sent'
		   AND attempted_at >= datetime('now', '-12 hours')`,
		ts.UTC(), userID,
	)
	if err != nil {
		return fmt.Errorf("flip proactive replied: %w", err)
	}
	return tx.Commit()
}

// HasRecentProactiveAttempt retorna true se ha alguma tentativa proativa
// (independente de status) nas ultimas `within` horas. Usado como lock
// anti-rajada do checkInactivity — sobrevive a restart porque vive em DB.
func (db *DB) HasRecentProactiveAttempt(userID int64, within time.Duration) (bool, error) {
	if within <= 0 {
		return false, nil
	}
	cutoff := time.Now().UTC().Add(-within)
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM proactive_attempts
		 WHERE user_id = ? AND attempted_at >= ?`,
		userID, cutoff,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has recent proactive: %w", err)
	}
	return count > 0, nil
}

// RecordProactiveAttempt insere uma row com status='sent'. Devolve o id
// pra caso o caller precise marcar como failed depois (race com sendMsg).
//
// Estrategia anti-duplicata: o caller faz HasRecentProactiveAttempt ANTES
// e RecordProactiveAttempt ANTES de chamar sendMsg. Se duas instancias do
// scheduler rodassem em paralelo (cenario nao-MVP), poderia haver double
// insert — aceitavel ate migrarmos pra Postgres com lock distribuido.
func (db *DB) RecordProactiveAttempt(userID int64, message string) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO proactive_attempts (user_id, attempted_at, message_sent, status)
		 VALUES (?, ?, ?, 'sent')`,
		userID, time.Now().UTC(), message,
	)
	if err != nil {
		return 0, fmt.Errorf("record proactive: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// MarkProactiveAttemptFailed marca status='failed'. Chamada quando sendMsg
// retorna erro depois de RecordProactiveAttempt ter inserido a row
// — preservamos o registro do attempt, mas marcamos a falha pra observar.
func (db *DB) MarkProactiveAttemptFailed(id int64) error {
	_, err := db.conn.Exec(
		`UPDATE proactive_attempts SET status = 'failed' WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("mark proactive failed: %w", err)
	}
	return nil
}

// GetLastProactiveAttempt retorna a ultima tentativa registrada para
// userID, ou ErrNoProactiveAttempt se nao houver. Util pra testes e pra
// inspecao em painel.
func (db *DB) GetLastProactiveAttempt(userID int64) (*ProactiveAttempt, error) {
	row := db.conn.QueryRow(
		`SELECT id, user_id, attempted_at, message_sent, status, replied_at
		 FROM proactive_attempts WHERE user_id = ?
		 ORDER BY attempted_at DESC LIMIT 1`, userID,
	)
	pa := &ProactiveAttempt{}
	var replied sql.NullTime
	err := row.Scan(&pa.ID, &pa.UserID, &pa.AttemptedAt, &pa.MessageSent, &pa.Status, &replied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoProactiveAttempt
	}
	if err != nil {
		return nil, fmt.Errorf("get last proactive: %w", err)
	}
	if replied.Valid {
		t := replied.Time
		pa.RepliedAt = &t
	}
	return pa, nil
}

// PauseProactive seta proactive_paused_until = now + days dias. Usado pela
// tool pausar_proatividade. Range util: 1..30. Caller eh responsavel por
// validar.
func (db *DB) PauseProactive(userID int64, days int) error {
	if days < 1 {
		days = 1
	}
	if days > 30 {
		days = 30
	}
	until := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	res, err := db.conn.Exec(
		`UPDATE users SET proactive_paused_until = ? WHERE id = ?`,
		until, userID,
	)
	if err != nil {
		return fmt.Errorf("pause proactive: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// IsProactivePaused retorna true se proactive_paused_until for futuro.
// NULL/zero = nao pausado. Defensivo: erro de DB devolve (false, err) — caller
// pode logar e seguir (nao tomar decisao critica em cima de dado faltando).
func (db *DB) IsProactivePaused(userID int64) (bool, error) {
	var until sql.NullTime
	err := db.conn.QueryRow(
		`SELECT proactive_paused_until FROM users WHERE id = ?`, userID,
	).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, fmt.Errorf("is proactive paused: %w", err)
	}
	if !until.Valid {
		return false, nil
	}
	return until.Time.After(time.Now().UTC()), nil
}

// ErrNoProactiveAttempt indica que nao ha row em proactive_attempts pro
// user requisitado.
var ErrNoProactiveAttempt = errors.New("no proactive attempt found")
