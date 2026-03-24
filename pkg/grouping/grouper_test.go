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
		{Name: "notes", Index: 2, Group: ""}, // Empty Group means Default
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: "^SD\\|"}, // Pattern kept for backwards compat but not used
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
		name      string
		color     string
		amount    float64
		want      string
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
		name      string
		color     string
		amount    float64
		want      string
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

func TestFindGroupTheme(t *testing.T) {
	groups := []config.Group{
		{Name: "Alpha", Theme: config.Theme{Bg: "#ff0000", Fg: "#ffffff"}},
		{Name: "Beta", Theme: config.Theme{Bg: "#00ff00", Fg: "#000000"}},
	}

	t.Run("found_returns_correct_theme", func(t *testing.T) {
		theme := FindGroupTheme("Alpha", groups)
		if theme.Bg != "#ff0000" {
			t.Errorf("expected Bg #ff0000, got %s", theme.Bg)
		}
		if theme.Fg != "#ffffff" {
			t.Errorf("expected Fg #ffffff, got %s", theme.Fg)
		}
	})

	t.Run("not_found_returns_default_dark_theme", func(t *testing.T) {
		theme := FindGroupTheme("Missing", groups)
		if theme.Bg != "#000000" {
			t.Errorf("expected default Bg #000000, got %s", theme.Bg)
		}
		if theme.Fg != "#ffffff" {
			t.Errorf("expected default Fg #ffffff, got %s", theme.Fg)
		}
	})

	t.Run("empty_groups_returns_default", func(t *testing.T) {
		theme := FindGroupTheme("Any", nil)
		if theme.Bg != "#000000" {
			t.Errorf("expected default Bg, got %s", theme.Bg)
		}
	})
}

func TestShadeColorByIndex(t *testing.T) {
	t.Run("index_zero_no_change", func(t *testing.T) {
		got := ShadeColorByIndex("#ff0000", 0)
		if got != "#ff0000" {
			t.Errorf("expected #ff0000, got %s", got)
		}
	})

	t.Run("index_1_darkens_by_8_percent", func(t *testing.T) {
		got := ShadeColorByIndex("#ffffff", 1)
		if !colorsClose(got, "#ebebeb", 3) {
			t.Errorf("unexpected shade at index 1: %s", got)
		}
	})

	t.Run("high_index_caps_at_40_percent", func(t *testing.T) {
		got5 := ShadeColorByIndex("#ffffff", 5)
		got10 := ShadeColorByIndex("#ffffff", 10)
		if got5 != got10 {
			t.Errorf("indices 5 and 10 should both cap at 40%% darken, got %s vs %s", got5, got10)
		}
	})

	t.Run("invalid_color_returned_unchanged", func(t *testing.T) {
		got := ShadeColorByIndex("notacolor", 1)
		if got != "notacolor" {
			t.Errorf("expected original, got %s", got)
		}
	})
}

func TestSaturateColor(t *testing.T) {
	t.Run("returns_valid_hex", func(t *testing.T) {
		got := SaturateColor("#3498db")
		if len(got) != 7 || got[0] != '#' {
			t.Errorf("expected hex color, got %s", got)
		}
	})

	t.Run("invalid_color_returned_unchanged", func(t *testing.T) {
		got := SaturateColor("bad")
		if got != "bad" {
			t.Errorf("expected 'bad', got %s", got)
		}
	})

	t.Run("grayscale_returns_valid_hex", func(t *testing.T) {
		got := SaturateColor("#808080")
		if len(got) != 7 || got[0] != '#' {
			t.Errorf("expected hex color, got %s", got)
		}
	})
}

func TestInactiveTabColor(t *testing.T) {
	t.Run("returns_valid_hex", func(t *testing.T) {
		got := InactiveTabColor("#336699", 0, 0)
		if len(got) != 7 || got[0] != '#' {
			t.Errorf("expected hex color, got %s", got)
		}
	})

	t.Run("invalid_color_returned_unchanged", func(t *testing.T) {
		got := InactiveTabColor("bad", 0, 0)
		if got != "bad" {
			t.Errorf("expected 'bad', got %s", got)
		}
	})

	t.Run("lightens_dark_color", func(t *testing.T) {
		base := "#333333"
		got := InactiveTabColor(base, 0.1, 1.0)
		if got == base {
			t.Errorf("expected lightened color, got same as input %s", got)
		}
		if len(got) != 7 || got[0] != '#' {
			t.Errorf("expected hex color, got %s", got)
		}
	})

	t.Run("caps_lightness_at_0_75", func(t *testing.T) {
		got := InactiveTabColor("#ffffff", 0.5, 1.0)
		if len(got) != 7 || got[0] != '#' {
			t.Errorf("expected hex color, got %s", got)
		}
	})
}

func TestGroupWindowsWithOptionsPinned(t *testing.T) {
	windows := []tmux.Window{
		{Name: "pinned-win", Index: 0, Group: "Default", Pinned: true},
		{Name: "normal-win", Index: 1, Group: "Default"},
	}
	groups := []config.Group{
		{Name: "Default"},
	}

	t.Run("pinned_window_appears_in_pinned_group", func(t *testing.T) {
		result := GroupWindowsWithOptions(windows, groups, false)
		if len(result) < 2 {
			t.Fatalf("expected at least 2 groups (Pinned + Default), got %d", len(result))
		}
		if result[0].Name != "Pinned" {
			t.Errorf("expected first group to be Pinned, got %s", result[0].Name)
		}
		if len(result[0].Windows) != 1 {
			t.Errorf("expected 1 pinned window, got %d", len(result[0].Windows))
		}
	})

	t.Run("include_empty_adds_all_configured_groups", func(t *testing.T) {
		emptyGroups := []config.Group{
			{Name: "Default"},
			{Name: "Other"},
		}
		result := GroupWindowsWithOptions([]tmux.Window{}, emptyGroups, true)
		names := map[string]bool{}
		for _, g := range result {
			names[g.Name] = true
		}
		if !names["Default"] {
			t.Error("expected Default group in includeEmpty result")
		}
		if !names["Other"] {
			t.Error("expected Other group in includeEmpty result")
		}
	})
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

func TestGroupWindowsWithOptionsHidesEmptyGroups(t *testing.T) {
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "Active"},
	}
	groups := []config.Group{
		{Name: "Active", Pattern: ""},
		{Name: "EmptyGroup", Pattern: ""},
		{Name: "Default", Pattern: ""},
	}

	result := GroupWindowsWithOptions(windows, groups, false)
	for _, g := range result {
		if g.Name == "EmptyGroup" {
			t.Fatal("expected EmptyGroup hidden when includeEmpty=false")
		}
	}
}

func TestGroupWindowsWithOptionsShowsEmptyGroups(t *testing.T) {
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "Active"},
	}
	groups := []config.Group{
		{Name: "Active", Pattern: ""},
		{Name: "EmptyGroup", Pattern: ""},
		{Name: "Default", Pattern: ""},
	}

	result := GroupWindowsWithOptions(windows, groups, true)
	found := false
	for _, g := range result {
		if g.Name == "EmptyGroup" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected EmptyGroup present when includeEmpty=true")
	}
}

func TestShowEmptyGroupsDefaultIsFalse(t *testing.T) {
	windows := []tmux.Window{}
	groups := []config.Group{
		{Name: "Orphan", Pattern: ""},
		{Name: "Default", Pattern: ""},
	}
	result := GroupWindows(windows, groups)
	for _, g := range result {
		if len(g.Windows) == 0 && g.Name != "Default" {
			t.Fatalf("empty group %q shown by default — should be hidden", g.Name)
		}
	}
}
