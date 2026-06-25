package cyclepane

import "testing"

func TestMoveTargetIndex(t *testing.T) {
	cases := []struct {
		name      string
		activeIdx int
		n         int
		dir       string
		wantIdx   int
		wantOK    bool
	}{
		// promote -> slot 0
		{"promote from middle", 2, 4, "promote", 0, true},
		{"promote from last", 3, 4, "promote", 0, true},
		{"promote already primary is no-op", 0, 4, "promote", 0, false},

		// next wraps forward
		{"next middle", 1, 4, "next", 2, true},
		{"next wraps at end", 3, 4, "next", 0, true},

		// prev wraps backward
		{"prev middle", 2, 4, "prev", 1, true},
		{"prev wraps at start", 0, 4, "prev", 3, true},

		// guards
		{"too few panes", 0, 1, "promote", 0, false},
		{"active not found", -1, 4, "next", 0, false},
		{"active out of range", 5, 4, "next", 0, false},
		{"unknown direction", 1, 4, "sideways", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotIdx, gotOK := moveTargetIndex(c.activeIdx, c.n, c.dir)
			if gotOK != c.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, c.wantOK)
			}
			if gotOK && gotIdx != c.wantIdx {
				t.Errorf("idx = %d, want %d", gotIdx, c.wantIdx)
			}
		})
	}
}

// A two-pane window must still allow promote/next/prev (the smallest case where
// reordering is meaningful).
func TestMoveTargetIndexTwoPanes(t *testing.T) {
	if idx, ok := moveTargetIndex(1, 2, "promote"); !ok || idx != 0 {
		t.Errorf("promote(1,2) = (%d,%v), want (0,true)", idx, ok)
	}
	if idx, ok := moveTargetIndex(0, 2, "next"); !ok || idx != 1 {
		t.Errorf("next(0,2) = (%d,%v), want (1,true)", idx, ok)
	}
	if idx, ok := moveTargetIndex(1, 2, "prev"); !ok || idx != 0 {
		t.Errorf("prev(1,2) = (%d,%v), want (0,true)", idx, ok)
	}
}
