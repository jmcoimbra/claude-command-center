package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gcal "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

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
