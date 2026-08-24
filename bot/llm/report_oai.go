package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompatReport implementa ReportProvider contra backend
// OpenAI-compatible. Sintese em texto livre pro responsavel — sem tools,
// sem JSON estrito.
type OpenAICompatReport struct {
	chat *OpenAICompatChat
}

func NewOpenAICompatReport(apiKey string, p Profile, model string) *OpenAICompatReport {
	return &OpenAICompatReport{chat: NewOpenAICompatChat(apiKey, p, model)}
}

func (r *OpenAICompatReport) Name() string { return r.chat.Name() }

func (r *OpenAICompatReport) Synthesize(ctx context.Context, req ReportRequest) (ReportResponse, error) {
	model := r.chat.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	var sys strings.Builder
	for _, p := range req.System {
		if sys.Len() > 0 {
			sys.WriteString("\n\n")
		}
		sys.WriteString(p.Text)
	}

	msgs := make([]openai.ChatCompletionMessage, 0, 2)
	if sys.Len() > 0 {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem, Content: sys.String(),
		})
	}
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser, Content: req.UserPrompt,
	})

	body := compatRequest{
		Model: model, MaxTokens: maxTokens, Temperature: 0.4, Messages: msgs,
	}
	if r.chat.profile.DisableThinking {
		body.Thinking = &thinkingCfg{Type: "disabled"}
	}

	var resp compatResponse
	err := RetryWithBackoff(ctx, 3, func() error {
		var e error
		resp, e = r.chat.post(ctx, body)
		return e
	})
	if err != nil {
		return ReportResponse{}, fmt.Errorf("%s report: %w", r.chat.Name(), err)
	}
	if len(resp.Choices) == 0 {
		return ReportResponse{}, errors.New(r.chat.Name() + " report: empty choices")
	}

	usedModel := resp.Model
	if usedModel == "" {
		usedModel = model
	}
	return ReportResponse{
		Text:      strings.TrimSpace(resp.Choices[0].Message.Content),
		ModelUsed: usedModel,
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptCacheHitTokens,
		},
	}, nil
}
