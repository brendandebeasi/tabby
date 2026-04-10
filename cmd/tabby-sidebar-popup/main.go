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
)

var debugLog *log.Logger

// popupModel is a minimal BubbleTea model for the sidebar popup.
// It connects to the daemon as "popup:{sessionID}", receives render frames,
// and exits on Esc (which closes the tmux display-popup).
type popupModel struct {
	conn      net.Conn
	clientID  string
	width     int
	height    int
	connected bool

	content        string
	pinnedContent  string
	pinnedHeight   int
	regions        []daemon.ClickableRegion
	pinnedRegions  []daemon.ClickableRegion
	viewportOffset int
	sequenceNum    uint64
	sidebarBg      string

	sendMu *sync.Mutex
}

type connectedMsg struct {
	conn     net.Conn
	clientID string
}
type disconnectedMsg struct{}
type renderMsg struct{ payload *daemon.RenderPayload }
type tickMsg time.Time

func (m popupModel) Init() tea.Cmd {
	return connectCmd()
}

func connectCmd() tea.Cmd {
	return func() tea.Msg {
		sockPath := daemon.SocketPath(*sessionID)
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
		clientID := fmt.Sprintf("popup:%s", *sessionID)
		return connectedMsg{conn: conn, clientID: clientID}
	}
}

func (m popupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectedMsg:
		m.conn = msg.conn
		m.clientID = msg.clientID
		m.connected = true
		debugLog.Printf("Connected as %s", m.clientID)
		go m.receiveLoop()
		m.sendSubscribe()
		return m, nil

	case disconnectedMsg:
		m.connected = false
		if m.conn != nil {
			m.conn.Close()
			m.conn = nil
		}
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return connectCmd()()
		})

	case renderMsg:
		m.content = msg.payload.Content
		m.pinnedContent = msg.payload.PinnedContent
		m.pinnedHeight = msg.payload.PinnedHeight
		m.regions = msg.payload.Regions
		m.pinnedRegions = msg.payload.PinnedRegions
		m.viewportOffset = msg.payload.ViewportOffset
		m.sequenceNum = msg.payload.SequenceNum
		m.sidebarBg = msg.payload.SidebarBg
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			if m.connected {
				m.sendUnsubscribe()
			}
			return m, tea.Quit
		}
		// Forward other keys as input events
		if m.connected {
			m.sendInput(&daemon.InputPayload{
				SequenceNum: m.sequenceNum,
				Type:        "key",
				Key:         msg.String(),
			})
		}
		return m, nil

	case tea.MouseMsg:
		if m.connected && msg.Action == tea.MouseActionPress {
			m.sendInput(&daemon.InputPayload{
				SequenceNum: m.sequenceNum,
				Type:        "mouse",
				MouseX:      msg.X,
				MouseY:      msg.Y,
				Button:      mouseButton(msg.Button),
				Action:      "press",
			})
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

func mouseButton(b tea.MouseButton) string {
	switch b {
	case tea.MouseButtonLeft:
		return "left"
	case tea.MouseButtonRight:
		return "right"
	case tea.MouseButtonMiddle:
		return "middle"
	case tea.MouseButtonWheelUp:
		return "wheelup"
	case tea.MouseButtonWheelDown:
		return "wheeldown"
	default:
		return "left"
	}
}

func (m popupModel) View() string {
	if !m.connected || m.content == "" {
		return ""
	}

	lines := strings.Split(m.content, "\n")
	// Clamp to visible height
	visibleLines := m.height - m.pinnedHeight
	if visibleLines < 0 {
		visibleLines = 0
	}

	// Apply viewport offset
	start := m.viewportOffset
	if start > len(lines)-visibleLines {
		start = len(lines) - visibleLines
	}
	if start < 0 {
		start = 0
	}
	end := start + visibleLines
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[start:end]

	// Pad short lines
	for i, l := range visible {
		lw := runewidth.StringWidth(stripAnsi(l))
		if lw < m.width {
			visible[i] = l + strings.Repeat(" ", m.width-lw)
		}
	}

	result := strings.Join(visible, "\n")

	// Append pinned content below scrollable area
	if m.pinnedContent != "" {
		result += "\n" + m.pinnedContent
	}
	return result
}

func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (m *popupModel) receiveLoop() {
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
					if globalProgram != nil {
						globalProgram.Send(renderMsg{payload: &payload})
					}
				}
			}
		case daemon.MsgPong:
			// keep-alive
		}
	}
	if globalProgram != nil {
		globalProgram.Send(disconnectedMsg{})
	}
}

func (m *popupModel) sendMessage(msg daemon.Message) {
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

func (m *popupModel) sendSubscribe() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgSubscribe,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: "TrueColor",
		},
	})
}

func (m *popupModel) sendUnsubscribe() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgUnsubscribe,
		ClientID: m.clientID,
	})
}

func (m *popupModel) sendResize() {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgResize,
		ClientID: m.clientID,
		Payload: daemon.ResizePayload{
			Width:  m.width,
			Height: m.height,
		},
	})
}

func (m *popupModel) sendInput(input *daemon.InputPayload) {
	m.sendMessage(daemon.Message{
		Type:     daemon.MsgInput,
		ClientID: m.clientID,
		Payload:  input,
	})
}

var globalProgram *tea.Program

func main() {
	flag.Parse()

	debugLog = log.New(io.Discard, "", 0)

	// Get session ID from environment if not provided
	if *sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			*sessionID = strings.TrimSpace(string(out))
		}
	}

	lipgloss.SetColorProfile(termenv.NewOutput(os.Stdout).ColorProfile())

	resetTerminal := func() {
		fmt.Print("\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1005l\033[?1006l\033[?1015l")
		fmt.Print("\033[?1049l")
		fmt.Print("\033[?2004l")
		fmt.Print("\033[0m\033[?25h")
		os.Stdout.Sync()
	}
	resetTerminal()
	defer resetTerminal()

	model := popupModel{
		width:  80,
		height: 24,
		sendMu: &sync.Mutex{},
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
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
		os.Exit(1)
	}
	resetTerminal()
}
