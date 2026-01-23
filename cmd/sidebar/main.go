package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	"github.com/muesli/termenv"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/perf"
	"github.com/b/tmux-tabs/pkg/tmux"
)

var debugLog *log.Logger
var debugEnabled bool

// abs returns absolute value of an int
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func initDebugLog(enabled bool) {
	debugEnabled = enabled
	if !enabled {
		return
	}
	f, err := os.OpenFile("/tmp/tabby-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	debugLog = log.New(f, "", log.Ltime|log.Lmicroseconds)
}

func debug(format string, args ...interface{}) {
	if debugEnabled && debugLog != nil {
		debugLog.Printf(format, args...)
	}
}

// getCurrentDir returns the directory containing the sidebar binary
func getCurrentDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	// Go up one level from bin/ to the plugin root
	return filepath.Dir(filepath.Dir(exe))
}

// windowRef stores both visual position and window reference
// Fixes BUG-005: cursor vs window index confusion
type windowRef struct {
	window *tmux.Window
	line   int
}

// paneRef stores visual position and pane reference
type paneRef struct {
	pane       *tmux.Pane
	window     *tmux.Window
	windowIdx  int
	line       int
}

// groupRef stores visual position and group reference
type groupRef struct {
	group *grouping.GroupedWindows
	line  int
}

// Default spinner frames if not configured
var defaultSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

type model struct {
	windows    []tmux.Window
	grouped    []grouping.GroupedWindows
	config     *config.Config
	cursor     int         // Visual line position of cursor
	windowRefs []windowRef // Maps visual lines to windows
	paneRefs   []paneRef   // Maps visual lines to panes
	groupRefs  []groupRef  // Maps visual lines to groups
	totalLines int         // Total number of visual lines

	// Terminal size
	width  int // Terminal width (for dynamic resizing)
	height int // Terminal height

	// Confirmation dialog state
	confirmClose  bool         // Whether we're showing close confirmation
	confirmWindow *tmux.Window // Window pending close confirmation

	// Spinner animation state
	spinnerFrame  int  // Current frame index for busy spinner
	spinnerActive bool // Whether spinner ticker is running

	// Collapse state
	collapsedGroups      map[string]bool // groupName -> isCollapsed
	sidebarCollapsed     bool            // Whether the sidebar itself is collapsed to 1 char
	sidebarExpandedWidth int             // Remembered width when expanded

	// Double-click tracking
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int
}

type refreshMsg struct{}

type reloadConfigMsg struct{}

type spinnerTickMsg struct{}

type periodicRefreshMsg struct{}

// triggerRefresh returns a command that triggers a refresh
func triggerRefresh() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
}

// delayedRefresh waits a bit before refreshing (for operations like kill-window)
func delayedRefresh() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return refreshMsg{}
	})
}

// signalSidebarsDelayed signals all sidebars to refresh after a short delay
// This runs in a goroutine to avoid blocking the UI
func signalSidebarsDelayed() {
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("bash", "-c", `
		for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
			kill -USR1 "$pid" 2>/dev/null || true
		done
	`).Run()
}

// spinnerTick schedules the next spinner frame update
func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// periodicRefresh schedules periodic data refresh (for pane titles, etc.)
func periodicRefresh() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		return periodicRefreshMsg{}
	})
}

// hasAnimatedIndicators checks if any window has an animated indicator (busy or input with frames)
func (m model) hasAnimatedIndicators() bool {
	for _, w := range m.windows {
		if w.Busy {
			return true
		}
		// Input indicator is animated if it has frames configured
		if w.Input && len(m.config.Indicators.Input.Frames) > 0 {
			return true
		}
	}
	return false
}

// loadCollapsedGroups reads collapsed group state from tmux session option @tabby_collapsed_groups
// Returns a map of group names to collapsed state
func loadCollapsedGroups() map[string]bool {
	result := make(map[string]bool)
	out, err := exec.Command("tmux", "show-options", "-v", "-q", "@tabby_collapsed_groups").Output()
	if err != nil || len(out) == 0 {
		return result
	}
	var groups []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &groups); err != nil {
		return result
	}
	for _, g := range groups {
		result[g] = true
	}
	return result
}

// saveCollapsedGroups writes collapsed group state to tmux session option @tabby_collapsed_groups
func saveCollapsedGroups(collapsed map[string]bool) {
	var groups []string
	for name, isCollapsed := range collapsed {
		if isCollapsed {
			groups = append(groups, name)
		}
	}
	if len(groups) == 0 {
		// Clear the option if nothing is collapsed
		_ = exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
		return
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return
	}
	_ = exec.Command("tmux", "set-option", "@tabby_collapsed_groups", string(data)).Run()
}

// toggleGroupCollapse toggles the collapsed state for a group
func (m *model) toggleGroupCollapse(groupName string) {
	if m.collapsedGroups == nil {
		m.collapsedGroups = make(map[string]bool)
	}
	m.collapsedGroups[groupName] = !m.collapsedGroups[groupName]
	saveCollapsedGroups(m.collapsedGroups)
}

// isGroupCollapsed returns whether a group is collapsed
func (m model) isGroupCollapsed(groupName string) bool {
	if m.collapsedGroups == nil {
		return false
	}
	return m.collapsedGroups[groupName]
}

// toggleSidebarCollapse collapses or expands the sidebar
func (m *model) toggleSidebarCollapse() {
	if m.sidebarCollapsed {
		// Expand: restore previous width
		width := m.sidebarExpandedWidth
		if width < 20 {
			width = 25 // Default width
		}
		_ = exec.Command("tmux", "resize-pane", "-x", fmt.Sprintf("%d", width)).Run()
		m.sidebarCollapsed = false
	} else {
		// Collapse: save current width and shrink to 2 chars
		m.sidebarExpandedWidth = m.width
		_ = exec.Command("tmux", "resize-pane", "-x", "2").Run()
		m.sidebarCollapsed = true
	}
}

// toggleWindowCollapse toggles the collapsed state for a window (hides/shows panes)
func toggleWindowCollapse(windowIndex int, collapsed bool) {
	if collapsed {
		_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", windowIndex), "@tabby_collapsed", "1").Run()
	} else {
		_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", windowIndex), "-u", "@tabby_collapsed").Run()
	}
}

// getIndicatorIcon returns the icon for an indicator, using animation frames if available
func (m model) getIndicatorIcon(ind config.Indicator) string {
	// If frames are configured, use the current animation frame
	if len(ind.Frames) > 0 {
		return ind.Frames[m.spinnerFrame%len(ind.Frames)]
	}
	// Fall back to single icon
	return ind.Icon
}

// getBusyFrames returns the spinner frames for the busy indicator
func (m model) getBusyFrames() []string {
	if len(m.config.Indicators.Busy.Frames) > 0 {
		return m.config.Indicators.Busy.Frames
	}
	return defaultSpinnerFrames
}

func (m model) Init() tea.Cmd {
	// Start periodic refresh for pane titles, etc.
	return periodicRefresh()
}

func (m *model) buildWindowRefs() {
	m.windowRefs = make([]windowRef, 0)
	m.paneRefs = make([]paneRef, 0)
	m.groupRefs = make([]groupRef, 0)
	line := 0

	// Iterate over grouped windows - this keeps each group together
	// Windows within each group are already sorted by index
	for gi := range m.grouped {
		group := &m.grouped[gi]

		// Group header line - track for right-click menu
		m.groupRefs = append(m.groupRefs, groupRef{
			group: group,
			line:  line,
		})
		line++

		// Skip windows if group is collapsed
		if m.isGroupCollapsed(group.Name) {
			continue
		}

		for wi := range group.Windows {
			win := &group.Windows[wi]

			m.windowRefs = append(m.windowRefs, windowRef{
				window: win,
				line:   line,
			})
			line++

			// Track pane lines if window has multiple panes and window is not collapsed
			if len(win.Panes) > 1 && !win.Collapsed {
				for pi := range win.Panes {
					m.paneRefs = append(m.paneRefs, paneRef{
						pane:      &win.Panes[pi],
						window:    win,
						windowIdx: win.Index,
						line:      line,
					})
					line++
				}
			}
		}
	}
	m.totalLines = line
}

// getWindowAtLine returns the window at the given visual line number
// Fixes BUG-004: Y-coordinate off-by-one
func (m model) getWindowAtLine(y int) *tmux.Window {
	for _, ref := range m.windowRefs {
		if ref.line == y {
			return ref.window
		}
	}
	return nil
}

// getPaneAtLine returns the pane at the given visual line number
func (m model) getPaneAtLine(y int) (*paneRef, bool) {
	for i, ref := range m.paneRefs {
		if ref.line == y {
			return &m.paneRefs[i], true
		}
	}
	return nil, false
}

// getGroupAtLine returns the group at the given visual line number
func (m model) getGroupAtLine(y int) (*groupRef, bool) {
	for i, ref := range m.groupRefs {
		if ref.line == y {
			return &m.groupRefs[i], true
		}
	}
	return nil, false
}

// getSelectedWindow returns the window at the current cursor position
// Fixes BUG-005: properly maps cursor to window
func (m model) getSelectedWindow() *tmux.Window {
	for _, ref := range m.windowRefs {
		if ref.line == m.cursor {
			return ref.window
		}
	}
	return nil
}

// isWindowLine checks if the given line contains a window (not a group header)
func (m model) isWindowLine(y int) bool {
	for _, ref := range m.windowRefs {
		if ref.line == y {
			return true
		}
	}
	return false
}

// calculateButtonLines returns the line numbers for New Tab, New Group, and Close Tab buttons
func (m model) calculateButtonLines() (newTabLine, newGroupLine, closeTabLine, collapseLine int) {
	// Buttons appear after all groups with a blank line
	baseLine := m.totalLines + 1 // +1 for blank line

	newTabLine = -1
	newGroupLine = -1
	closeTabLine = -1
	collapseLine = -1 // Not used but kept for compatibility

	if m.config.Sidebar.NewTabButton {
		newTabLine = baseLine
		baseLine++
	}
	if m.config.Sidebar.NewGroupButton {
		newGroupLine = baseLine
		baseLine++
	}
	if m.config.Sidebar.CloseButton {
		closeTabLine = baseLine
	}
	return
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle confirmation dialog first
		if m.confirmClose && m.confirmWindow != nil {
			switch msg.String() {
			case "y", "Y":
				// Confirmed - kill the window and switch to last window
				windowIdx := m.confirmWindow.Index
				m.confirmClose = false
				m.confirmWindow = nil
				// Kill window, switch to last window, then focus main pane
				_ = exec.Command("bash", "-c", fmt.Sprintf(`
					tmux kill-window -t :%d
					tmux last-window 2>/dev/null || tmux select-window -t :0
					main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
					if [ -n "$main_pane" ]; then
						tmux select-pane -t "$main_pane"
					fi
				`, windowIdx)).Run()
				// Signal all sidebars to refresh after brief delay
				go func() {
					time.Sleep(100 * time.Millisecond)
					_ = exec.Command("bash", "-c", `
						for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
							kill -USR1 "$pid" 2>/dev/null || true
						done
					`).Run()
				}()
				return m, delayedRefresh()
			case "n", "N", "esc", "escape":
				// Cancelled
				m.confirmClose = false
				m.confirmWindow = nil
				return m, nil
			default:
				// Any other key cancels
				m.confirmClose = false
				m.confirmWindow = nil
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc", "escape":
			_ = exec.Command("tmux", "last-pane").Run()
			return m, nil
		case "j", "down":
			// Move cursor down to next window line
			for _, ref := range m.windowRefs {
				if ref.line > m.cursor {
					m.cursor = ref.line
					break
				}
			}
		case "k", "up":
			// Move cursor up to previous window line
			for i := len(m.windowRefs) - 1; i >= 0; i-- {
				if m.windowRefs[i].line < m.cursor {
					m.cursor = m.windowRefs[i].line
					break
				}
			}
		case "enter":
			if win := m.getSelectedWindow(); win != nil {
				_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
				// Select the pane that isn't running sidebar
				_ = exec.Command("bash", "-c", `
					main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
					if [ -n "$main_pane" ]; then
						tmux select-pane -t "$main_pane"
					fi
				`).Run()
				// Signal ALL sidebars to refresh
				_ = exec.Command("bash", "-c", `
					for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
						kill -USR1 "$pid" 2>/dev/null || true
					done
				`).Run()
				return m, nil
			}
		case "d", "x":
			if win := m.getSelectedWindow(); win != nil {
				// Enter confirmation mode
				m.confirmClose = true
				m.confirmWindow = win
				return m, nil
			}
		case "c", "n":
			// Just create new window - the after-new-window hook adds the sidebar
			// 'c' matches tmux default, 'n' kept for compatibility
			_ = exec.Command("tmux", "new-window").Run()
			return m, delayedRefresh()
		case "|", "%":
			// Horizontal split (left/right) - matches tmux prefix + %
			_ = exec.Command("bash", "-c", `
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux split-window -h -t "$main_pane" -c "#{pane_current_path}"
				fi
			`).Run()
			return m, nil
		case "-", "\"":
			// Vertical split (top/bottom) - matches tmux prefix + "
			_ = exec.Command("bash", "-c", `
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux split-window -v -t "$main_pane" -c "#{pane_current_path}"
				fi
			`).Run()
			return m, nil
		case "ctrl+<", "ctrl+[", "alt+<", "alt+[":
			// Collapse/expand sidebar (requires modifier key)
			m.toggleSidebarCollapse()
			return m, nil
		}

	case tea.MouseMsg:
		// If sidebar is collapsed, any click expands it
		if m.sidebarCollapsed {
			if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
				m.toggleSidebarCollapse()
				return m, nil
			}
			return m, nil // Ignore other mouse events when collapsed
		}

		// Update cursor on hover if it's a window line
		if m.isWindowLine(msg.Y) {
			m.cursor = msg.Y
		}

		clicked := m.getWindowAtLine(msg.Y)
		newTabLine, newGroupLine, closeTabLine, _ := m.calculateButtonLines()

		// Check for double-click to toggle sidebar collapse
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			now := time.Now()
			// Double-click if within 400ms and same position (or close)
			if now.Sub(m.lastClickTime) < 400*time.Millisecond &&
				abs(msg.X-m.lastClickX) <= 2 && abs(msg.Y-m.lastClickY) <= 1 {
				// Double-click detected - toggle sidebar collapse
				m.toggleSidebarCollapse()
				m.lastClickTime = time.Time{} // Reset to prevent triple-click
				return m, nil
			}
			m.lastClickTime = now
			m.lastClickX = msg.X
			m.lastClickY = msg.Y
		}

		// Check for click on right edge (divider area) - collapse sidebar
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.width > 0 && msg.X >= m.width-1 {
				m.toggleSidebarCollapse()
				return m, nil
			}
		}

		// Handle mouse clicks - check for press action
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonLeft:
				// Check if clicking on group header collapse toggle (first 2 chars)
				if groupRef, ok := m.getGroupAtLine(msg.Y); ok && msg.X < 2 {
					m.toggleGroupCollapse(groupRef.group.Name)
					m.buildWindowRefs()
					return m, nil
				}
				// Check if clicking on window collapse toggle (chars 3-4, after tree branch)
				// This only works for windows that have multiple panes
				if clicked != nil && msg.X >= 3 && msg.X <= 4 && len(clicked.Panes) > 1 {
					toggleWindowCollapse(clicked.Index, !clicked.Collapsed)
					// Signal all sidebars to refresh
					go signalSidebarsDelayed()
					return m, delayedRefresh()
				}
				// Check if clicking on a pane first
				if paneRef, ok := m.getPaneAtLine(msg.Y); ok {
					// OPTIMISTIC UI: Update local state immediately
					windowIdx := paneRef.windowIdx
					paneID := paneRef.pane.ID
					for i := range m.windows {
						m.windows[i].Active = (m.windows[i].Index == windowIdx)
						// Also update active pane within the window
						for j := range m.windows[i].Panes {
							m.windows[i].Panes[j].Active = (m.windows[i].Panes[j].ID == paneID)
						}
					}
					m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
					m.buildWindowRefs()

					// Send tmux command asynchronously
					go func() {
						_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", windowIdx), ";",
							"select-pane", "-t", paneID).Run()
						signalSidebarsDelayed()
					}()
					return m, nil
				} else if clicked != nil {
					// OPTIMISTIC UI: Update local state immediately for instant feedback
					clickedIndex := clicked.Index
					for i := range m.windows {
						m.windows[i].Active = (m.windows[i].Index == clickedIndex)
					}
					// Recompute grouped state with optimistic update
					m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
					m.buildWindowRefs()

					// Send tmux command - use direct tmux command, much faster than bash
					// Just select window, tmux will remember the last active pane
					go func() {
						_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", clickedIndex)).Run()
						// Signal other sidebars to refresh
						signalSidebarsDelayed()
					}()

					// Return immediately with updated model
					return m, nil
				} else if m.config.Sidebar.NewTabButton && msg.Y == newTabLine {
					// Just create new window - the after-new-window hook adds the sidebar
					_ = exec.Command("tmux", "new-window").Run()
					return m, delayedRefresh()
				} else if m.config.Sidebar.NewGroupButton && msg.Y == newGroupLine {
					// Open new group prompt
					m.showNewGroupPrompt()
					return m, nil
				} else if m.config.Sidebar.CloseButton && msg.Y == closeTabLine {
					// Close currently selected window (cursor position) - with confirmation
					if win := m.getSelectedWindow(); win != nil {
						m.confirmClose = true
						m.confirmWindow = win
						return m, nil
					}
				}
			case tea.MouseButtonMiddle:
				if clicked != nil {
					// Middle-click closes the clicked window - with confirmation
					m.confirmClose = true
					m.confirmWindow = clicked
					return m, nil
				}
			case tea.MouseButtonRight:
				// Check if clicking on a group header first
				if groupRef, ok := m.getGroupAtLine(msg.Y); ok {
					m.showGroupContextMenu(groupRef)
					return m, triggerRefresh()
				}
				// Check if clicking on a pane
				if paneRef, ok := m.getPaneAtLine(msg.Y); ok {
					m.showPaneContextMenu(paneRef)
					return m, triggerRefresh()
				}
				// Check if right-clicking on indicator column (X=0) with an alert
				if clicked != nil && msg.X == 0 && (clicked.Activity || clicked.Bell || clicked.Busy) {
					m.showAlertPopup(clicked)
					return m, nil
				}
				// Otherwise check for window context menu
				if clicked != nil {
					m.showContextMenu(clicked)
					return m, triggerRefresh()
				}
			}
		}

	case refreshMsg:
		t := perf.Start("Update.refreshMsg")
		defer t.Stop()
		windows, _ := tmux.ListWindowsWithPanes()
		// Clear bell flag for active windows (user has seen it)
		for _, w := range windows {
			if w.Active && w.Bell {
				_ = exec.Command("tmux", "set-option", "-t", fmt.Sprintf(":%d", w.Index), "-wu", "@tabby_bell").Run()
			}
		}
		m.windows = windows
		m.grouped = grouping.GroupWindowsWithOptions(windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
		// Always update pane colors (custom colors can change anytime)
		updatePaneHeaderColors(m.grouped)
		m.buildWindowRefs()
		// Ensure cursor is still on a valid window line
		if !m.isWindowLine(m.cursor) && len(m.windowRefs) > 0 {
			m.cursor = m.windowRefs[0].line
		}
		// Start spinner animation if any windows are busy and not already running
		if m.hasAnimatedIndicators() && !m.spinnerActive {
			m.spinnerActive = true
			return m, spinnerTick()
		}
		return m, nil

	case spinnerTickMsg:
		// Advance spinner frame
		busyFrames := m.getBusyFrames()
		m.spinnerFrame = (m.spinnerFrame + 1) % len(busyFrames)
		// Continue animation if still have busy windows
		if m.hasAnimatedIndicators() {
			return m, spinnerTick()
		}
		// Stop animation
		m.spinnerActive = false
		return m, nil

	case reloadConfigMsg:
		cfg, err := config.LoadConfig(config.DefaultConfigPath())
		if err == nil {
			m.config = cfg
			m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
			updatePaneHeaderColors(m.grouped)
			m.buildWindowRefs()
		}
		return m, nil

	case periodicRefreshMsg:
		// Periodic refresh for pane titles and other dynamic data
		windows, _ := tmux.ListWindowsWithPanes()
		// Clear bell flag for active windows (user has seen it)
		for _, w := range windows {
			if w.Active && w.Bell {
				_ = exec.Command("tmux", "set-option", "-t", fmt.Sprintf(":%d", w.Index), "-wu", "@tabby_bell").Run()
			}
		}
		m.windows = windows
		m.grouped = grouping.GroupWindowsWithOptions(windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
		updatePaneHeaderColors(m.grouped)
		m.buildWindowRefs()
		// Schedule next periodic refresh
		return m, periodicRefresh()

	case tea.WindowSizeMsg:
		// Update terminal size for dynamic resizing
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	t := perf.Start("View")
	defer t.Stop()

	// Show confirmation dialog if active
	if m.confirmClose && m.confirmWindow != nil {
		confirmStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e74c3c")).
			Padding(1, 1)
		windowStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f1c40f"))
		promptStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff"))

		return confirmStyle.Render("Close window?") + "\n\n" +
			windowStyle.Render(fmt.Sprintf("  %d. %s", m.confirmWindow.Index, m.confirmWindow.Name)) + "\n\n" +
			promptStyle.Render("  Press y to confirm, n to cancel")
	}

	debug("--- View() render ---")

	// Show collapsed view if sidebar is collapsed
	if m.sidebarCollapsed {
		// Render vertical ">" characters down the sidebar
		expandIcon := ">"
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Bold(true)
		var s string
		for i := 0; i < m.height; i++ {
			s += style.Render(expandIcon) + "\n"
		}
		return s
	}

	var s string
	// Use terminal width if available, otherwise default to 25
	sidebarWidth := m.width
	if sidebarWidth < 20 {
		sidebarWidth = 25 // Default minimum width
	}
	contentWidth := sidebarWidth - 4 // Space for tree chars and arrow

	// Visual position counter (0-n from top to bottom)
	visualPos := 0

	// Iterate over grouped windows - keeps each group together
	for _, group := range m.grouped {
		theme := group.Theme
		isCollapsed := m.isGroupCollapsed(group.Name)

		// Show group header with collapse indicator at start
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Fg)).
			Background(lipgloss.Color(theme.Bg)).
			Bold(true)

		// Collapse indicator: ⊟ expanded, ⊞ collapsed (at start)
		expandedIcon := m.config.Sidebar.Colors.DisclosureExpanded
		if expandedIcon == "" {
			expandedIcon = "⊟"
		}
		collapsedIcon := m.config.Sidebar.Colors.DisclosureCollapsed
		if collapsedIcon == "" {
			collapsedIcon = "⊞"
		}
		collapseIcon := expandedIcon
		if isCollapsed {
			collapseIcon = collapsedIcon
		}
		disclosureColor := m.config.Sidebar.Colors.DisclosureFg
		if disclosureColor == "" {
			disclosureColor = "#000000"
		}
		collapseStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(disclosureColor)).
			Background(lipgloss.Color(theme.Bg))

		icon := theme.Icon
		if icon != "" {
			icon += " "
		}

		// Build header: [collapse] [icon] Name [count if collapsed]
		headerText := icon + group.Name
		if isCollapsed && len(group.Windows) > 0 {
			headerText += fmt.Sprintf(" (%d)", len(group.Windows))
		}

		// Width: 2 for collapse icon + space, rest for content
		headerContentStyle := headerStyle.Width(sidebarWidth - 2)
		s += collapseStyle.Render(collapseIcon+" ") + headerContentStyle.Render(headerText) + "\n"

		// Skip windows if group is collapsed
		if isCollapsed {
			continue
		}

		// Tree characters - configurable color
		treeFg := m.config.Sidebar.Colors.TreeFg
		if treeFg == "" {
			treeFg = "#888888"
		}
		treeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(treeFg))

		// Active indicator color (can be "auto" to use window/group bg color)
		activeIndFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg

		// Tree branch characters
		treeBranchChar := m.config.Sidebar.Colors.TreeBranch
		if treeBranchChar == "" {
			treeBranchChar = "├─"
		}
		treeBranchLastChar := m.config.Sidebar.Colors.TreeBranchLast
		if treeBranchLastChar == "" {
			treeBranchLastChar = "└─"
		}
		treeConnectorChar := m.config.Sidebar.Colors.TreeConnector
		if treeConnectorChar == "" {
			treeConnectorChar = "─"
		}
		treeConnectorPanesChar := m.config.Sidebar.Colors.TreeConnectorPanes
		if treeConnectorPanesChar == "" {
			treeConnectorPanesChar = "┬"
		}
		treeContinueChar := m.config.Sidebar.Colors.TreeContinue
		if treeContinueChar == "" {
			treeContinueChar = "│"
		}

		// Show windows in this group
		numWindows := len(group.Windows)
		for wi, win := range group.Windows {
			isActive := win.Active
			isLastInGroup := wi == numWindows-1

			// Choose colors - custom color overrides group theme
			var bgColor, fgColor string
			isTransparent := win.CustomColor == "transparent"
			if isTransparent {
				// Transparent mode: no background, just text color
				bgColor = ""
				if isActive {
					fgColor = "#ffffff"
				} else {
					fgColor = "#888888"
				}
			} else if win.CustomColor != "" {
				if isActive {
					bgColor = win.CustomColor
				} else {
					bgColor = grouping.ShadeColorByIndex(win.CustomColor, 1)
				}
				fgColor = "#ffffff"
			} else if isActive {
				bgColor = theme.ActiveBg
				if bgColor == "" {
					bgColor = theme.Bg
				}
				fgColor = theme.ActiveFg
				if fgColor == "" {
					fgColor = theme.Fg
				}
			} else {
				bgColor = theme.Bg
				fgColor = theme.Fg
			}
			if fgColor == "" {
				fgColor = "#ffffff"
			}

			// Build style
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor))
			if bgColor != "" {
				style = style.Background(lipgloss.Color(bgColor))
			}
			if isActive {
				style = style.Bold(true)
			}

			// Display name is the window name
			displayName := win.Name

			// Build alert indicator (shown at start of tab if any alert)
			// Busy indicator always shown (even for active window - you're waiting for it)
			// Other indicators skipped for active window - you're already looking at it
			alertIcon := ""
			ind := m.config.Indicators

			// Debug: log window state
			debug("Window %d (%s): Active=%v, Busy=%v (panes: %d), Bell=%v, Activity=%v, Silence=%v",
				win.Index, win.Name, isActive, win.Busy, len(win.Panes), win.Bell, win.Activity, win.Silence)
			for _, p := range win.Panes {
				debug("  Pane %d: cmd=%s, busy=%v, active=%v", p.Index, p.Command, p.Busy, p.Active)
			}

			if ind.Busy.Enabled && win.Busy {
				debug("  -> BUSY indicator (Busy.Enabled=%v, win.Busy=%v)", ind.Busy.Enabled, win.Busy)
				// Busy indicator shown even for active window
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Busy.Color))
				if ind.Busy.Bg != "" {
					alertStyle = alertStyle.Background(lipgloss.Color(ind.Busy.Bg))
				}
				busyFrames := m.getBusyFrames()
				alertIcon = alertStyle.Render(busyFrames[m.spinnerFrame%len(busyFrames)])
			} else if ind.Input.Enabled && win.Input {
				debug("  -> INPUT indicator (needs user input)")
				// Input indicator shown even for active window - important to see
				inputIcon := ind.Input.Icon
				if inputIcon == "" {
					inputIcon = "?"
				}
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Input.Color))
				if ind.Input.Bg != "" {
					alertStyle = alertStyle.Background(lipgloss.Color(ind.Input.Bg))
				}
				// Support animation frames for input indicator
				if len(ind.Input.Frames) > 0 {
					alertIcon = alertStyle.Render(ind.Input.Frames[m.spinnerFrame%len(ind.Input.Frames)])
				} else {
					alertIcon = alertStyle.Render(inputIcon)
				}
			} else if !isActive {
				// Other indicators only for inactive windows
				if ind.Bell.Enabled && win.Bell {
					debug("  -> BELL indicator")
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))
					if ind.Bell.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Bell.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Bell))
				} else if ind.Activity.Enabled && win.Activity {
					debug("  -> ACTIVITY indicator")
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))
					if ind.Activity.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Activity.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Activity))
				} else if ind.Silence.Enabled && win.Silence {
					debug("  -> SILENCE indicator")
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))
					if ind.Silence.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Silence.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Silence))
				} else {
					debug("  -> no indicator")
				}
			} else {
				debug("  -> no indicator (active window, not busy)")
			}

			// Build tab content (use visual position, not tmux index)
			baseContent := fmt.Sprintf("%d. %s", visualPos, displayName)
			availableWidth := contentWidth - 2 // space for indicator
			if lipgloss.Width(baseContent) > availableWidth {
				truncated := ""
				for _, r := range baseContent {
					if lipgloss.Width(truncated+string(r)) > availableWidth-1 {
						break
					}
					truncated += string(r)
				}
				baseContent = truncated + "~"
			}

			// Render indicator at far left
			var indicatorPart string
			if alertIcon != "" {
				indicatorPart = alertIcon
			} else {
				indicatorPart = " "
			}

			// Window tree branch
			var treeBranch string
			if isLastInGroup {
				treeBranch = treeBranchLastChar
			} else {
				treeBranch = treeBranchChar
			}

			// Window line - show collapse indicator if has panes
			hasPanes := len(win.Panes) > 1
			isWindowCollapsed := win.Collapsed
			var windowCollapseIcon string

			// Get configurable icons
			expandedIcon := m.config.Sidebar.Colors.DisclosureExpanded
			if expandedIcon == "" {
				expandedIcon = "⊟"
			}
			collapsedIcon := m.config.Sidebar.Colors.DisclosureCollapsed
			if collapsedIcon == "" {
				collapsedIcon = "⊞"
			}

			if hasPanes {
				if isWindowCollapsed {
					windowCollapseIcon = collapsedIcon // Collapsed - click to expand
				} else {
					windowCollapseIcon = expandedIcon // Expanded - click to collapse
				}
			}

			// Add pane count to window name if collapsed and has multiple panes
			displayContent := baseContent
			if hasPanes && isWindowCollapsed {
				displayContent = fmt.Sprintf("%s (%d)", baseContent, len(win.Panes))
			}

			// Style for window collapse icon (configurable color on window bg)
			disclosureColor := m.config.Sidebar.Colors.DisclosureFg
			if disclosureColor == "" {
				disclosureColor = "#000000"
			}
			windowCollapseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(disclosureColor))
			if bgColor != "" {
				windowCollapseStyle = windowCollapseStyle.Background(lipgloss.Color(bgColor))
			}

			// Calculate content width
			// Layout: indicator(1) + tree(2) + extras + content
			prefixWidth := 3 // indicator + tree branch
			if hasPanes {
				prefixWidth += 2 // collapse icon + space
			} else {
				prefixWidth += 0 // single-pane windows: no extra indent
			}
			windowContentWidth := sidebarWidth - prefixWidth

			// Truncate content if needed
			contentText := displayContent
			contentLen := lipgloss.Width(contentText)
			if contentLen > windowContentWidth {
				truncated := ""
				for _, r := range contentText {
					if lipgloss.Width(truncated+string(r)) > windowContentWidth-1 {
						break
					}
					truncated += string(r)
				}
				contentText = truncated + "~"
				contentLen = lipgloss.Width(contentText)
			}
			// Pad to fill width
			contentStyle := style.Width(windowContentWidth)

			// Get active indicator icon and style
			activeIndicator := m.config.Sidebar.Colors.ActiveIndicator
			if activeIndicator == "" {
				activeIndicator = "◀"
			}

			// Determine active indicator color - "auto" uses window/group bg
			var activeIndFg string
			if activeIndFgConfig == "auto" || activeIndFgConfig == "" {
				if bgColor != "" {
					activeIndFg = bgColor
				} else {
					activeIndFg = "#ffffff" // Default for transparent
				}
			} else {
				activeIndFg = activeIndFgConfig
			}
			arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(activeIndFg)).Bold(true)
			// Add background if configured
			activeIndBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
			if activeIndBgConfig != "" {
				arrowStyle = arrowStyle.Background(lipgloss.Color(activeIndBgConfig))
			}

			debug("  Rendering: hasPanes=%v, isActive=%v, windowCollapseIcon='%s'", hasPanes, isActive, windowCollapseIcon)

			if hasPanes {
				// Windows with panes: ├─⊟ content (collapse icon after tree)
				debug("    -> hasPanes branch: icon='%s'", windowCollapseIcon)
				s += indicatorPart + treeStyle.Render(treeBranch) + windowCollapseStyle.Render(windowCollapseIcon+" ") + contentStyle.Render(contentText) + "\n"
			} else if isActive {
				// Active single-pane: ├● content (last char becomes indicator)
				// Get first char of tree branch (├ or └)
				treeBranchRunes := []rune(treeBranch)
				treeBranchFirst := string(treeBranchRunes[0])

				// Determine indicator background - "auto" uses group's active_indicator_bg (lighter color)
				indicatorBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
				var indicatorBg string
				if indicatorBgConfig == "" || indicatorBgConfig == "auto" {
					// Use theme's active_indicator_bg if set, otherwise fall back to theme.Bg
					if theme.ActiveIndicatorBg != "" {
						indicatorBg = theme.ActiveIndicatorBg
					} else {
						indicatorBg = theme.Bg
					}
				} else {
					indicatorBg = indicatorBgConfig
				}

				// Determine indicator foreground - "auto" uses same as bg (solid color block)
				indicatorFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg
				var indicatorFg string
				if indicatorFgConfig == "" || indicatorFgConfig == "auto" {
					indicatorFg = indicatorBg // Same as bg = solid color block
				} else {
					indicatorFg = indicatorFgConfig
				}

				activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Background(lipgloss.Color(indicatorBg)).Bold(true)
				s += indicatorPart + treeStyle.Render(treeBranchFirst) + activeIndStyle.Render(activeIndicator) + contentStyle.Render(contentText) + "\n"
			} else {
				// Inactive single-pane: ├─ content
				s += indicatorPart + treeStyle.Render(treeBranch) + contentStyle.Render(contentText) + "\n"
			}

			// Show panes if window has multiple panes and is not collapsed
			if len(win.Panes) > 1 && !isWindowCollapsed {
				var paneBg, paneFg, activePaneBg string
				if win.CustomColor != "" {
					paneBg = grouping.LightenColor(win.CustomColor, 0.3)
					activePaneBg = win.CustomColor
					paneFg = "#ffffff"
				} else {
					paneBg = grouping.LightenColor(theme.Bg, 0.3)
					activePaneBg = theme.ActiveBg
					paneFg = theme.Fg
					if paneFg == "" {
						paneFg = "#ffffff"
					}
				}

				paneStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(paneFg)).
					Background(lipgloss.Color(paneBg))

				activePaneStyle := paneStyle
				if isActive {
					activePaneFg := "#ffffff"
					if win.CustomColor == "" && theme.ActiveFg != "" {
						activePaneFg = theme.ActiveFg
					}
					activePaneStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(activePaneFg)).
						Background(lipgloss.Color(activePaneBg)).
						Bold(true)
				}

				// Tree continuation: │ if more windows below, space if last window
				var treeContinue string
				if isLastInGroup {
					treeContinue = " " // No more windows below
				} else {
					treeContinue = treeStyle.Render(treeContinueChar) // More windows below
				}

				numPanes := len(win.Panes)
				for pi, pane := range win.Panes {
					isLastPane := pi == numPanes-1

					// Pane branch: use first char of tree branch chars
					var paneBranch string
					if isLastPane {
						// Use first rune from treeBranchLastChar
						for _, r := range treeBranchLastChar {
							paneBranch = string(r)
							break
						}
					} else {
						// Use first rune from treeBranchChar
						for _, r := range treeBranchChar {
							paneBranch = string(r)
							break
						}
					}

					paneNum := fmt.Sprintf("%d.%d", visualPos, pane.Index)
					paneLabel := pane.Command
					if pane.LockedTitle != "" {
						paneLabel = pane.LockedTitle
					} else if pane.Title != "" && pane.Title != pane.Command {
						paneLabel = pane.Title
					}
					paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

					paneIndentWidth := 6 // space(1) + windowCont(1) + space(1) + corner(1) + connector(2 or 1+indicator)
					paneContentWidth := sidebarWidth - paneIndentWidth

					// Truncate content if needed
					if len(paneText) > paneContentWidth {
						paneText = paneText[:paneContentWidth-1] + "~"
					}

					// Active pane gets indicator, inactive gets extended pipe
					paneActiveIndicator := m.config.Sidebar.Colors.ActiveIndicator
					if paneActiveIndicator == "" {
						paneActiveIndicator = "█"
					}
					if pane.Active && isActive {
						// Create pane indicator style using theme's lighter color
						paneIndicatorBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
						var paneIndicatorBg string
						if paneIndicatorBgConfig == "" || paneIndicatorBgConfig == "auto" {
							if theme.ActiveIndicatorBg != "" {
								paneIndicatorBg = theme.ActiveIndicatorBg
							} else {
								paneIndicatorBg = theme.Bg
							}
						} else {
							paneIndicatorBg = paneIndicatorBgConfig
						}
						paneIndicatorFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg
						var paneIndicatorFg string
						if paneIndicatorFgConfig == "" || paneIndicatorFgConfig == "auto" {
							paneIndicatorFg = paneIndicatorBg // Same as bg = solid color
						} else {
							paneIndicatorFg = paneIndicatorFgConfig
						}
						paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Background(lipgloss.Color(paneIndicatorBg)).Bold(true)
						fullWidthPaneStyle := activePaneStyle.Width(paneContentWidth)
						s += " " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar) + paneIndStyle.Render(paneActiveIndicator) + fullWidthPaneStyle.Render(paneText) + "\n"
					} else {
						// Extend pipe to connect
						s += " " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText) + "\n"
					}
				}
			}

			// Increment visual position for next window
			visualPos++
		}
	}

	// Buttons
	if m.config.Sidebar.NewTabButton {
		s += "\n"
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#27ae60"))
		s += buttonStyle.Render("[+] New Tab") + "\n"
	}

	if m.config.Sidebar.NewGroupButton {
		if !m.config.Sidebar.NewTabButton {
			s += "\n" // Add blank line if New Tab button isn't there
		}
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b59b6"))
		s += buttonStyle.Render("[+] New Group") + "\n"
	}

	if m.config.Sidebar.CloseButton {
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))
		s += buttonStyle.Render("[x] Close Tab") + "\n"
	}

	return s
}

func (m model) showContextMenu(win *tmux.Window) {
	// Build menu arguments dynamically
	// -O keeps menu open when mouse exits (allows click to select)
	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - simple rename without prefix manipulation
	// Group assignment is now handled by @tabby_group option, not window name prefixes
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%' ; set-window-option -t :%d automatic-rename off\"", win.Name, win.Index, win.Index)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option (restore automatic naming)
	unlockCmd := fmt.Sprintf("set-window-option -t :%d automatic-rename on", win.Index)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Collapse/Expand panes option (only for windows with multiple panes)
	if len(win.Panes) > 1 {
		args = append(args, "", "", "") // Separator
		if win.Collapsed {
			expandCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_collapsed", win.Index)
			args = append(args, "Expand Panes", "e", expandCmd)
		} else {
			collapseCmd := fmt.Sprintf("set-window-option -t :%d @tabby_collapsed 1", win.Index)
			args = append(args, "Collapse Panes", "c", collapseCmd)
		}
	}

	// Separator
	args = append(args, "", "", "")

	// Move to Group submenu - sets @tabby_group window option
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range m.config.Groups {
		if group.Name == "Default" {
			continue // Skip default group in the submenu (use "Remove from Group" instead)
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			// Set the @tabby_group window option
			setGroupCmd := fmt.Sprintf("set-window-option -t :%d @tabby_group '%s'", win.Index, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, setGroupCmd)
		}
	}

	// Option to remove from group (move to Default) - unsets @tabby_group
	if win.Group != "" {
		removeCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_group", win.Index)
		args = append(args, "  Remove from Group", "0", removeCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Set Color submenu
	args = append(args, "-Set Tab Color", "", "")
	colorOptions := []struct {
		name string
		hex  string
		key  string
	}{
		{"Red", "#e74c3c", "r"},
		{"Orange", "#e67e22", "o"},
		{"Yellow", "#f1c40f", "y"},
		{"Green", "#27ae60", "g"},
		{"Blue", "#3498db", "b"},
		{"Purple", "#9b59b6", "p"},
		{"Pink", "#e91e63", "i"},
		{"Cyan", "#00bcd4", "c"},
		{"Gray", "#7f8c8d", "a"},
		{"Transparent", "transparent", "t"},
	}
	for _, color := range colorOptions {
		setColorCmd := fmt.Sprintf("set-window-option -t :%d @tabby_color '%s'", win.Index, color.hex)
		args = append(args, fmt.Sprintf("  %s", color.name), color.key, setColorCmd)
	}
	// Reset option
	resetColorCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_color", win.Index)
	args = append(args, "  Reset to Default", "d", resetColorCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options - target pane 1 (sidebar is pane 0, main content is pane 1+)
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -h -c '#{pane_current_path}'", win.Index, win.Index)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -v -c '#{pane_current_path}'", win.Index, win.Index)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Open in Finder - opens the pane's current directory
	openFinderCmd := fmt.Sprintf("run-shell 'open \"#{pane_current_path}\"'")
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Kill option - uses helper script to avoid complex quoting issues
	killCmd := fmt.Sprintf("run-shell '%s/scripts/kill_window.sh %d'", getCurrentDir(), win.Index)
	args = append(args, "Kill", "k", killCmd)

	_ = exec.Command("tmux", args...).Run()
}

func (m model) showPaneContextMenu(pr *paneRef) {
	// Use locked title, then title, then command for display
	paneLabel := pr.pane.Command
	if pr.pane.LockedTitle != "" {
		paneLabel = pr.pane.LockedTitle
	} else if pr.pane.Title != "" && pr.pane.Title != pr.pane.Command {
		paneLabel = pr.pane.Title
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Pane %d.%d: %s", pr.windowIdx, pr.pane.Index, paneLabel),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - sets @tabby_pane_title to lock the name
	currentTitle := pr.pane.LockedTitle
	if currentTitle == "" {
		currentTitle = pr.pane.Title
	}
	if currentTitle == "" {
		currentTitle = pr.pane.Command
	}
	// Use helper script to handle the rename and lock
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"run-shell '%s/scripts/rename_pane.sh %s \\\"%%%%\\\"'\"", currentTitle, getCurrentDir(), pr.pane.ID)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option (clear locked title, show command instead)
	unlockCmd := fmt.Sprintf("set-option -p -t %s -u @tabby_pane_title ; select-pane -t %s -T ''", pr.pane.ID, pr.pane.ID)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options for this pane
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '#{pane_current_path}'", pr.windowIdx, pr.pane.ID)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '#{pane_current_path}'", pr.windowIdx, pr.pane.ID)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", pr.windowIdx, pr.pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Break pane to new window
	breakCmd := fmt.Sprintf("break-pane -s %s", pr.pane.ID)
	args = append(args, "Break to New Window", "b", breakCmd)

	// Open in Finder - opens this pane's current directory
	openFinderCmd := fmt.Sprintf("run-shell 'open \"#(tmux display-message -t %s -p \"#{pane_current_path}\")\"'", pr.pane.ID)
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Close pane
	args = append(args, "Close Pane", "x", fmt.Sprintf("kill-pane -t %s", pr.pane.ID))

	_ = exec.Command("tmux", args...).Run()
}

// showAlertPopup shows recent output from a window with an alert indicator
func (m model) showAlertPopup(win *tmux.Window) {
	// Determine alert type for title
	alertType := "Activity"
	if win.Bell {
		alertType = "Bell"
	} else if win.Busy {
		alertType = "Busy"
	}

	// Use tmux popup to show recent output captured from the window's main pane
	// Find the non-sidebar pane in the target window
	// Use less with -R for colors, ESC or q to quit
	popupCmd := fmt.Sprintf(`
		target_pane=$(tmux list-panes -t :%d -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
		if [ -n "$target_pane" ]; then
			tmux capture-pane -t "$target_pane" -p -e -S -50 > /tmp/tabby-alert-$$.txt
			tmux display-popup -w 80 -h 25 -T " %s: %s (ESC/q to close) " -E "less -R +G /tmp/tabby-alert-$$.txt; rm -f /tmp/tabby-alert-$$.txt"
		fi
	`, win.Index, alertType, win.Name)

	_ = exec.Command("bash", "-c", popupCmd).Run()
}

// showGroupContextMenu shows a context menu for a group header
func (m model) showGroupContextMenu(gr *groupRef) {
	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Group: %s (%d windows)", gr.group.Name, len(gr.group.Windows)),
		"-x", "M",
		"-y", "M",
	}

	// Build list of window indices in this group
	var indices []string
	for _, win := range gr.group.Windows {
		indices = append(indices, fmt.Sprintf("%d", win.Index))
	}
	indicesStr := strings.Join(indices, " ")

	// Add new window in this group - set @tabby_group option and use working_dir if configured
	var workingDir string
	for _, cfgGroup := range m.config.Groups {
		if cfgGroup.Name == gr.group.Name && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			break
		}
	}

	// Use configured working_dir, or fall back to current pane's path
	dirArg := "'#{pane_current_path}'"
	if workingDir != "" {
		dirArg = fmt.Sprintf("'%s'", workingDir)
	}

	if gr.group.Name != "Default" {
		newWindowCmd := fmt.Sprintf("new-window -c %s ; set-window-option @tabby_group '%s'", dirArg, gr.group.Name)
		args = append(args, fmt.Sprintf("New %s Window", gr.group.Name), "n", newWindowCmd)
	} else {
		newWindowCmd := fmt.Sprintf("new-window -c %s", dirArg)
		args = append(args, "New Window", "n", newWindowCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Collapse/Expand option
	if m.isGroupCollapsed(gr.group.Name) {
		expandCmd := fmt.Sprintf("run-shell '%s/scripts/toggle_group_collapse.sh \"%s\" expand'", getCurrentDir(), gr.group.Name)
		args = append(args, "Expand Group", "e", expandCmd)
	} else {
		collapseCmd := fmt.Sprintf("run-shell '%s/scripts/toggle_group_collapse.sh \"%s\" collapse'", getCurrentDir(), gr.group.Name)
		args = append(args, "Collapse Group", "c", collapseCmd)
	}

	// Only show Edit/Delete for non-Default groups
	if gr.group.Name != "Default" {
		// Separator
		args = append(args, "", "", "")

		// Edit Group submenu
		args = append(args, "-Edit Group", "", "")

		// Rename
		renameCmd := fmt.Sprintf("command-prompt -I '%s' -p 'New name:' \"run-shell '%s/scripts/rename_group.sh %s %%%%'\"",
			gr.group.Name, getCurrentDir(), gr.group.Name)
		args = append(args, "  Rename", "r", renameCmd)

		// Change Color submenu
		args = append(args, "  -Change Color", "", "")
		colorOptions := []struct {
			name string
			hex  string
			key  string
		}{
			{"Red", "#e74c3c", "r"},
			{"Orange", "#e67e22", "o"},
			{"Yellow", "#f1c40f", "y"},
			{"Green", "#27ae60", "g"},
			{"Blue", "#3498db", "b"},
			{"Purple", "#9b59b6", "p"},
			{"Pink", "#e91e63", "i"},
			{"Cyan", "#00bcd4", "c"},
			{"Gray", "#7f8c8d", "a"},
			{"Transparent", "transparent", "t"},
		}
		for _, color := range colorOptions {
			setColorCmd := fmt.Sprintf("run-shell '%s/scripts/set_group_color.sh \"%s\" \"%s\"'",
				getCurrentDir(), gr.group.Name, color.hex)
			args = append(args, fmt.Sprintf("    %s", color.name), color.key, setColorCmd)
		}

		// Set Working Directory option
		currentWorkingDir := workingDir
		if currentWorkingDir == "" {
			currentWorkingDir = "~"
		}
		setWorkingDirCmd := fmt.Sprintf("command-prompt -I '%s' -p 'Working directory:' \"run-shell '%s/scripts/set_group_working_dir.sh \\\"%s\\\" \\\"%%%%\\\"'\"",
			currentWorkingDir, getCurrentDir(), gr.group.Name)
		args = append(args, "  Set Working Directory", "w", setWorkingDirCmd)

		// Separator before Delete
		args = append(args, "", "", "")

		// Delete Group
		deleteCmd := fmt.Sprintf("confirm-before -p 'Delete group %s? (y/n)' \"run-shell '%s/scripts/delete_group.sh %s'\"",
			gr.group.Name, getCurrentDir(), gr.group.Name)
		args = append(args, "Delete Group", "d", deleteCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Close all windows in this group - pass window indices directly
	if len(indices) > 0 {
		closeAllCmd := fmt.Sprintf("run-shell '%s/scripts/kill_windows.sh %s'", getCurrentDir(), indicesStr)
		args = append(args, "Close All Windows", "x", closeAllCmd)
	}

	_ = exec.Command("tmux", args...).Run()
}

// showNewGroupPrompt shows a prompt to create a new group
func (m model) showNewGroupPrompt() {
	// Use tmux command-prompt to get the group name
	// The script will add the group to config.yaml and trigger a config reload
	scriptPath := getCurrentDir() + "/scripts/new_group.sh"
	promptCmd := fmt.Sprintf("command-prompt -p 'New group name:' \"run-shell '%s %%%%'\"", scriptPath)
	_ = exec.Command("tmux", "run-shell", "-b", fmt.Sprintf("tmux %s", promptCmd)).Run()
}

// extractGroupPrefix extracts the window name prefix from a regex pattern
// e.g., "^SD\\|" -> "SD|", "^GP\\|" -> "GP|"
func extractGroupPrefix(pattern string) string {
	if len(pattern) < 2 {
		return ""
	}
	// Remove leading ^ if present
	if pattern[0] == '^' {
		pattern = pattern[1:]
	}
	// Unescape common patterns
	pattern = strings.ReplaceAll(pattern, "\\|", "|")
	pattern = strings.ReplaceAll(pattern, "\\.", ".")
	// If it still has regex chars, it's not a simple prefix
	if strings.ContainsAny(pattern, ".*+?[](){}$") {
		return ""
	}
	return pattern
}

// findWindowGroup returns the group name and theme for a window based on @tabby_group option
func findWindowGroup(win *tmux.Window, groups []config.Group) (string, config.Theme) {
	var defaultTheme config.Theme
	defaultName := "Default"

	// Get group name from window option (set via @tabby_group)
	groupName := win.Group
	if groupName == "" {
		groupName = "Default"
	}

	// Find the matching group config
	for _, group := range groups {
		if group.Name == "Default" {
			defaultTheme = group.Theme
		}
		if group.Name == groupName {
			return group.Name, group.Theme
		}
	}

	// Group not found in config, fall back to Default
	if defaultTheme.Bg != "" {
		return defaultName, defaultTheme
	}
	return defaultName, config.Theme{
		Bg:       "#3498db",
		Fg:       "#ffffff",
		ActiveBg: "#2980b9",
		ActiveFg: "#ffffff",
	}
}



func watchConfig(p *tea.Program, configPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	_ = watcher.Add(configPath)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Write == fsnotify.Write {
					p.Send(reloadConfigMsg{})
				}
			case <-watcher.Errors:
				return
			}
		}
	}()
}

// updatePaneHeaderColors sets pane header colors on each window for pane-border-format
func updatePaneHeaderColors(grouped []grouping.GroupedWindows) {
	for _, group := range grouped {
		baseColor := group.Theme.Bg
		for _, win := range group.Windows {
			// Use window custom color if set, otherwise group color
			color := baseColor
			if win.CustomColor != "" {
				color = win.CustomColor
			}
			// Set both active and inactive colors
			_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_active", color).Run()
			inactive := grouping.LightenColor(color, 0.15)
			_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_inactive", inactive).Run()
		}
	}
}

func main() {
	// Force ANSI256 color mode to avoid partial 24-bit escape code issues
	lipgloss.SetColorProfile(termenv.ANSI256)

	cfg, _ := config.LoadConfig(config.DefaultConfigPath())

	// Initialize debug logging based on config
	initDebugLog(cfg.Sidebar.Debug)
	debug("=== Sidebar starting ===")
	windows, _ := tmux.ListWindowsWithPanes()
	grouped := grouping.GroupWindowsWithOptions(windows, cfg.Groups, cfg.Sidebar.ShowEmptyGroups)

	// Set initial pane header colors
	updatePaneHeaderColors(grouped)

	m := model{
		windows:         windows,
		grouped:         grouped,
		config:          cfg,
		collapsedGroups: loadCollapsedGroups(),
	}
	m.buildWindowRefs()

	// Set initial cursor to first window
	if len(m.windowRefs) > 0 {
		m.cursor = m.windowRefs[0].line
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	watchConfig(p, config.DefaultConfigPath())

	go func() {
		for range sigChan {
			p.Send(refreshMsg{})
		}
	}()

	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
