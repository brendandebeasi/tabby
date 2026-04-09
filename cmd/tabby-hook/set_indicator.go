package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// doSetIndicator replaces set-tabby-indicator.sh: set tabby indicators
// (busy, bell, activity, silence, input, crash) on a tmux window.
// Usage: tabby-hook set-indicator <type> <0|1>
func doSetIndicator(args []string) {
	if len(args) < 2 {
		return
	}
	indicator := args[0]
	value := args[1]

	stateDir := "/tmp/tabby-state"
	os.MkdirAll(stateDir, 0755)

	sessionOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	session := strings.TrimSpace(string(sessionOut))

	// Resolve the window for this indicator
	win := resolveIndicatorWindow(indicator, value, session, stateDir)
	if win == "" {
		return
	}

	winTarget := ":" + win

	switch indicator {
	case "busy":
		if value == "1" {
			touchFile(fmt.Sprintf("%s/busy-%s-%s", stateDir, session, win))
			if session != "" {
				os.WriteFile(fmt.Sprintf("%s/last-%s", stateDir, session), []byte(win), 0644)
			}
			tmuxSetWindowOpt(winTarget, "@tabby_busy", "1")
			tmuxUnsetWindowOpt(winTarget, "@tabby_bell")
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_busy")
			os.Remove(fmt.Sprintf("%s/busy-%s-%s", stateDir, session, win))
			if session != "" {
				os.WriteFile(fmt.Sprintf("%s/last-%s", stateDir, session), []byte(win), 0644)
			}
		}

	case "bell":
		if value == "1" {
			tmuxUnsetWindowOpt(winTarget, "@tabby_busy")
			tmuxSetWindowOpt(winTarget, "@tabby_bell", "1")
			os.Remove(fmt.Sprintf("%s/busy-%s-%s", stateDir, session, win))
			if session != "" {
				os.WriteFile(fmt.Sprintf("%s/last-%s", stateDir, session), []byte(win), 0644)
			}
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_bell")
		}

	case "activity":
		if value == "1" {
			tmuxSetWindowOpt(winTarget, "@tabby_activity", "1")
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_activity")
		}

	case "silence":
		if value == "1" {
			tmuxSetWindowOpt(winTarget, "@tabby_silence", "1")
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_silence")
		}

	case "input":
		if value == "1" {
			tmuxUnsetWindowOpt(winTarget, "@tabby_busy")
			tmuxSetWindowOpt(winTarget, "@tabby_input", "1")
			if session != "" {
				os.WriteFile(fmt.Sprintf("%s/last-%s", stateDir, session), []byte(win), 0644)
			}
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_input")
		}

	case "crash":
		if value == "1" {
			tmuxSetWindowOpt(winTarget, "@tabby_crash", "1")
		} else {
			tmuxUnsetWindowOpt(winTarget, "@tabby_crash")
		}
	}

	// Signal daemon to refresh immediately
	signalDaemon("USR1")
}

// resolveIndicatorWindow finds the correct tmux window for the indicator.
func resolveIndicatorWindow(indicator, value, session, stateDir string) string {
	// Strategy 1: TMUX_PANE environment variable
	tmuxPane := os.Getenv("TMUX_PANE")
	if tmuxPane != "" {
		// Verify pane still exists
		out, err := exec.Command("tmux", "display-message", "-t", tmuxPane, "-p", "#{window_index}").Output()
		if err == nil {
			win := strings.TrimSpace(string(out))
			if win != "" {
				return win
			}
		}
	}

	// Strategy 2: Walk up process tree to find tmux pane
	searchPID := os.Getpid()
	for i := 0; i < 10; i++ {
		ppidOut, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(searchPID)).Output()
		if err != nil {
			break
		}
		searchPID, err = strconv.Atoi(strings.TrimSpace(string(ppidOut)))
		if err != nil || searchPID <= 1 {
			break
		}
		out, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid}|#{window_index}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "|", 2)
			if len(parts) == 2 && parts[0] == strconv.Itoa(searchPID) {
				return parts[1]
			}
		}
	}

	// Strategy 3: For busy=1, use active window (user just typed)
	if indicator == "busy" && value == "1" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{window_index}").Output()
		win := strings.TrimSpace(string(out))
		if win != "" {
			return win
		}
	}

	// Strategy 4: State-based recovery for stop/bell events
	return resolveWindowFromState(session, stateDir)
}

// resolveWindowFromState finds the window from state files.
func resolveWindowFromState(session, stateDir string) string {
	// Check last-session file
	if session != "" {
		data, err := os.ReadFile(fmt.Sprintf("%s/last-%s", stateDir, session))
		if err == nil {
			win := strings.TrimSpace(string(data))
			if win != "" && windowExists(win) {
				return win
			}
		}
	}

	// Check busy state files
	if session != "" {
		entries, _ := os.ReadDir(stateDir)
		prefix := "busy-" + session + "-"
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), prefix) {
				win := strings.TrimPrefix(e.Name(), prefix)
				if windowExists(win) {
					return win
				}
			}
		}
	}

	// Last resort: find window with @tabby_busy set
	out, _ := exec.Command("tmux", "list-windows", "-F", "#{window_index} #{@tabby_busy}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] != "" && parts[1] != "0" {
			return parts[0]
		}
	}

	return ""
}

func windowExists(win string) bool {
	out, _ := exec.Command("tmux", "list-windows", "-F", "#{window_index}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == win {
			return true
		}
	}
	return false
}

func tmuxSetWindowOpt(target, key, value string) {
	exec.Command("tmux", "set-option", "-t", target, "-w", key, value).Run()
}

func tmuxUnsetWindowOpt(target, key string) {
	exec.Command("tmux", "set-option", "-t", target, "-wu", key).Run()
}

func touchFile(path string) {
	f, err := os.Create(path)
	if err == nil {
		f.Close()
	}
}
