package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// fakeAnalysis implementa AnalysisClient com saida fixa.
type fakeAnalysis struct {
	out      SnapshotOutput
	err      error
	rawOver  string // se nao vazio, usa esse no lugar do JSON marshalado
	captured llm.AnalysisRequest
}

func (f *fakeAnalysis) Analyze(_ context.Context, req llm.AnalysisRequest) (llm.AnalysisResponse, error) {
	f.captured = req
	if f.err != nil {
		return llm.AnalysisResponse{}, f.err
	}
	if f.rawOver != "" {
		return llm.AnalysisResponse{JSON: json.RawMessage(f.rawOver)}, nil
	}
	b, _ := json.Marshal(f.out)
	return llm.AnalysisResponse{JSON: b}, nil
}

func TestWriteSnapshot_InfersFromConversation(t *testing.T) {
	out := SnapshotOutput{
		HumorScore:         3,
		HumorNuance:        "tom estavel",
		EnergiaScore:       3,
		SociabilidadeScore: 4,
		AutocuidadoScore:   4,
		SinaisObservados:   []string{"mencionou caminhada matinal"},
		EventosDia:         []string{"tomou pressao com a vizinha"},
		Confidence:         3,
	}
	client := &fakeAnalysis{out: out}

	in := SnapshotInput{
		User:        User{ID: 1, Name: "Antonia"},
		Date:        time.Now(),
		NewMessages: []ConversationMessage{{Role: "user", Text: "fui caminhar hoje", Timestamp: time.Now()}},
	}
	got, err := WriteSnapshot(context.Background(), client, in)
	if err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if got.HumorScore != 3 || got.Confidence != 3 {
		t.Errorf("expected scores echoed, got %+v", got)
	}
	// Confirma que payload contem date no formato YYYY-MM-DD.
	if !strings.Contains(client.captured.UserPrompt, in.Date.Format("2006-01-02")) {
		t.Errorf("user prompt missing date: %s", client.captured.UserPrompt)
	}
}

func TestWriteSnapshot_RejectsLiteralQuotes(t *testing.T) {
	bad := SnapshotOutput{
		HumorScore:       3,
		Confidence:       3,
		SinaisObservados: []string{`ela disse "to me sentindo um lixo" hoje`},
	}
	client := &fakeAnalysis{out: bad}
	_, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if err == nil {
		t.Fatal("expected error rejecting literal quote")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "literal-looking quotation") {
		t.Errorf("expected privacy violation msg, got: %v", err)
	}
}

func TestWriteSnapshot_RejectsClinicalTerms(t *testing.T) {
	bad := SnapshotOutput{
		HumorScore:       2,
		Confidence:       3,
		SinaisObservados: []string{"apresenta sintomas de depressao leve"},
	}
	client := &fakeAnalysis{out: bad}
	_, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "clinical term") {
		t.Errorf("expected clinical-term msg, got: %v", err)
	}
}

func TestWriteSnapshot_RejectsFofoca(t *testing.T) {
	bad := SnapshotOutput{
		HumorScore: 2,
		Confidence: 3,
		EventosDia: []string{"brigou com a filha sobre dinheiro"},
	}
	client := &fakeAnalysis{out: bad}
	_, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "fofoca") {
		t.Errorf("expected fofoca msg, got: %v", err)
	}
}

func TestWriteSnapshot_AcceptsObservational(t *testing.T) {
	good := SnapshotOutput{
		HumorScore:         3,
		HumorNuance:        "saudosa do filho",
		EnergiaScore:       3,
		SociabilidadeScore: 4,
		AutocuidadoScore:   5,
		SinaisObservados:   []string{"mencionou tontura matinal"},
		EventosDia:         []string{"tomou pressao com a vizinha enfermeira"},
		Confidence:         3,
	}
	if err := ValidateSnapshotOutput(good); err != nil {
		t.Fatalf("expected accept, got: %v", err)
	}
}

func TestWriteSnapshot_AcceptsZeroScoresLowConfidence(t *testing.T) {
	good := SnapshotOutput{Confidence: 1}
	if err := ValidateSnapshotOutput(good); err != nil {
		t.Fatalf("expected accept, got: %v", err)
	}
}

func TestWriteSnapshot_SafetyAlertWhenCompanionMissed(t *testing.T) {
	out := SnapshotOutput{
		HumorScore: 2,
		Confidence: 3,
		EventosDia: []string{"queixa de dor no peito apos almoco"},
		SafetyAlertNeeded: &SafetyAlert{
			Severity:    "warn",
			Category:    "medico_fisico",
			Reason:      "queixa de dor toracica recorrente",
			Recommended: "considerar avaliacao medica hoje",
		},
	}
	client := &fakeAnalysis{out: out}
	in := SnapshotInput{
		User: User{ID: 1, Name: "Antonia"},
		Date: time.Now(),
		NewMessages: []ConversationMessage{
			{Role: "user", Text: "to com uma dor no peito chata desde o almoco", Timestamp: time.Now()},
		},
		AlertasGerados: nil,
	}
	got, err := WriteSnapshot(context.Background(), client, in)
	if err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if got.SafetyAlertNeeded == nil {
		t.Fatal("expected SafetyAlertNeeded != nil")
	}
	if got.SafetyAlertNeeded.Category != "medico_fisico" {
		t.Errorf("expected medico_fisico, got %q", got.SafetyAlertNeeded.Category)
	}
}

func TestWriteSnapshot_SafetyAlert_RequiresValidCategory(t *testing.T) {
	bad := SnapshotOutput{
		HumorScore: 3,
		Confidence: 3,
		SafetyAlertNeeded: &SafetyAlert{
			Severity: "warn",
			Category: "intriga_familiar", // INVALID
			Reason:   "razao qualquer",
		},
	}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected validation error for invalid category")
	} else if !strings.Contains(err.Error(), "category invalido") {
		t.Errorf("expected category-invalid msg, got: %v", err)
	}
}

func TestWriteSnapshot_SafetyAlert_RequiresValidSeverity(t *testing.T) {
	bad := SnapshotOutput{
		HumorScore: 3,
		Confidence: 3,
		SafetyAlertNeeded: &SafetyAlert{
			Severity: "muito_grave", // INVALID
			Category: "medico_fisico",
			Reason:   "x",
		},
	}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected validation error for invalid severity")
	}
}

func TestWriteSnapshot_HandlesMarkdownFences(t *testing.T) {
	good := SnapshotOutput{HumorScore: 3, Confidence: 3}
	b, _ := json.Marshal(good)
	wrapped := "```json\n" + string(b) + "\n```"
	client := &fakeAnalysis{rawOver: wrapped}

	got, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if err != nil {
		t.Fatalf("WriteSnapshot with fences: %v", err)
	}
	if got.HumorScore != 3 {
		t.Errorf("expected fences to be stripped, got: %+v", got)
	}
}

func TestWriteSnapshot_PropagatesAPIError(t *testing.T) {
	client := &fakeAnalysis{err: errors.New("boom")}
	_, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("expected ErrAPI, got %v", err)
	}
}

func TestWriteSnapshot_RejectsMalformedJSON(t *testing.T) {
	client := &fakeAnalysis{rawOver: "{not json"}
	_, err := WriteSnapshot(context.Background(), client, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if !errors.Is(err, ErrParse) {
		t.Fatalf("expected ErrParse, got %v", err)
	}
}

func TestWriteSnapshot_NilClient(t *testing.T) {
	_, err := WriteSnapshot(context.Background(), nil, SnapshotInput{User: User{ID: 1}, Date: time.Now()})
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("expected ErrAPI for nil client, got %v", err)
	}
}

func TestValidateSnapshotOutput_RejectsTooManyItems(t *testing.T) {
	bad := SnapshotOutput{
		Confidence:       3,
		SinaisObservados: []string{"a", "b", "c", "d", "e", "f"}, // 6
	}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected error for >5 sinais")
	}
}

func TestValidateSnapshotOutput_RejectsTooLongItem(t *testing.T) {
	bad := SnapshotOutput{
		Confidence:       3,
		SinaisObservados: []string{strings.Repeat("a", 200)},
	}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected error for item > 100 ch")
	}
}

func TestValidateSnapshotOutput_RejectsScoreOutOfRange(t *testing.T) {
	bad := SnapshotOutput{Confidence: 3, HumorScore: 7}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected error for score=7")
	}
}

func TestValidateSnapshotOutput_RejectsConfidenceOutOfRange(t *testing.T) {
	bad := SnapshotOutput{Confidence: 0}
	if err := ValidateSnapshotOutput(bad); err == nil {
		t.Fatal("expected error for confidence=0")
	}
	bad2 := SnapshotOutput{Confidence: 6}
	if err := ValidateSnapshotOutput(bad2); err == nil {
		t.Fatal("expected error for confidence=6")
	}
}

func TestSnapshotOutput_ToDailySnapshot(t *testing.T) {
	o := SnapshotOutput{
		HumorScore:       4,
		HumorNuance:      "leve",
		EnergiaScore:     3,
		SinaisObservados: []string{"x"},
		Confidence:       3,
	}
	when := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	snap := o.ToDailySnapshot(42, when, SnapshotCounts{NConversations: 1, NMessages: 5, DurationMinutes: 12})
	if snap.UserID != 42 {
		t.Errorf("UserID: %d", snap.UserID)
	}
	if snap.HumorScore != 4 {
		t.Errorf("HumorScore: %d", snap.HumorScore)
	}
	if snap.NMessages != 5 || snap.DurationMinutes != 12 {
		t.Errorf("counts not propagated: %+v", snap)
	}
}

func TestStripFences_HandlesNoFence(t *testing.T) {
	s := stripFences(`{"x":1}`)
	if s != `{"x":1}` {
		t.Errorf("got %q", s)
	}
}

func TestStripFences_HandlesJsonFence(t *testing.T) {
	s := stripFences("```json\n{\"x\":1}\n```")
	if s != `{"x":1}` {
		t.Errorf("got %q", s)
	}
}

func TestStripFences_HandlesPlainFence(t *testing.T) {
	s := stripFences("```\n{\"x\":1}\n```")
	if s != `{"x":1}` {
		t.Errorf("got %q", s)
	}
}
