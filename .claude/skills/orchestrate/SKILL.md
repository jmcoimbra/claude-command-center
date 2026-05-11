---
name: orchestrate
description: From a worker session, claim an orchestrator-assigned role (first intake) or reconnect to a previously claimed role after /clear. Reads the role's pending handoff (or recent history on reconnect), sets the worker's session topic, and writes a checkin back to the inbox. Use right after opening a worker terminal in the target worktree, or after /clear to re-establish context.
user_invocable: true
---

# Orchestrate (worker intake or reconnect)

The bookend on the worker side of an orchestrator → worker handoff. Used when:

- The orchestrator session has written one or more handoff messages to its inbox, each addressed to a role name like `a`, `b`, or `wave-0b`, and the user has opened a fresh worker terminal in the target worktree. (**First intake.**)
- The user has been working in a worker session, ran `/clear` to free context (e.g., before `/ralph-review`), and wants to re-establish the role binding. (**Reconnect.**)
- The orchestrator has sent a new handoff to a role that already finished a previous task. (**Re-handoff.**)

`/orchestrate` figures out which state it's in by looking at the inbox and branches accordingly. The user just types `/orchestrate <role>` (or just `/orchestrate` if role resolution by worktree finds a single match) and the skill handles the rest.

In all cases, the skill **does not start executing the task**. After it runs, hand control back to the user; they will tell you when to begin (or continue).

## Arguments

- `$ARGUMENTS` — optional role name (`a`, `wave-0b`, etc.). If omitted, the skill tries to infer the role by resolving the current worktree against active orchestrators' threads.

## Step 1: Resolve which orchestrator and role this terminal belongs to

### CLI surface (don't guess)

The orchestrator CLI is narrower than it looks. Only these verbs/flags exist:

- `ccc orchestrator list [--all] [--json]` — list active orchestrators (with `--json`, each entry includes `project`, `status`, `created_at`, etc.)
- `ccc orchestrator inbox send|list|mark-read|resolve-role` — see flags below
- `ccc orchestrator inbox list [--orchestrator N] [--to R] [--from S] [--kind K] [--unread] [--all] [--json]`
- `ccc orchestrator inbox resolve-role [--worktree W] [--project P] [--include-completed] [--json]` — **excludes completed threads by default.** A role the orchestrator marked done via `thread complete` will not appear unless `--include-completed` is passed (rare: cleanup or postscript on a wrapped-up thread).
- `ccc orchestrator status [--json]` — **current session only**, no `--orchestrator` flag

**Does NOT exist:** `thread list`, `status --orchestrator <name>`, any other thread-enumeration verb, subcommand-level `--help`. The authoritative reference is `ccc orchestrator --help`. Don't invent verbs — if it isn't in that top-level help, it isn't there.

### Resolve by worktree first

```bash
PWD_NOW=$(pwd)
ccc orchestrator inbox resolve-role --worktree "$PWD_NOW" --project "$PWD_NOW" --json
```

The output is a JSON array of `{orchestrator, role, project, worktree}` entries.

- **If `$ARGUMENTS` was provided**, filter to entries with matching `role`. If exactly one matches, use it. If none match, ask the user whether to proceed anyway (orchestrator may not have created the thread yet); they can paste the orchestrator name explicitly.
- **If `$ARGUMENTS` was empty**, the array determines the action:
  - **Empty array** → the worktree isn't bound to a thread yet (common for a fresh `ccc/<date>` branch where the orchestrator wrote a handoff but the role's worktree metadata wasn't set). Fall back to discovery instead of asking the user blind:
    1. `ccc orchestrator list --json` — find active orchestrators whose `project` matches the current repo root (`git rev-parse --show-toplevel`).
    2. If **exactly one** orchestrator matches this project, list its unread handoffs with `ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --kind handoff --unread --json` and group by the `to` field. Each unique `to` is a candidate role.
    3. Present the candidate roles as an `AskUserQuestion`. Use the handoff's `topic` field as the human-readable label (e.g. `"query (wave 1b: query-svc brainstorm)"`), not the bare role name — `topic` is what the orchestrator named the work, so it's far more recognizable than `query`.
    4. If **zero** orchestrators match the project, or **multiple** match with ambiguous projects, only then fall back to a free-text prompt asking the user to paste the orchestrator name and role.
  - **Exactly one entry** → use it.
  - **Multiple entries** → show the list and ask which one (prefer `topic` as the label here too — see Step 3a for how it's extracted).

After this step you have `$ORCH_NAME` and `$ROLE`.

## Step 2: Detect state (first intake vs. reconnect vs. re-handoff)

The signal of "this role has already been intook before" is whether the role has ever sent a message *to* the orchestrator. Check both prior outbound and current unread inbound:

```bash
PRIOR_OUTBOUND=$(ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --from "$ROLE" --json)
UNREAD_INBOUND=$(ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --to "$ROLE" --unread --json)
```

Classify (treat `[]` and empty as the same):

- `PRIOR_OUTBOUND` empty, `UNREAD_INBOUND` contains a handoff → **first intake.** Go to Step 3a.
- `PRIOR_OUTBOUND` non-empty, `UNREAD_INBOUND` contains a handoff → **re-handoff.** Mention one line that prior work exists ("This role has prior history — picking up a new handoff."), then go to Step 3a.
- `PRIOR_OUTBOUND` non-empty, `UNREAD_INBOUND` empty (or contains only non-handoff messages) → **reconnect.** Go to Step 3b.
- `PRIOR_OUTBOUND` empty, `UNREAD_INBOUND` empty → no handoff yet. Tell the user:

  > No handoff message for role `<role>` in orchestrator `<orch>`, and no prior history. The orchestrator may not have written one yet. Want me to check for any unread messages instead?

  Then stop.

## Step 3a: First intake or re-handoff (existing flow)

From `UNREAD_INBOUND` (already fetched in Step 2), pick the message with the highest `id` whose `kind == "handoff"` and `from == "orchestrator"`. That's the handoff to process.

Extract from the chosen message:

- `body` — the task description
- `topic` — the worker topic to set (may be empty)
- `project`, `branch`, `worktree` — target metadata
- `id` — needed later for `mark-read`

### Sanity-check the local environment

Compare current pwd / branch to the handoff's `project` / `branch` / `worktree`. If there's a mismatch, surface it as a warning and ask whether to proceed — never `cd` for the user.

### Set the session topic

The statusline and `remind-session-topic` hook resolve the session id from `~/.claude/session-topics/pid-$PPID.map` — **not** from `$CCC_SESSION_ID`. Always write the topic to the pid-map-resolved file (matches what `/set-topic` does), otherwise the topic lands in an orphan file and the statusline stays blank.

```bash
SESSION_ID=$(cat ~/.claude/session-topics/pid-$PPID.map 2>/dev/null)
if [ -z "$SESSION_ID" ]; then
  echo "Could not resolve session ID — /orchestrate needs a Claude session"
  exit 1
fi
WORKER_TOPIC="${HANDOFF_TOPIC:-$ROLE}"
printf '%s' "$WORKER_TOPIC" > ~/.claude/session-topics/${SESSION_ID}.txt
```

If a topic is already set on this session and it differs, ask before overwriting.

### Write the checkin

The checkin's `--session-id` is the CCC-side session correlation id; prefer `$CCC_SESSION_ID` and fall back to the pid-map id only if it's unset. (Topic writes use the pid-map id above; the inbox `session_id` field uses this one.)

```bash
PROJECT=$(pwd)
BRANCH=$(git branch --show-current 2>/dev/null || echo "")
WORKTREE=""
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  TOPLEVEL=$(git rev-parse --show-toplevel)
  COMMON=$(git rev-parse --git-common-dir 2>/dev/null)
  if [ -n "$COMMON" ] && [ "$(dirname "$COMMON")" != "$TOPLEVEL" ]; then
    WORKTREE="$TOPLEVEL"
  fi
fi
CHECKIN_SESSION_ID="${CCC_SESSION_ID:-$SESSION_ID}"

ccc orchestrator inbox send \
  --orchestrator "$ORCH_NAME" \
  --to orchestrator \
  --from "$ROLE" \
  --kind checkin \
  --project "$PROJECT" \
  --branch "$BRANCH" \
  --worktree "$WORKTREE" \
  --session-id "$CHECKIN_SESSION_ID" \
  --body "Picked up handoff. Topic set to \"$WORKER_TOPIC\". Ready to start."
```

### Mark the handoff read

```bash
ccc orchestrator inbox mark-read --orchestrator "$ORCH_NAME" --to "$ROLE" --up-to "$HANDOFF_ID"
```

### Summarize and hand control back

Print a tight summary:

- **Orchestrator:** `<orch>`
- **Role:** `<role>`
- **Topic set:** `<worker-topic>`
- **Task (one sentence):** distilled from the handoff body
- **Checkin sent.**

Then:

> Checkin is in the orchestrator's inbox. When you're ready to start the work, say "go" (or describe how you'd like to proceed) and I'll dive in.

Do **not** start executing the task in this turn. The user will tell you when to begin.

## Step 3b: Reconnect (resume after /clear)

This branch fires when the role has prior outbound history but no unread inbound handoff. The orchestrator already knows about us; we just need to re-establish local session state and load enough recent history to continue.

### Set the session topic (idempotent)

Determine the topic to set:

1. Look at all prior handoffs to this role: `ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --to "$ROLE" --kind handoff --json`. If any have a non-empty `topic`, use the most recent one's `topic`.
2. Otherwise fall back to `$ROLE`.

Resolve `SESSION_ID` from the pid map (the statusline's source of truth), **not** `$CCC_SESSION_ID` — those can diverge and a write to the wrong file leaves the statusline blank.

```bash
SESSION_ID=$(cat ~/.claude/session-topics/pid-$PPID.map 2>/dev/null)
if [ -z "$SESSION_ID" ]; then
  echo "Could not resolve session ID — /orchestrate needs a Claude session"
  exit 1
fi
WORKER_TOPIC="${HANDOFF_TOPIC:-$ROLE}"
CURRENT_TOPIC=$(cat ~/.claude/session-topics/${SESSION_ID}.txt 2>/dev/null || echo "")
if [ "$CURRENT_TOPIC" != "$WORKER_TOPIC" ]; then
  printf '%s' "$WORKER_TOPIC" > ~/.claude/session-topics/${SESSION_ID}.txt
fi
```

If `$CURRENT_TOPIC` is already correct, no write is needed. If a *different* topic is set, ask before overwriting.

### Show the recap

Fetch the last 5 messages of interaction between this role and the orchestrator:

```bash
ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --to "$ROLE" --json
ccc orchestrator inbox list --orchestrator "$ORCH_NAME" --from "$ROLE" --json
```

Merge the two arrays, sort by `id` ascending, take the last 5. Render each as a tight block (same format as `/check-messages`):

```
─── #<id>  <kind>  <from> → <to>  at <ts> ───
  <body>
```

If there are no messages at all (shouldn't happen in reconnect mode but guard anyway), say so.

### Do NOT send a reconnect checkin

The orchestrator already has full thread state from the original intake — a "reconnected" checkin would just be noise. If the user wants to ping the orchestrator, they can run `/ask-orchestrator` after reconnecting.

### Do NOT mark anything new as read

There's nothing unread to mark. Skip the `mark-read` step entirely.

### Summarize and hand control back

Print a tight summary:

- **Mode:** Reconnect
- **Orchestrator:** `<orch>`
- **Role:** `<role>`
- **Topic set:** `<worker-topic>` (or "unchanged" if it was already correct)
- **Recap:** last 5 messages shown above.

Then:

> Reconnected to `<orch>:<role>`. Topic restored, recent history loaded. Suggested next actions:
>
> - `/check-messages` — verify nothing is waiting for you
> - `/ask-orchestrator` — send a status update or question
> - Tell me what you want to work on (continue the task, run `/ralph-review`, etc.)

Do **not** start executing anything in this turn. Wait for the user.

## Notes

- **Pass `--orchestrator $ORCH_NAME` to every inbox call.** Worker sessions have their own topic (the worker topic, e.g. `wave-0b`), not `ORCHESTRATE: ...`. The flag bypasses topic resolution so we never have to fake one.
- **`topic` is a first-class field on handoff messages.** It carries two jobs: it's the worker-topic the orchestrator wants set on the worker session (used in Step 3a/3b), and it's the most human-readable label for the role. Anywhere this skill displays a role to the user — discovery prompts, multi-entry disambiguation, the summary block — prefer the handoff's `topic` over the bare role name (e.g. `"query (wave 1b: query-svc brainstorm)"` beats `"query"`). Fall back to the role name only when no `topic` is set.
- **No clipboard handling here.** This is the inbox-based version of the workflow. The clipboard `PASTE INTO` flow has been retired in favor of durable, queryable messages.
- **Don't include secrets or large file dumps in the checkin body.** Keep it a short status sentence. The orchestrator already has the task body.
