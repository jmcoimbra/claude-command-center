package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestRestartPreservesStarred verifies that starring a todo, closing the DB,
// and reopening it (simulating a CCC restart) preserves the starred and focus
// flags. This was broken by BUG-150: the schema migration re-added removed
// columns (session_status, triage_status), triggering migrateTodoStatusFSM
// on every startup, which recreated the table without focus/starred columns.
func TestRestartPreservesStarred(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Step 1: Open DB, insert a todo, star it
	database, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	todo := Todo{ID: "restart1", Title: "Restart test", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	if err := DBInsertTodo(database, todo); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := DBSetTodoStar(database, "restart1", true); err != nil {
		t.Fatalf("star: %v", err)
	}

	// Verify starred is set before closing
	loaded, err := DBLoadTodoByID(database, "restart1")
	if err != nil {
		t.Fatalf("load before close: %v", err)
	}
	if !loaded.Starred {
		t.Fatal("expected starred=true before closing DB")
	}
	if !loaded.Focus {
		t.Fatal("expected focus=true before closing DB")
	}

	// Step 2: Close DB (simulate CCC exit)
	database.Close()

	// Step 3: Reopen DB (simulate CCC restart — runs migrateSchema again)
	database2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen DB: %v", err)
	}
	defer database2.Close()

	// Step 4: Load via LoadCommandCenterFromDB (same path as CCC startup)
	cc, err := LoadCommandCenterFromDB(database2)
	if err != nil {
		t.Fatalf("load after restart: %v", err)
	}

	// Step 5: Verify starred and focus survived the restart
	var found *Todo
	for i := range cc.Todos {
		if cc.Todos[i].ID == "restart1" {
			found = &cc.Todos[i]
		}
	}
	if found == nil {
		t.Fatal("todo not found after restart")
	}
	if !found.Starred {
		t.Error("starred=false after restart — stars lost!")
	}
	if !found.Focus {
		t.Error("focus=false after restart — focus lost!")
	}
}

// TestRestartPreservesFocusOnly verifies that the focus flag (without star)
// also survives a restart.
func TestRestartPreservesFocusOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	todo := Todo{ID: "focus1", Title: "Focus test", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()}
	if err := DBInsertTodo(database, todo); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := DBSetTodoFocus(database, "focus1", true); err != nil {
		t.Fatalf("focus: %v", err)
	}

	database.Close()

	database2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database2.Close()

	loaded, err := DBLoadTodoByID(database2, "focus1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Focus {
		t.Error("focus=false after restart")
	}
	if loaded.Starred {
		t.Error("starred should remain false (only focus was set)")
	}
}

// TestMigrationDoesNotReAddRemovedColumns verifies that the schema migration
// does not re-add triage_status or session_status columns that were removed
// by migrateTodoStatusFSM. Re-adding them would cause the FSM migration to
// run again on every startup, destroying focus/starred data.
func TestMigrationDoesNotReAddRemovedColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open: full migration runs
	database, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if columnExists(database, "cc_todos", "triage_status") {
		t.Error("triage_status should not exist after first migration")
	}
	if columnExists(database, "cc_todos", "session_status") {
		t.Error("session_status should not exist after first migration")
	}
	database.Close()

	// Second open: migration runs again (must be idempotent)
	database2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer database2.Close()

	if columnExists(database2, "cc_todos", "triage_status") {
		t.Error("triage_status should not exist after second migration — migration is not idempotent!")
	}
	if columnExists(database2, "cc_todos", "session_status") {
		t.Error("session_status should not exist after second migration — migration is not idempotent!")
	}
}

// TestMultipleRestartsPreserveStars verifies stars survive across many
// restart cycles, not just one.
func TestMultipleRestartsPreserveStars(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Create and star
	database, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	DBInsertTodo(database, Todo{ID: "multi1", Title: "Multi restart", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})
	DBSetTodoStar(database, "multi1", true)
	database.Close()

	// Restart 5 times
	for i := 0; i < 5; i++ {
		db, err := OpenDB(dbPath)
		if err != nil {
			t.Fatalf("restart %d: %v", i+1, err)
		}
		loaded, err := DBLoadTodoByID(db, "multi1")
		if err != nil {
			t.Fatalf("restart %d load: %v", i+1, err)
		}
		if !loaded.Starred {
			t.Fatalf("restart %d: starred=false", i+1)
		}
		if !loaded.Focus {
			t.Fatalf("restart %d: focus=false", i+1)
		}
		db.Close()
	}
}

// TestRestartPreservesAllTodoData verifies that other todo fields (not just
// focus/starred) also survive the migration on restart.
func TestRestartPreservesAllTodoData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	database, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	cc := &CommandCenter{
		GeneratedAt: now,
		Todos: []Todo{
			{
				ID: "data1", Title: "Data test", Status: StatusBacklog,
				Source: "github", SourceRef: "gh-123",
				Context: "some context", Detail: "some detail",
				WhoWaiting: "alice", ProjectDir: "/tmp/proj",
				Due: "2026-04-15", Effort: "2h",
				CreatedAt: now,
			},
		},
	}
	if err := DBSaveRefreshResult(database, cc); err != nil {
		t.Fatalf("save: %v", err)
	}
	DBSetTodoStar(database, "data1", true)
	database.Close()

	// Reopen and verify all fields
	database2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer database2.Close()

	loaded, _ := DBLoadTodoByID(database2, "data1")
	if loaded == nil {
		t.Fatal("todo not found after restart")
	}
	if loaded.Title != "Data test" {
		t.Errorf("title: got %q", loaded.Title)
	}
	if loaded.Status != StatusBacklog {
		t.Errorf("status: got %q", loaded.Status)
	}
	if loaded.SourceRef != "gh-123" {
		t.Errorf("source_ref: got %q", loaded.SourceRef)
	}
	if !loaded.Starred {
		t.Error("starred lost on restart")
	}
	if !loaded.Focus {
		t.Error("focus lost on restart")
	}
}

// TestSchemaIdempotency is a direct check that opening the DB twice doesn't
// corrupt the schema or data — used to pin down the root cause of BUG-150.
func TestSchemaIdempotency(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open, write, close
	db1, _ := OpenDB(dbPath)
	DBInsertTodo(db1, Todo{ID: "idem1", Title: "Idem", Status: StatusBacklog, Source: "manual", CreatedAt: time.Now()})
	DBSetTodoStar(db1, "idem1", true)
	db1.Close()

	// Open again WITHOUT migrateSchema — data should be intact
	rawDB, _ := sql.Open("sqlite", dbPath)
	rawDB.Exec("PRAGMA journal_mode=WAL")
	rawDB.Exec("PRAGMA busy_timeout=5000")
	rawDB.SetMaxOpenConns(1)
	var f, s int
	rawDB.QueryRow("SELECT COALESCE(focus, 0), COALESCE(starred, 0) FROM cc_todos WHERE id = 'idem1'").Scan(&f, &s)
	if f != 1 || s != 1 {
		t.Fatalf("raw read after close: focus=%d starred=%d (expected 1,1)", f, s)
	}
	rawDB.Close()

	// Open with OpenDB (runs migrateSchema) — data should still be intact
	db2, _ := OpenDB(dbPath)
	defer db2.Close()
	db2.QueryRow("SELECT COALESCE(focus, 0), COALESCE(starred, 0) FROM cc_todos WHERE id = 'idem1'").Scan(&f, &s)
	if f != 1 || s != 1 {
		t.Errorf("after migrateSchema: focus=%d starred=%d (expected 1,1) — migration destroyed data!", f, s)
	}
}
