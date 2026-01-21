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

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

type model struct {
	window     *tmux.Window
	groupTheme config.Theme
	config     *config.Config
	width      int
	windowIdx  int
}

type refreshMsg struct{}

// clickRegion tracks where clickable elements are
type clickRegion struct {
	startX int
	endX   int
	action string // "pane:ID", "split-v", "split-h", "close"
	paneID string
}

var clickRegions []clickRegion

func (m model) Init() tea.Cmd {
	return triggerRefresh()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "h", "left":
			// Previous pane
			_ = exec.Command("tmux", "select-pane", "-t", ":.{previous}").Run()
			return m, triggerRefresh()
		case "l", "right":
			// Next pane
			_ = exec.Command("tmux", "select-pane", "-t", ":.{next}").Run()
			return m, triggerRefresh()
		case "|", "v":
			// Split vertical
			_ = exec.Command("tmux", "split-window", "-h", "-c", "#{pane_current_path}").Run()
			return m, triggerRefresh()
		case "-", "s":
			// Split horizontal
			_ = exec.Command("tmux", "split-window", "-v", "-c", "#{pane_current_path}").Run()
			return m, triggerRefresh()
		case "x":
			// Close current pane (but not if it's the pane-bar itself)
			if m.window != nil {
				for _, pane := range m.window.Panes {
					if pane.Active && pane.Command != "pane-bar" {
						_ = exec.Command("tmux", "kill-pane", "-t", pane.ID).Run()
						break
					}
				}
			}
			return m, delayedRefresh()
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Find which region was clicked
			for _, region := range clickRegions {
				if msg.X >= region.startX && msg.X < region.endX {
					switch {
					case strings.HasPrefix(region.action, "pane:"):
						// Select pane
						_ = exec.Command("tmux", "select-pane", "-t", region.paneID).Run()
						return m, triggerRefresh()
					case region.action == "split-v":
						_ = exec.Command("tmux", "split-window", "-h", "-c", "#{pane_current_path}").Run()
						return m, triggerRefresh()
					case region.action == "split-h":
						_ = exec.Command("tmux", "split-window", "-v", "-c", "#{pane_current_path}").Run()
						return m, triggerRefresh()
					case region.action == "close":
						// Close active pane (not the pane-bar)
						if m.window != nil {
							for _, pane := range m.window.Panes {
								if pane.Active && pane.Command != "pane-bar" {
									_ = exec.Command("tmux", "kill-pane", "-t", pane.ID).Run()
									break
								}
							}
						}
						return m, delayedRefresh()
					}
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case refreshMsg:
		// Refresh window and pane data
		windows, _ := tmux.ListWindowsWithPanes()
		for i := range windows {
			if windows[i].Active {
				m.window = &windows[i]
				m.windowIdx = windows[i].Index
				break
			}
		}
		// Get group theme for active window
		if m.window != nil {
			grouped := grouping.GroupWindows(windows, m.config.Groups)
			for _, group := range grouped {
				for _, win := range group.Windows {
					if win.Index == m.windowIdx {
						m.groupTheme = group.Theme
						break
					}
				}
			}
		}
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	// Reset click regions
	clickRegions = nil

	if m.window == nil {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("No window")
	}

	// Filter out utility panes (pane-bar, sidebar) from display
	var panes []tmux.Pane
	for _, p := range m.window.Panes {
		if p.Command != "pane-bar" && p.Command != "sidebar" {
			panes = append(panes, p)
		}
	}

	if len(panes) == 0 {
		return ""
	}

	// Determine base color
	var baseColor string
	if m.window.CustomColor != "" {
		baseColor = m.window.CustomColor
	} else if m.groupTheme.Bg != "" {
		baseColor = m.groupTheme.Bg
	} else {
		baseColor = "#3498db"
	}

	var parts []string
	currentX := 0

	// Pane entries
	for _, pane := range panes {
		var bg, fg string
		if pane.Active {
			bg = baseColor
			fg = "#ffffff"
		} else {
			bg = grouping.InactiveTabColor(baseColor, 0, 0) // Use default lighten/saturate values
			fg = "#cccccc"
		}

		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(fg)).
			Background(lipgloss.Color(bg)).
			Padding(0, 1)

		if pane.Active {
			style = style.Bold(true)
		}

		// Pane label
		paneLabel := pane.Command
		if pane.Title != "" && pane.Title != pane.Command {
			paneLabel = pane.Title
		}

		indicator := " "
		if pane.Active {
			indicator = ">"
		}

		paneText := fmt.Sprintf("%s%d.%d %s", indicator, m.windowIdx, pane.Index, paneLabel)
		rendered := style.Render(paneText)
		renderedWidth := runewidth.StringWidth(paneText) + 2 // +2 for padding

		// Track click region
		clickRegions = append(clickRegions, clickRegion{
			startX: currentX,
			endX:   currentX + renderedWidth,
			action: "pane:" + pane.ID,
			paneID: pane.ID,
		})

		parts = append(parts, rendered)
		currentX += renderedWidth + 1 // +1 for space
	}

	// Join panes with space
	panesPart := strings.Join(parts, " ")

	// Button styles
	buttonStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#27ae60")).
		Padding(0, 1)

	closeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e74c3c")).
		Padding(0, 1)

	// Button text
	splitVBtn := buttonStyle.Render("[|]")
	splitHBtn := buttonStyle.Render("[-]")
	closeBtn := closeStyle.Render("[x]")

	// Each button is 5 chars wide: " [x] " (padding + content)
	btnWidth := 5

	// Calculate spacer to push buttons right
	totalBtnWidth := btnWidth*3 + 2 // 3 buttons + 2 spaces between
	spacerWidth := m.width - currentX - totalBtnWidth - 1
	if spacerWidth < 1 {
		spacerWidth = 1
	}
	spacer := strings.Repeat(" ", spacerWidth)

	// Track button click regions starting after panes + spacer
	btnX := currentX + spacerWidth

	clickRegions = append(clickRegions, clickRegion{
		startX: btnX,
		endX:   btnX + btnWidth,
		action: "split-v",
	})
	btnX += btnWidth + 1 // +1 for space between buttons

	clickRegions = append(clickRegions, clickRegion{
		startX: btnX,
		endX:   btnX + btnWidth,
		action: "split-h",
	})
	btnX += btnWidth + 1

	clickRegions = append(clickRegions, clickRegion{
		startX: btnX,
		endX:   btnX + btnWidth,
		action: "close",
	})

	return panesPart + spacer + splitVBtn + " " + splitHBtn + " " + closeBtn
}

func triggerRefresh() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
}

func delayedRefresh() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return refreshMsg{}
	})
}

func main() {
	cfg, _ := config.LoadConfig(config.DefaultConfigPath())

	// Find active window
	windows, _ := tmux.ListWindowsWithPanes()
	var activeWindow *tmux.Window
	var windowIdx int
	var groupTheme config.Theme

	for i := range windows {
		if windows[i].Active {
			activeWindow = &windows[i]
			windowIdx = windows[i].Index
			break
		}
	}

	// Get group theme
	if activeWindow != nil {
		grouped := grouping.GroupWindows(windows, cfg.Groups)
		for _, group := range grouped {
			for _, win := range group.Windows {
				if win.Index == windowIdx {
					groupTheme = group.Theme
					break
				}
			}
		}
	}

	m := model{
		window:     activeWindow,
		groupTheme: groupTheme,
		config:     cfg,
		width:      120,
		windowIdx:  windowIdx,
	}

	// Listen for refresh signals
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
