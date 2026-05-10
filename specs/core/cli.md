# SPEC: CLI Subcommands

## Purpose

CCC provides several CLI subcommands beyond the default TUI launcher for setup, diagnostics, and operational tasks.

## Subcommands

### `ccc` (default)

Launches the TUI dashboard. Requires a working database — exits with error if DB can't be opened.

### `ccc setup`

Interactive setup wizard that walks through:

1. Dashboard name and color palette
2. Calendar credentials check (prints OAuth setup instructions if missing)
3. GitHub CLI authentication check (prints `gh auth login` instructions if missing)
4. Granola configuration check
5. Saves config to `~/.config/ccc/config.yaml`

Loads existing config as defaults if one exists.

### `ccc doctor`

Diagnostic command that checks system health. Prints `[OK]` or `[!!]` per check with actionable fix instructions.

**Checks:**
1. Config file exists and parses
2. Database opens successfully
3. Calendar credentials present and valid
4. GitHub CLI authenticated (`gh auth token`)
5. Granola configured (stored-accounts.json exists)
6. `ai-cron` binary found (next to executable or on PATH)
7. `claude` CLI on PATH
8. Data freshness — warns if `generated_at` > 30 minutes stale

Exit code 0 if all pass, 1 if any fail.

### `ccc install-schedule`

Adds a crontab entry for scheduled background refresh. Uses crontab instead of launchd to avoid macOS "Background Items Added" notifications that re-trigger on every binary rebuild.

- Crontab entry with `# ai-cron schedule` marker comment
- Interval from `config.refresh_interval` (default 5m), converted to cron `*/N * * * *`
- Sources `~/.config/ccc/.env` before running (for API keys in cron environment)
- Logs to `~/.config/ccc/data/refresh.log`
- Cleans up legacy launchd plist (`~/Library/LaunchAgents/com.ccc.refresh.plist`) if present
- Idempotent: skips if identical entry already exists, replaces if entry differs

### `ccc uninstall-schedule`

Removes the ai-cron crontab entry.

- Removes lines containing the `# ai-cron schedule` marker
- Clears crontab entirely if no other entries remain
- Also cleans up legacy launchd plist if present
- No-op if no schedule is installed

### `ccc notify [event]`

Sends a notification event to all running CCC instances, causing them to reload from DB.

- Scans `~/.config/ccc/data/` for `ccc-*.sock` files
- Connects to each unix socket and sends the event string (default: "reload")
- Stale sockets (connection refused) are automatically cleaned up
- Prints count of instances notified
- Useful for external scripts (e.g., after ai-cron runs in launchd)

### `ccc update-todo`

Updates fields on an existing todo. Designed for headless agents to submit structured session summaries.

- Flags: `--id` (required), `--session-summary`, `--session-status`
- `--session-summary -` reads summary text from stdin (for long summaries)
- Calls `tui.SendNotify("reload")` after update so all running CCC instances refresh
- Exits with error if `--id` is empty

### `ccc sessions`

Alias for default (launches TUI).

### `ccc daemon start`

Spawns a detached background daemon process. The daemon handles data refresh on a configurable interval, agent lifecycle management, and session tracking via a unix socket.

- Re-execs the `ccc` binary with `--daemon-internal` in a new session (survives parent exit)
- Writes PID to `~/.config/ccc/daemon.pid`
- Logs to `~/.config/ccc/data/daemon.log`
- Listens on `~/.config/ccc/daemon.sock`
- Refresh interval from `config.daemon.refresh_interval` (default 5m, minimum 1m)
- Errors if daemon is already running (checks PID file + signal 0 liveness)
- Prints PID, socket path, and log path on success

### `ccc daemon stop`

Sends SIGTERM to the running daemon.

- Reads PID from `~/.config/ccc/daemon.pid`
- Sends SIGTERM to the process
- Removes PID file
- No-op message if daemon is not running (cleans up stale PID file)

### `ccc daemon status`

Prints whether the daemon is running and checks socket health.

- Reads PID file and checks process liveness via signal 0
- If running, attempts a ping via the daemon socket
- Reports: `stopped`, `running (PID: N)`, and socket health (`healthy` or `unreachable`)
- Cleans up stale PID files referencing dead processes

### `ccc register`

Registers a Claude session with the daemon for lifecycle tracking.

- Flags: `--session-id` (required), `--pid` (required), `--project` (required), `--worktree-path` (optional)
- Tries daemon socket first; falls back to direct DB insert if daemon is not running
- Inserts a `SessionRecord` with state `active` and current timestamp
- Used by shell hooks when a new Claude session starts

### `ccc update-session`

Updates metadata on a registered session.

- Flags: `--session-id` (required), `--topic` (optional)
- Requires the daemon to be running (no direct-DB fallback)
- Sends update via daemon socket client
- Used by Claude agents to set the session topic for the TUI status line

### `ccc stop-all`

Emergency stop: kills all running agents managed by the daemon.

- Requires the daemon to be running
- Sends stop-all command via daemon socket
- Prints count of agents killed
- Designed for runaway-agent scenarios

### `ccc refresh`

Triggers an immediate data refresh via the daemon.

- Requires the daemon to be running
- Sends refresh command via daemon socket
- Prints confirmation on success

### `ccc add-todo`

Creates a new todo in the Command Center database.

- Flags: `--title` (required), `--source` (default `cli`), `--source-ref`, `--context`, `--detail`, `--who-waiting`, `--project-dir`, `--session-id`, `--due` (YYYY-MM-DD), `--effort`
- Generates a unique ID and sets status to `backlog`
- Sends `reload` notification to all running CCC instances after insert
- Prints the created todo title and ID

### `ccc add-bookmark`

Saves a session bookmark for later resume.

- Flags: `--session-id` (required), `--project` (required), `--repo` (required), `--branch` (required), `--summary` (required), `--label` (optional), `--worktree-path` (optional), `--source-repo` (optional)
- Inserts a bookmark record linked to a session
- Sends `reload` notification to all running CCC instances after insert
- Used by the `/bookmark` or `/wind-down` skills to persist session context

### `ccc todo --get <display_id>`

Retrieves a single todo by its display ID and prints it as JSON.

- `--get` takes an integer display ID (the short numeric ID shown in the TUI, not the UUID)
- Outputs pretty-printed JSON to stdout
- Exits with error if no todo matches

### `ccc todo --fetch-context <display_id>`

Fetches the original source context for a todo (e.g., the Slack message, GitHub issue, or Granola meeting that generated it).

- `--fetch-context` takes an integer display ID
- Builds a context registry with available source fetchers (Granola, GitHub, Slack, Gmail)
- Fetches live context from the original source and saves it to the DB
- Prints the fetched content to stdout
- Times out after 2 minutes

### `ccc paths`

Lists learned project paths. Default output is plain text (one path per line with description if available).

- `--json` — Outputs full JSON with path metadata, per-project skills, global skills, and routing rules
- `--auto-describe` — Generates descriptions for paths that lack one, using LLM if available (falls back to heuristic)
- `--refresh-skills` — Forces skill cache refresh when used with `--json`
- `--add-rule <path>` — Adds routing rules for a project path. Requires at least one of:
  - `--use-for <description>` — Marks the path as suitable for a category of work
  - `--not-for <description>` — Marks the path as unsuitable for a category of work
  - `--prompt-hint <text>` — Sets a prompt generation hint for the path

### `ccc worktrees`

Lists CCC-managed git worktrees across all known project paths.

- Default (no subcommand): scans all learned paths, resolves to git repo roots, deduplicates, and lists worktrees grouped by repo with branch name, relative path, and age
- Prints "No CCC worktrees found" if none exist

### `ccc worktrees prune [path]`

Removes CCC-managed worktrees with interactive confirmation.

- No argument: collects all CCC worktrees across all known repos
- With `[path]`: prunes worktrees only for the specified repo path
- Lists targets and prompts for `y/N` confirmation before removing
- Prints each removed worktree branch on success

### `ccc orchestrator <verb>`

Manages orchestrators — named contexts that coordinate multiple working sessions. The orchestrator subsystem is documented in detail in `specs/core/orchestrator.md`. The CLI verbs below are the surface that the `/orchestrator` skill drives.

All state is file-based under `~/.claude/orchestrators/<name>/`. There is no database or daemon involvement.

The orchestrator name is resolved from the current session topic, which must have the form `ORCHESTRATE: <name>`. Verbs that need an active orchestrator fail with a clear error if the topic is missing or malformed; `list` and `overlap-check` do not require an active orchestrator.

#### `ccc orchestrator init`

Creates or no-ops the orchestrator named by the current session topic.

- Flags: `--project <path>` (optional)
- Idempotent: if `~/.claude/orchestrators/<name>/state.md` already exists with `status: active`, nothing changes
- Otherwise, creates the directory with `log.sh`, empty `transcript.md`, empty `state.log`, and a fresh `state.md` (frontmatter `status: active`, `started_at: <now>`)

#### `ccc orchestrator status`

Prints current state by reading `state.md`.

- Flags: `--json` (optional). When set, emits a structured JSON object instead of the raw markdown.
- Output covers orchestrator name, status, project, started_at, every thread, every open question, recent decisions.

#### `ccc orchestrator thread add`

Adds a thread under `# Threads` in `state.md`.

- Flags: `--name` (required), `--project`, `--branch`, `--worktree`, `--session-id`, `--status` (default `planning`)
- Thread name must be unique within the orchestrator

#### `ccc orchestrator thread set-status`

Updates a thread's `status:` line and appends a `state.log` entry.

- Flags: `--name` (required), `--status` (required), `--reason` (optional)
- Status is freeform text — typical values are `planning`, `in-flight`, `blocked`, `awaiting-user`, `complete`

#### `ccc orchestrator thread complete`

Shorthand for setting a thread's status to `complete`.

- Flags: `--name` (required)

#### `ccc orchestrator decision add`

Appends a timestamped line under `# Decisions` in `state.md`.

- Flags: `--body` (required, or `--body -` to read from stdin), `--thread` (optional)
- Appends a `state.log` entry

#### `ccc orchestrator question add`

Appends an open question under `# Questions` with a fresh `Q<n>` ID.

- Flags: `--body` (required, or `--body -` to read from stdin), `--thread` (optional)

#### `ccc orchestrator question resolve`

Flips a question's `(open)` marker to `(resolved)` and records the resolution timestamp.

- Flags: `--id` (required, e.g. `Q1`), `--note` (optional)
- Fails if no question with that ID exists

#### `ccc orchestrator overlap-check`

Finds non-complete orchestrators whose project or themes overlap with the supplied context. Used by the `/orchestrator` skill on startup before naming a new orchestrator.

- Flags: `--project <path>` (optional), `--themes <comma-separated>` (optional)
- Reads each `~/.claude/orchestrators/*/state.md` and returns matches as JSON: an array of `{name, project, started_at, match_reason}` objects
- Returns an empty array (exit 0) when there are no matches
- Excludes orchestrators whose `state.md` frontmatter has `status: complete`

#### `ccc orchestrator paste-header`

Prints a standardized "PASTE INTO" block for the given thread, suitable for the orchestrator session to surface to the user.

- Flags: `--thread <name>` (required)
- Output format:
  ```
  ─── PASTE INTO: <thread name> ───
    Project:  <project path>
    Worktree: <worktree path or "(none)">
    ccc topic: "<expected topic for the worker session>"
    Verify:   terminal prompt shows that branch before pasting
  ```
- Fails with a clear error if the thread does not exist

#### `ccc orchestrator complete`

Marks the current orchestrator as complete by updating the YAML frontmatter in `state.md`.

- No flags
- Sets `status: complete` and `completed_at: <now>`
- No-op (exit 0 with a message) if the orchestrator is already complete

#### `ccc orchestrator list`

Lists orchestrators by reading directories under `~/.claude/orchestrators/`.

- Flags: `--all` (include completed), `--json`
- Default text output: one line per orchestrator with name, status, project, started_at, thread count

### `ccc help` / `ccc -h` / `ccc --help`

Prints usage information.

## Test Cases

- Doctor: all checks return DoctorCheck with correct OK/fail states
- Doctor: missing config → `[!!]` with "run 'ccc setup'" message
- Doctor: stale data (>30m) → `[!!]` with age warning
- Schedule: crontab entry contains binary path, interval, and marker comment
- Schedule: uninstall with no entry → prints "No schedule installed"
- Schedule: install cleans up legacy launchd plist if present
- Config: ParseRefreshInterval with valid durations
- Config: ParseRefreshInterval with empty/invalid → returns default
- Config: ParseRefreshInterval with <1m → returns default
- Notify: socket path contains PID and ends with .sock
- Notify: socket path respects CCC_STATE_DIR env var
- Notify: SendNotify with no instances returns error
- Notify: SendNotify reaches a listening socket
- Notify: stale socket files are cleaned up on failed connection
- Daemon start: spawns detached process and writes PID file
- Daemon start: errors if daemon already running
- Daemon stop: sends SIGTERM and removes PID file
- Daemon stop: no-op if not running, cleans stale PID file
- Daemon status: reports running/stopped with socket health
- Daemon status: cleans up stale PID file for dead process
- Register: creates session record via daemon socket
- Register: falls back to direct DB insert when daemon not running
- Register: errors if required flags missing
- Update-session: updates topic via daemon socket
- Update-session: errors if daemon not running
- Stop-all: returns count of killed agents
- Stop-all: errors if daemon not running
- Refresh: triggers refresh via daemon socket
- Add-todo: creates todo with generated ID and status `active`
- Add-todo: errors if --title missing
- Add-todo: sends reload notification after insert
- Add-bookmark: creates bookmark with required session/project/repo/branch/summary
- Add-bookmark: errors if any required flag missing
- Add-bookmark: sends reload notification after insert
- Todo --get: returns JSON for valid display_id
- Todo --get: errors for non-existent display_id
- Todo --get: errors for non-integer display_id
- Todo --fetch-context: fetches live source context and prints to stdout
- Todo --fetch-context: times out after 2 minutes
- Paths: plain text listing with descriptions
- Paths --json: includes skills, routing rules, global skills
- Paths --auto-describe: generates descriptions for undescribed paths
- Paths --add-rule: requires at least one of --use-for, --not-for, --prompt-hint
- Worktrees: lists worktrees grouped by repo with age
- Worktrees: prints message if no worktrees found
- Worktrees prune: prompts for confirmation before removing
- Worktrees prune [path]: scopes to single repo
