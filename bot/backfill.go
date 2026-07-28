package main

import (
	"sort"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// Parâmetros do backfill no reconecte. Ver docs/superpowers/specs/
// 2026-07-27-backfill-reconnect-design.md.
const (
	// backfillAgeCeiling: não respondemos backlog mais velho que isto. Responder
	// "que horas são?" de dias atrás é pior que silêncio.
	backfillAgeCeiling = 24 * time.Hour
	// backfillCap: teto de mensagens processadas por rodada de HistorySync
	// (rede de segurança anti-flood). Mantemos as mais recentes.
	backfillCap = 30
	// backfillFutureSkew: tolerância pra timestamps levemente no futuro (skew de
	// relógio entre o celular e nós). Além disto, o timestamp é considerado
	// espúrio e a mensagem é descartada.
	backfillFutureSkew = 5 * time.Minute
)

// backfillCandidate é uma mensagem do backlog (HistorySync) já parseada e com o
// remetente resolvido para um usuário registrado. Os campos escalares alimentam
// a seleção pura; msg é repassado a handleMessage quando selecionado.
type backfillCandidate struct {
	MsgID     string
	UserID    int64
	Timestamp time.Time       // hora de envio (do WebMessageInfo), em UTC
	FromMe    bool            // mensagem do próprio bot
	msg       *events.Message // já parseado via ParseWebMessage
}

// selectBackfill decide, entre os candidatos do backlog, quais realmente
// responder. Regras (todas necessárias para não responder o que já foi tratado,
// nem o que é antigo demais, nem inundar):
//
//   - descarta FromMe (mensagens do próprio bot);
//   - descarta Timestamp mais que backfillFutureSkew no futuro (timestamp espúrio);
//   - descarta Timestamp fora da janela (< now - ageCeiling);
//   - descarta Timestamp <= marca d'água do usuário (já tratado antes da queda);
//   - deduplica por MsgID;
//   - se o total passar de cap, mantém os `cap` MAIS RECENTES (dropped conta o
//     restante, que o caller loga).
//
// Devolve os selecionados em ordem cronológica (mais antigo primeiro), pra
// alimentar handleMessage na ordem em que o usuário mandou. watermarks sem
// entrada para um usuário equivale a marca zero (passa tudo dentro da janela).
func selectBackfill(cands []backfillCandidate, watermarks map[int64]time.Time,
	now time.Time, ageCeiling time.Duration, cap int) (selected []backfillCandidate, dropped int) {

	cutoff := now.Add(-ageCeiling)
	futureLimit := now.Add(backfillFutureSkew)

	seen := make(map[string]bool, len(cands))
	for _, c := range cands {
		if c.FromMe {
			continue
		}
		if c.Timestamp.After(futureLimit) {
			continue
		}
		if c.Timestamp.Before(cutoff) {
			continue
		}
		if wm, ok := watermarks[c.UserID]; ok && !c.Timestamp.After(wm) {
			continue // <= marca d'água: já tratado
		}
		if seen[c.MsgID] {
			continue
		}
		seen[c.MsgID] = true
		selected = append(selected, c)
	}

	// Ordem cronológica; MsgID como desempate determinístico.
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Timestamp.Equal(selected[j].Timestamp) {
			return selected[i].MsgID < selected[j].MsgID
		}
		return selected[i].Timestamp.Before(selected[j].Timestamp)
	})

	if cap > 0 && len(selected) > cap {
		dropped = len(selected) - cap
		selected = selected[len(selected)-cap:] // mantém os mais recentes (cauda)
	}
	return selected, dropped
}
