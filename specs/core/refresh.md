# SPEC: Refresh Package

## Purpose
Fetches data from multiple external sources (Google Calendar, Gmail, GitHub, Slack, Granola), uses LLM extraction to identify action items and commitments, merges fresh data with existing state, and saves the updated command center state to SQLite.

## Interface

- **Input**: `Options` struct configuring the refresh run
  - `Verbose bool` — enable verbose logging
  - `DryRun bool` — print JSON to stdout instead of writing
  - `DB *sql.DB` — open SQLite database connection
  - `Sources []DataSource` — list of data sources to fetch from
  - `LLM llm.LLM` — LLM for extraction and suggestions (haiku)
  - `RoutingLLM llm.LLM` — LLM for routing/validation (sonnet); falls back to `LLM` if nil
  - `ContextRegistry *ContextRegistry` — for fetching source context on todos
- **Output**: `error` (nil on success)
- **Entry point**: `refresh.Run(opts Options) error`
- **Dependencies**: Google OAuth2 tokens (Calendar, Gmail), Slack bot token, Granola stored auth, `gh` CLI, `claude` CLI

### DataSource Interface

See `specs/core/datasource.md` for full details. Each source implements `Name()`, `Enabled()`, and `Fetch(ctx)` methods. Per-source config (CalendarIDs, GitHubRepos, etc.) lives on source structs, not on `Options`. Auth loading happens inside each source's `Fetch()`.

## Behavior

1. Load env vars from `~/.config/ccc/.env` (for cron/non-interactive environments)
2. Load existing state from SQLite via `db.LoadCommandCenterFromDB(opts.DB)`
3. Migrate calendar credentials if needed (one-time)
4. **Parallel data fetch**: Iterate `opts.Sources`; for each enabled source, spawn a goroutine calling `Fetch(ctx)`. Each source loads its own auth; auth failures produce warnings, not fatal errors. LLM extraction for Slack/Granola happens inside `Fetch()`. See `specs/core/todo-extraction.md` for extraction rules.
5. **Combine results**: Merge all `SourceResult` values into a single `FreshData` (calendar from first non-nil, todos/pull requests concatenated)
6. **Merge**: Combine fresh data with existing state preserving IDs, statuses, dismissed items, manual items, and pause states
7. **Execute pending actions**: Process booking requests by creating calendar events in free slots (loads calendar auth independently)
8. **Generate suggestions**: LLM-based priority ranking of todos (if `opts.LLM` is non-nil)
9. **Generate proposed prompts**: Route eligible todos (active, has source, no prompt yet) using `RoutingLLM` (sonnet). The routing step validates ownership — a task is Aaron's if he committed to it OR if someone else assigned it to him by name (see `specs/core/todo-extraction.md`). If the LLM returns `project_dir: "REJECT"`, the todo is auto-dismissed. Otherwise, it assigns a project directory and generates an actionable prompt.
10. **Dedup pass**: After routing, `dedupTodos` processes merge suggestions. For each todo where routing set `merge_into`, the system checks veto history via `WerePreviouslyMergedAndVetoed`, then synthesizes combined todos via LLM (see Todo Synthesis below).
11. **Fetch source context**: For todos with a `source_ref`, fetch raw source content (transcripts, threads, PR comments) via `ContextRegistry` and cache in `source_context`/`source_context_at` columns.
12. **Knowledge extraction** (when knowledge plugin is enabled): For each todo with a populated `source_context` (Granola, Slack, Gmail), call the knowledge extraction function to identify topics, decisions, positions, and open threads. Write artifacts to the knowledge tables. See `specs/core/knowledge-extraction.md`.
13. **Insight analysis** (when knowledge plugin is enabled): Run silence detection and drift detection over the knowledge tables. Write results to `knowledge_surfaced_insights`. Publish `knowledge.insights.updated` event if insights changed. See `specs/core/knowledge-analysis.md`.
14. Save merged state to SQLite via `db.DBSaveRefreshResult(opts.DB, merged)` (or print to stdout if DryRun)

## Types

Types are consolidated in `internal/db/` as the single source of truth. The refresh package imports from `internal/db` rather than maintaining its own duplicated type definitions in `refresh/types.go`.

## Locking

Refresh locking is implemented in `internal/lockfile/lockfile.go`:

- `AcquireLock(stateDir string)` — acquires an advisory file lock via `syscall.Flock()` to prevent concurrent refresh runs. Returns a release function on success, or `ErrAlreadyLocked` if another process holds the lock.
- `IsLocked(stateDir string) bool` — checks whether a refresh is currently in progress (used by TUI to skip spawning refresh if one is already running)

The lockfile lives at `~/.config/ccc/data/refresh.lock` with `0o600` permissions. The flock-based approach is atomic and eliminates the TOCTOU race condition of the previous PID-based implementation.

## Configurable Refresh Interval

The refresh interval is configurable via `config.yaml`:

```yaml
refresh_interval: "10m"  # default: "5m", minimum: "1m"
```

`Config.ParseRefreshInterval()` parses the duration string, returning `DefaultRefreshInterval` (5m) if the string is empty, unparseable, or less than 1 minute.

The CC plugin reads this at `Init()` and uses it for:
- Background auto-refresh timer
- Stale data detection on startup

## Refresh Status Indicator

The CC footer shows refresh status:
- **Normal**: "refreshed Xm ago" (muted)
- **Refreshing**: "refreshing..." with animated dots (cyan)
- **Error**: "refresh failed: ..." (red, truncated to 60 chars)

Fields on Plugin struct: `lastRefreshAt time.Time`, `lastRefreshError string`.

## Auto-Refresh on Startup

During `Init()`, after loading CC from DB, if `GeneratedAt` is older than the configured refresh interval, an auto-refresh is triggered via `StartCmds()`. This handles the common case of launching CCC after machine sleep.

## ai-cron Binary

A standalone binary at `cmd/ai-cron/main.go` provides the CLI entrypoint for refresh.

**Flags:**
- `-v` — verbose logging
- `--dry-run` — print result to stdout instead of writing to DB
- `--no-llm` — skip LLM calls (data-only refresh)

This binary is what crontab invokes on schedule, and what the TUI spawns when the user presses `r`.

## Background Scheduling (crontab)

Background refresh uses crontab instead of launchd. macOS BTM (Background Task Management) tracks `executableModifiedDate` for launch agent binaries — every `make build` recompiles `ai-cron`, changes its mtime, and re-triggers the "Background Items Added" notification. Crontab bypasses BTM entirely.

The schedule is managed via `ccc install-schedule` and `ccc uninstall-schedule` (see `specs/core/cli.md`). Implementation lives in `internal/config/schedule.go`.

**Schedule entry format:**

```
*/N * * * * [ -f ~/.config/ccc/.env ] && . ~/.config/ccc/.env; /path/to/ai-cron >> ~/.config/ccc/data/refresh.log 2>&1 # ai-cron schedule
```

- **Interval**: Derived from `config.refresh_interval`, converted to whole minutes (`*/N`), minimum 1 minute
- **Env sourcing**: The `.env` file is sourced inline since cron does not inherit shell environment variables (needed for `SLACK_BOT_TOKEN`, etc.)
- **Marker comment**: `# ai-cron schedule` identifies CCC entries for idempotent install/uninstall
- **Log file**: Output appended to `~/.config/ccc/data/refresh.log`
- **Legacy cleanup**: Install and uninstall both remove the old launchd plist (`~/Library/LaunchAgents/com.ccc.refresh.plist`) if present

## Security

### Data Sanitization

All external API data is stripped of ANSI escape sequences at the refresh boundary before entering the system. This prevents terminal injection attacks where a malicious calendar event title, PR title, or Slack message could inject OSC sequences or manipulate terminal state. Sanitization uses `internal/sanitize.StripANSI()` (wrapping `ansi.Strip()`).

### API Response Size Limits

HTTP responses from Slack and Granola APIs are read with `io.LimitReader(resp.Body, 10*1024*1024)` (10MB cap) to prevent memory exhaustion from malicious or corrupted responses. Granola additionally decompresses gzip before the limit is applied.

### OAuth Hardening

- **PKCE (S256)**: All OAuth2 flows use Proof Key for Code Exchange (`internal/auth/pkce.go`). The code verifier is generated per flow and included in both the authorization URL and token exchange.
- **Random state parameter**: OAuth state is a 16-byte crypto/rand hex string, validated on callback. Prevents CSRF attacks that could associate an attacker's Google account with the user's CCC.
- **Loopback binding**: The OAuth callback server binds to `127.0.0.1` only (not all interfaces), preventing LAN-based callback interception.

### Lock File

Refresh locking uses `syscall.Flock()` for atomic advisory file locking (`internal/lockfile/lockfile.go`), eliminating the TOCTOU race condition in the previous PID-based approach. The lock file is written with `0o600` permissions.

## Data Sources

| Source | Auth | Data |
|--------|------|------|
| Google Calendar | OAuth2 token from `~/.config/google-calendar-mcp/` | Today/tomorrow events from configured calendar IDs |
| Gmail | OAuth2 token from `~/.gmail-mcp/work.json` | Unread emails from last 3 days |
| GitHub | `gh` CLI auth | Open PRs authored by user, with review comment counts |
| Slack | `SLACK_BOT_TOKEN` env var | Messages with commitment language + thread context |
| Granola | Token from Electron app cache | This week's meetings with transcripts |

## Merge Rules

- **Calendar**: Replaced entirely each refresh
- **Todos**: Matched by `source_ref`; dismissed = tombstone (never recreated); existing items preserve ID/status/created_at while updating title/detail/context; new items get generated IDs; manual items always preserved
- **PullRequests**: Merge-based upsert. Each fresh PR is upserted by ID — GitHub-sourced fields are updated while agent tracking columns (`agent_session_id`, `agent_status`, `agent_category`, `agent_head_sha`, `agent_summary`) are preserved. PRs missing from the fresh batch are archived (`state = "archived"`), not deleted. Archived PRs reappearing are reactivated (`state = "open"`).
- **PendingActions**: Preserved from existing state

## Test Cases

- ANSI escape sequences stripped from external API data (sanitize.StripANSI)
- API responses capped at 10MB via io.LimitReader
- OAuth state parameter is random and validated on callback
- PKCE code verifier/challenge generated per flow and round-trips correctly
- Lock file acquired atomically via flock (concurrent acquisition returns ErrAlreadyLocked)
- Calendar replaced entirely on merge
- Dismissed todo never recreated from fresh data
- Existing todo updated (preserves ID, status, created_at)
- New todo gets generated ID and "new" status
- Manual todos preserved across merges
- Pending actions preserved
- Nil existing state handled gracefully

### Source Context

- `shouldRefresh` returns true when `source_context` is empty
- `shouldRefresh` returns false for TTL=0 fetcher with existing context (immutable)
- `shouldRefresh` returns true when `source_context_at` is older than TTL
- `FetchContextBestEffort` logs errors without propagating them
- `FetchAndSave` persists context to DB and updates in-memory todo

### Knowledge pipeline

- Knowledge extraction runs after source-context fetch when the knowledge plugin is enabled
- Knowledge extraction is skipped when the knowledge plugin is disabled
- Knowledge extraction iterates only todos with populated `source_context` (Granola, Slack, Gmail)
- Knowledge extraction errors for one todo do not block extraction for others
- Insight analysis runs after knowledge extraction when the knowledge plugin is enabled
- Insight analysis is skipped when the knowledge plugin is disabled
- Insight analysis publishes `knowledge.insights.updated` when insights change
- Extraction and analysis run sequentially (analysis depends on extraction output)

### Routing

- Eligible todos: active, has source, not manual, no proposed_prompt
- Rejected todos get `status: "dismissed"` and `proposed_prompt: "REJECTED: ..."`
- `merge_into` from routing triggers dedup pass
- Legacy fallback generates prompts without project assignment when no paths exist

### Todo Synthesis (Dedup)

- Vetoed pair (via `WerePreviouslyMergedAndVetoed`) clears `merge_into` and skips merge
- Veto is pair-specific: vetoing A+B does not prevent A+C
- Synthesis of existing merge target expands to original IDs before re-synthesizing
- Old synthesis todo and merge records are cleaned up when re-synthesizing
- `BuildSynthesisTodo` inherits status from merge target, non-LLM fields from newest original
- Synthesis todo gets `source: "merge"` and a fresh UUID

## Todo Routing Prompt Generation

### Path Context Assembly (`loadPathContext`)

Before generating prompts, the system assembles a `PathContext` struct from multiple sources:

1. **Learned paths**: Loaded via `db.DBLoadPathsFull(database)` — each path has a directory and description
2. **Routing rules**: Loaded via `db.LoadRoutingRules()` from config file — per-path `use_for`, `not_for`, and `prompt_hint` directives
3. **Project skills**: Loaded via `db.GetProjectSkills(path, false)` — per-project skills discovered from disk (cached with 1hr TTL)
4. **Global skills**: Loaded via `db.GetGlobalSkills(false)` — skills available in all projects

If no learned paths exist, falls back to `generateProposedPromptsLegacy` which does batch prompt-only generation without project assignment.

### Routing Prompt Structure (`buildRoutingPrompt`)

The routing prompt sent to sonnet includes these sections:

1. **Task**: Title, detail, context, source, who_waiting, due
2. **Source Context**: If `source_context` is populated, included in `<source_context>` XML tags with source name and fetch timestamp
3. **Available Projects**: Each path with description, project-specific skills (name + description), and routing rules (use_for, not_for, prompt_hint)
4. **Global Skills**: Listed with a note not to prefer projects just because they share global skills
5. **Existing Todos**: Up to 50 active todos listed with display ID, internal ID, title, and due date — enables `merge_into` detection
6. **User Instructions**: Optional `todo_instructions.md` loaded from working directory or `~/.config/ccc/todo_instructions.md`
7. **Instructions**: Ownership validation rules and output format specification

### Routing Result (`TodoPromptResult`)

The LLM returns JSON with:
- `project_dir`: path or `"REJECT"`
- `proposed_prompt`: markdown prompt for agent execution
- `reasoning`: one-sentence explanation
- `merge_into`: existing todo ID if this is a duplicate (optional)
- `merge_note`: reason for merge (optional)

## Todo Synthesis (Dedup)

### Flow (`dedupTodos` in `refresh.go`)

1. Build a lookup map of todos by ID
2. Group todos by their `merge_into` target
3. For each group, check veto history via `WerePreviouslyMergedAndVetoed` — if vetoed, clear the `merge_into` field and skip
4. Collect originals: if the target is already a `source:"merge"` todo, expand to its non-vetoed original IDs via `DBGetOriginalIDs`; otherwise use the target itself. Append the new todos to the originals list.
5. Call `Synthesize` (LLM) to combine all originals into one
6. Call `BuildSynthesisTodo` to create the synthesis todo
7. Insert the synthesis todo into the DB, record merge relationships via `DBInsertMerge`
8. If the target was itself a synthesis, delete the old synthesis and its merge records

### WerePreviouslyMergedAndVetoed

Prevents re-merging a specific pair that was previously split. Groups all merges by `synthesis_id`, then checks if both IDs appear in the same group with at least one vetoed. This is pair-specific — vetoing A from a merge with B does not prevent A from merging with unrelated todo C.

### BuildSynthesisTodo

Creates a new `db.Todo` with:
- **Source**: `"merge"` (identifies synthesized todos)
- **Status**: Inherited from the merge target (preserves triage decisions)
- **LLM fields**: Title, detail, context, who_waiting, due, effort from `SynthesisResult`
- **Non-LLM fields**: `project_dir`, `proposed_prompt`, `source_context`, `source_context_at` from the newest original
- **ID**: Fresh UUID via `db.GenID()`
- **DisplayID**: 0 (DB auto-assigns via `MAX(display_id)+1` on insert)

### Synthesis Prompt

The LLM receives all originals (oldest first) with display ID, title, source, due, who_waiting, effort, and detail. Instructed that the newest entry is the source of truth where information overlaps. Returns a single JSON object with the combined fields.

## Key Changes from AI-RON Original

- Package `refresh` (not `main`); exposes `Run(opts Options) error`
- GitHub repos come from source struct config, not hardcoded
- Calendar supports multiple IDs via CalendarSource config
- Auto-accept is configurable via CalendarSource.AutoAcceptDomains (not hardcoded to @example.com)
- Env file reads from `~/.config/ccc/.env` instead of `~/.airon-env`
- State stored in SQLite (via `internal/db`) instead of `command-center.json`
- DataSource interface replaces hardcoded goroutines — each source owns its auth, enablement, and fetching
- LLM extraction for Slack/Granola happens inside each source's Fetch(), not as a separate phase
- LLM extraction prompts reference speaker labels ([Aaron] / [Other]) for ownership validation
- Two-tier LLM: haiku for cheap extraction, sonnet for routing/validation with rejection capability

## Source Context

Raw source excerpts (transcripts, Slack threads, PR comments, email threads) are cached on todos for use in routing prompts and agent execution.

### ContextFetcher Interface

```go
type ContextFetcher interface {
    FetchContext(sourceRef string) (string, error)
    ContextTTL() time.Duration // 0 = immutable
}
```

### ContextRegistry

Maps source names to `ContextFetcher` implementations. Registered at startup in `ai-cron`.

| Source | TTL | Fetch Strategy |
|--------|-----|---------------|
| Granola | 0 (immutable) | Meeting transcript via `/v1/get-document-transcript` with speaker labels |
| Slack | 24h | +/-24h message window around source message + thread replies |
| GitHub | 24h | PR/issue body + comments via `gh` CLI |
| Gmail | 24h | Full email thread via Gmail API |

### TTL Behavior

`shouldRefresh(todo, fetcher)` determines whether a todo's cached context is stale:

1. If `source_context` is empty, always refresh
2. If `ContextTTL()` returns 0, the content is immutable — never refresh after the first fetch (Granola transcripts)
3. If `source_context_at` is missing or unparseable, refresh
4. Otherwise, refresh only if `time.Since(source_context_at) > TTL`

### FetchContextBestEffort (Fire-and-Forget)

`FetchContextBestEffort` is a wrapper around `FetchAndSave` that logs errors instead of returning them. It is called **sequentially** (not in parallel) for each todo after prompt generation. Errors in one todo do not block context fetching for others.

`FetchAndSave` performs the full cycle: checks TTL, calls `fetcher.FetchContext(sourceRef)`, persists via `db.DBUpdateTodoSourceContext`, and updates the in-memory todo struct.

### Source-Specific Context Fetchers

**Slack** (`sources/slack/context.go`): Parses the `source_ref` permalink (`https://app.slack.com/archives/{channelID}/p{ts}`), extracts channel ID and timestamp, then fetches `conversations.history` in a +/-24h window around the target message (up to 100 messages). For each message, also fetches thread replies via `conversations.replies`. Output includes timestamps: `[{ts}] {text}` with thread replies indented.

**Gmail** (`sources/gmail/context.go`): The `source_ref` is a Gmail message ID. Fetches the message metadata to obtain the `threadId`, then calls `GetThread` to retrieve the full thread. Each message is formatted with Subject/From/To/Date headers and the plain-text body. Messages are joined with `---` separators.

**Granola** (`sources/granola/context.go`): The `source_ref` format is `{meeting_id}-{title_hash}`. The meeting ID is extracted by splitting at the last dash. Fetches the transcript via `granolaGetTranscript` with `[Aaron]:`/`[Other]:` speaker labels.

**GitHub** (`sources/github/context.go`): Fetches PR/issue body and comments via the `gh` CLI.

### Speaker Attribution (Granola)

Granola transcript chunks include a `source` field: `"microphone"` = Aaron, `"system"` = other participants. Transcripts are formatted with `[Aaron]:` and `[Other]:` labels, enabling the LLM to determine who made each commitment. Consecutive chunks from the same speaker are concatenated (no duplicate label). An `[Unknown]:` label is used for unrecognized source values.

### Refresh Integration

After prompt generation, `FetchContextBestEffort` is called for each todo. Context is stored in `source_context` and `source_context_at` columns. The routing prompt includes source context in `<source_context>` tags.

### CLI

`ccc todo --fetch-context <display_id>` — manually fetch and cache source context for a specific todo.
