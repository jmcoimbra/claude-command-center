package commandcenter

import (
	"context"
	"fmt"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	calendar "github.com/anutron/claude-command-center/internal/refresh/sources/calendar"
	tea "github.com/charmbracelet/bubbletea"
	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// ---------------------------------------------------------------------------
// Message types for calendar booking tea.Cmds
// ---------------------------------------------------------------------------

// bookingCompleteMsg is returned when a calendar time block has been booked.
type bookingCompleteMsg struct {
	todoID    string
	startTime time.Time
	endTime   time.Time
	duration  int
}

// bookingErrorMsg is returned when a calendar booking fails.
type bookingErrorMsg struct {
	err error
}

// releaseCompleteMsg is returned when future bookings have been released.
type releaseCompleteMsg struct {
	todoID  string
	count   int
}

// releaseErrorMsg is returned when releasing bookings fails.
type releaseErrorMsg struct {
	err error
}

// ---------------------------------------------------------------------------
// Calendar commands
// ---------------------------------------------------------------------------

// scheduleBlockCmd creates a background tea.Cmd that finds a free slot on the
// user's Google Calendar and inserts a time-block event. On success it writes
// a booking record to cc_todo_bookings and returns a bookingCompleteMsg.
func scheduleBlockCmd(p *Plugin, todoID, title string, durationMinutes int) tea.Cmd {
	database := p.database
	return func() tea.Msg {
		ctx := context.Background()

		ts, err := calendar.LoadAuth()
		if err != nil {
			return bookingErrorMsg{err: fmt.Errorf("calendar auth: %w", err)}
		}

		srv, err := gcal.NewService(ctx, option.WithTokenSource(ts))
		if err != nil {
			return bookingErrorMsg{err: fmt.Errorf("calendar service: %w", err)}
		}

		slot, err := calendar.FindFreeSlot(ctx, srv, durationMinutes)
		if err != nil {
			return bookingErrorMsg{err: fmt.Errorf("find free slot: %w", err)}
		}

		endTime := slot.Add(time.Duration(durationMinutes) * time.Minute)
		event := &gcal.Event{
			Summary: title,
			Start: &gcal.EventDateTime{
				DateTime: slot.Format(time.RFC3339),
			},
			End: &gcal.EventDateTime{
				DateTime: endTime.Format(time.RFC3339),
			},
		}

		created, err := srv.Events.Insert("primary", event).Context(ctx).Do()
		if err != nil {
			return bookingErrorMsg{err: fmt.Errorf("create event: %w", err)}
		}

		// Persist the booking record to the database.
		if database != nil {
			booking := db.TodoBooking{
				TodoID:      todoID,
				EventID:     created.Id,
				StartTime:   slot,
				EndTime:     endTime,
				DurationMin: durationMinutes,
				CreatedAt:   time.Now(),
			}
			if err := db.DBInsertBooking(database, booking); err != nil {
				// Event was created but booking record failed — log but still report success.
				// The calendar event exists regardless.
				return bookingCompleteMsg{
					todoID:    todoID,
					startTime: slot,
					endTime:   endTime,
					duration:  durationMinutes,
				}
			}
		}

		return bookingCompleteMsg{
			todoID:    todoID,
			startTime: slot,
			endTime:   endTime,
			duration:  durationMinutes,
		}
	}
}

// releaseBookingsCmd creates a background tea.Cmd that deletes all future
// Google Calendar events associated with a todo and removes the corresponding
// booking records from cc_todo_bookings.
func releaseBookingsCmd(p *Plugin, todoID string) tea.Cmd {
	database := p.database
	return func() tea.Msg {
		if database == nil {
			return releaseErrorMsg{err: fmt.Errorf("no database")}
		}

		ctx := context.Background()

		bookings, err := db.DBGetFutureBookingsForTodo(database, todoID)
		if err != nil {
			return releaseErrorMsg{err: fmt.Errorf("load bookings: %w", err)}
		}

		if len(bookings) == 0 {
			return releaseCompleteMsg{todoID: todoID, count: 0}
		}

		ts, err := calendar.LoadAuth()
		if err != nil {
			return releaseErrorMsg{err: fmt.Errorf("calendar auth: %w", err)}
		}

		srv, err := gcal.NewService(ctx, option.WithTokenSource(ts))
		if err != nil {
			return releaseErrorMsg{err: fmt.Errorf("calendar service: %w", err)}
		}

		deleted := 0
		for _, b := range bookings {
			if err := srv.Events.Delete("primary", b.EventID).Context(ctx).Do(); err != nil {
				// Log but continue — the event may have been manually deleted.
				continue
			}
			deleted++
		}

		if err := db.DBDeleteFutureBookings(database, todoID); err != nil {
			return releaseErrorMsg{err: fmt.Errorf("delete booking records: %w", err)}
		}

		return releaseCompleteMsg{todoID: todoID, count: deleted}
	}
}
