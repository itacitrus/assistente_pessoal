package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Tools de lembrete pontual (contrato P2 — mata o B4 pela raiz: o modelo
// prometia "te lembro às 23:58" porque a ferramenta NÃO EXISTIA). Desacoplada
// do Google Calendar de propósito: funciona para o idoso sem agenda conectada.
//
// O instante é resolvido em Go (ResolveEventDate — a MESMA regra determinística
// de criar_evento: inferred rola pra amanhã se a hora já passou; explicit no
// passado ajusta com nota) ou por offset relativo ("daqui 20 min" →
// offset_minutes, espelhando adiar_remedio). O LLM NUNCA deriva o instante.

type agendarLembreteParams struct {
	Text          string `json:"text"`
	DateSource    string `json:"date_source"`    // "inferred" (default) | "explicit"
	Date          string `json:"date"`           // YYYY-MM-DD quando explicit
	TimeHHMM      string `json:"time_hhmm"`      // ex: "23:58"
	OffsetMinutes int    `json:"offset_minutes"` // alternativa: "daqui a 20 min"
}

func handleAgendarLembrete(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p agendarLembreteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	p.Text = strings.TrimSpace(p.Text)
	if p.Text == "" {
		return "Preciso saber do que te lembrar — qual é o recado do lembrete?", nil
	}

	now := time.Now()
	loc := agent.db.GetEventTimezone(user.ID, now)
	if loc == nil {
		loc = BRT()
	}

	var fireAt time.Time
	var adjustNote string
	switch {
	case p.OffsetMinutes > 0:
		fireAt = now.Add(time.Duration(p.OffsetMinutes) * time.Minute)
	case strings.TrimSpace(p.TimeHHMM) != "":
		src := DateSourceInferred
		if strings.TrimSpace(p.DateSource) == string(DateSourceExplicit) {
			src = DateSourceExplicit
		}
		res, err := ResolveEventDate(ResolveInput{
			Source:       src,
			ExplicitDate: strings.TrimSpace(p.Date),
			Time:         strings.TrimSpace(p.TimeHHMM),
			Now:          now,
			Loc:          loc,
		})
		if err != nil {
			return fmt.Sprintf("Não consegui entender o horário do lembrete (%v). Me diz o horário tipo \"23:58\" ou \"daqui a 20 minutos\"?", err), nil
		}
		fireAt = res.Start
		adjustNote = res.AdjustNote
	default:
		return "Pra quando agendo esse lembrete? Me diz um horário (ex: \"às 23:58\") ou um prazo (\"daqui a 20 minutos\").", nil
	}

	// Persiste ANTES de confirmar (nunca narrar sem persistir). Duplicata
	// pendente idêntica é idempotente — confirma como se tivesse criado.
	if _, err := agent.db.CreateReminderIfAbsent(user.ID, p.Text, fireAt); err != nil &&
		!errors.Is(err, ErrReminderDuplicate) {
		return "", fmt.Errorf("salvar lembrete: %w", err)
	}
	if agent.audit != nil {
		agent.audit.Log(user.ID, "agendar_lembrete", "",
			fmt.Sprintf("fire_at=%s|text=%.60s", fireAt.UTC().Format(time.RFC3339), p.Text))
	}

	// Eco do instante RESOLVIDO (não do que o modelo acha): rollover inferido
	// e ajuste de data passada ficam visíveis na confirmação.
	local := fireAt.In(loc)
	rel := relativeDayLabel(local, now.In(loc))
	when := fmt.Sprintf("%s, %s às %s", weekdaysPT[local.Weekday()], local.Format("02/01"), local.Format("15:04"))
	if rel != "" {
		when = fmt.Sprintf("%s (%s) às %s", rel, local.Format("02/01"), local.Format("15:04"))
	}
	out := fmt.Sprintf("LEMBRETE_SALVO|display=Lembrete salvo: vou te avisar %s — %s", when, p.Text)
	if adjustNote != "" {
		// |nota= é advisory (não exigido verbatim pelo guard I4).
		out += "\n|nota=" + adjustNote
	}
	return out, nil
}

type cancelarLembreteParams struct {
	ReminderID int64  `json:"reminder_id"`
	Query      string `json:"query"`
}

func handleCancelarLembrete(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cancelarLembreteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	pending, err := agent.db.ListPendingReminders(user.ID)
	if err != nil {
		return "", fmt.Errorf("listar lembretes: %w", err)
	}
	if len(pending) == 0 {
		return "Não há nenhum lembrete pendente pra cancelar.", nil
	}

	target := int64(0)
	switch {
	case p.ReminderID > 0:
		target = p.ReminderID
	case strings.TrimSpace(p.Query) != "":
		q := strings.ToLower(strings.TrimSpace(p.Query))
		var matches []Reminder
		for _, r := range pending {
			if strings.Contains(strings.ToLower(r.Text), q) {
				matches = append(matches, r)
			}
		}
		if len(matches) == 1 {
			target = matches[0].ID
		} else if len(matches) > 1 {
			return "Tem mais de um lembrete parecido:\n" + formatReminderList(agent, user, matches) +
				"\nQual deles cancelo? (use o id)", nil
		}
	}
	if target == 0 {
		if len(pending) == 1 {
			target = pending[0].ID
		} else {
			return "Lembretes pendentes:\n" + formatReminderList(agent, user, pending) +
				"\nQual deles cancelo? (use o id)", nil
		}
	}

	ok, err := agent.db.CancelReminder(user.ID, target)
	if err != nil {
		return "", fmt.Errorf("cancelar lembrete: %w", err)
	}
	if !ok {
		return "Esse lembrete não está mais pendente (já disparou ou foi cancelado).", nil
	}
	if agent.audit != nil {
		agent.audit.Log(user.ID, "cancelar_lembrete", "", fmt.Sprintf("id=%d", target))
	}
	return "Lembrete cancelado.", nil
}

func formatReminderList(agent *Agent, user *User, list []Reminder) string {
	now := time.Now()
	loc := agent.db.GetEventTimezone(user.ID, now)
	if loc == nil {
		loc = BRT()
	}
	var b strings.Builder
	for _, r := range list {
		local := r.FireAt.In(loc)
		fmt.Fprintf(&b, "- [id:%d] %s, %s às %s — %s\n",
			r.ID, weekdaysPT[local.Weekday()], local.Format("02/01"), local.Format("15:04"), r.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}
