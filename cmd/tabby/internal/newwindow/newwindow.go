// Package newwindow spawns a new tmux window, assigns it to a group, and
// starts a sidebar renderer pane if the sidebar is enabled.
// Exported as the `tabby new-window` subcommand.
package newwindow

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tabbycfg "github.com/brendandebeasi/tabby/pkg/config"
	daemonpkg "github.com/brendandebeasi/tabby/pkg/daemon"
	tmuxpkg "github.com/brendandebeasi/tabby/pkg/tmux"
)

type config struct {
	session   string
	group     string
	path      string
	color     string
	icon      string
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
	fs.StringVar(&cfg.color, "color", "", "@tabby_color to seed on the new window (e.g. inherited from an ssh parent)")
	fs.StringVar(&cfg.icon, "icon", "", "@tabby_icon to seed on the new window")
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

	// Group is only ever what the caller passed explicitly (-group, e.g. a
	// group's dedicated "+"). We deliberately do NOT inherit the active window's
	// @tabby_group here: a plain new tab (sidebar +, prefix-c, M-n) should be
	// grouped by WHERE it opens, not by which tab launched it. The daemon's
	// cwd->group preset (presetGroupForWindow) files it under the matching
	// group's working_dir on the next refresh, or leaves it in Default.
	group := strings.TrimSpace(cfg.group)
	color := strings.TrimSpace(cfg.color)
	icon := strings.TrimSpace(cfg.icon)

	windowPath := strings.TrimSpace(cfg.path)
	if windowPath == "" && strings.TrimSpace(cfg.clientTTY) != "" {
		windowPath = readTmuxDisplayForClient(cfg, strings.TrimSpace(cfg.clientTTY), "#{pane_current_path}")
	}
	if windowPath == "" {
		windowPath = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{pane_current_path}")
	}

	// Load config once (also reused for the native-borders check below).
	tcfg, _ := tabbycfg.LoadConfig(tabbycfg.DefaultConfigPath())

	// If the firing pane is currently in an ssh/mosh session, re-run that exact
	// connection in the new tab so it lands on the same host, and treat the parent
	// as remote so its decorations copy over. srcRemote is detected independently
	// of remoteCmd: the parent's foreground command being ssh/mosh is enough to
	// copy decorations even when the exact argv can't be captured to re-run (e.g.
	// ssh isn't reachable to actually reconnect). Gated by
	// sidebar.new_tab_inherit_ssh (default true).
	remoteCmd := ""
	srcRemote := false
	inheritSSH := tcfg == nil || tcfg.Sidebar.NewTabInheritSSH == nil || *tcfg.Sidebar.NewTabInheritSSH
	if inheritSSH {
		if panePID := firingPanePID(cfg); panePID > 0 {
			remoteCmd = tmuxpkg.RemoteCommandForPane(panePID)
		}
		srcRemote = remoteCmd != "" || firingPaneIsRemote(cfg)
	}

	// Copy the parent tab's decorations (group/color/icon) so the new tab shares
	// its visual identity — whenever the parent is a remote/ssh tab, even if the
	// exact ssh command couldn't be captured to re-run. Caller-supplied values
	// (the daemon "+" path passes -group/-color/-icon) always win. Read straight
	// off the firing window's tmux options, since this subcommand (prefix-c / M-n)
	// has no view of the daemon's in-memory appearance. The usual "grouped by where
	// it opens" rule still holds for a plain (non-remote) new tab.
	if srcRemote {
		if fw := firingWindowID(cfg); fw != "" {
			if group == "" {
				group = readTmuxWindowOption(fw, "@tabby_group")
			}
			if color == "" {
				color = readTmuxWindowOption(fw, "@tabby_color")
			}
			if icon == "" {
				icon = readTmuxWindowOption(fw, "@tabby_icon")
			}
		}
	}

	// Register this spawn with the daemon BEFORE creating the window. Unlike the
	// daemon's own "+"-click path (which sets the in-flight status in-process),
	// this subcommand runs in a SEPARATE process, so the daemon otherwise never
	// learns the firing client tty. Without it, the post-creation move-window
	// renumber shuffle drops tmux's active marker and the focus re-assert
	// (preferredWindowFocusTarget, gated on a "ready" status) is skipped — tmux's
	// fallback election then lands on the FIRST window instead of the new one.
	// Fire-and-forget: a down/absent daemon must never block window creation.
	sendDaemonHook(sessionID, "new-window-pending", map[string]string{
		"tty":   strings.TrimSpace(cfg.clientTTY),
		"group": group,
		"path":  windowPath,
	})

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

	// NOTE: the ssh/mosh re-run is NOT passed as new-window's shell-command. A
	// trailing "cmd; exec $SHELL" runs under a non-interactive `$SHELL -c`, which
	// gives ssh no controlling foreground process group of its own, so tmux reports
	// pane_current_command as the wrapper shell (e.g. "zsh"), not "ssh" — and the
	// daemon's remote detection (which gates on pane_current_command) never fires,
	// so the tab gets no ssh icon/host color. Instead we create a normal interactive
	// shell and send-keys the command into it below, exactly mirroring a hand-typed
	// ssh: ssh becomes the pane's foreground command and the tab returns to a shell
	// on disconnect.
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

	// Mark the window ready IMMEDIATELY (before the slower sidebar/pane work
	// below) so the daemon's in-flight status flips to "ready" ahead of the
	// reorder refresh — @tabby_spawning is still set here and gates that reorder
	// until this process's defer clears it, so "ready" is reliably in place when
	// preferredWindowFocusTarget runs and re-selects the new window.
	sendDaemonHook(sessionID, "new-window-ready", map[string]string{
		"window": newWindowID,
	})

	if group != "" && group != "Default" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_group", group); err != nil {
			debugLog(cfg, "failed setting @tabby_group on %s: %v", newWindowID, err)
		}
	}

	// Seed inherited appearance (from an ssh parent tab) so the new tab is born
	// with the host's look instead of flickering through the local launch dir's
	// identity until the daemon detects the ssh.
	if color != "" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_color", color); err != nil {
			debugLog(cfg, "failed setting @tabby_color on %s: %v", newWindowID, err)
		}
	}
	if icon != "" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_icon", icon); err != nil {
			debugLog(cfg, "failed setting @tabby_icon on %s: %v", newWindowID, err)
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
					// In native-borders mode the aux pane gets a blank label via
					// the pane-border-format conditional, so we don't need to
					// disable the strip per-pane. We MUST skip this set in
					// native mode anyway: tmux treats pane-border-status as
					// window-scope and the `-p` flag silently falls through, so
					// setting it "off" here would clobber the window-level
					// "top" set by applyNativeBorders and the border label
					// would vanish from every pane in the new window.
					nativeBorders := false
					if tcfg != nil && tcfg.PaneHeader.Native != nil {
						nativeBorders = *tcfg.PaneHeader.Native
					}
					if !nativeBorders {
						if _, err := runTmuxOutput(cfg, "set-option", "-p", "-t", rendererPaneID, "pane-border-status", "off"); err != nil {
							debugLog(cfg, "failed to disable pane-border-status on %s: %v", rendererPaneID, err)
						}
					}
				}
			}
		}
	}

	contentPane := firstPane
	if firstPane != "" {
		if _, err := runTmuxOutput(cfg, "select-pane", "-t", firstPane); err != nil {
			debugLog(cfg, "select-pane failed for first pane %s: %v", firstPane, err)
		}
		debugLog(cfg, "select-pane firstPane completed")
	} else {
		focusFirstContentPane(cfg, newWindowID)
		debugLog(cfg, "focusFirstContentPane completed")
		contentPane = firstPaneInWindow(cfg, newWindowID)
	}

	// Type the ssh/mosh command into the new tab's interactive shell — exactly as
	// if hand-typed — so ssh becomes the pane's foreground command (detected for
	// the ssh icon/host color) and the tab returns to a shell on disconnect.
	if remoteCmd != "" && contentPane != "" {
		sendCommandToPane(cfg, contentPane, remoteCmd)
	}
	_ = filepath.Dir // silence unused import when code paths change
	return 0
}

// sendCommandToPane types a shell command into a pane and presses Enter, the way
// a user would. -l sends the command literally (no key-name interpretation); the
// Enter is a separate, non-literal key. tmux buffers the input in the pane's pty,
// so the shell runs it once its prompt is ready even if still sourcing rc.
func sendCommandToPane(cfg *config, paneID, command string) {
	if _, err := runTmuxOutput(cfg, "send-keys", "-t", paneID, "-l", command); err != nil {
		debugLog(cfg, "send-keys (literal) failed for %s: %v", paneID, err)
		return
	}
	if _, err := runTmuxOutput(cfg, "send-keys", "-t", paneID, "Enter"); err != nil {
		debugLog(cfg, "send-keys Enter failed for %s: %v", paneID, err)
	}
}

// sendDaemonHook dials the daemon's unix socket and sends one MsgHook envelope,
// then disconnects. Mirrors the hook CLI's sendHook: short dial/write deadlines
// and every error is swallowed so a down or slow daemon can never delay (or
// fail) window creation — the hook is a best-effort focus-scoping signal, not a
// prerequisite. sessionID is the tmux #{session_id} (e.g. "$0").
func sendDaemonHook(sessionID, kind string, args map[string]string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	conn, err := net.DialTimeout("unix", daemonpkg.SocketPath(sessionID), 200*time.Millisecond)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	data, err := json.Marshal(daemonpkg.Message{
		Type:    daemonpkg.MsgHook,
		Payload: daemonpkg.HookPayload{Kind: kind, Args: args},
	})
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// firingPanePID returns the PID of the shell in the active pane of the client
// that fired the new-tab action (via -client-tty), or the daemon's own active
// pane as a fallback. Returns 0 if it can't be determined.
func firingPanePID(cfg *config) int {
	raw := ""
	if tty := strings.TrimSpace(cfg.clientTTY); tty != "" {
		raw = readTmuxDisplayForClient(cfg, tty, "#{pane_pid}")
	}
	if raw == "" {
		raw = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{pane_pid}")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return pid
}

// firingWindowID returns the window the new-tab action fired from: the active
// window of the client identified by -client-tty, or the server's current
// window as a fallback. Empty if it can't be resolved.
func firingWindowID(cfg *config) string {
	if tty := strings.TrimSpace(cfg.clientTTY); tty != "" {
		if w := readTmuxDisplayForClient(cfg, tty, "#{window_id}"); w != "" {
			return w
		}
	}
	return runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{window_id}")
}

// firingPaneIsRemote reports whether the firing client's active pane is running
// a remote connection, by matching its foreground command against the same set
// the daemon uses. This is the decoration-copy signal that does NOT depend on
// capturing the ssh argv for re-run.
func firingPaneIsRemote(cfg *config) bool {
	cmd := ""
	if tty := strings.TrimSpace(cfg.clientTTY); tty != "" {
		cmd = readTmuxDisplayForClient(cfg, tty, "#{pane_current_command}")
	}
	if cmd == "" {
		cmd = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{pane_current_command}")
	}
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "ssh", "mosh", "mosh-client", "telnet":
		return true
	}
	return false
}

// readTmuxWindowOption returns a window-scoped tmux option value, or "" if unset.
func readTmuxWindowOption(windowID, name string) string {
	out, err := exec.Command("tmux", "show-options", "-wqv", "-t", windowID, name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
