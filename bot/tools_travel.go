package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

type registrarViagemParams struct {
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Timezone     string `json:"timezone"`
	LocationName string `json:"location_name"`
}

func handleRegistrarViagem(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p registrarViagemParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	start, err := time.ParseInLocation(dateLayout, p.StartDate, BRT())
	if err != nil {
		return "", fmt.Errorf("parse start_date: %w", err)
	}
	end, err := time.ParseInLocation(dateLayout, p.EndDate, BRT())
	if err != nil {
		return "", fmt.Errorf("parse end_date: %w", err)
	}

	period := &TravelPeriod{
		UserID:       user.ID,
		StartDate:    start,
		EndDate:      end,
		Timezone:     p.Timezone,
		LocationName: p.LocationName,
	}
	if err := agent.db.CreateTravelPeriod(period); err != nil {
		if errors.Is(err, ErrTravelPeriodOverlap) {
			return "Ja existe um periodo de viagem sobreposto a essas datas. Liste as viagens existentes antes de registrar uma nova.", nil
		}
		return "", fmt.Errorf("create travel period: %w", err)
	}

	// Create a visible all-day marker on the user's calendar spanning the whole
	// trip, so the agenda shows "Viagem: X" across those days. Non-blocking
	// (transparency=transparent) — it's a visual cue, not a time reservation.
	// If this fails we do NOT roll back the DB period: the period itself is
	// what drives tz normalization and the marker is a nice-to-have.
	refreshToken, err := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}
	markerTitle := fmt.Sprintf("✈️ Viagem: %s", p.LocationName)
	if markerID, markerErr := agent.cal.CreateAllDayEvent(ctx, refreshToken, user.GoogleCalendarID, markerTitle, start, end); markerErr != nil {
		log.Printf("[%s] registrar_viagem: failed to create all-day marker (continuing): %v", user.Name, markerErr)
	} else if err := agent.db.SetTravelCalendarEventID(period.ID, markerID); err != nil {
		log.Printf("[%s] registrar_viagem: failed to persist marker event id %s: %v", user.Name, markerID, err)
	} else {
		period.CalendarEventID = markerID
	}

	// List events already scheduled in this window so the agent can ask the
	// user whether to keep them in BRT or convert to the destination tz.
	windowStart := start
	windowEnd := end.Add(24*time.Hour - time.Second)

	events, err := agent.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, windowStart, windowEnd)
	if err != nil {
		// Period was created; we just can't preview events. Surface a softer message.
		agent.audit.Log(user.ID, "registrar_viagem", "", fmt.Sprintf("%s %s-%s", p.LocationName, p.StartDate, p.EndDate))
		return fmt.Sprintf("Viagem registrada: %s de %s a %s (%s). Nao consegui listar compromissos existentes na janela.",
			p.LocationName, p.StartDate, p.EndDate, p.Timezone), nil
	}

	agent.audit.Log(user.ID, "registrar_viagem", "", fmt.Sprintf("%s %s-%s", p.LocationName, p.StartDate, p.EndDate))

	// The travel period is now active — events in the window were listed BEFORE
	// normalization, so their Start/End still reflect whatever Google returned
	// (usually BRT for BR users). Build a summary showing both the original
	// time and the equivalent in the destination tz.
	destLoc, _ := time.LoadLocation(p.Timezone)
	if destLoc == nil {
		destLoc = BRT()
	}

	// Filter out the marker event we just created — it'd be noise in the list
	// of "compromissos ja marcados".
	var filtered []CalendarEvent
	for _, ev := range events {
		if ev.ID == period.CalendarEventID {
			continue
		}
		filtered = append(filtered, ev)
	}
	events = filtered

	if len(events) == 0 {
		return fmt.Sprintf("Viagem registrada: %s de %s a %s (%s). Marcador '✈️ Viagem: %s' criado na agenda. Nenhum compromisso existente nessas datas.",
			p.LocationName, p.StartDate, p.EndDate, p.Timezone, p.LocationName), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Viagem registrada: %s de %s a %s (%s). Marcador '✈️ Viagem: %s' criado na agenda.\n\n",
		p.LocationName, p.StartDate, p.EndDate, p.Timezone, p.LocationName)
	fmt.Fprintf(&sb, "Compromissos ja marcados nessa janela (%d):\n", len(events))
	for _, ev := range events {
		origTime := ev.Start.In(BRT()).Format("02/01 15:04")
		destTime := ev.Start.In(destLoc).Format("15:04")
		fmt.Fprintf(&sb, "- [id:%s] %s — %s BRT (= %s em %s)\n",
			ev.ID, ev.Title, origTime, destTime, p.LocationName)
	}
	sb.WriteString("\nIMPORTANTE: pergunte ao usuario, em linguagem natural, quais compromissos ele quer MANTER no horario atual (BRT) e quais quer CONVERTER para o horario local de destino. Para cada um que ele pedir para converter, chame editar_evento com new_time no horario de destino e timezone adequado.")
	return sb.String(), nil
}

func handleListarViagens(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	periods, err := agent.db.ListTravelPeriods(user.ID, true)
	if err != nil {
		return "", fmt.Errorf("list travel periods: %w", err)
	}
	if len(periods) == 0 {
		return "Nenhuma viagem registrada.", nil
	}

	var sb strings.Builder
	sb.WriteString("Viagens registradas:\n")
	for _, p := range periods {
		fmt.Fprintf(&sb, "- [id:%d] %s: %s a %s (%s)\n",
			p.ID, p.LocationName,
			p.StartDate.Format(dateLayout), p.EndDate.Format(dateLayout),
			p.Timezone)
	}
	return sb.String(), nil
}

type cancelarViagemParams struct {
	PeriodID     int64  `json:"period_id"`
	LocationName string `json:"location_name"`
}

func handleCancelarViagem(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cancelarViagemParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	if p.PeriodID == 0 && p.LocationName == "" {
		return "Preciso do period_id ou location_name para cancelar.", nil
	}

	id := p.PeriodID
	if id == 0 {
		periods, err := agent.db.ListTravelPeriods(user.ID, false)
		if err != nil {
			return "", fmt.Errorf("list travel periods: %w", err)
		}
		var matches []TravelPeriod
		needle := strings.ToLower(p.LocationName)
		for _, tp := range periods {
			if strings.Contains(strings.ToLower(tp.LocationName), needle) {
				matches = append(matches, tp)
			}
		}
		if len(matches) == 0 {
			return fmt.Sprintf("Nao encontrei viagem com o nome %q.", p.LocationName), nil
		}
		if len(matches) > 1 {
			var sb strings.Builder
			sb.WriteString("Varias viagens com esse nome. Peca pro usuario especificar ou use period_id:\n")
			for _, m := range matches {
				fmt.Fprintf(&sb, "- [id:%d] %s %s a %s\n", m.ID, m.LocationName,
					m.StartDate.Format(dateLayout), m.EndDate.Format(dateLayout))
			}
			return sb.String(), nil
		}
		id = matches[0].ID
	}

	// Look up the period before deleting so we can also remove the all-day
	// marker event from Google Calendar. Best-effort: if the delete fails
	// (event manually removed, token expired, etc) we log and proceed — the
	// DB period must come down regardless.
	period, _ := agent.db.GetTravelPeriodByID(id, user.ID)
	if period != nil && period.CalendarEventID != "" {
		refreshToken, decErr := Decrypt(user.GoogleCredentials, agent.cfg.EncryptionKey)
		if decErr != nil {
			log.Printf("[%s] cancelar_viagem: decrypt failed (skipping marker delete): %v", user.Name, decErr)
		} else if delErr := agent.cal.DeleteEvent(ctx, refreshToken, user.GoogleCalendarID, period.CalendarEventID); delErr != nil {
			log.Printf("[%s] cancelar_viagem: marker delete failed (continuing): %v", user.Name, delErr)
		}
	}

	if err := agent.db.DeleteTravelPeriod(id, user.ID); err != nil {
		return "", fmt.Errorf("delete travel period: %w", err)
	}
	agent.audit.Log(user.ID, "cancelar_viagem", "", fmt.Sprintf("id=%d", id))
	return "Viagem cancelada e marcador removido da agenda. Compromissos nessas datas voltam a ser interpretados no fuso do Brasil.", nil
}
