package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

type IntentResult struct {
	Intent              string     `json:"intent"`
	Data                IntentData `json:"data"`
	ConfirmationMessage string     `json:"confirmation_message"`
}

type IntentData struct {
	Title           string `json:"title,omitempty"`
	Date            string `json:"date,omitempty"`
	Time            string `json:"time,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	Location        string `json:"location,omitempty"`
	TargetUser      string `json:"target_user,omitempty"`

	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	SearchQuery string          `json:"search_query,omitempty"`
	Changes     json.RawMessage `json:"changes,omitempty"`
}

type ClaudeClient struct {
	client *anthropic.Client
}

func NewClaudeClient(apiKey string) *ClaudeClient {
	return &ClaudeClient{
		client: anthropic.NewClient(apiKey),
	}
}

func BuildIntentPrompt(userName, _ string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e um assistente de agenda do usuario %s. Analise as mensagens e retorne APENAS um JSON valido.

Data/hora atual: %s

Voce tem acesso ao historico recente da conversa. Use-o para entender o contexto — por exemplo, se o usuario fez um pedido antes e agora esta confirmando ou perguntando sobre ele.

Intencoes possiveis:
- criar_evento: extraia title, date (YYYY-MM-DD), time (HH:MM), duration_minutes (default: 60), location (se mencionado). Se o usuario mencionar a agenda de outra pessoa, extraia target_user com o nome.
- consultar_agenda: extraia start_date (YYYY-MM-DD), end_date (YYYY-MM-DD)
- editar_evento: extraia search_query (texto para encontrar o evento), changes (objeto com campos a alterar)
- cancelar_evento: extraia search_query
- confirmar: o usuario esta confirmando uma acao pendente
- negar: o usuario esta negando uma acao pendente
- consultar_log: o usuario quer ver o historico de acoes. Extraia start_date, end_date.

Responda APENAS com JSON, sem markdown, sem explicacao:
{"intent": "...", "data": {...}, "confirmation_message": "mensagem amigavel para o usuario em portugues"}`, userName, now)
}

func ParseIntentResponse(raw []byte) (*IntentResult, error) {
	s := strings.TrimSpace(string(raw))
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var result IntentResult
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, fmt.Errorf("parse intent JSON: %w (raw: %s)", err, s)
	}
	return &result, nil
}

func (c *ClaudeClient) ExtractIntent(ctx context.Context, userName, message string, history []ConversationMessage) (*IntentResult, error) {
	systemPrompt := BuildIntentPrompt(userName, "")
	// Use system prompt for instructions, conversation history as messages
	var messages []anthropic.Message

	// Add conversation history as prior messages
	for _, h := range history {
		role := anthropic.RoleUser
		if h.Role == "assistant" {
			role = anthropic.RoleAssistant
		}
		content := h.Content
		messages = append(messages, anthropic.Message{
			Role:    role,
			Content: []anthropic.MessageContent{{Type: "text", Text: &content}},
		})
	}

	// Add current message
	messages = append(messages, anthropic.Message{
		Role:    anthropic.RoleUser,
		Content: []anthropic.MessageContent{{Type: "text", Text: &message}},
	})

	resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaudeHaiku4Dot5,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  messages,
	})
	if err != nil {
		return nil, fmt.Errorf("claude API: %w", err)
	}

	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("claude returned empty response")
	}

	text := resp.Content[0].GetText()
	return ParseIntentResponse([]byte(text))
}
