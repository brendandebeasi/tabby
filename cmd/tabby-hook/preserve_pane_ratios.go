package main

import (
	"os/exec"
	"strings"
)

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

	// Apply the saved layout (may fail if pane count changed, that's fine)
	exec.Command("tmux", "select-layout", "-t", windowID, layout).Run()
}
