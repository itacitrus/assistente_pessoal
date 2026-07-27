package api

import (
	"encoding/base32"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Segredo TOTP conhecido para os testes (base32 do seed RFC).
func testTOTPSecret(t *testing.T) string {
	t.Helper()
	return base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
}

// newAdminLoginServer monta um Server com admin + segredo TOTP configurados.
func newAdminLoginServer(t *testing.T, secret string) (*Server, *fakeStore, *http.ServeMux) {
	t.Helper()
	store := newFakeStore()
	srv := NewServer(Config{
		Store:           store,
		WebBaseURL:      testOrigin,
		AllowedOrigins:  []string{testOrigin},
		CookieSecure:    false,
		AdminPhones:     []string{adminPhone},
		AdminTOTPSecret: secret,
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, store, mux
}

// currentCode gera o código TOTP válido agora, para o segredo dado.
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := generateTOTP(secret, time.Now(), 6)
	if err != nil {
		t.Fatalf("generateTOTP: %v", err)
	}
	return code
}

func TestAdminLogin_Sucesso_SetaCookie(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": currentCode(t, secret)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var setCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName && c.Value != "" {
			setCookie = true
		}
	}
	if !setCookie {
		t.Fatal("login bem-sucedido deveria setar o cookie de sessão")
	}
}

func TestAdminLogin_CodigoErrado_401(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": "000000"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_NaoAdmin_401(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	// Usuário comum tenta com o código TOTP correto — não é admin.
	store.addUser("Maria", "5511988888888")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": "5511988888888", "code": currentCode(t, secret)})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_TOTPDesabilitado_401(t *testing.T) {
	// Sem AdminTOTPSecret configurado, admin-login nunca autentica.
	_, store, mux := newAdminLoginServer(t, "")
	store.addUser("Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": "287082"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_Replay_MesmoCodigoRejeitado(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)
	code := currentCode(t, secret)

	rec1 := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": code})
	if rec1.Code != http.StatusOK {
		t.Fatalf("primeiro login status = %d, want 200; body=%s", rec1.Code, rec1.Body.String())
	}
	// Reuso do mesmo código (mesmo passo de tempo) deve ser rejeitado.
	rec2 := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": code})
	if rec2.Code == http.StatusOK {
		t.Fatal("replay do mesmo código deveria ser rejeitado")
	}
}

func TestAdminLogin_RateLimit_429(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)

	// Estoura o limite por telefone (3/h) com códigos errados.
	for i := 0; i < rateLimitPhonePerHour; i++ {
		doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
			map[string]string{"phone": adminPhone, "code": "000000"})
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": currentCode(t, secret)})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 após estourar o limite; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_SemOrigin_403(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": currentCode(t, secret)}, withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (origin/CSRF); body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminLogin_BodyInvalido_400(t *testing.T) {
	secret := testTOTPSecret(t)
	_, _, mux := newAdminLoginServer(t, secret)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login", "nao-json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// Erro genérico não revela se o telefone é admin nem se o código chegou perto.
func TestAdminLogin_ErroGenerico(t *testing.T) {
	secret := testTOTPSecret(t)
	_, store, mux := newAdminLoginServer(t, secret)
	store.addUser("Rambo", adminPhone)
	store.addUser("Maria", "5511988888888")

	recAdmin := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": adminPhone, "code": "000000"})
	recNao := doRequest(t, mux, http.MethodPost, "/api/v1/auth/admin-login",
		map[string]string{"phone": "5511988888888", "code": "000000"})

	msg := func(rec interface{ Bytes() []byte }) string {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Bytes(), &body)
		return body.Error.Message
	}
	if m1, m2 := msg(recAdmin.Body), msg(recNao.Body); m1 != m2 || m1 == "" {
		t.Fatalf("mensagens diferentes vazam se o telefone é admin: %q vs %q", m1, m2)
	}
}
