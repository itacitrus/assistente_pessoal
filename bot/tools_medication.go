package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// =========================================================================
// Tools de medicacao (Fase 3)
// =========================================================================
//
// Sao 7 tools:
//   - cadastrar_medicamento  → cria pending_confirmation; user confirma
//   - listar_medicamentos    → leitura simples
//   - editar_medicamento     → cria pending_confirmation (mudanca eh sensivel)
//   - cancelar_medicamento   → cria pending_confirmation (idem)
//   - marcar_remedio_tomado  → aplica direto (declaracao do user)
//   - pular_dose             → aplica direto (decisao do user, com razao)
//   - extrair_receita_imagem → tool de visao; nao persiste sozinha
//
// Consideracao sobre confirmacao em "marcar_remedio_tomado" e "pular_dose":
//
// "Tomei" eh ato declarativo do usuario. Pedir confirmacao ("voce tomou
// mesmo? confirma?") seria condescendente — especialmente com idoso. Por
// isso aplicamos direto, sem pending_confirmation. Mesmo principio para
// pular_dose, que ja exige razao explicita (defensiva).
//
// Cadastro/edicao/cancelamento sim criam pending — mudancas estruturais
// merecem confirmacao explicita, porque podem afetar futuras escalacoes.

// medicationToolHandlers eh o subset registrado em tools.go::toolHandlers.
// Mantido aqui para evitar declaracoes duplicadas no init de tools.go.
var medicationToolHandlers = map[string]ToolHandler{
	"cadastrar_medicamento":       handleCadastrarMedicamento,
	"buscar_medicamento_catalogo": handleBuscarMedicamentoCatalogo,
	"listar_medicamentos":         handleListarMedicamentos,
	"editar_medicamento":          handleEditarMedicamento,
	"cancelar_medicamento":        handleCancelarMedicamento,
	"marcar_remedio_tomado":       handleMarcarRemedioTomado,
	"adiar_remedio":               handleAdiarRemedio,
	"pular_dose":                  handlePularDose,
	"extrair_receita_imagem":      handleExtrairReceitaImagem,
}

// =========================================================================
// buscar_medicamento_catalogo
// =========================================================================

type buscarMedicamentoCatalogoParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// handleBuscarMedicamentoCatalogo resolve o que o usuario digitou/falou contra
// o catalogo oficial (ANVISA/CMED), corrigindo erros de grafia e fonetica. NAO
// persiste nada: devolve candidatos ranqueados para o LLM CONFIRMAR em
// linguagem natural antes de chamar cadastrar_medicamento com o nome/dose certos.
func handleBuscarMedicamentoCatalogo(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p buscarMedicamentoCatalogoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return "Preciso do nome (mesmo aproximado) do remédio pra procurar no catálogo.", nil
	}
	limit := p.Limit
	if limit <= 0 || limit > 8 {
		limit = 5
	}
	matches, err := agent.db.ResolveDrug(q, limit)
	if err != nil {
		return "", fmt.Errorf("resolve drug %q: %w", q, err)
	}
	if len(matches) == 0 {
		return fmt.Sprintf(
			"Não encontrei %q no catálogo da ANVISA. Pode ser um nome pouco comum, manipulado, ou com grafia bem diferente. Siga com o que o usuário informou — NÃO invente nome nem dose.",
			q,
		), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Correspondências no catálogo ANVISA para %q (mais provável primeiro):\n", q)
	for _, m := range matches {
		name := m.CommercialName
		if m.Concentration != "" {
			name += " " + m.Concentration
		}
		ingredient := strings.ToLower(strings.TrimSpace(m.ActiveIngredient))
		if ingredient != "" && normalizeName(ingredient) != normalizeName(m.CommercialName) {
			fmt.Fprintf(&b, "- %s — princípio ativo: %s\n", name, ingredient)
		} else {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	b.WriteString("\nConfirme com o usuário, em linguagem natural e SEM menu numerado, qual é o correto (ex: \"Você quis dizer o X de Ymg?\"). Depois cadastre usando exatamente o nome e a dose do item confirmado. Se nenhum bater, pergunte ao usuário em vez de chutar.")
	return b.String(), nil
}

// =========================================================================
// cadastrar_medicamento
// =========================================================================

type cadastrarMedicamentoParams struct {
	TargetUser       string `json:"target_user"`
	Name             string `json:"name"`
	Dose             string `json:"dose"`
	Instructions     string `json:"instructions"`
	ScheduleRRULE    string `json:"schedule_rrule"`
	StartDate        string `json:"start_date"`
	EndDate          string `json:"end_date"`
	Critical         bool   `json:"critical"`
	ToleranceMinutes int    `json:"tolerance_minutes"`
	LateDosePolicy   string `json:"late_dose_policy"`
}

func handleCadastrarMedicamento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cadastrarMedicamentoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.ScheduleRRULE) == "" {
		return "Preciso do nome do medicamento e dos horários. Pergunte ao usuário.", nil
	}

	target, denyMsg, err := resolveTargetForMedication(agent, user, p.TargetUser)
	if err != nil {
		return "", err
	}
	if denyMsg != "" {
		return denyMsg, nil
	}

	if _, err := ParseRRULE(p.ScheduleRRULE); err != nil {
		return fmt.Sprintf("Não consegui entender o horário '%s' (%v). Pode descrever em palavras (ex: 'todos os dias às 8h')?", p.ScheduleRRULE, err), nil
	}

	startDate := strings.TrimSpace(p.StartDate)
	if startDate == "" {
		startDate = time.Now().In(BRT()).Format(dateLayout)
	}

	policy, perr := ValidateLateDosePolicy(strings.TrimSpace(p.LateDosePolicy))
	if perr != nil {
		return "Não reconheci essa política de dose atrasada. As opções são: decisão do médico, pular, tomar e manter a próxima, ou tomar e recalcular os horários.", nil
	}

	// Persiste DIRETO no ato da chamada (espelha criar_evento). A confirmacao
	// em linguagem natural acontece ANTES, na conversa — quando o LLM chama
	// esta tool, eh porque o usuario ja confirmou. O retorno reflete o estado
	// REAL do banco: nunca dizemos "cadastrei" sem ter cadastrado.
	mi := &MedicationIntent{
		Name:             p.Name,
		Dose:             p.Dose,
		Instructions:     p.Instructions,
		ScheduleRRULE:    p.ScheduleRRULE,
		StartDate:        startDate,
		EndDate:          strings.TrimSpace(p.EndDate),
		Critical:         p.Critical,
		ToleranceMinutes: p.ToleranceMinutes,
		LateDosePolicy:   string(policy),
	}
	med, err := persistMedicationFromIntent(agent.db, target.ID, user.ID, mi)
	if err != nil {
		return "", err
	}
	agent.audit.Log(user.ID, "medication_created", med.Name,
		fmt.Sprintf("med_id=%d|owner_id=%d|rrule=%s|critical=%t|via=chat", med.ID, target.ID, p.ScheduleRRULE, p.Critical))

	desc := DescribeRRULE(p.ScheduleRRULE)
	dose := strings.TrimSpace(p.Dose)
	doseSuffix := ""
	if dose != "" {
		doseSuffix = " " + dose
	}
	if target.ID != user.ID {
		return fmt.Sprintf("Pronto, cadastrei %s%s pra %s, %s. Vou lembrar no horário certo.", p.Name, doseSuffix, firstName(target.Name), desc), nil
	}
	return fmt.Sprintf("Pronto, cadastrei %s%s, %s. Vou te lembrar no horário certo.", p.Name, doseSuffix, desc), nil
}

// persistMedicationFromIntent cria medication + schedule a partir de um intent
// de cadastro (Reminder=false). Fonte UNICA de persistencia de cadastro de
// medicamento via conversa — usada pela tool cadastrar_medicamento. ownerID eh
// o dono do remedio; creatorID eh quem cadastrou (pode ser o responsavel
// cadastrando pra um dependente).
func persistMedicationFromIntent(db *DB, ownerID, creatorID int64, mi *MedicationIntent) (*Medication, error) {
	policy, perr := ValidateLateDosePolicy(mi.LateDosePolicy)
	if perr != nil {
		policy = LatePolicyConsultDoctor
	}
	med := &Medication{
		UserID:           ownerID,
		Name:             mi.Name,
		Dose:             mi.Dose,
		Instructions:     mi.Instructions,
		CreatedByUserID:  creatorID,
		ToleranceMinutes: mi.ToleranceMinutes,
		LateDosePolicy:   policy,
		// Cadastro via bot defaulta pra exigir confirmacao — comportamento
		// seguro de cuidado. Desligar fica no painel (form), por enquanto.
		RequireConfirmation: true,
	}
	if err := db.CreateMedication(med); err != nil {
		return nil, fmt.Errorf("create medication: %w", err)
	}

	startDate, err := time.ParseInLocation(dateLayout, mi.StartDate, BRT())
	if err != nil {
		startDate = time.Now().In(BRT())
	}
	var endPtr *time.Time
	if strings.TrimSpace(mi.EndDate) != "" {
		if t, e := time.ParseInLocation(dateLayout, mi.EndDate, BRT()); e == nil {
			endPtr = &t
		}
	}
	sched := &MedicationSchedule{
		MedicationID: med.ID,
		RRULE:        mi.ScheduleRRULE,
		StartDate:    startDate,
		EndDate:      endPtr,
		Critical:     mi.Critical,
	}
	if err := db.CreateMedicationSchedule(sched); err != nil {
		return nil, fmt.Errorf("create schedule: %w", err)
	}
	return med, nil
}

// =========================================================================
// listar_medicamentos
// =========================================================================

type listarMedicamentosParams struct {
	TargetUser string `json:"target_user"`
}

func handleListarMedicamentos(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p listarMedicamentosParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	target, denyMsg, err := resolveTargetForMedication(agent, user, p.TargetUser)
	if err != nil {
		return "", err
	}
	if denyMsg != "" {
		return denyMsg, nil
	}
	meds, err := agent.db.ListActiveMedications(target.ID)
	if err != nil {
		return "", fmt.Errorf("list medications: %w", err)
	}
	if len(meds) == 0 {
		if target.ID == user.ID {
			return "Você não tem medicamentos cadastrados.", nil
		}
		return fmt.Sprintf("%s não tem medicamentos cadastrados.", target.Name), nil
	}
	var sb strings.Builder
	if target.ID == user.ID {
		sb.WriteString("Medicamentos cadastrados:\n")
	} else {
		sb.WriteString(fmt.Sprintf("Medicamentos de %s:\n", target.Name))
	}
	for _, m := range meds {
		scheds, _ := agent.db.ListSchedulesForMedication(m.ID)
		if len(scheds) == 0 {
			line := fmt.Sprintf("- %s", m.Name)
			if m.Dose != "" {
				line += " " + m.Dose
			}
			sb.WriteString(line + " (sem horários)\n")
			continue
		}
		for _, s := range scheds {
			line := fmt.Sprintf("- %s", m.Name)
			if m.Dose != "" {
				line += " " + m.Dose
			}
			line += " — " + DescribeRRULE(s.RRULE)
			if s.Critical {
				line += " (crítico)"
			}
			sb.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// =========================================================================
// editar_medicamento
// =========================================================================

type editarMedicamentoParams struct {
	MedicationID      int64  `json:"medication_id"`
	NameQuery         string `json:"name_query"`
	NewName           string `json:"new_name"`
	NewDose           string `json:"new_dose"`
	NewInstructions   string `json:"new_instructions"`
	NewScheduleRRULE  string `json:"new_schedule_rrule"`
	NewEndDate        string `json:"new_end_date"`
	NewCritical       *bool  `json:"new_critical"`
	NewToleranceMin   *int   `json:"new_tolerance_minutes"`
	NewLateDosePolicy string `json:"new_late_dose_policy"`
}

func handleEditarMedicamento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p editarMedicamentoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	med, msg, err := resolveMedication(agent, user, p.MedicationID, p.NameQuery)
	if err != nil {
		return "", err
	}
	if med == nil {
		return msg, nil
	}

	if p.NewScheduleRRULE != "" {
		if _, err := ParseRRULE(p.NewScheduleRRULE); err != nil {
			return fmt.Sprintf("Não consegui entender o novo horário '%s' (%v).", p.NewScheduleRRULE, err), nil
		}
	}

	// Aplica direto (sem pending) — edicoes simples sao baixo-risco e
	// criar pending pra cada delta vira ruido. Mudancas estruturais (RRULE
	// novo) substituem todos os schedules; mantemos a politica anterior
	// nos novos schedules a menos que NewCritical seja explicito.
	var pNewName, pNewDose, pNewInstr *string
	if p.NewName != "" {
		v := p.NewName
		pNewName = &v
	}
	if p.NewDose != "" {
		v := p.NewDose
		pNewDose = &v
	}
	if p.NewInstructions != "" {
		v := p.NewInstructions
		pNewInstr = &v
	}
	var pNewPolicy *LateDosePolicy
	if strings.TrimSpace(p.NewLateDosePolicy) != "" {
		validated, perr := ValidateLateDosePolicy(strings.TrimSpace(p.NewLateDosePolicy))
		if perr != nil {
			return "Não reconheci essa política de dose atrasada. As opções são: decisão do médico, pular, tomar e manter a próxima, ou tomar e recalcular os horários.", nil
		}
		pNewPolicy = &validated
	}
	if pNewName != nil || pNewDose != nil || pNewInstr != nil || p.NewToleranceMin != nil || pNewPolicy != nil {
		if err := agent.db.UpdateMedicationFields(med.ID, pNewName, pNewDose, pNewInstr, p.NewToleranceMin, pNewPolicy, nil); err != nil {
			return "", fmt.Errorf("update medication: %w", err)
		}
	}

	if p.NewScheduleRRULE != "" {
		// Substitui todos os schedules. Pega criticality do schedule antigo
		// (assumindo unico schedule no caso comum) ou usa NewCritical se
		// passado.
		critical := false
		if p.NewCritical != nil {
			critical = *p.NewCritical
		} else {
			old, _ := agent.db.ListSchedulesForMedication(med.ID)
			if len(old) > 0 {
				critical = old[0].Critical
			}
		}
		if err := agent.db.DeleteSchedulesForMedication(med.ID); err != nil {
			return "", fmt.Errorf("delete old schedules: %w", err)
		}
		startDate := time.Now().In(BRT())
		var endDatePtr *time.Time
		if strings.TrimSpace(p.NewEndDate) != "" {
			ed, parseErr := time.ParseInLocation(dateLayout, p.NewEndDate, BRT())
			if parseErr != nil {
				return fmt.Sprintf("Não entendi a data de fim '%s' (use YYYY-MM-DD).", p.NewEndDate), nil
			}
			endDatePtr = &ed
		}
		s := &MedicationSchedule{
			MedicationID: med.ID,
			RRULE:        p.NewScheduleRRULE,
			StartDate:    startDate,
			EndDate:      endDatePtr,
			Critical:     critical,
		}
		if err := agent.db.CreateMedicationSchedule(s); err != nil {
			return "", fmt.Errorf("create new schedule: %w", err)
		}
	}

	logEditDetails(agent, user, med, p)
	return fmt.Sprintf("Atualizei o cadastro de %s.", med.Name), nil
}

// logEditDetails encapsula audit log da edicao com snippet dos campos
// alterados (sem dados sensiveis em excesso).
func logEditDetails(agent *Agent, user *User, med *Medication, p editarMedicamentoParams) {
	parts := []string{fmt.Sprintf("med_id=%d", med.ID)}
	if p.NewName != "" {
		parts = append(parts, "new_name="+p.NewName)
	}
	if p.NewDose != "" {
		parts = append(parts, "new_dose="+p.NewDose)
	}
	if p.NewScheduleRRULE != "" {
		parts = append(parts, "new_rrule="+p.NewScheduleRRULE)
	}
	agent.audit.Log(user.ID, "medication_edited", med.Name, strings.Join(parts, "|"))
}

// =========================================================================
// cancelar_medicamento
// =========================================================================

type cancelarMedicamentoParams struct {
	MedicationID int64  `json:"medication_id"`
	NameQuery    string `json:"name_query"`
	Reason       string `json:"reason"`
}

func handleCancelarMedicamento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p cancelarMedicamentoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	med, msg, err := resolveMedication(agent, user, p.MedicationID, p.NameQuery)
	if err != nil {
		return "", err
	}
	if med == nil {
		return msg, nil
	}

	if err := agent.db.DeactivateMedication(med.ID); err != nil {
		return "", fmt.Errorf("deactivate: %w", err)
	}
	agent.audit.Log(user.ID, "medication_canceled", med.Name,
		fmt.Sprintf("med_id=%d|reason=%s", med.ID, strings.TrimSpace(p.Reason)))
	return fmt.Sprintf("Cancelei %s. Os lembretes futuros vão parar.", med.Name), nil
}

// =========================================================================
// marcar_remedio_tomado
// =========================================================================

type marcarRemedioTomadoParams struct {
	MedicationID int64  `json:"medication_id"`
	NameQuery    string `json:"name_query"`
}

func handleMarcarRemedioTomado(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p marcarRemedioTomadoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	// "Tomei" GENERICO (sem citar remedio): se ha mais de uma dose pendente
	// agora (lembrete agrupado), o idoso quer dizer que tomou TODAS. Marca todas
	// de uma vez. Se citar um remedio especifico, cai no caminho granular abaixo.
	if p.MedicationID == 0 && strings.TrimSpace(p.NameQuery) == "" {
		if msg, handled := marcarTodasPendentes(agent, user); handled {
			return msg, nil
		}
		// Nenhuma pendente agora — segue pro caminho normal (tomada fora de
		// lembrete, resolvida por horario mais proximo).
	}

	pc, err := agent.db.GetActivePendingForUserAndMedication(user.ID, p.MedicationID)
	if err != nil {
		return "", fmt.Errorf("get pending: %w", err)
	}
	if pc == nil {
		// Sem pending ativo — o usuario tomou FORA de um lembrete (pre-empcao,
		// lembrete ja escalado/desistido, ou nunca houve). Mesmo assim a
		// tomada PRECISA ser registrada no intake_log, senao some da aderencia
		// e o responsavel ve "nenhuma dose" mesmo o idoso tendo tomado.
		med, resolveMsg := resolveMedicationForIntake(agent, user, p.MedicationID, p.NameQuery)
		if med == nil {
			return resolveMsg, nil
		}
		scheduledAt := nearestScheduledDose(agent, user.ID, med.ID, time.Now().UTC())
		if err := agent.db.RecordTakenIntake(med.ID, scheduledAt, "tomei (fora de lembrete)"); err != nil {
			log.Printf("marcar_remedio_tomado: record taken (no pending): %v", err)
			return "", fmt.Errorf("record taken: %w", err)
		}
		agent.audit.Log(user.ID, "medication_taken", med.Name,
			fmt.Sprintf("med_id=%d|note=fora_de_pending|scheduled=%s", med.ID, scheduledAt.Format(time.RFC3339)))
		return fmt.Sprintf("Anotado, %s.", firstName(user.Name)), nil
	}

	mi := parseMedicationIntent(pc)
	if mi == nil || mi.MedicationID == 0 {
		return "Anotei, mas não identifiquei qual lembrete. Se o lembrete vier de novo, me avisa.", nil
	}

	if err := agent.db.UpdateIntakeStatus(mi.MedicationID, mi.ScheduledAt, IntakeTaken, "tomei"); err != nil {
		log.Printf("marcar_remedio_tomado: update intake: %v", err)
	}
	if err := agent.db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		log.Printf("marcar_remedio_tomado: resolve pending: %v", err)
	}

	med, _ := agent.db.GetMedicationByID(mi.MedicationID)
	medName := "remedio"
	if med != nil {
		medName = med.Name
	}
	agent.audit.Log(user.ID, "medication_taken", medName,
		fmt.Sprintf("med_id=%d|pc=%d", mi.MedicationID, pc.ID))

	// Politica take_recalculate: tomou atrasado e o responsavel configurou que,
	// nesse caso, os horarios devem ser reancorados a partir de agora. So age
	// quando o idoso de fato tomou (acao dele) e ha atraso material.
	delta := time.Now().UTC().Sub(mi.ScheduledAt)
	if med != nil && med.LateDosePolicy == LatePolicyTakeRecalculate && delta >= time.Minute {
		newDesc, rErr := agent.db.RescheduleMedicationByDelta(med.ID, delta)
		if rErr != nil {
			log.Printf("marcar_remedio_tomado: reschedule: %v", rErr)
		} else {
			agent.audit.Log(user.ID, "medication_rescheduled", medName,
				fmt.Sprintf("med_id=%d|delta_min=%d|new=%s", med.ID, int(delta.Minutes()), newDesc))
			return fmt.Sprintf(
				"Anotado, %s. Como seu responsável configurou para esse remédio, reagendei os próximos horários a partir de agora (%s). "+
					"Se preferir voltar ao horário original, dá pra ajustar pelo painel.",
				firstName(user.Name), newDesc), nil
		}
	}

	// Resposta neutra — sem reforco positivo. Vide regra no escalation.go.
	return fmt.Sprintf("Anotado, %s.", firstName(user.Name)), nil
}

// marcarTodasPendentes marca como tomadas TODAS as doses de medicacao
// pendentes do usuario agora. Retorna (mensagem, true) quando agiu em lote
// (>=2 pendentes); (zero, false) quando ha 0 ou 1 pendente — nesse caso o
// caller segue pelo caminho granular (que cobre o single com recalculo de
// horario e o "tomou fora de lembrete").
func marcarTodasPendentes(agent *Agent, user *User) (string, bool) {
	pendings, err := agent.db.ListActiveMedicationPendingsForUser(user.ID)
	if err != nil {
		log.Printf("marcar_remedio_tomado: list pendings: %v", err)
		return "", false
	}
	if len(pendings) < 2 {
		return "", false // deixa o caminho granular resolver (single / fora de lembrete)
	}

	var names []string
	for i := range pendings {
		pc := &pendings[i]
		mi := parseMedicationIntent(pc)
		if mi == nil || mi.MedicationID == 0 {
			continue
		}
		if err := agent.db.UpdateIntakeStatus(mi.MedicationID, mi.ScheduledAt, IntakeTaken, "tomei (todos)"); err != nil {
			log.Printf("marcar_remedio_tomado(todos): update intake: %v", err)
		}
		if err := agent.db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
			log.Printf("marcar_remedio_tomado(todos): resolve pending: %v", err)
		}
		med, _ := agent.db.GetMedicationByID(mi.MedicationID)
		medName := "remedio"
		if med != nil {
			medName = med.Name
		}
		names = append(names, medName)
		agent.audit.Log(user.ID, "medication_taken", medName,
			fmt.Sprintf("med_id=%d|pc=%d|via=batch", mi.MedicationID, pc.ID))
	}
	if len(names) == 0 {
		return "", false
	}
	return fmt.Sprintf("Anotado, %s. Marquei %s como tomados.", firstName(user.Name), joinNames(names)), true
}

// =========================================================================
// adiar_remedio
// =========================================================================

type adiarRemedioParams struct {
	MedicationID int64  `json:"medication_id"`
	HorarioHHMM  string `json:"horario_hhmm"`  // ex: "18:40" (horario que o idoso disse)
	DaquiMinutos int    `json:"daqui_minutos"` // alternativa: "daqui a 30 min"
}

// handleAdiarRemedio registra que o idoso disse que vai tomar mais tarde.
// NAO marca como tomado. Grava deferred_until (se houver horario) para UM
// lembrete gentil naquele momento. A familia continua sendo avisada em segredo
// se a tolerancia expirar sem confirmacao — adiar nao cancela isso, so silencia
// a cobranca ate o horario dito.
func handleAdiarRemedio(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p adiarRemedioParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	pc, err := agent.db.GetActivePendingForUserAndMedication(user.ID, p.MedicationID)
	if err != nil {
		return "", fmt.Errorf("get pending: %w", err)
	}
	if pc == nil {
		return "Tudo bem, sem pressa. Quando tomar, é só me avisar.", nil
	}

	var deferred time.Time
	switch {
	case p.DaquiMinutos > 0:
		deferred = time.Now().Add(time.Duration(p.DaquiMinutos) * time.Minute)
	case strings.TrimSpace(p.HorarioHHMM) != "":
		h, m, perr := parseHHMM(strings.TrimSpace(p.HorarioHHMM))
		if perr == nil {
			now := time.Now().In(BRT())
			deferred = time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, BRT())
		}
	}
	if !deferred.IsZero() {
		if err := agent.db.SetPendingDeferredUntil(pc.ID, deferred); err != nil {
			log.Printf("adiar_remedio: set deferred: %v", err)
		}
	}

	med, _ := agent.db.GetMedicationByID(medMedicationID(pc))
	medName := "remedio"
	if med != nil {
		medName = med.Name
	}
	agent.audit.Log(user.ID, "medication_deferred", medName,
		fmt.Sprintf("med_id=%d|pc=%d|until=%s", medMedicationID(pc), pc.ID, deferred.Format(time.RFC3339)))

	if !deferred.IsZero() {
		return fmt.Sprintf("Combinado, %s. Quando tomar, me avisa que eu anoto.", firstName(user.Name)), nil
	}
	return fmt.Sprintf("Tudo bem, %s, sem pressa. Quando tomar, é só me avisar.", firstName(user.Name)), nil
}

// =========================================================================
// pular_dose
// =========================================================================

type pularDoseParams struct {
	MedicationID int64  `json:"medication_id"`
	Reason       string `json:"reason"`
}

func handlePularDose(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p pularDoseParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if strings.TrimSpace(p.Reason) == "" {
		return "Preciso saber a razão do pulo (ex: 'esqueci de comprar', 'estou enjoado'). Pergunte ao usuário.", nil
	}

	pc, err := agent.db.GetActivePendingForUserAndMedication(user.ID, p.MedicationID)
	if err != nil {
		return "", fmt.Errorf("get pending: %w", err)
	}
	if pc == nil {
		return "Não tenho lembrete em aberto pra registrar como pulada.", nil
	}
	mi := parseMedicationIntent(pc)
	if mi == nil || mi.MedicationID == 0 {
		return "Anotei o pulo, mas não identifiquei qual lembrete.", nil
	}

	if err := agent.db.UpdateIntakeStatus(mi.MedicationID, mi.ScheduledAt, IntakeSkipped, p.Reason); err != nil {
		log.Printf("pular_dose: update intake: %v", err)
	}
	if err := agent.db.ResolvePendingConfirmation(pc.ID, "skipped"); err != nil {
		log.Printf("pular_dose: resolve pending: %v", err)
	}

	med, _ := agent.db.GetMedicationByID(mi.MedicationID)
	medName := "remedio"
	if med != nil {
		medName = med.Name
	}
	agent.audit.Log(user.ID, "medication_skipped", medName,
		fmt.Sprintf("med_id=%d|pc=%d|reason=%s", mi.MedicationID, pc.ID, strings.TrimSpace(p.Reason)))

	return fmt.Sprintf("Anotei que você pulou esta dose (%s).", strings.TrimSpace(p.Reason)), nil
}

// =========================================================================
// extrair_receita_imagem
// =========================================================================

// extrairReceitaItem eh o sub-objeto que Claude devolve por item identificado
// na receita. Tipagem solta — frequency_text e duration_text ficam em texto
// livre exatamente como na receita; conversao em RRULE eh responsabilidade
// do agent ao chamar cadastrar_medicamento.
type extrairReceitaItem struct {
	Name          string `json:"name"`
	Dose          string `json:"dose"`
	FrequencyText string `json:"frequency_text"`
	DurationText  string `json:"duration_text"`
}

type extrairReceitaParams struct {
	Items []extrairReceitaItem `json:"items"`
}

// MediaCacheDir eh onde a tool persiste imagens com TTL 24h (vide §7.3 do
// plano — privacidade/PII em dados medicos exige conservadorismo).
//
// NOTA: Implementado como cache estrutural. Hoje a imagem chega ao Claude
// via stack de visao existente (handler.go baixa e passa Data; agent.go
// monta MessageContent base64). A persistencia em disco eh apenas para
// auditoria forense quando habilitada, controlada por env
// LURCH_MEDIA_CACHE=1.
//
// TODO: cron de limpeza dos arquivos > 24h. Nao bloqueia Fase 3 — fica
// como follow-up. Se cache ficar sem rotacao, em piloto cresce no maximo
// alguns MB por idoso/dia (foto de receita ocasional).
const MediaCacheDir = "data/media_cache"

func handleExtrairReceitaImagem(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p extrairReceitaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if len(p.Items) == 0 {
		return "Não consegui identificar medicamentos na imagem. Pode me mandar de novo, ou descrever em texto?", nil
	}

	// Audit: extracao bruta para revisao posterior. NUNCA persiste a imagem
	// — apenas o texto extraido. Privacidade em dados medicos.
	rawJSON, _ := json.Marshal(p.Items)
	agent.audit.Log(user.ID, "prescription_image_processed", "", string(rawJSON))

	// Sumario para o agente seguir o fluxo item-a-item (vide §7 do plano).
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Extraídos %d medicamentos da receita. ", len(p.Items)))
	sb.WriteString("Apresentar item-a-item ao usuário em linguagem natural, sem menu numerado, ")
	sb.WriteString("perguntando o horário de cada um, e chamar cadastrar_medicamento ao confirmar. ")
	sb.WriteString("O OCR de receita erra com frequência — quando houver correspondência no catálogo, ")
	sb.WriteString("confirme com o usuário o nome/dose do catálogo (sem afirmar que está certo) antes de cadastrar:\n")
	for i, it := range p.Items {
		line := fmt.Sprintf("%d. %s", i+1, it.Name)
		if strings.TrimSpace(it.Dose) != "" {
			line += " " + it.Dose
		}
		if strings.TrimSpace(it.FrequencyText) != "" {
			line += " (frequência: " + it.FrequencyText + ")"
		}
		if strings.TrimSpace(it.DurationText) != "" {
			line += " (duração: " + it.DurationText + ")"
		}
		// Enriquecimento best-effort: resolve o nome lido pelo OCR contra o
		// catalogo. So anexa quando ha correspondencia forte (>=0.7) e ela
		// difere do que foi lido — sinal pro agente CONFIRMAR a correcao, nunca
		// aplicar sozinho. Falha/catalogo vazio: segue sem sugestao.
		if matches, err := agent.db.ResolveDrug(it.Name, 1); err == nil && len(matches) > 0 {
			top := matches[0]
			suggestion := top.CommercialName
			if top.Concentration != "" {
				suggestion += " " + top.Concentration
			}
			differs := normalizeName(top.CommercialName) != normalizeName(it.Name) ||
				(top.Concentration != "" && normalizeName(top.Concentration) != normalizeName(it.Dose))
			if top.Confidence >= 0.7 && differs {
				line += fmt.Sprintf(" [catálogo sugere: %s — confirmar com o usuário]", suggestion)
			}
		}
		sb.WriteString(line + "\n")
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// CacheMediaImage persiste imagem em disco com TTL implicito (limpeza fica
// como cron follow-up). Caminho: <MediaCacheDir>/<sha1(data)>.<ext>.
//
// Hoje so eh chamada quando env LURCH_MEDIA_CACHE=1 — comportamento default
// eh nao escrever em disco. Isto preserva a politica do plano (§7.3) de
// nao reter imagem alem do necessario.
func CacheMediaImage(data []byte, mime string) (string, error) {
	if os.Getenv("LURCH_MEDIA_CACHE") != "1" {
		return "", nil
	}
	if len(data) == 0 {
		return "", errors.New("empty image data")
	}
	if err := os.MkdirAll(MediaCacheDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	h := sha1.Sum(data)
	ext := ".jpg"
	if strings.Contains(mime, "png") {
		ext = ".png"
	}
	path := filepath.Join(MediaCacheDir, hex.EncodeToString(h[:])+ext)
	// Idempotente — se ja existe (mesmo hash), nao reescreve.
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}
	return path, nil
}

// =========================================================================
// Helpers compartilhados
// =========================================================================

// resolveTargetForMedication aplica a logica de target_user nas tools de
// medicacao. Retorna:
//   - target=user, denyMsg="" → caminho self (default)
//   - target=outro, denyMsg="" → permitido via family_links
//   - target=nil, denyMsg!=""  → nega com mensagem natural
func resolveTargetForMedication(agent *Agent, user *User, targetName string) (*User, string, error) {
	if strings.TrimSpace(targetName) == "" || strings.EqualFold(targetName, user.Name) {
		return user, "", nil
	}
	t, err := agent.perms.ResolveByName(targetName)
	if err != nil {
		return nil, "", fmt.Errorf("resolve target: %w", err)
	}
	if t == nil {
		// Nao achou usuario global por nome — pode ser referencia por
		// parentesco ("pai", "minha mae") ou primeiro nome de um dependente.
		// Resolve entre os dependentes do proprio usuario (nome OU parentesco).
		if deps, derr := agent.db.GetDependents(user.ID); derr == nil {
			if match := pickDependentByName(deps, targetName); match != nil {
				t = match
			}
		}
	}
	if t == nil {
		return nil, fmt.Sprintf("Não encontrei o usuário '%s'.", targetName), nil
	}
	can, err := agent.db.CanManageMedicationFor(user.ID, t.ID)
	if err != nil {
		return nil, "", fmt.Errorf("check family link: %w", err)
	}
	if !can {
		return nil, fmt.Sprintf("Você não tem permissão pra mexer em medicamento de %s. Cadastre o vínculo familiar primeiro.", t.Name), nil
	}
	return t, "", nil
}

// resolveMedication tenta achar a Medication por id ou name_query. Retorna
// (med, msg, err): med != nil = achou; med == nil = caller usa msg como
// resposta natural ao user. err != nil = erro real (DB, etc).
func resolveMedication(agent *Agent, user *User, id int64, nameQuery string) (*Medication, string, error) {
	if id > 0 {
		med, err := agent.db.GetMedicationByID(id)
		if err != nil {
			if errors.Is(err, ErrMedicationNotFound) {
				return nil, fmt.Sprintf("Não achei medicamento com id %d.", id), nil
			}
			return nil, "", fmt.Errorf("get medication: %w", err)
		}
		if med.UserID != user.ID {
			can, _ := agent.db.CanManageMedicationFor(user.ID, med.UserID)
			if !can {
				return nil, "Esse medicamento não é seu e você não tem permissão pra mexer.", nil
			}
		}
		return med, "", nil
	}
	if strings.TrimSpace(nameQuery) == "" {
		return nil, "Preciso do id ou do nome do medicamento.", nil
	}
	meds, err := agent.db.ListActiveMedications(user.ID)
	if err != nil {
		return nil, "", fmt.Errorf("list meds: %w", err)
	}
	low := strings.ToLower(strings.TrimSpace(nameQuery))
	for i := range meds {
		if strings.Contains(strings.ToLower(meds[i].Name), low) {
			return &meds[i], "", nil
		}
	}
	return nil, fmt.Sprintf("Não achei medicamento com nome parecido com '%s'.", nameQuery), nil
}

// resolveMedicationForIntake escolhe QUAL remedio o usuario quis dizer ao
// declarar "tomei" sem haver lembrete ativo. Prioridade:
//  1. medication_id explicito.
//  2. name_query (substring no nome dos remedios ativos do proprio usuario).
//  3. se o usuario tem exatamente UM remedio ativo, eh esse.
//
// Em qualquer outro caso (nenhum/ambiguo), retorna med=nil + uma pergunta
// natural — preferimos perguntar a registrar a dose errada.
func resolveMedicationForIntake(agent *Agent, user *User, id int64, nameQuery string) (*Medication, string) {
	if id > 0 {
		med, err := agent.db.GetMedicationByID(id)
		if err == nil {
			return med, ""
		}
	}
	meds, err := agent.db.ListActiveMedications(user.ID)
	if err != nil {
		return nil, "Tive um problema pra achar seus remédios agora. Pode me dizer de novo daqui a pouco?"
	}
	if q := strings.ToLower(strings.TrimSpace(nameQuery)); q != "" {
		for i := range meds {
			if strings.Contains(strings.ToLower(meds[i].Name), q) {
				return &meds[i], ""
			}
		}
		return nil, fmt.Sprintf("Não achei nenhum remédio seu com nome parecido com '%s'. De qual você tá falando?", nameQuery)
	}
	if len(meds) == 1 {
		return &meds[0], ""
	}
	if len(meds) == 0 {
		return nil, "Não tenho nenhum remédio seu cadastrado pra anotar. Quer que eu cadastre?"
	}
	return nil, "De qual remédio você tá falando? Me diz o nome que eu anoto."
}

// nearestScheduledDose devolve o horario agendado mais proximo de `now` para o
// medicamento — preferindo a ultima ocorrencia <= now (a dose que o usuario
// provavelmente acabou de tomar). Casar com o slot real mantem a idempotencia
// com o scheduler (UNIQUE medication_id+scheduled_at). Se nenhuma ocorrencia
// cair na janela do dia, usa `now` (registro ad-hoc, ainda contabilizado).
func nearestScheduledDose(agent *Agent, userID, medID int64, now time.Time) time.Time {
	scheds, err := agent.db.ListSchedulesForMedication(medID)
	if err != nil || len(scheds) == 0 {
		return now
	}
	loc := agent.db.GetEventTimezone(userID, now)
	windowStart := now.Add(-18 * time.Hour)
	windowEnd := now.Add(2 * time.Hour)
	var best time.Time
	var bestPast bool
	for i := range scheds {
		occs, err := ExpandOccurrences(&scheds[i], windowStart, windowEnd, loc)
		if err != nil {
			continue
		}
		for _, occ := range occs {
			isPast := !occ.After(now)
			switch {
			case best.IsZero():
				best, bestPast = occ, isPast
			case bestPast && isPast:
				// ambas no passado: fica com a mais recente (mais perto de now).
				if occ.After(best) {
					best = occ
				}
			case !bestPast && isPast:
				// passado vence futuro (dose mais provavelmente "tomada agora").
				best, bestPast = occ, true
			case !bestPast && !isPast:
				// ambas no futuro: fica com a mais cedo.
				if occ.Before(best) {
					best = occ
				}
			}
		}
	}
	if best.IsZero() {
		return now
	}
	return best.UTC()
}
