// terminal.go — terminal reset helpers for renderer shutdown paths.
package renderer

import "io"

// terminalResetSequence disables every mouse tracking mode we know of, exits
// the bubbletea alternate screen, turns off bracketed paste, resets
// attributes, and shows the cursor. Written through StdoutSink so a wedged
// pty can never park the caller.
const terminalResetSequence = "\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1005l\033[?1006l\033[?1015l" +
	"\033[?1049l" +
	"\033[?2004l" +
	"\033[0m\033[?25h"

// ResetTerminal writes the disable-everything escape sequences. Renderers
// call this during graceful shutdown and from signal handlers. Safe to call
// repeatedly, and never blocks: the bytes queue on StdoutSink like any other
// frame and are dropped rather than parked on if the pty is full.
func ResetTerminal() {
	io.WriteString(StdoutSink, terminalResetSequence)
}
