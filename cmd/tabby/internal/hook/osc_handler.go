package hook

import (
	"bufio"
	"os"
	"strings"
)

// tabbyOSCPrefix is the raw OSC sequence emitted by emitOSCFallback.
const tabbyOSCPrefix = "\x1b]7700;tabby-indicator;"

// tabbyOSCPrefixDCS is the same sequence after DCS passthrough un-escaping
// (each ESC in the payload is doubled by the sender when inside inner tmux).
const tabbyOSCPrefixDCS = "\x1b\x1b]7700;tabby-indicator;"

// doOSCHandler reads stdin (a tmux pipe-pane output stream) and calls
// doSetIndicator whenever a tabby OSC 7700 indicator sequence is found.
// Runs until stdin is closed, which happens when the tmux pane exits.
func doOSCHandler() {
	r := bufio.NewReaderSize(os.Stdin, 65536)
	var window []byte

	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		window = append(window, b)

		// Keep the sliding window bounded; retain enough tail to avoid
		// splitting a partial sequence across a trim boundary.
		if len(window) > 4096 {
			window = window[len(window)-512:]
		}

		ws := string(window)

		// DCS-wrapped variant (sent when hook ran inside an inner tmux).
		if idx := strings.Index(ws, tabbyOSCPrefixDCS); idx >= 0 {
			rest := ws[idx+len(tabbyOSCPrefixDCS):]
			if end := strings.IndexByte(rest, '\x07'); end >= 0 {
				applyIndicatorPayload(rest[:end])
				window = window[:0]
			}
			continue
		}

		// Raw OSC variant (sent when hook ran with no inner tmux).
		if idx := strings.Index(ws, tabbyOSCPrefix); idx >= 0 {
			rest := ws[idx+len(tabbyOSCPrefix):]
			if end := strings.IndexByte(rest, '\x07'); end >= 0 {
				applyIndicatorPayload(rest[:end])
				window = window[:0]
			}
		}
	}
}

// applyIndicatorPayload parses "indicator;value" and calls doSetIndicator.
func applyIndicatorPayload(payload string) {
	parts := strings.SplitN(payload, ";", 2)
	if len(parts) != 2 {
		return
	}
	doSetIndicator([]string{parts[0], parts[1]})
}
