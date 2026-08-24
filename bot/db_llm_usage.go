package main

import (
	"fmt"
	"time"
)

// LLMUsageRow e um registro de consumo de LLM por turno/chamada.
//
// Existe porque ate a migracao para DeepSeek o projeto NAO tinha nenhuma
// contabilidade de token — so log.Printf efemeros. Sem baseline nao da pra
// provar a economia da migracao, nem detectar regressao de custo, nem saber
// se o cache de prefixo esta de fato sendo aproveitado.
type LLMUsageRow struct {
	UserID          int64
	Engine          string // "operational" | "companion" | "proactive" | "sales" | "analysis" | "report" | "vision"
	Provider        string // "deepseek" | "gemini" | "anthropic"
	Model           string
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	LatencyMS       int64
	CreatedAt       time.Time
}

// llmUsageSchema roda junto das demais migracoes (chamado de migrate()).
const llmUsageSchema = `
	CREATE TABLE IF NOT EXISTS llm_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL DEFAULT 0,
		engine TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_llm_usage_created ON llm_usage(created_at);
	CREATE INDEX IF NOT EXISTS idx_llm_usage_provider ON llm_usage(provider, model);
`

// RecordLLMUsage grava uma linha de consumo. Tolerante por design: campos
// vazios sao aceitos, porque falha de contabilidade NUNCA pode derrubar o
// turno do usuario.
func (db *DB) RecordLLMUsage(r LLMUsageRow) error {
	ts := r.CreatedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	_, err := db.conn.Exec(`
		INSERT INTO llm_usage
			(user_id, engine, provider, model, input_tokens, output_tokens, cache_read_tokens, latency_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.Engine, r.Provider, r.Model,
		r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.LatencyMS, ts.UTC())
	if err != nil {
		return fmt.Errorf("record llm usage: %w", err)
	}
	return nil
}

// SumLLMUsageSince agrega o consumo por provider|model desde `since`.
// A chave do mapa e provider + "|" + model.
func (db *DB) SumLLMUsageSince(since time.Time) (map[string]LLMUsageRow, error) {
	rows, err := db.conn.Query(`
		SELECT provider, model,
		       SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(latency_ms)
		FROM llm_usage
		WHERE created_at >= ?
		GROUP BY provider, model`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("sum llm usage: %w", err)
	}
	defer rows.Close()

	out := map[string]LLMUsageRow{}
	for rows.Next() {
		var r LLMUsageRow
		if err := rows.Scan(&r.Provider, &r.Model,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.LatencyMS); err != nil {
			return nil, fmt.Errorf("scan llm usage: %w", err)
		}
		out[r.Provider+"|"+r.Model] = r
	}
	return out, rows.Err()
}
