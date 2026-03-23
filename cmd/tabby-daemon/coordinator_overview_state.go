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

func (c *Coordinator) toggleViewMode() {
	c.stateMu.Lock()
	if c.viewMode == "overview" {
		c.viewMode = "current"
	} else {
		c.viewMode = "overview"
	}
	mode := c.viewMode
	c.stateMu.Unlock()
	exec.Command("tmux", "set-option", "-g", "@tabby_view_mode", mode).Run() //nolint:errcheck
}

// isOverviewWindowCollapsed returns true if the window should be collapsed in overview mode.
// Missing key = collapsed (default behavior).
// This method acquires stateMu.RLock — do NOT call from code that already holds stateMu.
func (c *Coordinator) isOverviewWindowCollapsed(windowID string) bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.isOverviewWindowCollapsedLocked(windowID)
}

// isOverviewWindowCollapsedLocked is the lock-free variant for callers that already
// hold c.stateMu (e.g. renderOverviewContent called from generateMainContent under RenderForClient).
// true in the map means expanded; missing key or false means collapsed.
func (c *Coordinator) isOverviewWindowCollapsedLocked(windowID string) bool {
	expanded, exists := c.overviewCollapsed[windowID]
	if !exists {
		return true // default: collapsed
	}
	return !expanded
}
