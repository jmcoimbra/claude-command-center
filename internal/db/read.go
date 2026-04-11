package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Read methods
// ---------------------------------------------------------------------------

func LoadCommandCenterFromDB(db *sql.DB) (*CommandCenter, error) {
	cc := &CommandCenter{}

	todos, err := dbLoadTodos(db)
	if err != nil {
		return nil, fmt.Errorf("load todos: %w", err)
	}
	cc.Todos = todos

	cal, err := dbLoadCalendar(db)
	if err != nil {
		return nil, fmt.Errorf("load calendar: %w", err)
	}
	cc.Calendar = cal

	sug, err := dbLoadSuggestions(db)
	if err != nil {
		return nil, fmt.Errorf("load suggestions: %w", err)
	}
	cc.Suggestions = sug

	prs, err := DBLoadPullRequests(db)
	if err != nil {
		return nil, fmt.Errorf("load pull requests: %w", err)
	}
	cc.PullRequests = prs

	actions, err := dbLoadPendingActions(db)
	if err != nil {
		return nil, fmt.Errorf("load pending actions: %w", err)
	}
	cc.PendingActions = actions

	genAt, err := dbLoadGeneratedAt(db)
	if err == nil {
		cc.GeneratedAt = genAt
	}

	merges, err := DBLoadMerges(db)
	if err != nil {
		return nil, fmt.Errorf("load merges: %w", err)
	}
	cc.Merges = merges

	return cc, nil
}

func dbLoadTodos(db *sql.DB) ([]Todo, error) {
	rows, err := db.Query(`SELECT id, COALESCE(display_id, 0), title, status, source, source_ref, context, detail,
		who_waiting, project_dir, launch_mode, due, effort, session_id, proposed_prompt,
		session_summary, session_log_path, source_context, source_context_at,
		COALESCE(focus, 0), COALESCE(starred, 0),
		created_at, completed_at
		FROM cc_todos WHERE deleted_at IS NULL ORDER BY sort_order ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []Todo
	for rows.Next() {
		var t Todo
		var createdStr string
		var completedStr sql.NullString
		var sourceRef, ctx, detail, who, projDir, launchMode, due, effort, sessionID, proposedPrompt, sessionSummary, sessionLogPath sql.NullString
		var sourceContext, sourceContextAt sql.NullString
		var focus, starred int

		err := rows.Scan(&t.ID, &t.DisplayID, &t.Title, &t.Status, &t.Source,
			&sourceRef, &ctx, &detail, &who, &projDir, &launchMode, &due, &effort, &sessionID,
			&proposedPrompt, &sessionSummary, &sessionLogPath, &sourceContext, &sourceContextAt,
			&focus, &starred,
			&createdStr, &completedStr)
		if err != nil {
			return nil, err
		}

		t.SourceRef = sourceRef.String
		t.Context = ctx.String
		t.Detail = detail.String
		t.WhoWaiting = who.String
		t.ProjectDir = projDir.String
		t.LaunchMode = launchMode.String
		t.Due = due.String
		t.Effort = effort.String
		t.SessionID = sessionID.String
		t.ProposedPrompt = proposedPrompt.String
		t.SessionSummary = sessionSummary.String
		t.SessionLogPath = sessionLogPath.String
		t.SourceContext = sourceContext.String
		t.SourceContextAt = sourceContextAt.String
		t.Focus = focus != 0
		t.Starred = starred != 0
		t.CreatedAt = ParseTime(createdStr)
		if completedStr.Valid {
			ct := ParseTime(completedStr.String)
			t.CompletedAt = &ct
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

func dbLoadCalendar(db *sql.DB) (CalendarData, error) {
	cal := CalendarData{}
	rows, err := db.Query(`SELECT day, title, start_time, end_time, all_day, declined, calendar_id
		FROM cc_calendar_cache ORDER BY start_time ASC`)
	if err != nil {
		return cal, err
	}
	defer rows.Close()

	for rows.Next() {
		var day, title, startStr, endStr, calendarID string
		var allDay, declined bool
		if err := rows.Scan(&day, &title, &startStr, &endStr, &allDay, &declined, &calendarID); err != nil {
			return cal, err
		}
		ev := CalendarEvent{
			Title:      title,
			Start:      ParseTime(startStr),
			End:        ParseTime(endStr),
			AllDay:     allDay,
			Declined:   declined,
			CalendarID: calendarID,
		}
		if day == "today" {
			cal.Today = append(cal.Today, ev)
		} else {
			cal.Tomorrow = append(cal.Tomorrow, ev)
		}
	}
	if err := rows.Err(); err != nil {
		return cal, err
	}

	// Clamp multi-day events to their day boundaries so they sort and
	// display correctly (e.g. a 3-day event shows as starting at midnight
	// today, not at its original start time days ago).
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	todayEnd := todayStart.Add(24 * time.Hour)
	tomorrowEnd := todayEnd.Add(24 * time.Hour)

	cal.Today = clampEventsToDayBounds(cal.Today, todayStart, todayEnd)
	cal.Tomorrow = clampEventsToDayBounds(cal.Tomorrow, todayEnd, tomorrowEnd)

	sortCalendarEvents(cal.Today)
	sortCalendarEvents(cal.Tomorrow)

	return cal, nil
}

// clampEventsToDayBounds adjusts multi-day events so their Start/End are
// clamped to the given day boundaries. This ensures correct sort order and
// display for events that span multiple days.
func clampEventsToDayBounds(events []CalendarEvent, dayStart, dayEnd time.Time) []CalendarEvent {
	for i := range events {
		if events[i].AllDay {
			continue
		}
		wasClamped := events[i].Start.Before(dayStart)
		if wasClamped {
			events[i].Start = dayStart
		}
		if events[i].End.After(dayEnd) {
			events[i].End = dayEnd
		}
		// Events that span the entire day after clamping are effectively all-day.
		// This catches Exchange/Outlook-style "all-day" events that use DateTime
		// instead of Date, and multi-day events clamped to day boundaries.
		if wasClamped && events[i].End.Sub(events[i].Start) >= 12*time.Hour {
			events[i].AllDay = true
		}
	}
	return events
}

// sortCalendarEvents sorts events with all-day events first, then timed
// events by start time.
func sortCalendarEvents(events []CalendarEvent) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].AllDay != events[j].AllDay {
			return events[i].AllDay // all-day events first
		}
		return events[i].Start.Before(events[j].Start)
	})
}

func dbLoadSuggestions(db *sql.DB) (Suggestions, error) {
	var s Suggestions
	var rankedJSON, reasonsJSON sql.NullString
	var focus sql.NullString
	err := db.QueryRow(`SELECT focus, ranked_todo_ids, reasons FROM cc_suggestions WHERE id = 1`).
		Scan(&focus, &rankedJSON, &reasonsJSON)
	if err == sql.ErrNoRows {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.Focus = focus.String
	if rankedJSON.Valid {
		if err := json.Unmarshal([]byte(rankedJSON.String), &s.RankedTodoIDs); err != nil {
			log.Printf("WARNING: corrupt ranked_todo_ids JSON: %v", err)
		}
	}
	if reasonsJSON.Valid {
		if err := json.Unmarshal([]byte(reasonsJSON.String), &s.Reasons); err != nil {
			log.Printf("WARNING: corrupt reasons JSON: %v", err)
		}
	}
	return s, nil
}

func dbLoadPendingActions(db *sql.DB) ([]PendingAction, error) {
	rows, err := db.Query(`SELECT type, todo_id, duration_minutes, requested_at FROM cc_pending_actions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var actions []PendingAction
	for rows.Next() {
		var a PendingAction
		var reqStr string
		var dur sql.NullInt64
		if err := rows.Scan(&a.Type, &a.TodoID, &dur, &reqStr); err != nil {
			return nil, err
		}
		if dur.Valid {
			a.DurationMinutes = int(dur.Int64)
		}
		a.RequestedAt = ParseTime(reqStr)
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

// DBLoadPullRequests loads all non-archived pull requests from the database.
func DBLoadPullRequests(d *sql.DB) ([]PullRequest, error) {
	rows, err := d.Query(`SELECT id, repo, number, title, url, author, draft,
		created_at, updated_at, review_decision, my_role,
		reviewer_logins, pending_reviewer_logins,
		comment_count, unresolved_thread_count, last_activity_at,
		ci_status, category, fetched_at,
		state, head_sha, agent_session_id, agent_status, agent_category, agent_head_sha, agent_summary
		FROM cc_pull_requests
		WHERE state != 'archived'
		  AND ignored = 0
		  AND repo NOT IN (SELECT repo FROM cc_ignored_repos)
		ORDER BY last_activity_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []PullRequest
	for rows.Next() {
		var pr PullRequest
		var createdStr, updatedStr, lastActivityStr, fetchedStr string
		var reviewDecision, myRole, ciStatus, category sql.NullString
		var reviewersJSON, pendingReviewersJSON sql.NullString
		var state, headSHA, agentSessionID, agentStatus, agentCategory, agentHeadSHA, agentSummary sql.NullString

		err := rows.Scan(&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author, &pr.Draft,
			&createdStr, &updatedStr, &reviewDecision, &myRole,
			&reviewersJSON, &pendingReviewersJSON,
			&pr.CommentCount, &pr.UnresolvedThreadCount, &lastActivityStr,
			&ciStatus, &category, &fetchedStr,
			&state, &headSHA, &agentSessionID, &agentStatus, &agentCategory, &agentHeadSHA, &agentSummary)
		if err != nil {
			return nil, err
		}

		pr.CreatedAt = ParseTime(createdStr)
		pr.UpdatedAt = ParseTime(updatedStr)
		pr.LastActivityAt = ParseTime(lastActivityStr)
		pr.FetchedAt = ParseTime(fetchedStr)
		pr.ReviewDecision = reviewDecision.String
		pr.MyRole = myRole.String
		pr.CIStatus = ciStatus.String
		pr.Category = category.String
		pr.State = state.String
		pr.HeadSHA = headSHA.String
		pr.AgentSessionID = agentSessionID.String
		pr.AgentStatus = agentStatus.String
		pr.AgentCategory = agentCategory.String
		pr.AgentHeadSHA = agentHeadSHA.String
		pr.AgentSummary = agentSummary.String

		if reviewersJSON.Valid {
			if err := json.Unmarshal([]byte(reviewersJSON.String), &pr.ReviewerLogins); err != nil {
				log.Printf("WARNING: corrupt reviewer_logins JSON for PR %s: %v", pr.ID, err)
			}
		}
		if pendingReviewersJSON.Valid {
			if err := json.Unmarshal([]byte(pendingReviewersJSON.String), &pr.PendingReviewerLogins); err != nil {
				log.Printf("WARNING: corrupt pending_reviewer_logins JSON for PR %s: %v", pr.ID, err)
			}
		}

		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

func dbLoadGeneratedAt(db *sql.DB) (time.Time, error) {
	var val sql.NullString
	err := db.QueryRow(`SELECT value FROM cc_meta WHERE key = 'generated_at'`).Scan(&val)
	if err != nil || !val.Valid {
		return time.Time{}, err
	}
	return ParseTime(val.String), nil
}

// DBLoadBookmarks loads all bookmarked sessions from the database.
func DBLoadBookmarks(db *sql.DB) ([]Session, error) {
	rows, err := db.Query(`SELECT session_id, project, repo, branch, label, summary, created_at, worktree_path, source_repo
		FROM cc_bookmarks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sid, createdStr string
		var project, repo, branch, label, summary, worktreePath, sourceRepo sql.NullString
		if err := rows.Scan(&sid, &project, &repo, &branch, &label, &summary, &createdStr, &worktreePath, &sourceRepo); err != nil {
			return nil, err
		}
		sessions = append(sessions, Session{
			SessionID:    sid,
			Project:      project.String,
			Repo:         repo.String,
			Branch:       branch.String,
			Summary:      summary.String,
			Created:      ParseTime(createdStr),
			Type:         SessionBookmark,
			WorktreePath: worktreePath.String,
			SourceRepo:   sourceRepo.String,
		})
	}
	return sessions, rows.Err()
}

// DBLoadPaths loads all learned paths from the database.
func DBLoadPaths(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT path FROM cc_learned_paths ORDER BY sort_order ASC, added_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if paths == nil {
		paths = []string{}
	}
	return paths, rows.Err()
}

// DBLoadPathsFull loads all learned paths with full metadata.
func DBLoadPathsFull(d *sql.DB) ([]PathEntry, error) {
	rows, err := d.Query(`SELECT path, description, added_at, sort_order FROM cc_learned_paths ORDER BY sort_order ASC, added_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []PathEntry
	for rows.Next() {
		var e PathEntry
		var addedAt string
		if err := rows.Scan(&e.Path, &e.Description, &addedAt, &e.SortOrder); err != nil {
			return nil, err
		}
		e.AddedAt = ParseTime(addedAt)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []PathEntry{}
	}
	return entries, rows.Err()
}

// DBLoadSourceSync loads the sync status for a given data source.
func DBLoadSourceSync(d *sql.DB, source string) (*SourceSync, error) {
	var ss SourceSync
	var lastSuccess, lastError sql.NullString
	var updatedAt string
	err := d.QueryRow(`SELECT source, last_success, last_error, updated_at FROM cc_source_sync WHERE source = ?`, source).
		Scan(&ss.Source, &lastSuccess, &lastError, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastSuccess.Valid && lastSuccess.String != "" {
		t := ParseTime(lastSuccess.String)
		ss.LastSuccess = &t
	}
	ss.LastError = lastError.String
	ss.UpdatedAt = ParseTime(updatedAt)
	return &ss, nil
}

// DBLoadAllSourceSync loads sync status for all tracked sources.
func DBLoadAllSourceSync(d *sql.DB) (map[string]*SourceSync, error) {
	rows, err := d.Query(`SELECT source, last_success, last_error, updated_at FROM cc_source_sync`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*SourceSync)
	for rows.Next() {
		var ss SourceSync
		var lastSuccess, lastError sql.NullString
		var updatedAt string
		if err := rows.Scan(&ss.Source, &lastSuccess, &lastError, &updatedAt); err != nil {
			return nil, err
		}
		if lastSuccess.Valid && lastSuccess.String != "" {
			t := ParseTime(lastSuccess.String)
			ss.LastSuccess = &t
		}
		ss.LastError = lastError.String
		ss.UpdatedAt = ParseTime(updatedAt)
		result[ss.Source] = &ss
	}
	return result, rows.Err()
}

// DBLoadTodoByDisplayID loads a single todo by its display_id.
// Returns nil, nil if no todo with that display_id exists.
func DBLoadTodoByID(db *sql.DB, id string) (*Todo, error) {
	var t Todo
	var createdStr string
	var completedStr sql.NullString
	var sourceRef, ctx, detail, who, projDir, launchMode, due, effort, sessionID, proposedPrompt, sessionSummary, sessionLogPath sql.NullString
	var sourceContext, sourceContextAt sql.NullString
	var focus, starred int

	err := db.QueryRow(`SELECT id, COALESCE(display_id, 0), title, status, source, source_ref, context, detail,
		who_waiting, project_dir, launch_mode, due, effort, session_id, proposed_prompt,
		session_summary, session_log_path, source_context, source_context_at,
		COALESCE(focus, 0), COALESCE(starred, 0),
		created_at, completed_at
		FROM cc_todos WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&t.ID, &t.DisplayID, &t.Title, &t.Status, &t.Source,
			&sourceRef, &ctx, &detail, &who, &projDir, &launchMode, &due, &effort, &sessionID,
			&proposedPrompt, &sessionSummary, &sessionLogPath, &sourceContext, &sourceContextAt,
			&focus, &starred,
			&createdStr, &completedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t.SourceRef = sourceRef.String
	t.Context = ctx.String
	t.Detail = detail.String
	t.WhoWaiting = who.String
	t.ProjectDir = projDir.String
	t.LaunchMode = launchMode.String
	t.Due = due.String
	t.Effort = effort.String
	t.SessionID = sessionID.String
	t.ProposedPrompt = proposedPrompt.String
	t.SessionSummary = sessionSummary.String
	t.SessionLogPath = sessionLogPath.String
	t.SourceContext = sourceContext.String
	t.SourceContextAt = sourceContextAt.String
	t.Focus = focus != 0
	t.Starred = starred != 0
	t.CreatedAt = ParseTime(createdStr)
	if completedStr.Valid {
		ct := ParseTime(completedStr.String)
		t.CompletedAt = &ct
	}
	return &t, nil
}

func DBLoadTodoByDisplayID(db *sql.DB, displayID int) (*Todo, error) {
	var t Todo
	var createdStr string
	var completedStr sql.NullString
	var sourceRef, ctx, detail, who, projDir, launchMode, due, effort, sessionID, proposedPrompt, sessionSummary, sessionLogPath sql.NullString
	var sourceContext, sourceContextAt sql.NullString
	var focus, starred int

	err := db.QueryRow(`SELECT id, COALESCE(display_id, 0), title, status, source, source_ref, context, detail,
		who_waiting, project_dir, launch_mode, due, effort, session_id, proposed_prompt,
		session_summary, session_log_path, source_context, source_context_at,
		COALESCE(focus, 0), COALESCE(starred, 0),
		created_at, completed_at
		FROM cc_todos WHERE display_id = ? AND deleted_at IS NULL`, displayID).
		Scan(&t.ID, &t.DisplayID, &t.Title, &t.Status, &t.Source,
			&sourceRef, &ctx, &detail, &who, &projDir, &launchMode, &due, &effort, &sessionID,
			&proposedPrompt, &sessionSummary, &sessionLogPath, &sourceContext, &sourceContextAt,
			&focus, &starred,
			&createdStr, &completedStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	t.SourceRef = sourceRef.String
	t.Context = ctx.String
	t.Detail = detail.String
	t.WhoWaiting = who.String
	t.ProjectDir = projDir.String
	t.LaunchMode = launchMode.String
	t.Due = due.String
	t.Effort = effort.String
	t.SessionID = sessionID.String
	t.ProposedPrompt = proposedPrompt.String
	t.SessionSummary = sessionSummary.String
	t.SessionLogPath = sessionLogPath.String
	t.SourceContext = sourceContext.String
	t.SourceContextAt = sourceContextAt.String
	t.Focus = focus != 0
	t.Starred = starred != 0
	t.CreatedAt = ParseTime(createdStr)
	if completedStr.Valid {
		ct := ParseTime(completedStr.String)
		t.CompletedAt = &ct
	}
	return &t, nil
}

// DBLoadMerges loads all merge records from the database.
func DBLoadMerges(database *sql.DB) ([]TodoMerge, error) {
	rows, err := database.Query(`SELECT synthesis_id, original_id, vetoed, created_at FROM cc_todo_merges`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var merges []TodoMerge
	for rows.Next() {
		var m TodoMerge
		var vetoed int
		if err := rows.Scan(&m.SynthesisID, &m.OriginalID, &vetoed, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Vetoed = vetoed != 0
		merges = append(merges, m)
	}
	return merges, rows.Err()
}

// ---------------------------------------------------------------------------
// Read methods -- Sessions (daemon registry)
// ---------------------------------------------------------------------------

// DBLoadSessions loads all sessions ordered by registered_at DESC.
func DBLoadSessions(d *sql.DB) ([]SessionRecord, error) {
	return dbLoadSessionsWhere(d, "1=1")
}

// DBLoadVisibleSessions loads sessions where state is "active" or "ended",
// ordered by registered_at DESC.
func DBLoadVisibleSessions(d *sql.DB) ([]SessionRecord, error) {
	return dbLoadSessionsWhere(d, "state IN ('active', 'ended')")
}

func dbLoadSessionsWhere(d *sql.DB, where string) ([]SessionRecord, error) {
	rows, err := d.Query(`SELECT session_id, topic, pid, project, repo, branch,
		worktree_path, state, registered_at, ended_at
		FROM cc_sessions WHERE ` + where + ` ORDER BY registered_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var s SessionRecord
		var topic, project, repo, branch, worktreePath, endedAt sql.NullString
		if err := rows.Scan(&s.SessionID, &topic, &s.PID, &project, &repo, &branch,
			&worktreePath, &s.State, &s.RegisteredAt, &endedAt); err != nil {
			return nil, err
		}
		s.Topic = topic.String
		s.Project = project.String
		s.Repo = repo.String
		s.Branch = branch.String
		s.WorktreePath = worktreePath.String
		s.EndedAt = endedAt.String
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// DBLoadIgnoredRepos returns all repos in the ignore list, sorted alphabetically.
func DBLoadIgnoredRepos(d *sql.DB) ([]string, error) {
	rows, err := d.Query(`SELECT repo FROM cc_ignored_repos ORDER BY repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

// DBLoadIgnoredPRs returns all ignored but non-archived PRs (for settings pane).
func DBLoadIgnoredPRs(d *sql.DB) ([]PullRequest, error) {
	rows, err := d.Query(`SELECT id, repo, number, title, url, author
		FROM cc_pull_requests WHERE ignored = 1 AND state != 'archived'
		ORDER BY last_activity_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prs []PullRequest
	for rows.Next() {
		var pr PullRequest
		if err := rows.Scan(&pr.ID, &pr.Repo, &pr.Number, &pr.Title, &pr.URL, &pr.Author); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

// DBIsEmpty returns true if no todos exist in the database yet.
func DBIsEmpty(db *sql.DB) bool {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM cc_todos WHERE deleted_at IS NULL`).Scan(&count)
	return err != nil || count == 0
}

// ---------------------------------------------------------------------------
// Read methods -- Archived Sessions
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Read methods -- Todo bookings (focus & star scheduling)
// ---------------------------------------------------------------------------

// DBGetBookingsForTodo returns all bookings for a given todo ordered by start_time.
func DBGetBookingsForTodo(db *sql.DB, todoID string) ([]TodoBooking, error) {
	return dbQueryBookings(db, `SELECT id, todo_id, event_id, start_time, end_time, duration_min, created_at
		FROM cc_todo_bookings WHERE todo_id = ? ORDER BY start_time ASC`, todoID)
}

// DBGetFutureBookingsForTodo returns only future bookings for a given todo.
func DBGetFutureBookingsForTodo(db *sql.DB, todoID string) ([]TodoBooking, error) {
	return dbQueryBookings(db, `SELECT id, todo_id, event_id, start_time, end_time, duration_min, created_at
		FROM cc_todo_bookings WHERE todo_id = ? AND start_time > strftime('%Y-%m-%dT%H:%M:%SZ', 'now') ORDER BY start_time ASC`, todoID)
}

func dbQueryBookings(db *sql.DB, query string, args ...interface{}) ([]TodoBooking, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bookings []TodoBooking
	for rows.Next() {
		var b TodoBooking
		var startStr, endStr, createdStr string
		if err := rows.Scan(&b.ID, &b.TodoID, &b.EventID, &startStr, &endStr, &b.DurationMin, &createdStr); err != nil {
			return nil, err
		}
		b.StartTime = ParseTime(startStr)
		b.EndTime = ParseTime(endStr)
		b.CreatedAt = ParseTime(createdStr)
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

// DBLoadArchivedSessions loads all archived sessions, most recent first.
func DBLoadArchivedSessions(db *sql.DB) ([]ArchivedSession, error) {
	rows, err := db.Query(`SELECT session_id, topic, project, repo, branch, worktree_path, registered_at, ended_at
		FROM cc_archived_sessions ORDER BY ended_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []ArchivedSession
	for rows.Next() {
		var s ArchivedSession
		if err := rows.Scan(&s.SessionID, &s.Topic, &s.Project, &s.Repo, &s.Branch, &s.WorktreePath, &s.RegisteredAt, &s.EndedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
