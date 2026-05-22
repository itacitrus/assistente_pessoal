package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

const adminPhone = "5511900000000"

// newAdminTestServer monta um Server com adminPhone no allowlist.
func newAdminTestServer(t *testing.T) (*Server, *fakeStore, *http.ServeMux) {
	t.Helper()
	store := newFakeStore()
	srv := NewServer(Config{
		Store:          store,
		WebBaseURL:     testOrigin,
		AllowedOrigins: []string{testOrigin},
		CookieSecure:   false,
		AdminPhones:    []string{adminPhone},
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, store, mux
}

func TestAdminUsers_NonAdmin_Forbidden(t *testing.T) {
	_, store, mux := newAdminTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511988888888")

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/admin/users", nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminUsers_Admin_ListsAndSearches(t *testing.T) {
	_, store, mux := newAdminTestServer(t)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	store.addUser("Maria Silva", "5511988888888")
	store.addUser("Joao Souza", "5511977777777")

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/admin/users", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp AdminUsersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Users) < 3 {
		t.Fatalf("expected >=3 users (admin + 2), got %d", len(resp.Users))
	}

	// Busca por nome restringe.
	rec = doRequest(t, mux, http.MethodGet, "/api/v1/admin/users?q=maria", nil, withCookie(cookie))
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, u := range resp.Users {
		if u.Name != "Maria Silva" {
			t.Fatalf("search returned non-matching user %q", u.Name)
		}
	}
}

func TestAdminImpersonate_FullCycle(t *testing.T) {
	_, store, mux := newAdminTestServer(t)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	target := store.addUser("Maria Silva", "5511988888888")

	// Liga "ver como".
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/impersonate",
		map[string]int64{"user_id": target.ID}, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("impersonate start status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// /me agora reflete o alvo, mas mantem is_admin e viewing_as.
	rec = doRequest(t, mux, http.MethodGet, "/api/v1/me", nil, withCookie(cookie))
	var me MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.User == nil || me.User.ID != target.ID {
		t.Fatalf("me.id = %v, want target %d", me.User, target.ID)
	}
	if !me.IsAdmin {
		t.Fatal("is_admin should stay true while impersonating")
	}
	if me.ViewingAs == nil || me.ViewingAs.ID != target.ID {
		t.Fatalf("viewing_as = %v, want target %d", me.ViewingAs, target.ID)
	}

	// Desliga.
	rec = doRequest(t, mux, http.MethodDelete, "/api/v1/admin/impersonate", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("impersonate stop status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, mux, http.MethodGet, "/api/v1/me", nil, withCookie(cookie))
	var meAfter MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &meAfter); err != nil {
		t.Fatal(err)
	}
	if meAfter.ViewingAs != nil {
		t.Fatalf("viewing_as should be nil after stop, got %v", meAfter.ViewingAs)
	}
	if meAfter.User.PhoneNumber != adminPhone {
		t.Fatalf("after stop /me should be admin, got %q", meAfter.User.PhoneNumber)
	}
}

func TestAdminImpersonate_NonAdmin_Forbidden(t *testing.T) {
	_, store, mux := newAdminTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511988888888")
	target := store.addUser("Joao", "5511977777777")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/impersonate",
		map[string]int64{"user_id": target.ID}, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// Dupla barreira: mesmo com a coluna de impersonacao setada na sessao de um
// NAO-admin, o RequireAuth nao troca o usuario efetivo.
func TestImpersonation_NonAdminSessionIgnored(t *testing.T) {
	_, store, mux := newAdminTestServer(t)
	maria, cookie := loggedInUser(store, "Maria", "5511988888888")
	victim := store.addUser("Vitima", "5511966666666")

	// Forca a coluna direto no store (simula write malicioso/bug).
	for _, sess := range store.sessions {
		if sess.UserID == maria.ID {
			sess.ImpersonatedUserID = victim.ID
		}
	}

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil, withCookie(cookie))
	var me MeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.User.ID != maria.ID {
		t.Fatalf("non-admin impersonation must be ignored: me.id=%d want %d", me.User.ID, maria.ID)
	}
	if me.IsAdmin || me.ViewingAs != nil {
		t.Fatal("non-admin must not gain admin/viewing_as")
	}
}
