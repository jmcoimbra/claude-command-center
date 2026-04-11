package commandcenter

import (
	"fmt"
	"strings"
	"time"

	"github.com/anutron/claude-command-center/internal/db"
	"github.com/charmbracelet/lipgloss"
)

// renderDetailViewScrollable renders the detail view with a scrollable viewport.
func (p *Plugin) renderDetailViewScrollable(width, height int) string {
	todo := p.detailTodo()
	if todo == nil {
		return ""
	}

	s := &p.styles
	innerWidth := width - 4
	if innerWidth < 40 {
		innerWidth = 40
	}

	hasActiveSession := todo.Status == db.StatusRunning || todo.Status == db.StatusBlocked

	// Build the full body content (no truncation).
	body := p.buildDetailBody(s, *todo, innerWidth, hasActiveSession)

	// Footer hints (always visible, outside viewport)
	hints := p.buildDetailHints(s, *todo, hasActiveSession)

	// Command input section (pinned to bottom, outside viewport)
	var commandSection string
	if p.detailMode == "commandInput" || p.detailMode == "trainingInput" {
		divider := s.DescMuted.Render(strings.Repeat("\u2500", innerWidth-2))
		label := "Tell me what changed:"
		if p.detailMode == "trainingInput" {
			label = "Train routing & prompt rules (applies to all future todos):"
		}
		inputLabel := s.DescMuted.Render(label)
		indentedInput := lipgloss.NewStyle().PaddingLeft(2).Render(p.commandTextArea.View())
		commandSection = lipgloss.JoinVertical(lipgloss.Left,
			"  "+divider,
			"  "+inputLabel,
			indentedInput,
		)
	}

	// Viewport sizing: total height minus hints(1) + blank(1) + border(2) = 4 lines of chrome
	fixedChrome := 4
	if commandSection != "" {
		fixedChrome += lipgloss.Height(commandSection) + 1 // +1 for blank line
	}
	vpHeight := height - fixedChrome
	if vpHeight < 5 {
		vpHeight = 5
	}
	vpWidth := innerWidth - 2

	// Initialize or resize viewport
	if !p.detailVPReady || p.detailVP.Width != vpWidth || p.detailVP.Height != vpHeight {
		p.detailVP.Width = vpWidth
		p.detailVP.Height = vpHeight
		p.detailVPReady = true
	}
	p.detailVP.SetContent(body)

	parts := []string{
		p.detailVP.View(),
	}
	if commandSection != "" {
		parts = append(parts, "", commandSection)
	}
	parts = append(parts, "", "  "+hints)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return s.PanelBorder.Width(innerWidth).Height(height - 2).Render(content)
}

// buildDetailBody renders the full detail content for the viewport (no truncation).
func (p *Plugin) buildDetailBody(s *ccStyles, todo db.Todo, innerWidth int, hasActiveSession bool) string {
	title := s.SectionHeader.Render(fmt.Sprintf("TODO #%d", todo.DisplayID))
	star := starPrefix(s, todo)
	todoTitle := lipgloss.NewStyle().Foreground(s.ColorWhite).Bold(true).Render(star + todo.Title)

	// Two-column layout for fields
	colWidth := (innerWidth - 6) / 2
	if colWidth < 20 {
		colWidth = 20
	}

	fieldStr := p.buildDetailFields(s, todo, colWidth)

	// Path picker
	pathPickerSection := p.buildPathPicker(s, innerWidth)

	// Session status
	var sessionSection string
	if hasActiveSession {
		spinnerChar := refreshSpinner(p.frame)
		sessionIndicator := spinnerChar + " " + lipgloss.NewStyle().Foreground(s.ColorCyan).Bold(true).Render("Agent running")
		sessionSection = "\n  " + sessionIndicator
	} else if todo.Status == db.StatusReview || todo.Status == db.StatusFailed {
		statusLabel := "completed"
		statusColor := s.ColorGreen
		if todo.Status == db.StatusFailed {
			statusLabel = "failed"
			statusColor = s.ColorYellow
		}
		sessionIndicator := lipgloss.NewStyle().Foreground(statusColor).Bold(true).Render("\u25cf Session: " + statusLabel)
		sessionSection = "\n  " + sessionIndicator
	}

	// Training indicator
	var trainingSection string
	if p.claudeLoading && p.claudeLoadingTodo == todo.ID {
		spinnerChar := refreshSpinner(p.frame)
		label := p.claudeLoadingMsg
		if label == "" {
			label = "Working..."
		}
		elapsed := time.Since(p.claudeLoadingAt).Truncate(time.Second)
		label = fmt.Sprintf("%s (%s)", label, elapsed)
		trainingIndicator := spinnerChar + " " + lipgloss.NewStyle().Foreground(s.ColorCyan).Bold(true).Render(label)
		trainingSection = "\n  " + trainingIndicator
	}

	// Session summary — full, no truncation, with markdown rendering
	var summarySection string
	if todo.SessionSummary != "" {
		summaryHeader := s.SectionHeader.Render("  SESSION SUMMARY")
		wrapped := wrapText(todo.SessionSummary, innerWidth-6)
		bodyStyle := lipgloss.NewStyle().Foreground(s.ColorWhite)
		rendered := renderMarkdown(wrapped, s.SectionHeader, bodyStyle, s.DescMuted)
		var summaryLines []string
		for _, line := range strings.Split(rendered, "\n") {
			summaryLines = append(summaryLines, "   "+line)
		}
		summaryBody := strings.Join(summaryLines, "\n")
		summarySection = lipgloss.JoinVertical(lipgloss.Left, "", summaryHeader, "", summaryBody)
	}

	// Detail section — full, no truncation, with markdown rendering
	var detailSection string
	if todo.Detail != "" {
		detailHeader := s.SectionHeader.Render("  DETAIL")
		wrapped := wrapText(todo.Detail, innerWidth-6)
		bodyStyle := lipgloss.NewStyle().Foreground(s.ColorWhite)
		rendered := renderMarkdown(wrapped, s.SectionHeader, bodyStyle, s.DescMuted)
		var detailLines []string
		for _, line := range strings.Split(rendered, "\n") {
			detailLines = append(detailLines, "   "+line)
		}
		detailBody := strings.Join(detailLines, "\n")
		detailSection = lipgloss.JoinVertical(lipgloss.Left, "", detailHeader, "", detailBody)
	}

	// Prompt section — full, no truncation, with markdown rendering
	var promptSection string
	if todo.ProposedPrompt != "" {
		promptHeader := s.SectionHeader.Render("  PROMPT")
		wrapped := wrapText(todo.ProposedPrompt, innerWidth-6)
		bodyStyle := lipgloss.NewStyle().Foreground(s.ColorWhite)
		rendered := renderMarkdown(wrapped, s.SectionHeader, bodyStyle, s.DescMuted)
		var promptLines []string
		for _, line := range strings.Split(rendered, "\n") {
			promptLines = append(promptLines, "   "+line)
		}
		promptBody := strings.Join(promptLines, "\n")
		promptSection = lipgloss.JoinVertical(lipgloss.Left, "", promptHeader, "", promptBody)
	} else {
		promptSection = "\n  " + s.SectionHeader.Render("PROMPT") + "  " + s.DescMuted.Render("(no prompt set)")
	}

	// Notice banner
	var noticeBanner string
	if p.detailNotice != "" {
		bgColor := s.ColorGreen
		icon := "\u2713"
		if p.detailNoticeType == "removed" {
			bgColor = s.ColorYellow
			icon = "\u2717"
		}
		noticeBanner = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(bgColor).
			Bold(true).
			Padding(0, 1).
			Render(icon + " " + p.detailNotice)
	}

	// Assemble parts
	parts := []string{
		"  " + title,
		"",
	}
	if noticeBanner != "" {
		parts = append(parts, "  "+noticeBanner, "")
	}
	parts = append(parts,
		"  "+todoTitle,
		"",
		fieldStr,
	)
	if pathPickerSection != "" {
		parts = append(parts, pathPickerSection)
	}
	if sessionSection != "" {
		parts = append(parts, sessionSection)
	}
	if trainingSection != "" {
		parts = append(parts, trainingSection)
	}
	if summarySection != "" {
		parts = append(parts, summarySection)
	}
	if detailSection != "" {
		parts = append(parts, detailSection)
	}

	// Sources section — only for synthesis todos
	sourcesSection := p.buildSourcesSection(s, todo, innerWidth)
	if sourcesSection != "" {
		parts = append(parts, sourcesSection)
	}

	parts = append(parts, promptSection)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// buildDetailFields renders the two-column field layout.
func (p *Plugin) buildDetailFields(s *ccStyles, todo db.Todo, colWidth int) string {
	type fieldEntry struct {
		label string
		value string
		idx   int
	}
	editableFields := []fieldEntry{
		{"Status", todo.Status, 0},
		{"Due", "", 1},
		{"Project", shortDirName(todo.ProjectDir), 2},
	}
	if todo.Due != "" {
		urgency := db.DueUrgency(todo.Due)
		label := db.FormatDueLabel(todo.Due)
		editableFields[1].value = s.DueStyle(urgency).Render(todo.Due + " (" + label + ")")
	}

	type roField struct {
		label string
		value string
	}
	var rightFields []roField
	if todo.Source != "" {
		rightFields = append(rightFields, roField{"Source", todo.Source})
	}
	if todo.Context != "" {
		rightFields = append(rightFields, roField{"Context", displayContext(todo.Context)})
	}
	if todo.WhoWaiting != "" {
		rightFields = append(rightFields, roField{"Who waiting", todo.WhoWaiting})
	}
	if todo.LaunchMode != "" {
		rightFields = append(rightFields, roField{"Mode", todo.LaunchMode})
	}
	rightFields = append(rightFields, roField{"Created", todo.CreatedAt.Format("Jan 2, 2006")})

	var leftLines []string
	for _, f := range editableFields {
		label := s.SectionHeader.Render(f.label + ":")
		val := f.value
		if val == "" {
			val = s.DescMuted.Render("\u2014")
		}

		if p.detailMode == "selectingStatus" && f.idx == 0 {
			var optParts []string
			for i, opt := range statusOptions {
				if i == p.detailStatusCursor {
					optParts = append(optParts, lipgloss.NewStyle().
						Background(s.ColorCyan).
						Foreground(lipgloss.Color("#000000")).
						Bold(true).
						Padding(0, 1).
						Render(opt))
				} else {
					optParts = append(optParts, s.DescMuted.Render(opt))
				}
			}
			leftLines = append(leftLines, fmt.Sprintf("  %-14s %s", label, strings.Join(optParts, "  ")))
		} else if p.detailMode == "selectingPath" && f.idx == 2 {
			filterDisplay := p.detailPathFilter
			if filterDisplay == "" {
				filterDisplay = s.DescMuted.Render("type to filter...")
			} else {
				filterDisplay = lipgloss.NewStyle().Foreground(s.ColorCyan).Render(filterDisplay)
			}
			leftLines = append(leftLines, fmt.Sprintf("  %-14s %s", label, filterDisplay))
		} else if p.detailMode == "editingField" && p.detailSelectedField == f.idx {
			leftLines = append(leftLines, fmt.Sprintf("  %-14s %s", label, p.detailFieldInput.View()))
		} else if (p.detailMode == "viewing" || p.detailMode == "commandInput") && p.detailSelectedField == f.idx {
			leftLines = append(leftLines, fmt.Sprintf("  %-14s [%s]", label, val))
		} else {
			leftLines = append(leftLines, fmt.Sprintf("  %-14s %s", label, val))
		}
	}

	var rightLines []string
	for _, f := range rightFields {
		label := s.SectionHeader.Render(f.label + ":")
		val := f.value
		if val == "" {
			val = s.DescMuted.Render("\u2014")
		}
		rightLines = append(rightLines, fmt.Sprintf("%-14s %s", label, val))
	}

	for len(leftLines) < len(rightLines) {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < len(leftLines) {
		rightLines = append(rightLines, "")
	}

	var fieldRows []string
	for i := range leftLines {
		left := leftLines[i]
		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		leftRendered := left
		leftWidth := lipgloss.Width(leftRendered)
		if leftWidth < colWidth {
			leftRendered += strings.Repeat(" ", colWidth-leftWidth)
		}
		fieldRows = append(fieldRows, leftRendered+"  "+right)
	}
	return strings.Join(fieldRows, "\n")
}

// buildPathPicker renders the path picker section.
func (p *Plugin) buildPathPicker(s *ccStyles, innerWidth int) string {
	filteredPaths := p.filteredPaths()
	if p.detailMode == "selectingPath" && len(filteredPaths) > 0 {
		maxVisible := 8
		startIdx := 0
		if p.detailPathCursor >= maxVisible {
			startIdx = p.detailPathCursor - maxVisible + 1
		}
		endIdx := startIdx + maxVisible
		if endIdx > len(filteredPaths) {
			endIdx = len(filteredPaths)
		}

		var pathLines []string
		for i := startIdx; i < endIdx; i++ {
			path := filteredPaths[i]
			displayPath := path
			if len(displayPath) > innerWidth-8 {
				displayPath = "..." + displayPath[len(displayPath)-(innerWidth-11):]
			}
			if i == p.detailPathCursor {
				pathLines = append(pathLines, lipgloss.NewStyle().
					Background(s.ColorCyan).
					Foreground(lipgloss.Color("#000000")).
					Bold(true).
					Padding(0, 1).
					Render(displayPath))
			} else {
				pathLines = append(pathLines, "  "+s.DescMuted.Render(displayPath))
			}
		}

		if startIdx > 0 {
			pathLines = append([]string{s.CalendarTime.Render(fmt.Sprintf("  \u25b2 %d more", startIdx))}, pathLines...)
		}
		if endIdx < len(filteredPaths) {
			pathLines = append(pathLines, s.CalendarTime.Render(fmt.Sprintf("  \u25bc %d more", len(filteredPaths)-endIdx)))
		}

		pickerHint := s.Hint.Render("  j/k navigate \u00b7 type to filter \u00b7 enter select \u00b7 esc cancel")
		pathLines = append(pathLines, pickerHint)

		return "\n" + lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(s.ColorCyan).
			Width(innerWidth - 4).
			Padding(0, 1).
			Render(strings.Join(pathLines, "\n"))
	} else if p.detailMode == "selectingPath" && len(filteredPaths) == 0 {
		return "\n  " + s.DescMuted.Render("No paths match filter")
	}
	return ""
}

// buildSourcesSection renders the Sources section for synthesis todos.
func (p *Plugin) buildSourcesSection(s *ccStyles, todo db.Todo, innerWidth int) string {
	if todo.Source != "merge" || p.cc == nil {
		return ""
	}
	origIDs := db.DBGetOriginalIDs(p.cc.Merges, todo.ID)
	if len(origIDs) == 0 {
		return ""
	}

	header := s.SectionHeader.Render("  SOURCES")
	var lines []string
	lines = append(lines, "", header, "")

	for i, oid := range origIDs {
		orig := p.cc.FindTodo(oid)
		var display string
		if orig != nil {
			titleStr := flattenTitle(orig.Title)
			if len(titleStr) > innerWidth-20 && innerWidth > 20 {
				titleStr = titleStr[:innerWidth-23] + "..."
			}
			display = fmt.Sprintf("#%d — %s (%s)", orig.DisplayID, titleStr, orig.Source)
		} else {
			display = fmt.Sprintf("%s (not found)", oid)
		}

		if i == p.mergeSourceCursor {
			cursor := lipgloss.NewStyle().Foreground(s.ColorCyan).Bold(true).Render("> ")
			entry := lipgloss.NewStyle().Foreground(s.ColorWhite).Bold(true).Render(display)
			lines = append(lines, "  "+cursor+entry)
		} else {
			entry := s.DescMuted.Render(display)
			lines = append(lines, "    "+entry)
		}
	}

	lines = append(lines, "")
	lines = append(lines, "  "+s.Hint.Render("[/] select source \u00b7 U unmerge selected"))

	return strings.Join(lines, "\n")
}

// buildDetailHints returns the hint string for the current detail mode.
func (p *Plugin) buildDetailHints(s *ccStyles, todo db.Todo, hasActiveSession bool) string {
	switch p.detailMode {
	case "viewing":
		baseHints := "j/k navigate \u00b7 f focus \u00b7 s star \u00b7 S schedule \u00b7 x done \u00b7 X remove \u00b7 tab cycle \u00b7 enter edit \u00b7 o launch"
		if todo.Source == "merge" && p.cc != nil && len(db.DBGetOriginalIDs(p.cc.Merges, todo.ID)) > 0 {
			baseHints += " \u00b7 U unmerge"
		}
		if todo.SessionID != "" && todo.Status != db.StatusRunning && todo.Status != db.StatusEnqueued {
			baseHints += " \u00b7 r resume"
		}
		if hasActiveSession {
			baseHints += " \u00b7 w watch"
		} else if todo.SessionLogPath != "" {
			baseHints += " \u00b7 w log"
		}
		baseHints += " \u00b7 c command \u00b7 T train \u00b7 ? help \u00b7 esc back"
		return s.Hint.Render(baseHints)
	case "editingField":
		return s.Hint.Render("enter confirm \u00b7 esc cancel")
	case "selectingStatus":
		return s.Hint.Render("\u2190/\u2192 select \u00b7 enter confirm \u00b7 esc cancel")
	case "selectingPath":
		return s.Hint.Render("j/k navigate \u00b7 type to filter \u00b7 enter select \u00b7 esc cancel")
	case "commandInput":
		return s.Hint.Render("enter submit to AI \u00b7 esc cancel")
	case "trainingInput":
		return s.Hint.Render("enter submit training \u00b7 esc cancel")
	}
	return ""
}
