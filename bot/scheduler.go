package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron    *cron.Cron
	db      *DB
	cal     *CalendarClient
	cfg     *Config
	sendMsg func(phone, text string) error
}

func NewScheduler(db *DB, cal *CalendarClient, cfg *Config, sendMsg func(phone, text string) error) *Scheduler {
	return &Scheduler{
		cron:    cron.New(cron.WithLocation(time.Local)),
		db:      db,
		cal:     cal,
		cfg:     cfg,
		sendMsg: sendMsg,
	}
}

func (s *Scheduler) Start() {
	s.cron.AddFunc("* * * * *", s.checkReminders)
	s.cron.AddFunc("* * * * *", s.checkAutoConfirm)
	s.cron.AddFunc("* * * * *", s.checkDailySummaries)
	s.cron.AddFunc("* * * * *", s.checkWeeklySummaries)
	s.cron.AddFunc("* * * * *", s.checkExpiredPermissionRequests)

	s.cron.Start()
	log.Println("Scheduler started")
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) checkReminders() {
	users, err := s.db.ListActiveUsers()
	if err != nil {
		log.Printf("Scheduler: error listing users: %v", err)
		return
	}

	for _, user := range users {
		s.checkUserReminders(&user)
	}
}

func (s *Scheduler) checkUserReminders(user *User) {
	reminderDuration, err := time.ParseDuration(user.ReminderBefore)
	if err != nil {
		reminderDuration = time.Hour
	}

	refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
	if err != nil {
		return
	}

	now := time.Now()
	windowStart := now.Add(reminderDuration - 30*time.Second)
	windowEnd := now.Add(reminderDuration + 30*time.Second)

	ctx := context.Background()
	events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, windowStart, windowEnd)
	if err != nil {
		log.Printf("Scheduler: error listing events for %s: %v", user.Name, err)
		if IsInvalidGrantErr(err) {
			if _, reauthErr := SendReauthLinkIfDue(s.db, s.cal, s.sendMsg, user, time.Now()); reauthErr != nil {
				log.Printf("Scheduler: SendReauthLinkIfDue for %s: %v", user.Name, reauthErr)
			}
		}
		return
	}
	s.db.ApplyEventTimezones(user.ID, events)

	for _, ev := range events {
		sent, _ := s.db.HasSentReminder(user.ID, ev.ID)
		if sent {
			continue
		}

		msg := FormatReminder(ev)
		if err := s.sendMsg(user.PhoneNumber, msg); err != nil {
			log.Printf("Scheduler: error sending reminder to %s: %v", user.Name, err)
			continue
		}
		s.db.MarkReminderSent(user.ID, ev.ID)
		log.Printf("Scheduler: sent reminder to %s for %s", user.Name, ev.Title)
	}
}

func (s *Scheduler) checkAutoConfirm() {
	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		timeout, err := time.ParseDuration(user.AutoConfirmTimeout)
		if err != nil {
			timeout = s.cfg.DefaultAutoConfirmTimeout
		}

		expired, err := s.db.GetExpiredPendingConfirmations(user.ID, timeout)
		if err != nil {
			continue
		}

		for _, pc := range expired {
			cm := NewConfirmationManager(s.db, s.cal, s.cfg)
			msg, err := cm.executeConfirmation(&user, &pc)
			if err != nil {
				log.Printf("Scheduler: auto-confirm error for %s: %v", user.Name, err)
				s.db.ResolvePendingConfirmation(pc.ID, "error")
				continue
			}

			autoMsg := fmt.Sprintf("Confirmei automaticamente:\n\n%s", msg)
			s.sendMsg(user.PhoneNumber, autoMsg)
			log.Printf("Scheduler: auto-confirmed event for %s", user.Name)
		}
	}
}

func (s *Scheduler) checkDailySummaries() {
	now := time.Now()
	currentTime := now.Format("15:04")

	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		if user.DailySummaryTime != currentTime {
			continue
		}
		if now.Second() > 30 {
			continue
		}

		refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
		if err != nil {
			continue
		}

		ctx := context.Background()
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		dayEnd := dayStart.Add(24*time.Hour - time.Second)

		events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, dayStart, dayEnd)
		if err != nil {
			log.Printf("Scheduler: error getting daily events for %s: %v", user.Name, err)
			if IsInvalidGrantErr(err) {
				if _, reauthErr := SendReauthLinkIfDue(s.db, s.cal, s.sendMsg, &user, time.Now()); reauthErr != nil {
					log.Printf("Scheduler: SendReauthLinkIfDue for %s: %v", user.Name, reauthErr)
				}
			}
			continue
		}
		s.db.ApplyEventTimezones(user.ID, events)

		if len(events) == 0 {
			continue // Don't send daily summary if no events
		}

		msg := FormatDailySummary(user.Name, events, dayStart)
		s.sendMsg(user.PhoneNumber, msg)
		log.Printf("Scheduler: sent daily summary to %s (%d events)", user.Name, len(events))
	}
}

func (s *Scheduler) checkExpiredPermissionRequests() {
	// Expire permission requests older than 24 hours
	expired, err := s.db.GetExpiredPermissionRequests(24 * time.Hour)
	if err != nil {
		log.Printf("Scheduler: error getting expired permission requests: %v", err)
		return
	}

	for _, req := range expired {
		s.db.ResolvePermissionRequest(req.ID, "expired")
		// Notify requester that request expired
		msg := fmt.Sprintf("%s nao respondeu a sua solicitacao de acesso. Tente novamente mais tarde.", req.TargetName)
		if err := s.sendMsg(req.RequesterPhone, msg); err != nil {
			log.Printf("Scheduler: error notifying requester %s about expired permission: %v", req.RequesterName, err)
		}
		log.Printf("Scheduler: expired permission request from %s to %s", req.RequesterName, req.TargetName)
	}
}

func (s *Scheduler) checkWeeklySummaries() {
	now := time.Now()
	currentTime := now.Format("15:04")
	currentDay := now.Weekday().String()

	users, err := s.db.ListActiveUsers()
	if err != nil {
		return
	}

	for _, user := range users {
		if user.WeeklySummaryTime != currentTime {
			continue
		}
		if !strings.EqualFold(user.WeeklySummaryDay, currentDay) {
			continue
		}
		if now.Second() > 30 {
			continue
		}

		refreshToken, err := Decrypt(user.GoogleCredentials, s.cfg.EncryptionKey)
		if err != nil {
			continue
		}

		ctx := context.Background()
		weekStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		weekEnd := weekStart.AddDate(0, 0, 7)

		events, err := s.cal.ListEvents(ctx, refreshToken, user.GoogleCalendarID, weekStart, weekEnd)
		if err != nil {
			log.Printf("Scheduler: error getting weekly events for %s: %v", user.Name, err)
			continue
		}
		s.db.ApplyEventTimezones(user.ID, events)

		msg := FormatWeeklySummary(user.Name, events, weekStart)
		s.sendMsg(user.PhoneNumber, msg)
		log.Printf("Scheduler: sent weekly summary to %s (%d events)", user.Name, len(events))
	}
}
