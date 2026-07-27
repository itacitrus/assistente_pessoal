package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// fakePairer implementa Pairer para testar os handlers sem whatsmeow.
type fakePairer struct {
	status      PairingStatus
	startMethod string
	startPhone  string
	startErr    error
	resetCalled bool
}

func (f *fakePairer) Start(_ context.Context, method, phone string) (PairingStatus, error) {
	f.startMethod, f.startPhone = method, phone
	if f.startErr != nil {
		return PairingStatus{}, f.startErr
	}
	f.status = PairingStatus{Status: "waiting", Method: method, PairCode: "CODE1234"}
	return f.status, nil
}
func (f *fakePairer) Status() PairingStatus { return f.status }
func (f *fakePairer) Reset(_ context.Context) (PairingStatus, error) {
	f.resetCalled = true
	f.status = PairingStatus{Status: "idle"}
	return f.status, nil
}

func newPairingTestServer(t *testing.T, p Pairer) (*fakeStore, *http.ServeMux) {
	t.Helper()
	store := newFakeStore()
	srv := NewServer(Config{
		Store:          store,
		WebBaseURL:     testOrigin,
		AllowedOrigins: []string{testOrigin},
		CookieSecure:   false,
		AdminPhones:    []string{adminPhone},
		Pairer:         p,
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	return store, mux
}

func TestPairingStatus_NonAdmin_Forbidden(t *testing.T) {
	store, mux := newPairingTestServer(t, &fakePairer{})
	_, cookie := loggedInUser(store, "Maria", "5511988888888")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/admin/pairing/status", nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPairingStatus_Unauthenticated_401(t *testing.T) {
	_, mux := newPairingTestServer(t, &fakePairer{})
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/admin/pairing/status", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPairingStatus_Admin_ReturnsStatus(t *testing.T) {
	p := &fakePairer{status: PairingStatus{Status: "paired", ConnectedAs: "+5561999"}}
	store, mux := newPairingTestServer(t, p)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/admin/pairing/status", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got PairingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "paired" || got.ConnectedAs != "+5561999" {
		t.Fatalf("resposta = %+v", got)
	}
}

func TestPairingStart_Admin_QR(t *testing.T) {
	p := &fakePairer{}
	store, mux := newPairingTestServer(t, p)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/pairing/start",
		map[string]string{"method": "qr"}, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if p.startMethod != "qr" {
		t.Fatalf("Start recebeu method=%q, want qr", p.startMethod)
	}
}

func TestPairingStart_Admin_Phone_PassesNumber(t *testing.T) {
	p := &fakePairer{}
	store, mux := newPairingTestServer(t, p)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/pairing/start",
		map[string]string{"method": "phone", "phone": "5561999887766"}, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if p.startMethod != "phone" || p.startPhone != "5561999887766" {
		t.Fatalf("Start recebeu method=%q phone=%q", p.startMethod, p.startPhone)
	}
}

func TestPairingStart_NonAdmin_Forbidden(t *testing.T) {
	store, mux := newPairingTestServer(t, &fakePairer{})
	_, cookie := loggedInUser(store, "Maria", "5511988888888")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/pairing/start",
		map[string]string{"method": "qr"}, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPairingStart_NoOrigin_Forbidden(t *testing.T) {
	store, mux := newPairingTestServer(t, &fakePairer{})
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	// Sem Origin: RequireOrigin barra POST (CSRF).
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/pairing/start",
		map[string]string{"method": "qr"}, withCookie(cookie), withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (origin); body=%s", rec.Code, rec.Body.String())
	}
}

func TestPairingReset_Admin_CallsReset(t *testing.T) {
	p := &fakePairer{}
	store, mux := newPairingTestServer(t, p)
	_, cookie := loggedInUser(store, "Rambo", adminPhone)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/admin/pairing/reset", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !p.resetCalled {
		t.Fatal("Reset não foi chamado")
	}
}
