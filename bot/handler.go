package main

import (
	"context"
	"log"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type Handler struct {
	client       *whatsmeow.Client
	db           *DB
	orchestrator *Orchestrator
}

func NewHandler(client *whatsmeow.Client, db *DB, orchestrator *Orchestrator) *Handler {
	return &Handler{client: client, db: db, orchestrator: orchestrator}
}

func (h *Handler) HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		h.handleMessage(v)
	}
}

func (h *Handler) handleMessage(msg *events.Message) {
	sender := msg.Info.Sender.User

	user, err := h.db.GetUserByPhone(sender)
	if err == ErrUserNotFound {
		h.sendText(msg.Info.Sender, "Nao te conheço ainda. Peca ao administrador para te cadastrar.")
		return
	}
	if err != nil {
		log.Printf("Error looking up user %s: %v", sender, err)
		return
	}
	if !user.IsActive {
		return
	}

	ctx := context.Background()

	var text string

	if audioMsg := msg.Message.GetAudioMessage(); audioMsg != nil {
		audioData, err := h.client.Download(ctx, audioMsg)
		if err != nil {
			log.Printf("Error downloading audio from %s: %v", sender, err)
			h.sendText(msg.Info.Sender, "Nao consegui baixar o audio. Tente novamente.")
			return
		}
		text, err = h.orchestrator.transcription.Transcribe(audioData, "audio.ogg")
		if err != nil {
			log.Printf("Error transcribing audio from %s: %v", sender, err)
			h.sendText(msg.Info.Sender, "Nao consegui transcrever o audio. Tente novamente.")
			return
		}
	} else if textMsg := msg.Message.GetConversation(); textMsg != "" {
		text = textMsg
	} else if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		text = extMsg.GetText()
	}

	if text == "" {
		return
	}

	log.Printf("[%s] %s: %s", user.Name, sender, text)

	// Intercept "1"/"2"/"3" responses for pending permission requests
	trimmed := strings.TrimSpace(text)
	if trimmed == "1" || trimmed == "2" || trimmed == "3" {
		reply, handled, err := h.orchestrator.HandlePermissionResponse(ctx, user, trimmed)
		if err != nil {
			log.Printf("Error handling permission response from %s: %v", sender, err)
		} else if handled {
			if reply != "" {
				h.sendText(msg.Info.Sender, reply)
			}
			return
		}
	}

	response, err := h.orchestrator.Process(ctx, user, text)
	if err != nil {
		log.Printf("Error processing message from %s: %v", sender, err)
		h.sendText(msg.Info.Sender, "Ocorreu um erro ao processar sua mensagem. Tente novamente.")
		return
	}

	if response != "" {
		h.sendText(msg.Info.Sender, response)
	}
}

func (h *Handler) sendText(to types.JID, text string) {
	_, err := h.client.SendMessage(context.Background(), to, &waE2E.Message{
		Conversation: &text,
	})
	if err != nil {
		log.Printf("Error sending message to %s: %v", to.User, err)
	}
}

func (h *Handler) SendTextToPhone(phone, text string) error {
	jid := types.NewJID(phone, types.DefaultUserServer)
	_, err := h.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: &text,
	})
	return err
}
