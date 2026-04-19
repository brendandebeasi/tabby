// Package windowheader is the top-of-window navigation bar renderer.
// Exported as the `tabby render window-header` subcommand.
package windowheader

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
	windowID  *string
	debugMode *bool
)

// Initialize the flag pointers at package load time so tests (which
// exercise the rendererModel without going through Run's FlagSet) don't
// nil-deref when they read *debugMode etc. Run() reassigns these via its
// own FlagSet.
func init() {
	empty := ""
	falseVal := false
	sessionID = &empty
	winEmpty := ""
	windowID = &winEmpty
	debugMode = &falseVal
}

var debugLog *log.Logger
var crashLog *log.Logger
var inputLog *log.Logger

var inputLogEnabled bool
var inputLogCheckTime time.Time

func initInputLog() {
	inputLogPath := fmt.Sprintf("/tmp/window-header-%s-input.log", *windowID)
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
	crashLogPath := fmt.Sprintf("/tmp/window-header-%s-crash.log", *windowID)
	f, err := os.OpenFile(crashLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		crashLog = log.New(os.Stderr, "[CRASH] ", log.LstdFlags)
		return
	}
	crashLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func logCrash(context string, r interface{}) {
	crashLog.Printf("=== CRASH in %s ===", context)
	crashLog.Printf("Window: %s, Session: %s", *windowID, *sessionID)
	crashLog.Printf("Panic: %v", r)
	crashLog.Printf("Stack trace:\n%s", debug.Stack())
	crashLog.Printf("=== END CRASH ===\n")
}

func recoverAndLog(context string) {
	if r := recover(); r != nil {
		logCrash(context, r)
	}
}

// rendererModel is a minimal Bubbletea model for the window header
type rendererModel struct {
	conn      net.Conn
	target    daemon.RenderTarget
	clientID  string // derived from target.Key(); kept for log continuity
	width     int
	height    int
	connected bool

	// Debounce generation counter for WindowSizeMsg (see resizeFlushMsg).
	resizeGen int

	// Render state from daemon
	content       string
	regions       []daemon.ClickableRegion
	sequenceNum   uint64
	sidebarBg     string
	terminalBg    string
	clientProfile string // "phone" or "desktop"
	headerHeight  int    // rows allocated to this header (0 or 1 = single row)
	// Window/pane tracking (received from daemon payload for title text)
	activePaneID   string
	activeWindowID string
	collapsed      bool
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

// resizeFlushMsg fires after a debounce window; gen is compared against
// the model's resizeGen so only the most recent WindowSizeMsg in a burst
// triggers an outgoing MsgResize.
type resizeFlushMsg struct {
	gen int
}

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

		// Resolve the tmux window id this header renders. No PID fallback.
		winIDStr := *windowID
		if winIDStr == "" {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
				winIDStr = strings.TrimSpace(string(out))
			}
		}
		if winIDStr == "" {
			debugLog.Printf("Failed to resolve window id for window-header renderer")
			conn.Close()
			return disconnectedMsg{}
		}
		target := daemon.RenderTarget{Kind: daemon.TargetWindowHeader, WindowID: winIDStr}

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
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("CONNECTED client=%s window=%s pane=%s", m.clientID, *windowID, m.headerPaneID)
		}

		// Start receiver goroutine
		go m.receiveLoop()

		// Send subscribe with initial size
		m.sendSubscribe()
		return m, nil

	case disconnectedMsg:
		m.connected = false
		debugLog.Printf("Disconnected from daemon")
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("DISCONNECTED client=%s window=%s pane=%s", m.clientID, *windowID, m.headerPaneID)
		}
		// Try to reconnect after a delay
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return connectCmd()()
		})

	case renderMsg:
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("RENDER_APPLY seq=%d payload_w=%d payload_h=%d model_w=%d model_h=%d", msg.payload.SequenceNum, msg.payload.Width, msg.payload.Height, m.width, m.height)
		}
		// Dedup: if the visible content and height are unchanged, update only
		// non-visual state (regions, sequence, theme) so View returns the same
		// string and Bubble Tea's inline renderer suppresses the redraw.
		if msg.payload.Content == m.content && msg.payload.Height == m.headerHeight {
			m.regions = msg.payload.Regions
			m.sequenceNum = msg.payload.SequenceNum
			m.sidebarBg = msg.payload.SidebarBg
			m.terminalBg = msg.payload.TerminalBg
			m.clientProfile = msg.payload.ActiveClient.Profile
			return m, nil
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
		// Check if target window still exists -- exit if it was closed
		if *windowID != "" {
			if _, err := exec.Command("tmux", "display-message", "-t", *windowID, "-p", "#{window_id}").Output(); err != nil {
				debugLog.Printf("Target window %s no longer exists, exiting", *windowID)
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
		if !m.connected {
			return m, nil
		}
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("WINDOW_SIZE width=%d height=%d client=%s", m.width, m.height, m.clientID)
		}
		m.resizeGen++
		gen := m.resizeGen
		return m, tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
			return resizeFlushMsg{gen: gen}
		})

	case resizeFlushMsg:
		if msg.gen == m.resizeGen && m.connected {
			if inputLog != nil && isInputLogEnabled() {
				inputLog.Printf("WINDOW_SIZE_FLUSH width=%d height=%d client=%s gen=%d", m.width, m.height, m.clientID, msg.gen)
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
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf("CLICK_RESOLVE client=%s seq=%d button=%v x=%d y=%d action=%s target=%s window=%s pane=%s", m.clientID, m.sequenceNum, button, x, y, resolvedAction, resolvedTarget, *windowID, m.headerPaneID)
	}

	// For window-header, target reference is the window ID.
	paneIDStr := *windowID
	if paneIDStr == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
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
		ViewportOffset:        0,
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

	// Multi-row phone layout: daemon sends newline-separated rows in Content.
	// Render each row, padding to full width.
	if m.headerHeight >= 2 {
		lines := strings.Split(m.content, "\n")
		out := make([]string, 0, len(lines))
		for _, row := range lines {
			rowWidth := runewidth.StringWidth(stripAnsi(row))
			if rowWidth < m.width {
				row += strings.Repeat(" ", m.width-rowWidth)
			}
			out = append(out, row)
		}
		return strings.Join(out, "\n")
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
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("SEND_DROP reason=nil_conn type=%s client=%s", msg.Type, m.clientID)
		}
		return
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("SEND_DROP reason=marshal type=%s client=%s err=%v", msg.Type, m.clientID, err)
		}
		return
	}
	deadline := time.Now().Add(time.Second)
	if err := m.conn.SetWriteDeadline(deadline); err != nil {
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("SEND_WARN reason=deadline type=%s client=%s err=%v", msg.Type, m.clientID, err)
		}
	}
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf("SEND_MSG type=%s client=%s bytes=%d", msg.Type, m.clientID, len(data)+1)
	}
	if _, err := m.conn.Write(append(data, '\n')); err != nil {
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("SEND_ERR type=%s client=%s err=%v", msg.Type, m.clientID, err)
		}
		if *debugMode {
			debugLog.Printf("sendMessage write failed type=%s client=%s err=%v", msg.Type, m.clientID, err)
		}
		return
	}
	if inputLog != nil && isInputLogEnabled() {
		if msg.Type == daemon.MsgInput {
			if payload, ok := msg.Payload.(*daemon.InputPayload); ok && payload != nil {
				inputLog.Printf("SEND_INPUT_OK client=%s action=%s resolved=%s target=%s x=%d y=%d pane=%s sourcePane=%s", m.clientID, payload.Action, payload.ResolvedAction, payload.ResolvedTarget, payload.MouseX, payload.MouseY, payload.PaneID, payload.SourcePaneID)
			}
		}
	}
}

func (m *rendererModel) sendSubscribe() {
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
	fs := flag.NewFlagSet("window-header", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionID = fs.String("session", "", "tmux session ID")
	windowID = fs.String("window", "", "tmux window ID this renderer is for")
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
		logPath := fmt.Sprintf("/tmp/window-header-%s.log", *windowID)
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

	// Detect our own tmux pane ID (the header pane itself).
	// Used for menu positioning so menus appear at the click location.
	headerPaneID := os.Getenv("TMUX_PANE")

	debugLog.Printf("Starting window header renderer for session %s, window %s (header pane: %s)", *sessionID, *windowID, headerPaneID)
	crashLog.Printf("Window header renderer started for window %s, session %s (header pane: %s)", *windowID, *sessionID, headerPaneID)

	// Force TrueColor mode for accurate theme rendering
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Reset terminal state before starting to clean up any stale modes
	resetTerminal := func() {
		renderer.ResetTerminal()
		os.Stdout.WriteString("\033[0m\033[?25h")
		os.Stdout.Sync()
	}

	resetTerminal()
	defer resetTerminal()

	model := rendererModel{
		width:        80,
		height:       1,
		headerPaneID: headerPaneID,
		sendMu:       &sync.Mutex{},
	}

	p := tea.NewProgram(model, tea.WithMouseCellMotion(), tea.WithReportFocus())
	globalProgram = p

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			p.Send(tickMsg(time.Now()))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer recoverAndLog("signal-handler")
		<-sigCh
		resetTerminal()
		if p != nil {
			p.Send(tea.Quit())
		}
		time.Sleep(100 * time.Millisecond)
		resetTerminal()
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		resetTerminal()
		return 1
	}
	resetTerminal()
	return 0
}

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}
