package sidebarpopup

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// buildContent makes an n-line content blob so resolveClick's viewport
// clamping has something to slice against.
func buildContent(n int) string {
	return strings.Repeat("x\n", n)
}

func TestRenderHeightReservesCloseButton(t *testing.T) {
	m := popupModel{height: 24}
	if got, want := m.renderHeight(), 24-closeButtonHeight; got != want {
		t.Fatalf("renderHeight() = %d, want %d", got, want)
	}
	// Never advertise a non-positive height even on a tiny popup.
	tiny := popupModel{height: 1}
	if got := tiny.renderHeight(); got != 1 {
		t.Fatalf("renderHeight() on tiny popup = %d, want 1", got)
	}
}

func TestResolveClick(t *testing.T) {
	// A phone-height popup whose main list fills the visible rows. Regions mimic
	// generateMainContent: a normal window tab on line 5 and a floated Minimized
	// tab on the last visible content line.
	const height = 24
	visible := height - closeButtonHeight // 21 content rows drawn (pinnedHeight 0)

	m := popupModel{
		width:          40,
		height:         height,
		pinnedHeight:   0,
		viewportOffset: 0,
		content:        buildContent(visible),
		regions: []daemon.ClickableRegion{
			// left gutter toggle on the normal tab
			{StartLine: 5, EndLine: 5, StartCol: 0, EndCol: 5, Action: "toggle_panes", Target: "@10"},
			// body of the normal tab -> select
			{StartLine: 5, EndLine: 5, StartCol: 5, EndCol: 0, Action: "select_window", Target: "@10"},
			// a minimized tab floated to the bottom-most visible content row
			{StartLine: visible - 1, EndLine: visible - 1, StartCol: 0, EndCol: 0, Action: "select_window", Target: "@min"},
		},
	}

	cases := []struct {
		name       string
		x, y       int
		wantAction string
		wantTarget string
	}{
		{"normal tab body selects", 20, 5, "select_window", "@10"},
		{"normal tab gutter toggles", 2, 5, "toggle_panes", "@10"},
		{"minimized bottom row selects", 20, visible - 1, "select_window", "@min"},
		{"empty row resolves nothing", 20, 2, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, tgt := m.resolveClick(tc.x, tc.y)
			if a != tc.wantAction || tgt != tc.wantTarget {
				t.Fatalf("resolveClick(%d,%d) = (%q,%q), want (%q,%q)",
					tc.x, tc.y, a, tgt, tc.wantAction, tc.wantTarget)
			}
		})
	}
}

// The bottom-most drawn content row must be reachable — it is exactly the row a
// floated Minimized tab lands on, and the pre-fix full-height reporting pushed
// it under the close button where it could not be hit.
func TestResolveClickReachesLastVisibleRow(t *testing.T) {
	const height = 12
	visible := height - closeButtonHeight
	m := popupModel{
		width:   30,
		height:  height,
		content: buildContent(visible),
		regions: []daemon.ClickableRegion{
			{StartLine: visible - 1, EndLine: visible - 1, StartCol: 0, EndCol: 0, Action: "select_window", Target: "@parked"},
		},
	}
	if a, tgt := m.resolveClick(10, visible-1); a != "select_window" || tgt != "@parked" {
		t.Fatalf("last visible row not reachable: got (%q,%q)", a, tgt)
	}
}
