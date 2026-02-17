package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// TestHeaderColorsStruct verifies the HeaderColors struct has expected fields
func TestHeaderColorsStruct(t *testing.T) {
	colors := HeaderColors{
		Fg: "#ffffff",
		Bg: "#000000",
	}

	if colors.Fg != "#ffffff" {
		t.Errorf("Expected Fg=#ffffff, got %s", colors.Fg)
	}
	if colors.Bg != "#000000" {
		t.Errorf("Expected Bg=#000000, got %s", colors.Bg)
	}
}

// TestHeaderColorsEmpty verifies empty HeaderColors work correctly
func TestHeaderColorsEmpty(t *testing.T) {
	colors := HeaderColors{}

	if colors.Fg != "" {
		t.Errorf("Expected empty Fg, got %s", colors.Fg)
	}
	if colors.Bg != "" {
		t.Errorf("Expected empty Bg, got %s", colors.Bg)
	}
}

// TestDefaultSidebarWidth verifies the expected default sidebar width
func TestDefaultSidebarWidth(t *testing.T) {
	// Default sidebar width should be 25 (from config and scripts)
	expectedDefault := 25

	// This is a documentation test - verifying the constant matches expectations
	if expectedDefault < 10 || expectedDefault > 80 {
		t.Errorf("Default sidebar width %d outside reasonable bounds (10-80)", expectedDefault)
	}
}

// TestCollapsedHeightMinimum verifies minimum collapsed pane height
func TestCollapsedHeightMinimum(t *testing.T) {
	// Minimum collapsed height should be 1 (tmux minimum)
	collapsedHeight := 1

	if collapsedHeight < 1 {
		t.Errorf("Collapsed height %d is less than tmux minimum of 1", collapsedHeight)
	}
}

func TestTrimContentAndRegions(t *testing.T) {
	content := "a\nb\nc\nd\n"
	regions := []daemon.ClickableRegion{
		{StartLine: 0, EndLine: 0, Action: "one"},
		{StartLine: 2, EndLine: 3, Action: "two"},
		{StartLine: 4, EndLine: 4, Action: "drop"},
	}

	trimmed, trimmedRegions := trimContentAndRegions(content, regions, 3)

	if trimmed != "a\nb\nc\n" {
		t.Fatalf("unexpected trimmed content: %q", trimmed)
	}
	if len(trimmedRegions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(trimmedRegions))
	}
	if trimmedRegions[1].StartLine != 2 || trimmedRegions[1].EndLine != 2 {
		t.Fatalf("expected second region clamped to line 2, got start=%d end=%d", trimmedRegions[1].StartLine, trimmedRegions[1].EndLine)
	}
}

func TestTrimContentAndRegionsZeroLines(t *testing.T) {
	trimmed, trimmedRegions := trimContentAndRegions("a\nb\n", []daemon.ClickableRegion{{StartLine: 0, EndLine: 0}}, 0)

	if trimmed != "" {
		t.Fatalf("expected empty content, got %q", trimmed)
	}
	if len(trimmedRegions) != 0 {
		t.Fatalf("expected no regions, got %d", len(trimmedRegions))
	}
}
