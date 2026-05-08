package daemon

import (
	"context"
	"fmt"
	"os/exec"
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
	Reason  string // diagnostic — included in RECONCILE_OP log lines
	Subject string // optional client/window id for logging
}

type ResizeOpKind int

const (
	OpResizeWindow ResizeOpKind = iota // resize-window -x X -y Y -t @id
	OpResizePaneX                      // resize-pane   -x X -t %id
	OpResizePaneY                      // resize-pane   -y Y -t %id
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
