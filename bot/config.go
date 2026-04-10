// bot/config.go
package main

import (
	"fmt"
	"os"
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

	DefaultDailySummaryTime  string
	DefaultWeeklySummaryDay  string
	DefaultWeeklySummaryTime string
	DefaultReminderBefore    time.Duration
	DefaultAutoConfirmTimeout time.Duration
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
		cfg.GoogleRedirectURI = "http://localhost:8080/oauth/callback"
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

	// Validate required fields
	required := map[string]string{
		"GOOGLE_CLIENT_ID":     cfg.GoogleClientID,
		"GOOGLE_CLIENT_SECRET": cfg.GoogleClientSecret,
		"ANTHROPIC_API_KEY":    cfg.AnthropicAPIKey,
		"ENCRYPTION_KEY":       cfg.EncryptionKey,
	}
	for name, val := range required {
		if val == "" {
			return nil, fmt.Errorf("required env var %s is not set", name)
		}
	}

	return cfg, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
