package commandcenter

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/anutron/claude-command-center/internal/plugin"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// triageFilterOrder defines the tab order for triage filters in expanded view.
var triageFilterOrder = []string{"focus", "inbox", "agents", "review", "all"}

// HandleKey handles key input and returns an action.
func (p *Plugin) HandleKey(msg tea.KeyMsg) plugin.Action {
	// Help overlay: any key dismisses
	if p.showHelp {
		p.showHelp = false
		return plugin.ConsumedAction()
	}

	// Two-key chord: "g" prefix (Gmail-style shortcuts)
	if p.gPending {
		p.gPending = false
		if msg.String() == "i" || msg.String() == "u" {
			// "gi" = go inbox, "gu" = go up: return to list view from wherever we are
			p.detailView = false
			p.detailMode = "viewing"
			p.taskRunnerView = false
			p.sessionViewerActive = false
			p.detailNotice = ""
			return plugin.NoopAction()
		}
		// Any other key after "g" — not a recognized chord, fall through
	}

	// Session viewer (sub-view of detail)
	if p.sessionViewerActive && p.detailView {
		return p.handleSessionViewer(msg)
	}

	// Task runner view (sub-view of detail)
	if p.taskRunnerView && p.detailView {
		return p.handleTaskRunnerView(msg)
	}

	// Detail view
	if p.detailView {
		return p.handleDetailView(msg)
	}

	// Search input
	if p.searchActive {
		return p.handleSearchInput(msg)
	}

	// Quick todo entry
	if p.addingTodoQuick {
		return p.handleAddingTodoQuick(msg)
	}

	// Rich todo creation
	if p.addingTodoRich {
		return p.handleAddingTodoRich(msg)
	}

	// Schedule modal (vertical duration picker / booking acknowledgment)
	if p.scheduleModalActive {
		return p.handleScheduleModal(msg)
	}

	// Unstar confirm mode (after unstarring with future bookings: y/n)
	if p.unstarConfirmMode {
		return p.handleUnstarConfirm(msg)
	}

	// Help toggle
	if msg.String() == "?" {
		p.showHelp = !p.showHelp
		return plugin.ConsumedAction()
	}

	// Esc handling
	if msg.String() == "esc" {
		// Clear search filter if active (before collapsing expanded view)
		if strings.TrimSpace(p.searchInput.Value()) != "" {
			p.searchInput.SetValue("")
			p.ccCursor = 0
			p.ccScrollOffset = 0
			p.ccExpandedOffset = 0
			return plugin.NoopAction()
		}
		if p.ccExpanded {
			p.ccExpanded = false
			p.ccExpandedCols = 0
			p.ccExpandedOffset = 0
			p.ccScrollOffset = 0
			p.ccCursor = 0
			return plugin.NoopAction()
		}
		if p.pendingLaunchTodo != nil {
			p.pendingLaunchTodo = nil
			p.subView = "command"
			return plugin.NoopAction()
		}
		// Let host handle esc for quit
		return plugin.Action{Type: plugin.ActionUnhandled}
	}

	return p.handleCommandTab(msg)
}

func (p *Plugin) handleCommandTab(msg tea.KeyMsg) plugin.Action {
	if p.cc == nil {
		return plugin.NoopAction()
	}
	activeTodos := p.filteredTodos()
	maxCursor := len(activeTodos) - 1
	if maxCursor < 0 {
		maxCursor = 0
	}

	todoViewHeight := p.normalMaxVisibleTodos()

	switch msg.String() {
	case "up", "k":
		if p.ccExpanded {
			if p.ccCursor > 0 {
				p.ccCursor--
				if p.ccCursor < p.ccExpandedOffset {
					rowsPerCol := p.expandedRowsPerCol()
					numCols := p.expandedNumCols()
					pageSize := rowsPerCol * numCols
					p.ccExpandedOffset -= pageSize
					if p.ccExpandedOffset < 0 {
						p.ccExpandedOffset = 0
					}
				}
			} else {
				p.ccExpanded = false
				p.ccExpandedOffset = 0
				p.ccScrollOffset = 0
				if p.ccCursor > todoViewHeight-1 {
					p.ccCursor = todoViewHeight - 1
				}
			}
		} else {
			if p.ccCursor > 0 {
				p.ccCursor--
				if p.ccCursor < p.ccScrollOffset {
					p.ccScrollOffset = p.ccCursor
				}
			}
		}
		return plugin.NoopAction()

	case "down", "j":
		if p.ccExpanded {
			if p.ccCursor < maxCursor {
				p.ccCursor++
				rowsPerCol := p.expandedRowsPerCol()
				numCols := p.expandedNumCols()
				pageSize := rowsPerCol * numCols
				if p.ccCursor >= p.ccExpandedOffset+pageSize {
					p.ccExpandedOffset += pageSize
				}
			}
		} else {
			if p.ccCursor < maxCursor {
				p.ccCursor++
				if p.ccCursor >= todoViewHeight {
					// Auto-expand when cursor moves past visible area.
					// Set triageFilter to "all" so expanded view shows the same
					// items as the collapsed view (all non-new), not just backlog.
					p.ccExpanded = true
					p.ccExpandedCols = 2
					p.ccExpandedOffset = 0
					p.triageFilter = "all"
				}
			}
		}
		return plugin.NoopAction()

	case "left", "h":
		if p.ccExpanded {
			rowsPerCol := p.expandedRowsPerCol()
			numCols := p.expandedNumCols()
			pageSize := rowsPerCol * numCols
			relIdx := p.ccCursor - p.ccExpandedOffset
			col := relIdx / rowsPerCol
			row := relIdx % rowsPerCol
			if col > 0 {
				// Move to previous column on same page
				p.ccCursor = p.ccExpandedOffset + (col-1)*rowsPerCol + row
				if p.ccCursor > maxCursor {
					p.ccCursor = maxCursor
				}
			} else if p.ccExpandedOffset > 0 {
				// Paginate left: go to previous page, land in last column same row
				p.ccExpandedOffset -= pageSize
				if p.ccExpandedOffset < 0 {
					p.ccExpandedOffset = 0
				}
				p.ccCursor = p.ccExpandedOffset + (numCols-1)*rowsPerCol + row
				if p.ccCursor > maxCursor {
					p.ccCursor = maxCursor
				}
			}
		}
		return plugin.NoopAction()

	case "right", "l":
		if p.ccExpanded {
			rowsPerCol := p.expandedRowsPerCol()
			numCols := p.expandedNumCols()
			pageSize := rowsPerCol * numCols
			relIdx := p.ccCursor - p.ccExpandedOffset
			col := relIdx / rowsPerCol
			row := relIdx % rowsPerCol
			if col < numCols-1 {
				// Move to next column on same page
				target := p.ccExpandedOffset + (col+1)*rowsPerCol + row
				if target > maxCursor {
					target = maxCursor
				}
				p.ccCursor = target
			} else if p.ccExpandedOffset+pageSize <= maxCursor {
				// Paginate right: go to next page, land in first column same row
				p.ccExpandedOffset += pageSize
				p.ccCursor = p.ccExpandedOffset + row
				if p.ccCursor > maxCursor {
					p.ccCursor = maxCursor
				}
			}
		}
		return plugin.NoopAction()

	case "shift+up":
		if len(activeTodos) > 1 && p.ccCursor > 0 && p.ccCursor < len(activeTodos) {
			// Find the absolute indices in cc.Todos for the two active todos
			activeA := p.ccCursor - 1
			activeB := p.ccCursor
			idA := activeTodos[activeA].ID
			idB := activeTodos[activeB].ID
			// Find absolute indices
			absA, absB := -1, -1
			for i := range p.cc.Todos {
				if p.cc.Todos[i].ID == idA {
					absA = i
				}
				if p.cc.Todos[i].ID == idB {
					absB = i
				}
			}
			if absA >= 0 && absB >= 0 {
				p.cc.SwapTodos(absA, absB)
				p.ccCursor--
				if p.ccCursor < p.ccScrollOffset {
					p.ccScrollOffset = p.ccCursor
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: p.dbWriteCmd(func(database *sql.DB) error {
					return db.DBSwapTodoOrder(database, idA, idB)
				})}
			}
		}
		return plugin.NoopAction()

	case "shift+down":
		if len(activeTodos) > 1 && p.ccCursor < len(activeTodos)-1 {
			activeA := p.ccCursor
			activeB := p.ccCursor + 1
			idA := activeTodos[activeA].ID
			idB := activeTodos[activeB].ID
			absA, absB := -1, -1
			for i := range p.cc.Todos {
				if p.cc.Todos[i].ID == idA {
					absA = i
				}
				if p.cc.Todos[i].ID == idB {
					absB = i
				}
			}
			if absA >= 0 && absB >= 0 {
				p.cc.SwapTodos(absA, absB)
				p.ccCursor++
				todoViewHeight := p.normalMaxVisibleTodos()
				if p.ccCursor >= p.ccScrollOffset+todoViewHeight {
					p.ccScrollOffset++
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: p.dbWriteCmd(func(database *sql.DB) error {
					return db.DBSwapTodoOrder(database, idA, idB)
				})}
			}
		}
		return plugin.NoopAction()

	case "x":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			p.undoStack = append(p.undoStack, undoEntry{
				todoID:     todo.ID,
				prevStatus: todo.Status,
				prevDoneAt: todo.CompletedAt,
				cursorPos:  p.ccCursor,
			})
			todoID := todo.ID
			killCmd := p.killAgent(todoID)
			p.cc.CompleteTodo(todoID)
			p.publishEvent("todo.completed", map[string]interface{}{"id": todoID, "title": todo.Title})
			newFiltered := len(p.filteredTodos())
			if p.ccCursor >= newFiltered && newFiltered > 0 {
				p.ccCursor = newFiltered - 1
			}
			if p.ccScrollOffset > p.ccCursor {
				p.ccScrollOffset = p.ccCursor
			}
			p.clampExpandedOffset()
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
		return plugin.NoopAction()

	case "X":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			p.undoStack = append(p.undoStack, undoEntry{
				todoID:     todo.ID,
				prevStatus: todo.Status,
				prevDoneAt: todo.CompletedAt,
				cursorPos:  p.ccCursor,
			})
			todoID := todo.ID
			killCmd := p.killAgent(todoID)
			p.cc.RemoveTodo(todoID)
			p.publishEvent("todo.dismissed", map[string]interface{}{"id": todoID, "title": todo.Title})
			newFiltered := len(p.filteredTodos())
			if p.ccCursor >= newFiltered && newFiltered > 0 {
				p.ccCursor = newFiltered - 1
			}
			if p.ccScrollOffset > p.ccCursor {
				p.ccScrollOffset = p.ccCursor
			}
			p.clampExpandedOffset()
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
		return plugin.NoopAction()

	case "u":
		if len(p.undoStack) > 0 {
			entry := p.undoStack[len(p.undoStack)-1]
			p.undoStack = p.undoStack[:len(p.undoStack)-1]
			p.cc.RestoreTodo(entry.todoID, entry.prevStatus, entry.prevDoneAt)
			p.ccCursor = entry.cursorPos
			if p.ccCursor >= len(p.cc.ActiveTodos()) && len(p.cc.ActiveTodos()) > 0 {
				p.ccCursor = len(p.cc.ActiveTodos()) - 1
			}
			p.flashMessage = "Undid last action"
			p.flashMessageAt = time.Now()
			prevStatus := entry.prevStatus
			prevDoneAt := entry.prevDoneAt
			dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
				return db.DBRestoreTodo(database, entry.todoID, prevStatus, prevDoneAt)
			})
			cmds := []tea.Cmd{dbCmd, tea.ClearScreen}
			if focusCmd := p.triggerFocusRefresh(); focusCmd != nil {
				cmds = append(cmds, focusCmd)
			}
			if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
				cmds = append(cmds, notifyCmd)
			}
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
		}
		return plugin.NoopAction()

	case "d":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			todoID := todo.ID
			p.cc.DeferTodo(todoID)
			p.publishEvent("todo.deferred", map[string]interface{}{"id": todoID, "title": todo.Title})
			dbCmd := p.dbWriteCmd(func(database *sql.DB) error { return db.DBDeferTodo(database, todoID) })
			if focusCmd := p.triggerFocusRefresh(); focusCmd != nil {
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(dbCmd, focusCmd)}
			}
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: dbCmd}
		}
		return plugin.NoopAction()

	case "p":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			todoID := todo.ID
			p.cc.PromoteTodo(todoID)
			p.publishEvent("todo.promoted", map[string]interface{}{"id": todoID, "title": todo.Title})
			p.ccCursor = 0
			p.ccScrollOffset = 0
			dbCmd := p.dbWriteCmd(func(database *sql.DB) error { return db.DBPromoteTodo(database, todoID) })
			if focusCmd := p.triggerFocusRefresh(); focusCmd != nil {
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(dbCmd, focusCmd)}
			}
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: dbCmd}
		}
		return plugin.NoopAction()

	case " ":
		// Cycle expanded view: collapsed → 2-col → 1-col → collapsed
		if !p.ccExpanded {
			p.ccExpanded = true
			p.ccExpandedCols = 2
			p.ccExpandedOffset = 0
		} else if p.ccExpandedCols == 2 {
			p.ccExpandedCols = 1
			p.ccExpandedOffset = 0
		} else {
			p.ccExpanded = false
			p.ccExpandedCols = 0
			p.ccExpandedOffset = 0
			p.ccScrollOffset = 0
			if p.ccCursor >= todoViewHeight {
				p.ccCursor = todoViewHeight - 1
			}
		}
		return plugin.NoopAction()

	case "c":
		ensureCC(&p.cc)
		p.addingTodoRich = true
		p.flashMessage = ""
		p.commandConversation = nil
		p.todoTextArea.Reset()
		taWidth := p.textareaWidth()
		p.todoTextArea.SetWidth(taWidth)
		cmd := p.todoTextArea.Focus()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}

	case "t":
		ensureCC(&p.cc)
		p.addingTodoQuick = true
		p.flashMessage = ""
		p.quickTodoTextArea.Reset()
		taWidth := p.textareaWidth()
		p.quickTodoTextArea.SetWidth(taWidth)
		cmd := p.quickTodoTextArea.Focus()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}

	case "/":
		p.searchActive = true
		p.searchInput.Focus()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: textinput.Blink}

	case "tab":
		if p.ccExpanded {
			// Cycle triage filter forward
			idx := 0
			for i, f := range triageFilterOrder {
				if f == p.triageFilter {
					idx = i
					break
				}
			}
			p.triageFilter = triageFilterOrder[(idx+1)%len(triageFilterOrder)]
			p.ccCursor = 0
			p.ccExpandedOffset = 0
			return plugin.ConsumedAction()
		}
		return plugin.Action{Type: plugin.ActionUnhandled}

	case "shift+tab":
		if p.ccExpanded {
			// Cycle triage filter backward
			idx := 0
			for i, f := range triageFilterOrder {
				if f == p.triageFilter {
					idx = i
					break
				}
			}
			p.triageFilter = triageFilterOrder[(idx-1+len(triageFilterOrder))%len(triageFilterOrder)]
			p.ccCursor = 0
			p.ccExpandedOffset = 0
			return plugin.ConsumedAction()
		}
		return plugin.Action{Type: plugin.ActionUnhandled}

	case "y":
		if p.ccExpanded {
			filtered := p.filteredTodos()
			if len(filtered) > 0 && p.ccCursor < len(filtered) {
				todo := filtered[p.ccCursor]
				p.cc.AcceptTodo(todo.ID)
				todoID := todo.ID
				// Adjust cursor if the filtered list will shrink
				newFiltered := p.filteredTodos()
				if p.ccCursor >= len(newFiltered) && len(newFiltered) > 0 {
					p.ccCursor = len(newFiltered) - 1
				}
				dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
					return db.DBAcceptTodo(database, todoID)
				})
				cmds := []tea.Cmd{dbCmd}
				if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
					cmds = append(cmds, notifyCmd)
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
			}
		}
		return plugin.NoopAction()

	case "Y":
		if p.ccExpanded {
			filtered := p.filteredTodos()
			if len(filtered) > 0 && p.ccCursor < len(filtered) {
				todo := filtered[p.ccCursor]
				p.cc.AcceptTodo(todo.ID)
				todoID := todo.ID

				// Enter detail/task runner view
				p.detailView = true
				p.detailTodoID = todoID
				p.detailMode = "viewing"
				p.detailSelectedField = 0
				p.mergeSourceCursor = 0
				p.textInput.Reset()
				p.textInput.Placeholder = "Tell me what changed..."
				p.detailFieldInput.Reset()
				p.enterTaskRunner(todo)

				dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
					return db.DBAcceptTodo(database, todoID)
				})
				cmds := []tea.Cmd{dbCmd}
				if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
					cmds = append(cmds, notifyCmd)
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
			}
		}
		return plugin.NoopAction()

	case "b":
		p.showBacklog = !p.showBacklog
		p.ccCursor = 0
		p.ccScrollOffset = 0
		return plugin.NoopAction()

	case "s":
		// Star toggle
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			todoID := todo.ID
			if !todo.Starred {
				// Star it: set starred=true and focused=true
				for i := range p.cc.Todos {
					if p.cc.Todos[i].ID == todoID {
						p.cc.Todos[i].Starred = true
						p.cc.Todos[i].Focus = true
						break
					}
				}
				p.cc.AcceptTodo(todoID)
				p.openScheduleModal(todoID)
				dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
					if err := db.DBAcceptTodo(database, todoID); err != nil {
						return err
					}
					return db.DBSetTodoStar(database, todoID, true)
				})
				cmds := []tea.Cmd{dbCmd}
				if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
					cmds = append(cmds, notifyCmd)
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmds...)}
			}
			// Unstar it: check for future bookings
			futureBookings, err := db.DBGetFutureBookingsForTodo(p.database, todoID)
			numFutureBookings := len(futureBookings)
			if err != nil || numFutureBookings == 0 {
				// No future bookings — unstar immediately
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
			// Future bookings exist — ask to release them
			p.unstarConfirmMode = true
			p.unstarConfirmTodoID = todoID
			if numFutureBookings == 1 {
				p.flashMessage = "Release calendar block? (y/n)"
			} else {
				p.flashMessage = fmt.Sprintf("Release %d calendar blocks? (y/n)", numFutureBookings)
			}
			p.flashMessageAt = time.Now()
			return plugin.NoopAction()
		}
		return plugin.NoopAction()

	case "S":
		// Schedule: open schedule modal (star if not already starred)
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			todoID := todo.ID
			p.openScheduleModal(todoID)
			if !todo.Starred {
				for i := range p.cc.Todos {
					if p.cc.Todos[i].ID == todoID {
						p.cc.Todos[i].Starred = true
						p.cc.Todos[i].Focus = true
						break
					}
				}
				dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
					return db.DBSetTodoStar(database, todoID, true)
				})
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: dbCmd}
			}
		}
		return plugin.NoopAction()

	case "f":
		// Focus toggle
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			todoID := todo.ID
			if todo.Focus {
				// If starred with future bookings, trigger unstar cleanup flow first
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
						return plugin.NoopAction()
					}
				}
				// Unfocus: remove focus and star
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
				cmdsUnfocus := []tea.Cmd{dbCmd}
				if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
					cmdsUnfocus = append(cmdsUnfocus, notifyCmd)
				}
				return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmdsUnfocus...)}
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
			cmdsFocus := []tea.Cmd{dbCmd}
			if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
				cmdsFocus = append(cmdsFocus, notifyCmd)
			}
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmdsFocus...)}
		}
		return plugin.NoopAction()

	case "r":
		if !p.ccRefreshing && p.cfg.RefreshEnabled() {
			p.ccRefreshing = true
			p.ccLastRefreshTriggered = time.Now()
			p.flashMessage = "Refreshing..."
			p.flashMessageAt = time.Now()
			return plugin.Action{Type: plugin.ActionNoop, TeaCmd: refreshCCCmd()}
		}
		if p.ccRefreshing {
			p.flashMessage = "Already refreshing..."
			p.flashMessageAt = time.Now()
		}
		return plugin.NoopAction()

	case "enter":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			p.detailView = true
			p.detailTodoID = activeTodos[p.ccCursor].ID
			p.detailMode = "viewing"
			p.detailSelectedField = 0
			p.mergeSourceCursor = 0
			p.textInput.Reset()
			p.textInput.Placeholder = "Tell me what changed..."
			p.detailFieldInput.Reset()
			return plugin.NoopAction()
		}
		return plugin.NoopAction()

	case "o":
		if len(activeTodos) > 0 && p.ccCursor < len(activeTodos) {
			todo := activeTodos[p.ccCursor]
			sid := todo.SessionID
			// Try to recover session ID from log file if missing.
			if sid == "" {
				sid = p.extractSessionIDFromLog(&todo)
			}
			// If todo has an existing session, resume it directly
			if sid != "" {
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
			// Otherwise, enter detail view + task runner (don't launch directly)
			p.detailView = true
			p.detailTodoID = todo.ID
			p.detailMode = "viewing"
			p.detailSelectedField = 0
			p.mergeSourceCursor = 0
			p.textInput.Reset()
			p.detailFieldInput.Reset()
			p.enterTaskRunner(todo)
			return plugin.NoopAction()
		}
		return plugin.NoopAction()

	case "g":
		p.gPending = true
		return plugin.NoopAction()
	}

	return plugin.NoopAction()
}


func (p *Plugin) handleAddingTodoRich(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "ctrl+d":
		text := strings.TrimSpace(p.todoTextArea.Value())
		if text == "" {
			p.addingTodoRich = false
			p.todoTextArea.Blur()
			p.commandConversation = nil
			return plugin.NoopAction()
		}
		p.commandConversation = append(p.commandConversation, commandTurn{role: "user", text: text})
		prompt := buildCommandPromptWithHistory(p.cc, p.cfg.Name, p.commandConversation)
		p.addingTodoRich = false
		p.todoTextArea.Blur()
		p.claudeLoading = true
		p.claudeLoadingAt = time.Now()
		p.claudeLoadingMsg = "Processing..."
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: claudeCommandCmd(p.llm, prompt, "")}

	case "esc":
		p.addingTodoRich = false
		p.todoTextArea.Blur()
		p.commandConversation = nil
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.todoTextArea, cmd = p.todoTextArea.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

func (p *Plugin) handleAddingTodoQuick(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "ctrl+d":
		text := strings.TrimSpace(p.quickTodoTextArea.Value())
		if text == "" {
			p.addingTodoQuick = false
			p.quickTodoTextArea.Blur()
			return plugin.NoopAction()
		}
		p.addingTodoQuick = false
		p.quickTodoTextArea.Blur()
		p.claudeLoading = true
		p.claudeLoadingAt = time.Now()
		p.claudeLoadingMsg = "Creating todo..."
		prompt := buildEnrichPrompt(text, p.cc.ActiveTodos())
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: claudeEnrichCmd(p.llm, prompt)}

	case "esc":
		p.addingTodoQuick = false
		p.quickTodoTextArea.Blur()
		return plugin.NoopAction()
	}

	var cmd tea.Cmd
	p.quickTodoTextArea, cmd = p.quickTodoTextArea.Update(msg)
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

// openScheduleModal opens the schedule modal for the given todo.
func (p *Plugin) openScheduleModal(todoID string) {
	p.scheduleModalActive = true
	p.scheduleModalState = "picker"
	p.scheduleModalCursor = 2 // default to 1h
	p.scheduleModalTodoID = todoID
	p.scheduleModalLastBooking = ""
}

func (p *Plugin) handleScheduleModal(msg tea.KeyMsg) plugin.Action {
	if p.scheduleModalState == "booked" {
		switch msg.String() {
		case "S":
			// Schedule another block
			p.scheduleModalState = "picker"
			p.scheduleModalCursor = 2
			return plugin.NoopAction()
		case "esc":
			p.scheduleModalActive = false
			return plugin.NoopAction()
		}
		return plugin.NoopAction()
	}

	// Picker state
	switch msg.String() {
	case "up", "k":
		if p.scheduleModalCursor > 0 {
			p.scheduleModalCursor--
		}
		return plugin.NoopAction()

	case "down", "j":
		if p.scheduleModalCursor < len(bookingDurations)-1 {
			p.scheduleModalCursor++
		}
		return plugin.NoopAction()

	case "enter":
		todoID := p.scheduleModalTodoID
		dur := bookingDurations[p.scheduleModalCursor]
		// Find the todo title
		var title string
		if p.cc != nil {
			for _, t := range p.cc.Todos {
				if t.ID == todoID {
					title = t.Title
					break
				}
			}
		}
		p.flashMessage = fmt.Sprintf("Booking %dm for %s...", dur, title)
		p.flashMessageAt = time.Now()
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: scheduleBlockCmd(p, todoID, title, dur)}

	case "esc":
		p.scheduleModalActive = false
		return plugin.NoopAction()
	}

	return plugin.NoopAction()
}

func (p *Plugin) handleUnstarConfirm(msg tea.KeyMsg) plugin.Action {
	todoID := p.unstarConfirmTodoID
	alsoUnfocus := p.unstarConfirmAlsoUnfocus
	p.unstarConfirmMode = false
	p.unstarConfirmTodoID = ""
	p.unstarConfirmAlsoUnfocus = false
	p.flashMessage = ""

	switch msg.String() {
	case "y":
		// Unstar and release bookings: delete calendar events + DB records.
		for i := range p.cc.Todos {
			if p.cc.Todos[i].ID == todoID {
				p.cc.Todos[i].Starred = false
				if alsoUnfocus {
					p.cc.Todos[i].Focus = false
				}
				break
			}
		}
		dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
			if alsoUnfocus {
				return db.DBSetTodoFocus(database, todoID, false) // clears both
			}
			return db.DBSetTodoStar(database, todoID, false) // clears only star
		})
		relCmd := releaseBookingsCmd(p, todoID)
		cmdsY := []tea.Cmd{dbCmd, relCmd}
		if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
			cmdsY = append(cmdsY, notifyCmd)
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmdsY...)}

	case "n":
		// Unstar but keep bookings.
		for i := range p.cc.Todos {
			if p.cc.Todos[i].ID == todoID {
				p.cc.Todos[i].Starred = false
				if alsoUnfocus {
					p.cc.Todos[i].Focus = false
				}
				break
			}
		}
		dbCmd := p.dbWriteCmd(func(database *sql.DB) error {
			if alsoUnfocus {
				return db.DBSetTodoFocus(database, todoID, false)
			}
			return db.DBSetTodoStar(database, todoID, false)
		})
		cmdsN := []tea.Cmd{dbCmd}
		if notifyCmd := p.notifyPeersCmd("data.refreshed"); notifyCmd != nil {
			cmdsN = append(cmdsN, notifyCmd)
		}
		return plugin.Action{Type: plugin.ActionNoop, TeaCmd: tea.Batch(cmdsN...)}

	default:
		// Any other key: cancel, stay starred
		return plugin.NoopAction()
	}
}

func (p *Plugin) handleSearchInput(msg tea.KeyMsg) plugin.Action {
	switch msg.String() {
	case "enter":
		p.searchActive = false
		p.searchInput.Blur()
		// Ensure cursor is valid for the (possibly shorter) filtered list
		filtered := p.filteredTodos()
		if p.ccCursor >= len(filtered) {
			if len(filtered) > 0 {
				p.ccCursor = len(filtered) - 1
			} else {
				p.ccCursor = 0
			}
		}
		p.ccScrollOffset = 0
		p.ccExpandedOffset = 0
		// Open the selected item directly — skip the intermediate "frozen filter" state
		if len(filtered) > 0 && p.ccCursor < len(filtered) {
			p.detailView = true
			p.detailTodoID = filtered[p.ccCursor].ID
			p.detailMode = "viewing"
			p.detailSelectedField = 0
			p.mergeSourceCursor = 0
			p.textInput.Reset()
			p.textInput.Placeholder = "Tell me what changed..."
			p.detailFieldInput.Reset()
		}
		return plugin.NoopAction()
	case "esc":
		p.searchActive = false
		p.searchInput.Blur()
		p.searchInput.SetValue("")
		p.ccCursor = 0
		p.ccScrollOffset = 0
		p.ccExpandedOffset = 0
		return plugin.NoopAction()
	}

	prevQuery := p.searchInput.Value()
	var cmd tea.Cmd
	p.searchInput, cmd = p.searchInput.Update(msg)
	// Reset cursor when filter changes
	if p.searchInput.Value() != prevQuery {
		p.ccCursor = 0
		p.ccScrollOffset = 0
		p.ccExpandedOffset = 0
	}
	return plugin.Action{Type: plugin.ActionNoop, TeaCmd: cmd}
}

// parseDueDate attempts to parse common date input formats.
// Returns (YYYY-MM-DD, true) if recognized, or ("", false) if LLM fallback is needed.
func parseDueDate(input string, now time.Time) (string, bool) {
	input = strings.TrimSpace(input)

	// Already YYYY-MM-DD
	if _, err := time.Parse("2006-01-02", input); err == nil {
		return input, true
	}

	// Try "mm dd" or "m dd" or "mm d" or "m d" (space-separated)
	parts := strings.Fields(input)
	if len(parts) == 2 {
		var month, day int
		if _, err := fmt.Sscanf(parts[0], "%d", &month); err == nil {
			if _, err := fmt.Sscanf(parts[1], "%d", &day); err == nil {
				if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
					year := now.Year()
					candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, now.Location())
					// Use next year if the date has already passed
					if candidate.Before(now.Truncate(24 * time.Hour)) {
						year++
					}
					return fmt.Sprintf("%04d-%02d-%02d", year, month, day), true
				}
			}
		}
	}

	return "", false
}
