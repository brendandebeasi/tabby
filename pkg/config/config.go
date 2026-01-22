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
	NewTabButton bool          `yaml:"new_tab_button"`
	CloseButton  bool          `yaml:"close_button"`
	Colors       SidebarColors `yaml:"colors"`
}

type SidebarColors struct {
	HeaderFg   string `yaml:"header_fg"`   // Group header text (default: #000000)
	ActiveFg   string `yaml:"active_fg"`   // Active tab text (default: #ffffff)
	InactiveFg string `yaml:"inactive_fg"` // Inactive tab text (default: #cccccc)
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
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
	Theme   Theme  `yaml:"theme"`
}

type Theme struct {
	Bg         string `yaml:"bg"`
	Fg         string `yaml:"fg"`
	ActiveBg   string `yaml:"active_bg"`
	ActiveFg   string `yaml:"active_fg"`
	InactiveBg string `yaml:"inactive_bg"` // Inactive tab background (default: computed from bg)
	InactiveFg string `yaml:"inactive_fg"` // Inactive tab text
	Icon       string `yaml:"icon"`
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
	Busy     Indicator `yaml:"busy"` // Foreground process running (auto-detected)
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
