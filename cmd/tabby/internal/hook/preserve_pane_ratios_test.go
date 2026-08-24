package hook

import "testing"

// TestLayoutPaneCount pins the leaf-counting used to reject a saved layout that
// no longer describes the window. Getting this wrong in either direction is
// costly: too low and a good layout is thrown away, too high and tmux truncates
// the layout and scrambles the surviving panes.
func TestLayoutPaneCount(t *testing.T) {
	cases := []struct {
		name   string
		layout string
		want   int
	}{
		{
			// A window that was never split.
			name:   "single pane",
			layout: "b3a4,200x50,0,0,0",
			want:   1,
		},
		{
			name:   "two panes side by side",
			layout: "4f3b,200x50,0,0{100x50,0,0,0,99x50,101,0,2}",
			want:   2,
		},
		{
			// Captured live from tmux 3.6 before killing the middle pane; this
			// is the shape that used to be applied to a two-pane window.
			name:   "three panes side by side",
			layout: "1f24,200x50,0,0{100x50,0,0,0,49x50,101,0,1,49x50,151,0,2}",
			want:   3,
		},
		{
			// Square brackets are the vertical container; a nested mix has to
			// count every leaf and no container.
			name:   "nested containers",
			layout: "d0b1,200x50,0,0{39x50,0,0,0,160x50,40,0[160x25,40,0,1,160x24,40,26{80x24,40,26,2,79x24,121,26,3}]}",
			want:   4,
		},
		{
			// Pane indices are not bounded by 9, and neither are dimensions.
			name:   "multi-digit indices",
			layout: "aaaa,300x100,0,0{150x100,0,0,10,149x100,151,0,123}",
			want:   2,
		},
		{
			name:   "empty",
			layout: "",
			want:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layoutPaneCount(tc.layout); got != tc.want {
				t.Errorf("layoutPaneCount(%q) = %d, want %d", tc.layout, got, tc.want)
			}
		})
	}
}
