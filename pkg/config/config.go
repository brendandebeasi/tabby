package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	Position   string     `yaml:"position"`
	Height     int        `yaml:"height"`
	Style      Style      `yaml:"style"`
	Overflow   Overflow   `yaml:"overflow"`
	Groups     []Group    `yaml:"groups"`
	Bindings   Bindings   `yaml:"bindings"`
	Sidebar    Sidebar    `yaml:"sidebar"`
	Indicators Indicators `yaml:"indicators"`
}

type Sidebar struct {
	NewTabButton bool `yaml:"new_tab_button"`
	CloseButton  bool `yaml:"close_button"`
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
	Bg       string `yaml:"bg"`
	Fg       string `yaml:"fg"`
	ActiveBg string `yaml:"active_bg"`
	ActiveFg string `yaml:"active_fg"`
	Icon     string `yaml:"icon"`
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
}

type Indicator struct {
	Enabled bool   `yaml:"enabled"`
	Icon    string `yaml:"icon"`
	Color   string `yaml:"color"`
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".tmux/plugins/tmux-tabs/config.yaml")
}
