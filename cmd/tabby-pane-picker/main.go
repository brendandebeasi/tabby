package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var windowID = flag.String("window", "", "tmux window ID to list panes for")

type paneInfo struct {
	id      string
	index   string
	command string
	path    string
	active  bool
}

type model struct {
	panes    []paneInfo
	cursor   int
	filter   string
	filtered []paneInfo
	width    int
	height   int
	err      string
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Padding(0, 1)
	normalStyle   = lipgloss.NewStyle().Padding(0, 1)
	activeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Padding(0, 1)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Padding(0, 1)
	filterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	hintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if strings.HasPrefix(p, home) {
			p = "~" + p[len(home):]
		}
	}
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
		return strings.Join(parts, "/")
	}
	return p
}

func loadPanes(winID string) ([]paneInfo, string, error) {
	args := []string{"list-panes", "-F",
		"#{pane_id}|#{pane_index}|#{pane_current_command}|#{pane_current_path}|#{pane_active}"}
	if winID != "" {
		args = append(args, "-t", winID)
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return nil, "", err
	}

	var activePaneID string
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		p := paneInfo{
			id:      parts[0],
			index:   parts[1],
			command: parts[2],
			path:    shortenPath(parts[3]),
			active:  parts[4] == "1",
		}
		if p.active {
			activePaneID = p.id
		}
		panes = append(panes, p)
	}
	return panes, activePaneID, nil
}

func applyFilter(panes []paneInfo, filter string) []paneInfo {
	if filter == "" {
		return panes
	}
	filter = strings.ToLower(filter)
	var result []paneInfo
	for _, p := range panes {
		if strings.HasPrefix(strings.ToLower(p.command), filter) {
			result = append(result, p)
		}
	}
	return result
}

func initialModel() model {
	panes, _, err := loadPanes(*windowID)
	m := model{panes: panes, width: 80, height: 24}
	if err != nil {
		m.err = fmt.Sprintf("tmux error: %v", err)
		return m
	}
	m.filtered = applyFilter(panes, "")
	// Start cursor on the active pane
	for i, p := range m.filtered {
		if p.active {
			m.cursor = i
			break
		}
	}
	return m
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit

		case "up", "ctrl+p", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "ctrl+n", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}

		case "enter":
			if len(m.filtered) > 0 {
				chosen := m.filtered[m.cursor]
				exec.Command("tmux", "select-pane", "-t", chosen.id).Run()
			}
			return m, tea.Quit

		case "backspace", "ctrl+h":
			if len(m.filter) > 0 {
				m.filter = m.filter[:len(m.filter)-1]
				m.filtered = applyFilter(m.panes, m.filter)
				if m.cursor >= len(m.filtered) {
					m.cursor = max(0, len(m.filtered)-1)
				}
			}

		default:
			// Type-to-filter: only printable single chars
			if len(msg.String()) == 1 {
				m.filter += msg.String()
				m.filtered = applyFilter(m.panes, m.filter)
				m.cursor = 0
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.err != "" {
		return m.err + "\n\nPress q to close."
	}

	var sb strings.Builder

	title := titleStyle.Render("Pane Picker")
	sb.WriteString(title + "\n")

	if m.filter != "" {
		sb.WriteString(filterStyle.Render("Filter: "+m.filter) + "\n")
	} else {
		sb.WriteString(hintStyle.Render("Type to filter by command") + "\n")
	}
	sb.WriteString("\n")

	if len(m.filtered) == 0 {
		sb.WriteString(dimStyle.Render("No panes match.") + "\n")
	} else {
		for i, p := range m.filtered {
			label := fmt.Sprintf("[%s] %-20s  %s", p.index, p.command, p.path)
			// Truncate to terminal width minus padding
			maxLen := m.width - 4
			if maxLen > 0 && len(label) > maxLen {
				label = label[:maxLen]
			}
			if i == m.cursor {
				sb.WriteString(selectedStyle.Render(label) + "\n")
			} else if p.active {
				sb.WriteString(activeStyle.Render(label) + "\n")
			} else {
				sb.WriteString(normalStyle.Render(label) + "\n")
			}
		}
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("Enter: select  Esc/q: cancel  Up/Down: navigate"))

	return sb.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	flag.Parse()

	// If window not specified, use current window
	if *windowID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		if err == nil {
			*windowID = strings.TrimSpace(string(out))
		}
	}

	// Set color profile based on binary location (consistent with other tabby binaries)
	_ = filepath.Dir(os.Args[0])

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
