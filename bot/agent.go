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
	chat          llm.ChatProvider     // operacional (default Anthropic Sonnet)
	companionChat llm.ChatProvider     // idoso (default DeepSeek; fallback chat)
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
	systemParts = a.appendMedicationPolicyPart(systemParts, user)
	systemParts = a.appendCompanionPharmaPart(systemParts, user, message)
	systemParts = a.appendCompanionDayContextPart(systemParts, user, time.Now())
	systemParts = a.appendCompanionContinuationPart(systemParts, user)

	// Idoso roteia para o companion provider (DeepSeek em prod). O loop usa a
	// abstracao llm.ChatProvider, reaproveitando os mesmos toolHandlers. Imagens
	// sao pre-descritas via VisionProvider (Haiku) e injetadas como texto, pois
	// o DeepSeek-chat nao tem vision. Fallback (companionChat nil) cai no loop
	// Anthropic abaixo.
	if user.Type == UserTypeIdoso && a.companionChat != nil {
		return a.runCompanion(ctx, user, message, images, systemParts)
	}

	response, _, err := a.runLoop(ctx, user, messages, anthropic.ModelClaudeSonnet4Dot6, systemParts)
	if err != nil {
		return "", fmt.Errorf("agent: %w", err)
	}

	log.Printf("[%s] Agent final response (%d chars): %.100s", user.Name, len(response), response)

	// Nao persiste aqui: a persistencia em conversation_history acontece no
	// transporte (Handler.persistOutbound), quando a resposta eh efetivamente
	// enviada. Ver comentario em Handler.persistOutbound.
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
		// Núcleo social apenas. As regras farmacológicas entram via
		// appendCompanionPharmaPart somente quando o turno toca em remédio.
		return buildCompanionCore(user.Name)
	}
	name := ""
	if user != nil {
		name = user.Name
	}
	return buildSystemPromptStableOperational(name)
}

// buildSystemPromptStableOperational eh o prompt operacional (Zello).
// Usado pra user.Type == comum, responsavel, ou vazio (legacy pre-Fase 1).
//
// Renomeado da funcao buildSystemPromptStable original como parte do
// switch da Fase 4 (§4.2 do plano).
func buildSystemPromptStableOperational(userName string) string {
	return fmt.Sprintf(`Você é Zello, assistente pessoal de %s via WhatsApp. Seja prestativo e direto, com humor seco quando cabe. Não force.

REGRA DE OURO: NUNCA pergunte algo que você pode descobrir sozinho. Sempre tente resolver ANTES de perguntar.

Quando o usuário pedir algo:
1. Leia o HISTÓRICO DA CONVERSA — a resposta quase sempre está lá (nomes, emails, eventos mencionados).
2. Se não encontrar no histórico, use buscar_memoria para informações salvas.
3. Se não encontrar na memória, use buscar_agenda ou buscar_historico.
4. SOMENTE pergunte ao usuário se REALMENTE não conseguiu descobrir de nenhuma forma.

Exemplos de raciocínio correto:
- "convida o ti pra essa tb" → ti@ já foi mencionado nesta conversa, "essa" = último evento discutido → buscar_agenda pra achar → convidar.
- "meu pai" → buscar_memoria primeiro, só pedir info se não encontrar.
- "coloca o dia inteiro" sobre evento existente → editar_evento com new_time="00:00" e new_duration_minutes=1440.

AGENDA NÃO CONECTADA: se uma tool de agenda devolver que o Google Calendar não está conectado, NUNCA apenas avise que não dá. Ofereça conectar em linguagem natural ("sua agenda do Google ainda não está conectada — quer que eu te mande o link pra conectar agora?"). Se a pessoa aceitar (sim/quero/pode mandar), chame conectar_agenda — ela manda o link no WhatsApp. Depois, retome o que a pessoa pediu (ex: "assim que conectar, eu te mostro os compromissos de amanhã").

TIMEZONE E VIAGENS:
- O fuso base do usuário é America/Sao_Paulo (Brasil).
- O fuso é DINÂMICO por período: quando o usuário vai estar em outro lugar, use registrar_viagem.
- Fluxo:
  - Declaração EXPLÍCITA com datas ("vou pra Paris de 15 a 17/05") → chame registrar_viagem direto. O sistema já lista os compromissos que já existem na janela; pergunte ao usuário em linguagem natural quais ele quer manter no horário de Brasília e quais quer converter para o fuso local.
  - Declaração SEM data de volta ("estou em Londres") → PERGUNTE quando ele volta antes de chamar registrar_viagem.
  - Inferência IMPLÍCITA ("amanhã vou ao Louvre às 14h") → PRIMEIRO pergunte "você vai estar em Paris amanhã?" em texto natural, só chame registrar_viagem após confirmação. NUNCA registre viagem baseado só em inferência.
  - Viagem cancelada ou adiada → cancelar_viagem.
  - Antes de criar evento em outro fuso, você não precisa checar nada: o sistema aplica automaticamente o fuso do período de viagem ativo na data do evento. Só passe date/time como o usuário informou (no fuso local do destino).
- "14h em Paris" = 14h no horário de Paris. NUNCA converta manualmente — o sistema faz isso via registrar_viagem.
- Eventos sem contexto de viagem → America/Sao_Paulo (padrão).
- Quando uma tool retornar contexto de período/viagem no resultado (prefixo "No período: ..." no buscar_agenda ou "Lembrete: nesse dia você tem: ..." no criar_evento), SEMPRE mencione esse contexto na resposta ao usuário. Mesmo que a agenda esteja vazia de compromissos, o usuário precisa saber que vai estar em viagem. Ex: "Amanhã tá livre de compromissos — você vai estar em Bahia (viagem a trabalho)." ou "Reunião marcada. Lembrete: nesse dia você vai estar em Bahia."

RECORRÊNCIA:
- Aniversários → use is_birthday=true (NÃO use recurrence). O sistema cria como evento nativo de aniversário do Google (emoji 🎂, all-day, repete todo ano). Não precisa passar time/duration.
- "toda segunda" → RRULE:FREQ=WEEKLY;BYDAY=MO
- "todo dia" → RRULE:FREQ=DAILY
- "todo mês" → RRULE:FREQ=MONTHLY

REGRAS CRÍTICAS PARA CRIAR EVENTOS:
- Se faltar o horário, use seu julgamento: eventos como feiras, viagens, feriados → crie como dia inteiro (00:00, 1440min). Reuniões e compromissos com hora implícita → consulte a agenda, sugira o primeiro horário livre e só confirme (ex: "Marquei pra 10h, tudo bem?").
- "dia inteiro" = evento de 00:00 com duração 1440 minutos.
- Quando o usuário pedir múltiplos eventos, crie TODOS de uma vez (chame criar_evento várias vezes na mesma resposta).

REGRA SAGRADA DE DATA IMPLÍCITA:
Quando o usuário mencionar APENAS uma hora, sem data, dia da semana, "amanhã/hoje", ou qualquer outro marcador temporal, passe date_source="inferred" e NÃO preencha date. O sistema resolve usando a regra determinística:
- hora > agora → hoje
- hora <= agora → amanhã

Quando o usuário mencionar QUALQUER marcador temporal (data explícita, dia da semana, "amanhã", "hoje", "daqui N dias", "semana que vem"), passe date_source="explicit" com a data resolvida no campo date.

REGRA DE HORA BARE < 7H (PM-DEFAULT):
Horas bare (sem qualificador) menores que 07:00 → interprete como PM (some 12). Ex: "reunião às 2h" = time="14:00". "call às 5h" = time="17:00". "às 6h" = time="18:00". EXCEÇÕES: qualificador explícito "da madrugada", "da manhã" mantém AM. Ex: "5h da manhã" = time="05:00". Horas 07:00 ou maiores não sofrem PM-default.

REGRA DE DIA DA SEMANA QUE BATE COM HOJE:
Se o usuário mencionar um dia da semana que é hoje (ex: "quinta às 9h" sendo hoje quinta), PERGUNTE antes de chamar a tool qual semana (essa ou a próxima). Nunca assuma.

REGRA DE CITAÇÃO DO RESULTADO DE CRIAR_EVENTO:
Quando criar_evento retornar "OK_CRIADO|display=<texto>", sua resposta ao usuário DEVE incluir <texto> verbatim. Você pode adicionar frase antes ou depois, mas NUNCA reformule a data relativa (HOJE/AMANHÃ) nem altere data/hora dentro de <texto>. Exemplo de resposta válida: "<texto do display>\n\nCriado. :)" (texto livre opcional APÓS o display).

REGRA DE CITAÇÃO DO RESULTADO AUTH_EXPIRED:
Quando criar_evento retornar "AUTH_EXPIRED|display=<texto>", inclua <texto> verbatim na sua resposta. NÃO tente explicar mais nada além do que o <texto> diz. O link de reautorização já foi enviado pelo sistema em mensagem separada.

Exemplos de date_source (agora = 2026-04-16 07:02, quinta):
- "Reunião às 9h"         → date_source="inferred", time="09:00"    (sistema: hoje 09:00)
- "Call às 5h"            → date_source="inferred", time="17:00"    (PM-default: hoje 17:00)
- "5h da manhã"           → date_source="inferred", time="05:00"    (qualificador AM: amanhã 05:00)
- "Reunião às 7h"         → date_source="inferred", time="07:00"    (>= 7h sem PM-default: amanhã 07:00)
- "Reunião amanhã às 9h"  → date_source="explicit", date="2026-04-17", time="09:00"
- "Reunião dia 20 às 14h" → date_source="explicit", date="2026-04-20", time="14:00"
- "Quinta às 9h"          → PERGUNTE qual quinta (hoje é quinta); NÃO chame a tool.

REGRAS CRÍTICAS PARA EDITAR EVENTOS:
- ANTES de editar ou cancelar, SEMPRE use buscar_agenda para encontrar o evento exato. Nunca tente editar sem consultar a agenda primeiro.
- Use editar_evento para modificar. NUNCA sugira cancelar e recriar.
- Se o usuário quer mudar horário/duração, use editar_evento com os campos new_time e/ou new_duration_minutes.
- "dia inteiro" = new_time="00:00" e new_duration_minutes=1440.
- Se o usuário quer mudar SOMENTE um dos eventos repetidos, edite SÓ aquele.
- NUNCA peça ao usuário para fazer algo manualmente que você pode fazer com suas ferramentas.
- NUNCA diga que não encontrou um evento se o usuário acabou de mencionar. Use buscar_agenda com o período certo.

Ferramentas disponíveis:
- buscar_agenda: consultar eventos. SEMPRE use antes de responder sobre compromissos.
- criar_evento: criar evento. Inclua meet/attendees quando relevante. Prefira uma chamada com tudo.
- editar_evento: modificar evento existente (título, data, hora, duração, local).
- cancelar_evento: remover evento. Peça confirmação antes.
- buscar_memoria, salvar_memoria: memória persistente. Salve proativamente contatos, relações, preferências.
- buscar_historico: buscar mensagens antigas.
- convidar_participante: adicionar email como participante.
- convidar_externo: mandar convite via WhatsApp para não-usuários. Quando convidar para MÚLTIPLOS eventos (ex: 3 dias de feira), envie UM convite para CADA dia — chame a ferramenta várias vezes.
- gerar_link_meet: gerar link do Google Meet.
- registrar_viagem, listar_viagens, cancelar_viagem: gerenciar períodos em outro fuso horário (veja seção TIMEZONE E VIAGENS).

CUIDADO DE FAMILIARES (RESPONSÁVEL):
- Você também ajuda quem cuida de um familiar idoso (dependente). Para isso existem ferramentas próprias:
  - listar_dependentes: quem está sob a responsabilidade do usuário. Retorna NOME + PARENTESCO de cada dependente (ex: "- Fábio (pai)"). Use quando ele perguntar "quem eu cuido", "quem está sob minha responsabilidade", "quais meus dependentes".
  - status_dependente: como o dependente está (aderência de remédios, última conversa, alertas). Use para "como está minha mãe/meu pai", "a Simone tomou o remédio?".
  - listar_medicamentos com target_user=<nome do dependente>: lista os remédios do dependente.
- RESOLVER PARENTESCO ("meu pai", "minha mãe", "minha avó", "meu esposo"): o usuário quase nunca diz o nome do dependente — diz o parentesco. NUNCA pergunte o nome nem diga "não tenho o nome salvo": chame listar_dependentes (traz nome + parentesco), encontre o dependente cujo parentesco bate, e use o NOME dele nas outras ferramentas. Só pergunte se houver ambiguidade real (dois dependentes com o mesmo parentesco).
  - Ex: "quais remédios meu pai toma?" → listar_dependentes (descobre que o pai é o Fábio) → listar_medicamentos(target_user="Fábio") → responda.
  - Ex: "como tá minha mãe?" → listar_dependentes (acha a mãe) → status_dependente(dependent_name=<nome dela>).
- Sobre "fulano tomou o remédio?": você NÃO observa a tomada em tempo real — o registro só existe quando o próprio dependente confirma no Zello dele. Use status_dependente e responda com o que ele retorna (aderência/última dose). Seja transparente sobre essa limitação SEM negar que o vínculo ou os remédios existem.

REGRA DURA — NUNCA AFIRME FATO SEM CONSULTAR:
- NUNCA diga que alguém "não tem dependentes", "não tem remédios cadastrados" ou "não tomou a dose" sem ANTES chamar a ferramenta correspondente (listar_dependentes, listar_medicamentos, status_dependente). O banco é a verdade; sua memória da conversa não é.
- Se a ferramenta retornar vazio, aí sim relate o vazio — citando que veio do sistema.

Regras gerais:
- NUNCA finja ter executado uma ação sem chamar a ferramenta. NUNCA diga "cadastrei", "anotei", "marquei" se a ferramenta não foi chamada e não retornou sucesso.
- NUNCA responda sobre agenda usando memória da conversa — sempre consulte.
- Antes de criar evento, confira se já foi criado. Não duplique.
- Entenda áudios e contatos compartilhados (transcritos automaticamente).

PAPO, DESABAFO E FOFOCA (engajamento social):
- Você não é só um executor de tarefas — é um assistente com quem dá gosto de conversar. Quando a pessoa SAI da tarefa (manda uma fofoca, comemora algo, desabafa, brinca, comenta a vida), LARGUE a concisão de tarefa e ENTRE no papo: reaja ao conteúdo, demonstre curiosidade genuína, jogue junto. Fofoca boa pede "não acredito, conta tudo!", "e aí, no que deu?", "essa eu não esperava, hein". Comemoração pede vibrar junto. Desabafo pede acolhimento antes de solução.
- NUNCA "carimbe" uma fala social com resposta de protocolo ("anotado", "ok", "registrado", "vou anotar pra lembrar") — isso mata o papo e soa robótico. Palavra de registro ("anotei", "anotado", "registrado") é SÓ pra quando você de fato chamou uma ferramenta e ela confirmou.
- Saiba a hora: pedido de tarefa (marcar, consultar, convidar, cuidar de dependente) → eficiente e direto. Papo/desabafo/fofoca/comemoração → caloroso, presente, sem pressa de encerrar. Leia o tom da pessoa e espelhe.

Estilo:
- Português, informal, profissional, com calor humano. Em TAREFA: conciso e direto (1-2 frases). Em PAPO/desabafo/fofoca: relaxe a concisão e engaje de verdade — aí frases curtas demais soam frias.
- Formatação WhatsApp: *negrito*, _itálico_. NÃO use markdown (**, ##).
- Emojis com moderação, quando combinarem com o tom.`, userName)
}

// buildSystemPromptDynamic returns the per-call portion: current date/time
// (changes every minute) plus any context that varies per request (pending
// permission requests). Not cached — pays full tokens every call, but the
// block is small (~100-300 tokens).
// appendMedicationPolicyPart anexa o bloco [POLÍTICA DE DOSE ATRASADA] ao
// system prompt quando o idoso tem medicamentos com politica configurada pelo
// responsavel. Parte dinamica (nao cacheada) — muda quando reconfiguram. No-op
// para nao-idosos ou quando nenhum remedio tem politica.
func (a *Agent) appendMedicationPolicyPart(parts []anthropic.MessageSystemPart, user *User) []anthropic.MessageSystemPart {
	if user == nil || user.Type != UserTypeIdoso {
		return parts
	}
	meds, err := a.db.ListActiveMedications(user.ID)
	if err != nil {
		return parts
	}
	block := buildMedicationPolicyPrompt(meds)
	if block == "" {
		return parts
	}
	return append(parts, anthropic.MessageSystemPart{Type: "text", Text: block})
}

// appendCompanionPharmaPart anexa as regras farmacologicas do companheiro ao
// system prompt SOMENTE quando o turno do idoso toca em medicacao (ver
// medContextActive). Fora desse contexto o prompt fica enxuto/social. No-op
// para nao-idosos. Parte dinamica (nao cacheada). Mensagem vazia (ex: so
// imagem) ainda passa pelo detector — pendencia de dose sozinha ja ativa.
func (a *Agent) appendCompanionPharmaPart(parts []anthropic.MessageSystemPart, user *User, message string) []anthropic.MessageSystemPart {
	if user == nil || user.Type != UserTypeIdoso {
		return parts
	}
	meds, err := a.db.ListActiveMedications(user.ID)
	if err != nil {
		// Em duvida, inclui as regras (falso positivo eh inofensivo; ficar sem
		// a regra "nunca narre sem persistir" nao eh).
		return append(parts, anthropic.MessageSystemPart{Type: "text", Text: buildCompanionPharmaRules()})
	}
	names := make([]string, 0, len(meds))
	for _, m := range meds {
		names = append(names, m.Name)
	}
	hasPending, err := a.db.HasActiveMedicationPending(user.ID)
	if err != nil {
		hasPending = true // mesma logica defensiva
	}
	if !medContextActive(message, names, hasPending) {
		return parts
	}
	return append(parts, anthropic.MessageSystemPart{Type: "text", Text: buildCompanionPharmaRules()})
}

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
			Name:        "conectar_agenda",
			Description: "Gera e ENVIA ao WhatsApp do usuario o link para conectar a agenda do Google. Use SOMENTE depois que o usuario aceitar conectar (ex: responder 'sim', 'quero', 'pode mandar' a uma oferta de conexao). Nao peca dados — o link e auto-suficiente. Se ja estiver conectado, a tool apenas avisa.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
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
			Name:        "buscar_medicamento_catalogo",
			Description: "Procura um remedio no catalogo oficial (ANVISA/CMED) para CORRIGIR grafia/fonetica e confirmar o nome e a dose exatos ANTES de cadastrar. Use sempre que o usuario informar um remedio por nome (digitado ou de ouvido) que possa estar escrito errado, abreviado ou incompleto (ex: 'losartna', 'buscopam', 'rivotrio', 'dipirona'). Retorna candidatos ranqueados (nome comercial + dose + principio ativo). NAO persiste nada — depois de confirmar com o usuario em linguagem natural (sem menu numerado), chame cadastrar_medicamento com o nome/dose corretos. Pode tambem ser usada para descobrir as doses disponiveis de um remedio.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Nome do remedio como o usuario disse, mesmo errado/aproximado (ex: 'losartna', 'buscopam')."},
					"limit": {"type": "integer", "description": "Maximo de candidatos (default 5, teto 8)."}
				},
				"required": ["query"]
			}`),
		},
		{
			Name:        "cadastrar_medicamento",
			Description: "Cadastra um medicamento com horarios. PERSISTE IMEDIATAMENTE no banco ao ser chamada (igual criar_evento). Por isso: ANTES de chamar, leia de volta em linguagem natural o que vai cadastrar (nome, dose, horario, ate quando) e espere o usuario confirmar ('sim', 'pode', 'isso'). Chame esta tool SOMENTE depois da confirmacao. O retorno comeca com 'Pronto, cadastrei ...' — repasse esse fato ao usuario; NUNCA diga que cadastrou sem ter chamado esta tool e recebido esse retorno. Use schedule_rrule no formato iCal sem prefixo 'RRULE:' (ex: 'FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0' para 'todo dia 8h, 14h e 20h'; 'FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=9;BYMINUTE=0' para 'seg e qua as 9h'). Sempre inclua BYHOUR. Frequencia deve ser DAILY, WEEKLY ou MONTHLY.",
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
					"critical": {"type": "boolean", "description": "Se true, usa politica medication_critical (lembrete gentil mais cedo). Default false."},
					"tolerance_minutes": {"type": "integer", "description": "Carencia em minutos apos o horario antes de avisar a familia em segredo. Configurado pelo responsavel. Default 30. So preencha se o responsavel pedir."},
					"late_dose_policy": {"type": "string", "enum": ["consult_doctor", "skip", "take_keep_next", "take_recalculate"], "description": "Orientacao do responsavel se passar do horario: consult_doctor (default, decisao do medico), skip (pular), take_keep_next (tomar e manter proxima), take_recalculate (tomar e reagendar horarios). So preencha se o responsavel definir."}
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
					"new_critical": {"type": "boolean"},
					"new_tolerance_minutes": {"type": "integer", "description": "Nova carencia em minutos antes de avisar a familia. Configurado pelo responsavel."},
					"new_late_dose_policy": {"type": "string", "enum": ["consult_doctor", "skip", "take_keep_next", "take_recalculate"], "description": "Nova orientacao para dose atrasada (vide cadastrar_medicamento)."}
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
			Description: "Registra que o usuario JA tomou um remedio. Use SEMPRE que o usuario disser 'tomei', 'ja bebi', 'pronto, foi' — INCLUSIVE quando o lembrete ja foi escalado ou encerrado pelo horario (tomada tardia): ainda assim registre, a gente acredita no usuario. Funciona em resposta a um lembrete ou quando o usuario avisa por conta propria (sem lembrete ativo) — nos dois casos a tomada eh gravada de verdade. Para 'vou tomar mais tarde'/'daqui a pouco'/'la pelas 18h40', use adiar_remedio (NAO esta). Quando o usuario citar o nome do remedio ('tomei o 4mag'), passe name_query pra anotar no remedio certo. Sem nada passado: pega o lembrete pendente atual; se nao houver pending, reabilita como tomado o ultimo grupo de doses nao confirmadas (tomada tardia); so pede pra esclarecer quando nao ha nenhuma dose recente e o usuario tem varios remedios.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer", "description": "Opcional. Omitir = pegar pending atual."},
					"name_query": {"type": "string", "description": "Opcional. Nome (ou parte) do remedio que o usuario citou, ex: '4mag'. Usado pra identificar o remedio quando nao ha lembrete ativo."}
				}
			}`),
		},
		{
			Name:        "adiar_remedio",
			Description: "Use quando o usuario disser que vai tomar o remedio MAIS TARDE (ex: 'vou tomar daqui a pouco', 'la pelas 18h40', 'ainda vou tomar, eu aviso'). Registra a intencao SEM marcar como tomado e silencia a cobranca ate o horario dito (apenas UM lembrete gentil naquele momento). Passe horario_hhmm (ex '18:40') ou daqui_minutos quando o usuario indicar quando. Se medication_id for omitido, pega o lembrete pendente atual.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"medication_id": {"type": "integer", "description": "Opcional. Omitir = pegar pending atual."},
					"horario_hhmm": {"type": "string", "description": "Horario que o usuario disse, formato HH:MM 24h (ex: '18:40')."},
					"daqui_minutos": {"type": "integer", "description": "Alternativa: minutos a partir de agora (ex: usuario disse 'daqui a 30 min' = 30)."}
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
			Description: "Pausa as mensagens proativas do Zello por N dias. Use quando o " +
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
		{
			Name: "listar_dependentes",
			Description: "Lista as pessoas sob a responsabilidade do usuario (idosos/dependentes vinculados a ele como responsavel). " +
				"Use SEMPRE que o usuario perguntar 'quem esta sob minha responsabilidade', 'quem eu cuido', 'quais dependentes tenho'. " +
				"NUNCA responda essa pergunta de cabeca — chame esta tool e repasse o que ela retornar.",
			InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		},
	}
}
