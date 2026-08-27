package daemon

import (
	"strings"
	"testing"

	daemonpkg "github.com/brendandebeasi/tabby/pkg/daemon"
)

func findRegion(regions []daemonpkg.ClickableRegion, target, action string) *daemonpkg.ClickableRegion {
	for i := range regions {
		if regions[i].Target == target && regions[i].Action == action {
			return &regions[i]
		}
	}
	return nil
}

// The sidebar's < / > buttons rendered but clicking them did nothing, because
// region extraction went through zone.Scan + zone.Get: Scan hands bounds to a
// background goroutine, so the first frame produced no regions at all and later
// frames produced stale ones -- and the two scans per frame (top zone, bottom
// zone) deleted each other's zones. Regions must be available on the very first
// render of the content they describe.
func TestRenderWidgetZoneExtractsRegionsOnFirstRender(t *testing.T) {
	c := newRenderCoordinator(t)
	const width = 25

	entries := []widgetEntry{
		{name: "nav_buttons", zone: "bottom", priority: 10, content: c.renderNavButtons(width)},
		{name: "resize_buttons", zone: "bottom", priority: 9999, content: c.renderSidebarResizeButtons(width)},
	}

	content, regions := c.renderWidgetZone(entries, width)

	if strings.ContainsRune(content, 0x1b) && strings.Contains(content, "z") {
		// Not conclusive on its own (styling uses escapes too), so check
		// specifically that no zone marker survived.
		for _, id := range widgetZoneIDs {
			if m := zoneMarker(id); m != "" && strings.Contains(content, m) {
				t.Errorf("zone marker for %s leaked into rendered content", id)
			}
		}
	}

	shrink := findRegion(regions, "sidebar", "shrink")
	grow := findRegion(regions, "sidebar", "grow")
	if shrink == nil || grow == nil {
		t.Fatalf("expected sidebar shrink and grow regions on first render, got %+v", regions)
	}

	// renderNavButtons emits two lines (nav row + theme toggle row), so the
	// resize row lands on line 2.
	if shrink.StartLine != 2 || shrink.EndLine != 2 {
		t.Errorf("shrink region lines = %d-%d, want 2-2", shrink.StartLine, shrink.EndLine)
	}
	if shrink.StartCol != 0 || shrink.EndCol != width/2 {
		t.Errorf("shrink region cols = %d-%d, want 0-%d", shrink.StartCol, shrink.EndCol, width/2)
	}
	if grow.StartCol != width/2 || grow.EndCol != width {
		t.Errorf("grow region cols = %d-%d, want %d-%d", grow.StartCol, grow.EndCol, width/2, width)
	}
	if grow.StartLine != 2 || grow.EndLine != 2 {
		t.Errorf("grow region lines = %d-%d, want 2-2", grow.StartLine, grow.EndLine)
	}

	prev := findRegion(regions, "sidebar", "prev_window")
	next := findRegion(regions, "sidebar", "next_window")
	if prev == nil || next == nil {
		t.Fatalf("expected window nav regions, got %+v", regions)
	}
	if prev.StartLine != 0 || next.StartLine != 0 {
		t.Errorf("nav regions on lines %d/%d, want 0/0", prev.StartLine, next.StartLine)
	}

	// Rendering again must not accumulate or drop regions.
	_, again := c.renderWidgetZone(entries, width)
	if len(again) != len(regions) {
		t.Errorf("second render produced %d regions, first produced %d", len(again), len(regions))
	}
}

func TestScanZoneRegionsIgnoresNonZoneEscapes(t *testing.T) {
	marker := zoneMarker("sidebar:grow")
	if marker == "" {
		t.Fatal("no marker for sidebar:grow")
	}
	raw := "\x1b[31mred\x1b[0m\n" + marker + "\x1b[1mGO\x1b[0m" + marker

	clean, regions := scanZoneRegions(raw, widgetZoneIDs)
	if strings.Contains(clean, marker) {
		t.Error("marker survived the scan")
	}
	if !strings.Contains(clean, "\x1b[31m") || !strings.Contains(clean, "\x1b[1m") {
		t.Errorf("styling escapes were stripped: %q", clean)
	}
	if len(regions) != 1 {
		t.Fatalf("got %d regions, want 1: %+v", len(regions), regions)
	}
	r := regions[0]
	if r.StartLine != 1 || r.EndLine != 1 || r.StartCol != 0 || r.EndCol != 2 {
		t.Errorf("region = lines %d-%d cols %d-%d, want lines 1-1 cols 0-2",
			r.StartLine, r.EndLine, r.StartCol, r.EndCol)
	}
}
