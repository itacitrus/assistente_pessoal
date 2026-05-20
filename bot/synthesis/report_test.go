package synthesis

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// fakeReport implementa ReportClient com saida fixa.
type fakeReport struct {
	out      ReportOutput
	err      error
	rawOver  string
	captured llm.ReportRequest
}

func (f *fakeReport) Synthesize(_ context.Context, req llm.ReportRequest) (llm.ReportResponse, error) {
	f.captured = req
	if f.err != nil {
		return llm.ReportResponse{}, f.err
	}
	if f.rawOver != "" {
		return llm.ReportResponse{Text: f.rawOver}, nil
	}
	b, _ := json.Marshal(f.out)
	return llm.ReportResponse{Text: string(b)}, nil
}

func TestSynthesize_ProducesTendencyFromSnapshots(t *testing.T) {
	snaps := genTrendSnapshots(14, "up")
	client := &fakeReport{out: ReportOutput{
		Tendencia:        "melhorando",
		NivelPreocupacao: "tranquilo",
		Comparacao:       "humor 4.2 ultimos 7d vs 3.1 anteriores",
		Resumo:           "Ela tem estado mais animada essa semana.",
	}}
	out, err := Synthesize(context.Background(), client, ReportInput{
		Dependent: User{ID: 1, Name: "Antonia"},
		Days:      14,
		Snapshots: snaps,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if out.Tendencia != "melhorando" {
		t.Errorf("expected melhorando, got %s", out.Tendencia)
	}
}

func TestSynthesize_HandlesEmptyWindow(t *testing.T) {
	client := &fakeReport{out: ReportOutput{
		Tendencia:        "indeterminado",
		NivelPreocupacao: "tranquilo",
		Resumo:           "Sem dados suficientes nesse periodo.",
	}}
	out, err := Synthesize(context.Background(), client, ReportInput{
		Dependent: User{ID: 1, Name: "Antonia"},
		Days:      14,
		Snapshots: nil,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if out.Tendencia != "indeterminado" {
		t.Errorf("expected indeterminado, got %s", out.Tendencia)
	}
}

func TestSynthesize_DefaultsDaysTo14(t *testing.T) {
	client := &fakeReport{out: ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "Tudo certo.",
	}}
	if _, err := Synthesize(context.Background(), client, ReportInput{
		Dependent: User{ID: 1, Name: "X"},
		Days:      0,
	}); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	// O payload tem "days":14
	if !strings.Contains(client.captured.UserPrompt, `"days":14`) {
		t.Errorf("expected days defaulted to 14: %s", client.captured.UserPrompt)
	}
}

func TestSynthesize_PropagatesValidationError(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           `Ela disse "me sinto sozinha" essa semana.`,
	}
	client := &fakeReport{out: bad}
	_, err := Synthesize(context.Background(), client, ReportInput{
		Dependent: User{ID: 1, Name: "X"},
		Days:      14,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestValidateReportOutput_RejectsQuote(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           `Ela disse "me sinto sozinha" essa semana.`,
	}
	if err := ValidateReportOutput(bad); err == nil {
		t.Fatal("expected privacy error")
	}
}

func TestValidateReportOutput_RejectsClinical(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "piorando",
		NivelPreocupacao: "atencao",
		Resumo:           "Apresenta quadro de depressao leve essa semana.",
	}
	if err := ValidateReportOutput(bad); err == nil {
		t.Fatal("expected clinical-term error")
	}
}

func TestValidateReportOutput_AcceptsLongitudinal(t *testing.T) {
	good := ReportOutput{
		Tendencia:        "piorando",
		Comparacao:       "humor 2.5 ultimos 7d vs 3.5 anteriores",
		HumorRecente:     "tem aparecido o tema saudade nas ultimas conversas",
		Resumo:           "Tem sido um periodo um pouco mais quieto. Vale uma atencao extra.",
		NivelPreocupacao: "atencao",
		RecomendacoesCarinhosas: []string{
			"talvez ligue pra ela hoje, ela tem aparecido mais quieta",
		},
	}
	if err := ValidateReportOutput(good); err != nil {
		t.Fatalf("expected accept, got: %v", err)
	}
}

func TestValidateReportOutput_RejectsInvalidTendencia(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "ruim",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
	}
	if err := ValidateReportOutput(bad); err == nil {
		t.Fatal("expected error for invalid tendencia")
	}
}

func TestValidateReportOutput_RejectsTooManyRecomendacoes(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "x.",
		RecomendacoesCarinhosas: []string{
			"a", "b", "c", "d",
		},
	}
	if err := ValidateReportOutput(bad); err == nil {
		t.Fatal("expected error for >3 recomendacoes")
	}
}

func TestValidateReportOutput_RejectsEmptyResumo(t *testing.T) {
	bad := ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "   ",
	}
	if err := ValidateReportOutput(bad); err == nil {
		t.Fatal("expected error for empty resumo")
	}
}

func TestSynthesize_DaysSinceLastTalkNeg1WhenNoTalk(t *testing.T) {
	client := &fakeReport{out: ReportOutput{
		Tendencia:        "indeterminado",
		NivelPreocupacao: "tranquilo",
		Resumo:           "Sem dados.",
	}}
	in := ReportInput{
		Dependent:         User{ID: 1, Name: "X"},
		Days:              7,
		LastUserMessageAt: sql.NullTime{Valid: false},
	}
	if _, err := Synthesize(context.Background(), client, in); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !strings.Contains(client.captured.UserPrompt, `"days_since_last_talk":-1`) {
		t.Errorf("expected days_since_last_talk:-1, got: %s", client.captured.UserPrompt)
	}
}

func TestSynthesize_PropagatesAPIError(t *testing.T) {
	client := &fakeReport{err: errors.New("boom")}
	_, err := Synthesize(context.Background(), client, ReportInput{Dependent: User{ID: 1}, Days: 7})
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("expected ErrAPI, got %v", err)
	}
}

func TestSynthesize_RejectsMalformedJSON(t *testing.T) {
	client := &fakeReport{rawOver: "not json"}
	_, err := Synthesize(context.Background(), client, ReportInput{Dependent: User{ID: 1}, Days: 7})
	if !errors.Is(err, ErrParse) {
		t.Fatalf("expected ErrParse, got %v", err)
	}
}

func TestSynthesize_HandlesMarkdownFences(t *testing.T) {
	good := ReportOutput{
		Tendencia:        "estavel",
		NivelPreocupacao: "tranquilo",
		Resumo:           "Tudo certo.",
	}
	b, _ := json.Marshal(good)
	wrapped := "```json\n" + string(b) + "\n```"
	client := &fakeReport{rawOver: wrapped}
	out, err := Synthesize(context.Background(), client, ReportInput{Dependent: User{ID: 1}, Days: 7})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if out.Tendencia != "estavel" {
		t.Errorf("expected estavel, got %s", out.Tendencia)
	}
}

func TestSynthesize_NilClient(t *testing.T) {
	_, err := Synthesize(context.Background(), nil, ReportInput{Dependent: User{ID: 1}, Days: 7})
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("expected ErrAPI, got %v", err)
	}
}

func TestToMedicationStatsW_DedupesNames(t *testing.T) {
	in := MedicationStats{
		Scheduled:     14,
		Taken:         10,
		Missed:        4,
		AdherenceFrac: 0.71,
		MissedDoses: []MissedDose{
			{MedicationName: "losartana"},
			{MedicationName: "losartana"},
			{MedicationName: "metformina"},
		},
	}
	got := toMedicationStatsW(in)
	if got.AdherencePct != 71 {
		t.Errorf("expected 71, got %d", got.AdherencePct)
	}
	if len(got.MissedNames) != 2 {
		t.Errorf("expected 2 unique names, got %v", got.MissedNames)
	}
}

func TestDaysSinceTalk(t *testing.T) {
	if got := daysSinceTalk(sql.NullTime{Valid: false}); got != -1 {
		t.Errorf("expected -1, got %d", got)
	}
	t3 := time.Now().Add(-3 * 24 * time.Hour)
	if got := daysSinceTalk(sql.NullTime{Time: t3, Valid: true}); got < 2 || got > 4 {
		t.Errorf("expected ~3, got %d", got)
	}
}

// genTrendSnapshots gera snapshots ordenados DESC por data com tendencia
// crescente ("up") ou decrescente ("down") nos scores.
func genTrendSnapshots(n int, dir string) []DailySnapshot {
	var snaps []DailySnapshot
	now := time.Now().Truncate(24 * time.Hour)
	for i := 0; i < n; i++ {
		base := 3
		if dir == "up" {
			base = 2 + i/4 // 2,2,2,2, 3,3,3,3, 4,...
			if base > 5 {
				base = 5
			}
		} else if dir == "down" {
			base = 5 - i/4
			if base < 1 {
				base = 1
			}
		}
		snaps = append(snaps, DailySnapshot{
			UserID:             1,
			SnapshotDate:       now.Add(-time.Duration(i) * 24 * time.Hour),
			HumorScore:         base,
			EnergiaScore:       base,
			SociabilidadeScore: base,
			AutocuidadoScore:   base,
			Confidence:         3,
			NMessages:          5,
		})
	}
	return snaps
}
