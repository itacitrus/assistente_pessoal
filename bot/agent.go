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

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/liushuangls/go-anthropic/v2"
)

// Agent eh o orquestrador de chat com Claude. A Fase 4 introduz a camada
// de provider abstraction (bot/llm/): o agent passa a CONHECER varios
// providers (chat operacional, companion, analysis, report, vision) e
// roteia conforme user.Type via pickChat.
//
// O campo `client` (SDK Anthropic direto) eh mantido pra preservar
// comportamento atual do Run() operacional — a tradução completa pra
// llm.ChatProvider ficou como follow-up. Contracts publicos do Agent nao
// mudaram; `client` segue como o caminho default quando companionChat
// nao esta configurado.
//
// Roteamento de companion (idoso) usa companionChat se setado. Snapshot
// writer (Fase 4 §10) usa snapshotWriter (interface) — Fase 5 vai injetar
// implementacao concreta.
type Agent struct {
	// SDK direto Anthropic — usado por Run() (caminho operacional). Mantido
	// pra preservar comportamento dos 150 testes existentes.
	client *anthropic.Client

	// Provider abstraction (Fase 4). Operacional = Anthropic Sonnet;
	// companion = DeepSeek (default) ou Anthropic se nao configurado.
	chat          llm.ChatProvider // operacional (default Anthropic Sonnet)
	companionChat llm.ChatProvider // idoso (default DeepSeek; fallback chat)
	analysis      llm.AnalysisProvider // snapshot writer (Haiku)
	report        llm.ReportProvider   // sintese pro responsavel (Sonnet)
	vision        llm.VisionProvider   // descricao de imagem (Haiku)

	// Snapshot writer hook — interface no proprio pacote (snapshotwriter.go).
	// Fase 5 vai injetar implementacao concreta; Fase 4 deixa o gancho.
	snapshotWriter SnapshotWriter

	// MediaLoader pra comentar_imagem. Default nil = handler responde
	// "cache nao configurado". PR-MEDIA-1 (Fase 4) injeta MediaCache real.
	media MediaLoader

	cal     *CalendarClient
	db      *DB
	cfg     *Config
	perms   *PermissionManager
	audit   *AuditLog
	sendMsg func(phone, text string) error
}

type ToolHandler func(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error)

// NewAgent constroi o agent com SDK direto pra Anthropic (caminho
// operacional). Compat retro: assinatura mantida da Fase 3.
//
// Pra construir com providers Fase 4, use NewAgent + a.WithProviders(...)
// (preferido) ou NewAgentWithProviders (helper).
func NewAgent(apiKey string, cal *CalendarClient, db *DB, cfg *Config, sendMsg func(phone, text string) error) *Agent {
	return &Agent{
		client:         anthropic.NewClient(apiKey),
		cal:            cal,
		db:             db,
		cfg:            cfg,
		perms:          NewPermissionManager(db),
		audit:          NewAuditLog(db),
		sendMsg:        sendMsg,
		snapshotWriter: noopSnapshotWriter{},
	}
}

// WithProviders configura os providers do bot/llm/ para a Fase 4. Fluent
// pra encadear configuracao em main.go. Aceita nil — se chat=nil, mantem
// o caminho atual via SDK Anthropic direto. Se companionChat=nil, idoso
// cai no chat (que pode ser Anthropic).
func (a *Agent) WithProviders(chat, companion llm.ChatProvider, analysis llm.AnalysisProvider, report llm.ReportProvider, vision llm.VisionProvider) *Agent {
	a.chat = chat
	a.companionChat = companion
	a.analysis = analysis
	a.report = report
	a.vision = vision
	return a
}

// WithSnapshotWriter injeta uma implementacao de SnapshotWriter. Fase 5
// chama isso com a impl concreta de snapshotter. Default = noop.
func (a *Agent) WithSnapshotWriter(s SnapshotWriter) *Agent {
	if s == nil {
		s = noopSnapshotWriter{}
	}
	a.snapshotWriter = s
	return a
}

// WithMediaLoader injeta um MediaLoader pra comentar_imagem (Fase 4).
// PR-MEDIA-1 vai injetar a impl concreta (MediaCache em disco). Default
// nil = handler retorna "cache nao configurado".
func (a *Agent) WithMediaLoader(m MediaLoader) *Agent {
	a.media = m
	return a
}

// pickChat retorna o ChatProvider apropriado pra user.Type. Roteamento:
//   - user.Type == idoso E companionChat != nil → companion (DeepSeek default).
//   - resto OU companion nil → chat operacional (Anthropic Sonnet).
//   - chat nil → fallback nil (caller deve usar caminho legacy via client).
//
// Quando ambos chat/companion sao nil (testes que nao injetam), retorna
// nil. Caller (Run) entao vai pelo caminho com SDK direto.
func (a *Agent) pickChat(user *User) llm.ChatProvider {
	if user != nil && user.Type == UserTypeIdoso && a.companionChat != nil {
		return a.companionChat
	}
	return a.chat
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

					log.Printf("[%s] Tool call: %s input=%s", user.Name, toolName, string(toolInput))

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
						// Log the exact string we ship back to the model. Lets a
						// post-mortem see if the agent hallucinated success from
						// a CONFLITO/error-payload-as-string the handler returned.
						preview := result
						if len(preview) > 500 {
							preview = preview[:500] + "...[truncated]"
						}
						log.Printf("[%s] Tool %s result: %s", user.Name, toolName, preview)
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

// buildSystemPromptStable retorna o system prompt apropriado para o
// user.Type. Roteador da Fase 4: idoso recebe persona companion;
// outros tipos (comum, responsavel, vazio legacy) recebem o operacional.
// O texto retornado e estavel por persona — Anthropic faz longest-prefix
// matching, entao cada persona tem seu cache distinto sem cache thrashing.
//
// CRITICO: user.Type eh estavel por conversa (so muda via admin). Cache
// hit rate em conversa multi-turno fica acima de 70% facilmente.
func buildSystemPromptStable(user *User) string {
	if user != nil && user.Type == UserTypeIdoso {
		return buildCompanionPrompt(user.Name)
	}
	name := ""
	if user != nil {
		name = user.Name
	}
	return buildSystemPromptStableOperational(name)
}

// buildSystemPromptStableOperational eh o prompt original (Charles Lurch).
// Usado pra user.Type == comum, responsavel, ou vazio (legacy pre-Fase 1).
//
// Renomeado da funcao buildSystemPromptStable original como parte do
// switch da Fase 4 (§4.2 do plano).
func buildSystemPromptStableOperational(userName string) string {
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
- Quando uma tool retornar contexto de periodo/viagem no resultado (prefixo "No periodo: ..." no buscar_agenda ou "Lembrete: nesse dia voce tem: ..." no criar_evento), SEMPRE mencione esse contexto na resposta ao usuario. Mesmo que a agenda esteja vazia de compromissos, o usuario precisa saber que vai estar em viagem. Ex: "Amanha tá livre de compromissos — você vai estar em Bahia (viagem a trabalho)." ou "Reunião marcada. Lembrete: nesse dia você vai estar em Bahia."

RECORRENCIA:
- Aniversarios → use is_birthday=true (NAO use recurrence). O sistema cria como evento nativo de aniversario do Google (emoji 🎂, all-day, repete todo ano). Nao precisa passar time/duration.
- "toda segunda" → RRULE:FREQ=WEEKLY;BYDAY=MO
- "todo dia" → RRULE:FREQ=DAILY
- "todo mes" → RRULE:FREQ=MONTHLY

REGRAS CRITICAS PARA CRIAR EVENTOS:
- Se faltar o horario, use seu julgamento: eventos como feiras, viagens, feriados → crie como dia inteiro (00:00, 1440min). Reunioes e compromissos com hora implicita → consulte a agenda, sugira o primeiro horario livre e so confirme (ex: "Marquei pra 10h, tudo bem?").
- "dia inteiro" = evento de 00:00 com duracao 1440 minutos.
- Quando o usuario pedir multiplos eventos, crie TODOS de uma vez (chame criar_evento varias vezes na mesma resposta).

REGRA SAGRADA DE DATA IMPLICITA:
Quando o usuario mencionar APENAS uma hora, sem data, dia da semana, "amanha/hoje", ou qualquer outro marcador temporal, passe date_source="inferred" e NAO preencha date. O sistema resolve usando a regra deterministica:
- hora > agora → hoje
- hora <= agora → amanha

Quando o usuario mencionar QUALQUER marcador temporal (data explicita, dia da semana, "amanha", "hoje", "daqui N dias", "semana que vem"), passe date_source="explicit" com a data resolvida no campo date.

REGRA DE HORA BARE < 7H (PM-DEFAULT):
Horas bare (sem qualificador) menores que 07:00 → interprete como PM (some 12). Ex: "reuniao as 2h" = time="14:00". "call as 5h" = time="17:00". "as 6h" = time="18:00". EXCECOES: qualificador explicito "da madrugada", "da manha" mantem AM. Ex: "5h da manha" = time="05:00". Horas 07:00 ou maiores nao sofrem PM-default.

REGRA DE DIA DA SEMANA QUE BATE COM HOJE:
Se o usuario mencionar um dia da semana que e hoje (ex: "quinta as 9h" sendo hoje quinta), PERGUNTE antes de chamar a tool qual semana (essa ou a proxima). Nunca assuma.

REGRA DE CITACAO DO RESULTADO DE CRIAR_EVENTO:
Quando criar_evento retornar "OK_CRIADO|display=<texto>", sua resposta ao usuario DEVE incluir <texto> verbatim. Voce pode adicionar frase antes ou depois, mas NUNCA reformule a data relativa (HOJE/AMANHA) nem altere data/hora dentro de <texto>. Exemplo de resposta valida: "<texto do display>\n\nCriado. :)" (texto livre opcional APOS o display).

REGRA DE CITACAO DO RESULTADO AUTH_EXPIRED:
Quando criar_evento retornar "AUTH_EXPIRED|display=<texto>", inclua <texto> verbatim na sua resposta. NAO tente explicar mais nada alem do que o <texto> diz. O link de reautorizacao ja foi enviado pelo sistema em mensagem separada.

Exemplos de date_source (agora = 2026-04-16 07:02, quinta):
- "Reuniao as 9h"         → date_source="inferred", time="09:00"    (sistema: hoje 09:00)
- "Call as 5h"            → date_source="inferred", time="17:00"    (PM-default: hoje 17:00)
- "5h da manha"           → date_source="inferred", time="05:00"    (qualificador AM: amanha 05:00)
- "Reuniao as 7h"         → date_source="inferred", time="07:00"    (>= 7h sem PM-default: amanha 07:00)
- "Reuniao amanha as 9h"  → date_source="explicit", date="2026-04-17", time="09:00"
- "Reuniao dia 20 as 14h" → date_source="explicit", date="2026-04-20", time="14:00"
- "Quinta as 9h"          → PERGUNTE qual quinta (hoje e quinta); NAO chame a tool.

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
					"date_source": {"type": "string", "enum": ["explicit", "inferred"], "description": "explicit quando o usuario mencionou qualquer marcador temporal (data, dia da semana, amanha, hoje, daqui N dias). inferred quando o usuario mencionou APENAS hora, sem nenhum marcador temporal. OBRIGATORIO."},
					"date": {"type": "string", "description": "Data YYYY-MM-DD. Obrigatorio quando date_source=explicit. IGNORADO pelo sistema quando date_source=inferred (o sistema resolve via regra deterministica: hora > agora -> hoje; hora <= agora -> amanha)."},
					"time": {"type": "string", "description": "Horario de inicio HH:MM. Para horas bare menores que 07:00 sem qualificador, aplique PM-default (ex: '2h' -> 14:00, '5h' -> 17:00). Qualificadores 'da madrugada'/'da manha' mantem AM."},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"},
					"com_meet": {"type": "boolean", "description": "Gera link do Google Meet. SOMENTE passe true quando o usuario pedir explicitamente (ex: 'com meet', 'remoto', 'online', 'videochamada', 'por video', 'chamada') OU quando o contexto deixar obvio que e remoto (ex: participantes em outra cidade sem local fisico). NUNCA infira Meet so porque e 'reuniao'. Reunioes presenciais sao o default."},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Emails de participantes (opcional, NAO peca proativamente)"},
					"force_conflict": {"type": "boolean", "description": "Se true, cria mesmo com conflito de horario (so usar apos usuario confirmar)"},
					"timezone": {"type": "string", "description": "Fuso horario IANA (ex: Europe/London). Default: America/Sao_Paulo."},
					"recurrence": {"type": "string", "description": "Regra de recorrencia iCal para eventos recorrentes NAO-aniversario. Ex: RRULE:FREQ=WEEKLY;BYDAY=MO para toda segunda. Para aniversarios use is_birthday=true em vez disso."},
					"is_birthday": {"type": "boolean", "description": "Se true, cria como aniversario nativo do Google (all-day, recorrencia anual automatica, emoji 🎂). Use para qualquer aniversario. Nao precisa de time/duration/recurrence quando true."}
				},
				"required": ["title", "date_source"]
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
			Description: "Salva uma informacao sobre o usuario para lembrar no futuro. Use para contatos, preferencias, enderecos, relacoes pessoais, etc. Salve PROATIVAMENTE quando o usuario mencionar informacoes pessoais relevantes. Para idosos no modo companion, use category=social_context para pessoas/eventos/rotinas/interesses/relatos do dia-a-dia (chave com prefixo: pessoa:nome, evento:descr, rotina:descr, interesse:tema, relato:descr). Use prefixo de chave 'risco:' SOMENTE quando ha componente real de saude/seguranca (queda, dor toracica, isolamento prolongado) — essas memorias atravessam a fronteira de privacidade e chegam ao relatorio do responsavel.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Categoria: contato, endereco, preferencia, relacao, trabalho, social_context, outro. Use social_context para fofoca social do idoso (pessoas, eventos, rotinas, interesses)."},
					"key": {"type": "string", "description": "Identificador curto. Em social_context, use prefixo de tipo: pessoa:nome_snake, evento:descr_snake, rotina:nome, interesse:tema, relato:descr. Use prefixo 'risco:' (ex: risco:queda_recente) SOMENTE para sinais reais de saude/seguranca — risco: atravessa fronteira de privacidade e chega ao relatorio do responsavel."},
					"value": {"type": "string", "description": "Informacao completa (ex: Fabio de Freitas - 61982279928, ou 'vizinha do 302, tem gato Bigode')"}
				},
				"required": ["category", "key", "value"]
			}`),
		},
		{
			Name:        "buscar_memoria",
			Description: "Busca informacoes salvas sobre o usuario (contatos, preferencias, enderecos, etc). Use ANTES de pedir informacoes que o usuario ja pode ter fornecido antes. Para idosos no modo companion, busque com category=social_context no inicio de cada conversa pra puxar 2-3 contextos recentes — evita perguntar de novo o que ele ja contou.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Termo de busca (ex: pai, escritorio, endereco, pessoa, evento)"},
					"category": {"type": "string", "description": "Filtrar por categoria (opcional): contato, endereco, preferencia, relacao, trabalho, social_context"}
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
		// Fase 3 (idosos): medicacao + escalacao.
		{
			Name:        "cadastrar_medicamento",
			Description: "Cadastra um medicamento com horarios. Cria pending_confirmation; o usuario confirma na proxima mensagem antes da persistencia. Use schedule_rrule no formato iCal sem prefixo 'RRULE:' (ex: 'FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0' para 'todo dia 8h, 14h e 20h'; 'FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=9;BYMINUTE=0' para 'seg e qua as 9h'). Sempre inclua BYHOUR. Frequencia deve ser DAILY, WEEKLY ou MONTHLY.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"target_user": {"type": "string", "description": "Nome do usuario alvo (omitir = self). Se diferente, exige vinculo em family_links."},
					"name": {"type": "string", "description": "Nome do medicamento (ex: Losartana, AAS, Metformina)"},
					"dose": {"type": "string", "description": "Dose (ex: '50mg', '1 comprimido', '10 gotas')"},
					"instructions": {"type": "string", "description": "Instrucoes (ex: 'em jejum', 'com agua', 'apos almoco')"},
					"schedule_rrule": {"type": "string", "description": "RRULE iCal. Ex: 'FREQ=DAILY;BYHOUR=8;BYMINUTE=0' (1x/dia 8h)."},
					"start_date": {"type": "string", "description": "Data de inicio YYYY-MM-DD. Default: hoje."},
					"end_date": {"type": "string", "description": "Data de fim YYYY-MM-DD (inclusiva). Omitir = continuo."},
					"critical": {"type": "boolean", "description": "Se true, usa politica medication_critical (5 tentativas, 3min). Default false."}
				},
				"required": ["name", "schedule_rrule"]
			}`),
		},
		{
			Name:        "listar_medicamentos",
			Description: "Lista medicamentos ativos do usuario (ou de outro via target_user, com vinculo familiar).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"target_user": {"type": "string", "description": "Nome do usuario (omitir = self)."}
				}
			}`),
		},
		{
			Name:        "editar_medicamento",
			Description: "Edita campos de um medicamento existente. Para mudar horario, passe new_schedule_rrule (substitui todos os schedules atuais). Pode passar id direto ou nome aproximado em name_query.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer", "description": "ID do medicamento (preferivel)."},
					"name_query": {"type": "string", "description": "Nome aproximado (fallback)."},
					"new_name": {"type": "string"},
					"new_dose": {"type": "string"},
					"new_instructions": {"type": "string"},
					"new_schedule_rrule": {"type": "string", "description": "RRULE substituindo todos os schedules."},
					"new_end_date": {"type": "string", "description": "Nova data de fim YYYY-MM-DD."},
					"new_critical": {"type": "boolean"}
				}
			}`),
		},
		{
			Name:        "cancelar_medicamento",
			Description: "Cancela um medicamento (soft-delete: active=0). Lembretes futuros param. Historico de tomadas eh preservado. Sempre peca razao ao usuario antes de chamar.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer"},
					"name_query": {"type": "string"},
					"reason": {"type": "string", "description": "Motivo (ex: 'medico tirou', 'nao preciso mais')."}
				}
			}`),
		},
		{
			Name:        "marcar_remedio_tomado",
			Description: "Registra que o usuario tomou um remedio. Use SEMPRE que o usuario disser 'tomei', 'ja bebi', 'pronto, foi', em resposta a um lembrete. NUNCA chame quando o usuario disser 'vou tomar', 'daqui a pouco', 'ja ja' (eh futuro, nao confirma — apenas faca um ack textual). Se medication_id for omitido, pega o lembrete pendente atual (kind='medication').",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer", "description": "Opcional. Omitir = pegar pending atual."}
				}
			}`),
		},
		{
			Name:        "pular_dose",
			Description: "Registra que o usuario decidiu pular a dose atual. Salva razao e marca intake_log status='skipped'. NAO cancela o medicamento (proximas doses continuam). SEMPRE pergunte a razao em texto natural ao usuario antes de chamar.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer"},
					"reason": {"type": "string", "description": "Razao do skip (ex: 'estou enjoado', 'esqueci de comprar')."}
				},
				"required": ["reason"]
			}`),
		},
		{
			Name:        "extrair_receita_imagem",
			Description: "Use SOMENTE quando o usuario enviou uma imagem que parece ser receita medica (lista de remedios manuscrita ou impressa). Extrai cada item da receita olhando a imagem. APOS extrair, voce DEVE apresentar item-a-item ao usuario em linguagem natural (sem menu numerado), perguntar o horario de cada um, e chamar cadastrar_medicamento para cada item confirmado. Se a dose nao estiver clara na imagem, pergunte ao usuario; NAO invente.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"items": {
						"type": "array",
						"description": "Lista de medicamentos identificados na imagem.",
						"items": {
							"type": "object",
							"properties": {
								"name": {"type": "string", "description": "Nome do remedio"},
								"dose": {"type": "string", "description": "Dose escrita na receita"},
								"frequency_text": {"type": "string", "description": "Frequencia em texto livre, exatamente como escrito (ex: '1x ao dia', '8/8h', 'em jejum')"},
								"duration_text": {"type": "string", "description": "Duracao do tratamento se mencionada (ex: '7 dias', 'continuo', 'ate acabar')"}
							},
							"required": ["name"]
						}
					}
				},
				"required": ["items"]
			}`),
		},
		// Fase 4 (idosos): tools do companion. So fazem sentido quando
		// user.Type=idoso — handlers tem guard explicito.
		{
			Name: "alertar_familia",
			Description: "Envia um alerta para os familiares do idoso quando voce detecta " +
				"um sinal serio (ideacao suicida, sintoma agudo, queda, recusa de comer/beber, " +
				"violencia/negligencia, ou padrao persistente preocupante). Esta e a UNICA " +
				"tool para acionar a familia em sinal de risco. Use com calibracao: critical " +
				"para risco agudo, warn para padrao preocupante mas nao agudo, info para " +
				"observacao a registrar. Quando em duvida entre warn e critical, escolha " +
				"critical. Esta tool so faz sentido quando user.Type=idoso. " +
				"O retorno desta tool inclui um JSON com `disclose_to_elder` e `suggested_tone` — " +
				"voce DEVE seguir essas orientacoes na resposta ao idoso. Em particular, em " +
				"category=psicologico/violencia/negligencia, NAO mencione ao idoso que voce " +
				"alertou a familia (preserva a confianca dele).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"severity": {
						"type": "string",
						"enum": ["info", "warn", "critical"],
						"description": "info=observar, warn=preocupante mas nao agudo, critical=acionar agora."
					},
					"category": {
						"type": "string",
						"enum": ["medico_fisico", "psicologico", "violencia", "negligencia", "outros"],
						"description": "Categoria do sinal. Define se voce mencionara ao idoso que avisou a familia. medico_fisico (sintoma agudo, queda, dor) -> pode mencionar; psicologico (ideacao, ruminacao) -> NAO mencione; violencia/negligencia -> NAO mencione (pode escalar risco fisico); outros -> handler te diz no retorno."
					},
					"reason": {
						"type": "string",
						"description": "Descricao breve e factual em PT-BR do que voce observou. 1-2 frases. Sem interpretacao clinica."
					},
					"recommended_action": {
						"type": "string",
						"description": "Sugestao opcional do que a familia pode fazer agora (ex: 'ligar pra ele agora', 'passar la hoje')."
					}
				},
				"required": ["severity", "category", "reason"]
			}`),
		},
		{
			Name: "pausar_proatividade",
			Description: "Pausa as mensagens proativas do Lurch por N dias. Use quando o " +
				"idoso pedir tregua ('nao me chame por uma semana', 'me deixa quieto uns dias'). " +
				"Confirme em linguagem natural antes de chamar.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"dias": {"type": "integer", "minimum": 1, "maximum": 30, "description": "Quantos dias pausar (1 a 30)."}
				},
				"required": ["dias"]
			}`),
		},
		{
			Name: "comentar_imagem",
			Description: "Quando o idoso enviou uma imagem (foto, sticker, GIF) e voce " +
				"quer comentar sobre ela, use esta tool. Recebe um image_id (referencia " +
				"ao blob recebido). Retorna uma descricao curta em PT-BR (2-3 frases) e " +
				"uma classificacao de tom sugerido (familia, meme, paisagem, comida, " +
				"religioso, humoristico, outros). Voce DEVE incorporar a descricao numa " +
				"resposta natural ao idoso — nao cite a tool, nao seja robotico, comente " +
				"como amigo: 'que linda essa foto!', 'eita, esse meme e bom mesmo'.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"image_id": {
						"type": "string",
						"description": "ID da imagem recebida pelo handler de WhatsApp (sha1 do blob no media_cache)."
					},
					"context_hint": {
						"type": "string",
						"description": "Opcional. Pista de contexto — ex: 'veio em grupo da familia', 'enviou logo apos falar do neto'."
					}
				},
				"required": ["image_id"]
			}`),
		},
		{
			Name: "comentar_link",
			Description: "Quando o idoso enviou uma URL (link de noticia, video, post de " +
				"rede social), use esta tool pra extrair contexto leve. Retorna titulo, " +
				"descricao breve, host e (se houver) URL da imagem de previa. NAO faz " +
				"fact-check, NAO resume reportagem inteira — voce e amigo, nao jornalista. " +
				"Comente leve. Se o dominio nao estiver na lista permitida, a tool retorna " +
				"string explicativa — nesse caso, peca pro idoso te contar do que se trata.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL completa, com http:// ou https://."}
				},
				"required": ["url"]
			}`),
		},
		// Fase 5 (idosos): tool do responsavel. Authz: db.IsGuardianOf.
		// Sem vinculo familiar = mensagem natural negando.
		{
			Name: "status_dependente",
			Description: "Retorna estado longitudinal de um dependente (idoso) sob responsabilidade do usuario. " +
				"Disponivel APENAS quando family_links autoriza (db.IsGuardianOf). Inclui aderencia de " +
				"medicacao 7d, ultima conversa, alertas em aberto, tendencia das ultimas 2 semanas, e " +
				"sintese acolhedora gerada por sub-agente longitudinal. NUNCA retorna citacoes literais " +
				"do que o idoso disse — apenas observacoes agregadas. Use quando o usuario perguntar " +
				"'como esta minha mae/pai/avo'. Pelo menos um identificador (id, telefone ou nome) tem " +
				"que ser passado.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"dependent_id":    {"type": "integer", "description": "ID do dependente (preferencial)."},
					"dependent_phone": {"type": "string",  "description": "Telefone do dependente (fallback) — apenas digitos com DDD."},
					"dependent_name":  {"type": "string",  "description": "Nome do dependente (fallback fuzzy entre dependentes do guardian)."},
					"days":            {"type": "integer", "description": "Janela de analise em dias (default 14, max 90)."}
				}
			}`),
		},
	}
}
