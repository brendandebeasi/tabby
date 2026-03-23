package main

import "os/exec"

// setViewMode switches between "current" and "overview" view modes.
// It persists the new mode to the @tabby_view_mode tmux option.
// Callers are responsible for triggering a render after this call.
func (c *Coordinator) setViewMode(mode string) {
	c.stateMu.Lock()
	c.viewMode = mode
	c.stateMu.Unlock()
	exec.Command("tmux", "set-option", "-g", "@tabby_view_mode", mode).Run() //nolint:errcheck
}

// toggleOverviewWindow flips the per-window collapse state in overview mode.
// Windows not in the map default to collapsed (false = expanded, missing key = collapsed).
func (c *Coordinator) toggleOverviewWindow(windowID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.overviewCollapsed == nil {
		c.overviewCollapsed = make(map[string]bool)
	}
	// Missing key = collapsed; toggling sets it to expanded (false)
	c.overviewCollapsed[windowID] = !c.overviewCollapsed[windowID]
}

// isOverviewWindowCollapsed returns true if the window should be collapsed in overview mode.
// Missing key = collapsed (default behavior).
func (c *Coordinator) isOverviewWindowCollapsed(windowID string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	expanded, exists := c.overviewCollapsed[windowID]
	if !exists {
		return true // default: collapsed
	}
	return !expanded
}
