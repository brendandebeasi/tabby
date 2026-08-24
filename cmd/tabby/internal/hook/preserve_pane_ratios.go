package hook

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// layoutLeafCell matches one leaf (pane-bearing) cell of a tmux layout string.
// Leaves look like "80x24,0,0,3" — width, height, x, y, pane index. Container
// cells share the first three fields but are followed by "{" or "[" instead of
// a fourth number, so they never match.
var layoutLeafCell = regexp.MustCompile(`\d+x\d+,\d+,\d+,\d+`)

// layoutPaneIDs returns the pane ids a layout string names. A leaf's fourth
// field is the pane's id number, i.e. the "7" of "%7" — so a layout reading
// "...,0,...,2,...,3" describes panes %0, %2 and %3.
func layoutPaneIDs(layout string) map[int]bool {
	ids := map[int]bool{}
	for _, cell := range layoutLeafCell.FindAllString(layout, -1) {
		fields := strings.Split(cell, ",")
		if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			ids[n] = true
		}
	}
	return ids
}

// livePaneIDs lists the window's current pane ids in the numeric form layout
// strings use.
func livePaneIDs(windowID string) map[int]bool {
	ids := map[int]bool{}
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}").Output()
	if err != nil {
		return ids
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(line, "%")); err == nil {
			ids[n] = true
		}
	}
	return ids
}

// samePaneSet reports whether two pane-id sets are identical. An empty set is
// never a match: it means the read failed, not that the window is empty.
func samePaneSet(a, b map[int]bool) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}

// doPreservePaneRatios replaces preserve_pane_ratios.sh:
// restore saved pane layout after a pane exits to preserve size ratios.
// Called from after-kill-pane hook.
func doPreservePaneRatios(args []string) {
	windowID := ""
	if len(args) > 0 {
		windowID = args[0]
	}
	if windowID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		windowID = strings.TrimSpace(string(out))
	}
	if windowID == "" {
		return
	}

	// Daemon-managed system pane cleanup sets a one-shot skip flag
	skip, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_skip_preserve_"+windowID).Output()
	if strings.TrimSpace(string(skip)) == "1" {
		exec.Command("tmux", "set-option", "-g", "@tabby_skip_preserve_"+windowID, "0").Run()
		return
	}

	// Check if we have a saved layout for this window
	saved, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_layout_"+windowID).Output()
	layout := strings.TrimSpace(string(saved))
	if layout == "" {
		return
	}

	// Only attempt restore if more than one pane remains
	live := livePaneIDs(windowID)
	if len(live) <= 1 {
		return
	}

	// The saved layout usually describes the window as it was BEFORE the pane
	// died, so it has one cell too many — and tmux does not reject that.
	// layout_parse silently destroys trailing cells until the counts agree,
	// which hands a survivor the dead pane's slot. Measured on tmux 3.6 with
	// three side-by-side panes 100|49|49: killing the middle one leaves tmux's
	// own (correct) 150|49, and applying the stale layout turns that into
	// 100|99 — an untouched pane visibly jumps. When the killed pane happened
	// to be the last cell the truncation coincides with what tmux already did,
	// so the restore is a no-op. In other words this only ever changed the
	// window when it changed it wrongly, which is the "closing one split
	// scrambles the rest, one pane shrinks" report.
	//
	// Apply a layout only while it still describes this window -- and "describes"
	// has to mean the same PANES, not merely the same number of them.
	//
	// A count check alone passes in exactly one situation: a pane exits and the
	// daemon respawns a system pane (sidebar, header) before this runs, so the
	// tallies agree again while the saved layout still names the dead pane. That
	// was documented as the case this hook exists for. It isn't -- it is the case
	// this hook corrupts. select-layout does not reject a layout naming panes
	// that no longer exist; it assigns cells POSITIONALLY. Measured on tmux 3.6
	// with three panes 100|32|66: kill the middle one, respawn one, and the
	// counts match at 3, so the stale layout applies and the surviving right-hand
	// pane inherits the dead pane's 32-wide slot while the newborn takes its 66.
	// An untouched pane visibly jumps and a boundary appears where the dead pane
	// used to be -- the "closing one split leaves a ghost split" report.
	//
	// Since @tabby_layout_<wid> is re-saved on every refresh, at after-kill-pane
	// it always describes the pre-kill pane set, so this check is expected to
	// skip most of the time. That is correct: there is nothing faithful to
	// replay. The daemon reached the same conclusion for its own restored
	// layouts in ec9868a (layoutPaneIDs/samePaneSet in daemon/layout.go); this
	// hook was the one path still going on a tally.
	if !samePaneSet(layoutPaneIDs(layout), live) {
		return
	}

	// Apply the saved layout (may fail if pane count changed, that's fine)
	exec.Command("tmux", "select-layout", "-t", windowID, layout).Run()
}
