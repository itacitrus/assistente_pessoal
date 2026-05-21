package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
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
	meds, err := a.db.ListActiveMedications(dependentID)
	if err != nil {
		return nil, err
	}
	out := make([]api.MedicationItem, 0, len(meds))
	for _, m := range meds {
		out = append(out, api.MedicationItem{
			ID:           m.ID,
			Name:         m.Name,
			Dose:         m.Dose,
			Instructions: m.Instructions,
			Schedule:     a.describeMedicationSchedule(m.ID),
			Active:       m.Active,
		})
	}
	return out, nil
}

// describeMedicationSchedule monta o texto humano juntando todos os schedules
// do medicamento (best-effort — fallback pra string vazia se nao ha schedule).
func (a *apiAdapter) describeMedicationSchedule(medID int64) string {
	scheds, err := a.db.ListSchedulesForMedication(medID)
	if err != nil || len(scheds) == 0 {
		return ""
	}
	parts := make([]string, 0, len(scheds))
	for _, s := range scheds {
		parts = append(parts, capitalizeFirst(DescribeRRULE(s.RRULE)))
	}
	return strings.Join(parts, "; ")
}

// CreateDependentMedication cria o medicamento (dono=dependente, criado pelo
// guardiao) + 1 schedule com a RRULE montada a partir de times/frequency/days.
// Audita medication_created no contexto do guardiao.
func (a *apiAdapter) CreateDependentMedication(ctx context.Context, guardianID, dependentID int64, in api.CreateMedicationRequest) (*api.MedicationItem, error) {
	ok, err := a.db.IsGuardianOf(guardianID, dependentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.ErrNotFound
	}

	rrule, err := buildMedicationRRULE(in.Times, in.Frequency, in.Days)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", api.ErrValidation, err)
	}

	med := &Medication{
		UserID:          dependentID,
		Name:            strings.TrimSpace(in.Name),
		Dose:            strings.TrimSpace(in.Dose),
		Instructions:    strings.TrimSpace(in.Instructions),
		CreatedByUserID: guardianID,
	}
	if err := a.db.CreateMedication(med); err != nil {
		return nil, fmt.Errorf("create medication: %w", err)
	}
	sched := &MedicationSchedule{
		MedicationID: med.ID,
		RRULE:        rrule,
		StartDate:    time.Now().In(BRT()),
	}
	if err := a.db.CreateMedicationSchedule(sched); err != nil {
		return nil, fmt.Errorf("create medication schedule: %w", err)
	}

	if a.audit != nil {
		_ = a.audit.Log(guardianID, "medication_created", med.Name,
			fmt.Sprintf("dependent_id=%d|medication_id=%d|rrule=%s", dependentID, med.ID, rrule))
	}

	return &api.MedicationItem{
		ID:           med.ID,
		Name:         med.Name,
		Dose:         med.Dose,
		Instructions: med.Instructions,
		Schedule:     capitalizeFirst(DescribeRRULE(rrule)),
		Active:       true,
	}, nil
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
