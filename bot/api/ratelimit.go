package api

import (
	"sync"
	"time"
)

// statusCache eh um cache em memoria com TTL. Usado pra GET /family/dependents/{id}/status,
// onde a chamada de Synthesize (Sonnet) eh cara e o frontend pode polled
// num refresh-loop.
//
// Implementacao minima:
//   - sync.Map[key]entry; entry tem expiry;
//   - GC eh "lazy" via janela de tempo: cada Get checa expiry, se expirado
//     remove. Sem goroutine dedicada — overhead minimo, comporta bem o
//     trafego esperado (centenas de guardians, refresh poucas vezes/min).
//
// Reentrancia: nao serializamos chamadas pra mesmo key; se duas requests
// chegarem antes do primeiro Set, ambas fazem Synthesize. Aceitavel — TTL 60s
// limita o blast radius. Otimizacao single-flight ficaria pra futuro se virar
// problema.
type statusCache struct {
	m   sync.Map // key string -> entry
	ttl time.Duration
}

type cacheEntry struct {
	value  *StatusResponse
	expiry time.Time
}

func newStatusCache(ttl time.Duration) *statusCache {
	return &statusCache{ttl: ttl}
}

// Get retorna o valor se ainda valido. Caller usa "ok" pra decidir se chama
// Synthesize ou nao.
func (c *statusCache) Get(key string) (*StatusResponse, bool) {
	v, ok := c.m.Load(key)
	if !ok {
		return nil, false
	}
	e, ok := v.(cacheEntry)
	if !ok {
		return nil, false
	}
	if time.Now().UTC().After(e.expiry) {
		c.m.Delete(key)
		return nil, false
	}
	return e.value, true
}

// Set sobrescreve o valor com expiry now+ttl. Nao copia — caller eh dono
// do StatusResponse. Como a struct eh imutavel apos retornar do BuildDependentStatus,
// compartilhar ponteiro entre requests do mesmo key eh seguro.
func (c *statusCache) Set(key string, value *StatusResponse) {
	c.m.Store(key, cacheEntry{
		value:  value,
		expiry: time.Now().UTC().Add(c.ttl),
	})
}

// Invalidate forca remocao de uma chave. Usado quando UI muda algo que
// afeta o status (futuro: cancelamento de medicacao, etc).
func (c *statusCache) Invalidate(key string) {
	c.m.Delete(key)
}
