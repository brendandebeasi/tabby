package hook

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// doFocusPane replaces focus_pane.sh: focus a specific tmux
// session/window/pane and optionally bring terminal to foreground.
// Usage: tabby-hook focus-pane [session:]window[.pane]
func doFocusPane(args []string) {
	target := "0"
	if len(args) > 0 {
		target = args[0]
	}

	session, window, pane := parsePaneTarget(target)

	// Validate session exists
	if exec.Command("tmux", "has-session", "-t", session).Run() != nil {
		return
	}

	// Select window and pane
	windowTarget := session + ":" + window
	paneTarget := session + ":" + window + "." + pane
	exec.Command("tmux", "select-window", "-t", windowTarget).Run()
	exec.Command("tmux", "select-pane", "-t", paneTarget).Run()

	time.Sleep(100 * time.Millisecond)

	// Signal daemon to refresh
	sessionID, _ := exec.Command("tmux", "display-message", "-t", session, "-p", "#{session_id}").Output()
	sid := strings.TrimSpace(string(sessionID))
	if sid != "" {
		signalDaemonForSession(sid, "USR1")
	}

	// Refresh status bar
	exec.Command("tmux", "refresh-client", "-t", session, "-S").Run()

	// Read terminal_app from config and bring to foreground
	configFile := resolveConfigFile()
	termApp := readConfigValue(configFile, "terminal_app")
	if termApp == "" {
		return
	}

	if termApp == "Ghostty" {
		script := `tell application "Ghostty" to activate
delay 0.05
tell application "System Events"
    tell process "Ghostty"
        set wCount to count of windows
        repeat with i from 1 to wCount
            set wName to name of window i
            if wName contains "tmux" then
                perform action "AXRaise" of window i
                exit repeat
            end if
        end repeat
    end tell
end tell`
		exec.Command("osascript", "-e", script).Run()
	} else {
		exec.Command("osascript", "-e", `tell application "`+termApp+`" to activate`).Run()
	}
}

// parsePaneTarget parses [session:]window[.pane] into components.
func parsePaneTarget(target string) (session, window, pane string) {
	pane = "0"

	if idx := strings.Index(target, ":"); idx >= 0 {
		session = target[:idx]
		target = target[idx+1:]
	} else {
		out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			session = lines[0]
		}
	}

	if idx := strings.Index(target, "."); idx >= 0 {
		window = target[:idx]
		pane = target[idx+1:]
	} else {
		window = target
	}
	return
}

// signalDaemonForSession sends a signal to the daemon for a specific session.
func signalDaemonForSession(sessionID, sig string) {
	pidFile := "/tmp/tabby-daemon-" + sessionID + ".pid"
	data, err := readFileBytes(pidFile)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid != "" {
		exec.Command("kill", "-"+sig, pid).Run()
	}
}

// resolveConfigFile finds the tabby config file path.
func resolveConfigFile() string {
	// Check XDG first, then fallback
	home, _ := exec.Command("sh", "-c", "echo $HOME").Output()
	h := strings.TrimSpace(string(home))
	candidates := []string{
		h + "/.config/tabby/config.yaml",
		h + "/.config/tabby/config.yml",
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

// readConfigValue reads a simple key: value from a YAML config file.
func readConfigValue(configFile, key string) string {
	if configFile == "" {
		return ""
	}
	data, err := readFileBytes(configFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			val := strings.TrimPrefix(trimmed, key+":")
			val = strings.TrimSpace(val)
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// readFileBytes reads a file and returns its contents.
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
