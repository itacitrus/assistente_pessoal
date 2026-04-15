package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarClient struct {
	oauthConfig *oauth2.Config
}

type CalendarEvent struct {
	ID         string
	Title      string
	Start      time.Time
	End        time.Time
	Location   string
	MeetLink   string
	Attendees  []string
	Timezone   string   // defaults to America/Sao_Paulo
	Recurrence []string // iCal RRULE, e.g. ["RRULE:FREQ=YEARLY"]
	// EventType mirrors Google Calendar's `eventType` field. Currently we only
	// create events of type "birthday" (see CreateEvent). Populated on read.
	EventType string
}

func NewCalendarClient(clientID, clientSecret, redirectURI string) *CalendarClient {
	return &CalendarClient{
		oauthConfig: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURI,
			Scopes:       []string{calendar.CalendarEventsScope},
			Endpoint:     google.Endpoint,
		},
	}
}

func (c *CalendarClient) AuthURL(state string) string {
	return c.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

func (c *CalendarClient) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return c.oauthConfig.Exchange(ctx, code)
}

func (c *CalendarClient) serviceForUser(ctx context.Context, refreshToken string) (*calendar.Service, error) {
	token := &oauth2.Token{RefreshToken: refreshToken}
	tokenSource := c.oauthConfig.TokenSource(ctx, token)
	return calendar.NewService(ctx, option.WithTokenSource(tokenSource))
}

func (c *CalendarClient) CreateEvent(ctx context.Context, refreshToken, calendarID string, ev CalendarEvent) (*CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	tz := ev.Timezone
	if tz == "" {
		tz = "America/Sao_Paulo"
	}

	event := &calendar.Event{
		Summary:  ev.Title,
		Location: ev.Location,
	}

	if ev.EventType == "birthday" {
		// Birthdays are native all-day events. Google API enforces that
		// eventType="birthday" MUST include RRULE:FREQ=YEARLY (returns HTTP 400
		// "'birthday' event type must have an annual recurrence" otherwise).
		// Use Date (not DateTime) + BirthdayProperties.Type.
		event.EventType = "birthday"
		event.BirthdayProperties = &calendar.EventBirthdayProperties{Type: "birthday"}
		event.Start = &calendar.EventDateTime{Date: ev.Start.Format(dateLayout)}
		event.End = &calendar.EventDateTime{Date: ev.Start.AddDate(0, 0, 1).Format(dateLayout)}
		event.Recurrence = []string{"RRULE:FREQ=YEARLY"}
	} else {
		event.Start = &calendar.EventDateTime{
			DateTime: ev.Start.Format(time.RFC3339),
			TimeZone: tz,
		}
		event.End = &calendar.EventDateTime{
			DateTime: ev.End.Format(time.RFC3339),
			TimeZone: tz,
		}
		if len(ev.Recurrence) > 0 {
			event.Recurrence = ev.Recurrence
		}
	}

	// Add attendees
	if len(ev.Attendees) > 0 {
		for _, email := range ev.Attendees {
			event.Attendees = append(event.Attendees, &calendar.EventAttendee{Email: email})
		}
	}

	// Add Google Meet if requested
	if ev.MeetLink == "generate" {
		event.ConferenceData = &calendar.ConferenceData{
			CreateRequest: &calendar.CreateConferenceRequest{
				RequestId:             fmt.Sprintf("meet-%d", time.Now().UnixNano()),
				ConferenceSolutionKey: &calendar.ConferenceSolutionKey{Type: "hangoutsMeet"},
			},
		}
	}

	insertCall := svc.Events.Insert(calendarID, event).SendUpdates("all")
	if ev.MeetLink == "generate" {
		insertCall = insertCall.ConferenceDataVersion(1)
	}
	created, err := insertCall.Do()
	if err != nil {
		return nil, fmt.Errorf("insert event: %w", err)
	}

	meetLink := ""
	if created.ConferenceData != nil && created.ConferenceData.EntryPoints != nil {
		for _, ep := range created.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				meetLink = ep.Uri
				break
			}
		}
	}

	return &CalendarEvent{
		ID:       created.Id,
		Title:    created.Summary,
		MeetLink: meetLink,
		Start:    ev.Start,
		End:   ev.End,
	}, nil
}

func (c *CalendarClient) ListEvents(ctx context.Context, refreshToken, calendarID string, start, end time.Time) ([]CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	events, err := svc.Events.List(calendarID).
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}

	var result []CalendarEvent
	for _, item := range events.Items {
		ev := CalendarEvent{
			ID:        item.Id,
			Title:     item.Summary,
			Location:  item.Location,
			EventType: item.EventType,
		}
		parseEventTimes(item, &ev)
		result = append(result, ev)
	}
	return result, nil
}

// parseEventTimes fills ev.Start/ev.End from a Google Calendar event item.
// Handles both timed events (DateTime field) and all-day events (Date field,
// e.g. birthdays, holidays, multi-day events imported from external sources).
func parseEventTimes(item *calendar.Event, ev *CalendarEvent) {
	if item.Start != nil {
		if item.Start.DateTime != "" {
			ev.Start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
		} else if item.Start.Date != "" {
			ev.Start, _ = time.ParseInLocation(dateLayout, item.Start.Date, BRT())
		}
	}
	if item.End != nil {
		if item.End.DateTime != "" {
			ev.End, _ = time.Parse(time.RFC3339, item.End.DateTime)
		} else if item.End.Date != "" {
			ev.End, _ = time.ParseInLocation(dateLayout, item.End.Date, BRT())
		}
	}
}

func (c *CalendarClient) DeleteEvent(ctx context.Context, refreshToken, calendarID, eventID string) error {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}
	return svc.Events.Delete(calendarID, eventID).Do()
}

func (c *CalendarClient) UpdateEvent(ctx context.Context, refreshToken, calendarID, eventID string, ev CalendarEvent) error {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}

	// Fetch the existing event first, then modify fields
	existing, err := svc.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return fmt.Errorf("get event for update: %w", err)
	}

	if ev.Title != "" {
		existing.Summary = ev.Title
	}
	if ev.Location != "" {
		existing.Location = ev.Location
	}
	// Preserve the all-day Date format when editing a birthday-typed event.
	// Google makes EventType immutable after creation; switching to DateTime
	// would either fail or silently degrade the event.
	isAllDay := existing.EventType == "birthday"
	if !ev.Start.IsZero() {
		if isAllDay {
			existing.Start = &calendar.EventDateTime{Date: ev.Start.Format(dateLayout)}
		} else {
			existing.Start = &calendar.EventDateTime{
				DateTime: ev.Start.Format(time.RFC3339),
				TimeZone: "America/Sao_Paulo",
			}
		}
	}
	if !ev.End.IsZero() {
		if isAllDay {
			existing.End = &calendar.EventDateTime{Date: ev.End.Format(dateLayout)}
		} else {
			existing.End = &calendar.EventDateTime{
				DateTime: ev.End.Format(time.RFC3339),
				TimeZone: "America/Sao_Paulo",
			}
		}
	}

	_, err = svc.Events.Update(calendarID, eventID, existing).Do()
	return err
}

func (c *CalendarClient) AddAttendees(ctx context.Context, refreshToken, calendarID, eventID string, emails []string) error {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}

	existing, err := svc.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}

	for _, email := range emails {
		existing.Attendees = append(existing.Attendees, &calendar.EventAttendee{Email: email})
	}

	_, err = svc.Events.Update(calendarID, eventID, existing).SendUpdates("all").Do()
	return err
}

func (c *CalendarClient) AddMeetLink(ctx context.Context, refreshToken, calendarID, eventID string) (string, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return "", fmt.Errorf("calendar service: %w", err)
	}

	existing, err := svc.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return "", fmt.Errorf("get event: %w", err)
	}

	existing.ConferenceData = &calendar.ConferenceData{
		CreateRequest: &calendar.CreateConferenceRequest{
			RequestId:             fmt.Sprintf("meet-%d", time.Now().UnixNano()),
			ConferenceSolutionKey: &calendar.ConferenceSolutionKey{Type: "hangoutsMeet"},
		},
	}

	updated, err := svc.Events.Update(calendarID, eventID, existing).ConferenceDataVersion(1).Do()
	if err != nil {
		return "", fmt.Errorf("add meet: %w", err)
	}

	if updated.ConferenceData != nil {
		for _, ep := range updated.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				return ep.Uri, nil
			}
		}
	}
	return "", fmt.Errorf("meet link not generated")
}

func (c *CalendarClient) FindEvent(ctx context.Context, refreshToken, calendarID, query string) (*CalendarEvent, error) {
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	now := time.Now()
	events, err := svc.Events.List(calendarID).
		TimeMin(now.Add(-24*time.Hour).Format(time.RFC3339)).
		TimeMax(now.Add(30*24*time.Hour).Format(time.RFC3339)).
		Q(query).
		SingleEvents(true).
		OrderBy("startTime").
		MaxResults(1).
		Do()
	if err != nil {
		return nil, fmt.Errorf("search events: %w", err)
	}

	if len(events.Items) == 0 {
		return nil, fmt.Errorf("nenhum evento encontrado para: %s", query)
	}

	item := events.Items[0]
	ev := &CalendarEvent{
		ID:        item.Id,
		Title:     item.Summary,
		Location:  item.Location,
		EventType: item.EventType,
	}
	parseEventTimes(item, ev)
	return ev, nil
}
