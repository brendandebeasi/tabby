package hook

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
)

// tabbyOSCPrefix is the raw OSC sequence emitted by emitOSCFallback.
const tabbyOSCPrefix = "\x1b]7700;tabby-indicator;"

// tabbyOSCPrefixDCS is the same sequence after DCS passthrough un-escaping
// (each ESC in the payload is doubled by the sender when inside inner tmux).
const tabbyOSCPrefixDCS = "\x1b\x1b]7700;tabby-indicator;"

// tabbyCWDPrefix / tabbyCWDPrefixDCS carry a remote pane's working directory,
// emitted by the tabby-remote-cwd shell hook (scripts/tabby-remote-cwd.sh) on a
// host reached over ssh/mosh. The bytes travel over the connection into the
// LOCAL pane, where this handler (attached via pipe-pane) sees them and records
// the value on the local pane as @tabby_remote_cwd. The payload is
// "host\x1ftopmost" (remote hostname + remote project root).
const tabbyCWDPrefix = "\x1b]7700;tabby-cwd;"
const tabbyCWDPrefixDCS = "\x1b\x1b]7700;tabby-cwd;"

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
			continue
		}

		// Remote-cwd report (DCS-wrapped, when the remote shell ran inside an
		// inner tmux on the remote host).
		if idx := strings.Index(ws, tabbyCWDPrefixDCS); idx >= 0 {
			rest := ws[idx+len(tabbyCWDPrefixDCS):]
			if end := strings.IndexByte(rest, '\x07'); end >= 0 {
				applyRemoteCWDPayload(rest[:end])
				window = window[:0]
			}
			continue
		}

		// Remote-cwd report (raw OSC, the common case over ssh with no inner
		// tmux on the remote).
		if idx := strings.Index(ws, tabbyCWDPrefix); idx >= 0 {
			rest := ws[idx+len(tabbyCWDPrefix):]
			if end := strings.IndexByte(rest, '\x07'); end >= 0 {
				applyRemoteCWDPayload(rest[:end])
				window = window[:0]
			}
		}
	}
}

// applyRemoteCWDPayload records a remote pane's reported "host\x1ftopmost" on
// the LOCAL pane (this handler's source pane, identified by TMUX_PANE — the same
// signal the indicator path relies on) as @tabby_remote_cwd, which
// windowNameKey reads to key the tab's saved name on the remote project. Writes
// only when the value changed so a per-prompt report on an idle pane is a no-op.
func applyRemoteCWDPayload(payload string) {
	payload = strings.TrimSpace(payload)
	if payload == "" || !strings.Contains(payload, "\x1f") {
		return
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	// Skip the write when unchanged (avoids churn from per-prompt reports).
	if cur, err := exec.Command("tmux", "show-options", "-pqv", "-t", pane, "@tabby_remote_cwd").Output(); err == nil {
		if strings.TrimSpace(string(cur)) == payload {
			return
		}
	}
	exec.Command("tmux", "set-option", "-p", "-t", pane, "@tabby_remote_cwd", payload).Run()
}

// applyIndicatorPayload parses "indicator;value" and calls doSetIndicator.
func applyIndicatorPayload(payload string) {
	parts := strings.SplitN(payload, ";", 2)
	if len(parts) != 2 {
		return
	}
	doSetIndicator([]string{parts[0], parts[1]})
}
