package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type Handler struct {
	client         *whatsmeow.Client
	db             *DB
	orchestrator   *Orchestrator
	unknownReplied map[string]time.Time // rate limit: one reply per unknown number per hour
	unknownMu      sync.Mutex
}

func NewHandler(client *whatsmeow.Client, db *DB, orchestrator *Orchestrator) *Handler {
	return &Handler{
		client:         client,
		db:             db,
		orchestrator:   orchestrator,
		unknownReplied: make(map[string]time.Time),
	}
}

func (h *Handler) HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		h.handleMessage(v)
	default:
		log.Printf("DEBUG_EVENT: type=%T", evt)
	}
}

// normalizeBRPhone tries to match a Brazilian phone number with or without the 9th digit.
// Brazilian mobile numbers: 55 + DD (2 digits) + 9 + 8 digits = 13 digits total.
// WhatsApp sometimes delivers without the leading 9: 55 + DD + 8 digits = 12 digits.
func normalizeBRPhone(phone string) []string {
	variants := []string{phone}

	if strings.HasPrefix(phone, "55") {
		digits := phone[2:] // DD + number
		if len(digits) == 11 && digits[2] == '9' {
			// Has the 9 — also try without: 55 + DD + last 8
			without9 := "55" + digits[:2] + digits[3:]
			variants = append(variants, without9)
		} else if len(digits) == 10 {
			// Missing the 9 — also try with: 55 + DD + 9 + 8 digits
			with9 := "55" + digits[:2] + "9" + digits[2:]
			variants = append(variants, with9)
		}
	}
	return variants
}

func (h *Handler) handleMessage(msg *events.Message) {
	// Resolve sender phone number — WhatsApp may use LID instead of phone number
	senderJID := msg.Info.Sender.ToNonAD()
	if senderJID.Server == "lid" {
		resolved, resolveErr := h.client.Store.LIDs.GetPNForLID(context.Background(), senderJID)
		if resolveErr == nil && resolved.User != "" {
			log.Printf("DEBUG: resolved LID %s -> phone %s", senderJID.User, resolved.User)
			senderJID = resolved.ToNonAD()
		} else {
			log.Printf("DEBUG: could not resolve LID %s: %v", senderJID.User, resolveErr)
		}
	}

	sender := senderJID.User
	log.Printf("DEBUG: sender=%s pushName=%s isFromMe=%v", sender, msg.Info.PushName, msg.Info.IsFromMe)

	// Ignore messages from self (the bot's own number)
	if msg.Info.IsFromMe {
		return
	}

	// Try all phone variants (with/without 9th digit)
	var user *User
	var err error
	for _, variant := range normalizeBRPhone(sender) {
		user, err = h.db.GetUserByPhone(variant)
		if err == nil {
			break
		}
	}
	if user == nil {
		err = ErrUserNotFound
	}
	if err == ErrUserNotFound {
		// Rate limit: only reply once per hour per unknown number
		h.unknownMu.Lock()
		lastReply, exists := h.unknownReplied[sender]
		if exists && time.Since(lastReply) < time.Hour {
			h.unknownMu.Unlock()
			return
		}
		h.unknownReplied[sender] = time.Now()
		h.unknownMu.Unlock()

		log.Printf("Unknown number: %s", sender)
		h.sendText(senderJID, "Nao te conheço ainda. Peca ao administrador para te cadastrar.")
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
			h.sendText(senderJID, "Nao consegui baixar o audio. Tente novamente.")
			return
		}
		text, err = h.orchestrator.transcription.Transcribe(audioData, "audio.ogg")
		if err != nil {
			log.Printf("Error transcribing audio from %s: %v", sender, err)
			h.sendText(senderJID, "Nao consegui transcrever o audio. Tente novamente.")
			return
		}
	} else if textMsg := msg.Message.GetConversation(); textMsg != "" {
		text = textMsg
	} else if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		text = extMsg.GetText()
	} else if contactMsg := msg.Message.GetContactMessage(); contactMsg != nil {
		// Extract contact info from vCard
		text = h.parseContactMessage(contactMsg)
	} else if contactsMsg := msg.Message.GetContactsArrayMessage(); contactsMsg != nil {
		// Multiple contacts shared
		var parts []string
		for _, c := range contactsMsg.GetContacts() {
			parts = append(parts, h.parseContactMessage(c))
		}
		text = strings.Join(parts, "\n")
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
				h.sendText(senderJID, reply)
			}
			return
		}
	}

	response, err := h.orchestrator.Process(ctx, user, text)
	if err != nil {
		log.Printf("Error processing message from %s: %v", sender, err)
		h.sendText(senderJID, "Ocorreu um erro ao processar sua mensagem. Tente novamente.")
		return
	}

	if response != "" {
		// Save bot response to conversation history
		h.db.AddConversationMessage(user.ID, "assistant", response)
		h.sendText(senderJID, response)
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
	// Try to verify the number is on WhatsApp first
	results, err := h.client.IsOnWhatsApp(context.Background(), []string{"+" + phone})
	if err != nil {
		log.Printf("IsOnWhatsApp check failed for %s: %v", phone, err)
	} else if len(results) > 0 && results[0].IsIn {
		// Use the JID returned by WhatsApp (correct format)
		log.Printf("SendTextToPhone: %s is on WhatsApp as %s", phone, results[0].JID.String())
		_, err := h.client.SendMessage(context.Background(), results[0].JID, &waE2E.Message{
			Conversation: &text,
		})
		return err
	} else if len(results) > 0 && !results[0].IsIn {
		log.Printf("SendTextToPhone: %s is NOT on WhatsApp", phone)
		return fmt.Errorf("numero %s nao esta no WhatsApp", phone)
	}

	// Fallback: send directly
	jid := types.NewJID(phone, types.DefaultUserServer)
	log.Printf("SendTextToPhone: sending to %s (fallback)", jid.String())
	_, err = h.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: &text,
	})
	return err
}

// parseContactMessage extracts name and phone from a shared WhatsApp contact vCard.
func (h *Handler) parseContactMessage(contact *waE2E.ContactMessage) string {
	name := contact.GetDisplayName()
	vcard := contact.GetVcard()

	// Extract phone number from vCard TEL field
	phone := ""
	for _, line := range strings.Split(vcard, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(strings.ToUpper(line), "TEL") {
			// Format: TEL;type=CELL:+5561981012927 or TEL:+5561981012927
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				phone = strings.TrimSpace(parts[1])
				phone = strings.ReplaceAll(phone, "+", "")
				phone = strings.ReplaceAll(phone, " ", "")
				phone = strings.ReplaceAll(phone, "-", "")
				break
			}
		}
	}

	if phone != "" && name != "" {
		return fmt.Sprintf("[Contato compartilhado] Nome: %s, Telefone: %s", name, phone)
	} else if name != "" {
		return fmt.Sprintf("[Contato compartilhado] Nome: %s", name)
	}
	return "[Contato compartilhado — nao consegui extrair os dados]"
}
