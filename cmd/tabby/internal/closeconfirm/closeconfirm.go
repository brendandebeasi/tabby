// Package closeconfirm is the display-popup binary for the "close this
// window?" confirmation. Exported as the `tabby render close-confirm`
// subcommand.
//
// It replaces the old cramped `tmux display-menu` confirm with two BIG,
// full-width, tappable buttons — Keep it open / Close window — that are also
// driven by the keyboard:
//
//	tap a button        act immediately (Keep = dismiss, Close = kill)
//	y                   close the window
//	n / q / Esc / C-c   keep it open (dismiss)
//	up/down, k/j, tab   move the selection between the two buttons
//	Enter               activate the selected button
//
// On confirm the renderer runs `tmux kill-window -t <target>` itself, so the
// launcher (coordinator.launchCloseConfirmPopup) is fire-and-forget. The safe
// option (Keep) starts selected, so a stray Enter never closes a window.
package closeconfirm

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/brendandebeasi/tabby/pkg/renderer"
)

// choice identifies which of the two buttons is selected.
type choice int

const (
	choiceKeep choice = iota
	choiceClose
)

// ── styles ──────────────────────────────────────────────────────────────
//
// The popup paints itself on a dark card so it reads as a modal on any
// terminal theme (lipgloss resets attributes per segment, so the bg has to
// live on every style, not just the frame). Keep is a calm green, Close a
// clear destructive red; the selected button brightens + bolds so a keyboard
// user can see what Enter will do.
const cardBg = "#1e293b"

const (
	keepBg     = "#2f6f4f"
	keepBgSel  = "#3f9268"
	closeBg    = "#8f3a3a"
	closeBgSel = "#c04a4a"
	btnFg      = "#ffffff"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f8fafc")).
			Background(lipgloss.Color(cardBg))
	hintStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(lipgloss.Color("#94a3b8")).
			Background(lipgloss.Color(cardBg))
)

// ── model ───────────────────────────────────────────────────────────────

type model struct {
	target   string // tmux window id to kill on confirm
	selected choice
	width    int
	height   int

	// confirmed is read by Run() after the TUI exits; it performs the
	// kill-window side effect outside the render loop.
	confirmed bool
}

func initialModel(target string) model {
	return model{
		target:   target,
		selected: choiceKeep, // safe default: Enter keeps the window
		width:    60,
		height:   18,
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.confirmed = true
			return m, tea.Quit
		case "n", "N", "q", "esc", "ctrl+c":
			m.confirmed = false
			return m, tea.Quit
		case "enter", " ":
			m.confirmed = m.selected == choiceClose
			return m, tea.Quit
		case "up", "k", "left", "h":
			m.selected = choiceKeep
			return m, nil
		case "down", "j", "right", "l":
			m.selected = choiceClose
			return m, nil
		case "tab":
			if m.selected == choiceKeep {
				m.selected = choiceClose
			} else {
				m.selected = choiceKeep
			}
			return m, nil
		}
		return m, nil

	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		// A tap acts immediately on the button it lands in — most intuitive
		// on touch. Taps outside either band are ignored (they neither
		// confirm nor dismiss), so a stray tap can't close a window.
		b := computeBands(m.height)
		switch {
		case msg.Y >= b.keepStart && msg.Y <= b.keepEnd:
			m.confirmed = false
			return m, tea.Quit
		case msg.Y >= b.closeStart && msg.Y <= b.closeEnd:
			m.confirmed = true
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

// ── layout ──────────────────────────────────────────────────────────────

// bands holds the absolute (0-indexed) row ranges of each region so View and
// the mouse hit-test agree exactly on where the buttons are.
type bands struct {
	titleStart, titleEnd int
	keepStart, keepEnd   int
	closeStart, closeEnd int
	hintRow              int
}

// computeBands splits h rows into: a small title area, two equal button
// bands separated by a one-row gap, and a one-row hint at the bottom.
func computeBands(h int) bands {
	if h < 8 {
		// Degenerate tiny popup: give each region one row and let the rest
		// clamp. Better a squished dialog than a panic.
		return bands{
			titleStart: 0, titleEnd: 0,
			keepStart: 1, keepEnd: 1,
			closeStart: 2, closeEnd: 2,
			hintRow: max0(h - 1),
		}
	}
	titleStart, titleEnd := 0, 2 // 3 rows: pad, title, pad
	hintRow := h - 1
	top := titleEnd + 1
	bottom := hintRow - 1 // one blank row above the hint
	avail := bottom - top // rows for two bands + a one-row gap
	if avail < 3 {
		avail = 3
	}
	gap := 1
	bandH := (avail - gap) / 2
	if bandH < 1 {
		bandH = 1
	}
	keepStart := top
	keepEnd := keepStart + bandH - 1
	closeStart := keepEnd + 1 + gap
	closeEnd := closeStart + bandH - 1
	return bands{
		titleStart: titleStart, titleEnd: titleEnd,
		keepStart: keepStart, keepEnd: keepEnd,
		closeStart: closeStart, closeEnd: closeEnd,
		hintRow: hintRow,
	}
}

// ── view ────────────────────────────────────────────────────────────────

func (m model) View() string {
	w, h := m.width, m.height
	if w < 4 || h < 4 {
		return ""
	}
	b := computeBands(h)
	cardFill := lipgloss.NewStyle().Background(lipgloss.Color(cardBg)).
		Render(strings.Repeat(" ", w))

	rows := make([]string, h)
	for i := range rows {
		rows[i] = cardFill
	}

	// Title (centered on the middle row of the title area).
	titleRow := (b.titleStart + b.titleEnd) / 2
	if titleRow >= 0 && titleRow < h {
		rows[titleRow] = centerOnCard("Close this window?", w, titleStyle)
	}

	// Buttons.
	keepRows := renderButton("Keep it open", "n", keepBg, keepBgSel,
		b.keepEnd-b.keepStart+1, w, m.selected == choiceKeep)
	for i, r := range keepRows {
		if idx := b.keepStart + i; idx >= 0 && idx < h {
			rows[idx] = r
		}
	}
	closeRows := renderButton("Close window", "y", closeBg, closeBgSel,
		b.closeEnd-b.closeStart+1, w, m.selected == choiceClose)
	for i, r := range closeRows {
		if idx := b.closeStart + i; idx >= 0 && idx < h {
			rows[idx] = r
		}
	}

	// Hint.
	if b.hintRow >= 0 && b.hintRow < h {
		rows[b.hintRow] = centerOnCard(
			"Enter = selected   |   y = close   |   n / Esc = keep", w, hintStyle)
	}

	return strings.Join(rows, "\n")
}

// renderButton draws a full-width, `rows`-tall colored button with its label
// centered on the middle row. The mnemonic key is shown in the label so the
// keyboard shortcut is discoverable; the selected button brightens, bolds,
// and gains ">  … <" markers so a keyboard user sees what Enter will do.
func renderButton(label, key, bg, bgSel string, rows, width int, selected bool) []string {
	fill := bg
	if selected {
		fill = bgSel
	}
	st := lipgloss.NewStyle().
		Foreground(lipgloss.Color(btnFg)).
		Background(lipgloss.Color(fill)).
		Bold(selected)
	blank := st.Render(strings.Repeat(" ", max0(width)))

	text := fmt.Sprintf("%s  ( %s )", label, key)
	if selected {
		text = ">  " + text + "  <"
	}
	lw := runewidth.StringWidth(text)
	pad := width - lw
	if pad < 0 {
		pad = 0
		text = runewidth.Truncate(text, width, "")
		lw = runewidth.StringWidth(text)
		pad = width - lw
		if pad < 0 {
			pad = 0
		}
	}
	left := pad / 2
	right := pad - left
	labelRow := st.Render(strings.Repeat(" ", left) + text + strings.Repeat(" ", right))

	out := make([]string, 0, rows)
	mid := rows / 2
	for i := 0; i < rows; i++ {
		if i == mid {
			out = append(out, labelRow)
		} else {
			out = append(out, blank)
		}
	}
	return out
}

// centerOnCard centers s across width using the given style, padding the
// remainder with card-bg spaces so the whole row is painted.
func centerOnCard(s string, width int, style lipgloss.Style) string {
	lw := runewidth.StringWidth(s)
	if lw > width {
		s = runewidth.Truncate(s, width, "")
		lw = runewidth.StringWidth(s)
	}
	pad := width - lw
	left := pad / 2
	right := pad - left
	bg := lipgloss.NewStyle().Background(lipgloss.Color(cardBg))
	return bg.Render(strings.Repeat(" ", left)) +
		style.Render(s) +
		bg.Render(strings.Repeat(" ", right))
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

// ── entry point ─────────────────────────────────────────────────────────

// Run is the render-dispatch entry point. It starts the TUI, then — if the
// user confirmed — kills the target window outside the render loop.
func Run(args []string) int {
	fs := flag.NewFlagSet("close-confirm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	target := fs.String("target", "", "tmux window id to close on confirm")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	win := *target
	if win == "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
			win = strings.TrimSpace(string(out))
		}
	}
	if win == "" {
		fmt.Fprintln(os.Stderr, "close-confirm: no target window")
		return 1
	}

	lipgloss.SetColorProfile(termenv.NewOutput(os.Stdout).ColorProfile())

	resetTerminal := func() {
		renderer.ResetTerminal()
		fmt.Print("\033[0m\033[?25h")
		os.Stdout.Sync()
	}
	resetTerminal()
	defer resetTerminal()

	p := tea.NewProgram(initialModel(win), tea.WithAltScreen(), tea.WithMouseCellMotion())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		if p != nil {
			p.Send(tea.Quit())
		}
	}()

	finalModel, runErr := p.Run()
	resetTerminal()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "close-confirm: %v\n", runErr)
		return 1
	}

	if fm, ok := finalModel.(model); ok && fm.confirmed {
		// Perform the kill outside the TUI so the terminal is already reset.
		if err := exec.Command("tmux", "kill-window", "-t", win).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "close-confirm: kill-window: %v\n", err)
			return 1
		}
	}
	return 0
}
