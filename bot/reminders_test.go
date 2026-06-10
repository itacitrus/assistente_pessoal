package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingReminderNotifier registra envios e pode falhar sob demanda.
type recordingReminderNotifier struct {
	mu    sync.Mutex
	sent  []string
	fails int // próximos N envios falham
}

func (n *recordingReminderNotifier) Send(_ context.Context, u *User, msg string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fails > 0 {
		n.fails--
		return errors.New("whatsapp down")
	}
	n.sent = append(n.sent, u.Name+": "+msg)
	return nil
}
func (n *recordingReminderNotifier) Channel() string { return "test" }

func (n *recordingReminderNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sent)
}

func reminderScheduler(t *testing.T, db *DB, n Notifier, now time.Time) *Scheduler {
	t.Helper()
	s := NewScheduler(db, nil, &Config{}, nil, n, nil)
	s.withNowFunc(func() time.Time { return now })
	return s
}

// ---------------------------------------------------------------------------
// Tool agendar_lembrete
// ---------------------------------------------------------------------------

// O gate central do B4: a confirmação só existe porque a row existe.
func TestAgendarLembrete_PersistsBeforeConfirming(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db, audit: NewAuditLog(db)}
	u := mkIdoso(t, db, "Fábio", 0) // sem Google Calendar conectado

	params, _ := json.Marshal(agendarLembreteParams{Text: "aniversário do Clóvis", TimeHHMM: "23:58"})
	out, err := handleAgendarLembrete(context.Background(), a, u, params)
	if err != nil {
		t.Fatalf("handleAgendarLembrete: %v", err)
	}
	if !strings.Contains(out, "23:58") {
		t.Errorf("confirmação deveria ecoar o horário resolvido, got %q", out)
	}
	pend, err := db.ListPendingReminders(u.ID)
	if err != nil || len(pend) != 1 {
		t.Fatalf("esperava 1 lembrete pendente, got %d (%v)", len(pend), err)
	}
	if pend[0].Text != "aniversário do Clóvis" {
		t.Errorf("texto persistido errado: %q", pend[0].Text)
	}
	// O instante resolvido bate com 23:58 no fuso local (inferred: hoje se
	// ainda não passou, amanhã se passou — ambos 23:58 locais).
	local := pend[0].FireAt.In(BRT())
	if local.Format("15:04") != "23:58" {
		t.Errorf("fire_at deveria ser 23:58 local, got %s", local.Format("15:04"))
	}

	// Idempotência: repetir o mesmo pedido não duplica nem erra.
	out2, err := handleAgendarLembrete(context.Background(), a, u, params)
	if err != nil {
		t.Fatalf("repetição deveria ser idempotente, got err=%v", err)
	}
	if pend2, _ := db.ListPendingReminders(u.ID); len(pend2) != 1 {
		t.Fatalf("duplicata pendente não pode existir, got %d", len(pend2))
	}
	_ = out2
}

func TestAgendarLembrete_OffsetMinutes(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	u := mkIdoso(t, db, "Fábio", 0)

	params, _ := json.Marshal(agendarLembreteParams{Text: "tirar o bolo do forno", OffsetMinutes: 20})
	if _, err := handleAgendarLembrete(context.Background(), a, u, params); err != nil {
		t.Fatalf("offset: %v", err)
	}
	pend, _ := db.ListPendingReminders(u.ID)
	if len(pend) != 1 {
		t.Fatalf("esperava 1 pendente, got %d", len(pend))
	}
	d := time.Until(pend[0].FireAt)
	if d < 18*time.Minute || d > 21*time.Minute {
		t.Errorf("fire_at deveria ser ~20min no futuro, got %v", d)
	}
}

func TestAgendarLembrete_EmptyOrUnparseable_AsksUser(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	u := mkIdoso(t, db, "Fábio", 0)

	// Sem texto → pergunta, não erro, nenhuma row.
	params, _ := json.Marshal(agendarLembreteParams{TimeHHMM: "10:00"})
	out, err := handleAgendarLembrete(context.Background(), a, u, params)
	if err != nil || !strings.Contains(out, "?") {
		t.Fatalf("sem texto deveria perguntar, got out=%q err=%v", out, err)
	}
	// Sem horário nem offset → pergunta.
	params, _ = json.Marshal(agendarLembreteParams{Text: "remédio"})
	out, err = handleAgendarLembrete(context.Background(), a, u, params)
	if err != nil || !strings.Contains(out, "?") {
		t.Fatalf("sem horário deveria perguntar, got out=%q err=%v", out, err)
	}
	// Horário implausível → pede de novo, não erro.
	params, _ = json.Marshal(agendarLembreteParams{Text: "remédio", TimeHHMM: "depois"})
	out, err = handleAgendarLembrete(context.Background(), a, u, params)
	if err != nil || !strings.Contains(out, "horário") {
		t.Fatalf("horário inválido deveria pedir esclarecimento, got out=%q err=%v", out, err)
	}
	if pend, _ := db.ListPendingReminders(u.ID); len(pend) != 0 {
		t.Fatalf("nenhuma row pode nascer de pedido incompleto, got %d", len(pend))
	}
}

// O ciclo "me lembra" → "deixa pra lá" → "pensando melhor, me lembra sim".
// Sem o índice parcial, a 3ª etapa colidiria com a row cancelada e o B4
// renasceria com a tool presente.
func TestAgendarLembrete_RecreateAfterCancel(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	u := mkIdoso(t, db, "Fábio", 0)

	params, _ := json.Marshal(agendarLembreteParams{Text: "ligar pro médico", TimeHHMM: "08:00", DateSource: "explicit",
		Date: time.Now().In(BRT()).AddDate(0, 0, 1).Format("2006-01-02")})
	if _, err := handleAgendarLembrete(context.Background(), a, u, params); err != nil {
		t.Fatalf("criar: %v", err)
	}
	pend, _ := db.ListPendingReminders(u.ID)
	if len(pend) != 1 {
		t.Fatalf("esperava 1 pendente, got %d", len(pend))
	}
	if ok, err := db.CancelReminder(u.ID, pend[0].ID); !ok || err != nil {
		t.Fatalf("cancelar: ok=%v err=%v", ok, err)
	}
	// Recriar EXATAMENTE igual tem que funcionar.
	if _, err := handleAgendarLembrete(context.Background(), a, u, params); err != nil {
		t.Fatalf("recriar após cancelar: %v", err)
	}
	pend, _ = db.ListPendingReminders(u.ID)
	if len(pend) != 1 || pend[0].Status != "pending" {
		t.Fatalf("recriação deveria gerar novo pendente, got %+v", pend)
	}
}

// ---------------------------------------------------------------------------
// Scheduler checkAdHocReminders
// ---------------------------------------------------------------------------

func TestCheckAdHocReminders_FiresAtDueTime_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Fábio", 0)
	fireAt := time.Now().Add(-30 * time.Second)
	if _, err := db.CreateReminderIfAbsent(u.ID, "aniversário do Clóvis", fireAt); err != nil {
		t.Fatalf("create: %v", err)
	}
	n := &recordingReminderNotifier{}
	s := reminderScheduler(t, db, n, time.Now())

	s.checkAdHocReminders()
	s.checkAdHocReminders() // 2º tick não pode reenviar
	if n.count() != 1 {
		t.Fatalf("esperava exatamente 1 envio, got %d", n.count())
	}
	if !strings.Contains(n.sent[0], "aniversário do Clóvis") {
		t.Errorf("mensagem deveria conter o texto do lembrete, got %q", n.sent[0])
	}
	// Texto ecoa o horário AGENDADO (não o de envio).
	if !strings.Contains(n.sent[0], fireAt.In(BRT()).Format("15:04")) {
		t.Errorf("mensagem deveria ecoar o horário agendado, got %q", n.sent[0])
	}
	if pend, _ := db.ListPendingReminders(u.ID); len(pend) != 0 {
		t.Fatalf("após envio não pode sobrar pendente, got %d", len(pend))
	}
}

func TestCheckAdHocReminders_ConcurrentTicksSendOnce(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Fábio", 0)
	if _, err := db.CreateReminderIfAbsent(u.ID, "remédio das 22h", time.Now().Add(-10*time.Second)); err != nil {
		t.Fatalf("create: %v", err)
	}
	n := &recordingReminderNotifier{}
	s := reminderScheduler(t, db, n, time.Now())

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.checkAdHocReminders() }()
	}
	wg.Wait()
	if n.count() != 1 {
		t.Fatalf("ticks concorrentes: esperava 1 envio (claim-first), got %d", n.count())
	}
}

func TestCheckAdHocReminders_CatchUpAfterMissedTick(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Fábio", 0)
	// 3 minutos atrasado (deploy às 23:57) → ainda dispara.
	if _, err := db.CreateReminderIfAbsent(u.ID, "aniversário", time.Now().Add(-3*time.Minute)); err != nil {
		t.Fatalf("create: %v", err)
	}
	n := &recordingReminderNotifier{}
	reminderScheduler(t, db, n, time.Now()).checkAdHocReminders()
	if n.count() != 1 {
		t.Fatalf("catch-up dentro da graça deveria disparar, got %d envios", n.count())
	}
}

func TestCheckAdHocReminders_StaleReminderMarkedMissed(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Fábio", 0)
	// 3 horas atrasado (> graça de 2h) → missed, sem envio (não acorda o idoso).
	if _, err := db.CreateReminderIfAbsent(u.ID, "aviso velho", time.Now().Add(-3*time.Hour)); err != nil {
		t.Fatalf("create: %v", err)
	}
	n := &recordingReminderNotifier{}
	reminderScheduler(t, db, n, time.Now()).checkAdHocReminders()
	if n.count() != 0 {
		t.Fatalf("stale não pode disparar, got %d envios", n.count())
	}
	var status string
	if err := db.conn.QueryRow(`SELECT status FROM reminders WHERE user_id=?`, u.ID).Scan(&status); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "missed" {
		t.Fatalf("stale deveria virar 'missed', got %q", status)
	}
}

func TestCheckAdHocReminders_SendFailureRefiresNextTick(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Fábio", 0)
	if _, err := db.CreateReminderIfAbsent(u.ID, "remédio", time.Now().Add(-10*time.Second)); err != nil {
		t.Fatalf("create: %v", err)
	}
	n := &recordingReminderNotifier{fails: 1}
	s := reminderScheduler(t, db, n, time.Now())

	s.checkAdHocReminders() // 1º tick: envio falha → compensação a pending
	if n.count() != 0 {
		t.Fatalf("1º tick deveria ter falhado, got %d envios", n.count())
	}
	if pend, _ := db.ListPendingReminders(u.ID); len(pend) != 1 || pend[0].Attempts != 1 {
		t.Fatalf("falha deveria devolver a pending com attempts=1, got %+v", pend)
	}
	s.checkAdHocReminders() // 2º tick: envia
	if n.count() != 1 {
		t.Fatalf("2º tick deveria entregar, got %d envios (1 entrega total)", n.count())
	}
}

func TestResolveReminderInstant_PastTimeRollsToTomorrow(t *testing.T) {
	loc := BRT()
	now := time.Date(2026, 6, 9, 22, 10, 0, 0, loc)

	// inferred 23:58 às 22:10 → HOJE 23:58.
	res, err := ResolveEventDate(ResolveInput{Source: DateSourceInferred, Time: "23:58", Now: now, Loc: loc})
	if err != nil || !res.Start.Equal(time.Date(2026, 6, 9, 23, 58, 0, 0, loc)) {
		t.Fatalf("23:58@22:10 deveria ser hoje, got %v err=%v", res.Start, err)
	}
	// inferred 08:00 às 22:10 → AMANHÃ 08:00.
	res, err = ResolveEventDate(ResolveInput{Source: DateSourceInferred, Time: "08:00", Now: now, Loc: loc})
	if err != nil || !res.Start.Equal(time.Date(2026, 6, 10, 8, 0, 0, 0, loc)) {
		t.Fatalf("08:00@22:10 deveria rolar pra amanhã, got %v err=%v", res.Start, err)
	}
	// explicit hoje + hora passada → amanhã com AdjustNote.
	res, err = ResolveEventDate(ResolveInput{Source: DateSourceExplicit, ExplicitDate: "2026-06-09", Time: "20:00", Now: now, Loc: loc})
	if err != nil || !res.Adjusted || res.AdjustNote == "" {
		t.Fatalf("explicit passado hoje deveria ajustar com nota, got %+v err=%v", res, err)
	}
}

func TestCancelarLembrete_ByQueryAndAmbiguity(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	u := mkIdoso(t, db, "Fábio", 0)
	mk := func(text string, min int) {
		if _, err := db.CreateReminderIfAbsent(u.ID, text, time.Now().Add(time.Duration(min)*time.Minute)); err != nil {
			t.Fatalf("create %s: %v", text, err)
		}
	}
	mk("ligar pro médico", 30)
	mk("ligar pra farmácia", 60)

	// Query ambígua → lista com ids, nada cancelado.
	params, _ := json.Marshal(cancelarLembreteParams{Query: "ligar"})
	out, err := handleCancelarLembrete(context.Background(), a, u, params)
	if err != nil || !strings.Contains(out, "id:") {
		t.Fatalf("ambíguo deveria listar, got out=%q err=%v", out, err)
	}
	if pend, _ := db.ListPendingReminders(u.ID); len(pend) != 2 {
		t.Fatalf("nada deveria ser cancelado em ambiguidade, got %d", len(pend))
	}

	// Query única → cancela.
	params, _ = json.Marshal(cancelarLembreteParams{Query: "farmácia"})
	out, err = handleCancelarLembrete(context.Background(), a, u, params)
	if err != nil || !strings.Contains(out, "cancelado") {
		t.Fatalf("query única deveria cancelar, got out=%q err=%v", out, err)
	}
	pend, _ := db.ListPendingReminders(u.ID)
	if len(pend) != 1 || pend[0].Text != "ligar pro médico" {
		t.Fatalf("restante errado: %+v", pend)
	}
}

// Garantia cross-feature: promessa lastreada em lembrete pendente passa no
// guard I2a (integração guard ↔ reminders).
func TestGuard_PromiseBackedByPendingReminderPasses(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db, cfg: &Config{OutputGuardMode: "enforce"}}
	u := mkIdoso(t, db, "Fábio", 0)
	loc := BRT()
	fireAt := time.Date(time.Now().Year()+1, 6, 9, 23, 58, 0, 0, loc)
	if _, err := db.CreateReminderIfAbsent(u.ID, "aniversário do Clóvis", fireAt); err != nil {
		t.Fatalf("create: %v", err)
	}
	tc := &TurnContext{Now: time.Now(), Loc: loc, Period: "noite"}
	out, action := a.guardOutput(guardInput{User: u, TC: tc, UserMsg: "vai me lembrar?", Engine: "companion"},
		fmt.Sprintf("Pode deixar, te lembro às %s do aniversário do Clóvis.", fireAt.In(loc).Format("15:04")), nil)
	if action != "none" {
		t.Fatalf("promessa lastreada em reminder pendente é verdadeira, got action=%s out=%q", action, out)
	}
}
