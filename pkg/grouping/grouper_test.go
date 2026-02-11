package grouping

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestGroupWindows(t *testing.T) {
	// Windows use @tabby_group option stored in Group field
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "StudioDome"},
		{Name: "tool", Index: 1, Group: "Gunpowder"},
		{Name: "notes", Index: 2, Group: ""},  // Empty Group means Default
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: "^SD\\|"},  // Pattern kept for backwards compat but not used
		{Name: "Gunpowder", Pattern: "^GP\\|"},
		{Name: "Default", Pattern: ".*"},
	}

	result := GroupWindows(windows, groups)

	counts := map[string]int{}
	for _, group := range result {
		counts[group.Name] = len(group.Windows)
	}

	if counts["StudioDome"] != 1 {
		t.Fatalf("expected StudioDome count 1, got %d", counts["StudioDome"])
	}
	if counts["Gunpowder"] != 1 {
		t.Fatalf("expected Gunpowder count 1, got %d", counts["Gunpowder"])
	}
	if counts["Default"] != 1 {
		t.Fatalf("expected Default count 1, got %d", counts["Default"])
	}
}

func TestGroupWindowsFallbackToDefault(t *testing.T) {
	// Windows with unknown group name fall back to Default
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "UnknownGroup"},
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: ""},
		{Name: "Default", Pattern: ""},
	}

	result := GroupWindows(windows, groups)

	if len(result) != 1 {
		t.Fatalf("expected 1 group, got %d", len(result))
	}
	if result[0].Name != "Default" {
		t.Fatalf("expected Default group, got %s", result[0].Name)
	}
	if len(result[0].Windows) != 1 {
		t.Fatalf("expected 1 window in Default, got %d", len(result[0].Windows))
	}
}

func TestLightenColor(t *testing.T) {
	tests := []struct {
		name     string
		color    string
		amount   float64
		want     string
		tolerance int64
	}{
		{"lighten black by 15%", "#000000", 0.15, "#262626", 2},
		{"lighten black by 50%", "#000000", 0.50, "#7f7f7f", 2},
		{"lighten black fully", "#000000", 1.0, "#ffffff", 2},
		{"lighten white (no change)", "#ffffff", 0.5, "#ffffff", 2},
		{"lighten mid gray by 15%", "#808080", 0.15, "#939393", 2},
		{"lighten blue by 15%", "#3498db", 0.15, "#4da8e1", 5},
		{"invalid color returns original", "#gg", 0.5, "#gg", 0},
		{"no hash prefix", "808080", 0.15, "939393", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LightenColor(tt.color, tt.amount)
			if tt.tolerance == 0 {
				if got != tt.want {
					t.Errorf("LightenColor(%q, %v) = %q, want %q", tt.color, tt.amount, got, tt.want)
				}
			} else {
				if !colorsClose(got, tt.want, tt.tolerance) {
					t.Errorf("LightenColor(%q, %v) = %q, want ~%q", tt.color, tt.amount, got, tt.want)
				}
			}
		})
	}
}

func TestDarkenColor(t *testing.T) {
	tests := []struct {
		name     string
		color    string
		amount   float64
		want     string
		tolerance int64
	}{
		{"darken white by 15%", "#ffffff", 0.15, "#d9d9d9", 2},
		{"darken white by 50%", "#ffffff", 0.50, "#7f7f7f", 2},
		{"darken white fully", "#ffffff", 1.0, "#000000", 2},
		{"darken black (no change)", "#000000", 0.5, "#000000", 2},
		{"darken mid gray by 15%", "#808080", 0.15, "#6d6d6d", 2},
		{"darken blue by 15%", "#3498db", 0.15, "#2c81ba", 5},
		{"invalid color returns original", "#gg", 0.5, "#gg", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DarkenColor(tt.color, tt.amount)
			if tt.tolerance == 0 {
				if got != tt.want {
					t.Errorf("DarkenColor(%q, %v) = %q, want %q", tt.color, tt.amount, got, tt.want)
				}
			} else {
				if !colorsClose(got, tt.want, tt.tolerance) {
					t.Errorf("DarkenColor(%q, %v) = %q, want ~%q", tt.color, tt.amount, got, tt.want)
				}
			}
		})
	}
}

// colorsClose checks if two hex colors are close within a tolerance (per channel)
func colorsClose(a, b string, tolerance int64) bool {
	// Strip # prefix if present
	if len(a) > 0 && a[0] == '#' {
		a = a[1:]
	}
	if len(b) > 0 && b[0] == '#' {
		b = b[1:]
	}
	if len(a) != 6 || len(b) != 6 {
		return a == b
	}

	parseHex := func(s string) int64 {
		var v int64
		for _, c := range s {
			v *= 16
			if c >= '0' && c <= '9' {
				v += int64(c - '0')
			} else if c >= 'a' && c <= 'f' {
				v += int64(c - 'a' + 10)
			} else if c >= 'A' && c <= 'F' {
				v += int64(c - 'A' + 10)
			}
		}
		return v
	}

	r1, g1, b1 := parseHex(a[0:2]), parseHex(a[2:4]), parseHex(a[4:6])
	r2, g2, b2 := parseHex(b[0:2]), parseHex(b[2:4]), parseHex(b[4:6])

	abs := func(x int64) int64 {
		if x < 0 {
			return -x
		}
		return x
	}

	return abs(r1-r2) <= tolerance && abs(g1-g2) <= tolerance && abs(b1-b2) <= tolerance
}
