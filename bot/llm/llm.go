// Package llm abstrai os providers de LLM usados pelo bot. Define as
// interfaces ChatProvider, AnalysisProvider, ReportProvider e VisionProvider
// e os tipos canonicos (Message, ContentBlock, ToolDef etc.) que cada
// implementacao traduz pra/da seu SDK.
//
// A motivacao vem da Fase 4 do plano de idosos (decisao D8 do overview):
// estrategia 3-tier de modelos (Sonnet pra operacional, DeepSeek pra
// companion, Haiku pra snapshot+vision). A interface uniformiza o ponto
// de chamada — o Agent passa a depender de ChatProvider, nao de um SDK
// concreto.
//
// Implementacoes:
//   - AnthropicChat / AnthropicAnalysis / AnthropicReport / AnthropicVision
//     (anthropic_*.go) — wrappers do SDK liushuangls/go-anthropic/v2.
//   - DeepSeekChat (deepseek_chat.go) — usa github.com/sashabaranov/go-openai
//     apontado pra https://api.deepseek.com/v1.
//
// Preserva prompt cache do Anthropic via SystemPart.Cacheable. Providers
// sem cache (DeepSeek/OpenAI) ignoram a flag — concatenam tudo numa
// system message no inicio do array.
package llm

import (
	"context"
	"encoding/json"
)

// Role identifica o autor de uma mensagem dentro do historico.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentBlock e a unidade minima de uma mensagem. O conteudo pode ser
// texto puro, um pedido de tool_use do modelo (assistant), ou um
// tool_result devolvendo dados ao modelo (user).
type ContentBlock struct {
	Type       string          `json:"type"` // "text" | "tool_use" | "tool_result" | "image"
	Text       string          `json:"text,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`
	ToolResult string          `json:"tool_result,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	// Para imagem inline (vision em chat). Sempre base64 (sem prefixo data:).
	ImageMedia string `json:"image_media,omitempty"` // "image/jpeg" | "image/png"
	ImageData  string `json:"image_data,omitempty"`  // base64
}

// Message e um turno completo do dialogo. Suporta multiplos blocks
// (ex: assistant pode emitir texto + tool_use no mesmo turno).
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// SystemPart permite system prompt fragmentado, cada parte podendo
// solicitar cache (Anthropic). Provider sem cache concatena tudo.
type SystemPart struct {
	Text      string `json:"text"`
	Cacheable bool   `json:"cacheable,omitempty"`
}

// ToolDef e a definicao de uma tool exposta ao modelo. Schema sempre
// JSON Schema — providers fazem traducao se precisar (Anthropic usa
// "input_schema", OpenAI usa "parameters" dentro de "function").
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Usage e contagem de tokens. Cada provider preenche o que sabe;
// campos podem vir 0 quando o backend nao reporta.
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
}

// StopReason normaliza fim do turno entre providers.
//
//	"end_turn"   — modelo terminou de falar
//	"tool_use"   — modelo pediu tool
//	"max_tokens" — bateu o limite
//	"error"      — backend abortou
type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopError     StopReason = "error"
)

// ChatRequest descreve uma chamada de chat conversacional com tools.
// Modelo pode ser sobrescrito por chamada (ex: forcar Haiku numa fase
// de debug); default e o configurado no provider.
type ChatRequest struct {
	System        []SystemPart
	Messages      []Message
	Tools         []ToolDef
	MaxTokens     int
	Temperature   float64 // 0 = default do provider
	ModelOverride string  // opcional
}

// ChatResponse e o retorno de um turno do modelo.
type ChatResponse struct {
	Content    []ContentBlock // text + tool_use blocks
	StopReason StopReason
	Usage      Usage
	ModelUsed  string // resolved (apos override)
}

// AnalysisRequest — analise estruturada (snapshot writer, safety review).
// Sem tools; output JSON estrito esperado.
type AnalysisRequest struct {
	System        []SystemPart
	UserPrompt    string
	SchemaName    string          // p/ logging — ex: "psych_state_v1"
	SchemaJSON    json.RawMessage // JSON Schema do output esperado
	MaxTokens     int
	ModelOverride string
}

// AnalysisResponse e o retorno de uma analise estruturada.
type AnalysisResponse struct {
	JSON      json.RawMessage
	Usage     Usage
	ModelUsed string
}

// ReportRequest — sintese final pro responsavel. Sem tools, prompt
// elaborado, output em texto livre (ou markdown leve).
type ReportRequest struct {
	System        []SystemPart
	UserPrompt    string
	MaxTokens     int
	ModelOverride string
}

// ReportResponse retorna o texto sintetizado.
type ReportResponse struct {
	Text      string
	Usage     Usage
	ModelUsed string
}

// VisionRequest — descricao de imagem com prompt customizavel.
type VisionRequest struct {
	System        []SystemPart
	Prompt        string // pergunta sobre a imagem
	ImageMedia    string // "image/jpeg" | "image/png" | "image/webp" | "image/gif"
	ImageData     string // base64 raw (sem prefixo data:)
	MaxTokens     int
	ModelOverride string
}

// VisionResponse retorna a descricao em texto.
type VisionResponse struct {
	Text      string
	Usage     Usage
	ModelUsed string
}

// --- Interfaces ---

// ChatProvider — chat conversacional com tool use e system prompt.
type ChatProvider interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Name() string
	SupportsTools() bool
	SupportsVision() bool // se false, vision em chat tem que cair em VisionProvider
}

// AnalysisProvider — analise estruturada com output JSON.
type AnalysisProvider interface {
	Analyze(ctx context.Context, req AnalysisRequest) (AnalysisResponse, error)
	Name() string
}

// ReportProvider — sintese de texto longa (Sonnet em geral).
type ReportProvider interface {
	Synthesize(ctx context.Context, req ReportRequest) (ReportResponse, error)
	Name() string
}

// VisionProvider — descricao de imagem.
type VisionProvider interface {
	DescribeImage(ctx context.Context, req VisionRequest) (VisionResponse, error)
	Name() string
}
