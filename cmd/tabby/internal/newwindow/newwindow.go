// Package newwindow spawns a new tmux window, assigns it to a group, and
// starts a sidebar renderer pane if the sidebar is enabled.
// Exported as the `tabby new-window` subcommand.
package newwindow

import (
	"context"
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
	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	tmuxpkg "github.com/brendandebeasi/tabby/pkg/tmux"
)

type config struct {
	session   string
	group     string
	path      string
	color     string
	icon      string
	after     string
	clientTTY string
	noSidebar bool
	printID   bool
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
	fs.StringVar(&cfg.after, "after", "", "window ID to insert the new window after")
	fs.StringVar(&cfg.clientTTY, "client-tty", "", "client TTY for multi-client focus")
	fs.BoolVar(&cfg.noSidebar, "no-sidebar", false, "skip sidebar creation (mobile/collapsed)")
	fs.BoolVar(&cfg.printID, "print-id", false, "print created window ID to stdout")
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

	afterWindowID := strings.TrimSpace(cfg.after)
	if afterWindowID == "" {
		afterWindowID = firingWindowID(cfg)
	}
	if i := strings.IndexByte(afterWindowID, '#'); i >= 0 {
		afterWindowID = afterWindowID[:i]
	}

	group := strings.TrimSpace(cfg.group)
	color := strings.TrimSpace(cfg.color)
	icon := strings.TrimSpace(cfg.icon)

	windowPath := strings.TrimSpace(cfg.path)
	if windowPath == "" && strings.TrimSpace(cfg.clientTTY) != "" {
		windowPath = readTmuxDisplayForClient(cfg, strings.TrimSpace(cfg.clientTTY), "#{pane_current_path}")
	}
	if windowPath == "" && afterWindowID != "" {
		windowPath = runTmuxTrimmedOrEmpty(cfg, "display-message", "-t", afterWindowID, "-p", "#{pane_current_path}")
	}
	if windowPath == "" {
		windowPath = runTmuxTrimmedOrEmpty(cfg, "display-message", "-p", "#{pane_current_path}")
	}

	// Load config once (also reused for the native-borders check below).
	tcfg, _ := tabbycfg.LoadConfig(tabbycfg.DefaultConfigPath())

	// Resolve group and style: match configured working_dir if in a mapped folder,
	// otherwise use Default group/style.
	if group == "" {
		if dg := resolveDirGroup(windowPath, tcfg); dg != nil {
			group = dg.Name
			if color == "" && strings.TrimSpace(dg.Theme.Bg) != "" {
				color = strings.TrimSpace(dg.Theme.Bg)
			}
			if icon == "" && strings.TrimSpace(dg.Theme.Icon) != "" {
				icon = strings.TrimSpace(dg.Theme.Icon)
			}
		} else {
			group = "Default"
			if color == "" {
				color = resolveDirColor(windowPath, tcfg)
			}
		}
	} else if color == "" {
		color = resolveDirColor(windowPath, tcfg)
	}

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

	if srcRemote {
		if fw := firingWindowID(cfg); fw != "" {
			if icon == "" {
				icon = readTmuxWindowOption(fw, "@tabby_icon")
			}
		}
	}

	// Register this spawn with the daemon BEFORE creating the window.
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

	tmuxArgs := []string{"new-window", "-P", "-F", "#{window_id}"}
	if afterWindowID != "" {
		tmuxArgs = append(tmuxArgs, "-a", "-t", afterWindowID)
	} else {
		tmuxArgs = append(tmuxArgs, "-t", sessionID+":")
	}
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

	if group != "" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_group", group); err != nil {
			debugLog(cfg, "failed setting @tabby_group on %s: %v", newWindowID, err)
		}
	}

	if color != "" {
		if _, err := runTmuxOutput(cfg, "set-window-option", "-t", newWindowID, "@tabby_color", color); err != nil {
			debugLog(cfg, "failed setting @tabby_color on %s: %v", newWindowID, err)
		}
		_ = runTmuxTrimmedOrEmpty(cfg, "set-window-option", "-t", newWindowID, "@tabby_color_seeded", "1")
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
	// The landing command goes through this same send-keys path for the same
	// reason: whatever it picks must run in the pane's own interactive shell, or
	// tmux reports the wrapper shell as pane_current_command and the daemon
	// loses remote detection. eval keeps a chosen `cd` in this shell rather than
	// a subshell that exits immediately.
	if cmdToType := NewTabCommand(tcfg, remoteCmd); cmdToType != "" && contentPane != "" {
		sendCommandToPane(cfg, contentPane, cmdToType)
	}
	if cfg.printID {
		fmt.Println(newWindowID)
	}
	_ = filepath.Dir // silence unused import when code paths change
	return 0
}

// NewTabCommand is what a freshly created tab should type into its own shell:
// the command inherited from a remote parent if there is one, otherwise the
// landing command when landing is on, otherwise nothing at all — a bare prompt,
// which is what a new tab has always given.
//
// The inherited command wins on purpose. A tab spawned off a remote one is
// asking for that host, not for a chance to pick another.
func NewTabCommand(tcfg *tabbycfg.Config, remoteCmd string) string {
	if remoteCmd != "" {
		return remoteCmd
	}
	if LandingEnabled(tcfg) {
		return LandingCommand(tcfg)
	}
	return ""
}

// LandingCommand is what a new tab types when it opens on a launcher.
//
// landing.command names the launcher; empty means tabby's own. A configured
// value is a shell command line and is sent verbatim — it is not quoted or
// wrapped, because quoting it would break the eval it almost certainly needs,
// and the shell that receives it is the one that has to do the word splitting.
// A value naming something absent or non-executable costs one "command not
// found" and leaves the prompt it was typed at, which is a usable shell.
//
// For tabby's own launcher: it writes the chosen command to stdout and draws
// its UI on stderr, so eval runs the choice in this shell. A failed or
// cancelled launcher prints nothing, which evals to a no-op. It resolves the
// running binary rather than trusting PATH, so a tabby that was started by
// absolute path still finds itself, and falls back to a bare name when the
// executable cannot be resolved.
func LandingCommand(tcfg *tabbycfg.Config) string {
	if tcfg != nil {
		if custom := strings.TrimSpace(tcfg.Landing.Command); custom != "" {
			return custom
		}
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "tabby"
	}
	return `eval "$(` + shellQuote(exe) + ` landing)"`
}

// shellQuote wraps s in single quotes, escaping any it contains, so a path with
// spaces survives the shell that types it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LandingEnabled reports whether new windows should open on the launcher.
// Absent config means off: a new tab that lands somewhere unexpected is worse
// than one that does not.
func LandingEnabled(tcfg *tabbycfg.Config) bool {
	return tcfg != nil && tcfg.Landing.Enabled != nil && *tcfg.Landing.Enabled
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
	out, err := tmux.Cmd("show-options", "-wqv", "-t", windowID, name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func readTmuxOption(name string) string {
	out, _ := tmux.Cmd("show-option", "-gqv", name).Output()
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
	cmd := tmux.Cmd(args...)
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

func resolveDirGroup(dir string, tcfg *tabbycfg.Config) *tabbycfg.Group {
	dir = strings.TrimSpace(dir)
	if dir == "" || tcfg == nil {
		return nil
	}
	dir = filepath.Clean(dir)

	var bestGroup *tabbycfg.Group
	bestLen := -1
	for i := range tcfg.Groups {
		g := &tcfg.Groups[i]
		if g.Name == "" || g.Name == "Default" {
			continue
		}
		wdir := expandWorkingDir(g.WorkingDir)
		if wdir == "" {
			continue
		}
		if dir == wdir || strings.HasPrefix(dir, wdir+string(filepath.Separator)) {
			if len(wdir) > bestLen {
				bestLen = len(wdir)
				bestGroup = g
			}
		}
	}
	return bestGroup
}

func resolveDirColor(dir string, tcfg *tabbycfg.Config) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)

	// 1. Check ~/.local/state/tabby/cwd-colors.json
	statePath := paths.StatePath("cwd-colors.json")
	if data, err := os.ReadFile(statePath); err == nil {
		var colorsMap map[string]struct {
			Color string `json:"color"`
		}
		if err := json.Unmarshal(data, &colorsMap); err == nil {
			top := gitToplevel(dir)
			if top != "" {
				if entry, ok := colorsMap[filepath.Clean(top)]; ok && strings.TrimSpace(entry.Color) != "" {
					return strings.TrimSpace(entry.Color)
				}
			}
			if entry, ok := colorsMap[dir]; ok && strings.TrimSpace(entry.Color) != "" {
				return strings.TrimSpace(entry.Color)
			}
		}
	}

	// 2. Check configured groups matching working_dir
	if tcfg != nil {
		bestLen := -1
		bestColor := ""
		for _, g := range tcfg.Groups {
			if g.Name == "" || g.Name == "Default" || strings.TrimSpace(g.Theme.Bg) == "" {
				continue
			}
			wdir := expandWorkingDir(g.WorkingDir)
			if wdir == "" {
				continue
			}
			if dir == wdir || strings.HasPrefix(dir, wdir+string(filepath.Separator)) {
				if len(wdir) > bestLen {
					bestLen = len(wdir)
					bestColor = strings.TrimSpace(g.Theme.Bg)
				}
			}
		}
		if bestColor != "" {
			return bestColor
		}
	}

	return ""
}

func gitToplevel(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}

func expandWorkingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if dir == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Clean(home)
		}
		return ""
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Clean(filepath.Join(home, dir[2:]))
		}
	}
	return filepath.Clean(dir)
}
