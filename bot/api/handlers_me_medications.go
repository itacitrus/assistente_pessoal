package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// =========================================================================
// Medicacao do proprio titular — /api/v1/me/medications
// =========================================================================
//
// Espelha os handlers de medicacao do dependente (handlers_family.go), mas o
// dono eh o proprio usuario logado: sem checagem de guardiao. O motor de
// lembrete/escalacao (scheduler_medication.go + escalation.go) ja roda pra
// qualquer user ativo, entao nada novo no agendamento — apenas exponhamos o
// CRUD pro titular gerenciar os proprios remedios.

// handleMyMedicationsCollection roteia GET (list) vs POST (create).
func (s *Server) handleMyMedicationsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListMyMedications(w, r)
	case http.MethodPost:
		s.handleCreateMyMedication(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}

// handleMyMedicationResource roteia /me/medications/{id} (DELETE = soft-delete).
func (s *Server) handleMyMedicationResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.route("/api/v1/me/medications/"))
	idStr := strings.Trim(path, "/")
	medID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || medID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do medicamento inválido.")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.handleUpdateMyMedication(w, r, medID)
	case http.MethodDelete:
		s.handleDeleteMyMedication(w, r, medID)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}

// handleUpdateMyMedication — PATCH /me/medications/{id}. Replace com o shape de
// criacao.
func (s *Server) handleUpdateMyMedication(w http.ResponseWriter, r *http.Request, medID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var req CreateMedicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	if msg := validateCreateMedication(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	item, err := s.store.UpdateMyMedication(r.Context(), user.ID, medID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, CodeNotFound, "Medicamento não encontrado.")
		case errors.Is(err, ErrMedicationDuplicate):
			writeError(w, http.StatusConflict, CodeMedicationDup, msgMedicationDuplicate)
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao editar medicamento.")
		}
		return
	}
	writeJSON(w, http.StatusOK, *item)
}

func (s *Server) handleListMyMedications(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	meds, err := s.store.ListMyMedications(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao listar medicamentos.")
		return
	}
	if meds == nil {
		meds = []MedicationItem{}
	}
	writeJSON(w, http.StatusOK, MedicationsResponse{Medications: meds})
}

func (s *Server) handleCreateMyMedication(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var req CreateMedicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	if msg := validateCreateMedication(&req); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	item, err := s.store.CreateMyMedication(r.Context(), user.ID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrMedicationDuplicate):
			writeError(w, http.StatusConflict, CodeMedicationDup, msgMedicationDuplicate)
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao cadastrar medicamento.")
		}
		return
	}
	writeJSON(w, http.StatusCreated, *item)
}

// handleDrugSearch — GET /me/drugs/search?q=<termo>&limit=<n>. Autocomplete do
// cadastro de remedio: resolve o termo (mesmo com erro de grafia/fonetica)
// contra o catalogo ANVISA/CMED. Apenas leitura, exige autenticacao. q curto
// (<2) ou catalogo nao populado -> lista vazia.
func (s *Server) handleDrugSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	if user := userFromContext(r.Context()); user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, DrugSearchResponse{Matches: []DrugMatch{}})
		return
	}
	limit := int(parseInt64Query(r, "limit"))
	if limit <= 0 || limit > 15 {
		limit = 8
	}
	matches, err := s.store.ResolveDrug(r.Context(), q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar no catálogo de medicamentos.")
		return
	}
	if matches == nil {
		matches = []DrugMatch{}
	}
	writeJSON(w, http.StatusOK, DrugSearchResponse{Matches: matches})
}

// handleMyIntakes — GET /me/intakes. Historico de tomadas do proprio titular.
// Query: ?days (1..90, default 14), ?medication_id (filtra um remedio).
func (s *Server) handleMyIntakes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	days := parseDaysParam(r, 14, 90)
	medID := parseInt64Query(r, "medication_id")
	intakes, err := s.store.ListMyIntakes(r.Context(), user.ID, days, medID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Medicamento não encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao listar histórico de tomadas.")
		return
	}
	if intakes == nil {
		intakes = []IntakeEntry{}
	}
	writeJSON(w, http.StatusOK, IntakesResponse{Intakes: intakes, Days: days})
}

func (s *Server) handleDeleteMyMedication(w http.ResponseWriter, r *http.Request, medID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	if err := s.store.DeactivateMyMedication(r.Context(), user.ID, medID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Medicamento não encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao remover medicamento.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
