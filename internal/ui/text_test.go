package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderMarkdown_HeadingStripsPrefix(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("## What was done", heading, body, code)
	if strings.Contains(result, "## ") {
		t.Errorf("expected ## prefix to be stripped, got: %q", result)
	}
	if !strings.Contains(result, "What was done") {
		t.Errorf("expected heading text to be present, got: %q", result)
	}
}

func TestRenderMarkdown_BulletRendersWithDot(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("- First item\n- Second item", heading, body, code)
	if strings.Contains(result, "\n- ") || strings.HasPrefix(result, "- ") {
		t.Errorf("expected raw '- ' prefix to be replaced, got: %q", result)
	}
	if !strings.Contains(result, "\u2022") {
		t.Errorf("expected bullet character, got: %q", result)
	}
	if !strings.Contains(result, "First item") {
		t.Errorf("expected bullet text to be present, got: %q", result)
	}
	if !strings.Contains(result, "Second item") {
		t.Errorf("expected second bullet text to be present, got: %q", result)
	}
}

func TestRenderMarkdown_InlineCodeStripsBackticks(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("Used `slack_send_message` tool", heading, body, code)
	if strings.Contains(result, "`") {
		t.Errorf("expected backticks to be stripped, got: %q", result)
	}
	if !strings.Contains(result, "slack_send_message") {
		t.Errorf("expected code text to be present, got: %q", result)
	}
}

func TestRenderMarkdown_EmptyLinesPreserved(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("line one\n\nline two", heading, body, code)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines (with empty middle), got %d: %q", len(lines), result)
	}
	if lines[1] != "" {
		t.Errorf("expected empty middle line, got: %q", lines[1])
	}
}

func TestRenderMarkdown_PlainTextPassesThrough(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("Just plain text here", heading, body, code)
	if !strings.Contains(result, "Just plain text here") {
		t.Errorf("expected plain text to pass through, got: %q", result)
	}
}

func TestRenderMarkdown_MixedContent(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	input := "## What was done\n- Searched for user using `slack_search` tool\n- Sent message\n\n## Key decisions\n- Used MCP tools"
	result := RenderMarkdown(input, heading, body, code)

	// ## prefixes stripped
	if strings.Contains(result, "## ") {
		t.Errorf("expected ## prefixes stripped, got: %q", result)
	}
	// - prefixes replaced
	if strings.Contains(result, "\n- ") {
		t.Errorf("expected '- ' prefixes replaced with bullets, got: %q", result)
	}
	// backticks stripped
	if strings.Contains(result, "`") {
		t.Errorf("expected backticks stripped, got: %q", result)
	}
	// Content present
	if !strings.Contains(result, "What was done") {
		t.Errorf("expected heading text present, got: %q", result)
	}
	if !strings.Contains(result, "slack_search") {
		t.Errorf("expected code text present, got: %q", result)
	}
	// Bullets present
	if !strings.Contains(result, "\u2022") {
		t.Errorf("expected bullet characters, got: %q", result)
	}
}

func TestRenderMarkdown_UnmatchedBacktickPreserved(t *testing.T) {
	heading := lipgloss.NewStyle()
	body := lipgloss.NewStyle()
	code := lipgloss.NewStyle()

	result := RenderMarkdown("has an unmatched ` backtick", heading, body, code)
	// The unmatched backtick should be preserved (rendered as part of body text)
	if !strings.Contains(result, "`") {
		t.Errorf("expected unmatched backtick to be preserved, got: %q", result)
	}
}
