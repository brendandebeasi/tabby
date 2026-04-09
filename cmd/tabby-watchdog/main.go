// tabby-watchdog supervises the tabby-daemon process.
// It restarts the daemon on unexpected exit (crash, OOM, etc.)
// and gives up after too many restarts within a time window.
//
// Usage: tabby-watchdog -session <session_id> [-debug]
package main

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

func main() {
	sessionID := ""
	debug := false
	daemonArgs := []string{}

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-session":
			if i+1 < len(os.Args) {
				i++
				sessionID = os.Args[i]
				daemonArgs = append(daemonArgs, "-session", sessionID)
			}
		case "-debug":
			debug = true
			daemonArgs = append(daemonArgs, "-debug")
		default:
			daemonArgs = append(daemonArgs, os.Args[i])
		}
	}

	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "watchdog: -session required")
		// Fall through to exec daemon directly
		exe, _ := os.Executable()
		daemonBin := filepath.Join(filepath.Dir(exe), "tabby-daemon")
		cmd := exec.Command(daemonBin, os.Args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
		return
	}

	_ = debug

	sentinel := fmt.Sprintf("/tmp/tabby-daemon-%s.clean-stop", sessionID)
	watchdogPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.watchdog.pid", sessionID)
	crashLog := fmt.Sprintf("/tmp/tabby-daemon-%s-crash.log", sessionID)
	daemonPidFile := fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)

	exe, _ := os.Executable()
	daemonBin := filepath.Join(filepath.Dir(exe), "tabby-daemon")
	crashHook := filepath.Join(filepath.Dir(exe), "..", "scripts", "crash-handler.sh")

	// Write our PID
	os.WriteFile(watchdogPidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
	defer os.Remove(watchdogPidFile)

	restartCount := 0
	windowStart := time.Now()

	for {
		os.Remove(sentinel)

		cmd := exec.Command(daemonBin, daemonArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()

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
			return
		}

		// Another daemon took over
		if pidData, err := os.ReadFile(daemonPidFile); err == nil {
			pid := strings.TrimSpace(string(pidData))
			if pid != "" {
				if exec.Command("kill", "-0", pid).Run() == nil {
					return
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
			os.Exit(1)
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
