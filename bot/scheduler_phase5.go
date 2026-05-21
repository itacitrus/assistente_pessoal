package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// =========================================================================
// Fase 5 — scheduler jobs longitudinais
// =========================================================================
//
// 2 jobs novos:
//
//   checkInactivityEscalation  — cron 1-min, gating shouldRunPhase5(30min).
//                                Detecta idosos que nao respondem a tentativa
//                                proativa ha mais de N horas (default 4) e
//                                dispara alerta pra responsaveis com
//                                NotifyOnInactivity=true. Idempotente via
//                                HasOpenInactivityEscalation(user, attempt).
//
//   runDailyPsychSnapshotCatchup — cron 1-hora, gating shouldRunPhase5(60min).
//                                  Pra cada idoso ativo com mensagens hoje
//                                  mas sem snapshot, chama snapshot writer.
//                                  Garante 1 row/dia/idoso ativo.
//
// Estado interno do scheduler (lastRun map) eh in-memory; nao sobrevive
// restart. Aceitavel: um restart quase nunca acontece dentro de uma janela
// de 30min, e mesmo se acontecer, idempotencia em DB protege.

// phase5State agrega estado runtime exclusivo dos jobs Fase 5. Mantido em
// arquivo separado pra nao poluir Scheduler com campos irrelevantes pros
// jobs antigos.
type phase5State struct {
	// mu protege lastRun e snapshotWriterFn.
	mu      sync.Mutex
	lastRun map[string]time.Time
	// snapshotWriterFn eh o gancho injetado por main.go pra que o catchup
	// possa rodar o snapshot writer real (Haiku). Se nil, catchup skipa
	// — defesa em profundidade pra ambientes sem LLM.
	snapshotWriterFn SnapshotWriter
}

// p5State eh a instancia singleton. Uso intencional de package var pra
// evitar adicionar campos em Scheduler — o Scheduler ja eh grande, e a
// Fase 5 quer poder testar os jobs sem mexer no struct.
var p5State = &phase5State{lastRun: map[string]time.Time{}}

// SetSnapshotWriterForCatchup injeta o snapshot writer impl pro job de
// catchup. Chamado por main.go apos construir o agent.
func SetSnapshotWriterForCatchup(w SnapshotWriter) {
	p5State.mu.Lock()
	defer p5State.mu.Unlock()
	p5State.snapshotWriterFn = w
}

// shouldRunPhase5 retorna true se o cooldown da chave passou. Cooldown
// roda em memoria — restart reseta. Aceitavel pros jobs Fase 5 (idempotentes
// no DB).
func shouldRunPhase5(key string, cooldown time.Duration) bool {
	p5State.mu.Lock()
	defer p5State.mu.Unlock()
	last, ok := p5State.lastRun[key]
	now := time.Now()
	if !ok || now.Sub(last) >= cooldown {
		p5State.lastRun[key] = now
		return true
	}
	return false
}

// resetPhase5State eh helper de testes — limpa cooldown.
func resetPhase5State() {
	p5State.mu.Lock()
	defer p5State.mu.Unlock()
	p5State.lastRun = map[string]time.Time{}
}

// =========================================================================
// checkInactivityEscalation
// =========================================================================

// inactivityEscalationThreshold eh o tempo minimo apos a tentativa proativa
// (Fase 4: Zello puxou conversa) sem resposta antes de escalar para a familia
// (Fase 5: alertar responsavel). Default 4h.
//
// IMPORTANTE: este threshold eh DIFERENTE de users.inactivity_threshold_hours
// (Fase 4) — aquela coluna define quando Zello puxa conversa (default 24h);
// esta constante define a janela pos-puxada antes de avisar o responsavel.
// Sao decisoes distintas pra evitar alarme falso quando idoso so esta dormindo.
const inactivityEscalationThreshold = 4 * time.Hour

func (s *Scheduler) checkInactivityEscalation() {
	if !shouldRunPhase5("inactivity_escalation", 30*time.Minute) {
		return
	}
	elders, err := s.db.ListUsersByType(UserTypeIdoso)
	if err != nil {
		log.Printf("Scheduler[inactivity_esc]: list elders: %v", err)
		return
	}
	for i := range elders {
		s.checkInactivityEscalationForElder(&elders[i])
	}
}

// checkInactivityEscalationForElder processa 1 idoso. Separado pra teste.
func (s *Scheduler) checkInactivityEscalationForElder(elder *User) {
	threshold := inactivityEscalationThreshold

	attempt, err := s.db.GetLatestProactiveAttempt(elder.ID)
	if err != nil {
		log.Printf("Scheduler[inactivity_esc] %s: GetLatestProactiveAttempt: %v", elder.Name, err)
		return
	}
	if attempt == nil {
		return // nunca puxou conversa
	}
	// Idoso ja respondeu: scheduler vai marcar como 'replied' via
	// MarkUserMessageReceivedAndProactive. Nao escala.
	if attempt.Status == "replied" {
		return
	}
	// Idoso enviou mensagem APOS a tentativa — escalou pra outra coisa,
	// nao precisa alarmar.
	if elder.LastUserMessageAt != nil && elder.LastUserMessageAt.After(attempt.AttemptedAt) {
		return
	}
	// Janela ainda nao fechou.
	if time.Since(attempt.AttemptedAt) < threshold {
		return
	}

	guardians, err := s.db.GetGuardiansForInactivity(elder.ID)
	if err != nil {
		log.Printf("Scheduler[inactivity_esc] %s: GetGuardiansForInactivity: %v", elder.Name, err)
		return
	}
	if len(guardians) == 0 {
		return // sem responsaveis opted-in
	}

	now := time.Now().UTC()
	for _, g := range guardians {
		if g.Other == nil {
			continue
		}
		exists, err := s.db.HasOpenInactivityEscalation(elder.ID, attempt.ID)
		if err != nil {
			log.Printf("Scheduler[inactivity_esc] %s: HasOpenInactivityEscalation: %v", elder.Name, err)
			continue
		}
		if exists {
			continue
		}

		hours := int(time.Since(attempt.AttemptedAt).Hours())
		msg := buildInactivityEscalationMsg(elder, &g, hours)

		details := fmt.Sprintf("attempt_id=%d|hours=%d|guardian=%s", attempt.ID, hours, g.Other.Name)
		escID, err := s.db.CreateInactivityEscalation(
			elder.ID, g.Other.ID, attempt.ID, "warn", details, now,
		)
		if err != nil {
			log.Printf("Scheduler[inactivity_esc] %s: CreateInactivityEscalation: %v", elder.Name, err)
			continue
		}

		if s.sendMsg == nil {
			s.db.UpdateEscalationStatus(escID, "failed")
			continue
		}
		if err := s.sendMsg(g.Other.PhoneNumber, msg); err != nil {
			log.Printf("Scheduler[inactivity_esc] %s: send to %s: %v", elder.Name, g.Other.Name, err)
			s.db.UpdateEscalationStatus(escID, "failed")
			continue
		}

		// Audit estruturado pra observabilidade.
		if s.agent != nil && s.agent.audit != nil {
			s.agent.audit.Log(elder.ID, "inactivity_escalation_triggered", g.Other.Name,
				fmt.Sprintf("attempt_id=%d|escalation_id=%d|hours=%d", attempt.ID, escID, hours))
		}
		log.Printf("Scheduler[inactivity_esc] %s: escalou pra %s apos %dh sem resposta",
			elder.Name, g.Other.Name, hours)
	}
}

// buildInactivityEscalationMsg monta o texto WhatsApp pra responsavel.
// Tom: gentil, nao alarmista. Inactivity nao eh emergencia — pode ter
// muitos motivos (viagem, telefone descarregado).
func buildInactivityEscalationMsg(elder *User, link *FamilyLink, hoursIdle int) string {
	rel := "familiar"
	if link != nil {
		rel = relationshipPT(link.Relationship)
	}
	guardianFirst := "Oi"
	if link != nil && link.Other != nil {
		guardianFirst = "Oi " + firstName(link.Other.Name)
	}
	return fmt.Sprintf(
		"%s, sua %s %s não responde ao Zello há %s. Tentei puxar conversa sem sucesso. Pode ser nada — telefone descarregado, viagem — mas vale dar uma ligada.",
		guardianFirst, rel, elder.Name, humanizeIdleHours(hoursIdle),
	)
}

// relationshipPT mapeia a relacao do family_link pra termo amigavel pt-BR.
// Preserva genero quando souber via sufixo _de.
func relationshipPT(rel string) string {
	switch rel {
	case "filho_de", "filha_de", "filha", "filho":
		return "mãe"
	case "marido_de", "marido":
		return "esposa"
	case "esposa_de", "esposa":
		return "marido"
	case "neto_de", "neta_de", "neto", "neta":
		return "avó"
	case "sobrinho_de", "sobrinha_de", "sobrinha", "sobrinho":
		return "tia"
	default:
		return "familiar"
	}
}

// humanizeIdleHours converte horas em texto humano.
func humanizeIdleHours(h int) string {
	if h < 1 {
		return "menos de uma hora"
	}
	if h == 1 {
		return "1 hora"
	}
	if h < 24 {
		return fmt.Sprintf("%d horas", h)
	}
	days := h / 24
	if days == 1 {
		return "1 dia"
	}
	return fmt.Sprintf("%d dias", days)
}

// =========================================================================
// runDailyPsychSnapshotCatchup
// =========================================================================
//
// Roda a cada hora. Pra cada idoso ativo com mensagens "ontem em local TZ"
// mas sem linha em psych_state_daily, chama o snapshot writer (via SnapshotWriter
// interface — mesma impl que o handler usa).
//
// Idempotencia: writer faz UPSERT no banco. Se rodou ha pouco e ja existe
// snapshot com confidence>=2, pulamos pra economizar Haiku.

func (s *Scheduler) runDailyPsychSnapshotCatchup() {
	if !shouldRunPhase5("psych_snapshot_catchup", 60*time.Minute) {
		return
	}
	p5State.mu.Lock()
	writer := p5State.snapshotWriterFn
	p5State.mu.Unlock()
	if writer == nil {
		// Sem writer injetado, nao tem o que fazer. Logamos uma vez por
		// hora — eh esperado em ambientes sem Haiku.
		log.Printf("Scheduler[psych_catchup]: snapshot writer nao injetado — skip")
		return
	}

	yesterday := time.Now().Add(-24 * time.Hour)
	elders, err := s.db.GetUsersWithMessagesOnDay(yesterday)
	if err != nil {
		log.Printf("Scheduler[psych_catchup]: list elders with msgs: %v", err)
		return
	}
	for i := range elders {
		s.catchupSnapshotForElder(writer, &elders[i], yesterday)
	}
}

// catchupSnapshotForElder roda 1 idoso. Le snapshot existente; se ja tem
// confidence>=2, skip. Caso contrario, chama writer (que faz UPSERT).
//
// Reusa SnapshotWriter pra eliminar duplicacao com o caminho do handler —
// MaybeUpdateSnapshot ja faz toda a pipeline.
func (s *Scheduler) catchupSnapshotForElder(writer SnapshotWriter, elder *User, day time.Time) {
	tz := s.db.GetEventTimezone(elder.ID, day)
	if tz == nil {
		tz = BRT()
	}
	localDay := day.In(tz)
	dayDate := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 0, 0, 0, 0, tz)

	existing, err := s.db.GetSnapshot(elder.ID, dayDate)
	if err != nil {
		log.Printf("Scheduler[psych_catchup] %s: GetSnapshot: %v", elder.Name, err)
		return
	}
	if existing != nil && existing.Confidence >= 2 {
		return // ja temos snapshot razoavel
	}

	// Defesa em profundidade: se consent revoked em todos os links, skip.
	guardians, _ := s.db.GetGuardians(elder.ID)
	if len(guardians) > 0 && allGuardiansConsentRevoked(s.db, guardians) {
		log.Printf("Scheduler[psych_catchup] %s: consent revoked em todos os links — skip", elder.Name)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := writer.MaybeUpdateSnapshot(ctx, elder.ID); err != nil {
		// Audit ja foi feito pelo writer; aqui so log nivel scheduler.
		log.Printf("Scheduler[psych_catchup] %s: MaybeUpdateSnapshot: %v", elder.Name, err)
		return
	}
}

// allGuardiansConsentRevoked retorna true se NAO ha nenhum link com
// consent='active'. Se a lista estiver vazia, retorna false (sem responsaveis,
// mas mantemos o snapshot pra historico futuro).
func allGuardiansConsentRevoked(db *DB, guardians []FamilyLink) bool {
	if len(guardians) == 0 {
		return false
	}
	for _, g := range guardians {
		consent, _ := db.GetDependentConsent(g.GuardianID, g.DependentID)
		if consent != ConsentRevoked {
			return false
		}
	}
	return true
}

