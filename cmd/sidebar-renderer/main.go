package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/b/tmux-tabs/pkg/daemon"
)

var (
	sessionID = flag.String("session", "", "tmux session ID")
	windowID  = flag.String("window", "", "tmux window ID this renderer is for")
	debug     = flag.Bool("debug", false, "Enable debug logging")
)

var debugLog *log.Logger

// rendererModel is a minimal Bubbletea model for the renderer
type rendererModel struct {
	conn       net.Conn
	clientID   string
	width      int
	height     int
	connected  bool

	// Render state from daemon
	content        string
	pinnedContent  string
	pinnedHeight   int
	regions        []daemon.ClickableRegion
	viewportOffset int
	totalLines     int
	sequenceNum    uint64

	// Viewport scroll state
	scrollY int

	// Message sending (thread-safe)
	sendMu sync.Mutex
}

// Message types
type connectedMsg struct {
	conn     net.Conn
	clientID string
}

type disconnectedMsg struct{}

type renderMsg struct {
	payload *daemon.RenderPayload
}

type tickMsg time.Time

// Init implements tea.Model
func (m rendererModel) Init() tea.Cmd {
	return tea.Batch(
		connectCmd(),
		tickCmd(),
	)
}

// connectCmd connects to the daemon
func connectCmd() tea.Cmd {
	return func() tea.Msg {
		sockPath := daemon.SocketPath(*sessionID)

		// Try connecting with retry
		var conn net.Conn
		var err error
		for i := 0; i < 10; i++ {
			conn, err = net.Dial("unix", sockPath)
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			debugLog.Printf("Failed to connect to daemon: %v", err)
			return disconnectedMsg{}
		}

		// Get unique client ID
		var clientID string
		if *windowID != "" {
			// Use provided window ID (passed from toggle script)
			clientID = *windowID
		} else {
			// Fallback: try to get window ID from tmux
			if out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
				windowIDStr := strings.TrimSpace(string(out))
				if windowIDStr != "" {
					clientID = windowIDStr
				}
			}
			if clientID == "" {
				// Last resort: use PID
				clientID = fmt.Sprintf("renderer-%d", os.Getpid())
			}
		}

		return connectedMsg{conn: conn, clientID: clientID}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update implements tea.Model
func (m rendererModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectedMsg:
		m.conn = msg.conn
		m.clientID = msg.clientID
		m.connected = true
		debugLog.Printf("Connected as %s", m.clientID)

		// Start receiver goroutine
		go m.receiveLoop()

		// Send subscribe with initial size
		m.sendSubscribe()
		return m, nil

	case disconnectedMsg:
		m.connected = false
		debugLog.Printf("Disconnected from daemon")
		// Try to reconnect after a delay
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return connectCmd()()
		})

	case renderMsg:
		m.content = msg.payload.Content
		m.pinnedContent = msg.payload.PinnedContent
		m.pinnedHeight = msg.payload.PinnedHeight
		m.regions = msg.payload.Regions
		m.totalLines = msg.payload.TotalLines
		m.sequenceNum = msg.payload.SequenceNum
		// Clamp scroll
		maxScroll := m.totalLines - (m.height - m.pinnedHeight)
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollY > maxScroll {
			m.scrollY = maxScroll
		}
		return m, nil

	case tickMsg:
		// Periodic tick for keep-alive and reconnect
		if m.connected {
			m.sendPing()
		}
		return m, tickCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.sendUnsubscribe()
			return m, tea.Quit
		case "up", "k":
			if m.scrollY > 0 {
				m.scrollY--
				m.sendViewportUpdate()
			}
		case "down", "j":
			maxScroll := m.totalLines - (m.height - m.pinnedHeight)
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scrollY < maxScroll {
				m.scrollY++
				m.sendViewportUpdate()
			}
		case "enter":
			// Send enter key to daemon
			m.sendInput(&daemon.InputPayload{
				Type: "key",
				Key:  "enter",
			})
		case "r":
			// Refresh
			m.sendInput(&daemon.InputPayload{
				Type: "key",
				Key:  "r",
			})
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.connected {
			m.sendResize()
		}
		return m, nil
	}

	return m, nil
}

// handleMouse processes mouse events
func (m rendererModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.connected {
		return m, nil
	}

	// Calculate which area was clicked
	scrollableHeight := m.height - m.pinnedHeight
	clickedArea := "scrollable"
	pinnedRelY := 0

	if msg.Y >= scrollableHeight {
		clickedArea = "pinned"
		pinnedRelY = msg.Y - scrollableHeight
	}

	// Translate Y to content line (accounting for scroll)
	contentY := msg.Y + m.scrollY

	// Check for scroll
	if msg.Button == tea.MouseButtonWheelUp {
		if m.scrollY > 0 {
			m.scrollY--
			m.sendViewportUpdate()
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		maxScroll := m.totalLines - scrollableHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollY < maxScroll {
			m.scrollY++
			m.sendViewportUpdate()
		}
		return m, nil
	}

	// Only handle clicks, not moves
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}

	// Find which region was clicked (if any) in the scrollable area
	var resolvedAction, resolvedTarget string
	if clickedArea == "scrollable" {
		for _, region := range m.regions {
			if contentY >= region.StartLine && contentY <= region.EndLine {
				resolvedAction = region.Action
				resolvedTarget = region.Target
				break
			}
		}
	}

	// Get our pane ID for context menus
	paneID := m.clientID
	if paneID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		paneID = strings.TrimSpace(string(out))
	}

	button := ""
	switch msg.Button {
	case tea.MouseButtonLeft:
		button = "left"
	case tea.MouseButtonRight:
		button = "right"
	case tea.MouseButtonMiddle:
		button = "middle"
	}

	input := &daemon.InputPayload{
		SequenceNum:    m.sequenceNum,
		Type:           "action",
		MouseX:         msg.X,
		MouseY:         msg.Y,
		Button:         button,
		Action:         "press",
		ClickedArea:    clickedArea,
		PinnedRelY:     pinnedRelY,
		ViewportOffset: m.scrollY,
		ResolvedAction: resolvedAction,
		ResolvedTarget: resolvedTarget,
		PaneID:         paneID,
	}

	m.sendInput(input)
	return m, nil
}

// spinnerFrames for loading animation
var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// View implements tea.Model
func (m rendererModel) View() string {
	if !m.connected {
		// Show animated loading indicator
		frame := spinnerFrames[int(time.Now().UnixMilli()/100)%len(spinnerFrames)]
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))
		return style.Render(fmt.Sprintf(" %s Loading...", frame))
	}

	// Calculate viewport
	scrollableHeight := m.height - m.pinnedHeight
	if scrollableHeight < 1 {
		scrollableHeight = 1
	}

	// Split content into lines and extract visible portion
	lines := strings.Split(m.content, "\n")
	visibleStart := m.scrollY
	visibleEnd := visibleStart + scrollableHeight

	if visibleStart >= len(lines) {
		visibleStart = len(lines) - 1
		if visibleStart < 0 {
			visibleStart = 0
		}
	}
	if visibleEnd > len(lines) {
		visibleEnd = len(lines)
	}

	// Build visible content
	var visible []string
	for i := visibleStart; i < visibleEnd && i < len(lines); i++ {
		visible = append(visible, lines[i])
	}

	// Pad if needed
	for len(visible) < scrollableHeight {
		visible = append(visible, "")
	}

	scrollable := strings.Join(visible, "\n")

	// Combine with pinned content
	return scrollable + m.pinnedContent
}

// receiveLoop reads messages from the daemon
func (m *rendererModel) receiveLoop() {
	scanner := bufio.NewScanner(m.conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		var msg daemon.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case daemon.MsgRender:
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var payload daemon.RenderPayload
				if json.Unmarshal(payloadBytes, &payload) == nil {
					// Send to the tea program via a channel or direct update
					// For simplicity, we'll use the global program reference
					if globalProgram != nil {
						globalProgram.Send(renderMsg{payload: &payload})
					}
				}
			}
		case daemon.MsgPong:
			// Keep-alive response
		}
	}

	// Connection closed
	if globalProgram != nil {
		globalProgram.Send(disconnectedMsg{})
	}
}

// Send methods
func (m *rendererModel) sendMessage(msg daemon.Message) {
	if m.conn == nil {
		return
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	m.conn.SetWriteDeadline(time.Now().Add(time.Second))
	m.conn.Write(append(data, '\n'))
}

func (m *rendererModel) sendSubscribe() {
	// Detect color profile
	colorProfile := "ANSI256"
	if termenv.ColorProfile() == termenv.TrueColor {
		colorProfile = "TrueColor"
	} else if termenv.ColorProfile() == termenv.Ascii {
		colorProfile = "Ascii"
	} else if termenv.ColorProfile() == termenv.ANSI {
		colorProfile = "ANSI"
	}

	m.sendMessage(daemon.Message{
		Type:     daemon.MsgSubscribe,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: colorProfile,
		},
	})
}

func (m *rendererModel) sendUnsubscribe() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgUnsubscribe,
		ClientID: m.clientID,
	})
}

func (m *rendererModel) sendResize() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgResize,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:  m.width,
			Height: m.height,
		},
	})
}

func (m *rendererModel) sendViewportUpdate() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgViewportUpdate,
		ClientID: m.clientID,
		Payload: daemon.ViewportUpdatePayload{
			ViewportOffset: m.scrollY,
		},
	})
}

func (m *rendererModel) sendInput(input *daemon.InputPayload) {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgInput,
		ClientID: m.clientID,
		Payload:  input,
	})
}

func (m *rendererModel) sendPing() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgPing,
		ClientID: m.clientID,
	})
}

// Global program reference for message passing from receiveLoop
var globalProgram *tea.Program

func main() {
	flag.Parse()

	if *debug {
		debugLog = log.New(os.Stderr, "[renderer] ", log.LstdFlags|log.Lmicroseconds)
	} else {
		debugLog = log.New(os.Stderr, "", 0)
	}

	// Get session ID from environment if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	debugLog.Printf("Starting renderer for session %s", *sessionID)

	// Force ANSI256 color mode
	lipgloss.SetColorProfile(termenv.ANSI256)

	model := rendererModel{
		width:  80,
		height: 24,
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if p != nil {
			p.Send(tea.Quit())
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Helper to convert string to int
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
