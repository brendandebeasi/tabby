package daemon

import "testing"

// The mobile-keyboard clamp pins every sidebar to keyboard_width while the
// active client is short, and holds the pin for a few seconds after the height
// recovers so the sidebar doesn't flap while an on-screen keyboard animates
// away. The adopt path used to read that pinned width as a user drag: a phone
// client attaching for a moment clamped the active window's sidebar to the
// default keyboard_width of 15, the next pass adopted 15 over a global of 25,
// and every window in the session followed the phone down and stayed there
// after it detached (WIDTH_SYNC_ADOPT active=@7 from=25 to=15).
func TestAtKeyboardWidthClamp(t *testing.T) {
	const (
		kbWidth     = 15
		kbThreshold = 38
	)
	cases := []struct {
		name         string
		measured     int
		globalWidth  int
		activeHeight int
		holdPending  bool
		want         bool
	}{
		{"short client sitting at the clamp", 15, 25, 24, false, true},
		{"hold outlives the recovered height", 15, 25, 49, true, true},
		// An expired-but-not-yet-retired hold must still guard: the adopt
		// decision runs before the loop that restores the clamped width, so this
		// pass is still measuring a clamped pane.
		{"expired hold not yet retired still guards", 15, 25, 49, true, true},
		{"tall client with no hold is a real drag", 15, 25, 49, false, false},
		{"width below the clamp is not the clamp", 12, 25, 24, true, false},
		{"width above the clamp is not the clamp", 20, 25, 24, true, false},
		{"clamp at or above the global cannot shrink it", 15, 15, 24, true, false},
		{"clamp wider than the global is a widening drag", 15, 12, 24, true, false},
		{"unmeasured height with no hold does not clamp", 15, 25, 0, false, false},
		{"unmeasured height still clamps under a hold", 15, 25, 0, true, true},
		{"height exactly at the threshold clamps", 15, 25, 38, false, true},
		{"height one row above the threshold does not", 15, 25, 39, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := atKeyboardWidthClamp(tc.measured, tc.globalWidth, kbWidth, kbThreshold, tc.activeHeight, tc.holdPending)
			if got != tc.want {
				t.Fatalf("atKeyboardWidthClamp(measured=%d, global=%d, kb=%d, thr=%d, h=%d, holdPending=%v) = %v, want %v",
					tc.measured, tc.globalWidth, kbWidth, kbThreshold, tc.activeHeight, tc.holdPending, got, tc.want)
			}
		})
	}
}
