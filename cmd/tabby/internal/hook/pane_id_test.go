package hook

import (
	"slices"
	"testing"
)

func TestResolvePaneID(t *testing.T) {
	live := func(ids ...string) func(string) bool {
		return func(p string) bool { return slices.Contains(ids, p) }
	}
	const activePane = "%6"
	active := func() string { return activePane }

	tests := []struct {
		name   string
		env    string
		exists func(string) bool
		active func() string
		want   string
	}{
		{
			name:   "live TMUX_PANE wins over the active pane",
			env:    "%12",
			exists: live("%6", "%12"),
			active: active,
			want:   "%12",
		},
		{
			// The bug: a tmux server started from inside another tmux
			// leaks a dead pane id into every run-shell hook.
			name:   "stale TMUX_PANE falls back to the active pane",
			env:    "%35001",
			exists: live("%6", "%12"),
			active: active,
			want:   activePane,
		},
		{
			name:   "empty TMUX_PANE falls back to the active pane",
			env:    "",
			exists: live("%6"),
			active: active,
			want:   activePane,
		},
		{
			name:   "surrounding whitespace is trimmed",
			env:    " %12\n",
			exists: live("%12"),
			active: active,
			want:   "%12",
		},
		{
			name:   "no active pane keeps the stale value rather than sending empty",
			env:    "%35001",
			exists: live(),
			active: func() string { return "" },
			want:   "%35001",
		},
		{
			name:   "nothing anywhere yields empty",
			env:    "",
			exists: live(),
			active: func() string { return "" },
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePaneID(tt.env, tt.exists, tt.active); got != tt.want {
				t.Errorf("resolvePaneID(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}
