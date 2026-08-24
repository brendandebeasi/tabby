package hook

import (
	"reflect"
	"testing"
)

// TestLayoutPaneIDs pins the leaf parsing used to decide whether a saved layout
// still describes the window. Getting this wrong in either direction is costly:
// miss a pane and a good layout is thrown away, invent one and tmux applies a
// stale layout positionally and scrambles the survivors.
func TestLayoutPaneIDs(t *testing.T) {
	cases := []struct {
		name   string
		layout string
		want   map[int]bool
	}{
		{
			// A window that was never split.
			name:   "single pane",
			layout: "b3a4,200x50,0,0,0",
			want:   map[int]bool{0: true},
		},
		{
			name:   "two panes side by side",
			layout: "4f3b,200x50,0,0{100x50,0,0,0,99x50,101,0,2}",
			want:   map[int]bool{0: true, 2: true},
		},
		{
			// Captured live from tmux 3.6 before killing the middle pane; this
			// is the shape that used to be applied to a two-pane window.
			name:   "three panes side by side",
			layout: "1f24,200x50,0,0{100x50,0,0,0,49x50,101,0,1,49x50,151,0,2}",
			want:   map[int]bool{0: true, 1: true, 2: true},
		},
		{
			// Square brackets are the vertical container; a nested mix has to
			// read every leaf and no container.
			name:   "nested containers",
			layout: "d0b1,200x50,0,0{39x50,0,0,0,160x50,40,0[160x25,40,0,1,160x24,40,26{80x24,40,26,2,79x24,121,26,3}]}",
			want:   map[int]bool{0: true, 1: true, 2: true, 3: true},
		},
		{
			// Pane ids are not bounded by 9, and neither are dimensions.
			name:   "multi-digit ids",
			layout: "aaaa,300x100,0,0{150x100,0,0,10,149x100,151,0,123}",
			want:   map[int]bool{10: true, 123: true},
		},
		{
			name:   "empty",
			layout: "",
			want:   map[int]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := layoutPaneIDs(tc.layout)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("layoutPaneIDs(%q) = %v, want %v", tc.layout, got, tc.want)
			}
		})
	}
}

// TestSamePaneSetRejectsStaleLayout is the regression that matters, captured
// from a live tmux 3.6 run: three panes 100|32|66, kill the middle (%1), then
// let the daemon respawn one (%3). The pane COUNT is 3 both before and after,
// so a tally-based guard waves the stale layout through and select-layout --
// which assigns cells positionally, not by pane id -- hands surviving %2 the
// dead pane's 32-wide slot and newborn %3 the 66. Comparing pane sets is what
// catches it.
func TestSamePaneSetRejectsStaleLayout(t *testing.T) {
	saved := "2d24,200x50,0,0{100x50,0,0,0,32x50,101,0,1,66x50,134,0,2}"
	live := map[int]bool{0: true, 2: true, 3: true}

	if got, want := len(layoutPaneIDs(saved)), len(live); got != want {
		t.Fatalf("precondition: counts must match to reach the guard, got %d vs %d", got, want)
	}
	if samePaneSet(layoutPaneIDs(saved), live) {
		t.Error("stale layout naming dead pane %1 must not be applied")
	}
}

func TestSamePaneSet(t *testing.T) {
	cases := []struct {
		name string
		a, b map[int]bool
		want bool
	}{
		{"identical", map[int]bool{0: true, 2: true}, map[int]bool{2: true, 0: true}, true},
		{"different member", map[int]bool{0: true, 1: true}, map[int]bool{0: true, 2: true}, false},
		{"different size", map[int]bool{0: true}, map[int]bool{0: true, 1: true}, false},
		// An empty layout or a failed list-panes read must never look like a
		// match, or a window would be "restored" to nothing.
		{"both empty", map[int]bool{}, map[int]bool{}, false},
		{"empty saved", map[int]bool{}, map[int]bool{0: true}, false},
		{"empty live", map[int]bool{0: true}, map[int]bool{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := samePaneSet(tc.a, tc.b); got != tc.want {
				t.Errorf("samePaneSet(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
