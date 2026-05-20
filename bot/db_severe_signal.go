package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// =========================================================================
// Severe-signal escalations (Fase 4 — alertar_familia)
// =========================================================================
//
// A tabela escalations foi criada na Fase 3 acoplada a pending_confirmations
// (medicacao). A Fase 4 adicionou colunas (user_id, severity, details,
// proactive_attempt_id) para suportar:
//   1. severe_signal — alertar_familia direto (sem pending). Uso colunas
//      user_id + severity + details. pending_confirmation_id=0 (sentinel).
//   2. snapshot safety net — Haiku detectou critical apos conversa.
//      policy_name="severe_signal_safety_net".
//
// FK em pending_confirmation_id NAO eh enforced (PRAGMA foreign_keys nao
// esta ligado em data/bot.db). pending_confirmation_id=0 funciona como
// sentinel "nao aplicavel". Caso futuramente liguemos FKs, criamos uma
// row sentinela em pending_confirmations(id=0) ou separamos a tabela.

// SevereSignalEscalation eh um registro de alertar_familia. Diferente da
// Escalation (Fase 3, medicacao), nao tem attempt_number/recipient unicos
// — pode haver multiplos guardians no mesmo dispatch, e cada um eh uma row.
type SevereSignalEscalation struct {
	ID              int64
	UserID          int64
	PolicyName      string
	Severity        string
	Details         string
	RecipientUserID int64
	CreatedAt       time.Time
}

// RecordSevereSignalEscalation insere uma row em escalations representando
// uma notificacao de severe_signal (alertar_familia ou safety_net). Usa
// pending_confirmation_id=0 como sentinel pra distinguir de escalation de
// medicacao. attempt_number=0 idem.
//
// Param:
//   userID         — idoso (sujeito do alerta).
//   policyName     — "severe_signal" | "severe_signal_safety_net" | "severe_signal_supressed".
//   severity       — "info" | "warn" | "critical".
//   details        — campo livre pipe-separated (ver convencao em audit.go).
//   recipientID    — guardian que recebeu. 0 quando nao houve guardian (orfao).
//   channel        — "whatsapp" | "voice" | "" (vazio se nao houve envio).
func (db *DB) RecordSevereSignalEscalation(userID int64, policyName, severity, details string, recipientID int64, channel string, now time.Time) (*SevereSignalEscalation, error) {
	if channel == "" {
		channel = "whatsapp"
	}
	if recipientID < 0 {
		recipientID = 0
	}
	// UNIQUE da Fase 3 cobre (pending_confirmation_id, attempt_number,
	// recipient_user_id). Pra severe_signal usamos pending_confirmation_id=0
	// como sentinel — entao precisamos garantir unicidade via attempt_number.
	// Estrategia: usar timestamp UTC em segundos como attempt_number "unico".
	// Resolucao 1s eh suficiente — em pratica, severe_signal eh raro
	// (cooldown 1h pra critical), e mesmo em rajada sintetica os
	// timestamps diferem por dezenas de ms (insert eh sequencial).
	attemptKey := int(now.UTC().Unix() % 2147483647)
	res, err := db.conn.Exec(
		`INSERT INTO escalations
		 (pending_confirmation_id, policy_name, attempt_number, scheduled_for,
		  status, notifier_used, recipient_user_id, sent_at,
		  user_id, severity, details)
		 VALUES (0, ?, ?, ?, 'sent', ?, ?, ?, ?, ?, ?)`,
		policyName, attemptKey, now.UTC(), channel, recipientID, now.UTC(),
		userID, severity, details,
	)
	if err != nil {
		// Se houver colisao mesma assim (timestamp identico + recipient
		// identico — improvavel mas possivel em test rapido), usa epoch ms.
		if strings.Contains(err.Error(), "UNIQUE") {
			attemptKey = int(now.UTC().UnixNano() % 2147483647)
			res, err = db.conn.Exec(
				`INSERT INTO escalations
				 (pending_confirmation_id, policy_name, attempt_number, scheduled_for,
				  status, notifier_used, recipient_user_id, sent_at,
				  user_id, severity, details)
				 VALUES (0, ?, ?, ?, 'sent', ?, ?, ?, ?, ?, ?)`,
				policyName, attemptKey, now.UTC(), channel, recipientID, now.UTC(),
				userID, severity, details,
			)
			if err != nil {
				return nil, fmt.Errorf("record severe signal escalation (retry): %w", err)
			}
		} else {
			return nil, fmt.Errorf("record severe signal escalation: %w", err)
		}
	}
	id, _ := res.LastInsertId()
	return &SevereSignalEscalation{
		ID:              id,
		UserID:          userID,
		PolicyName:      policyName,
		Severity:        severity,
		Details:         details,
		RecipientUserID: recipientID,
		CreatedAt:       now.UTC(),
	}, nil
}

// HasRecentSevereSignalEscalation retorna true se houve escalation de
// severe_signal para esse userID com a severity dada nas ultimas `within`.
// Usado pelo handler de alertar_familia pra cooldown — evita reenvio em
// rajada de mensagens ambiguas.
//
// Match por policy_name LIKE 'severe_signal%' cobre as variantes
// (severe_signal, severe_signal_safety_net) — ambas contam pra cooldown.
func (db *DB) HasRecentSevereSignalEscalation(userID int64, severity string, within time.Duration) (bool, error) {
	if within <= 0 {
		return false, nil
	}
	cutoff := time.Now().UTC().Add(-within)
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM escalations
		 WHERE user_id = ?
		   AND severity = ?
		   AND policy_name LIKE 'severe_signal%'
		   AND created_at >= ?`,
		userID, severity, cutoff,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has recent severe signal: %w", err)
	}
	return count > 0, nil
}

// ListSevereSignalEscalations retorna o historico de severe_signal pra
// userID. Util pra testes e painel admin.
func (db *DB) ListSevereSignalEscalations(userID int64, limit int) ([]SevereSignalEscalation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(
		`SELECT id, user_id, policy_name, severity, details, recipient_user_id, created_at
		 FROM escalations
		 WHERE user_id = ? AND policy_name LIKE 'severe_signal%'
		 ORDER BY created_at DESC LIMIT ?`,
		userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list severe signal: %w", err)
	}
	defer rows.Close()
	var out []SevereSignalEscalation
	for rows.Next() {
		var e SevereSignalEscalation
		if err := rows.Scan(&e.ID, &e.UserID, &e.PolicyName, &e.Severity, &e.Details, &e.RecipientUserID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ErrSevereSignalCooldown indica que ha escalation recente para esse
// (user, severity). Caller decide se ainda registra a row de "supressed"
// pra observabilidade ou se aborta totalmente.
var ErrSevereSignalCooldown = errors.New("severe signal in cooldown window")
