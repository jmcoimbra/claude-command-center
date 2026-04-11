# Focus & Star: Priority Management for CCC

## Problem

The todo list has ~30 items. There's no way to narrow focus to what matters right now. The user needs a Things-like "Today" concept — a way to commit to a short list and optionally block calendar time for it.

## Design

Two orthogonal layers on top of existing todo statuses:

- **Focus** — "I'm paying attention to this." A curated shortlist (ideally 5-10 items, no hard cap). Gray star indicator.
- **Star** — "I'm doing this now." A subset of focus. Yellow star indicator. Starring offers calendar scheduling.

Starring auto-focuses. Unstarring does NOT remove from focus. These layers are orthogonal to the existing status model (new/backlog/enqueued/running/etc.) — a todo can be `backlog` + focused, or `running` + starred.

## Data Model

### New fields on `db.Todo`

- `focus` (bool) — whether the item is in the focus group
- `starred` (bool) — whether the item is starred (implies `focus = true`)

### New table: `cc_todo_bookings`

Tracks calendar events booked for a todo so they can be cleaned up on unstar.

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | UUID |
| `todo_id` | TEXT NOT NULL | FK to cc_todos |
| `calendar_event_id` | TEXT NOT NULL | Google Calendar event ID |
| `start_time` | DATETIME NOT NULL | Event start |
| `duration_minutes` | INT NOT NULL | Event duration |
| `created_at` | DATETIME NOT NULL | When booked |

## UI Changes

### Home Screen (Collapsed View)

The collapsed command center view changes from showing all non-new todos to showing **only starred items**.

- Starred items display with a yellow star prefix
- When no starred items exist, show a nudge: "No starred items. Press space to expand, f to focus, s to star."
- The calendar panel remains unchanged on the left

### Expanded View

A new **"focus"** triage tab is added to the tab bar:

- **Tab order**: focus, todo, inbox, agents, review, all
- **Focus tab contents**: all items where `focus = true` (includes both starred and non-starred focused items)
- Starred items show yellow star, focused-but-not-starred items show gray star
- The "all" tab continues to show everything non-terminal

### Star/Focus Indicators in All Views

- Yellow star (`★`) before the title for starred items — visible in collapsed view, expanded view, and detail view
- Gray star (`☆`) before the title for focused-but-not-starred items — visible in expanded view and detail view (not in collapsed view, since collapsed only shows starred)

## Key Bindings

### Command Center Tab (new/changed)

| Key | Context | Description |
|-----|---------|-------------|
| `f` | normal | Toggle focus on selected todo |
| `s` | normal | Toggle star on selected todo. First star asks "Schedule time?" |
| `S` | normal | Add a schedule block for selected todo (duration picker, books calendar slot) |

The existing `s` (booking mode) is replaced by the star toggle. The existing `b` (toggle backlog) is unchanged.

### Star Toggle (`s`) Flow

**Starring (currently unstarred):**

1. Set `starred = true` and `focus = true` on the todo
2. Persist to DB
3. Enter scheduling offer mode: flash "★ <title> — Schedule time? S = yes, any key = skip"
4. If `S` → enter scheduling flow (duration picker)
5. If any other key → exit offer mode, process key normally
6. If no keypress within 3 seconds → auto-dismiss offer, stay starred without scheduling

**Unstarring (currently starred):**

1. Check for future booked calendar events for this todo
2. If no future events: unstar immediately, flash "Unstarred: <title>"
3. If one future event: ask "Release calendar block?" (y/n)
4. If multiple future events: ask "Release N calendar blocks?" (y/n)
5. If yes → delete future events from Google Calendar, remove booking records, unstar
6. If no → unstar but leave calendar events in place
7. Unstarring does NOT remove focus

### Focus Toggle (`f`) Flow

1. If currently focused: remove focus (and remove star if starred, triggering the unstar cleanup flow)
2. If not focused: set `focus = true`
3. Persist to DB
4. Flash: "Focused: <title>" or "Unfocused: <title>"

### Schedule (`S`) Flow

1. Enter duration picker (reuses existing booking UI: left/right to select duration)
2. On confirm: call `findFreeSlot` to find next available slot
3. Create Google Calendar event with todo title
4. Store event ID in `cc_todo_bookings`
5. Flash: "Booked <duration> for <title> at <time>"
6. If todo is not already starred, star it (scheduling implies starring)

Can be invoked repeatedly — each `S` adds another block.

## Calendar Cleanup on Unstar

When unstarring a todo with booked events:

1. Query `cc_todo_bookings` for this todo
2. Filter to events where `start_time` is in the future
3. If future events exist, prompt:
   - 1 event: "Release calendar block?"
   - N events: "Release N calendar blocks?"
4. On confirm: delete each future event from Google Calendar via API, remove from `cc_todo_bookings`
5. Past events (already happened) are left alone — they represent work done

## Scheduling is Additive

There is no concept of "total effort" or "deadline" in v1. Each scheduling action books one block. The user builds up calendar time incrementally:

- Star something, book 2 hours
- Monday passes, more to do → `S` to book another 2 hours
- Need even more time → `S` again

This keeps the model simple and under user control.

## Calendar API Architecture

Calendar scheduling and cleanup happen **directly from the TUI** in background `tea.Cmd`s — NOT through ai-cron pending actions. This gives instant feedback when booking or releasing calendar blocks.

- **Scheduling:** TUI gets an OAuth token source from config, calls `findFreeSlot` to find an open slot, creates the event via `Events.Insert`, and writes the booking record to `cc_todo_bookings` — all in a single background command. Flash message shows the booked time immediately.
- **Cleanup on unstar:** TUI queries `cc_todo_bookings` for future events, deletes each via `Events.Delete`, removes DB records. Confirmation is instant.
- **`findFreeSlot`:** Relocated from `internal/refresh/sources/calendar/actions.go` to a shared package so both the TUI and ai-cron can use it.
- **Old booking flow removed:** The `cc_pending_actions` booking type and `executePendingActions` in ai-cron are replaced by this direct approach. The pending actions table may remain if used for other action types.

## Interaction with Existing Features

- **Sort order**: Starred items sort to the top within any view. Within starred items, existing sort_order applies.
- **Complete/dismiss**: Completing or dismissing a starred todo unsets star and focus. Does NOT trigger calendar cleanup (you completed the work, the time was well spent).
- **Triage accept (`y`)**: Accepting a new todo does not auto-focus or auto-star it. It moves to backlog as today.
- **Detail view**: Shows focus/star state. Star/focus can be toggled from detail view with same keys.
- **Agent lifecycle**: An agent-managed todo can be focused/starred independently of its agent status.

## What This Does NOT Include

- Multi-day effort spreading ("10 hours by end of month" distributed across days) — future enhancement
- Deadline tracking on focus items — the existing `due` field serves this purpose
- Hard caps on focus/star count — the UI communicates density naturally
- Rescheduling when conflicts arise — user manages via `S` to add blocks as needed
