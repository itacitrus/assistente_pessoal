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

// A DeepSeek so tem response_format json_object (nao json_schema), e a doc
// exige que a palavra "json" apareca no prompt.
func TestAnalysisPedeJSONObject(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got,
		`{"choices":[{"message":{"content":"{\"a\":1}"},"finish_reason":"stop"}],"usage":{}}`)
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
	raw, _ := json.Marshal(got["messages"])
	if !strings.Contains(strings.ToLower(string(raw)), "json") {
		t.Fatal("a doc da DeepSeek exige a palavra json no prompt")
	}
}

// A doc da DeepSeek admite que o modelo "may occasionally return empty
// content" com json_object. Sem retry, a sintese congela em silencio — que e
// exatamente o incidente ja vivido em prod (painel congelado, sem erro).
func TestAnalysisRetentaJSONInvalido(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
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

func TestAnalysisDesisteAposSegundaFalha(t *testing.T) {
	srv := stubServer(t, nil,
		`{"choices":[{"message":{"content":"nada de json aqui"},"finish_reason":"stop"}],"usage":{}}`)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	if _, err := NewOpenAICompatAnalysis("k", p, "").Analyze(context.Background(), AnalysisRequest{UserPrompt: "x"}); err == nil {
		t.Fatal("duas respostas invalidas tem que virar erro, nao JSON silenciosamente vazio")
	}
}

// Modelo devolvendo ```json ...``` continua sendo aceito.
func TestAnalysisTiraFenceDeMarkdown(t *testing.T) {
	srv := stubServer(t, nil,
		"{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"a\\\":2}\\n```\"},\"finish_reason\":\"stop\"}],\"usage\":{}}")
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatAnalysis("k", p, "").Analyze(context.Background(), AnalysisRequest{UserPrompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.JSON) != `{"a":2}` {
		t.Fatalf("JSON = %s", resp.JSON)
	}
}

func TestReportDevolveTexto(t *testing.T) {
	srv := stubServer(t, nil,
		`{"choices":[{"message":{"content":"A semana foi tranquila."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	p := ProfileDeepSeek()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatReport("k", p, "").Synthesize(context.Background(), ReportRequest{UserPrompt: "resuma"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "A semana foi tranquila." {
		t.Fatalf("texto = %q", resp.Text)
	}
	if resp.Usage.InputTokens != 10 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestVisionMandaImageURLBase64(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got,
		`{"choices":[{"message":{"content":"uma caixa de remedio"},"finish_reason":"stop"}],"usage":{}}`)
	p := ProfileDeepSeekVision()
	p.BaseURL = srv.URL
	resp, err := NewOpenAICompatVision("k", p, "").DescribeImage(context.Background(), VisionRequest{
		Prompt: "o que e", ImageMedia: "image/png", ImageData: "QUJD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "uma caixa de remedio" {
		t.Fatalf("texto = %q", resp.Text)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), "data:image/png;base64,QUJD") {
		t.Fatalf("imagem nao foi como data URI: %s", raw)
	}
	if got["model"] != "deepseek-v4-flash-vision-exp" {
		t.Fatalf("modelo de visao = %v", got["model"])
	}
}

func TestVisionMediaVaziaAssumeJPEG(t *testing.T) {
	var got map[string]any
	srv := stubServer(t, &got, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}],"usage":{}}`)
	p := ProfileDeepSeekVision()
	p.BaseURL = srv.URL
	NewOpenAICompatVision("k", p, "").DescribeImage(context.Background(), VisionRequest{
		Prompt: "o que e", ImageData: "QUJD",
	})
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), "data:image/jpeg;base64") {
		t.Fatalf("media vazia deveria virar jpeg: %s", raw)
	}
}

func TestProvidersReportamNomeDoPerfil(t *testing.T) {
	p := ProfileDeepSeek()
	if n := NewOpenAICompatAnalysis("k", p, "").Name(); n != "deepseek" {
		t.Errorf("analysis Name = %q", n)
	}
	if n := NewOpenAICompatReport("k", p, "").Name(); n != "deepseek" {
		t.Errorf("report Name = %q", n)
	}
	if n := NewOpenAICompatVision("k", ProfileDeepSeekVision(), "").Name(); n != "deepseek" {
		t.Errorf("vision Name = %q", n)
	}
}
