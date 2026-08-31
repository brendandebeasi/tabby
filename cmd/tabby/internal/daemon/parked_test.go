package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

// A window minimized on one grouped session is parked by THAT session's daemon,
// which is usually not the daemon rendering the sidebar you are looking at — a
// peer's park bumps nothing in our process, so generation-only invalidation left
// the peer serving a pre-minimize list forever and the Minimized section simply
// never appeared on the windows it drew.
func TestParkedCacheUsable_AgesOutForPeerParks(t *testing.T) {
	now := time.Now()

	assert.True(t, parkedCacheUsable(true, 3, 3, now.Add(-parkedCacheTTL/2), now),
		"a fresh, current-generation entry is served without re-querying tmux")
	assert.False(t, parkedCacheUsable(true, 3, 3, now.Add(-parkedCacheTTL-time.Millisecond), now),
		"past the TTL we re-query, which is how a peer daemon's park becomes visible")
	assert.False(t, parkedCacheUsable(false, 3, 3, now, now),
		"an invalidated entry is never served, however fresh")
	assert.False(t, parkedCacheUsable(true, 3, 4, now, now),
		"a local park moved the generation on, so the entry is stale")
}
