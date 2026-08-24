package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// balanceHints sao marcadores de saldo/credencial que os providers mandam no
// corpo, nem sempre acompanhados de status distinto — a DeepSeek usa 429 pra
// "prepayment credits are depleted", que NAO e rate limit: esperar nao
// resolve, so queima invocacao.
var balanceHints = []string{
	"insufficient balance",
	"credit balance is too low",
	"prepayment credits are depleted",
	"exceeded your current quota",
	"billing",
}

// IsRetryable classifica erros que se recuperam esperando: rate limit e
// indisponibilidade do backend. Request invalido, auth e saldo nao melhoram
// com espera.
func IsRetryable(err error) bool {
	if err == nil || IsAuthOrBalance(err) {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 429, 500, 502, 503, 529:
			return true
		}
	}
	return false
}

// IsAuthOrBalance identifica falha de credencial ou de saldo — a condicao que
// derrubou o bot em silencio (1818 invalid_request_error em 72h sem ninguem
// ser avisado). Vira alerta de admin, nunca retry.
func IsAuthOrBalance(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatusCode == 401 || apiErr.HTTPStatusCode == 402 {
			return true
		}
	}
	low := strings.ToLower(err.Error())
	for _, h := range balanceHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// RetryWithBackoff repete fn enquanto o erro for retryable, com backoff
// exponencial limitado a 30s por espera.
func RetryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	delay := 2 * time.Second
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryable(err) || attempt == maxRetries {
			return err
		}
		wait := delay
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
	return lastErr
}
