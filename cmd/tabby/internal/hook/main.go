// Package hook is a thin CLI that sends commands to the tabby daemon.
// Socket-based commands go through the daemon's Unix socket.
// Direct commands (signals, lifecycle) execute inline.
// Exported as the `tabby hook` subcommand.
package hook

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

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

func Run(allArgs []string) int {
	if len(allArgs) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tabby hook <action> [args...]")
		return 1
	}

	action := allArgs[0]
	args := allArgs[1:]

	// Direct-execution commands (no daemon socket needed)
	switch action {
	case "on-pane-resize":
		doOnPaneResize(args)
		return 0
	case "signal-client-resize":
		doSignalClientResize(args)
		return 0
	case "stabilize-client-resize":
		doStabilizeClientResize(args)
		return 0
	case "restore-input-focus":
		doRestoreInputFocus(args)
		return 0
	case "ensure-sidebar":
		doEnsureSidebar(args)
		return 0
	case "preserve-pane-ratios":
		doPreservePaneRatios(args)
		return 0
	case "focus-pane":
		doFocusPane(args)
		return 0
	case "set-indicator":
		doSetIndicator(args)
		return 0
	case "set-title":
		doSetTitle(args)
		return 0
	case "osc-handler":
		doOSCHandler()
		return 0
	case "resurrect-save":
		doResurrectSave(args)
		return 0
	case "resurrect-restore":
		doResurrectRestore(args)
		return 0

	// Tmux-hook subcommands (Step 4 of daemon refactor; see
	// /Users/b/.claude/plans/nifty-jingling-tulip.md). Each forwards the
	// hook over the daemon socket as a typed MsgHook. Failures are silent
	// (no error to tmux) so a stopped/restarting daemon doesn't surface
	// errors to the user. The daemon also still accepts SIGUSR1/SIGUSR2
	// during this rollout, so older binaries on disk continue to work.
	case "client-resized":
		doHookClientResized(args)
		return 0
	case "after-select-window":
		doHookAfterSelectWindow(args)
		return 0
	case "after-resize-pane":
		doHookAfterResizePane(args)
		return 0
	case "client-attached":
		doHookClientAttached(args)
		return 0
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

	case "toggle-collapse-sidebar":
		// No args needed — hides/shows the sidebar by stashing/restoring
		// panes (matches the phone hamburger path). See coordinator's
		// "toggle_collapse_sidebar" action handler.

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

	case "next-window", "prev-window":
		// Daemon resolves the active window itself. We forward
		// TMUX_PANE + the most-recently-active client TTY purely for
		// diagnostic logging (NAV_KEY_TRIGGER) so multi-client phone
		// vs desktop traces can be distinguished. Daemon ignores
		// non-window targets here, so behaviour is unchanged.
		target = os.Getenv("TMUX_PANE")
		if target == "" {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
				target = strings.TrimSpace(string(out))
			}
		}
		// Capture two TTYs to disambiguate which client fired the
		// binding. `display-message` without -c uses tmux's "default
		// client" — when run-shell is dispatched from a key binding,
		// that's the client that invoked it. We *also* record the
		// most-recently-active client TTY (sorted by client_activity)
		// as a sanity check, in case the default-client doesn't
		// reflect the keystroke origin.
		// TABBY_INVOKING_TTY is substituted by tmux at binding-dispatch
		// time using #{client_tty} — that's the firing client. If the
		// keybinding doesn't set it (older tabby.tmux), fall back to
		// display-message which uses tmux's default-client heuristic.
		invokingTTY := strings.TrimSpace(os.Getenv("TABBY_INVOKING_TTY"))
		if invokingTTY == "" {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{client_tty}").Output(); err == nil {
				invokingTTY = strings.TrimSpace(string(out))
			}
		}
		mostActiveTTY := ""
		if out, err := exec.Command("tmux", "list-clients", "-F", "#{client_activity}|#{client_tty}").Output(); err == nil {
			bestAct := int64(-1)
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(strings.TrimSpace(line), "|", 2)
				if len(parts) != 2 {
					continue
				}
				if act, err := strconv.ParseInt(parts[0], 10, 64); err == nil && act > bestAct {
					bestAct = act
					mostActiveTTY = parts[1]
				}
			}
		}
		value = "invoking=" + invokingTTY + ";most_active=" + mostActiveTTY

	case "toggle-minimize-window":
		// Default to current active pane; daemon resolves its window.
		target = os.Getenv("TMUX_PANE")
		for i := 0; i < len(args); i++ {
			if (args[i] == "-t" || args[i] == "--target") && i+1 < len(args) {
				target = args[i+1]
				break
			}
		}

	default:
		fatal("Unknown action: " + action)
	}

	daemonAction := strings.ReplaceAll(action, "-", "_")
	if daemonAction == "exit_if_no_main" {
		daemonAction = "exit_if_no_main_windows"
	}

	if err := sendAction(daemonAction, target, value); err != nil {
		// Silently exit — daemon may be restarting or not yet ready.
		// Exiting non-zero causes tmux to display error messages to the user.
		return 0
	}
	return 0
}

// ── Direct-execution commands ───────────────────────────────────────────

// doOnPaneResize replaces on_pane_resize.sh: only signal daemon if
// the resized pane is a sidebar pane (prevents feedback loops).
//
// Post-consolidation, pane_current_command reports "tabby" for every
// subcommand, so we also check pane_start_command which retains the
// original "exec -a sidebar-renderer ..." invocation.
func doOnPaneResize(args []string) {
	if len(args) < 1 || args[0] == "" {
		return
	}
	hookPane := args[0]
	out, err := exec.Command("tmux", "display", "-p", "-t", hookPane,
		"#{pane_current_command}|#{pane_start_command}").Output()
	if err != nil {
		return
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	cur := parts[0]
	start := ""
	if len(parts) == 2 {
		start = parts[1]
	}
	if !isSidebarStartOrCurrent(cur, start) {
		return
	}
	signalDaemon("USR1")
}

// isSidebarStartOrCurrent is the hook-package copy of the daemon's
// isSidebarPaneCommand. Kept local to avoid a cross-package import.
func isSidebarStartOrCurrent(cur, start string) bool {
	for _, s := range []string{cur, start} {
		if s == "" {
			continue
		}
		lower := strings.ToLower(s)
		if strings.Contains(lower, "sidebar-renderer") || strings.Contains(lower, "render sidebar") {
			return true
		}
	}
	return false
}

// doSignalClientResize is the handler for tmux's client-resized hook.
// It used to iterate every window and force-resize each one to the firing
// client's dimensions. That caused multi-client resize churn: with two
// attached clients (e.g. desktop + phone), each client's resize event would
// drag every window to its own size, fighting tmux until things settled.
// tmux already handles window sizing correctly via `window-size latest` +
// `aggressive-resize on`, so the explicit resize-window loop was redundant
// AND harmful. Now we just signal the daemon; the daemon's client geometry
// tick elects a single active client and runs one width sync per change.
func doSignalClientResize(args []string) {
	_ = args
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

// ── Tmux-hook subcommands (Step 4) ──────────────────────────────────────

// doHookClientResized forwards a tmux client-resized hook to the daemon as a
// MsgHook over the unix socket. Replaces the old `kill -USR2` path used by
// `tabby hook signal-client-resize`. The args carry the firing client's tty
// + size at hook time, captured by tmux format strings; the daemon reads
// them for diagnostics but still re-elects the active client via its own
// elector (so a non-active client's resize doesn't drag every window).
//
// Backward compat: the daemon still accepts SIGUSR2; older `tabby hook`
// binaries on disk during a partial deploy continue to function. The
// daemon's lastResizeKey dedup absorbs any duplicate signal+hook fires.
func doHookClientResized(args []string) {
	hookArgs := map[string]string{}
	if len(args) >= 1 {
		hookArgs["tty"] = args[0]
	}
	if len(args) >= 2 {
		hookArgs["width"] = args[1]
	}
	if len(args) >= 3 {
		hookArgs["height"] = args[2]
	}
	_ = sendHook("client-resized", hookArgs)
}

// doHookAfterSelectWindow forwards a tmux after-select-window hook. Replaces
// the inline `kill -USR1` previously embedded in the hook command string.
func doHookAfterSelectWindow(args []string) {
	hookArgs := map[string]string{}
	if len(args) >= 1 {
		hookArgs["window"] = args[0]
	}
	_ = sendHook("after-select-window", hookArgs)
}

// doHookAfterResizePane forwards a tmux after-resize-pane hook. The
// sidebar/header filter still lives in doOnPaneResize on the CLI side; this
// path is for callers that have already filtered.
func doHookAfterResizePane(args []string) {
	hookArgs := map[string]string{}
	if len(args) >= 1 {
		hookArgs["pane"] = args[0]
	}
	_ = sendHook("after-resize-pane", hookArgs)
}

// doHookClientAttached forwards a tmux client-attached hook so the daemon
// pokes refresh and observes the new client immediately. The cycle-pane
// `--ensure-content` step still runs from the tmux command string, not the
// daemon, so this is purely a refresh nudge.
func doHookClientAttached(args []string) {
	_ = args
	_ = sendHook("client-attached", nil)
}

// sendHook dials the daemon socket and sends a MsgHook envelope. Mirrors
// sendAction's graceful-failure pattern: dial timeout 200ms (tmux hooks are
// hot paths), and any error returns nil so tmux sees no failure.
func sendHook(kind string, args map[string]string) error {
	sessionID, err := getSessionID()
	if err != nil {
		return nil
	}
	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err != nil {
		// Daemon down or starting up — silent no-op matches signalDaemon.
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	msg := daemon.Message{
		Type:    daemon.MsgHook,
		Payload: daemon.HookPayload{Kind: kind, Args: args},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil
	}
	return nil
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

	msg := daemon.Message{
		Type:   daemon.MsgInput,
		Target: daemon.RenderTarget{Kind: daemon.TargetHook, Instance: "tabby-hook"},
		Payload: daemon.InputPayload{
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
