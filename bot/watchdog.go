package main

import (
	"log"
	"time"
)

type Watchdog struct {
	holder     *ClientHolder
	sendMsg    func(phone, text string) error
	adminPhone string
	interval   time.Duration
}

func NewWatchdog(holder *ClientHolder, sendMsg func(phone, text string) error, adminPhone string) *Watchdog {
	return &Watchdog{
		holder:     holder,
		sendMsg:    sendMsg,
		adminPhone: adminPhone,
		interval:   5 * time.Minute,
	}
}

func (w *Watchdog) Start() {
	go func() {
		consecutiveFails := 0
		for {
			time.Sleep(w.interval)

			client := w.holder.Get()
			// Sem client vivo ou ainda não logado (device apagado / aguardando
			// pareamento): não há sessão a reconectar. Reconectar aqui só
			// atrapalharia o PairingManager (que dirige o client não-autenticado
			// e o canal de QR). Deixa o pareamento cuidar disso.
			if client == nil || client.Store.ID == nil {
				consecutiveFails = 0
				continue
			}

			// IsLoggedIn (socket + autenticado), não IsConnected: num 405 o socket
			// abre e Connect() devolve nil, mas o servidor recusa o login — o
			// watchdog antigo reportou "reconnected successfully" 440x com o bot mudo.
			if !client.IsLoggedIn() {
				consecutiveFails++
				log.Printf("Watchdog: WhatsApp not logged in (attempt %d)", consecutiveFails)
				if consecutiveFails >= 3 {
					log.Printf("Watchdog: ALERT — WhatsApp fora há %d verificações seguidas", consecutiveFails)
				}

				client.Disconnect() // socket meio-aberto faria Connect devolver ErrAlreadyConnected
				if err := client.Connect(); err != nil {
					log.Printf("Watchdog: reconnect failed: %v", err)
				}
				// Sucesso só é confirmado no próximo tick, via IsLoggedIn.
			} else {
				if consecutiveFails > 0 {
					log.Printf("Watchdog: WhatsApp logged in again after %d failed checks", consecutiveFails)
				}
				consecutiveFails = 0
			}
		}
	}()
	log.Println("Watchdog started (interval: 5m)")
}
