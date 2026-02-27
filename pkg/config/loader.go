package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"gopkg.in/yaml.v3"
)

var (
	ErrGroupNotFound     = errors.New("group not found")
	ErrGroupExists       = errors.New("group already exists")
	ErrCannotDeleteGroup = errors.New("cannot delete this group")
)

// IconPreset defines icons for tree, disclosure, and indicators
type IconPreset struct {
	// Disclosure triangles (expand/collapse groups)
	DisclosureExpanded  string
	DisclosureCollapsed string
	// Active indicator
	ActiveIndicator string
	// Tree connectors
	TreeBranch         string
	TreeBranchLast     string
	TreeConnector      string
	TreeConnectorPanes string
	TreeContinue       string
	// Indicator icons
	ActivityIcon string
	BellIcon     string
	SilenceIcon  string
	BusyIcon     string
	InputIcon    string
}

// IconPresets maps style names to icon sets
var IconPresets = map[string]IconPreset{
	"emoji": {
		DisclosureExpanded:  "v",
		DisclosureCollapsed: ">",
		ActiveIndicator:     ">>",
		TreeBranch:          "",
		TreeBranchLast:      "",
		TreeConnector:       "",
		TreeConnectorPanes:  "",
		TreeContinue:        "",
		ActivityIcon:        "!",
		BellIcon:            "**",
		SilenceIcon:         "**",
		BusyIcon:            "**",
		InputIcon:           "?",
	},
	"nerd": {
		DisclosureExpanded:  "", // nf-fa-chevron_down
		DisclosureCollapsed: "", // nf-fa-chevron_right
		ActiveIndicator:     "", // nf-fa-caret_right
		TreeBranch:          "", // nf-cod-indent (or use box drawing)
		TreeBranchLast:      "", // nf-cod-indent
		TreeConnector:       "",
		TreeConnectorPanes:  "",
		TreeContinue:        "",
		ActivityIcon:        "", // nf-fa-circle
		BellIcon:            "", // nf-fa-bell
		SilenceIcon:         "", // nf-fa-bell_slash
		BusyIcon:            "", // nf-fa-spinner (or use loading)
		InputIcon:           "", // nf-fa-question_circle
	},
	"ascii": {
		DisclosureExpanded:  "[-]",
		DisclosureCollapsed: "[+]",
		ActiveIndicator:     ">",
		TreeBranch:          "+-",
		TreeBranchLast:      "`-",
		TreeConnector:       "-",
		TreeConnectorPanes:  "+-",
		TreeContinue:        "|",
		ActivityIcon:        "*",
		BellIcon:            "!",
		SilenceIcon:         "-",
		BusyIcon:            "*",
		InputIcon:           "?",
	},
	"box": {
		// Box drawing characters (default, works with any font)
		DisclosureExpanded:  "v",
		DisclosureCollapsed: ">",
		ActiveIndicator:     ">>",
		TreeBranch:          "",
		TreeBranchLast:      "",
		TreeConnector:       "",
		TreeConnectorPanes:  "",
		TreeContinue:        "",
		ActivityIcon:        "!",
		BellIcon:            "**",
		SilenceIcon:         "**",
		BusyIcon:            "**",
		InputIcon:           "?",
	},
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// SaveConfig writes the config to the specified path
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// AddGroup adds a new group to the config.
// The group is inserted before the "Default" group to ensure pattern matching order.
func AddGroup(cfg *Config, group Group) error {
	// Check if group name already exists
	for _, g := range cfg.Groups {
		if g.Name == group.Name {
			return ErrGroupExists
		}
	}

	// Find the Default group index and insert before it
	defaultIdx := -1
	for i, g := range cfg.Groups {
		if g.Name == "Default" {
			defaultIdx = i
			break
		}
	}

	if defaultIdx == -1 {
		// No Default group, just append
		cfg.Groups = append(cfg.Groups, group)
	} else {
		// Insert before Default
		cfg.Groups = append(cfg.Groups[:defaultIdx], append([]Group{group}, cfg.Groups[defaultIdx:]...)...)
	}
	return nil
}

// UpdateGroup updates an existing group by name
func UpdateGroup(cfg *Config, oldName string, group Group) error {
	for i, g := range cfg.Groups {
		if g.Name == oldName {
			cfg.Groups[i] = group
			return nil
		}
	}
	return ErrGroupNotFound
}

// DeleteGroup removes a group by name.
// The "Default" group cannot be deleted.
func DeleteGroup(cfg *Config, name string) error {
	if name == "Default" {
		return ErrCannotDeleteGroup
	}

	for i, g := range cfg.Groups {
		if g.Name == name {
			cfg.Groups = append(cfg.Groups[:i], cfg.Groups[i+1:]...)
			return nil
		}
	}
	return ErrGroupNotFound
}

// FindGroup returns a pointer to the group with the given name, or nil if not found
func FindGroup(cfg *Config, name string) *Group {
	for i := range cfg.Groups {
		if cfg.Groups[i].Name == name {
			return &cfg.Groups[i]
		}
	}
	return nil
}

// DefaultGroup returns a new group with default theme colors.
// It uses the group index to pick a unique color from the palette.
func DefaultGroup(name string) Group {
	return DefaultGroupWithIndex(name, 0)
}

// DefaultGroupWithIndex returns a new group using the palette color at the given index.
func DefaultGroupWithIndex(name string, groupIndex int) Group {
	bg := colors.GetDefaultGroupColor(groupIndex)
	return Group{
		Name:    name,
		Pattern: fmt.Sprintf("^%s\\|", name),
		Theme: Theme{
			Bg:   bg,
			Icon: "",
			// Leave Fg, ActiveBg, ActiveFg, InactiveBg, InactiveFg empty
			// so AutoFillTheme() derives them properly from the base color.
		},
	}
}

func applyDefaults(cfg *Config) {
	// Apply icon style preset if set (before individual icon defaults)
	applyIconStyleDefaults(cfg)

	if cfg.Indicators.Activity.Icon == "" {
		cfg.Indicators.Activity.Icon = "●"
		cfg.Indicators.Activity.Color = "#f39c12"
	}
	if cfg.Indicators.Bell.Icon == "" {
		cfg.Indicators.Bell.Icon = "◆"
		cfg.Indicators.Bell.Color = "#e74c3c"
	}
	if cfg.Indicators.Silence.Icon == "" {
		cfg.Indicators.Silence.Icon = "○"
		cfg.Indicators.Silence.Color = "#95a5a6"
	}
	if cfg.Indicators.Last.Icon == "" {
		cfg.Indicators.Last.Icon = "-"
		cfg.Indicators.Last.Color = "#3498db"
	}
	// Note: PaneHeader.CustomBorder defaults (HandleColor, HandleIcon, Draggable)
	// are applied at render time in coordinator.go since we can't distinguish
	// between unset and false for Draggable in YAML.

	// Sidebar header defaults
	if cfg.Sidebar.Header.Text == "" {
		cfg.Sidebar.Header.Text = "TABBY"
	}
	if cfg.Sidebar.Header.Height == 0 {
		cfg.Sidebar.Header.Height = 3
	}
	if cfg.Sidebar.Header.PaddingBottom == 0 {
		cfg.Sidebar.Header.PaddingBottom = 1
	}
	// Bool defaults (Centered, ActiveColor, Bold) default to true.
	// Since Go zero-value is false, we use *bool pointers in the struct
	// so nil = unset = use default (true). See headerBoolDefault() in coordinator.

	// Sidebar text defaults
	if cfg.Sidebar.Colors.InactiveFg == "" {
		cfg.Sidebar.Colors.InactiveFg = "#f2f2ee"
	}

	// Active indicator animation: blink pattern (5 visible + 1 blank frame).
	// Only set if user hasn't configured frames at all — an empty YAML list
	// (active_indicator_frames: []) is indistinguishable from missing, so
	// this always fills the default when the slice is nil/empty.
	if len(cfg.Sidebar.Colors.ActiveIndicatorFrames) == 0 {
		cfg.Sidebar.Colors.ActiveIndicatorFrames = []string{"▶", "▶", "▶", "▶", "▶", " "}
	}

	// Disclosure icons
	if cfg.Sidebar.Colors.DisclosureExpanded == "" {
		cfg.Sidebar.Colors.DisclosureExpanded = "⊟"
	}
	if cfg.Sidebar.Colors.DisclosureCollapsed == "" {
		cfg.Sidebar.Colors.DisclosureCollapsed = "⊞"
	}

	// Tree connector defaults
	if cfg.Sidebar.Colors.TreeBranch == "" {
		cfg.Sidebar.Colors.TreeBranch = "├─"
	}
	if cfg.Sidebar.Colors.TreeBranchLast == "" {
		cfg.Sidebar.Colors.TreeBranchLast = "└─"
	}
	if cfg.Sidebar.Colors.TreeConnector == "" {
		cfg.Sidebar.Colors.TreeConnector = "─"
	}
	if cfg.Sidebar.Colors.TreeConnectorPanes == "" {
		cfg.Sidebar.Colors.TreeConnectorPanes = "┬"
	}
	if cfg.Sidebar.Colors.TreeContinue == "" {
		cfg.Sidebar.Colors.TreeContinue = "│"
	}

	if cfg.Web.Host == "" {
		cfg.Web.Host = "127.0.0.1"
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 8080
	}
}

// applyIconStyleDefaults applies icon preset values based on IconStyle setting.
// Only sets values that are empty (allows user overrides).
func applyIconStyleDefaults(cfg *Config) {
	style := cfg.Sidebar.IconStyle
	if style == "" {
		return // No global icon style set, use individual defaults
	}

	preset, ok := IconPresets[style]
	if !ok {
		return // Unknown style
	}

	// Apply disclosure icons
	if cfg.Sidebar.Colors.DisclosureExpanded == "" && preset.DisclosureExpanded != "" {
		cfg.Sidebar.Colors.DisclosureExpanded = preset.DisclosureExpanded
	}
	if cfg.Sidebar.Colors.DisclosureCollapsed == "" && preset.DisclosureCollapsed != "" {
		cfg.Sidebar.Colors.DisclosureCollapsed = preset.DisclosureCollapsed
	}

	// Apply active indicator
	if cfg.Sidebar.Colors.ActiveIndicator == "" && preset.ActiveIndicator != "" {
		cfg.Sidebar.Colors.ActiveIndicator = preset.ActiveIndicator
	}

	// Apply tree icons
	if cfg.Sidebar.Colors.TreeBranch == "" && preset.TreeBranch != "" {
		cfg.Sidebar.Colors.TreeBranch = preset.TreeBranch
	}
	if cfg.Sidebar.Colors.TreeBranchLast == "" && preset.TreeBranchLast != "" {
		cfg.Sidebar.Colors.TreeBranchLast = preset.TreeBranchLast
	}
	if cfg.Sidebar.Colors.TreeConnector == "" && preset.TreeConnector != "" {
		cfg.Sidebar.Colors.TreeConnector = preset.TreeConnector
	}
	if cfg.Sidebar.Colors.TreeConnectorPanes == "" && preset.TreeConnectorPanes != "" {
		cfg.Sidebar.Colors.TreeConnectorPanes = preset.TreeConnectorPanes
	}
	if cfg.Sidebar.Colors.TreeContinue == "" && preset.TreeContinue != "" {
		cfg.Sidebar.Colors.TreeContinue = preset.TreeContinue
	}

	// Apply indicator icons
	if cfg.Indicators.Activity.Icon == "" && preset.ActivityIcon != "" {
		cfg.Indicators.Activity.Icon = preset.ActivityIcon
	}
	if cfg.Indicators.Bell.Icon == "" && preset.BellIcon != "" {
		cfg.Indicators.Bell.Icon = preset.BellIcon
	}
	if cfg.Indicators.Silence.Icon == "" && preset.SilenceIcon != "" {
		cfg.Indicators.Silence.Icon = preset.SilenceIcon
	}
	if cfg.Indicators.Busy.Icon == "" && preset.BusyIcon != "" {
		cfg.Indicators.Busy.Icon = preset.BusyIcon
	}
	if cfg.Indicators.Input.Icon == "" && preset.InputIcon != "" {
		cfg.Indicators.Input.Icon = preset.InputIcon
	}

	// Apply to widget styles if not already set
	if cfg.Widgets.Pet.Style == "" {
		cfg.Widgets.Pet.Style = style
	}
	if cfg.Widgets.Stats.Style == "" {
		cfg.Widgets.Stats.Style = style
	}
	if cfg.Widgets.Session.Style == "" {
		cfg.Widgets.Session.Style = style
	}
	if cfg.Widgets.Git.Style == "" {
		cfg.Widgets.Git.Style = style
	}
}
