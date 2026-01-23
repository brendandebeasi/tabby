package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Position   string      `yaml:"position"`
	Height     int         `yaml:"height"`
	Style      Style       `yaml:"style"`
	Overflow   Overflow    `yaml:"overflow"`
	Groups     []Group     `yaml:"groups"`
	Bindings   Bindings    `yaml:"bindings"`
	Sidebar    Sidebar     `yaml:"sidebar"`
	PaneHeader PaneHeader  `yaml:"pane_header"`
	Indicators Indicators  `yaml:"indicators"`
	Prompt     PromptStyle `yaml:"prompt"`
	Widgets    Widgets     `yaml:"widgets"`
}

type Widgets struct {
	Clock ClockWidget `yaml:"clock"`
}

type ClockWidget struct {
	Enabled       bool   `yaml:"enabled"`
	Format        string `yaml:"format"`          // Go time format (default: "15:04:05")
	ShowDate      bool   `yaml:"show_date"`       // Show date below time
	DateFmt       string `yaml:"date_format"`     // Date format (default: "Mon Jan 2")
	Fg            string `yaml:"fg"`              // Text color
	Bg            string `yaml:"bg"`              // Background color
	Position      string `yaml:"position"`        // "top" or "bottom" (default: bottom)
	Pin           bool   `yaml:"pin"`             // Pin to bottom of sidebar (scrolls with content if false)
	Priority      int    `yaml:"priority"`        // Order when multiple widgets pinned (lower = closer to bottom)
	Divider       string `yaml:"divider"`         // Divider line above widget
	DividerBottom string `yaml:"divider_bottom"`  // Divider line below widget
	DividerFg     string `yaml:"divider_fg"`      // Divider color
	PaddingTop    int    `yaml:"padding_top"`     // Blank lines above content
	PaddingBot    int    `yaml:"padding_bottom"`  // Blank lines below content
	MarginTop     int    `yaml:"margin_top"`      // Lines above top divider
	MarginBot     int    `yaml:"margin_bottom"`   // Lines below bottom divider
}

type PromptStyle struct {
	Fg   string `yaml:"fg"`   // Prompt text color (default: #000000)
	Bg   string `yaml:"bg"`   // Prompt background (default: #f0f0f0)
	Bold bool   `yaml:"bold"` // Bold text (default: true)
}

type PaneHeader struct {
	ActiveFg      string `yaml:"active_fg"`       // Active pane header text (default: #ffffff)
	ActiveBg      string `yaml:"active_bg"`       // Active pane header bg fallback (default: #3498db)
	InactiveFg    string `yaml:"inactive_fg"`     // Inactive pane header text (default: #cccccc)
	InactiveBg    string `yaml:"inactive_bg"`     // Inactive pane header bg fallback (default: #333333)
	CommandFg     string `yaml:"command_fg"`      // Command text color (default: #aaaaaa)
	BorderFromTab bool   `yaml:"border_from_tab"` // Use tab's color for active pane border (default: false)
	BorderLines   string `yaml:"border_lines"`    // Border style: single, double, heavy, simple, number (default: single)
	BorderFg      string `yaml:"border_fg"`       // Border foreground color (default: #444444)
}

type Sidebar struct {
	NewTabButton    bool          `yaml:"new_tab_button"`
	CloseButton     bool          `yaml:"close_button"`
	NewGroupButton  bool          `yaml:"new_group_button"`
	ShowEmptyGroups bool          `yaml:"show_empty_groups"`
	SortBy          string        `yaml:"sort_by"`
	Debug           bool          `yaml:"debug"` // Enable debug logging to /tmp/tabby-debug.log
	Colors          SidebarColors `yaml:"colors"`
}

type SidebarColors struct {
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
	Bg                 string `yaml:"bg"`
	Fg                 string `yaml:"fg"`
	ActiveBg           string `yaml:"active_bg"`
	ActiveFg           string `yaml:"active_fg"`
	InactiveBg         string `yaml:"inactive_bg"`          // Inactive tab background (default: computed from bg)
	InactiveFg         string `yaml:"inactive_fg"`          // Inactive tab text
	Icon               string `yaml:"icon"`
	ActiveIndicatorBg  string `yaml:"active_indicator_bg"`  // Lighter color for active indicator block
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
