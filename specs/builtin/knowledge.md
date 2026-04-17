# SPEC: Knowledge Plugin (built-in)

## Purpose

Extracts structured knowledge artifacts (topics, decisions, positions, open threads) from source material that already flows through the CCC refresh pipeline (Granola transcripts, Slack threads, Gmail bodies). Persists them durably in SQLite and surfaces proactive synthesis insights (silence alerts, drift detection) to the Command Center morning view. The corpus compounds over time and will serve a future MCP server for on-demand queries (out of scope for this first cut).

## Slug: `knowledge`

## Interface

- **Inputs**: Source material via `source_context` on todos (populated by the refresh pipeline's context-fetch step), existing knowledge tables for dedup and edge inference, LLM (Sonnet) for extraction and drift detection
- **Outputs**: Rows in knowledge tables (`knowledge_topics`, `knowledge_decisions`, `knowledge_positions`, `knowledge_open_threads`, `knowledge_edges`); rows in the contract table (`knowledge_surfaced_insights`); `knowledge.insights.updated` events on the bus
- **Dependencies**: `internal/refresh` pipeline (triggers extraction and analysis), `llm.LLM` (Sonnet), `plugin.EventBus`, SQLite database

## Tables

The knowledge plugin owns six tables via its migration namespace. All IDs are `TEXT PRIMARY KEY` (8-char random hex via `db.GenID()`).

### `knowledge_topics`

A recurring subject. Deduplicated by name (case-insensitive unique constraint).

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Unique identifier |
| `name` | TEXT NOT NULL UNIQUE | Topic name (case-insensitive dedup) |
| `description` | TEXT | Brief description of the topic |
| `first_seen` | TEXT NOT NULL | RFC3339 timestamp of first mention |
| `last_seen` | TEXT NOT NULL | RFC3339 timestamp of most recent mention |
| `mention_count` | INTEGER NOT NULL DEFAULT 0 | Number of times mentioned across sources |

### `knowledge_decisions`

A discrete decision made in a conversation. Append-only – a new decision on the same subject creates a new row.

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Unique identifier |
| `title` | TEXT NOT NULL | Short decision title |
| `description` | TEXT NOT NULL | What was decided |
| `alternatives` | TEXT | Alternatives considered (free text) |
| `reasoning` | TEXT | Why this was chosen |
| `participants` | TEXT | JSON array of participant names |
| `aaron_present` | INTEGER NOT NULL | 1 if Aaron was in the conversation |
| `source` | TEXT NOT NULL | granola, slack, or gmail |
| `source_ref` | TEXT NOT NULL | Meeting ID, message permalink, etc. |
| `decided_at` | TEXT NOT NULL | When the decision was made |
| `extracted_at` | TEXT NOT NULL | When this row was created |

### `knowledge_positions`

A stated stance someone took. Multiple positions on the same topic over time form an evolution chain via `knowledge_edges`.

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Unique identifier |
| `holder` | TEXT NOT NULL | "Aaron" or another participant name |
| `position` | TEXT NOT NULL | The stance itself |
| `topic_id` | TEXT | FK to `knowledge_topics` (nullable) |
| `source` | TEXT NOT NULL | granola, slack, or gmail |
| `source_ref` | TEXT NOT NULL | Meeting ID, message permalink, etc. |
| `stated_at` | TEXT NOT NULL | When the position was stated |
| `extracted_at` | TEXT NOT NULL | When this row was created |

### `knowledge_open_threads`

Something raised but not resolved. Mutable – status can change from open to resolved or abandoned.

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Unique identifier |
| `description` | TEXT NOT NULL | What the open thread is about |
| `blocking_on` | TEXT | What's needed to resolve (free text) |
| `topic_id` | TEXT | FK to `knowledge_topics` (nullable) |
| `first_raised_by` | TEXT | Who first raised this thread (e.g. "Aaron") |
| `source` | TEXT NOT NULL | granola, slack, or gmail |
| `source_ref` | TEXT NOT NULL | Meeting ID, message permalink, etc. |
| `first_raised_at` | TEXT NOT NULL | When it was first mentioned |
| `last_activity_at` | TEXT NOT NULL | When it was last mentioned |
| `status` | TEXT NOT NULL | open, resolved, or abandoned |
| `resolved_by` | TEXT | FK to `knowledge_decisions` if applicable |

### `knowledge_edges`

Relationships between artifacts. Graph-shaped data in a relational store – lets truth evolve without losing history.

| Column | Type | Description |
|--------|------|-------------|
| `from_id` | TEXT NOT NULL | Source artifact ID |
| `from_type` | TEXT NOT NULL | topic, decision, position, or thread |
| `to_id` | TEXT NOT NULL | Target artifact ID |
| `to_type` | TEXT NOT NULL | topic, decision, position, or thread |
| `relationship` | TEXT NOT NULL | evolves, contradicts, relates_to, resolves, or mentions |
| `created_at` | TEXT NOT NULL | When the edge was created |

Composite primary key: `(from_id, to_id, relationship)`.

Edge examples:

- A new position `evolves` a prior position by the same holder on the same topic
- A decision `resolves` an open thread it closes
- Two positions on the same topic by different holders may `contradicts` each other
- Topics have many `mentions` edges from decisions, positions, and threads

### `knowledge_surfaced_insights`

The contract table between the knowledge plugin and Command Center. Knowledge plugin writes; Command Center reads.

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT PRIMARY KEY | Deterministic ID derived from underlying artifact IDs |
| `type` | TEXT NOT NULL | silence_alert or drift_detection |
| `title` | TEXT NOT NULL | Human-readable insight title |
| `body` | TEXT NOT NULL | Insight detail text |
| `source_refs` | TEXT | JSON array of related artifact IDs |
| `priority` | INTEGER NOT NULL DEFAULT 50 | Priority for ordering (lower = higher priority) |
| `surfaced_at` | TEXT NOT NULL | When the insight was first surfaced |
| `dismissed_at` | TEXT | When the user dismissed the insight (nullable) |

## Artifact Types

The knowledge plugin extracts four artifact types from source material:

- **Topic**: A recurring subject that appears across multiple conversations. Topics are deduplicated by name – the extraction prompt is given existing topic names to reuse. When a topic is mentioned again, `last_seen` and `mention_count` are updated.

- **Decision**: A discrete choice made in a conversation. Decisions are append-only – a new decision on the same subject creates a new row. Captures alternatives considered, reasoning, participants, and whether Aaron was present.

- **Position**: A stated stance someone took on something. Positions are never overwritten – new positions create new rows with `evolves` edges to predecessors by the same holder on the same topic. This preserves the full evolution of a stance over time.

- **Open thread**: Something raised but not resolved. Open threads are mutable – the extraction pipeline updates `last_activity_at` when mentioned again, or transitions to `resolved` when a decision closes it. Qualifying threads (first raised by Aaron) are candidates for silence alerts.

## Behavior

### Lifecycle

1. Plugin registers with the plugin registry alongside other built-in plugins
2. During `Init`, runs migrations to create all six tables
3. Does not expose a visible tab in the TUI (no Knowledge tab in this first cut) – `View()` returns empty, key handling is no-op
4. The plugin exists primarily to own the tables and the extraction/analysis passes that run during refresh

### Extraction pass

Runs during the refresh pipeline as a post-processing step after source-context fetch:

1. Iterates over todos with `source_context` populated (Granola, Slack, Gmail sources)
2. For each, calls the extraction function with the cached source content, source type, and source reference
3. The extraction function calls Sonnet to identify artifacts and returns structured JSON
4. Artifacts are written to the knowledge tables with idempotence on `source_ref` + artifact-type-specific dedup
5. Edge inference runs at extraction time: when inserting a new position, looks up prior positions by the same holder on the same topic and creates `evolves` edges
6. Topic deduplication: the extraction prompt receives existing topic names so it can reuse them; the `name` column has a unique constraint

See `specs/core/knowledge-extraction.md` for full extraction behavior.

### Analysis passes

Run during refresh after extraction completes. Both write to `knowledge_surfaced_insights`.

1. **Silence alerts**: Scans topics and open threads for items that have gone quiet despite being previously active. Deterministic insight IDs prevent duplication. Insights whose condition no longer holds are removed.
2. **Drift detection**: Selects Aaron's positions from the last 60 days and asks Sonnet whether newer decisions/positions show evidence of shift. Writes insights when drift is detected.

See `specs/core/knowledge-analysis.md` for full analysis behavior.

After any writes to `knowledge_surfaced_insights`, publishes a `knowledge.insights.updated` event on the bus (payload: none – consumers re-query the table).

### Contract with Command Center

The `knowledge_surfaced_insights` table is the entire interface between plugins. No Go imports between them.

- **Knowledge plugin writes**: Insight rows from silence alerts and drift detection
- **Command Center reads**: Queries active (non-dismissed) rows ordered by priority
- **Communication**: `knowledge.insights.updated` event signals Command Center to re-query
- **Dismiss**: Command Center updates `dismissed_at` directly on the table

If the knowledge plugin is disabled, the table is empty and the Command Center's insights section is empty or hidden. No code in Command Center depends on knowledge plugin code.

### 1-month backfill

On first run after the knowledge plugin is enabled (detected by checking a `knowledge_backfill_state` row in the migration), the plugin fetches the last 30 days of source content and runs extraction on all of it. This seeds the corpus so silence alerts and drift detection have history to work with on day one.

- **Window**: 30 days (longer than the default 14-day refresh window because Aaron was on vacation during part of the recent past)
- **Resumability**: Per-source progress is tracked in `knowledge_backfill_state` so a partial failure can resume from the last successful source/timestamp
- **One-shot**: Once `knowledge_backfill_state` shows completion, subsequent refreshes operate on go-forward data only
- **Cost guard**: Logs estimated API call count before starting so the user can see the expected magnitude
- **CLI trigger**: `ccc --backfill-knowledge` manually re-triggers backfill if needed

### Configuration

- Knowledge plugin can be enabled/disabled via config (when disabled, extraction and analysis passes are skipped)
- Drift detection can be independently disabled via config without affecting silence alerts or extraction
- Silence alert thresholds are configurable (default: 10 days for topics, 7 days for open threads)

## Test Cases

### Migration

- All six tables are created with correct columns, indexes, and constraints
- Indexes exist on `knowledge_topics.name`, `knowledge_positions(holder, topic_id)`, `knowledge_open_threads.last_activity_at`, `knowledge_open_threads(first_raised_by, last_activity_at)`, `knowledge_surfaced_insights.dismissed_at`
- `knowledge_backfill_state` row is created during migration

### Extraction

- Extraction from a Granola transcript produces topics, decisions, positions, and threads
- Extraction from a Slack thread produces the same artifact types
- Extraction from a Gmail body produces the same artifact types
- Running extraction twice on the same `source_ref` does not duplicate rows (idempotence)
- Existing topic names are reused (case-insensitive dedup on `knowledge_topics.name`)
- `mention_count` and `last_seen` are updated when a topic is mentioned again
- Open thread `last_activity_at` is updated when mentioned again

### Edge inference

- Adding a position by the same holder on the same topic creates an `evolves` edge to the prior position
- Adding a position on a topic with no prior position by the same holder creates no `evolves` edge
- Edges have correct `from_type`, `to_type`, and `relationship` values

### Insight contract

- Writing to `knowledge_surfaced_insights` publishes `knowledge.insights.updated` event
- Dismiss updates `dismissed_at` without removing the row
- Deterministic insight IDs: re-running analysis with same conditions produces same insight IDs
- Insight whose underlying condition no longer holds is removed

### Backfill

- Backfill runs on first plugin enable (no `knowledge_backfill_state` completion marker)
- Backfill respects the 30-day window
- Backfill is resumable: per-source progress is tracked
- Backfill does not re-run on subsequent refreshes after completion
- CLI flag manually re-triggers backfill

### Refresh integration

- Extraction pass runs after source-context fetch when knowledge plugin is enabled
- Analysis pass runs after extraction when knowledge plugin is enabled
- Both passes are skipped when the knowledge plugin is disabled
- Extraction errors for one source do not block extraction for others

### Error cases

- LLM extraction returns malformed JSON – logged and skipped, other sources still processed
- LLM extraction returns empty artifacts – no rows written, no error
- Database write failure during extraction – logged, does not crash refresh
- Backfill partial failure – resumes from last successful point on next run
