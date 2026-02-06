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
	window     *tmux.Window
	groupTheme config.Theme
	config     *config.Config
	theme      *colors.Theme
	width      int
	windowIdx  int
}

// clickRegion tracks clickable areas for mouse handling
type clickRegion struct {
	startX int
	endX   int
	action string
	paneID string
}

var clickRegions []clickRegion

// ... (skipping some parts to find View)

func (m model) getActiveFg() string {
	if m.config.PaneHeader.ActiveFg != "" {
		return m.config.PaneHeader.ActiveFg
	}
	if m.theme != nil {
		return m.theme.PaneActiveFg
	}
	return "#ffffff"
}

func (m model) getInactiveFg() string {
	if m.config.PaneHeader.InactiveFg != "" {
		return m.config.PaneHeader.InactiveFg
	}
	if m.theme != nil {
		return m.theme.PaneInactiveFg
	}
	return "#cccccc"
}

func (m model) getButtonFg() string {
	if m.config.PaneHeader.ButtonFg != "" {
		return m.config.PaneHeader.ButtonFg
	}
	if m.theme != nil {
		return m.theme.PaneButtonFg
	}
	return "#888888"
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

	var parts []string
	currentX := 0

	activeFg := m.getActiveFg()
	inactiveFg := m.getInactiveFg()

	// Pane entries
	for _, pane := range panes {
		var fg string
		if pane.Active {
			fg = activeFg
		} else {
			fg = inactiveFg
		}

		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(fg)).
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
	buttonFg := m.getButtonFg()
	buttonStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(buttonFg)).
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

	// Load color theme
	var theme *colors.Theme
	if cfg.Sidebar.Theme != "" {
		t := colors.GetTheme(cfg.Sidebar.Theme)
		theme = &t
	}

	m := model{
		window:     activeWindow,
		groupTheme: groupTheme,
		config:     cfg,
		theme:      theme,
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
