package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

func newPickerTestModel() rendererModel {
	m := rendererModel{
		width:         80,
		height:        24,
		connected:     true,
		content:       strings.Repeat("\n", 40),
		totalLines:    41,
		pickerShowing: true,
		pickerTitle:   "Set Marker",
		pickerScope:   "window",
		pickerTarget:  "@1",
		pickerOptions: []daemon.MarkerOptionPayload{
			{Symbol: "🚀", Name: "rocket", Keywords: "launch"},
			{Symbol: "🔥", Name: "fire", Keywords: "hot"},
		},
	}
	m.pickerApplyFilter()
	return m
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

func TestRenderPickerModalShowsEmptyStateAndMeta(t *testing.T) {
	m := newPickerTestModel()
	m.pickerQuery = "zzzz-no-match"
	m.pickerApplyFilter()

	lines := m.renderPickerModal()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Set Marker") {
		t.Fatalf("expected picker title in modal")
	}
	if !strings.Contains(joined, "Search:") {
		t.Fatalf("expected search line in modal")
	}
	if !strings.Contains(joined, "Results: 0") {
		t.Fatalf("expected zero results meta in modal")
	}
	if !strings.Contains(joined, "No matching markers") {
		t.Fatalf("expected empty-state text in modal")
	}
}

func TestViewOverlaysPickerModal(t *testing.T) {
	m := newPickerTestModel()
	view := m.View()

	if !strings.Contains(view, "Set Marker") {
		t.Fatalf("expected picker title to be overlayed in view")
	}
	if !strings.Contains(view, "Results:") {
		t.Fatalf("expected picker results meta in overlayed view")
	}
}

func TestRenderPickerModalFixtureOutput(t *testing.T) {
	if os.Getenv("TABBY_PRINT_PICKER_FIXTURE") != "1" {
		t.Skip("fixture output disabled")
	}
	fixturePath := "../../tests/screenshots/baseline/sidebar-marker-picker.txt"
	fixtureBytes, err := os.ReadFile(fixturePath)
	fixture := ""
	if err == nil {
		fixture = string(fixtureBytes)
	} else {
		m := newPickerTestModel()
		fixture = m.View()
	}

	fmt.Println("TABBY_PICKER_FIXTURE_BEGIN")
	fmt.Println(fixture)
	fmt.Println("TABBY_PICKER_FIXTURE_END")
}
