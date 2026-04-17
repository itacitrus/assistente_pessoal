package main

import (
	"testing"
	"time"
)

func TestConfirmationFlow(t *testing.T) {
	db := setupTestDB(t)
	encKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	fakeToken, _ := Encrypt("fake-refresh-token", encKey)
	db.CreateUser(&User{
		PhoneNumber:        "5511999999999",
		Name:               "Waldyr",
		GoogleCalendarID:   "waldyr@gmail.com",
		GoogleCredentials:  fakeToken,
		AutoConfirmTimeout: "2h",
	})

	user, _ := db.GetUserByPhone("5511999999999")

	intentData := IntentData{
		Title:           "Reuniao com CEO",
		Date:            "2026-04-11",
		Time:            "15:00",
		DurationMinutes: 60,
	}

	cfg := &Config{EncryptionKey: encKey}
	cm := NewConfirmationManager(db, nil, cfg)

	msg, err := cm.CreatePending(user, intentData, "Agendar Reuniao com CEO para 11/04 as 15h?")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if msg == "" {
		t.Fatal("expected confirmation message")
	}

	pc, err := db.GetPendingConfirmation(user.ID)
	if err != nil {
		t.Fatalf("GetPendingConfirmation failed: %v", err)
	}
	if pc.Status != "pending" {
		t.Fatalf("expected status pending, got %s", pc.Status)
	}

	denyMsg, err := cm.Deny(user)
	if err != nil {
		t.Fatalf("Deny failed: %v", err)
	}
	if denyMsg == "" {
		t.Fatal("expected deny message")
	}

	_, err = db.GetPendingConfirmation(user.ID)
	if err != ErrNoPendingConfirmation {
		t.Fatalf("expected ErrNoPendingConfirmation, got %v", err)
	}
}

func TestAutoConfirmExpiry(t *testing.T) {
	db := setupTestDB(t)

	db.CreateUser(&User{
		PhoneNumber:      "111",
		Name:             "Test",
		GoogleCalendarID: "t@g.com",
		GoogleCredentials: "x",
	})
	user, _ := db.GetUserByPhone("111")

	db.CreatePendingConfirmation(&PendingConfirmation{
		UserID:    user.ID,
		EventData: `{"title":"Test","date":"2026-04-11","time":"10:00","duration_minutes":30}`,
	})

	expired, err := db.GetExpiredPendingConfirmations(user.ID, 0*time.Second)
	if err != nil {
		t.Fatalf("GetExpiredPendingConfirmations failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0].UserName != "Test" {
		t.Fatalf("expected user name Test, got %s", expired[0].UserName)
	}
}

func TestRegressao_BugReuniaoOTC(t *testing.T) {
	// Incidente 16/04/2026 07:02: usuario disse "Reuniao as 9h com OTC".
	// Regra sagrada: 9h > 7h:02 -> HOJE (16/04) 09:00.
	// Bot criou para 18/04 e confirmou "amanha as 9h" -> divergencia tripla.
	// Este teste blinda: com date_source=inferred, o resolver produz a
	// data correta, e o FormatEventCreated aplica rotulo HOJE.
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	incidentNow := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	res, err := ResolveEventDate(ResolveInput{
		Source: DateSourceInferred,
		Time:   "09:00",
		Now:    incidentNow,
		Loc:    brt,
	})
	if err != nil {
		t.Fatalf("resolver falhou: %v", err)
	}
	wantStart := time.Date(2026, 4, 16, 9, 0, 0, 0, brt)
	if !res.Start.Equal(wantStart) {
		t.Fatalf("BUG REINCIDENTE: resolver deu %s, esperava %s (HOJE 09:00)", res.Start, wantStart)
	}
	if res.Adjusted {
		t.Fatalf("Adjusted deveria ser false em inferred")
	}

	// Validar formatacao da narrativa.
	ev := CalendarEvent{
		Title: "Reuniao com OTC",
		Start: res.Start,
		End:   res.Start.Add(time.Hour),
	}
	// A funcao FormatEventCreated usa time.Now() internamente. Validamos
	// relativeDayLabel diretamente com incidentNow injetado.
	label := relativeDayLabel(ev.Start, incidentNow)
	if label != "HOJE" {
		t.Fatalf("BUG NARRATIVO: relativeDayLabel retornou %q, esperava HOJE", label)
	}
}

func TestRegressao_InferredTardeVaiProHoje(t *testing.T) {
	// Caso PM-default: "call as 5h" as 07:02 -> Claude converte pra 17:00,
	// resolver coloca hoje 17:00 (17 > 7:02).
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	res, err := ResolveEventDate(ResolveInput{
		Source: DateSourceInferred,
		Time:   "17:00",
		Now:    now,
		Loc:    brt,
	})
	if err != nil {
		t.Fatalf("resolver falhou: %v", err)
	}
	want := time.Date(2026, 4, 16, 17, 0, 0, 0, brt)
	if !res.Start.Equal(want) {
		t.Fatalf("PM-default path quebrou: deu %s, esperava %s", res.Start, want)
	}
	if relativeDayLabel(res.Start, now) != "HOJE" {
		t.Fatalf("rotulo relativo deveria ser HOJE")
	}
}
