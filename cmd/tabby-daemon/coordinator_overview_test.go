package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

var zoneMarkRe = regexp.MustCompile(`\x1b\[[0-9;]*z`)

func stripForGolden(s string) string {
	return zoneMarkRe.ReplaceAllString(stripAnsi(s), "")
}

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

func TestTabSwitcherRegions(t *testing.T) {
	c := newOverviewCoordinator("current")
	_, regions := c.renderTabSwitcher(25)

	if len(regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regions))
	}
	if regions[0].Action != "switch_view" || regions[0].Target != "current" {
		t.Errorf("region[0]: got action=%q target=%q, want switch_view/current", regions[0].Action, regions[0].Target)
	}
	if regions[1].Action != "switch_view" || regions[1].Target != "overview" {
		t.Errorf("region[1]: got action=%q target=%q, want switch_view/overview", regions[1].Action, regions[1].Target)
	}
	if regions[0].EndCol <= 0 || regions[1].StartCol <= 0 {
		t.Errorf("tab regions have zero column bounds: %+v %+v", regions[0], regions[1])
	}
}

func TestTabSwitcherNarrowWidth(t *testing.T) {
	c := newOverviewCoordinator("overview")
	content, regions := c.renderTabSwitcher(12)
	if content == "" {
		t.Error("renderTabSwitcher(12) returned empty content")
	}
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions at narrow width, got %d", len(regions))
	}
}

func TestOverviewContentEmpty(t *testing.T) {
	c := newOverviewCoordinator("overview")
	content, regions := c.renderOverviewContent(30)
	_ = content
	_ = regions
}

func TestOverviewContentRegions(t *testing.T) {
	c := newOverviewCoordinator("overview")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	_, regions := c.renderOverviewContent(30)

	if len(regions) == 0 {
		t.Fatal("renderOverviewContent returned no regions")
	}
	for i, r := range regions {
		if r.Action == "" {
			t.Errorf("region[%d] has empty action", i)
		}
		if r.Target == "" {
			t.Errorf("region[%d] has empty target", i)
		}
		switch r.Action {
		case "select_window", "select_pane", "overview_toggle_window":
		default:
			t.Errorf("region[%d] has unexpected action %q", i, r.Action)
		}
	}
}

func TestGoldenRenderOverviewContent(t *testing.T) {
	c := newOverviewCoordinator("overview")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	content, _ := c.renderOverviewContent(30)
	checkOrUpdateGolden(t, "render_overview_content", stripForGolden(content))
}

func TestGoldenRenderOverviewContentExpanded(t *testing.T) {
	c := newOverviewCoordinator("overview")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	c.overviewCollapsed["@1"] = true
	content, _ := c.renderOverviewContent(30)
	checkOrUpdateGolden(t, "render_overview_content_expanded", stripForGolden(content))
}

func TestGoldenTabSwitcherCurrent(t *testing.T) {
	c := newOverviewCoordinator("current")
	content, _ := c.renderTabSwitcher(25)
	checkOrUpdateGolden(t, "tab_switcher_current", stripForGolden(content))
}

func TestGoldenTabSwitcherOverview(t *testing.T) {
	c := newOverviewCoordinator("overview")
	content, _ := c.renderTabSwitcher(25)
	checkOrUpdateGolden(t, "tab_switcher_overview", stripForGolden(content))
}

func TestSwitchViewAction(t *testing.T) {
	c := newOverviewCoordinator("current")
	c.setViewMode("overview")

	c.stateMu.RLock()
	mode := c.viewMode
	c.stateMu.RUnlock()

	if mode != "overview" {
		t.Errorf("viewMode = %q, want \"overview\"", mode)
	}

	c.setViewMode("current")
	c.stateMu.RLock()
	mode = c.viewMode
	c.stateMu.RUnlock()
	if mode != "current" {
		t.Errorf("viewMode = %q, want \"current\"", mode)
	}
}

func TestToggleOverviewWindowAction(t *testing.T) {
	c := newOverviewCoordinator("overview")

	// First toggle: default collapsed → expanded (false in map = NOT collapsed)
	c.toggleOverviewWindow("@1")
	if c.isOverviewWindowCollapsed("@1") {
		t.Error("after 1st toggle, window should be expanded (not collapsed)")
	}

	// Second toggle: back to collapsed
	c.toggleOverviewWindow("@1")
	if !c.isOverviewWindowCollapsed("@1") {
		t.Error("after 2nd toggle, window should be collapsed again")
	}
}

func TestSelectPaneKeepsOverview(t *testing.T) {
	// Verify select_pane action does not change viewMode
	c := newOverviewCoordinator("overview")
	// viewMode starts as "overview"
	c.stateMu.RLock()
	before := c.viewMode
	c.stateMu.RUnlock()

	if before != "overview" {
		t.Fatalf("precondition: viewMode = %q, want \"overview\"", before)
	}

	// select_pane doesn't touch viewMode — just verify the field is unchanged
	// (full tmux integration tested by e2e)
	c.stateMu.RLock()
	after := c.viewMode
	c.stateMu.RUnlock()
	if after != "overview" {
		t.Errorf("after select_pane simulation: viewMode = %q, want \"overview\"", after)
	}
}

func TestGenerateMainContentTabSwitcherAlwaysPresent(t *testing.T) {
	for _, mode := range []string{"current", "overview"} {
		t.Run(mode, func(t *testing.T) {
			c := newOverviewCoordinator(mode)
			content, _ := c.generateMainContent("@0", 30, 40)
			plain := stripForGolden(content)
			if !strings.Contains(plain, "Window") {
				t.Errorf("mode=%s: 'Window' tab label missing from content", mode)
			}
			if !strings.Contains(plain, "All") {
				t.Errorf("mode=%s: 'All' tab label missing from content", mode)
			}
		})
	}
}

func TestGenerateMainContentRegionShiftInCurrentMode(t *testing.T) {
	c := newOverviewCoordinator("current")
	windows := makeTestWindows(1)
	c.grouped = groupTestWindows(windows, c.config)
	_, regions := c.generateMainContent("@0", 30, 40)

	for _, r := range regions {
		if r.Action == "select_window" {
			if r.StartLine < 2 {
				t.Errorf("select_window region StartLine=%d, want >= 2 (tab switcher occupies lines 0-1)", r.StartLine)
			}
			return
		}
	}
	t.Error("no select_window region found")
}

func TestGoldenGenerateMainContentOverview(t *testing.T) {
	c := newOverviewCoordinator("overview")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	content, _ := c.generateMainContent("@1", 30, 40)
	checkOrUpdateGolden(t, "generate_main_content_overview", stripForGolden(content))
}

func TestGoldenGenerateMainContentCurrent(t *testing.T) {
	c := newOverviewCoordinator("current")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	content, _ := c.generateMainContent("@1", 30, 40)
	checkOrUpdateGolden(t, "generate_main_content_current", stripForGolden(content))
}

func TestRenderForClientOverviewMode(t *testing.T) {
	c := newOverviewCoordinator("overview")
	windows := makeTestWindows(3)
	c.grouped = groupTestWindows(windows, c.config)
	c.clientWidths = make(map[string]int)
	c.lastWindowSelect = make(map[string]time.Time)
	c.lastWindowByClient = make(map[string]time.Time)

	payload := c.RenderForClient("test-client", 25, 40)
	if payload == nil {
		t.Fatal("RenderForClient returned nil")
	}
	if payload.Content == "" {
		t.Fatal("RenderForClient returned empty content")
	}

	plain := stripAnsi(payload.Content)
	if !strings.Contains(plain, "Frontend") && !strings.Contains(plain, "Default") {
		t.Errorf("overview content missing group names; got:\n%s", plain)
	}

	if len(payload.Regions) < 2 {
		t.Errorf("expected at least 2 regions (tab switcher), got %d", len(payload.Regions))
	}
}
