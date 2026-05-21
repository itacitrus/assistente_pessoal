package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// newTestServerWithReport monta um Server com ReportClient fake (pra insights)
// e TTLs curtos previsiveis.
func newTestServerWithReport(t *testing.T, report synthesis.ReportClient) (*Server, *fakeStore, *http.ServeMux) {
	t.Helper()
	store := newFakeStore()
	srv := NewServer(Config{
		Store:          store,
		WebBaseURL:     testOrigin,
		AllowedOrigins: []string{testOrigin},
		CookieSecure:   false,
		ReportClient:   report,
	})
	mux := http.NewServeMux()
	srv.Mount(mux)
	return srv, store, mux
}

// =========================================================================
// GET /me/agenda
// =========================================================================

func TestMeAgenda_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/agenda", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMeAgenda_MethodNotAllowed(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda", nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestMeAgenda_MapsEventsAndActivity(t *testing.T) {
	_, store, mux := newTestServer(t)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")

	start := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	store.upcoming[u.ID] = []AgendaEvent{
		{ID: "abc", Title: "Consulta cardiologista", Start: start, End: &end, AllDay: false, Location: "Clinica X"},
	}
	store.activity[u.ID] = []ActivityItem{
		{Action: "criar_evento", Label: "Criou evento", At: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)},
	}

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/agenda", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp AgendaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Upcoming) != 1 || resp.Upcoming[0].ID != "abc" {
		t.Fatalf("upcoming = %+v", resp.Upcoming)
	}
	if resp.Upcoming[0].End == nil || !resp.Upcoming[0].End.Equal(end) {
		t.Fatalf("end nao mapeado: %+v", resp.Upcoming[0].End)
	}
	if len(resp.RecentActivity) != 1 || resp.RecentActivity[0].Label != "Criou evento" {
		t.Fatalf("activity = %+v", resp.RecentActivity)
	}

	// Confirma o contrato JSON exato (nomes de campos).
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, key := range []string{"google_connected", "upcoming", "recent_activity"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("campo %q ausente no JSON", key)
		}
	}
}

func TestMeAgenda_NilSlicesBecomeEmptyArrays(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/agenda", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"upcoming":[]`) || !strings.Contains(body, `"recent_activity":[]`) {
		t.Fatalf("esperava arrays vazios, got: %s", body)
	}
}

func TestMeAgenda_StoreError500(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.upcomingErr = errors.New("calendar down")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/agenda", nil, withCookie(cookie))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// =========================================================================
// GET /me/insights
// =========================================================================

func TestMeInsights_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServerWithReport(t, &fakeReport{})
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeInsights_HappyPath(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "Consulta", Start: time.Now()}},
	}

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp InsightsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Available {
		t.Fatal("available = false, want true")
	}
	if resp.PeriodDays != 30 {
		t.Fatalf("period_days = %d, want 30", resp.PeriodDays)
	}
	if resp.Summary == "" || len(resp.Insights) == 0 {
		t.Fatalf("resp incompleta: %+v", resp)
	}

	// Contrato JSON exato.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, key := range []string{"generated_at", "period_days", "available", "summary", "insights"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("campo %q ausente no JSON", key)
		}
	}
}

func TestMeInsights_CacheAvoidsRegen(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "Consulta", Start: time.Now()}},
	}

	for i := 0; i < 3; i++ {
		rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d status = %d", i, rec.Code)
		}
	}
	if got := report.calls.Load(); got != 1 {
		t.Fatalf("Synthesize chamado %d vezes, want 1 (cache)", got)
	}
}

func TestMeInsights_UnavailableWithLittleData(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	// Sem Google e atividade insuficiente -> available=false, sem chamar Sonnet.
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: false,
		ActivityCounts:  []synthesis.ActivityCount{{Action: "criar_evento", Count: 1}},
	}

	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Available {
		t.Fatal("available = true, want false")
	}
	if resp.Summary == "" {
		t.Fatal("summary deveria explicar a indisponibilidade")
	}
	if len(resp.Insights) != 0 {
		t.Fatalf("insights = %d, want 0", len(resp.Insights))
	}
	if got := report.calls.Load(); got != 0 {
		t.Fatalf("Synthesize chamado %d vezes, want 0 (sem dado)", got)
	}
}

func TestMeInsights_NilReportClientUnavailable(t *testing.T) {
	_, store, mux := newTestServerWithReport(t, nil)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "x", Start: time.Now()}},
	}
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Available {
		t.Fatal("available = true sem ReportClient, want false")
	}
}

func TestMeInsights_ProviderErrorDegrades(t *testing.T) {
	report := &fakeReport{err: errors.New("sonnet down")}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "x", Start: time.Now()}},
	}
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degradado)", rec.Code)
	}
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Available {
		t.Fatal("available = true apos erro do provider, want false")
	}
}

func TestMeInsights_DataStoreError500(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsDataErr = errors.New("db down")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestMeInsights_DaysClampedAndDefault(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "x", Start: time.Now()}},
	}
	// days acima do max (365) deve clampar.
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=9999", nil, withCookie(cookie))
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PeriodDays != 365 {
		t.Fatalf("period_days = %d, want 365 (clamp)", resp.PeriodDays)
	}
}
