package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// snapshotBackoff segura retentativas de snapshot que estao falhando.
//
// Motivacao concreta: com o saldo da Anthropic zerado, o catchup horario
// percorria 7 dias x N idosos e disparava ~21 chamadas CONDENADAS por hora,
// cada uma virando um invalid_request_error. Em 72h isso somou 1.818 erros
// no log de producao — todos previsiveis apos o primeiro.
//
// Duas defesas, em niveis diferentes:
//
//  1. Por (usuario, dia): backoff exponencial pra falha transitoria, com
//     desistencia apos maxFailuresPerDay tentativas.
//  2. Por varredura: quando o erro e de credencial/saldo (llm.IsAuthOrBalance),
//     nao adianta tentar os outros 20 — a varredura inteira aborta.
type snapshotBackoff struct {
	mu    sync.Mutex
	state map[string]*backoffEntry
}

type backoffEntry struct {
	failures int
	nextTry  time.Time
}

const (
	// maxFailuresPerDay: apos isso, desiste do par (usuario, dia) ate o
	// processo reiniciar ou o snapshot ter sucesso por outro caminho.
	maxFailuresPerDay = 6
	// backoffBase e o primeiro intervalo; dobra a cada falha.
	backoffBase = time.Minute
	// backoffCap limita a espera — o catchup roda de hora em hora de qualquer
	// forma, entao passar disso nao muda nada na pratica.
	backoffCap = time.Hour
)

func newSnapshotBackoff() *snapshotBackoff {
	return &snapshotBackoff{state: map[string]*backoffEntry{}}
}

func backoffKey(userID int64, day string) string {
	return fmt.Sprintf("%d|%s", userID, day)
}

// ShouldSkip diz se o par (usuario, dia) ainda esta em janela de espera ou ja
// esgotou as tentativas.
func (b *snapshotBackoff) ShouldSkip(userID int64, day string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.state[backoffKey(userID, day)]
	if !ok {
		return false
	}
	if e.failures >= maxFailuresPerDay {
		return true
	}
	return time.Now().Before(e.nextTry)
}

// RecordFailure registra uma falha e agenda a proxima janela.
func (b *snapshotBackoff) RecordFailure(userID int64, day string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := backoffKey(userID, day)
	e, ok := b.state[k]
	if !ok {
		e = &backoffEntry{}
		b.state[k] = e
	}
	e.failures++
	wait := backoffBase << (e.failures - 1)
	if wait > backoffCap || wait <= 0 {
		wait = backoffCap
	}
	e.nextTry = time.Now().Add(wait)
}

// RecordSuccess zera o estado do par.
func (b *snapshotBackoff) RecordSuccess(userID int64, day string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, backoffKey(userID, day))
}

// shouldAbortSweep diz se o erro e do tipo que condena a varredura inteira —
// credencial ou saldo nao melhoram no proximo idoso da lista.
func shouldAbortSweep(err error) bool {
	return llm.IsAuthOrBalance(err)
}
