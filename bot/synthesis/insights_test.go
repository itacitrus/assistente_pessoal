package synthesis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// stubReport eh um ReportClient de teste com resposta/erro fixos.
type stubReport struct {
	text string
	err  error
}

func (s stubReport) Synthesize(_ context.Context, _ llm.ReportRequest) (llm.ReportResponse, error) {
	if s.err != nil {
		return llm.ReportResponse{}, s.err
	}
	return llm.ReportResponse{Text: s.text}, nil
}

func validInsightsJSON() string {
	return `{"summary":"Voce concentra compromissos nas tardes.","insights":[` +
		`{"title":"Tardes movimentadas","detail":"A maioria dos compromissos cai entre 14h e 18h.","kind":"pattern"},` +
		`{"title":"Consultas regulares","detail":"Voce mantem consultas com frequencia.","kind":"health"}]}`
}

func sampleInput() AgendaInsightsInput {
	return AgendaInsightsInput{
		UserName:        "Maria",
		PeriodDays:      30,
		GoogleConnected: true,
		PastEvents: []AgendaEventLite{
			{Title: "Consulta", Start: time.Now().Add(-48 * time.Hour)},
		},
		ActivityCounts: []ActivityCount{{Action: "criar_evento", Count: 5}},
	}
}

func TestAgendaInsights_HappyPath(t *testing.T) {
	out, err := AgendaInsights(context.Background(), stubReport{text: validInsightsJSON()}, sampleInput())
	if err != nil {
		t.Fatalf("err inesperado: %v", err)
	}
	if out.Summary == "" {
		t.Fatal("summary vazio")
	}
	if len(out.Insights) != 2 {
		t.Fatalf("insights = %d, want 2", len(out.Insights))
	}
	if out.Insights[0].Kind != "pattern" {
		t.Fatalf("kind = %q", out.Insights[0].Kind)
	}
}

func TestAgendaInsights_StripsFences(t *testing.T) {
	wrapped := "```json\n" + validInsightsJSON() + "\n```"
	out, err := AgendaInsights(context.Background(), stubReport{text: wrapped}, sampleInput())
	if err != nil {
		t.Fatalf("err inesperado: %v", err)
	}
	if len(out.Insights) != 2 {
		t.Fatalf("insights = %d", len(out.Insights))
	}
}

func TestAgendaInsights_NilClient(t *testing.T) {
	_, err := AgendaInsights(context.Background(), nil, sampleInput())
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("err = %v, want ErrAPI", err)
	}
}

func TestAgendaInsights_APIError(t *testing.T) {
	_, err := AgendaInsights(context.Background(), stubReport{err: errors.New("boom")}, sampleInput())
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("err = %v, want ErrAPI", err)
	}
}

func TestAgendaInsights_ParseError(t *testing.T) {
	_, err := AgendaInsights(context.Background(), stubReport{text: "nao eh json"}, sampleInput())
	if !errors.Is(err, ErrParse) {
		t.Fatalf("err = %v, want ErrParse", err)
	}
}

func TestAgendaInsights_EmptyResponse(t *testing.T) {
	_, err := AgendaInsights(context.Background(), stubReport{text: "   "}, sampleInput())
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("err = %v, want ErrAPI", err)
	}
}

func TestAgendaInsights_ValidationRejectsBadKind(t *testing.T) {
	bad := `{"summary":"ok","insights":[{"title":"x","detail":"y","kind":"banana"}]}`
	_, err := AgendaInsights(context.Background(), stubReport{text: bad}, sampleInput())
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestAgendaInsights_DefaultsPeriodDays(t *testing.T) {
	in := sampleInput()
	in.PeriodDays = 0
	// Nao da pra inspecionar o payload diretamente, mas garante que nao quebra.
	if _, err := AgendaInsights(context.Background(), stubReport{text: validInsightsJSON()}, in); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestValidateAgendaInsightsOutput(t *testing.T) {
	mkInsights := func(n int) []AgendaInsight {
		out := make([]AgendaInsight, n)
		for i := range out {
			out[i] = AgendaInsight{Title: "t", Detail: "d", Kind: "pattern"}
		}
		return out
	}

	tests := []struct {
		name    string
		out     AgendaInsightsOutput
		wantErr bool
	}{
		{"ok", AgendaInsightsOutput{Summary: "ok", Insights: mkInsights(3)}, false},
		{"summary vazio", AgendaInsightsOutput{Summary: "  ", Insights: mkInsights(1)}, true},
		{"summary longo", AgendaInsightsOutput{Summary: strings.Repeat("a", 501), Insights: mkInsights(1)}, true},
		{"sem insights", AgendaInsightsOutput{Summary: "ok", Insights: nil}, true},
		{"insights demais", AgendaInsightsOutput{Summary: "ok", Insights: mkInsights(7)}, true},
		{"title vazio", AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: "", Detail: "d", Kind: "pattern"}}}, true},
		{"detail vazio", AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: "t", Detail: "", Kind: "pattern"}}}, true},
		{"kind invalido", AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: "t", Detail: "d", Kind: "x"}}}, true},
		{"title longo", AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: strings.Repeat("a", 121), Detail: "d", Kind: "pattern"}}}, true},
		{"detail longo", AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: "t", Detail: strings.Repeat("a", 401), Kind: "pattern"}}}, true},
		{"termo clinico", AgendaInsightsOutput{Summary: "voce tem depressao", Insights: mkInsights(1)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgendaInsightsOutput(tc.out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAgendaInsightsOutput_AllKinds(t *testing.T) {
	for kind := range validInsightKinds {
		out := AgendaInsightsOutput{Summary: "ok", Insights: []AgendaInsight{{Title: "t", Detail: "d", Kind: kind}}}
		if err := ValidateAgendaInsightsOutput(out); err != nil {
			t.Fatalf("kind %q rejeitado: %v", kind, err)
		}
	}
}

func TestHasEnoughData(t *testing.T) {
	tests := []struct {
		name string
		in   AgendaInsightsInput
		want bool
	}{
		{"google + eventos", AgendaInsightsInput{GoogleConnected: true, PastEvents: []AgendaEventLite{{Title: "x"}}}, true},
		{"google sem eventos, atividade alta", AgendaInsightsInput{GoogleConnected: true, ActivityCounts: []ActivityCount{{Action: "a", Count: 5}}}, true},
		{"sem google, atividade alta", AgendaInsightsInput{ActivityCounts: []ActivityCount{{Action: "a", Count: 3}}}, true},
		{"sem google, atividade baixa", AgendaInsightsInput{ActivityCounts: []ActivityCount{{Action: "a", Count: 2}}}, false},
		{"vazio", AgendaInsightsInput{}, false},
		{"google conectado mas sem eventos e sem atividade", AgendaInsightsInput{GoogleConnected: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.HasEnoughData(); got != tc.want {
				t.Fatalf("HasEnoughData = %v, want %v", got, tc.want)
			}
		})
	}
}
