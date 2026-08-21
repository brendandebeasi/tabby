// Package landing is the full-pane launcher a new tmux window opens to.
// Exported as the `tabby landing` subcommand.
//
// The page is a join over data tabby already owns: local directory colors and
// icons from the cwd-colors state file, ssh host colors and groups from the
// remote_hosts globs in the config. The only new column is a usage rank. That
// join is performed offline by the tabby-landing skill, which writes
// landing.yaml; this package renders it and nothing else.
//
// The chosen command is written to stdout, not executed, so the calling shell
// can eval it:
//
//	eval "$(tabby landing)"
//
// Running the command in the pane's own interactive shell is what keeps tmux
// reporting pane_current_command as "ssh" for remote entries, which the daemon
// needs for the remote icon and host color. The TUI itself renders to stderr so
// stdout carries only the command.
package landing

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brendandebeasi/tabby/pkg/textwidth"
)

// tickInterval paces the header sweep. Slow enough to read as motion rather
// than flicker, and cheap: one repaint of a five-row banner.
const tickInterval = 120 * time.Millisecond

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type model struct {
	all      []Entry
	filtered []Entry
	groups   []group
	cursor   int
	filter   string
	width    int
	height   int
	phase    int
	chosen   string
	quitting bool
}

func newModel(entries []Entry) model {
	m := model{all: entries, width: 80, height: 24}
	m.refilter()
	return m
}

// refilter recomputes the filtered set and its grouping, keeping the cursor in
// range. Called on every filter edit.
func (m *model) refilter() {
	m.filtered = applyFilter(m.all, m.filter)
	m.groups = groupEntries(m.filtered)
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// flat is the entries in the order they are drawn, which is the order the
// cursor walks: group by group, entry by entry.
func (m model) flat() []Entry {
	out := make([]Entry, 0, len(m.filtered))
	for _, g := range m.groups {
		out = append(out, g.entries...)
	}
	return out
}

func (m model) cursorEntry() *Entry {
	f := m.flat()
	if m.cursor < 0 || m.cursor >= len(f) {
		return nil
	}
	return &f[m.cursor]
}

func (m model) Init() tea.Cmd {
	return tick()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.phase++
		return m, tick()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "esc":
		// Esc clears a filter first and only leaves on a second press, so a
		// mistyped filter does not drop you out of the page.
		if m.filter != "" {
			m.filter = ""
			m.refilter()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}

	case "down", "ctrl+n":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}

	case "home", "ctrl+a":
		m.cursor = 0

	case "end", "ctrl+e":
		m.cursor = len(m.filtered) - 1

	case "left", "right":
		m.cursor = m.jumpGroup(msg.String() == "right")

	case "enter":
		if e := m.cursorEntry(); e != nil {
			m.chosen = e.Command()
		}
		m.quitting = true
		return m, tea.Quit

	case "ctrl+u":
		m.filter = ""
		m.refilter()

	case "backspace", "ctrl+h":
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.refilter()
		}

	case " ":
		m.filter += " "
		m.refilter()

	default:
		// A fast typist or a paste arrives as one KeyRunes message carrying
		// several runes, so take them all rather than only single-rune events.
		if msg.Type == tea.KeyRunes && !msg.Alt {
			if s := printableRunes(msg.Runes); s != "" {
				m.filter += s
				m.cursor = 0
				m.refilter()
			}
		}
	}
	return m, nil
}

// printableRunes drops control characters from a key event so a stray escape
// sequence or a pasted newline cannot land in the filter string.
func printableRunes(rs []rune) string {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if unicode.IsPrint(r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// jumpGroup moves the cursor to the first entry of the next or previous group.
func (m model) jumpGroup(forward bool) int {
	starts := []int{}
	n := 0
	for _, g := range m.groups {
		starts = append(starts, n)
		n += len(g.entries)
	}
	if len(starts) == 0 {
		return m.cursor
	}
	if forward {
		for _, s := range starts {
			if s > m.cursor {
				return s
			}
		}
		return starts[0]
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] < m.cursor {
			return starts[i]
		}
	}
	return starts[len(starts)-1]
}

func (m model) View() string {
	// Bubbletea prints the final frame on exit. Returning an empty view keeps
	// the pane clean for the command the shell is about to run.
	if m.quitting {
		return ""
	}

	compact := m.width <= compactMax
	var out []string

	header := renderHeader(m.width, m.phase)
	out = append(out, strings.Split(header, "\n")...)
	out = append(out, "")

	if len(m.filtered) == 0 {
		out = append(out, renderEmpty(m.filter))
	} else {
		rows, cursorRow := renderGroups(m.groups, m.cursorEntry(), m.width, compact)
		out = append(out, m.window(rows, cursorRow)...)
	}

	out = append(out, "")
	out = append(out, renderHint(m.width, m.filter))

	for i, line := range out {
		out[i] = textwidth.Clamp(line, m.width)
	}
	return strings.Join(out, "\n")
}

// window scrolls rows so cursorRow stays visible within the height left after
// the header and hint bar.
func (m model) window(rows []string, cursorRow int) []string {
	avail := m.height - len(strings.Split(renderHeader(m.width, m.phase), "\n")) - 3
	if avail < 1 || len(rows) <= avail {
		return rows
	}
	start := 0
	if cursorRow >= avail {
		start = cursorRow - avail + 1
	}
	if start+avail > len(rows) {
		start = len(rows) - avail
	}
	if start < 0 {
		start = 0
	}
	return rows[start : start+avail]
}

// Run is the `tabby landing` entry point.
func Run(args []string) int {
	fs := flag.NewFlagSet("landing", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("config", "", "path to landing.yaml (default: tabby config dir)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" {
		*path = ConfigPath()
	}

	cfg, err := Load(*path)
	if err != nil {
		// A missing or broken landing.yaml must not stop a new tab from
		// opening. Say why on stderr, print nothing on stdout, exit clean.
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "tabby: no %s yet; run the tabby-landing skill to generate one\n", *path)
		} else {
			fmt.Fprintf(os.Stderr, "tabby: %v\n", err)
		}
		return 0
	}
	if len(cfg.Entries) == 0 {
		fmt.Fprintf(os.Stderr, "tabby: %s has no usable entries\n", *path)
		return 0
	}

	// The TUI draws on stderr so stdout carries only the chosen command.
	p := tea.NewProgram(
		newModel(cfg.Entries),
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
	)
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tabby: landing: %v\n", err)
		return 1
	}
	if fm, ok := final.(model); ok && fm.chosen != "" {
		fmt.Println(fm.chosen)
	}
	return 0
}
