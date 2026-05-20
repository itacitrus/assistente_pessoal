package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

// hourWindow eh a janela do rate limit. Mantida como var pra permitir
// override em testes (sem expor flag pro mundo).
var hourWindow = time.Hour

// rxBRPhone valida 12 ou 13 digitos comecando com 55 (Brasil + DDD + numero).
// 12 = sem 9 inicial (numero antigo); 13 = com 9. Ambos aceitos.
var rxBRPhone = regexp.MustCompile(`^55\d{10,11}$`)

// rxOnlyDigits remove tudo que nao for digito.
var rxOnlyDigits = regexp.MustCompile(`\D+`)

// rxHHMM aceita HH:MM 24h (00:00..23:59).
var rxHHMM = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// validReminderBefore segue conjunto fechado pra alinhar com o frontend.
var validReminderBefore = map[string]bool{
	"15m": true, "30m": true, "1h": true, "2h": true, "4h": true,
}

// validAutoConfirmTimeout segue conjunto fechado.
var validAutoConfirmTimeout = map[string]bool{
	"30m": true, "1h": true, "2h": true, "4h": true, "never": true,
}

// validWeeklyDay aceita os 7 dias em ingles minusculo.
var validWeeklyDay = map[string]bool{
	"sunday": true, "monday": true, "tuesday": true, "wednesday": true,
	"thursday": true, "friday": true, "saturday": true,
}

// normalizePhone mantem so digitos e prefixa 55 quando faltar — paridade
// com o normalizeBRPhone do main, mas standalone (pacote api eh
// independente). NAO faz toggle de 9-digit; o rxBRPhone tolera os 2 formatos.
func normalizePhone(s string) string {
	digits := rxOnlyDigits.ReplaceAllString(s, "")
	if digits == "" {
		return ""
	}
	if !strings.HasPrefix(digits, "55") {
		// Se vier sem o 55 inicial, prefixa — assumimos contexto BR.
		digits = "55" + digits
	}
	return digits
}

// validBRPhone retorna true se phone bate com regex BR (apos normalize).
func validBRPhone(phone string) bool {
	return rxBRPhone.MatchString(phone)
}

// validatePreferencesPatch retorna mensagem de erro humana ou "" se ok.
// Centraliza regras pra evitar drift entre /users/me e /family/dependents/{id}.
func validatePreferencesPatch(p *PreferencesPatch) string {
	if p.Name != nil {
		if msg := validateName(*p.Name); msg != "" {
			return msg
		}
	}
	if p.DailySummaryTime != nil && !rxHHMM.MatchString(*p.DailySummaryTime) {
		return "daily_summary_time deve ser HH:MM (24h)."
	}
	if p.WeeklySummaryTime != nil && !rxHHMM.MatchString(*p.WeeklySummaryTime) {
		return "weekly_summary_time deve ser HH:MM (24h)."
	}
	if p.WeeklySummaryDay != nil && !validWeeklyDay[strings.ToLower(*p.WeeklySummaryDay)] {
		return "weekly_summary_day deve ser sunday..saturday."
	}
	if p.ReminderBefore != nil && !validReminderBefore[*p.ReminderBefore] {
		return "reminder_before deve ser 15m, 30m, 1h, 2h ou 4h."
	}
	if p.AutoConfirmTimeout != nil && !validAutoConfirmTimeout[*p.AutoConfirmTimeout] {
		return "auto_confirm_timeout deve ser 30m, 1h, 2h, 4h ou never."
	}
	if p.InactivityThresholdHours != nil {
		v := *p.InactivityThresholdHours
		if v < 4 || v > 168 {
			return "inactivity_threshold_hours deve ser entre 4 e 168 (1 semana)."
		}
	}
	return ""
}

// validateDependentPatch reusa validacoes de cima — DependentPatch eh
// subset de PreferencesPatch sem auto_confirm_timeout.
func validateDependentPatch(p *DependentPatch) string {
	pp := PreferencesPatch{
		Name:                     p.Name,
		DailySummaryTime:         p.DailySummaryTime,
		WeeklySummaryDay:         p.WeeklySummaryDay,
		WeeklySummaryTime:        p.WeeklySummaryTime,
		ReminderBefore:           p.ReminderBefore,
		InactivityThresholdHours: p.InactivityThresholdHours,
	}
	return validatePreferencesPatch(&pp)
}

// validateCreateDependent valida shape do request. NAO valida unicidade
// do phone (Store decide).
func validateCreateDependent(req *CreateDependentRequest) string {
	if msg := validateName(req.Name); msg != "" {
		return msg
	}
	if req.Phone == "" || !validBRPhone(req.Phone) {
		return "phone deve ter 55 + DDD + numero (ex: 5511999999999)."
	}
	rel := strings.TrimSpace(req.Relationship)
	if len(rel) < 2 || len(rel) > 30 {
		return "relationship deve ter entre 2 e 30 caracteres."
	}
	return ""
}

// validateName aplica limite 2..80 (mesmo que o plano §4 spec).
func validateName(name string) string {
	n := strings.TrimSpace(name)
	if len(n) < 2 || len(n) > 80 {
		return "name deve ter entre 2 e 80 caracteres."
	}
	return ""
}

// clientIP extrai IP do request. Respeita X-Forwarded-For (primeiro item)
// quando atras de proxy reverso conhecido. Em prod, terraform sobe nginx
// na frente — XFF eh confiavel.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// pega o primeiro IP da lista.
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// RemoteAddr tem porta — strip.
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

// joinDetails concatena pares de audit details com separador `|`. Mesmo
// padrao usado no main.go AuditLog.
func joinDetails(parts []string) string {
	return strings.Join(parts, "|")
}

