package main

import (
	"sync"

	"go.mau.fi/whatsmeow"
)

// ClientHolder guarda o *whatsmeow.Client vivo atrás de um RWMutex. Handler,
// watchdog e demais consumidores leem via Get() em vez de capturar o ponteiro
// uma única vez — assim o re-pareamento pode trocar o client em runtime
// (Set) e todos passam a usar o novo, sem reiniciar o processo.
type ClientHolder struct {
	mu sync.RWMutex
	c  *whatsmeow.Client
}

// Get devolve o client atual (pode ser nil antes do primeiro Set).
func (h *ClientHolder) Get() *whatsmeow.Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.c
}

// Set publica o client vivo. Chamado no boot e a cada pareamento concluído.
func (h *ClientHolder) Set(c *whatsmeow.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.c = c
}
