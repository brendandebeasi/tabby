// Package sidebarpopup is the display-popup variant of the sidebar renderer.
// Exported as the `tabby sidebar-popup` subcommand.
package sidebarpopup

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

var sessionID *string

// Initialize the flag pointer at package load time. Run() reassigns via its
// own FlagSet.
func init() {
	empty := ""
	sessionID = &empty
}

var debugLog *log.Logger

// popupModel is a minimal BubbleTea model for the sidebar popup.
// It connects to the daemon as "popup:{sessionID}", receives render frames,
// and exits on Esc (which closes the tmux display-popup).
type popupModel struct {
	conn      net.Conn
	target    daemon.RenderTarget
	clientID  string // derived from target.Key(); kept for log continuity
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
	conn   net.Conn
	target daemon.RenderTarget
}
type disconnectedMsg struct{}
type renderMsg struct{ payload *daemon.RenderPayload }
type tickMsg time.Time

func (m popupModel) Init() tea.Cmd {
	return connectCmd()
}

func connectCmd() tea.Cmd {
	return func() tea.Msg {
		conn, err := renderer.Connect(daemon.SocketPath(*sessionID), 10, 100*time.Millisecond)
		if err != nil {
			debugLog.Printf("Failed to connect to daemon: %v", err)
			return disconnectedMsg{}
		}
		target := daemon.RenderTarget{Kind: daemon.TargetSidebarPopup, Instance: *sessionID}
		return connectedMsg{conn: conn, target: target}
	}
}

func (m popupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case connectedMsg:
		m.conn = msg.conn
		m.target = msg.target
		m.clientID = msg.target.Key()
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
			// Tap anywhere on the big bottom close button dismisses the popup.
			if msg.Y >= m.height-closeButtonHeight {
				m.sendUnsubscribe()
				return m, tea.Quit
			}
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
		// Even before the first render, paint the whole surface in the sidebar bg
		// so no dark tmux-default flashes through.
		if m.sidebarBg != "" && m.width > 0 && m.height > 0 {
			blank := m.bgStyle().Render(strings.Repeat(" ", m.width))
			rows := make([]string, m.height)
			for i := range rows {
				rows[i] = blank
			}
			return strings.Join(rows, "\n")
		}
		return ""
	}

	bgStyle := m.bgStyle()
	// bgPad returns s padded to the full width with bg-coloured spaces (so the
	// right-hand gap never shows the dark popup default).
	bgPad := func(s string) string {
		lw := runewidth.StringWidth(stripAnsi(s))
		if lw < m.width {
			return s + bgStyle.Render(strings.Repeat(" ", m.width-lw))
		}
		return s
	}
	blankRow := bgStyle.Render(strings.Repeat(" ", max0(m.width)))

	// Reserve the last closeButtonHeight rows for a big tap-to-close button
	// (mobile-friendly: Esc is awkward on a phone). Content keeps the rows above
	// it, so list click coordinates are unchanged.
	visibleLines := m.height - m.pinnedHeight - closeButtonHeight
	if visibleLines < 1 {
		visibleLines = 1
	}

	lines := strings.Split(m.content, "\n")
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

	out := make([]string, 0, m.height)
	for _, l := range visible {
		out = append(out, bgPad(l))
	}
	// Fill any remaining content rows so the surface is fully painted.
	for len(out) < visibleLines {
		out = append(out, blankRow)
	}

	result := strings.Join(out, "\n")
	if m.pinnedContent != "" {
		result += "\n" + m.pinnedContent
	}
	result += "\n" + m.closeButtonRows()
	return result
}

// bgStyle is the lipgloss style that paints a cell in the sidebar background.
func (m popupModel) bgStyle() lipgloss.Style {
	s := lipgloss.NewStyle()
	if m.sidebarBg != "" {
		s = s.Background(lipgloss.Color(m.sidebarBg))
	}
	return s
}

// closeButtonHeight is how many rows the big bottom close button occupies.
const closeButtonHeight = 3

// closeButtonRows renders the full-width, multi-row GRAY button block that
// dismisses the popup on tap. The whole block is the hit target (see the
// MouseMsg handler). Explicit gray bg + white fg so it renders identically on
// any theme/terminal (the earlier reverse-video bar was invisible on some).
func (m popupModel) closeButtonRows() string {
	const (
		grayBg  = "#5c5c5c"
		whiteFg = "#ffffff"
	)
	st := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color(whiteFg)).
		Background(lipgloss.Color(grayBg))
	blank := st.Render(strings.Repeat(" ", max0(m.width)))

	label := "☰  Close" // hamburger, echoing the button that opened it
	lw := runewidth.StringWidth(label)
	pad := m.width - lw
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	labelRow := st.Render(strings.Repeat(" ", left) + label + strings.Repeat(" ", right))

	rows := make([]string, 0, closeButtonHeight)
	for len(rows) < closeButtonHeight {
		if len(rows) == closeButtonHeight/2 {
			rows = append(rows, labelRow)
		} else {
			rows = append(rows, blank)
		}
	}
	return strings.Join(rows, "\n")
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
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
	renderer.ReceiveMessages(m.conn, func(msg daemon.Message) bool {
		switch msg.Type {
		case daemon.MsgRender:
			var p daemon.RenderPayload
			if renderer.DecodePayload(msg, &p) && globalProgram != nil {
				globalProgram.Send(renderMsg{payload: &p})
			}
		case daemon.MsgPong:
			// keep-alive
		}
		return true
	})
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
		Type:   daemon.MsgSubscribe,
		Target: m.target,
		Payload: daemon.ResizePayload{
			Width:        m.width,
			Height:       m.height,
			ColorProfile: "TrueColor",
		},
	})
}

func (m *popupModel) sendUnsubscribe() {
	m.sendMessage(daemon.Message{
		Type:   daemon.MsgUnsubscribe,
		Target: m.target,
	})
}

func (m *popupModel) sendResize() {
	m.sendMessage(daemon.Message{
		Type:   daemon.MsgResize,
		Target: m.target,
		Payload: daemon.ResizePayload{
			Width:  m.width,
			Height: m.height,
		},
	})
}

func (m *popupModel) sendInput(input *daemon.InputPayload) {
	m.sendMessage(daemon.Message{
		Type:    daemon.MsgInput,
		Target:  m.target,
		Payload: input,
	})
}

var globalProgram *tea.Program

func Run(args []string) int {
	fs := flag.NewFlagSet("sidebar-popup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionID = fs.String("session", "", "tmux session ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}

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
		renderer.ResetTerminal()
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
		return 1
	}
	resetTerminal()
	return 0
}
