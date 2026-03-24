package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeYAML: %v", err)
	}
	return path
}

func loadEmpty(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadConfig(writeYAML(t, ""))
	if err != nil {
		t.Fatalf("loadEmpty: %v", err)
	}
	return cfg
}

func loadYAML(t *testing.T, content string) (*Config, error) {
	t.Helper()
	return LoadConfig(writeYAML(t, content))
}

func TestLoadConfig(t *testing.T) {
	t.Run("valid full yaml file", func(t *testing.T) {
		cfg, err := LoadConfig("testdata/valid_full.yaml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.NotNil(t, cfg)
		assert.Equal(t, "MY TABBY", cfg.Sidebar.Header.Text)
		assert.Equal(t, 4, cfg.Sidebar.Header.Height)
		assert.Len(t, cfg.Groups, 2)
		assert.Equal(t, "Work", cfg.Groups[0].Name)
	})

	t.Run("valid minimal yaml file", func(t *testing.T) {
		cfg, err := LoadConfig("testdata/valid_minimal.yaml")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.NotNil(t, cfg)
		assert.Len(t, cfg.Groups, 1)
		assert.Equal(t, "Default", cfg.Groups[0].Name)
		assert.Equal(t, "TABBY", cfg.Sidebar.Header.Text)
	})

	t.Run("missing file returns error wrapping os.ErrNotExist", func(t *testing.T) {
		_, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.yaml"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		assert.True(t, errors.Is(err, os.ErrNotExist),
			"expected os.ErrNotExist in error chain, got: %v", err)
	})

	t.Run("empty file returns config with all defaults applied", func(t *testing.T) {
		cfg, err := loadYAML(t, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.NotNil(t, cfg)
		assert.Equal(t, "TABBY", cfg.Sidebar.Header.Text)
		assert.Equal(t, "●", cfg.Indicators.Activity.Icon)
	})

	t.Run("malformed yaml returns error containing 'yaml'", func(t *testing.T) {
		_, err := LoadConfig("testdata/invalid_yaml.yaml")
		if err == nil {
			t.Fatal("expected error for malformed yaml, got nil")
		}
		assert.True(t, strings.Contains(strings.ToLower(err.Error()), "yaml"),
			"expected error message to contain 'yaml', got: %v", err)
	})

	t.Run("unknown yaml fields silently ignored", func(t *testing.T) {
		cfg, err := loadYAML(t, `
unknown_top_level: true
another_unknown: "value"
sidebar:
  header:
    text: "KNOWN"
  unknown_sidebar_field: 42
`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.NotNil(t, cfg)
		assert.Equal(t, "KNOWN", cfg.Sidebar.Header.Text)
	})
}

func TestApplyDefaults_Indicators(t *testing.T) {
	cfg := loadEmpty(t)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"activity icon", cfg.Indicators.Activity.Icon, "●"},
		{"activity color", cfg.Indicators.Activity.Color, "#f39c12"},
		{"bell icon", cfg.Indicators.Bell.Icon, "◆"},
		{"bell color", cfg.Indicators.Bell.Color, "#e74c3c"},
		{"silence icon", cfg.Indicators.Silence.Icon, "○"},
		{"silence color", cfg.Indicators.Silence.Color, "#95a5a6"},
		{"last icon", cfg.Indicators.Last.Icon, "-"},
		{"last color", cfg.Indicators.Last.Color, "#3498db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}

func TestApplyDefaults_PaneHeader(t *testing.T) {
	cfg := loadEmpty(t)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"resize grow icon", cfg.PaneHeader.ResizeGrowIcon, ">"},
		{"resize shrink icon", cfg.PaneHeader.ResizeShrinkIcon, "<"},
		{"resize vertical grow icon", cfg.PaneHeader.ResizeVerticalGrowIcon, "↓"},
		{"resize vertical shrink icon", cfg.PaneHeader.ResizeVerticalShrinkIcon, "↑"},
		{"resize separator", cfg.PaneHeader.ResizeSeparator, "¦"},
		{"collapse expanded icon", cfg.PaneHeader.CollapseExpandedIcon, "▾"},
		{"collapse collapsed icon", cfg.PaneHeader.CollapseCollapsedIcon, "▸"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}

func TestApplyDefaults_SidebarHeader(t *testing.T) {
	cfg := loadEmpty(t)

	assert.Equal(t, "TABBY", cfg.Sidebar.Header.Text)
	assert.Equal(t, 3, cfg.Sidebar.Header.Height)
	assert.Equal(t, 1, cfg.Sidebar.Header.PaddingBottom)
}

func TestApplyDefaults_SidebarColors(t *testing.T) {
	cfg := loadEmpty(t)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"inactive fg", cfg.Sidebar.Colors.InactiveFg, "#f2f2ee"},
		{"disclosure expanded", cfg.Sidebar.Colors.DisclosureExpanded, "⊟"},
		{"disclosure collapsed", cfg.Sidebar.Colors.DisclosureCollapsed, "⊞"},
		{"tree branch", cfg.Sidebar.Colors.TreeBranch, "├─"},
		{"tree branch last", cfg.Sidebar.Colors.TreeBranchLast, "└─"},
		{"tree connector", cfg.Sidebar.Colors.TreeConnector, "─"},
		{"tree connector panes", cfg.Sidebar.Colors.TreeConnectorPanes, "┬"},
		{"tree continue", cfg.Sidebar.Colors.TreeContinue, "│"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}

	assert.Len(t, cfg.Sidebar.Colors.ActiveIndicatorFrames, 6)
}

func TestApplyDefaults_UserValuesNotOverwritten(t *testing.T) {
	cfg, err := loadYAML(t, `
indicators:
  activity:
    icon: "★"
    color: "#ff0000"
sidebar:
  header:
    text: "CUSTOM"
    height: 5
  colors:
    inactive_fg: "#aabbcc"
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assert.Equal(t, "★", cfg.Indicators.Activity.Icon)
	assert.Equal(t, "#ff0000", cfg.Indicators.Activity.Color)
	assert.Equal(t, "CUSTOM", cfg.Sidebar.Header.Text)
	assert.Equal(t, 5, cfg.Sidebar.Header.Height)
	assert.Equal(t, "#aabbcc", cfg.Sidebar.Colors.InactiveFg)
	assert.Equal(t, "◆", cfg.Indicators.Bell.Icon)
}

func TestApplyIconStyleDefaults(t *testing.T) {
	presets := []string{"emoji", "nerd", "ascii", "box"}

	for _, style := range presets {
		style := style
		t.Run("preset_"+style, func(t *testing.T) {
			cfg, err := loadYAML(t, "sidebar:\n  icon_style: \""+style+"\"\n")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			preset := IconPresets[style]

			if preset.DisclosureExpanded != "" {
				assert.Equal(t, preset.DisclosureExpanded, cfg.Sidebar.Colors.DisclosureExpanded)
			}
			if preset.DisclosureCollapsed != "" {
				assert.Equal(t, preset.DisclosureCollapsed, cfg.Sidebar.Colors.DisclosureCollapsed)
			}
			if preset.ActiveIndicator != "" {
				assert.Equal(t, preset.ActiveIndicator, cfg.Sidebar.Colors.ActiveIndicator)
			}
			if preset.ActivityIcon != "" {
				assert.Equal(t, preset.ActivityIcon, cfg.Indicators.Activity.Icon)
			}
			if preset.BellIcon != "" {
				assert.Equal(t, preset.BellIcon, cfg.Indicators.Bell.Icon)
			}
			if preset.SilenceIcon != "" {
				assert.Equal(t, preset.SilenceIcon, cfg.Indicators.Silence.Icon)
			}
			if preset.TreeBranch != "" {
				assert.Equal(t, preset.TreeBranch, cfg.Sidebar.Colors.TreeBranch)
			}
		})
	}
}

func TestApplyIconStyleDefaults_UserOverrideNotOverwritten(t *testing.T) {
	cfg, err := loadYAML(t, `
sidebar:
  icon_style: "ascii"
indicators:
  activity:
    icon: "MY_ICON"
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, "MY_ICON", cfg.Indicators.Activity.Icon)
}

func TestApplyIconStyleDefaults_UnknownStyleFallsToHardcodedDefaults(t *testing.T) {
	cfg, err := loadYAML(t, `sidebar:
  icon_style: "unknown_style_xyz"
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, "●", cfg.Indicators.Activity.Icon)
	assert.Equal(t, "⊟", cfg.Sidebar.Colors.DisclosureExpanded)
}

func TestApplyIconStyleDefaults_EmptyStyleFallsToHardcodedDefaults(t *testing.T) {
	cfg, err := loadYAML(t, `sidebar:
  icon_style: ""
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, "●", cfg.Indicators.Activity.Icon)
}

func TestSaveConfig_RoundTrip(t *testing.T) {
	cfg, err := LoadConfig("testdata/valid_full.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg.Sidebar.Header.Text = "ROUND-TRIP"
	cfg.Groups = append(cfg.Groups, Group{
		Name:    "Personal",
		Pattern: "^personal\\|",
		Theme:   Theme{Bg: "#e74c3c"},
	})

	out := filepath.Join(t.TempDir(), "out.yaml")
	if err := SaveConfig(out, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	cfg2, err := LoadConfig(out)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}

	assert.Equal(t, "ROUND-TRIP", cfg2.Sidebar.Header.Text)
	assert.Len(t, cfg2.Groups, 3)

	personal := FindGroup(cfg2, "Personal")
	if personal == nil {
		t.Fatal("Personal group not found after round-trip")
	}
	assert.Equal(t, "#e74c3c", personal.Theme.Bg)
}

func TestSaveConfig_WriteToDirectoryReturnsError(t *testing.T) {
	cfg := &Config{}
	err := SaveConfig(t.TempDir(), cfg)
	assert.Error(t, err)
}

func TestAddGroup_InsertsBeforeDefault(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Existing", Pattern: "^ex"},
			{Name: "Default", Pattern: ".*"},
		},
	}
	if err := AddGroup(cfg, Group{Name: "NewGroup", Pattern: "^new"}); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}

	defaultIdx, newIdx := -1, -1
	for i, g := range cfg.Groups {
		if g.Name == "Default" {
			defaultIdx = i
		}
		if g.Name == "NewGroup" {
			newIdx = i
		}
	}
	assert.Greater(t, defaultIdx, newIdx)
}

func TestAddGroup_AppendsWhenNoDefaultGroup(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Alpha", Pattern: "^alpha"},
		},
	}
	if err := AddGroup(cfg, Group{Name: "Beta", Pattern: "^beta"}); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	assert.Len(t, cfg.Groups, 2)
	assert.Equal(t, "Beta", cfg.Groups[1].Name)
}

func TestAddGroup_DuplicateNameReturnsErrGroupExists(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Work", Pattern: "^work"},
		},
	}
	err := AddGroup(cfg, Group{Name: "Work", Pattern: "^work2"})
	assert.ErrorIs(t, err, ErrGroupExists)
}

func TestUpdateGroup_UpdatesExistingGroup(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Work", Pattern: "^work", Theme: Theme{Bg: "#aaaaaa"}},
		},
	}
	updated := Group{Name: "Work", Pattern: "^work-updated", Theme: Theme{Bg: "#123456"}}
	if err := UpdateGroup(cfg, "Work", updated); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	assert.Equal(t, "^work-updated", cfg.Groups[0].Pattern)
	assert.Equal(t, "#123456", cfg.Groups[0].Theme.Bg)
}

func TestUpdateGroup_NonExistentReturnsErrGroupNotFound(t *testing.T) {
	cfg := &Config{}
	err := UpdateGroup(cfg, "Ghost", Group{Name: "Ghost"})
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestDeleteGroup_DeletesExistingNonDefaultGroup(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Work", Pattern: "^work"},
			{Name: "Default", Pattern: ".*"},
		},
	}
	if err := DeleteGroup(cfg, "Work"); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}
	assert.Len(t, cfg.Groups, 1)
	assert.Equal(t, "Default", cfg.Groups[0].Name)
}

func TestDeleteGroup_DefaultGroupReturnsErrCannotDeleteGroup(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Default", Pattern: ".*"},
		},
	}
	err := DeleteGroup(cfg, "Default")
	assert.ErrorIs(t, err, ErrCannotDeleteGroup)
	assert.Len(t, cfg.Groups, 1)
}

func TestDeleteGroup_NonExistentReturnsErrGroupNotFound(t *testing.T) {
	cfg := &Config{}
	err := DeleteGroup(cfg, "Ghost")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestFindGroup_ReturnsPointerToExistingGroup(t *testing.T) {
	cfg := &Config{
		Groups: []Group{
			{Name: "Alpha", Pattern: "^alpha", Theme: Theme{Bg: "#aaa"}},
			{Name: "Beta", Pattern: "^beta", Theme: Theme{Bg: "#bbb"}},
		},
	}

	g := FindGroup(cfg, "Alpha")
	if g == nil {
		t.Fatal("expected non-nil group pointer")
	}
	assert.Equal(t, "#aaa", g.Theme.Bg)

	g.Theme.Bg = "#changed"
	assert.Equal(t, "#changed", cfg.Groups[0].Theme.Bg)
}

func TestFindGroup_ReturnsNilForNonExistentGroup(t *testing.T) {
	cfg := &Config{}
	assert.Nil(t, FindGroup(cfg, "Ghost"))
}

func TestDefaultGroup_ContainsNameAndNonEmptyColor(t *testing.T) {
	g := DefaultGroup("MyGroup")

	assert.Equal(t, "MyGroup", g.Name)
	assert.Contains(t, g.Pattern, "MyGroup")
	assert.NotEmpty(t, g.Theme.Bg)
}

func TestDefaultGroupWithIndex(t *testing.T) {
	tests := []struct {
		name  string
		index int
	}{
		{"index 0", 0},
		{"index 1", 1},
		{"index 5", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := DefaultGroupWithIndex("G", tt.index)
			assert.Equal(t, "G", g.Name)
			assert.NotEmpty(t, g.Theme.Bg)
		})
	}

	t.Run("different indices produce different colors", func(t *testing.T) {
		g0 := DefaultGroupWithIndex("G", 0)
		g1 := DefaultGroupWithIndex("G", 1)
		assert.NotEqual(t, g0.Theme.Bg, g1.Theme.Bg)
	})
}

func TestErrorSentinels_AreDistinct(t *testing.T) {
	assert.NotEqual(t, ErrGroupNotFound, ErrGroupExists)
	assert.NotEqual(t, ErrGroupExists, ErrCannotDeleteGroup)
	assert.NotEqual(t, ErrGroupNotFound, ErrCannotDeleteGroup)
}

func TestErrorSentinels_ErrorsIs(t *testing.T) {
	assert.ErrorIs(t, ErrGroupNotFound, ErrGroupNotFound)
	assert.ErrorIs(t, ErrGroupExists, ErrGroupExists)
	assert.ErrorIs(t, ErrCannotDeleteGroup, ErrCannotDeleteGroup)

	assert.False(t, errors.Is(ErrGroupNotFound, ErrGroupExists))
	assert.False(t, errors.Is(ErrGroupExists, ErrCannotDeleteGroup))
}

func TestIconPresets_AllPresetsPresent(t *testing.T) {
	expected := []string{"emoji", "nerd", "ascii", "box"}
	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			_, ok := IconPresets[name]
			assert.True(t, ok)
		})
	}
}

func TestIconPresets_AsciiHasNonEmptyValues(t *testing.T) {
	preset := IconPresets["ascii"]
	assert.NotEmpty(t, preset.DisclosureExpanded)
	assert.NotEmpty(t, preset.DisclosureCollapsed)
	assert.NotEmpty(t, preset.ActivityIcon)
	assert.NotEmpty(t, preset.TreeBranch)
}
