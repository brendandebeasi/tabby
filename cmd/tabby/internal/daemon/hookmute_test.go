package daemon

import (
	"strings"
	"testing"
)

// isMutatingTmuxCommand decides whether a tmux invocation closes the hook gate.
// A false negative leaves the daemon amplifying its own mutations; a false
// positive mutes hooks the user is waiting on. set-option must stay out: the
// gate is itself a set-option, and matching it would re-enter this path.
func TestIsMutatingTmuxCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"move-window", []string{"move-window", "-s", "@1", "-t", "2"}, true},
		{"kill-window", []string{"kill-window", "-t", "@4"}, true},
		{"link-window", []string{"link-window", "-d", "-s", "@1", "-t", "x:9"}, true},
		{"verb after global flags", []string{"-L", "sock", "swap-window", "-s", "1"}, true},
		{"read is not mutating", []string{"display-message", "-p", "#{session_id}"}, false},
		{"list is not mutating", []string{"list-windows", "-a"}, false},
		{"set-option is excluded", []string{"set-option", "-g", "@tabby_mute", "1"}, false},
		{"show-options is excluded", []string{"show-options", "-gqv", "@tabby_mute"}, false},
		{"empty", nil, false},
		{"flags only", []string{"-g", "-q"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMutatingTmuxCommand(tc.args); got != tc.want {
				t.Errorf("isMutatingTmuxCommand(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// Muting a navigation verb breaks window switching: the daemon selects a window
// on the user's behalf, which closed the gate, which muted the
// after-select-window hook that runs `refresh-client -S` — leaving the client
// drawing the old window's contents inside the new window's border.
func TestNavigationVerbsAreNotMuted(t *testing.T) {
	for _, verb := range []string{"select-window", "select-pane", "next-window", "previous-window", "switch-client", "last-window"} {
		if isMutatingTmuxCommand([]string{verb, "-t", "@1"}) {
			t.Errorf("%s must not close the hook gate: it mutes the refresh the selection depends on", verb)
		}
	}
}

// The gate must carry THIS daemon's session id, not a boolean. tmux options are
// per-server while daemons are per-session, so a boolean muted every peer
// daemon's hooks too and left their window-list models stale. The cleared value
// has to be something no session id can equal, or clearing would permanently
// mute whichever session it collided with.
func TestHookMuteValueCarriesTheSessionID(t *testing.T) {
	prev := sessionID
	defer func() { sessionID = prev }()

	id := "$137"
	sessionID = &id
	if got := hookMuteValue(true); got != id {
		t.Errorf("hookMuteValue(true) = %q, want the daemon's session id %q", got, id)
	}
	off := hookMuteValue(false)
	if off == id {
		t.Fatalf("cleared value %q equals a session id: that session would stay muted", off)
	}
	if strings.HasPrefix(off, "$") {
		t.Errorf("cleared value %q looks like a session id (`$N`)", off)
	}
}

// A daemon that cannot name its own session cannot scope the gate. Engaging an
// unscoped mute there would silence every peer, so it must not engage at all —
// an unsuppressed burst is a cost, a silenced peer is a bug.
func TestNoSessionIDMeansNoMute(t *testing.T) {
	prev := sessionID
	defer func() { sessionID = prev; ClearHookMute() }()

	empty := ""
	sessionID = &empty
	ClearHookMute()

	noteTmuxMutation([]string{"kill-window", "-t", "@4"})

	hookMuteState.Lock()
	engaged := hookMuteState.engaged
	hookMuteState.Unlock()
	if engaged {
		t.Error("daemon with no session id engaged the gate; that mutes every peer daemon")
	}
}
