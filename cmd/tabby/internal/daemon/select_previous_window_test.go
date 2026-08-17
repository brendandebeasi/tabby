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
