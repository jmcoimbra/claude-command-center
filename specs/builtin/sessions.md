# SPEC: Sessions Plugin (built-in)

## Purpose

Manage sessions, project launching, and worktrees as a single plugin with internal sub-tabs. The nav bar shows one "Sessions" entry; the plugin renders four sub-tabs internally (following the PRs plugin pattern). Users can launch new Claude sessions, browse saved/live sessions, and manage git worktrees.

## Slug: `sessions`

## Routes

- `sessions` — default route, renders the New Session sub-tab
- `sessions/new` — New Session sub-tab (project picker)
- `sessions/saved` — Saved sub-tab (bookmarked sessions, ViewFilterSavedOnly)
- `sessions/recent` — Recent sub-tab (live daemon sessions, ViewFilterLiveOnly)
- `sessions/worktrees` — Worktrees sub-tab

**Legacy aliases** (for backward compatibility):

- `sessions/active` → redirects to `sessions/recent`
- `sessions/resume` → redirects to `sessions/saved`
- `sessions/sessions` → redirects to `sessions/recent`

## Nav Bar

The Sessions plugin registers a **single host tab** in the nav bar with route `sessions`. `Tab`/`Shift-Tab` at the host level skips to the next/previous plugin (Command Center, PRs, etc.) — it does NOT cycle sub-tabs.

Inside the plugin, a sub-tab bar renders at the top, **centered horizontally**: `[1] New Session  [2] Saved  [3] Recent  [4] Worktrees`

## State

- unified *unifiedView (manages live, saved, archived sessions)
- newList (bubbles/list.Model)
- paths []string
- confirming, confirmYes bool
- confirmItem
- subTab int — 0=new, 1=saved, 2=recent, 3=worktrees
- worktreeItems []worktreeItem
- worktreeCursor int
- worktreeWarning string (non-empty = show warning overlay)
- worktreeConfirmAction string ("delete" or "prune")
- worktreeConfirmTarget string
- flashMessage string, flashMessageAt time.Time

## Sessions Tab: Three-Tier Model

The sessions tab displays sessions in three tiers:

| Tier | Source | Visibility | Description |
|------|--------|------------|-------------|
| **Live** | Daemon RPC | Always (main mode) | Running, active, or blocked sessions. Blocked sessions render with a yellow dot indicator and "Blocked" text. |
| **Saved** | `cc_bookmarks` table | Always (main mode) | User-bookmarked sessions |
| **Archived** | `cc_archived_sessions` table | Archive mode only | Auto-saved ended sessions |

### Session Lifecycle

1. Session registers with daemon → appears in **Live**
2. User presses `b` on a live session → also saved to `cc_bookmarks`
3. Session ends in daemon:
   - If bookmarked → moves to **Saved**
   - If not bookmarked → auto-persisted to **Archived**
4. `d` on archived → permanently deletes from DB
5. `d` on saved → removes bookmark

### Deduplication

A session that is both live in the daemon AND bookmarked appears in **Live** only, with a ★ indicator. When it ends, it moves to **Saved**.

### View Modes

The Saved and Recent sub-tabs each have their own view filter. The `A` key (shift-a) toggles archive mode within the current sub-tab. The `a` key (lowercase) archives the selected session.

| Sub-Tab / Mode | Contents | Default |
|----------------|----------|---------|
| **Recent** (main) | Live sessions only | Yes |
| **Recent** (archive) | Archived sessions only | No |
| **Saved** (main) | Saved/bookmarked sessions only | Yes |
| **Saved** (archive) | Archived sessions only | No |

The Recent sub-tab MUST NOT show saved sessions. Saved sessions appear exclusively in the Saved sub-tab.

### Auto-Archiving

When `Refresh()` polls the daemon, it compares the current session list against the previous snapshot. Sessions that were previously running but are now ended (and not bookmarked) are auto-archived to `cc_archived_sessions`. If the daemon is disconnected, no archiving occurs.

**Concurrency model:** `Refresh()` returns a `tea.Cmd` that fetches data (daemon RPC, DB reads, auto-archiving) in a background goroutine and returns it as a `sessionsRefreshMsg`. State is only mutated in `HandleMessage` on the main bubbletea loop, never from background goroutines. This prevents data races between tea.Cmd goroutines and `View()`. Exception: `unifiedView.Refresh()` (the direct method) is called once from `SetDaemonClientFunc()` before the bubbletea loop starts — this is safe because no concurrent access exists at that point.

## Key Bindings

### Sub-Tab Navigation (available from any sub-tab, when not in overlay)

| Key | Description | Promoted |
|-----|-------------|----------|
| 1 | Switch to New Session sub-tab | yes |
| 2 | Switch to Saved sub-tab | yes |
| 3 | Switch to Recent sub-tab | yes |
| 4 | Switch to Worktrees sub-tab | yes |
| left/right | Cycle sub-tabs (wraps around) | yes |
| esc | Back to New Session; from New Session, quit | yes |

`Tab`/`Shift-Tab` are NOT consumed by the plugin — they propagate to the host for switching between top-level plugins.

### Saved / Recent sub-tabs (main mode)

| Key | Description | Promoted |
|-----|-------------|----------|
| enter | Resume selected session | yes |
| b | Bookmark live session → Saved (Recent only) | yes |
| d | Dismiss/remove (tier-dependent) | yes |
| a | Archive selected session (verb action) | yes |
| A | View archive list (toggle archive mode) | yes |
| j/k or up/down | Navigate list | yes |

### Saved / Recent sub-tabs (archive mode)

| Key | Description | Promoted |
|-----|-------------|----------|
| enter | Resume archived session | yes |
| b | Promote to Saved (bookmark) | yes |
| d | Permanently delete | yes |
| A | Return to main mode | yes |
| j/k or up/down | Navigate list | yes |

### New Session sub-tab

| Key | Description | Promoted |
|-----|-------------|----------|
| enter | Launch session in selected path | yes |
| w | Launch in a new worktree (git repos only) | yes |
| shift+up/down | Reorder paths | yes |
| del/backspace | Remove from saved list (with confirmation) | yes |
| up/down | Navigate list | yes |
| type | Filter list | yes |

### Worktrees sub-tab

| Key | Description | Promoted |
|-----|-------------|----------|
| enter | Launch Claude in selected worktree | yes |
| d | Delete selected worktree (with confirmation) | yes |
| p | Prune all worktrees for selected project (with confirmation) | yes |
| up/down/k/j | Navigate worktree list | yes |
| esc | Back to New Session sub-tab | - |

### Confirmation dialogs (delete path)

| Key | Description |
|-----|-------------|
| y | Confirm delete |
| n/esc | Cancel |
| left/right/tab | Toggle yes/no selection |
| enter | Execute currently highlighted choice |

After confirming a deletion (y or enter with yes selected), any active filter text is cleared and the filter is re-applied so the New sub-tab returns to the full unfiltered list.

### Worktree warning overlay (not a git repo)

| Key | Description |
|-----|-------------|
| enter | Launch directly in the directory (without worktree) |
| esc | Cancel, return to list |

### Worktree confirmation overlay (delete/prune)

| Key | Description |
|-----|-------------|
| y | Confirm delete or prune |
| n/esc | Cancel |

## Hint Bar

Each sub-tab displays a hint bar at the bottom:

- **New Session:** `type to filter   enter launch   w worktree   shift+up/down reorder   del remove   esc quit`
- **Saved (main):** `enter resume   b bookmark   d dismiss   j/k navigate   a archive   A view archive`
- **Saved (archive):** `enter resume   b save   d delete   j/k navigate   A back`
- **Recent (main):** `enter resume   b bookmark   d dismiss   j/k navigate   a archive   A view archive`
- **Recent (archive):** `enter resume   b save   d delete   j/k navigate   A back`
- **Worktrees:** `enter launch   d delete   p prune   esc back`
- **Worktree warning:** `⚠ Not a git repository — worktrees require git.` + `[enter] Launch directly in this directory   [esc] Cancel`
- **Delete confirmation:** `Delete worktree <label>?` + `[y] Yes, delete   [n] Cancel`
- **Prune confirmation:** `Remove all worktrees for <project>? (<count> worktrees)` + `[y] Yes, prune all   [n] Cancel`

## Event Bus

- Publishes: `project.selected` with {path, prompt} when user picks a project
- Publishes: `pending.todo.cancel` when user cancels a pending todo launch
- Subscribes: `pending.todo` to set a pending launch context
- Handles `plugin.NotifyMsg` for `data.refreshed`, `session.registered`, `session.updated`, `session.ended` — dispatches async `Refresh()` cmd

## Topic Bridge (filesystem watcher)

The daemon watches `~/.claude/session-topics/` for topic file changes every 3 seconds. Claude Code writes topic text to `{session-uuid}.txt` files; the watcher maps those UUIDs back to CCC session IDs:

1. `pid-{PID}.map` files map process IDs → Claude session UUIDs
2. CCC sessions are registered with their PID
3. Watcher reads `.txt` files, resolves Claude UUID → PID → CCC session ID
4. Updates the session record and broadcasts `session.updated`
5. The TUI receives the notification and triggers an async `Refresh()`
6. The Recent sub-tab re-renders, now showing the updated topic

This is fully automatic — no CLAUDE.md snippet or CLI call needed. Any session launched from CCC (or any Claude session whose PID has a map file) gets its topic synced.

**Manual override:** `ccc update-session --session-id <id> --topic <topic>` still works for direct topic updates via the daemon RPC.

**Session ID reuse on resume:** When the user resumes a session (enter on a live/saved session), the plugin passes the original CCC session ID back through `ActionLaunch.Args["session_id"]`. The TUI reuses this ID instead of generating a new UUID, so the resumed Claude process writes to the same daemon session record — preserving the topic and session continuity.

## Storage

### cc_bookmarks (user-curated)

| Column | Description |
|--------|-------------|
| session_id | Claude Code session UUID (primary key) |
| project | Directory where Claude indexes the session |
| repo | Repository display name |
| branch | Branch name at bookmark time |
| label | User-provided label |
| summary | One-line summary of session work |
| worktree_path | Worktree directory path (NULL if not a worktree session) |
| source_repo | Main repo path for worktree sessions (NULL if not a worktree) |

### cc_archived_sessions (auto-saved)

| Column | Description |
|--------|-------------|
| session_id | Session UUID (primary key) |
| topic | Session topic at time of archiving |
| project | Project directory |
| repo | Repository name |
| branch | Branch name |
| worktree_path | Worktree path (if applicable) |
| registered_at | When session was registered (NOT NULL) |
| ended_at | When session ended (NOT NULL) |

### Worktree-aware bookmarks

Claude Code stores session files under `~/.claude/projects/<project-path-encoded>/`. For worktree sessions, Claude maps to the **main repo's** project dir.

When creating a bookmark from a worktree:
- `project` = main repo path (where Claude indexes sessions)
- `worktree_path` = the actual worktree directory

When resuming a worktree bookmark:
- If `worktree_path` is set, `cd` to the worktree path
- If `worktree_path` is empty, `cd` to `project`

### CLI: `ccc add-bookmark`

Flags: `--session-id`, `--project`, `--repo`, `--branch`, `--summary` (required), `--label`, `--worktree-path`, `--source-repo` (optional).

## Behavior

1. On Init, loads paths from DB, bookmarks from DB, archived sessions from DB, creates unified view
2. Recent sub-tab shows live sessions (from daemon) with archive toggle
3. Saved sub-tab shows bookmarked sessions with archive toggle
4. New Session sub-tab shows project paths + Browse option
5. Worktrees sub-tab shows all CCC-managed worktrees grouped by project
6. Enter on a live/saved/archived session resumes it (`--resume <session_id>`). For live sessions, the daemon's CCC-generated session_id differs from Claude CLI's session UUID, so the plugin resolves the real Claude session_id by scanning `~/.claude/projects/<encoded-project>/` for the most recently modified `.jsonl` file. If no file is found, falls back to the daemon session_id.

**Resolving Claude session IDs:** When the user presses `enter` on a live session, CCC resolves the Claude session UUID by finding the most recently modified JSONL file in `~/.claude/projects/<encoded-path>/`. The path is encoded by replacing all path separators (`/`) with `-` (e.g., `/Users/aaron/project` → `-Users-aaron-project`). If no JSONL file is found, the daemon session ID is used for `--resume`.
7. Enter on a project path launches Claude in that directory (sets `CCC_SESSION_ID` env var)
8. `b` on a live session bookmarks it; on an archived session promotes it to Saved
9. `d` dismisses live ended sessions, removes bookmarks, or deletes archived sessions (tier-dependent)
10. `a` in Saved/Recent sub-tabs archives the selected session (writes to `cc_archived_sessions`, removes from current view); `A` toggles between main and archive modes
11. `w` on a path in New Session sub-tab launches Claude in a new worktree
    - If the path is not a git repo, shows a warning overlay
12. Worktrees sub-tab scans all saved paths for git repos, lists their worktrees grouped by project
13. Delete/backspace on paths shows confirmation dialog
14. Shift+up/down swaps selected path, persisted via `sort_order` column
15. When pendingLaunchTodo is set (via event bus), shows banner "Select project for: <title>"
16. If `config.HomeDir` is set, auto-added to paths list on Init
17. `esc` from Saved/Recent/Worktrees returns to New Session sub-tab
18. `1/2/3/4` switch directly to sub-tabs; `left/right` arrows cycle sub-tabs (wraps)

### LLM Observability

In `Init`, the sessions plugin wraps `ctx.LLM` with `ObservableLLM`, publishing events to the event bus with source `"sessions"`. The `LLMDescribePath` call uses `llm.WithOperation(ctx, "describe")` to tag the operation.

### LLM Path Descriptions

When a new path is added (via Browse or `config.HomeDir`), the plugin generates a project description:

1. `LLMDescribePath(llm, dir)` reads the first 200 lines of `README.md` and 100 lines of `CLAUDE.md` from the directory
2. If both files are missing/empty, falls back to `db.AutoDescribePath(dir)` (heuristic based on directory contents)
3. If files exist, builds a prompt asking for a 1-2 sentence summary (what it does, tech stack, domain) and calls `llm.Complete`
4. On LLM error or empty response, falls back to heuristic
5. The description is persisted via `db.DBUpdatePathDescription`

**Invocation paths:**
- **Browse flow (fzf):** On `fzfFinishedMsg`, writes the heuristic description immediately to DB, then fires `backgroundDescribe` in a goroutine to upgrade it via LLM. The goroutine write is fire-and-forget since the TUI is about to quit for launch.
- **pathDescribeCmd:** Returns a `tea.Cmd` wrapping `LLMDescribePath` for async use within the bubbletea loop. On completion (`pathDescribeFinishedMsg`), writes the description to DB.

### Browse Flow (New Sub-Tab)

The "Browse..." item is always the last entry in the New sub-tab list (`isBrowse: true`).

1. User selects "Browse..." and presses `enter`
2. Plugin launches `fzf` via `tea.Exec` (full-screen takeover) with:
   - `--walker=dir` — directory-only results
   - `--walker-root=$HOME` — starts from home directory
   - `--walker-skip=.git,node_modules,.venv,__pycache__,.cache,.Trash,Library`
   - `--scheme=path`, `--exact`, `--layout=reverse`
3. On `fzfFinishedMsg` (user selected a path):
   - Adds path to the in-memory paths list and persists via `db.DBAddPath`
   - Writes heuristic description immediately, fires background LLM description upgrade
   - Clears any active filter text so the New sub-tab list shows all items
   - Emits a `LaunchRequestMsg` via `tea.Cmd` — the host processes this as an `ActionLaunch`, launching a session at the selected path immediately
   - **Note:** The launch is emitted via `LaunchRequestMsg` (not a direct `ActionLaunch` return) because `HandleMessage` actions are routed through `broadcastMessage`, which only collects `TeaCmd`s and ignores action types
4. On error or empty selection (user pressed `esc` in fzf): no-op

### Daemon Archive RPC on Session Dismiss

When the user presses `d` on an **ended** live session:

1. If the session is still active/running, dismiss is blocked with flash message "Can't dismiss running session"
2. For ended sessions, the plugin calls `client.ArchiveSession(ArchiveSessionParams{SessionID: sel.SessionID})` via the daemon RPC
3. The daemon removes the session from its live list
4. The plugin also calls `unified.RemoveSession(sel.SessionID)` to remove it from the local view immediately
5. Flash message: "Dismissed: <first 8 chars of session ID>"

This is separate from auto-archiving (which writes to `cc_archived_sessions` in the DB). The daemon `ArchiveSession` RPC tells the daemon to stop tracking the session — it does NOT write to the local archive DB. Auto-archiving to DB happens during `Refresh()` when the plugin detects sessions that transitioned from live to ended.

### NavigateTo Args (pending_todo_title)

`NavigateTo(route, args)` accepts an optional `pending_todo_title` key in the `args` map:

- If `args["pending_todo_title"]` is present, sets `pendingLaunchTodo` to a `db.Todo` with that title
- This triggers the pending launch banner in the New sub-tab: "Select project for: <title>"
- When the user selects a path while `pendingLaunchTodo` is set, the launch includes `initial_prompt` with formatted todo context
- Pressing `esc` while a pending todo is active clears it, publishes `pending.todo.cancel` on the event bus, and navigates to the command-center plugin

**Full pending todo fields (via event bus):** The `pending.todo` event bus subscription populates a richer `db.Todo` with `Title`, `Context`, `Detail`, `WhoWaiting`, `Due`, and `Effort`. The `NavigateTo` args path only sets `Title`.

### Session Label Rendering

Session labels follow this fallback order:
1. **Topic** — if set via `/set-topic`, displayed as the label (e.g., "AGENT CONSOLE")
2. **Project basename** — `filepath.Base(project)` (e.g., "claude-command-center")
3. **Branch** — last resort when both topic and project are empty

For **live sessions** (Recent sub-tab):

- **With topic:** `topic  project (branch)  age` — project basename appears in suffix when topic displaces it from label
- **Without topic:** `project (branch)  age` — project basename is the label

For **saved sessions** (Saved sub-tab), the suffix shows project basename and branch: `claude-command-center (main)`

For **archived sessions**, the suffix shows how long ago the session ended

### Session List Viewport Scrolling

The session list (Saved, Recent, and Archive modes) scrolls within the available terminal height instead of growing unbounded. The `unifiedView.View(width, height)` method constrains output to fit:

1. Computes `maxVisible = height - 6` (reserving lines for tab bar, hints, and padding); minimum 5
2. Builds the full line list (section headers + session rows)
3. If total lines exceed `maxVisible`, applies a scroll window:
   - Tracks which rendered line the cursor sits on
   - Adjusts `scrollOffset` to keep the cursor line visible
   - Renders only `lines[scrollOffset : scrollOffset+maxVisible]`
   - Shows `▲ N more above` indicator when scrolled past the top
   - Shows `▼ N more below` indicator when content extends past the bottom
4. If total lines fit within `maxVisible`, renders all lines and resets `scrollOffset` to 0

Cursor wrap-around (MoveDown past last item → first item) resets `scrollOffset` to 0. Cursor wrap-up (MoveUp past first item → last item) lets `View()` adjust the offset to show the last item.

`ToggleArchive()` resets both `cursor` and `scrollOffset` to 0.

### Blocked Session Rendering

Blocked sessions are detected by cross-referencing live sessions with daemon agent statuses:

1. On each `Refresh()`, the unified view calls `client.ListAgents()` to fetch all active `AgentStatusResult` entries
2. `isSessionBlocked(sessionID)` checks if any agent has `Status == "blocked"` and matches either `a.SessionID == sessionID` or `a.ID == sessionID`
3. Blocked sessions that are otherwise active/running render with:
   - **Yellow dot** (`●` in `#f1fa8c`) instead of the green dot for active sessions
   - **"Blocked" text** (yellow, `#f1fa8c`) prepended to the age suffix
4. Non-blocked active/running sessions render with a green dot (`●` in `#50fa7b`)
5. Ended sessions render with a muted hollow dot (`○`) regardless of block state

## Test Cases

### Sub-tab navigation

- `1` key switches to New Session sub-tab (subTab=0)
- `2` key switches to Saved sub-tab (subTab=1)
- `3` key switches to Recent sub-tab (subTab=2)
- `4` key switches to Worktrees sub-tab (subTab=3)
- `right` arrow from New Session goes to Saved; from Worktrees wraps to New Session
- `left` arrow from New Session wraps to Worktrees; from Saved goes to New Session
- `esc` from Saved/Recent/Worktrees returns to New Session
- `esc` from New Session quits (returns ActionQuit)
- `Tab`/`Shift-Tab` are NOT consumed — propagate to host for plugin switching
- Sub-tab bar renders with active tab highlighted (e.g., `[1] New Session  [2] Saved  ...`)

### Session display and filtering

- Recent sub-tab shows only live sessions in main mode (no Saved)
- Saved sub-tab shows only bookmarked sessions in main mode (no Live)
- Both sub-tabs show Archived section in archive mode
- Toggle archive mode resets cursor
- Deduplication: bookmarked live session shows ★ in Recent, not duplicated in Saved
- Empty state shows appropriate message per sub-tab
- Init loads paths, bookmarks, and archived sessions

### Session label rendering

- Live session with topic: renders `topic  project (branch)  age`
- Live session without topic: renders `project (branch)  age`
- Saved session label shows topic or project basename + branch
- Archived session suffix shows how long ago it ended

### Session actions

- Enter on live session returns ActionLaunch with correct dir, resume_id (resolved from Claude session files, not daemon ID), and session_id (CCC session ID for topic continuity)
- Enter on saved session returns ActionLaunch
- Enter on archived session returns ActionLaunch
- `b` on live session saves bookmark to DB
- `b` on archived session promotes to Saved, removes from archive
- `d` on running session shows "Can't dismiss" flash
- `d` on saved session removes bookmark
- `d` on archived session deletes from DB
- `d` on ended live session calls daemon ArchiveSession RPC and removes from view
- `a` on ended live session archives it to DB and removes from view
- `a` on running/active live session shows "Can't archive running session" flash
- `a` on saved session archives it to DB, removes bookmark, removes from view
- `A` toggles archive mode (view archive list)
- `A` in archive mode returns to main mode
- Auto-archive: ended session (not bookmarked) written to cc_archived_sessions
- Auto-archive: bookmarked ended session NOT archived

### Topic bridge

- Launching a new session generates a fresh UUID for `CCC_SESSION_ID` env var
- Resuming a session reuses the original CCC session ID (preserves topic)
- `session.updated` notification triggers async Refresh
- After topic update via `ccc update-session`, Recent sub-tab shows the new topic in the session label

### New Session sub-tab

- HandleKey "enter" on path sets Launch action
- HandleKey "delete" enters confirming mode
- Shift+up/down reorders paths
- HandleKey "w" on a git repo path sets Launch with worktree=true
- HandleKey "w" on a non-git path shows worktree warning
- LLMDescribePath with README.md returns LLM-generated summary
- LLMDescribePath without README.md or CLAUDE.md falls back to heuristic
- LLMDescribePath on LLM error falls back to heuristic
- Browse (fzf) selection adds path to DB, writes heuristic description, fires background LLM upgrade, clears filter text, emits LaunchRequestMsg to launch session
- Browse (fzf) selection clears any active filter so the New sub-tab list is not empty if launch fails
- Browse (fzf) cancellation (esc/error) is a no-op
- Delete confirmation (y) on a path while filter is active clears the filter and returns to the full list

### Worktrees sub-tab

- Worktree confirmation y executes action, n/esc cancels
- Esc from Worktrees sub-tab returns to New Session

### Routing / NavigateTo

- NavigateTo("sessions/saved") sets subTab to 1 (Saved)
- NavigateTo("sessions/recent") sets subTab to 2 (Recent)
- NavigateTo("sessions/active") redirects to Recent (subTab=2)
- NavigateTo("sessions/resume") redirects to Saved (subTab=1)
- NavigateTo("sessions") sets subTab to 0 (New Session)
- Switching sub-tabs does not corrupt other sub-tabs' content
- NavigateTo with `pending_todo_title` arg sets pending launch context and shows banner
- Esc with pending todo clears it, publishes `pending.todo.cancel`, navigates to command-center

### Session list viewport scrolling

- With 30 sessions in a 20-line terminal, view output is bounded (not all 30+ lines rendered)
- "more below" indicator appears when items extend past the viewport
- Scrolling cursor down past the viewport shows "more above" indicator and keeps cursor item visible
- With 3 sessions in a 38-line terminal, all items render with no scroll indicators
- Cursor wrap from bottom to top resets scroll to top
- Toggle archive resets scroll offset

### Blocked sessions

- Blocked session (agent status == "blocked") renders yellow dot and "Blocked" text
- Active non-blocked session renders green dot
- Ended session renders muted hollow dot
