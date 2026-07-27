package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

// pairingTTL é a folga sobre o limite do WhatsApp (~160s até o websocket de
// login fechar). O contexto da sessão morre um pouco depois para não cortar o
// último QR válido.
const pairingTTL = 170 * time.Second

// QREvent é uma rotação do QR: a string a renderizar e quando expira.
type QREvent struct {
	Code      string
	ExpiresAt time.Time
}

// PairResult é o desfecho único de uma tentativa: sucesso (JID conectado) ou erro.
type PairResult struct {
	JID string
	Err error
}

// PairingSession abstrai uma tentativa de pareamento já conectada. Isola a
// orquestração do whatsmeow (device novo, Connect, GetQRChannel, PairPhone,
// swap do holder) para o PairingManager ser testável sem rede.
type PairingSession interface {
	QR() <-chan QREvent
	RequestPhoneCode(ctx context.Context, phone string) (string, error)
	Result() <-chan PairResult
	Close()
}

// PairingStatus é o snapshot que a API devolve ao painel.
type PairingStatus struct {
	Status      string     `json:"status"` // idle|starting|waiting|paired|error
	Method      string     `json:"method,omitempty"`
	PairCode    string     `json:"pair_code,omitempty"`
	QRPNGBase64 string     `json:"qr_png_base64,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ConnectedAs string     `json:"connected_as,omitempty"`
	Error       string     `json:"error,omitempty"`
}

var onlyDigits = regexp.MustCompile(`\D`)

// PairingManager mantém a máquina de estados e dirige uma PairingSession por vez.
type PairingManager struct {
	mu     sync.Mutex
	status PairingStatus

	begin      func(ctx context.Context) (PairingSession, error)
	logout     func(ctx context.Context) error
	currentJID func() string

	sess   PairingSession
	cancel context.CancelFunc
}

// NewPairingManager injeta os colaboradores: begin cria uma sessão conectada,
// logout desconecta a conta atual, currentJID informa a conta viva ("" se
// deslogado).
func NewPairingManager(
	begin func(ctx context.Context) (PairingSession, error),
	logout func(ctx context.Context) error,
	currentJID func() string,
) *PairingManager {
	return &PairingManager{
		begin:      begin,
		logout:     logout,
		currentJID: currentJID,
		status:     PairingStatus{Status: "idle"},
	}
}

// Status devolve um snapshot. Em idle, reflete a conexão atual (connected_as).
func (m *PairingManager) Status() PairingStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	if s.Status == "idle" {
		s.ConnectedAs = m.currentJID()
	}
	return s
}

// Start inicia (ou reinicia) uma tentativa de pareamento. method ∈ {phone, qr};
// para phone, o número (dígitos, internacional) é obrigatório.
func (m *PairingManager) Start(method, phone string) error {
	method = strings.TrimSpace(method)
	if method != "phone" && method != "qr" {
		return fmt.Errorf("método de pareamento inválido: %q", method)
	}
	var digits string
	if method == "phone" {
		digits = onlyDigits.ReplaceAllString(phone, "")
		if len(digits) < 7 {
			return fmt.Errorf("número de telefone muito curto")
		}
		if strings.HasPrefix(digits, "0") {
			return fmt.Errorf("número deve ser internacional (com DDI), sem 0 inicial")
		}
	}

	m.mu.Lock()
	// Encerra qualquer sessão em andamento antes de começar outra.
	m.abortLocked()

	ctx, cancel := context.WithTimeout(context.Background(), pairingTTL)
	sess, err := m.begin(ctx)
	if err != nil {
		cancel()
		m.status = PairingStatus{Status: "error", Error: err.Error()}
		m.mu.Unlock()
		return err
	}
	m.sess = sess
	m.cancel = cancel
	m.status = PairingStatus{Status: "waiting", Method: method}
	go m.drive(sess)
	m.mu.Unlock()

	// Fora do lock: o código por telefone espera o primeiro QR chegar (exigência
	// da lib) e faz um round-trip de rede — não deve congelar o Status().
	if method == "phone" {
		code, err := sess.RequestPhoneCode(ctx, digits)
		m.mu.Lock()
		if m.sess == sess { // ainda é a sessão corrente
			if err != nil {
				m.abortLocked()
				m.status = PairingStatus{Status: "error", Error: err.Error()}
			} else {
				m.status.PairCode = code
			}
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// drive consome a sessão até o desfecho, atualizando o status a cada rotação de
// QR e no resultado final.
func (m *PairingManager) drive(sess PairingSession) {
	qr := sess.QR()
	res := sess.Result()
	for {
		select {
		case ev, ok := <-qr:
			if !ok {
				qr = nil
				continue
			}
			png, err := qrPNGDataURI(ev.Code)
			if err != nil {
				log.Printf("pairing: falha ao renderizar QR: %v", err)
				continue
			}
			exp := ev.ExpiresAt
			m.mu.Lock()
			if m.sess == sess && m.status.Status == "waiting" {
				m.status.QRPNGBase64 = png
				m.status.ExpiresAt = &exp
			}
			m.mu.Unlock()
		case r := <-res:
			m.mu.Lock()
			if m.sess == sess {
				if r.Err != nil {
					m.status = PairingStatus{Status: "error", Error: r.Err.Error()}
				} else {
					m.status = PairingStatus{Status: "paired", ConnectedAs: r.JID}
				}
				m.sess = nil
			}
			m.mu.Unlock()
			return
		}
	}
}

// Reset desloga a conta atual e volta ao estado idle, pronto para novo Start.
func (m *PairingManager) Reset(ctx context.Context) error {
	m.mu.Lock()
	m.abortLocked()
	m.mu.Unlock()

	if err := m.logout(ctx); err != nil {
		m.mu.Lock()
		m.status = PairingStatus{Status: "error", Error: err.Error()}
		m.mu.Unlock()
		return err
	}
	m.mu.Lock()
	m.status = PairingStatus{Status: "idle"}
	m.mu.Unlock()
	return nil
}

// abortLocked encerra a sessão ativa. Requer m.mu travado.
func (m *PairingManager) abortLocked() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.sess != nil {
		m.sess.Close()
		m.sess = nil
	}
}
