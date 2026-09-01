package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/ansi"
	"github.com/mattn/go-runewidth"
)

func resetQuotaBarCache() {
	quotaBarCache.Clear()
	quotaBarCacheN.Store(0)
}

func pct(v float64) *float64 { return &v }

func inMinutes(m int) int64 {
	return time.Now().Add(time.Duration(m) * time.Minute).UnixMilli()
}

func TestRenderQuotaBarReturnsSameOutputFromCache(t *testing.T) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	first := renderQuotaBar(pct(0.9), reset, 14, "#1f6feb", "#ffffff", "#0d1117")
	second := renderQuotaBar(pct(0.9), reset, 14, "#1f6feb", "#ffffff", "#0d1117")
	if first != second {
		t.Fatalf("cached render differs from first render:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "90%") {
		t.Fatalf("render dropped the percentage: %q", first)
	}
	if got := runewidth.StringWidth(ansi.Strip(first)); got != 14 {
		t.Fatalf("bar is %d cells wide, want 14", got)
	}
	if got := quotaBarCacheN.Load(); got != 1 {
		t.Fatalf("repeat render stored %d entries, want 1", got)
	}
}

// The memo key holds the formatted label rather than resetMs precisely so the
// countdown keeps ticking. A regression here would freeze every bar at
// whatever time it first rendered.
func TestRenderQuotaBarCountdownKeepsTicking(t *testing.T) {
	resetQuotaBarCache()
	far, near := inMinutes(120), inMinutes(60)
	farBar := renderQuotaBar(pct(0.9), far, 14, "#1f6feb", "#ffffff", "#0d1117")
	nearBar := renderQuotaBar(pct(0.9), near, 14, "#1f6feb", "#ffffff", "#0d1117")
	if farBar == nearBar {
		t.Fatalf("a bar resetting in 2h rendered the same as one resetting in 1h: %q", farBar)
	}
	// shortResetDur counts down from now, so ask it what it just produced
	// rather than hard-coding a label that drifts within the test's runtime.
	for _, c := range []struct{ bar, want string }{{farBar, shortResetDur(far)}, {nearBar, shortResetDur(near)}} {
		if !strings.Contains(c.bar, c.want) {
			t.Errorf("bar %q is missing its countdown %q", c.bar, c.want)
		}
	}
}

// Two different fractions can round to the same label ("90% 2h") yet must
// still fill a different number of cells, so pct belongs in the key even
// though the label already does.
func TestRenderQuotaBarKeysOnTheFillNotJustTheLabel(t *testing.T) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	low := renderQuotaBar(pct(0.10), reset, 20, "#1f6feb", "#ffffff", "#0d1117")
	high := renderQuotaBar(pct(0.90), reset, 20, "#1f6feb", "#ffffff", "#0d1117")
	if low == high {
		t.Fatalf("10%% and 90%% rendered identically: %q", low)
	}
	if got := quotaBarCacheN.Load(); got != 2 {
		t.Fatalf("two distinct fills stored %d entries, want 2", got)
	}
}

func TestRenderQuotaBarKeysOnEveryInput(t *testing.T) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	base := renderQuotaBar(pct(0.5), reset, 14, "#1f6feb", "#ffffff", "#0d1117")
	if got := renderQuotaBar(pct(0.5), reset, 16, "#1f6feb", "#ffffff", "#0d1117"); got == base {
		t.Errorf("a different width produced the same output as the base render")
	}
	// Under a test binary lipgloss sees no TTY and drops color, so the three
	// color inputs legitimately render the same bytes here. Their
	// contribution to the key is checked through the entry count instead.
	renderQuotaBar(pct(0.5), reset, 14, "#da3633", "#ffffff", "#0d1117")
	renderQuotaBar(pct(0.5), reset, 14, "#1f6feb", "#000000", "#0d1117")
	renderQuotaBar(pct(0.5), reset, 14, "#1f6feb", "#ffffff", "#ffffff")
	if got := quotaBarCacheN.Load(); got != 5 {
		t.Fatalf("five distinct keys stored %d entries, want 5", got)
	}
}

func TestRenderQuotaBarCacheStaysBounded(t *testing.T) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	for i := range quotaBarCacheMax * 2 {
		renderQuotaBar(pct(0.5), reset, 4+i, "#1f6feb", "#ffffff", "#0d1117")
	}
	if got := quotaBarCacheN.Load(); got > quotaBarCacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", got, quotaBarCacheMax)
	}
	live := 0
	quotaBarCache.Range(func(any, any) bool {
		live++
		return true
	})
	if live > quotaBarCacheMax {
		t.Fatalf("map holds %d entries, want at most %d", live, quotaBarCacheMax)
	}
}

func TestRenderQuotaBarSurvivesCacheEviction(t *testing.T) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	want := renderQuotaBar(pct(0.5), reset, 14, "#1f6feb", "#ffffff", "#0d1117")
	for i := range quotaBarCacheMax * 2 {
		renderQuotaBar(pct(0.5), reset, 4+i, "#1f6feb", "#ffffff", "#0d1117")
	}
	if got := renderQuotaBar(pct(0.5), reset, 14, "#1f6feb", "#ffffff", "#0d1117"); got != want {
		t.Fatalf("render after eviction differs:\n%q\n%q", want, got)
	}
}

// A nil fraction is the "no data yet" bar; it must not panic and must not
// collide with a real reading.
func TestRenderQuotaBarHandlesUnknownUsage(t *testing.T) {
	resetQuotaBarCache()
	unknown := renderQuotaBar(nil, 0, 14, "#1f6feb", "#ffffff", "#0d1117")
	if !strings.Contains(unknown, "--") {
		t.Fatalf("unknown usage did not render a placeholder: %q", unknown)
	}
	if got := renderQuotaBar(pct(0), 0, 14, "#1f6feb", "#ffffff", "#0d1117"); got == unknown {
		t.Fatalf("0%% rendered the same as unknown usage: %q", got)
	}
}

func BenchmarkQuotaBarBody(b *testing.B) {
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		quotaBarBody("90% 2h", 40+i%40, 14, "#1f6feb", "#ffffff", "#0d1117")
	}
}

func BenchmarkRenderQuotaBar(b *testing.B) {
	resetQuotaBarCache()
	reset := inMinutes(150)
	fracs := []float64{0.12, 0.44, 0.78, 0.91}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		renderQuotaBar(&fracs[i%len(fracs)], reset, 14, "#1f6feb", "#ffffff", "#0d1117")
	}
}
