package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	zone "github.com/lrstanley/bubblezone"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

var crashLog *log.Logger
var eventLog *log.Logger
var inputLog *log.Logger
var daemonStartTime time.Time

// lastNewWindowCreation tracks when the coordinator last created a new window.
// The refresh loop uses this to skip selectContentPaneInActiveWindow() calls
// that would race with the new-window handler's own focus management.
var lastNewWindowCreation time.Time

// rotateLogFile rotates a log file if it exceeds maxBytes.
// Keeps one .prev backup. Returns nil on success or if file is small enough.
func rotateLogFile(path string, maxBytes int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return
	}
	prevPath := path + ".prev"
	os.Remove(prevPath)
	os.Rename(path, prevPath)
}

// rotateLogs rotates all tabby log files on startup to prevent unbounded growth.
func rotateLogs(sessionID string) {
	// Rotate session-specific logs
	rotateLogFile(daemon.RuntimePath(sessionID, "-events.log"), 1*1024*1024) // 1MB
	rotateLogFile(daemon.RuntimePath(sessionID, "-crash.log"), 512*1024)     // 512KB
	rotateLogFile(daemon.RuntimePath(sessionID, "-input.log"), 1*1024*1024)  // 1MB
	rotateLogFile("/tmp/tabby-debug.log", 50*1024*1024)                      // 50MB
	rotateLogFile("/tmp/tabby-indicator-debug.log", 5*1024*1024)             // 5MB
}

// checkPreviousCrash detects if the previous daemon died abnormally and logs forensics.
func checkPreviousCrash(sessionID string) {
	pidPath := daemon.RuntimePath(sessionID, ".pid")
	heartbeatPath := daemon.RuntimePath(sessionID, ".heartbeat")

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return // No previous PID file — clean start
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return
	}

	// Check if process is still alive
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if proc.Signal(syscall.Signal(0)) == nil {
		return // Previous daemon still running (will be handled by PID claim logic)
	}

	// Previous daemon is dead — log forensics
	crashLog.Printf("=== PREVIOUS DAEMON DIED ABNORMALLY ===")
	crashLog.Printf("Previous PID: %d (dead)", pid)

	// Check heartbeat file for last-alive timestamp
	if hbData, err := os.ReadFile(heartbeatPath); err == nil {
		parts := strings.SplitN(strings.TrimSpace(string(hbData)), "\n", 2)
		if len(parts) >= 1 {
			crashLog.Printf("Last heartbeat: %s", parts[0])
		}
	}

	// Read last 20 lines of the previous events log for context
	eventsPath := daemon.RuntimePath(sessionID, "-events.log")
	// Check .prev first (in case we just rotated), then current
	for _, path := range []string{eventsPath + ".prev", eventsPath} {
		if evData, err := os.ReadFile(path); err == nil && len(evData) > 0 {
			lines := strings.Split(strings.TrimSpace(string(evData)), "\n")
			start := 0
			if len(lines) > 20 {
				start = len(lines) - 20
			}
			crashLog.Printf("Last events from %s:", filepath.Base(path))
			for _, line := range lines[start:] {
				crashLog.Printf("  %s", line)
			}
			break // Only show one file
		}
	}

	crashLog.Printf("=== END PREVIOUS CRASH FORENSICS ===")
	logEvent("PREVIOUS_CRASH pid=%d", pid)
}

// writeHeartbeatLoop periodically writes a heartbeat file with PID and timestamp.
// External tools (or the next daemon startup) can check this to detect hangs.
func writeHeartbeatLoop(sessionID string, done <-chan struct{}) {
	heartbeatPath := daemon.RuntimePath(sessionID, ".heartbeat")
	pid := os.Getpid()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	writeHeartbeat := func() {
		content := fmt.Sprintf("%s\npid=%d\nuptime=%s\n",
			time.Now().Format(time.RFC3339),
			pid,
			time.Since(daemonStartTime).Truncate(time.Second))
		os.WriteFile(heartbeatPath, []byte(content), 0644)
	}

	writeHeartbeat() // Write immediately on start
	for {
		select {
		case <-ticker.C:
			writeHeartbeat()
		case <-done:
			os.Remove(heartbeatPath)
			return
		}
	}
}

func initCrashLog(sessionID string) {
	crashLogPath := daemon.RuntimePath(sessionID, "-crash.log")
	f, err := os.OpenFile(crashLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		crashLog = log.New(os.Stderr, "[CRASH] ", log.LstdFlags)
		return
	}
	crashLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func initEventLog(sessionID string) {
	eventLogPath := daemon.RuntimePath(sessionID, "-events.log")
	f, err := os.OpenFile(eventLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		eventLog = log.New(os.Stderr, "[EVENT] ", log.LstdFlags)
		return
	}
	eventLog = log.New(f, "[event] ", log.LstdFlags|log.Lmicroseconds)
}

func logEvent(format string, args ...interface{}) {
	if eventLog != nil {
		eventLog.Printf(format, args...)
	}
}

var inputLogEnabled bool
var inputLogCheckTime time.Time

func initInputLog(sessionID string) {
	inputLogPath := daemon.RuntimePath(sessionID, "-input.log")
	f, err := os.OpenFile(inputLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		inputLog = log.New(os.Stderr, "[INPUT] ", log.LstdFlags)
		return
	}
	inputLog = log.New(f, "[input] ", log.LstdFlags|log.Lmicroseconds)
}

// isInputLogEnabled checks if input logging is enabled via tmux option @tabby_input_log
// Caches result for 10 seconds to avoid excessive tmux calls
func isInputLogEnabled() bool {
	if time.Since(inputLogCheckTime) > 10*time.Second {
		out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_input_log").Output()
		if err != nil {
			inputLogEnabled = false
		} else {
			val := strings.TrimSpace(string(out))
			inputLogEnabled = val == "on" || val == "1" || val == "true"
		}
		inputLogCheckTime = time.Now()
	}
	return inputLogEnabled
}

func logInput(format string, args ...interface{}) {
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf(format, args...)
	}
}

func logCrash(context string, r interface{}) {
	crashLog.Printf("=== CRASH in %s ===", context)
	crashLog.Printf("Panic: %v", r)
	crashLog.Printf("Stack trace:\n%s", debug.Stack())
	crashLog.Printf("=== END CRASH ===\n")
}

func recoverAndLog(context string) {
	if r := recover(); r != nil {
		logCrash(context, r)
	}
}

var (
	sessionID = flag.String("session", "", "tmux session ID")
	debugMode = flag.Bool("debug", false, "Enable debug logging")
)

var debugLog *log.Logger

// getRendererBin returns the path to the sidebar-renderer binary
func getRendererBin() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, "sidebar-renderer")
}

// getPaneHeaderBin returns the path to the pane-header binary
func getPaneHeaderBin() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, "pane-header")
}

// getPopupBin returns the path to the tabby-sidebar-popup binary
func getPopupBin() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, "tabby-sidebar-popup")
}

// saveLayoutBeforeKill saves the current window layout for a pane's window
// so that tabby-hook preserve-pane-ratios can restore it after the kill.
// This MUST be called before user/content-pane kill-pane operations.
func saveLayoutBeforeKill(paneID string) {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}\x1f#{window_layout}").Output()
	if err != nil {
		return
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "\x1f", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return
	}
	exec.Command("tmux", "set-option", "-g", fmt.Sprintf("@tabby_layout_%s", parts[0]), parts[1]).Run()
}

// markSkipPreserveForWindow tells tabby-hook preserve-pane-ratios to skip exactly once
// for a specific window. Use this for daemon-managed system-pane cleanup
// (headers/sidebar) where restoring a saved layout can corrupt mixed splits.
func markSkipPreserveForWindow(paneID string) {
	windowOut, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	windowID := strings.TrimSpace(string(windowOut))
	if windowID == "" {
		return
	}
	exec.Command("tmux", "set-option", "-g", fmt.Sprintf("@tabby_skip_preserve_%s", windowID), "1").Run()
}

// layoutStatePath returns the path to the persistent layout state file.
func layoutStatePath() string {
	return paths.StatePath("pane_layouts.json")
}

// saveLayoutsToDisk persists all current window layouts to disk.
// Called periodically to survive daemon restarts.
func saveLayoutsToDisk(windows []tmux.Window) {
	layouts := make(map[string]string)
	for _, win := range windows {
		if win.Layout != "" {
			layouts[win.ID] = win.Layout
		}
	}
	if len(layouts) == 0 {
		return
	}
	data, err := json.Marshal(layouts)
	if err != nil {
		return
	}
	paths.EnsureStateDir()
	os.WriteFile(layoutStatePath(), data, 0644)
}

// restoreLayoutsFromDisk loads saved layouts and sets them as tmux options
// so that tabby-hook preserve-pane-ratios can use them after header spawning.
func restoreLayoutsFromDisk() {
	data, err := os.ReadFile(layoutStatePath())
	if err != nil {
		return
	}
	var layouts map[string]string
	if err := json.Unmarshal(data, &layouts); err != nil {
		return
	}
	for windowID, layout := range layouts {
		exec.Command("tmux", "set-option", "-g", fmt.Sprintf("@tabby_layout_%s", windowID), layout).Run()
	}
}

// computeResponsiveSidebarWidth applies responsive breakpoint logic given all parameters.
// This is the pure, testable core of responsiveSidebarWidth.
func computeResponsiveSidebarWidth(windowWidth, mobileMax, tabletMax, mobileWidth, tabletWidth, desktopWidth, maxPercent, minContentCols int) int {
	return tmux.ComputeResponsiveSidebarWidth(windowWidth, mobileMax, tabletMax, mobileWidth, tabletWidth, desktopWidth, maxPercent, minContentCols)
}

// responsiveSidebarWidth computes the appropriate sidebar width for a given window,
// accounting for mobile/tablet/desktop breakpoints and content constraints.
// windowID: the tmux window ID to compute width for
// globalWidth: the saved global sidebar width (typically 25)
// Returns the responsive width as an int.
func responsiveSidebarWidth(windowID string, globalWidth int) int {
	return tmux.ResponsiveSidebarWidth(windowID, globalWidth)
}

// spawnRenderersForNewWindows checks for windows without renderers and spawns them.
// Returns true if any renderer was spawned (caller should restore focus afterward).
// The coordinator is used to compute the bounded sidebar width, ensuring the spawn
// uses the same width calculation as RunWidthSync (prevents resize churn on startup).
func spawnRenderersForNewWindows(server *daemon.Server, sessionID string, windows []tmux.Window, coordinator *Coordinator) bool {
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		logEvent("SPAWN_SKIP script_lock_active")
		return false
	}
	rendererBin := getRendererBin()
	if rendererBin == "" {
		return false
	}
	spawned := false

	// Use coordinator's globalWidth for consistency with RunWidthSync.
	// This ensures sidebars spawn at the same width that RunWidthSync will use,
	// preventing resize churn on startup.
	globalWidth := coordinator.GetGlobalWidth()

	// Get the currently active window so we only select-pane in it
	// We can't easily cache this as it changes frequently, but one query is better than N
	activeWindowOut, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	activeWindow := strings.TrimSpace(string(activeWindowOut))

	// Get connected clients (each identified by their window ID)
	connectedClients := make(map[string]bool)
	for _, clientID := range server.GetAllClientIDs() {
		connectedClients[clientID] = true
	}

	debugLog.Printf("spawnRenderers: active=%s clients=%v", activeWindow, connectedClients)

	// Check each window
	for _, win := range windows {
		windowID := win.ID
		if windowID == "" {
			continue
		}

		// Skip if already has a renderer
		if connectedClients[windowID] {
			logEvent("SPAWN_CHECK window=%s result=skip_has_client", windowID)
			continue
		}

		// Live check: query tmux directly for ANY sidebar/renderer pane in this window.
		// The cached win.Panes has sidebar panes filtered out by ListWindowsWithPanes,
		// so we must ask tmux directly. This also catches renderers from other daemons.
		// Dead system panes (from a crashed daemon) are killed here so focus can escape them.
		hasRenderer := false
		if rawOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_id}\x1f#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output(); err == nil {
			for _, rawLine := range strings.Split(strings.TrimSpace(string(rawOut)), "\n") {
				if rawLine == "" {
					continue
				}
				rawParts := strings.SplitN(rawLine, "\x1f", 4)
				if len(rawParts) < 4 {
					continue
				}
				paneID := rawParts[0]
				dead := rawParts[1] == "1"
				curCmd := rawParts[2]
				startCmd := rawParts[3]
				isSystem := strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") ||
					strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer")
				if dead && isSystem {
					// Kill dead system panes so tmux moves focus to the content pane
					logEvent("CLEANUP_DEAD_SYSTEM_PANE window=%s pane=%s cmd=%s", windowID, paneID, curCmd)
					exec.Command("tmux", "kill-pane", "-t", paneID).Run()
					continue
				}
				if !dead && isSystem {
					hasRenderer = true
					break
				}
			}
		}
		if hasRenderer {
			logEvent("SPAWN_CHECK window=%s result=skip_has_pane", windowID)
			continue
		}

		// Get first pane in window for splitting (use cached panes)
		if len(win.Panes) == 0 {
			continue
		}
		firstPane := win.Panes[0].ID

		// Compute bounded width for this window using coordinator's logic.
		// This ensures the spawn uses the same width calculation as RunWidthSync,
		// preventing resize churn during startup.
		width := coordinator.boundedSidebarWidthForWindow(windowID, globalWidth, 0)

		// Spawn renderer in this window
		// Log active pane before spawn for debugging focus issues
		// Optimization: We can skip this log or assume activeWindow is correct enough
		logEvent("SPAWN_RENDERER window=%s pane=%s width=%d", windowID, firstPane, width)
		debugLog.Printf("Spawning renderer for new window %s (pane %s) with width %d", windowID, firstPane, width)

		// Use exec to replace shell with renderer (matches toggle_sidebar_daemon.sh behavior)
		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && exec '%s' -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
		cmd := exec.Command("tmux", "split-window", "-d", "-t", firstPane, "-h", "-b", "-f", "-l", fmt.Sprintf("%d", width), "-P", "-F", "#{pane_id}", cmdStr)
		paneOut, err := cmd.CombinedOutput()
		if err != nil {
			debugLog.Printf("Failed to spawn renderer: %v, output: %s", err, string(paneOut))
			continue
		}
		newPaneID := strings.TrimSpace(string(paneOut))
		if newPaneID != "" {
			exec.Command("tmux", "set-option", "-p", "-t", newPaneID, "pane-border-status", "off").Run()
		}

		spawned = true
		logEvent("SPAWN_COMPLETE window=%s pane=%s", windowID, newPaneID)
	}
	return spawned
}

// cleanupSidebarsForClosedWindows removes sidebar panes from windows that no longer exist
func cleanupSidebarsForClosedWindows(server *daemon.Server, windows []tmux.Window) {
	currentWindows := make(map[string]bool)
	for _, win := range windows {
		currentWindows[win.ID] = true
	}

	// Check each connected client - if their window no longer exists, disconnect them
	for _, clientID := range server.GetAllClientIDs() {
		// Clients are usually window IDs (e.g. "@1") or "header:@1"
		targetID := clientID
		if strings.HasPrefix(clientID, "header:") {
			// For headers, we need to map back to the window.
			// But currently headers are tracked by pane ID in clientID?
			// Actually, clientID for renderers IS the window ID.
			// ClientID for headers is "header:<paneID>".
			// We can't easily check if a pane exists from just window list efficiently
			// without iterating all panes.
			// But since we have all panes in 'windows', we can check.
			continue // Skip headers for now, let them die naturally when pane dies
		}

		if !currentWindows[targetID] {
			debugLog.Printf("Window %s no longer exists, client will be cleaned up", clientID)
			// The client will disconnect when the pane closes
		}
	}
}

// cleanupOrphanedSidebars closes sidebar panes in windows where all other panes were closed
func cleanupOrphanedSidebars(windows []tmux.Window) {
	// Skip cleanup during new window creation to prevent killing windows
	// whose content pane hasn't been detected yet (race condition).
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_new_window_id").Output(); err == nil {
		if strings.TrimSpace(string(out)) != "" {
			return
		}
	}

	for _, win := range windows {
		windowID := win.ID
		if windowID == "" {
			continue
		}

		paneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_id}\x1f#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		if err != nil {
			continue
		}

		hasSidebar := false
		nonSystemLive := 0
		for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\x1f", 4)
			if len(parts) < 4 {
				continue
			}
			dead := parts[1] == "1"
			cmd := parts[2]
			startCmd := parts[3]

			if strings.Contains(cmd, "sidebar") || strings.Contains(startCmd, "sidebar") ||
				strings.Contains(cmd, "renderer") || strings.Contains(startCmd, "renderer") {
				hasSidebar = true
			}
			if dead {
				continue
			}
			if !paneIsSystemPane(cmd, startCmd) {
				nonSystemLive++
			}
		}

		if hasSidebar && nonSystemLive == 0 {
			currentWindow := ""
			if curOut, curErr := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); curErr == nil {
				currentWindow = strings.TrimSpace(string(curOut))
			}
			if currentWindow == windowID {
				exec.Command("tmux", "last-window").Run()
			}
			logEvent("CLEANUP_ORPHAN_SIDEBAR_WINDOW window=%s", windowID)
			debugLog.Printf("Window %s has only system panes, closing window", windowID)
			exec.Command("tmux", "kill-window", "-t", windowID).Run()
		}
	}
}

func paneIsSystemPane(cmd string, startCmd string) bool {
	return strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") ||
		strings.Contains(cmd, "tabby") || strings.Contains(cmd, "pane-header") ||
		strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
		strings.Contains(startCmd, "tabby") || strings.Contains(startCmd, "pane-header")
}

var orphanWindowFirstSeen = map[string]time.Time{}

func cleanupOrphanWindowsByTmux(sessionID string) {
	if sessionID == "" {
		return
	}

	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_enable_orphan_window_kill").Output(); err != nil || strings.TrimSpace(string(out)) != "1" {
		return
	}

	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_new_window_id").Output(); err == nil {
		if strings.TrimSpace(string(out)) != "" {
			return
		}
	}

	out, err := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}").Output()
	if err != nil {
		return
	}
	now := time.Now()
	seen := make(map[string]bool)

	for _, rawWid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		windowID := strings.TrimSpace(rawWid)
		if windowID == "" {
			continue
		}
		seen[windowID] = true

		paneOut, paneErr := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		if paneErr != nil {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(paneOut)), "\n")
		if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}

		hasSidebar := false
		nonSystemLive := 0
		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\x1f", 3)
			if len(parts) < 3 {
				continue
			}
			dead := parts[0] == "1"
			cmd := parts[1]
			startCmd := parts[2]
			if strings.Contains(cmd, "sidebar") || strings.Contains(startCmd, "sidebar") {
				hasSidebar = true
			}
			if dead {
				continue
			}
			if !paneIsSystemPane(cmd, startCmd) {
				nonSystemLive++
			}
		}

		if !(hasSidebar && nonSystemLive == 0) {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}

		firstSeen, ok := orphanWindowFirstSeen[windowID]
		if !ok {
			orphanWindowFirstSeen[windowID] = now
			continue
		}
		if now.Sub(firstSeen) < 5*time.Second {
			continue
		}

		confirmOut, confirmErr := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		if confirmErr != nil {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}
		confirmHasSidebar := false
		confirmNonSystemLive := 0
		for _, line := range strings.Split(strings.TrimSpace(string(confirmOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\x1f", 3)
			if len(parts) < 3 {
				continue
			}
			dead := parts[0] == "1"
			cmd := parts[1]
			startCmd := parts[2]
			if strings.Contains(cmd, "sidebar") || strings.Contains(startCmd, "sidebar") {
				confirmHasSidebar = true
			}
			if dead {
				continue
			}
			if !paneIsSystemPane(cmd, startCmd) {
				confirmNonSystemLive++
			}
		}
		if !(confirmHasSidebar && confirmNonSystemLive == 0) {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}

		currentWindow := ""
		if curOut, curErr := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); curErr == nil {
			currentWindow = strings.TrimSpace(string(curOut))
		}
		if currentWindow == windowID {
			exec.Command("tmux", "last-window").Run()
		}
		logEvent("CLEANUP_ORPHAN_WINDOW window=%s source=daemon_fallback", windowID)
		exec.Command("tmux", "kill-window", "-t", windowID).Run()
		delete(orphanWindowFirstSeen, windowID)
	}

	for wid := range orphanWindowFirstSeen {
		if !seen[wid] {
			delete(orphanWindowFirstSeen, wid)
		}
	}
}

// spawnPaneHeaders spawns header panes above each content pane in all windows.
// Each content pane gets its own header showing that pane's info and action buttons.
// Height is 1 line normally, or 2 lines when custom_border is enabled (to render our own border).
func spawnPaneHeaders(server *daemon.Server, sessionID string, customBorder bool, headerHeightRows int, windows []tmux.Window) {
	// NOTE: No @tabby_spawning check here — the caller (doPaneLayoutOps) already
	// holds the lock. Checking it here would self-deadlock since the caller sets
	// @tabby_spawning=1 before calling us.
	headerBin := getPaneHeaderBin()
	if headerBin == "" {
		return
	}

	// Check if pane headers are enabled
	out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	if err != nil || strings.TrimSpace(string(out)) != "on" {
		return
	}

	panesWithHeader := make(map[string]bool) // content paneID -> has header

	if headerOut, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output(); err == nil {
		headersByTarget := make(map[string][]string)
		for _, line := range strings.Split(strings.TrimSpace(string(headerOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\x1f", 3)
			if len(parts) < 3 {
				continue
			}
			paneID := parts[0]
			curCmd := parts[1]
			startCmd := parts[2]
			if !strings.Contains(curCmd, "pane-header") && !strings.Contains(startCmd, "pane-header") {
				continue
			}
			target := paneTargetFromStartCmd(startCmd)
			if target == "" {
				continue
			}
			panesWithHeader[target] = true
			headersByTarget[target] = append(headersByTarget[target], paneID)
		}

		for target, headerPanes := range headersByTarget {
			if len(headerPanes) <= 1 {
				continue
			}
			for _, extraPane := range headerPanes[1:] {
				logEvent("HEADER_DEDUP target=%s kill=%s", target, extraPane)
				markSkipPreserveForWindow(extraPane)
				exec.Command("tmux", "kill-pane", "-t", extraPane).Run()
			}
		}
	}

	// First pass: identify system panes vs content panes
	type paneEntry struct {
		id       string
		windowID string
		height   int
		width    int
	}
	var contentPanes []paneEntry

	// Flatten all panes from all windows
	for _, win := range windows {
		for _, p := range win.Panes {
			curCmd := p.Command
			startCmd := p.StartCommand

			// Check if this is a system pane (sidebar/renderer/header/daemon)
			// by checking BOTH current command and start command
			isSystem := strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") ||
				strings.Contains(curCmd, "pane-header") || strings.Contains(curCmd, "tabby") ||
				strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
				strings.Contains(startCmd, "pane-header") || strings.Contains(startCmd, "tabby")

			if isSystem {
				// Track which content panes already have a header process running
				if strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
					target := paneTargetFromStartCmd(startCmd)
					if target != "" {
						panesWithHeader[target] = true
					}
				}
				continue
			}

			contentPanes = append(contentPanes, paneEntry{
				id:       p.ID,
				windowID: win.ID,
				height:   p.Height,
				width:    p.Width,
			})
		}
	}

	// Second pass: spawn a header for each content pane that doesn't have one
	spawned := false
	spawnedInWindow := make(map[string]bool) // track windows we spawned in for cleanup
	for _, pane := range contentPanes {
		// Skip if this pane already has a header
		if panesWithHeader[pane.id] {
			continue
		}

		// Pane must be tall enough to split off a 1-line header
		if pane.height < 3 {
			continue
		}

		// Spawn header pane above this content pane
		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		// Log active pane before spawn for debugging focus issues
		activeBeforeHeader := ""
		if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			activeBeforeHeader = strings.TrimSpace(string(out))
		}
		logEvent("SPAWN_HEADER pane=%s window=%s active_before=%s width=%d height=%d custom_border=%v header_rows=%d", pane.id, pane.windowID, activeBeforeHeader, pane.width, pane.height, customBorder, headerHeightRows)
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && exec '%s' -session '%s' -pane '%s' %s", headerBin, sessionID, pane.id, debugFlag)
		spawnCmd := exec.Command("tmux", "split-window", "-d", "-t", pane.id, "-v", "-b", "-l", fmt.Sprintf("%d", headerHeightRows), cmdStr)
		if out, err := spawnCmd.CombinedOutput(); err != nil {
			debugLog.Printf("Failed to spawn pane header for %s: %v, output: %s", pane.id, err, string(out))
			continue
		}
		spawned = true
		spawnedInWindow[pane.windowID] = true
	}

	// Disable pane borders on all newly spawned header panes
	if spawned {
		for winID := range spawnedInWindow {
			headerPaneOut, err := exec.Command("tmux", "list-panes", "-t", winID, "-F",
				"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
			if err == nil {
				for _, hLine := range strings.Split(string(headerPaneOut), "\n") {
					hLine = strings.TrimSpace(hLine)
					if strings.Contains(hLine, "pane-header") {
						hParts := strings.SplitN(hLine, "\x1f", 3)
						if len(hParts) >= 1 {
							hTarget := "1"
							if globalCoordinator != nil {
								hTarget = fmt.Sprintf("%d", globalCoordinator.desiredPaneHeaderHeight())
							}
							exec.Command("tmux", "resize-pane", "-t", hParts[0], "-y", hTarget).Run()
							exec.Command("tmux", "set-option", "-p", "-t", hParts[0], "pane-border-status", "off").Run()
							exec.Command("tmux", "set-option", "-p", "-t", hParts[0], "pane-border-lines", "off").Run()
						}
					}
				}
			}
		}
	}
}

// paneTargetRegex extracts the -pane argument from a pane-header start command
var paneTargetRegex = regexp.MustCompile(`-pane\s+(?:'([^']+)'|"([^"]+)"|([^\s]+))`)

func paneTargetFromStartCmd(startCmd string) string {
	matches := paneTargetRegex.FindStringSubmatch(startCmd)
	if len(matches) < 2 {
		return ""
	}
	for i := 1; i < len(matches); i++ {
		if matches[i] != "" {
			return matches[i]
		}
	}
	return ""
}

// cleanupOrphanedHeaders removes header panes that are disabled or orphaned
// (target pane no longer exists).
func cleanupOrphanedHeaders(customBorder bool, coordinator *Coordinator, activeWindowID string) {
	// Get all panes with geometry, start command, and window ID.
	// Scoped to our session with -t to avoid cross-session interference.
	listArgs := []string{"list-panes", "-s"}
	if *sessionID != "" {
		listArgs = append(listArgs, "-t", *sessionID)
	}
	listArgs = append(listArgs, "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_width}\x1f#{pane_start_command}\x1f#{pane_height}\x1f#{pane_top}\x1f#{pane_left}\x1f#{window_id}")
	out, err := exec.Command("tmux", listArgs...).Output()
	if err != nil {
		return
	}

	// Check if pane headers are disabled
	enabledOut, _ := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	headersEnabled := strings.TrimSpace(string(enabledOut)) == "on"

	// Build a set of content pane IDs and track their dimensions
	// Check both current and start commands to handle race condition after split-window
	contentPaneExists := make(map[string]bool)
	contentPaneDimensions := make(map[string]struct{ width, height, top, left int })
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 8)
		if len(parts) < 8 {
			continue
		}
		paneID := parts[0]
		curCmd := parts[1]
		widthStr := parts[2]
		startCmd := parts[3]
		heightStr := parts[4]
		topStr := parts[5]
		leftStr := parts[6]
		isSystem := strings.Contains(curCmd, "pane-header") || strings.Contains(curCmd, "sidebar") ||
			strings.Contains(curCmd, "renderer") || strings.Contains(curCmd, "tabby") ||
			strings.Contains(startCmd, "pane-header") || strings.Contains(startCmd, "sidebar") ||
			strings.Contains(startCmd, "renderer") || strings.Contains(startCmd, "tabby")
		if !isSystem {
			contentPaneExists[paneID] = true
			width, _ := strconv.Atoi(widthStr)
			height, _ := strconv.Atoi(heightStr)
			top, _ := strconv.Atoi(topStr)
			left, _ := strconv.Atoi(leftStr)
			contentPaneDimensions[paneID] = struct{ width, height, top, left int }{width, height, top, left}
		}
	}

	// Collect header panes with their dimensions
	type headerInfo struct {
		paneID   string
		windowID string
		target   string
		width    int
		height   int
		top      int
		left     int
	}
	var headers []headerInfo

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 8)
		if len(parts) < 8 {
			continue
		}
		curCmd := parts[1]
		widthStr := parts[2]
		startCmd := parts[3]
		if !strings.Contains(curCmd, "pane-header") && !strings.Contains(startCmd, "pane-header") {
			continue
		}
		w, _ := strconv.Atoi(widthStr)
		h, _ := strconv.Atoi(parts[4])
		top, _ := strconv.Atoi(parts[5])
		left, _ := strconv.Atoi(parts[6])
		winID := parts[7]
		target := paneTargetFromStartCmd(startCmd)
		headers = append(headers, headerInfo{
			paneID: parts[0], windowID: winID,
			target: target, width: w, height: h, top: top, left: left,
		})
	}

	keepHeader := make(map[string]bool)
	byTarget := make(map[string][]string)
	for _, hdr := range headers {
		if hdr.target == "" {
			continue
		}
		byTarget[hdr.target] = append(byTarget[hdr.target], hdr.paneID)
	}
	for _, paneIDs := range byTarget {
		sort.Strings(paneIDs)
		if len(paneIDs) > 0 {
			keepHeader[paneIDs[0]] = true
		}
	}

	// Process headers: kill disabled, orphaned, or mismatched dimensions
	killed := false
	for _, hdr := range headers {
		// Kill if headers disabled globally
		if !headersEnabled {
			logEvent("CLEANUP_HEADER pane=%s reason=disabled", hdr.paneID)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		if hdr.target != "" && !keepHeader[hdr.paneID] {
			logEvent("CLEANUP_HEADER pane=%s target=%s reason=duplicate_target", hdr.paneID, hdr.target)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		// Force header height to expected size if it's grown beyond it
		expectedHeight := coordinator.desiredPaneHeaderHeight()
		if hdr.height > expectedHeight {
			debugLog.Printf("Header %s height=%d, forcing to %d", hdr.paneID, hdr.height, expectedHeight)
			logEvent("HEADER_HEIGHT_ADJUST trigger=cleanup pane=%s height=%d expected=%d", hdr.paneID, hdr.height, expectedHeight)
			exec.Command("tmux", "resize-pane", "-t", hdr.paneID, "-y", fmt.Sprintf("%d", expectedHeight)).Run()
		}

		// Kill if target pane no longer exists
		if hdr.target == "" {
			continue
		}
		if !contentPaneExists[hdr.target] {
			logEvent("CLEANUP_HEADER pane=%s target=%s reason=target_gone", hdr.paneID, hdr.target)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		// Kill if header geometry doesn't match its target pane.
		// Valid header must match target width/left and sit above target top.
		if targetDims, ok := contentPaneDimensions[hdr.target]; ok {
			if hdr.width != targetDims.width {
				logEvent("CLEANUP_HEADER pane=%s target=%s reason=width_mismatch header_w=%d target_w=%d", hdr.paneID, hdr.target, hdr.width, targetDims.width)
				markSkipPreserveForWindow(hdr.paneID)
				exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
				killed = true
				continue
			}
			if hdr.left != targetDims.left {
				logEvent("CLEANUP_HEADER pane=%s target=%s reason=left_mismatch header_left=%d target_left=%d", hdr.paneID, hdr.target, hdr.left, targetDims.left)
				markSkipPreserveForWindow(hdr.paneID)
				exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
				killed = true
				continue
			}
			if hdr.top >= targetDims.top {
				logEvent("CLEANUP_HEADER pane=%s target=%s reason=not_above_target header_top=%d target_top=%d", hdr.paneID, hdr.target, hdr.top, targetDims.top)
				markSkipPreserveForWindow(hdr.paneID)
				exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
				killed = true
				continue
			}
		}
	}

	// Restore sidebar widths if we killed any headers (layout may have shifted)
	if killed {
		logEvent("WIDTH_SYNC_REQUEST trigger=cleanup_orphan_headers active=%s force=1", activeWindowID)
		coordinator.RunWidthSync(activeWindowID, true)
	}
}

// isWatchdogEnabled checks if the watchdog is enabled via tmux option
func isWatchdogEnabled() bool {
	out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_watchdog").Output()
	if err != nil {
		return true // default: enabled
	}
	val := strings.TrimSpace(string(out))
	return val != "off" && val != "0" && val != "false"
}

// watchdogCheckRenderers verifies sidebar renderer processes are alive and respawns dead ones
func watchdogCheckRenderers(server *daemon.Server, sessionID string) {
	if !isWatchdogEnabled() {
		return
	}

	rendererBin := getRendererBin()
	if rendererBin == "" {
		return
	}

	// Get all panes with PID info, scoped to our session
	watchdogArgs := []string{"list-panes", "-s"}
	if sessionID != "" {
		watchdogArgs = append(watchdogArgs, "-t", sessionID)
	}
	watchdogArgs = append(watchdogArgs, "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_pid}\x1f#{window_id}\x1f#{pane_dead}")
	out, err := exec.Command("tmux", watchdogArgs...).Output()
	if err != nil {
		return
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		paneID := parts[0]
		cmd := parts[1]
		pidStr := parts[2]
		windowID := parts[3]
		paneDead := parts[4]

		// Only check sidebar/renderer panes
		isSidebar := strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer")
		isHeader := strings.Contains(cmd, "pane-header")
		if !isSidebar && !isHeader {
			continue
		}

		// Check if tmux considers the pane dead
		if paneDead == "1" {
			logEvent("DEAD_PANE pane=%s cmd=%s window=%s -- killing dead pane", paneID, cmd, windowID)
			markSkipPreserveForWindow(paneID)
			exec.Command("tmux", "kill-pane", "-t", paneID).Run()

			// Respawn sidebar renderer if it was a sidebar
			if isSidebar {
				logEvent("RESPAWN_SIDEBAR window=%s after dead pane cleanup", windowID)
				debugFlag := ""
				if *debugMode {
					debugFlag = "-debug"
				}
				cmdStr := fmt.Sprintf("exec '%s' -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
				exec.Command("tmux", "split-window", "-d", "-t", windowID, "-h", "-b", "-l", "25", cmdStr).Run()
			}
			continue
		}

		// Check if process is actually alive (signal 0 test)
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process is dead but tmux hasn't noticed yet
			logEvent("ZOMBIE_PANE pane=%s pid=%d cmd=%s window=%s -- process dead, killing pane",
				paneID, pid, cmd, windowID)
			markSkipPreserveForWindow(paneID)
			exec.Command("tmux", "kill-pane", "-t", paneID).Run()

			if isSidebar {
				logEvent("RESPAWN_SIDEBAR window=%s after zombie pane cleanup", windowID)
				debugFlag := ""
				if *debugMode {
					debugFlag = "-debug"
				}
				cmdStr := fmt.Sprintf("exec '%s' -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
				exec.Command("tmux", "split-window", "-d", "-t", windowID, "-h", "-b", "-l", "25", cmdStr).Run()
			}
		}
	}
}

// restoreSidebarWidths is deprecated. Use coordinator.RunWidthSync(activeWindowID, true) instead.
// Kept as empty stub for backwards compatibility.
func restoreSidebarWidths() {
}

// updateHeaderBorderStyles sets pane-border-style on each header pane.
// When custom_border is enabled, borders are made invisible (fg=bg=default) since we
// render our own border line with drag handle in the header pane itself.
// When custom_border is disabled, borders use the header's text color for visibility.
// Also sets sidebar pane backgrounds using set-option (not select-pane -P which steals focus).
func updateHeaderBorderStyles(coordinator *Coordinator) {
	// Get all panes with start command info
	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
	if err != nil {
		return
	}

	// Removed: select-pane -P calls were stealing focus
	// Sidebar and pane-header content is rendered with proper ANSI colors via lipgloss
	// Terminal panes use native colors from the terminal emulator
	_ = coordinator // suppress unused warning
	_ = out
}

// saveFocusState saves the current window and pane to tmux options
// so they can be restored after daemon startup completes
func saveFocusState(sessionID string) {
	// Get current window and pane
	windowOut, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	currentWindow := strings.TrimSpace(string(windowOut))

	paneOut, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return
	}
	currentPane := strings.TrimSpace(string(paneOut))

	// Save to tmux options
	exec.Command("tmux", "set-option", "-g", "@tabby_last_window", currentWindow).Run()
	exec.Command("tmux", "set-option", "-g", "@tabby_last_pane", currentPane).Run()

	logEvent("SAVE_FOCUS window=%s pane=%s", currentWindow, currentPane)
}

// restoreFocusState restores the previously saved window and pane focus
func restoreFocusState() {
	newWindowOut, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_new_window_id").Output()
	if strings.TrimSpace(string(newWindowOut)) != "" {
		return
	}

	// Get saved window and pane
	windowOut, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_last_window").Output()
	if err != nil {
		return
	}
	savedWindow := strings.TrimSpace(string(windowOut))

	paneOut, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_last_pane").Output()
	if err != nil {
		return
	}
	savedPane := strings.TrimSpace(string(paneOut))

	logEvent("RESTORE_FOCUS window=%s pane=%s", savedWindow, savedPane)

	// Restore window focus
	if savedWindow != "" {
		exec.Command("tmux", "select-window", "-t", savedWindow).Run()
	}

	// Restore pane focus
	if savedPane != "" {
		exec.Command("tmux", "select-pane", "-t", savedPane).Run()
	}
}

func shouldRestoreFocus() bool {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}
	cmd := strings.ToLower(strings.TrimSpace(string(out)))
	if cmd == "" {
		return false
	}
	return strings.Contains(cmd, "sidebar") ||
		strings.Contains(cmd, "renderer") ||
		strings.Contains(cmd, "pane-header")
}

// syncClientSizesFromTmux updates all client widths/heights from actual tmux pane sizes.
// Returns true when any tracked sidebar size changed.
func syncClientSizesFromTmux(server *daemon.Server, coordinator *Coordinator, trigger string) bool {
	start := time.Now()

	// Get all panes with their sizes and commands
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{window_id}\x1f#{pane_id}\x1f#{pane_width}\x1f#{pane_height}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
	if err != nil {
		logEvent("GEOM_SYNC_ERROR trigger=%s err=%v", trigger, err)
		return false
	}

	// Build a map of window ID -> sidebar pane dimensions
	type paneSize struct {
		width  int
		height int
		paneID string
	}

	sidebarSizes := make(map[string]paneSize)
	headerSizes := make(map[string]paneSize)

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 6)
		if len(parts) < 6 {
			continue
		}
		windowID := parts[0]
		paneID := parts[1]
		width, _ := strconv.Atoi(parts[2])
		height, _ := strconv.Atoi(parts[3])
		cmd := parts[4]
		startCmd := parts[5]

		// Check if this is a sidebar/renderer pane
		if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
			sidebarSizes[windowID] = paneSize{width: width, height: height, paneID: paneID}
			continue
		}

		if strings.Contains(cmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
			targetPane := paneTargetFromStartCmd(startCmd)
			if targetPane == "" {
				targetPane = paneID
			}
			headerSizes["header:"+targetPane] = paneSize{width: width, height: height, paneID: paneID}
		}
	}

	sizesChanged := false
	changeCount := 0

	// Update server client sizes and coordinator width snapshots.
	for windowID, size := range sidebarSizes {
		// Skip clearly invalid widths (e.g. pane collapsed to 0 or 1)
		if size.width < 5 {
			logEvent("GEOM_SYNC_SKIP trigger=%s client=%s width=%d height=%d reason=too_small", trigger, windowID, size.width, size.height)
			continue
		}
		if info := server.GetClientInfo(windowID); info != nil {
			if info.Width != size.width || info.Height != size.height {
				sizesChanged = true
				changeCount++
				logEvent("GEOM_SYNC_SIDEBAR trigger=%s client=%s prev=%dx%d new=%dx%d delta_w=%d delta_h=%d",
					trigger, windowID, info.Width, info.Height, size.width, size.height, size.width-info.Width, size.height-info.Height)
			}
		} else {
			sizesChanged = true
			changeCount++
			logEvent("GEOM_SYNC_SIDEBAR trigger=%s client=%s prev=none new=%dx%d", trigger, windowID, size.width, size.height)
		}
		server.UpdateClientSize(windowID, size.width, size.height)
		if coordinator != nil {
			coordinator.UpdateClientSizeSnapshot(windowID, size.width, size.height)
		}
	}

	for clientID, size := range headerSizes {
		if info := server.GetClientInfo(clientID); info != nil {
			if info.Width != size.width || info.Height != size.height {
				sizesChanged = true
				changeCount++
				logEvent("HEADER_SIZE_SYNC trigger=%s client=%s prev=%dx%d new=%dx%d",
					trigger, clientID, info.Width, info.Height, size.width, size.height)
			}
		} else {
			if size.width > 0 || size.height > 0 {
				changeCount++
				sizesChanged = true
				logEvent("HEADER_SIZE_SYNC trigger=%s client=%s prev=none new=%dx%d", trigger, clientID, size.width, size.height)
			}
		}
		desiredH := coordinator.desiredPaneHeaderHeight()
		if size.height > desiredH {
			logEvent("HEADER_HEIGHT_ANOMALY trigger=%s client=%s height=%d desired=%d", trigger, clientID, size.height, desiredH)
			exec.Command("tmux", "resize-pane", "-t", size.paneID, "-y", fmt.Sprintf("%d", desiredH)).Run()
		}
		server.UpdateClientSize(clientID, size.width, size.height)
	}

	if changeCount > 0 {
		logEvent("GEOM_SYNC_APPLY trigger=%s changed=%d duration_ms=%d", trigger, changeCount, time.Since(start).Milliseconds())
	}

	return sizesChanged
}

func activeClientGeometry() (width int, height int, tty string, activity int64, ok bool) {
	const idleWindow = int64(1500)
	now := time.Now().Unix()
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}\x1f#{client_width}\x1f#{client_height}\x1f#{client_flags}\x1f#{client_activity}").Output()
	if err != nil {
		return 0, 0, "", 0, false
	}

	type clientInfo struct {
		tty      string
		width    int
		height   int
		focused  bool
		activity int64
	}

	var attachedClients []clientInfo
	focusedCount := 0

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		w, errW := strconv.Atoi(parts[1])
		h, errH := strconv.Atoi(parts[2])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue
		}
		flags := parts[3]
		activity, _ := strconv.ParseInt(parts[4], 10, 64)
		info := clientInfo{
			tty:      parts[0],
			width:    w,
			height:   h,
			focused:  strings.Contains(flags, "focused"),
			activity: activity,
		}
		if strings.Contains(flags, "attached") {
			attachedClients = append(attachedClients, info)
			if info.focused {
				focusedCount++
			}
		}
	}

	if len(attachedClients) == 0 {
		return 0, 0, "", 0, false
	}

	bestIdx := 0
	for i := 1; i < len(attachedClients); i++ {
		c := attachedClients[i]
		deltaBest := now - attachedClients[bestIdx].activity
		deltaCur := now - c.activity
		bestActive := deltaBest <= idleWindow
		curActive := deltaCur <= idleWindow
		if bestActive != curActive {
			if curActive {
				bestIdx = i
			}
			continue
		}
		// Both focused — use most recent activity (standard behavior).
		// The phone only wins when it genuinely has the latest user input.
		if bestActive && curActive {
			if c.activity > attachedClients[bestIdx].activity {
				bestIdx = i
			}
			continue
		}
		if c.activity > attachedClients[bestIdx].activity {
			bestIdx = i
			continue
		}
		if c.activity == attachedClients[bestIdx].activity {
			if c.focused && !attachedClients[bestIdx].focused {
				bestIdx = i
			}
		}
	}

	best := attachedClients[bestIdx]
	reason := "activity"
	if now-best.activity > idleWindow {
		reason = "stale_activity"
	}
	if best.focused {
		reason = reason + "+focused"
	}
	logEvent("CLIENT_GEOM_SELECT tty=%s size=%dx%d reason=%s activity=%d attached=%d focused=%d", best.tty, best.width, best.height, reason, best.activity, len(attachedClients), focusedCount)
	return best.width, best.height, best.tty, best.activity, true
}


func resizeAllWindowsToClient(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		return
	}
	for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		wid = strings.TrimSpace(wid)
		if wid == "" {
			continue
		}
		exec.Command("tmux", "resize-window", "-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height), "-t", wid).Run()
	}
}

// resetTerminalModes sends escape sequences to disable mouse tracking modes
// This is called on daemon startup to clean up stale state from crashed renderers
func resetTerminalModes(sessionID string) {
	// Use tmux's refresh-client to reset terminal state
	// The -S flag forces a full refresh which can help reset stuck modes
	// We must refresh ALL clients to handle multi-client scenarios correctly
	if out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output(); err == nil {
		for _, tty := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if tty == "" {
				continue
			}
			exec.Command("tmux", "refresh-client", "-t", tty, "-S").Run()
		}
	}

	// Also try to reset mouse mode by toggling tmux's mouse option
	// This forces tmux to re-sync mouse state with the terminal
	exec.Command("tmux", "set", "-g", "mouse", "off").Run()
	exec.Command("tmux", "set", "-g", "mouse", "on").Run()
}

func main() {
	flag.Parse()

	// Initialize BubbleZone for touch button click detection
	zone.NewGlobal()

	// Get session ID from environment if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	daemonStartTime = time.Now()

	// Rotate logs before opening them to prevent unbounded growth
	rotateLogs(*sessionID)

	// Initialize logging early
	initCrashLog(*sessionID)
	initEventLog(*sessionID)
	initInputLog(*sessionID)
	defer recoverAndLog("main")

	// Check if previous daemon died abnormally (before we claim the PID file)
	checkPreviousCrash(*sessionID)

	// Scope tmux queries to this session
	tmux.SetSessionTarget(*sessionID)

	if *debugMode {
		debugLog = log.New(os.Stderr, "[daemon] ", log.LstdFlags|log.Lmicroseconds)
		SetCoordinatorDebugLog(debugLog)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	debugLog.Printf("Starting daemon for session %s", *sessionID)
	crashLog.Printf("Daemon started for session %s pid=%d", *sessionID, os.Getpid())

	// Save current window and pane for focus restoration after setup
	saveFocusState(*sessionID)

	// Create coordinator for centralized rendering
	coordinator := NewCoordinator(*sessionID)
	globalCoordinator = coordinator

	// Enable coordinator debug logging if debug mode is on
	if *debugMode {
		SetCoordinatorDebugLog(debugLog)
	}

	// Create server
	server := daemon.NewServer(*sessionID)

	// Set up debug logging for render diagnostics
	server.DebugLog = func(format string, args ...interface{}) {
		logEvent(format, args...)
	}

	// Set up render callback using coordinator (with panic recovery)
	server.OnRenderNeeded = func(clientID string, width, height int) (result *daemon.RenderPayload) {
		defer func() {
			if r := recover(); r != nil {
				debugLog.Printf("PANIC in OnRenderNeeded (client=%s): %v\n%s", clientID, r, debug.Stack())
				logEvent("PANIC_RENDER client=%s err=%v", clientID, r)
				crashLog.Printf("PANIC_RENDER client=%s err=%v\n%s", clientID, r, debug.Stack())
				result = nil
			}
		}()
		// Route header clients to pane header renderer
		if strings.HasPrefix(clientID, "header:") {
			return coordinator.RenderHeaderForClient(clientID, width, height)
		}
		renderClientID := clientID
		if idx := strings.Index(clientID, "#web-"); idx > 0 {
			renderClientID = clientID[:idx]
		}
		return coordinator.RenderForClient(renderClientID, width, height)
	}

	refreshCh := make(chan struct{}, 10)

	// Ignore SIGPIPE — daemon runs backgrounded and stdout/stderr may become broken pipes
	signal.Ignore(syscall.SIGPIPE)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	var selfTerminating atomic.Bool

	// Set up input callback with panic recovery
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		logEvent("INPUT_START client=%s type=%s btn=%s action=%s resolved=%s x=%d y=%d", clientID, input.Type, input.Button, input.Action, input.ResolvedAction, input.MouseX, input.MouseY)
		defer func() {
			if r := recover(); r != nil {
				debugLog.Printf("PANIC in OnInput handler (client=%s): %v", clientID, r)
				logEvent("PANIC_INPUT client=%s err=%v", clientID, r)
			}
		}()
		needsRefresh := coordinator.HandleInput(clientID, input)
		logEvent("INPUT_HANDLED client=%s needsRefresh=%v", clientID, needsRefresh)
		if needsRefresh {
			// Immediate optimistic render: HandleInput already updated the
			// coordinator state (e.g. SetActiveWindowOptimistic for select_window)
			// so rendering NOW gives the requesting client the correct header
			// color without waiting for the full BroadcastRender round-trip.
			server.SendRenderToClient(clientID)
			// Broadcast to remaining clients asynchronously so the input
			// goroutine is not blocked by O(n) renders before returning.
			go server.BroadcastRender()
			// Signal the main refresh loop for full state sync
			// (spawn/cleanup renderers, update pane colors, etc.)
			select {
			case refreshCh <- struct{}{}:
			default:
				// Channel full, refresh already pending
			}
			logEvent("INPUT_SIGNALED_REFRESH client=%s", clientID)
		} else {
			// Internal-only state change (e.g. toggle_group) - render the
			// requesting client immediately for snappy response, then broadcast
			// to remaining clients asynchronously.
			server.SendRenderToClient(clientID)
			go server.BroadcastRender()
		}
		logEvent("INPUT_DONE client=%s", clientID)
	}

	// Set up connect/disconnect callbacks
	server.OnConnect = func(clientID string, paneID string) {
		logEvent("CLIENT_CONNECT client=%s pane=%s", clientID, paneID)
		if paneID != "" {
			coordinator.ApplyThemeToPane(paneID)
		}
	}
	// OnResize: update the coordinator's size snapshot for rendering, but do
	// NOT trigger width reconciliation. Width is determined deterministically
	// by (globalWidth, activeClientWidth) and only changes via explicit
	// triggers: user drag (after-resize-pane hook), active-client switch
	// (clientGeometryTicker), new window spawn, or config change. Reacting to
	// the renderer's own size reports here caused the tmux-reflow feedback
	// loop on app launches.
	server.OnResize = func(clientID string, width, height int, paneID string) {
		coordinator.UpdateClientSizeSnapshot(clientID, width, height)
		if paneID != "" {
			coordinator.ApplyThemeToPane(paneID)
		}
	}
	server.OnDisconnect = func(clientID string) {
		coordinator.RemoveClient(clientID)
		debugLog.Printf("Client disconnected: %s", clientID)
		logEvent("CLIENT_DISCONNECT client=%s", clientID)
	}

	// Set up menu send callback for in-renderer context menus
	coordinator.OnSendMenu = func(clientID string, menu *daemon.MenuPayload) {
		server.SendMenuToClient(clientID, menu)
	}
	coordinator.OnSendMarkerPicker = func(clientID string, picker *daemon.MarkerPickerPayload) {
		server.SendMarkerPickerToClient(clientID, picker)
	}
	coordinator.OnSendColorPicker = func(clientID string, picker *daemon.ColorPickerPayload) {
		server.SendColorPickerToClient(clientID, picker)
	}
	coordinator.OnSyncSidebarClientWidths = func(newWidth int) {
		for _, id := range server.GetAllClientIDs() {
			if !strings.HasPrefix(id, "header:") {
				server.UpdateClientWidth(id, newWidth)
			}
		}
	}

	// Register SIGUSR1 handler BEFORE server.Start() creates the socket.
	// ensure_sidebar.sh sends USR1 the moment it detects the socket, so the
	// handler must be in place before the socket appears or the signal arrives
	// before Notify and kills the process (Go uses the OS default — terminate).
	refreshSigCh := make(chan os.Signal, 10)
	signal.Notify(refreshSigCh, syscall.SIGUSR1)
	go func() {
		for range refreshSigCh {
			logEvent("SIGNAL_USR1 session=%s", *sessionID)

			select {
			case refreshCh <- struct{}{}:
			default:
				select {
				case <-refreshCh:
				default:
				}
				select {
				case refreshCh <- struct{}{}:
				default:
				}
				logEvent("SIGNAL_USR1 queue=full action=coalesced")
			}
		}
	}()

	// SIGUSR2 = client-resized: immediately force a full width sync across all windows.
	// Using a separate signal bypasses the 500ms debounce on the normal USR1 path.
	clientResizedCh := make(chan os.Signal, 5)
	signal.Notify(clientResizedCh, syscall.SIGUSR2)
	go func() {
		for range clientResizedCh {
			logEvent("SIGNAL_USR2_CLIENT_RESIZED session=%s", *sessionID)
			go func() {
				syncClientSizesFromTmux(server, coordinator, "sigusr2")
				activeWin := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
				logEvent("WIDTH_SYNC_REQUEST trigger=sigusr2 active=%s force=1", activeWin)
				coordinator.RunWidthSync(activeWin, true /* force */)
				server.BroadcastRender()
				logEvent("SIGNAL_USR2_DONE session=%s active=%s", *sessionID, activeWin)
			}()
		}
	}()

	// Start server (creates the socket — SIGUSR1 is already registered above,
	// so any signal from ensure_sidebar.sh that arrives immediately is safe).
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	debugLog.Printf("Server listening on %s", server.GetSocketPath())
	logEvent("DAEMON_START session=%s pid=%d", *sessionID, os.Getpid())
	resetTerminalModes(*sessionID)

	// Start heartbeat writer (detects hangs on next startup)
	heartbeatDone := make(chan struct{})
	go writeHeartbeatLoop(*sessionID, heartbeatDone)

	// Idle-client reaper: opt-in via @tabby_client_idle_timeout_hours (>0).
	// Detaches only clients idle longer than the threshold, never the currently
	// active client, and never runs at startup (which was detaching legitimate
	// passive viewers the moment the daemon booted).
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		reap := func() {
			hours := 0
			if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_client_idle_timeout_hours").Output(); err == nil {
				if v, perr := strconv.Atoi(strings.TrimSpace(string(out))); perr == nil && v > 0 {
					hours = v
				}
			}
			if hours <= 0 {
				return // opt-in only
			}
			threshold := time.Duration(hours) * time.Hour
			activeTTY := strings.TrimSpace(tmuxOutputTrimmed("display-message", "-p", "#{client_tty}"))
			out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}|#{client_activity}").Output()
			if err != nil {
				return
			}
			now := time.Now()
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(line, "|", 2)
				if len(parts) != 2 || parts[0] == "" {
					continue
				}
				if parts[0] == activeTTY {
					continue
				}
				actSec, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					continue
				}
				idle := now.Sub(time.Unix(actSec, 0))
				if idle > threshold {
					logEvent("CLIENT_IDLE_DETACH tty=%s idle=%s threshold=%s", parts[0], idle.Round(time.Second), threshold)
					exec.Command("tmux", "detach-client", "-t", parts[0]).Run()
				}
			}
		}
		for range ticker.C {
			reap()
		}
	}()

	// Start deadlock watchdog
	StartDeadlockWatchdog()

	// Restore focus after daemon initialization completes
	// Wait for renderers to spawn and settle before restoring focus
	go func() {
		time.Sleep(300 * time.Millisecond)
		if shouldRestoreFocus() {
			restoreFocusState()
		}
	}()

	// Start coordinator refresh loops with change detection
	go func() {
		defer recoverAndLog("refresh-loop")

		// consecutiveStalls tracks how many times each task has stalled in a row.
		// A single stall is forgiven (tmux can hang momentarily under load).
		// After maxConsecutiveStalls, the daemon self-terminates.
		consecutiveStalls := map[string]int{}
		const maxConsecutiveStalls = 3

		runLoopTask := func(task string, timeout time.Duration, fn func()) bool {
			done := make(chan struct{})
			go func() {
				defer close(done)
				fn()
			}()

			select {
			case <-done:
				// Success: reset stall counter for this task
				if consecutiveStalls[task] > 0 {
					logEvent("LOOP_RECOVERED task=%s after_stalls=%d", task, consecutiveStalls[task])
				}
				consecutiveStalls[task] = 0
				return true
			case <-time.After(timeout):
				uptime := time.Since(daemonStartTime).Truncate(time.Second)
				consecutiveStalls[task]++
				stalls := consecutiveStalls[task]
				logEvent("LOOP_STALL task=%s timeout_ms=%d uptime=%s clients=%d consecutive=%d/%d",
					task, timeout.Milliseconds(), uptime, server.ClientCount(), stalls, maxConsecutiveStalls)
				if crashLog != nil {
					crashLog.Printf("LOOP_STALL task=%s timeout=%v uptime=%s clients=%d consecutive=%d/%d",
						task, timeout, uptime, server.ClientCount(), stalls, maxConsecutiveStalls)
					buf := make([]byte, 64*1024)
					n := runtime.Stack(buf, true)
					crashLog.Printf("LOOP_STALL all goroutines:\n%s", buf[:n])
				}
				if stalls >= maxConsecutiveStalls {
					logEvent("LOOP_FATAL task=%s reason=max_consecutive_stalls stalls=%d", task, stalls)
					if crashLog != nil {
						crashLog.Printf("LOOP_FATAL: %d consecutive stalls for %s, self-terminating", stalls, task)
					}
					selfTerminating.Store(true)
					select {
					case sigCh <- syscall.SIGTERM:
					default:
					}
					return false
				}
				// Non-fatal: skip this iteration, retry on next tick
				logEvent("LOOP_SKIP_DEGRADED task=%s stalls=%d (will retry next tick)", task, stalls)
				return true
			}
		}

		// runLoopTaskNonFatal runs a task with a timeout but only logs on stall (no SIGTERM).
		// Use for cosmetic tasks like animation where a skipped frame is acceptable.
		runLoopTaskNonFatal := func(task string, timeout time.Duration, fn func()) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				fn()
			}()

			select {
			case <-done:
			case <-time.After(timeout):
				uptime := time.Since(daemonStartTime).Truncate(time.Second)
				logEvent("LOOP_SKIP task=%s timeout_ms=%d uptime=%s clients=%d", task, timeout.Milliseconds(), uptime, server.ClientCount())
				if crashLog != nil {
					crashLog.Printf("LOOP_SKIP task=%s timeout=%v (non-fatal, skipping frame)", task, timeout)
				}
			}
		}

		refreshTicker := time.NewTicker(30 * time.Second)              // Window list poll (fallback, signal_refresh handles real-time)
		windowCheckTicker := time.NewTicker(3 * time.Second)           // Spawn/cleanup poll (fallback, reduced from 10s for faster new-window response)
		clientGeometryTicker := time.NewTicker(250 * time.Millisecond) // Fallback for client switches that do not emit hooks
		animationTicker := time.NewTicker(100 * time.Millisecond)      // Combined spinner + pet animation (was two separate tickers)
		gitTicker := time.NewTicker(5 * time.Second)                   // Git status
		watchdogTicker := time.NewTicker(5 * time.Second)              // Watchdog: check renderer health
		defer refreshTicker.Stop()
		defer windowCheckTicker.Stop()
		defer clientGeometryTicker.Stop()
		defer animationTicker.Stop()
		defer gitTicker.Stop()
		defer watchdogTicker.Stop()

		lastWindowsHash := ""
		lastGitState := ""
		lastClientGeometry := ""
		activeWindowID := "" // Track active window for optimized rendering

		lastWindowCount := 0 // Track window count for close detection

		// Helper to get current active window ID (cached, updated on events)
		updateActiveWindow := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
				newID := strings.TrimSpace(string(out))
				if newID != activeWindowID && newID != "" {
					// Window changed — track in history and handle select
					coordinator.TrackWindowHistory(newID)
					coordinator.HandleWindowSelect(newID)
				}
				activeWindowID = newID
			}
		}
		updateActiveWindow() // Initial fetch
		lastWindowCount = len(coordinator.GetWindows())

		// Initial call to set sidebar pane backgrounds (before any events)
		go func() {
			time.Sleep(500 * time.Millisecond) // Wait for renderers to spawn
			updateHeaderBorderStyles(coordinator)
		}()

		// Debounce pane layout operations (spawn/cleanup headers) to prevent
		// feedback loops: these ops trigger pane-focus-in hooks which send USR1
		// back to us, causing re-entry. 50ms cooldown is sufficient to break cycle
		// while keeping UI responsive.
		var lastPaneLayoutOps time.Time
		paneLayoutCooldown := 150 * time.Millisecond

		// Restore saved layouts once at startup, before the main event loop.
		// Must not run inside doPaneLayoutOps: spawnPaneHeaders sets @tabby_spawning=1
		// which blocks layout updates to @tabby_layout_<windowID>,
		// so any stale layout written after the split would persist and be applied
		// by tabby-hook preserve-pane-ratios, corrupting the live pane geometry.
		restoreLayoutsFromDisk()

		// Eager initial spawn: don't wait for the 3s windowCheckTicker.
		// The coordinator already has windows from NewCoordinator, so spawn
		// renderers immediately to cut cold-boot sidebar latency.
		{
			windows := coordinator.GetWindows()
			if spawnRenderersForNewWindows(server, *sessionID, windows, coordinator) {
				logEvent("INITIAL_SPAWN_COMPLETE")
				server.BroadcastRender()
			}
		}

		doPaneLayoutOps := func() {
			now := time.Now()
			if now.Sub(lastPaneLayoutOps) < paneLayoutCooldown {
				logEvent("PANE_LAYOUT_SKIP cooldown_remaining=%dms", (paneLayoutCooldown - now.Sub(lastPaneLayoutOps)).Milliseconds())
				return
			}
			lastPaneLayoutOps = now
			logEvent("PANE_LAYOUT_START")
			customBorder := coordinator.GetConfig().PaneHeader.CustomBorder
			exec.Command("tmux", "set-option", "-g", "@tabby_spawning", "1").Run()
			spawnPaneHeaders(server, *sessionID, customBorder, coordinator.desiredPaneHeaderHeight(), coordinator.GetWindows())
			exec.Command("tmux", "set-option", "-g", "@tabby_spawning", "0").Run()
			cleanupOrphanedHeaders(customBorder, coordinator, activeWindowID)
			// NOTE: updateHeaderBorderStyles is NOT called here to avoid
			// border flickering. It's only called when windows hash changes
			// (on refreshCh + hash change) which is when groups/colors change.
			// Drain any USR1 signals our tmux commands just triggered
			// (split-window/kill-pane/resize-pane fire pane-focus-in hooks)
			for {
				select {
				case <-refreshCh:
				default:
					return
				}
			}
		}

		// Debounce signal_refresh: if we just finished a full refresh cycle,
		// skip the heavy spawn/cleanup work and only do the fast path
		// (RefreshWindows + BroadcastRender). This prevents feedback loops
		// where spawn/cleanup tmux ops trigger hooks that send more USR1 signals.
		var lastFullRefresh time.Time
		fullRefreshCooldown := 100 * time.Millisecond

		for {
			// Record heartbeat at each loop iteration for deadlock detection
			recordHeartbeat()

			select {
			case <-refreshCh:
				ok := runLoopTask("signal_refresh", 20*time.Second, func() {
					start := time.Now()
					logEvent("SIGNAL_REFRESH session=%s", *sessionID)

					updateActiveWindow()
					// Sync client sizes first so width sync sees real tmux dimensions
					// for both active and inactive windows after a client resize.
					sizesChanged := syncClientSizesFromTmux(server, coordinator, "signal_refresh")

					// Optimistic render: flip the active window flag and render
					// immediately so the sidebar highlights the correct window
					// before the slow RefreshWindows round-trip completes.
					// This cuts perceived lag from ~500ms to ~50ms on Cmd+[/].
					coordinator.SetActiveWindowOptimistic(activeWindowID)
					server.SendRenderToClient(activeWindowID)

					coordinator.RefreshWindows()
					t1 := time.Now()

					// Detect window close: if window count decreased, select
					// the most recently visited surviving window from history.
					currentWindowCount := len(coordinator.GetWindows())
					if currentWindowCount < lastWindowCount && lastWindowCount > 0 {
						coordinator.SelectPreviousWindow()
						updateActiveWindow() // Re-fetch after selecting
					}
					lastWindowCount = currentWindowCount

					// Save window layouts inline (replaces save_pane_layout.sh hook)
					coordinator.SaveWindowLayouts()

					// Full render after RefreshWindows to pick up any state changes
					// (pane titles, AI indicators, git status, etc.).
					server.BroadcastRender()
					t1b := time.Now()
					// Force a coordinated width sync when tmux reported actual pane
					// size changes; this prevents inactive windows from catching up
					// one by one as the user later visits them.
					logEvent("WIDTH_SYNC_REQUEST trigger=signal_refresh active=%s force=%v", activeWindowID, sizesChanged)
					coordinator.RunWidthSync(activeWindowID, sizesChanged)
					coordinator.RunHeaderHeightSync(activeWindowID)
					coordinator.RunZoomSync(activeWindowID)

					// Apply pane dimming inline (replaces cycle-pane --dim-only shell call)
					coordinator.ApplyPaneDimming(activeWindowID)

					// Enforce status bar exclusivity (replaces enforce_status_exclusivity.sh)
					coordinator.EnforceStatusExclusivity(*sessionID)

					// Heavy ops (spawn/cleanup/layout) only if enough time has
					// passed since the last full refresh. This breaks the feedback
					// loop: doPaneLayoutOps triggers tmux hooks → USR1 → signal_refresh
					// → doPaneLayoutOps again. With debounce, rapid signals only do
					// the fast path above (RefreshWindows + BroadcastRender).
					if time.Since(lastFullRefresh) >= fullRefreshCooldown {
						windows := coordinator.GetWindows()
						spawnedRenderer := spawnRenderersForNewWindows(server, *sessionID, windows, coordinator)
						t2 := time.Now()

						cleanupOrphanedSidebars(windows)
						cleanupOrphanWindowsByTmux(*sessionID)
						t3 := time.Now()

						cleanupSidebarsForClosedWindows(server, windows)
						t4 := time.Now()

						doPaneLayoutOps()
						t5 := time.Now()

						_ = spawnedRenderer

						// Apply new window group + preserve grouped window names
						// (replaces apply_new_window_group.sh + preserve_window_name.sh)
						coordinator.ApplyNewWindowGroup()
						coordinator.PreserveWindowNames()

						currentHash := coordinator.GetWindowsHash()
						if currentHash != lastWindowsHash {
							updateHeaderBorderStyles(coordinator)
						}

						// Second broadcast only if structure changed (new/removed renderers)
						structureChanged := spawnedRenderer || currentHash != lastWindowsHash
						if structureChanged {
							syncClientSizesFromTmux(server, coordinator, "structure_refresh")
							server.BroadcastRender()
						}
						lastWindowsHash = currentHash
						lastFullRefresh = time.Now()

						debugLog.Printf("PERF: RefreshWindows=%v EarlyRender=%v Spawn=%v Cleanup1=%v Cleanup2=%v Layout=%v TOTAL=%v",
							t1.Sub(start), t1b.Sub(t1), t2.Sub(t1b), t3.Sub(t2), t4.Sub(t3), t5.Sub(t4), t5.Sub(start))
					} else {
						debugLog.Printf("PERF: RefreshWindows=%v EarlyRender=%v (fast-path, heavy ops skipped)",
							t1.Sub(start), t1b.Sub(t1))
					}

					// Drain stale USR1 signals our tmux commands generated via hooks
					drainCount := 0
					for {
						select {
						case <-refreshCh:
							drainCount++
						default:
							goto drained
						}
					}
				drained:
					if drainCount > 0 {
						logEvent("DRAIN_STALE count=%d", drainCount)
					}
				})
				if !ok {
					return
				}
			case <-windowCheckTicker.C: // Fallback polling: spawn/cleanup for missed events
				// Window check is a polling task — stalls are non-fatal (skip and retry next tick)
				runLoopTaskNonFatal("window_check", 8*time.Second, func() {
					logEvent("WINDOW_CHECK_TICK")
					// Use cached window state — signal_refresh keeps it fresh via USR1.
					// Calling RefreshWindows() here added a redundant ListWindowsWithPanes()
					// tmux round-trip that caused lock contention and task stalls under load.
					windows := coordinator.GetWindows()

					spawnedFallback := spawnRenderersForNewWindows(server, *sessionID, windows, coordinator)
					cleanupOrphanedSidebars(windows)
					cleanupOrphanWindowsByTmux(*sessionID)
					cleanupSidebarsForClosedWindows(server, windows)
					doPaneLayoutOps()
					_ = spawnedFallback
					// Persist current layouts to disk for restart recovery
					saveLayoutsToDisk(windows)
					// Width sync as fallback for missed events
					logEvent("WIDTH_SYNC_REQUEST trigger=window_check active=%s force=0", activeWindowID)
					coordinator.RunWidthSync(activeWindowID, false)
				})
			case <-clientGeometryTicker.C:
				runLoopTaskNonFatal("client_geometry_tick", 2*time.Second, func() {
					w, h, tty, activity, ok := activeClientGeometry()
					if !ok {
						return
					}
					geomKey := fmt.Sprintf("%s:%dx%d:%d", tty, w, h, activity)
					if geomKey == lastClientGeometry {
						return
					}
					lastClientGeometry = geomKey
					logEvent("CLIENT_GEOMETRY_CHANGE tty=%s size=%dx%d activity=%d", tty, w, h, activity)
					coordinator.SetActiveClientWidth(w)
					syncClientSizesFromTmux(server, coordinator, "geometry_tick")
					activeWin := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
					logEvent("WIDTH_SYNC_REQUEST trigger=geometry_tick active=%s force=1", activeWin)
					coordinator.RunWidthSync(activeWin, true)
					coordinator.RunHeaderHeightSync(activeWin)
					coordinator.RunZoomSync(activeWin)
					server.BroadcastRender()
				})
			case <-watchdogTicker.C:
				ok := runLoopTask("watchdog", 6*time.Second, func() {
					logInput("HEALTH clients=%d", server.ClientCount())
					watchdogCheckRenderers(server, *sessionID)
				})
				if !ok {
					return
				}
			case <-refreshTicker.C:
				ok := runLoopTask("refresh_tick", 8*time.Second, func() {
					// Fallback polling: always refresh windows (needed for staleness
					// detection of stuck @tabby_busy), but only broadcast render and
					// update header styles if the hash actually changed.
					coordinator.RefreshWindows()
					currentHash := coordinator.GetWindowsHash()
					if currentHash != lastWindowsHash {
						updateHeaderBorderStyles(coordinator)
						server.BroadcastRender()
						lastWindowsHash = currentHash
					}
				})
				if !ok {
					return
				}
			case <-animationTicker.C:
				// Combined spinner + pet animation tick with timeout protection.
				// Animation is cosmetic — a stall just skips the frame (non-fatal).
				runLoopTaskNonFatal("animation_tick", 2*time.Second, func() {
					spinnerVisible := coordinator.IncrementSpinner()
					petChanged := coordinator.UpdatePetState()
					indicatorAnimated := coordinator.HasActiveIndicatorAnimation()
					if spinnerVisible || petChanged || indicatorAnimated {
						perf.Log("animationTick (render)")
						server.RenderActiveWindowOnly(activeWindowID)
					}
				})
			case <-gitTicker.C:
				ok := runLoopTask("git_tick", 6*time.Second, func() {
					// Only broadcast if git state changed
					currentGitState := coordinator.GetGitStateHash()
					if currentGitState != lastGitState {
						perf.Log("gitTick (changed)")
						coordinator.RefreshGit()
						coordinator.RefreshSession()
						server.BroadcastRender()
						lastGitState = currentGitState
					}
				})
				if !ok {
					return
				}
			}
		}
	}()

	// Monitor for idle shutdown (no clients for 30s), session existence, and socket/PID health
	go func() {
		defer recoverAndLog("idle-monitor")
		idleTicker := time.NewTicker(10 * time.Second)
		socketCheckTicker := time.NewTicker(3 * time.Second)
		defer idleTicker.Stop()
		defer socketCheckTicker.Stop()
		idleStart := time.Time{}
		myPid := os.Getpid()
		socketPath := server.GetSocketPath()

		for {
			select {
			case <-socketCheckTicker.C:
				// Check if our socket still exists
				if _, err := os.Stat(socketPath); os.IsNotExist(err) {
					logEvent("SHUTDOWN_REASON session=%s reason=socket_gone pid=%d", *sessionID, myPid)
					debugLog.Printf("Socket %s no longer exists, shutting down", socketPath)
					sigCh <- syscall.SIGTERM
					return
				}

				// Check if PID file still has our PID (another daemon may have taken over)
				pidPath := daemon.RuntimePath(*sessionID, ".pid")
				if data, err := os.ReadFile(pidPath); err == nil {
					pidStr := strings.TrimSpace(string(data))
					if pid, err := strconv.Atoi(pidStr); err == nil && pid != myPid {
						logEvent("SHUTDOWN_REASON session=%s reason=pid_replaced our=%d new=%d", *sessionID, myPid, pid)
						debugLog.Printf("PID file replaced (ours=%d, new=%d), shutting down", myPid, pid)
						sigCh <- syscall.SIGTERM
						return
					}
				}

			case <-idleTicker.C:
				// Check if session still exists
				if _, err := exec.Command("tmux", "has-session", "-t", *sessionID).Output(); err != nil {
					logEvent("SHUTDOWN_REASON session=%s reason=session_gone", *sessionID)
					debugLog.Printf("Session %s no longer exists, shutting down", *sessionID)
					sigCh <- syscall.SIGTERM
					return
				}

				// Check if any windows remain
				out, err := exec.Command("tmux", "list-windows", "-t", *sessionID, "-F", "#{window_id}").Output()
				if err != nil || strings.TrimSpace(string(out)) == "" {
					logEvent("SHUTDOWN_REASON session=%s reason=no_windows", *sessionID)
					debugLog.Printf("No windows remaining, shutting down")
					sigCh <- syscall.SIGTERM
					return
				}

				// Idle timeout if no clients
				if server.ClientCount() == 0 {
					if idleStart.IsZero() {
						idleStart = time.Now()
					} else if time.Since(idleStart) > 30*time.Second {
						logEvent("SHUTDOWN_REASON session=%s reason=idle_timeout clients=0", *sessionID)
						debugLog.Printf("No clients for 30s, shutting down")
						sigCh <- syscall.SIGTERM
						return
					}
				} else {
					idleStart = time.Time{}
				}
			}
		}
	}()

	sig := <-sigCh
	uptime := time.Since(daemonStartTime).Truncate(time.Second)
	debugLog.Printf("Shutting down daemon (signal=%v, uptime=%s)", sig, uptime)
	logEvent("DAEMON_STOP session=%s pid=%d signal=%v uptime=%s clients=%d", *sessionID, os.Getpid(), sig, uptime, server.ClientCount())
	crashLog.Printf("Daemon stopped: signal=%v pid=%d uptime=%s clients=%d", sig, os.Getpid(), uptime, server.ClientCount())

	// Write clean-stop sentinel so the watchdog knows this was intentional.
	// Skip if self-terminating due to LOOP_FATAL — that's a crash, not a clean stop.
	if !selfTerminating.Load() {
		sentinelPath := daemon.RuntimePath(*sessionID, ".clean-stop")
		os.WriteFile(sentinelPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
	}

	close(heartbeatDone)
	server.Stop()
}
