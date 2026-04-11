# SPEC: TUI Host (internal/tui)

## Purpose

The thin host shell for the Claude Command Center. Manages the Bubbletea application lifecycle, tab bar, banner animation, and plugin dispatch. Contains no domain logic — all functionality lives in plugins.

## Interface

- **Inputs**: `*sql.DB`, `*config.Config`, `plugin.EventBus`, `plugin.Logger`, `llm.LLM` (passed to `NewModel`); optional external plugins via variadic `extPlugins`; `DaemonConn` attached via `SetDaemonConn()`; onboarding mode via `SetOnboarding()`; return context via `SetReturnedFromLaunch()` / `SetReturnContext()`
- **Outputs**: `Model` implementing `tea.Model` (Init/Update/View); `LaunchAction` set when a plugin requests a session launch
- **Dependencies**: `internal/config`, `internal/plugin`, `internal/builtin/sessions`, `internal/builtin/commandcenter`, `internal/builtin/prs`, `internal/builtin/settings`, `internal/daemon`, `internal/agent`, `internal/external`, `internal/worktree`, Bubbletea framework

## Architecture

### Files

- `model.go` — Main model struct, plugin wiring, Init/Update/View, action dispatch, budget widget, flash messages
- `styles.go` — Styles struct derived from `config.Palette` (all colors configurable)
- `effects.go` — Animation: tick messages, gradient interpolation, fade-in, pulsing pointer
- `banner.go` — ASCII art banner with animated gradient, subtitle from config name
- `launch.go` — `LaunchAction` type, `RunClaude` function, `resolveSessionDir`, `validateLaunchDir`
- `daemon.go` — `DaemonConn` lifecycle, auto-start, reconnect, event subscription, event routing
- `onboarding.go` — Multi-step setup wizard (welcome, palette, sources, done), skills install, shell hook detection
- `stub_plugin.go` — Placeholder plugin for disabled external plugins
- `notify.go` — Unix socket cross-instance notification (PID-scoped sockets, `SendNotify`)

### Model

```go
type Model struct {
    cfg       *config.Config
    styles    *Styles
    grad      *GradientColors
    tabs      []tabEntry       // visible (filtered) tab list
    allTabs   []tabEntry       // full unfiltered tab list
    activeTab tab
    width, height, frame int
    Launch    *LaunchAction
    allPlugins []plugin.Plugin  // every unique plugin for lifecycle management
    returnedFromLaunch bool     // set when TUI restarts after a Claude session
    returnTodoID string         // todo ID to return to after Claude session
    returnWasResumeJoin bool    // true if session was a join/resume
    pendingQuit bool            // double-esc-to-quit state
    pendingQuitAt time.Time
    onboarding bool             // true during setup wizard
    onboardingState *onboardingState
    flashMessage string         // temporary status below tab bar
    flashExpiresAt time.Time
    db        *sql.DB
    daemonConn *DaemonConn
    bus        plugin.EventBus
    budgetStatus daemon.BudgetStatusResult
    budgetLastPoll time.Time
    budgetAvailable bool        // true after first successful poll
}
```

### Tab Entries

Each tab maps a label to a plugin and a route within that plugin. Multiple tabs can reference the same plugin with different routes:

| Tab | Plugin | Route |
|-----|--------|-------|
| Command Center | commandcenter | `commandcenter` |
| Sessions | sessions | `sessions` |
| PRs | prs | `waiting` |
| *(external plugin tabs)* | *(external)* | *(plugin-defined)* |
| *(stub tabs for disabled plugins)* | *(stub)* | *(plugin name)* |
| Settings | settings | `settings` |

## Behavior

### Initialization

1. Build styles and gradient colors from the config palette
2. Create plugin instances (sessions, command center, PRs)
3. Build a `plugin.Registry` with all plugins (built-in + external + settings)
4. Create settings plugin with registry reference: `settings.New(registry)`
5. Create shared plugin context with the **shared bus and logger from main.go** (not a local bus — this ensures all plugins communicate via the same event bus)
6. Call `Init(ctx)` on each plugin
7. Wire tab entries to plugins and routes (external plugin tabs before settings, settings always last)
8. Add stub tab entries for configured-but-not-loaded external plugins (see [Stub Plugins](#stub-plugins))
9. Collect all unique plugins into `allPlugins` for lifecycle management
10. In `Init()`, start animation tick, plugin startup commands (`Starter` interface), initial data load, trigger initial budget poll, and emit `ReturnMsg` if `returnedFromLaunch` is set
11. If onboarding mode is active, `Init()` only starts the animation tick — plugin init is deferred until onboarding completes (see [Onboarding](#onboarding))

### Daemon Integration

The host maintains a `DaemonConn` that wraps two daemon RPC connections: one for commands, one for event subscription. The lifecycle is:

1. **Pre-allocation**: `main.go` creates a `DaemonConn` via `NewDaemonConn(logger, bus)` and attaches it to the model via `SetDaemonConn()` before `tea.NewProgram` runs. This two-phase init is required because `Model` is a value type copied by `NewProgram` — the pointer must be shared.
2. **Auto-start**: `Connect(p)` calls `connectDaemon()` which first tries a direct socket connection. If that fails, it calls `daemon.StartProcess()` to spawn the daemon as a detached background process, waits 500ms, and retries. The TUI runs without a daemon connection if both attempts fail (not fatal).
3. **Plugin injection**: `SetDaemonConn()` iterates `allPlugins` and calls `SetDaemonClientFunc(fn)` on any plugin implementing the `daemonAware` interface, giving plugins lazy access to the daemon client. It also subscribes to `llm.started` and `llm.finished` events on the event bus and forwards them to the daemon via `client.ReportLLMActivity()` in fire-and-forget goroutines.
4. **Event subscription**: `Connect()` opens a second socket connection and starts a goroutine that calls `subClient.Subscribe()`. Each daemon event is injected into the bubbletea program as a `DaemonEventMsg`.
5. **DaemonEventMsg routing**: When a `DaemonEventMsg` arrives in `Update()`, the host does two things:
   - Publishes to the event bus via `routeDaemonEvent()` (converts to `plugin.Event{Source: "daemon", Topic: evt.Type, Payload: evt.Data}`)
   - Broadcasts `plugin.NotifyMsg{Event: evt.Type, Data: evt.Data}` to all plugins so they can dispatch async tea.Cmds via `HandleMessage` (instead of mutating state directly in event bus handlers, which would race with tea.Cmd goroutines)
6. **Disconnect detection**: When the subscription goroutine exits (connection lost), it sends `DaemonDisconnectedMsg`. The host sets `connected` to false and schedules a reconnect attempt.
7. **Reconnect**: On `daemonReconnectMsg` (fired after a 10-second delay), the host calls `DaemonConn.Reconnect()` which closes stale connections, re-establishes both RPC and subscription connections, and restarts the subscription goroutine. If reconnection fails, another attempt is scheduled.
8. **Shutdown**: `Model.Shutdown()` calls `DaemonConn.Close()` which closes both connections.

### Budget Widget

The host polls the daemon for budget status and renders a widget pinned to the upper-right corner of the terminal (row 1, 2 chars from right edge). The widget is overlaid on the rendered page by replacing characters in the output string.

- **Polling**: Every 5 seconds (on tick), if the daemon is connected, the host fires a `pollBudgetCmd()` that calls `client.GetBudgetStatus()`. Results arrive as `budgetStatusMsg` and update `budgetStatus` / `budgetAvailable`.
- **Immediate refresh on agent state change**: When a plugin sends `plugin.AgentStateChangedMsg` (agent launched, queued, finished, or killed), the host immediately re-polls budget status so the widget updates without waiting for the next 5-second tick.
- **Initial poll**: Triggered during `Init()` alongside other startup commands.
- **Display states**:
  - Daemon not connected: `[not running]` in dim text
  - Emergency stopped: `[EMERGENCY STOP]` in bold red
  - Critical budget: `[$X.XX/$Y/hr CRITICAL]` in bold red
  - Warning budget: `[$X.XX/$Y/hr ⚠]` in yellow
  - Normal with agents: `[$X.XX/$Y/hr · N agent(s)]` in bright text
  - Normal idle: `[$X.XX/$Y/hr]` in dim text

### Flash Messages

The host supports temporary status messages displayed below the tab bar. A flash message consists of a string and an expiration time.

- **Setting**: Any host code can set `flashMessage` and `flashExpiresAt` (currently used by Ctrl+X emergency stop)
- **Rendering**: If `flashMessage` is non-empty and not expired, it renders centered below the tab bar in the `TitleBoldC` style. If expired, the flash is cleared.
- **Priority**: Flash messages take precedence over the double-esc quit hint in the same rendering slot.

### Input Dispatch

- **Tab/Shift+Tab**: Plugin gets first chance — the active plugin's `HandleKey` is called first. If the plugin returns `ActionConsumed` or has a `TeaCmd`, the key is consumed (this allows forms/editors to use Tab for field navigation). Otherwise, the host cycles the active tab: sends `TabLeaveMsg` to previous plugin, calls `NavigateTo(route)` on new plugin, sends `TabViewMsg` to new plugin.
- **Ctrl+Z**: Suspends the TUI process (sends `tea.Suspend` — standard terminal background behavior).
- **Ctrl+X**: Emergency stop — calls `client.StopAllAgents()` via the daemon. Shows result as a flash message ("EMERGENCY STOP: N agent(s) killed", or error/disconnected message). Does not quit the TUI.
- **Esc (double-esc to quit)**: Active plugin gets first chance. If plugin returns anything other than "unhandled" or "quit", the key is consumed. Otherwise:
  - First esc at top level: sets `pendingQuit = true`, starts a 2-second timeout timer, and renders "Press esc again to quit" hint below the tab bar.
  - Second esc within 2 seconds: quits the TUI immediately.
  - Timeout expires (`quitTimeoutMsg`): cancels pending quit.
  - Any non-esc key while pending: cancels pending quit.
- **`~` (tilde)**: Plugin gets first chance — the active plugin's `HandleKey` is called first. If the plugin returns `ActionConsumed` or has a `TeaCmd`, the key is consumed (this allows text inputs to type `~` without triggering the console overlay). Otherwise, the host toggles the console overlay.
- **All other keys**: Forward to active plugin's `HandleKey`, process the returned action.

### Message Broadcast

Non-key messages (ticks, window resize, `NotifyMsg`, custom plugin messages) are broadcast to all unique plugins via `HandleMessage`. Each plugin slug is visited once (deduplicated via the visible `tabs` slice, not `allPlugins`).

**Focus regain**: When a `tea.FocusMsg` arrives (terminal regained focus, enabled via `tea.WithReportFocus()`), the host issues `tea.ClearScreen` to force a full repaint, clearing ghost artifacts from alt-screen reentry.

### Action Processing

Plugins return `plugin.Action` values. The host processes them. After every action, `rebuildTabs()` is called to reflect any plugin toggle changes.

| Action Type | Host Behavior |
|-------------|---------------|
| `launch` | Validate dir for external plugins (see [Launch Dir Validation](#launch-dir-validation)), build `LaunchAction` from args (dir, resume_id, initial_prompt, worktree, todo_id), broadcast `LaunchMsg` to all plugins, quit TUI |
| `open_url` | Open URL via `exec.Command("open", url)` (macOS) — fire-and-forget |
| `quit` | Quit TUI |
| `navigate` | Switch to target tab (`sessions` → "new" route, `command` → "commandcenter" route), activate plugin route |
| `unhandled` | Quit TUI (esc fallthrough) |
| `noop` | Execute `TeaCmd` if present, otherwise no-op |

### Tab Filtering

The host maintains two tab lists: `allTabs` (full unfiltered set) and `tabs` (current visible subset). `rebuildTabs()` filters `allTabs` by checking `cfg.PluginEnabled(ownerSlug)` for each tab entry. When rebuilding:

- The host tries to preserve the current active tab by matching on route
- If the current route is no longer visible, the active tab is clamped to the valid range

This allows plugins to be toggled on/off at runtime (via settings) without restarting the TUI.

### Stub Plugins

When an external plugin is listed in `config.ExternalPlugins` but was not loaded at startup (e.g., the plugin binary is missing or disabled), the host creates a `stubPlugin` placeholder. The stub:

- Implements the full `plugin.Plugin` interface with no-op handlers
- Renders a centered "Restart CCC to activate this plugin" message in yellow
- Allows `rebuildTabs()` to show/hide the tab dynamically if the user toggles the plugin in settings
- Is not added to `allPlugins` (no lifecycle management needed)

### Launch Dir Validation

When a launch action comes from an external plugin (detected via type assertion to `*external.ExternalPlugin`), the host validates the requested directory against learned paths stored in the database:

1. Load all learned paths via `db.DBLoadPaths()`
2. Resolve symlinks on both the requested dir and each allowed path
3. Allow exact matches or subdirectory matches (with trailing separator to prevent prefix collisions like `/project` matching `/project2`)
4. If no match, reject the launch with a stderr warning and return to the TUI
5. Empty dir (meaning "use cwd") is always allowed

Built-in plugins are not subject to this restriction.

### Worktree Launch

When `LaunchAction.Worktree` is true, `RunClaude()` calls `worktree.PrepareWorktree(dir)` to create an isolated git worktree before launching Claude. If worktree creation fails, a warning is printed to stderr and Claude launches in the original directory as a fallback.

### Session Dir Resolution

When resuming a session (`WasResumeJoin` is true and args contain `--resume`), `RunClaude()` calls `resolveSessionDir()` to find the correct working directory:

1. Check if the session file exists under the fallback dir's encoded project path in `~/.claude/projects/`
2. If not found, scan all project directories for the session file
3. Reverse the path encoding (`-` → `/`) to recover the original directory
4. Fall through to the original dir if no match is found

This handles cases where the session was created in a different directory (e.g., via worktree or project dir mismatch).

### Onboarding

When the config file doesn't exist (first run) or `ccc setup` is invoked, the host enters onboarding mode. During onboarding:

- **Plugin init is deferred**: `Init()` only starts the animation tick. Plugin `StartCmds()` and `Refresh()` are not called until onboarding completes.
- **Update is intercepted**: All messages route through `updateOnboarding()` instead of the normal `Update()` path.
- **View is replaced**: The banner renders (for live preview during palette selection) but plugin content and tab bar are replaced by the onboarding UI.

**Onboarding steps:**

1. **Welcome** (`stepWelcome`): User sets the banner title (name) and subtitle via text inputs. Tab/Shift+Tab switches between fields.
2. **Palette** (`stepPalette`): User picks a color palette with live banner preview. Left/right arrows cycle palettes.
3. **Sources** (`stepSources`): Hub screen listing data sources (Calendar, GitHub, Granola, Slack). Each source shows validation status (credentials check). Valid sources are auto-enabled. User can enter per-source detail flows.
4. **Source Detail** (`stepSourceDetail`): Per-source configuration (calendar ID entry, GitHub repo/username, etc.). Supports fetching available calendars from Google API with a selection UI.
5. **Done** (`stepDone`): Saves config, then fires three parallel background tasks:
   - **MCP build**: `config.BuildAndConfigureMCP()` — builds and configures MCP servers
   - **Skills installation**: `config.InstallSkills()` — symlinks skill files from the CCC repo's `.claude/skills/` directory into the user's skills directory
   - **Shell hook detection**: `config.IsShellHookInstalled()` — checks if the CCC shell hook is present in `~/.zshrc` (does not auto-install; only reports status)

On completion (Enter), onboarding mode is cleared and `deferredPluginInit()` fires all deferred `StartCmds()` and `Refresh()` calls.

### TUI Loop

The TUI runs in a loop managed by `main.go`:

1. Create `Model`, set flags (`returnedFromLaunch`, onboarding), attach `DaemonConn`
2. Run `tea.NewProgram` with alt screen, focus reporting, and mouse cell motion (prevents terminal vertical scrolling; text selection available via Option+click)
3. Start unix socket listener for cross-instance notifications
4. Connect to daemon (auto-starts if needed)
5. When the program exits:
   - If `Launch` is nil (user pressed Esc): exit the loop
   - If `Launch` is set: call `RunClaude()` with an `onStart` callback. The callback receives the claude process PID and resolved directory, which `main.go` uses to register the session with the daemon. After claude exits, `main.go` updates the session to "ended" via the daemon. The daemon connection is closed *after* the session lifecycle is complete, not before `RunClaude`. Write the resolved dir to `~/.config/ccc/data/last-dir` (for shell hook cd), set `returnedFromLaunch = true`, loop back to step 1

### Session Registration on Launch

When the TUI launches a Claude session, `main.go` registers the session with the daemon so it appears in the Active tab:

1. **Before launch**: The daemon connection (`DaemonConn`) is kept open across the launch (not closed before `RunClaude`).
2. **On process start**: `RunClaude` accepts an `onStart` callback (`func(pid int)`) that fires after `cmd.Start()` succeeds but before `cmd.Wait()`. The callback registers the session with the daemon using a generated UUID as the session ID, the claude process PID, and the resolved project directory.
3. **After exit**: When claude exits, `main.go` marks the session as "ended" by updating its state via the daemon. The daemon's dead-session pruning also handles this as a fallback.
4. **Graceful degradation**: If the daemon connection is nil or registration fails, the error is logged but does not prevent the claude launch. The session simply won't appear in the Active tab.

### Cross-Plugin Communication

All cross-plugin communication uses the event bus exclusively. The host does not hold direct references to specific plugin types — it only interacts with plugins through the `plugin.Plugin` interface. The `allPlugins` slice holds every unique plugin instance for shutdown and lifecycle management.

### Rendering

1. Banner with animated gradient (top, with top padding) — hidden if `cfg.BannerVisible()` returns false
2. Tab bar with active tab highlighted (center-aligned, `> label` format)
3. Flash message or double-esc hint or empty line (below tab bar)
4. Active plugin's `View(width, contentHeight, frame)` output
5. Centered in terminal via `lipgloss.Place`
6. Budget widget overlaid on row 1, right-aligned (see [Budget Widget](#budget-widget)). The overlay row must account for banner visibility — when the banner is hidden, the overlay row must skip the tab bar.

**Width normalization**: Before joining sections vertically, each section (banner, tab bar, flash/hint, plugin content) must be padded to a consistent width (`ContentMaxWidth = 144`) using `lipgloss.PlaceHorizontal`. This ensures `JoinVertical(lipgloss.Left, ...)` produces stable horizontal alignment regardless of the active plugin's content width. Without this, narrower or wider plugin content shifts the banner and tab bar horizontally.

**Content height calculation**: The host computes the overhead (banner + spacing + tab bar) by rendering the header sections and counting newlines, then passes `terminalHeight - overhead` as `contentHeight` to the plugin (minimum 10). This prevents plugins from sizing their layouts to the full terminal height and overflowing past the banner/tabs.

### Animation

- Tick-driven gradient shimmer on banner, fade-in on startup, pulsing pointer on selected items
- Gradient uses three configurable color stops (GradStart/GradMid/GradEnd) from palette

### Cross-Instance Notification

Multiple CCC instances share the same SQLite DB. A unix socket notification system keeps them in sync:

- Each TUI instance creates a PID-scoped socket at `~/.config/ccc/data/ccc-<PID>.sock`
- A goroutine listens for newline-delimited event strings on the socket
- Incoming events are injected as `plugin.NotifyMsg` into the bubbletea program via `p.Send()`
- Plugins handle `NotifyMsg` by reloading data from DB
- `ccc notify [event]` connects to all `ccc-*.sock` files and sends the event (default: "reload")
- Stale sockets (connection refused) are automatically cleaned up

### Shutdown & Error Handling

- **Shutdown**: Calls `Shutdown()` on every unique plugin (deduplicated by slug), then closes the daemon connection
- **Database required**: If the database cannot be opened, the process exits with a clear error message
- **Signal handling**: SIGINT and SIGTERM trigger graceful shutdown — all external plugin subprocesses are cleaned up
- **Claude exit errors**: If `claude` exits non-zero, the error is printed to stderr but the TUI loop continues
- **RunClaude error propagation**: `launch.go:RunClaude()` returns errors from `cmd.Run()`
- **Interactive launch with InitialPrompt**: When `LaunchAction.InitialPrompt` is set, `RunClaude` writes the prompt to `~/.config/ccc/data/task-context.md`, passes it via `--append-system-prompt` (persistent context across the session), and sends a short kickoff message as the positional prompt argument so Claude starts working immediately instead of waiting for user input
- **Session ID env var**: When `LaunchAction.SessionID` is set, `RunClaude` passes it as `CCC_SESSION_ID` in the subprocess environment. This allows the CLAUDE.md session topic snippet inside the Claude subprocess to call `ccc update-session --session-id` with the correct CCC-generated session ID (instead of trying to read Claude's internal session ID from pid-map files, which is a different UUID).

## Key Design Decisions

1. **No domain logic in host** — The host knows nothing about todos, pull requests, calendar, or sessions. It only knows about tabs, plugins, and actions.
2. **Colors from palette** — No hardcoded color constants. All colors derived from `config.Palette` via `NewStyles()`.
3. **Multiple tabs per plugin** — A single plugin can power multiple tabs via different routes.
4. **Plugin registration order** — Tab order is defined by the host, not the plugins.
5. **Event-bus-only communication** — No direct plugin-to-plugin references; all cross-plugin communication goes through the shared event bus.
6. **Two-phase daemon init** — `DaemonConn` is pre-allocated and attached via pointer before `tea.NewProgram` copies the `Model` value type. This ensures the bubbletea model and `main.go` share the same connection state.
7. **Dual daemon event path** — Daemon events are routed both through the event bus (for synchronous handlers) and as `NotifyMsg` broadcasts (for async tea.Cmd dispatching), preventing data races.
8. **Plugin-first key dispatch for Tab/Esc** — The active plugin gets first chance at Tab, Shift+Tab, and Esc keys before the host processes them. This allows plugin forms and editors to consume these keys without host interference.
9. **Graceful daemon absence** — The TUI runs fully without a daemon connection. Budget widget shows "[not running]", Ctrl+X shows "daemon not connected", and no features crash.
10. **Stub plugins for hot-toggle** — Disabled external plugins get placeholder tabs so they appear in the tab bar immediately when enabled in settings, without requiring a restart for tab registration (only for rendering real content).

## Test Cases

- NewModel creates model with correct config name and initial tab
- Tab navigation cycles through all tabs and wraps
- Tab switching sends TabLeaveMsg/TabViewMsg
- Tab key is consumed by plugin when plugin returns ActionConsumed
- Window resize updates dimensions
- View renders without panic
- Styles generated for all built-in palettes
- Gradient color interpolation produces valid hex
- subtitleFromName generates spaced uppercase from config name
- Double-esc quits the TUI; single esc shows hint and times out
- Non-esc key cancels pending quit state
- Tab entries map to correct plugins
- allPlugins contains all unique plugin instances
- returnedFromLaunch emits ReturnMsg on Init
- Shutdown calls Shutdown on each unique plugin once and closes daemon connection
- Stub plugin renders "Restart CCC to activate" message
- validateLaunchDir rejects dirs outside learned paths
- validateLaunchDir allows subdirectories of learned paths
- validateLaunchDir rejects prefix collisions (e.g., `/project2` vs `/project`)
- resolveSessionDir finds session in non-default project directory
- resolveSessionDir falls back to original dir when session not found
- Onboarding defers plugin init until completion
- Budget widget shows correct state for each warning level
- AgentStateChangedMsg triggers immediate budget re-poll when daemon is connected
- Flash message renders when active and clears when expired
- DaemonDisconnectedMsg triggers reconnect cycle
- FocusMsg triggers ClearScreen for repaint
- Tab bar remains visible when banner is hidden (budget widget overlay must not overwrite the tab bar row)
- RunClaude onStart callback fires with the process PID after cmd.Start succeeds
- Session is registered with daemon before waiting for claude to exit
- Session is marked ended after claude exits
- Launch succeeds even when daemon connection is nil (graceful degradation)
- Banner and tab bar have consistent width across all tabs (BUG-127): switching tabs must not shift the banner or tab bar horizontally — every section in the vertical join must be padded to the same width
