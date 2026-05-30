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

// bufferDelay is how long we wait for additional messages from the same user
// before processing the accumulated batch. Resets on every new message.
const bufferDelay = 5 * time.Second

// elderBufferDelay eh a janela de coalescencia para idosos. Maior de proposito:
// idoso costuma mandar a mensagem em pedacos com pausa ("Bom dia" ... "Pronto"),
// e com 5s esses pedacos viravam DOIS turnos -> duas respostas (duas saudacoes),
// dando a impressao de "dois agentes". Com a janela maior eles coalescem num
// unico turno -> uma resposta. Companheiro nao eh canal de baixa latencia; trocar
// alguns segundos por coerencia social vale a pena.
const elderBufferDelay = 9 * time.Second

type pendingBuffer struct {
	texts     []string
	images    []ImageAttachment
	timer     *time.Timer
	senderJID types.JID
	pushName  string // WhatsApp profile name — palpite de nome p/ signup de lead
	gen       uint64 // incremented on each reset; flush checks this to ignore stale timers
}

type Handler struct {
	client         *whatsmeow.Client
	db             *DB
	orchestrator   *Orchestrator
	unknownReplied map[string]time.Time
	unknownMu      sync.Mutex
	processedMsgs  map[string]bool // dedup by message ID
	processedMu    sync.Mutex
	buffers        map[string]*pendingBuffer
	bufMu          sync.Mutex
	// procLocks serializa o processamento por usuario: um turno por vez. Sem
	// isso, dois flushes do mesmo usuario podiam rodar em paralelo e o segundo
	// nem via a resposta do primeiro no historico (race -> respostas incoerentes,
	// dupla saudacao). Lazy via getProcLock.
	procLocks map[string]*sync.Mutex
	procMu    sync.Mutex
}

func NewHandler(client *whatsmeow.Client, db *DB, orchestrator *Orchestrator) *Handler {
	return &Handler{
		client:         client,
		db:             db,
		orchestrator:   orchestrator,
		unknownReplied: make(map[string]time.Time),
		processedMsgs:  make(map[string]bool),
		buffers:        make(map[string]*pendingBuffer),
		procLocks:      make(map[string]*sync.Mutex),
	}
}

// getProcLock devolve (criando se preciso) o mutex de serializacao de turno para
// `phone`. Garante "um turno por vez por usuario".
func (h *Handler) getProcLock(phone string) *sync.Mutex {
	h.procMu.Lock()
	defer h.procMu.Unlock()
	m, ok := h.procLocks[phone]
	if !ok {
		m = &sync.Mutex{}
		h.procLocks[phone] = m
	}
	return m
}

func (h *Handler) HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		h.handleMessage(v)
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
	// Ignore non-DM messages: groups, broadcasts, newsletters, status updates
	chat := msg.Info.Chat
	if msg.Info.IsGroup || chat.Server == "g.us" || chat.Server == "broadcast" || chat.Server == "newsletter" {
		return
	}
	// Ignore status updates (status@broadcast)
	if chat.User == "status" {
		return
	}

	// Dedup: skip if we already processed this message ID
	msgID := msg.Info.ID
	h.processedMu.Lock()
	if h.processedMsgs[string(msgID)] {
		h.processedMu.Unlock()
		return
	}
	h.processedMsgs[string(msgID)] = true
	// Keep map from growing forever — prune if too large
	if len(h.processedMsgs) > 1000 {
		h.processedMsgs = make(map[string]bool)
	}
	h.processedMu.Unlock()

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

	// Extract text early — needed for both registered and unknown users
	ctx := context.Background()
	var text string
	if textMsg := msg.Message.GetConversation(); textMsg != "" {
		text = textMsg
	} else if extMsg := msg.Message.GetExtendedTextMessage(); extMsg != nil {
		text = extMsg.GetText()
	} else if contactMsg := msg.Message.GetContactMessage(); contactMsg != nil {
		text = h.parseContactMessage(contactMsg)
	} else if contactsMsg := msg.Message.GetContactsArrayMessage(); contactsMsg != nil {
		var parts []string
		for _, c := range contactsMsg.GetContacts() {
			parts = append(parts, h.parseContactMessage(c))
		}
		text = strings.Join(parts, "\n")
	}

	if err == ErrUserNotFound {
		if text == "" {
			return // Ignore non-text from unknown users (audio, etc)
		}
		log.Printf("Unknown number %s: %s (buffering)", sender, text)
		h.bufferAndSchedule(sender, senderJID, msg.Info.PushName, text, nil, false)
		return
	}
	if err != nil {
		log.Printf("Error looking up user %s: %v", sender, err)
		return
	}
	if !user.IsActive {
		return
	}

	// For registered users, also handle audio and images
	var images []ImageAttachment
	hadAudio := false
	if audioMsg := msg.Message.GetAudioMessage(); audioMsg != nil && text == "" {
		hadAudio = true
		audioData, audioErr := h.client.Download(ctx, audioMsg)
		if audioErr != nil {
			log.Printf("Error downloading audio from %s: %v", sender, audioErr)
			h.sendText(senderJID, "Nao consegui baixar o audio. Tente novamente.")
			return
		}
		text, audioErr = h.orchestrator.transcription.Transcribe(audioData, "audio.ogg")
		if audioErr != nil {
			log.Printf("Error transcribing audio from %s: %v", sender, audioErr)
			h.sendText(senderJID, "Nao consegui transcrever o audio. Tente novamente.")
			return
		}
	}
	if imgMsg := msg.Message.GetImageMessage(); imgMsg != nil {
		imgData, imgErr := h.client.Download(ctx, imgMsg)
		if imgErr != nil {
			log.Printf("Error downloading image from %s: %v", sender, imgErr)
		} else {
			mime := imgMsg.GetMimetype()
			if mime == "" {
				mime = "image/jpeg"
			}
			images = append(images, ImageAttachment{Data: imgData, Mime: mime})
			// Use caption as text if available
			if caption := imgMsg.GetCaption(); caption != "" && text == "" {
				text = caption
			}
			log.Printf("[%s] Image received (%d bytes, %s)", user.Name, len(imgData), mime)
		}
	}

	if text == "" && len(images) == 0 {
		// Áudio que transcreveu vazio (silêncio, ruído, fala inaudível) NÃO
		// pode virar silêncio do bot — pra um idoso que mandou áudio, ficar
		// sem resposta parece abandono. Pedimos pra repetir.
		if hadAudio {
			h.sendText(senderJID, "Não consegui entender o áudio — deu pra ouvir bem pouco. Pode mandar de novo ou me escrever?")
		}
		return
	}

	log.Printf("[%s] %s: %s", user.Name, sender, text)

	h.bufferAndSchedule(sender, senderJID, msg.Info.PushName, text, images, user.Type == UserTypeIdoso)
}

// bufferAndSchedule appends a message to the per-user pending buffer and (re)arms
// the coalescing timer. When the timer fires without new messages, the batch is
// flushed to the orchestrator as a single request. isElder usa uma janela maior
// (elderBufferDelay) para coalescer mensagens em pedacos do idoso num so turno.
func (h *Handler) bufferAndSchedule(phone string, senderJID types.JID, pushName, text string, images []ImageAttachment, isElder bool) {
	h.bufMu.Lock()
	defer h.bufMu.Unlock()

	pb, ok := h.buffers[phone]
	if !ok {
		pb = &pendingBuffer{senderJID: senderJID}
		h.buffers[phone] = pb
	}
	if pushName != "" {
		pb.pushName = pushName
	}
	if text != "" {
		pb.texts = append(pb.texts, text)
	}
	pb.images = append(pb.images, images...)

	pb.gen++
	gen := pb.gen
	if pb.timer != nil {
		pb.timer.Stop()
	}
	delay := bufferDelay
	if isElder {
		delay = elderBufferDelay
	}
	pb.timer = time.AfterFunc(delay, func() { h.flushBuffer(phone, gen) })
}

// flushBuffer drains the pending buffer for phone and dispatches the batch.
// The gen parameter guards against stale timer fires: if another message
// arrived between the timer firing and us acquiring the lock, the generation
// won't match and we return without processing (the new timer will flush).
func (h *Handler) flushBuffer(phone string, gen uint64) {
	h.bufMu.Lock()
	pb, ok := h.buffers[phone]
	if !ok || pb.gen != gen {
		h.bufMu.Unlock()
		return
	}
	delete(h.buffers, phone)
	h.bufMu.Unlock()

	text := strings.Join(pb.texts, "\n")
	ctx := context.Background()

	// Re-lookup user at flush time in case state changed during buffering.
	var user *User
	for _, variant := range normalizeBRPhone(phone) {
		if u, err := h.db.GetUserByPhone(variant); err == nil {
			user = u
			break
		}
	}

	if user == nil {
		log.Printf("Flushing unknown buffer %s (%d msgs)", phone, len(pb.texts))
		response, err := h.orchestrator.ProcessUnknown(ctx, phone, pb.pushName, text)
		if err != nil {
			log.Printf("Error processing unknown user %s: %v", phone, err)
			return
		}
		if response != "" {
			h.sendText(pb.senderJID, response)
		}
		return
	}
	if !user.IsActive {
		return
	}

	// Serializa o processamento por usuario: um turno por vez. Se outra mensagem
	// chegou e disparou outro flush, ele espera aqui — garantindo que ESTE turno
	// persista sua resposta no historico ANTES do proximo rodar (sem corrida, sem
	// dupla saudacao de turnos concorrentes). A goroutine de snapshot la embaixo
	// nao segura o lock (o `go` retorna na hora; defer libera no fim da funcao).
	lock := h.getProcLock(phone)
	lock.Lock()
	defer lock.Unlock()

	log.Printf("[%s] Flushing buffer (%d msgs, %d imgs)", user.Name, len(pb.texts), len(pb.images))

	// Fase 4 (idosos): atualiza last_user_message_at + flipa proactive
	// 'sent' -> 'replied'. Idempotente; nao bloqueia o processing se
	// falhar — caller ainda responde ao idoso.
	if err := h.db.MarkUserMessageReceivedAndProactive(user.ID, time.Now()); err != nil {
		log.Printf("[%s] MarkUserMessageReceivedAndProactive: %v", user.Name, err)
	}

	response, err := h.orchestrator.Process(ctx, user, text, pb.images)
	if err != nil {
		log.Printf("Error processing message from %s: %v", phone, err)
		h.sendText(pb.senderJID, "Ocorreu um erro ao processar sua mensagem. Tente novamente.")
		return
	}
	if response != "" {
		h.sendText(pb.senderJID, response)
	} else {
		// Resposta vazia a uma mensagem DIRETA é suspeita (drop/empty LLM).
		// Pra idoso, silêncio soa como abandono — manda um fallback curto em
		// vez de ghostear. Pra outros, só loga (não fabrica resposta).
		log.Printf("[%s] resposta vazia a mensagem direta — possível drop/empty", user.Name)
		if user.Type == UserTypeIdoso {
			h.sendText(pb.senderJID, "Tô aqui, viu, "+firstName(user.Name)+". Me conta de novo o que você precisa?")
		}
	}

	// Fase 4: trigger snapshot pos-conversa para idosos. Heuristica de
	// "conversa significativa" decide se vale chamar Haiku. Roda em
	// goroutine separada com timeout 30s — nunca bloqueia o idoso.
	if user.Type == UserTypeIdoso && h.orchestrator != nil && h.orchestrator.agent != nil {
		go h.maybeSnapshotIdoso(user.ID)
	}
}

// maybeSnapshotIdoso decide se chama o snapshot writer. Heuristica simples
// (sem precisar de DB extra): chama incondicional, e o snapshot writer
// de Fase 5 vai checar internamente se a conversa foi significativa.
// Caller ja garantiu user.Type==idoso e agent existente.
//
// Wrap com timeout 30s — Haiku tipicamente responde em 5-10s; 30s eh
// folga. Erro logado, nunca propaga pro fluxo do idoso.
func (h *Handler) maybeSnapshotIdoso(userID int64) {
	if h.orchestrator == nil || h.orchestrator.agent == nil {
		return
	}
	w := h.orchestrator.agent.snapshotWriter
	if w == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.MaybeUpdateSnapshot(ctx, userID); err != nil {
		log.Printf("[snapshot] user=%d: %v", userID, err)
	}
}

// isSignificantConversation eh a heuristica de "vale chamar Haiku?". Live
// no handler pra ficar perto de quem dispara. SnapshotWriter da Fase 5
// vai re-aplicar antes de chamar Haiku — defesa em profundidade.
//
// Criterios (OR):
//   - >=5 turnos do user no mesmo dia (timezone do user, ou BRT default).
//   - >=2 turnos com duracao >= 3min entre primeiro e ultimo.
//   - Pelo menos 1 chamada de alertar_familia hoje.
func isSignificantConversation(userTurns int, firstAt, lastAt time.Time, alertsToday int) bool {
	if userTurns >= 5 {
		return true
	}
	if userTurns >= 2 && lastAt.Sub(firstAt) >= 3*time.Minute {
		return true
	}
	if alertsToday > 0 {
		return true
	}
	return false
}

func (h *Handler) sendText(to types.JID, text string) {
	if err := h.sendWithRetry(to, text); err != nil {
		log.Printf("Error sending message to %s after retries: %v", to.User, err)
		return
	}
	h.persistOutbound(to.User, text)
}

// sendWithRetry tenta enviar ate 3x com backoff curto (0.5s, 1s). Falha
// transitoria de rede/whatsmeow nao pode descartar silenciosamente a resposta
// — sem retry, um erro pontual deixava o usuario sem resposta (e, como
// persistOutbound so roda apos sucesso, a fala sumia sem rastro no historico).
func (h *Handler) sendWithRetry(to types.JID, text string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		_, err = h.client.SendMessage(context.Background(), to, &waE2E.Message{
			Conversation: &text,
		})
		if err == nil {
			return nil
		}
		log.Printf("send attempt %d to %s failed: %v", attempt+1, to.User, err)
	}
	return err
}

func (h *Handler) SendTextToPhone(phone, text string) error {
	// Try to verify the number is on WhatsApp first
	results, err := h.client.IsOnWhatsApp(context.Background(), []string{"+" + phone})
	if err != nil {
		log.Printf("IsOnWhatsApp check failed for %s: %v", phone, err)
	} else if len(results) > 0 && results[0].IsIn {
		// Use the JID returned by WhatsApp (correct format)
		log.Printf("SendTextToPhone: %s is on WhatsApp as %s", phone, results[0].JID.String())
		if err := h.sendWithRetry(results[0].JID, text); err != nil {
			return err
		}
		h.persistOutbound(phone, text)
		return nil
	} else if len(results) > 0 && !results[0].IsIn {
		log.Printf("SendTextToPhone: %s is NOT on WhatsApp", phone)
		return fmt.Errorf("numero %s nao esta no WhatsApp", phone)
	}

	// Fallback: send directly
	jid := types.NewJID(phone, types.DefaultUserServer)
	log.Printf("SendTextToPhone: sending to %s (fallback)", jid.String())
	if err := h.sendWithRetry(jid, text); err != nil {
		return err
	}
	h.persistOutbound(phone, text)
	return nil
}

// persistOutbound grava em conversation_history toda mensagem efetivamente
// enviada a um usuario cadastrado, como turno role="assistant".
//
// Centralizado aqui — no unico ponto de transporte — de proposito: TODA fala
// do bot (resposta, lembrete de medicacao, escalacao, sintese, alerta a
// guardiao, magic link) deve fazer parte do historico. Caso contrario o LLM
// monta a janela de contexto sem a propria fala anterior e perde o fio — ex:
// ao confirmar um remedio que ele mesmo lembrou, respondia "tomar o que?".
// Persistir no transporte garante que nenhum sender novo possa esquecer.
//
// Lookup por telefone com variantes do 9o digito BR. Numero sem usuario
// (ex: resposta a desconhecido) eh ignorado — nao ha historico a manter.
// So eh chamado apos envio bem-sucedido: turno "fantasma" nao delivered nao
// entra no historico.
func (h *Handler) persistOutbound(phone, text string) {
	if text == "" {
		return
	}
	for _, variant := range normalizeBRPhone(phone) {
		if u, err := h.db.GetUserByPhone(variant); err == nil {
			h.db.AddConversationMessage(u.ID, "assistant", text)
			return
		}
	}
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
