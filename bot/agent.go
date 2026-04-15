package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
func (a *Agent) Run(ctx context.Context, user *User, message string, images []ImageAttachment) (string, error) {
	history, _ := a.db.GetConversationHistory(user.ID, 30)
	messages := buildMessages(history, message)

	// Attach all images to the last (current) user message
	if len(images) > 0 {
		lastIdx := len(messages) - 1
		for _, img := range images {
			if len(img.Data) == 0 {
				continue
			}
			mime := img.Mime
			if mime == "" {
				mime = "image/jpeg"
			}
			imgContent := anthropic.NewImageMessageContent(anthropic.MessageContentSource{
				Type:      anthropic.MessagesContentSourceTypeBase64,
				MediaType: mime,
				Data:      base64.StdEncoding.EncodeToString(img.Data),
			})
			messages[lastIdx].Content = append(messages[lastIdx].Content, imgContent)
		}
		if message == "" {
			hint := "[Imagem(ns) enviada(s) pelo usuario. Analise e identifique compromissos, eventos ou informacoes relevantes.]"
			messages[lastIdx].Content = append([]anthropic.MessageContent{anthropic.NewTextMessageContent(hint)}, messages[lastIdx].Content...)
		}
	}

	pendingReq, _ := a.db.GetPendingPermissionRequest(user.ID)
	systemParts := []anthropic.MessageSystemPart{
		{
			Type: "text",
			Text: buildSystemPromptStable(user.Name),
			CacheControl: &anthropic.MessageCacheControl{
				Type: anthropic.CacheControlTypeEphemeral,
			},
		},
		{
			Type: "text",
			Text: buildSystemPromptDynamic(pendingReq),
		},
	}

	response, _, err := a.runLoop(ctx, user, messages, anthropic.ModelClaudeSonnet4Dot6, systemParts)
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
func (a *Agent) runLoop(ctx context.Context, user *User, messages []anthropic.Message, model anthropic.Model, systemParts []anthropic.MessageSystemPart) (string, bool, error) {
	tools := buildToolDefinitions()
	maxIterations := 8

	for i := 0; i < maxIterations; i++ {
		log.Printf("[%s] Agent loop iteration %d (model=%s, msgs=%d)", user.Name, i+1, model, len(messages))

		// Mark the last content block of the final message with cache_control
		// so Anthropic caches the conversation prefix up to here. Subsequent
		// iterations extend the prefix and keep hitting the cache.
		markLastMessageForCache(messages)

		temp := float32(0.3)
		resp, err := a.createMessagesWithRetry(ctx, user, anthropic.MessagesRequest{
			Model:       model,
			MaxTokens:   4096,
			Temperature: &temp,
			MultiSystem: systemParts,
			Messages:    messages,
			Tools:       tools,
		})
		if err != nil {
			return "", false, fmt.Errorf("claude API: %w", err)
		}

		u := resp.Usage
		log.Printf("[%s] Agent response: stop=%s content_blocks=%d tokens=in:%d/out:%d cache=write:%d/read:%d",
			user.Name, resp.StopReason, len(resp.Content),
			u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens)

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

// markLastMessageForCache attaches cache_control: ephemeral to the final
// content block of the final message. Anthropic uses this as a cache
// breakpoint: the entire prefix up to and including this block is cached for
// 5 minutes. On the next call, Anthropic does longest-prefix matching — so
// even as new messages are appended, the cached prefix keeps hitting and only
// new content counts as uncached input tokens.
//
// Clears cache_control from previously-marked blocks first to keep only one
// active breakpoint (Anthropic allows up to 4, but one at the tail is
// simplest and avoids drift).
func markLastMessageForCache(messages []anthropic.Message) {
	if len(messages) == 0 {
		return
	}
	// Clear any prior breakpoints — we only want one active, at the tail.
	for i := range messages {
		for j := range messages[i].Content {
			messages[i].Content[j].CacheControl = nil
		}
	}
	last := &messages[len(messages)-1]
	if len(last.Content) == 0 {
		return
	}
	tail := &last.Content[len(last.Content)-1]
	tail.CacheControl = &anthropic.MessageCacheControl{
		Type: anthropic.CacheControlTypeEphemeral,
	}
}

// createMessagesWithRetry wraps client.CreateMessages with retry on 429
// (rate limit) and 529/overloaded. Uses exponential backoff with jitter,
// capped at 30s per wait, max 3 retries. Other errors propagate immediately.
func (a *Agent) createMessagesWithRetry(ctx context.Context, user *User, req anthropic.MessagesRequest) (anthropic.MessagesResponse, error) {
	const maxRetries = 3
	delay := 2 * time.Second

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := a.client.CreateMessages(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Only retry on rate limit / overloaded — other errors (invalid
		// request, auth, etc.) won't recover by waiting.
		var apiErr *anthropic.APIError
		if !errors.As(err, &apiErr) || (!apiErr.IsRateLimitErr() && !apiErr.IsOverloadedErr()) {
			return resp, err
		}
		if attempt == maxRetries {
			break
		}

		wait := delay
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		log.Printf("[%s] API %s — retry %d/%d in %s", user.Name, apiErr.Type, attempt+1, maxRetries, wait)
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
	return anthropic.MessagesResponse{}, lastErr
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

// buildSystemPromptStable returns the large, stable portion of the system
// prompt. Contains identity, rules, and tool descriptions — everything that
// does not change across calls. Marked with cache_control so Anthropic caches
// the prefix (system + tools + this block) for 5 min and subsequent calls in
// the same conversation only pay full tokens for new messages.
//
// userName is included here because it's stable per-user: caching per user
// still works well, and including it here lets the agent address the user by
// name in every response without needing a dynamic suffix for just that.
func buildSystemPromptStable(userName string) string {
	return fmt.Sprintf(`Voce e Charles Lurch, assistente pessoal de %s via WhatsApp. Seu nome e uma homenagem ao Lurch (Tropeço), o mordomo da Familia Adams. Ocasionalmente, com moderacao e bom timing, insira referencias sutis a isso — um "You rang?" quando chamado, um humor seco, uma formalidade exagerada por um instante. Nao force.

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

TIMEZONE E VIAGENS:
- O fuso base do usuario e America/Sao_Paulo (Brasil).
- O fuso e DINAMICO por periodo: quando o usuario vai estar em outro lugar, use registrar_viagem.
- Fluxo:
  - Declaracao EXPLICITA com datas ("vou pra Paris de 15 a 17/05") → chame registrar_viagem direto. O sistema ja lista os compromissos que ja existem na janela; pergunte ao usuario em linguagem natural quais ele quer manter no horario de Brasilia e quais quer converter para o fuso local.
  - Declaracao SEM data de volta ("estou em Londres") → PERGUNTE quando ele volta antes de chamar registrar_viagem.
  - Inferencia IMPLICITA ("amanha vou ao Louvre as 14h") → PRIMEIRO pergunte "voce vai estar em Paris amanha?" em texto natural, so chame registrar_viagem apos confirmacao. NUNCA registre viagem baseado so em inferencia.
  - Viagem cancelada ou adiada → cancelar_viagem.
  - Antes de criar evento em outro fuso, voce nao precisa checar nada: o sistema aplica automaticamente o fuso do periodo de viagem ativo na data do evento. So passe date/time como o usuario informou (no fuso local do destino).
- "14h em Paris" = 14h no horario de Paris. NUNCA converta manualmente — o sistema faz isso via registrar_viagem.
- Eventos sem contexto de viagem → America/Sao_Paulo (padrao).

RECORRENCIA:
- Aniversarios → use is_birthday=true (NAO use recurrence). O sistema cria como evento nativo de aniversario do Google (emoji 🎂, all-day, repete todo ano). Nao precisa passar time/duration.
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
- registrar_viagem, listar_viagens, cancelar_viagem: gerenciar periodos em outro fuso horario (veja secao TIMEZONE E VIAGENS).

Regras gerais:
- NUNCA finja ter executado uma acao sem chamar a ferramenta.
- NUNCA responda sobre agenda usando memoria da conversa — sempre consulte.
- Antes de criar evento, confira se ja foi criado. Nao duplique.
- Entenda audios e contatos compartilhados (transcritos automaticamente).

Estilo:
- Portugues, informal, profissional. MUITO conciso — 1-2 frases. Direto ao ponto.
- Formatacao WhatsApp: *negrito*, _italico_. NAO use markdown (**, ##).
- Sem emojis excessivos.`, userName)
}

// buildSystemPromptDynamic returns the per-call portion: current date/time
// (changes every minute) plus any context that varies per request (pending
// permission requests). Not cached — pays full tokens every call, but the
// block is small (~100-300 tokens).
func buildSystemPromptDynamic(pendingReq *PermissionRequest) string {
	now := time.Now().In(BRT()).Format("2006-01-02 15:04 (Monday)")
	out := fmt.Sprintf("Data/hora atual: %s (fuso: America/Sao_Paulo).", now)
	if pendingReq != nil {
		out += fmt.Sprintf(`

CONTEXTO ATUAL — SOLICITACAO DE PERMISSAO PENDENTE:
%s pediu autorizacao para criar um evento na agenda deste usuario. Dados do evento: %s
Se a resposta do usuario for para autorizar ou negar essa solicitacao, chame responder_permissao com a decisao apropriada (once/always/deny) baseada em como ele respondeu em linguagem natural. Se a resposta dele nao for sobre essa solicitacao, ignore este contexto e prossiga normalmente.`,
			pendingReq.RequesterName, pendingReq.EventData)
	}
	return out
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
					"recurrence": {"type": "string", "description": "Regra de recorrencia iCal para eventos recorrentes NAO-aniversario. Ex: RRULE:FREQ=WEEKLY;BYDAY=MO para toda segunda. Para aniversarios use is_birthday=true em vez disso."},
					"is_birthday": {"type": "boolean", "description": "Se true, cria como aniversario nativo do Google (all-day, recorrencia anual automatica, emoji 🎂). Use para qualquer aniversario. Nao precisa de time/duration/recurrence quando true."}
				},
				"required": ["title", "date"]
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
					"time": {"type": "string", "description": "Horario de inicio (HH:MM). Obrigatorio exceto para aniversarios."},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"},
					"recurrence": {"type": "string", "description": "RRULE para eventos recorrentes nao-aniversario"},
					"is_birthday": {"type": "boolean", "description": "Se true, cria como aniversario nativo do Google (all-day, anual)"}
				},
				"required": ["target_user", "title", "date"]
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
		{
			Name:        "registrar_viagem",
			Description: "Registra um periodo em que o usuario estara em outro fuso horario. Eventos criados ou listados nessas datas sao interpretados no fuso do destino; fora do periodo, tudo volta ao fuso padrao (America/Sao_Paulo). Chame sempre que o usuario declarar viagem EXPLICITA (ex: 'estarei em Paris de 15 a 17/05'). Para inferencias implicitas (ex: 'amanha vou ao Louvre as 14h'), PRIMEIRO pergunte em linguagem natural se ele estara mesmo em Paris nessa data e so chame a tool apos confirmacao.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"start_date": {"type": "string", "description": "Data de inicio da viagem (YYYY-MM-DD)"},
					"end_date": {"type": "string", "description": "Data de fim da viagem (YYYY-MM-DD, inclusiva)"},
					"timezone": {"type": "string", "description": "Fuso IANA do destino (ex: Europe/Paris, America/New_York)"},
					"location_name": {"type": "string", "description": "Nome legivel do local (ex: Paris, Nova York)"}
				},
				"required": ["start_date", "end_date", "timezone", "location_name"]
			}`),
		},
		{
			Name:        "listar_viagens",
			Description: "Lista as viagens futuras registradas pelo usuario.",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		},
		{
			Name:        "cancelar_viagem",
			Description: "Remove um periodo de viagem registrado. Use quando o usuario cancelar ou adiar uma viagem.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period_id": {"type": "integer", "description": "ID do periodo (obtido via listar_viagens). Preferivel."},
					"location_name": {"type": "string", "description": "Nome do local (fallback; busca fuzzy)"}
				}
			}`),
		},
		{
			Name:        "responder_permissao",
			Description: "Responde a uma solicitacao pendente de permissao de acesso a agenda (quando outro usuario pediu para criar evento na agenda deste). Use SO quando o contexto indicar que ha uma solicitacao pendente e o usuario respondeu autorizando ou negando. Interprete a resposta em linguagem natural e escolha a decisao adequada.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"decision": {
						"type": "string",
						"enum": ["once", "always", "deny"],
						"description": "once = autoriza so desta vez; always = autoriza permanente; deny = nega"
					}
				},
				"required": ["decision"]
			}`),
		},
	}
}
