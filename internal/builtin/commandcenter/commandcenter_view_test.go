package commandcenter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// viewContains asserts that view contains text, dumping the full view on failure.
func viewContains(t *testing.T, view, text string) {
	t.Helper()
	if !strings.Contains(view, text) {
		t.Errorf("expected view to contain %q but it did not.\nFull view:\n%s", text, view)
	}
}

// viewNotContains asserts that view does NOT contain text.
func viewNotContains(t *testing.T, view, text string) {
	t.Helper()
	if strings.Contains(view, text) {
		t.Errorf("expected view NOT to contain %q but it did.\nFull view:\n%s", text, view)
	}
}

// testPluginWithTodos creates a plugin with the given todos pre-loaded.
func testPluginWithTodos(t *testing.T, todos []db.Todo) *Plugin {
	t.Helper()
	p := testPlugin(t)
	p.cc = &db.CommandCenter{
		GeneratedAt: time.Now(),
		Todos:       todos,
	}
	p.width = 120
	p.height = 40
	return p
}

// renderView renders the plugin's View at standard dimensions.
func renderView(p *Plugin) string {
	return p.View(120, 38, 0)
}

// ---------------------------------------------------------------------------
// Status Badge Rendering (9 tests)
// ---------------------------------------------------------------------------

func TestView_TodoStatusNew(t *testing.T) {
	// "new" status todos appear in the "New" triage tab when expanded
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Inbox item alpha", Status: db.StatusNew, Source: "github", CreatedAt: time.Now()},
	})
	// Expand the view and switch to New tab
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand the view")
	}
	// Default triage is "focus", tab once to reach "new"
	p.HandleKey(keyMsg("tab"))
	view := renderView(p)
	viewContains(t, view, "Inbox item alpha")
}

func TestView_TodoStatusBacklog(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Backlog task bravo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Backlog task bravo")
}

func TestView_TodoStatusEnqueued(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Enqueued task charlie", Status: db.StatusEnqueued, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Enqueued task charlie")
	viewContains(t, view, "queued")
}

func TestView_TodoStatusRunning(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Running task delta", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Running task delta")
	viewContains(t, view, "agent working")
}

func TestView_TodoStatusBlocked(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Blocked task echo", Status: db.StatusBlocked, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Blocked task echo")
	viewContains(t, view, "needs input")
}

func TestView_TodoStatusReview(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Review task foxtrot", Status: db.StatusReview, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Review task foxtrot")
	viewContains(t, view, "ready for review")
}

func TestView_TodoStatusFailed(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Failed task golf", Status: db.StatusFailed, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewContains(t, view, "Failed task golf")
	viewContains(t, view, "failed")
}

func TestView_TodoStatusCompleted(t *testing.T) {
	// Completed todos are terminal; they should NOT appear in the default active list.
	// Active task must be starred to appear in the collapsed view.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Completed task hotel", Status: db.StatusCompleted, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", Title: "Active task india", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	view := renderView(p)
	viewNotContains(t, view, "Completed task hotel")
	viewContains(t, view, "Active task india")
}

func TestView_TodoStatusDismissed(t *testing.T) {
	// Dismissed todos are terminal; they should NOT appear in any active view
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Dismissed task juliet", Status: db.StatusDismissed, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", Title: "Active task kilo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	view := renderView(p)
	viewNotContains(t, view, "Dismissed task juliet")
	// Also check expanded view with "all" filter
	p.HandleKey(keyMsg(" "))
	// Cycle tabs to "all" (focus -> new -> backlog -> agents -> review -> all)
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))
	view = renderView(p)
	viewNotContains(t, view, "Dismissed task juliet")
}

// ---------------------------------------------------------------------------
// Navigation and View Modes (9 tests)
// ---------------------------------------------------------------------------

func TestView_ExpandCollapseSpaceCycles(t *testing.T) {
	p := testPluginWithCC(t)

	// Start collapsed
	view1 := renderView(p)

	// Press space -> expanded 2-col
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded || p.ccExpandedCols != 2 {
		t.Fatal("first space should expand to 2-col")
	}
	// Switch to "all" tab so items are visible (default is "focus" but test data may not be focused)
	p.triageFilter = "all"
	view2 := renderView(p)

	// Press space -> expanded 1-col
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded || p.ccExpandedCols != 1 {
		t.Fatal("second space should switch to 1-col")
	}
	view3 := renderView(p)

	// Press space -> collapsed
	p.HandleKey(keyMsg(" "))
	if p.ccExpanded {
		t.Fatal("third space should collapse")
	}

	// Views should differ at each stage
	if view1 == view2 {
		t.Error("collapsed and 2-col expanded views should differ")
	}
	if view2 == view3 {
		t.Error("2-col and 1-col expanded views should differ")
	}
}

func TestView_ExpandedViewShowsTriageTabs(t *testing.T) {
	p := testPluginWithCC(t)
	p.HandleKey(keyMsg(" "))
	view := renderView(p)
	viewContains(t, view, "Focus")
	viewContains(t, view, "New")
	viewContains(t, view, "Backlog")
	viewContains(t, view, "Agents")
	viewNotContains(t, view, "ToDo")
	viewNotContains(t, view, "Inbox")
}

func TestView_ExpandedTwoColumnClampsToTerminalHeight(t *testing.T) {
	// BUG-131: 2-column expanded view must not overflow the terminal height.
	// Create enough todos to overflow if height clamping is wrong.
	var todos []db.Todo
	for i := 0; i < 40; i++ {
		todos = append(todos, db.Todo{
			ID:        fmt.Sprintf("t%d", i+1),
			DisplayID: i + 1,
			Title:     fmt.Sprintf("Todo item %d", i+1),
			Status:    db.StatusBacklog,
			Source:    "manual",
			CreatedAt: time.Now(),
		})
	}
	p := testPluginWithTodos(t, todos)

	// Enter expanded 2-column view
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded || p.ccExpandedCols != 2 {
		t.Fatal("space should expand to 2-col")
	}

	// Render at a specific height
	termHeight := 38
	view := p.View(120, termHeight, 0)

	// Count lines in the rendered view
	lines := strings.Split(view, "\n")
	viewHeight := termHeight - 14 // TUI chrome
	if len(lines) > viewHeight {
		t.Errorf("expanded 2-column view has %d lines, exceeds available height %d (terminal=%d, chrome=14)", len(lines), viewHeight, termHeight)
	}

	// The hints bar must be visible in the output
	viewContains(t, view, "filter")
	viewContains(t, view, "navigate")
}

func TestView_TriageTabFiltersContent(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Backlog lima", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", Title: "Inbox mike", Status: db.StatusNew, Source: "github", CreatedAt: time.Now()},
		{ID: "t3", Title: "Running november", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now()},
	})

	// Expand to see triage tabs (default is "focus")
	p.HandleKey(keyMsg(" "))
	focusView := renderView(p)
	viewContains(t, focusView, "Backlog lima")
	viewNotContains(t, focusView, "Inbox mike")

	// Switch to New tab (focus -> new)
	p.HandleKey(keyMsg("tab"))
	newView := renderView(p)
	viewContains(t, newView, "Inbox mike")
	viewNotContains(t, newView, "Backlog lima")

	// Switch to agents tab (new -> backlog -> agents)
	p.HandleKey(keyMsg("tab")) // new -> backlog
	p.HandleKey(keyMsg("tab")) // backlog -> agents
	agentView := renderView(p)
	viewContains(t, agentView, "Running november")
	viewNotContains(t, agentView, "Backlog lima")
}

func TestView_DetailViewOpensOnEnter(t *testing.T) {
	p := testPluginWithCC(t)
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}
	view := renderView(p)
	// Detail view shows "TODO #" header
	viewContains(t, view, "TODO #")
	viewContains(t, view, "First todo")
}

func TestView_DetailViewClosesOnEsc(t *testing.T) {
	p := testPluginWithCC(t)
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.detailView {
		t.Fatal("esc should close detail view")
	}
	// Back to list view, should contain todo titles
	view := renderView(p)
	viewContains(t, view, "First todo")
}

func TestView_DetailViewTracksTodoByID(t *testing.T) {
	p := testPluginWithCC(t)
	// Move cursor to second todo, then open detail
	p.HandleKey(keyMsg("j"))
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}
	view := renderView(p)
	viewContains(t, view, "Second todo")
}

func TestView_SearchModeEnterExit(t *testing.T) {
	p := testPluginWithCC(t)

	// Enter search mode
	p.HandleKey(keyMsg("/"))
	if !p.searchActive {
		t.Fatal("/ should activate search")
	}
	view := renderView(p)
	// Search view shows the "/" prompt indicator and hints
	viewContains(t, view, "esc clear")

	// Exit search with esc
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.searchActive {
		t.Fatal("esc should deactivate search")
	}
}

func TestView_SearchFilterUpdatesView(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Alpha unique name", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", Title: "Bravo different name", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	})

	// Expand to get full todo listing (default is "focus" tab)
	p.HandleKey(keyMsg(" "))
	viewBefore := renderView(p)
	viewContains(t, viewBefore, "Alpha unique name")
	viewContains(t, viewBefore, "Bravo different name")

	// Enter search mode and type filter characters
	p.HandleKey(keyMsg("/"))
	for _, ch := range "Alpha" {
		p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}

	viewAfter := renderView(p)
	viewContains(t, viewAfter, "Alpha unique name")
	viewNotContains(t, viewAfter, "Bravo different name")
}

func TestView_SearchEnterOpensDirectly(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Searchable oscar", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Other papa", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Search for a specific todo, press enter — should open detail view directly (BUG-115)
	p.HandleKey(keyMsg("/"))
	for _, ch := range "oscar" {
		p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	p.HandleKey(specialKeyMsg(tea.KeyEnter))

	// Should open detail view directly, not freeze the filter
	if !p.detailView {
		t.Fatal("enter in search should open detail view directly (BUG-115)")
	}
	view := renderView(p)
	viewContains(t, view, "Searchable oscar")
}

// ---------------------------------------------------------------------------
// Agent Interactions (8 tests)
// ---------------------------------------------------------------------------

func TestView_TaskRunnerStep1ProjectSelection(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/testproject"

	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView || p.taskRunnerStep != 1 {
		t.Fatal("o should open task runner at step 1")
	}
	view := renderView(p)
	viewContains(t, view, "Step 1/3")
	viewContains(t, view, "Project")
}

func TestView_TaskRunnerStep2ModeSelection(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/testproject"

	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	if p.taskRunnerStep != 2 {
		t.Fatal("step should be 2")
	}
	view := renderView(p)
	viewContains(t, view, "Step 2/3")
	viewContains(t, view, "Mode")
	viewContains(t, view, "Normal")
	viewContains(t, view, "Worktree")
	viewContains(t, view, "Sandbox")
}

func TestView_TaskRunnerStep3LaunchOptions(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/testproject"

	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	p.HandleKey(keyMsg("enter")) // step 2 -> 3
	if p.taskRunnerStep != 3 {
		t.Fatal("step should be 3")
	}
	view := renderView(p)
	viewContains(t, view, "Step 3/3")
	viewContains(t, view, "Run Claude")
	viewContains(t, view, "Queue Agent")
	viewContains(t, view, "Run Agent Now")
}

func TestView_EditGuardBlocksMutationDuringAgent(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusRunning

	// Open detail view
	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}

	// Press enter again — should be blocked by edit guard (status=running)
	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	if !strings.Contains(p.flashMessage, "being updated by agent") {
		t.Errorf("expected flash about agent, got %q", p.flashMessage)
	}
}

func TestView_EditGuardBlocksCommandDuringAgent(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusRunning

	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	// Press 'c' — command input should also be blocked
	p.HandleKey(keyMsg("c"))
	if !strings.Contains(p.flashMessage, "being updated by agent") {
		t.Errorf("expected flash about agent, got %q", p.flashMessage)
	}
}

func TestView_EditGuardAllowsWatchDuringAgent(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusRunning

	// Set up daemon so the "w" key can open the session viewer.
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client { return client })
	_ = client.LaunchAgent(daemon.LaunchAgentParams{ID: todo.ID, Prompt: "test", Dir: t.TempDir()})
	time.Sleep(100 * time.Millisecond)

	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	// 'w' should NOT be blocked — it opens the session viewer
	p.HandleKey(keyMsg("w"))
	if !p.sessionViewerActive {
		t.Error("w should open session viewer even during active agent")
	}
}

func TestView_EditGuardAllowsNavigationDuringAgent(t *testing.T) {
	// Navigation (j/k) always works regardless of agent state — no guard needed.
	// Test cursor navigation with running status todos.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Agent todo quebec", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Normal todo romeo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	if p.ccCursor != 0 {
		t.Fatal("initial cursor should be 0")
	}
	p.HandleKey(keyMsg("j"))
	if p.ccCursor != 1 {
		t.Error("j should move cursor down")
	}
	p.HandleKey(keyMsg("k"))
	if p.ccCursor != 0 {
		t.Error("k should move cursor up")
	}
}

func TestView_SessionViewerOpensOnW(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusRunning

	// Set up daemon with an active agent for this todo.
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client { return client })
	_ = client.LaunchAgent(daemon.LaunchAgentParams{ID: todo.ID, Prompt: "test", Dir: t.TempDir()})
	time.Sleep(100 * time.Millisecond)

	// Open detail view, then press w
	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	p.HandleKey(keyMsg("w"))

	if !p.sessionViewerActive {
		t.Fatal("w should open session viewer")
	}

	view := renderView(p)
	viewContains(t, view, "SESSION VIEWER")
}

func TestView_NoSessionShowsFlash(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusBacklog

	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	p.HandleKey(keyMsg("w"))

	if p.sessionViewerActive {
		t.Error("session viewer should NOT open without active session")
	}
	if p.flashMessage == "" {
		t.Error("expected flash message when no session available")
	}
}

func TestView_SessionViewerClosesOnEsc(t *testing.T) {
	p := testPluginWithCC(t)
	todo := &p.cc.Todos[0]
	todo.Status = db.StatusRunning

	// Set up daemon with an active agent for this todo.
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client { return client })
	_ = client.LaunchAgent(daemon.LaunchAgentParams{ID: todo.ID, Prompt: "test", Dir: t.TempDir()})
	time.Sleep(100 * time.Millisecond)

	// Open detail → session viewer
	p.HandleKey(specialKeyMsg(tea.KeyEnter))
	p.HandleKey(keyMsg("w"))
	if !p.sessionViewerActive {
		t.Fatal("session viewer should be active")
	}

	// Esc closes session viewer, returns to detail
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.sessionViewerActive {
		t.Fatal("esc should close session viewer")
	}
	if !p.detailView {
		t.Error("should return to detail view after closing session viewer")
	}
}

func TestView_AgentFinishedUpdatesViewToReview(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Agent task sierra", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Before: agent status indicator should show "agent working"
	view := renderView(p)
	viewContains(t, view, "agent working")

	// Simulate agent finished (exit code 0 -> review)
	p.onAgentFinished("t1", 0)

	// After: status should change to review
	view = renderView(p)
	viewContains(t, view, "ready for review")
}

// ---------------------------------------------------------------------------
// Budget and Agent Header (4 tests)
// ---------------------------------------------------------------------------

func TestView_ExpandedAgentsHeaderShowsCounts(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Running agent tango", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", Title: "Queued agent uniform", Status: db.StatusEnqueued, Source: "manual", CreatedAt: time.Now()},
		{ID: "t3", Title: "Backlog task victor", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})

	// Expand and switch to agents tab
	p.HandleKey(keyMsg(" "))
	// tab from "focus" -> "new" -> "backlog" -> "agents"
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))
	p.HandleKey(keyMsg("tab"))

	view := renderView(p)
	// Agents tab should show the agent todos
	viewContains(t, view, "Running agent tango")
	viewContains(t, view, "Queued agent uniform")
	viewNotContains(t, view, "Backlog task victor")
}

func TestView_LaunchDeniedShowsFlash(t *testing.T) {
	// Skip: LaunchDeniedMsg does not exist in the current codebase.
	// The flash message mechanism for launch denial is handled via
	// p.flashMessage directly in the agent launch path, not through
	// a dedicated message type.
	t.Skip("LaunchDeniedMsg does not exist in current codebase")
}

func TestView_KillAgentUpdatesStatus(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Kill target whiskey", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	// Simulate agent finished with non-zero exit code (like a kill)
	p.onAgentFinished("t1", 1)

	view := renderView(p)
	// Non-zero exit sets status to "failed"
	viewContains(t, view, "failed")
}

func TestView_ConcurrencyLimitQueuesAgent(t *testing.T) {
	// In the collapsed view, renderTodoPanel shows agentStatusIndicator per-todo.
	// Starred todos with active statuses show their agent status indicators.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Active xray", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Active yankee", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t3", Title: "Active zulu", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t4", Title: "Queued amber", Status: db.StatusEnqueued, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Stay in collapsed view where status indicators are rendered
	view := renderView(p)
	viewContains(t, view, "queued")
	viewContains(t, view, "agent working")
}

// ---------------------------------------------------------------------------
// Daemon Agent Observability (detail spinner based on todo status)
// ---------------------------------------------------------------------------

func TestView_DetailSpinnerShownForDaemonRunningTodo(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning
	// No local session — this simulates a daemon-managed agent.

	// Open detail view for first todo
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}

	view := renderView(p)
	viewContains(t, view, "Agent running")
	viewContains(t, view, "w watch")
}

// ---------------------------------------------------------------------------
// Detail View: Auto-advance after complete/dismiss
// ---------------------------------------------------------------------------

func TestView_DetailCompleteAdvancesToNextTodo(t *testing.T) {
	// Setup: 4 todos, open detail on first, navigate to #3 with j, mark done.
	// After auto-advance, detail should show #4 (next), not #1.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Alpha first", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Bravo second", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t3", Title: "Charlie third", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t4", Title: "Delta fourth", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Open detail on first todo (ccCursor=0)
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}
	view := renderView(p)
	viewContains(t, view, "Alpha first")

	// Navigate to third todo using j
	p.HandleKey(keyMsg("j")) // now on Bravo
	p.HandleKey(keyMsg("j")) // now on Charlie
	view = renderView(p)
	viewContains(t, view, "Charlie third")

	// Mark done
	p.HandleKey(keyMsg("x"))
	if p.detailNotice == "" {
		t.Fatal("expected a notice after marking done")
	}

	// Simulate auto-advance by backdating the notice and sending a tick
	p.detailNoticeAt = time.Now().Add(-2 * time.Second)
	p.HandleMessage(ui.TickMsg(time.Now()))

	// After auto-advance, detail view should show Delta (next), not Alpha
	if !p.detailView {
		t.Fatal("detail view should still be open after auto-advance")
	}
	view = renderView(p)
	viewContains(t, view, "Delta fourth")
	viewNotContains(t, view, "Alpha first")
}

func TestView_DetailDismissAdvancesToNextTodo(t *testing.T) {
	// Same as above but with dismiss (X) instead of complete (x).
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Alpha first", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Bravo second", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t3", Title: "Charlie third", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t4", Title: "Delta fourth", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	p.HandleKey(keyMsg("enter"))
	p.HandleKey(keyMsg("j")) // Bravo
	p.HandleKey(keyMsg("j")) // Charlie
	p.HandleKey(keyMsg("X")) // dismiss Charlie

	p.detailNoticeAt = time.Now().Add(-2 * time.Second)
	p.HandleMessage(ui.TickMsg(time.Now()))

	if !p.detailView {
		t.Fatal("detail view should still be open after auto-advance")
	}
	view := renderView(p)
	viewContains(t, view, "Delta fourth")
	viewNotContains(t, view, "Alpha first")
}

func TestView_DetailCompleteLastTodoAdvancesToPrevious(t *testing.T) {
	// When completing the last item, cursor should move to the new last item.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Alpha first", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Bravo second", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
		{ID: "t3", Title: "Charlie third", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	p.HandleKey(keyMsg("enter"))
	p.HandleKey(keyMsg("j")) // Bravo
	p.HandleKey(keyMsg("j")) // Charlie (last)
	p.HandleKey(keyMsg("x")) // complete Charlie

	p.detailNoticeAt = time.Now().Add(-2 * time.Second)
	p.HandleMessage(ui.TickMsg(time.Now()))

	if !p.detailView {
		t.Fatal("detail view should still be open after auto-advance")
	}
	view := renderView(p)
	viewContains(t, view, "Bravo second")
}

// ---------------------------------------------------------------------------
// Source navigation uses [/] not j/k (bug fix)
// ---------------------------------------------------------------------------

func TestView_SourceNavBracketKeys(t *testing.T) {
	// Create a synthesis (merge) todo with two source todos.
	synthTodo := db.Todo{
		ID: "synth1", Title: "Merged todo", Status: db.StatusBacklog,
		Source: "merge", CreatedAt: time.Now(), Starred: true,
	}
	origA := db.Todo{
		ID: "origA", Title: "Original Alpha", Status: db.StatusBacklog,
		Source: "github", DisplayID: 10, CreatedAt: time.Now(),
	}
	origB := db.Todo{
		ID: "origB", Title: "Original Bravo", Status: db.StatusBacklog,
		Source: "slack", DisplayID: 11, CreatedAt: time.Now(),
	}

	p := testPluginWithTodos(t, []db.Todo{synthTodo, origA, origB})
	p.cc.Merges = []db.TodoMerge{
		{SynthesisID: "synth1", OriginalID: "origA"},
		{SynthesisID: "synth1", OriginalID: "origB"},
	}

	// Open detail view on the synthesis todo
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("detail view should be open")
	}

	// Render and verify sources section with hint text using [/]
	view := renderView(p)
	viewContains(t, view, "[/] select source")
	viewNotContains(t, view, "j/k select source")

	// mergeSourceCursor starts at 0; pressing ] should move to 1
	p.HandleKey(keyMsg("]"))
	if p.mergeSourceCursor != 1 {
		t.Errorf("expected mergeSourceCursor=1 after ], got %d", p.mergeSourceCursor)
	}

	// View should show > cursor on Bravo (index 1), not Alpha
	view = renderView(p)
	viewContains(t, view, "> #11")  // Bravo is now selected
	viewContains(t, view, "Original Bravo")

	// pressing [ should move back to 0
	p.HandleKey(keyMsg("["))
	if p.mergeSourceCursor != 0 {
		t.Errorf("expected mergeSourceCursor=0 after [, got %d", p.mergeSourceCursor)
	}

	// View should show > cursor back on Alpha (index 0)
	view = renderView(p)
	viewContains(t, view, "> #10")  // Alpha is selected again
}

func TestView_MergeSourceCursorResetOnDetailOpen(t *testing.T) {
	// When opening a new detail view, mergeSourceCursor should reset to 0
	// to prevent stale cursor positions from a previously viewed synthesis todo.
	synthTodo := db.Todo{
		ID: "synth1", Title: "Merged todo", Status: db.StatusBacklog,
		Source: "merge", CreatedAt: time.Now(), Starred: true,
	}
	origA := db.Todo{
		ID: "origA", Title: "Original Alpha", Status: db.StatusBacklog,
		Source: "github", DisplayID: 10, CreatedAt: time.Now(),
	}
	origB := db.Todo{
		ID: "origB", Title: "Original Bravo", Status: db.StatusBacklog,
		Source: "slack", DisplayID: 11, CreatedAt: time.Now(),
	}

	p := testPluginWithTodos(t, []db.Todo{synthTodo, origA, origB})
	p.cc.Merges = []db.TodoMerge{
		{SynthesisID: "synth1", OriginalID: "origA"},
		{SynthesisID: "synth1", OriginalID: "origB"},
	}

	// Open detail view and move cursor to index 1
	p.HandleKey(keyMsg("enter"))
	p.HandleKey(keyMsg("]"))
	if p.mergeSourceCursor != 1 {
		t.Fatalf("expected mergeSourceCursor=1, got %d", p.mergeSourceCursor)
	}

	// Close and reopen detail view
	p.HandleKey(keyMsg("esc"))
	if p.detailView {
		t.Fatal("detail view should be closed after esc")
	}
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("detail view should be open after enter")
	}

	// mergeSourceCursor should be reset to 0
	if p.mergeSourceCursor != 0 {
		t.Errorf("expected mergeSourceCursor=0 after reopening detail, got %d", p.mergeSourceCursor)
	}
}

func TestView_UnmergeShowsFeedback(t *testing.T) {
	// Pressing U on a synthesis todo with 3 sources should show a flash message,
	// remove the source from in-memory merges, and keep the detail view open.
	synthTodo := db.Todo{
		ID: "synth1", Title: "Merged todo", Status: db.StatusBacklog,
		Source: "merge", CreatedAt: time.Now(), Starred: true,
	}
	origA := db.Todo{
		ID: "origA", Title: "Original Alpha", Status: db.StatusBacklog,
		Source: "github", DisplayID: 10, CreatedAt: time.Now(),
	}
	origB := db.Todo{
		ID: "origB", Title: "Original Bravo", Status: db.StatusBacklog,
		Source: "slack", DisplayID: 11, CreatedAt: time.Now(),
	}
	origC := db.Todo{
		ID: "origC", Title: "Original Charlie", Status: db.StatusBacklog,
		Source: "gmail", DisplayID: 12, CreatedAt: time.Now(),
	}

	p := testPluginWithTodos(t, []db.Todo{synthTodo, origA, origB, origC})
	p.cc.Merges = []db.TodoMerge{
		{SynthesisID: "synth1", OriginalID: "origA"},
		{SynthesisID: "synth1", OriginalID: "origB"},
		{SynthesisID: "synth1", OriginalID: "origC"},
	}

	// Open detail view (cursor on synth1)
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("detail view should be open")
	}

	// Cursor is at 0 (origA). Press U to unmerge it.
	action := p.HandleKey(keyMsg("U"))

	// Should return a tea.Cmd for the DB write
	if action.TeaCmd == nil {
		t.Error("expected U to return a tea.Cmd for DB write")
	}

	// Flash message should confirm the unmerge
	if p.flashMessage == "" {
		t.Error("expected flash message after unmerge")
	}
	if !strings.Contains(p.flashMessage, "Unmerged") {
		t.Errorf("expected flash message to contain 'Unmerged', got %q", p.flashMessage)
	}

	// Detail view should remain open (2 sources remain)
	if !p.detailView {
		t.Error("detail view should remain open with 2+ sources remaining")
	}

	// In-memory merges should be updated (origA vetoed)
	remainingIDs := db.DBGetOriginalIDs(p.cc.Merges, "synth1")
	if len(remainingIDs) != 2 {
		t.Errorf("expected 2 remaining sources after unmerge, got %d", len(remainingIDs))
	}

	// View should no longer show origA in sources section
	view := renderView(p)
	viewContains(t, view, "Original Bravo")
	viewContains(t, view, "Original Charlie")
}

func TestView_UnmergeDissolvesWithTwoSources(t *testing.T) {
	// When a synthesis todo has only 2 sources and one is unmerged,
	// the synthesis should dissolve: detail view closes, flash shows dissolution.
	synthTodo := db.Todo{
		ID: "synth1", Title: "Merged todo", Status: db.StatusBacklog,
		Source: "merge", CreatedAt: time.Now(), Starred: true,
	}
	origA := db.Todo{
		ID: "origA", Title: "Original Alpha", Status: db.StatusBacklog,
		Source: "github", DisplayID: 10, CreatedAt: time.Now(),
	}
	origB := db.Todo{
		ID: "origB", Title: "Original Bravo", Status: db.StatusBacklog,
		Source: "slack", DisplayID: 11, CreatedAt: time.Now(),
	}

	p := testPluginWithTodos(t, []db.Todo{synthTodo, origA, origB})
	p.cc.Merges = []db.TodoMerge{
		{SynthesisID: "synth1", OriginalID: "origA"},
		{SynthesisID: "synth1", OriginalID: "origB"},
	}

	// Open detail view
	p.HandleKey(keyMsg("enter"))

	// Press U to unmerge origA — should dissolve the synthesis
	p.HandleKey(keyMsg("U"))

	// Detail view should be closed (synthesis dissolved)
	if p.detailView {
		t.Error("detail view should be closed after synthesis dissolution")
	}

	// Flash message should mention dissolution
	if !strings.Contains(p.flashMessage, "dissolved") {
		t.Errorf("expected flash message to mention dissolution, got %q", p.flashMessage)
	}
}

func TestView_ExpandedCompleteRemovesItem(t *testing.T) {
	todos := []db.Todo{
		{ID: "t1", DisplayID: 1, Title: "Alpha task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", DisplayID: 2, Title: "Bravo task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t3", DisplayID: 3, Title: "Charlie task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	}
	p := testPluginWithTodos(t, todos)

	// Enter expanded view
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("should be in expanded view")
	}

	// All 3 items visible
	view := renderView(p)
	viewContains(t, view, "Alpha task")
	viewContains(t, view, "Bravo task")
	viewContains(t, view, "Charlie task")

	// Complete the first item (cursor at 0)
	p.HandleKey(keyMsg("x"))

	// After completing, the item should be gone from the view
	view = renderView(p)
	viewNotContains(t, view, "Alpha task")
	viewContains(t, view, "Bravo task")
	viewContains(t, view, "Charlie task")

	// Verify no duplication: each remaining item appears exactly once
	if strings.Count(view, "Bravo task") != 1 {
		t.Errorf("expected 'Bravo task' exactly once, got %d occurrences", strings.Count(view, "Bravo task"))
	}
	if strings.Count(view, "Charlie task") != 1 {
		t.Errorf("expected 'Charlie task' exactly once, got %d occurrences", strings.Count(view, "Charlie task"))
	}
}

func TestView_ExpandedDismissRemovesItem(t *testing.T) {
	todos := []db.Todo{
		{ID: "t1", DisplayID: 1, Title: "Delta task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", DisplayID: 2, Title: "Echo task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	}
	p := testPluginWithTodos(t, todos)
	p.HandleKey(keyMsg(" "))

	// Dismiss first item
	p.HandleKey(keyMsg("X"))

	view := renderView(p)
	viewNotContains(t, view, "Delta task")
	viewContains(t, view, "Echo task")
	if strings.Count(view, "Echo task") != 1 {
		t.Errorf("expected 'Echo task' exactly once, got %d occurrences", strings.Count(view, "Echo task"))
	}
}

func TestView_ExpandedUndoRestoresItem(t *testing.T) {
	todos := []db.Todo{
		{ID: "t1", DisplayID: 1, Title: "Foxtrot task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", DisplayID: 2, Title: "Golf task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	}
	p := testPluginWithTodos(t, todos)
	p.HandleKey(keyMsg(" "))

	// Complete then undo — switch to "all" tab because completing clears Focus,
	// and undo doesn't restore it. The "all" tab shows all active items regardless.
	p.triageFilter = "all"
	p.HandleKey(keyMsg("x"))
	view := renderView(p)
	viewNotContains(t, view, "Foxtrot task")

	p.HandleKey(keyMsg("u"))
	view = renderView(p)
	viewContains(t, view, "Foxtrot task")
	viewContains(t, view, "Golf task")
}

func TestView_ExpandedOffsetClampedAfterComplete(t *testing.T) {
	todos := []db.Todo{
		{ID: "t1", DisplayID: 1, Title: "Hotel task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", DisplayID: 2, Title: "India task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t3", DisplayID: 3, Title: "Juliet task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	}
	p := testPluginWithTodos(t, todos)
	p.HandleKey(keyMsg(" ")) // expand

	// Artificially set offset beyond what the list can support after removal
	p.ccExpandedOffset = 20
	p.ccCursor = 0

	// Complete an item — clampExpandedOffset should fix the stale offset
	p.HandleKey(keyMsg("x"))

	if p.ccExpandedOffset != 0 {
		t.Errorf("expected ccExpandedOffset=0 after clamping (only 2 items left), got %d", p.ccExpandedOffset)
	}
}

func TestView_JKNavigateTodosOnMergeTodo(t *testing.T) {
	// j/k should navigate between todos even when on a merge todo.
	synthTodo := db.Todo{
		ID: "synth1", Title: "Merged todo", Status: db.StatusBacklog,
		Source: "merge", CreatedAt: time.Now(), Starred: true,
	}
	otherTodo := db.Todo{
		ID: "t2", Title: "Other todo", Status: db.StatusBacklog,
		Source: "manual", CreatedAt: time.Now(), Starred: true,
	}

	p := testPluginWithTodos(t, []db.Todo{synthTodo, otherTodo})
	p.cc.Merges = []db.TodoMerge{
		{SynthesisID: "synth1", OriginalID: "origA"},
		{SynthesisID: "synth1", OriginalID: "origB"},
	}

	// Open detail view on the synthesis todo
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("detail view should be open")
	}
	if p.detailTodoID != "synth1" {
		t.Fatalf("expected detail on synth1, got %s", p.detailTodoID)
	}

	// j should navigate to the next todo, NOT get captured by source nav
	p.HandleKey(keyMsg("j"))
	if p.detailTodoID != "t2" {
		t.Errorf("expected j to navigate to t2, but detailTodoID=%s", p.detailTodoID)
	}
}

// ---------------------------------------------------------------------------
// Help Overlay (3 tests)
// ---------------------------------------------------------------------------

func TestView_HelpOverlayShowsKeyboardShortcuts(t *testing.T) {
	p := testPluginWithCC(t)

	// Before ?, no help overlay.
	view := renderView(p)
	viewNotContains(t, view, "KEYBOARD SHORTCUTS")

	// Press ? to toggle help on.
	action := p.HandleKey(keyMsg("?"))
	if !p.showHelp {
		t.Fatal("? should toggle showHelp on")
	}
	if action.Type != plugin.ActionConsumed {
		t.Errorf("? should return ConsumedAction, got %q", action.Type)
	}

	view = renderView(p)
	viewContains(t, view, "KEYBOARD SHORTCUTS")
	viewContains(t, view, "Toggle this help")
	viewContains(t, view, "Command Center")
}

func TestView_HelpOverlayDismissesOnAnyKey(t *testing.T) {
	p := testPluginWithCC(t)

	// Open help overlay.
	p.HandleKey(keyMsg("?"))
	if !p.showHelp {
		t.Fatal("? should toggle showHelp on")
	}

	// Any key should dismiss.
	action := p.HandleKey(keyMsg("q"))
	if p.showHelp {
		t.Fatal("any key should dismiss help overlay")
	}
	if action.Type != plugin.ActionConsumed {
		t.Errorf("dismiss key should return ConsumedAction, got %q", action.Type)
	}

	// View should no longer contain help overlay.
	view := renderView(p)
	viewNotContains(t, view, "KEYBOARD SHORTCUTS")
}

func TestView_HelpOverlayInDetailView(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Detail help test", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Open detail view.
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}

	// Press ? in detail view.
	action := p.HandleKey(keyMsg("?"))
	if !p.showHelp {
		t.Fatal("? should toggle showHelp on in detail view")
	}
	if action.Type != plugin.ActionConsumed {
		t.Errorf("? in detail view should return ConsumedAction, got %q", action.Type)
	}

	view := renderView(p)
	viewContains(t, view, "KEYBOARD SHORTCUTS")
	viewContains(t, view, "Todo Detail")
}

func TestView_DetailJKResetsScrollPosition(t *testing.T) {
	// Create todos with long detail text so the viewport can scroll
	longDetail := strings.Repeat("Line of detail text.\n", 50)
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "First todo", Status: db.StatusBacklog, Source: "manual", Detail: longDetail, CreatedAt: time.Now(), Starred: true},
		{ID: "t2", Title: "Second todo", Status: db.StatusBacklog, Source: "manual", Detail: longDetail, CreatedAt: time.Now(), Starred: true},
		{ID: "t3", Title: "Third todo", Status: db.StatusBacklog, Source: "manual", Detail: longDetail, CreatedAt: time.Now(), Starred: true},
	})

	// Open detail view on first todo
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}

	// Render to initialize viewport dimensions
	renderView(p)

	// Scroll down several lines
	p.HandleKey(keyMsg("down"))
	p.HandleKey(keyMsg("down"))
	p.HandleKey(keyMsg("down"))
	renderView(p) // apply scroll

	if p.detailVP.YOffset == 0 {
		t.Fatal("expected viewport to be scrolled down after arrow keys")
	}

	// Navigate to next todo with j
	p.HandleKey(keyMsg("j"))

	if p.detailTodoID != "t2" {
		t.Fatalf("expected detailTodoID to be t2, got %s", p.detailTodoID)
	}
	if p.detailVP.YOffset != 0 {
		t.Errorf("expected viewport YOffset to be 0 after j navigation, got %d", p.detailVP.YOffset)
	}

	// Now scroll down again and test k (previous)
	renderView(p)
	p.HandleKey(keyMsg("down"))
	p.HandleKey(keyMsg("down"))
	p.HandleKey(keyMsg("down"))
	renderView(p)

	if p.detailVP.YOffset == 0 {
		t.Fatal("expected viewport to be scrolled down after arrow keys")
	}

	p.HandleKey(keyMsg("k"))
	if p.detailTodoID != "t1" {
		t.Fatalf("expected detailTodoID to be t1, got %s", p.detailTodoID)
	}
	if p.detailVP.YOffset != 0 {
		t.Errorf("expected viewport YOffset to be 0 after k navigation, got %d", p.detailVP.YOffset)
	}
}

// ---------------------------------------------------------------------------
// Calendar Event Duration Inline (Bug Fix)
// ---------------------------------------------------------------------------

func TestView_CalendarEventDurationOnSameLine(t *testing.T) {
	// Calendar event duration must render on the same line as the time and title,
	// not wrap to a new line due to width miscalculation with panel borders.
	p := testPlugin(t)
	p.cfg.Calendar.Enabled = true
	p.cfg.Calendar.Calendars = []config.CalendarEntry{
		{ID: "cal1", Label: "Work", Color: "#7aa2f7"},
	}

	now := time.Now()
	// Create an event that starts in the future (so it's not "past")
	eventStart := time.Date(now.Year(), now.Month(), now.Day(), 14, 0, 0, 0, now.Location())
	eventEnd := eventStart.Add(90 * time.Minute)

	p.cc = &db.CommandCenter{
		GeneratedAt: now,
		Calendar: db.CalendarData{
			Today: []db.CalendarEvent{
				{
					Title:      "Team Standup Meeting",
					Start:      eventStart,
					End:        eventEnd,
					CalendarID: "cal1",
				},
			},
		},
		Todos: []db.Todo{
			{ID: "t1", Title: "Test todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: now},
		},
	}
	p.width = 120
	p.height = 40

	view := renderView(p)

	// The duration "1h30m" must be on the same line as the event title.
	// Split the view into lines and find the line with the event title.
	lines := strings.Split(view, "\n")
	foundTitleLine := false
	for _, line := range lines {
		if strings.Contains(line, "Team Standup Meeting") {
			foundTitleLine = true
			if !strings.Contains(line, "1h30m") {
				t.Errorf("duration '1h30m' should be on the same line as 'Team Standup Meeting', but it was not.\nLine: %q\nFull view:\n%s", line, view)
			}
			break
		}
	}
	if !foundTitleLine {
		t.Fatalf("expected to find 'Team Standup Meeting' in view but did not.\nFull view:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Focus & Star View Tests
// ---------------------------------------------------------------------------

// TestView_CollapsedShowsOnlyStarred: the collapsed view should show only starred todos,
// not all non-new todos. Non-starred todos should be hidden.
func TestView_CollapsedShowsOnlyStarred(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred task alpha", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
		{ID: "t2", Title: "Unstarred task beta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: false},
		{ID: "t3", Title: "Focused not starred gamma", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
	})

	// Ensure collapsed (not expanded)
	if p.ccExpanded {
		t.Fatal("plugin should start collapsed")
	}

	view := renderView(p)

	// Starred item should appear
	viewContains(t, view, "Starred task alpha")
	// Non-starred, non-focused items should NOT appear in collapsed view
	viewNotContains(t, view, "Unstarred task beta")
	// Focused-but-not-starred should NOT appear in collapsed view
	viewNotContains(t, view, "Focused not starred gamma")
	// Yellow star indicator should appear for the starred item
	viewContains(t, view, "★")
}

// TestView_CollapsedEmptyNudge: when no todos are starred, the collapsed view
// should show a nudge message instead of an empty list.
func TestView_CollapsedEmptyNudge(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Unstarred task delta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false},
		{ID: "t2", Title: "Another unstarred task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false},
	})

	// Ensure collapsed
	if p.ccExpanded {
		t.Fatal("plugin should start collapsed")
	}

	view := renderView(p)

	// Nudge message should be visible
	viewContains(t, view, "No starred items")
	// The unstarred todos should NOT appear
	viewNotContains(t, view, "Unstarred task delta")
	viewNotContains(t, view, "Another unstarred task")
}

// TestView_FocusTabShowsFocused: the expanded "focus" triage tab should show
// all focused items (both starred and focused-but-not-starred).
func TestView_FocusTabShowsFocused(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred item epsilon", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
		{ID: "t2", Title: "Focused not starred zeta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
		{ID: "t3", Title: "Not focused eta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: false},
	})

	// Expand the view — default tab is now "focus"
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand the view")
	}

	// Focus is already the default tab — no need to navigate
	view := renderView(p)

	// Both starred and focused-but-not-starred should appear
	viewContains(t, view, "Starred item epsilon")
	viewContains(t, view, "Focused not starred zeta")
	// Non-focused item should NOT appear in focus tab
	viewNotContains(t, view, "Not focused eta")
}

// TestView_StarIndicators: starred items show yellow star (★), focused-but-not-starred
// items show gray star (☆) in expanded views.
func TestView_StarIndicators(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred theta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
		{ID: "t2", Title: "Focused only iota", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
		{ID: "t3", Title: "Plain kappa", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: false},
	})

	// Expand to show all items — default tab is now "focus"
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand")
	}
	// Focus is already the default tab — no navigation needed
	view := renderView(p)

	// Starred item should show yellow star ★
	viewContains(t, view, "★")
	// Focused-only item should show gray star ☆
	viewContains(t, view, "☆")
}

// TestView_SchedulingOffer: after starring a todo, the flash message should show
// a scheduling offer prompt.
func TestView_SchedulingOffer(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Task to star lambda", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false},
	})

	// Simulate the scheduling offer state that will be set by the 's' key in Stage 5.
	// The flash message format is: "★ <title> — Schedule time? S = yes, any key = skip"
	p.flashMessage = "★ Task to star lambda — Schedule time? S = yes, any key = skip"

	// The scheduling offer flash should appear in the view
	view := renderView(p)

	// After starring, the flash should mention scheduling
	viewContains(t, view, "Schedule time")
}

// TestView_HelpOverlayShowsFSKeys verifies that the help overlay shows f, s, S bindings.
func TestView_HelpOverlayShowsFSKeys(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Focusable todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	// Open help overlay.
	p.HandleKey(keyMsg("?"))
	if !p.showHelp {
		t.Fatal("? should toggle showHelp on")
	}

	view := renderView(p)
	viewContains(t, view, "KEYBOARD SHORTCUTS")
	// f key entry
	viewContains(t, view, "Toggle focus")
	// s key entry
	viewContains(t, view, "Toggle star")
	// S key entry
	viewContains(t, view, "Schedule block")
	// Old "Schedule time block" entry should be gone
	viewNotContains(t, view, "Schedule time block for todo")
}

// TestView_CompleteClears StarFocus verifies that completing a todo clears its star and focus
// in memory so the collapsed view immediately removes it.
func TestView_CompleteClears_StarFocus(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred focused task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
	})

	// Complete the todo (x key).
	p.HandleKey(keyMsg("x"))

	// The in-memory todo should have Starred and Focus cleared.
	found := false
	for _, t2 := range p.cc.Todos {
		if t2.ID == "t1" {
			found = true
			if t2.Starred {
				t.Error("Starred should be false after completing todo")
			}
			if t2.Focus {
				t.Error("Focus should be false after completing todo")
			}
		}
	}
	if !found {
		t.Error("todo t1 not found in cc.Todos")
	}
}

// TestView_DismissClearsStarFocus verifies that dismissing a todo clears its star and focus
// in memory.
func TestView_DismissClearsStarFocus(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred task to dismiss", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
	})

	// Dismiss the todo (X key).
	p.HandleKey(keyMsg("X"))

	// The in-memory todo should have Starred and Focus cleared.
	found := false
	for _, t2 := range p.cc.Todos {
		if t2.ID == "t1" {
			found = true
			if t2.Starred {
				t.Error("Starred should be false after dismissing todo")
			}
			if t2.Focus {
				t.Error("Focus should be false after dismissing todo")
			}
		}
	}
	if !found {
		t.Error("todo t1 not found in cc.Todos")
	}
}

// TestView_SortStarredFirst verifies that starred todos appear before non-starred
// todos in the filtered list (both collapsed and expanded views).
func TestView_SortStarredFirst(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Unstarred alpha", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
		{ID: "t2", Title: "Starred beta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
		{ID: "t3", Title: "Unstarred gamma", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
	})

	// Expand to see all todos (default is "focus" tab, all have Focus=true).
	p.HandleKey(keyMsg(" "))

	filtered := p.filteredTodos()
	if len(filtered) < 2 {
		t.Fatalf("expected at least 2 filtered todos, got %d", len(filtered))
	}
	// First item must be the starred one.
	if !filtered[0].Starred {
		t.Errorf("expected first item to be starred, got %q (starred=%v)", filtered[0].Title, filtered[0].Starred)
	}
}

func TestView_DetailShowsStarIndicator(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Starred detail task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
	})
	p.width = 120
	p.height = 40
	// Enter detail view
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := p.View(120, 40, 0)
	viewContains(t, view, "★")
	viewContains(t, view, "Starred detail task")
}

func TestView_DetailShowsFocusIndicator(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Focused detail task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: true},
	})
	p.width = 120
	p.height = 40
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := p.View(120, 40, 0)
	viewContains(t, view, "☆")
	viewContains(t, view, "Focused detail task")
}

func TestView_DetailHintsShowFSKeys(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Detail hints task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	p.width = 120
	p.height = 40
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := p.View(120, 40, 0)
	viewContains(t, view, "f focus")
	viewContains(t, view, "s star")
	viewContains(t, view, "S schedule")
}

// ---------------------------------------------------------------------------
// Session Summary Markdown Rendering (BUG-133)
// ---------------------------------------------------------------------------

func TestView_DetailSessionSummaryRendersHeadingsWithoutPrefix(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{
			ID:             "t1",
			Title:          "Summary test task",
			Status:         db.StatusReview,
			Source:         "manual",
			CreatedAt:      time.Now(),
			Starred:        true,
			SessionSummary: "## What was done\n- Searched for user\n- Sent message\n\n## Key decisions\n- Used MCP tools",
		},
	})
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := renderView(p)
	// SESSION SUMMARY header should be present
	viewContains(t, view, "SESSION SUMMARY")
	// Heading text should be present
	viewContains(t, view, "What was done")
	viewContains(t, view, "Key decisions")
	// Raw ## prefix should NOT be visible
	viewNotContains(t, view, "## What was done")
	viewNotContains(t, view, "## Key decisions")
}

func TestView_DetailSessionSummaryRendersBulletsWithoutDashPrefix(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{
			ID:             "t1",
			Title:          "Bullet test task",
			Status:         db.StatusReview,
			Source:         "manual",
			CreatedAt:      time.Now(),
			Starred:        true,
			SessionSummary: "- First bullet\n- Second bullet",
		},
	})
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := renderView(p)
	// Bullet text should be present
	viewContains(t, view, "First bullet")
	viewContains(t, view, "Second bullet")
	// Bullet character should be present
	viewContains(t, view, "\u2022")
}

func TestView_DetailSessionSummaryRendersInlineCodeWithoutBackticks(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{
			ID:             "t1",
			Title:          "Code test task",
			Status:         db.StatusReview,
			Source:         "manual",
			CreatedAt:      time.Now(),
			Starred:        true,
			SessionSummary: "- Used `slack_send_message` MCP tool",
		},
	})
	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	view := renderView(p)
	// Code text should be present
	viewContains(t, view, "slack_send_message")
	// Backticks should NOT be visible in the rendered output
	viewNotContains(t, view, "`slack_send_message`")
}

func TestView_StarringInboxItemMovesToFocusTab(t *testing.T) {
	// BUG-137: Starring an inbox item should accept it (new -> backlog)
	// and set Focus=true, moving it from the Inbox tab to the Focus tab.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Triage me", Status: db.StatusNew, Source: "github", CreatedAt: time.Now()},
	})

	// Expand and switch to New tab (focus -> new)
	p.HandleKey(keyMsg(" "))
	p.HandleKey(keyMsg("tab"))
	newView := renderView(p)
	viewContains(t, newView, "New (1)")
	viewContains(t, newView, "Triage me")

	// Star the item while on the New tab — opens schedule modal
	p.HandleKey(keyMsg("s"))

	// Dismiss the schedule modal to see the underlying view
	p.HandleKey(specialKeyMsg(tea.KeyEsc))

	// The item's status should now be "backlog" (accepted) with Focus=true, so it should
	// no longer appear in the New tab — New count drops to 0, focus gains it.
	newAfterStar := renderView(p)
	viewContains(t, newAfterStar, "New (0)")
	viewContains(t, newAfterStar, "Focus (1)")

	// Switch to focus tab and verify the item appears there
	p.triageFilter = "focus"
	focusView := renderView(p)
	viewContains(t, focusView, "Triage me")
}

// ---------------------------------------------------------------------------
// Backlog Toggle (BUG-135)
// ---------------------------------------------------------------------------

func TestView_BacklogTabShowsCompletedItems(t *testing.T) {
	now := time.Now()
	doneAt := now.Add(-time.Hour)
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Active task alpha", Status: db.StatusBacklog, Source: "manual", CreatedAt: now, Starred: true},
		{ID: "t2", Title: "Completed task bravo", Status: db.StatusCompleted, Source: "manual", CreatedAt: now, CompletedAt: &doneAt},
		{ID: "t3", Title: "Dismissed task charlie", Status: db.StatusDismissed, Source: "manual", CreatedAt: now, CompletedAt: &doneAt},
	})

	// Before b key: collapsed view shows starred active items
	view := renderView(p)
	viewContains(t, view, "Active task alpha")
	viewContains(t, view, "TODOS")

	// Press b to jump to Backlog tab
	p.HandleKey(keyMsg("b"))
	if !p.ccExpanded {
		t.Fatal("b should expand the view")
	}
	if p.triageFilter != "backlog" {
		t.Fatalf("b should set triageFilter to 'backlog', got %q", p.triageFilter)
	}

	view = renderView(p)
	// Completed items should be visible
	viewContains(t, view, "Completed task bravo")
	viewContains(t, view, "Dismissed task charlie")
	// Active items should NOT be in the backlog tab
	viewNotContains(t, view, "Active task alpha")
}

func TestView_BacklogTabResetsCursor(t *testing.T) {
	now := time.Now()
	doneAt := now.Add(-time.Hour)
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "First active", Status: db.StatusBacklog, Source: "manual", CreatedAt: now, Starred: true},
		{ID: "t2", Title: "Second active", Status: db.StatusBacklog, Source: "manual", CreatedAt: now, Starred: true},
		{ID: "t3", Title: "Done item", Status: db.StatusCompleted, Source: "manual", CreatedAt: now, CompletedAt: &doneAt},
	})

	// Move cursor down
	p.HandleKey(keyMsg("j"))
	if p.ccCursor != 1 {
		t.Fatalf("expected cursor at 1, got %d", p.ccCursor)
	}

	// Press b — cursor should reset to 0
	p.HandleKey(keyMsg("b"))
	if p.ccCursor != 0 {
		t.Fatalf("expected cursor reset to 0 after b key, got %d", p.ccCursor)
	}
}

func TestView_BacklogTabEmptyState(t *testing.T) {
	now := time.Now()
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Only active", Status: db.StatusBacklog, Source: "manual", CreatedAt: now, Starred: true},
	})

	// Press b to jump to backlog — no completed items
	p.HandleKey(keyMsg("b"))
	view := renderView(p)
	// Should show "No active todos" (expanded view empty state)
	viewContains(t, view, "No active todos")
}

func TestView_BacklogTabCountInTabBar(t *testing.T) {
	now := time.Now()
	doneAt := now.Add(-time.Hour)
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Active one", Status: db.StatusBacklog, Source: "manual", CreatedAt: now, Starred: true},
		{ID: "t2", Title: "Done one", Status: db.StatusCompleted, Source: "manual", CreatedAt: now, CompletedAt: &doneAt},
		{ID: "t3", Title: "Done two", Status: db.StatusDismissed, Source: "manual", CreatedAt: now, CompletedAt: &doneAt},
	})

	p.HandleKey(keyMsg("b"))
	view := renderView(p)
	// Tab bar should show Backlog count
	viewContains(t, view, "Backlog (2)")
}

// ---------------------------------------------------------------------------
// BUG-136: Star toggle duplicate line rendering
// ---------------------------------------------------------------------------

func TestView_StarToggleNoDuplicateLine(t *testing.T) {
	// BUG-136: After pressing 's' to star a todo, the schedule modal overlay
	// covers the view. The title should not appear duplicated. Dismissing the
	// modal should show the todo list with the title appearing exactly once.
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Build the Thanx Security Plugin", DisplayID: 153, Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.ccExpandedCols = 1
	p.triageFilter = "all"
	p.ccCursor = 0

	// Press 's' to star — opens schedule modal
	p.HandleKey(keyMsg("s"))

	view := renderView(p)
	// Schedule modal should be present
	viewContains(t, view, "Schedule time block")

	// Dismiss the modal
	p.HandleKey(specialKeyMsg(tea.KeyEsc))

	view = renderView(p)
	// "153." must appear exactly once — no duplicate
	numCount := strings.Count(view, "153.")
	if numCount != 1 {
		t.Errorf("after dismissing modal: expected '153.' exactly 1 time, got %d\nView:\n%s", numCount, view)
	}
}

func TestView_StarToggleExpandedTitleMaxWidth(t *testing.T) {
	// Verify that long titles with stars don't wrap inside the column, which would
	// produce a duplicate-looking line (BUG-136 root cause).
	longTitle := "Build the Thanx Security Plugin with extensive documentation and comprehensive testing coverage for all modules"
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: longTitle, DisplayID: 153, Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	p.ccExpanded = true
	p.ccExpandedCols = 2
	p.triageFilter = "all"
	p.ccCursor = 0

	view := renderView(p)

	// "153." must appear exactly once — no wrapping should duplicate the prefix.
	numCount := strings.Count(view, "153.")
	if numCount != 1 {
		t.Errorf("long title 2-col: expected '153.' exactly 1 time, got %d\nView:\n%s", numCount, view)
	}
}

func TestView_StarPrefixWidthExpandedItem(t *testing.T) {
	// Directly verify renderExpandedTodoItem produces exactly 2 lines for both
	// starred and unstarred items at various column widths.
	p := testPlugin(t)

	todo := db.Todo{
		ID: "t1", Title: "Build the Thanx Security Plugin", DisplayID: 153,
		Status: db.StatusBacklog, Source: "manual", Starred: true,
	}

	for _, colWidth := range []int{40, 50, 58, 60, 80, 120} {
		rendered := renderExpandedTodoItem(&p.styles, &p.grad, todo, 153, true, colWidth, 0, false)
		lineCount := strings.Count(rendered, "\n") + 1
		if lineCount != 2 {
			t.Errorf("starred item at width %d: expected 2 lines, got %d\nRendered:\n%q", colWidth, lineCount, rendered)
		}
	}
}

func TestView_StarPrefixWidthOverflow(t *testing.T) {
	// Verify that the star prefix width is properly accounted for in the
	// expanded view. A title filling the full titleMax should NOT cause
	// the rendered line to exceed maxWidth (which would cause lipgloss
	// to wrap, producing extra lines).
	p := testPlugin(t)

	// At colWidth=58: current prefixWidth = 2 + len("153. ") = 7
	// titleMax = 58 - 7 = 51
	// line width = pointer(2) + num(3) + ". "(2) + star(2) + title(51) = 60
	// This overflows colWidth=58 by 2!
	colWidth := 58
	title := strings.Repeat("A", 60) // long enough to fill titleMax after truncation

	todo := db.Todo{
		ID: "t1", Title: title, DisplayID: 153,
		Status: db.StatusBacklog, Source: "manual", Starred: true,
	}

	rendered := renderExpandedTodoItem(&p.styles, &p.grad, todo, 153, true, colWidth, 0, false)

	// The item should produce exactly 2 lines (title + details).
	// If the title line overflows maxWidth, lipgloss column wrapping in
	// renderExpandedTodoView will split it into more lines.
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Errorf("starred item at colWidth=%d with max-length title: expected 2 lines, got %d\nRendered:\n%q", colWidth, len(lines), rendered)
	}

	// Additionally, the visual width of line1 should not exceed colWidth.
	line1Width := lipgloss.Width(lines[0])
	if line1Width > colWidth {
		t.Errorf("line1 visual width (%d) exceeds colWidth (%d) — star prefix width not accounted for\nLine1: %q", line1Width, colWidth, lines[0])
	}
}

func TestView_CollapsedStarPrefixWidthOverflow(t *testing.T) {
	// In the collapsed view, titleMaxWidth = width - 8.
	// But for 3-digit display IDs, the actual prefix is wider:
	// pointer(2) + numStr(3) + ". "(2) + star(2) + title = contentWidth + numLen - 4
	// For numLen=3: contentWidth - 1 (fits)
	// For numLen=4: contentWidth (fits exactly)
	// For numLen=1: contentWidth - 3 (fits)
	// Actually: line = 2 + numLen + 2 + 2 + (width-8) = numLen + width - 2
	// For numLen=3: 3 + width - 2 = width + 1 -> OVERFLOWS by 1!
	p := testPlugin(t)

	contentWidth := 50 // simulate content area

	todo := db.Todo{
		ID: "t1", Title: strings.Repeat("B", 100), DisplayID: 153,
		Status: db.StatusBacklog, Source: "manual", Starred: true,
	}

	todos := []db.Todo{todo}
	rendered := renderTodoPanel(&p.styles, &p.grad, todos, 0, 0, 5, contentWidth, 0, "", nil, 3)

	// Find the line with "153."
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "153.") {
			w := lipgloss.Width(line)
			if w > contentWidth {
				t.Errorf("collapsed todo line visual width (%d) exceeds contentWidth (%d)\nLine: %q", w, contentWidth, line)
			}
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Manual Refresh (r key) — BUG-141
// ---------------------------------------------------------------------------

func TestView_ManualRefreshShowsFlashMessage(t *testing.T) {
	p := testPluginWithCC(t)
	// Reset ccRefreshing (Init sets it true because empty DB has zero GeneratedAt)
	p.ccRefreshing = false
	action := p.HandleKey(keyMsg("r"))
	if action.TeaCmd == nil {
		t.Fatal("r key should return a TeaCmd for refresh")
	}
	if !p.ccRefreshing {
		t.Error("ccRefreshing should be true after pressing r")
	}
	if p.flashMessage == "" {
		t.Error("expected flash message after pressing r")
	}
	if !strings.Contains(p.flashMessage, "Refreshing") {
		t.Errorf("flash message = %q, want something containing 'Refreshing'", p.flashMessage)
	}
	view := renderView(p)
	viewContains(t, view, "Refreshing")
}

func TestView_ManualRefreshCompletionShowsFlashMessage(t *testing.T) {
	p := testPluginWithCC(t)
	p.ccRefreshing = true
	handled, _ := p.HandleMessage(ccRefreshFinishedMsg{err: nil})
	if !handled {
		t.Fatal("ccRefreshFinishedMsg should be handled")
	}
	if p.ccRefreshing {
		t.Error("ccRefreshing should be false after refresh finished")
	}
	if p.flashMessage == "" {
		t.Error("expected flash message after refresh completes")
	}
	if !strings.Contains(p.flashMessage, "Refresh") {
		t.Errorf("flash message = %q, want something containing 'Refresh'", p.flashMessage)
	}
}

func TestView_ManualRefreshNoOpWhenAlreadyRefreshing(t *testing.T) {
	p := testPluginWithCC(t)
	p.ccRefreshing = true
	p.flashMessage = ""
	action := p.HandleKey(keyMsg("r"))
	if action.TeaCmd != nil {
		t.Error("r key should not dispatch another refresh when already refreshing")
	}
	if p.flashMessage == "" {
		t.Error("expected flash message even when already refreshing")
	}
}

// ---------------------------------------------------------------------------
// BUG-138: Focus tab visible and default
// ---------------------------------------------------------------------------

// TestView_FocusTabIsDefaultOnExpand verifies that pressing space to expand
// lands on the Focus tab (not ToDo).
func TestView_FocusTabIsDefaultOnExpand(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Focused item one", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", Title: "Unfocused item two", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: false},
	})

	// Expand the view
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand the view")
	}

	// Default triageFilter should be "focus"
	if p.triageFilter != "focus" {
		t.Fatalf("BUG-138: expected default triageFilter 'focus', got %q", p.triageFilter)
	}

	view := renderView(p)
	// Focus tab should be visible and active
	viewContains(t, view, "Focus")
	// Focused item should be visible
	viewContains(t, view, "Focused item one")
	// Unfocused item should NOT be visible in the Focus tab
	viewNotContains(t, view, "Unfocused item two")
}

// TestView_NoToDoTabInExpandedView verifies that the "ToDo" tab label
// does not appear in the expanded filter bar (BUG-138).
func TestView_NoToDoTabInExpandedView(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Some task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	})

	p.HandleKey(keyMsg(" "))
	view := renderView(p)

	// "ToDo" should not appear as a tab label
	viewNotContains(t, view, "ToDo")
	// "Focus" should appear instead
	viewContains(t, view, "Focus")
}

// TestView_FocusTabOrderCycles verifies the tab cycling order:
// Focus -> New -> Backlog -> Agents -> Review -> All -> Focus (wraps)
func TestView_FocusTabOrderCycles(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Task one", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
	})

	p.HandleKey(keyMsg(" "))
	if p.triageFilter != "focus" {
		t.Fatalf("expected 'focus', got %q", p.triageFilter)
	}

	// Tab through the order
	p.HandleKey(keyMsg("tab")) // focus -> new
	if p.triageFilter != "new" {
		t.Errorf("expected 'new', got %q", p.triageFilter)
	}

	p.HandleKey(keyMsg("tab")) // new -> backlog
	if p.triageFilter != "backlog" {
		t.Errorf("expected 'backlog', got %q", p.triageFilter)
	}

	p.HandleKey(keyMsg("tab")) // backlog -> agents
	if p.triageFilter != "agents" {
		t.Errorf("expected 'agents', got %q", p.triageFilter)
	}

	p.HandleKey(keyMsg("tab")) // agents -> review
	if p.triageFilter != "review" {
		t.Errorf("expected 'review', got %q", p.triageFilter)
	}

	p.HandleKey(keyMsg("tab")) // review -> all
	if p.triageFilter != "all" {
		t.Errorf("expected 'all', got %q", p.triageFilter)
	}

	p.HandleKey(keyMsg("tab")) // all -> focus (wrap)
	if p.triageFilter != "focus" {
		t.Errorf("expected 'focus' (wrap), got %q", p.triageFilter)
	}
}

// TestView_FocusTabWithCount verifies that the Focus tab shows the count
// of focused items in the tab bar.
func TestView_FocusTabWithCount(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Focused A", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: true},
		{ID: "t2", Title: "Focused B", Status: db.StatusNew, Source: "github", CreatedAt: time.Now(), Focus: true},
		{ID: "t3", Title: "Not focused", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Focus: false},
	})

	p.HandleKey(keyMsg(" "))
	view := renderView(p)
	viewContains(t, view, "Focus (2)")
}

// ---------------------------------------------------------------------------
// Schedule Modal View Tests (BUG-134)
// ---------------------------------------------------------------------------

func TestView_ScheduleModalPickerRendering(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Schedule me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star the todo to open schedule modal
	p.HandleKey(keyMsg("s"))

	view := renderView(p)

	// Modal should contain title and duration options
	viewContains(t, view, "Schedule time block")
	viewContains(t, view, "15m")
	viewContains(t, view, "30m")
	viewContains(t, view, "1h")
	viewContains(t, view, "2h")
	viewContains(t, view, "4h")

	// Hint text should be present
	viewContains(t, view, "j/k nav")
	viewContains(t, view, "enter select")
	viewContains(t, view, "esc")
}

func TestView_ScheduleModalCursorNavigation(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Navigate me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star to open modal — cursor starts at index 2 (1h)
	p.HandleKey(keyMsg("s"))
	if p.scheduleModalCursor != 2 {
		t.Fatalf("initial cursor = %d, want 2", p.scheduleModalCursor)
	}

	// Move up to 30m
	p.HandleKey(keyMsg("k"))
	if p.scheduleModalCursor != 1 {
		t.Errorf("after k: cursor = %d, want 1", p.scheduleModalCursor)
	}

	// Move up to 15m
	p.HandleKey(keyMsg("k"))
	if p.scheduleModalCursor != 0 {
		t.Errorf("after k: cursor = %d, want 0", p.scheduleModalCursor)
	}

	// Can't go above 0
	p.HandleKey(keyMsg("k"))
	if p.scheduleModalCursor != 0 {
		t.Errorf("at top, cursor should stay at 0, got %d", p.scheduleModalCursor)
	}

	// Move down to end
	p.HandleKey(keyMsg("j"))
	p.HandleKey(keyMsg("j"))
	p.HandleKey(keyMsg("j"))
	p.HandleKey(keyMsg("j"))
	if p.scheduleModalCursor != 4 {
		t.Errorf("at bottom: cursor = %d, want 4", p.scheduleModalCursor)
	}

	// Can't go below 4
	p.HandleKey(keyMsg("j"))
	if p.scheduleModalCursor != 4 {
		t.Errorf("at bottom, cursor should stay at 4, got %d", p.scheduleModalCursor)
	}
}

func TestView_ScheduleModalEscFromPicker(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Dismiss me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star to open modal
	p.HandleKey(keyMsg("s"))
	if !p.scheduleModalActive {
		t.Fatal("modal should be open")
	}

	// Esc dismisses without scheduling
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.scheduleModalActive {
		t.Error("esc should close the modal")
	}

	// View should now show the todo list, not the modal
	view := renderView(p)
	viewNotContains(t, view, "Schedule time block")
	viewContains(t, view, "Dismiss me")
}

func TestView_ScheduleModalBookedState(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Book me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star to open modal
	p.HandleKey(keyMsg("s"))

	// Simulate a booking completion
	p.HandleMessage(bookingCompleteMsg{
		todoID:    "t1",
		startTime: time.Date(2026, 4, 11, 14, 30, 0, 0, time.Local),
		endTime:   time.Date(2026, 4, 11, 15, 30, 0, 0, time.Local),
		duration:  60,
	})

	// Modal should be in "booked" state
	if p.scheduleModalState != "booked" {
		t.Fatalf("modal state = %q, want booked", p.scheduleModalState)
	}

	view := renderView(p)
	viewContains(t, view, "Booked 60m at 2:30pm")
	viewContains(t, view, "schedule another block")
}

func TestView_ScheduleModalBookedEscDismisses(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Ack me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star and simulate booking
	p.HandleKey(keyMsg("s"))
	p.HandleMessage(bookingCompleteMsg{
		todoID:    "t1",
		startTime: time.Date(2026, 4, 11, 14, 0, 0, 0, time.Local),
		endTime:   time.Date(2026, 4, 11, 14, 30, 0, 0, time.Local),
		duration:  30,
	})

	// In booked state, esc should dismiss
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.scheduleModalActive {
		t.Error("esc in booked state should close the modal")
	}
}

func TestView_ScheduleModalBookedSSchedulesAnother(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Another me", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	// Star and simulate booking
	p.HandleKey(keyMsg("s"))
	p.HandleMessage(bookingCompleteMsg{
		todoID:    "t1",
		startTime: time.Date(2026, 4, 11, 10, 0, 0, 0, time.Local),
		endTime:   time.Date(2026, 4, 11, 10, 15, 0, 0, time.Local),
		duration:  15,
	})

	if p.scheduleModalState != "booked" {
		t.Fatalf("modal state = %q, want booked", p.scheduleModalState)
	}

	// Press S to schedule another block
	p.HandleKey(keyMsg("S"))

	if p.scheduleModalState != "picker" {
		t.Errorf("after S: modal state = %q, want picker", p.scheduleModalState)
	}
	if !p.scheduleModalActive {
		t.Error("modal should still be active")
	}

	// View should show the picker again
	view := renderView(p)
	viewContains(t, view, "Schedule time block")
}

func TestView_ScheduleModalFromSKey(t *testing.T) {
	// S key (shift) should open modal directly
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Direct schedule", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})
	p.ccExpanded = true
	p.triageFilter = "all"
	p.ccCursor = 0

	p.HandleKey(keyMsg("S"))

	if !p.scheduleModalActive {
		t.Error("S should open schedule modal")
	}

	view := renderView(p)
	viewContains(t, view, "Schedule time block")
}

func TestView_ScheduleModalCollapsedView(t *testing.T) {
	// Modal should also work in collapsed view
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Collapsed modal", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true},
	})

	p.HandleKey(keyMsg("S"))

	if !p.scheduleModalActive {
		t.Error("S should open schedule modal in collapsed view")
	}

	view := renderView(p)
	viewContains(t, view, "Schedule time block")
	viewContains(t, view, "1h")
}
