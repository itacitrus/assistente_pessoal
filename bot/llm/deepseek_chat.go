package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// DefaultDeepSeekBaseURL eh o endpoint padrao da API DeepSeek (compativel
// com OpenAI).
const DefaultDeepSeekBaseURL = "https://api.deepseek.com/v1"

// DefaultDeepSeekModel eh o modelo default — V4-Flash em prod (vide D8).
const DefaultDeepSeekModel = "deepseek-chat"

// DeepSeekChat implementa ChatProvider via API OpenAI-compatible da
// DeepSeek. Traduz nossa Message canonica em ChatCompletionMessage e
// vice-versa. Sem prompt cache (ignora SystemPart.Cacheable).
type DeepSeekChat struct {
	client       *openai.Client
	defaultModel string
}

// NewDeepSeekChat constroi o provider. Se baseURL=="" usa default.
// Se defaultModel=="" usa "deepseek-chat".
func NewDeepSeekChat(apiKey, baseURL, defaultModel string) *DeepSeekChat {
	cfg := openai.DefaultConfig(apiKey)
	if baseURL == "" {
		cfg.BaseURL = DefaultDeepSeekBaseURL
	} else {
		cfg.BaseURL = baseURL
	}
	if defaultModel == "" {
		defaultModel = DefaultDeepSeekModel
	}
	return &DeepSeekChat{
		client:       openai.NewClientWithConfig(cfg),
		defaultModel: defaultModel,
	}
}

// Name retorna "deepseek".
func (d *DeepSeekChat) Name() string { return "deepseek" }

// SupportsTools retorna true — function calling em V4-Flash.
func (d *DeepSeekChat) SupportsTools() bool { return true }

// SupportsVision retorna false — V4-Flash chat nao tem vision em prod.
// Vision precisa cair em VisionProvider separado (Anthropic Haiku).
func (d *DeepSeekChat) SupportsVision() bool { return false }

// Chat traduz ChatRequest → openai.ChatCompletionRequest, chama o
// endpoint, traduz de volta.
//
// System prompt: OpenAI nao tem cache_control. Concatenamos todos os
// SystemPart numa unica role="system" message no inicio. Aceitamos pagar
// full prompt em cada turno (custo D8 ja contabiliza).
//
// Tool calls: choice.Message.ToolCalls vira []ContentBlock{type:"tool_use"}.
// Tool results: nossa Message role=user com block tool_result vira
// {role:"tool", tool_call_id, content} no array.
func (d *DeepSeekChat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := d.defaultModel
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// System: concatena todas as SystemPart (Cacheable e ignorado — DeepSeek
	// nao tem cache).
	var sysText string
	for i, p := range req.System {
		if i > 0 {
			sysText += "\n\n"
		}
		sysText += p.Text
	}

	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages)+1)
	if sysText != "" {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysText,
		})
	}
	for _, m := range req.Messages {
		translated, err := toOpenAIMessage(m)
		if err != nil {
			return ChatResponse{}, fmt.Errorf("translate msg: %w", err)
		}
		msgs = append(msgs, translated...)
	}

	tools := make([]openai.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return ChatResponse{}, fmt.Errorf("tool %s schema: %w", t.Name, err)
			}
		}
		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}

	cr := openai.ChatCompletionRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: float32(req.Temperature),
		Messages:    msgs,
		Tools:       tools,
	}
	resp, err := d.client.CreateChatCompletion(ctx, cr)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("deepseek chat: %w", err)
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, errors.New("deepseek: empty choices")
	}
	choice := resp.Choices[0]

	// tool_calls do OpenAI -> []ContentBlock tool_use.
	blocks := make([]ContentBlock, 0, len(choice.Message.ToolCalls)+1)
	if strings.TrimSpace(choice.Message.Content) != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		blocks = append(blocks, ContentBlock{
			Type:      "tool_use",
			ToolUseID: tc.ID,
			ToolName:  tc.Function.Name,
			ToolInput: json.RawMessage(tc.Function.Arguments),
		})
	}

	return ChatResponse{
		Content:    blocks,
		StopReason: mapOpenAIStop(choice.FinishReason),
		ModelUsed:  model,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}, nil
}

// toOpenAIMessage e o ponto-chave da traducao. Cada turno do nosso formato
// pode virar 1+ mensagens no OpenAI:
//   - role=user com text simples → {role:"user", content:"..."}
//   - role=user com tool_result → {role:"tool", tool_call_id, content}
//   - role=assistant com tool_use → {role:"assistant", tool_calls:[...]}
//
// Quando role=user mistura text + tool_result, emite as tool messages
// PRIMEIRO depois a user — OpenAI exige tool messages logo apos o
// assistant que pediu.
func toOpenAIMessage(m Message) ([]openai.ChatCompletionMessage, error) {
	switch m.Role {
	case RoleUser:
		var texts []string
		var toolResults []openai.ChatCompletionMessage
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				texts = append(texts, b.Text)
			case "tool_result":
				toolResults = append(toolResults, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: b.ToolUseID,
					Content:    b.ToolResult,
				})
			case "image":
				// V4-Flash chat nao suporta vision. Caller deveria usar
				// VisionProvider separado e injetar a descricao como text.
				// Aqui retornamos erro pra falhar rapido, em vez de silenciar.
				return nil, fmt.Errorf("deepseek-chat does not support image content blocks")
			}
		}
		out := toolResults
		if len(texts) > 0 {
			out = append(out, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: joinNonEmpty(texts, "\n"),
			})
		}
		return out, nil
	case RoleAssistant:
		msg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant}
		var texts []string
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				texts = append(texts, b.Text)
			case "tool_use":
				msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
					ID:   b.ToolUseID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      b.ToolName,
						Arguments: string(b.ToolInput),
					},
				})
			}
		}
		msg.Content = joinNonEmpty(texts, "\n")
		return []openai.ChatCompletionMessage{msg}, nil
	}
	return nil, fmt.Errorf("unsupported role %q", m.Role)
}

// mapOpenAIStop normaliza FinishReason → StopReason.
func mapOpenAIStop(r openai.FinishReason) StopReason {
	switch r {
	case openai.FinishReasonToolCalls, openai.FinishReasonFunctionCall:
		return StopToolUse
	case openai.FinishReasonLength:
		return StopMaxTokens
	case openai.FinishReasonStop:
		return StopEndTurn
	case openai.FinishReasonContentFilter:
		return StopError
	default:
		return StopEndTurn
	}
}

// joinNonEmpty concatena strings nao-vazias com sep.
func joinNonEmpty(parts []string, sep string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += sep
		}
		out += p
	}
	return out
}
