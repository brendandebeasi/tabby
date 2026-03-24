package main

import (
	"os"
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func withArgs(args []string, fn func()) {
	old := os.Args
	os.Args = args
	defer func() { os.Args = old }()
	fn()
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
		t.Fatalf("disabled bell must not appear, got %q", got)
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
		t.Fatalf("last must not show on active window, got %q", got)
	}
}

func TestBuildIndicators_WithColor(t *testing.T) {
	win := tmux.Window{Activity: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Activity: config.Indicator{Enabled: true, Icon: "●", Color: "#00ff00"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "fg=") {
		t.Fatalf("want fg= color escape in %q", got)
	}
}

func TestBuildIndicators_MultipleActive(t *testing.T) {
	win := tmux.Window{Bell: true, Activity: true}
	cfg := &config.Config{
		Indicators: config.Indicators{
			Bell:     config.Indicator{Enabled: true, Icon: "🔔"},
			Activity: config.Indicator{Enabled: true, Icon: "●"},
		},
	}
	got := buildIndicators(win, cfg)
	if !strings.Contains(got, "🔔") || !strings.Contains(got, "●") {
		t.Fatalf("want both indicators in %q", got)
	}
}

func TestMain_NoArgs(t *testing.T) {
	withArgs([]string{"cmd"}, main)
}

func TestMain_InvalidIndex(t *testing.T) {
	withArgs([]string{"cmd", "notanumber"}, main)
}

func TestMain_ValidIndex(t *testing.T) {
	withArgs([]string{"cmd", "1"}, main)
}
