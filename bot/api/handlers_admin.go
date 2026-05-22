package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Area admin do painel. Privilegio vem do allowlist ADMIN_PHONES (env, fora
// do banco). O gate SEMPRE usa o dono REAL da sessao (realUserFromContext),
// nunca o efetivo — assim o admin nao perde acesso a area admin enquanto esta
// "vendo como" outra pessoa, e um usuario impersonado nao herda o privilegio.

// requireAdmin garante que o dono real da sessao eh admin. Em caso negativo
// escreve a resposta de erro e retorna ok=false.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*User, bool) {
	real := realUserFromContext(r.Context())
	if real == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return nil, false
	}
	if !s.isAdmin(real) {
		writeError(w, http.StatusForbidden, CodeForbidden, "Acesso restrito.")
		return nil, false
	}
	return real, true
}

// handleAdminUsers — GET /api/v1/admin/users?q=<busca>. Lista usuarios por
// nome/telefone pra tela de admin. Somente admin.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	users, err := s.store.SearchUsers(r.Context(), q, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar usuários.")
		return
	}
	if users == nil {
		users = []User{}
	}
	writeJSON(w, http.StatusOK, AdminUsersResponse{Users: users})
}

// handleAdminImpersonate — POST/DELETE /api/v1/admin/impersonate. POST com
// {"user_id": N} liga a visao "ver como"; DELETE desliga. Somente admin.
// RequireOrigin (CSRF) cobre POST/DELETE no Mount. A impersonacao fica gravada
// na sessao do admin — o RequireAuth passa a resolver o usuario efetivo pro
// alvo em toda request seguinte.
func (s *Server) handleAdminImpersonate(w http.ResponseWriter, r *http.Request) {
	real, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	sessID := sessionIDFromContext(r.Context())
	if sessID == 0 {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Sessão não resolvida.")
		return
	}

	switch r.Method {
	case http.MethodPost:
		var body struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
			return
		}
		if body.UserID <= 0 {
			writeError(w, http.StatusBadRequest, CodeValidation, "user_id é obrigatório.")
			return
		}
		target, err := s.store.GetUserByID(r.Context(), body.UserID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, CodeNotFound, "Usuário não encontrado.")
				return
			}
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao localizar usuário.")
			return
		}
		// Impersonar a si mesmo = sair da visao (limpa). Evita estado redundante.
		setID := target.ID
		if target.ID == real.ID {
			setID = 0
		}
		if err := s.store.SetSessionImpersonation(r.Context(), sessID, setID); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao ativar a visão.")
			return
		}
		s.store.Audit(r.Context(), real.ID, "admin_impersonate_start", target.PhoneNumber,
			fmt.Sprintf("target_id=%d", target.ID))
		writeJSON(w, http.StatusOK, target)

	case http.MethodDelete:
		if err := s.store.SetSessionImpersonation(r.Context(), sessID, 0); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao sair da visão.")
			return
		}
		s.store.Audit(r.Context(), real.ID, "admin_impersonate_stop", "", "")
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}
