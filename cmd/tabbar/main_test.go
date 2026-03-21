package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestBuildTabs_Empty(t *testing.T) {
	m := model{grouped: nil, width: 80}
	tabs := m.buildTabs()
	if len(tabs) != 0 {
		t.Fatalf("buildTabs with empty grouped = %d tabs, want 0", len(tabs))
	}
}

func TestBuildTabs_OneGroup(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "zsh", Active: true}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	tabs := m.buildTabs()
	if len(tabs) != 1 {
		t.Fatalf("buildTabs with 1 window = %d tabs, want 1", len(tabs))
	}
}

func TestBuildTabs_MultipleGroups(t *testing.T) {
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{{Index: 1, Name: "zsh"}}},
			{Windows: []tmux.Window{{Index: 2, Name: "vim"}, {Index: 3, Name: "bash"}}},
		},
		width: 80,
	}
	tabs := m.buildTabs()
	if len(tabs) != 3 {
		t.Fatalf("buildTabs with 3 windows across 2 groups = %d tabs, want 3", len(tabs))
	}
}

func TestGetWindowAtX_Empty(t *testing.T) {
	m := model{grouped: nil, width: 80}
	if got := m.getWindowAtX(10); got != nil {
		t.Fatal("getWindowAtX on empty model should return nil")
	}
}

func TestGetWindowAtX_HitFirstTab(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "zsh", Active: true}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	// The first tab starts at X=0 for scrollPos=0
	got := m.getWindowAtX(0)
	if got == nil {
		t.Fatal("getWindowAtX at X=0 should hit first tab")
	}
}

func TestGetWindowAtX_MissRight(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "a", Active: false}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	// Clicking far right should miss
	got := m.getWindowAtX(500)
	if got != nil {
		t.Fatal("getWindowAtX at X=500 should miss")
	}
}

func TestIsNewTabButtonAt_Basic(t *testing.T) {
	m := model{grouped: nil, width: 80}
	// With no tabs, button starts at position 1
	// Just check the function returns a bool without panicking
	got1 := m.isNewTabButtonAt(0)
	got2 := m.isNewTabButtonAt(100)
	_ = got1
	_ = got2
}

func TestGetPaneAtX_NilWindow(t *testing.T) {
	m := model{windows: nil, width: 80}
	if got := m.getPaneAtX(10); got != nil {
		t.Fatal("getPaneAtX with nil windows should return nil")
	}
}

func TestGetPaneAtX_SinglePane(t *testing.T) {
	// Single pane - getPaneAtX returns nil (len(panes) <= 1)
	m := model{
		windows: []tmux.Window{
			{
				Index:  1,
				Active: true,
				Panes: []tmux.Pane{
					{ID: "%1", Index: 0, Active: true, Command: "zsh"},
				},
			},
		},
		width: 80,
	}
	if got := m.getPaneAtX(5); got != nil {
		t.Fatal("getPaneAtX with single pane should return nil")
	}
}

func TestGetPaneAtX_MultiplePanes(t *testing.T) {
	m := model{
		windows: []tmux.Window{
			{
				Index:  1,
				Active: true,
				Panes: []tmux.Pane{
					{ID: "%1", Index: 0, Active: true, Command: "zsh"},
					{ID: "%2", Index: 1, Active: false, Command: "vim"},
				},
			},
		},
		width: 80,
	}
	// Click at X=2 which is right at the start of pane text after indent
	got := m.getPaneAtX(2)
	// Just check it doesn't panic; result may be nil or a pane depending on widths
	_ = got
}

func TestTriggerRefresh_ReturnsCmd(t *testing.T) {
	cmd := triggerRefresh()
	if cmd == nil {
		t.Fatal("triggerRefresh should return non-nil Cmd")
	}
}

func TestDelayedRefresh_ReturnsCmd(t *testing.T) {
	cmd := delayedRefresh()
	if cmd == nil {
		t.Fatal("delayedRefresh should return non-nil Cmd")
	}
}

func TestView_Empty(t *testing.T) {
	m := model{grouped: nil, width: 80}
	got := m.View()
	if got == "" {
		t.Fatal("View() with empty grouped should return 'No windows' message")
	}
}

func TestView_WithOneWindow(t *testing.T) {
	win := tmux.Window{Index: 1, Name: "zsh", Active: true}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	got := m.View()
	if got == "" {
		t.Fatal("View() with windows should return non-empty")
	}
}

func TestView_WithScrollIndicator(t *testing.T) {
	win1 := tmux.Window{Index: 1, Name: "zsh", Active: true}
	win2 := tmux.Window{Index: 2, Name: "vim", Active: false}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win1, win2}},
		},
		width:     80,
		scrollPos: 1,
	}
	got := m.View()
	if got == "" {
		t.Fatal("View() with scrollPos>0 should return non-empty")
	}
}

func TestAdjustScrollForActiveTab_Empty(t *testing.T) {
	m := model{grouped: nil, width: 80}
	m.adjustScrollForActiveTab()
	if m.scrollPos != 0 {
		t.Fatalf("adjustScrollForActiveTab with empty grouped should set scrollPos=0, got %d", m.scrollPos)
	}
}

func TestAdjustScrollForActiveTab_WithActiveWindow(t *testing.T) {
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{
				{Index: 1, Name: "zsh", Active: false},
				{Index: 2, Name: "vim", Active: true},
			}},
		},
		width:     80,
		scrollPos: 5,
	}
	m.adjustScrollForActiveTab()
	if m.scrollPos < 0 {
		t.Fatal("scrollPos should not be negative after adjustment")
	}
}

func TestBuildPaneBar_NoActiveWindow(t *testing.T) {
	m := model{grouped: nil, width: 80}
	got := m.buildPaneBar()
	if got != "" {
		t.Fatalf("buildPaneBar with no active window should return empty, got %q", got)
	}
}

func TestBuildPaneBar_ActiveWindowSinglePane(t *testing.T) {
	win := tmux.Window{
		Index:  1,
		Active: true,
		Panes:  []tmux.Pane{{ID: "%1", Index: 0, Active: true, Command: "zsh"}},
	}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	got := m.buildPaneBar()
	if got != "" {
		t.Fatalf("buildPaneBar with single pane should return empty, got %q", got)
	}
}

func TestBuildPaneBar_ActiveWindowMultiplePanes(t *testing.T) {
	win := tmux.Window{
		Index:  1,
		Active: true,
		Panes: []tmux.Pane{
			{ID: "%1", Index: 0, Active: true, Command: "zsh"},
			{ID: "%2", Index: 1, Active: false, Command: "vim"},
		},
	}
	m := model{
		grouped: []grouping.GroupedWindows{
			{Windows: []tmux.Window{win}},
		},
		width: 80,
	}
	got := m.buildPaneBar()
	if got == "" {
		t.Fatal("buildPaneBar with multiple panes should return non-empty")
	}
}
