package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// doResurrectSave replaces resurrect_save_hook.sh:
// strips Tabby utility pane lines from the resurrect save file so that
// sidebar-renderer, pane-header, and tabby-daemon panes are not restored
// as zombie shells on next resurrect-restore.
// Usage: tabby-hook resurrect-save <save-file>
func doResurrectSave(args []string) {
	if len(args) < 1 {
		return
	}
	saveFile := args[0]
	if saveFile == "" || !fileExists(saveFile) {
		return
	}

	data, err := os.ReadFile(saveFile)
	if err != nil {
		return
	}

	// Tabby utility process names that should never be restored.
	// macOS truncates to 15 chars: "sidebar-renderer" → "sidebar-rendere"
	tabbyProcs := []string{
		"sidebar-renderer", "sidebar-rendere", "sidebar",
		"tabby-daemon", "pane-header",
	}

	var filtered []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 10 && fields[0] == "pane" {
			cmd := fields[9]
			drop := false
			for _, proc := range tabbyProcs {
				if cmd == proc || strings.HasPrefix(cmd, proc) {
					drop = true
					break
				}
			}
			if drop {
				continue
			}
		}
		filtered = append(filtered, line)
	}

	os.WriteFile(saveFile, []byte(strings.Join(filtered, "\n")), 0644)
}

// doResurrectRestore replaces resurrect_restore_hook.sh:
// cleans stale Tabby state and re-initializes the sidebar after
// a tmux-resurrect restore.
// Usage: tabby-hook resurrect-restore
func doResurrectRestore(args []string) {
	// 1. Kill stale Tabby processes
	for _, proc := range []string{"tabby-daemon", "sidebar-renderer", "pane-header"} {
		exec.Command("pkill", "-f", proc).Run()
	}

	// 2. Reset mouse escape sequences on all client TTYs
	resetSeq := "\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1006l\033[?1015l"
	out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output()
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

	// 3. Clean runtime files
	patterns := []string{
		"/tmp/tabby-daemon-*.pid",
		"/tmp/tabby-daemon-*.sock",
		"/tmp/tabby-sidebar-*.state",
		"/tmp/tabby-daemon-*-events.log",
		"/tmp/tabby-daemon-*-input.log",
		"/tmp/tabby-ensure-debounce-*",
	}
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			os.Remove(m)
		}
	}

	// 4. Kill zombie Tabby panes
	paneOut, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_current_command}|#{pane_id}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		cmd := parts[0]
		if cmd == "sidebar-renderer" || cmd == "sidebar" || cmd == "tabby-daemon" || cmd == "pane-header" {
			exec.Command("tmux", "kill-pane", "-t", parts[1]).Run()
		}
	}

	// 5. Brief pause for tmux to settle
	time.Sleep(500 * time.Millisecond)

	// 6. Re-initialize based on saved mode
	mode, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar").Output()
	if strings.TrimSpace(string(mode)) == "enabled" {
		sessionID, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		windowID, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		sid := strings.TrimSpace(string(sessionID))
		wid := strings.TrimSpace(string(windowID))
		if sid != "" {
			go ensureSidebar(sid, wid)
		}
	}
}
