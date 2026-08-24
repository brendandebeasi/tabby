package daemon

import (
	"testing"
	"time"
)

// A stuck "inFlight" used to be permanent: every consumer bails out on it, so a
// handshake whose second half never arrived left the daemon unable to follow
// focus until it was restarted.
func TestNewWindowStatusExpiresStaleInFlight(t *testing.T) {
	c := &Coordinator{}
	c.newWindowStatus = NewWindowStatus{
		State:   "inFlight",
		Group:   "work",
		Created: time.Now().Add(-loopNewWindowInFlightTimeout - time.Second),
	}

	if got := c.NewWindowStatus().State; got != "none" {
		t.Fatalf("stale inFlight should expire to none, got %q", got)
	}
	// The clear must be persistent, not just masked on the way out.
	if got := c.newWindowStatus.State; got != "none" {
		t.Fatalf("stored state should be cleared, got %q", got)
	}
}

func TestNewWindowStatusKeepsFreshInFlight(t *testing.T) {
	c := &Coordinator{}
	c.newWindowStatus = NewWindowStatus{
		State:   "inFlight",
		Created: time.Now(),
	}

	if got := c.NewWindowStatus().State; got != "inFlight" {
		t.Fatalf("fresh inFlight should survive, got %q", got)
	}
}

// Tests build this struct by hand without a timestamp; a zero Created carries
// no age to judge, so it must not read as infinitely old.
func TestNewWindowStatusKeepsUnstampedInFlight(t *testing.T) {
	c := &Coordinator{}
	c.newWindowStatus = NewWindowStatus{State: "inFlight"}

	if got := c.NewWindowStatus().State; got != "inFlight" {
		t.Fatalf("unstamped inFlight should survive, got %q", got)
	}
}

// "ready" has always carried its own 3s timeout, applied by the loop rather
// than here; NewWindowStatus must not second-guess it.
func TestNewWindowStatusLeavesStaleReadyAlone(t *testing.T) {
	c := &Coordinator{}
	c.newWindowStatus = NewWindowStatus{
		State:   "ready",
		Created: time.Now().Add(-time.Hour),
	}

	if got := c.NewWindowStatus().State; got != "ready" {
		t.Fatalf("ready is the loop's to expire, got %q", got)
	}
}

func TestNewWindowStatusDefaultsToNone(t *testing.T) {
	c := &Coordinator{}
	if got := c.NewWindowStatus().State; got != "none" {
		t.Fatalf("empty state should read as none, got %q", got)
	}
}
