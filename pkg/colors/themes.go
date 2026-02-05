package colors

// Theme represents a complete color theme for the sidebar
type Theme struct {
	Name        string
	Description string
	Dark        bool // Is this a dark theme?

	// Sidebar
	SidebarBg    string
	HeaderFg     string
	ActiveFg     string
	InactiveFg   string
	DisclosureFg string
	TreeFg       string
	TreeBg       string

	// Pane Headers
	PaneActiveBg   string
	PaneActiveFg   string
	PaneInactiveBg string
	PaneInactiveFg string
	CommandFg      string
	PaneButtonFg   string
	BorderFg       string
	HandleColor    string
	TerminalBg     string

	// Widgets
	DividerFg string
	WidgetFg  string

	// Prompt
	PromptFg string
	PromptBg string

	// Group defaults (for tabs without custom colors)
	DefaultGroupBg     string
	DefaultGroupFg     string
	DefaultActiveBg    string
	DefaultActiveFg    string
	DefaultIndicatorBg string

	// Sidebar Buttons
	ButtonBg            string
	ButtonFg            string
	ButtonPrimaryBg     string
	ButtonPrimaryFg     string
	ButtonSecondaryBg   string
	ButtonSecondaryFg   string
	ButtonDestructiveBg string
	ButtonDestructiveFg string
}

// Built-in themes
var Themes = map[string]Theme{
	"rose-pine-dawn": {
		Name:        "Rose Pine Dawn",
		Description: "Soft light theme with warm colors",
		Dark:        false,

		// Global
		SidebarBg:  "#faf4ed",
		TerminalBg: "#faf4ed",
		BorderFg:   "#dfdad9",
		PromptFg:   "#575279",
		PromptBg:   "#f2e9e1",

		// Sidebar
		HeaderFg:     "#575279",
		ActiveFg:     "#575279",
		InactiveFg:   "#9893a5",
		DisclosureFg: "#797593",
		TreeFg:       "#9893a5",
		TreeBg:       "",

		// Pane Headers
		PaneActiveBg:   "#f2e9e1",
		PaneActiveFg:   "#575279",
		PaneInactiveBg: "#fffaf3",
		PaneInactiveFg: "#9893a5",
		CommandFg:      "#797593",
		PaneButtonFg:   "#575279",
		HandleColor:    "#9893a5",

		// Widgets
		DividerFg: "#dfdad9",
		WidgetFg:  "#797593",

		// Sidebar Tab Defaults
		DefaultGroupBg:     "#fffaf3", // Surface
		DefaultGroupFg:     "#9893a5", // Muted
		DefaultActiveBg:    "#f2e9e1", // Overlay
		DefaultActiveFg:    "#575279", // Text
		DefaultIndicatorBg: "#d7827e", // Rose

		// Sidebar Buttons
		ButtonBg:            "#f2e9e1", // Overlay
		ButtonFg:            "#575279", // Text
		ButtonPrimaryBg:     "#286983", // Pine
		ButtonPrimaryFg:     "#faf4ed",
		ButtonSecondaryBg:   "#907aa9", // Iris
		ButtonSecondaryFg:   "#faf4ed",
		ButtonDestructiveBg: "#b4637a", // Love
		ButtonDestructiveFg: "#faf4ed",
	},

	"rose-pine": {
		Name:        "Rose Pine",
		Description: "Elegant dark theme with muted colors",
		Dark:        true,

		SidebarBg:    "#191724",
		HeaderFg:     "#e0def4",
		ActiveFg:     "#e0def4",
		InactiveFg:   "#6e6a86",
		DisclosureFg: "#908caa",
		TreeFg:       "#6e6a86",
		TreeBg:       "",

		PaneActiveBg:   "#26233a",
		PaneActiveFg:   "#e0def4",
		PaneInactiveBg: "#1f1d2e",
		PaneInactiveFg: "#6e6a86",
		CommandFg:      "#908caa",
		PaneButtonFg:   "#6e6a86",
		BorderFg:       "#403d52",
		HandleColor:    "#6e6a86",
		TerminalBg:     "#191724",

		DividerFg: "#403d52",
		WidgetFg:  "#908caa",

		PromptFg: "#e0def4",
		PromptBg: "#26233a",

		DefaultGroupBg:     "#9ccfd8",
		DefaultGroupFg:     "#191724",
		DefaultActiveBg:    "#31748f",
		DefaultActiveFg:    "#e0def4",
		DefaultIndicatorBg: "#ebbcba",
	},

	"rose-pine-moon": {
		Name:        "Rose Pine Moon",
		Description: "Dark theme with a hint of warmth",
		Dark:        true,

		SidebarBg:    "#232136",
		HeaderFg:     "#e0def4",
		ActiveFg:     "#e0def4",
		InactiveFg:   "#6e6a86",
		DisclosureFg: "#908caa",
		TreeFg:       "#6e6a86",
		TreeBg:       "",

		PaneActiveBg:   "#393552",
		PaneActiveFg:   "#e0def4",
		PaneInactiveBg: "#2a273f",
		PaneInactiveFg: "#6e6a86",
		CommandFg:      "#908caa",
		PaneButtonFg:   "#6e6a86",
		BorderFg:       "#44415a",
		HandleColor:    "#6e6a86",
		TerminalBg:     "#232136",

		DividerFg: "#44415a",
		WidgetFg:  "#908caa",

		PromptFg: "#e0def4",
		PromptBg: "#393552",

		DefaultGroupBg:     "#9ccfd8",
		DefaultGroupFg:     "#232136",
		DefaultActiveBg:    "#3e8fb0",
		DefaultActiveFg:    "#e0def4",
		DefaultIndicatorBg: "#ea9a97",
	},

	"catppuccin-mocha": {
		Name:        "Catppuccin Mocha",
		Description: "Soothing dark theme",
		Dark:        true,

		SidebarBg:    "#1e1e2e",
		HeaderFg:     "#cdd6f4",
		ActiveFg:     "#cdd6f4",
		InactiveFg:   "#6c7086",
		DisclosureFg: "#9399b2",
		TreeFg:       "#6c7086",

		PaneActiveBg:   "#313244",
		PaneActiveFg:   "#cdd6f4",
		PaneInactiveBg: "#181825",
		PaneInactiveFg: "#6c7086",
		CommandFg:      "#9399b2",
		PaneButtonFg:   "#6c7086",
		BorderFg:       "#45475a",
		HandleColor:    "#6c7086",
		TerminalBg:     "#1e1e2e",

		DividerFg: "#45475a",
		WidgetFg:  "#9399b2",

		PromptFg: "#cdd6f4",
		PromptBg: "#313244",

		DefaultGroupBg:     "#89b4fa",
		DefaultGroupFg:     "#1e1e2e",
		DefaultActiveBg:    "#74c7ec",
		DefaultActiveFg:    "#1e1e2e",
		DefaultIndicatorBg: "#f38ba8",
	},

	"catppuccin-latte": {
		Name:        "Catppuccin Latte",
		Description: "Light pastel theme",
		Dark:        false,

		SidebarBg:    "#eff1f5",
		HeaderFg:     "#4c4f69",
		ActiveFg:     "#4c4f69",
		InactiveFg:   "#9ca0b0",
		DisclosureFg: "#7c7f93",
		TreeFg:       "#9ca0b0",

		PaneActiveBg:   "#e6e9ef",
		PaneActiveFg:   "#4c4f69",
		PaneInactiveBg: "#dce0e8",
		PaneInactiveFg: "#9ca0b0",
		CommandFg:      "#7c7f93",
		PaneButtonFg:   "#9ca0b0",
		BorderFg:       "#bcc0cc",
		HandleColor:    "#9ca0b0",
		TerminalBg:     "#eff1f5",

		DividerFg: "#bcc0cc",
		WidgetFg:  "#7c7f93",

		PromptFg: "#4c4f69",
		PromptBg: "#e6e9ef",

		DefaultGroupBg:     "#1e66f5",
		DefaultGroupFg:     "#eff1f5",
		DefaultActiveBg:    "#04a5e5",
		DefaultActiveFg:    "#eff1f5",
		DefaultIndicatorBg: "#d20f39",
	},

	"dracula": {
		Name:        "Dracula",
		Description: "Dark theme with vibrant colors",
		Dark:        true,

		SidebarBg:    "#282a36",
		HeaderFg:     "#f8f8f2",
		ActiveFg:     "#f8f8f2",
		InactiveFg:   "#6272a4",
		DisclosureFg: "#bd93f9",
		TreeFg:       "#6272a4",

		PaneActiveBg:   "#44475a",
		PaneActiveFg:   "#f8f8f2",
		PaneInactiveBg: "#21222c",
		PaneInactiveFg: "#6272a4",
		CommandFg:      "#6272a4",
		PaneButtonFg:   "#6272a4",
		BorderFg:       "#44475a",
		HandleColor:    "#6272a4",
		TerminalBg:     "#282a36",

		DividerFg: "#44475a",
		WidgetFg:  "#6272a4",

		PromptFg: "#f8f8f2",
		PromptBg: "#44475a",

		DefaultGroupBg:     "#bd93f9",
		DefaultGroupFg:     "#282a36",
		DefaultActiveBg:    "#ff79c6",
		DefaultActiveFg:    "#282a36",
		DefaultIndicatorBg: "#50fa7b",
	},

	"nord": {
		Name:        "Nord",
		Description: "Arctic, north-bluish color palette",
		Dark:        true,

		SidebarBg:    "#2e3440",
		HeaderFg:     "#eceff4",
		ActiveFg:     "#eceff4",
		InactiveFg:   "#4c566a",
		DisclosureFg: "#d8dee9",
		TreeFg:       "#4c566a",

		PaneActiveBg:   "#3b4252",
		PaneActiveFg:   "#eceff4",
		PaneInactiveBg: "#2e3440",
		PaneInactiveFg: "#4c566a",
		CommandFg:      "#d8dee9",
		PaneButtonFg:   "#4c566a",
		BorderFg:       "#4c566a",
		HandleColor:    "#4c566a",
		TerminalBg:     "#2e3440",

		DividerFg: "#4c566a",
		WidgetFg:  "#d8dee9",

		PromptFg: "#eceff4",
		PromptBg: "#3b4252",

		DefaultGroupBg:     "#81a1c1",
		DefaultGroupFg:     "#2e3440",
		DefaultActiveBg:    "#88c0d0",
		DefaultActiveFg:    "#2e3440",
		DefaultIndicatorBg: "#bf616a",
	},

	"solarized-dark": {
		Name:        "Solarized Dark",
		Description: "Precision colors for machines and people",
		Dark:        true,

		SidebarBg:    "#002b36",
		HeaderFg:     "#839496",
		ActiveFg:     "#93a1a1",
		InactiveFg:   "#586e75",
		DisclosureFg: "#657b83",
		TreeFg:       "#586e75",

		PaneActiveBg:   "#073642",
		PaneActiveFg:   "#93a1a1",
		PaneInactiveBg: "#002b36",
		PaneInactiveFg: "#586e75",
		CommandFg:      "#657b83",
		PaneButtonFg:   "#586e75",
		BorderFg:       "#073642",
		HandleColor:    "#586e75",
		TerminalBg:     "#002b36",

		DividerFg: "#073642",
		WidgetFg:  "#657b83",

		PromptFg: "#93a1a1",
		PromptBg: "#073642",

		DefaultGroupBg:     "#268bd2",
		DefaultGroupFg:     "#002b36",
		DefaultActiveBg:    "#2aa198",
		DefaultActiveFg:    "#002b36",
		DefaultIndicatorBg: "#cb4b16",
	},

	"solarized-light": {
		Name:        "Solarized Light",
		Description: "Light variant of Solarized",
		Dark:        false,

		SidebarBg:    "#fdf6e3",
		HeaderFg:     "#657b83",
		ActiveFg:     "#586e75",
		InactiveFg:   "#93a1a1",
		DisclosureFg: "#839496",
		TreeFg:       "#93a1a1",

		PaneActiveBg:   "#eee8d5",
		PaneActiveFg:   "#586e75",
		PaneInactiveBg: "#fdf6e3",
		PaneInactiveFg: "#93a1a1",
		CommandFg:      "#839496",
		PaneButtonFg:   "#93a1a1",
		BorderFg:       "#eee8d5",
		HandleColor:    "#93a1a1",
		TerminalBg:     "#fdf6e3",

		DividerFg: "#eee8d5",
		WidgetFg:  "#839496",

		PromptFg: "#586e75",
		PromptBg: "#eee8d5",

		DefaultGroupBg:     "#268bd2",
		DefaultGroupFg:     "#fdf6e3",
		DefaultActiveBg:    "#2aa198",
		DefaultActiveFg:    "#fdf6e3",
		DefaultIndicatorBg: "#cb4b16",
	},

	"gruvbox-dark": {
		Name:        "Gruvbox Dark",
		Description: "Retro groove color scheme",
		Dark:        true,

		SidebarBg:    "#282828",
		HeaderFg:     "#ebdbb2",
		ActiveFg:     "#ebdbb2",
		InactiveFg:   "#928374",
		DisclosureFg: "#a89984",
		TreeFg:       "#928374",

		PaneActiveBg:   "#3c3836",
		PaneActiveFg:   "#ebdbb2",
		PaneInactiveBg: "#282828",
		PaneInactiveFg: "#928374",
		CommandFg:      "#a89984",
		PaneButtonFg:   "#928374",
		BorderFg:       "#504945",
		HandleColor:    "#928374",
		TerminalBg:     "#282828",

		DividerFg: "#504945",
		WidgetFg:  "#a89984",

		PromptFg: "#ebdbb2",
		PromptBg: "#3c3836",

		DefaultGroupBg:     "#458588",
		DefaultGroupFg:     "#282828",
		DefaultActiveBg:    "#83a598",
		DefaultActiveFg:    "#282828",
		DefaultIndicatorBg: "#fb4934",
	},

	"gruvbox-light": {
		Name:        "Gruvbox Light",
		Description: "Light variant of Gruvbox",
		Dark:        false,

		SidebarBg:    "#fbf1c7",
		HeaderFg:     "#3c3836",
		ActiveFg:     "#3c3836",
		InactiveFg:   "#928374",
		DisclosureFg: "#7c6f64",
		TreeFg:       "#928374",

		PaneActiveBg:   "#ebdbb2",
		PaneActiveFg:   "#3c3836",
		PaneInactiveBg: "#fbf1c7",
		PaneInactiveFg: "#928374",
		CommandFg:      "#7c6f64",
		PaneButtonFg:   "#928374",
		BorderFg:       "#d5c4a1",
		HandleColor:    "#928374",
		TerminalBg:     "#fbf1c7",

		DividerFg: "#d5c4a1",
		WidgetFg:  "#7c6f64",

		PromptFg: "#3c3836",
		PromptBg: "#ebdbb2",

		DefaultGroupBg:     "#458588",
		DefaultGroupFg:     "#fbf1c7",
		DefaultActiveBg:    "#689d6a",
		DefaultActiveFg:    "#fbf1c7",
		DefaultIndicatorBg: "#cc241d",
	},

	"tokyo-night": {
		Name:        "Tokyo Night",
		Description: "A dark theme inspired by Tokyo at night",
		Dark:        true,

		SidebarBg:    "#1a1b26",
		HeaderFg:     "#c0caf5",
		ActiveFg:     "#c0caf5",
		InactiveFg:   "#565f89",
		DisclosureFg: "#9aa5ce",
		TreeFg:       "#565f89",

		PaneActiveBg:   "#24283b",
		PaneActiveFg:   "#c0caf5",
		PaneInactiveBg: "#1a1b26",
		PaneInactiveFg: "#565f89",
		CommandFg:      "#9aa5ce",
		PaneButtonFg:   "#565f89",
		BorderFg:       "#414868",
		HandleColor:    "#565f89",
		TerminalBg:     "#1a1b26",

		DividerFg: "#414868",
		WidgetFg:  "#9aa5ce",

		PromptFg: "#c0caf5",
		PromptBg: "#24283b",

		DefaultGroupBg:     "#7aa2f7",
		DefaultGroupFg:     "#1a1b26",
		DefaultActiveBg:    "#7dcfff",
		DefaultActiveFg:    "#1a1b26",
		DefaultIndicatorBg: "#f7768e",
	},

	"one-dark": {
		Name:        "One Dark",
		Description: "Atom's iconic dark theme",
		Dark:        true,

		SidebarBg:    "#282c34",
		HeaderFg:     "#abb2bf",
		ActiveFg:     "#abb2bf",
		InactiveFg:   "#5c6370",
		DisclosureFg: "#828997",
		TreeFg:       "#5c6370",

		PaneActiveBg:   "#3e4451",
		PaneActiveFg:   "#abb2bf",
		PaneInactiveBg: "#282c34",
		PaneInactiveFg: "#5c6370",
		CommandFg:      "#828997",
		PaneButtonFg:   "#5c6370",
		BorderFg:       "#3e4451",
		HandleColor:    "#5c6370",
		TerminalBg:     "#282c34",

		DividerFg: "#3e4451",
		WidgetFg:  "#828997",

		PromptFg: "#abb2bf",
		PromptBg: "#3e4451",

		DefaultGroupBg:     "#61afef",
		DefaultGroupFg:     "#282c34",
		DefaultActiveBg:    "#56b6c2",
		DefaultActiveFg:    "#282c34",
		DefaultIndicatorBg: "#e06c75",
	},

	// Default theme that respects terminal colors (transparent)
	"default": {
		Name:        "Default",
		Description: "Uses terminal default colors (transparent)",
		Dark:        true, // Assumption, mostly for contrast calculation

		SidebarBg:    "", // Transparent
		HeaderFg:     "", // Default
		ActiveFg:     "", // Default
		InactiveFg:   "", // Default
		DisclosureFg: "", // Default
		TreeFg:       "", // Default

		PaneActiveBg:   "", // Default
		PaneActiveFg:   "", // Default
		PaneInactiveBg: "", // Default
		PaneInactiveFg: "", // Default
		CommandFg:      "", // Default
		PaneButtonFg:   "", // Default
		BorderFg:       "", // Default
		HandleColor:    "", // Default
		TerminalBg:     "", // Default

		DividerFg: "", // Default
		WidgetFg:  "", // Default

		PromptFg: "", // Default
		PromptBg: "", // Default

		DefaultGroupBg:     "", // Default
		DefaultGroupFg:     "", // Default
		DefaultActiveBg:    "", // Default
		DefaultActiveFg:    "", // Default
		DefaultIndicatorBg: "", // Default
	},

	// Default dark theme (backward compatible)
	"dark": {
		Name:        "Dark",
		Description: "Default dark theme",
		Dark:        true,

		SidebarBg:    "#1a1a2e",
		HeaderFg:     "#ffffff",
		ActiveFg:     "#ffffff",
		InactiveFg:   "#888888",
		DisclosureFg: "#e8e8e8",
		TreeFg:       "#888888",

		PaneActiveBg:   "#3498db",
		PaneActiveFg:   "#ffffff",
		PaneInactiveBg: "#333333",
		PaneInactiveFg: "#cccccc",
		CommandFg:      "#aaaaaa",
		PaneButtonFg:   "#888888",
		BorderFg:       "#444444",
		HandleColor:    "#666666",
		TerminalBg:     "#1e1e1e",

		DividerFg: "#444444",
		WidgetFg:  "#aaaaaa",

		PromptFg: "#000000",
		PromptBg: "#f0f0f0",

		DefaultGroupBg:     "#3498db",
		DefaultGroupFg:     "#ffffff",
		DefaultActiveBg:    "#2980b9",
		DefaultActiveFg:    "#ffffff",
		DefaultIndicatorBg: "#26c6da",
	},
}

// GetTheme returns a theme by name, or the default dark theme if not found
func GetTheme(name string) Theme {
	if theme, ok := Themes[name]; ok {
		return theme
	}
	return Themes["dark"]
}

// ListThemes returns all available theme names
func ListThemes() []string {
	names := make([]string, 0, len(Themes))
	for name := range Themes {
		names = append(names, name)
	}
	return names
}
