package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	anthropic "github.com/liushuangls/go-anthropic/v2"
)

// maxLeadTurnsPerDay limita quantos turnos de LLM um numero frio consome em 24h.
// Defesa anti-abuso (numeros errados, spam) — alto o bastante para nunca atrapalhar
// um interessado real. Acima disso, paramos de responder ate a janela rolar.
const maxLeadTurnsPerDay = 60

// salesMaxIterations limita o loop de tool-use do agente de vendas. 1 chamada
// de criar_conta + fechamento basta; folga pequena cobre reconfirmacao de nome.
const salesMaxIterations = 4

// RunSalesAgent conduz a conversa de aquisicao com um numero NAO cadastrado.
// Persona de vendedor simpatico: apresenta o Zello, responde duvidas e conduz
// ao cadastro; quando a pessoa confirma interesse, provisiona a conta via tool
// criar_conta (nunca narra cadastro sem persistir). pushName eh o nome do perfil
// WhatsApp, usado como palpite a confirmar no momento do cadastro.
func (a *Agent) RunSalesAgent(ctx context.Context, phone, pushName, message string) (string, error) {
	if err := a.db.UpsertLead(phone, strings.TrimSpace(pushName)); err != nil {
		log.Printf("[lead %s] upsert: %v", phone, err)
	}

	// Anti-abuso: nao gasta LLM com numero que ja excedeu a cota diaria.
	if n, err := a.db.CountLeadMessagesSince(phone, time.Now().Add(-24*time.Hour)); err == nil && n >= maxLeadTurnsPerDay {
		log.Printf("[lead %s] daily turn cap reached (%d) — staying silent", phone, n)
		return "", nil
	}

	if err := a.db.AddLeadMessage(phone, "user", message); err != nil {
		log.Printf("[lead %s] add user message: %v", phone, err)
	}

	history, _ := a.db.GetLeadMessages(phone, 30)
	messages := buildLeadMessages(history)

	system := buildSalesSystemPrompt(strings.TrimSpace(pushName))
	tools := []anthropic.ToolDefinition{salesCreateAccountTool()}

	response, err := a.runSalesLoop(ctx, phone, messages, system, tools)
	if err != nil {
		return "", err
	}

	if response != "" {
		if err := a.db.AddLeadMessage(phone, "assistant", response); err != nil {
			log.Printf("[lead %s] add assistant message: %v", phone, err)
		}
	}
	return response, nil
}

// runSalesLoop roda Haiku com a tool criar_conta ate end_turn. Auto-contido
// (sem *User): o lead ainda nao tem conta.
func (a *Agent) runSalesLoop(ctx context.Context, phone string, messages []anthropic.Message, system string, tools []anthropic.ToolDefinition) (string, error) {
	temp := float32(0.5)
	for i := 0; i < salesMaxIterations; i++ {
		resp, err := a.client.CreateMessages(ctx, anthropic.MessagesRequest{
			Model:       anthropic.ModelClaudeHaiku4Dot5,
			MaxTokens:   512,
			Temperature: &temp,
			System:      system,
			Messages:    messages,
			Tools:       tools,
		})
		if err != nil {
			return "", fmt.Errorf("claude API: %w", err)
		}

		if resp.StopReason == anthropic.MessagesStopReasonToolUse {
			messages = append(messages, anthropic.Message{Role: anthropic.RoleAssistant, Content: resp.Content})
			var toolResults []anthropic.MessageContent
			for _, c := range resp.Content {
				if c.Type != anthropic.MessagesContentTypeToolUse || c.MessageContentToolUse == nil {
					continue
				}
				result := a.handleSalesTool(ctx, phone, c.MessageContentToolUse.Name, c.MessageContentToolUse.Input)
				toolResults = append(toolResults, anthropic.NewToolResultMessageContent(c.MessageContentToolUse.ID, result, false))
			}
			messages = append(messages, anthropic.Message{Role: anthropic.RoleUser, Content: toolResults})
			continue
		}

		// end_turn / max_tokens: junta o texto e devolve.
		var parts []string
		for _, c := range resp.Content {
			if c.Type == anthropic.MessagesContentTypeText {
				parts = append(parts, c.GetText())
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n")), nil
	}
	// Esgotou iteracoes apos um tool-use sem fechamento textual: fallback seguro.
	return "Pronto! Qualquer coisa, é só me chamar aqui. 😊", nil
}

// handleSalesTool despacha as tools do agente de vendas. Hoje so criar_conta.
func (a *Agent) handleSalesTool(ctx context.Context, phone, name string, input json.RawMessage) string {
	if name != "criar_conta" {
		return fmt.Sprintf("Ferramenta desconhecida: %s", name)
	}
	var args struct {
		Nome string `json:"nome"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "Erro ao ler os dados do cadastro. Peça o nome de novo e tente outra vez."
	}
	return a.provisionLeadAccount(ctx, phone, args.Nome)
}

// provisionLeadAccount cria a conta do lead (type comum, default), marca o lead
// como convertido e envia boas-vindas + magic link do painel. Idempotente: se o
// numero ja tiver conta (corrida, variantes de DDD9), nao duplica. Retorna a
// string que o modelo recebe como resultado da tool (guia o fechamento textual).
func (a *Agent) provisionLeadAccount(ctx context.Context, phone, rawName string) string {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return "Ainda não tenho o nome para o cadastro. Pergunte o nome da pessoa e só então chame criar_conta."
	}

	// Idempotencia: cobre todas as variantes BR (com/sem 9o digito).
	for _, variant := range normalizeBRPhone(phone) {
		if existing, err := a.db.GetUserByPhone(variant); err == nil {
			_ = a.db.MarkLeadConverted(phone)
			log.Printf("[lead %s] account already exists (id=%d) — idempotent", phone, existing.ID)
			return "Esse número já tem uma conta no Zello. Avise que ele pode entrar no painel pelo site (login com o número) — não crie de novo."
		}
	}

	user := &User{PhoneNumber: phone, Name: name}
	if err := a.db.CreateUser(user); err != nil {
		// Corrida: alguem criou entre o check e o insert. Re-checa antes de falhar.
		for _, variant := range normalizeBRPhone(phone) {
			if _, e2 := a.db.GetUserByPhone(variant); e2 == nil {
				_ = a.db.MarkLeadConverted(phone)
				return "Esse número já tem uma conta no Zello. Não crie de novo; avise que pode acessar pelo painel."
			}
		}
		log.Printf("[lead %s] create user: %v", phone, err)
		return "Não consegui concluir o cadastro agora por um erro interno. Peça desculpas e diga que pode tentar de novo em instantes."
	}

	if err := a.db.MarkLeadConverted(phone); err != nil {
		log.Printf("[lead %s] mark converted: %v", phone, err)
	}
	if a.audit != nil {
		_ = a.audit.Log(user.ID, "self_signup_whatsapp", phone, "via=sales_agent")
	}

	if err := a.sendWelcomeWithMagicLink(user); err != nil {
		log.Printf("[lead %s] send welcome: %v", phone, err)
		// Conta JA existe; so o envio do link falhou. O usuario consegue logar
		// pelo site (numero ja cadastrado). Guia o modelo a nao prometer link.
		return fmt.Sprintf("Conta de %s criada com sucesso. O envio do link automático falhou — diga que a conta está pronta e que ela pode acessar o painel pelo site, fazendo login com o número. Seja breve e caloroso.", firstName(name))
	}

	return fmt.Sprintf("Conta de %s criada e a mensagem de boas-vindas com o link do painel JÁ foi enviada. Responda apenas com uma saudação curta e calorosa de boas-vindas — NÃO repita o link nem diga que vai enviar algo.", firstName(name))
}

// sendWelcomeWithMagicLink cria uma sessao pending e envia ao WhatsApp do
// novo usuario a mensagem de boas-vindas com o magic link do painel + nota de
// consentimento (LGPD). Usa o mesmo callback de envio do bot (SendTextToPhone).
func (a *Agent) sendWelcomeWithMagicLink(user *User) error {
	if a.sendMsg == nil {
		return errors.New("sendMsg callback nao configurado")
	}
	_, plaintext, err := a.db.CreatePendingSession(user.ID, "whatsapp-signup", "whatsapp-signup")
	if err != nil {
		return fmt.Errorf("create pending session: %w", err)
	}
	url := strings.TrimRight(resolveWebBaseURL(), "/") + "/auth/verify?token=" + plaintext
	msg := fmt.Sprintf(
		"Pronto, %s! 🎉 Criei sua conta no Zello.\n\n"+
			"Aqui está seu acesso ao painel (vale por 15 minutos):\n%s\n\n"+
			"No painel você cadastra quem quer cuidar, configura os lembretes de "+
			"remédio e acompanha tudo de perto.\n\n"+
			"Ao usar o Zello você concorda com nossos termos e política de "+
			"privacidade (zello.chat). Qualquer dúvida, é só me chamar por aqui. 😊\n— Zello",
		firstName(user.Name), url,
	)
	return a.sendMsg(user.PhoneNumber, msg)
}

// buildLeadMessages converte o historico do lead em mensagens da API Anthropic.
func buildLeadMessages(history []LeadMessage) []anthropic.Message {
	msgs := make([]anthropic.Message, 0, len(history))
	for _, h := range history {
		role := anthropic.RoleUser
		if h.Role == "assistant" {
			role = anthropic.RoleAssistant
		}
		content := h.Content
		msgs = append(msgs, anthropic.Message{
			Role:    role,
			Content: []anthropic.MessageContent{{Type: "text", Text: &content}},
		})
	}
	// Defensivo: a API exige que a conversa comece com role=user. Se por algum
	// motivo o historico abrir com assistant (poda), injeta um placeholder.
	if len(msgs) == 0 || msgs[0].Role != anthropic.RoleUser {
		oi := "Oi"
		msgs = append([]anthropic.Message{{Role: anthropic.RoleUser, Content: []anthropic.MessageContent{{Type: "text", Text: &oi}}}}, msgs...)
	}
	return msgs
}

// salesCreateAccountTool eh a definicao da tool de provisionamento.
func salesCreateAccountTool() anthropic.ToolDefinition {
	return anthropic.ToolDefinition{
		Name:        "criar_conta",
		Description: "Cria a conta do usuário no Zello. Chame SOMENTE depois que a pessoa demonstrar interesse claro em usar a plataforma E você tiver confirmado o nome dela. A conta nasce como titular (quem cuida); depois ela cadastra os dependentes no painel. NUNCA diga que cadastrou antes de chamar esta tool e receber sucesso.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"nome": {"type": "string", "description": "Nome da pessoa, confirmado na conversa. Use o nome do perfil se ela confirmou, ou o que ela informou."}
			},
			"required": ["nome"]
		}`),
	}
}

// buildSalesSystemPrompt monta a persona do agente de vendas. pushName entra
// no prompt para guiar a confirmacao de nome no cadastro.
func buildSalesSystemPrompt(pushName string) string {
	var nameHint string
	if pushName != "" {
		nameHint = fmt.Sprintf(
			"O nome no perfil de WhatsApp da pessoa é \"%s\". Use como palpite: na hora de cadastrar, "+
				"confirme (\"posso criar sua conta como %s?\"). Se ela corrigir ou o palpite não parecer um nome real, pergunte o nome certo antes de chamar criar_conta.",
			pushName, firstName(pushName))
	} else {
		nameHint = "Você não sabe o nome da pessoa. Antes de criar a conta, pergunte como ela se chama."
	}

	return `Você é o Zello, um assistente de cuidado por WhatsApp. Está conversando com alguém que AINDA NÃO tem conta — seu papel é apresentar o Zello e, com simpatia, conduzir essa pessoa ao cadastro. Você é um vendedor caloroso e genuíno, nunca insistente ou robótico.

O que o Zello faz (explique com naturalidade, sem despejar tudo de uma vez):
- Lembra a pessoa querida de tomar os remédios na hora certa, todo dia.
- Faz companhia: conversa, escuta, puxa papo — bom contra a solidão de quem mora só.
- Dá tranquilidade pra família: avisa os responsáveis se algo preocupante acontece, sem invadir a privacidade do idoso.

Como agir:
- Seja breve e humano (WhatsApp): 1 a 3 frases por mensagem, tom acolhedor, português informal.
- Responda dúvidas e objeções com honestidade. Se perguntarem preço/como funciona, explique de forma simples.
- Conduza para a conversão: quando fizer sentido, convide ("quer que eu crie sua conta agora? leva menos de um minuto").
- A conta é da pessoa que CUIDA (o titular). Os idosos/dependentes ela cadastra depois, pelo painel.
- Quando a pessoa demonstrar interesse claro (ex: "quero", "sim", "vamos", "como faço"), confirme o nome e chame a tool criar_conta. Só então a conta existe.
- NUNCA afirme que criou a conta sem ter chamado criar_conta e recebido sucesso. Nada de "já cadastrei" no vácuo.
- Se a pessoa claramente não tem interesse, agradeça com gentileza e não insista.

` + nameHint
}
