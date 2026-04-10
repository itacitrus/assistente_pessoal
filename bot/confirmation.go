package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ConfirmationManager struct {
	db  *DB
	cal *CalendarClient
	cfg *Config
}

func NewConfirmationManager(db *DB, cal *CalendarClient, cfg *Config) *ConfirmationManager {
	return &ConfirmationManager{db: db, cal: cal, cfg: cfg}
}

func (cm *ConfirmationManager) CreatePending(user *User, intentData IntentData, confirmMsg string) (string, error) {
	eventJSON, err := json.Marshal(intentData)
	if err != nil {
		return "", fmt.Errorf("marshal event data: %w", err)
	}

	pc := &PendingConfirmation{
		UserID:    user.ID,
		EventData: string(eventJSON),
	}
	if err := cm.db.CreatePendingConfirmation(pc); err != nil {
		return "", fmt.Errorf("save pending: %w", err)
	}

	return confirmMsg, nil
}

func (cm *ConfirmationManager) Confirm(user *User) (string, error) {
	pc, err := cm.db.GetPendingConfirmation(user.ID)
	if err == ErrNoPendingConfirmation {
		return "Nao ha nenhuma acao pendente para confirmar.", nil
	}
	if err != nil {
		return "", err
	}

	return cm.executeConfirmation(user, pc)
}

func (cm *ConfirmationManager) Deny(user *User) (string, error) {
	pc, err := cm.db.GetPendingConfirmation(user.ID)
	if err == ErrNoPendingConfirmation {
		return "Nao ha nenhuma acao pendente para cancelar.", nil
	}
	if err != nil {
		return "", err
	}

	if err := cm.db.ResolvePendingConfirmation(pc.ID, "cancelled"); err != nil {
		return "", err
	}
	return "Ok, cancelado!", nil
}

func (cm *ConfirmationManager) executeConfirmation(user *User, pc *PendingConfirmation) (string, error) {
	var data IntentData
	if err := json.Unmarshal([]byte(pc.EventData), &data); err != nil {
		return "", fmt.Errorf("unmarshal event data: %w", err)
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, cm.cfg.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt credentials: %w", err)
	}

	startTime, err := time.ParseInLocation("2006-01-02 15:04", data.Date+" "+data.Time, time.Local)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}

	duration := time.Duration(data.DurationMinutes) * time.Minute
	if data.DurationMinutes == 0 {
		duration = 60 * time.Minute
	}

	ev := CalendarEvent{
		Title:    data.Title,
		Location: data.Location,
		Start:    startTime,
		End:      startTime.Add(duration),
	}

	created, err := cm.cal.CreateEvent(context.Background(), refreshToken, user.GoogleCalendarID, ev)
	if err != nil {
		return "", fmt.Errorf("create calendar event: %w", err)
	}

	if err := cm.db.ResolvePendingConfirmation(pc.ID, "confirmed"); err != nil {
		return "", err
	}

	return FormatEventCreated(*created), nil
}
