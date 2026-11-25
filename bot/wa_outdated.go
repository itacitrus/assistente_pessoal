package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

// recoverOutdated reage ao 405 "client outdated": o WhatsApp recusou a versão
// de cliente embutida no whatsmeow e NÃO reconecta sozinho (o bot fica mudo).
// Busca a versão publicada do WhatsApp Web, aplica e reconecta. Se a publicada
// já é a que usamos, só um bump da lib resolve — devolve erro em vez de
// reconectar (senão vira loop 405 → fetch → 405).
//
// Isto cobre só a expiração da versão; mudanças de protocolo continuam exigindo
// bump do whatsmeow (ver incidente LID de jul/2026).
func recoverOutdated(
	ctx context.Context,
	fetch func(context.Context) (*store.WAVersionContainer, error),
	reconnect func() error,
) error {
	cur := store.GetWAVersion()
	latest, err := fetch(ctx)
	if err != nil {
		return fmt.Errorf("buscar versão do WhatsApp Web: %w", err)
	}
	if *latest == cur {
		return fmt.Errorf("versão publicada (%s) é a mesma recusada — bump do whatsmeow necessário", cur)
	}
	store.SetWAVersion(*latest)
	log.Printf("WhatsApp: versão de cliente %s recusada (405); usando %s publicada no WhatsApp Web", cur, *latest)
	return reconnect()
}

// handleClientOutdated liga recoverOutdated ao client vivo do holder.
func (h *Handler) handleClientOutdated() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := recoverOutdated(ctx,
		func(ctx context.Context) (*store.WAVersionContainer, error) {
			return whatsmeow.GetLatestVersion(ctx, nil)
		},
		func() error {
			c := h.holder.Get()
			if c == nil {
				return fmt.Errorf("sem client no holder")
			}
			return c.Connect()
		})
	if err != nil {
		log.Printf("WhatsApp: ALERTA — client outdated (405) sem recuperação automática: %v", err)
	}
}
