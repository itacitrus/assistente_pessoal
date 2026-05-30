package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
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

// Acoes que classify pode decidir para um pending.
const (
	actNone = iota
	actNudge
	actEscalate
)

// pendingItem casa um pending com o medicamento + instante resolvidos, pra
// processar (e agrupar mensagens) sem reler o DB.
type pendingItem struct {
	pc          *PendingConfirmation
	pol         EscalationPolicy
	med         *Medication // pode ser nil (lookup falhou)
	scheduledAt time.Time
}

// nudgeEngagementGrace eh a janela apos a ultima fala do idoso em que NAO mandamos
// o cutucao gentil — ele acabou de interagir, cutucar soa como "ignorei sua
// resposta". Derivada da tolerancia: min(20min, tolerance/2). O teto em tolerance/2
// eh deliberado: garante que a supressao nunca empurre o cutucao para depois do
// deadline (scheduledAt+tolerance), onde a familia ja seria avisada — engajamento
// adia o cutucao, NUNCA silencia a rede de seguranca real.
func nudgeEngagementGrace(toleranceMinutes int) time.Duration {
	if toleranceMinutes <= 0 {
		toleranceMinutes = DefaultToleranceMinutes
	}
	half := time.Duration(toleranceMinutes) * time.Minute / 2
	if cap := 20 * time.Minute; half > cap {
		return cap
	}
	return half
}

// classify decide o que fazer com um pending AGORA, sem efeito colateral: nudge
// gentil, escalar (deadline), ou nada. Retorna o item resolvido (med +
// scheduledAt) pra quem for executar/agrupar. ok=false quando o pending nao
// tem politica valida (sem escalacao). user (pode ser nil) traz LastUserMessageAt
// para a supressao de cutucao por engajamento — so afeta o ramo do nudge.
func (e *EscalationEngine) classify(now time.Time, user *User, pc *PendingConfirmation) (action int, item pendingItem, ok bool) {
	if pc == nil || pc.EscalationPolicy == nil || *pc.EscalationPolicy == "" {
		return actNone, pendingItem{}, false
	}
	pol, found := escalationPolicies[*pc.EscalationPolicy]
	if !found {
		log.Printf("escalation: unknown policy %q on pending %d", *pc.EscalationPolicy, pc.ID)
		return actNone, pendingItem{}, false
	}

	var med *Medication
	if pc.Kind == "medication" {
		mi := parseMedicationIntent(pc)
		if mi != nil && mi.MedicationID > 0 {
			if m, mErr := e.db.GetMedicationByID(mi.MedicationID); mErr == nil {
				med = m
			}
		}
	}
	scheduledAt := medScheduledAt(pc)
	tolerance := DefaultToleranceMinutes
	if med != nil && med.ToleranceMinutes > 0 {
		tolerance = med.ToleranceMinutes
	}
	item = pendingItem{pc: pc, pol: pol, med: med, scheduledAt: scheduledAt}

	deadline := scheduledAt.Add(time.Duration(tolerance) * time.Minute)
	if !now.Before(deadline) {
		return actEscalate, item, true
	}

	// Dentro da janela: no maximo UM lembrete gentil.
	if pc.AttemptNumber >= 1 {
		return actNone, item, true
	}
	// Quando cutucar: no horario que o idoso disse (se adiou), senao no meio da
	// janela de tolerancia.
	nudgeAt := scheduledAt.Add(time.Duration(tolerance) * time.Minute / 2)
	if pc.DeferredUntil != nil {
		nudgeAt = *pc.DeferredUntil
	}
	if now.Before(nudgeAt) {
		return actNone, item, true
	}
	// Supressao por engajamento: se o idoso interagiu DEPOIS do lembrete (apos
	// scheduledAt) e ha pouco (dentro da grace), nao cutuca — ele esta presente,
	// acabou de responder. Apenas ADIA (re-dispara em ticks seguintes se ainda
	// <deadline). NUNCA toca a escalacao a familia, ja avaliada no deadline acima.
	if user != nil && user.LastUserMessageAt != nil {
		lum := user.LastUserMessageAt.UTC()
		if lum.After(scheduledAt) && now.Sub(lum) < nudgeEngagementGrace(tolerance) {
			return actNone, item, true
		}
	}
	return actNudge, item, true
}

// HandlePending processa UM pending. Mantido pra reuso/teste — em producao o
// scheduler chama ProcessPendings (agrupa mensagens por usuario). Aqui o grupo
// eh de tamanho 1, entao as mensagens saem na forma simples (single-med).
func (e *EscalationEngine) HandlePending(now time.Time, pc *PendingConfirmation) {
	user, err := e.db.GetUserByID(pc.UserID)
	if err != nil {
		log.Printf("escalation pc %d: user lookup: %v", pc.ID, err)
		return
	}
	action, item, ok := e.classify(now, user, pc)
	if !ok || action == actNone {
		return
	}
	switch action {
	case actNudge:
		e.sendNudges(now, user, []pendingItem{item})
	case actEscalate:
		if item.pol.EscalateTo == EscalateToFamily {
			e.escalateToFamily(now, user, []pendingItem{item})
		} else {
			e.markMissed([]pendingItem{item})
		}
	}
}

// ProcessPendings eh o caminho de PRODUCAO: agrupa os pendings por usuario e
// MANDA UMA MENSAGEM por etapa (lembrete gentil agrupado; aviso a familia
// agrupado por guardiao). O controle continua granular — cada pending tem seu
// proprio intake_log/resolucao —, so a mensagem eh agrupada.
func (e *EscalationEngine) ProcessPendings(now time.Time, pendings []PendingConfirmation) {
	byUser := map[int64][]*PendingConfirmation{}
	var order []int64
	for i := range pendings {
		pc := &pendings[i]
		if _, seen := byUser[pc.UserID]; !seen {
			order = append(order, pc.UserID)
		}
		byUser[pc.UserID] = append(byUser[pc.UserID], pc)
	}
	for _, uid := range order {
		user, err := e.db.GetUserByID(uid)
		if err != nil {
			log.Printf("escalation: user %d lookup: %v", uid, err)
			continue
		}
		var nudges, family, missed []pendingItem
		for _, pc := range byUser[uid] {
			action, item, ok := e.classify(now, user, pc)
			if !ok {
				continue
			}
			switch action {
			case actNudge:
				nudges = append(nudges, item)
			case actEscalate:
				if item.pol.EscalateTo == EscalateToFamily {
					family = append(family, item)
				} else {
					missed = append(missed, item)
				}
			}
		}
		if len(nudges) > 0 {
			e.sendNudges(now, user, nudges)
		}
		if len(family) > 0 {
			e.escalateToFamily(now, user, family)
		}
		if len(missed) > 0 {
			e.markMissed(missed)
		}
	}
}

// sendNudges manda UM lembrete gentil ao proprio idoso cobrindo todos os
// `items` (1 = forma simples; N = lista agrupada). So bumpa attempt/registra
// apos envio OK — falha de canal deixa pra proxima rodada.
func (e *EscalationEngine) sendNudges(now time.Time, user *User, items []pendingItem) {
	if len(items) == 0 {
		return
	}
	// Re-checa cada pending: nao cutuca dose que foi resolvida (ex: "tomei") entre
	// a leitura do batch (GetActiveMedicationPendings) e agora. Sem isso, o cutucao
	// podia chegar logo depois de o idoso confirmar — exatamente a sensacao de
	// "ignorou minha resposta".
	open := items[:0:0]
	for _, it := range items {
		stillOpen, err := e.db.MedicationPendingStillOpen(it.pc.ID)
		if err != nil {
			log.Printf("escalation pc %d: still-open check: %v", it.pc.ID, err)
			continue
		}
		if stillOpen {
			open = append(open, it)
		}
	}
	if len(open) == 0 {
		return
	}
	items = open

	var msg string
	if len(items) == 1 {
		msg = gentleNudgeMsg(EscalationContext{
			User: user, Medication: items[0].med, ScheduledAt: items[0].scheduledAt,
			Recipient: user, DeferredUntil: items[0].pc.DeferredUntil,
		})
	} else {
		msg = groupedNudgeMsg(user.Name, medNamesOf(items))
	}
	if err := e.notifier.Send(context.Background(), user, msg); err != nil {
		log.Printf("escalation: nudge send to user %d failed: %v", user.ID, err)
		return
	}
	for _, it := range items {
		if err := e.db.RecordEscalationAttempt(it.pc.ID, policyNameOf(it.pc), 1, user.ID, e.notifier.Channel(), now); err != nil {
			if !errors.Is(err, ErrIntakeLogDuplicate) {
				log.Printf("escalation pc %d: record attempt: %v", it.pc.ID, err)
			}
		}
		if err := e.db.UpdatePendingAttempt(it.pc.ID, 1, now); err != nil {
			log.Printf("escalation pc %d: update pending: %v", it.pc.ID, err)
		}
	}
}

// escalateToFamily avisa, EM SEGREDO, os guardians (notify_on_medication_miss=1)
// sobre `items` (1 = msg simples; N = lista agrupada). Cada guardiao recebe UMA
// mensagem cobrindo todos os remedios nao confirmados. Marca cada dose como
// 'escalated' e resolve cada pending. Sem guardiao → markMissed.
func (e *EscalationEngine) escalateToFamily(now time.Time, user *User, items []pendingItem) {
	if len(items) == 0 {
		return
	}
	guardians, err := e.db.ListGuardiansForUser(user.ID, "notify_on_medication_miss")
	if err != nil {
		log.Printf("escalation user %d: list guardians: %v", user.ID, err)
	}
	if len(guardians) == 0 {
		e.markMissed(items)
		return
	}

	// CAS-resolve cada pending para 'escalated' ANTES de avisar a familia. So
	// seguimos com os que ESTE tick resolveu (status era 'pending'): se um "tomei"
	// concorrente ja resolveu o pending entre a leitura do batch e agora, NAO
	// avisamos a familia indevidamente nem regredimos a dose 'taken'.
	live := items[:0:0]
	for _, it := range items {
		won, rerr := e.db.ResolvePendingConfirmationIfPending(it.pc.ID, "escalated")
		if rerr != nil {
			log.Printf("escalation pc %d: resolve: %v", it.pc.ID, rerr)
			continue
		}
		if won {
			live = append(live, it)
		}
	}
	if len(live) == 0 {
		return
	}

	const familyAttempt = 2 // 1 = lembrete gentil ao idoso; 2 = aviso a familia
	for i := range guardians {
		g := guardians[i]
		var msg string
		if len(live) == 1 {
			msg = familyMissMsg(EscalationContext{
				User: user, Medication: live[0].med, ScheduledAt: live[0].scheduledAt,
				Recipient: &g, DeferredUntil: live[0].pc.DeferredUntil,
			})
		} else {
			msg = groupedFamilyMissMsg(user, live)
		}
		if err := e.notifier.Send(context.Background(), &g, msg); err != nil {
			log.Printf("escalation user %d: notify guardian %d: %v", user.ID, g.ID, err)
			continue
		}
		for _, it := range live {
			if err := e.db.RecordEscalationAttempt(it.pc.ID, policyNameOf(it.pc), familyAttempt, g.ID, e.notifier.Channel(), now); err != nil {
				if !errors.Is(err, ErrIntakeLogDuplicate) {
					log.Printf("escalation pc %d: record family attempt: %v", it.pc.ID, err)
				}
			}
		}
	}

	for _, it := range live {
		if it.med != nil {
			if err := e.db.UpdateIntakeStatusIfPending(it.med.ID, it.scheduledAt, IntakeEscalated, ""); err != nil {
				log.Printf("escalation pc %d: update intake escalated: %v", it.pc.ID, err)
			}
		}
		NewAuditLog(e.db).Log(user.ID, "medication_escalated", "",
			fmt.Sprintf("pc=%d guardians=%d", it.pc.ID, len(guardians)))
	}
}

// markMissed marca cada dose de `items` como 'missed' e resolve o pending. Sem
// alerta — usado quando nao ha guardian ou a politica nao escala pra familia.
func (e *EscalationEngine) markMissed(items []pendingItem) {
	for _, it := range items {
		// CAS: so marca missed se ESTE tick resolveu o pending. Se um "tomei"
		// concorrente ja resolveu, nao regride a dose nem audita 'missed'.
		won, err := e.db.ResolvePendingConfirmationIfPending(it.pc.ID, "missed")
		if err != nil {
			log.Printf("escalation pc %d: resolve missed: %v", it.pc.ID, err)
			continue
		}
		if !won {
			continue
		}
		if it.med != nil {
			if err := e.db.UpdateIntakeStatusIfPending(it.med.ID, it.scheduledAt, IntakeMissed, ""); err != nil {
				log.Printf("escalation pc %d: update intake missed: %v", it.pc.ID, err)
			}
		}
		NewAuditLog(e.db).Log(it.pc.UserID, "medication_missed", "", fmt.Sprintf("pc=%d", it.pc.ID))
	}
}

// policyNameOf devolve o nome da politica do pending, com fallback seguro.
func policyNameOf(pc *PendingConfirmation) string {
	if pc.EscalationPolicy != nil && *pc.EscalationPolicy != "" {
		return *pc.EscalationPolicy
	}
	return "medication_default"
}

// medNamesOf extrai os nomes dos medicamentos dos items (fallback "o remédio").
func medNamesOf(items []pendingItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.med != nil {
			out = append(out, it.med.Name)
		} else {
			out = append(out, "o remédio")
		}
	}
	return out
}

// groupedNudgeMsg eh o lembrete gentil agrupado (>=2 remedios). Mesmas regras
// do single: tom leve, sem pressa, sem mencionar familia, sem orientar dose.
func groupedNudgeMsg(userName string, medNames []string) string {
	return fmt.Sprintf("%s, passando pra lembrar dos remédios: %s. Sem pressa — me avisa quando tomar.",
		firstName(userName), joinNames(medNames))
}

// groupedFamilyMissMsg eh o aviso SECRETO agrupado ao guardiao (>=2 remedios).
// Verdadeiro (reflete adiamento por remedio), sobrio, deixa a decisao clinica
// com a familia/medico. Mantem as marcas de seguranca ("nao confirm",
// "nao oriento").
func groupedFamilyMissMsg(user *User, items []pendingItem) string {
	elderName := firstName(user.Name)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		medName := "o remédio"
		if it.med != nil {
			medName = it.med.Name
		}
		p := fmt.Sprintf("%s das %s", medName, it.scheduledAt.In(BRT()).Format("15h"))
		if it.pc.DeferredUntil != nil {
			p += fmt.Sprintf(" (disse que tomaria por volta das %s)", it.pc.DeferredUntil.In(BRT()).Format("15h04"))
		}
		parts = append(parts, p)
	}
	return fmt.Sprintf(
		"Oi. Ainda não confirmei que %s tomou: %s. Anotei como não confirmadas. "+
			"Se achar melhor, vale dar uma olhada e, se precisar, conferir com o médico — "+
			"eu não oriento sobre dose atrasada por segurança.",
		elderName, joinNames(parts),
	)
}

// joinNames junta itens em PT-BR: "A", "A e B", "A, B e C".
func joinNames(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " e " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " e " + items[len(items)-1]
	}
}
