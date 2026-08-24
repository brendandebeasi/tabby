// Package tmuxhooks is the single source of truth for the global tmux hooks
// tabby registers at runtime.
//
// tabby.tmux installs a baseline set at plugin load. The watchdog and `tabby
// toggle` re-register when a daemon starts, because a session created after
// plugin load (most easily a grouped clone) never ran the config. `set-hook -g`
// replaces rather than merges, so every definition here silently overwrites the
// config's version of the same hook name — the two must stay in sync, and a
// hook name tabby.tmux deliberately leaves unset must never appear here.
//
// Both call sites previously kept their own copy of this table and had drifted
// from the config: the bodies dropped `refresh-client -S` after a window switch
// and the daemon/geometry steps on attach, and registered after-rename-window,
// which tabby.tmux documents as forbidden.
package tmuxhooks

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Definition is one global tmux hook: the hook name and the command body that
// `set-hook -g` binds to it.
type Definition struct {
	Name string
	Cmd  string
}

// okGuard terminates every hook body. tmux prints "'<cmd>' returned N" into
// every attached client when a run-shell body exits nonzero, so one failing
// step — most often refresh-client firing with no current client — becomes a
// screenful of noise on every window switch. Hook bodies are best-effort
// housekeeping and nothing consumes their exit status.
const okGuard = "; true"

// bg wraps one command body in a backgrounded run-shell that always reports
// success to tmux.
func bg(cmd string) string {
	return fmt.Sprintf("run-shell -b '%s%s'", cmd, okGuard)
}

// bgQuoted is bg for a body that itself contains single quotes, which tmux's
// own single-quoted string cannot hold. tmux does no shell-variable expansion,
// so double-quoting the body at the tmux level is equivalent for everything
// except the quote character.
//
// A body needs this when it passes a session id. tmux expands #{session_id} to
// text like `$246`, and the shell that runs the body then reads that as a
// positional parameter and substitutes it away — to the empty string, or for
// session $0 to the shell's own name. Double quotes do not stop it; only
// single quotes at the shell level do. `#{window_id}`, `#{pane_id}` and
// `#{client_tty}` expand to `@N`, `%N` and a path, none of which the shell
// touches, so those stay in the cheaper bg form.
func bgQuoted(cmd string) string {
	return fmt.Sprintf("run-shell -b \"%s%s\"", cmd, okGuard)
}

// Retired lists hook names earlier versions registered and that must now be
// actively unset. A global hook outlives the binary that set it — it survives
// daemon restarts and plugin reloads for the life of the tmux server — so
// dropping a name from Definitions only stops new registrations; clearing it
// from a server already running takes an explicit `set-hook -gu`.
func Retired() []string {
	return []string{"after-rename-window"}
}

// Definitions returns the hooks to register for the given tabby executable.
// exe is an absolute path to the consolidated binary, e.g.
// <plugin>/bin/tabby; sibling scripts resolve relative to it.
//
// Deliberately absent: after-rename-window. The daemon renames windows itself
// while refreshing tab titles, so binding that hook feeds each rename straight
// back into another refresh — see the note at tabby.tmux's `bind-key r`, which
// sets @tabby_name_locked in the user-facing rename paths instead precisely so
// this hook is not needed.
func Definitions(exe string) []Definition {
	hookCmd := fmt.Sprintf("%s hook", exe)
	cycleCmd := fmt.Sprintf("%s cycle-pane", exe)
	ensureDaemon := filepath.Join(filepath.Dir(filepath.Dir(exe)), "scripts", "ensure-daemon.sh")

	ensureSidebar := fmt.Sprintf("%s ensure-sidebar '#{session_id}' '#{window_id}'", hookCmd)
	ensureContent := fmt.Sprintf("%s --ensure-content", cycleCmd)

	join := func(parts ...string) string { return strings.Join(parts, "; ") }

	return []Definition{
		{"after-resize-pane", bg(fmt.Sprintf("%s on-pane-resize \"#{hook_pane}\"", hookCmd))},
		{"after-resize-window", bg(fmt.Sprintf("%s on-pane-resize \"#{pane_id}\"", hookCmd))},
		{"client-resized", join(
			bg(fmt.Sprintf("%s client-resized \"#{client_tty}\" \"#{client_width}\" \"#{client_height}\"", hookCmd)),
			bgQuoted(ensureSidebar),
		)},
		// refresh-client -S is what repaints the client after a window switch.
		// Without it the client keeps serving the previous window's layout, so
		// mouse coordinates map to stale pane boundaries and clicks land on the
		// wrong pane or are dropped entirely.
		{"after-select-window", join(
			bg(fmt.Sprintf("%s after-select-window \"#{window_id}\"; tmux refresh-client -S 2>/dev/null", hookCmd)),
			bgQuoted(ensureSidebar),
			bg(ensureContent),
		)},
		// A reattaching client can land on a session whose daemon has already
		// idle-quit, so ensure-daemon runs before the steps that need one.
		{"client-attached", join(
			bg(fmt.Sprintf("%s \"\" \"\" \"#{client_tty}\"", ensureDaemon)),
			bg(fmt.Sprintf("%s client-attached", hookCmd)),
			bgQuoted(ensureSidebar),
			bgQuoted(fmt.Sprintf("%s stabilize-client-resize '#{session_id}' '#{window_id}' '#{client_tty}' '#{client_width}' '#{client_height}'", hookCmd)),
			bg(ensureContent),
		)},
	}
}
