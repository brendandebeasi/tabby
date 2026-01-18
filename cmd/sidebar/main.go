package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

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

type model struct {
	windows    []tmux.Window
	grouped    []grouping.GroupedWindows
	config     *config.Config
	cursor     int         // Visual line position of cursor
	windowRefs []windowRef // Maps visual lines to windows
	totalLines int         // Total number of visual lines
}

type refreshMsg struct{}

type reloadConfigMsg struct{}

func (m model) Init() tea.Cmd {
	return nil
}

func (m *model) buildWindowRefs() {
	m.windowRefs = make([]windowRef, 0)
	line := 0
	for gi := range m.grouped {
		line++ // Group header line
		for wi := range m.grouped[gi].Windows {
			m.windowRefs = append(m.windowRefs, windowRef{
				window: &m.grouped[gi].Windows[wi],
				line:   line,
			})
			line++
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
				_ = exec.Command("tmux", "last-pane").Run()
			}
		case "d", "x":
			if win := m.getSelectedWindow(); win != nil {
				_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
			}
		case "n":
			_ = exec.Command("tmux", "new-window").Run()
		}

	case tea.MouseMsg:
		// Update cursor on hover if it's a window line
		if m.isWindowLine(msg.Y) {
			m.cursor = msg.Y
		}

		clicked := m.getWindowAtLine(msg.Y)
		newTabLine, closeTabLine := m.calculateButtonLines()

		switch msg.Type {
		case tea.MouseLeft:
			if clicked != nil {
				// Click on window - select it and return focus to main pane
				_ = exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", clicked.Index)).Run()
				_ = exec.Command("tmux", "last-pane").Run()
			} else if m.config.Sidebar.NewTabButton && msg.Y == newTabLine {
				_ = exec.Command("tmux", "new-window").Run()
			} else if m.config.Sidebar.CloseButton && msg.Y == closeTabLine {
				// Close currently selected window (cursor position)
				if win := m.getSelectedWindow(); win != nil {
					_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", win.Index)).Run()
				}
			}
		case tea.MouseMiddle:
			if clicked != nil {
				// Middle-click closes the clicked window
				_ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", clicked.Index)).Run()
			}
		case tea.MouseRight:
			if clicked != nil {
				m.showContextMenu(clicked)
			}
		}

	case refreshMsg:
		windows, _ := tmux.ListWindows()
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
	var s string
	line := 0

	for _, group := range m.grouped {
		// Group header
		headerStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color(group.Theme.Bg)).
			Padding(0, 1)
		s += headerStyle.Render(fmt.Sprintf("%s %s", group.Theme.Icon, group.Name)) + "\n"
		line++

		// Windows in group
		for i, win := range group.Windows {
			line++

			// Apply shading based on index within the group
			shadedBg := grouping.ShadeColorByIndex(group.Theme.Bg, i)

			// Determine if this line is selected
			isSelected := line == m.cursor
			isActive := win.Active

			// Build style
			style := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color(shadedBg))

			if isSelected {
				style = style.Reverse(true)
			}
			if isActive {
				style = style.Bold(true)
			}

			// Format window entry
			prefix := "  "
			if isActive {
				prefix = "> "
			}

			// Build indicators
			indicators := buildIndicators(win, m.config)

			windowText := fmt.Sprintf("%s[%d] %s%s", prefix, win.Index, win.Name, indicators)
			s += style.Render(windowText) + "\n"
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
	menuCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -- '%%%%'\"", win.Name)
	_ = exec.Command(
		"tmux",
		"display-menu",
		"-T",
		fmt.Sprintf("Window %d: %s", win.Index, win.Name),
		"-x",
		"M",
		"-y",
		"M",
		"Rename",
		"r",
		menuCmd,
		"",
		"",
		"",
		"Kill",
		"k",
		fmt.Sprintf("kill-window -t :%d", win.Index),
	).Run()
}

func buildIndicators(win tmux.Window, cfg *config.Config) string {
	var indicators strings.Builder
	ind := cfg.Indicators

	if ind.Bell.Enabled && win.Bell {
		indicators.WriteString(" ")
		indicators.WriteString(ind.Bell.Icon)
	}
	if ind.Activity.Enabled && win.Activity {
		indicators.WriteString(" ")
		indicators.WriteString(ind.Activity.Icon)
	}
	if ind.Silence.Enabled && win.Silence {
		indicators.WriteString(" ")
		indicators.WriteString(ind.Silence.Icon)
	}
	if ind.Last.Enabled && win.Last && !win.Active {
		indicators.WriteString(" ")
		indicators.WriteString(ind.Last.Icon)
	}

	return indicators.String()
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
	windows, _ := tmux.ListWindows()
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
