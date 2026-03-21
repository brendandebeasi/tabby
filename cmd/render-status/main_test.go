package main

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestGetActiveWindowIndex_FirstWindow(t *testing.T) {
	windows := []tmux.Window{{Index: 1, Active: true}, {Index: 2}}
	if got := getActiveWindowIndex(windows); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestGetActiveWindowIndex_MiddleWindow(t *testing.T) {
	windows := []tmux.Window{{Index: 1}, {Index: 2, Active: true}, {Index: 3}}
	if got := getActiveWindowIndex(windows); got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestGetActiveWindowIndex_LastWindow(t *testing.T) {
	windows := []tmux.Window{{Index: 1}, {Index: 2}, {Index: 3, Active: true}}
	if got := getActiveWindowIndex(windows); got != 2 {
		t.Fatalf("want 2, got %d", got)
	}
}

func TestGetActiveWindowIndex_NoneActive(t *testing.T) {
	windows := []tmux.Window{{Index: 1}, {Index: 2}}
	if got := getActiveWindowIndex(windows); got != 0 {
		t.Fatalf("want 0 fallback, got %d", got)
	}
}

func TestGetActiveWindowIndex_Empty(t *testing.T) {
	if got := getActiveWindowIndex(nil); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestCalculateTabWidth_NoIcon(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "bash"}
	group := grouping.GroupedWindows{}
	cfg := &config.Config{}
	got := calculateTabWidth(win, group, cfg)
	if got <= 0 {
		t.Fatalf("tab width must be positive, got %d", got)
	}
}

func TestCalculateTabWidth_WithIcon(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "vim"}
	withIcon := calculateTabWidth(win, grouping.GroupedWindows{Theme: config.Theme{Icon: "★"}}, &config.Config{})
	withoutIcon := calculateTabWidth(win, grouping.GroupedWindows{}, &config.Config{})
	if withIcon <= withoutIcon {
		t.Fatalf("icon should increase width: withIcon=%d withoutIcon=%d", withIcon, withoutIcon)
	}
}

func TestCalculateTabWidth_LongerName(t *testing.T) {
	short := calculateTabWidth(tmux.Window{Index: 1, Name: "sh"}, grouping.GroupedWindows{}, &config.Config{})
	long := calculateTabWidth(tmux.Window{Index: 1, Name: "very-long-name"}, grouping.GroupedWindows{}, &config.Config{})
	if long <= short {
		t.Fatalf("longer name must produce wider tab: short=%d long=%d", short, long)
	}
}

func TestBuildIndicators_None(t *testing.T) {
	win := tmux.Window{}
	cfg := &config.Config{}
	if got := buildIndicators(win, cfg); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestBuildIndicators_Bell(t *testing.T) {
	win := tmux.Window{Bell: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Bell: config.Indicator{Enabled: true, Icon: "🔔"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "🔔") {
		t.Fatalf("want 🔔 in %q", got)
	}
}

func TestBuildIndicators_BellDisabled(t *testing.T) {
	win := tmux.Window{Bell: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Bell: config.Indicator{Enabled: false, Icon: "🔔"},
		},
	}
	if got := buildIndicators(win, cfg); got != "" {
		t.Fatalf("disabled bell should not appear, got %q", got)
	}
}

func TestBuildIndicators_Activity(t *testing.T) {
	win := tmux.Window{Activity: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Activity: config.Indicator{Enabled: true, Icon: "●"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "●") {
		t.Fatalf("want ● in %q", got)
	}
}

func TestBuildIndicators_Silence(t *testing.T) {
	win := tmux.Window{Silence: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Silence: config.Indicator{Enabled: true, Icon: "🔇"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "🔇") {
		t.Fatalf("want 🔇 in %q", got)
	}
}

func TestBuildIndicators_LastNotActive(t *testing.T) {
	win := tmux.Window{Last: true, Active: false}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Last: config.Indicator{Enabled: true, Icon: "⟵"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "⟵") {
		t.Fatalf("want ⟵ in %q", got)
	}
}

func TestBuildIndicators_LastActive_Skipped(t *testing.T) {
	win := tmux.Window{Last: true, Active: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Last: config.Indicator{Enabled: true, Icon: "⟵"},
		},
	}
	if got := buildIndicators(win, cfg); got != "" {
		t.Fatalf("last indicator must not show on active window, got %q", got)
	}
}

func TestBuildIndicators_WithColor(t *testing.T) {
	win := tmux.Window{Bell: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Bell: config.Indicator{Enabled: true, Icon: "🔔", Color: "#ff0000"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "🔔") {
		t.Fatalf("want 🔔 in %q", got)
	}
	if !strings.Contains(got, "fg=") {
		t.Fatalf("want fg= color escape in %q", got)
	}
}
