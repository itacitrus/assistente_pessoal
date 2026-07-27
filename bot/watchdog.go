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

			if !client.IsConnected() {
				consecutiveFails++
				log.Printf("Watchdog: WhatsApp disconnected (attempt %d)", consecutiveFails)

				err := client.Connect()
				if err != nil {
					log.Printf("Watchdog: reconnect failed: %v", err)
					if consecutiveFails >= 3 {
						log.Printf("Watchdog: ALERT — 3 consecutive reconnect failures")
					}
					continue
				}

				log.Println("Watchdog: reconnected successfully")
				consecutiveFails = 0
			} else {
				consecutiveFails = 0
			}
		}
	}()
	log.Println("Watchdog started (interval: 5m)")
}
