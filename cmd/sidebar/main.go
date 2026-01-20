package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

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

type model struct {
	windows    []tmux.Window
	grouped    []grouping.GroupedWindows
	config     *config.Config
	cursor     int         // Visual line position of cursor
	windowRefs []windowRef // Maps visual lines to windows
	paneRefs   []paneRef   // Maps visual lines to panes
	totalLines int         // Total number of visual lines

	// Confirmation dialog state
	confirmClose  bool         // Whether we're showing close confirmation
	confirmWindow *tmux.Window // Window pending close confirmation
}

type refreshMsg struct{}

type reloadConfigMsg struct{}

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

func (m model) Init() tea.Cmd {
	return nil
}

func (m *model) buildWindowRefs() {
	m.windowRefs = make([]windowRef, 0)
	m.paneRefs = make([]paneRef, 0)
	line := 0
	for gi := range m.grouped {
		line++ // Group header line
		for wi := range m.grouped[gi].Windows {
			win := &m.grouped[gi].Windows[wi]
			m.windowRefs = append(m.windowRefs, windowRef{
				window: win,
				line:   line,
			})
			line++
			// Track pane lines if window has multiple panes
			if len(win.Panes) > 1 {
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

// calculateButtonLines returns the line numbers for New Tab and Close Tab buttons
func (m model) calculateButtonLines() (newTabLine, closeTabLine int) {
	// Buttons appear after all groups with a blank line
	baseLine := m.totalLines + 1 // +1 for blank line

	newTabLine = -1
	closeTabLine = -1

	if m.config.Sidebar.NewTabButton {
		newTabLine = baseLine
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
				// Confirmed - kill the window
				_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", m.confirmWindow.Index)).Run()
				m.confirmClose = false
				m.confirmWindow = nil
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
					tmux split-window -h -t "$main_pane"
				fi
			`).Run()
			return m, nil
		case "-", "\"":
			// Vertical split (top/bottom) - matches tmux prefix + "
			_ = exec.Command("bash", "-c", `
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux split-window -v -t "$main_pane"
				fi
			`).Run()
			return m, nil
		}

	case tea.MouseMsg:
		// Update cursor on hover if it's a window line
		if m.isWindowLine(msg.Y) {
			m.cursor = msg.Y
		}

		clicked := m.getWindowAtLine(msg.Y)
		newTabLine, closeTabLine := m.calculateButtonLines()

		// Handle mouse clicks - check for press action
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonLeft:
				// Check if clicking on a pane first
				if paneRef, ok := m.getPaneAtLine(msg.Y); ok {
					// Click on pane - switch to window first, then select pane
					_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", paneRef.windowIdx)).Run()
					_ = exec.Command("tmux", "select-pane", "-t", paneRef.pane.ID).Run()
					// Signal ALL sidebars to refresh
					_ = exec.Command("bash", "-c", `
						for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
							kill -USR1 "$pid" 2>/dev/null || true
						done
					`).Run()
					return m, nil
				} else if clicked != nil {
					// Click on window - select it and focus the main content pane
					_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", clicked.Index)).Run()
					// Select the pane that isn't running sidebar (main content pane)
					_ = exec.Command("bash", "-c", `
						main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
						if [ -n "$main_pane" ]; then
							tmux select-pane -t "$main_pane"
						fi
					`).Run()
					// Signal ALL sidebars to refresh (we switched windows, so a different sidebar is now visible)
					_ = exec.Command("bash", "-c", `
						for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
							kill -USR1 "$pid" 2>/dev/null || true
						done
					`).Run()
					return m, nil
				} else if m.config.Sidebar.NewTabButton && msg.Y == newTabLine {
					// Just create new window - the after-new-window hook adds the sidebar
					_ = exec.Command("tmux", "new-window").Run()
					return m, delayedRefresh()
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
				// Check if clicking on a pane first
				if paneRef, ok := m.getPaneAtLine(msg.Y); ok {
					m.showPaneContextMenu(paneRef)
					return m, triggerRefresh()
				}
				// Otherwise check for window
				if clicked != nil {
					m.showContextMenu(clicked)
					return m, triggerRefresh()
				}
			}
		}

	case refreshMsg:
		windows, _ := tmux.ListWindowsWithPanes()
		m.windows = windows
		m.grouped = grouping.GroupWindows(windows, m.config.Groups)
		m.buildWindowRefs()
		// Ensure cursor is still on a valid window line
		if !m.isWindowLine(m.cursor) && len(m.windowRefs) > 0 {
			m.cursor = m.windowRefs[0].line
		}
		return m, nil

	case reloadConfigMsg:
		cfg, err := config.LoadConfig(config.DefaultConfigPath())
		if err == nil {
			m.config = cfg
			m.grouped = grouping.GroupWindows(m.windows, m.config.Groups)
			m.buildWindowRefs()
		}
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
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

	var s string
	line := 0
	for _, group := range m.grouped {
		// Group header - use active colors for emphasis
		headerFg := group.Theme.ActiveFg
		if headerFg == "" {
			headerFg = group.Theme.Fg
		}
		if headerFg == "" {
			headerFg = "#ffffff"
		}
		headerStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(headerFg)).
			Background(lipgloss.Color(group.Theme.Bg)).
			Padding(0, 1)
		s += headerStyle.Render(fmt.Sprintf("%s %s", group.Theme.Icon, group.Name)) + "\n"
		line++

		// Windows in group
		for i, win := range group.Windows {
			// Determine if this line is selected (check BEFORE incrementing)
			isSelected := line == m.cursor
			isActive := win.Active

			// Choose colors based on active state
			var bgColor, fgColor string
			if isActive {
				// Active window uses prominent active colors
				bgColor = group.Theme.ActiveBg
				if bgColor == "" {
					bgColor = group.Theme.Bg
				}
				fgColor = group.Theme.ActiveFg
				if fgColor == "" {
					fgColor = group.Theme.Fg
				}
			} else {
				// Inactive windows use shaded background
				bgColor = grouping.ShadeColorByIndex(group.Theme.Bg, i)
				fgColor = group.Theme.Fg
			}
			if fgColor == "" {
				fgColor = "#ffffff"
			}

			// Build style with theme-aware colors
			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color(fgColor)).
				Background(lipgloss.Color(bgColor))

			// Active window: bold with distinct marker
			if isActive {
				style = style.Bold(true)
			}

			// Selected (cursor) uses underline to avoid washing out project colors
			if isSelected {
				style = style.Underline(true)
			}

			// Format window entry with clear active indicator
			var activeMarker string
			if isActive {
				activeMarker = ">>> " // Very visible active marker
			} else {
				activeMarker = "    " // Spacing for alignment
			}

			// Strip group prefix from window name for cleaner display
			displayName := stripGroupPrefix(win.Name, group.Name, m.config.Groups)

			// Build indicators
			indicators := buildIndicators(win, m.config)

			windowText := fmt.Sprintf("%s%d. %s%s", activeMarker, win.Index, displayName, indicators)
			s += style.Render(windowText) + "\n"
			line++ // Increment AFTER rendering (must match buildWindowRefs order)

			// Show panes if window has multiple panes
			if len(win.Panes) > 1 {
				// Use lighter version of group background for pane styling
				paneBg := grouping.LightenColor(group.Theme.Bg, 0.3)
				paneFg := group.Theme.Fg
				if paneFg == "" {
					paneFg = "#ffffff"
				}

				paneStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(paneFg)).
					Background(lipgloss.Color(paneBg))

				// Active pane only highlighted if window is also active
				activePaneStyle := paneStyle
				if isActive {
					activePaneStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(group.Theme.ActiveFg)).
						Background(lipgloss.Color(group.Theme.ActiveBg)).
						Bold(true)
				}

				for pi, pane := range win.Panes {
					var prefix string
					if pi == len(win.Panes)-1 {
						prefix = "   └─ "
					} else {
						prefix = "   ├─ "
					}

					// Show pane number as window.pane (e.g., 0.1, 0.2)
					paneNum := fmt.Sprintf("%d.%d", win.Index, pane.Index)
					// Show title if set, otherwise show command
					paneLabel := pane.Command
					if pane.Title != "" && pane.Title != pane.Command {
						paneLabel = pane.Title
					}
					paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)
					if pane.Active && isActive {
						s += activePaneStyle.Render(prefix+"► "+paneText) + "\n"
					} else if pane.Active {
						s += paneStyle.Render(prefix+"► "+paneText) + "\n"
					} else {
						s += paneStyle.Render(prefix+"  "+paneText) + "\n"
					}
					line++
				}
			}
		}
	}

	// Buttons
	if m.config.Sidebar.NewTabButton {
		s += "\n"
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#27ae60"))
		s += buttonStyle.Render("[+] New Tab") + "\n"
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

	// Rename option
	menuCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%'\"", win.Name, win.Index)
	args = append(args, "Rename", "r", menuCmd)

	// Separator
	args = append(args, "", "", "")

	// Move to Group submenu - add each group as an option
	args = append(args, "-Move to Group", "", "")
	for i, group := range m.config.Groups {
		if group.Name == "Default" {
			continue // Skip default group in the submenu
		}
		// Extract prefix from pattern
		prefix := extractPrefixFromPattern(group.Pattern)
		// Get base name without any existing prefix
		baseName := getBaseWindowName(win.Name, m.config.Groups)
		newName := prefix + baseName
		key := fmt.Sprintf("%d", i+1)
		if i < 9 {
			renameCmd := fmt.Sprintf("rename-window -t :%d -- '%s'", win.Index, newName)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, renameCmd)
		}
	}

	// Option to remove prefix (move to Default)
	baseName := getBaseWindowName(win.Name, m.config.Groups)
	if baseName != win.Name {
		removeCmd := fmt.Sprintf("rename-window -t :%d -- '%s'", win.Index, baseName)
		args = append(args, "  Remove Prefix", "0", removeCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Split options - target pane 1 (sidebar is pane 0, main content is pane 1+)
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -h", win.Index, win.Index)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -v", win.Index, win.Index)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Kill option
	args = append(args, "Kill", "k", fmt.Sprintf("kill-window -t :%d", win.Index))

	_ = exec.Command("tmux", args...).Run()
}

func (m model) showPaneContextMenu(pr *paneRef) {
	// Use title if set, otherwise command for display
	paneLabel := pr.pane.Command
	if pr.pane.Title != "" && pr.pane.Title != pr.pane.Command {
		paneLabel = pr.pane.Title
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Pane %d.%d: %s", pr.windowIdx, pr.pane.Index, paneLabel),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - use command-prompt to get new title
	currentTitle := pr.pane.Title
	if currentTitle == "" {
		currentTitle = pr.pane.Command
	}
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"select-pane -t %s -T '%%%%'\"", currentTitle, pr.pane.ID)
	args = append(args, "Rename", "r", renameCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options for this pane
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h", pr.windowIdx, pr.pane.ID)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v", pr.windowIdx, pr.pane.ID)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", pr.windowIdx, pr.pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Separator
	args = append(args, "", "", "")

	// Close pane
	args = append(args, "Close Pane", "x", fmt.Sprintf("kill-pane -t %s", pr.pane.ID))

	_ = exec.Command("tmux", args...).Run()
}

// extractPrefixFromPattern extracts the literal prefix from a regex pattern
// e.g., "^SD\\|" -> "SD|"
func extractPrefixFromPattern(pattern string) string {
	if len(pattern) > 0 && pattern[0] == '^' {
		pattern = pattern[1:]
	}
	// Unescape common regex escapes
	pattern = strings.ReplaceAll(pattern, "\\|", "|")
	pattern = strings.ReplaceAll(pattern, "\\.", ".")
	pattern = strings.ReplaceAll(pattern, "\\-", "-")
	// Remove regex wildcards
	pattern = strings.ReplaceAll(pattern, ".*", "")
	pattern = strings.ReplaceAll(pattern, ".+", "")
	return pattern
}

// getBaseWindowName strips any known group prefix from the window name
func getBaseWindowName(windowName string, groups []config.Group) string {
	for _, g := range groups {
		if g.Name == "Default" {
			continue
		}
		prefix := extractPrefixFromPattern(g.Pattern)
		if prefix != "" && strings.HasPrefix(windowName, prefix) {
			return strings.TrimPrefix(windowName, prefix)
		}
	}
	return windowName
}

func buildIndicators(win tmux.Window, cfg *config.Config) string {
	var indicators strings.Builder
	ind := cfg.Indicators

	if ind.Bell.Enabled && win.Bell {
		indicators.WriteString(" ")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))
		indicators.WriteString(style.Render(ind.Bell.Icon))
	}
	if ind.Activity.Enabled && win.Activity {
		indicators.WriteString(" ")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))
		indicators.WriteString(style.Render(ind.Activity.Icon))
	}
	if ind.Silence.Enabled && win.Silence {
		indicators.WriteString(" ")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))
		indicators.WriteString(style.Render(ind.Silence.Icon))
	}
	if ind.Last.Enabled && win.Last && !win.Active {
		indicators.WriteString(" ")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Last.Color))
		indicators.WriteString(style.Render(ind.Last.Icon))
	}

	return indicators.String()
}

// stripGroupPrefix removes the group prefix (e.g., "SD|", "GP|") from window names
// for cleaner display since the group header already shows the category
func stripGroupPrefix(windowName, groupName string, groups []config.Group) string {
	// Find the matching group's pattern
	for _, g := range groups {
		if g.Name == groupName {
			// Extract prefix from pattern like "^SD\\|" -> "SD|"
			// or "^GP\\|" -> "GP|"
			pattern := g.Pattern
			if len(pattern) > 0 && pattern[0] == '^' {
				pattern = pattern[1:] // Remove anchor
			}
			// Unescape common regex escapes
			pattern = strings.ReplaceAll(pattern, "\\|", "|")
			pattern = strings.ReplaceAll(pattern, "\\.", ".")
			pattern = strings.ReplaceAll(pattern, "\\-", "-")

			// If the window name starts with this prefix, strip it
			if strings.HasPrefix(windowName, pattern) {
				return strings.TrimPrefix(windowName, pattern)
			}
			break
		}
	}
	return windowName
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

func main() {
	cfg, _ := config.LoadConfig(config.DefaultConfigPath())
	windows, _ := tmux.ListWindowsWithPanes()
	grouped := grouping.GroupWindows(windows, cfg.Groups)

	m := model{windows: windows, grouped: grouped, config: cfg}
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
