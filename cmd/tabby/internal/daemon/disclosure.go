package daemon

import "github.com/rivo/uniseg"

// disclosureCellWidth returns the column width reserved for a group row's
// expand/collapse icon.
//
// Both icons share one cell width so the disclosure control — and therefore
// the group name that follows it — occupies the same columns whether the group
// is expanded or collapsed. Sizing the cell from whichever icon happened to be
// showing let the clickable toggle zone shift underneath the pointer: a click
// that collapsed a group could then land on the group-name region, which has no
// left-click action, so expanding took a second click at a different spot.
//
// The built-in icons are both one cell wide, so this only matters once a user
// configures disclosure_expanded / disclosure_collapsed with glyphs of
// differing width (an emoji against an arrow, say).
func disclosureCellWidth(expandedIcon, collapsedIcon string) int {
	w := uniseg.StringWidth(expandedIcon)
	if cw := uniseg.StringWidth(collapsedIcon); cw > w {
		w = cw
	}
	if w < 1 {
		w = 1
	}
	return w
}
