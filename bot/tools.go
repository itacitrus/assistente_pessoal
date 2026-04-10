package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

var toolHandlers = map[string]ToolHandler{
	"buscar_agenda":              handleBuscarAgenda,
	"criar_evento":               handleCriarEvento,
	"editar_evento":              handleEditarEvento,
	"cancelar_evento":            handleCancelarEvento,
	"buscar_historico":           handleBuscarHistorico,
	"criar_evento_outro_usuario": handleCriarEventoOutroUsuario,
	"gerar_link_meet":            handleGerarLinkMeet,
	"convidar_externo":           handleConvidarExterno,
	"salvar_memoria":             handleSalvarMemoria,
	"buscar_memoria":             handleBuscarMemoria,
}

type buscarAgendaParams struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func handleBuscarAgenda(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p buscarAgendaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	loc := time.Now().Location()
	startDate, err := time.ParseInLocation("2006-01-02", p.StartDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse start_date: %w", err)
	}
	endDate, err := time.ParseInLocation("2006-01-02", p.EndDate, loc)
	if err != nil {
		return "", fmt.Errorf("parse end_date: %w", err)
	}
	endDate = endDate.Add(24*time.Hour - time.Second)

	events, err := agent.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, startDate, endDate)
	if err != nil {
		return "", fmt.Errorf("list events: %w", err)
	}

	agent.audit.Log(user.ID, "consultar_agenda", "", fmt.Sprintf("%s a %s", p.StartDate, p.EndDate))
	return FormatEventList(events), nil
}

type criarEventoParams struct {
	Title           string `json:"title"`
	Date            string `json:"date"`
	Time            string `json:"time"`
	DurationMinutes int    `json:"duration_minutes"`
	Location        string `json:"location"`
	ComMeet         bool   `json:"com_meet"`
}

func handleCriarEvento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p criarEventoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	loc := time.Now().Location()
	startTime, err := time.ParseInLocation("2006-01-02 15:04", p.Date+" "+p.Time, loc)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}

	duration := time.Duration(p.DurationMinutes) * time.Minute
	if p.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}

	ev := CalendarEvent{
		Title:    p.Title,
		Location: p.Location,
		Start:    startTime,
		End:      startTime.Add(duration),
	}
	if p.ComMeet {
		ev.MeetLink = "generate"
	}

	created, err := agent.cal.CreateEvent(ctx, refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		return "", fmt.Errorf("create event: %w", err)
	}

	agent.audit.Log(user.ID, "criar_evento", "", p.Title)
	result := FormatEventCreated(*created)
	if created.MeetLink != "" {
		result += fmt.Sprintf("\nLink do Meet: %s", created.MeetLink)
	}
	return result, nil
}

type editarEventoParams struct {
	SearchQuery     string `json:"search_query"`
	NewTitle        string `json:"new_title"`
	NewDate         string `json:"new_date"`
	NewTime         string `json:"new_time"`
	NewDurationMins int    `json:"new_duration_minutes"`
	NewLocation     string `json:"new_location"`
}

func handleEditarEvento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p editarEventoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	ev, err := agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	updated := *ev
	if p.NewTitle != "" {
		updated.Title = p.NewTitle
	}
	loc := time.Now().Location()
	if p.NewDate != "" || p.NewTime != "" {
		// Keep existing date/time if only one is provided
		dateStr := ev.Start.Format("2006-01-02")
		timeStr := ev.Start.Format("15:04")
		if p.NewDate != "" {
			dateStr = p.NewDate
		}
		if p.NewTime != "" {
			timeStr = p.NewTime
		}
		newStart, parseErr := time.ParseInLocation("2006-01-02 15:04", dateStr+" "+timeStr, loc)
		if parseErr == nil {
			duration := ev.End.Sub(ev.Start)
			updated.Start = newStart
			updated.End = newStart.Add(duration)
		}
	}
	if p.NewDurationMins > 0 {
		updated.End = updated.Start.Add(time.Duration(p.NewDurationMins) * time.Minute)
	}
	if p.NewLocation != "" {
		updated.Location = p.NewLocation
	}

	if err := agent.cal.UpdateEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID, updated); err != nil {
		return "", fmt.Errorf("update event: %w", err)
	}

	agent.audit.Log(user.ID, "editar_evento", "", ev.Title)
	return fmt.Sprintf("Evento *%s* atualizado com sucesso!", ev.Title), nil
}

type cancelarEventoParams struct {
	SearchQuery string `json:"search_query"`
}

func handleCancelarEvento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cancelarEventoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	ev, err := agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	if err := agent.cal.DeleteEvent(ctx, refreshToken, user.GoogleCalendarID, ev.ID); err != nil {
		return "", fmt.Errorf("delete event: %w", err)
	}

	agent.audit.Log(user.ID, "cancelar_evento", "", ev.Title)
	return fmt.Sprintf("Evento *%s* cancelado.", ev.Title), nil
}

type buscarHistoricoParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func handleBuscarHistorico(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p buscarHistoricoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if p.Limit == 0 {
		p.Limit = 10
	}

	msgs, err := agent.db.SearchConversationHistory(user.ID, p.Query, p.Limit)
	if err != nil {
		return "", fmt.Errorf("search history: %w", err)
	}

	if len(msgs) == 0 {
		return "Nenhuma mensagem encontrada no historico.", nil
	}

	var sb strings.Builder
	for _, m := range msgs {
		ts := m.CreatedAt.Format("02/01 15:04")
		role := "Usuario"
		if m.Role == "assistant" {
			role = "Assistente"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", ts, role, m.Content))
	}
	return sb.String(), nil
}

type criarEventoOutroUsuarioParams struct {
	TargetUser      string `json:"target_user"`
	Title           string `json:"title"`
	Date            string `json:"date"`
	Time            string `json:"time"`
	DurationMinutes int    `json:"duration_minutes"`
	Location        string `json:"location"`
}

func handleCriarEventoOutroUsuario(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p criarEventoOutroUsuarioParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	target, err := agent.perms.ResolveByName(p.TargetUser)
	if err != nil {
		return "", fmt.Errorf("resolve target user: %w", err)
	}
	if target == nil {
		return fmt.Sprintf("Nao encontrei o usuario '%s'.", p.TargetUser), nil
	}

	canSchedule, err := agent.perms.CanScheduleFor(user.ID, target.ID)
	if err != nil {
		return "", fmt.Errorf("check permission: %w", err)
	}

	if !canSchedule {
		// No permission: create a permission request and notify the target
		eventData := IntentData{
			Title:           p.Title,
			Date:            p.Date,
			Time:            p.Time,
			DurationMinutes: p.DurationMinutes,
			Location:        p.Location,
			TargetUser:      target.Name,
		}
		eventJSON, _ := json.Marshal(eventData)
		msgForTarget, err := agent.perms.RequestPermission(user, target, string(eventJSON))
		if err != nil {
			return "", fmt.Errorf("request permission: %w", err)
		}
		agent.audit.Log(user.ID, "permission_request", target.Name, p.Title)
		if agent.sendMsg != nil {
			agent.sendMsg(target.PhoneNumber, msgForTarget)
		}
		return fmt.Sprintf("Pedi permissao a %s para criar o evento. Aguardando resposta.", target.Name), nil
	}

	// Has permission: create event on target's calendar
	targetToken, err := Decrypt(target.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt target credentials: %w", err)
	}

	loc := time.Now().Location()
	startTime, err := time.ParseInLocation("2006-01-02 15:04", p.Date+" "+p.Time, loc)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}

	duration := time.Duration(p.DurationMinutes) * time.Minute
	if p.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}

	ev := CalendarEvent{
		Title:    p.Title,
		Location: p.Location,
		Start:    startTime,
		End:      startTime.Add(duration),
	}

	created, err := agent.cal.CreateEvent(ctx, targetToken, target.GoogleCalendarID, ev)
	if err != nil {
		return "", fmt.Errorf("create event on target calendar: %w", err)
	}

	agent.audit.Log(user.ID, "criar_evento", target.Name, p.Title)
	log.Printf("[%s] Created event on %s's calendar: %s", user.Name, target.Name, p.Title)
	return fmt.Sprintf("Evento criado na agenda de %s: %s", target.Name, FormatEventCreated(*created)), nil
}

type gerarLinkMeetParams struct {
	SearchQuery string `json:"search_query"`
}

func handleGerarLinkMeet(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p gerarLinkMeetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	ev, err := agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	meetLink, err := agent.cal.AddMeetLink(ctx, refreshToken, user.GoogleCalendarID, ev.ID)
	if err != nil {
		return "", fmt.Errorf("add meet link: %w", err)
	}

	agent.audit.Log(user.ID, "gerar_meet", "", ev.Title)
	return fmt.Sprintf("Link do Meet para *%s*: %s", ev.Title, meetLink), nil
}

type convidarExternoParams struct {
	Phone       string `json:"phone"`
	Name        string `json:"name"`
	EventTitle  string `json:"event_title"`
	EventDate   string `json:"event_date"`
	EventTime   string `json:"event_time"`
	MeetLink    string `json:"meet_link"`
	Location    string `json:"location"`
}

func handleConvidarExterno(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p convidarExternoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	// Normalize phone number (add 55 if needed)
	phone := strings.ReplaceAll(p.Phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	phone = strings.ReplaceAll(phone, "+", "")
	if !strings.HasPrefix(phone, "55") {
		phone = "55" + phone
	}

	// Build invite message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Ola, %s! Sou o assistente da Itacitrus.\n\n", p.Name))
	sb.WriteString(fmt.Sprintf("*%s* te convidou para:\n", user.Name))
	sb.WriteString(fmt.Sprintf("*%s*\n", p.EventTitle))
	sb.WriteString(fmt.Sprintf("Data: %s as %s\n", p.EventDate, p.EventTime))
	if p.Location != "" {
		sb.WriteString(fmt.Sprintf("Local: %s\n", p.Location))
	}
	if p.MeetLink != "" {
		sb.WriteString(fmt.Sprintf("\nLink da reuniao: %s\n", p.MeetLink))
	}
	sb.WriteString("\nQualquer duvida, fale diretamente com " + user.Name + ".")

	if agent.sendMsg == nil {
		return "Erro: nao consigo enviar mensagens no momento.", nil
	}

	err := agent.sendMsg(phone, sb.String())
	if err != nil {
		return "", fmt.Errorf("send invite: %w", err)
	}

	agent.audit.Log(user.ID, "convidar_externo", p.Name, p.EventTitle)
	log.Printf("[%s] Sent invite to %s (%s) for %s", user.Name, p.Name, phone, p.EventTitle)
	return fmt.Sprintf("Convite enviado para %s (%s) via WhatsApp.", p.Name, p.Phone), nil
}

type salvarMemoriaParams struct {
	Category string `json:"category"`
	Key      string `json:"key"`
	Value    string `json:"value"`
}

func handleSalvarMemoria(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p salvarMemoriaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if err := agent.db.SaveMemory(user.ID, p.Category, p.Key, p.Value); err != nil {
		return "", fmt.Errorf("save memory: %w", err)
	}

	log.Printf("[%s] Saved memory: %s/%s = %s", user.Name, p.Category, p.Key, p.Value)
	return fmt.Sprintf("Anotado: %s -> %s", p.Key, p.Value), nil
}

type buscarMemoriaParams struct {
	Query    string `json:"query"`
	Category string `json:"category"`
}

func handleBuscarMemoria(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p buscarMemoriaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	var mems []UserMemory
	var err error
	if p.Query != "" {
		mems, err = agent.db.SearchMemories(user.ID, p.Query)
	} else {
		mems, err = agent.db.GetMemories(user.ID, p.Category)
	}
	if err != nil {
		return "", fmt.Errorf("search memories: %w", err)
	}

	if len(mems) == 0 {
		return "Nenhuma informacao encontrada.", nil
	}

	var sb strings.Builder
	for _, m := range mems {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", m.Category, m.Key, m.Value))
	}
	return sb.String(), nil
}
