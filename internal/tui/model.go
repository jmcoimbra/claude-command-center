package tui

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/agent"
	"github.com/anutron/claude-command-center/internal/builtin/commandcenter"
	"github.com/anutron/claude-command-center/internal/builtin/prs"
	"github.com/anutron/claude-command-center/internal/builtin/sessions"
	"github.com/anutron/claude-command-center/internal/builtin/settings"
	"github.com/anutron/claude-command-center/internal/config"
	"github.com/anutron/claude-command-center/internal/daemon"
	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/external"
	"github.com/anutron/claude-command-center/internal/llm"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/anutron/claude-command-center/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// quitTimeoutMsg is sent after the double-escape timeout expires.
type quitTimeoutMsg struct{}

// budgetStatusMsg carries a fresh budget status from the daemon.
type budgetStatusMsg struct {
	status daemon.BudgetStatusResult
	err    error
}

const quitTimeout = 2 * time.Second

// budgetPollInterval controls how often the TUI polls budget status from the daemon.
const budgetPollInterval = 5 * time.Second

// Verify built-in plugins implement Starter at compile time.
var _ plugin.Starter = (*sessions.Plugin)(nil)
var _ plugin.Starter = (*commandcenter.Plugin)(nil)

type tab int

const (
	tabCommand tab = iota // "Command Center" tab
	tabNew                // "Sessions" consolidated tab
)

type tabEntry struct {
	label     string
	plugin    plugin.Plugin
	route     string
	ownerSlug string // plugin slug that owns this tab, for filtering
}

// Model is the main Bubbletea model — a thin host that dispatches to plugins.
type Model struct {
	cfg    *config.Config
	styles *Styles
	grad   *GradientColors

	tabs      []tabEntry
	activeTab tab
	width     int
	height    int
	frame     int

	Launch *LaunchAction

	// allTabs is the full unfiltered tab list; tabs is the current visible subset.
	allTabs []tabEntry
	// allPlugins holds every unique plugin for lifecycle management.
	allPlugins []plugin.Plugin

	// pendingQuit tracks double-escape-to-quit state.
	pendingQuit   bool
	pendingQuitAt time.Time

	// returnedFromLaunch is set when the TUI restarts after a Claude session.
	returnedFromLaunch bool
	// returnTodoID is the todo ID to return to after a Claude session.
	returnTodoID string
	// returnWasResumeJoin is true if the session was a join/resume.
	returnWasResumeJoin bool

	// Onboarding flow state.
	onboarding      bool
	onboardingState *onboardingState

	// flashMessage is a temporary status message shown below the tab bar.
	flashMessage   string
	flashExpiresAt time.Time

	db *sql.DB

	// Daemon connection for session registry and event subscription.
	daemonConn *DaemonConn
	bus          plugin.EventBus

	// Budget widget state — polled from daemon.
	budgetStatus     daemon.BudgetStatusResult
	budgetLastPoll   time.Time
	budgetAvailable  bool // true once we've received at least one successful poll

	// Agent console overlay state.
	console consoleOverlay
}

// NewModel creates the main TUI model with plugins.
// bus and logger are owned by main.go and shared across all plugins.
// Optional extPlugins are appended as additional tabs.
func NewModel(database *sql.DB, cfg *config.Config, bus plugin.EventBus, logger plugin.Logger, l llm.LLM, extPlugins ...plugin.Plugin) Model {
	pal := config.GetPalette(cfg.Palette, cfg.Colors)
	styles := &Styles{}
	*styles = NewStyles(pal)
	grad := &GradientColors{}
	*grad = NewGradientColors(pal)

	// Create a single shared agent runner for all plugins.
	maxConcurrent := cfg.Agent.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	runner := agent.NewRunner(maxConcurrent)

	sessPlug := &sessions.Plugin{}
	ccPlug := commandcenter.New()
	prsPlug := &prs.Plugin{}

	// Build registry with all plugins.
	registry := plugin.NewRegistry()
	registry.Register(ccPlug)
	registry.Register(sessPlug)
	registry.Register(prsPlug)
	for _, ep := range extPlugins {
		registry.Register(ep)
	}

	settingsPlug := settings.New(registry)
	registry.Register(settingsPlug)

	ctx := plugin.Context{
		DB:          database,
		Config:      cfg,
		Styles:      styles,
		Grad:        grad,
		Bus:         bus,
		Logger:      logger,
		DBPath:      config.DBPath(),
		LLM:         l,
		AgentRunner: runner,
		NotifyPeers: func(event string) { _ = SendNotify(event) },
	}

	_ = sessPlug.Init(ctx)
	_ = ccPlug.Init(ctx)
	_ = prsPlug.Init(ctx)
	_ = settingsPlug.Init(ctx)

	// Build the full tab list (allTabs); rebuildTabs filters to visible.
	var allTabs []tabEntry
	allTabs = append(allTabs,
		tabEntry{label: "Command Center", plugin: ccPlug, route: "commandcenter", ownerSlug: "commandcenter"},
		tabEntry{label: "Sessions", plugin: sessPlug, route: "sessions", ownerSlug: "sessions"},
		tabEntry{label: "PRs", plugin: prsPlug, route: "waiting", ownerSlug: "prs"},
	)
	// Track which external plugins were loaded (started at boot).
	loadedExtSlugs := map[string]bool{}
	for _, ep := range extPlugins {
		loadedExtSlugs[ep.Slug()] = true
		routes := ep.Routes()
		if len(routes) > 0 {
			for _, r := range routes {
				allTabs = append(allTabs, tabEntry{label: r.Description, plugin: ep, route: r.Slug, ownerSlug: ep.Slug()})
			}
		} else {
			allTabs = append(allTabs, tabEntry{label: ep.TabName(), plugin: ep, route: ep.Slug(), ownerSlug: ep.Slug()})
		}
	}
	// Add stub tab entries for external plugins that are in config but were
	// not loaded at startup (disabled). This allows rebuildTabs to show a
	// placeholder tab if the user enables them at runtime without restarting.
	for _, entry := range cfg.ExternalPlugins {
		if loadedExtSlugs[entry.Name] {
			continue
		}
		stub := newStubPlugin(entry.Name, entry.Name)
		allTabs = append(allTabs, tabEntry{label: entry.Name, plugin: stub, route: entry.Name, ownerSlug: entry.Name})
	}
	allTabs = append(allTabs, tabEntry{label: "Settings", plugin: settingsPlug, route: "settings", ownerSlug: "settings"})

	// Collect all unique plugins for shutdown.
	allPlugins := []plugin.Plugin{sessPlug, ccPlug, prsPlug, settingsPlug}
	allPlugins = append(allPlugins, extPlugins...)

	m := Model{
		cfg:          cfg,
		styles:       styles,
		grad:         grad,
		allTabs:      allTabs,
		activeTab:    0,
		allPlugins:   allPlugins,
		db:  database,
		bus: bus,
	}
	m.rebuildTabs()
	return m
}

// rebuildTabs filters allTabs to only enabled plugins, preserving the active tab if possible.
func (m *Model) rebuildTabs() {
	currentRoute := ""
	if int(m.activeTab) < len(m.tabs) {
		currentRoute = m.tabs[m.activeTab].route
	}

	var filtered []tabEntry
	for _, t := range m.allTabs {
		if !m.cfg.PluginEnabled(t.ownerSlug) {
			continue
		}
		filtered = append(filtered, t)
	}
	m.tabs = filtered

	// Try to stay on the same route
	if currentRoute != "" {
		for i, t := range m.tabs {
			if t.route == currentRoute {
				m.activeTab = tab(i)
				return
			}
		}
	}
	// Fallback: clamp to valid range
	if int(m.activeTab) >= len(m.tabs) {
		m.activeTab = tab(len(m.tabs) - 1)
		if m.activeTab < 0 {
			m.activeTab = 0
		}
	}
}

// findTabByRoute returns the index of a tab with the given route, or -1 if not found.
func (m *Model) findTabByRoute(route string) int {
	for i, t := range m.tabs {
		if t.route == route {
			return i
		}
	}
	return -1
}

// SetReturnedFromLaunch marks that this TUI instance is returning from a Claude session.
// Must be called before the program is run.
func (m *Model) SetReturnedFromLaunch() {
	m.returnedFromLaunch = true

	// Switch to the Command Center tab so the user returns to where they launched from.
	if idx := m.findTabByRoute("commandcenter"); idx >= 0 {
		m.activeTab = tab(idx)
	}
}

// SetReturnContext stores the todo context from the previous launch so plugins
// can restore state (e.g., return to detail view, update session status).
func (m *Model) SetReturnContext(todoID string, wasResumeJoin bool) {
	m.returnTodoID = todoID
	m.returnWasResumeJoin = wasResumeJoin
}

// daemonAware is implemented by plugins that need a daemon client reference.
type daemonAware interface {
	SetDaemonClientFunc(fn func() *daemon.Client)
}

// SetDaemonConn attaches the daemon connection to the model.
// Must be called before the program is run.
func (m *Model) SetDaemonConn(dc *DaemonConn) {
	m.daemonConn = dc
	for _, p := range m.allPlugins {
		if da, ok := p.(daemonAware); ok {
			da.SetDaemonClientFunc(dc.Client)
		}
	}

	// Subscribe to LLM activity events and forward to daemon (fire-and-forget).
	if m.bus != nil {
		m.bus.Subscribe("llm.started", func(evt plugin.Event) {
			client := dc.Client()
			if client == nil {
				return
			}
			payload, ok := evt.Payload.(llm.EventPayload)
			if !ok {
				return
			}
			id, _ := payload["id"].(string)
			operation, _ := payload["operation"].(string)
			source, _ := payload["source"].(string)
			go client.ReportLLMActivity(daemon.LLMActivityEvent{
				ID:        id,
				Operation: operation,
				Source:    source,
				StartedAt: time.Now(),
				Status:    "running",
			})
		})

		m.bus.Subscribe("llm.finished", func(evt plugin.Event) {
			client := dc.Client()
			if client == nil {
				return
			}
			payload, ok := evt.Payload.(llm.EventPayload)
			if !ok {
				return
			}
			id, _ := payload["id"].(string)
			operation, _ := payload["operation"].(string)
			source, _ := payload["source"].(string)
			durationMs, _ := payload["duration_ms"].(int64)
			errMsg, _ := payload["error"].(string)
			status, _ := payload["status"].(string)
			startedAt, _ := payload["started_at"].(time.Time)
			now := time.Now()
			go client.ReportLLMActivity(daemon.LLMActivityEvent{
				ID:         id,
				Operation:  operation,
				Source:     source,
				StartedAt:  startedAt,
				FinishedAt: &now,
				DurationMs: int(durationMs),
				Error:      errMsg,
				Status:     status,
			})
		})
	}
}

// DaemonClient returns the daemon RPC client, or nil if not connected.
func (m Model) DaemonClient() *daemon.Client {
	return m.daemonConn.Client()
}

// DaemonConnected returns whether the daemon connection is active.
func (m Model) DaemonConnected() bool {
	return m.daemonConn.Connected()
}

// SetOnboarding enables the onboarding flow. Must be called before the program is run.
func (m *Model) SetOnboarding() {
	m.onboarding = true
	m.onboardingState = newOnboardingState(m.cfg)
}

func (m Model) activePlugin() plugin.Plugin {
	return m.tabs[m.activeTab].plugin
}

// Shutdown calls Shutdown on every unique plugin and closes daemon connections.
func (m Model) Shutdown() {
	seen := map[string]bool{}
	for _, p := range m.allPlugins {
		if !seen[p.Slug()] {
			seen[p.Slug()] = true
			p.Shutdown()
		}
	}
	m.daemonConn.Close()
}

func (m Model) Init() tea.Cmd {
	// During onboarding, only run the tick — defer plugin init until onboarding completes.
	if m.onboarding {
		return ui.TickCmd()
	}

	var cmds []tea.Cmd
	cmds = append(cmds, ui.TickCmd())

	// Collect StartCmds from all plugins that implement Starter.
	seen := map[string]bool{}
	for _, p := range m.allPlugins {
		if seen[p.Slug()] {
			continue
		}
		seen[p.Slug()] = true
		if starter, ok := p.(plugin.Starter); ok {
			if cmd := starter.StartCmds(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// Initial data load for plugins that need it.
	if m.db != nil {
		for _, p := range m.allPlugins {
			switch p.Slug() {
			case "sessions", "prs":
				if cmd := p.Refresh(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	}

	// Trigger initial budget poll.
	cmds = append(cmds, m.pollBudgetCmd())

	if m.returnedFromLaunch {
		todoID := m.returnTodoID
		wasResume := m.returnWasResumeJoin
		cmds = append(cmds, func() tea.Msg {
			return plugin.ReturnMsg{
				TodoID:        todoID,
				WasResumeJoin: wasResume,
			}
		})
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.onboarding {
		return m.updateOnboarding(msg)
	}

	switch msg := msg.(type) {
	case ui.TickMsg:
		m.frame++
		var cmds []tea.Cmd
		cmds = append(cmds, ui.TickCmd())
		// Poll budget status periodically from the daemon.
		if time.Since(m.budgetLastPoll) >= budgetPollInterval && m.DaemonConnected() {
			m.budgetLastPoll = time.Now()
			cmds = append(cmds, m.pollBudgetCmd())
		}
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)

	case tea.FocusMsg:
		// Terminal regained focus — force a full screen repaint to clear ghost artifacts.
		var cmds []tea.Cmd
		cmds = append(cmds, tea.ClearScreen)
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		var cmds []tea.Cmd
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)

	case quitTimeoutMsg:
		// Timer expired — cancel pending quit.
		m.pendingQuit = false
		return m, nil

	case tea.KeyMsg:
		// Any non-esc key cancels pending quit.
		if m.pendingQuit && msg.Type != tea.KeyEsc {
			m.pendingQuit = false
		}

		switch msg.Type {
		case tea.KeyTab:
			// Let the active plugin try Tab first — forms and inline editors
			// need Tab to navigate between fields (BUG-041).
			action := m.activePlugin().HandleKey(msg)
			if action.Type == plugin.ActionConsumed || action.TeaCmd != nil {
				return m.processAction(action)
			}
			prev := m.activeTab
			m.activeTab = (m.activeTab + 1) % tab(len(m.tabs))
			cmd := m.activateTab(prev)
			return m, cmd
		case tea.KeyShiftTab:
			// Let the active plugin try Shift+Tab first (same as Tab above).
			action := m.activePlugin().HandleKey(msg)
			if action.Type == plugin.ActionConsumed || action.TeaCmd != nil {
				return m.processAction(action)
			}
			prev := m.activeTab
			m.activeTab = (m.activeTab + tab(len(m.tabs)) - 1) % tab(len(m.tabs))
			cmd := m.activateTab(prev)
			return m, cmd
		case tea.KeyCtrlZ:
			return m, tea.Suspend
		case tea.KeyCtrlX:
			// Emergency stop: kill all running agents via daemon.
			if client := m.DaemonClient(); client != nil {
				result, err := client.StopAllAgents()
				if err == nil {
					m.flashMessage = fmt.Sprintf("EMERGENCY STOP: %d agent(s) killed", result.Stopped)
					m.flashExpiresAt = time.Now().Add(3 * time.Second)
				} else {
					m.flashMessage = fmt.Sprintf("Emergency stop failed: %v", err)
					m.flashExpiresAt = time.Now().Add(3 * time.Second)
				}
			} else {
				m.flashMessage = "Emergency stop: daemon not connected"
				m.flashExpiresAt = time.Now().Add(3 * time.Second)
			}
			return m, nil
		case tea.KeyEsc:
			// Console overlay intercepts esc before the plugin.
			if m.console.visible {
				if m.console.detail {
					m.console.detail = false
					m.console.scroll = 0
				} else {
					m.console.close()
				}
				return m, nil
			}
			// Let active plugin try esc first
			action := m.activePlugin().HandleKey(msg)
			if action.Type != "unhandled" && action.Type != "quit" {
				return m.processAction(action)
			}
			// Double-escape to quit: second esc within timeout quits immediately.
			if m.pendingQuit && time.Since(m.pendingQuitAt) < quitTimeout {
				return m, tea.Quit
			}
			// First esc at top level: start pending quit with timeout.
			m.pendingQuit = true
			m.pendingQuitAt = time.Now()
			return m, tea.Tick(quitTimeout, func(time.Time) tea.Msg {
				return quitTimeoutMsg{}
			})
		}

		// Console overlay: handle keys when visible.
		if m.console.visible {
			switch msg.String() {
			case "~":
				if m.console.detail {
					m.console.detail = false
					m.console.scroll = 0
				} else {
					m.console.close()
				}
				return m, nil
			case "j", "down":
				if m.console.detail {
					if m.console.scroll < m.console.maxDetailScroll(m.height) {
						m.console.scroll++
					}
				} else if m.console.cursor < len(m.console.entries)-1 {
					m.console.cursor++
				}
				return m, nil
			case "k", "up":
				if m.console.detail {
					if m.console.scroll > 0 {
						m.console.scroll--
					}
				} else if m.console.cursor > 0 {
					m.console.cursor--
				}
				return m, nil
			case "enter":
				if !m.console.detail && m.console.selected() != nil {
					m.console.detail = true
					m.console.scroll = 0
				}
				return m, nil
			case "X": // Shift+X: kill selected agent
				if m.console.detail {
					if e := m.console.selected(); e != nil && (e.Status == "running" || e.Status == "processing" || e.Status == "blocked") {
						if client := m.DaemonClient(); client != nil {
							_ = client.StopAgent(e.AgentID)
							m.flashMessage = fmt.Sprintf("Killed agent %s", e.AgentID)
							m.flashExpiresAt = time.Now().Add(3 * time.Second)
							// Refresh entries
							if entries, err := client.ListAgentHistory(24); err == nil {
								m.console.entries = entries
								if m.console.cursor >= len(entries) {
									m.console.cursor = max(0, len(entries)-1)
								}
							}
							m.console.detail = false
						}
					}
				}
				return m, nil
			default:
				return m, nil // consume all keys while overlay is open
			}
		}

		action := m.activePlugin().HandleKey(msg)
		if action.Type == plugin.ActionConsumed || action.TeaCmd != nil {
			return m.processAction(action)
		}

		if msg.String() == "~" {
			var entries []db.AgentHistoryEntry
			if client := m.DaemonClient(); client != nil {
				entries, _ = client.ListAgentHistory(24)
				if activity, err := client.ListLLMActivity(); err == nil {
					m.console.llmActivity = activity
				}
			}
			m.console.toggle(entries)
			return m, nil
		}

		return m.processAction(action)

	case DaemonEventMsg:
		// Route daemon events through the event bus so plugins can react.
		if m.bus != nil {
			routeDaemonEvent(m.bus, msg.Event)
		}
		// Refresh console entries on relevant agent events if the overlay is open.
		if m.console.visible {
			switch msg.Event.Type {
			case "agent.started", "agent.finished", "agent.stopped", "agent.cost_updated":
				if client := m.DaemonClient(); client != nil {
					if entries, err := client.ListAgentHistory(24); err == nil {
						m.console.entries = entries
						if m.console.cursor >= len(entries) {
							m.console.cursor = max(0, len(entries)-1)
						}
					}
				}
			case "llm.started", "llm.finished":
				if client := m.DaemonClient(); client != nil {
					if activity, err := client.ListLLMActivity(); err == nil {
						m.console.llmActivity = activity
					}
				}
			}
		}
		// Refresh budget widget on cost updates so the spend figure stays current.
		if msg.Event.Type == "agent.cost_updated" && m.DaemonConnected() {
			m.budgetLastPoll = time.Now()
			var cmds []tea.Cmd
			cmds = append(cmds, m.pollBudgetCmd())
			m.broadcastMessage(plugin.NotifyMsg{Event: msg.Event.Type, Data: msg.Event.Data}, &cmds)
			return m, tea.Batch(cmds...)
		}
		// Broadcast NotifyMsg for all daemon events so plugins can dispatch
		// async refresh commands via HandleMessage (instead of mutating state
		// directly in event bus handlers, which risks data races with tea.Cmd
		// goroutines).
		var cmds []tea.Cmd
		m.broadcastMessage(plugin.NotifyMsg{Event: msg.Event.Type, Data: msg.Event.Data}, &cmds)
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case DaemonDisconnectedMsg:
		if m.daemonConn != nil {
			m.daemonConn.connected.Store(false)
		}
		// Schedule a reconnect attempt.
		return m, daemonReconnectCmd()

	case daemonReconnectMsg:
		// Attempt to reconnect to the daemon.
		if m.daemonConn != nil && m.daemonConn.Reconnect() {
			return m, nil
		}
		// Still disconnected — schedule another attempt after delay.
		return m, daemonReconnectCmd()

	case budgetStatusMsg:
		if msg.err == nil {
			m.budgetStatus = msg.status
			m.budgetAvailable = true
		}
		return m, nil

	case plugin.AgentStateChangedMsg:
		// A plugin launched, queued, finished, or killed an agent.
		// Immediately re-poll budget status so the budget widget updates
		// without waiting for the next 5-second tick.
		var cmds []tea.Cmd
		if m.DaemonConnected() {
			m.budgetLastPoll = time.Now()
			cmds = append(cmds, m.pollBudgetCmd())
		}
		// Refresh console entries if the overlay is open.
		if m.console.visible {
			if client := m.DaemonClient(); client != nil {
				if entries, err := client.ListAgentHistory(24); err == nil {
					m.console.entries = entries
					if m.console.cursor >= len(entries) {
						m.console.cursor = max(0, len(entries)-1)
					}
				}
			}
		}
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)

	case plugin.NotifyMsg:
		// External notification — reload all plugins from DB
		var cmds []tea.Cmd
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)

	case plugin.LaunchReadyMsg:
		// A plugin finished async pre-launch work (e.g. stopping a daemon
		// agent). Now it's safe to quit and hand off to RunClaude.
		return m, tea.Quit

	case plugin.LaunchRequestMsg:
		// A plugin emitted a launch request via tea.Cmd (e.g. after fzf
		// browse selection). Route it through processAction so the host
		// handles it the same as an ActionLaunch from HandleKey.
		return m.processAction(plugin.Action{
			Type: plugin.ActionLaunch,
			Args: msg.Args,
		})

	default:
		var cmds []tea.Cmd
		m.broadcastMessage(msg, &cmds)
		return m, tea.Batch(cmds...)
	}
}

// broadcastMessage sends a message to all unique plugins and collects cmds.
func (m *Model) broadcastMessage(msg tea.Msg, cmds *[]tea.Cmd) {
	seen := map[string]bool{}
	for _, t := range m.tabs {
		slug := t.plugin.Slug()
		if seen[slug] {
			continue
		}
		seen[slug] = true
		_, action := t.plugin.HandleMessage(msg)
		if action.TeaCmd != nil {
			*cmds = append(*cmds, action.TeaCmd)
		}
	}
}

func (m *Model) activateTab(prevTab tab) tea.Cmd {
	var cmds []tea.Cmd

	// Send TabLeaveMsg to the previous plugin.
	prevEntry := m.tabs[prevTab]
	_, leaveAction := prevEntry.plugin.HandleMessage(plugin.TabLeaveMsg{Route: prevEntry.route})
	if leaveAction.TeaCmd != nil {
		cmds = append(cmds, leaveAction.TeaCmd)
	}

	// Navigate the new plugin to its route.
	newEntry := m.tabs[m.activeTab]
	newEntry.plugin.NavigateTo(newEntry.route, nil)

	// Send TabViewMsg to the new plugin.
	_, viewAction := newEntry.plugin.HandleMessage(plugin.TabViewMsg{Route: newEntry.route})
	if viewAction.TeaCmd != nil {
		cmds = append(cmds, viewAction.TeaCmd)
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func (m Model) processAction(action plugin.Action) (tea.Model, tea.Cmd) {
	// Rebuild tabs in case a plugin toggle changed visibility.
	m.rebuildTabs()

	switch action.Type {
	case plugin.ActionLaunch:
		// Constrain launch dir for external plugins to learned paths only.
		if dir := action.Args["dir"]; dir != "" {
			if _, isExt := m.activePlugin().(*external.ExternalPlugin); isExt {
				if err := validateLaunchDir(m.db, dir); err != nil {
					// Reject the launch — log a warning and return to the TUI.
					fmt.Fprintf(
						os.Stderr,
						"WARNING: external plugin %q launch rejected: %v\n",
						m.activePlugin().Slug(), err,
					)
					return m, nil
				}
			}
		}

		la := &LaunchAction{Dir: action.Args["dir"]}
		if rid := action.Args["resume_id"]; rid != "" {
			la.Args = []string{"--resume", rid}
			la.WasResumeJoin = true
		}
		if prompt := action.Args["initial_prompt"]; prompt != "" {
			la.InitialPrompt = prompt
		}
		if action.Args["worktree"] == "true" {
			la.Worktree = true
		}
		if todoID := action.Args["todo_id"]; todoID != "" {
			la.ReturnToTodoID = todoID
		}
		if sid := action.Args["session_id"]; sid != "" {
			la.SessionID = sid
		}
		m.Launch = la
		// Broadcast LaunchMsg to all plugins before quitting.
		var cmds []tea.Cmd
		m.broadcastMessage(plugin.LaunchMsg{
			Dir:      action.Args["dir"],
			ResumeID: action.Args["resume_id"],
		}, &cmds)
		// When resuming a session, a plugin may need async time to stop the
		// daemon agent. In that case the plugin returns a tea.Cmd that emits
		// LaunchReadyMsg when done; we defer tea.Quit until that arrives.
		// For non-resume launches (or when no plugin returns a cmd) quit now.
		if action.Args["resume_id"] == "" || len(cmds) == 0 {
			cmds = append(cmds, tea.Quit)
		}
		return m, tea.Batch(cmds...)

	case plugin.ActionOpenURL:
		if action.Payload != "" {
			_ = exec.Command("open", action.Payload).Start()
		}
		return m, nil

	case plugin.ActionQuit:
		return m, tea.Quit

	case plugin.ActionNavigate:
		var cmd tea.Cmd
		switch action.Payload {
		case "sessions":
			if idx := m.findTabByRoute("sessions"); idx >= 0 {
				prev := m.activeTab
				m.activeTab = tab(idx)
				cmd = m.activateTab(prev)
			}
		case "command":
			if idx := m.findTabByRoute("commandcenter"); idx >= 0 {
				prev := m.activeTab
				m.activeTab = tab(idx)
				cmd = m.activateTab(prev)
			}
		}
		return m, cmd

	case plugin.ActionUnhandled:
		return m, tea.Quit

	default: // ActionNoop and anything else
		if action.TeaCmd != nil {
			return m, action.TeaCmd
		}
		return m, nil
	}
}

func (m Model) View() string {
	topPad := strings.Repeat("\n", m.cfg.GetBannerTopPadding())

	if m.onboarding {
		// During onboarding, always show banner for preview (unless user toggled it off).
		var banner string
		if m.cfg.BannerVisible() {
			banner = topPad + renderGradientBanner(m.grad, m.cfg.Name, m.cfg.Subtitle, ui.ContentMaxWidth, m.frame)
		} else {
			banner = topPad
		}
		content := m.onboardingState.view(m.width, m.height, m.styles, m.grad, m.cfg, m.frame)
		page := lipgloss.JoinVertical(lipgloss.Left, banner, "", content)
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, page)
		}
		return page
	}

	var sections []string
	if m.cfg.BannerVisible() {
		sections = append(sections, topPad+renderGradientBanner(m.grad, m.cfg.Name, m.cfg.Subtitle, ui.ContentMaxWidth, m.frame))
	}

	tabBar := m.renderTabBar()

	// Compute overhead height so plugins know the available content area.
	// Build the header sections, count their lines, and pass the remainder.
	headerParts := make([]string, len(sections))
	copy(headerParts, sections)
	headerParts = append(headerParts, "", tabBar, "")
	header := lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	contentHeight := m.height - strings.Count(header, "\n") - 1
	if contentHeight < 10 {
		contentHeight = 10
	}

	content := m.activePlugin().View(m.width, contentHeight, m.frame)

	// Pad each section to ContentMaxWidth so JoinVertical(Left) produces
	// stable horizontal alignment regardless of plugin content width (BUG-127).
	// Use MaxWidth to clamp sections that exceed ContentMaxWidth.
	padWidth := ui.ContentMaxWidth
	pad := func(s string) string {
		clamped := lipgloss.NewStyle().MaxWidth(padWidth).Render(s)
		return lipgloss.PlaceHorizontal(padWidth, lipgloss.Left, clamped)
	}
	sections = append(sections, pad(""), tabBar)
	if m.flashMessage != "" && time.Now().Before(m.flashExpiresAt) {
		hint := m.styles.TitleBoldC.Render(m.flashMessage)
		sections = append(sections, lipgloss.PlaceHorizontal(ui.ContentMaxWidth, lipgloss.Center, hint))
	} else if m.pendingQuit {
		hint := m.styles.TitleBoldC.Render("Press esc again to quit")
		sections = append(sections, lipgloss.PlaceHorizontal(ui.ContentMaxWidth, lipgloss.Center, hint))
	} else {
		m.flashMessage = "" // clear expired flash
		sections = append(sections, pad(""))
	}
	sections = append(sections, pad(content))
	page := lipgloss.JoinVertical(lipgloss.Left, sections...)

	if m.width > 0 && m.height > 0 {
		page = lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, page)

		// Overlay console when visible — replaces page content entirely.
		if m.console.visible {
			page = m.console.render(m.width, m.height)
		}

		// Overlay budget widget pinned to the upper-right corner (2 chars from right).
		// When the banner is visible, place on row 1 (inside the banner).
		// When hidden, place on row 0 to avoid overwriting the tab bar.
		if budget := m.renderBudgetWidget(); budget != "" && m.width > 0 {
			bw := lipgloss.Width(budget)
			pad := m.width - bw - 2
			if pad > 0 {
				budgetLine := strings.Repeat(" ", pad) + budget
				lines := strings.Split(page, "\n")
				row := 0
				if m.cfg.BannerVisible() {
					row = 1
				}
				if row < len(lines) {
					lines[row] = budgetLine
				}
				page = strings.Join(lines, "\n")
			}
		}

		return page
	}
	return page
}

func (m Model) renderTabBar() string {
	sep := m.styles.InactiveTab.Render(" | ")
	var parts []string
	for i, t := range m.tabs {
		if tab(i) == m.activeTab {
			parts = append(parts, m.styles.ActiveTab.Render("> "+t.label))
		} else {
			parts = append(parts, m.styles.InactiveTab.Render(t.label))
		}
		if i < len(m.tabs)-1 {
			parts = append(parts, sep)
		}
	}
	tabBar := strings.Join(parts, "")
	return lipgloss.PlaceHorizontal(ui.ContentMaxWidth, lipgloss.Center, tabBar)
}

// pollBudgetCmd returns a tea.Cmd that fetches budget status from the daemon.
func (m Model) pollBudgetCmd() tea.Cmd {
	return func() tea.Msg {
		client := m.DaemonClient()
		if client == nil {
			return budgetStatusMsg{err: fmt.Errorf("no daemon connection")}
		}
		status, err := client.GetBudgetStatus()
		return budgetStatusMsg{status: status, err: err}
	}
}

// renderBudgetWidget returns the styled budget widget string for the top-right corner.
// Shows budget and agent count, or [not running] when the daemon isn't connected.
func (m Model) renderBudgetWidget() string {
	if !m.DaemonConnected() {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89")).
			Render("[not running]")
	}

	bs := m.budgetStatus

	// Emergency stop overrides everything.
	if m.budgetAvailable && bs.EmergencyStopped {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f7768e")).
			Bold(true).
			Render("[EMERGENCY STOP]")
	}

	spent := bs.HourlySpent
	limit := bs.HourlyLimit
	agents := bs.ActiveAgents

	// Format: [$X.XX/$Y/hr · 3 agents]
	text := fmt.Sprintf("[$%.2f/$%.0f/hr", spent, limit)
	if agents > 0 {
		text += fmt.Sprintf(" · %d agent", agents)
		if agents != 1 {
			text += "s"
		}
	}

	// Determine style based on warning level and agent activity.
	if m.budgetAvailable {
		switch bs.WarningLevel {
		case "critical":
			text += " CRITICAL]"
			return lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f7768e")).
				Bold(true).
				Render(text)
		case "warning":
			text += " \u26a0]"
			return lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e0af68")).
				Render(text)
		}
	}

	text += "]"
	if agents > 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5")).
			Render(text)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#565f89")).
		Render(text)
}
