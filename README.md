# Claude Command Center

A terminal dashboard that puts your entire workday in one place — calendar, GitHub, todos, meetings, Slack, and Gmail — with Claude built in as your copilot.

![Dashboard Overview](docs/images/dashboard-overview.png)

## Why CCC?

Most developer tools optimize for a single workflow. CCC aggregates the signals that matter across all your tools into a single terminal interface, then gives you Claude right where you need it.

**Launch Claude where you work.** Start a Claude session from any context — a todo item, a PR review, a meeting follow-up. CCC hands Claude the relevant context and drops you straight into the conversation. When Claude exits, you're back in the dashboard.

**Bookmark and resume sessions.** Never lose track of an in-progress conversation. Bookmark Claude sessions with a keypress and pick them back up later from any terminal window. Sessions remember their working directory, project context, and what spawned them.

**Automated todo list management.** Todos flow in from your connected sources — GitHub issues assigned to you, Slack threads that need follow-up, meeting action items extracted by AI. CCC triages, deduplicates, and routes them so you see a single prioritized list instead of checking five apps.

**Agentic todo resolution.** Select a todo and launch Claude with full context to resolve it. CCC fetches source material (the Slack thread, the GitHub issue, the meeting transcript), builds a prompt, and hands it off. When Claude finishes, CCC updates the todo status automatically.

## Features

### Session Launcher

Launch, bookmark, and resume Claude sessions tied to your projects. CCC learns your project paths and gives you instant access from a fuzzy-searchable list.

![Session Launcher](docs/images/session-launcher.png)

### Command Center

Your unified inbox. Calendar events, todos, GitHub activity, meetings, and messages — all in one scrollable view with keyboard navigation.

![Command Center](docs/images/command-center.png)

### Todo Management

AI-powered todo extraction from meetings, Slack, Gmail, and GitHub. Todos are triaged, deduplicated, and actionable — select one and launch Claude to resolve it.

![Todo Management](docs/images/todo-management.png)

### Settings & Doctor

Configure data sources, check connection health, and manage OAuth tokens from inside the TUI. `ccc doctor --live` validates every integration end-to-end.

![Settings](docs/images/settings.png)

### Automations

Run scheduled headless scripts during `ai-cron` cycles — no UI footprint. Automations hook into the refresh pipeline to act on your data (e.g., auto-accepting calendar invites from trusted domains). A Python SDK is included for writing your own. See [docs/automations.md](docs/automations.md).

### External Plugins

Extend CCC with plugins written in any language. Plugins communicate over JSON-lines via stdin/stdout and get their own tab in the dashboard. See the [pomodoro example](examples/pomodoro) to get started.

![External Plugin](docs/images/external-plugin.png)

### Pull Requests

Dedicated PR tracking tab with GitHub integration. Auto-review agents, ignore/archive workflows, and per-repo filtering.

### Console Overlay

Real-time agent and LLM activity observability. Toggle with `~` to see active agents, cost tracking, and streaming output. Also available as a standalone TUI via `ccc console`.

### Background Daemon

Session registry, agent lifecycle management, budget enforcement, event distribution, and refresh scheduling — all managed by a background daemon that auto-starts with the TUI.

### Orchestrators

A coordination layer for working on multiple things in parallel. An **orchestrator** is a Claude session whose only job is to keep things straight across multiple working sessions — track threads, log decisions, hold open questions, help decide what to focus on next. State lives at `~/.claude/orchestrators/<name>/` with `state.md` as the source of truth. Identity is by session topic (`ORCHESTRATE: <name>`).

Start one with the `/orchestrator` skill in a fresh terminal. From any working session, use `/ask-orchestrator` to copy a structured handoff to the clipboard and paste it into your orchestrator. CLI surface lives under `ccc orchestrator` (init, status, thread CRUD, decision/question logging, overlap-check, paste-header, complete, list). See [`specs/core/orchestrator.md`](specs/core/orchestrator.md) for the full data model.

## Architecture

CCC is two binaries, a daemon, and a plugin system:

- **`ccc`** — The TUI + daemon + CLI subcommands. Built with [bubbletea](https://github.com/charmbracelet/bubbletea).
- **`ai-cron`** — The data fetcher. Runs on a schedule (or manually) to pull data from all connected sources into a local SQLite database.
- **Daemon** — Background process managing sessions, agents, refresh, and event distribution. Communicates via Unix socket JSON-RPC.
- **Plugin system** — Built-in plugins (command center, PRs, sessions, settings) plus external plugins that run as subprocesses speaking JSON-lines.
- **Automations** — Headless scripts executed during refresh cycles for background tasks like auto-accepting calendar invites. Written in Python via the included SDK.

Data flows one way: `ai-cron` writes to SQLite, `ccc` reads from it. The TUI never hits external APIs directly.

## Built-in Connectors

Each connector has its own setup guide with prerequisites, step-by-step configuration, and troubleshooting:

| Connector | What it provides | Setup guide |
|-----------|-----------------|-------------|
| Google Calendar | Today's events in the command center | [docs/sources/calendar.md](docs/sources/calendar.md) |
| GitHub | PRs, issues, review requests, notifications | [docs/sources/github.md](docs/sources/github.md) |
| Gmail | Inbox messages with AI-extracted action items | [docs/sources/gmail.md](docs/sources/gmail.md) |
| Slack | Unread messages and threads needing follow-up | [docs/sources/slack.md](docs/sources/slack.md) |
| Granola | Meeting notes with AI-extracted todos | [docs/sources/granola.md](docs/sources/granola.md) |

All connectors are optional. CCC is useful with zero sources configured — the session launcher and todo system work standalone.

## Prerequisites

- Go 1.24+
- Node.js 18+ (for MCP servers)
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) (for agent features)
- GitHub CLI (`gh`) configured with auth

## Quick Start

```bash
git clone https://github.com/anutron/claude-command-center.git
cd claude-command-center
make build

# Install (symlinks binaries to /usr/local/bin)
make install

# Create config
mkdir -p ~/.config/ccc
cp config.example.yaml ~/.config/ccc/config.yaml
# Edit config.yaml with your settings

# Run
ccc
```

## Build Commands

```bash
make build     # Build ccc + ai-cron binaries
make test      # Run all tests
make install   # Build + symlink binaries + build MCP servers
make servers   # Build MCP servers (gmail)
make clean     # Remove built binaries
```

## Getting Started

See [AGENTS.md](AGENTS.md) for detailed installation and setup instructions. That document is designed to be followed by a Claude agent end-to-end, but works fine for humans too.
