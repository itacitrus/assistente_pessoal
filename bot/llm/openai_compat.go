package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// DefaultDeepSeekBaseURL eh o endpoint da API DeepSeek (compativel com OpenAI).
// Mantido como const publica por compatibilidade com callers antigos.
const DefaultDeepSeekBaseURL = "https://api.deepseek.com"

// DefaultDeepSeekModel eh o modelo default.
//
// ATENCAO: deepseek-chat e deepseek-reasoner foram DESCONTINUADOS em
// 24/07/2026. Ainda respondem como alias nao-documentado, mas sumiram do
// /models e da pagina de precos. O destino correto e deepseek-v4-flash.
const DefaultDeepSeekModel = "deepseek-v4-flash"

// requestTimeout: o servidor da DeepSeek mantem a conexao em keep-alive por
// ate 10 minutos antes de desistir, e o http.Client default nao tem deadline
// nenhum. Sem timeout explicito um turno pode ficar pendurado indefinidamente.
const requestTimeout = 120 * time.Second

// OpenAICompatChat implementa ChatProvider contra qualquer backend compativel
// com a API da OpenAI (DeepSeek, Gemini). Traduz nossa Message canonica em
// ChatCompletionMessage e vice-versa.
//
// Nao usa client.CreateChatCompletion do go-openai porque precisamos de dois
// campos que o SDK nao cobre:
//   - "thinking" no request (proprietario da DeepSeek — sem ele o V4 raciocina
//     por default e gasta o max_tokens antes de responder);
//   - prompt_cache_hit_tokens/miss na resposta (o SDK descarta, e sao eles que
//     provam se a economia de cache esta acontecendo).
type OpenAICompatChat struct {
	profile Profile
	model   string
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewOpenAICompatChat constroi o provider a partir de um Profile.
// modelOverride vazio usa o DefaultModel do perfil.
func NewOpenAICompatChat(apiKey string, p Profile, modelOverride string) *OpenAICompatChat {
	model := modelOverride
	if model == "" {
		model = p.DefaultModel
	}
	return &OpenAICompatChat{
		profile: p,
		model:   model,
		apiKey:  apiKey,
		baseURL: p.BaseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// NewDeepSeekChat mantem a assinatura antiga pra nao quebrar callers e testes
// existentes. baseURL vazio usa o do perfil; defaultModel vazio usa
// deepseek-v4-flash (nao mais o aposentado deepseek-chat).
func NewDeepSeekChat(apiKey, baseURL, defaultModel string) *OpenAICompatChat {
	p := ProfileDeepSeek()
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	return NewOpenAICompatChat(apiKey, p, defaultModel)
}

// Name retorna o nome do perfil ("deepseek" | "gemini").
func (d *OpenAICompatChat) Name() string { return d.profile.Name }

// SupportsTools retorna true — function calling validado nos dois perfis.
func (d *OpenAICompatChat) SupportsTools() bool { return true }

// SupportsVision depende do perfil: o modelo de chat da DeepSeek recusa
// imagem com HTTP 400; o de visao e o Gemini aceitam.
func (d *OpenAICompatChat) SupportsVision() bool { return d.profile.SupportsVisionInChat }

// Model expoe o modelo resolvido (usado em log e contabilidade).
func (d *OpenAICompatChat) Model() string { return d.model }

// --- Tipos do protocolo ---

type thinkingCfg struct {
	Type string `json:"type"`
}

type compatRequest struct {
	Model       string                         `json:"model"`
	Messages    []openai.ChatCompletionMessage `json:"messages"`
	Tools       []openai.Tool                  `json:"tools,omitempty"`
	MaxTokens   int                            `json:"max_tokens,omitempty"`
	Temperature float32                        `json:"temperature,omitempty"`
	Thinking    *thinkingCfg                   `json:"thinking,omitempty"`
	// ResponseFormat e usado pelo AnalysisProvider (json_object).
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type compatUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

type compatChoice struct {
	Message struct {
		Content   string            `json:"content"`
		ToolCalls []openai.ToolCall `json:"tool_calls"`
		// ReasoningContent so aparece com thinking ligado. Nao reenviamos:
		// o teste contra a API real mostrou que omitir NAO causa o HTTP 400
		// que a doc sugere, e o perfil desliga thinking de qualquer forma.
		ReasoningContent string `json:"reasoning_content"`
	} `json:"message"`
	FinishReason openai.FinishReason `json:"finish_reason"`
}

type compatResponse struct {
	Model   string         `json:"model"`
	Choices []compatChoice `json:"choices"`
	Usage   compatUsage    `json:"usage"`
}

// post faz a chamada HTTP com o corpo proprietario e classifica o erro em
// openai.APIError, pra IsRetryable/IsAuthOrBalance conseguirem decidir.
func (d *OpenAICompatChat) post(ctx context.Context, body any) (compatResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return compatResponse{}, err
	}
	url := strings.TrimSuffix(d.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return compatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)

	res, err := d.http.Do(req)
	if err != nil {
		return compatResponse{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	if res.StatusCode >= 400 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return compatResponse{}, &openai.APIError{
			HTTPStatusCode: res.StatusCode,
			Message:        msg,
			Type:           e.Error.Type,
		}
	}
	var out compatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return compatResponse{}, fmt.Errorf("resposta invalida: %w (raw: %.200s)", err, raw)
	}
	return out, nil
}

// Chat traduz ChatRequest → corpo OpenAI-compatible, chama o endpoint com
// retry, e traduz de volta.
//
// System prompt: nao ha cache_control. Concatenamos todas as SystemPart numa
// unica role="system" no inicio. A flag Cacheable e ignorada — a DeepSeek
// cacheia por prefixo automaticamente, sem knob.
func (d *OpenAICompatChat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	model := d.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	var sysText strings.Builder
	for i, p := range req.System {
		if i > 0 {
			sysText.WriteString("\n\n")
		}
		sysText.WriteString(p.Text)
	}

	msgs := make([]openai.ChatCompletionMessage, 0, len(req.Messages)+1)
	if sysText.Len() > 0 {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysText.String(),
		})
	}
	for _, m := range req.Messages {
		translated, err := toOpenAIMessage(m, d.profile)
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
				Name: t.Name, Description: t.Description, Parameters: schema,
			},
		})
	}

	cr := compatRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: float32(req.Temperature),
		Messages:    msgs,
		Tools:       tools,
	}
	if d.profile.DisableThinking {
		cr.Thinking = &thinkingCfg{Type: "disabled"}
	}

	var resp compatResponse
	err := RetryWithBackoff(ctx, 3, func() error {
		var e error
		resp, e = d.post(ctx, cr)
		return e
	})
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%s chat: %w", d.profile.Name, err)
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, errors.New(d.profile.Name + ": empty choices")
	}
	choice := resp.Choices[0]

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

	usedModel := resp.Model
	if usedModel == "" {
		usedModel = model
	}
	return ChatResponse{
		Content:    blocks,
		StopReason: mapOpenAIStop(choice.FinishReason),
		ModelUsed:  usedModel,
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptCacheHitTokens,
		},
	}, nil
}

// toOpenAIMessage e o ponto-chave da traducao. Cada turno do nosso formato
// pode virar 1+ mensagens no OpenAI:
//   - role=user com text simples → {role:"user", content:"..."}
//   - role=user com tool_result → {role:"tool", tool_call_id, content}
//   - role=assistant com tool_use → {role:"assistant", tool_calls:[...]}
//
// Quando role=user mistura text + tool_result, emite as tool messages PRIMEIRO
// e depois a user — OpenAI exige tool messages logo apos o assistant que pediu.
func toOpenAIMessage(m Message, p Profile) ([]openai.ChatCompletionMessage, error) {
	switch m.Role {
	case RoleUser:
		var texts []string
		var parts []openai.ChatMessagePart
		var toolResults []openai.ChatCompletionMessage
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				texts = append(texts, b.Text)
			case "tool_result":
				content := b.ToolResult
				// OpenAI/DeepSeek nao tem flag is_error. Sem o prefixo o modelo
				// nao distingue "a ferramenta explodiu" de "a ferramenta
				// retornou este texto" — e a regra dura de nunca narrar sucesso
				// sem confirmacao da ferramenta depende dessa distincao.
				if b.IsError {
					content = "[ERRO] " + content
				}
				toolResults = append(toolResults, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: b.ToolUseID,
					Content:    content,
				})
			case "image":
				if !p.SupportsVisionInChat {
					// Falha rapido em vez de silenciar a imagem: o caller
					// deveria ter usado o VisionProvider e injetado a descricao
					// como texto.
					return nil, fmt.Errorf("provider %q nao suporta bloco de imagem", p.Name)
				}
				media := b.ImageMedia
				if media == "" {
					media = "image/jpeg"
				}
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL: "data:" + media + ";base64," + b.ImageData,
					},
				})
			}
		}
		out := toolResults
		switch {
		case len(parts) > 0:
			// Multimodal: texto vira part junto das imagens.
			all := make([]openai.ChatMessagePart, 0, len(parts)+1)
			if t := joinNonEmpty(texts, "\n"); t != "" {
				all = append(all, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText, Text: t,
				})
			}
			all = append(all, parts...)
			out = append(out, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleUser, MultiContent: all,
			})
		case len(texts) > 0:
			out = append(out, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleUser, Content: joinNonEmpty(texts, "\n"),
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
						Name: b.ToolName, Arguments: string(b.ToolInput),
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
