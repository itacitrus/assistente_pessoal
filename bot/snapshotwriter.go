package main

import (
	"context"
	"time"
)

// SnapshotWriter eh o gancho que a Fase 5 vai injetar pra escrever
// snapshots psicologicos diarios (Haiku 4.5). A Fase 4 deixa a interface
// no pacote main com uma implementacao no-op default, pra que:
//
//   1. Agent.snapshotWriter NUNCA seja nil (evita panic em handler).
//   2. handler.flushBuffer possa chamar MaybeUpdateSnapshot incondicionalmente
//      em goroutine, sem se preocupar se Fase 5 ja foi mergeada.
//   3. Testes da Fase 4 possam validar o GANCHO sem depender de Haiku
//      ou da tabela psych_state_daily (que pertence a Fase 5).
//
// Heuristica de "conversa significativa" vive em handler.go. SnapshotWriter
// recebe userID — implementacao concreta resolve user e contexto. Erros
// sao apenas logados; nao bloqueiam o fluxo do idoso.
type SnapshotWriter interface {
	// MaybeUpdateSnapshot eh chamado apos cada conversa significativa.
	// Implementacao concreta:
	//   1. Carrega historico do dia.
	//   2. Chama Haiku 4.5 com schema psych_state_v1.
	//   3. UPSERT em psych_state_daily.
	//   4. Safety net: se severity_max=critical e companion nao alertou,
	//      dispara alertar_familia direto.
	//
	// Implementacoes devem ser non-blocking (caller chama em goroutine
	// com timeout 30s) e idempotentes (UPSERT por (user_id, date)).
	MaybeUpdateSnapshot(ctx context.Context, userID int64) error

	// UpdateSnapshotForDay roda a mesma pipeline de MaybeUpdateSnapshot mas
	// para um dia-alvo explicito (instante qualquer DENTRO do dia desejado;
	// a impl resolve o fuso do user e normaliza pra meia-noite local). Usado
	// pelo catchup do scheduler pra preencher dias PASSADOS sem snapshot —
	// MaybeUpdateSnapshot sozinho so opera "hoje" e nao consegue backfillar.
	UpdateSnapshotForDay(ctx context.Context, userID int64, day time.Time) error
}

// noopSnapshotWriter e o default — nao faz nada. Ate Fase 5 mergear,
// o gancho fica armado mas inerte. Nenhum custo, nenhuma chamada Haiku.
type noopSnapshotWriter struct{}

// MaybeUpdateSnapshot sempre retorna nil sem efeito.
func (noopSnapshotWriter) MaybeUpdateSnapshot(_ context.Context, _ int64) error { return nil }

// UpdateSnapshotForDay sempre retorna nil sem efeito.
func (noopSnapshotWriter) UpdateSnapshotForDay(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
