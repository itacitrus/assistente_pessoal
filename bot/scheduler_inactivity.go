package main

import (
	"context"
	"log"
	"time"
)

// =========================================================================
// checkInactivity (Fase 4 — companion + proatividade)
// =========================================================================
//
// Roda a cada minuto (cron 1-min), mas filtra dentro: so executa quando
// minute%15==0. Pra cada idoso ativo:
//
//   1. Trégua manual ainda valida? proactive_paused_until > now → skip.
//   2. Threshold respeitado? hours_since_last_msg < threshold → skip.
//   3. Lock 4h: HasRecentProactiveAttempt(user, 4h) → skip.
//   4. Janela horaria 8h-21h em loc local → skip se fora.
//   5. Gera mensagem via agent.RunProactive.
//   6. Registra em proactive_attempts (lock pessimista) ANTES de enviar.
//   7. Envia via sendMsg. Falha → marca attempt como failed.
//
// Lock 4h sobrevive a restart porque vive em DB. UNIQUE em
// idx_proactive_attempts_user_attempted impede dupla insercao no mesmo
// segundo (na pratica — race window minima dado SQLite single-instance).

const (
	// proactiveLockWindow eh o tempo minimo entre duas tentativas
	// proativas pro mesmo idoso, independente de threshold do user.
	// Garantia: no maximo uma puxada a cada 4h.
	proactiveLockWindow = 4 * time.Hour

	// proactiveDefaultThreshold eh usado quando user.InactivityThresholdHours
	// vem zero/invalido — defesa em profundidade.
	proactiveDefaultThreshold = 24

	// proactiveBackoffWindow: se ja existe uma puxada SEM resposta dentro desta
	// janela, nao cutuca de novo. Evita o caso "mandei 3 mensagens iguais num
	// dia e o idoso nao respondeu nenhuma". Quando ele responde, o status flipa
	// de 'sent' -> 'replied' (MarkUserMessageReceivedAndProactive) e o portao
	// reabre na hora. Resultado: no maximo ~1 puxada nao-respondida por ~dia.
	proactiveBackoffWindow = 20 * time.Hour

	// proactiveRunCtxTimeout eh o teto pra RunProactive. Se o agente
	// (DeepSeek/Anthropic) demorar mais, abortamos — proxima rodada
	// tenta de novo em 15min. Nao bloqueia o cron.
	proactiveRunCtxTimeout = 60 * time.Second
)

// hasUnansweredProactive informa se alguma das tentativas esta 'sent' (enviada
// mas ainda sem resposta). 'replied'/'failed'/'ignored' nao contam.
func hasUnansweredProactive(attempts []ProactiveAttempt) bool {
	for _, a := range attempts {
		if a.Status == "sent" {
			return true
		}
	}
	return false
}

// checkInactivity eh o entry point do cron. Itera sobre idosos ativos.
func (s *Scheduler) checkInactivity() {
	now := s.now()
	// Gate: minute%15==0 e segundos < 30. Cron eh 1-min mas so queremos a
	// cada 15min. Segunda janela do mesmo minuto evitada com second<30.
	if now.Minute()%15 != 0 {
		return
	}
	if now.Second() > 30 {
		return
	}

	if s.agent == nil {
		// agent nao injetado: testes que nao exercitam companion. OK.
		return
	}

	users, err := s.db.ListActiveUsers()
	if err != nil {
		log.Printf("Scheduler[inactivity]: list users: %v", err)
		return
	}

	for i := range users {
		u := users[i]
		if u.Type != UserTypeIdoso {
			continue
		}
		s.checkUserInactivity(&u, now)
	}
}

// checkUserInactivity processa um unico idoso. Separado pra facilitar
// teste em isolamento.
func (s *Scheduler) checkUserInactivity(user *User, now time.Time) {
	// 1. Tregua manual?
	paused, err := s.db.IsProactivePaused(user.ID)
	if err != nil {
		log.Printf("Scheduler[inactivity] %s: IsProactivePaused: %v", user.Name, err)
		return
	}
	if paused {
		return
	}

	// 2. Threshold atingido?
	threshold := user.InactivityThresholdHours
	if threshold <= 0 {
		threshold = proactiveDefaultThreshold
	}
	var lastMsg time.Time
	if user.LastUserMessageAt != nil {
		lastMsg = *user.LastUserMessageAt
	} else {
		// Idoso nunca falou nesta versao do bot. Usar created_at como base
		// — evita disparar no segundo minuto pos-cadastro.
		lastMsg = user.CreatedAt
	}
	hoursIdle := int(now.Sub(lastMsg).Hours())
	if hoursIdle < threshold {
		return
	}

	// 3. Lock 4h via DB.
	recent, err := s.db.HasRecentProactiveAttempt(user.ID, proactiveLockWindow)
	if err != nil {
		log.Printf("Scheduler[inactivity] %s: HasRecentProactiveAttempt: %v", user.Name, err)
		return
	}
	if recent {
		return
	}

	// 3b. Back-off: se ja cutucamos e o idoso nao respondeu (status 'sent'
	// dentro da janela de back-off), nao insiste. Quando ele responder, o
	// status flipa pra 'replied' e o portao reabre. Evita repetir puxada
	// sem retorno (caso Elizabete: 3 mensagens iguais num dia, zero resposta).
	recentAttempts, err := s.db.GetRecentProactiveAttempts(user.ID, proactiveBackoffWindow, 5)
	if err != nil {
		log.Printf("Scheduler[inactivity] %s: GetRecentProactiveAttempts: %v", user.Name, err)
		return
	}
	if hasUnansweredProactive(recentAttempts) {
		return
	}

	// 4. Janela horaria local.
	if !proactiveWindowAllowed(now, BRT()) {
		return
	}

	// 5. Gera mensagem via agent.
	ctx, cancel := context.WithTimeout(context.Background(), proactiveRunCtxTimeout)
	defer cancel()

	msg, err := s.agent.RunProactive(ctx, user, hoursIdle)
	if err != nil {
		log.Printf("Scheduler[inactivity] %s: RunProactive: %v", user.Name, err)
		return
	}
	if msg == "" {
		log.Printf("Scheduler[inactivity] %s: agente decidiu nao puxar conversa", user.Name)
		return
	}

	// 6. Registra ANTES de enviar (lock pessimista). Se enviar falhar,
	// marcamos como 'failed' depois — o registro do attempt fica.
	attemptID, err := s.db.RecordProactiveAttempt(user.ID, msg)
	if err != nil {
		log.Printf("Scheduler[inactivity] %s: RecordProactiveAttempt: %v", user.Name, err)
		return
	}

	// 7. Envia.
	if s.sendMsg == nil {
		log.Printf("Scheduler[inactivity] %s: sendMsg nil — marcando attempt failed", user.Name)
		s.db.MarkProactiveAttemptFailed(attemptID)
		return
	}
	if err := s.sendMsg(user.PhoneNumber, msg); err != nil {
		log.Printf("Scheduler[inactivity] %s: sendMsg: %v", user.Name, err)
		s.db.MarkProactiveAttemptFailed(attemptID)
		return
	}

	// Audit estruturado.
	if s.agent != nil && s.agent.audit != nil {
		s.agent.audit.LogProactiveAttemptSent(user.ID, hoursIdle, attemptID, msg)
	}
	log.Printf("Scheduler[inactivity] %s: puxou conversa apos %dh idle (attempt %d)",
		user.Name, hoursIdle, attemptID)
}

// now retorna o relogio injetado, ou time.Now por default.
func (s *Scheduler) now() time.Time {
	if s.nowFunc == nil {
		return time.Now()
	}
	return s.nowFunc()
}
