package daemon

import "testing"

// The reported bug: open a new window from an existing one, then close it.
// Focus should return to the window it was opened from, not to tmux's
// adjacent-window default.
func TestPickPreviousWindow(t *testing.T) {
	tests := []struct {
		name     string
		history  []string
		existing map[string]bool
		want     string
	}{
		{
			name:     "closed window at head is skipped for the one behind it",
			history:  []string{"@1654", "@1596", "@1533"},
			existing: map[string]bool{"@1596": true, "@1533": true},
			want:     "@1596",
		},
		{
			name:     "new window never recorded leaves a stale head",
			history:  []string{"@1596", "@1533"},
			existing: map[string]bool{"@1596": true, "@1533": true},
			want:     "@1596",
		},
		{
			name:     "skips several dead windows",
			history:  []string{"@9", "@8", "@1533"},
			existing: map[string]bool{"@1533": true},
			want:     "@1533",
		},
		{
			name:     "no survivor yields empty",
			history:  []string{"@9", "@8"},
			existing: map[string]bool{"@1533": true},
			want:     "",
		},
		{
			name:     "empty history yields empty",
			history:  nil,
			existing: map[string]bool{"@1533": true},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickPreviousWindow(tt.history, tt.existing); got != tt.want {
				t.Errorf("pickPreviousWindow() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TrackWindowHistory must move a revisited window back to the head, so the
// most recently visited survivor is always chosen first.
func TestTrackWindowHistory_RestoreOrderAfterRevisit(t *testing.T) {
	c := newTestCoordinator(t)
	c.TrackWindowHistory("@1533")
	c.TrackWindowHistory("@1596")
	c.TrackWindowHistory("@1533")
	c.TrackWindowHistory("@1654")

	existing := map[string]bool{"@1533": true, "@1596": true}
	if got := pickPreviousWindow(c.GetWindowHistory(), existing); got != "@1533" {
		t.Errorf("after closing @1654, restored to %q, want @1533", got)
	}
}

// With two terminals attached to one session, each client needs its own back
// target. Observed: a window opened from @1596 on one client restored to
// @1533 -- a window the OTHER client had visited -- because both fed one
// shared stack.
func TestPerClientWindowHistory(t *testing.T) {
	c := newTestCoordinator(t)

	// Client A works in @1596, then opens @1662 from it.
	c.TrackWindowHistoryForClient("/dev/ttys016", "@1596")
	c.TrackWindowHistoryForClient("/dev/ttys016", "@1662")
	// Client B is off in @1533 the whole time.
	c.TrackWindowHistoryForClient("/dev/ttys000", "@1533")

	existing := map[string]bool{"@1596": true, "@1533": true}

	c.stateMu.RLock()
	histA := append([]string(nil), c.clientWindowHistory["/dev/ttys016"]...)
	histB := append([]string(nil), c.clientWindowHistory["/dev/ttys000"]...)
	shared := append([]string(nil), c.windowHistory...)
	c.stateMu.RUnlock()

	if got := pickPreviousWindow(histA, existing); got != "@1596" {
		t.Errorf("client A restored to %q, want @1596", got)
	}
	if got := pickPreviousWindow(histB, existing); got != "@1533" {
		t.Errorf("client B restored to %q, want @1533", got)
	}
	// The shared stack is the one that produced the bug: its head is client
	// B's window, which is why it is now only a fallback.
	if got := pickPreviousWindow(shared, existing); got != "@1533" {
		t.Errorf("shared stack head = %q, want @1533 (documents the old behavior)", got)
	}
}

func TestDistinctClientWidths(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int
	}{
		{"two differing clients", "185\n164\n", 2},
		{"same width twice", "164\n164\n", 1},
		{"single client", "164\n", 1},
		{"empty", "", 0},
		{"junk ignored", "164\n\nabc\n0\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := distinctClientWidths(tt.out); got != tt.want {
				t.Errorf("distinctClientWidths(%q) = %d, want %d", tt.out, got, tt.want)
			}
		})
	}
}
