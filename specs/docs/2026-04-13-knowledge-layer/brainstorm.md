# Knowledge layer for CCC

## Purpose

CCC currently ingests a lot of high-signal data (Granola transcripts, Slack messages, Gmail threads) and extracts exactly one thing from it: commitments Aaron has made. Everything else dissipates – decisions Aaron made or witnessed, positions he's taken, topics that span multiple channels, threads that were raised and then went quiet.

This brainstorm proposes a knowledge layer that compounds that signal over time. The system extracts structured artifacts (topics, decisions, positions, open threads) from the same source material, stores them durably in CCC's database, and surfaces synthesis insights (silence alerts, drift detection) proactively in the Command Center morning view. A future MCP server (out of scope for this first cut) will expose the corpus to Claude sessions for on-demand queries (memory, handoffs, decision lookup).

## Background

### Why now

Existing CCC architecture already has the pipework for this:

- Refresh pipeline that fetches sources in parallel, applies LLM extraction, persists to SQLite
- Plugin model with namespaced tables, event bus, and a Command Center that already shows synthesized signals (todo focus, suggestions)
- Source caching (`source_context` on todos) that already pulls and stores raw transcripts
- LLM access via the Claude CLI, which means LLM costs are absorbed by the Claude Code subscription rather than per-token

The missing piece is a layer that extracts non-actionable knowledge from the same source material and accumulates it indefinitely.

### Why not Obsidian / a wiki

Earlier discussion considered modeling this as an Obsidian vault following the "LLM Wiki" pattern (Karpathy, popularized by Defileo on X). The conclusion: Aaron is a high-bandwidth person whose data streams in passively – he's not actively curating a research corpus, so the wiki interaction model doesn't fit. The value is in the synthesis being available to other things (the morning view, future Claude sessions), not in browsing markdown files. SQLite, embedded in CCC, with optional future export to Obsidian, is a better fit.

### Why not a graph database

Knowledge is relational and truth shifts over time, which suggests a graph database. But:

- The high-priority jobs (silence alerts, decision journal, position tracking) are temporal and structured queries, not graph traversals
- The graph-shaped jobs (thread tracking, drift detection) are LLM synthesis problems, not query problems
- Graph databases add a server process to a single-binary Go app – meaningful operational complexity for a personal tool

A link table inside SQLite (`knowledge_edges`) gives graph-shaped data without the operational cost. Truth evolution is handled by never overwriting: new positions are new rows with timestamps, with optional "evolves" edges to predecessors.

## Jobs to be done

From the prior brainstorm session, scored by Aaron:

| # | Job | Score | Surface |
|---|-----|-------|---------|
| 1 | Position tracking | 5 (10/10) | MCP / Claude (future) |
| 2 | Silence alerts | 5 | CCC proactive |
| 3 | Decision journal | 5 | MCP / Claude (future) |
| 4 | Delegation with context | 5 | MCP / Skill (future) |
| 5 | Thread tracking | 4 | MCP / Skill (future) |
| 6 | Drift detection | 4 | CCC proactive |
| 7 | Smart search | 4 | MCP / Claude (future) |
| 8 | Pre-meeting context loading | 3 | (deprioritized) |
| 9 | Relationship continuity | 3 | (deprioritized) |
| 10 | Status report generation | 2 | (dropped – Aaron does not produce these) |

Aaron flagged a key dimension: most of these jobs (#1, #3, #4, #5, #7) are pull-mode – outputs he wants to ask for, not see in a UI. Two are push-mode (#2, #6) and belong in CCC's Command Center morning view as "heads up" alerts.

This split shapes the architecture.

## Architecture

### Three-layer design

```
                    Extraction (CCC refresh)
                            │
                            ▼
                  ┌─────────────────────┐
                  │  Knowledge tables   │
                  │  (topics, decisions,│
                  │   positions,        │
                  │   open threads)     │
                  └─────────┬───────────┘
                            │
              ┌─────────────┴─────────────┐
              ▼                           ▼
    ┌──────────────────┐        ┌──────────────────┐
    │ Proactive (CCC)  │        │ Pull (MCP server)│
    │                  │        │                  │
    │ – Silence alerts │        │ (out of scope –  │
    │ – Drift detect   │        │  future plan)    │
    │                  │        │                  │
    │ Morning view     │        │                  │
    │ insights section │        │                  │
    └──────────────────┘        └──────────────────┘
```

Three layers, three responsibilities:

1. **Extraction layer** – runs during the existing refresh pipeline. Sonnet processes new source material and emits structured knowledge artifacts. Writes to dedicated tables.
2. **Proactive surfacing layer** – analysis passes (silence detection, drift detection) over the knowledge tables. Writes "surfaced insights" to a contract table that Command Center reads.
3. **Pull layer (deferred)** – MCP server exposes the corpus to Claude sessions for on-demand queries. Skills wrap MCP tools into commands like `/handoff` and `/recall`. Out of scope for the first cut.

The knowledge tables are the single source of truth that both consumption surfaces share.

### Extraction pipeline

The extraction integrates into the existing `internal/refresh` pipeline as an additional pass on source material that already flows through. Concretely:

- For Granola: each new meeting transcript that gets processed for commitments is also processed for knowledge artifacts (one additional Sonnet call per meeting)
- For Slack: thread content already cached for commitment extraction is also processed
- For Gmail: the same source body that today generates commitment extraction is also processed

The extraction prompt asks Sonnet to identify four artifact types (defined below) and return them as structured JSON. The extraction is idempotent on `source_ref` – if a meeting is reprocessed, the artifacts are matched by their source reference and updated rather than duplicated.

**Cost ceiling:** Based on a real one-week sample (21 meetings, average ~17K input tokens per transcript), all-Sonnet extraction across Granola alone is ~$8/month at API rates. Adding Slack and Gmail roughly doubles that. Since CCC uses the Claude CLI rather than the API, this cost is absorbed by the Claude Code subscription. Decision: Sonnet for everything, no triage tier.

### Knowledge tables (data model)

Five tables, all owned by the new knowledge plugin via its migration namespace.

#### `knowledge_topics`

A recurring subject. Topics are deduplicated by name (case-insensitive) – the extraction prompt is instructed to reuse existing topic names when possible.

```
id            TEXT PRIMARY KEY
name          TEXT NOT NULL UNIQUE
description   TEXT
first_seen    TEXT NOT NULL
last_seen     TEXT NOT NULL
mention_count INTEGER NOT NULL DEFAULT 0
```

#### `knowledge_decisions`

A discrete decision made in a conversation. Decisions are append-only – a new decision on the same subject creates a new row, not an update.

```
id              TEXT PRIMARY KEY
title           TEXT NOT NULL
description     TEXT NOT NULL
alternatives    TEXT              -- alternatives considered (free text)
reasoning       TEXT              -- why this was chosen
participants    TEXT              -- JSON array of names
aaron_present   INTEGER NOT NULL  -- 1 if Aaron was in the conversation, 0 otherwise
source          TEXT NOT NULL     -- granola | slack | gmail
source_ref      TEXT NOT NULL     -- meeting id, message permalink, etc.
decided_at      TEXT NOT NULL     -- when the decision was made
extracted_at    TEXT NOT NULL
```

#### `knowledge_positions`

A stated stance someone took on something. Multiple positions on the same topic over time form an evolution chain (via `knowledge_edges`).

```
id            TEXT PRIMARY KEY
holder        TEXT NOT NULL     -- "Aaron" or another name
position      TEXT NOT NULL     -- the stance itself
topic_id      TEXT              -- FK to knowledge_topics, nullable
source        TEXT NOT NULL
source_ref    TEXT NOT NULL
stated_at     TEXT NOT NULL
extracted_at  TEXT NOT NULL
```

#### `knowledge_open_threads`

Something raised but not resolved. Open threads are mutable – the extraction pipeline may update `last_activity_at` when a thread is mentioned again, or close it when a decision resolves it.

```
id              TEXT PRIMARY KEY
description     TEXT NOT NULL
blocking_on     TEXT             -- what's needed to resolve (free text)
topic_id        TEXT             -- FK to knowledge_topics, nullable
first_raised_at TEXT NOT NULL
last_activity_at TEXT NOT NULL
status          TEXT NOT NULL    -- open | resolved | abandoned
resolved_by     TEXT             -- FK to knowledge_decisions if applicable
```

#### `knowledge_edges`

Relationships between artifacts. This is the "graph-shaped data in a relational store" that lets truth evolve without losing history.

```
from_id       TEXT NOT NULL
from_type     TEXT NOT NULL  -- topic | decision | position | thread
to_id         TEXT NOT NULL
to_type       TEXT NOT NULL
relationship  TEXT NOT NULL  -- evolves | contradicts | relates_to | resolves | mentions
created_at    TEXT NOT NULL
PRIMARY KEY (from_id, to_id, relationship)
```

Examples of edges:

- A new position has an `evolves` edge from itself to a prior position by the same holder on the same topic
- A decision has a `resolves` edge to an open thread it closes
- Two positions on the same topic by different holders may have a `contradicts` edge (extraction-time inference)
- A topic has many `mentions` edges from decisions, positions, and threads that reference it

#### `knowledge_surfaced_insights`

The contract table between the knowledge plugin and Command Center. Knowledge plugin writes insights here; Command Center reads.

```
id            TEXT PRIMARY KEY
type          TEXT NOT NULL  -- silence_alert | drift_detection
title         TEXT NOT NULL
body          TEXT NOT NULL
source_refs   TEXT           -- JSON array of related artifact IDs
priority      INTEGER NOT NULL DEFAULT 50
surfaced_at   TEXT NOT NULL
dismissed_at  TEXT
```

### Proactive surfacing (silence alerts and drift detection)

Two analysis passes run during refresh, after extraction completes.

**Silence alerts** scan `knowledge_topics` and `knowledge_open_threads` for items where `last_seen` / `last_activity_at` is older than a configurable threshold (default: 10 days for topics, 7 days for open threads) AND the item was previously active (mention_count > 3 for topics; first raised by Aaron for threads). For each match, an insight row is written with type `silence_alert`.

**Drift detection** is harder and uses Sonnet. The pass selects positions Aaron has held in the last 60 days and asks Sonnet: "For each of these positions, is there evidence in newer decisions or positions that the plan has shifted away from this stance?" When the answer is yes, an insight row is written with type `drift_detection`, including the original position and the evidence of shift.

Both passes are idempotent: existing insights are matched by a deterministic ID derived from the underlying artifact IDs and re-evaluated rather than duplicated. An insight whose underlying condition no longer holds (the topic was mentioned again, the open thread was resolved) is removed.

When new insights are written, the knowledge plugin publishes a `knowledge.insights.updated` event on the bus.

### Communication between knowledge plugin and Command Center

The `knowledge_surfaced_insights` table is the entire interface. No Go imports between plugins, no direct function calls.

**Knowledge plugin's responsibilities:**

- Owns the schema and migrations for all knowledge tables, including `knowledge_surfaced_insights`
- Runs extraction during refresh
- Runs analysis passes (silence, drift) during refresh
- Writes rows to `knowledge_surfaced_insights`
- Publishes `knowledge.insights.updated` events

**Command Center's responsibilities:**

- Subscribes to `knowledge.insights.updated` and triggers a re-query
- In its View() function, queries `knowledge_surfaced_insights` for active (non-dismissed) rows ordered by priority
- Renders an "Insights" section in the morning view
- Handles a dismiss key that updates `dismissed_at` on the selected insight

If the knowledge plugin is disabled, the table is empty and CC's insights section is empty (or hidden). No code in CC depends on knowledge plugin code.

### Refresh pipeline integration

The existing refresh pipeline (`internal/refresh/refresh.go`) runs source fetches in parallel, then a sequence of post-processing steps (merge, suggestions, routing, dedup, source context fetch, save). The knowledge layer adds two new post-processing steps after the existing source context fetch:

1. **Knowledge extraction** – for each new source item with cached `source_context`, run the knowledge extraction prompt and write artifacts to the knowledge tables
2. **Insight analysis** – run silence detection and drift detection over the knowledge tables, write to `knowledge_surfaced_insights`

Both steps run sequentially after extraction (extraction must complete before analysis can run). They can be skipped via config if the knowledge plugin is disabled.

## Scope of first cut

### Included

- Knowledge plugin scaffold (registers with plugin registry, owns migrations)
- Five knowledge tables (`knowledge_topics`, `knowledge_decisions`, `knowledge_positions`, `knowledge_open_threads`, `knowledge_edges`) plus the contract table (`knowledge_surfaced_insights`)
- Knowledge extraction pass added to refresh pipeline (Sonnet, processes Granola transcripts, Slack threads, Gmail bodies that already have cached `source_context`)
- Silence alert analysis pass
- Drift detection analysis pass (Sonnet)
- `knowledge.insights.updated` event publication
- Command Center integration: insights section in morning view, dismiss key
- 1-month backfill (one-time): on first run after the knowledge plugin is enabled, fetch the last 30 days of Granola meetings, Slack messages, and Gmail threads (within the source-specific limits already in place) and run extraction on all of them. Seeds the corpus so silence alerts and drift detection have history to work with on day one. The window is 30 days rather than 14 because Aaron was on vacation during part of the recent past, so a longer window is needed to capture meaningful baseline activity.

### Deferred (future plans)

- MCP server hosted by CCC daemon, exposing read tools for the knowledge corpus
- Skills (`/handoff`, `/recall`, `/positions`, `/decisions`) that wrap MCP tools
- Browsing UI in CCC (a "Knowledge" tab)
- Position tracking visualization (timeline of how a stance has evolved)
- Other artifact types (Person, Question, Insight)
- Periodic "lint" pass that finds contradictions and stale data
- Optional Obsidian export

### Out of scope entirely (dropped)

- Status report generation – Aaron does not produce these
- Pre-meeting context loading – low score, complex to implement well
- Relationship continuity – low score, can be partially served by future MCP queries

## Cost

Based on real measurement of a one-week sample (Apr 6–10, 2026):

- 21 Granola meetings, average ~17K input tokens per transcript
- All-Sonnet extraction: ~$2.10/week, ~$8.40/month, ~$110/year at API rates
- With Slack and Gmail: ~$300/year worst case

Since CCC routes LLM calls through the `claude` CLI rather than the Anthropic API, this cost is absorbed by Aaron's Claude Code subscription. The relevant constraint is rate limits, not dollars. Sonnet calls are sequential within refresh (one per source item), which is rate-limit friendly.

The 1-month backfill is a one-time cost: ~80–90 meetings × $0.10 each + Slack/Gmail = roughly $20–40 one-time absorbed by subscription.

## Risks and mitigations

**Extraction quality** – Sonnet might produce noisy or duplicate artifacts, especially for topics. Mitigation: extraction prompt is given the existing topic names so it can reuse them; idempotence on `source_ref` prevents per-meeting duplication; manual review of the first weeks of insights before trusting drift detection.

**Surfaced insight noise** – silence alerts could fire on things that don't actually matter. Mitigation: thresholds (mention_count > 3 for topics, "first raised by Aaron" for threads) keep the bar high; insights have a dismiss action; priority field allows future tuning.

**Drift detection cost and accuracy** – this is the heaviest analysis pass, and the most likely to misfire. Mitigation: scoped to positions in the last 60 days only; runs once per refresh, not per source item; can be disabled via config without affecting the rest of the system.

**Backfill failure modes** – fetching 2 weeks of data could partially fail. Mitigation: backfill is resumable – it tracks per-source progress and can pick up where it left off. If a source's backfill fails entirely, the rest of the system still works on go-forward data.

**Source coverage gaps** – Calendar and GitHub are not included in extraction (they are structured data, not narrative). This is intentional but worth noting: the corpus represents conversations and documents, not calendar/PR history.

## Open questions

- **Edge inference at extraction time vs. periodic pass.** The first cut infers `evolves` edges at extraction time (when adding a new position, look for prior positions by the same holder on the same topic). `contradicts` and `relates_to` edges may benefit from a periodic batch inference pass. Decision deferred to implementation.

- **Insight retention.** When does a dismissed insight get garbage collected? Probably 30 days, but this can be tuned later.

- **Aaron-only filtering.** Should drift detection only consider Aaron's positions, or also positions of others he interacts with frequently (Zach in particular)? First cut: Aaron only. Future expansion possible.
