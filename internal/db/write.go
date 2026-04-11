package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Write methods -- Todos
// ---------------------------------------------------------------------------

func DBCompleteTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET status = 'completed', completed_at = ?, updated_at = ?, focus = 0, starred = 0 WHERE id = ?`,
		now, now, id)
	if err != nil {
		return fmt.Errorf("complete todo %s: %w", id, err)
	}
	return nil
}

func DBDismissTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET status = 'dismissed', updated_at = ?, focus = 0, starred = 0 WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("dismiss todo %s: %w", id, err)
	}
	return nil
}

func DBRestoreTodo(db *sql.DB, id, status string, completedAt *time.Time) error {
	now := FormatTime(time.Now())
	var ca *string
	if completedAt != nil {
		s := FormatTime(*completedAt)
		ca = &s
	}
	_, err := db.Exec(`UPDATE cc_todos SET status = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		status, ca, now, id)
	if err != nil {
		return fmt.Errorf("restore todo %s: %w", id, err)
	}
	return nil
}

func DBDeferTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET sort_order = (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM cc_todos WHERE status NOT IN ('completed', 'dismissed') AND deleted_at IS NULL), updated_at = ? WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("defer todo %s: %w", id, err)
	}
	return nil
}

func DBPromoteTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET sort_order = (SELECT COALESCE(MIN(sort_order), 0) - 1 FROM cc_todos WHERE status NOT IN ('completed', 'dismissed') AND deleted_at IS NULL), updated_at = ? WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("promote todo %s: %w", id, err)
	}
	return nil
}

func DBInsertTodo(db *sql.DB, t Todo) error {
	now := FormatTime(time.Now())
	createdAt := FormatTime(t.CreatedAt)
	if t.CreatedAt.IsZero() {
		createdAt = now
	}
	var completedAt *string
	if t.CompletedAt != nil {
		s := FormatTime(*t.CompletedAt)
		completedAt = &s
	}
	_, err := db.Exec(`INSERT INTO cc_todos (id, title, status, source, source_ref, context, detail,
		who_waiting, project_dir, due, effort, session_id, proposed_prompt, session_summary,
		session_log_path, source_context, source_context_at,
		display_id, sort_order, created_at, completed_at, updated_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		(SELECT COALESCE(MAX(display_id), 0) + 1 FROM cc_todos),
		(SELECT COALESCE(MAX(sort_order), 0) + 1 FROM cc_todos WHERE status NOT IN ('completed', 'dismissed') AND deleted_at IS NULL),
		?, ?, ?)`,
		t.ID, t.Title, t.Status, t.Source, t.SourceRef, t.Context, t.Detail,
		t.WhoWaiting, t.ProjectDir, t.Due, t.Effort, t.SessionID, t.ProposedPrompt, t.SessionSummary,
		t.SessionLogPath, t.SourceContext, t.SourceContextAt,
		createdAt, completedAt, now)
	if err != nil {
		return fmt.Errorf("insert todo %s: %w", t.ID, err)
	}
	return nil
}

func DBUpdateTodo(db *sql.DB, id string, t Todo) error {
	now := FormatTime(time.Now())
	var completedAt *string
	if t.CompletedAt != nil {
		s := FormatTime(*t.CompletedAt)
		completedAt = &s
	}
	_, err := db.Exec(`UPDATE cc_todos SET title = ?, status = ?, source = ?,
		source_ref = NULLIF(?, ''), context = NULLIF(?, ''), detail = NULLIF(?, ''),
		who_waiting = NULLIF(?, ''), project_dir = NULLIF(?, ''), due = NULLIF(?, ''),
		effort = NULLIF(?, ''), session_id = NULLIF(?, ''),
		proposed_prompt = NULLIF(?, ''),
		session_summary = NULLIF(?, ''), session_log_path = NULLIF(?, ''),
		source_context = NULLIF(?, ''),
		source_context_at = NULLIF(?, ''),
		completed_at = ?, updated_at = ?
		WHERE id = ?`,
		t.Title, t.Status, t.Source, t.SourceRef, t.Context, t.Detail,
		t.WhoWaiting, t.ProjectDir, t.Due, t.Effort, t.SessionID,
		t.ProposedPrompt, t.SessionSummary, t.SessionLogPath,
		t.SourceContext, t.SourceContextAt, completedAt, now, id)
	if err != nil {
		return fmt.Errorf("update todo %s: %w", id, err)
	}
	return nil
}

// DBAcceptTodo transitions a todo from "new" to "backlog".
// Only updates if the current status is "new" to avoid overwriting
// a status that has already advanced (e.g., "running" from an agent launch).
func DBAcceptTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
		StatusBacklog, now, id, StatusNew)
	if err != nil {
		return fmt.Errorf("accept todo %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoStatus updates the status column for a todo.
func DBUpdateTodoStatus(db *sql.DB, id string, status string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET status = ?, updated_at = ? WHERE id = ?`,
		status, now, id)
	if err != nil {
		return fmt.Errorf("update todo status %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoProjectDir updates only the project_dir column for a todo.
func DBUpdateTodoProjectDir(db *sql.DB, id string, projectDir string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET project_dir = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		projectDir, now, id)
	if err != nil {
		return fmt.Errorf("update todo project_dir %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoLaunchMode updates only the launch_mode column for a todo.
func DBUpdateTodoLaunchMode(db *sql.DB, id string, launchMode string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET launch_mode = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		launchMode, now, id)
	if err != nil {
		return fmt.Errorf("update todo launch_mode %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoSessionID updates only the session_id column for a todo.
func DBUpdateTodoSessionID(db *sql.DB, id string, sessionID string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET session_id = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		sessionID, now, id)
	if err != nil {
		return fmt.Errorf("update todo session_id %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoSessionSummary updates only the session_summary column for a todo.
func DBUpdateTodoSessionSummary(db *sql.DB, id string, summary string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET session_summary = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		summary, now, id)
	if err != nil {
		return fmt.Errorf("update todo session summary %s: %w", id, err)
	}
	return nil
}

// DBUpdateTodoSourceContext updates only the source_context columns for a todo.
func DBUpdateTodoSourceContext(db *sql.DB, id, sourceContext, sourceContextAt string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET source_context = NULLIF(?, ''), source_context_at = NULLIF(?, ''), updated_at = ? WHERE id = ?`,
		sourceContext, sourceContextAt, now, id)
	if err != nil {
		return fmt.Errorf("update todo source_context %s: %w", id, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Focus & Star priority
// ---------------------------------------------------------------------------

// DBSetTodoStar sets the starred flag on a todo.
// Starring also sets focus=1 (starred items are implicitly focused).
// Unstarring (starred=false) clears only the starred flag; focus is unchanged.
func DBSetTodoStar(db *sql.DB, todoID string, starred bool) error {
	now := FormatTime(time.Now())
	var err error
	if starred {
		_, err = db.Exec(`UPDATE cc_todos SET starred = 1, focus = 1, updated_at = ? WHERE id = ?`, now, todoID)
	} else {
		_, err = db.Exec(`UPDATE cc_todos SET starred = 0, updated_at = ? WHERE id = ?`, now, todoID)
	}
	if err != nil {
		return fmt.Errorf("set todo star %s: %w", todoID, err)
	}
	return nil
}

// DBSetTodoFocus sets the focus flag on a todo.
// Focusing sets focus=1. Unfocusing sets focus=0 AND starred=0
// (a todo cannot be starred without being focused).
func DBSetTodoFocus(db *sql.DB, todoID string, focus bool) error {
	now := FormatTime(time.Now())
	var err error
	if focus {
		_, err = db.Exec(`UPDATE cc_todos SET focus = 1, updated_at = ? WHERE id = ?`, now, todoID)
	} else {
		_, err = db.Exec(`UPDATE cc_todos SET focus = 0, starred = 0, updated_at = ? WHERE id = ?`, now, todoID)
	}
	if err != nil {
		return fmt.Errorf("set todo focus %s: %w", todoID, err)
	}
	return nil
}

// DBClearStarAndFocus clears both star and focus flags on a todo.
// Used when completing or dismissing a todo.
func DBClearStarAndFocus(db *sql.DB, todoID string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET focus = 0, starred = 0, updated_at = ? WHERE id = ?`, now, todoID)
	if err != nil {
		return fmt.Errorf("clear star and focus %s: %w", todoID, err)
	}
	return nil
}

// DBInsertBooking inserts a new calendar booking record for a todo.
func DBInsertBooking(db *sql.DB, booking TodoBooking) error {
	now := FormatTime(time.Now())
	createdAt := now
	if !booking.CreatedAt.IsZero() {
		createdAt = FormatTime(booking.CreatedAt)
	}
	_, err := db.Exec(`INSERT INTO cc_todo_bookings (todo_id, event_id, start_time, end_time, duration_min, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		booking.TodoID, booking.EventID,
		FormatTime(booking.StartTime), FormatTime(booking.EndTime),
		booking.DurationMin, createdAt)
	if err != nil {
		return fmt.Errorf("insert booking for todo %s: %w", booking.TodoID, err)
	}
	return nil
}

// DBDeleteFutureBookings deletes all future bookings for a given todo.
func DBDeleteFutureBookings(db *sql.DB, todoID string) error {
	_, err := db.Exec(`DELETE FROM cc_todo_bookings WHERE todo_id = ? AND start_time > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')`, todoID)
	if err != nil {
		return fmt.Errorf("delete future bookings for todo %s: %w", todoID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Calendar & Suggestions
// ---------------------------------------------------------------------------

func DBReplaceCalendar(db *sql.DB, cal CalendarData) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM cc_calendar_cache`); err != nil {
		return err
	}

	now := FormatTime(time.Now())
	stmt, err := tx.Prepare(`INSERT INTO cc_calendar_cache (day, title, start_time, end_time, all_day, declined, calendar_id, cached_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ev := range cal.Today {
		if _, err := stmt.Exec("today", ev.Title, FormatTime(ev.Start), FormatTime(ev.End), ev.AllDay, ev.Declined, ev.CalendarID, now); err != nil {
			return err
		}
	}
	for _, ev := range cal.Tomorrow {
		if _, err := stmt.Exec("tomorrow", ev.Title, FormatTime(ev.Start), FormatTime(ev.End), ev.AllDay, ev.Declined, ev.CalendarID, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func DBSaveFocus(db *sql.DB, focus string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`INSERT OR REPLACE INTO cc_suggestions (id, focus, ranked_todo_ids, reasons, updated_at)
		VALUES (1, ?,
			COALESCE((SELECT ranked_todo_ids FROM cc_suggestions WHERE id = 1), '[]'),
			COALESCE((SELECT reasons FROM cc_suggestions WHERE id = 1), '{}'),
			?)`, focus, now)
	if err != nil {
		return fmt.Errorf("save focus: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Pending Actions
// ---------------------------------------------------------------------------

func DBInsertPendingAction(db *sql.DB, a PendingAction) error {
	_, err := db.Exec(`INSERT INTO cc_pending_actions (type, todo_id, duration_minutes, requested_at)
		VALUES (?, ?, ?, ?)`,
		a.Type, a.TodoID, a.DurationMinutes, FormatTime(a.RequestedAt))
	if err != nil {
		return fmt.Errorf("insert pending action %s: %w", a.TodoID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Bookmarks
// ---------------------------------------------------------------------------

func DBInsertBookmark(db *sql.DB, b Session, label string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO cc_bookmarks (session_id, project, repo, branch, label, summary, created_at, worktree_path, source_repo)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		b.SessionID, b.Project, b.Repo, b.Branch, label, b.Summary, FormatTime(b.Created),
		b.WorktreePath, b.SourceRepo)
	if err != nil {
		return fmt.Errorf("insert bookmark %s: %w", b.SessionID, err)
	}
	return nil
}

func DBRemoveBookmark(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM cc_bookmarks WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("remove bookmark %s: %w", sessionID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Learned Paths
// ---------------------------------------------------------------------------

func DBAddPath(db *sql.DB, path string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO cc_learned_paths (path, added_at, sort_order) VALUES (?, ?,
		(SELECT COALESCE(MAX(sort_order), 0) + 1 FROM cc_learned_paths))`,
		path, FormatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("add path %s: %w", path, err)
	}
	return nil
}

func DBRemovePath(db *sql.DB, path string) error {
	_, err := db.Exec(`DELETE FROM cc_learned_paths WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("remove path %s: %w", path, err)
	}
	return nil
}

// DBUpdatePathDescription updates the description for a learned path.
func DBUpdatePathDescription(db *sql.DB, path, description string) error {
	res, err := db.Exec(`UPDATE cc_learned_paths SET description = ? WHERE path = ?`, description, path)
	if err != nil {
		return fmt.Errorf("update path description %s: %w", path, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("path not found: %s", path)
	}
	return nil
}

// DBSwapPathOrder swaps the sort_order of two paths.
func DBSwapPathOrder(database *sql.DB, pathA, pathB string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("swap path order: begin tx: %w", err)
	}
	defer tx.Rollback()

	var orderA, orderB int
	if err := tx.QueryRow(`SELECT sort_order FROM cc_learned_paths WHERE path = ?`, pathA).Scan(&orderA); err != nil {
		return fmt.Errorf("swap path order: read A: %w", err)
	}
	if err := tx.QueryRow(`SELECT sort_order FROM cc_learned_paths WHERE path = ?`, pathB).Scan(&orderB); err != nil {
		return fmt.Errorf("swap path order: read B: %w", err)
	}

	if _, err := tx.Exec(`UPDATE cc_learned_paths SET sort_order = ? WHERE path = ?`, orderB, pathA); err != nil {
		return fmt.Errorf("swap path order: write A: %w", err)
	}
	if _, err := tx.Exec(`UPDATE cc_learned_paths SET sort_order = ? WHERE path = ?`, orderA, pathB); err != nil {
		return fmt.Errorf("swap path order: write B: %w", err)
	}

	return tx.Commit()
}

// DBSwapTodoOrder swaps the sort_order of two todos by ID.
func DBSwapTodoOrder(database *sql.DB, idA, idB string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("swap todo order: begin tx: %w", err)
	}
	defer tx.Rollback()

	var orderA, orderB int
	if err := tx.QueryRow(`SELECT sort_order FROM cc_todos WHERE id = ?`, idA).Scan(&orderA); err != nil {
		return fmt.Errorf("swap todo order: read A: %w", err)
	}
	if err := tx.QueryRow(`SELECT sort_order FROM cc_todos WHERE id = ?`, idB).Scan(&orderB); err != nil {
		return fmt.Errorf("swap todo order: read B: %w", err)
	}

	now := FormatTime(time.Now())
	if _, err := tx.Exec(`UPDATE cc_todos SET sort_order = ?, updated_at = ? WHERE id = ?`, orderB, now, idA); err != nil {
		return fmt.Errorf("swap todo order: write A: %w", err)
	}
	if _, err := tx.Exec(`UPDATE cc_todos SET sort_order = ?, updated_at = ? WHERE id = ?`, orderA, now, idB); err != nil {
		return fmt.Errorf("swap todo order: write B: %w", err)
	}

	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Write methods -- Pull Requests
// ---------------------------------------------------------------------------

// DBSavePullRequests performs a merge-based upsert of pull requests within the
// given transaction. Agent columns are preserved on conflict. Open PRs not in
// the fresh batch are archived (state='archived') rather than deleted.
// Slice fields (ReviewerLogins, PendingReviewerLogins) are JSON-serialized.
func DBSavePullRequests(tx *sql.Tx, prs []PullRequest) error {
	// Build set of fresh IDs for archive step
	freshIDs := make(map[string]bool, len(prs))
	for _, pr := range prs {
		freshIDs[pr.ID] = true
	}

	// Archive open PRs not in fresh batch
	rows, err := tx.Query(`SELECT id FROM cc_pull_requests WHERE state = 'open'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			rows.Scan(&id)
			if !freshIDs[id] {
				tx.Exec(`UPDATE cc_pull_requests SET state = 'archived' WHERE id = ?`, id)
			}
		}
	}

	// Upsert each PR — preserve agent columns on conflict
	for _, pr := range prs {
		reviewersJSON, _ := json.Marshal(pr.ReviewerLogins)
		if pr.ReviewerLogins == nil {
			reviewersJSON = []byte("[]")
		}
		pendingJSON, _ := json.Marshal(pr.PendingReviewerLogins)
		if pr.PendingReviewerLogins == nil {
			pendingJSON = []byte("[]")
		}

		_, err := tx.Exec(`INSERT INTO cc_pull_requests
			(id, repo, number, title, url, author, draft,
			 created_at, updated_at, review_decision, my_role,
			 reviewer_logins, pending_reviewer_logins,
			 comment_count, unresolved_thread_count, last_activity_at,
			 ci_status, category, fetched_at, state, head_sha)
			VALUES (?, ?, ?, ?, ?, ?, ?,
				?, ?, NULLIF(?,''), NULLIF(?,''),
				?, ?,
				?, ?, ?,
				NULLIF(?,''), NULLIF(?,''), ?, 'open', ?)
			ON CONFLICT(id) DO UPDATE SET
				repo=excluded.repo, number=excluded.number,
				title=excluded.title, url=excluded.url,
				author=excluded.author, draft=excluded.draft,
				created_at=excluded.created_at, updated_at=excluded.updated_at,
				review_decision=excluded.review_decision, my_role=excluded.my_role,
				reviewer_logins=excluded.reviewer_logins,
				pending_reviewer_logins=excluded.pending_reviewer_logins,
				comment_count=excluded.comment_count,
				unresolved_thread_count=excluded.unresolved_thread_count,
				last_activity_at=excluded.last_activity_at,
				ci_status=excluded.ci_status, category=excluded.category,
				fetched_at=excluded.fetched_at, state='open',
				head_sha=excluded.head_sha`,
			pr.ID, pr.Repo, pr.Number, pr.Title, pr.URL, pr.Author, pr.Draft,
			FormatTime(pr.CreatedAt), FormatTime(pr.UpdatedAt), pr.ReviewDecision, pr.MyRole,
			string(reviewersJSON), string(pendingJSON),
			pr.CommentCount, pr.UnresolvedThreadCount, FormatTime(pr.LastActivityAt),
			pr.CIStatus, pr.Category, FormatTime(pr.FetchedAt), pr.HeadSHA)
		if err != nil {
			return fmt.Errorf("upsert pull request %s: %w", pr.ID, err)
		}
	}
	return nil
}

// DBSetPRIgnored sets or clears the ignored flag on a pull request.
func DBSetPRIgnored(d *sql.DB, id string, ignored bool) error {
	_, err := d.Exec(`UPDATE cc_pull_requests SET ignored = ? WHERE id = ?`, ignored, id)
	return err
}

// DBAddIgnoredRepo adds a repo to the ignore list.
func DBAddIgnoredRepo(d *sql.DB, repo string) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO cc_ignored_repos (repo) VALUES (?)`, repo)
	return err
}

// DBRemoveIgnoredRepo removes a repo from the ignore list.
func DBRemoveIgnoredRepo(d *sql.DB, repo string) error {
	_, err := d.Exec(`DELETE FROM cc_ignored_repos WHERE repo = ?`, repo)
	return err
}

// DBUpdatePRAgentStatus updates the agent tracking columns for a pull request.
func DBUpdatePRAgentStatus(d *sql.DB, id, agentStatus, agentSessionID, agentCategory, agentHeadSHA, agentSummary string) error {
	_, err := d.Exec(`UPDATE cc_pull_requests SET
		agent_status=?, agent_session_id=NULLIF(?,''),
		agent_category=?, agent_head_sha=?, agent_summary=NULLIF(?,'')
		WHERE id=?`,
		agentStatus, agentSessionID, agentCategory, agentHeadSHA, agentSummary, id)
	return err
}

// DBUpdatePRAgentSessionID updates only the agent_session_id column for a
// pull request, leaving all other agent columns unchanged.
func DBUpdatePRAgentSessionID(d *sql.DB, id, sessionID string) error {
	_, err := d.Exec(`UPDATE cc_pull_requests SET agent_session_id=NULLIF(?,'') WHERE id=?`,
		sessionID, id)
	return err
}

// ---------------------------------------------------------------------------
// Write methods -- Bulk refresh result
// ---------------------------------------------------------------------------

// DBSaveRefreshResult atomically replaces all refresh-managed data (todos,
// calendar, suggestions, pending actions, generated_at) in a single
// transaction. This is the write path used by ai-cron.
func DBSaveRefreshResult(d *sql.DB, cc *CommandCenter) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := FormatTime(time.Now())

	// --- Todos: delete only IDs we're about to re-insert ---
	// This preserves any todos created during the refresh window (race-safe).
	// Todos not in cc.Todos (e.g., manual todos added mid-refresh) survive.

	// Snapshot focus/starred and session fields before delete so we can restore
	// them after re-insert. These are set outside the refresh cycle (user priority
	// flags and daemon-set session fields) and must survive refresh.
	type todoSnapshot struct {
		Focus, Starred       bool
		SessionID, Summary   string
		LogPath              string
	}
	snapshots := make(map[string]todoSnapshot)
	if len(cc.Todos) > 0 {
		ids := make([]interface{}, len(cc.Todos))
		placeholders := make([]string, len(cc.Todos))
		for i, t := range cc.Todos {
			ids[i] = t.ID
			placeholders[i] = "?"
		}
		rows, err := tx.Query(`SELECT id, COALESCE(focus, 0), COALESCE(starred, 0), COALESCE(session_id, ''), COALESCE(session_summary, ''), COALESCE(session_log_path, '') FROM cc_todos WHERE id IN (`+strings.Join(placeholders, ",")+`)`, ids...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id string
				var focusInt, starredInt int
				var sessID, sessSummary, sessLogPath string
				if rows.Scan(&id, &focusInt, &starredInt, &sessID, &sessSummary, &sessLogPath) == nil {
					snapshots[id] = todoSnapshot{
						Focus: focusInt == 1, Starred: starredInt == 1,
						SessionID: sessID, Summary: sessSummary, LogPath: sessLogPath,
					}
				}
			}
			rows.Close()
		}

		query := `DELETE FROM cc_todos WHERE id IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := tx.Exec(query, ids...); err != nil {
			return fmt.Errorf("clear known todos: %w", err)
		}
	}
	// Find the current max display_id so we can assign IDs to new todos.
	maxDisplayID := 0
	for _, t := range cc.Todos {
		if t.DisplayID > maxDisplayID {
			maxDisplayID = t.DisplayID
		}
	}
	for i, t := range cc.Todos {
		createdAt := FormatTime(t.CreatedAt)
		if t.CreatedAt.IsZero() {
			createdAt = now
		}
		var completedAt *string
		if t.CompletedAt != nil {
			s := FormatTime(*t.CompletedAt)
			completedAt = &s
		}
		// Assign a display_id to new todos that don't have one yet.
		displayID := t.DisplayID
		if displayID == 0 {
			maxDisplayID++
			displayID = maxDisplayID
		}
		// Include focus/starred in the INSERT so they survive refresh cycles
		// without relying solely on the snapshot-restore step below.
		focusVal := 0
		if t.Focus {
			focusVal = 1
		}
		starVal := 0
		if t.Starred {
			starVal = 1
		}
		_, err := tx.Exec(`INSERT INTO cc_todos (id, title, status, source, source_ref, context, detail,
			who_waiting, project_dir, launch_mode, due, effort, session_id, proposed_prompt, session_summary,
			session_log_path, source_context, source_context_at,
			focus, starred,
			display_id, sort_order, created_at, completed_at, updated_at)
			VALUES (?, ?, ?, ?,
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''),
			NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			?, ?,
			?, ?, ?, ?, ?)`,
			t.ID, t.Title, t.Status, t.Source,
			t.SourceRef, t.Context, t.Detail,
			t.WhoWaiting, t.ProjectDir, t.LaunchMode,
			t.Due, t.Effort, t.SessionID,
			t.ProposedPrompt, t.SessionSummary,
			t.SessionLogPath, t.SourceContext, t.SourceContextAt,
			focusVal, starVal,
			displayID, i, createdAt, completedAt, now)
		if err != nil {
			return fmt.Errorf("insert todo %s: %w", t.ID, err)
		}
	}

	// Restore focus/starred and session fields from snapshot.
	for id, snap := range snapshots {
		if snap.Focus || snap.Starred || snap.SessionID != "" || snap.Summary != "" || snap.LogPath != "" {
			focusVal, starVal := 0, 0
			if snap.Focus {
				focusVal = 1
			}
			if snap.Starred {
				starVal = 1
			}
			if _, err := tx.Exec(`UPDATE cc_todos SET focus = ?, starred = ?, session_id = NULLIF(?, ''), session_summary = NULLIF(?, ''), session_log_path = NULLIF(?, '') WHERE id = ?`,
				focusVal, starVal, snap.SessionID, snap.Summary, snap.LogPath, id); err != nil {
				return fmt.Errorf("restore snapshot for todo %s: %w", id, err)
			}
		}
	}

	// --- Pull requests: replace ---
	if err := DBSavePullRequests(tx, cc.PullRequests); err != nil {
		return err
	}

	// --- Calendar: replace ---
	if _, err := tx.Exec(`DELETE FROM cc_calendar_cache`); err != nil {
		return fmt.Errorf("clear calendar: %w", err)
	}
	for _, ev := range cc.Calendar.Today {
		if _, err := tx.Exec(`INSERT INTO cc_calendar_cache (day, title, start_time, end_time, all_day, declined, calendar_id, cached_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"today", ev.Title, FormatTime(ev.Start), FormatTime(ev.End), ev.AllDay, ev.Declined, ev.CalendarID, now); err != nil {
			return fmt.Errorf("insert calendar event: %w", err)
		}
	}
	for _, ev := range cc.Calendar.Tomorrow {
		if _, err := tx.Exec(`INSERT INTO cc_calendar_cache (day, title, start_time, end_time, all_day, declined, calendar_id, cached_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"tomorrow", ev.Title, FormatTime(ev.Start), FormatTime(ev.End), ev.AllDay, ev.Declined, ev.CalendarID, now); err != nil {
			return fmt.Errorf("insert calendar event: %w", err)
		}
	}

	// --- Suggestions ---
	rankedJSON, _ := json.Marshal(cc.Suggestions.RankedTodoIDs)
	reasonsJSON, _ := json.Marshal(cc.Suggestions.Reasons)
	if _, err := tx.Exec(`INSERT OR REPLACE INTO cc_suggestions (id, focus, ranked_todo_ids, reasons, updated_at)
		VALUES (1, ?, ?, ?, ?)`,
		cc.Suggestions.Focus, string(rankedJSON), string(reasonsJSON), now); err != nil {
		return fmt.Errorf("save suggestions: %w", err)
	}

	// --- Pending actions ---
	if _, err := tx.Exec(`DELETE FROM cc_pending_actions`); err != nil {
		return fmt.Errorf("clear pending actions: %w", err)
	}
	for _, a := range cc.PendingActions {
		if _, err := tx.Exec(`INSERT INTO cc_pending_actions (type, todo_id, duration_minutes, requested_at)
			VALUES (?, ?, ?, ?)`,
			a.Type, a.TodoID, a.DurationMinutes, FormatTime(a.RequestedAt)); err != nil {
			return fmt.Errorf("insert pending action: %w", err)
		}
	}

	// --- Generated at ---
	if _, err := tx.Exec(`INSERT OR REPLACE INTO cc_meta (key, value, updated_at)
		VALUES ('generated_at', ?, ?)`,
		FormatTime(cc.GeneratedAt), now); err != nil {
		return fmt.Errorf("save generated_at: %w", err)
	}

	return tx.Commit()
}

// DBSaveSuggestions replaces the suggestions row.
func DBSaveSuggestions(d *sql.DB, s Suggestions) error {
	now := FormatTime(time.Now())
	rankedJSON, _ := json.Marshal(s.RankedTodoIDs)
	reasonsJSON, _ := json.Marshal(s.Reasons)
	_, err := d.Exec(`INSERT OR REPLACE INTO cc_suggestions (id, focus, ranked_todo_ids, reasons, updated_at)
		VALUES (1, ?, ?, ?, ?)`,
		s.Focus, string(rankedJSON), string(reasonsJSON), now)
	if err != nil {
		return fmt.Errorf("save suggestions: %w", err)
	}
	return nil
}

// DBSetMeta upserts a key-value pair in cc_meta.
func DBSetMeta(d *sql.DB, key, value string) error {
	now := FormatTime(time.Now())
	_, err := d.Exec(`INSERT OR REPLACE INTO cc_meta (key, value, updated_at) VALUES (?, ?, ?)`,
		key, value, now)
	if err != nil {
		return fmt.Errorf("set meta %s: %w", key, err)
	}
	return nil
}

// DBUpsertSourceSync records a sync result (success or failure) for a data source.
func DBUpsertSourceSync(d *sql.DB, source string, syncErr error) error {
	now := FormatTime(time.Now())
	if syncErr == nil {
		// Success: update last_success, clear last_error
		_, err := d.Exec(`INSERT OR REPLACE INTO cc_source_sync (source, last_success, last_error, updated_at)
			VALUES (?, ?, '', ?)`, source, now, now)
		if err != nil {
			return fmt.Errorf("upsert source sync %s: %w", source, err)
		}
	} else {
		// Failure: keep existing last_success, update last_error
		_, err := d.Exec(`INSERT INTO cc_source_sync (source, last_success, last_error, updated_at)
			VALUES (?, NULL, ?, ?)
			ON CONFLICT(source) DO UPDATE SET last_error = ?, updated_at = ?`,
			source, syncErr.Error(), now, syncErr.Error(), now)
		if err != nil {
			return fmt.Errorf("upsert source sync %s: %w", source, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Merges
// ---------------------------------------------------------------------------

func DBInsertMerge(db *sql.DB, synthesisID, originalID string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`INSERT OR REPLACE INTO cc_todo_merges (synthesis_id, original_id, vetoed, created_at)
		VALUES (?, ?, 0, ?)`, synthesisID, originalID, now)
	return err
}

func DBSetMergeVetoed(db *sql.DB, synthesisID, originalID string, vetoed bool) error {
	v := 0
	if vetoed {
		v = 1
	}
	_, err := db.Exec(`UPDATE cc_todo_merges SET vetoed = ? WHERE synthesis_id = ? AND original_id = ?`,
		v, synthesisID, originalID)
	return err
}

func DBDeleteSynthesisMerges(db *sql.DB, synthesisID string) error {
	_, err := db.Exec(`DELETE FROM cc_todo_merges WHERE synthesis_id = ?`, synthesisID)
	return err
}

func DBDeleteTodo(db *sql.DB, id string) error {
	now := FormatTime(time.Now())
	_, err := db.Exec(`UPDATE cc_todos SET deleted_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

// ---------------------------------------------------------------------------
// Write methods -- Sessions (daemon registry)
// ---------------------------------------------------------------------------

// DBInsertSession inserts or replaces a session in cc_sessions.
func DBInsertSession(d *sql.DB, s SessionRecord) error {
	_, err := d.Exec(`INSERT OR REPLACE INTO cc_sessions
		(session_id, topic, pid, project, repo, branch, worktree_path, state, registered_at, ended_at)
		VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, ''))`,
		s.SessionID, s.Topic, s.PID, s.Project, s.Repo, s.Branch, s.WorktreePath, s.State, s.RegisteredAt, s.EndedAt)
	if err != nil {
		return fmt.Errorf("insert session %s: %w", s.SessionID, err)
	}
	return nil
}

// DBUpdateSession updates specific fields on a session. Only whitelisted
// field names are accepted to prevent SQL injection.
func DBUpdateSession(d *sql.DB, sessionID string, fields map[string]interface{}) error {
	allowed := map[string]bool{
		"topic": true, "pid": true, "project": true, "repo": true,
		"branch": true, "worktree_path": true, "state": true, "ended_at": true,
	}

	setClauses := make([]string, 0, len(fields))
	args := make([]interface{}, 0, len(fields)+1)
	for k, v := range fields {
		if !allowed[k] {
			return fmt.Errorf("disallowed field %q in session update", k)
		}
		setClauses = append(setClauses, k+" = ?")
		args = append(args, v)
	}
	if len(setClauses) == 0 {
		return nil
	}

	query := "UPDATE cc_sessions SET "
	for i, clause := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += clause
	}
	query += " WHERE session_id = ?"
	args = append(args, sessionID)

	_, err := d.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("update session %s: %w", sessionID, err)
	}
	return nil
}

// DBUpdateSessionState updates the state of a session. If the new state is
// "ended", ended_at is automatically set to the current time.
func DBUpdateSessionState(d *sql.DB, sessionID, state string) error {
	fields := map[string]interface{}{"state": state}
	if state == "ended" {
		fields["ended_at"] = FormatTime(time.Now())
	}
	return DBUpdateSession(d, sessionID, fields)
}

// DBClearPendingActions removes all pending actions.
func DBClearPendingActions(d *sql.DB) error {
	_, err := d.Exec(`DELETE FROM cc_pending_actions`)
	if err != nil {
		return fmt.Errorf("clear pending actions: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Write methods -- Archived Sessions
// ---------------------------------------------------------------------------

// DBInsertArchivedSession inserts or replaces an archived session.
func DBInsertArchivedSession(db *sql.DB, s ArchivedSession) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO cc_archived_sessions
		(session_id, topic, project, repo, branch, worktree_path, registered_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.SessionID, s.Topic, s.Project, s.Repo, s.Branch, s.WorktreePath, s.RegisteredAt, s.EndedAt)
	return err
}

// DBDeleteArchivedSession removes an archived session by ID.
func DBDeleteArchivedSession(db *sql.DB, sessionID string) error {
	_, err := db.Exec(`DELETE FROM cc_archived_sessions WHERE session_id = ?`, sessionID)
	return err
}
