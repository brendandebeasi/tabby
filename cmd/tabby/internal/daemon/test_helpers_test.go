package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

// testConfig returns a minimal but valid config with all required fields set.
// This mirrors what applyDefaults() produces without needing a config file.
func testConfig() *config.Config {
	cfg := &config.Config{}
	// Groups
	cfg.Groups = []config.Group{
		{Name: "TestGroup", Pattern: `^TestGroup\|`, Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Default", Pattern: `.*`, Theme: config.Theme{Bg: "#2c3e50"}},
	}
	// Sidebar header
	cfg.Sidebar.Header.Text = "TABBY"
	cfg.Sidebar.Header.Height = 3
	cfg.Sidebar.Header.PaddingBottom = 1
	// Indicators
	cfg.Indicators.Activity.Icon = "●"
	cfg.Indicators.Activity.Color = "#f39c12"
	cfg.Indicators.Bell.Icon = "◆"
	cfg.Indicators.Bell.Color = "#e74c3c"
	cfg.Indicators.Silence.Icon = "○"
	cfg.Indicators.Silence.Color = "#95a5a6"
	cfg.Indicators.Last.Icon = "-"
	cfg.Indicators.Last.Color = "#3498db"
	// Active indicator animation frames
	cfg.Sidebar.Colors.ActiveIndicatorFrames = []string{"▶", "▶", "▶", "▶", "▶", " "}
	cfg.Sidebar.Colors.InactiveFg = "#f2f2ee"
	// Disclosure icons
	cfg.Sidebar.Colors.DisclosureExpanded = "⊟"
	cfg.Sidebar.Colors.DisclosureCollapsed = "⊞"
	// Tree connectors
	cfg.Sidebar.Colors.TreeBranch = "├─"
	cfg.Sidebar.Colors.TreeBranchLast = "└─"
	cfg.Sidebar.Colors.TreeConnector = "─"
	cfg.Sidebar.Colors.TreeConnectorPanes = "┬"
	cfg.Sidebar.Colors.TreeContinue = "│"
	// PaneHeader icons
	cfg.PaneHeader.ResizeGrowIcon = ">"
	cfg.PaneHeader.ResizeShrinkIcon = "<"
	cfg.PaneHeader.ResizeVerticalGrowIcon = "↓"
	cfg.PaneHeader.ResizeVerticalShrinkIcon = "↑"
	cfg.PaneHeader.ResizeSeparator = "¦"
	cfg.PaneHeader.CollapseExpandedIcon = "▾"
	cfg.PaneHeader.CollapseCollapsedIcon = "▸"
	return cfg
}

// testGroup returns a config.Group with the given name, pattern, and background color.
func testGroup(name, pattern, color string) config.Group {
	return config.Group{
		Name:    name,
		Pattern: pattern,
		Theme:   config.Theme{Bg: color},
	}
}

// testWindow builds a tmux.Window with the given name, active state, and panes
// (one per command string). The first pane is marked Active.
func testWindow(name string, active bool, paneCommands ...string) tmux.Window {
	w := tmux.Window{
		ID:        "@" + name,
		Name:      name,
		Active:    active,
		SyncWidth: true,
	}
	for i, cmd := range paneCommands {
		w.Panes = append(w.Panes, tmux.Pane{
			ID:      fmt.Sprintf("%%%s-%d", name, i),
			Index:   i,
			Command: cmd,
			Active:  i == 0,
		})
	}
	return w
}

// newTestCoordinator builds a minimal Coordinator with all maps initialised,
// no tmux calls, no event loop, and no sockets. Safe to use in pure unit tests.
func newTestCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	cfg := testConfig()
	return &Coordinator{
		config:             cfg,
		bgDetector:         colors.NewBackgroundDetector(colors.ThemeModeDark),
		windows:            []tmux.Window{},
		grouped:            []grouping.GroupedWindows{},
		windowVisualPos:    make(map[string]int),
		collapsedGroups:    make(map[string]bool),
		cwdColors:          make(map[string]CWDColorMapping),
		clientWidths:       make(map[string]int),
		prevPaneBusy:       make(map[string]bool),
		prevPaneTitle:      make(map[string]string),
		hookPaneActive:     make(map[string]bool),
		hookPaneBusyIdleAt: make(map[string]int64),
		aiBellUntil:        make(map[int]int64),
		pendingMenus:       make(map[string][]menuItemDef),
		lastWindowSelect:   make(map[string]time.Time),
		lastWindowByClient: make(map[string]time.Time),
		lastPaneMenuOpen:   make(map[string]time.Time),
		sessionID:          "test-session",
		globalWidth:        30,
	}
}

// TestHelpersSanity verifies that the helper functions produce valid,
// self-consistent state without calling tmux or starting any goroutines.
func TestHelpersSanity(t *testing.T) {
	c := newTestCoordinator(t)
	assert.NotNil(t, c.config)
	assert.NotNil(t, c.collapsedGroups)
	assert.NotNil(t, c.prevPaneBusy)
	assert.Equal(t, "test-session", c.sessionID)
	assert.Equal(t, 2, len(c.config.Groups))
	assert.Equal(t, 30, c.globalWidth)

	// All maps should be non-nil
	assert.NotNil(t, c.windows)
	assert.NotNil(t, c.grouped)
	assert.NotNil(t, c.windowVisualPos)
	assert.NotNil(t, c.cwdColors)
	assert.NotNil(t, c.clientWidths)
	assert.NotNil(t, c.prevPaneTitle)
	assert.NotNil(t, c.hookPaneActive)
	assert.NotNil(t, c.hookPaneBusyIdleAt)
	assert.NotNil(t, c.aiBellUntil)
	assert.NotNil(t, c.pendingMenus)
	assert.NotNil(t, c.lastWindowSelect)
	assert.NotNil(t, c.lastWindowByClient)
	assert.NotNil(t, c.lastPaneMenuOpen)

	// Config fields
	assert.Equal(t, "TABBY", c.config.Sidebar.Header.Text)
	assert.Equal(t, 3, c.config.Sidebar.Header.Height)
	assert.Equal(t, "●", c.config.Indicators.Activity.Icon)
	assert.Equal(t, "◆", c.config.Indicators.Bell.Icon)
	assert.Equal(t, 6, len(c.config.Sidebar.Colors.ActiveIndicatorFrames))

	// testWindow helper
	w := testWindow("MyWin", true, "bash", "vim")
	assert.Equal(t, "MyWin", w.Name)
	assert.Equal(t, "@MyWin", w.ID)
	assert.True(t, w.Active)
	assert.Equal(t, 2, len(w.Panes))
	assert.Equal(t, "bash", w.Panes[0].Command)
	assert.Equal(t, "vim", w.Panes[1].Command)
	assert.True(t, w.Panes[0].Active)
	assert.False(t, w.Panes[1].Active)

	// testGroup helper
	g := testGroup("Dev", `^Dev\|`, "#3498db")
	assert.Equal(t, "Dev", g.Name)
	assert.Equal(t, `^Dev\|`, g.Pattern)
	assert.Equal(t, "#3498db", g.Theme.Bg)

	// testConfig groups
	cfg := testConfig()
	assert.Equal(t, "TestGroup", cfg.Groups[0].Name)
	assert.Equal(t, "Default", cfg.Groups[1].Name)
}
