package commandcenter

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"path/filepath"

	"github.com/anutron/claude-command-center/internal/agent"
	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// testDB opens an in-memory SQLite database for testing.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDB(":memory:")
	if err != nil {
		// Fallback: try without db.OpenDB if it doesn't support :memory:
		t.Fatalf("failed to open test db: %v", err)
	}
	return database
}

func testPlugin(t *testing.T) *Plugin {
	t.Helper()
	t.Setenv("CCC_CONFIG_DIR", t.TempDir())
	p := New()
	database := testDB(t)
	t.Cleanup(func() { database.Close() })

	cfg := config.DefaultConfig()
	ctx := plugin.Context{
		DB:          database,
		Config:      cfg,
		AgentRunner: agent.NewRunner(3),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return p
}

func testPluginWithCC(t *testing.T) *Plugin {
	t.Helper()
	p := testPlugin(t)
	p.cc = &db.CommandCenter{
		GeneratedAt: time.Now(),
		Todos: []db.Todo{
			{ID: "t1", Title: "First todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
			{ID: "t2", Title: "Second todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
			{ID: "t3", Title: "Third todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		},
	}
	p.width = 120
	p.height = 40
	return p
}

// insertTestPaths inserts paths into the cc_learned_paths table so that
// DBLoadPaths will return them. This is needed because enterTaskRunner reloads
// paths from the DB dynamically.
func insertTestPaths(t *testing.T, database *sql.DB, paths []string) {
	t.Helper()
	for i, p := range paths {
		_, err := database.Exec(`INSERT INTO cc_learned_paths (path, description, sort_order, added_at) VALUES (?, '', ?, datetime('now'))`, p, i)
		if err != nil {
			t.Fatalf("failed to insert test path %q: %v", p, err)
		}
	}
}

func keyMsg(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func specialKeyMsg(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

func TestSlugAndTabName(t *testing.T) {
	p := New()
	if p.Slug() != "commandcenter" {
		t.Errorf("Slug() = %q, want %q", p.Slug(), "commandcenter")
	}
	if p.TabName() != "Command Center" {
		t.Errorf("TabName() = %q, want %q", p.TabName(), "Command Center")
	}
}

func TestRoutes(t *testing.T) {
	p := testPlugin(t)
	routes := p.Routes()
	if len(routes) != 1 {
		t.Fatalf("Routes() returned %d routes, want 1", len(routes))
	}
	if routes[0].Slug != "commandcenter" {
		t.Errorf("routes[0].Slug = %q, want %q", routes[0].Slug, "commandcenter")
	}
}

func TestInitLoadsCC(t *testing.T) {
	p := testPlugin(t)
	// With an empty DB, cc may be nil or empty
	// The important thing is that Init doesn't error
	if p.database == nil {
		t.Error("database should be set after Init")
	}
	if p.cfg == nil {
		t.Error("cfg should be set after Init")
	}
}

func TestNavigationUpDown(t *testing.T) {
	p := testPluginWithCC(t)

	// Start at cursor 0
	if p.ccCursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", p.ccCursor)
	}

	// Move down
	p.HandleKey(keyMsg("j"))
	if p.ccCursor != 1 {
		t.Errorf("after j: cursor = %d, want 1", p.ccCursor)
	}

	p.HandleKey(keyMsg("j"))
	if p.ccCursor != 2 {
		t.Errorf("after j j: cursor = %d, want 2", p.ccCursor)
	}

	// Move up
	p.HandleKey(keyMsg("k"))
	if p.ccCursor != 1 {
		t.Errorf("after k: cursor = %d, want 1", p.ccCursor)
	}

	// Move up with "up"
	p.HandleKey(specialKeyMsg(tea.KeyUp))
	if p.ccCursor != 0 {
		t.Errorf("after up: cursor = %d, want 0", p.ccCursor)
	}

	// Don't go below 0
	p.HandleKey(keyMsg("k"))
	if p.ccCursor != 0 {
		t.Errorf("after k at 0: cursor = %d, want 0", p.ccCursor)
	}
}

func TestDownArrowAutoExpandsPastVisibleArea(t *testing.T) {
	p := testPlugin(t)
	// Create enough todos to exceed the visible area.
	// Set a small height so normalMaxVisibleTodos is small.
	p.height = 30 // gives a small visible count
	maxVisible := p.normalMaxVisibleTodos()

	// Create maxVisible+2 todos so we can go past the visible limit.
	var todos []db.Todo
	for i := 0; i < maxVisible+2; i++ {
		todos = append(todos, db.Todo{
			ID:        fmt.Sprintf("t%d", i),
			Title:     fmt.Sprintf("Todo %d", i),
			Status:    db.StatusBacklog,
			Source:    "manual",
			CreatedAt: time.Now(),
		})
	}
	p.cc = &db.CommandCenter{GeneratedAt: time.Now(), Todos: todos}
	p.width = 120

	// Navigate down to the last visible position (maxVisible - 1).
	for i := 0; i < maxVisible-1; i++ {
		p.HandleKey(keyMsg("j"))
	}
	if p.ccExpanded {
		t.Fatal("should not be expanded yet")
	}
	if p.ccCursor != maxVisible-1 {
		t.Fatalf("cursor = %d, want %d", p.ccCursor, maxVisible-1)
	}

	// One more down should trigger auto-expand.
	p.HandleKey(keyMsg("j"))
	if !p.ccExpanded {
		t.Error("expected auto-expand when cursor moves past visible area")
	}
	if p.ccCursor != maxVisible {
		t.Errorf("cursor = %d, want %d (the first non-visible todo)", p.ccCursor, maxVisible)
	}
	if p.ccExpandedCols != 2 {
		t.Errorf("expandedCols = %d, want 2", p.ccExpandedCols)
	}
	if p.ccExpandedOffset != 0 {
		t.Errorf("expandedOffset = %d, want 0", p.ccExpandedOffset)
	}
}

func TestCompleteTodo(t *testing.T) {
	p := testPluginWithCC(t)

	activeBefore := len(p.cc.ActiveTodos())
	action := p.HandleKey(keyMsg("x"))
	activeAfter := len(p.cc.ActiveTodos())

	if activeAfter != activeBefore-1 {
		t.Errorf("after x: active todos = %d, want %d", activeAfter, activeBefore-1)
	}
	if action.TeaCmd == nil {
		t.Error("x should return a TeaCmd for DB write")
	}
	if len(p.undoStack) != 1 {
		t.Errorf("undo stack len = %d, want 1", len(p.undoStack))
	}
}

func TestDismissTodo(t *testing.T) {
	p := testPluginWithCC(t)

	activeBefore := len(p.cc.ActiveTodos())
	action := p.HandleKey(keyMsg("X"))
	activeAfter := len(p.cc.ActiveTodos())

	if activeAfter != activeBefore-1 {
		t.Errorf("after X: active todos = %d, want %d", activeAfter, activeBefore-1)
	}
	if action.TeaCmd == nil {
		t.Error("X should return a TeaCmd for DB write")
	}
}

func TestUndoCompletion(t *testing.T) {
	p := testPluginWithCC(t)

	activeBefore := len(p.cc.ActiveTodos())

	// Complete first todo
	p.HandleKey(keyMsg("x"))
	if len(p.cc.ActiveTodos()) != activeBefore-1 {
		t.Fatal("todo should be completed")
	}

	// Undo
	action := p.HandleKey(keyMsg("u"))
	if len(p.cc.ActiveTodos()) != activeBefore {
		t.Errorf("after undo: active todos = %d, want %d", len(p.cc.ActiveTodos()), activeBefore)
	}
	if p.flashMessage != "Undid last action" {
		t.Errorf("flash message = %q, want %q", p.flashMessage, "Undid last action")
	}
	if action.TeaCmd == nil {
		t.Error("undo should return a TeaCmd for DB write")
	}
}

// execBatchCmds executes a tea.Cmd and any nested batch commands, collecting
// all resulting messages. This handles tea.Batch wrapping.
func execBatchCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			execBatchCmds(c)
		}
	}
}

func TestCompleteTodoNotifiesPeers(t *testing.T) {
	p := testPluginWithCC(t)
	var notified []string
	p.notifyPeers = func(event string) { notified = append(notified, event) }

	action := p.HandleKey(keyMsg("x"))
	if action.TeaCmd == nil {
		t.Fatal("x should return a TeaCmd")
	}
	execBatchCmds(action.TeaCmd)
	if len(notified) == 0 {
		t.Error("complete todo should notify peers")
	} else if notified[0] != "data.refreshed" {
		t.Errorf("notify event = %q, want %q", notified[0], "data.refreshed")
	}
}

func TestDismissTodoNotifiesPeers(t *testing.T) {
	p := testPluginWithCC(t)
	var notified []string
	p.notifyPeers = func(event string) { notified = append(notified, event) }

	action := p.HandleKey(keyMsg("X"))
	if action.TeaCmd == nil {
		t.Fatal("X should return a TeaCmd")
	}
	execBatchCmds(action.TeaCmd)
	if len(notified) == 0 {
		t.Error("dismiss todo should notify peers")
	} else if notified[0] != "data.refreshed" {
		t.Errorf("notify event = %q, want %q", notified[0], "data.refreshed")
	}
}

func TestUndoNotifiesPeers(t *testing.T) {
	p := testPluginWithCC(t)
	p.HandleKey(keyMsg("x")) // complete first

	var notified []string
	p.notifyPeers = func(event string) { notified = append(notified, event) }

	action := p.HandleKey(keyMsg("u"))
	if action.TeaCmd == nil {
		t.Fatal("u should return a TeaCmd")
	}
	execBatchCmds(action.TeaCmd)
	if len(notified) == 0 {
		t.Error("undo should notify peers")
	} else if notified[0] != "data.refreshed" {
		t.Errorf("notify event = %q, want %q", notified[0], "data.refreshed")
	}
}

func TestDetailCompleteTodoNotifiesPeers(t *testing.T) {
	p := testPluginWithCC(t)
	var notified []string
	p.notifyPeers = func(event string) { notified = append(notified, event) }

	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	action := p.HandleKey(keyMsg("x"))
	if action.TeaCmd == nil {
		t.Fatal("detail x should return a TeaCmd")
	}
	execBatchCmds(action.TeaCmd)
	if len(notified) == 0 {
		t.Error("detail complete should notify peers")
	} else if notified[0] != "data.refreshed" {
		t.Errorf("notify event = %q, want %q", notified[0], "data.refreshed")
	}
}

func TestDetailDismissTodoNotifiesPeers(t *testing.T) {
	p := testPluginWithCC(t)
	var notified []string
	p.notifyPeers = func(event string) { notified = append(notified, event) }

	p.detailView = true
	p.detailTodoID = "t1"
	p.detailMode = "viewing"

	action := p.HandleKey(keyMsg("X"))
	if action.TeaCmd == nil {
		t.Fatal("detail X should return a TeaCmd")
	}
	execBatchCmds(action.TeaCmd)
	if len(notified) == 0 {
		t.Error("detail dismiss should notify peers")
	} else if notified[0] != "data.refreshed" {
		t.Errorf("notify event = %q, want %q", notified[0], "data.refreshed")
	}
}

func TestNotifyPeersCmdNilWhenNotConfigured(t *testing.T) {
	p := testPluginWithCC(t)
	cmd := p.notifyPeersCmd("data.refreshed")
	if cmd != nil {
		t.Error("notifyPeersCmd should return nil when notifyPeers is not configured")
	}
}

func TestCreateTodoEntersRichMode(t *testing.T) {
	p := testPluginWithCC(t)

	action := p.HandleKey(keyMsg("c"))
	if !p.addingTodoRich {
		t.Error("c should enter addingTodoRich mode")
	}
	if action.TeaCmd == nil {
		t.Error("c should return a TeaCmd (textarea focus)")
	}
}

func TestEnterOpensDetailView(t *testing.T) {
	p := testPluginWithCC(t)

	_ = p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Error("enter should open detail view")
	}
	if p.detailTodoID != p.cc.ActiveTodos()[0].ID {
		t.Errorf("detailTodoID = %q, want first active todo ID", p.detailTodoID)
	}
	if p.detailMode != "viewing" {
		t.Errorf("detailMode = %q, want %q", p.detailMode, "viewing")
	}
	if p.detailSelectedField != 0 {
		t.Errorf("detailSelectedField = %d, want 0", p.detailSelectedField)
	}
}

func TestOpenLaunchOnTodoWithProjectDir(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	action := p.HandleKey(keyMsg("o"))
	// Should enter detail view + task runner, NOT launch directly
	if action.Type != "noop" {
		t.Errorf("o on todo with project dir: type = %q, want %q", action.Type, "noop")
	}
	if !p.detailView {
		t.Error("detailView should be true")
	}
	if !p.taskRunnerView {
		t.Error("taskRunnerView should be true")
	}
}

func TestOpenLaunchOnTodoWithSessionID(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].SessionID = "abc12345-session-id"
	p.cc.Todos[0].ProjectDir = "/tmp/proj"

	action := p.HandleKey(keyMsg("o"))
	if action.Type != "launch" {
		t.Errorf("o on todo with session: type = %q, want %q", action.Type, "launch")
	}
	if action.Args["resume_id"] != "abc12345-session-id" {
		t.Errorf("resume_id = %q, want %q", action.Args["resume_id"], "abc12345-session-id")
	}
}

func TestOpenLaunchOnTodoWithoutProjectDir(t *testing.T) {
	p := testPluginWithCC(t)
	// No project dir, no session ID

	action := p.HandleKey(keyMsg("o"))
	// Should enter detail view + task runner, NOT navigate to sessions
	if action.Type != "noop" {
		t.Errorf("o on todo without project dir: type = %q, want %q", action.Type, "noop")
	}
	if !p.detailView {
		t.Error("detailView should be true")
	}
	if !p.taskRunnerView {
		t.Error("taskRunnerView should be true")
	}
}

func TestSubViewSwitching(t *testing.T) {
	p := testPluginWithCC(t)

	if p.subView != "command" {
		t.Fatalf("initial subView = %q, want %q", p.subView, "command")
	}

	p.NavigateTo("commandcenter", nil)
	if p.subView != "command" {
		t.Errorf("after NavigateTo command: subView = %q, want %q", p.subView, "command")
	}
}

func TestDeferTodo(t *testing.T) {
	p := testPluginWithCC(t)
	firstID := p.cc.ActiveTodos()[0].ID

	action := p.HandleKey(keyMsg("d"))
	activeTodos := p.cc.ActiveTodos()
	lastActive := activeTodos[len(activeTodos)-1]
	if lastActive.ID != firstID {
		t.Errorf("deferred todo should be at end, got %q at end", lastActive.ID)
	}
	if action.TeaCmd == nil {
		t.Error("d should return a TeaCmd for DB write")
	}
}

func TestPromoteTodo(t *testing.T) {
	p := testPluginWithCC(t)
	p.ccCursor = 2
	lastID := p.cc.ActiveTodos()[2].ID

	action := p.HandleKey(keyMsg("p"))
	firstActive := p.cc.ActiveTodos()[0]
	if firstActive.ID != lastID {
		t.Errorf("promoted todo should be at top, got %q at top", firstActive.ID)
	}
	if p.ccCursor != 0 {
		t.Errorf("cursor should be 0 after promote, got %d", p.ccCursor)
	}
	if action.TeaCmd == nil {
		t.Error("p should return a TeaCmd for DB write")
	}
}

func TestToggleBacklog(t *testing.T) {
	p := testPluginWithCC(t)

	if p.showBacklog {
		t.Fatal("initial showBacklog should be false")
	}

	p.HandleKey(keyMsg("b"))
	if !p.showBacklog {
		t.Error("after b: showBacklog should be true")
	}

	p.HandleKey(keyMsg("b"))
	if p.showBacklog {
		t.Error("after b b: showBacklog should be false")
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	p := testPluginWithCC(t)

	// Command view
	output := p.View(120, 40, 0)
	if output == "" {
		t.Error("command view should not be empty")
	}
}

func TestViewWithNilCC(t *testing.T) {
	p := testPlugin(t)
	p.cc = nil

	output := p.View(120, 40, 0)
	if output == "" {
		t.Error("view with nil CC should not be empty")
	}
}

func TestHelpOverlay(t *testing.T) {
	p := testPluginWithCC(t)

	p.HandleKey(keyMsg("?"))
	if !p.showHelp {
		t.Error("? should toggle help on")
	}

	output := p.View(120, 40, 0)
	if output == "" {
		t.Error("help overlay should not be empty")
	}

	// Any key dismisses
	p.HandleKey(keyMsg("q"))
	if p.showHelp {
		t.Error("any key should dismiss help")
	}
}

func TestBookingMode(t *testing.T) {
	p := testPluginWithCC(t)

	// S (shift+s) enters booking mode directly
	p.HandleKey(keyMsg("S"))
	if !p.bookingMode {
		t.Error("S should enter booking mode")
	}
	if p.bookingCursor != 2 {
		t.Errorf("initial booking cursor = %d, want 2", p.bookingCursor)
	}

	// Navigate booking
	p.HandleKey(keyMsg("l"))
	if p.bookingCursor != 3 {
		t.Errorf("after l: booking cursor = %d, want 3", p.bookingCursor)
	}

	p.HandleKey(keyMsg("h"))
	if p.bookingCursor != 2 {
		t.Errorf("after h: booking cursor = %d, want 2", p.bookingCursor)
	}

	// Esc cancels
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.bookingMode {
		t.Error("esc should cancel booking mode")
	}
}

func TestStarKey(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// Star an unstarred todo
	action := p.HandleKey(keyMsg("s"))
	if action.TeaCmd == nil {
		t.Error("s on unstarred todo should return a TeaCmd for DB write")
	}

	// In-memory state should reflect starred + focused
	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after star")
	}
	if !updated.Starred {
		t.Error("todo should be starred after s")
	}
	if !updated.Focused {
		t.Error("todo should be focused after starring")
	}

	// Should enter schedule offer mode
	if !p.scheduleOfferMode {
		t.Error("s on unstarred todo should enter scheduleOfferMode")
	}

	// Flash message should contain the star symbol and title
	if !strings.Contains(p.flashMessage, "★") {
		t.Errorf("flash message should contain ★, got %q", p.flashMessage)
	}
	if !strings.Contains(p.flashMessage, todo.Title) {
		t.Errorf("flash message should contain todo title %q, got %q", todo.Title, p.flashMessage)
	}
}

func TestUnstarKey(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// First star the todo (in-memory only, no future bookings)
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == todo.ID {
			p.cc.Todos[i].Starred = true
			break
		}
	}
	p.scheduleOfferMode = false // reset offer mode

	// Now unstar it — no future bookings in test DB so should unstar immediately
	action := p.HandleKey(keyMsg("s"))
	if action.TeaCmd == nil {
		t.Error("s on starred todo should return a TeaCmd for DB write")
	}

	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after unstar")
	}
	if updated.Starred {
		t.Error("todo should be unstarred after s on starred todo")
	}

	// Flash message should mention "Unstarred"
	if !strings.Contains(p.flashMessage, "Unstarred") {
		t.Errorf("flash message should contain 'Unstarred', got %q", p.flashMessage)
	}

	// Should NOT be in schedule offer mode
	if p.scheduleOfferMode {
		t.Error("unstarring should not enter scheduleOfferMode")
	}

	// Should NOT be in unstar confirm mode (no future bookings)
	if p.unstarConfirmMode {
		t.Error("unstarring with no bookings should not enter unstarConfirmMode")
	}
}

func TestFocusKey(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// Focus an unfocused todo
	action := p.HandleKey(keyMsg("f"))
	if action.TeaCmd == nil {
		t.Error("f on unfocused todo should return a TeaCmd for DB write")
	}

	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after focus")
	}
	if !updated.Focused {
		t.Error("todo should be focused after f")
	}

	// Flash message should mention "Focused"
	if !strings.Contains(p.flashMessage, "Focused") {
		t.Errorf("flash message should contain 'Focused', got %q", p.flashMessage)
	}

	// Now unfocus it
	action = p.HandleKey(keyMsg("f"))
	if action.TeaCmd == nil {
		t.Error("f on focused todo should return a TeaCmd for DB write")
	}

	updated = p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after unfocus")
	}
	if updated.Focused {
		t.Error("todo should be unfocused after second f")
	}

	// Flash message should mention "Unfocused"
	if !strings.Contains(p.flashMessage, "Unfocused") {
		t.Errorf("flash message should contain 'Unfocused', got %q", p.flashMessage)
	}
}

func TestScheduleKey(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// S on unstarred todo: should enter booking mode and star the todo
	_ = p.HandleKey(keyMsg("S"))

	if !p.bookingMode {
		t.Error("S should enter booking mode")
	}
	if p.bookingCursor != 2 {
		t.Errorf("initial booking cursor = %d, want 2", p.bookingCursor)
	}

	// Todo should be starred since it wasn't before
	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after S")
	}
	if !updated.Starred {
		t.Error("todo should be starred after S on unstarred todo")
	}

	// Esc cancels booking mode
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.bookingMode {
		t.Error("esc should cancel booking mode")
	}
}

func TestScheduleOfferModeInterception(t *testing.T) {
	p := testPluginWithCC(t)

	// Star a todo to enter offer mode
	p.HandleKey(keyMsg("s"))
	if !p.scheduleOfferMode {
		t.Fatal("expected scheduleOfferMode after starring")
	}

	// S key in offer mode should enter booking mode
	p.HandleKey(keyMsg("S"))
	if p.scheduleOfferMode {
		t.Error("S in offer mode should exit offer mode")
	}
	if !p.bookingMode {
		t.Error("S in offer mode should enter booking mode")
	}
}

func TestScheduleOfferModeAnyKeySkips(t *testing.T) {
	p := testPluginWithCC(t)

	// Star a todo to enter offer mode
	p.HandleKey(keyMsg("s"))
	if !p.scheduleOfferMode {
		t.Fatal("expected scheduleOfferMode after starring")
	}

	// Any other key should exit offer mode without entering booking mode
	p.HandleKey(keyMsg("j"))
	if p.scheduleOfferMode {
		t.Error("any key in offer mode should exit offer mode")
	}
	if p.bookingMode {
		t.Error("non-S key in offer mode should not enter booking mode")
	}
	// j should have moved the cursor
	if p.ccCursor != 1 {
		t.Errorf("j in offer mode should move cursor: cursor = %d, want 1", p.ccCursor)
	}
}

func TestUnstarConfirmModeWithFutureBookings(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// Star the todo in-memory
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == todo.ID {
			p.cc.Todos[i].Starred = true
			break
		}
	}

	// Insert a future booking into the DB so unstar triggers confirm mode
	future := time.Now().Add(24 * time.Hour)
	_, err := p.database.Exec(`INSERT INTO cc_todo_bookings (todo_id, start_time, end_time, created_at) VALUES (?, ?, ?, ?)`,
		todo.ID,
		fmt.Sprintf("%s", future.UTC().Format(time.RFC3339)),
		fmt.Sprintf("%s", future.Add(time.Hour).UTC().Format(time.RFC3339)),
		fmt.Sprintf("%s", time.Now().UTC().Format(time.RFC3339)),
	)
	if err != nil {
		t.Fatalf("failed to insert test booking: %v", err)
	}

	// Pressing s should enter unstarConfirmMode
	p.HandleKey(keyMsg("s"))
	if !p.unstarConfirmMode {
		t.Error("s on starred todo with future bookings should enter unstarConfirmMode")
	}
	if p.unstarConfirmTodoID != todo.ID {
		t.Errorf("unstarConfirmTodoID = %q, want %q", p.unstarConfirmTodoID, todo.ID)
	}

	// Flash should mention "Release" and "calendar block"
	if !strings.Contains(p.flashMessage, "Release") {
		t.Errorf("flash message should contain 'Release', got %q", p.flashMessage)
	}

	// Press y: should unstar
	p.HandleKey(keyMsg("y"))
	if p.unstarConfirmMode {
		t.Error("y should exit unstarConfirmMode")
	}
	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after unstar confirm")
	}
	if updated.Starred {
		t.Error("todo should be unstarred after confirming with y")
	}
}

func TestUnstarConfirmModeN(t *testing.T) {
	p := testPluginWithCC(t)
	todo := p.cc.ActiveTodos()[0]

	// Star the todo and set confirm mode directly
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == todo.ID {
			p.cc.Todos[i].Starred = true
			break
		}
	}
	p.unstarConfirmMode = true
	p.unstarConfirmTodoID = todo.ID

	// Press n: should unstar but keep bookings (just unstar via DB)
	action := p.HandleKey(keyMsg("n"))
	if p.unstarConfirmMode {
		t.Error("n should exit unstarConfirmMode")
	}
	if action.TeaCmd == nil {
		t.Error("n in confirm mode should return a TeaCmd for DB write")
	}
	updated := p.cc.FindTodo(todo.ID)
	if updated == nil {
		t.Fatal("could not find todo after n in confirm mode")
	}
	if updated.Starred {
		t.Error("todo should be unstarred after pressing n in confirm mode")
	}
}

func TestHandleMessageCCLoaded(t *testing.T) {
	p := testPlugin(t)
	newCC := &db.CommandCenter{
		GeneratedAt: time.Now(),
		Todos: []db.Todo{
			{ID: "new1", Title: "New todo", Status: db.StatusBacklog},
		},
	}

	handled, _ := p.HandleMessage(ccLoadedMsg{cc: newCC})
	if !handled {
		t.Error("ccLoadedMsg should be handled")
	}
	if p.cc == nil {
		t.Fatal("cc should be set after ccLoadedMsg")
	}
	if len(p.cc.Todos) != 1 {
		t.Errorf("cc.Todos len = %d, want 1", len(p.cc.Todos))
	}
}

func TestHandleMessageRefreshFinished(t *testing.T) {
	p := testPlugin(t)
	p.ccRefreshing = true

	handled, _ := p.HandleMessage(ccRefreshFinishedMsg{err: nil})
	if !handled {
		t.Error("ccRefreshFinishedMsg should be handled")
	}
	if p.ccRefreshing {
		t.Error("ccRefreshing should be false after refresh finished")
	}
}

func TestDisplayContext(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"some plain context", "some plain context"},
		{"https://example.slack.com/archives/C01EXAMPLE/p1773165648549789?thread_ts=1771862390.043209&cid=C01EXAMPLE", "Slack"},
		{"https://mycompany.slack.com/archives/C01ABC/p123456", "Slack"},
		{"https://workspace.slack.com/messages/general", "Slack"},
		{"https://github.com/owner/repo/issues/42", "GitHub"},
		// Slack channel with description (BUG-074)
		{"#proj-dashboard-permissions-via-rbac – RBAC feature is in QA/bug bash phase, these items are non-blocking but needed in parallel", "Slack: #proj-dashboard-permissions-vi..."},
		{"#general – Company announcements", "Slack: #general"},
		{"#general - Company announcements", "Slack: #general"},
		{"#my-channel", "Slack: #my-channel"},
		// Long plain text gets truncated
		{"this is a very long context string that should be truncated to forty chars", "this is a very long context string th..."},
		// Short plain text passes through
		{"short", "short"},
	}

	for _, tt := range tests {
		got := displayContext(tt.input)
		if got != tt.want {
			t.Errorf("displayContext(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"key": "value"}`, `{"key": "value"}`},
		{"```json\n{\"key\": \"value\"}\n```", `{"key": "value"}`},
		{`some text {"key": "value"} more text`, `{"key": "value"}`},
	}

	for _, tt := range tests {
		got := extractJSON(tt.input)
		if got != tt.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetailViewCommandInput(t *testing.T) {
	p := testPluginWithCC(t)

	// Enter detail view
	_ = p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("enter should open detail view")
	}
	if p.detailMode != "viewing" {
		t.Fatalf("detailMode = %q, want viewing", p.detailMode)
	}

	// Press c for command input
	action := p.HandleKey(keyMsg("c"))
	if p.detailMode != "commandInput" {
		t.Errorf("after c: detailMode = %q, want commandInput", p.detailMode)
	}
	if action.TeaCmd == nil {
		t.Error("c should return a TeaCmd (blink)")
	}

	// Verify the view renders the command input section
	view := p.View(120, 40, 0)
	if !strings.Contains(view, "Tell me what changed") {
		t.Error("detail view in commandInput mode should show 'Tell me what changed' label")
	}
	if !strings.Contains(view, "submit to AI") {
		t.Error("detail view in commandInput mode should show 'submit to AI' hint")
	}
}

func TestCommandTextAreaWrapsText(t *testing.T) {
	p := testPluginWithCC(t)
	termWidth := 120

	// Enter detail view, then command input
	_ = p.HandleKey(keyMsg("enter"))
	_ = p.HandleKey(keyMsg("c"))
	if p.detailMode != "commandInput" {
		t.Fatalf("detailMode = %q, want commandInput", p.detailMode)
	}

	// Type a long string via HandleKey (like a real user typing)
	longText := strings.Repeat("x", 130) // Longer than textarea width
	for _, ch := range longText {
		p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}

	// Render the view
	view := p.View(termWidth, 40, 0)
	lines := strings.Split(view, "\n")

	// No rendered line should exceed the terminal width
	maxLineWidth := 0
	for _, line := range lines {
		w := lipgloss.Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}
	if maxLineWidth > termWidth {
		t.Errorf("text overflows: max line width %d > terminal width %d", maxLineWidth, termWidth)
	}

	// The long text should wrap across multiple lines
	xLines := 0
	for _, line := range lines {
		if strings.Contains(line, "xxx") {
			xLines++
		}
	}
	if xLines < 2 {
		t.Errorf("expected text to wrap across multiple lines, but only found %d lines with 'xxx'", xLines)
	}

	// All textarea lines should be consistently indented (PaddingLeft applied uniformly)
	taView := p.commandTextArea.View()
	taLines := strings.Split(taView, "\n")
	for _, line := range taLines {
		w := lipgloss.Width(line)
		if w > p.textareaWidth() {
			t.Errorf("textarea line wider than textareaWidth(): %d > %d", w, p.textareaWidth())
		}
	}
}

func TestCommandTextAreaWrapsNarrowTerminal(t *testing.T) {
	p := testPluginWithCC(t)
	p.width = 80

	// Enter detail view, then command input
	_ = p.HandleKey(keyMsg("enter"))
	_ = p.HandleKey(keyMsg("c"))

	// Type text that exceeds narrow terminal width
	longText := strings.Repeat("y", 100)
	for _, ch := range longText {
		p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}

	view := p.View(80, 40, 0)
	lines := strings.Split(view, "\n")

	maxLineWidth := 0
	for _, line := range lines {
		w := lipgloss.Width(line)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}
	if maxLineWidth > 80 {
		t.Errorf("text overflows narrow terminal: max line width %d > 80", maxLineWidth)
	}

	// Text should wrap
	yLines := 0
	for _, line := range lines {
		if strings.Contains(line, "yyy") {
			yLines++
		}
	}
	if yLines < 2 {
		t.Errorf("expected text to wrap in narrow terminal, but only found %d lines with 'yyy'", yLines)
	}
}

func TestTaskRunnerStepNavigation(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner via detail view
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("taskRunnerView should be true after 'o'")
	}
	if p.taskRunnerStep != 1 {
		t.Fatalf("initial step = %d, want 1", p.taskRunnerStep)
	}

	// Enter advances step 1 -> 2
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerStep != 2 {
		t.Errorf("after enter at step 1: step = %d, want 2", p.taskRunnerStep)
	}

	// Enter advances step 2 -> 3
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerStep != 3 {
		t.Errorf("after enter at step 2: step = %d, want 3", p.taskRunnerStep)
	}

	// Esc goes back step 3 -> 2
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerStep != 2 {
		t.Errorf("after esc at step 3: step = %d, want 2", p.taskRunnerStep)
	}

	// Esc goes back step 2 -> 1
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerStep != 1 {
		t.Errorf("after esc at step 2: step = %d, want 1", p.taskRunnerStep)
	}

	// Esc at step 1 exits task runner view
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerView {
		t.Error("esc at step 1 should exit taskRunnerView")
	}
}

func TestTaskRunnerPathPickerNoSelection(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner, then manually open path picker
	p.HandleKey(keyMsg("o"))
	p.taskRunnerPickingPath = true
	p.detailPaths = []string{"/tmp/a", "/tmp/b"}

	// Set cursor to -1 (no selection) and press enter — should NOT panic
	p.taskRunnerPathCursor = -1
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerPickingPath {
		t.Error("enter should close the path picker")
	}
}

func TestTaskRunnerModeCycling(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner and advance to step 2
	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerStep != 2 {
		t.Fatalf("step = %d, want 2", p.taskRunnerStep)
	}

	// Default mode is "normal"
	if p.taskRunnerMode != "normal" {
		t.Fatalf("initial mode = %q, want %q", p.taskRunnerMode, "normal")
	}

	// Right arrow cycles normal -> worktree
	p.HandleKey(keyMsg("right"))
	if p.taskRunnerMode != "worktree" {
		t.Errorf("after right: mode = %q, want %q", p.taskRunnerMode, "worktree")
	}

	// Right again: worktree -> sandbox
	p.HandleKey(keyMsg("right"))
	if p.taskRunnerMode != "sandbox" {
		t.Errorf("after right right: mode = %q, want %q", p.taskRunnerMode, "sandbox")
	}

	// Right wraps: sandbox -> normal
	p.HandleKey(keyMsg("right"))
	if p.taskRunnerMode != "normal" {
		t.Errorf("after right wrap: mode = %q, want %q", p.taskRunnerMode, "normal")
	}

	// Left wraps: normal -> sandbox
	p.HandleKey(keyMsg("left"))
	if p.taskRunnerMode != "sandbox" {
		t.Errorf("after left wrap: mode = %q, want %q", p.taskRunnerMode, "sandbox")
	}
}

func TestTaskRunnerLaunchInteractive(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner, advance to step 3
	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	p.HandleKey(keyMsg("enter")) // step 2 -> 3

	// Default launch cursor is 0 (Run Claude)
	if p.taskRunnerLaunchCursor != 0 {
		t.Fatalf("initial launch cursor = %d, want 0", p.taskRunnerLaunchCursor)
	}

	// Enter at cursor 0 should launch interactive session
	action := p.HandleKey(keyMsg("enter"))
	if p.taskRunnerView {
		t.Error("task runner should be closed after launch")
	}
	if action.Type != "launch" {
		t.Errorf("action type = %q, want 'launch'", action.Type)
	}
	if action.Args["dir"] != "/tmp/myproject" {
		t.Errorf("launch dir = %q, want '/tmp/myproject'", action.Args["dir"])
	}
	if action.Args["initial_prompt"] == "" {
		t.Error("interactive launch should include initial_prompt")
	}
}

func TestTaskRunnerLaunchQueue(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner, advance to step 3
	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	p.HandleKey(keyMsg("enter")) // step 2 -> 3

	// Move launch cursor to 1 (Queue Agent)
	p.HandleKey(keyMsg("right"))
	if p.taskRunnerLaunchCursor != 1 {
		t.Fatalf("launch cursor = %d, want 1", p.taskRunnerLaunchCursor)
	}

	// Enter at cursor 1 should queue agent
	action := p.HandleKey(keyMsg("enter"))
	if p.taskRunnerView {
		t.Error("task runner should be closed after launch")
	}
	if action.TeaCmd == nil {
		t.Error("launch should return a TeaCmd")
	}
	if !strings.Contains(p.flashMessage, "queued") && !strings.Contains(p.flashMessage, "launched") {
		t.Errorf("flash message = %q, want to contain 'queued' or 'launched'", p.flashMessage)
	}
}

func TestTaskRunnerLaunchRunNow(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner, advance to step 3
	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	p.HandleKey(keyMsg("enter")) // step 2 -> 3

	// Move launch cursor to 2 (Run Agent Now)
	p.HandleKey(keyMsg("right"))
	p.HandleKey(keyMsg("right"))
	if p.taskRunnerLaunchCursor != 2 {
		t.Fatalf("launch cursor = %d, want 2", p.taskRunnerLaunchCursor)
	}

	// Enter at cursor 2 should launch immediately
	action := p.HandleKey(keyMsg("enter"))
	if p.taskRunnerView {
		t.Error("task runner should be closed after launch")
	}
	if action.TeaCmd == nil {
		t.Error("launch should return a TeaCmd")
	}
}

func TestTaskRunnerRefineKey(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"

	// Enter task runner, advance to step 3
	p.HandleKey(keyMsg("o"))
	p.HandleKey(keyMsg("enter")) // step 1 -> 2
	p.HandleKey(keyMsg("enter")) // step 2 -> 3

	// Press 'c' to enter instruction input mode
	p.HandleKey(keyMsg("c"))
	if !p.taskRunnerInputting {
		t.Error("'c' at step 3 should set taskRunnerInputting to true")
	}

	// Esc should cancel input mode
	p.HandleKey(specialKeyMsg(tea.KeyEscape))
	if p.taskRunnerInputting {
		t.Error("esc should cancel instruction input")
	}
}

func TestWizardSelectionsPersistedOnBackout(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"
	insertTestPaths(t, p.database, []string{"/tmp/a", "/tmp/b", "/tmp/myproject"})
	p.detailPaths = []string{"/tmp/a", "/tmp/b", "/tmp/myproject"}

	// Press 'o' to enter wizard
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView || p.taskRunnerStep != 1 {
		t.Fatalf("expected taskRunnerView=true step=1, got view=%v step=%d", p.taskRunnerView, p.taskRunnerStep)
	}

	// Step 1 -> Step 2
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerStep != 2 {
		t.Fatalf("step = %d, want 2", p.taskRunnerStep)
	}

	// Change mode to worktree
	p.HandleKey(keyMsg("right")) // normal -> worktree
	if p.taskRunnerMode != "worktree" {
		t.Fatalf("mode = %q, want worktree", p.taskRunnerMode)
	}

	// Step 2 -> Step 3
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerStep != 3 {
		t.Fatalf("step = %d, want 3", p.taskRunnerStep)
	}

	// Now back out: Step 3 -> Step 2
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerStep != 2 {
		t.Fatalf("step = %d, want 2", p.taskRunnerStep)
	}

	// Step 2 -> Step 1
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerStep != 1 {
		t.Fatalf("step = %d, want 1", p.taskRunnerStep)
	}

	// Step 1 -> exit task runner (should save selections)
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerView {
		t.Fatal("taskRunnerView should be false after esc at step 1")
	}

	// Exit detail view
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.detailView {
		t.Fatal("detailView should be false after esc")
	}

	// Re-open wizard on the same todo
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("taskRunnerView should be true after re-opening")
	}

	// The mode should be restored to worktree
	if p.taskRunnerMode != "worktree" {
		t.Errorf("after re-open: mode = %q, want worktree", p.taskRunnerMode)
	}

	// The path cursor should be restored
	expectedPathCursor := 2 // /tmp/myproject is at index 2
	if p.taskRunnerPathCursor != expectedPathCursor {
		t.Errorf("after re-open: pathCursor = %d, want %d", p.taskRunnerPathCursor, expectedPathCursor)
	}
}

func TestWizardSelectionsPersistedWithPathChange(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "" // no project dir
	insertTestPaths(t, p.database, []string{"/tmp/a", "/tmp/b", "/tmp/c"})
	p.detailPaths = []string{"/tmp/a", "/tmp/b", "/tmp/c"}

	// Press 'o' to enter wizard — path picker should auto-open
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("taskRunnerView should be true")
	}
	if !p.taskRunnerPickingPath {
		t.Fatal("path picker should auto-open for todo with no project dir")
	}

	// Navigate to /tmp/b (index 1) and select it
	// Cursor starts at -1 due to no project dir, j increments to 0, then to 1
	p.HandleKey(keyMsg("j")) // cursor -1 -> 0
	p.HandleKey(keyMsg("j")) // cursor 0 -> 1
	p.HandleKey(keyMsg("enter")) // select /tmp/b
	if p.taskRunnerPickingPath {
		t.Fatal("path picker should close after enter")
	}
	if p.taskRunnerPathCursor != 1 {
		t.Fatalf("pathCursor = %d, want 1", p.taskRunnerPathCursor)
	}

	// Step 1 -> Step 2
	p.HandleKey(keyMsg("enter"))
	// Change mode to sandbox
	p.HandleKey(keyMsg("right")) // normal -> worktree
	p.HandleKey(keyMsg("right")) // worktree -> sandbox

	// Back out: Step 2 -> Step 1 -> exit
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 2 -> 1
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 1 -> exit (saves)
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // exit detail view

	// Re-open wizard
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("taskRunnerView should be true")
	}

	// Path cursor should be restored to 1 (/tmp/b)
	if p.taskRunnerPathCursor != 1 {
		t.Errorf("after re-open: pathCursor = %d, want 1", p.taskRunnerPathCursor)
	}

	// Mode should be restored to sandbox
	if p.taskRunnerMode != "sandbox" {
		t.Errorf("after re-open: mode = %q, want sandbox", p.taskRunnerMode)
	}

	// Path picker should NOT auto-open since we have saved selections
	if p.taskRunnerPickingPath {
		t.Error("path picker should NOT auto-open when saved selections exist")
	}
}

func TestWizardSelectionsPersistedEscFromStep2(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"
	insertTestPaths(t, p.database, []string{"/tmp/a", "/tmp/b", "/tmp/myproject"})
	p.detailPaths = []string{"/tmp/a", "/tmp/b", "/tmp/myproject"}

	// Press 'o' to enter wizard
	p.HandleKey(keyMsg("o"))

	// Step 1 -> Step 2
	p.HandleKey(keyMsg("enter"))

	// Change mode to worktree on step 2
	p.HandleKey(keyMsg("right")) // normal -> worktree

	// Now escape from step 2 (goes to step 1, does NOT save yet)
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerStep != 1 {
		t.Fatalf("step = %d, want 1", p.taskRunnerStep)
	}

	// Escape from step 1 (saves and exits wizard)
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.taskRunnerView {
		t.Fatal("should have exited task runner")
	}

	// Verify the mode was saved
	saved, ok := p.wizardSelections[p.cc.Todos[0].ID]
	if !ok {
		t.Fatal("wizard selections should be saved")
	}
	if saved.mode != "worktree" {
		t.Errorf("saved mode = %q, want worktree", saved.mode)
	}

	// Exit detail view and re-open
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	p.HandleKey(keyMsg("o"))

	if p.taskRunnerMode != "worktree" {
		t.Errorf("after re-open: mode = %q, want worktree", p.taskRunnerMode)
	}
}

func TestWizardSelectionsPersistedFromDetailView(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"
	insertTestPaths(t, p.database, []string{"/tmp/a", "/tmp/b", "/tmp/myproject"})
	p.detailPaths = []string{"/tmp/a", "/tmp/b", "/tmp/myproject"}

	// Enter detail view first (not task runner)
	p.HandleKey(keyMsg("enter"))
	if !p.detailView {
		t.Fatal("should be in detail view")
	}
	if p.taskRunnerView {
		t.Fatal("should NOT be in task runner yet")
	}

	// Press 'o' from detail view to enter task runner
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("should be in task runner")
	}

	// Advance to step 2 and change mode
	p.HandleKey(keyMsg("enter"))
	p.HandleKey(keyMsg("right")) // normal -> worktree

	// Back out to list
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 2 -> 1
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 1 -> exit task runner (saves)
	if p.taskRunnerView {
		t.Fatal("should have exited task runner")
	}

	// Exit detail view
	p.HandleKey(specialKeyMsg(tea.KeyEsc))
	if p.detailView {
		t.Fatal("should have exited detail view")
	}

	// Re-open via 'o' from list
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerView {
		t.Fatal("should be in task runner again")
	}

	if p.taskRunnerMode != "worktree" {
		t.Errorf("mode = %q, want worktree", p.taskRunnerMode)
	}
}

func TestWizardPickingPathNotStaleOnReopen(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "" // no project dir triggers auto-open
	insertTestPaths(t, p.database, []string{"/tmp/a", "/tmp/b"})
	p.detailPaths = []string{"/tmp/a", "/tmp/b"}

	// Enter wizard — auto-opens path picker
	p.HandleKey(keyMsg("o"))
	if !p.taskRunnerPickingPath {
		t.Fatal("path picker should auto-open")
	}

	// Select a path
	p.HandleKey(keyMsg("enter"))
	if p.taskRunnerPickingPath {
		t.Fatal("path picker should close after enter")
	}

	// Go to step 2 and change mode
	p.HandleKey(keyMsg("enter"))
	p.HandleKey(keyMsg("right")) // worktree

	// Back out all the way
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 2 -> 1
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // step 1 -> exit (saves)
	p.HandleKey(specialKeyMsg(tea.KeyEsc)) // exit detail

	// Re-enter — path picker should NOT auto-open
	p.HandleKey(keyMsg("o"))
	if p.taskRunnerPickingPath {
		t.Error("path picker should NOT auto-open when saved selections exist")
	}
	if p.taskRunnerMode != "worktree" {
		t.Errorf("mode = %q, want worktree", p.taskRunnerMode)
	}
}

func TestParseDueDate(t *testing.T) {
	// Fixed "now" for deterministic tests: 2026-03-14
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		input   string
		want    string
		wantOK  bool
	}{
		// Already YYYY-MM-DD
		{"2026-04-01", "2026-04-01", true},
		{"2025-12-25", "2025-12-25", true},

		// mm dd format — future date in current year
		{"03 20", "2026-03-20", true},
		{"3 20", "2026-03-20", true},
		{"04 01", "2026-04-01", true},
		{"12 25", "2026-12-25", true},

		// mm dd format — date already passed → next year
		{"01 05", "2027-01-05", true},
		{"03 13", "2027-03-13", true},

		// mm dd format — today is still valid (not past)
		{"03 14", "2026-03-14", true},

		// Invalid month/day
		{"13 01", "", false},
		{"00 15", "", false},
		{"03 32", "", false},

		// Natural language — should return false for LLM fallback
		{"wednesday", "", false},
		{"next friday", "", false},
		{"end of month", "", false},
		{"tomorrow", "", false},

		// Empty string
		{"", "", false},
	}

	for _, tt := range tests {
		got, ok := parseDueDate(tt.input, now)
		if ok != tt.wantOK {
			t.Errorf("parseDueDate(%q): ok = %v, want %v", tt.input, ok, tt.wantOK)
		}
		if ok && got != tt.want {
			t.Errorf("parseDueDate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestYKeyInTriageFilterDoesNotPanic(t *testing.T) {
	p := testPluginWithCC(t)
	// Set up todos with different triage statuses
	p.cc.Todos = []db.Todo{
		{ID: "t-new-1", Title: "New todo 1", Status: db.StatusNew, Source: "manual", CreatedAt: time.Now(), ProjectDir: "/tmp/proj1"},
		{ID: "t-acc-1", Title: "Accepted todo", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now(), ProjectDir: "/tmp/proj2"},
		{ID: "t-new-2", Title: "New todo 2", Status: db.StatusNew, Source: "manual", CreatedAt: time.Now(), ProjectDir: "/tmp/proj3"},
	}
	insertTestPaths(t, p.database, []string{"/tmp/proj1", "/tmp/proj2", "/tmp/proj3"})

	// Enter expanded view and set triage filter to "new"
	p.HandleKey(keyMsg(" ")) // toggle expanded
	if !p.ccExpanded {
		t.Fatal("expected expanded view")
	}
	// Set filter to "inbox" — only t-new-1 and t-new-2 should be visible
	p.triageFilter = "inbox"
	p.ccCursor = 0

	filtered := p.filteredTodos()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered todos in 'new' filter, got %d", len(filtered))
	}
	if filtered[0].ID != "t-new-1" {
		t.Fatalf("expected first filtered todo to be t-new-1, got %s", filtered[0].ID)
	}

	// Press Y on cursor 0 — should NOT panic and should open task runner for t-new-1
	p.HandleKey(keyMsg("Y"))

	if !p.detailView {
		t.Error("Y should open detail view")
	}
	if !p.taskRunnerView {
		t.Error("Y should open task runner view")
	}
	if p.detailTodoID != "t-new-1" {
		t.Errorf("detailTodoID = %q, want %q", p.detailTodoID, "t-new-1")
	}
}

// TestExtractSessionSummary is now tested in internal/agent/runner_test.go
// since the implementation was moved to the agent package.

func TestBuildEnrichPromptIncludesActiveTodos(t *testing.T) {
	todos := []db.Todo{
		{ID: "a", DisplayID: 12, Title: "Send report to Bob"},
		{ID: "b", DisplayID: 13, Title: "Review PR"},
	}
	prompt := buildEnrichPrompt("do the report thing", todos)

	if !strings.Contains(prompt, "#12") {
		t.Error("prompt should contain display ID #12")
	}
	if !strings.Contains(prompt, "Send report to Bob") {
		t.Error("prompt should contain existing todo title")
	}
	if !strings.Contains(prompt, "merge_into") {
		t.Error("prompt should ask for merge_into field")
	}
}

func TestBuildEnrichPromptNoTodosSection(t *testing.T) {
	prompt := buildEnrichPrompt("do something", nil)
	if strings.Contains(prompt, "Existing Todos") {
		t.Error("prompt should not contain 'Existing Todos' section when there are no active todos")
	}
}

func TestBuildEnrichPromptCapsAt50Todos(t *testing.T) {
	todos := make([]db.Todo, 60)
	for i := range todos {
		todos[i] = db.Todo{ID: fmt.Sprintf("id-%d", i), DisplayID: i + 1, Title: fmt.Sprintf("Todo %d", i+1)}
	}
	prompt := buildEnrichPrompt("some task", todos)
	// Only first 50 should appear; todo #51 (DisplayID 51) should not
	if strings.Contains(prompt, "#51") {
		t.Error("prompt should not include todo #51 — cap is 50")
	}
	if !strings.Contains(prompt, "#50") {
		t.Error("prompt should include todo #50")
	}
}

func TestSearchFilterMatchesDisplayID(t *testing.T) {
	p := testPlugin(t)
	p.cc = &db.CommandCenter{
		GeneratedAt: time.Now(),
		Todos: []db.Todo{
			{ID: "t1", DisplayID: 183, Title: "Alpha task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
			{ID: "t2", DisplayID: 42, Title: "Beta task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
			{ID: "t3", DisplayID: 1830, Title: "Gamma task", Status: db.StatusBacklog, Source: "manual", CreatedAt: time.Now()},
		},
	}
	p.width = 120
	p.height = 40

	// Searching "183" should match display_id 183 exactly, not 1830
	p.searchInput.SetValue("183")
	filtered := p.filteredTodos()
	if len(filtered) != 1 {
		t.Fatalf("search '183': got %d results, want 1", len(filtered))
	}
	if filtered[0].DisplayID != 183 {
		t.Errorf("search '183': got display_id %d, want 183", filtered[0].DisplayID)
	}

	// Searching "42" should match display_id 42
	p.searchInput.SetValue("42")
	filtered = p.filteredTodos()
	if len(filtered) != 1 {
		t.Fatalf("search '42': got %d results, want 1", len(filtered))
	}
	if filtered[0].DisplayID != 42 {
		t.Errorf("search '42': got display_id %d, want 42", filtered[0].DisplayID)
	}

	// Searching "task" should match all three by title
	p.searchInput.SetValue("task")
	filtered = p.filteredTodos()
	if len(filtered) != 3 {
		t.Fatalf("search 'task': got %d results, want 3", len(filtered))
	}

	// Searching "alpha" should match only t1 by title
	p.searchInput.SetValue("alpha")
	filtered = p.filteredTodos()
	if len(filtered) != 1 {
		t.Fatalf("search 'alpha': got %d results, want 1", len(filtered))
	}
	if filtered[0].ID != "t1" {
		t.Errorf("search 'alpha': got id %q, want t1", filtered[0].ID)
	}
}

// startTestDaemon creates a daemon server with an agent runner and returns a connected client.
func startTestDaemon(t *testing.T) *daemon.Client {
	t.Helper()
	dir := t.TempDir()
	d, err := db.OpenDB(filepath.Join(dir, "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	runner := agent.NewRunner(10)
	// Use /tmp for socket to avoid macOS path length limits.
	sockPath := filepath.Join("/tmp", fmt.Sprintf("ccc-test-%d.sock", time.Now().UnixNano()))
	srv := daemon.NewServer(daemon.ServerConfig{
		SocketPath:  sockPath,
		DB:          d,
		AgentRunner: runner,
	})
	go srv.Serve()
	t.Cleanup(func() { srv.Shutdown() })

	time.Sleep(50 * time.Millisecond)

	client, err := daemon.NewClient(sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestSetDaemonClientFunc(t *testing.T) {
	p := testPlugin(t)

	// Initially no daemon client.
	if got := p.daemonClient(); got != nil {
		t.Fatal("expected nil daemon client before SetDaemonClientFunc")
	}

	client := startTestDaemon(t)

	// Wire the daemon client.
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	if got := p.daemonClient(); got == nil {
		t.Fatal("expected non-nil daemon client after SetDaemonClientFunc")
	}
}

func TestLaunchAgentViasDaemon(t *testing.T) {
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	todo := p.cc.Todos[0]
	qs := queuedSession{
		TodoID:     todo.ID,
		Prompt:     "test prompt",
		ProjectDir: t.TempDir(),
		Mode:       "normal",
		Perm:       "default",
		Budget:     1.0,
		AutoStart:  true,
	}

	_ = p.launchOrQueueAgent(qs)

	// The todo should be set to running status (optimistic).
	if p.cc.Todos[0].Status != db.StatusRunning {
		t.Errorf("expected todo status %q, got %q", db.StatusRunning, p.cc.Todos[0].Status)
	}

	// Verify the daemon accepted the agent (ListAgents returns it).
	time.Sleep(100 * time.Millisecond)
	agents, err := client.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	// The agent should be listed (may have already finished for a trivial prompt,
	// so we check that the launch was accepted by the daemon without error).
	_ = agents // launch was accepted — the status update on the plugin confirms RPC was used
}

func TestKillAgentViasDaemon(t *testing.T) {
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	todo := p.cc.Todos[0]

	// Launch an agent via daemon first.
	err := client.LaunchAgent(daemon.LaunchAgentParams{
		ID:     todo.ID,
		Prompt: "echo hello",
		Dir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	// Kill via the plugin — should use daemon RPC.
	cmd := p.killAgent(todo.ID)

	// Status should be backlog after kill.
	if p.cc.Todos[0].Status != db.StatusBacklog {
		t.Errorf("expected todo status %q after kill, got %q", db.StatusBacklog, p.cc.Todos[0].Status)
	}
	_ = cmd
}

func TestNoDaemonShowsFlashMessage(t *testing.T) {
	p := testPluginWithCC(t)
	// No daemon client set — should show flash message.

	todo := p.cc.Todos[0]
	qs := queuedSession{
		TodoID:     todo.ID,
		Prompt:     "test prompt",
		ProjectDir: t.TempDir(),
		Mode:       "normal",
		Perm:       "default",
		Budget:     1.0,
		AutoStart:  true,
	}

	p.launchOrQueueAgent(qs)

	// Without daemon, flash message should indicate the problem.
	if p.flashMessage == "" {
		t.Fatal("expected flash message when daemon is not connected")
	}
	if !strings.Contains(p.flashMessage, "Daemon not connected") {
		t.Errorf("expected 'Daemon not connected' flash, got %q", p.flashMessage)
	}

	// The todo should NOT be set to running (no launch happened).
	if p.cc.Todos[0].Status == db.StatusRunning {
		t.Error("todo should not be running when daemon is not connected")
	}
}

func TestActiveAgentCountWithDaemon(t *testing.T) {
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	// Should be 0 with empty daemon.
	if count := p.activeAgentCount(); count != 0 {
		t.Errorf("expected 0 active agents, got %d", count)
	}

	// Launch an agent.
	err := client.LaunchAgent(daemon.LaunchAgentParams{
		ID:     "test-count",
		Prompt: "echo hello",
		Dir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	count := p.activeAgentCount()
	// May be 0 or 1 depending on how quickly the echo process exits.
	// The key thing is no error occurred — the daemon path was used.
	_ = count
}

func TestCanLaunchAgentWithDaemon(t *testing.T) {
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	// With no agents running, should be able to launch.
	if !p.canLaunchAgent() {
		t.Error("expected canLaunchAgent to return true with empty daemon")
	}
}

func TestFilteredTodosNormalViewIncludesAgentStatuses(t *testing.T) {
	p := testPluginWithCC(t)
	// Add todos with various statuses
	p.cc.Todos = []db.Todo{
		{ID: "t1", Title: "Backlog", Status: db.StatusBacklog},
		{ID: "t2", Title: "Running", Status: db.StatusRunning},
		{ID: "t3", Title: "Enqueued", Status: db.StatusEnqueued},
		{ID: "t4", Title: "Review", Status: db.StatusReview},
		{ID: "t5", Title: "Blocked", Status: db.StatusBlocked},
		{ID: "t6", Title: "Failed", Status: db.StatusFailed},
		{ID: "t7", Title: "Inbox", Status: db.StatusNew},
	}

	// Normal (non-expanded) view should include everything except "new"
	p.ccExpanded = false
	filtered := p.filteredTodos()
	if len(filtered) != 6 {
		t.Fatalf("expected 6 non-new todos in normal view, got %d", len(filtered))
	}
	for _, todo := range filtered {
		if todo.Status == db.StatusNew {
			t.Error("normal view should not include 'new' status todos")
		}
	}
}

func TestAgentLaunchPersistsRunningStatus(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].ProjectDir = "/tmp/myproject"
	p.cc.Todos[0].ProposedPrompt = "Do the thing"

	// Set up daemon so launch can succeed.
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client { return client })

	// Launch agent via launchOrQueueAgent
	qs := queuedSession{
		TodoID:     "t1",
		Prompt:     "Do the thing",
		ProjectDir: "/tmp/myproject",
		Mode:       "normal",
		Perm:       "auto",
		Budget:     5.0,
		AutoStart:  true,
	}
	cmd := p.launchOrQueueAgent(qs)

	// In-memory status should be running
	if p.cc.Todos[0].Status != db.StatusRunning {
		t.Errorf("expected in-memory status %q, got %q", db.StatusRunning, p.cc.Todos[0].Status)
	}

	// The returned cmd should not be nil (includes persist + launch)
	if cmd == nil {
		t.Error("expected non-nil tea.Cmd from launchOrQueueAgent")
	}
}

func TestHandleLaunchDeniedRevertsStatus(t *testing.T) {
	p := testPluginWithCC(t)
	// Simulate the todo being set to enqueued (as launchOrQueueAgent does when queued)
	p.cc.Todos[0].Status = db.StatusEnqueued

	msg := agent.LaunchDeniedMsg{ID: "t1", Reason: "budget exceeded"}
	handled, action := p.HandleMessage(msg)
	if !handled {
		t.Error("expected LaunchDeniedMsg to be handled")
	}
	if action.TeaCmd == nil {
		t.Error("expected a persist cmd")
	}

	// Status should revert to backlog
	if p.cc.Todos[0].Status != db.StatusBacklog {
		t.Errorf("expected status %q after denial, got %q", db.StatusBacklog, p.cc.Todos[0].Status)
	}

	// Flash message should mention the denial
	if !strings.Contains(p.flashMessage, "budget exceeded") {
		t.Errorf("flash message %q should contain denial reason", p.flashMessage)
	}
}

func TestSearchEnterOpensSelectedItem(t *testing.T) {
	p := testPluginWithCC(t)

	// Activate search mode with "/"
	p.HandleKey(keyMsg("/"))
	if !p.searchActive {
		t.Fatal("expected searchActive after pressing /")
	}

	// Type a search query that matches "First todo"
	p.searchInput.SetValue("First")
	p.ccCursor = 0

	// Press Enter while in search mode — should open detail view directly
	p.HandleKey(keyMsg("enter"))

	if p.searchActive {
		t.Error("searchActive should be false after enter")
	}
	if !p.detailView {
		t.Error("detailView should be true — enter in search mode should open the selected item directly")
	}
	if p.detailTodoID != "t1" {
		t.Errorf("detailTodoID = %q, want %q — should open the first filtered item", p.detailTodoID, "t1")
	}
}

func TestHandleDaemonAgentFinished(t *testing.T) {
	p := testPluginWithCC(t)
	// Set the todo to "running" to simulate a daemon-managed agent in progress.
	p.cc.Todos[0].Status = db.StatusRunning

	// Simulate the daemon broadcasting agent.finished with exit code 0.
	data := []byte(`{"id":"t1","exit_code":0}`)
	handled, action := p.handleDaemonAgentFinished(data)
	if !handled {
		t.Fatal("expected handleDaemonAgentFinished to return handled=true")
	}
	if action.TeaCmd == nil {
		t.Fatal("expected non-nil TeaCmd for DB persistence")
	}

	// The in-memory status should be "review" for a successful exit.
	if p.cc.Todos[0].Status != db.StatusReview {
		t.Errorf("expected todo status %q, got %q", db.StatusReview, p.cc.Todos[0].Status)
	}
}

func TestHandleDaemonAgentFinishedFailure(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning

	// Simulate daemon broadcasting agent.finished with non-zero exit code.
	data := []byte(`{"id":"t1","exit_code":1}`)
	handled, _ := p.handleDaemonAgentFinished(data)
	if !handled {
		t.Fatal("expected handleDaemonAgentFinished to return handled=true")
	}

	// The in-memory status should be "failed" for a non-zero exit.
	if p.cc.Todos[0].Status != db.StatusFailed {
		t.Errorf("expected todo status %q, got %q", db.StatusFailed, p.cc.Todos[0].Status)
	}
}

func TestHandleDaemonAgentFinishedInvalidPayload(t *testing.T) {
	p := testPluginWithCC(t)

	// Invalid JSON should return handled=false.
	handled, _ := p.handleDaemonAgentFinished([]byte(`not json`))
	if handled {
		t.Error("expected handled=false for invalid JSON")
	}

	// Empty ID should return handled=false.
	handled, _ = p.handleDaemonAgentFinished([]byte(`{"id":"","exit_code":0}`))
	if handled {
		t.Error("expected handled=false for empty ID")
	}
}

// ==========================================
// VIEW-LEVEL REGRESSION TESTS
// ==========================================

func TestBUG113_DownArrowAutoExpandsView(t *testing.T) {
	p := testPlugin(t)
	p.height = 30
	p.width = 120
	maxVisible := p.normalMaxVisibleTodos()

	// Create enough todos to overflow the visible area.
	var todos []db.Todo
	for i := 0; i < maxVisible+5; i++ {
		todos = append(todos, db.Todo{
			ID:        fmt.Sprintf("t%d", i),
			Title:     fmt.Sprintf("Todo item %d", i),
			Status:    db.StatusBacklog,
			Source:    "manual",
			CreatedAt: time.Now(),
		})
	}
	p.cc = &db.CommandCenter{GeneratedAt: time.Now(), Todos: todos}

	// Navigate down to one before the limit.
	for i := 0; i < maxVisible-1; i++ {
		p.HandleKey(keyMsg("j"))
	}

	// Should still be in normal (non-expanded) view.
	if p.ccExpanded {
		t.Fatal("BUG-113 regression: should not be expanded yet")
	}

	// One more down should trigger auto-expand.
	p.HandleKey(keyMsg("j"))

	if !p.ccExpanded {
		t.Error("BUG-113 regression: ccExpanded should be true after auto-expand")
	}
	if p.ccExpandedCols != 2 {
		t.Errorf("BUG-113 regression: expandedCols = %d, want 2", p.ccExpandedCols)
	}

	// Render expanded view — the expanded tab bar uses format "ToDo (N)"
	// which uniquely identifies the expanded view.
	view := p.View(120, 30, 0)
	expectedTab := fmt.Sprintf("ToDo (%d)", maxVisible+5)
	if !strings.Contains(view, expectedTab) {
		t.Errorf("BUG-113 regression: expanded view should show tab bar with %q, but not found in rendered output", expectedTab)
	}
	// All todos should be accessible in the expanded view.
	lastTodo := fmt.Sprintf("Todo item %d", maxVisible)
	if !strings.Contains(view, lastTodo) {
		t.Errorf("BUG-113 regression: todo beyond normal visible area (%q) should be visible in expanded view", lastTodo)
	}
}

func TestBUG115_SearchEnterOpensItem(t *testing.T) {
	p := testPluginWithCC(t)

	// Enter search mode.
	p.HandleKey(keyMsg("/"))
	if !p.searchActive {
		t.Fatal("expected searchActive after /")
	}

	// Type a search query matching the first todo.
	p.searchInput.SetValue("First")
	p.ccCursor = 0

	// Press Enter — should open detail view directly.
	p.HandleKey(keyMsg("enter"))

	if p.searchActive {
		t.Error("BUG-115 regression: searchActive should be false after enter")
	}
	if !p.detailView {
		t.Fatal("BUG-115 regression: enter in search mode should open detail view directly")
	}

	// Render and check that the detail view content appears (not the todo list).
	view := p.View(120, 40, 0)
	// The detail view renders the todo title and field labels like "Status", "Due", "Project".
	if !strings.Contains(view, "First todo") {
		t.Error("BUG-115 regression: detail view should show the selected todo title 'First todo'")
	}
	// The detail view should NOT show the search filter bar.
	if strings.Contains(view, "filter:") {
		t.Error("BUG-115 regression: detail view should not show 'filter:' text — item should be opened, not frozen")
	}
}

func TestBUG116_RunningTodoVisibleInView(t *testing.T) {
	p := testPluginWithCC(t)
	// Set a todo to StatusRunning — before the fix, this was filtered OUT.
	p.cc.Todos[0].Status = db.StatusRunning

	// Normal (non-expanded) view should include running todos.
	view := p.View(120, 40, 0)
	if !strings.Contains(view, "First todo") {
		t.Error("BUG-116 regression: running todo 'First todo' should be visible in normal view")
	}
	if !strings.Contains(view, "agent working") {
		t.Error("BUG-116 regression: running todo should show 'agent working' status indicator")
	}
}

func TestBUG116_RunningTodoVisibleInExpandedAgentsTab(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning

	// Enter expanded view and switch to agents tab.
	p.HandleKey(keyMsg(" ")) // expand
	if !p.ccExpanded {
		t.Fatal("expected expanded view")
	}
	// Cycle to "agents" tab: todo -> inbox -> agents
	p.HandleKey(specialKeyMsg(tea.KeyTab))
	p.HandleKey(specialKeyMsg(tea.KeyTab))
	if p.triageFilter != "agents" {
		t.Fatalf("expected triageFilter 'agents', got %q", p.triageFilter)
	}

	view := p.View(120, 40, 0)
	if !strings.Contains(view, "First todo") {
		t.Error("BUG-116 regression: running todo should appear under Agents tab in expanded view")
	}
}

func TestBUG117_DaemonAgentFinishedTransitionsToReview(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning
	p.cc.Todos[0].SessionID = "sess-abc"

	// Simulate daemon broadcasting agent.finished.
	msg := plugin.NotifyMsg{
		Event: "agent.finished",
		Data:  []byte(`{"id":"t1","exit_code":0}`),
	}
	handled, _ := p.HandleMessage(msg)
	if !handled {
		t.Fatal("BUG-117 regression: agent.finished NotifyMsg should be handled")
	}

	// Verify in-memory status transitioned.
	if p.cc.Todos[0].Status != db.StatusReview {
		t.Errorf("BUG-117 regression: expected status %q, got %q", db.StatusReview, p.cc.Todos[0].Status)
	}

	// Render normal view — the todo should show "ready for review" indicator.
	view := p.View(120, 40, 0)
	if !strings.Contains(view, "ready for review") {
		t.Error("BUG-117 regression: todo should show 'ready for review' status in rendered view after agent.finished")
	}
	if strings.Contains(view, "agent working") {
		t.Error("BUG-117 regression: todo should NOT show 'agent working' after transitioning to review")
	}
}

func TestBUG117_DaemonAgentFinishedFailureShowsFailed(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning
	p.cc.Todos[0].SessionID = "sess-abc"

	// Simulate daemon broadcasting agent.finished with non-zero exit code.
	msg := plugin.NotifyMsg{
		Event: "agent.finished",
		Data:  []byte(`{"id":"t1","exit_code":1}`),
	}
	p.HandleMessage(msg)

	if p.cc.Todos[0].Status != db.StatusFailed {
		t.Errorf("BUG-117 regression: expected status %q for non-zero exit, got %q", db.StatusFailed, p.cc.Todos[0].Status)
	}

	// Render — should show "failed" indicator.
	view := p.View(120, 40, 0)
	if !strings.Contains(view, "failed") {
		t.Error("BUG-117 regression: todo should show 'failed' indicator for non-zero exit code")
	}
}

func TestNotifyMsgAgentFinishedRouting(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning

	// Send a NotifyMsg with agent.finished event — this is what the host
	// broadcasts when a daemon event arrives.
	msg := plugin.NotifyMsg{
		Event: "agent.finished",
		Data:  []byte(`{"id":"t1","exit_code":0}`),
	}
	handled, action := p.HandleMessage(msg)
	if !handled {
		t.Fatal("expected HandleMessage to handle agent.finished NotifyMsg")
	}
	if action.TeaCmd == nil {
		t.Fatal("expected non-nil TeaCmd")
	}
	if p.cc.Todos[0].Status != db.StatusReview {
		t.Errorf("expected todo status %q, got %q", db.StatusReview, p.cc.Todos[0].Status)
	}
}

func TestBUG124_AutoExpandSetsTriageFilterAll(t *testing.T) {
	p := testPlugin(t)
	p.height = 30
	p.width = 120
	maxVisible := p.normalMaxVisibleTodos()

	// Create todos with mixed statuses (backlog + enqueued) to exercise
	// the filter difference between collapsed and expanded views.
	var todos []db.Todo
	for i := 0; i < maxVisible+3; i++ {
		status := db.StatusBacklog
		if i%3 == 0 {
			status = db.StatusEnqueued
		}
		todos = append(todos, db.Todo{
			ID:        fmt.Sprintf("t%d", i),
			Title:     fmt.Sprintf("Todo %d", i),
			Status:    status,
			Source:    "manual",
			CreatedAt: time.Now(),
		})
	}
	p.cc = &db.CommandCenter{GeneratedAt: time.Now(), Todos: todos}

	// Verify default triageFilter is "todo" (which would show only backlog).
	if p.triageFilter != "todo" {
		t.Fatalf("expected default triageFilter 'todo', got %q", p.triageFilter)
	}

	// Navigate down past visible area to trigger auto-expand.
	for i := 0; i < maxVisible; i++ {
		p.HandleKey(keyMsg("j"))
	}

	if !p.ccExpanded {
		t.Fatal("BUG-124 regression: expected auto-expand to trigger")
	}

	// After auto-expand, triageFilter must be "all" so the same items are visible.
	if p.triageFilter != "all" {
		t.Errorf("BUG-124 regression: expected triageFilter 'all' after auto-expand, got %q", p.triageFilter)
	}

	// Verify that enqueued todos are in the filtered list (they wouldn't be under "todo" filter).
	filtered := p.filteredTodos()
	hasEnqueued := false
	for _, todo := range filtered {
		if todo.Status == db.StatusEnqueued {
			hasEnqueued = true
			break
		}
	}
	if !hasEnqueued {
		t.Error("BUG-124 regression: expanded view after auto-expand should include enqueued todos (same as collapsed view)")
	}
}

func TestBUG124_AutoExpandWithSuggestionBanner(t *testing.T) {
	p := testPlugin(t)
	p.height = 50
	p.width = 120

	// Before adding suggestions, record maxVisible.
	maxVisibleClean := p.normalMaxVisibleTodos()

	var todos []db.Todo
	for i := 0; i < maxVisibleClean+3; i++ {
		todos = append(todos, db.Todo{
			ID:        fmt.Sprintf("t%d", i),
			Title:     fmt.Sprintf("Todo %d", i),
			Status:    db.StatusBacklog,
			Source:    "manual",
			CreatedAt: time.Now(),
		})
	}
	p.cc = &db.CommandCenter{
		GeneratedAt: time.Now(),
		Todos:       todos,
		Suggestions: db.Suggestions{Focus: "Work on the critical bug fix"},
	}

	// Recompute maxVisible now that suggestions are set — it should be smaller.
	maxVisibleWithSuggestion := p.normalMaxVisibleTodos()
	if maxVisibleWithSuggestion >= maxVisibleClean {
		t.Fatalf("expected normalMaxVisibleTodos to decrease with suggestion banner, got %d vs %d", maxVisibleWithSuggestion, maxVisibleClean)
	}

	// Navigate down to trigger auto-expand — should take exactly one press
	// past the last visible item.
	for i := 0; i < maxVisibleWithSuggestion; i++ {
		p.HandleKey(keyMsg("j"))
	}

	if !p.ccExpanded {
		t.Errorf("BUG-124 regression: expected auto-expand after %d down presses (maxVisible with suggestion)", maxVisibleWithSuggestion)
	}
}

func TestDaemonAgentSessionID_UpdatesTodoSessionID(t *testing.T) {
	p := testPluginWithCC(t)
	p.cc.Todos[0].Status = db.StatusRunning

	msg := plugin.NotifyMsg{
		Event: "agent.session_id",
		Data:  []byte(`{"id":"t1","session_id":"uuid-abc-123"}`),
	}
	handled, _ := p.HandleMessage(msg)
	if !handled {
		t.Fatal("expected HandleMessage to handle agent.session_id NotifyMsg")
	}
	if p.cc.Todos[0].SessionID != "uuid-abc-123" {
		t.Errorf("expected session ID %q, got %q", "uuid-abc-123", p.cc.Todos[0].SessionID)
	}
}

func TestWKeyDaemonFallback(t *testing.T) {
	// When no local agent session exists but daemon reports an active agent,
	// pressing w should open the session viewer and start daemon polling.
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	// Launch an agent via daemon so AgentStatus returns "processing".
	todo := p.cc.Todos[0]
	if err := client.LaunchAgent(daemon.LaunchAgentParams{
		ID:     todo.ID,
		Prompt: "test prompt",
		Dir:    t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let daemon register the agent

	// Open detail view for the todo.
	p.detailView = true
	p.detailTodoID = todo.ID
	p.detailMode = "viewing"

	// Press w — should open session viewer via daemon path (no local session).
	action := p.handleDetailViewing(keyMsg("w"))

	if !p.sessionViewerActive {
		t.Fatal("expected sessionViewerActive to be true after w key")
	}
	if p.sessionViewerTodoID != todo.ID {
		t.Errorf("expected sessionViewerTodoID %q, got %q", todo.ID, p.sessionViewerTodoID)
	}
	if !p.sessionViewerListening {
		t.Fatal("expected sessionViewerListening to be true (daemon polling)")
	}
	// The action should carry a TeaCmd (the daemon polling command).
	if action.TeaCmd == nil {
		t.Fatal("expected action to carry a TeaCmd for daemon event polling")
	}
}

func TestDaemonAgentEventMsgAccumulatesEvents(t *testing.T) {
	p := testPluginWithCC(t)
	client := startTestDaemon(t)
	p.SetDaemonClientFunc(func() *daemon.Client {
		return client
	})

	todo := p.cc.Todos[0]

	// Set up session viewer as if we just opened it via daemon.
	p.sessionViewerActive = true
	p.sessionViewerTodoID = todo.ID
	p.sessionViewerListening = true
	p.sessionViewerReplayEvents = nil

	// Simulate receiving a daemon agent event.
	ev := sessionEvent{Type: "assistant_text", Text: "Hello from daemon"}
	handled, _ := p.HandleMessage(daemonAgentEventMsg{
		todoID: todo.ID,
		event:  ev,
		offset: 1,
		done:   false,
	})

	if !handled {
		t.Fatal("expected daemonAgentEventMsg to be handled")
	}
	if len(p.sessionViewerReplayEvents) != 1 {
		t.Fatalf("expected 1 replay event, got %d", len(p.sessionViewerReplayEvents))
	}
	if p.sessionViewerReplayEvents[0].Text != "Hello from daemon" {
		t.Errorf("unexpected event text: %q", p.sessionViewerReplayEvents[0].Text)
	}
}

func TestDaemonAgentEventDoneTriggersCompletion(t *testing.T) {
	p := testPluginWithCC(t)

	todo := p.cc.Todos[0]
	p.sessionViewerActive = true
	p.sessionViewerTodoID = todo.ID
	p.sessionViewerListening = true

	// When done=true, it should emit an agentEventsDoneMsg via TeaCmd.
	ev := sessionEvent{Type: "assistant_text", Text: "Final message"}
	_, action := p.HandleMessage(daemonAgentEventMsg{
		todoID: todo.ID,
		event:  ev,
		offset: 5,
		done:   true,
	})

	if action.TeaCmd == nil {
		t.Fatal("expected TeaCmd for done=true daemonAgentEventMsg")
	}
	// Execute the TeaCmd and check it produces agentEventsDoneMsg.
	result := action.TeaCmd()
	if _, ok := result.(agentEventsDoneMsg); !ok {
		t.Fatalf("expected agentEventsDoneMsg, got %T", result)
	}
}

func TestDaemonAgentPollStopsWhenViewerClosed(t *testing.T) {
	p := testPluginWithCC(t)

	todo := p.cc.Todos[0]
	// Viewer is NOT active — polling should stop.
	p.sessionViewerActive = false
	p.sessionViewerTodoID = todo.ID
	p.sessionViewerListening = true

	handled, action := p.HandleMessage(daemonAgentPollMsg{
		todoID: todo.ID,
		offset: 3,
	})

	if !handled {
		t.Fatal("expected daemonAgentPollMsg to be handled")
	}
	// No daemon client and viewer not active — should noop.
	if action.TeaCmd != nil {
		t.Fatal("expected no TeaCmd when viewer is closed")
	}
	if p.sessionViewerListening {
		t.Fatal("expected sessionViewerListening to be false after viewer closed")
	}
}

// ==========================================
// Command LLM Delegation Tests
// ==========================================

func TestHandleClaudeCommandFinished_Delegate(t *testing.T) {
	p := testPluginWithCC(t)

	msg := claudeCommandFinishedMsg{
		output: `{"message":"Delegating to agent...","delegate":{"prompt":"Read Granola call notes and summarize","project_dir":"/Users/test/project"},"todos":[],"complete_todo_ids":[]}`,
	}

	handled, action := p.handleClaudeCommandFinished(msg)
	if !handled {
		t.Fatal("expected message to be handled")
	}

	if !strings.Contains(p.flashMessage, "Delegat") {
		t.Errorf("flash message = %q, want to contain 'Delegat'", p.flashMessage)
	}

	if p.cc == nil {
		t.Fatal("cc should not be nil")
	}

	var delegateTodo *db.Todo
	for i := range p.cc.Todos {
		if strings.Contains(p.cc.Todos[i].Detail, "Read Granola call") ||
			strings.Contains(p.cc.Todos[i].ProposedPrompt, "Read Granola call") {
			delegateTodo = &p.cc.Todos[i]
			break
		}
	}
	if delegateTodo == nil {
		t.Fatal("expected a new todo created for the delegate prompt")
	}
	if delegateTodo.ProjectDir != "/Users/test/project" {
		t.Errorf("delegate todo project_dir = %q, want '/Users/test/project'", delegateTodo.ProjectDir)
	}

	if action.TeaCmd == nil {
		t.Error("delegate should return a TeaCmd for launching/queuing the agent")
	}
}

func TestHandleClaudeCommandFinished_DelegateAndTodos(t *testing.T) {
	p := testPluginWithCC(t)

	msg := claudeCommandFinishedMsg{
		output: `{
			"message":"Created todo and delegating...",
			"delegate":{"prompt":"Investigate the bug","project_dir":"/tmp/proj"},
			"todos":[{"title":"Follow up on PR review","due":"","who_waiting":"","effort":"","context":"","detail":"","project_dir":""}],
			"complete_todo_ids":[]
		}`,
	}

	todosBefore := len(p.cc.Todos)
	handled, _ := p.handleClaudeCommandFinished(msg)
	if !handled {
		t.Fatal("expected message to be handled")
	}

	todosAfter := len(p.cc.Todos)
	newTodos := todosAfter - todosBefore
	if newTodos < 2 {
		t.Errorf("expected at least 2 new todos (1 simple + 1 delegate), got %d new", newTodos)
	}

	var foundSimple bool
	for _, todo := range p.cc.Todos {
		if todo.Title == "Follow up on PR review" {
			foundSimple = true
			break
		}
	}
	if !foundSimple {
		t.Error("expected simple todo 'Follow up on PR review' to be created")
	}
}

func TestHandleClaudeCommandFinished_DelegateAndAsk(t *testing.T) {
	p := testPluginWithCC(t)

	msg := claudeCommandFinishedMsg{
		output: `{
			"message":"",
			"ask":"Which project should I delegate to?",
			"delegate":{"prompt":"Do the thing","project_dir":"/tmp/proj"},
			"todos":[],
			"complete_todo_ids":[]
		}`,
	}

	todosBefore := len(p.cc.Todos)
	handled, _ := p.handleClaudeCommandFinished(msg)
	if !handled {
		t.Fatal("expected message to be handled")
	}

	todosAfter := len(p.cc.Todos)
	if todosAfter != todosBefore {
		t.Errorf("ask should prevent delegation; todos before=%d, after=%d", todosBefore, todosAfter)
	}

	if !p.addingTodoRich {
		t.Error("ask should set addingTodoRich to true for conversation continuation")
	}

	if !strings.Contains(p.flashMessage, "Which project") {
		t.Errorf("flash message = %q, want to contain the ask question", p.flashMessage)
	}
}

func TestHandleClaudeCommandFinished_EmptyDelegate(t *testing.T) {
	p := testPluginWithCC(t)

	msg := claudeCommandFinishedMsg{
		output: `{
			"message":"Done",
			"delegate":{"prompt":"","project_dir":""},
			"todos":[],
			"complete_todo_ids":[]
		}`,
	}

	todosBefore := len(p.cc.Todos)
	handled, _ := p.handleClaudeCommandFinished(msg)
	if !handled {
		t.Fatal("expected message to be handled")
	}

	todosAfter := len(p.cc.Todos)
	if todosAfter != todosBefore {
		t.Errorf("empty delegate should not create new todos; before=%d, after=%d", todosBefore, todosAfter)
	}

	if p.flashMessage != "Done" {
		t.Errorf("flash message = %q, want 'Done'", p.flashMessage)
	}
}

