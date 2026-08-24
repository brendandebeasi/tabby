package daemon

import "testing"

// Only the sidebar renderer draws the marker/color overlays. Menus opened from
// a window header or pane header used to either hide the entry (marker) or
// offer one that silently did nothing (color); both now resolve to the sidebar
// of the same window.
func TestPickerHostClient(t *testing.T) {
	c := newTestCoordinator(t)
	c.clientWidths["@7"] = 30

	cases := []struct {
		name     string
		clientID string
		want     string
	}{
		{"sidebar hosts itself", "@7", "@7"},
		{"window header routes to its sidebar", "window-header:@7", "@7"},
		{"window header with no sidebar connected", "window-header:@9", ""},
		{"sidebar popup cannot draw a picker", "sidebar-popup:abc", ""},
		{"empty client", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.pickerHostClient(tc.clientID); got != tc.want {
				t.Fatalf("pickerHostClient(%q) = %q, want %q", tc.clientID, got, tc.want)
			}
		})
	}
}

// A sidebar client is hosted even when it is not in clientWidths: it is the
// renderer asking, so it is by definition connected.
func TestPickerHostClientSidebarNeedsNoRegistration(t *testing.T) {
	c := newTestCoordinator(t)
	if got := c.pickerHostClient("@42"); got != "@42" {
		t.Fatalf("pickerHostClient = %q, want @42", got)
	}
}
