package daemon

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withProfile pins the colour profile for one test and restores it after. The
// profile is global to lipgloss, so these tests must not run in parallel with
// anything that renders.
func withProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// paintSlow is what paint replaces: the straight lipgloss call.
func paintSlow(text, fg, bg string, bold bool) string {
	return paintStyle(paintKey{fg: fg, bg: bg, bold: bold}).Render(text)
}

var paintProfiles = []struct {
	name string
	p    termenv.Profile
}{
	{"TrueColor", termenv.TrueColor},
	{"ANSI256", termenv.ANSI256},
	{"ANSI", termenv.ANSI},
	{"Ascii", termenv.Ascii},
}

func TestPaintMatchesLipglossAcrossProfiles(t *testing.T) {
	cases := []struct {
		text, fg, bg string
		bold         bool
	}{
		{"hello", "#ff8800", "", false},
		{"hello", "#ff8800", "#223344", false},
		{"hello", "", "#223344", false},
		{"hello", "#ffffff", "#000000", true},
		{"hello", "", "", false},
		{"", "#ff8800", "", false},
		{" padded ", "#abcdef", "#123456", true},
		{"🐈 wide", "#ff0000", "", false},
		{"esc\x1b[1membedded\x1b[0m", "#ff0000", "#00ff00", false},
		{"named", "5", "12", false},
		{"multi\nline", "#ff8800", "#223344", true},
		{"tab\there", "#ff8800", "", false},
	}
	for _, prof := range paintProfiles {
		t.Run(prof.name, func(t *testing.T) {
			withProfile(t, prof.p)
			paintCache.Clear()
			paintCacheN.Store(0)
			for _, tc := range cases {
				got := paint(tc.text, tc.fg, tc.bg, tc.bold)
				want := paintSlow(tc.text, tc.fg, tc.bg, tc.bold)
				if got != want {
					t.Errorf("paint(%q, %q, %q, %v)\n got %q\nwant %q",
						tc.text, tc.fg, tc.bg, tc.bold, got, want)
				}
			}
		})
	}
}

// TestPaintCachesTheWrapperNotTheText is the whole point of the change: a
// second call with the same colours must not build a new style.
func TestPaintCachesTheWrapperNotTheText(t *testing.T) {
	withProfile(t, termenv.TrueColor)
	paintCache.Clear()
	paintCacheN.Store(0)

	paint("first", "#ff8800", "#223344", false)
	if n := paintCacheN.Load(); n != 1 {
		t.Fatalf("after one colour combination, cache holds %d entries, want 1", n)
	}
	paint("second, entirely different text", "#ff8800", "#223344", false)
	if n := paintCacheN.Load(); n != 1 {
		t.Fatalf("text is being keyed on: cache holds %d entries, want 1", n)
	}
	paint("third", "#ff8800", "#223344", true)
	if n := paintCacheN.Load(); n != 2 {
		t.Fatalf("bold must be part of the key: cache holds %d entries, want 2", n)
	}
}

// TestPaintKeysOnTheProfile guards the stale-entry trap: the same colours
// under a different profile are a different escape sequence.
func TestPaintKeysOnTheProfile(t *testing.T) {
	paintCache.Clear()
	paintCacheN.Store(0)

	withProfile(t, termenv.TrueColor)
	full := paint("x", "#ff8800", "", false)
	lipgloss.SetColorProfile(termenv.Ascii)
	plain := paint("x", "#ff8800", "", false)

	if plain != "x" {
		t.Errorf("under Ascii, paint returned %q, want the bare text", plain)
	}
	if full == plain {
		t.Errorf("TrueColor and Ascii produced the same output %q", full)
	}
}

func TestPaintCacheStaysBounded(t *testing.T) {
	withProfile(t, termenv.TrueColor)
	paintCache.Clear()
	paintCacheN.Store(0)

	for i := 0; i < paintCacheMax*3; i++ {
		// A distinct colour per iteration, the shape a long gradient has.
		paint("x", hexOf(i), "", false)
	}
	if n := paintCacheN.Load(); n > paintCacheMax {
		t.Errorf("cache grew to %d entries, cap is %d", n, paintCacheMax)
	}
	count := 0
	paintCache.Range(func(_, _ any) bool { count++; return true })
	if count > paintCacheMax {
		t.Errorf("cache holds %d live entries, cap is %d", count, paintCacheMax)
	}
}

// hexOf turns a counter into a distinct hex colour.
func hexOf(i int) string {
	const digits = "0123456789abcdef"
	b := []byte("#000000")
	for p := 6; p >= 1; p-- {
		b[p] = digits[i&0xf]
		i >>= 4
	}
	return string(b)
}

func FuzzPaint(f *testing.F) {
	f.Add("hello", "#ff8800", "#223344", true)
	f.Add("", "", "", false)
	f.Add("multi\nline\ttab", "#ffffff", "", false)
	f.Add("\x1b[31mred\x1b[0m", "#00ff00", "#000000", true)
	f.Add("🐈", "9", "", false)

	f.Fuzz(func(t *testing.T, text, fg, bg string, bold bool) {
		// Colour strings come from themes and config, not from arbitrary
		// input; a fuzzed one that lipgloss cannot parse is not interesting.
		if !plausibleColor(fg) || !plausibleColor(bg) {
			t.Skip()
		}
		for _, prof := range paintProfiles {
			lipgloss.SetColorProfile(prof.p)
			got := paint(text, fg, bg, bold)
			want := paintSlow(text, fg, bg, bold)
			if got != want {
				t.Fatalf("profile %s, paint(%q, %q, %q, %v)\n got %q\nwant %q",
					prof.name, text, fg, bg, bold, got, want)
			}
		}
		lipgloss.SetColorProfile(termenv.TrueColor)
	})
}

// plausibleColor reports whether s is the shape of colour the daemon actually
// passes: empty, a #rrggbb hex string, or a small decimal palette index.
func plausibleColor(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "#") {
		if len(s) != 7 {
			return false
		}
		for _, r := range s[1:] {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
		return true
	}
	// A palette index, which termenv only tolerates in 0-255: out of range it
	// indexes its 256-entry table unguarded and panics, for a plain lipgloss
	// call just as much as for paint. Colours reach the daemon from themes and
	// config, so that is a config-validation problem, not one paint can fix.
	if len(s) > 3 {
		return false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n <= 255
}

func BenchmarkPaintLipgloss(b *testing.B) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = paintSlow("session-name", "#ff8800", "#223344", true)
	}
}

func BenchmarkPaint(b *testing.B) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	paintCache.Clear()
	paintCacheN.Store(0)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = paint("session-name", "#ff8800", "#223344", true)
	}
}
