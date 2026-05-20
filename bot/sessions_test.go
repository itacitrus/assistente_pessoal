package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Fase 2 (web/UI) — sessions.go tests
// =========================================================================
//
// Foca nas regras de borda: token plaintext nunca persistido, status
// transitions valido pending->active->revoked, expirado vira erro proprio,
// rate limit eh contagem fiel.

func TestGenerateSessionTokenIsHexAnd64Chars(t *testing.T) {
	plaintext, hash, err := generateSessionToken()
	if err != nil {
		t.Fatalf("generateSessionToken: %v", err)
	}
	if len(plaintext) != 64 {
		t.Fatalf("plaintext len = %d, want 64", len(plaintext))
	}
	// hex valida.
	for _, c := range plaintext {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("plaintext nao eh hex lowercase: %q", plaintext)
		}
	}
	if len(hash) != 64 {
		t.Fatalf("hash len = %d, want 64", len(hash))
	}
	if plaintext == hash {
		t.Fatal("plaintext nao deveria igualar hash")
	}
	// Determinismo do hash: HashSessionToken(plaintext) == hash.
	if HashSessionToken(plaintext) != hash {
		t.Fatal("HashSessionToken nao eh deterministico")
	}
}

func TestGenerateSessionTokenIsRandom(t *testing.T) {
	seen := make(map[string]bool, 10)
	for i := 0; i < 10; i++ {
		p, _, err := generateSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatal("token repetido em 10 chamadas")
		}
		seen[p] = true
	}
}

func TestCreatePendingSessionPersistsHashNotPlaintext(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Maria")
	sess, plaintext, err := db.CreatePendingSession(users[0].ID, "127.0.0.1", "ua")
	if err != nil {
		t.Fatalf("CreatePendingSession: %v", err)
	}
	if sess.Status != "pending" {
		t.Fatalf("status = %q, want pending", sess.Status)
	}
	if sess.TokenHash == plaintext {
		t.Fatal("hash gravado igual ao plaintext")
	}
	// Validar que o plaintext nao aparece no banco.
	var count int
	err = db.conn.QueryRow(`SELECT COUNT(*) FROM web_sessions WHERE token_hash = ?`, plaintext).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("plaintext encontrado em token_hash — vazamento de credencial")
	}
	// E o hash precisa achar a row.
	err = db.conn.QueryRow(`SELECT COUNT(*) FROM web_sessions WHERE token_hash = ?`, sess.TokenHash).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("hash nao achou a row: count=%d", count)
	}
}

func TestActivateSessionHappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.ActivateSession(plaintext)
	if err != nil {
		t.Fatalf("ActivateSession: %v", err)
	}
	if sess.Status != "active" {
		t.Fatalf("status = %q, want active", sess.Status)
	}
	if sess.ActivatedAt == nil {
		t.Fatal("ActivatedAt nao foi preenchido")
	}
	if sess.ExpiresAt.Before(time.Now().Add(29 * 24 * time.Hour)) {
		t.Fatalf("expires_at deveria ser ~30d, got %v", sess.ExpiresAt)
	}
}

func TestActivateSessionRejectsAlreadyUsed(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateSession(plaintext); err != nil {
		t.Fatal(err)
	}
	// Segunda chamada deve falhar.
	_, err = db.ActivateSession(plaintext)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestActivateSessionRejectsUnknownToken(t *testing.T) {
	db := setupTestDB(t)
	_, err := db.ActivateSession("definitelynotatoken")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestActivateSessionRejectsExpired(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	sess, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Forca expiracao.
	_, err = db.conn.Exec(`UPDATE web_sessions SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-1*time.Minute), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ActivateSession(plaintext)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

func TestGetActiveSessionByTokenAndTouch(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateSession(plaintext); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetActiveSessionByToken(plaintext)
	if err != nil {
		t.Fatalf("GetActiveSessionByToken: %v", err)
	}
	if got.UserID != users[0].ID {
		t.Fatalf("user_id mismatch")
	}
	// Touch — sliding window. Nao testamos a precisao de timestamp,
	// so que nao retorna erro e que esta perto de "agora + 30d".
	if err := db.TouchSession(got.ID); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	// Re-le pra confirmar last_used_at != nil.
	post, err := db.GetActiveSessionByToken(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if post.LastUsedAt == nil {
		t.Fatal("LastUsedAt nao foi atualizado")
	}
}

func TestGetActiveSessionByTokenRejectsRevoked(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.ActivateSession(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	_, err = db.GetActiveSessionByToken(plaintext)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}

func TestGetActiveSessionByTokenRejectsExpired(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.ActivateSession(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.conn.Exec(`UPDATE web_sessions SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.GetActiveSessionByToken(plaintext)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

func TestRevokeSessionIsIdempotent(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Joao")
	_, plaintext, err := db.CreatePendingSession(users[0].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.ActivateSession(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	// Segunda chamada nao retorna erro.
	if err := db.RevokeSession(sess.ID); err != nil {
		t.Fatalf("RevokeSession (2nd) = %v, want nil", err)
	}
}

func TestCountRecentLoginAttempts(t *testing.T) {
	db := setupTestDB(t)
	phone := "5511999999999"
	for i := 0; i < 4; i++ {
		if err := db.RecordLoginAttempt(phone, "1.1.1.1"); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.CountRecentLoginAttempts(phone, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("count = %d, want 4", n)
	}
	// Janela curta (-2h, mas attempt eh agora) ainda conta.
	// Janela "futuro" — ainda da pra comparar; cutoff = now-1ns ja exclui tudo.
	n, err = db.CountRecentLoginAttempts(phone, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count com janela 1ns = %d, want 0", n)
	}
}

func TestCountRecentLoginAttemptsByIPSeparatedFromPhone(t *testing.T) {
	db := setupTestDB(t)
	if err := db.RecordLoginAttempt("5511aaaa", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordLoginAttempt("5511bbbb", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordLoginAttempt("5511aaaa", "9.9.9.9"); err != nil {
		t.Fatal(err)
	}
	n, err := db.CountRecentLoginAttemptsByIP("1.1.1.1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count by ip = %d, want 2", n)
	}
}

func TestCountRecentLoginAttemptsByEmptyIP(t *testing.T) {
	db := setupTestDB(t)
	n, err := db.CountRecentLoginAttemptsByIP("", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count for empty ip = %d, want 0", n)
	}
}

func TestHashSessionTokenStable(t *testing.T) {
	if HashSessionToken("abc") != HashSessionToken("abc") {
		t.Fatal("HashSessionToken nao eh estavel")
	}
	if HashSessionToken("abc") == HashSessionToken("abd") {
		t.Fatal("HashSessionToken nao colide pra inputs distintos")
	}
	// Garante hex lowercase 64 chars (sha256).
	out := HashSessionToken("foo")
	if len(out) != 64 {
		t.Fatalf("hash len = %d, want 64", len(out))
	}
	if strings.ToLower(out) != out {
		t.Fatalf("hash nao eh lowercase: %q", out)
	}
}
