package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompatAnalysis implementa AnalysisProvider contra backend
// OpenAI-compatible. Usado pelo snapshot writer e pela sintese.
//
// Diferenca central em relacao ao AnthropicAnalysis: a DeepSeek nao tem
// json_schema estrito, so response_format json_object — que a propria doc
// admite poder devolver content vazio ocasionalmente. Por isso aqui existe
// UMA retentativa com o erro no feedback. Sem ela, uma resposta invalida
// congela a sintese em silencio (incidente ja vivido em prod: painel parado
// sem erro aparente).
type OpenAICompatAnalysis struct {
	chat *OpenAICompatChat
}

func NewOpenAICompatAnalysis(apiKey string, p Profile, model string) *OpenAICompatAnalysis {
	return &OpenAICompatAnalysis{chat: NewOpenAICompatChat(apiKey, p, model)}
}

func (a *OpenAICompatAnalysis) Name() string { return a.chat.Name() }

func (a *OpenAICompatAnalysis) Analyze(ctx context.Context, req AnalysisRequest) (AnalysisResponse, error) {
	model := a.chat.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1500
	}

	var sys strings.Builder
	for _, p := range req.System {
		if sys.Len() > 0 {
			sys.WriteString("\n\n")
		}
		sys.WriteString(p.Text)
	}
	// A doc da DeepSeek exige a palavra "json" no prompt quando
	// response_format=json_object. A instrucao abaixo cobre isso e o schema.
	if sys.Len() > 0 {
		sys.WriteString("\n\n")
	}
	if len(req.SchemaJSON) > 0 {
		fmt.Fprintf(&sys,
			"Responda APENAS um objeto json valido seguindo este schema (sem markdown, sem prefixo, sem comentario):\n%s",
			string(req.SchemaJSON))
	} else {
		sys.WriteString("Responda APENAS um objeto json valido (sem markdown, sem prefixo, sem comentario).")
	}

	attempt := func(extra string) (compatResponse, error) {
		system := sys.String()
		if extra != "" {
			system += "\n\n" + extra
		}
		body := compatRequest{
			Model:       model,
			MaxTokens:   maxTokens,
			Temperature: 0.2, // analise = baixa criatividade
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: system},
				{Role: openai.ChatMessageRoleUser, Content: req.UserPrompt},
			},
			ResponseFormat: map[string]string{"type": "json_object"},
		}
		if a.chat.profile.DisableThinking {
			body.Thinking = &thinkingCfg{Type: "disabled"}
		}
		var resp compatResponse
		err := RetryWithBackoff(ctx, 3, func() error {
			var e error
			resp, e = a.chat.post(ctx, body)
			return e
		})
		return resp, err
	}

	parse := func(resp compatResponse) (json.RawMessage, string, bool) {
		if len(resp.Choices) == 0 {
			return nil, "", false
		}
		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		cleaned := stripJSONFence(raw)
		var sanity any
		if err := json.Unmarshal([]byte(cleaned), &sanity); err != nil {
			return nil, raw, false
		}
		return json.RawMessage(cleaned), raw, true
	}

	resp, err := attempt("")
	if err != nil {
		return AnalysisResponse{}, fmt.Errorf("%s analysis: %w", a.chat.Name(), err)
	}
	out, raw, ok := parse(resp)
	if !ok {
		// Uma retentativa com o problema no feedback. A doc admite content
		// vazio ocasional; sem retry isso vira sintese congelada em silencio.
		resp, err = attempt("A resposta anterior nao era um objeto json valido. Responda somente o objeto json, nada mais.")
		if err != nil {
			return AnalysisResponse{}, fmt.Errorf("%s analysis (retry): %w", a.chat.Name(), err)
		}
		out, raw, ok = parse(resp)
		if !ok {
			return AnalysisResponse{}, fmt.Errorf("%s analysis: output nao e json valido apos retry (raw: %.200s)", a.chat.Name(), raw)
		}
	}

	usedModel := resp.Model
	if usedModel == "" {
		usedModel = model
	}
	return AnalysisResponse{
		JSON:      out,
		ModelUsed: usedModel,
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptCacheHitTokens,
		},
	}, nil
}
