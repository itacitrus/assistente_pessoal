package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

// RunProactive gera uma mensagem proativa para um idoso inativo.
//
// O scheduler chama isso periodicamente (cron 1-min, gating de 15min) quando
// o idoso fica calado por mais que user.InactivityThresholdHours horas. A
// mensagem-sintetica "[SISTEMA] %s nao fala ha N horas — puxe conversa..."
// e injetada como role=user no array de mensagens, MAS NAO eh persistida
// em conversation_history (o user nao mandou nada de fato).
//
// Justificativa: nao queremos que mensagens "[SISTEMA] ..." poluam
// historico futuro. O agente precisa do prompt synthetic so naquele turno.
// A resposta gerada eh persistida no transporte (Handler.persistOutbound)
// quando o scheduler a envia ao usuario.
//
// Caminho:
//  1. Carrega historico (30 mensagens).
//  2. Append synthetic prompt como role=user no fim.
//  3. Roda runLoop com persona companion (rotada por user.Type=idoso).
//
// Retorna a string da mensagem proativa, ou "" se o agente decidir nao
// puxar (resposta vazia respeitada — caller nao envia).
func (a *Agent) RunProactive(ctx context.Context, user *User, hoursIdle int) (string, error) {
	if user == nil {
		return "", fmt.Errorf("RunProactive: nil user")
	}
	if user.Type != UserTypeIdoso {
		return "", fmt.Errorf("RunProactive: user %s is not idoso (type=%s)", user.Name, user.Type)
	}

	history, _ := a.db.GetConversationHistory(user.ID, 30)

	syntheticPrompt := fmt.Sprintf(
		"[SISTEMA] %s não fala há cerca de %d horas. Puxe conversa naturalmente, "+
			"referenciando algo que você já sabe sobre ele/ela (busque em social_context "+
			"se precisar). Mensagem única, curta, sem soar robótico, sem perguntar de "+
			"saúde diretamente, sem listas. Se ele pediu trégua recente, NÃO mande nada — "+
			"responda com a string vazia.",
		user.Name, hoursIdle,
	)
	syntheticPrompt += proactiveAvoidRepeatHint(a.db, user.ID)
	messages := buildMessages(history, syntheticPrompt)

	// Persona companion via roteador. user.Type==idoso garante.
	pendingReq, _ := a.db.GetPendingPermissionRequest(user.ID)
	systemParts := []anthropic.MessageSystemPart{
		{
			Type: "text",
			Text: buildSystemPromptStable(user),
			CacheControl: &anthropic.MessageCacheControl{
				Type: anthropic.CacheControlTypeEphemeral,
			},
		},
		{
			Type: "text",
			Text: buildSystemPromptDynamic(pendingReq),
		},
	}
	systemParts = a.appendMedicationPolicyPart(systemParts, user)

	response, _, err := a.runLoop(ctx, user, messages, anthropic.ModelClaudeSonnet4Dot6, systemParts)
	if err != nil {
		return "", fmt.Errorf("agent proactive: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" {
		log.Printf("[%s] RunProactive: agente decidiu nao puxar conversa", user.Name)
		return "", nil
	}

	// Nao persiste aqui: a mensagem proativa entra em conversation_history no
	// transporte (Handler.persistOutbound) quando o scheduler a envia. O
	// synthetic prompt [SISTEMA] nunca eh enviado, entao nunca eh persistido.
	return response, nil
}

// proactiveAvoidRepeatHint monta uma instrucao listando as ultimas puxadas
// proativas (24h) para o modelo NAO repetir o mesmo gancho/assunto. Sem o
// hint, com memoria social escassa, o modelo cai sempre no gancho universal
// (o tempo) — foi o caso "friozinho gostoso" repetido 3x num dia. Retorna ""
// quando nao ha puxadas recentes.
func proactiveAvoidRepeatHint(db *DB, userID int64) string {
	attempts, err := db.GetRecentProactiveAttempts(userID, 24*time.Hour, 4)
	if err != nil || len(attempts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nVocê JÁ puxou conversa nas últimas horas com as mensagens abaixo. " +
		"NÃO repita o mesmo gancho, tema ou abertura (ex: se já falou do tempo/frio, " +
		"NÃO fale do tempo de novo). Traga algo genuinamente novo — outra memória, outra " +
		"pessoa, outro interesse dele. Se não tiver nada novo e relevante pra dizer, " +
		"responda com a string vazia em vez de insistir:")
	for _, at := range attempts {
		msg := strings.TrimSpace(at.MessageSent)
		if msg == "" {
			continue
		}
		if len(msg) > 160 {
			msg = msg[:160] + "…"
		}
		b.WriteString("\n- \"")
		b.WriteString(msg)
		b.WriteString("\"")
	}
	return b.String()
}

// proactiveWindowAllowed retorna true se now (em loc) esta entre 8h e 21h
// — janela em que faz sentido puxar conversa com idoso. Madrugada e
// final de noite respeitam o sono. Exposto pra tests injetarem hora.
func proactiveWindowAllowed(now time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = BRT()
	}
	h := now.In(loc).Hour()
	return h >= 8 && h < 21
}
