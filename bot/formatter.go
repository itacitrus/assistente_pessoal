package main

import (
	"fmt"
	"strings"
	"time"
)

var weekdaysPT = map[time.Weekday]string{
	time.Sunday:    "Domingo",
	time.Monday:    "Segunda",
	time.Tuesday:   "Terca",
	time.Wednesday: "Quarta",
	time.Thursday:  "Quinta",
	time.Friday:    "Sexta",
	time.Saturday:  "Sabado",
}

func FormatDailySummary(userName string, events []CalendarEvent, date time.Time) string {
	dayStr := date.Format("02/01/2006")
	weekday := weekdaysPT[date.Weekday()]

	if len(events) == 0 {
		return fmt.Sprintf("Bom dia, %s! Sua agenda de %s (%s) esta livre. Nenhum compromisso hoje.", userName, weekday, dayStr)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Bom dia, %s! Sua agenda de %s (%s):\n\n", userName, weekday, dayStr))
	for _, ev := range events {
		startStr := ev.Start.Format("15:04")
		endStr := ev.End.Format("15:04")
		sb.WriteString(fmt.Sprintf("  %s - %s: %s\n", startStr, endStr, ev.Title))
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d compromisso(s)", len(events)))
	return sb.String()
}

func FormatWeeklySummary(userName string, events []CalendarEvent, weekStart time.Time) string {
	weekEndDate := weekStart.AddDate(0, 0, 6)

	if len(events) == 0 {
		return fmt.Sprintf("Boa noite, %s! Sua semana de %s a %s esta livre.",
			userName, weekStart.Format("02/01"), weekEndDate.Format("02/01"))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Boa noite, %s! Agenda da semana (%s a %s):\n\n",
		userName, weekStart.Format("02/01"), weekEndDate.Format("02/01")))

	currentDay := ""
	for _, ev := range events {
		dayKey := ev.Start.Format("02/01")
		weekday := weekdaysPT[ev.Start.Weekday()]
		if dayKey != currentDay {
			if currentDay != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("*%s %s*\n", weekday, dayKey))
			currentDay = dayKey
		}
		sb.WriteString(fmt.Sprintf("  %s: %s\n", ev.Start.Format("15:04"), ev.Title))
	}
	sb.WriteString(fmt.Sprintf("\nTotal: %d compromisso(s) na semana", len(events)))
	return sb.String()
}

func FormatReminder(ev CalendarEvent) string {
	return fmt.Sprintf("Lembrete: *%s* comeca as %s (em 1 hora)",
		ev.Title, ev.Start.Format("15:04"))
}

func FormatEventCreated(ev CalendarEvent) string {
	weekday := weekdaysPT[ev.Start.Weekday()]
	if ev.EventType == "birthday" {
		return fmt.Sprintf("Aniversario criado: *%s*\n%s, %s (repete todo ano)",
			ev.Title, weekday, ev.Start.Format("02/01"))
	}
	return fmt.Sprintf("Evento criado: *%s*\n%s, %s as %s",
		ev.Title, weekday, ev.Start.Format("02/01"), ev.Start.Format("15:04"))
}

func FormatEventList(events []CalendarEvent) string {
	if len(events) == 0 {
		return "Nenhum compromisso encontrado nesse periodo."
	}

	var sb strings.Builder
	currentDay := ""
	for _, ev := range events {
		dayKey := ev.Start.Format("02/01")
		weekday := weekdaysPT[ev.Start.Weekday()]
		if dayKey != currentDay {
			if currentDay != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("*%s %s*\n", weekday, dayKey))
			currentDay = dayKey
		}
		suffix := ""
		if ev.EventType != "" && ev.EventType != "default" {
			suffix += fmt.Sprintf(" [type:%s]", ev.EventType)
		}
		if ev.RecurringEventID != "" {
			// Master id is what DeleteEvent needs to remove the whole series.
			suffix += fmt.Sprintf(" [master:%s]", ev.RecurringEventID)
		}
		sb.WriteString(fmt.Sprintf("  %s - %s: %s [id:%s]%s\n", ev.Start.Format("15:04"), ev.End.Format("15:04"), ev.Title, ev.ID, suffix))
	}
	return sb.String()
}
