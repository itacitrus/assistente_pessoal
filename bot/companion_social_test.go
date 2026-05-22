package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================================================================
// Separação de modos (C): núcleo social x regras farmacológicas.
// =========================================================================

func TestCompanionPromptSplit(t *testing.T) {
	core := buildCompanionCore("Joaquim")
	pharma := buildCompanionPharmaRules()

	// O núcleo NÃO pode carregar as regras de remédio — é o ponto do split.
	leaks := []string{"REGRA FARMACOLÓGICA", "REGRA DURA DE VERDADE", "marcar_remedio_tomado", "cadastrar_medicamento", "adiar_remedio"}
	for _, s := range leaks {
		if strings.Contains(core, s) {
			t.Errorf("núcleo social vazou regra farmacológica: %q", s)
		}
	}
	// As regras de remédio precisam estar no bloco farmacológico.
	for _, s := range []string{"REGRA FARMACOLÓGICA", "REGRA DURA DE VERDADE", "marcar_remedio_tomado", "adiar_remedio"} {
		if !strings.Contains(pharma, s) {
			t.Errorf("bloco farmacológico faltando: %q", s)
		}
	}
	// As regras sociais novas (B) precisam estar no núcleo.
	for _, s := range []string{"REAGIR, NÃO CARIMBAR", "RECONHECER E ESPELHAR DESPEDIDA", "carimbe"} {
		if !strings.Contains(core, s) {
			t.Errorf("núcleo social faltando regra B: %q", s)
		}
	}
	// O nome é substituído (sem placeholder vazado).
	if strings.Contains(core, "{{NOME}}") {
		t.Error("placeholder {{NOME}} não foi substituído no núcleo")
	}
	if !strings.Contains(core, "Joaquim") {
		t.Error("nome do idoso não apareceu no núcleo")
	}
}

// =========================================================================
// Detector de contexto de remédio (C).
// =========================================================================

func TestMedContextActive(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		medNames   []string
		hasPending bool
		want       bool
	}{
		{"dose pendente sempre ativa", "agitado até as 17h", nil, true, true},
		{"fala social pura sem remédio", "agitado até as 17h", nil, false, false},
		{"despedida pura", "até. bom dia", nil, false, false},
		{"termo forte sem med cadastrado", "preciso cadastrar um remédio", nil, false, true},
		{"termo forte com acento", "fui na farmácia hoje", nil, false, true},
		{"medicação escrito por extenso", "esqueci da medicação", nil, false, true},
		{"cita nome de remédio cadastrado", "tomei o vezicare", []string{"Vezicare"}, false, true},
		{"termo ambíguo SEM med cadastrado é social", "tomei um café com a vizinha", nil, false, false},
		{"termo ambíguo COM med cadastrado conta", "já tomei todos", []string{"Pristiq"}, false, true},
		{"jejum com med cadastrado", "tô em jejum desde cedo", []string{"Losartana"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := medContextActive(c.message, c.medNames, c.hasPending)
			if got != c.want {
				t.Errorf("medContextActive(%q, %v, pending=%v) = %v, want %v",
					c.message, c.medNames, c.hasPending, got, c.want)
			}
		})
	}
}

// =========================================================================
// Wiring condicional do bloco farmacológico (C).
// =========================================================================

func TestAppendCompanionPharmaPart(t *testing.T) {
	db := setupTestDB(t)
	a := &Agent{db: db}
	idoso := mkIdoso(t, db, "Dona Cida", 0)

	// Fala social pura, sem remédio cadastrado → bloco NÃO entra.
	parts := a.appendCompanionPharmaPart(nil, idoso, "bom dia, tudo bem por aí?")
	if len(parts) != 0 {
		t.Fatalf("fala social não deveria anexar bloco farmacológico, got %d parts", len(parts))
	}

	// Fala que toca em remédio → bloco entra.
	parts = a.appendCompanionPharmaPart(nil, idoso, "preciso cadastrar um remédio novo")
	if len(parts) != 1 || !strings.Contains(parts[0].Text, "REGRA FARMACOLÓGICA") {
		t.Fatalf("fala sobre remédio deveria anexar bloco farmacológico, got %d parts", len(parts))
	}

	// Não-idoso nunca recebe o bloco.
	comum := &User{ID: idoso.ID, Type: UserTypeComum}
	if got := a.appendCompanionPharmaPart(nil, comum, "tomei o remédio"); len(got) != 0 {
		t.Fatalf("não-idoso não deveria receber bloco farmacológico")
	}
}

// =========================================================================
// Conversores Anthropic → canônico (llm).
// =========================================================================

func TestToolDefsToLLM(t *testing.T) {
	defs := buildToolDefinitions()
	got := toolDefsToLLM(defs)
	if len(got) != len(defs) {
		t.Fatalf("toolDefsToLLM perdeu tools: got %d, want %d", len(got), len(defs))
	}
	for i, d := range got {
		if d.Name == "" {
			t.Errorf("tool %d sem nome", i)
		}
		if len(d.InputSchema) == 0 {
			t.Errorf("tool %q sem schema convertido", d.Name)
		}
		var m map[string]any
		if err := json.Unmarshal(d.InputSchema, &m); err != nil {
			t.Errorf("tool %q schema não é JSON válido: %v", d.Name, err)
		}
	}
}

func TestBuildMessagesLLM(t *testing.T) {
	hist := []ConversationMessage{
		{Role: "user", Content: "oi"},
		{Role: "assistant", Content: "olá, tudo bem?"},
		{Role: "user", Content: ""}, // vazio é pulado
	}
	msgs := buildMessagesLLM(hist, "como vai?")
	// 2 do histórico (vazio pulado) + 1 atual.
	if len(msgs) != 3 {
		t.Fatalf("esperava 3 mensagens, got %d", len(msgs))
	}
	if msgs[1].Role != llm.RoleAssistant {
		t.Errorf("segunda mensagem deveria ser assistant, got %s", msgs[1].Role)
	}
	if msgs[len(msgs)-1].Content[0].Text != "como vai?" {
		t.Errorf("mensagem atual incorreta: %q", msgs[len(msgs)-1].Content[0].Text)
	}

	// Mensagem vazia (só imagem) recebe placeholder.
	only := buildMessagesLLM(nil, "")
	if only[0].Content[0].Text != "[imagem enviada]" {
		t.Errorf("mensagem vazia deveria virar placeholder, got %q", only[0].Content[0].Text)
	}
}

// =========================================================================
// Loop DeepSeek sobre llm.ChatProvider (reaproveita toolHandlers).
// =========================================================================

// scriptedChat é um ChatProvider de teste que devolve respostas pré-roteiradas
// e guarda a última request, pra inspecionar threading de tool_result.
type scriptedChat struct {
	responses []llm.ChatResponse
	calls     int
	lastReq   llm.ChatRequest
}

func (s *scriptedChat) Name() string         { return "scripted" }
func (s *scriptedChat) SupportsTools() bool  { return true }
func (s *scriptedChat) SupportsVision() bool { return false }
func (s *scriptedChat) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	s.lastReq = req
	if s.calls >= len(s.responses) {
		return llm.ChatResponse{Content: []llm.ContentBlock{{Type: "text", Text: "(fim)"}}, StopReason: llm.StopEndTurn}, nil
	}
	r := s.responses[s.calls]
	s.calls++
	return r, nil
}

func TestRunLoopLLM_TextOnly(t *testing.T) {
	prov := &scriptedChat{responses: []llm.ChatResponse{
		{Content: []llm.ContentBlock{{Type: "text", Text: "oi, tudo bem!"}}, StopReason: llm.StopEndTurn},
	}}
	a := &Agent{}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "oi"}}}}
	got, err := a.runLoopLLM(context.Background(), &User{Name: "X"}, prov, nil, msgs, nil)
	if err != nil {
		t.Fatalf("runLoopLLM erro: %v", err)
	}
	if got != "oi, tudo bem!" {
		t.Errorf("texto incorreto: %q", got)
	}
	if prov.calls != 1 {
		t.Errorf("esperava 1 chamada, got %d", prov.calls)
	}
}

func TestRunLoopLLM_ToolThenText(t *testing.T) {
	prov := &scriptedChat{responses: []llm.ChatResponse{
		{Content: []llm.ContentBlock{{Type: "tool_use", ToolUseID: "t1", ToolName: "ferramenta_inexistente", ToolInput: json.RawMessage(`{}`)}}, StopReason: llm.StopToolUse},
		{Content: []llm.ContentBlock{{Type: "text", Text: "pronto"}}, StopReason: llm.StopEndTurn},
	}}
	a := &Agent{}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "faz algo"}}}}
	got, err := a.runLoopLLM(context.Background(), &User{Name: "X"}, prov, nil, msgs, nil)
	if err != nil {
		t.Fatalf("runLoopLLM erro: %v", err)
	}
	if got != "pronto" {
		t.Errorf("texto final incorreto: %q", got)
	}
	if prov.calls != 2 {
		t.Errorf("esperava 2 chamadas, got %d", prov.calls)
	}
	// A 2ª request precisa conter o tool_result de erro encadeado.
	last := prov.lastReq.Messages
	if len(last) < 3 {
		t.Fatalf("esperava >=3 mensagens na 2ª request, got %d", len(last))
	}
	tail := last[len(last)-1]
	if tail.Role != llm.RoleUser || len(tail.Content) == 0 || tail.Content[0].Type != "tool_result" {
		t.Fatalf("último turno deveria ser tool_result do user, got %+v", tail)
	}
	if !tail.Content[0].IsError || !strings.Contains(tail.Content[0].ToolResult, "desconhecida") {
		t.Errorf("tool_result de ferramenta desconhecida malformado: %+v", tail.Content[0])
	}
}
