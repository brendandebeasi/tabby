// Package degradedmodelspopup is the display-popup binary for the TeamClaude
// degraded-models warning. Exported as the `tabby render degraded-models-popup`
// subcommand and launched from the TeamClaude widget's warning icon.
//
// Flow:
//
//  1. Load tabby's config to find the teamclaude proxy URL + API key.
//  2. Fetch GET /teamclaude/models (degraded map) and GET /teamclaude/status
//     (for the current-account context line). The proxy is the source of
//     truth; we re-fetch here so the popup always shows fresh data when opened.
//  3. Render a TUI listing each actively-downgraded model, the smaller model it
//     is being routed to, its strike count, and a reset countdown — plus two
//     selectable links to Anthropic's status page and DownDetector. Arrow keys
//     / j/k move; Enter opens the link in a browser; Esc/q/Ctrl-C closes.
//
// The popup fetches its own data rather than going through the daemon socket,
// so it needs no new daemon protocol op. The --session flag is accepted for
// symmetry with the sister popup binaries but is unused.
package degradedmodelspopup

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/renderer"
	"github.com/brendandebeasi/tabby/pkg/teamclaude"
)

// fetchTimeout caps the proxy round-trips. The proxy is on the LAN; 5s is
// generous and keeps the popup from hanging if it's unreachable.
const fetchTimeout = 5 * time.Second

// Status / DownDetector links surfaced in the popup. Anthropic publishes
// incidents at status.anthropic.com; DownDetector aggregates user reports.
const (
	urlAnthropicStatus = "https://status.anthropic.com"
	urlDownDetector    = "https://downdetector.com/status/anthropic"
)

// link is a selectable footer action.
type link struct {
	label string
	url   string
}

var links = []link{
	{"Anthropic status", urlAnthropicStatus},
	{"DownDetector", urlDownDetector},
}

// degradedRow is one actively-downgraded model, ready for display.
type degradedRow struct {
	model    string
	fallback string // smaller model requests are routed to ("" if unknown)
	strikes  int
	resetIn  time.Duration // until the degradation lifts (<=0 if already past)
}

type model struct {
	rows           []degradedRow
	currentAccount string
	fetchErr       string // non-empty if the models fetch failed
	cursor         int    // index into links
	width, height  int
}

// ── styling (dark card; Background set on every text style so near-white text
// never washes out on light terminals — the lipgloss card-background trap) ──

const (
	bgColor    = "#1a1a2e"
	fgColor    = "#e6e6e6"
	dimColor   = "#9a9ab0"
	warnColor  = "#ffd93d"
	modelColor = "#ff8c69"
	linkColor  = "#7aa2f7"
	selColor   = "#1a1a2e"
)

func bg(s lipgloss.Style) lipgloss.Style { return s.Background(lipgloss.Color(bgColor)) }

var (
	cardStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(bgColor)).
			Foreground(lipgloss.Color(fgColor)).
			Padding(1, 2)
	titleStyle   = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(warnColor)).Bold(true))
	dimStyle     = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(dimColor)))
	bodyStyle    = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor)))
	modelStyle   = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(modelColor)).Bold(true))
	linkStyle    = bg(lipgloss.NewStyle().Foreground(lipgloss.Color(linkColor)))
	linkSelStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(linkColor)).
			Foreground(lipgloss.Color(selColor)).
			Bold(true)
)

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(links)-1 {
				m.cursor++
			}
		case "enter", " ":
			openURL(links[m.cursor].url)
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("⚠ Anthropic overload — models downgraded") + "\n")
	if m.currentAccount != "" {
		b.WriteString(dimStyle.Render("account: "+m.currentAccount) + "\n")
	}
	b.WriteString("\n")

	switch {
	case m.fetchErr != "":
		b.WriteString(bg(lipgloss.NewStyle().Foreground(lipgloss.Color(modelColor))).
			Render("could not reach the teamclaude proxy:") + "\n")
		b.WriteString(dimStyle.Render("  "+m.fetchErr) + "\n")
	case len(m.rows) == 0:
		b.WriteString(bg(lipgloss.NewStyle().Foreground(lipgloss.Color("#6bcb77"))).
			Render("All models healthy — no downgrades active.") + "\n")
	default:
		b.WriteString(dimStyle.Render("The proxy is routing these around the overload:") + "\n")
		for _, r := range m.rows {
			line := "  " + modelStyle.Render(short(r.model))
			if r.fallback != "" {
				line += bodyStyle.Render("  →  ") + modelStyle.Render(short(r.fallback))
			}
			meta := fmt.Sprintf("   strikes %d", r.strikes)
			if r.resetIn > 0 {
				meta += "   resets in " + fmtDur(r.resetIn)
			}
			line += dimStyle.Render(meta)
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Check provider status:") + "\n")
	for i, l := range links {
		label := fmt.Sprintf(" %s — %s ", l.label, l.url)
		if i == m.cursor {
			b.WriteString("  " + linkSelStyle.Render("▸"+label) + "\n")
		} else {
			b.WriteString("  " + linkStyle.Render(" "+label) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ move · Enter open · Esc close"))

	return cardStyle.Render(b.String())
}

// short trims the "claude-" prefix for a more compact model label.
func short(m string) string { return strings.TrimPrefix(m, "claude-") }

// fmtDur renders a coarse "in N" style countdown: "45s", "4m", "2h".
func fmtDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// openURL opens a URL in the user's default browser, cross-platform. Best
// effort: a failure to launch is silent (the popup is closing anyway).
func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// buildRows turns the proxy's degraded map into sorted, display-ready rows for
// the models that are actively being downgraded right now.
func buildRows(models teamclaude.Models, now time.Time) []degradedRow {
	nowMs := now.UnixMilli()
	var rows []degradedRow
	for name, d := range models {
		if d.Until <= nowMs {
			continue // strikes building but not actively degraded
		}
		rows = append(rows, degradedRow{
			model:    name,
			fallback: teamclaude.FallbackMap[name],
			strikes:  d.Strikes,
			resetIn:  time.Duration(d.Until-nowMs) * time.Millisecond,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].model < rows[j].model })
	return rows
}

// Run is the `tabby render degraded-models-popup` entry point.
func Run(args []string) int {
	fs := flag.NewFlagSet("degraded-models-popup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	_ = fs.String("session", "", "tmux session ID (accepted for symmetry; unused)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "degraded-models-popup: load config:", err)
		return 1
	}
	tc := cfg.Widgets.TeamClaude
	if tc.URL == "" {
		fmt.Fprintln(os.Stderr, "degraded-models-popup: teamclaude widget has no url configured")
		return 1
	}
	apiKey := tc.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("TABBY_TEAMCLAUDE_API_KEY")
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	m := model{}
	models, mErr := teamclaude.FetchModels(ctx, tc.URL, apiKey)
	if mErr != nil {
		m.fetchErr = mErr.Error()
	} else {
		m.rows = buildRows(models, time.Now())
	}
	// Status is best-effort context only; ignore its error.
	if st, sErr := teamclaude.Fetch(ctx, tc.URL, apiKey); sErr == nil && st != nil {
		m.currentAccount = st.CurrentAccount
	}

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
		fmt.Fprintf(os.Stderr, "degraded-models-popup: %v\n", runErr)
		return 1
	}
	return 0
}
