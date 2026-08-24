package main

import (
	"errors"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// Prod gerou 1.818 invalid_request_error em 72h: a varredura horaria
// percorria 7 dias x N idosos disparando ~21 chamadas condenadas por hora.
func TestSnapshotBackoffSeguraAposFalha(t *testing.T) {
	b := newSnapshotBackoff()
	if b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("primeira tentativa nunca e pulada")
	}
	b.RecordFailure(1, "2026-08-24")
	if !b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("logo apos falhar tem que segurar")
	}
}

func TestSnapshotBackoffIsolaPorUsuarioEDia(t *testing.T) {
	b := newSnapshotBackoff()
	b.RecordFailure(1, "2026-08-24")
	if b.ShouldSkip(2, "2026-08-24") {
		t.Fatal("falha do user 1 nao pode segurar o user 2")
	}
	if b.ShouldSkip(1, "2026-08-25") {
		t.Fatal("falha de um dia nao pode segurar outro dia")
	}
}

func TestSnapshotBackoffLimpaNoSucesso(t *testing.T) {
	b := newSnapshotBackoff()
	b.RecordFailure(1, "2026-08-24")
	b.RecordSuccess(1, "2026-08-24")
	if b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("sucesso tem que zerar o backoff")
	}
}

// Falha permanente (saldo) nao pode virar retry infinito.
func TestSnapshotBackoffDesisteAposTeto(t *testing.T) {
	b := newSnapshotBackoff()
	for i := 0; i < maxFailuresPerDay+2; i++ {
		b.RecordFailure(1, "2026-08-24")
	}
	if !b.ShouldSkip(1, "2026-08-24") {
		t.Fatal("apos o teto tem que parar de tentar no dia")
	}
}

// O backoff nao pode estourar o shift quando o contador cresce.
func TestSnapshotBackoffNaoEstouraOverflow(t *testing.T) {
	b := newSnapshotBackoff()
	for i := 0; i < 70; i++ {
		b.RecordFailure(1, "2026-08-24")
	}
	b.mu.Lock()
	e := b.state[backoffKey(1, "2026-08-24")]
	b.mu.Unlock()
	if e.nextTry.IsZero() {
		t.Fatal("nextTry nao pode ficar zerado com muitas falhas")
	}
}

// Credencial/saldo condena a varredura inteira: nao adianta tentar os
// proximos 20 idosos.
func TestShouldAbortSweep(t *testing.T) {
	if !shouldAbortSweep(&openai.APIError{HTTPStatusCode: 402, Message: "Insufficient Balance"}) {
		t.Error("saldo deveria abortar a varredura")
	}
	if !shouldAbortSweep(errors.New("Your credit balance is too low to access the Anthropic API")) {
		t.Error("mensagem de credito da Anthropic deveria abortar a varredura")
	}
	if shouldAbortSweep(errors.New("timeout lendo o banco")) {
		t.Error("erro local nao pode abortar a varredura toda")
	}
	if shouldAbortSweep(nil) {
		t.Error("nil nao aborta nada")
	}
}
