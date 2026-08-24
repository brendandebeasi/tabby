package hook

import (
	"os"
	"os/exec"
	"strings"
)

// currentPaneID returns the pane a hook invocation should act on.
//
// TMUX_PANE is normally the right answer: when a hook runs from inside a pane's
// own shell (the OSC handler, a user-invoked `tabby hook ...`), it names that
// pane exactly. But tmux copies TMUX_PANE into a *server's* global environment
// when the server itself was started from inside another tmux, and every
// `run-shell` dispatched from a key binding then inherits that stale value —
// a pane id belonging to a long-dead server. Hooks bound to keys (C-b x, C-b n)
// would target a pane that does not exist, and silently do nothing.
//
// So trust TMUX_PANE only when it names a live pane, and otherwise fall back to
// asking tmux for the pane the binding actually fired in.
func currentPaneID() string {
	return resolvePaneID(os.Getenv("TMUX_PANE"), paneExists, activePaneID)
}

// resolvePaneID is the pure core of currentPaneID, split out for testing.
func resolvePaneID(envPane string, exists func(string) bool, active func() string) string {
	envPane = strings.TrimSpace(envPane)
	if envPane != "" && exists(envPane) {
		return envPane
	}
	if p := strings.TrimSpace(active()); p != "" {
		return p
	}
	// Nothing better to offer: hand back whatever we were given so callers
	// keep their previous behaviour rather than sending an empty target.
	return envPane
}

// paneExists reports whether pane names a pane on the current tmux server.
// display-message -t exits non-zero (and prints nothing) for an unknown id.
func paneExists(pane string) bool {
	out, err := exec.Command("tmux", "display-message", "-t", pane, "-p", "#{pane_id}").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// activePaneID asks tmux for the pane of the client that dispatched this hook.
func activePaneID() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
