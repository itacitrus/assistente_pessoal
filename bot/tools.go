package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
)

// isBackgroundEvent returns true when an existing calendar event should NOT
// be treated as a time-blocking conflict for a new event at the same instant.
// Covers: birthdays, all-day markers, zero-duration reminders, and events
// whose duration spans a day or more (travel markers, multi-day conferences
// shown as day-blocks, etc).
func isBackgroundEvent(e CalendarEvent) bool {
	if e.EventType == "birthday" {
		return true
	}
	if e.End.IsZero() {
		// Some imported events have no End — treat as a point-in-time marker.
		return true
	}
	duration := e.End.Sub(e.Start)
	if duration <= 0 {
		// Zero- or negative-duration: a reminder, not a time block.
		return true
	}
	if duration >= 20*time.Hour {
		// Covers native all-day (24h), multi-day, and near-all-day imports.
		return true
	}
	// Native Google all-day events have Start at midnight + duration multiple of 24h.
	if e.Start.Hour() == 0 && e.Start.Minute() == 0 && duration >= 24*time.Hour {
		return true
	}
	return false
}

var toolHandlers = buildToolHandlers()

// buildToolHandlers retorna o registry completo de handlers. Construido em
// funcao pra permitir merge com mapas extras (Fase 3 — medicacao) sem
// duplicar declaracoes.
func buildToolHandlers() map[string]ToolHandler {
	m := map[string]ToolHandler{
		"buscar_agenda":              handleBuscarAgenda,
		"conectar_agenda":            handleConectarAgenda,
		"criar_evento":               handleCriarEvento,
		"editar_evento":              handleEditarEvento,
		"cancelar_evento":            handleCancelarEvento,
		"buscar_historico":           handleBuscarHistorico,
		"criar_evento_outro_usuario": handleCriarEventoOutroUsuario,
		"gerar_link_meet":            handleGerarLinkMeet,
		"convidar_externo":           handleConvidarExterno,
		"convidar_participante":      handleConvidarParticipante,
		"salvar_memoria":             handleSalvarMemoria,
		"buscar_memoria":             handleBuscarMemoria,
		"registrar_viagem":           handleRegistrarViagem,
		"listar_viagens":             handleListarViagens,
		"cancelar_viagem":            handleCancelarViagem,
		"responder_permissao":        handleResponderPermissao,
	}
	// Fase 3 (idosos): medicacao. Mantido em mapa proprio em
	// tools_medication.go pra preservar coesao por feature.
	for name, h := range medicationToolHandlers {
		m[name] = h
	}
	// Fase 4 (idosos): companion. Mesma estrategia — coesao por feature.
	for name, h := range companionToolHandlers {
		m[name] = h
	}
	// Fase 5 (idosos): tools de responsavel — status_dependente.
	for name, h := range familyToolHandlers {
		m[name] = h
	}
	return m
}

// googleNotConnectedMsg eh devolvido pelas tools de agenda quando o usuario
// ainda nao conectou o Google Calendar. NAO eh erro: o agente recebe esta
// string como resultado e, seguindo a regra do system prompt, OFERECE conectar
// (em vez de so negar) e chama conectar_agenda se o usuario aceitar.
const googleNotConnectedMsg = "O Google Calendar deste usuario ainda nao esta conectado, entao nao consigo acessar/alterar a agenda. NAO apenas negue: pergunte se a pessoa quer conectar agora e, se aceitar, chame a tool conectar_agenda."

// handleConectarAgenda gera um link de conexao com o Google Calendar (state
// OAuth de uso unico + AuthURL) e ENVIA pro WhatsApp do usuario — mesmo
// mecanismo do botao do painel e do reauth. Use quando o usuario aceitar
// conectar. Idempotente: se ja estiver conectado, so avisa.
func handleConectarAgenda(ctx context.Context, agent *Agent, user *User, _ json.RawMessage) (string, error) {
	if user.GoogleCredentials != "" {
		return "A agenda do Google ja esta conectada.", nil
	}
	if agent.cal == nil {
		return "", fmt.Errorf("conectar_agenda: calendar client nao configurado")
	}
	state, err := agent.db.CreateOAuthState(user.ID, oauthStateTTL)
	if err != nil {
		return "", fmt.Errorf("create oauth state: %w", err)
	}
	authURL := agent.cal.AuthURL(state)
	msg := fmt.Sprintf(
		"Pra conectar sua agenda do Google, é só tocar aqui:\n\n%s\n\nVocê autoriza na sua conta Google e eu já passo a enxergar seus compromissos.",
		authURL,
	)
	if err := agent.sendMsg(user.PhoneNumber, msg); err != nil {
		return "", fmt.Errorf("send connect link: %w", err)
	}
	return "Link de conexão com o Google enviado ao usuário. Avise que é só tocar no link que você mandou.", nil
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

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	loc := BRT()
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
	agent.db.ApplyEventTimezones(user.ID, events)

	// Partition: multi-day background events (travel markers, all-day
	// conferences, etc) are rendered as a prefix note — otherwise they get
	// grouped under their *start* date, which confuses users who queried a
	// later date in the span ("pedi 16/04, apareceu Sexta 10/04").
	var prefixNotes []string
	var listEvents []CalendarEvent
	for _, e := range events {
		if isBackgroundEvent(e) && spansMultipleCalendarDays(e) {
			prefixNotes = append(prefixNotes, fmt.Sprintf("%s (%s a %s)",
				e.Title,
				e.Start.In(BRT()).Format("02/01"),
				e.End.In(BRT()).Add(-time.Second).Format("02/01")))
		} else {
			listEvents = append(listEvents, e)
		}
	}

	agent.audit.Log(user.ID, "consultar_agenda", "", fmt.Sprintf("%s a %s", p.StartDate, p.EndDate))

	body := FormatEventList(listEvents)
	if len(prefixNotes) > 0 {
		prefix := "No periodo: " + strings.Join(prefixNotes, "; ") + "\n\n"
		return prefix + body, nil
	}
	return body, nil
}

// spansMultipleCalendarDays reports whether the event's Start and End fall on
// different calendar dates in BRT. Used to detect multi-day events that would
// otherwise get grouped under their start date alone.
func spansMultipleCalendarDays(e CalendarEvent) bool {
	if e.End.IsZero() {
		return false
	}
	sY, sM, sD := e.Start.In(BRT()).Date()
	// Google all-day events use exclusive end (next midnight). Treat
	// "ends exactly at next day's 00:00" as single-day, not multi-day.
	effectiveEnd := e.End.In(BRT()).Add(-time.Second)
	eY, eM, eD := effectiveEnd.Date()
	return sY != eY || sM != eM || sD != eD
}

type criarEventoParams struct {
	Title           string   `json:"title"`
	DateSource      string   `json:"date_source"` // "explicit" | "inferred"
	Date            string   `json:"date"`
	Time            string   `json:"time"`
	DurationMinutes int      `json:"duration_minutes"`
	Location        string   `json:"location"`
	Attendees       []string `json:"attendees"`
	ComMeet         bool     `json:"com_meet"`
	ForceConflict   bool     `json:"force_conflict"`
	Timezone        string   `json:"timezone"`
	Recurrence      string   `json:"recurrence"`
	IsBirthday      bool     `json:"is_birthday"`
}

func handleCriarEvento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p criarEventoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	// Birthdays are native Google all-day yearly events. Parse only the date;
	// ignore time/duration/timezone/recurrence/conflicts — none apply.
	if p.IsBirthday {
		bdayStart, err := time.ParseInLocation(dateLayout, p.Date, BRT())
		if err != nil {
			return "", fmt.Errorf("parse birthday date: %w", err)
		}
		ev := CalendarEvent{
			Title:     p.Title,
			Location:  p.Location,
			Start:     bdayStart,
			End:       bdayStart.AddDate(0, 0, 1),
			EventType: "birthday",
		}
		created, err := agent.cal.CreateEvent(ctx, refreshToken, user.GoogleCalendarID, ev)
		if err != nil {
			return "", fmt.Errorf("create birthday event: %w", err)
		}
		agent.audit.Log(user.ID, "criar_evento", "", p.Title+" (aniversario)")
		return FormatEventCreated(*created), nil
	}

	// Hint inicial de data para lookup de fuso: data explicita se houver,
	// senao "agora" (caminho inferred). Apos resolver a data final, checamos
	// de novo se o fuso muda (caso viagem comece na data resolvida).
	// Hint de fuso: se p.Date for invalido, parsedDateHint vira zero-time.
	// GetEventTimezone retornara BRT por default; o erro sera capturado
	// depois em ResolveEventDate com diagnostico adequado.
	var parsedDateHint time.Time
	if p.Date != "" {
		parsedDateHint, _ = time.ParseInLocation("2006-01-02", p.Date, BRT())
	} else {
		parsedDateHint = time.Now().In(BRT())
	}
	loc := agent.db.GetEventTimezone(user.ID, parsedDateHint)
	tz := p.Timezone
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	} else {
		tz = loc.String()
	}
	if p.Time == "" {
		log.Printf("[%s] criar_evento early-return: missing time (title=%q date=%s)", user.Name, p.Title, p.Date)
		return "Preciso do horário do evento. Pergunte ao usuário.", nil
	}
	if p.DateSource == "" {
		// Defensive: o schema exige date_source, mas se vier vazio tratamos
		// como explicit pra preservar comportamento anterior. Logamos pra
		// monitorar se acontece em producao.
		log.Printf("[%s] criar_evento warn: date_source vazio (title=%q date=%s) — tratando como explicit", user.Name, p.Title, p.Date)
		p.DateSource = string(DateSourceExplicit)
	}
	res, err := ResolveEventDate(ResolveInput{
		Source:       DateSource(p.DateSource),
		ExplicitDate: p.Date,
		Time:         p.Time,
		Now:          time.Now().In(loc),
		Loc:          loc,
	})
	if err != nil {
		return "", fmt.Errorf("resolve event date: %w", err)
	}
	// Se a data resolvida cai em outro fuso (viagem comecando nessa data),
	// re-resolver com o novo Loc.
	if resolvedLoc := agent.db.GetEventTimezone(user.ID, res.Start); resolvedLoc.String() != loc.String() && p.Timezone == "" {
		loc = resolvedLoc
		tz = loc.String()
		res, err = ResolveEventDate(ResolveInput{
			Source:       DateSource(p.DateSource),
			ExplicitDate: p.Date,
			Time:         p.Time,
			Now:          time.Now().In(loc),
			Loc:          loc,
		})
		if err != nil {
			return "", fmt.Errorf("resolve event date (re-tz): %w", err)
		}
	}
	startTime := res.Start

	duration := time.Duration(p.DurationMinutes) * time.Minute
	if p.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}
	endTime := startTime.Add(duration)

	// Check for conflicts before creating (unless user confirmed).
	// If the conflict-check API call fails, we do NOT short-circuit creation —
	// the priority is to actually create the event. We surface the check
	// failure in the result so the agent reports it to the user.
	//
	// "Background" events (birthdays, all-day markers, zero-duration reminders,
	// travel-day markers) are not time-blocking — they go into allDayNotes
	// instead of triggering CONFLITO. Only real time-overlapping meetings
	// raise a conflict.
	var allDayNotes []string
	var conflictCheckWarn string
	if !p.ForceConflict {
		existing, listErr := agent.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, startTime, endTime)
		if listErr != nil {
			if IsInvalidGrantErr(listErr) {
				if _, reauthErr := SendReauthLinkIfDue(agent.db, agent.cal, agent.sendMsg, user, time.Now()); reauthErr != nil {
					log.Printf("[%s] SendReauthLinkIfDue: %v", user.Name, reauthErr)
				}
				return "AUTH_EXPIRED|display=Não consegui checar a agenda — sua autorização com o Google Calendar expirou. Acabei de te mandar um link pra reautorizar.", nil
			}
			log.Printf("[%s] criar_evento conflict-check ListEvents failed (continuing anyway): %v", user.Name, listErr)
			conflictCheckWarn = fmt.Sprintf("\n(aviso: nao consegui checar conflitos: %v)", listErr)
		} else {
			agent.db.ApplyEventTimezones(user.ID, existing)
			var realConflicts []CalendarEvent
			for _, e := range existing {
				if isBackgroundEvent(e) {
					allDayNotes = append(allDayNotes, e.Title)
				} else {
					realConflicts = append(realConflicts, e)
				}
			}
			if len(realConflicts) > 0 {
				var conflicts []string
				for _, e := range realConflicts {
					conflicts = append(conflicts, fmt.Sprintf("- %s (%s - %s)", e.Title, e.Start.Format("15:04"), e.End.Format("15:04")))
				}
				log.Printf("[%s] criar_evento early-return: CONFLITO detected title=%q start=%s conflicts=%d",
					user.Name, p.Title, startTime.Format(time.RFC3339), len(realConflicts))
				return fmt.Sprintf("CONFLITO: já existem eventos nesse horário:\n%s\nO evento NÃO foi criado. Pergunte ao usuário se quer marcar mesmo assim. Se ele confirmar, chame criar_evento novamente com force_conflict=true.", strings.Join(conflicts, "\n")), nil
			}
		}
	}

	// Location/travel-period awareness: if the new event's date falls inside a
	// registered travel period, add a note so the agent can judge whether the
	// location is physically compatible (e.g., don't marcar encontro presencial
	// em Brasilia quando o usuario estara em viagem na Bahia). Non-blocking —
	// just a hint. The agent decides what to do with it.
	if tp, _ := agent.db.GetTravelPeriodForDate(user.ID, startTime); tp != nil {
		allDayNotes = append(allDayNotes,
			fmt.Sprintf("Voce estara em %s nessa data (viagem %s a %s)",
				tp.LocationName,
				tp.StartDate.Format("02/01"),
				tp.EndDate.Format("02/01")))
	}

	ev := CalendarEvent{
		Title:     p.Title,
		Location:  p.Location,
		Attendees: p.Attendees,
		Timezone:  tz,
		Start:     startTime,
		End:       endTime,
	}
	if p.Recurrence != "" {
		ev.Recurrence = []string{p.Recurrence}
	}
	if p.ComMeet {
		ev.MeetLink = "generate"
	}

	created, err := agent.cal.CreateEvent(ctx, refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		if IsInvalidGrantErr(err) {
			if _, reauthErr := SendReauthLinkIfDue(agent.db, agent.cal, agent.sendMsg, user, time.Now()); reauthErr != nil {
				log.Printf("[%s] SendReauthLinkIfDue: %v", user.Name, reauthErr)
			}
			return "AUTH_EXPIRED|display=Não consegui criar o evento — sua autorização com o Google Calendar expirou. Acabei de te mandar um link pra reautorizar.", nil
		}
		return "", fmt.Errorf("create event: %w", err)
	}

	// Snippet da ultima mensagem do usuario pra observabilidade de data implicita.
	var userMsgSnippet string
	if hist, histErr := agent.db.GetConversationHistory(user.ID, 5); histErr == nil {
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				userMsgSnippet = hist[i].Content
				break
			}
		}
	}
	if auditErr := agent.audit.LogCriarEvento(user.ID, p.Title, userMsgSnippet, p.DateSource, p.Date, p.Time,
		res.Start.Format(time.RFC3339), res.Adjusted); auditErr != nil {
		log.Printf("[%s] LogCriarEvento failed: %v", user.Name, auditErr)
	}
	display := FormatEventCreated(*created)
	if res.AdjustNote != "" {
		display = res.AdjustNote + "\n" + display
	}
	if created.MeetLink != "" {
		display += fmt.Sprintf("\nLink do Meet: %s", created.MeetLink)
	}
	if len(allDayNotes) > 0 {
		display += fmt.Sprintf("\nLembrete: nesse dia voce tem: %s", strings.Join(allDayNotes, ", "))
	}
	if conflictCheckWarn != "" {
		display += conflictCheckWarn
	}
	return "OK_CRIADO|display=" + display, nil
}

type editarEventoParams struct {
	EventID         string `json:"event_id"`
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

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	var ev *CalendarEvent
	if p.EventID != "" {
		// Direct lookup by ID — more reliable
		ev = &CalendarEvent{ID: p.EventID}
		// Get full event details
		events, _ := agent.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, time.Now().Add(-30*24*time.Hour), time.Now().Add(365*24*time.Hour))
		agent.db.ApplyEventTimezones(user.ID, events)
		for _, e := range events {
			if e.ID == p.EventID {
				ev = &e
				break
			}
		}
	} else if p.SearchQuery != "" {
		ev, err = agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
		if err != nil {
			return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
		}
	} else {
		return "Preciso do event_id ou search_query para encontrar o evento.", nil
	}
	agent.db.ApplyEventTimezone(user.ID, ev)

	updated := *ev
	if p.NewTitle != "" {
		updated.Title = p.NewTitle
	}
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
		// Interpret the new date/time in whatever tz applies on that calendar date
		// (travel period tz, or BRT default). Prevents editing a Paris-period
		// event as if the new time were in BRT.
		parsedDate, _ := time.ParseInLocation("2006-01-02", dateStr, BRT())
		loc := agent.db.GetEventTimezone(user.ID, parsedDate)
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
	EventID     string `json:"event_id"`
	SearchQuery string `json:"search_query"`
}

func handleCancelarEvento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cancelarEventoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	var eventID, eventTitle string
	if p.EventID != "" {
		eventID = p.EventID
		// Fetch the real title before deleting so the user sees "Evento *Reunião
		// com Carlos* cancelado" instead of "Evento *01mbi86jnlam31t7cmclna67dc*
		// cancelado". Best-effort: if the fetch fails the ID is still usable.
		if ev, getErr := agent.cal.GetEvent(ctx, refreshToken, user.GoogleCalendarID, p.EventID); getErr == nil && ev.Title != "" {
			eventTitle = ev.Title
		} else {
			eventTitle = p.EventID
		}
	} else if p.SearchQuery != "" {
		ev, findErr := agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
		if findErr != nil {
			return fmt.Sprintf("Não encontrei o evento: %v", findErr), nil
		}
		eventID = ev.ID
		eventTitle = ev.Title
	} else {
		return "Preciso do event_id ou search_query para encontrar o evento.", nil
	}

	if err := agent.cal.DeleteEvent(ctx, refreshToken, user.GoogleCalendarID, eventID); err != nil {
		return "", fmt.Errorf("delete event: %w", err)
	}

	agent.audit.Log(user.ID, "cancelar_evento", "", eventTitle)
	return fmt.Sprintf("Evento *%s* cancelado.", eventTitle), nil
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
		return "Nenhuma mensagem encontrada no histórico.", nil
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
	Recurrence      string `json:"recurrence"`
	IsBirthday      bool   `json:"is_birthday"`
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
		return fmt.Sprintf("Não encontrei o usuário '%s'.", p.TargetUser), nil
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
		return fmt.Sprintf("Pedi permissão a %s para criar o evento. Aguardando resposta.", target.Name), nil
	}

	// Has permission: create event on target's calendar
	targetToken, err := Decrypt(target.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt target credentials: %w", err)
	}

	var ev CalendarEvent
	if p.IsBirthday {
		bdayStart, err := time.ParseInLocation(dateLayout, p.Date, BRT())
		if err != nil {
			return "", fmt.Errorf("parse birthday date: %w", err)
		}
		ev = CalendarEvent{
			Title:     p.Title,
			Location:  p.Location,
			Start:     bdayStart,
			End:       bdayStart.AddDate(0, 0, 1),
			EventType: "birthday",
		}
	} else {
		// Use the TARGET user's travel period (if any) to interpret date/time —
		// the event is on their calendar, so their location is what matters.
		parsedDate, _ := time.ParseInLocation("2006-01-02", p.Date, BRT())
		loc := agent.db.GetEventTimezone(target.ID, parsedDate)
		startTime, err := time.ParseInLocation("2006-01-02 15:04", p.Date+" "+p.Time, loc)
		if err != nil {
			return "", fmt.Errorf("parse event time: %w", err)
		}

		duration := time.Duration(p.DurationMinutes) * time.Minute
		if p.DurationMinutes == 0 {
			duration = 60 * time.Minute
		}

		ev = CalendarEvent{
			Title:    p.Title,
			Location: p.Location,
			Start:    startTime,
			End:      startTime.Add(duration),
			Timezone: loc.String(),
		}
		if p.Recurrence != "" {
			ev.Recurrence = []string{p.Recurrence}
		}
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

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
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
	Phone      string `json:"phone"`
	Name       string `json:"name"`
	EventTitle string `json:"event_title"`
	EventDate  string `json:"event_date"`
	EventTime  string `json:"event_time"`
	MeetLink   string `json:"meet_link"`
	Location   string `json:"location"`
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

	// Build Google Calendar "Add to Calendar" link
	calLink := ""
	loc := BRT()
	// Try multiple date formats
	var startTime time.Time
	for _, layout := range []string{"2006-01-02 15:04", "02/01/2006 15:04", "2006-01-02 15:04:05"} {
		if t, e := time.ParseInLocation(layout, p.EventDate+" "+p.EventTime, loc); e == nil {
			startTime = t
			break
		}
	}
	if !startTime.IsZero() {
		endTime := startTime.Add(60 * time.Minute)
		calLink = fmt.Sprintf("https://calendar.google.com/calendar/render?action=TEMPLATE&text=%s&dates=%s/%s",
			url.QueryEscape(p.EventTitle),
			startTime.UTC().Format("20060102T150405Z"),
			endTime.UTC().Format("20060102T150405Z"))
		if p.Location != "" {
			calLink += "&location=" + url.QueryEscape(p.Location)
		}
		if p.MeetLink != "" {
			calLink += "&details=" + url.QueryEscape("Link: "+p.MeetLink)
		}
	}

	// Build invite message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Olá, %s! Sou Zello, assistente do %s.\n\n", p.Name, firstName(user.Name)))
	sb.WriteString(fmt.Sprintf("*%s* te convidou para:\n", user.Name))
	sb.WriteString(fmt.Sprintf("*%s*\n", p.EventTitle))
	sb.WriteString(fmt.Sprintf("Data: %s às %s\n", p.EventDate, p.EventTime))
	if p.Location != "" {
		sb.WriteString(fmt.Sprintf("Local: %s\n", p.Location))
	}
	if p.MeetLink != "" {
		sb.WriteString(fmt.Sprintf("\nLink da reunião: %s\n", p.MeetLink))
	}
	if calLink != "" {
		sb.WriteString(fmt.Sprintf("\nAdicionar à sua agenda: %s\n", calLink))
	}
	sb.WriteString("\nQualquer dúvida, fale diretamente com " + user.Name + ".")

	if agent.sendMsg == nil {
		return "Erro: não consigo enviar mensagens no momento.", nil
	}

	err := agent.sendMsg(phone, sb.String())
	if err != nil {
		return "", fmt.Errorf("send invite: %w", err)
	}

	agent.audit.Log(user.ID, "convidar_externo", p.Name, p.EventTitle)
	log.Printf("[%s] Sent invite to %s (%s) for %s", user.Name, p.Name, phone, p.EventTitle)
	return fmt.Sprintf("Convite enviado para %s (%s) via WhatsApp.", p.Name, p.Phone), nil
}

type convidarParticipanteParams struct {
	SearchQuery string   `json:"search_query"`
	Emails      []string `json:"emails"`
}

func handleConvidarParticipante(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p convidarParticipanteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if user.GoogleCredentials == "" {
		return googleNotConnectedMsg, nil
	}
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	ev, err := agent.cal.FindEvent(ctx, refreshToken, user.GoogleCalendarID, p.SearchQuery)
	if err != nil {
		return fmt.Sprintf("Nao encontrei o evento: %v", err), nil
	}

	if err := agent.cal.AddAttendees(ctx, refreshToken, user.GoogleCalendarID, ev.ID, p.Emails); err != nil {
		return "", fmt.Errorf("add attendees: %w", err)
	}

	agent.audit.Log(user.ID, "convidar_participante", strings.Join(p.Emails, ", "), ev.Title)
	return fmt.Sprintf("Participantes adicionados a *%s*. O Google Calendar enviou convite por email.", ev.Title), nil
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
		return "Nenhuma informação encontrada.", nil
	}

	var sb strings.Builder
	for _, m := range mems {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", m.Category, m.Key, m.Value))
	}
	return sb.String(), nil
}
