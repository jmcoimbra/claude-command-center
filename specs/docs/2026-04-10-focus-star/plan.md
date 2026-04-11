# Focus & Star — Implementation Plan

**Goal:** Add a two-tier priority system (focus + star) to the command center, with additive calendar scheduling tied to starred items.

**Design doc:** `specs/docs/2026-04-10-focus-star/brainstorm.md` — Read this first. It contains the full data model, UI behavior, key bindings, and scheduling flow. This plan describes execution order; the design doc describes what to build and why.

**Assumptions and boundaries:**

- In scope: focus/star fields, home screen filter, focus triage tab, star/focus/schedule key bindings, booking tracking table, calendar cleanup on unstar
- Not in scope: multi-day effort spreading, deadline-aware scheduling, hard caps on focus/star counts
- Relies on: existing `findFreeSlot` logic (relocated from `internal/refresh/sources/calendar/actions.go` to a shared package), existing booking duration picker UI, OAuth token source from config
- Architecture decision: Calendar API calls happen directly from the TUI in background `tea.Cmd`s, NOT through ai-cron pending actions. This gives instant feedback for scheduling and unstar cleanup. The existing ai-cron booking flow via `cc_pending_actions` is removed.

## Stages

### Stage 1: Update specs

Update `specs/builtin/command-center.md` to document:

- New `focus` and `starred` fields on Todo
- New `cc_todo_bookings` table
- Changed collapsed view behavior (starred-only home screen)
- New "focus" triage tab in expanded view
- New key bindings: `f` (toggle focus), `s` (toggle star), `S` (schedule block)
- Star toggle flow with scheduling offer
- Unstar flow with calendar cleanup prompt
- Focus toggle flow (unfocusing a starred item triggers unstar cleanup)
- Star/focus indicators (yellow `★` for starred, gray `☆` for focused)
- Interaction with complete/dismiss (clears star+focus, no calendar cleanup)
- **Calendar API architecture:** Scheduling and cleanup happen directly from the TUI in background `tea.Cmd`s (not via ai-cron pending actions). Document: new `calendar.go` file in commandcenter plugin, `findFreeSlot` relocation to shared package, removal of old `cc_pending_actions` booking flow, OAuth token source usage from config.

Also update the key binding table to replace `s` (booking mode) with `s` (star toggle) and add `S` and `f`.

### Stage 2: Write failing tests

Write tests from the updated spec before implementation. Tests must fail first.

**DB layer tests** (`internal/db/db_test.go` or new `internal/db/bookings_test.go`):

- `TestStarTodo` — set starred+focus, verify both fields persist
- `TestUnstarTodo` — unstar, verify starred=false, focus unchanged
- `TestToggleFocus` — toggle focus on/off, verify field
- `TestUnfocusStarredTodo` — unfocus a starred item, verify both cleared
- `TestCompleteDismissClears` — complete/dismiss starred+focused todo, verify both cleared
- `TestInsertBooking` / `TestGetBookings` / `TestDeleteFutureBookings` — CRUD for `cc_todo_bookings`

**View tests** (`internal/builtin/commandcenter/commandcenter_view_test.go`):

- `TestView_CollapsedShowsOnlyStarred` — collapsed view renders only starred items, shows yellow star
- `TestView_CollapsedEmptyNudge` — no starred items shows nudge message
- `TestView_FocusTabShowsFocused` — expanded focus tab shows both starred and focused items
- `TestView_StarIndicators` — yellow star for starred, gray star for focused-but-not-starred
- `TestView_SchedulingOffer` — after starring, flash shows scheduling offer text

**Key handler tests** (`internal/builtin/commandcenter/commandcenter_test.go`):

- `TestStarKey` — `s` on unstarred item sets starred+focus
- `TestUnstarKey` — `s` on starred item triggers unstar flow
- `TestFocusKey` — `f` toggles focus
- `TestScheduleKey` — `S` enters duration picker

### Stage 3: Data model and DB layer

**Depends on:** Stage 2

Add the new fields and table. This is the foundation — everything else builds on it.

**Files to modify:**

- `internal/db/types.go` — add `Focus bool` and `Starred bool` to `Todo` struct
- `internal/db/schema.go` — add migration: `ALTER TABLE cc_todos ADD COLUMN focus INTEGER NOT NULL DEFAULT 0` and `ALTER TABLE cc_todos ADD COLUMN starred INTEGER NOT NULL DEFAULT 0`. Create `cc_todo_bookings` table.
- `internal/db/write.go` — add `DBSetTodoStar`, `DBSetTodoFocus`, `DBInsertBooking`, `DBDeleteFutureBookings`, `DBClearStarAndFocus` (for complete/dismiss)
- `internal/db/read.go` — add `DBGetBookingsForTodo`, update `scanTodo` to read focus/starred columns. Update `DBCompleteTodo`/`DBDismissTodo` to clear star+focus.

**Done when:** DB tests from Stage 2 pass. `make test` green.

### Stage 4: Home screen and focus tab (view layer)

**Depends on:** Stage 3

Change the collapsed view to show only starred items and add the focus triage tab.

**Files to modify:**

- `internal/builtin/commandcenter/commandcenter.go`:
  - `filteredTodos()` — collapsed view: filter to `starred == true` instead of `status != new`
  - `triageCounts()` — add "focus" key counting `focus == true` items
  - `triageFilterOrder` in `cc_keys.go` — prepend "focus" to the tab order
- `internal/builtin/commandcenter/cc_view.go`:
  - `renderTodoPanel` — show yellow `★` prefix for starred items, gray `☆` for focused-but-not-starred
  - `renderCommandCenterView` — when collapsed and no starred items, render nudge message
  - `renderExpandedTodoView` — star indicators in expanded view
  - Detail view — show star/focus indicator in title bar

**Done when:** View tests from Stage 2 pass. Collapsed view shows only starred items. Focus tab filters correctly. Star indicators render in all views.

### Stage 5: Key bindings (star, focus, schedule)

**Depends on:** Stage 4

Wire up the `s`, `S`, and `f` keys. This replaces the existing `s` booking mode.

**Files to modify:**

- `internal/builtin/commandcenter/cc_keys.go`:
  - Replace `s` handler (was booking mode) with star toggle
  - Add `S` handler for scheduling (enters existing duration picker, reused from old booking flow)
  - Add `f` handler for focus toggle
  - Add `scheduleOfferMode` state — after starring, intercepts next keypress for scheduling offer
  - Add `unstarConfirmMode` state — after unstarring with future bookings, intercepts `y`/`n`
- `internal/builtin/commandcenter/commandcenter.go`:
  - Add state fields: `scheduleOfferMode bool`, `unstarConfirmMode bool`, `unstarConfirmTodoID string`
  - Update `Init` hints to reflect new key bindings

### Stage 5b: Direct Calendar API from TUI

**Depends on:** Stage 5

Calendar scheduling and cleanup happen directly from the TUI via background `tea.Cmd`s, not through ai-cron.

**New file: `internal/builtin/commandcenter/calendar.go`**

Owns all Calendar API interactions for the command center plugin:

- `scheduleBlockCmd(todoID, title string, durationMinutes int)` — background `tea.Cmd` that:
  1. Gets OAuth token source from config (same pattern as `internal/refresh/sources/calendar/calendar.go`)
  2. Calls `findFreeSlot` (relocated or imported from calendar package)
  3. Creates Google Calendar event via `Events.Insert`
  4. Writes booking record to `cc_todo_bookings` with the returned event ID
  5. Returns a message with the booked time for flash display
- `releaseBookingsCmd(todoID string)` — background `tea.Cmd` that:
  1. Queries `cc_todo_bookings` for future events for this todo
  2. Deletes each from Google Calendar via `Events.Delete`
  3. Removes records from `cc_todo_bookings`
  4. Returns a message confirming cleanup

**Relocate `findFreeSlot`:** Move from `internal/refresh/sources/calendar/actions.go` to a shared location (either export it from the calendar package, or extract to a small `internal/calendar/` utility package) so both ai-cron and the TUI can use it.

**Remove old booking flow:** The `cc_pending_actions` booking type and `executePendingActions` in ai-cron are no longer needed for scheduling. Remove the pending action insertion from the old `s` key handler. If `cc_pending_actions` is used for other action types, keep the table but remove booking-specific code.

**Done when:** Key handler tests from Stage 2 pass. `S` books a calendar event and shows instant confirmation. Unstar with cleanup deletes future events immediately. No more round-trip through ai-cron for scheduling.

### Stage 6: Integration and polish

**Depends on:** Stage 5

- Update help overlay (`?`) to show new key bindings
- Update complete/dismiss handlers to clear star+focus fields
- Verify cross-instance sync works (star/focus changes trigger `NotifyPeers`)
- Manual testing: star items, schedule blocks, verify calendar events, unstar with cleanup
- Update `filteredTodos` to sort starred items to top within any view

**Done when:** `make test` green. Manual testing confirms full flow works.
