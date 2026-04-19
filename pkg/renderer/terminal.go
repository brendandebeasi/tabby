package renderer

import "os"

// terminalResetSequence disables every mouse tracking mode we know of, exits
// the bubbletea alternate screen, and turns off bracketed paste. Written to
// stdout directly (not Printf'd) so it runs before shells reclaim the TTY.
const terminalResetSequence = "\033[?1000l\033[?1002l\033[?1003l\033[?1004l\033[?1005l\033[?1006l\033[?1015l" +
	"\033[?1049l" +
	"\033[?2004l"

// ResetTerminal writes the disable-everything escape sequences to stdout.
// Renderers call this during graceful shutdown and also from signal handlers.
// Safe to call repeatedly.
func ResetTerminal() {
	os.Stdout.WriteString(terminalResetSequence)
}
