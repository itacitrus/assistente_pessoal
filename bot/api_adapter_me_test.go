package main

import (
	"context"
	"testing"
	"time"
)

// testEncKey eh uma chave AES-256 valida (64 hex chars) pra cifrar creds em teste.
const testEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeCal implementa calendarReader pra testes sem OAuth real.
type fakeCal struct {
	events []CalendarEvent
	err    error
	calls  int
}

func (f *fakeCal) ListEvents(_ context.Context, _, _ string, _, _ time.Time) ([]CalendarEvent, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

func (f *fakeCal) AuthURL(state string) string {
	return "https://accounts.google.com/o/oauth2/auth?state=" + state
}

// insertActionLog grava uma linha de action_log com created_at explicito.
func insertActionLog(t *testing.T, db *DB, userID int64, action string, at time.Time) {
	t.Helper()
	_, err := db.conn.Exec(
		`INSERT INTO action_log (user_id, action, target_user, details, created_at) VALUES (?, ?, '', '', ?)`,
		userID, action, at.UTC())
	if err != nil {
		t.Fatalf("insert action_log: %v", err)
	}
}

func TestRecentActivity_OrderLimitLabels(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511988887777", Name: "Maria", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	base := time.Now().UTC()
	// Insere 10 entradas RELEVANTES com timestamps crescentes; esperamos as 8
	// mais recentes.
	for i := 0; i < 10; i++ {
		insertActionLog(t, db, u.ID, "criar_evento", base.Add(time.Duration(i)*time.Minute))
	}
	// Uma acao de RUIDO (consulta) mais recente — deve ser filtrada pelo allowlist.
	insertActionLog(t, db, u.ID, "consultar_agenda", base.Add(20*time.Minute))

	items, err := a.RecentActivity(context.Background(), u.ID, 8)
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	if len(items) != 8 {
		t.Fatalf("len = %d, want 8", len(items))
	}
	// O ruido (consultar_agenda) foi filtrado: o mais recente eh criar_evento.
	if items[0].Action != "criar_evento" {
		t.Fatalf("primeiro action = %q, want criar_evento (ruido filtrado)", items[0].Action)
	}
	// Ordem desc por created_at.
	for i := 1; i < len(items); i++ {
		if items[i-1].At.Before(items[i].At) {
			t.Fatalf("ordem nao desc em %d: %v < %v", i, items[i-1].At, items[i].At)
		}
	}
	// Label conhecido mapeado.
	if items[0].Label != "Criou evento" {
		t.Fatalf("label criar_evento = %q", items[0].Label)
	}
}

func TestRecentActivity_DefaultLimit(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511988886666", Name: "Joao", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	for i := 0; i < 12; i++ {
		insertActionLog(t, db, u.ID, "medication_taken", time.Now().UTC().Add(time.Duration(i)*time.Minute))
	}
	items, err := a.RecentActivity(context.Background(), u.ID, 0) // 0 -> default 8
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	if len(items) != 8 {
		t.Fatalf("len = %d, want 8 (default limit)", len(items))
	}
}

func TestRecentActivity_FiltersNoise(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511988884444", Name: "Carla", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	base := time.Now().UTC()
	// Mistura ruido (consultas/sistema) com acoes relevantes.
	insertActionLog(t, db, u.ID, "consultar_agenda", base.Add(1*time.Minute))
	insertActionLog(t, db, u.ID, "web_login_succeeded", base.Add(2*time.Minute))
	insertActionLog(t, db, u.ID, "criar_evento", base.Add(3*time.Minute))
	insertActionLog(t, db, u.ID, "synthesis_executed", base.Add(4*time.Minute))
	insertActionLog(t, db, u.ID, "medication_taken", base.Add(5*time.Minute))

	items, err := a.ActivityHistory(context.Background(), u.ID, 100)
	if err != nil {
		t.Fatalf("ActivityHistory: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2 relevantes", len(items))
	}
	for _, it := range items {
		if it.Action != "criar_evento" && it.Action != "medication_taken" {
			t.Fatalf("acao de ruido vazou: %q", it.Action)
		}
	}
}

func TestRecentActivity_Empty(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511988885555", Name: "Ana", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	items, err := a.RecentActivity(context.Background(), u.ID, 8)
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestAgendaInsightsData_NoGoogle_OnlyActivity(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511988884444", Name: "Maria Silva", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	now := time.Now().UTC()
	insertActionLog(t, db, u.ID, "criar_evento", now.Add(-1*time.Hour))
	insertActionLog(t, db, u.ID, "criar_evento", now.Add(-2*time.Hour))
	insertActionLog(t, db, u.ID, "editar_evento", now.Add(-3*time.Hour))

	in, err := a.AgendaInsightsData(context.Background(), u.ID, 30)
	if err != nil {
		t.Fatalf("AgendaInsightsData: %v", err)
	}
	if in.GoogleConnected {
		t.Fatal("GoogleConnected = true sem credenciais")
	}
	if in.UserName != "Maria" {
		t.Fatalf("UserName = %q, want Maria (primeiro nome)", in.UserName)
	}
	if in.PeriodDays != 30 {
		t.Fatalf("PeriodDays = %d", in.PeriodDays)
	}
	// activity_counts agregada por acao.
	got := map[string]int{}
	for _, c := range in.ActivityCounts {
		got[c.Action] = c.Count
	}
	if got["criar_evento"] != 2 || got["editar_evento"] != 1 {
		t.Fatalf("counts = %+v", got)
	}
	// Sem google e atividade >= 3 -> tem dado suficiente.
	if !in.HasEnoughData() {
		t.Fatal("HasEnoughData = false, want true (3 acoes)")
	}
}

func TestAgendaEventsToAPI_AllDayAndEnd(t *testing.T) {
	timedStart := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	timedEnd := timedStart.Add(time.Hour)
	allDayStart := time.Date(2026, 5, 23, 0, 0, 0, 0, BRT())
	events := []CalendarEvent{
		{ID: "1", Title: "Reuniao", Start: timedStart, End: timedEnd, Location: "Sala"},
		{ID: "2", Title: "Feriado", Start: allDayStart},
		{ID: "3", Title: "Aniversario", Start: allDayStart, EventType: "birthday"},
	}
	out := agendaEventsToAPI(events)
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].AllDay {
		t.Fatal("evento timed marcado como all_day")
	}
	if out[0].End == nil || !out[0].End.Equal(timedEnd) {
		t.Fatalf("end nao mapeado: %+v", out[0].End)
	}
	if !out[1].AllDay {
		t.Fatal("evento meia-noite BRT deveria ser all_day")
	}
	if out[1].End != nil {
		t.Fatal("evento sem end deveria ter End nil")
	}
	if !out[2].AllDay {
		t.Fatal("aniversario deveria ser all_day")
	}
}

func TestEventsToLite(t *testing.T) {
	timed := time.Date(2026, 5, 22, 9, 30, 0, 0, time.UTC)
	allDay := time.Date(2026, 5, 23, 0, 0, 0, 0, BRT())
	events := []CalendarEvent{
		{ID: "1", Title: "Call", Start: timed, Location: "Sala", Attendees: []string{"x@y.com"}},
		{ID: "2", Title: "Feriado", Start: allDay},
	}
	lite := eventsToLite(events)
	if len(lite) != 2 {
		t.Fatalf("len = %d", len(lite))
	}
	if lite[0].Title != "Call" || lite[0].AllDay {
		t.Fatalf("timed lite incorreto: %+v", lite[0])
	}
	if !lite[1].AllDay {
		t.Fatal("evento meia-noite deveria ser all_day")
	}
}

func TestIsAllDayEvent_ZeroStart(t *testing.T) {
	if isAllDayEvent(CalendarEvent{}) {
		t.Fatal("evento sem start nao deveria ser all_day")
	}
}

func TestActivityLabelPT(t *testing.T) {
	if got := activityLabelPT("criar_evento"); got != "Criou evento" {
		t.Fatalf("label conhecido = %q", got)
	}
	if got := activityLabelPT("xyz_nao_mapeado"); got != "xyz_nao_mapeado" {
		t.Fatalf("fallback = %q", got)
	}
}

// userWithGoogle cria um user com credenciais Google cifradas com testEncKey.
func userWithGoogle(t *testing.T, db *DB, phone, name string) *User {
	t.Helper()
	enc, err := Encrypt("fake-refresh-token", testEncKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	u := &User{
		PhoneNumber:       phone,
		Name:              name,
		Type:              UserTypeComum,
		GoogleCalendarID:  "primary",
		GoogleCredentials: enc,
	}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestUpcomingEvents_NoGoogle_Empty(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := &User{PhoneNumber: "5511977770000", Name: "Sem Google", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	out, err := a.UpcomingEvents(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("UpcomingEvents: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("len = %d, want 0", len(out))
	}
}

func TestUpcomingEvents_GoogleConnected_MapsAndCaps(t *testing.T) {
	a, db, _ := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977771111", "Maria")

	// 12 eventos -> deve capar em agendaUpcomingMax (10).
	var evs []CalendarEvent
	base := time.Now().Add(time.Hour)
	for i := 0; i < 12; i++ {
		s := base.Add(time.Duration(i) * time.Hour)
		evs = append(evs, CalendarEvent{ID: string(rune('a' + i)), Title: "ev", Start: s, End: s.Add(30 * time.Minute)})
	}
	fc := &fakeCal{events: evs}
	a.cal = fc

	out, err := a.UpcomingEvents(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("UpcomingEvents: %v", err)
	}
	if len(out) != agendaUpcomingMax {
		t.Fatalf("len = %d, want %d", len(out), agendaUpcomingMax)
	}
	if fc.calls != 1 {
		t.Fatalf("ListEvents chamado %d vezes", fc.calls)
	}
	if out[0].End == nil {
		t.Fatal("end deveria ser mapeado")
	}
}

func TestAgendaInsightsData_GoogleConnected_FetchesPastAndFuture(t *testing.T) {
	a, db, _ := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977772222", "Joao Pereira")
	fc := &fakeCal{events: []CalendarEvent{
		{ID: "1", Title: "Consulta", Start: time.Now().Add(-24 * time.Hour)},
	}}
	a.cal = fc

	in, err := a.AgendaInsightsData(context.Background(), u.ID, 30)
	if err != nil {
		t.Fatalf("AgendaInsightsData: %v", err)
	}
	if !in.GoogleConnected {
		t.Fatal("GoogleConnected = false")
	}
	if in.UserName != "Joao" {
		t.Fatalf("UserName = %q", in.UserName)
	}
	// Past + future = 2 chamadas ao ListEvents.
	if fc.calls != 2 {
		t.Fatalf("ListEvents chamado %d vezes, want 2", fc.calls)
	}
	if len(in.PastEvents) != 1 || len(in.UpcomingEvents) != 1 {
		t.Fatalf("eventos: past=%d future=%d", len(in.PastEvents), len(in.UpcomingEvents))
	}
	if !in.HasEnoughData() {
		t.Fatal("HasEnoughData = false com eventos")
	}
}
