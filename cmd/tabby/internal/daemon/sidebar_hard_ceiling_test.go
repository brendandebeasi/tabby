package daemon

import "testing"

// TestSidebarHardCeiling locks in the window-relative clamp that prevents the
// sidebar from "getting very large": a runaway width (e.g. a too-wide drag
// adopted as the global) must never survive past this ceiling.
func TestSidebarHardCeiling(t *testing.T) {
	const (
		maxPercent = 20 // default @tabby_sidebar_mobile_max_percent
		minContent = 40 // default @tabby_sidebar_mobile_min_content_cols
	)
	cases := []struct {
		name        string
		windowWidth int
		maxPercent  int
		minContent  int
		want        int
	}{
		// 20% of 200 = 40; content floor 200-40=160 -> fraction wins.
		{"desktop_200_fraction_wins", 200, maxPercent, minContent, 40},
		// 20% of 300 = 60; content floor 260 -> fraction wins.
		{"ultrawide_300", 300, maxPercent, minContent, 60},
		// Narrow window: fraction 20%*100=20, content 100-40=60 -> fraction wins.
		{"narrow_100", 100, maxPercent, minContent, 20},
		// Very narrow: fraction 20%*70=14 -> floored to 15.
		{"tiny_70_floor", 70, maxPercent, minContent, 15},
		// Content floor can win when minContentCols is large relative to width:
		// fraction 20%*120=24, content 120-100=20 -> content wins.
		{"content_floor_wins", 120, maxPercent, 100, 20},
		// Higher configured percent loosens the cap: 33% of 240 = 79.
		{"loose_percent", 240, 33, minContent, 79},
		// Regression: the mobile 20% guard applied to a desktop window pinned a
		// 167-column window's sidebar to 33, so an explicit drag to 49 was
		// clamped back on the next width sync and the sidebar snapped back
		// forever. Desktop windows get the looser desktop percent (default 50),
		// which leaves room for the drag while still blocking a runaway.
		{"desktop_167_mobile_percent_too_tight", 167, maxPercent, minContent, 33},
		{"desktop_167_desktop_percent_allows_drag", 167, 50, minContent, 83},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sidebarHardCeiling(tc.windowWidth, tc.maxPercent, tc.minContent)
			if got != tc.want {
				t.Fatalf("sidebarHardCeiling(%d, %d, %d) = %d, want %d",
					tc.windowWidth, tc.maxPercent, tc.minContent, got, tc.want)
			}
			// Invariant: never below the absolute floor.
			if got < 15 {
				t.Fatalf("ceiling %d below hard floor 15", got)
			}
		})
	}
}

// TestSidebarEffectiveMaxPercent locks in the two invariants that matter for the
// desktop relaxation, independently of whatever options this machine has set:
// the mobile guard still applies at or below the tablet threshold, and above it
// the ceiling is never *tighter* than the mobile guard.
func TestSidebarEffectiveMaxPercent(t *testing.T) {
	threshold := sidebarTabletMaxWindowCols()

	if got := sidebarEffectiveMaxPercent(threshold, 20); got != 20 {
		t.Fatalf("at tablet threshold %d: got %d%%, want the mobile guard 20%%", threshold, got)
	}
	if got := sidebarEffectiveMaxPercent(threshold+1, 20); got < 20 {
		t.Fatalf("above tablet threshold: got %d%%, must not be tighter than the mobile guard 20%%", got)
	}
	// A mobile guard looser than the desktop default must not be tightened.
	if got := sidebarEffectiveMaxPercent(threshold+1, 60); got < 60 {
		t.Fatalf("above tablet threshold: got %d%%, must not be tighter than the mobile guard 60%%", got)
	}
}
