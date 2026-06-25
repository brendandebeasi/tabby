// Package dashlayoutpopup is the display-popup binary for the dashboard
// layout-style picker. Exported as the `tabby render dash-layout-popup`
// subcommand and launched from the daemon's dashboard_layout_picker action
// (bound to prefix+L).
//
// Flow:
//
//  1. Read the currently-persisted arrangement from the global tmux option
//     @tabby_dash_layout so the cursor starts on it.
//  2. Render a small TUI: a vertical menu of the 5 native tmux arrangements on
//     top and a live ASCII preview of the highlighted one below. Arrow keys /
//     j/k move; Enter applies; Esc/q/Ctrl-C cancels.
//  3. On Enter, post the chosen layout back to the daemon over its unix socket
//     as a dashboard_set_layout action (same envelope the `dashboard`
//     subcommand uses), then quit. The daemon persists it and, if a dashboard
//     window is live, re-applies select-layout immediately.
//
// The popup talks to the daemon only on Enter, so cancelling changes nothing.
package dashlayoutpopup

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/renderer"
)

// layoutChoice is one selectable arrangement: its tmux layout name, a short
// human label, and a fixed-size ASCII preview (box-drawing only, width-stable).
type layoutChoice struct {
	name    string   // tmux select-layout name
	label   string   // menu label
	preview []string // ASCII art, padded to previewRows
}

const previewRows = 5

// choices are the 5 native tmux layouts, in menu order. "tiled" is the default.
// Previews are deliberately small and width-stable (no ambiguous-width glyphs).
var choices = []layoutChoice{
	{"tiled", "Grid", []string{
		"┌───┬───┐",
		"│   │   │",
		"├───┼───┤",
		"│   │   │",
		"└───┴───┘",
	}},
	{"even-horizontal", "Columns", []string{
		"┌─┬─┬─┬─┐",
		"│ │ │ │ │",
		"│ │ │ │ │",
		"│ │ │ │ │",
		"└─┴─┴─┴─┘",
	}},
	{"even-vertical", "Rows", []string{
		"┌───────┐",
		"├───────┤",
		"├───────┤",
		"├───────┤",
		"└───────┘",
	}},
	{"main-vertical", "Main + stack", []string{
		"┌─────┬─┐",
		"│     ├─┤",
		"│     ├─┤",
		"│     ├─┤",
		"└─────┴─┘",
	}},
	{"main-horizontal", "Main + row", []string{
		"┌───────┐",
		"│       │",
		"│       │",
		"├─┬─┬─┬─┤",
		"└─┴─┴─┴─┘",
	}},
	// "-auto" modes: same geometry as the main-* layouts, but the ACTIVE pane is
	// always the big one (it follows focus). The "*" marks the slot the focused
	// pane occupies.
	{"main-vertical-auto", "Main + stack (active)", []string{
		"┌─────┬─┐",
		"│  *  ├─┤",
		"│     ├─┤",
		"│     ├─┤",
		"└─────┴─┘",
	}},
	{"main-horizontal-auto", "Main + row (active)", []string{
		"┌───────┐",
		"│   *   │",
		"│       │",
		"├─┬─┬─┬─┤",
		"└─┴─┴─┴─┘",
	}},
}

// ── styling (dark card; Background set on every text style so near-white text
// never washes out on light terminals — the lipgloss card-background trap) ──

const (
	bgColor   = "#1a1a2e"
	fgColor   = "#e6e6e6"
	dimColor  = "#9a9ab0"
	accent    = "#7aa2f7"
	selFg     = "#1a1a2e"
	previewFg = "#8be9c0"
)

func bg(s lipgloss.Style) lipgloss.Style { return s.Background(lipgloss.Color(bgColor)) }

var (
	cardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Padding(1, 2)
	titleStyle   = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(accent)).Bold(true))
	dimStyle     = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(dimColor)))
	itemStyle    = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor)))
	selItemStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(accent)).
			Foreground(lipgloss.Color(selFg)).
			Bold(true)
	previewStyle = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(previewFg)))
)

type model struct {
	sessionID string
	cursor    int  // index into choices
	current   int  // index of the persisted/active layout (marked with a dot)
	chosen    bool // Enter pressed (vs. cancelled)
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.chosen = true
			// Best-effort: tell the daemon to apply + persist the choice. A
			// failure is silent (the popup is closing); the user can retry.
			_ = sendSetLayout(m.sessionID, choices[m.cursor].name)
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Dashboard layout") + "\n\n")

	for i, c := range choices {
		marker := " "
		if i == m.current {
			marker = "•" // currently-active arrangement
		}
		label := fmt.Sprintf(" %s %s ", marker, c.label)
		if i == m.cursor {
			b.WriteString(" " + selItemStyle.Render("▸"+label) + "\n")
		} else {
			b.WriteString(" " + itemStyle.Render(" "+label) + "\n")
		}
	}

	b.WriteString("\n")
	for _, line := range padPreview(choices[m.cursor].preview) {
		b.WriteString("   " + previewStyle.Render(line) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ move · Enter apply · Esc cancel"))

	return cardStyle.Render(b.String())
}

// padPreview pads (or trims) an ASCII preview to exactly previewRows lines so
// the box below the menu keeps a constant height as the cursor moves.
func padPreview(lines []string) []string {
	out := make([]string, 0, previewRows)
	out = append(out, lines...)
	for len(out) < previewRows {
		out = append(out, "")
	}
	return out[:previewRows]
}

// currentIndex returns the index of name in choices, or 0 (the default "tiled")
// when name is empty or unrecognized.
func currentIndex(name string) int {
	for i, c := range choices {
		if c.name == name {
			return i
		}
	}
	return 0
}

// readCurrentLayout reads the persisted dashboard arrangement from the global
// tmux option, or "" if unset.
func readCurrentLayout() string {
	out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_dash_layout").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sendSetLayout posts a dashboard_set_layout action to the daemon's unix socket
// for the given session, asking it to apply + persist the chosen arrangement.
// Mirrors the envelope the `dashboard` subcommand uses.
func sendSetLayout(sessionID, layout string) error {
	if sessionID == "" {
		return fmt.Errorf("no session id")
	}
	conn, err := net.DialTimeout("unix", daemon.SocketPath(sessionID), 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	msg := daemon.Message{
		Type:   daemon.MsgInput,
		Target: daemon.RenderTarget{Kind: daemon.TargetHook, Instance: "tabby-dash-layout"},
		Payload: daemon.InputPayload{
			Type:           "action",
			ResolvedAction: "dashboard_set_layout",
			ResolvedTarget: layout,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}

// Run is the `tabby render dash-layout-popup` entry point.
func Run(args []string) int {
	fs := flag.NewFlagSet("dash-layout-popup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionFlag := fs.String("session", "", "tmux session ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sessionID := strings.TrimSpace(*sessionFlag)
	if sessionID == "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output(); err == nil {
			sessionID = strings.TrimSpace(string(out))
		}
	}

	cur := currentIndex(readCurrentLayout())
	m := model{sessionID: sessionID, cursor: cur, current: cur}

	// Match sister popup binaries: clamp the color profile so lipgloss renders
	// truecolor on capable terminals.
	lipgloss.SetColorProfile(termenv.NewOutput(os.Stdout).ColorProfile())

	resetTerminal := func() {
		renderer.ResetTerminal()
		fmt.Print("\033[0m\033[?25h")
		os.Stdout.Sync()
	}
	resetTerminal()
	defer resetTerminal()

	p := tea.NewProgram(m, tea.WithAltScreen())

	// tmux sends SIGINT/SIGTERM on display-popup close; quit gracefully so the
	// terminal is reset cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		if p != nil {
			p.Send(tea.Quit())
		}
	}()

	if _, runErr := p.Run(); runErr != nil {
		resetTerminal()
		fmt.Fprintf(os.Stderr, "dash-layout-popup: %v\n", runErr)
		return 1
	}
	return 0
}
