package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// =========================================================================
// EscalationEngine (Fase 3)
// =========================================================================
//
// PRINCIPIO DE SEGURANCA FARMACOLOGICA:
//
// Por padrao (late_dose_policy='consult_doctor') o bot NAO recomenda tomar a
// dose atrasada nem "compensar" — a decisao cabe ao medico. Algumas drogas tem
// janela curta de seguranca (paracetamol+ibuprofeno, anticoagulantes,
// anti-hipertensivos, hipoglicemiantes) e dose dupla acidental pode dar dano.
//
// EXCECAO (Fase 3.1): quando o RESPONSAVEL configura uma late_dose_policy
// explicita no medicamento, o bot RELATA essa orientacao ao idoso deixando
// claro que eh "recomendacao do responsavel, nao orientacao medica". O bot
// nunca age sozinho — quem decide tomar/pular eh sempre o idoso. Essa parte
// vive no chat livre (system prompt da persona) e nas tools, NAO neste motor.
//
// Este motor (escalacao automatica) segue regras de comunicacao:
//   1. Mensagens ao idoso NUNCA contem "ainda da tempo", "tome agora",
//      "compense a dose". Tom neutro/cuidadoso. (TestEscalationMessages_*)
//   2. Cadencia dirigida pela TOLERANCIA do medicamento: deadline =
//      scheduled_at + tolerance_minutes. Antes do deadline, no maximo UM
//      lembrete gentil (no horario que o idoso disse, se adiou; senao no meio
//      da janela). Sem cobranca repetida — evita parecer ansioso.
//   3. A familia eh avisada EM SEGREDO no deadline. As mensagens ao idoso
//      NUNCA mencionam que a familia sera/foi avisada (nada de "ameaca").
//   4. A mensagem ao guardian eh VERDADEIRA: reflete se o idoso adiou (e pra
//      quando) em vez de afirmar falsamente "nao respondeu".
//
// Engine eh stateless: toda decisao deriva de estado em DB. Restart no meio
// do fluxo retoma do estado persistido em pending_confirmations.

// escalationPolicies eh o registry global. Politica como dado: chave =
// nome usado em pending_confirmations.escalation_policy. EscalateTo define
// quem recebe ao expirar a tolerancia.
var escalationPolicies = map[string]EscalationPolicy{
	"medication_default": {
		Name:        "medication_default",
		MaxAttempts: 1,
		Interval:    5 * time.Minute,
		EscalateTo:  EscalateToFamily,
	},
	"medication_critical": {
		Name:        "medication_critical",
		MaxAttempts: 1,
		Interval:    3 * time.Minute,
		EscalateTo:  EscalateToFamily,
	},
}

// gentleNudgeMsg eh o UNICO lembrete gentil dentro da janela de tolerancia.
// Tom leve, sem pressa, sem mencionar familia, sem orientar dose atrasada.
func gentleNudgeMsg(ec EscalationContext) string {
	name := firstName(ec.User.Name)
	medName := "o remédio"
	if ec.Medication != nil {
		medName = ec.Medication.Name
	}
	return fmt.Sprintf("%s, passando pra lembrar do %s. Sem pressa — me avisa quando tomar.", name, medName)
}

// familyMissMsg eh a mensagem SECRETA ao guardian quando a tolerancia expira
// sem confirmacao. Verdadeira: reflete se o idoso adiou (e pra quando). Tom
// sobrio; deixa a decisao clinica com a familia/medico. NUNCA afirma "nao
// respondeu" — so afirma o que sabemos: nao houve confirmacao da toma.
func familyMissMsg(ec EscalationContext) string {
	elderName := firstName(ec.User.Name)
	medName := "o remédio"
	if ec.Medication != nil {
		medName = ec.Medication.Name
	}
	timeStr := ec.ScheduledAt.In(BRT()).Format("15h")
	if ec.DeferredUntil != nil {
		deferStr := ec.DeferredUntil.In(BRT()).Format("15h04")
		return fmt.Sprintf(
			"Oi. %s disse que tomaria %s das %s mais tarde (por volta das %s), mas até agora não confirmei a toma. "+
				"Anotei como não confirmada. Se achar melhor, vale dar uma olhada e, se precisar, conferir com o médico — "+
				"eu não oriento sobre dose atrasada por segurança.",
			elderName, medName, timeStr, deferStr,
		)
	}
	return fmt.Sprintf(
		"Oi. Ainda não confirmei que %s tomou %s das %s. Anotei como não confirmada. "+
			"Se achar melhor, vale dar uma olhada e, se precisar, conferir com o médico — "+
			"eu não oriento sobre dose atrasada por segurança.",
		elderName, medName, timeStr,
	)
}

// EscalationEngine eh stateless: db + notifier. Toda decisao deriva de
// estado em DB. Engine nao mantem mapa em memoria, lock per-PC, etc.
// Race entre dois ticks eh resolvido pelo UNIQUE em escalations.
type EscalationEngine struct {
	db       *DB
	notifier Notifier
}

// NewEscalationEngine constroi o engine. n=nil eh aceito apenas pra testes
// que validam metodos puros (estado, helpers); chamadas de Send vao panicar.
func NewEscalationEngine(db *DB, n Notifier) *EscalationEngine {
	return &EscalationEngine{db: db, notifier: n}
}

// HandlePending decide se essa pending deve receber nova tentativa, escalar
// para familia, ou ser deixada em paz (intervalo ainda nao fechou).
//
// Fluxo:
//   1. Politica desconhecida ou nil → return (sem escalacao).
//   2. last_attempt_at != nil && now - last < Interval → return (cedo).
//   3. nextAttempt > MaxAttempts && EscalateTo=family → escalateToFamily.
//   4. nextAttempt > MaxAttempts && outro → markMissedAndResolve.
//   5. caso comum → manda mensagem ao proprio user, registra escalation,
//      bumpa attempt em pending_confirmations.
func (e *EscalationEngine) HandlePending(now time.Time, pc *PendingConfirmation) {
	if pc == nil {
		return
	}
	if pc.EscalationPolicy == nil || *pc.EscalationPolicy == "" {
		return // sem politica = sem escalacao
	}
	pol, ok := escalationPolicies[*pc.EscalationPolicy]
	if !ok {
		log.Printf("escalation: unknown policy %q on pending %d", *pc.EscalationPolicy, pc.ID)
		return
	}

	user, err := e.db.GetUserByID(pc.UserID)
	if err != nil {
		log.Printf("escalation pc %d: user lookup: %v", pc.ID, err)
		return
	}

	var med *Medication
	if pc.Kind == "medication" {
		mi := parseMedicationIntent(pc)
		if mi != nil && mi.MedicationID > 0 {
			m, mErr := e.db.GetMedicationByID(mi.MedicationID)
			if mErr == nil {
				med = m
			}
		}
	}

	scheduledAt := medScheduledAt(pc)
	tolerance := DefaultToleranceMinutes
	if med != nil && med.ToleranceMinutes > 0 {
		tolerance = med.ToleranceMinutes
	}
	deadline := scheduledAt.Add(time.Duration(tolerance) * time.Minute)

	// Deadline da tolerancia: a familia eh avisada em segredo, e a dose vai
	// pra 'nao confirmada'. A familia NUNCA eh avisada antes disto.
	if !now.Before(deadline) {
		switch pol.EscalateTo {
		case EscalateToFamily:
			e.escalateToFamily(now, pc, user, med)
		default:
			e.markMissedAndResolve(pc)
		}
		return
	}

	// Dentro da janela de tolerancia: no maximo UM lembrete gentil.
	if pc.AttemptNumber >= 1 {
		return // ja cutucamos uma vez; silencio ate o deadline
	}

	// Quando cutucar: no horario que o idoso disse (se adiou), senao no meio
	// da janela de tolerancia. Evita cobrar logo de cara e parecer ansioso.
	nudgeAt := scheduledAt.Add(time.Duration(tolerance) * time.Minute / 2)
	if pc.DeferredUntil != nil {
		nudgeAt = *pc.DeferredUntil
	}
	if now.Before(nudgeAt) {
		return
	}

	ec := EscalationContext{
		User:          user,
		Medication:    med,
		ScheduledAt:   scheduledAt,
		Recipient:     user,
		DeferredUntil: pc.DeferredUntil,
	}
	if err := e.notifier.Send(context.Background(), user, gentleNudgeMsg(ec)); err != nil {
		log.Printf("escalation pc %d: notifier failed: %v", pc.ID, err)
		// Nao bumpa attempt — proxima rodada tenta de novo. Recovery em
		// queda momentanea do canal de envio.
		return
	}
	if err := e.db.RecordEscalationAttempt(pc.ID, pol.Name, 1, user.ID, e.notifier.Channel(), now); err != nil {
		if !errors.Is(err, ErrIntakeLogDuplicate) {
			log.Printf("escalation pc %d: record attempt: %v", pc.ID, err)
		}
	}
	if err := e.db.UpdatePendingAttempt(pc.ID, 1, now); err != nil {
		log.Printf("escalation pc %d: update pending: %v", pc.ID, err)
	}
}

// escalateToFamily envia mensagem para guardians vinculados em family_links
// com notify_on_medication_miss=1. Marca intake_log como 'escalated' e
// resolve pending. Audita o evento.
//
// Sem guardian → markMissedAndResolve (sem alerta, mas log de missed).
//
// Cada guardian eh uma row separada em escalations (UNIQUE pega duplicidade).
// Falha de envio individual nao impede tentativa pra outros guardians.
func (e *EscalationEngine) escalateToFamily(now time.Time, pc *PendingConfirmation, user *User, med *Medication) {
	guardians, err := e.db.ListGuardiansForUser(user.ID, "notify_on_medication_miss")
	if err != nil {
		log.Printf("escalation pc %d: list guardians: %v", pc.ID, err)
	}

	if len(guardians) == 0 {
		// Sem familia pra avisar. Marca como missed e segue.
		e.markMissedAndResolve(pc)
		return
	}

	scheduledAt := medScheduledAt(pc)
	const familyAttempt = 2 // 1 = lembrete gentil ao idoso; 2 = aviso a familia
	policyName := "medication_default"
	if pc.EscalationPolicy != nil {
		policyName = *pc.EscalationPolicy
	}
	for i := range guardians {
		g := guardians[i]
		ec := EscalationContext{
			User:          user,
			Medication:    med,
			ScheduledAt:   scheduledAt,
			Recipient:     &g,
			DeferredUntil: pc.DeferredUntil,
		}
		msg := familyMissMsg(ec)
		if err := e.notifier.Send(context.Background(), &g, msg); err != nil {
			log.Printf("escalation pc %d: notify guardian %d: %v", pc.ID, g.ID, err)
			continue
		}
		if err := e.db.RecordEscalationAttempt(pc.ID, policyName, familyAttempt, g.ID, e.notifier.Channel(), now); err != nil {
			if !errors.Is(err, ErrIntakeLogDuplicate) {
				log.Printf("escalation pc %d: record family attempt: %v", pc.ID, err)
			}
		}
	}

	// Atualiza intake_log para 'escalated', resolve pending.
	if pc.Kind == "medication" {
		mi := parseMedicationIntent(pc)
		if mi != nil && mi.MedicationID > 0 {
			if err := e.db.UpdateIntakeStatus(mi.MedicationID, mi.ScheduledAt, IntakeEscalated, ""); err != nil {
				log.Printf("escalation pc %d: update intake escalated: %v", pc.ID, err)
			}
		}
	}
	if err := e.db.ResolvePendingConfirmation(pc.ID, "escalated"); err != nil {
		log.Printf("escalation pc %d: resolve: %v", pc.ID, err)
	}
	NewAuditLog(e.db).Log(user.ID, "medication_escalated", "",
		fmt.Sprintf("pc=%d guardians=%d", pc.ID, len(guardians)))
}

// markMissedAndResolve marca intake_log como 'missed', resolve pending e
// audita. Usado quando esgotou tentativas sem guardian, ou quando politica
// nao define escalacao pra familia.
func (e *EscalationEngine) markMissedAndResolve(pc *PendingConfirmation) {
	if pc.Kind == "medication" {
		mi := parseMedicationIntent(pc)
		if mi != nil && mi.MedicationID > 0 {
			if err := e.db.UpdateIntakeStatus(mi.MedicationID, mi.ScheduledAt, IntakeMissed, ""); err != nil {
				log.Printf("escalation pc %d: update intake missed: %v", pc.ID, err)
			}
		}
	}
	if err := e.db.ResolvePendingConfirmation(pc.ID, "missed"); err != nil {
		log.Printf("escalation pc %d: resolve missed: %v", pc.ID, err)
	}
	NewAuditLog(e.db).Log(pc.UserID, "medication_missed", "", fmt.Sprintf("pc=%d", pc.ID))
}
