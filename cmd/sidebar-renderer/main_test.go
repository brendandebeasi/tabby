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

func TestAbsInt_Renderer(t *testing.T) {
	if abs(5) != 5 || abs(-5) != 5 || abs(0) != 0 {
		t.Fatal("abs() wrong result")
	}
}

func TestMaxInt_Renderer(t *testing.T) {
	if max(3, 7) != 7 || max(7, 3) != 7 || max(5, 5) != 5 {
		t.Fatal("max() wrong result")
	}
}

func TestAtoiRenderer(t *testing.T) {
	if atoi("42") != 42 || atoi("-1") != -1 || atoi("bad") != 0 || atoi("") != 0 {
		t.Fatal("atoi() wrong result")
	}
}

func TestClampInt(t *testing.T) {
	if clampInt(5, 0, 10) != 5 {
		t.Fatal("within range")
	}
	if clampInt(-3, 0, 10) != 0 {
		t.Fatal("below min")
	}
	if clampInt(15, 0, 10) != 10 {
		t.Fatal("above max")
	}
}

func TestHexToHSL(t *testing.T) {
	h, s, l := hexToHSL("#ffffff")
	if l < 90 {
		t.Fatalf("white should have high lightness, got %d", l)
	}
	_ = h
	_ = s

	h2, s2, l2 := hexToHSL("#000000")
	if l2 > 10 {
		t.Fatalf("black should have near-zero lightness, got %d", l2)
	}
	_ = h2
	_ = s2

	h3, s3, l3 := hexToHSL("gggggg")
	if h3 != 180 || s3 != 70 || l3 != 50 {
		t.Fatalf("non-hex chars should return defaults, got h=%d s=%d l=%d", h3, s3, l3)
	}
}

func TestHslToHex(t *testing.T) {
	got := hslToHex(0, 0, 100)
	if got != "#ffffff" {
		t.Fatalf("pure white (H=0 S=0 L=100) = %q, want #ffffff", got)
	}

	got = hslToHex(0, 0, 0)
	if got != "#000000" {
		t.Fatalf("pure black (H=0 S=0 L=0) = %q, want #000000", got)
	}
}

func TestSliderCursorIndex(t *testing.T) {
	if sliderCursorIndex(0, 100, 10) != 0 {
		t.Fatal("value=0 should map to index 0")
	}
	if sliderCursorIndex(100, 100, 10) != 9 {
		t.Fatal("value=max should map to last index")
	}
	if sliderCursorIndex(50, 100, 10) != 5 {
		t.Fatalf("value=50 out of 100 in width 10 should map to ~5, got %d", sliderCursorIndex(50, 100, 10))
	}

	if sliderCursorIndex(0, 0, 10) != 0 {
		t.Fatal("zero maxVal should return 0")
	}
}

func TestSliderValueAtPos(t *testing.T) {
	if sliderValueAtPos(0, 10, 100) != 0 {
		t.Fatal("pos=0 should give value 0")
	}
	if sliderValueAtPos(9, 10, 100) != 100 {
		t.Fatal("pos=last should give max value")
	}
	got5 := sliderValueAtPos(5, 10, 100)
	if got5 < 50 || got5 > 60 {
		t.Fatalf("pos=5 of 10 should give 50-60 range, got %d", got5)
	}
}

func TestFuzzyScore(t *testing.T) {
	if fuzzyScore("", "anything") < 0 {
		t.Fatal("empty query should always match")
	}
	if fuzzyScore("abc", "xyzabc") < 0 {
		t.Fatal("should find abc in xyzabc")
	}
	if fuzzyScore("notfound", "xyz") >= 0 {
		t.Fatal("query chars not in candidate should return -1")
	}
	shorter := fuzzyScore("abc", "abc")
	longer := fuzzyScore("abc", "xyzabc")
	if shorter <= longer {
		t.Fatal("shorter candidate with same query should score higher than longer candidate")
	}
}

func TestIsInMenuBounds(t *testing.T) {
	m := rendererModel{
		width:  40,
		height: 10,
		menuY:  2,
		menuItems: []daemon.MenuItemPayload{
			{Label: "A"},
			{Label: "B"},
		},
	}
	if !m.isInMenuBounds(10, m.menuStartY()) {
		t.Fatal("point at startY should be in bounds")
	}
	if m.isInMenuBounds(10, 0) {
		t.Fatal("point above menu should be out of bounds")
	}
	if m.isInMenuBounds(-1, m.menuStartY()) {
		t.Fatal("negative x should be out of bounds")
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
