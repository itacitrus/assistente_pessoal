package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liushuangls/go-anthropic/v2"
)

// AnthropicAnalysis implementa AnalysisProvider via Anthropic. Usado pelo
// snapshot writer (Fase 4/5) — espera output JSON estrito.
type AnthropicAnalysis struct {
	client       *anthropic.Client
	defaultModel string
}

// NewAnthropicAnalysis constroi o provider. Default = Haiku 4.5 (vide D8).
func NewAnthropicAnalysis(apiKey, defaultModel string) *AnthropicAnalysis {
	if defaultModel == "" {
		defaultModel = DefaultAnthropicHaikuModel
	}
	return &AnthropicAnalysis{
		client:       anthropic.NewClient(apiKey),
		defaultModel: defaultModel,
	}
}

// Name retorna "anthropic".
func (a *AnthropicAnalysis) Name() string { return "anthropic" }

// Analyze pede ao modelo um JSON conforme SchemaJSON e devolve em
// AnalysisResponse.JSON. Se o modelo retornar texto com markdown (```json
// ...```), a funcao limpa antes de devolver.
func (a *AnthropicAnalysis) Analyze(ctx context.Context, req AnalysisRequest) (AnalysisResponse, error) {
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
	// Inject schema na ultima system part — instrucao "responda APENAS JSON".
	if len(req.SchemaJSON) > 0 {
		sys = append(sys, anthropic.MessageSystemPart{
			Type: "text",
			Text: fmt.Sprintf(
				"Responda APENAS um objeto JSON valido seguindo este schema (sem markdown, sem prefixo, sem comentario):\n%s",
				string(req.SchemaJSON)),
		})
	}

	temp := float32(0.2) // analise = baixa criatividade
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
		return AnalysisResponse{}, fmt.Errorf("anthropic analysis: %w", err)
	}

	text := strings.TrimSpace(resp.GetFirstContentText())
	cleaned := stripJSONFence(text)
	// Validacao light: tem que parsear como JSON. Erros sao reportados ao caller
	// — caller pode logar e seguir, ou retentar.
	var sanity any
	if err := json.Unmarshal([]byte(cleaned), &sanity); err != nil {
		return AnalysisResponse{}, fmt.Errorf("analysis output not valid JSON: %w (raw: %.200s)", err, text)
	}

	return AnalysisResponse{
		JSON:      json.RawMessage(cleaned),
		ModelUsed: model,
		Usage: Usage{
			InputTokens:         resp.Usage.InputTokens,
			OutputTokens:        resp.Usage.OutputTokens,
			CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		},
	}, nil
}

// stripJSONFence remove markdown fences de codigo se o modelo emitir
// "```json...```" em vez de JSON puro. Tolerante a JSON sem fence.
func stripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Pula a primeira linha (```json ou ```) e a ultima ```
		idx := strings.Index(s, "\n")
		if idx >= 0 {
			s = s[idx+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
