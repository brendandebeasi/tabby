package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestWindowKnownAtBracketStart(t *testing.T) {
	windows := []tmux.Window{{ID: "@10"}, {ID: "@11"}, {ID: "@12"}}

	if !windowKnownAtBracketStart(windows, "@11") {
		t.Fatal("a window present in the pre-bracket list must count as known")
	}
	if windowKnownAtBracketStart(windows, "@99") {
		t.Fatal("a window created mid-bracket must not count as known")
	}
	// An empty id means tmux told us nothing; treating it as known keeps the
	// caller on the conservative restore path instead of adopting a blank.
	if !windowKnownAtBracketStart(windows, "") {
		t.Fatal("an empty window id must count as known")
	}
	if windowKnownAtBracketStart(nil, "@10") {
		t.Fatal("an empty pre-bracket list cannot vouch for any window")
	}
}

func TestNewWindowFlowActive(t *testing.T) {
	if newWindowFlowActive(nil) {
		t.Fatal("a nil coordinator has no new-window flow")
	}

	coord := &Coordinator{}
	if newWindowFlowActive(coord) {
		t.Fatalf("a fresh coordinator reports state %q, want no active flow", coord.NewWindowStatus().State)
	}

	for _, state := range []string{"inFlight", "ready"} {
		coord.newWindowStatus = NewWindowStatus{State: state}
		if !newWindowFlowActive(coord) {
			t.Fatalf("state %q must count as an active new-window flow", state)
		}
	}

	coord.newWindowStatus = NewWindowStatus{State: "none"}
	if newWindowFlowActive(coord) {
		t.Fatal("state \"none\" must not count as an active new-window flow")
	}
}
