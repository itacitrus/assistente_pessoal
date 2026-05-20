package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// handleCreateDependent — POST /family/dependents.
// Auth: qualquer usuario logado (responsaveis e tambem comuns que decidem
// adicionar familiar; o user.Type podera ser promovido para 'responsavel'
// pelo admin/UI, fora do escopo desta fase).
func (s *Server) handleCreateDependent(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	var req CreateDependentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON invalido.")
		return
	}
	req.Phone = normalizePhone(req.Phone)
	if msg := validateCreateDependent(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	dep, link, err := s.store.CreateDependent(r.Context(), user.ID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrConflict):
			// Phone ja em uso — distinguimos do generico "ja existe vinculo".
			writeError(w, http.StatusConflict, CodePhoneInUse, "Esse telefone ja esta cadastrado.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao cadastrar dependente.")
		}
		return
	}
	// Audit feito no adapter (LogFamilyLinkCreated). Aqui nao duplicamos.
	writeJSON(w, http.StatusCreated, CreateDependentResponse{User: *dep, Link: *link})
}

// handleListDependents — GET /family/dependents. Auth: qualquer logado.
// Retorna lista vazia se o user nao tem dependentes.
func (s *Server) handleListDependents(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	deps, err := s.store.ListDependents(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao listar dependentes.")
		return
	}
	if deps == nil {
		deps = []DependentSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dependents": deps})
}

// handleUpdateDependent — PATCH /family/dependents/{id}. Apenas guardian
// daquele dependente.
func (s *Server) handleUpdateDependent(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Voce nao eh responsavel por este dependente.")
		return
	}
	var p DependentPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON invalido.")
		return
	}
	if msg := validateDependentPatch(&p); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	updated, err := s.store.UpdateDependent(r.Context(), user.ID, depID, p)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, CodeNotFound, "Dependente nao encontrado.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao atualizar dependente.")
		}
		return
	}
	s.store.Audit(r.Context(), user.ID, "user_preferences_updated", updated.PhoneNumber,
		"on_dependent="+strconv.FormatInt(depID, 10))
	writeJSON(w, http.StatusOK, updated)
}

// handleUpdateNotify — PATCH /family/links/{id}/notify. Apenas guardian
// dono daquele link.
func (s *Server) handleUpdateNotify(w http.ResponseWriter, r *http.Request, linkID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	link, err := s.store.GetFamilyLink(r.Context(), linkID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Vinculo familiar nao encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar vinculo.")
		return
	}
	if link.GuardianID != user.ID {
		writeError(w, http.StatusForbidden, CodeForbidden, "Voce nao eh responsavel por este vinculo.")
		return
	}

	var p NotifyPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON invalido.")
		return
	}
	updated, err := s.store.UpdateNotifyPrefs(r.Context(), user.ID, linkID, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao atualizar preferencias.")
		return
	}
	// Audit (family_notify_prefs_updated) feito pelo adapter — passa diff.
	writeJSON(w, http.StatusOK, updated)
}

// handleDependentStatus — GET /family/dependents/{id}/status?days=14.
// Cache em memoria 60s por (depID, days) pra cortar custo Sonnet em
// refresh-loop.
func (s *Server) handleDependentStatus(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	days := parseDaysQuery(r, 14, 90)

	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Voce nao eh responsavel por este dependente.")
		return
	}
	consent, err := s.store.GetDependentConsent(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar consentimento.")
		return
	}
	if consent == "revoked" {
		writeError(w, http.StatusForbidden, CodeConsentRevoked,
			"O dependente revogou o consentimento de relatorio agregado.")
		return
	}

	cacheKey := fmt.Sprintf("%d-%d", depID, days)
	if cached, ok := s.statusCache.Get(cacheKey); ok {
		s.store.Audit(r.Context(), user.ID, "status_dependente_consulted", "",
			fmt.Sprintf("dependent_id=%d|days=%d|cache=hit", depID, days))
		writeJSON(w, http.StatusOK, cached)
		return
	}
	resp, err := s.store.BuildDependentStatus(r.Context(), user.ID, depID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao gerar status.")
		return
	}
	s.statusCache.Set(cacheKey, resp)
	s.store.Audit(r.Context(), user.ID, "status_dependente_consulted", "",
		fmt.Sprintf("dependent_id=%d|days=%d|cache=miss", depID, days))
	writeJSON(w, http.StatusOK, resp)
}

// handleDependentTimeline — GET /family/dependents/{id}/timeline?days=90.
// Sem cache: payload eh leve (90 pontos), Synthesize nao roda.
func (s *Server) handleDependentTimeline(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	days := parseDaysQuery(r, 90, 365)

	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Voce nao eh responsavel por este dependente.")
		return
	}
	consent, err := s.store.GetDependentConsent(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar consentimento.")
		return
	}
	if consent == "revoked" {
		writeError(w, http.StatusForbidden, CodeConsentRevoked,
			"O dependente revogou o consentimento de relatorio agregado.")
		return
	}

	dep, err := s.store.GetUserByID(r.Context(), depID)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "Dependente nao encontrado.")
		return
	}
	points, err := s.store.GetTimeline(r.Context(), depID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar timeline.")
		return
	}
	if points == nil {
		points = []SnapshotPoint{}
	}
	resp := TimelineResponse{
		Dependent: DependentRef{ID: dep.ID, Name: dep.Name},
		Days:      days,
		Snapshots: points,
	}
	s.store.Audit(r.Context(), user.ID, "timeline_consulted", "",
		fmt.Sprintf("dependent_id=%d|days=%d|points=%d", depID, days, len(points)))
	writeJSON(w, http.StatusOK, resp)
}

// parseDaysQuery extrai e clampa o param `days`. Default e maximo configuraveis.
func parseDaysQuery(r *http.Request, def, max int) int {
	q := r.URL.Query().Get("days")
	if q == "" {
		return def
	}
	n, err := strconv.Atoi(q)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
