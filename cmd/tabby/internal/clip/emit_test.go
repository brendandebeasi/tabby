package clip

import (
	"strings"
	"testing"
)

func TestEncodeBareSequence(t *testing.T) {
	got := Encode([]byte("hello"), false)
	want := "\033]52;c;aGVsbG8=\a"
	if got != want {
		t.Fatalf("Encode() = %q, want %q", got, want)
	}
}

// mosh 1.4 recognizes only the BEL-terminated form with the "c" selector.
// Switching either one silently drops the clipboard write on the phone, which
// is the hardest failure in this feature to notice, so pin both.
func TestEncodeUsesMoshCompatibleFraming(t *testing.T) {
	got := Encode([]byte("x"), false)
	if !strings.HasPrefix(got, "\033]52;c;") {
		t.Errorf("sequence must open with ESC]52;c; — got %q", got)
	}
	if !strings.HasSuffix(got, "\a") {
		t.Errorf("sequence must end with BEL, not ST — got %q", got)
	}
}

func TestEncodePassthroughWrapsAndDoublesEscapes(t *testing.T) {
	got := Encode([]byte("hello"), true)
	want := "\033Ptmux;\033\033]52;c;aGVsbG8=\a\033\\"
	if got != want {
		t.Fatalf("Encode(passthrough) = %q, want %q", got, want)
	}
}

// A newline anywhere in the payload terminates the escape early and dumps the
// rest of the base64 onto the screen as text. Encoding is what protects
// against that, so prove multi-line input survives.
func TestEncodeMultilinePayloadHasNoRawNewline(t *testing.T) {
	got := Encode([]byte("one\ntwo\nthree"), false)
	if strings.Contains(got, "\n") {
		t.Fatalf("encoded sequence contains a raw newline: %q", got)
	}
}

func TestTruncateKeepsTail(t *testing.T) {
	got, truncated := Truncate([]byte("abcdefgh"), 3)
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if string(got) != "fgh" {
		t.Errorf("Truncate() = %q, want %q", got, "fgh")
	}
}

func TestTruncateLeavesShortPayloadAlone(t *testing.T) {
	got, truncated := Truncate([]byte("abc"), 8)
	if truncated {
		t.Error("truncated = true, want false")
	}
	if string(got) != "abc" {
		t.Errorf("Truncate() = %q, want %q", got, "abc")
	}
}

func TestTruncateZeroMaxDisablesCap(t *testing.T) {
	in := []byte("abcdefgh")
	got, truncated := Truncate(in, 0)
	if truncated {
		t.Error("truncated = true, want false")
	}
	if string(got) != string(in) {
		t.Errorf("Truncate() = %q, want %q", got, in)
	}
}

func TestTrimCaptureDropsPaddingAndTrailingBlanks(t *testing.T) {
	got := trimCapture("first line   \nsecond\t\n\n   \n")
	want := "first line\nsecond"
	if got != want {
		t.Fatalf("trimCapture() = %q, want %q", got, want)
	}
}

func TestParseSendRejectsTwoSources(t *testing.T) {
	var opts sendOpts
	code, stop := parseSend([]string{"--text", "a", "--file", "b"}, &opts)
	if !stop || code != 2 {
		t.Fatalf("parseSend() = (%d, %v), want (2, true)", code, stop)
	}
}

// `--pane` takes an optional target, so the parser has to tell a target from
// the next flag. Getting this wrong swallows the flag and sends the wrong
// pane's contents.
func TestParseSendPaneTargetIsOptional(t *testing.T) {
	var bare sendOpts
	if code, stop := parseSend([]string{"--pane", "--quiet"}, &bare); stop {
		t.Fatalf("parseSend() stopped with code %d, want it to parse", code)
	}
	if !bare.paneSet || bare.pane != "" || !bare.quiet {
		t.Errorf("parseSend(--pane --quiet) = %+v, want paneSet with no target and quiet", bare)
	}

	var targeted sendOpts
	if code, stop := parseSend([]string{"--pane", "%3"}, &targeted); stop {
		t.Fatalf("parseSend() stopped with code %d, want it to parse", code)
	}
	if targeted.pane != "%3" {
		t.Errorf("pane = %q, want %q", targeted.pane, "%3")
	}
}

func TestParseSendRejectsMissingFlagValue(t *testing.T) {
	var opts sendOpts
	code, stop := parseSend([]string{"--text"}, &opts)
	if !stop || code != 2 {
		t.Fatalf("parseSend() = (%d, %v), want (2, true)", code, stop)
	}
}
