package calendar

import (
	"context"
	"fmt"
	"time"

	gcal "google.golang.org/api/calendar/v3"
)

// Working hours: Monday–Friday, 9am–6pm local time.
const (
	workingHourStart = 9
	workingHourEnd   = 18
)

// nextWorkingWindow returns the earliest working-hours window [start, end] that
// can fit the given duration, starting from now. Start is rounded up to the
// next 15-minute boundary and clamped into the Mon–Fri, 9am–6pm window. If
// today's remaining time can't fit duration, it rolls forward to the next
// weekday's 9am. Returns zero values if no window is found within a bounded
// lookahead (should not happen in practice).
func nextWorkingWindow(now time.Time, duration time.Duration) (time.Time, time.Time) {
	candidate := now.Truncate(15 * time.Minute).Add(15 * time.Minute)
	for i := 0; i < 14; i++ {
		loc := candidate.Location()
		dayStart := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), workingHourStart, 0, 0, 0, loc)
		dayEnd := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), workingHourEnd, 0, 0, 0, loc)
		wd := candidate.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			if candidate.Before(dayStart) {
				candidate = dayStart
			}
			if !candidate.Add(duration).After(dayEnd) {
				return candidate, dayEnd
			}
		}
		next := candidate.AddDate(0, 0, 1)
		candidate = time.Date(next.Year(), next.Month(), next.Day(), workingHourStart, 0, 0, 0, next.Location())
	}
	return time.Time{}, time.Time{}
}

// FindFreeSlot searches the user's primary calendar for the next available time
// slot of the given duration (in minutes) inside working hours: Monday–Friday,
// 9am–6pm local time.
func FindFreeSlot(ctx context.Context, srv *gcal.Service, durationMinutes int) (time.Time, error) {
	duration := time.Duration(durationMinutes) * time.Minute
	start, endOfDay := nextWorkingWindow(time.Now(), duration)
	if start.IsZero() {
		return time.Time{}, fmt.Errorf("no working-hours window available for %d minutes", durationMinutes)
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
