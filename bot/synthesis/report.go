package synthesis

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// Synthesize chama Sonnet com o prompt longitudinal e devolve um relatorio
// acolhedor para o responsavel familiar. Le APENAS snapshots agregados +
// stats — nunca conversa crua.
//
// Default Days=14. Se 0/negativo, ajusta. Caller pode passar 7 ou 30 conforme
// necessidade. Validacao final via ValidateReportOutput.
func Synthesize(ctx context.Context, client ReportClient, in ReportInput) (ReportOutput, error) {
	if client == nil {
		return ReportOutput{}, fmt.Errorf("%w: client nil", ErrAPI)
	}
	if in.Days <= 0 {
		in.Days = 14
	}

	payload, err := json.Marshal(struct {
		Dependent         User             `json:"dependent"`
		Days              int              `json:"days"`
		Snapshots         []DailySnapshot  `json:"snapshots"`
		MedicationStats   medicationStatsW `json:"medication_stats"`
		OpenAlerts        []Alert          `json:"open_alerts"`
		DaysSinceLastTalk int              `json:"days_since_last_talk"`
	}{
		Dependent:         in.Dependent,
		Days:              in.Days,
		Snapshots:         in.Snapshots,
		MedicationStats:   toMedicationStatsW(in.MedicationStats),
		OpenAlerts:        in.OpenAlerts,
		DaysSinceLastTalk: daysSinceTalk(in.LastUserMessageAt),
	})
	if err != nil {
		return ReportOutput{}, fmt.Errorf("%w: marshal input: %v", ErrParse, err)
	}

	resp, err := client.Synthesize(ctx, llm.ReportRequest{
		System: []llm.SystemPart{
			{Text: reportSystemPromptPTBR, Cacheable: true},
			{Text: "Responda APENAS um objeto JSON valido (sem markdown, sem prefixo)."},
		},
		UserPrompt: string(payload),
		MaxTokens:  1500,
	})
	if err != nil {
		return ReportOutput{}, fmt.Errorf("%w: %v", ErrAPI, err)
	}

	raw := stripFences(resp.Text)
	if raw == "" {
		return ReportOutput{}, fmt.Errorf("%w: empty response", ErrAPI)
	}

	var out ReportOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return ReportOutput{}, fmt.Errorf("%w: %v (raw=%q)", ErrParse, err, truncate(raw, 200))
	}
	if err := ValidateReportOutput(out); err != nil {
		return ReportOutput{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return out, nil
}

// medicationStatsW eh a forma "wire" passada ao Sonnet — flatter que
// MedicationStats interno, sem campos que o modelo nao precisa.
type medicationStatsW struct {
	Scheduled    int      `json:"scheduled"`
	Taken        int      `json:"taken"`
	Missed       int      `json:"missed"`
	Skipped      int      `json:"skipped"`
	AdherencePct int      `json:"adherence_pct"`
	MissedNames  []string `json:"missed_names"`
}

func toMedicationStatsW(s MedicationStats) medicationStatsW {
	seen := map[string]bool{}
	names := make([]string, 0, len(s.MissedDoses))
	for _, d := range s.MissedDoses {
		if !seen[d.MedicationName] {
			names = append(names, d.MedicationName)
			seen[d.MedicationName] = true
		}
	}
	return medicationStatsW{
		Scheduled:    s.Scheduled,
		Taken:        s.Taken,
		Missed:       s.Missed,
		Skipped:      s.Skipped,
		AdherencePct: int(100 * s.AdherenceFrac),
		MissedNames:  names,
	}
}

// daysSinceTalk converte sql.NullTime em dias inteiros desde now. -1 = nunca.
func daysSinceTalk(t sql.NullTime) int {
	if !t.Valid {
		return -1
	}
	return int(time.Since(t.Time).Hours() / 24)
}
