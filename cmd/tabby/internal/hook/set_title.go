package hook

import (
	"os"
	"os/exec"
	"strings"

	"github.com/brendandebeasi/tabby/pkg/config"
)

// doSetTitle sets a display-only AI summary as the window's tab title.
// Called by AI agent hooks (Claude Code, OpenCode) to surface a short
// description of what the agent is currently doing.
//
// Usage: tabby hook set-title <text...>
//   - Pass an empty string (or no args) to clear.
//   - Text is truncated to config.AI.TabSummary.MaxWords (default 3, 0=no truncation).
//   - Stored on the window as @tabby_ai_title and read by the renderer.
//   - Display-only: does NOT rename the window — clears cleanly when unset.
func doSetTitle(args []string) {
	text := strings.TrimSpace(strings.Join(args, " "))

	// Resolve target window. Reuse the same logic as set-indicator so AI hook
	// scripts can find the right window even when invoked from subprocesses.
	stateDir := "/tmp/tabby-state"
	os.MkdirAll(stateDir, 0755)
	sessionOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	session := strings.TrimSpace(string(sessionOut))
	win := resolveIndicatorWindow("input", "1", session, stateDir)
	if win == "" {
		return
	}
	winTarget := ":" + win

	if text == "" {
		tmuxUnsetWindowOpt(winTarget, "@tabby_ai_title")
		signalDaemon("USR1")
		return
	}

	maxWords := 3
	if cfg, err := config.LoadConfig(config.DefaultConfigPath()); err == nil && cfg != nil {
		maxWords = cfg.AI.TabSummary.MaxWords
	}
	tmuxSetWindowOpt(winTarget, "@tabby_ai_title", truncateWords(text, maxWords))
	signalDaemon("USR1")
}

// truncateWords keeps the first n whitespace-delimited words. n <= 0 returns
// the input unchanged (after whitespace collapse). Control chars are stripped.
func truncateWords(s string, n int) string {
	clean := stripControl(s)
	fields := strings.Fields(clean)
	if n > 0 && len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}

func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
			if r == '\t' || r == '\n' || r == '\r' {
				b.WriteRune(' ')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
