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
	"github.com/mattn/go-runewidth"

	"github.com/b/tmux-tabs/pkg/colors"
	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

type model struct {
	windows   []tmux.Window
	grouped   []grouping.GroupedWindows
	config    *config.Config
	theme     *colors.Theme
	width     int
	height    int
	scrollPos int // First visible tab index for overflow handling
}

type refreshMsg struct{}

func (m model) Init() tea.Cmd {
	return triggerRefresh() // Load windows on startup
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "h", "left":
			_ = exec.Command("tmux", "previous-window").Run()
			return m, triggerRefresh()
		case "l", "right":
			_ = exec.Command("tmux", "next-window").Run()
			return m, triggerRefresh()
		case "n":
			_ = exec.Command("tmux", "new-window").Run()
			// Add tabbar to the new window and focus main pane
			_ = exec.Command("bash", "-c", `
				PLUGIN_DIR="${HOME}/.tmux/plugins/tmux-tabs"
				tmux split-window -v -b -l 2 "exec \"${PLUGIN_DIR}/bin/tabbar\""
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':tabbar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux select-pane -t "$main_pane"
				fi
			`).Run()
			return m, triggerRefresh()
		case "d", "x":
			// Delete current window
			for _, win := range m.windows {
				if win.Active {
					_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
					break
				}
			}
			return m, delayedRefresh()
		}

	case tea.MouseMsg:
		// Handle mouse clicks - check for press action
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonLeft:
				// Check if click is on pane bar (line 2, Y == 1)
				if msg.Y == 1 {
					if pane := m.getPaneAtX(msg.X); pane != nil {
						// Click on pane - select it
						_ = exec.Command("tmux", "select-pane", "-t", pane.ID).Run()
						return m, triggerRefresh()
					}
				}
				// Find which tab was clicked based on X position (line 1, Y == 0)
				if msg.Y == 0 {
					if win := m.getWindowAtX(msg.X); win != nil {
						_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
						// Select the pane that isn't running tabbar (main content pane)
						_ = exec.Command("bash", "-c", `
							main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':tabbar$' | head -1 | cut -d: -f1)
							if [ -n "$main_pane" ]; then
								tmux select-pane -t "$main_pane"
							fi
						`).Run()
						return m, triggerRefresh()
					}
					// Check if [+] button was clicked
					if m.isNewTabButtonAt(msg.X) {
						_ = exec.Command("tmux", "new-window").Run()
						// Add tabbar to the new window and focus main pane
						_ = exec.Command("bash", "-c", `
							PLUGIN_DIR="${HOME}/.tmux/plugins/tmux-tabs"
							tmux split-window -v -b -l 2 "exec \"${PLUGIN_DIR}/bin/tabbar\""
							main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':tabbar$' | head -1 | cut -d: -f1)
							if [ -n "$main_pane" ]; then
								tmux select-pane -t "$main_pane"
							fi
						`).Run()
						return m, triggerRefresh()
					}
				}
			case tea.MouseButtonMiddle:
				// Middle-click closes the clicked tab
				if win := m.getWindowAtX(msg.X); win != nil {
					_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
					return m, delayedRefresh()
				}
			case tea.MouseButtonRight:
				// Right-click shows context menu
				if win := m.getWindowAtX(msg.X); win != nil {
					m.showContextMenu(win)
					return m, triggerRefresh()
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustScrollForActiveTab()

	case refreshMsg:
		windows, _ := tmux.ListWindowsWithPanes()
		m.windows = windows
		m.grouped = grouping.GroupWindows(windows, m.config.Groups)
		m.adjustScrollForActiveTab()
		return m, nil
	}
	return m, nil
}

// tabEntry stores info about each tab for rendering and click detection
type tabEntry struct {
	window      *tmux.Window
	group       *grouping.GroupedWindows
	displayName string
	rendered    string
	width       int
}

// buildTabs creates all tab entries with their rendered content
func (m model) buildTabs() []tabEntry {
	var tabs []tabEntry

	for gi := range m.grouped {
		group := &m.grouped[gi]
		for wi := range group.Windows {
			win := &group.Windows[wi]
			displayName := win.Name // No prefix stripping needed - groups use @tabby_group option

			// Choose colors - custom color overrides group theme
			var bg, fg string
			if win.CustomColor != "" {
				// Custom color set by user
				if win.Active {
					bg = win.CustomColor
				} else {
					bg = grouping.ShadeColorByIndex(win.CustomColor, 1)
				}
				fg = "#ffffff"
			} else if win.Active {
				bg = group.Theme.ActiveBg
				fg = group.Theme.ActiveFg
				if bg == "" && m.theme != nil {
					bg = m.theme.DefaultActiveBg
				}
				if fg == "" && m.theme != nil {
					fg = m.theme.DefaultActiveFg
				}
			} else {
				bg = group.Theme.Bg
				fg = group.Theme.Fg
				if bg == "" && m.theme != nil {
					bg = m.theme.DefaultGroupBg
				}
				if fg == "" && m.theme != nil {
					fg = m.theme.DefaultGroupFg
				}
			}
			if bg == "" {
				bg = "#333333"
			}
			if fg == "" {
				fg = "#ffffff"
			}

			// Build tab style
			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color(fg)).
				Padding(0, 1)

			if m.theme != nil && m.theme.SidebarBg != "" {
				style = style.Background(lipgloss.Color(bg))
			}

			if win.Active {
				style = style.Bold(true)
			}

			// Add icon if present
			icon := ""
			if group.Theme.Icon != "" {
				icon = group.Theme.Icon + " "
			}

			// Include window index number
			tabText := fmt.Sprintf("%s%d:%s", icon, win.Index, displayName)
			rendered := style.Render(tabText)

			tabs = append(tabs, tabEntry{
				window:      win,
				group:       group,
				displayName: displayName,
				rendered:    rendered,
				width:       runewidth.StringWidth(lipgloss.NewStyle().Render(tabText)) + 2, // +2 for padding
			})
		}
	}
	return tabs
}

// adjustScrollForActiveTab ensures the active tab is visible
func (m *model) adjustScrollForActiveTab() {
	tabs := m.buildTabs()
	if len(tabs) == 0 {
		m.scrollPos = 0
		return
	}

	// Find active tab index
	activeIdx := 0
	for i, tab := range tabs {
		if tab.window.Active {
			activeIdx = i
			break
		}
	}

	// Calculate available width (reserve space for scroll indicators and [+])
	availableWidth := m.width - 10

	// If active tab is before scroll position, scroll left
	if activeIdx < m.scrollPos {
		m.scrollPos = activeIdx
	}

	// If active tab is after visible area, scroll right
	usedWidth := 0
	for i := m.scrollPos; i < len(tabs); i++ {
		usedWidth += tabs[i].width + 1 // +1 for gap
		if i == activeIdx {
			if usedWidth > availableWidth {
				// Need to scroll right
				m.scrollPos = activeIdx
			}
			break
		}
	}
}

func (m model) View() string {
	tabs := m.buildTabs()
	if len(tabs) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("No windows")
	}

	availableWidth := m.width - 10 // Reserve for indicators and [+]
	if availableWidth < 20 {
		availableWidth = 80 // Fallback
	}

	var parts []string
	usedWidth := 0
	hasMore := false

	// Show left scroll indicator if needed
	if m.scrollPos > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("<"))
		usedWidth += 2
	}

	// Render visible tabs
	for i := m.scrollPos; i < len(tabs); i++ {
		tab := tabs[i]
		tabWidth := tab.width + 1 // +1 for gap

		if usedWidth+tabWidth > availableWidth && i > m.scrollPos {
			hasMore = true
			break
		}

		parts = append(parts, tab.rendered)
		usedWidth += tabWidth
	}

	// Show right scroll indicator if needed
	if hasMore {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(">"))
	}

	// Join tabs
	tabBar := strings.Join(parts, " ")

	// Add new tab button
	newTabStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#27ae60"))
	tabBar += " " + newTabStyle.Render("[+]")

	// Build pane bar if active window has multiple panes
	paneBar := m.buildPaneBar()
	if paneBar != "" {
		return tabBar + "\n" + paneBar
	}

	return tabBar
}

// buildPaneBar returns the pane bar for the active window (empty if <=1 pane)
func (m model) buildPaneBar() string {
	// Find active window and its group
	var activeWindow *tmux.Window
	var activeGroup *grouping.GroupedWindows
	for gi := range m.grouped {
		for wi := range m.grouped[gi].Windows {
			if m.grouped[gi].Windows[wi].Active {
				activeWindow = &m.grouped[gi].Windows[wi]
				activeGroup = &m.grouped[gi]
				break
			}
		}
		if activeWindow != nil {
			break
		}
	}

	if activeWindow == nil || len(activeWindow.Panes) <= 1 {
		return ""
	}

	// Use lighter version of colors for pane styling - prefer custom color
	var paneFg, activePaneFg string
	if activeWindow.CustomColor != "" {
		paneFg = "#ffffff"
		activePaneFg = "#ffffff"
	} else {
		paneFg = activeGroup.Theme.Fg
		activePaneFg = activeGroup.Theme.ActiveFg
		if paneFg == "" {
			paneFg = "#ffffff"
		}
		if activePaneFg == "" {
			activePaneFg = "#ffffff"
		}
	}

	paneStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(paneFg)).
		Padding(0, 1)

	activePaneStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(activePaneFg)).
		Bold(true).
		Padding(0, 1)

	var parts []string
	for _, pane := range activeWindow.Panes {
		paneNum := fmt.Sprintf("%d.%d", activeWindow.Index, pane.Index)
		// Show title if set, otherwise show command
		paneLabel := pane.Command
		if pane.Title != "" && pane.Title != pane.Command {
			paneLabel = pane.Title
		}
		paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

		if pane.Active {
			parts = append(parts, activePaneStyle.Render("► "+paneText))
		} else {
			parts = append(parts, paneStyle.Render("  "+paneText))
		}
	}

	return " " + strings.Join(parts, " ")
}

// getWindowAtX finds which window tab is at the given X coordinate
func (m model) getWindowAtX(x int) *tmux.Window {
	tabs := m.buildTabs()
	currentX := 0

	// Account for left scroll indicator
	if m.scrollPos > 0 {
		currentX += 2 // "< " width
	}

	for i := m.scrollPos; i < len(tabs); i++ {
		tab := tabs[i]
		tabWidth := tab.width + 1 // +1 for gap after

		if x >= currentX && x < currentX+tabWidth {
			return tab.window
		}
		currentX += tabWidth
	}
	return nil
}

// isNewTabButtonAt checks if the given X position is on the [+] button
func (m model) isNewTabButtonAt(x int) bool {
	// Calculate where [+] button starts (after all visible tabs)
	tabs := m.buildTabs()
	buttonStart := 0

	// Account for left scroll indicator
	if m.scrollPos > 0 {
		buttonStart += 2
	}

	// Add width of visible tabs
	availableWidth := m.width - 10
	if availableWidth < 20 {
		availableWidth = 80
	}

	usedWidth := buttonStart
	for i := m.scrollPos; i < len(tabs); i++ {
		tabWidth := tabs[i].width + 1
		if usedWidth+tabWidth > availableWidth && i > m.scrollPos {
			break
		}
		usedWidth += tabWidth
	}

	// [+] button is after the tabs with a space
	buttonStart = usedWidth + 1
	// Button is "[+]" which is 3 chars, plus some padding
	return x >= buttonStart && x <= buttonStart+5
}

// getPaneAtX finds which pane was clicked on the pane bar (line 2) based on X position
func (m model) getPaneAtX(x int) *tmux.Pane {
	// Find active window
	var activeWindow *tmux.Window
	for i := range m.windows {
		if m.windows[i].Active {
			activeWindow = &m.windows[i]
			break
		}
	}

	if activeWindow == nil || len(activeWindow.Panes) <= 1 {
		return nil
	}

	// Calculate positions of each pane entry
	// Format: "  >>> 0.1 cmd  " or "      0.1 cmd  "
	currentX := 2 // Initial indent

	for i := range activeWindow.Panes {
		pane := &activeWindow.Panes[i]
		paneNum := fmt.Sprintf("%d.%d", activeWindow.Index, pane.Index)

		// Show title if set, otherwise show command
		paneLabel := pane.Command
		if pane.Title != "" && pane.Title != pane.Command {
			paneLabel = pane.Title
		}
		var paneText string
		if pane.Active {
			paneText = ">>> " + paneNum + " " + paneLabel
		} else {
			paneText = "    " + paneNum + " " + paneLabel
		}

		paneWidth := len(paneText) + 2 // +2 for spacing between panes

		if x >= currentX && x < currentX+paneWidth {
			return pane
		}
		currentX += paneWidth
	}
	return nil
}

func (m model) showContextMenu(win *tmux.Window) {
	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - simple rename, group assignment uses @tabby_group option
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%' ; set-window-option -t :%d automatic-rename off\"", win.Name, win.Index, win.Index)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock auto-rename option
	unlockCmd := fmt.Sprintf("set-window-option -t :%d automatic-rename on", win.Index)
	args = append(args, "Auto-name", "a", unlockCmd)

	// Separator
	args = append(args, "", "", "")

	// Move to Group submenu - sets @tabby_group window option
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range m.config.Groups {
		if group.Name == "Default" {
			continue // Skip default group in the submenu
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
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

	// Kill option
	args = append(args, "Kill", "k", fmt.Sprintf("kill-window -t :%d", win.Index))

	_ = exec.Command("tmux", args...).Run()
}

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

func main() {
	cfg, _ := config.LoadConfig(config.DefaultConfigPath())
	windows, _ := tmux.ListWindows()
	grouped := grouping.GroupWindows(windows, cfg.Groups)

	// Load color theme
	var theme *colors.Theme
	if cfg.Sidebar.Theme != "" {
		t := colors.GetTheme(cfg.Sidebar.Theme)
		theme = &t
	}

	m := model{
		windows:   windows,
		grouped:   grouped,
		config:    cfg,
		theme:     theme,
		width:     120, // Will be updated by WindowSizeMsg
		scrollPos: 0,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	p := tea.NewProgram(m, tea.WithMouseCellMotion())

	go func() {
		for range sigChan {
			p.Send(refreshMsg{})
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
