package main

import (
	"context"
	"encoding/base64"
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

// RunForUnknown handles messages from non-registered users.
// No tools, no history — just a polite, brief response like a human messenger would give.
func (a *Agent) RunForUnknown(ctx context.Context, senderPhone, message string) (string, error) {
	prompt := `Voce e Charles Lurch, assistente pessoal. Alguem te mandou uma mensagem.

Voce age como um mensageiro educado:
- Se a pessoa agradeceu ou confirmou presenca: responda brevemente ("Obrigado! Qualquer duvida, fale com quem te convidou.")
- Se a pessoa tem duvida sobre uma reuniao/convite que voce entregou: responda com base no que sabe.
- Se a pessoa pedir algo (marcar reuniao, consultar agenda, etc): diga educadamente que so usuarios cadastrados podem solicitar isso.
- Se ja se apresentou antes na conversa, NAO se apresente de novo.
- NUNCA inicie conversas longas. Seja breve e educado — 1 frase no maximo.
- Portugues informal.`

	userMsg := message
	messages := []anthropic.Message{
		{Role: anthropic.RoleUser, Content: []anthropic.MessageContent{{Type: "text", Text: &userMsg}}},
	}

	temp := float32(0.3)
	resp, err := a.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:       anthropic.ModelClaudeHaiku4Dot5,
		MaxTokens:   256,
		Temperature: &temp,
		System:      prompt,
		Messages:    messages,
	})
	if err != nil {
		return "", fmt.Errorf("claude API: %w", err)
	}

	if len(resp.Content) == 0 {
		return "", nil
	}

	return resp.Content[0].GetText(), nil
}

// Run processes a user message using Sonnet with tool use.
func (a *Agent) Run(ctx context.Context, user *User, message string, imageData []byte, imageMime string) (string, error) {
	history, _ := a.db.GetConversationHistory(user.ID, 30)
	messages := buildMessages(history, message)

	// If image is attached, add it to the last (current) user message
	if len(imageData) > 0 && imageMime != "" {
		lastIdx := len(messages) - 1
		imgContent := anthropic.NewImageMessageContent(anthropic.MessageContentSource{
			Type:      anthropic.MessagesContentSourceTypeBase64,
			MediaType: imageMime,
			Data:      base64.StdEncoding.EncodeToString(imageData),
		})
		messages[lastIdx].Content = append(messages[lastIdx].Content, imgContent)
		if message == "" {
			// If no text, add a prompt for the image
			hint := "[Imagem enviada pelo usuario. Analise e identifique compromissos, eventos ou informacoes relevantes.]"
			messages[lastIdx].Content = append([]anthropic.MessageContent{anthropic.NewTextMessageContent(hint)}, messages[lastIdx].Content...)
		}
	}

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
		if h.Content == "" {
			continue
		}
		role := anthropic.RoleUser
		if h.Role == "assistant" {
			role = anthropic.RoleAssistant
		}
		msgs = append(msgs, anthropic.Message{
			Role:    role,
			Content: []anthropic.MessageContent{anthropic.NewTextMessageContent(h.Content)},
		})
	}
	// Add current user message (may be empty if image-only — agent.Run adds image content after)
	if userMsg == "" {
		userMsg = "[imagem enviada]"
	}
	msgs = append(msgs, anthropic.Message{
		Role:    anthropic.RoleUser,
		Content: []anthropic.MessageContent{anthropic.NewTextMessageContent(userMsg)},
	})
	return msgs
}

func buildSystemPrompt(userName string) string {
	now := time.Now().In(BRT()).Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e Charles Lurch, assistente pessoal do Waldyr, via WhatsApp. Voce esta conversando com %s. Data/hora atual: %s (fuso: America/Sao_Paulo)

REGRA DE OURO: NUNCA pergunte algo que voce pode descobrir sozinho. Sempre tente resolver ANTES de perguntar.

Quando o usuario pedir algo:
1. Leia o HISTORICO DA CONVERSA — a resposta quase sempre esta la (nomes, emails, eventos mencionados).
2. Se nao encontrar no historico, use buscar_memoria para informacoes salvas.
3. Se nao encontrar na memoria, use buscar_agenda ou buscar_historico.
4. SOMENTE pergunte ao usuario se REALMENTE nao conseguiu descobrir de nenhuma forma.

Exemplos de raciocinio correto:
- "convida o ti pra essa tb" → ti@ ja foi mencionado nesta conversa, "essa" = ultimo evento discutido → buscar_agenda pra achar → convidar.
- "meu pai" → buscar_memoria primeiro, so pedir info se nao encontrar.
- "coloca o dia inteiro" sobre evento existente → editar_evento com new_time="00:00" e new_duration_minutes=1440.

TIMEZONE:
- O fuso base do usuario e America/Sao_Paulo (Brasil).
- O fuso e DINAMICO baseado em onde o usuario esta em cada data:
  - "Vou pra Europa de 13 a 20/05" → salve na memoria (categoria "viagem") com datas. Eventos NESSE PERIODO usam fuso europeu. Apos 20/05, volta automaticamente pro Brasil.
  - "Estou em Londres" (sem data de volta) → PERGUNTE quando ele volta para ajustar o fuso dos compromissos nesse periodo.
  - "Reuniao em Roma dia 15 as 14h" (evento pontual no exterior, sem viagem declarada) → 14h e horario de Roma (Europe/Rome), so nesse evento.
  - Eventos sem contexto de local estrangeiro → America/Sao_Paulo.
- Sempre que inferir fuso, use buscar_memoria para checar viagens salvas e determinar o fuso correto para a data do evento.
- 14h em Paris = 14h de Paris. NUNCA converta — use o fuso do local.

RECORRENCIA:
- Aniversarios → RRULE:FREQ=YEARLY (sempre recorrente, sem perguntar)
- "toda segunda" → RRULE:FREQ=WEEKLY;BYDAY=MO
- "todo dia" → RRULE:FREQ=DAILY
- "todo mes" → RRULE:FREQ=MONTHLY

REGRAS CRITICAS PARA CRIAR EVENTOS:
- Se faltar o horario, use seu julgamento: eventos como feiras, viagens, feriados → crie como dia inteiro (00:00, 1440min). Reunioes e compromissos com hora implicita → consulte a agenda, sugira o primeiro horario livre e so confirme (ex: "Marquei pra 10h, tudo bem?").
- "dia inteiro" = evento de 00:00 com duracao 1440 minutos.
- Quando o usuario pedir multiplos eventos, crie TODOS de uma vez (chame criar_evento varias vezes na mesma resposta).

REGRAS CRITICAS PARA EDITAR EVENTOS:
- ANTES de editar ou cancelar, SEMPRE use buscar_agenda para encontrar o evento exato. Nunca tente editar sem consultar a agenda primeiro.
- Use editar_evento para modificar. NUNCA sugira cancelar e recriar.
- Se o usuario quer mudar horario/duracao, use editar_evento com os campos new_time e/ou new_duration_minutes.
- "dia inteiro" = new_time="00:00" e new_duration_minutes=1440.
- Se o usuario quer mudar SOMENTE um dos eventos repetidos, edite SÓ aquele.
- NUNCA peca ao usuario para fazer algo manualmente que voce pode fazer com suas ferramentas.
- NUNCA diga que nao encontrou um evento se o usuario acabou de mencionar. Use buscar_agenda com o periodo certo.

Ferramentas disponiveis:
- buscar_agenda: consultar eventos. SEMPRE use antes de responder sobre compromissos.
- criar_evento: criar evento. Inclua meet/attendees quando relevante. Prefira uma chamada com tudo.
- editar_evento: modificar evento existente (titulo, data, hora, duracao, local).
- cancelar_evento: remover evento. Peca confirmacao antes.
- buscar_memoria, salvar_memoria: memoria persistente. Salve proativamente contatos, relacoes, preferencias.
- buscar_historico: buscar mensagens antigas.
- convidar_participante: adicionar email como participante.
- convidar_externo: mandar convite via WhatsApp para nao-usuarios. Quando convidar para MULTIPLOS eventos (ex: 3 dias de feira), envie UM convite para CADA dia — chame a ferramenta varias vezes.
- gerar_link_meet: gerar link do Google Meet.

Regras gerais:
- NUNCA finja ter executado uma acao sem chamar a ferramenta.
- NUNCA responda sobre agenda usando memoria da conversa — sempre consulte.
- Antes de criar evento, confira se ja foi criado. Nao duplique.
- Entenda audios e contatos compartilhados (transcritos automaticamente).

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
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Emails de participantes (opcional, NAO peca proativamente)"},
					"force_conflict": {"type": "boolean", "description": "Se true, cria mesmo com conflito de horario (so usar apos usuario confirmar)"},
					"timezone": {"type": "string", "description": "Fuso horario IANA (ex: Europe/London). Default: America/Sao_Paulo."},
					"recurrence": {"type": "string", "description": "Regra de recorrencia iCal. Ex: RRULE:FREQ=YEARLY para aniversarios, RRULE:FREQ=WEEKLY;BYDAY=MO para toda segunda"}
				},
				"required": ["title", "date", "time"]
			}`),
		},
		{
			Name:        "editar_evento",
			Description: "Edita um evento existente na agenda. SEMPRE use buscar_agenda antes para obter o event_id.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"event_id": {"type": "string", "description": "ID do evento (obtido via buscar_agenda). Preferivel ao search_query."},
					"search_query": {"type": "string", "description": "Texto para encontrar o evento (fallback se nao tiver event_id)"},
					"new_title": {"type": "string", "description": "Novo titulo (opcional)"},
					"new_date": {"type": "string", "description": "Nova data YYYY-MM-DD (opcional)"},
					"new_time": {"type": "string", "description": "Novo horario HH:MM (opcional)"},
					"new_duration_minutes": {"type": "integer", "description": "Nova duracao em minutos (opcional)"},
					"new_location": {"type": "string", "description": "Novo local (opcional)"}
				}
			}`),
		},
		{
			Name:        "cancelar_evento",
			Description: "Cancela (deleta) um evento da agenda. SEMPRE use buscar_agenda antes para obter o event_id.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"event_id": {"type": "string", "description": "ID do evento (obtido via buscar_agenda). Preferivel."},
					"search_query": {"type": "string", "description": "Texto para encontrar o evento (fallback)"}
				}
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
