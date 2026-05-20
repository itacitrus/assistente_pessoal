package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// =========================================================================
// Fase 5 — tool status_dependente + BuildDependentStatus reusavel
// =========================================================================
//
// status_dependente eh chamada pelo responsavel (guardian) via WhatsApp.
// Pergunta "como esta minha mae?" → tool puxa snapshots dos ultimos N dias,
// agregados de medicacao 7d, alertas em aberto, e roda synthesis.Synthesize
// pra produzir relatorio acolhedor.
//
// BuildDependentStatus eh a fonte UNICA pro chat e (futura Fase 2) endpoint
// REST. O endpoint vai chamar a mesma funcao — diferenca eh so o consumidor
// (formatStatusForChat vs JSON resp).
//
// Authz dura: db.IsGuardianOf(caller, dep). Sem vinculo = mensagem natural
// negando. Consent revoked = mensagem padrao informando.

// familyToolHandlers eh o sub-registry da Fase 5 (mesma estrategia da
// Fase 3 medicacao e Fase 4 companion).
var familyToolHandlers = map[string]ToolHandler{
	"status_dependente": handleStatusDependente,
}

// statusDependenteParams espelha o input schema. Pelo menos um identificador
// deve ser passado (id > phone > name fuzzy).
type statusDependenteParams struct {
	DependentID    int64  `json:"dependent_id"`
	DependentPhone string `json:"dependent_phone"`
	DependentName  string `json:"dependent_name"`
	Days           int    `json:"days"`
}

// DependentStatusReport eh o report completo entregue ao caller (chat handler
// hoje, REST handler na Fase 2). Estrutura rica pra suportar ambas surfaces
// sem reagregar dados.
type DependentStatusReport struct {
	Dependent         *User
	Days              int
	DaysSinceLastTalk int
	LastUserMessageAt sql.NullTime
	Medication        synthesis.MedicationStats
	ProactiveAttempts ProactiveAttemptsStats
	AlertsOpen        []FamilyAlert
	Snapshots         []synthesis.DailySnapshot
	Synthesis         synthesis.ReportOutput
}

func handleStatusDependente(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p statusDependenteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if user == nil {
		return "", errors.New("status_dependente: user nil")
	}
	if p.Days <= 0 {
		p.Days = 14
	}
	if p.Days > 90 {
		p.Days = 90
	}

	dep, err := resolveDependent(agent.db, user.ID, p)
	if err != nil {
		return formatStatusError(err), nil
	}

	ok, err := agent.db.IsGuardianOf(user.ID, dep.ID)
	if err != nil {
		return "", fmt.Errorf("is guardian of: %w", err)
	}
	if !ok {
		return fmt.Sprintf("Voce nao tem autorizacao pra consultar o status de %s. Se acha que isso esta errado, peca pra %s te cadastrar como responsavel.", dep.Name, dep.Name), nil
	}

	consent, err := agent.db.GetDependentConsent(user.ID, dep.ID)
	if err != nil {
		return "", fmt.Errorf("get consent: %w", err)
	}
	if consent == ConsentRevoked {
		// Mensagem padrao — sem vazar dados, sem revelar o que existia antes.
		return fmt.Sprintf("%s revogou o consentimento de relatorio agregado. Voce ainda pode entrar em contato direto.", dep.Name), nil
	}

	report, err := BuildDependentStatus(ctx, agent.db, agent.report, dep, p.Days)
	if err != nil {
		// Reportamos uma mensagem amigavel; audit_log ja capturou via
		// BuildDependentStatus.
		return fmt.Sprintf("Tive um problema buscando o status de %s. Posso tentar de novo daqui a pouco?", dep.Name), nil
	}

	if agent.audit != nil {
		adherencePct := 0
		if report.Medication.Scheduled > 0 {
			adherencePct = int(100 * report.Medication.AdherenceFrac)
		}
		details := fmt.Sprintf(
			"via=chat|days=%d|adherence_pct=%d|days_silent=%d|alerts_open=%d|tendencia=%s|nivel=%s|n_snapshots=%d",
			report.Days, adherencePct, report.DaysSinceLastTalk, len(report.AlertsOpen),
			report.Synthesis.Tendencia, report.Synthesis.NivelPreocupacao, len(report.Snapshots),
		)
		agent.audit.Log(user.ID, "status_dependente_consulted", dep.Name, details)
	}

	return formatStatusForChat(report), nil
}

// BuildDependentStatus eh a fonte unica de DependentStatusReport — chamada
// hoje pelo handler de chat, na Fase 2 sera chamada tb pelo endpoint REST.
//
// Recebe report client (Sonnet) injetado em vez de agent inteiro: permite
// usar em contextos de teste/REST sem precisar do Agent completo.
//
// Caller eh responsavel por:
//   - Validar IsGuardianOf antes de chamar.
//   - Validar consent != revoked antes de chamar.
//
// Falhas no Synthesize NAO falham a funcao — degrada elegantemente para
// ReportOutput{Tendencia: "indeterminado", ...}. Audit log captura ambas.
func BuildDependentStatus(ctx context.Context, db *DB, report llm.ReportProvider, dep *User, days int) (*DependentStatusReport, error) {
	if db == nil {
		return nil, errors.New("BuildDependentStatus: db nil")
	}
	if dep == nil {
		return nil, errors.New("BuildDependentStatus: dep nil")
	}
	if days <= 0 {
		days = 14
	}
	now := time.Now().UTC()
	weekAgo := now.Add(-7 * 24 * time.Hour)
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)

	rep := &DependentStatusReport{Dependent: dep, Days: days}

	if dep.LastUserMessageAt != nil {
		rep.LastUserMessageAt = sql.NullTime{Time: *dep.LastUserMessageAt, Valid: true}
		rep.DaysSinceLastTalk = int(now.Sub(*dep.LastUserMessageAt).Hours() / 24)
	} else {
		rep.DaysSinceLastTalk = -1
	}

	medStats, err := db.GetMedicationStats7d(dep.ID, weekAgo, now)
	if err != nil {
		return nil, fmt.Errorf("med stats: %w", err)
	}
	rep.Medication = medStats

	paStats, err := db.GetProactiveAttemptsStats(dep.ID, weekAgo, now)
	if err != nil {
		return nil, fmt.Errorf("proactive stats: %w", err)
	}
	rep.ProactiveAttempts = paStats

	alerts, err := db.GetOpenAlertsForUser(dep.ID)
	if err != nil {
		return nil, fmt.Errorf("alerts: %w", err)
	}
	rep.AlertsOpen = alerts

	snaps, err := db.GetSnapshotsForUserDateRange(dep.ID, windowStart, now)
	if err != nil {
		return nil, fmt.Errorf("snapshots: %w", err)
	}
	rep.Snapshots = snaps

	// Roda synthesis (Sonnet) — degrada a indeterminado se falha.
	synthIn := synthesis.ReportInput{
		Dependent: synthesis.User{
			ID:   dep.ID,
			Name: dep.Name,
		},
		Days:              days,
		Snapshots:         snaps,
		MedicationStats:   medStats,
		OpenAlerts:        toSynthesisAlerts(alerts),
		LastUserMessageAt: rep.LastUserMessageAt,
	}

	if report != nil {
		out, sErr := synthesis.Synthesize(ctx, report, synthIn)
		if sErr != nil {
			rep.Synthesis = synthesis.ReportOutput{
				Tendencia:        "indeterminado",
				Resumo:           "Nao foi possivel gerar sintese agora.",
				NivelPreocupacao: "indeterminado",
			}
			// Caller registra synthesis_failed se tiver auditor.
			NewAuditLog(db).Log(dep.ID, "synthesis_failed", "", sanitizeAuditReason(sErr.Error()))
		} else {
			rep.Synthesis = out
			NewAuditLog(db).Log(dep.ID, "synthesis_executed", "",
				fmt.Sprintf("tendencia=%s|nivel=%s|n_snapshots=%d",
					out.Tendencia, out.NivelPreocupacao, len(snaps)))
		}
	} else {
		// Sem report client (testes) — fallback explicito.
		rep.Synthesis = synthesis.ReportOutput{
			Tendencia:        "indeterminado",
			Resumo:           "Nao foi possivel gerar sintese (provider nao configurado).",
			NivelPreocupacao: "indeterminado",
		}
	}

	return rep, nil
}

// resolveDependent procura o User a partir dos identificadores. Prioridade:
// id > phone > name fuzzy entre dependentes do guardian.
func resolveDependent(db *DB, guardianID int64, p statusDependenteParams) (*User, error) {
	if p.DependentID > 0 {
		u, err := db.GetUserByID(p.DependentID)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	if strings.TrimSpace(p.DependentPhone) != "" {
		phone := normalizePhoneFamily(p.DependentPhone)
		u, err := db.GetUserByPhone(phone)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	if strings.TrimSpace(p.DependentName) != "" {
		deps, err := db.GetDependents(guardianID)
		if err != nil {
			return nil, err
		}
		match := pickDependentByName(deps, p.DependentName)
		if match == nil {
			return nil, fmt.Errorf("dependente nao encontrado pelo nome: %q", p.DependentName)
		}
		return match, nil
	}
	return nil, errors.New("informe dependent_id, dependent_phone ou dependent_name")
}

// pickDependentByName faz match fuzzy: case-insensitive, primeiro por
// substring no nome completo, depois por primeiro nome. Retorna nil se nao
// achar (caller decide como reportar).
func pickDependentByName(deps []FamilyLink, query string) *User {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	for _, d := range deps {
		if d.Other == nil {
			continue
		}
		if strings.Contains(strings.ToLower(d.Other.Name), q) {
			return d.Other
		}
	}
	for _, d := range deps {
		if d.Other == nil {
			continue
		}
		first := strings.ToLower(strings.Fields(d.Other.Name)[0])
		if strings.HasPrefix(first, q) {
			return d.Other
		}
	}
	return nil
}

// normalizePhoneFamily eh forma simplificada de normalizar — strip espaco,
// hifen, parentese, +. NAO adiciona prefixo 55 automaticamente (quem chama
// pode ter o numero ja completo).
func normalizePhoneFamily(s string) string {
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.ReplaceAll(s, "+", "")
	return s
}

// toSynthesisAlerts converte FamilyAlert pro shape do synthesis package.
// Synthesize so precisa de policy_name, severity e created_at — message
// fica fora (privacidade: detalhe do alerta nao precisa atravessar barreira).
func toSynthesisAlerts(alerts []FamilyAlert) []synthesis.Alert {
	out := make([]synthesis.Alert, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, synthesis.Alert{
			PolicyName: a.PolicyName,
			Severity:   a.Severity,
			CreatedAt:  a.CreatedAt,
		})
	}
	return out
}

// formatStatusError eh a saida amigavel quando resolveDependent falha.
func formatStatusError(err error) string {
	if errors.Is(err, ErrUserNotFound) {
		return "Nao encontrei esse dependente nos meus registros. Confere o nome ou pede pra ele se cadastrar primeiro."
	}
	return fmt.Sprintf("Nao consegui localizar o dependente: %v", err)
}

// formatStatusForChat eh a renderizacao pro WhatsApp. Foco no que cabe em
// 1 mensagem mobile — tendencia, aderencia, ultima conversa, alertas,
// resumo, ponto de atencao, 1 sugestao.
func formatStatusForChat(r *DependentStatusReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status de %s (ultimos %d dias):\n\n", r.Dependent.Name, r.Days))

	// Tendencia (estrela).
	if r.Synthesis.Tendencia != "" {
		sb.WriteString(fmt.Sprintf("Tendencia: %s.\n", r.Synthesis.Tendencia))
	}
	if r.Synthesis.Comparacao != "" {
		sb.WriteString(r.Synthesis.Comparacao + "\n")
	}

	// Medicacao.
	if r.Medication.Scheduled == 0 {
		sb.WriteString("Sem medicacoes cadastradas.\n")
	} else {
		pct := int(100 * r.Medication.AdherenceFrac)
		sb.WriteString(fmt.Sprintf("Aderencia 7d: %d/%d doses (%d%%).\n",
			r.Medication.Taken, r.Medication.Scheduled, pct))
	}

	// Ultima conversa.
	switch {
	case !r.LastUserMessageAt.Valid:
		sb.WriteString("Ainda nao houve conversa.\n")
	case r.DaysSinceLastTalk == 0:
		sb.WriteString("Falou com o Lurch hoje.\n")
	case r.DaysSinceLastTalk == 1:
		sb.WriteString("Ultima conversa: ontem.\n")
	default:
		sb.WriteString(fmt.Sprintf("Ultima conversa ha %d dias.\n", r.DaysSinceLastTalk))
	}

	if len(r.AlertsOpen) > 0 {
		sb.WriteString(fmt.Sprintf("Alertas em aberto: %d.\n", len(r.AlertsOpen)))
	}

	sb.WriteString("\n" + r.Synthesis.Resumo)
	if r.Synthesis.PontoDeAtencao != "" {
		sb.WriteString("\n\nPonto de atencao: " + r.Synthesis.PontoDeAtencao)
	}
	if len(r.Synthesis.RecomendacoesCarinhosas) > 0 {
		sb.WriteString("\n\nSugestao: " + r.Synthesis.RecomendacoesCarinhosas[0])
	}
	return sb.String()
}
