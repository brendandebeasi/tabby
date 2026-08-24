package daemon

import "testing"

func TestDisclosureCellWidthIsStateIndependent(t *testing.T) {
	cases := []struct {
		name     string
		expanded string
		collapse string
		want     int
	}{
		{"builtin icons", "⊟", "⊞", 1},
		{"ascii", "-", "+", 1},
		{"wide expanded", "🔽", "▶", 2},
		{"wide collapsed", "▼", "🔼", 2},
		{"both wide", "🔽", "🔼", 2},
		{"empty falls back to one cell", "", "", 1},
		{"one side empty", "", "▶", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := disclosureCellWidth(tc.expanded, tc.collapse)
			if got != tc.want {
				t.Fatalf("disclosureCellWidth(%q, %q) = %d, want %d",
					tc.expanded, tc.collapse, got, tc.want)
			}
			// The whole point: swapping which icon is "current" cannot change
			// the reserved width, so the toggle's hit zone never moves.
			if rev := disclosureCellWidth(tc.collapse, tc.expanded); rev != got {
				t.Fatalf("width depends on argument order: %d vs %d", got, rev)
			}
		})
	}
}
