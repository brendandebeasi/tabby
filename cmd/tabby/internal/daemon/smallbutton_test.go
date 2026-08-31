package daemon

import (
	"strings"
	"testing"
)

func resetSmallButtonCache() {
	smallButtonCache.Clear()
	smallButtonCacheN.Store(0)
}

func TestRenderSmallButtonReturnsSameOutputFromCache(t *testing.T) {
	resetSmallButtonCache()
	first := renderSmallButton(12, "New Tab", "#1f6feb", "#ffffff")
	second := renderSmallButton(12, "New Tab", "#1f6feb", "#ffffff")
	if first != second {
		t.Fatalf("cached render differs from first render:\n%q\n%q", first, second)
	}
	if !strings.Contains(first, "New Tab") {
		t.Fatalf("render dropped the label: %q", first)
	}
	if got := smallButtonCacheN.Load(); got != 1 {
		t.Fatalf("repeat render stored %d entries, want 1", got)
	}
}

func TestRenderSmallButtonKeysOnEveryInput(t *testing.T) {
	resetSmallButtonCache()
	base := renderSmallButton(12, "Close", "#1f6feb", "#ffffff")
	// Under a test binary lipgloss sees no TTY and drops color, so bg and fg
	// legitimately render the same bytes here. Their contribution to the key
	// is checked through the entry count instead.
	if got := renderSmallButton(14, "Close", "#1f6feb", "#ffffff"); got == base {
		t.Errorf("a different width produced the same output as the base render")
	}
	if got := renderSmallButton(12, "Split", "#1f6feb", "#ffffff"); got == base {
		t.Errorf("a different label produced the same output as the base render")
	}
	renderSmallButton(12, "Close", "#da3633", "#ffffff")
	renderSmallButton(12, "Close", "#1f6feb", "#000000")
	if got := smallButtonCacheN.Load(); got != 5 {
		t.Fatalf("five distinct keys stored %d entries, want 5", got)
	}
}

func TestRenderSmallButtonCacheStaysBounded(t *testing.T) {
	resetSmallButtonCache()
	for i := range smallButtonCacheMax * 2 {
		renderSmallButton(i, "Tab", "#1f6feb", "#ffffff")
	}
	if got := smallButtonCacheN.Load(); got > smallButtonCacheMax {
		t.Fatalf("cache holds %d entries, want at most %d", got, smallButtonCacheMax)
	}
	live := 0
	smallButtonCache.Range(func(any, any) bool {
		live++
		return true
	})
	if live > smallButtonCacheMax {
		t.Fatalf("map holds %d entries, want at most %d", live, smallButtonCacheMax)
	}
}

func TestRenderSmallButtonSurvivesCacheEviction(t *testing.T) {
	resetSmallButtonCache()
	want := renderSmallButton(12, "New Tab", "#1f6feb", "#ffffff")
	for i := range smallButtonCacheMax * 2 {
		renderSmallButton(i, "Tab", "#1f6feb", "#ffffff")
	}
	if got := renderSmallButton(12, "New Tab", "#1f6feb", "#ffffff"); got != want {
		t.Fatalf("render after eviction differs:\n%q\n%q", want, got)
	}
}

func BenchmarkRenderSmallButton(b *testing.B) {
	resetSmallButtonCache()
	labels := []string{"New Tab", "New Group", "Close", "Touch", "Wider", "Narrower"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		renderSmallButton(12, labels[i%len(labels)], "#1f6feb", "#ffffff")
	}
}
