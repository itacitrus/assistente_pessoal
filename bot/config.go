// bot/config.go
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURI  string
	AnthropicAPIKey    string
	AssemblyAIAPIKey   string
	EncryptionKey      string
	TranscriptionURL   string

	DefaultDailySummaryTime   string
	DefaultWeeklySummaryDay   string
	DefaultWeeklySummaryTime  string
	DefaultReminderBefore     time.Duration
	DefaultAutoConfirmTimeout time.Duration

	// Provider por papel (migracao DeepSeek). Cada um aceita
	// "deepseek" | "gemini" | "anthropic"; ausente = "deepseek".
	//
	// A separacao OPERACIONAL x CLINICO nao e por tipo de agente e sim por
	// SENSIBILIDADE do usuario (ver llm_router.go): quem PODERIA tocar dado
	// de saude num turno vai pro clinico. Hoje os dois apontam pro DeepSeek;
	// deixar parametrizado torna a troca do caminho clinico uma edicao de
	// variavel, nao um redesenho.
	LLMProviderOperacional string
	LLMProviderClinico     string
	LLMProviderSales       string
	LLMProviderAnalysis    string
	LLMProviderReport      string
	LLMProviderVision      string

	// LLMProviderCompanion e o nome legado de LLM_PROVIDER_CLINICO, mantido
	// por compatibilidade com .env antigo.
	LLMProviderCompanion string

	DeepSeekAPIKey  string
	DeepSeekBaseURL string // vazio = https://api.deepseek.com (o /v1 e legado)
	DeepSeekModel   string // default deepseek-v4-flash

	GeminiAPIKey string
	GeminiModel  string // default gemini-3.6-flash

	// Output-guard (contrato de precisão temporal, P1).
	// OUTPUT_GUARD_MODE: off | log | enforce. Default "log" — audita
	// violações sem alterar a resposta; promover a "enforce" só após o
	// aceite (7 dias com zero enforcement I1/I4 nos audits).
	// GUARD_ENFORCE_PHONES: canário — telefones (CSV) que recebem enforce
	// mesmo em modo log (admin primeiro).
	OutputGuardMode    string
	GuardEnforcePhones []string

	// AdminPhones recebe alertas operacionais (ex: saldo/credencial de LLM
	// recusada). Fonte: ADMIN_PHONES (CSV) com fallback ADMIN_PHONE.
	AdminPhones []string
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURI:  os.Getenv("GOOGLE_REDIRECT_URI"),
		AnthropicAPIKey:    os.Getenv("ANTHROPIC_API_KEY"),
		AssemblyAIAPIKey:   os.Getenv("ASSEMBLYAI_API_KEY"),
		EncryptionKey:      os.Getenv("ENCRYPTION_KEY"),
		TranscriptionURL:   os.Getenv("TRANSCRIPTION_URL"),

		DefaultDailySummaryTime:  envOrDefault("DEFAULT_DAILY_SUMMARY_TIME", "07:00"),
		DefaultWeeklySummaryDay:  envOrDefault("DEFAULT_WEEKLY_SUMMARY_DAY", "sunday"),
		DefaultWeeklySummaryTime: envOrDefault("DEFAULT_WEEKLY_SUMMARY_TIME", "20:00"),
	}

	if cfg.TranscriptionURL == "" {
		cfg.TranscriptionURL = "http://localhost:8000"
	}
	if cfg.GoogleRedirectURI == "" {
		cfg.GoogleRedirectURI = "http://localhost:8080/assistente/oauth/callback"
	}

	var err error
	cfg.DefaultReminderBefore, err = time.ParseDuration(envOrDefault("DEFAULT_REMINDER_BEFORE", "1h"))
	if err != nil {
		return nil, fmt.Errorf("invalid DEFAULT_REMINDER_BEFORE: %w", err)
	}
	cfg.DefaultAutoConfirmTimeout, err = time.ParseDuration(envOrDefault("DEFAULT_AUTO_CONFIRM_TIMEOUT", "2h"))
	if err != nil {
		return nil, fmt.Errorf("invalid DEFAULT_AUTO_CONFIRM_TIMEOUT: %w", err)
	}

	// Migracao DeepSeek: provider por papel, default deepseek.
	cfg.DeepSeekAPIKey = os.Getenv("DEEPSEEK_API_KEY")
	cfg.DeepSeekBaseURL = strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	cfg.DeepSeekModel = strings.TrimSpace(envOrDefault("DEEPSEEK_MODEL", "deepseek-v4-flash"))
	cfg.GeminiAPIKey = os.Getenv("GEMINI_API_KEY")
	cfg.GeminiModel = strings.TrimSpace(envOrDefault("GEMINI_MODEL", "gemini-3.6-flash"))

	// LLM_PROVIDER_COMPANION e o nome legado do papel clinico. Se estiver no
	// .env e LLM_PROVIDER_CLINICO nao, ele vale — evita que um .env antigo
	// mude de comportamento em silencio no deploy.
	cfg.LLMProviderCompanion = normProvider(os.Getenv("LLM_PROVIDER_COMPANION"))
	clinicoDefault := "deepseek"
	if cfg.LLMProviderCompanion != "" && os.Getenv("LLM_PROVIDER_CLINICO") == "" {
		clinicoDefault = cfg.LLMProviderCompanion
	}

	cfg.LLMProviderOperacional = normProvider(envOrDefault("LLM_PROVIDER_OPERACIONAL", "deepseek"))
	cfg.LLMProviderClinico = normProvider(envOrDefault("LLM_PROVIDER_CLINICO", clinicoDefault))
	cfg.LLMProviderSales = normProvider(envOrDefault("LLM_PROVIDER_SALES", "deepseek"))
	cfg.LLMProviderAnalysis = normProvider(envOrDefault("LLM_PROVIDER_ANALYSIS", "deepseek"))
	cfg.LLMProviderReport = normProvider(envOrDefault("LLM_PROVIDER_REPORT", "deepseek"))
	// Visao na DeepSeek vive num modelo separado (deepseek-v4-flash-vision-exp):
	// o modelo de chat recusa imagem com HTTP 400.
	cfg.LLMProviderVision = normProvider(envOrDefault("LLM_PROVIDER_VISION", "deepseek-vision"))

	roles := map[string]string{
		"LLM_PROVIDER_OPERACIONAL": cfg.LLMProviderOperacional,
		"LLM_PROVIDER_CLINICO":     cfg.LLMProviderClinico,
		"LLM_PROVIDER_SALES":       cfg.LLMProviderSales,
		"LLM_PROVIDER_ANALYSIS":    cfg.LLMProviderAnalysis,
		"LLM_PROVIDER_REPORT":      cfg.LLMProviderReport,
		"LLM_PROVIDER_VISION":      cfg.LLMProviderVision,
	}
	for env, prov := range roles {
		if !validProvider(prov) {
			return nil, fmt.Errorf("%s invalido %q (use deepseek|deepseek-vision|gemini|anthropic)", env, prov)
		}
	}

	cfg.AdminPhones = resolveAdminPhones()

	cfg.OutputGuardMode = strings.ToLower(strings.TrimSpace(envOrDefault("OUTPUT_GUARD_MODE", "log")))
	if cfg.OutputGuardMode != "off" && cfg.OutputGuardMode != "log" && cfg.OutputGuardMode != "enforce" {
		return nil, fmt.Errorf("OUTPUT_GUARD_MODE invalido %q (use off|log|enforce)", cfg.OutputGuardMode)
	}
	for _, p := range strings.Split(os.Getenv("GUARD_ENFORCE_PHONES"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.GuardEnforcePhones = append(cfg.GuardEnforcePhones, p)
		}
	}

	// Validate required fields.
	//
	// ANTHROPIC_API_KEY deixou de ser incondicional: com todos os papeis em
	// DeepSeek o bot precisa subir sem ela — era exatamente o que impedia o
	// sistema de voltar ao ar com o cartao cancelado.
	required := map[string]string{
		"GOOGLE_CLIENT_ID":     cfg.GoogleClientID,
		"GOOGLE_CLIENT_SECRET": cfg.GoogleClientSecret,
		"ENCRYPTION_KEY":       cfg.EncryptionKey,
	}
	for name, val := range required {
		if val == "" {
			return nil, fmt.Errorf("required env var %s is not set", name)
		}
	}

	// Cada papel exige a chave do provider que ele referencia. Falhar no boot
	// e melhor que descobrir em runtime, no turno de um usuario.
	keyFor := map[string]string{
		"deepseek":        cfg.DeepSeekAPIKey,
		"deepseek-vision": cfg.DeepSeekAPIKey,
		"gemini":          cfg.GeminiAPIKey,
		"anthropic":       cfg.AnthropicAPIKey,
	}
	envFor := map[string]string{
		"deepseek":        "DEEPSEEK_API_KEY",
		"deepseek-vision": "DEEPSEEK_API_KEY",
		"gemini":          "GEMINI_API_KEY",
		"anthropic":       "ANTHROPIC_API_KEY",
	}
	for role, prov := range roles {
		if keyFor[prov] == "" {
			return nil, fmt.Errorf("%s=%s exige %s, que nao esta setada", role, prov, envFor[prov])
		}
	}

	return cfg, nil
}

// normProvider normaliza o valor de uma env var de papel.
func normProvider(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// validProvider diz se o nome de provider e reconhecido.
func validProvider(v string) bool {
	switch v {
	case "deepseek", "deepseek-vision", "gemini", "anthropic":
		return true
	}
	return false
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// BRT returns the America/Sao_Paulo timezone. Falls back to UTC-3 fixed offset.
func BRT() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		loc = time.FixedZone("BRT", -3*60*60)
	}
	return loc
}
