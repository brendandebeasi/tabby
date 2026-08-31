package daemon

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/rivo/uniseg"
)

// constrainWidgetWidthRebuild is what constrainWidgetWidth used to do
// unconditionally: split, measure, truncate the wide lines, rejoin. The fast
// path has to agree with it byte for byte on every input, so the tests below
// compare against it rather than against hand-written expectations.
func constrainWidgetWidthRebuild(content string, maxWidth int) string {
	if maxWidth < 1 {
		return content
	}
	lines := strings.Split(content, "\n")
	var result strings.Builder
	for i, line := range lines {
		if uniseg.StringWidth(stripAnsi(line)) > maxWidth {
			result.WriteString(truncateToWidthUniseg(line, maxWidth))
		} else {
			result.WriteString(line)
		}
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}
	return result.String()
}

func constrainCases() []struct {
	name     string
	content  string
	maxWidth int
} {
	return []struct {
		name     string
		content  string
		maxWidth int
	}{
		{"empty", "", 20},
		{"single short line", "cpu 4%", 20},
		{"single line exactly at the limit", "abcdefghij", 10},
		{"single line one over", "abcdefghijk", 10},
		{"multi line all fitting", "one\ntwo\nthree", 20},
		{"multi line one overflowing", "one\nthis line is far too wide\nthree", 10},
		{"multi line all overflowing", "aaaaaaaaaaaa\nbbbbbbbbbbbb", 4},
		{"trailing newline", "one\n", 20},
		{"leading newline", "\none", 20},
		{"only newlines", "\n\n\n", 20},
		{"ansi styled and fitting", "\x1b[31mred\x1b[0m", 10},
		{"ansi styled and overflowing", "\x1b[31mred and rather long\x1b[0m", 6},
		{"wide emoji fitting", "☁️ 1️⃣", 10},
		{"wide emoji overflowing", "☁️☁️☁️☁️☁️☁️", 4},
		{"maxWidth zero", "anything at all", 0},
		{"maxWidth negative", "anything at all", -3},
	}
}

func TestConstrainWidgetWidthMatchesTheRebuild(t *testing.T) {
	for _, tc := range constrainCases() {
		t.Run(tc.name, func(t *testing.T) {
			want := constrainWidgetWidthRebuild(tc.content, tc.maxWidth)
			got := constrainWidgetWidth(tc.content, tc.maxWidth)
			if got != want {
				t.Fatalf("constrainWidgetWidth(%q, %d)\n got %q\nwant %q", tc.content, tc.maxWidth, got, want)
			}
		})
	}
}

// The point of the fast path is that a frame with nothing to truncate hands
// back the caller's own string instead of a rebuilt copy. Compare the backing
// pointers, because an equal copy would pass a == check and prove nothing.
func TestConstrainWidgetWidthReturnsTheInputWhenNothingOverflows(t *testing.T) {
	for _, tc := range constrainCases() {
		if constrainWidgetWidthRebuild(tc.content, tc.maxWidth) != tc.content {
			continue // this case really does need rebuilding
		}
		t.Run(tc.name, func(t *testing.T) {
			got := constrainWidgetWidth(tc.content, tc.maxWidth)
			if unsafe.StringData(got) != unsafe.StringData(tc.content) {
				t.Fatalf("constrainWidgetWidth(%q, %d) rebuilt an identical string instead of returning the input",
					tc.content, tc.maxWidth)
			}
		})
	}
}

func TestGradientColumnsIsStableAndCached(t *testing.T) {
	first := gradientColumns("#204070", "#102038", 26)
	second := gradientColumns("#204070", "#102038", 26)

	if len(first) != 26 {
		t.Fatalf("got %d columns, want 26", len(first))
	}
	if &first[0] != &second[0] {
		t.Fatal("gradientColumns recomputed a key it had already seen")
	}
	for i, esc := range first {
		if !strings.HasPrefix(esc, "\x1b[48;2;") || !strings.HasSuffix(esc, "m") {
			t.Fatalf("column %d = %q, want a truecolor background escape", i, esc)
		}
	}
}

func TestGradientColumnsVariesWithEveryKeyField(t *testing.T) {
	base := gradientColumns("#204070", "#102038", 26)

	if other := gradientColumns("#207040", "#102038", 26); other[0] == base[0] {
		t.Error("changing fromHex did not change the leading column")
	}
	if other := gradientColumns("#204070", "#381020", 26); other[len(other)-1] == base[len(base)-1] {
		t.Error("changing toHex did not change the trailing column")
	}
	if other := gradientColumns("#204070", "#102038", 40); len(other) != 40 {
		t.Errorf("changing width returned %d columns, want 40", len(other))
	}
}

// The cap exists so a drag-resize, which walks one width at a time, cannot
// grow the cache without bound.
func TestGradientColumnsCacheIsBounded(t *testing.T) {
	for i := 0; i < gradientColumnsCacheMax*3; i++ {
		gradientColumns(fmt.Sprintf("#%06x", i), "#102038", 8)
	}
	if n := gradientColumnsCacheN.Load(); n > gradientColumnsCacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", n, gradientColumnsCacheMax)
	}

	live := 0
	gradientColumnsCache.Range(func(_, _ any) bool {
		live++
		return true
	})
	if int64(live) > gradientColumnsCacheMax {
		t.Fatalf("cache map holds %d entries, want at most %d", live, gradientColumnsCacheMax)
	}
}

func BenchmarkConstrainWidgetWidthFits(b *testing.B) {
	content := strings.Repeat("cpu 4%  mem 61%\n", 12)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = constrainWidgetWidth(content, 40)
	}
}

func BenchmarkGradientColumns(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = gradientColumns("#204070", "#102038", 26)
	}
}
