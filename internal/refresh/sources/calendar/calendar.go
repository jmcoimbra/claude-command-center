package calendar

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/refresh"
	"golang.org/x/oauth2"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// CalendarSource fetches today/tomorrow calendar events from Google Calendar.
type CalendarSource struct {
	CalendarIDs       []string
	AutoAcceptDomains []string
	enabled           bool
}

// New creates a CalendarSource with the given config.
func New(enabled bool, calendarIDs, autoAcceptDomains []string) *CalendarSource {
	return &CalendarSource{
		CalendarIDs:       calendarIDs,
		AutoAcceptDomains: autoAcceptDomains,
		enabled:           enabled,
	}
}

func (s *CalendarSource) Name() string  { return "calendar" }
func (s *CalendarSource) Enabled() bool { return s.enabled }

func (s *CalendarSource) Fetch(ctx context.Context) (*refresh.SourceResult, error) {
	if err := MigrateCalendarCredentials(); err != nil {
		log.Printf("calendar credential migration: %v", err)
	}

	ts, err := loadCalendarAuth()
	if err != nil {
		return nil, fmt.Errorf("calendar auth: %w", err)
	}

	calendarIDs := s.CalendarIDs
	if len(calendarIDs) == 0 {
		calendarIDs = []string{"primary"}
	}

	data, err := fetchCalendarEvents(ctx, ts, calendarIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	if len(s.AutoAcceptDomains) > 0 {
		autoAccept(ctx, ts, s.AutoAcceptDomains)
	}

	return &refresh.SourceResult{Calendar: data}, nil
}

// PostMerge implements refresh.PostMerger. Previously executed pending calendar
// booking actions; bookings now happen directly from the TUI via scheduleBlockCmd.
func (s *CalendarSource) PostMerge(ctx context.Context, database *sql.DB, cc *db.CommandCenter, verbose bool) error {
	return nil
}

func fetchCalendarEvents(ctx context.Context, ts oauth2.TokenSource, calendarIDs []string) (*db.CalendarData, error) {
	srv, err := gcal.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayEnd := todayStart.Add(24 * time.Hour)
	tomorrowEnd := todayEnd.Add(24 * time.Hour)

	var todayEvents, tomorrowEvents []db.CalendarEvent

	for _, calID := range calendarIDs {
		today, err := listEvents(ctx, srv, calID, todayStart, todayEnd)
		if err != nil {
			log.Printf("calendar %s today fetch: %v", calID, err)
			continue
		}
		todayEvents = append(todayEvents, today...)

		tomorrow, err := listEvents(ctx, srv, calID, todayEnd, tomorrowEnd)
		if err != nil {
			log.Printf("calendar %s tomorrow fetch: %v", calID, err)
			continue
		}
		tomorrowEvents = append(tomorrowEvents, tomorrow...)
	}

	return &db.CalendarData{
		Today:    todayEvents,
		Tomorrow: tomorrowEvents,
	}, nil
}

func listEvents(ctx context.Context, srv *gcal.Service, calendarID string, timeMin, timeMax time.Time) ([]db.CalendarEvent, error) {
	events, err := srv.Events.List(calendarID).
		TimeMin(timeMin.Format(time.RFC3339)).
		TimeMax(timeMax.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	var result []db.CalendarEvent
	for _, item := range events.Items {
		if item.EventType == "workingLocation" {
			continue
		}

		ev := db.CalendarEvent{
			Title:      item.Summary,
			CalendarID: calendarID,
		}

		if item.Start.DateTime == "" {
			ev.AllDay = true
			if t, err := time.Parse("2006-01-02", item.Start.Date); err == nil {
				ev.Start = t
			}
			if t, err := time.Parse("2006-01-02", item.End.Date); err == nil {
				ev.End = t
			}
		} else {
			if t, err := parseDateTime(item.Start.DateTime); err == nil {
				ev.Start = t
			} else {
				log.Printf("calendar %s: unparseable start datetime %q for %q: %v", calendarID, item.Start.DateTime, item.Summary, err)
			}
			if t, err := parseDateTime(item.End.DateTime); err == nil {
				ev.End = t
			} else {
				log.Printf("calendar %s: unparseable end datetime %q for %q: %v", calendarID, item.End.DateTime, item.Summary, err)
			}
		}

		for _, attendee := range item.Attendees {
			if attendee.Self && attendee.ResponseStatus == "declined" {
				ev.Declined = true
				break
			}
		}

		result = append(result, ev)
	}

	return result, nil
}

// parseDateTime tries multiple datetime formats that Google Calendar may return.
// The API nominally uses RFC3339 but Exchange-synced calendars, external iCal
// subscriptions, and recurring-event expansions sometimes produce variants
// (e.g., missing timezone offset, fractional seconds with offset).
func parseDateTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",       // no timezone (treat as UTC)
		"2006-01-02T15:04:05-07:00", // explicit, same as RFC3339 but for clarity
		"2006-01-02T15:04:05Z07:00", // Go's RFC3339 constant (redundant, but safe)
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("no known format matched %q", s)
}

// autoAccept accepts pending events from organizers matching the given domains.
func autoAccept(ctx context.Context, ts oauth2.TokenSource, domains []string) {
	srv, err := gcal.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrowEnd := todayStart.Add(48 * time.Hour)

	events, err := srv.Events.List("primary").
		TimeMin(todayStart.Format(time.RFC3339)).
		TimeMax(tomorrowEnd.Format(time.RFC3339)).
		SingleEvents(true).
		Context(ctx).
		Do()
	if err != nil {
		return
	}

	for _, item := range events.Items {
		needsAction := false
		for _, attendee := range item.Attendees {
			if attendee.Self && attendee.ResponseStatus == "needsAction" {
				needsAction = true
				break
			}
		}
		if !needsAction {
			continue
		}

		if item.Organizer != nil && matchesDomain(item.Organizer.Email, domains) {
			for i, attendee := range item.Attendees {
				if attendee.Self {
					item.Attendees[i].ResponseStatus = "accepted"
					break
				}
			}
			_, err := srv.Events.Patch("primary", item.Id, &gcal.Event{
				Attendees: item.Attendees,
			}).SendUpdates("none").Context(ctx).Do()
			if err != nil {
				log.Printf("auto-accept failed for %q: %v", item.Summary, err)
			}
		}
	}
}

// CalendarInfo describes a calendar available in the user's Google account.
type CalendarInfo struct {
	ID      string
	Summary string
	Primary bool
}

// ListAvailableCalendars returns all calendars visible to the authenticated user.
func ListAvailableCalendars() ([]CalendarInfo, error) {
	ts, err := LoadAuth()
	if err != nil {
		return nil, fmt.Errorf("calendar auth: %w", err)
	}

	srv, err := gcal.NewService(context.Background(), option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}

	list, err := srv.CalendarList.List().Do()
	if err != nil {
		return nil, fmt.Errorf("list calendars: %w", err)
	}

	var calendars []CalendarInfo
	for _, entry := range list.Items {
		calendars = append(calendars, CalendarInfo{
			ID:      entry.Id,
			Summary: entry.Summary,
			Primary: entry.Primary,
		})
	}
	return calendars, nil
}

func matchesDomain(email string, domains []string) bool {
	for _, domain := range domains {
		if strings.HasSuffix(email, "@"+domain) {
			return true
		}
	}
	return false
}
