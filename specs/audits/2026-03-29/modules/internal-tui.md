# Spec Audit: internal/tui, internal/ui

**Date:** 2026-03-29
**Specs:** specs/core/host.md, specs/core/onboarding.md

---

## internal/tui/model.go

### NewModel

- **[COVERED]** host.md "Initialization" steps 1-8: palette, plugins, registry, settings, context, Init, tabs, allPlugins
- **[UNCOVERED-BEHAVIORAL]** Agent runner creation (`agent.NewRunner`) with max concurrent from config. Host spec says "no domain logic in host" — the agent runner wiring is an integration detail not mentioned. **Intent question:** Should agent runner construction be documented?
- **[UNCOVERED-BEHAVIORAL]** PRs plugin (`prs.Plugin`) is created and registered but not mentioned in host.md tab table. **Intent question:** Add PRs to the host spec tab table?
- **[COVERED]** host.md tab table shows tab ordering (sessions tabs, command center, external, settings last)
- **[UNCOVERED-BEHAVIORAL]** "Active" tab (route: "active") — host.md tab table shows "New Session" and "Resume" but not "Active". The code has three sessions tabs: Active, New Session, Resume. **Intent question:** Update host spec tab table to include all three sessions routes?
- **[UNCOVERED-BEHAVIORAL]** Stub plugins for disabled external plugins — code creates `stubPlugin` placeholders. Not in host spec.

### rebuildTabs

- **[UNCOVERED-BEHAVIORAL]** Dynamic tab filtering based on PluginEnabled, preserving active tab route. Not explicitly spec'd. **Intent question:** Should the host spec document runtime tab rebuilding?

### Init

- **[COVERED]** host.md "Initialization" step 9: "start animation tick, plugin startup commands (Starter interface), initial data load, and emit ReturnMsg if returnedFromLaunch is set"
- **[UNCOVERED-BEHAVIORAL]** Budget polling started on Init (`pollBudgetCmd`). Not in host spec.

### Update — Key handling

- **[COVERED]** host.md "Input Dispatch": Tab/Shift+Tab cycles, Esc handling, forward to plugin
- **[UNCOVERED-BEHAVIORAL]** Tab/Shift+Tab first offers to active plugin — if plugin consumes it (e.g., form navigation), host does not cycle. Spec says "Tab/Shift+Tab: Cycle active tab" without mentioning plugin-first dispatch.
- **[COVERED]** host.md: Esc offers to active plugin first, then quits
- **[UNCOVERED-BEHAVIORAL]** Double-escape to quit — first esc starts 2-second pending quit timer, second esc within timeout quits. Spec says "if plugin returns 'unhandled' or 'quit', exit the TUI" without mentioning double-esc. **Intent question:** Should double-esc be spec'd?
- **[UNCOVERED-BEHAVIORAL]** Ctrl+Z suspends (tea.Suspend). Not in spec.
- **[UNCOVERED-BEHAVIORAL]** Ctrl+X emergency stop — kills all running agents via daemon. Not in host spec.

### Update — Message handling

- **[COVERED]** host.md "Message Broadcast": ticks, window resize, NotifyMsg broadcast to all unique plugins
- **[COVERED]** host.md "Daemon events": DaemonEventMsg routes through event bus AND broadcasts NotifyMsg
- **[UNCOVERED-BEHAVIORAL]** `DaemonDisconnectedMsg` triggers reconnect timer. Not in host spec.
- **[UNCOVERED-BEHAVIORAL]** `daemonReconnectMsg` attempts reconnection with 10s interval. Not in host spec.
- **[UNCOVERED-BEHAVIORAL]** `budgetStatusMsg` updates budget widget state. Not in host spec.
- **[UNCOVERED-BEHAVIORAL]** `tea.FocusMsg` triggers full screen repaint (`tea.ClearScreen`). Not in host spec.

### processAction

- **[COVERED]** host.md "Action Processing" table: launch, quit, navigate, unhandled, noop
- **[UNCOVERED-BEHAVIORAL]** ActionLaunch validates external plugin launch directories against learned paths (`validateLaunchDir`). Not in host spec. **Intent question:** Should external plugin launch dir validation be spec'd?
- **[UNCOVERED-BEHAVIORAL]** ActionLaunch supports `worktree: "true"` arg. Not in host spec action table.
- **[UNCOVERED-BEHAVIORAL]** ActionOpenURL opens a URL via `exec.Command("open", ...)`. Not in host spec action table. **Intent question:** Add "open_url" action to spec?

### View

- **[COVERED]** host.md "Rendering" steps 1-4: banner, tab bar, plugin view, centered
- **[COVERED]** host.md "Content height calculation": overhead computation, passes contentHeight to plugin
- **[UNCOVERED-BEHAVIORAL]** Flash message display below tab bar (with expiry). Not in host spec.
- **[UNCOVERED-BEHAVIORAL]** "Press esc again to quit" hint when pendingQuit is active. Not in spec.
- **[UNCOVERED-BEHAVIORAL]** Budget widget pinned to upper-right corner, showing spend/limit/agents/status. Not in host spec.

### Shutdown

- **[COVERED]** host.md "Shutdown": calls Shutdown on each unique plugin (deduplicated by slug)
- **[UNCOVERED-BEHAVIORAL]** Also closes daemon connection. Not explicit in host spec.

---

## internal/tui/banner.go

### textToBanner / subtitleFromText

- **[COVERED]** onboarding.md "Dynamic Block Font": blockFont map, textToBanner, subtitleFromText behavior
- **[COVERED]** host.md "Animation" section

### renderBanner / renderGradientBanner

- **[COVERED]** host.md "Rendering" step 1 and "Animation" section: gradient shimmer, configurable color stops

---

## internal/tui/daemon.go

### DaemonConn / NewDaemonConn / Connect / Reconnect / Close

- **[UNCOVERED-BEHAVIORAL]** Entire daemon connection lifecycle (auto-start, two connections — RPC + subscription, reconnection with exponential backoff). No spec covers TUI-daemon connection management. **Intent question:** Should host.md have a "Daemon Connection" section?

### connectDaemon / autoStartDaemon

- **[UNCOVERED-BEHAVIORAL]** Auto-starts daemon via `daemon.StartProcess()` if not running. Not in host spec.

### routeDaemonEvent

- **[COVERED]** host.md "Daemon events": "routes it through the event bus"

---

## internal/tui/effects.go (alias)

- **[COVERED]** Delegates to ui.NewGradientColors — no additional behavior

---

## internal/tui/launch.go

### LaunchAction type

- **[COVERED]** host.md: "LaunchAction set when a plugin requests a session launch"
- **[UNCOVERED-BEHAVIORAL]** `ReturnToTodoID` and `WasResumeJoin` fields for return context. Not in host spec.

### resolveSessionDir

- **[UNCOVERED-BEHAVIORAL]** Searches Claude project directories to find the correct dir for a session ID when resuming. Not in any spec. **Intent question:** Should session dir resolution be spec'd?

### RunClaude

- **[COVERED]** host.md "Interactive launch with InitialPrompt": `--append-system-prompt` and positional prompt
- **[COVERED]** host.md "RunClaude error propagation": returns errors from cmd.Run()
- **[UNCOVERED-BEHAVIORAL]** Worktree creation via `worktree.PrepareWorktree` when `action.Worktree` is true. Partially covered by worktree spec but not by host spec.

### validateLaunchDir

- **[UNCOVERED-BEHAVIORAL]** Constrains external plugin launch dirs to learned paths. Resolves symlinks to prevent traversal. Not in any spec. **Intent question:** Should external plugin sandboxing be spec'd?

---

## internal/tui/notify.go

### SocketPath / StartNotifyListener / SendNotify

- **[COVERED]** host.md "Cross-Instance Notification": PID-scoped socket, newline events, NotifyMsg injection, stale cleanup
- **[COVERED]** cli.md "ccc notify": socket scan, event sending, stale cleanup

---

## internal/tui/onboarding.go

- **[COVERED]** onboarding.md comprehensively covers all steps (0-3), sub-flows, keys, transitions
- **[UNCOVERED-BEHAVIORAL]** Skills installation during Step 3 (`skillsInstalling`, `skillsResult`). Not in onboarding spec. **Intent question:** Should skill installation be added to onboarding spec Step 3?
- **[UNCOVERED-BEHAVIORAL]** Shell hook detection (`shellHookInstalled`). Not in onboarding spec.

---

## internal/tui/styles.go (alias)

- **[COVERED]** Delegates to ui.NewStyles — no additional behavior

---

## internal/tui/stub_plugin.go

### stubPlugin

- **[UNCOVERED-BEHAVIORAL]** Shows "Restart CCC to activate this plugin" for disabled external plugins. Not in host spec.

---

## internal/ui/effects.go

### TickMsg / TickCmd

- **[COVERED]** host.md "Animation": tick-driven animation

### GradientColor / FadeMultiplier / ApplyFade / PulsingPointerStyle

- **[COVERED]** host.md "Animation": "Gradient uses three configurable color stops", "fade-in on startup, pulsing pointer"

### Constants (TickFPS, FadeInFrames, ShimmerSpeed, PulsePeriod)

- **[UNCOVERED-IMPLEMENTATION]** Internal animation parameters, no spec needed

---

## internal/ui/styles.go

### Styles struct / NewStyles / DueStyle

- **[COVERED]** host.md "Colors from palette" — "No hardcoded color constants. All colors derived from config.Palette via NewStyles()"
- **[UNCOVERED-IMPLEMENTATION]** Individual style definitions — implementation detail

### ContentMaxWidth

- **[UNCOVERED-IMPLEMENTATION]** Layout constant (144), no spec needed

---

## internal/ui/text.go

### WrapText / TruncateToWidth / FlattenTitle

- **[UNCOVERED-IMPLEMENTATION]** Text utility functions, no behavioral spec needed

---

## Spec -> Code Direction Gaps

1. **host.md tab table** lists "Threads" tab (commandcenter/threads) but code has no such route — code has "Active", "New Session", "Resume", "Command Center", "PRs". **CONTRADICTS** or outdated spec.
2. **host.md "Cross-Plugin Communication"** says "The host does not hold direct references to specific plugin types" — but code holds `sessionsPlug`, `ccPlug`, `prsPlug`, `settingsPlug` typed fields for daemon client wiring. **CONTRADICTS** (code needs typed references for daemon integration).
3. **host.md** says Esc "exit the TUI" but code implements double-esc pattern. **CONTRADICTS**.

---

## Summary

- **CONTRADICTS: 3** — Tab table outdated (Threads vs PRs/Active), typed plugin refs vs "no direct references", single-esc vs double-esc
- **UNCOVERED-BEHAVIORAL: 19** — Budget widget, daemon connection lifecycle, auto-start, reconnect, flash messages, double-esc, Ctrl+Z/X, Tab plugin-first dispatch, stub plugins, launch dir validation, session dir resolution, FocusMsg repaint, ActionOpenURL, worktree launch, return context, skills install in onboarding, shell hook in onboarding, rebuildTabs, agent runner
- **COVERED: ~20 behavioral paths**
