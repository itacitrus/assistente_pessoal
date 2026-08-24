package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer devolve um httptest.Server que responde `body` e grava o request
// decodificado em `got`.
func stubServer(t *testing.T, got *map[string]any, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			*got = m
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const okBody = `{"model":"deepseek-v4-flash","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`

func simpleReq() ChatRequest {
	return ChatRequest{Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}}}}
}

// Perfil DeepSeek nao pode apontar pros modelos aposentados (24/07/2026) nem
// pro /v1 legado.
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
	if p.SupportsVisionInChat {
		t.Fatal("modelo de chat da DeepSeek recusa imagem com HTTP 400")
	}
}

func TestProfileByName(t *testing.T) {
	for _, name := range []string{"deepseek", "deepseek-vision", "gemini"} {
		if _, ok := ProfileByName(name); !ok {
			t.Errorf("perfil %q deveria existir", name)
		}
	}
	if _, ok := ProfileByName("gpt5"); ok {
		t.Error("perfil desconhecido nao pode resolver")
	}
}

func TestProfileDeepSeekVisionAceitaImagem(t *testing.T) {
	p := ProfileDeepSeekVision()
	if !p.SupportsVisionInChat {
		t.Fatal("perfil de visao tem que aceitar imagem")
	}
	if p.DefaultModel != "deepseek-v4-flash-vision-exp" {
		t.Fatalf("modelo = %q", p.DefaultModel)
	}
}

// D4.1: OpenAI nao tem flag is_error. Sem o prefixo o modelo nao distingue
// "a tool explodiu" de "a tool retornou este texto" — e a regra dura de nunca
// narrar sucesso sem confirmacao da ferramenta depende dessa distincao.
func TestToolResultErroGanhaPrefixo(t *testing.T) {
	msg := Message{Role: RoleUser, Content: []ContentBlock{
		{Type: "tool_result", ToolUseID: "t1", ToolResult: "boom", IsError: true},
	}}
	out, err := toOpenAIMessage(msg, Profile{})
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
	out, err := toOpenAIMessage(msg, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out[0].Content, "[ERRO]") {
		t.Fatal("tool_result de sucesso nao pode levar prefixo de erro")
	}
}

// D4.8: no V4 o thinking vem LIGADO por default e consome o max_tokens —
// com orcamento baixo o content volta vazio (mensagem em branco no WhatsApp).
func TestThinkingDesligadoNoRequest(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got, okBody)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	if _, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), simpleReq()); err != nil {
		t.Fatal(err)
	}
	th, ok := got["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Fatalf(`request sem {"thinking":{"type":"disabled"}}: %v`, got["thinking"])
	}
}

func TestProfileGeminiNaoMandaThinking(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got, okBody)
	p := ProfileGemini()
	p.BaseURL = srv.URL
	if _, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), simpleReq()); err != nil {
		t.Fatal(err)
	}
	if _, present := got["thinking"]; present {
		t.Fatal("perfil Gemini nao pode mandar campo thinking (proprietario da DeepSeek)")
	}
}

// D5: go-openai nao desserializa os campos de cache da DeepSeek — sem isso
// nao da pra medir se a economia de cache esta acontecendo.
func TestUsageLeCacheHitMiss(t *testing.T) {
	body := `{"model":"deepseek-v4-flash","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
	 "usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`
	srv := stubServer(t, nil, body)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), simpleReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CacheReadTokens != 80 {
		t.Fatalf("CacheReadTokens = %d, quer 80", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 10 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestModelOverridePorConfig(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got, okBody)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	if _, err := NewOpenAICompatChat("k", p, "deepseek-v4-pro").Chat(context.Background(), simpleReq()); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "deepseek-v4-pro" {
		t.Fatalf("modelo = %v, quer deepseek-v4-pro", got["model"])
	}
}

// Erro HTTP tem que virar openai.APIError pra IsRetryable/IsAuthOrBalance
// conseguirem classificar.
func TestErroHTTPViraAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		io.WriteString(w, `{"error":{"message":"Insufficient Balance","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	_, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), simpleReq())
	if err == nil {
		t.Fatal("402 deveria propagar erro")
	}
	if !IsAuthOrBalance(err) {
		t.Fatalf("erro de saldo nao foi classificado: %v", err)
	}
}

// Perfil com visao serializa a imagem como data URI.
func TestVisionProfileSerializaImagem(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got, okBody)
	p := ProfileDeepSeekVision()
	p.BaseURL = srv.URL
	req := ChatRequest{Messages: []Message{{Role: RoleUser, Content: []ContentBlock{
		{Type: "text", Text: "o que e isso"},
		{Type: "image", ImageMedia: "image/png", ImageData: "QUJD"},
	}}}}
	if _, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), "data:image/png;base64,QUJD") {
		t.Fatalf("imagem nao foi como data URI: %s", raw)
	}
}

// Perfil sem visao continua falhando rapido em vez de silenciar a imagem.
func TestSemVisionImagemFalhaRapido(t *testing.T) {
	srv := stubServer(t, nil, okBody)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	req := ChatRequest{Messages: []Message{{Role: RoleUser, Content: []ContentBlock{
		{Type: "image", ImageMedia: "image/png", ImageData: "QUJD"},
	}}}}
	if _, err := NewOpenAICompatChat("k", p, "").Chat(context.Background(), req); err == nil {
		t.Fatal("provider sem visao tem que falhar rapido com bloco de imagem")
	}
}

// Shim de compatibilidade: NewDeepSeekChat continua existindo pros callers
// e testes antigos, mas agora entrega o perfil correto.
func TestNewDeepSeekChatShim(t *testing.T) {
	c := NewDeepSeekChat("k", "", "")
	if c.Name() != "deepseek" {
		t.Fatalf("Name = %q", c.Name())
	}
	if !c.SupportsTools() {
		t.Fatal("DeepSeek suporta tools")
	}
	if c.SupportsVision() {
		t.Fatal("modelo de chat da DeepSeek nao tem vision")
	}
	if c.model != "deepseek-v4-flash" {
		t.Fatalf("shim caiu num modelo aposentado: %q", c.model)
	}
}
