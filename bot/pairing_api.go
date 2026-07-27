package main

import (
	"context"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// apiPairer adapta o PairingManager (pacote main) à interface api.Pairer,
// convertendo o status entre os dois pacotes. Mantém pairing.go livre de
// dependência do pacote api.
type apiPairer struct{ m *PairingManager }

func newAPIPairer(m *PairingManager) api.Pairer { return apiPairer{m: m} }

func (a apiPairer) Start(_ context.Context, method, phone string) (api.PairingStatus, error) {
	if err := a.m.Start(method, phone); err != nil {
		return api.PairingStatus{}, err
	}
	return toAPIPairingStatus(a.m.Status()), nil
}

func (a apiPairer) Status() api.PairingStatus {
	return toAPIPairingStatus(a.m.Status())
}

func (a apiPairer) Reset(ctx context.Context) (api.PairingStatus, error) {
	if err := a.m.Reset(ctx); err != nil {
		return api.PairingStatus{}, err
	}
	return toAPIPairingStatus(a.m.Status()), nil
}

func toAPIPairingStatus(s PairingStatus) api.PairingStatus {
	return api.PairingStatus{
		Status:      s.Status,
		Method:      s.Method,
		PairCode:    s.PairCode,
		QRPNGBase64: s.QRPNGBase64,
		ExpiresAt:   s.ExpiresAt,
		ConnectedAs: s.ConnectedAs,
		Error:       s.Error,
	}
}
