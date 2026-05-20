package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

func (s *Scheduler) checkUserMedicationReminders(user *User, windowStart, windowEnd, now time.Time) {
	meds, err := s.db.ListActiveMedications(user.ID)
	if err != nil {
		log.Printf("[%s] medication reminders: list failed: %v", user.Name, err)
		return
	}
	for _, m := range meds {
		scheds, err := s.db.ListSchedulesForMedication(m.ID)
		if err != nil {
			continue
		}
		for i := range scheds {
			sched := scheds[i]
			// Localiza fuso vigente para a janela atual (respeita travel periods).
			loc := s.db.GetEventTimezone(user.ID, now)
			occs, err := ExpandOccurrences(&sched, windowStart, windowEnd, loc)
			if err != nil {
				log.Printf("[%s] med %d: rrule expand failed: %v", user.Name, m.ID, err)
				continue
			}
			for _, occ := range occs {
				s.fireMedicationReminder(user, &m, &sched, occ)
			}
		}
	}
}

// fireMedicationReminder dispara um lembrete e cria o pending_confirmation.
// 4 fases:
//   1. Lock idempotente via UNIQUE em medication_intake_log.
//   2. Cria pending_confirmation kind=medication com escalation_policy.
//   3. Envia mensagem via Notifier.
//   4. Audit.
//
// Falha de envio (3) NAO desfaz pending — proxima rodada do escalation
// engine vai retry como attempt 1.
func (s *Scheduler) fireMedicationReminder(user *User, m *Medication, sched *MedicationSchedule, scheduledAt time.Time) {
	err := s.db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)
	if err != nil {
		if errors.Is(err, ErrIntakeLogDuplicate) {
			return // ja disparado em tick anterior; idempotente
		}
		log.Printf("[%s] med %d: intake log insert failed: %v", user.Name, m.ID, err)
		return
	}

	intent := IntentData{
		Medication: &MedicationIntent{
			MedicationID: m.ID,
			ScheduledAt:  scheduledAt,
			Reminder:     true,
		},
	}
	eventJSON, _ := json.Marshal(intent)
	policy := "medication_default"
	if sched.Critical {
		policy = "medication_critical"
	}
	pc := &PendingConfirmation{
		UserID:           user.ID,
		EventData:        string(eventJSON),
		Kind:             "medication",
		EscalationPolicy: &policy,
	}
	if err := s.db.CreatePendingConfirmation(pc); err != nil {
		log.Printf("[%s] med %d: create pending failed: %v", user.Name, m.ID, err)
		return
	}

	msg := fmt.Sprintf("Hora do %s", m.Name)
	if m.Dose != "" {
		msg += " " + m.Dose
	}
	msg += fmt.Sprintf(", %s. Pode confirmar quando tomar?", firstName(user.Name))
	if m.Instructions != "" {
		msg += " Lembra: " + m.Instructions + "."
	}

	if err := s.notifier.Send(context.Background(), user, msg); err != nil {
		log.Printf("[%s] med %d: notifier send failed: %v", user.Name, m.ID, err)
		return
	}

	NewAuditLog(s.db).Log(user.ID, "medication_reminder_sent", m.Name,
		fmt.Sprintf("med_id=%d|scheduled=%s|policy=%s", m.ID, scheduledAt.Format(time.RFC3339), policy))
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
	for i := range pendings {
		pc := pendings[i]
		s.eng.HandlePending(now, &pc)
	}
}
