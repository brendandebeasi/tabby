package daemon

import "testing"

// A sidebar width measured while the client is mid-reflow is a transient, not a
// drag. Width flips were already guarded; a height-only flip was not, so a
// forced sync pass landing on the transient adopted it as the global and jumped
// every window's sidebar (WIDTH_SYNC_ADOPT active=@1930 from=30 to=46).
func TestClientGeometryFlipped(t *testing.T) {
	cases := []struct {
		name                                string
		lastW, lastH, curW, curH            int
		wantWidthChanged, wantHeightChanged bool
	}{
		{"steady geometry is not a flip", 167, 45, 167, 45, false, false},
		{"width flip", 167, 45, 120, 45, true, false},
		{"height-only flip still counts", 167, 45, 167, 46, false, true},
		{"both axes flip", 167, 45, 120, 46, true, true},
		{"first pass has no baseline width", 0, 45, 167, 45, false, false},
		{"first pass has no baseline height", 167, 0, 167, 46, false, false},
		{"unmeasured current width is not a flip", 167, 45, 0, 45, false, false},
		{"unmeasured current height is not a flip", 167, 45, 167, 0, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotW, gotH := clientGeometryFlipped(tc.lastW, tc.lastH, tc.curW, tc.curH)
			if gotW != tc.wantWidthChanged || gotH != tc.wantHeightChanged {
				t.Fatalf("clientGeometryFlipped(%d,%d,%d,%d) = (%v,%v), want (%v,%v)",
					tc.lastW, tc.lastH, tc.curW, tc.curH, gotW, gotH, tc.wantWidthChanged, tc.wantHeightChanged)
			}
		})
	}
}
