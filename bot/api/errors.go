package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// ErrorEnvelope eh o shape de TODA resposta de erro do /api/v1/*. Frontend
// pode parsear `error.code` deterministicamente sem inferir do http status.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody descreve o erro pro consumidor.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Codigos canonicos. Match com plano §4.6. Manter em sync com frontend.
const (
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeValidation       = "validation_error"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeInvalidPhone     = "invalid_phone"
	CodeInvalidToken     = "invalid_token"
	CodeTokenExpired     = "token_expired"
	CodeAlreadyUsed      = "already_used"
	CodeConsentRevoked   = "consent_revoked"
	CodePhoneInUse       = "phone_already_in_use"
	CodeOriginForbidden  = "origin_forbidden"
	CodeMedicationDup    = "medication_duplicate"
)

// writeError serializa um envelope JSON com o status http apropriado.
// log apenas em 5xx — 4xx nao polui log (e o atacante pode forcar muitos).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status >= 500 {
		log.Printf("api 5xx: code=%s message=%s", code, message)
	}
	_ = json.NewEncoder(w).Encode(ErrorEnvelope{
		Error: ErrorBody{Code: code, Message: message},
	})
}

// writeJSON encapsula o pattern: status + Content-Type + Encode.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("api writeJSON: %v", err)
	}
}
