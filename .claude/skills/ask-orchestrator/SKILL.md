---
name: ask-orchestrator
description: From a working session, prepare a clipboard handoff to the orchestrator session. Detects local context (project, branch, session id) and copies a structured "HANDOFF TO ORCHESTRATOR" block. Use when stuck on a decision the orchestrator should weigh in on, or when the orchestrator should know about a status change.
---

# Ask Orchestrator

Used from a **working session** (not from the orchestrator itself). Prepares a clipboard block that the user pastes into their orchestrator terminal so the orchestrator can decide, log, or coordinate.

## Arguments

- `$ARGUMENTS` — optional question or context to send. If omitted, ask the user what they want the orchestrator to know.

## Step 1: Confirm we're not already in the orchestrator

```bash
SESSION_ID=$(cat ~/.claude/session-topics/pid-$PPID.map 2>/dev/null)
TOPIC=""
if [ -n "$SESSION_ID" ]; then
  TOPIC=$(cat ~/.claude/session-topics/${SESSION_ID}.txt 2>/dev/null)
fi
echo "topic: $TOPIC"
```

If `$TOPIC` starts with `ORCHESTRATE: `, the user is invoking this from inside the orchestrator session. Tell them:

> You're already in the orchestrator (`<topic>`). `/ask-orchestrator` is meant for worker sessions. Use the orchestrator CLI directly here.

Then stop.

## Step 2: Get the question

If `$ARGUMENTS` is non-empty, use it. Otherwise ask:

> What would you like to send to the orchestrator?

Wait for the answer.

## Step 3: Gather local context

```bash
PROJECT=$(pwd)
BRANCH=$(git branch --show-current 2>/dev/null || echo "")
REPO=$(basename "$(git rev-parse --show-toplevel 2>/dev/null || pwd)")
```

Decide on a "from-session-id" the orchestrator can reference. Prefer `$CCC_SESSION_ID` (set by ccc when launching) if present, otherwise fall back to `$SESSION_ID` (the one resolved in Step 1 from the topic file).

## Step 4: Find the destination orchestrator

```bash
ccc orchestrator list --json 2>/dev/null
```

Parse the JSON.

- **Zero active orchestrators**: Tell the user there's no orchestrator running. Print the handoff block to the screen anyway so they can save it, and suggest:
  > No orchestrator is active. Start one with `/orchestrator <name>` in another terminal, then re-run this command.
- **One active orchestrator**: Use its name as the destination. Skip to Step 5.
- **Multiple active**: Show the list and ask the user to pick by name.

## Step 5: Build the handoff block

Emit (and copy to clipboard) a block of this exact shape:

```
─── HANDOFF TO ORCHESTRATOR: <orchestrator-name> ───
  From session: <from-session-id>
  Project: <PROJECT>
  Repo:    <REPO>
  Branch:  <BRANCH>
  Topic:   <TOPIC or "(none)">
  ────────────────────────────────────────────────
  Question / context:
  <the question text>

  Recent context (optional, only if the user pre-provided it):
  <recent context, e.g., what they were working on>
```

Copy it to the clipboard:

```bash
printf '%s' "$BLOCK" | pbcopy
```

## Step 6: Tell the user what to do

> Copied a handoff block to your clipboard for orchestrator `<name>`. Switch to that terminal and paste. The orchestrator will register or update the thread and respond.

If there were zero orchestrators active in Step 4, just print the block to the screen with the suggestion to start one.

## Notes

- **This is human-mediated messaging.** There is no programmatic delivery. The user copies and pastes between terminals. A future version may add a worker-side hook that delivers messages on the next prompt submit, but v1 is clipboard-only.
- **Don't auto-update local state.** Don't run any `ccc` commands that mutate orchestrator state — that's the orchestrator's job. This skill only reads (`list --json`) and prepares the clipboard.
- **Don't include credentials, secrets, or large file contents in the block.** Treat it like a Slack message to your orchestrator persona — the body is human-readable summary, not raw output.
