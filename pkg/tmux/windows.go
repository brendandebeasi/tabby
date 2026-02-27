package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/perf"
)

// ansiEscapeRegex matches ANSI escape sequences including:
// - Standard CSI sequences: \x1b[...m (colors, styles)
// - OSC sequences: \x1b]...BEL or \x1b]...\x1b\ (titles, etc)
// - Partial/orphaned CSI: \[[0-9;]+[a-zA-Z] (missing ESC char)
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]|\x1b\].*?(?:\x07|\x1b\\)|\[[0-9;:]+[a-zA-Z]`)

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

type Pane struct {
	ID           string
	Index        int
	Active       bool
	Command      string // Current command running in pane
	StartCommand string // Initial command pane was started with
	Title        string // Pane title if set
	LockedTitle  string // Locked title that won't be overwritten (from @tabby_pane_title)
	Busy         bool   // Pane has a foreground process (not shell)
	AIBusy       bool   // AI tool in this pane is actively working
	AIInput      bool   // AI tool in this pane is waiting for user input
	Remote       bool   // Pane is running a remote connection (ssh, mosh, etc.)
	Top          int    // Y position of pane in window layout (for visual ordering)
	Width        int    // Pane width
	Height       int    // Pane height
	CurrentPath  string // Current working directory of pane
	LastActivity int64  // Unix timestamp of last pane output (for idle detection)
	PID          int    // Process ID of the shell in this pane
	Collapsed    bool   // Pane is collapsed to header only
}

// idleCommands are processes that indicate "idle" state (not busy)
// Includes shells and long-running daemon-like processes
var idleCommands = map[string]bool{
	// Shells
	"bash": true, "zsh": true, "fish": true, "sh": true, "dash": true,
	"tcsh": true, "csh": true, "ksh": true, "ash": true,
	// Long-running processes that are often idle
	"node": true, "python": true, "python3": true, "python3.11": true, "python3.12": true,
	"ruby": true, "nvim": true, "vim": true, "emacs": true,
	// Remote connections - busy state detected via activity instead
	"ssh": true, "mosh": true, "mosh-client": true, "telnet": true,
}

// aiToolCommands are interactive AI/coding tools that have distinct
// "working" (busy) and "waiting for input" states. Configured from config.yaml.
var aiToolCommands = map[string]bool{}

// aiIdleTimeout is how many seconds of no pane output before an AI tool
// is considered "waiting for input" rather than "busy working".
var aiIdleTimeout int64 = 10

var sessionTarget string

// SetSessionTarget scopes tmux queries to a specific session.
func SetSessionTarget(sessionID string) {
	sessionTarget = strings.TrimSpace(sessionID)
}

// ConfigureBusyDetection applies user config to idle/busy detection.
// extraIdle adds commands to the idle list.
// aiTools lists interactive AI tools that distinguish busy vs waiting-for-input.
// idleTimeout is seconds of no output before an AI tool is considered idle.
func ConfigureBusyDetection(extraIdle, aiTools []string, idleTimeout int) {
	for _, cmd := range extraIdle {
		idleCommands[cmd] = true
	}
	aiToolCommands = make(map[string]bool, len(aiTools))
	for _, cmd := range aiTools {
		aiToolCommands[cmd] = true
	}
	if idleTimeout > 0 {
		aiIdleTimeout = int64(idleTimeout)
	}
}

// semverRegex matches version-number process names like "2.1.17" (Claude Code).
// Claude Code sets its process title to its semver version.
var semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// IsAITool returns true if the command is a configured AI tool.
// Also detects Claude Code which sets its process name to a semver version.
func IsAITool(command string) bool {
	if aiToolCommands[command] {
		return true
	}
	// Claude Code uses its version number as the process title (e.g., "2.1.17")
	return semverRegex.MatchString(command)
}

// HasSpinner returns true if the title starts with a braille pattern dot (U+2800-U+28FF),
// which AI tools like Claude Code use as a working/thinking spinner.
// Note: ✳ (U+2733) is Claude Code's idle icon, NOT a spinner.
func HasSpinner(title string) bool {
	for _, r := range title {
		// Braille patterns U+2800-U+28FF (excluding U+2800 which is blank)
		return r >= 0x2801 && r <= 0x28FF
	}
	return false
}

// HasIdleIcon returns true if the title starts with ✳ (U+2733),
// which Claude Code uses as its explicit idle/waiting-for-input icon.
func HasIdleIcon(title string) bool {
	for _, r := range title {
		return r == 0x2733
	}
	return false
}

// AIIdleTimeout returns the configured idle timeout in seconds.
func AIIdleTimeout() int64 {
	return aiIdleTimeout
}

// remoteCommands are processes that connect to remote systems
// For these, we detect "busy" based on recent activity rather than just running
var remoteCommands = map[string]bool{
	"ssh": true, "mosh": true, "mosh-client": true, "telnet": true,
}

// sidebarCommands are commands that should be filtered from pane lists
var sidebarCommands = map[string]bool{
	"sidebar": true, "tabbar": true, "sidebar-master": true, "sidebar-shadow": true,
	"tabby-daemon": true, "sidebar-renderer": true, "render-status": true,
	"pane-header": true,
}

// isSidebarCommand checks if a command should be filtered from pane lists
// Handles both exact matches and path-qualified commands (e.g., /path/to/sidebar-renderer)
func isSidebarCommand(cmd string) bool {
	// Check exact match first
	if sidebarCommands[cmd] {
		return true
	}
	// Check if the command ends with any of the sidebar commands (for path-qualified)
	for sidebarCmd := range sidebarCommands {
		if strings.HasSuffix(cmd, "/"+sidebarCmd) {
			return true
		}
	}
	// Also check if command contains known utility substrings anywhere
	if strings.Contains(cmd, "sidebar") || strings.Contains(cmd, "tabby-daemon") || strings.Contains(cmd, "pane-header") {
		return true
	}
	return false
}

// isPaneBusy returns true if the command is not an idle/shell process.
// AI tool commands are handled separately (busy depends on activity, not just running).
func isPaneBusy(command string) bool {
	// AI tools have activity-based busy detection (handled at coordinator level).
	// Use IsAITool() which also catches semver process names like "2.1.17" (Claude Code).
	if IsAITool(command) {
		return false
	}
	return !idleCommands[command]
}

type Window struct {
	ID          string
	Index       int
	Name        string
	Active      bool
	Activity    bool   // Window has unseen activity (monitor-activity)
	Bell        bool   // Window has triggered bell
	Silence     bool   // Window has been silent (monitor-silence)
	Last        bool   // Window was the last active window
	Busy        bool   // Window is busy (set via @tabby_busy option by the running process)
	Input       bool   // Window needs user input (set via @tabby_input option)
	CustomColor string // User-defined tab color (set via @tabby_color option)
	Group       string // User-assigned group name (set via @tabby_group option)
	Collapsed   bool   // Panes are hidden in sidebar (set via @tabby_collapsed option)
	NameLocked  bool   // Window name was explicitly set by user (set via @tabby_name_locked)
	SyncWidth   bool   // Sync sidebar width with global setting (set via @tabby_sync_width, default true)
	Pinned      bool   // Window is pinned to top of sidebar (set via @tabby_pinned option)
	Icon        string // Custom icon/emoji for window (set via @tabby_icon option)
	Panes       []Pane
	Layout      string // Window layout string from tmux (e.g., "abc1,80x24,0,0{40x24,0,0,1,39x24,41,0,2}")
}

func ListWindows() ([]Window, error) {
	t := perf.Start("tmux.ListWindows")
	defer t.Stop()

	args := []string{"list-windows"}
	if sessionTarget != "" {
		args = append(args, "-t", sessionTarget)
	}
	args = append(args, "-F",
		"#{window_id}\x1f#{window_index}\x1f#{window_name}\x1f#{window_active}\x1f#{window_activity_flag}\x1f#{window_bell_flag}\x1f#{window_silence_flag}\x1f#{window_last_flag}\x1f#{@tabby_color}\x1f#{@tabby_group}\x1f#{@tabby_busy}\x1f#{@tabby_bell}\x1f#{@tabby_activity}\x1f#{@tabby_silence}\x1f#{@tabby_collapsed}\x1f#{@tabby_input}\x1f#{@tabby_name_locked}\x1f#{@tabby_sync_width}\x1f#{session_id}\x1f#{@tabby_pinned}\x1f#{@tabby_icon}\x1f#{window_layout}")
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-windows failed: %w", err)
	}

	var windows []Window
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 8 {
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		customColor := ""
		if len(parts) >= 9 {
			customColor = strings.TrimSpace(parts[8])
		}
		group := ""
		if len(parts) >= 10 {
			group = strings.TrimSpace(parts[9])
		}
		busy := false
		if len(parts) >= 11 {
			// @tabby_busy can be "1", "true", or any non-empty value
			busyVal := strings.TrimSpace(parts[10])
			busy = busyVal == "1" || busyVal == "true"
		}
		// Bell can be from tmux's window_bell_flag OR our custom @tabby_bell option
		bell := parts[5] == "1"
		if len(parts) >= 12 {
			tabbyBell := strings.TrimSpace(parts[11])
			if tabbyBell == "1" || tabbyBell == "true" {
				bell = true
			}
		}
		// Activity can be from tmux's window_activity_flag OR our custom @tabby_activity option
		activity := parts[4] == "1"
		if len(parts) >= 13 {
			tabbyActivity := strings.TrimSpace(parts[12])
			if tabbyActivity == "1" || tabbyActivity == "true" {
				activity = true
			}
		}
		// Silence can be from tmux's window_silence_flag OR our custom @tabby_silence option
		silence := parts[6] == "1"
		if len(parts) >= 14 {
			tabbySilence := strings.TrimSpace(parts[13])
			if tabbySilence == "1" || tabbySilence == "true" {
				silence = true
			}
		}
		// Collapsed state from @tabby_collapsed option
		collapsed := false
		if len(parts) >= 15 {
			tabbyCollapsed := strings.TrimSpace(parts[14])
			collapsed = tabbyCollapsed == "1" || tabbyCollapsed == "true"
		}
		// Input needed state from @tabby_input option
		input := false
		if len(parts) >= 16 {
			tabbyInput := strings.TrimSpace(parts[15])
			input = tabbyInput == "1" || tabbyInput == "true"
		}
		// Name locked state from @tabby_name_locked option
		nameLocked := false
		if len(parts) >= 17 {
			tabbyNameLocked := strings.TrimSpace(parts[16])
			nameLocked = tabbyNameLocked == "1" || tabbyNameLocked == "true"
		}
		// Sync width state from @tabby_sync_width option (default true)
		syncWidth := true
		if len(parts) >= 18 {
			tabbySyncWidth := strings.TrimSpace(parts[17])
			// If explicitly "0" or "false", set to false. Otherwise (empty or "1"), true.
			if tabbySyncWidth == "0" || tabbySyncWidth == "false" {
				syncWidth = false
			}
		}
		// Pinned state from @tabby_pinned option
		pinned := false
		if len(parts) >= 20 {
			tabbyPinned := strings.TrimSpace(parts[19])
			pinned = tabbyPinned == "1" || tabbyPinned == "true"
		}
		// Icon from @tabby_icon option
		icon := ""
		if len(parts) >= 21 {
			icon = strings.TrimSpace(parts[20])
		}
		// Window layout string
		layout := ""
		if len(parts) >= 22 {
			layout = strings.TrimSpace(parts[21])
		}
		// Session ID safety net: skip windows that belong to a different session.
		// tmux list-windows -t $SESSION can transiently return wrong-session windows.
		if sessionTarget != "" && len(parts) >= 19 {
			winSessionID := strings.TrimSpace(parts[18])
			if winSessionID != "" && winSessionID != sessionTarget {
				continue
			}
		}
		windows = append(windows, Window{
			ID:          parts[0],
			Index:       index,
			Name:        stripANSI(parts[2]),
			Active:      parts[3] == "1",
			Activity:    activity,
			Bell:        bell,
			Silence:     silence,
			Last:        parts[7] == "1",
			Busy:        busy,
			Input:       input,
			CustomColor: customColor,
			Group:       group,
			Collapsed:   collapsed,
			NameLocked:  nameLocked,
			SyncWidth:   syncWidth,
			Pinned:      pinned,
			Icon:        icon,
			Layout:      layout,
		})
	}

	return windows, nil
}

// ListPanesForWindow returns all panes in a specific window
func ListPanesForWindow(windowIndex int) ([]Pane, error) {
	t := perf.Start(fmt.Sprintf("tmux.ListPanesForWindow(%d)", windowIndex))
	defer t.Stop()

	windowTarget := fmt.Sprintf(":%d", windowIndex)
	if sessionTarget != "" {
		windowTarget = fmt.Sprintf("%s:%d", sessionTarget, windowIndex)
	}
	cmd := exec.Command("tmux", "list-panes", "-t", windowTarget, "-F",
		"#{pane_id}\x1f#{pane_index}\x1f#{pane_active}\x1f#{pane_current_command}\x1f#{pane_title}\x1f#{pane_pid}\x1f#{pane_last_activity}\x1f#{@tabby_pane_title}\x1f#{pane_top}\x1f#{pane_current_path}\x1f#{@tabby_pane_collapsed}\x1f#{@tabby_pane_prev_height}\x1f#{pane_start_command}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Get current process PID to filter out our own pane
	myPID := fmt.Sprintf("%d", os.Getpid())

	// Current time for activity comparison
	now := time.Now().Unix()

	var panes []Pane
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		// Skip sidebar/tabbar/daemon panes by command name
		// Check for exact matches and also suffix matches (for path-qualified commands)
		cmd := parts[3]
		startCommand := ""
		if len(parts) >= 12 {
			startCommand = parts[11]
		}
		if isSidebarCommand(cmd) || isSidebarCommand(startCommand) {
			continue
		}
		// Skip our own pane (in case command name hasn't updated yet)
		if len(parts) >= 6 && parts[5] == myPID {
			continue
		}
		command := stripANSI(parts[3])
		isRemote := remoteCommands[command]
		busy := isPaneBusy(command)

		// Parse last activity timestamp
		var lastActivityTS int64
		if len(parts) >= 7 {
			lastActivityTS, _ = strconv.ParseInt(parts[6], 10, 64)
		}

		// For remote connections, check if there's been activity in the last 3 seconds
		if isRemote && lastActivityTS > 0 {
			if now-lastActivityTS <= 3 {
				busy = true
			}
		}

		// Get locked title if set
		var lockedTitle string
		if len(parts) >= 8 {
			lockedTitle = strings.TrimSpace(parts[7])
		}

		top := 0
		if len(parts) >= 9 {
			top, _ = strconv.Atoi(parts[8])
		}

		currentPath := ""
		if len(parts) >= 10 {
			currentPath = parts[9]
		}

		panePID := 0
		if len(parts) >= 6 {
			panePID, _ = strconv.Atoi(parts[5])
		}

		collapsed := false
		if len(parts) >= 11 {
			collapsedVal := strings.TrimSpace(parts[10])
			collapsed = collapsedVal == "1" || strings.EqualFold(collapsedVal, "true")
		}
		panes = append(panes, Pane{
			ID:           parts[0],
			Index:        index,
			Active:       parts[2] == "1",
			Command:      command,
			StartCommand: startCommand,
			Title:        stripANSI(parts[4]),
			LockedTitle:  lockedTitle,
			Busy:         busy,
			Remote:       isRemote,
			Top:          top,
			CurrentPath:  currentPath,
			LastActivity: lastActivityTS,
			PID:          panePID,
			Collapsed:    collapsed,
		})
	}
	return panes, nil
}

// ListAllPanes returns all panes across all windows in a single tmux command
// This is more efficient than calling ListPanesForWindow for each window (N+1 problem)
func ListAllPanes() (map[int][]Pane, error) {
	t := perf.Start("tmux.ListAllPanes")
	defer t.Stop()

	// Added pane_start_command, pane_width, pane_height
	// Use -s (session) instead of -a (all) when scoped to a session,
	// to prevent cross-session pane mixing via window index collision.
	args := []string{"list-panes"}
	if sessionTarget != "" {
		args = append(args, "-s", "-t", sessionTarget)
	} else {
		args = append(args, "-a")
	}
	args = append(args, "-F",
		"#{window_index}\x1f#{pane_id}\x1f#{pane_index}\x1f#{pane_active}\x1f#{pane_current_command}\x1f#{pane_title}\x1f#{pane_pid}\x1f#{pane_last_activity}\x1f#{@tabby_pane_title}\x1f#{pane_top}\x1f#{pane_current_path}\x1f#{@tabby_pane_collapsed}\x1f#{@tabby_pane_prev_height}\x1f#{pane_start_command}\x1f#{pane_width}\x1f#{pane_height}")
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	myPID := fmt.Sprintf("%d", os.Getpid())
	now := time.Now().Unix()
	result := make(map[int][]Pane)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 6 {
			continue
		}

		windowIdx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		paneIdx, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}

		// Skip sidebar/tabbar/daemon panes by command name
		cmd := parts[4]
		startCommand := ""
		if len(parts) >= 14 {
			startCommand = parts[13]
		}
		if isSidebarCommand(cmd) || isSidebarCommand(startCommand) {
			continue
		}
		// Skip our own pane
		if len(parts) >= 7 && parts[6] == myPID {
			continue
		}

		command := stripANSI(parts[4])
		isRemote := remoteCommands[command]
		busy := isPaneBusy(command)

		// Parse last activity timestamp
		var lastActivityTS int64
		if len(parts) >= 8 {
			lastActivityTS, _ = strconv.ParseInt(parts[7], 10, 64)
		}

		// For remote connections, check if there's been activity in the last 3 seconds
		if isRemote && lastActivityTS > 0 && now-lastActivityTS <= 3 {
			busy = true
		}

		lockedTitle := ""
		if len(parts) >= 9 {
			lockedTitle = strings.TrimSpace(parts[8])
		}

		top := 0
		if len(parts) >= 10 {
			top, _ = strconv.Atoi(parts[9])
		}

		currentPath := ""
		if len(parts) >= 11 {
			currentPath = parts[10]
		}

		panePID := 0
		if len(parts) >= 7 {
			panePID, _ = strconv.Atoi(parts[6])
		}

		width := 0
		if len(parts) >= 15 {
			width, _ = strconv.Atoi(parts[14])
		}

		height := 0
		if len(parts) >= 16 {
			height, _ = strconv.Atoi(parts[15])
		}

		pane := Pane{
			ID:           parts[1],
			Index:        paneIdx,
			Active:       parts[3] == "1",
			Command:      command,
			StartCommand: startCommand,
			Title:        stripANSI(parts[5]),
			LockedTitle:  lockedTitle,
			Busy:         busy,
			Remote:       isRemote,
			Top:          top,
			Width:        width,
			Height:       height,
			CurrentPath:  currentPath,
			LastActivity: lastActivityTS,
			PID:          panePID,
		}
		if len(parts) >= 12 {
			collapsedVal := strings.TrimSpace(parts[11])
			if collapsedVal == "1" || collapsedVal == "true" {
				pane.Collapsed = true
			}
		}

		result[windowIdx] = append(result[windowIdx], pane)
	}

	return result, nil
}

// ListWindowsWithPanes returns all windows with their panes
// Uses optimized single-query approach to avoid N+1 problem
func ListWindowsWithPanes() ([]Window, error) {
	t := perf.Start("tmux.ListWindowsWithPanes")
	defer t.Stop()

	windows, err := ListWindows()
	if err != nil {
		return nil, err
	}

	// Get all panes in a single command
	allPanes, err := ListAllPanes()
	if err != nil {
		// Fall back to per-window queries if bulk query fails
		for i := range windows {
			panes, _ := ListPanesForWindow(windows[i].Index)
			windows[i].Panes = panes
		}
	} else {
		// Assign panes to their windows
		for i := range windows {
			windows[i].Panes = allPanes[windows[i].Index]
		}
	}

	// Filter out sidebar/header panes defensively before sorting/reindexing.
	for i := range windows {
		if len(windows[i].Panes) == 0 {
			continue
		}
		filtered := windows[i].Panes[:0]
		for _, pane := range windows[i].Panes {
			if isSidebarCommand(pane.Command) || isSidebarCommand(pane.StartCommand) {
				continue
			}
			filtered = append(filtered, pane)
		}
		windows[i].Panes = filtered
	}

	// Sort panes by visual position (top to bottom) and re-index sequentially.
	// tmux list-panes returns panes in creation order, not visual order.
	// Using pane_top ensures numbering matches the top-to-bottom layout.
	for i := range windows {
		sort.Slice(windows[i].Panes, func(a, b int) bool {
			return windows[i].Panes[a].Top < windows[i].Panes[b].Top
		})
		for j := range windows[i].Panes {
			windows[i].Panes[j].Index = j
		}
	}

	// Auto-detect busy state from panes (non-AI tools only).
	// AI tool busy/bell/input states are handled by the coordinator's
	// stateful processAIToolStates() which tracks transitions.
	for i := range windows {
		if !windows[i].Busy {
			for _, pane := range windows[i].Panes {
				if pane.Busy {
					windows[i].Busy = true
					break
				}
			}
		}
	}

	return windows, nil
}
