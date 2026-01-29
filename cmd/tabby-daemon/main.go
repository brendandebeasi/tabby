package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	zone "github.com/lrstanley/bubblezone"

	"github.com/b/tmux-tabs/pkg/daemon"
)

var crashLog *log.Logger
var eventLog *log.Logger

func initCrashLog(sessionID string) {
	crashLogPath := fmt.Sprintf("/tmp/tabby-daemon-%s-crash.log", sessionID)
	f, err := os.OpenFile(crashLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		crashLog = log.New(os.Stderr, "[CRASH] ", log.LstdFlags)
		return
	}
	crashLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func initEventLog(sessionID string) {
	eventLogPath := fmt.Sprintf("/tmp/tabby-daemon-%s-events.log", sessionID)
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

// spawnRenderersForNewWindows checks for windows without renderers and spawns them
func spawnRenderersForNewWindows(server *daemon.Server, sessionID string) {
	rendererBin := getRendererBin()
	if rendererBin == "" {
		return
	}

	// Get all windows in the session
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		debugLog.Printf("spawnRenderers: failed to list windows: %v", err)
		return
	}

	// Get the currently active window so we only select-pane in it
	activeWindowOut, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	activeWindow := strings.TrimSpace(string(activeWindowOut))

	// Get connected clients (each identified by their window ID)
	connectedClients := make(map[string]bool)
	for _, clientID := range server.GetAllClientIDs() {
		connectedClients[clientID] = true
	}

	windowIDs := strings.Split(strings.TrimSpace(string(out)), "\n")
	debugLog.Printf("spawnRenderers: windows=%v active=%s clients=%v", windowIDs, activeWindow, connectedClients)

	// Check each window
	for _, windowID := range windowIDs {
		windowID = strings.TrimSpace(windowID)
		if windowID == "" {
			continue
		}

		// Skip if already has a renderer
		if connectedClients[windowID] {
			logEvent("SPAWN_CHECK window=%s result=skip_has_client", windowID)
			continue
		}

		// Check if window already has a sidebar/renderer pane (in case renderer hasn't connected yet)
		// Check BOTH current command AND start command: after split-window, pane_current_command
		// is briefly "zsh" (default shell) before exec replaces it with the renderer binary.
		// The after-split-window hook fires during this window, so we must also check start command.
		paneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_current_command}\x1f#{pane_start_command}").Output()
		if err != nil {
			continue
		}
		hasRenderer := false
		for _, line := range strings.Split(string(paneOut), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "sidebar") || strings.Contains(line, "renderer") {
				hasRenderer = true
				break
			}
		}
		if hasRenderer {
			logEvent("SPAWN_CHECK window=%s result=skip_has_pane", windowID)
			continue
		}

		// Get first pane in window for splitting
		firstPaneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}").Output()
		if err != nil {
			continue
		}
		firstPane := strings.TrimSpace(strings.Split(string(firstPaneOut), "\n")[0])
		if firstPane == "" {
			continue
		}

		// Spawn renderer in this window
		// Log active pane before spawn for debugging focus issues
		activeBeforeSpawn := ""
		if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			activeBeforeSpawn = strings.TrimSpace(string(out))
		}
		logEvent("SPAWN_RENDERER window=%s pane=%s active_before=%s", windowID, firstPane, activeBeforeSpawn)
		debugLog.Printf("Spawning renderer for new window %s (pane %s)", windowID, firstPane)
		// Use exec to replace shell with renderer (matches toggle_sidebar_daemon.sh behavior)
		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		cmdStr := fmt.Sprintf("exec '%s' -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
		cmd := exec.Command("tmux", "split-window", "-d", "-t", firstPane, "-h", "-b", "-f", "-l", "25", cmdStr)
		if out, err := cmd.CombinedOutput(); err != nil {
			debugLog.Printf("Failed to spawn renderer: %v, output: %s", err, string(out))
			continue
		}

		// After spawning, resize the sidebar pane and focus main pane
		// Get the sidebar pane (leftmost pane after split)
		sidebarPaneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}").Output()
		if err == nil {
			for _, line := range strings.Split(string(sidebarPaneOut), "\n") {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "sidebar") || strings.Contains(line, "renderer") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) >= 1 {
						sidebarPane := parts[0]
						// Get window height from the main pane
						heightOut, err := exec.Command("tmux", "display-message", "-t", firstPane, "-p", "#{pane_height}").Output()
						if err == nil {
							windowHeight := strings.TrimSpace(string(heightOut))
							exec.Command("tmux", "resize-pane", "-t", sidebarPane, "-x", "25", "-y", windowHeight).Run()
						}
						break
					}
				}
			}
		}

		logEvent("SPAWN_COMPLETE window=%s", windowID)
	}
}

// cleanupSidebarsForClosedWindows removes sidebar panes from windows that no longer exist
func cleanupSidebarsForClosedWindows(server *daemon.Server) {
	// Get current windows
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		return
	}

	currentWindows := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			currentWindows[line] = true
		}
	}

	// Check each connected client - if their window no longer exists, disconnect them
	for _, clientID := range server.GetAllClientIDs() {
		if !currentWindows[clientID] {
			debugLog.Printf("Window %s no longer exists, client will be cleaned up", clientID)
			// The client will disconnect when the pane closes
		}
	}
}

// cleanupOrphanedSidebars closes sidebar panes in windows where all other panes were closed
func cleanupOrphanedSidebars() {
	// Get all windows
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	if err != nil {
		return
	}

	windowIDs := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, windowID := range windowIDs {
		windowID = strings.TrimSpace(windowID)
		if windowID == "" {
			continue
		}

		// Get all panes in this window
		paneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}").Output()
		if err != nil {
			continue
		}

		panes := strings.Split(strings.TrimSpace(string(paneOut)), "\n")
		var sidebarPaneID string
		nonSidebarCount := 0

		for _, line := range panes {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) < 2 {
				continue
			}
			paneID := parts[0]
			cmd := parts[1]

			if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") || strings.Contains(cmd, "tabby") || strings.Contains(cmd, "pane-header") {
				sidebarPaneID = paneID
			} else {
				nonSidebarCount++
			}
		}

		// If only sidebar pane remains, close it (which closes the window)
		if nonSidebarCount == 0 && sidebarPaneID != "" {
			logEvent("CLEANUP_ORPHAN_SIDEBAR window=%s pane=%s -- only sidebar remains", windowID, sidebarPaneID)
			debugLog.Printf("Window %s has only sidebar pane, closing it", windowID)
			exec.Command("tmux", "kill-pane", "-t", sidebarPaneID).Run()
		}
	}
}

// spawnPaneHeaders spawns 1-line header panes above each content pane in all windows.
// Each content pane gets its own header showing that pane's info and action buttons.
func spawnPaneHeaders(server *daemon.Server, sessionID string) {
	headerBin := getPaneHeaderBin()
	if headerBin == "" {
		return
	}

	// Check if pane headers are enabled
	out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	if err != nil || strings.TrimSpace(string(out)) != "on" {
		return
	}

	// Get all panes in session with both current and start commands.
	// We check pane_start_command to handle the race condition where split-window
	// fires hooks before exec replaces the shell (pane_current_command briefly shows "zsh").
	paneOut, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{window_id}\x1f#{pane_height}\x1f#{pane_start_command}").Output()
	if err != nil {
		return
	}

	// Track which content panes already have headers (connected to daemon or process exists)
	panesWithHeader := make(map[string]bool) // content paneID -> has header
	for _, clientID := range server.GetAllClientIDs() {
		if strings.HasPrefix(clientID, "header:") {
			panesWithHeader[strings.TrimPrefix(clientID, "header:")] = true
		}
	}

	// First pass: identify system panes vs content panes
	type paneEntry struct {
		id       string
		windowID string
		height   int
	}
	var contentPanes []paneEntry

	// Track panes that have a header process (even if not yet connected to daemon)
	for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		pID := parts[0]
		curCmd := parts[1]
		winID := parts[2]
		heightStr := parts[3]
		startCmd := parts[4]

		// Check if this is a system pane (sidebar/renderer/header/daemon)
		// by checking BOTH current command and start command
		isSystem := strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") ||
			strings.Contains(curCmd, "pane-header") || strings.Contains(curCmd, "tabby") ||
			strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
			strings.Contains(startCmd, "pane-header") || strings.Contains(startCmd, "tabby")

		if isSystem {
			// Track which content panes already have a header process running
			if strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
				matches := paneTargetRegex.FindStringSubmatch(startCmd)
				if len(matches) >= 2 {
					panesWithHeader[matches[1]] = true
				}
			}
			continue
		}

		h, _ := strconv.Atoi(heightStr)
		contentPanes = append(contentPanes, paneEntry{id: pID, windowID: winID, height: h})
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
		logEvent("SPAWN_HEADER pane=%s window=%s active_before=%s", pane.id, pane.windowID, activeBeforeHeader)
		cmdStr := fmt.Sprintf("exec '%s' -session '%s' -pane '%s' %s", headerBin, sessionID, pane.id, debugFlag)
		spawnCmd := exec.Command("tmux", "split-window", "-d", "-t", pane.id, "-v", "-b", "-l", "1", cmdStr)
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
							exec.Command("tmux", "set-option", "-p", "-t", hParts[0], "pane-border-status", "off").Run()
						}
					}
				}
			}
		}
		// Restore sidebar widths if we spawned headers (layout may have shifted)
		restoreSidebarWidths()
	}
}

// paneTargetRegex extracts the -pane argument from a pane-header start command
var paneTargetRegex = regexp.MustCompile(`-pane\s+'([^']+)'`)

// cleanupOrphanedHeaders removes header panes that are disabled or orphaned
// (target pane no longer exists).
func cleanupOrphanedHeaders() {
	// Get all panes with start command, width, height, and window ID
	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_width}\x1f#{pane_start_command}\x1f#{pane_height}\x1f#{window_id}").Output()
	if err != nil {
		return
	}

	// Check if pane headers are disabled
	enabledOut, _ := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	headersEnabled := strings.TrimSpace(string(enabledOut)) == "on"

	// Build a set of content pane IDs
	// Check both current and start commands to handle race condition after split-window
	contentPaneExists := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 6)
		if len(parts) < 4 {
			continue
		}
		curCmd := parts[1]
		startCmd := parts[3]
		isSystem := strings.Contains(curCmd, "pane-header") || strings.Contains(curCmd, "sidebar") ||
			strings.Contains(curCmd, "renderer") || strings.Contains(curCmd, "tabby") ||
			strings.Contains(startCmd, "pane-header") || strings.Contains(startCmd, "sidebar") ||
			strings.Contains(startCmd, "renderer") || strings.Contains(startCmd, "tabby")
		if !isSystem {
			contentPaneExists[parts[0]] = true
		}
	}

	// Collect header panes
	type headerInfo struct {
		paneID   string
		windowID string
		target   string
		height   int
	}
	var headers []headerInfo

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 6)
		if len(parts) < 4 {
			continue
		}
		curCmd := parts[1]
		startCmd := parts[3]
		if !strings.Contains(curCmd, "pane-header") && !strings.Contains(startCmd, "pane-header") {
			continue
		}
		h := 0
		if len(parts) >= 5 {
			h, _ = strconv.Atoi(parts[4])
		}
		winID := ""
		if len(parts) >= 6 {
			winID = parts[5]
		}
		matches := paneTargetRegex.FindStringSubmatch(startCmd)
		target := ""
		if len(matches) >= 2 {
			target = matches[1]
		}
		headers = append(headers, headerInfo{
			paneID: parts[0], windowID: winID,
			target: target, height: h,
		})
	}

	// Process headers: kill disabled or orphaned
	killed := false
	for _, hdr := range headers {
		// Kill if headers disabled globally
		if !headersEnabled {
			logEvent("CLEANUP_HEADER pane=%s reason=disabled", hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		// Force header height to 1 if it's grown
		if hdr.height > 1 {
			debugLog.Printf("Header %s height=%d, forcing to 1", hdr.paneID, hdr.height)
			exec.Command("tmux", "resize-pane", "-t", hdr.paneID, "-y", "1").Run()
		}

		// Kill if target pane no longer exists
		if hdr.target == "" {
			continue
		}
		if !contentPaneExists[hdr.target] {
			logEvent("CLEANUP_HEADER pane=%s target=%s reason=target_gone", hdr.paneID, hdr.target)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
	}

	// Restore sidebar widths if we killed any headers (layout may have shifted)
	if killed {
		restoreSidebarWidths()
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

	// Get all panes with PID info
	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_pid}\x1f#{window_id}\x1f#{pane_dead}").Output()
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

// restoreSidebarWidths ensures all sidebar panes match the saved desired width.
// Reads @tabby_sidebar_width (set by grow/shrink buttons) with a minimum of 15.
func restoreSidebarWidths() {
	// Read saved desired width
	desiredWidth := 25
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width").Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w >= 15 {
			desiredWidth = w
		}
	}

	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_width}").Output()
	if err != nil {
		return
	}
	widthStr := strconv.Itoa(desiredWidth)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		cmd := parts[1]
		if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
			width, _ := strconv.Atoi(parts[2])
			if width != desiredWidth && width > 0 {
				exec.Command("tmux", "resize-pane", "-t", parts[0], "-x", widthStr).Run()
			}
		}
	}
}

// updateHeaderBorderStyles sets pane-border-style on each header pane so borders
// use the header's text color as fg (for visible divider lines) and tab color as bg.
func updateHeaderBorderStyles(coordinator *Coordinator) {
	// Get all panes with start command info
	out, err := exec.Command("tmux", "list-panes", "-s", "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
	if err != nil {
		return
	}

	// Also get sidebar bg color for sidebar panes
	sidebarBg := coordinator.GetSidebarBg()

	// Collect individual tmux commands to run
	type tmuxCmd struct {
		args []string
	}
	var cmds []tmuxCmd

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
			continue
		}
		paneID := parts[0]
		cmd := parts[1]
		startCmd := parts[2]

		// Set sidebar pane background color (use set-option, NOT select-pane -P which steals focus)
		if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
			if sidebarBg != "" {
				cmds = append(cmds, tmuxCmd{[]string{"set-option", "-p", "-t", paneID, "pane-style", fmt.Sprintf("bg=%s", sidebarBg)}})
			}
			continue
		}

		if !strings.Contains(cmd, "pane-header") {
			continue
		}

		// Extract target pane ID from start command (-pane '%123')
		matches := paneTargetRegex.FindStringSubmatch(startCmd)
		if len(matches) < 2 {
			continue
		}
		targetPaneID := matches[1]

		colors := coordinator.GetHeaderColorsForPane(targetPaneID)
		if colors.Bg == "" {
			continue
		}

		// Fg = text color (visible divider line characters), Bg = tab/header color
		style := fmt.Sprintf("fg=%s,bg=%s", colors.Fg, colors.Bg)
		cmds = append(cmds, tmuxCmd{[]string{"set-option", "-p", "-t", paneID, "pane-border-style", style}})
		cmds = append(cmds, tmuxCmd{[]string{"set-option", "-p", "-t", paneID, "pane-active-border-style", style}})
	}

	// Run each command individually (tmux ';' separator doesn't work well with exec)
	for _, c := range cmds {
		exec.Command("tmux", c.args...).Run()
	}
}

// resetTerminalModes sends escape sequences to disable mouse tracking modes
// This is called on daemon startup to clean up stale state from crashed renderers
func resetTerminalModes(sessionID string) {
	// Use tmux's refresh-client to reset terminal state
	// The -S flag forces a full refresh which can help reset stuck modes
	exec.Command("tmux", "refresh-client", "-S").Run()

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

	// Initialize logging early
	initCrashLog(*sessionID)
	initEventLog(*sessionID)
	defer recoverAndLog("main")

	if *debugMode {
		debugLog = log.New(os.Stderr, "[daemon] ", log.LstdFlags|log.Lmicroseconds)
		SetCoordinatorDebugLog(debugLog)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	debugLog.Printf("Starting daemon for session %s", *sessionID)
	crashLog.Printf("Daemon started for session %s", *sessionID)

	// Create coordinator for centralized rendering
	coordinator := NewCoordinator(*sessionID)

	// Enable coordinator debug logging if debug mode is on
	if *debugMode {
		SetCoordinatorDebugLog(debugLog)
	}

	// Create server
	server := daemon.NewServer(*sessionID)

	// Set up render callback using coordinator (with panic recovery)
	server.OnRenderNeeded = func(clientID string, width, height int) (result *daemon.RenderPayload) {
		defer func() {
			if r := recover(); r != nil {
				debugLog.Printf("PANIC in OnRenderNeeded (client=%s): %v", clientID, r)
				logEvent("PANIC_RENDER client=%s err=%v", clientID, r)
				result = nil
			}
		}()
		// Route header clients to pane header renderer
		if strings.HasPrefix(clientID, "header:") {
			return coordinator.RenderHeaderForClient(clientID, width, height)
		}
		return coordinator.RenderForClient(clientID, width, height)
	}

	// Set up input callback with panic recovery
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		defer func() {
			if r := recover(); r != nil {
				debugLog.Printf("PANIC in OnInput handler (client=%s): %v", clientID, r)
				logEvent("PANIC_INPUT client=%s err=%v", clientID, r)
			}
		}()
		needsRefresh := coordinator.HandleInput(clientID, input)
		if needsRefresh {
			// Only refresh windows for window-related actions (expensive tmux calls)
			coordinator.RefreshWindows()
		}
		// Re-render all clients with fresh state
		server.BroadcastRender()
	}

	// Set up connect/disconnect callbacks
	server.OnConnect = func(clientID string) {
		logEvent("CLIENT_CONNECT client=%s", clientID)
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

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	debugLog.Printf("Server listening on %s", server.GetSocketPath())
	logEvent("DAEMON_START session=%s pid=%d", *sessionID, os.Getpid())

	// Channel for event-driven refresh (SIGUSR1 from tmux hooks)
	refreshCh := make(chan struct{}, 10)

	// Listen for SIGUSR1 signals from tmux hooks for instant refresh
	refreshSigCh := make(chan os.Signal, 10)
	signal.Notify(refreshSigCh, syscall.SIGUSR1)
	go func() {
		for range refreshSigCh {
			select {
			case refreshCh <- struct{}{}:
			default:
				// Channel full, refresh already pending
			}
		}
	}()

	// Start coordinator refresh loops with change detection
	go func() {
		defer recoverAndLog("refresh-loop")

		refreshTicker := time.NewTicker(5 * time.Second)        // Window list poll (fallback, less frequent now)
		windowCheckTicker := time.NewTicker(2 * time.Second)    // Spawn/cleanup poll (fallback)
		spinnerTicker := time.NewTicker(100 * time.Millisecond) // Spinner animation
		gitTicker := time.NewTicker(5 * time.Second)            // Git status
		petTicker := time.NewTicker(100 * time.Millisecond)     // Pet state updates (for smooth animation)
		watchdogTicker := time.NewTicker(5 * time.Second)       // Watchdog: check renderer health
		defer refreshTicker.Stop()
		defer windowCheckTicker.Stop()
		defer spinnerTicker.Stop()
		defer gitTicker.Stop()
		defer petTicker.Stop()
		defer watchdogTicker.Stop()

		lastWindowsHash := ""
		lastGitState := ""

		// Debounce pane layout operations (spawn/cleanup headers) to prevent
		// feedback loops: these ops trigger pane-focus-in hooks which send USR1
		// back to us, causing re-entry. 500ms cooldown breaks the cycle.
		var lastPaneLayoutOps time.Time
		paneLayoutCooldown := 500 * time.Millisecond

		doPaneLayoutOps := func() {
			now := time.Now()
			if now.Sub(lastPaneLayoutOps) < paneLayoutCooldown {
				logEvent("PANE_LAYOUT_SKIP cooldown_remaining=%dms", (paneLayoutCooldown - now.Sub(lastPaneLayoutOps)).Milliseconds())
				return
			}
			lastPaneLayoutOps = now
			logEvent("PANE_LAYOUT_START")
			spawnPaneHeaders(server, *sessionID)
			cleanupOrphanedHeaders()
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

		for {
			select {
			case <-refreshCh:
				logEvent("SIGNAL_REFRESH session=%s", *sessionID)
				coordinator.RefreshWindows()
				spawnRenderersForNewWindows(server, *sessionID)
				cleanupOrphanedSidebars()
				cleanupSidebarsForClosedWindows(server)
				doPaneLayoutOps()
				currentHash := coordinator.GetWindowsHash()
				if currentHash != lastWindowsHash {
					updateHeaderBorderStyles(coordinator)
				}
				server.BroadcastRender()
				lastWindowsHash = currentHash
			case <-windowCheckTicker.C:
				// Fallback polling: spawn/cleanup for missed events
				logEvent("WINDOW_CHECK_TICK")
				spawnRenderersForNewWindows(server, *sessionID)
				cleanupOrphanedSidebars()
				cleanupSidebarsForClosedWindows(server)
				doPaneLayoutOps()
			case <-watchdogTicker.C:
				watchdogCheckRenderers(server, *sessionID)
			case <-refreshTicker.C:
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
			case <-spinnerTicker.C:
				// Spinner always updates (for animation)
				coordinator.IncrementSpinner()
				server.BroadcastRender()
			case <-gitTicker.C:
				// Only broadcast if git state changed
				currentGitState := coordinator.GetGitStateHash()
				if currentGitState != lastGitState {
					coordinator.RefreshGit()
					coordinator.RefreshSession()
					server.BroadcastRender()
					lastGitState = currentGitState
				}
			case <-petTicker.C:
				// Pet always updates (for animation and state changes)
				coordinator.UpdatePetState()
				server.BroadcastRender()
			}
		}
	}()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

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
				pidPath := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", *sessionID)
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
				out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
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

	<-sigCh
	debugLog.Printf("Shutting down daemon")
	logEvent("DAEMON_STOP session=%s pid=%d", *sessionID, os.Getpid())
	server.Stop()
}
