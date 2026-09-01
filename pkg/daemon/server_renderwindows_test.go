package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// sidebarClient is the shape RenderWindowsOnly filters on: a sidebar target
// pinned to one tmux window.
func sidebarClient(windowID string) *ClientInfo {
	return &ClientInfo{
		Width:  80,
		Height: 24,
		Target: RenderTarget{Kind: TargetSidebar, WindowID: windowID},
	}
}

// renderRecorder installs an OnRenderNeeded that counts calls per client, and
// returns a snapshot function that waits out the render batch timer first.
func renderRecorder(s *Server) func() map[string]int {
	var mu sync.Mutex
	calls := make(map[string]int)
	s.OnRenderNeeded = func(clientID string, _, _ int) *RenderPayload {
		mu.Lock()
		calls[clientID]++
		mu.Unlock()
		return nil
	}
	return func() map[string]int {
		time.Sleep(s.renderBatchDelay + 20*time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		snapshot := make(map[string]int, len(calls))
		for k, v := range calls {
			snapshot[k] = v
		}
		return snapshot
	}
}

func TestRenderWindowsOnly_SendsToEveryListedWindow(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar-a"] = sidebarClient("@1")
	s.clients["sidebar-b"] = sidebarClient("@2")
	s.clients["sidebar-c"] = sidebarClient("@3")
	snapshot := renderRecorder(s)

	// Two attached terminals sitting on different windows: both must animate.
	s.RenderWindowsOnly("@1", "@3")

	calls := snapshot()
	assert.Equal(t, 1, calls["sidebar-a"])
	assert.Equal(t, 1, calls["sidebar-c"])
	assert.Zero(t, calls["sidebar-b"], "unwatched window should stay idle")
}

func TestRenderWindowsOnly_SkipsNonSidebarTargets(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar"] = sidebarClient("@1")
	// Headers are keyed per pane, not per window, so a window-wide animation
	// frame has nothing to say to them.
	s.clients["header"] = &ClientInfo{
		Width:  80,
		Height: 1,
		Target: RenderTarget{Kind: TargetPaneHeader, WindowID: "@1", PaneID: "%1"},
	}
	snapshot := renderRecorder(s)

	s.RenderWindowsOnly("@1")

	calls := snapshot()
	assert.Equal(t, 1, calls["sidebar"])
	assert.Zero(t, calls["header"])
}

func TestRenderWindowsOnly_DuplicateWindowSendsOnce(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar"] = sidebarClient("@1")
	snapshot := renderRecorder(s)

	// Two attached sessions can be looking at the same window; the caller does
	// not have to dedupe for us.
	s.RenderWindowsOnly("@1", "@1")

	assert.Equal(t, 1, snapshot()["sidebar"])
}

func TestRenderWindowsOnly_NoWindowsIsNoOp(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar"] = sidebarClient("@1")
	snapshot := renderRecorder(s)

	assert.NotPanics(t, func() { s.RenderWindowsOnly() })

	assert.Empty(t, snapshot())
}

func TestRenderWindowsOnly_UnknownWindowIsNoOp(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar"] = sidebarClient("@1")
	snapshot := renderRecorder(s)

	s.RenderWindowsOnly("@99")

	assert.Empty(t, snapshot())
}

func TestRenderActiveWindowOnly_StillTargetsThatOneWindow(t *testing.T) {
	s := newTestServer(t)
	s.clients["sidebar-a"] = sidebarClient("@1")
	s.clients["sidebar-b"] = sidebarClient("@2")
	snapshot := renderRecorder(s)

	s.RenderActiveWindowOnly("@2")

	calls := snapshot()
	assert.Equal(t, 1, calls["sidebar-b"])
	assert.Zero(t, calls["sidebar-a"])
}
