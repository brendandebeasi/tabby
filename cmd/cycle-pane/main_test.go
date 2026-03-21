package main

import (
	"testing"
)

func TestIsSkip_SidebarRenderer(t *testing.T) {
	if !isSkip("sidebar-renderer") {
		t.Fatal("sidebar-renderer should be skipped")
	}
}

func TestIsSkip_Tabbar(t *testing.T) {
	if !isSkip("tabbar") {
		t.Fatal("tabbar should be skipped")
	}
}

func TestIsSkip_PaneBar(t *testing.T) {
	if !isSkip("pane-bar") {
		t.Fatal("pane-bar should be skipped")
	}
}

func TestIsSkip_CaseInsensitive(t *testing.T) {
	if !isSkip("SIDEBAR-RENDER") {
		t.Fatal("isSkip should be case-insensitive")
	}
}

func TestIsSkip_NormalCommand(t *testing.T) {
	if isSkip("zsh") {
		t.Fatal("zsh should not be skipped")
	}
	if isSkip("vim") {
		t.Fatal("vim should not be skipped")
	}
	if isSkip("bash") {
		t.Fatal("bash should not be skipped")
	}
}

func TestIsHeader_PaneHeader(t *testing.T) {
	if !isHeader("pane-header") {
		t.Fatal("pane-header should be a header")
	}
}

func TestIsHeader_CaseInsensitive(t *testing.T) {
	if !isHeader("PANE-HEADER") {
		t.Fatal("isHeader should be case-insensitive")
	}
}

func TestIsHeader_NormalCommand(t *testing.T) {
	if isHeader("zsh") {
		t.Fatal("zsh should not be a header")
	}
}

func TestIsUtility_Skip(t *testing.T) {
	if !isUtility("tabbar") {
		t.Fatal("tabbar should be utility")
	}
}

func TestIsUtility_Header(t *testing.T) {
	if !isUtility("pane-header") {
		t.Fatal("pane-header should be utility")
	}
}

func TestIsUtility_Normal(t *testing.T) {
	if isUtility("zsh") {
		t.Fatal("zsh should not be utility")
	}
}

func TestFilterContent_RemovesUtility(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", active: true, command: "zsh", left: 0},
		{id: "%2", active: false, command: "tabbar", left: 5},
		{id: "%3", active: false, command: "pane-header", left: 0},
		{id: "%4", active: false, command: "vim", left: 10},
	}
	content := filterContent(panes)
	if len(content) != 2 {
		t.Fatalf("filterContent should keep 2 content panes, got %d", len(content))
	}
	for _, p := range content {
		if isUtility(p.command) {
			t.Fatalf("filterContent should not include utility pane %s", p.command)
		}
	}
}

func TestFilterContent_Empty(t *testing.T) {
	if got := filterContent(nil); got != nil {
		t.Fatalf("filterContent(nil) should return nil, got %v", got)
	}
}

func TestFilterContent_AllUtility(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", command: "tabbar"},
		{id: "%2", command: "sidebar-renderer"},
	}
	content := filterContent(panes)
	if len(content) != 0 {
		t.Fatalf("expected 0 content panes, got %d", len(content))
	}
}

func TestFilterContent_AllContent(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", active: true, command: "zsh"},
		{id: "%2", active: false, command: "vim"},
	}
	content := filterContent(panes)
	if len(content) != 2 {
		t.Fatalf("expected 2 content panes, got %d", len(content))
	}
}
