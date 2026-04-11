package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// WrapText wraps text at word boundaries to fit within maxWidth columns.
// It preserves existing newlines and handles empty paragraphs.
func WrapText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return text
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			if len(current)+1+len(word) > maxWidth {
				lines = append(lines, current)
				current = word
			} else {
				current += " " + word
			}
		}
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}

// TruncateToWidth truncates a string to maxWidth runes, appending "~" if truncated.
// Returns empty string if maxWidth <= 0.
func TruncateToWidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	return string(runes[:maxWidth-1]) + "~"
}

// FlattenTitle collapses newlines and multiple spaces in a string to single spaces.
func FlattenTitle(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// RenderMarkdown performs simple line-by-line markdown rendering for TUI display.
// It handles:
//   - ## headings: rendered with headingStyle (bold cyan section headers)
//   - - bullets: rendered with bullet character prefix, text in bodyStyle
//   - `backtick` inline code: rendered with codeStyle (dimmed)
//   - Plain text: rendered with bodyStyle
//
// The text should already be word-wrapped before calling this function.
func RenderMarkdown(text string, headingStyle, bodyStyle, codeStyle lipgloss.Style) string {
	lines := strings.Split(text, "\n")
	var result []string
	for _, line := range lines {
		if line == "" {
			result = append(result, "")
			continue
		}
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimPrefix(line, "## ")
			result = append(result, headingStyle.Render(heading))
			continue
		}
		if strings.HasPrefix(line, "- ") {
			body := strings.TrimPrefix(line, "- ")
			body = renderInlineCode(body, bodyStyle, codeStyle)
			result = append(result, "  \u2022 "+body)
			continue
		}
		// Plain text line — render inline code
		rendered := renderInlineCode(line, bodyStyle, codeStyle)
		result = append(result, rendered)
	}
	return strings.Join(result, "\n")
}

// renderInlineCode replaces `backtick` spans with codeStyle-rendered text.
// Non-code text is rendered with bodyStyle.
func renderInlineCode(text string, bodyStyle, codeStyle lipgloss.Style) string {
	var result strings.Builder
	for {
		idx := strings.Index(text, "`")
		if idx == -1 {
			// No more backticks — render remaining text with bodyStyle.
			if text != "" {
				result.WriteString(bodyStyle.Render(text))
			}
			break
		}
		// Render text before the opening backtick.
		if idx > 0 {
			result.WriteString(bodyStyle.Render(text[:idx]))
		}
		rest := text[idx+1:]
		closeIdx := strings.Index(rest, "`")
		if closeIdx == -1 {
			// Unmatched backtick — render remainder with bodyStyle (including the backtick).
			result.WriteString(bodyStyle.Render("`" + rest))
			break
		}
		// Render code span with codeStyle.
		codeContent := rest[:closeIdx]
		result.WriteString(codeStyle.Render(codeContent))
		text = rest[closeIdx+1:]
	}
	return result.String()
}
