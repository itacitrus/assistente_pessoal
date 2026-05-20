package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testOrigin = "http://localhost:3000"

// newTestServer monta Server + ServeMux pra cada teste. Cookie nao Secure
// porque os tests usam http://, nao https://.
func newTestServer(t *testing.T) (*Server, *fakeStore, *http.ServeMux) {
	t.Helper()
	store := newFakeStore()
	srv := NewServer(Config{
		Store:          store,
		WebBaseURL:     testOrigin,
		AllowedOrigins: []string{testOrigin},
		CookieSecure:   false,
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, store, mux
}

func doRequest(t *testing.T, mux http.Handler, method, path string, body any, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Default Origin pra rotas mutativas — testes podem sobrescrever.
	req.Header.Set("Origin", testOrigin)
	for _, opt := range opts {
		opt(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// loggedInUser cria user + sessao ativa e devolve o cookie pro request.
func loggedInUser(store *fakeStore, name, phone string) (*User, *http.Cookie) {
	u := store.addUser(name, phone)
	id, plaintext, _ := store.CreatePendingSession(nil, u.ID, "", "")
	_ = id
	_, _, _ = store.ActivateSession(nil, plaintext)
	return u, &http.Cookie{Name: CookieName, Value: plaintext}
}

func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(c)
	}
}

func withOrigin(origin string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Origin", origin)
	}
}

func withoutOrigin() func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Del("Origin")
	}
}

// =========================================================================
// auth/request-link
// =========================================================================

func TestRequestLink_PhoneExists_ReturnsOpaque200AndSendsLink(t *testing.T) {
	_, store, mux := newTestServer(t)
	store.addUser("Maria", "5511999999999")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "5511999999999"})

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.magicLinks) != 1 {
		t.Fatalf("expected 1 magic link sent, got %d", len(store.magicLinks))
	}
	if !strings.Contains(store.magicLinks[0].Message, "/auth/verify?token=") {
		t.Fatalf("magic link message lacks verify URL: %q", store.magicLinks[0].Message)
	}
}

func TestRequestLink_PhoneNotExists_ReturnsOpaque200_NoMessage(t *testing.T) {
	_, store, mux := newTestServer(t)

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "5511999999999"})

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(store.magicLinks) != 0 {
		t.Fatalf("expected 0 magic link, got %d (would leak existence)", len(store.magicLinks))
	}
}

func TestRequestLink_RateLimitByPhone(t *testing.T) {
	_, store, mux := newTestServer(t)
	store.addUser("Maria", "5511999999999")

	for i := 0; i < 3; i++ {
		rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
			map[string]string{"phone": "5511999999999"})
		if rec.Code != 200 {
			t.Fatalf("attempt %d: status = %d", i, rec.Code)
		}
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "5511999999999"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th attempt: status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestLink_InvalidPhone(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "abc123"})
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != CodeInvalidPhone {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeInvalidPhone)
	}
}

func TestRequestLink_NormalizesPhoneWithMask(t *testing.T) {
	_, store, mux := newTestServer(t)
	store.addUser("Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "(11) 99999-9999"})
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.magicLinks) != 1 {
		t.Fatalf("expected 1 magic link sent, got %d", len(store.magicLinks))
	}
}

// =========================================================================
// auth/verify
// =========================================================================

func TestVerify_ValidToken_SetsCookieAndReturnsUser(t *testing.T) {
	_, store, mux := newTestServer(t)
	user := store.addUser("Maria", "5511999999999")
	_, plaintext, _ := store.CreatePendingSession(nil, user.ID, "", "")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/verify",
		map[string]string{"token": plaintext})

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != CookieName || cookies[0].Value == "" {
		t.Fatalf("cookie nao foi setado; cookies=%v", cookies)
	}
}

func TestVerify_AlreadyUsedToken_Returns409(t *testing.T) {
	_, store, mux := newTestServer(t)
	user := store.addUser("Maria", "5511999999999")
	_, plaintext, _ := store.CreatePendingSession(nil, user.ID, "", "")
	_, _, _ = store.ActivateSession(nil, plaintext)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/verify",
		map[string]string{"token": plaintext})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestVerify_UnknownToken_Returns400(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/verify",
		map[string]string{"token": "deadbeef"})
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// =========================================================================
// /me
// =========================================================================

func TestMe_NoCookie_Returns401(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMe_BadCookie_Returns401(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil,
		withCookie(&http.Cookie{Name: CookieName, Value: "deadbeef"}))
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMe_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got User
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != user.ID {
		t.Fatalf("returned user mismatch: got=%d want=%d", got.ID, user.ID)
	}
}

// =========================================================================
// CSRF / Origin enforcement
// =========================================================================

func TestRequireOrigin_NoOriginOnPOST_Returns403(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "5511999999999"},
		withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireOrigin_BadOriginOnPOST_Returns403(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": "5511999999999"},
		withOrigin("https://evil.example.com"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireOrigin_GetWithoutOriginIsAllowed(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	// /me eh GET — sem RequireOrigin, mesmo sem header passa.
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil,
		withCookie(cookie),
		withoutOrigin())
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// =========================================================================
// /family/dependents — POST + GET
// =========================================================================

func TestCreateDependent_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents",
		CreateDependentRequest{
			Name:         "Vovo Maria",
			Phone:        "5511777777777",
			Relationship: "mae",
		},
		withCookie(cookie))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp CreateDependentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.User.PhoneNumber != "5511777777777" {
		t.Fatalf("phone mismatch: %v", resp.User.PhoneNumber)
	}
}

func TestCreateDependent_PhoneAlreadyInUse_Returns409(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	store.addUser("Existente", "5511777777777")

	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents",
		CreateDependentRequest{
			Name:         "Vovo Maria",
			Phone:        "5511777777777",
			Relationship: "mae",
		},
		withCookie(cookie))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDependent_ValidationFails(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents",
		CreateDependentRequest{
			Name:         "X",
			Phone:        "abc",
			Relationship: "mae",
		},
		withCookie(cookie))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestListDependents_ReturnsEmpty(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/family/dependents", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Dependents []DependentSummary `json:"dependents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Dependents == nil {
		t.Fatal("dependents nao deveria ser null no JSON")
	}
}

func TestListDependents_AfterCreate(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/family/dependents", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Dependents []DependentSummary `json:"dependents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Dependents) != 1 {
		t.Fatalf("len = %d, want 1", len(resp.Dependents))
	}
}

// =========================================================================
// /family/dependents/{id}/status — auth + cache
// =========================================================================

func TestStatus_NotGuardian_Returns403(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	// NAO criamos link.
	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status"
	rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var env ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != CodeForbidden {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeForbidden)
	}
}

func TestStatus_Guardian_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")

	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status?days=30"
	rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var sr StatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatal(err)
	}
	if sr.Days != 30 {
		t.Fatalf("days = %d, want 30", sr.Days)
	}
}

func TestStatus_ConsentRevoked_Returns403(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")
	store.consents[joinGDKey(guardian.ID, dep.ID)] = "revoked"

	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status"
	rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != CodeConsentRevoked {
		t.Fatalf("code = %q, want %q", env.Error.Code, CodeConsentRevoked)
	}
}

func TestStatus_CacheHitOnSecondCall(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")

	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status?days=30"
	for i := 0; i < 5; i++ {
		rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
		if rec.Code != 200 {
			t.Fatalf("call %d: status %d", i, rec.Code)
		}
	}
	if got := store.synthesizeCalls.Load(); got != 1 {
		t.Fatalf("Synthesize chamado %d vezes, want 1 (cache deveria ter pegado o resto)", got)
	}
}

func TestStatus_CacheKeyIncludesDays(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")

	urlA := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status?days=30"
	urlB := "/api/v1/family/dependents/" + intStr(dep.ID) + "/status?days=14"
	doRequest(t, mux, http.MethodGet, urlA, nil, withCookie(cookie))
	doRequest(t, mux, http.MethodGet, urlB, nil, withCookie(cookie))
	if got := store.synthesizeCalls.Load(); got != 2 {
		t.Fatalf("Synthesize calls = %d, want 2 (different days = different keys)", got)
	}
}

// =========================================================================
// /family/dependents/{id}/timeline
// =========================================================================

func TestTimeline_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")
	store.snapshots[dep.ID] = []SnapshotPoint{
		{Date: "2026-05-01", Humor: 4, Confidence: 4},
		{Date: "2026-05-02", Humor: 0, Confidence: 1}, // baixa confianca permanece
	}

	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/timeline?days=30"
	rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp TimelineResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Snapshots) != 2 {
		t.Fatalf("got %d points, want 2", len(resp.Snapshots))
	}
	if resp.Days != 30 {
		t.Fatalf("days = %d, want 30", resp.Days)
	}
}

func TestTimeline_NotGuardian_Returns403(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	url := "/api/v1/family/dependents/" + intStr(dep.ID) + "/timeline"
	rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// =========================================================================
// PATCH /users/me — preferencias
// =========================================================================

func TestUpdateMe_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	newName := "Maria Atualizada"
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/users/me",
		PreferencesPatch{Name: &newName, DailySummaryTime: ptrStr("08:30")},
		withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got User
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Name != newName {
		t.Fatalf("name nao atualizou: %q", got.Name)
	}
	if got.DailySummaryTime != "08:30" {
		t.Fatalf("daily_summary_time nao atualizou: %q", got.DailySummaryTime)
	}
}

func TestUpdateMe_InvalidTime(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/users/me",
		PreferencesPatch{DailySummaryTime: ptrStr("25:99")},
		withCookie(cookie))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateMe_InvalidWeeklyDay(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/users/me",
		PreferencesPatch{WeeklySummaryDay: ptrStr("segunda")},
		withCookie(cookie))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// =========================================================================
// PATCH /family/links/{id}/notify
// =========================================================================

func TestUpdateNotify_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	link := store.addLink(guardian.ID, dep.ID, "mae")
	url := "/api/v1/family/links/" + intStr(link.ID) + "/notify"
	off := false
	rec := doRequest(t, mux, http.MethodPatch, url,
		NotifyPatch{OnMedicationMiss: &off},
		withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got FamilyLink
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Notify.OnMedicationMiss {
		t.Fatal("OnMedicationMiss nao foi pra false")
	}
}

func TestUpdateNotify_NotOwner_Returns403(t *testing.T) {
	_, store, mux := newTestServer(t)
	otherUser := store.addUser("Outro", "5511666666666")
	dep := store.addUser("Vovo Maria", "5511777777777")
	link := store.addLink(otherUser.ID, dep.ID, "mae")
	_, cookie := loggedInUser(store, "Joao", "5511888888888")

	url := "/api/v1/family/links/" + intStr(link.ID) + "/notify"
	off := false
	rec := doRequest(t, mux, http.MethodPatch, url,
		NotifyPatch{OnMedicationMiss: &off},
		withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// =========================================================================
// PATCH /family/dependents/{id}
// =========================================================================

func TestUpdateDependent_HappyPath(t *testing.T) {
	_, store, mux := newTestServer(t)
	guardian, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	store.addLink(guardian.ID, dep.ID, "mae")
	url := "/api/v1/family/dependents/" + intStr(dep.ID)
	newName := "Maria Atualizada"
	rec := doRequest(t, mux, http.MethodPatch, url,
		DependentPatch{Name: &newName},
		withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got User
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Name != newName {
		t.Fatalf("name nao atualizou: %q", got.Name)
	}
}

func TestUpdateDependent_NotGuardian_Returns403(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	dep := store.addUser("Vovo Maria", "5511777777777")
	url := "/api/v1/family/dependents/" + intStr(dep.ID)
	rec := doRequest(t, mux, http.MethodPatch, url,
		DependentPatch{Name: ptrStr("Foo")},
		withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// =========================================================================
// /auth/logout
// =========================================================================

func TestLogout_RevokesSession(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/logout", nil, withCookie(cookie))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	// Cookie de logout limpo.
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].MaxAge >= 0 {
		t.Fatalf("cookie nao foi expirado: %v", cookies)
	}
	// Cookie original ja nao funciona mais.
	rec2 := doRequest(t, mux, http.MethodGet, "/api/v1/me", nil, withCookie(cookie))
	if rec2.Code != 401 {
		t.Fatalf("apos logout, /me deveria 401; got %d (user=%d)", rec2.Code, user.ID)
	}
}

// =========================================================================
// CORS preflight
// =========================================================================

func TestCORSPreflight(t *testing.T) {
	_, _, mux := newTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/request-link", nil)
	req.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Fatalf("Allow-Origin = %q, want %q", got, testOrigin)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("Allow-Credentials nao foi setado")
	}
}

// =========================================================================
// helpers
// =========================================================================

func ptrStr(s string) *string { return &s }

func intStr(i int64) string {
	// Helper sem pegar dependencias de strconv pra cada chamada.
	return jsonNumber(i)
}

func jsonNumber(i int64) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// Sanity test that fakeStore time.Now references progress (no flakes).
func TestFakeStoreTimeNowProgresses(t *testing.T) {
	t1 := time.Now()
	time.Sleep(time.Microsecond)
	t2 := time.Now()
	if !t2.After(t1) {
		t.Fatal("time nao avanca — teste flako")
	}
}
