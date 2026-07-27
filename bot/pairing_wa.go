package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// waPairingSession é a PairingSession real: cria um device limpo, conecta um
// client não-autenticado e traduz o canal de QR do whatsmeow no contrato que o
// PairingManager consome. No sucesso, publica o client vivo no holder para que
// handler/watchdog/scheduler passem a usá-lo sem reiniciar o processo.
type waPairingSession struct {
	client *whatsmeow.Client
	holder *ClientHolder

	qr     chan QREvent
	result chan PairResult
	ready  chan struct{} // fechado no primeiro QR — PairPhone exige a conexão pronta

	closeOnce sync.Once
	readyOnce sync.Once
}

// newWAPairingBegin devolve o begin injetado no PairingManager. Cada chamada
// inicia uma tentativa isolada em um device novo.
func newWAPairingBegin(container *sqlstore.Container, clientLog waLog.Logger, handler *Handler, holder *ClientHolder) func(context.Context) (PairingSession, error) {
	return func(ctx context.Context) (PairingSession, error) {
		device := container.NewDevice()
		client := whatsmeow.NewClient(device, clientLog)
		// O handler de mensagens já fica anexado: assim que parear, o tráfego
		// inbound é tratado sem re-wiring.
		client.AddEventHandler(handler.HandleEvent)

		qrChan, err := client.GetQRChannel(ctx)
		if err != nil {
			return nil, fmt.Errorf("abrir canal de QR: %w", err)
		}
		if err := client.Connect(); err != nil {
			return nil, fmt.Errorf("conectar: %w", err)
		}

		s := &waPairingSession{
			client: client,
			holder: holder,
			qr:     make(chan QREvent, 8),
			result: make(chan PairResult, 1),
			ready:  make(chan struct{}),
		}
		go s.readLoop(qrChan)
		return s, nil
	}
}

func (s *waPairingSession) QR() <-chan QREvent        { return s.qr }
func (s *waPairingSession) Result() <-chan PairResult { return s.result }

// RequestPhoneCode espera o primeiro QR (conexão pronta) e pede o código de 8
// dígitos ao servidor. O displayName precisa ser "Browser (OS)" válido, senão a
// lib recebe 400.
func (s *waPairingSession) RequestPhoneCode(ctx context.Context, phone string) (string, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return "", fmt.Errorf("conexão não ficou pronta a tempo: %w", ctx.Err())
	}
	code, err := s.client.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", fmt.Errorf("gerar código de pareamento: %w", err)
	}
	return code, nil
}

// readLoop traduz os itens do canal de QR do whatsmeow em QREvent/PairResult.
func (s *waPairingSession) readLoop(qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		switch item.Event {
		case "code":
			s.readyOnce.Do(func() { close(s.ready) })
			// Fallback: se a página admin estiver indisponível, o código bruto
			// no log permite gerar o QR em qualquer gerador externo.
			log.Printf("pairing: novo QR (expira em %s) — código bruto: %s", item.Timeout, item.Code)
			select {
			case s.qr <- QREvent{Code: item.Code, ExpiresAt: time.Now().Add(item.Timeout)}:
			default: // consumidor lento: descarta o mais antigo, mantém o fluxo
			}
		case whatsmeow.QRChannelSuccess.Event:
			jid := ""
			if id := s.client.Store.ID; id != nil {
				jid = "+" + id.User
			}
			s.holder.Set(s.client) // client vivo agora é este
			log.Printf("pairing: pareado com sucesso como %s", jid)
			s.emit(PairResult{JID: jid})
			return
		default: // timeout, err-unexpected-state, etc.
			s.client.Disconnect()
			s.emit(PairResult{Err: fmt.Errorf("pareamento não concluído: %s", item.Event)})
			return
		}
	}
	// Canal fechado sem sucesso.
	s.emit(PairResult{Err: fmt.Errorf("canal de pareamento encerrado")})
}

func (s *waPairingSession) emit(r PairResult) {
	select {
	case s.result <- r:
	default:
	}
}

// Close aborta a sessão: desconecta o client não-autenticado. Idempotente.
func (s *waPairingSession) Close() {
	s.closeOnce.Do(func() {
		if s.client.IsConnected() {
			s.client.Disconnect()
		}
	})
}
