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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Definition is one global tmux hook: the hook name and the command body that
// `set-hook -g` binds to it.
type Definition struct {
	Name string
	Cmd  string
}

// Install unsets every Retired hook and registers every Definition, in
// parallel. This is the only way these hooks reach tmux: `tabby toggle` and the
// watchdog call it when a daemon starts, and tabby.tmux shells out to
// `tabby install-hooks` at plugin load rather than repeating the bodies in
// shell. They used to be written twice — once here and once in the config — and
// `set-hook -g` replaces rather than merges, so the daemon's copy silently won
// and the config's diverged unnoticed.
//
// exe is an absolute path to the tabby binary; see Definitions.
func Install(exe string) {
	// Any daemon that died holding the gate closed is unstuck here, before the
	// hooks that gate reads are (re)registered.
	ClearMute()

	var wg sync.WaitGroup
	for _, name := range Retired() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exec.Command("tmux", "set-hook", "-gu", name).Run()
		}()
	}
	wg.Wait()
	for _, h := range Definitions(exe) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exec.Command("tmux", "set-hook", "-g", h.Name, h.Cmd).Run()
		}()
	}
	wg.Wait()
}

// Run implements the `tabby install-hooks` subcommand, which is how tabby.tmux
// registers these at plugin load.
// Symlinks are deliberately not resolved: the hook bodies must keep naming the
// binary by the path tabby was invoked through, because sibling scripts resolve
// against it (see Definitions) and the plugin directory is commonly a symlink
// to a checkout elsewhere.
func Run(_ []string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "install-hooks: %v\n", err)
		return 1
	}
	Install(exe)
	return 0
}

// okGuard terminates every hook body. Hook bodies are best-effort housekeeping
// and nothing consumes their exit status, so a failing step — most often
// refresh-client firing with no current client — must not surface.
const okGuard = "; true"

// muteGate wraps a hook body so it is skipped while the daemon is mutating tmux
// itself. The condition is an -F format, evaluated inside the server, so a muted
// hook spawns no socketpair and forks no shell — the saving has to happen here,
// because by the time a script could check the flag tmux has already forked it.
//
// The gate is SESSION-SCOPED, and that is the whole design. tmux options are
// per-server but daemons are per-session, so a plain boolean flag silenced every
// daemon in the group, not just the one doing the mutating: the peers never
// heard about the window-list changes and their models went stale (observed as
// minimize leaving state mixed across windows). So the muting daemon writes its
// OWN session id into the option, and a hook is dropped only where it fired in
// that same session — which is exactly the daemon that caused it and already
// knows. hook-notify.sh signals the daemon of #{session_id}, so every peer's
// notification still gets through.
//
// Both no-daemon values read as "run": unset expands to empty and ClearMute
// writes "0", neither of which can equal a #{session_id} (always `$N`). The
// comparison fails open in every direction — an unknown or stale id in the
// option mutes nothing.
const muteGate = "#{?#{==:#{" + MuteOption + "},#{session_id}},0,1}"

// MuteOption is the server option the daemon closes around its own tmux
// mutations, holding its own session id for the duration. Exported so the
// daemon and watchdog name it from one place.
const MuteOption = "@tabby_mute"

// clearedMuteValue is what an open gate reads. It is written rather than
// unsetting the option so the gate stays a plain string comparison, and it must
// never look like a tmux session id (`$N`) or it would mute that session for
// good. ClearedMuteValue is the exported spelling for the daemon.
const clearedMuteValue = "0"

// ClearedMuteValue is clearedMuteValue for callers outside this package.
const ClearedMuteValue = clearedMuteValue

// daemonGate is muteGate plus a test for a daemon actually existing in the
// session the hook fired in. It is for hooks whose every step is a message to a
// daemon: with none running, the fire does nothing but fork a shell to fail at
// reading a pid file that is not there.
//
// That is not a rounding error in a grouped set. Window and pane hooks fire once
// per session a window is linked into, and only the sessions someone actually
// runs tabby in have a daemon; the rest are along for the ride because they
// share the window list. Measured on this 9-session group, one new-window drove
// ~400 window-linked fires and eight ninths of them were sessions with no daemon
// to signal.
//
// It gates only hooks whose trigger is a WINDOW- or PANE-level change, because
// those are shared by the whole group: the daemon's own session gets its own
// fire of the same event, so dropping the peers' copies loses no information.
// Client- and session-level hooks (client-attached, client-resized,
// client-session-changed, session-created) fire in one session only, which may
// well be a daemonless peer whose request a peer daemon services via the
// fallback in dialDaemon — and two of them exist to START a daemon, so gating
// them on one would be a deadlock. Those keep the plain muteGate.
//
// DaemonOption is set on the daemon's own session, not the server, for the same
// reason muteGate carries a session id: a server-wide flag would say "some
// daemon is running somewhere" and open the gate for all nine sessions again.
//
// Every failure mode is fail-open or self-correcting. A daemon killed without
// unsetting the option leaves the gate open, which is exactly today's behaviour.
// A daemon still starting has not set it yet, so a few fires are skipped — and
// the daemon reconciles on its own tick regardless, which is what makes every
// step behind this gate best-effort in the first place.
const daemonGate = "#{?#{&&:#{!=:#{" + DaemonOption + "},},#{!=:#{" + MuteOption + "},#{session_id}}},1,0}"

// DaemonOption is the session option a running daemon sets on its own session
// for the life of the process. Exported so the daemon names it from one place.
const DaemonOption = "@tabby_daemon"

// MarkDaemonPresent opens daemonGate for sessionID. Called once at daemon
// startup; the value is unread, only its emptiness matters.
func MarkDaemonPresent(sessionID string) {
	exec.Command("tmux", "set-option", "-t", sessionID, DaemonOption, "1").Run()
}

// ClearDaemonPresent closes daemonGate for sessionID, on the daemon's way out.
// Best-effort: a daemon that dies without reaching this leaves the gate open,
// which costs the pointless fires this gate saves and nothing else.
func ClearDaemonPresent(sessionID string) {
	exec.Command("tmux", "set-option", "-t", sessionID, "-u", DaemonOption).Run()
}

// ClearMute forces the hook gate open. The option lives on the server, so a
// daemon killed mid-batch leaves it set and every tabby hook stays silent for
// the life of the server — tabby looks frozen and nothing in the logs says why.
// Install calls this, which covers plugin load, `tabby toggle` and every
// watchdog restart; the daemon also calls it at startup.
func ClearMute() {
	exec.Command("tmux", "set-option", "-g", MuteOption, clearedMuteValue).Run()
}

// ungatedJob is job() without the mute gate, for hooks whose suppression the
// user can see. The gate exists to stop the window-list churn a daemon batch
// replays to every session in a grouped set; a selection fires one hook in one
// session and is not part of that. Gating them anyway cost a visible bug: the
// window you clicked drew the previous window's contents inside its own border,
// because the daemon does window-list work as part of servicing the click, and
// the refresh-client -S below was collateral damage of the gate that work closed.
func ungatedJob(steps ...string) string {
	return fmt.Sprintf("if-shell -b \"%s%s\" \"\"", strings.Join(steps, "; "), okGuard)
}

// job wraps a hook's steps in a single backgrounded tmux job that runs them in
// order and reports nothing.
//
// Two properties matter, and both are why this is one job rather than several.
//
// if-shell, not run-shell: tmux prints "'<cmd>' returned N" into every attached
// client when a *run-shell* body exits nonzero. if-shell runs the same job and
// silently takes its (empty) else branch instead. Neither form can suppress
// tmux's own "failed to run command" — that is raised by job_run() before any
// job exists, when socketpair() or fork() fails.
//
// One job, not several: every backgrounded body in a hook is a separate tmux
// job, and each job costs a socketpair in the server plus a forked shell. Hooks
// fire once per session a window is linked into, so in an 8-session grouped set
// a three-body after-select-window is 24 socketpairs and 24 forks per window
// switch. Against the server's default soft RLIMIT_NOFILE of 256 that is enough
// to exhaust it, at which point socketpair() fails and tmux drops the command.
// Chaining the steps into one body makes it 8, and gives them a real execution
// order — five concurrent jobs have none.
//
// The body is double-quoted at the tmux level so it can contain shell-level
// single quotes. tmux does no shell-variable expansion, so the two quoting
// styles are equivalent to tmux for everything except the quote character, and
// a step passing #{session_id} has no choice: tmux expands it to text like
// `$246`, which the shell running the body would otherwise read as a positional
// parameter and substitute away — to the empty string, or for session $0 to the
// shell's own name. Only single quotes at the shell level stop that.
func job(steps ...string) string {
	body := fmt.Sprintf("if-shell -b \"%s%s\" \"\"", strings.Join(steps, "; "), okGuard)
	// No -b on the gate: -F evaluates a format inside the server, so there is no
	// shell to background and -b would only defer the body by a queue turn. The
	// body's own if-shell -b is what keeps the work off the server's critical
	// path; deferring the gate as well would reorder steps that are meant to run
	// before it (see after-kill-pane in tabby.tmux).
	return fmt.Sprintf("if-shell -F '%s' '%s' ''", muteGate, escapeSingle(body))
}

// daemonJob is job() behind daemonGate instead of muteGate, for hooks whose
// every step is a message to a daemon. See daemonGate for which hooks qualify.
func daemonJob(steps ...string) string {
	body := fmt.Sprintf("if-shell -b \"%s%s\" \"\"", strings.Join(steps, "; "), okGuard)
	return fmt.Sprintf("if-shell -F '%s' '%s' ''", daemonGate, escapeSingle(body))
}

// escapeSingle makes body safe to embed in the single-quoted branch of the
// outer if-shell. tmux uses shell-style quoting, so a literal single quote must
// leave and re-enter the quoted run.
func escapeSingle(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// Retired lists hook names earlier versions registered and that must now be
// actively unset. A global hook outlives the binary that set it — it survives
// daemon restarts and plugin reloads for the life of the tmux server — so
// dropping a name from Definitions only stops new registrations; clearing it
// from a server already running takes an explicit `set-hook -gu`.
//
// client-active and client-focus-in fire on focus shifts that change no
// geometry; client-resized already covers every real size change. They were
// dropped here but kept being re-registered by tabby.tmux, so each focus change
// still paid three jobs per session.
func Retired() []string {
	return []string{"after-rename-window", "client-active", "client-focus-in"}
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
	ensureDaemon := filepath.Join(filepath.Dir(filepath.Dir(exe)), "scripts", "ensure-daemon.sh")

	// batch runs several tabby subcommands in one process. Each step here is a
	// subcommand and its arguments, without the binary path — see runBatch in
	// cmd/tabby/main.go. A hook that ran three tabby commands cost three
	// fork/execs on every fire, and hooks fire once per session a window is
	// linked into; one exec does the same work.
	batch := func(steps ...string) string {
		return fmt.Sprintf("%s batch -- %s", exe, strings.Join(steps, " -- "))
	}

	// Formats are single-quoted uniformly. Only #{session_id} strictly needs it
	// (see job), but a merged body is only safe to reason about if every step
	// quotes the same way, and no path or format here contains a single quote.
	ensureSidebar := "hook ensure-sidebar '#{session_id}' '#{window_id}'"
	ensureContent := "cycle-pane --ensure-content"

	return []Definition{
		// Pane geometry is a property of the window, so every session the window
		// is linked into fires these for one resize. daemonJob keeps the fire in
		// the daemon's own session and drops the daemonless peers' copies of the
		// same event.
		{"after-resize-pane", daemonJob(fmt.Sprintf("%s hook on-pane-resize '#{hook_pane}'", exe))},
		{"after-resize-window", daemonJob(fmt.Sprintf("%s hook on-pane-resize '#{pane_id}'", exe))},
		// Not daemonJob: a client resize fires only in the client's own session,
		// which may be a daemonless peer whose request dialDaemon hands to a peer
		// daemon. There is no second fire to fall back on.
		{"client-resized", job(batch(
			"hook client-resized '#{client_tty}' '#{client_width}' '#{client_height}'",
			ensureSidebar,
		))},
		// refresh-client -S is what repaints the client after a window switch.
		// Without it the client keeps serving the previous window's layout, so
		// mouse coordinates map to stale pane boundaries and clicks land on the
		// wrong pane or are dropped entirely.
		{"after-select-window", ungatedJob(
			batch(
				"hook after-select-window '#{window_id}'",
				ensureSidebar,
				ensureContent,
			),
			"tmux refresh-client -S 2>/dev/null",
		)},
		// A reattaching client can land on a session whose daemon has already
		// idle-quit, so ensure-daemon runs before the steps that need one. That
		// ordering is only real because these share one job: as separate
		// backgrounded bodies they raced.
		{"client-attached", job(
			fmt.Sprintf("%s '' '' '#{client_tty}'", ensureDaemon),
			batch(
				"hook client-attached",
				ensureSidebar,
				"hook stabilize-client-resize '#{session_id}' '#{window_id}' '#{client_tty}' '#{client_width}' '#{client_height}'",
				ensureContent,
			),
		)},
	}
}
