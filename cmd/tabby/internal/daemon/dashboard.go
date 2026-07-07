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

// paneBorderFormat returns the tmux pane-border-format string used by BOTH
// applyDashboardBorders and applyNativeBorders so the label is identical
// across views.
//
// Layout per pane:
//   - Chrome panes (sidebar / window-header / pane-header): blank, colour-
//     neutral strip via #[align=centre,fg=default,bg=default] so the row
//     blends with the terminal default bg (no visible divider above them).
//     Matched on both #{pane_current_command} (legacy) and #{pane_start_command}
//     (consolidated tabby subcommand reports "tabby" as current command).
//   - Content panes: " <window_name> | <pane_title> ", with command + folder
//     as fallback when pane_title is empty or equals #{host_short} (the
//     daemon machine's hostname — a non-informative default). Right-aligned
//     "[prefix+, for actions]" hint on the right.
//
// `#,` is the literal escape for a comma inside #{?…} branches.
func paneBorderFormat() string {
	const chrome = "#[align=centre#,fg=default#,bg=default] "
	// Use pane_title only when it isn't trivially bash's default OSC string
	// (which contains the host short name — e.g. "b@bdm1: ~" for any home-dir
	// shell). When the title contains host_short, fall back to command + folder
	// so the strip stays informative across a gathered dashboard of bash panes.
	// The "tab name" portion prefers the origin window name (set per-pane on
	// dashboard gather as @tabby_dash_origin_name) so dashboard tiles read with
	// their origin name instead of "Dashboard" on every tile. When the option
	// is unset (non-dashboard windows, or pre-existing dashboards from before
	// this tag landed) it falls back to the live #{window_name}.
	// Prefix the window's marker (@tabby_icon) when set, so the pane-border label
	// mirrors the tab's marker — same as the sidebar row and TABBY header. The
	// #{!=…,} empty-check matches the origin-name pattern below; the trailing
	// space in the "then" branch separates the marker from the name.
	// Marker: prefer the pane-local @tabby_dash_origin_icon (set on gathered
	// dashboard tiles, whose window-scoped @tabby_icon no longer reflects their
	// origin tab) and fall back to the window's own @tabby_icon on normal windows.
	const marker = "#{?#{!=:#{@tabby_dash_origin_icon},},#{@tabby_dash_origin_icon},#{@tabby_icon}}"
	const content = " #{?#{!=:" + marker + ",}," + marker + " ,}#{?#{!=:#{@tabby_dash_origin_name},},#{@tabby_dash_origin_name},#{window_name}} #[fg=default] | #[fg=default]" +
		"#{?#{&&:#{!=:#{pane_title},}," +
		"#{&&:#{!=:#{pane_title},#{host_short}}," +
		"#{!=:#{m:*#{host_short}*,#{pane_title}},1}}}," +
		"#{pane_title}," +
		"#{pane_current_command}  #{b:pane_current_path}}" +
		"#[align=right][prefix+#, for actions] "
	const chromeMatch = "#{||:" +
		"#{m:*sidebar*,#{pane_current_command}}," +
		"#{m:*header*,#{pane_current_command}}," +
		"#{m:*sidebar*,#{pane_start_command}}," +
		"#{m:*header*,#{pane_start_command}}}"
	return "#{?" + chromeMatch + "," + chrome + "," + content + "}"
}

// dashboardPaneBorderFormat wraps the shared pane-border label so EVERY dashboard
// tile paints its OWN border colour — including the focused one. tmux has no
// per-pane active-border-style (that option is window-scoped), so we can't colour
// the active tile via set-option. Instead the format itself, evaluated per pane,
// picks the tile's colour from pane-local options set at apply time: the saturated
// tab colour when the pane is active, the lightened one when it isn't.
//
// We set BOTH `bg` and `fill` to that colour. `bg` paints the background of the
// rendered label text; `fill` paints the EMPTY space after it (tmux's align gap /
// trailing padding). With `fill` alone only the padding got coloured — the label
// text kept the default bg, which read as a stray "partial" colour on one edge.
// Together they paint the whole border line. The choice is atomic with the label
// render — no per-focus tmux writes, no flicker. fg is baked in (uniform across
// tiles) since the label already resets to default mid-string.
func dashboardPaneBorderFormat(activeFg, inactiveFg string) string {
	color := "#{?pane_active,#{@tabby_dash_bg_active},#{@tabby_dash_bg_inactive}}"
	style := "#[bg=" + color + "#,fill=" + color + "#,fg=#{?pane_active," + activeFg + "," + inactiveFg + "}]"
	return style + paneBorderFormat()
}

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
	// Sticky @tabby_* window options that define a tab's identity/appearance and
	// must survive the gather/restore round-trip — origin windows are killed and
	// recreated, so anything not snapshotted here is lost (this is why the AI
	// summary title used to vanish after a dashboard toggle). Transient state
	// (@tabby_busy/_bell/_activity/_silence/_input) is intentionally NOT captured;
	// the daemon recomputes it each tick.
	AITitle    string
	Color      string
	Icon       string
	Pinned     string
	Collapsed  string
	Minimized  string
	NameLocked string
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
		"#{window_id}\t#{window_index}\t#{window_name}\t#{@tabby_group}"+
			"\t#{@tabby_ai_title}\t#{@tabby_color}\t#{@tabby_icon}\t#{@tabby_pinned}"+
			"\t#{@tabby_collapsed}\t#{@tabby_minimized}\t#{@tabby_name_locked}")
	for _, line := range dashLines(winOut) {
		parts := strings.SplitN(line, "\t", 11)
		if len(parts) < 3 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		idx := 0
		if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			idx = n
		}
		name := parts[2]
		if id == "" || strings.HasPrefix(name, sidebarStashWindowPrefix) {
			continue
		}
		// field returns the trimmed nth part, or "" if the format emitted fewer.
		field := func(n int) string {
			if n < len(parts) {
				return strings.TrimSpace(parts[n])
			}
			return ""
		}
		snaps[id] = dashWindowSnapshot{
			Name:       name,
			Group:      field(3),
			Index:      idx,
			AITitle:    field(4),
			Color:      field(5),
			Icon:       field(6),
			Pinned:     field(7),
			Collapsed:  field(8),
			Minimized:  field(9),
			NameLocked: field(10),
		}
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

	// Move each content pane into the dashboard window, tagging its origin
	// window id AND its origin window name. The name lets the border-format
	// helper render `<origin_window_name> | <pane_title>` per tile instead of
	// the dashboard window's own name on every tile.
	//
	// Re-tile after every join: joining repeatedly into one target halves its
	// size each time, so without a reflow the Nth join eventually fails for lack
	// of space.
	for _, p := range content {
		_ = tmuxRun("set-option", "-p", "-t", p.pane, "@tabby_dash_origin", p.win)
		if s, ok := snaps[p.win]; ok {
			// Tag the origin tab's name, marker, and colour onto the pane itself.
			// These window-scoped @tabby_* options don't travel with join-pane (the
			// panes now live in the single dashboard window), so without pane-local
			// copies the border format would resolve #{@tabby_icon}/colour against
			// the dashboard window and every tile would render marker-less and in a
			// single uniform colour. The format + per-tile style read these back.
			if s.Name != "" {
				_ = tmuxRun("set-option", "-p", "-t", p.pane, "@tabby_dash_origin_name", s.Name)
			}
			if s.Icon != "" {
				_ = tmuxRun("set-option", "-p", "-t", p.pane, "@tabby_dash_origin_icon", s.Icon)
			}
			// Tag the tab's EFFECTIVE colour: a custom @tabby_color wins, else the
			// window's group theme colour — same precedence the sidebar and native
			// borders use. Capturing only the custom colour left grouped tabs (e.g.
			// Gunpowder/StudioDome) with no per-tile colour, so only windows with an
			// explicit @tabby_color painted. Windows with neither stay uncoloured.
			effColor := s.Color
			if effColor == "" && s.Group != "" {
				for _, g := range c.grouped {
					if g.Name == s.Group && g.Theme.Bg != "" {
						effColor = g.Theme.Bg
						break
					}
				}
			}
			if effColor != "" {
				_ = tmuxRun("set-option", "-p", "-t", p.pane, "@tabby_dash_origin_color", effColor)
			}
		}
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
	c.nativeBorderMu.Lock()
	c.dashboardBorderSig = "" // force the first border apply for this dashboard
	c.nativeBorderMu.Unlock()
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

	// Capture the tile the user has focused WITHIN the dashboard grid right now.
	// Its pane_id survives the join-pane round trip, so on exit we refocus the
	// window it lands in — the user's last-focused tile becomes the active window,
	// which is what they expect when toggling the dashboard off. Falls back to the
	// pre-entry pane (dashboardReturnPane) when the active pane is aux/sidebar.
	inDashActivePane := ""
	if out := tmuxOutputTrimmed("list-panes", "-t", dashID, "-F",
		"#{pane_active}\t#{pane_id}\t#{pane_current_command}\t#{pane_start_command}"); out != "" {
		for _, line := range dashLines(out) {
			parts := strings.SplitN(line, "\t", 4)
			if len(parts) < 3 || parts[0] != "1" {
				continue
			}
			cur := parts[2]
			start := ""
			if len(parts) == 4 {
				start = parts[3]
			}
			if isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start) {
				continue
			}
			inDashActivePane = parts[1]
			break
		}
	}

	// Group the dashboard's content panes by their recorded origin window.
	// originName is read from the @tabby_dash_origin_name pane tag (set at gather
	// time) so exit can restore window names even if the in-memory snapshot
	// (c.dashboardOrigins) was lost — e.g. the daemon restarted while gathered.
	groups := map[string][]string{}
	originName := map[string]string{}
	var groupOrder []string
	paneOut := tmuxOutputTrimmed("list-panes", "-t", dashID, "-F",
		"#{pane_id}\t#{@tabby_dash_origin}\t#{@tabby_dash_origin_name}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(paneOut) {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		pane, origin, oname, cur := parts[0], strings.TrimSpace(parts[1]), parts[2], parts[3]
		start := ""
		if len(parts) == 5 {
			start = parts[4]
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
		if oname != "" && originName[origin] == "" {
			originName[origin] = oname
		}
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
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_origin_name")
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_origin_icon")
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_origin_color")
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_bg_active")
			_ = tmuxRun("set-option", "-p", "-t", p, "-u", "@tabby_dash_bg_inactive")
			if err := tmuxRun("join-pane", "-d", "-h", "-s", p, "-t", ph); err != nil {
				_ = tmuxRun("join-pane", "-d", "-s", p, "-t", ph)
			}
			_ = tmuxRun("select-layout", "-t", newWin, "tiled")
		}
		if ph != "" && paneCount(newWin) > 1 {
			_ = tmuxRun("kill-pane", "-t", ph)
		}
		// Prefer the in-memory snapshot name; fall back to the pane tag so a
		// daemon that restarted while gathered still restores window names.
		name := snap.Name
		if name == "" {
			name = originName[origin]
		}
		if name != "" {
			_ = tmuxRun("rename-window", "-t", newWin, name)
		}
		if snap.Group != "" && snap.Group != "Default" {
			_ = tmuxRun("set-window-option", "-t", newWin, "@tabby_group", snap.Group)
		}
		// Restore the sticky @tabby_* options dropped when the origin window was
		// recreated — AI summary title, manual color/icon, pin/collapse/minimize,
		// name lock — so the tab looks identical after a dashboard round-trip.
		for _, o := range []struct{ name, val string }{
			{"@tabby_ai_title", snap.AITitle},
			{"@tabby_color", snap.Color},
			{"@tabby_icon", snap.Icon},
			{"@tabby_pinned", snap.Pinned},
			{"@tabby_collapsed", snap.Collapsed},
			{"@tabby_minimized", snap.Minimized},
			{"@tabby_name_locked", snap.NameLocked},
		} {
			if o.val != "" {
				_ = tmuxRun("set-window-option", "-t", newWin, o.name, o.val)
			}
		}
		_ = tmuxRun("select-layout", "-t", newWin, "tiled")
	}

	_ = tmuxRun("kill-window", "-t", dashID)

	// Restore focus: prefer the tile the user had focused inside the dashboard
	// (its id survived the round trip), falling back to the pre-dashboard pane.
	focusPane := c.dashboardReturnPane
	if inDashActivePane != "" {
		focusPane = inDashActivePane
	}
	if focusPane != "" {
		if win := tmuxOutputTrimmed("display-message", "-p", "-t", focusPane, "#{window_id}"); win != "" {
			_ = tmuxRun("select-window", "-t", win)
			_ = tmuxRun("select-pane", "-t", focusPane)
		}
	}

	c.dashboardWindowID = ""
	c.dashboardOrigins = nil
	c.dashboardOrder = nil
	c.dashboardReturnPane = ""
	c.nativeBorderMu.Lock()
	c.dashboardBorderSig = "" // next dashboard starts with a clean signature
	c.nativeBorderMu.Unlock()
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
	// Re-point the active-border edge colour at the newly focused tile. Keyboard
	// cycling sets dashboardSkipBroadcast, so the input loop returns before the
	// refresh path that would otherwise call applyDashboardBorders — do it here so
	// the focused tile's side edges update immediately. It's signature-gated, so
	// this is a no-op unless the active colour actually changed.
	c.applyDashboardBorders()
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
	// Inactive border: lighten the bg ~60% toward white so unfocused tiles
	// read as clearly dim, mirroring the regular-window applyNativeBorders
	// treatment. Adjacent edges between active + inactive tiles will show a
	// brief half/half stripe, which doubles as a focus cue.
	inactiveBg := lightenHex(activeBg, 0.60)

	// Build the per-tile plan from a single list-panes query. Each content tile
	// gets pane-border-status=top (a PANE-LOCAL option — window-level inherits
	// tabby's global 'off') plus its OWN colour, taken from the origin tab's
	// custom colour when it has one and the neutral default otherwise. We store
	// TWO colours per tile — bgActive (saturated) and bgInactive (lightened) — as
	// pane-local options; the pane-border-format's `fill` reads them via
	// #{?pane_active,…} so EVERY tile's top label bar paints its own colour.
	//
	// The side/bottom edges are the pane's border LINES, which `fill` can't reach.
	// For inactive tiles we colour them via a per-pane pane-border-style whose fg
	// AND bg are the tile colour (a solid colour edge matching the top bar). The
	// ACTIVE tile's edges come from the window-scoped pane-active-border-style —
	// there's no per-pane override — so we point it at whichever tile is currently
	// focused (activeColor below) and re-apply when focus moves. Aux panes stay bare.
	type tilePlan struct{ id, status, style, bgActive, bgInactive string }
	var plan []tilePlan
	activeColor := activeBg // pane-active-border-style bg: the focused tile's colour
	out := tmuxOutputTrimmed("list-panes", "-t", dash, "-F",
		"#{pane_id}\t#{@tabby_dash_origin_color}\t#{pane_active}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		id, originColor, paneActive, cur := parts[0], strings.TrimSpace(parts[1]), parts[2], parts[3]
		start := ""
		if len(parts) == 5 {
			start = parts[4]
		}
		if isAuxiliaryPaneCommand(cur) || isSidebarPaneCommand(cur, start) {
			plan = append(plan, tilePlan{id: id, status: "off"})
			continue
		}
		bgA, bgI := activeBg, inactiveBg
		if originColor != "" && originColor != "transparent" {
			bgA = originColor
			// Dim inactive coloured tiles only gently (15% toward white) so the
			// tab's colour stays clearly recognisable and light-on-colour label
			// text stays legible — a 60% lighten washed the colour out to near
			// white on light terminals, reading as "no colour applied".
			bgI = lightenHex(originColor, 0.15)
		}
		if paneActive == "1" {
			activeColor = bgA
		}
		plan = append(plan, tilePlan{
			id:     id,
			status: "top",
			// Solid colour edge: fg == bg so the whole 1-cell border line reads as
			// the tile colour, matching its top label bar.
			style:      "fg=" + bgI + ",bg=" + bgI,
			bgActive:   bgA,
			bgInactive: bgI,
		})
	}

	// Signature-skip: every set-window-option / set-option below forces a tmux
	// border redraw, so re-issuing identical values on each refresh flickers
	// (very visibly once tiles carry distinct colours). Nothing external clobbers
	// the dashboard window's border options in steady state — the chrome passes
	// (spawnPaneHeaders/spawnWindowHeaders/buildPaneHeaderColorArgs) all skip the
	// dashboard window — so caching is safe. activeColor is in the signature so a
	// focus change (which moves the active edge colour) re-applies, but an ordinary
	// refresh with unchanged focus collapses to zero tmux calls. Reset to "" on
	// dashboard enter/exit so the next apply re-asserts.
	var sb strings.Builder
	sb.WriteString(dash)
	sb.WriteString("|" + activeFg + "," + inactiveFg + "," + activeBg + "," + inactiveBg + "," + activeColor)
	for _, t := range plan {
		sb.WriteString("|" + t.id + ":" + t.status + ":" + t.style + ":" + t.bgActive + ":" + t.bgInactive)
	}
	sig := sb.String()
	c.nativeBorderMu.Lock()
	unchanged := sig == c.dashboardBorderSig
	if !unchanged {
		c.dashboardBorderSig = sig
	}
	c.nativeBorderMu.Unlock()
	if unchanged {
		return
	}

	// Window-level styling. The dashboard uses its own format (per-tile `fill`)
	// while sharing the label body with applyNativeBorders: chrome panes blank,
	// content panes show window-name | pane-title (or command + folder fallback).
	// The active-border-style bg is the focused tile's own colour (solid edge).
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-lines", "single")
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-format", dashboardPaneBorderFormat(activeFg, inactiveFg))
	_ = tmuxRun("set-window-option", "-t", dash, "pane-active-border-style",
		"fg="+activeColor+",bg="+activeColor)
	_ = tmuxRun("set-window-option", "-t", dash, "pane-border-style",
		"fg="+inactiveFg+",bg="+inactiveBg)

	// Per-tile styling: the pane-local colours the format's `fill` reads, plus the
	// solid colour edge style and the border-status row.
	for _, t := range plan {
		if t.status == "off" {
			_ = tmuxRun("set-option", "-p", "-t", t.id, "pane-border-status", "off")
			continue
		}
		_ = tmuxRun("set-option", "-p", "-t", t.id, "@tabby_dash_bg_active", t.bgActive)
		_ = tmuxRun("set-option", "-p", "-t", t.id, "@tabby_dash_bg_inactive", t.bgInactive)
		_ = tmuxRun("set-option", "-p", "-t", t.id, "pane-border-status", "top")
		_ = tmuxRun("set-option", "-p", "-t", t.id, "pane-border-style", t.style)
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
	activeFg := c.config.PaneHeader.ActiveFg
	if activeFg == "" {
		activeFg = "#ffffff"
	}
	inactiveFg := c.config.PaneHeader.InactiveFg
	if inactiveFg == "" {
		inactiveFg = "#bbbbbb"
	}
	// The border colour tracks the TAB colour: a window's own custom @tabby_color
	// wins, exactly as it does for the sidebar row and window header. Only when the
	// window carries no custom colour do we fall back to its group theme (then
	// Default, then any group, then config / hardcoded).
	activeBg := ""
	for _, g := range c.grouped {
		for _, w := range g.Windows {
			if w.ID == winID {
				if cc := strings.TrimSpace(w.CustomColor); cc != "" && cc != "transparent" {
					activeBg = cc
				}
				break
			}
		}
		if activeBg != "" {
			break
		}
	}
	if activeBg == "" {
		for _, g := range c.grouped {
			if g.Name == groupName && g.Theme.Bg != "" {
				activeBg = g.Theme.Bg
				break
			}
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
	inactiveBg := lightenHex(activeBg, 0.60)

	// Cache the per-window signature so we don't re-issue 5 set-window-option
	// calls every refresh — these were a big chunk of the tab-switch latency
	// (5 windows × 5 options ≈ 25 tmux execs per layout pass). The signature
	// covers everything that actually goes into the tmux options below; if
	// nothing changed we skip the batched set entirely.
	sig := activeFg + "|" + inactiveFg + "|" + activeBg + "|" + inactiveBg
	c.nativeBorderMu.Lock()
	if c.nativeBorderSig == nil {
		c.nativeBorderSig = make(map[string]string)
	}
	prev := c.nativeBorderSig[winID]
	c.nativeBorderSig[winID] = sig
	c.nativeBorderMu.Unlock()
	if prev == sig {
		return
	}

	// Batch all five window-option sets into a single tmux invocation. Tmux's
	// command separator `;` (passed as a literal argv element) lets us send
	// `set-window-option … ; set-window-option … ; …` in one exec — saves a
	// handful of fork/wait round trips per window per refresh.
	args := []string{
		"set-window-option", "-t", winID, "pane-border-lines", "single",
		";", "set-window-option", "-t", winID, "pane-border-status", "top",
		";", "set-window-option", "-t", winID, "pane-border-format", paneBorderFormat(),
		";", "set-window-option", "-t", winID, "pane-active-border-style",
		"fg=" + activeFg + ",bg=" + activeBg,
		";", "set-window-option", "-t", winID, "pane-border-style",
		"fg=" + inactiveFg + ",bg=" + inactiveBg,
	}
	_ = tmuxRun(args...)
}

// InvalidateNativeBorderCache clears the per-window native-border signature
// cache so the next applyNativeBorders pass re-issues the tmux set-options.
// Called when something that *could* externally clobber the per-window
// pane-border-* options happens (theme reload, daemon respawn, manual
// option-unset path) so we don't sit on a stale cache.
func (c *Coordinator) InvalidateNativeBorderCache() {
	c.nativeBorderMu.Lock()
	c.nativeBorderSig = nil
	c.nativeBorderMu.Unlock()
}

// maybeExitDashboardForPhone exits the gathered dashboard grid if (a) a
// dashboard is currently active and (b) at least one attached tmux client is
// phone-class (width < 100). Called on tmux client-attached so phone users
// land back in their normal windows the moment they connect, even when a
// desktop client stays the "active" one (which keeps the profile-transition
// path from firing).
func (c *Coordinator) maybeExitDashboardForPhone() {
	if c.dashboardWindowID == "" {
		return
	}
	out, err := tmuxOutputCtx("list-clients", "-F", "#{client_width}")
	if err != nil {
		return
	}
	hasPhone := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		w, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if w > 0 && w < 100 {
			hasPhone = true
			break
		}
	}
	if !hasPhone {
		return
	}
	c.exitDashboard()
	coordinatorDebugLog.Printf("phone client attached: auto-exited dashboard")
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
