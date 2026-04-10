package main

import (
	"log"
	"time"

	"go.mau.fi/whatsmeow"
)

type Watchdog struct {
	client     *whatsmeow.Client
	sendMsg    func(phone, text string) error
	adminPhone string
	interval   time.Duration
}

func NewWatchdog(client *whatsmeow.Client, sendMsg func(phone, text string) error, adminPhone string) *Watchdog {
	return &Watchdog{
		client:     client,
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

			if !w.client.IsConnected() {
				consecutiveFails++
				log.Printf("Watchdog: WhatsApp disconnected (attempt %d)", consecutiveFails)

				err := w.client.Connect()
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
