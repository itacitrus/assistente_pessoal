package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================== AGENDA INSIGHTS ============================
//
// AgendaInsights eh o terceiro sub-agente (Sonnet — qualidade, baixo volume)
// da camada de sintese. Diferente de Synthesize (relatorio familiar sobre um
// idoso), este analisa o PROPRIO uso de agenda do usuario logado: padroes de
// horario, recorrencia, tipos de compromisso e regularidade.
//
// Contrato de privacidade/escopo:
//   - So recebe titulos/horarios de eventos e contagem de atividade por tipo.
//   - NAO inventa dado fora do input.
//   - NAO faz juizo clinico nem diagnostico (reusa clinicalTerms na validacao).
//   - Tom util e factual, em pt-BR.

// AgendaEventLite eh a projecao slim de um evento de calendario que o
// sub-agente recebe. Sem location/attendees — so o necessario pra inferir
// padrao de horario/tipo.
type AgendaEventLite struct {
	Title  string    `json:"title"`
	Start  time.Time `json:"start"`
	AllDay bool      `json:"all_day"`
}

// ActivityCount eh a contagem de uma acao tipada no action_log do usuario no
// periodo analisado (ex: {"criar_evento": 12}).
type ActivityCount struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
}

// AgendaInsightsInput eh tudo que AgendaInsights precisa. Montado pelo caller
// (store adapter) lendo calendario + action_log. PeriodDays eh a janela
// retroativa pedida; eventos futuros (proximos 14d) entram em PastEvents +
// UpcomingEvents separados pra o modelo distinguir padrao consolidado de
// agenda futura.
type AgendaInsightsInput struct {
	UserName        string            `json:"user_name"`
	PeriodDays      int               `json:"period_days"`
	GoogleConnected bool              `json:"google_connected"`
	PastEvents      []AgendaEventLite `json:"past_events"`
	UpcomingEvents  []AgendaEventLite `json:"upcoming_events"`
	ActivityCounts  []ActivityCount   `json:"activity_counts"`
}

// HasEnoughData decide se ha dado suficiente pra valer a pena gastar Sonnet.
// Regra: precisa de Google conectado COM ao menos 1 evento, OU atividade
// agregada relevante (>= 3 acoes registradas). Caso contrario o handler
// devolve available=false sem chamar o modelo.
func (in AgendaInsightsInput) HasEnoughData() bool {
	hasEvents := in.GoogleConnected && (len(in.PastEvents)+len(in.UpcomingEvents)) > 0
	totalActivity := 0
	for _, a := range in.ActivityCounts {
		totalActivity += a.Count
	}
	return hasEvents || totalActivity >= 3
}

// AgendaInsight eh um item individual de insight. Kind eh um enum fechado
// validado em ValidateAgendaInsightsOutput.
type AgendaInsight struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Kind   string `json:"kind"` // pattern|health|social|productivity|other
}

// AgendaInsightsOutput eh o JSON validado retornado pelo sub-agente.
type AgendaInsightsOutput struct {
	Summary  string          `json:"summary"`
	Insights []AgendaInsight `json:"insights"`
}

// validInsightKinds espelha o enum publico do contrato JSON (frontend
// depende). Valor fora do set invalida o output.
var validInsightKinds = map[string]bool{
	"pattern":      true,
	"health":       true,
	"social":       true,
	"productivity": true,
	"other":        true,
}

// AgendaInsights chama Sonnet com o prompt de analise de agenda e devolve
// insights validados. NAO deve ser chamado quando in.HasEnoughData()==false —
// o caller trata esse caso com available=false sem gastar o modelo.
//
// Default PeriodDays=30. Validacao final via ValidateAgendaInsightsOutput.
func AgendaInsights(ctx context.Context, client ReportClient, in AgendaInsightsInput) (AgendaInsightsOutput, error) {
	if client == nil {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: client nil", ErrAPI)
	}
	if in.PeriodDays <= 0 {
		in.PeriodDays = 30
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: marshal input: %v", ErrParse, err)
	}

	resp, err := client.Synthesize(ctx, llm.ReportRequest{
		System: []llm.SystemPart{
			{Text: agendaInsightsSystemPromptPTBR, Cacheable: true},
			{Text: "Responda APENAS um objeto JSON valido (sem markdown, sem prefixo)."},
		},
		UserPrompt: string(payload),
		MaxTokens:  1200,
	})
	if err != nil {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: %v", ErrAPI, err)
	}

	raw := stripFences(resp.Text)
	if raw == "" {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: empty response", ErrAPI)
	}

	var out AgendaInsightsOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: %v (raw=%q)", ErrParse, err, truncate(raw, 200))
	}
	if err := ValidateAgendaInsightsOutput(out); err != nil {
		return AgendaInsightsOutput{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return out, nil
}

// ValidateAgendaInsightsOutput aplica o contrato:
//   - summary nao vazio, max 500 ch.
//   - insights entre 1 e 6 itens.
//   - cada insight: title nao vazio (max 120 ch), detail nao vazio (max 400 ch),
//     kind no enum.
//   - sem termo clinico (reusa clinicalTerms — sem juizo clinico).
func ValidateAgendaInsightsOutput(o AgendaInsightsOutput) error {
	if strings.TrimSpace(o.Summary) == "" {
		return fmt.Errorf("summary vazio")
	}
	if len(o.Summary) > 500 {
		return fmt.Errorf("summary excede 500 ch: %d", len(o.Summary))
	}
	if len(o.Insights) < 1 {
		return fmt.Errorf("insights vazio: precisa de ao menos 1 item")
	}
	if len(o.Insights) > 6 {
		return fmt.Errorf("insights excede 6: %d", len(o.Insights))
	}
	for i, ins := range o.Insights {
		if strings.TrimSpace(ins.Title) == "" {
			return fmt.Errorf("insight[%d].title vazio", i)
		}
		if len(ins.Title) > 120 {
			return fmt.Errorf("insight[%d].title excede 120 ch: %d", i, len(ins.Title))
		}
		if strings.TrimSpace(ins.Detail) == "" {
			return fmt.Errorf("insight[%d].detail vazio", i)
		}
		if len(ins.Detail) > 400 {
			return fmt.Errorf("insight[%d].detail excede 400 ch: %d", i, len(ins.Detail))
		}
		if !validInsightKinds[ins.Kind] {
			return fmt.Errorf("insight[%d].kind invalido: %q (use pattern|health|social|productivity|other)", i, ins.Kind)
		}
	}

	all := o.Summary
	for _, ins := range o.Insights {
		all += " " + ins.Title + " " + ins.Detail
	}
	lower := " " + strings.ToLower(all) + " "
	for _, term := range clinicalTerms {
		if strings.Contains(lower, term) {
			return fmt.Errorf("output contains clinical term: %q", term)
		}
	}
	return nil
}
