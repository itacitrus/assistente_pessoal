package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// PairingManager dirige a máquina de estados do pareamento. A orquestração do
// whatsmeow (criar device, conectar, QR, PairPhone) fica atrás da interface
// PairingSession, então aqui testamos só transições de estado com uma sessão
// fake — sem rede.

type fakeSession struct {
	qr           chan QREvent
	result       chan PairResult
	phoneCalls   []string
	phoneCode    string
	phoneErr     error
	closed       bool
	mu           sync.Mutex
}

func newFakeSession() *fakeSession {
	return &fakeSession{qr: make(chan QREvent, 4), result: make(chan PairResult, 1), phoneCode: "ABCD1234"}
}

func (f *fakeSession) QR() <-chan QREvent { return f.qr }
func (f *fakeSession) Result() <-chan PairResult { return f.result }
func (f *fakeSession) RequestPhoneCode(_ context.Context, phone string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phoneCalls = append(f.phoneCalls, phone)
	return f.phoneCode, f.phoneErr
}
func (f *fakeSession) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condição não satisfeita a tempo")
}

func mgrWithSession(sess PairingSession) *PairingManager {
	return NewPairingManager(
		func(context.Context) (PairingSession, error) { return sess, nil },
		func(context.Context) error { return nil },
		func() string { return "" },
	)
}

func TestPairing_MetodoInvalido(t *testing.T) {
	m := mgrWithSession(newFakeSession())
	if err := m.Start("carta-pombo", ""); err == nil {
		t.Fatal("método inválido deveria falhar")
	}
	if s := m.Status().Status; s != "idle" {
		t.Fatalf("status após erro de validação = %q, want idle", s)
	}
}

func TestPairing_PhoneSemNumero(t *testing.T) {
	m := mgrWithSession(newFakeSession())
	if err := m.Start("phone", ""); err == nil {
		t.Fatal("method=phone sem número deveria falhar")
	}
	if err := m.Start("phone", "0619..."); err == nil {
		t.Fatal("número começando com 0 (não-internacional) deveria falhar")
	}
}

func TestPairing_QRFluxo(t *testing.T) {
	sess := newFakeSession()
	m := mgrWithSession(sess)
	if err := m.Start("qr", ""); err != nil {
		t.Fatalf("Start(qr): %v", err)
	}
	st := m.Status()
	if st.Status != "waiting" || st.Method != "qr" {
		t.Fatalf("após Start(qr): status=%q method=%q, want waiting/qr", st.Status, st.Method)
	}
	exp := time.Now().Add(60 * time.Second)
	sess.qr <- QREvent{Code: "2@abc,key,==", ExpiresAt: exp}
	eventually(t, func() bool { return m.Status().QRPNGBase64 != "" })
	if got := m.Status().QRPNGBase64; got[:11] != "data:image/" {
		t.Fatalf("QRPNGBase64 não é data-URI: %.20q", got)
	}
}

func TestPairing_PhoneFluxoGeraCodigo(t *testing.T) {
	sess := newFakeSession()
	sess.phoneCode = "WXYZ7788"
	m := mgrWithSession(sess)
	if err := m.Start("phone", "5561999887766"); err != nil {
		t.Fatalf("Start(phone): %v", err)
	}
	st := m.Status()
	if st.Method != "phone" || st.PairCode != "WXYZ7788" {
		t.Fatalf("após Start(phone): method=%q code=%q, want phone/WXYZ7788", st.Method, st.PairCode)
	}
	if len(sess.phoneCalls) != 1 || sess.phoneCalls[0] != "5561999887766" {
		t.Fatalf("RequestPhoneCode chamado com %v", sess.phoneCalls)
	}
}

func TestPairing_SucessoVaiParaPaired(t *testing.T) {
	sess := newFakeSession()
	m := mgrWithSession(sess)
	_ = m.Start("qr", "")
	sess.result <- PairResult{JID: "+5561999887766"}
	eventually(t, func() bool { return m.Status().Status == "paired" })
	st := m.Status()
	if st.ConnectedAs != "+5561999887766" {
		t.Fatalf("connected_as = %q", st.ConnectedAs)
	}
	if st.QRPNGBase64 != "" || st.PairCode != "" {
		t.Fatal("ao parear, QR e código devem ser limpos")
	}
}

func TestPairing_ErroVaiParaError(t *testing.T) {
	sess := newFakeSession()
	m := mgrWithSession(sess)
	_ = m.Start("qr", "")
	sess.result <- PairResult{Err: errors.New("expirou")}
	eventually(t, func() bool { return m.Status().Status == "error" })
	if e := m.Status().Error; e == "" {
		t.Fatal("status error deveria carregar mensagem")
	}
}

func TestPairing_IdleRefleteConexaoAtual(t *testing.T) {
	m := NewPairingManager(
		func(context.Context) (PairingSession, error) { return newFakeSession(), nil },
		func(context.Context) error { return nil },
		func() string { return "+5561000000000" },
	)
	st := m.Status()
	if st.Status != "idle" || st.ConnectedAs != "+5561000000000" {
		t.Fatalf("idle status=%q connected_as=%q", st.Status, st.ConnectedAs)
	}
}

func TestPairing_ResetChamaLogout(t *testing.T) {
	var logoutCalls int
	m := NewPairingManager(
		func(context.Context) (PairingSession, error) { return newFakeSession(), nil },
		func(context.Context) error { logoutCalls++; return nil },
		func() string { return "" },
	)
	if err := m.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if logoutCalls != 1 {
		t.Fatalf("logout chamado %d vezes, want 1", logoutCalls)
	}
	if s := m.Status().Status; s != "idle" {
		t.Fatalf("após reset status=%q, want idle", s)
	}
}
