package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

type Agent struct {
	client  *anthropic.Client
	cal     *CalendarClient
	db      *DB
	cfg     *Config
	perms   *PermissionManager
	audit   *AuditLog
	sendMsg func(phone, text string) error
}

type ToolHandler func(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error)

func NewAgent(apiKey string, cal *CalendarClient, db *DB, cfg *Config, sendMsg func(phone, text string) error) *Agent {
	return &Agent{
		client:  anthropic.NewClient(apiKey),
		cal:     cal,
		db:      db,
		cfg:     cfg,
		perms:   NewPermissionManager(db),
		audit:   NewAuditLog(db),
		sendMsg: sendMsg,
	}
}

// Run tries Haiku first, escalates to Sonnet if needed.
func (a *Agent) Run(ctx context.Context, user *User, message string) (string, error) {
	history, _ := a.db.GetConversationHistory(user.ID, 10)
	messages := buildMessages(history, message)

	// Try Haiku first
	response, escalated, err := a.runLoop(ctx, user, messages, anthropic.ModelClaudeHaiku4Dot5, buildHaikuSystemPrompt(user.Name))
	if err != nil {
		return "", fmt.Errorf("haiku: %w", err)
	}

	if escalated {
		log.Printf("[%s] Escalating to Sonnet", user.Name)
		// Re-build messages (don't reuse Haiku's modified slice)
		messages = buildMessages(history, message)
		response, _, err = a.runLoop(ctx, user, messages, anthropic.ModelClaudeSonnet4Dot6, buildSonnetSystemPrompt(user.Name))
		if err != nil {
			return "", fmt.Errorf("sonnet: %w", err)
		}
	}

	log.Printf("[%s] Agent final response (%d chars): %.100s", user.Name, len(response), response)

	// Save assistant response to history
	if response != "" {
		a.db.AddConversationMessage(user.ID, "assistant", response)
	}

	return response, nil
}

// runLoop is the core agent loop: send messages, handle tool_use, repeat.
func (a *Agent) runLoop(ctx context.Context, user *User, messages []anthropic.Message, model anthropic.Model, systemPrompt string) (string, bool, error) {
	tools := buildToolDefinitions()
	maxIterations := 8

	for i := 0; i < maxIterations; i++ {
		log.Printf("[%s] Agent loop iteration %d (model=%s, msgs=%d)", user.Name, i+1, model, len(messages))

		temp := float32(0.3)
		resp, err := a.client.CreateMessages(ctx, anthropic.MessagesRequest{
			Model:       model,
			MaxTokens:   4096,
			Temperature: &temp,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			return "", false, fmt.Errorf("claude API: %w", err)
		}

		log.Printf("[%s] Agent response: stop=%s content_blocks=%d", user.Name, resp.StopReason, len(resp.Content))

		// Check for escalation: if first content is text that looks like {"escalate": true, ...}
		if len(resp.Content) > 0 && resp.Content[0].Type == anthropic.MessagesContentTypeText {
			text := resp.Content[0].GetText()
			if isEscalation(text) {
				return "", true, nil
			}
		}

		if resp.StopReason == anthropic.MessagesStopReasonEndTurn || resp.StopReason == anthropic.MessagesStopReasonMaxTokens {
			// Extract text from response
			var textParts []string
			for _, c := range resp.Content {
				if c.Type == anthropic.MessagesContentTypeText {
					textParts = append(textParts, c.GetText())
				}
			}
			return strings.Join(textParts, "\n"), false, nil
		}

		if resp.StopReason == anthropic.MessagesStopReasonToolUse {
			// Append the assistant's response as-is (includes tool_use blocks)
			messages = append(messages, anthropic.Message{
				Role:    anthropic.RoleAssistant,
				Content: resp.Content,
			})

			// Execute each tool call and build results
			var toolResults []anthropic.MessageContent
			for _, c := range resp.Content {
				if c.Type == anthropic.MessagesContentTypeToolUse && c.MessageContentToolUse != nil {
					toolName := c.MessageContentToolUse.Name
					toolID := c.MessageContentToolUse.ID
					toolInput := c.MessageContentToolUse.Input

					log.Printf("[%s] Tool call: %s", user.Name, toolName)

					handler, ok := toolHandlers[toolName]
					if !ok {
						toolResults = append(toolResults, anthropic.NewToolResultMessageContent(toolID, fmt.Sprintf("Ferramenta desconhecida: %s", toolName), true))
						continue
					}

					result, err := handler(ctx, a, user, toolInput)
					if err != nil {
						log.Printf("[%s] Tool %s error: %v", user.Name, toolName, err)
						toolResults = append(toolResults, anthropic.NewToolResultMessageContent(toolID, fmt.Sprintf("Erro: %v", err), true))
					} else {
						toolResults = append(toolResults, anthropic.NewToolResultMessageContent(toolID, result, false))
					}
				}
			}

			// Send tool results back
			messages = append(messages, anthropic.Message{
				Role:    anthropic.RoleUser,
				Content: toolResults,
			})
			continue
		}

		// Unknown stop reason — return whatever text we have
		return resp.GetFirstContentText(), false, nil
	}

	return "Desculpe, nao consegui completar a operacao (muitas etapas).", false, nil
}

func buildMessages(history []ConversationMessage, userMsg string) []anthropic.Message {
	var msgs []anthropic.Message
	for _, h := range history {
		role := anthropic.RoleUser
		if h.Role == "assistant" {
			role = anthropic.RoleAssistant
		}
		msgs = append(msgs, anthropic.Message{
			Role:    role,
			Content: []anthropic.MessageContent{anthropic.NewTextMessageContent(h.Content)},
		})
	}
	msgs = append(msgs, anthropic.Message{
		Role:    anthropic.RoleUser,
		Content: []anthropic.MessageContent{anthropic.NewTextMessageContent(userMsg)},
	})
	return msgs
}

func isEscalation(text string) bool {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return false
	}
	var esc struct {
		Escalate bool   `json:"escalate"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text), &esc); err != nil {
		return false
	}
	return esc.Escalate
}

func buildHaikuSystemPrompt(userName string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e o assistente pessoal de %s via WhatsApp.

IMPORTANTE: Voce e o modelo rapido. Se a mensagem envolver QUALQUER dos cenarios abaixo,
NAO tente resolver — responda APENAS com o texto: {"escalate": true, "reason": "motivo"}

Cenarios para escalar:
- Criar mais de 1 evento por mensagem
- Referencia a conversas passadas que precisa buscar
- Pedidos ambiguos que exigem interpretacao criativa
- Editar/mover multiplos eventos
- Mensagens longas com multiplas instrucoes
- Agendar na agenda de outro usuario
- Qualquer coisa que voce nao tenha 90%% de certeza

Na duvida, ESCALE. Errar pra cima e melhor que errar pra baixo.

Se for simples e voce tiver certeza, use as ferramentas disponiveis.
O usuario pode te mandar audios e contatos — eles sao transcritos/convertidos em texto automaticamente antes de chegar a voce. Voce CONSEGUE entender audios.
SEMPRE use buscar_agenda quando o usuario perguntar sobre compromissos — NUNCA responda sobre agenda usando memoria da conversa.
Ao criar evento com informacoes claras, crie DIRETO e avise (nao peca confirmacao).
Responda em portugues, informal mas profissional. Seja MUITO conciso — maximo 2-3 frases. Sem emojis excessivos. Va direto ao ponto.

Formatacao WhatsApp: *negrito*, _italico_, ~tachado~, ` + "```codigo```" + `. NAO use markdown (**, ##, etc).

Data/hora atual: %s`, userName, now)
}

func buildSonnetSystemPrompt(userName string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e o assistente pessoal de %s via WhatsApp. Seja conciso e amigavel.

O usuario pode te mandar audios e contatos — eles sao transcritos/convertidos em texto automaticamente. Voce CONSEGUE entender audios.

Voce tem ferramentas para gerenciar a agenda. Use-as livremente:
- SEMPRE use buscar_agenda quando o usuario perguntar sobre compromissos — NUNCA responda sobre agenda usando memoria da conversa
- Ao criar evento com informacoes claras, crie DIRETO e avise (nao peca confirmacao)
- So peca confirmacao quando houver ambiguidade, conflito de horario, ou acao destrutiva (cancelar/editar)
- Para agendar na agenda de outro usuario, verifique permissao primeiro
- Se o usuario referir algo de conversas anteriores, use buscar_historico
- Responda em portugues, informal mas profissional. Seja MUITO conciso — maximo 2-3 frases. Sem emojis excessivos. Va direto ao ponto.

Formatacao WhatsApp: *negrito*, _italico_, ~tachado~, ` + "```codigo```" + `. NAO use markdown (**, ##, etc).

Data/hora atual: %s`, userName, now)
}

func buildToolDefinitions() []anthropic.ToolDefinition {
	return []anthropic.ToolDefinition{
		{
			Name:        "buscar_agenda",
			Description: "Busca eventos na agenda do usuario em um periodo.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"start_date": {"type": "string", "description": "Data de inicio (YYYY-MM-DD)"},
					"end_date": {"type": "string", "description": "Data de fim (YYYY-MM-DD)"}
				},
				"required": ["start_date", "end_date"]
			}`),
		},
		{
			Name:        "criar_evento",
			Description: "Cria um novo evento na agenda do usuario. Crie direto quando as informacoes forem claras.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Titulo do evento"},
					"date": {"type": "string", "description": "Data do evento (YYYY-MM-DD)"},
					"time": {"type": "string", "description": "Horario de inicio (HH:MM)"},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"},
					"com_meet": {"type": "boolean", "description": "Se true, gera link do Google Meet automaticamente"}
				},
				"required": ["title", "date", "time"]
			}`),
		},
		{
			Name:        "editar_evento",
			Description: "Edita um evento existente na agenda.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"search_query": {"type": "string", "description": "Texto para encontrar o evento"},
					"new_title": {"type": "string", "description": "Novo titulo (opcional)"},
					"new_date": {"type": "string", "description": "Nova data YYYY-MM-DD (opcional)"},
					"new_time": {"type": "string", "description": "Novo horario HH:MM (opcional)"},
					"new_duration_minutes": {"type": "integer", "description": "Nova duracao em minutos (opcional)"},
					"new_location": {"type": "string", "description": "Novo local (opcional)"}
				},
				"required": ["search_query"]
			}`),
		},
		{
			Name:        "cancelar_evento",
			Description: "Cancela (deleta) um evento da agenda.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"search_query": {"type": "string", "description": "Texto para encontrar o evento a cancelar"}
				},
				"required": ["search_query"]
			}`),
		},
		{
			Name:        "buscar_historico",
			Description: "Busca mensagens anteriores na conversa com o usuario.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Texto para buscar no historico"},
					"limit": {"type": "integer", "description": "Numero maximo de resultados (default: 10)"}
				},
				"required": ["query"]
			}`),
		},
		{
			Name:        "criar_evento_outro_usuario",
			Description: "Cria um evento na agenda de outro usuario (requer permissao).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"target_user": {"type": "string", "description": "Nome do usuario alvo"},
					"title": {"type": "string", "description": "Titulo do evento"},
					"date": {"type": "string", "description": "Data do evento (YYYY-MM-DD)"},
					"time": {"type": "string", "description": "Horario de inicio (HH:MM)"},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"}
				},
				"required": ["target_user", "title", "date", "time"]
			}`),
		},
		{
			Name:        "convidar_externo",
			Description: "Envia convite via WhatsApp para uma pessoa externa (nao cadastrada). Usa quando o usuario quer convidar alguem por numero de telefone.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"phone": {"type": "string", "description": "Numero de telefone do convidado (com DDD)"},
					"name": {"type": "string", "description": "Nome do convidado"},
					"event_title": {"type": "string", "description": "Titulo do evento"},
					"event_date": {"type": "string", "description": "Data do evento (DD/MM/YYYY ou descritivo)"},
					"event_time": {"type": "string", "description": "Horario do evento (HH:MM)"},
					"meet_link": {"type": "string", "description": "Link do Google Meet (opcional, se existir)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"}
				},
				"required": ["phone", "name", "event_title", "event_date", "event_time"]
			}`),
		},
		{
			Name:        "gerar_link_meet",
			Description: "Gera um link do Google Meet para um evento existente.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"search_query": {"type": "string", "description": "Texto para encontrar o evento"}
				},
				"required": ["search_query"]
			}`),
		},
	}
}
