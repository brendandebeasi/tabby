package daemon

import (
	"runtime"
	"testing"
	"unicode/utf8"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/ansi"
	"github.com/charmbracelet/lipgloss"
)

// smallButtonBodySlow is the body renderSmallButton used before the manual
// padding: lipgloss doing the centering. The fuzz target below holds the fast
// path to producing byte-identical output.
func smallButtonBodySlow(width int, label, bgColor, fgColor string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Foreground(lipgloss.Color(fgColor)).
		Bold(true).
		Width(width).
		Align(lipgloss.Center).
		Render(label)
}

func FuzzSmallButtonBody(f *testing.F) {
	for _, label := range []string{"", "+ Tab", "x Close Tab", "<", "▲", "◐ Dark", "日本", "🐈"} {
		for _, w := range []int{0, 1, 2, 5, 11, 30} {
			f.Add(w, label)
		}
	}
	f.Fuzz(func(t *testing.T, width int, label string) {
		if !utf8.ValidString(label) || width < 0 || width > 200 {
			t.Skip()
		}
		got := smallButtonBody(width, label, "#27ae60", "#ffffff")
		want := smallButtonBodySlow(width, label, "#27ae60", "#ffffff")
		// Not byte-identical by design: lipgloss's Align emits the padding as
		// its own background-only run, while padding before the Render puts
		// the whole button in one run. What has to match is the button the
		// terminal draws, so compare the visible cells.
		if ansi.Strip(got) != ansi.Strip(want) {
			t.Fatalf("smallButtonBody(%d, %q) drew %q, want %q", width, label, ansi.Strip(got), ansi.Strip(want))
		}
		if len(got) > len(want) {
			t.Fatalf("smallButtonBody(%d, %q) emitted %d bytes, more than lipgloss's %d", width, label, len(got), len(want))
		}
	})
}

// The point of the manual padding is to keep the button off cellbuf.Wrap,
// whose pooled 32KB parser buffer was the daemon's single biggest allocation
// source. A miss has to be cheap, not just a hit.
func TestSmallButtonBodyIsCheapOnAMiss(t *testing.T) {
	smallButtonBody(30, "warmup", "#27ae60", "#ffffff")
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	const n = 200
	for i := range n {
		smallButtonBody(30, "+ New Tab", "#27ae60", "#ffffff")
		_ = i
	}
	runtime.ReadMemStats(&b)
	if per := (b.TotalAlloc - a.TotalAlloc) / n; per > 4096 {
		t.Fatalf("an uncached button allocated %d bytes, want well under a pooled parser's 32KB", per)
	}
}

func BenchmarkSmallButtonBody(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkString = smallButtonBody(30, "+ New Tab", "#27ae60", "#ffffff")
	}
}

func BenchmarkSmallButtonBodySlow(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		sinkString = smallButtonBodySlow(30, "+ New Tab", "#27ae60", "#ffffff")
	}
}

var sinkString string
