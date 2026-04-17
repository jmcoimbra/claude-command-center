package knowledge

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/llm"
)

// BackfillState tracks per-source backfill progress.
type BackfillState struct {
	Source     string
	LastOffset string // last processed source_ref or timestamp cursor
	Completed  bool
}

// backfillSources lists the narrative source types eligible for backfill.
var backfillSources = []string{"granola", "slack", "gmail"}

// RunBackfill performs the one-time 30-day backfill of knowledge extraction.
// It processes historical source content and runs extraction on each item.
// Progress is tracked per source for resumability. If the backfill was
// previously completed, this function returns immediately with no work done.
func RunBackfill(ctx context.Context, database *sql.DB, model llm.LLM) error {
	// Check if already complete – short-circuit without touching the LLM.
	complete, err := IsBackfillComplete(database)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}

	// Load per-source backfill state.
	states, err := loadBackfillStates(database)
	if err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -30)
	now := db.FormatTime(time.Now())

	for _, source := range backfillSources {
		state := states[source]
		if state != nil && state.Completed {
			continue
		}

		lastOffset := ""
		if state != nil {
			lastOffset = state.LastOffset
		}

		// Fetch todos for this source within the 30-day window that have
		// source_context populated. The cc_todos table lives in the core
		// schema – it may not exist in test databases that only run the
		// knowledge plugin migrations, so treat a missing table as zero items.
		todos, err := loadBackfillTodos(database, source, cutoff, lastOffset)
		if err != nil {
			log.Printf("knowledge backfill: error loading todos for %s: %v", source, err)
			// Mark the source as complete so subsequent runs don't retry
			// a source whose table is absent.
			markSourceComplete(database, source, now)
			continue
		}

		if len(todos) > 0 {
			log.Printf("Knowledge backfill: processing %d items from %s", len(todos), source)
		}

		existingTopics := loadExistingTopicNames(database)
		processed := 0

		for _, t := range todos {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			_, extractErr := Extract(ctx, database, model, t.SourceRef, t.Source, t.SourceContext, existingTopics)
			if extractErr != nil {
				log.Printf("knowledge backfill: extraction error for %s/%s: %v", t.Source, t.SourceRef, extractErr)
				// Continue to next item – individual failures do not stop the backfill.
				continue
			}
			processed++

			// Refresh topic names after each extraction for dedup guidance.
			existingTopics = loadExistingTopicNames(database)

			// Update progress so a restart can resume from here.
			updateBackfillProgress(database, source, t.SourceRef, processed, now)
		}

		// Mark this source as complete.
		markSourceComplete(database, source, now)
	}

	return nil
}

// IsBackfillComplete checks whether the backfill has already completed
// for all tracked sources. Returns true only when every source row in
// knowledge_backfill_state has completed = 1.
func IsBackfillComplete(database *sql.DB) (bool, error) {
	var incomplete int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM knowledge_backfill_state WHERE completed = 0`,
	).Scan(&incomplete)
	if err != nil {
		return false, err
	}
	return incomplete == 0, nil
}

// ResetBackfill clears the backfill state for all sources so backfill
// can be re-triggered on the next run.
func ResetBackfill(database *sql.DB) error {
	_, err := database.Exec(
		`UPDATE knowledge_backfill_state SET completed = 0, last_offset = '', updated_at = ?`,
		db.FormatTime(time.Now()),
	)
	return err
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// loadBackfillStates reads the current backfill state for all sources.
func loadBackfillStates(database *sql.DB) (map[string]*BackfillState, error) {
	rows, err := database.Query(`SELECT source, last_offset, completed FROM knowledge_backfill_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := make(map[string]*BackfillState)
	for rows.Next() {
		var s BackfillState
		var completed int
		if err := rows.Scan(&s.Source, &s.LastOffset, &completed); err != nil {
			return nil, err
		}
		s.Completed = completed != 0
		states[s.Source] = &s
	}
	return states, rows.Err()
}

// backfillTodo holds the minimal fields needed for backfill extraction.
type backfillTodo struct {
	SourceRef     string
	Source        string
	SourceContext string
}

// loadBackfillTodos fetches todos from cc_todos matching the given source,
// within the cutoff window, with source_context populated, ordered by
// created_at ASC for deterministic processing. If lastOffset is non-empty,
// only returns rows with source_ref > lastOffset (for resumability).
func loadBackfillTodos(database *sql.DB, source string, cutoff time.Time, lastOffset string) ([]backfillTodo, error) {
	cutoffStr := db.FormatTime(cutoff)

	var rows *sql.Rows
	var err error

	if lastOffset != "" {
		rows, err = database.Query(
			`SELECT source_ref, source, source_context FROM cc_todos
			 WHERE source = ? AND source_context IS NOT NULL AND source_context != ''
			   AND created_at >= ? AND source_ref > ? AND deleted_at IS NULL
			 ORDER BY source_ref ASC`,
			source, cutoffStr, lastOffset,
		)
	} else {
		rows, err = database.Query(
			`SELECT source_ref, source, source_context FROM cc_todos
			 WHERE source = ? AND source_context IS NOT NULL AND source_context != ''
			   AND created_at >= ? AND deleted_at IS NULL
			 ORDER BY source_ref ASC`,
			source, cutoffStr,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []backfillTodo
	for rows.Next() {
		var t backfillTodo
		if err := rows.Scan(&t.SourceRef, &t.Source, &t.SourceContext); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

// loadExistingTopicNames queries all topic names from the knowledge_topics table.
// Returns an empty slice if the query fails.
func loadExistingTopicNames(database *sql.DB) []string {
	rows, err := database.Query("SELECT name FROM knowledge_topics")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		names = append(names, name)
	}
	return names
}

// updateBackfillProgress records the last successfully processed source_ref
// and the running count for a source.
func updateBackfillProgress(database *sql.DB, source, lastRef string, count int, now string) {
	_, err := database.Exec(
		`UPDATE knowledge_backfill_state SET last_offset = ?, updated_at = ? WHERE source = ?`,
		lastRef, now, source,
	)
	if err != nil {
		log.Printf("knowledge backfill: error updating progress for %s: %v", source, err)
	}
}

// markSourceComplete sets the completed flag for a source.
func markSourceComplete(database *sql.DB, source, now string) {
	_, err := database.Exec(
		`UPDATE knowledge_backfill_state SET completed = 1, updated_at = ? WHERE source = ?`,
		now, source,
	)
	if err != nil {
		log.Printf("knowledge backfill: error marking %s complete: %v", source, err)
	}
}
