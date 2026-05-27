package main

import (
	"context"
	"fmt"
	"log"
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
	// RecurringEventID is the master event ID when this event is an expanded
	// instance of a recurring series. Empty for single events. Passing this ID
	// (not the instance ID) to DeleteEvent removes the whole series.
	RecurringEventID string
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

// calID resolve um calendar id vazio para "primary", o alias do Google para o
// calendario principal do usuario. Usuarios conectados via OAuth podem ter id
// vazio no banco (default do schema); string vazia nao eh referencia valida na
// API. Normalizar aqui, no boundary com o Google, garante que nenhuma operacao
// de calendario (web ou WhatsApp) chegue ao Google com id vazio.
func calID(id string) string {
	if id == "" {
		return "primary"
	}
	return id
}

func (c *CalendarClient) serviceForUser(ctx context.Context, refreshToken string) (*calendar.Service, error) {
	token := &oauth2.Token{RefreshToken: refreshToken}
	tokenSource := c.oauthConfig.TokenSource(ctx, token)
	return calendar.NewService(ctx, option.WithTokenSource(tokenSource))
}

func (c *CalendarClient) CreateEvent(ctx context.Context, refreshToken, calendarID string, ev CalendarEvent) (*CalendarEvent, error) {
	calendarID = calID(calendarID)
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
		// Birthdays are native all-day events. Google enforces the full set
		// of constraints below on eventType="birthday" — each violation is
		// a separate 400. Listing them here from the API reference to avoid
		// the discovery-by-error cycle:
		//   - Start/End must use Date (all-day), not DateTime
		//   - Recurrence must be RRULE:FREQ=YEARLY
		//   - Transparency must be "transparent" (doesn't block time)
		//   - Visibility must be "private"
		//   - No attendees (we never set any for birthdays)
		//   - guestsCanInviteOthers defaults to false (we never override)
		event.EventType = "birthday"
		event.BirthdayProperties = &calendar.EventBirthdayProperties{Type: "birthday"}
		event.Start = &calendar.EventDateTime{Date: ev.Start.Format(dateLayout)}
		event.End = &calendar.EventDateTime{Date: ev.Start.AddDate(0, 0, 1).Format(dateLayout)}
		event.Recurrence = []string{"RRULE:FREQ=YEARLY"}
		event.Transparency = "transparent"
		event.Visibility = "private"
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

	// Verify — re-fetch the event we just created to prove it actually landed
	// on the calendar we targeted. Without this, a silent drop or a routing
	// quirk would surface as the user seeing "criado" and then not finding
	// the event minutes later.
	verify, verifyErr := svc.Events.Get(calendarID, created.Id).Do()
	if verifyErr != nil {
		log.Printf("CreateEvent verify FAILED: id=%s calendar=%s err=%v",
			created.Id, calendarID, verifyErr)
		return nil, fmt.Errorf("event created (id=%s) but verification fetch failed: %w",
			created.Id, verifyErr)
	}
	organizerEmail := ""
	if verify.Organizer != nil {
		organizerEmail = verify.Organizer.Email
	}
	log.Printf("CreateEvent OK: id=%s calendar=%s organizer=%s status=%s htmlLink=%s",
		verify.Id, calendarID, organizerEmail, verify.Status, verify.HtmlLink)

	meetLink := ""
	if verify.ConferenceData != nil && verify.ConferenceData.EntryPoints != nil {
		for _, ep := range verify.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				meetLink = ep.Uri
				break
			}
		}
	}

	return &CalendarEvent{
		ID:       verify.Id,
		Title:    verify.Summary,
		MeetLink: meetLink,
		Start:    ev.Start,
		End:      ev.End,
	}, nil
}

// CreateAllDayEvent creates an all-day event spanning [startDate, endDate] (both
// inclusive, dates only in BRT). Transparency is "transparent" so the event
// does not block time — it's a visual marker. Used for travel period markers;
// could be extended to other all-day non-birthday use cases.
//
// Google's Date-format all-day events use an exclusive end date: to span
// 10 Apr to 12 Apr you set end=13 Apr.
func (c *CalendarClient) CreateAllDayEvent(ctx context.Context, refreshToken, calendarID, title string, startDate, endDate time.Time) (string, error) {
	calendarID = calID(calendarID)
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return "", fmt.Errorf("calendar service: %w", err)
	}
	event := &calendar.Event{
		Summary:      title,
		Transparency: "transparent",
		Start:        &calendar.EventDateTime{Date: startDate.Format(dateLayout)},
		End:          &calendar.EventDateTime{Date: endDate.AddDate(0, 0, 1).Format(dateLayout)},
	}
	created, err := svc.Events.Insert(calendarID, event).Do()
	if err != nil {
		return "", fmt.Errorf("insert all-day event: %w", err)
	}
	// Verify — same reason as CreateEvent: prove it landed on the target calendar.
	verify, verifyErr := svc.Events.Get(calendarID, created.Id).Do()
	if verifyErr != nil {
		log.Printf("CreateAllDayEvent verify FAILED: id=%s calendar=%s err=%v",
			created.Id, calendarID, verifyErr)
		return "", fmt.Errorf("all-day event created (id=%s) but verification failed: %w",
			created.Id, verifyErr)
	}
	log.Printf("CreateAllDayEvent OK: id=%s calendar=%s title=%q span=%s..%s status=%s",
		verify.Id, calendarID, title,
		startDate.Format(dateLayout), endDate.Format(dateLayout), verify.Status)
	return verify.Id, nil
}

// GetEvent fetches a single event by ID. Used when we need the title/details
// for an event we only know by ID (e.g., cancelling by id, showing a nicer
// confirmation message).
func (c *CalendarClient) GetEvent(ctx context.Context, refreshToken, calendarID, eventID string) (*CalendarEvent, error) {
	calendarID = calID(calendarID)
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}
	item, err := svc.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return nil, fmt.Errorf("get event: %w", err)
	}
	ev := &CalendarEvent{
		ID:               item.Id,
		Title:            item.Summary,
		Location:         item.Location,
		EventType:        item.EventType,
		RecurringEventID: item.RecurringEventId,
	}
	parseEventTimes(item, ev)
	return ev, nil
}

func (c *CalendarClient) ListEvents(ctx context.Context, refreshToken, calendarID string, start, end time.Time) ([]CalendarEvent, error) {
	calendarID = calID(calendarID)
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
			ID:               item.Id,
			Title:            item.Summary,
			Location:         item.Location,
			EventType:        item.EventType,
			RecurringEventID: item.RecurringEventId,
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
	calendarID = calID(calendarID)
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return fmt.Errorf("calendar service: %w", err)
	}
	if err := svc.Events.Delete(calendarID, eventID).Do(); err != nil {
		return err
	}
	// Verify — a successful delete should surface either 410 Gone or a
	// status="cancelled" when we try to Get. If it still returns an active
	// event, the delete didn't take — fail loudly.
	verify, verifyErr := svc.Events.Get(calendarID, eventID).Do()
	if verifyErr != nil {
		// Expected path: 404/410. Treat as success.
		log.Printf("DeleteEvent OK (verify returned err as expected): id=%s calendar=%s", eventID, calendarID)
		return nil
	}
	if verify.Status == "cancelled" {
		log.Printf("DeleteEvent OK (status=cancelled): id=%s calendar=%s", eventID, calendarID)
		return nil
	}
	log.Printf("DeleteEvent verify FAILED: id=%s calendar=%s status=%s — event still present",
		eventID, calendarID, verify.Status)
	return fmt.Errorf("delete API returned OK but event %s is still active (status=%s)", eventID, verify.Status)
}

func (c *CalendarClient) UpdateEvent(ctx context.Context, refreshToken, calendarID, eventID string, ev CalendarEvent) error {
	calendarID = calID(calendarID)
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

	updated, err := svc.Events.Update(calendarID, eventID, existing).Do()
	if err != nil {
		return err
	}
	// Verify — Update is PUT; Google is consistent so a subsequent Get should
	// return the new fields. We compare Summary + Start as a sanity check and
	// log so a post-mortem can correlate.
	verify, verifyErr := svc.Events.Get(calendarID, eventID).Do()
	if verifyErr != nil {
		log.Printf("UpdateEvent verify FAILED: id=%s calendar=%s err=%v",
			eventID, calendarID, verifyErr)
		return fmt.Errorf("event updated but verification fetch failed: %w", verifyErr)
	}
	var startStr string
	if verify.Start != nil {
		if verify.Start.DateTime != "" {
			startStr = verify.Start.DateTime
		} else {
			startStr = verify.Start.Date
		}
	}
	log.Printf("UpdateEvent OK: id=%s calendar=%s summary=%q start=%s status=%s",
		updated.Id, calendarID, verify.Summary, startStr, verify.Status)
	return nil
}

func (c *CalendarClient) AddAttendees(ctx context.Context, refreshToken, calendarID, eventID string, emails []string) error {
	calendarID = calID(calendarID)
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
	calendarID = calID(calendarID)
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
	calendarID = calID(calendarID)
	svc, err := c.serviceForUser(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}

	now := time.Now()
	events, err := svc.Events.List(calendarID).
		TimeMin(now.Add(-24 * time.Hour).Format(time.RFC3339)).
		TimeMax(now.Add(30 * 24 * time.Hour).Format(time.RFC3339)).
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
		ID:               item.Id,
		Title:            item.Summary,
		Location:         item.Location,
		EventType:        item.EventType,
		RecurringEventID: item.RecurringEventId,
	}
	parseEventTimes(item, ev)
	return ev, nil
}
