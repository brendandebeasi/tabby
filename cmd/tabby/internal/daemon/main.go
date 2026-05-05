package daemon

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
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

var crashLog *log.Logger
var eventLog *log.Logger
var inputLog *log.Logger
var daemonStartTime time.Time

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
	sessionID *string
	debugMode *bool
)

// Initialize the flag pointers at package load time so test helpers that
// touch daemon state without calling Run don't nil-deref. Run() reassigns
// these via its own FlagSet.
func init() {
	empty := ""
	falseVal := false
	sessionID = &empty
	debugMode = &falseVal
}

var debugLog *log.Logger

// tabbyExe returns the path to the consolidated tabby binary (ourselves).
// All renderers are now subcommands of this binary.
func tabbyExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// rendererExecPrefix returns a shell-safe command prefix that launches a
// renderer subcommand with its argv[0] set to a legacy name (sidebar-renderer,
// window-header, pane-header, tabby-sidebar-popup). The legacy argv[0] keeps
// tmux's #{pane_current_command} showing the expected name so the daemon's
// substring-based detection (strings.Contains(cmd, "sidebar") etc.) works
// unchanged after consolidation.
//
// Returns the fully-quoted `exec -a <name> <tabby> render <kind>` string;
// callers append their own -flag args.
func rendererExecPrefix(argv0, kind string) string {
	exe := tabbyExe()
	if exe == "" {
		return ""
	}
	return fmt.Sprintf("exec -a %s '%s' render %s", argv0, exe, kind)
}

// getRendererBin (legacy name preserved) now returns an exec-command prefix
// for the sidebar renderer rather than a raw binary path. Callers that
// previously did fmt.Sprintf("exec '%s' -flags", rendererBin) now just do
// fmt.Sprintf("%s -flags", rendererBin) because the "exec '...'" is baked in.
func getRendererBin() string {
	return rendererExecPrefix("sidebar-renderer", "sidebar")
}

func getWindowHeaderBin() string {
	return rendererExecPrefix("window-header", "window-header")
}

func getPaneHeaderBin() string {
	return rendererExecPrefix("pane-header", "pane-header")
}

func getPopupBin() string {
	return rendererExecPrefix("tabby-sidebar-popup", "sidebar-popup")
}

// saveLayoutBeforeKill saves the current window layout for a pane's window
// so that tabby-hook preserve-pane-ratios can restore it after the kill.
// This MUST be called before user/content-pane kill-pane operations.
func saveLayoutBeforeKill(paneID string) {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}|||#{window_layout}").Output()
	if err != nil {
		return
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|||", 2)
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
	if coordinator.sidebarHidden {
		logEvent("SPAWN_SKIP reason=sidebar_hidden")
		return false
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		logEvent("SPAWN_SKIP script_lock_active")
		return false
	}
	// Collect windows whose sidebar is currently stashed (break-pane'd out).
	// Those sidebars are alive in holding windows — don't re-spawn.
	stashedWindows := make(map[string]bool)
	if stashOut, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_name}").Output(); err == nil {
		for _, name := range strings.Split(strings.TrimSpace(string(stashOut)), "\n") {
			if !strings.HasPrefix(name, "_tabby_stash_") {
				continue
			}
			orig := "@" + strings.TrimPrefix(name, "_tabby_stash_")
			stashedWindows[orig] = true
		}
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
		// Skip if the sidebar for this window is currently stashed off-screen
		// (user hid it via the hamburger). The stashed sidebar-renderer is
		// still alive and will be join-pane'd back when the user shows it.
		if stashedWindows[windowID] {
			logEvent("SPAWN_CHECK window=%s result=skip_stashed", windowID)
			continue
		}

		// Live check: query tmux directly for ANY sidebar/renderer pane in this window.
		// The cached win.Panes has sidebar panes filtered out by ListWindowsWithPanes,
		// so we must ask tmux directly. This also catches renderers from other daemons.
		// Dead system panes (from a crashed daemon) are killed here so focus can escape them.
		hasRenderer := false
		if rawOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_id}|||#{pane_dead}|||#{pane_current_command}|||#{pane_start_command}").Output(); err == nil {
			for _, rawLine := range strings.Split(strings.TrimSpace(string(rawOut)), "\n") {
				if rawLine == "" {
					continue
				}
				rawParts := strings.SplitN(rawLine, "|||", 4)
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
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && %s -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
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
		// Clients are usually window IDs (e.g. "@1") or "window-header:@1" or "header:%1"
		targetID := clientID
		if isHeaderClient(clientID) {
			// Window/pane headers die naturally when their window/pane closes.
			continue
		}

		if !currentWindows[targetID] {
			debugLog.Printf("Window %s no longer exists, client will be cleaned up", clientID)
			// The client will disconnect when the pane closes
		}
	}
}

// cleanupOrphanedSidebars closes sidebar panes in windows where all other panes were closed
func cleanupOrphanedSidebars(windows []tmux.Window, coordinator *Coordinator) {
	// Skip cleanup during new window creation to prevent killing windows
	// whose content pane hasn't been detected yet (race condition).
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}
	if coordinator != nil {
		status := coordinator.NewWindowStatus()
		if status.State == "inFlight" || status.State == "ready" {
			return
		}
	}

	for _, win := range windows {
		windowID := win.ID
		if windowID == "" {
			continue
		}
		// Sidebar stash windows hold break-pane'd sidebars while hidden on
		// mobile. They legitimately contain only a sidebar pane — killing
		// them would kill the stashed renderer and trigger a respawn.
		if strings.HasPrefix(win.Name, "_tabby_stash_") {
			continue
		}

		paneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_id}|||#{pane_dead}|||#{pane_current_command}|||#{pane_start_command}").Output()
		if err != nil {
			continue
		}

		hasSidebar := false
		nonSystemLive := 0
		for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|||", 4)
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

// startOSCPipes attaches a pipe-pane osc-handler to every content pane that
// doesn't already have one. The handler reads pane output and fires
// doSetIndicator locally whenever a remote hook emits an OSC 7700 sequence —
// the fallback path used when tabby-hook runs inside an SSH session on a
// remote host that can't reach this daemon's socket directly.
//
// pipe-pane -o is a no-op if a pipe is already running on the pane, so this
// is safe to call on every refresh cycle.
func startOSCPipes(windows []tmux.Window) {
	exe := tabbyExe()
	if exe == "" {
		return
	}
	// Prefer the consolidated 'tabby' binary for hook subcommands. When the
	// daemon runs as a legacy 'tabby-daemon' binary, look for 'tabby' in the
	// same directory so the osc-handler subcommand is available.
	tabbyBin := exe
	if candidate := filepath.Join(filepath.Dir(exe), "tabby"); candidate != exe {
		if _, err := os.Stat(candidate); err == nil {
			tabbyBin = candidate
		}
	}
	cmd := fmt.Sprintf("'%s' hook osc-handler", tabbyBin)
	for _, win := range windows {
		for _, p := range win.Panes {
			if paneIsSystemPane(p.Command, p.StartCommand) {
				continue
			}
			exec.Command("tmux", "pipe-pane", "-o", "-t", p.ID, cmd).Run()
		}
	}
}

func paneIsSystemPane(cmd string, startCmd string) bool {
	return strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") ||
		strings.Contains(cmd, "tabby") || strings.Contains(cmd, "window-header") || strings.Contains(cmd, "pane-header") ||
		strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
		strings.Contains(startCmd, "tabby") || strings.Contains(startCmd, "window-header") || strings.Contains(startCmd, "pane-header")
}

var orphanWindowFirstSeen = map[string]time.Time{}

func cleanupOrphanWindowsByTmux(sessionID string, coordinator *Coordinator) {
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
	if coordinator != nil {
		status := coordinator.NewWindowStatus()
		if status.State == "inFlight" || status.State == "ready" {
			return
		}
	}

	out, err := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}|||#{window_name}").Output()
	if err != nil {
		return
	}
	now := time.Now()
	seen := make(map[string]bool)

	for _, rawLine := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		winParts := strings.SplitN(strings.TrimSpace(rawLine), "|||", 2)
		if len(winParts) < 1 {
			continue
		}
		windowID := winParts[0]
		if windowID == "" {
			continue
		}
		// Skip sidebar stash windows: they hold break-pane'd sidebars while
		// hidden on mobile and legitimately contain only a sidebar pane.
		if len(winParts) == 2 && strings.HasPrefix(winParts[1], sidebarStashWindowPrefix) {
			delete(orphanWindowFirstSeen, windowID)
			continue
		}
		seen[windowID] = true

		paneOut, paneErr := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
			"#{pane_dead}|||#{pane_current_command}|||#{pane_start_command}").Output()
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
			parts := strings.SplitN(line, "|||", 3)
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
			"#{pane_dead}|||#{pane_current_command}|||#{pane_start_command}").Output()
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
			parts := strings.SplitN(line, "|||", 3)
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

// spawnWindowHeaders spawns exactly ONE window-header pane per window, positioned
// at the top and spanning the window's full content width. The header tracks the
// window's active pane internally and renders a single 1-row (desktop) or 3-row
// (phone) strip shared across all content panes.
func spawnWindowHeaders(server *daemon.Server, sessionID string, customBorder bool, headerHeightRows int, windows []tmux.Window, coordinator *Coordinator) {
	// NOTE: No @tabby_spawning check here — the caller (doPaneLayoutOps) already
	// holds the lock. Checking it here would self-deadlock since the caller sets
	// @tabby_spawning=1 before calling us.
	headerBin := getWindowHeaderBin()
	if headerBin == "" {
		return
	}

	// Check if window headers are enabled (tmux option name unchanged for compat)
	out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	if err != nil || strings.TrimSpace(string(out)) != "on" {
		return
	}

	// Window-headers are a phone-only affordance: they host the hamburger + carousel
	// buttons that replace the sidebar interaction on narrow clients. On desktop,
	// kill any that are around and skip spawning new ones.
	if coordinator != nil && coordinator.ActiveClientProfile() != "phone" {
		if headerOut, err := exec.Command("tmux", "list-panes", "-a", "-F",
			"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(headerOut)), "\n") {
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, "|||", 3)
				if len(parts) < 3 {
					continue
				}
				if strings.Contains(parts[1], "window-header") || strings.Contains(parts[2], "window-header") {
					markSkipPreserveForWindow(parts[0])
					exec.Command("tmux", "kill-pane", "-t", parts[0]).Run()
					logEvent("WINDOW_HEADER_KILL_DESKTOP pane=%s", parts[0])
				}
			}
		}
		return
	}

	// Discover existing window-header panes keyed by window_id
	windowsWithHeader := make(map[string]bool)
	if headerOut, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{window_id}|||#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output(); err == nil {
		byWindow := make(map[string][]string)
		for _, line := range strings.Split(strings.TrimSpace(string(headerOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|||", 4)
			if len(parts) < 4 {
				continue
			}
			winID := parts[0]
			paneID := parts[1]
			curCmd := parts[2]
			startCmd := parts[3]
			if !strings.Contains(curCmd, "window-header") && !strings.Contains(startCmd, "window-header") {
				continue
			}
			windowsWithHeader[winID] = true
			byWindow[winID] = append(byWindow[winID], paneID)
		}
		// Deduplicate: kill any extra window-header panes in the same window
		for winID, panes := range byWindow {
			if len(panes) <= 1 {
				continue
			}
			sort.Strings(panes)
			for _, extraPane := range panes[1:] {
				logEvent("WINDOW_HEADER_DEDUP window=%s kill=%s", winID, extraPane)
				markSkipPreserveForWindow(extraPane)
				exec.Command("tmux", "kill-pane", "-t", extraPane).Run()
			}
		}
	}

	// For each window without a header, spawn one above the topmost content pane
	spawned := false
	spawnedInWindow := make(map[string]bool)
	for _, win := range windows {
		if win.ID == "" {
			continue
		}
		if windowsWithHeader[win.ID] {
			continue
		}
		// Find the topmost content pane (smallest pane_top, non-system)
		var topPane *tmux.Pane
		for i := range win.Panes {
			p := win.Panes[i]
			if paneIsSystemPane(p.Command, p.StartCommand) {
				continue
			}
			if topPane == nil || p.Top < topPane.Top {
				tp := p
				topPane = &tp
			}
		}
		if topPane == nil {
			continue
		}
		// Pane must be tall enough to split off a header row
		if topPane.Height < headerHeightRows+2 {
			continue
		}

		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		activeBeforeHeader := ""
		if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			activeBeforeHeader = strings.TrimSpace(string(out))
		}
		logEvent("SPAWN_WINDOW_HEADER window=%s target_pane=%s active_before=%s custom_border=%v header_rows=%d",
			win.ID, topPane.ID, activeBeforeHeader, customBorder, headerHeightRows)
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && %s -session '%s' -window '%s' %s",
			headerBin, sessionID, win.ID, debugFlag)
		// -v -f (without -b) = full-width split at bottom of window (footer).
		spawnCmd := exec.Command("tmux", "split-window", "-d", "-t", topPane.ID, "-v", "-f",
			"-l", fmt.Sprintf("%d", headerHeightRows), cmdStr)
		if out, err := spawnCmd.CombinedOutput(); err != nil {
			debugLog.Printf("Failed to spawn window-header for %s: %v, output: %s", win.ID, err, string(out))
			continue
		}
		spawned = true
		spawnedInWindow[win.ID] = true
	}

	// Disable pane borders on all newly spawned header panes
	if spawned {
		for winID := range spawnedInWindow {
			headerPaneOut, err := exec.Command("tmux", "list-panes", "-t", winID, "-F",
				"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
			if err == nil {
				for _, hLine := range strings.Split(string(headerPaneOut), "\n") {
					hLine = strings.TrimSpace(hLine)
					if strings.Contains(hLine, "window-header") {
						hParts := strings.SplitN(hLine, "|||", 3)
						if len(hParts) >= 1 {
							hTarget := "1"
							if globalCoordinator != nil {
								hTarget = fmt.Sprintf("%d", globalCoordinator.desiredWindowHeaderHeight())
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

// spawnPaneHeaders spawns a header pane above each content pane in all windows.
// Each content pane gets its own 1-row title strip. The phone button bar lives
// on window-header; pane-headers are always 1 row on both desktop and phone.
func spawnPaneHeaders(server *daemon.Server, sessionID string, customBorder bool, headerHeightRows int, windows []tmux.Window) {
	// NOTE: No @tabby_spawning check here — the caller (doPaneLayoutOps) already
	// holds the lock.
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
		"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output(); err == nil {
		headersByTarget := make(map[string][]string)
		for _, line := range strings.Split(strings.TrimSpace(string(headerOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|||", 3)
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

	type paneEntry struct {
		id       string
		windowID string
		height   int
		width    int
	}
	var contentPanes []paneEntry

	for _, win := range windows {
		for _, p := range win.Panes {
			isSystem := strings.Contains(p.Command, "sidebar") || strings.Contains(p.Command, "renderer") ||
				strings.Contains(p.Command, "pane-header") || strings.Contains(p.Command, "window-header") ||
				strings.Contains(p.Command, "tabby") ||
				strings.Contains(p.StartCommand, "sidebar") || strings.Contains(p.StartCommand, "renderer") ||
				strings.Contains(p.StartCommand, "pane-header") || strings.Contains(p.StartCommand, "window-header") ||
				strings.Contains(p.StartCommand, "tabby")
			if isSystem {
				if strings.Contains(p.Command, "pane-header") || strings.Contains(p.StartCommand, "pane-header") {
					target := paneTargetFromStartCmd(p.StartCommand)
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

	spawned := false
	spawnedInWindow := make(map[string]bool)
	for _, pane := range contentPanes {
		if panesWithHeader[pane.id] {
			continue
		}
		if pane.height < 3 {
			continue
		}
		debugFlag := ""
		if *debugMode {
			debugFlag = "-debug"
		}
		activeBeforeHeader := ""
		if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			activeBeforeHeader = strings.TrimSpace(string(out))
		}
		logEvent("SPAWN_HEADER pane=%s window=%s active_before=%s width=%d height=%d custom_border=%v header_rows=%d",
			pane.id, pane.windowID, activeBeforeHeader, pane.width, pane.height, customBorder, headerHeightRows)
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && %s -session '%s' -pane '%s' %s",
			headerBin, sessionID, pane.id, debugFlag)
		spawnCmd := exec.Command("tmux", "split-window", "-d", "-t", pane.id, "-v", "-b", "-l",
			fmt.Sprintf("%d", headerHeightRows), cmdStr)
		if out, err := spawnCmd.CombinedOutput(); err != nil {
			debugLog.Printf("Failed to spawn pane header for %s: %v, output: %s", pane.id, err, string(out))
			continue
		}
		spawned = true
		spawnedInWindow[pane.windowID] = true
	}

	if spawned {
		for winID := range spawnedInWindow {
			headerPaneOut, err := exec.Command("tmux", "list-panes", "-t", winID, "-F",
				"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
			if err == nil {
				for _, hLine := range strings.Split(string(headerPaneOut), "\n") {
					hLine = strings.TrimSpace(hLine)
					if strings.Contains(hLine, "pane-header") {
						hParts := strings.SplitN(hLine, "|||", 3)
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

// windowTargetRegex extracts the -window argument from a window-header start command
var windowTargetRegex = regexp.MustCompile(`-window\s+(?:'([^']+)'|"([^"]+)"|([^\s]+))`)

func windowTargetFromStartCmd(startCmd string) string {
	matches := windowTargetRegex.FindStringSubmatch(startCmd)
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

// cleanupOrphanedHeaders removes header panes (window-header and pane-header)
// that are disabled or orphaned (target window/pane no longer exists).
func cleanupOrphanedHeaders(customBorder bool, coordinator *Coordinator, activeWindowID string) {
	// Get all panes with geometry, start command, and window ID.
	// Scoped to our session with -t to avoid cross-session interference.
	listArgs := []string{"list-panes", "-s"}
	if *sessionID != "" {
		listArgs = append(listArgs, "-t", *sessionID)
	}
	listArgs = append(listArgs, "-F",
		"#{pane_id}|||#{pane_current_command}|||#{pane_width}|||#{pane_start_command}|||#{pane_height}|||#{pane_top}|||#{pane_left}|||#{window_id}")
	out, err := exec.Command("tmux", listArgs...).Output()
	if err != nil {
		return
	}

	enabledOut, _ := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	headersEnabled := strings.TrimSpace(string(enabledOut)) == "on"

	// Track which windows and content panes currently exist
	windowExists := make(map[string]bool)
	contentPaneExists := make(map[string]bool)

	type headerInfo struct {
		paneID         string
		windowID       string
		target         string
		height         int
		isWindowHeader bool
	}
	var windowHeaders []headerInfo
	var paneHeaders []headerInfo

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	// First pass: collect content panes and windows
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 8)
		if len(parts) < 8 {
			continue
		}
		winID := parts[7]
		if winID != "" {
			windowExists[winID] = true
		}
		curCmd := parts[1]
		startCmd := parts[3]
		isSystem := strings.Contains(curCmd, "window-header") || strings.Contains(curCmd, "pane-header") ||
			strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") || strings.Contains(curCmd, "tabby") ||
			strings.Contains(startCmd, "window-header") || strings.Contains(startCmd, "pane-header") ||
			strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") || strings.Contains(startCmd, "tabby")
		if !isSystem {
			contentPaneExists[parts[0]] = true
		}
	}

	// Second pass: collect header panes
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 8)
		if len(parts) < 8 {
			continue
		}
		curCmd := parts[1]
		startCmd := parts[3]
		h, _ := strconv.Atoi(parts[4])
		winID := parts[7]

		if strings.Contains(curCmd, "window-header") || strings.Contains(startCmd, "window-header") {
			target := windowTargetFromStartCmd(startCmd)
			if target == "" {
				target = winID
			}
			windowHeaders = append(windowHeaders, headerInfo{
				paneID: parts[0], windowID: winID, target: target, height: h, isWindowHeader: true,
			})
		} else if strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
			target := paneTargetFromStartCmd(startCmd)
			paneHeaders = append(paneHeaders, headerInfo{
				paneID: parts[0], windowID: winID, target: target, height: h, isWindowHeader: false,
			})
		}
	}

	killed := false

	// --- Window-header cleanup: one per window ---
	keepWindowHeader := make(map[string]bool)
	byWindow := make(map[string][]string)
	for _, hdr := range windowHeaders {
		byWindow[hdr.windowID] = append(byWindow[hdr.windowID], hdr.paneID)
	}
	for _, paneIDs := range byWindow {
		sort.Strings(paneIDs)
		if len(paneIDs) > 0 {
			keepWindowHeader[paneIDs[0]] = true
		}
	}
	for _, hdr := range windowHeaders {
		if !headersEnabled {
			logEvent("CLEANUP_WINDOW_HEADER pane=%s reason=disabled", hdr.paneID)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
		if !keepWindowHeader[hdr.paneID] {
			logEvent("CLEANUP_WINDOW_HEADER pane=%s window=%s reason=duplicate", hdr.paneID, hdr.windowID)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
		expectedHeight := coordinator.desiredWindowHeaderHeight()
		if hdr.height > expectedHeight {
			logEvent("WINDOW_HEADER_HEIGHT_ADJUST trigger=cleanup pane=%s height=%d expected=%d", hdr.paneID, hdr.height, expectedHeight)
			exec.Command("tmux", "resize-pane", "-t", hdr.paneID, "-y", fmt.Sprintf("%d", expectedHeight)).Run()
		}
		if hdr.target != "" && hdr.target != hdr.windowID && !windowExists[hdr.target] {
			logEvent("CLEANUP_WINDOW_HEADER pane=%s target=%s reason=target_window_gone", hdr.paneID, hdr.target)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
		}
	}

	// --- Pane-header cleanup: one per content pane ---
	keepPaneHeader := make(map[string]bool)
	byTarget := make(map[string][]string)
	for _, hdr := range paneHeaders {
		if hdr.target != "" {
			byTarget[hdr.target] = append(byTarget[hdr.target], hdr.paneID)
		}
	}
	for _, paneIDs := range byTarget {
		sort.Strings(paneIDs)
		if len(paneIDs) > 0 {
			keepPaneHeader[paneIDs[0]] = true
		}
	}
	for _, hdr := range paneHeaders {
		if !headersEnabled {
			logEvent("CLEANUP_PANE_HEADER pane=%s reason=disabled", hdr.paneID)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
		if hdr.target == "" || !keepPaneHeader[hdr.paneID] {
			logEvent("CLEANUP_PANE_HEADER pane=%s target=%s reason=duplicate_or_no_target", hdr.paneID, hdr.target)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
		// Kill if target content pane no longer exists
		if hdr.target != "" && !contentPaneExists[hdr.target] {
			logEvent("CLEANUP_PANE_HEADER pane=%s target=%s reason=target_pane_gone", hdr.paneID, hdr.target)
			markSkipPreserveForWindow(hdr.paneID)
			exec.Command("tmux", "kill-pane", "-t", hdr.paneID).Run()
			killed = true
			continue
		}
		expectedHeight := coordinator.desiredPaneHeaderHeight()
		if hdr.height > expectedHeight {
			logEvent("PANE_HEADER_HEIGHT_ADJUST trigger=cleanup pane=%s height=%d expected=%d", hdr.paneID, hdr.height, expectedHeight)
			exec.Command("tmux", "resize-pane", "-t", hdr.paneID, "-y", fmt.Sprintf("%d", expectedHeight)).Run()
		}
	}

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

// watchdogCheckRenderers verifies sidebar renderer processes are alive and respawns dead ones.
// It also detects layout corruption where a left sidebar has been flipped to a full-width
// top/bottom bar (e.g. after tmux source ~/.tmux.conf) and corrects it by killing and
// respawning the renderer in the correct horizontal split position.
func watchdogCheckRenderers(server *daemon.Server, sessionID string, coordinator *Coordinator) {
	if !isWatchdogEnabled() {
		return
	}

	rendererBin := getRendererBin()
	if rendererBin == "" {
		return
	}

	// Get all panes with PID and geometry info, scoped to our session.
	watchdogArgs := []string{"list-panes", "-s"}
	if sessionID != "" {
		watchdogArgs = append(watchdogArgs, "-t", sessionID)
	}
	watchdogArgs = append(watchdogArgs, "-F",
		"#{pane_id}|||#{pane_current_command}|||#{pane_pid}|||#{window_id}|||#{pane_dead}|||#{pane_width}|||#{window_width}|||#{pane_start_command}")
	out, err := exec.Command("tmux", watchdogArgs...).Output()
	if err != nil {
		return
	}

	sidebarHidden := coordinator.sidebarHidden
	globalWidth := coordinator.GetGlobalWidth()

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 8)
		if len(parts) < 7 {
			continue
		}
		paneID := parts[0]
		cmd := parts[1]
		pidStr := parts[2]
		windowID := parts[3]
		paneDead := parts[4]
		paneWidth, _ := strconv.Atoi(parts[5])
		windowWidth, _ := strconv.Atoi(parts[6])
		start := ""
		if len(parts) >= 8 {
			start = parts[7]
		}

		// Identify sidebar/header panes. Post-consolidation, pane_current_command
		// is just "tabby" — the sidebar/header identity lives in pane_start_command
		// (`exec -a sidebar-renderer ...` or `... render sidebar ...`). Check both so
		// LAYOUT_CORRUPT_SIDEBAR and ZOMBIE_PANE detection actually fires.
		combined := cmd + " " + start
		isSidebar := strings.Contains(combined, "sidebar-renderer") || strings.Contains(combined, "render sidebar")
		isHeader := strings.Contains(combined, "window-header") || strings.Contains(combined, "pane-header")
		if !isSidebar && !isHeader {
			continue
		}

		// Check if tmux considers the pane dead
		if paneDead == "1" {
			logEvent("DEAD_PANE pane=%s cmd=%s window=%s -- killing dead pane", paneID, cmd, windowID)
			markSkipPreserveForWindow(paneID)
			exec.Command("tmux", "kill-pane", "-t", paneID).Run()

			// Respawn sidebar renderer if it was a sidebar
			if isSidebar && !sidebarHidden {
				logEvent("RESPAWN_SIDEBAR window=%s after dead pane cleanup", windowID)
				debugFlag := ""
				if *debugMode {
					debugFlag = "-debug"
				}
				cmdStr := fmt.Sprintf("%s -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
				exec.Command("tmux", "split-window", "-d", "-t", windowID, "-h", "-b", "-l", fmt.Sprintf("%d", globalWidth), cmdStr).Run()
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

			if isSidebar && !sidebarHidden {
				logEvent("RESPAWN_SIDEBAR window=%s after zombie pane cleanup", windowID)
				debugFlag := ""
				if *debugMode {
					debugFlag = "-debug"
				}
				cmdStr := fmt.Sprintf("%s -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
				exec.Command("tmux", "split-window", "-d", "-t", windowID, "-h", "-b", "-l", fmt.Sprintf("%d", globalWidth), cmdStr).Run()
			}
			continue
		}

		// Layout corruption check: a sidebar-renderer should never be full-width.
		// If pane_width >= window_width-2 the split direction has been flipped (e.g.
		// by tmux source ~/.tmux.conf recalculating layouts). Kill and respawn so the
		// next watchdog / spawnRenderersForNewWindows cycle restores the left sidebar.
		if isSidebar && !sidebarHidden && windowWidth > 0 && paneWidth >= windowWidth-2 {
			logEvent("LAYOUT_CORRUPT_SIDEBAR pane=%s window=%s pane_w=%d win_w=%d -- killing flipped sidebar",
				paneID, windowID, paneWidth, windowWidth)
			markSkipPreserveForWindow(paneID)
			exec.Command("tmux", "kill-pane", "-t", paneID).Run()
			debugFlag := ""
			if *debugMode {
				debugFlag = "-debug"
			}
			cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && %s -session '%s' -window '%s' %s", rendererBin, sessionID, windowID, debugFlag)
			exec.Command("tmux", "split-window", "-d", "-t", windowID, "-h", "-b", "-f", "-l", fmt.Sprintf("%d", globalWidth), cmdStr).Run()
		}
	}
}

// panelAuditApplyFixes controls whether panelAudit applies fixes or only logs.
// Flip to false to run in detect-only mode if the auditor starts misfiring.
const panelAuditApplyFixes = true

// panelAudit checks utility-panel state for cross-window inconsistencies and
// drift between coordinator memory and live tmux state. Fires from the same
// 5s watchdog tick as watchdogCheckRenderers but covers a different concern:
// watchdogCheckRenderers handles dead processes; panelAudit handles structural
// drift (sidebar widths diverging across windows, missing/duplicate headers,
// hidden-state divergence). Each detected issue logs an AUDIT_* event whether
// or not it is auto-fixed.
//
// Source-of-truth policy:
//   - sidebarHidden vs. physical stash window  -> physical (tmux) wins
//   - sidebar width vs. coordinator.globalWidth -> coordinator wins, fix via RunWidthSync
//   - duplicate header panes                    -> lowest pane_id wins, kill the rest
//   - missing pane-header on a content pane     -> trigger OnRefreshLayout, do not spawn directly
func panelAudit(sessionID string, coordinator *Coordinator) {
	if !isWatchdogEnabled() {
		return
	}

	// Skip during legitimate state transitions to avoid false positives.
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		return
	}
	if status := coordinator.NewWindowStatus(); status.State == "inFlight" {
		return
	}

	type paneInfo struct {
		paneID, windowID, cmd, startCmd string
		width, height, top              int
	}
	type windowPanels struct {
		windowHeight  int
		sidebars      []paneInfo
		windowHeaders []paneInfo
		paneHeaders   []paneInfo
		contentPanes  []paneInfo
	}

	// Single snapshot of all panes for this pass — every check reads from it.
	snapArgs := []string{"list-panes", "-s"}
	if sessionID != "" {
		snapArgs = append(snapArgs, "-t", sessionID)
	}
	snapArgs = append(snapArgs, "-F",
		"#{pane_id}|||#{window_id}|||#{pane_current_command}|||#{pane_start_command}|||#{pane_width}|||#{pane_top}|||#{pane_height}|||#{window_height}")
	out, err := exec.Command("tmux", snapArgs...).Output()
	if err != nil {
		logEvent("AUDIT_SNAPSHOT_ERR err=%v", err)
		return
	}

	byWindow := map[string]*windowPanels{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 8)
		if len(parts) < 8 {
			continue
		}
		w, _ := strconv.Atoi(parts[4])
		t, _ := strconv.Atoi(parts[5])
		h, _ := strconv.Atoi(parts[6])
		wh, _ := strconv.Atoi(parts[7])
		p := paneInfo{paneID: parts[0], windowID: parts[1], cmd: parts[2], startCmd: parts[3], width: w, height: h, top: t}
		win := byWindow[p.windowID]
		if win == nil {
			win = &windowPanels{}
			byWindow[p.windowID] = win
		}
		if wh > win.windowHeight {
			win.windowHeight = wh
		}
		combined := p.cmd + " " + p.startCmd
		switch {
		case strings.Contains(combined, "sidebar-renderer") || strings.Contains(combined, "render sidebar"):
			win.sidebars = append(win.sidebars, p)
		case strings.Contains(combined, "window-header"):
			win.windowHeaders = append(win.windowHeaders, p)
		case strings.Contains(combined, "pane-header"):
			win.paneHeaders = append(win.paneHeaders, p)
		default:
			if !paneIsSystemPane(p.cmd, p.startCmd) {
				win.contentPanes = append(win.contentPanes, p)
			}
		}
	}

	profile := coordinator.ActiveClientProfile()
	activeWindowID := ""
	for _, w := range coordinator.GetWindows() {
		if w.Active {
			activeWindowID = w.ID
			break
		}
	}

	// Track if any check requested a layout refresh so we only call once.
	needLayoutRefresh := false

	// --- Check 1: sidebarHidden flag vs. physical stash state -----------------
	{
		memory := coordinator.sidebarHidden
		physical := sidebarIsStashed()
		if memory != physical {
			if panelAuditApplyFixes && profile == "desktop" {
				logEvent("AUDIT_HIDDEN_DRIFT memory=%v physical=%v profile=%s action=fix", memory, physical, profile)
				coordinator.sidebarHidden = physical
			} else {
				logEvent("AUDIT_HIDDEN_DRIFT memory=%v physical=%v profile=%s action=defer", memory, physical, profile)
			}
		}
	}

	// --- Check 2: sidebar presence + height -----------------------------------
	// Every window should have exactly one sidebar pane spanning roughly the
	// full window height (minus header rows). Catches:
	//   - missing sidebar (window has none at all — width-consistency below
	//     would silently skip this window)
	//   - squashed sidebar (pane exists but height collapsed to a few rows;
	//     pane_dead=0 so watchdogCheckRenderers can't see it either)
	// Fix: kill the bad pane; next watchdogCheckRenderers tick respawns it
	// at the correct geometry via the existing dead-pane respawn path.
	if !coordinator.sidebarHidden && profile != "phone" {
		for winID, win := range byWindow {
			if win.windowHeight <= 0 {
				continue
			}
			// Allow up to 4 rows for window-header (1-3 row strip) + tmux border.
			minExpectedHeight := win.windowHeight - 4
			if minExpectedHeight < 5 {
				minExpectedHeight = 5
			}
			switch {
			case len(win.sidebars) == 0:
				logEvent("AUDIT_SIDEBAR_MISSING window=%s window_height=%d action=%s",
					winID, win.windowHeight, fixOrLog("await_watchdog_respawn"))
				// Nothing to kill; watchdogCheckRenderers won't see a missing pane,
				// but spawnRenderersForNewWindows runs on windowCheckTicker (3s) and
				// will detect the empty window and spawn the sidebar.
			default:
				for _, sb := range win.sidebars {
					if sb.height < minExpectedHeight {
						logEvent("AUDIT_SIDEBAR_SQUASHED window=%s pane=%s height=%d window_height=%d action=%s",
							winID, sb.paneID, sb.height, win.windowHeight, fixOrLog("kill_for_respawn"))
						if panelAuditApplyFixes {
							markSkipPreserveForWindow(sb.paneID)
							exec.Command("tmux", "kill-pane", "-t", sb.paneID).Run()
						}
					}
				}
			}
		}
	}

	// --- Check 3: sidebar width consistency across windows --------------------
	// Skipped on phone (keyboard-clamp creates legitimate variance) and when
	// sidebar is hidden (no sidebars to compare).
	if !coordinator.sidebarHidden && profile != "phone" {
		globalWidth := coordinator.GetGlobalWidth()
		minW, maxW := 0, 0
		var widthList []string
		first := true
		drift := false
		for winID, win := range byWindow {
			if len(win.sidebars) == 0 {
				continue
			}
			// Use the lowest-id sidebar's width (dedup of duplicate sidebars
			// is watchdogCheckRenderers' job, not ours).
			sb := win.sidebars[0]
			for _, s := range win.sidebars[1:] {
				if s.paneID < sb.paneID {
					sb = s
				}
			}
			widthList = append(widthList, fmt.Sprintf("%s=%d", winID, sb.width))
			if first {
				minW, maxW = sb.width, sb.width
				first = false
			} else {
				if sb.width < minW {
					minW = sb.width
				}
				if sb.width > maxW {
					maxW = sb.width
				}
			}
			// Allow ±1 col tmux rounding slop.
			if globalWidth > 0 && (sb.width < globalWidth-1 || sb.width > globalWidth+1) {
				drift = true
			}
		}
		if !first && (maxW-minW > 1) {
			drift = true
		}
		if drift {
			sort.Strings(widthList)
			action := "log"
			if panelAuditApplyFixes && activeWindowID != "" {
				action = "run_width_sync"
			}
			logEvent("AUDIT_WIDTH_DRIFT windows=%d min=%d max=%d global=%d list=%s action=%s",
				len(widthList), minW, maxW, coordinator.GetGlobalWidth(), strings.Join(widthList, ","), action)
			if panelAuditApplyFixes && activeWindowID != "" {
				coordinator.RunWidthSync(activeWindowID, true)
			}
		}
	}

	// --- Check 4: window-header count per window ------------------------------
	// Phone profile expects exactly one per window when @tabby_pane_headers=on.
	// Desktop profile expects zero (any leak should be killed).
	headersOpt, _ := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output()
	headersEnabled := strings.TrimSpace(string(headersOpt)) == "on"

	for winID, win := range byWindow {
		if profile == "desktop" {
			if len(win.windowHeaders) > 0 {
				logEvent("AUDIT_WINDOW_HEADER window=%s count=%d profile=desktop expected=0 action=%s",
					winID, len(win.windowHeaders), fixOrLog("kill_desktop"))
				if panelAuditApplyFixes {
					for _, p := range win.windowHeaders {
						markSkipPreserveForWindow(p.paneID)
						exec.Command("tmux", "kill-pane", "-t", p.paneID).Run()
					}
				}
			}
			continue
		}
		// phone profile
		if !headersEnabled {
			continue
		}
		switch {
		case len(win.windowHeaders) == 0:
			logEvent("AUDIT_WINDOW_HEADER window=%s count=0 profile=phone expected=1 action=%s",
				winID, fixOrLog("request_refresh"))
			needLayoutRefresh = true
		case len(win.windowHeaders) > 1:
			// Keep lowest pane_id, kill the rest (mirror WINDOW_HEADER_DEDUP).
			sorted := append([]paneInfo(nil), win.windowHeaders...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].paneID < sorted[j].paneID })
			logEvent("AUDIT_WINDOW_HEADER window=%s count=%d profile=phone expected=1 action=%s",
				winID, len(sorted), fixOrLog("dedup"))
			if panelAuditApplyFixes {
				for _, p := range sorted[1:] {
					markSkipPreserveForWindow(p.paneID)
					exec.Command("tmux", "kill-pane", "-t", p.paneID).Run()
				}
			}
		}
	}

	// --- Check 5: pane-header count per content pane --------------------------
	// When @tabby_pane_headers=on, expect exactly one pane-header targeting
	// each content pane. Detect missing, duplicate, and orphan (target gone).
	if headersEnabled {
		for winID, win := range byWindow {
			contentByID := map[string]bool{}
			for _, c := range win.contentPanes {
				contentByID[c.paneID] = true
			}
			headersByTarget := map[string][]paneInfo{}
			for _, h := range win.paneHeaders {
				target := paneTargetFromStartCmd(h.startCmd)
				headersByTarget[target] = append(headersByTarget[target], h)
			}
			// Orphans + duplicates
			for target, hdrs := range headersByTarget {
				if target == "" || !contentByID[target] {
					logEvent("AUDIT_PANE_HEADER window=%s target=%s count=%d action=%s",
						winID, target, len(hdrs), fixOrLog("kill_orphan"))
					if panelAuditApplyFixes {
						for _, h := range hdrs {
							markSkipPreserveForWindow(h.paneID)
							exec.Command("tmux", "kill-pane", "-t", h.paneID).Run()
						}
					}
					continue
				}
				if len(hdrs) > 1 {
					sort.Slice(hdrs, func(i, j int) bool { return hdrs[i].paneID < hdrs[j].paneID })
					logEvent("AUDIT_PANE_HEADER window=%s target=%s count=%d action=%s",
						winID, target, len(hdrs), fixOrLog("dedup"))
					if panelAuditApplyFixes {
						for _, h := range hdrs[1:] {
							markSkipPreserveForWindow(h.paneID)
							exec.Command("tmux", "kill-pane", "-t", h.paneID).Run()
						}
					}
				}
			}
			// Missing
			for _, c := range win.contentPanes {
				if _, ok := headersByTarget[c.paneID]; !ok {
					logEvent("AUDIT_PANE_HEADER window=%s target=%s count=0 action=%s",
						winID, c.paneID, fixOrLog("request_refresh"))
					needLayoutRefresh = true
				}
			}
		}
	}

	if needLayoutRefresh && panelAuditApplyFixes && coordinator.OnRefreshLayout != nil {
		coordinator.OnRefreshLayout()
	}
}

// fixOrLog returns the fix action name when fixes are enabled, otherwise
// "detect_only" — keeps log events readable in either mode.
func fixOrLog(fixAction string) string {
	if panelAuditApplyFixes {
		return fixAction
	}
	return "detect_only"
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
		"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
	if err != nil {
		return
	}

	// Removed: select-pane -P calls were stealing focus
	// Sidebar and window-header content is rendered with proper ANSI colors via lipgloss
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
func restoreFocusState(coordinator *Coordinator) {
	if coordinator != nil {
		status := coordinator.NewWindowStatus()
		if status.State == "inFlight" || status.State == "ready" {
			return
		}
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
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
		strings.Contains(cmd, "window-header") ||
		strings.Contains(cmd, "pane-header")
}

// syncClientSizesFromTmux updates all client widths/heights from actual tmux pane sizes.
// Returns true when any tracked sidebar size changed.
func syncClientSizesFromTmux(server *daemon.Server, coordinator *Coordinator, trigger string) bool {
	start := time.Now()

	// Get all panes with their sizes and commands
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{window_id}|||#{pane_id}|||#{pane_width}|||#{pane_height}|||#{pane_current_command}|||#{pane_start_command}").Output()
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
		parts := strings.SplitN(line, "|||", 6)
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

		if strings.Contains(cmd, "window-header") || strings.Contains(startCmd, "window-header") {
			// For window-header panes, the clientID is "window-header:<windowID>"
			headerSizes["window-header:"+windowID] = paneSize{width: width, height: height, paneID: paneID}
			continue
		}

		if strings.Contains(cmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
			// For pane-header panes, extract the target content pane ID from start command.
			// clientID is "header:<paneID>".
			targetPane := paneTargetFromStartCmd(startCmd)
			if targetPane != "" {
				headerSizes["header:"+targetPane] = paneSize{width: width, height: height, paneID: paneID}
			}
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
		info := server.GetClientInfo(windowID)
		if info == nil {
			continue
		}
		if info.Width != size.width || info.Height != size.height {
			sizesChanged = true
			changeCount++
			logEvent("GEOM_SYNC_SIDEBAR trigger=%s client=%s prev=%dx%d new=%dx%d delta_w=%d delta_h=%d",
				trigger, windowID, info.Width, info.Height, size.width, size.height, size.width-info.Width, size.height-info.Height)
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
		// Enforce correct height per header type
		if daemon.KindOf(clientID) == daemon.TargetWindowHeader {
			desiredH := coordinator.desiredWindowHeaderHeight()
			if size.height > desiredH {
				logEvent("HEADER_HEIGHT_ANOMALY trigger=%s client=%s height=%d desired=%d", trigger, clientID, size.height, desiredH)
				exec.Command("tmux", "resize-pane", "-t", size.paneID, "-y", fmt.Sprintf("%d", desiredH)).Run()
			}
		} else if daemon.KindOf(clientID) == daemon.TargetPaneHeader {
			desiredH := coordinator.desiredPaneHeaderHeight()
			if size.height > desiredH {
				logEvent("HEADER_HEIGHT_ANOMALY trigger=%s client=%s height=%d desired=%d", trigger, clientID, size.height, desiredH)
				exec.Command("tmux", "resize-pane", "-t", size.paneID, "-y", fmt.Sprintf("%d", desiredH)).Run()
			}
		}
		server.UpdateClientSize(clientID, size.width, size.height)
	}

	if changeCount > 0 {
		logEvent("GEOM_SYNC_APPLY trigger=%s changed=%d duration_ms=%d", trigger, changeCount, time.Since(start).Milliseconds())
	}

	return sizesChanged
}

// activeClientElector is the single source of truth for "which physical tmux
// client is active right now". Constructed in main(), shared across all
// callers that previously reached into package globals for preferred/sticky
// tty state.
var activeClientElector *daemon.ClientElector

func sourceWindowIDFromClientID(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if daemon.KindOf(clientID) == daemon.TargetWindowHeader {
		return strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
	}
	if strings.HasPrefix(clientID, "@") {
		return clientID
	}
	return ""
}

func clientTTYForPane(paneID string) string {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{client_tty}").Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(out))
	if strings.HasPrefix(tty, "#{") {
		return ""
	}
	return tty
}

// activeClientGeometry is a thin compat shim around the shared elector —
// retained so the many callers in this file and coordinator.go don't all
// need to be rewritten in one go. Prefer activeClientElector.Elect() at
// new call sites.
func activeClientGeometry() (width int, height int, tty string, activity int64, ok bool) {
	if activeClientElector == nil {
		return 0, 0, "", 0, false
	}
	res := activeClientElector.Elect()
	if !res.OK {
		return 0, 0, "", 0, false
	}
	return res.Client.Width, res.Client.Height, res.Client.TTY, res.Activity, true
}

// setPreferredClientTTY and latestAttachedClientTTY are compat shims for
// the same reason.
func setPreferredClientTTY(tty, reason string) {
	if activeClientElector != nil {
		activeClientElector.Pin(tty, reason)
	}
}

func latestAttachedClientTTY() string {
	if activeClientElector == nil {
		return ""
	}
	return activeClientElector.LatestAttachedTTY()
}

// resizeAllWindowsToClient authoritatively locks every tmux window to the
// elected active client's dimensions, overriding tmux's `window-size latest`
// auto-reflow. This is the single source of truth for window geometry —
// inactive clients (e.g. an idle phone) must not be able to shrink windows.
func resizeAllWindowsToClient(width, height int, reason string) {
	if width <= 0 || height <= 0 {
		return
	}
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Output()
	if err != nil {
		logEvent("RESIZE_ALL_WINDOWS_ERR reason=%s err=%v", reason, err)
		return
	}
	count := 0
	for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		wid = strings.TrimSpace(wid)
		if wid == "" {
			continue
		}
		if err := exec.Command("tmux", "resize-window", "-x", fmt.Sprintf("%d", width), "-y", fmt.Sprintf("%d", height), "-t", wid).Run(); err == nil {
			count++
		}
	}
	logEvent("RESIZE_ALL_WINDOWS reason=%s size=%dx%d count=%d", reason, width, height, count)
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

func Run(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionID = fs.String("session", "", "tmux session ID")
	debugMode = fs.Bool("debug", false, "Enable debug logging")
	if err := fs.Parse(args); err != nil {
		return 2
	}

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

	// Build the shared active-client elector before the server, so every
	// downstream component (server callbacks, coordinator, hook handlers)
	// reads from the same elected tty.
	activeClientElector = daemon.NewClientElector(logEvent, 0)

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
		// Route window-header clients (window-header:@<id>) to the window header renderer
		if daemon.KindOf(clientID) == daemon.TargetWindowHeader {
			return coordinator.RenderHeaderForClient(clientID, width, height)
		}
		// Route pane-header clients (header:%<paneID>) to the per-pane title strip renderer
		if daemon.KindOf(clientID) == daemon.TargetPaneHeader {
			return coordinator.RenderPaneHeaderForClient(clientID, width, height)
		}
		renderClientID := clientID
		if idx := strings.Index(clientID, "#web-"); idx > 0 {
			renderClientID = clientID[:idx]
		}
		return coordinator.RenderForClient(renderClientID, width, height)
	}

	refreshCh := make(chan struct{}, 10)

	// Build the event loop. Step 1 of the daemon refactor (see
	// /Users/b/.claude/plans/nifty-jingling-tulip.md): the loop owns
	// coordinator mutations driven by renderer input, so we no longer
	// have one server-worker goroutine per connection mutating state in
	// parallel with the main select loop. Tickers, signals, and tmux
	// hooks remain on their existing goroutines for now (Steps 2-4).
	loop := NewLoop(coordinator, server, activeClientElector, refreshCh)
	loopCtx, loopCancel := context.WithCancel(context.Background())
	defer loopCancel()
	go loop.Run(loopCtx)

	// Ignore SIGPIPE — daemon runs backgrounded and stdout/stderr may become broken pipes
	signal.Ignore(syscall.SIGPIPE)

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	var selfTerminating atomic.Bool

	// Set up input callback with panic recovery.
	// The server validates msg.Target before calling OnInput, so clientID
	// is always non-empty and corresponds to a known renderer kind.
	// The body of the handler now runs on the loop goroutine via
	// Loop.handleRendererInput; this shim only forwards the event.
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		logEvent("INPUT_START client=%s type=%s btn=%s action=%s resolved=%s x=%d y=%d target=%s pane=%s sourcePane=%s", clientID, input.Type, input.Button, input.Action, input.ResolvedAction, input.MouseX, input.MouseY, input.ResolvedTarget, input.PaneID, input.SourcePaneID)
		loop.Submit(RendererInputEvent{ClientID: clientID, Input: input})
	}

	// Set up connect/disconnect callbacks
	server.OnConnect = func(clientID string, paneID string) {
		logEvent("CLIENT_CONNECT client=%s pane=%s", clientID, paneID)
		if tty := latestAttachedClientTTY(); tty != "" {
			setPreferredClientTTY(tty, fmt.Sprintf("connect:%s", clientID))
		}
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
			if daemon.KindOf(id) != daemon.TargetWindowHeader {
				server.UpdateClientWidth(id, newWidth)
			}
		}
	}
	coordinator.OnRefreshClient = func(clientID string) {
		server.SendRenderToClient(clientID)
	}
	coordinator.OnRefreshLayout = func() {
		// Non-blocking poke: the main loop will pick this up and run
		// doPaneLayoutOps next tick. Used on profile transitions so the
		// window-header topology follows the active client's form factor.
		select {
		case refreshCh <- struct{}{}:
		default:
		}
	}

	// Register SIGUSR1/SIGUSR2 handlers BEFORE server.Start() creates the
	// socket. ensure_sidebar.sh sends USR1 the moment it detects the socket,
	// so the handler must be in place before the socket appears or the signal
	// arrives before Notify and kills the process (Go uses the OS default —
	// terminate).
	//
	// Step 3 of the daemon refactor (see
	// /Users/b/.claude/plans/nifty-jingling-tulip.md): both former
	// signal-handler goroutine bodies now live on Loop (handleRefreshSignal /
	// handleClientResized). This goroutine is just a producer that drops
	// duplicate signals at submitCoalesced via flags.usr1 / flags.usr2.
	// SIGUSR2 now dedups against lastResizeKey (shared with the 250ms
	// clientGeometryTicker) — that is the deliberate behavioral fix.
	sigUSRCh := make(chan os.Signal, 16)
	signal.Notify(sigUSRCh, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for sig := range sigUSRCh {
			switch sig {
			case syscall.SIGUSR1:
				loop.submitCoalesced(&loop.flags.usr1, SignalEvent{Sig: syscall.SIGUSR1})
			case syscall.SIGUSR2:
				loop.submitCoalesced(&loop.flags.usr2, SignalEvent{Sig: syscall.SIGUSR2})
			}
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
			restoreFocusState(coordinator)
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

		// Apply auto-theme immediately on startup (don't wait for first tick).
		if want := coordinator.ResolveAutoTheme(); want != "" && want != coordinator.ActiveThemeName() {
			coordinator.SetTheme(want)
		}

		// Step 2 of the daemon refactor: tickers run via loop.go now.
		// activeWindowID and lastWindowsHash are still mutated by this
		// goroutine's refreshCh / updateActiveWindow path, so they remain
		// locals here; we mirror them onto the Loop after each write so
		// tick handlers (which run on the loop goroutine) can observe the
		// latest values without racing. Step 3 moves the refresh handler
		// onto the loop and these locals collapse.
		loop.SetLastAutoTheme(coordinator.ActiveThemeName())

		lastWindowsHash := ""        // mirrored to loop via loop.SetLastWindowsHash
		activeWindowID := ""         // mirrored to loop via loop.SetActiveWindowID

		lastWindowCount := 0 // Track window count for close detection

		newWindowReadyHold := 900 * time.Millisecond
		newWindowReadyTimeout := 3 * time.Second
		postReadyStabilize := 2500 * time.Millisecond
		lastReadyWindowID := ""
		var lastReadyClearedAt time.Time

		coordinatorActiveWindowID := func() string {
			for _, w := range coordinator.GetWindows() {
				if w.Active {
					return w.ID
				}
			}
			return ""
		}

		updateActiveWindow := func() {
			status := coordinator.NewWindowStatus()
			coordActive := coordinatorActiveWindowID()
			logEvent("READY_STATE_TRACE phase=update_active_start state=%s ready=%s age_ms=%d daemon_active=%s coordinator_active=%s", status.State, status.WindowID, time.Since(status.Created).Milliseconds(), activeWindowID, coordActive)
			if status.State == "inFlight" {
				logEvent("UPDATE_ACTIVE_WINDOW_WAIT reason=new_window_inflight daemon_active=%s coordinator_active=%s", activeWindowID, coordActive)
				return
			}
			if status.State == "ready" {
				if status.WindowID != "" {
					lastReadyWindowID = status.WindowID
				}
				ageMs := time.Since(status.Created).Milliseconds()
				if time.Since(status.Created) > newWindowReadyTimeout {
					logEvent("NEW_WINDOW_READY_TIMEOUT_CLEAR window=%s age_ms=%d", status.WindowID, ageMs)
					coordinator.ClearNewWindowStatus()
					if status.WindowID != "" {
						lastReadyWindowID = status.WindowID
					}
					lastReadyClearedAt = time.Now()
				} else {
					hasWindow := false
					for _, w := range coordinator.GetWindows() {
						if w.ID == status.WindowID {
							hasWindow = true
							break
						}
					}
					if hasWindow && status.WindowID != "" && activeWindowID != status.WindowID {
						logEvent("WINDOW_STATE_DRIFT source=new_window_ready tmux_active=unknown daemon_active=%s coordinator_active=%s ready_window=%s", activeWindowID, coordActive, status.WindowID)
					}
					logEvent("READY_STATE_TRACE phase=update_active_ready_observe state=%s ready=%s age_ms=%d daemon_active=%s coordinator_active=%s hasWindow=%v", status.State, status.WindowID, ageMs, activeWindowID, coordActive, hasWindow)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			args := []string{"display-message"}
			if _, _, tty, _, ok := activeClientGeometry(); ok && strings.TrimSpace(tty) != "" {
				args = append(args, "-c", strings.TrimSpace(tty))
			}
			args = append(args, "-p", "#{window_id}")
			if out, err := exec.CommandContext(ctx, "tmux", args...).Output(); err == nil {
				newID := strings.TrimSpace(string(out))
				if newID != "" {
					logEvent("UPDATE_ACTIVE_WINDOW_TMUX_QUERY daemon_old=%s tmux_new=%s coordinator_active=%s", activeWindowID, newID, coordActive)
				}
				logEvent("READY_STATE_TRACE phase=update_active_tmux_query state=%s ready=%s daemon_active=%s tmux_active=%s coordinator_active=%s", status.State, status.WindowID, activeWindowID, newID, coordActive)
				if newID != "" {
					if newID != activeWindowID || newID != coordActive {
						logEvent("WINDOW_STATE_DRIFT source=tmux_query tmux_active=%s daemon_active=%s coordinator_active=%s", newID, activeWindowID, coordActive)
					}
					if newID != activeWindowID {
						if !lastReadyClearedAt.IsZero() && lastReadyWindowID != "" {
							sinceClear := time.Since(lastReadyClearedAt)
							if sinceClear <= postReadyStabilize && activeWindowID == lastReadyWindowID && newID != lastReadyWindowID {
								logEvent("UPDATE_ACTIVE_WINDOW_TMUX_SUPPRESS old=%s new=%s last_ready=%s since_clear_ms=%d", activeWindowID, newID, lastReadyWindowID, sinceClear.Milliseconds())
								return
							}
						}
						navAt, navWindow, settleUntil, settledWindow := loop.NavSettleState()
						if !settleUntil.IsZero() && time.Now().Before(settleUntil) && settledWindow != "" {
							if newID != settledWindow {
								logEvent("UPDATE_ACTIVE_WINDOW_TMUX_SUPPRESS_NAV old=%s new=%s settled=%s remaining_ms=%d marked_window=%s", activeWindowID, newID, settledWindow, time.Until(settleUntil).Milliseconds(), navWindow)
								return
							}
							logEvent("UPDATE_ACTIVE_WINDOW_TMUX_NAV_CONFIRMED old=%s new=%s settled=%s age_ms=%d", activeWindowID, newID, settledWindow, time.Since(navAt).Milliseconds())
						}
						logEvent("UPDATE_ACTIVE_WINDOW_TMUX_OBSERVE old=%s new=%s coordinator_active=%s", activeWindowID, newID, coordActive)
					}
					activeWindowID = newID
					loop.SetActiveWindowID(newID)
				}
			} else {
				logEvent("UPDATE_ACTIVE_WINDOW_TMUX_ERR err=%v", err)
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
		// Must not run inside doPaneLayoutOps: spawnWindowHeaders sets @tabby_spawning=1
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
			status := coordinator.NewWindowStatus()
			logEvent("READY_STATE_TRACE phase=pane_layout_start state=%s ready=%s age_ms=%d active=%s", status.State, status.WindowID, time.Since(status.Created).Milliseconds(), activeWindowID)
			if status.State == "inFlight" {
				logEvent("PANE_LAYOUT_SKIP reason=new_window_inflight")
				return
			}
			if status.State == "ready" {
				age := time.Since(status.Created)
				if age > newWindowReadyTimeout {
					logEvent("PANE_LAYOUT_READY_TIMEOUT_CLEAR window=%s age_ms=%d", status.WindowID, age.Milliseconds())
					coordinator.ClearNewWindowStatus()
					status = coordinator.NewWindowStatus()
				} else if age > newWindowReadyHold {
					logEvent("PANE_LAYOUT_SKIP reason=new_window_ready window=%s age_ms=%d", status.WindowID, age.Milliseconds())
					return
				}
			}
			if now.Sub(lastPaneLayoutOps) < paneLayoutCooldown {
				logEvent("PANE_LAYOUT_SKIP cooldown_remaining=%dms", (paneLayoutCooldown - now.Sub(lastPaneLayoutOps)).Milliseconds())
				return
			}
			lastPaneLayoutOps = now
			logEvent("PANE_LAYOUT_START activeProfile=%s sidebarHidden=%v newWindowState=%s",
				coordinator.ActiveClientProfile(), coordinator.sidebarHidden,
				status.State)
			customBorder := coordinator.GetConfig().PaneHeader.CustomBorder
			exec.Command("tmux", "set-option", "-g", "@tabby_spawning", "1").Run()
			windows := coordinator.GetWindows()
			spawnWindowHeaders(server, *sessionID, customBorder, coordinator.desiredWindowHeaderHeight(), windows, coordinator)
			spawnPaneHeaders(server, *sessionID, customBorder, coordinator.desiredPaneHeaderHeight(), windows)
			exec.Command("tmux", "set-option", "-g", "@tabby_spawning", "0").Run()
			startOSCPipes(windows)
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

		// Wire ticker dependencies onto the loop and start ticker goroutines
		// (Step 2 of the daemon refactor). The migrated handler bodies live
		// on Loop in loop.go; runTicker drives the submitCoalesced producer
		// for each cadence. SetTickDeps must precede any tick submission.
		loop.SetTickDeps(LoopTickDeps{
			RunLoopTask:         runLoopTask,
			RunLoopTaskNonFatal: runLoopTaskNonFatal,
			UpdateActiveWindow:  updateActiveWindow,
			SessionID:           *sessionID,
			MyPid:               os.Getpid(),
			SocketPath:          server.GetSocketPath(),
			SigCh:               sigCh,
		})
		go runTicker(loopCtx, 250*time.Millisecond, func() { loop.submitCoalesced(&loop.flags.geom, ClientGeomTickEvent{}) })
		go runTicker(loopCtx, 100*time.Millisecond, func() { loop.submitCoalesced(&loop.flags.anim, AnimationTickEvent{}) })
		go runTicker(loopCtx, 3*time.Second, func() { loop.submitCoalesced(&loop.flags.window, WindowCheckTickEvent{}) })
		go runTicker(loopCtx, 30*time.Second, func() { loop.submitCoalesced(&loop.flags.refresh, RefreshTickEvent{}) })
		go runTicker(loopCtx, 5*time.Second, func() { loop.submitCoalesced(&loop.flags.git, GitTickEvent{}) })
		go runTicker(loopCtx, 5*time.Second, func() { loop.submitCoalesced(&loop.flags.watchdog, WatchdogTickEvent{}) })
		go runTicker(loopCtx, 60*time.Second, func() { loop.submitCoalesced(&loop.flags.autoTheme, AutoThemeTickEvent{}) })
		go runTicker(loopCtx, 3*time.Second, func() { loop.submitCoalesced(&loop.flags.socket, SocketCheckTickEvent{}) })
		go runTicker(loopCtx, 10*time.Second, func() { loop.submitCoalesced(&loop.flags.idle, IdleTickEvent{}) })

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

					windowsAfterRefresh := coordinator.GetWindows()
					currentWindowCount := len(windowsAfterRefresh)
					if currentWindowCount < lastWindowCount && lastWindowCount > 0 {
						activeStillExists := false
						for _, w := range windowsAfterRefresh {
							if w.ID == activeWindowID {
								activeStillExists = true
								break
							}
						}
						if !activeStillExists {
							logEvent("WINDOW_CLOSE_RESTORE_TRIGGER active=%s prev_count=%d count=%d", activeWindowID, lastWindowCount, currentWindowCount)
							coordinator.SelectPreviousWindow()
							updateActiveWindow() // Re-fetch after selecting
						} else {
							logEvent("WINDOW_CLOSE_RESTORE_SKIP reason=active_exists active=%s prev_count=%d count=%d", activeWindowID, lastWindowCount, currentWindowCount)
						}
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

						cleanupOrphanedSidebars(windows, coordinator)
						cleanupOrphanWindowsByTmux(*sessionID, coordinator)
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
							if w, h, _, _, ok := activeClientGeometry(); ok {
								resizeAllWindowsToClient(w, h, "structure_refresh")
							}
							syncClientSizesFromTmux(server, coordinator, "structure_refresh")
							server.BroadcastRender()
						}
						lastWindowsHash = currentHash
						loop.SetLastWindowsHash(currentHash)
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
			}
		}
	}()

	// Idle/socket monitoring (idle-shutdown, session-existence, socket/PID
	// health) was migrated into the loop's handleIdleTick / handleSocketCheckTick
	// in Step 2 of the daemon refactor. The dedicated idle-monitor goroutine
	// is gone; ticks are submitted via runTicker calls wired alongside the
	// other tick goroutines above.

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
	return 0
}
