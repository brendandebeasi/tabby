package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestIsEffectivelyParked(t *testing.T) {
	cases := []struct {
		name string
		win  tmux.Window
		want bool
	}{
		{"not minimized", tmux.Window{Minimized: false, Panes: []tmux.Pane{{ID: "%5"}}}, false},
		{"parked synthetic %parked", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: "%parked"}}}, true},
		{"parked no panes", tmux.Window{Minimized: true}, true},
		{"parked empty pane id", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: ""}}}, true},
		{"surfaced real pane", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: "%12"}}}, false},
	}
	for _, c := range cases {
		if got := isEffectivelyParked(c.win); got != c.want {
			t.Errorf("%s: isEffectivelyParked = %v, want %v", c.name, got, c.want)
		}
	}
}

// The toggle-minimize keybinding resolves its target from the CURRENT window,
// which is never the parked window the user wants back. Guard the peek fallback
// that makes the binding a real toggle instead of a one-way park.
func TestCurrentPeekedWindow(t *testing.T) {
	c := &Coordinator{}
	if got := c.currentPeekedWindow(); got != "" {
		t.Errorf("no peek: got %q, want empty", got)
	}
	c.peekedWindowID = "@42"
	if got := c.currentPeekedWindow(); got != "@42" {
		t.Errorf("peeked: got %q, want @42", got)
	}
	c.clearPeekIf("@99")
	if got := c.currentPeekedWindow(); got != "@42" {
		t.Errorf("clearPeekIf on a different window must not clear: got %q", got)
	}
	c.clearPeekIf("@42")
	if got := c.currentPeekedWindow(); got != "" {
		t.Errorf("clearPeekIf on the peeked window: got %q, want empty", got)
	}
}
