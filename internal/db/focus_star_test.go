package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Focus & Star DB Tests
// ---------------------------------------------------------------------------

func openFocusStarTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertTestTodo(t *testing.T, db *sql.DB, id, title string) {
	t.Helper()
	todo := Todo{
		ID:        id,
		Title:     title,
		Status:    StatusBacklog,
		Source:    "manual",
		CreatedAt: time.Now(),
	}
	if err := DBInsertTodo(db, todo); err != nil {
		t.Fatalf("insert todo %s: %v", id, err)
	}
}

// loadTodoByID is a helper that loads a single todo by ID.
func loadTodoByID(t *testing.T, db *sql.DB, id string) Todo {
	t.Helper()
	cc, err := LoadCommandCenterFromDB(db)
	if err != nil {
		t.Fatalf("load CC: %v", err)
	}
	for _, todo := range cc.Todos {
		if todo.ID == id {
			return todo
		}
	}
	t.Fatalf("todo %s not found", id)
	return Todo{}
}

// TestStarTodo: starring a todo should set starred=true AND focus=true.
func TestStarTodo(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	if err := DBSetTodoStar(db, "todo1", true); err != nil {
		t.Fatalf("DBSetTodoStar: %v", err)
	}

	todo := loadTodoByID(t, db, "todo1")
	if !todo.Starred {
		t.Error("expected Starred=true after starring")
	}
	if !todo.Focus {
		t.Error("expected Focus=true after starring (starring implies focus)")
	}
}

// TestUnstarTodo: unstarring should set starred=false; focus should remain true.
func TestUnstarTodo(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	// Star first
	if err := DBSetTodoStar(db, "todo1", true); err != nil {
		t.Fatalf("DBSetTodoStar true: %v", err)
	}
	// Then unstar
	if err := DBSetTodoStar(db, "todo1", false); err != nil {
		t.Fatalf("DBSetTodoStar false: %v", err)
	}

	todo := loadTodoByID(t, db, "todo1")
	if todo.Starred {
		t.Error("expected Starred=false after unstarring")
	}
	// Unstarring should NOT remove focus
	if !todo.Focus {
		t.Error("expected Focus=true to remain after unstarring (unstar does not remove focus)")
	}
}

// TestToggleFocus: toggling focus on/off should persist correctly.
func TestToggleFocus(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	// Enable focus
	if err := DBSetTodoFocus(db, "todo1", true); err != nil {
		t.Fatalf("DBSetTodoFocus true: %v", err)
	}
	todo := loadTodoByID(t, db, "todo1")
	if !todo.Focus {
		t.Error("expected Focus=true after enabling")
	}

	// Disable focus
	if err := DBSetTodoFocus(db, "todo1", false); err != nil {
		t.Fatalf("DBSetTodoFocus false: %v", err)
	}
	todo = loadTodoByID(t, db, "todo1")
	if todo.Focus {
		t.Error("expected Focus=false after disabling")
	}
}

// TestUnfocusStarredTodo: unfocusing a starred todo should clear BOTH starred and focus.
func TestUnfocusStarredTodo(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	// Star it (sets starred=true, focus=true)
	if err := DBSetTodoStar(db, "todo1", true); err != nil {
		t.Fatalf("DBSetTodoStar: %v", err)
	}

	// Unfocus it — because it's starred, both should be cleared
	if err := DBSetTodoFocus(db, "todo1", false); err != nil {
		t.Fatalf("DBSetTodoFocus false: %v", err)
	}

	todo := loadTodoByID(t, db, "todo1")
	if todo.Focus {
		t.Error("expected Focus=false after unfocusing a starred todo")
	}
	if todo.Starred {
		t.Error("expected Starred=false after unfocusing a starred todo (unfocus clears star)")
	}
}

// TestCompleteDismissClears: completing or dismissing a starred+focused todo should
// clear both starred and focus.
func TestCompleteDismissClears(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "Complete me")
	insertTestTodo(t, db, "todo2", "Dismiss me")

	// Star both (sets starred+focus)
	if err := DBSetTodoStar(db, "todo1", true); err != nil {
		t.Fatalf("star todo1: %v", err)
	}
	if err := DBSetTodoStar(db, "todo2", true); err != nil {
		t.Fatalf("star todo2: %v", err)
	}

	// Complete todo1
	if err := DBCompleteTodo(db, "todo1"); err != nil {
		t.Fatalf("complete todo1: %v", err)
	}
	todo := loadTodoByID(t, db, "todo1")
	if todo.Starred {
		t.Error("todo1: expected Starred=false after completing")
	}
	if todo.Focus {
		t.Error("todo1: expected Focus=false after completing")
	}

	// Dismiss todo2
	if err := DBDismissTodo(db, "todo2"); err != nil {
		t.Fatalf("dismiss todo2: %v", err)
	}
	todo = loadTodoByID(t, db, "todo2")
	if todo.Starred {
		t.Error("todo2: expected Starred=false after dismissing")
	}
	if todo.Focus {
		t.Error("todo2: expected Focus=false after dismissing")
	}
}

// ---------------------------------------------------------------------------
// Booking CRUD Tests
// ---------------------------------------------------------------------------

// TestInsertBooking: inserting a booking should persist it and allow retrieval.
func TestInsertBooking(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	booking := TodoBooking{
		TodoID:          "todo1",
		CalendarEventID: "gcal-event-abc123",
		StartTime:       time.Now().Add(2 * time.Hour),
		DurationMinutes: 60,
	}

	if err := DBInsertBooking(db, booking); err != nil {
		t.Fatalf("DBInsertBooking: %v", err)
	}

	bookings, err := DBGetBookingsForTodo(db, "todo1")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo: %v", err)
	}
	if len(bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(bookings))
	}
	if bookings[0].CalendarEventID != "gcal-event-abc123" {
		t.Errorf("expected CalendarEventID=gcal-event-abc123, got %q", bookings[0].CalendarEventID)
	}
	if bookings[0].DurationMinutes != 60 {
		t.Errorf("expected DurationMinutes=60, got %d", bookings[0].DurationMinutes)
	}
}

// TestGetBookings: bookings for a todo are returned; bookings for other todos are not.
func TestGetBookings(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "Task one")
	insertTestTodo(t, db, "todo2", "Task two")

	b1 := TodoBooking{
		TodoID:          "todo1",
		CalendarEventID: "event-1",
		StartTime:       time.Now().Add(1 * time.Hour),
		DurationMinutes: 30,
	}
	b2 := TodoBooking{
		TodoID:          "todo1",
		CalendarEventID: "event-2",
		StartTime:       time.Now().Add(3 * time.Hour),
		DurationMinutes: 60,
	}
	b3 := TodoBooking{
		TodoID:          "todo2",
		CalendarEventID: "event-3",
		StartTime:       time.Now().Add(2 * time.Hour),
		DurationMinutes: 45,
	}

	for _, b := range []TodoBooking{b1, b2, b3} {
		if err := DBInsertBooking(db, b); err != nil {
			t.Fatalf("DBInsertBooking: %v", err)
		}
	}

	bookings1, err := DBGetBookingsForTodo(db, "todo1")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo todo1: %v", err)
	}
	if len(bookings1) != 2 {
		t.Fatalf("expected 2 bookings for todo1, got %d", len(bookings1))
	}

	bookings2, err := DBGetBookingsForTodo(db, "todo2")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo todo2: %v", err)
	}
	if len(bookings2) != 1 {
		t.Fatalf("expected 1 booking for todo2, got %d", len(bookings2))
	}
}

// TestDeleteFutureBookings: deleting future bookings removes only future ones,
// leaving past bookings intact.
func TestDeleteFutureBookings(t *testing.T) {
	db := openFocusStarTestDB(t)
	insertTestTodo(t, db, "todo1", "My task")

	past := TodoBooking{
		TodoID:          "todo1",
		CalendarEventID: "event-past",
		StartTime:       time.Now().Add(-2 * time.Hour), // in the past
		DurationMinutes: 60,
	}
	future := TodoBooking{
		TodoID:          "todo1",
		CalendarEventID: "event-future",
		StartTime:       time.Now().Add(4 * time.Hour), // in the future
		DurationMinutes: 30,
	}

	if err := DBInsertBooking(db, past); err != nil {
		t.Fatalf("insert past booking: %v", err)
	}
	if err := DBInsertBooking(db, future); err != nil {
		t.Fatalf("insert future booking: %v", err)
	}

	if err := DBDeleteFutureBookings(db, "todo1"); err != nil {
		t.Fatalf("DBDeleteFutureBookings: %v", err)
	}

	bookings, err := DBGetBookingsForTodo(db, "todo1")
	if err != nil {
		t.Fatalf("DBGetBookingsForTodo: %v", err)
	}
	if len(bookings) != 1 {
		t.Fatalf("expected 1 booking after deleting future (only past remains), got %d", len(bookings))
	}
	if bookings[0].CalendarEventID != "event-past" {
		t.Errorf("expected remaining booking to be event-past, got %q", bookings[0].CalendarEventID)
	}
}
