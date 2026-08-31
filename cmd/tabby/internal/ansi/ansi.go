// Package ansi handles ANSI escape sequences in strings that are about to be
// measured or truncated.
package ansi

import "strings"

// Strip removes SGR colour sequences (ESC [ ... m) from s.
//
// It replaces four identical copies of a regexp-based version that each called
// regexp.MustCompile inside the function body, recompiling the pattern on
// every call. That showed up as roughly a twelfth of the daemon's CPU while
// idle, plus the allocation churn behind it, because the sidebar strips every
// line of every widget on every frame.
//
// Only sequences ending in 'm' are removed, matching the behaviour of the
// `\x1b\[[0-9;]*m` pattern this replaces. Anything else, including a bare ESC
// or an unterminated sequence, is left in place: this runs on text headed for
// a width calculation, where dropping bytes that still occupy columns would be
// worse than leaving an oddity alone.
func Strip(s string) string {
	// The overwhelmingly common case is a line with no escapes at all, which
	// needs no allocation and no copy.
	i := strings.IndexByte(s, 0x1b)
	if i < 0 {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s[:i])

	for i < len(s) {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		if end, ok := sgrEnd(s, i); ok {
			i = end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// sgrEnd reports whether an SGR sequence starts at s[i], and if so the index
// just past its terminating 'm'.
func sgrEnd(s string, i int) (int, bool) {
	j := i + 1
	if j >= len(s) || s[j] != '[' {
		return 0, false
	}
	for j++; j < len(s); j++ {
		c := s[j]
		if c >= '0' && c <= '9' || c == ';' {
			continue
		}
		if c == 'm' {
			return j + 1, true
		}
		return 0, false
	}
	return 0, false
}
