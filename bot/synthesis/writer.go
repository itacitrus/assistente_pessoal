package synthesis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// WriteSnapshot chama Haiku 4.5 com o prompt do snapshot writer e devolve
// SnapshotOutput validado. Caller persiste via DB.UpsertPsychSnapshot.
//
// Idempotencia: caller eh responsavel por nao racing no mesmo (user, date)
// — UPSERT em DB resolve writes concorrentes deterministicamente. WriteSnapshot
// nao toca em DB; soh produz output puro.
//
// Erros sao envolvidos por ErrParse / ErrValidation / ErrAPI pra caller
// usar errors.Is.
func WriteSnapshot(ctx context.Context, client AnalysisClient, in SnapshotInput) (SnapshotOutput, error) {
	if client == nil {
		return SnapshotOutput{}, fmt.Errorf("%w: client nil", ErrAPI)
	}

	// Monta payload JSON que vira a user message. System prompt fala
	// SOBRE este payload — eles vivem juntos.
	payload, err := json.Marshal(struct {
		User                   User                  `json:"user"`
		Date                   string                `json:"date"`
		PreviousSnapshot       *DailySnapshot        `json:"previous_snapshot,omitempty"`
		NewMessages            []ConversationMessage `json:"new_messages"`
		MedicationsTakenToday  []MedicationIntake    `json:"medications_taken_today"`
		MedicationsMissedToday []MedicationIntake    `json:"medications_missed_today"`
		SocialContextRiskMemos []Memory              `json:"social_context_risk_memos"`
		AlertasGerados         []Alert               `json:"alertas_gerados_hoje"`
	}{
		User:                   in.User,
		Date:                   in.Date.Format("2006-01-02"),
		PreviousSnapshot:       in.PreviousSnapshot,
		NewMessages:            in.NewMessages,
		MedicationsTakenToday:  in.MedicationsTakenToday,
		MedicationsMissedToday: in.MedicationsMissedToday,
		SocialContextRiskMemos: in.SocialContextRiskMemos,
		AlertasGerados:         in.AlertasGerados,
	})
	if err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: marshal input: %v", ErrParse, err)
	}

	resp, err := client.Analyze(ctx, llm.AnalysisRequest{
		System: []llm.SystemPart{
			{Text: writerSystemPromptPTBR, Cacheable: true},
		},
		UserPrompt: string(payload),
		SchemaName: "psych_state_v1",
		SchemaJSON: json.RawMessage(writerOutputSchema),
		MaxTokens:  1024,
	})
	if err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrAPI, err)
	}

	raw := stripFences(string(resp.JSON))
	if raw == "" {
		return SnapshotOutput{}, fmt.Errorf("%w: empty response", ErrAPI)
	}

	var out SnapshotOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: %v (raw=%q)", ErrParse, err, truncate(raw, 200))
	}
	if err := ValidateSnapshotOutput(out); err != nil {
		return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return out, nil
}
