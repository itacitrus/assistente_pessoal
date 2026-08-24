package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompatVision implementa VisionProvider contra backend
// OpenAI-compatible.
//
// No perfil DeepSeek isto aponta pro deepseek-v4-flash-vision-exp, que e
// EXPERIMENTAL: teto de 384 tokens por imagem, sem modo OCR, sem PDF. Por
// isso o contrato do sistema e que a visao EXTRAI TEXTO e nunca decide o
// medicamento — quem decide e o drug_catalog (CMED + matching fonetico).
// Leitura ruim vira "nao identifiquei", nunca dose errada persistida.
type OpenAICompatVision struct {
	chat *OpenAICompatChat
}

func NewOpenAICompatVision(apiKey string, p Profile, model string) *OpenAICompatVision {
	return &OpenAICompatVision{chat: NewOpenAICompatChat(apiKey, p, model)}
}

func (v *OpenAICompatVision) Name() string { return v.chat.Name() }

func (v *OpenAICompatVision) DescribeImage(ctx context.Context, req VisionRequest) (VisionResponse, error) {
	model := v.chat.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 500
	}
	media := req.ImageMedia
	if media == "" {
		media = "image/jpeg"
	}

	var sys strings.Builder
	for _, p := range req.System {
		if sys.Len() > 0 {
			sys.WriteString("\n\n")
		}
		sys.WriteString(p.Text)
	}

	parts := []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeText, Text: req.Prompt},
		{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL: "data:" + media + ";base64," + req.ImageData,
			},
		},
	}

	msgs := make([]openai.ChatCompletionMessage, 0, 2)
	if sys.Len() > 0 {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role: openai.ChatMessageRoleSystem, Content: sys.String(),
		})
	}
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser, MultiContent: parts,
	})

	body := compatRequest{
		Model: model, MaxTokens: maxTokens, Temperature: 0.3, Messages: msgs,
	}
	// O modelo de visao da DeepSeek raciocina por default como os demais V4.
	if v.chat.profile.DisableThinking {
		body.Thinking = &thinkingCfg{Type: "disabled"}
	}

	var resp compatResponse
	err := RetryWithBackoff(ctx, 2, func() error {
		var e error
		resp, e = v.chat.post(ctx, body)
		return e
	})
	if err != nil {
		return VisionResponse{}, fmt.Errorf("%s vision: %w", v.chat.Name(), err)
	}
	if len(resp.Choices) == 0 {
		return VisionResponse{}, errors.New(v.chat.Name() + " vision: empty choices")
	}

	usedModel := resp.Model
	if usedModel == "" {
		usedModel = model
	}
	return VisionResponse{
		Text:      strings.TrimSpace(resp.Choices[0].Message.Content),
		ModelUsed: usedModel,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}, nil
}
