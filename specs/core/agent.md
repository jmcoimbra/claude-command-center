# SPEC: Agent Subsystem

## Purpose

Manages headless Claude Code agent sessions from within CCC. Provides process lifecycle management (launch, kill, queue, monitor), cost tracking with budget enforcement, and rate limiting to prevent runaway automation spend. This subsystem was built in response to a runaway-agent incident (412 concurrent sessions, $1500 burned) and exists to make autonomous agent usage safe and observable.

## Interface

### Inputs

- **Request**: describes an agent to spawn
  - `ID` — unique identifier (e.g., todo ID), used for dedup and cooldown tracking
  - `Prompt` — initial text sent to the agent (via stdin pipe, closed immediately after write, for new sessions; PTY for resume sessions)
  - `ProjectDir` — working directory for the agent process
  - `Worktree` — if true, passes `--worktree` to `claude`
  - `Permission` — permission mode string (`"default"`, `"plan"`, `"auto"`)
  - `Budget` — max USD spend for this session; passed to Claude CLI if >= $0.50; also used for per-session budget enforcement via SIGINT
  - `ResumeID` — if set, resumes an existing Claude session instead of creating a new one
  - `AutoStart` — if true, auto-launch when dequeued
  - `Automation` — which automation triggered this (e.g., `"pr-review"`), used for rate limit scoping
  - `CostCallback` — optional callback invoked with `(inputTokens, outputTokens, costUSD)` on each usage event

### Outputs

- **Tea messages** emitted into the bubbletea event loop:
  - `SessionStartedMsg{ID, Session}` — process launched successfully
  - `SessionFinishedMsg{ID, ExitCode}` — process exited
  - `SessionIDCapturedMsg{ID, SessionID}` — Claude session UUID captured
  - `SessionBlockedMsg{ID, Question}` — agent waiting for user input (SendUserMessage/AskUser tool detected)
  - `SessionEventMsg{ID, Event}` — parsed event from agent stdout (assistant text, tool use, tool result, error, user, system)
  - `SessionEventsDoneMsg{ID}` — event channel closed
  - `LaunchDeniedMsg{ID, Reason}` — launch blocked by budget or rate limit (GovernedRunner only)

### Dependencies

- **Claude CLI** (`claude`) — must be on PATH. New sessions use `-p --verbose --output-format stream-json --session-id UUID [--permission-mode MODE] [--worktree] [--max-budget-usd N]` with stdin pipe. Resume sessions use `--verbose --resume ID` via PTY.
- **SQLite database** (`cc_agent_costs`, `cc_budget_state` tables) — for cost tracking, budget state, and rate limit queries
- **`config.AgentConfig`** — budget limits, rate limit parameters, concurrency cap
- **`github.com/creack/pty`** — PTY allocation for resume sessions (not needed for new sessions)
- **Claude native log files** (`~/.claude/projects/<encoded-path>/<session-id>.jsonl`) — used for event parsing in resume sessions. New sessions read stream-json directly from stdout instead. The encoded path is the project directory with all `/` replaced by `-` (including the leading slash, so `/Users/aaron/project` becomes `-Users-aaron-project`).

## Behavior

### TUI Consumption Model

TUI consumers (plugins) receive agent state exclusively via daemon push events (`agent.started`, `agent.finished`, `agent.session_id`, `agent.cost_updated`), not local runner messages. The command center plugin has no local agent runner — all agent operations go through daemon RPCs. The PR plugin retains a local runner as a fallback but prefers the daemon path when connected.

### Runner (core process lifecycle)

The `Runner` interface is the low-level session manager. `NewRunner(maxConcurrent)` creates a concrete `defaultRunner` (defaults to 10 if maxConcurrent <= 0).

**Launching:**

1. `LaunchOrQueue(req)` is called with a `Request`.
2. Dedup check: if the request ID is already active or queued, returns `(false, nil)` — silently ignored.
3. If under the concurrency limit, launches immediately via `launchSession`. Otherwise, appends to a FIFO queue and returns `(true, nil)`.
4. `launchSession` runs in a `tea.Cmd` goroutine. Behavior differs by session type:

   **New sessions (no ResumeID):**
   - Generates a UUID for the Claude session ID upfront.
   - Builds CLI args: `claude -p --verbose --output-format stream-json --session-id UUID [--permission-mode MODE] [--worktree] [--max-budget-usd N]`.
   - Writes the prompt to an `io.Pipe` stdin and **immediately closes the writer** — `claude -p` reads stdin until EOF before processing, so leaving the pipe open would block the agent forever.
   - `StdinWriter` is nil for new sessions — `-p` mode is non-interactive and cannot accept follow-up messages.
   - Captures stdout via `cmd.StdoutPipe()` for stream-json event parsing.
   - Starts the process via `cmd.Start()` (no PTY needed).
   - Registers the session in the active map.
   - Starts `monitorSessionFromStdout` in a goroutine.
   - Returns `SessionStartedMsg`.

   **Resume sessions (ResumeID set):**
   - Builds CLI args: `claude --verbose --resume ID [--permission-mode MODE] [--worktree]`.
   - Starts the process via PTY (`pty.Start`) — needed for `SendMessage` to write to stdin.
   - Drains PTY stdout to `/dev/null` (events come from the native log, not stdout).
   - Registers the session in the active map.
   - Starts `monitorSessionFromLog` in a goroutine (tails the native log file).
   - Returns `SessionStartedMsg`.

**Monitoring (two modes):**

*Stdout monitoring (new sessions using `-p` mode):*

1. `monitorSessionFromStdout` reads stream-json JSONL directly from the process stdout pipe.
2. Uses a `bufio.Scanner` — no polling, no file creation race. Events arrive as soon as Claude emits them.
3. For each event:
   - Writes to CCC's own session log file and the session output buffer.
   - Parses into `SessionEvent` structs and pushes to `EventsCh` (buffered channel, capacity 64).
   - Detects blocking events: `tool_use` with name `SendUserMessage` or `AskUser` sets session status to `"blocked"`.
   - Extracts token usage and invokes the `CostCallback`.
   - **Per-session budget enforcement:** if cumulative cost exceeds `Budget`, sends `SIGINT` to the process.
4. When stdout closes (process exits), calls `cmd.Wait()`, records exit code, and closes the `done` channel.

*Native log tailing (resume sessions):*

1. `tailNativeLog` polls the Claude native JSONL log file at `~/.claude/projects/<encoded-path>/<session-id>.jsonl`.
2. Waits up to 30 seconds for the file to appear (polling every 200ms).
3. Once open, reads JSONL lines and sends parsed `map[string]interface{}` events to a channel.
4. When no new lines are available, polls every 200ms.
5. `monitorSessionFromLog` consumes these events with the same processing as stdout monitoring (log, parse, detect blocking, track cost, enforce budget).
6. When the process exits, drains remaining log events for up to 2 seconds, then records the exit code and closes the `done` channel.

**Killing:**

- `Kill(id)` removes the session from the active map, closes the PTY (sends SIGHUP to the process group), then calls `Process.Kill()`.

**Shutdown:**

- `Shutdown()` closes all PTYs (SIGHUP), sends SIGINT to all processes, then waits up to 3 seconds per session for exit.

**Queue draining:**

- `DrainQueue()` pops the next queued request if there is capacity. Called by the host on tick.

**Cleanup:**

- `CleanupFinished(id)` removes a finished session from the active map, closes its PTY, and returns the session for summary extraction.

**Other:**

- `CheckProcesses()` polls active sessions for completion (via the `done` channel) and status changes. Returns batched tea messages for finished, blocked, and session-ID-captured events.
- `SendMessage(id, message)` writes to the PTY (resume sessions) or StdinWriter (if available) and resets status from `"blocked"` to `"processing"`. Returns an error for `-p` mode sessions where StdinWriter is nil.
- `Watch(id)` returns a `tea.Cmd` that listens on the session's `EventsCh`.

### GovernedRunner (budget + rate limit enforcement)

`GovernedRunner` wraps a `Runner` and adds pre-launch checks. It implements the `Runner` interface so consumers are unaware of the governance layer.

**`LaunchOrQueue` flow:**

1. **Budget check** — calls `BudgetTracker.CanLaunch(budget)`. If denied, returns `LaunchDeniedMsg`.
2. **Rate limit check** — calls `RateLimiter.CanLaunch(id, automation)`. If denied, returns `LaunchDeniedMsg`.
3. **Record launch** — inserts a cost row via `BudgetTracker.RecordLaunch`, wires a `CostCallback` that calls `RecordCost` on each usage event.
4. **Delegate** — calls `inner.LaunchOrQueue(req)`.
5. If the inner runner queued it (concurrency limit), marks the cost row as "cancelled" via `RecordCancelled` to avoid polluting budget accounting with phantom completed runs.

**`CleanupFinished` flow:**

1. Delegates to the inner runner to get the finished session.
2. Looks up the cost row ID, records duration and exit code via `RecordFinished`.

All other methods delegate directly to the inner runner.

### BudgetTracker

Tracks cumulative agent spend against rolling hourly and daily budget limits, backed by SQLite.

**State:**

- `hourlySpent` / `dailySpent` — cached in memory, refreshed from DB on every cost update.
- `stopped` — emergency stop flag, persisted in `cc_budget_state` table.

**`CanLaunch(budget)`** checks in order:

1. Emergency stop active? Deny.
2. `hourlySpent + budget > HourlyBudget`? Deny.
3. `dailySpent + budget > DailyBudget`? Deny.
4. Otherwise, allow.

**`RecordCost(rowID, inputTokens, outputTokens, costUSD)`:**

- Updates the `cc_agent_costs` row with cumulative cost/token counts.
- Monitors accumulate tokens and cost across all API calls in a session and pass cumulative values.
- Refreshes cached hourly/daily totals from DB.

**`RecordFinished(rowID, durationSec, exitCode)`:**

- Sets `finished_at`, `duration_sec`, `exit_code`, and `status` ("completed" or "failed") on the cost row.
- Does NOT overwrite `cost_usd`, `input_tokens`, or `output_tokens` — those are tracked incrementally by `RecordCost` during execution.
- Refreshes cached totals.

**`RecordCancelled(rowID)`:**

- Marks the cost row with `status = "cancelled"`, `exit_code = 0`, `duration_sec = 0`.
- Used for agents that were queued but never actually ran (e.g., denied by inner runner concurrency limit).
- Refreshes cached totals.

**`EmergencyStop()` / `Resume()`:**

- Toggle the `stopped` flag in memory and persist to `cc_budget_state` with key `"emergency_stop"`.
- Emergency stop survives daemon restarts (loaded from DB on construction).

**Warning levels** (in `Status()`):

- `"critical"` — hourly spend >= 95% of limit
- `"warning"` — hourly spend >= `BudgetWarningPct` (configurable, e.g., 0.80)
- `"none"` — below thresholds

### RateLimiter

Prevents spawn loops via three checks. Fully stateless in memory; all state comes from DB queries, surviving daemon restarts.

**`CanLaunch(agentID, automation)` checks in order:**

1. **Per-automation hourly cap** — counts launches for this `automation` in the last hour. Default cap: 20. Skipped if `automation` is empty.
2. **Per-agent-ID cooldown** — checks time since last launch of this `agentID`. Default cooldown: 15 minutes. Blocks if elapsed time < cooldown.
3. **Failure backoff** — counts failures for this `automation` in the last hour. If > 0, applies exponential backoff: `min(baseSec * 2^(failures-1), maxSec)`. Default base: 60s, max: 3600s (1 hour). Skipped if `automation` is empty.

### Cost Estimation

Token usage is extracted from native log events that have `message.stop_reason` and `message.usage` fields. Cost is estimated from the model name:

- **Opus** (`model` contains "opus"): $15/M input, $75/M output
- **Sonnet** (default): $3/M input, $15/M output

### Session Event Parsing

Events from the native log are parsed into `SessionEvent` structs with types:

- `assistant_text` — text content from assistant messages
- `tool_use` — tool name, input (truncated to 80 chars), tool ID
- `tool_result` — result text, tool ID correlation, error flag
- `error` — error message
- `user` — user message text
- `system` — system messages (subtypes, session ID)

### Session Logging

Each session gets its own JSONL log file at `~/.config/ccc/data/session-logs/<timestamp>_<id>.jsonl`. Contains all parsed native log events plus start/exit markers.

### Database Schema

**`cc_agent_costs`:**

| Column | Type | Description |
|---|---|---|
| `id` | INTEGER PK | Auto-increment row ID |
| `agent_id` | TEXT | Request ID (e.g., todo ID) |
| `automation` | TEXT | Which automation triggered the launch |
| `started_at` | TEXT | ISO timestamp |
| `finished_at` | TEXT | ISO timestamp (NULL while running) |
| `duration_sec` | INTEGER | Wall-clock duration |
| `budget_usd` | REAL | Budgeted amount for this session |
| `cost_usd` | REAL | Actual/estimated cost (updated in real-time) |
| `input_tokens` | INTEGER | Cumulative input tokens |
| `output_tokens` | INTEGER | Cumulative output tokens |
| `cost_source` | TEXT | Always "estimate" currently |
| `exit_code` | INTEGER | Process exit code |
| `status` | TEXT | "running", "completed", "failed", "cancelled" |

Indexed on `started_at` for rolling-window budget queries.

**`cc_budget_state`:**

| Column | Type | Description |
|---|---|---|
| `key` | TEXT PK | State key (e.g., "emergency_stop") |
| `value_num` | REAL | Numeric value |
| `value_text` | TEXT | Text value |
| `updated_at` | TEXT | ISO timestamp |

## Configuration

All settings live under the `agent:` key in `~/.config/ccc/config.yaml` via `config.AgentConfig`:

| Setting | Default | Description |
|---|---|---|
| `max_concurrent` | 10 | Max simultaneous agent sessions |
| `default_budget` | — | Default USD budget per session |
| `default_permission` | — | Default permission mode |
| `hourly_budget` | — | Max spend per rolling hour |
| `daily_budget` | — | Max spend per rolling 24h |
| `budget_warning_pct` | — | Warn at this fraction of hourly budget (e.g., 0.80) |
| `max_launches_per_automation_per_hour` | 20 | Per-automation hourly launch cap |
| `cooldown_minutes` | 15 | Min time between launches of the same agent ID |
| `failure_backoff_base_seconds` | 60 | Initial failure backoff |
| `failure_backoff_max_seconds` | 3600 | Max failure backoff (1 hour) |

## Test Cases

### Happy path

- Launch a new session under concurrency limit: returns `SessionStartedMsg`, process starts via `-p` mode with pipe I/O
- Launch a resume session: returns `SessionStartedMsg`, process starts via PTY with `--resume`
- Launch when at concurrency limit: request is queued, `DrainQueue` returns it when capacity opens
- Budget check passes when spend + request < hourly and daily limits
- Rate limit passes when agent is not in cooldown and automation is under cap
- Cost callback updates token counts and USD in real-time as agent runs
- Session finishes with exit code 0: `SessionFinishedMsg` emitted, cost row marked "completed"
- Summary extraction pulls last assistant text or result text from session output

### Error cases

- PTY start fails (resume session): emits `SessionFinishedMsg` with exit code -1, logs error
- Stdout pipe or process start fails (new session): emits `SessionFinishedMsg` with exit code -1, logs error
- Emergency stop active: `CanLaunch` returns false, `LaunchDeniedMsg` emitted
- Hourly budget exceeded: `CanLaunch` returns false with spend breakdown
- Daily budget exceeded: same pattern
- Automation hits hourly launch cap: denied with count/cap info
- Agent ID in cooldown: denied with remaining time
- Failure backoff active: denied with failure count and remaining backoff time
- Per-session budget exceeded: SIGINT sent to process during monitoring
- Process exits with non-zero code: cost row marked "failed", exit code recorded

### Edge cases

- Duplicate request ID (already active or queued): silently ignored, returns `(false, nil)`
- Native log file does not appear within 30 seconds (resume sessions only): monitoring goroutine exits, session runs blind. New sessions read from stdout and are not affected by native log availability.
- `NativeLogPath` encodes project dir with leading `-` (e.g., `/Users/aaron/project` → `-Users-aaron-project`) matching Claude CLI's actual directory naming; incorrect encoding causes session viewer to show "Waiting for events..." indefinitely (BUG-122)
- Stdin pipe for new sessions must be closed after writing the prompt — `claude -p` reads until EOF. Leaving the pipe open causes the agent to hang with "Waiting for events..." indefinitely (BUG-130)
- Event channel full (64 capacity): events dropped silently (non-blocking send)
- Process exits before `CheckProcesses` runs: `SessionIDCapturedMsg` emitted before `SessionFinishedMsg` to ensure session ID is persisted
- Inner runner queues a governed launch: cost row is immediately cleaned up to avoid phantom budget consumption
- Emergency stop state survives daemon restart (persisted in `cc_budget_state`)
- Rate limiter is fully stateless in memory; survives restarts via DB queries
- Kill closes PTY first (SIGHUP to process group) then calls Process.Kill
- Shutdown sends SIGINT (graceful) not SIGKILL; waits up to 3 seconds per session
