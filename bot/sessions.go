package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// =========================================================================
// Fase 2 (web/UI) — sessoes do painel + magic link
// =========================================================================
//
// Token plaintext eh gerado com 32 bytes via crypto/rand e codificado em hex
// (64 chars). Apenas o sha256(token) em hex eh gravado no banco — o plaintext
// fica:
//   - na mensagem de WhatsApp do magic link (15min)
//   - no cookie httpOnly do navegador (30d)
//
// Sessao tem 4 estados:
//   pending  -> recem-criada, antes do clique no link (validade 15min)
//   active   -> apos POST /auth/verify; sliding window 30d
//   revoked  -> apos logout explicito ou troca de credencial
//   expired  -> expires_at < now (sweep marca em batch; lookup tambem trata)
//
// Authoring: nada de imediatismo — nao reusamos sessao expirada nem
// ressuscitamos revoked. Sempre cria nova entry pra cada request-link.

// WebSession representa uma sessao do painel web. Quem persiste nunca toca
// no plaintext do token — esta struct reflete fielmente as colunas.
type WebSession struct {
	ID          int64
	UserID      int64
	TokenHash   string
	Status      string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	ActivatedAt *time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
	IP          string
	UserAgent   string

	// ImpersonatedUserID eh o usuario que o dono (admin) desta sessao esta
	// visualizando via "ver como". NULL = sem impersonacao. So tem efeito se
	// o dono real for admin (checado no RequireAuth) — gravar aqui nao concede
	// privilegio por si so.
	ImpersonatedUserID *int64
}

// Sentinels.
var (
	ErrSessionNotFound = errors.New("web session not found")
	ErrSessionInvalid  = errors.New("web session in unexpected state")
	ErrSessionExpired  = errors.New("web session expired")
)

// Constantes de tempo, em vez de hardcode espalhado.
const (
	// MagicLinkTTL eh quanto tempo o magic link enviado por WhatsApp permanece
	// valido. 15min eh o padrao de mercado (Vercel/Linear/Cal.com) — curto o
	// suficiente pra reduzir janela de ataque, longo o suficiente pra abrir
	// o WhatsApp e clicar no link sem fricao.
	MagicLinkTTL = 15 * time.Minute

	// SessionTTL eh a janela deslizante de uma sessao ativa. Renovada a cada
	// RequireAuth — 30d sem volta = re-login.
	SessionTTL = 30 * 24 * time.Hour
)

// generateSessionToken gera 32 bytes aleatorios em hex (64 chars). Retorna
// (plaintext, sha256_hex). O plaintext eh o que vai no link/cookie; o hash
// eh o que vai no banco.
func generateSessionToken() (plaintext, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("rand read: %w", err)
	}
	plaintext = hex.EncodeToString(buf)
	hash = HashSessionToken(plaintext)
	return plaintext, hash, nil
}

// HashSessionToken normaliza a hash do token. Usar em todo lookup —
// plaintext nunca atinge o WHERE clause direto.
func HashSessionToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// CreatePendingSession insere uma row pending pro user. Caller eh responsavel
// por, em sequencia, mandar o magic link via WhatsApp com o plaintext. Em caso
// de falha no envio, a row continua existindo (expira em 15min) — opacidade
// da resposta eh preservada.
//
// Retorna a sessao criada (com plaintext via channel separado) — para evitar
// o caller misturar plaintext com hash, retornamos o plaintext em string
// separada.
func (db *DB) CreatePendingSession(userID int64, ip, userAgent string) (sess *WebSession, plaintext string, err error) {
	plaintext, hash, err := generateSessionToken()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	expires := now.Add(MagicLinkTTL)
	res, err := db.conn.Exec(`
		INSERT INTO web_sessions
			(user_id, token_hash, status, expires_at, created_at, ip, user_agent)
		VALUES (?, ?, 'pending', ?, ?, ?, ?)`,
		userID, hash, expires, now, ip, userAgent,
	)
	if err != nil {
		return nil, "", fmt.Errorf("insert web session: %w", err)
	}
	id, _ := res.LastInsertId()
	return &WebSession{
		ID:        id,
		UserID:    userID,
		TokenHash: hash,
		Status:    "pending",
		ExpiresAt: expires,
		CreatedAt: now,
		IP:        ip,
		UserAgent: userAgent,
	}, plaintext, nil
}

// ActivateSession marca uma sessao pending como active e estende o
// expires_at pra now()+30d. Retorna a sessao atualizada com user_id pra o
// caller poder buscar o user.
//
// Erros distintos pra cada cenario — caller decide como mapear pra HTTP:
//   - ErrSessionNotFound: hash nao existe
//   - ErrSessionExpired : pending mas expirou
//   - ErrSessionInvalid : ja active ou revoked (defesa contra reuso de link)
func (db *DB) ActivateSession(plaintext string) (*WebSession, error) {
	hash := HashSessionToken(plaintext)
	sess, err := db.getSessionByHash(hash)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	switch sess.Status {
	case "pending":
		if sess.ExpiresAt.Before(now) {
			return nil, ErrSessionExpired
		}
	case "active", "revoked", "expired":
		return nil, ErrSessionInvalid
	default:
		return nil, ErrSessionInvalid
	}

	newExpires := now.Add(SessionTTL)
	_, err = db.conn.Exec(`
		UPDATE web_sessions SET
			status       = 'active',
			activated_at = ?,
			last_used_at = ?,
			expires_at   = ?
		WHERE id = ?`,
		now, now, newExpires, sess.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("activate web session: %w", err)
	}
	sess.Status = "active"
	sess.ActivatedAt = &now
	sess.LastUsedAt = &now
	sess.ExpiresAt = newExpires
	return sess, nil
}

// GetActiveSessionByToken eh o lookup do middleware RequireAuth. Aceita o
// plaintext do cookie. Retorna ErrSessionNotFound se hash nao existe,
// ErrSessionExpired se status=active mas expires_at<now, ErrSessionInvalid
// se status != active.
//
// NAO toca em last_used_at — TouchSession faz isso depois pra distinguir
// "olhei a sessao" (no-op) de "renovei" (write).
func (db *DB) GetActiveSessionByToken(plaintext string) (*WebSession, error) {
	hash := HashSessionToken(plaintext)
	sess, err := db.getSessionByHash(hash)
	if err != nil {
		return nil, err
	}
	if sess.Status != "active" {
		return nil, ErrSessionInvalid
	}
	if sess.ExpiresAt.Before(time.Now().UTC()) {
		return nil, ErrSessionExpired
	}
	return sess, nil
}

// TouchSession atualiza last_used_at e estende expires_at em sliding window.
// Chamado pelo middleware apos validar a sessao. Idempotente — se chamado
// 2x no mesmo segundo, simplesmente sobrescreve com now identico.
//
// Retorna erro se a sessao nao existe ou nao esta ativa — caller ja deveria
// ter validado, mas defesa em profundidade.
func (db *DB) TouchSession(sessionID int64) error {
	now := time.Now().UTC()
	expires := now.Add(SessionTTL)
	res, err := db.conn.Exec(`
		UPDATE web_sessions SET
			last_used_at = ?,
			expires_at   = ?
		WHERE id = ? AND status = 'active'`,
		now, expires, sessionID,
	)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// RevokeSession marca uma sessao como revoked. Idempotente — se ja revoked,
// no-op. Usado pelo logout e (futuro) pelo flow de troca de credencial.
func (db *DB) RevokeSession(sessionID int64) error {
	now := time.Now().UTC()
	_, err := db.conn.Exec(`
		UPDATE web_sessions SET
			status     = 'revoked',
			revoked_at = ?
		WHERE id = ?`,
		now, sessionID,
	)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// SetSessionImpersonation grava (ou limpa) o usuario-alvo da impersonacao na
// sessao. targetUserID == nil limpa ("sair da visao"). So muda sessoes ativas
// — uma sessao revogada/expirada nao pode impersonar. A checagem de privilegio
// (dono eh admin?) acontece no handler/middleware; aqui eh persistencia pura.
func (db *DB) SetSessionImpersonation(sessionID int64, targetUserID *int64) error {
	var arg interface{}
	if targetUserID != nil {
		arg = *targetUserID
	}
	res, err := db.conn.Exec(`
		UPDATE web_sessions SET impersonated_user_id = ?
		WHERE id = ? AND status = 'active'`,
		arg, sessionID,
	)
	if err != nil {
		return fmt.Errorf("set session impersonation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// CountRecentLoginAttempts conta tentativas de magic link nas ultimas
// `window` por phone. Usado pelo rate limiter — 3/hora atualmente.
//
// Phone aqui eh sempre o normalizado (so digitos, prefixo 55).
func (db *DB) CountRecentLoginAttempts(phone string, window time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-window)
	var n int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM web_login_attempts
		WHERE phone = ? AND created_at >= ?`,
		phone, cutoff,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count login attempts: %w", err)
	}
	return n, nil
}

// CountRecentLoginAttemptsByIP conta tentativas pelo mesmo IP. Defesa contra
// scanner que enumera phones — 10/hora atualmente.
func (db *DB) CountRecentLoginAttemptsByIP(ip string, window time.Duration) (int, error) {
	if ip == "" {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-window)
	var n int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM web_login_attempts
		WHERE ip = ? AND created_at >= ?`,
		ip, cutoff,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count login attempts by ip: %w", err)
	}
	return n, nil
}

// RecordLoginAttempt grava uma tentativa de magic link. NAO falha o fluxo
// se write falhar — caller loga e segue. Rate limit eh defesa em profundidade
// alem do anti-enumeracao opaco.
func (db *DB) RecordLoginAttempt(phone, ip string) error {
	_, err := db.conn.Exec(`
		INSERT INTO web_login_attempts (phone, ip) VALUES (?, ?)`,
		phone, ip,
	)
	if err != nil {
		return fmt.Errorf("record login attempt: %w", err)
	}
	return nil
}

// getSessionByHash eh o helper compartilhado pelo Activate/Get. Hidrata
// todos os campos.
func (db *DB) getSessionByHash(hash string) (*WebSession, error) {
	var s WebSession
	var activated, lastUsed, revoked sql.NullTime
	var impersonated sql.NullInt64
	err := db.conn.QueryRow(`
		SELECT id, user_id, token_hash, status, expires_at, created_at,
		       activated_at, last_used_at, revoked_at, ip, user_agent,
		       impersonated_user_id
		FROM web_sessions
		WHERE token_hash = ?`,
		hash,
	).Scan(
		&s.ID, &s.UserID, &s.TokenHash, &s.Status, &s.ExpiresAt, &s.CreatedAt,
		&activated, &lastUsed, &revoked, &s.IP, &s.UserAgent,
		&impersonated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query web session: %w", err)
	}
	if impersonated.Valid && impersonated.Int64 > 0 {
		v := impersonated.Int64
		s.ImpersonatedUserID = &v
	}
	if activated.Valid {
		t := activated.Time
		s.ActivatedAt = &t
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		s.LastUsedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		s.RevokedAt = &t
	}
	return &s, nil
}
