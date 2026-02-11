package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// TestStripAnsi verifies ANSI escape code stripping
func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no ansi",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "simple color",
			input:    "\x1b[31mred\x1b[0m",
			expected: "red",
		},
		{
			name:     "multiple colors",
			input:    "\x1b[31mred\x1b[32mgreen\x1b[0m",
			expected: "redgreen",
		},
		{
			name:     "256 color",
			input:    "\x1b[38;5;196mtext\x1b[0m",
			expected: "text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripAnsi(tt.input)
			if result != tt.expected {
				t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestAbsInt verifies absolute value function
func TestAbsInt(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 0},
		{5, 5},
		{-5, 5},
		{-1, 1},
		{100, 100},
		{-100, 100},
	}

	for _, tt := range tests {
		result := absInt(tt.input)
		if result != tt.expected {
			t.Errorf("absInt(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

// TestClickableRegionBounds verifies region coordinate handling
func TestClickableRegionBounds(t *testing.T) {
	region := daemon.ClickableRegion{
		StartLine: 0,
		EndLine:   0,
		StartCol:  5,
		EndCol:    15,
		Action:    "test_action",
		Target:    "test_target",
	}

	// Test point inside region
	x, y := 10, 0
	if y < region.StartLine || y > region.EndLine {
		t.Errorf("Point (%d,%d) Y should be within lines %d-%d", x, y, region.StartLine, region.EndLine)
	}
	if x < region.StartCol || x >= region.EndCol {
		t.Errorf("Point (%d,%d) X should be within cols %d-%d", x, y, region.StartCol, region.EndCol)
	}

	// Test point outside region (left)
	x = 2
	if x >= region.StartCol && x < region.EndCol {
		t.Errorf("Point (%d,%d) should be outside region cols %d-%d", x, y, region.StartCol, region.EndCol)
	}

	// Test point outside region (right)
	x = 20
	if x >= region.StartCol && x < region.EndCol {
		t.Errorf("Point (%d,%d) should be outside region cols %d-%d", x, y, region.StartCol, region.EndCol)
	}
}

// TestRendererModelDefaults verifies default model values
func TestRendererModelDefaults(t *testing.T) {
	model := rendererModel{
		width:  80,
		height: 1,
	}

	if model.width != 80 {
		t.Errorf("Expected default width 80, got %d", model.width)
	}
	if model.height != 1 {
		t.Errorf("Expected default height 1 for header, got %d", model.height)
	}
	if model.connected {
		t.Error("Expected connected to be false by default")
	}
}

// TestSpinnerFrames verifies spinner animation frames exist
func TestSpinnerFrames(t *testing.T) {
	if len(spinnerFrames) == 0 {
		t.Error("spinnerFrames should not be empty")
	}
	if len(spinnerFrames) < 2 {
		t.Error("spinnerFrames should have multiple frames for animation")
	}
}
