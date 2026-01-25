package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

// Long-press detection thresholds
const (
	longPressThreshold  = 500 * time.Millisecond
	doubleTapThreshold  = 300 * time.Millisecond
	doubleTapDistance   = 3 // max pixels between taps
	movementThreshold   = 5 // pixels
)

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
	pinnedRegions  []daemon.ClickableRegion
	viewportOffset int
	totalLines     int
	sequenceNum    uint64

	// Viewport scroll state
	scrollY int

	// Long-press detection for iOS/mobile right-click
	mouseDownTime   time.Time
	mouseDownPos    struct{ X, Y int }
	longPressActive bool

	// Double-tap detection (alternative right-click for iOS)
	lastTapTime time.Time
	lastTapPos  struct{ X, Y int }

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

type longPressMsg struct {
	X, Y int
}

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
		m.regions = msg.payload.Regions
		m.totalLines = msg.payload.TotalLines
		m.sequenceNum = msg.payload.SequenceNum

		// SIMPLIFIED: Clamp scroll based on simple height calculation
		maxScroll := m.totalLines - m.height
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollY > maxScroll {
			m.scrollY = maxScroll
		}

		// Debug logging
		if *debug {
			contentLines := strings.Count(m.content, "\n")
			debugLog.Printf("=== RENDER PAYLOAD ===")
			debugLog.Printf("  SequenceNum: %d", m.sequenceNum)
			debugLog.Printf("  Content: %d lines, %d bytes", contentLines, len(m.content))
			debugLog.Printf("  Regions: %d total", len(m.regions))
			debugLog.Printf("  TotalLines: %d, maxScroll: %d", m.totalLines, maxScroll)
			// Log first few regions for debugging
			for i, r := range m.regions {
				if i < 10 {
					debugLog.Printf("  Region[%d]: lines %d-%d, cols %d-%d, action=%s target=%s",
						i, r.StartLine, r.EndLine, r.StartCol, r.EndCol, r.Action, r.Target)
				}
			}
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

	case longPressMsg:
		// Long-press timer fired - check if still valid
		if m.longPressActive && msg.X == m.mouseDownPos.X && msg.Y == m.mouseDownPos.Y {
			if *debug {
				debugLog.Printf("Long-press detected at X=%d Y=%d", msg.X, msg.Y)
			}
			// Treat as right-click
			return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight)
		}
		return m, nil

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

// abs returns absolute value of an int
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// handleMouse processes mouse events with long-press detection for iOS/mobile
func (m rendererModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.connected {
		return m, nil
	}

	// Debug logging for mouse events
	if *debug {
		debugLog.Printf("=== MOUSE EVENT ===")
		debugLog.Printf("  Position: X=%d Y=%d", msg.X, msg.Y)
		debugLog.Printf("  Button: %v Action: %v Ctrl: %v Shift: %v Alt: %v", msg.Button, msg.Action, msg.Ctrl, msg.Shift, msg.Alt)
		debugLog.Printf("  Viewport: height=%d scrollY=%d totalLines=%d", m.height, m.scrollY, m.totalLines)
		debugLog.Printf("  LongPressActive: %v", m.longPressActive)
	}

	// Check for scroll wheel
	if msg.Button == tea.MouseButtonWheelUp {
		if m.scrollY > 0 {
			m.scrollY--
			m.sendViewportUpdate()
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		maxScroll := m.totalLines - m.height
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollY < maxScroll {
			m.scrollY++
			m.sendViewportUpdate()
		}
		return m, nil
	}

	// Handle mouse lifecycle for long-press detection
	switch msg.Action {
	case tea.MouseActionPress:
		// Shift+click or Ctrl+click = right-click (alternative for tmux which intercepts right-click)
		if (msg.Shift || msg.Ctrl) && msg.Button == tea.MouseButtonLeft {
			if *debug {
				debugLog.Printf("  Shift/Ctrl+click detected (Shift=%v Ctrl=%v), treating as right-click", msg.Shift, msg.Ctrl)
			}
			return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight)
		}

		if msg.Button == tea.MouseButtonLeft {
			// Start long-press detection
			m.mouseDownTime = time.Now()
			m.mouseDownPos = struct{ X, Y int }{msg.X, msg.Y}
			m.longPressActive = true
			if *debug {
				debugLog.Printf("  Starting long-press timer at X=%d Y=%d", msg.X, msg.Y)
			}
			// Start a timer for long-press
			return m, tea.Tick(longPressThreshold, func(t time.Time) tea.Msg {
				return longPressMsg{X: msg.X, Y: msg.Y}
			})
		}
		// Right or middle click - process immediately
		return m.processMouseClick(msg.X, msg.Y, msg.Button)

	case tea.MouseActionMotion:
		// Check if movement exceeds threshold - cancel long-press if so
		if m.longPressActive {
			dx := abs(msg.X - m.mouseDownPos.X)
			dy := abs(msg.Y - m.mouseDownPos.Y)
			if dx > movementThreshold || dy > movementThreshold {
				if *debug {
					debugLog.Printf("  Long-press cancelled due to movement: dx=%d dy=%d", dx, dy)
				}
				m.longPressActive = false
				m.mouseDownTime = time.Time{}
			}
		}
		return m, nil

	case tea.MouseActionRelease:
		if m.longPressActive {
			// Release before long-press timer fired - check for double-tap or single click
			elapsed := time.Since(m.mouseDownTime)
			m.longPressActive = false
			m.mouseDownTime = time.Time{}

			if elapsed < longPressThreshold {
				// Check for double-tap (right-click alternative for iOS)
				timeSinceLastTap := time.Since(m.lastTapTime)
				dx := abs(msg.X - m.lastTapPos.X)
				dy := abs(msg.Y - m.lastTapPos.Y)

				if timeSinceLastTap < doubleTapThreshold && dx <= doubleTapDistance && dy <= doubleTapDistance {
					// Double-tap detected - treat as right-click
					if *debug {
						debugLog.Printf("  Double-tap detected (interval=%v, distance=%d,%d) -> right-click", timeSinceLastTap, dx, dy)
					}
					m.lastTapTime = time.Time{} // Reset to prevent triple-tap
					return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight)
				}

				// Single tap - record for potential double-tap and process as left-click
				if *debug {
					debugLog.Printf("  Quick click (elapsed=%v)", elapsed)
				}
				m.lastTapTime = time.Now()
				m.lastTapPos = struct{ X, Y int }{msg.X, msg.Y}
				return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonLeft)
			}
			// Long-press would have already triggered via timer
		}
		return m, nil
	}

	return m, nil
}

// processMouseClick handles the actual click processing after determining button type
func (m rendererModel) processMouseClick(x, y int, button tea.MouseButton) (tea.Model, tea.Cmd) {
	var resolvedAction, resolvedTarget string

	// Translate Y to content line (accounting for scroll)
	contentY := y + m.scrollY

	if *debug {
		debugLog.Printf("  Processing click: button=%v Y=%d ContentY=%d (scroll=%d)", button, y, contentY, m.scrollY)
	}

	// Check all regions with simple Y-based matching
	if *debug {
		debugLog.Printf("  Checking %d regions...", len(m.regions))
	}
	for i, region := range m.regions {
		if contentY >= region.StartLine && contentY <= region.EndLine {
			// Check column range if specified (EndCol=0 means full width)
			endCol := region.EndCol
			if endCol == 0 {
				endCol = m.width
			}
			if x >= region.StartCol && x < endCol {
				resolvedAction = region.Action
				resolvedTarget = region.Target
				if *debug {
					debugLog.Printf("  -> Matched region[%d]: lines %d-%d, cols %d-%d, action=%s target=%s",
						i, region.StartLine, region.EndLine, region.StartCol, endCol, region.Action, region.Target)
				}
				break
			}
		}
	}

	if *debug {
		if resolvedAction != "" {
			debugLog.Printf("  RESOLVED: action=%s target=%s", resolvedAction, resolvedTarget)
		} else {
			debugLog.Printf("  RESOLVED: (no match)")
		}
	}

	// Get our pane ID for context menus
	paneID := m.clientID
	if paneID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		paneID = strings.TrimSpace(string(out))
	}

	buttonStr := ""
	switch button {
	case tea.MouseButtonLeft:
		buttonStr = "left"
	case tea.MouseButtonRight:
		buttonStr = "right"
	case tea.MouseButtonMiddle:
		buttonStr = "middle"
	}

	input := &daemon.InputPayload{
		SequenceNum:    m.sequenceNum,
		Type:           "action",
		MouseX:         x,
		MouseY:         y,
		Button:         buttonStr,
		Action:         "press",
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

	// SIMPLIFIED: Just show visible window of content (no pinned section)
	lines := strings.Split(m.content, "\n")
	visibleStart := m.scrollY
	visibleEnd := visibleStart + m.height

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
	for len(visible) < m.height {
		visible = append(visible, "")
	}

	return strings.Join(visible, "\n")
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

	// Note: BubbleZone initialization removed - zone detection happens in daemon only.
	// The daemon extracts zone bounds and sends accurate ClickableRegions.

	if *debug {
		// Write debug log to file instead of stderr to avoid corrupting the display
		logPath := fmt.Sprintf("/tmp/sidebar-renderer-%s.log", *windowID)
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			debugLog = log.New(os.Stderr, "[renderer] ", log.LstdFlags|log.Lmicroseconds)
		} else {
			debugLog = log.New(logFile, "[renderer] ", log.LstdFlags|log.Lmicroseconds)
		}
	} else {
		debugLog = log.New(io.Discard, "", 0)
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
