// tabby-dev provides developer workflow commands.
// Replaces dev-reload.sh and dev-status.sh.
//
// Usage: tabby-dev <reload|status> [args...]
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tabby-dev <reload|status> [args...]")
		os.Exit(1)
	}

	exe, _ := os.Executable()
	binDir := filepath.Dir(exe)
	baseDir := filepath.Dir(binDir)

	switch os.Args[1] {
	case "reload":
		doReload(baseDir, binDir)
	case "status":
		sessionArg := ""
		if len(os.Args) > 2 {
			sessionArg = os.Args[2]
		}
		doStatus(baseDir, binDir, sessionArg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// doReload replaces dev-reload.sh: rebuild binaries and restart sidebar if enabled.
func doReload(baseDir, binDir string) {
	// Check if dev reload is enabled (opt-in)
	enabled := os.Getenv("TABBY_DEV_RELOAD")
	if enabled == "" {
		out, _ := exec.Command("tmux", "show-option", "-gqv", "@tabby_dev_reload_enabled").Output()
		enabled = strings.TrimSpace(string(out))
	}
	if enabled != "1" && enabled != "true" {
		tmuxMsg("Tabby: dev reload disabled (@tabby_dev_reload_enabled 1)", 3000)
		os.Exit(0)
	}

	// Get current sidebar state before rebuild
	sessionID := tmuxGet("display-message", "-p", "#{session_id}")
	sidebarState, _ := exec.Command("tmux", "show-options", "-qv", "@tabby_sidebar").Output()
	state := strings.TrimSpace(string(sidebarState))

	// Rebuild binaries
	tmuxMsg("Tabby: rebuilding binaries...", 2000)
	installScript := filepath.Join(baseDir, "scripts", "install.sh")
	if err := exec.Command(installScript).Run(); err != nil {
		tmuxMsg("Tabby: build failed (see scripts/install.sh)", 3000)
		os.Exit(1)
	}
	tmuxMsg("Tabby: build complete", 2000)

	if state == "enabled" {
		fmt.Println("Tabby: restarting sidebar...")

		// Kill daemon if running
		pidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)
		killFromPidFile(pidFile)
		os.Remove(pidFile)

		// Gracefully stop renderers
		gracefulKillAuxPanes()
		time.Sleep(500 * time.Millisecond)
		killAuxPanes()

		// Reset mouse state
		run("tmux", "set", "-g", "mouse", "off")
		time.Sleep(100 * time.Millisecond)
		run("tmux", "set", "-g", "mouse", "on")

		time.Sleep(500 * time.Millisecond)

		// Toggle off then on
		toggleBin := filepath.Join(binDir, "tabby-toggle")
		if err := exec.Command(toggleBin).Run(); err != nil {
			tmuxMsg("Tabby: reload failed (toggle step 1)", 4000)
			os.Exit(1)
		}
		if err := exec.Command(toggleBin).Run(); err != nil {
			tmuxMsg("Tabby: reload failed (toggle step 2)", 4000)
			os.Exit(1)
		}

		// Verify daemon freshness
		statusBin := os.Args[0] // self
		out, err := exec.Command(statusBin, "status", sessionID).CombinedOutput()
		if err != nil {
			tmuxMsg("Tabby: reload failed (stale runtime)", 5000)
			fmt.Print(string(out))
			os.Exit(1)
		}

		// Restore focus
		hookBin := filepath.Join(binDir, "tabby-hook")
		exec.Command(hookBin, "restore-input-focus", sessionID).Run()

		tmuxMsg("Tabby: reload complete", 2000)
	} else {
		hookBin := filepath.Join(binDir, "tabby-hook")
		exec.Command(hookBin, "restore-input-focus", sessionID).Run()
		tmuxMsg("Tabby: rebuild complete (sidebar disabled)", 2000)
	}
}

// doStatus replaces dev-status.sh: show whether the running daemon matches the latest built binary.
func doStatus(baseDir, binDir, sessionArg string) {
	daemonBin := filepath.Join(binDir, "tabby-daemon")

	if !fileExists(daemonBin) {
		fmt.Printf("missing daemon binary: %s\nbuild first: ./scripts/install.sh\n", daemonBin)
		os.Exit(1)
	}

	sessionID, sessionName := resolveSession(sessionArg)
	if sessionID == "" {
		fmt.Println("no tmux session found")
		os.Exit(1)
	}

	if sessionName == "" || sessionName == sessionID {
		out, _ := exec.Command("tmux", "display-message", "-p", "-t", sessionID, "#{session_name}").Output()
		sessionName = strings.TrimSpace(string(out))
	}

	pidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)
	sockFile := fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)

	binInfo, err := os.Stat(daemonBin)
	if err != nil {
		fmt.Println("cannot stat daemon binary")
		os.Exit(1)
	}
	binMtime := binInfo.ModTime()

	fmt.Println("Tabby Runtime Status")
	fmt.Printf("session: %s (%s)\n", sessionName, sessionID)
	fmt.Printf("binary:  %s\n", daemonBin)
	fmt.Printf("built:   %s\n", binMtime.Format("2006-01-02 15:04:05"))

	fixHint := "fix:     ./bin/tabby-toggle && ./bin/tabby-toggle"

	if !fileExists(pidFile) {
		fmt.Printf("daemon:  stopped (pid file missing: %s)\n", pidFile)
		fmt.Println("status:  STALE")
		fmt.Println(fixHint)
		os.Exit(1)
	}

	pidData, _ := os.ReadFile(pidFile)
	daemonPID := strings.TrimSpace(string(pidData))
	if daemonPID == "" || exec.Command("ps", "-p", daemonPID).Run() != nil {
		fmt.Printf("daemon:  stopped (stale pid file: %s)\n", pidFile)
		fmt.Println("status:  STALE")
		fmt.Println(fixHint)
		os.Exit(1)
	}

	cmdOut, _ := exec.Command("ps", "-p", daemonPID, "-o", "command=").Output()
	runningCmd := strings.TrimSpace(string(cmdOut))

	pidInfo, _ := os.Stat(pidFile)
	pidMtime := pidInfo.ModTime()

	sockStatus := "no"
	if fi, err := os.Stat(sockFile); err == nil && fi.Mode()&os.ModeSocket != 0 {
		sockStatus = "yes"
	}

	fmt.Printf("daemon:  running pid=%s\n", daemonPID)
	fmt.Printf("started: %s (pid file)\n", pidMtime.Format("2006-01-02 15:04:05"))
	fmt.Printf("socket:  %s (%s)\n", sockStatus, sockFile)
	fmt.Printf("cmd:     %s\n", runningCmd)

	fresh := true
	if pidMtime.Before(binMtime) {
		fresh = false
	}
	expected := daemonBin + " -session " + sessionID
	if !strings.Contains(runningCmd, expected) {
		fresh = false
	}

	if fresh {
		fmt.Println("status:  FRESH (running latest build)")
		os.Exit(0)
	}

	fmt.Println("status:  STALE (daemon older than current build or mismatched binary)")
	fmt.Println(fixHint)
	os.Exit(1)
}

// resolveSession resolves a session target to ID and name.
func resolveSession(target string) (string, string) {
	if target != "" {
		if strings.HasPrefix(target, "$") {
			name, _ := exec.Command("tmux", "display-message", "-p", "-t", target, "#{session_name}").Output()
			return target, strings.TrimSpace(string(name))
		}
		id, _ := exec.Command("tmux", "display-message", "-p", "-t", target, "#{session_id}").Output()
		return strings.TrimSpace(string(id)), target
	}

	id, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	name, _ := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	sid := strings.TrimSpace(string(id))
	sname := strings.TrimSpace(string(name))

	if sid == "" {
		out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_id} #{session_name}").Output()
		first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
		if len(first) > 0 {
			parts := strings.SplitN(first[0], " ", 2)
			if len(parts) == 2 {
				return parts[0], parts[1]
			}
		}
	}
	return sid, sname
}

// ── Helpers ─────────────────────────────────────────────────────────────

func run(args ...string) {
	exec.Command(args[0], args[1:]...).Run()
}

func tmuxGet(args ...string) string {
	out, _ := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out))
}

func tmuxMsg(msg string, durationMs int) {
	exec.Command("tmux", "display-message", "-d", fmt.Sprintf("%d", durationMs), msg).Run()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	time.Sleep(100 * time.Millisecond)
}

func killAuxPanes() {
	out, _ := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_current_command}|#{pane_id}").Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		cmd := strings.ToLower(parts[0])
		if strings.HasPrefix(cmd, "sidebar") || strings.HasPrefix(cmd, "pane-header") || strings.HasPrefix(cmd, "tabby-daemon") {
			exec.Command("tmux", "kill-pane", "-t", parts[1]).Run()
		}
	}
}
