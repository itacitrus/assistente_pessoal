package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/liushuangls/go-anthropic/v2"
)

// runCompanion conduz um turno do idoso pelo companion provider (DeepSeek em
// prod) via a abstracao llm.ChatProvider, reaproveitando os mesmos toolHandlers
// do caminho Anthropic. Diferencas em relacao ao runLoop Anthropic:
//   - imagens sao pre-descritas pelo VisionProvider (Haiku) e injetadas como
//     texto, pois o DeepSeek-chat nao tem vision;
//   - sem prompt cache (DeepSeek concatena tudo) — a flag Cacheable e ignorada.
//
// systemParts ja vem montado por Run (core + dinamico + politica + regras
// farmacologicas condicionais), entao aqui so traduzimos pro formato llm.
func (a *Agent) runCompanion(ctx context.Context, user *User, message string, images []ImageAttachment, systemParts []anthropic.MessageSystemPart) (string, error) {
	augMsg := message
	var descs []string
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		if d := a.describeImageForCompanion(ctx, user, img); d != "" {
			descs = append(descs, d)
		}
	}
	if len(descs) > 0 {
		var prefix strings.Builder
		for _, d := range descs {
			prefix.WriteString("[Imagem que ele te mandou: ")
			prefix.WriteString(d)
			prefix.WriteString("]\n")
		}
		augMsg = strings.TrimSpace(prefix.String() + augMsg)
	}

	history, _ := a.db.GetConversationHistory(user.ID, 30)
	messages := buildMessagesLLM(history, augMsg)
	system := systemPartsToLLM(systemParts)
	tools := toolDefsToLLM(buildToolDefinitions())

	resp, err := a.runLoopLLM(ctx, user, a.companionChat, system, messages, tools)
	if err != nil {
		return "", fmt.Errorf("agent companion: %w", err)
	}
	log.Printf("[%s] Companion final response (%d chars): %.100s", user.Name, len(resp), resp)
	return resp, nil
}

// describeImageForCompanion gera uma descricao curta da imagem via
// VisionProvider (Haiku) pra injetar como texto no turno do companheiro.
// Retorna "" se nao houver vision provider ou em caso de erro (o bot ainda
// responde, so sem detalhe da imagem). Nao expoe ao idoso que houve uma
// "descricao" — o prompt orienta a comentar como se tivesse visto a foto.
func (a *Agent) describeImageForCompanion(ctx context.Context, user *User, img ImageAttachment) string {
	if a.vision == nil {
		return ""
	}
	media := img.Mime
	if media == "" {
		media = "image/jpeg"
	}
	resp, err := a.vision.DescribeImage(ctx, llm.VisionRequest{
		Prompt: "Descreva esta imagem em 1-2 frases curtas, em portugues do Brasil, " +
			"de forma simples e calorosa: o que aparece, pessoas, lugar e o clima " +
			"emocional. Sem listar, sem jargao, sem inventar o que nao da pra ver.",
		ImageMedia: media,
		ImageData:  base64.StdEncoding.EncodeToString(img.Data),
		MaxTokens:  200,
	})
	if err != nil {
		log.Printf("[%s] companion vision describe error: %v", user.Name, err)
		return ""
	}
	return strings.TrimSpace(resp.Text)
}

// runLoopLLM e o loop de tool-use sobre llm.ChatProvider — espelha runLoop
// (Anthropic) mas em tipos canonicos. Reaproveita o registry toolHandlers (a
// assinatura do handler ja e agnostica de provider: ctx, agent, user, json).
func (a *Agent) runLoopLLM(ctx context.Context, user *User, provider llm.ChatProvider, system []llm.SystemPart, messages []llm.Message, tools []llm.ToolDef) (string, error) {
	const maxIterations = 8
	for i := 0; i < maxIterations; i++ {
		log.Printf("[%s] Companion loop iteration %d (provider=%s, msgs=%d)", user.Name, i+1, provider.Name(), len(messages))

		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System:      system,
			Messages:    messages,
			Tools:       tools,
			MaxTokens:   4096,
			Temperature: 0.4,
		})
		if err != nil {
			return "", fmt.Errorf("companion chat: %w", err)
		}
		log.Printf("[%s] Companion response: stop=%s blocks=%d tokens=in:%d/out:%d model=%s",
			user.Name, resp.StopReason, len(resp.Content), resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.ModelUsed)

		if resp.StopReason != llm.StopToolUse {
			return collectLLMText(resp.Content), nil
		}

		// Anexa o turno do assistant (texto + tool_use) como veio.
		messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})

		var results []llm.ContentBlock
		for _, c := range resp.Content {
			if c.Type != "tool_use" {
				continue
			}
			handler, ok := toolHandlers[c.ToolName]
			if !ok {
				results = append(results, errToolResultLLM(c.ToolUseID, fmt.Sprintf("Ferramenta desconhecida: %s", c.ToolName)))
				continue
			}
			log.Printf("[%s] Tool call: %s input=%s", user.Name, c.ToolName, string(c.ToolInput))
			result, err := handler(ctx, a, user, c.ToolInput)
			if err != nil {
				log.Printf("[%s] Tool %s error: %v", user.Name, c.ToolName, err)
				results = append(results, errToolResultLLM(c.ToolUseID, fmt.Sprintf("Erro: %v", err)))
				continue
			}
			preview := result
			if len(preview) > 500 {
				preview = preview[:500] + "...[truncated]"
			}
			log.Printf("[%s] Tool %s result: %s", user.Name, c.ToolName, preview)
			results = append(results, llm.ContentBlock{Type: "tool_result", ToolUseID: c.ToolUseID, ToolResult: result})
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: results})
	}
	return "Desculpe, nao consegui completar a operacao (muitas etapas).", nil
}

// errToolResultLLM monta um tool_result de erro no formato canonico.
func errToolResultLLM(toolUseID, msg string) llm.ContentBlock {
	return llm.ContentBlock{Type: "tool_result", ToolUseID: toolUseID, ToolResult: msg, IsError: true}
}

// collectLLMText junta os blocks de texto de uma resposta.
func collectLLMText(content []llm.ContentBlock) string {
	var parts []string
	for _, c := range content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// buildMessagesLLM espelha buildMessages (Anthropic) em tipos canonicos. O
// historico e armazenado como texto puro (tool_use/tool_result nao sao
// persistidos), entao todo turno do historico vira um unico block de texto.
func buildMessagesLLM(history []ConversationMessage, userMsg string) []llm.Message {
	var msgs []llm.Message
	for _, h := range history {
		if h.Content == "" {
			continue
		}
		role := llm.RoleUser
		if h.Role == "assistant" {
			role = llm.RoleAssistant
		}
		msgs = append(msgs, llm.Message{Role: role, Content: []llm.ContentBlock{{Type: "text", Text: h.Content}}})
	}
	if userMsg == "" {
		userMsg = "[imagem enviada]"
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: userMsg}}})
	return msgs
}

// systemPartsToLLM converte as system parts do formato Anthropic pro canonico.
// CacheControl != nil vira Cacheable=true (DeepSeek ignora; Anthropic honra).
func systemPartsToLLM(parts []anthropic.MessageSystemPart) []llm.SystemPart {
	out := make([]llm.SystemPart, 0, len(parts))
	for _, p := range parts {
		out = append(out, llm.SystemPart{Text: p.Text, Cacheable: p.CacheControl != nil})
	}
	return out
}

// upcomingReminder eh um lembrete de remedio que ainda vai disparar hoje.
type upcomingReminder struct {
	at    time.Time // no fuso local do idoso
	names []string
}

// upcomingMedRemindersToday lista os lembretes de remedio que ainda vao
// disparar de `now` ate o fim do dia (fuso local do idoso). Usado pra o
// companheiro saber se a conversa atual eh o ULTIMO contato programado do dia
// — sem isso ele desejava "boa noite/descanse bem" cedo demais, antes de um
// lembrete posterior (caso Fabio: "descanse bem" 18h02, lembrete 19h00).
func (a *Agent) upcomingMedRemindersToday(user *User, now time.Time) []upcomingReminder {
	loc := a.db.GetEventTimezone(user.ID, now)
	if loc == nil {
		loc = BRT()
	}
	nowLocal := now.In(loc)
	endOfDay := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 23, 59, 59, 0, loc)
	if !now.Before(endOfDay) {
		return nil
	}
	meds, err := a.db.ListActiveMedications(user.ID)
	if err != nil || len(meds) == 0 {
		return nil
	}
	byInstant := map[int64][]string{}
	var order []int64
	for i := range meds {
		m := &meds[i]
		scheds, err := a.db.ListSchedulesForMedication(m.ID)
		if err != nil {
			continue
		}
		for j := range scheds {
			occs, err := ExpandOccurrences(&scheds[j], now, endOfDay, loc)
			if err != nil {
				continue
			}
			for _, occ := range occs {
				key := occ.UnixNano()
				if _, ok := byInstant[key]; !ok {
					order = append(order, key)
				}
				present := false
				for _, n := range byInstant[key] {
					if n == m.Name {
						present = true
						break
					}
				}
				if !present {
					byInstant[key] = append(byInstant[key], m.Name)
				}
			}
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]upcomingReminder, 0, len(order))
	for _, key := range order {
		out = append(out, upcomingReminder{at: time.Unix(0, key).In(loc), names: byInstant[key]})
	}
	return out
}

// appendCompanionDayContextPart injeta o [CONTEXTO DO DIA] no prompt do idoso:
// quais lembretes de remedio ainda faltam hoje. Permite ao companheiro decidir
// se eh o ultimo contato do dia antes de se despedir com "boa noite". No-op
// para nao-idosos. So injeta de tarde/noite (>=14h local) — de manha "ultimo
// contato do dia" nao faz sentido e so polui o prompt.
func (a *Agent) appendCompanionDayContextPart(parts []anthropic.MessageSystemPart, user *User, now time.Time) []anthropic.MessageSystemPart {
	if user == nil || user.Type != UserTypeIdoso {
		return parts
	}
	loc := a.db.GetEventTimezone(user.ID, now)
	if loc == nil {
		loc = BRT()
	}
	if now.In(loc).Hour() < 14 {
		return parts
	}
	rem := a.upcomingMedRemindersToday(user, now)
	var text string
	if len(rem) == 0 {
		text = "[CONTEXTO DO DIA] Não há mais lembretes de remédio programados para hoje. " +
			"Se for noite e fizer sentido, pode se despedir desejando boa noite/bom descanso."
	} else {
		var b strings.Builder
		b.WriteString("[CONTEXTO DO DIA] Ainda há lembrete(s) de remédio HOJE: ")
		for i, r := range rem {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(r.at.Format("15:04"))
			b.WriteString(" (")
			b.WriteString(strings.Join(r.names, ", "))
			b.WriteString(")")
		}
		b.WriteString(". Portanto ESTE não é o último contato do dia — NÃO se despeça com " +
			"\"boa noite\"/\"descanse bem\"/\"até amanhã\". Encerre de forma aberta (\"estou por aqui\").")
		text = b.String()
	}
	return append(parts, anthropic.MessageSystemPart{Type: "text", Text: text})
}

// toolDefsToLLM converte as ToolDefinition do Anthropic pro ToolDef canonico.
// anthropic.ToolDefinition.InputSchema e `any` (guarda um json.RawMessage em
// buildToolDefinitions); json.Marshal recupera os bytes do schema.
func toolDefsToLLM(defs []anthropic.ToolDefinition) []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(defs))
	for _, d := range defs {
		var schema json.RawMessage
		if d.InputSchema != nil {
			if b, err := json.Marshal(d.InputSchema); err == nil {
				schema = b
			}
		}
		out = append(out, llm.ToolDef{Name: d.Name, Description: d.Description, InputSchema: schema})
	}
	return out
}
