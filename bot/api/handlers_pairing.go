package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// PairingStatus é o DTO devolvido ao painel admin com o estado do pareamento do
// WhatsApp. Espelha o status do PairingManager (pacote main), que é adaptado
// para esta interface em main.go.
type PairingStatus struct {
	Status      string     `json:"status"` // idle|starting|waiting|paired|error
	Method      string     `json:"method,omitempty"`
	PairCode    string     `json:"pair_code,omitempty"`
	QRPNGBase64 string     `json:"qr_png_base64,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ConnectedAs string     `json:"connected_as,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// Pairer é a fronteira entre a API e o gerenciador de pareamento do whatsmeow
// (implementado no pacote main). Consumer-defines-interface: só o que os
// handlers admin precisam.
type Pairer interface {
	Start(ctx context.Context, method, phone string) (PairingStatus, error)
	Status() PairingStatus
	Reset(ctx context.Context) (PairingStatus, error)
}

// handlePairingStatus — GET /api/v1/admin/pairing/status. Somente admin. O
// painel faz polling deste endpoint enquanto o pareamento está em andamento.
func (s *Server) handlePairingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.pairer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "Pareamento indisponível.")
		return
	}
	writeJSON(w, http.StatusOK, s.pairer.Status())
}

// handlePairingStart — POST /api/v1/admin/pairing/start. Somente admin. Body:
// {method:"phone"|"qr", phone?}. Inicia (ou reinicia) o pareamento.
func (s *Server) handlePairingStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	real, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if s.pairer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "Pareamento indisponível.")
		return
	}
	var body struct {
		Method string `json:"method"`
		Phone  string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	status, err := s.pairer.Start(r.Context(), body.Method, body.Phone)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	s.store.Audit(r.Context(), real.ID, "wa_pairing_start", "", "method="+body.Method)
	writeJSON(w, http.StatusOK, status)
}

// handlePairingReset — POST /api/v1/admin/pairing/reset. Somente admin. Desloga
// a conta atual e volta ao modo pareamento (para trocar de número).
func (s *Server) handlePairingReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	real, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if s.pairer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeInternal, "Pareamento indisponível.")
		return
	}
	status, err := s.pairer.Reset(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	s.store.Audit(r.Context(), real.ID, "wa_pairing_reset", "", "")
	writeJSON(w, http.StatusOK, status)
}
