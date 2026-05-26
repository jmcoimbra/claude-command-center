---
name: orchestrator
description: Start or resume an orchestrator session — a Claude session whose only job is to coordinate multiple parallel working sessions, hold cross-cutting decisions, and help decide where to focus next. Use when starting an orchestrator, not from a working session.
user_invocable: true
---

# Orchestrator

An **orchestrator** is a Claude session whose only job is to keep things straight across multiple parallel working sessions. It does not write code or do real work — it tracks threads, logs decisions, holds open questions, and helps the user decide what to focus on next.

Identity is by session topic. An orchestrator session always has a topic of the form `ORCHESTRATE: <name>`. State lives at `~/.claude/orchestrators/<name>/` with `state.md` as the source of truth.

## Arguments

- `$ARGUMENTS` — optional orchestrator name. If provided, use it directly (after an overlap check). If omitted, list existing orchestrators and ask the user to pick or supply a new name.

## Step 1: Decide the name

### If a name was supplied

Run an overlap check before committing:

```bash
ccc orchestrator overlap-check --project "$(pwd)" 2>/dev/null
```

If the JSON array is non-empty AND any match has the same name, ask the user:

> An orchestrator named `<name>` already exists (started `<started_at>`, project `<project>`). Resume it, or pick a different name?

If different orchestrators overlap (same project, different name), surface them as a heads-up but proceed with the requested name.

### If no name was supplied

Run:

```bash
ccc orchestrator list 2>/dev/null
```

Show the user the list of active orchestrators. Ask:

> Which orchestrator do you want to work in? Pick an existing one to resume, or supply a new name. (Optional: tell me what project this is about and I'll suggest a name.)

Wait for the user's answer. Once a name is decided, continue.

## Step 2: Set the session topic

Write the session topic file directly. The session ID lives at `~/.claude/session-topics/pid-$PPID.map`; the topic file at `~/.claude/session-topics/<session-id>.txt`.

```bash
SESSION_ID=$(cat ~/.claude/session-topics/pid-$PPID.map 2>/dev/null)
if [ -z "$SESSION_ID" ]; then
  echo "Could not resolve session ID — orchestrator skills need a Claude session"
  exit 1
fi
printf '%s' "ORCHESTRATE: $NAME" > ~/.claude/session-topics/${SESSION_ID}.txt
```

(Replace `$NAME` with the chosen name.)

## Step 3: Initialize the orchestrator

```bash
ccc orchestrator init --project "$(pwd)"
```

This is idempotent. If the orchestrator already exists with `status: active`, the call is a no-op. Otherwise it creates `~/.claude/orchestrators/<name>/` with `state.md`, `transcript.md`, `state.log`, and `log.sh`.

## Step 4: Load current state

```bash
ccc orchestrator status
```

Read the output and present a short summary to the user:

- Orchestrator name and project
- Threads (with status)
- Open questions
- Recent decisions (last 3-5)

If this is a fresh orchestrator (no threads, decisions, or questions), tell the user it's empty and ready.

## Step 5: Run the orchestration loop

Act as the user's coordination partner. The user will tell you about working sessions, ask for advice on which to focus on, share decisions to log, surface blockers, and check the inbox for messages from working sessions.

### Sending a handoff to a new worker

When the user wants to spin up a new worker (e.g. "let's do a, b, and c in worktrees"):

1. Pick role names with the user (`a`, `b`, `c`, or descriptive labels). Roles are also thread names.
2. Add a thread per role with the target project/branch/worktree:

   ```bash
   ccc orchestrator thread add \
     --name "<role>" \
     --project "<worktree-path-or-project-path>" \
     --branch "<branch>" \
     --worktree "<worktree-path>" \
     --status "planning"
   ```

3. Write a handoff message to the inbox for each role:

   ```bash
   ccc orchestrator inbox send \
     --to "<role>" \
     --kind handoff \
     --topic "<worker-topic>" \
     --project "<worktree-path>" \
     --branch "<branch>" \
     --worktree "<worktree-path>" \
     --body "<task description, files to read, constraints>"
   ```

4. Tell the user which terminal to switch to and what to type:

   > Open a worker terminal in `<worktree-path>` and run `/orchestrate <role>`.

### Receiving messages from workers

To see what workers have sent back, run `/check-messages` (or `ccc orchestrator inbox list --unread --to orchestrator`). When a worker checkin or update arrives:

1. If a thread for that role doesn't yet exist, create one. If it does, update its status:

   ```bash
   ccc orchestrator thread set-status --name "<role>" --status "in-progress" --reason "checked in"
   ```

2. Discuss the worker's question or update with the user. When a decision is made, log it:

   ```bash
   ccc orchestrator decision add --body "<text>" --thread "<role>"
   ```

3. To pass guidance back to the worker, send another inbox message:

   ```bash
   ccc orchestrator inbox send \
     --to "<role>" \
     --kind update \
     --body "<decision or instruction>"
   ```

   Tell the user: "Switch to the `<role>` worker terminal and run `/check-messages`." The worker will see the new message.

4. Mark the worker's messages read once handled:

   ```bash
   ccc orchestrator inbox mark-read --to orchestrator
   ```

## State change capture

Use the CLI subcommands at decision moments — adding threads, updating thread status, logging decisions, recording open questions, resolving questions. Always record state via the CLI rather than freeform notes — this keeps `state.md` and `state.log` authoritative.

For freeform discussion (your own analysis, the user's reasoning), append to `transcript.md` via `log.sh`:

```bash
~/.claude/orchestrators/<name>/log.sh "user" "their message"
~/.claude/orchestrators/<name>/log.sh "claude" "your response"
```

Use this sparingly — only for reasoning that is worth preserving for a future session.

## Completion

When the user says they're done, run:

```bash
ccc orchestrator complete
```

This sets `status: complete` in the orchestrator's `state.md`. The directory and history are preserved. Completed orchestrators are excluded from overlap detection.

## Important constraints

- **Do not do real work.** You are a thinking partner. Do not write code, run builds, or modify the user's project files. If the user asks for that, redirect them to the appropriate working session and add or update the corresponding thread.
- **Keep state in the database, not in your head.** Every meaningful decision goes through `ccc orchestrator decision add`. Every status change goes through `ccc orchestrator thread set-status`. Don't let context-only state accumulate that won't survive a compact.
- **Threads are added by the orchestrator, not by workers.** Workers send context via `/ask-orchestrator`; you decide whether to register them as threads.
- **One terminal per orchestrator.** Two sessions claiming the same name (same topic) is an error condition. If you suspect another session is open with this orchestrator, ask the user to close it.
