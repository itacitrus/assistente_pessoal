package llm

import (
	"context"
	"fmt"

	"github.com/liushuangls/go-anthropic/v2"
)

// AnthropicReport implementa ReportProvider — sintese final pro
// responsavel (Fase 5). Default Sonnet (output sensivel, baixo volume).
type AnthropicReport struct {
	client       *anthropic.Client
	defaultModel string
}

// NewAnthropicReport constroi o provider.
func NewAnthropicReport(apiKey, defaultModel string) *AnthropicReport {
	if defaultModel == "" {
		defaultModel = DefaultAnthropicChatModel
	}
	return &AnthropicReport{
		client:       anthropic.NewClient(apiKey),
		defaultModel: defaultModel,
	}
}

// Name retorna "anthropic".
func (a *AnthropicReport) Name() string { return "anthropic" }

// Synthesize gera um texto livre baseado no UserPrompt + System.
func (a *AnthropicReport) Synthesize(ctx context.Context, req ReportRequest) (ReportResponse, error) {
	model := a.defaultModel
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1500
	}

	sys := make([]anthropic.MessageSystemPart, 0, len(req.System))
	cacheBudgetUsed := false
	for _, p := range req.System {
		part := anthropic.MessageSystemPart{Type: "text", Text: p.Text}
		if p.Cacheable && !cacheBudgetUsed {
			part.CacheControl = &anthropic.MessageCacheControl{
				Type: anthropic.CacheControlTypeEphemeral,
			}
			cacheBudgetUsed = true
		}
		sys = append(sys, part)
	}

	temp := float32(0.5)
	mr := anthropic.MessagesRequest{
		Model:       anthropic.Model(model),
		MaxTokens:   maxTokens,
		MultiSystem: sys,
		Messages: []anthropic.Message{
			{
				Role: anthropic.RoleUser,
				Content: []anthropic.MessageContent{
					anthropic.NewTextMessageContent(req.UserPrompt),
				},
			},
		},
		Temperature: &temp,
	}

	resp, err := a.client.CreateMessages(ctx, mr)
	if err != nil {
		return ReportResponse{}, fmt.Errorf("anthropic report: %w", err)
	}

	return ReportResponse{
		Text:      resp.GetFirstContentText(),
		ModelUsed: model,
		Usage: Usage{
			InputTokens:         resp.Usage.InputTokens,
			OutputTokens:        resp.Usage.OutputTokens,
			CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		},
	}, nil
}
