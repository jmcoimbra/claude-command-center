package commandcenter

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func (p *Plugin) handleDetailView(msg tea.KeyMsg) plugin.Action {
	switch p.detailMode {
	case "editingField":
		return p.handleDetailEditingField(msg)
	case "selectingStatus":
		return p.handleDetailStatusSelect(msg)
	case "selectingPath":
		return p.handleDetailPathSelect(msg)
	case "commandInput":
		return p.handleDetailCommandInput(msg)
	case "trainingInput":
		return p.handleDetailTrainingInput(msg)
	default:
		return p.handleDetailViewing(msg)
	}
}

// statusOptions are the available status values for inline selection.
var statusOptions = []string{"backlog", "blocked", "completed", "dismissed"}

// detailFieldCount is the number of cyclable fields in the detail view.
const detailFieldCount = 3 // 0=Status, 1=Due, 2=ProjectDir

func (p *Plugin) handleDetailViewing(msg tea.KeyMsg) plugin.Action {
	// While showing a notice, ignore all keys except esc
	if p.detailNotice != "" {
		if msg.String() == "esc" {
			p.detailNotice = ""
			p.detailView = false
			p.detailMode = "viewing"
			return plugin.NoopAction()
		}
		return plugin.NoopAction()
	}

	// Block edit/mutation operations when the daemon reports an active agent.
	agentActive := false
	if todo := p.detailTodo(); todo != nil {
		agentActive = todo.Status == db.StatusRunning || todo.Status == db.StatusBlocked || todo.Status == db.StatusEnqueued
	}

	switch msg.String() {
	case "up":
		p.detailVP.LineUp(1)
		return plugin.ConsumedAction()
	case "down":
		p.detailVP.LineDown(1)
		return plugin.ConsumedAction()
	case "pgup":
		p.detailVP.HalfViewUp()
		return plugin.ConsumedAction()
	case "pgdown":
		p.detailVP.HalfViewDown()
		return plugin.ConsumedAction()
	case "tab":
		p.detailSelectedField = (p.detailSelectedField + 1) % detailFieldCount
		return plugin.ConsumedAction()
	case "shift+tab":
		p.detailSelectedField = (p.detailSelectedField - 1 + detailFieldCount) % detailFieldCount
		return plugin.ConsumedAction()
	case "enter":
		if agentActive {
			p.flashMessage = "Todo is being updated by agent"
			p.flashMessageAt = time.Now()
			return plugin.ConsumedAction()
		}
		return p.enterDetailFieldEdit()
	case "x":
		return p.detailCompleteTodo()
	case "X":
		return p.detailDismissTodo()
	case "delete", "backspace":
		// Kill running agent session for this todo
		if todo := p.detailTodo(); todo != nil {
			if killCmd := p.killAgent(todo.ID); killCmd != nil {
				p.flashMessage = "Agent killed"
				p.flashMessageAt = time.Now()
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: killCmd}
			}
			p.flashMessage = "No running agent for this todo"
			p.flashMessageAt = time.Now()
		}
		return plugin.ConsumedAction()
	case "f":
		// Focus toggle — delegate to the same logic as list view
		if todo := p.detailTodo(); todo != nil {
			return p.handleDetailFocusToggle(todo)
		}
		return plugin.ConsumedAction()
	case "s":
		// Star toggle — delegate to the same logic as list view
		if todo := p.detailTodo(); todo != nil {
			return p.handleDetailStarToggle(todo)
		}
		return plugin.ConsumedAction()
	case "S":
		// Schedule — open schedule modal
		if todo := p.detailTodo(); todo != nil {
			p.openScheduleModal(todo.ID)
		}
		return plugin.ConsumedAction()
	case "j":
		// Next todo
		activeTodos := p.cc.ActiveTodos()
		idx := p.detailTodoActiveIndex()
		if idx >= 0 && idx < len(activeTodos)-1 {
			p.detailTodoID = activeTodos[idx+1].ID
			p.detailSelectedField = 0
			p.detailVP.GotoTop()
		}
		return plugin.ConsumedAction()
	case "k":
		// Previous todo
		activeTodos := p.cc.ActiveTodos()
		idx := p.detailTodoActiveIndex()
		if idx > 0 {
			p.detailTodoID = activeTodos[idx-1].ID
			p.detailSelectedField = 0
			p.detailVP.GotoTop()
		}
		return plugin.ConsumedAction()
	case "]":
		// Navigate to next source in synthesis todo
		if todo := p.detailTodo(); todo != nil && todo.Source == "merge" && p.cc != nil {
			origIDs := db.DBGetOriginalIDs(p.cc.Merges, todo.ID)
			if len(origIDs) > 0 && p.mergeSourceCursor < len(origIDs)-1 {
				p.mergeSourceCursor++
			}
		}
		return plugin.ConsumedAction()
	case "[":
		// Navigate to previous source in synthesis todo
		if todo := p.detailTodo(); todo != nil && todo.Source == "merge" && p.cc != nil {
			origIDs := db.DBGetOriginalIDs(p.cc.Merges, todo.ID)
			if len(origIDs) > 0 && p.mergeSourceCursor > 0 {
				p.mergeSourceCursor--
			}
		}
		return plugin.ConsumedAction()
	case "w":
		// Open session viewer for todos with active agent sessions or saved logs.
		// Priority: daemon session > saved log on disk.
		if todo := p.detailTodo(); todo != nil {
			// Check daemon for an active agent.
			if dc := p.daemonClient(); dc != nil {
				if status, err := dc.AgentStatus(todo.ID); err == nil && (status.Status == "processing" || status.Status == "blocked") {
					p.initSessionViewer(todo.ID)
					// Don't clear sessionViewerReplayEvents here — the daemon's
					// StreamAgentOutput returns the full event history from offset 0,
					// so replay events will be populated by the polling loop. Clearing
					// them caused a brief empty-viewer flash during the 500ms poll delay.
					if !p.sessionViewerListening {
						p.sessionViewerListening = true
						return plugin.Action{Type: plugin.ActionNoop, TeaCmd: listenForDaemonAgentEvents(todo.ID, dc, 0)}
					}
					return plugin.ConsumedAction()
				}
			}

			// No active session anywhere — try saved log on disk.
			if todo.SessionLogPath != "" {
				if err := p.initSessionViewerFromLog(todo.ID, todo.SessionLogPath); err != nil {
					p.flashMessage = fmt.Sprintf("Cannot open session log: %v", err)
					p.flashMessageAt = time.Now()
					return plugin.ConsumedAction()
				}
				return plugin.ConsumedAction()
			}
			// No active session and no log
			p.flashMessage = "No active session for this todo"
			p.flashMessageAt = time.Now()
		}
		return plugin.ConsumedAction()
	case "o":
		// If todo has a session_id, join/resume that session directly
		if todo := p.detailTodo(); todo != nil {
			sid := todo.SessionID
			// Try to recover session ID from log file if missing.
			if sid == "" {
				sid = p.extractSessionIDFromLog(todo)
			}
			if sid != "" {
				// Verify session file still exists before attempting to join.
				if !sessionFileExists(sid) {
					p.flashMessage = "Session expired — use r to re-run or c to edit prompt first"
					p.flashMessageAt = time.Now()
					return plugin.ConsumedAction()
				}
				// Backfill the in-memory and DB session ID.
				if todo.SessionID == "" {
					todo.SessionID = sid
					p.persistSessionID(todo.ID, sid)
				}
				dir := todo.ProjectDir
				if dir == "" {
					home, _ := os.UserHomeDir()
					dir = home
				}
				return plugin.Action{
					Type: "launch",
					Args: map[string]string{
						"dir":       dir,
						"resume_id": sid,
						"todo_id":   todo.ID,
					},
				}
			}
			// Always go through task runner for new launches
			p.enterTaskRunner(*todo)
		}
		return plugin.NoopAction()
	case "r":
		// Resume a completed/failed agent session as a new headless agent
		if todo := p.detailTodo(); todo != nil && todo.SessionID != "" && !agentActive {
			dir := todo.ProjectDir
			if dir == "" {
				home, _ := os.UserHomeDir()
				dir = home
			}
			// Only pass ResumeID if the session file still exists.
			resumeID := todo.SessionID
			if !sessionFileExists(resumeID) {
				resumeID = ""
			}
			qs := queuedSession{
				TodoID:     todo.ID,
				Prompt:     todo.ProposedPrompt,
				ProjectDir: dir,
				Mode:       todo.LaunchMode,
				Perm:       p.cfg.Agent.DefaultPermission,
				Budget:     p.cfg.Agent.DefaultBudget,
				AutoStart:  true,
				ResumeID:   resumeID,
			}
			cmd := p.launchOrQueueAgent(qs)
			p.detailView = false
			p.detailMode = "viewing"
			verb := "resumed"
			if resumeID == "" {
				verb = "re-launched"
			}
			p.flashMessage = fmt.Sprintf("Agent %s for: %s", verb, truncateToWidth(flattenTitle(todo.Title), 40))
			p.flashMessageAt = time.Now()
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
		}
		return plugin.ConsumedAction()
	case "c":
		if agentActive {
			p.flashMessage = "Todo is being updated by agent"
			p.flashMessageAt = time.Now()
			return plugin.ConsumedAction()
		}
		p.detailMode = "commandInput"
		p.commandTextArea.Reset()
		inputWidth := p.textareaWidth()
		p.commandTextArea.SetWidth(inputWidth)
		cmd := p.commandTextArea.Focus()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
	case "U":
		// Unmerge selected source from synthesis todo
		if todo := p.detailTodo(); todo != nil && todo.Source == "merge" && p.cc != nil {
			origIDs := db.DBGetOriginalIDs(p.cc.Merges, todo.ID)
			if len(origIDs) > 0 && p.mergeSourceCursor < len(origIDs) {
				selectedOrigID := origIDs[p.mergeSourceCursor]
				synthID := todo.ID

				// Build a display name for the flash message
				unmergedName := selectedOrigID
				if orig := p.cc.FindTodo(selectedOrigID); orig != nil {
					unmergedName = flattenTitle(orig.Title)
				}

				// Update in-memory merges immediately so the view reflects the change
				for i := range p.cc.Merges {
					if p.cc.Merges[i].SynthesisID == synthID && p.cc.Merges[i].OriginalID == selectedOrigID {
						p.cc.Merges[i].Vetoed = true
						break
					}
				}

				// Adjust cursor if it's now out of bounds
				remainingIDs := db.DBGetOriginalIDs(p.cc.Merges, synthID)
				if len(remainingIDs) <= 1 {
					// Will delete synthesis — exit detail view
					p.detailView = false
					p.detailMode = "viewing"
					p.flashMessage = fmt.Sprintf("Unmerged: %s (synthesis dissolved)", truncateToWidth(unmergedName, 40))
				} else {
					if p.mergeSourceCursor >= len(remainingIDs) {
						p.mergeSourceCursor = len(remainingIDs) - 1
					}
					p.flashMessage = fmt.Sprintf("Unmerged: %s", truncateToWidth(unmergedName, 40))
				}
				p.flashMessageAt = time.Now()

				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: p.dbWriteCmd(func(database *sql.DB) error {
					if err := db.DBSetMergeVetoed(database, synthID, selectedOrigID, true); err != nil {
						return err
					}
					// Count remaining non-vetoed originals
					merges, err := db.DBLoadMerges(database)
					if err != nil {
						return err
					}
					remaining := db.DBGetOriginalIDs(merges, synthID)
					if len(remaining) <= 1 {
						// Only one (or zero) left — delete synthesis and restore the last original
						_ = db.DBDeleteSynthesisMerges(database, synthID)
						_ = db.DBDeleteTodo(database, synthID)
					}
					return nil
				})}
			}
		}
		return plugin.ConsumedAction()
	case "T":
		// Train routing and prompt generation rules
		if todo := p.detailTodo(); todo != nil {
			p.detailMode = "trainingInput"
			p.commandTextArea.Reset()
			inputWidth := p.textareaWidth()
			p.commandTextArea.SetWidth(inputWidth)
			cmd := p.commandTextArea.Focus()
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
		}
		return plugin.ConsumedAction()
	case "?":
		p.showHelp = !p.showHelp
		return plugin.ConsumedAction()
	case "g":
		p.gPending = true
		return plugin.NoopAction()
	case "esc":
		p.detailView = false
		p.detailMode = "viewing"
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

// detailCompleteTodo marks the current detail todo as done and shows a notice.
func (p *Plugin) detailCompleteTodo() plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		return plugin.NoopAction()
	}
	todo := *todoPtr
	p.undoStack = append(p.undoStack, undoEntry{
		todoID:     todo.ID,
		prevStatus: todo.Status,
		prevDoneAt: todo.CompletedAt,
		cursorPos:  p.ccCursor,
	})
	todoID := todo.ID

	// Kill any running agent session for this todo.
	killCmd := p.killAgent(todoID)

	// Sync ccCursor to the detail todo's position in filteredTodos BEFORE removal,
	// so that auto-advance in handleTickMsg picks the correct next item.
	p.syncCursorToDetailTodo()

	p.cc.CompleteTodo(todoID)
	p.publishEvent("todo.completed", map[string]interface{}{"id": todoID, "title": todo.Title})

	// Adjust list cursor to stay in bounds (use filteredTodos to match the view)
	newFiltered := len(p.filteredTodos())
	if p.ccCursor >= newFiltered && newFiltered > 0 {
		p.ccCursor = newFiltered - 1
	}
	if p.ccScrollOffset > p.ccCursor {
		p.ccScrollOffset = p.ccCursor
	}

	p.detailNotice = fmt.Sprintf("Done: %s", flattenTitle(todo.Title))
	p.detailNoticeType = "done"
	p.detailNoticeAt = time.Now()

	dbCmd := p.dbWriteCmd(func(database *sql.DB) error { return db.DBCompleteTodo(database, todoID) })
	cmds := []tea.Cmd{dbCmd, tea.ClearScreen}
	if killCmd != nil {
		cmds = append(cmds, killCmd)
	}
	if focusCmd := p.triggerFocusRefresh(); focusCmd != nil {
		cmds = append(cmds, focusCmd)
	}
	if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
		cmds = append(cmds, notifyCmd)
	}
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
}

// detailDismissTodo removes the current detail todo and shows a notice.
func (p *Plugin) detailDismissTodo() plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		return plugin.NoopAction()
	}
	todo := *todoPtr
	p.undoStack = append(p.undoStack, undoEntry{
		todoID:     todo.ID,
		prevStatus: todo.Status,
		prevDoneAt: todo.CompletedAt,
		cursorPos:  p.ccCursor,
	})
	todoID := todo.ID

	// Kill any running agent session for this todo.
	killCmd := p.killAgent(todoID)

	// Sync ccCursor to the detail todo's position in filteredTodos BEFORE removal,
	// so that auto-advance in handleTickMsg picks the correct next item.
	p.syncCursorToDetailTodo()

	p.cc.RemoveTodo(todoID)
	p.publishEvent("todo.dismissed", map[string]interface{}{"id": todoID, "title": todo.Title})

	// Adjust list cursor to stay in bounds (use filteredTodos to match the view)
	newFiltered := len(p.filteredTodos())
	if p.ccCursor >= newFiltered && newFiltered > 0 {
		p.ccCursor = newFiltered - 1
	}
	if p.ccScrollOffset > p.ccCursor {
		p.ccScrollOffset = p.ccCursor
	}

	p.detailNotice = fmt.Sprintf("Removed: %s", flattenTitle(todo.Title))
	p.detailNoticeType = "removed"
	p.detailNoticeAt = time.Now()

	dbCmd := p.dbWriteCmd(func(database *sql.DB) error { return db.DBDismissTodo(database, todoID) })
	cmds := []tea.Cmd{dbCmd, tea.ClearScreen}
	if killCmd != nil {
		cmds = append(cmds, killCmd)
	}
	if focusCmd := p.triggerFocusRefresh(); focusCmd != nil {
		cmds = append(cmds, focusCmd)
	}
	if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
		cmds = append(cmds, notifyCmd)
	}
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
}

func (p *Plugin) enterDetailFieldEdit() plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		return plugin.NoopAction()
	}
	todo := *todoPtr

	switch p.detailSelectedField {
	case 0: // Status — show inline selector
		p.detailMode = "selectingStatus"
		p.detailStatusCursor = 0
		for i, opt := range statusOptions {
			if opt == todo.Status {
				p.detailStatusCursor = i
				break
			}
		}
		return plugin.NoopAction()
	case 1: // Due — open text input
		p.detailMode = "editingField"
		p.detailFieldInput.Reset()
		p.detailFieldInput.Placeholder = "mm dd, or natural language"
		p.detailFieldInput.SetValue(todo.Due)
		p.detailFieldInput.Focus()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: textinput.Blink}
	case 2: // ProjectDir — open scrollable path picker
		// Reload paths from DB so newly added sessions are available.
		if p.database != nil {
			if paths, err := db.DBLoadPaths(p.database); err == nil {
				p.detailPaths = paths
			}
		}
		if len(p.detailPaths) == 0 {
			// No paths available; open text input instead
			p.detailMode = "editingField"
			p.detailFieldInput.Reset()
			p.detailFieldInput.Placeholder = "/path/to/project"
			p.detailFieldInput.SetValue(todo.ProjectDir)
			p.detailFieldInput.Focus()
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: textinput.Blink}
		}
		// Enter path selection mode
		p.detailMode = "selectingPath"
		p.detailPathFilter = ""
		p.detailPathCursor = 0
		for i, path := range p.detailPaths {
			if path == todo.ProjectDir {
				p.detailPathCursor = i
				break
			}
		}
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

func (p *Plugin) commitDetailFieldEdit(todo db.Todo, field, value string) plugin.Action {
	// Apply the change in-memory
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == todo.ID {
			switch field {
			case "status":
				p.cc.Todos[i].Status = value
			case "due":
				p.cc.Todos[i].Due = value
			case "project_dir":
				p.cc.Todos[i].ProjectDir = value
			case "proposed_prompt":
				p.cc.Todos[i].ProposedPrompt = value
			}
			// Persist full todo update
			updated := p.cc.Todos[i]
			dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
				return db.DBUpdateTodo(database, updated.ID, updated)
			})
			cmds := []tea.Cmd{dbCmd}
			if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
				cmds = append(cmds, notifyCmd)
			}
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
		}
	}
	return plugin.NoopAction()
}

func (p *Plugin) handleDetailStatusSelect(msg tea.KeyMsg) plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		p.detailMode = "viewing"
		return plugin.NoopAction()
	}
	todo := *todoPtr

	switch msg.String() {
	case "left", "h":
		if p.detailStatusCursor > 0 {
			p.detailStatusCursor--
		}
		return plugin.NoopAction()
	case "right", "l":
		if p.detailStatusCursor < len(statusOptions)-1 {
			p.detailStatusCursor++
		}
		return plugin.NoopAction()
	case "enter":
		newStatus := statusOptions[p.detailStatusCursor]
		p.detailMode = "viewing"
		return p.commitDetailFieldEdit(todo, "status", newStatus)
	case "esc":
		p.detailMode = "viewing"
		return plugin.NoopAction()
	}
	return plugin.NoopAction()
}

// filteredPaths returns the subset of detailPaths matching the current filter.
func (p *Plugin) filteredPaths() []string {
	if p.detailPathFilter == "" {
		return p.detailPaths
	}
	lower := strings.ToLower(p.detailPathFilter)
	var out []string
	for _, path := range p.detailPaths {
		if strings.Contains(strings.ToLower(path), lower) {
			out = append(out, path)
		}
	}
	return out
}

func (p *Plugin) handleDetailPathSelect(msg tea.KeyMsg) plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		p.detailMode = "viewing"
		return plugin.NoopAction()
	}
	todo := *todoPtr

	filtered := p.filteredPaths()

	switch msg.String() {
	case "up", "k":
		if p.detailPathCursor > 0 {
			p.detailPathCursor--
		}
		return plugin.NoopAction()
	case "down", "j":
		if p.detailPathCursor < len(filtered)-1 {
			p.detailPathCursor++
		}
		return plugin.NoopAction()
	case "enter":
		if len(filtered) > 0 && p.detailPathCursor < len(filtered) {
			newPath := filtered[p.detailPathCursor]
			p.detailMode = "viewing"
			p.detailPathFilter = ""
			return p.commitDetailFieldEdit(todo, "project_dir", newPath)
		}
		p.detailMode = "viewing"
		p.detailPathFilter = ""
		return plugin.NoopAction()
	case "esc":
		p.detailMode = "viewing"
		p.detailPathFilter = ""
		return plugin.NoopAction()
	case "backspace":
		if len(p.detailPathFilter) > 0 {
			p.detailPathFilter = p.detailPathFilter[:len(p.detailPathFilter)-1]
			p.detailPathCursor = 0
		}
		return plugin.NoopAction()
	default:
		// Typing characters filters the list
		key := msg.String()
		if len(key) == 1 {
			p.detailPathFilter += key
			p.detailPathCursor = 0
		}
		return plugin.NoopAction()
	}
}

func (p *Plugin) handleDetailEditingField(msg tea.KeyMsg) plugin.Action {
	todoPtr := p.detailTodo()
	if todoPtr == nil {
		p.detailMode = "viewing"
		p.detailFieldInput.Blur()
		return plugin.NoopAction()
	}
	todo := *todoPtr

	switch msg.String() {
	case "enter":
		value := strings.TrimSpace(p.detailFieldInput.Value())
		p.detailMode = "viewing"
		p.detailFieldInput.Blur()
		switch p.detailSelectedField {
		case 1: // Due
			if value == "" {
				return p.commitDetailFieldEdit(todo, "due", "")
			}
			parsed, ok := parseDueDate(value, time.Now())
			if ok {
				return p.commitDetailFieldEdit(todo, "due", parsed)
			}
			// Not a recognized format — use LLM to parse natural language
			p.claudeLoading = true
			p.claudeLoadingAt = time.Now()
			p.claudeLoadingMsg = "Parsing date..."
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: claudeDateParseCmd(p.llm, value, todo.ID)}
		case 2: // ProjectDir
			return p.commitDetailFieldEdit(todo, "project_dir", value)
		}
		return plugin.NoopAction()
	case "esc":
		p.detailMode = "viewing"
		p.detailFieldInput.Blur()
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.detailFieldInput, cmd = p.detailFieldInput.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

func (p *Plugin) handleDetailCommandInput(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "enter":
		// Enter submits the command (not a newline)
		instruction := strings.TrimSpace(p.commandTextArea.Value())
		if instruction == "" {
			return plugin.NoopAction()
		}
		todoPtr := p.detailTodo()
		if todoPtr == nil {
			p.detailMode = "viewing"
			p.commandTextArea.Blur()
			return plugin.NoopAction()
		}
		todo := *todoPtr
		prompt := buildEditPrompt(todo, instruction)
		p.detailView = false
		p.detailMode = "viewing"
		p.commandTextArea.Blur()
		p.commandTextArea.Reset()
		p.claudeLoading = true
		p.claudeLoadingAt = time.Now()
		p.claudeLoadingMsg = "Updating todo..."
		p.claudeLoadingTodo = todo.ID
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: claudeEditCmd(p.llm, prompt, todo.ID)}
	case "esc":
		p.detailMode = "viewing"
		p.commandTextArea.Blur()
		p.commandTextArea.Reset()
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.commandTextArea, cmd = p.commandTextArea.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

func (p *Plugin) handleDetailTrainingInput(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "enter":
		instruction := strings.TrimSpace(p.commandTextArea.Value())
		if instruction == "" {
			return plugin.NoopAction()
		}
		todoPtr := p.detailTodo()
		if todoPtr == nil {
			p.detailMode = "viewing"
			p.commandTextArea.Blur()
			return plugin.NoopAction()
		}
		todo := *todoPtr
		p.detailMode = "viewing"
		p.commandTextArea.Blur()
		p.commandTextArea.Reset()
		p.claudeLoading = true
		p.claudeLoadingAt = time.Now()
		p.claudeLoadingMsg = "Training prompt rules..."
		p.claudeLoadingTodo = todo.ID
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: claudeTrainCmd(p.llm, todo, instruction)}
	case "esc":
		p.detailMode = "viewing"
		p.commandTextArea.Blur()
		p.commandTextArea.Reset()
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.commandTextArea, cmd = p.commandTextArea.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

// sessionFileExists checks whether a Claude session file exists in any project directory.
func sessionFileExists(sessionID string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return false
	}
	sessionFile := sessionID + ".jsonl"
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(projectsDir, e.Name(), sessionFile)); err == nil {
			return true
		}
	}
	return false
}

// extractSessionIDFromLog reads the first few lines of a todo's session log
// to recover the session_id when it wasn't captured via the normal event flow.
func (p *Plugin) extractSessionIDFromLog(todo *db.Todo) string {
	logPath := todo.SessionLogPath
	if logPath == "" {
		return ""
	}
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	// Check first 10 lines for a session_id field.
	for i := 0; i < 10 && scanner.Scan(); i++ {
		line := scanner.Text()
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if sid, ok := event["session_id"].(string); ok && sid != "" {
			return sid
		}
	}
	return ""
}

// persistSessionID writes a recovered session ID to the DB in the background.
func (p *Plugin) persistSessionID(todoID, sessionID string) {
	if p.database == nil {
		return
	}
	go func() {
		_ = db.DBUpdateTodoSessionID(p.database, todoID, sessionID)
	}()
}

// handleDetailStarToggle handles the 's' key in detail view.
func (p *Plugin) handleDetailStarToggle(todo *db.Todo) plugin.Action {
	todoID := todo.ID
	if !todo.Starred {
		// Star it
		for i := range p.cc.Todos {
			if p.cc.Todos[i].ID == todoID {
				p.cc.Todos[i].Starred = true
				p.cc.Todos[i].Focus = true
				break
			}
		}
		p.openScheduleModal(todoID)
		dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
			return db.DBSetTodoStar(database, todoID, true)
		})
		cmds := []tea.Cmd{dbCmd}
		if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
			cmds = append(cmds, notifyCmd)
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
	}
	// Unstar: check for future bookings
	futureBookings, _ := db.DBGetFutureBookingsForTodo(p.database, todoID)
	if len(futureBookings) == 0 {
		for i := range p.cc.Todos {
			if p.cc.Todos[i].ID == todoID {
				p.cc.Todos[i].Starred = false
				break
			}
		}
		p.flashMessage = "Unstarred: " + todo.Title
		p.flashMessageAt = time.Now()
		dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
			return db.DBSetTodoStar(database, todoID, false)
		})
		cmds := []tea.Cmd{dbCmd}
		if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
			cmds = append(cmds, notifyCmd)
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
	}
	// Future bookings exist — enter confirm mode
	p.unstarConfirmMode = true
	p.unstarConfirmTodoID = todoID
	if len(futureBookings) == 1 {
		p.flashMessage = "Release calendar block? (y/n)"
	} else {
		p.flashMessage = fmt.Sprintf("Release %d calendar blocks? (y/n)", len(futureBookings))
	}
	p.flashMessageAt = time.Now()
	return plugin.ConsumedAction()
}

// handleDetailFocusToggle handles the 'f' key in detail view.
func (p *Plugin) handleDetailFocusToggle(todo *db.Todo) plugin.Action {
	todoID := todo.ID
	if todo.Focus {
		// If starred with future bookings, trigger unstar cleanup
		if todo.Starred {
			futureBookings, _ := db.DBGetFutureBookingsForTodo(p.database, todoID)
			if len(futureBookings) > 0 {
				p.unstarConfirmMode = true
				p.unstarConfirmTodoID = todoID
				p.unstarConfirmAlsoUnfocus = true
				if len(futureBookings) == 1 {
					p.flashMessage = "Release calendar block? (y/n)"
				} else {
					p.flashMessage = fmt.Sprintf("Release %d calendar blocks? (y/n)", len(futureBookings))
				}
				p.flashMessageAt = time.Now()
				return plugin.ConsumedAction()
			}
		}
		// Unfocus
		for i := range p.cc.Todos {
			if p.cc.Todos[i].ID == todoID {
				p.cc.Todos[i].Focus = false
				p.cc.Todos[i].Starred = false
				break
			}
		}
		p.flashMessage = "Unfocused: " + todo.Title
		p.flashMessageAt = time.Now()
		dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
			return db.DBSetTodoFocus(database, todoID, false)
		})
		cmds := []tea.Cmd{dbCmd}
		if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
			cmds = append(cmds, notifyCmd)
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
	}
	// Focus it
	for i := range p.cc.Todos {
		if p.cc.Todos[i].ID == todoID {
			p.cc.Todos[i].Focus = true
			break
		}
	}
	p.flashMessage = "Focused: " + todo.Title
	p.flashMessageAt = time.Now()
	dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
		return db.DBSetTodoFocus(database, todoID, true)
	})
	cmds := []tea.Cmd{dbCmd}
	if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
		cmds = append(cmds, notifyCmd)
	}
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
}
