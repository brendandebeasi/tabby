package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/b/tmux-tabs/pkg/daemon"
)

var (
	sessionID = flag.String("session", "", "tmux session ID")
	debug     = flag.Bool("debug", false, "Enable debug logging")
)

var debugLog *log.Logger

// getRendererBin returns the path to the sidebar-renderer binary
func getRendererBin() string {
	// Get path relative to this binary
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	return filepath.Join(dir, "sidebar-renderer")
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
		return
	}

	// Get connected clients (each identified by their window ID)
	connectedClients := make(map[string]bool)
	for _, clientID := range server.GetAllClientIDs() {
		connectedClients[clientID] = true
	}

	// Check each window
	windowIDs := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, windowID := range windowIDs {
		windowID = strings.TrimSpace(windowID)
		if windowID == "" {
			continue
		}

		// Skip if already has a renderer
		if connectedClients[windowID] {
			continue
		}

		// Check if window already has a sidebar/renderer pane (in case renderer hasn't connected yet)
		paneOut, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_current_command}").Output()
		if err != nil {
			continue
		}
		hasRenderer := false
		for _, cmd := range strings.Split(string(paneOut), "\n") {
			cmd = strings.TrimSpace(cmd)
			if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") {
				hasRenderer = true
				break
			}
		}
		if hasRenderer {
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
		debugLog.Printf("Spawning renderer for new window %s (pane %s)", windowID, firstPane)
		// Use exec to replace shell with renderer (matches toggle_sidebar_daemon.sh behavior)
		cmdStr := fmt.Sprintf("exec '%s' -session '%s' -window '%s'", rendererBin, sessionID, windowID)
		cmd := exec.Command("tmux", "split-window", "-t", firstPane, "-h", "-b", "-f", "-l", "25", cmdStr)
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

		// Focus the main pane (right pane) instead of staying in sidebar
		exec.Command("tmux", "select-pane", "-t", windowID, "-R").Run()
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

			if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "renderer") || strings.Contains(cmd, "tabby") {
				sidebarPaneID = paneID
			} else {
				nonSidebarCount++
			}
		}

		// If only sidebar pane remains, close it (which closes the window)
		if nonSidebarCount == 0 && sidebarPaneID != "" {
			debugLog.Printf("Window %s has only sidebar pane, closing it", windowID)
			exec.Command("tmux", "kill-pane", "-t", sidebarPaneID).Run()
		}
	}
}

func main() {
	flag.Parse()

	if *debug {
		debugLog = log.New(os.Stderr, "[daemon] ", log.LstdFlags|log.Lmicroseconds)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	// Get session ID from environment if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	debugLog.Printf("Starting daemon for session %s", *sessionID)

	// Create coordinator for centralized rendering
	coordinator := NewCoordinator(*sessionID)

	// Create server
	server := daemon.NewServer(*sessionID)

	// Set up render callback using coordinator
	server.OnRenderNeeded = func(clientID string, width, height int) *daemon.RenderPayload {
		return coordinator.RenderForClient(clientID, width, height)
	}

	// Set up input callback
	server.OnInput = func(clientID string, input *daemon.InputPayload) {
		coordinator.HandleInput(clientID, input)
		// Immediately refresh windows to get updated state from tmux
		coordinator.RefreshWindows()
		// Re-render all clients with fresh state
		server.BroadcastRender()
	}

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	debugLog.Printf("Server listening on %s", server.GetSocketPath())

	// Start coordinator refresh loops with change detection
	go func() {
		refreshTicker := time.NewTicker(2 * time.Second)        // Window list (less frequent)
		windowCheckTicker := time.NewTicker(500 * time.Millisecond) // Check for new windows (faster)
		spinnerTicker := time.NewTicker(100 * time.Millisecond) // Spinner animation
		gitTicker := time.NewTicker(5 * time.Second)            // Git status
		petTicker := time.NewTicker(100 * time.Millisecond)     // Pet state updates (for smooth animation)
		defer refreshTicker.Stop()
		defer windowCheckTicker.Stop()
		defer spinnerTicker.Stop()
		defer gitTicker.Stop()
		defer petTicker.Stop()

		lastWindowsHash := ""
		lastGitState := ""

		for {
			select {
			case <-windowCheckTicker.C:
				// Check for new windows that need renderers (fast check)
				spawnRenderersForNewWindows(server, *sessionID)
				// Also cleanup orphaned sidebars
				cleanupOrphanedSidebars()
			case <-refreshTicker.C:
				// Only broadcast if windows changed
				currentHash := coordinator.GetWindowsHash()
				if currentHash != lastWindowsHash {
					coordinator.RefreshWindows()
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

	// Monitor for idle shutdown (no clients for 30s) and session existence
	go func() {
		idleTicker := time.NewTicker(10 * time.Second)
		defer idleTicker.Stop()
		idleStart := time.Time{}

		for {
			select {
			case <-idleTicker.C:
				// Check if session still exists
				if _, err := exec.Command("tmux", "has-session", "-t", *sessionID).Output(); err != nil {
					debugLog.Printf("Session %s no longer exists, shutting down", *sessionID)
					sigCh <- syscall.SIGTERM
					return
				}

				// Check if any windows remain
				out, err := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
				if err != nil || strings.TrimSpace(string(out)) == "" {
					debugLog.Printf("No windows remaining, shutting down")
					sigCh <- syscall.SIGTERM
					return
				}

				// Idle timeout if no clients
				if server.ClientCount() == 0 {
					if idleStart.IsZero() {
						idleStart = time.Now()
					} else if time.Since(idleStart) > 30*time.Second {
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
	server.Stop()
}
