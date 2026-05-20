package llm

import (
	"context"
	"fmt"

	"github.com/liushuangls/go-anthropic/v2"
)

// AnthropicVision implementa VisionProvider via Haiku 4.5. Recebe uma
// imagem em base64 + um prompt e devolve descricao em texto.
type AnthropicVision struct {
	client       *anthropic.Client
	defaultModel string
}

// NewAnthropicVision constroi o provider. Default Haiku 4.5 (vide D8).
func NewAnthropicVision(apiKey, defaultModel string) *AnthropicVision {
	if defaultModel == "" {
		defaultModel = DefaultAnthropicHaikuModel
	}
	return &AnthropicVision{
		client:       anthropic.NewClient(apiKey),
		defaultModel: defaultModel,
	}
}

// Name retorna "anthropic".
func (a *AnthropicVision) Name() string { return "anthropic" }

// DescribeImage manda a imagem ao Haiku junto com Prompt e devolve o
// texto da resposta.
func (a *AnthropicVision) DescribeImage(ctx context.Context, req VisionRequest) (VisionResponse, error) {
	if req.ImageData == "" || req.ImageMedia == "" {
		return VisionResponse{}, fmt.Errorf("vision: image data + media type required")
	}
	model := a.defaultModel
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 400
	}

	sys := make([]anthropic.MessageSystemPart, 0, len(req.System))
	for _, p := range req.System {
		sys = append(sys, anthropic.MessageSystemPart{Type: "text", Text: p.Text})
	}

	src := anthropic.MessageContentSource{
		Type:      anthropic.MessagesContentSourceTypeBase64,
		MediaType: req.ImageMedia,
		Data:      req.ImageData,
	}
	imgContent := anthropic.NewImageMessageContent(src)
	textContent := anthropic.NewTextMessageContent(req.Prompt)

	temp := float32(0.3)
	mr := anthropic.MessagesRequest{
		Model:       anthropic.Model(model),
		MaxTokens:   maxTokens,
		MultiSystem: sys,
		Messages: []anthropic.Message{
			{
				Role:    anthropic.RoleUser,
				Content: []anthropic.MessageContent{imgContent, textContent},
			},
		},
		Temperature: &temp,
	}

	resp, err := a.client.CreateMessages(ctx, mr)
	if err != nil {
		return VisionResponse{}, fmt.Errorf("anthropic vision: %w", err)
	}

	return VisionResponse{
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
