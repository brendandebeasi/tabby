package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ResizeOp describes a single deferred tmux resize. Builders return slices
// of these instead of invoking tmux directly so the loop can batch every
// op for one logical event into a single chained tmux command, firing only
// one trailing after-resize-pane hook per cycle instead of one per op.
type ResizeOp struct {
	Kind    ResizeOpKind
	Target  string // pane id (%n) or window id (@n)
	X       int    // cols (Kind=Window or Kind=PaneX)
	Y       int    // rows (Kind=Window or Kind=PaneY)
	Layout  string // tmux layout string (Kind=SelectLayout)
	Reason  string // diagnostic — included in RECONCILE_OP log lines
	Subject string // optional client/window id for logging
}

type ResizeOpKind int

const (
	OpResizeWindow ResizeOpKind = iota // resize-window -x X -y Y -t @id
	OpResizePaneX                      // resize-pane   -x X -t %id
	OpResizePaneY                      // resize-pane   -y Y -t %id
	OpSelectLayout                     // select-layout -t @id "<layout-string>"
)

// flushOpsBatched executes ops as a single chained tmux command bracketed by
// @tabby_spawning toggles. The bracket suppresses the spawn / focus-restore
// paths from re-entering during reconciliation; the chain itself collapses
// what used to be N separate exec.Command calls (each firing its own
// after-resize-pane hook) into one. tmux processes commands serially within
// a single client connection and only redraws panes once per command, so the
// renderer pty receives a single SIGWINCH at the final state instead of one
// per op.
//
// reason is recorded in the RECONCILE_FLUSH log line for diagnostics.
func flushOpsBatched(ops []ResizeOp, reason string) {
	if len(ops) == 0 {
		return
	}

	// tmux command-line argv has a soft limit (ARG_MAX is generous on macOS
	// but each chained ` ; ` separator counts; chunk to keep argv well under
	// 1 MiB).
	const maxOpsPerChunk = 60

	for i := 0; i < len(ops); i += maxOpsPerChunk {
		end := i + maxOpsPerChunk
		if end > len(ops) {
			end = len(ops)
		}
		chunk := ops[i:end]
		args := make([]string, 0, len(chunk)*9+4)

		first := i == 0
		last := end == len(ops)

		if first {
			args = append(args, "set-option", "-g", "@tabby_spawning", "1", ";")
		}
		for j, op := range chunk {
			if j > 0 || !first {
				args = append(args, ";")
			}
			switch op.Kind {
			case OpResizeWindow:
				args = append(args, "resize-window", "-t", op.Target, "-x", fmtInt(op.X), "-y", fmtInt(op.Y))
			case OpResizePaneX:
				args = append(args, "resize-pane", "-t", op.Target, "-x", fmtInt(op.X))
			case OpResizePaneY:
				args = append(args, "resize-pane", "-t", op.Target, "-y", fmtInt(op.Y))
			case OpSelectLayout:
				args = append(args, "select-layout", "-t", op.Target, op.Layout)
			}
			logEvent("RECONCILE_OP reason=%s kind=%d target=%s x=%d y=%d subject=%s op_reason=%s",
				reason, op.Kind, op.Target, op.X, op.Y, op.Subject, op.Reason)
		}
		if last {
			args = append(args, ";", "set-option", "-g", "@tabby_spawning", "0")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := exec.CommandContext(ctx, "tmux", args...).Run()
		cancel()
		if err != nil {
			logEvent("RECONCILE_FLUSH_ERR reason=%s chunk_start=%d chunk_size=%d err=%v",
				reason, i, len(chunk), err)
		} else {
			logEvent("RECONCILE_FLUSH reason=%s chunk_start=%d chunk_size=%d", reason, i, len(chunk))
		}
	}
}

func fmtInt(i int) string { return fmt.Sprintf("%d", i) }

// planWindowSizes returns ResizeOps that lock every tmux window to the
// active client's geometry. Pure: takes width/height + window list, returns
// ops. The caller is responsible for executing them via flushOpsBatched.
//
// Replaces the per-window exec.Command loop in resizeAllWindowsToClient
// (one subprocess per window → one chained command for all windows).
func planWindowSizes(width, height int, windowIDs []string) []ResizeOp {
	if width <= 0 || height <= 0 || len(windowIDs) == 0 {
		return nil
	}
	ops := make([]ResizeOp, 0, len(windowIDs))
	for _, wid := range windowIDs {
		wid = strings.TrimSpace(wid)
		if wid == "" {
			continue
		}
		ops = append(ops, ResizeOp{
			Kind:    OpResizeWindow,
			Target:  wid,
			X:       width,
			Y:       height,
			Reason:  "active_client_geom",
			Subject: wid,
		})
	}
	return ops
}

// listAllWindowIDs queries tmux for every window id. Used by the planning
// phase of a reconcile so callers don't each have to issue their own
// list-windows.
func listAllWindowIDs() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F", "#{window_id}").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

// listSidebarPanesByWindow queries tmux once for every sidebar pane and
// returns a windowID → paneID mapping. Used by the width-sync planner so
// it can target paneIDs directly instead of issuing one list-panes per
// window during execution.
func listSidebarPanesByWindow() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-s", "-F",
		"#{pane_id}|#{window_id}|#{pane_current_command}|#{pane_start_command}").Output()
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		if isSidebarPaneCommand(parts[2], parts[3]) {
			// First match wins (each window has one sidebar pane).
			if _, dup := result[parts[1]]; !dup {
				result[parts[1]] = parts[0]
			}
		}
	}
	return result
}

// listHeaderPanes queries tmux once for every window-header / pane-header
// pane and returns ops that adjust their height to the configured target.
// windowWidthFn maps a header pane's window-width to the desired height
// (lets the loop reuse Coordinator.desiredWindowHeaderHeightForWidth for
// window-headers and the static pane-header height for pane-headers).
type headerPaneInfo struct {
	PaneID        string
	CurrentHeight int
	WindowWidth   int
	IsWindowHdr   bool
}

func listHeaderPanes() []headerPaneInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F",
		"#{pane_id}|||#{pane_height}|||#{pane_current_command}|||#{pane_start_command}|||#{window_width}").Output()
	if err != nil {
		return nil
	}
	var result []headerPaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 5)
		if len(parts) < 5 {
			continue
		}
		curCmd := parts[2]
		startCmd := parts[3]
		isWindowHeader := strings.Contains(curCmd, "window-header") || strings.Contains(startCmd, "window-header")
		isPaneHeader := strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header")
		if !isWindowHeader && !isPaneHeader {
			continue
		}
		curH, _ := atoiSafe(parts[1])
		winW, _ := atoiSafe(parts[4])
		result = append(result, headerPaneInfo{
			PaneID:        parts[0],
			CurrentHeight: curH,
			WindowWidth:   winW,
			IsWindowHdr:   isWindowHeader,
		})
	}
	return result
}

// windowLayoutSnapshot is a single (windowID, width, layout) row returned by
// snapshotWindowLayouts. Captured before a window-resize batch so multi-pane
// layouts can be restored proportionally when the same width becomes active
// again.
type windowLayoutSnapshot struct {
	WindowID string
	Width    int
	Layout   string
	Panes    int
}

// snapshotWindowLayouts queries tmux once for every window's current layout
// string + width + pane count. Single subprocess, no writes. Used by the
// reconcile planner to populate the per-(windowID, width) layout cache before
// emitting OpResizeWindow ops that would otherwise let tmux scale splits
// greedily.
func snapshotWindowLayouts() []windowLayoutSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "list-windows", "-a", "-F",
		"#{window_id}|||#{window_width}|||#{window_panes}|||#{window_layout}").Output()
	if err != nil {
		return nil
	}
	var result []windowLayoutSnapshot
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 4)
		if len(parts) != 4 {
			continue
		}
		w, err := atoiSafe(parts[1])
		if err != nil || w <= 0 {
			continue
		}
		panes, _ := atoiSafe(parts[2])
		layout := strings.TrimSpace(parts[3])
		if layout == "" {
			continue
		}
		result = append(result, windowLayoutSnapshot{
			WindowID: strings.TrimSpace(parts[0]),
			Width:    w,
			Panes:    panes,
			Layout:   layout,
		})
	}
	return result
}

// layoutOuterRe captures the outer "checksum,WxH,X,Y" prefix of a tmux layout
// string. The 5 hex digits + comma is the format; rest is the tree.
var layoutOuterRe = regexp.MustCompile(`^[0-9a-f]+,(\d+)x(\d+),\d+,\d+`)

// layoutLeafRe matches every "WxH,X,Y,paneID" leaf in a tmux layout tree.
// Non-leaf nodes have `[` or `{` after the position instead of `,paneID`.
var layoutLeafRe = regexp.MustCompile(`(\d+)x(\d+),(\d+),(\d+),(\d+)`)

// tmuxLayoutChecksum returns the 4-hex-digit checksum tmux prepends to layout
// strings. Mirrors layout-custom.c:layout_checksum in tmux: rotate the
// running 16-bit value right by 1 (LSB→MSB), then add each byte. Tmux
// rejects select-layout with a mismatched checksum, so we must compute it
// to apply a custom-built layout.
func tmuxLayoutChecksum(s string) string {
	var csum uint16
	for i := 0; i < len(s); i++ {
		csum = (csum >> 1) | ((csum & 1) << 15)
		csum += uint16(s[i])
	}
	return fmt.Sprintf("%04x", csum)
}

// buildSidebarPlusFooterLayout constructs the canonical tmux layout string
// for a tabby window with sidebar, content (pane-header + body), and a
// full-width footer bar at the bottom. Returns "" if any dimension is
// non-positive or won't fit.
//
// Pane numbers are the integer suffix of a tmux pane id (e.g. %1635 → 1635).
//
// Reference structure (from a healthy production layout):
//
//	[<top WxTopH 0,0 {sidebar SBxTopH 0,0 P1, content CWxTopH SX,0
//	     [pane-hdr CWx1 SX,0 P2, body CWxBodyH SX,2 P3]}>,
//	 <bar Wx BarH 0,BarY P4>]
func buildSidebarPlusFooterLayout(windowW, windowH, sidebarW, barH int,
	sidebarPaneNum, paneHdrNum, bodyPaneNum, barPaneNum int) string {
	if windowW <= 0 || windowH <= 0 || sidebarW <= 0 || barH <= 0 {
		return ""
	}
	topH := windowH - barH - 1 // -1 for divider between top section and bar
	if topH <= 2 {             // need at least 1 row for pane-header + 1 for body
		return ""
	}
	contentW := windowW - sidebarW - 1 // -1 for vertical divider after sidebar
	if contentW <= 0 {
		return ""
	}
	contentX := sidebarW + 1
	bodyH := topH - 2 // -1 for pane-header, -1 for divider below it
	if bodyH <= 0 {
		return ""
	}
	barY := windowH - barH

	body := fmt.Sprintf(
		"%dx%d,0,0[%dx%d,0,0{%dx%d,0,0,%d,%dx%d,%d,0[%dx%d,%d,0,%d,%dx%d,%d,2,%d]},%dx%d,0,%d,%d]",
		windowW, windowH,
		windowW, topH,
		sidebarW, topH, sidebarPaneNum,
		contentW, topH, contentX,
		contentW, 1, contentX, paneHdrNum,
		contentW, bodyH, contentX, bodyPaneNum,
		windowW, barH, barY, barPaneNum,
	)
	return tmuxLayoutChecksum(body) + "," + body
}

// looksMalformedLayout returns true when a tmux layout string contains a
// footer-shaped leaf pane (≤4 rows tall, sitting on the window's bottom edge)
// that's narrower than the window width. In tabby that pane is the
// window-header button bar, which must always span the full window width;
// when it gets nested inside a (sidebar | content) split it's only as wide
// as the content side, rendering as a "squished" bar with the sidebar to its
// left. Refusing to cache or replay such a layout prevents the corruption
// from sticking across active-client switches.
func looksMalformedLayout(layout string) bool {
	om := layoutOuterRe.FindStringSubmatch(layout)
	if om == nil {
		return false
	}
	outerW, err := strconv.Atoi(om[1])
	if err != nil || outerW <= 0 {
		return false
	}
	outerH, err := strconv.Atoi(om[2])
	if err != nil || outerH <= 0 {
		return false
	}
	for _, m := range layoutLeafRe.FindAllStringSubmatch(layout, -1) {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[4])
		// Footer signature: short pane sitting on the bottom edge of the window.
		if h > 0 && h <= 4 && y+h == outerH && w > 0 && w < outerW {
			return true
		}
	}
	return false
}

// SaveWindowLayout caches a tmux layout string under (windowID, width). Called
// before each lock-windows-to-active reconcile so the prior client's split
// proportions can be replayed when that client regains focus.
func (c *Coordinator) SaveWindowLayout(windowID string, width int, layout string) {
	if windowID == "" || width <= 0 || layout == "" {
		return
	}
	if looksMalformedLayout(layout) {
		logEvent("LAYOUT_CACHE_REJECTED windowID=%s width=%d reason=footer_squish layout=%s", windowID, width, layout)
		return
	}
	c.windowLayoutsMu.Lock()
	defer c.windowLayoutsMu.Unlock()
	m, ok := c.windowLayouts[windowID]
	if !ok {
		m = make(map[int]string)
		c.windowLayouts[windowID] = m
	}
	m[width] = layout
}

// GetWindowLayout returns the saved layout string for (windowID, width), or
// "" if none is cached.
func (c *Coordinator) GetWindowLayout(windowID string, width int) string {
	if windowID == "" || width <= 0 {
		return ""
	}
	c.windowLayoutsMu.Lock()
	defer c.windowLayoutsMu.Unlock()
	m, ok := c.windowLayouts[windowID]
	if !ok {
		return ""
	}
	layout := m[width]
	if layout == "" {
		return ""
	}
	// Re-validate on read so any pre-existing bad entry (cached before this
	// validator existed, or persisted before a daemon restart) gets
	// dropped instead of replayed forever via select-layout.
	if looksMalformedLayout(layout) {
		logEvent("LAYOUT_CACHE_DROPPED_ON_READ windowID=%s width=%d reason=footer_squish", windowID, width)
		delete(m, width)
		return ""
	}
	return layout
}

// ForgetWindowLayouts drops cached layouts for the given windowID (e.g. when
// the window is closed). No-op if windowID is empty or unknown.
func (c *Coordinator) ForgetWindowLayouts(windowID string) {
	if windowID == "" {
		return
	}
	c.windowLayoutsMu.Lock()
	defer c.windowLayoutsMu.Unlock()
	delete(c.windowLayouts, windowID)
}

// ForgetAllWindowLayouts drops every cached layout. Use after a structural
// change that invalidates pane counts across all windows — e.g. sidebar
// hide/restore (break-pane / join-pane changes pane count in every window),
// after which any cached "saved layout at width W" is for the wrong pane
// topology and replaying it via select-layout would visibly snap the user
// to a stale geometry.
func (c *Coordinator) ForgetAllWindowLayouts() {
	c.windowLayoutsMu.Lock()
	defer c.windowLayoutsMu.Unlock()
	if len(c.windowLayouts) == 0 {
		return
	}
	c.windowLayouts = make(map[string]map[int]string)
}

func atoiSafe(s string) (int, error) {
	n := 0
	neg := false
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("digits required")
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(ch-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
