package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// handleUpdateMe processa PATCH /api/v1/users/me. Validacoes em bloco —
// retorna o primeiro erro estruturado.
func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	var p PreferencesPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "JSON inválido.")
		return
	}
	if msg := validatePreferencesPatch(&p); msg != "" {
		writeError(w, http.StatusBadRequest, CodeValidation, msg)
		return
	}
	updated, err := s.store.UpdateUserPreferences(r.Context(), user.ID, p)
	if err != nil {
		if errors.Is(err, ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao atualizar preferencias.")
		return
	}
	s.store.Audit(r.Context(), user.ID, "user_preferences_updated", user.PhoneNumber,
		summarizePrefsPatch(p))
	writeJSON(w, http.StatusOK, updated)
}

// summarizePrefsPatch produz string compacta dos campos alterados pra audit.
func summarizePrefsPatch(p PreferencesPatch) string {
	var parts []string
	if p.Name != nil {
		parts = append(parts, "name")
	}
	if p.DailySummaryTime != nil {
		parts = append(parts, fmt.Sprintf("daily_summary_time=%s", *p.DailySummaryTime))
	}
	if p.WeeklySummaryDay != nil {
		parts = append(parts, fmt.Sprintf("weekly_summary_day=%s", *p.WeeklySummaryDay))
	}
	if p.WeeklySummaryTime != nil {
		parts = append(parts, fmt.Sprintf("weekly_summary_time=%s", *p.WeeklySummaryTime))
	}
	if p.ReminderBefore != nil {
		parts = append(parts, fmt.Sprintf("reminder_before=%s", *p.ReminderBefore))
	}
	if p.AutoConfirmTimeout != nil {
		parts = append(parts, fmt.Sprintf("auto_confirm_timeout=%s", *p.AutoConfirmTimeout))
	}
	if p.InactivityThresholdHours != nil {
		parts = append(parts, fmt.Sprintf("inactivity_threshold_hours=%d", *p.InactivityThresholdHours))
	}
	if len(parts) == 0 {
		return "no_changes"
	}
	return joinDetails(parts)
}
