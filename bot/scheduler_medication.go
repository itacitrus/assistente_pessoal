package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// =========================================================================
// Jobs de medicacao no Scheduler (Fase 3)
// =========================================================================
//
// Mantidos em arquivo separado pra coesao por feature. scheduler.go fica
// com jobs legados (eventos de calendario), e scheduler_medication.go com
// os novos.
//
// Janela do scheduler: cada tick eh "* * * * *" (1min). Cada job calcula
// uma janela [now-60s, now+1s] e expande RRULEs nessa faixa. Janela
// assimetrica de proposito — preferimos atrasar 1min a perder ocorrencia
// por clock skew. Idempotencia via UNIQUE em medication_intake_log impede
// duplicar.

// checkMedicationReminders varre todos os usuarios ativos e, para cada
// medicamento ativo, expande o RRULE no fuso vigente daquele dia para
// detectar ocorrencias dentro da janela [now-60s, now+1s]. Para cada
// ocorrencia:
//   1. Tenta INSERT em medication_intake_log (UNIQUE garante idempotencia).
//   2. Cria pending_confirmation kind=medication com escalation_policy.
//   3. Envia mensagem natural via Notifier.
//
// Se o INSERT falhar por UNIQUE, o disparo ja aconteceu (em outra invocacao
// concorrente ou em restart pos-tick). Skip silencioso.
func (s *Scheduler) checkMedicationReminders() {
	if s.notifier == nil {
		return // Fase 3 nao habilitada (testes/CLI sem dependencias)
	}
	users, err := s.db.ListActiveUsers()
	if err != nil {
		log.Printf("Scheduler: error listing users for medication reminders: %v", err)
		return
	}
	now := time.Now().UTC()
	windowStart := now.Add(-60 * time.Second)
	windowEnd := now.Add(1 * time.Second)

	for i := range users {
		s.checkUserMedicationReminders(&users[i], windowStart, windowEnd, now)
	}
}

// medOccurrence eh um medicamento que vence num dado instante, com a flag
// critical do schedule que o originou (afeta a politica de escalacao).
type medOccurrence struct {
	med      *Medication
	critical bool
}

func (s *Scheduler) checkUserMedicationReminders(user *User, windowStart, windowEnd, now time.Time) {
	meds, err := s.db.ListActiveMedications(user.ID)
	if err != nil {
		log.Printf("[%s] medication reminders: list failed: %v", user.Name, err)
		return
	}
	// Localiza fuso vigente para a janela atual (respeita travel periods).
	loc := s.db.GetEventTimezone(user.ID, now)

	// Agrupa as ocorrencias por INSTANTE exato (UnixNano). Todos os remedios que
	// vencem no mesmo horario entram num lembrete unico — o controle de toma
	// segue granular (1 intake_log + 1 pending por remedio), so a MENSAGEM eh
	// agrupada.
	groups := map[int64][]medOccurrence{}
	var order []int64 // ordem estavel de disparo (por instante crescente)
	for i := range meds {
		m := &meds[i]
		scheds, err := s.db.ListSchedulesForMedication(m.ID)
		if err != nil {
			continue
		}
		for j := range scheds {
			occs, err := ExpandOccurrences(&scheds[j], windowStart, windowEnd, loc)
			if err != nil {
				log.Printf("[%s] med %d: rrule expand failed: %v", user.Name, m.ID, err)
				continue
			}
			for _, occ := range occs {
				key := occ.UnixNano()
				if _, seen := groups[key]; !seen {
					order = append(order, key)
				}
				groups[key] = appendMedOccurrence(groups[key], m, scheds[j].Critical)
			}
		}
	}
	sortInt64(order)
	for _, key := range order {
		at := time.Unix(0, key).UTC()
		s.fireMedicationReminderGroup(user, groups[key], at)
	}
}

// appendMedOccurrence adiciona m ao grupo deduplicando por med.ID (um remedio
// com dois schedules no mesmo instante aparece UMA vez). Se qualquer schedule
// for critical, a ocorrencia fica critical.
func appendMedOccurrence(group []medOccurrence, m *Medication, critical bool) []medOccurrence {
	for i := range group {
		if group[i].med.ID == m.ID {
			group[i].critical = group[i].critical || critical
			return group
		}
	}
	return append(group, medOccurrence{med: m, critical: critical})
}

// fireMedicationReminderGroup dispara UM lembrete agrupado para todos os
// remedios que vencem no mesmo instante:
//  1. Lock idempotente por remedio (UNIQUE em medication_intake_log). Remedios
//     ja disparados (tick anterior) saem do grupo desta mensagem.
//  2. Para os que EXIGEM confirmacao, cria pending_confirmation (escalacao).
//     Os que nao exigem ficam so com o intake_log 'pending' — o sweeper marca
//     'unknown' depois da tolerancia (sem cutucao nem familia).
//  3. Envia UMA mensagem listando os remedios recem-disparados.
//  4. Audita por remedio.
//
// Falha de envio NAO desfaz os pendings — a proxima rodada do escalation engine
// retoma como attempt 1.
func (s *Scheduler) fireMedicationReminderGroup(user *User, occs []medOccurrence, scheduledAt time.Time) {
	var fired []medOccurrence
	for _, o := range occs {
		err := s.db.CreateIntakeLogIfAbsent(o.med.ID, scheduledAt, IntakePending)
		if err != nil {
			if errors.Is(err, ErrIntakeLogDuplicate) {
				continue // ja disparado neste slot; idempotente
			}
			log.Printf("[%s] med %d: intake log insert failed: %v", user.Name, o.med.ID, err)
			continue
		}
		fired = append(fired, o)
	}
	if len(fired) == 0 {
		return
	}

	anyRequireConfirm := false
	for _, o := range fired {
		if !o.med.RequireConfirmation {
			continue // so lembrete; sem pending, sem escalacao
		}
		anyRequireConfirm = true
		intent := IntentData{
			Medication: &MedicationIntent{
				MedicationID: o.med.ID,
				ScheduledAt:  scheduledAt,
				Reminder:     true,
			},
		}
		eventJSON, _ := json.Marshal(intent)
		policy := "medication_default"
		if o.critical {
			policy = "medication_critical"
		}
		pc := &PendingConfirmation{
			UserID:           user.ID,
			EventData:        string(eventJSON),
			Kind:             "medication",
			EscalationPolicy: &policy,
		}
		if err := s.db.CreatePendingConfirmation(pc); err != nil {
			log.Printf("[%s] med %d: create pending failed: %v", user.Name, o.med.ID, err)
		}
	}

	meds := make([]*Medication, 0, len(fired))
	for _, o := range fired {
		meds = append(meds, o.med)
	}
	msg := buildMedicationReminderMessage(user.Name, meds, anyRequireConfirm)
	if err := s.notifier.Send(context.Background(), user, msg); err != nil {
		log.Printf("[%s] medication group reminder send failed: %v", user.Name, err)
		return
	}

	for _, o := range fired {
		NewAuditLog(s.db).Log(user.ID, "medication_reminder_sent", o.med.Name,
			fmt.Sprintf("med_id=%d|scheduled=%s|grouped=%d", o.med.ID, scheduledAt.Format(time.RFC3339), len(fired)))
	}
}

// buildMedicationReminderMessage monta a mensagem de lembrete. Com um unico
// remedio mantem a forma direta ("Hora do X 50mg, Nome."); com varios, lista
// todos num lembrete so. A pergunta de confirmacao so entra se ao menos um dos
// remedios exige confirmacao (anyRequireConfirm) — pra remedio "so lembrete"
// nao cobramos resposta.
func buildMedicationReminderMessage(userName string, meds []*Medication, anyRequireConfirm bool) string {
	name := firstName(userName)
	ask := ""
	if anyRequireConfirm {
		ask = " Pode confirmar quando tomar?"
	}

	if len(meds) == 1 {
		m := meds[0]
		msg := "Hora do " + m.Name
		if m.Dose != "" {
			msg += " " + m.Dose
		}
		msg += ", " + name + "."
		if m.Instructions != "" {
			msg += " Lembra: " + m.Instructions + "."
		}
		return msg + ask
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Hora dos remédios, %s:", name)
	for _, m := range meds {
		b.WriteString("\n• " + m.Name)
		if m.Dose != "" {
			b.WriteString(" " + m.Dose)
		}
		if m.Instructions != "" {
			b.WriteString(" (lembra: " + m.Instructions + ")")
		}
	}
	b.WriteString(ask)
	return b.String()
}

// sortInt64 ordena uma slice de int64 in-place (crescente). Pequena, sem
// dependencia — usada pra disparar os grupos de lembrete em ordem de horario.
func sortInt64(a []int64) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}

// checkMedicationEscalation aplica EscalationPolicy a pending_confirmations
// kind=medication ainda em status=pending. A logica de tentativas/interval
// mora em EscalationEngine (escalation.go); aqui apenas descobrimos
// candidatas e delegamos.
func (s *Scheduler) checkMedicationEscalation() {
	if s.eng == nil {
		return
	}
	pendings, err := s.db.GetActiveMedicationPendings()
	if err != nil {
		log.Printf("Scheduler: get medication pendings: %v", err)
		return
	}
	now := time.Now().UTC()
	// Caminho em lote: agrupa por usuario e manda UMA mensagem por etapa
	// (cutucao agrupado; aviso a familia agrupado por guardiao). O controle
	// segue granular por remedio — so a mensagem eh agrupada.
	s.eng.ProcessPendings(now, pendings)
}

// checkMedicationUnknownDoses fecha o ciclo dos remedios que NAO exigem
// confirmacao: passada a tolerancia sem o idoso ter confirmado, a dose 'pending'
// vira 'unknown' ("nao sei"). Esses remedios nao tem pending_confirmation, entao
// o escalation engine nunca os toca — este sweeper eh o unico que os resolve.
func (s *Scheduler) checkMedicationUnknownDoses() {
	if s.db == nil {
		return
	}
	n, err := s.db.MarkStaleNoConfirmDosesUnknown(time.Now().UTC())
	if err != nil {
		log.Printf("Scheduler: mark unknown doses: %v", err)
		return
	}
	if n > 0 {
		log.Printf("Scheduler: marked %d no-confirm dose(s) as unknown", n)
	}
}
