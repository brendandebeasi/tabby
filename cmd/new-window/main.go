package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	tmuxpkg "github.com/brendandebeasi/tabby/pkg/tmux"
)

var (
	flagSession   = flag.String("session", "", "target tmux session ID")
	flagGroup     = flag.String("group", "", "group name for the new window")
	flagPath      = flag.String("path", "", "working directory for the new window")
	flagClientTTY = flag.String("client-tty", "", "client TTY for multi-client focus")
	flagNoSidebar = flag.Bool("no-sidebar", false, "skip sidebar creation (mobile/collapsed)")
	flagDebug     = flag.Bool("debug", false, "enable debug logging")
)

func main() {
	flag.Parse()

	sidebarEnabled := readTmuxOption("@tabby_sidebar") == "enabled" && !*flagNoSidebar

	sessionID := strings.TrimSpace(*flagSession)
	if sessionID == "" {
		sessionID = readSessionID()
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "new-window: failed to determine session ID")
		os.Exit(1)
	}

	group := strings.TrimSpace(*flagGroup)
	if group == "" && strings.TrimSpace(*flagClientTTY) != "" {
		group = readTmuxDisplayForClient(strings.TrimSpace(*flagClientTTY), "#{@tabby_group}")
	}
	if group == "" {
		group = runTmuxTrimmedOrEmpty("show-window-option", "-v", "@tabby_group")
	}

	windowPath := strings.TrimSpace(*flagPath)
	if windowPath == "" && strings.TrimSpace(*flagClientTTY) != "" {
		windowPath = readTmuxDisplayForClient(strings.TrimSpace(*flagClientTTY), "#{pane_current_path}")
	}
	if windowPath == "" {
		windowPath = runTmuxTrimmedOrEmpty("display-message", "-p", "#{pane_current_path}")
	}

	if _, err := runTmuxOutput("set-option", "-g", "@tabby_spawning", "1"); err != nil {
		debugLog("failed to set @tabby_spawning=1: %v", err)
	}
	spawnGuardSet := true

	defer func() {
		if spawnGuardSet {
			if _, err := runTmuxOutput("set-option", "-gu", "@tabby_spawning"); err != nil {
				debugLog("failed to clear @tabby_spawning: %v", err)
			}
		}
	}()

	args := []string{"new-window", "-P", "-F", "#{window_id}", "-t", sessionID + ":"}
	if windowPath != "" {
		args = append(args, "-c", windowPath)
	}
	newWindowID, err := runTmuxOutput(args...)
	newWindowID = firstMatchingToken(newWindowID, "@")
	if err != nil || newWindowID == "" {
		fmt.Fprintf(os.Stderr, "new-window: failed to create window: %v\n", err)
		os.Exit(1)
	}

	if group != "" && group != "Default" {
		if _, err := runTmuxOutput("set-window-option", "-t", newWindowID, "@tabby_group", group); err != nil {
			debugLog("failed setting @tabby_group on %s: %v", newWindowID, err)
		}
	}

	firstPane := ""
	if sidebarEnabled {
		firstPane = firstPaneInWindow(newWindowID)
		if firstPane != "" {
			globalWidth := 25
			if w := readTmuxOptionInt("@tabby_sidebar_width"); w > 0 {
				globalWidth = w
			}
			// Match RunWidthSync / boundedSidebarWidthForWindow semantics:
			// the tier width is a cap, the global is the "requested" width.
			// Without this cap, the sidebar spawns at tier (e.g. 25) and is
			// then immediately resized to globalWidth (e.g. 15), producing a
			// visible one-frame jump on window creation.
			width := tmuxpkg.ResponsiveSidebarWidth(newWindowID, globalWidth)
			if globalWidth > 0 && globalWidth < width {
				width = globalWidth
			}

			position := readTmuxOption("@tabby_sidebar_position")
			if position == "" {
				position = "left"
			}

			exe, exeErr := os.Executable()
			rendererBin := ""
			if exeErr == nil {
				rendererBin = filepath.Join(filepath.Dir(exe), "sidebar-renderer")
			}
			if rendererBin == "" {
				debugLog("renderer binary path resolution failed")
			} else {
				debugArg := ""
				if *flagDebug {
					debugArg = "-debug"
				}
				cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && exec '%s' -session '%s' -window '%s' %s",
					rendererBin, sessionID, newWindowID, debugArg)

				splitArgs := []string{"split-window", "-d", "-t", firstPane, "-h"}
				if position != "right" {
					splitArgs = append(splitArgs, "-b")
				}
				splitArgs = append(splitArgs,
					"-f", "-l", strconv.Itoa(width), "-P", "-F", "#{pane_id}", cmdStr,
				)
				rendererPaneID, splitErr := runTmuxOutput(splitArgs...)
				rendererPaneID = firstMatchingToken(rendererPaneID, "%")
				if splitErr != nil {
					debugLog("split-window failed for %s: %v", newWindowID, splitErr)
				} else if rendererPaneID != "" {
					if _, err := runTmuxOutput("set-option", "-p", "-t", rendererPaneID, "pane-border-status", "off"); err != nil {
						debugLog("failed to disable pane-border-status on %s: %v", rendererPaneID, err)
					}
				}
			}
		}
	}

	if firstPane != "" {
		if _, err := runTmuxOutput("select-pane", "-t", firstPane); err != nil {
			debugLog("select-pane failed for first pane %s: %v", firstPane, err)
		}
		debugLog("select-pane firstPane completed")
	} else {
		focusFirstContentPane(newWindowID)
		debugLog("focusFirstContentPane completed")
	}

	fmt.Println(newWindowID)

	// SIGWINCH broadcast removed: tmux sends SIGWINCH to all panes automatically
	// during its own reflow, so an explicit broadcast here causes extra resize churn.
}

func readTmuxOption(name string) string {
	return runTmuxTrimmedOrEmpty("show-option", "-gqv", name)
}

func readTmuxOptionInt(name string) int {
	v := strings.TrimSpace(readTmuxOption(name))
	if v == "" {
		return 0
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}

func readSessionID() string {
	return runTmuxTrimmedOrEmpty("display-message", "-p", "#{session_id}")
}

func readTmuxDisplayForClient(clientTTY, format string) string {
	out := runTmuxTrimmedOrEmpty("display-message", "-p", "-c", clientTTY, format)
	if strings.HasPrefix(out, "#{") && strings.HasSuffix(out, "}") {
		return ""
	}
	return out
}

func runTmuxTrimmedOrEmpty(args ...string) string {
	out, err := runTmuxOutput(args...)
	if err != nil {
		return ""
	}
	return out
}

func runTmuxOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		if *flagDebug {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			fmt.Fprintf(os.Stderr, "[new-window] tmux %s -> err=%v out=%q stderr=%q\n", strings.Join(args, " "), err, trimmed, stderr)
		}
		return trimmed, err
	}
	if *flagDebug {
		fmt.Fprintf(os.Stderr, "[new-window] tmux %s -> %q\n", strings.Join(args, " "), trimmed)
	}
	return trimmed, nil
}

func firstPaneInWindow(windowID string) string {
	out, err := runTmuxOutput("list-panes", "-t", windowID, "-F", "#{pane_id}")
	if err != nil || out == "" {
		return ""
	}
	if paneID := firstMatchingToken(out, "%"); paneID != "" {
		return paneID
	}
	return ""
}

func focusFirstContentPane(windowID string) {
	out, err := runTmuxOutput("list-panes", "-t", windowID, "-F", "#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
	if err != nil || out == "" {
		return
	}

	firstAny := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		paneID := strings.TrimSpace(parts[0])
		if paneID == "" {
			continue
		}
		if firstAny == "" {
			firstAny = paneID
		}

		curCmd := ""
		startCmd := ""
		if len(parts) > 1 {
			curCmd = strings.ToLower(parts[1])
		}
		if len(parts) > 2 {
			startCmd = strings.ToLower(parts[2])
		}
		if strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") ||
			strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") {
			continue
		}
		if _, err := runTmuxOutput("select-pane", "-t", paneID); err != nil {
			debugLog("fallback select-pane failed for %s: %v", paneID, err)
		}
		return
	}

	if firstAny != "" {
		if _, err := runTmuxOutput("select-pane", "-t", firstAny); err != nil {
			debugLog("fallback select-pane(firstAny) failed for %s: %v", firstAny, err)
		}
	}
}

func sendWinchToContentPanes(windowID string) {
	out, err := runTmuxOutput("list-panes", "-t", windowID, "-F", "#{pane_id}\t#{pane_pid}\t#{pane_start_command}\t#{pane_current_command}")
	if err != nil || out == "" {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		startCmd := strings.ToLower(parts[2])
		curCmd := strings.ToLower(parts[3])
		if strings.Contains(startCmd, "sidebar") || strings.Contains(startCmd, "renderer") ||
			strings.Contains(curCmd, "sidebar") || strings.Contains(curCmd, "renderer") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || pid <= 0 {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGWINCH); err != nil {
			debugLog("failed SIGWINCH pid=%d pane=%s: %v", pid, strings.TrimSpace(parts[0]), err)
		}
	}
}

func shSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func pickContentPane(windowID string) string {
	out, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
		"#{pane_id}|#{pane_current_command}|#{pane_start_command}|#{pane_active}").Output()
	var first string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		pID, cmd, startCmd, active := parts[0], parts[1], parts[2], parts[3]
		combined := strings.ToLower(cmd + "|" + startCmd)
		if strings.Contains(combined, "sidebar") || strings.Contains(combined, "renderer") ||
			strings.Contains(combined, "pane-header") || strings.Contains(combined, "tabby-daemon") {
			continue
		}
		if active == "1" {
			return pID
		}
		if first == "" {
			first = pID
		}
	}
	return first
}

func firstMatchingToken(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			fields := strings.Fields(line)
			if len(fields) > 0 && strings.HasPrefix(fields[0], prefix) {
				return fields[0]
			}
		}
	}
	return ""
}

func debugLog(format string, a ...any) {
	if !*flagDebug {
		return
	}
	fmt.Fprintf(os.Stderr, "[new-window] "+format+"\n", a...)
}
