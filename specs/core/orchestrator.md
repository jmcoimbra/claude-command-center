# SPEC: Orchestrator subsystem

## Purpose

Aaron coordinates multiple long-running Claude Code sessions in parallel — for example, a Postgres migration in one terminal, an API design discussion in another, a feature build in a third. Each of those is a "working session" with its own focus, branch, and context. There is value in a separate session whose only job is to see across all of them, hold the cross-cutting decisions, and help him think about which working session needs attention next.

That separate session is called an **orchestrator**. An orchestrator is not a working session — it does not write code or call APIs to do real work. It is a thinking partner that holds the map of what's in flight and what's been decided.

This spec describes the file layout, identity model, and CLI surface that backs orchestrators. The companion skills (`/orchestrator`, `/ask-orchestrator`) are documented separately as skills.

## Scope of v1

Communication between an orchestrator session and its working sessions is **clipboard-mediated**. The orchestrator skill emits a "PASTE INTO" block; the user copies it into the relevant terminal. There is no programmatic message delivery between sessions. A future version may add hook-based messaging.

In v1, an orchestrator session can only see the orchestrator it owns. There is no cross-orchestrator dashboard. The CCC TUI does not have an orchestrator tab.

## Concepts

- **Orchestrator** — a named, persistent context for coordinating multiple working sessions. Created when the user invokes `/orchestrator <name>` in a fresh Claude session. Has lifecycle states `active` and `complete`.

- **Thread** — a record inside an orchestrator that tracks one working session. A thread has a human label (e.g. `postgres-migration`), an optional CCC session ID binding, project/branch/worktree metadata, a status, and a last-update summary. Statuses are freeform text (typical values: `planning`, `in-flight`, `blocked`, `awaiting-user`, `complete`) — not enforced as an enum.

- **Decision** — a freeform note recorded by the orchestrator capturing a choice made during orchestration. May reference a specific thread.

- **Question** — an open question the orchestrator is holding. Has a body and a status (`open` or `resolved`).

- **Identity by topic** — an orchestrator session is identified by setting its session topic to `ORCHESTRATE: <name>`. CLI subcommands resolve "which orchestrator is this session" by reading the session topic file and stripping the prefix.

## Storage

All orchestrator state lives on disk under `~/.claude/orchestrators/<name>/`. There is no database involvement. Files are the source of truth.

Each orchestrator's directory holds:

- **`state.md`** — the orchestrator's structured-markdown state. Holds metadata in YAML frontmatter (name, status, project, started_at, completed_at) and four sections (`# Threads`, `# Decisions`, `# Questions`, `# Notes`). Read and mutated by the CLI subcommands. Hand-editable in a pinch.

- **`transcript.md`** — append-only discussion history. Written via `log.sh`.

- **`state.log`** — append-only timeline of state changes (one line per CLI mutation). A scannable companion to `transcript.md`.

- **`log.sh`** — small append logger script created at init time. Mirrors the `/interview` skill's logger pattern. Invoked by the orchestrator skill to capture discussion turns.

The CLI is responsible for keeping `state.md` well-formed when it mutates sections. A user editing `state.md` by hand is supported but at their own risk.

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

## Thread registration

A thread comes into existence one of three ways. In all cases, the **orchestrator session** is what calls `ccc orchestrator thread add` — the worker session never registers itself.

1. **Proactive.** "I'm about to spin up a postgres-migration worker." Add a thread with `status=planning` and no session ID. Bind the session ID later when the worker exists.

2. **Reactive (clipboard handoff arrives).** A worker uses `/ask-orchestrator` to put a "HANDOFF TO ORCHESTRATOR" block on the clipboard. The block carries the worker's CCC session ID, project, branch, and the question. The user pastes it into the orchestrator session; the orchestrator skill creates or updates a thread with that metadata. **Most common first-attach moment.**

3. **Adoptive.** "I already have a session running on `feature/auth` — track it." The orchestrator skill queries CCC for active sessions and creates a thread with the session ID pre-filled.

There is no separate "attach" step — binding a session ID after the fact is just a thread update.

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

- `ccc orchestrator thread add --name <n> [--project <p>] [--branch <b>] [--worktree <w>] [--session-id <id>] [--status <s>]` — add a thread row.

- `ccc orchestrator thread set-status --name <n> --status <s> [--reason <r>]` — update a thread's status.

- `ccc orchestrator thread complete --name <n>` — shorthand for setting status to `complete`.

- `ccc orchestrator decision add --body <text> [--thread <n>]` — append a decision.

- `ccc orchestrator question add --body <text> [--thread <n>]` — append an open question.

- `ccc orchestrator question resolve --id <Q1> [--note <text>]` — mark a question resolved.

- `ccc orchestrator overlap-check --project <p> [--themes <t>]` — list non-complete orchestrators whose project or themes overlap. JSON output.

- `ccc orchestrator paste-header --thread <n>` — emit the standardized "PASTE INTO" block.

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
