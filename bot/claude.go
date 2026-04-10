package main

import "encoding/json"

// IntentData is kept for backward compatibility with ConfirmationManager and Scheduler
// which still use it for pending confirmations.
type IntentData struct {
	Title           string `json:"title,omitempty"`
	Date            string `json:"date,omitempty"`
	Time            string `json:"time,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	Location        string `json:"location,omitempty"`
	TargetUser      string `json:"target_user,omitempty"`

	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`

	SearchQuery string          `json:"search_query,omitempty"`
	Changes     json.RawMessage `json:"changes,omitempty"`
}
