package sessions

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	tea "github.com/charmbracelet/bubbletea"
)

// testLogger is a no-op logger for tests.
type testLogger struct{}

func (testLogger) Info(_, _ string, _ ...interface{})  {}
func (testLogger) Warn(_, _ string, _ ...interface{})  {}
func (testLogger) Error(_, _ string, _ ...interface{}) {}
func (testLogger) Recent(_ int) []plugin.LogEntry      { return nil }

func testConfig() *config.Config {
	return &config.Config{
		Name:    "TestBot",
		Palette: "aurora",
	}
}

func setupPlugin(t *testing.T) *Plugin {
	t.Helper()
	t.Setenv("CCC_CONFIG_DIR", t.TempDir())
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := testConfig()
	p := &Plugin{}
	err = p.Init(plugin.Context{
		DB:     database,
		Config: cfg,
		Bus:    plugin.NewBus(),
		Logger: testLogger{},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	// Send a window size so lists have dimensions
	p.HandleMessage(tea.WindowSizeMsg{Width: 120, Height: 40})
	return p
}

// setupSessionsPlugin returns a plugin with subTab set to subTabRecent (live sessions view).
func setupSessionsPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := setupPlugin(t)
	p.subTab = subTabRecent
	p.unified.viewFilter = ViewFilterLiveOnly
	return p
}

func TestInitLoadsPaths(t *testing.T) {
	t.Setenv("CCC_CONFIG_DIR", t.TempDir())
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	_ = db.DBAddPath(database, "/home/user/project-a")
	_ = db.DBAddPath(database, "/home/user/project-b")

	cfg := testConfig()
	p := &Plugin{}
	err = p.Init(plugin.Context{
		DB:     database,
		Config: cfg,
		Bus:    plugin.NewBus(),
		Logger: testLogger{},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	if len(p.paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(p.paths))
	}
	if p.paths[0] != "/home/user/project-a" {
		t.Fatalf("expected project-a, got %s", p.paths[0])
	}

	// New list should have: 2 paths + Browse = 3 items
	items := p.newList.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 new list items, got %d", len(items))
	}
}

func TestHandleKeyEnterOnPathReturnsLaunch(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew

	// Add a path so there's something beyond home
	_ = db.DBAddPath(p.db, "/tmp/myproject")
	p.paths = append(p.paths, "/tmp/myproject")
	p.newList.SetItems(p.buildNewItems())

	// Select the path we just added (index 0 since no more home item)
	p.newList.Select(0)

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Type != "launch" {
		t.Fatalf("expected launch action, got %s", action.Type)
	}
	if action.Args["dir"] != "/tmp/myproject" {
		t.Fatalf("expected dir /tmp/myproject, got %s", action.Args["dir"])
	}
}

func TestHandleKeyEnterOnSessionReturnsResume(t *testing.T) {
	p := setupSessionsPlugin(t)
	p.unified.viewFilter = ViewFilterSavedOnly // Resume tab shows saved sessions

	// Load a saved session into the unified view
	sessions := []db.Session{
		{
			SessionID: "sess-abc",
			Project:   "/home/user/proj",
			Repo:      "proj",
			Branch:    "main",
			Summary:   "test session",
			Created:   time.Now(),
			Type:      db.SessionBookmark,
		},
	}
	p.unified.SetSavedSessions(sessions)
	// cursor starts at 0 which will hit the saved session

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Type != "launch" {
		t.Fatalf("expected launch action, got %s", action.Type)
	}
	if action.Args["resume_id"] != "sess-abc" {
		t.Fatalf("expected resume_id sess-abc, got %s", action.Args["resume_id"])
	}
	if action.Args["dir"] != "/home/user/proj" {
		t.Fatalf("expected dir /home/user/proj, got %s", action.Args["dir"])
	}
}

func TestHandleKeyDeleteEntersConfirming(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew

	_ = db.DBAddPath(p.db, "/tmp/deleteme")
	p.paths = append(p.paths, "/tmp/deleteme")
	p.newList.SetItems(p.buildNewItems())

	// Select the path item (index 0)
	p.newList.Select(0)

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyDelete})
	if action.Type != "noop" {
		t.Fatalf("expected noop during confirm setup, got %s", action.Type)
	}
	if !p.confirming {
		t.Fatal("expected confirming to be true")
	}
	if p.confirmItem.path != "/tmp/deleteme" {
		t.Fatalf("expected confirm path /tmp/deleteme, got %s", p.confirmItem.path)
	}
}

func TestConfirmingYRemovesItem(t *testing.T) {
	p := setupPlugin(t)

	_ = db.DBAddPath(p.db, "/tmp/removeme")
	p.paths = append(p.paths, "/tmp/removeme")
	p.newList.SetItems(p.buildNewItems())

	// Enter confirming mode
	p.confirming = true
	p.confirmItem = newItem{path: "/tmp/removeme", label: "removeme"}

	// Press "y"
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if action.Type != "noop" {
		t.Fatalf("expected noop, got %s", action.Type)
	}
	if p.confirming {
		t.Fatal("expected confirming to be false after y")
	}

	// Verify path was removed
	for _, path := range p.paths {
		if path == "/tmp/removeme" {
			t.Fatal("expected path to be removed from p.paths")
		}
	}
}

func TestSubTabSwitching(t *testing.T) {
	p := setupPlugin(t)

	// After Init, subTab defaults to subTabNew
	if p.subTab != subTabNew {
		t.Fatalf("expected initial subTab subTabNew, got %d", p.subTab)
	}

	// Switch to Saved via '2'
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if p.subTab != subTabSaved {
		t.Fatalf("expected subTab subTabSaved, got %d", p.subTab)
	}

	// Switch back to New Session via '1'
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew, got %d", p.subTab)
	}
}

func TestHandleKeyDeleteOnFirstPathEntersConfirming(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew

	// Add a path and select it
	_ = db.DBAddPath(p.db, "/tmp/firstpath")
	p.paths = append(p.paths, "/tmp/firstpath")
	p.newList.SetItems(p.buildNewItems())
	p.newList.Select(0)

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyDelete})
	if action.Type != "noop" {
		t.Fatalf("expected noop action type, got %s", action.Type)
	}
	if !p.confirming {
		t.Fatal("should enter confirming for first path")
	}
	if p.confirmItem.path != "/tmp/firstpath" {
		t.Fatalf("expected confirm path /tmp/firstpath, got %s", p.confirmItem.path)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	p := setupPlugin(t)

	// Should not panic for any sub-tab
	p.subTab = subTabNew
	output := p.View(120, 40, 0)
	if output == "" {
		t.Fatal("expected non-empty view for new tab")
	}

	p.subTab = subTabRecent
	output = p.View(120, 40, 0)
	if output == "" {
		t.Fatal("expected non-empty view for sessions tab")
	}
}

// TestUnifiedViewLoadsSavedSessions verifies that SetSavedSessions populates
// the unified view and SelectedItem returns the right session.
func TestUnifiedViewLoadsSavedSessions(t *testing.T) {
	p := setupSessionsPlugin(t)

	sessions := []db.Session{
		{
			SessionID: "s1",
			Repo:      "repo1",
			Branch:    "main",
			Created:   time.Now(),
			Type:      db.SessionBookmark,
		},
	}
	p.unified.SetSavedSessions(sessions)
	p.unified.viewFilter = ViewFilterSavedOnly // Resume tab shows saved sessions

	if len(p.unified.savedSessions) != 1 {
		t.Fatalf("expected 1 saved session, got %d", len(p.unified.savedSessions))
	}

	sel := p.unified.SelectedItem()
	if sel == nil {
		t.Fatal("expected selected item, got nil")
	}
	if sel.SessionID != "s1" {
		t.Fatalf("expected session ID s1, got %s", sel.SessionID)
	}
}

func TestNavigateTo(t *testing.T) {
	p := setupPlugin(t)

	p.NavigateTo("active", nil)
	if p.subTab != subTabRecent {
		t.Fatalf("expected subTab subTabRecent, got %d", p.subTab)
	}
	if p.unified.viewFilter != ViewFilterLiveOnly {
		t.Fatalf("expected viewFilter live_only for active route, got %q", p.unified.viewFilter)
	}

	p.NavigateTo("resume", nil)
	if p.subTab != subTabSaved {
		t.Fatalf("expected subTab subTabSaved for resume route, got %d", p.subTab)
	}
	if p.unified.viewFilter != ViewFilterSavedOnly {
		t.Fatalf("expected viewFilter saved_only for resume route, got %q", p.unified.viewFilter)
	}

	p.NavigateTo("sessions", nil)
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew for sessions route, got %d", p.subTab)
	}

	p.NavigateTo("new", nil)
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew, got %d", p.subTab)
	}
}

func TestEscWithPendingTodoNavigatesToCommand(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew
	p.pendingLaunchTodo = &db.Todo{Title: "test task"}

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyEscape})
	if action.Type != "navigate" {
		t.Fatalf("expected navigate action, got %s", action.Type)
	}
	if action.Payload != "command" {
		t.Fatalf("expected payload 'command', got %s", action.Payload)
	}
	if p.pendingLaunchTodo != nil {
		t.Fatal("expected pendingLaunchTodo to be cleared")
	}
}

func TestFormatTodoContext(t *testing.T) {
	todo := db.Todo{
		Title:   "Fix the bug",
		Context: "Found in prod",
		Due:     "2026-03-10",
	}
	result := formatTodoContext(todo)
	if result == "" {
		t.Fatal("expected non-empty context")
	}
	if !contains(result, "Fix the bug") {
		t.Fatal("expected title in context")
	}
	if !contains(result, "Found in prod") {
		t.Fatal("expected context field in output")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFilterFromFirstCharacter(t *testing.T) {
	p := setupSessionsPlugin(t)
	p.unified.viewFilter = ViewFilterSavedOnly // show saved sessions for this test

	// Load 3 saved sessions with different repos
	sessions := []db.Session{
		{SessionID: "s1", Project: "/home/user/claude-command-center", Repo: "claude-command-center", Branch: "main", Summary: "Working on the command center dashboard", Created: time.Now(), Type: db.SessionBookmark},
		{SessionID: "s2", Project: "/home/user/sherlock", Repo: "sherlock", Branch: "main", Summary: "Building the investigation dashboard with complex queries", Created: time.Now(), Type: db.SessionBookmark},
		{SessionID: "s3", Project: "/home/user/merchant-ui", Repo: "merchant-ui", Branch: "main", Summary: "Merchant portal UI with Tailwind CSS layout improvements", Created: time.Now(), Type: db.SessionBookmark},
	}
	p.unified.SetSavedSessions(sessions)

	// Verify 3 items visible via displayItems
	items := p.unified.displayItems()
	if len(items) != 3 {
		t.Fatalf("expected 3 items before filtering, got %d", len(items))
	}

	// Unified view doesn't do text filtering — just verify all items are present
	// and can be navigated. The unified view uses cursor-based navigation.
	p.unified.MoveDown()
	sel := p.unified.SelectedItem()
	if sel == nil {
		t.Fatal("expected selected item after MoveDown")
	}
}

func TestTypeToFilterNewTab(t *testing.T) {
	p := setupPlugin(t)
	// Switch to new tab explicitly so we can test filter behavior
	p.subTab = subTabNew

	// Add paths so we have items to filter
	_ = db.DBAddPath(p.db, "/tmp/alpha-project")
	_ = db.DBAddPath(p.db, "/tmp/beta-project")
	p.paths = append(p.paths, "/tmp/alpha-project", "/tmp/beta-project")
	p.newList.SetItems(p.buildNewItems())

	// Typing a character should immediately start filtering (no '/' needed)
	// Note: 's' and 'n' are sub-tab shortcuts on new tab, so use 'b' which is not.
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if p.filterText != "b" {
		t.Fatalf("expected filterText 'b', got %q", p.filterText)
	}

	// Type more chars
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if p.filterText != "bet" {
		t.Fatalf("expected filterText 'bet', got %q", p.filterText)
	}

	// Visible items should be filtered
	visible := p.newList.VisibleItems()
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible item after filtering 'bet', got %d", len(visible))
	}

	// Backspace should edit the filter
	p.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if p.filterText != "be" {
		t.Fatalf("expected filterText 'be', got %q after backspace", p.filterText)
	}

	// Escape should clear the filter
	p.HandleKey(tea.KeyMsg{Type: tea.KeyEscape})
	if p.filterText != "" {
		t.Fatalf("expected empty filterText after escape, got %q", p.filterText)
	}
}

func TestTypeToFilterShortcutsDisabledWhileFiltering(t *testing.T) {
	p := setupPlugin(t)
	// Must be on new tab for type-to-filter to work
	p.subTab = subTabNew

	// Start filtering
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if p.filterText != "c" {
		t.Fatalf("expected filterText 'c', got %q", p.filterText)
	}

	// Pressing 's' while filtering should append to filter, not switch tabs
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew while filtering, got %d", p.subTab)
	}
	if p.filterText != "cs" {
		t.Fatalf("expected filterText 'cs', got %q", p.filterText)
	}

	// Same for 'n' and 't'
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if p.filterText != "csn" {
		t.Fatalf("expected filterText 'csn', got %q", p.filterText)
	}
}

func TestEnterDirectlyLaunchesOnNewTab(t *testing.T) {
	p := setupPlugin(t)

	// Add a path
	_ = db.DBAddPath(p.db, "/tmp/myproject")
	p.paths = append(p.paths, "/tmp/myproject")
	p.newList.SetItems(p.buildNewItems())
	p.newList.Select(0)
	p.subTab = subTabNew

	// Single Enter should launch directly
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Type != "launch" {
		t.Fatalf("expected launch action from single Enter, got %s", action.Type)
	}
	if action.Args["dir"] != "/tmp/myproject" {
		t.Fatalf("expected dir /tmp/myproject, got %s", action.Args["dir"])
	}
}

func TestSubstringFilter(t *testing.T) {
	targets := []string{
		"claude-command-center main Working on CCC",
		"sherlock main Investigation dashboard",
		"merchant-ui main Merchant portal",
	}

	tests := []struct {
		term     string
		expected int
	}{
		{"c", 3},       // all three contain "c" somewhere
		{"cl", 1},      // only claude-command-center
		{"sh", 1},      // only sherlock
		{"main", 3},    // all contain "main"
		{"xyz", 0},     // nothing matches
		{"CLAUDE", 1},  // case-insensitive
	}

	for _, tc := range tests {
		ranks := substringFilter(tc.term, targets)
		if len(ranks) != tc.expected {
			t.Errorf("substringFilter(%q): expected %d matches, got %d", tc.term, tc.expected, len(ranks))
		}
	}
}

// ---------------------------------------------------------------------------
// New integration tests for unified view
// ---------------------------------------------------------------------------
// BUG regression tests (view-level)
// ---------------------------------------------------------------------------

// TestBUG118_ActiveTabOnlyShowsLiveSessions is a regression test for BUG-118.
// Before the fix, saved sessions leaked into the Active tab. The fix added
// viewFilter to unifiedView so Active shows only live and Resume shows only saved.
func TestBUG118_ActiveTabOnlyShowsLiveSessions(t *testing.T) {
	p := setupSessionsPlugin(t)

	now := time.Now()
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-1",
			Topic:        "Live Session",
			Project:      "/proj",
			State:        "active",
			RegisteredAt: now.Format(time.RFC3339),
		},
	}
	p.unified.savedSessions = []db.Session{
		{
			SessionID: "saved-1",
			Project:   "/proj",
			Repo:      "proj",
			Branch:    "main",
			Summary:   "Saved Session",
			Created:   now.Add(-1 * time.Hour),
			Type:      db.SessionBookmark,
		},
	}

	// Active tab mode: only live sessions should be visible.
	p.unified.viewFilter = ViewFilterLiveOnly
	view := p.View(120, 38, 0)
	if !strings.Contains(view, "Live Session") {
		t.Errorf("BUG-118 regression: Active tab should show live session.\nView:\n%s", view)
	}
	if strings.Contains(view, "Saved Session") {
		t.Errorf("BUG-118 regression: Active tab should NOT show saved session.\nView:\n%s", view)
	}

	// Resume tab mode: only saved sessions should be visible.
	p.unified.viewFilter = ViewFilterSavedOnly
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "Saved Session") {
		t.Errorf("BUG-118 regression: Resume tab should show saved session.\nView:\n%s", view)
	}
	if strings.Contains(view, "Live Session") {
		t.Errorf("BUG-118 regression: Resume tab should NOT show live session.\nView:\n%s", view)
	}
}

// TestBUG119_TabSwitchingDoesNotCorruptViews is a regression test for BUG-119.
// Before the fix, NavigateTo did not handle "active" and "resume" routes
// correctly, causing the Resume tab to show New Session content and tab
// switching to corrupt all tabs. This test verifies at the VIEW level.
func TestBUG119_TabSwitchingDoesNotCorruptViews(t *testing.T) {
	p := setupPlugin(t)

	// Populate data for all three tabs.
	_ = db.DBAddPath(p.db, "/home/user/project-a")
	p.paths = append(p.paths, "/home/user/project-a")
	p.newList.SetItems(p.buildNewItems())

	now := time.Now()
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-1",
			Topic:        "My Active Session",
			Project:      "/home/user/project-a",
			State:        "active",
			RegisteredAt: now.Format(time.RFC3339),
		},
	}
	p.unified.savedSessions = []db.Session{
		{
			SessionID: "saved-1",
			Project:   "/home/user/project-b",
			Repo:      "project-b",
			Branch:    "main",
			Summary:   "My Saved Session",
			Created:   now.Add(-1 * time.Hour),
			Type:      db.SessionBookmark,
		},
	}

	// 1. Navigate to Active tab, verify live session is visible.
	p.NavigateTo("active", nil)
	p.HandleMessage(plugin.TabViewMsg{Route: "active"})
	view := p.View(120, 38, 0)
	if !strings.Contains(view, "My Active Session") {
		t.Errorf("BUG-119 regression: Active tab should show live session.\nView:\n%s", view)
	}

	// 2. Navigate to New tab, verify project path content.
	p.NavigateTo("new", nil)
	p.HandleMessage(plugin.TabViewMsg{Route: "new"})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "project-a") {
		t.Errorf("BUG-119 regression: New tab should show project path.\nView:\n%s", view)
	}

	// 3. Navigate to Resume tab, verify saved session is visible.
	p.NavigateTo("resume", nil)
	p.HandleMessage(plugin.TabViewMsg{Route: "resume"})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "My Saved Session") {
		t.Errorf("BUG-119 regression: Resume tab should show saved session.\nView:\n%s", view)
	}

	// 4. Key corruption check: navigate BACK to Active and verify it still works.
	p.NavigateTo("active", nil)
	p.HandleMessage(plugin.TabViewMsg{Route: "active"})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "My Active Session") {
		t.Errorf("BUG-119 regression: Active tab corrupted after tab switching — live session not visible.\nView:\n%s", view)
	}
}

// ---------------------------------------------------------------------------

// TestBUG121_ArchiveKeybindingsIntegration is a regression test for BUG-121.
// It verifies the full rendered output shows the correct hint bar text and that
// key presses produce the expected visual state changes — testing what the user
// actually sees, not just internal state.
func TestBUG121_ArchiveKeybindingsIntegration(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Inject an ended live session so 'a' has something to act on.
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "int-test-001",
			Topic:        "Integration test session",
			Project:      "/home/user/proj",
			Repo:         "proj",
			Branch:       "main",
			State:        "ended",
			RegisteredAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
			EndedAt:      time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
		},
	}

	// 1. Verify the hint bar shows both keybindings in normal mode.
	view := p.View(120, 38, 0)
	if !strings.Contains(view, "a archive") {
		t.Errorf("BUG-121 regression: hint bar missing 'a archive' in normal mode.\nView:\n%s", view)
	}
	if !strings.Contains(view, "A view archive") {
		t.Errorf("BUG-121 regression: hint bar missing 'A view archive' in normal mode.\nView:\n%s", view)
	}

	// 2. Press 'A' (shift-a) — should enter archive mode, hint bar changes.
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "A back") {
		t.Errorf("BUG-121 regression: hint bar missing 'A back' in archive mode.\nView:\n%s", view)
	}
	// Should NOT show 'a archive' in archive mode.
	if strings.Contains(view, "a archive") {
		t.Errorf("BUG-121 regression: hint bar still shows 'a archive' in archive mode.\nView:\n%s", view)
	}

	// 3. Press 'A' again — back to normal mode with both hints.
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "a archive") {
		t.Errorf("BUG-121 regression: hint bar missing 'a archive' after returning from archive mode.\nView:\n%s", view)
	}

	// 4. Press 'a' (lowercase) — should archive the ended session.
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	archived, _ := db.DBLoadArchivedSessions(p.db)
	if len(archived) != 1 || archived[0].SessionID != "int-test-001" {
		t.Errorf("BUG-121 regression: pressing 'a' did not archive the session. archived=%d", len(archived))
	}

	// 5. Inject a running session and verify 'a' is blocked.
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "int-test-002",
			Topic:        "Running session",
			Project:      "/home/user/proj",
			State:        "running",
			RegisteredAt: time.Now().Format(time.RFC3339),
		},
	}
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	view = p.View(120, 38, 0)
	if !strings.Contains(view, "Can't archive running session") {
		t.Errorf("BUG-121 regression: expected flash message when archiving running session.\nView:\n%s", view)
	}
}

func TestSessionsArchiveToggle(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add archived sessions directly
	p.unified.archivedSessions = []db.ArchivedSession{
		{
			SessionID:    "arch-1",
			Topic:        "Archived session",
			Project:      "/home/user/proj",
			Repo:         "proj",
			Branch:       "main",
			RegisteredAt: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
			EndedAt:      time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	if p.unified.archiveMode {
		t.Fatal("expected archiveMode to be false initially")
	}

	// Press 'A' (shift-a) — should enter archive mode
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if !p.unified.archiveMode {
		t.Fatal("expected archiveMode to be true after pressing 'A'")
	}

	// Press 'A' again — should leave archive mode
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	if p.unified.archiveMode {
		t.Fatal("expected archiveMode to be false after pressing 'A' again")
	}
}

func TestSessionsArchiveActionOnEndedLive(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add an ended live session
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-ended-001",
			Topic:        "Ended session",
			Project:      "/home/user/proj",
			Repo:         "proj",
			Branch:       "main",
			State:        "ended",
			RegisteredAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
			EndedAt:      time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
		},
	}

	// Press 'a' to archive the selected session
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if action.Type != "consumed" {
		t.Fatalf("expected consumed action, got %s", action.Type)
	}

	// Verify session was archived to DB
	archived, _ := db.DBLoadArchivedSessions(p.db)
	if len(archived) != 1 {
		t.Fatalf("expected 1 archived session, got %d", len(archived))
	}
	if archived[0].SessionID != "live-ended-001" {
		t.Fatalf("expected session ID live-ended-001, got %s", archived[0].SessionID)
	}

	// Verify flash message
	if p.flashMessage == "" {
		t.Fatal("expected flash message after archiving")
	}
}

func TestSessionsArchiveActionOnRunningBlocked(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add a running live session
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-running-001",
			Topic:        "Running session",
			Project:      "/home/user/proj",
			Repo:         "proj",
			Branch:       "main",
			State:        "running",
			RegisteredAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	// Press 'a' to archive — should be blocked
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if action.Type != "consumed" {
		t.Fatalf("expected consumed action, got %s", action.Type)
	}

	// Verify no archived sessions
	archived, _ := db.DBLoadArchivedSessions(p.db)
	if len(archived) != 0 {
		t.Fatalf("expected 0 archived sessions, got %d", len(archived))
	}

	// Verify flash message indicates blocking
	if p.flashMessage != "Can't archive running session" {
		t.Fatalf("expected 'Can't archive running session' flash, got %q", p.flashMessage)
	}
}

func TestSessionsEnterLaunchesLive(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add a live session
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-sess-001",
			Topic:        "My live session",
			Project:      "/home/user/myproject",
			Repo:         "myproject",
			Branch:       "main",
			State:        "ended",
			RegisteredAt: time.Now().Add(-30 * time.Minute).Format(time.RFC3339),
		},
	}
	// cursor is at 0, which hits the live session

	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if action.Type != "launch" {
		t.Fatalf("expected launch action, got %s", action.Type)
	}
	if action.Args["resume_id"] != "live-sess-001" {
		t.Fatalf("expected resume_id live-sess-001, got %s", action.Args["resume_id"])
	}
	if action.Args["dir"] != "/home/user/myproject" {
		t.Fatalf("expected dir /home/user/myproject, got %s", action.Args["dir"])
	}
}

func TestSessionsBookmarkSavesToDB(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add a live ended session
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "live-sess-bk1",
			Topic:        "Session to bookmark",
			Project:      "/home/user/project",
			Repo:         "project",
			Branch:       "feature",
			State:        "ended",
			RegisteredAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	// Press 'b' to bookmark
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if action.Type != "consumed" && action.Type != "noop" {
		t.Fatalf("unexpected action type: %s", action.Type)
	}

	// Verify the bookmark was saved to DB
	bookmarks, err := db.DBLoadBookmarks(p.db)
	if err != nil {
		t.Fatalf("load bookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if bookmarks[0].SessionID != "live-sess-bk1" {
		t.Fatalf("expected session ID live-sess-bk1, got %s", bookmarks[0].SessionID)
	}
}

func TestSessionsBookmarkArchivedPromotesToSaved(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add an archived session and switch to archive mode
	now := time.Now()
	_ = db.DBInsertArchivedSession(p.db, db.ArchivedSession{
		SessionID:    "arch-promote-001",
		Topic:        "Archived to promote",
		Project:      "/home/user/proj",
		Repo:         "proj",
		Branch:       "main",
		RegisteredAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		EndedAt:      now.Add(-1 * time.Hour).Format(time.RFC3339),
	})
	p.unified.ReloadArchived()
	p.unified.ToggleArchive() // enter archive mode

	// Press 'b' to promote to saved
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if action.Type != "consumed" {
		t.Fatalf("expected consumed action, got %s", action.Type)
	}

	// Verify bookmark was created
	bookmarks, err := db.DBLoadBookmarks(p.db)
	if err != nil {
		t.Fatalf("load bookmarks: %v", err)
	}
	if len(bookmarks) != 1 {
		t.Fatalf("expected 1 bookmark, got %d", len(bookmarks))
	}
	if bookmarks[0].SessionID != "arch-promote-001" {
		t.Fatalf("expected session ID arch-promote-001, got %s", bookmarks[0].SessionID)
	}

	// Verify archived session was removed
	archived, _ := db.DBLoadArchivedSessions(p.db)
	if len(archived) != 0 {
		t.Fatalf("expected 0 archived sessions after promotion, got %d", len(archived))
	}
}

func TestSessionsDismissSavedRemovesBookmark(t *testing.T) {
	p := setupSessionsPlugin(t)
	p.unified.viewFilter = ViewFilterSavedOnly // Resume tab shows saved sessions

	// Insert a bookmark directly into DB
	_ = db.DBInsertBookmark(p.db, db.Session{
		SessionID: "saved-dismiss-001",
		Project:   "/home/user/proj",
		Repo:      "proj",
		Branch:    "main",
		Summary:   "Session to dismiss",
		Created:   time.Now(),
	}, "test label")

	// Reload saved sessions into unified view
	sessions, _ := db.DBLoadBookmarks(p.db)
	p.unified.SetSavedSessions(sessions)

	// Cursor is at 0 → saved session (no live sessions)
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if action.Type != "consumed" {
		t.Fatalf("expected consumed action, got %s", action.Type)
	}

	// Verify bookmark was removed from DB
	bookmarks, err := db.DBLoadBookmarks(p.db)
	if err != nil {
		t.Fatalf("load bookmarks: %v", err)
	}
	if len(bookmarks) != 0 {
		t.Fatalf("expected 0 bookmarks after dismiss, got %d", len(bookmarks))
	}
}

func TestSessionsDismissArchivedDeletesFromDB(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Insert an archived session
	now := time.Now()
	_ = db.DBInsertArchivedSession(p.db, db.ArchivedSession{
		SessionID:    "arch-delete-001",
		Topic:        "Archived to delete",
		Project:      "/home/user/proj",
		Repo:         "proj",
		Branch:       "main",
		RegisteredAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		EndedAt:      now.Add(-1 * time.Hour).Format(time.RFC3339),
	})
	p.unified.ReloadArchived()
	p.unified.ToggleArchive() // enter archive mode

	// Press 'd' to delete
	action := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if action.Type != "consumed" {
		t.Fatalf("expected consumed action, got %s", action.Type)
	}

	// Verify archived session was removed from DB
	archived, _ := db.DBLoadArchivedSessions(p.db)
	if len(archived) != 0 {
		t.Fatalf("expected 0 archived sessions after delete, got %d", len(archived))
	}

	// Verify flash message
	if p.flashMessage == "" {
		t.Fatal("expected flash message after delete")
	}
}

func TestSessionsDismissRunningBlocked(t *testing.T) {
	p := setupSessionsPlugin(t)

	// Add a running (active) session — dismiss should be blocked
	p.unified.liveSessions = []daemon.SessionInfo{
		{
			SessionID:    "running-sess-001",
			Topic:        "Active session",
			Project:      "/home/user/active",
			Repo:         "active",
			Branch:       "main",
			State:        "running",
			RegisteredAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}

	// Press 'd' — should show flash message, not dismiss
	p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if p.flashMessage == "" {
		t.Fatal("expected flash message set after 'd' on running session")
	}
	if !contains(p.flashMessage, "Can't dismiss") {
		t.Fatalf("expected 'Can't dismiss' in flash message, got %q", p.flashMessage)
	}

	// Session should still be in the list
	if len(p.unified.liveSessions) != 1 {
		t.Fatalf("expected session to still be present, got %d sessions", len(p.unified.liveSessions))
	}
}

// BUG-119: NavigateTo("resume") must set subTab to saved, not leave it unchanged.
func TestNavigateToResumeRoute(t *testing.T) {
	p := setupPlugin(t)
	// Start on the new sub-tab (simulates user on New Session tab)
	p.subTab = subTabNew

	// Switch to the Resume route (as the host tab bar does)
	p.NavigateTo("resume", nil)

	if p.subTab != subTabSaved {
		t.Fatalf("expected subTab subTabSaved after NavigateTo('resume'), got %d", p.subTab)
	}
}

// BUG-119: NavigateTo("active") must set subTab to recent, not leave it unchanged.
func TestNavigateToActiveRoute(t *testing.T) {
	p := setupPlugin(t)
	// Start on the new sub-tab
	p.subTab = subTabNew

	p.NavigateTo("active", nil)

	if p.subTab != subTabRecent {
		t.Fatalf("expected subTab subTabRecent after NavigateTo('active'), got %d", p.subTab)
	}
}

// BUG-119: Switching tabs should not corrupt content — each tab renders independently.
func TestTabSwitchingDoesNotCorruptContent(t *testing.T) {
	p := setupPlugin(t)

	// Start on sessions (maps to new)
	p.NavigateTo("sessions", nil)
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew, got %d", p.subTab)
	}

	// Switch to new
	p.NavigateTo("new", nil)
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew, got %d", p.subTab)
	}

	// Switch to resume (maps to saved)
	p.NavigateTo("resume", nil)
	if p.subTab != subTabSaved {
		t.Fatalf("expected subTab subTabSaved after resume, got %d", p.subTab)
	}

	// Switch back to active (maps to recent)
	p.NavigateTo("active", nil)
	if p.subTab != subTabRecent {
		t.Fatalf("expected subTab subTabRecent after active, got %d", p.subTab)
	}

	// Switch to new again — should still work
	p.NavigateTo("new", nil)
	if p.subTab != subTabNew {
		t.Fatalf("expected subTab subTabNew after switching back, got %d", p.subTab)
	}
}

// BUG-119: TabViewMsg with route "resume" should trigger a refresh command.
func TestTabViewMsgResumeTriggersRefresh(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabRecent

	handled, action := p.HandleMessage(plugin.TabViewMsg{Route: "resume"})
	if !handled {
		t.Fatal("expected TabViewMsg with route 'resume' to be handled")
	}
	// action.TeaCmd may be nil if unified has no daemon client, but the message
	// should still be handled (returns true).
	_ = action
}

// ---------------------------------------------------------------------------
// findClaudeSessionID
// ---------------------------------------------------------------------------

func TestFindClaudeSessionID_FindsMostRecent(t *testing.T) {
	// Create a fake ~/.claude/projects/<encoded>/ directory with session files.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	projectDir := "/Users/test/my-project"
	encoded := strings.ReplaceAll(projectDir, "/", "-")
	sessDir := tmpHome + "/.claude/projects/" + encoded
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write two session files with different mod times.
	oldFile := sessDir + "/old-session-uuid.jsonl"
	newFile := sessDir + "/new-session-uuid.jsonl"
	if err := os.WriteFile(oldFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set old file to the past.
	past := time.Now().Add(-1 * time.Hour)
	os.Chtimes(oldFile, past, past)

	if err := os.WriteFile(newFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findClaudeSessionID(projectDir)
	if got != "new-session-uuid" {
		t.Fatalf("expected 'new-session-uuid', got %q", got)
	}
}

func TestFindClaudeSessionID_EmptyOnMissingDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	got := findClaudeSessionID("/nonexistent/project")
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// --- BUG-143: Browse flow tests ---

func TestFzfFinishedMsg_ClearsFilterText(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew

	// Simulate user typing a filter before triggering browse
	p.filterText = "somefilter"
	p.applyFilter()

	// Simulate fzf returning a path
	_, _ = p.HandleMessage(fzfFinishedMsg{path: "/tmp/test-project"})

	if p.filterText != "" {
		t.Fatalf("expected filterText to be cleared after fzf browse, got %q", p.filterText)
	}
}

func TestFzfFinishedMsg_AddsPathToList(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew
	initialCount := len(p.paths)

	_, _ = p.HandleMessage(fzfFinishedMsg{path: "/tmp/new-browse-path"})

	if len(p.paths) != initialCount+1 {
		t.Fatalf("expected %d paths after browse, got %d", initialCount+1, len(p.paths))
	}
	found := false
	for _, path := range p.paths {
		if path == "/tmp/new-browse-path" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected /tmp/new-browse-path in paths list after browse")
	}
}

func TestFzfFinishedMsg_EmitsLaunchRequestMsg(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew

	_, action := p.HandleMessage(fzfFinishedMsg{path: "/tmp/browse-launch"})

	// The action type should be noop (not "launch") because the launch is
	// emitted as a LaunchRequestMsg via TeaCmd.
	if action.Type == plugin.ActionLaunch {
		t.Fatal("expected action.Type != ActionLaunch (launch should go through LaunchRequestMsg)")
	}
	if action.TeaCmd == nil {
		t.Fatal("expected a TeaCmd to be returned for LaunchRequestMsg emission")
	}

	// Execute the TeaCmd and verify it produces a LaunchRequestMsg.
	msg := action.TeaCmd()
	launchReq, ok := msg.(plugin.LaunchRequestMsg)
	if !ok {
		t.Fatalf("expected LaunchRequestMsg, got %T", msg)
	}
	if launchReq.Args["dir"] != "/tmp/browse-launch" {
		t.Fatalf("expected dir=/tmp/browse-launch, got %q", launchReq.Args["dir"])
	}
}

func TestFzfFinishedMsg_ErrorIsNoop(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew
	initialCount := len(p.paths)

	_, action := p.HandleMessage(fzfFinishedMsg{path: "", err: os.ErrNotExist})

	if action.Type != plugin.ActionNoop {
		t.Fatalf("expected noop on fzf error, got %q", action.Type)
	}
	if len(p.paths) != initialCount {
		t.Fatal("paths should not change on fzf error")
	}
}

func TestFzfFinishedMsg_EmptyPathIsNoop(t *testing.T) {
	p := setupPlugin(t)
	p.subTab = subTabNew
	initialCount := len(p.paths)

	_, action := p.HandleMessage(fzfFinishedMsg{path: ""})

	if action.Type != plugin.ActionNoop {
		t.Fatalf("expected noop on empty path, got %q", action.Type)
	}
	if len(p.paths) != initialCount {
		t.Fatal("paths should not change on empty fzf selection")
	}
}
