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
	"sort"
	"strconv"
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
	windowID  = flag.String("window", "", "tmux window ID this renderer is for")
	debugMode = flag.Bool("debug", false, "Enable debug logging")
)

var debugLog *log.Logger
var crashLog *log.Logger
var inputLog *log.Logger

var inputLogEnabled bool
var inputLogCheckTime time.Time

func initInputLog() {
	inputLogPath := fmt.Sprintf("/tmp/sidebar-renderer-%s-input.log", *windowID)
	f, err := os.OpenFile(inputLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		inputLog = log.New(os.Stderr, "[INPUT] ", log.LstdFlags)
		return
	}
	inputLog = log.New(f, "[input] ", log.LstdFlags|log.Lmicroseconds)
}

// isInputLogEnabled checks if input logging is enabled via tmux option @tabby_input_log
// Caches result for 10 seconds to avoid excessive tmux calls
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

func initCrashLog() {
	crashLogPath := fmt.Sprintf("/tmp/sidebar-renderer-%s-crash.log", *windowID)
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

// Long-press detection thresholds
const (
	longPressThreshold = 350 * time.Millisecond // Faster for better responsiveness
	doubleTapThreshold = 600 * time.Millisecond // Increased for mobile
	doubleTapDistance  = 10                     // max pixels between taps (increased for mobile/touch)
	movementThreshold  = 25                     // pixels - very lenient for mobile finger drift
)

// rendererModel is a minimal Bubbletea model for the renderer
type rendererModel struct {
	conn      net.Conn
	clientID  string
	width     int
	height    int
	connected bool

	// Render state from daemon
	content        string
	pinnedContent  string
	pinnedHeight   int
	regions        []daemon.ClickableRegion
	pinnedRegions  []daemon.ClickableRegion
	viewportOffset int
	totalLines     int
	sequenceNum    uint64
	isTouchMode    bool // Touch mode status from coordinator
	sidebarBg      string
	terminalBg     string

	// Viewport scroll state
	scrollY int

	// Long-press detection for iOS/mobile right-click
	mouseDownTime   time.Time
	mouseDownPos    struct{ X, Y int }
	mouseDownValid  bool
	longPressActive bool
	skipNextRelease bool // Set when menu closes to prevent false drag detection

	// Double-tap detection (alternative right-click for iOS)
	lastTapTime time.Time
	lastTapPos  struct{ X, Y int }

	// Message sending (thread-safe)
	sendMu sync.Mutex

	// Context menu overlay state
	menuShowing    bool
	menuTitle      string
	menuItems      []daemon.MenuItemPayload
	menuY          int  // Screen Y where menu was requested
	menuHighlight  int  // Currently highlighted item index (-1 = none)
	menuDragActive bool // First interaction after menu appears uses release-to-select

	// Sidebar pane ID for focus management (context menu keyboard input)
	sidebarPaneID string

	pickerShowing      bool
	pickerTitle        string
	pickerScope        string
	pickerTarget       string
	pickerOptions      []daemon.MarkerOptionPayload
	pickerFiltered     []int
	pickerQuery        string
	pickerCursor       int
	pickerScroll       int
	pickerMouseDown    bool
	pickerInputFocused bool
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

type menuMsg struct {
	payload *daemon.MenuPayload
}

type markerPickerMsg struct {
	payload *daemon.MarkerPickerPayload
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
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("CONNECTED client=%s", m.clientID)
		}

		// Start receiver goroutine
		go m.receiveLoop()

		// Send subscribe with initial size
		m.sendSubscribe()

		// Refresh tmux mouse state after reconnection
		// This fixes clicks not working after daemon restart
		go func() {
			time.Sleep(100 * time.Millisecond)
			// Toggle mouse mode to force tmux to re-sync with terminal
			exec.Command("tmux", "set", "-g", "mouse", "off").Run()
			exec.Command("tmux", "set", "-g", "mouse", "on").Run()
			// Refresh all clients to ensure mouse tracking is active
			if out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output(); err == nil {
				for _, tty := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					if tty != "" {
						exec.Command("tmux", "refresh-client", "-t", tty, "-S").Run()
					}
				}
			}
		}()
		return m, nil

	case disconnectedMsg:
		m.connected = false
		m.mouseDownValid = false
		m.longPressActive = false
		m.mouseDownTime = time.Time{}
		// Close old connection to clean up resources
		if m.conn != nil {
			m.conn.Close()
			m.conn = nil
		}
		debugLog.Printf("Disconnected from daemon, will retry in 1s")
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("DISCONNECTED")
		}
		// Try to reconnect after a delay
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return connectCmd()()
		})

	case tea.BlurMsg:
		m.mouseDownValid = false
		m.longPressActive = false
		m.mouseDownTime = time.Time{}
		if m.menuShowing {
			m.menuDismiss()
			m.menuShowing = false
			m.menuDragActive = false
		}
		return m, nil

	case menuMsg:
		m.pickerShowing = false
		m.menuShowing = true
		m.menuTitle = msg.payload.Title
		m.menuItems = msg.payload.Items
		m.menuY = msg.payload.Y
		m.menuHighlight = -1
		m.menuDragActive = true // Assume right button still held for drag-to-select
		// Focus the sidebar pane so keyboard shortcuts reach the renderer
		m.menuFocusSidebar()
		return m, nil

	case markerPickerMsg:
		m.menuShowing = false
		m.pickerShowing = true
		m.pickerTitle = msg.payload.Title
		m.pickerScope = msg.payload.Scope
		m.pickerTarget = msg.payload.Target
		m.pickerOptions = append([]daemon.MarkerOptionPayload(nil), msg.payload.Options...)
		m.pickerQuery = ""
		m.pickerCursor = 0
		m.pickerScroll = 0
		m.pickerMouseDown = false
		m.pickerInputFocused = true
		m.pickerApplyFilter()
		m.menuFocusSidebar()
		return m, nil

	case renderMsg:
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("RENDER_APPLY seq=%d lines=%d regions=%d", msg.payload.SequenceNum, msg.payload.TotalLines, len(msg.payload.Regions))
		}
		m.content = msg.payload.Content
		m.regions = msg.payload.Regions
		m.totalLines = msg.payload.TotalLines
		m.sequenceNum = msg.payload.SequenceNum
		m.isTouchMode = msg.payload.IsTouchMode
		m.sidebarBg = msg.payload.SidebarBg
		m.terminalBg = msg.payload.TerminalBg

		// SIMPLIFIED: Clamp scroll based on simple height calculation
		maxScroll := m.totalLines - m.height
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollY > maxScroll {
			m.scrollY = maxScroll
		}

		// Debug logging
		if *debugMode {
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
		if m.pickerShowing {
			return m.handlePickerKey(msg)
		}
		// Menu mode: intercept all keys
		if m.menuShowing {
			return m.handleMenuKey(msg)
		}
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
		case "m":
			// Open marker picker
			m.sendInput(&daemon.InputPayload{
				Type: "key",
				Key:  "m",
			})
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case longPressMsg:
		// Long-press timer fired - check if still valid (allow some tolerance for touch)
		dx := abs(msg.X - m.mouseDownPos.X)
		dy := abs(msg.Y - m.mouseDownPos.Y)
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("LONGPRESS_TIMER active=%v dx=%d dy=%d threshold=%d downPos=(%d,%d)",
				m.longPressActive, dx, dy, movementThreshold, m.mouseDownPos.X, m.mouseDownPos.Y)
		}
		if m.longPressActive && dx <= movementThreshold && dy <= movementThreshold {
			if *debugMode {
				debugLog.Printf("Long-press detected at X=%d Y=%d (drift: dx=%d dy=%d)", msg.X, msg.Y, dx, dy)
			}
			if inputLog != nil && isInputLogEnabled() {
				inputLog.Printf("LONGPRESS_TRIGGERED -> right-click at (%d,%d)", m.mouseDownPos.X, m.mouseDownPos.Y)
			}
			m.longPressActive = false // Prevent release from triggering click
			m.skipNextRelease = true  // Skip the release event
			// Treat as right-click (simulated)
			return m.processMouseClick(m.mouseDownPos.X, m.mouseDownPos.Y, tea.MouseButtonRight, true)
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

	if m.pickerShowing {
		return m.handlePickerMouse(msg)
	}

	// Menu mode: intercept all mouse events
	if m.menuShowing {
		return m.handleMenuMouse(msg)
	}

	// Debug logging for mouse events
	if *debugMode {
		debugLog.Printf("=== MOUSE EVENT ===")
		debugLog.Printf("  Position: X=%d Y=%d", msg.X, msg.Y)
		debugLog.Printf("  Button: %v Action: %v Ctrl: %v Shift: %v Alt: %v", msg.Button, msg.Action, msg.Ctrl, msg.Shift, msg.Alt)
		debugLog.Printf("  Viewport: height=%d scrollY=%d totalLines=%d", m.height, m.scrollY, m.totalLines)
		debugLog.Printf("  LongPressActive: %v", m.longPressActive)
	}

	// Input logging for all mouse events (to debug mobile)
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf("MOUSE btn=%v action=%v x=%d y=%d shift=%v ctrl=%v longPress=%v",
			msg.Button, msg.Action, msg.X, msg.Y, msg.Shift, msg.Ctrl, m.longPressActive)
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
			if *debugMode {
				debugLog.Printf("  Shift/Ctrl+click detected (Shift=%v Ctrl=%v), treating as right-click", msg.Shift, msg.Ctrl)
			}
			m.skipNextRelease = true      // Skip release to prevent false drag detection
			m.mouseDownTime = time.Time{} // Clear stale timestamp
			m.mouseDownValid = false
			return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight, true)
		}

		if msg.Button == tea.MouseButtonLeft {
			m.mouseDownValid = true
			if !m.isTouchMode {
				m.mouseDownTime = time.Now()
				m.mouseDownPos = struct{ X, Y int }{msg.X, msg.Y}
				m.longPressActive = false
				return m, nil
			}

			// Check for double-click (second press within threshold)
			timeSinceLastClick := time.Since(m.lastTapTime)
			clickDx := abs(msg.X - m.lastTapPos.X)
			clickDy := abs(msg.Y - m.lastTapPos.Y)

			if timeSinceLastClick < doubleTapThreshold && clickDx <= doubleTapDistance && clickDy <= doubleTapDistance {
				// Double-click detected - treat as right-click to open context menu
				if *debugMode {
					debugLog.Printf("  Double-click detected on PRESS (interval=%v, distance=%d,%d) -> right-click", timeSinceLastClick, clickDx, clickDy)
				}
				m.lastTapTime = time.Time{} // Reset to prevent triple-click
				m.skipNextRelease = true    // Don't process the release
				m.mouseDownValid = false
				return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonRight, true)
			}

			// Start long-press detection
			m.mouseDownTime = time.Now()
			m.mouseDownPos = struct{ X, Y int }{msg.X, msg.Y}
			m.longPressActive = true
			if *debugMode {
				debugLog.Printf("  Starting long-press timer at X=%d Y=%d", msg.X, msg.Y)
			}
			// Start a timer for long-press
			return m, tea.Tick(longPressThreshold, func(t time.Time) tea.Msg {
				return longPressMsg{X: msg.X, Y: msg.Y}
			})
		}
		// Right or middle click - process immediately (not simulated)
		// Skip the release to prevent false drag detection from stale mouseDownPos
		m.skipNextRelease = true
		m.mouseDownTime = time.Time{} // Clear to prevent stale elapsed time checks
		m.mouseDownValid = false
		return m.processMouseClick(msg.X, msg.Y, msg.Button, false)

	case tea.MouseActionMotion:
		// Check if movement exceeds threshold - cancel long-press if so
		if m.longPressActive {
			dx := abs(msg.X - m.mouseDownPos.X)
			dy := abs(msg.Y - m.mouseDownPos.Y)
			if dx > movementThreshold || dy > movementThreshold {
				if *debugMode {
					debugLog.Printf("  Long-press cancelled due to movement: dx=%d dy=%d", dx, dy)
				}
				m.longPressActive = false
				m.mouseDownTime = time.Time{}
				m.mouseDownValid = false
			}
		}
		return m, nil

	case tea.MouseActionRelease:
		// Skip this release if menu just closed (prevents false drag detection)
		if m.skipNextRelease {
			m.skipNextRelease = false
			m.longPressActive = false
			m.mouseDownTime = time.Time{}
			m.mouseDownValid = false
			return m, nil
		}

		if !m.mouseDownValid {
			if m.isTouchMode && msg.Button == tea.MouseButtonLeft {
				if *debugMode {
					debugLog.Printf("  Release-only left click fallback at x=%d y=%d", msg.X, msg.Y)
				}
				if inputLog != nil && isInputLogEnabled() {
					inputLog.Printf("RELEASE_ONLY_FALLBACK x=%d y=%d", msg.X, msg.Y)
				}
				return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonLeft, false)
			}
			m.longPressActive = false
			m.mouseDownTime = time.Time{}
			return m, nil
		}
		m.mouseDownValid = false

		if !m.isTouchMode {
			if m.mouseDownTime.IsZero() {
				return m, nil
			}
			elapsed := time.Since(m.mouseDownTime)
			m.mouseDownTime = time.Time{}

			dx := abs(msg.X - m.mouseDownPos.X)
			dy := abs(msg.Y - m.mouseDownPos.Y)
			isDrag := elapsed > 0 && (dx > 5 || dy > 2)
			if isDrag || elapsed <= 0 {
				return m, nil
			}

			return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonLeft, false)
		}

		wasLongPressActive := m.longPressActive
		m.longPressActive = false
		if m.mouseDownTime.IsZero() {
			return m, nil
		}
		elapsed := time.Since(m.mouseDownTime)
		m.mouseDownTime = time.Time{}

		if *debugMode {
			debugLog.Printf("  RELEASE at (%d,%d), press was (%d,%d), longPress=%v elapsed=%v",
				msg.X, msg.Y, m.mouseDownPos.X, m.mouseDownPos.Y, wasLongPressActive, elapsed)
		}

		// Detect drag by comparing press vs release position directly.
		// This works even if motion events aren't received (e.g. mosh).
		// Use larger threshold (5 chars horizontal, 2 lines vertical) to avoid
		// false drag detection from slight mouse movement during clicks
		// Also skip if elapsed is 0 (no valid press - stale state)
		dx := abs(msg.X - m.mouseDownPos.X)
		dy := abs(msg.Y - m.mouseDownPos.Y)
		isDrag := elapsed > 0 && (dx > 5 || dy > 2)

		if isDrag {
			if *debugMode {
				debugLog.Printf("  Drag detected: from (%d,%d) to (%d,%d), dx=%d dy=%d",
					m.mouseDownPos.X, m.mouseDownPos.Y, msg.X, msg.Y, dx, dy)
			}
			return m, nil
		}

		// Record click time/position for double-click detection (checked on next press)
		m.lastTapTime = time.Now()
		m.lastTapPos = struct{ X, Y int }{msg.X, msg.Y}

		// Only process left-click action if this wasn't a long-press that already triggered
		if wasLongPressActive && elapsed < longPressThreshold {
			if *debugMode {
				debugLog.Printf("  Quick click (elapsed=%v) -> left-click action", elapsed)
			}
			return m.processMouseClick(msg.X, msg.Y, tea.MouseButtonLeft, false)
		}
		// Long-press would have already triggered via timer
		return m, nil
	}

	return m, nil
}

// processMouseClick handles the actual click processing after determining button type
func (m rendererModel) processMouseClick(x, y int, button tea.MouseButton, isSimulated bool) (tea.Model, tea.Cmd) {
	var resolvedAction, resolvedTarget string

	// Translate Y to content line (accounting for scroll)
	contentY := y + m.scrollY

	if *debugMode {
		debugLog.Printf("  Processing click: button=%v Y=%d ContentY=%d (scroll=%d) simulated=%v", button, y, contentY, m.scrollY, isSimulated)
	}

	// Check all regions with simple Y-based matching
	if *debugMode {
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
				if *debugMode {
					debugLog.Printf("  -> Matched region[%d]: lines %d-%d, cols %d-%d, action=%s target=%s",
						i, region.StartLine, region.EndLine, region.StartCol, endCol, region.Action, region.Target)
				}
				break
			}
		}
	}

	if button == tea.MouseButtonRight && resolvedAction == "" {
		for _, region := range m.regions {
			if contentY < region.StartLine || contentY > region.EndLine {
				continue
			}
			switch region.Action {
			case "select_window", "select_pane", "toggle_group", "toggle_panes", "group_header", "sidebar_header_area", "sidebar_settings":
				resolvedAction = region.Action
				resolvedTarget = region.Target
				if region.Action == "select_window" || region.Action == "select_pane" {
					break
				}
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

	// Get our actual pane ID (not client/window ID) for resize and context menus
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		out, _ := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		paneID = strings.TrimSpace(string(out))
	}

	// Mobile menu zone: rightmost 3 columns auto-trigger right-click for context menu
	// This provides a reliable way to open menus on mobile without long-press/double-tap
	menuZoneStart := m.width - 3
	if menuZoneStart < 0 {
		menuZoneStart = 0
	}
	if button == tea.MouseButtonLeft && x >= menuZoneStart && resolvedAction != "" {
		// Only convert to right-click for window/pane/group actions (not buttons)
		if resolvedAction == "select_window" || resolvedAction == "select_pane" || resolvedAction == "toggle_panes" || resolvedAction == "toggle_group" {
			if *debugMode {
				debugLog.Printf("  Menu zone click (x=%d >= %d) -> converting to right-click", x, menuZoneStart)
			}
			if inputLog != nil && isInputLogEnabled() {
				inputLog.Printf("MENU_ZONE x=%d width=%d -> right-click", x, m.width)
			}
			button = tea.MouseButtonRight
		}
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
		ViewportOffset:        m.scrollY,
		ResolvedAction:        resolvedAction,
		ResolvedTarget:        resolvedTarget,
		PaneID:                paneID,
		IsSimulatedRightClick: isSimulated,
		IsTouchMode:           m.isTouchMode,
	}

	m.sendInput(input)
	return m, nil
}

// --- Context Menu Handling ---

// handleMenuKey processes keyboard events while menu is showing
func (m rendererModel) handleMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Check menu item shortcut keys FIRST (before navigation)
	// This ensures shortcuts like "e", "c", "r" always trigger their item
	// even if they overlap with navigation keys
	for i, item := range m.menuItems {
		if !item.Separator && !item.Header && item.Key == key {
			m.menuSelect(i)
			m.menuShowing = false
			m.menuDragActive = false
			m.menuRestoreFocus()
			return m, nil
		}
	}

	// Then handle navigation and control keys
	switch key {
	case "escape", "q":
		m.menuDismiss()
		m.menuShowing = false
		m.menuDragActive = false
		m.menuRestoreFocus()
		return m, nil
	case "up", "k":
		m.menuMoveHighlight(-1)
		return m, nil
	case "down", "j":
		m.menuMoveHighlight(1)
		return m, nil
	case "enter":
		if m.menuHighlight >= 0 && m.menuHighlight < len(m.menuItems) {
			item := m.menuItems[m.menuHighlight]
			if !item.Separator && !item.Header {
				m.menuSelect(m.menuHighlight)
			} else {
				m.menuDismiss()
			}
		} else {
			m.menuDismiss()
		}
		m.menuShowing = false
		m.menuDragActive = false
		m.menuRestoreFocus()
		return m, nil
	}
	return m, nil
}

// handleMenuMouse processes mouse events while menu is showing
func (m rendererModel) handleMenuMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Scroll wheel should close menu
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("MENU_MOUSE scroll dismiss")
		}
		m.menuDismiss()
		m.menuShowing = false
		m.menuDragActive = false
		m.menuRestoreFocus()
		return m, nil
	}

	inMenu := m.isInMenuBounds(msg.X, msg.Y)
	itemIdx := m.menuItemAtScreenY(msg.Y)
	if inputLog != nil && isInputLogEnabled() {
		menuH := len(m.menuItems) + 2
		startY := m.menuStartY()
		inputLog.Printf("MENU_MOUSE action=%v btn=%v x=%d y=%d inMenu=%v itemIdx=%d startY=%d menuH=%d width=%d items=%d",
			msg.Action, msg.Button, msg.X, msg.Y, inMenu, itemIdx, startY, menuH, m.width, len(m.menuItems))
	}

	switch msg.Action {
	case tea.MouseActionMotion:
		// Highlight item under cursor
		if inMenu && itemIdx >= 0 {
			m.menuHighlight = itemIdx
		} else {
			m.menuHighlight = -1
		}
		return m, nil

	case tea.MouseActionRelease:
		if m.menuDragActive {
			m.menuDragActive = false
			if inMenu && itemIdx >= 0 {
				item := m.menuItems[itemIdx]
				if !item.Separator && !item.Header {
					m.menuSelect(itemIdx)
					m.menuShowing = false
					m.skipNextRelease = true // Prevent false drag detection
					m.menuRestoreFocus()
					return m, nil
				}
			}
			// Released outside or on non-selectable item - keep menu open
			// so user can use keyboard shortcuts after releasing the mouse
			return m, nil
		}
		return m, nil

	case tea.MouseActionPress:
		if !inMenu {
			// Click outside menu - close
			if inputLog != nil && isInputLogEnabled() {
				inputLog.Printf("MENU_DISMISS reason=click_outside x=%d y=%d", msg.X, msg.Y)
			}
			m.menuDismiss()
			m.menuShowing = false
			m.menuDragActive = false
			m.skipNextRelease = true // Prevent false drag detection
			m.menuRestoreFocus()
			return m, nil
		}
		if msg.Button == tea.MouseButtonLeft && !m.menuDragActive {
			// Direct left-click on menu item (not drag mode)
			if itemIdx >= 0 {
				item := m.menuItems[itemIdx]
				if !item.Separator && !item.Header {
					if inputLog != nil && isInputLogEnabled() {
						inputLog.Printf("MENU_SELECT idx=%d label=%s", itemIdx, item.Label)
					}
					m.menuSelect(itemIdx)
					m.menuShowing = false
					m.skipNextRelease = true // Prevent false drag detection
					m.menuRestoreFocus()
					return m, nil
				}
			}
		}
		return m, nil
	}

	return m, nil
}

// menuMoveHighlight moves the highlight to the next/previous selectable item
func (m *rendererModel) menuMoveHighlight(direction int) {
	if len(m.menuItems) == 0 {
		return
	}
	start := m.menuHighlight
	if start < 0 {
		if direction > 0 {
			start = -1
		} else {
			start = len(m.menuItems)
		}
	}
	for i := start + direction; i >= 0 && i < len(m.menuItems); i += direction {
		item := m.menuItems[i]
		if !item.Separator && !item.Header {
			m.menuHighlight = i
			return
		}
	}
}

// menuSelect sends the selected menu item index to the daemon
func (m *rendererModel) menuSelect(index int) {
	m.sendInput(&daemon.InputPayload{
		Type:   "menu_select",
		MouseX: index,
	})
}

// menuDismiss sends a cancel signal to clean up pending menu state in the daemon
func (m *rendererModel) menuDismiss() {
	m.sendInput(&daemon.InputPayload{
		Type:   "menu_select",
		MouseX: -1,
	})
}

// menuFocusSidebar focuses the sidebar pane so keyboard events reach the renderer
func (m *rendererModel) menuFocusSidebar() {
	if m.sidebarPaneID != "" {
		exec.Command("tmux", "select-pane", "-t", m.sidebarPaneID).Run()
	}
}

// menuRestoreFocus returns focus to the previously active pane
func (m *rendererModel) menuRestoreFocus() {
	// select-pane -l switches back to the last active pane
	exec.Command("tmux", "select-pane", "-l").Run()
}

// menuStartY returns the computed screen Y where the menu starts rendering
func (m rendererModel) menuStartY() int {
	menuH := len(m.menuItems) + 2 // top border + items + bottom border
	startY := m.menuY
	// Clamp to fit within screen
	if startY+menuH > m.height {
		startY = m.height - menuH
	}
	if startY < 0 {
		startY = 0
	}
	return startY
}

// isInMenuBounds checks if screen coordinates are within the menu box
func (m rendererModel) isInMenuBounds(x, y int) bool {
	menuH := len(m.menuItems) + 2
	startY := m.menuStartY()
	return y >= startY && y < startY+menuH && x >= 0 && x < m.width
}

// menuItemAtScreenY returns the menu item index at a screen Y coordinate
// Returns -1 if not on an item row or if the item is a separator/header
func (m rendererModel) menuItemAtScreenY(screenY int) int {
	startY := m.menuStartY()
	relY := screenY - startY
	// Row 0 = top border, rows 1..N = items, row N+1 = bottom border
	itemIdx := relY - 1
	if itemIdx < 0 || itemIdx >= len(m.menuItems) {
		return -1
	}
	if m.menuItems[itemIdx].Separator || m.menuItems[itemIdx].Header {
		return -1
	}
	return itemIdx
}

// renderMenuLines generates styled lines for the menu overlay
func (m rendererModel) renderMenuLines() []string {
	w := m.width
	if w < 6 || len(m.menuItems) == 0 {
		return nil
	}

	borderColor := lipgloss.Color("#666")
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000"))
	highlightStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2563eb")).
		Bold(true)
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Bold(true)

	var lines []string

	// Top border with title: ┌─ Title ───┐
	title := m.menuTitle
	maxTitleW := w - 5 // "┌─ " + " ┐" overhead
	if maxTitleW < 0 {
		maxTitleW = 0
	}
	title = truncateToWidth(title, maxTitleW)
	titleW := runewidth.StringWidth(title)
	padCount := w - 3 - titleW // "┌─" + title + pad*"─" + "┐" = w
	if padCount < 0 {
		padCount = 0
	}
	topBorder := borderStyle.Render("┌─" + title + strings.Repeat("─", padCount) + "┐")
	lines = append(lines, topBorder)

	// Inner content width: "│ " + content + " │" = w  =>  content = w - 4
	innerW := w - 4
	if innerW < 1 {
		innerW = 1
	}

	for i, item := range m.menuItems {
		if item.Separator {
			sep := borderStyle.Render("├" + strings.Repeat("─", w-2) + "┤")
			lines = append(lines, sep)
			continue
		}

		label := item.Label
		key := item.Key
		if item.Header {
			trimmed := strings.TrimLeft(label, " ")
			indent := len(label) - len(trimmed)
			label = strings.Repeat(" ", indent) + "> " + strings.TrimSpace(trimmed)
		}

		// Build inner text content (plain, no ANSI)
		var inner string
		if key != "" {
			keyW := runewidth.StringWidth(key)
			labelMax := innerW - keyW - 1
			if labelMax < 0 {
				labelMax = 0
			}
			label = truncateToWidth(label, labelMax)
			labelW := runewidth.StringWidth(label)
			gap := labelMax - labelW
			if gap < 0 {
				gap = 0
			}
			inner = label + strings.Repeat(" ", gap) + " " + key
		} else {
			label = truncateToWidth(label, innerW)
			labelW := runewidth.StringWidth(label)
			inner = label + strings.Repeat(" ", innerW-labelW)
		}

		// Apply style
		border := borderStyle.Render("│")
		var styledInner string
		if item.Header {
			styledInner = headerStyle.Render(" " + inner + " ")
		} else if i == m.menuHighlight {
			styledInner = highlightStyle.Render(" " + inner + " ")
		} else {
			styledInner = normalStyle.Render(" " + inner + " ")
		}
		lines = append(lines, border+styledInner+border)
	}

	// Bottom border
	bottomBorder := borderStyle.Render("└" + strings.Repeat("─", w-2) + "┘")
	lines = append(lines, bottomBorder)

	return lines
}

type pickerMatch struct {
	optionIdx int
	score     int
}

func normalizePickerText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return s
}

func fuzzyScore(query, candidate string) int {
	q := normalizePickerText(query)
	c := normalizePickerText(candidate)
	if q == "" {
		return 1
	}
	if strings.Contains(c, q) {
		return 1000 - len(c)
	}
	qr := []rune(q)
	cr := []rune(c)
	qi := 0
	score := 0
	streak := 0
	for i, r := range cr {
		if qi >= len(qr) {
			break
		}
		if r == qr[qi] {
			qi++
			streak++
			score += 10 + (streak * 4)
			if i == 0 || cr[i-1] == ' ' {
				score += 8
			}
		} else {
			streak = 0
		}
	}
	if qi != len(qr) {
		return -1
	}
	return score - len(cr)
}

func (m *rendererModel) pickerApplyFilter() {
	matches := make([]pickerMatch, 0, len(m.pickerOptions))
	for i, opt := range m.pickerOptions {
		candidate := strings.TrimSpace(opt.Symbol + " " + opt.Name + " " + opt.Keywords)
		score := fuzzyScore(m.pickerQuery, candidate)
		if score >= 0 {
			matches = append(matches, pickerMatch{optionIdx: i, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score == matches[j].score {
			li := strings.ToLower(m.pickerOptions[matches[i].optionIdx].Name)
			lj := strings.ToLower(m.pickerOptions[matches[j].optionIdx].Name)
			return li < lj
		}
		return matches[i].score > matches[j].score
	})
	m.pickerFiltered = make([]int, 0, len(matches))
	for _, match := range matches {
		m.pickerFiltered = append(m.pickerFiltered, match.optionIdx)
	}
	if len(m.pickerFiltered) == 0 {
		m.pickerCursor = -1
		m.pickerScroll = 0
		return
	}
	if m.pickerCursor < 0 {
		m.pickerCursor = 0
	}
	if m.pickerCursor >= len(m.pickerFiltered) {
		m.pickerCursor = len(m.pickerFiltered) - 1
	}
}

func (m *rendererModel) pickerDismiss(sendCancel bool) {
	if sendCancel {
		m.sendInput(&daemon.InputPayload{Type: "marker_picker", PickerAction: "cancel"})
	}
	m.pickerShowing = false
	m.pickerMouseDown = false
	m.pickerQuery = ""
	m.pickerFiltered = nil
	m.menuRestoreFocus()
}

func (m *rendererModel) pickerSelectCurrent() {
	if m.pickerCursor < 0 || m.pickerCursor >= len(m.pickerFiltered) {
		return
	}
	opt := m.pickerOptions[m.pickerFiltered[m.pickerCursor]]
	m.sendInput(&daemon.InputPayload{
		Type:         "marker_picker",
		PickerAction: "apply",
		PickerScope:  m.pickerScope,
		PickerTarget: m.pickerTarget,
		PickerValue:  opt.Symbol,
		PickerQuery:  m.pickerQuery,
	})
	m.pickerDismiss(false)
}

func (m *rendererModel) pickerVisibleRows() int {
	_, _, _, modalH := m.pickerModalLayout()
	h := modalH - 8
	if h < 2 {
		h = 2
	}
	return h
}

func (m *rendererModel) pickerEnsureCursorVisible() {
	rows := m.pickerVisibleRows()
	if m.pickerCursor < m.pickerScroll {
		m.pickerScroll = m.pickerCursor
	}
	if m.pickerCursor >= m.pickerScroll+rows {
		m.pickerScroll = m.pickerCursor - rows + 1
	}
	maxScroll := len(m.pickerFiltered) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.pickerScroll > maxScroll {
		m.pickerScroll = maxScroll
	}
	if m.pickerScroll < 0 {
		m.pickerScroll = 0
	}
}

func (m rendererModel) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "escape", "q":
		m.pickerDismiss(true)
		return m, nil
	case "enter":
		m.pickerSelectCurrent()
		return m, nil
	case "up", "k":
		if len(m.pickerFiltered) > 0 && m.pickerCursor > 0 {
			m.pickerCursor--
			m.pickerEnsureCursorVisible()
		}
		return m, nil
	case "down", "j":
		if len(m.pickerFiltered) > 0 && m.pickerCursor < len(m.pickerFiltered)-1 {
			m.pickerCursor++
			m.pickerEnsureCursorVisible()
		}
		return m, nil
	case "ctrl+u":
		m.pickerQuery = ""
		m.pickerApplyFilter()
		return m, nil
	case "backspace", "ctrl+h":
		if m.pickerQuery != "" {
			r := []rune(m.pickerQuery)
			if len(r) > 0 {
				m.pickerQuery = string(r[:len(r)-1])
				m.pickerApplyFilter()
			}
		}
		return m, nil
	}

	if msg.String() == "space" {
		m.pickerQuery += " "
		m.pickerApplyFilter()
		return m, nil
	}

	if len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			if r >= 32 && r != 127 {
				m.pickerQuery += string(r)
			}
		}
		m.pickerApplyFilter()
	}

	return m, nil
}

func (m rendererModel) pickerModalLayout() (startX, startY, modalW, modalH int) {
	modalW = m.width - 6
	if modalW > 72 {
		modalW = 72
	}
	if modalW < 26 {
		modalW = m.width
	}
	modalH = m.height - 4
	if modalH > 20 {
		modalH = 20
	}
	if modalH < 10 {
		modalH = m.height
	}
	startX = (m.width - modalW) / 2
	startY = (m.height - modalH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}
	return
}

func (m rendererModel) pickerIndexAt(screenX, screenY int) int {
	startX, startY, modalW, modalH := m.pickerModalLayout()
	if screenX < startX || screenX >= startX+modalW || screenY < startY || screenY >= startY+modalH {
		return -2
	}
	listTop := startY + 5
	rows := m.pickerVisibleRows()
	if screenY < listTop || screenY >= listTop+rows {
		return -1
	}
	rel := screenY - listTop
	idx := m.pickerScroll + rel
	if idx < 0 || idx >= len(m.pickerFiltered) {
		return -1
	}
	return idx
}

func (m rendererModel) handlePickerMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp {
		if m.pickerScroll > 0 {
			m.pickerScroll--
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		rows := m.pickerVisibleRows()
		max := len(m.pickerFiltered) - rows
		if max < 0 {
			max = 0
		}
		if m.pickerScroll < max {
			m.pickerScroll++
		}
		return m, nil
	}

	idx := m.pickerIndexAt(msg.X, msg.Y)
	switch msg.Action {
	case tea.MouseActionMotion:
		if idx >= 0 {
			m.pickerCursor = idx
		}
	case tea.MouseActionPress:
		if idx == -2 {
			m.pickerDismiss(true)
			return m, nil
		}
		if idx >= 0 {
			m.pickerCursor = idx
			m.pickerMouseDown = true
		}
	case tea.MouseActionRelease:
		if m.pickerMouseDown && idx >= 0 && idx == m.pickerCursor {
			m.pickerSelectCurrent()
			m.pickerMouseDown = false
			return m, nil
		}
		m.pickerMouseDown = false
	}
	return m, nil
}

func (m rendererModel) renderPickerModal() []string {
	_, _, modalW, _ := m.pickerModalLayout()
	innerW := modalW - 4
	if innerW < 8 {
		innerW = 8
	}

	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000"))
	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000"))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Italic(true)
	highlightStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2563eb")).Bold(true)

	lines := make([]string, 0, 24)
	title := truncateToWidth(m.pickerTitle, innerW)
	titlePad := innerW - runewidth.StringWidth(title)
	if titlePad < 0 {
		titlePad = 0
	}
	lines = append(lines, borderStyle.Render("┌"+strings.Repeat("─", modalW-2)+"┐"))
	lines = append(lines, borderStyle.Render("│ ")+titleStyle.Render(title)+strings.Repeat(" ", titlePad)+borderStyle.Render(" │"))

	qPrefix := "Search: "
	queryMax := innerW - runewidth.StringWidth(qPrefix)
	queryText := truncateToWidth(m.pickerQuery, queryMax)
	queryLine := qPrefix + queryText
	queryPad := innerW - runewidth.StringWidth(queryLine)
	if queryPad < 0 {
		queryPad = 0
	}
	lines = append(lines, borderStyle.Render("│ ")+lineStyle.Render(queryLine)+strings.Repeat(" ", queryPad)+borderStyle.Render(" │"))

	meta := fmt.Sprintf("Results: %d", len(m.pickerFiltered))
	meta = truncateToWidth(meta, innerW)
	metaPad := innerW - runewidth.StringWidth(meta)
	if metaPad < 0 {
		metaPad = 0
	}
	lines = append(lines, borderStyle.Render("│ ")+lineStyle.Render(meta)+strings.Repeat(" ", metaPad)+borderStyle.Render(" │"))
	lines = append(lines, borderStyle.Render("├"+strings.Repeat("─", modalW-2)+"┤"))

	rows := m.pickerVisibleRows()
	m.pickerEnsureCursorVisible()
	for i := 0; i < rows; i++ {
		idx := m.pickerScroll + i
		content := ""
		styleForRow := lineStyle
		if idx >= 0 && idx < len(m.pickerFiltered) {
			opt := m.pickerOptions[m.pickerFiltered[idx]]
			if opt.Symbol == "" {
				content = opt.Name
			} else {
				content = fmt.Sprintf("%s  %s", opt.Symbol, opt.Name)
			}
		} else if len(m.pickerFiltered) == 0 && i == 0 {
			content = "No matching markers"
			styleForRow = emptyStyle
		}
		content = truncateToWidth(content, innerW)
		pad := innerW - runewidth.StringWidth(content)
		if pad < 0 {
			pad = 0
		}
		text := content + strings.Repeat(" ", pad)
		if idx == m.pickerCursor {
			lines = append(lines, borderStyle.Render("│ ")+highlightStyle.Render(text)+borderStyle.Render(" │"))
		} else {
			lines = append(lines, borderStyle.Render("│ ")+styleForRow.Render(text)+borderStyle.Render(" │"))
		}
	}

	lines = append(lines, borderStyle.Render("├"+strings.Repeat("─", modalW-2)+"┤"))
	help := "Enter: apply  Esc: cancel  Up/Down: select  Fuzzy search"
	help = truncateToWidth(help, innerW)
	helpPad := innerW - runewidth.StringWidth(help)
	if helpPad < 0 {
		helpPad = 0
	}
	lines = append(lines, borderStyle.Render("│ ")+lineStyle.Render(help)+strings.Repeat(" ", helpPad)+borderStyle.Render(" │"))
	lines = append(lines, borderStyle.Render("└"+strings.Repeat("─", modalW-2)+"┘"))

	return lines
}

// truncateToWidth truncates a string to fit within maxW display columns
func truncateToWidth(s string, maxW int) string {
	if runewidth.StringWidth(s) <= maxW {
		return s
	}
	w := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW {
			return s[:i]
		}
		w += rw
	}
	return s
}

// spinnerFrames for loading animation
var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// View implements tea.Model
func (m rendererModel) View() string {
	if !m.connected || m.content == "" {
		frame := spinnerFrames[int(time.Now().UnixMilli()/100)%len(spinnerFrames)]
		loadingText := fmt.Sprintf(" %s Loading...", frame)
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Width(m.width)

		if m.sidebarBg != "" {
			style = style.Background(lipgloss.Color(m.sidebarBg))
		}

		// Don't set background - let terminal's natural background show through
		var lines []string
		lines = append(lines, style.Render(loadingText))
		for i := 1; i < m.height; i++ {
			if m.sidebarBg != "" {
				lines = append(lines, style.Render(strings.Repeat(" ", m.width)))
			} else {
				lines = append(lines, "")
			}
		}
		return strings.Join(lines, "\n")
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

	bgStyle := lipgloss.NewStyle()
	if m.sidebarBg != "" {
		bgStyle = bgStyle.Background(lipgloss.Color(m.sidebarBg))
	}

	// Build visible content, padding each line to full width
	// This ensures old content is always overwritten
	var visible []string
	for i := visibleStart; i < visibleEnd && i < len(lines); i++ {
		line := lines[i]
		// Pad line to full width if shorter
		lineWidth := runewidth.StringWidth(stripAnsi(line))
		if lineWidth < m.width {
			line += strings.Repeat(" ", m.width-lineWidth)
		}
		if m.sidebarBg != "" {
			visible = append(visible, bgStyle.Render(line))
		} else {
			visible = append(visible, line)
		}
	}

	// Pad remaining lines with full-width blank lines
	blankLine := strings.Repeat(" ", m.width)
	if m.sidebarBg != "" {
		blankLine = bgStyle.Render(blankLine)
	}
	for len(visible) < m.height {
		visible = append(visible, blankLine)
	}

	// Overlay context menu if showing
	if m.menuShowing {
		menuLines := m.renderMenuLines()
		startY := m.menuStartY()
		for i, ml := range menuLines {
			row := startY + i
			if row >= 0 && row < len(visible) {
				visible[row] = ml
			}
		}
	}

	if m.pickerShowing {
		startX, startY, modalW, _ := m.pickerModalLayout()
		pickerLines := m.renderPickerModal()
		for i, pl := range pickerLines {
			row := startY + i
			if row < 0 || row >= len(visible) {
				continue
			}
			left := strings.Repeat(" ", startX)
			rightWidth := m.width - startX - modalW
			if rightWidth < 0 {
				rightWidth = 0
			}
			right := strings.Repeat(" ", rightWidth)
			visible[row] = left + pl + right
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
					// Send to the tea program via a channel or direct update
					// For simplicity, we'll use the global program reference
					if globalProgram != nil {
						if inputLog != nil && isInputLogEnabled() {
							inputLog.Printf("RENDER_RECV seq=%d regions=%d content_len=%d", payload.SequenceNum, len(payload.Regions), len(payload.Content))
						}
						globalProgram.Send(renderMsg{payload: &payload})
					} else {
						if inputLog != nil && isInputLogEnabled() {
							inputLog.Printf("RENDER_RECV_DROP seq=%d reason=globalProgram_nil", payload.SequenceNum)
						}
					}
				}
			}
		case daemon.MsgMenu:
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var payload daemon.MenuPayload
				if json.Unmarshal(payloadBytes, &payload) == nil {
					if globalProgram != nil {
						globalProgram.Send(menuMsg{payload: &payload})
					}
				}
			}
		case daemon.MsgMarkerPicker:
			if msg.Payload != nil {
				payloadBytes, _ := json.Marshal(msg.Payload)
				var payload daemon.MarkerPickerPayload
				if json.Unmarshal(payloadBytes, &payload) == nil {
					if globalProgram != nil {
						globalProgram.Send(markerPickerMsg{payload: &payload})
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
	// Detect color profile - prefer TrueColor
	colorProfile := "TrueColor"

	m.sendMessage(daemon.Message{
		Type:     daemon.MsgSubscribe,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: colorProfile,
			PaneID:       m.sidebarPaneID,
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
			PaneID: m.sidebarPaneID,
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
	if inputLog != nil && isInputLogEnabled() {
		inputLog.Printf("SEND button=%s x=%d y=%d action=%s target=%s connected=%v",
			input.Button, input.MouseX, input.MouseY,
			input.ResolvedAction, input.ResolvedTarget, m.connected)
	}
	if !m.connected {
		if inputLog != nil && isInputLogEnabled() {
			inputLog.Printf("SEND_FAILED not connected")
		}
		return
	}
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
	initInputLog()
	defer recoverAndLog("main")

	// Note: BubbleZone initialization removed - zone detection happens in daemon only.
	// The daemon extracts zone bounds and sends accurate ClickableRegions.

	if *debugMode {
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
	crashLog.Printf("Renderer started for window %s, session %s", *windowID, *sessionID)

	// Force TrueColor mode for accurate theme rendering
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Get our own pane ID for focus management (context menu keyboard input)
	sidebarPane := os.Getenv("TMUX_PANE")
	if sidebarPane == "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
			sidebarPane = strings.TrimSpace(string(out))
		}
	}

	model := rendererModel{
		width:         80,
		height:        24,
		sidebarPaneID: sidebarPane,
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithReportFocus())
	globalProgram = p

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		defer recoverAndLog("signal-handler")
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

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}
