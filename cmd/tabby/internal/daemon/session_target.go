package daemon

import "strings"

// Every tmux query the daemon makes about "the current window" has to name the
// session it is asking about. tmux does not resolve an unqualified target from
// the $TMUX in our environment — it picks the most recently active *client* and
// answers for that client's session. Under grouped sessions (two sessions
// sharing one window set, each with its own current window) that means daemon
// $1 asking `display-message -p '#{window_id}'` routinely gets $2's answer.
//
// The wrong answer is silent, and it is the shape behind a family of focus
// complaints: closing a window moves focus to the wrong neighbor, a manual
// window switch gets overridden a moment later, new-window focus bounces. Each
// one is a daemon acting on another session's idea of what is active.
//
// A stale TMUX_PANE in the tmux global environment (a dead pane id copied into
// every daemon at spawn) makes the mis-resolution certain rather than
// occasional, but it is not the cause: clearing TMUX_PANE does not make tmux
// fall back to $TMUX's session field. Explicit qualification is the only
// reliable answer, so these helpers exist and TestNoUnqualifiedDisplayMessage
// forbids an unqualified `display-message -p` elsewhere in the package.

// sessionTarget renders a tmux `-t` argument that pins a command to sess, e.g.
// "$1:". The trailing colon means "this session, whatever its current window
// is" — without it tmux would read "$1" as a window or pane name. Returns ""
// for an empty session so callers can tell "no session known" from a real
// target.
func sessionTarget(sess string) string {
	sess = strings.TrimSpace(sess)
	if sess == "" {
		return ""
	}
	return sess + ":"
}

// sessionTarget is the Coordinator's own session, as a tmux target.
func (c *Coordinator) sessionTarget() string {
	return sessionTarget(c.sessionID)
}

// displayMessageArgs builds the argv for a `display-message -p` pinned to sess.
// It drops the -t flag entirely rather than passing an empty target, because
// tmux treats an empty -t argument as an error rather than as "unspecified".
func displayMessageArgs(sess, format string) []string {
	if target := sessionTarget(sess); target != "" {
		return []string{"display-message", "-t", target, "-p", format}
	}
	return []string{"display-message", "-p", format}
}

// displayMessageArgs builds a session-pinned display-message argv for callers
// that need to run it themselves (a caller-owned context, say).
func (c *Coordinator) displayMessageArgs(format string) []string {
	return displayMessageArgs(c.sessionID, format)
}

// displayMessageIn evaluates a tmux format string against sess and returns the
// trimmed output. With no session it falls back to an unqualified query, which
// is what tests and the pre-flag startup path get; that is a guess, but a
// daemon with no session has nothing better to offer.
func displayMessageIn(sess, format string) string {
	return tmuxOutputTrimmed(displayMessageArgs(sess, format)...)
}

// activeWindowIDIn returns the window ID active in sess.
func activeWindowIDIn(sess string) string {
	return displayMessageIn(sess, "#{window_id}")
}

// activePaneIDIn returns the pane ID active in sess's current window.
func activePaneIDIn(sess string) string {
	return displayMessageIn(sess, "#{pane_id}")
}

// DisplayMessage evaluates a tmux format string against this daemon's session.
func (c *Coordinator) DisplayMessage(format string) string {
	return displayMessageIn(c.sessionID, format)
}

// ActiveWindowID returns the window ID active in this daemon's own session.
// Prefer this over any bare display-message: see the note at the top of the
// file for what an unqualified read actually returns.
func (c *Coordinator) ActiveWindowID() string {
	return activeWindowIDIn(c.sessionID)
}

// ActivePaneID returns the pane ID active in this daemon's own session.
func (c *Coordinator) ActivePaneID() string {
	return activePaneIDIn(c.sessionID)
}

// discoverSessionID asks tmux which session we belong to. This is the one
// display-message read that cannot be session-qualified — there is no target to
// qualify with until it answers — so every other read in the package goes
// through the helpers above and TestNoUnqualifiedDisplayMessage allows this
// file alone. The answer is only as good as the invoking context: prefer the
// -session flag, which the tmux hooks always pass.
func discoverSessionID() string {
	return tmuxOutputTrimmed("display-message", "-p", "#{session_id}")
}

// SessionID returns this daemon's session id, asking tmux only when the
// -session flag was empty.
func (c *Coordinator) SessionID() string {
	if s := strings.TrimSpace(c.sessionID); s != "" {
		return s
	}
	return discoverSessionID()
}

// listClientsArgs builds a `list-clients` argv scoped to this daemon's session.
// The same reasoning as displayMessageArgs applies: a tmux server hosts several
// sessions, and an unscoped list-clients hands us clients we do not own — whose
// geometry we would then measure and whose active window we would then trust.
// The -t flag is omitted rather than passed empty, which tmux rejects.
func listClientsArgs(format string) []string {
	args := []string{"list-clients"}
	if sess := daemonSessionID(); sess != "" {
		args = append(args, "-t", sess)
	}
	return append(args, "-F", format)
}

// daemonSessionID returns the session this daemon was started for, from the
// -session flag. Used by the call sites that have no Coordinator in scope.
func daemonSessionID() string {
	if sessionID == nil {
		return ""
	}
	return strings.TrimSpace(*sessionID)
}
