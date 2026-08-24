package hook

import (
	"os/exec"
	"regexp"
	"strings"
)

// layoutLeafCell matches one leaf (pane-bearing) cell of a tmux layout string.
// Leaves look like "80x24,0,0,3" — width, height, x, y, pane index. Container
// cells share the first three fields but are followed by "{" or "[" instead of
// a fourth number, so they never match.
var layoutLeafCell = regexp.MustCompile(`\d+x\d+,\d+,\d+,\d+`)

// layoutPaneCount reports how many panes a tmux layout string describes.
func layoutPaneCount(layout string) int {
	return len(layoutLeafCell.FindAllString(layout, -1))
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
	out, _ := exec.Command("tmux", "list-panes", "-t", windowID).Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, l := range lines {
		if l != "" {
			count++
		}
	}
	if count <= 1 {
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
	// Apply a layout only while it still describes this window. That leaves the
	// case the hook exists for: a pane exits and the daemon respawns a system
	// pane (sidebar, header) before this runs, so the counts match again and
	// the saved ratios are the ones to put back.
	if layoutPaneCount(layout) != count {
		return
	}

	// Apply the saved layout (may fail if pane count changed, that's fine)
	exec.Command("tmux", "select-layout", "-t", windowID, layout).Run()
}
