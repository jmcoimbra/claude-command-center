package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

func TestNextWorkingWindow(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	// Known dates:
	// 2026-04-13 Monday, 2026-04-17 Friday, 2026-04-18 Saturday, 2026-04-19 Sunday, 2026-04-20 Monday
	cases := []struct {
		name     string
		now      time.Time
		duration time.Duration
		wantStart time.Time
	}{
		{
			name:      "weekday early morning clamps to 9am",
			now:       time.Date(2026, 4, 13, 2, 15, 0, 0, loc),
			duration:  2 * time.Hour,
			wantStart: time.Date(2026, 4, 13, 9, 0, 0, 0, loc),
		},
		{
			name:      "weekday mid-day rounds up to next 15",
			now:       time.Date(2026, 4, 13, 10, 3, 0, 0, loc),
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 13, 10, 15, 0, 0, loc),
		},
		{
			name:      "weekday after 6pm rolls to tomorrow 9am",
			now:       time.Date(2026, 4, 13, 19, 0, 0, 0, loc),
			duration:  30 * time.Minute,
			wantStart: time.Date(2026, 4, 14, 9, 0, 0, 0, loc),
		},
		{
			name:      "saturday rolls to monday 9am",
			now:       time.Date(2026, 4, 18, 10, 0, 0, 0, loc),
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 20, 9, 0, 0, 0, loc),
		},
		{
			name:      "sunday rolls to monday 9am",
			now:       time.Date(2026, 4, 19, 10, 0, 0, 0, loc),
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 20, 9, 0, 0, 0, loc),
		},
		{
			name:      "friday night rolls to monday 9am",
			now:       time.Date(2026, 4, 17, 23, 0, 0, 0, loc),
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 20, 9, 0, 0, 0, loc),
		},
		{
			name:      "duration doesn't fit remaining window rolls to next day",
			now:       time.Date(2026, 4, 13, 17, 30, 0, 0, loc),
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 14, 9, 0, 0, 0, loc),
		},
		{
			name:      "duration exactly fits until 6pm",
			now:       time.Date(2026, 4, 13, 16, 46, 0, 0, loc), // rounds up to 17:00
			duration:  time.Hour,
			wantStart: time.Date(2026, 4, 13, 17, 0, 0, 0, loc),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd := nextWorkingWindow(tc.now, tc.duration)
			if !gotStart.Equal(tc.wantStart) {
				t.Errorf("start: got %v, want %v", gotStart, tc.wantStart)
			}
			wantEnd := time.Date(tc.wantStart.Year(), tc.wantStart.Month(), tc.wantStart.Day(), 18, 0, 0, 0, tc.wantStart.Location())
			if !gotEnd.Equal(wantEnd) {
				t.Errorf("end: got %v, want %v", gotEnd, wantEnd)
			}
			if gotStart.Weekday() == time.Saturday || gotStart.Weekday() == time.Sunday {
				t.Errorf("start landed on weekend: %v (%s)", gotStart, gotStart.Weekday())
			}
		})
	}
}

func TestFindFreeSlot_MalformedTimes(t *testing.T) {
	// Mock Google Calendar API server that returns events with unparseable times
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := &gcal.Events{
			Items: []*gcal.Event{
				{
					Summary: "Malformed Event",
					Start: &gcal.EventDateTime{
						DateTime: "not-a-valid-time",
					},
					End: &gcal.EventDateTime{
						DateTime: "also-not-valid",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	srv, err := gcal.NewService(context.Background(),
		option.WithHTTPClient(server.Client()),
		option.WithEndpoint(server.URL),
	)
	if err != nil {
		t.Fatalf("create calendar service: %v", err)
	}

	// FindFreeSlot should return an error when it encounters events with
	// unparseable times, rather than silently treating them as year 0001.
	//
	// BUG: Lines 100 and 106 use `eventStart, _ := time.Parse(...)` which
	// discards the error. The malformed event silently gets zero-time boundaries,
	// making it invisible to the slot-finding algorithm.
	_, err = FindFreeSlot(context.Background(), srv, 30)
	if err == nil {
		t.Error("FindFreeSlot should return error for events with unparseable times, but silently succeeded")
	}
}
