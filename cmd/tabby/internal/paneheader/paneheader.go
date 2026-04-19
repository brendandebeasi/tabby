// Package paneheader is the per-pane title/controls header renderer.
// Exported as the `tabby render pane-header` subcommand.
package paneheader

import (
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
	"github.com/brendandebeasi/tabby/pkg/renderer"
)

var (
	sessionID *string
	paneID    *string
	debugMode *bool
)

// Initialize the flag pointers at package load time so tests that exercise
// rendererModel without going through Run's FlagSet don't nil-deref. Run()
// reassigns these via its own FlagSet.
func init() {
	empty := ""
	paneEmpty := ""
	falseVal := false
	sessionID = &empty
	paneID = &paneEmpty
	debugMode = &falseVal
}

var debugLog *log.Logger
var crashLog *log.Logger
var inputLog *log.Logger

var inputLogEnabled bool
var inputLogCheckTime time.Time

func initInputLog() {
	inputLogPath := fmt.Sprintf("/tmp/pane-header-%s-input.log", *paneID)
	f, err := os.OpenFile(inputLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		inputLog = log.New(os.Stderr, "[INPUT] ", log.LstdFlags)
		return
	}
	inputLog = log.New(f, "[input] ", log.LstdFlags|log.Lmicroseconds)
}

func isInputLogEnabled() bool {
	if time.Since(inputLogCheckTime) > 10*time.Second {
		out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_input_log").Output()
		if err != nil {
			inputLogEnabled = false
		} else {
			val := strings.TrimSpace(string(out))
			inputLogEnabled = val == "on" || val == "1" || val == "true"
		}
		inputLogCheckTime = time.Now()
	}
	return inputLogEnabled
}

const (
	longPressThreshold = 350 * time.Millisecond
	doubleTapThreshold = 600 * time.Millisecond
	doubleTapDistance  = 10
	movementThreshold  = 25
)

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
	target    daemon.RenderTarget
	clientID  string // derived from target.Key(); kept for log continuity
	width     int
	height    int
	connected bool

	// Render state from daemon
	content       string
	regions       []daemon.ClickableRegion
	sequenceNum   uint64
	sidebarBg     string
	terminalBg    string
	clientProfile string // "phone" or "desktop"
	headerHeight  int    // rows allocated to this header (0 or 1 = single row)
	// The header pane's own tmux pane ID (for menu positioning)
	headerPaneID string

	mouseDownPos    struct{ X, Y int }
	mouseDownTime   time.Time
	longPressActive bool
	skipNextRelease bool
	lastTapTime     time.Time
	lastTapPos      struct{ X, Y int }

	// Message sending (thread-safe) — pointer avoids go-vet lock-copy warnings
	// since BubbleTea passes models by value
	sendMu *sync.Mutex
}

// Message types
type connectedMsg struct {
	conn   net.Conn
	target daemon.RenderTarget
}

type disconnectedMsg struct{}

type renderMsg struct {
	payload *daemon.RenderPayload
}

type tickMsg time.Time

type longPressMsg struct {
	X int
	Y int
}

// Init implements tea.Model
func (m rendererModel) Init() tea.Cmd {
	return connectCmd()
}

// connectCmd connects to the daemon
func connectCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := renderer.Connect(daemon.SocketPath(*sessionID), 12, 200*time.Millisecond)
		if err != nil {
			debugLog.Printf("Failed to connect to daemon: %v", err)
			return disconnectedMsg{}
		}

		// Resolve the tmux pane id this header renders. No PID fallback.
		paneIDStr := *paneID
		if paneIDStr == "" {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
				paneIDStr = strings.TrimSpace(string(out))
			}
		}
		if paneIDStr == "" {
			debugLog.Printf("Failed to resolve pane id for pane-header renderer")
			conn.Close()
			return disconnectedMsg{}
		}
		target := daemon.RenderTarget{Kind: daemon.TargetPaneHeader, PaneID: paneIDStr}

		return connectedMsg{conn: conn, target: target}
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
		m.target = msg.target
		m.clientID = msg.target.Key()
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
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("RENDER_APPLY seq=%d payload_w=%d payload_h=%d model_w=%d model_h=%d", msg.payload.SequenceNum, msg.payload.Width, msg.payload.Height, m.width, m.height)
		}
		m.content = msg.payload.Content
		m.regions = msg.payload.Regions
		m.sequenceNum = msg.payload.SequenceNum
		m.sidebarBg = msg.payload.SidebarBg
		m.terminalBg = msg.payload.TerminalBg
		m.clientProfile = msg.payload.ActiveClient.Profile
		m.headerHeight = msg.payload.Height
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

	case longPressMsg:
		if m.longPressActive {
			dx := absInt(msg.X - m.mouseDownPos.X)
			dy := absInt(msg.Y - m.mouseDownPos.Y)
			if dx <= movementThreshold && dy <= movementThreshold {
				m.longPressActive = false
				m.skipNextRelease = true
				m.mouseDownTime = time.Time{}
				return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight, true)
			}
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.connected {
			if inputLog != nil && isInputLogEnabled() {
				inputLog.Printf("WINDOW_SIZE width=%d height=%d client=%s", m.width, m.height, m.clientID)
			}
			m.sendResize()
		}
		return m, nil

	case tea.FocusMsg:
		// Click handling uses direct mouse forwarding from tmux bindings.
		// Keep focus events as no-ops.
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
	return m.processMouseClick(x, y, tea.MouseButtonLeft, false)
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
		if button == tea.MouseButtonLeft {
			m.longPressActive = false
			m.skipNextRelease = false
			return m, nil
		}

		m.skipNextRelease = true
		m.longPressActive = false
		m.mouseDownTime = time.Time{}
		return m.processMouseClick(msg.X, msg.Y, button, false)

	case tea.MouseActionRelease:
		if m.skipNextRelease {
			m.skipNextRelease = false
			m.longPressActive = false
			m.mouseDownTime = time.Time{}
			return m, nil
		}

		m.longPressActive = false
		if m.mouseDownTime.IsZero() {
			return m, nil
		}
		elapsed := time.Since(m.mouseDownTime)
		dx := msg.X - m.mouseDownPos.X
		dy := msg.Y - m.mouseDownPos.Y
		m.mouseDownTime = time.Time{}
		if elapsed > 0 && (absInt(dx) > 5 || absInt(dy) > 2) {
			return m, nil
		}
		return m.processMouseClick(m.mouseDownPos.X, m.mouseDownPos.Y, tea.MouseButtonLeft, false)

	case tea.MouseActionMotion:
		if m.longPressActive {
			dx := absInt(msg.X - m.mouseDownPos.X)
			dy := absInt(msg.Y - m.mouseDownPos.Y)
			if dx > movementThreshold || dy > movementThreshold {
				m.longPressActive = false
				m.mouseDownTime = time.Time{}
			}
		}
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
func (m rendererModel) processMouseClick(x, y int, button tea.MouseButton, isSimulated bool) (tea.Model, tea.Cmd) {
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
		SequenceNum:           m.sequenceNum,
		Type:                  "action",
		MouseX:                x,
		MouseY:                y,
		Button:                buttonStr,
		Action:                "press",
		ViewportOffset:        0, // No scrolling for single-line header
		ResolvedAction:        resolvedAction,
		ResolvedTarget:        resolvedTarget,
		PaneID:                paneIDStr,
		SourcePaneID:          m.headerPaneID,
		IsSimulatedRightClick: isSimulated,
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

	// 2-row phone layout: daemon sends two newline-separated lines in Content.
	// Render both rows, padding each to full width.
	if m.headerHeight >= 2 && m.clientProfile == "phone" {
		lines := strings.SplitN(m.content, "\n", 2)
		row0 := lines[0]
		row1 := ""
		if len(lines) > 1 {
			row1 = lines[1]
		}
		row0Width := runewidth.StringWidth(stripAnsi(row0))
		if row0Width < m.width {
			row0 += strings.Repeat(" ", m.width-row0Width)
		}
		row1Width := runewidth.StringWidth(stripAnsi(row1))
		if row1Width < m.width {
			row1 += strings.Repeat(" ", m.width-row1Width)
		}
		return row0 + "\n" + row1
	}

	// Single-row default behavior
	line := m.content
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	lineWidth := runewidth.StringWidth(stripAnsi(line))
	if lineWidth < m.width {
		line += strings.Repeat(" ", m.width-lineWidth)
	}
	return line
}

// receiveLoop reads messages from the daemon.
// Wraps the shared renderer.ReceiveMessages.
func (m *rendererModel) receiveLoop() {
	defer recoverAndLog("receiveLoop")
	renderer.ReceiveMessages(m.conn, func(msg daemon.Message) bool {
		switch msg.Type {
		case daemon.MsgRender:
			var p daemon.RenderPayload
			if renderer.DecodePayload(msg, &p) {
				if inputLog != nil && isInputLogEnabled() {
					inputLog.Printf("RENDER_RECV seq=%d payload_w=%d payload_h=%d", p.SequenceNum, p.Width, p.Height)
				}
				if globalProgram != nil {
					globalProgram.Send(renderMsg{payload: &p})
				}
			}
		case daemon.MsgPong:
			// keep-alive response
		}
		return true
	})
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
		Target:   m.target,
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
		Target:   m.target,
	})
}

func (m *rendererModel) sendResize() {
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf("SEND_RESIZE client=%s width=%d height=%d pane=%s", m.clientID, m.width, m.height, m.headerPaneID)
	}
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgResize,
		Target:   m.target,
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
		Target:   m.target,
		Payload:  input,
	})
}

func (m *rendererModel) sendPing() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgPing,
		Target:   m.target,
	})
}

// Global program reference for message passing from receiveLoop
var globalProgram *tea.Program

func Run(args []string) int {
	fs := flag.NewFlagSet("pane-header", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionID = fs.String("session", "", "tmux session ID")
	paneID = fs.String("pane", "", "tmux pane ID this renderer is for")
	debugMode = fs.Bool("debug", false, "Enable debug logging")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Initialize crash logging early
	initCrashLog()
	initInputLog()
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
		renderer.ResetTerminal()
		os.Stdout.WriteString("\033[0m\033[?25h")
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
		sendMu:       &sync.Mutex{},
	}

	p := tea.NewProgram(model, tea.WithMouseCellMotion(), tea.WithReportFocus())
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
		return 1
	}
	// Final cleanup after normal exit
	resetTerminal()
	return 0
}

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}
