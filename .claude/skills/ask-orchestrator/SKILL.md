---
name: ask-orchestrator
description: From a worker session, send a message (question, status update, or freeform note) to the orchestrator coordinating this work. Writes directly to the orchestrator's inbox — no clipboard handoff. Use when stuck on a decision, when status changes, or when the orchestrator should know something.
user_invocable: true
---

# Ask Orchestrator

Used from a **working session** (not from the orchestrator itself). Writes a message to the orchestrator's `inbox.jsonl` so it shows up in the orchestrator's next `/check-messages`.

## Arguments

- `$ARGUMENTS` — optional message body. If omitted, ask the user what they want to send.

## Step 1: Confirm we're not already in the orchestrator

```bash
SESSION_ID="${CCC_SESSION_ID:-$(cat ~/.claude/session-topics/pid-$PPID.map 2>/dev/null)}"
TOPIC=""
if [ -n "$SESSION_ID" ]; then
  TOPIC=$(cat ~/.claude/session-topics/${SESSION_ID}.txt 2>/dev/null)
fi
echo "topic: $TOPIC"
```

If `$TOPIC` starts with `ORCHESTRATE: `, the user is invoking this from inside the orchestrator session. Tell them:

> You're already in the orchestrator (`<topic>`). `/ask-orchestrator` is meant for worker sessions. Use `/check-messages` or `ccc orchestrator inbox send` directly here.

Then stop.

## Step 2: Get the message body

If `$ARGUMENTS` is non-empty, use it. Otherwise ask:

> What would you like to send to the orchestrator?

Wait for the answer.

Also classify the message kind:

- **question** — explicit request for a decision or input
- **update** — status change, progress note, "FYI"
- **checkin** — first contact / restart-of-session announce

If unclear, default to `update`.

## Step 3: Resolve which orchestrator + role this terminal belongs to

```bash
PWD_NOW=$(pwd)
ccc orchestrator inbox resolve-role --worktree "$PWD_NOW" --project "$PWD_NOW" --json
```

- **Empty array** — no thread exists for this worktree. Either we're not registered yet, or there's no orchestrator coordinating this work. Ask the user:

  > I can't find an orchestrator thread for this worktree. Active orchestrators:
  > <listed via `ccc orchestrator list --json`>
  >
  > Which orchestrator should I send this to? (And what role name should I claim?)

- **Exactly one entry** — use it directly.

- **Multiple entries** — show the list and let the user pick.

After this step you have `$ORCH_NAME` and `$ROLE`.

## Step 4: Gather local context

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
FROM_SESSION="${CCC_SESSION_ID:-$SESSION_ID}"
```

## Step 5: Send the message

Worker sessions don't have an `ORCHESTRATE:` topic, so pass `--orchestrator` explicitly:

```bash
ccc orchestrator inbox send \
  --orchestrator "$ORCH_NAME" \
  --to orchestrator \
  --from "$ROLE" \
  --kind "$KIND" \
  --project "$PROJECT" \
  --branch "$BRANCH" \
  --worktree "$WORKTREE" \
  --session-id "$FROM_SESSION" \
  --body "$MESSAGE_BODY"
```

## Step 6: Confirm to the user

> Sent to orchestrator `<orch>` as role `<role>` (kind=<kind>). They'll see it next time they run `/check-messages`.

## Notes

- **No clipboard.** v2 of the orchestrator workflow uses the inbox directly. Messages are durable and re-readable.
- **Don't include credentials or large file dumps in the body.** Treat the body like a short Slack message — summary, not raw output.
- **This skill only writes.** Decisions, thread mutations, and resolutions belong to the orchestrator side. Don't run any other `ccc orchestrator` mutation here.
