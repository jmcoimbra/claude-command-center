package refresh

import (
	"testing"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
)

func TestMerge_CalendarReplacedEntirely(t *testing.T) {
	existing := &db.CommandCenter{
		Calendar: db.CalendarData{
			Today: []db.CalendarEvent{{Title: "Old Meeting"}},
		},
	}
	fresh := &FreshData{
		Calendar: db.CalendarData{
			Today: []db.CalendarEvent{{Title: "New Meeting"}},
		},
	}

	result := Merge(existing, fresh)
	if len(result.Calendar.Today) != 1 || result.Calendar.Today[0].Title != "New Meeting" {
		t.Errorf("expected calendar to be replaced, got %v", result.Calendar.Today)
	}
}

func TestMerge_DismissedTodoNeverRecreated(t *testing.T) {
	existing := &db.CommandCenter{
		Todos: []db.Todo{
			{ID: "abc", Title: "Old", Status: "dismissed", SourceRef: "granola-123"},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "Old Recreated", Source: "granola", SourceRef: "granola-123"},
		},
	}

	result := Merge(existing, fresh)
	active := 0
	for _, t := range result.Todos {
		if t.SourceRef == "granola-123" && t.Status != "dismissed" {
			active++
		}
	}
	if active != 0 {
		t.Errorf("dismissed todo was recreated as active")
	}
}

func TestMerge_ExistingTodoUpdated(t *testing.T) {
	existing := &db.CommandCenter{
		Todos: []db.Todo{
			{ID: "abc", Title: "Old Title", Status: db.StatusBacklog, SourceRef: "ref-1",
				Detail: "old detail", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "New Title", SourceRef: "ref-1", Detail: "new detail", WhoWaiting: "Bob"},
		},
	}

	result := Merge(existing, fresh)
	found := false
	for _, todo := range result.Todos {
		if todo.SourceRef == "ref-1" {
			found = true
			if todo.ID != "abc" {
				t.Errorf("expected ID preserved as 'abc', got %q", todo.ID)
			}
			if todo.Title != "New Title" {
				t.Errorf("expected title updated to 'New Title', got %q", todo.Title)
			}
			if todo.Detail != "new detail" {
				t.Errorf("expected detail updated, got %q", todo.Detail)
			}
			if todo.Status != db.StatusBacklog {
				t.Errorf("expected status preserved as 'backlog', got %q", todo.Status)
			}
			if todo.CreatedAt.Year() != 2026 {
				t.Errorf("expected created_at preserved, got %v", todo.CreatedAt)
			}
		}
	}
	if !found {
		t.Error("todo with ref-1 not found in merged result")
	}
}

func TestMerge_NewTodoGetsID(t *testing.T) {
	existing := &db.CommandCenter{}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "Brand New", Source: "slack", SourceRef: "slack-456"},
		},
	}

	result := Merge(existing, fresh)
	if len(result.Todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(result.Todos))
	}
	if result.Todos[0].ID == "" {
		t.Error("expected new todo to get an ID")
	}
	if result.Todos[0].Status != db.StatusNew {
		t.Errorf("expected status 'new', got %q", result.Todos[0].Status)
	}
}

func TestMerge_ManualTodosPreserved(t *testing.T) {
	existing := &db.CommandCenter{
		Todos: []db.Todo{
			{ID: "manual-1", Title: "My Task", Status: db.StatusBacklog, Source: "manual"},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "From Granola", Source: "granola", SourceRef: "g-1"},
		},
	}

	result := Merge(existing, fresh)
	found := false
	for _, todo := range result.Todos {
		if todo.ID == "manual-1" {
			found = true
		}
	}
	if !found {
		t.Error("manual todo was not preserved")
	}
}

func TestMerge_PendingActionsPreserved(t *testing.T) {
	existing := &db.CommandCenter{
		PendingActions: []db.PendingAction{
			{Type: "booking", TodoID: "abc", DurationMinutes: 30},
		},
	}
	fresh := &FreshData{}

	result := Merge(existing, fresh)
	if len(result.PendingActions) != 1 {
		t.Errorf("expected pending actions preserved, got %d", len(result.PendingActions))
	}
}

func TestMerge_CompletedTodoNotOverwritten(t *testing.T) {
	existing := &db.CommandCenter{
		Todos: []db.Todo{
			{ID: "abc", Title: "Create Google Slides", Status: "completed", SourceRef: "meeting-123-abc"},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "Create Google Slides for data team", Source: "granola", SourceRef: "meeting-123-abc"},
		},
	}

	result := Merge(existing, fresh)
	for _, todo := range result.Todos {
		if todo.SourceRef == "meeting-123-abc" {
			if todo.Status != "completed" {
				t.Errorf("completed todo was overwritten, status = %q", todo.Status)
			}
			if todo.Title != "Create Google Slides" {
				t.Errorf("completed todo title was overwritten to %q", todo.Title)
			}
			return
		}
	}
	t.Error("completed todo was dropped entirely")
}

func TestMergeStatus(t *testing.T) {
	t.Run("new external todo gets status new", func(t *testing.T) {
		existing := &db.CommandCenter{}
		fresh := &FreshData{
			Todos: []db.Todo{
				{Title: "Review PR", Source: "github", SourceRef: "gh-999"},
			},
		}

		result := Merge(existing, fresh)
		if len(result.Todos) != 1 {
			t.Fatalf("expected 1 todo, got %d", len(result.Todos))
		}
		if result.Todos[0].Status != db.StatusNew {
			t.Errorf("expected status 'new', got %q", result.Todos[0].Status)
		}
	})

	t.Run("fresh todo with no source_ref keeps its status", func(t *testing.T) {
		existing := &db.CommandCenter{}
		fresh := &FreshData{
			Todos: []db.Todo{
				{Title: "Loose item", Source: "manual", Status: db.StatusBacklog},
			},
		}

		result := Merge(existing, fresh)
		if len(result.Todos) != 1 {
			t.Fatalf("expected 1 todo, got %d", len(result.Todos))
		}
		if result.Todos[0].Status != db.StatusBacklog {
			t.Errorf("expected status 'backlog', got %q", result.Todos[0].Status)
		}
	})

	t.Run("existing todo status preserved on merge", func(t *testing.T) {
		existing := &db.CommandCenter{
			Todos: []db.Todo{
				{ID: "t1", Title: "Old", Status: db.StatusBacklog, SourceRef: "ref-1"},
			},
		}
		fresh := &FreshData{
			Todos: []db.Todo{
				{Title: "Updated", SourceRef: "ref-1", Status: db.StatusNew},
			},
		}

		result := Merge(existing, fresh)
		for _, todo := range result.Todos {
			if todo.SourceRef == "ref-1" {
				if todo.Status != db.StatusBacklog {
					t.Errorf("expected status preserved as 'backlog', got %q", todo.Status)
				}
				return
			}
		}
		t.Error("todo with ref-1 not found")
	})

	t.Run("completed todo preserved as-is", func(t *testing.T) {
		existing := &db.CommandCenter{
			Todos: []db.Todo{
				{ID: "t2", Title: "Done", Status: "completed", SourceRef: "ref-2"},
			},
		}
		fresh := &FreshData{
			Todos: []db.Todo{
				{Title: "Done Updated", SourceRef: "ref-2", Status: db.StatusNew},
			},
		}

		result := Merge(existing, fresh)
		for _, todo := range result.Todos {
			if todo.SourceRef == "ref-2" {
				if todo.Status != "completed" {
					t.Errorf("expected status 'completed', got %q", todo.Status)
				}
				return
			}
		}
		t.Error("completed todo not found")
	})

	t.Run("dismissed todo remains tombstoned", func(t *testing.T) {
		existing := &db.CommandCenter{
			Todos: []db.Todo{
				{ID: "t3", Title: "Gone", Status: "dismissed", SourceRef: "ref-3"},
			},
		}
		fresh := &FreshData{
			Todos: []db.Todo{
				{Title: "Gone Recreated", Source: "granola", SourceRef: "ref-3"},
			},
		}

		result := Merge(existing, fresh)
		for _, todo := range result.Todos {
			if todo.SourceRef == "ref-3" && todo.Status != "dismissed" {
				t.Error("dismissed todo was recreated as non-dismissed")
			}
		}
	})
}

func TestMerge_SuggestionsPreserved(t *testing.T) {
	existing := &db.CommandCenter{
		Suggestions: db.Suggestions{
			Focus:         "Ship the calendar fix",
			RankedTodoIDs: []string{"todo-1", "todo-2"},
			Reasons:       map[string]string{"todo-1": "urgent deadline"},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "New task", Source: "granola", SourceRef: "g-1"},
		},
	}

	result := Merge(existing, fresh)
	if result.Suggestions.Focus != "Ship the calendar fix" {
		t.Errorf("expected suggestions.Focus preserved, got %q", result.Suggestions.Focus)
	}
	if len(result.Suggestions.RankedTodoIDs) != 2 {
		t.Errorf("expected 2 ranked todo IDs preserved, got %d", len(result.Suggestions.RankedTodoIDs))
	}
	if result.Suggestions.Reasons["todo-1"] != "urgent deadline" {
		t.Errorf("expected reasons preserved, got %v", result.Suggestions.Reasons)
	}
}

func TestMerge_NilExisting(t *testing.T) {
	fresh := &FreshData{
		Calendar: db.CalendarData{
			Today: []db.CalendarEvent{{Title: "Meeting"}},
		},
		Todos: []db.Todo{
			{Title: "Task", Source: "granola", SourceRef: "g-1"},
		},
	}

	result := Merge(nil, fresh)
	if len(result.Calendar.Today) != 1 {
		t.Error("expected calendar data")
	}
	if len(result.Todos) != 1 {
		t.Error("expected 1 todo")
	}
}

func TestMerge_FocusStarPreserved(t *testing.T) {
	existing := &db.CommandCenter{
		Todos: []db.Todo{
			{ID: "t1", Title: "Starred task", Status: db.StatusBacklog, SourceRef: "gh-1", Focus: true, Starred: true},
			{ID: "t2", Title: "Focused task", Status: db.StatusBacklog, SourceRef: "sl-1", Focus: true, Starred: false},
			{ID: "t3", Title: "Manual starred", Status: db.StatusBacklog, Source: "manual", Focus: true, Starred: true},
		},
	}
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "Starred task (updated)", Source: "github", SourceRef: "gh-1"},
			{Title: "Focused task (updated)", Source: "slack", SourceRef: "sl-1"},
		},
	}

	result := Merge(existing, fresh)

	for _, todo := range result.Todos {
		switch todo.ID {
		case "t1":
			if !todo.Starred {
				t.Error("t1: Starred should be preserved across merge")
			}
			if !todo.Focus {
				t.Error("t1: Focus should be preserved across merge")
			}
		case "t2":
			if !todo.Focus {
				t.Error("t2: Focus should be preserved across merge")
			}
		case "t3":
			if !todo.Starred {
				t.Error("t3: Starred should be preserved for manual todo (unmatched)")
			}
			if !todo.Focus {
				t.Error("t3: Focus should be preserved for manual todo (unmatched)")
			}
		}
	}
}

func TestMerge_FocusStarPreservedFullRefreshCycle(t *testing.T) {
	// This test simulates the FULL refresh.Run cycle:
	// 1. Populate DB with todos
	// 2. Star some
	// 3. Load existing from DB
	// 4. Merge with fresh data
	// 5. Save via DBSaveRefreshResult
	// 6. Reload from DB
	// 7. Verify stars survive

	dir := t.TempDir()
	database, err := db.OpenDB(dir + "/test.db")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer database.Close()

	now := time.Now()

	// Step 1: Initial populate
	initial := &db.CommandCenter{
		GeneratedAt: now,
		Todos: []db.Todo{
			{ID: "t1", Title: "GitHub task", Status: db.StatusBacklog, Source: "github", SourceRef: "gh-1", CreatedAt: now},
			{ID: "t2", Title: "Slack task", Status: db.StatusBacklog, Source: "slack", SourceRef: "sl-1", CreatedAt: now},
			{ID: "t3", Title: "Manual task", Status: db.StatusBacklog, Source: "manual", CreatedAt: now},
		},
	}
	if err := db.DBSaveRefreshResult(database, initial); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Step 2: Star some todos
	if err := db.DBSetTodoStar(database, "t1", true); err != nil {
		t.Fatalf("star t1: %v", err)
	}
	if err := db.DBSetTodoStar(database, "t3", true); err != nil {
		t.Fatalf("star t3: %v", err)
	}

	// Step 3: Load existing (simulating what refresh.Run does)
	existing, err := db.LoadCommandCenterFromDB(database)
	if err != nil {
		t.Fatalf("load existing: %v", err)
	}

	// Verify existing has stars
	for _, todo := range existing.Todos {
		switch todo.ID {
		case "t1":
			if !todo.Starred || !todo.Focus {
				t.Errorf("existing t1 should be starred+focused, got starred=%v focus=%v", todo.Starred, todo.Focus)
			}
		case "t3":
			if !todo.Starred || !todo.Focus {
				t.Errorf("existing t3 should be starred+focused, got starred=%v focus=%v", todo.Starred, todo.Focus)
			}
		}
	}

	// Step 4: Simulate fresh data from sources
	fresh := &FreshData{
		Todos: []db.Todo{
			{Title: "GitHub task (updated)", Source: "github", SourceRef: "gh-1"},
			{Title: "Slack task (updated)", Source: "slack", SourceRef: "sl-1"},
			{Title: "Brand new task", Source: "granola", SourceRef: "gra-1"},
		},
	}

	// Step 5: Merge (simulating what refresh.Run does)
	merged := Merge(existing, fresh)

	// Verify merged result has stars
	for _, todo := range merged.Todos {
		switch todo.ID {
		case "t1":
			if !todo.Starred || !todo.Focus {
				t.Errorf("merged t1 should be starred+focused, got starred=%v focus=%v", todo.Starred, todo.Focus)
			}
		case "t3":
			if !todo.Starred || !todo.Focus {
				t.Errorf("merged t3 should be starred+focused, got starred=%v focus=%v", todo.Starred, todo.Focus)
			}
		}
	}

	// Step 6: Save via DBSaveRefreshResult (simulating what refresh.Run does)
	if err := db.DBSaveRefreshResult(database, merged); err != nil {
		t.Fatalf("save merged: %v", err)
	}

	// Step 7: Reload from DB (simulating what CCC TUI does after refresh)
	reloaded, err := db.LoadCommandCenterFromDB(database)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	for _, todo := range reloaded.Todos {
		switch todo.ID {
		case "t1":
			if !todo.Starred {
				t.Error("reloaded t1 should be starred")
			}
			if !todo.Focus {
				t.Error("reloaded t1 should be focused")
			}
		case "t3":
			if !todo.Starred {
				t.Error("reloaded t3 should be starred")
			}
			if !todo.Focus {
				t.Error("reloaded t3 should be focused")
			}
		}
	}
}
