package main

import (
	"encoding/json"
	"time"
)

// IntentData is kept for backward compatibility with ConfirmationManager and Scheduler
// which still use it for pending confirmations.
type IntentData struct {
	Title           string `json:"title,omitempty"`
	Date            string `json:"date,omitempty"`
	Time            string `json:"time,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	Location        string `json:"location,omitempty"`
	TargetUser      string `json:"target_user,omitempty"`
	Recurrence      string `json:"recurrence,omitempty"`
	IsBirthday      bool   `json:"is_birthday,omitempty"`

	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	SearchQuery string          `json:"search_query,omitempty"`
	Changes     json.RawMessage `json:"changes,omitempty"`

	// Fase 3 (idosos): quando este pending eh de medicacao, Medication eh
	// populado e os campos de evento ficam vazios. Coexiste com TargetUser
	// quando responsavel cadastra remedio pra dependente.
	Medication *MedicationIntent `json:"medication,omitempty"`
}

// MedicationIntent carrega o payload de pendings de medicacao.
//
// Dois fluxos:
//   1. Cadastro pendente:  Reminder=false. Campos Name/Dose/.../ScheduleRRULE/StartDate.
//   2. Lembrete da hora:   Reminder=true. Campos MedicationID/ScheduledAt.
type MedicationIntent struct {
	// Para "criar cadastro de medicacao" pendente de confirmacao:
	Name          string `json:"name,omitempty"`
	Dose          string `json:"dose,omitempty"`
	Instructions  string `json:"instructions,omitempty"`
	ScheduleRRULE string `json:"schedule_rrule,omitempty"`
	StartDate     string `json:"start_date,omitempty"`
	EndDate       string `json:"end_date,omitempty"`
	Critical      bool   `json:"critical,omitempty"`

	// Para "lembrete de tomada" pendente de confirmacao:
	MedicationID int64     `json:"medication_id,omitempty"`
	ScheduledAt  time.Time `json:"scheduled_at,omitempty"`
	Reminder     bool      `json:"reminder,omitempty"`
}
