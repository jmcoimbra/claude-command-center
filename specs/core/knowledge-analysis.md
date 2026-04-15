# SPEC: Knowledge Analysis

## Purpose

Runs two analysis passes over the knowledge tables during refresh to detect patterns worth surfacing to the user: silence alerts (things that have gone quiet) and drift detection (positions that may have shifted). Writes results to `knowledge_surfaced_insights` for consumption by Command Center.

## Interface

- **Inputs**:
  - `db` (*sql.DB) – database connection for reading knowledge tables and writing insights
  - `llm` (llm.LLM) – Sonnet model (used by drift detection only)
  - `bus` (plugin.EventBus) – for publishing `knowledge.insights.updated` events
  - Configuration: silence thresholds, drift detection enabled/disabled
- **Outputs**: Rows in `knowledge_surfaced_insights`; `knowledge.insights.updated` events on the bus
- **Dependencies**: Knowledge tables populated by extraction, LLM (Sonnet) for drift detection

## Behavior

### Execution order

Both passes run during the refresh pipeline after the extraction pass completes. They execute sequentially:

1. Silence alert pass
2. Drift detection pass

After both passes complete, if any rows were written or removed from `knowledge_surfaced_insights`, a single `knowledge.insights.updated` event is published on the bus. The event has no payload – consumers re-query the table.

Both passes are skipped when the knowledge plugin is disabled.

### Silence alerts

Scans `knowledge_topics` and `knowledge_open_threads` for items that have gone quiet despite being previously active.

#### Thresholds

- **Topics**: `last_seen` older than 10 days (configurable)
- **Open threads**: `last_activity_at` older than 7 days (configurable)

#### Qualifying conditions

Not every old topic or thread triggers an alert. An item must meet qualifying conditions:

- **Topics**: `mention_count > 3` – the topic was discussed enough times to indicate it matters. A topic mentioned once or twice and then not again is not interesting.
- **Open threads**: First raised by Aaron – threads Aaron started are more likely to need his follow-up. Threads raised by others are less likely to be his responsibility.

#### Insight generation

For each qualifying item that exceeds its silence threshold:

1. Generate a deterministic insight ID from the underlying artifact ID and type (e.g., `sha256("silence:" + artifact_id)[:16]`)
2. Check if an insight with this ID already exists in `knowledge_surfaced_insights`
3. If it exists and is not dismissed: update `surfaced_at` to now (keeps it fresh)
4. If it exists and is dismissed: leave it alone (user explicitly dismissed it)
5. If it does not exist: insert a new row with type `silence_alert`, a descriptive title, and body text

Title format: "No activity on: {topic name}" or "Open thread gone quiet: {description truncated}"

Body includes: how long since last activity, mention count (for topics), what was last discussed.

#### Removal

After generating silence alerts, scan existing `silence_alert` insights whose underlying condition no longer holds:

- The topic was mentioned again (its `last_seen` is now within the threshold)
- The open thread was updated (its `last_activity_at` is now within the threshold)
- The open thread was resolved or abandoned

Remove these stale insights from `knowledge_surfaced_insights` (hard delete, not soft delete).

### Drift detection

Asks Sonnet whether Aaron's recent positions show evidence of shifting away from stated stances. This is the heavier analysis pass and the most likely to surface false positives.

#### Scope

- Selects positions from `knowledge_positions` where `holder = "Aaron"` and `stated_at` is within the last 60 days
- First cut: Aaron's positions only (not positions of others he interacts with)

#### Sonnet prompt

The prompt provides:

1. Each of Aaron's recent positions (position text, topic, when stated, source)
2. Recent decisions and newer positions from the last 60 days that may show evidence of shift
3. Instructions: "For each position, determine whether newer decisions or positions show evidence that the plan has shifted away from this stance. Return only positions where you have clear evidence of shift, not speculation."

The prompt asks Sonnet to return a JSON array:

```json
[
  {
    "position_id": "string",
    "original_position": "string",
    "evidence_of_shift": "string",
    "shifted_to": "string"
  }
]
```

#### Insight generation

For each position where Sonnet identifies drift:

1. Generate a deterministic insight ID from the position ID (e.g., `sha256("drift:" + position_id)[:16]`)
2. Write an insight with type `drift_detection`
3. Title: "Position may have shifted: {position text truncated}"
4. Body: Original position, evidence of shift, what it may have shifted to

#### Accuracy mitigations

- Scoped to 60 days only (not the full corpus) to reduce noise
- Runs once per refresh, not per source item
- Can be disabled via config without affecting the rest of the knowledge system
- Insights are dismissible – false positives can be cleared by the user

#### Frequency

Drift detection runs once per refresh cycle (not per source item). It evaluates all qualifying positions in a single Sonnet call (or batched calls if the position count is large).

#### Removal

Drift insights whose underlying position has been explicitly superseded (a newer position by Aaron on the same topic exists with an `evolves` edge) are removed, since the user has already moved on.

## Test Cases

### Silence alerts – happy path

- Topic with `mention_count > 3` and `last_seen` older than 10 days produces a silence alert
- Open thread first raised by Aaron with `last_activity_at` older than 7 days produces a silence alert
- Alert title and body contain the topic name or thread description
- Alert has type `silence_alert` and a valid deterministic ID

### Silence alerts – qualifying conditions

- Topic with `mention_count <= 3` does not produce a silence alert even if old
- Topic with `mention_count > 3` but `last_seen` within 10 days does not produce an alert
- Open thread not first raised by Aaron does not produce a silence alert even if old
- Open thread with `last_activity_at` within 7 days does not produce an alert
- Resolved open thread does not produce an alert regardless of age

### Silence alerts – idempotence

- Running silence analysis twice with the same data produces the same insight IDs
- Running silence analysis twice does not duplicate rows in `knowledge_surfaced_insights`
- A previously dismissed silence alert is not re-surfaced
- A silence alert whose condition no longer holds (topic was mentioned again) is removed
- A silence alert for a resolved open thread is removed

### Silence alerts – threshold configuration

- Custom topic threshold (e.g., 5 days) fires alerts sooner than the default 10 days
- Custom thread threshold (e.g., 3 days) fires alerts sooner than the default 7 days

### Drift detection – happy path

- Using a mock LLM that returns drift evidence, a drift insight is written to `knowledge_surfaced_insights`
- Insight has type `drift_detection` and contains the original position and evidence text
- Insight ID is deterministic from the position ID

### Drift detection – no drift

- Using a mock LLM that returns an empty array, no drift insights are written
- Existing drift insights from a previous run are not affected when the LLM returns no drift

### Drift detection – scope

- Only positions by Aaron are evaluated (positions by others are excluded)
- Only positions from the last 60 days are evaluated (older positions are excluded)

### Drift detection – configuration

- When drift detection is disabled via config, no drift analysis runs and no insights are written
- Disabling drift detection does not affect silence alerts or extraction

### Drift detection – removal

- A drift insight for a position that has been explicitly superseded (newer `evolves` edge from Aaron on the same topic) is removed

### Event publication

- When new insights are written, `knowledge.insights.updated` is published on the bus
- When stale insights are removed, `knowledge.insights.updated` is published on the bus
- When no changes are made, no event is published
- Event payload is empty (consumers re-query the table)

### Error cases

- Drift detection LLM returns malformed JSON – logged, no insights written, silence alerts still run
- Drift detection LLM returns an empty response – treated as no drift detected
- Database read failure during silence scan – logged, does not crash refresh
- Database write failure for an insight – logged, other insights still processed
