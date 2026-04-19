// Package watchdog supervises the tabby daemon: restarts on unexpected
// exit, gives up after too many restarts, runs the crash handler.
// Exported as the `tabby watchdog` subcommand.
package watchdog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxRestarts   = 5
	restartWindow = 60 * time.Second
	restartDelay  = 1 * time.Second
)

// Run is the subcommand entry. args is os.Args[2:] i.e. tokens after
// "watchdog".
func Run(args []string) int {
	sessionID := ""
	debug := false
	daemonArgs := []string{"daemon"} // self-invoke the daemon subcommand

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-session":
			if i+1 < len(args) {
				i++
				sessionID = args[i]
				daemonArgs = append(daemonArgs, "-session", sessionID)
			}
		case "-debug":
			debug = true
			daemonArgs = append(daemonArgs, "-debug")
		default:
			daemonArgs = append(daemonArgs, args[i])
		}
	}
	_ = debug

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "watchdog: cannot resolve own executable:", err)
		return 1
	}

	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "watchdog: -session required")
		// Fall through to exec daemon directly
		cmd := exec.Command(exe, daemonArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		return 0
	}

	sentinel := fmt.Sprintf("/tmp/tabby-daemon-%s.clean-stop", sessionID)
	watchdogPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.watchdog.pid", sessionID)
	crashLog := fmt.Sprintf("/tmp/tabby-daemon-%s-crash.log", sessionID)
	daemonPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)

	binDir := filepath.Dir(exe)
	crashHook := filepath.Join(binDir, "..", "scripts", "crash-handler.sh")

	// Write our PID
	os.WriteFile(watchdogPidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
	defer os.Remove(watchdogPidFile)

	restartCount := 0
	windowStart := time.Now()

	for {
		os.Remove(sentinel)

		// Register hooks before daemon starts so resize/focus events are captured
		registerHooks(exe)

		cmd := exec.Command(exe, daemonArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Start daemon, wait for PID file, then store PID in tmux option
		cmd.Start()
		go func() {
			for i := 0; i < 40; i++ {
				if pidData, err := os.ReadFile(daemonPidFile); err == nil {
					pid := strings.TrimSpace(string(pidData))
					if pid != "" {
						exec.Command("tmux", "set-option", "-g", "@tabby_daemon_pid", pid).Run()
						break
					}
				}
				time.Sleep(25 * time.Millisecond)
			}
		}()
		err := cmd.Wait()

		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}

		// Clean shutdown
		if _, err := os.Stat(sentinel); err == nil {
			os.Remove(sentinel)
			return 0
		}

		// Another daemon took over
		if pidData, err := os.ReadFile(daemonPidFile); err == nil {
			pid := strings.TrimSpace(string(pidData))
			if pid != "" {
				if exec.Command("kill", "-0", pid).Run() == nil {
					return 0
				}
			}
		}

		elapsed := time.Since(windowStart)
		if elapsed > restartWindow {
			restartCount = 0
			windowStart = time.Now()
		}

		restartCount++

		if restartCount > maxRestarts {
			logCrash(crashLog, "WATCHDOG_GIVE_UP restarts=%d window=%s session=%s",
				maxRestarts, restartWindow, sessionID)
			// Run crash handler for investigation
			if fileExists(crashHook) {
				cmd := exec.Command(crashHook, sessionID,
					strconv.Itoa(exitCode),
					strconv.Itoa(restartCount),
					strconv.Itoa(maxRestarts))
				cmd.Run()
			}
			return 1
		}

		logCrash(crashLog, "WATCHDOG_RESTART exit_code=%d attempt=%d/%d session=%s",
			exitCode, restartCount, maxRestarts, sessionID)

		// Transient crash: lightweight notification in background
		if fileExists(crashHook) {
			cmd := exec.Command(crashHook, sessionID,
				strconv.Itoa(exitCode),
				strconv.Itoa(restartCount),
				strconv.Itoa(maxRestarts))
			cmd.Start()
		}

		time.Sleep(restartDelay)
	}
}

func logCrash(path, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006/01/02 15:04:05")
	entry := fmt.Sprintf("%s %s\n", ts, msg)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(entry)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// registerHooks ensures tmux hooks for resize/focus/select are registered.
// Invokes the consolidated `tabby hook` and `tabby cycle-pane` subcommands.
func registerHooks(exe string) {
	hookCmd := fmt.Sprintf("%s hook", exe)
	cycleCmd := fmt.Sprintf("%s cycle-pane", exe)
	signalCmd := `kill -USR1 $(tmux show-option -gqv @tabby_daemon_pid) 2>/dev/null || true`

	type hook struct{ name, cmd string }
	hooks := []hook{
		{"after-resize-pane", fmt.Sprintf("run-shell -b '%s on-pane-resize \"#{hook_pane}\"'", hookCmd)},
		{"after-resize-window", fmt.Sprintf("run-shell -b '%s'", signalCmd)},
		{"client-resized", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"client-active", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"client-focus-in", fmt.Sprintf("run-shell '%s signal-client-resize \"#{client_width}\" \"#{client_height}\"'; run-shell '%s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s'", hookCmd, hookCmd, signalCmd)},
		{"after-select-window", fmt.Sprintf("run-shell -b '%s; tmux refresh-client -S; %s ensure-sidebar \"#{session_id}\" \"#{window_id}\"'; run-shell -b '%s --dim-only'", signalCmd, hookCmd, cycleCmd)},
	}
	for _, h := range hooks {
		exec.Command("tmux", "set-hook", "-g", h.name, h.cmd).Run()
	}
}
