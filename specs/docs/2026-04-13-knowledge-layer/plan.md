# Knowledge layer for CCC – implementation plan

**Goal:** Build a knowledge layer that extracts topics, decisions, positions, and open threads from Granola, Slack, and Gmail source material; persists them in dedicated SQLite tables; and surfaces silence alerts and drift detection in the Command Center morning view. Includes a 1-month backfill on first run.

**Design doc:** `specs/docs/2026-04-13-knowledge-layer/brainstorm.md` – Read this first. It contains the architecture (three-layer design), the data model (six tables and their relationships), the extraction approach (Sonnet for everything, no triage), the analysis passes (silence and drift), the contract between the knowledge plugin and Command Center (`knowledge_surfaced_insights` table + event bus signal), and the rationale for SQLite over a graph database. This plan describes execution order; the design doc describes what to build and why.

**Assumptions and boundaries:**

- In scope: knowledge plugin scaffold; six tables (`knowledge_topics`, `knowledge_decisions`, `knowledge_positions`, `knowledge_open_threads`, `knowledge_edges`, `knowledge_surfaced_insights`); extraction during refresh from Granola, Slack, and Gmail; silence alert analysis; drift detection analysis (Sonnet); CC integration for the insights section; 1-month backfill.
- Out of scope: MCP server, skills (`/handoff`, `/recall`, etc.), browsing UI in CCC, position-evolution timeline visualization, additional artifact types, periodic lint pass, Obsidian export. These are future plans.
- Relies on: existing refresh pipeline (`internal/refresh/`); existing source-context caching (`source_context` field on todos); existing LLM access via the Claude CLI; existing plugin registry, event bus, and migration framework; existing Granola/Slack/Gmail source adapters in `internal/refresh/sources/`.

## Stages

### Stage 1: Update specs

Create the spec files that capture the behavior described in the brainstorm doc. These specs become the source of truth for what the knowledge layer does.

Files to create or update:

- `specs/builtin/knowledge.md` (new) – plugin overview, responsibilities, lifecycle, and the contract with Command Center via `knowledge_surfaced_insights`. Document the four artifact types and their semantics, the five storage tables, the extraction trigger, the two analysis passes, and the backfill behavior.
- `specs/core/knowledge-extraction.md` (new) – extraction prompt structure, idempotence rules (matched by `source_ref`), edge inference at extraction time (`evolves` edges between positions), and how source coverage maps to artifact types.
- `specs/core/knowledge-analysis.md` (new) – silence alert thresholds and qualifying conditions; drift detection scope, prompt, and accuracy mitigations.
- `specs/builtin/commandcenter.md` (update) – add the insights section and the dismiss key. Document the subscription to `knowledge.insights.updated` and the read pattern against `knowledge_surfaced_insights`.
- `specs/plugin/event-bus.md` (update) – add `knowledge.insights.updated` to the event catalog with payload shape (none – consumers re-query the table).
- `specs/core/refresh.md` (update) – document the two new post-processing steps (extraction and analysis) added after source-context fetch, and that they are skipped when the knowledge plugin is disabled.

Use `/spec-writer` to produce each spec text so format conventions are consistent across the codebase.

### Stage 2: Write failing tests

**Depends on:** Stage 1

Write tests from the updated specs that fail because the implementation doesn't exist yet. Tests must fail first to prove the behavioral gap (Prove-It Pattern).

Test categories:

- **Migration tests** (`internal/builtin/knowledge/migrations_test.go`) – verify all six tables are created with correct columns, indexes, and constraints when the plugin's migrations run.
- **Extraction tests** (`internal/builtin/knowledge/extraction_test.go`) – using fixture transcripts (small Granola, Slack, Gmail samples), verify the extraction function returns the expected artifacts; verify idempotence on `source_ref`; verify that running extraction twice on the same input does not duplicate rows.
- **Edge inference tests** – verify that adding a new position by the same holder on the same topic creates an `evolves` edge to the prior position.
- **Silence analysis tests** (`internal/builtin/knowledge/silence_test.go`) – seed knowledge tables with controlled timestamps; verify silence alerts fire for qualifying topics/threads and not for disqualified ones (mention_count ≤ 3, recently-mentioned, not first-raised by Aaron).
- **Drift analysis tests** (`internal/builtin/knowledge/drift_test.go`) – using a mock LLM, verify drift detection writes insights when the LLM returns "yes" and does not when it returns "no".
- **Insight contract tests** – verify that writing to `knowledge_surfaced_insights` publishes `knowledge.insights.updated`; verify dismiss updates `dismissed_at`.
- **CC integration view tests** (`internal/builtin/commandcenter/cc_view_insights_test.go`) – using `internal/testutil` view helpers, seed `knowledge_surfaced_insights` with active rows, render the Command Center view, assert the insights section appears with the expected titles. Test the dismiss key via `HandleKey()` and assert the dismissed insight no longer appears in the next render.
- **Backfill tests** (`internal/builtin/knowledge/backfill_test.go`) – verify backfill respects the 30-day window; verify resumability (per-source progress tracking); verify backfill is one-shot (does not re-run on subsequent refreshes).
- **Refresh integration tests** – verify the refresh pipeline calls extraction and analysis when the knowledge plugin is enabled; verify they are skipped when it is disabled.

All tests must fail at the end of this stage. The plan succeeds when subsequent stages make them pass.

### Stage 3: Plugin scaffold and migrations

**Depends on:** Stage 2

Create the knowledge plugin skeleton and all six table migrations. This is the foundation that every subsequent stage builds on.

Deliverables:

- New directory `internal/builtin/knowledge/` with the standard plugin file layout (`knowledge.go` for the main plugin struct, `migrations.go` for the migration definitions).
- Implement `plugin.Plugin` interface (`Slug`, `TabName`, `Init`, `Shutdown`, `Migrations`, `View`, `KeyBindings`, `HandleKey`, `HandleMessage`, `Routes`, `NavigateTo`, `RefreshInterval`, `Refresh`). For this first cut, `View` returns empty (no Knowledge tab is exposed yet) and key handling is no-op. The plugin exists primarily to own the tables and the extraction/analysis passes.
- Migration that creates all six tables with the columns specified in the brainstorm doc, plus reasonable indexes (`knowledge_topics.name`, `knowledge_positions.holder + topic_id`, `knowledge_open_threads.last_activity_at`, `knowledge_surfaced_insights.dismissed_at`, etc.).
- Register the plugin in the plugin registry alongside the other built-in plugins.
- Migration tests from Stage 2 should pass after this stage.

Done criteria: `make test` runs the migration tests successfully; `make build` succeeds; running `ccc` does not crash and the new tables exist in the SQLite database.

### Stage 4: Knowledge extraction core (Granola)

**Depends on:** Stage 3

Build the extraction function and integrate it into the refresh pipeline, using Granola as the first source. This stage proves the end-to-end pipeline works for one source before broadening coverage.

Deliverables:

- New file `internal/builtin/knowledge/extraction.go` with the `Extract(ctx, db, llm, sourceRef, sourceType, content)` function. The function builds the extraction prompt (per the spec), calls Sonnet via the existing LLM interface, parses the JSON response, and writes artifacts to the tables.
- Idempotence: artifacts are matched by `source_ref` + artifact-type-specific dedup rules. Re-running extraction on the same source content does not duplicate rows.
- Edge inference: when inserting a position, look up prior positions by the same holder on the same topic and create an `evolves` edge.
- Topic deduplication: the extraction prompt is given the existing topic names so it can reuse them; the `name` column has a unique constraint.
- Refresh pipeline integration: add a new post-processing step in `internal/refresh/refresh.go` that, after source-context fetch, iterates over Granola todos with `source_context` populated and calls `Extract`. Wrap behind a config check for whether the knowledge plugin is enabled.
- Extraction tests, edge inference tests, refresh integration tests (Granola path) from Stage 2 should pass.

Done criteria: a real refresh against Aaron's data populates the knowledge tables with topics, decisions, positions, and threads from recent Granola meetings. Manual spot-check of a Zach 1:1 transcript confirms the extracted artifacts are sensible.

### Stage 5: Add Slack source to extraction

**Depends on:** Stage 4

Extend the extraction integration to also process Slack thread content. The extraction function itself needs no change (it accepts `sourceType`); the refresh pipeline integration is what changes.

Deliverables:

- Update the refresh post-processing step to also iterate over Slack todos with `source_context` populated and call `Extract` with `sourceType = "slack"`.
- Adjust the extraction prompt if Slack-specific framing is needed (likely minor – the prompt should treat any text content uniformly).
- Refresh integration tests (Slack path) from Stage 2 should pass.

Done criteria: a real refresh populates knowledge tables with Slack-derived artifacts in addition to Granola ones.

### Stage 6: Add Gmail source to extraction

**Depends on:** Stage 4 (parallel with Stage 5)

Same shape as Stage 5, for Gmail. This can be developed in parallel with Stage 5 since it touches a different code path.

Deliverables:

- Update the refresh post-processing step to also iterate over Gmail todos with `source_context` populated and call `Extract` with `sourceType = "gmail"`.
- Adjust the extraction prompt if Gmail-specific framing is needed.
- Refresh integration tests (Gmail path) from Stage 2 should pass.

Done criteria: a real refresh populates knowledge tables with Gmail-derived artifacts in addition to Granola and Slack ones.

### Stage 7: Silence alerts and Command Center integration

**Depends on:** Stage 4 (parallel with Stages 5 and 6)

Build the silence-alert analysis pass, the insight surface, and the Command Center integration. This stage delivers the first user-visible value (insights in the morning view).

Deliverables:

- New file `internal/builtin/knowledge/silence.go` with the silence-alert analysis function. Scans `knowledge_topics` and `knowledge_open_threads` for items where `last_seen` / `last_activity_at` is older than the configured threshold AND meets qualifying conditions (mention_count > 3 for topics; first raised by Aaron for threads). Writes matching insights to `knowledge_surfaced_insights` with type `silence_alert`.
- Idempotence: insight IDs are deterministic from the underlying artifact ID. Re-running silence analysis updates existing insights rather than duplicating; insights whose underlying condition no longer holds are removed.
- Refresh integration: add a second post-processing step after extraction that runs silence analysis. Publish `knowledge.insights.updated` event after writes.
- Update the event catalog in `internal/plugin/eventbus.go` (or wherever events are documented) to include `knowledge.insights.updated`.
- Command Center integration in `internal/builtin/commandcenter/`:
  - Subscribe to `knowledge.insights.updated` in `Init`. On event, trigger a re-query and re-render.
  - Add a query function that reads active (non-dismissed) rows from `knowledge_surfaced_insights` ordered by priority.
  - Add an "Insights" section to the Command Center view that renders the active insights with their titles and bodies.
  - Add a dismiss key (suggested: `x` while an insight is selected) that updates `dismissed_at`.
- Silence analysis tests, insight contract tests, and CC integration view tests from Stage 2 should pass.

Done criteria: a real refresh populates the insights table with silence alerts; opening Command Center shows the insights section; pressing the dismiss key removes an insight from the view. View tests assert the rendered output.

### Stage 8: Drift detection

**Depends on:** Stage 7

Add the drift-detection analysis pass. Reuses the insight surface built in Stage 7, so this stage focuses on the analysis logic.

Deliverables:

- New file `internal/builtin/knowledge/drift.go` with the drift-detection analysis function. Selects positions held by Aaron in the last 60 days. For each, builds a Sonnet prompt asking whether newer decisions or positions show evidence the plan has shifted away from this stance. Writes matching insights to `knowledge_surfaced_insights` with type `drift_detection`.
- Refresh integration: add drift analysis to the post-processing sequence after silence analysis. Both write to the same surface, so CC's insights section shows both types.
- Drift analysis tests from Stage 2 should pass.

Done criteria: a real refresh produces drift-detection insights when applicable; they appear in CC's insights section alongside silence alerts and are dismissable the same way.

### Stage 9: 1-month backfill

**Depends on:** Stages 5 and 6 (needs all three source extractors to be in place)

Implement the one-time 30-day backfill that seeds the corpus so silence alerts and drift detection have history to work with on day one.

Deliverables:

- New file `internal/builtin/knowledge/backfill.go` with the backfill orchestration. On first plugin start (detected by checking a `knowledge_backfill_state` row, also created in Stage 3 migrations), iterate per source and fetch the last 30 days of source content. For each item, ensure `source_context` is cached (using existing context fetchers), then call `Extract`.
- Resumability: track per-source progress in `knowledge_backfill_state` so a partial failure can pick up from the last successful source/timestamp on the next run.
- Triggering: backfill runs once when the plugin is first enabled. Provide a CLI flag (`ccc --backfill-knowledge` or similar; align with existing CLI conventions in `cmd/ccc/`) to manually re-trigger if needed.
- Don't re-run automatically: once `knowledge_backfill_state` shows completion, subsequent refreshes operate on go-forward data only.
- Cost guard: log estimated API call count before starting backfill so the user can see the expected magnitude.
- Backfill tests from Stage 2 should pass.

Done criteria: a fresh enable of the knowledge plugin triggers the backfill, populates the knowledge tables with 30 days of artifacts, and on completion records the backfill state. A second refresh after completion does not re-run the backfill. Manual trigger via CLI flag works.
