package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// waitForInsights aguarda (com timeout) o regen assincrono persistir os
// insights de (userID, days) no fakeStore. Os insights agora sao gerados em
// background — testes consultam a persistencia em vez do retorno imediato.
func waitForInsights(t *testing.T, store *fakeStore, userID int64, days int) *InsightsResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := store.GetUserInsights(context.Background(), userID, days); err == nil {
			return r
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("insights nao persistidos no tempo esperado")
	return nil
}

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
// GET /me/agenda/events?from&to (calendário mensal)
// =========================================================================

func TestMeAgendaEvents_FiltersByRange(t *testing.T) {
	_, store, mux := newTestServer(t)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.upcoming[u.ID] = []AgendaEvent{
		{ID: "in", Title: "Dentro", Start: time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)},
		{ID: "out", Title: "Fora", Start: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)},
	}
	rec := doRequest(t, mux, http.MethodGet,
		"/api/v1/me/agenda/events?from=2026-05-01&to=2026-05-31", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp AgendaEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].ID != "in" {
		t.Fatalf("expected only in-range event, got %+v", resp.Events)
	}
}

func TestMeAgendaEvents_Validation(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	cases := []string{
		"/api/v1/me/agenda/events?from=xx&to=2026-05-31", // from inválido
		"/api/v1/me/agenda/events?from=2026-05-01",       // to ausente
		"/api/v1/me/agenda/events?from=2026-05-31&to=2026-05-01", // to antes de from
		"/api/v1/me/agenda/events?from=2026-01-01&to=2026-12-31", // > 62 dias
	}
	for _, url := range cases {
		rec := doRequest(t, mux, http.MethodGet, url, nil, withCookie(cookie))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", url, rec.Code)
		}
	}
}

func TestMeAgendaEvents_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet,
		"/api/v1/me/agenda/events?from=2026-05-01&to=2026-05-31", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
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

// A geracao agora eh assincrona: 1o GET devolve placeholder pending e dispara
// o regen em background; quando persiste, o GET seguinte serve o resultado.
func TestMeInsights_HappyPath(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "Consulta", Start: time.Now()}},
	}

	// 1a chamada: placeholder pending (sem bloquear).
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var first InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if first.Available || !first.Pending {
		t.Fatalf("1a chamada deveria ser placeholder pending: %+v", first)
	}

	// Espera o regen async persistir e consulta de novo.
	waitForInsights(t, store, u.ID, 30)
	rec = doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
	var resp InsightsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Available {
		t.Fatalf("available = false apos regen, resp=%+v", resp)
	}
	if resp.PeriodDays != 30 || resp.Summary == "" || len(resp.Insights) == 0 {
		t.Fatalf("resp incompleta: %+v", resp)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for _, key := range []string{"generated_at", "period_days", "available", "summary", "insights"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("campo %q ausente no JSON", key)
		}
	}
}

func TestMeInsights_RegenOnceThenServesPersisted(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "Consulta", Start: time.Now()}},
	}

	// 1a chamada dispara 1 regen (async).
	doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
	waitForInsights(t, store, u.ID, 30)

	// Chamadas seguintes servem o persistido (fresco) — sem novo Sonnet.
	for i := 0; i < 3; i++ {
		doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=30", nil, withCookie(cookie))
	}
	if got := report.calls.Load(); got != 1 {
		t.Fatalf("Synthesize chamado %d vezes, want 1", got)
	}
}

func TestMeInsights_UnavailableWithLittleData(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	// Sem Google e atividade insuficiente -> regen persiste available=false sem
	// chamar Sonnet.
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: false,
		ActivityCounts:  []synthesis.ActivityCount{{Action: "criar_evento", Count: 1}},
	}

	doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	resp := waitForInsights(t, store, u.ID, 30)
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
	doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	resp := waitForInsights(t, store, u.ID, 30)
	if resp.Available {
		t.Fatal("available = true sem ReportClient, want false")
	}
}

func TestMeInsights_ProviderErrorStaysPending(t *testing.T) {
	report := &fakeReport{err: errors.New("sonnet down")}
	_, store, mux := newTestServerWithReport(t, report)
	u, cookie := loggedInUser(store, "Maria", "5511999999999")
	store.insightsData[u.ID] = synthesis.AgendaInsightsInput{
		GoogleConnected: true,
		PastEvents:      []synthesis.AgendaEventLite{{Title: "x", Start: time.Now()}},
	}
	// Erro de IA NAO persiste — a UI nunca recebe 500, so o placeholder pending.
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (graceful)", rec.Code)
	}
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Available {
		t.Fatal("available = true apos erro do provider, want false")
	}
	// Nada foi persistido (regen falhou).
	if _, err := store.GetUserInsights(context.Background(), u.ID, 30); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nao deveria persistir em falha de IA, got %v", err)
	}
}

func TestMeInsights_DataGatherErrorStaysGraceful(t *testing.T) {
	report := &fakeReport{}
	_, store, mux := newTestServerWithReport(t, report)
	_, cookie := loggedInUser(store, "Maria", "5511999999999")
	// Falha ao montar dados acontece no regen async — o request NAO deve 500.
	store.insightsDataErr = errors.New("db down")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (falha de gather nao derruba a UI)", rec.Code)
	}
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.Pending {
		t.Fatalf("esperava placeholder pending, got %+v", resp)
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
	// days acima do max (365) deve clampar — o placeholder ja reflete isso.
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/insights?days=9999", nil, withCookie(cookie))
	var resp InsightsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.PeriodDays != 365 {
		t.Fatalf("period_days = %d, want 365 (clamp)", resp.PeriodDays)
	}
	_ = u
}
