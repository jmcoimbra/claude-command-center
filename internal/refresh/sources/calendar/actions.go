package calendar

import (
	"context"
	"fmt"
	"time"

	gcal "google.golang.org/api/calendar/v3"
)

// FindFreeSlot searches the user's primary calendar for the next available time
// slot of the given duration (in minutes). It looks from now until 6pm today,
// or from 9am-6pm tomorrow if today is full.
func FindFreeSlot(ctx context.Context, srv *gcal.Service, durationMinutes int) (time.Time, error) {
	now := time.Now()
	start := now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location())

	if start.After(endOfDay) {
		tomorrow := now.AddDate(0, 0, 1)
		start = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 9, 0, 0, 0, tomorrow.Location())
		endOfDay = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 18, 0, 0, 0, tomorrow.Location())
	}

	events, err := srv.Events.List("primary").
		TimeMin(start.Format(time.RFC3339)).
		TimeMax(endOfDay.Format(time.RFC3339)).
		SingleEvents(true).
		OrderBy("startTime").
		Context(ctx).
		Do()
	if err != nil {
		return time.Time{}, err
	}

	duration := time.Duration(durationMinutes) * time.Minute
	candidate := start

	for _, item := range events.Items {
		if item.Start.DateTime == "" {
			continue
		}
		eventStart, err := time.Parse(time.RFC3339, item.Start.DateTime)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse event start %q: %w", item.Start.DateTime, err)
		}

		if candidate.Add(duration).Before(eventStart) || candidate.Add(duration).Equal(eventStart) {
			return candidate, nil
		}

		eventEnd, err := time.Parse(time.RFC3339, item.End.DateTime)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse event end %q: %w", item.End.DateTime, err)
		}
		if eventEnd.After(candidate) {
			candidate = eventEnd
		}
	}

	if candidate.Add(duration).Before(endOfDay) || candidate.Add(duration).Equal(endOfDay) {
		return candidate, nil
	}

	return time.Time{}, fmt.Errorf("no free slot of %d minutes found today", durationMinutes)
}
