// Package navtrace records the end-to-end lifecycle of a keyboard next/prev-window
// request so a DROPPED request is visible in one place. tabby's window
// navigation crosses two processes — the short-lived `tabby hook` invoked by the
// tmux key binding, then the daemon over a unix socket — and a request can be
// lost at several points that were previously silent or ambiguous (the hook's
// socket write failing while the daemon restarts, the loop's input queue
// dropping a full event, or the daemon receiving it but declining to switch).
//
// Every stage writes one correlatable line here, keyed by a navid minted in the
// hook, so:
//
//	tail -f /tmp/tabby-nav-trace.log
//
// shows HOOK_SENT -> RECV -> outcome for each keypress. A HOOK_SENT with no
// matching RECV was dropped in transit; a RECV whose outcome is SUPPRESSED or
// NOOP was received but produced no switch.
package navtrace

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Path is the shared, session-agnostic trace file. Session-agnostic on purpose:
// the hook doesn't need to resolve a tmux session id to write, and every window
// nav across every session converges on one tailable file (the navid + tty in
// each line disambiguate origin).
const Path = "/tmp/tabby-nav-trace.log"

var mu sync.Mutex

// Write appends one timestamped line to Path. Best-effort by design: tracing
// must never change navigation behaviour, so every error is swallowed. Safe for
// concurrent callers — mu serializes goroutines within a process, and each write
// is a single short O_APPEND (atomic across processes for line-sized payloads).
func Write(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()

	f, err := os.OpenFile(Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006/01/02 15:04:05.000000"), fmt.Sprintf(format, args...))
}
