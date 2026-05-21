package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// =========================================================================
// Conexao com o Google Calendar — titular + dependente
// =========================================================================

func TestMeGoogleConnect_ReturnsURL(t *testing.T) {
	_, store, mux := newTestServer(t)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/google/connect-url", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.URL == "" {
		t.Fatalf("expected non-empty url")
	}
	if len(store.googleConnectFor) != 1 || store.googleConnectFor[0] != u.ID {
		t.Fatalf("expected connect-url issued for titular %d, got %v", u.ID, store.googleConnectFor)
	}
	// Audit registrado com alvo=self.
	if !hasAudit(store, "google_connect_url_issued") {
		t.Fatalf("expected google_connect_url_issued audit")
	}
}

func TestMeGoogleConnect_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/google/connect-url", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMeGoogleConnect_RejectsMissingOrigin(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/google/connect-url", nil,
		withCookie(cookie), withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (CSRF/origin); body=%s", rec.Code, rec.Body.String())
	}
}

func TestDependentGoogleConnect_SendsLinkToDependent(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Fabio", "5511900000001")

	// Cria o dependente — zera o sink do welcome pra isolar o link de conexao.
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents",
		map[string]string{"name": "Joaquim Silva", "phone": "5511900000002", "relationship": "Pai"},
		withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	store.whatsappSent = nil

	path := "/api/v1/family/dependents/" + strconv.FormatInt(created.User.ID, 10) + "/google"
	rec = doRequest(t, mux, http.MethodPost, path, nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("connect status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.whatsappSent) != 1 {
		t.Fatalf("expected 1 connect message, got %d", len(store.whatsappSent))
	}
	sent := store.whatsappSent[0]
	if sent.Phone != "5511900000002" {
		t.Fatalf("connect link sent to wrong phone: %q", sent.Phone)
	}
	// A mensagem leva o nome do dependente e a URL de consentimento.
	for _, want := range []string{"Joaquim", "accounts.google.com", "Zello"} {
		if !contains(sent.Message, want) {
			t.Fatalf("connect message missing %q: %q", want, sent.Message)
		}
	}
	// A URL foi emitida pro DEPENDENTE, nao pro guardiao.
	if len(store.googleConnectFor) != 1 || store.googleConnectFor[0] != created.User.ID {
		t.Fatalf("expected connect-url issued for dependent %d, got %v", created.User.ID, store.googleConnectFor)
	}
}

func TestDependentGoogleConnect_RejectsNonGuardian(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Estranho", "5511900000009")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents/999/google", nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.whatsappSent) != 0 {
		t.Fatalf("expected no message sent to non-guardian, got %d", len(store.whatsappSent))
	}
}

func hasAudit(store *fakeStore, action string) bool {
	for _, a := range store.audits {
		if a.Action == action {
			return true
		}
	}
	return false
}
