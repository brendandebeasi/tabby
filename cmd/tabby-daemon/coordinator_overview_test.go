package main

import (
	"fmt"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// newOverviewCoordinator creates a minimal coordinator for overview mode testing.
func newOverviewCoordinator(viewMode string) *Coordinator {
	return &Coordinator{
		config:            &config.Config{},
		bgDetector:        colors.NewBackgroundDetector(colors.ThemeModeDark),
		collapsedGroups:   make(map[string]bool),
		viewMode:          viewMode,
		overviewCollapsed: make(map[string]bool),
	}
}

// makeTestWindows creates N test windows with 2 panes each, cycling through groups.
func makeTestWindows(count int) []tmux.Window {
	groups := []string{"Frontend", "Backend", "Default"}
	var windows []tmux.Window

	for i := 0; i < count; i++ {
		group := groups[i%len(groups)]
		window := tmux.Window{
			ID:    fmt.Sprintf("@%d", i+1),
			Index: i,
			Name:  fmt.Sprintf("win-%d", i+1),
			Group: group,
			Panes: []tmux.Pane{
				{
					ID:      fmt.Sprintf("%%0.%d", i),
					Index:   0,
					Command: "vim",
				},
				{
					ID:      fmt.Sprintf("%%1.%d", i),
					Index:   1,
					Command: "bash",
				},
			},
		}
		windows = append(windows, window)
	}

	return windows
}

// groupTestWindows wraps grouping.GroupWindowsWithOptions with a test config.
func groupTestWindows(windows []tmux.Window, cfg *config.Config) []grouping.GroupedWindows {
	// Ensure config has the three test groups defined
	if len(cfg.Groups) == 0 {
		cfg.Groups = []config.Group{
			{
				Name:    "Frontend",
				Pattern: "^Frontend",
			},
			{
				Name:    "Backend",
				Pattern: "^Backend",
			},
			{
				Name:    "Default",
				Pattern: ".*",
			},
		}
	}

	return grouping.GroupWindowsWithOptions(windows, cfg.Groups, false)
}

// TestOverviewHelpers smoke test: creates coordinator and verifies basic setup.
func TestOverviewHelpers(t *testing.T) {
	c := newOverviewCoordinator("overview")

	if c == nil {
		t.Fatal("newOverviewCoordinator returned nil")
	}
	if c.config == nil {
		t.Fatal("coordinator config is nil")
	}
	if c.bgDetector == nil {
		t.Fatal("coordinator bgDetector is nil")
	}
	if c.collapsedGroups == nil {
		t.Fatal("coordinator collapsedGroups is nil")
	}
}

// TestMakeTestWindows verifies window creation and grouping.
func TestMakeTestWindows(t *testing.T) {
	windows := makeTestWindows(3)

	if len(windows) != 3 {
		t.Errorf("makeTestWindows(3) returned %d windows, want 3", len(windows))
	}

	// Check each window has 2 panes
	for i, win := range windows {
		if len(win.Panes) < 2 {
			t.Errorf("window %d has %d panes, want >= 2", i, len(win.Panes))
		}
	}

	// Check groups are assigned correctly (cycling: Frontend, Backend, Default)
	expectedGroups := []string{"Frontend", "Backend", "Default"}
	for i, win := range windows {
		if win.Group != expectedGroups[i] {
			t.Errorf("window %d group = %q, want %q", i, win.Group, expectedGroups[i])
		}
	}
}

// TestGroupTestWindows verifies grouping helper works correctly.
func TestGroupTestWindows(t *testing.T) {
	windows := makeTestWindows(3)
	cfg := &config.Config{}

	grouped := groupTestWindows(windows, cfg)

	if len(grouped) == 0 {
		t.Fatal("groupTestWindows returned empty result")
	}

	// Verify we have groups with windows
	totalWindows := 0
	for _, g := range grouped {
		totalWindows += len(g.Windows)
	}

	if totalWindows != 3 {
		t.Errorf("grouped windows total = %d, want 3", totalWindows)
	}
}
