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

func BuildIntentPrompt(userName, message string) string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	return fmt.Sprintf(`Voce e um assistente de agenda. Analise a mensagem do usuario %s e retorne APENAS um JSON valido.

Data/hora atual: %s

Intencoes possiveis:
- criar_evento: extraia title, date (YYYY-MM-DD), time (HH:MM), duration_minutes (default: 60), location (se mencionado). Se o usuario mencionar a agenda de outra pessoa, extraia target_user com o nome.
- consultar_agenda: extraia start_date (YYYY-MM-DD), end_date (YYYY-MM-DD)
- editar_evento: extraia search_query (texto para encontrar o evento), changes (objeto com campos a alterar)
- cancelar_evento: extraia search_query
- confirmar: o usuario esta confirmando uma acao pendente
- negar: o usuario esta negando uma acao pendente
- consultar_log: o usuario quer ver o historico de acoes. Extraia start_date, end_date.

Responda APENAS com JSON, sem markdown, sem explicacao:
{"intent": "...", "data": {...}, "confirmation_message": "mensagem amigavel para o usuario em portugues"}

Mensagem do usuario: %s`, userName, now, message)
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

func (c *ClaudeClient) ExtractIntent(ctx context.Context, userName, message string) (*IntentResult, error) {
	prompt := BuildIntentPrompt(userName, message)

	resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaudeHaiku4Dot5,
		MaxTokens: 1024,
		Messages: []anthropic.Message{
			{
				Role:    anthropic.RoleUser,
				Content: []anthropic.MessageContent{{Type: "text", Text: &prompt}},
			},
		},
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
