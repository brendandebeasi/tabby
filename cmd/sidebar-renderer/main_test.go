package main

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

func TestSliceByColumns(t *testing.T) {
	got := sliceByColumns("abcdef", 1, 4)
	if got != "bcd" {
		t.Fatalf("sliceByColumns() = %q, want %q", got, "bcd")
	}
}

func TestTruncateToWidth(t *testing.T) {
	if got := truncateToWidth("hello", 3); got != "hel" {
		t.Fatalf("truncateToWidth() = %q, want %q", got, "hel")
	}
	if got := truncateToWidth("ok", 5); got != "ok" {
		t.Fatalf("truncateToWidth() = %q, want %q", got, "ok")
	}
}

func TestMenuStartYClampsToScreen(t *testing.T) {
	m := rendererModel{
		height: 5,
		menuY:  10,
		menuItems: []daemon.MenuItemPayload{
			{Label: "A"},
			{Label: "B"},
		},
	}

	if got := m.menuStartY(); got != 1 {
		t.Fatalf("menuStartY() = %d, want %d", got, 1)
	}
}

func TestMenuItemAtScreenYSkipsNonSelectable(t *testing.T) {
	m := rendererModel{
		width:  40,
		height: 10,
		menuY:  2,
		menuItems: []daemon.MenuItemPayload{
			{Label: "Header", Header: true},
			{Label: "Selectable"},
			{Label: "Sep", Separator: true},
		},
	}

	if got := m.menuItemAtScreenY(3); got != -1 {
		t.Fatalf("header row should not be selectable, got %d", got)
	}
	if got := m.menuItemAtScreenY(4); got != 1 {
		t.Fatalf("expected selectable row index 1, got %d", got)
	}
	if got := m.menuItemAtScreenY(5); got != -1 {
		t.Fatalf("separator row should not be selectable, got %d", got)
	}
}

func TestRenderMenuLinesIncludesTitleAndBorders(t *testing.T) {
	m := rendererModel{
		width:         20,
		height:        10,
		menuY:         0,
		menuTitle:     "Menu",
		menuItems:     []daemon.MenuItemPayload{{Label: "Item", Key: "i"}},
		menuHighlight: 0,
	}

	lines := m.renderMenuLines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 menu lines, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Menu") {
		t.Fatalf("expected rendered menu to include title")
	}
	if !strings.Contains(joined, "┌") || !strings.Contains(joined, "└") {
		t.Fatalf("expected rendered menu borders")
	}
}
