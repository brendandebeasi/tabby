// Package petqapopup is the display-popup binary for the cat's Q&A loop.
// Exported as the `tabby render pet-qa-popup` subcommand.
//
// Flow:
//
//  1. Dial the daemon socket and issue PetQAOpGetPending. If there's no
//     pending question, print a friendly note and exit 0.
//  2. Render the question with a framed visual. choice-kind questions get
//     a vertical selectable list (arrow keys / j/k, Enter to submit).
//     free_text questions get a single-line text input (Enter when
//     non-empty, Esc cancels).
//  3. On submit, send PetQAOpAnswer. On success, briefly show a
//     confirmation (including any newly distilled trait) for ~600ms then
//     exit 0. On error, render the error and wait for any key before
//     exiting 1.
//
// Phase 2 of the Q&A loop; see
// /Users/b/.claude/plans/wiggly-discovering-starlight.md.
package petqapopup

import (
	"bufio"
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

// confirmDelay is how long the success screen sits on-screen so the user
// can read any newly-distilled trait before the popup closes.
const confirmDelay = 600 * time.Millisecond

// dialTimeout caps how long we wait to reach the daemon socket; the daemon
// is local, so a slow dial almost certainly means "not running".
const dialTimeout = 2 * time.Second

// requestTimeout caps the full request/response round-trip. The daemon's
// Q&A handlers are synchronous and cheap (no LLM); 5s is generous.
const requestTimeout = 5 * time.Second

// ── view phases ─────────────────────────────────────────────────────────

type phase int

const (
	phaseLoading phase = iota
	phaseNoPending
	phasePrompting
	phaseSubmitting
	phaseConfirming
	phaseError
)

// ── messages ────────────────────────────────────────────────────────────

type pendingLoadedMsg struct {
	pending *daemon.PendingQuestion
	err     error
}

type answerSubmittedMsg struct {
	resp *daemon.PetQAResponse
	err  error
}

// exitTickMsg fires after the confirmation screen has been visible long
// enough; receiving it triggers a graceful quit.
type exitTickMsg struct{}

// ── model ───────────────────────────────────────────────────────────────

type model struct {
	sessionID string

	phase   phase
	pending *daemon.PendingQuestion

	// Choice-kind state.
	selected int

	// Free-text state. We render `input` directly; the trailing cursor is
	// drawn by the View. No bubbles/textinput dep needed for one line.
	input string

	// Confirmation state. Captured from PetQAResponse so the success
	// screen can show any newly distilled trait.
	newTrait *daemon.PersonalityTrait

	// Error state.
	errText string

	// Exit code propagated up from Run.
	exitCode int

	// Terminal size. The popup is sized by tmux display-popup, but we
	// adapt to whatever dimensions bubbletea reports.
	width  int
	height int
}

func initialModel(sessionID string) model {
	return model{
		sessionID: sessionID,
		phase:     phaseLoading,
		width:     60,
		height:    20,
	}
}

func (m model) Init() tea.Cmd {
	// Run() pre-loads the pending question synchronously and seeds the
	// model in phasePrompting, so the TUI starts with everything it
	// needs. If the model ever ships from an entry point that doesn't
	// pre-load (e.g. a future test harness), kicking off the loader
	// here keeps that path working.
	if m.phase == phaseLoading {
		return loadPendingCmd(m.sessionID)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case pendingLoadedMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errText = msg.err.Error()
			m.exitCode = 1
			return m, nil
		}
		if msg.pending == nil {
			// Print the friendly note (mirrors `tabby pet ask` with no
			// pending) and exit immediately — no TUI required.
			m.phase = phaseNoPending
			return m, tea.Quit
		}
		m.pending = msg.pending
		m.phase = phasePrompting
		return m, nil

	case answerSubmittedMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.errText = msg.err.Error()
			m.exitCode = 1
			return m, nil
		}
		if msg.resp == nil || !msg.resp.OK {
			m.phase = phaseError
			if msg.resp != nil && msg.resp.Error != "" {
				m.errText = msg.resp.Error
			} else {
				m.errText = "the daemon refused the answer."
			}
			m.exitCode = 1
			return m, nil
		}
		m.newTrait = msg.resp.NewTrait
		m.phase = phaseConfirming
		return m, tea.Tick(confirmDelay, func(time.Time) tea.Msg {
			return exitTickMsg{}
		})

	case exitTickMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey factors out keyboard handling per-phase. Esc always exits
// without submitting (exit 0); the error phase exits on any key.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Universal: Ctrl-C is treated like Esc for tmux popup ergonomics.
	if key == "ctrl+c" {
		m.exitCode = 0
		return m, tea.Quit
	}

	switch m.phase {
	case phaseError:
		// Any key dismisses the error and propagates exit 1 from Run.
		return m, tea.Quit

	case phaseConfirming:
		// Confirmation is on a timer; allow Esc/Enter to skip the dwell.
		if key == "esc" || key == "enter" {
			return m, tea.Quit
		}
		return m, nil

	case phaseSubmitting:
		// Keys are ignored while the answer is in flight to avoid
		// double-submit or surprising cancels mid-write.
		return m, nil

	case phasePrompting:
		if key == "esc" {
			m.exitCode = 0
			return m, tea.Quit
		}
		if m.pending == nil {
			return m, nil
		}
		if m.pending.Kind == "choice" {
			return m.handleChoiceKey(key)
		}
		// Default to free_text behaviour for any non-"choice" kind. The
		// daemon may emit other kinds in future phases (e.g. "llm") with
		// the same answer shape; falling through here keeps the popup
		// usable rather than wedged on an unknown kind.
		return m.handleFreeTextKey(msg)
	}
	return m, nil
}

func (m model) handleChoiceKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(m.pending.Choices)-1 {
			m.selected++
		}
	case "home", "g":
		m.selected = 0
	case "end", "G":
		m.selected = len(m.pending.Choices) - 1
	case "enter":
		if len(m.pending.Choices) == 0 {
			return m, nil
		}
		answer := m.pending.Choices[m.selected]
		m.phase = phaseSubmitting
		return m, submitAnswerCmd(m.sessionID, m.pending.ID, answer)
	}
	// Number shortcuts: "1".."9" jump to that choice and (if valid)
	// submit. Mirrors the numbered display in View().
	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		idx := int(key[0] - '1')
		if idx < len(m.pending.Choices) {
			m.selected = idx
		}
	}
	return m, nil
}

func (m model) handleFreeTextKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter":
		trimmed := strings.TrimSpace(m.input)
		if trimmed == "" {
			// Disallow blank submissions; the daemon also rejects empty
			// answers but we'd rather not round-trip for it.
			return m, nil
		}
		m.phase = phaseSubmitting
		return m, submitAnswerCmd(m.sessionID, m.pending.ID, trimmed)
	case "backspace":
		if len(m.input) > 0 {
			// Trim by rune, not byte, so UTF-8 input doesn't get
			// truncated mid-codepoint.
			runes := []rune(m.input)
			m.input = string(runes[:len(runes)-1])
		}
		return m, nil
	}
	// Append printable runes. bubbletea sets msg.Runes for character
	// input; anything with a single rune and no modifier we treat as
	// typed text. This deliberately ignores arrows, function keys, etc.
	if len(msg.Runes) > 0 && !msg.Alt {
		for _, r := range msg.Runes {
			if r >= 0x20 && r != 0x7f {
				m.input += string(r)
			}
		}
	}
	return m, nil
}

// ── view ────────────────────────────────────────────────────────────────

// Styles. Kept package-level so View doesn't reallocate per frame; lipgloss
// styles are immutable values, copy is cheap, but no need to recompute.
var (
	frameStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#a78bfa")).
			Padding(0, 1)
	questionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f8fafc"))
	choiceStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#cbd5e1"))
	selectedChoiceStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#0f172a")).
				Background(lipgloss.Color("#a78bfa"))
	hintStyle = lipgloss.NewStyle().
			Faint(true).
			Foreground(lipgloss.Color("#94a3b8"))
	inputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f8fafc"))
	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#34d399"))
	traitStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("#fbbf24"))
	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ef4444"))
)

func (m model) View() string {
	switch m.phase {
	case phaseLoading:
		// Brief flicker until pendingLoadedMsg lands; the daemon round-
		// trip is fast enough that this is rarely visible.
		return hintStyle.Render("  checking with the cat...")
	case phaseNoPending:
		// We Quit immediately on this phase, so View runs at most once
		// before bubbletea tears down. Stay empty to keep the popup
		// quiet — Run() prints the friendly note on stdout after Quit.
		return ""
	case phasePrompting:
		return m.renderPrompt()
	case phaseSubmitting:
		return m.renderPrompt() + "\n" + hintStyle.Render("  sending...")
	case phaseConfirming:
		return m.renderConfirmation()
	case phaseError:
		return m.renderError()
	}
	return ""
}

func (m model) renderPrompt() string {
	if m.pending == nil {
		return ""
	}

	// Frame width adapts to the popup. Subtract a couple of cells for
	// the border + padding so wrapping doesn't fight the frame.
	innerWidth := m.width - 4
	if innerWidth < 20 {
		innerWidth = 20
	}

	var b strings.Builder
	b.WriteString(questionStyle.Width(innerWidth).Render(m.pending.Text))
	b.WriteString("\n\n")

	if m.pending.Kind == "choice" {
		for i, c := range m.pending.Choices {
			line := fmt.Sprintf("  %d. %s", i+1, c)
			if i == m.selected {
				b.WriteString(selectedChoiceStyle.Render(line))
			} else {
				b.WriteString(choiceStyle.Render(line))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(hintStyle.Render("  ↑/↓ or j/k to move · Enter to choose · Esc to cancel"))
	} else {
		// free_text and anything else we don't recognise: show a single-
		// line input with a fake block cursor at the end.
		cursor := "▏"
		visible := m.input + cursor
		// Crude truncation to inner width; the input doesn't scroll.
		if lw := lipgloss.Width(visible); lw > innerWidth-2 {
			// Drop runes from the left so the cursor stays visible —
			// matches what most line editors do near the right edge.
			runes := []rune(visible)
			start := lw - (innerWidth - 2)
			if start < len(runes) {
				visible = string(runes[start:])
			}
		}
		b.WriteString("  ")
		b.WriteString(inputStyle.Render(visible))
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("  type your answer · Enter to send · Esc to cancel"))
	}

	return frameStyle.Width(m.width - 2).Render(b.String())
}

func (m model) renderConfirmation() string {
	var b strings.Builder
	b.WriteString(successStyle.Render("  thanks — the cat heard you."))
	if m.newTrait != nil && strings.TrimSpace(m.newTrait.Text) != "" {
		b.WriteString("\n")
		b.WriteString(traitStyle.Render("  learned: " + m.newTrait.Text))
	}
	return frameStyle.Width(m.width - 2).Render(b.String())
}

func (m model) renderError() string {
	var b strings.Builder
	b.WriteString(errorStyle.Render("  something went wrong"))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("  " + m.errText))
	b.WriteString("\n\n")
	b.WriteString(hintStyle.Render("  press any key to close"))
	return frameStyle.Width(m.width - 2).Render(b.String())
}

// ── socket transport ────────────────────────────────────────────────────

// loadPendingCmd asks the daemon for the current pending question. We
// emit a single pendingLoadedMsg whether the request succeeded, failed,
// or returned no pending question.
func loadPendingCmd(sessionID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := request(sessionID, &daemon.PetQARequest{Op: daemon.PetQAOpGetPending})
		if err != nil {
			return pendingLoadedMsg{err: err}
		}
		if !resp.OK {
			return pendingLoadedMsg{err: fmt.Errorf("%s", resp.Error)}
		}
		return pendingLoadedMsg{pending: resp.Pending}
	}
}

// submitAnswerCmd sends the user's answer to the daemon. The daemon
// validates against the pending question and may produce a fresh trait;
// either way we forward the whole response to the model.
func submitAnswerCmd(sessionID, id, answer string) tea.Cmd {
	return func() tea.Msg {
		resp, err := request(sessionID, &daemon.PetQARequest{
			Op:     daemon.PetQAOpAnswer,
			ID:     id,
			Answer: answer,
		})
		if err != nil {
			return answerSubmittedMsg{err: err}
		}
		return answerSubmittedMsg{resp: resp}
	}
}

// request is a tiny one-shot client mirroring pet.request — dial the
// session socket, write a single MsgPetQA, read a single MsgPetQA reply.
// Kept local rather than promoted to pkg/daemon because the CLI and
// popup are the only callers today and lifting it would require shared
// session-resolution logic that doesn't otherwise belong in the package.
func request(sessionID string, req *daemon.PetQARequest) (*daemon.PetQAResponse, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("no tmux session — start tabby first.")
	}
	conn, err := net.DialTimeout("unix", daemon.SocketPath(sessionID), dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("tabby daemon not running in this session — start tabby first.")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))

	msg := daemon.Message{Type: daemon.MsgPetQA, Payload: req}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("daemon closed connection without a response")
	}
	var respMsg daemon.Message
	if err := json.Unmarshal(scanner.Bytes(), &respMsg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if respMsg.Type != daemon.MsgPetQA {
		return nil, fmt.Errorf("unexpected response type %q", respMsg.Type)
	}
	payloadBytes, err := json.Marshal(respMsg.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	var resp daemon.PetQAResponse
	if err := json.Unmarshal(payloadBytes, &resp); err != nil {
		return nil, fmt.Errorf("decode response payload: %w", err)
	}
	return &resp, nil
}

// ── entry point ─────────────────────────────────────────────────────────

// Run is the renderdispatch entry point. Returns the exit code the
// outer `tabby` command should propagate.
//
// The function does a synchronous daemon probe before starting the TUI
// so the two non-interactive paths ("no pending question" and "daemon
// unreachable") print plain stdout/stderr messages and exit cleanly —
// useful for both ergonomics (no flash of empty TUI) and smoke testing
// the binary outside a tmux popup.
func Run(args []string) int {
	fs := flag.NewFlagSet("pet-qa-popup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionFlag := fs.String("session", "", "tmux session ID (auto-detected if omitted)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	sessionID := *sessionFlag
	if sessionID == "" {
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		if err == nil {
			sessionID = strings.TrimSpace(string(out))
		}
	}

	// Probe the daemon before lighting up the TUI. If it fails we print
	// to stderr and exit; if there's no pending question we print to
	// stdout and exit; only the question-present case starts bubbletea.
	resp, err := request(sessionID, &daemon.PetQARequest{Op: daemon.PetQAOpGetPending})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pet-qa-popup:", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "pet-qa-popup:", resp.Error)
		return 1
	}
	if resp.Pending == nil {
		fmt.Println("the cat has nothing to ask right now.")
		return 0
	}

	// Match sister popup binaries: clamp the color profile so lipgloss
	// renders truecolor on capable terminals.
	lipgloss.SetColorProfile(termenv.NewOutput(os.Stdout).ColorProfile())

	resetTerminal := func() {
		renderer.ResetTerminal()
		fmt.Print("\033[0m\033[?25h")
		os.Stdout.Sync()
	}
	resetTerminal()
	defer resetTerminal()

	m := initialModel(sessionID)
	// Skip the loading phase — we already have the pending question.
	m.pending = resp.Pending
	m.phase = phasePrompting
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Honor SIGINT/SIGTERM by asking the program to quit gracefully so
	// the terminal gets reset cleanly. tmux sends one of these on
	// display-popup close.
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
		fmt.Fprintf(os.Stderr, "pet-qa-popup: %v\n", runErr)
		return 1
	}

	if fm, ok := finalModel.(model); ok {
		return fm.exitCode
	}
	return 0
}
