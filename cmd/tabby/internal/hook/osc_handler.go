package hook

import (
	"bufio"
	"bytes"
	"io"
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

// The stream this reads is every byte a pane prints, and the sequences it is
// looking for arrive a few times a minute at most. So the cost that matters is
// the cost of NOT matching, paid per byte of ordinary terminal output on every
// piped pane at once.
//
// It used to be paid per byte literally: the reader took one byte at a time,
// converted the whole 4KB sliding window to a string (a fresh allocation and
// copy of up to 4KB, for every byte) and ran four substring searches over all
// of it. Scanning 1MB of build output cost 5.5s and 2.58GB of allocation, which
// is how two handlers on chatty panes came to burn 9 and 14 minutes of CPU over
// three days. Reading in chunks and searching the bytes in place makes the same
// megabyte 0.4ms and 0 allocations.
const (
	// oscReadChunk is the read size. Anything above a few KB makes no
	// measurable difference; the win is in not scanning per byte.
	oscReadChunk = 32 << 10

	// oscMaxPending caps the partial sequence carried across reads. A prefix
	// whose terminator never arrives — a truncated write, a pane printing the
	// prefix as literal text — must not grow the window without bound.
	oscMaxPending = 8 << 10
)

// oscPrefixes is searched in order, longest first. The DCS-wrapped forms must
// come first: each is its raw counterpart with an extra leading ESC, so the raw
// prefix also matches one byte into a DCS-wrapped sequence, and testing raw
// first would consume the wrapped one at the wrong offset.
var oscPrefixes = [][]byte{
	[]byte(tabbyOSCPrefixDCS),
	[]byte(tabbyCWDPrefixDCS),
	[]byte(tabbyOSCPrefix),
	[]byte(tabbyCWDPrefix),
}

// oscKeepTail is the most bytes of a prefix that can be sitting at the end of a
// read waiting for the rest of it, so it is the longest prefix less one. Sizing
// this off the SHORTEST prefix instead drops the head of the longest one on
// every chunk boundary, and a pane emitting an indicator loses it whenever the
// pty happens to split there.
var oscKeepTail = func() int {
	n := 0
	for _, p := range oscPrefixes {
		if len(p) > n {
			n = len(p)
		}
	}
	return n - 1
}()

// doOSCHandler reads stdin (a tmux pipe-pane output stream) and applies every
// tabby OSC 7700 sequence it finds. Runs until stdin closes, which happens when
// the pane exits.
func doOSCHandler() {
	scanOSCStream(os.Stdin, applyOSCPayload)
}

// applyOSCPayload dispatches on which prefix the payload followed.
func applyOSCPayload(prefix, payload string) {
	switch prefix {
	case tabbyOSCPrefix, tabbyOSCPrefixDCS:
		applyIndicatorPayload(payload)
	case tabbyCWDPrefix, tabbyCWDPrefixDCS:
		applyRemoteCWDPayload(payload)
	}
}

// scanOSCStream is doOSCHandler's loop, with the tmux side effects behind apply
// so it can be tested and benchmarked without a server.
func scanOSCStream(in io.Reader, apply func(prefix, payload string)) {
	r := bufio.NewReaderSize(in, 64<<10)
	buf := make([]byte, oscReadChunk)
	// One buffer for the life of the process, sized for a full read on top of
	// the largest carry. consumeOSC returns a slice pointing into it, so the
	// carry is copied back to the front rather than resliced in place: a
	// reslice keeps the tail's small capacity, and the next append reallocates
	// the whole thing. That alone was ~2.4MB of garbage per megabyte piped.
	window := make([]byte, 0, oscReadChunk+oscMaxPending+oscKeepTail)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			window = append(window, buf[:n]...)
			// copy handles the overlap: the destination is always at or below
			// the source, so it copies forward.
			window = window[:copy(window[:cap(window)], consumeOSC(window, apply))]
		}
		if err != nil {
			return
		}
	}
}

// consumeOSC applies every complete sequence in window and returns the bytes
// that still have to be carried into the next read: a sequence split across a
// chunk boundary, or a tail short enough to be the start of one.
//
// Unlike the byte-at-a-time version this replaced, it drops only up to the
// terminator of a sequence it applied rather than clearing the whole window, so
// two sequences arriving in the same read both take effect. The old loop reset
// the window on the first and lost the second.
func consumeOSC(window []byte, apply func(prefix, payload string)) []byte {
	for {
		at, prefix := firstOSCPrefix(window)
		if at < 0 {
			// Nothing in flight. Keep only what could be the start of a prefix.
			if len(window) > oscKeepTail {
				window = window[len(window)-oscKeepTail:]
			}
			return window
		}
		rest := window[at+len(prefix):]

		// A payload is plain text and ends at the BEL. An ESC before that BEL
		// means this prefix is not a real sequence — a pane printing one as
		// literal text, or a write truncated mid-sequence — and the BEL further
		// along belongs to whatever starts at that ESC. Taking it anyway would
		// apply a payload of junk AND eat the real sequence inside it.
		end := bytes.IndexByte(rest, '\x07')
		esc := bytes.IndexByte(rest, '\x1b')
		if esc >= 0 && (end < 0 || esc < end) {
			window = rest[esc:]
			continue
		}
		if end < 0 {
			// Still arriving. Carry the partial, unless it has outgrown any
			// real payload, in which case the terminator is never coming and
			// holding it would rescan the same bytes for the life of the pane.
			if len(rest) > oscMaxPending {
				window = rest[len(rest)-oscKeepTail:]
				continue
			}
			return window[at:]
		}
		apply(string(prefix), string(rest[:end]))
		window = rest[end+1:]
	}
}

// firstOSCPrefix returns the offset of the earliest prefix in window, or -1.
// Earliest rather than first-listed: a DCS-wrapped sequence later in the buffer
// must not be handled before a raw one that arrived ahead of it.
func firstOSCPrefix(window []byte) (int, []byte) {
	best, found := -1, []byte(nil)
	for _, p := range oscPrefixes {
		at := bytes.Index(window, p)
		if at < 0 {
			continue
		}
		// On a tie the longer prefix wins, which is the DCS form: both match at
		// the same offset only when the raw prefix is the wrapped one's tail.
		if best < 0 || at < best || (at == best && len(p) > len(found)) {
			best, found = at, p
		}
	}
	return best, found
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
