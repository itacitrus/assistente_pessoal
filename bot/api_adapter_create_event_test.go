package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// CreateAgendaEvent cria um evento no Google Calendar do titular, localizando
// data+hora no fuso dele e reusando o CreateEvent do CalendarClient. Aviso no
// WhatsApp é opcional e não pode derrubar a criação.

func TestCreateAgendaEvent_LocalizaNoFusoEDuracao(t *testing.T) {
	a, db, sent := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977773333", "André")
	fc := &fakeCal{}
	a.cal = fc

	res, err := a.CreateAgendaEvent(context.Background(), u.ID, api.CreateEventInput{
		Title: "Consulta Dr. Elson", Date: "2026-07-28", Time: "15:30", DurationMin: 45,
	})
	if err != nil {
		t.Fatalf("CreateAgendaEvent: %v", err)
	}
	if fc.created == nil {
		t.Fatal("cal.CreateEvent não foi chamado")
	}
	// Start deve ser 28/07 15:30 no horário de Brasília (UTC-3 → 18:30Z).
	wantStart := time.Date(2026, 7, 28, 15, 30, 0, 0, BRT())
	if !fc.created.Start.Equal(wantStart) {
		t.Fatalf("Start = %v, want %v (15:30 BRT)", fc.created.Start.In(BRT()), wantStart)
	}
	if got := fc.created.End.Sub(fc.created.Start); got != 45*time.Minute {
		t.Fatalf("duração = %v, want 45min", got)
	}
	if fc.created.Title != "Consulta Dr. Elson" {
		t.Fatalf("título = %q", fc.created.Title)
	}
	if res.Event.Title != "Consulta Dr. Elson" {
		t.Fatalf("evento devolvido = %+v", res.Event)
	}
	if res.Notified {
		t.Fatal("Notify=false não deveria avisar")
	}
	if len(*sent) != 0 {
		t.Fatalf("não deveria ter mandado WhatsApp: %v", *sent)
	}
}

func TestCreateAgendaEvent_DuracaoDefault60(t *testing.T) {
	a, db, _ := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977774444", "Maria")
	fc := &fakeCal{}
	a.cal = fc

	if _, err := a.CreateAgendaEvent(context.Background(), u.ID, api.CreateEventInput{
		Title: "Reunião", Date: "2026-08-10", Time: "09:00", DurationMin: 0,
	}); err != nil {
		t.Fatalf("CreateAgendaEvent: %v", err)
	}
	if got := fc.created.End.Sub(fc.created.Start); got != 60*time.Minute {
		t.Fatalf("duração default = %v, want 60min", got)
	}
}

func TestCreateAgendaEvent_NotifyMandaWhatsApp(t *testing.T) {
	a, db, sent := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977775555", "André")
	a.cal = &fakeCal{}

	res, err := a.CreateAgendaEvent(context.Background(), u.ID, api.CreateEventInput{
		Title: "Consulta", Date: "2026-07-28", Time: "15:30", DurationMin: 60, Notify: true,
	})
	if err != nil {
		t.Fatalf("CreateAgendaEvent: %v", err)
	}
	if !res.Notified {
		t.Fatal("Notify=true deveria marcar Notified")
	}
	if len(*sent) != 1 {
		t.Fatalf("esperava 1 WhatsApp enviado, veio %d", len(*sent))
	}
	joined := strings.Join(*sent, "\n")
	if !strings.Contains(joined, u.PhoneNumber) || !strings.Contains(joined, "Consulta") {
		t.Fatalf("mensagem de aviso não menciona titular/evento: %v", *sent)
	}
}

func TestCreateAgendaEvent_SemGoogle_ErrNoCalendar(t *testing.T) {
	a, db, _ := mkAdapter(t)
	a.encKey = testEncKey
	u := &User{PhoneNumber: "5511977776666", Name: "Sem Google", Type: UserTypeComum}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	a.cal = &fakeCal{}
	_, err := a.CreateAgendaEvent(context.Background(), u.ID, api.CreateEventInput{
		Title: "X", Date: "2026-07-28", Time: "15:30",
	})
	if !errors.Is(err, api.ErrNoCalendar) {
		t.Fatalf("err = %v, want api.ErrNoCalendar", err)
	}
}

func TestCreateAgendaEvent_AvisoFalho_NaoDerrubaCriacao(t *testing.T) {
	a, db, _ := mkAdapter(t)
	a.encKey = testEncKey
	u := userWithGoogle(t, db, "5511977777777", "André")
	a.cal = &fakeCal{}
	// sendMsg que falha — a criação do evento deve sobreviver, Notified=false.
	a.sendMsg = func(_, _ string) error { return errors.New("whatsapp offline") }

	res, err := a.CreateAgendaEvent(context.Background(), u.ID, api.CreateEventInput{
		Title: "Consulta", Date: "2026-07-28", Time: "15:30", Notify: true,
	})
	if err != nil {
		t.Fatalf("aviso falho não deveria derrubar a criação: %v", err)
	}
	if res.Notified {
		t.Fatal("aviso falhou — Notified deveria ser false")
	}
	if res.Event.Title != "Consulta" {
		t.Fatal("evento deveria ter sido criado mesmo com aviso falho")
	}
}
