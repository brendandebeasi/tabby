// Dashboard mode gathers every content pane from across the session's windows
// into a single tiled "dashboard" window so the user can glance at everything
// at once, then dive into one via native tmux zoom (prefix+z), resize, or kill.
// Toggling off restores each pane to its original window.
//
// Design notes:
//   - Content panes are MOVED (join-pane), never copied — they stay the real,
//     fully interactive panes, so zoom/resize/close are native tmux. Pane IDs
//     survive join-pane, so the focus-return target is stable across the trip.
//   - Origin windows are emptied and destroyed on enter, and recreated from an
//     in-memory snapshot on exit (name + group + path). No user content is ever
//     killed — it lives in the dashboard window while gathered.
//   - The dashboard window carries window-option @tabby_dashboard=1 and renders
//     as a normal tabby window (sidebar + headers + the new-window button).
package daemon

import (
	"fmt"
	"strconv"
	"strings"
)

// lightenHex returns a hex colour blended `frac` of the way toward white. Used
// by applyNativeBorders to produce a softer inactive-pane border colour from
// the active group colour. Returns hex unchanged when it can't be parsed.
func lightenHex(hex string, frac float64) string {
	if len(hex) != 7 || hex[0] != '#' {
		return hex
	}
	r, err1 := strconv.ParseUint(hex[1:3], 16, 8)
	g, err2 := strconv.ParseUint(hex[3:5], 16, 8)
	b, err3 := strconv.ParseUint(hex[5:7], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return hex
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	blend := func(c uint64) uint64 {
		return uint64(float64(c) + (255-float64(c))*frac)
	}
	return fmt.Sprintf("#%02x%02x%02x", blend(r), blend(g), blend(b))
}

const dashboardWindowName = "Dashboard"

// dashWindowSnapshot records enough of an origin window to recreate it on exit
// and to render it in the sidebar while gathered.
type dashWindowSnapshot struct {
	Name  string
	Group string
	Path  string
	Index int
}

// dashboardActiveWindowID returns the window_id of the dashboard window for the
// session (the one tagged @tabby_dashboard=1), or "" if none. Reads from tmux so
// it is correct even after a daemon restart.
func dashboardActiveWindowID(sess string) string {
	if sess == "" {
		return ""
	}
	out := tmuxOutputTrimmed("list-windows", "-t", sess, "-F", "#{window_id}\t#{@tabby_dashboard}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) == "1" {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

// enterDashboard gathers all content panes into one tiled dashboard window.
func (c *Coordinator) enterDashboard() {
	sess := c.dashboardSession()
	if sess == "" {
		return
	}

	c.ForgetAllWindowLayouts()
	_ = tmuxRun("set-option", "-g", "@tabby_spawning", "1")
	defer tmuxRun("set-option", "-gu", "@tabby_spawning")

	// Pane IDs survive join-pane, so retPane stays valid across the round trip.
	retPane := tmuxOutputTrimmed("display-message", "-p", "-t", sess, "#{pane_id}")

	// Snapshot every real window (skip sidebar stash windows), in index order.
	snaps := map[string]dashWindowSnapshot{}
	var order []string
	winOut := tmuxOutputTrimmed("list-windows", "-t", sess, "-F",
		"#{window_id}\t#{window_index}\t#{window_name}\t#{@tabby_group}")
	for _, line := range dashLines(winOut) {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		idx := 0
		if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			idx = n
		}
		name := parts[2]
		group := ""
		if len(parts) == 4 {
			group = strings.TrimSpace(parts[3])
		}
		if id == "" || strings.HasPrefix(name, sidebarStashWindowPrefix) {
			continue
		}
		snaps[id] = dashWindowSnapshot{Name: name, Group: group, Index: idx}
		order = append(order, id)
	}

	// Create the dashboard window (detached: don't yank the client yet).
	dashID := firstToken(tmuxOutputTrimmed("new-window", "-d", "-P", "-F", "#{window_id}", "-t", sess+":"), "@")
	if dashID == "" {
		return
	}
	_ = tmuxRun("set-window-option", "-t", dashID, "@tabby_dashboard", "1")
	_ = tmuxRun("rename-window", "-t", dashID, dashboardWindowName)
	_ = tmuxRun("set-window-option", "-t", dashID, "@tabby_name_locked", "1")
	placeholder := firstToken(tmuxOutputTrimmed("list-panes", "-t", dashID, "-F", "#{pane_id}"), "%")

	// Enumerate content panes across the session, skipping aux panes and the
	// dashboard window. Tab-split tolerates an empty trailing field: shell panes
	// have no pane_start_command and tmuxOutputTrimmed strips the trailing tab
	// off the last line, so that row can arrive with only 3 fields.
	type pinfo struct{ pane, win, path string }
	var content []pinfo
	paneOut := tmuxOutputTrimmed("list-panes", "-s", "-t", sess, "-F",
		"#{pane_id}\t#{window_id}\t#{pane_current_command}\t#{pane_start_command}\t#{pane_current_path}")
	for _, line := range dashLines(paneOut) {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 3 {
			continue
		}
		pane, win, cur := parts[0], parts[1], parts[2]
		start, path := "", ""
		if len(parts) >= 4 {
			start = parts[3]
		}
		if len(parts) >= 5 {
			path = parts[4]
		}
		if win == dashID || isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start) {
			continue
		}
		content = append(content, pinfo{pane: pane, win: win, path: path})
		if s, ok := snaps[win]; ok && s.Path == "" {
			s.Path = path
			snaps[win] = s
		}
	}

	if len(content) == 0 {
		_ = tmuxRun("kill-window", "-t", dashID)
		return
	}

	// Move each content pane into the dashboard window, tagging its origin.
	// Re-tile after every join: joining repeatedly into one target halves its
	// size each time, so without a reflow the Nth join eventually fails for lack
	// of space.
	for _, p := range content {
		_ = tmuxRun("set-option", "-p", "-t", p.pane, "@tabby_dash_origin", p.win)
		if err := tmuxRun("join-pane", "-d", "-h", "-s", p.pane, "-t", placeholder); err != nil {
			_ = tmuxRun("join-pane", "-d", "-s", p.pane, "-t", placeholder)
		}
		_ = tmuxRun("select-layout", "-t", dashID, "tiled")
	}

	if placeholder != "" && paneCount(dashID) > 1 {
		_ = tmuxRun("kill-pane", "-t", placeholder)
	}

	// Destroy origin windows that are now content-empty (guard against killing a
	// window where a join failed and a content pane still lives).
	for _, id := range order {
		if !windowHasContent(id) {
			_ = tmuxRun("kill-window", "-t", id)
		}
	}

	_ = tmuxRun("select-layout", "-t", dashID, "tiled")
	_ = tmuxRun("select-window", "-t", dashID)

	// Label each tile with tmux's NATIVE pane-border-status (a single line on the
	// pane's own border) instead of tabby's overlay header panes — the latter are
	// separate panes whose own borders/resize rows jumble a dense tiled grid.
	// Content panes may carry a pane-local pane-border-status=off (set by tabby
	// when they were normal content panes); clear it so the window-level setting
	// wins. applyDashboardBorders sets the window-level options and is re-run on
	// every refresh (tabby's global border management resets them otherwise).
	for _, p := range content {
		_ = tmuxRun("set-option", "-p", "-t", p.pane, "-u", "pane-border-status")
		_ = tmuxRun("set-option", "-p", "-t", p.pane, "-u", "pane-border-style")
	}
	c.dashboardWindowID = dashID
	c.applyDashboardBorders()

	c.dashboardOrigins = snaps
	c.dashboardOrder = order
	c.dashboardReturnPane = retPane
	coordinatorDebugLog.Printf("enterDashboard: gathered %d panes into %s from %d windows", len(content), dashID, len(order))
}

// exitDashboard restores every gathered pane to its origin window (recreating
// the destroyed window from the snapshot) and removes the dashboard window.
// Returns a map from each origin window id (the snapshot key, also the value of
// @tabby_dash_origin on gathered panes) to the id of the window it was restored
// into, so callers can select the right recreated window.
func (c *Coordinator) exitDashboard() map[string]string {
	restored := map[string]string{}
	sess := c.dashboardSession()
	if sess == "" {
		return restored
	}

	c.ForgetAllWindowLayouts()
	_ = tmuxRun("set-option", "-g", "@tabby_spawning", "1")
	defer tmuxRun("set-option", "-gu", "@tabby_spawning")

	dashID := c.dashboardWindowID
	if dashID == "" || dashboardActiveWindowID(sess) != dashID {
		dashID = dashboardActiveWindowID(sess)
	}
	if dashID == "" {
		return restored
	}

	// Group the dashboard's content panes by their recorded origin window.
	groups := map[string][]string{}
	var groupOrder []string
	paneOut := tmuxOutputTrimmed("list-panes", "-t", dashID, "-F",
		"#{pane_id}\t#{@tabby_dash_origin}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(paneOut) {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		pane, origin, cur := parts[0], strings.TrimSpace(parts[1]), parts[2]
		start := ""
		if len(parts) == 4 {
			start = parts[3]
		}
		if isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start) {
			continue
		}
		if origin == "" {
			origin = "__orphan__"
		}
		if _, ok := groups[origin]; !ok {
			groupOrder = append(groupOrder, origin)
		}
		groups[origin] = append(groups[origin], pane)
	}

	// Restore in original index order first, then any leftovers/orphans.
	var origins []string
	seen := map[string]bool{}
	for _, id := range c.dashboardOrder {
		if _, ok := groups[id]; ok {
			origins = append(origins, id)
			seen[id] = true
		}
	}
	for _, id := range groupOrder {
		if !seen[id] {
			origins = append(origins, id)
		}
	}

	for _, origin := range origins {
		snap := c.dashboardOrigins[origin]
		args := []string{"new-window", "-d", "-P", "-F", "#{window_id}", "-t", sess + ":"}
		if snap.Path != "" {
			args = append(args, "-c", snap.Path)
		}
		newWin := firstToken(tmuxOutputTrimmed(args...), "@")
		if newWin == "" {
			continue
		}
		restored[origin] = newWin
		ph := firstToken(tmuxOutputTrimmed("list-panes", "-t", newWin, "-F", "#{pane_id}"), "%")
		for _, p := range groups[origin] {
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_origin")
			if err := tmuxRun("join-pane", "-d", "-h", "-s", p, "-t", ph); err != nil {
				_ = tmuxRun("join-pane", "-d", "-s", p, "-t", ph)
			}
			_ = tmuxRun("select-layout", "-t", newWin, "tiled")
		}
		if ph != "" && paneCount(newWin) > 1 {
			_ = tmuxRun("kill-pane", "-t", ph)
		}
		if snap.Name != "" {
			_ = tmuxRun("rename-window", "-t", newWin, snap.Name)
		}
		if snap.Group != "" && snap.Group != "Default" {
			_ = tmuxRun("set-window-option", "-t", newWin, "@tabby_group", snap.Group)
		}
		_ = tmuxRun("select-layout", "-t", newWin, "tiled")
	}

	_ = tmuxRun("kill-window", "-t", dashID)

	// Restore focus to the pre-dashboard pane (its id survived the round trip).
	if c.dashboardReturnPane != "" {
		if win := tmuxOutputTrimmed("display-message", "-p", "-t", c.dashboardReturnPane, "#{window_id}"); win != "" {
			_ = tmuxRun("select-window", "-t", win)
			_ = tmuxRun("select-pane", "-t", c.dashboardReturnPane)
		}
	}

	c.dashboardWindowID = ""
	c.dashboardOrigins = nil
	c.dashboardOrder = nil
	c.dashboardReturnPane = ""
	coordinatorDebugLog.Printf("exitDashboard: restored %d origin windows", len(origins))
	return restored
}

// exitDashboardAndSelect restores the gathered panes and focuses the window the
// given origin id was restored into. Used when the user clicks a remembered
// window row in the sidebar or navigates to it from the dashboard.
func (c *Coordinator) exitDashboardAndSelect(origin string) {
	restored := c.exitDashboard()
	if target := restored[origin]; target != "" {
		_ = tmuxRun("select-window", "-t", target)
	}
	focusContentPaneInActiveWindow()
}

// dashboardNavStep handles cmd+opt+[ / cmd+opt+] (M-{ / M-}). While the
// dashboard is open these cycle focus between the tiles (content panes) in the
// grid rather than switching windows; the dashboard is only entered/exited on
// explicit request (M-0 / prefix+0 / clicking "0. Dashboard"). Outside the
// dashboard it returns false so ordinary window navigation runs unchanged.
func (c *Coordinator) dashboardNavStep(delta int) bool {
	if c.dashboardWindowID == "" {
		return false
	}
	c.dashboardCyclePane(delta)
	// Sidebar content doesn't change for an in-dashboard pane cycle. Tell the
	// input loop to skip the post-input broadcast so the renderer doesn't redraw.
	c.dashboardSkipBroadcast.Store(true)
	return true
}

// dashboardCyclePane moves focus to the next/prev CONTENT tile in the dashboard
// window, skipping the sidebar/aux panes and wrapping at the ends.
func (c *Coordinator) dashboardCyclePane(delta int) {
	dash := c.dashboardWindowID
	if dash == "" {
		return
	}
	out := tmuxOutputTrimmed("list-panes", "-t", dash, "-F",
		"#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_start_command}")
	var content []string
	active := -1
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 3 {
			continue
		}
		id, act, cur := parts[0], parts[1], parts[2]
		start := ""
		if len(parts) == 4 {
			start = parts[3]
		}
		if isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start) {
			continue
		}
		content = append(content, id)
		if act == "1" {
			active = len(content) - 1
		}
	}
	if len(content) == 0 {
		return
	}
	if active == -1 {
		active = 0
	}
	target := content[((active+delta)%len(content)+len(content))%len(content)]
	_ = tmuxRun("select-pane", "-t", target)
}

// applyDashboardBorders (re)asserts native pane-border labels on the dashboard
// window. Called on enter AND on every refresh from doPaneLayoutOps, because
// tabby's global border management resets pane-border-status/style each cycle,
// so a one-time set wouldn't stick. No-op when the dashboard isn't active.
func (c *Coordinator) applyDashboardBorders() {
	if c.dashboardWindowID == "" {
		return
	}
	dash := dashboardActiveWindowID(c.dashboardSession())
	if dash == "" {
		return
	}
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-lines", "single")
	// Border format: 4 clickable buttons on the left (close / zoom / v-split /
	// h-split), each 3 cols wide (icon centred), then a separator, then the
	// Border label shows command + folder on the left, menu shortcut hint on the
	// right. Tile actions come from a popup menu opened with prefix+, — tmux
	// 3.5a doesn't deliver mouse events to the pane-border-status row, so
	// on-border buttons aren't reachable.
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-format",
		" #{pane_current_command}  #{b:pane_current_path}#[align=right][prefix+, for actions] ")
	// Match the regular tabby pane-header colors: dark-blue bg (Default group's
	// tab color, or pane_header.active_bg fallback) + white text.
	activeFg := c.config.PaneHeader.ActiveFg
	if activeFg == "" {
		activeFg = "#ffffff"
	}
	inactiveFg := c.config.PaneHeader.InactiveFg
	if inactiveFg == "" {
		inactiveFg = activeFg
	}
	activeBg := ""
	for _, g := range c.grouped {
		if g.Name == "Default" && g.Theme.Bg != "" {
			activeBg = g.Theme.Bg
			break
		}
	}
	if activeBg == "" {
		for _, g := range c.grouped {
			if g.Theme.Bg != "" {
				activeBg = g.Theme.Bg
				break
			}
		}
	}
	if activeBg == "" {
		if c.config.PaneHeader.ActiveBg != "" {
			activeBg = c.config.PaneHeader.ActiveBg
		} else {
			activeBg = "#3498db"
		}
	}
	_ = tmuxRun("set-window-option", "-t", dash, "pane-active-border-style",
		"fg="+activeFg+",bg="+activeBg)
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-style",
		"fg="+inactiveFg+",bg="+activeBg)
	tileStyle := "fg=" + inactiveFg + ",bg=" + activeBg
	// Per content tile: set pane-border-status=top as a PANE-LOCAL option. Window-
	// level didn't hold (it inherits tabby's global 'off'); pane-local is the
	// highest-precedence scope and can't be overridden by the global. Clearing the
	// pane-local style lets the label inherit the visible global border color
	// (gathered panes carried a hidden fg=bg style from their origin windows).
	// Aux panes (sidebar) keep their borderless state.
	out := tmuxOutputTrimmed("list-panes", "-t", dash, "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		id, cur := parts[0], parts[1]
		start := ""
		if len(parts) == 3 {
			start = parts[2]
		}
		isAux := isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start)
		if isAux {
			// Keep the sidebar/aux panes borderless — no label strip on them.
			_ = tmuxRun("set-option", "-p", "-t", id, "pane-border-status", "off")
			continue
		}
		_ = tmuxRun("set-option", "-p", "-t", id, "pane-border-status", "top")
		_ = tmuxRun("set-option", "-p", "-t", id, "pane-border-style", tileStyle)
	}
}

// applyNativeBorders sets tmux's native pane-border-status row on every
// content pane of a regular (non-dashboard) window, mirroring the dashboard
// pattern from applyDashboardBorders. Called per non-dashboard window on each
// pane-layout pass when PaneHeader.Native is enabled, replacing the Bubbletea
// pane-header aux pane.
//
// Resolves the active border colour from the window's OWN group theme (not the
// Default group fallback the dashboard uses). The inactive border keeps the
// same bg as active so shared edges don't half/half between adjacent tiles,
// only the fg dims so the active pane reads as focused.
func (c *Coordinator) applyNativeBorders(winID, groupName string) {
	if winID == "" {
		return
	}
	_ = tmuxRun("set-window-option", "-t", winID, "pane-border-lines", "single")
	// pane-border-status is window-scope in tmux 3.5a (set-option -p falls
	// through to window). One status row gets allocated above every pane in
	// the window, including aux panes. We render an EMPTY format on aux panes
	// via a conditional so they show a blank strip rather than the label.
	_ = tmuxRun("set-window-option", "-t", winID, "pane-border-status", "top")
	_ = tmuxRun("set-window-option", "-t", winID, "pane-border-format",
		"#{?#{||:#{m:*sidebar*,#{pane_current_command}},#{m:*header*,#{pane_current_command}}},, #{pane_current_command}  #{b:pane_current_path}#[align=right][prefix+, for actions] }")
	activeFg := c.config.PaneHeader.ActiveFg
	if activeFg == "" {
		activeFg = "#ffffff"
	}
	inactiveFg := c.config.PaneHeader.InactiveFg
	if inactiveFg == "" {
		// Dim white for "not active" cue without changing bg.
		inactiveFg = "#bbbbbb"
	}
	activeBg := ""
	for _, g := range c.grouped {
		if g.Name == groupName && g.Theme.Bg != "" {
			activeBg = g.Theme.Bg
			break
		}
	}
	if activeBg == "" {
		for _, g := range c.grouped {
			if g.Name == "Default" && g.Theme.Bg != "" {
				activeBg = g.Theme.Bg
				break
			}
		}
	}
	if activeBg == "" {
		for _, g := range c.grouped {
			if g.Theme.Bg != "" {
				activeBg = g.Theme.Bg
				break
			}
		}
	}
	if activeBg == "" {
		if c.config.PaneHeader.ActiveBg != "" {
			activeBg = c.config.PaneHeader.ActiveBg
		} else {
			activeBg = "#3498db"
		}
	}
	_ = tmuxRun("set-window-option", "-t", winID, "pane-active-border-style",
		"fg="+activeFg+",bg="+activeBg)
	// Inactive border: lighten the bg ~60% toward white so unfocused panes
	// read as clearly dim without going flat or losing the group colour. Tmux
	// renders a brief vertical half/half stripe on edges shared with the
	// active pane, which doubles as a focus cue.
	inactiveBg := lightenHex(activeBg, 0.60)
	_ = tmuxRun("set-window-option", "-t", winID, "pane-border-style",
		"fg="+inactiveFg+",bg="+inactiveBg)
	tileStyle := "fg=" + inactiveFg + ",bg=" + inactiveBg
	out := tmuxOutputTrimmed("list-panes", "-t", winID, "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		id, cur := parts[0], parts[1]
		start := ""
		if len(parts) == 3 {
			start = parts[2]
		}
		isAux := isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start)
		if isAux {
			continue
		}
		_ = tmuxRun("set-option", "-p", "-t", id, "pane-border-style", tileStyle)
	}
}

// dashboardSession resolves the session id this coordinator manages.
func (c *Coordinator) dashboardSession() string {
	if s := strings.TrimSpace(c.sessionID); s != "" {
		return s
	}
	return tmuxOutputTrimmed("display-message", "-p", "#{session_id}")
}

// ── small helpers ───────────────────────────────────────────────────────────

func dashLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// firstToken returns the first whitespace-delimited token across all lines that
// starts with prefix (e.g. "@" for window ids, "%" for pane ids).
func firstToken(output, prefix string) string {
	for _, line := range dashLines(output) {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, prefix) {
				return f
			}
		}
	}
	return ""
}

func paneCount(windowID string) int {
	return len(dashLines(tmuxOutputTrimmed("list-panes", "-t", windowID, "-F", "#{pane_id}")))
}

// windowHasContent reports whether a window still holds at least one non-aux pane.
func windowHasContent(windowID string) bool {
	out := tmuxOutputTrimmed("list-panes", "-t", windowID, "-F",
		"#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		cur := parts[0]
		start := ""
		if len(parts) == 2 {
			start = parts[1]
		}
		if !isAuxiliaryPaneCommand(cur) && !isSidebarPaneCommand(cur, start) {
			return true
		}
	}
	return false
}
