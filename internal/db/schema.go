package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// OpenDB opens (or creates) the SQLite database at dbPath, enables WAL mode,
// and runs the idempotent schema migration.
func OpenDB(dbPath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrateSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS cc_todos (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			source TEXT NOT NULL DEFAULT 'manual',
			source_ref TEXT,
			context TEXT,
			detail TEXT,
			who_waiting TEXT,
			project_dir TEXT,
			due TEXT,
			effort TEXT,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			completed_at TEXT,
			updated_at TEXT NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_cc_todos_source_ref
			ON cc_todos(source_ref) WHERE source_ref IS NOT NULL AND source_ref != '';

		CREATE TABLE IF NOT EXISTS cc_calendar_cache (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			day TEXT NOT NULL,
			title TEXT NOT NULL,
			start_time TEXT NOT NULL,
			end_time TEXT NOT NULL,
			all_day INTEGER NOT NULL DEFAULT 0,
			declined INTEGER NOT NULL DEFAULT 0,
			calendar_id TEXT NOT NULL DEFAULT '',
			cached_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cc_suggestions (
			id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			focus TEXT,
			ranked_todo_ids TEXT DEFAULT '[]',
			reasons TEXT DEFAULT '{}',
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cc_pending_actions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			todo_id TEXT NOT NULL,
			duration_minutes INTEGER,
			requested_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cc_meta (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cc_bookmarks (
			session_id TEXT PRIMARY KEY,
			project TEXT,
			repo TEXT,
			branch TEXT,
			label TEXT,
			summary TEXT,
			created_at TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS cc_learned_paths (
			path TEXT PRIMARY KEY,
			added_at TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}

	// Add calendar_id column if missing (added after initial schema)
	_, _ = db.Exec(`ALTER TABLE cc_calendar_cache ADD COLUMN calendar_id TEXT NOT NULL DEFAULT ''`)

	// Add session_id column if missing (added for CLI todo creation with session links)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN session_id TEXT`)

	// Add sort_order column to learned paths if missing (added for manual reordering)
	_, _ = db.Exec(`ALTER TABLE cc_learned_paths ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`)

	// Add worktree columns to bookmarks if missing (added for worktree sessions)
	_, _ = db.Exec(`ALTER TABLE cc_bookmarks ADD COLUMN worktree_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE cc_bookmarks ADD COLUMN source_repo TEXT`)

	// Add proposed_prompt and session_status columns to todos if missing (added for todo agent launcher)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN proposed_prompt TEXT`)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN session_status TEXT`)

	// Add display_id column for stable human-readable references (e.g. "TODO #19")
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN display_id INTEGER`)
	// Backfill any rows missing a display_id
	_, _ = db.Exec(`UPDATE cc_todos SET display_id = (
		SELECT rn FROM (
			SELECT id, ROW_NUMBER() OVER (ORDER BY created_at ASC) AS rn
			FROM cc_todos
		) ranked WHERE ranked.id = cc_todos.id
	) WHERE display_id IS NULL`)

	// Add triage_status column to todos if missing (added for todo triage tabs)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN triage_status TEXT NOT NULL DEFAULT 'accepted'`)

	// Add session_summary column to todos if missing (added for agent review summaries)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN session_summary TEXT`)

	// Source sync tracking table (added for BUG-015: data source connectivity validation)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_source_sync (
		source TEXT PRIMARY KEY,
		last_success TEXT,
		last_error TEXT,
		updated_at TEXT NOT NULL
	)`)
	// Ensure every learned path has a unique sort_order (fixes duplicates from swap bug).
	// Uses ROW_NUMBER to assign dense sequential values ordered by existing sort_order then added_at.
	_, _ = db.Exec(`UPDATE cc_learned_paths SET sort_order = (
		SELECT rn FROM (
			SELECT path, ROW_NUMBER() OVER (ORDER BY sort_order ASC, added_at ASC) AS rn
			FROM cc_learned_paths
		) ranked WHERE ranked.path = cc_learned_paths.path
	)`)

	// Add description column to learned paths if missing (for LLM-generated project summaries)
	_, _ = db.Exec(`ALTER TABLE cc_learned_paths ADD COLUMN description TEXT NOT NULL DEFAULT ''`)

	// Add launch_mode column to todos if missing (for persisting wizard mode selection)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN launch_mode TEXT`)

	// Add source_context columns to todos if missing (for todo source context feature)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN source_context TEXT`)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN source_context_at TEXT`)

	// Add session_log_path column to todos if missing (for session log replay)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN session_log_path TEXT`)

	// Drop the threads table (feature removed, preserved on threads-feature branch)
	_, _ = db.Exec(`DROP TABLE IF EXISTS cc_threads`)

	// Add cc_todo_merges table for duplicate detection (tracks synthesis/original relationships)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_todo_merges (
		synthesis_id TEXT NOT NULL,
		original_id TEXT NOT NULL,
		vetoed INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		PRIMARY KEY (synthesis_id, original_id)
	)`)

	// --- Todo status redesign migration ---
	// Collapse three status fields (status, triage_status, session_status) into
	// a single status FSM. We detect whether the migration is needed by checking
	// if the triage_status column still exists.
	if columnExists(db, "cc_todos", "triage_status") {
		if err := migrateTodoStatusFSM(db); err != nil {
			return fmt.Errorf("todo status FSM migration: %w", err)
		}
	}

	// Pull requests table for PR tracking plugin
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_pull_requests (
		id TEXT PRIMARY KEY,
		repo TEXT NOT NULL,
		number INTEGER NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		author TEXT NOT NULL,
		draft INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		review_decision TEXT,
		my_role TEXT,
		reviewer_logins TEXT DEFAULT '[]',
		pending_reviewer_logins TEXT DEFAULT '[]',
		comment_count INTEGER NOT NULL DEFAULT 0,
		unresolved_thread_count INTEGER NOT NULL DEFAULT 0,
		last_activity_at TEXT NOT NULL,
		ci_status TEXT,
		category TEXT,
		fetched_at TEXT NOT NULL
	)`)

	// BUG-101: Backfill display_id for existing rows that have display_id=0.
	// The original backfill only handled NULL, but rows may have ended up with 0
	// (e.g. explicit default or COALESCE in reads masking NULL). This assigns
	// sequential IDs starting after the current max, ordered by created_at.
	_, _ = db.Exec(`UPDATE cc_todos SET display_id = (
		SELECT COALESCE(MAX(display_id), 0) FROM cc_todos WHERE display_id > 0
	) + (
		SELECT COUNT(*) FROM cc_todos t2
		WHERE (t2.display_id IS NULL OR t2.display_id = 0)
		AND t2.rowid <= cc_todos.rowid
	) WHERE display_id IS NULL OR display_id = 0`)

	// Session registry table (for the CCC daemon).
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_sessions (
		session_id TEXT PRIMARY KEY,
		topic TEXT,
		pid INTEGER,
		project TEXT,
		repo TEXT,
		branch TEXT,
		worktree_path TEXT,
		state TEXT NOT NULL DEFAULT 'active',
		registered_at TEXT NOT NULL,
		ended_at TEXT
	)`)

	// Automation runs tracking table (for the automation framework).
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_automation_runs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		name        TEXT NOT NULL,
		started_at  TEXT NOT NULL,
		finished_at TEXT NOT NULL,
		status      TEXT NOT NULL,
		message     TEXT NOT NULL DEFAULT ''
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_automation_runs_name_started
		ON cc_automation_runs(name, started_at)`)

	// PR automation columns (agent tracking)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN state TEXT NOT NULL DEFAULT 'open'`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN head_sha TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN agent_status TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN agent_category TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN agent_head_sha TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN agent_summary TEXT NOT NULL DEFAULT ''`)

	// PR ignore support
	_, _ = db.Exec(`ALTER TABLE cc_pull_requests ADD COLUMN ignored BOOLEAN NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_ignored_repos (repo TEXT PRIMARY KEY)`)

	// Agent cost tracking (for agent governance / budget caps)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_agent_costs (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_id      TEXT NOT NULL,
		automation    TEXT NOT NULL DEFAULT '',
		started_at    TEXT NOT NULL,
		finished_at   TEXT,
		duration_sec  INTEGER,
		budget_usd    REAL NOT NULL DEFAULT 0,
		cost_usd      REAL NOT NULL DEFAULT 0,
		input_tokens  INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cost_source   TEXT NOT NULL DEFAULT 'estimate',
		exit_code     INTEGER,
		status        TEXT NOT NULL DEFAULT 'running',
		project_dir   TEXT NOT NULL DEFAULT ''
	)`)
	// Migration: add project_dir column if missing (existing DBs).
	_, _ = db.Exec(`ALTER TABLE cc_agent_costs ADD COLUMN project_dir TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_costs_started ON cc_agent_costs(started_at)`)

	// Budget state key-value store (for agent governance)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_budget_state (
		key        TEXT PRIMARY KEY,
		value_num  REAL NOT NULL DEFAULT 0,
		value_text TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL
	)`)

	// Archived sessions table (auto-saved ended sessions for the unified sessions view)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_archived_sessions (
		session_id    TEXT PRIMARY KEY,
		topic         TEXT,
		project       TEXT,
		repo          TEXT,
		branch        TEXT,
		worktree_path TEXT,
		registered_at TEXT NOT NULL,
		ended_at      TEXT NOT NULL
	)`)

	// Soft-delete support: add deleted_at column and update unique index to exclude soft-deleted rows
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN deleted_at TEXT`)
	_, _ = db.Exec(`DROP INDEX IF EXISTS idx_cc_todos_source_ref`)
	_, _ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_cc_todos_source_ref ON cc_todos(source_ref) WHERE source_ref IS NOT NULL AND source_ref != '' AND deleted_at IS NULL`)

	// Focus & star priority system
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN focus INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE cc_todos ADD COLUMN starred INTEGER NOT NULL DEFAULT 0`)

	// Booking table: tracks Google Calendar events scheduled for todos
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cc_todo_bookings (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		todo_id      TEXT NOT NULL,
		event_id     TEXT NOT NULL,
		start_time   TEXT NOT NULL,
		end_time     TEXT NOT NULL,
		duration_min INTEGER NOT NULL DEFAULT 0,
		created_at   TEXT NOT NULL
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_todo_bookings_todo_id ON cc_todo_bookings(todo_id)`)

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// columnExists checks whether a column exists on a table via pragma.
func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// migrateTodoStatusFSM remaps the three-field status model to a single status
// column and then drops triage_status and session_status via table recreation.
func migrateTodoStatusFSM(db *sql.DB) error {
	// Step 1: Remap status values while old columns still exist.
	// Order matters: session_status mappings first (more specific), then
	// triage_status mappings for rows with no session_status.
	remaps := []string{
		// session_status-based mappings (status='active')
		`UPDATE cc_todos SET status = 'enqueued'  WHERE status = 'active' AND COALESCE(session_status, '') = 'queued'`,
		`UPDATE cc_todos SET status = 'running'   WHERE status = 'active' AND COALESCE(session_status, '') = 'active'`,
		`UPDATE cc_todos SET status = 'review'    WHERE status = 'active' AND COALESCE(session_status, '') = 'review'`,
		`UPDATE cc_todos SET status = 'failed'    WHERE status = 'active' AND COALESCE(session_status, '') = 'failed'`,
		`UPDATE cc_todos SET status = 'review'    WHERE status = 'active' AND COALESCE(session_status, '') = 'completed'`,
		`UPDATE cc_todos SET status = 'blocked'   WHERE status = 'active' AND COALESCE(session_status, '') = 'blocked'`,
		// triage_status-based mappings (remaining active rows with no session_status)
		`UPDATE cc_todos SET status = 'new'       WHERE status = 'active' AND COALESCE(triage_status, 'accepted') = 'new' AND COALESCE(session_status, '') = ''`,
		`UPDATE cc_todos SET status = 'backlog'   WHERE status = 'active' AND COALESCE(triage_status, 'accepted') = 'accepted' AND COALESCE(session_status, '') = ''`,
		// completed and dismissed stay as-is (no UPDATE needed)
	}
	for _, q := range remaps {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("remap: %w", err)
		}
	}

	// Step 2: Recreate the table without triage_status and session_status.
	// SQLite doesn't reliably support DROP COLUMN in all builds, so we use
	// the standard create-copy-drop-rename pattern.
	stmts := []string{
		`CREATE TABLE cc_todos_new (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'backlog',
			source TEXT NOT NULL DEFAULT 'manual',
			source_ref TEXT,
			context TEXT,
			detail TEXT,
			who_waiting TEXT,
			project_dir TEXT,
			launch_mode TEXT,
			due TEXT,
			effort TEXT,
			sort_order INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			proposed_prompt TEXT,
			session_summary TEXT,
			session_log_path TEXT,
			source_context TEXT,
			source_context_at TEXT,
			display_id INTEGER,
			created_at TEXT NOT NULL,
			completed_at TEXT,
			updated_at TEXT NOT NULL
		)`,
		`INSERT INTO cc_todos_new (id, title, status, source, source_ref, context, detail,
			who_waiting, project_dir, launch_mode, due, effort, sort_order, session_id, proposed_prompt,
			session_summary, session_log_path, source_context, source_context_at, display_id,
			created_at, completed_at, updated_at)
		SELECT id, title, status, source, source_ref, context, detail,
			who_waiting, project_dir, launch_mode, due, effort, sort_order, session_id, proposed_prompt,
			session_summary, session_log_path, source_context, source_context_at, display_id,
			created_at, completed_at, updated_at
		FROM cc_todos`,
		`DROP TABLE cc_todos`,
		`ALTER TABLE cc_todos_new RENAME TO cc_todos`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cc_todos_source_ref
			ON cc_todos(source_ref) WHERE source_ref IS NOT NULL AND source_ref != ''`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("recreate table: %w", err)
		}
	}

	return nil
}

func ParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05", s)
		if err != nil {
			return time.Time{}
		}
	}
	return t.Local()
}
