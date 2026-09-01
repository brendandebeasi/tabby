// Package textwidth measures and trims strings by the columns a terminal
// actually draws, not by rune or byte count.
//
// Every tabby TUI composes rows from parts whose widths are computed
// independently. A row wider than the pane wraps, which scrolls the
// alt-screen buffer and leaves every later frame offset, so the surface
// stays corrupted until its renderer restarts. Centralizing the measurement
// keeps the sidebar, the landing page, and any future full-pane surface
// agreeing on what "one column" means.
package textwidth

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

var (
	// ansiAllRe matches every SGR escape in a string.
	ansiAllRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// ansiHeadRe matches an SGR escape only at the start of a string.
	ansiHeadRe = regexp.MustCompile(`^\x1b\[[0-9;]*m`)
)

// StripANSI removes SGR color escapes so the remainder can be measured.
func StripANSI(s string) string {
	// Most rows handed to Clamp and Pad are plain text. Running the regex
	// engine over them to find nothing costs more than the scan that proves
	// there is nothing to find.
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	return ansiAllRe.ReplaceAllString(s, "")
}

// asciiCells reports the drawn width of s and whether it could be measured by
// length alone.
//
// Every byte in 0x20..0x7e is one self-contained, single-column, non-combining
// grapheme, so a string made only of those is exactly len(s) columns wide.
// Anything else -- a control byte, DEL, or any multi-byte sequence -- falls
// back to the general path. This matters because the general path segments
// the string into grapheme clusters, which costs on the order of 100ns per
// rune, and the sidebar measures dozens of rows per frame.
func asciiCells(s string) (int, bool) {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return 0, false
		}
	}
	return len(s), true
}

// Truncate cuts s down to w columns, appending tail if anything was dropped.
// It is an exact, faster replacement for runewidth.Truncate.
func Truncate(s string, w int, tail string) string {
	sw, ok := asciiCells(s)
	if !ok {
		return runewidth.Truncate(s, w, tail)
	}
	if sw <= w {
		return s
	}
	// Every byte of s is one column, so the cut point is just w minus the
	// room the tail needs -- no need to walk the string to find it. The tail
	// itself is usually "…", so it still gets measured properly.
	return s[:max(w-Cells(tail), 0)] + tail
}

// Cells measures the columns a terminal draws for s, treating it as a plain
// run of characters. It is an exact, faster replacement for
// runewidth.StringWidth.
//
// Use Display instead for anything that may contain emoji: Cells inherits
// runewidth's rune-by-rune measurement of presentation sequences.
func Cells(s string) int {
	if w, ok := asciiCells(s); ok {
		return w
	}
	return runewidth.StringWidth(s)
}

// Display measures the columns a terminal actually draws for s.
//
// Measuring rune-by-rune reports 1 for emoji presentation sequences such as
// the keycap "1<VS16><keycap>", which every terminal draws as 2 columns.
// Padding computed from the smaller number overflows the pane by a column.
// Measure by grapheme cluster instead, treating any cluster carrying an
// emoji presentation selector as double-width.
func Display(s string) int {
	if w, ok := asciiCells(s); ok {
		return w
	}
	// Without a presentation selector anywhere in s there is nothing to
	// correct, and runewidth segments into the same clusters this function
	// would. Deferring to it drops a whole redundant segmentation pass: the
	// loop below segments s, then calls StringWidth on each cluster, which
	// segments it again.
	if !strings.ContainsRune(s, vs16) {
		return runewidth.StringWidth(s)
	}
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		cw := runewidth.StringWidth(cluster)
		if cw < 2 && strings.ContainsRune(cluster, vs16) {
			cw = 2
		}
		w += cw
	}
	return w
}

// vs16 is VARIATION SELECTOR-16, the codepoint that asks for the emoji
// rendering of an otherwise text-default character.
const vs16 = '️'

// Clamp trims s to at most maxW drawn columns, preserving ANSI escapes.
//
// Escapes are copied through without consuming width, so a clamped row keeps
// its color. Clamping bounds a width miscalculation to a single clipped row
// instead of a wrapped frame.
func Clamp(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if Display(StripANSI(s)) <= maxW {
		return s
	}
	// A run of printable ASCII carries no escapes to preserve and no cluster
	// that spans bytes, so the cut is just a slice. The general path below
	// re-segments the remaining suffix for every character it copies.
	if _, ok := asciiCells(s); ok {
		return s[:maxW]
	}
	var b strings.Builder
	w := 0
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			if loc := ansiHeadRe.FindStringIndex(s[i:]); loc != nil {
				b.WriteString(s[i : i+loc[1]])
				i += loc[1]
				continue
			}
		}
		g := uniseg.NewGraphemes(s[i:])
		if !g.Next() {
			break
		}
		cluster := g.Str()
		cw := Display(cluster)
		if w+cw > maxW {
			break
		}
		b.WriteString(cluster)
		w += cw
		i += len(cluster)
	}
	return b.String()
}

// Pad right-pads s with spaces to exactly w drawn columns, clamping if s is
// already wider.
func Pad(s string, w int) string {
	cur := Display(StripANSI(s))
	if cur > w {
		return Clamp(s, w)
	}
	return s + strings.Repeat(" ", w-cur)
}

// IsPlainASCII reports whether every byte is printable ASCII, which is the
// case where a string's column count is provably len(s) and every width
// implementation agrees on it. Callers that must match another library's
// measurement byte for byte can use it to gate a fast path.
func IsPlainASCII(s string) bool {
	_, ok := asciiCells(s)
	return ok
}
