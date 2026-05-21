package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestMyMedications_CreateListDelete(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	// Create.
	body := map[string]any{
		"name":      "Losartana",
		"dose":      "50mg",
		"times":     []string{"08:00"},
		"frequency": "daily",
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/medications", body, withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created MedicationItem
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.ID == 0 || created.Name != "Losartana" {
		t.Fatalf("unexpected created: %+v", created)
	}

	// List.
	rec = doRequest(t, mux, http.MethodGet, "/api/v1/me/medications", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var listResp MedicationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listResp.Medications) != 1 {
		t.Fatalf("expected 1 med, got %d", len(listResp.Medications))
	}

	// Delete.
	path := "/api/v1/me/medications/" + strconv.FormatInt(created.ID, 10)
	rec = doRequest(t, mux, http.MethodDelete, path, nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyMedications_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/medications", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyMedications_TemporaryUntilEcho(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	body := map[string]any{
		"name":      "Amoxicilina",
		"dose":      "500mg",
		"times":     []string{"08:00", "20:00"},
		"frequency": "daily",
		"duration":  map[string]any{"kind": "until", "until": "2099-01-10"},
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/medications", body, withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created MedicationItem
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.EndsAt == nil || *created.EndsAt != "2099-01-10" {
		t.Fatalf("expected ends_at 2099-01-10, got %v", created.EndsAt)
	}
}

func TestMyMedications_InvalidDurationRejected(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	body := map[string]any{
		"name":      "X",
		"times":     []string{"08:00"},
		"frequency": "daily",
		"duration":  map[string]any{"kind": "period", "count": 0, "unit": "days"},
	}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/medications", body, withCookie(cookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
