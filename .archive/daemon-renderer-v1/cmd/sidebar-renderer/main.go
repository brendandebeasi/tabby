package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// detectedColorProfile holds the terminal's color capability
var detectedColorProfile string

func init() {
	// Detect terminal color capabilities
	profile := termenv.ColorProfile()
	switch profile {
	case termenv.Ascii:
		detectedColorProfile = "Ascii"
	case termenv.ANSI:
		detectedColorProfile = "ANSI"
	case termenv.ANSI256:
		detectedColorProfile = "ANSI256"
	case termenv.TrueColor:
		detectedColorProfile = "TrueColor"
	default:
		detectedColorProfile = "ANSI256"
	}
}

const (
	maxReconnectAttempts = 3
	reconnectBackoff     = 1 * time.Second
)

var (
	sessionID = flag.String("session", "", "tmux session ID")
	paneID    = flag.String("pane", "", "tmux pane ID")
	debug     = flag.Bool("debug", false, "enable debug logging")
)

func main() {
	flag.Parse()

	// Get session ID from tmux if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	// Get pane ID from tmux if not provided
	if *paneID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
		if err == nil {
			*paneID = strings.TrimSpace(string(out))
		}
	}

	p := tea.NewProgram(
		newModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// Model for the lightweight renderer
type model struct {
	// Connection state
	conn            net.Conn
	connMu          sync.Mutex
	connected       bool
	reconnectCount  int
	lastError       string

	// Render state from coordinator
	content        string
	pinnedContent  string
	totalLines     int
	pinnedHeight   int
	sequenceNum    uint64
	regions        []daemon.ClickableRegion // Clickable regions for local hit testing

	// Local state
	width          int
	height         int
	viewportOffset int

	// Styles
	errorStyle     lipgloss.Style
	statusStyle    lipgloss.Style
}

func newModel() model {
	return model{
		errorStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
		statusStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true),
	}
}

// Messages
type connectResultMsg struct {
	conn net.Conn
	err  error
}

type renderMsg daemon.RenderPayload

type disconnectedMsg struct {
	err error
}

type reconnectTickMsg struct{}

type keepListeningMsg struct{} // signals Update to continue listening

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.connectCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Clamp viewport offset
		m.clampViewport()
		// Send resize to coordinator
		if m.connected {
			m.sendResize()
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.disconnect()
			return m, tea.Quit
		case "up", "k":
			if m.viewportOffset > 0 {
				m.viewportOffset--
				m.sendViewportUpdate()
			}
			return m, nil
		case "down", "j":
			m.viewportOffset++
			m.clampViewport()
			m.sendViewportUpdate()
			return m, nil
		case "pgup":
			m.viewportOffset -= m.scrollableHeight()
			if m.viewportOffset < 0 {
				m.viewportOffset = 0
			}
			m.sendViewportUpdate()
			return m, nil
		case "pgdown":
			m.viewportOffset += m.scrollableHeight()
			m.clampViewport()
			m.sendViewportUpdate()
			return m, nil
		case "home", "g":
			m.viewportOffset = 0
			m.sendViewportUpdate()
			return m, nil
		case "end", "G":
			m.viewportOffset = m.maxViewportOffset()
			m.sendViewportUpdate()
			return m, nil
		default:
			// Forward other keys to coordinator
			if m.connected {
				m.sendKeyInput(msg.String())
			}
			return m, nil
		}

	case tea.MouseMsg:
		// Handle clicks locally using regions, then send semantic action to daemon
		if m.connected {
			m.sendMouseInput(msg)
		}
		return m, nil

	case connectResultMsg:
		if msg.err != nil {
			m.lastError = msg.err.Error()
			m.reconnectCount++
			if m.reconnectCount < maxReconnectAttempts {
				return m, tea.Tick(reconnectBackoff, func(t time.Time) tea.Msg {
					return reconnectTickMsg{}
				})
			}
			// Max retries exceeded
			return m, nil
		}
		m.conn = msg.conn
		m.connected = true
		m.reconnectCount = 0
		m.lastError = ""
		// Send initial subscribe with size
		m.sendSubscribe()
		return m, m.listenCmd()

	case renderMsg:
		m.content = msg.Content
		m.pinnedContent = msg.PinnedContent
		m.totalLines = msg.TotalLines
		m.pinnedHeight = msg.PinnedHeight
		m.sequenceNum = msg.SequenceNum
		m.regions = msg.Regions // Store clickable regions for local hit testing
		// Apply suggested viewport if we haven't scrolled
		if m.viewportOffset == 0 && msg.ViewportOffset > 0 {
			m.viewportOffset = msg.ViewportOffset
		}
		m.clampViewport()
		return m, m.listenCmd()

	case disconnectedMsg:
		m.connected = false
		m.lastError = "Disconnected from coordinator"
		if msg.err != nil {
			m.lastError = msg.err.Error()
		}
		m.reconnectCount = 0
		return m, tea.Tick(reconnectBackoff, func(t time.Time) tea.Msg {
			return reconnectTickMsg{}
		})

	case reconnectTickMsg:
		return m, m.connectCmd()

	case keepListeningMsg:
		// Continue listening after pong or unknown message
		if m.connected {
			return m, m.listenCmd()
		}
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	if !m.connected {
		return m.renderDisconnected()
	}

	if m.content == "" && m.pinnedContent == "" {
		return m.statusStyle.Render("Waiting for content...")
	}

	// Calculate scrollable area
	scrollableHeight := m.scrollableHeight()
	if scrollableHeight <= 0 {
		return m.pinnedContent
	}

	// Apply viewport to content
	lines := strings.Split(m.content, "\n")
	start := m.viewportOffset
	end := start + scrollableHeight
	if start >= len(lines) {
		start = len(lines) - 1
		if start < 0 {
			start = 0
		}
	}
	if end > len(lines) {
		end = len(lines)
	}

	visibleLines := lines[start:end]

	// Pad if needed
	for len(visibleLines) < scrollableHeight {
		visibleLines = append(visibleLines, "")
	}

	return strings.Join(visibleLines, "\n") + "\n" + m.pinnedContent
}

func (m model) renderDisconnected() string {
	var sb strings.Builder

	if m.reconnectCount < maxReconnectAttempts {
		sb.WriteString(m.statusStyle.Render(fmt.Sprintf("Connecting... (attempt %d/%d)", m.reconnectCount+1, maxReconnectAttempts)))
	} else {
		sb.WriteString(m.errorStyle.Render("Coordinator unavailable"))
	}

	if m.lastError != "" {
		sb.WriteString("\n")
		sb.WriteString(m.statusStyle.Render(m.lastError))
	}

	// Center in terminal
	content := sb.String()
	lines := strings.Split(content, "\n")
	padTop := (m.height - len(lines)) / 2
	if padTop < 0 {
		padTop = 0
	}

	return strings.Repeat("\n", padTop) + content
}

func (m model) scrollableHeight() int {
	return m.height - m.pinnedHeight
}

func (m model) maxViewportOffset() int {
	max := m.totalLines - m.scrollableHeight()
	if max < 0 {
		return 0
	}
	return max
}

func (m *model) clampViewport() {
	max := m.maxViewportOffset()
	if m.viewportOffset > max {
		m.viewportOffset = max
	}
	if m.viewportOffset < 0 {
		m.viewportOffset = 0
	}
}

// Connection commands
func (m model) connectCmd() tea.Cmd {
	return func() tea.Msg {
		socketPath := daemon.SocketPath(*sessionID)
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		return connectResultMsg{conn: conn, err: err}
	}
}

func (m model) listenCmd() tea.Cmd {
	return func() tea.Msg {
		m.connMu.Lock()
		conn := m.conn
		m.connMu.Unlock()

		if conn == nil {
			return disconnectedMsg{err: fmt.Errorf("no connection")}
		}

		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			return disconnectedMsg{err: scanner.Err()}
		}

		var msg daemon.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return disconnectedMsg{err: err}
		}

		switch msg.Type {
		case daemon.MsgRender:
			// Decode render payload
			payloadBytes, _ := json.Marshal(msg.Payload)
			var render daemon.RenderPayload
			json.Unmarshal(payloadBytes, &render)
			return renderMsg(render)
		case daemon.MsgPong:
			// Continue listening after pong
			return keepListeningMsg{}
		}

		// Unknown message type - continue listening
		return keepListeningMsg{}
	}
}

func (m *model) disconnect() {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.conn != nil {
		m.sendMessage(daemon.Message{
			Type:     daemon.MsgUnsubscribe,
			ClientID: *paneID,
		})
		m.conn.Close()
		m.conn = nil
	}
	m.connected = false
}

// Send methods
func (m *model) sendMessage(msg daemon.Message) {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.conn == nil {
		return
	}
	data, _ := json.Marshal(msg)
	m.conn.SetWriteDeadline(time.Now().Add(time.Second))
	m.conn.Write(append(data, '\n'))
}

func (m *model) sendSubscribe() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgSubscribe,
		ClientID: *paneID,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: detectedColorProfile,
		},
	})
}

func (m *model) sendResize() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgResize,
		ClientID: *paneID,
		Payload: daemon.ResizePayload{
			Width:  m.width,
			Height: m.height,
		},
	})
}

func (m *model) sendViewportUpdate() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgViewportUpdate,
		ClientID: *paneID,
		Payload: daemon.ViewportUpdatePayload{
			ViewportOffset: m.viewportOffset,
		},
	})
}

func (m *model) sendKeyInput(key string) {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgInput,
		ClientID: *paneID,
		Payload: daemon.InputPayload{
			SequenceNum: m.sequenceNum,
			Type:        "key",
			Key:         key,
			ViewportOffset: m.viewportOffset,
			PaneID:      *paneID,
		},
	})
}

func (m *model) sendMouseInput(msg tea.MouseMsg) {
	if *debug {
		fmt.Fprintf(os.Stderr, "[renderer] sendMouseInput: button=%v action=%v x=%d y=%d viewportOffset=%d pinnedHeight=%d scrollableHeight=%d numRegions=%d\n",
			msg.Button, msg.Action, msg.X, msg.Y, m.viewportOffset, m.pinnedHeight, m.scrollableHeight(), len(m.regions))
	}

	// Handle scroll wheel locally for responsiveness
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.viewportOffset > 0 {
			m.viewportOffset -= 3
			if m.viewportOffset < 0 {
				m.viewportOffset = 0
			}
			m.sendViewportUpdate()
		}
		return
	case tea.MouseButtonWheelDown:
		m.viewportOffset += 3
		m.clampViewport()
		m.sendViewportUpdate()
		return
	}

	// Only process left clicks on press
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return
	}

	// Check if click is in pinned area (bottom of screen)
	scrollableHeight := m.scrollableHeight()
	if msg.Y >= scrollableHeight {
		if *debug {
			fmt.Fprintf(os.Stderr, "[renderer] click in pinned area (y=%d >= scrollableHeight=%d), ignoring\n", msg.Y, scrollableHeight)
		}
		return // Click in pinned content area, not handled yet
	}

	// Calculate the absolute line that was clicked (screen Y + viewport offset)
	clickY := msg.Y + m.viewportOffset

	if *debug {
		fmt.Fprintf(os.Stderr, "[renderer] click: screenY=%d + viewportOffset=%d = clickY=%d\n", msg.Y, m.viewportOffset, clickY)
		for i, region := range m.regions {
			fmt.Fprintf(os.Stderr, "[renderer]   region[%d]: lines %d-%d action=%q target=%q\n", i, region.StartLine, region.EndLine, region.Action, region.Target)
		}
	}

	// Look up the clicked region
	var resolvedAction, resolvedTarget string
	for _, region := range m.regions {
		if clickY >= region.StartLine && clickY <= region.EndLine {
			resolvedAction = region.Action
			resolvedTarget = region.Target
			break
		}
	}

	if *debug {
		fmt.Fprintf(os.Stderr, "[renderer] resolved click at y=%d (abs=%d) -> action=%q target=%q\n",
			msg.Y, clickY, resolvedAction, resolvedTarget)
	}

	// Send semantic action to daemon
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgInput,
		ClientID: *paneID,
		Payload: daemon.InputPayload{
			SequenceNum:    m.sequenceNum,
			Type:           "action",
			ViewportOffset: m.viewportOffset,
			PaneID:         *paneID,
			ResolvedAction: resolvedAction,
			ResolvedTarget: resolvedTarget,
		},
	})
}
