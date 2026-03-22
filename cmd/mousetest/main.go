package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	lastEvent string
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Log to file
	f, _ := os.OpenFile("/tmp/mousetest.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if f != nil {
		fmt.Fprintf(f, "[%s] %T: %v\n", time.Now().Format("15:04:05"), msg, msg)
		f.Close()
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.lastEvent = fmt.Sprintf("Key: %s", msg.String())
	case tea.MouseMsg:
		m.lastEvent = fmt.Sprintf("Mouse: %v at %d,%d", msg.Button, msg.X, msg.Y)
	case tea.WindowSizeMsg:
		m.lastEvent = fmt.Sprintf("Size: %dx%d", msg.Width, msg.Height)
	}
	return m, nil
}

func (m model) View() string {
	return fmt.Sprintf("Mouse Test\n\nLast event: %s\n\nPress 'q' to quit", m.lastEvent)
}

func main() {
	// Try without alt screen
	p := tea.NewProgram(model{}, tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
