package main

import (
	"testing"
	"time"
)

func TestRecordELeLLMUsage(t *testing.T) {
	db := setupTestDB(t)
	err := db.RecordLLMUsage(LLMUsageRow{
		UserID: 1, Engine: "operational", Provider: "deepseek", Model: "deepseek-v4-flash",
		InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 800, LatencyMS: 1200,
	})
	if err != nil {
		t.Fatal(err)
	}
	sums, err := db.SumLLMUsageSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := sums["deepseek|deepseek-v4-flash"]
	if !ok {
		t.Fatalf("agregado ausente: %+v", sums)
	}
	if got.InputTokens != 1000 || got.OutputTokens != 200 || got.CacheReadTokens != 800 {
		t.Fatalf("agregado errado: %+v", got)
	}
	if got.LatencyMS != 1200 {
		t.Fatalf("latencia = %d", got.LatencyMS)
	}
}

func TestSumLLMUsageAgregaPorModelo(t *testing.T) {
	db := setupTestDB(t)
	db.RecordLLMUsage(LLMUsageRow{UserID: 1, Provider: "deepseek", Model: "deepseek-v4-flash", InputTokens: 100})
	db.RecordLLMUsage(LLMUsageRow{UserID: 2, Provider: "deepseek", Model: "deepseek-v4-flash", InputTokens: 50})
	db.RecordLLMUsage(LLMUsageRow{UserID: 3, Provider: "gemini", Model: "gemini-3.6-flash", InputTokens: 7})
	sums, err := db.SumLLMUsageSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("grupos = %d, quer 2: %+v", len(sums), sums)
	}
	if sums["deepseek|deepseek-v4-flash"].InputTokens != 150 {
		t.Fatalf("soma do deepseek = %d, quer 150", sums["deepseek|deepseek-v4-flash"].InputTokens)
	}
}

func TestSumLLMUsageRespeitaJanela(t *testing.T) {
	db := setupTestDB(t)
	db.RecordLLMUsage(LLMUsageRow{UserID: 1, Provider: "deepseek", Model: "m", InputTokens: 5})
	sums, err := db.SumLLMUsageSince(time.Now().Add(time.Hour)) // janela no futuro
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("janela futura deveria vir vazia: %+v", sums)
	}
}

// Falha de contabilidade nunca pode derrubar o turno do usuario.
func TestRecordLLMUsageAceitaLinhaMinima(t *testing.T) {
	db := setupTestDB(t)
	if err := db.RecordLLMUsage(LLMUsageRow{UserID: 1}); err != nil {
		t.Fatalf("linha minima deveria gravar: %v", err)
	}
}

func TestRecordLLMUsageCarimbaCreatedAt(t *testing.T) {
	db := setupTestDB(t)
	if err := db.RecordLLMUsage(LLMUsageRow{UserID: 1, Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	sums, _ := db.SumLLMUsageSince(time.Now().Add(-time.Minute))
	if _, ok := sums["p|m"]; !ok {
		t.Fatal("CreatedAt zero deveria virar agora, e cair dentro da janela recente")
	}
}
