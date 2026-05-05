package daemon

import (
	"io"
	"log"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestSetCoordinatorDebugLog(t *testing.T) {
	orig := coordinatorDebugLog
	defer func() { coordinatorDebugLog = orig }()
	logger := log.New(io.Discard, "[test] ", 0)
	SetCoordinatorDebugLog(logger)
	assert.Equal(t, logger, coordinatorDebugLog)
}

func TestStopDeadlockWatchdog(t *testing.T) {
	deadlockWatchdog = true
	StopDeadlockWatchdog()
	assert.False(t, deadlockWatchdog)
}

func TestRecordHeartbeat(t *testing.T) {
	recordHeartbeat()
	heartbeatMu.Lock()
	ts := lastHeartbeat
	heartbeatMu.Unlock()
	assert.Greater(t, ts, int64(0))
}

func TestWindowTargetForIndex_WithSession(t *testing.T) {
	c := newTestCoordinator(t)
	c.sessionID = "test-session"
	assert.Equal(t, "test-session:3", c.windowTargetForIndex(3))
}

func TestWindowTargetForIndex_WithoutSession(t *testing.T) {
	c := newTestCoordinator(t)
	c.sessionID = ""
	assert.Equal(t, ":5", c.windowTargetForIndex(5))
}

func TestGetWindowFirstPaneCWDByIndex_Found(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Panes: []tmux.Pane{{CurrentPath: "/home/user/projects"}}},
		{Index: 2, Panes: []tmux.Pane{{CurrentPath: "/tmp/work"}}},
	}
	assert.Equal(t, "/home/user/projects", c.getWindowFirstPaneCWDByIndex(1))
	assert.Equal(t, "/tmp/work", c.getWindowFirstPaneCWDByIndex(2))
}

func TestGetWindowFirstPaneCWDByIndex_NotFound(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{Index: 1, Panes: []tmux.Pane{{CurrentPath: "/tmp"}}}}
	assert.Equal(t, "", c.getWindowFirstPaneCWDByIndex(99))
}

func TestGetWindowFirstPaneCWDByIndex_Empty(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "", c.getWindowFirstPaneCWDByIndex(1))
}

func TestGetActiveWindowFirstPaneCWD_Found(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Active: false, Panes: []tmux.Pane{{CurrentPath: "/inactive"}}},
		{Index: 2, Active: true, Panes: []tmux.Pane{{CurrentPath: "/active/work"}}},
	}
	assert.Equal(t, "/active/work", c.getActiveWindowFirstPaneCWD())
}

func TestGetActiveWindowFirstPaneCWD_NoneActive(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Active: false, Panes: []tmux.Pane{{CurrentPath: "/inactive"}}},
	}
	assert.Equal(t, "", c.getActiveWindowFirstPaneCWD())
}

func TestResolveWindowCWD_UsesWindowCWD(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Active: false, Panes: []tmux.Pane{{CurrentPath: "/specific"}}},
		{Index: 2, Active: true, Panes: []tmux.Pane{{CurrentPath: "/active"}}},
	}
	assert.Equal(t, "/specific", c.resolveWindowCWD(1))
}

func TestResolveWindowCWD_FallsBackToActive(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Active: true, Panes: []tmux.Pane{{CurrentPath: "/active"}}},
	}
	assert.Equal(t, "/active", c.resolveWindowCWD(99))
}

func TestShortenPath_Root(t *testing.T) {
	assert.Equal(t, "/", shortenPath("/", "/home/user"))
}

func TestShortenPath_HomeDir(t *testing.T) {
	assert.Equal(t, "~", shortenPath("/home/user", "/home/user"))
}

func TestShortenPath_Subdir(t *testing.T) {
	assert.Equal(t, "projects", shortenPath("/home/user/projects", "/home/user"))
}

func TestShortenPath_DeepPath(t *testing.T) {
	assert.Equal(t, "tabby", shortenPath("/home/user/git/tabby", "/home/user"))
}

func TestSyncWindowNames_EmptyWindows(t *testing.T) {
	c := newTestCoordinator(t)
	pending := c.syncWindowNames()
	assert.Nil(t, pending)
}

func TestSyncWindowNames_LockedWindowSkipped(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "locked", NameLocked: true, Panes: []tmux.Pane{{CurrentPath: "/new/path"}}},
	}
	pending := c.syncWindowNames()
	assert.Nil(t, pending)
}

func TestSyncWindowNames_NoPanesSkipped(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "nopanes"},
	}
	pending := c.syncWindowNames()
	assert.Nil(t, pending)
}

func TestSyncWindowNames_NoPanePathsSkipped(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "nopaths", Panes: []tmux.Pane{{CurrentPath: ""}}},
	}
	pending := c.syncWindowNames()
	assert.Nil(t, pending)
}

func TestSyncWindowNames_SameNameNoRename(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "tabby", Panes: []tmux.Pane{{CurrentPath: "/home/user/tabby"}}},
	}
	pending := c.syncWindowNames()
	assert.Nil(t, pending)
}

func TestSyncWindowNames_DifferentNameTriggersRename(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "old-name", Panes: []tmux.Pane{{CurrentPath: "/home/user/tabby"}}},
	}
	pending := c.syncWindowNames()
	assert.Len(t, pending, 1)
	if len(pending) > 0 {
		assert.Equal(t, "@1", pending[0].windowID)
		assert.Equal(t, "tabby", pending[0].desiredName)
	}
}

func TestSyncWindowNames_MultiplePanesDeduplicated(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{
		ID:   "@1",
		Name: "old",
		Panes: []tmux.Pane{
			{CurrentPath: "/home/user/tabby"},
			{CurrentPath: "/home/user/tabby"},
		},
	}}
	pending := c.syncWindowNames()
	assert.Len(t, pending, 1)
	if len(pending) > 0 {
		assert.Equal(t, "tabby", pending[0].desiredName)
	}
}

func TestSyncWindowIndices_EmptyGroupedReturnsNil(t *testing.T) {
	c := newTestCoordinator(t)
	pending := c.syncWindowIndices()
	assert.Nil(t, pending)
}

func TestSyncWindowIndices_AllMatchReturnsNil(t *testing.T) {
	c := newTestCoordinator(t)
	c.grouped = []grouping.GroupedWindows{{
		Name: "Default",
		Windows: []tmux.Window{
			{ID: "@1", Index: 1},
			{ID: "@2", Index: 2},
		},
	}}
	c.windowVisualPos = map[string]int{"@1": 1, "@2": 2}
	pending := c.syncWindowIndices()
	assert.Nil(t, pending)
}

func TestSyncWindowIndices_MismatchReturnsPending(t *testing.T) {
	c := newTestCoordinator(t)
	c.grouped = []grouping.GroupedWindows{{
		Name: "Default",
		Windows: []tmux.Window{
			{ID: "@1", Index: 2},
			{ID: "@2", Index: 1},
		},
	}}
	c.windowVisualPos = map[string]int{"@1": 1, "@2": 2}
	pending := c.syncWindowIndices()
	assert.NotNil(t, pending)
	assert.Greater(t, len(pending), 0)
}

func TestBuildPaneHeaderColorArgs_EmptyGrouped(t *testing.T) {
	c := newTestCoordinator(t)
	args := c.buildPaneHeaderColorArgs()
	assert.Empty(t, args)
}

func TestBuildPaneHeaderColorArgs_OneWindowNoAutoBorder(t *testing.T) {
	c := newTestCoordinator(t)
	c.grouped = []grouping.GroupedWindows{{
		Name:  "Default",
		Theme: config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1"},
		Windows: []tmux.Window{
			{ID: "@1", Index: 1, Name: "bash"},
		},
	}}
	args := c.buildPaneHeaderColorArgs()
	assert.NotEmpty(t, args)
	found := false
	for _, arg := range args {
		if arg == "@tabby_pane_active" {
			found = true
			break
		}
	}
	assert.True(t, found, "should contain @tabby_pane_active option")
}

func TestBuildPaneHeaderColorArgs_AutoBorderEnabled(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.AutoBorder = true
	c.grouped = []grouping.GroupedWindows{{
		Theme: config.Theme{Bg: "#3498db", Fg: "#ffffff"},
		Windows: []tmux.Window{
			{ID: "@1", Index: 1, Name: "bash", Panes: []tmux.Pane{
				{ID: "%1", Command: "bash"},
			}},
		},
	}}
	args := c.buildPaneHeaderColorArgs()
	found := false
	for _, arg := range args {
		if arg == "pane-active-border-style" {
			found = true
			break
		}
	}
	assert.True(t, found, "should contain pane-active-border-style")
}

func TestBuildPaneHeaderColorArgs_ShellIntegrationIcon(t *testing.T) {
	c := newTestCoordinator(t)
	enabled := true
	c.config.Prompt.ShellIntegration = &enabled
	c.grouped = []grouping.GroupedWindows{{
		Theme:   config.Theme{Bg: "#2c3e50", Icon: "★"},
		Windows: []tmux.Window{{ID: "@1", Index: 1}},
	}}
	args := c.buildPaneHeaderColorArgs()
	found := false
	for _, arg := range args {
		if arg == "@tabby_prompt_icon" {
			found = true
			break
		}
	}
	assert.True(t, found, "should contain @tabby_prompt_icon")
}

func TestGetWindowsHash_Empty(t *testing.T) {
	c := newTestCoordinator(t)
	hash := c.GetWindowsHash()
	assert.Equal(t, "0", hash)
}

func TestGetWindowsHash_WithWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{ID: "@1", Name: "bash", Active: true},
		{ID: "@2", Name: "vim", Active: false},
	}
	hash := c.GetWindowsHash()
	assert.Contains(t, hash, "@1")
	assert.Contains(t, hash, "@2")
}

func TestGetWindowsHash_ChangesWithState(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{ID: "@1", Active: false}}
	h1 := c.GetWindowsHash()
	c.windows[0].Active = true
	h2 := c.GetWindowsHash()
	assert.NotEqual(t, h1, h2)
}

func TestTreeCPU_NilTree(t *testing.T) {
	var pt *processTree
	assert.Equal(t, 0.0, pt.treeCPU(1))
}

func TestTreeCPU_ZeroPID(t *testing.T) {
	pt := &processTree{children: make(map[int][]int), cpuByPID: make(map[int]float64)}
	assert.Equal(t, 0.0, pt.treeCPU(0))
}

func TestTreeCPU_NegativePID(t *testing.T) {
	pt := &processTree{children: make(map[int][]int), cpuByPID: make(map[int]float64)}
	assert.Equal(t, 0.0, pt.treeCPU(-1))
}

func TestTreeCPU_SingleProcess(t *testing.T) {
	pt := &processTree{
		children: make(map[int][]int),
		cpuByPID: map[int]float64{100: 5.5},
	}
	assert.Equal(t, 5.5, pt.treeCPU(100))
}

func TestTreeCPU_WithChildren(t *testing.T) {
	pt := &processTree{
		children: map[int][]int{100: {101, 102}},
		cpuByPID: map[int]float64{100: 1.0, 101: 2.0, 102: 3.0},
	}
	assert.Equal(t, 6.0, pt.treeCPU(100))
}

func TestTreeCPU_UnknownPID(t *testing.T) {
	pt := &processTree{
		children: make(map[int][]int),
		cpuByPID: map[int]float64{100: 5.0},
	}
	assert.Equal(t, 0.0, pt.treeCPU(999))
}

func TestProcessAIToolStates_EmptyWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	pending := c.processAIToolStates(nil)
	assert.Empty(t, pending)
}

func TestProcessAIToolStates_NoAIPanes_NoBusyInput(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Active: true, Panes: []tmux.Pane{
			{ID: "%1", Command: "bash"},
		}},
	}
	pending := c.processAIToolStates(nil)
	assert.Empty(t, pending)
}

func TestProcessAIToolStates_ClearsStaleBusy(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Busy: true, Panes: []tmux.Pane{
			{ID: "%1", Command: "bash"},
		}},
	}
	pending := c.processAIToolStates(nil)
	found := false
	for _, p := range pending {
		if p.key == "@tabby_busy" && p.unset {
			found = true
			break
		}
	}
	assert.True(t, found, "should unset stale @tabby_busy")
}

func TestProcessAIToolStates_ClearsStaleInput(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Input: true, Panes: []tmux.Pane{
			{ID: "%1", Command: "bash"},
		}},
	}
	pending := c.processAIToolStates(nil)
	found := false
	for _, p := range pending {
		if p.key == "@tabby_input" && p.unset {
			found = true
			break
		}
	}
	assert.True(t, found, "should unset stale @tabby_input")
}

func TestProcessAIToolStates_PrevBusyTriggersBell(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Panes: []tmux.Pane{
			{ID: "%1", Command: "bash"},
		}},
	}
	c.prevPaneBusy["%1"] = true
	pending := c.processAIToolStates(nil)
	found := false
	for _, p := range pending {
		if p.key == "@tabby_bell" && p.value == "1" {
			found = true
			break
		}
	}
	assert.True(t, found, "prev busy pane should trigger bell")
}

func TestProcessAIToolStates_WithPreloadedProcessTree(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = true
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Panes: []tmux.Pane{
			{ID: "%1", Command: "bash"},
		}},
	}
	pt := &processTree{
		children: make(map[int][]int),
		cpuByPID: make(map[int]float64),
	}
	pending := c.processAIToolStates(pt)
	assert.NotNil(t, c.cachedProcessTree)
	assert.Empty(t, pending)
}

func TestProcessAIToolStates_MultipleWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Indicators.Busy.Enabled = false
	c.config.Indicators.Input.Enabled = false
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1, Busy: true, Panes: []tmux.Pane{{ID: "%1", Command: "bash"}}},
		{ID: "@2", Index: 2, Input: true, Panes: []tmux.Pane{{ID: "%2", Command: "zsh"}}},
	}
	pending := c.processAIToolStates(nil)
	assert.GreaterOrEqual(t, len(pending), 2)
}

func TestUpdatePetState_PetDisabled(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Widgets.Pet.Enabled = false
	changed := c.UpdatePetState()
	assert.False(t, changed)
}

func TestUpdatePetState_PetEnabledNoWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Widgets.Pet.Enabled = true
	c.lastWidth = 30
	_ = c.UpdatePetState()
}

func TestTogglePaneCollapse_EmptyTarget(t *testing.T) {
	c := newTestCoordinator(t)
	result := c.togglePaneCollapse(":")
	assert.False(t, result)
}

func TestTogglePaneCollapse_NonNumericTarget(t *testing.T) {
	c := newTestCoordinator(t)
	result := c.togglePaneCollapse(":notanumber")
	assert.False(t, result)
}

func TestTogglePaneCollapse_NoMatchingWindow(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{Index: 1, Name: "test"}}
	result := c.togglePaneCollapse(":99")
	assert.False(t, result)
}

func TestTogglePaneCollapse_MatchingWindow_NotCollapsed(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		{Index: 1, Name: "test", Panes: []tmux.Pane{{ID: "%1"}}},
	}
	result := c.togglePaneCollapse(":1")
	_ = result
}

func TestCollapseWindowPanes_EmptyPanes(t *testing.T) {
	c := newTestCoordinator(t)
	win := &tmux.Window{Index: 1, Name: "test", Panes: []tmux.Pane{}}
	c.collapseWindowPanes(":1", win)
}

func TestCollapseWindowPanes_WithPanes(t *testing.T) {
	c := newTestCoordinator(t)
	win := &tmux.Window{
		Index: 1, Name: "test",
		Panes: []tmux.Pane{{ID: "%1"}, {ID: "%2"}},
	}
	c.collapseWindowPanes(":1", win)
}

func TestExpandWindowPanes_EmptyPanes(t *testing.T) {
	c := newTestCoordinator(t)
	win := &tmux.Window{Index: 1, Name: "test", Panes: []tmux.Pane{}}
	c.expandWindowPanes(":1", win)
}

func TestExpandWindowPanes_WithPanes(t *testing.T) {
	c := newTestCoordinator(t)
	win := &tmux.Window{
		Index: 1, Name: "test",
		Panes: []tmux.Pane{{ID: "%1"}, {ID: "%2"}},
	}
	c.expandWindowPanes(":1", win)
}

func TestNewCoordinator_ReturnsNonNil(t *testing.T) {
	c := NewCoordinator("test-session")
	assert.NotNil(t, c)
}

func TestGetPaneHeaderInactiveBg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.InactiveBg = ""
	c.theme = nil
	got := c.getPaneHeaderInactiveBg()
	_ = got
}

func TestGetPaneHeaderInactiveFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.InactiveFg = ""
	c.theme = nil
	got := c.getPaneHeaderInactiveFg()
	_ = got
}

func TestGetCommandFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.CommandFg = ""
	c.theme = nil
	got := c.getCommandFg()
	_ = got
}

func TestGetButtonFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.ButtonFg = ""
	c.theme = nil
	got := c.getButtonFg()
	_ = got
}

func TestGetBorderFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.BorderFg = ""
	c.theme = nil
	got := c.getBorderFg()
	_ = got
}

func TestGetHandleColor_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.HandleColor = ""
	c.theme = nil
	got := c.getHandleColor()
	_ = got
}

func TestGetPromptFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Prompt.Fg = ""
	c.theme = nil
	got := c.getPromptFg()
	_ = got
}

func TestGetPromptBg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Prompt.Bg = ""
	c.theme = nil
	got := c.getPromptBg()
	_ = got
}

func TestGetDividerFg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.DividerFg = ""
	c.theme = nil
	got := c.getDividerFg()
	_ = got
}

func TestGetTerminalBg_DefaultFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.TerminalBg = ""
	c.theme = nil
	got := c.GetTerminalBg()
	_ = got
}

func TestSetWindowColor_NotFound(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{Index: 1, Name: "test"}}
	c.setWindowColor(99, "#ff0000")
}

func TestSetWindowIcon_NotFound(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{{Index: 1, Name: "test"}}
	c.setWindowIcon(99, "🔥")
}

func TestGroupedWindowsFromWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.windows = []tmux.Window{
		testWindow("TestGroup|win1", true, "zsh"),
		testWindow("Default|win2", false, "vim"),
	}
	grouped := grouping.GroupWindows(c.windows, c.config.Groups)
	assert.NotNil(t, grouped)
}
