# Migração Anthropic → DeepSeek — Plano de Implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrar todos os papéis de LLM do bot de Anthropic para DeepSeek, com roteamento por env var, nove defesas de protocolo e instrumentação de custo, terminando com deploy em produção.

**Architecture:** O pacote `bot/llm` já define as interfaces certas (`ChatProvider`, `AnalysisProvider`, `ReportProvider`, `VisionProvider`) e o `runLoopLLM` em `agent_companion.go` já é um loop de tool-use completo sobre elas. A migração **generaliza** o `deepseek_chat.go` em um provider OpenAI-compatible com perfis (DeepSeek/Gemini), e faz `Run()`/`RunProactive()` usarem o loop da abstração em vez do SDK Anthropic direto. A Anthropic continua compilando atrás de env var.

**Tech Stack:** Go 1.x · `github.com/sashabaranov/go-openai v1.41.2` · `github.com/liushuangls/go-anthropic/v2 v2.18.0` (mantido) · SQLite

**Spec:** `docs/superpowers/specs/2026-08-24-migracao-deepseek-design.md` (commit `e184c11`)

## Global Constraints

- **Modelo default DeepSeek:** `deepseek-v4-flash`. NUNCA usar `deepseek-chat` ou `deepseek-reasoner` (descontinuados 24/07/2026).
- **Base URL DeepSeek:** `https://api.deepseek.com` (raiz, **sem** `/v1` — o `/v1` atual é legado).
- **Thinking:** desligado explicitamente no perfil DeepSeek. Com thinking ligado o `max_tokens` é consumido pelo raciocínio e `content` volta vazio.
- **Nenhum teste pode fazer chamada real de API.** A suíte roda hoje com `ANTHROPIC_API_KEY`/`DEEPSEEK_API_KEY` ausentes e passa. Manter.
- **Baseline obrigatório:** `go test ./...` = 731 testes, 4 pacotes, todos `ok`. Nenhuma task pode deixar a suíte vermelha.
- **Testes que quebram por tipo `anthropic.*` são MIGRADOS, nunca deletados.**
- **Idioma do código:** comentários em português sem acento (padrão do repo).
- **Commits:** frequentes, um por task, mensagem em português.

---

### Task 1: Provider OpenAI-compatible unificado + perfis

**Files:**
- Create: `bot/llm/profiles.go`
- Create: `bot/llm/retry.go`
- Modify: `bot/llm/deepseek_chat.go` → renomear para `bot/llm/openai_compat.go`, generalizar
- Test: `bot/llm/openai_compat_test.go`, `bot/llm/retry_test.go`

**Interfaces:**
- Consumes: `ChatProvider`, `ChatRequest`, `ChatResponse`, `Message`, `ContentBlock` (de `llm.go`, inalterados)
- Produces:
  - `type Profile struct { Name, BaseURL, DefaultModel string; DisableThinking bool; SupportsVisionInChat bool }`
  - `func ProfileDeepSeek() Profile` · `func ProfileGemini() Profile` · `func ProfileByName(name string) (Profile, bool)`
  - `func NewOpenAICompatChat(apiKey string, p Profile, modelOverride string) *OpenAICompatChat`
  - `func NewDeepSeekChat(apiKey, baseURL, defaultModel string) *OpenAICompatChat` — **shim de compatibilidade**, mantém os testes atuais compilando
  - `func IsRetryable(err error) bool` · `func IsAuthOrBalance(err error) bool`
  - `func RetryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error`

- [ ] **Step 1: Escrever os testes que falham**

Em `bot/llm/openai_compat_test.go`:

```go
// Perfil DeepSeek nao pode apontar pros modelos aposentados nem pro /v1 legado.
func TestProfileDeepSeekDefaults(t *testing.T) {
	p := ProfileDeepSeek()
	if p.DefaultModel != "deepseek-v4-flash" {
		t.Fatalf("modelo default = %q, quer deepseek-v4-flash", p.DefaultModel)
	}
	if strings.HasSuffix(p.BaseURL, "/v1") {
		t.Fatalf("base URL %q ainda usa o /v1 legado", p.BaseURL)
	}
	if !p.DisableThinking {
		t.Fatal("perfil DeepSeek precisa desligar thinking (senao max_tokens vira raciocinio)")
	}
}

// D4.1: OpenAI nao tem flag is_error. Sem o prefixo o modelo nao distingue
// "a tool explodiu" de "a tool retornou este texto".
func TestToolResultErroGanhaPrefixo(t *testing.T) {
	msg := Message{Role: RoleUser, Content: []ContentBlock{
		{Type: "tool_result", ToolUseID: "t1", ToolResult: "boom", IsError: true},
	}}
	out, err := toOpenAIMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !strings.HasPrefix(out[0].Content, "[ERRO] ") {
		t.Fatalf("tool_result de erro sem prefixo [ERRO]: %+v", out)
	}
}

func TestToolResultOkNaoGanhaPrefixo(t *testing.T) {
	msg := Message{Role: RoleUser, Content: []ContentBlock{
		{Type: "tool_result", ToolUseID: "t1", ToolResult: "ok", IsError: false},
	}}
	out, _ := toOpenAIMessage(msg)
	if strings.Contains(out[0].Content, "[ERRO]") {
		t.Fatal("tool_result de sucesso nao pode levar prefixo de erro")
	}
}

// D4.8: thinking desligado tem que ir no corpo do request.
func TestThinkingDesligadoNoRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	c := NewOpenAICompatChat("k", p, "")
	if _, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	th, ok := got["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Fatalf(`request sem {"thinking":{"type":"disabled"}}: %v`, got["thinking"])
	}
}

// D5: go-openai nao desserializa os campos de cache da DeepSeek.
func TestUsageLeCacheHitMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"},{}],
		 "usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CacheReadTokens != 80 {
		t.Fatalf("CacheReadTokens = %d, quer 80", resp.Usage.CacheReadTokens)
	}
}

// Perfil Gemini nao manda thinking (campo e especifico da DeepSeek).
func TestProfileGeminiNaoMandaThinking(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	p := ProfileGemini()
	p.BaseURL = srv.URL
	NewOpenAICompatChat("k", p, "").Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}}},
	})
	if _, present := got["thinking"]; present {
		t.Fatal("perfil Gemini nao pode mandar campo thinking")
	}
}
```

Em `bot/llm/retry_test.go`:

```go
func TestIsRetryable(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{{429, true}, {500, true}, {502, true}, {503, true}, {529, true}, {400, false}, {401, false}, {402, false}}
	for _, c := range cases {
		err := &openai.APIError{HTTPStatusCode: c.code}
		if got := IsRetryable(err); got != c.want {
			t.Errorf("IsRetryable(%d) = %v, quer %v", c.code, got, c.want)
		}
	}
}

// Saldo/auth precisam ser distinguiveis: viram alerta de admin, nao retry.
func TestIsAuthOrBalance(t *testing.T) {
	for _, code := range []int{401, 402} {
		if !IsAuthOrBalance(&openai.APIError{HTTPStatusCode: code}) {
			t.Errorf("HTTP %d deveria contar como auth/saldo", code)
		}
	}
	if IsAuthOrBalance(&openai.APIError{HTTPStatusCode: 500}) {
		t.Error("500 nao e auth/saldo")
	}
}

// A DeepSeek sinaliza saldo por mensagem, nem sempre por status.
func TestIsAuthOrBalancePorMensagem(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 429, Message: "Your prepayment credits are depleted"}
	if !IsAuthOrBalance(err) {
		t.Error("mensagem de credito esgotado deveria contar como saldo")
	}
}

func TestRetryWithBackoffParaEmErroNaoRetryable(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), 3, func() error {
		calls++
		return &openai.APIError{HTTPStatusCode: 400}
	})
	if calls != 1 {
		t.Fatalf("chamadas = %d, quer 1 (400 nao retenta)", calls)
	}
	if err == nil {
		t.Fatal("erro deveria propagar")
	}
}
```

- [ ] **Step 2: Rodar os testes e confirmar que falham**

Run: `cd bot && go test ./llm/ -run 'TestProfile|TestToolResult|TestThinking|TestUsageLe|TestIsRetryable|TestIsAuthOrBalance|TestRetryWith' -v`
Expected: FAIL — `undefined: ProfileDeepSeek`, `undefined: NewOpenAICompatChat`, `undefined: IsRetryable`

- [ ] **Step 3: Criar `bot/llm/profiles.go`**

```go
package llm

// Profile descreve as particularidades de um backend OpenAI-compatible.
// DeepSeek e Gemini falam o mesmo protocolo; o que muda e endpoint, modelo
// default e alguns campos proprietarios.
type Profile struct {
	Name         string
	BaseURL      string
	DefaultModel string
	// DisableThinking manda {"thinking":{"type":"disabled"}} no request.
	// Campo proprietario da DeepSeek: no V4 o raciocinio vem LIGADO por
	// default e consome o max_tokens, devolvendo content vazio.
	DisableThinking bool
	// SupportsVisionInChat indica se o modelo de chat aceita bloco de imagem.
	// DeepSeek: false (visao vive em modelo separado). Gemini: true.
	SupportsVisionInChat bool
}

// ProfileDeepSeek — deepseek-v4-flash. NAO usar deepseek-chat nem
// deepseek-reasoner: descontinuados em 24/07/2026.
func ProfileDeepSeek() Profile {
	return Profile{
		Name:            "deepseek",
		BaseURL:         "https://api.deepseek.com",
		DefaultModel:    "deepseek-v4-flash",
		DisableThinking: true,
	}
}

// ProfileDeepSeekVision — modelo de visao da DeepSeek. Experimental: teto de
// 384 tokens por imagem, sem OCR. Por isso a visao NUNCA decide medicamento
// (ver extrairReceita: o drug_catalog decide).
func ProfileDeepSeekVision() Profile {
	p := ProfileDeepSeek()
	p.DefaultModel = "deepseek-v4-flash-vision-exp"
	p.SupportsVisionInChat = true
	return p
}

// ProfileGemini — endpoint OpenAI-compatible do Google.
func ProfileGemini() Profile {
	return Profile{
		Name:                 "gemini",
		BaseURL:              "https://generativelanguage.googleapis.com/v1beta/openai",
		DefaultModel:         "gemini-3.6-flash",
		SupportsVisionInChat: true,
	}
}

// ProfileByName resolve o perfil pelo nome usado nas env vars.
func ProfileByName(name string) (Profile, bool) {
	switch name {
	case "deepseek":
		return ProfileDeepSeek(), true
	case "deepseek-vision":
		return ProfileDeepSeekVision(), true
	case "gemini":
		return ProfileGemini(), true
	}
	return Profile{}, false
}
```

- [ ] **Step 4: Criar `bot/llm/retry.go`**

```go
package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// IsRetryable classifica erros que se recuperam esperando: rate limit e
// indisponibilidade do backend. Erro de request invalido, auth ou saldo nao
// melhora com espera.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if IsAuthOrBalance(err) {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429, 500, 502, 503, 529:
			return true
		}
	}
	return false
}

// balanceHints sao marcadores de saldo esgotado que os providers mandam no
// corpo, nem sempre acompanhados de status distinto (a DeepSeek usa 429 pra
// "prepayment credits are depleted").
var balanceHints = []string{
	"insufficient balance",
	"credit balance is too low",
	"prepayment credits are depleted",
	"quota",
	"billing",
}

// IsAuthOrBalance identifica falha de credencial ou de saldo — condicao que
// derruba o bot em silencio e por isso vira alerta de admin, nao retry.
func IsAuthOrBalance(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatusCode == 401 || apiErr.HTTPStatusCode == 402 {
			return true
		}
		msg := strings.ToLower(apiErr.Message)
		for _, h := range balanceHints {
			if strings.Contains(msg, h) {
				return true
			}
		}
	}
	low := strings.ToLower(err.Error())
	for _, h := range balanceHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// RetryWithBackoff repete fn enquanto o erro for retryable, com backoff
// exponencial limitado a 30s por espera.
func RetryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	delay := 2 * time.Second
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryable(err) || attempt == maxRetries {
			return err
		}
		wait := delay
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
	return lastErr
}
```

- [ ] **Step 5: Generalizar o provider**

`git mv bot/llm/deepseek_chat.go bot/llm/openai_compat.go`, e então:

1. Renomear `DeepSeekChat` → `OpenAICompatChat`; o struct passa a ser:

```go
type OpenAICompatChat struct {
	client   *openai.Client
	profile  Profile
	model    string
	apiKey   string
	baseURL  string
}
```

2. Novo construtor + shim de compatibilidade:

```go
// NewOpenAICompatChat constroi o provider a partir de um Profile.
// modelOverride vazio usa o DefaultModel do perfil.
func NewOpenAICompatChat(apiKey string, p Profile, modelOverride string) *OpenAICompatChat {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = p.BaseURL
	model := modelOverride
	if model == "" {
		model = p.DefaultModel
	}
	return &OpenAICompatChat{
		client: openai.NewClientWithConfig(cfg), profile: p,
		model: model, apiKey: apiKey, baseURL: p.BaseURL,
	}
}

// NewDeepSeekChat mantem a assinatura antiga pra nao quebrar callers e testes
// existentes. baseURL vazio usa o do perfil.
func NewDeepSeekChat(apiKey, baseURL, defaultModel string) *OpenAICompatChat {
	p := ProfileDeepSeek()
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	return NewOpenAICompatChat(apiKey, p, defaultModel)
}

func (d *OpenAICompatChat) Name() string        { return d.profile.Name }
func (d *OpenAICompatChat) SupportsTools() bool { return true }
func (d *OpenAICompatChat) SupportsVision() bool { return d.profile.SupportsVisionInChat }
```

3. `Chat` passa a montar o request via struct própria (o `go-openai` não expõe `thinking` nem lê os campos de cache). Substituir a chamada `d.client.CreateChatCompletion` por `d.post`:

```go
// compatRequest espelha o ChatCompletionRequest com os campos proprietarios
// que o go-openai nao tem. Precisamos do controle porque:
//   - "thinking" e especifico da DeepSeek e sem ele o V4 raciocina por default;
//   - a resposta traz prompt_cache_hit_tokens/miss que o SDK descarta.
type compatRequest struct {
	Model       string                         `json:"model"`
	Messages    []openai.ChatCompletionMessage `json:"messages"`
	Tools       []openai.Tool                  `json:"tools,omitempty"`
	MaxTokens   int                            `json:"max_tokens,omitempty"`
	Temperature float32                        `json:"temperature,omitempty"`
	Thinking    *thinkingCfg                   `json:"thinking,omitempty"`
}

type thinkingCfg struct {
	Type string `json:"type"`
}

type compatUsage struct {
	PromptTokens         int `json:"prompt_tokens"`
	CompletionTokens     int `json:"completion_tokens"`
	PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
}

type compatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          string             `json:"content"`
			ReasoningContent string             `json:"reasoning_content"`
			ToolCalls        []openai.ToolCall  `json:"tool_calls"`
		} `json:"message"`
		FinishReason openai.FinishReason `json:"finish_reason"`
	} `json:"choices"`
	Usage compatUsage `json:"usage"`
}

// post faz a chamada HTTP com o corpo proprietario e classifica o erro em
// openai.APIError, pra IsRetryable/IsAuthOrBalance funcionarem.
func (d *OpenAICompatChat) post(ctx context.Context, body compatRequest) (compatResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return compatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(d.baseURL, "/")+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return compatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	// Timeout explicito: o servidor da DeepSeek mantem keep-alive por ate 10
	// minutos antes de desistir, e o http.Client default nao tem deadline.
	cl := &http.Client{Timeout: 120 * time.Second}
	res, err := cl.Do(req)
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
		return compatResponse{}, &openai.APIError{
			HTTPStatusCode: res.StatusCode,
			Message:        e.Error.Message,
			Type:           e.Error.Type,
		}
	}
	var out compatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return compatResponse{}, fmt.Errorf("resposta invalida: %w", err)
	}
	return out, nil
}
```

No corpo de `Chat`, montar e enviar:

```go
	cr := compatRequest{
		Model: model, MaxTokens: maxTokens,
		Temperature: float32(req.Temperature), Messages: msgs, Tools: tools,
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
```

E o `Usage` de retorno:

```go
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheReadTokens: resp.Usage.PromptCacheHitTokens,
		},
```

4. Em `toOpenAIMessage`, o `case "tool_result"` passa a honrar `IsError`:

```go
			case "tool_result":
				content := b.ToolResult
				// OpenAI/DeepSeek nao tem flag is_error. Sem o prefixo o modelo
				// nao distingue falha de tool de retorno legitimo — e a regra
				// dura "nunca narre sucesso sem confirmacao" depende disso.
				if b.IsError {
					content = "[ERRO] " + content
				}
				toolResults = append(toolResults, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: b.ToolUseID,
					Content:    content,
				})
```

5. O `case "image"` passa a respeitar o perfil: se `SupportsVisionInChat`, emite `MultiContent` com `image_url`; senão mantém o erro atual.

- [ ] **Step 6: Rodar os testes e confirmar que passam**

Run: `cd bot && go test ./llm/ -v 2>&1 | tail -20`
Expected: PASS em todos, incluindo os 25 testes pré-existentes de `llm/`

- [ ] **Step 7: Commit**

```bash
git add bot/llm/
git commit -m "feat(llm): provider OpenAI-compat unificado com perfis DeepSeek/Gemini

- generaliza deepseek_chat.go em openai_compat.go (perfis + modelo por config)
- deepseek-v4-flash como default; deepseek-chat/reasoner foram aposentados
- thinking:disabled explicito (senao o V4 gasta max_tokens raciocinando)
- le prompt_cache_hit_tokens/miss (go-openai descarta esses campos)
- tool_result de erro ganha prefixo [ERRO] (OpenAI nao tem is_error)
- retry/backoff canonico + classificacao de auth/saldo
- timeout de 120s (http.Client default nao tem deadline)"
```

---

### Task 2: Providers de Analysis, Report e Vision

**Files:**
- Create: `bot/llm/analysis_oai.go`, `bot/llm/report_oai.go`, `bot/llm/vision_oai.go`
- Test: `bot/llm/analysis_oai_test.go`, `bot/llm/vision_oai_test.go`

**Interfaces:**
- Consumes: `Profile`, `RetryWithBackoff` (Task 1); `AnalysisRequest/Response`, `ReportRequest/Response`, `VisionRequest/Response`, `stripJSONFence` (de `anthropic_analysis.go`)
- Produces:
  - `func NewOpenAICompatAnalysis(apiKey string, p Profile, model string) *OpenAICompatAnalysis`
  - `func NewOpenAICompatReport(apiKey string, p Profile, model string) *OpenAICompatReport`
  - `func NewOpenAICompatVision(apiKey string, p Profile, model string) *OpenAICompatVision`

- [ ] **Step 1: Escrever os testes que falham**

```go
// A DeepSeek so tem response_format json_object (nao json_schema), e a doc
// exige a palavra "json" no prompt.
func TestAnalysisPedeJSONObject(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"a\":1}"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatAnalysis("k", p, "").Analyze(context.Background(), AnalysisRequest{
		UserPrompt: "analise isso", SchemaJSON: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	rf, _ := got["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_object" {
		t.Fatalf("sem response_format json_object: %v", got["response_format"])
	}
	if string(resp.JSON) != `{"a":1}` {
		t.Fatalf("JSON = %s", resp.JSON)
	}
}

// A doc da DeepSeek admite que o modelo "may occasionally return empty
// content" com json_object. Uma resposta invalida tem que ser retentada,
// senao a sintese congela em silencio (incidente ja vivido em prod).
func TestAnalysisRetentaJSONInvalido(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, `{"choices":[{"message":{"content":"desculpa, nao entendi"},"finish_reason":"stop"}],"usage":{}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatAnalysis("k", p, "").Analyze(context.Background(), AnalysisRequest{UserPrompt: "x"})
	if err != nil {
		t.Fatalf("deveria ter retentado e passado: %v", err)
	}
	if calls != 2 {
		t.Fatalf("chamadas = %d, quer 2 (uma falha + um retry)", calls)
	}
	if string(resp.JSON) != `{"ok":true}` {
		t.Fatalf("JSON = %s", resp.JSON)
	}
}

func TestVisionMandaImageURLBase64(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"choices":[{"message":{"content":"uma caixa"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeekVision()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatVision("k", p, "").DescribeImage(context.Background(), VisionRequest{
		Prompt: "o que e", ImageMedia: "image/png", ImageData: "QUJD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "uma caixa" {
		t.Fatalf("texto = %q", resp.Text)
	}
	body, _ := json.Marshal(got)
	if !strings.Contains(string(body), "data:image/png;base64,QUJD") {
		t.Fatalf("imagem nao foi como data URI: %s", body)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./llm/ -run 'TestAnalysis|TestVision' -v`
Expected: FAIL — `undefined: NewOpenAICompatAnalysis`

- [ ] **Step 3: Implementar os três providers**

`analysis_oai.go` — pontos obrigatórios:
- `response_format: {"type":"json_object"}` no corpo
- injetar na system a instrução com a palavra **json** literal: `"Responda APENAS um objeto json valido seguindo este schema (sem markdown, sem prefixo, sem comentario):\n" + schema`
- `temperature: 0.2`
- após `stripJSONFence`, tentar `json.Unmarshal`; se falhar, **uma** retentativa acrescentando à system: `"A resposta anterior nao era json valido. Responda somente o objeto json."`
- se a segunda também falhar, devolver erro com os 200 primeiros chars do raw

`report_oai.go` — mais simples: system + user prompt, sem tools, sem `response_format`, devolve texto.

`vision_oai.go` — monta `MultiContent` com `{"type":"image_url","image_url":{"url":"data:"+media+";base64,"+data}}` e o texto do prompt; usa `RetryWithBackoff`.

- [ ] **Step 4: Rodar e confirmar que passam**

Run: `cd bot && go test ./llm/ -v 2>&1 | tail -10`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add bot/llm/
git commit -m "feat(llm): providers OpenAI-compat de analysis, report e vision

analysis usa response_format json_object com retry no parse — a doc da
DeepSeek admite content vazio ocasional, e sintese quebrada ja congelou o
painel em prod sem erro aparente."
```

---

### Task 3: Circuit breaker de tool loop

**Files:**
- Create: `bot/tool_breaker.go`
- Test: `bot/tool_breaker_test.go`

**Interfaces:**
- Produces:
  - `type ToolBreaker struct{ ... }` · `func NewToolBreaker(maxRepeat int) *ToolBreaker`
  - `func (b *ToolBreaker) Allow(name string, args json.RawMessage) bool`
  - `func (b *ToolBreaker) Tripped() bool`
  - `func normalizeToolArgs(args json.RawMessage) string`

- [ ] **Step 1: Escrever os testes que falham**

```go
// Loop de call identica e o modo de falha agentico mais documentado do
// DeepSeek — o modelo chega a escrever "vou parar" e reemite a mesma call.
func TestBreakerCortaCallRepetida(t *testing.T) {
	b := NewToolBreaker(2)
	args := json.RawMessage(`{"data":"2026-08-24"}`)
	if !b.Allow("buscar_agenda", args) {
		t.Fatal("1a chamada tem que passar")
	}
	if !b.Allow("buscar_agenda", args) {
		t.Fatal("2a chamada tem que passar")
	}
	if b.Allow("buscar_agenda", args) {
		t.Fatal("3a chamada identica tem que ser cortada")
	}
	if !b.Tripped() {
		t.Fatal("breaker deveria estar acionado")
	}
}

// Ordem de chave no JSON nao pode enganar o dedupe.
func TestBreakerNormalizaOrdemDasChaves(t *testing.T) {
	b := NewToolBreaker(1)
	b.Allow("t", json.RawMessage(`{"a":1,"b":2}`))
	if b.Allow("t", json.RawMessage(`{"b":2,"a":1}`)) {
		t.Fatal("mesma call com chaves em outra ordem deveria ser cortada")
	}
}

func TestBreakerNaoCortaCallsDiferentes(t *testing.T) {
	b := NewToolBreaker(1)
	b.Allow("buscar_agenda", json.RawMessage(`{"data":"2026-08-24"}`))
	if !b.Allow("buscar_agenda", json.RawMessage(`{"data":"2026-08-25"}`)) {
		t.Fatal("argumento diferente e outra call")
	}
	if !b.Allow("criar_evento", json.RawMessage(`{"data":"2026-08-24"}`)) {
		t.Fatal("tool diferente e outra call")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run TestBreaker -v`
Expected: FAIL — `undefined: NewToolBreaker`

- [ ] **Step 3: Implementar `bot/tool_breaker.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolBreaker corta repeticao da MESMA tool com os MESMOS argumentos dentro
// de um turno. Sem isso, o loop de call identica (modo de falha documentado
// do DeepSeek, onde o modelo verbaliza "vou parar" e reemite) queima tokens e
// trava o turno do usuario.
type ToolBreaker struct {
	maxRepeat int
	seen      map[string]int
	tripped   bool
}

func NewToolBreaker(maxRepeat int) *ToolBreaker {
	if maxRepeat < 1 {
		maxRepeat = 1
	}
	return &ToolBreaker{maxRepeat: maxRepeat, seen: map[string]int{}}
}

// Allow registra a chamada e devolve false quando ela ja passou do teto.
func (b *ToolBreaker) Allow(name string, args json.RawMessage) bool {
	key := name + "|" + normalizeToolArgs(args)
	b.seen[key]++
	if b.seen[key] > b.maxRepeat {
		b.tripped = true
		return false
	}
	return true
}

func (b *ToolBreaker) Tripped() bool { return b.tripped }

// normalizeToolArgs devolve uma forma canonica do JSON de argumentos, com as
// chaves ordenadas — senao a mesma call com chaves em outra ordem escapa do
// dedupe. JSON invalido cai no texto cru (ja e sinal de problema).
func normalizeToolArgs(args json.RawMessage) string {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return strings.TrimSpace(string(args))
	}
	return canonicalJSON(v)
}

func canonicalJSON(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%q:%s", k, canonicalJSON(t[k]))
		}
		sb.WriteByte('}')
		return sb.String()
	case []any:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(canonicalJSON(e))
		}
		sb.WriteByte(']')
		return sb.String()
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
```

- [ ] **Step 4: Rodar e confirmar que passam**

Run: `cd bot && go test ./ -run TestBreaker -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add bot/tool_breaker.go bot/tool_breaker_test.go
git commit -m "feat(agent): circuit breaker de tool loop

dedupe de (tool, args normalizados) com teto por turno. loop de call
identica e o modo de falha agentico mais documentado do DeepSeek."
```

---

### Task 4: Guard de CJK e sanitizador de markdown

**Files:**
- Modify: `bot/output_guard.go`
- Create: `bot/whatsapp_format.go`
- Test: `bot/output_guard_cjk_test.go`, `bot/whatsapp_format_test.go`

**Interfaces:**
- Consumes: `guardInput`, `guardViolation`, `a.detectViolations` (de `output_guard.go`)
- Produces:
  - `func detectI6CJK(text string) []guardViolation`
  - `func sanitizeForWhatsApp(text string) string`

- [ ] **Step 1: Escrever os testes que falham**

```go
// Vazamento de chines e limitacao declarada pelo fornecedor (paper do R1:
// "optimized for Chinese and English... may result in language mixing").
// Publico e idoso: um caractere CJK numa mensagem sobre remedio quebra
// confianca de forma desproporcional.
func TestDetectI6CJK(t *testing.T) {
	if len(detectI6CJK("Bom dia! Ja tomou o remedio?")) != 0 {
		t.Fatal("texto em PT-BR limpo nao pode violar I6")
	}
	v := detectI6CJK("Bom dia! 好的 ja tomou o remedio?")
	if len(v) == 0 {
		t.Fatal("caractere Han deveria violar I6")
	}
	if v[0].Invariant != "I6" {
		t.Fatalf("invariante = %q, quer I6", v[0].Invariant)
	}
}

func TestDetectI6NaoPegaAcentoNemEmoji(t *testing.T) {
	for _, s := range []string{"Já tomou? Coração, atenção!", "Tudo bem 😊 bom dia"} {
		if len(detectI6CJK(s)) != 0 {
			t.Fatalf("falso positivo em %q", s)
		}
	}
}

// O prompt proibe ** e ##, mas nao existia sanitizador — a Anthropic
// simplesmente obedecia. WhatsApp renderiza ** como asterisco literal.
func TestSanitizeForWhatsApp(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hoje: **14:00** reuniao", "Hoje: *14:00* reuniao"},
		{"## Agenda\nreuniao", "Agenda\nreuniao"},
		{"### Titulo", "Titulo"},
		{"texto normal", "texto normal"},
		{"*ja negrito*", "*ja negrito*"},
		{"__sublinhado__", "_sublinhado_"},
	}
	for _, c := range cases {
		if got := sanitizeForWhatsApp(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, quer %q", c.in, got, c.want)
		}
	}
}

// Bullet de markdown vira bullet de texto (WhatsApp nao renderiza "- ").
func TestSanitizePreservaQuebrasDeLinha(t *testing.T) {
	in := "Compromissos:\n- **14:00** contador\n- **17:30** escola"
	got := sanitizeForWhatsApp(in)
	if strings.Contains(got, "**") {
		t.Fatalf("sobrou markdown: %q", got)
	}
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("quebras de linha perdidas: %q", got)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run 'TestDetectI6|TestSanitize' -v`
Expected: FAIL — `undefined: detectI6CJK`, `undefined: sanitizeForWhatsApp`

- [ ] **Step 3: Implementar**

Em `bot/output_guard.go`, adicionar ao cabeçalho de invariantes a linha
`//	I6  caractere CJK na saida (enforce — regenera)` e:

```go
// cjkRe cobre CJK Unified Ideographs, Hiragana, Katakana e Hangul. Nao pega
// acento latino nem emoji.
var cjkRe = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Hangul}]`)

// detectI6CJK — vazamento de idioma. Limitacao declarada pelo fornecedor do
// DeepSeek; publico idoso; assunto remedio. Enforce, nunca audit-only.
func detectI6CJK(text string) []guardViolation {
	if !cjkRe.MatchString(text) {
		return nil
	}
	return []guardViolation{{
		Invariant: "I6",
		Detail:    "caractere CJK na resposta: " + cjkRe.FindString(text),
		Sentence:  sentenceContaining(text, cjkRe.FindString(text)),
	}}
}
```

E registrar em `detectViolations`, junto das demais enforceable:

```go
	enforceable = append(enforceable, detectI6CJK(text)...)
```

Criar `bot/whatsapp_format.go`:

```go
package main

import (
	"regexp"
	"strings"
)

var (
	mdBoldRe    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdUnderRe   = regexp.MustCompile(`__(.+?)__`)
	mdHeadingRe = regexp.MustCompile(`(?m)^#{1,6}\s*`)
)

// sanitizeForWhatsApp converte markdown que o WhatsApp nao renderiza para o
// equivalente que ele renderiza. O prompt ja proibe markdown pesado, mas isso
// era garantido so pela obediencia do modelo — a Anthropic obedecia, o
// DeepSeek emite ** com frequencia. Ultima linha determinista antes do
// transporte.
func sanitizeForWhatsApp(text string) string {
	out := mdBoldRe.ReplaceAllString(text, "*$1*")
	out = mdUnderRe.ReplaceAllString(out, "_$1_")
	out = mdHeadingRe.ReplaceAllString(out, "")
	return strings.TrimRight(out, " \t")
}
```

Aplicar no fim de `guardOutput`, imediatamente antes do `return` da resposta final, para que valha nos três engines.

- [ ] **Step 4: Rodar a suíte inteira**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok` — nenhum teste de guard pré-existente pode quebrar

- [ ] **Step 5: Commit**

```bash
git add bot/output_guard.go bot/whatsapp_format.go bot/output_guard_cjk_test.go bot/whatsapp_format_test.go
git commit -m "feat(guard): invariante I6 (CJK) e sanitizador de markdown do WhatsApp"
```

---

### Task 5: Roteador por sensibilidade

**Files:**
- Create: `bot/llm_router.go`
- Test: `bot/llm_router_test.go`

**Interfaces:**
- Consumes: `llm.ChatProvider`, `User`, `UserTypeIdoso`, `DB`
- Produces:
  - `type ProviderSet struct { Operacional, Clinico llm.ChatProvider; Sales llm.ChatProvider; Analysis llm.AnalysisProvider; Report llm.ReportProvider; Vision llm.VisionProvider }`
  - `func (a *Agent) hasHealthSurface(user *User) bool`
  - `func (a *Agent) chatFor(user *User) llm.ChatProvider`
  - `func (a *Agent) engineNameFor(user *User) string` — devolve `"operational"` ou `"companion"`

- [ ] **Step 1: Escrever os testes que falham**

```go
// Rotear por AGENTE nao separa dado de saude: o caminho operacional tambem
// expoe tools de medicacao (um filho adulto cadastra o remedio da mae).
// O criterio e "este turno PODERIA tocar saude", nao "tocou".
func TestHasHealthSurfaceIdoso(t *testing.T) {
	a := newTestAgent(t)
	u := &User{ID: 1, Type: UserTypeIdoso}
	if !a.hasHealthSurface(u) {
		t.Fatal("idoso sempre tem superficie de saude")
	}
}

func TestHasHealthSurfaceAdultoComMedicacao(t *testing.T) {
	a := newTestAgent(t)
	u := seedUserWithMedication(t, a.db)
	if !a.hasHealthSurface(u) {
		t.Fatal("adulto com medicacao cadastrada tem superficie de saude")
	}
}

func TestHasHealthSurfaceAdultoSoAgenda(t *testing.T) {
	a := newTestAgent(t)
	u := seedPlainUser(t, a.db)
	if a.hasHealthSurface(u) {
		t.Fatal("adulto sem medicacao e sem pessoa vinculada nao tem superficie de saude")
	}
}

// Adulto que cuida de alguem com medicacao tambem carrega posologia.
func TestHasHealthSurfacePessoaVinculadaComMedicacao(t *testing.T) {
	a := newTestAgent(t)
	u := seedUserWithCaredPersonMedication(t, a.db)
	if !a.hasHealthSurface(u) {
		t.Fatal("usuario com pessoa vinculada medicada tem superficie de saude")
	}
}

func TestChatForRoteiaPelaSensibilidade(t *testing.T) {
	a := newTestAgent(t)
	op := &fakeChat{name: "op"}
	cl := &fakeChat{name: "clinico"}
	a.chat, a.companionChat = op, cl

	if got := a.chatFor(seedPlainUser(t, a.db)); got.Name() != "op" {
		t.Fatalf("usuario sem saude foi pro %q", got.Name())
	}
	if got := a.chatFor(&User{ID: 9, Type: UserTypeIdoso}); got.Name() != "clinico" {
		t.Fatalf("idoso foi pro %q", got.Name())
	}
}

// Sem provider clinico configurado, cai no operacional em vez de quebrar.
func TestChatForFallbackQuandoClinicoNil(t *testing.T) {
	a := newTestAgent(t)
	a.chat = &fakeChat{name: "op"}
	a.companionChat = nil
	if got := a.chatFor(&User{ID: 9, Type: UserTypeIdoso}); got == nil || got.Name() != "op" {
		t.Fatal("sem clinico, idoso cai no operacional")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run 'TestHasHealthSurface|TestChatFor' -v`
Expected: FAIL — `undefined: hasHealthSurface`

- [ ] **Step 3: Implementar `bot/llm_router.go`**

```go
package main

import "github.com/giovannirambo/assistente_pessoal/bot/llm"

// hasHealthSurface responde "este turno PODERIA tocar dado de saude?" — nao
// "tocou?". Nao da pra saber qual tool o modelo vai escolher antes de ele
// escolher, entao o criterio e conservador de proposito.
//
// Roteia por USUARIO e nao por tipo de agente porque o agente operacional
// tambem expoe as tools de medicacao: um filho adulto cadastrando o remedio
// da mae carrega posologia pelo caminho operacional.
func (a *Agent) hasHealthSurface(user *User) bool {
	if user == nil {
		return false
	}
	if user.Type == UserTypeIdoso {
		return true
	}
	if a.db == nil {
		return false
	}
	if meds, err := a.db.ListActiveMedications(user.ID); err == nil && len(meds) > 0 {
		return true
	}
	if people, err := a.db.ListPeople(user.ID); err == nil {
		for _, p := range people {
			if meds, err := a.db.ListActiveMedicationsForPerson(user.ID, p.ID); err == nil && len(meds) > 0 {
				return true
			}
		}
	}
	return false
}

// chatFor devolve o ChatProvider do turno. Substitui pickChat (que roteava so
// por user.Type e era codigo morto).
func (a *Agent) chatFor(user *User) llm.ChatProvider {
	if a.hasHealthSurface(user) && a.companionChat != nil {
		return a.companionChat
	}
	return a.chat
}

// engineNameFor da o rotulo de engine usado pelo output-guard e pelo audit.
func (a *Agent) engineNameFor(user *User) string {
	if user != nil && user.Type == UserTypeIdoso {
		return "companion"
	}
	return "operational"
}
```

> **Nota de implementação:** se `ListActiveMedicationsForPerson` não existir com essa assinatura em `db_medication.go`, use a função equivalente que existe (verifique com `grep -n 'func (d \*DB) List.*Medication' bot/db_medication.go`) e ajuste o teste para casar. Não crie função nova de DB nesta task.

- [ ] **Step 4: Rodar e confirmar que passam**

Run: `cd bot && go test ./ -run 'TestHasHealthSurface|TestChatFor' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add bot/llm_router.go bot/llm_router_test.go
git commit -m "feat(llm): roteador por sensibilidade (saude -> clinico, resto -> operacional)"
```

---

### Task 6: Instrumentação de custo

**Files:**
- Create: `bot/db_llm_usage.go`
- Modify: `bot/db.go` (migração da tabela, junto das demais)
- Test: `bot/db_llm_usage_test.go`

**Interfaces:**
- Produces:
  - `type LLMUsageRow struct { UserID int64; Engine, Provider, Model string; InputTokens, OutputTokens, CacheReadTokens int; LatencyMS int64; CreatedAt time.Time }`
  - `func (d *DB) RecordLLMUsage(r LLMUsageRow) error`
  - `func (d *DB) SumLLMUsageSince(since time.Time) (map[string]LLMUsageRow, error)` — agregado por `provider|model`

- [ ] **Step 1: Escrever os testes que falham**

```go
func TestRecordELeLLMUsage(t *testing.T) {
	db := newTestDB(t)
	err := db.RecordLLMUsage(LLMUsageRow{
		UserID: 1, Engine: "operational", Provider: "deepseek", Model: "deepseek-v4-flash",
		InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 800, LatencyMS: 1200,
	})
	if err != nil {
		t.Fatal(err)
	}
	sums, err := db.SumLLMUsageSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := sums["deepseek|deepseek-v4-flash"]
	if !ok {
		t.Fatalf("agregado ausente: %+v", sums)
	}
	if got.InputTokens != 1000 || got.OutputTokens != 200 || got.CacheReadTokens != 800 {
		t.Fatalf("agregado errado: %+v", got)
	}
}

func TestSumLLMUsageRespeitaJanela(t *testing.T) {
	db := newTestDB(t)
	db.RecordLLMUsage(LLMUsageRow{UserID: 1, Provider: "deepseek", Model: "m", InputTokens: 5})
	sums, _ := db.SumLLMUsageSince(time.Now().Add(time.Hour)) // janela no futuro
	if len(sums) != 0 {
		t.Fatalf("janela futura deveria vir vazia: %+v", sums)
	}
}

// Falha de contabilidade nunca pode derrubar o turno do usuario.
func TestRecordLLMUsageNaoQuebraComEngineVazia(t *testing.T) {
	db := newTestDB(t)
	if err := db.RecordLLMUsage(LLMUsageRow{UserID: 1}); err != nil {
		t.Fatalf("linha minima deveria gravar: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run TestRecordELeLLMUsage -v`
Expected: FAIL — `undefined: RecordLLMUsage`

- [ ] **Step 3: Implementar**

Migração (seguir o padrão das demais em `db.go`):

```sql
CREATE TABLE IF NOT EXISTS llm_usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    engine TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_llm_usage_created ON llm_usage(created_at);
```

`RecordLLMUsage` faz um INSERT simples. `SumLLMUsageSince` agrega com
`GROUP BY provider, model` e chaveia o mapa por `provider + "|" + model`.

- [ ] **Step 4: Rodar e confirmar que passam**

Run: `cd bot && go test ./ -run TestRecordELe -v && go test ./... 2>&1 | tail -6`
Expected: PASS em tudo

- [ ] **Step 5: Commit**

```bash
git add bot/db_llm_usage.go bot/db.go bot/db_llm_usage_test.go
git commit -m "feat(obs): tabela llm_usage — contabilidade de token por provider/modelo

sem baseline nao da pra provar a economia da migracao nem detectar regressao."
```

---

### Task 7: Config e wiring por papel

**Files:**
- Modify: `bot/config.go`, `bot/main.go`, `.env.example`
- Test: `bot/config_providers_test.go`

**Interfaces:**
- Consumes: `llm.ProfileByName`, `llm.NewOpenAICompatChat` (Task 1), providers da Task 2
- Produces: campos novos em `Config`:
  `LLMProviderOperacional, LLMProviderClinico, LLMProviderSales, LLMProviderAnalysis, LLMProviderReport, LLMProviderVision string`
  `DeepSeekModel, GeminiAPIKey, GeminiModel string`
  e `func (c *Config) providerKey(name string) string`

- [ ] **Step 1: Escrever os testes que falham**

```go
// A migracao inteira depende do bot subir sem chave da Anthropic.
func TestLoadConfigNaoExigeAnthropicQuandoTudoDeepSeek(t *testing.T) {
	setMinimalEnv(t) // GOOGLE_*, ENCRYPTION_KEY
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("bot tem que subir sem chave Anthropic: %v", err)
	}
	if cfg.LLMProviderOperacional != "deepseek" {
		t.Fatalf("default do operacional = %q, quer deepseek", cfg.LLMProviderOperacional)
	}
}

// Se um papel aponta pra anthropic, ai sim a chave e obrigatoria.
func TestLoadConfigExigeChaveDoProviderEscolhido(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_REPORT", "anthropic")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("papel apontando pra anthropic sem chave deveria falhar no boot")
	}
}

func TestDeepSeekModelDefault(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	cfg, _ := LoadConfig()
	if cfg.DeepSeekModel != "deepseek-v4-flash" {
		t.Fatalf("modelo = %q, quer deepseek-v4-flash", cfg.DeepSeekModel)
	}
}

func TestProviderDesconhecidoFalhaNoBoot(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_OPERACIONAL", "gpt5")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("provider desconhecido tem que falhar no boot, nao silenciosamente")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run 'TestLoadConfigNao|TestDeepSeekModel|TestProviderDesconhecido' -v`
Expected: FAIL

- [ ] **Step 3: Implementar em `config.go`**

- Ler as seis env vars de papel com `envOrDefault(..., "deepseek")`
- `DeepSeekModel` = `envOrDefault("DEEPSEEK_MODEL", "deepseek-v4-flash")`
- `GeminiAPIKey` = `GEMINI_API_KEY`, `GeminiModel` = `envOrDefault("GEMINI_MODEL", "gemini-3.6-flash")`
- Validar cada papel contra `{deepseek, gemini, anthropic}` → erro no boot se desconhecido
- **Trocar** a exigência incondicional de `ANTHROPIC_API_KEY` no map `required` por: para cada papel, exigir a chave do provider que ele referencia
- Remover `LLMProviderCompanion` do map `required`; manter a leitura da var antiga como alias de `LLM_PROVIDER_CLINICO` para compatibilidade

Em `main.go`, substituir o bloco 180–197 por uma construção baseada em perfil:

```go
	build := func(role, provider string) llm.ChatProvider {
		switch provider {
		case "anthropic":
			return llm.NewAnthropicChat(cfg.AnthropicAPIKey, "")
		case "gemini":
			p, _ := llm.ProfileByName("gemini")
			return llm.NewOpenAICompatChat(cfg.GeminiAPIKey, p, cfg.GeminiModel)
		default:
			p, _ := llm.ProfileByName("deepseek")
			if cfg.DeepSeekBaseURL != "" {
				p.BaseURL = cfg.DeepSeekBaseURL
			}
			return llm.NewOpenAICompatChat(cfg.DeepSeekAPIKey, p, cfg.DeepSeekModel)
		}
	}
	opChat := build("operacional", cfg.LLMProviderOperacional)
	clinicoChat := build("clinico", cfg.LLMProviderClinico)
```

Analysis/Report/Vision seguem o mesmo padrão com os construtores da Task 2.
Vision em DeepSeek usa `ProfileByName("deepseek-vision")`.

Atualizar `.env.example` com todas as vars novas e um comentário dizendo que
ausente = `deepseek`.

- [ ] **Step 4: Rodar a suíte inteira**

Run: `cd bot && go build ./... && go test ./... 2>&1 | tail -6`
Expected: build ok, todos os pacotes `ok`

- [ ] **Step 5: Commit**

```bash
git add bot/config.go bot/main.go .env.example bot/config_providers_test.go
git commit -m "feat(config): provider por papel via env; ANTHROPIC_API_KEY deixa de ser obrigatoria"
```

---

### Task 8: Convergência do agente operacional

**Files:**
- Modify: `bot/agent.go`, `bot/agent_proactive.go`, `bot/turn_context.go`, `bot/agent_companion.go`
- Test: `bot/agent_convergence_test.go`

**Interfaces:**
- Consumes: `chatFor`/`engineNameFor` (Task 5), `NewToolBreaker` (Task 3), `runLoopLLM`/`buildMessagesLLM`/`systemPartsToLLM`/`toolDefsToLLM` (existentes)
- Produces:
  - `func (a *Agent) runViaProvider(ctx context.Context, user *User, message string, images []ImageAttachment, systemParts []anthropic.MessageSystemPart, tc *TurnContext, provider llm.ChatProvider, engine string) (string, error)`
  - `runCompanion` passa a ser um wrapper fino sobre `runViaProvider`

- [ ] **Step 1: Escrever os testes que falham**

```go
// O caminho operacional tem que passar pela abstracao — hoje ele fala direto
// com o SDK Anthropic e por isso nao da pra trocar de provider.
func TestRunUsaProviderInjetado(t *testing.T) {
	a := newTestAgent(t)
	fake := &fakeChat{name: "op", reply: "tudo certo"}
	a.chat = fake
	u := seedPlainUser(t, a.db)
	out, err := a.Run(context.Background(), u, "oi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls == 0 {
		t.Fatal("Run nao passou pelo ChatProvider injetado")
	}
	if out != "tudo certo" {
		t.Fatalf("resposta = %q", out)
	}
}

// Usuario com superficie de saude vai pro provider clinico, mesmo nao sendo idoso.
func TestRunAdultoComMedicacaoVaiProClinico(t *testing.T) {
	a := newTestAgent(t)
	op := &fakeChat{name: "op", reply: "op"}
	cl := &fakeChat{name: "clinico", reply: "clinico"}
	a.chat, a.companionChat = op, cl
	u := seedUserWithMedication(t, a.db)
	if _, err := a.Run(context.Background(), u, "oi", nil); err != nil {
		t.Fatal(err)
	}
	if cl.calls == 0 || op.calls != 0 {
		t.Fatalf("roteamento errado: clinico=%d operacional=%d", cl.calls, op.calls)
	}
}

// Provider sem vision (DeepSeek chat) recebe a imagem como texto pre-descrito.
func TestRunImagemViraDescricaoQuandoProviderNaoTemVision(t *testing.T) {
	a := newTestAgent(t)
	a.chat = &fakeChat{name: "op", reply: "vi a foto", supportsVision: false}
	a.vision = &fakeVision{desc: "uma caixa de remedio"}
	u := seedPlainUser(t, a.db)
	if _, err := a.Run(context.Background(), u, "", []ImageAttachment{{Data: []byte("x"), Mime: "image/png"}}); err != nil {
		t.Fatal(err)
	}
	sent := a.chat.(*fakeChat).lastRequest
	if !strings.Contains(flattenLLMMessages(sent.Messages), "uma caixa de remedio") {
		t.Fatal("descricao da imagem nao foi injetada como texto")
	}
}

// O breaker precisa estar ativo no loop, senao a call repetida trava o turno.
func TestRunLoopCortaToolRepetida(t *testing.T) {
	a := newTestAgent(t)
	// fakeChat programado pra pedir SEMPRE a mesma tool com os mesmos args
	a.chat = &fakeChat{name: "op", alwaysCallTool: "buscar_agenda", toolArgs: `{"data":"2026-08-24"}`}
	u := seedPlainUser(t, a.db)
	out, err := a.Run(context.Background(), u, "o que tenho hoje", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.chat.(*fakeChat).calls > 5 {
		t.Fatalf("breaker nao cortou: %d chamadas", a.chat.(*fakeChat).calls)
	}
	if out == "" {
		t.Fatal("mesmo cortando, o turno tem que devolver alguma coisa ao usuario")
	}
}
```

Criar os fakes em `bot/agent_convergence_test.go`:
`fakeChat` implementa `llm.ChatProvider` gravando `calls`, `lastRequest`, e
devolvendo `reply` (ou um `tool_use` quando `alwaysCallTool != ""`);
`fakeVision` implementa `llm.VisionProvider`.

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run 'TestRunUsaProvider|TestRunAdulto|TestRunImagem|TestRunLoopCorta' -v`
Expected: FAIL

- [ ] **Step 3: Implementar a convergência**

1. Renomear `runCompanion` → `runViaProvider`, com dois parâmetros a mais (`provider llm.ChatProvider`, `engine string`), e trocar dentro dele:
   - `a.companionChat` → `provider`
   - `Engine: "companion"` → `Engine: engine`
   - a pré-descrição de imagem só acontece quando `!provider.SupportsVision()`; quando o provider suporta, anexar `ContentBlock{Type:"image", ImageMedia, ImageData}` na última mensagem
   - `a.reconcileTakenAfterTurn(...)` só quando `engine == "companion"`

2. `runCompanion` vira:

```go
func (a *Agent) runCompanion(ctx context.Context, user *User, message string, images []ImageAttachment, systemParts []anthropic.MessageSystemPart, tc *TurnContext) (string, error) {
	return a.runViaProvider(ctx, user, message, images, systemParts, tc, a.companionChat, "companion")
}
```

3. Em `Run()`, substituir todo o trecho que monta `messages` no formato Anthropic e o `if user.Type == UserTypeIdoso ... else runLoop(...)` por:

```go
	if provider := a.chatFor(user); provider != nil {
		return a.runViaProvider(ctx, user, message, images, systemParts, tc, provider, a.engineNameFor(user))
	}
	// Sem provider injetado (testes legados): caminho Anthropic via SDK direto.
	res, err := a.runLoop(ctx, user, buildMessages(history, message, tc.Now, tc.Loc), anthropic.ModelClaudeSonnet4Dot6, systemParts)
	...
```

> `runLoop`, `markLastMessageForCache`, `anthropicRewriteFn` e `createMessagesWithRetry` **permanecem** — são o caminho Anthropic atrás de config, e os testes existentes dependem deles. Não deletar.

4. Em `agent_proactive.go`, aplicar a mesma troca no `RunProactive`.

5. Instrumentar: dentro de `runLoopLLM`, após cada `provider.Chat`, gravar
   `a.db.RecordLLMUsage(...)` com `engine`, `provider.Name()`, `resp.ModelUsed`,
   tokens e latência. Erro de gravação só loga — nunca derruba o turno.

6. Instanciar `NewToolBreaker(2)` por turno em `runLoopLLM` e, quando
   `Allow` devolver false, responder aquele `tool_use` com um `tool_result` de
   erro (`"[ERRO] chamada repetida cortada; use o resultado anterior"`) em vez
   de executar o handler.

- [ ] **Step 4: Rodar a suíte inteira**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok`. Se algum teste legado quebrar por assumir SDK direto,
**migrar o teste** para injetar `fakeChat` — não deletar.

- [ ] **Step 5: Commit**

```bash
git add bot/agent.go bot/agent_proactive.go bot/agent_companion.go bot/turn_context.go bot/agent_convergence_test.go
git commit -m "refactor(agent): Run e RunProactive passam pela abstracao llm

runCompanion vira runViaProvider, parametrizado por provider e engine.
imagem: pre-descrita quando o provider nao tem vision, nativa quando tem.
breaker de tool loop e contabilidade de token ligados no loop."
```

---

### Task 9: Sales agent pela abstração

**Files:**
- Modify: `bot/sales_agent.go`
- Test: `bot/sales_agent_test.go` (existente — adaptar)

**Interfaces:**
- Consumes: `llm.ChatProvider`, `runLoopLLM`
- Produces: campo `salesChat llm.ChatProvider` em `Agent`, setado por `WithProviders`

- [ ] **Step 1: Escrever o teste que falha**

```go
func TestSalesAgentUsaProvider(t *testing.T) {
	a := newTestAgent(t)
	fake := &fakeChat{name: "sales", reply: "posso te ajudar"}
	a.salesChat = fake
	out, err := a.RunSales(context.Background(), &User{ID: 1, Name: "Lead"}, "quanto custa")
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls == 0 {
		t.Fatal("sales nao passou pelo provider")
	}
	if out == "" {
		t.Fatal("resposta vazia")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run TestSalesAgentUsaProvider -v`
Expected: FAIL

- [ ] **Step 3: Portar** as 21 referências `anthropic.*` de `sales_agent.go` para os tipos `llm.*`, usando `runLoopLLM` com `a.salesChat`. Loop pequeno: 512 tokens, 1 tool, sem cache, sem guard — é o piloto mais barato do repo.

- [ ] **Step 4: Rodar**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok`

- [ ] **Step 5: Commit**

```bash
git add bot/sales_agent.go bot/sales_agent_test.go
git commit -m "refactor(sales): agente de vendas pela abstracao llm"
```

---

### Task 10: Alerta de saldo e fallback por request

**Files:**
- Create: `bot/llm_health.go`
- Modify: `bot/agent_companion.go` (dentro de `runLoopLLM`)
- Test: `bot/llm_health_test.go`

**Interfaces:**
- Consumes: `llm.IsAuthOrBalance`, `llm.IsRetryable` (Task 1), `cfg.AdminPhones`, `a.sendMsg`
- Produces:
  - `type FallbackChat struct{ primary, secondary llm.ChatProvider; onFailover func(error) }`
  - `func NewFallbackChat(primary, secondary llm.ChatProvider, onFailover func(error)) *FallbackChat` — implementa `llm.ChatProvider`
  - `func (a *Agent) alertAdminOnce(key, msg string)` — dedupe de 1h

- [ ] **Step 1: Escrever os testes que falham**

```go
// O fallback tem que ser POR REQUEST. Hoje main.go escolhe o provider uma vez
// no boot: se o DeepSeek cair as 3h, o companion fica morto ate alguem
// reiniciar o container — e lembrete de medicacao perdido e dose perdida.
func TestFallbackTrocaDeProviderNoErro(t *testing.T) {
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 503}}
	secondary := &fakeChat{name: "anthropic", reply: "resposta do backup"}
	fc := NewFallbackChat(primary, secondary, nil)
	resp, err := fc.Chat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if secondary.calls != 1 {
		t.Fatal("secundario nao foi acionado")
	}
	if collectLLMText(resp.Content) != "resposta do backup" {
		t.Fatal("resposta veio do provider errado")
	}
}

func TestFallbackNaoTrocaEmErroDeRequestInvalido(t *testing.T) {
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 400}}
	secondary := &fakeChat{name: "anthropic", reply: "backup"}
	fc := NewFallbackChat(primary, secondary, nil)
	if _, err := fc.Chat(context.Background(), llm.ChatRequest{}); err == nil {
		t.Fatal("400 e bug nosso: nao pode mascarar trocando de provider")
	}
	if secondary.calls != 0 {
		t.Fatal("secundario nao devia ter sido chamado")
	}
}

// Saldo esgotado foi exatamente o modo do incidente atual: 1818 erros em 72h
// sem ninguem ser avisado.
func TestSaldoEsgotadoDisparaCallback(t *testing.T) {
	var got error
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 402, Message: "Insufficient Balance"}}
	fc := NewFallbackChat(primary, &fakeChat{name: "b", reply: "ok"}, func(e error) { got = e })
	fc.Chat(context.Background(), llm.ChatRequest{})
	if got == nil {
		t.Fatal("saldo esgotado tem que notificar")
	}
}

func TestAlertAdminOnceDeduplica(t *testing.T) {
	a := newTestAgent(t)
	var sent int
	a.sendMsg = func(phone, text string) error { sent++; return nil }
	a.cfg.AdminPhones = []string{"5561999999999"}
	a.alertAdminOnce("saldo", "saldo acabou")
	a.alertAdminOnce("saldo", "saldo acabou")
	if sent != 1 {
		t.Fatalf("avisos enviados = %d, quer 1 (dedupe de 1h)", sent)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run 'TestFallback|TestSaldo|TestAlertAdmin' -v`
Expected: FAIL

- [ ] **Step 3: Implementar `bot/llm_health.go`**

- `FallbackChat.Chat`: chama `primary`; se `err != nil` e (`IsRetryable(err)` ou `IsAuthOrBalance(err)`), dispara `onFailover(err)` e tenta `secondary`. Erro não-retryable propaga sem tentar o secundário (400 é bug nosso, trocar de provider mascara).
- `Name()` devolve o nome do primário; `SupportsTools`/`SupportsVision` idem.
- `alertAdminOnce`: mapa `key → time.Time` protegido por mutex, janela de 1h, envia para cada telefone de `cfg.AdminPhones` via `a.sendMsg`.
- Em `main.go`, envolver cada `ChatProvider` num `FallbackChat` quando houver chave do provider secundário; o `onFailover` chama `agent.alertAdminOnce`.

> Se `cfg.AdminPhones` não existir, ler de `ADMIN_PHONES` em `config.go` (a env já é usada pela área admin — verifique com `grep -rn 'ADMIN_PHONES' bot/`).

- [ ] **Step 4: Rodar a suíte inteira**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok`

- [ ] **Step 5: Commit**

```bash
git add bot/llm_health.go bot/llm_health_test.go bot/main.go bot/config.go
git commit -m "feat(llm): fallback por request e alerta de saldo pro admin

o fallback era so no boot: DeepSeek fora as 3h deixava o companion morto
ate alguem reiniciar. saldo esgotado agora avisa em vez de silenciar."
```

---

### Task 11: Backoff no scheduler de snapshot

**Files:**
- Modify: `bot/scheduler.go` (ou onde vive `psych_catchup` — localizar com `grep -rn 'psych_catchup' bot/`)
- Test: `bot/scheduler_backoff_test.go`

**Interfaces:**
- Produces:
  - `type snapshotBackoff struct{ ... }`
  - `func (s *snapshotBackoff) ShouldSkip(userID int64, day string) bool`
  - `func (s *snapshotBackoff) RecordFailure(userID int64, day string)`
  - `func (s *snapshotBackoff) RecordSuccess(userID int64, day string)`

- [ ] **Step 1: Escrever os testes que falham**

```go
// Prod gerou 1818 invalid_request_error em 72h reprocessando snapshot em loop
// apertado, varias vezes por segundo, sem backoff.
func TestSnapshotBackoffSegurraApósFalhas(t *testing.T) {
	b := newSnapshotBackoff()
	if b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("primeira tentativa nunca e pulada")
	}
	b.RecordFailure(1, "2026-08-24")
	if !b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("logo apos falhar tem que segurar")
	}
}

func TestSnapshotBackoffIsolaPorUsuarioEDia(t *testing.T) {
	b := newSnapshotBackoff()
	b.RecordFailure(1, "2026-08-24")
	if b.ShouldSkip(2, "2026-08-24") {
		t.Fatal("falha do user 1 nao pode segurar o user 2")
	}
	if b.ShouldSkip(1, "2026-08-25") {
		t.Fatal("falha de um dia nao pode segurar outro dia")
	}
}

func TestSnapshotBackoffLimpaNoSucesso(t *testing.T) {
	b := newSnapshotBackoff()
	b.RecordFailure(1, "2026-08-24")
	b.RecordSuccess(1, "2026-08-24")
	if b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("sucesso tem que zerar o backoff")
	}
}

// Teto: falha permanente (saldo) nao pode virar retry infinito.
func TestSnapshotBackoffTemTeto(t *testing.T) {
	b := newSnapshotBackoff()
	for i := 0; i < 20; i++ {
		b.RecordFailure(1, "2026-08-24")
	}
	if !b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("apos muitas falhas tem que parar de tentar no dia")
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run TestSnapshotBackoff -v`
Expected: FAIL

- [ ] **Step 3: Implementar** backoff exponencial em memória por `(userID, dia)`: 1min, 2, 4, 8… com teto de 1h e desistência após 6 falhas no mesmo dia. Consultar `ShouldSkip` antes de chamar o writer, e registrar sucesso/falha depois.

- [ ] **Step 4: Rodar**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok`

- [ ] **Step 5: Commit**

```bash
git add bot/scheduler*.go bot/scheduler_backoff_test.go
git commit -m "fix(scheduler): backoff no psych_catchup

sem backoff, falha de snapshot virava tempestade: 1818 erros em 72h em prod."
```

---

### Task 12: Sinalizar item de receita sem lastro no catálogo

> **Correção ao escopo original.** A auditoria classificou `extrair_receita_imagem` como "estruturalmente quebrada, dose errada cadastrada a partir de alucinação". Leitura do código desmente: `handleExtrairReceitaImagem` ([tools_medication.go:853](../../../bot/tools_medication.go)) **não persiste nada** — devolve um sumário para o agente apresentar item a item, e só `cadastrar_medicamento` (com confirmação do usuário) grava. Já resolve pelo `ResolveDrug` com confiança ≥ 0,7 e anota `[catálogo sugere: X — confirmar com o usuário]`. O desenho está certo e tem humano no circuito.
>
> **O gap real, menor e específico:** item **sem nenhuma** correspondência no catálogo passa sem qualquer anotação — fica visualmente idêntico a um item lido corretamente. Com modelo mais fraco e visão limitada a 384 tokens/imagem, é exatamente o caso do nome inventado. A correção é anotar a ausência de lastro.

**Files:**
- Modify: `bot/tools_medication.go` (dentro do laço de itens, ~linha 889), `bot/agent.go` (descrição da tool, ~linha 1075)
- Test: `bot/tools_medication_receita_test.go`

**Interfaces:**
- Consumes: `(db *DB) ResolveDrug(query string, limit int) ([]DrugMatch, error)` — confirmado em `drug_catalog.go:120`

- [ ] **Step 1: Escrever os testes que falham**

```go
// Item sem lastro no catalogo hoje sai identico a um item lido corretamente.
// Com visao de 384 tokens e modelo mais fraco, esse e o caso do nome inventado.
func TestReceitaSinalizaItemSemLastroNoCatalogo(t *testing.T) {
	a := newTestAgent(t)
	u := seedPlainUser(t, a.db)
	out, err := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Xyzabcina Fantasia","dose":"50mg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sem correspondência no catálogo") {
		t.Fatalf("item sem lastro nao foi sinalizado: %q", out)
	}
}

// O handler continua nao persistindo — isso e do desenho, nao regressao.
func TestReceitaNaoPersisteNada(t *testing.T) {
	a := newTestAgent(t)
	u := seedPlainUser(t, a.db)
	handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Dipirona","dose":"500mg"}]}`))
	meds, _ := a.db.ListActiveMedications(u.ID)
	if len(meds) != 0 {
		t.Fatalf("handler persistiu %d medicamentos — ele nao deveria persistir nada", len(meds))
	}
}

// Catalogo vazio (ambiente de teste) nao pode transformar todo item em alerta
// ruidoso: sem indice carregado, o handler segue sem anotar.
func TestReceitaCatalogoVazioNaoRuido(t *testing.T) {
	a := newTestAgent(t) // sem seed de catalogo
	u := seedPlainUser(t, a.db)
	out, _ := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Dipirona","dose":"500mg"}]}`))
	if strings.Count(out, "sem correspondência no catálogo") > 1 {
		t.Fatalf("catalogo vazio virou ruido: %q", out)
	}
}
```

- [ ] **Step 2: Rodar e confirmar falha**

Run: `cd bot && go test ./ -run TestReceita -v`
Expected: FAIL — a anotação de "sem correspondência" ainda não existe

- [ ] **Step 3: Implementar**

No laço de itens, o `if matches, err := agent.db.ResolveDrug(...)` ganha um ramo
para o caso sem match. Só anota quando o catálogo **está populado** — senão
ambiente sem índice viraria alerta em todo item:

```go
		matches, err := agent.db.ResolveDrug(it.Name, 1)
		switch {
		case err == nil && len(matches) > 0:
			// ... bloco de sugestao existente, inalterado ...
		case err == nil && agent.db.drugCatalogPopulated():
			// Sem nenhuma correspondencia com o catalogo populado: pode ser
			// leitura ruim do OCR ou nome inventado pelo modelo. O agente
			// precisa saber pra pedir confirmacao explicita antes de cadastrar.
			line += " [sem correspondência no catálogo — confirmar o nome com o usuário antes de cadastrar]"
		}
```

Adicionar em `drug_catalog.go` o helper:

```go
// drugCatalogPopulated diz se ha indice carregado. Sem isso, ambiente sem
// catalogo (teste, primeira subida) anotaria "sem correspondencia" em todo
// item — ruido que treina o agente a ignorar o alerta.
func (db *DB) drugCatalogPopulated() bool {
	idx, err := db.drugIndex()
	return err == nil && len(idx) > 0
}
```

Na descrição da tool em `agent.go`, acrescentar ao final:
`" Só chame esta ferramenta com o que estiver LEGÍVEL na imagem. NUNCA complete itens de memória, por suposição ou por conhecimento geral de medicamentos."`

- [ ] **Step 4: Rodar**

Run: `cd bot && go test ./... 2>&1 | tail -6`
Expected: todos `ok`

- [ ] **Step 5: Commit**

```bash
git add bot/tools_medication.go bot/agent.go bot/tools_medication_receita_test.go
git commit -m "fix(medicacao): receita resolve pelo catalogo CMED antes de persistir

a visao extrai texto; quem decide o medicamento e o drug_catalog. leitura
ruim vira 'nao identifiquei', nunca dose errada cadastrada."
```

---

### Task 13: Harness de qualidade PT-BR

**Files:**
- Create: `bot/quality_harness_test.go`
- Test: o próprio arquivo

**Interfaces:**
- Produces: `func checkResponseQuality(t *testing.T, text string, toolsCalled []string, userMsg string) []string` — devolve lista de problemas

- [ ] **Step 1: Escrever os testes**

```go
// Este harness reprovou o gemini-2.5-flash-lite com 0/5 tool calls e 3/5
// narrando persistencia sem persistir. Entra na suite permanente.
var narraPersistenciaRe = regexp.MustCompile(`(?i)\b(anotei|anotado|registrei|registrado|cadastrei|cadastrado|guardei|salvei|deixar registrado|anotadinho|marquei|marcado)\b`)
var menuNumeradoRe = regexp.MustCompile(`(?m)^\s*[1-9][\).]\s+\S`)

func checkResponseQuality(t *testing.T, text string, toolsCalled []string, userMsg string) []string {
	t.Helper()
	var problems []string
	if cjkRe.MatchString(text) {
		problems = append(problems, "CJK: "+cjkRe.FindString(text))
	}
	if strings.Contains(text, "**") || regexp.MustCompile(`(?m)^#{1,6}\s`).MatchString(text) {
		problems = append(problems, "markdown que o WhatsApp nao renderiza")
	}
	if menuNumeradoRe.MatchString(text) {
		problems = append(problems, "menu numerado (o bot e IA, nao chatbot)")
	}
	if narraPersistenciaRe.MatchString(text) && len(toolsCalled) == 0 {
		problems = append(problems, "narrou persistencia sem chamar tool: "+narraPersistenciaRe.FindString(text))
	}
	return problems
}

func TestHarnessPegaNarrarSemPersistir(t *testing.T) {
	p := checkResponseQuality(t, "Metformina 850mg, anotado!", nil, "anota ai metformina 850")
	if len(p) == 0 {
		t.Fatal("harness tem que pegar narracao sem tool")
	}
}

func TestHarnessAceitaNarrarComTool(t *testing.T) {
	p := checkResponseQuality(t, "Pronto, anotei aqui!", []string{"cadastrar_medicamento"}, "anota ai")
	if len(p) != 0 {
		t.Fatalf("com tool chamada nao ha violacao: %v", p)
	}
}

func TestHarnessPegaCJKEMarkdownEMenu(t *testing.T) {
	if len(checkResponseQuality(t, "好的, tudo certo", nil, "")) == 0 {
		t.Error("CJK nao pego")
	}
	if len(checkResponseQuality(t, "Hoje: **14:00**", nil, "")) == 0 {
		t.Error("markdown nao pego")
	}
	if len(checkResponseQuality(t, "Escolha:\n1) agenda\n2) remedio", nil, "")) == 0 {
		t.Error("menu numerado nao pego")
	}
}

// Replay: roda as respostas do fakeChat pelo harness, garantindo que o
// pipeline inteiro (guard + sanitizador) entrega texto limpo.
func TestPipelineEntregaTextoLimpo(t *testing.T) {
	a := newTestAgent(t)
	a.chat = &fakeChat{name: "op", reply: "Hoje: **14:00** reuniao 好的"}
	u := seedPlainUser(t, a.db)
	out, err := a.Run(context.Background(), u, "o que tenho hoje", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "**") {
		t.Fatalf("markdown vazou pro transporte: %q", out)
	}
}
```

- [ ] **Step 2: Rodar**

Run: `cd bot && go test ./ -run 'TestHarness|TestPipelineEntrega' -v`
Expected: PASS (após Task 4 e Task 8)

- [ ] **Step 3: Commit**

```bash
git add bot/quality_harness_test.go
git commit -m "test: harness de qualidade PT-BR (CJK, markdown, menu, narrar-sem-persistir)"
```

---

### Task 14: Verificação final e deploy

**Files:** nenhum código novo

- [ ] **Step 1: Suíte completa sem chaves no ambiente**

Run: `cd bot && env -u ANTHROPIC_API_KEY -u DEEPSEEK_API_KEY -u GEMINI_API_KEY go test -count=1 ./... 2>&1 | tail -8`
Expected: 4 pacotes `ok`, zero falhas. Confirmar contagem ≥ 731 testes.

- [ ] **Step 2: Build**

Run: `cd bot && go build -o /tmp/bot-check . && echo BUILD_OK`
Expected: `BUILD_OK`

- [ ] **Step 3: Smoke test contra a API real**

Escrever um script pontual (não versionado) que:
1. Carrega `DEEPSEEK_API_KEY` do `.env`
2. Confere saldo em `GET /user/balance`
3. Faz um turno com tool e valida `finish_reason: tool_calls`
4. Roda a resposta pelo `checkResponseQuality`

Expected: saldo > 0, tool chamada, zero problemas de qualidade.

- [ ] **Step 4: Preparar o `.env` de produção**

Via SSM (base64 — ver `[[ssm-base64-quoting]]`), **acrescentando linhas, nunca sobrescrevendo o arquivo** (`ADMIN_TOTP_SECRET` e outros segredos vivem lá):

```
DEEPSEEK_API_KEY=<chave>
DEEPSEEK_MODEL=deepseek-v4-flash
LLM_PROVIDER_OPERACIONAL=deepseek
LLM_PROVIDER_CLINICO=deepseek
LLM_PROVIDER_SALES=deepseek
LLM_PROVIDER_ANALYSIS=deepseek
LLM_PROVIDER_REPORT=deepseek
LLM_PROVIDER_VISION=deepseek-vision
```

- [ ] **Step 5: Deploy**

Pré-checagens obrigatórias antes de buildar (t3.small, 2GB — build sem swap derruba prod):

```bash
swapon --show      # nao pode estar vazio
df -h /            # se apertado: docker builder prune -af
```

Deploy:
```bash
cd /opt/assistente
sudo -u ec2-user git fetch origin           # OBRIGATORIO — reset nao busca da rede
sudo -u ec2-user git reset --hard origin/main
sudo -u ec2-user docker compose build bot
sudo -u ec2-user docker compose up -d bot
```

Expected: o `up` mostra `Recreated`. Container `Up X days` pós-deploy significa no-op.

- [ ] **Step 6: Verificação pós-deploy**

```bash
docker compose logs --since 5m bot | grep -icE 'invalid_request_error|credit balance'   # tem que ser 0
docker compose logs --since 5m bot | grep -iE 'provider=|model='                        # tem que mostrar deepseek
curl -s -o /dev/null -w '%{http_code}' https://api.zello.chat/api/v1/me                 # 401
curl -s -o /dev/null -w '%{http_code}' https://zello.chat/login                         # 200
```

- [ ] **Step 7: Commit final e avisar o usuário para testar**

---

## Self-Review

**Cobertura da spec:**

| Item da spec | Task |
|---|---|
| §3.3 provider unificado + perfis | 1 |
| §3.3 analysis/report/vision | 2 |
| §3.1/3.2 roteamento por sensibilidade | 5 |
| §3.4 convergência do agente | 8, 9 |
| §4.1 `is_error` → `[ERRO]` | 1 |
| §4.2 circuit breaker | 3, 8 |
| §4.3 guard CJK | 4 |
| §4.4 sanitizador markdown | 4 |
| §4.5 alerta de saldo | 10 |
| §4.6 backoff do scheduler | 11 |
| §4.7 fallback por request | 10 |
| §4.8 thinking desligado | 1 |
| §4.9 visão não decide medicamento | 12 |
| §5 observabilidade `llm_usage` | 6 |
| §6 aceite (suíte, paridade, PT-BR) | 13, 14 |
| §7 sequência e deploy | 14 |

Sem lacunas.

**Consistência de tipos:** `OpenAICompatChat` (Task 1) é usado por Tasks 2, 7, 10. `ToolBreaker.Allow` (Task 3) é consumido em Task 8. `chatFor`/`engineNameFor` (Task 5) são consumidos em Task 8. `RecordLLMUsage` (Task 6) é chamado em Task 8. `cjkRe` é definido em Task 4 e reutilizado em Task 13. `fakeChat` nasce em Task 8 e é reutilizado em 9, 10, 13.

**Riscos de execução:** Tasks 5, 10, 11 e 12 dependem de nomes de função que precisam ser confirmados no código (`ListActiveMedicationsForPerson`, `ADMIN_PHONES`, local do `psych_catchup`, `Resolve*` do catálogo). Cada uma carrega o `grep` que resolve a dúvida — o executor confirma antes de escrever.
