package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// limites de rate. Centralizados pra ajustar facil.
const (
	rateLimitPhonePerHour = 3
	rateLimitIPPerHour    = 10
)

// requestLinkBody eh o input de POST /auth/request-link.
type requestLinkBody struct {
	Phone string `json:"phone"`
}

// handleRequestLink dispara o magic link via WhatsApp. Resposta opaca
// (200 sempre que rate limit ok) — atacante nao consegue enumerar phones
// cadastrados.
//
// Auditoria: web_login_requested (sucesso ou phone nao cadastrado).
//   - userID = id real se existir, 0 se nao existe (nao revela na resposta)
func (s *Server) handleRequestLink(w http.ResponseWriter, r *http.Request) {
	var body requestLinkBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	phone := normalizePhone(body.Phone)
	if !validBRPhone(phone) {
		writeError(w, http.StatusBadRequest, CodeInvalidPhone, "Telefone inválido. Use 55 + DDD + número.")
		return
	}
	ip := clientIP(r)

	// Rate limit — checa antes de gravar tentativa pra nao inflar contador
	// quando ja deveria bloquear.
	ctx := r.Context()
	if n, _ := s.store.CountRecentLoginAttempts(ctx, phone, hourWindow); n >= rateLimitPhonePerHour {
		s.store.Audit(ctx, 0, "web_login_failed", phone, "reason=rate_limit_phone")
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, "Muitas tentativas. Tente de novo em 1 hora.")
		return
	}
	if n, _ := s.store.CountRecentLoginAttemptsByIP(ctx, ip, hourWindow); n >= rateLimitIPPerHour {
		s.store.Audit(ctx, 0, "web_login_failed", phone, "reason=rate_limit_ip|ip="+ip)
		writeError(w, http.StatusTooManyRequests, CodeRateLimited, "Muitas tentativas deste dispositivo. Tente de novo em 1 hora.")
		return
	}
	if err := s.store.RecordLoginAttempt(ctx, phone, ip); err != nil {
		log.Printf("api: record login attempt: %v", err)
		// Nao falha o flow — rate limit eh defesa em profundidade.
	}

	user, err := s.store.GetUserByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Audit pra observabilidade, mas resposta opaca.
			s.store.Audit(ctx, 0, "web_login_requested", phone, "reason=phone_not_found")
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar usuário.")
		return
	}

	sessID, plaintext, err := s.store.CreatePendingSession(ctx, user.ID, ip, r.UserAgent())
	if err != nil {
		log.Printf("api: create pending session: %v", err)
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao iniciar sessão.")
		return
	}

	url := s.webBaseURL + "/auth/verify?token=" + plaintext
	msg := fmt.Sprintf(
		"Oi %s! Aqui está seu link de acesso ao painel do Zello — vale por 15 minutos:\n\n%s\n\nSe não foi você que pediu, pode ignorar.",
		user.Name, url,
	)
	if err := s.store.SendMagicLink(ctx, user.PhoneNumber, msg); err != nil {
		log.Printf("api: send magic link to %s failed: %v", user.PhoneNumber, err)
		// Audit registra falha mas a resposta segue 200 — opacidade.
		s.store.Audit(ctx, user.ID, "web_login_failed", user.PhoneNumber,
			fmt.Sprintf("session_id=%d|reason=send_failed", sessID))
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	s.store.Audit(ctx, user.ID, "web_login_requested", user.PhoneNumber,
		fmt.Sprintf("session_id=%d", sessID))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// verifyBody eh o input de POST /auth/verify.
type verifyBody struct {
	Token string `json:"token"`
}

// handleVerify ativa a sessao e retorna cookie + user. POST (nao GET) —
// previews de link no WhatsApp/Telegram nao consomem o token.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	var body verifyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	body.Token = strings.TrimSpace(body.Token)
	if body.Token == "" {
		writeError(w, http.StatusBadRequest, CodeInvalidToken, "Token ausente.")
		return
	}

	ctx := r.Context()
	userID, sessID, err := s.store.ActivateSession(ctx, body.Token)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusBadRequest, CodeInvalidToken, "Link inválido ou já consumido.")
		case errors.Is(err, ErrSessionExpired):
			writeError(w, http.StatusGone, CodeTokenExpired, "Link expirado. Peça um novo pelo painel.")
		case errors.Is(err, ErrSessionInvalid):
			writeError(w, http.StatusConflict, CodeAlreadyUsed, "Link ja usado. Peca um novo pelo painel.")
		default:
			log.Printf("api: activate session: %v", err)
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao validar link.")
		}
		return
	}

	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Usuário não encontrado.")
		return
	}

	setSessionCookie(w, body.Token, s.cookieSecure, s.cookieDomain)
	s.store.Audit(ctx, user.ID, "web_login_succeeded", user.PhoneNumber,
		fmt.Sprintf("session_id=%d", sessID))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// handleLogout revoga a sessao atual e limpa cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessID := sessionIDFromContext(r.Context())
	if sessID > 0 {
		_ = s.store.RevokeSession(r.Context(), sessID)
	}
	user := userFromContext(r.Context())
	if user != nil {
		s.store.Audit(r.Context(), user.ID, "web_session_revoked", user.PhoneNumber,
			fmt.Sprintf("session_id=%d", sessID))
	}
	clearSessionCookie(w, s.cookieSecure, s.cookieDomain)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleMe retorna o usuario logado (efetivo) + contexto de admin. Durante
// uma impersonacao, `user` eh o alvo (painel que o admin esta vendo) e
// `viewing_as` o identifica; `is_admin` segue refletindo o dono real.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		// RequireAuth ja teria pego — defensivo.
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	real := realUserFromContext(r.Context())
	resp := MeResponse{User: user, IsAdmin: s.isAdmin(real)}
	if real != nil && user.ID != real.ID {
		resp.ViewingAs = &ViewingAs{ID: user.ID, Name: user.Name, Phone: user.PhoneNumber}
	}
	writeJSON(w, http.StatusOK, resp)
}
