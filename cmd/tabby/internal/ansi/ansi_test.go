package ansi

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// oldStripAnsi is verbatim the implementation this package replaces, kept as
// the oracle: the only thing that matters about Strip is that it agrees with
// what shipped, while costing far less.
var oldRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func oldStripAnsi(s string) string { return oldRegex.ReplaceAllString(s, "") }

func TestStripMatchesTheRegexItReplaces(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"\x1b[0m",
		"\x1b[31mred\x1b[0m",
		"\x1b[1;32mbold green\x1b[0m tail",
		"lead \x1b[38;5;214mcolour\x1b[m done",
		"\x1b[m",                    // empty parameter list
		"\x1b[999;999;999mabsurd",   // long parameters
		"\x1b[31m\x1b[32m\x1b[33mx", // adjacent sequences
		"tabby \x1b[36m✓\x1b[0m ✗",  // multibyte neighbours
		"\x1b",                      // bare escape
		"\x1b[",                     // truncated
		"\x1b[31",                   // unterminated
		"\x1b[31X",                  // wrong terminator
		"\x1b]0;title\x07",          // OSC, not SGR
		"\x1b[2J",                   // erase-display, not SGR
		"\x1b[A",                    // cursor-up, not SGR
		"a\x1bb",                    // escape mid-text
		strings.Repeat("\x1b[31mx\x1b[0m", 50),
	}
	for _, in := range cases {
		assert.Equal(t, oldStripAnsi(in), Strip(in), "input %q", in)
	}
}

func TestStripLeavesCleanStringsUntouched(t *testing.T) {
	// The hot path: no escape means no allocation and no copy.
	in := "a perfectly ordinary window title"
	assert.Equal(t, in, Strip(in))
}

func FuzzStripMatchesRegex(f *testing.F) {
	for _, s := range []string{"\x1b[31mx", "\x1b[", "plain", "\x1b[0;1;2m", "\x1b]8;;u\x07"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		assert.Equal(t, oldStripAnsi(s), Strip(s))
	})
}

func BenchmarkStripClean(b *testing.B) {
	s := "vellum-assistant  claude  3 panes"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Strip(s)
	}
}

func BenchmarkStripColoured(b *testing.B) {
	s := "\x1b[1;36mvellum-assistant\x1b[0m  \x1b[32mclaude\x1b[0m  3 panes"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Strip(s)
	}
}

func BenchmarkOldStripColoured(b *testing.B) {
	s := "\x1b[1;36mvellum-assistant\x1b[0m  \x1b[32mclaude\x1b[0m  3 panes"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = oldStripAnsi(s)
	}
}

// BenchmarkOldStripColouredRecompiled reproduces the actual shipped cost: the
// MustCompile call sat inside the function, so the pattern was rebuilt per
// call.
func BenchmarkOldStripColouredRecompiled(b *testing.B) {
	s := "\x1b[1;36mvellum-assistant\x1b[0m  \x1b[32mclaude\x1b[0m  3 panes"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
		_ = re.ReplaceAllString(s, "")
	}
}
