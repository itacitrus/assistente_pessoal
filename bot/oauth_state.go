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
// OAuth state — conexao com o Google Calendar (titular + dependentes)
// =========================================================================
//
// O fluxo OAuth do Google passa um parametro `state` que volta intacto no
// callback. Historicamente usavamos o telefone do usuario como state — mas
// telefone eh adivinhavel, o que abria espaco pra um atacante forjar uma URL
// de consentimento amarrada a conta de outra pessoa (CSRF de conexao: a
// agenda do atacante acabaria conectada a conta da vitima).
//
// Aqui o state vira um token opaco de 32 bytes (crypto/rand, hex), de uso
// unico e com expiracao, vinculado no banco ao user alvo. Gravamos apenas o
// sha256(token) — o plaintext vive so na URL (navegador do titular) ou na
// mensagem de WhatsApp (dependente). O callback resgata o token, valida que
// nao expirou nem foi usado, marca como usado e descobre QUAL user deve
// receber as credenciais. Mesmo padrao das sessoes web (vide sessions.go).

// oauthStateTTL eh a validade de um state de conexao com o Google. Generoso
// porque o link tambem viaja por WhatsApp (dependente) e pode ser aberto
// horas depois — single-use + vinculo ao user mantem o risco baixo mesmo
// com janela longa.
const oauthStateTTL = 24 * time.Hour

// Sentinels do resgate de state. Caller (callback OAuth) mapeia pra uma
// pagina de erro amigavel.
var (
	ErrOAuthStateNotFound = errors.New("oauth state not found")
	ErrOAuthStateExpired  = errors.New("oauth state expired")
	ErrOAuthStateUsed     = errors.New("oauth state already used")
)

// hashOAuthState normaliza a hash do token. Usar em todo lookup — plaintext
// nunca toca o WHERE clause direto.
func hashOAuthState(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// CreateOAuthState gera um token opaco de uso unico vinculado a userID e o
// persiste (apenas o hash). Retorna o plaintext, que o caller embute na URL
// de consentimento do Google. ttl define a janela de validade.
func (db *DB) CreateOAuthState(userID int64, ttl time.Duration) (plaintext string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand read: %w", err)
	}
	plaintext = hex.EncodeToString(buf)
	now := time.Now().UTC()
	_, err = db.conn.Exec(`
		INSERT INTO oauth_states (user_id, token_hash, expires_at, created_at)
		VALUES (?, ?, ?, ?)`,
		userID, hashOAuthState(plaintext), now.Add(ttl), now,
	)
	if err != nil {
		return "", fmt.Errorf("insert oauth state: %w", err)
	}
	return plaintext, nil
}

// ConsumeOAuthState resgata um state pelo plaintext: valida que existe, nao
// expirou e nao foi usado, marca como usado e retorna o user alvo. O UPDATE
// condicional (used_at IS NULL) garante atomicidade contra duplo-resgate
// concorrente — so um vencedor marca a row e recebe RowsAffected==1.
func (db *DB) ConsumeOAuthState(plaintext string) (userID int64, err error) {
	hash := hashOAuthState(plaintext)

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var (
		id        int64
		expiresAt time.Time
		usedAt    sql.NullTime
	)
	err = tx.QueryRow(`
		SELECT id, user_id, expires_at, used_at
		FROM oauth_states WHERE token_hash = ?`, hash,
	).Scan(&id, &userID, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrOAuthStateNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("query oauth state: %w", err)
	}
	if usedAt.Valid {
		return 0, ErrOAuthStateUsed
	}
	if expiresAt.Before(time.Now().UTC()) {
		return 0, ErrOAuthStateExpired
	}

	res, err := tx.Exec(`
		UPDATE oauth_states SET used_at = ?
		WHERE id = ? AND used_at IS NULL`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return 0, fmt.Errorf("mark oauth state used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		// Perdeu a corrida — outra requisicao consumiu entre o SELECT e o UPDATE.
		return 0, ErrOAuthStateUsed
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit oauth state: %w", err)
	}
	return userID, nil
}
