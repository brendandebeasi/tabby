// tabby-hook is a thin CLI that sends commands to the tabby-daemon.
// Socket-based commands go through the daemon's Unix socket.
// Direct commands (signals, lifecycle) execute inline.
//
// Usage: tabby-hook <action> [args...]
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type message struct {
	Type     string      `json:"type"`
	ClientID string      `json:"client_id,omitempty"`
	Payload  interface{} `json:"payload,omitempty"`
}

type inputPayload struct {
	Type           string `json:"type"`
	ResolvedAction string `json:"resolved_action"`
	ResolvedTarget string `json:"resolved_target,omitempty"`
	PickerValue    string `json:"picker_value,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tabby-hook <action> [args...]")
		os.Exit(1)
	}

	action := os.Args[1]
	args := os.Args[2:]

	// Direct-execution commands (no daemon socket needed)
	switch action {
	case "on-pane-resize":
		doOnPaneResize(args)
		return
	case "signal-client-resize":
		doSignalClientResize(args)
		return
	case "stabilize-client-resize":
		doStabilizeClientResize(args)
		return
	case "restore-input-focus":
		doRestoreInputFocus(args)
		return
	case "ensure-sidebar":
		doEnsureSidebar(args)
		return
	case "preserve-pane-ratios":
		doPreservePaneRatios(args)
		return
	case "focus-pane":
		doFocusPane(args)
		return
	case "set-indicator":
		doSetIndicator(args)
		return
	case "resurrect-save":
		doResurrectSave(args)
		return
	case "resurrect-restore":
		doResurrectRestore(args)
		return
	}

	// Socket-based commands
	var target, value string

	switch action {
	case "delete-group":
		if len(args) < 1 {
			fatal("Usage: tabby-hook delete-group <name>")
		}
		target = args[0]

	case "rename-group":
		if len(args) < 2 {
			fatal("Usage: tabby-hook rename-group <old-name> <new-name>")
		}
		target = args[0]
		value = args[1]

	case "set-group-color":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-color <name> <color>")
		}
		target = args[0]
		value = args[1]

	case "set-group-marker":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-marker <name> <marker>")
		}
		target = args[0]
		value = args[1]

	case "set-group-working-dir":
		if len(args) < 2 {
			fatal("Usage: tabby-hook set-group-working-dir <name> <dir>")
		}
		target = args[0]
		value = args[1]

	case "toggle-group-collapse":
		if len(args) < 2 {
			fatal("Usage: tabby-hook toggle-group-collapse <name> <collapse|expand>")
		}
		target = args[0]
		value = args[1]

	case "toggle-pane-collapse":
		target = os.Getenv("TMUX_PANE")
		for i := 0; i < len(args); i++ {
			if (args[i] == "-t" || args[i] == "--target") && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}

	case "kill-pane":
		target = os.Getenv("TMUX_PANE")
		for i := 0; i < len(args); i++ {
			if (args[i] == "-t" || args[i] == "--target") && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}

	case "toggle-sidebar":
		// No args needed

	case "new-group":
		if len(args) > 0 {
			target = args[0]
		}

	case "kill-window":
		if len(args) < 1 {
			fatal("Usage: tabby-hook kill-window <window-index>")
		}
		target = args[0]

	case "split-pane":
		if len(args) < 1 {
			fatal("Usage: tabby-hook split-pane <v|h>")
		}
		target = args[0]
		if len(args) > 1 {
			value = args[1]
		} else if pane := os.Getenv("TMUX_PANE"); pane != "" {
			value = pane
		}

	case "pane-click":
		if len(args) < 5 {
			fatal("Usage: tabby-hook pane-click <pane-id> <mouse-x> <mouse-y> <pane-left> <pane-top>")
		}
		target = args[0]
		value = args[1] + "," + args[2] + "," + args[3] + "," + args[4]

	case "exit-if-no-main":
		// No args needed

	default:
		fatal("Unknown action: " + action)
	}

	daemonAction := strings.ReplaceAll(action, "-", "_")

	if err := sendAction(daemonAction, target, value); err != nil {
		// Silently exit — daemon may be restarting or not yet ready.
		// Exiting non-zero causes tmux to display error messages to the user.
		os.Exit(0)
	}
}

// ── Direct-execution commands ───────────────────────────────────────────

// doOnPaneResize replaces on_pane_resize.sh: only signal daemon if
// the resized pane is a sidebar pane (prevents feedback loops).
func doOnPaneResize(args []string) {
	if len(args) < 1 || args[0] == "" {
		return
	}
	hookPane := args[0]
	out, err := exec.Command("tmux", "display", "-p", "-t", hookPane, "#{pane_current_command}").Output()
	if err != nil {
		return
	}
	cmd := strings.TrimSpace(string(out))
	if !strings.HasPrefix(cmd, "sidebar") {
		return
	}
	signalDaemon("USR1")
}

// doSignalClientResize replaces signal_client_resize.sh: resize all windows
// to client dimensions, then send USR2 to daemon.
func doSignalClientResize(args []string) {
	width := ""
	height := ""
	if len(args) >= 1 {
		width = args[0]
	}
	if len(args) >= 2 {
		height = args[1]
	}

	if width != "" && height != "" {
		if isNumeric(width) && isNumeric(height) {
			out, _ := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
			for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if wid == "" {
					continue
				}
				exec.Command("tmux", "resize-window", "-x", width, "-y", height, "-t", wid).Run()
			}
		}
	}
	signalDaemon("USR2")
}

// doStabilizeClientResize replaces stabilize_client_resize.sh:
// ensure sidebar + signal client resize + refresh.
func doStabilizeClientResize(args []string) {
	// Args: session_id window_id client_tty client_width client_height
	windowID := ""
	clientWidth := ""
	clientHeight := ""
	if len(args) >= 2 {
		windowID = args[1]
	}
	if len(args) >= 4 {
		clientWidth = args[3]
	}
	if len(args) >= 5 {
		clientHeight = args[4]
	}

	if windowID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		windowID = strings.TrimSpace(string(out))
	}

	sessionID, _ := getSessionID()

	// Ensure sidebar
	ensureSidebar(sessionID, windowID)

	// Signal client resize
	if clientWidth != "" && clientHeight != "" {
		doSignalClientResize([]string{clientWidth, clientHeight})
	}

	exec.Command("tmux", "refresh-client", "-S").Run()
}

// doRestoreInputFocus replaces restore_input_focus.sh: find a content
// pane and select it (avoids focus landing on sidebar/header panes).
func doRestoreInputFocus(args []string) {
	spawning, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output()
	if strings.TrimSpace(string(spawning)) == "1" {
		return
	}

	sessionID := ""
	if len(args) > 0 {
		sessionID = args[0]
	}
	if sessionID == "" {
		sessionID, _ = getSessionID()
	}
	if sessionID == "" {
		return
	}

	curWin, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	windowID := strings.TrimSpace(string(curWin))
	if windowID == "" {
		out, _ := exec.Command("tmux", "list-windows", "-t", sessionID, "-F", "#{window_id}").Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			windowID = lines[0]
		}
	}

	curPane, _ := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	paneID := strings.TrimSpace(string(curPane))

	targetPane := ""

	// If current pane is a content pane, use it
	if paneID != "" {
		curCmd, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_current_command}").Output()
		cmd := strings.TrimSpace(string(curCmd))
		if !isAuxCmd(cmd) {
			targetPane = paneID
		}
	}

	// Search current window for content pane
	if targetPane == "" && windowID != "" {
		targetPane = findFirstContentPane(windowID)
	}

	// Search all session panes
	if targetPane == "" {
		out, _ := exec.Command("tmux", "list-panes", "-t", sessionID, "-F", "#{pane_id}|#{pane_current_command}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "|", 2)
			if len(parts) != 2 {
				continue
			}
			if !isAuxCmd(parts[1]) {
				targetPane = parts[0]
				break
			}
		}
	}

	if targetPane != "" {
		// Get the window containing this pane and select it
		tw, _ := exec.Command("tmux", "display-message", "-p", "-t", targetPane, "#{window_id}").Output()
		if w := strings.TrimSpace(string(tw)); w != "" {
			exec.Command("tmux", "select-window", "-t", w).Run()
		}
		exec.Command("tmux", "select-pane", "-t", targetPane).Run()
	}

	// Mouse reset to clear any stuck state
	exec.Command("tmux", "set", "-g", "mouse", "off").Run()
	time.Sleep(50 * time.Millisecond)
	exec.Command("tmux", "set", "-g", "mouse", "on").Run()

	// Refresh all clients
	out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output()
	for _, tty := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if tty != "" {
			exec.Command("tmux", "refresh-client", "-t", tty, "-S").Run()
		}
	}
}

// doEnsureSidebar replaces ensure_sidebar.sh: check daemon is running,
// start via watchdog if needed, signal for renderer spawning.
func doEnsureSidebar(args []string) {
	spawning, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output()
	if strings.TrimSpace(string(spawning)) == "1" {
		return
	}

	sessionID, _ := getSessionID()
	if sessionID == "" {
		return
	}

	windowID := ""
	if len(args) >= 2 {
		windowID = args[1]
	}
	if windowID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		windowID = strings.TrimSpace(string(out))
	}

	ensureSidebar(sessionID, windowID)
}

// ensureSidebar is the core logic shared by doEnsureSidebar and doStabilizeClientResize.
func ensureSidebar(sessionID, windowID string) {
	if sessionID == "" {
		return
	}

	// Check mode
	mode, _ := exec.Command("tmux", "show-options", "-gqv", "@tabby_sidebar").Output()
	modeStr := strings.TrimSpace(string(mode))
	if modeStr == "" {
		mode2, _ := exec.Command("tmux", "show-options", "-qv", "@tabby_sidebar").Output()
		modeStr = strings.TrimSpace(string(mode2))
	}
	if modeStr == "" {
		stateFile := fmt.Sprintf("/tmp/tabby-sidebar-%s.state", sessionID)
		data, err := os.ReadFile(stateFile)
		if err == nil {
			modeStr = strings.TrimSpace(string(data))
		}
	}

	if modeStr != "enabled" {
		return
	}

	exec.Command("tmux", "set-option", "-g", "status", "off").Run()

	// Check if current window already has a sidebar renderer
	if windowID != "" {
		out, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_current_command}|#{pane_start_command}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.Contains(line, "sidebar-renderer") || strings.Contains(line, "sidebar") {
				return // sidebar already exists
			}
		}
	}

	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	pidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)

	// Check daemon alive
	daemonRunning := false
	if fileExists(sockPath) && fileExists(pidFile) {
		pidData, _ := os.ReadFile(pidFile)
		pid := strings.TrimSpace(string(pidData))
		if pid != "" {
			if err := exec.Command("kill", "-0", pid).Run(); err == nil {
				daemonRunning = true
			}
		}
	}

	if !daemonRunning {
		// Check if watchdog is already running
		watchdogPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.watchdog.pid", sessionID)
		watchdogAlive := false
		if fileExists(watchdogPidFile) {
			wpData, _ := os.ReadFile(watchdogPidFile)
			wp := strings.TrimSpace(string(wpData))
			if wp != "" {
				if err := exec.Command("kill", "-0", wp).Run(); err == nil {
					watchdogAlive = true
				}
			}
		}

		if !watchdogAlive {
			os.Remove(sockPath)
			os.Remove(pidFile)

			exe, _ := os.Executable()
			watchdogBin := filepath.Join(filepath.Dir(exe), "tabby-watchdog")
			watchdogArgs := []string{"-session", sessionID}
			if os.Getenv("TABBY_DEBUG") == "1" {
				watchdogArgs = append(watchdogArgs, "-debug")
			}

			cmd := exec.Command(watchdogBin, watchdogArgs...)
			cmd.Start()
		}

		// Wait for socket
		for i := 0; i < 20; i++ {
			if fileExists(sockPath) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		exec.Command("tmux", "set-option", "-gu", "@tabby_spawning").Run()
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

func signalDaemon(sig string) {
	out, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_daemon_pid").Output()
	pid := strings.TrimSpace(string(out))
	if pid == "" {
		// Fallback: read PID file
		sessionID, err := getSessionID()
		if err != nil {
			return
		}
		pidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid = strings.TrimSpace(string(data))
	}
	if pid != "" {
		exec.Command("kill", "-"+sig, pid).Run()
	}
}

func isAuxCmd(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "sidebar") ||
		strings.Contains(lower, "renderer") ||
		strings.Contains(lower, "pane-header") ||
		strings.Contains(lower, "tabby-daemon")
}

func findFirstContentPane(windowID string) string {
	out, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}|#{pane_current_command}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if !isAuxCmd(parts[1]) {
			return parts[0]
		}
	}
	return ""
}

func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sendAction(action, target, value string) error {
	sessionID, err := getSessionID()
	if err != nil {
		return fmt.Errorf("failed to get session ID: %w", err)
	}

	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("daemon not running (socket %s): %w", sockPath, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	msg := message{
		Type: "input",
		Payload: inputPayload{
			Type:           "action",
			ResolvedAction: action,
			ResolvedTarget: target,
			PickerValue:    value,
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = conn.Write(data)
	return err
}

func getSessionID() (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
