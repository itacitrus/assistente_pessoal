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
// PRINCIPIO DE SEGURANCA FARMACOLOGICA (regra dura — vide plano §9):
//
// O bot NUNCA recomenda tomar a dose atrasada nem "compensar" doses perdidas.
// A decisao de tomar uma dose fora do horario cabe ao medico (ou em segunda
// instancia a familia que pode contata-lo) — nunca ao bot. Algumas drogas
// tem janela curta de seguranca (paracetamol+ibuprofeno, anticoagulantes,
// anti-hipertensivos como Losartana, hipoglicemiantes); dose dupla acidental
// pode causar dano serio.
//
// Consequencias praticas — todas codificadas em insistMsg/finalFamilyMsg:
//
//   1. Mensagens de escalacao NAO contem "ainda da tempo", "tome agora",
//      "compense a dose", "nao esqueca de tomar". Tom neutro/cuidadoso.
//   2. Mensagem final (attempt = MaxAttempts) explicita "vou anotar como
//      nao tomada" e encaminha pra medico/familia.
//   3. Mensagem ao guardian explicita "anotei como dose nao tomada" e diz
//      textualmente que o bot "nao oriento" sobre compensacao.
//   4. Quando idoso responde "tomei agora, atrasado" (cf. tool
//      marcar_remedio_tomado), o bot registra `taken` mas resposta eh
//      neutra — "anotado" — sem reforco positivo. Vive no system prompt
//      da persona companion (Fase 4) para o caso conversacional.
//
// Como adicionar politica nova:
//   1. Adicione entrada em escalationPolicies (map abaixo).
//   2. Defina MaxAttempts/Interval/EscalateTo/EscalationMsg.
//   3. Reuse insistMsg/finalFamilyMsg ou escreva novas, respeitando o
//      principio acima (regex de TestEscalationMessages_DoNotPushLateDose
//      vai pegar mensagem que viole).
//
// Engine eh stateless: toda decisao deriva de estado em DB. Restart no
// meio do fluxo retoma do attempt persistido em pending_confirmations.

// escalationPolicies eh o registry global. Politica como dado: chave =
// nome usado em pending_confirmations.escalation_policy.
var escalationPolicies = map[string]EscalationPolicy{
	"medication_default": {
		Name:        "medication_default",
		MaxAttempts: 3,
		Interval:    5 * time.Minute,
		EscalateTo:  EscalateToFamily,
		EscalationMsg: func(ec EscalationContext) string {
			if ec.IsFinalEscalation {
				return finalFamilyMsg(ec)
			}
			// Ultima tentativa ao proprio user antes da escalacao a familia:
			// mensagem "vou anotar". Como MaxAttempts varia por politica, usamos
			// AttemptNumber pra detectar (vide note no engine — o engine passa
			// AttemptNumber == MaxAttempts no ultimo handle do usuario).
			if ec.AttemptNumber >= 3 {
				return lastUserMsg(ec)
			}
			return insistMsg(ec)
		},
	},
	"medication_critical": {
		Name:        "medication_critical",
		MaxAttempts: 5,
		Interval:    3 * time.Minute,
		EscalateTo:  EscalateToFamily,
		EscalationMsg: func(ec EscalationContext) string {
			if ec.IsFinalEscalation {
				return finalFamilyMsg(ec)
			}
			if ec.AttemptNumber >= 5 {
				return lastUserMsg(ec)
			}
			return insistMsg(ec)
		},
	},
}

// insistMsg gera tom progressivamente mais cuidadoso conforme attempt sobe.
// Vide regra dura no topo do arquivo: NUNCA orientar dose tardia.
//
// Esta funcao trata as tentativas 1..N-1 (intermediarias). A ultima tentativa
// (attempt==MaxAttempts) usa lastUserMsg (com "vou anotar"). A mensagem ao
// guardian pos-escalacao usa finalFamilyMsg.
func insistMsg(ec EscalationContext) string {
	name := firstName(ec.User.Name)
	medName := "o remedio"
	if ec.Medication != nil {
		medName = ec.Medication.Name
	}
	switch ec.AttemptNumber {
	case 1:
		return fmt.Sprintf("Hora do %s, %s. Me avisa quando tomar, sem pressa.", medName, name)
	case 2:
		return fmt.Sprintf("%s, tudo bem por ai? Me avisa quando puder.", name)
	case 3:
		return fmt.Sprintf("%s, ainda nao tive noticia sobre o %s. Aconteceu alguma coisa? Estou aqui.", name, medName)
	case 4:
		return fmt.Sprintf("%s, fiquei pensando em voce. Me avisa quando puder, mesmo que so um \"oi\".", name)
	}
	return fmt.Sprintf("%s, esta tudo bem por ai?", name)
}

// lastUserMsg eh enviada no AttemptNumber == MaxAttempts: avisa que vai
// anotar como nao tomada e — explicitamente — NAO orienta tomar atrasado.
// Usada por todas as politicas que marcam essa mesma fronteira.
func lastUserMsg(ec EscalationContext) string {
	name := firstName(ec.User.Name)
	return fmt.Sprintf(
		"%s, vou anotar essa dose como nao tomada e avisar a familia. "+
			"Por seguranca, nao oriento sobre dose atrasada — se for o caso, fale com seu medico ou familia antes.",
		name,
	)
}

// finalFamilyMsg eh a mensagem ao guardian quando escala. Tom sobrio,
// factual; deixa a decisao clinica com a familia/medico, nao sugere acao
// especifica.
func finalFamilyMsg(ec EscalationContext) string {
	elderName := firstName(ec.User.Name)
	medName := "o remedio"
	if ec.Medication != nil {
		medName = ec.Medication.Name
	}
	timeStr := ec.ScheduledAt.In(BRT()).Format("15h")
	return fmt.Sprintf(
		"Oi. %s nao confirmou que tomou %s das %s e nao respondeu apos varias tentativas. "+
			"Anotei como dose nao tomada. Vale falar com %s e, se necessario, conferir com o medico "+
			"se essa dose deve ou nao ser compensada — eu nao oriento isso por seguranca.",
		elderName, medName, timeStr, elderName,
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

	// Janela de intervalo: ja tentamos? esperou tempo suficiente?
	if pc.LastAttemptAt != nil && now.Sub(*pc.LastAttemptAt) < pol.Interval {
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

	nextAttempt := pc.AttemptNumber + 1
	exhausted := nextAttempt > pol.MaxAttempts

	if exhausted {
		switch pol.EscalateTo {
		case EscalateToFamily:
			e.escalateToFamily(now, pc, user, med, pol)
		default:
			// EscalateToSelfOnly ou EscalateToNone e atingiu o teto.
			e.markMissedAndResolve(pc)
		}
		return
	}

	// Insistencia ao proprio user.
	ec := EscalationContext{
		User:          user,
		Medication:    med,
		ScheduledAt:   medScheduledAt(pc),
		AttemptNumber: nextAttempt,
		Recipient:     user,
	}
	msg := pol.EscalationMsg(ec)

	if err := e.notifier.Send(context.Background(), user, msg); err != nil {
		log.Printf("escalation pc %d: notifier failed: %v", pc.ID, err)
		// Nao bumpa attempt — proxima rodada tenta de novo. Permite
		// recovery em queda momentanea do canal de envio.
		return
	}

	if err := e.db.RecordEscalationAttempt(pc.ID, pol.Name, nextAttempt, user.ID, e.notifier.Channel(), now); err != nil {
		// UNIQUE = ja registrou, ok. Outros = log e segue (nao reverte
		// envio, ja foi).
		if !errors.Is(err, ErrIntakeLogDuplicate) {
			log.Printf("escalation pc %d: record attempt: %v", pc.ID, err)
		}
	}
	if err := e.db.UpdatePendingAttempt(pc.ID, nextAttempt, now); err != nil {
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
func (e *EscalationEngine) escalateToFamily(now time.Time, pc *PendingConfirmation, user *User, med *Medication, pol EscalationPolicy) {
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
	finalAttempt := pc.AttemptNumber + 1
	for i := range guardians {
		g := guardians[i]
		ec := EscalationContext{
			User:              user,
			Medication:        med,
			ScheduledAt:       scheduledAt,
			AttemptNumber:     finalAttempt,
			Recipient:         &g,
			IsFinalEscalation: true,
		}
		msg := pol.EscalationMsg(ec)
		if err := e.notifier.Send(context.Background(), &g, msg); err != nil {
			log.Printf("escalation pc %d: notify guardian %d: %v", pc.ID, g.ID, err)
			continue
		}
		if err := e.db.RecordEscalationAttempt(pc.ID, pol.Name, finalAttempt, g.ID, e.notifier.Channel(), now); err != nil {
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
