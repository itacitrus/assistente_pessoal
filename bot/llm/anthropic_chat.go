package llm

import (
	"context"
	"fmt"

	"github.com/liushuangls/go-anthropic/v2"
)

// DefaultAnthropicChatModel eh o modelo default para o Charles Lurch
// operacional. Pode ser sobrescrito pela construcao do AnthropicChat.
const DefaultAnthropicChatModel = string(anthropic.ModelClaudeSonnet4Dot6)

// DefaultAnthropicHaikuModel eh o default pra snapshot writer e vision.
const DefaultAnthropicHaikuModel = string(anthropic.ModelClaudeHaiku4Dot5)

// AnthropicChat implementa ChatProvider chamando o SDK
// liushuangls/go-anthropic/v2. Mantem prompt cache (cache_control:
// ephemeral) na primeira parte cacheable do system + na ultima content
// block dos messages quando o caller pedir (via ContentBlock vazio com
// flag — para simplicidade, o cache de mensagens fica do lado do caller).
type AnthropicChat struct {
	client       *anthropic.Client
	defaultModel string
}

// NewAnthropicChat constroi um chat provider Anthropic.
// Se defaultModel for vazio, usa DefaultAnthropicChatModel.
func NewAnthropicChat(apiKey, defaultModel string) *AnthropicChat {
	if defaultModel == "" {
		defaultModel = DefaultAnthropicChatModel
	}
	return &AnthropicChat{
		client:       anthropic.NewClient(apiKey),
		defaultModel: defaultModel,
	}
}

// Name retorna "anthropic" — usado em logs e tests pra identificar provider.
func (a *AnthropicChat) Name() string { return "anthropic" }

// SupportsTools retorna true — Anthropic suporta tool use desde v3.5.
func (a *AnthropicChat) SupportsTools() bool { return true }

// SupportsVision retorna true — Anthropic suporta image blocks em
// content[].
func (a *AnthropicChat) SupportsVision() bool { return true }

// Chat traduz nossa request canonica em anthropic.MessagesRequest, chama o
// SDK e re-traduz o response. Mantem prompt cache via cache_control:
// ephemeral nas system parts marcadas Cacheable.
func (a *AnthropicChat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := a.defaultModel
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// System parts: primeiro Cacheable=true ganha CacheControl.
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

	msgs := make([]anthropic.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		am, err := toAnthropicMessage(m)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("translate message: %w", err)
		}
		msgs = append(msgs, am)
	}

	tools := make([]anthropic.ToolDefinition, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, anthropic.ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	mr := anthropic.MessagesRequest{
		Model:       anthropic.Model(model),
		MaxTokens:   maxTokens,
		MultiSystem: sys,
		Messages:    msgs,
		Tools:       tools,
	}
	if req.Temperature != 0 {
		t := float32(req.Temperature)
		mr.Temperature = &t
	}

	resp, err := a.client.CreateMessages(ctx, mr)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic chat: %w", err)
	}

	out := ChatResponse{
		Content:    fromAnthropicContent(resp.Content),
		ModelUsed:  model,
		StopReason: mapAnthropicStop(resp.StopReason),
		Usage: Usage{
			InputTokens:         resp.Usage.InputTokens,
			OutputTokens:        resp.Usage.OutputTokens,
			CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		},
	}
	return out, nil
}
