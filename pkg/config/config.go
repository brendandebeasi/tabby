package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Position      string        `yaml:"position"`
	Height        int           `yaml:"height"`
	Style         Style         `yaml:"style"`
	Overflow      Overflow      `yaml:"overflow"`
	Groups        []Group       `yaml:"groups"`
	Bindings      Bindings      `yaml:"bindings"`
	Sidebar       Sidebar       `yaml:"sidebar"`
	PaneHeader    PaneHeader    `yaml:"pane_header"`
	Indicators    Indicators    `yaml:"indicators"`
	Prompt        PromptStyle   `yaml:"prompt"`
	Widgets       Widgets       `yaml:"widgets"`
	BusyDetection BusyDetection `yaml:"busy_detection"`
	TerminalTitle TerminalTitle `yaml:"terminal_title"`
}

// BusyDetection configures which pane commands trigger the busy indicator.
// By default, any foreground process that isn't a shell or editor is "busy".
// AI tools have activity-based detection: busy when outputting, input when idle.
type BusyDetection struct {
	ExtraIdle   []string `yaml:"extra_idle"`   // Additional commands to treat as idle (not busy)
	AITools     []string `yaml:"ai_tools"`     // Interactive AI tools (busy when active, input when idle)
	IdleTimeout int      `yaml:"idle_timeout"` // Seconds of no output before AI tool shows as "input needed" (default: 10)
}

type TerminalTitle struct {
	Enabled bool   `yaml:"enabled"`
	Format  string `yaml:"format"`
}

type Widgets struct {
	Clock   ClockWidget   `yaml:"clock"`
	Pet     PetWidget     `yaml:"pet"`
	Git     GitWidget     `yaml:"git"`
	Session SessionWidget `yaml:"session"`
	Stats   StatsWidget   `yaml:"stats"`
}

// StatsWidget shows system stats (CPU, memory, battery)
type StatsWidget struct {
	Enabled        bool   `yaml:"enabled"`
	Style          string `yaml:"style"`           // nerd | emoji | ascii | minimal
	ShowCPU        bool   `yaml:"show_cpu"`        // Show CPU usage
	ShowMemory     bool   `yaml:"show_memory"`     // Show memory usage
	ShowBattery    bool   `yaml:"show_battery"`    // Show battery status
	BarStyle       string `yaml:"bar_style"`       // block | braille | dots | ascii
	BarWidth       int    `yaml:"bar_width"`       // Width of progress bars
	UpdateInterval int    `yaml:"update_interval"` // Seconds between updates (default: 2)
	Position       string `yaml:"position"`        // top | bottom
	Pin            bool   `yaml:"pin"`             // Pin to position
	Priority       int    `yaml:"priority"`        // Order among widgets
	Fg             string `yaml:"fg"`              // Text color
	Bg             string `yaml:"bg"`              // Background color
	CPUFg          string `yaml:"cpu_fg"`          // CPU text color
	MemoryFg       string `yaml:"memory_fg"`       // Memory text color
	BatteryFg      string `yaml:"battery_fg"`      // Battery text color
	Divider        string `yaml:"divider"`         // Divider line above widget
	DividerFg      string `yaml:"divider_fg"`      // Divider color
	PaddingTop     int    `yaml:"padding_top"`     // Blank lines above content
	PaddingBot     int    `yaml:"padding_bottom"`  // Blank lines below content
	MarginTop      int    `yaml:"margin_top"`      // Lines above top divider
	MarginBot      int    `yaml:"margin_bottom"`   // Lines below bottom divider
}

// SessionWidget shows tmux session info
type SessionWidget struct {
	Enabled         bool   `yaml:"enabled"`
	Style           string `yaml:"style"`          // nerd | emoji | ascii | minimal
	ShowClients     bool   `yaml:"show_clients"`   // Show connected clients count
	ShowWindowCount bool   `yaml:"show_windows"`   // Show window count
	Position        string `yaml:"position"`       // top | bottom
	Pin             bool   `yaml:"pin"`            // Pin to position
	Priority        int    `yaml:"priority"`       // Order among widgets
	Fg              string `yaml:"fg"`             // Text color
	Bg              string `yaml:"bg"`             // Background color
	SessionFg       string `yaml:"session_fg"`     // Session name color
	Divider         string `yaml:"divider"`        // Divider line above widget
	DividerFg       string `yaml:"divider_fg"`     // Divider color
	PaddingTop      int    `yaml:"padding_top"`    // Blank lines above content
	PaddingBot      int    `yaml:"padding_bottom"` // Blank lines below content
	MarginTop       int    `yaml:"margin_top"`     // Lines above top divider
	MarginBot       int    `yaml:"margin_bottom"`  // Lines below bottom divider
}

// GitWidget shows git repository status
type GitWidget struct {
	Enabled        bool   `yaml:"enabled"`
	Style          string `yaml:"style"`           // nerd | emoji | ascii | minimal
	ShowCounts     bool   `yaml:"show_counts"`     // Show file counts (+3 -2)
	ShowInsertions bool   `yaml:"show_insertions"` // Show line changes
	ShowStash      bool   `yaml:"show_stash"`      // Show stash count
	UpdateInterval int    `yaml:"update_interval"` // Seconds between updates (default: 5)
	Position       string `yaml:"position"`        // top | bottom
	Pin            bool   `yaml:"pin"`             // Pin to position
	Priority       int    `yaml:"priority"`        // Order among widgets
	Fg             string `yaml:"fg"`              // Text color
	Bg             string `yaml:"bg"`              // Background color
	BranchFg       string `yaml:"branch_fg"`       // Branch name color
	CleanFg        string `yaml:"clean_fg"`        // Clean status color
	DirtyFg        string `yaml:"dirty_fg"`        // Dirty status color
	AheadFg        string `yaml:"ahead_fg"`        // Ahead indicator color
	BehindFg       string `yaml:"behind_fg"`       // Behind indicator color
	Divider        string `yaml:"divider"`         // Divider line above widget
	DividerFg      string `yaml:"divider_fg"`      // Divider color
	PaddingTop     int    `yaml:"padding_top"`     // Blank lines above content
	PaddingBot     int    `yaml:"padding_bottom"`  // Blank lines below content
	MarginTop      int    `yaml:"margin_top"`      // Lines above top divider
	MarginBot      int    `yaml:"margin_bottom"`   // Lines below bottom divider
}

// PetWidget configures the virtual pet (cat, dog, etc.)
type PetWidget struct {
	Enabled         bool   `yaml:"enabled"`
	Name            string `yaml:"name"`             // Pet's name (default: "Whiskers")
	Style           string `yaml:"style"`            // emoji | nerd | ascii
	Rows            int    `yaml:"rows"`             // 1 or 2 rows for play area
	Thoughts        bool   `yaml:"thoughts"`         // Enable LLM thoughts
	ThoughtInterval int    `yaml:"thought_interval"` // Seconds between LLM thoughts
	ThoughtSpeed    int    `yaml:"thought_speed"`    // Frames per scroll step (1=fast, 5=slow)
	Position        string `yaml:"position"`         // top | bottom
	Pin             bool   `yaml:"pin"`              // Pin to bottom
	HungerDecay     int    `yaml:"hunger_decay"`     // Seconds between hunger ticks
	PoopChance      int    `yaml:"poop_chance"`      // % chance to poop after eating
	ActionChance    int    `yaml:"action_chance"`    // % chance to do random action when idle (default: 15)
	CanDie          bool   `yaml:"can_die"`          // If true, pet dies when starved; if false, just guilt trips
	TouchButtons    bool   `yaml:"touch_buttons"`    // Show large touch-friendly action buttons
	// LLM settings for thoughts
	LLMProvider string `yaml:"llm_provider"` // openai | anthropic | ollama (default: anthropic)
	LLMModel    string `yaml:"llm_model"`    // Model name (default: claude-3-haiku-20240307)
	LLMAPIKey   string `yaml:"llm_api_key"`  // API key (or use env: ANTHROPIC_API_KEY, OPENAI_API_KEY)
	// Custom icons (override style preset)
	Icons PetIcons `yaml:"icons"` // Custom icons for each element
	// Styling (same as other widgets)
	Fg            string `yaml:"fg"`             // Text color
	Bg            string `yaml:"bg"`             // Background color
	Priority      int    `yaml:"priority"`       // Order when multiple widgets pinned (lower = closer to bottom)
	Divider       string `yaml:"divider"`        // Divider line above widget
	DividerBottom string `yaml:"divider_bottom"` // Divider line below widget
	DividerFg     string `yaml:"divider_fg"`     // Divider color
	PaddingTop    int    `yaml:"padding_top"`    // Blank lines above content
	PaddingBot    int    `yaml:"padding_bottom"` // Blank lines below content
	MarginTop     int    `yaml:"margin_top"`     // Lines above top divider
	MarginBot     int    `yaml:"margin_bottom"`  // Lines below bottom divider
}

// PetIcons allows customizing individual icons in the pet widget
type PetIcons struct {
	// Pet states
	Idle     string `yaml:"idle"`     // Idle cat (default: style-based)
	Walking  string `yaml:"walking"`  // Walking cat
	Jumping  string `yaml:"jumping"`  // Jumping cat
	Playing  string `yaml:"playing"`  // Playing cat
	Eating   string `yaml:"eating"`   // Eating cat
	Sleeping string `yaml:"sleeping"` // Sleeping cat
	Happy    string `yaml:"happy"`    // Happy cat
	Hungry   string `yaml:"hungry"`   // Hungry cat
	// Items
	Yarn string `yaml:"yarn"` // Yarn ball
	Food string `yaml:"food"` // Food item
	Poop string `yaml:"poop"` // Poop
	// UI elements
	Thought    string `yaml:"thought"`     // Thought bubble icon
	Heart      string `yaml:"heart"`       // Pet/love icon
	Life       string `yaml:"life"`        // Life/health icon
	HungerIcon string `yaml:"hunger_icon"` // Hunger stat icon
	HappyIcon  string `yaml:"happy_icon"`  // Happiness stat icon (when happy)
	SadIcon    string `yaml:"sad_icon"`    // Happiness stat icon (when sad)
	Ground     string `yaml:"ground"`      // Ground character
}

type ClockWidget struct {
	Enabled       bool   `yaml:"enabled"`
	Format        string `yaml:"format"`         // Go time format (default: "15:04:05")
	ShowDate      bool   `yaml:"show_date"`      // Show date below time
	DateFmt       string `yaml:"date_format"`    // Date format (default: "Mon Jan 2")
	Fg            string `yaml:"fg"`             // Text color
	Bg            string `yaml:"bg"`             // Background color
	Position      string `yaml:"position"`       // "top" or "bottom" (default: bottom)
	Pin           bool   `yaml:"pin"`            // Pin to bottom of sidebar (scrolls with content if false)
	Priority      int    `yaml:"priority"`       // Order when multiple widgets pinned (lower = closer to bottom)
	Divider       string `yaml:"divider"`        // Divider line above widget
	DividerBottom string `yaml:"divider_bottom"` // Divider line below widget
	DividerFg     string `yaml:"divider_fg"`     // Divider color
	PaddingTop    int    `yaml:"padding_top"`    // Blank lines above content
	PaddingBot    int    `yaml:"padding_bottom"` // Blank lines below content
	MarginTop     int    `yaml:"margin_top"`     // Lines above top divider
	MarginBot     int    `yaml:"margin_bottom"`  // Lines below bottom divider
}

type PromptStyle struct {
	Fg   string `yaml:"fg"`   // Prompt text color (default: #000000)
	Bg   string `yaml:"bg"`   // Prompt background (default: #f0f0f0)
	Bold bool   `yaml:"bold"` // Bold text (default: true)
}

type PaneHeader struct {
	ActiveFg      string  `yaml:"active_fg"`       // Active pane header text (default: #ffffff)
	ActiveBg      string  `yaml:"active_bg"`       // Active pane header bg fallback (default: #3498db)
	InactiveFg    string  `yaml:"inactive_fg"`     // Inactive pane header text (default: #cccccc)
	InactiveBg    string  `yaml:"inactive_bg"`     // Inactive pane header bg fallback (default: #333333)
	CommandFg     string  `yaml:"command_fg"`      // Dimmed pane text color (default: #aaaaaa)
	ButtonFg      string  `yaml:"button_fg"`       // Button text color for [|] [-] [x] (default: #888888)
	DividerFg     string  `yaml:"divider_fg"`      // Divider "|" between panes (default: same as button_fg)
	BorderFromTab bool    `yaml:"border_from_tab"` // Use tab's color for active pane border (default: false)
	AutoBorder    bool    `yaml:"auto_border"`     // Auto-set pane border color from window's resolved color (default: false)
	BorderLines   string  `yaml:"border_lines"`    // Border style: single, double, heavy, simple, number (default: single)
	BorderFg      string  `yaml:"border_fg"`       // Border foreground color (default: #444444)
	DimInactive   bool    `yaml:"dim_inactive"`    // Enable dimming of inactive panes (default: false)
	DimOpacity    float64 `yaml:"dim_opacity"`     // Opacity for dimmed panes 0.0-1.0 (default: 0.7)
	// Custom border settings - render our own border instead of tmux's
	CustomBorder bool   `yaml:"custom_border"` // Enable custom border rendering (default: false)
	HandleColor  string `yaml:"handle_color"`  // Drag handle color (default: #666666)
	HandleIcon   string `yaml:"handle_icon"`   // Drag handle icon (default: "⋯")
	Draggable    bool   `yaml:"draggable"`     // Allow drag-to-resize via custom border (default: true)
	LargeMode    bool   `yaml:"large_mode"`
	TerminalBg   string `yaml:"terminal_bg"` // Terminal background color for hiding borders (default: #000000)
}

type SidebarHeader struct {
	Text          string `yaml:"text"`           // Header text (default: "TABBY")
	Height        int    `yaml:"height"`         // Total header rows (default: 3)
	PaddingBottom int    `yaml:"padding_bottom"` // Transparent rows below header (default: 1)
	Centered      *bool  `yaml:"centered"`       // Center text horizontally and vertically (default: true)
	ActiveColor   *bool  `yaml:"active_color"`   // Color based on active window group (default: true)
	Fg            string `yaml:"fg"`             // Override text color (default: "" = auto from active group or theme)
	Bg            string `yaml:"bg"`             // Override background color (default: "" = transparent/sidebar bg)
	Bold          *bool  `yaml:"bold"`           // Bold text (default: true)
}

type Sidebar struct {
	Position        string        `yaml:"position"`     // "left" (default) or "right"
	Mode            string        `yaml:"mode"`         // "full" (default) or "partial"
	Header          SidebarHeader `yaml:"header"`       // Sidebar header configuration
	PaneHeaders     bool          `yaml:"pane_headers"` // Enable clickable overlay pane headers
	NewTabButton    bool          `yaml:"new_tab_button"`
	CloseButton     bool          `yaml:"close_button"`
	NewGroupButton  bool          `yaml:"new_group_button"`
	ShowEmptyGroups bool          `yaml:"show_empty_groups"`
	SortBy          string        `yaml:"sort_by"`
	Debug           bool          `yaml:"debug"`           // Enable debug logging to /tmp/tabby-debug.log
	TouchMode       bool          `yaml:"touch_mode"`      // Larger tap targets for mobile/touch
	LineHeight      int           `yaml:"line_height"`     // Extra blank lines between items (0=compact, 1+=spaced)
	ActionZone      string        `yaml:"action_zone"`     // Widget zone for action buttons: "top" or "bottom" (default: "bottom")
	ActionPriority  int           `yaml:"action_priority"` // Priority within zone (default: 90)
	PrefixMode      bool          `yaml:"prefix_mode"`     // Flat window list with group prefixes (SD| NAME) instead of hierarchy
	Theme           string        `yaml:"theme"`           // Color theme: rose-pine-dawn, catppuccin-mocha, dracula, nord, etc.
	ThemeMode       string        `yaml:"theme_mode"`      // Theme detection: "auto" (default), "dark", or "light" (deprecated, use theme)
	Colors          SidebarColors `yaml:"colors"`          // Manual color overrides (applied on top of theme)
	TouchButtons    TouchButtons  `yaml:"touch_buttons"`   // Touch mode button styling
}

// TouchButtons configures colors for touch mode buttons
type TouchButtons struct {
	NewTabBg       string `yaml:"new_tab_bg"`       // New Tab button background (default: #27ae60)
	NewTabFg       string `yaml:"new_tab_fg"`       // New Tab button text (default: #ffffff)
	NewTabBorder   string `yaml:"new_tab_border"`   // New Tab button border (default: same as fg)
	NewGroupBg     string `yaml:"new_group_bg"`     // New Group button background (default: #9b59b6)
	NewGroupFg     string `yaml:"new_group_fg"`     // New Group button text (default: #ffffff)
	NewGroupBorder string `yaml:"new_group_border"` // New Group button border (default: same as fg)
	CloseBg        string `yaml:"close_bg"`         // Close button background (default: #e74c3c)
	CloseFg        string `yaml:"close_fg"`         // Close button text (default: #ffffff)
	CloseBorder    string `yaml:"close_border"`     // Close button border (default: same as fg)
	ActiveBorder   string `yaml:"active_border"`    // Active tab border color (default: from theme.active_indicator_bg)
	InactiveBorder string `yaml:"inactive_border"`  // Inactive tab border color (default: same as text)
}

type SidebarColors struct {
	Bg                  string `yaml:"bg"`                   // Sidebar background color (default: #1a1a2e)
	HeaderFg            string `yaml:"header_fg"`            // Group header text (default: #000000)
	ActiveFg            string `yaml:"active_fg"`            // Active tab text (default: #ffffff)
	InactiveFg          string `yaml:"inactive_fg"`          // Inactive tab text (default: #cccccc)
	DisclosureFg        string `yaml:"disclosure_fg"`        // Disclosure icon color (default: #000000)
	DisclosureExpanded  string `yaml:"disclosure_expanded"`  // Expanded state icon (default: ⊟)
	DisclosureCollapsed string `yaml:"disclosure_collapsed"` // Collapsed state icon (default: ⊞)
	ActiveIndicator     string `yaml:"active_indicator"`     // Active window/pane icon (default: ●)
	ActiveIndicatorFg   string `yaml:"active_indicator_fg"`  // Active indicator foreground (default: auto)
	ActiveIndicatorBg   string `yaml:"active_indicator_bg"`  // Active indicator background (default: empty)
	TreeFg              string `yaml:"tree_fg"`              // Tree branch color (default: #888888)
	TreeBranch          string `yaml:"tree_branch"`          // Branch connector: ├─ (default: ├─)
	TreeBranchLast      string `yaml:"tree_branch_last"`     // Last branch: └─ (default: └─)
	TreeConnector       string `yaml:"tree_connector"`       // Horizontal connector (default: ─)
	TreeConnectorPanes  string `yaml:"tree_connector_panes"` // Connector when has panes (default: ┬)
	TreeContinue        string `yaml:"tree_continue"`        // Vertical continuation (default: │)
}

type Style struct {
	Rounded        bool   `yaml:"rounded"`
	SeparatorLeft  string `yaml:"separator_left"`
	SeparatorRight string `yaml:"separator_right"`
}

type Overflow struct {
	Mode      string `yaml:"mode"`
	Indicator string `yaml:"indicator"`
}

type Group struct {
	Name       string `yaml:"name"`
	Pattern    string `yaml:"pattern"`
	Theme      Theme  `yaml:"theme"`
	WorkingDir string `yaml:"working_dir"` // Default directory for new windows/panes in this group
}

type Theme struct {
	Bg                string `yaml:"bg"`
	Fg                string `yaml:"fg"`
	ActiveBg          string `yaml:"active_bg"`
	ActiveFg          string `yaml:"active_fg"`
	InactiveBg        string `yaml:"inactive_bg"` // Inactive tab background (default: computed from bg)
	InactiveFg        string `yaml:"inactive_fg"` // Inactive tab text
	Icon              string `yaml:"icon"`
	ActiveIndicatorBg string `yaml:"active_indicator_bg"` // Lighter color for active indicator block
}

type Bindings struct {
	ToggleSidebar string `yaml:"toggle_sidebar"`
	NextTab       string `yaml:"next_tab"`
	PrevTab       string `yaml:"prev_tab"`
}

type Indicators struct {
	Activity Indicator `yaml:"activity"`
	Bell     Indicator `yaml:"bell"`
	Silence  Indicator `yaml:"silence"`
	Last     Indicator `yaml:"last"`
	Busy     Indicator `yaml:"busy"`  // Foreground process running (auto-detected)
	Input    Indicator `yaml:"input"` // Waiting for user input (e.g., Claude needs response)
}

type Indicator struct {
	Enabled bool     `yaml:"enabled"`
	Icon    string   `yaml:"icon"`
	Color   string   `yaml:"color"`            // Foreground color
	Bg      string   `yaml:"bg,omitempty"`     // Background color (optional, for visibility)
	Frames  []string `yaml:"frames,omitempty"` // Animation frames (if set, animates through these)
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".tmux/plugins/tmux-tabs/config.yaml")
}
