package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// =========================================================================
// LinkAllowed / MatchHost — table driven cobre subdominio direto e
// negativos (SSRF-defense).
// =========================================================================

func TestMatchHost_TableDriven(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		// Exato
		{"globo.com", true},
		{"youtube.com", true},
		{"x.com", true},

		// Com www. e m. (normalizados)
		{"www.globo.com", true},
		{"m.facebook.com", true},
		{"WWW.GLOBO.COM", true},

		// Subdominio direto
		{"blog.globo.com", true},
		{"noticias.uol.com.br", true},

		// Whitespace e case
		{"  globo.com ", true},

		// Negativos — defesa contra SSRF e domain spoofing
		{"globo.com.evil.tk", false},
		{"globo.com-evil.tk", false},
		{"random-blog.tk", false},
		{"localhost", false},
		{"127.0.0.1", false},
		{"169.254.169.254", false},
		{"10.0.0.1", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			if got := MatchHost(c.host); got != c.want {
				t.Fatalf("MatchHost(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestLinkAllowed_AliasOfMatchHost(t *testing.T) {
	if MatchHost("globo.com") != LinkAllowed("globo.com") {
		t.Fatal("LinkAllowed should be alias of MatchHost")
	}
	if MatchHost("evil.tk") != LinkAllowed("evil.tk") {
		t.Fatal("LinkAllowed should be alias of MatchHost (negative)")
	}
}

// =========================================================================
// AnthropicChat translate — round-trip de tipos.
// Testa toAnthropicMessage / fromAnthropicContent / mapAnthropicStop sem
// chamar Anthropic real.
// =========================================================================

func TestToAnthropicMessage_TextOnly(t *testing.T) {
	m := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "text", Text: "oi"},
		},
	}
	am, err := toAnthropicMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(am.Role) != "user" {
		t.Errorf("role: got %s want user", am.Role)
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(am.Content))
	}
}

func TestToAnthropicMessage_ToolUse(t *testing.T) {
	m := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: "tool_use", ToolUseID: "abc", ToolName: "x", ToolInput: json.RawMessage(`{"k":"v"}`)},
		},
	}
	am, err := toAnthropicMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(am.Content))
	}
	if am.Content[0].MessageContentToolUse == nil {
		t.Fatal("expected tool_use block")
	}
	if am.Content[0].MessageContentToolUse.ID != "abc" {
		t.Errorf("id: got %s", am.Content[0].MessageContentToolUse.ID)
	}
}

func TestToAnthropicMessage_ToolResult(t *testing.T) {
	m := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "abc", ToolResult: "ok", IsError: false},
		},
	}
	am, err := toAnthropicMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(am.Content))
	}
	if am.Content[0].MessageContentToolResult == nil {
		t.Fatal("expected tool_result block")
	}
}

func TestToAnthropicMessage_ImageBase64(t *testing.T) {
	m := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "image", ImageMedia: "image/jpeg", ImageData: "AAAA"},
		},
	}
	am, err := toAnthropicMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(am.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(am.Content))
	}
	if am.Content[0].Source == nil {
		t.Fatal("expected image source")
	}
	if am.Content[0].Source.MediaType != "image/jpeg" {
		t.Errorf("media: %s", am.Content[0].Source.MediaType)
	}
}

func TestToAnthropicMessage_UnsupportedType(t *testing.T) {
	m := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "unknown_xyz"},
		},
	}
	if _, err := toAnthropicMessage(m); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// =========================================================================
// DeepSeek translate — round-trip tool_use / tool_result.
// =========================================================================

func TestToOpenAIMessage_TextOnly(t *testing.T) {
	m := Message{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}}
	out, err := toOpenAIMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(out))
	}
	if out[0].Role != openai.ChatMessageRoleUser {
		t.Errorf("role: %s", out[0].Role)
	}
	if out[0].Content != "oi" {
		t.Errorf("content: %s", out[0].Content)
	}
}

func TestToOpenAIMessage_AssistantToolUse(t *testing.T) {
	m := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: "text", Text: "vou chamar tool"},
			{Type: "tool_use", ToolUseID: "tool-1", ToolName: "buscar", ToolInput: json.RawMessage(`{"q":"x"}`)},
		},
	}
	out, err := toOpenAIMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 assistant msg, got %d", len(out))
	}
	if out[0].Role != openai.ChatMessageRoleAssistant {
		t.Errorf("role: %s", out[0].Role)
	}
	if len(out[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(out[0].ToolCalls))
	}
	if out[0].ToolCalls[0].ID != "tool-1" {
		t.Errorf("id: %s", out[0].ToolCalls[0].ID)
	}
	if out[0].ToolCalls[0].Function.Name != "buscar" {
		t.Errorf("name: %s", out[0].ToolCalls[0].Function.Name)
	}
}

func TestToOpenAIMessage_ToolResultEmittedAsRoleTool(t *testing.T) {
	m := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "tool-1", ToolResult: `{"ok":true}`},
		},
	}
	out, err := toOpenAIMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(out))
	}
	if out[0].Role != openai.ChatMessageRoleTool {
		t.Errorf("expected role=tool, got %s", out[0].Role)
	}
	if out[0].ToolCallID != "tool-1" {
		t.Errorf("tool_call_id: %s", out[0].ToolCallID)
	}
	if out[0].Content != `{"ok":true}` {
		t.Errorf("content: %s", out[0].Content)
	}
}

func TestToOpenAIMessage_RejectsImage(t *testing.T) {
	m := Message{
		Role:    RoleUser,
		Content: []ContentBlock{{Type: "image", ImageMedia: "image/jpeg", ImageData: "AAAA"}},
	}
	if _, err := toOpenAIMessage(m); err == nil {
		t.Fatal("DeepSeek-chat should not accept image content")
	}
}

func TestMapOpenAIStop(t *testing.T) {
	cases := []struct {
		in   openai.FinishReason
		want StopReason
	}{
		{openai.FinishReasonStop, StopEndTurn},
		{openai.FinishReasonToolCalls, StopToolUse},
		{openai.FinishReasonFunctionCall, StopToolUse},
		{openai.FinishReasonLength, StopMaxTokens},
		{openai.FinishReasonContentFilter, StopError},
		{openai.FinishReasonNull, StopEndTurn},
	}
	for _, c := range cases {
		t.Run(string(c.in), func(t *testing.T) {
			if got := mapOpenAIStop(c.in); got != c.want {
				t.Errorf("got %s want %s", got, c.want)
			}
		})
	}
}

// =========================================================================
// DeepSeekChat — integracao com mock OpenAI server.
// Confirma:
//   1. system parts concatenam numa role=system.
//   2. tools sao traduzidos com Function.Parameters.
//   3. ChatCompletion choice -> ContentBlocks (text + tool_use).
//   4. Cacheable=true em SystemPart NAO causa erro (ignorado).
// =========================================================================

func TestDeepSeekChat_BasicRoundTrip(t *testing.T) {
	var capturedReq openai.ChatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)
		resp := openai.ChatCompletionResponse{
			ID:      "x",
			Object:  "chat.completion",
			Choices: []openai.ChatCompletionChoice{
				{
					Index: 0,
					Message: openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: "ola",
					},
					FinishReason: openai.FinishReasonStop,
				},
			},
			Usage: openai.Usage{PromptTokens: 10, CompletionTokens: 2},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	d := NewDeepSeekChat("sk-test", srv.URL, "deepseek-chat")
	got, err := d.Chat(context.Background(), ChatRequest{
		System: []SystemPart{
			{Text: "system A", Cacheable: true}, // Cacheable ignorado — sem erro.
			{Text: "system B"},
		},
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}},
		},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.ModelUsed != "deepseek-chat" {
		t.Errorf("model: %s", got.ModelUsed)
	}
	if len(got.Content) != 1 || got.Content[0].Text != "ola" {
		t.Errorf("content: %+v", got.Content)
	}
	if got.StopReason != StopEndTurn {
		t.Errorf("stop: %s", got.StopReason)
	}
	if got.Usage.InputTokens != 10 {
		t.Errorf("input: %d", got.Usage.InputTokens)
	}

	// Confirma que system parts foram concatenados.
	if len(capturedReq.Messages) < 1 || capturedReq.Messages[0].Role != "system" {
		t.Fatalf("expected first message system, got %+v", capturedReq.Messages)
	}
	sysContent := capturedReq.Messages[0].Content
	if !strings.Contains(sysContent, "system A") || !strings.Contains(sysContent, "system B") {
		t.Errorf("expected concat of both system parts, got: %s", sysContent)
	}
}

func TestDeepSeekChat_ToolUseInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openai.ChatCompletionResponse{
			ID: "x", Object: "chat.completion",
			Choices: []openai.ChatCompletionChoice{
				{
					Message: openai.ChatCompletionMessage{
						Role: openai.ChatMessageRoleAssistant,
						ToolCalls: []openai.ToolCall{
							{
								ID:   "tool-99",
								Type: openai.ToolTypeFunction,
								Function: openai.FunctionCall{
									Name:      "buscar",
									Arguments: `{"q":"hello"}`,
								},
							},
						},
					},
					FinishReason: openai.FinishReasonToolCalls,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	d := NewDeepSeekChat("sk-test", srv.URL, "deepseek-chat")
	got, err := d.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "oi"}}},
		},
		Tools: []ToolDef{
			{Name: "buscar", Description: "busca", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.StopReason != StopToolUse {
		t.Errorf("stop: %s, want tool_use", got.StopReason)
	}
	if len(got.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(got.Content))
	}
	if got.Content[0].Type != "tool_use" {
		t.Errorf("type: %s", got.Content[0].Type)
	}
	if got.Content[0].ToolUseID != "tool-99" {
		t.Errorf("tool_use_id: %s", got.Content[0].ToolUseID)
	}
	if got.Content[0].ToolName != "buscar" {
		t.Errorf("tool_name: %s", got.Content[0].ToolName)
	}
}

// =========================================================================
// stripJSONFence — analysis helper.
// =========================================================================

func TestStripJSONFence(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"  ```json\n{\"a\":1}\n```  ", `{"a":1}`},
	}
	for _, c := range cases {
		if got := stripJSONFence(c.in); got != c.want {
			t.Errorf("stripJSONFence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// =========================================================================
// joinNonEmpty
// =========================================================================

func TestJoinNonEmpty(t *testing.T) {
	cases := []struct {
		parts []string
		sep   string
		want  string
	}{
		{[]string{"a", "b"}, "-", "a-b"},
		{[]string{"a", "", "b"}, "-", "a-b"},
		{[]string{"", ""}, "-", ""},
		{[]string{}, "-", ""},
	}
	for _, c := range cases {
		if got := joinNonEmpty(c.parts, c.sep); got != c.want {
			t.Errorf("joinNonEmpty(%v) = %q, want %q", c.parts, got, c.want)
		}
	}
}

// =========================================================================
// Provider Name() — lock against typo in provider routing.
// =========================================================================

func TestAnthropicChat_Name(t *testing.T) {
	a := NewAnthropicChat("k", "")
	if a.Name() != "anthropic" {
		t.Fatalf("name: %s", a.Name())
	}
	if !a.SupportsTools() {
		t.Fatal("anthropic supports tools")
	}
	if !a.SupportsVision() {
		t.Fatal("anthropic supports vision")
	}
}

func TestDeepSeekChat_Name(t *testing.T) {
	d := NewDeepSeekChat("k", "", "")
	if d.Name() != "deepseek" {
		t.Fatalf("name: %s", d.Name())
	}
	if !d.SupportsTools() {
		t.Fatal("deepseek supports tools")
	}
	if d.SupportsVision() {
		t.Fatal("deepseek-chat does not support vision")
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"www.globo.com", "globo.com"},
		{"M.FACEBOOK.COM", "facebook.com"},
		{"  bbc.com  ", "bbc.com"},
		{"GLOBO.COM", "globo.com"},
	}
	for _, c := range cases {
		if got := NormalizeHost(c.in); got != c.want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// =========================================================================
// looksLikeBase64
// =========================================================================

// =========================================================================
// Constructors smoke — confirma defaults + Name() — sem chamada de API.
// =========================================================================

func TestAnthropicAnalysis_Constructor(t *testing.T) {
	a := NewAnthropicAnalysis("k", "")
	if a.Name() != "anthropic" {
		t.Fatalf("name: %s", a.Name())
	}
	a2 := NewAnthropicAnalysis("k", "claude-haiku-custom")
	if a2.defaultModel != "claude-haiku-custom" {
		t.Fatalf("override: %s", a2.defaultModel)
	}
}

func TestAnthropicReport_Constructor(t *testing.T) {
	r := NewAnthropicReport("k", "")
	if r.Name() != "anthropic" {
		t.Fatalf("name: %s", r.Name())
	}
	if r.defaultModel == "" {
		t.Fatal("default model should be non-empty")
	}
}

func TestAnthropicVision_Constructor(t *testing.T) {
	v := NewAnthropicVision("k", "")
	if v.Name() != "anthropic" {
		t.Fatalf("name: %s", v.Name())
	}
}

// =========================================================================
// fromAnthropicContent — estruturas vazias e tipos de bloco.
// =========================================================================

func TestFromAnthropicContent_Empty(t *testing.T) {
	out := fromAnthropicContent(nil)
	if len(out) != 0 {
		t.Fatalf("expected 0 blocks for nil, got %d", len(out))
	}
}

func TestMapAnthropicStop(t *testing.T) {
	if mapAnthropicStop("end_turn") != StopEndTurn {
		t.Error("end_turn")
	}
	if mapAnthropicStop("tool_use") != StopToolUse {
		t.Error("tool_use")
	}
	if mapAnthropicStop("max_tokens") != StopMaxTokens {
		t.Error("max_tokens")
	}
	if mapAnthropicStop("stop_sequence") != StopEndTurn {
		t.Error("stop_sequence falls back to end_turn")
	}
	if mapAnthropicStop("xyz_unknown") != StopEndTurn {
		t.Error("unknown falls back to end_turn")
	}
}

func TestLooksLikeBase64(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"AAAA", true},
		{"abc==", true},
		{"abc/+", true},
		{"hello world!", false}, // espaco e !
		{"abc😀", false},
		{"", false},
		{"AB", false}, // muito curto
	}
	for _, c := range cases {
		if got := looksLikeBase64(c.s); got != c.want {
			t.Errorf("looksLikeBase64(%q) = %v want %v", c.s, got, c.want)
		}
	}
}
