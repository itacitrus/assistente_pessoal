package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func agendaCreateServer(t *testing.T) (*fakeStore, *http.ServeMux) {
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
	return store, mux
}

func TestAgendaCreate_Sucesso(t *testing.T) {
	store, mux := agendaCreateServer(t)
	u, cookie := loggedInUser(store, "André", "5511977778888")

	body := map[string]any{"title": "Consulta Dr. Elson", "date": "2026-07-28", "time": "15:30", "duration_min": 45, "notify": true}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", body, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var res CreateEventResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Event.Title != "Consulta Dr. Elson" || !res.Notified {
		t.Fatalf("resposta = %+v", res)
	}
	if len(store.createEventCalls) != 1 {
		t.Fatalf("CreateAgendaEvent chamado %d vezes", len(store.createEventCalls))
	}
	call := store.createEventCalls[0]
	if call.UserID != u.ID {
		t.Fatalf("criou para userID %d, esperava o usuário efetivo %d", call.UserID, u.ID)
	}
	if call.In.Date != "2026-07-28" || call.In.Time != "15:30" || call.In.DurationMin != 45 || !call.In.Notify {
		t.Fatalf("input repassado errado: %+v", call.In)
	}
}

func TestAgendaCreate_Unauthenticated_401(t *testing.T) {
	_, mux := agendaCreateServer(t)
	body := map[string]any{"title": "X", "date": "2026-07-28", "time": "15:30"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgendaCreate_SemOrigin_403(t *testing.T) {
	store, mux := agendaCreateServer(t)
	_, cookie := loggedInUser(store, "André", "5511977778888")
	body := map[string]any{"title": "X", "date": "2026-07-28", "time": "15:30"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", body, withCookie(cookie), withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (origin); body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgendaCreate_TituloVazio_400(t *testing.T) {
	store, mux := agendaCreateServer(t)
	_, cookie := loggedInUser(store, "André", "5511977778888")
	body := map[string]any{"title": "   ", "date": "2026-07-28", "time": "15:30"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", body, withCookie(cookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAgendaCreate_DataHoraInvalida_400(t *testing.T) {
	store, mux := agendaCreateServer(t)
	_, cookie := loggedInUser(store, "André", "5511977778888")
	for _, bad := range []map[string]any{
		{"title": "X", "date": "28/07/2026", "time": "15:30"},
		{"title": "X", "date": "2026-07-28", "time": "3pm"},
		{"title": "X", "date": "2026-13-40", "time": "15:30"},
	} {
		rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", bad, withCookie(cookie))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("input %v: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestAgendaCreate_SemGoogle_409(t *testing.T) {
	store, mux := agendaCreateServer(t)
	_, cookie := loggedInUser(store, "André", "5511977778888")
	store.createEventErr = ErrNoCalendar
	body := map[string]any{"title": "X", "date": "2026-07-28", "time": "15:30"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/agenda/events", body, withCookie(cookie))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// GET continua funcionando após o path passar a despachar GET/POST.
func TestAgendaEvents_GET_AindaLista(t *testing.T) {
	store, mux := agendaCreateServer(t)
	_, cookie := loggedInUser(store, "André", "5511977778888")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/me/agenda/events?from=2026-07-01&to=2026-07-31", nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
