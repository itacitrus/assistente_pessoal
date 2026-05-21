package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// handleCreateDependent — POST /family/dependents.
// Auth: qualquer usuario logado (responsaveis e tambem comuns que decidem
// adicionar familiar; o user.Type podera ser promovido para 'responsavel'
// pelo admin/UI, fora do escopo desta fase).
func (s *Server) handleCreateDependent(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var req CreateDependentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
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
			writeError(w, http.StatusConflict, CodePhoneInUse, "Esse telefone já está cadastrado.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao cadastrar dependente.")
		}
		return
	}
	// Audit feito no adapter (LogFamilyLinkCreated). Aqui nao duplicamos.

	// Boas-vindas: apresenta o Zello ao idoso uma unica vez, na criacao.
	// Best-effort — falha de envio nao aborta a criacao (ja persistida).
	welcome := buildDependentWelcomeMessage(dep.Name, user.Name)
	if err := s.store.SendWhatsApp(r.Context(), dep.PhoneNumber, welcome); err != nil {
		// Loga via audit como falha implicita seria ruido; apenas seguimos.
		// O 201 ja foi conquistado; o usuario pode reenviar boas-vindas no futuro.
		_ = err
	} else {
		s.store.Audit(r.Context(), user.ID, "dependent_welcomed", dep.PhoneNumber,
			fmt.Sprintf("dependent_id=%d", dep.ID))
	}

	writeJSON(w, http.StatusCreated, CreateDependentResponse{User: *dep, Link: *link})
}

// handleResendWelcome — POST /family/dependents/{id}/welcome. Reenvia a
// mensagem de boas-vindas do Zello ao dependente. Util quando o envio na
// criacao falhou (WhatsApp fora do ar, numero recem-validado) ou quando o
// dependente foi cadastrado antes de a feature de boas-vindas existir. Apenas
// o guardian do dependente pode disparar. Diferente da criacao, aqui o envio
// NAO eh best-effort: se falhar, devolvemos erro pro usuario saber que nao
// chegou (o call site eh um clique explicito "Reenviar", nao um efeito colateral).
func (s *Server) handleResendWelcome(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	dep, err := s.store.GetUserByID(r.Context(), depID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Dependente não encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar dependente.")
		return
	}
	msg := buildDependentWelcomeMessage(dep.Name, user.Name)
	if err := s.store.SendWhatsApp(r.Context(), dep.PhoneNumber, msg); err != nil {
		writeError(w, http.StatusBadGateway, CodeInternal,
			"Não foi possível enviar a mensagem agora. Tente de novo em instantes.")
		return
	}
	s.store.Audit(r.Context(), user.ID, "dependent_welcomed", dep.PhoneNumber,
		fmt.Sprintf("dependent_id=%d|resend=true", dep.ID))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// buildDependentWelcomeMessage compoe a mensagem calorosa de apresentacao do
// Zello ao idoso recem-cadastrado. Tom companion (caloroso/acolhedor), pt-BR,
// assinada Zello. `dependentName` e `guardianName` sao nomes completos — usamos
// o primeiro nome de cada um para soar proximo.
func buildDependentWelcomeMessage(dependentName, guardianName string) string {
	dep := firstNamePT(dependentName)
	guardian := firstNamePT(guardianName)
	saudacao := "Olá"
	if dep != "" {
		saudacao = "Olá, " + dep
	}
	quem := "alguém da sua família"
	if guardian != "" {
		quem = guardian
	}
	return fmt.Sprintf(
		"%s! Eu sou o Zello. 😊\n\n"+
			"%s me pediu pra te dar uma mãozinha no dia a dia — lembrar dos seus "+
			"remédios na hora certa e fazer companhia pra uma boa conversa sempre "+
			"que você quiser.\n\n"+
			"Pode falar comigo quando tiver vontade: me conta como foi o dia, tira "+
			"uma dúvida, ou só bate um papo. Estou aqui, viu?\n\n"+
			"— Zello",
		saudacao, quem)
}

// firstNamePT extrai o primeiro nome de um nome completo. Vive no api package
// (espelha firstName do main) para evitar dependencia reversa.
func firstNamePT(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return ""
	}
	if i := strings.IndexByte(full, ' '); i > 0 {
		return full[:i]
	}
	return full
}

// handleListDependents — GET /family/dependents. Auth: qualquer logado.
// Retorna lista vazia se o user nao tem dependentes.
func (s *Server) handleListDependents(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
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
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	var p DependentPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	// Telefone: normaliza (so digitos, prefixo 55) e valida antes de tudo. O
	// numero eh a identidade do dependente no WhatsApp — typo aqui faz lembrete
	// nao chegar, entao validamos cedo.
	if p.Phone != nil {
		*p.Phone = normalizePhone(*p.Phone)
		if !validBRPhone(*p.Phone) {
			writeError(w, http.StatusBadRequest, CodeInvalidPhone,
				"Telefone inválido. Use 55 + DDD + número.")
			return
		}
	}
	if msg := validateDependentPatch(&p); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	updated, err := s.store.UpdateDependent(r.Context(), user.ID, depID, p)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusNotFound, CodeNotFound, "Dependente não encontrado.")
		case errors.Is(err, ErrConflict):
			writeError(w, http.StatusConflict, CodePhoneInUse,
				"Esse telefone já está cadastrado para outra pessoa.")
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
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	link, err := s.store.GetFamilyLink(r.Context(), linkID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Vínculo familiar não encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao buscar vínculo.")
		return
	}
	if link.GuardianID != user.ID {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este vínculo.")
		return
	}

	var p NotifyPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
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
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	days := parseDaysQuery(r, 14, 90)

	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	consent, err := s.store.GetDependentConsent(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar consentimento.")
		return
	}
	if consent == "revoked" {
		writeError(w, http.StatusForbidden, CodeConsentRevoked,
			"O dependente revogou o consentimento de relatório agregado.")
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
	// Nao cacheia o placeholder "sendo preparada" — a regen assincrona popula a
	// sintese em segundos, e o cache de 60s faria o usuario ver "preparando"
	// por tempo demais. Status com sintese ja disponivel cacheia normalmente.
	if resp.SynthesisAvailable {
		s.statusCache.Set(cacheKey, resp)
	}
	s.store.Audit(r.Context(), user.ID, "status_dependente_consulted", "",
		fmt.Sprintf("dependent_id=%d|days=%d|cache=miss", depID, days))
	writeJSON(w, http.StatusOK, resp)
}

// handleDependentTimeline — GET /family/dependents/{id}/timeline?days=90.
// Sem cache: payload eh leve (90 pontos), Synthesize nao roda.
func (s *Server) handleDependentTimeline(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	days := parseDaysQuery(r, 90, 365)

	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	consent, err := s.store.GetDependentConsent(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar consentimento.")
		return
	}
	if consent == "revoked" {
		writeError(w, http.StatusForbidden, CodeConsentRevoked,
			"O dependente revogou o consentimento de relatório agregado.")
		return
	}

	dep, err := s.store.GetUserByID(r.Context(), depID)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "Dependente não encontrado.")
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

// =========================================================================
// Family / medicacao do dependente
// =========================================================================

// handleListDependentMedications — GET /family/dependents/{id}/medications.
func (s *Server) handleListDependentMedications(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	meds, err := s.store.ListDependentMedications(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao listar medicamentos.")
		return
	}
	if meds == nil {
		meds = []MedicationItem{}
	}
	writeJSON(w, http.StatusOK, MedicationsResponse{Medications: meds})
}

// handleCreateDependentMedication — POST /family/dependents/{id}/medications.
func (s *Server) handleCreateDependentMedication(w http.ResponseWriter, r *http.Request, depID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
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
	item, err := s.store.CreateDependentMedication(r.Context(), user.ID, depID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao cadastrar medicamento.")
		}
		return
	}
	writeJSON(w, http.StatusCreated, *item)
}

// handleDeleteDependentMedication — DELETE /family/dependents/{id}/medications/{medId}.
func (s *Server) handleDeleteDependentMedication(w http.ResponseWriter, r *http.Request, depID, medID int64) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	ok, err := s.store.IsGuardianOf(r.Context(), user.ID, depID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao verificar autorizacao.")
		return
	}
	if !ok {
		writeError(w, http.StatusForbidden, CodeForbidden, "Você não é responsável por este dependente.")
		return
	}
	if err := s.store.DeactivateDependentMedication(r.Context(), user.ID, depID, medID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, CodeNotFound, "Medicamento não encontrado.")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao remover medicamento.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validateCreateMedication valida o body de criacao de medicamento. Retorna
// string vazia se ok, mensagem de erro PT-BR caso contrario.
func validateCreateMedication(req *CreateMedicationRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return "Nome do medicamento é obrigatório."
	}
	if len(req.Times) < 1 || len(req.Times) > 6 {
		return "Informe de 1 a 6 horários."
	}
	for _, t := range req.Times {
		if !isValidHHMM(t) {
			return "Horário inválido: use o formato HH:MM (ex: 08:00)."
		}
	}
	switch strings.ToLower(strings.TrimSpace(req.Frequency)) {
	case "daily":
		// days ignorado
	case "weekly":
		if len(req.Days) == 0 {
			return "Para frequência semanal, informe ao menos um dia da semana."
		}
		for _, d := range req.Days {
			if _, ok := weekdayToBYDAY[strings.ToLower(strings.TrimSpace(d))]; !ok {
				return "Dia da semana inválido: use mon, tue, wed, thu, fri, sat ou sun."
			}
		}
	default:
		return "Frequencia invalida: use 'daily' ou 'weekly'."
	}
	return ""
}

// isValidHHMM valida formato HH:MM 24h (00:00..23:59).
func isValidHHMM(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil {
		return false
	}
	return h >= 0 && h <= 23 && m >= 0 && m <= 59
}

// weekdayToBYDAY mapeia o dia da semana (en, 3 letras) -> token BYDAY iCal.
var weekdayToBYDAY = map[string]string{
	"mon": "MO", "tue": "TU", "wed": "WE", "thu": "TH",
	"fri": "FR", "sat": "SA", "sun": "SU",
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
