# SPEC: Knowledge Extraction

## Purpose

Extracts structured knowledge artifacts (topics, decisions, positions, open threads) from source material during the CCC refresh pipeline. Runs after source-context fetch, processes todos with cached `source_context`, and writes artifacts to the knowledge tables owned by the knowledge plugin.

## Interface

- **Inputs**:
  - `source_context` (string) – the cached raw content (transcript, thread, email body)
  - `source_type` (string) – "granola", "slack", or "gmail"
  - `source_ref` (string) – the source reference (meeting ID, message permalink, etc.)
  - `existing_topic_names` ([]string) – current topic names for dedup guidance
  - `db` (*sql.DB) – database connection for reading/writing knowledge tables
  - `llm` (llm.LLM) – Sonnet model for extraction
- **Outputs**: Rows written to `knowledge_topics`, `knowledge_decisions`, `knowledge_positions`, `knowledge_open_threads`, and `knowledge_edges`
- **Dependencies**: Knowledge plugin tables (must exist), LLM (Sonnet)

## Behavior

### Trigger

Extraction runs during the refresh pipeline as a post-processing step after source-context fetch. For each todo with a populated `source_context` field (from Granola, Slack, or Gmail sources), the extraction function is called with the cached content. Calendar and GitHub sources are not processed – they are structured data, not narrative content.

### LLM model

Sonnet is used for all extraction. There is no triage tier – this was an explicit design decision based on cost analysis. At CLI rates the cost is absorbed by the Claude Code subscription. The relevant constraint is rate limits, not dollars.

### Extraction prompt structure

The prompt asks Sonnet to analyze the source content and return a JSON object with four arrays:

```json
{
  "topics": [
    {
      "name": "string (reuse existing name if applicable)",
      "description": "string"
    }
  ],
  "decisions": [
    {
      "title": "string",
      "description": "string",
      "alternatives": "string or null",
      "reasoning": "string or null",
      "participants": ["string"],
      "aaron_present": true,
      "decided_at": "RFC3339 timestamp"
    }
  ],
  "positions": [
    {
      "holder": "string (e.g. 'Aaron', 'Zach')",
      "position": "string",
      "topic_name": "string or null (references a topic from the topics array)",
      "stated_at": "RFC3339 timestamp"
    }
  ],
  "open_threads": [
    {
      "description": "string",
      "blocking_on": "string or null",
      "topic_name": "string or null",
      "status": "open",
      "first_raised_by": "string"
    }
  ]
}
```

The prompt includes:

1. The source content in XML tags (`<source_content>`)
2. The source type and reference for context
3. The list of existing topic names with instructions to reuse them when applicable (case-insensitive match)
4. Instructions to identify only substantive artifacts – not every passing mention

### Topic deduplication

The extraction prompt receives all existing topic names from `knowledge_topics`. Sonnet is instructed to reuse an existing name when the subject matches (case-insensitive). The `name` column on `knowledge_topics` has a UNIQUE constraint – if the LLM returns a new name that collides, the existing row is updated (`last_seen`, `mention_count`) rather than duplicated.

When a topic is referenced by the extraction output:

1. Look up the topic by name (case-insensitive)
2. If found: update `last_seen` to now, increment `mention_count`
3. If not found: insert a new row with `first_seen` and `last_seen` set to now, `mention_count = 1`

### Idempotence

Artifacts are matched by `source_ref` combined with artifact-type-specific dedup:

- **Topics**: Deduplicated by name (unique constraint). Re-extraction updates `last_seen` and `mention_count`.
- **Decisions**: Matched by `source_ref` + `title`. If a decision with the same `source_ref` and similar title already exists, it is skipped rather than duplicated.
- **Positions**: Matched by `source_ref` + `holder` + `topic_id`. If a position with the same holder and topic from the same source already exists, it is skipped.
- **Open threads**: Matched by `source_ref` + `description` similarity. If a matching thread exists, `last_activity_at` is updated. If the extraction indicates the thread is resolved, `status` is updated to "resolved".

Re-running extraction on the same source content produces the same artifacts without duplication.

### Edge inference at extraction time

When inserting a new position:

1. Query `knowledge_positions` for prior positions by the same `holder` on the same `topic_id`
2. If a prior position exists, create an `evolves` edge from the new position to the most recent prior position
3. The `evolves` edge captures how a stance has shifted over time

This is performed at extraction time (not as a periodic batch pass) because the relationship between consecutive positions by the same holder on the same topic is deterministic and does not require cross-source reasoning.

Other edge types (`contradicts`, `relates_to`, `resolves`, `mentions`) may be inferred by the extraction prompt directly or by a future periodic analysis pass.

### Source coverage

| Source | Artifact types | Notes |
|--------|---------------|-------|
| Granola | All four | Meeting transcripts with speaker labels |
| Slack | All four | Thread content with conversation context |
| Gmail | All four | Email thread bodies |
| Calendar | Not processed | Structured data, not narrative |
| GitHub | Not processed | Structured data, not narrative |

### Refresh pipeline integration

The extraction step is added to `internal/refresh/refresh.go` after the existing source-context fetch step (step 11 in the current pipeline):

1. Check if the knowledge plugin is enabled via config
2. If enabled, iterate over todos with `source_context` populated
3. For each, call the extraction function
4. Extraction errors are logged but do not block other todos or the rest of the pipeline

The extraction step runs sequentially (one source item at a time) which is rate-limit friendly for Sonnet.

## Test Cases

### Happy path

- Extraction from a Granola transcript returns topics, decisions, positions, and open threads
- Extraction from a Slack thread returns the same artifact types
- Extraction from a Gmail body returns the same artifact types
- Extracted artifacts are written to the correct tables with correct field values
- Topics are deduplicated by name – existing topic's `mention_count` increments and `last_seen` updates
- New topics get `mention_count = 1` and matching `first_seen`/`last_seen`

### Idempotence

- Running extraction twice on the same `source_ref` does not duplicate decisions, positions, or threads
- Running extraction twice on the same `source_ref` updates topic `mention_count` only once (not double-counted)
- Open thread re-extraction updates `last_activity_at` without creating a duplicate row

### Edge inference

- New position by the same holder on the same topic creates an `evolves` edge to the prior position
- New position by the same holder on a different topic creates no edge
- New position by a different holder on the same topic creates no `evolves` edge
- First position on a topic by a holder creates no edge (no predecessor)
- Edge has correct `from_id`, `from_type`, `to_id`, `to_type`, `relationship`, and `created_at`

### Error cases

- LLM returns malformed JSON – logged, no artifacts written, other sources still processed
- LLM returns empty arrays – no rows written, no error
- LLM returns a topic name that collides with an existing one (case mismatch) – existing row is updated
- Database write failure – logged, does not crash the refresh pipeline
- Source content is empty string – extraction is skipped for that todo

### Refresh integration

- Extraction runs for Granola, Slack, and Gmail todos with `source_context`
- Extraction is skipped for todos without `source_context`
- Extraction is skipped for Calendar and GitHub todos
- Extraction is skipped when the knowledge plugin is disabled
- Extraction error for one todo does not block extraction for others
