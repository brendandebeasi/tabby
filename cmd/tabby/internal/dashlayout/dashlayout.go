// Package dashlayout holds pure helpers shared by the daemon and the cycle-pane
// binary for the dashboard's "-auto" layouts (Main+stack/row, active), where the
// focused pane is always the big/main one.
package dashlayout

import "sort"

// PlanActiveMainSwaps returns the sequence of pane swaps that rearrange a
// window's panes so the active pane occupies the first/main slot and the rest
// follow in ascending numeric pane-id order. Applying the swaps in order with
// `tmux swap-pane -s src -t dst` (src/dst are pane ids like "%3") yields that
// arrangement.
//
// Sorting the non-active panes by pane id (a stable, creation-order-ish key)
// keeps the stack stable: a pane always falls back to the same slot when it
// isn't the active one, instead of drifting to wherever the newly-focused pane
// happened to vacate.
//
// paneIDs must be in current position (pane-index) order; activeID must be one
// of them. Returns nil when nothing needs to move (already arranged) or the
// input is degenerate (<2 panes, or activeID absent).
func PlanActiveMainSwaps(paneIDs []string, activeID string) [][2]string {
	if len(paneIDs) < 2 || activeID == "" {
		return nil
	}
	found := false
	for _, id := range paneIDs {
		if id == activeID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	others := make([]string, 0, len(paneIDs)-1)
	for _, id := range paneIDs {
		if id != activeID {
			others = append(others, id)
		}
	}
	sort.Slice(others, func(i, j int) bool { return paneNum(others[i]) < paneNum(others[j]) })
	target := append([]string{activeID}, others...)

	// Selection sort current -> target via pairwise swaps. pos tracks where each
	// pane currently sits so we can find the pane that belongs at slot i in O(1).
	current := append([]string(nil), paneIDs...)
	pos := make(map[string]int, len(current))
	for i, id := range current {
		pos[id] = i
	}
	var swaps [][2]string
	for i := range target {
		if current[i] == target[i] {
			continue
		}
		j := pos[target[i]] // current position of the pane that should be at i
		swaps = append(swaps, [2]string{current[i], current[j]})
		pos[current[i]], pos[current[j]] = j, i
		current[i], current[j] = current[j], current[i]
	}
	return swaps
}

// paneNum extracts the integer part of a tmux pane id ("%123" -> 123). Unparsable
// ids sort last so they never claim the main slot's neighbour by accident.
func paneNum(id string) int {
	n, seen := 0, false
	for _, c := range id {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
			seen = true
		}
	}
	if !seen {
		return 1 << 30
	}
	return n
}
