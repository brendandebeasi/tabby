// Package newwindow spawns a new tmux window, assigns it to a group, and
// starts a sidebar renderer pane if the sidebar is enabled.
// Exported as the `tabby new-window` subcommand.
package newwindow

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tmuxpkg "github.com/brendandebeasi/tabby/pkg/tmux"
)

type config struct {
	session   string
	group     string
	path      string
	clientTTY string
	noSidebar bool
	debug     bool
}

func Run(args []string) int {
	fs := flag.NewFlagSet("new-window", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfg := &config{}
	fs.StringVar(&cfg.session, "session", "", "target tmux session ID")
	fs.StringVar(&cfg.group, "group", "", "group name for the new window")
	fs.StringVar(&cfg.path, "path", "", "working directory for the new window")
	fs.StringVar(&cfg.clientTTY, "client-tty", "", "client TTY for multi-client focus")
	fs.BoolVar(&cfg.noSidebar, "no-sidebar", false, "skip sidebar creation (mobile/collapsed)")
	fs.BoolVar(&cfg.debug, "debug", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sidebarEnabled := readTmuxOption("@tabby_sidebar") == "enabled" && !cfg.noSidebar

	sessionID := strings.TrimSpace(cfg.session)
	if sessionID == "" {
		sessionID = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{session_id}")
	}
	if sessionID == "" {
		fmt.Fprintln(os.Stderr, "new-window: failed to determine session ID")
		return 1
	}

	group := strings.TrimSpace(cfg.group)
	if group == "" && strings.TrimSpace(cfg.clientTTY) != "" {
		group = readTmuxDisplayForClient(cfg, strings.TrimSpace(cfg.clientTTY), "#{@tabby_group}")
	}
	if group == "" {
		group = runTmuxTrimmedOrEmpty(cfg, "show-window-option", "-v", "@tabby_group")
	}

	windowPath := strings.TrimSpace(cfg.path)
	if windowPath == "" && strings.TrimSpace(cfg.clientTTY) != "" {
		windowPath = readTmuxDisplayForClient(cfg, strings.TrimSpace(cfg.clientTTY), "#{pane_current_path}")
	}
	if windowPath == "" {
		windowPath = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{pane_current_path}")
	}

	if _, err := runTmuxOutput(cfg, "set-option", "-g", "@tabby_spawning", "1"); err != nil {
		debugLog(cfg, "failed to set @tabby_spawning=1: %v", err)
	}
	spawnGuardSet := true

	defer func() {
		if spawnGuardSet {
			if _, err := runTmuxOutput(cfg, "set-option", "-gu", "@tabby_spawning"); err != nil {
				debugLog(cfg, "failed to clear @tabby_spawning: %v", err)
			}
		}
	}()

	tmuxArgs := []string{"new-window", "-P", "-F", "#{window_id}", "-t", sessionID + ":"}
	if windowPath != "" {
		tmuxArgs = append(tmuxArgs, "-c", windowPath)
	}
	newWindowID, err := runTmuxOutput(cfg, tmuxArgs...)
	newWindowID = firstMatchingToken(newWindowID, "@")
	if err != nil || newWindowID == "" {
		fmt.Fprintf(os.Stderr, "new-window: failed to create window: %v\n", err)
		return 1
	}

	if group != "" && group != "Default" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_group", group); err != nil {
			debugLog(cfg, "failed setting @tabby_group on %s: %v", newWindowID, err)
		}
	}

	firstPane := ""
	if sidebarEnabled {
		firstPane = firstPaneInWindow(cfg, newWindowID)
		if firstPane != "" {
			globalWidth := 25
			if w := readTmuxOptionInt("@tabby_sidebar_width"); w > 0 {
				globalWidth = w
			}
			// Match RunWidthSync / boundedSidebarWidthForWindow semantics.
			width := tmuxpkg.ResponsiveSidebarWidth(newWindowID, globalWidth)
			if globalWidth > 0 && globalWidth < width {
				width = globalWidth
			}

			position := readTmuxOption("@tabby_sidebar_position")
			if position == "" {
				position = "left"
			}

			// Self-exec: same tabby binary, `render sidebar` subcommand.
			exe, exeErr := os.Executable()
			if exeErr != nil {
				debugLog(cfg, "cannot resolve tabby executable: %v", exeErr)
			} else {
				debugArg := ""
				if cfg.debug {
					debugArg = "-debug"
				}
				// exec -a sidebar-renderer preserves the legacy argv[0] so
				// tmux's #{pane_current_command} still shows "sidebar-renderer"
				// for detection/dim logic that matches on process name.
				cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && exec -a sidebar-renderer '%s' render sidebar -session '%s' -window '%s' %s",
					exe, sessionID, newWindowID, debugArg)

				splitArgs := []string{"split-window", "-d", "-t", firstPane, "-h"}
				if position != "right" {
					splitArgs = append(splitArgs, "-b")
				}
				splitArgs = append(splitArgs,
					"-f", "-l", strconv.Itoa(width), "-P", "-F", "#{pane_id}", cmdStr,
				)
				rendererPaneID, splitErr := runTmuxOutput(cfg, splitArgs...)
				rendererPaneID = firstMatchingToken(rendererPaneID, "%")
				if splitErr != nil {
					debugLog(cfg, "split-window failed for %s: %v", newWindowID, splitErr)
				} else if rendererPaneID != "" {
					if _, err := runTmuxOutput(cfg, "set-option", "-p", "-t", rendererPaneID, "pane-border-status", "off"); err != nil {
						debugLog(cfg, "failed to disable pane-border-status on %s: %v", rendererPaneID, err)
					}
				}
			}
		}
	}

	if firstPane != "" {
		if _, err := runTmuxOutput(cfg, "select-pane", "-t", firstPane); err != nil {
			debugLog(cfg, "select-pane failed for first pane %s: %v", firstPane, err)
		}
		debugLog(cfg, "select-pane firstPane completed")
	} else {
		focusFirstContentPane(cfg, newWindowID)
		debugLog(cfg, "focusFirstContentPane completed")
	}
	_ = filepath.Dir // silence unused import when code paths change
	return 0
}

func readTmuxOption(name string) string {
	out, _ := exec.Command("tmux", "show-option", "-gqv", name).Output()
	return strings.TrimSpace(string(out))
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

func readTmuxDisplayForClient(cfg *config, clientTTY, format string) string {
	out := runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "-c", clientTTY, format)
	if strings.HasPrefix(out, "#{") && strings.HasSuffix(out, "}") {
		return ""
	}
	return out
}

func runTmuxTrimmedOrEmpty(cfg *config, args ...string) string {
	out, err := runTmuxOutput(cfg, args...)
	if err != nil {
		return ""
	}
	return out
}

func runTmuxOutput(cfg *config, args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		if cfg.debug {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			fmt.Fprintf(os.Stderr, "[new-window] tmux %s -> err=%v out=%q stderr=%q\n", strings.Join(args, " "), err, trimmed, stderr)
		}
		return trimmed, err
	}
	if cfg.debug {
		fmt.Fprintf(os.Stderr, "[new-window] tmux %s -> %q\n", strings.Join(args, " "), trimmed)
	}
	return trimmed, nil
}

func firstPaneInWindow(cfg *config, windowID string) string {
	out, err := runTmuxOutput(cfg, "list-panes", "-t", windowID, "-F", "#{pane_id}")
	if err != nil || out == "" {
		return ""
	}
	if paneID := firstMatchingToken(out, "%"); paneID != "" {
		return paneID
	}
	return ""
}

func focusFirstContentPane(cfg *config, windowID string) {
	out, err := runTmuxOutput(cfg, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
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
		if _, err := runTmuxOutput(cfg, "select-pane", "-t", paneID); err != nil {
			debugLog(cfg, "fallback select-pane failed for %s: %v", paneID, err)
		}
		return
	}

	if firstAny != "" {
		if _, err := runTmuxOutput(cfg, "select-pane", "-t", firstAny); err != nil {
			debugLog(cfg, "fallback select-pane(firstAny) failed for %s: %v", firstAny, err)
		}
	}
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

func debugLog(cfg *config, format string, a ...any) {
	if !cfg.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "[new-window] "+format+"\n", a...)
}
