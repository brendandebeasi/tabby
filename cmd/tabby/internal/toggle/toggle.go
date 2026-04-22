// Package toggle enables or disables the tabby sidebar for the current
// tmux session. Exported as the `tabby toggle` subcommand.
//
// Migrated from cmd/tabby-toggle. Binary references now resolve to the
// consolidated tabby binary (via os.Executable) with subcommand arguments,
// so "tabby-daemon" → `tabby daemon`, "tabby-watchdog" → `tabby watchdog`,
// etc.
package toggle

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Run is the subcommand entry point. args are the tokens following
// "toggle" on the command line; currently none are accepted.
func Run(args []string) int {
	_ = args

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: cannot resolve own executable:", err)
		return 1
	}

	sessionID := tmuxGetValue("display-message", "-p", "#{session_id}")
	if sessionID == "" {
		return 1
	}

	stateFile := fmt.Sprintf("/tmp/tabby-sidebar-%s.state", sessionID)
	sockPath := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
	pidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)
	cleanStopSentinel := fmt.Sprintf("/tmp/tabby-daemon-%s.clean-stop", sessionID)
	watchdogPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.watchdog.pid", sessionID)

	// Concurrency guard — remove stale locks older than 30s
	toggleLock := fmt.Sprintf("/tmp/tabby-toggle-%s.lock", sessionID)
	if err := os.Mkdir(toggleLock, 0755); err != nil {
		if info, serr := os.Stat(toggleLock); serr == nil && time.Since(info.ModTime()) > 30*time.Second {
			os.Remove(toggleLock)
			if err2 := os.Mkdir(toggleLock, 0755); err2 != nil {
				return 0
			}
		} else {
			return 0 // another toggle running
		}
	}
	defer os.Remove(toggleLock)

	// Global watchdog: if we haven't finished in 30s something is genuinely
	// wedged. Signal via channel so a successful exit cancels the timer and
	// defers (lock cleanup) always run.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-time.After(30 * time.Second):
			os.Remove(toggleLock)
			fmt.Fprintln(os.Stderr, "Error: tabby toggle timed out after 30s")
			os.Exit(1)
		}
	}()

	// Get current state
	currentState := tmuxGetValue("show-options", "-qv", "@tabby_sidebar")
	if currentState == "" {
		if data, err := os.ReadFile(stateFile); err == nil {
			currentState = strings.TrimSpace(string(data))
		}
	}

	if currentState == "enabled" {
		disable(sessionID, pidFile, sockPath, cleanStopSentinel, watchdogPidFile, stateFile)
	} else {
		enable(sessionID, exe, pidFile, sockPath, watchdogPidFile, stateFile)
	}

	// Mouse reset
	run("tmux", "set", "-g", "mouse", "off")
	time.Sleep(20 * time.Millisecond)
	run("tmux", "set", "-g", "mouse", "on")

	// Refresh all clients
	out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output()
	for _, tty := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if tty != "" {
			run("tmux", "refresh-client", "-t", tty, "-S")
		}
	}
	return 0
}

func disable(sessionID, pidFile, sockPath, sentinel, watchdogPidFile, stateFile string) {
	_ = sessionID

	// Write sentinel for watchdog
	os.WriteFile(sentinel, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	// Kill daemon
	killFromPidFile(pidFile)
	os.Remove(pidFile)
	os.Remove(sockPath)

	// Kill watchdog
	killFromPidFile(watchdogPidFile)
	os.Remove(watchdogPidFile)

	// Gracefully stop renderers (SIGTERM first)
	gracefulKillAuxPanes()

	// Reset mouse escape sequences
	resetMouseEscapeSequences()

	// Kill remaining aux panes
	killAuxPanes()

	run("tmux", "refresh-client", "-S")

	// Remove resize hooks in parallel
	var unhookWg sync.WaitGroup
	for _, h := range []string{"after-resize-pane", "after-resize-window", "client-resized", "after-select-window", "client-active", "client-focus-in", "client-attached"} {
		unhookWg.Add(1)
		go func(name string) {
			defer unhookWg.Done()
			run("tmux", "set-hook", "-gu", name)
		}(h)
	}
	unhookWg.Wait()

	os.WriteFile(stateFile, []byte("disabled"), 0644)
	run("tmux", "set-option", "@tabby_sidebar", "disabled")
	run("tmux", "set-option", "-g", "status", "on")
}

func enable(sessionID, exe, pidFile, sockPath, watchdogPidFile, stateFile string) {
	os.WriteFile(stateFile, []byte("enabled"), 0644)
	run("tmux", "set-option", "@tabby_sidebar", "enabled")

	// Snapshot pane layouts before system panes are spawned
	windowIDs := listWindowIDs()
	for _, wid := range windowIDs {
		saved := tmuxGetValue("show-option", "-gqv", "@tabby_layout_"+wid)
		if saved != "" {
			run("tmux", "set-option", "-g", "@tabby_restore_layout_"+wid, saved)
		}
	}

	// Clean up existing aux panes
	gracefulKillAuxPanes()
	resetMouseEscapeSequences()
	killAuxPanes()

	run("tmux", "set-option", "-g", "status", "off")

	// Save current focus
	curWindow := tmuxGetValue("display-message", "-p", "#{window_id}")
	curPane := tmuxGetValue("display-message", "-p", "#{pane_id}")
	run("tmux", "set-option", "-g", "@tabby_last_window", curWindow)
	run("tmux", "set-option", "-g", "@tabby_last_pane", curPane)

	// Start daemon via watchdog — same tabby binary, "watchdog" subcommand.
	watchdogArgs := []string{"watchdog", "-session", sessionID}
	if os.Getenv("TABBY_DEBUG") == "1" {
		watchdogArgs = append(watchdogArgs, "-debug")
	}
	cmd := exec.Command(exe, watchdogArgs...)
	cmd.Start()

	// Wait for socket
	socketReady := false
	for i := 0; i < 20; i++ {
		if fileExists(sockPath) {
			socketReady = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !socketReady {
		fmt.Fprintln(os.Stderr, "Error: Failed to start daemon (socket not created)")
		os.Exit(1)
	}

	// Store daemon PID in tmux option
	if pidData, err := os.ReadFile(pidFile); err == nil {
		daemonPid := strings.TrimSpace(string(pidData))
		run("tmux", "set-option", "-g", "@tabby_daemon_pid", daemonPid)
	}

	// Register hooks using the consolidated tabby binary. Each hook invokes
	// `tabby hook <subcommand>` or `tabby cycle-pane` etc.
	hookCmd := fmt.Sprintf("%s hook", exe)
	cycleCmd := fmt.Sprintf("%s cycle-pane", exe)

	// shellcheck disable equivalent: these use $() which tmux expands at hook time
	signalCmd := `kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true`

	// Register hooks in parallel
	var hookWg sync.WaitGroup
	hooks := [][2]string{
		{"after-resize-pane", fmt.Sprintf("run-shell -b '%s on-pane-resize \"#{hook_pane}\"'", hookCmd)},
		{"after-resize-window", fmt.Sprintf("run-shell -b '%s'", signalCmd)},
		{"client-resized", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"client-active", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"client-focus-in", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"after-select-window", fmt.Sprintf("run-shell -b '%s; tmux refresh-client -S; %s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s --ensure-content'", signalCmd, hookCmd, cycleCmd)},
		{"client-attached", fmt.Sprintf("run-shell -b '%s --ensure-content'", cycleCmd)},
	}
	for _, h := range hooks {
		hookWg.Add(1)
		go func(name, cmd string) {
			defer hookWg.Done()
			run("tmux", "set-hook", "-g", name, cmd)
		}(h[0], h[1])
	}
	hookWg.Wait()

	// Wait for renderers
	for i := 0; i < 10; i++ {
		out, _ := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_current_command}|#{pane_start_command}").Output()
		count := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "sidebar-renderer") || strings.Contains(line, "sidebar") {
				count++
			}
		}
		if count > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Restore content pane layouts
	for _, wid := range windowIDs {
		restoreLayout := tmuxGetValue("show-option", "-gqv", "@tabby_restore_layout_"+wid)
		if restoreLayout != "" {
			run("tmux", "select-layout", "-t", wid, restoreLayout)
			run("tmux", "set-option", "-g", "@tabby_layout_"+wid, restoreLayout)
			run("tmux", "set-option", "-gu", "@tabby_restore_layout_"+wid)
		}
	}

	// Restore focus
	if curWindow != "" {
		run("tmux", "select-window", "-t", curWindow)
	}
	if curPane != "" {
		run("tmux", "select-pane", "-t", curPane)
	} else {
		sidebarPos := tmuxGetValue("show-option", "-gqv", "@tabby_sidebar_position")
		if sidebarPos == "left" {
			run("tmux", "select-pane", "-t", "{right}")
		} else {
			run("tmux", "select-pane", "-t", "{left}")
		}
	}

	// Clear activity flags
	for _, wid := range windowIDs {
		run("tmux", "set-window-option", "-t", wid, "-q", "monitor-activity", "off")
		run("tmux", "set-window-option", "-t", wid, "-q", "monitor-activity", "on")
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

func run(args ...string) {
	exec.Command(args[0], args[1:]...).Run()
}

func tmuxGetValue(args ...string) string {
	out, _ := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out))
}

func listWindowIDs() []string {
	out, _ := exec.Command("tmux", "list-windows", "-F", "#{window_id}").Output()
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			ids = append(ids, line)
		}
	}
	return ids
}

func killFromPidFile(pidFile string) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid != "" {
		exec.Command("kill", pid).Run()
	}
}

func killAuxPanes() {
	out, _ := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_current_command}|#{pane_id}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		cmd := strings.ToLower(parts[0])
		if strings.HasPrefix(cmd, "sidebar") || strings.HasPrefix(cmd, "pane-header") || strings.HasPrefix(cmd, "tabby-daemon") || strings.HasPrefix(cmd, "tabby") {
			exec.Command("tmux", "kill-pane", "-t", parts[1]).Run()
		}
	}
}

func gracefulKillAuxPanes() {
	out, _ := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_current_command}|#{pane_id}|#{pane_pid}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		cmd := strings.ToLower(parts[0])
		if strings.HasPrefix(cmd, "sidebar") || strings.HasPrefix(cmd, "pane-header") {
			if parts[2] != "" {
				exec.Command("kill", "-TERM", parts[2]).Run()
			}
		}
	}
	time.Sleep(30 * time.Millisecond)
}

func resetMouseEscapeSequences() {
	out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output()
	resetSeq := "\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1006l\033[?1015l"
	for _, tty := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if tty == "" {
			continue
		}
		f, err := os.OpenFile(tty, os.O_WRONLY, 0)
		if err != nil {
			continue
		}
		f.WriteString(resetSeq)
		f.Close()
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
