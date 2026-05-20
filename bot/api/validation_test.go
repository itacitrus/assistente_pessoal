package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizePhone_AddsPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"5511999999999", "5511999999999"},
		{"(11) 99999-9999", "5511999999999"},
		{"11999999999", "5511999999999"},
		{"   ", ""},
	}
	for _, c := range cases {
		got := normalizePhone(c.in)
		if got != c.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidBRPhoneAcceptsBoth12And13(t *testing.T) {
	if !validBRPhone("5511999999999") {
		t.Fatal("13 digitos deve passar")
	}
	if !validBRPhone("551199999999") {
		t.Fatal("12 digitos (sem 9) deve passar")
	}
	if validBRPhone("12345") {
		t.Fatal("string curta nao deve passar")
	}
	if validBRPhone("4411999999999") {
		t.Fatal("nao-BR nao deve passar")
	}
}

func TestValidatePreferencesPatchAllChecks(t *testing.T) {
	bad := "x"
	verylong := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghi"
	cases := []struct {
		name string
		in   PreferencesPatch
		want bool // want error
	}{
		{"empty ok", PreferencesPatch{}, false},
		{"name too short", PreferencesPatch{Name: &bad}, true},
		{"name too long", PreferencesPatch{Name: &verylong}, true},
		{"daily bad fmt", PreferencesPatch{DailySummaryTime: ptrStr("9:00")}, true},
		{"daily ok", PreferencesPatch{DailySummaryTime: ptrStr("09:00")}, false},
		{"weekly bad", PreferencesPatch{WeeklySummaryDay: ptrStr("amanha")}, true},
		{"reminder bad", PreferencesPatch{ReminderBefore: ptrStr("2d")}, true},
		{"auto bad", PreferencesPatch{AutoConfirmTimeout: ptrStr("forever")}, true},
		{"thresh too low", PreferencesPatch{InactivityThresholdHours: ptrInt(2)}, true},
		{"thresh too high", PreferencesPatch{InactivityThresholdHours: ptrInt(999)}, true},
		{"thresh ok", PreferencesPatch{InactivityThresholdHours: ptrInt(48)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := validatePreferencesPatch(&c.in)
			if (msg != "") != c.want {
				t.Fatalf("validatePreferencesPatch(%+v) returned %q (wantErr=%v)", c.in, msg, c.want)
			}
		})
	}
}

func TestValidateCreateDependent(t *testing.T) {
	cases := []struct {
		name string
		req  CreateDependentRequest
		want bool
	}{
		{"ok", CreateDependentRequest{Name: "Maria", Phone: "5511999999999", Relationship: "mae"}, false},
		{"short name", CreateDependentRequest{Name: "M", Phone: "5511999999999", Relationship: "mae"}, true},
		{"bad phone", CreateDependentRequest{Name: "Maria", Phone: "abc", Relationship: "mae"}, true},
		{"empty rel", CreateDependentRequest{Name: "Maria", Phone: "5511999999999", Relationship: ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := validateCreateDependent(&c.req)
			if (msg != "") != c.want {
				t.Fatalf("validateCreateDependent(%+v) returned %q (wantErr=%v)", c.req, msg, c.want)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(r *http.Request)
		want    string
	}{
		{
			"X-Forwarded-For single",
			func(r *http.Request) { r.Header.Set("X-Forwarded-For", "1.1.1.1") },
			"1.1.1.1",
		},
		{
			"X-Forwarded-For chain",
			func(r *http.Request) { r.Header.Set("X-Forwarded-For", "2.2.2.2, 3.3.3.3") },
			"2.2.2.2",
		},
		{
			"X-Real-IP",
			func(r *http.Request) { r.Header.Set("X-Real-IP", "9.9.9.9") },
			"9.9.9.9",
		},
		{
			"RemoteAddr fallback",
			func(r *http.Request) { r.RemoteAddr = "8.8.8.8:443" },
			"8.8.8.8",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			c.setup(r)
			got := clientIP(r)
			if got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseDaysQuery(t *testing.T) {
	cases := []struct {
		raw, def, max int
		want          int
	}{}
	_ = cases
	mkR := func(q string) *http.Request {
		return httptest.NewRequest("GET", "/foo?"+q, nil)
	}
	if got := parseDaysQuery(mkR(""), 14, 90); got != 14 {
		t.Fatalf("default = %d, want 14", got)
	}
	if got := parseDaysQuery(mkR("days=abc"), 14, 90); got != 14 {
		t.Fatalf("bad input fallback = %d, want 14", got)
	}
	if got := parseDaysQuery(mkR("days=-3"), 14, 90); got != 14 {
		t.Fatalf("negative fallback = %d, want 14", got)
	}
	if got := parseDaysQuery(mkR("days=200"), 14, 90); got != 90 {
		t.Fatalf("clamp = %d, want 90", got)
	}
	if got := parseDaysQuery(mkR("days=30"), 14, 90); got != 30 {
		t.Fatalf("explicit = %d, want 30", got)
	}
}

func TestStatusCache_TTLExpiration(t *testing.T) {
	c := newStatusCache(10 * time.Millisecond)
	v := &StatusResponse{Days: 7}
	c.Set("k", v)
	if got, ok := c.Get("k"); !ok || got.Days != 7 {
		t.Fatal("set+get sanity falhou")
	}
	time.Sleep(15 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("entry expirada nao foi removida")
	}
}

func TestStatusCache_Invalidate(t *testing.T) {
	c := newStatusCache(time.Hour)
	c.Set("k", &StatusResponse{Days: 7})
	c.Invalidate("k")
	if _, ok := c.Get("k"); ok {
		t.Fatal("Invalidate nao removeu entry")
	}
}

func TestServer_RoutesUnknown(t *testing.T) {
	_, _, mux := newTestServer(t)
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/family/dependents/abc/status", nil)
	// 401 esperado pq RequireAuth pega antes (cookie ausente). Apenas confirmamos
	// que o servidor nao panico.
	if rec.Code == 0 {
		t.Fatal("nenhum status escrito")
	}
}

func TestServer_BadDependentID(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	rec := doRequest(t, mux, http.MethodGet, "/api/v1/family/dependents/abc/status", nil, withCookie(cookie))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestServer_BadLinkID(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	off := false
	rec := doRequest(t, mux, http.MethodPatch, "/api/v1/family/links/abc/notify",
		NotifyPatch{OnMedicationMiss: &off}, withCookie(cookie))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestServer_MethodNotAllowedOnCollection(t *testing.T) {
	_, store, mux := newTestServer(t)
	_, cookie := loggedInUser(store, "Joao", "5511888888888")
	rec := doRequest(t, mux, http.MethodPut, "/api/v1/family/dependents", nil, withCookie(cookie))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// rate limit por IP — dispara 11 requests do mesmo IP variando phones.
// Usa 11 phones BR validos diferentes pra esgotar o IP-bucket sem travar
// no phone-bucket (3/h por phone).
func TestRequestLink_RateLimitByIP(t *testing.T) {
	_, store, mux := newTestServer(t)
	mkPhone := func(i int) string {
		// 5511 9 + 8 digitos. i de 0..10 ocupa 2 digitos no fim. Total = 13.
		hi := byte('0' + (i / 10))
		lo := byte('0' + (i % 10))
		return "551190000" + string([]byte{hi, lo}) + "00"
	}
	for i := 0; i < 10; i++ {
		phone := mkPhone(i)
		store.addUser("U", phone)
		rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
			map[string]string{"phone": phone},
			func(r *http.Request) { r.RemoteAddr = "1.2.3.4:1111" })
		if rec.Code != 200 {
			t.Fatalf("call %d (phone=%s): status = %d body=%s", i, phone, rec.Code, rec.Body.String())
		}
	}
	last := mkPhone(10)
	store.addUser("U2", last)
	rec := doRequest(t, mux, http.MethodPost, "/api/v1/auth/request-link",
		map[string]string{"phone": last},
		func(r *http.Request) { r.RemoteAddr = "1.2.3.4:1111" })
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
}

func ptrInt(v int) *int { return &v }
