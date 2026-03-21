package main

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestGetActiveFg_ConfigWins(t *testing.T) {
	m := model{config: &config.Config{PaneHeader: config.PaneHeader{ActiveFg: "#ff0000"}}}
	if got := m.getActiveFg(); got != "#ff0000" {
		t.Fatalf("getActiveFg config = %q, want #ff0000", got)
	}
}

func TestGetActiveFg_ThemeWins(t *testing.T) {
	th := colors.GetTheme("dark")
	m := model{
		config: &config.Config{},
		theme:  &th,
	}
	got := m.getActiveFg()
	if got == "" {
		t.Fatal("getActiveFg with theme should return non-empty")
	}
}

func TestGetActiveFg_Default(t *testing.T) {
	m := model{config: &config.Config{}}
	if got := m.getActiveFg(); got == "" {
		t.Fatal("getActiveFg default should return non-empty fallback")
	}
}

func TestGetInactiveFg_ConfigWins(t *testing.T) {
	m := model{config: &config.Config{PaneHeader: config.PaneHeader{InactiveFg: "#888888"}}}
	if got := m.getInactiveFg(); got != "#888888" {
		t.Fatalf("getInactiveFg config = %q, want #888888", got)
	}
}

func TestGetInactiveFg_Default(t *testing.T) {
	m := model{config: &config.Config{}}
	if got := m.getInactiveFg(); got == "" {
		t.Fatal("getInactiveFg default should return non-empty fallback")
	}
}

func TestGetButtonFg_ConfigWins(t *testing.T) {
	m := model{config: &config.Config{PaneHeader: config.PaneHeader{ButtonFg: "#00ff00"}}}
	if got := m.getButtonFg(); got != "#00ff00" {
		t.Fatalf("getButtonFg config = %q, want #00ff00", got)
	}
}

func TestGetButtonFg_Default(t *testing.T) {
	m := model{config: &config.Config{}}
	if got := m.getButtonFg(); got == "" {
		t.Fatal("getButtonFg default should return non-empty fallback")
	}
}

func TestView_NilWindow(t *testing.T) {
	m := model{config: &config.Config{}, window: nil, width: 80}
	got := m.View()
	if !strings.Contains(got, "No window") {
		t.Fatalf("View() nil window should show 'No window', got %q", got)
	}
}

func TestView_EmptyPanes(t *testing.T) {
	win := &tmux.Window{Index: 1, Panes: []tmux.Pane{}}
	m := model{config: &config.Config{}, window: win, width: 80}
	got := m.View()
	if got != "" {
		t.Fatalf("View() empty panes should return empty, got %q", got)
	}
}

func TestView_WithSinglePane(t *testing.T) {
	win := &tmux.Window{
		Index: 1,
		Panes: []tmux.Pane{
			{ID: "%1", Index: 0, Active: true, Command: "zsh"},
		},
	}
	m := model{config: &config.Config{}, window: win, width: 80, windowIdx: 1}
	got := m.View()
	if got == "" {
		t.Fatal("View() with single pane should return non-empty")
	}
}

func TestView_WithMultiplePanes(t *testing.T) {
	win := &tmux.Window{
		Index: 1,
		Panes: []tmux.Pane{
			{ID: "%1", Index: 0, Active: true, Command: "zsh"},
			{ID: "%2", Index: 1, Active: false, Command: "vim"},
		},
	}
	m := model{config: &config.Config{}, window: win, width: 80, windowIdx: 1}
	got := m.View()
	if got == "" {
		t.Fatal("View() with multiple panes should return non-empty")
	}
}

func TestView_FiltersPaneBarPanes(t *testing.T) {
	win := &tmux.Window{
		Index: 1,
		Panes: []tmux.Pane{
			{ID: "%1", Index: 0, Active: false, Command: "pane-bar"},
		},
	}
	m := model{config: &config.Config{}, window: win, width: 80}
	got := m.View()
	if got != "" {
		t.Fatalf("View() with only pane-bar panes should return empty, got %q", got)
	}
}

func TestView_PaneTitleOverridesCommand(t *testing.T) {
	win := &tmux.Window{
		Index: 1,
		Panes: []tmux.Pane{
			{ID: "%1", Index: 0, Active: true, Command: "python3", Title: "my-script"},
		},
	}
	m := model{config: &config.Config{}, window: win, width: 80, windowIdx: 1}
	got := m.View()
	if !strings.Contains(got, "my-script") {
		t.Fatalf("View() should show pane title instead of command, got %q", got)
	}
}

func TestTriggerRefresh_ReturnsCmd(t *testing.T) {
	cmd := triggerRefresh()
	if cmd == nil {
		t.Fatal("triggerRefresh should return non-nil Cmd")
	}
}
