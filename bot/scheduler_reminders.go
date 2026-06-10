package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// Cron de lembretes pontuais (contrato P2).
//
// POR QUE CRON E NÃO WEBHOOK (princípio webhook-first): não existe provider
// externo aqui — o "evento" é o relógio do próprio sistema atingindo um
// instante que o próprio sistema persistiu. Não há quem registre webhook; o
// cron de 1 min é o mesmo padrão já estabelecido para medicação, e a regra de
// frequência mínima para polling de provider externo não incide sobre
// scheduler interno de estado próprio.
//
// QUIET-HOURS: lembrete PEDIDO PELO USUÁRIO bypassa proactiveWindowAllowed
// por design — user-initiated ≠ bot-initiated (precedente: medicação dispara
// em qualquer horário configurado; believe-the-user). "Me lembra às 23:58"
// dispara às 23:58.

// reminderStalenessGrace: catch-up dispara atrasado dentro desta graça (tick
// perdido, restart, deploy às 23:57). Além dela, um lembrete pontual velho é
// pior que nenhum ("te aviso às 23:58" às 03:00 acorda o idoso) → 'missed'
// + audit, nunca silêncio.
const reminderStalenessGrace = 2 * time.Hour

// reminderMaxSendAttempts: falha de envio devolve a 'pending' (re-dispara no
// próximo tick); no cap vira 'failed' auditado.
const reminderMaxSendAttempts = 5

// checkAdHocReminders dispara lembretes pendentes vencidos. Claim ATÔMICO
// antes do envio (padrão provado do repo — fireMedicationReminderGroup faz
// CreateIntakeLogIfAbsent ANTES do Send): robfig/cron v3 não serializa
// ativações e o send pode passar de 60s; send-then-mark teria janela real de
// envio duplo. Falha de envio compensa de volta a 'pending'.
func (s *Scheduler) checkAdHocReminders() {
	if s.notifier == nil && s.sendMsg == nil {
		return // dependências de envio ausentes (CLI/testes)
	}
	now := s.nowFunc().UTC()
	due, err := s.db.GetDueReminders(now)
	if err != nil {
		log.Printf("Scheduler[reminders]: list due: %v", err)
		return
	}
	for _, r := range due {
		s.fireAdHocReminder(r, now)
	}
}

func (s *Scheduler) fireAdHocReminder(r Reminder, now time.Time) {
	if now.Sub(r.FireAt) > reminderStalenessGrace {
		if err := s.db.MarkReminderMissed(r.ID); err != nil {
			log.Printf("Scheduler[reminders]: mark missed id=%d: %v", r.ID, err)
			return
		}
		log.Printf("Scheduler[reminders]: id=%d stale (%s atrasado) — missed", r.ID, now.Sub(r.FireAt).Round(time.Minute))
		NewAuditLog(s.db).Log(r.UserID, "reminder_missed_stale", "",
			fmt.Sprintf("id=%d|fire_at=%s|late=%s", r.ID, r.FireAt.Format(time.RFC3339), now.Sub(r.FireAt).Round(time.Minute)))
		return
	}

	// Lookup/validação do usuário ANTES do claim: MarkReminderMissed só
	// transiciona rows 'pending' — depois do claim ('sent') seria no-op e a
	// promessa sumiria em silêncio com status mentiroso. Erro transitório de
	// DB nem toca a row (próximo tick re-tenta; a graça de staleness limita).
	user, err := s.db.GetUserByID(r.UserID)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		log.Printf("Scheduler[reminders]: lookup user %d p/ id=%d falhou (transitório): %v", r.UserID, r.ID, err)
		return
	}
	if user == nil || !user.IsActive {
		log.Printf("Scheduler[reminders]: user %d indisponível p/ id=%d — missed", r.UserID, r.ID)
		if mErr := s.db.MarkReminderMissed(r.ID); mErr != nil {
			log.Printf("Scheduler[reminders]: mark missed id=%d: %v", r.ID, mErr)
			return
		}
		NewAuditLog(s.db).Log(r.UserID, "reminder_missed_user_unavailable", "",
			fmt.Sprintf("id=%d|fire_at=%s", r.ID, r.FireAt.Format(time.RFC3339)))
		return
	}

	claimed, err := s.db.ClaimReminderForSend(r.ID, now)
	if err != nil {
		log.Printf("Scheduler[reminders]: claim id=%d: %v", r.ID, err)
		return
	}
	if !claimed {
		return // outro tick ganhou a row
	}

	// Texto SEMPRE ecoa o horário agendado (no fuso local do usuário): dentro
	// da graça do catch-up um disparo atrasado se auto-explica.
	loc := s.db.GetEventTimezone(user.ID, r.FireAt)
	if loc == nil {
		loc = BRT()
	}
	msg := fmt.Sprintf("Lembrete das %s: %s", r.FireAt.In(loc).Format("15:04"), r.Text)

	var sendErr error
	if s.notifier != nil {
		sendErr = s.notifier.Send(context.Background(), user, msg)
	} else {
		sendErr = s.sendMsg(user.PhoneNumber, msg)
	}
	if sendErr != nil {
		log.Printf("Scheduler[reminders]: send id=%d falhou: %v — devolvendo a pending", r.ID, sendErr)
		failed, relErr := s.db.ReleaseReminderAfterSendFailure(r.ID, reminderMaxSendAttempts)
		if relErr != nil {
			log.Printf("Scheduler[reminders]: release id=%d: %v", r.ID, relErr)
		}
		if failed {
			NewAuditLog(s.db).Log(r.UserID, "reminder_send_failed", "",
				fmt.Sprintf("id=%d|attempts>=%d", r.ID, reminderMaxSendAttempts))
		}
		return
	}
	log.Printf("Scheduler[reminders]: enviado id=%d para %s (%s)", r.ID, user.Name, r.FireAt.In(loc).Format("15:04"))
}
