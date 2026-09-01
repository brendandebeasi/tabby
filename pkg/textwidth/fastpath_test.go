package textwidth

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// displaySlow and clampSlow are the bodies of Display and Clamp as they stood
// before the ASCII fast paths were added. The fuzz targets below hold the fast
// paths to producing byte-identical results.
func displaySlow(s string) int {
	w := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		cw := runewidth.StringWidth(cluster)
		if cw < 2 && strings.ContainsRune(cluster, '\uFE0F') {
			cw = 2
		}
		w += cw
	}
	return w
}

func clampSlow(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if displaySlow(stripSlow(s)) <= maxW {
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
		cw := displaySlow(cluster)
		if w+cw > maxW {
			break
		}
		b.WriteString(cluster)
		w += cw
		i += len(cluster)
	}
	return b.String()
}

func stripSlow(s string) string { return ansiAllRe.ReplaceAllString(s, "") }

func TestCellsMatchesRunewidth(t *testing.T) {
	for _, s := range []string{
		"", " ", "hello world", "90% 2h", "~/git/tabby", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~",
		"\x7f", "\t", "\n", "a\tb", "日本", "a日", "é", "🐈", "1️⃣", "👋🏽", "\x1b[31mred\x1b[0m",
	} {
		if got, want := Cells(s), runewidth.StringWidth(s); got != want {
			t.Errorf("Cells(%q) = %d, runewidth.StringWidth = %d", s, got, want)
		}
	}
}

// The fast path claims len(s) is the width for printable ASCII. Check that
// claim against runewidth for every byte it accepts, one at a time.
func TestAsciiCellsAcceptsOnlySingleColumnBytes(t *testing.T) {
	for b := range 256 {
		s := string([]byte{byte(b)})
		w, ok := asciiCells(s)
		if !ok {
			continue
		}
		if b < 0x20 || b > 0x7e {
			t.Errorf("byte %#02x took the fast path", b)
		}
		if w != 1 || runewidth.StringWidth(s) != 1 {
			t.Errorf("byte %#02x: fast path says %d, runewidth says %d", b, w, runewidth.StringWidth(s))
		}
	}
	if _, ok := asciiCells("é"); ok {
		t.Error("a multi-byte rune took the fast path")
	}
}

func FuzzDisplayFastPath(f *testing.F) {
	for _, s := range []string{"", "hello", "90% 2h", "日本", "🐈", "1️⃣", "\x1b[31mx\x1b[0m", "\x00\x7f"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip()
		}
		if got, want := Display(s), displaySlow(s); got != want {
			t.Fatalf("Display(%q) = %d, want %d", s, got, want)
		}
	})
}

func FuzzClampFastPath(f *testing.F) {
	for _, s := range []string{"", "hello world", "日本語です", "🐈🐈🐈", "\x1b[31mcolored\x1b[0m"} {
		for _, w := range []int{0, 1, 3, 8} {
			f.Add(s, w)
		}
	}
	f.Fuzz(func(t *testing.T, s string, maxW int) {
		if !utf8.ValidString(s) || maxW < -1 || maxW > 1<<12 {
			t.Skip()
		}
		if got, want := Clamp(s, maxW), clampSlow(s, maxW); got != want {
			t.Fatalf("Clamp(%q, %d) = %q, want %q", s, maxW, got, want)
		}
	})
}

func FuzzStripANSIFastPath(f *testing.F) {
	for _, s := range []string{"", "plain", "\x1b[31mx\x1b[0m", "\x1b[2Jx", "\x1b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip()
		}
		if got, want := StripANSI(s), stripSlow(s); got != want {
			t.Fatalf("StripANSI(%q) = %q, want %q", s, got, want)
		}
	})
}

func BenchmarkCellsASCII(b *testing.B) {
	for b.Loop() {
		sinkI = Cells("claude-code main 90% 2h")
	}
}

// The sidebar separates fields with "·", so plenty of real rows miss the fast
// path. This is the floor the fast path is measured against.
func BenchmarkCellsNonASCII(b *testing.B) {
	for b.Loop() {
		sinkI = Cells("claude-code · main · 90% 2h")
	}
}

func BenchmarkClampASCII(b *testing.B) {
	const row = "  agent  ~/git/tabby  main  90% 2h  idle 4m  "
	for b.Loop() {
		sinkS = Clamp(row, 24)
	}
}

func BenchmarkStripANSIPlain(b *testing.B) {
	for b.Loop() {
		sinkS = StripANSI("  agent  ~/git/tabby  main  90% 2h  ")
	}
}

var (
	sinkS string
	sinkI int
)

func BenchmarkDisplayNonASCII(b *testing.B) {
	for b.Loop() {
		sinkI = Display("claude-code · main · 90% 2h")
	}
}

func BenchmarkDisplayEmoji(b *testing.B) {
	for b.Loop() {
		sinkI = Display("☁️ claude-code · main 1️⃣")
	}
}

func FuzzTruncateFastPath(f *testing.F) {
	for _, s := range []string{"", "hello world", "~/git/tabby", "日本語です", "🐈🐈🐈", "1️⃣x"} {
		for _, tail := range []string{"", "…", "...", "~"} {
			for _, w := range []int{-1, 0, 1, 3, 8, 99} {
				f.Add(s, w, tail)
			}
		}
	}
	f.Fuzz(func(t *testing.T, s string, w int, tail string) {
		if !utf8.ValidString(s) || !utf8.ValidString(tail) || w < -8 || w > 1<<12 {
			t.Skip()
		}
		if got, want := Truncate(s, w, tail), runewidth.Truncate(s, w, tail); got != want {
			t.Fatalf("Truncate(%q, %d, %q) = %q, want %q", s, w, tail, got, want)
		}
	})
}

func BenchmarkTruncateASCII(b *testing.B) {
	for b.Loop() {
		sinkS = Truncate("  agent  ~/git/tabby  main  90% 2h  ", 20, "…")
	}
}
