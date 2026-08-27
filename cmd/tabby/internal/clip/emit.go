package clip

import (
	"encoding/base64"
	"strings"
)

// MaxPayload is the default ceiling on raw bytes sent in one OSC 52 write.
//
// The escape has to survive every layer between the emitting pane and the
// outer device: tmux's parser, mosh's terminal emulator, then the terminal
// app itself. Each imposes its own cap and none of them report a failure —
// an oversized payload is dropped silently, or worse, truncated mid-base64
// so the receiver writes garbage to the clipboard. 64 KiB of raw input is
// ~87 KB once base64-encoded, which clears the limits we know about with
// room to spare. Callers can raise or lower it with --max.
const MaxPayload = 64 * 1024

// Selection is the OSC 52 selection buffer to target.
//
// Only "c" (clipboard) is used. mosh 1.4 parses the selector but accepts
// nothing else — a "p" (primary) request is discarded before it reaches the
// client — so offering the choice would just be a way to send bytes into a
// hole. The constant exists to name the value at its use site rather than
// leave a bare "c" in the middle of an escape sequence.
const Selection = "c"

// Encode renders payload as an OSC 52 clipboard-set escape sequence.
//
// The shape is  ESC ] 52 ; c ; <base64> BEL. BEL terminates rather than
// ST (ESC \) because that is the form mosh's parser recognizes, and mosh is
// the narrowest link in the chain for the phone.
//
// When passthrough is true the sequence is wrapped in a tmux DCS envelope
// with the inner ESC doubled, which tells an enclosing tmux to forward the
// bytes upstream verbatim instead of interpreting them. That is only correct
// when the emitting process sits inside a tmux that is NOT the one meant to
// act on the escape — a nested tmux on a remote host, for instance. Writing
// to a local pane's pty is the opposite case: that tmux is the intended
// consumer, so the sequence must go out bare.
func Encode(payload []byte, passthrough bool) string {
	b64 := base64.StdEncoding.EncodeToString(payload)
	seq := "\033]52;" + Selection + ";" + b64 + "\a"
	if !passthrough {
		return seq
	}
	return "\033Ptmux;" + strings.ReplaceAll(seq, "\033", "\033\033") + "\033\\"
}

// Truncate caps payload at max bytes, reporting whether it had to cut.
//
// Cutting is preferable to refusing: the common oversized source is a long
// scrollback capture, where the tail is the part worth having. A max of zero
// or less disables the cap.
func Truncate(payload []byte, max int) (out []byte, truncated bool) {
	if max <= 0 || len(payload) <= max {
		return payload, false
	}
	return payload[len(payload)-max:], true
}
