package renderer

import (
	"os"
	"time"
)

// terminalResetSequence disables every mouse tracking mode we know of, exits
// the bubbletea alternate screen, and turns off bracketed paste. Written to
// stdout directly (not Printf'd) so it runs before shells reclaim the TTY.
const terminalResetSequence = "\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1005l\033[?1006l\033[?1015l" +
	"\033[?1049l" +
	"\033[?2004l" +
	"\033[0m\033[?25h"

// ResetTerminal writes the disable-everything escape sequences to stdout.
// Renderers call this during graceful shutdown and also from signal handlers.
// Safe to call repeatedly.
//
// The write is deadline-capped: a pane whose pty buffer is full (tmux stops
// draining it, e.g. a backgrounded window) blocks a stdout write forever, and
// a renderer parked in this write is the "dead sidebar that ignores SIGTERM"
// — the signal handler calls ResetTerminal before anything that actually
// stops the process. Two hundred ms is generous for a 60-byte escape write.
func ResetTerminal() {
	f := os.Stdout
	f.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	defer f.SetWriteDeadline(time.Time{})
	f.WriteString(terminalResetSequence)
}
