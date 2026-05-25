package config

// Palette defines the color scheme for the TUI.
type Palette struct {
	Fg         string
	Highlight  string
	SelectedBg string
	Pointer    string
	Muted      string
	Cyan       string
	Yellow     string
	White      string
	Purple     string
	Green      string
	GradStart  string
	GradMid    string
	GradEnd    string
	BgDark     string
}

var palettes = map[string]Palette{
	"aurora": {
		Fg: "#c0caf5", Highlight: "#7aa2f7", SelectedBg: "#283457",
		Pointer: "#7dcfff", Muted: "#8890a8", Cyan: "#7dcfff",
		Yellow: "#e0af68", White: "#ffffff", Purple: "#bb9af7",
		Green: "#9ece6a", GradStart: "#7dcfff", GradMid: "#7aa2f7",
		GradEnd: "#bb9af7", BgDark: "#1a1b26",
	},
	"ocean": {
		Fg: "#b8c9e3", Highlight: "#5ba0d0", SelectedBg: "#1a3a4a",
		Pointer: "#4ec9b0", Muted: "#6a8a9a", Cyan: "#4ec9b0",
		Yellow: "#d4a656", White: "#e8eef5", Purple: "#8a7dc9",
		Green: "#4ec9b0", GradStart: "#4ec9b0", GradMid: "#5ba0d0",
		GradEnd: "#8a7dc9", BgDark: "#0d1f2d",
	},
	"ember": {
		Fg: "#e8d5c0", Highlight: "#e8875a", SelectedBg: "#3a2518",
		Pointer: "#f0a050", Muted: "#a08070", Cyan: "#f0a050",
		Yellow: "#f0c050", White: "#f5efe8", Purple: "#c97d8a",
		Green: "#a0c050", GradStart: "#f0a050", GradMid: "#e8875a",
		GradEnd: "#c97d8a", BgDark: "#1a1210",
	},
	"neon": {
		Fg: "#d0f0c0", Highlight: "#00ff88", SelectedBg: "#0a2a1a",
		Pointer: "#00ffcc", Muted: "#60a080", Cyan: "#00ffcc",
		Yellow: "#ffff00", White: "#ffffff", Purple: "#ff00ff",
		Green: "#00ff88", GradStart: "#00ff88", GradMid: "#00ffcc",
		GradEnd: "#ff00ff", BgDark: "#0a0a0a",
	},
	"mono": {
		Fg: "#c0c0c0", Highlight: "#e0e0e0", SelectedBg: "#303030",
		Pointer: "#ffffff", Muted: "#808080", Cyan: "#b0b0b0",
		Yellow: "#d0d0d0", White: "#ffffff", Purple: "#a0a0a0",
		Green: "#c0c0c0", GradStart: "#a0a0a0", GradMid: "#c0c0c0",
		GradEnd: "#e0e0e0", BgDark: "#1a1a1a",
	},
	"light": {
		Fg: "#1a1410", Highlight: "#b94d10", SelectedBg: "#f0e0c8",
		Pointer: "#c95a10", Muted: "#807070", Cyan: "#0a6080",
		Yellow: "#a07010", White: "#000000", Purple: "#6a3a70",
		Green: "#506a20", GradStart: "#c95a10", GradMid: "#a04060",
		GradEnd: "#6a3a70", BgDark: "#f8f0e8",
	},
}

// GetPalette returns the named palette. If the name is "custom" and custom
// colors are provided, a palette is built from those colors. Falls back to
// aurora for unknown names.
func GetPalette(name string, custom *CustomColors) Palette {
	if name == "custom" && custom != nil {
		return Palette{
			Fg: custom.Primary, Highlight: custom.Secondary, SelectedBg: custom.Primary,
			Pointer: custom.Accent, Muted: custom.Secondary, Cyan: custom.Accent,
			Yellow: custom.Accent, White: "#ffffff", Purple: custom.Secondary,
			Green: custom.Accent, GradStart: custom.Primary, GradMid: custom.Secondary,
			GradEnd: custom.Accent, BgDark: "#000000",
		}
	}
	if p, ok := palettes[name]; ok {
		return p
	}
	return palettes["aurora"]
}

// PaletteNames returns the names of all built-in palettes.
func PaletteNames() []string {
	return []string{"aurora", "ocean", "ember", "neon", "mono", "light"}
}
