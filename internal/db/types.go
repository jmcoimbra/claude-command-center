package db

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CommandCenter struct {
	GeneratedAt    time.Time       `json:"generated_at"`
	Calendar       CalendarData    `json:"calendar"`
	Todos          []Todo          `json:"todos"`
	Merges         []TodoMerge     `json:"merges,omitempty"`
	PullRequests   []PullRequest   `json:"pull_requests,omitempty"`
	Suggestions    Suggestions     `json:"suggestions"`
	PendingActions []PendingAction `json:"pending_actions"`
	Warnings       []Warning       `json:"warnings,omitempty"`
}

type PullRequest struct {
	ID                    string    `json:"id"`                      // "owner/repo#number"
	Repo                  string    `json:"repo"`                    // "owner/repo"
	Number                int       `json:"number"`
	Title                 string    `json:"title"`
	URL                   string    `json:"url"`
	Author                string    `json:"author"`
	Draft                 bool      `json:"draft"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	ReviewDecision        string    `json:"review_decision"`         // "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED"
	MyRole                string    `json:"my_role"`                 // "author", "reviewer", "both"
	ReviewerLogins        []string  `json:"reviewer_logins"`         // JSON-serialized in DB
	PendingReviewerLogins []string  `json:"pending_reviewer_logins"` // JSON-serialized in DB
	CommentCount          int       `json:"comment_count"`
	UnresolvedThreadCount int       `json:"unresolved_thread_count"`
	LastActivityAt        time.Time `json:"last_activity_at"`
	CIStatus              string    `json:"ci_status"`               // "success", "failure", "pending"
	Category              string    `json:"category"`                // computed: "waiting", "respond", "review", "stale"
	HeadSHA               string    `json:"head_sha"`                // commit SHA of the PR head
	FetchedAt             time.Time `json:"fetched_at"`

	// Agent tracking fields
	State          string `json:"state"`            // "open" or "archived"
	AgentSessionID string `json:"agent_session_id"` // Claude session UUID
	AgentStatus    string `json:"agent_status"`     // "", "pending", "running", "completed", "failed"
	AgentCategory  string `json:"agent_category"`   // "review" or "respond"
	AgentHeadSHA   string `json:"agent_head_sha"`   // SHA when agent last ran
	AgentSummary   string `json:"agent_summary"`    // Summary from agent completion
}

type Warning struct {
	Source  string    `json:"source"`
	Message string   `json:"message"`
	At      time.Time `json:"at"`
}

type CalendarData struct {
	Today    []CalendarEvent `json:"today"`
	Tomorrow []CalendarEvent `json:"tomorrow"`
}

type CalendarConflict struct {
	EventA string
	EventB string
	Day    string // "today" or "tomorrow"
	Start  time.Time
	End    time.Time
}

// FindConflicts returns all pairs of overlapping events across today and tomorrow.
// Skips declined, all-day, and already-ended events to reduce noise.
func (cal *CalendarData) FindConflicts() []CalendarConflict {
	now := time.Now()
	var conflicts []CalendarConflict
	conflicts = append(conflicts, findOverlaps(cal.Today, "today", now)...)
	conflicts = append(conflicts, findOverlaps(cal.Tomorrow, "tomorrow", now)...)
	return conflicts
}

func findOverlaps(events []CalendarEvent, day string, now time.Time) []CalendarConflict {
	var real []CalendarEvent
	for _, ev := range events {
		if !ev.Declined && !ev.AllDay && ev.End.After(now) {
			real = append(real, ev)
		}
	}

	var conflicts []CalendarConflict
	for i := 0; i < len(real); i++ {
		for j := i + 1; j < len(real); j++ {
			a, b := real[i], real[j]
			if a.Start.Before(b.End) && b.Start.Before(a.End) {
				start := a.Start
				if b.Start.After(start) {
					start = b.Start
				}
				end := a.End
				if b.End.Before(end) {
					end = b.End
				}
				conflicts = append(conflicts, CalendarConflict{
					EventA: a.Title,
					EventB: b.Title,
					Day:    day,
					Start:  start,
					End:    end,
				})
			}
		}
	}
	return conflicts
}

type CalendarEvent struct {
	Title      string    `json:"title"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	AllDay     bool      `json:"all_day,omitempty"`
	Declined   bool      `json:"declined,omitempty"`
	CalendarID string    `json:"calendar_id,omitempty"`
}

// ---------------------------------------------------------------------------
// Todo status constants
// ---------------------------------------------------------------------------

const (
	StatusNew       = "new"
	StatusBacklog   = "backlog"
	StatusEnqueued  = "enqueued"
	StatusRunning   = "running"
	StatusBlocked   = "blocked"
	StatusReview    = "review"
	StatusFailed    = "failed"
	StatusCompleted = "completed"
	StatusDismissed = "dismissed"
)

// validTransitions maps each status to the set of non-universal transitions
// it supports. Universal exits (completed, dismissed) are always valid from
// any state and are handled separately in ValidTransition.
var validTransitions = map[string]map[string]bool{
	StatusNew:       {StatusBacklog: true},
	StatusBacklog:   {StatusEnqueued: true, StatusRunning: true},
	StatusEnqueued:  {StatusRunning: true, StatusBacklog: true},
	StatusRunning:   {StatusBlocked: true, StatusReview: true, StatusFailed: true, StatusBacklog: true},
	StatusBlocked:   {StatusRunning: true, StatusBacklog: true},
	StatusReview:    {StatusBacklog: true, StatusEnqueued: true, StatusRunning: true},
	StatusFailed:    {StatusBacklog: true, StatusEnqueued: true, StatusRunning: true},
	StatusCompleted: {StatusBacklog: true},
	StatusDismissed: {StatusBacklog: true},
}

// ValidTransition returns true if the state machine allows moving from → to.
func ValidTransition(from, to string) bool {
	// Universal exits: any state → completed or dismissed.
	if to == StatusCompleted || to == StatusDismissed {
		return true
	}
	if allowed, ok := validTransitions[from]; ok {
		return allowed[to]
	}
	return false
}

// IsTerminalStatus returns true for completed and dismissed.
func IsTerminalStatus(status string) bool {
	return status == StatusCompleted || status == StatusDismissed
}

// IsAgentStatus returns true for statuses where an agent is involved
// (enqueued, running, blocked).
func IsAgentStatus(status string) bool {
	return status == StatusEnqueued || status == StatusRunning || status == StatusBlocked
}

type Todo struct {
	ID          string     `json:"id"`
	DisplayID   int        `json:"display_id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Source      string     `json:"source"`
	SourceRef   string     `json:"source_ref"`
	Context     string     `json:"context"`
	Detail      string     `json:"detail"`
	WhoWaiting  string     `json:"who_waiting"`
	ProjectDir  string     `json:"project_dir"`
	LaunchMode  string     `json:"launch_mode,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	Due            string     `json:"due"`
	Effort         string     `json:"effort"`
	ProposedPrompt string     `json:"proposed_prompt,omitempty"`
	SessionSummary string     `json:"session_summary,omitempty"`
	SessionLogPath string     `json:"session_log_path,omitempty"`
	SourceContext    string     `json:"source_context,omitempty"`
	SourceContextAt  string     `json:"source_context_at,omitempty"`
	Starred        bool       `json:"starred,omitempty"`
	Focused        bool       `json:"focused,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	MergeInto      string     `json:"-"` // transient — not persisted to DB
}

// TodoMerge tracks which original todos have been merged into a synthesis todo.
type TodoMerge struct {
	SynthesisID string `json:"synthesis_id"`
	OriginalID  string `json:"original_id"`
	Vetoed      bool   `json:"vetoed"`
	CreatedAt   string `json:"created_at"`
}

// DBGetOriginalIDs returns the non-vetoed original IDs for a synthesis.
func DBGetOriginalIDs(merges []TodoMerge, synthesisID string) []string {
	var ids []string
	for _, m := range merges {
		if m.SynthesisID == synthesisID && !m.Vetoed {
			ids = append(ids, m.OriginalID)
		}
	}
	return ids
}

// WerePreviouslyMergedAndVetoed checks if two IDs were ever in the same
// synthesis and one of them was vetoed out. Prevents re-merging a
// specific pair while allowing each ID to merge with unrelated todos.
func WerePreviouslyMergedAndVetoed(merges []TodoMerge, idA, idB string) bool {
	synthGroups := make(map[string][]TodoMerge)
	for _, m := range merges {
		synthGroups[m.SynthesisID] = append(synthGroups[m.SynthesisID], m)
	}
	for _, group := range synthGroups {
		hasA, hasB, hasVeto := false, false, false
		for _, m := range group {
			if m.OriginalID == idA {
				hasA = true
				if m.Vetoed {
					hasVeto = true
				}
			}
			if m.OriginalID == idB {
				hasB = true
				if m.Vetoed {
					hasVeto = true
				}
			}
		}
		if hasA && hasB && hasVeto {
			return true
		}
	}
	return false
}

// FindTodo returns a pointer to the todo with the given ID, or nil.
func (cc *CommandCenter) FindTodo(id string) *Todo {
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			return &cc.Todos[i]
		}
	}
	return nil
}

type Suggestions struct {
	Focus         string            `json:"focus"`
	RankedTodoIDs []string          `json:"ranked_todo_ids"`
	Reasons       map[string]string `json:"reasons"`
}

type PendingAction struct {
	Type            string    `json:"type"`
	TodoID          string    `json:"todo_id"`
	DurationMinutes int       `json:"duration_minutes"`
	RequestedAt     time.Time `json:"requested_at"`
}

// SourceSync tracks the last sync status for a data source.
type SourceSync struct {
	Source      string     `json:"source"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// ID generation
// ---------------------------------------------------------------------------

func GenID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ---------------------------------------------------------------------------
// Time helpers
// ---------------------------------------------------------------------------

func RelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func DueUrgency(due string) string {
	if due == "" {
		return "none"
	}
	d, err := time.ParseInLocation("2006-01-02", due, time.Local)
	if err != nil {
		return "none"
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	dayAfter := today.AddDate(0, 0, 2)

	if d.Before(today) {
		return "overdue"
	}
	if d.Before(dayAfter) {
		return "soon"
	}
	return "later"
}

func FormatDueLabel(due string) string {
	if due == "" {
		return ""
	}
	d, err := time.Parse("2006-01-02", due)
	if err != nil {
		return ""
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	diff := int(d.Sub(today).Hours() / 24)
	switch {
	case diff < 0:
		return "overdue"
	case diff == 0:
		return "due today"
	case diff == 1:
		return "due tomorrow"
	default:
		return "due " + d.Format("Mon")
	}
}

// ---------------------------------------------------------------------------
// CommandCenter mutation methods
// ---------------------------------------------------------------------------

func (cc *CommandCenter) CompleteTodo(id string) {
	now := time.Now()
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			cc.Todos[i].Status = "completed"
			cc.Todos[i].CompletedAt = &now
			return
		}
	}
}

func (cc *CommandCenter) RestoreTodo(id, status string, completedAt *time.Time) {
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			cc.Todos[i].Status = status
			cc.Todos[i].CompletedAt = completedAt
			return
		}
	}
}

func (cc *CommandCenter) AcceptTodo(id string) {
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			cc.Todos[i].Status = StatusBacklog
			return
		}
	}
}

func (cc *CommandCenter) AddTodo(title string) *Todo {
	// Compute next display_id from in-memory todos so the detail view
	// shows the correct ID before the next DB reload.
	maxDisplayID := 0
	for _, existing := range cc.Todos {
		if existing.DisplayID > maxDisplayID {
			maxDisplayID = existing.DisplayID
		}
	}
	t := Todo{
		ID:        GenID(),
		DisplayID: maxDisplayID + 1,
		Title:     title,
		Status:    StatusBacklog,
		Source:    "manual",
		CreatedAt: time.Now(),
	}
	cc.Todos = append(cc.Todos, t)
	return &cc.Todos[len(cc.Todos)-1]
}

func (cc *CommandCenter) RemoveTodo(id string) {
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			cc.Todos[i].Status = "dismissed"
			return
		}
	}
}

func (cc *CommandCenter) DeferTodo(id string) {
	idx := -1
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}

	todo := cc.Todos[idx]
	cc.Todos = append(cc.Todos[:idx], cc.Todos[idx+1:]...)

	// Insert after the last active (non-terminal) todo.
	// Terminal items can appear anywhere in sort order (they keep their
	// original sort_order when completed), so we can't just look for the
	// first terminal item.
	insertAt := 0
	for i, t := range cc.Todos {
		if !IsTerminalStatus(t.Status) {
			insertAt = i + 1
		}
	}

	cc.Todos = append(cc.Todos[:insertAt], append([]Todo{todo}, cc.Todos[insertAt:]...)...)
}

func (cc *CommandCenter) PromoteTodo(id string) {
	idx := -1
	for i := range cc.Todos {
		if cc.Todos[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}

	todo := cc.Todos[idx]
	cc.Todos = append(cc.Todos[:idx], cc.Todos[idx+1:]...)
	cc.Todos = append([]Todo{todo}, cc.Todos...)
}

// SwapTodos swaps two todos by their slice indices.
func (cc *CommandCenter) SwapTodos(i, j int) {
	if i < 0 || j < 0 || i >= len(cc.Todos) || j >= len(cc.Todos) {
		return
	}
	cc.Todos[i], cc.Todos[j] = cc.Todos[j], cc.Todos[i]
}

// VisibleTodos returns todos not hidden by a non-vetoed merge.
func (cc *CommandCenter) VisibleTodos() []Todo {
	hidden := make(map[string]bool)
	for _, m := range cc.Merges {
		if !m.Vetoed {
			hidden[m.OriginalID] = true
		}
	}
	var out []Todo
	for _, t := range cc.Todos {
		if !hidden[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

func (cc *CommandCenter) ActiveTodos() []Todo {
	var out []Todo
	for _, t := range cc.VisibleTodos() {
		if !IsTerminalStatus(t.Status) {
			out = append(out, t)
		}
	}
	return out
}

func (cc *CommandCenter) CompletedTodos() []Todo {
	var out []Todo
	for _, t := range cc.Todos {
		if IsTerminalStatus(t.Status) {
			out = append(out, t)
		}
	}
	return out
}

func (cc *CommandCenter) AddPendingBooking(todoID string, durationMinutes int) {
	cc.PendingActions = append(cc.PendingActions, PendingAction{
		Type:            "booking",
		TodoID:          todoID,
		DurationMinutes: durationMinutes,
		RequestedAt:     time.Now(),
	})
}

// SessionRecord represents a row in the cc_sessions table (daemon session registry).
type SessionRecord struct {
	SessionID    string `json:"session_id"`
	Topic        string `json:"topic"`
	PID          int    `json:"pid"`
	Project      string `json:"project"`
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
	WorktreePath string `json:"worktree_path"`
	State        string `json:"state"` // active | ended | archived
	RegisteredAt string `json:"registered_at"`
	EndedAt      string `json:"ended_at,omitempty"`
}

// PathEntry holds full metadata for a learned path row.
type PathEntry struct {
	Path        string    `json:"path"`
	Description string    `json:"description"`
	AddedAt     time.Time `json:"added_at"`
	SortOrder   int       `json:"sort_order"`
}

// ---------------------------------------------------------------------------
// Learned-paths CRUD (file-based)
// ---------------------------------------------------------------------------

func LoadPaths(file string) ([]string, error) {
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var paths []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			paths = append(paths, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if paths == nil {
		paths = []string{}
	}
	return paths, nil
}

func SavePaths(file string, paths []string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, p := range paths {
		if _, err := fmt.Fprintln(f, p); err != nil {
			return err
		}
	}
	return nil
}

func AddPath(paths []string, newPath string) []string {
	for _, p := range paths {
		if p == newPath {
			return paths
		}
	}
	return append(paths, newPath)
}

func RemovePath(paths []string, target string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != target {
			out = append(out, p)
		}
	}
	return out
}

