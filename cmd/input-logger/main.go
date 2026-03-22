package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type mode int

const (
	modeDevice mode = iota
	modeDescribe
	modeListening
)

type event struct {
	timestamp time.Time
	desc      string
}

type testCase struct {
	device      string
	description string
	events      []event
	startTime   time.Time
}

type model struct {
	mode        mode
	device      string
	deviceInput string
	description string
	testCases   []testCase
	savedCount  int
	current     *testCase
	width       int
	height      int
	outputFile  string
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeDevice:
		return m.updateDevice(msg)
	case modeDescribe:
		return m.updateDescribe(msg)
	case modeListening:
		return m.updateListening(msg)
	}
	return m, nil
}

func (m model) updateDevice(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.deviceInput != "" {
				m.device = m.deviceInput
				m.deviceInput = ""
				m.mode = modeDescribe
			}
		case "backspace":
			if len(m.deviceInput) > 0 {
				m.deviceInput = m.deviceInput[:len(m.deviceInput)-1]
			}
		default:
			if len(msg.String()) == 1 || msg.String() == "space" {
				if msg.String() == "space" {
					m.deviceInput += " "
				} else {
					m.deviceInput += msg.String()
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m model) updateDescribe(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.saveToFile()
			return m, tea.Quit
		case "ctrl+s":
			m.saveToFile()
			return m, nil
		case "tab":
			m.mode = modeDevice
			m.deviceInput = m.device
			return m, nil
		case "enter":
			if m.description != "" {
				m.current = &testCase{
					device:      m.device,
					description: m.description,
					events:      []event{},
					startTime:   time.Now(),
				}
				m.description = ""
				m.mode = modeListening
			}
		case "backspace":
			if len(m.description) > 0 {
				m.description = m.description[:len(m.description)-1]
			}
		default:
			if len(msg.String()) == 1 || msg.String() == "space" {
				if msg.String() == "space" {
					m.description += " "
				} else {
					m.description += msg.String()
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m model) updateListening(msg tea.Msg) (tea.Model, tea.Cmd) {
	var desc string

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Escape or F1 to finish listening
		if msg.String() == "esc" || msg.String() == "f1" {
			if m.current != nil {
				m.testCases = append(m.testCases, *m.current)
				m.current = nil
				m.saveToFile() // Auto-save after each test
			}
			m.mode = modeDescribe
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			if m.current != nil {
				m.testCases = append(m.testCases, *m.current)
			}
			m.saveToFile()
			return m, tea.Quit
		}
		desc = fmt.Sprintf("KEY: %q (type=%s)", msg.String(), msg.Type)

	case tea.MouseMsg:
		action := "unknown"
		switch msg.Action {
		case tea.MouseActionPress:
			action = "PRESS"
		case tea.MouseActionRelease:
			action = "RELEASE"
		case tea.MouseActionMotion:
			action = "MOTION"
		}

		button := "none"
		switch msg.Button {
		case tea.MouseButtonLeft:
			button = "LEFT"
		case tea.MouseButtonRight:
			button = "RIGHT"
		case tea.MouseButtonMiddle:
			button = "MIDDLE"
		case tea.MouseButtonWheelUp:
			button = "WHEEL_UP"
		case tea.MouseButtonWheelDown:
			button = "WHEEL_DOWN"
		case tea.MouseButtonWheelLeft:
			button = "WHEEL_LEFT"
		case tea.MouseButtonWheelRight:
			button = "WHEEL_RIGHT"
		case tea.MouseButtonBackward:
			button = "BACKWARD"
		case tea.MouseButtonForward:
			button = "FORWARD"
		case tea.MouseButtonNone:
			button = "NONE"
		}

		// Skip motion events with no button to reduce noise
		if msg.Action == tea.MouseActionMotion && msg.Button == tea.MouseButtonNone {
			return m, nil
		}

		modifiers := ""
		if msg.Alt {
			modifiers += " +Alt"
		}
		if msg.Ctrl {
			modifiers += " +Ctrl"
		}
		if msg.Shift {
			modifiers += " +Shift"
		}
		if modifiers == "" {
			modifiers = " (no mods)"
		}
		desc = fmt.Sprintf("MOUSE: %s %s at (%d, %d)%s [raw: %s]", action, button, msg.X, msg.Y, modifiers, msg.String())

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		desc = fmt.Sprintf("RESIZE: %dx%d", msg.Width, msg.Height)
	}

	if desc != "" && m.current != nil {
		m.current.events = append(m.current.events, event{
			timestamp: time.Now(),
			desc:      desc,
		})
	}

	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString("=== Input Logger ===\n")
	b.WriteString(fmt.Sprintf("Window: %dx%d\n", m.width, m.height))
	b.WriteString("-------------------\n\n")

	switch m.mode {
	case modeDevice:
		b.WriteString("What device are you testing with?\n")
		b.WriteString("(e.g., iPhone 15, iPad Pro, MacBook trackpad, Magic Mouse)\n\n")
		b.WriteString(fmt.Sprintf("> %s_\n\n", m.deviceInput))
		b.WriteString("Press ENTER to continue\n")

	case modeDescribe:
		b.WriteString(fmt.Sprintf("Device: %s\n", m.device))
		b.WriteString("-------------------\n")
		b.WriteString("Describe what you're about to do, then press ENTER:\n\n")
		b.WriteString(fmt.Sprintf("> %s_\n\n", m.description))
		b.WriteString("-------------------\n")
		b.WriteString("Ctrl+S = save | Ctrl+C = save & quit\n\n")

		b.WriteString("Tab = change device\n\n")

		if m.savedCount > 0 || len(m.testCases) > 0 {
			b.WriteString(fmt.Sprintf("Saved: %d | Pending: %d\n", m.savedCount, len(m.testCases)))
			for i, tc := range m.testCases {
				b.WriteString(fmt.Sprintf("  - [%s] %s (%d events)\n", tc.device, tc.description, len(tc.events)))
				_ = i
			}
		}

	case modeListening:
		b.WriteString(fmt.Sprintf("LISTENING: %s\n", m.current.description))
		b.WriteString("Press ESC or F1 when done\n")
		b.WriteString("-------------------\n\n")

		if len(m.current.events) == 0 {
			b.WriteString("(waiting for input...)\n")
		} else {
			// Show last 15 events, newest first
			start := 0
			if len(m.current.events) > 15 {
				start = len(m.current.events) - 15
			}
			for i := len(m.current.events) - 1; i >= start; i-- {
				e := m.current.events[i]
				elapsed := e.timestamp.Sub(m.current.startTime)
				b.WriteString(fmt.Sprintf("[+%6.0fms] %s\n", float64(elapsed.Milliseconds()), e.desc))
			}
			if start > 0 {
				b.WriteString(fmt.Sprintf("... and %d earlier events\n", start))
			}
		}
	}

	return b.String()
}

func (m *model) saveToFile() {
	if len(m.testCases) == 0 {
		return
	}

	// Check if file exists to determine if we need header
	needsHeader := true
	if _, err := os.Stat(m.outputFile); err == nil {
		needsHeader = false
	}

	var b strings.Builder
	if needsHeader {
		b.WriteString("# Input Logger Results\n")
		b.WriteString(fmt.Sprintf("# Generated: %s\n\n", time.Now().Format(time.RFC3339)))
	}

	for i, tc := range m.testCases {
		b.WriteString(fmt.Sprintf("## Test: %s\n", tc.description))
		b.WriteString(fmt.Sprintf("Device: %s\n", tc.device))
		b.WriteString(fmt.Sprintf("Started: %s\n", tc.startTime.Format("2006-01-02 15:04:05.000")))
		b.WriteString(fmt.Sprintf("Events: %d\n\n", len(tc.events)))

		if len(tc.events) == 0 {
			b.WriteString("(no events captured)\n\n")
		} else {
			for _, e := range tc.events {
				elapsed := e.timestamp.Sub(tc.startTime)
				b.WriteString(fmt.Sprintf("[+%6.0fms] %s\n", float64(elapsed.Milliseconds()), e.desc))
			}
			b.WriteString("\n")
		}
		b.WriteString("---\n\n")
		// Mark as saved by removing from slice after write succeeds
		_ = i
	}

	// Append to file (or create if doesn't exist)
	f, err := os.OpenFile(m.outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
		return
	}
	defer f.Close()

	_, err = f.WriteString(b.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error saving: %v\n", err)
		return
	}

	// Track saved count and clear to avoid duplicates
	m.savedCount += len(m.testCases)
	m.testCases = []testCase{}
}

func main() {
	outputFile := "input-log.md"
	device := ""

	// Parse args: [-d device] [outputfile]
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-d" && i+1 < len(args) {
			device = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			outputFile = args[i]
		}
	}

	startMode := modeDevice
	if device != "" {
		startMode = modeDescribe
	}

	m := model{
		mode:       startMode,
		device:     device,
		width:      80,
		height:     24,
		outputFile: outputFile,
	}

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}
