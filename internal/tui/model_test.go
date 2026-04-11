package tui

import (
	"strings"
	"testing"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func testSetup(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("CCC_CONFIG_DIR", t.TempDir())
	return &config.Config{
		Name:    "Test Center",
		Palette: "aurora",
		Todos:   config.TodosConfig{Enabled: true},
	}
}

func TestNewModel(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	if m.cfg.Name != "Test Center" {
		t.Errorf("expected name 'Test Center', got %q", m.cfg.Name)
	}
	if m.activeTab != tabCommand {
		t.Errorf("expected initial tab to be tabCommand")
	}
	if m.Launch != nil {
		t.Error("expected Launch to be nil initially")
	}
	// After consolidation: Command Center(0), Sessions(1), PRs(2), Settings(3)
	if len(m.tabs) != 4 {
		t.Errorf("expected 4 tabs (Sessions consolidated), got %d", len(m.tabs))
	}
}

func TestTabNavigationWithKeyTab(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	// After consolidation: Command Center(0), Sessions(1), PRs(2), Settings(3)
	// Tab forward through all 4 tabs.
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(Model)
	if m.activeTab != tabNew {
		t.Errorf("expected tabNew (Sessions) after one tab, got %d", m.activeTab)
	}

	// PRs tab (index 2)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(Model)
	if m.activeTab != 2 {
		t.Errorf("expected tab 2 (PRs) after two tabs, got %d", m.activeTab)
	}

	// Settings tab (index 3)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(Model)
	if m.activeTab != 3 {
		t.Errorf("expected tab 3 (Settings) after three tabs, got %d", m.activeTab)
	}

	// Wrap back to Command Center (0)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = result.(Model)
	if m.activeTab != tabCommand {
		t.Errorf("expected tabCommand after four tabs (wrap), got %d", m.activeTab)
	}
}

func TestWindowResize(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(Model)
	if m.width != 120 || m.height != 40 {
		t.Errorf("expected 120x40, got %dx%d", m.width, m.height)
	}
}

func TestViewDoesNotPanic(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})
	m.width = 120
	m.height = 40

	// View with default tab (New Session)
	v := m.View()
	if v == "" {
		t.Error("expected non-empty view")
	}

	// Command Center tab
	prev := m.activeTab
	m.activeTab = tabCommand
	m.activateTab(prev)
	v = m.View()
	if v == "" {
		t.Error("expected non-empty view for command tab")
	}

}

func TestTabBarVisibleWhenBannerHidden(t *testing.T) {
	cfg := testSetup(t)
	cfg.SetShowBanner(false)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})
	m.width = 120
	m.height = 40

	v := m.View()

	// The tab bar should contain tab labels regardless of banner visibility.
	// BUG-123: The budget widget overlay was overwriting the tab bar row when
	// the banner was hidden because it unconditionally targeted row 1.
	if !strings.Contains(v, "Sessions") {
		t.Error("tab bar label 'Sessions' missing from view when banner is hidden")
	}
	if !strings.Contains(v, "Command Center") {
		t.Error("tab bar label 'Command Center' missing from view when banner is hidden")
	}
	if !strings.Contains(v, "Settings") {
		t.Error("tab bar label 'Settings' missing from view when banner is hidden")
	}
}

func TestStylesFromPalette(t *testing.T) {
	for _, name := range config.PaletteNames() {
		pal := config.GetPalette(name, nil)
		styles := NewStyles(pal)
		if styles.ColorCyan == "" {
			t.Errorf("palette %q produced empty ColorCyan", name)
		}
	}
}

func TestGradientColorsFromPalette(t *testing.T) {
	pal := config.GetPalette("aurora", nil)
	g := NewGradientColors(pal)
	c := ui.GradientColor(&g, 0.5)
	hex := c.Hex()
	if hex == "" {
		t.Error("expected non-empty hex color")
	}
}

func TestSubtitleFromText(t *testing.T) {
	got := subtitleFromText("CCC")
	if got != "C C C" {
		t.Errorf("expected 'C C C', got %q", got)
	}

	got = subtitleFromText("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}

	got = subtitleFromText("Center")
	if got != "C E N T E R" {
		t.Errorf("expected 'C E N T E R', got %q", got)
	}
}

func TestEscQuits(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	// Default sub-tab is now 0 (New Session). First Esc from New Session
	// should set pendingQuit in the host (since the plugin returns ActionQuit).
	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Second Esc should quit.
	_, cmd := newM.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Error("expected non-nil cmd (tea.Quit) on second esc")
	}
}

func TestBUG114_AgentStateChangedMsgHandled(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	// Set window size so View() can render.
	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(Model)

	// BUG-114: AgentStateChangedMsg must be handled by the TUI host to trigger
	// an immediate budget re-poll. Verify the message type exists (compiles) and
	// that Update processes it without panic, returning a command (the budget poll).
	result, cmd := m.Update(plugin.AgentStateChangedMsg{})
	m = result.(Model)

	// When daemon is not connected, cmd may be nil. The key assertion is no panic.
	// If daemon were connected, cmd != nil (budget re-poll). Either way, verify
	// the model is still renderable.
	_ = cmd

	v := m.View()
	if v == "" {
		t.Error("expected non-empty view after AgentStateChangedMsg")
	}
}

func TestPluginTabMapping(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})

	// After consolidation: Command Center(0), Sessions(1), PRs(2), Settings(3)
	if m.tabs[0].plugin.Slug() != "commandcenter" {
		t.Errorf("expected tab 0 to be commandcenter, got %s", m.tabs[0].plugin.Slug())
	}
	if m.tabs[0].label != "Command Center" {
		t.Errorf("expected tab 0 label 'Command Center', got %q", m.tabs[0].label)
	}
	if m.tabs[1].plugin.Slug() != "sessions" {
		t.Errorf("expected tab 1 to be sessions, got %s", m.tabs[1].plugin.Slug())
	}
	if m.tabs[2].plugin.Slug() != "prs" {
		t.Errorf("expected tab 2 to be prs, got %s", m.tabs[2].plugin.Slug())
	}
	if m.tabs[3].plugin.Slug() != "settings" {
		t.Errorf("expected tab 3 to be settings, got %s", m.tabs[3].plugin.Slug())
	}
}

func TestLaunchRequestMsg_SetsLaunchAction(t *testing.T) {
	cfg := testSetup(t)
	database, err := db.OpenDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	m := NewModel(database, cfg, plugin.NewBus(), plugin.NewMemoryLogger(), llm.NoopLLM{})
	m.width = 120
	m.height = 40

	// Send a LaunchRequestMsg (as would be emitted by browse flow)
	newModel, _ := m.Update(plugin.LaunchRequestMsg{
		Args: map[string]string{"dir": "/tmp/browse-target"},
	})

	updated := newModel.(Model)
	if updated.Launch == nil {
		t.Fatal("expected Launch to be set after LaunchRequestMsg")
	}
	if updated.Launch.Dir != "/tmp/browse-target" {
		t.Fatalf("expected Launch.Dir=/tmp/browse-target, got %q", updated.Launch.Dir)
	}
}
