package main

import (
	"errors"
	"testing"
	"time"
)

func setupTestUser(t *testing.T, db *DB) *User {
	t.Helper()
	u := &User{
		PhoneNumber:       "5511999999999",
		Name:              "Waldyr",
		GoogleCalendarID:  "x@gmail.com",
		GoogleCredentials: "enc",
	}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(dateLayout, s, BRT())
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}

func TestCreateTravelPeriod(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID:       u.ID,
		StartDate:    mustDate(t, "2026-05-15"),
		EndDate:      mustDate(t, "2026-05-17"),
		Timezone:     "Europe/Paris",
		LocationName: "Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("CreateTravelPeriod: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("expected ID to be set")
	}
}

func TestCreateTravelPeriodInvalidTz(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID:    u.ID,
		StartDate: mustDate(t, "2026-05-15"),
		EndDate:   mustDate(t, "2026-05-17"),
		Timezone:  "Not/ARealTz",
	}
	if err := db.CreateTravelPeriod(p); err == nil {
		t.Fatal("expected error for invalid timezone")
	}
}

func TestCreateTravelPeriodEndBeforeStart(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID:    u.ID,
		StartDate: mustDate(t, "2026-05-17"),
		EndDate:   mustDate(t, "2026-05-15"),
		Timezone:  "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err == nil {
		t.Fatal("expected error when end before start")
	}
}

func TestCreateTravelPeriodOverlapRejected(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	first := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(first); err != nil {
		t.Fatalf("first: %v", err)
	}

	cases := map[string][2]string{
		"exact-same":     {"2026-05-15", "2026-05-17"},
		"starts-inside":  {"2026-05-16", "2026-05-20"},
		"ends-inside":    {"2026-05-10", "2026-05-16"},
		"fully-contains": {"2026-05-14", "2026-05-20"},
		"fully-inside":   {"2026-05-16", "2026-05-16"},
	}
	for name, rng := range cases {
		p := &TravelPeriod{
			UserID: u.ID, StartDate: mustDate(t, rng[0]),
			EndDate: mustDate(t, rng[1]), Timezone: "Europe/Lisbon",
		}
		if err := db.CreateTravelPeriod(p); !errors.Is(err, ErrTravelPeriodOverlap) {
			t.Errorf("%s: expected overlap error, got %v", name, err)
		}
	}
}

func TestCreateTravelPeriodAdjacentAllowed(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	first := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(first); err != nil {
		t.Fatalf("first: %v", err)
	}

	// Day immediately after — adjacent, not overlapping.
	second := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-18"),
		EndDate: mustDate(t, "2026-05-20"), Timezone: "Europe/Lisbon",
	}
	if err := db.CreateTravelPeriod(second); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestGetTravelPeriodForDate(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("CreateTravelPeriod: %v", err)
	}

	cases := []struct {
		date   string
		inside bool
	}{
		{"2026-05-14", false},
		{"2026-05-15", true}, // start boundary
		{"2026-05-16", true},
		{"2026-05-17", true}, // end boundary
		{"2026-05-18", false},
	}
	for _, c := range cases {
		got, err := db.GetTravelPeriodForDate(u.ID, mustDate(t, c.date))
		if err != nil {
			t.Errorf("%s: %v", c.date, err)
			continue
		}
		if c.inside && got == nil {
			t.Errorf("%s: expected period, got nil", c.date)
		}
		if !c.inside && got != nil {
			t.Errorf("%s: expected nil, got period %d", c.date, got.ID)
		}
	}
}

func TestGetEventTimezoneDefaultsToBRT(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	loc := db.GetEventTimezone(u.ID, mustDate(t, "2026-05-10"))
	if loc.String() != BRT().String() {
		t.Errorf("expected BRT, got %s", loc)
	}
}

func TestGetEventTimezoneReturnsPeriodTz(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("CreateTravelPeriod: %v", err)
	}

	loc := db.GetEventTimezone(u.ID, mustDate(t, "2026-05-16"))
	if loc.String() != "Europe/Paris" {
		t.Errorf("expected Europe/Paris, got %s", loc)
	}
}

func TestGetEventTimezoneUsesBRTAtBoundary(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	// Event at 23:00 UTC on May 17 is still May 17 in BRT (20:00).
	// The date-match should use BRT calendar date.
	p := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("CreateTravelPeriod: %v", err)
	}

	// 2026-05-17T23:00:00Z = 2026-05-17 20:00 BRT → still inside period.
	eventInUTC := time.Date(2026, 5, 17, 23, 0, 0, 0, time.UTC)
	loc := db.GetEventTimezone(u.ID, eventInUTC)
	if loc.String() != "Europe/Paris" {
		t.Errorf("expected Europe/Paris, got %s", loc)
	}

	// 2026-05-18T02:00:00Z = 2026-05-17 23:00 BRT → still May 17 in BRT → inside.
	eventLateUTC := time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC)
	loc = db.GetEventTimezone(u.ID, eventLateUTC)
	if loc.String() != "Europe/Paris" {
		t.Errorf("late UTC: expected Europe/Paris, got %s", loc)
	}

	// 2026-05-18T04:00:00Z = 2026-05-18 01:00 BRT → outside period.
	eventNextDayUTC := time.Date(2026, 5, 18, 4, 0, 0, 0, time.UTC)
	loc = db.GetEventTimezone(u.ID, eventNextDayUTC)
	if loc.String() != BRT().String() {
		t.Errorf("next day: expected BRT, got %s", loc)
	}
}

func TestListTravelPeriods(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	periods := []TravelPeriod{
		{UserID: u.ID, StartDate: mustDate(t, "2026-06-01"), EndDate: mustDate(t, "2026-06-05"), Timezone: "Europe/Paris"},
		{UserID: u.ID, StartDate: mustDate(t, "2026-05-15"), EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Lisbon"},
	}
	for i := range periods {
		if err := db.CreateTravelPeriod(&periods[i]); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	got, err := db.ListTravelPeriods(u.ID, false)
	if err != nil {
		t.Fatalf("ListTravelPeriods: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// Ordered by start_date asc — Lisbon first (May), then Paris (Jun).
	if got[0].Timezone != "Europe/Lisbon" {
		t.Errorf("expected Lisbon first, got %s", got[0].Timezone)
	}
}

func TestDeleteTravelPeriod(t *testing.T) {
	db := setupTestDB(t)
	u := setupTestUser(t, db)

	p := &TravelPeriod{
		UserID: u.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := db.DeleteTravelPeriod(p.ID, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := db.GetTravelPeriodForDate(u.ID, mustDate(t, "2026-05-16"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestDeleteTravelPeriodWrongUser(t *testing.T) {
	db := setupTestDB(t)
	u1 := setupTestUser(t, db)

	u2 := &User{PhoneNumber: "5511888888888", Name: "Other"}
	if err := db.CreateUser(u2); err != nil {
		t.Fatalf("create u2: %v", err)
	}

	p := &TravelPeriod{
		UserID: u1.ID, StartDate: mustDate(t, "2026-05-15"),
		EndDate: mustDate(t, "2026-05-17"), Timezone: "Europe/Paris",
	}
	if err := db.CreateTravelPeriod(p); err != nil {
		t.Fatalf("create: %v", err)
	}

	// u2 trying to delete u1's period — should fail.
	if err := db.DeleteTravelPeriod(p.ID, u2.ID); err == nil {
		t.Fatal("expected error when deleting another user's period")
	}
}
