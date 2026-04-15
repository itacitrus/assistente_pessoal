package main

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// TravelPeriod represents a window during which the user will be in a different
// timezone. Events whose date falls within [StartDate, EndDate] are interpreted
// and displayed in this period's Timezone instead of the user's default (BRT).
type TravelPeriod struct {
	ID              int64
	UserID          int64
	StartDate       time.Time // date-only (midnight in BRT, used only for comparison)
	EndDate         time.Time // inclusive
	Timezone        string    // IANA, e.g. "Europe/Paris"
	LocationName    string
	CalendarEventID string // ID of the "Viagem" all-day marker event on Google Calendar
	CreatedAt       time.Time
}

var ErrTravelPeriodOverlap = errors.New("travel period overlaps with existing period")

// CreateTravelPeriod inserts a new period. Returns ErrTravelPeriodOverlap if
// the [start, end] range overlaps any existing period for the same user.
func (db *DB) CreateTravelPeriod(p *TravelPeriod) error {
	if p.EndDate.Before(p.StartDate) {
		return fmt.Errorf("end_date before start_date")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", p.Timezone, err)
	}

	startStr := p.StartDate.Format(dateLayout)
	endStr := p.EndDate.Format(dateLayout)

	var existingID int64
	err := db.conn.QueryRow(
		`SELECT id FROM user_travel_periods
		 WHERE user_id = ? AND NOT (end_date < ? OR start_date > ?)
		 LIMIT 1`, p.UserID, startStr, endStr,
	).Scan(&existingID)
	if err == nil {
		return ErrTravelPeriodOverlap
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check overlap: %w", err)
	}

	result, err := db.conn.Exec(
		`INSERT INTO user_travel_periods (user_id, start_date, end_date, timezone, location_name, calendar_event_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		p.UserID, startStr, endStr, p.Timezone, p.LocationName, p.CalendarEventID)
	if err != nil {
		return err
	}
	p.ID, _ = result.LastInsertId()
	return nil
}

// SetTravelCalendarEventID persists the Google Calendar event ID of the
// "Viagem" all-day marker so we can delete it if the period is canceled.
func (db *DB) SetTravelCalendarEventID(periodID int64, eventID string) error {
	_, err := db.conn.Exec(
		`UPDATE user_travel_periods SET calendar_event_id = ? WHERE id = ?`,
		eventID, periodID)
	return err
}

// GetTravelPeriodByID returns the single period matching id+userID, or nil
// if not found. Used by the cancel path to look up the linked calendar event.
func (db *DB) GetTravelPeriodByID(id, userID int64) (*TravelPeriod, error) {
	p := &TravelPeriod{}
	var startStr, endStr string
	err := db.conn.QueryRow(
		`SELECT id, user_id, start_date, end_date, timezone, location_name, calendar_event_id, created_at
		 FROM user_travel_periods WHERE id = ? AND user_id = ?`, id, userID,
	).Scan(&p.ID, &p.UserID, &startStr, &endStr, &p.Timezone, &p.LocationName, &p.CalendarEventID, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.StartDate, _ = time.ParseInLocation(dateLayout, startStr, BRT())
	p.EndDate, _ = time.ParseInLocation(dateLayout, endStr, BRT())
	return p, nil
}

// ListTravelPeriods returns periods for the user ordered by start_date asc.
// If onlyFuture is true, periods whose end_date is before today are excluded.
func (db *DB) ListTravelPeriods(userID int64, onlyFuture bool) ([]TravelPeriod, error) {
	query := `SELECT id, user_id, start_date, end_date, timezone, location_name, calendar_event_id, created_at
	          FROM user_travel_periods WHERE user_id = ?`
	args := []any{userID}
	if onlyFuture {
		query += ` AND end_date >= ?`
		args = append(args, time.Now().In(BRT()).Format(dateLayout))
	}
	query += ` ORDER BY start_date ASC`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TravelPeriod
	for rows.Next() {
		var p TravelPeriod
		var startStr, endStr string
		if err := rows.Scan(&p.ID, &p.UserID, &startStr, &endStr, &p.Timezone,
			&p.LocationName, &p.CalendarEventID, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.StartDate, _ = time.ParseInLocation(dateLayout, startStr, BRT())
		p.EndDate, _ = time.ParseInLocation(dateLayout, endStr, BRT())
		result = append(result, p)
	}
	return result, rows.Err()
}

// DeleteTravelPeriod removes a period. User-scoped to prevent cross-user deletes.
func (db *DB) DeleteTravelPeriod(id, userID int64) error {
	res, err := db.conn.Exec(
		`DELETE FROM user_travel_periods WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("travel period not found")
	}
	return nil
}

// GetTravelPeriodForDate returns the travel period covering the given date for
// the user, or nil if no period matches. The date is compared as YYYY-MM-DD in
// the user's home timezone (BRT) — this is a calendar-date match, not an
// instant match.
func (db *DB) GetTravelPeriodForDate(userID int64, date time.Time) (*TravelPeriod, error) {
	dateStr := date.In(BRT()).Format(dateLayout)
	p := &TravelPeriod{}
	var startStr, endStr string
	err := db.conn.QueryRow(
		`SELECT id, user_id, start_date, end_date, timezone, location_name, calendar_event_id, created_at
		 FROM user_travel_periods
		 WHERE user_id = ? AND start_date <= ? AND end_date >= ?
		 ORDER BY created_at DESC LIMIT 1`, userID, dateStr, dateStr,
	).Scan(&p.ID, &p.UserID, &startStr, &endStr, &p.Timezone, &p.LocationName, &p.CalendarEventID, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.StartDate, _ = time.ParseInLocation(dateLayout, startStr, BRT())
	p.EndDate, _ = time.ParseInLocation(dateLayout, endStr, BRT())
	return p, nil
}

// GetEventTimezone returns the location to use when interpreting or displaying
// an event that falls on the given date for the given user. Falls back to BRT
// if no travel period matches or on any error.
func (db *DB) GetEventTimezone(userID int64, eventDate time.Time) *time.Location {
	period, err := db.GetTravelPeriodForDate(userID, eventDate)
	if err != nil || period == nil {
		return BRT()
	}
	loc, err := time.LoadLocation(period.Timezone)
	if err != nil {
		return BRT()
	}
	return loc
}

// ApplyEventTimezones converts each event's Start/End to the location that
// applies to the user on that event's calendar date. This normalizes display:
// events on travel-period dates show in the period's tz, all others show BRT.
// The underlying instant is preserved — only the *time.Time's Location changes,
// so formatting via .Format("15:04") produces the right local hour.
func (db *DB) ApplyEventTimezones(userID int64, events []CalendarEvent) {
	for i := range events {
		db.ApplyEventTimezone(userID, &events[i])
	}
}

// ApplyEventTimezone is the single-event version of ApplyEventTimezones.
func (db *DB) ApplyEventTimezone(userID int64, event *CalendarEvent) {
	if event == nil || event.Start.IsZero() {
		return
	}
	loc := db.GetEventTimezone(userID, event.Start)
	event.Start = event.Start.In(loc)
	if !event.End.IsZero() {
		event.End = event.End.In(loc)
	}
}
