package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// =========================================================================
// RRULE wrapper (Fase 3)
// =========================================================================
//
// Razoes para encapsular a lib em vez de usar rrule.RRule direto:
//
//   1. Centralizar tratamento de timezone — RRULE nao tem fuso intrinseco;
//      quem cria a RRule decide o Dtstart.Location. Bug classico de RRULE
//      (BYHOUR=8 virando 8h UTC = 5h BRT) so acontece se voce esquecer
//      desse detalhe. Aqui passamos loc explicito sempre.
//
//   2. Restringir o subset suportado: DAILY/WEEKLY/MONTHLY apenas. HOURLY
//      e mais granular nao fazem sentido pra remedio (e horarios
//      intra-hospitalares fogem do MVP).
//
//   3. DescribeRRULE em PT-BR — para mensagens ao idoso. RRULE crua eh
//      ininteligivel pra usuario final.
//
// Janela do scheduler (Fase 3 §6, fix do plano §8.3):
//   ExpandOccurrences eh chamada com janela [now-60s, now+1s]. A janela eh
//   assimetrica de proposito — preferimos atrasar o lembrete em ate 1min do
//   que perder uma ocorrencia por clock skew. Idempotencia via UNIQUE em
//   medication_intake_log impede duplicar caso a janela cubra duas vezes.

// ParseRRULE valida e devolve uma rrule.RRule pronta. Aceita prefixo "RRULE:"
// ou string crua começando em "FREQ=...". Recusa frequencias menos
// granulares que DAILY (YEARLY/HOURLY/MINUTELY/SECONDLY) — fora do escopo
// medicacao MVP.
//
// Tambem exige BYHOUR — sem ele, uma RRULE como "FREQ=DAILY" so define data,
// nao hora. Nao da pra montar lembrete sem hora.
func ParseRRULE(s string) (*rrule.RRule, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("rrule vazia")
	}
	raw := strings.TrimPrefix(strings.TrimSpace(s), "RRULE:")
	opts, err := rrule.StrToROption(raw)
	if err != nil {
		return nil, fmt.Errorf("rrule parse: %w", err)
	}
	switch opts.Freq {
	case rrule.DAILY, rrule.WEEKLY, rrule.MONTHLY:
		// ok
	default:
		return nil, fmt.Errorf("frequencia nao suportada: use DAILY, WEEKLY ou MONTHLY")
	}
	if len(opts.Byhour) == 0 {
		return nil, fmt.Errorf("rrule sem BYHOUR — preciso saber o horario do remedio (ex: ;BYHOUR=8)")
	}
	rr, err := rrule.NewRRule(*opts)
	if err != nil {
		return nil, fmt.Errorf("rrule build: %w", err)
	}
	return rr, nil
}

// ExpandOccurrences devolve todas as ocorrencias de schedule no intervalo
// [start, end), interpretado no fuso loc.
//
// Lê start_date do schedule pra fixar o Dtstart no fuso correto. Sem isto,
// BYHOUR=8 vira 8h UTC e a hora local sai 5h (em BRT). Comportamento
// considerado bug pelo plano.
//
// Respeita end_date: se schedule.EndDate != nil, define Until na lib pra
// cortar ocorrencias depois da data inclusiva.
func ExpandOccurrences(sched *MedicationSchedule, start, end time.Time, loc *time.Location) ([]time.Time, error) {
	if sched == nil {
		return nil, fmt.Errorf("nil schedule")
	}
	if loc == nil {
		loc = BRT()
	}
	raw := strings.TrimPrefix(strings.TrimSpace(sched.RRULE), "RRULE:")
	opts, err := rrule.StrToROption(raw)
	if err != nil {
		return nil, fmt.Errorf("rrule parse: %w", err)
	}
	if len(opts.Byhour) == 0 {
		return nil, fmt.Errorf("rrule sem BYHOUR")
	}

	// Dtstart no fuso correto. Use start_date como dia 0, hora 00:00 — a
	// lib cuida de pular pra primeira ocorrencia onde BYHOUR/BYMINUTE bate.
	dtStart := time.Date(
		sched.StartDate.Year(), sched.StartDate.Month(), sched.StartDate.Day(),
		0, 0, 0, 0, loc,
	)
	opts.Dtstart = dtStart

	// Until: end_date inclusiva. Adicionamos 24h pra garantir que o ultimo dia
	// inteiro seja considerado pela lib.
	if sched.EndDate != nil {
		opts.Until = sched.EndDate.In(loc).Add(24 * time.Hour).Add(-time.Second)
	}

	rr, err := rrule.NewRRule(*opts)
	if err != nil {
		return nil, fmt.Errorf("rrule build: %w", err)
	}

	occs := rr.Between(start, end, true)
	sort.Slice(occs, func(i, j int) bool { return occs[i].Before(occs[j]) })
	return occs, nil
}

// shiftRRULEHours desloca todos os horarios (BYHOUR/BYMINUTE) de uma RRULE por
// delta, preservando frequencia, intervalo e dias da semana. Usado pela
// politica take_recalculate: o idoso tomou atrasado e os horarios passam a
// ancorar no novo horario, mantendo o espacamento entre doses.
//
// Como BYMINUTE eh unico (cf. buildMedicationRRULE) e delta eh constante, todos
// os horarios deslocam de forma uniforme — o minuto resultante eh o mesmo pra
// todos. Horarios que passam de 24h fazem wrap (mod 24h); para atrasos tipicos
// (minutos/poucas horas) isso so afeta doses ja tarde da noite.
func shiftRRULEHours(rruleStr string, delta time.Duration) (string, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(rruleStr), "RRULE:")
	opts, err := rrule.StrToROption(raw)
	if err != nil {
		return "", fmt.Errorf("rrule parse: %w", err)
	}
	if len(opts.Byhour) == 0 {
		return "", fmt.Errorf("rrule sem BYHOUR")
	}
	minute := 0
	if len(opts.Byminute) > 0 {
		minute = opts.Byminute[0]
	}
	deltaMin := int(delta.Round(time.Minute) / time.Minute)
	const dayMin = 24 * 60
	newHours := make([]int, 0, len(opts.Byhour))
	newMinute := minute
	for i, h := range opts.Byhour {
		total := (((h*60+minute+deltaMin)%dayMin)+dayMin)%dayMin
		newHours = append(newHours, total/60)
		if i == 0 {
			newMinute = total % 60
		}
	}
	sort.Ints(newHours)

	var sb strings.Builder
	switch opts.Freq {
	case rrule.DAILY:
		sb.WriteString("FREQ=DAILY")
	case rrule.WEEKLY:
		sb.WriteString("FREQ=WEEKLY")
	case rrule.MONTHLY:
		sb.WriteString("FREQ=MONTHLY")
	default:
		return "", fmt.Errorf("frequencia nao suportada para recalculo")
	}
	if opts.Interval > 1 {
		fmt.Fprintf(&sb, ";INTERVAL=%d", opts.Interval)
	}
	if len(opts.Byweekday) > 0 {
		days := make([]string, 0, len(opts.Byweekday))
		for _, d := range opts.Byweekday {
			days = append(days, weekdayICAL(d))
		}
		sb.WriteString(";BYDAY=" + strings.Join(days, ","))
	}
	hourStrs := make([]string, 0, len(newHours))
	for _, h := range newHours {
		hourStrs = append(hourStrs, fmt.Sprintf("%d", h))
	}
	sb.WriteString(";BYHOUR=" + strings.Join(hourStrs, ","))
	fmt.Fprintf(&sb, ";BYMINUTE=%d", newMinute)
	return sb.String(), nil
}

// weekdayICAL mapeia rrule.Weekday (0=MO..6=SU) para o codigo iCal BYDAY.
func weekdayICAL(d rrule.Weekday) string {
	return [...]string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}[d.Day()]
}

// DescribeRRULE retorna texto natural em PT-BR. Best-effort — fallback para
// a string crua se o caso nao for coberto. Usado em mensagens ao usuario
// pra confirmacao de cadastro ("vou cadastrar X, todos os dias as 8h").
func DescribeRRULE(s string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(s), "RRULE:")
	opts, err := rrule.StrToROption(raw)
	if err != nil {
		return s
	}
	var freq string
	switch opts.Freq {
	case rrule.DAILY:
		if opts.Interval > 1 {
			freq = fmt.Sprintf("a cada %d dias", opts.Interval)
		} else {
			freq = "todos os dias"
		}
	case rrule.WEEKLY:
		if len(opts.Byweekday) == 0 {
			freq = "toda semana"
		} else {
			days := []string{}
			for _, d := range opts.Byweekday {
				days = append(days, weekdayPT(d))
			}
			freq = "toda " + joinPT(days)
		}
	case rrule.MONTHLY:
		freq = "todo mês"
	default:
		return s
	}
	if len(opts.Byhour) == 0 {
		return freq
	}
	hours := make([]string, 0, len(opts.Byhour))
	for _, h := range opts.Byhour {
		hours = append(hours, fmt.Sprintf("%dh", h))
	}
	return freq + " às " + joinPT(hours)
}

// weekdayPT mapeia rrule.Weekday (lib usa 0=MO..6=SU) para nome em PT-BR.
func weekdayPT(d rrule.Weekday) string {
	switch d.Day() {
	case 0:
		return "segunda"
	case 1:
		return "terça"
	case 2:
		return "quarta"
	case 3:
		return "quinta"
	case 4:
		return "sexta"
	case 5:
		return "sábado"
	case 6:
		return "domingo"
	}
	return ""
}

// joinPT junta lista com ", " e " e " antes do ultimo. Util para frases
// naturais em PT-BR ("8h, 14h e 20h").
func joinPT(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " e " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " e " + items[len(items)-1]
}
