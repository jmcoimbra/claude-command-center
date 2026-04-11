package ui

import (
	"github.com/anutron/claude-command-center/internal/config"
	"github.com/charmbracelet/lipgloss"
)

// ContentMaxWidth is the maximum width for content panels.
const ContentMaxWidth = 144

// Styles holds all lipgloss styles derived from a palette.
type Styles struct {
	// Colors
	ColorFg         lipgloss.Color
	ColorHighlight  lipgloss.Color
	ColorSelectedBg lipgloss.Color
	ColorPointer    lipgloss.Color
	ColorMuted      lipgloss.Color
	ColorCyan       lipgloss.Color
	ColorYellow     lipgloss.Color
	ColorWhite      lipgloss.Color
	ColorPurple     lipgloss.Color
	ColorGreen      lipgloss.Color

	// Styles
	Banner       lipgloss.Style
	Subtitle     lipgloss.Style
	ActiveTab    lipgloss.Style
	InactiveTab  lipgloss.Style
	Hint         lipgloss.Style
	SelectedItem lipgloss.Style
	NormalItem   lipgloss.Style
	TitleBoldW   lipgloss.Style
	TitleBoldC   lipgloss.Style
	DescMuted    lipgloss.Style
	BranchYellow lipgloss.Style
	Pointer      lipgloss.Style

	// Command center styles
	SectionHeader    lipgloss.Style
	CalendarTime     lipgloss.Style
	CalendarFree     lipgloss.Style
	CalendarPersonal lipgloss.Style
	CalendarFamily   lipgloss.Style
	DueOverdue       lipgloss.Style
	DueSoon          lipgloss.Style
	DueLater         lipgloss.Style
	Suggestion       lipgloss.Style
	PanelBorder      lipgloss.Style
	RefreshInfo      lipgloss.Style

	// Calendar past-event style
	CalendarPast lipgloss.Style
}

// NewStyles creates a Styles from a palette.
func NewStyles(p config.Palette) Styles {
	colorFg := lipgloss.Color(p.Fg)
	colorHighlight := lipgloss.Color(p.Highlight)
	colorSelectedBg := lipgloss.Color(p.SelectedBg)
	colorPointer := lipgloss.Color(p.Pointer)
	colorMuted := lipgloss.Color(p.Muted)
	colorCyan := lipgloss.Color(p.Cyan)
	colorYellow := lipgloss.Color(p.Yellow)
	colorWhite := lipgloss.Color(p.White)
	colorPurple := lipgloss.Color(p.Purple)
	colorGreen := lipgloss.Color(p.Green)

	_ = colorHighlight // used indirectly

	return Styles{
		ColorFg:         colorFg,
		ColorHighlight:  colorHighlight,
		ColorSelectedBg: colorSelectedBg,
		ColorPointer:    colorPointer,
		ColorMuted:      colorMuted,
		ColorCyan:       colorCyan,
		ColorYellow:     colorYellow,
		ColorWhite:      colorWhite,
		ColorPurple:     colorPurple,
		ColorGreen:      colorGreen,

		Banner:   lipgloss.NewStyle().Foreground(colorCyan),
		Subtitle: lipgloss.NewStyle().Foreground(colorMuted),

		ActiveTab:   lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		InactiveTab: lipgloss.NewStyle().Foreground(colorMuted),

		Hint:         lipgloss.NewStyle().Foreground(colorMuted),
		SelectedItem: lipgloss.NewStyle().Foreground(colorWhite).Background(colorSelectedBg),
		NormalItem:   lipgloss.NewStyle().Foreground(colorFg),
		TitleBoldW:   lipgloss.NewStyle().Foreground(colorWhite).Bold(true),
		TitleBoldC:   lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		DescMuted:    lipgloss.NewStyle().Foreground(colorMuted),
		BranchYellow: lipgloss.NewStyle().Foreground(colorYellow),
		Pointer:      lipgloss.NewStyle().Foreground(colorPointer),

		SectionHeader: lipgloss.NewStyle().Foreground(colorCyan).Bold(true),
		CalendarTime:  lipgloss.NewStyle().Foreground(colorMuted),
		CalendarFree:  lipgloss.NewStyle().Foreground(colorMuted).Faint(true),
		CalendarPersonal: lipgloss.NewStyle().Foreground(colorGreen),
		CalendarFamily:   lipgloss.NewStyle().Foreground(colorPurple),
		DueOverdue: lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")),
		DueSoon:    lipgloss.NewStyle().Foreground(colorYellow),
		DueLater:   lipgloss.NewStyle().Foreground(colorMuted),
		Suggestion: lipgloss.NewStyle().Foreground(colorPurple).Italic(true),
		PanelBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3b4261")).
			Padding(0, 1),
		RefreshInfo: lipgloss.NewStyle().Foreground(colorMuted),

		CalendarPast: lipgloss.NewStyle().Foreground(colorMuted).Faint(true),

	}
}

// DueStyle returns the appropriate style for a due urgency level.
func (s *Styles) DueStyle(urgency string) lipgloss.Style {
	switch urgency {
	case "overdue":
		return s.DueOverdue
	case "soon":
		return s.DueSoon
	case "later":
		return s.DueLater
	default:
		return s.DueLater
	}
}
