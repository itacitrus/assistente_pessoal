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
	"status_dependente":  handleStatusDependente,
	"listar_dependentes": handleListarDependentes,
}

// handleListarDependentes lista quem esta sob a responsabilidade do usuario
// (vinculos family_links onde ele eh guardian). Existe pra que o responsavel
// que pergunta "quem esta sob minha responsabilidade?" receba a VERDADE do
// banco — antes nao havia ferramenta e o LLM inventava "ninguem cadastrado".
func handleListarDependentes(ctx context.Context, agent *Agent, user *User, _ json.RawMessage) (string, error) {
	if user == nil {
		return "", errors.New("listar_dependentes: user nil")
	}
	deps, err := agent.db.GetDependents(user.ID)
	if err != nil {
		return "", fmt.Errorf("get dependents: %w", err)
	}
	if len(deps) == 0 {
		return "Você não tem ninguém cadastrado sob sua responsabilidade ainda.", nil
	}
	var sb strings.Builder
	sb.WriteString("Sob sua responsabilidade:\n")
	for _, d := range deps {
		if d.Other == nil {
			continue
		}
		line := "- " + d.Other.Name
		if rel := strings.TrimSpace(d.Relationship); rel != "" {
			line += " (" + rel + ")"
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
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
	// ActiveMedicationCount eh quantos remedios ativos o dependente tem
	// cadastrados AGORA. Distinto de Medication.Scheduled (doses materializadas
	// no intake_log na janela): um idoso recem-cadastrado tem remedios mas
	// ainda zero doses logadas. Sem isso, o relatorio dizia "sem medicacoes"
	// para quem TEM remedios — mentira para o responsavel.
	ActiveMedicationCount int
	ProactiveAttempts     ProactiveAttemptsStats
	AlertsOpen            []FamilyAlert
	Snapshots             []synthesis.DailySnapshot
	Synthesis             synthesis.ReportOutput
	// SynthesisAvailable=false quando ainda nao ha sintese persistida (idoso
	// novo, sem geracao). O caller mostra "sendo preparada" e dispara regen.
	SynthesisAvailable bool
	// SynthesisStale=true quando a sintese persistida foi gerada antes do
	// snapshot mais recente (dado novo desde a ultima geracao). O caller serve
	// a persistida e dispara regen assincrono — nunca bloqueia.
	SynthesisStale       bool
	SynthesisGeneratedAt time.Time
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
		return fmt.Sprintf("Você não tem autorização pra consultar o status de %s. Se acha que isso está errado, peça pra %s te cadastrar como responsável.", dep.Name, dep.Name), nil
	}

	consent, err := agent.db.GetDependentConsent(user.ID, dep.ID)
	if err != nil {
		return "", fmt.Errorf("get consent: %w", err)
	}
	if consent == ConsentRevoked {
		// Mensagem padrao — sem vazar dados, sem revelar o que existia antes.
		return fmt.Sprintf("%s revogou o consentimento de relatório agregado. Você ainda pode entrar em contato direto.", dep.Name), nil
	}

	report, err := BuildDependentStatus(ctx, agent.db, agent.report, dep, p.Days)
	if err != nil {
		// Reportamos uma mensagem amigavel; audit_log ja capturou via
		// BuildDependentStatus.
		return fmt.Sprintf("Tive um problema buscando o status de %s. Posso tentar de novo daqui a pouco?", dep.Name), nil
	}

	// Diferente do painel web (instantaneo, regen assincrono), o canal de chat
	// eh assincrono por natureza: o responsavel pediu e pode esperar alguns
	// segundos por uma sintese fresca. Entao, quando a persistida esta ausente
	// ou desatualizada, geramos na hora e usamos o resultado.
	if report.SynthesisStale && agent.report != nil {
		if regErr := RegenerateDependentSynthesis(ctx, agent.db, agent.report, dep, p.Days); regErr == nil {
			if stored, gErr := agent.db.GetDependentSynthesis(dep.ID); gErr == nil {
				report.Synthesis = stored.Report
				report.SynthesisAvailable = true
				report.SynthesisStale = false
			}
		}
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
// BuildDependentStatus monta o status do dependente para exibicao (painel/chat).
// NAO gera sintese — le a persistida (rapido). O parametro report fica por
// compatibilidade de assinatura com os call sites; a geracao mora em
// RegenerateDependentSynthesis. Mantido pra nao quebrar callers/testes.
func BuildDependentStatus(ctx context.Context, db *DB, _ llm.ReportProvider, dep *User, days int) (*DependentStatusReport, error) {
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

	// Aderencia usa a MESMA janela `days` (default 14) que rotula o card no
	// painel ("Aderência (14 dias)") e o detalhamento de tomadas — antes lia 7d
	// e divergia do rotulo. weekAgo segue valendo para as outras metricas (7d).
	medStats, err := db.GetMedicationStats7d(dep.ID, windowStart, now)
	if err != nil {
		return nil, fmt.Errorf("med stats: %w", err)
	}
	rep.Medication = medStats

	activeMeds, err := db.ListActiveMedications(dep.ID)
	if err != nil {
		return nil, fmt.Errorf("active meds: %w", err)
	}
	rep.ActiveMedicationCount = len(activeMeds)

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

	// Sintese (Sonnet) NAO roda no caminho da requisicao — eh cara e fazia a
	// pagina do dependente demorar segundos. Servimos a ultima sintese
	// persistida (instantanea). A geracao acontece fora do request:
	//   - regen assincrono quando fica "stale" (decidido pelo caller)
	//   - refresh diario pelo scheduler
	// Se ainda nao ha nenhuma (idoso novo), devolvemos placeholder e marcamos
	// SynthesisAvailable=false pro caller mostrar "sendo preparada".
	stored, sErr := db.GetDependentSynthesis(dep.ID)
	if sErr != nil {
		if !errors.Is(sErr, ErrSynthesisNotFound) {
			return nil, fmt.Errorf("get stored synthesis: %w", sErr)
		}
		rep.SynthesisAvailable = false
		rep.SynthesisStale = true // sem sintese -> precisa gerar
		rep.Synthesis = synthesis.ReportOutput{
			Tendencia:        "indeterminado",
			Resumo:           "Estamos preparando a primeira síntese — atualize em instantes.",
			NivelPreocupacao: "indeterminado",
		}
		return rep, nil
	}

	rep.Synthesis = stored.Report
	rep.SynthesisAvailable = true
	rep.SynthesisGeneratedAt = stored.GeneratedAt
	// Stale se surgiu snapshot novo depois da ultima geracao.
	if latest, ok, lErr := db.GetLatestSnapshotInferredAt(dep.ID); lErr == nil && ok {
		rep.SynthesisStale = stored.GeneratedAt.Before(latest)
	}
	// Tambem stale se a sintese eh de um dia anterior (frescor de CALENDARIO), mesmo
	// sem snapshot novo. Sem isto, quando os snapshots paravam de avancar (small-talk
	// nao gera snapshot), stale virava false PARA SEMPRE e o read-stale nunca
	// regenerava — o painel congelava. Usa o fuso do dependente. Custo: no maximo 1
	// regen/dia por este gatilho (apos regenerar hoje, generated_at >= inicio do dia).
	tz := db.GetEventTimezone(dep.ID, now)
	if tz == nil {
		tz = BRT()
	}
	nowLocal := now.In(tz)
	startOfToday := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, tz)
	if stored.GeneratedAt.Before(startOfToday) {
		rep.SynthesisStale = true
	}
	return rep, nil
}

// RegenerateDependentSynthesis roda a sintese (Sonnet) e persiste o resultado.
// Roda FORA do caminho de request — pelo regen assincrono (read stale) e pelo
// refresh diario do scheduler. Best-effort: em falha NAO sobrescreve a sintese
// anterior (mantem a ultima boa).
//
// OBSERVABILIDADE (critico): QUALQUER falha — report nil, erro de getter (med
// stats/alerts/snapshots), Synthesize ou upsert — eh auditada como synthesis_failed
// com o motivo. Antes, so a falha de Synthesize auditava; erros de getter/nil
// retornavam mudos e o painel congelava SEM nenhum rastro (foi o que mascarou a
// quebra em producao). O defer garante o registro em todos os caminhos de erro.
func RegenerateDependentSynthesis(ctx context.Context, db *DB, report llm.ReportProvider, dep *User, days int) (err error) {
	if db == nil || dep == nil {
		return errors.New("RegenerateDependentSynthesis: db/dep nil")
	}
	defer func() {
		if err != nil {
			NewAuditLog(db).Log(dep.ID, "synthesis_failed", "", sanitizeAuditReason(err.Error()))
		}
	}()
	if report == nil {
		return errors.New("RegenerateDependentSynthesis: report nil")
	}
	if days <= 0 {
		days = 14
	}
	now := time.Now().UTC()
	weekAgo := now.Add(-7 * 24 * time.Hour)
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)

	medStats, err := db.GetMedicationStats7d(dep.ID, weekAgo, now)
	if err != nil {
		return fmt.Errorf("med stats: %w", err)
	}
	alerts, err := db.GetOpenAlertsForUser(dep.ID)
	if err != nil {
		return fmt.Errorf("alerts: %w", err)
	}
	snaps, err := db.GetSnapshotsForUserDateRange(dep.ID, windowStart, now)
	if err != nil {
		return fmt.Errorf("snapshots: %w", err)
	}
	var lastMsg sql.NullTime
	if dep.LastUserMessageAt != nil {
		lastMsg = sql.NullTime{Time: *dep.LastUserMessageAt, Valid: true}
	}

	synthIn := synthesis.ReportInput{
		Dependent:         synthesis.User{ID: dep.ID, Name: dep.Name},
		Days:              days,
		Snapshots:         snaps,
		MedicationStats:   medStats,
		OpenAlerts:        toSynthesisAlerts(alerts),
		LastUserMessageAt: lastMsg,
	}
	out, sErr := synthesis.Synthesize(ctx, report, synthIn)
	if sErr != nil {
		return fmt.Errorf("synthesize: %w", sErr)
	}
	if uErr := db.UpsertDependentSynthesis(dep.ID, days, out, time.Now().UTC()); uErr != nil {
		return fmt.Errorf("persist synthesis: %w", uErr)
	}
	NewAuditLog(db).Log(dep.ID, "synthesis_executed", "",
		fmt.Sprintf("tendencia=%s|nivel=%s|n_snapshots=%d|persisted=true",
			out.Tendencia, out.NivelPreocupacao, len(snaps)))
	return nil
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
	// Fallback por parentesco: o responsavel costuma se referir ao dependente
	// pela relacao ("meu pai", "minha mae") em vez do nome. Casa o parentesco
	// gravado em family_links contra a query (qualquer direcao de substring).
	for _, d := range deps {
		if d.Other == nil {
			continue
		}
		rel := strings.ToLower(strings.TrimSpace(d.Relationship))
		if rel != "" && (strings.Contains(q, rel) || strings.Contains(rel, q)) {
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
		return "Não encontrei esse dependente nos meus registros. Confere o nome ou pede pra ele se cadastrar primeiro."
	}
	return fmt.Sprintf("Não consegui localizar o dependente: %v", err)
}

// formatStatusForChat eh a renderizacao pro WhatsApp. Foco no que cabe em
// 1 mensagem mobile — tendencia, aderencia, ultima conversa, alertas,
// resumo, ponto de atencao, 1 sugestao.
func formatStatusForChat(r *DependentStatusReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status de %s (últimos %d dias):\n\n", r.Dependent.Name, r.Days))

	// Tendência (estrela).
	if r.Synthesis.Tendencia != "" {
		sb.WriteString(fmt.Sprintf("Tendência: %s.\n", r.Synthesis.Tendencia))
	}
	if r.Synthesis.Comparacao != "" {
		sb.WriteString(r.Synthesis.Comparacao + "\n")
	}

	// Medicação. Distingue 3 casos para nunca enganar o responsável:
	//   - tem remédio E doses logadas → mostra aderência;
	//   - tem remédio mas nenhuma dose na janela → diz isso (não "sem remédios");
	//   - nenhum remédio cadastrado → diz que não há.
	switch {
	case r.Medication.Scheduled > 0:
		pct := int(100 * r.Medication.AdherenceFrac)
		sb.WriteString(fmt.Sprintf("Aderência 7d: %d/%d doses (%d%%).\n",
			r.Medication.Taken, r.Medication.Scheduled, pct))
	case r.ActiveMedicationCount > 0:
		plural := "remédio cadastrado"
		if r.ActiveMedicationCount > 1 {
			plural = "remédios cadastrados"
		}
		sb.WriteString(fmt.Sprintf("%d %s, mas ainda sem registro de doses nesta janela (os lembretes começam no próximo horário).\n",
			r.ActiveMedicationCount, plural))
	default:
		sb.WriteString("Sem medicações cadastradas.\n")
	}

	// Última conversa.
	switch {
	case !r.LastUserMessageAt.Valid:
		sb.WriteString("Ainda não houve conversa.\n")
	case r.DaysSinceLastTalk == 0:
		sb.WriteString("Falou com o Zello hoje.\n")
	case r.DaysSinceLastTalk == 1:
		sb.WriteString("Última conversa: ontem.\n")
	default:
		sb.WriteString(fmt.Sprintf("Última conversa há %d dias.\n", r.DaysSinceLastTalk))
	}

	if len(r.AlertsOpen) > 0 {
		sb.WriteString(fmt.Sprintf("Alertas em aberto: %d.\n", len(r.AlertsOpen)))
	}

	sb.WriteString("\n" + r.Synthesis.Resumo)
	if r.Synthesis.PontoDeAtencao != "" {
		sb.WriteString("\n\nPonto de atenção: " + r.Synthesis.PontoDeAtencao)
	}
	if len(r.Synthesis.RecomendacoesCarinhosas) > 0 {
		sb.WriteString("\n\nSugestão: " + r.Synthesis.RecomendacoesCarinhosas[0])
	}
	return sb.String()
}
