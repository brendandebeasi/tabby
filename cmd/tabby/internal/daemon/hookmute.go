package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/tmuxhooks"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// Hook muting: stop the daemon's own tmux mutations from firing tabby's hooks
// back at the daemon.
//
// Every window-moving tmux command fires window-linked and window-unlinked, and
// in a grouped session set tmux fires them ONCE PER SESSION in the group. Each
// fire is a tmux job — a socketpair() plus a forked shell — that signals this
// daemon, which reconciles and may emit another move batch. Measured on a live
// 9-session group, one `new-window` produced ~400 window-linked fires, 280 of
// them the same (session, window) pair: the daemon reacting to itself. That
// burst is what exhausts the server's file descriptors (soft RLIMIT_NOFILE is
// 256 by default on macOS), and fd exhaustion is what makes tmux print
// "'<body>' returned 1" — the forked child dies before it can exec the body.
//
// The gate is the @tabby_mute server option, holding the muting daemon's own
// session id and checked by every hook body as an -F format comparing it against
// #{session_id} (see muteGate in internal/tmuxhooks). A false -F condition is
// evaluated as a format inside the server: tmux spawns NOTHING, so a muted hook
// costs no socketpair and no fork. That is the whole point — suppressing the
// work inside the script would be too late, because the fork has already
// happened by then.
//
// Scoping it to the session is what makes the gate safe in a group. The option
// is server-wide, so a boolean muted every daemon at once and left the peers'
// window-list models stale. Carrying the id means a fire is dropped only in the
// session whose own daemon caused it; hook-notify.sh signals the daemon of
// #{session_id}, so every peer's notification still goes through.
//
// Muting is engaged lazily on the first mutating command and released by a
// debounce timer, so a batch of N moves costs one set-option in and one out
// rather than 2N. The debounce is deliberately short: while muted, a real user
// event in THIS session is suppressed too, and the daemon only learns about it
// on its next reconcile.
//
// Concurrent daemons still share the one option, so the last writer owns the
// gate and a peer engaging its own mute lifts this one early. That direction is
// safe — it costs unsuppressed fires, never a silenced daemon — and the next
// mutation re-engages. A daemon can never end up muted by someone else's id.

const (
	// hookMuteRelease is how long after the last mutating command the gate
	// reopens. Long enough to cover a move batch's settling, short enough
	// that a user event landing inside the window is only briefly invisible.
	hookMuteRelease = 150 * time.Millisecond

	// hookMuteCmdTimeout bounds the set-option calls themselves. These run on
	// the reconcile path, so they must never block it.
	hookMuteCmdTimeout = 2 * time.Second
)

// hookMuteState tracks whether this process currently holds the gate closed.
var hookMuteState struct {
	sync.Mutex
	engaged bool
	timer   *time.Timer
}

// mutatingTmuxVerbs are the tmux commands that can fire one of the hooks tabby
// installs. Reads (display-message, list-windows, show-options) are absent on
// purpose: gating them would widen the mute window for no benefit.
//
// set-option is deliberately absent too — it fires none of tabby's hooks, and
// including it would make engaging the mute re-enter this path.
//
// The navigation verbs — select-window, select-pane, next-window,
// previous-window, switch-client — are absent for a reason worth keeping. They
// were in this map once, and the result was that clicking a sidebar tab drew the
// previous window's contents inside the new window's border. The path is:
// clicking a tab asks the daemon to select-window, the daemon selecting a window
// closed the gate, and the after-select-window hook the selection then fired —
// the one whose whole job is `refresh-client -S` — was muted. The client went on
// serving the layout it already had.
//
// They also buy nothing. The amplification this gate exists to stop comes from
// the window-list churn (link, unlink, move, kill, new), which tmux replays to
// every session in a grouped set. Selecting fires one hook in one session, which
// is both cheap and exactly what the user is waiting to see happen.
var mutatingTmuxVerbs = map[string]bool{
	"break-pane":     true,
	"join-pane":      true,
	"kill-pane":      true,
	"kill-session":   true,
	"kill-window":    true,
	"link-window":    true,
	"move-window":    true,
	"new-session":    true,
	"new-window":     true,
	"resize-pane":    true,
	"resize-window":  true,
	"respawn-pane":   true,
	"respawn-window": true,
	"select-layout":  true,
	"split-window":   true,
	"swap-pane":      true,
	"swap-window":    true,
	"unlink-window":  true,
}

// tmuxGlobalFlagsWithValue are the tmux(1) server flags that take a separate
// argument. Their value is not the verb, so it has to be skipped along with the
// flag — otherwise `tmux -L sock kill-window` reads `sock` as the command.
var tmuxGlobalFlagsWithValue = map[string]bool{
	"-L": true, // socket name
	"-S": true, // socket path
	"-f": true, // config file
	"-c": true, // shell-command
	"-T": true, // terminal features
}

// isMutatingTmuxCommand reports whether args names a tmux command that can fire
// one of tabby's hooks. Leading global flags (tmux -L socket kill-window) are
// skipped so the verb is found wherever it sits.
func isMutatingTmuxCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" {
			continue
		}
		if a[0] == '-' {
			if tmuxGlobalFlagsWithValue[a] {
				i++ // its value is not the verb
			}
			continue
		}
		return mutatingTmuxVerbs[a]
	}
	return false
}

// noteTmuxMutation closes the hook gate if args is a mutating command, and
// (re)arms the timer that reopens it. Safe to call on every tmux invocation:
// non-mutating commands return immediately.
//
// The gate names this daemon's session rather than being a boolean, because
// @tabby_mute is a server option while daemons are per-session. A boolean here
// muted the hooks every OTHER daemon in the group was listening on, so peers
// never learned about the window-list changes this one had just made and their
// models went stale — observed as minimize leaving state mixed across windows.
// Writing the session id makes the suppression land only where the fire is this
// daemon's own echo; see muteGate in internal/tmuxhooks.
//
// A daemon with no resolvable session id cannot scope the gate, so it does not
// engage one — an unmuted burst is a cost, an incorrectly muted peer is a bug.
func noteTmuxMutation(args []string) {
	if !isMutatingTmuxCommand(args) {
		return
	}
	if daemonSessionID() == "" {
		return
	}
	hookMuteState.Lock()
	defer hookMuteState.Unlock()

	if !hookMuteState.engaged {
		setHookMute(true)
		hookMuteState.engaged = true
	}
	if hookMuteState.timer != nil {
		hookMuteState.timer.Stop()
	}
	hookMuteState.timer = time.AfterFunc(hookMuteRelease, releaseHookMute)
}

// releaseHookMute reopens the gate. It runs from the debounce timer.
func releaseHookMute() {
	hookMuteState.Lock()
	defer hookMuteState.Unlock()
	if !hookMuteState.engaged {
		return
	}
	setHookMute(false)
	hookMuteState.engaged = false
}

// ClearHookMute forces the gate open regardless of local state. A daemon that
// died mid-batch leaves @tabby_mute set, which would silence every hook on the
// server, so this is called at daemon startup and by the watchdog when no
// daemon is alive.
func ClearHookMute() {
	hookMuteState.Lock()
	defer hookMuteState.Unlock()
	if hookMuteState.timer != nil {
		hookMuteState.timer.Stop()
		hookMuteState.timer = nil
	}
	hookMuteState.engaged = false
	setHookMute(false)
}

// hookMuteValue is the option value for a closed (on) or open gate. Split out
// from setHookMute so the scoping can be tested without a tmux server.
func hookMuteValue(on bool) string {
	if on {
		return daemonSessionID()
	}
	return tmuxhooks.ClearedMuteValue
}

// setHookMute writes the gate option: this daemon's session id to close the
// gate on its own fires, "0" to reopen it. "0" rather than unsetting keeps the
// option a plain string comparison for the gate format, and no session id can
// ever equal it — tmux session ids are always `$N`.
//
// It execs tmux directly rather than going through tmuxCmd/tmuxRun, which would
// re-enter noteTmuxMutation.
func setHookMute(on bool) {
	v := hookMuteValue(on)
	ctx, cancel := context.WithTimeout(context.Background(), hookMuteCmdTimeout)
	defer cancel()
	if err := tmux.CmdContext(ctx, "set-option", "-g", tmuxhooks.MuteOption, v).Run(); err != nil && coordinatorDebugLog != nil {
		coordinatorDebugLog.Printf("hookmute: set %s=%s failed: %v", tmuxhooks.MuteOption, v, err)
	}
}
