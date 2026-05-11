# SPEC: Orchestrator subsystem

## Purpose

Aaron coordinates multiple long-running Claude Code sessions in parallel — for example, a Postgres migration in one terminal, an API design discussion in another, a feature build in a third. Each of those is a "working session" with its own focus, branch, and context. There is value in a separate session whose only job is to see across all of them, hold the cross-cutting decisions, and help him think about which working session needs attention next.

That separate session is called an **orchestrator**. An orchestrator is not a working session — it does not write code or call APIs to do real work. It is a thinking partner that holds the map of what's in flight and what's been decided.

This spec describes the file layout, identity model, and CLI surface that backs orchestrators. The companion skills (`/orchestrator`, `/ask-orchestrator`) are documented separately as skills.

## Scope of v1

Communication between an orchestrator session and its working sessions is **file-mediated via a per-orchestrator inbox** (`inbox.jsonl`). The orchestrator writes a handoff message addressed to a role; the user opens a worker terminal in the right worktree and runs `/orchestrate <role>`; the worker reads its message from the inbox and writes a checkin back. Notification is still human-mediated — the orchestrator tells the user which window to switch to — but the message payload itself is durable on disk and re-readable after the clipboard is gone.

The clipboard-based `PASTE INTO` flow from earlier drafts is retained at the CLI level (`paste-header`) but is no longer the primary transport. New skills target the inbox.

In v1, an orchestrator session can only see the orchestrator it owns. There is no cross-orchestrator dashboard. The CCC TUI does not have an orchestrator tab.

## Concepts

- **Orchestrator** — a named, persistent context for coordinating multiple working sessions. Created when the user invokes `/orchestrator <name>` in a fresh Claude session. Has lifecycle states `active` and `complete`.

- **Thread** — a record inside an orchestrator that tracks one working session. A thread has a human label (e.g. `postgres-migration`), an optional CCC session ID binding, project/branch/worktree metadata, a status, and a last-update summary. Statuses are freeform text (typical values: `planning`, `in-flight`, `blocked`, `awaiting-user`, `complete`) — not enforced as an enum.

- **Decision** — a freeform note recorded by the orchestrator capturing a choice made during orchestration. May reference a specific thread.

- **Question** — an open question the orchestrator is holding. Has a body and a status (`open` or `resolved`).

- **Identity by topic** — an orchestrator session is identified by setting its session topic to `ORCHESTRATE: <name>`. CLI subcommands resolve "which orchestrator is this session" by reading the session topic file and stripping the prefix.

- **Role** — a short handle (`a`, `b`, `c`, `wave-0b`, etc.) the orchestrator assigns to a worker. A thread may carry an explicit `role` (e.g. `spine`) that is distinct from its human-readable name (e.g. `wave 0c: typed API spine`). When the role is unset, the thread name is used as the role for backwards compatibility. The role is the routing key on `inbox.jsonl` — workers query their own role via `inbox resolve-role` and the orchestrator addresses messages with `--to <role>` (or `--thread <name>`, which resolves to the same role). A worker terminal claims a role by running `/orchestrate <role>`.

- **Inbox** — the append-only `inbox.jsonl` file inside an orchestrator's directory. Every cross-session message (orchestrator-to-worker handoffs, worker-to-orchestrator checkins, freeform updates and questions) is a line in this file. Replaces the clipboard as the durable transport.

## Storage

All orchestrator state lives on disk under `~/.claude/orchestrators/<name>/`. There is no database involvement. Files are the source of truth.

Each orchestrator's directory holds:

- **`state.md`** — the orchestrator's structured-markdown state. Holds metadata in YAML frontmatter (name, status, project, started_at, completed_at) and four sections (`# Threads`, `# Decisions`, `# Questions`, `# Notes`). Read and mutated by the CLI subcommands. Hand-editable in a pinch.

- **`transcript.md`** — append-only discussion history. Written via `log.sh`.

- **`state.log`** — append-only timeline of state changes (one line per CLI mutation). A scannable companion to `transcript.md`.

- **`log.sh`** — small append logger script created at init time. Mirrors the `/interview` skill's logger pattern. Invoked by the orchestrator skill to capture discussion turns.

- **`inbox.jsonl`** — append-only newline-delimited JSON. Each line is one message between the orchestrator and a worker (in either direction). Source of truth for cross-session communication.

- **`cursors.json`** — per-recipient read cursor. Maps recipient (`orchestrator` or a role name) to the highest message id that recipient has acknowledged. Used to compute "unread for me." Mutated in place; safe to delete (everything becomes unread again).

The CLI is responsible for keeping `state.md` well-formed when it mutates sections. A user editing `state.md` by hand is supported but at their own risk. Inbox files are documented enough that the user can grep/tail them by hand without ceremony.

### state.md format

```markdown
---
name: postgres-migration
status: active
project: ~/Personal/sherlock
started_at: 2026-05-09T15:00:00Z
completed_at:
---

# Threads

## postgres-migration
- role: postgres
- status: in-flight
- project: ~/Personal/sherlock
- branch: feature/postgres
- worktree:
- session-id: abc-123
- last-update: 2026-05-09T15:30:00Z
- last-summary: stage 8 complete, starting 9

# Decisions

- 2026-05-09T15:30:00Z: chose to defer stage 10 until stage 8 is verified

# Questions

- (open) 2026-05-09T15:35:00Z [Q1]: should we migrate the indexes before or after data?

# Notes

(freeform)
```

Question IDs are short identifiers (`Q1`, `Q2`, ...) assigned at add time, used by `question resolve`.

The `role` line is optional. When present it is the routing key used by `inbox.jsonl` for messages addressed to this thread. When absent, the thread name is used as the role (backwards-compatible with pre-`role`-field state.md files).

### inbox.jsonl format

One JSON object per line. Required fields: `id`, `ts`, `from`, `to`, `kind`, `body`. Optional fields carry handoff/checkin metadata.

```jsonl
{"id":1,"ts":"2026-05-10T16:30:00Z","from":"orchestrator","to":"a","kind":"handoff","body":"Migrate the cache layer to Redis. See branch notes.","topic":"redis-cache","project":"/Users/aaron/src/app","branch":"feat/redis","worktree":"/Users/aaron/src/app/.claude/worktrees/redis"}
{"id":2,"ts":"2026-05-10T16:32:14Z","from":"a","to":"orchestrator","kind":"checkin","body":"Picked up. Starting now.","project":"/Users/aaron/src/app/.claude/worktrees/redis","branch":"feat/redis","session_id":"abc-123"}
{"id":3,"ts":"2026-05-10T16:45:01Z","from":"a","to":"orchestrator","kind":"update","body":"Stage 1 done, stage 2 in progress."}
```

Field reference:

- **`id`** — monotonic positive integer scoped to this orchestrator. Assigned by the CLI at append time as `max(existing_ids) + 1`. Used by cursors.
- **`ts`** — RFC3339 UTC timestamp.
- **`from`** — `orchestrator` or a role name. Identifies sender.
- **`to`** — `orchestrator` or a role name. Identifies recipient.
- **`kind`** — one of `handoff`, `checkin`, `update`, `question`, `paste-back`. Freeform additions are allowed; readers should not fail on unknown kinds.
- **`body`** — the message text. May be multi-line (newlines escaped per JSON).
- **`topic`** — optional. For `handoff` messages, the session topic the worker should set when claiming the role.
- **`project`**, **`branch`**, **`worktree`**, **`session_id`** — optional metadata. Handoffs typically carry `project`/`branch`/`worktree` (the target). Checkins typically carry the worker's resolved `project`/`branch`/`worktree`/`session_id`.

### cursors.json format

```json
{
  "orchestrator": 12,
  "a": 5,
  "b": 7
}
```

Maps recipient → highest message id that recipient has marked read. A message with `to == R` and `id > cursors[R]` is unread for recipient R. Cursors for senders are not tracked.

## Thread registration

A thread comes into existence one of three ways. In all cases, the **orchestrator session** is what calls `ccc orchestrator thread add` — the worker session never registers itself.

1. **Proactive with inbox handoff (default).** The orchestrator decides "I want a worker named `a` to do X." It calls `thread add` with a role name (`a`) and target project/branch/worktree, then `inbox send --to a --kind handoff` with the task body and target topic. The user opens a terminal in the target worktree and runs `/orchestrate a`. The worker reads the handoff and writes a checkin back via `inbox send --to orchestrator --kind checkin --from a` with its resolved project/branch/session-id. The orchestrator's next `inbox list` or `/check-messages` invocation sees the checkin and patches the thread with the worker's metadata.

2. **Reactive (worker reaches out first).** A worker uses `/ask-orchestrator` to write a question or checkin to the inbox of an existing orchestrator. The orchestrator sees it on next `/check-messages` and, if no thread exists for that role, creates one.

3. **Adoptive.** "I already have a session running on `feature/auth` — track it." The orchestrator skill queries CCC for active sessions and creates a thread with the session ID pre-filled.

There is no separate "attach" step — binding a session ID after the fact is just a thread update.

### Role resolution from worktree

A worker terminal opened in a worktree that already maps to an existing thread (because a previous session for that role checked in earlier) can look up its own role without being told. `ccc orchestrator inbox resolve-role --worktree <path>` scans active orchestrators' threads for one whose `worktree` (or `project`) matches and returns `<orchestrator>:<role>`. The role returned is the thread's stored `role` field if set, otherwise the thread name. This lets a fresh session in the same worktree run `/check-messages` and pick up where the previous one left off without re-typing the role name.

Threads whose `Status == "complete"` are excluded from `resolve-role` output by default — `/orchestrate` and other intake flows should not see done work as candidates. Pass `--include-completed` to surface them (rare case: reconnecting to a completed thread for cleanup or a postscript message).

## Lifecycle

### Creation

The user invokes `/orchestrator` (skill) in a fresh session. The skill either takes a name or scans `~/.claude/orchestrators/*` for non-complete overlaps. Once a name is chosen:

1. Skill calls `/set-topic ORCHESTRATE: <name>`.
2. Skill calls `ccc orchestrator init [--project <path>]`. Idempotent — if the directory exists with `status=active` in `state.md`, nothing changes. Otherwise the directory is created with `log.sh`, an empty `transcript.md`, an empty `state.log`, and a fresh `state.md` (status=active, started_at=now).

### Active

Orchestrator session calls CLI subcommands at decision moments — adding threads, logging decisions, resolving questions — and calls `log.sh` to append discussion turns. To see current state, runs `ccc orchestrator status`, which reads `state.md` and prints it.

### Completion

`ccc orchestrator complete` updates the YAML frontmatter to set `status: complete` and `completed_at: <now>`, and appends a state-log entry. The directory is preserved. Completed orchestrators are filtered from overlap detection.

### Concurrency

Multiple orchestrators may be active simultaneously, each in its own terminal with its own topic. Names must be unique among non-complete orchestrators (enforced by directory existence). Reusing a completed orchestrator's name requires either renaming or completing-then-creating-fresh.

The CLI does no cross-process locking on `state.md`. In practice only one terminal claims a given name (via topic), and writes are infrequent enough that races are not a concern at this scale.

## CLI surface

Documented in detail in `specs/core/cli.md`. At a glance:

- `ccc orchestrator init [--project <path>]` — create the directory and bootstrap files.

- `ccc orchestrator status [--json]` — print state from `state.md`.

- `ccc orchestrator thread add --name <n> [--role <r>] [--project <p>] [--branch <b>] [--worktree <w>] [--session-id <id>] [--status <s>]` — add a thread row. `--role` is the short routing key used by inbox messages; when omitted the thread name is used as the role.

- `ccc orchestrator thread set-status --name <n> --status <s> [--reason <r>]` — update a thread's status.

- `ccc orchestrator thread set-role --name <n> --role <r>` — set or update the thread's routing role. Used to backfill the role onto threads created before the `role` field existed.

- `ccc orchestrator thread complete --name <n>` — shorthand for setting status to `complete`.

- `ccc orchestrator decision add --body <text> [--thread <n>]` — append a decision.

- `ccc orchestrator question add --body <text> [--thread <n>]` — append an open question.

- `ccc orchestrator question resolve --id <Q1> [--note <text>]` — mark a question resolved.

- `ccc orchestrator overlap-check --project <p> [--themes <t>]` — list non-complete orchestrators whose project or themes overlap. JSON output.

- `ccc orchestrator paste-header --thread <n>` — emit the standardized "PASTE INTO" block. Retained for skills that still want a clipboard transport.

- `ccc orchestrator inbox send [--orchestrator <name>] (--to <recipient> | --thread <name>) --kind <kind> --body <text> [--from <sender>] [--topic <t>] [--project <p>] [--branch <b>] [--worktree <w>] [--session-id <id>]` — append a message to an orchestrator's inbox. Exactly one of `--to` or `--thread` must be supplied; passing both is an error. `--thread <name>` looks up the thread's role (falling back to the thread name) and uses it as the recipient. Sender defaults to `orchestrator`. Recipient `*` broadcasts.

- `ccc orchestrator inbox list [--orchestrator <name>] [--to <recipient>] [--from <sender>] [--kind <kind>] [--unread] [--json] [--all]` — list inbox messages, optionally filtered. `--unread` requires `--to` and reads `cursors.json` to filter to messages with id greater than the recipient's cursor. Default output is one human-readable line per message; `--json` emits a JSON array.

- `ccc orchestrator inbox mark-read [--orchestrator <name>] --to <recipient> [--up-to <id>]` — set the recipient's cursor in `cursors.json`. With no `--up-to`, sets it to the highest existing message id.

- `ccc orchestrator inbox resolve-role [--worktree <path>] [--project <path>] [--include-completed] [--json]` — search active orchestrators for a thread whose `worktree`/`project` matches and return `<orchestrator>:<role>`. With `--json`, returns an array of matches. Threads whose status is `complete` are excluded by default; pass `--include-completed` to include them.

The `--orchestrator <name>` flag overrides session-topic resolution on the three inbox verbs that touch a single orchestrator's state. Worker sessions (whose topic is the worker topic, not `ORCHESTRATE: ...`) pass it explicitly so the CLI does not need to consult the session topic. When the flag is omitted, the CLI falls back to topic resolution as usual.

- `ccc orchestrator complete` — mark current orchestrator complete.

- `ccc orchestrator list [--all] [--json]` — list orchestrators (active by default).

The orchestrator name is resolved from the current session topic (`ORCHESTRATE: ` prefix). Subcommands fail with a clear error if the topic is missing or doesn't have the prefix, except `list` and `overlap-check` which don't require an active orchestrator.

## Test cases

### File layer

- `init` creates the directory with `state.md`, `transcript.md`, `state.log`, `log.sh`.
- `init` is idempotent — running twice on the same active orchestrator changes nothing.
- `init` recreates missing files in an existing directory without overwriting `state.md` if it has content.
- `thread add` appends a thread under `# Threads`.
- `thread add` with a name that already exists fails.
- `thread set-status` updates the thread's status field and appends a `state.log` entry.
- `thread complete` sets the thread's status to `complete`.
- `decision add` appends a timestamped line under `# Decisions`.
- `question add` appends an open line under `# Questions` with a fresh `Q<n>` ID.
- `question resolve` flips `(open)` to `(resolved)` for the matching ID.
- `complete` sets `status: complete` and `completed_at` in frontmatter.
- `complete` is a no-op (exit 0) when called on an already-complete orchestrator.

### Identity

- Topic `ORCHESTRATE: postgres-migration` resolves to orchestrator `postgres-migration`.
- Topic `Orchestrate: foo` (wrong prefix case) — CLI fails with a clear error.
- No topic file — CLI fails with a clear error.

### Overlap and listing

- `overlap-check --project <p>` returns matches by project substring across non-complete orchestrators.
- `overlap-check` returns an empty array (exit 0) when there are no matches.
- `list` shows non-complete orchestrators by default, all with `--all`.
- `list --json` emits a JSON array suitable for skill consumption.

### state.md robustness

- All mutators preserve sections they don't touch (e.g. `decision add` does not disturb `# Threads`).
- Reading `state.md` after every mutator round-trip yields the same logical state.
- Hand-editing `state.md` between mutations is tolerated as long as section headers are intact.

### Inbox

- `inbox send` appends a well-formed JSON line to `inbox.jsonl` with a monotonic `id` and current UTC `ts`.
- Sequential `inbox send` calls produce strictly increasing `id` values within a single orchestrator.
- `inbox list` returns messages in append order.
- `inbox list --to a --unread` returns only messages with `to == "a"` and `id > cursors["a"]`.
- `inbox list --to a --unread` also includes messages with `to == "*"` (broadcast) when their id exceeds the cursor.
- `inbox mark-read --to a` without `--up-to` sets `cursors["a"]` to the highest existing message id and makes subsequent `--unread` queries empty until new messages arrive.
- `inbox mark-read --to a --up-to 5` sets `cursors["a"]` to `5` exactly.
- `inbox resolve-role --worktree W` returns the single `<orchestrator>:<role>` whose thread `worktree` equals `W`. If multiple match, all are returned in `--json` form. If none match, exits cleanly with empty output.
- `inbox resolve-role --project P` falls back to thread `project` when `worktree` is empty.
- `inbox resolve-role` excludes threads whose status is `complete` by default. The same query with `--include-completed` includes them.
- Reading `inbox.jsonl` when the file does not yet exist returns an empty list, not an error.
- `inbox send/list/mark-read --orchestrator <name>` succeeds without an `ORCHESTRATE:` session topic. The flag overrides topic resolution.
- When `--orchestrator` is omitted and no session topic is set, the same verbs fail with a clear error pointing at both remediation paths (set a topic OR pass the flag).

### Roles

- `thread add --name N --role R` persists `role: R` under the thread in `state.md`. A subsequent `Load` round-trips `Thread.Role == "R"`.
- `thread add --name N` (no `--role`) leaves `Thread.Role` empty on disk. `resolve-role --worktree <thread worktree>` returns `<orchestrator>:N` (falls back to the thread name).
- `thread set-role --name N --role R` updates an existing thread's role; subsequent `resolve-role` returns `<orchestrator>:R`.
- `thread set-role --name <missing>` fails with a clear error.
- `inbox send --thread N` resolves the role from the thread record (stored role, falling back to the thread name) and routes the message there; recipients matching that role see it via `--unread --to <role>`.
- `inbox send --to X --thread N` (both supplied) fails with a clear "mutually exclusive" error.
- Pre-existing `state.md` files written before the `role` field existed parse and re-render without losing data; threads without a `role` line continue to behave exactly as before.
