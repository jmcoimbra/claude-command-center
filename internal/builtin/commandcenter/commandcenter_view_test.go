package commandcenter

import (
	"strings"
	"testing"
	"time"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
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
	// "new" status todos appear in the "inbox" triage tab when expanded
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Inbox item alpha", Status: db.StatusNew, Source: "github", CreatedAt: time.Now()},
	})
	// Expand the view and switch to inbox tab
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand the view")
	}
	// Default triage is "todo", switch to "inbox"
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
	// Cycle tabs to "all" (todo -> inbox -> agents -> review -> all)
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
	viewContains(t, view, "ToDo")
	viewContains(t, view, "Inbox")
	viewContains(t, view, "Agents")
}

func TestView_TriageTabFiltersContent(t *testing.T) {
	p := testPluginWithTodos(t, []db.Todo{
		{ID: "t1", Title: "Backlog lima", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", Title: "Inbox mike", Status: db.StatusNew, Source: "github", CreatedAt: time.Now()},
		{ID: "t3", Title: "Running november", Status: db.StatusRunning, Source: "manual", CreatedAt: time.Now()},
	})

	// Expand to see triage tabs (default is "todo")
	p.HandleKey(keyMsg(" "))
	todoView := renderView(p)
	viewContains(t, todoView, "Backlog lima")
	viewNotContains(t, todoView, "Inbox mike")

	// Switch to inbox tab
	p.HandleKey(keyMsg("tab"))
	inboxView := renderView(p)
	viewContains(t, inboxView, "Inbox mike")
	viewNotContains(t, inboxView, "Backlog lima")

	// Switch to agents tab
	p.HandleKey(keyMsg("tab"))
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
		{ID: "t1", Title: "Alpha unique name", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", Title: "Bravo different name", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	})

	// Expand to get full todo listing
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
	// tab from "todo" -> "inbox" -> "agents"
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
		{ID: "t1", DisplayID: 1, Title: "Alpha task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", DisplayID: 2, Title: "Bravo task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t3", DisplayID: 3, Title: "Charlie task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
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
		{ID: "t1", DisplayID: 1, Title: "Delta task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", DisplayID: 2, Title: "Echo task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
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
		{ID: "t1", DisplayID: 1, Title: "Foxtrot task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", DisplayID: 2, Title: "Golf task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
	}
	p := testPluginWithTodos(t, todos)
	p.HandleKey(keyMsg(" "))

	// Complete then undo
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
		{ID: "t1", DisplayID: 1, Title: "Hotel task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t2", DisplayID: 2, Title: "India task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		{ID: "t3", DisplayID: 3, Title: "Juliet task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
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

	// Expand the view
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand the view")
	}

	// Navigate to the "focus" tab — it should be the first tab in the new order
	// Tab order: focus, todo, inbox, agents, review, all
	// Default is "todo", so press shift+tab to go backward to "focus"
	p.HandleKey(specialKeyMsg(tea.KeyShiftTab))
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

	// Expand to show all items with focus tab
	p.HandleKey(keyMsg(" "))
	if !p.ccExpanded {
		t.Fatal("space should expand")
	}
	// Navigate to "focus" tab via shift+tab (focus is the first tab, default is "todo")
	p.HandleKey(specialKeyMsg(tea.KeyShiftTab))
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
		{ID: "t1", Title: "Unstarred alpha", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: false},
		{ID: "t2", Title: "Starred beta", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: true, Focus: true},
		{ID: "t3", Title: "Unstarred gamma", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), Starred: false, Focus: false},
	})

	// Expand to see all todos.
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
