// Package sessions implements the sessions plugin for CCC.
// It manages sub-views: "Sessions" (unified live + saved + archived),
// "New Session" (browse project paths), and "Worktrees".
package sessions

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	"github.com/anutron/claude-command-center/internal/worktree"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)


// ---------------------------------------------------------------------------
// Local item types (no tui import needed)
// ---------------------------------------------------------------------------

// newItem represents a "new session" entry in the launcher list.
type newItem struct {
	path     string
	label    string
	isBrowse bool
}

func (i newItem) Title() string       { return i.label }
func (i newItem) Description() string { return i.path }
func (i newItem) FilterValue() string { return i.label + " " + i.path }

// substringFilter is a case-insensitive substring filter for the list.
// Unlike the default fuzzy filter, this requires the search term to appear
// as a contiguous substring, which matches user expectations for short inputs.
func substringFilter(term string, targets []string) []list.Rank {
	term = strings.ToLower(term)
	var ranks []list.Rank
	for i, t := range targets {
		lower := strings.ToLower(t)
		idx := strings.Index(lower, term)
		if idx >= 0 {
			matchedIndexes := make([]int, len(term))
			for j := range term {
				matchedIndexes[j] = idx + j
			}
			ranks = append(ranks, list.Rank{
				Index:          i,
				MatchedIndexes: matchedIndexes,
			})
		}
	}
	return ranks
}

// worktreeItem represents a CCC-managed worktree in the worktrees sub-tab.
type worktreeItem struct {
	info    worktree.WorktreeInfo
	project string // display name (basename of repo root)
}

// ---------------------------------------------------------------------------
// Local styles
// ---------------------------------------------------------------------------

type sessionStyles struct {
	activeTab    lipgloss.Style
	inactiveTab  lipgloss.Style
	hint         lipgloss.Style
	sectionHeader lipgloss.Style
	selectedItem lipgloss.Style
	titleBoldC   lipgloss.Style
	titleBoldW   lipgloss.Style
	descMuted    lipgloss.Style
	branchYellow lipgloss.Style
	colorCyan    lipgloss.Color
	colorWhite   lipgloss.Color
}

func newSessionStyles(p config.Palette) sessionStyles {
	colorCyan := lipgloss.Color(p.Cyan)
	colorMuted := lipgloss.Color(p.Muted)
	colorWhite := lipgloss.Color(p.White)
	colorYellow := lipgloss.Color(p.Yellow)
	colorSelectedBg := lipgloss.Color(p.SelectedBg)

	return sessionStyles{
		activeTab:     lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		inactiveTab:   lipgloss.NewStyle().Foreground(colorMuted),
		hint:          lipgloss.NewStyle().Foreground(colorMuted),
		sectionHeader: lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		selectedItem:  lipgloss.NewStyle().Foreground(colorWhite).Background(colorSelectedBg),
		titleBoldC:    lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		titleBoldW:    lipgloss.NewStyle().Foreground(colorWhite).Bold(true),
		descMuted:     lipgloss.NewStyle().Foreground(colorMuted),
		branchYellow:  lipgloss.NewStyle().Foreground(colorYellow),
		colorCyan:     colorCyan,
		colorWhite:    colorWhite,
	}
}


// ---------------------------------------------------------------------------
// Item delegate
// ---------------------------------------------------------------------------

type itemDelegate struct {
	frame  int
	styles *sessionStyles
	grad   *ui.GradientColors
}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	selected := index == m.Index()
	width := m.Width() - 4

	var title, desc string
	pointer := "  "
	if selected && d.grad != nil {
		pointer = ui.PulsingPointerStyle(d.grad, d.frame).Render("> ")
	}

	switch it := item.(type) {
	case newItem:
		if it.isBrowse {
			title = d.styles.titleBoldC.Render("+ " + it.Title())
		} else {
			title = d.styles.titleBoldW.Render(it.Title())
			desc = "  " + d.styles.descMuted.Render(it.path)
		}

	default:
		title = item.FilterValue()
	}

	line := title + desc
	line = truncate(line, width)

	if selected {
		line = d.styles.selectedItem.Render(line)
	}

	fmt.Fprintf(w, "%s%s", pointer, line)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return s
	}
	if ansi.StringWidth(s) > max {
		return ansi.Truncate(s, max-1, "...")
	}
	return s
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type fzfFinishedMsg struct {
	path string
	err  error
}

type fzfProcess struct {
	output string
	stdin  io.Reader
	stderr io.Writer
}

func (f *fzfProcess) SetStdin(r io.Reader)  { f.stdin = r }
func (f *fzfProcess) SetStdout(_ io.Writer) {}
func (f *fzfProcess) SetStderr(w io.Writer) { f.stderr = w }

func (f *fzfProcess) Run() error {
	home, _ := os.UserHomeDir()
	var buf bytes.Buffer
	cmd := exec.Command("fzf",
		"--walker=dir",
		"--walker-root="+home,
		"--walker-skip=.git,node_modules,.venv,__pycache__,.cache,.Trash,Library",
		"--scheme=path",
		"--exact",
		"--ansi",
		"--layout=reverse",
		"--prompt=  path: ",
	)
	cmd.Stdin = f.stdin
	cmd.Stdout = &buf
	cmd.Stderr = f.stderr
	err := cmd.Run()
	if err != nil {
		return err
	}
	f.output = strings.TrimSpace(buf.String())
	return nil
}

// ---------------------------------------------------------------------------
// Sub-tab constants
// ---------------------------------------------------------------------------

const (
	subTabNew       = 0
	subTabSaved     = 1
	subTabRecent    = 2
	subTabWorktrees = 3
	subTabCount     = 4
)

// ---------------------------------------------------------------------------
// Plugin
// ---------------------------------------------------------------------------

// Plugin implements plugin.Plugin for session management.
type Plugin struct {
	db     *sql.DB
	cfg    *config.Config
	bus    plugin.EventBus
	logger plugin.Logger
	llm    llm.LLM

	styles sessionStyles
	grad   ui.GradientColors

	newList       list.Model
	paths         []string
	confirming    bool
	confirmYes    bool
	confirmItem   newItem
	spinner       spinner.Model
	width         int
	height        int
	subTab        int // one of subTabNew, subTabSaved, subTabRecent, subTabWorktrees
	frame         int

	// Worktrees sub-tab state
	worktreeItems         []worktreeItem
	worktreeCursor        int
	worktreeWarning       string        // non-empty = show warning overlay
	worktreeConfirmAction string        // "delete" or "prune"
	worktreeConfirmTarget string        // display label for confirmation

	pendingLaunchTodo *db.Todo

	// Type-to-filter: characters typed on new tab are collected here
	// and applied as a substring filter without requiring a '/' prefix.
	filterText string

	// Unified sessions view (replaces activeView + resumeList)
	unified        *unifiedView
	flashMessage   string
	flashMessageAt time.Time
}

// Slug returns the plugin identifier.
func (p *Plugin) Slug() string { return "sessions" }

// TabName returns the display name shown in the tab bar.
func (p *Plugin) TabName() string { return "Sessions" }

// Init initialises the plugin with context from the host.
func (p *Plugin) Init(ctx plugin.Context) error {
	p.db = ctx.DB
	p.cfg = ctx.Config
	p.bus = ctx.Bus
	p.logger = ctx.Logger
	if ctx.LLM != nil {
		p.llm = llm.NewObservableLLM(ctx.LLM, func(topic string, payload llm.EventPayload) {
			if p.bus != nil {
				p.bus.Publish(plugin.Event{
					Source:  "sessions",
					Topic:   topic,
					Payload: payload,
				})
			}
		}, "sessions")
	} else {
		p.llm = llm.NoopLLM{}
	}

	pal := config.GetPalette(p.cfg.Palette, p.cfg.Colors)
	p.styles = newSessionStyles(pal)
	if ctx.Grad != nil {
		p.grad = *ctx.Grad
	} else {
		p.grad = ui.NewGradientColors(pal)
	}

	p.subTab = subTabNew

	paths, _ := db.DBLoadPaths(p.db)
	// Ensure home_dir is in the paths list (at the front) if configured
	if hd := p.cfg.HomeDir; hd != "" {
		found := false
		for _, pa := range paths {
			if pa == hd {
				found = true
				break
			}
		}
		if !found {
			paths = append([]string{hd}, paths...)
			if p.db != nil {
				_ = db.DBAddPath(p.db, hd)
			}
		}
	}
	p.paths = paths

	newItems := p.buildNewItems()

	delegate := itemDelegate{styles: &p.styles, grad: &p.grad}
	nl := list.New(newItems, delegate, 0, 10)
	nl.SetShowTitle(false)
	nl.SetShowStatusBar(false)
	nl.SetFilteringEnabled(true)
	nl.SetShowHelp(false)
	nl.Filter = substringFilter
	p.newList = nl

	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = lipgloss.NewStyle().Foreground(p.styles.colorCyan)
	p.spinner = s

	// Initialise unified view (daemon client getter wired later via SetDaemonClientFunc)
	p.unified = NewUnifiedView(nil, p.styles)
	p.unified.db = p.db
	p.unified.viewFilter = ViewFilterLiveOnly // default for when Recent is selected

	// Load initial saved/archived sessions into unified view
	if p.db != nil {
		sessions, _ := db.DBLoadBookmarks(p.db)
		p.unified.SetSavedSessions(sessions)
		p.unified.ReloadArchived()
	}

	// Subscribe to events
	if p.bus != nil {
		p.bus.Subscribe("pending.todo", func(e plugin.Event) {
			m, ok := e.Payload.(map[string]interface{})
			if !ok {
				return
			}
			title, _ := m["title"].(string)
			context, _ := m["context"].(string)
			detail, _ := m["detail"].(string)
			whoWaiting, _ := m["who_waiting"].(string)
			due, _ := m["due"].(string)
			effort, _ := m["effort"].(string)
			p.pendingLaunchTodo = &db.Todo{
				Title:      title,
				Context:    context,
				Detail:     detail,
				WhoWaiting: whoWaiting,
				Due:        due,
				Effort:     effort,
			}
			p.subTab = subTabNew
		})
		// NOTE: data.refreshed and session.* events are handled via
		// plugin.NotifyMsg in HandleMessage, which dispatches an async
		// Refresh() cmd. This avoids mutating shared state directly in
		// event bus handlers (which would race with tea.Cmd goroutines).
	}

	return nil
}

// SetDaemonClientFunc wires the daemon client getter so the unified sessions
// view can fetch live data. Must be called after Init but before the program runs.
func (p *Plugin) SetDaemonClientFunc(fn func() *daemon.Client) {
	if p.unified != nil {
		p.unified.daemonClient = fn
		p.unified.Refresh()
	}
}

// StartCmds returns initial tea.Cmds (e.g., spinner tick) the host should run.
func (p *Plugin) StartCmds() tea.Cmd {
	return p.spinner.Tick
}

// Shutdown cleans up plugin resources.
func (p *Plugin) Shutdown() {}

// Migrations returns any DB migrations needed by this plugin.
func (p *Plugin) Migrations() []plugin.Migration { return nil }

// Routes returns navigable sub-routes.
func (p *Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Slug: "active", Description: "Active sessions (live only)"},
		{Slug: "resume", Description: "Resume sessions (saved/bookmarked)"},
		{Slug: "saved", Description: "Saved sessions (alias for resume)"},
		{Slug: "sessions", Description: "Sessions landing (alias for new)"},
		{Slug: "new", Description: "New session sub-tab"},
		{Slug: "worktrees", Description: "Worktrees sub-tab"},
	}
}

// NavigateTo switches to the requested sub-route.
func (p *Plugin) NavigateTo(route string, args map[string]string) {
	p.filterText = ""
	switch route {
	case "active":
		p.subTab = subTabRecent
		if p.unified != nil {
			p.unified.viewFilter = ViewFilterLiveOnly
		}
	case "sessions":
		// Host sends "sessions" when selecting the Sessions tab — land on New Session.
		p.subTab = subTabNew
		p.applyFilter()
	case "resume", "saved", "sessions/saved":
		p.subTab = subTabSaved
		if p.unified != nil {
			p.unified.viewFilter = ViewFilterSavedOnly
		}
	case "new", "sessions/new":
		p.subTab = subTabNew
		p.applyFilter()
	case "worktrees":
		p.subTab = subTabWorktrees
		p.refreshWorktreeList()
	}
	if todoTitle, ok := args["pending_todo_title"]; ok {
		p.pendingLaunchTodo = &db.Todo{Title: todoTitle}
	}
}

// RefreshInterval returns how often the plugin should auto-refresh.
func (p *Plugin) RefreshInterval() time.Duration { return 0 }

// Refresh returns a tea.Cmd that fetches session data in a background goroutine
// and returns it as a sessionsRefreshMsg for safe application on the main loop.
func (p *Plugin) Refresh() tea.Cmd {
	if p.unified == nil {
		return nil
	}
	// Snapshot state needed by the background goroutine. These reads happen on
	// the main loop and won't race with View(). prevLive is a slice header
	// copy — safe because HandleMessage always replaces the slice wholesale
	// (never appends in-place to the shared backing array).
	prevLive := p.unified.liveSessions
	daemonClientFn := p.unified.daemonClient
	database := p.db

	return func() tea.Msg {
		var msg sessionsRefreshMsg

		if daemonClientFn != nil {
			if client := daemonClientFn(); client != nil {
				if sessions, err := client.ListSessions(); err == nil {
					archiveNewlyEndedSessions(database, prevLive, sessions)
					msg.liveSessions = sessions
				}
				if agents, err := client.ListAgents(); err == nil && len(agents) > 0 {
					msg.agentsByID = make(map[string]daemon.AgentStatusResult, len(agents))
					for _, a := range agents {
						msg.agentsByID[a.ID] = a
					}
				}
			}
		}

		if database != nil {
			msg.savedSessions, _ = db.DBLoadBookmarks(database)
			msg.archivedSessions, _ = db.DBLoadArchivedSessions(database)
		}

		return msg
	}
}

// KeyBindings returns the key bindings for this plugin.
func (p *Plugin) KeyBindings() []plugin.KeyBinding {
	return []plugin.KeyBinding{
		{Key: "1-4", Description: "Switch sub-tabs", Promoted: true},
		{Key: "←/→", Description: "Cycle sub-tabs", Promoted: true},
		{Key: "a", Description: "Archive session", Promoted: true},
		{Key: "A", Description: "View archive", Promoted: true},
		{Key: "w", Description: "Launch in worktree", Promoted: true},
		{Key: "enter", Description: "Launch/resume session", Promoted: true},
		{Key: "b", Description: "Bookmark session", Promoted: true},
		{Key: "d", Description: "Dismiss/delete session", Promoted: true},
		{Key: "shift+up/down", Description: "Reorder paths", Promoted: true},
		{Key: "delete", Description: "Remove saved path", Promoted: true},
		{Key: "esc", Description: "Quit or cancel"},
	}
}

// HandleKey processes key input and returns an action for the host.
func (p *Plugin) HandleKey(msg tea.KeyMsg) plugin.Action {
	// Handle worktree warning overlay (not a git repo)
	if p.worktreeWarning != "" {
		return p.handleWorktreeWarning(msg)
	}

	// Handle worktree confirm overlay (delete/prune)
	if p.worktreeConfirmAction != "" {
		return p.handleWorktreeConfirm(msg)
	}

	if p.confirming {
		return p.handleConfirming(msg)
	}

	// When a filter is active on the new tab, only allow navigation keys
	// and filter-editing keys — don't match single-char shortcuts.
	filtering := p.filterText != "" && p.subTab == subTabNew

	switch msg.String() {
	case "1":
		if !filtering {
			p.subTab = subTabNew
			p.filterText = ""
			return plugin.NoopAction()
		}
	case "2":
		if !filtering {
			p.subTab = subTabSaved
			p.filterText = ""
			if p.unified != nil {
				p.unified.viewFilter = ViewFilterSavedOnly
			}
			if cmd := p.Refresh(); cmd != nil {
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
			}
			return plugin.NoopAction()
		}
	case "3":
		if !filtering {
			p.subTab = subTabRecent
			p.filterText = ""
			if p.unified != nil {
				p.unified.viewFilter = ViewFilterLiveOnly
			}
			if cmd := p.Refresh(); cmd != nil {
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
			}
			return plugin.NoopAction()
		}
	case "4":
		if !filtering {
			p.subTab = subTabWorktrees
			p.filterText = ""
			p.refreshWorktreeList()
			return plugin.NoopAction()
		}
	case "left":
		if !filtering {
			prev := p.subTab
			p.subTab = (p.subTab - 1 + subTabCount) % subTabCount
			p.filterText = ""
			p.syncSubTabState(prev)
			return plugin.NoopAction()
		}
	case "right":
		if !filtering {
			prev := p.subTab
			p.subTab = (p.subTab + 1) % subTabCount
			p.filterText = ""
			p.syncSubTabState(prev)
			return plugin.NoopAction()
		}
	case "esc":
		// If filter is active, clear it first
		if filtering {
			p.filterText = ""
			p.applyFilter()
			return plugin.NoopAction()
		}
		if p.subTab == subTabSaved || p.subTab == subTabRecent || p.subTab == subTabWorktrees {
			p.subTab = subTabNew
			return plugin.NoopAction()
		}
		if p.pendingLaunchTodo != nil {
			p.pendingLaunchTodo = nil
			if p.bus != nil {
				p.bus.Publish(plugin.Event{
					Source:  "sessions",
					Topic:   "pending.todo.cancel",
					Payload: map[string]interface{}{},
				})
			}
			return plugin.Action{Type: plugin.ActionNavigate, Payload: "command"}
		}
		return plugin.Action{Type: plugin.ActionQuit}
	}

	switch p.subTab {
	case subTabRecent, subTabSaved:
		return p.handleSessionsTab(msg)
	case subTabNew:
		return p.handleNewTab(msg)
	case subTabWorktrees:
		return p.handleWorktreesTab(msg)
	}
	return plugin.NoopAction()
}

// syncSubTabState sets viewFilter and triggers refresh when switching sub-tabs
// via arrow keys. Called after p.subTab has been updated.
func (p *Plugin) syncSubTabState(_ int) {
	switch p.subTab {
	case subTabSaved:
		if p.unified != nil {
			p.unified.viewFilter = ViewFilterSavedOnly
		}
	case subTabRecent:
		if p.unified != nil {
			p.unified.viewFilter = ViewFilterLiveOnly
		}
	case subTabWorktrees:
		p.refreshWorktreeList()
	}
}

// applyFilter sets the filter text on the active list for the current sub-tab.
func (p *Plugin) applyFilter() {
	switch p.subTab {
	case subTabNew:
		p.newList.SetFilterText(p.filterText)
	}
}

// savedCount returns the number of saved sessions.
func (p *Plugin) savedCount() int {
	if p.unified == nil {
		return 0
	}
	return len(p.unified.savedSessions)
}

// recentCount returns the number of live sessions.
func (p *Plugin) recentCount() int {
	if p.unified == nil {
		return 0
	}
	return len(p.unified.liveSessions)
}

// worktreeCount returns the number of worktree items.
func (p *Plugin) worktreeCount() int {
	return len(p.worktreeItems)
}

// HandleMessage processes non-key messages.
func (p *Plugin) HandleMessage(msg tea.Msg) (bool, plugin.Action) {
	switch msg := msg.(type) {
	case sessionsRefreshMsg:
		if p.unified != nil {
			if msg.liveSessions != nil {
				p.unified.liveSessions = msg.liveSessions
			}
			p.unified.agentsByID = msg.agentsByID
			if msg.savedSessions != nil {
				p.unified.savedSessions = msg.savedSessions
			}
			if msg.archivedSessions != nil {
				p.unified.archivedSessions = msg.archivedSessions
			}
			p.unified.clampCursor()
		}
		return true, plugin.NoopAction()

	case plugin.NotifyMsg:
		switch msg.Event {
		case "data.refreshed", "session.registered", "session.updated", "session.ended":
			if cmd := p.Refresh(); cmd != nil {
				return true, plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
			}
		}
		return false, plugin.NoopAction()

	case plugin.TabViewMsg:
		if msg.Route == "active" || msg.Route == "sessions" || msg.Route == "resume" || msg.Route == "saved" || msg.Route == "sessions/saved" || msg.Route == "sessions/new" {
			if cmd := p.Refresh(); cmd != nil {
				return true, plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
			}
		}
		// Returns true for all routes (including "new", "worktrees") because
		// this plugin owns them. broadcastMessage doesn't use the handled bool,
		// so the value has no effect on other plugins.
		return true, plugin.NoopAction()

	case fzfFinishedMsg:
		if msg.err != nil || msg.path == "" {
			return true, plugin.NoopAction()
		}
		p.paths = db.AddPath(p.paths, msg.path)
		if p.db != nil {
			_ = db.DBAddPath(p.db, msg.path)
			// Write heuristic description immediately so the path has metadata
			// even if the LLM upgrade doesn't complete before quit.
			if heuristic := db.AutoDescribePath(msg.path); heuristic != "" {
				_ = db.DBUpdatePathDescription(p.db, msg.path, heuristic)
			}
		}
		// Clear any active filter so the list shows all items if the launch
		// is somehow not processed (defensive).
		p.filterText = ""
		p.applyFilter()
		p.newList.SetItems(p.buildNewItems())
		// Fire background LLM description upgrade (may complete before app quits on launch)
		go p.backgroundDescribe(msg.path)
		// Emit a LaunchRequestMsg via tea.Cmd so the host processes the launch.
		// Returning ActionLaunch directly from HandleMessage doesn't work because
		// broadcastMessage only collects TeaCmds and ignores action types.
		launchPath := msg.path
		return true, plugin.Action{
			Type: plugin.ActionNoop,
			TeaCmd: func() tea.Msg {
				return plugin.LaunchRequestMsg{
					Args: map[string]string{"dir": launchPath},
				}
			},
		}

	case pathDescribeFinishedMsg:
		if msg.description != "" && p.db != nil {
			_ = db.DBUpdatePathDescription(p.db, msg.path, msg.description)
		}
		return true, plugin.NoopAction()

	case spinner.TickMsg:
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(msg)
		return true, plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}

	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height
		listWidth := ui.ContentMaxWidth
		if p.width > 0 && p.width < listWidth {
			listWidth = p.width
		}
		listHeight := p.height - 14
		if listHeight < 5 {
			listHeight = 5
		}
		p.newList.SetSize(listWidth, listHeight)
		return true, plugin.NoopAction()
	}

	// Delegate to active list for unhandled messages
	switch p.subTab {
	case subTabNew:
		var cmd tea.Cmd
		p.newList, cmd = p.newList.Update(msg)
		if cmd != nil {
			return true, plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
		}
	}
	return false, plugin.NoopAction()
}

// View renders the plugin's current view.
func (p *Plugin) View(width, height, frame int) string {
	p.frame = frame
	p.width = width
	p.height = height
	p.newList.SetDelegate(itemDelegate{frame: frame, styles: &p.styles, grad: &p.grad})

	tabBar := p.renderSubTabBar()

	var content string
	switch p.subTab {
	case subTabRecent, subTabSaved:
		content = p.viewSessionsTab()
	case subTabNew:
		content = p.viewNewTab()
	case subTabWorktrees:
		content = p.viewWorktreesTab()
	}

	return tabBar + content
}

// ---------------------------------------------------------------------------
// Internal: key handling
// ---------------------------------------------------------------------------

func (p *Plugin) handleNewTab(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "up":
		total := len(p.newList.VisibleItems())
		if total > 0 && p.newList.Index() == 0 {
			p.newList.Select(total - 1)
			return plugin.NoopAction()
		}
		var cmd tea.Cmd
		p.newList, cmd = p.newList.Update(msg)
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "k":
		if p.filterText != "" {
			break // treat as filter char when filtering
		}
		total := len(p.newList.VisibleItems())
		if total > 0 && p.newList.Index() == 0 {
			p.newList.Select(total - 1)
			return plugin.NoopAction()
		}
		var cmd tea.Cmd
		p.newList, cmd = p.newList.Update(msg)
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "down":
		total := len(p.newList.VisibleItems())
		if total > 0 && p.newList.Index() == total-1 {
			p.newList.Select(0)
			return plugin.NoopAction()
		}
		var cmd tea.Cmd
		p.newList, cmd = p.newList.Update(msg)
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "j":
		if p.filterText != "" {
			break // treat as filter char when filtering
		}
		total := len(p.newList.VisibleItems())
		if total > 0 && p.newList.Index() == total-1 {
			p.newList.Select(0)
			return plugin.NoopAction()
		}
		var cmd tea.Cmd
		p.newList, cmd = p.newList.Update(msg)
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "enter":
		item, ok := p.newList.SelectedItem().(newItem)
		if !ok {
			return plugin.NoopAction()
		}
		if item.isBrowse {
			proc := &fzfProcess{}
			cmd := tea.Exec(proc, func(err error) tea.Msg {
				return fzfFinishedMsg{path: proc.output, err: err}
			})
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
		}
		args := map[string]string{"dir": item.path}
		if p.pendingLaunchTodo != nil {
			args["initial_prompt"] = formatTodoContext(*p.pendingLaunchTodo)
			p.pendingLaunchTodo = nil
		}
		p.filterText = ""
		p.applyFilter()
		return plugin.Action{Type: plugin.ActionLaunch, Args: args}

	case "w":
		if p.filterText != "" {
			// When filtering, treat 'w' as a filter character
			break
		}
		item, ok := p.newList.SelectedItem().(newItem)
		if !ok || item.isBrowse {
			return plugin.NoopAction()
		}
		// Check if path is a git repo
		if !isGitRepo(item.path) {
			p.worktreeWarning = item.path
			return plugin.NoopAction()
		}
		return plugin.Action{
			Type: plugin.ActionLaunch,
			Args: map[string]string{"dir": item.path, "worktree": "true"},
		}

	case "shift+up":
		return p.movePathUp()
	case "shift+down":
		return p.movePathDown()

	case "delete":
		item, ok := p.newList.SelectedItem().(newItem)
		if !ok || item.isBrowse {
			return plugin.NoopAction()
		}
		p.confirming = true
		p.confirmYes = false
		p.confirmItem = item
		return plugin.NoopAction()

	case "backspace":
		if p.filterText != "" {
			// Edit filter text
			p.filterText = p.filterText[:len(p.filterText)-1]
			p.applyFilter()
			return plugin.NoopAction()
		}
		// When filter is empty, backspace triggers delete confirmation
		item, ok := p.newList.SelectedItem().(newItem)
		if !ok || item.isBrowse {
			return plugin.NoopAction()
		}
		p.confirming = true
		p.confirmYes = false
		p.confirmItem = item
		return plugin.NoopAction()
	}

	// Type-to-filter: any printable rune appends to the filter
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			p.filterText += string(r)
		}
		p.applyFilter()
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.newList, cmd = p.newList.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

func (p *Plugin) handleSessionsTab(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "up", "k":
		if p.unified != nil {
			p.unified.MoveUp()
		}
		return plugin.NoopAction()
	case "down", "j":
		if p.unified != nil {
			p.unified.MoveDown()
		}
		return plugin.NoopAction()

	case "a":
		// Archive the selected session (verb action).
		if p.unified == nil {
			return plugin.NoopAction()
		}
		sel := p.unified.SelectedItem()
		if sel == nil {
			return plugin.NoopAction()
		}
		switch sel.Tier {
		case TierLive:
			if sel.State == "active" || sel.State == "running" {
				p.flashMessage = "Can't archive running session"
				p.flashMessageAt = time.Now()
				return plugin.ConsumedAction()
			}
			// Write to archived DB, then dismiss from daemon.
			if p.db != nil {
				_ = db.DBInsertArchivedSession(p.db, db.ArchivedSession{
					SessionID:    sel.SessionID,
					Topic:        sel.Topic,
					Project:      sel.Project,
					Repo:         sel.Repo,
					Branch:       sel.Branch,
					WorktreePath: sel.WorktreePath,
					RegisteredAt: sel.RegisteredAt,
					EndedAt:      sel.EndedAt,
				})
				p.unified.ReloadArchived()
			}
			if p.unified.daemonClient != nil {
				client := p.unified.daemonClient()
				if client != nil {
					_ = client.ArchiveSession(daemon.ArchiveSessionParams{SessionID: sel.SessionID})
				}
			}
			p.unified.RemoveSession(sel.SessionID)
			p.flashMessage = "Archived: " + sel.SessionID[:min(8, len(sel.SessionID))]
		case TierSaved:
			// Archive the saved session: write to archive DB, remove bookmark.
			if p.db != nil {
				_ = db.DBInsertArchivedSession(p.db, db.ArchivedSession{
					SessionID:    sel.SessionID,
					Topic:        sel.Topic,
					Project:      sel.Project,
					Repo:         sel.Repo,
					Branch:       sel.Branch,
					WorktreePath: sel.WorktreePath,
					RegisteredAt: sel.RegisteredAt,
				})
				_ = db.DBRemoveBookmark(p.db, sel.SessionID)
				sessions, _ := db.DBLoadBookmarks(p.db)
				p.unified.SetSavedSessions(sessions)
				p.unified.ReloadArchived()
			}
			p.unified.RemoveSession(sel.SessionID)
			p.flashMessage = "Archived: " + sel.SessionID[:min(8, len(sel.SessionID))]
		case TierArchived:
			// Already archived — no-op.
			p.flashMessage = "Already archived"
		}
		p.flashMessageAt = time.Now()
		return plugin.ConsumedAction()

	case "A":
		// View archive list (toggle archive mode).
		if p.unified != nil {
			p.unified.ToggleArchive()
		}
		return plugin.NoopAction()

	case "enter":
		if p.unified == nil {
			return plugin.NoopAction()
		}
		sel := p.unified.SelectedItem()
		if sel == nil {
			return plugin.NoopAction()
		}
		dir := sel.Project
		if sel.WorktreePath != "" {
			dir = sel.WorktreePath
		}
		// For live sessions the daemon session_id is a CCC-generated UUID, not
		// the Claude CLI session UUID. Look up the real Claude session file in
		// ~/.claude/projects/ so that --resume finds it.
		resumeID := sel.SessionID
		if sel.Tier == TierLive {
			if claudeID := findClaudeSessionID(dir); claudeID != "" {
				resumeID = claudeID
			}
		}
		args := map[string]string{
			"dir":       dir,
			"resume_id": resumeID,
		}
		// Pass the CCC session ID so the TUI reuses it instead of
		// generating a new one — this preserves the session's topic.
		if sel.SessionID != "" {
			args["session_id"] = sel.SessionID
		}
		return plugin.Action{
			Type: plugin.ActionLaunch,
			Args: args,
		}

	case "b":
		if p.unified == nil {
			return plugin.NoopAction()
		}
		sel := p.unified.SelectedItem()
		if sel == nil {
			return plugin.NoopAction()
		}
		if sel.Tier == TierLive || sel.Tier == TierArchived {
			if p.db != nil {
				bk := db.Session{
					SessionID:    sel.SessionID,
					Project:      sel.Project,
					Repo:         sel.Repo,
					Branch:       sel.Branch,
					Created:      parseTime(sel.RegisteredAt),
					Summary:      sel.Topic,
					WorktreePath: sel.WorktreePath,
				}
				label := sel.Topic
				if label == "" {
					label = sel.Branch
				}
				_ = db.DBInsertBookmark(p.db, bk, label)
				if sel.Tier == TierArchived {
					_ = db.DBDeleteArchivedSession(p.db, sel.SessionID)
					p.unified.ReloadArchived()
				}
				sessions, _ := db.DBLoadBookmarks(p.db)
				p.unified.SetSavedSessions(sessions)
			}
			p.flashMessage = "Bookmarked: " + sel.SessionID[:min(8, len(sel.SessionID))]
			p.flashMessageAt = time.Now()
		}
		return plugin.ConsumedAction()

	case "d":
		if p.unified == nil {
			return plugin.NoopAction()
		}
		sel := p.unified.SelectedItem()
		if sel == nil {
			return plugin.NoopAction()
		}
		switch sel.Tier {
		case TierLive:
			if sel.State == "active" || sel.State == "running" {
				p.flashMessage = "Can't dismiss running session"
				p.flashMessageAt = time.Now()
				return plugin.ConsumedAction()
			}
			if p.unified.daemonClient != nil {
				client := p.unified.daemonClient()
				if client != nil {
					_ = client.ArchiveSession(daemon.ArchiveSessionParams{SessionID: sel.SessionID})
				}
			}
			p.unified.RemoveSession(sel.SessionID)
			p.flashMessage = "Dismissed: " + sel.SessionID[:min(8, len(sel.SessionID))]
		case TierSaved:
			if p.db != nil {
				_ = db.DBRemoveBookmark(p.db, sel.SessionID)
				sessions, _ := db.DBLoadBookmarks(p.db)
				p.unified.SetSavedSessions(sessions)
			}
			p.flashMessage = "Removed bookmark"
		case TierArchived:
			if p.db != nil {
				_ = db.DBDeleteArchivedSession(p.db, sel.SessionID)
				p.unified.ReloadArchived()
			}
			p.unified.RemoveSession(sel.SessionID)
			p.flashMessage = "Deleted archived session"
		}
		p.flashMessageAt = time.Now()
		return plugin.ConsumedAction()
	}
	return plugin.NoopAction()
}

func (p *Plugin) handleConfirming(msg tea.KeyMsg) plugin.Action {
	doDelete := func() {
		p.paths = db.RemovePath(p.paths, p.confirmItem.path)
		if p.db != nil {
			_ = db.DBRemovePath(p.db, p.confirmItem.path)
		}
		p.newList.SetItems(p.buildNewItems())
	}

	switch msg.String() {
	case "y":
		doDelete()
		p.confirming = false
		return plugin.NoopAction()
	case "enter":
		if p.confirmYes {
			doDelete()
		}
		p.confirming = false
		return plugin.NoopAction()
	case "n", "esc":
		p.confirming = false
		return plugin.NoopAction()
	case "left", "right", "tab":
		p.confirmYes = !p.confirmYes
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

func (p *Plugin) handleWorktreesTab(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "enter":
		if len(p.worktreeItems) == 0 {
			return plugin.NoopAction()
		}
		wt := p.worktreeItems[p.worktreeCursor]
		return plugin.Action{
			Type: plugin.ActionLaunch,
			Args: map[string]string{"dir": wt.info.Path},
		}

	case "d":
		if len(p.worktreeItems) == 0 {
			return plugin.NoopAction()
		}
		wt := p.worktreeItems[p.worktreeCursor]
		label := filepath.Base(wt.info.RepoRoot) + "/" + filepath.Base(wt.info.Path)
		p.worktreeConfirmAction = "delete"
		p.worktreeConfirmTarget = label
		return plugin.NoopAction()

	case "p":
		if len(p.worktreeItems) == 0 {
			return plugin.NoopAction()
		}
		wt := p.worktreeItems[p.worktreeCursor]
		// Count worktrees for this project
		count := 0
		for _, item := range p.worktreeItems {
			if item.info.RepoRoot == wt.info.RepoRoot {
				count++
			}
		}
		p.worktreeConfirmAction = "prune"
		p.worktreeConfirmTarget = fmt.Sprintf("%s? (%d worktrees)", filepath.Base(wt.info.RepoRoot), count)
		return plugin.NoopAction()

	case "up", "k":
		if len(p.worktreeItems) > 0 {
			if p.worktreeCursor > 0 {
				p.worktreeCursor--
			} else {
				p.worktreeCursor = len(p.worktreeItems) - 1
			}
		}
		return plugin.NoopAction()

	case "down", "j":
		if len(p.worktreeItems) > 0 {
			if p.worktreeCursor < len(p.worktreeItems)-1 {
				p.worktreeCursor++
			} else {
				p.worktreeCursor = 0
			}
		}
		return plugin.NoopAction()
	}

	return plugin.NoopAction()
}

func (p *Plugin) handleWorktreeWarning(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "enter":
		// Launch directly in the directory
		dir := p.worktreeWarning
		p.worktreeWarning = ""
		return plugin.Action{
			Type: plugin.ActionLaunch,
			Args: map[string]string{"dir": dir},
		}
	case "esc":
		p.worktreeWarning = ""
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

func (p *Plugin) handleWorktreeConfirm(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "y":
		if len(p.worktreeItems) > 0 {
			wt := p.worktreeItems[p.worktreeCursor]
			switch p.worktreeConfirmAction {
			case "delete":
				_ = worktree.RemoveWorktree(wt.info.RepoRoot, wt.info.Path)
				p.refreshWorktreeList()
			case "prune":
				_, _ = worktree.PruneWorktrees(wt.info.RepoRoot)
				p.refreshWorktreeList()
			}
		}
		p.worktreeConfirmAction = ""
		p.worktreeConfirmTarget = ""
		return plugin.NoopAction()
	case "n", "esc":
		p.worktreeConfirmAction = ""
		p.worktreeConfirmTarget = ""
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

func (p *Plugin) refreshWorktreeList() {
	p.worktreeItems = nil
	seen := map[string]bool{}
	for _, path := range p.paths {
		// Resolve to git repo root to avoid duplicates
		repoRoot := gitRepoRootFor(path)
		if repoRoot == "" || seen[repoRoot] {
			continue
		}
		seen[repoRoot] = true
		wts, err := worktree.ListWorktrees(repoRoot)
		if err != nil {
			continue
		}
		project := filepath.Base(repoRoot)
		for _, wt := range wts {
			p.worktreeItems = append(p.worktreeItems, worktreeItem{
				info:    wt,
				project: project,
			})
		}
	}
	if p.worktreeCursor >= len(p.worktreeItems) {
		p.worktreeCursor = max(0, len(p.worktreeItems)-1)
	}
}

// isGitRepo checks if the given directory is inside a git repository.
func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// gitRepoRootFor returns the git repo root for a directory, or "" if not a git repo.
func gitRepoRootFor(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return resolved
}

// ---------------------------------------------------------------------------
// Internal: views
// ---------------------------------------------------------------------------

func (p *Plugin) renderSubTabBar() string {
	type tabInfo struct {
		name  string
		count int
	}
	tabs := []tabInfo{
		{"New Session", -1}, // -1 means don't show count
		{"Saved", p.savedCount()},
		{"Recent", p.recentCount()},
		{"Worktrees", p.worktreeCount()},
	}
	var parts []string
	for i, t := range tabs {
		var label string
		if t.count >= 0 {
			label = fmt.Sprintf("[%d] %s (%d)", i+1, t.name, t.count)
		} else {
			label = fmt.Sprintf("[%d] %s", i+1, t.name)
		}
		if i == p.subTab {
			parts = append(parts, p.styles.activeTab.Render(label))
		} else {
			parts = append(parts, p.styles.inactiveTab.Render(label))
		}
	}
	bar := strings.Join(parts, "  ")
	return lipgloss.PlaceHorizontal(ui.ContentMaxWidth, lipgloss.Center, bar) + "\n"
}

func (p *Plugin) viewSessionsTab() string {
	if p.unified == nil {
		return p.styles.hint.Render("  Daemon not connected.")
	}
	listView := p.unified.View(p.width, p.height)
	hints := p.renderHints()
	if p.flashMessage != "" && time.Since(p.flashMessageAt) < 3*time.Second {
		flash := p.styles.sectionHeader.Render("  " + p.flashMessage)
		return lipgloss.JoinVertical(lipgloss.Left, listView, "", flash, "", hints)
	}
	return lipgloss.JoinVertical(lipgloss.Left, listView, "", hints)
}

func (p *Plugin) viewNewTab() string {
	var banner string
	if p.pendingLaunchTodo != nil {
		banner = p.styles.sectionHeader.Render("Select project for: ") +
			lipgloss.NewStyle().Foreground(p.styles.colorWhite).Bold(true).Render(p.pendingLaunchTodo.Title) +
			p.styles.hint.Render("  (esc to cancel)")
	}
	listView := p.newList.View()
	hints := p.renderHints()
	if banner != "" {
		return lipgloss.JoinVertical(lipgloss.Left, banner, "", listView, "", hints)
	}
	return lipgloss.JoinVertical(lipgloss.Left, listView, "", hints)
}

func (p *Plugin) viewWorktreesTab() string {
	var lines []string

	if len(p.worktreeItems) == 0 {
		lines = append(lines, p.styles.hint.Render("  No worktrees found. Press w in the new tab to create one."))
	} else {
		currentProject := ""
		for i, wt := range p.worktreeItems {
			// Group header
			if wt.project != currentProject {
				if currentProject != "" {
					lines = append(lines, "")
				}
				lines = append(lines, p.styles.sectionHeader.Render("  "+wt.project))
				currentProject = wt.project
			}

			age := timeAgo(wt.info.CreatedAt)
			branch := p.styles.branchYellow.Render(wt.info.Branch)
			ageStr := p.styles.descMuted.Render("  " + age)

			pointer := "  "
			if i == p.worktreeCursor && p.grad != (ui.GradientColors{}) {
				pointer = ui.PulsingPointerStyle(&p.grad, p.frame).Render("> ")
			}

			line := pointer + branch + ageStr
			if i == p.worktreeCursor {
				line = pointer + p.styles.selectedItem.Render(
					p.styles.branchYellow.Render(wt.info.Branch)+"  "+p.styles.descMuted.Render(age),
				)
			}
			lines = append(lines, line)
		}
	}

	listView := strings.Join(lines, "\n")
	hints := p.renderHints()
	return lipgloss.JoinVertical(lipgloss.Left, listView, "", hints)
}

// parseTime parses an RFC3339 time string, returning zero time on failure.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// sessionAge returns a human-readable age string from an RFC3339 timestamp.
func sessionAge(registered string) string {
	t := parseTime(registered)
	if t.IsZero() {
		return ""
	}
	return timeAgo(t)
}

// timeAgo returns a human-readable duration since t.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

func (p *Plugin) renderHints() string {
	var hints string
	if p.confirming {
		label := p.confirmItem.label
		yesStr := "yes"
		noStr := "no"
		if p.confirmYes {
			yesStr = p.styles.activeTab.Render("> yes")
			noStr = p.styles.inactiveTab.Render("no")
		} else {
			yesStr = p.styles.inactiveTab.Render("yes")
			noStr = p.styles.activeTab.Render("> no")
		}
		hints = p.styles.hint.Render(fmt.Sprintf("Remove %q from saved list?  ", label)) + yesStr + p.styles.hint.Render("  |  ") + noStr
	} else if p.worktreeWarning != "" {
		hints = p.styles.sectionHeader.Render("  ⚠ Not a git repository — worktrees require git.") + "\n" +
			p.styles.hint.Render("  [enter] Launch directly in this directory   [esc] Cancel")
	} else if p.worktreeConfirmAction != "" {
		switch p.worktreeConfirmAction {
		case "delete":
			hints = p.styles.sectionHeader.Render(fmt.Sprintf("  Delete worktree %s?", p.worktreeConfirmTarget)) + "\n" +
				p.styles.hint.Render("  [y] Yes, delete   [n] Cancel")
		case "prune":
			hints = p.styles.sectionHeader.Render(fmt.Sprintf("  Remove all worktrees for %s", p.worktreeConfirmTarget)) + "\n" +
				p.styles.hint.Render("  [y] Yes, prune all   [n] Cancel")
		}
	} else {
		switch p.subTab {
		case subTabRecent, subTabSaved:
			if p.unified != nil && p.unified.archiveMode {
				hints = p.styles.hint.Render("enter resume   b save   d delete   j/k navigate   A back   1-4 tabs   ←/→ cycle")
			} else {
				hints = p.styles.hint.Render("enter resume   b bookmark   d dismiss   j/k navigate   a archive   A view archive   1-4 tabs   ←/→ cycle")
			}
		case subTabNew:
			if p.filterText != "" {
				hints = p.styles.hint.Render(fmt.Sprintf("filter: %s   enter launch   esc clear   backspace edit", p.filterText))
			} else {
				hints = p.styles.hint.Render("type to filter   enter launch   w worktree   1-4 tabs   ←/→ cycle   shift+up/down reorder   del remove   esc quit")
			}
		case subTabWorktrees:
			hints = p.styles.hint.Render("enter launch   d delete   p prune   1-4 tabs   ←/→ cycle   esc back")
		default:
			hints = p.styles.hint.Render("1-4 tabs   ←/→ cycle   esc quit")
		}
	}
	return lipgloss.PlaceHorizontal(ui.ContentMaxWidth, lipgloss.Center, hints)
}

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

// backgroundDescribe runs LLMDescribePath in a goroutine and writes the result
// to DB. Used when the TUI is about to quit (launch) so a tea.Cmd wouldn't complete.
func (p *Plugin) backgroundDescribe(path string) {
	desc, _ := LLMDescribePath(p.llm, path)
	if desc != "" && p.db != nil {
		_ = db.DBUpdatePathDescription(p.db, path, desc)
	}
}

func (p *Plugin) buildNewItems() []list.Item {
	var items []list.Item
	for _, path := range p.paths {
		items = append(items, newItem{
			path:  path,
			label: filepath.Base(path),
		})
	}
	items = append(items, newItem{
		label:    "Browse...",
		isBrowse: true,
	})
	return items
}

func (p *Plugin) dbWriteCmd(fn func(*sql.DB) error) tea.Cmd {
	database := p.db
	return func() tea.Msg {
		if database != nil {
			_ = fn(database)
		}
		return nil
	}
}

// movePathUp swaps the selected path with the one above it.
func (p *Plugin) movePathUp() plugin.Action {
	idx := p.newList.Index()
	// Items: [...paths..., browse] — list index == path index
	if idx <= 0 || idx >= len(p.paths) {
		return plugin.NoopAction()
	}
	p.paths[idx-1], p.paths[idx] = p.paths[idx], p.paths[idx-1]
	p.newList.SetItems(p.buildNewItems())
	p.newList.Select(idx - 1)
	pathA, pathB := p.paths[idx-1], p.paths[idx]
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: p.dbWriteCmd(func(database *sql.DB) error {
		return db.DBSwapPathOrder(database, pathA, pathB)
	})}
}

// movePathDown swaps the selected path with the one below it.
func (p *Plugin) movePathDown() plugin.Action {
	idx := p.newList.Index()
	if idx < 0 || idx >= len(p.paths)-1 {
		return plugin.NoopAction()
	}
	p.paths[idx], p.paths[idx+1] = p.paths[idx+1], p.paths[idx]
	p.newList.SetItems(p.buildNewItems())
	p.newList.Select(idx + 1)
	pathA, pathB := p.paths[idx], p.paths[idx+1]
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: p.dbWriteCmd(func(database *sql.DB) error {
		return db.DBSwapPathOrder(database, pathA, pathB)
	})}
}

// SetPendingLaunchTodo sets a todo whose context will be written before launch.
func (p *Plugin) SetPendingLaunchTodo(todo *db.Todo) {
	p.pendingLaunchTodo = todo
	if todo != nil {
		p.subTab = subTabNew
	}
}

// formatTodoContext builds a markdown context string for a todo.
func formatTodoContext(todo db.Todo) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("## Task: %s\n", todo.Title))
	if todo.Context != "" {
		parts = append(parts, fmt.Sprintf("**Context:** %s", todo.Context))
	}
	if todo.WhoWaiting != "" {
		parts = append(parts, fmt.Sprintf("**Who's waiting:** %s", todo.WhoWaiting))
	}
	if todo.Due != "" {
		parts = append(parts, fmt.Sprintf("**Due:** %s", todo.Due))
	}
	if todo.Effort != "" {
		parts = append(parts, fmt.Sprintf("**Effort:** %s", todo.Effort))
	}
	if todo.Source != "" && todo.Source != "manual" {
		parts = append(parts, fmt.Sprintf("**Source:** %s", todo.Source))
	}
	if todo.Detail != "" {
		parts = append(parts, fmt.Sprintf("\n### Detail\n%s", todo.Detail))
	}
	return strings.Join(parts, "\n")
}

// findClaudeSessionID looks up the most recent Claude session file for a project
// directory. Claude stores sessions under ~/.claude/projects/<encoded-path>/<uuid>.jsonl.
// Returns the Claude session UUID if found, or empty string otherwise.
func findClaudeSessionID(projectDir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Encode the project path the way Claude does: replace "/" with "-".
	encoded := strings.ReplaceAll(filepath.Clean(projectDir), string(filepath.Separator), "-")
	sessDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		return ""
	}

	// Find the most recently modified .jsonl file.
	var bestName string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			bestName = e.Name()
		}
	}
	if bestName == "" {
		return ""
	}
	// Strip .jsonl extension to get the session UUID.
	return strings.TrimSuffix(bestName, ".jsonl")
}
