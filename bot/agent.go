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

// Run processes a user message using Sonnet with tool use.
func (a *Agent) Run(ctx context.Context, user *User, message string) (string, error) {
	history, _ := a.db.GetConversationHistory(user.ID, 30)
	messages := buildMessages(history, message)

	response, _, err := a.runLoop(ctx, user, messages, anthropic.ModelClaudeSonnet4Dot6, buildSystemPrompt(user.Name))
	if err != nil {
		return "", fmt.Errorf("agent: %w", err)
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

func buildSystemPrompt(userName string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e o assistente pessoal de %s via WhatsApp. Data/hora atual: %s

REGRA DE OURO: NUNCA pergunte algo que voce pode descobrir sozinho. Sempre tente resolver ANTES de perguntar.

Quando o usuario pedir algo:
1. Leia o HISTORICO DA CONVERSA — a resposta quase sempre esta la (nomes, emails, eventos mencionados).
2. Se nao encontrar no historico, use buscar_memoria para informacoes salvas.
3. Se nao encontrar na memoria, use buscar_agenda ou buscar_historico.
4. SOMENTE pergunte ao usuario se realmente nao conseguiu descobrir de nenhuma forma.

Exemplos de raciocinio correto:
- "convida o ti pra essa tb" → ti@ ja foi mencionado nesta conversa, "essa" = ultimo evento discutido → buscar_agenda pra achar o evento → convidar. NUNCA perguntar "qual evento?" se so tem um contexto possivel.
- "marca reuniao amanha" → criar direto, nao perguntar horario se ja esta implicito no contexto.
- "meu pai" → buscar_memoria primeiro, so pedir numero/email se nao encontrar.

Ferramentas disponiveis:
- buscar_agenda: consultar eventos. SEMPRE use antes de responder sobre compromissos.
- criar_evento: criar evento. Inclua meet/attendees quando relevante. Prefira uma chamada com tudo.
- editar_evento, cancelar_evento: modificar/remover eventos.
- buscar_memoria, salvar_memoria: memoria persistente do usuario. Salve proativamente contatos, relacoes, preferencias.
- buscar_historico: buscar mensagens antigas.
- convidar_participante: adicionar email como participante de evento existente.
- convidar_externo: mandar convite via WhatsApp para nao-usuarios.
- gerar_link_meet: gerar link do Google Meet para evento existente.

Regras:
- NUNCA finja ter executado uma acao sem chamar a ferramenta.
- NUNCA responda sobre agenda usando memoria da conversa — sempre consulte.
- Antes de criar evento, confira no historico se ja foi criado. Nao duplique.
- So peca confirmacao quando houver ambiguidade REAL ou acao destrutiva.
- Entenda audios e contatos compartilhados (sao transcritos automaticamente).

Estilo:
- Portugues, informal, profissional. MUITO conciso — 1-2 frases. Direto ao ponto.
- Formatacao WhatsApp: *negrito*, _italico_. NAO use markdown (**, ##).
- Sem emojis excessivos.`, userName, now)
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
			Description: "Cria um novo evento na agenda do usuario. Crie direto quando as informacoes forem claras. PREFERIVEL usar esta tool com todos os parametros (meet, attendees) de uma vez em vez de chamar criar_evento + gerar_link_meet + convidar_participante separadamente.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Titulo do evento"},
					"date": {"type": "string", "description": "Data do evento (YYYY-MM-DD)"},
					"time": {"type": "string", "description": "Horario de inicio (HH:MM)"},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"},
					"com_meet": {"type": "boolean", "description": "Se true, gera link do Google Meet automaticamente"},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Emails de participantes (opcional, NAO peca proativamente)"}
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
			Name:        "convidar_participante",
			Description: "Adiciona participantes a um evento existente pelo email. O Google Calendar envia convite oficial. NAO peca email proativamente — use apenas quando o usuario fornecer o email ou quando fizer sentido no contexto (ex: usuario pediu confirmacao de presenca).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"search_query": {"type": "string", "description": "Texto para encontrar o evento"},
					"emails": {"type": "array", "items": {"type": "string"}, "description": "Lista de emails dos participantes"}
				},
				"required": ["search_query", "emails"]
			}`),
		},
		{
			Name:        "salvar_memoria",
			Description: "Salva uma informacao sobre o usuario para lembrar no futuro. Use para contatos, preferencias, enderecos, relacoes pessoais, etc. Salve PROATIVAMENTE quando o usuario mencionar informacoes pessoais relevantes.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Categoria: contato, endereco, preferencia, relacao, trabalho, outro"},
					"key": {"type": "string", "description": "Identificador curto (ex: pai, escritorio, preferencia_horario)"},
					"value": {"type": "string", "description": "Informacao completa (ex: Fabio de Freitas - 61982279928)"}
				},
				"required": ["category", "key", "value"]
			}`),
		},
		{
			Name:        "buscar_memoria",
			Description: "Busca informacoes salvas sobre o usuario (contatos, preferencias, enderecos, etc). Use ANTES de pedir informacoes que o usuario ja pode ter fornecido antes.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Termo de busca (ex: pai, escritorio, endereco)"},
					"category": {"type": "string", "description": "Filtrar por categoria (opcional): contato, endereco, preferencia, relacao, trabalho"}
				}
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
