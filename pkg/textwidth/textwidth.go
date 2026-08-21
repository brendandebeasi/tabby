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
	return ansiAllRe.ReplaceAllString(s, "")
}

// Display measures the columns a terminal actually draws for s.
//
// Measuring rune-by-rune reports 1 for emoji presentation sequences such as
// the keycap "1<VS16><keycap>", which every terminal draws as 2 columns.
// Padding computed from the smaller number overflows the pane by a column.
// Measure by grapheme cluster instead, treating any cluster carrying an
// emoji presentation selector as double-width.
func Display(s string) int {
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		cw := runewidth.StringWidth(cluster)
		if cw < 2 && strings.ContainsRune(cluster, '️') {
			cw = 2
		}
		w += cw
	}
	return w
}

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
