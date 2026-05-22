package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
	"github.com/teambition/rrule-go"
)

// =========================================================================
// Adapter: medicacao do dependente (web/UI)
// =========================================================================
//
// Reusa o CRUD de db_medication.go + rrule.go. Autorizacao (IsGuardianOf) eh
// validada aqui, no adapter — o handler ja revalida, mas defesa em
// profundidade: nenhum metodo do Store confia que o caller checou.

// ListDependentMedications lista os medicamentos ativos do dependente com o
// horario descrito em texto humano (PT-BR).
func (a *apiAdapter) ListDependentMedications(ctx context.Context, guardianID, dependentID int64) ([]api.MedicationItem, error) {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrNotFound
	}
	return a.listMedicationsForOwner(dependentID)
}

// ListMyMedications lista os medicamentos ativos do proprio usuario logado.
func (a *apiAdapter) ListMyMedications(ctx context.Context, userID int64) ([]api.MedicationItem, error) {
	return a.listMedicationsForOwner(userID)
}

// listMedicationsForOwner eh o caminho comum de listagem — autorizacao fica a
// cargo do caller (guardiao validado, ou proprio usuario).
func (a *apiAdapter) listMedicationsForOwner(ownerID int64) ([]api.MedicationItem, error) {
	meds, err := a.db.ListActiveMedications(ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]api.MedicationItem, 0, len(meds))
	for i := range meds {
		out = append(out, a.buildMedicationItem(&meds[i]))
	}
	return out, nil
}

// buildMedicationItem monta a forma publica do medicamento, lendo os schedules
// pra compor o texto humano e a data de termino (quando temporario).
func (a *apiAdapter) buildMedicationItem(m *Medication) api.MedicationItem {
	scheds, err := a.db.ListSchedulesForMedication(m.ID)
	if err != nil {
		scheds = nil
	}
	text, endsAt := summarizeSchedules(scheds)
	times, freq, days := scheduleFormFields(scheds)
	return api.MedicationItem{
		ID:               m.ID,
		Name:             m.Name,
		Dose:             m.Dose,
		Instructions:     m.Instructions,
		Schedule:         text,
		Active:           m.Active,
		EndsAt:           endsAt,
		ToleranceMinutes: m.ToleranceMinutes,
		LateDosePolicy:   string(m.LateDosePolicy),
		Times:            times,
		Frequency:        freq,
		Days:             days,
	}
}

// scheduleFormFields extrai do PRIMEIRO schedule os campos estruturados que o
// form de edicao precisa pre-preencher: horarios "HH:MM", frequencia
// daily|weekly e dias da semana (mon..sun) quando weekly. Best-effort — devolve
// daily/sem horarios se nao conseguir parsear.
func scheduleFormFields(scheds []MedicationSchedule) (times []string, frequency string, days []string) {
	frequency = "daily"
	times = []string{}
	if len(scheds) == 0 {
		return times, frequency, days
	}
	raw := strings.TrimPrefix(strings.TrimSpace(scheds[0].RRULE), "RRULE:")
	opts, err := rrule.StrToROption(raw)
	if err != nil {
		return times, frequency, days
	}
	minute := 0
	if len(opts.Byminute) > 0 {
		minute = opts.Byminute[0]
	}
	hours := append([]int(nil), opts.Byhour...)
	sort.Ints(hours)
	for _, h := range hours {
		times = append(times, fmt.Sprintf("%02d:%02d", h, minute))
	}
	if opts.Freq == rrule.WEEKLY && len(opts.Byweekday) > 0 {
		frequency = "weekly"
		short := [...]string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
		for _, d := range opts.Byweekday {
			if i := d.Day(); i >= 0 && i < len(short) {
				days = append(days, short[i])
			}
		}
	}
	return times, frequency, days
}

// summarizeSchedules junta os schedules num texto PT-BR e calcula a data de
// termino do tratamento. Se QUALQUER schedule eh continuo (sem end_date), o
// medicamento eh tratado como continuo (sem ends_at) — o termino so existe
// quando todos os schedules terminam. ends_at usa a maior data (YYYY-MM-DD).
func summarizeSchedules(scheds []MedicationSchedule) (text string, endsAt *string) {
	if len(scheds) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(scheds))
	var maxEnd *time.Time
	anyContinuous := false
	for _, s := range scheds {
		parts = append(parts, capitalizeFirst(DescribeRRULE(s.RRULE)))
		if s.EndDate == nil {
			anyContinuous = true
			continue
		}
		if maxEnd == nil || s.EndDate.After(*maxEnd) {
			e := *s.EndDate
			maxEnd = &e
		}
	}
	text = strings.Join(parts, "; ")
	if !anyContinuous && maxEnd != nil {
		iso := maxEnd.Format("2006-01-02")
		endsAt = &iso
		text = fmt.Sprintf("%s · até %s", text, maxEnd.Format("02/01/2006"))
	}
	return text, endsAt
}

// CreateDependentMedication cria o medicamento (dono=dependente, criado pelo
// guardiao) + 1 schedule. Audita medication_created no contexto do guardiao.
func (a *apiAdapter) CreateDependentMedication(ctx context.Context, guardianID, dependentID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrNotFound
	}
	item, err := a.createMedicationForOwner(dependentID, guardianID, in)
	if err != nil {
		return nil, err
	}
	if a.audit != nil {
		_ = a.audit.Log(guardianID, "medication_created", item.Name,
			fmt.Sprintf("dependent_id=%d|medication_id=%d", dependentID, item.ID))
	}
	return item, nil
}

// CreateMyMedication cria um medicamento do proprio usuario logado (dono ==
// criador). Mesmo motor de lembrete/escalacao dos dependentes — sem guardiao,
// a escalacao apenas insiste e marca missed (vide escalateToFamily).
func (a *apiAdapter) CreateMyMedication(ctx context.Context, userID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	item, err := a.createMedicationForOwner(userID, userID, in)
	if err != nil {
		return nil, err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "medication_created", item.Name,
			fmt.Sprintf("self=true|medication_id=%d", item.ID))
	}
	return item, nil
}

// createMedicationForOwner eh o caminho comum: monta a RRULE + resolve a data
// de termino (duracao) + persiste medicamento e schedule. Autorizacao fica a
// cargo do caller. start = agora no fuso BRT (dia de inicio do tratamento).
func (a *apiAdapter) createMedicationForOwner(ownerID, createdByID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	rrule, err := buildMedicationRRULE(in.Times, in.Frequency, in.Days)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}
	start := time.Now().In(BRT())
	endDate, err := resolveMedicationEndDate(start, in.Duration)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}

	policy, err := ValidateLateDosePolicy(strings.TrimSpace(in.LateDosePolicy))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}
	med := &Medication{
		UserID:           ownerID,
		Name:             strings.TrimSpace(in.Name),
		Dose:             strings.TrimSpace(in.Dose),
		Instructions:     strings.TrimSpace(in.Instructions),
		CreatedByUserID:  createdByID,
		ToleranceMinutes: in.ToleranceMinutes,
		LateDosePolicy:   policy,
	}
	if err := a.db.CreateMedication(med); err != nil {
		return nil, fmt.Errorf("create medication: %w", err)
	}
	sched := &MedicationSchedule{
		MedicationID: med.ID,
		RRULE:        rrule,
		StartDate:    start,
		EndDate:      endDate,
	}
	if err := a.db.CreateMedicationSchedule(sched); err != nil {
		return nil, fmt.Errorf("create medication schedule: %w", err)
	}
	med.Active = true
	item := a.buildMedicationItem(med)
	return &item, nil
}

// UpdateDependentMedication edita um medicamento do dependente (PUT/replace),
// validando guardiao + posse. Substitui dados + schedule pelo conteudo de `in`.
func (a *apiAdapter) UpdateDependentMedication(ctx context.Context, guardianID, dependentID, medID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrNotFound
	}
	item, err := a.updateMedicationForOwner(dependentID, medID, in)
	if err != nil {
		return nil, err
	}
	if a.audit != nil {
		_ = a.audit.Log(guardianID, "medication_edited", item.Name,
			fmt.Sprintf("dependent_id=%d|medication_id=%d", dependentID, medID))
	}
	return item, nil
}

// UpdateMyMedication edita um medicamento do proprio titular (PUT/replace).
func (a *apiAdapter) UpdateMyMedication(ctx context.Context, userID, medID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	item, err := a.updateMedicationForOwner(userID, medID, in)
	if err != nil {
		return nil, err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "medication_edited", item.Name,
			fmt.Sprintf("self=true|medication_id=%d", medID))
	}
	return item, nil
}

// updateMedicationForOwner aplica a edicao (replace) garantindo que medID
// pertence a ownerID (nao vaza existencia de remedio alheio). Atualiza campos
// do medicamento e SUBSTITUI o schedule pelo novo (times/frequency/days +
// duracao). Autorizacao fica a cargo do caller.
func (a *apiAdapter) updateMedicationForOwner(ownerID, medID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	med, err := a.db.GetMedicationByID(medID)
	if err != nil || med.UserID != ownerID {
		return nil, api.ErrNotFound
	}
	rruleStr, err := buildMedicationRRULE(in.Times, in.Frequency, in.Days)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}
	policy, err := ValidateLateDosePolicy(strings.TrimSpace(in.LateDosePolicy))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}
	start := time.Now().In(BRT())
	endDate, err := resolveMedicationEndDate(start, in.Duration)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}

	name := strings.TrimSpace(in.Name)
	dose := strings.TrimSpace(in.Dose)
	instr := strings.TrimSpace(in.Instructions)
	tol := in.ToleranceMinutes
	if err := a.db.UpdateMedicationFields(medID, &name, &dose, &instr, &tol, &policy); err != nil {
		return nil, fmt.Errorf("update medication fields: %w", err)
	}
	// Substitui o(s) schedule(s) pelo novo conjunto.
	if err := a.db.DeleteSchedulesForMedication(medID); err != nil {
		return nil, fmt.Errorf("delete schedules: %w", err)
	}
	if err := a.db.CreateMedicationSchedule(&MedicationSchedule{
		MedicationID: medID,
		RRULE:        rruleStr,
		StartDate:    start,
		EndDate:      endDate,
	}); err != nil {
		return nil, fmt.Errorf("create schedule: %w", err)
	}
	updated, err := a.db.GetMedicationByID(medID)
	if err != nil {
		return nil, err
	}
	item := a.buildMedicationItem(updated)
	return &item, nil
}

// resolveMedicationEndDate converte a duracao informada na data de termino
// (end_date do schedule). nil = tratamento continuo. As semanticas de periodo
// sao INCLUSIVAS: "por 3 dias" cobre hoje + 2 dias; o ExpandOccurrences soma
// 24h ao end_date pra incluir o ultimo dia inteiro.
func resolveMedicationEndDate(start time.Time, d *api.MedicationDuration) (*time.Time, error) {
	if d == nil {
		return nil, nil
	}
	loc := start.Location()
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)

	switch strings.ToLower(strings.TrimSpace(d.Kind)) {
	case "", "continuous":
		return nil, nil
	case "period":
		if d.Count < 1 {
			return nil, fmt.Errorf("informe um número de dias/semanas/meses maior que zero")
		}
		var end time.Time
		switch strings.ToLower(strings.TrimSpace(d.Unit)) {
		case "days":
			if d.Count > 365 {
				return nil, fmt.Errorf("duração máxima de 365 dias")
			}
			end = startDay.AddDate(0, 0, d.Count-1)
		case "weeks":
			if d.Count > 52 {
				return nil, fmt.Errorf("duração máxima de 52 semanas")
			}
			end = startDay.AddDate(0, 0, d.Count*7-1)
		case "months":
			if d.Count > 24 {
				return nil, fmt.Errorf("duração máxima de 24 meses")
			}
			end = startDay.AddDate(0, d.Count, 0).AddDate(0, 0, -1)
		default:
			return nil, fmt.Errorf("unidade de duração inválida: use days, weeks ou months")
		}
		return &end, nil
	case "until":
		end, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(d.Until), loc)
		if err != nil {
			return nil, fmt.Errorf("data de término inválida (use AAAA-MM-DD)")
		}
		if end.Before(startDay) {
			return nil, fmt.Errorf("a data de término precisa ser hoje ou no futuro")
		}
		return &end, nil
	default:
		return nil, fmt.Errorf("tipo de duração inválido")
	}
}

// DeactivateDependentMedication faz soft-delete do medicamento, validando que
// ele pertence ao dependente e que o guardiao eh autorizado.
func (a *apiAdapter) DeactivateDependentMedication(ctx context.Context, guardianID, dependentID, medID int64) error {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return err
	}
	if !ok {
		return api.ErrNotFound
	}
	med, err := a.db.GetMedicationByID(medID)
	if err != nil {
		return api.ErrNotFound
	}
	if med.UserID != dependentID {
		// Medicamento existe mas eh de outro usuario — nao vaza existencia.
		return api.ErrNotFound
	}
	if err := a.db.DeactivateMedication(medID); err != nil {
		return err
	}
	if a.audit != nil {
		_ = a.audit.Log(guardianID, "medication_canceled", med.Name,
			fmt.Sprintf("dependent_id=%d|medication_id=%d", dependentID, medID))
	}
	return nil
}

// DeactivateMyMedication faz soft-delete de um medicamento do proprio usuario,
// validando que o medicamento pertence a ele (nao vaza existencia de remedio
// alheio).
func (a *apiAdapter) DeactivateMyMedication(ctx context.Context, userID, medID int64) error {
	med, err := a.db.GetMedicationByID(medID)
	if err != nil {
		return api.ErrNotFound
	}
	if med.UserID != userID {
		return api.ErrNotFound
	}
	if err := a.db.DeactivateMedication(medID); err != nil {
		return err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "medication_canceled", med.Name,
			fmt.Sprintf("self=true|medication_id=%d", medID))
	}
	return nil
}

// buildMedicationRRULE monta a RRULE iCal a partir dos horarios (HH:MM),
// frequencia ("daily"|"weekly") e dias da semana (mon..sun, usados so em
// weekly). Times sao deduplicados/ordenados por hora; BYMINUTE assume :00 do
// primeiro horario por simplicidade do MVP (horarios em minutos distintos
// dentro da mesma hora nao sao suportados — fora do escopo de remedio).
func buildMedicationRRULE(times []string, frequency string, days []string) (string, error) {
	if len(times) == 0 {
		return "", fmt.Errorf("informe ao menos um horario")
	}
	hourSet := map[int]struct{}{}
	minute := 0
	minuteSet := false
	for _, t := range times {
		h, m, err := parseHHMM(t)
		if err != nil {
			return "", err
		}
		hourSet[h] = struct{}{}
		if !minuteSet {
			minute = m
			minuteSet = true
		}
	}
	hours := make([]int, 0, len(hourSet))
	for h := range hourSet {
		hours = append(hours, h)
	}
	sort.Ints(hours)
	hourStrs := make([]string, 0, len(hours))
	for _, h := range hours {
		hourStrs = append(hourStrs, strconv.Itoa(h))
	}
	byhour := "BYHOUR=" + strings.Join(hourStrs, ",")
	byminute := "BYMINUTE=" + strconv.Itoa(minute)

	switch strings.ToLower(strings.TrimSpace(frequency)) {
	case "daily":
		return fmt.Sprintf("FREQ=DAILY;%s;%s", byhour, byminute), nil
	case "weekly":
		byday, err := buildBYDAY(days)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("FREQ=WEEKLY;%s;%s;%s", byday, byhour, byminute), nil
	default:
		return "", fmt.Errorf("frequencia invalida: use daily ou weekly")
	}
}

// buildBYDAY converte dias en (mon..sun) -> "BYDAY=MO,WE". Ordem canonica
// MO..SU pra RRULE estavel.
func buildBYDAY(days []string) (string, error) {
	order := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	tokens := map[string]string{
		"mon": "MO", "tue": "TU", "wed": "WE", "thu": "TH",
		"fri": "FR", "sat": "SA", "sun": "SU",
	}
	present := map[string]bool{}
	for _, d := range days {
		key := strings.ToLower(strings.TrimSpace(d))
		if _, ok := tokens[key]; !ok {
			return "", fmt.Errorf("dia da semana invalido: %s", d)
		}
		present[key] = true
	}
	if len(present) == 0 {
		return "", fmt.Errorf("informe ao menos um dia da semana")
	}
	out := make([]string, 0, len(present))
	for _, d := range order {
		if present[d] {
			out = append(out, tokens[d])
		}
	}
	return "BYDAY=" + strings.Join(out, ","), nil
}

// capitalizeFirst deixa a primeira letra maiuscula — DescribeRRULE devolve
// "todos os dias...", e na UI fica melhor "Todos os dias...".
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}
