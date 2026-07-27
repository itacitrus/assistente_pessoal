package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// totpSkewSteps tolera ±1 passo de 30s (drift de relógio do celular).
const totpSkewSteps = 1

// dummyTOTPSecret é usado para equalizar o custo de verificação quando o TOTP
// está desabilitado ou o telefone não é admin — evita vazar por timing se o
// telefone informado é admin.
const dummyTOTPSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// adminLoginBody é o input de POST /auth/admin-login.
type adminLoginBody struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// handleAdminLogin autentica o admin via TOTP, sem depender do WhatsApp. Mesmo
// motor de sessão do magic link (CreatePendingSession + ActivateSession), só
// que a prova de identidade é o código do app autenticador em vez do link.
//
// Resposta de falha é sempre genérica ("Telefone ou código inválidos") e a
// verificação TOTP roda mesmo para telefone não-admin / TOTP desabilitado, para
// não vazar por timing nem por mensagem se o telefone é admin.
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	var body adminLoginBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}

	ctx := r.Context()
	phone := normalizePhone(body.Phone)
	ip := clientIP(r)

	// Rate limit por telefone e por IP — o endpoint é alvo de brute force.
	if n, _ := s.store.CountRecentLoginAttempts(ctx, phone, hourWindow); n >= rateLimitPhonePerHour {
		s.store.Audit(ctx, 0, "admin_login_failed", phone, "reason=rate_limit_phone")
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, "Muitas tentativas. Tente de novo em 1 hora.")
		return
	}
	if n, _ := s.store.CountRecentLoginAttemptsByIP(ctx, ip, hourWindow); n >= rateLimitIPPerHour {
		s.store.Audit(ctx, 0, "admin_login_failed", phone, "reason=rate_limit_ip|ip="+ip)
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, "Muitas tentativas deste dispositivo. Tente de novo em 1 hora.")
		return
	}
	if err := s.store.RecordLoginAttempt(ctx, phone, ip); err != nil {
		log.Printf("api: record admin login attempt: %v", err)
	}

	isAdminPhone := s.isAdminPhone(phone)

	// Sempre valida TOTP (contra o segredo real ou um dummy) para equalizar
	// timing. secretInUse decide o segredo; a decisão de autenticar combina
	// telefone-admin + segredo-configurado + código-válido + não-replay.
	secretInUse := dummyTOTPSecret
	enabled := s.adminTOTPSecret != ""
	if enabled && isAdminPhone {
		secretInUse = s.adminTOTPSecret
	}
	step, codeOK := ValidateTOTP(secretInUse, body.Code, time.Now(), totpSkewSteps)

	fail := func(reason string) {
		s.store.Audit(ctx, 0, "admin_login_failed", phone, "reason="+reason)
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Telefone ou código inválidos.")
	}

	if !enabled {
		fail("disabled")
		return
	}
	if !isAdminPhone {
		fail("not_admin")
		return
	}
	if !codeOK {
		fail("bad_code")
		return
	}
	// Anti-replay: rejeita reuso do mesmo passo de tempo (ou anterior).
	if !s.consumeTOTPStep(step) {
		fail("replay")
		return
	}

	user, err := s.store.GetUserByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			fail("no_user")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao localizar usuário.")
		return
	}

	sessID, plaintext, err := s.store.CreatePendingSession(ctx, user.ID, ip, r.UserAgent())
	if err != nil {
		log.Printf("api: admin-login create session: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao iniciar sessão.")
		return
	}
	if _, _, err := s.store.ActivateSession(ctx, plaintext); err != nil {
		log.Printf("api: admin-login activate session: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao ativar sessão.")
		return
	}

	setSessionCookie(w, plaintext, s.cookieSecure, s.cookieDomain)
	s.store.Audit(ctx, user.ID, "admin_login_succeeded", user.PhoneNumber,
		fmt.Sprintf("session_id=%d", sessID))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// consumeTOTPStep aceita o passo de tempo `step` apenas se for estritamente
// maior que o último consumido — impede replay do mesmo código dentro da
// janela de validade. Retorna false se já foi usado.
func (s *Server) consumeTOTPStep(step int64) bool {
	s.adminTOTPMu.Lock()
	defer s.adminTOTPMu.Unlock()
	if step <= s.adminTOTPLastStep {
		return false
	}
	s.adminTOTPLastStep = step
	return true
}
