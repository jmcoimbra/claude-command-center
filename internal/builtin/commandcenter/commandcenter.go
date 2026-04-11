package commandcenter

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	ccStaleThreshold = 2 * time.Second
)

var bookingDurations = []int{15, 30, 60, 120, 240}

type commandTurn struct {
	role string
	text string
}

type undoEntry struct {
	todoID     string
	prevStatus string
	prevDoneAt *time.Time
	cursorPos  int
}

// wizardSelection stores wizard choices for a todo so they persist across open/close cycles.
type wizardSelection struct {
	pathCursor int    // selected path index (-1 = use todo's original project dir)
	mode       string // "normal", "worktree", "sandbox"
}

// ccLoadedMsg is sent when CC data is loaded from DB.
type ccLoadedMsg struct {
	cc  *db.CommandCenter
	err error
}

// dbWriteResult is sent when a DB write completes.
type dbWriteResult struct {
	err error
}

// Plugin implements the plugin.Plugin interface for the Command Center.
type Plugin struct {
	database *sql.DB
	cfg      *config.Config
	bus      plugin.EventBus
	logger   plugin.Logger
	llm         llm.LLM
	notifyPeers func(event string)
	styles      ccStyles
	grad     gradientColors

	// Command center state
	cc             *db.CommandCenter
	ccLastRead     time.Time
	ccLastWrite    time.Time // last DB mutation; suppresses reload during write cooldown
	ccCursor       int
	ccScrollOffset int
	showBacklog    bool
	ccExpanded       bool
	ccExpandedCols   int // 0 = use default (2), 1 = single column, 2 = two columns
	ccExpandedOffset int

	// Input modes
	bookingMode   bool
	bookingCursor int
	textInput     textinput.Model

	// Detail view
	detailView          bool
	detailTodoID        string // ID of the todo being viewed (stable across status changes)
	detailMode          string // "viewing", "editingField", "commandInput"
	detailSelectedField int    // 0=Status, 1=Due, 2=ProjectDir, 3=Prompt
	detailFieldInput    textinput.Model
	commandTextArea     textarea.Model // multi-line wrapping input for detail "c" command
	detailPaths         []string
	detailPathCursor    int
	detailPathFilter    string
	detailStatusCursor  int
	detailNotice        string    // flash notice shown after done/remove
	detailNoticeType    string    // "done" or "removed" — controls notice color
	detailNoticeAt      time.Time // when the notice was set
	detailVP            viewport.Model // scrollable viewport for detail view body
	detailVPReady       bool           // whether viewport has been initialized with dimensions

	// Task runner view (3-step wizard: 1=Project, 2=Mode, 3=Prompt)
	taskRunnerView        bool
	taskRunnerStep        int     // 1=Project, 2=Mode, 3=Prompt
	taskRunnerMode        string  // "normal", "worktree", "sandbox"
	taskRunnerPerm        string  // "default", "plan", "auto"
	taskRunnerBudget      float64
	taskRunnerPrompt      viewport.Model
	taskRunnerPromptText  string // raw text backing taskRunnerPrompt viewport
	taskRunnerRefining      bool   // true when AI refine is active
	taskRunnerReviewing     bool   // true when Plannotator is open in browser
	taskRunnerInputting     bool   // true when user is typing instructions for c key
	taskRunnerInstructInput textarea.Model
	taskRunnerReviewClean   string // clean prompt text before review edits
	taskRunnerPathCursor   int    // index into detailPaths for task runner project override
	taskRunnerLaunchCursor int    // 0=Run Claude, 1=Queue Agent, 2=Run Agent Now
	taskRunnerPickingPath  bool   // true when scrollable path picker is open
	taskRunnerPathFilter   string // type-to-filter string for path picker

	// Help overlay
	showHelp bool

	// Pending launch from todo
	pendingLaunchTodo *db.Todo

	// Rich todo creation
	addingTodoRich      bool
	todoTextArea        textarea.Model
	commandConversation []commandTurn

	// Quick todo entry (t key)
	addingTodoQuick    bool
	quickTodoTextArea  textarea.Model

	// Background claude processing
	claudeLoading     bool
	claudeLoadingMsg  string
	claudeLoadingTodo string
	claudeLoadingAt   time.Time

	// Background CC refresh
	ccRefreshing           bool
	ccLastRefreshTriggered time.Time
	lastRefreshAt          time.Time
	lastRefreshError       string

	// Undo stack
	undoStack []undoEntry

	// Flash message
	flashMessage   string
	flashMessageAt time.Time

	// Agent sessions — daemon is the sole agent manager
	daemonClientFunc func() *daemon.Client // nil-safe getter; set after Init by TUI host

	// Session viewer
	sessionViewerActive       bool
	sessionViewerTodoID       string
	sessionViewerVP           viewport.Model
	sessionViewerAutoScroll   bool
	sessionViewerDone         bool           // true when session has ended
	sessionViewerListening    bool           // true when listenForDaemonAgentEvents polling cmd is active
	sessionViewerInputting    bool           // true when textarea input is active
	sessionViewerInput        textarea.Model // textarea for sending messages to agent
	sessionViewerReplayEvents []sessionEvent // events loaded from disk for post-session replay

	// Wizard selections per-todo (persisted across open/close cycles)
	wizardSelections map[string]wizardSelection

	// Triage filter for expanded view tabs
	triageFilter string

	// Search filter
	searchActive bool
	searchInput  textinput.Model

	// Key chord state: "g" prefix for Gmail-style shortcuts (e.g., "gi" = go inbox)
	gPending bool

	// Star / focus / schedule offer modes
	scheduleOfferMode     bool   // after starring: intercepts next keypress (S=schedule, other=skip)
	unstarConfirmMode     bool   // after unstarring with future bookings: intercepts y/n
	unstarConfirmTodoID   string // which todo is pending unstar confirmation
	unstarConfirmAlsoUnfocus bool // true when confirm was triggered from f-key (should also clear focus)

	// Merge source cursor for unmerge UX in detail view
	mergeSourceCursor int

	// Sub-view identifier (currently only "command")
	subView string

	// Dimensions
	width, height int
	frame         int

	// Spinner
	spinner spinner.Model
}

// New creates a new commandcenter Plugin.
func New() *Plugin {
	return &Plugin{
		subView:          "command",
		triageFilter:     "focus",
		wizardSelections: make(map[string]wizardSelection),
	}
}

// StartCmds returns initial tea.Cmds (e.g., spinner tick) the host should run.
func (p *Plugin) StartCmds() tea.Cmd {
	if p.ccRefreshing && p.cfg.RefreshEnabled() {
		return tea.Batch(p.spinner.Tick, refreshCCCmd())
	}
	return p.spinner.Tick
}

// Slug returns the plugin slug.
func (p *Plugin) Slug() string { return "commandcenter" }

// TabName returns the primary tab name.
func (p *Plugin) TabName() string { return "Command Center" }

// Migrations returns DB migrations for the command center plugin.
func (p *Plugin) Migrations() []plugin.Migration {
	return []plugin.Migration{
		{
			Version: 1,
			SQL: `CREATE INDEX IF NOT EXISTS idx_cc_todos_status_sort ON cc_todos(status, sort_order);`,
		},
		{
			Version: 2,
			SQL:     `ALTER TABLE cc_todos ADD COLUMN session_log_path TEXT;`,
		},
	}
}

// Routes returns the sub-routes for this plugin.
func (p *Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Slug: "commandcenter", Description: "Command Center (calendar + todos)"},
	}
}

// NavigateTo switches to the given sub-route.
func (p *Plugin) NavigateTo(route string, args map[string]string) {
	p.subView = "command"
}

// RefreshInterval returns the configured interval between background refreshes.
func (p *Plugin) RefreshInterval() time.Duration {
	return ccRefreshInterval
}

// ccReloadInterval is separate from refresh — it's how often the plugin re-reads from DB.


// Refresh returns a command that triggers a CC refresh.
func (p *Plugin) Refresh() tea.Cmd {
	if !p.ccRefreshing && p.cfg.RefreshEnabled() {
		p.ccRefreshing = true
		p.ccLastRefreshTriggered = time.Now()
		return refreshCCCmd()
	}
	return nil
}

// KeyBindings returns the key bindings for the current sub-view.
func (p *Plugin) KeyBindings() []plugin.KeyBinding {
	return []plugin.KeyBinding{
		{Key: "up/k", Description: "Navigate todos", Promoted: true},
		{Key: "down/j", Description: "Navigate todos", Promoted: true},
		{Key: "shift+up/down", Description: "Move todo up/down", Promoted: true},
		{Key: "enter", Description: "View todo detail", Promoted: true},
		{Key: "space", Description: "Cycle expanded view", Promoted: true},
		{Key: "o", Description: "Launch Claude session", Promoted: true},
		{Key: "x", Description: "Mark todo done", Promoted: true},
		{Key: "u", Description: "Undo last action", Promoted: true},
		{Key: "c", Description: "Command — tell Claude what to do", Promoted: true},
		{Key: "t", Description: "Quick add todos", Promoted: true},
		{Key: "X", Description: "Dismiss todo"},
		{Key: "d", Description: "Defer todo"},
		{Key: "p", Description: "Promote todo to top"},
		{Key: "s", Description: "Star / unstar todo"},
		{Key: "S", Description: "Schedule time block"},
		{Key: "f", Description: "Toggle focus"},
		{Key: "/", Description: "Search/filter todos"},
		{Key: "y", Description: "Accept todo (triage)"},
		{Key: "tab", Description: "Cycle triage filter (expanded)"},
		{Key: "b", Description: "Toggle completed backlog"},
		{Key: "r", Description: "Refresh from all sources"},
		{Key: "gi/gu", Description: "Go to inbox (list view)"},
	}
}

// Init initializes the plugin with the given context.
func (p *Plugin) Init(ctx plugin.Context) error {
	p.database = ctx.DB
	p.cfg = ctx.Config
	p.bus = ctx.Bus
	p.logger = ctx.Logger
	p.notifyPeers = ctx.NotifyPeers
	if ctx.LLM != nil {
		p.llm = llm.NewObservableLLM(ctx.LLM, func(topic string, payload llm.EventPayload) {
			if p.bus != nil {
				p.bus.Publish(plugin.Event{
					Source:  "commandcenter",
					Topic:   topic,
					Payload: payload,
				})
			}
		}, "commandcenter")
	} else {
		p.llm = llm.NoopLLM{}
	}

	// Set refresh interval from config
	ccRefreshInterval = ctx.Config.ParseRefreshInterval()

	if ctx.Styles != nil {
		p.styles = *ctx.Styles
	} else {
		pal := config.GetPalette(p.cfg.Palette, p.cfg.Colors)
		p.styles = ui.NewStyles(pal)
	}
	if ctx.Grad != nil {
		p.grad = *ctx.Grad
	} else {
		pal := config.GetPalette(p.cfg.Palette, p.cfg.Colors)
		p.grad = ui.NewGradientColors(pal)
	}

	// Set up text input
	ti := textinput.New()
	ti.Placeholder = "Enter title..."
	ti.CharLimit = 0
	p.textInput = ti

	// Set up detail field input
	dfi := textinput.New()
	dfi.Placeholder = ""
	dfi.CharLimit = 120
	p.detailFieldInput = dfi
	p.detailMode = "viewing"

	// Load paths for project dir picker
	if ctx.DB != nil {
		paths, err := db.DBLoadPaths(ctx.DB)
		if err == nil {
			p.detailPaths = paths
		}
	}

	// Set up textarea
	ta := textarea.New()
	ta.Placeholder = "Tell " + p.cfg.Name + " what to do -- add todos, resolve conflicts, ask questions (ctrl+d submit, esc cancel)"
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(5)
	ta.FocusedStyle.Base = ta.FocusedStyle.Base.Foreground(p.styles.ColorWhite)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(p.styles.ColorMuted)
	p.todoTextArea = ta

	// Set up quick todo textarea
	qta := textarea.New()
	qta.Placeholder = "What do you need to do? (ctrl+d submit, esc cancel)"
	qta.CharLimit = 0
	qta.SetWidth(80)
	qta.SetHeight(5)
	qta.FocusedStyle.Base = qta.FocusedStyle.Base.Foreground(p.styles.ColorWhite)
	qta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	qta.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	qta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(p.styles.ColorMuted)
	p.quickTodoTextArea = qta

	// Set up command textarea for detail view (wrapping, Enter to submit)
	cta := textarea.New()
	cta.Placeholder = "Tell me what changed..."
	cta.CharLimit = 0
	cta.ShowLineNumbers = false
	cta.SetWidth(80)
	cta.SetHeight(3)
	cta.FocusedStyle.Base = cta.FocusedStyle.Base.Foreground(p.styles.ColorWhite)
	cta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	cta.FocusedStyle.CursorLine = lipgloss.NewStyle().Foreground(p.styles.ColorWhite)
	cta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(p.styles.ColorMuted)
	p.commandTextArea = cta

	// Set up search input
	si := textinput.New()
	si.Placeholder = "Search todos..."
	si.CharLimit = 80
	p.searchInput = si

	// Set up spinner
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = lipgloss.NewStyle().Foreground(p.styles.ColorCyan)
	p.spinner = s

	// Subscribe to events
	if p.bus != nil {
		p.bus.Subscribe("pending.todo.cancel", func(e plugin.Event) {
			p.pendingLaunchTodo = nil
		})
		p.bus.Subscribe("config.saved", func(e plugin.Event) {
			// Re-read config on save (palette, refresh interval, etc.)
			if p.logger != nil {
				p.logger.Info("commandcenter", "config.saved event received")
			}
		})
	}

	// Load CC from DB
	if p.database != nil {
		cc, err := db.LoadCommandCenterFromDB(p.database)
		if err != nil {
			if p.logger != nil {
				p.logger.Warn("commandcenter", "failed to load CC from DB", "err", err)
			}
		}
		if cc != nil {
			p.cc = cc
			// Auto-refresh if data is stale (e.g., after machine sleep)
			if time.Since(cc.GeneratedAt) > ccRefreshInterval {
				p.ccRefreshing = true
				p.ccLastRefreshTriggered = time.Now()
			}
		}
		p.ccLastRead = time.Now()
	}

	return nil
}

// Shutdown is called when the plugin is being shut down.
// Agent lifecycle is managed by the daemon — no local cleanup needed.
func (p *Plugin) Shutdown() {
}

// dbWriteCmd creates a tea.Cmd that performs a DB write.
// Sets ccLastWrite to suppress reloads that would race with in-flight writes.
func (p *Plugin) dbWriteCmd(fn func(*sql.DB) error) tea.Cmd {
	if p.database == nil {
		return nil
	}
	p.ccLastWrite = time.Now()
	database := p.database
	return func() tea.Msg {
		return dbWriteResult{err: fn(database)}
	}
}

// notifyPeersCmd returns a tea.Cmd that sends an event to all other running
// TUI instances via the notify socket system. Returns nil if no notify function
// is configured (e.g. in tests).
func (p *Plugin) notifyPeersCmd(event string) tea.Cmd {
	if p.notifyPeers == nil {
		return nil
	}
	notify := p.notifyPeers
	return func() tea.Msg {
		notify(event)
		return nil
	}
}

// loadCCFromDBCmd creates a tea.Cmd that loads CC data from the DB.
func (p *Plugin) loadCCFromDBCmd() tea.Cmd {
	database := p.database
	if database == nil {
		return nil
	}
	return func() tea.Msg {
		cc, err := db.LoadCommandCenterFromDB(database)
		return ccLoadedMsg{cc: cc, err: err}
	}
}

func ensureCC(cc **db.CommandCenter) {
	if *cc == nil {
		*cc = &db.CommandCenter{GeneratedAt: time.Now()}
	}
}

func (p *Plugin) normalMaxVisibleTodos() int {
	// Must match the maxVisibleTodos calculation in renderCommandCenterView.
	// viewCommandTab passes height = p.height - 14 to the render function.
	// renderCommandCenterView computes usedHeight from warnings + suggestions + base chrome,
	// then panelHeight = height - usedHeight, maxVisibleTodos = (panelHeight - 3) / 2, min 5.
	viewHeight := p.height - 14
	if viewHeight < 10 {
		viewHeight = 10
	}
	// Base usedHeight: 2 (top) + 2 (bottom) = 4.
	usedHeight := 4
	// Account for suggestion banner if present (header + body + border = ~4 lines, plus 1 gap).
	if p.cc != nil && p.cc.Suggestions.Focus != "" {
		usedHeight += 5
	}
	// Account for warning banner if present (~2 lines per warning + border + gap).
	if p.cc != nil && len(p.cc.Warnings) > 0 {
		usedHeight += len(p.cc.Warnings) + 3
	}
	panelHeight := viewHeight - usedHeight
	if panelHeight < 10 {
		panelHeight = 10
	}
	max := (panelHeight - 3) / 2
	if max < 5 {
		max = 5
	}
	return max
}

// textareaWidth returns the appropriate width for textareas based on the current terminal width.
// The detail view renders textareas inside a PanelBorder (2 chars for border) with a 2-char
// left padding prefix ("  "), so we subtract 8 from viewWidth to avoid overflow.
func (p *Plugin) textareaWidth() int {
	viewWidth := ui.ContentMaxWidth
	if p.width > 0 && p.width < viewWidth {
		viewWidth = p.width
	}
	// viewWidth - 4 = innerWidth (renderDetailView), minus 2 for "  " prefix = available space
	// The PanelBorder.Width(innerWidth) sets the content area, but the textarea must
	// also leave room for the 2-char left padding in the command section.
	w := viewWidth - 8
	if w < 40 {
		w = 40
	}
	return w
}

func (p *Plugin) expandedRowsPerCol() int {
	// p.height is the raw height from the TUI host.
	// viewCommandTab subtracts 14 for TUI-level chrome (logo, nav tabs, etc.)
	// so viewHeight = p.height - 14.
	// The expanded view adds its own chrome:
	// header(1) + tabBar(1) + blank(1) + columns + blank(1) + hints(1) + footer(1) = 6 lines.
	// Each todo item takes 2 lines (title + details).
	viewHeight := p.height - 14
	if viewHeight < 10 {
		viewHeight = 10
	}
	rows := (viewHeight - 6) / 2
	if rows < 3 {
		rows = 3
	}
	return rows
}

// clampExpandedOffset ensures ccExpandedOffset stays valid after list size changes.
func (p *Plugin) clampExpandedOffset() {
	total := len(p.filteredTodos())
	pageSize := p.expandedRowsPerCol() * p.expandedNumCols()
	if pageSize <= 0 {
		p.ccExpandedOffset = 0
		return
	}
	maxOffset := ((total - 1) / pageSize) * pageSize
	if maxOffset < 0 {
		maxOffset = 0
	}
	if p.ccExpandedOffset > maxOffset {
		p.ccExpandedOffset = maxOffset
	}
}

func (p *Plugin) expandedNumCols() int {
	if p.ccExpandedCols == 1 {
		return 1
	}
	return 2
}

// filteredTodos returns the subset of todos based on the current view mode, triage filter, and search query.
// When showBacklog is true, returns completed/dismissed items instead of active ones.
func (p *Plugin) filteredTodos() []db.Todo {
	if p.cc == nil {
		return nil
	}

	// Backlog mode: return completed/dismissed items (with search filter).
	if p.showBacklog {
		result := p.cc.CompletedTodos()
		query := strings.TrimSpace(p.searchInput.Value())
		if query == "" {
			return result
		}
		lower := strings.ToLower(query)
		var filtered []db.Todo
		for _, t := range result {
			titleMatch := strings.Contains(strings.ToLower(flattenTitle(t.Title)), lower)
			idMatch := query == fmt.Sprintf("%d", t.DisplayID)
			if titleMatch || idMatch {
				filtered = append(filtered, t)
			}
		}
		return filtered
	}

	allActive := p.cc.ActiveTodos()

	var result []db.Todo
	if !p.ccExpanded {
		// Collapsed view: show only starred items.
		for _, t := range allActive {
			if t.Starred {
				result = append(result, t)
			}
		}
	} else {
		// Expanded view: filter based on triageFilter
		switch p.triageFilter {
		case "focus":
			for _, t := range allActive {
				if t.Focus {
					result = append(result, t)
				}
			}
		case "todo":
			for _, t := range allActive {
				if t.Status == db.StatusBacklog {
					result = append(result, t)
				}
			}
		case "inbox":
			for _, t := range allActive {
				if t.Status == db.StatusNew {
					result = append(result, t)
				}
			}
		case "agents":
			for _, t := range allActive {
				if t.Status == db.StatusEnqueued || t.Status == db.StatusRunning || t.Status == db.StatusBlocked {
					result = append(result, t)
				}
			}
		case "review":
			for _, t := range allActive {
				if t.Status == db.StatusReview || t.Status == db.StatusFailed {
					result = append(result, t)
				}
			}
		default:
			result = allActive
		}
	}

	// Sort starred items to the top within any view.
	sortStarredFirst(result)

	// Apply search filter on top of triage filter
	query := strings.TrimSpace(p.searchInput.Value())
	if query == "" {
		return result
	}
	lower := strings.ToLower(query)
	var filtered []db.Todo
	for _, t := range result {
		titleMatch := strings.Contains(strings.ToLower(flattenTitle(t.Title)), lower)
		idMatch := query == fmt.Sprintf("%d", t.DisplayID)
		if titleMatch || idMatch {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// triageCounts returns the count of todos matching each filter category.
func (p *Plugin) triageCounts() map[string]int {
	counts := map[string]int{
		"focus":  0,
		"todo":   0,
		"inbox":  0,
		"agents": 0,
		"review": 0,
		"all":    0,
	}
	if p.cc == nil {
		return counts
	}
	for _, t := range p.cc.ActiveTodos() {
		counts["all"]++
		if t.Focus {
			counts["focus"]++
		}
		switch t.Status {
		case db.StatusBacklog:
			counts["todo"]++
		case db.StatusNew:
			counts["inbox"]++
		case db.StatusEnqueued, db.StatusRunning, db.StatusBlocked:
			counts["agents"]++
		case db.StatusReview, db.StatusFailed:
			counts["review"]++
		}
	}
	return counts
}

// sortStarredFirst sorts todos in-place so starred items appear before non-starred items,
// preserving relative order within each group.
func sortStarredFirst(todos []db.Todo) {
	if len(todos) == 0 {
		return
	}
	// Stable partition: starred first, then non-starred.
	starred := todos[:0:len(todos)]
	var rest []db.Todo
	for _, t := range todos {
		if t.Starred {
			starred = append(starred, t)
		} else {
			rest = append(rest, t)
		}
	}
	copy(todos, append(starred, rest...))
}

func (p *Plugin) triggerFocusRefresh() tea.Cmd {
	if p.cc == nil {
		return nil
	}
	p.claudeLoading = true
	p.claudeLoadingAt = time.Now()
	if len(p.cc.ActiveTodos()) == 0 {
		p.claudeLoadingMsg = "Admiring the empty list..."
		return claudeFocusCmd(p.llm, buildEmptyFocusPrompt(p.cc))
	}
	p.claudeLoadingMsg = "Updating focus..."
	return claudeFocusCmd(p.llm, buildFocusPrompt(p.cc))
}

// PendingLaunchTodo returns and clears the pending launch todo, if any.
func (p *Plugin) PendingLaunchTodo() *db.Todo {
	t := p.pendingLaunchTodo
	p.pendingLaunchTodo = nil
	return t
}

// SetPendingLaunchTodo sets a pending launch todo (used when navigating back from sessions).
func (p *Plugin) SetPendingLaunchTodo(todo *db.Todo) {
	p.pendingLaunchTodo = todo
}

// View renders the plugin's current view.
func (p *Plugin) View(width, height, frame int) string {
	p.width = width
	p.height = height
	p.frame = frame

	if p.showHelp {
		helpView := p.subView
		if p.detailView {
			helpView = "detail"
		}
		helpWidth := ui.ContentMaxWidth
		if width > 0 && width < helpWidth {
			helpWidth = width
		}
		return renderHelpOverlay(&p.styles, helpView, helpWidth, height)
	}

	return p.viewCommandTab(width, height)
}

func (p *Plugin) viewCommandTab(width, height int) string {
	viewWidth := ui.ContentMaxWidth
	if width > 0 && width < viewWidth {
		viewWidth = width
	}
	viewHeight := height - 14
	if viewHeight < 10 {
		viewHeight = 10
	}

	if p.sessionViewerActive && p.detailView {
		return p.renderSessionViewer(width, height)
	}

	if p.taskRunnerView && p.detailView && p.cc != nil {
		if todo := p.detailTodo(); todo != nil {
			// Determine the effective project dir for display
		taskRunnerProjectDir := todo.ProjectDir
		if p.taskRunnerPathCursor >= 0 && p.taskRunnerPathCursor < len(p.detailPaths) {
			taskRunnerProjectDir = p.detailPaths[p.taskRunnerPathCursor]
		}
		// Resize viewport to match current available space on every render.
		// Step 3 has ~13 lines of chrome (header, labels, divider, selector, etc.).
		vpWidth := viewWidth - 10
		if vpWidth < 40 {
			vpWidth = 40
		}
		vpHeight := viewHeight - 13
		if vpHeight < 5 {
			vpHeight = 5
		}
		if p.taskRunnerPrompt.Width != vpWidth || p.taskRunnerPrompt.Height != vpHeight {
			// When dimensions change, re-set both the size and content.
			// The charmbracelet viewport needs SetContent re-called
			// after dimension changes for the visible line window to
			// update correctly. Without this, only the first line
			// renders until an editor round-trip triggers SetContent.
			yOff := p.taskRunnerPrompt.YOffset
			p.taskRunnerPrompt.Width = vpWidth
			p.taskRunnerPrompt.Height = vpHeight
			p.taskRunnerPrompt.SetContent(wrapText(p.taskRunnerPromptText, vpWidth))
			p.taskRunnerPrompt.YOffset = yOff
		}
		return renderTaskRunner(&p.styles, *todo, p.taskRunnerMode, p.taskRunnerBudget, p.taskRunnerStep, p.taskRunnerPrompt, viewWidth, viewHeight, taskRunnerProjectDir, p.taskRunnerLaunchCursor, p.taskRunnerPickingPath, p.taskRunnerFilteredPaths(), p.taskRunnerPathCursor, p.taskRunnerPathFilter, p.taskRunnerRefining, p.taskRunnerReviewing, p.taskRunnerInputting, p.taskRunnerInstructInput)
		}
	}

	if p.detailView && p.cc != nil {
		if todo := p.detailTodo(); todo != nil {
			return p.renderDetailViewScrollable(viewWidth, height)
		}
		// Notice showing but no more active todos — render just the notice
		if p.detailNotice != "" {
			bgColor := p.styles.ColorGreen
			icon := "\u2713"
			if p.detailNoticeType == "removed" {
				bgColor = p.styles.ColorYellow
				icon = "\u2717"
			}
			notice := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(bgColor).
				Bold(true).
				Padding(0, 1).
				Render(icon + " " + p.detailNotice)
			empty := p.styles.DescMuted.Render("No more active todos")
			content := lipgloss.JoinVertical(lipgloss.Left, "", "  "+notice, "", "  "+empty)
			return p.styles.PanelBorder.Width(viewWidth - 4).Render(content)
		}
	}

	if p.ccExpanded && p.cc != nil {
		filtered := p.filteredTodos()
		counts := p.triageCounts()
		view := renderExpandedTodoView(&p.styles, &p.grad, filtered, p.ccCursor, p.ccExpandedOffset, p.expandedRowsPerCol(), p.expandedNumCols(), viewWidth, viewHeight, p.frame, p.claudeLoadingTodo, p.ccRefreshing, p.triageFilter, counts)
		if p.claudeLoading {
			elapsed := time.Since(p.claudeLoadingAt).Truncate(time.Second)
		loadingLine := "  " + p.spinner.View() + " " + p.claudeLoadingMsg + fmt.Sprintf(" (%s)", elapsed)
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", loadingLine)
		}
		if p.flashMessage != "" {
			flash := lipgloss.NewStyle().Foreground(p.styles.ColorGreen).Render("  > " + p.flashMessage)
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", flash)
		}
		if p.addingTodoQuick {
			inputLine := p.styles.SectionHeader.Render("QUICK TODO (ctrl+d submit, esc cancel):") + "\n" + p.quickTodoTextArea.View()
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", inputLine)
		}
		if p.addingTodoRich {
			inputLine := p.styles.SectionHeader.Render("COMMAND (ctrl+d submit, esc cancel):") + "\n" + p.todoTextArea.View()
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", inputLine)
		}
		if p.searchActive {
			searchLine := p.styles.SectionHeader.Render("/") + " " + p.searchInput.View() + "  " + p.styles.Hint.Render("enter keep filter \u00b7 esc clear")
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", searchLine)
		} else if strings.TrimSpace(p.searchInput.Value()) != "" {
			filterLabel := lipgloss.NewStyle().Foreground(p.styles.ColorCyan).Bold(true).Render("filter: " + p.searchInput.Value())
			filterHint := p.styles.Hint.Render("  / to edit \u00b7 esc to clear")
			view = lipgloss.JoinVertical(lipgloss.Left, view, "", "  "+filterLabel+filterHint)
		}
		return view
	}

	view := renderCommandCenterView(&p.styles, &p.grad, p.cc, p.cfg.Calendar.Calendars, p.cfg.Calendar.Enabled, viewWidth, viewHeight, p.ccCursor, p.ccScrollOffset, p.frame, p.claudeLoadingTodo, p.showBacklog, p.ccRefreshing, p.lastRefreshError, p.filteredTodos(), p.triageCounts(), p.cfg.Agent.MaxConcurrent)

	if p.claudeLoading {
		elapsed := time.Since(p.claudeLoadingAt).Truncate(time.Second)
		loadingLine := "  " + p.spinner.View() + " " + p.claudeLoadingMsg + fmt.Sprintf(" (%s)", elapsed)
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", loadingLine)
	}
	if p.flashMessage != "" {
		flash := lipgloss.NewStyle().Foreground(p.styles.ColorGreen).Render("  > " + p.flashMessage)
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", flash)
	}
	if p.addingTodoQuick {
		inputLine := p.styles.SectionHeader.Render("QUICK TODO (ctrl+d submit, esc cancel):") + "\n" + p.quickTodoTextArea.View()
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", inputLine)
	}
	if p.addingTodoRich {
		inputLine := p.styles.SectionHeader.Render("COMMAND (ctrl+d submit, esc cancel):") + "\n" + p.todoTextArea.View()
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", inputLine)
	}
	if p.bookingMode {
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", p.renderBookingPicker())
	}
	if p.searchActive {
		searchLine := p.styles.SectionHeader.Render("/") + " " + p.searchInput.View() + "  " + p.styles.Hint.Render("enter keep filter \u00b7 esc clear")
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", searchLine)
	} else if strings.TrimSpace(p.searchInput.Value()) != "" {
		filterLabel := lipgloss.NewStyle().Foreground(p.styles.ColorCyan).Bold(true).Render("filter: " + p.searchInput.Value())
		filterHint := p.styles.Hint.Render("  / to edit \u00b7 esc to clear")
		view = lipgloss.JoinVertical(lipgloss.Left, view, "", "  "+filterLabel+filterHint)
	}

	return view
}

func (p *Plugin) renderBookingPicker() string {
	labels := []string{"15m", "30m", "1h", "2h", "4h"}
	var parts []string
	for i, label := range labels {
		if i == p.bookingCursor {
			parts = append(parts, p.styles.ActiveTab.Render("> "+label))
		} else {
			parts = append(parts, p.styles.InactiveTab.Render(label))
		}
	}
	picker := strings.Join(parts, "  ")
	return p.styles.SectionHeader.Render("Book time: ") + picker + p.styles.Hint.Render("  (<-> select, enter confirm, esc cancel)")
}

// publishEvent is a helper for publishing events to the bus.
func (p *Plugin) publishEvent(topic string, payload map[string]interface{}) {
	if p.bus != nil {
		p.bus.Publish(plugin.Event{
			Source:  "commandcenter",
			Topic:   topic,
			Payload: payload,
		})
	}
}

// detailTodo returns the todo currently shown in the detail view, looked up by ID.
// Returns nil if the todo is not found (e.g. deleted).
func (p *Plugin) detailTodo() *db.Todo {
	if p.cc == nil || p.detailTodoID == "" {
		return nil
	}
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == p.detailTodoID {
			return &p.cc.Todos[i]
		}
	}
	return nil
}

// syncCursorToDetailTodo updates ccCursor to match the position of the current
// detail todo in filteredTodos(). This ensures that after completing/dismissing
// a todo from detail view (where j/k navigation updates detailTodoID but not
// ccCursor), the auto-advance logic in handleTickMsg uses the correct position.
func (p *Plugin) syncCursorToDetailTodo() {
	if p.cc == nil || p.detailTodoID == "" {
		return
	}
	for i, t := range p.filteredTodos() {
		if t.ID == p.detailTodoID {
			p.ccCursor = i
			return
		}
	}
}

// detailTodoActiveIndex returns the index of the detail todo within ActiveTodos(), or -1.
func (p *Plugin) detailTodoActiveIndex() int {
	if p.cc == nil || p.detailTodoID == "" {
		return -1
	}
	for i, t := range p.cc.ActiveTodos() {
		if t.ID == p.detailTodoID {
			return i
		}
	}
	return -1
}

// SetDaemonClientFunc wires the daemon client getter so the command center
// can route agent operations through the daemon when connected.
func (p *Plugin) SetDaemonClientFunc(fn func() *daemon.Client) {
	p.daemonClientFunc = fn
}

// daemonClient returns the daemon RPC client, or nil if not connected.
func (p *Plugin) daemonClient() *daemon.Client {
	if p.daemonClientFunc == nil {
		return nil
	}
	return p.daemonClientFunc()
}

// SubView returns the current sub-view name.
func (p *Plugin) SubView() string {
	return p.subView
}

// SetSubView sets the current sub-view.
func (p *Plugin) SetSubView(v string) {
	p.subView = v
}
