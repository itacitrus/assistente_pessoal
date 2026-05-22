package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// =========================================================================
// Pessoas na vida do proprio titular — /api/v1/me/people
// =========================================================================
//
// Curadoria manual do card "O que o Zello sabe sobre voce" > "Pessoas na sua
// vida". Grava memorias (relacao/social_context) que o assistente passa a
// conhecer nas conversas. Leitura continua em GET /me/profile-facts.

const (
	personNameMax   = 80
	personDetailMax = 500
)

// handleMyPeopleCollection roteia POST (criar), PATCH (editar) e DELETE.
func (s *Server) handleMyPeopleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateMyPerson(w, r)
	case http.MethodPatch:
		s.handleUpdateMyPerson(w, r)
	case http.MethodDelete:
		s.handleDeleteMyPerson(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}

// validatePersonFact valida nome/detalhe/tipo do corpo. Devolve "" se ok.
func validatePersonFact(req *PersonFactRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.Detail = strings.TrimSpace(req.Detail)
	if req.Name == "" {
		return "Informe o nome da pessoa."
	}
	if utf8.RuneCountInString(req.Name) > personNameMax {
		return "O nome é muito longo."
	}
	if strings.ContainsAny(req.Name, "\n\r") {
		return "O nome não pode ter quebras de linha."
	}
	if utf8.RuneCountInString(req.Detail) > personDetailMax {
		return "A descrição é muito longa."
	}
	if req.Type != PersonFactTypeRelacao && req.Type != PersonFactTypePessoa {
		return "Tipo inválido. Use \"relacao\" ou \"pessoa\"."
	}
	return ""
}

func (s *Server) handleCreateMyPerson(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var req PersonFactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	if msg := validatePersonFact(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	if err := s.store.CreatePersonFact(r.Context(), user.ID, req); err != nil {
		switch {
		case errors.Is(err, ErrConflict):
			writeError(w, http.StatusConflict, CodeValidation, "Já existe alguém com esse nome nessa lista.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao salvar pessoa.")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateMyPerson(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var req PersonFactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	if msg := validatePersonFact(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	if strings.TrimSpace(req.OriginalCategory) == "" || strings.TrimSpace(req.OriginalKey) == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "Identificador da entrada ausente.")
		return
	}
	if err := s.store.UpdatePersonFact(r.Context(), user.ID, req); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, CodeNotFound, "Pessoa não encontrada.")
		case errors.Is(err, ErrConflict):
			writeError(w, http.StatusConflict, CodeValidation, "Já existe alguém com esse nome nessa lista.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao editar pessoa.")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteMyPerson — DELETE /me/people?category=<cat>&key=<key>.
// Identidade vai na query (keys arbitrarias com unicode/espacos nao cabem
// bem em path param).
func (s *Server) handleDeleteMyPerson(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if category == "" || key == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "Informe category e key.")
		return
	}
	if err := s.store.DeletePersonFact(r.Context(), user.ID, category, key); err != nil {
		switch {
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao remover pessoa.")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
