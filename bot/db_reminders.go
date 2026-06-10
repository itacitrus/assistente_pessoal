package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Camada de dados dos lembretes pontuais (contrato P2). Espelha as convenções
// de db_medication.go: helpers idempotentes, claim atômico via UPDATE
// condicional, catch-up-safe (fire_at <= now), erros sentinela.
//
// fire_at é UNIX EPOCH SEGUNDOS (INTEGER) — ver comentário do schema em db.go.

var ErrReminderDuplicate = errors.New("lembrete pendente identico ja existe")

type Reminder struct {
	ID        int64
	UserID    int64
	Text      string
	FireAt    time.Time // UTC
	Status    string    // pending | sent | canceled | missed | failed
	Origin    string
	Attempts  int
	CreatedAt time.Time
}

// CreateReminderIfAbsent insere um lembrete pendente. Idempotente: colisão no
// índice único parcial (user_id, fire_at, text WHERE pending) devolve
// ErrReminderDuplicate — restart no mesmo minuto não duplica. Recriar após
// cancelar FUNCIONA (o índice só cobre pending).
func (db *DB) CreateReminderIfAbsent(userID int64, text string, fireAt time.Time) (int64, error) {
	res, err := db.conn.Exec(
		`INSERT INTO reminders (user_id, text, fire_at) VALUES (?, ?, ?)`,
		userID, text, fireAt.UTC().Unix())
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrReminderDuplicate
		}
		return 0, fmt.Errorf("create reminder: %w", err)
	}
	return res.LastInsertId()
}

// GetDueReminders lista os pendentes com fire_at <= now (catch-up-safe: tick
// perdido dispara atrasado, nunca some em silêncio). O teto de staleness é
// aplicado pelo scheduler (checkAdHocReminders), não aqui — o dado de "quão
// atrasado" é decisão de política, não de query.
func (db *DB) GetDueReminders(now time.Time) ([]Reminder, error) {
	rows, err := db.conn.Query(
		`SELECT id, user_id, text, fire_at, status, origin, attempts, created_at
		 FROM reminders WHERE status='pending' AND fire_at <= ?
		 ORDER BY fire_at ASC, id ASC`, now.UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("get due reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

// ClaimReminderForSend faz o claim ATÔMICO antes do envio (padrão provado do
// repo — CreateIntakeLogIfAbsent roda ANTES do Send): só um tick ganha a row.
// Retorna false quando outro tick (ou um envio anterior) já a reivindicou.
// robfig/cron v3 NÃO serializa ativações e o send pode passar de 60s —
// send-then-mark teria janela real de envio duplo.
func (db *DB) ClaimReminderForSend(id int64, now time.Time) (bool, error) {
	res, err := db.conn.Exec(
		`UPDATE reminders SET status='sent', sent_at=? WHERE id=? AND status='pending'`,
		now.UTC(), id)
	if err != nil {
		return false, fmt.Errorf("claim reminder: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ReleaseReminderAfterSendFailure devolve a row a 'pending' (compensação) para
// o próximo tick re-disparar; attempts incrementa e, no cap, vira 'failed'
// (auditável — a promessa não pode sumir em silêncio).
func (db *DB) ReleaseReminderAfterSendFailure(id int64, maxAttempts int) (failedOut bool, err error) {
	res, err := db.conn.Exec(
		`UPDATE reminders SET status='pending', sent_at=NULL, attempts=attempts+1
		 WHERE id=? AND status='sent'`, id)
	if err != nil {
		return false, fmt.Errorf("release reminder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	res, err = db.conn.Exec(
		`UPDATE reminders SET status='failed' WHERE id=? AND status='pending' AND attempts >= ?`,
		id, maxAttempts)
	if err != nil {
		return false, fmt.Errorf("fail reminder: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkReminderMissed marca como perdido (staleness além da graça do catch-up).
func (db *DB) MarkReminderMissed(id int64) error {
	_, err := db.conn.Exec(`UPDATE reminders SET status='missed' WHERE id=? AND status='pending'`, id)
	return err
}

// CancelReminder cancela um pendente do usuário. Retorna false se não havia
// pendente com esse id para esse usuário (id de outro usuário não cancela).
func (db *DB) CancelReminder(userID, id int64) (bool, error) {
	res, err := db.conn.Exec(
		`UPDATE reminders SET status='canceled' WHERE id=? AND user_id=? AND status='pending'`,
		id, userID)
	if err != nil {
		return false, fmt.Errorf("cancel reminder: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ListPendingReminders lista os pendentes do usuário em ordem de disparo.
// Consumido pela tool (listar/cancelar) e pelo guard I2a (promessa com
// horário que bate com um pendente é VERDADEIRA).
func (db *DB) ListPendingReminders(userID int64) ([]Reminder, error) {
	rows, err := db.conn.Query(
		`SELECT id, user_id, text, fire_at, status, origin, attempts, created_at
		 FROM reminders WHERE user_id=? AND status='pending'
		 ORDER BY fire_at ASC, id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list pending reminders: %w", err)
	}
	defer rows.Close()
	return scanReminders(rows)
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var fireAtUnix int64
		if err := rows.Scan(&r.ID, &r.UserID, &r.Text, &fireAtUnix, &r.Status, &r.Origin, &r.Attempts, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.FireAt = time.Unix(fireAtUnix, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}
