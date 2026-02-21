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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	zone "github.com/lrstanley/bubblezone"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

var crashLog *log.Logger
var eventLog *log.Logger
var inputLog *log.Logger

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

var inputLogEnabled bool
var inputLogCheckTime time.Time

func initInputLog(sessionID string) {
	inputLogPath := fmt.Sprintf("/tmp/tabby-daemon-%s-input.log", sessionID)
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

// spawnRenderersForNewWindows checks for windows without renderers and spawns them.
// Returns true if any renderer was spawned (caller should restore focus afterward).
func spawnRenderersForNewWindows(server *daemon.Server, sessionID string, windows []tmux.Window) bool {
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		logEvent("SPAWN_SKIP script_lock_active")
		return false
	}
	rendererBin := getRendererBin()
	if rendererBin == "" {
		return false
	}
	spawned := false

	// Get saved sidebar width or default
	width := "25"
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width").Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w > 0 {
			width = fmt.Sprintf("%d", w)
		}
	}

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
		hasRenderer := false
		if rawOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_current_command}\x1f#{pane_start_command}").Output(); err == nil {
			for _, rawLine := range strings.Split(strings.TrimSpace(string(rawOut)), "\n") {
				if rawLine == "" {
					continue
				}
				rawParts := strings.SplitN(rawLine, "\x1f", 2)
				curCmd := rawParts[0]
				startCmd := ""
				if len(rawParts) >= 2 {
					startCmd = rawParts[1]
				}
				if strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") ||
					strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") {
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

		// Spawn renderer in this window
		// Log active pane before spawn for debugging focus issues
		// Optimization: We can skip this log or assume activeWindow is correct enough
		logEvent("SPAWN_RENDERER window=%s pane=%s", windowID, firstPane)
		debugLog.Printf("Spawning renderer for new window %s (pane %s)", windowID, firstPane)

		// Use exec to replace shell with renderer (matches toggle_sidebar_daemon.sh behavior)
		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		cmdStr := fmt.Sprintf("exec '%s' -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
		cmd := exec.Command("tmux", "split-window", "-d", "-t", firstPane, "-h", "-b", "-f", "-l", width, cmdStr)
		if out, err := cmd.CombinedOutput(); err != nil {
			debugLog.Printf("Failed to spawn renderer: %v, output: %s", err, string(out))
			continue
		}

		spawned = true
		logEvent("SPAWN_COMPLETE window=%s", windowID)
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
	for _, win := range windows {
		windowID := win.ID
		var sidebarPaneID string
		nonSidebarCount := 0

		for _, p := range win.Panes {
			cmd := p.Command
			startCmd := p.StartCommand

			// Check if this is a system pane
			isSystem := strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") ||
				strings.Contains(cmd, "tabby") || strings.Contains(cmd, "pane-header") ||
				strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
				strings.Contains(startCmd, "tabby") || strings.Contains(startCmd, "pane-header")

			if isSystem {
				// We only care about the sidebar specifically for cleanup, but generic "system" check is safer
				// Actually, we want to close the window if ONLY system panes remain.
				// If we have multiple headers + sidebar but no content, we should close.
				// So we track the main sidebar pane ID to kill it (which usually closes the window if it's the last one)
				// Or we can just kill the window if count is 0.
				if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
					sidebarPaneID = p.ID
				}
			} else {
				nonSidebarCount++
			}
		}

		// If only system panes remain (and at least one is a sidebar/renderer), close the sidebar pane
		if nonSidebarCount == 0 && sidebarPaneID != "" {
			logEvent("CLEANUP_ORPHAN_SIDEBAR window=%s pane=%s -- only sidebar remains", windowID, sidebarPaneID)
			debugLog.Printf("Window %s has only sidebar pane, closing it", windowID)
			exec.Command("tmux", "kill-pane", "-t", sidebarPaneID).Run()
		}
	}
}

func paneIsSystemPane(cmd string, startCmd string) bool {
	return strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") ||
		strings.Contains(cmd, "tabby") || strings.Contains(cmd, "pane-header") ||
		strings.Contains(cmd, "tabbar") || strings.Contains(cmd, "pane-bar") ||
		strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
		strings.Contains(startCmd, "tabby") || strings.Contains(startCmd, "pane-header") ||
		strings.Contains(startCmd, "tabbar") || strings.Contains(startCmd, "pane-bar")
}

func cleanupOrphanWindowsByTmux(sessionID string) {
	if sessionID == "" {
		return
	}

	out, err := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}").Output()
	if err != nil {
		return
	}

	for _, rawWid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		windowID := strings.TrimSpace(rawWid)
		if windowID == "" {
			continue
		}

		paneOut, paneErr := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_dead}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		if paneErr != nil {
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(paneOut)), "\n")
		if len(lines) == 0 || (len(lines) == 1 && strings.TrimSpace(lines[0]) == "") {
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

		if hasSidebar && nonSystemLive == 0 {
			currentWindow := ""
			if curOut, curErr := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); curErr == nil {
				currentWindow = strings.TrimSpace(string(curOut))
			}
			if currentWindow == windowID {
				exec.Command("tmux", "last-window").Run()
			}
			logEvent("CLEANUP_ORPHAN_WINDOW window=%s source=daemon_fallback", windowID)
			exec.Command("tmux", "kill-window", "-t", windowID).Run()
		}
	}
}

// spawnPaneHeaders spawns header panes above each content pane in all windows.
// Each content pane gets its own header showing that pane's info and action buttons.
// Height is 1 line normally, or 2 lines when custom_border is enabled (to render our own border).
func spawnPaneHeaders(server *daemon.Server, sessionID string, customBorder bool, windows []tmux.Window) {
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		logEvent("HEADER_SPAWN_SKIP script_lock_active")
		return
	}
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
		logEvent("SPAWN_HEADER pane=%s window=%s active_before=%s width=%d height=%d custom_border=%v", pane.id, pane.windowID, activeBeforeHeader, pane.width, pane.height, customBorder)
		cmdStr := fmt.Sprintf("exec '%s' -session '%s' -pane '%s' %s", headerBin, sessionID, pane.id, debugFlag)
		// Split the target pane vertically (-v), placing header before/above (-b).
		// Height is 1 line normally. CustomBorder drag handle is rendered within the single line.
		headerHeight := "1"
		spawnCmd := exec.Command("tmux", "split-window", "-d", "-t", pane.id, "-v", "-b", "-l", headerHeight, cmdStr)
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
							exec.Command("tmux", "resize-pane", "-t", hParts[0], "-y", "1").Run()
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
func cleanupOrphanedHeaders(customBorder bool) {
	// Get all panes with start command, width, height, and window ID
	// Scoped to our session with -t to avoid cross-session interference
	listArgs := []string{"list-panes", "-s"}
	if *sessionID != "" {
		listArgs = append(listArgs, "-t", *sessionID)
	}
	listArgs = append(listArgs, "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_width}\x1f#{pane_start_command}\x1f#{pane_height}\x1f#{window_id}")
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
	contentPaneDimensions := make(map[string]struct{ width, height int })
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 6)
		if len(parts) < 4 {
			continue
		}
		paneID := parts[0]
		curCmd := parts[1]
		widthStr := parts[2]
		startCmd := parts[3]
		heightStr := ""
		if len(parts) >= 5 {
			heightStr = parts[4]
		}
		isSystem := strings.Contains(curCmd, "pane-header") || strings.Contains(curCmd, "sidebar") ||
			strings.Contains(curCmd, "renderer") || strings.Contains(curCmd, "tabby") ||
			strings.Contains(startCmd, "pane-header") || strings.Contains(startCmd, "sidebar") ||
			strings.Contains(startCmd, "renderer") || strings.Contains(startCmd, "tabby")
		if !isSystem {
			contentPaneExists[paneID] = true
			width, _ := strconv.Atoi(widthStr)
			height, _ := strconv.Atoi(heightStr)
			contentPaneDimensions[paneID] = struct{ width, height int }{width, height}
		}
	}

	// Collect header panes with their dimensions
	type headerInfo struct {
		paneID   string
		windowID string
		target   string
		width    int
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
		widthStr := parts[2]
		startCmd := parts[3]
		if !strings.Contains(curCmd, "pane-header") && !strings.Contains(startCmd, "pane-header") {
			continue
		}
		w, _ := strconv.Atoi(widthStr)
		h := 0
		if len(parts) >= 5 {
			h, _ = strconv.Atoi(parts[4])
		}
		winID := ""
		if len(parts) >= 6 {
			winID = parts[5]
		}
		target := paneTargetFromStartCmd(startCmd)
		headers = append(headers, headerInfo{
			paneID: parts[0], windowID: winID,
			target: target, width: w, height: h,
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
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		if hdr.target != "" && !keepHeader[hdr.paneID] {
			logEvent("CLEANUP_HEADER pane=%s target=%s reason=duplicate_target", hdr.paneID, hdr.target)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}

		// Force header height to expected size if it's grown beyond it
		// Expected: always 1 line (custom_border drag handle is rendered inline)
		expectedHeight := 1
		if hdr.height > expectedHeight {
			debugLog.Printf("Header %s height=%d, forcing to %d", hdr.paneID, hdr.height, expectedHeight)
			exec.Command("tmux", "resize-pane", "-t", hdr.paneID, "-y", fmt.Sprintf("%d", expectedHeight)).Run()
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

		// Kill if header width doesn't match target pane width (happens after horizontal splits)
		if targetDims, ok := contentPaneDimensions[hdr.target]; ok {
			if hdr.width != targetDims.width {
				logEvent("CLEANUP_HEADER pane=%s target=%s reason=width_mismatch header_w=%d target_w=%d", hdr.paneID, hdr.target, hdr.width, targetDims.width)
				exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
				killed = true
				continue
			}
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

	// Scoped to our session to avoid resizing panes in other sessions
	restoreArgs := []string{"list-panes", "-s"}
	if *sessionID != "" {
		restoreArgs = append(restoreArgs, "-t", *sessionID)
	}
	restoreArgs = append(restoreArgs, "-F",
		"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_width}")
	out, err := exec.Command("tmux", restoreArgs...).Output()
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

// syncClientSizesFromTmux updates all client widths/heights from actual tmux pane sizes
// This ensures background sidebars get correct dimensions on resize events
func syncClientSizesFromTmux(server *daemon.Server) {
	// Get all panes with their sizes and commands
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{window_id}\x1f#{pane_id}\x1f#{pane_width}\x1f#{pane_height}\x1f#{pane_current_command}").Output()
	if err != nil {
		return
	}

	// Build a map of window ID -> sidebar pane dimensions
	sidebarSizes := make(map[string]struct{ width, height int })

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		windowID := parts[0]
		// paneID := parts[1]
		width, _ := strconv.Atoi(parts[2])
		height, _ := strconv.Atoi(parts[3])
		cmd := parts[4]

		// Check if this is a sidebar/renderer pane
		if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
			sidebarSizes[windowID] = struct{ width, height int }{width, height}
		}
	}

	// Update server client sizes
	for windowID, size := range sidebarSizes {
		server.UpdateClientSize(windowID, size.width, size.height)
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

	// Initialize logging early
	initCrashLog(*sessionID)
	initEventLog(*sessionID)
	initInputLog(*sessionID)
	defer recoverAndLog("main")

	// Scope tmux queries to this session
	tmux.SetSessionTarget(*sessionID)

	if *debugMode {
		debugLog = log.New(os.Stderr, "[daemon] ", log.LstdFlags|log.Lmicroseconds)
		SetCoordinatorDebugLog(debugLog)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	debugLog.Printf("Starting daemon for session %s", *sessionID)
	crashLog.Printf("Daemon started for session %s", *sessionID)

	// Save current window and pane for focus restoration after setup
	saveFocusState(*sessionID)

	// Create coordinator for centralized rendering
	coordinator := NewCoordinator(*sessionID)

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
		return coordinator.RenderForClient(clientID, width, height)
	}

	refreshCh := make(chan struct{}, 10)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Set up input callback with panic recovery
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		logEvent("INPUT_START client=%s type=%s btn=%s action=%s x=%d y=%d", clientID, input.Type, input.Button, input.Action, input.MouseX, input.MouseY)
		defer func() {
			if r := recover(); r != nil {
				debugLog.Printf("PANIC in OnInput handler (client=%s): %v", clientID, r)
				logEvent("PANIC_INPUT client=%s err=%v", clientID, r)
			}
		}()
		needsRefresh := coordinator.HandleInput(clientID, input)
		logEvent("INPUT_HANDLED client=%s needsRefresh=%v", clientID, needsRefresh)
		if needsRefresh {
			// Signal the main refresh loop instead of doing expensive
			// RefreshWindows + BroadcastRender inline. The tmux action
			// (select_pane, select_window, etc.) already ran synchronously
			// in HandleInput, so tmux state is updated. The refresh loop
			// will pick this up and debounce with any USR1 signals from
			// tmux hooks triggered by the same action.
			select {
			case refreshCh <- struct{}{}:
			default:
				// Channel full, refresh already pending
			}
			logEvent("INPUT_SIGNALED_REFRESH client=%s", clientID)
		} else {
			// Internal-only state change (e.g. toggle_group) - render immediately
			// since no tmux state changed and no USR1 will arrive
			server.BroadcastRender()
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
	server.OnResize = func(clientID string, width, height int, paneID string) {
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

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	debugLog.Printf("Server listening on %s", server.GetSocketPath())
	logEvent("DAEMON_START session=%s pid=%d", *sessionID, os.Getpid())
	resetTerminalModes(*sessionID)

	// Start deadlock watchdog
	StartDeadlockWatchdog()

	// Restore focus after daemon initialization completes
	// Wait for renderers to spawn and settle before restoring focus
	go func() {
		time.Sleep(1500 * time.Millisecond)
		restoreFocusState()
	}()

	// Listen for SIGUSR1 signals from tmux hooks for instant refresh
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

	// Start coordinator refresh loops with change detection
	go func() {
		defer recoverAndLog("refresh-loop")

		runLoopTask := func(task string, timeout time.Duration, fn func()) bool {
			done := make(chan struct{})
			go func() {
				defer close(done)
				fn()
			}()

			select {
			case <-done:
				return true
			case <-time.After(timeout):
				logEvent("LOOP_STALL task=%s timeout_ms=%d", task, timeout.Milliseconds())
				if crashLog != nil {
					crashLog.Printf("LOOP_STALL task=%s timeout=%v", task, timeout)
				}
				select {
				case sigCh <- syscall.SIGTERM:
				default:
				}
				return false
			}
		}

		refreshTicker := time.NewTicker(5 * time.Second)          // Window list poll (fallback, less frequent now)
		windowCheckTicker := time.NewTicker(2 * time.Second)      // Spawn/cleanup poll (fallback)
		animationTicker := time.NewTicker(100 * time.Millisecond) // Combined spinner + pet animation (was two separate tickers)
		gitTicker := time.NewTicker(5 * time.Second)              // Git status
		watchdogTicker := time.NewTicker(5 * time.Second)         // Watchdog: check renderer health
		defer refreshTicker.Stop()
		defer windowCheckTicker.Stop()
		defer animationTicker.Stop()
		defer gitTicker.Stop()
		defer watchdogTicker.Stop()

		lastWindowsHash := ""
		lastGitState := ""
		activeWindowID := "" // Track active window for optimized rendering

		// Helper to get current active window ID (cached, updated on events)
		updateActiveWindow := func() {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
				activeWindowID = strings.TrimSpace(string(out))
			}
		}
		updateActiveWindow() // Initial fetch

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
		paneLayoutCooldown := 50 * time.Millisecond

		doPaneLayoutOps := func() {
			now := time.Now()
			if now.Sub(lastPaneLayoutOps) < paneLayoutCooldown {
				logEvent("PANE_LAYOUT_SKIP cooldown_remaining=%dms", (paneLayoutCooldown - now.Sub(lastPaneLayoutOps)).Milliseconds())
				return
			}
			lastPaneLayoutOps = now
			logEvent("PANE_LAYOUT_START")
			customBorder := coordinator.GetConfig().PaneHeader.CustomBorder
			spawnPaneHeaders(server, *sessionID, customBorder, coordinator.GetWindows())
			cleanupOrphanedHeaders(customBorder)
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
			// Record heartbeat at each loop iteration for deadlock detection
			recordHeartbeat()

			select {
			case <-refreshCh:
				ok := runLoopTask("signal_refresh", 8*time.Second, func() {
					start := time.Now()
					logEvent("SIGNAL_REFRESH session=%s", *sessionID)

					updateActiveWindow()
					coordinator.RefreshWindows()
					t1 := time.Now()

					windows := coordinator.GetWindows()
					spawnRenderersForNewWindows(server, *sessionID, windows)
					t2 := time.Now()

					cleanupOrphanedSidebars(windows)
					cleanupOrphanWindowsByTmux(*sessionID)
					t3 := time.Now()

					cleanupSidebarsForClosedWindows(server, windows)
					t4 := time.Now()

					doPaneLayoutOps()
					t5 := time.Now()

					currentHash := coordinator.GetWindowsHash()
					if currentHash != lastWindowsHash {
						updateHeaderBorderStyles(coordinator)
					}
					syncClientSizesFromTmux(server)
					server.BroadcastRender()
					t6 := time.Now()
					lastWindowsHash = currentHash

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

					debugLog.Printf("PERF: RefreshWindows=%v Spawn=%v Cleanup1=%v Cleanup2=%v Layout=%v Render=%v TOTAL=%v",
						t1.Sub(start), t2.Sub(t1), t3.Sub(t2), t4.Sub(t3), t5.Sub(t4), t6.Sub(t5), t6.Sub(start))
				})
				if !ok {
					return
				}
			case <-windowCheckTicker.C: // Fallback polling: spawn/cleanup for missed events
				ok := runLoopTask("window_check", 8*time.Second, func() {
					logEvent("WINDOW_CHECK_TICK")
					// Update active window in case user switched windows without triggering refresh
					updateActiveWindow()
					// Refresh windows first to get latest state for fallback ops
					coordinator.RefreshWindows()
					windows := coordinator.GetWindows()

					spawnRenderersForNewWindows(server, *sessionID, windows)
					cleanupOrphanedSidebars(windows)
					cleanupOrphanWindowsByTmux(*sessionID)
					cleanupSidebarsForClosedWindows(server, windows)
					doPaneLayoutOps()
				})
				if !ok {
					return
				}
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
				// Combined spinner + pet animation tick (was two separate tickers causing 2x BroadcastRender)
				// Only render if something visual actually changed (dirty flag pattern)
				spinnerVisible := coordinator.IncrementSpinner()
				petChanged := coordinator.UpdatePetState()
				indicatorAnimated := coordinator.HasActiveIndicatorAnimation()
				if spinnerVisible || petChanged || indicatorAnimated {
					perf.Log("animationTick (render)")
					// Only render active window during animation ticks (hidden windows don't need updates)
					server.RenderActiveWindowOnly(activeWindowID)
				}
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
	debugLog.Printf("Shutting down daemon (signal=%v)", sig)
	logEvent("DAEMON_STOP session=%s pid=%d signal=%v", *sessionID, os.Getpid(), sig)
	server.Stop()
}
