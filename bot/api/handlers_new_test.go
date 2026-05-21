package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Onboarding — boas-vindas ao dependente
// =========================================================================

func TestCreateDependent_SendsWelcomeOnce(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Fabio", "5511900000001")

	body := map[string]string{
		"name":         "Joaquim Silva",
		"phone":        "5511900000002",
		"relationship": "Pai",
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/family/dependents", body, withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if len(store.whatsappSent) != 1 {
		t.Fatalf("expected 1 welcome message, got %d", len(store.whatsappSent))
	}
	msg := store.whatsappSent[0].Message
	if store.whatsappSent[0].Phone != "5511900000002" {
		t.Fatalf("welcome sent to wrong phone: %q", store.whatsappSent[0].Phone)
	}
	for _, want := range []string{"Zello", "Joaquim", "Fabio"} {
		if !contains(msg, want) {
			t.Fatalf("welcome message missing %q: %q", want, msg)
		}
	}
	// Audit dependent_welcomed registrado.
	found := false
	for _, a := range store.audits {
		if a.Action == "dependent_welcomed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dependent_welcomed audit")
	}
}

func TestBuildDependentWelcomeMessage_Fallbacks(t *testing.T) {
	// Nomes vazios nao quebram a mensagem.
	msg := buildDependentWelcomeMessage("", "")
	if !contains(msg, "Zello") {
		t.Fatalf("welcome must mention Zello even with empty names: %q", msg)
	}
	if !contains(msg, "Olá!") {
		t.Fatalf("welcome must greet generically when name empty: %q", msg)
	}
}

// =========================================================================
// GET /me/activity
// =========================================================================

func TestMeActivity_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/activity", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeActivity_FiltersAndLimits(t *testing.T) {
	_, store, mux := newTestServer(t)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	now := time.Now().UTC()
	store.activity[u.ID] = []ActivityItem{
		{Action: "criar_evento", Label: "Criou evento", At: now},
		{Action: "consultar_agenda", Label: "Consultou agenda", At: now}, // ruido
		{Action: "medication_taken", Label: "Tomou medicamento", At: now},
		{Action: "web_login_succeeded", Label: "Entrou no painel", At: now}, // ruido
	}
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/activity?limit=100", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp ActivityResponse
	decodeBody(t, rec, &resp)
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2 relevantes; body=%s", len(resp.Items), rec.Body.String())
	}
}

func TestMeActivity_EmptyReturnsArrayNotNull(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/activity", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty activity should be [] not null; body=%s", rec.Body.String())
	}
}

// =========================================================================
// GET /me/profile-facts
// =========================================================================

func TestMeProfileFacts_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/profile-facts", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeProfileFacts_EmptyAvailableFalseAndArrays(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/profile-facts", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp ProfileFactsResponse
	decodeBody(t, rec, &resp)
	if resp.Available {
		t.Fatalf("available should be false when empty")
	}
	body := rec.Body.String()
	for _, key := range []string{`"relations":[]`, `"people":[]`, `"trips":[]`} {
		if !contains(body, key) {
			t.Fatalf("body missing non-null %s: %s", key, body)
		}
	}
}

func TestMeProfileFacts_WithData(t *testing.T) {
	_, store, mux := newTestServer(t)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.profileFacts[u.ID] = ProfileFactsResponse{
		Relations: []RelationFact{{Name: "Fabio", Relation: "Pai", Kind: "dependent"}},
		People:    []PersonFact{{Name: "Dr. Roberto", Detail: "cardiologista"}},
		Trips:     []TripFact{{Label: "Viagem", Destination: "Paris", Start: "2026-06-10", End: "2026-06-20"}},
	}
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/profile-facts", nil, withCookie(cookie))
	var resp ProfileFactsResponse
	decodeBody(t, rec, &resp)
	if !resp.Available {
		t.Fatalf("available should be true with data")
	}
	if len(resp.Relations) != 1 || len(resp.People) != 1 || len(resp.Trips) != 1 {
		t.Fatalf("unexpected counts: %+v", resp)
	}
}

// =========================================================================
// Family medications GET/POST/DELETE
// =========================================================================

func TestDependentMedications_GuardianRequired(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Estranho", "5511900000010")
	dep := store.addUser("Joaquim", "5511900000011")
	// Sem link guardian -> 403.
	rec := doRequest(t, mux, http.MethodGet,
		"/api/v1/family/dependents/"+itoa(dep.ID)+"/medications", nil, withCookie(cookie))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDependentMedications_CreateListDelete(t *testing.T) {
	_, store, mux := newTestServer(t)
	g, cookie := loggedInUser(store, "Fabio", "5511900000020")
	dep := store.addUser("Joaquim", "5511900000021")
	store.addLink(g.ID, dep.ID, "Pai")
	base := "/api/v1/family/dependents/" + itoa(dep.ID) + "/medications"

	// Empty list -> [] not null.
	rec := doRequest(t, mux, http.MethodGet, base, nil, withCookie(cookie))
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"medications":[]`) {
		t.Fatalf("empty list should be []; status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Create.
	body := map[string]any{
		"name":      "Losartana",
		"dose":      "50mg",
		"times":     []string{"08:00", "20:00"},
		"frequency": "daily",
	}
	rec = doRequest(t, mux, http.MethodPost, base, body, withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created MedicationItem
	decodeBody(t, rec, &created)
	if created.Name != "Losartana" || created.ID == 0 {
		t.Fatalf("unexpected created item: %+v", created)
	}

	// List shows it.
	rec = doRequest(t, mux, http.MethodGet, base, nil, withCookie(cookie))
	var list MedicationsResponse
	decodeBody(t, rec, &list)
	if len(list.Medications) != 1 {
		t.Fatalf("list len = %d, want 1", len(list.Medications))
	}

	// Delete.
	rec = doRequest(t, mux, http.MethodDelete, base+"/"+itoa(created.ID), nil, withCookie(cookie))
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Delete non-existent -> 404.
	rec = doRequest(t, mux, http.MethodDelete, base+"/9999", nil, withCookie(cookie))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d, want 404", rec.Code)
	}
}

func TestDependentMedications_Validation(t *testing.T) {
	_, store, mux := newTestServer(t)
	g, cookie := loggedInUser(store, "Fabio", "5511900000030")
	dep := store.addUser("Joaquim", "5511900000031")
	store.addLink(g.ID, dep.ID, "Pai")
	base := "/api/v1/family/dependents/" + itoa(dep.ID) + "/medications"

	cases := []map[string]any{
		{"name": "", "times": []string{"08:00"}, "frequency": "daily"},             // nome vazio
		{"name": "X", "times": []string{}, "frequency": "daily"},                   // sem horarios
		{"name": "X", "times": []string{"25:00"}, "frequency": "daily"},            // hora invalida
		{"name": "X", "times": []string{"08:00"}, "frequency": "monthly"},          // freq invalida
		{"name": "X", "times": []string{"08:00"}, "frequency": "weekly"},           // weekly sem days
		{"name": "X", "times": []string{"08:00"}, "frequency": "weekly", "days": []string{"funday"}}, // dia invalido
	}
	for i, c := range cases {
		rec := doRequest(t, mux, http.MethodPost, base, c, withCookie(cookie))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d: status = %d, want 400; body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestDependentMedications_MethodNotAllowed(t *testing.T) {
	_, store, mux := newTestServer(t)
	g, cookie := loggedInUser(store, "Fabio", "5511900000040")
	dep := store.addUser("Joaquim", "5511900000041")
	store.addLink(g.ID, dep.ID, "Pai")
	base := "/api/v1/family/dependents/" + itoa(dep.ID) + "/medications"

	// PUT na colecao -> 405.
	rec := doRequest(t, mux, http.MethodPut, base, nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT status = %d, want 405", rec.Code)
	}
	// POST no item -> 405.
	rec = doRequest(t, mux, http.MethodPost, base+"/1", nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("item POST status = %d, want 405", rec.Code)
	}
	// medId invalido -> 400.
	rec = doRequest(t, mux, http.MethodDelete, base+"/abc", nil, withCookie(cookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad medId status = %d, want 400", rec.Code)
	}
}

func TestMeProfileFacts_MethodNotAllowed(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999998")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/profile-facts", nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestMeActivity_MethodNotAllowed(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999997")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/activity", nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestValidateCreateMedication_OK(t *testing.T) {
	req := &CreateMedicationRequest{
		Name: "Losartana", Times: []string{"08:00", "20:00"}, Frequency: "weekly",
		Days: []string{"mon", "wed"},
	}
	if msg := validateCreateMedication(req); msg != "" {
		t.Fatalf("expected valid, got %q", msg)
	}
}

func TestIsValidHHMM(t *testing.T) {
	ok := []string{"00:00", "08:00", "23:59", "12:30"}
	bad := []string{"8:00", "24:00", "08:60", "0800", "ab:cd", ""}
	for _, s := range ok {
		if !isValidHHMM(s) {
			t.Fatalf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if isValidHHMM(s) {
			t.Fatalf("%q should be invalid", s)
		}
	}
}

// helpers locais ---

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
}
