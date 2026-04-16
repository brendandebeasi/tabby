package main

import (
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestGetWindows_InitiallyEmpty(t *testing.T) {
	c := newTestCoordinator(t)
	wins := c.GetWindows()
	assert.Empty(t, wins)
}

func TestGetWindows_ReturnsCopy(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()

	got := c.GetWindows()
	assert.Equal(t, 1, len(got))

	got[0].Name = "mutated"
	assert.Equal(t, "W1", c.GetWindows()[0].Name, "modifying returned slice must not affect internal state")
}

func TestGetWindows_MultipleWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("alpha", true, "bash"),
		testWindow("beta", false, "vim", "htop"),
	}
	c.stateMu.Unlock()

	got := c.GetWindows()
	assert.Equal(t, 2, len(got))
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "beta", got[1].Name)
	assert.Equal(t, 2, len(got[1].Panes))
}

func TestGetWindowsHash_ConsistentForSameState(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()

	h1 := c.GetWindowsHash()
	h2 := c.GetWindowsHash()
	assert.Equal(t, h1, h2)
}

func TestGetWindowsHash_ChangesWithWindowState(t *testing.T) {
	c := newTestCoordinator(t)

	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()
	h1 := c.GetWindowsHash()

	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("W1", true, "bash"),
		testWindow("W2", false, "vim"),
	}
	c.stateMu.Unlock()
	h2 := c.GetWindowsHash()

	assert.NotEqual(t, h1, h2)
}

func TestGetWindowsHash_EmptyIsStable(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, c.GetWindowsHash(), c.GetWindowsHash())
}

func TestIncrementSpinner_ReturnsFalseWhenNoActivity(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("idle", false, "bash")}
	c.stateMu.Unlock()

	before := c.spinnerFrame
	result := c.IncrementSpinner()
	assert.False(t, result)
	assert.Equal(t, before+1, c.spinnerFrame)
}

func TestIncrementSpinner_ReturnsTrueWhenWindowBusy(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("busy", true, "make")
	w.Busy = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	assert.True(t, c.IncrementSpinner())
}

func TestIncrementSpinner_ReturnsTrueWhenWindowHasBell(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("bell", false, "bash")
	w.Bell = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	assert.True(t, c.IncrementSpinner())
}

func TestIncrementSpinner_ReturnsTrueWhenPaneAIBusy(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("ai", true, "claude")
	w.Panes[0].AIBusy = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	assert.True(t, c.IncrementSpinner())
}

func TestIncrementSpinner_ReturnsTrueWhenPaneAIInput(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("ai", true, "claude")
	w.Panes[0].AIInput = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	assert.True(t, c.IncrementSpinner())
}

func TestIncrementSpinner_IncrementsMonotonically(t *testing.T) {
	c := newTestCoordinator(t)
	for i := 1; i <= 5; i++ {
		c.IncrementSpinner()
		assert.Equal(t, i, c.spinnerFrame)
	}
}

func TestGetCWDColorMapping_MissingReturnsNotFound(t *testing.T) {
	c := newTestCoordinator(t)
	_, ok := c.getCWDColorMapping("/some/path")
	assert.False(t, ok)
}

func TestGetCWDColorMapping_EmptyCWDReturnsFalse(t *testing.T) {
	c := newTestCoordinator(t)
	_, ok := c.getCWDColorMapping("")
	assert.False(t, ok)
}

func TestSetAndGetCWDColor(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/home/user/project", "#3498db")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "#3498db", m.Color)
}

func TestSetCWDColor_DeletesEntryWhenBothEmpty(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/project", "#ff0000")
	_, ok := c.getCWDColorMapping("/tmp/project")
	assert.True(t, ok)

	c.setCWDColor("/tmp/project", "")
	_, ok = c.getCWDColorMapping("/tmp/project")
	assert.False(t, ok, "entry should be removed when color and icon are both empty")
}

func TestSetAndGetCWDIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDIcon("/home/user/project", "🚀")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "🚀", m.Icon)
}

func TestSetCWDIcon_PreservesExistingColor(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/x", "#aabbcc")
	c.setCWDIcon("/tmp/x", "🌟")
	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok)
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "🌟", m.Icon)
}

func TestComputeVisualPositions_EmptyGrouped(t *testing.T) {
	c := newTestCoordinator(t)
	c.computeVisualPositions()
	assert.Empty(t, c.windowVisualPos)
}

func TestComputeVisualPositions_SingleGroup(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 1
	c.grouped = []grouping.GroupedWindows{
		{
			Name:    "Dev",
			Windows: []tmux.Window{testWindow("w1", true), testWindow("w2", false)},
		},
	}
	c.computeVisualPositions()

	assert.Equal(t, 1, c.windowVisualPos["@w1"])
	assert.Equal(t, 2, c.windowVisualPos["@w2"])
}

func TestComputeVisualPositions_MultipleGroups(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 0
	c.grouped = []grouping.GroupedWindows{
		{Name: "Group A", Windows: []tmux.Window{testWindow("w1", true)}},
		{Name: "Group B", Windows: []tmux.Window{testWindow("w2", false), testWindow("w3", false)}},
	}
	c.computeVisualPositions()

	assert.Equal(t, 0, c.windowVisualPos["@w1"])
	assert.Equal(t, 1, c.windowVisualPos["@w2"])
	assert.Equal(t, 2, c.windowVisualPos["@w3"])
}

func TestComputeVisualPositions_RebuildsFromScratch(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 0
	c.windowVisualPos["@old"] = 99

	c.grouped = []grouping.GroupedWindows{
		{Name: "G", Windows: []tmux.Window{testWindow("@new", true)}},
	}
	c.computeVisualPositions()

	_, hasOld := c.windowVisualPos["@old"]
	assert.False(t, hasOld, "stale entries must be cleared on recompute")
	assert.Equal(t, 0, c.windowVisualPos["@new"])
}

func TestGetConfig_ReturnsConfig(t *testing.T) {
	c := newTestCoordinator(t)
	cfg := c.GetConfig()
	assert.Same(t, c.config, cfg)
	assert.Equal(t, 2, len(cfg.Groups))
}

func TestNewWindowStatusLifecycle(t *testing.T) {
	c := newTestCoordinator(t)

	initial := c.NewWindowStatus()
	assert.Equal(t, "none", initial.State)
	assert.Empty(t, initial.WindowID)

	c.SetNewWindowInFlight("Dev", "/tmp/project")
	inFlight := c.NewWindowStatus()
	assert.Equal(t, "inFlight", inFlight.State)
	assert.Equal(t, "Dev", inFlight.Group)
	assert.Equal(t, "/tmp/project", inFlight.WorkingDir)
	assert.NotZero(t, inFlight.Created)

	c.SetNewWindowReady("@123")
	ready := c.NewWindowStatus()
	assert.Equal(t, "ready", ready.State)
	assert.Equal(t, "@123", ready.WindowID)
	assert.Equal(t, "Dev", ready.Group)
	assert.Equal(t, "/tmp/project", ready.WorkingDir)

	c.ClearNewWindowStatus()
	cleared := c.NewWindowStatus()
	assert.Equal(t, "none", cleared.State)
	assert.Empty(t, cleared.WindowID)
	assert.Empty(t, cleared.Group)
	assert.Empty(t, cleared.WorkingDir)
}

func TestWindowTransitionLifecycle(t *testing.T) {
	c := newTestCoordinator(t)

	assert.False(t, c.IsTransitionInProgress())

	err := c.BeginTransition("@2", "switch_window", "test")
	assert.NoError(t, err)
	assert.True(t, c.IsTransitionInProgress())

	c.stateMu.RLock()
	transition := c.windowTransition
	c.stateMu.RUnlock()

	assert.Equal(t, "@2", transition.TargetWindowID)
	assert.Equal(t, "switch_window", transition.Reason)
	assert.Equal(t, "test", transition.Source)
	assert.False(t, transition.StartedAt.IsZero())
	assert.WithinDuration(t, time.Now(), transition.StartedAt, 2*time.Second)

	c.CompleteTransition()
	assert.False(t, c.IsTransitionInProgress())
}

func TestWindowTransitionRejectsBeginWhileInProgress(t *testing.T) {
	c := newTestCoordinator(t)

	err := c.BeginTransition("@2", "switch_window", "test")
	assert.NoError(t, err)

	err = c.BeginTransition("@3", "switch_window", "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transition already in progress")
	assert.Contains(t, err.Error(), "target=@2")
}
