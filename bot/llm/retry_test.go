package llm

import (
	"context"
	"errors"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{429, true}, {500, true}, {502, true}, {503, true}, {529, true},
		{400, false}, {401, false}, {402, false}, {404, false},
	}
	for _, c := range cases {
		err := &openai.APIError{HTTPStatusCode: c.code}
		if got := IsRetryable(err); got != c.want {
			t.Errorf("IsRetryable(%d) = %v, quer %v", c.code, got, c.want)
		}
	}
	if IsRetryable(nil) {
		t.Error("nil nao e retryable")
	}
}

// 429 com mensagem de credito esgotado NAO e rate limit: esperar nao resolve.
func TestIsRetryableIgnora429DeSaldo(t *testing.T) {
	err := &openai.APIError{HTTPStatusCode: 429, Message: "Your prepayment credits are depleted"}
	if IsRetryable(err) {
		t.Fatal("429 de saldo esgotado nao pode virar retry infinito")
	}
}

func TestIsAuthOrBalance(t *testing.T) {
	for _, code := range []int{401, 402} {
		if !IsAuthOrBalance(&openai.APIError{HTTPStatusCode: code}) {
			t.Errorf("HTTP %d deveria contar como auth/saldo", code)
		}
	}
	if IsAuthOrBalance(&openai.APIError{HTTPStatusCode: 500}) {
		t.Error("500 nao e auth/saldo")
	}
	if IsAuthOrBalance(nil) {
		t.Error("nil nao e auth/saldo")
	}
}

// Os providers sinalizam saldo pelo corpo, nem sempre por status distinto.
func TestIsAuthOrBalancePorMensagem(t *testing.T) {
	msgs := []string{
		"Your prepayment credits are depleted",
		"Your credit balance is too low to access the Anthropic API",
		"Insufficient Balance",
	}
	for _, m := range msgs {
		if !IsAuthOrBalance(&openai.APIError{HTTPStatusCode: 429, Message: m}) {
			t.Errorf("mensagem %q deveria contar como saldo", m)
		}
	}
}

func TestIsAuthOrBalanceErroSimples(t *testing.T) {
	if !IsAuthOrBalance(errors.New("deepseek chat: Insufficient Balance")) {
		t.Error("erro embrulhado com mensagem de saldo deveria ser detectado")
	}
}

func TestRetryWithBackoffParaEmErroNaoRetryable(t *testing.T) {
	calls := 0
	err := RetryWithBackoff(context.Background(), 3, func() error {
		calls++
		return &openai.APIError{HTTPStatusCode: 400}
	})
	if calls != 1 {
		t.Fatalf("chamadas = %d, quer 1 (400 nao retenta)", calls)
	}
	if err == nil {
		t.Fatal("erro deveria propagar")
	}
}

func TestRetryWithBackoffSucessoNaPrimeira(t *testing.T) {
	calls := 0
	if err := RetryWithBackoff(context.Background(), 3, func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("chamadas = %d, quer 1", calls)
	}
}

func TestRetryWithBackoffRespeitaContextoCancelado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RetryWithBackoff(ctx, 3, func() error {
		return &openai.APIError{HTTPStatusCode: 503}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erro = %v, quer context.Canceled", err)
	}
}
