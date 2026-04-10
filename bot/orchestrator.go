package main

import (
	"context"
	"encoding/json"
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
	perms         *PermissionManager
	audit         *AuditLog
	sendMsg       func(phone, text string) error
}

func NewOrchestrator(claude *ClaudeClient, cal *CalendarClient, transcription *TranscriptionClient, db *DB, cfg *Config, sendMsg func(phone, text string) error) *Orchestrator {
	o := &Orchestrator{
		claude:        claude,
		cal:           cal,
		transcription: transcription,
		db:            db,
		cfg:           cfg,
		sendMsg:       sendMsg,
	}
	o.confirm = NewConfirmationManager(db, cal, cfg)
	o.perms = NewPermissionManager(db)
	o.audit = NewAuditLog(db)
	return o
}

func (o *Orchestrator) Process(ctx context.Context, user *User, message string) (string, error) {
	// Get conversation history for context
	history, _ := o.db.GetConversationHistory(user.ID, 10)

	// Save user message to history
	o.db.AddConversationMessage(user.ID, "user", message)

	intent, err := o.claude.ExtractIntent(ctx, user.Name, message, history)
	if err != nil {
		return "", fmt.Errorf("extract intent: %w", err)
	}

	log.Printf("[%s] Intent: %s", user.Name, intent.Intent)

	switch intent.Intent {
	case "criar_evento":
		if intent.Data.TargetUser != "" {
			return o.handleCrossUserCreate(ctx, user, intent)
		}
		o.audit.Log(user.ID, "criar_evento", "", intent.Data.Title)
		return o.confirm.CreatePending(user, intent.Data, intent.ConfirmationMessage)

	case "consultar_agenda":
		return o.handleConsulta(ctx, user, intent)

	case "editar_evento":
		return o.handleEditar(ctx, user, intent)

	case "cancelar_evento":
		return o.handleCancelar(ctx, user, intent)

	case "confirmar":
		// Check if there's a pending permission request the target is responding to
		pc, err := o.db.GetPendingConfirmation(user.ID)
		if err == nil {
			var data IntentData
			if jsonErr := json.Unmarshal([]byte(pc.EventData), &data); jsonErr == nil && data.TargetUser != "" {
				// This is a cross-user pending that needs permission — handled via HandlePermissionResponse
			}
		}
		return o.confirm.Confirm(user)

	case "negar":
		o.audit.Log(user.ID, "negar", "", "negou acao pendente")
		return o.confirm.Deny(user)

	case "consultar_log":
		return o.handleConsultarLog(ctx, user, intent)

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

	o.audit.Log(user.ID, "consultar_agenda", "", fmt.Sprintf("%s a %s", intent.Data.StartDate, intent.Data.EndDate))
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

	o.audit.Log(user.ID, "editar_evento", "", ev.Title)
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

	o.audit.Log(user.ID, "cancelar_evento", "", ev.Title)
	return fmt.Sprintf("Evento *%s* cancelado.", ev.Title), nil
}

func (o *Orchestrator) handleCrossUserCreate(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	target, err := o.perms.ResolveByName(intent.Data.TargetUser)
	if err != nil {
		return "", fmt.Errorf("resolve target user: %w", err)
	}
	if target == nil {
		return fmt.Sprintf("Nao encontrei o usuario '%s'.", intent.Data.TargetUser), nil
	}

	canSchedule, err := o.perms.CanScheduleFor(user.ID, target.ID)
	if err != nil {
		return "", fmt.Errorf("check permission: %w", err)
	}

	if !canSchedule {
		// No permission: create a permission request and notify the target
		eventJSON, _ := json.Marshal(intent.Data)
		msgForTarget, err := o.perms.RequestPermission(user, target, string(eventJSON))
		if err != nil {
			return "", fmt.Errorf("request permission: %w", err)
		}
		o.audit.Log(user.ID, "permission_request", target.Name, intent.Data.Title)
		if o.sendMsg != nil {
			o.sendMsg(target.PhoneNumber, msgForTarget)
		}
		return fmt.Sprintf("Pedi permissao a %s para criar o evento. Aguardando resposta.", target.Name), nil
	}

	// Has permission: save pending on the requester's behalf targeting target's calendar
	o.audit.Log(user.ID, "criar_evento", target.Name, intent.Data.Title)
	return o.confirm.CreatePendingForTarget(user, target, intent.Data, intent.ConfirmationMessage)
}

func (o *Orchestrator) handleConsultarLog(ctx context.Context, user *User, intent *IntentResult) (string, error) {
	loc := time.Now().Location()
	startDate, err := time.ParseInLocation("2006-01-02", intent.Data.StartDate, loc)
	if err != nil {
		// Default to last 7 days
		startDate = time.Now().AddDate(0, 0, -7)
	}
	endDate, err := time.ParseInLocation("2006-01-02", intent.Data.EndDate, loc)
	if err != nil {
		endDate = time.Now()
	}
	endDate = endDate.Add(24*time.Hour - time.Second)

	entries, err := o.audit.Query(user.ID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("query audit log: %w", err)
	}

	o.audit.Log(user.ID, "consultar_log", "", fmt.Sprintf("%s a %s", intent.Data.StartDate, intent.Data.EndDate))
	return FormatAuditLog(user.Name, entries), nil
}

// HandlePermissionResponse processes "1"/"2"/"3" responses from a target user
// about a pending cross-user permission request. Returns the reply message or
// empty string if no pending request exists.
func (o *Orchestrator) HandlePermissionResponse(ctx context.Context, user *User, choice string) (string, bool, error) {
	_, err := o.db.GetPendingPermissionRequest(user.ID)
	if err != nil {
		// No pending permission request for this user
		return "", false, nil
	}

	msgToTarget, msgToRequester, requesterPhone, err := o.perms.HandlePermissionResponse(user, choice)
	if err != nil {
		return "", false, fmt.Errorf("handle permission response: %w", err)
	}

	// Notify requester
	if o.sendMsg != nil && requesterPhone != "" && msgToRequester != "" {
		o.sendMsg(requesterPhone, msgToRequester)
	}

	// Log the action
	action := "deny_access"
	switch choice {
	case "1":
		action = "grant_access_once"
	case "2":
		action = "grant_access"
	}
	o.audit.Log(user.ID, action, "", "resposta a solicitacao de acesso")

	return msgToTarget, true, nil
}
