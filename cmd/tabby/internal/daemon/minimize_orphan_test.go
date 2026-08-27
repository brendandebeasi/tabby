package daemon

import "testing"

// Parking is a move-window out of the session, and tmux destroys a session whose
// last window leaves — with every grouped peer, since a group shares one window
// list. The dashboard is the worst case: it gathers every content pane and
// kill-windows the emptied origins, so it is always the last window AND the only
// record of the layout lives in the daemon that would die with the session.
func TestParkRefusalReason(t *testing.T) {
	cases := []struct {
		name        string
		count       int
		isDashboard bool
		wantRefused bool
	}{
		{"ordinary window with neighbors", 3, false, false},
		{"ordinary window, one neighbor", 2, false, false},
		{"only window", 1, false, true},
		{"no windows counted", 0, false, true},
		{"dashboard is never parkable", 4, true, true},
		{"dashboard alone", 1, true, true},
	}
	for _, c := range cases {
		got := parkRefusalReason(c.count, c.isDashboard)
		if refused := got != ""; refused != c.wantRefused {
			t.Errorf("%s: parkRefusalReason(%d, %v) = %q, refused=%v want %v",
				c.name, c.count, c.isDashboard, got, refused, c.wantRefused)
		}
	}
	// The dashboard check must not be reachable only through the count: a window
	// opened while the dashboard is up pushes the count past 1.
	if parkRefusalReason(9, true) == "" {
		t.Error("dashboard with many windows: want refusal, got none")
	}
}

// Grouped sessions share one window list, so a window minimized on the phone's
// peer must still show on the desktop's. Matching the origin SESSION ID alone is
// what made minimized windows look lost when the client moved between peers.
func TestMinWindowBelongsHere(t *testing.T) {
	live := map[string]string{
		"$0": "infras", // peer in my group
		"$1": "infras", // me
		"$7": "other",  // unrelated group
		"$9": "",       // ungrouped session
	}
	cases := []struct {
		name               string
		origin, group      string
		mySession, myGroup string
		want               bool
	}{
		{"own session id", "$1", "infras", "$1", "infras", true},
		{"grouped peer", "$0", "infras", "$1", "infras", true},
		{"foreign group", "$7", "other", "$1", "infras", false},
		{"ungrouped session sees only its own", "$0", "infras", "$9", "", false},
		{"ungrouped session sees its own", "$9", "", "$9", "", true},
		{"backfill: group looked up from live origin", "$0", "", "$1", "infras", true},
		{"backfill: dead origin, no stored group", "$404", "", "$1", "infras", false},
		{"dead origin keeps its group", "$404", "infras", "$1", "infras", true},
		{"empty origin", "", "", "$1", "infras", false},
	}
	for _, c := range cases {
		if got := minWindowBelongsHere(c.origin, c.group, c.mySession, c.myGroup, live); got != c.want {
			t.Errorf("%s: minWindowBelongsHere(%q, %q, %q, %q) = %v, want %v",
				c.name, c.origin, c.group, c.mySession, c.myGroup, got, c.want)
		}
	}
}

// The sweep adopts stranded windows by retagging them. Adopting one away from a
// live peer would make it vanish from the sidebar that peer is rendering, so a
// surviving group member counts as an owner just as the origin session does.
func TestMinRowHasLiveOwner(t *testing.T) {
	live := map[string]string{"$0": "infras", "$1": "infras", "$7": ""}
	cases := []struct {
		name          string
		origin, group string
		want          bool
	}{
		{"origin still live", "$0", "infras", true},
		{"origin dead, peer survives group", "$404", "infras", true},
		{"origin dead, group gone", "$404", "gone", false},
		{"origin dead, no group recorded", "$404", "", false},
		{"untagged", "", "", false},
		{"ungrouped session is its own owner", "$7", "", true},
	}
	for _, c := range cases {
		if got := minRowHasLiveOwner(c.origin, c.group, live); got != c.want {
			t.Errorf("%s: minRowHasLiveOwner(%q, %q) = %v, want %v", c.name, c.origin, c.group, got, c.want)
		}
	}
}
