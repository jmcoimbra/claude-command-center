# SPEC: Command Center Plugin (built-in)

## Purpose

The main productivity hub plugin. Manages todos, calendar events, AI-powered suggestions, and Claude integration. Provides one route: the command center view (calendar + todos).

## Slug: `commandcenter`

## Routes

- `commandcenter` — default view (calendar + todo panels)

## File Organization

| File | Responsibility |
|------|---------------|
| `commandcenter.go` | Main plugin struct, Init, NavigateTo, HandleMessage, Refresh, state management |
| `cc_keys.go` | All key handling: `HandleKey`, sub-handlers for command tab, detail view, rich todo creation, quick todo entry, schedule modal |
| `cc_keys_detail.go` | Detail view key handling: field editing, status/path selection, command input, training input, unmerge |
| `cc_keys_wizard.go` | Task runner wizard: 3-step flow, path picker, AI refinement, review loop, launch |
| `cc_keys_session.go` | Session viewer key handling: scroll, message input, join session |
| `cc_messages.go` | Message handling for async results (Claude responses, refresh finished, DB writes) |
| `cc_view.go` | Command center rendering: calendar panel, todo panel, warnings, suggestions, help overlay, detail view, schedule modal UI |
| `styles.go` | Local style/gradient types populated from `config.Palette` (avoids circular imports with tui) |
| `refresh.go` | Background refresh command (finds and spawns `ai-cron` binary) |
| `claude.go` | Background Claude CLI/LLM commands (edit, enrich, command, focus), prompt builders |
| `calendar.go` | Calendar API interactions: `scheduleBlockCmd` (background tea.Cmd for booking a time block), `releaseBookingsCmd` (background tea.Cmd for deleting calendar events). Uses `calendar.FindFreeSlot` and `calendar.LoadAuth` from `internal/refresh/sources/calendar`. |

**Related refresh files** (in `internal/refresh/`):

| File | Responsibility |
|------|---------------|
| `todo_agent.go` | `GenerateTodoPrompt` — LLM-based project routing and prompt generation for todos |
| `llm.go` | `generateSuggestions`, `generateProposedPrompts`, `loadPathContext` — orchestrates path metadata, skills, and routing rules into LLM calls |

## State

- `cc *db.CommandCenter` — loaded from DB, contains todos, calendar, suggestions
- `ccCursor int` — selected todo index in command tab
- `subView string` — active sub-view (currently only `"command"`)
- `showHelp bool` — help overlay toggle

- `detailView bool` — viewing a single todo's detail with edit input
- `detailNotice string` — transient notice banner in detail view (auto-clears after 1s)
- `addingTodoRich bool` — rich textarea for AI-powered todo creation
- `scheduleModalActive bool` — true when the schedule modal overlay is showing
- `scheduleModalState string` — "picker" (choosing duration) or "booked" (acknowledgment after booking)
- `scheduleModalCursor int` — selected index in the vertical duration list
- `scheduleModalTodoID string` — ID of the todo being scheduled
- `scheduleModalLastBooking string` — acknowledgment text after a successful booking (e.g. "Booked 30m at 2:30pm")
- `ccExpanded bool` — expanded multi-column todo view
- `triageFilter string` — active triage filter tab in expanded view (default: "focus")
- `addingTodoQuick bool` — quick textarea for LLM-enriched todo creation
- `gPending bool` — chord state: `g` was pressed, awaiting second key
- `mergeSourceCursor int` — selected source index in synthesis todo detail view
- `wizardSelections map[string]wizardSelection` — per-todo wizard selections persisted across open/close
- `undoStack []undoEntry` — stack of undo-able todo actions
- `pendingLaunchTodo *db.Todo` — todo awaiting session navigation
- `unstarConfirmMode bool` — true when unstarring a todo that has future calendar bookings; prompts user to release the calendar blocks
- `unstarConfirmTodoID string` — ID of the todo awaiting unstar confirmation

## Key Bindings

### Command Center Tab

| Key | Context | Description |
|-----|---------|-------------|
| `up`/`k` | normal | Move cursor up |
| `down`/`j` | normal | Move cursor down; auto-expands to expanded view when cursor would move past the last visible item (cursor lands on the next todo); sets triageFilter to "all" so expanded view shows the same items as the collapsed view |
| `shift+up` | normal | Swap todo with the one above |
| `shift+down` | normal | Swap todo with the one below |
| `left`/`h` | expanded | Move cursor left; paginates to previous page at left edge |
| `right`/`l` | expanded | Move cursor right; paginates to next page at right edge |
| `x` | normal | Complete selected todo (pushes to undo stack) |
| `X` | normal | Dismiss selected todo (pushes to undo stack) |
| `u` | normal | Undo last complete/dismiss |
| `d` | normal | Defer selected todo to bottom of list |
| `p` | normal | Promote selected todo to top of list |
| `space` | normal | Cycle expanded view: collapsed → 2-col → 1-col → collapsed |
| `c` | normal | Create todo via rich textarea (AI-powered) |
| `/` | normal | Search/filter todos (case insensitive) |
| `enter` | search | Open the selected item from the filtered list directly (no intermediate freeze state) |
| `esc` | search | Clear search query and exit search mode |
| `b` | normal | Jump to Backlog tab (expands view if collapsed) |
| `f` | normal | Toggle focus on selected todo (focus = move to top; unfocus clears star+focus) |
| `s` | normal | Toggle star on selected todo (star = starred+focused+accepted; opens schedule modal; unstar checks for calendar bookings) |
| `S` | normal | Open schedule modal for selected todo (auto-stars if not already starred) |
| `r` | normal | Manual refresh (spawns ai-cron); shows "Refreshing..." flash, then "Refreshed" on success |
| `enter` | normal | Open detail view for selected todo |
| `o` | normal | Launch session for todo (by session_id, project_dir, or navigate to sessions) |
| `t` | normal | Quick todo add (opens lightweight textarea, `ctrl+d` submits for LLM enrichment) |
| `g` | normal | Chord prefix for Gmail-style shortcuts (`gi` = go inbox / return to list, `gu` = go up / return to list) |
| `?` | any | Toggle help overlay |
| `tab` | expanded | Cycle triage filter forward |
| `shift+tab` | expanded | Cycle triage filter backward |
| `y` | expanded | Accept selected todo (triage) |
| `Y` | expanded | Accept + open task runner for selected todo |
| `esc` | expanded | Collapse expanded view |
| `esc` | pending launch | Cancel pending launch, return to command view |

### Detail View

Title bar shows "TODO #N" using the todo's `display_id`.

The detail view tracks the todo by **ID** (not list index), so status changes (e.g. cycling active → waiting) don't cause the view to jump to a different todo.

Editable fields are cycled with `tab`/`shift+tab`: Status (0), Due (1), ProjectDir (2). Prompt is not editable in the detail view — it is managed via the task runner wizard.

| Key | Context | Description |
|-----|---------|-------------|
| `tab` | detail:viewing | Cycle to next editable field |
| `shift+tab` | detail:viewing | Cycle to previous editable field |
| `enter` | detail:viewing | Edit selected field (Status opens inline selector with backlog/blocked/completed/dismissed; Due opens text input; ProjectDir opens scrollable path picker) |
| `enter` | detail:editing | Confirm field edit |
| `c` | detail:viewing | Open command input to edit todo via Claude LLM (blocked when agent is active on this todo) |
| `o` | detail:viewing | Join session (if session_id exists and session file is live) or open task runner |
| `r` | detail:viewing | Resume/re-launch agent (skips ResumeID if session expired) |
| `T` | detail:viewing | Train routing/prompt rules (opens training input textarea) |
| `U` | detail:viewing | Unmerge: detach the selected source from a synthesis todo |
| `w` | detail:viewing | Open live session viewer (local or daemon-managed active sessions), or replay saved session log |
| `delete`/`backspace` | detail:viewing | Kill running agent session for this todo |
| `g` | detail:viewing | Chord prefix for Gmail-style shortcuts (`gi`/`gu` = return to list view) |
| `up`/`down` | detail:viewing | Scroll detail viewport |
| `pgup`/`pgdown` | detail:viewing | Half-page scroll detail viewport |
| `j` | detail:viewing | Navigate to next active todo (resets viewport scroll to top) |
| `k` | detail:viewing | Navigate to previous active todo (resets viewport scroll to top) |
| `]` | detail:viewing | Navigate to next source in synthesis todo |
| `[` | detail:viewing | Navigate to previous source in synthesis todo |
| `x` | detail:viewing | Complete todo (shows notice banner, auto-advances after 1s) |
| `X` | detail:viewing | Dismiss todo (shows notice banner, auto-advances after 1s) |
| `esc` | detail:viewing | Return to list |
| `esc` | detail:editing | Cancel field edit |

While a notice banner is showing (1s after complete/dismiss), all keys except `esc` are blocked. After the notice clears, the view auto-advances to the next active todo. Auto-advance uses the position of the just-completed/dismissed todo in the filtered list (not `ccCursor`, which may be stale if the user navigated with `j`/`k` in detail view). When the completed/dismissed todo was the last in the list, the cursor moves to the new last item.

### Rich Todo Creation

| Key | Context | Description |
|-----|---------|-------------|
| `ctrl+d` | rich | Submit text to Claude for processing |
| `esc` | rich | Cancel and return to list |

### Quick Todo Entry

| Key | Context | Description |
|-----|---------|-------------|
| `ctrl+d` | quick | Submit text to LLM for enrichment (title, due, context, dedup detection) |
| `esc` | quick | Cancel and return to list |

Quick todo entry (`t`) opens a lightweight textarea. On submit, the text is sent to the LLM via `buildEnrichPrompt` which enriches the raw text into structured fields (title, due, who_waiting, effort, context, detail, project_dir, proposed_prompt). The LLM also checks for duplicates by returning a `merge_into` field — if a match is found, synthesis is triggered automatically. Todos created this way enter as `backlog` status directly (skip `new`).

### Schedule Modal

A centered modal overlay rendered on top of the todo list/detail view. Replaces the old schedule offer flash message and horizontal booking picker with a vertical list of time slots.

| Key | Context | Description |
|-----|---------|-------------|
| `up`/`k` | picker | Select shorter duration |
| `down`/`j` | picker | Select longer duration |
| `enter` | picker | Confirm booking and create calendar event |
| `esc` | picker | Dismiss modal (todo stays starred, no booking) |
| `S` | booked | Schedule another block (returns to picker) |
| `esc` | booked | Dismiss modal |

**States:**

- **picker** — vertical list of durations (15m, 30m, 1h, 2h, 4h) with cursor navigation. Default cursor position is index 2 (1h).
- **booked** — acknowledgment after a successful booking. Shows "Booked Xm at Y:YYpm" and offers S to schedule another block or Escape to dismiss.

**Entry points:**

- `s` key (star toggle) on an unstarred todo — stars the todo, then opens the schedule modal in picker state
- `S` key from command tab or detail view — opens the schedule modal (auto-stars if not already starred)

## Event Bus

- Publishes: `todo.completed`, `todo.dismissed`, `todo.deferred`, `todo.promoted`, `pending.todo`
- Subscribes to lifecycle messages: `TabViewMsg`, `ReturnMsg`, `NotifyMsg`, `LaunchMsg`

## Migrations

Two plugin-owned migrations:

1. `CREATE INDEX IF NOT EXISTS idx_cc_todos_status_sort ON cc_todos(status, sort_order)` — speeds up filtered todo queries
2. `ALTER TABLE cc_todos ADD COLUMN session_log_path TEXT` — stores the log file path for agent sessions

### Display IDs

Todos have a `display_id` column (auto-incrementing integer) for stable, human-readable references. Used in the detail view title ("TODO #N") and anywhere a short identifier is needed.

## Behavior

### Schedule Modal

When a todo is starred (via `s` key on an unstarred todo), it is also auto-accepted (status transitions from "new" to "backlog" via `AcceptTodo`). Then the schedule modal opens immediately in **picker** state, showing a centered overlay with a vertical list of time slot durations.

The `S` key from the command tab or detail view also opens the schedule modal (auto-starring the todo if not already starred).

**Picker state:**

- Vertical list: 15m, 30m, 1h, 2h, 4h
- Cursor starts at index 2 (1h)
- `j`/`down` moves cursor down, `k`/`up` moves cursor up
- `enter` triggers `scheduleBlockCmd` to create a calendar event
- `esc` dismisses modal without booking (todo stays starred)

**Booked state (after successful booking):**

- Shows acknowledgment: "Booked Xm at Y:YYpm"
- `S` returns to picker state for scheduling another block
- `esc` dismisses the modal

**Rendering:**

The modal is a centered lipgloss box with a border, overlaid on the existing view. It renders in `viewCommandTab` before returning the final view string. The modal intercepts all key input when active (checked early in `HandleKey`).

### Unstar Confirm Mode

When a user unstarring a todo has future calendar bookings (detected via `DBGetFutureBookingsForTodo`), `unstarConfirmMode` is set to `true` and `unstarConfirmTodoID` is set to the todo's ID. A flash message appears:

- **1 event**: `Release calendar block? (y/n)`
- **N events**: `Release N calendar blocks? (y/n)`

Responses:

- `y` — deletes the future Google Calendar events via the Calendar API (`releaseBookingsCmd`), removes the booking records from `cc_todo_bookings`, then unsets `starred` on the todo
- `n` — unsets `starred` on the todo but leaves calendar events in place (booking records remain)
- Any other key — cancels the confirmation; the todo stays starred and `unstarConfirmMode` is cleared

### Command Center View

1. Left panel: calendar (today's events with times, colors from config)
   - Each timed event renders on a single line: connector, time, title, and duration
   - The content width passed to calendar rendering must account for the panel border's horizontal frame size (border + padding) so that event lines fit within the panel without wrapping
2. Right panel: todos sorted by sort_order, with status indicators
3. Focus suggestion banner at top when available
4. Warning bar when data is stale or services are unreachable
5. Help overlay toggled with `?`
6. Expanded multi-column view when scrolling past visible todos. Rows per column use `(viewHeight - 6) / 2` where `viewHeight = height - 14` (TUI chrome) and 6 accounts for expanded-view chrome (header, tabBar, 2 blanks, hints, footer). This ensures the 2-column layout never overflows the terminal height. Left/right arrows paginate when at column edges. A triage filter tab bar appears below the header.

### Todo Lifecycle

- Create via `c` (rich textarea, `ctrl+d` submits to Claude LLM for structured todo creation)
- Complete with `x` (moves to completed, undo with `u`)
- Dismiss with `X` (tombstoned, never recreated by refresh)
- Defer with `d` (moves to bottom of list)
- Promote with `p` (moves to top of list)
- Expanded view with `space` (cycles: collapsed → 2-col → 1-col → collapsed)
- Launch with `enter` (resumes session_id, launches in project_dir, or navigates to sessions)

### Todo Status Model

Todos use a single `Status` field representing a finite state machine. This replaced an earlier three-field model (`Status`/`TriageStatus`/`SessionStatus`).

#### Status Values

| State | Meaning | Set by |
|-------|---------|--------|
| `new` | Extracted by refresh, awaiting triage | System (refresh) |
| `backlog` | Accepted, not being worked on | User (triage accept, manual create, reopen) |
| `enqueued` | Waiting for an agent slot | System (agent queue) |
| `running` | Agent actively working | System (agent runner) |
| `blocked` | Agent needs human input | System (agent detects blocking event) |
| `review` | Agent finished successfully, needs human review | System (agent exit 0) |
| `failed` | Agent finished with error, needs human review | System (agent exit != 0) |
| `completed` | Done | User |
| `dismissed` | Discarded / not relevant | User |

Manual todos created via `t` enter as `backlog` directly (skip `new`).

#### Filter Tabs (Expanded View)

When the expanded multi-column view is active, a tab bar appears below the header showing filter categories:

| Tab | Shows |
|-----|-------|
| Focus | todos where `Focus == true` |
| New | `new` status items (formerly "Inbox") |
| Backlog | `completed`, `dismissed` items (terminal states) |
| Agents | `enqueued`, `running`, `blocked` |
| Review | `review`, `failed` |
| All | all non-terminal (everything except `completed`, `dismissed`) |

- **Tab order**: Focus, New, Backlog, Agents, Review, All
- **Default tab**: Focus
- Pressing `space` from collapsed view expands into the todo list and lands on the Focus tab
- `tab` cycles filter forward, `shift+tab` cycles backward
- Switching tabs resets cursor and scroll offset to 0
- `b` key is a shortcut that expands the view (if collapsed) and jumps directly to the Backlog tab

#### Normal View Behavior

In the normal (collapsed) view:

- **Todo list** shows only starred todos (`Starred == true`); sorted starred-first within results
- **Nudge message** "No starred items. Press space to expand, f to focus, s to star." renders when no starred todos exist (replaces empty-list message)
- **Triage status bar** appears below the todo list showing counts per tab — only displayed if any count is non-zero

#### Star Indicators

Todos render a star prefix character in both collapsed and expanded views:

- **Starred** (`Starred == true`): yellow `★ ` prefix
- **Focused-not-starred** (`Focus == true` and `Starred == false`): gray `☆ ` prefix
- **Neither**: no prefix (2 spaces of padding to align with starred items)

Title max-width is reduced by 2 to account for the star prefix character. Both collapsed and expanded views compute the available title width dynamically based on the display-ID digit count, pointer width, separator, and star prefix to prevent line overflow.

#### Triage Actions

- `y` accepts the selected todo (sets status to `backlog`, persists to DB)
- `Y` accepts the selected todo AND opens the task runner (detail view)
- **Launching an agent** automatically accepts the todo (moves from `new` to agent lifecycle)

### Claude Integration

- `c` key opens rich textarea; `ctrl+d` submits text to Claude LLM for todo creation
- **Command LLM delegation:** When the command LLM determines an instruction requires external data (Granola transcripts, Slack messages, emails, files, GitHub PRs) or real work it cannot perform, it returns a `delegate` field. The handler creates a todo from the delegate prompt, sets its detail and project directory, and launches an agent session to do the real work. Ask takes priority over delegation; both delegation and simple todos can be processed in the same response.
- `space` on todo opens detail view with edit input for Claude-powered enrichment
- Focus suggestion is always visible — never renders as empty:
  - Auto-generates on data load when focus is empty (first launch, DB clear, post-refresh)
  - Auto-refreshes after todo mutations
  - When zero active todos: sends calendar context to LLM for a witty, surprising remark about the empty list
  - When active todos exist: generates LLM-based recommendation considering deadlines, who's waiting, calendar gaps, effort, and momentum
- All Claude calls run as background `tea.Cmd` (non-blocking)
- Uses `LLM` abstraction layer (not direct CLI calls)

### Command LLM Delegation

The command LLM (`c` key) is a stateless text completion with no tool access — it can only reason about the CC state JSON it receives. When a user asks for something that requires external data or tools, the command LLM delegates to a real agent.

#### Allowed Actions (Command LLM)

1. **Create todos** — extract action items from what the user says
2. **Complete todos** — mark existing todos as done
3. **Answer quick questions** — about the current state (calendar, todos, threads)
4. **Calendar actions** — decline/accept events, only when explicitly asked
5. **Slack/Gmail actions** — send messages, only when explicitly asked
6. **Delegate to agent** — if the instruction requires reading external data (Granola transcripts, Slack messages, emails, files, GitHub PRs), performing real work (writing code, sending messages), or anything not answerable from the command center state, set `delegate` with a rewritten prompt for the agent

#### Decision Logic

1. If the user describes something they need to do → create a todo
2. If the user says "done with X" or "finished X" → complete the matching todo
3. If the user explicitly says "decline", "accept", "send", "message" → take that action
4. If the user asks a question about their state → answer from command center data
5. If the instruction requires external data or tools → delegate to an agent
6. Otherwise → create a todo

#### Response Format

```json
{
  "message": "Brief summary of what you did",
  "ask": "",
  "delegate": {
    "prompt": "Rewritten agent prompt with full context",
    "project_dir": ""
  },
  "todos": [],
  "complete_todo_ids": []
}
```

The `delegate.prompt` is a rewritten version of the user's instruction — expanded for clarity, with enough context for a Claude Code agent to execute it. `delegate.project_dir` defaults to empty (agent uses `$HOME`).

#### Delegation Handler

In `handleClaudeCommandFinished`, when `resp.Delegate` is non-empty:

1. Create a todo with title derived from the delegate prompt (first ~60 chars)
2. Set `todo.Detail` to the full delegate prompt
3. Set `todo.ProjectDir` to delegate's project_dir (or `$HOME` if empty)
4. Insert into DB
5. Call `launchOrQueueAgent()` with a `queuedSession` built from the todo
6. Flash message: "Delegating to agent: <truncated title>"

#### Edge Cases

- **Delegation + ask**: If both `delegate` and `ask` are set, prefer `ask` (LLM needs clarification before delegating)
- **Delegation + todos**: Handle both — create the simple todos, then launch the agent for the delegated work
- **Empty delegate prompt**: Ignore — treat as no delegation
- **Daemon not connected**: Flash "Daemon not connected — cannot delegate to agent"

### Data Loading (Lifecycle Messages)

Instead of polling on a timer, the command center uses lifecycle messages to reload data from the DB at the right moments:

- **TabViewMsg:** Reload from DB if stale (>2s since last read)
- **ReturnMsg:** Always reload from DB (returning from a Claude session)
  - **Interactive session return:** When the user returns from any interactive Claude session (new, resume, or join), the associated todo transitions to `"review"` status unconditionally. Daemon-managed headless agents have their own completion path via `agent.finished` events and are not affected by this transition.
- **NotifyMsg:** Reload from DB (cross-instance notifications)

### Cross-Instance Todo Sync

When a user completes (`x`), dismisses (`X`), or changes the status of a todo in one TUI instance, all other running TUI instances are notified immediately so they reload from DB and reflect the change.

- **Mechanism:** After the DB write succeeds, a `"data.refreshed"` notification is sent via `NotifyPeers` (provided in `plugin.Context`). This reaches all other TUI instances through the notify socket system.
- **Receiving side:** Other instances already handle `"data.refreshed"` NotifyMsg by calling `loadCCFromDBCmd()`, which reloads all command center data from the shared SQLite database.
- **Write cooldown:** After any `dbWriteCmd`, a 2-second cooldown suppresses DB reloads triggered by external events. This prevents a race where async DB writes have not landed yet but a reload replaces in-memory state with stale DB data. The next stale check (>2s) picks up the final state.
- **Cooldown applies to ALL reload triggers:** `data.refreshed` NotifyMsg (peer/daemon notifications), `ccRefreshFinishedMsg` (ai-cron completion), and `TabViewMsg` (tab navigation). Any path that calls `loadCCFromDBCmd()` must respect the write cooldown.
- **Scope:** Applies to `detailCompleteTodo`, `detailDismissTodo`, status changes via `commitDetailFieldEdit`, and all star/focus operations.

### Refresh (ai-cron)

- Auto-refresh triggers when data is older than a threshold (tick-based)
- Manual refresh via `r` key — shows "Refreshing..." flash immediately and "Refreshed" flash on success
- Spawns `ai-cron` binary, then reloads from DB; reload respects write cooldown
- Refresh binary located next to running executable, then falls back to PATH
- **Incremental sync**: Granola and Slack sources check `cc_source_sync` for their last successful sync time and skip already-processed meetings/messages, reducing LLM calls
- **Deterministic source_ref (Granola)**: Source refs use `{meeting_id}-{sha256(title)[:8]}` instead of LLM-generated values, making deduplication reliable
- **Merge preserves completed todos**: Refresh merge logic preserves completed todos as-is rather than overwriting them with fresh data

### Chord Keybindings (`g` prefix)

The `g` key sets a `gPending` flag. The next keypress completes the chord:

| Chord | Context | Description |
|-------|---------|-------------|
| `gi` | any view | "Go inbox" — exit detail/task runner/session viewer, return to list view |
| `gu` | any view | "Go up" — same behavior as `gi` |

Any other key after `g` clears `gPending` and falls through to normal key handling. The chord is available in both the command tab and detail view.

### Edit Guards (Agent Active)

When a todo has an active agent (determined by `todo.Status == "running" || todo.Status == "blocked" || todo.Status == "enqueued"` — status-based, not local session), the detail view blocks mutation operations:

- **`enter` (edit field)**: Shows flash message "Todo is being updated by agent" instead of entering edit mode
- **`c` (command input)**: Shows the same flash message instead of opening command input
- **`r` (resume)**: Only available when `!agentActive` (no running session for this todo)

Other non-mutation keys (navigation, `w` watch, `o` join, `x`/`X` complete/dismiss) are not blocked.

### Training Routing Rules (`T`)

From the detail view, pressing `T` opens a training input textarea (`detailMode = "trainingInput"`). The user writes an instruction about how this type of todo should be routed. On `enter`, the instruction is sent to the LLM via `claudeTrainCmd` along with the todo's context.

The LLM returns structured JSON containing:

- `project_dir` — corrected project directory
- `use_for_rules` — routing rules to add (path + rule pairs) indicating when a project should be used
- `not_for_rules` — routing rules indicating when a project should NOT be used
- `prompt_hint` — hint text to improve future prompt generation for this project
- `prompt_hint_project` — which project the hint applies to
- `regenerated_prompt` — an improved proposed_prompt incorporating the training

On success, routing rules are persisted to `routing-rules.yaml`, prompt hints are saved, and the todo's project_dir and proposed_prompt are updated. Flash message shows what was trained (e.g., "Trained: +use_for on my-project, hint on my-project").

### Merge/Synthesis: Duplicate Detection and Display

#### Auto-Detection During Enrichment (Quick Todo `t`)

When creating a todo via quick entry (`t`), the LLM enrichment prompt includes existing active todos and asks the LLM to return a `merge_into` field if the new item is semantically the same as an existing todo. If `merge_into` is non-empty and points to a valid todo:

1. The new todo is inserted into the DB
2. `claudeSynthesizeCmd` is triggered, which calls the synthesis LLM to combine the originals into a single synthesized todo
3. If the merge target is itself a synthesis todo (source = "merge"), all its original sources are gathered and included

#### Auto-Detection During Refresh

During the refresh cycle (`dedupTodos` in `internal/refresh/refresh.go`), the routing LLM can flag todos as duplicates via `merge_into`. Todos flagged as duplicates of the same target are grouped, and `Synthesize` is called to produce a combined synthesis todo. The synthesis todo gets `Source = "merge"` and merge records are stored in `cc_todo_merges`.

#### Display of Merged Items

In the detail view, synthesis todos (where `todo.Source == "merge"`) show a **SOURCES** section listing all original todos with:

- `#display_id — title (source)` for each original
- A cursor (`mergeSourceCursor`) navigable with `]`/`[` within the sources list
- `j`/`k` always navigate between todos in detail view; `]`/`[` navigate sources independently
- Hint bar shows "[/] select source . U unmerge selected"
- `mergeSourceCursor` is reset to 0 each time the detail view is opened (prevents stale cursor from a previously viewed synthesis todo)

#### Unmerge (`U`)

Pressing `U` on a synthesis todo detaches the currently selected source:

1. Sets the merge record as "vetoed" in the DB via `DBSetMergeVetoed`
2. Removes the vetoed merge from `p.cc.Merges` in memory so the view updates immediately
3. Shows a flash message confirming the unmerge: "Unmerged: <source title>"
4. Adjusts `mergeSourceCursor` if it would be out of bounds after removal
5. Counts remaining non-vetoed originals
6. If only 0-1 originals remain, deletes the synthesis todo and its merge records entirely, exits detail view, and shows flash

### Session Viewer

The session viewer is a sub-view of the detail view (`sessionViewerActive = true`) that displays real-time or replayed agent session output.

#### Opening

- **Live daemon session** (`w` when daemon reports agent is active via `AgentStatus` RPC): Initializes the viewer and starts a polling loop that calls `StreamAgentOutput` RPC every 500ms via `listenForDaemonAgentEvents`. The daemon returns the full event history from offset 0, so previously loaded replay events are not cleared — this avoids a brief empty-viewer flash during the initial poll delay.
- **Saved log** (`w` when `todo.SessionLogPath` is set but no active session): Replays events from the saved log file on disk
- **No session**: Shows flash "No active session for this todo"

#### Key Bindings

| Key | Context | Description |
|-----|---------|-------------|
| `j`/`down` | viewer | Scroll down (disables auto-scroll) |
| `k`/`up` | viewer | Scroll up (disables auto-scroll) |
| `G` | viewer | Jump to bottom, re-enable auto-scroll |
| `g` | viewer | Jump to top, disable auto-scroll |
| `c` | viewer | Open message input textarea |
| `o` | viewer | Join session interactively (resume with session_id) |
| `esc` | viewer | Exit viewer, return to detail view |

#### Clarifying Question UX / Sending Messages to Agent

When an agent is blocked (detected via stream-JSON `tool_use` events with `SendUserMessage` or `AskUser`), the user can respond from the session viewer:

1. Press `c` to open the message input textarea
2. Type a response
3. Press `enter` to send — routes through daemon RPC first (`dc.SendAgentInput`), falls back to local `agent.SendUserMessage` which writes to the agent's stdin
4. `esc` cancels the input

The sent message is appended as a user event to the session's event list for display. Empty messages are not sent.

### SIGINT Before Resume (Graceful Agent Handoff)

When joining a session (`o` with `resume_id`), the system gracefully stops any running headless agent that owns that session before launching the interactive resume. In `handleLaunchMsg`:

1. Calls `dc.StopAgent(resumeID)` via daemon RPC to stop the headless agent
2. Waits briefly for the process to exit

This ensures the interactive session finds a consistent session file rather than competing with a still-running headless agent.

### Cross-Plugin Navigation

When a todo has a `project_dir`, pressing enter launches a Claude session there. When a todo has no project_dir, the plugin sets `pendingLaunchTodo` and navigates to the sessions plugin via the host's "navigate" action.

### Agent Sessions

CCC can launch, monitor, and manage headless Claude Code sessions that work on todos in the background. Sessions run as subprocesses with stream-JSON output, allowing CCC to track progress without blocking the UI.

#### Schema Fields on Todo

| Field | Type | Description |
|-------|------|-------------|
| `proposed_prompt` | `string` | The prompt to send to the Claude agent. Editable via task runner wizard. Falls back to `formatTodoContext(todo)` if empty. |
| `session_status` | `string` | Current agent session state. Empty string means no session. |
| `session_summary` | `string` | Summary of agent output after session completes. |
| `session_id` | `string` | Claude session ID for resuming an existing interactive session (predates headless agent sessions). |

#### Session Status Values

| Status | Meaning |
|--------|---------|
| `""` (empty) | No agent session associated with this todo |
| `"queued"` | Session is waiting to launch (concurrency limit reached) |
| `"active"` | Agent is running |
| `"blocked"` | Agent is waiting for user input (detected via stream-JSON tool_use events) |
| `"review"` | Agent finished successfully (exit code 0), output ready for review |
| `"failed"` | Agent exited with non-zero exit code |

#### Session Lifecycle

1. **Launch via daemon**: User presses `enter` in task runner step 3. `launchOrQueueAgent` sends the request to the daemon via `dc.LaunchAgent()`. If the daemon is not connected, shows a flash message. On success, the todo status is set to `"running"` optimistically and persisted to DB. Emits `plugin.AgentStateChangedMsg` so the TUI host immediately refreshes the budget widget.
2. **Auto-accept**: Launching automatically accepts the todo via `DBAcceptTodo`, which only transitions from `"new"` to `"backlog"` (no-op if the todo is already past `"new"`). This prevents a race where `AcceptTodo` could overwrite a `"running"` status back to `"backlog"`.
3. **Launch denied**: If the governed runner denies a launch (budget or rate limit), a `LaunchDeniedMsg` is emitted. The command center handles this by reverting the todo status to `"backlog"` and showing a flash message with the denial reason.
4. **Process start**: The daemon spawns `claude --print --output-format stream-json --verbose [flags] <prompt>` as a subprocess. The session lifecycle is managed entirely by the daemon.
5. **Monitoring**: The daemon's background goroutine reads stdout line-by-line, parsing stream-JSON events. It detects blocking events and broadcasts status changes via events.
6. **Session ID capture**: The daemon broadcasts `agent.session_id` with `{id, session_id}` when the Claude session UUID is captured from stream-json output. The plugin receives this as a `NotifyMsg`, persists `session_id` to the todo in DB and in-memory. This enables session resume (`o`) and console display.
7. **Completion**: When a daemon-managed agent exits, the daemon broadcasts `agent.finished` with `{id, exit_code}`. The plugin receives this as a `NotifyMsg{Event: "agent.finished", Data: ...}`, parses the payload, and calls `onAgentFinished`. This sets status to `"review"` (exit 0) or `"failed"` (non-zero), checks the DB for an agent-authored summary (submitted via `ccc update-todo`), persists status and summary to DB, and emits `plugin.AgentStateChangedMsg` to refresh the budget widget.
8. **Queue drain**: Queue management is handled daemon-side.
9. **Shutdown**: Agent lifecycle is managed by the daemon — no local cleanup needed.

#### Launch Options

Sessions are configured via the task runner wizard (step 3) with three launch modes:

- **Run Claude** (`taskRunnerLaunchCursor == 0`): Launches an interactive Claude session (not a headless agent). The todo prompt is passed via `--append-system-prompt` for persistent context and a short kickoff message ("Execute the task described in your system prompt.") is passed as the positional prompt argument so Claude starts working immediately without waiting for user input.
- **Queue Agent** (`taskRunnerLaunchCursor == 1`): `AutoStart = false` — agent launches immediately if under concurrency limit, otherwise queues without auto-start
- **Run Agent Now** (`taskRunnerLaunchCursor == 2`): `AutoStart = true` — agent launches immediately or queues with auto-start when capacity frees up

#### CLI Flags

The `claude` command is invoked with:

- `--print` — headless mode (no interactive TUI)
- `--output-format stream-json` — structured output for monitoring
- `--verbose` — detailed output
- `--permission-mode <perm>` — if perm is not "default" (options: "plan", "auto")
- `--max-budget-usd <budget>` — if budget >= $0.50
- `--worktree` — if mode is "worktree"

#### Prompt Postscript

The agent prompt is the user's prompt with a postscript appended. The postscript instructs the agent to call `ccc update-todo --id <todo-id> --session-summary` with a structured summary (what was done, key decisions, items needing review, open questions) before shutting down. This lets the agent author its own summary rather than relying on output scraping.

#### Join/Resume Existing Sessions

From the detail view, pressing `o` on a todo with a `session_id` launches an interactive session with `resume_id` (not a headless agent — this resumes a previous interactive Claude session). If no `session_id` exists, the task runner wizard opens instead.

**Expired session detection:** Before attempting to join (`o`) or resume (`r`), the system checks whether the Claude session file still exists on disk (`~/.claude/projects/<project>/<session_id>.jsonl`). Claude garbage-collects old session files, so a valid `session_id` in the database may point to a session that no longer exists.

- **`o` (join):** If the session file is missing, shows a flash message ("Session expired — use r to re-run or c to edit prompt first") instead of launching Claude into an error.
- **`r` (resume as headless agent):** If the session file is missing, drops the `ResumeID` and launches a fresh agent with the existing prompt. Flash message says "re-launched" instead of "resumed" so the user knows it started fresh.

#### Review Completed Sessions

Completed sessions (`session_status == "review"` or `"failed"`) show:

- **In the todo list**: styled status indicator (`● ready for review` in green, or `⏳ queued` in muted)
- **In the detail view**: a session status indicator (`● Session: running`, `● Session: completed`, `● Session: failed`) and a `SESSION SUMMARY` section with markdown rendered as structured content (see Session Summary Markdown Rendering below)
- **In the expanded view triage tabs**: the "Review" and "Blocked" tabs filter todos by `session_status`


#### Detail View Markdown Rendering

The `SESSION SUMMARY`, `DETAIL`, and `PROMPT` sections in the detail view render markdown content as structured TUI content using a simple line-by-line renderer (`ui.RenderMarkdown`). The renderer handles:

- **`## Heading` lines**: Rendered as bold cyan section headers (using `SectionHeader` style). The `## ` prefix is stripped.
- **`- bullet` lines**: Rendered with a `  • ` prefix (indented bullet character). The `- ` prefix is replaced.
- **Inline `` `backtick` `` content**: Rendered with the `DescMuted` style (dimmed). Backtick delimiters are stripped.
- **Plain text lines**: Rendered as-is with default foreground color.
- **Empty lines**: Preserved as-is for paragraph spacing.

All lines are word-wrapped to the available width before markdown interpretation. The raw `##`, `-`, and backtick markers are never visible in the rendered output.

#### Status Indicators in Todo List

| Status | Indicator | Color |
|--------|-----------|-------|
| `active` | `● agent working` | Cyan |
| `blocked` | `● needs input` | Yellow |
| `review` | `● ready for review` | Green |
| `queued` | `⏳ queued` | Muted |

An agent status header line also appears when sessions are running: `"2/10 agents running, 1 queued"`.

#### Concurrency Management

- `cfg.Agent.MaxConcurrent` controls the max number of simultaneous sessions (default 10)
- Concurrency is managed by the daemon, NOT by the plugin directly
- `dc.LaunchAgent(params)` sends the launch request to the daemon
- `dc.ListAgents()` returns active agent count for concurrency checks
- The plugin does NOT have `activeSessions`, `sessionQueue`, or `agentRunner` fields — all agent management is daemon-side

#### Event Bus Integration

- `agent.started` — published when a session begins running
- `agent.queued` — published when a session is added to the queue
- `agent.blocked` — published when stream-JSON detects a blocking event (includes `question` in payload)
- `agent.completed` — published when a session finishes (includes `exit_code` and `status`)

#### Stream-JSON Monitoring

The background goroutine parses each stdout line as JSON. It detects blocking events by checking:

1. Top-level events with `type == "tool_use"` and `name` of `"SendUserMessage"` or `"AskUser"`
2. `type == "assistant"` events containing `content` blocks with tool_use entries matching the same names

When a blocking event is detected, the question text is extracted from `input.message` or `input.question` fields. The daemon broadcasts blocking status changes via events, which the plugin receives as `NotifyMsg`.

### Todo-Agent Prompt Generation Pipeline

During refresh, the system generates `proposed_prompt` values and assigns `project_dir` for eligible todos (active, has a source other than "manual", no prompt yet). This pipeline runs in `generateProposedPrompts` within the refresh cycle.

#### Path Context Assembly (`loadPathContext`)

1. Load all learned paths with descriptions from `cc_learned_paths` via `DBLoadPathsFull`
2. Load routing rules from `~/.config/ccc/routing-rules.yaml` via `LoadRoutingRules`
3. Load global skills from `~/.claude/skills/*/SKILL.md` via `GetGlobalSkills` (cached, 1hr TTL)
4. For each learned path, load project-specific skills from `<path>/.claude/skills/*/SKILL.md` via `GetProjectSkills` (cached, 1hr TTL)
5. Attach routing rules to paths where a match exists
6. Assemble into `PathContext` struct: `Paths []PathWithMeta` + `GlobalSkills []SkillInfo`
7. Errors at any step are logged but not fatal — the pipeline works with partial context

#### Routing Prompt (`buildRoutingPrompt`)

For each eligible todo, builds a prompt containing:

1. **Task section** — todo title, detail, context, source, who_waiting, due date
2. **Available Projects section** — for each path: path, description, project skills (name + description), routing preferences (use_for / not_for)
3. **Global Skills section** — skills available in all projects, with a note not to prefer a project just because it shares global skills
4. **Instructions** — choose best project, generate an actionable prompt in imperative mood, include context, mention who is waiting, suggest what "done" looks like

The LLM returns JSON: `{"project_dir": "...", "proposed_prompt": "...", "reasoning": "..."}`

#### Fallback (Legacy Batch Mode)

If no learned paths exist (empty `PathContext.Paths`), falls back to `generateProposedPromptsLegacy`, which batches all eligible todos into a single LLM call that returns prompt-only results (no project assignment). Returns a map of `{todo_id: prompt_string}`.

#### Types

- `PathContext` — `Paths []PathWithMeta` + `GlobalSkills []SkillInfo`
- `PathWithMeta` — path, description, skills (per-project), routing_rules (optional)
- `TodoPromptResult` — project_dir, proposed_prompt, reasoning

### Task Runner Wizard

The task runner is a 3-step linear wizard for configuring and launching a Claude agent session on a todo. Accessed via `o` from the detail view or `Y` from triage.

#### Steps

1. **Project** (Step 1/3) — Shows the current project directory. `/` opens a scrollable path picker to change it. `enter` accepts and advances. `esc` exits the wizard.
2. **Mode** (Step 2/3) — Shows a reminder of the selected project. Inline mode selector cycles through Normal / Worktree / Sandbox with `←→`. `enter` advances. `esc` goes back to step 1.
3. **Prompt** (Step 3/3) — Shows project + mode reminder. Scrollable prompt viewport (`j/k` to scroll). Launch selector at bottom: `[ Run Claude ] Queue Agent  Run Agent Now` toggled with `←→`. `enter` launches. `esc` goes back to step 2.

#### Defaults

- **Budget**: from `cfg.Agent.DefaultBudget`, falls back to $5
- **Permission**: "auto" (hardcoded for headless agents)
- **Mode**: from `cfg.Agent.DefaultMode`, falls back to "normal"
- **Launch cursor**: 0 (Run Claude)

#### Selection Persistence

Wizard selections (project path cursor and mode) persist across open/close cycles within a session via `wizardSelections map[string]wizardSelection` keyed by todo ID. When reopening the wizard for the same todo:

1. In-memory cache is checked first (`wizardSelections[todo.ID]`)
2. Falls back to `todo.LaunchMode` from the DB (persisted on launch)
3. Falls back to config defaults

Selections are saved to the in-memory cache whenever the wizard is exited via `esc` (at any step) or after launching. The path cursor and mode are also persisted to the DB on launch (`persistProjectDir`, `persistLaunchMode`).

#### Auto-Open Path Picker

When a todo has no `project_dir` and no saved wizard selection exists, the path picker opens automatically on wizard entry (if learned paths are available). This avoids a blank project step for unrouted todos.

#### Key Bindings (Step 3)

| Key | Description |
|-----|-------------|
| `j`/`k` | Scroll prompt viewport |
| `←`/`→` | Cycle launch cursor (Run Claude / Queue Agent / Run Agent Now) |
| `enter` | Launch agent with selected options |
| `e` | Open prompt in external editor |
| `c` | AI prompt refinement (LLM improves prompt clarity and structure) |
| `r`/`p` | Review loop (Plannotator annotation → LLM revision cycle) |
| `esc` | Back to step 2 |

#### AI Prompt Refinement (`c`)

1. Opens an instruction input textarea (`taskRunnerInputting = true`) where the user types guidance for the LLM
2. On `enter`: sends instructions + current prompt to LLM via `claudeRefinePromptWithInstructionCmd`
3. Sets `taskRunnerRefining = true` (shows spinner in UI)
4. On response: updates prompt viewport, persists updated `proposed_prompt` to DB, flashes "Prompt refined", clears spinner
5. On error: flashes error message, clears spinner
6. On empty response: flashes "Refine returned empty result"
7. `esc` cancels the instruction input without triggering refinement

#### Review Loop (`r`)

1. Stores current prompt as clean baseline
2. Opens Plannotator with prompt for user annotation
3. On return:
   - If unchanged → "Prompt approved" flash, done
   - If annotated → sends original + annotated to LLM to address feedback, sets refining spinner
4. On LLM response: updates prompt, stores as new clean baseline, reopens Plannotator (loop continues)
5. Loop repeats until user approves (makes no changes)

#### Path Picker

Reused from previous implementation. `/` opens picker, type to filter, `j/k` or `↑/↓` to navigate, `enter` to select, `esc` to cancel.

## Test Cases

- Slug and tab name are correct
- Routes returns one route
- Init loads command center data from DB
- Navigation (up/down) moves cursor correctly
- Down arrow past last visible item in normal view auto-expands to expanded view with cursor on the next todo
- Auto-expand sets triageFilter to "all" so expanded view preserves the same items visible in collapsed view
- normalMaxVisibleTodos accounts for suggestion/warning banner height so auto-expand triggers at the correct position
- Complete todo updates status and pushes undo entry
- Dismiss todo (X) updates status and pushes undo entry
- Undo (u) restores previous state from undo stack
- Create todo (c) enters rich mode
- Enter on todo with session_id returns launch action with resume_id
- Enter on todo with project_dir returns launch action
- Enter on todo without project_dir navigates to sessions
- Defer (d) moves todo to bottom
- Promote (p) moves todo to top
- Shift+up/down swaps todo with neighbor, persists via DB sort_order swap (transaction-based)
- Toggle backlog (b) switches the primary todo list between active and completed/dismissed items; when active, `filteredTodos()` returns `CompletedTodos()` and the panel header shows "BACKLOG (N completed)" instead of "TODOS (N active)"; pressing `b` again returns to the active view; cursor resets to 0 on toggle
- Booking mode enter/exit and duration selection
- Calendar event line (time + title + duration) fits on a single line within the panel border — duration does not wrap to a new line
- View renders without panic (with and without data)
- Help overlay toggles on `?` and renders KEYBOARD SHORTCUTS content; returns ConsumedAction so the host does not apply fallback key handling
- Help overlay dismisses on any subsequent key press and restores the previous view
- Help overlay lists `f` (Toggle focus), `s` (Toggle star), and `S` (Schedule calendar block) key bindings
- Completing a todo (`x` or detail `x`) clears `Starred` and `Focus` fields in memory immediately (in addition to DB)
- Dismissing a todo (`X` or detail `X`) clears `Starred` and `Focus` fields in memory immediately (in addition to DB)
- Unstar confirm `y` dispatches `releaseBookingsCmd` to delete Google Calendar events for the todo, then unsets `starred` in DB
- Unstar confirm `n` unstars the todo in DB without touching calendar events
- `f`, `s` operations call `notifyPeersCmd("data.refreshed")` for cross-instance sync
- handleRefreshFinished respects write cooldown — skips DB reload if a dbWriteCmd was issued within last 2 seconds (prevents star/focus loss from stale reload after ai-cron completes)
- Starred todos sort before non-starred todos within any filtered view
- Starring an inbox item (status "new") auto-accepts it via AcceptTodo, moving it from Inbox to Todo tab
- Star prefix width (2 chars) is included in title max-width calculation for both collapsed and expanded views — prevents line overflow and visual duplication (BUG-136)
- Collapsed view computes title max-width per-item based on display-ID digit count
- HandleMessage processes async results
- Expanded view navigation (left/right columns)
- Expanded view left/right paginates at column edges
- Expanded 2-column view clamps content to terminal height — total rendered lines never exceed viewHeight (prevents header/hint bar from being pushed off screen)
- Detail view shows "TODO #N" title with display_id
- Detail view tracks todo by ID (not index) — status changes don't jump to different todo
- Detail view `enter` edits selected field (Status opens inline selector with backlog/blocked/completed/dismissed, Due opens text input, ProjectDir opens path picker)
- Detail view `x` completes todo with notice banner, auto-advances after 1s, and notifies peer instances
- Detail view `X` dismisses todo with notice banner, auto-advances after 1s, and notifies peer instances
- Completing/dismissing/undoing a todo issues `tea.ClearScreen` to force a full repaint (prevents ghost artifacts from bubbletea's differential renderer when list size changes)
- Completing/dismissing a todo clamps `ccExpandedOffset` so the expanded view doesn't show a stale page
- Detail view `x`/`X` after `j`/`k` navigation advances to the correct next item (not index 0)
- Detail view `j`/`k` navigates between active todos and resets the viewport scroll position to the top
- Detail view blocks keys (except esc) while notice banner is showing
- Granola/Slack incremental sync skips already-processed items via `cc_source_sync`
- Granola source_ref is deterministic (`{meeting_id}-{sha256(title)[:8]}`)
- Refresh merge preserves completed todos
- `DBSwapPathOrder` and `DBSwapTodoOrder` use transactions for atomicity
- Triage: refresh-created todos default to status "new"
- Triage: manually created todos default to status "backlog"
- Triage: normal view shows all non-terminal, non-inbox todos (backlog, running, enqueued, blocked, review, failed)
- Triage: tab/shift+tab cycles filter tab in expanded view
- Triage: y accepts a todo (sets status to "backlog"), Y accepts + opens task runner
- Triage: launching agent auto-accepts the todo (moves from "new" to agent lifecycle)
- Triage: refresh merge preserves existing status
- Task runner wizard: enter advances steps (1→2→3), esc goes back (3→2→1)
- Task runner wizard: esc at step 1 exits wizard
- Task runner wizard: left/right cycles mode in step 2
- Task runner wizard: enter at step 3 launches with Run Claude (cursor 0), Queue Agent (cursor 1), or Run Agent Now (cursor 2)
- Task runner wizard: `c` sets refining state, LLM response updates prompt
- Task runner wizard: `r` opens review loop, unchanged prompt = approved
- Task runner wizard: `r` annotated prompt triggers LLM revision and reopens Plannotator
- Agent sessions: launching sets status to "running", persists to DB immediately, and auto-accepts the todo
- Agent sessions: launch/queue/finish/kill emits AgentStateChangedMsg to refresh budget widget
- Agent sessions: queuing sets status to "enqueued" when at max concurrency, persists to DB
- Agent sessions: launch denied by budget/rate limit reverts status to "backlog" with flash message
- Agent sessions: stream-JSON blocking event sets session_status to "blocked" with question text
- Agent sessions: successful completion (exit 0) sets session_status to "review" with summary
- Agent sessions: failed completion (non-zero exit) sets session_status to "failed" with summary
- Agent sessions: daemon agent.finished event triggers onAgentFinished
- Agent sessions: daemon agent.session_id event persists session ID to todo
- Agent sessions: queue management is daemon-side
- Agent sessions: `o` on todo with session_id returns launch action with resume_id
- Agent sessions: `o` on todo without session_id opens task runner wizard
- Agent sessions: daemon not connected shows flash message on launch attempt
- Agent sessions: status indicators render correctly in todo list (active/blocked/review/queued)
- Agent sessions: detail view shows session status and summary sections
- Agent sessions: triage "Review" tab filters todos with session_status "review"
- Agent sessions: triage "Blocked" tab filters todos with session_status "blocked"
- Agent sessions: triage "Active" tab filters todos with session_status "active"
- Agent sessions: normal view includes todos with agent statuses (running, enqueued, blocked, review, failed) alongside backlog
- Agent sessions: DBAcceptTodo only transitions from "new" to "backlog" (no-op when status already advanced)
- Agent sessions: concurrency respects cfg.Agent.MaxConcurrent (default 10)
- Todo-agent pipeline: eligible todos are active, have a source != "manual", and no proposed_prompt
- Todo-agent pipeline: with learned paths, calls GenerateTodoPrompt per todo (sets project_dir + proposed_prompt)
- Todo-agent pipeline: without learned paths, falls back to legacy batch prompt (prompt-only, no project_dir)
- Todo-agent pipeline: loadPathContext assembles path descriptions, project skills, global skills, and routing rules
- Todo-agent pipeline: partial context failures (missing skills, missing rules) are logged but don't block other paths
- Todo-agent pipeline: LLM parse failure for one todo is logged and skipped, other todos still processed
- Search: `/` activates search mode, typing filters todos
- Search: `enter` in search mode opens the selected item directly (exits search and enters detail view in one keystroke)
- Search: `esc` in search mode clears the query and exits search
- Search: display_id exact match filters correctly
- Focus suggestion: always visible after data load (never empty banner)
- Focus suggestion: zero active todos generates LLM-powered witty remark with calendar context
- Focus suggestion: data load with empty focus triggers generation automatically
- Quick todo (`t`): opens lightweight textarea, `ctrl+d` submits for LLM enrichment
- Quick todo (`t`): empty submit cancels without LLM call
- Quick todo (`t`): enriched todo enters as `backlog` status (skips `new`)
- Quick todo (`t`): LLM merge_into triggers synthesis with existing todo
- Chord `g`: sets gPending, `gi` returns to list from detail/task runner/session viewer
- Chord `g`: sets gPending, `gu` returns to list (same as `gi`)
- Chord `g`: unrecognized second key clears gPending and falls through
- Training (`T`): opens training input in detail view, `enter` submits, `esc` cancels
- Training (`T`): LLM response applies use_for/not_for routing rules and prompt hints
- Training (`T`): updates todo project_dir and proposed_prompt from LLM result
- Unmerge (`U`): detaches selected source from synthesis todo
- Unmerge (`U`): when 0-1 sources remain, deletes synthesis todo entirely
- Unmerge (`U`): no-op when todo is not a synthesis (source != "merge")
- Edit guards: `enter` (edit field) blocked when agent is active, shows flash message
- Edit guards: `c` (command input) blocked when agent is active, shows flash message
- Edit guards: `r` (resume) only available when no active agent session
- Detail view `delete`/`backspace`: kills running agent session, shows flash
- Detail view `delete`/`backspace`: shows "No running agent" when no session active
- Session viewer: `w` opens live viewer for active session, replay for saved log, flash for no session
- Session viewer: `c` opens message input, `enter` sends to agent via daemon or local stdin
- Session viewer: `o` joins session interactively (extracts session_id from log if missing)
- Session viewer: `G` jumps to bottom and re-enables auto-scroll
- SIGINT before resume: joining a session sends SIGINT to headless agent, waits up to 5s
- SIGINT before resume: cleans up finished session before launching interactive resume
- Wizard selection persistence: reopening wizard restores previous path and mode choices
- Wizard selection persistence: `esc` at any step saves selections to in-memory cache
- Wizard auto-open path picker: opens when todo has no project_dir and no saved selection
- AI prompt refinement: opens instruction textarea first, then sends instructions + prompt to LLM
- Merge display: synthesis todos show SOURCES section with navigable source list
- Merge display: ]/[ navigate source list when viewing synthesis todo; j/k navigate between todos
- Merge display: mergeSourceCursor reset to 0 when entering detail view
- Unmerge (`U`): shows flash message confirming unmerge and updates view immediately
- Unmerge (`U`): adjusts mergeSourceCursor when out of bounds after removal
- Merge auto-detection: enrichment LLM returns merge_into for duplicate detection
- Merge auto-detection: refresh dedupTodos groups flagged duplicates and calls Synthesize
- Session summary: `## ` heading lines render as bold cyan headers without raw `##` prefix
- Session summary: `- ` bullet lines render with indented bullet character without raw `- ` prefix
- Session summary: inline backtick content renders with muted style, backtick delimiters stripped
- Session summary: plain text and empty lines preserved as-is
