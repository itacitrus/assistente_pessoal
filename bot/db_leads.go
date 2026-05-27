package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Lead representa um numero desconhecido em conversa de aquisicao (pre-cadastro).
type Lead struct {
	Phone     string
	NameGuess string
	Status    string // chatting | converted | declined
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LeadMessage eh um turno da conversa de vendas com um lead.
type LeadMessage struct {
	Role      string // "user" | "assistant"
	Content   string
	CreatedAt time.Time
}

const (
	LeadStatusChatting  = "chatting"
	LeadStatusConverted = "converted"
	LeadStatusDeclined  = "declined"
)

// GetLead busca um lead por telefone. Retorna ErrNotFound (sql.ErrNoRows
// embrulhado) quando nao existe.
func (db *DB) GetLead(phone string) (*Lead, error) {
	l := &Lead{}
	err := db.conn.QueryRow(
		`SELECT phone, name_guess, status, created_at, updated_at
		 FROM leads WHERE phone = ?`, phone,
	).Scan(&l.Phone, &l.NameGuess, &l.Status, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return l, nil
}

// UpsertLead garante a existencia do lead. Em insert, grava name_guess; em
// conflito (lead ja existe), so atualiza name_guess se ainda estiver vazio
// (nunca sobrescreve um palpite ja registrado) e bumpa updated_at. Nao mexe
// em status — transicoes de status sao explicitas (MarkLeadConverted etc).
func (db *DB) UpsertLead(phone, nameGuess string) error {
	now := time.Now().UTC()
	_, err := db.conn.Exec(`
		INSERT INTO leads (phone, name_guess, status, created_at, updated_at)
		VALUES (?, ?, 'chatting', ?, ?)
		ON CONFLICT(phone) DO UPDATE SET
			name_guess = CASE WHEN leads.name_guess = '' THEN excluded.name_guess ELSE leads.name_guess END,
			updated_at = excluded.updated_at`,
		phone, nameGuess, now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert lead: %w", err)
	}
	return nil
}

// MarkLeadConverted marca o lead como convertido (conta criada). Idempotente.
func (db *DB) MarkLeadConverted(phone string) error {
	_, err := db.conn.Exec(
		`UPDATE leads SET status = 'converted', updated_at = ? WHERE phone = ?`,
		time.Now().UTC(), phone,
	)
	return err
}

// AddLeadMessage anexa um turno ao historico do lead e poda o historico
// mantendo apenas os ultimos 50 turnos (espelha conversation_history).
func (db *DB) AddLeadMessage(phone, role, content string) error {
	if _, err := db.conn.Exec(
		`INSERT INTO lead_messages (phone, role, content) VALUES (?, ?, ?)`,
		phone, role, content,
	); err != nil {
		return fmt.Errorf("add lead message: %w", err)
	}
	db.conn.Exec(`DELETE FROM lead_messages WHERE phone = ? AND id NOT IN (
		SELECT id FROM lead_messages WHERE phone = ? ORDER BY id DESC LIMIT 50
	)`, phone, phone)
	return nil
}

// GetLeadMessages retorna o historico em ordem cronologica (mais antigo
// primeiro), limitado aos ultimos `limit` turnos.
func (db *DB) GetLeadMessages(phone string, limit int) ([]LeadMessage, error) {
	rows, err := db.conn.Query(
		`SELECT role, content, created_at FROM (
			SELECT id, role, content, created_at FROM lead_messages
			WHERE phone = ? ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC`, phone, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeadMessage
	for rows.Next() {
		var m LeadMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountLeadMessagesSince conta turnos do lead (qualquer role) a partir de
// `since`. Usado como defesa anti-spam: limita quanto LLM um numero frio
// consome por janela de tempo.
func (db *DB) CountLeadMessagesSince(phone string, since time.Time) (int, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM lead_messages WHERE phone = ? AND created_at >= ?`,
		phone, since.UTC(),
	).Scan(&n)
	return n, err
}
