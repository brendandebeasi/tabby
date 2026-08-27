package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func parkTestCoordinator() *Coordinator {
	c := &Coordinator{}
	c.windows = []tmux.Window{
		{ID: "@1", Index: 1},
		{ID: "@2", Index: 2},
		{ID: "@3", Index: 3},
		{ID: "@4", Index: 4},
	}
	return c
}

func TestParkDestinationPrefersUntakenLRU(t *testing.T) {
	c := parkTestCoordinator()
	// MRU-first history: @2 most recent, @4 oldest.
	c.windowHistory = []string{"@2", "@3", "@4"}

	// @2 taken by another session, contested=@1. Free windows are @3 and
	// @4; the least-recently-used of them is @4.
	got := c.parkDestinationLocked(parkSessionInfo{}, "@1", map[string]bool{"@1": true, "@2": true})
	if got != "@4" {
		t.Fatalf("want @4 (oldest free), got %q", got)
	}
}

func TestParkDestinationNeverVisitedWindow(t *testing.T) {
	c := parkTestCoordinator()
	c.windowHistory = []string{"@2", "@3"} // @4 never visited

	// Both visited windows taken or contested: the never-visited @4 wins
	// over re-parking onto someone's current window.
	got := c.parkDestinationLocked(parkSessionInfo{}, "@1", map[string]bool{"@1": true, "@2": true, "@3": true})
	if got != "@4" {
		t.Fatalf("want @4 (never visited, untaken), got %q", got)
	}
}

func TestParkDestinationStarvedFallback(t *testing.T) {
	c := parkTestCoordinator()
	c.windowHistory = []string{"@2", "@3", "@4"}

	// Every other window is taken: fall back to any alive non-contested
	// window rather than leaving the session on the active one.
	got := c.parkDestinationLocked(parkSessionInfo{}, "@1", map[string]bool{"@1": true, "@2": true, "@3": true, "@4": true})
	if got == "" || got == "@1" {
		t.Fatalf("starved fallback should return an alive non-contested window, got %q", got)
	}
}

func TestParkDestinationNoAliveWindows(t *testing.T) {
	c := &Coordinator{}
	c.windows = []tmux.Window{{ID: "@1", Index: 1}}

	got := c.parkDestinationLocked(parkSessionInfo{}, "@1", map[string]bool{"@1": true})
	if got != "" {
		t.Fatalf("single-window group has no destination, got %q", got)
	}
}
