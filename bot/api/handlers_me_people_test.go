package api

import (
	"net/http"
	"net/url"
	"testing"
)

// createPerson eh um helper local que dispara um POST /me/people.
func createPerson(t *testing.T, mux http.Handler, cookie *http.Cookie, name, detail, typ string) {
	t.Helper()
	body := map[string]any{"name": name, "detail": detail, "type": typ}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %q status = %d, want 201; body=%s", name, rec.Code, rec.Body.String())
	}
}

func TestMyPeople_CreateAndShowsInProfileFacts(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	// ProfileFacts do fake le do fixture, nao do store de pessoas — entao
	// validamos o caminho de gravacao via os mapas internos do fake.
	createPerson(t, mux, cookie, "João Victor", "Colega da Octalab", "pessoa")
	createPerson(t, mux, cookie, "Fábio", "Pai", "relacao")

	got := store.personFacts[user.ID]
	if v := got[fakePersonKey("social_context", "João Victor")]; v != "Colega da Octalab" {
		t.Fatalf("pessoa nao gravada como social_context; got=%q", v)
	}
	if v := got[fakePersonKey("relacao", "Fábio")]; v != "Pai" {
		t.Fatalf("relacao nao gravada; got=%q", v)
	}
}

func TestMyPeople_CreateDuplicateConflicts(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	createPerson(t, mux, cookie, "Ana", "Amiga", "pessoa")

	body := map[string]any{"name": "Ana", "detail": "Outra Ana", "type": "pessoa"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyPeople_UpdateRenameMovesKey(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	createPerson(t, mux, cookie, "Jo", "Colega", "pessoa")

	body := map[string]any{
		"name":              "João",
		"detail":            "Colega de trabalho",
		"type":              "pessoa",
		"original_category": "social_context",
		"original_key":      "Jo",
	}
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got := store.personFacts[user.ID]
	if _, ok := got[fakePersonKey("social_context", "Jo")]; ok {
		t.Fatalf("chave antiga 'Jo' deveria ter sumido apos rename")
	}
	if v := got[fakePersonKey("social_context", "João")]; v != "Colega de trabalho" {
		t.Fatalf("chave nova 'João' nao tem o valor esperado; got=%q", v)
	}
}

func TestMyPeople_UpdateChangeTypeMovesCategory(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	createPerson(t, mux, cookie, "Tio Bob", "Conhecido", "pessoa")

	// Promove de pessoa (social_context) para relacao.
	body := map[string]any{
		"name":              "Tio Bob",
		"detail":            "Tio",
		"type":              "relacao",
		"original_category": "social_context",
		"original_key":      "Tio Bob",
	}
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	got := store.personFacts[user.ID]
	if _, ok := got[fakePersonKey("social_context", "Tio Bob")]; ok {
		t.Fatalf("entrada deveria ter saido de social_context")
	}
	if v := got[fakePersonKey("relacao", "Tio Bob")]; v != "Tio" {
		t.Fatalf("entrada deveria virar relacao; got=%q", v)
	}
}

func TestMyPeople_UpdateMissingReturns404(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	body := map[string]any{
		"name":              "Fantasma",
		"detail":            "x",
		"type":              "pessoa",
		"original_category": "social_context",
		"original_key":      "Inexistente",
	}
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update inexistente status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyPeople_Delete(t *testing.T) {
	_, store, mux := newTestServer(t)
	user, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	createPerson(t, mux, cookie, "Carlos", "Vizinho", "pessoa")

	qs := url.Values{"category": {"social_context"}, "key": {"Carlos"}}
	rec := doRequest(t, mux, http.MethodDelete, "/api/v1/me/people?"+qs.Encode(), nil, withCookie(cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := store.personFacts[user.ID][fakePersonKey("social_context", "Carlos")]; ok {
		t.Fatalf("entrada deveria ter sido removida")
	}
}

func TestMyPeople_EmptyNameRejected(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	body := map[string]any{"name": "   ", "detail": "x", "type": "pessoa"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyPeople_BadTypeRejected(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")

	body := map[string]any{"name": "Ana", "detail": "x", "type": "outro"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body, withCookie(cookie))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyPeople_RequiresAuth(t *testing.T) {
	_, _, mux := newTestServer(t)
	body := map[string]any{"name": "Ana", "detail": "x", "type": "pessoa"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMyPeople_RequiresOrigin(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Giovanni", "5511988887777")
	body := map[string]any{"name": "Ana", "detail": "x", "type": "pessoa"}
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/me/people", body, withCookie(cookie), withoutOrigin())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}
