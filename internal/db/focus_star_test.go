package db

import (
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Focus & Star DB tests
// ---------------------------------------------------------------------------

func TestStarTodo(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	todo := Todo{ID: "star1", Title: "Star me", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	if err := DBInsertTodo(db, todo); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Star the todo — should set both starred=true and focus=true.
	if err := DBSetTodoStar(db, "star1", true); err != nil {
		t.Fatalf("DBSetTodoStar: %v", err)
	}

	loaded, err := DBLoadTodoByID(db, "star1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Starred {
		t.Error("expected starred=true after starring")
	}
	if !loaded.Focus {
		t.Error("expected focus=true after starring (starring implies focus)")
	}
}

func TestUnstarTodo(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	todo := Todo{ID: "unstar1", Title: "Unstar me", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	DBInsertTodo(db, todo)
	DBSetTodoStar(db, "unstar1", true) // star first

	// Now unstar — should clear starred but NOT change focus.
	if err := DBSetTodoStar(db, "unstar1", false); err != nil {
		t.Fatalf("DBSetTodoStar(false): %v", err)
	}

	loaded, err := DBLoadTodoByID(db, "unstar1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Starred {
		t.Error("expected starred=false after unstarring")
	}
	// Focus should remain unchanged (still true from the starring step).
	if !loaded.Focus {
		t.Error("expected focus=true after unstarring (unstar does not change focus)")
	}
}

func TestToggleFocus(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	todo := Todo{ID: "focus1", Title: "Focus me", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	DBInsertTodo(db, todo)

	// Toggle on.
	if err := DBSetTodoFocus(db, "focus1", true); err != nil {
		t.Fatalf("DBSetTodoFocus(true): %v", err)
	}
	loaded, _ := DBLoadTodoByID(db, "focus1")
	if !loaded.Focus {
		t.Error("expected focus=true after focusing")
	}
	if loaded.Starred {
		t.Error("expected starred=false — focusing alone should not star")
	}

	// Toggle off.
	if err := DBSetTodoFocus(db, "focus1", false); err != nil {
		t.Fatalf("DBSetTodoFocus(false): %v", err)
	}
	loaded, _ = DBLoadTodoByID(db, "focus1")
	if loaded.Focus {
		t.Error("expected focus=false after unfocusing")
	}
}

func TestUnfocusStarredTodo(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	todo := Todo{ID: "unfocus1", Title: "Unfocus starred", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	DBInsertTodo(db, todo)
	DBSetTodoStar(db, "unfocus1", true) // star implies focus

	// Unfocusing a starred item must clear BOTH focus and starred.
	if err := DBSetTodoFocus(db, "unfocus1", false); err != nil {
		t.Fatalf("DBSetTodoFocus(false): %v", err)
	}

	loaded, err := DBLoadTodoByID(db, "unfocus1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Focus {
		t.Error("expected focus=false after unfocusing starred item")
	}
	if loaded.Starred {
		t.Error("expected starred=false after unfocusing starred item (can't be starred without focus)")
	}
}

func TestCompleteDismissClears(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// Insert two todos: one to complete, one to dismiss.
	for _, id := range []string{"comp1", "dism1"} {
		DBInsertTodo(db, Todo{ID: id, Title: id, Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})
		DBSetTodoStar(db, id, true)
	}

	// Verify both are starred+focused before terminal transition.
	for _, id := range []string{"comp1", "dism1"} {
		t, _ := DBLoadTodoByID(db, id)
		if !t.Starred || !t.Focus {
			panic("precondition failed: todo should be starred+focused")
		}
	}

	if err := DBCompleteTodo(db, "comp1"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := DBDismissTodo(db, "dism1"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	comp, _ := DBLoadTodoByID(db, "comp1")
	if comp.Starred {
		t.Error("completed todo: expected starred=false")
	}
	if comp.Focus {
		t.Error("completed todo: expected focus=false")
	}

	dism, _ := DBLoadTodoByID(db, "dism1")
	if dism.Starred {
		t.Error("dismissed todo: expected starred=false")
	}
	if dism.Focus {
		t.Error("dismissed todo: expected focus=false")
	}
}

func TestInsertBooking(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	DBInsertTodo(db, Todo{ID: "book1", Title: "Booking test", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})

	now := time.Now().UTC().Truncate(time.Second)
	booking := TodoBooking{
		TodoID:      "book1",
		EventID:     "gcal-event-abc123",
		StartTime:   now.Add(1 * time.Hour),
		EndTime:     now.Add(2 * time.Hour),
		DurationMin: 60,
		CreatedAt:   now,
	}

	if err := DBInsertBooking(db, booking); err != nil {
		t.Fatalf("DBInsertBooking: %v", err)
	}

	bookings, err := DBGetBookingsForTodo(db, "book1")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo: %v", err)
	}
	if len(bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(bookings))
	}
	if bookings[0].TodoID != "book1" {
		t.Errorf("expected todo_id=book1, got %s", bookings[0].TodoID)
	}
	if bookings[0].EventID != "gcal-event-abc123" {
		t.Errorf("expected event_id=gcal-event-abc123, got %s", bookings[0].EventID)
	}
	if bookings[0].DurationMin != 60 {
		t.Errorf("expected duration_min=60, got %d", bookings[0].DurationMin)
	}
}

func TestGetBookings(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	DBInsertTodo(db, Todo{ID: "book2", Title: "Booking test 2", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})

	now := time.Now().UTC().Truncate(time.Second)
	// Insert a past booking and a future booking.
	pastBooking := TodoBooking{
		TodoID:      "book2",
		EventID:     "gcal-past",
		StartTime:   now.Add(-2 * time.Hour),
		EndTime:     now.Add(-1 * time.Hour),
		DurationMin: 60,
		CreatedAt:   now,
	}
	futureBooking := TodoBooking{
		TodoID:      "book2",
		EventID:     "gcal-future",
		StartTime:   now.Add(1 * time.Hour),
		EndTime:     now.Add(2 * time.Hour),
		DurationMin: 60,
		CreatedAt:   now,
	}
	DBInsertBooking(db, pastBooking)
	DBInsertBooking(db, futureBooking)

	// DBGetBookingsForTodo should return both.
	all, err := DBGetBookingsForTodo(db, "book2")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 bookings, got %d", len(all))
	}

	// DBGetFutureBookingsForTodo should return only the future one.
	future, err := DBGetFutureBookingsForTodo(db, "book2")
	if err != nil {
		t.Fatalf("DBGetFutureBookingsForTodo: %v", err)
	}
	if len(future) != 1 {
		t.Fatalf("expected 1 future booking, got %d", len(future))
	}
	if future[0].EventID != "gcal-future" {
		t.Errorf("expected gcal-future, got %s", future[0].EventID)
	}
}

func TestDeleteFutureBookings(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	DBInsertTodo(db, Todo{ID: "book3", Title: "Booking test 3", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})

	now := time.Now().UTC().Truncate(time.Second)
	DBInsertBooking(db, TodoBooking{
		TodoID: "book3", EventID: "gcal-past2",
		StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-1 * time.Hour),
		DurationMin: 60, CreatedAt: now,
	})
	DBInsertBooking(db, TodoBooking{
		TodoID: "book3", EventID: "gcal-future2",
		StartTime: now.Add(1 * time.Hour), EndTime: now.Add(2 * time.Hour),
		DurationMin: 60, CreatedAt: now,
	})

	// Delete only future bookings.
	if err := DBDeleteFutureBookings(db, "book3"); err != nil {
		t.Fatalf("DBDeleteFutureBookings: %v", err)
	}

	remaining, err := DBGetBookingsForTodo(db, "book3")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo after delete: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining booking (past), got %d", len(remaining))
	}
	if remaining[0].EventID != "gcal-past2" {
		t.Errorf("expected gcal-past2 to remain, got %s", remaining[0].EventID)
	}
}
