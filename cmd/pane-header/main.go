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
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

var (
	sessionID = flag.String("session", "", "tmux session ID")
	paneID    = flag.String("pane", "", "tmux pane ID this renderer is for")
	debugMode = flag.Bool("debug", false, "Enable debug logging")
)

var debugLog *log.Logger
var crashLog *log.Logger

func initCrashLog() {
	crashLogPath := fmt.Sprintf("/tmp/pane-header-%s-crash.log", *paneID)
	f, err := os.OpenFile(crashLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		crashLog = log.New(os.Stderr, "[CRASH] ", log.LstdFlags)
		return
	}
	crashLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func logCrash(context string, r interface{}) {
	crashLog.Printf("=== CRASH in %s ===", context)
	crashLog.Printf("Pane: %s, Session: %s", *paneID, *sessionID)
	crashLog.Printf("Panic: %v", r)
	crashLog.Printf("Stack trace:\n%s", debug.Stack())
	crashLog.Printf("=== END CRASH ===\n")
}

func recoverAndLog(context string) {
	if r := recover(); r != nil {
		logCrash(context, r)
	}
}

// rendererModel is a minimal Bubbletea model for the pane header
type rendererModel struct {
	conn      net.Conn
	clientID  string
	width     int
	height    int
	connected bool

	// Render state from daemon
	content     string
	regions     []daemon.ClickableRegion
	sequenceNum uint64
	sidebarBg   string
	terminalBg  string

	// The header pane's own tmux pane ID (for menu positioning)
	headerPaneID string

	mouseDownPos  struct{ X, Y int }
	mouseDownTime time.Time

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
	return connectCmd()
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

		// Get unique client ID: "header:{PANE_ID}"
		var clientID string
		if *paneID != "" {
			clientID = "header:" + *paneID
		} else {
			// Fallback: try to get pane ID from tmux
			if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
				paneIDStr := strings.TrimSpace(string(out))
				if paneIDStr != "" {
					clientID = "header:" + paneIDStr
				}
			}
			if clientID == "" {
				// Last resort: use PID
				clientID = fmt.Sprintf("header-renderer-%d", os.Getpid())
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
		m.sequenceNum = msg.payload.SequenceNum
		m.sidebarBg = msg.payload.SidebarBg
		m.terminalBg = msg.payload.TerminalBg
		return m, nil

	case tickMsg:
		// Periodic tick for keep-alive and reconnect (driven by external goroutine)
		if m.connected {
			m.sendPing()
		}
		// Check if target pane still exists -- exit if it was closed
		if *paneID != "" {
			if _, err := exec.Command("tmux", "display-message", "-t", *paneID, "-p", "#{pane_id}").Output(); err != nil {
				debugLog.Printf("Target pane %s no longer exists, exiting", *paneID)
				return m, tea.Quit
			}
		}
		return m, nil // No Cmd: avoid forced alt-screen repaint

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.sendUnsubscribe()
			return m, tea.Quit
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

	case tea.FocusMsg:
		// When the header pane gains focus (user clicked on it), read the stored
		// mouse position and process the click. This is the PRIMARY mechanism for
		// handling clicks on unfocused headers - the shell script stores the click
		// position and selects this pane, triggering FocusMsg.
		if m.connected {
			return m.handleFocusGain()
		}
		return m, nil

	case tea.BlurMsg:
		// Blur is normal - focus redirected to content pane
		return m, nil
	}

	return m, nil
}

// handleFocusGain handles when the header pane gains focus (typically from a click)
// It reads the stored mouse position from tmux options and simulates a click
func (m rendererModel) handleFocusGain() (tea.Model, tea.Cmd) {
	// Read the stored click position from tmux
	xOut, errX := exec.Command("tmux", "show-option", "-gqv", "@tabby_last_click_x").Output()
	yOut, errY := exec.Command("tmux", "show-option", "-gqv", "@tabby_last_click_y").Output()
	paneOut, errP := exec.Command("tmux", "show-option", "-gqv", "@tabby_last_click_pane").Output()

	if errX != nil || errY != nil || errP != nil {
		debugLog.Printf("handleFocusGain: couldn't read click position")
		return m, nil
	}

	xStr := strings.TrimSpace(string(xOut))
	yStr := strings.TrimSpace(string(yOut))
	clickedPane := strings.TrimSpace(string(paneOut))

	// Only process if the click was on this header pane
	if clickedPane != m.headerPaneID {
		debugLog.Printf("handleFocusGain: click was on %s, not %s", clickedPane, m.headerPaneID)
		return m, nil
	}

	x := 0
	y := 0
	if xStr != "" {
		fmt.Sscanf(xStr, "%d", &x)
	}
	if yStr != "" {
		fmt.Sscanf(yStr, "%d", &y)
	}

	debugLog.Printf("handleFocusGain: simulating click at X=%d Y=%d", x, y)

	// Process this as a left click at the stored position
	return m.processMouseClick(x, y, tea.MouseButtonLeft)
}

// handleMouse processes mouse events
func (m rendererModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.connected {
		return m, nil
	}

	if *debugMode {
		debugLog.Printf("=== MOUSE EVENT ===")
		debugLog.Printf("  Position: X=%d Y=%d", msg.X, msg.Y)
		debugLog.Printf("  Button: %v Action: %v Ctrl: %v Shift: %v Alt: %v", msg.Button, msg.Action, msg.Ctrl, msg.Shift, msg.Alt)
	}

	switch msg.Action {
	case tea.MouseActionPress:
		m.mouseDownPos = struct{ X, Y int }{msg.X, msg.Y}
		m.mouseDownTime = time.Now()

		button := msg.Button
		if (msg.Shift || msg.Ctrl) && msg.Button == tea.MouseButtonLeft {
			if *debugMode {
				debugLog.Printf("  Shift/Ctrl+click detected, treating as right-click")
			}
			button = tea.MouseButtonRight
		}
		if button != tea.MouseButtonLeft {
			return m.processMouseClick(msg.X, msg.Y, button)
		}
		return m, nil

	case tea.MouseActionRelease:
		elapsed := time.Since(m.mouseDownTime)
		dx := msg.X - m.mouseDownPos.X
		dy := msg.Y - m.mouseDownPos.Y

		if absInt(dx) <= 2 && absInt(dy) <= 1 && elapsed < 300*time.Millisecond {
			return m.processMouseClick(m.mouseDownPos.X, m.mouseDownPos.Y, tea.MouseButtonLeft)
		}
		return m, nil

	case tea.MouseActionMotion:
		return m, nil
	}

	return m, nil
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// processMouseClick handles the actual click processing
func (m rendererModel) processMouseClick(x, y int, button tea.MouseButton) (tea.Model, tea.Cmd) {
	var resolvedAction, resolvedTarget string

	// For single-line header, Y should always be 0 (no scrolling)
	if *debugMode {
		debugLog.Printf("  Processing click: button=%v X=%d Y=%d", button, x, y)
		debugLog.Printf("  Checking %d regions...", len(m.regions))
	}

	// Check all regions - simple line/column matching
	for i, region := range m.regions {
		if y >= region.StartLine && y <= region.EndLine {
			// Check column range if specified (EndCol=0 means full width)
			endCol := region.EndCol
			if endCol == 0 {
				endCol = m.width
			}
			if x >= region.StartCol && x < endCol {
				resolvedAction = region.Action
				resolvedTarget = region.Target
				if *debugMode {
					debugLog.Printf("  -> Matched region[%d]: lines %d-%d, cols %d-%d, action=%s target=%s",
						i, region.StartLine, region.EndLine, region.StartCol, endCol, region.Action, region.Target)
				}
				break
			}
		}
	}

	if *debugMode {
		if resolvedAction != "" {
			debugLog.Printf("  RESOLVED: action=%s target=%s", resolvedAction, resolvedTarget)
		} else {
			debugLog.Printf("  RESOLVED: (no match)")
		}
	}

	// Get our pane ID for context menus
	paneIDStr := *paneID
	if paneIDStr == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		paneIDStr = strings.TrimSpace(string(out))
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
		ViewportOffset: 0, // No scrolling for single-line header
		ResolvedAction: resolvedAction,
		ResolvedTarget: resolvedTarget,
		PaneID:         paneIDStr,
		SourcePaneID:   m.headerPaneID,
	}

	m.sendInput(input)
	return m, nil
}

// spinnerFrames for loading animation
var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// View implements tea.Model
func (m rendererModel) View() string {
	if !m.connected || m.content == "" {
		return ""
	}

	bgStyle := lipgloss.NewStyle()
	if m.terminalBg != "" {
		bgStyle = bgStyle.Background(lipgloss.Color(m.terminalBg))
	} else if m.sidebarBg != "" {
		bgStyle = bgStyle.Background(lipgloss.Color(m.sidebarBg))
	}

	lines := strings.Split(m.content, "\n")
	var visible []string
	for i := 0; i < m.height && i < len(lines); i++ {
		line := lines[i]
		// Pad line to full width if shorter
		lineWidth := runewidth.StringWidth(stripAnsi(line))
		if lineWidth < m.width {
			line += strings.Repeat(" ", m.width-lineWidth)
		}
		if m.terminalBg != "" || m.sidebarBg != "" {
			visible = append(visible, bgStyle.Render(line))
		} else {
			visible = append(visible, line)
		}
	}

	return strings.Join(visible, "\n")
}

// receiveLoop reads messages from the daemon
func (m *rendererModel) receiveLoop() {
	defer recoverAndLog("receiveLoop")
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
					// Send to the tea program
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
	// Force TrueColor profile for consistency
	colorProfile := "TrueColor"

	m.sendMessage(daemon.Message{
		Type:     daemon.MsgSubscribe,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: colorProfile,
			PaneID:       m.headerPaneID,
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
			PaneID: m.headerPaneID,
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

	// Initialize crash logging early
	initCrashLog()
	defer recoverAndLog("main")

	if *debugMode {
		// Write debug log to file instead of stderr to avoid corrupting the display
		logPath := fmt.Sprintf("/tmp/pane-header-%s.log", *paneID)
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			debugLog = log.New(os.Stderr, "[header] ", log.LstdFlags|log.Lmicroseconds)
		} else {
			debugLog = log.New(logFile, "[header] ", log.LstdFlags|log.Lmicroseconds)
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

	// Detect our own tmux pane ID (the header pane, NOT the content pane from --pane flag).
	// This is used for menu positioning so menus appear at the click location.
	// Use TMUX_PANE env var which tmux sets per-pane, rather than display-message
	// which can return the active pane instead of the pane running this process.
	headerPaneID := os.Getenv("TMUX_PANE")

	debugLog.Printf("Starting pane header renderer for session %s, pane %s (header pane: %s)", *sessionID, *paneID, headerPaneID)
	crashLog.Printf("Header renderer started for pane %s, session %s (header pane: %s)", *paneID, *sessionID, headerPaneID)

	// Force TrueColor mode for accurate theme rendering
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Reset terminal state before starting to clean up any stale modes
	resetTerminal := func() {
		// Disable ALL mouse tracking modes comprehensively
		// 1000=basic mouse tracking, 1002=button motion, 1003=any motion (cell motion)
		// 1004=focus events, 1005=UTF-8 encoding, 1006=SGR encoding, 1015=URXVT encoding
		fmt.Print("\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1005l\033[?1006l\033[?1015l")
		// Exit alternate screen buffer if active
		fmt.Print("\033[?1049l")
		// Disable bracketed paste mode
		fmt.Print("\033[?2004l")
		// Reset to normal mode and show cursor
		fmt.Print("\033[0m\033[?25h")
		// Flush output
		os.Stdout.Sync()
	}

	// Clean start
	resetTerminal()

	// Ensure cleanup on exit
	defer resetTerminal()

	model := rendererModel{
		width:        80,
		height:       1, // Single line for header
		headerPaneID: headerPaneID,
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	globalProgram = p

	// External ticker for keepalive/pane-check (avoids returning Cmds from Update
	// which would force alt-screen repaints and cause visible flicker on 1-line panes)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			p.Send(tickMsg(time.Now()))
		}
	}()

	// Handle signals - reset terminal immediately on signal to prevent stuck mouse modes
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer recoverAndLog("signal-handler")
		<-sigCh
		// Reset terminal FIRST before trying to quit tea program
		resetTerminal()
		if p != nil {
			p.Send(tea.Quit())
		}
		// Give tea a moment to quit gracefully, then force reset again
		time.Sleep(100 * time.Millisecond)
		resetTerminal()
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		resetTerminal() // Ensure cleanup even on error
		os.Exit(1)
	}
	// Final cleanup after normal exit
	resetTerminal()
}

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}
