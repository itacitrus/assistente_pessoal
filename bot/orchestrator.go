package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

type Orchestrator struct {
	claude        *ClaudeClient
	cal           *CalendarClient
	transcription *TranscriptionClient
	db            *DB
	cfg           *Config
	confirm       *ConfirmationManager
}

func NewOrchestrator(claude *ClaudeClient, cal *CalendarClient, transcription *TranscriptionClient, db *DB, cfg *Config) *Orchestrator {
	o := &Orchestrator{
		claude:        claude,
		cal:           cal,
		transcription: transcription,
		db:            db,
		cfg:           cfg,
	}
	o.confirm = NewConfirmationManager(db, cal, cfg)
	return o
}

func (o *Orchestrator) Process(ctx context.Context, user *User, message string) (string, error) {
	intent, err := o.claude.ExtractIntent(ctx, user.Name, message)
	if err != nil {
		return "", fmt.Errorf("extract intent: %w", err)
	}

	log.Printf("[%s] Intent: %s", user.Name, intent.Intent)

	switch intent.Intent {
	case "criar_evento":
		return o.confirm.CreatePending(user, intent.Data, intent.ConfirmationMessage)

	case "consultar_agenda":
		return o.handleConsulta(ctx, user, intent)

	case "editar_evento":
		return o.handleEditar(ctx, user, intent)

	case "cancelar_evento":
		return o.handleCancelar(ctx, user, intent)

	case "confirmar":
		return o.confirm.Confirm(user)

	case "negar":
		return o.confirm.Deny(user)

	default:
		return intent.ConfirmationMessage, nil
	}
}

func (o *Orchestrator) handleConsulta(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	loc := time.Now().Location()
	startDate, err := time.ParseInLocation("2006-01-02", intent.Data.StartDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse start_date: %w", err)
	}
	endDate, err := time.ParseInLocation("2006-01-02", intent.Data.EndDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse end_date: %w", err)
	}
	endDate = endDate.Add(24*time.Hour - time.Second)

	events, err := o.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("list events: %w", err)
	}

	return FormatEventList(events), nil
}

func (o *Orchestrator) handleEditar(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", err
	}

	ev, err := o.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, intent.Data.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	updated := *ev
	if intent.Data.Title != "" {
		updated.Title = intent.Data.Title
	}
	if intent.Data.Date != "" && intent.Data.Time != "" {
		loc := time.Now().Location()
		newStart, _ := time.ParseInLocation("2006-01-02 15:04", intent.Data.Date+" "+intent.Data.Time, loc)
		duration := ev.End.Sub(ev.Start)
		updated.Start = newStart
		updated.End = newStart.Add(duration)
	}

	if err := o.cal.UpdateEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID, updated); err != nil {
		return "", fmt.Errorf("update event: %w", err)
	}

	return fmt.Sprintf("Evento *%s* atualizado com sucesso!", ev.Title), nil
}

func (o *Orchestrator) handleCancelar(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	refreshToken, err := Decrypt(user.GoogleCredentials, o.cfg.EncryptionKey)
	if err != nil {
		return "", err
	}

	ev, err := o.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, intent.Data.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	if err := o.cal.DeleteEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID); err != nil {
		return "", fmt.Errorf("delete event: %w", err)
	}

	return fmt.Sprintf("Evento *%s* cancelado.", ev.Title), nil
}
