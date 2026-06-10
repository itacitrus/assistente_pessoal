package main

import (
	"fmt"
	"strings"
	"time"
)

var weekdaysPT = map[time.Weekday]string{
	time.Sunday:    "Domingo",
	time.Monday:    "Segunda",
	time.Tuesday:   "Terça",
	time.Wednesday: "Quarta",
	time.Thursday:  "Quinta",
	time.Friday:    "Sexta",
	time.Saturday:  "Sábado",
}

// weekdayAbbrevPT é a forma curta minúscula usada nos carimbos de histórico
// ("[ter 03/06 14:00]"). Mantida em sincronia com leadingStampRe (turn_context.go).
var weekdayAbbrevPT = map[time.Weekday]string{
	time.Sunday:    "dom",
	time.Monday:    "seg",
	time.Tuesday:   "ter",
	time.Wednesday: "qua",
	time.Thursday:  "qui",
	time.Friday:    "sex",
	time.Saturday:  "sáb",
}

// formatHistoryTurn prefixa um turno do histórico com o carimbo relativo de
// QUANDO ele aconteceu, computado em Go — a fonte da cegueira temporal do B2:
// sem isso o modelo via um muro de texto sem datas e chutava "amanhã" para um
// lembrete que disparou hoje. createdAt vem do driver em UTC (DATETIME
// CURRENT_TIMESTAMP); a conversão usa o fuso LOCAL do usuário (travel-aware),
// o mesmo do TurnContext — carimbo, relógio dinâmico e [AGORA] concordam por
// construção.
//
//	mesmo dia    → "[hoje 09:12] "
//	dia anterior → "[ontem 14:00] "
//	dia seguinte → "[amanhã 08:00] " (defensivo; clock skew)
//	outro dia    → "[ter 03/06 14:00] "
//	zero         → conteúdo inalterado (compat com rows/tests sem CreatedAt)
func formatHistoryTurn(content string, createdAt, now time.Time, loc *time.Location) string {
	if createdAt.IsZero() {
		return content
	}
	if loc == nil {
		loc = BRT()
	}
	local := createdAt.In(loc)
	nowLocal := now.In(loc)

	dayDiff := daysBetween(local, nowLocal)
	clock := local.Format("15:04")
	var stamp string
	switch dayDiff {
	case 0:
		stamp = "[hoje " + clock + "] "
	case 1:
		stamp = "[ontem " + clock + "] "
	case -1:
		stamp = "[amanhã " + clock + "] "
	default:
		stamp = fmt.Sprintf("[%s %s %s] ", weekdayAbbrevPT[local.Weekday()], local.Format("02/01"), clock)
	}
	return stamp + content
}

// daysBetween retorna a diferença em dias-calendário entre from e to (no
// Location de cada um). Ancora ambas as datas ao meio-dia UTC antes de
// dividir: UTC não tem transição de DST, então cada dia tem exatamente 24h —
// em fusos com horário de verão (viagem p/ Europe/Lisbon etc.) o dia do
// spring-forward tem 23h e meia-noite-a-meia-noite/24h truncaria errado
// (ontem viraria "[hoje]").
func daysBetween(from, to time.Time) int {
	f := time.Date(from.Year(), from.Month(), from.Day(), 12, 0, 0, 0, time.UTC)
	t := time.Date(to.Year(), to.Month(), to.Day(), 12, 0, 0, 0, time.UTC)
	return int(t.Sub(f) / (24 * time.Hour))
}

// relativeDayLabel retorna "HOJE" se eventStart e now caem no mesmo dia
// calendario (no fuso de eventStart); "AMANHA" se eventStart e o dia
// calendario seguinte; string vazia caso contrario. Ancora narrativa
// para impedir freehand divergente do agente.
func relativeDayLabel(eventStart, now time.Time) string {
	loc := eventStart.Location()
	nowInLoc := now.In(loc)
	sY, sM, sD := eventStart.Date()
	nY, nM, nD := nowInLoc.Date()
	if sY == nY && sM == nM && sD == nD {
		return "HOJE"
	}
	tomorrow := nowInLoc.AddDate(0, 0, 1)
	tY, tM, tD := tomorrow.Date()
	if sY == tY && sM == tM && sD == tD {
		return "AMANHÃ"
	}
	return ""
}

func FormatDailySummary(userName string, events []CalendarEvent, date time.Time) string {
	dayStr := date.Format("02/01/2006")
	weekday := weekdaysPT[date.Weekday()]

	if len(events) == 0 {
		return fmt.Sprintf("Bom dia, %s! Sua agenda de %s (%s) está livre. Nenhum compromisso hoje.", userName, weekday, dayStr)
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
		return fmt.Sprintf("Boa noite, %s! Sua semana de %s a %s está livre.",
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

// FormatReminder é auto-datado: o lembrete é o artefato mais relido do
// histórico, então carrega data absoluta PRIMEIRO e a palavra relativa como
// anotação ("Terça, 03/06 (HOJE)") — lido amanhã com carimbo "[ontem ...]",
// a âncora continua sendo a data absoluta, não um HOJE estaleça.
func FormatReminder(ev CalendarEvent, now time.Time) string {
	weekday := weekdaysPT[ev.Start.Weekday()]
	rel := ""
	if r := relativeDayLabel(ev.Start, now); r != "" {
		rel = " (" + r + ")"
	}
	return fmt.Sprintf("Lembrete: *%s* — %s, %s%s às %s (em 1 hora)",
		ev.Title, weekday, ev.Start.Format("02/01"), rel, ev.Start.Format("15:04"))
}

// FormatEventCreated segue a mesma ordem absoluto-primeiro do FormatReminder.
func FormatEventCreated(ev CalendarEvent, now time.Time) string {
	weekday := weekdaysPT[ev.Start.Weekday()]
	if ev.EventType == "birthday" {
		return fmt.Sprintf("Aniversário criado: *%s*\n%s, %s (repete todo ano)",
			ev.Title, weekday, ev.Start.Format("02/01"))
	}
	rel := ""
	if r := relativeDayLabel(ev.Start, now); r != "" {
		rel = " (" + r + ")"
	}
	return fmt.Sprintf("Evento criado: *%s*\n%s, %s%s às %s",
		ev.Title, weekday, ev.Start.Format("02/01"), rel, ev.Start.Format("15:04"))
}

func FormatEventList(events []CalendarEvent) string {
	if len(events) == 0 {
		return "Nenhum compromisso encontrado nesse período."
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
