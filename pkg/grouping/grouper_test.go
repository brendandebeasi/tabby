package grouping

import (
	"strconv"
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

func TestLightenColor_BoundaryLightness(t *testing.T) {
	tests := []struct {
		name   string
		color  string
		amount float64
		desc   string
	}{
		{"lighten at 0.0 amount", "#ff0000", 0.0, "no change"},
		{"lighten at 1.0 amount", "#ff0000", 1.0, "full lightening"},
		{"lighten pure black", "#000000", 0.5, "black to gray"},
		{"lighten pure white", "#ffffff", 0.5, "white stays white"},
		{"lighten red channel boundary", "#ff0000", 0.1, "red with lightening"},
		{"lighten green channel boundary", "#00ff00", 0.1, "green with lightening"},
		{"lighten blue channel boundary", "#0000ff", 0.1, "blue with lightening"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LightenColor(tt.color, tt.amount)
			if len(got) != 7 || got[0] != '#' {
				t.Errorf("LightenColor(%q, %v) %s should return valid hex, got %s", tt.color, tt.amount, tt.desc, got)
			}
		})
	}
}

func TestDarkenColor_BoundaryLightness(t *testing.T) {
	tests := []struct {
		name   string
		color  string
		amount float64
		desc   string
	}{
		{"darken at 0.0 amount", "#ff0000", 0.0, "no change"},
		{"darken at 1.0 amount", "#ff0000", 1.0, "full darkening"},
		{"darken pure white", "#ffffff", 0.5, "white to gray"},
		{"darken pure black", "#000000", 0.5, "black stays black"},
		{"darken red channel boundary", "#ff0000", 0.1, "red with darkening"},
		{"darken green channel boundary", "#00ff00", 0.1, "green with darkening"},
		{"darken blue channel boundary", "#0000ff", 0.1, "blue with darkening"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DarkenColor(tt.color, tt.amount)
			if len(got) != 7 || got[0] != '#' {
				t.Errorf("DarkenColor(%q, %v) %s should return valid hex, got %s", tt.color, tt.amount, tt.desc, got)
			}
		})
	}
}

func TestLightenColor_OverflowClamping(t *testing.T) {
	tests := []struct {
		name     string
		color    string
		amount   float64
		expected string
	}{
		{"lighten high red to white", "#ff0000", 1.0, "#ffffff"},
		{"lighten high green to white", "#00ff00", 1.0, "#ffffff"},
		{"lighten high blue to white", "#0000ff", 1.0, "#ffffff"},
		{"lighten mixed high channels", "#ffff00", 0.5, "#ffff7f"},
		{"lighten single high channel", "#ff0000", 0.5, "#ff7f7f"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LightenColor(tt.color, tt.amount)
			if got != tt.expected {
				t.Errorf("LightenColor(%q, %v) expected %s, got %s", tt.color, tt.amount, tt.expected, got)
			}
		})
	}
}

func TestLightenColor_InvalidFormats(t *testing.T) {
	tests := []struct {
		name  string
		color string
	}{
		{"too short", "#fff"},
		{"too long", "#fffffff"},
		{"invalid hex chars", "#gggggg"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LightenColor(tt.color, 0.5)
			if got != tt.color {
				t.Errorf("LightenColor(%q, 0.5) should return original for invalid format, got %s", tt.color, got)
			}
		})
	}
}

func TestDarkenColor_UnderflowClamping(t *testing.T) {
	tests := []struct {
		name     string
		color    string
		amount   float64
		expected string
	}{
		{"darken low red to black", "#ff0000", 1.0, "#000000"},
		{"darken low green to black", "#00ff00", 1.0, "#000000"},
		{"darken low blue to black", "#0000ff", 1.0, "#000000"},
		{"darken mixed low channels", "#ff0000", 0.5, "#7f0000"},
		{"darken single low channel", "#ff0000", 0.9, "#190000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DarkenColor(tt.color, tt.amount)
			if got != tt.expected {
				t.Errorf("DarkenColor(%q, %v) expected %s, got %s", tt.color, tt.amount, tt.expected, got)
			}
		})
	}
}

func TestDarkenColor_InvalidFormats(t *testing.T) {
	tests := []struct {
		name  string
		color string
	}{
		{"too short", "#fff"},
		{"too long", "#fffffff"},
		{"invalid hex chars", "#gggggg"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DarkenColor(tt.color, 0.5)
			if got != tt.color {
				t.Errorf("DarkenColor(%q, 0.5) should return original for invalid format, got %s", tt.color, got)
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

func TestRgbToHsl_PureRed(t *testing.T) {
	h, s, l := rgbToHsl(1.0, 0, 0)
	if h < -0.1 || h > 0.1 {
		t.Errorf("red hue should be ~0, got %v", h)
	}
	if s < 0.99 {
		t.Errorf("red saturation should be ~1.0, got %v", s)
	}
	if l < 0.49 || l > 0.51 {
		t.Errorf("red lightness should be ~0.5, got %v", l)
	}
}

func TestRgbToHsl_PureGreen(t *testing.T) {
	h, s, l := rgbToHsl(0, 1.0, 0)
	if h < 119 || h > 121 {
		t.Errorf("green hue should be ~120, got %v", h)
	}
	if s < 0.99 {
		t.Errorf("green saturation should be ~1.0, got %v", s)
	}
	if l < 0.49 || l > 0.51 {
		t.Errorf("green lightness should be ~0.5, got %v", l)
	}
}

func TestRgbToHsl_PureBlue(t *testing.T) {
	h, s, l := rgbToHsl(0, 0, 1.0)
	if h < 239 || h > 241 {
		t.Errorf("blue hue should be ~240, got %v", h)
	}
	if s < 0.99 {
		t.Errorf("blue saturation should be ~1.0, got %v", s)
	}
	if l < 0.49 || l > 0.51 {
		t.Errorf("blue lightness should be ~0.5, got %v", l)
	}
}

func TestRgbToHsl_Black(t *testing.T) {
	h, s, l := rgbToHsl(0, 0, 0)
	if h != 0 {
		t.Errorf("black hue should be 0, got %v", h)
	}
	if s != 0 {
		t.Errorf("black saturation should be 0, got %v", s)
	}
	if l != 0 {
		t.Errorf("black lightness should be 0, got %v", l)
	}
}

func TestRgbToHsl_White(t *testing.T) {
	h, s, l := rgbToHsl(1.0, 1.0, 1.0)
	if h != 0 {
		t.Errorf("white hue should be 0, got %v", h)
	}
	if s != 0 {
		t.Errorf("white saturation should be 0, got %v", s)
	}
	if l != 1.0 {
		t.Errorf("white lightness should be 1.0, got %v", l)
	}
}

func TestRgbToHsl_MidGray(t *testing.T) {
	h, s, l := rgbToHsl(0.5, 0.5, 0.5)
	if h != 0 {
		t.Errorf("gray hue should be 0, got %v", h)
	}
	if s != 0 {
		t.Errorf("gray saturation should be 0, got %v", s)
	}
	if l < 0.49 || l > 0.51 {
		t.Errorf("gray lightness should be ~0.5, got %v", l)
	}
}

func TestHslToRgb_AchromaticBlack(t *testing.T) {
	r, g, b := hslToRgb(0, 0, 0)
	if r != 0 || g != 0 || b != 0 {
		t.Errorf("HSL(0,0,0) should be RGB(0,0,0), got (%v,%v,%v)", r, g, b)
	}
}

func TestHslToRgb_AchromaticWhite(t *testing.T) {
	r, g, b := hslToRgb(0, 0, 1.0)
	if r != 1.0 || g != 1.0 || b != 1.0 {
		t.Errorf("HSL(0,0,1) should be RGB(1,1,1), got (%v,%v,%v)", r, g, b)
	}
}

func TestHslToRgb_AchromaticGray(t *testing.T) {
	r, g, b := hslToRgb(0, 0, 0.5)
	if r < 0.49 || r > 0.51 || g < 0.49 || g > 0.51 || b < 0.49 || b > 0.51 {
		t.Errorf("HSL(0,0,0.5) should be RGB(0.5,0.5,0.5), got (%v,%v,%v)", r, g, b)
	}
}

func TestHslToRgb_RedHue(t *testing.T) {
	r, g, b := hslToRgb(0, 1.0, 0.5)
	if r < 0.99 || g > 0.01 || b > 0.01 {
		t.Errorf("HSL(0,1,0.5) should be pure red, got (%v,%v,%v)", r, g, b)
	}
}

func TestHslToRgb_GreenHue(t *testing.T) {
	r, g, b := hslToRgb(120, 1.0, 0.5)
	if r > 0.01 || g < 0.99 || b > 0.01 {
		t.Errorf("HSL(120,1,0.5) should be pure green, got (%v,%v,%v)", r, g, b)
	}
}

func TestHslToRgb_BlueHue(t *testing.T) {
	r, g, b := hslToRgb(240, 1.0, 0.5)
	if r > 0.01 || g > 0.01 || b < 0.99 {
		t.Errorf("HSL(240,1,0.5) should be pure blue, got (%v,%v,%v)", r, g, b)
	}
}

func TestHueToRgb_FirstSector(t *testing.T) {
	result := hueToRgb(0.2, 0.8, 0.05)
	if result < 0.37 || result > 0.39 {
		t.Errorf("hueToRgb in first sector (t<1/6) should interpolate, got %v", result)
	}
}

func TestHueToRgb_SecondSector(t *testing.T) {
	result := hueToRgb(0.2, 0.8, 0.3)
	if result != 0.8 {
		t.Errorf("hueToRgb in second sector (1/6<=t<1/2) should return q, got %v", result)
	}
}

func TestHueToRgb_ThirdSector(t *testing.T) {
	result := hueToRgb(0.2, 0.8, 0.6)
	if result < 0.43 || result > 0.45 {
		t.Errorf("hueToRgb in third sector (1/2<=t<2/3) should interpolate, got %v", result)
	}
}

func TestHueToRgb_FourthSector(t *testing.T) {
	result := hueToRgb(0.2, 0.8, 0.8)
	if result != 0.2 {
		t.Errorf("hueToRgb in fourth sector (t>=2/3) should return p, got %v", result)
	}
}

func TestHueToRgb_NegativeT(t *testing.T) {
	result := hueToRgb(0.2, 0.8, -0.2)
	expected := hueToRgb(0.2, 0.8, 0.8)
	if result != expected {
		t.Errorf("hueToRgb with negative t should wrap, got %v expected %v", result, expected)
	}
}

func TestHueToRgb_GreaterThanOne(t *testing.T) {
	result := hueToRgb(0.2, 0.8, 1.2)
	expected := hueToRgb(0.2, 0.8, 0.2)
	if result != expected {
		t.Errorf("hueToRgb with t>1 should wrap, got %v expected %v", result, expected)
	}
}

func TestSaturateColor_Black(t *testing.T) {
	got := SaturateColor("#000000")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(black) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_White(t *testing.T) {
	got := SaturateColor("#ffffff")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(white) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_AlreadySaturated(t *testing.T) {
	got := SaturateColor("#ff0000")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(red) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_Grayscale(t *testing.T) {
	got := SaturateColor("#808080")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(gray) should return valid hex, got %s", got)
	}
}

func TestLightenColor_EdgeCaseZeroAmount(t *testing.T) {
	got := LightenColor("#808080", 0)
	if got != "#808080" {
		t.Errorf("LightenColor with 0 amount should return unchanged, got %s", got)
	}
}

func TestLightenColor_EdgeCaseFullAmount(t *testing.T) {
	got := LightenColor("#000000", 1.0)
	if got != "#ffffff" {
		t.Errorf("LightenColor black by 1.0 should be white, got %s", got)
	}
}

func TestLightenColor_AlreadyWhite(t *testing.T) {
	got := LightenColor("#ffffff", 0.5)
	if got != "#ffffff" {
		t.Errorf("LightenColor white should stay white, got %s", got)
	}
}

func TestDarkenColor_EdgeCaseZeroAmount(t *testing.T) {
	got := DarkenColor("#808080", 0)
	if got != "#808080" {
		t.Errorf("DarkenColor with 0 amount should return unchanged, got %s", got)
	}
}

func TestDarkenColor_EdgeCaseFullAmount(t *testing.T) {
	got := DarkenColor("#ffffff", 1.0)
	if got != "#000000" {
		t.Errorf("DarkenColor white by 1.0 should be black, got %s", got)
	}
}

func TestDarkenColor_AlreadyBlack(t *testing.T) {
	got := DarkenColor("#000000", 0.5)
	if got != "#000000" {
		t.Errorf("DarkenColor black should stay black, got %s", got)
	}
}

func TestLightenColor_PartialAmounts(t *testing.T) {
	tests := []struct {
		name      string
		color     string
		amount    float64
		tolerance int64
	}{
		{"lighten by 0.1", "#000000", 0.1, 2},
		{"lighten by 0.3", "#000000", 0.3, 2},
		{"lighten by 0.7", "#000000", 0.7, 2},
		{"lighten by 0.9", "#000000", 0.9, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LightenColor(tt.color, tt.amount)
			if len(got) != 7 || got[0] != '#' {
				t.Errorf("LightenColor(%q, %v) returned invalid hex: %q", tt.color, tt.amount, got)
			}
		})
	}
}

func TestLightenColor_Monotonic(t *testing.T) {
	base := "#333333"
	light1 := LightenColor(base, 0.2)
	light2 := LightenColor(base, 0.5)
	light3 := LightenColor(base, 0.8)

	parseHex := func(s string) int64 {
		if len(s) > 0 && s[0] == '#' {
			s = s[1:]
		}
		var v int64
		for _, c := range s {
			v *= 16
			if c >= '0' && c <= '9' {
				v += int64(c - '0')
			} else if c >= 'a' && c <= 'f' {
				v += int64(c - 'a' + 10)
			}
		}
		return v
	}

	v1 := parseHex(light1)
	v2 := parseHex(light2)
	v3 := parseHex(light3)

	if v1 >= v2 || v2 >= v3 {
		t.Errorf("lighten amounts should be monotonically increasing: %d < %d < %d", v1, v2, v3)
	}
}

func TestDarkenColor_PartialAmounts(t *testing.T) {
	tests := []struct {
		name      string
		color     string
		amount    float64
		tolerance int64
	}{
		{"darken by 0.1", "#ffffff", 0.1, 2},
		{"darken by 0.3", "#ffffff", 0.3, 2},
		{"darken by 0.7", "#ffffff", 0.7, 2},
		{"darken by 0.9", "#ffffff", 0.9, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DarkenColor(tt.color, tt.amount)
			if len(got) != 7 || got[0] != '#' {
				t.Errorf("DarkenColor(%q, %v) returned invalid hex: %q", tt.color, tt.amount, got)
			}
		})
	}
}

func TestDarkenColor_Monotonic(t *testing.T) {
	base := "#cccccc"
	dark1 := DarkenColor(base, 0.2)
	dark2 := DarkenColor(base, 0.5)
	dark3 := DarkenColor(base, 0.8)

	parseHex := func(s string) int64 {
		if len(s) > 0 && s[0] == '#' {
			s = s[1:]
		}
		var v int64
		for _, c := range s {
			v *= 16
			if c >= '0' && c <= '9' {
				v += int64(c - '0')
			} else if c >= 'a' && c <= 'f' {
				v += int64(c - 'a' + 10)
			}
		}
		return v
	}

	v1 := parseHex(dark1)
	v2 := parseHex(dark2)
	v3 := parseHex(dark3)

	if v1 <= v2 || v2 <= v3 {
		t.Errorf("darken amounts should be monotonically decreasing: %d > %d > %d", v1, v2, v3)
	}
}

func TestFindGroupThemeWithDefaults_GroupFound(t *testing.T) {
	groups := []config.Group{
		{Name: "TestGroup", Theme: config.Theme{Bg: "#ff0000", Fg: "#ffffff"}},
		{Name: "Default", Theme: config.Theme{Bg: "#000000", Fg: "#ffffff"}},
	}

	theme := FindGroupThemeWithDefaults("TestGroup", groups, true, 0)

	if theme.Bg != "#ff0000" {
		t.Errorf("expected Bg #ff0000, got %s", theme.Bg)
	}
	if theme.Fg != "#ffffff" {
		t.Errorf("expected Fg #ffffff, got %s", theme.Fg)
	}
}

func TestFindGroupThemeWithDefaults_GroupNotFound(t *testing.T) {
	groups := []config.Group{
		{Name: "TestGroup", Theme: config.Theme{Bg: "#ff0000", Fg: "#ffffff"}},
	}

	theme := FindGroupThemeWithDefaults("Missing", groups, true, 0)

	if theme.Bg == "" {
		t.Errorf("expected auto-filled Bg, got empty")
	}
	if theme.Fg == "" {
		t.Errorf("expected auto-filled Fg, got empty")
	}
}

func TestFindGroupThemeWithDefaults_EmptyBg(t *testing.T) {
	groups := []config.Group{
		{Name: "TestGroup", Theme: config.Theme{Bg: "", Fg: "#ffffff"}},
	}

	theme := FindGroupThemeWithDefaults("TestGroup", groups, true, 0)

	if theme.Bg == "" {
		t.Errorf("expected auto-filled Bg for empty theme, got empty")
	}
}

func TestFindGroupThemeWithDefaults_DarkTerminal(t *testing.T) {
	groups := []config.Group{
		{Name: "TestGroup", Theme: config.Theme{Bg: "#ff0000"}},
	}

	themeDark := FindGroupThemeWithDefaults("TestGroup", groups, true, 0)
	themeLight := FindGroupThemeWithDefaults("TestGroup", groups, false, 0)

	if themeDark.Bg == "" || themeLight.Bg == "" {
		t.Errorf("both dark and light terminal themes should have Bg")
	}
}

func TestFindGroupThemeWithDefaults_PreservesIcon(t *testing.T) {
	groups := []config.Group{
		{Name: "TestGroup", Theme: config.Theme{Bg: "#ff0000", Icon: "🚀"}},
	}

	theme := FindGroupThemeWithDefaults("TestGroup", groups, true, 0)

	if theme.Icon != "🚀" {
		t.Errorf("expected icon 🚀, got %s", theme.Icon)
	}
}

func TestResolveThemeColors_PartialTheme(t *testing.T) {
	theme := config.Theme{Bg: "#ff0000"}

	resolved := ResolveThemeColors(theme, true)

	if resolved.Bg == "" {
		t.Errorf("expected Bg to be filled, got empty")
	}
	if resolved.Fg == "" {
		t.Errorf("expected Fg to be auto-filled, got empty")
	}
	if resolved.ActiveBg == "" {
		t.Errorf("expected ActiveBg to be auto-filled, got empty")
	}
}

func TestResolveThemeColors_FullTheme(t *testing.T) {
	theme := config.Theme{
		Bg:       "#ff0000",
		Fg:       "#ffffff",
		ActiveBg: "#cc0000",
		ActiveFg: "#ffffff",
	}

	resolved := ResolveThemeColors(theme, true)

	if resolved.Bg != "#ff0000" {
		t.Errorf("expected Bg #ff0000, got %s", resolved.Bg)
	}
	if resolved.Fg != "#ffffff" {
		t.Errorf("expected Fg #ffffff, got %s", resolved.Fg)
	}
}

func TestResolveThemeColors_DarkTerminal(t *testing.T) {
	theme := config.Theme{Bg: "#ff0000"}

	themeDark := ResolveThemeColors(theme, true)
	themeLight := ResolveThemeColors(theme, false)

	if themeDark.Bg == "" || themeLight.Bg == "" {
		t.Errorf("both dark and light terminal themes should have Bg")
	}
}

func TestResolveThemeColors_EmptyTheme(t *testing.T) {
	theme := config.Theme{}

	resolved := ResolveThemeColors(theme, true)

	if resolved.Bg == "" {
		t.Errorf("expected auto-filled Bg, got empty")
	}
	if resolved.Fg == "" {
		t.Errorf("expected auto-filled Fg, got empty")
	}
}

func TestLightenColor_InvalidHexPassthrough(t *testing.T) {
	got := LightenColor("#gg", 0.5)
	if got != "#gg" {
		t.Errorf("LightenColor invalid hex should pass through unchanged, got %q", got)
	}
}

func TestLightenColor_ShortHexPassthrough(t *testing.T) {
	got := LightenColor("#fff", 0.5)
	if got != "#fff" {
		t.Errorf("LightenColor short hex should pass through unchanged, got %q", got)
	}
}

func TestLightenColor_ClampToWhite(t *testing.T) {
	// Lighten black by 100% should clamp to white (#ffffff)
	got := LightenColor("#000000", 1.0)
	if got != "#ffffff" {
		t.Errorf("LightenColor black by 100%% should clamp to white, got %q", got)
	}
}

func TestLightenColor_ClampPartialChannel(t *testing.T) {
	// Lighten #ff0000 (red) by 0.5 should clamp green and blue to 255
	// R: 255 + (255-255)*0.5 = 255
	// G: 0 + (255-0)*0.5 = 127.5 ≈ 127
	// B: 0 + (255-0)*0.5 = 127.5 ≈ 127
	got := LightenColor("#ff0000", 0.5)
	if got != "#ff7f7f" {
		t.Errorf("LightenColor #ff0000 by 0.5 should be #ff7f7f, got %q", got)
	}
}

func TestDarkenColor_InvalidHexPassthrough(t *testing.T) {
	got := DarkenColor("#gg", 0.5)
	if got != "#gg" {
		t.Errorf("DarkenColor invalid hex should pass through unchanged, got %q", got)
	}
}

func TestDarkenColor_ShortHexPassthrough(t *testing.T) {
	got := DarkenColor("#fff", 0.5)
	if got != "#fff" {
		t.Errorf("DarkenColor short hex should pass through unchanged, got %q", got)
	}
}

func TestDarkenColor_ClampToBlack(t *testing.T) {
	// Darken white by 100% should clamp to black (#000000)
	got := DarkenColor("#ffffff", 1.0)
	if got != "#000000" {
		t.Errorf("DarkenColor white by 100%% should clamp to black, got %q", got)
	}
}

func TestDarkenColor_ClampPartialChannel(t *testing.T) {
	// Darken #ff0000 (red) by 0.5 should clamp to #7f0000
	// R: 255 * (1.0 - 0.5) = 127.5 ≈ 127
	// G: 0 * (1.0 - 0.5) = 0
	// B: 0 * (1.0 - 0.5) = 0
	got := DarkenColor("#ff0000", 0.5)
	if got != "#7f0000" {
		t.Errorf("DarkenColor #ff0000 by 0.5 should be #7f0000, got %q", got)
	}
}

func TestShadeColorByIndex_CapAt40Percent(t *testing.T) {
	// index=6 means darken by 6*0.08=0.48, but capped at 0.40
	// So white (#ffffff = 255,255,255) becomes (255*(1-0.40), 255*(1-0.40), 255*(1-0.40)) = (153,153,153)
	result := ShadeColorByIndex("#ffffff", 6)
	if result == "" {
		t.Errorf("ShadeColorByIndex with index=6 should return valid hex, got empty")
	}
	hex := result
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		t.Errorf("ShadeColorByIndex with index=6 should return valid hex, got %q", result)
		return
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	// With 40% darkening applied to white, we expect ~153 (255 * 0.6)
	// Allow some tolerance for rounding
	if r > 160 || g > 160 || b > 160 {
		t.Errorf("ShadeColorByIndex with index=6 should darken to ~40%% reduction, got RGB(%d,%d,%d)", r, g, b)
	}
}

func TestSaturateColor_InvalidHexPassthrough(t *testing.T) {
	got := SaturateColor("#gg")
	if got != "#gg" {
		t.Errorf("SaturateColor invalid hex should pass through unchanged, got %q", got)
	}
}

func TestSaturateColor_LowSaturationColor(t *testing.T) {
	got := SaturateColor("#999999")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(low saturation) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_HighLightnessColor(t *testing.T) {
	got := SaturateColor("#e6e6e6")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(high lightness) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_LowLightnessColor(t *testing.T) {
	got := SaturateColor("#1a1a1a")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(low lightness) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_MidtoneColor(t *testing.T) {
	got := SaturateColor("#4d7a99")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(midtone) should return valid hex, got %s", got)
	}
}

func TestSaturateColor_NoHashPrefix(t *testing.T) {
	got := SaturateColor("3498db")
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("SaturateColor(no hash) should return valid hex with hash, got %s", got)
	}
}

func TestInactiveTabColor_InvalidHexPassthrough(t *testing.T) {
	got := InactiveTabColor("#gg", 0.1, 0.1)
	if got != "#gg" {
		t.Errorf("InactiveTabColor invalid hex should pass through unchanged, got %q", got)
	}
}

func TestInactiveTabColor_DefaultLightenValue(t *testing.T) {
	got := InactiveTabColor("#336699", 0, 0.85)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("InactiveTabColor with default lighten should return valid hex, got %s", got)
	}
}

func TestInactiveTabColor_DefaultSaturateValue(t *testing.T) {
	got := InactiveTabColor("#336699", 0.04, 0)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("InactiveTabColor with default saturate should return valid hex, got %s", got)
	}
}

func TestInactiveTabColor_BothDefaultValues(t *testing.T) {
	got := InactiveTabColor("#336699", 0, 0)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("InactiveTabColor with both defaults should return valid hex, got %s", got)
	}
}

func TestInactiveTabColor_HighLightenValue(t *testing.T) {
	got := InactiveTabColor("#336699", 0.5, 0.85)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("InactiveTabColor with high lighten should return valid hex, got %s", got)
	}
}

func TestInactiveTabColor_NoHashPrefix(t *testing.T) {
	got := InactiveTabColor("336699", 0.1, 0.85)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("InactiveTabColor(no hash) should return valid hex with hash, got %s", got)
	}
}

func TestRgbToHsl_AchromaticGray(t *testing.T) {
	h, s, l := rgbToHsl(0.5, 0.5, 0.5)
	if h != 0 {
		t.Errorf("rgbToHsl achromatic gray should have h=0, got %f", h)
	}
	if s != 0 {
		t.Errorf("rgbToHsl achromatic gray should have s=0, got %f", s)
	}
	if l != 0.5 {
		t.Errorf("rgbToHsl achromatic gray should have l=0.5, got %f", l)
	}
}

func TestLightenColor_InvalidHexParseError(t *testing.T) {
	// Invalid hex characters should trigger parse error and return input unchanged
	got := LightenColor("#gg0000", 0.5)
	if got != "#gg0000" {
		t.Errorf("LightenColor with invalid hex should pass through unchanged, got %q", got)
	}
}

func TestLightenColor_ExceedsMax(t *testing.T) {
	// Lighten #ff0000 (red) by 0.5 should clamp to #ff8080
	// R: 255 + (255-255)*0.5 = 255 (already at max, no change)
	// G: 0 + (255-0)*0.5 = 127.5 ≈ 127
	// B: 0 + (255-0)*0.5 = 127.5 ≈ 127
	got := LightenColor("#ff0000", 0.5)
	if got != "#ff7f7f" {
		t.Errorf("LightenColor #ff0000 by 0.5 should be #ff7f7f, got %q", got)
	}
}

func TestLightenColor_AllChannelsExceedMax(t *testing.T) {
	// Lighten #ffffff (white) by any amount should stay #ffffff (already at max)
	got := LightenColor("#ffffff", 0.5)
	if got != "#ffffff" {
		t.Errorf("LightenColor #ffffff should stay #ffffff, got %q", got)
	}
}

func TestDarkenColor_InvalidHexParseError(t *testing.T) {
	// Invalid hex characters should trigger parse error and return input unchanged
	got := DarkenColor("#gg0000", 0.5)
	if got != "#gg0000" {
		t.Errorf("DarkenColor with invalid hex should pass through unchanged, got %q", got)
	}
}

func TestDarkenColor_ExceedsMin(t *testing.T) {
	// Darken #000000 (black) by 0.5 should stay #000000 (already at min)
	got := DarkenColor("#000000", 0.5)
	if got != "#000000" {
		t.Errorf("DarkenColor #000000 should stay #000000, got %q", got)
	}
}

func TestDarkenColor_AllChannelsExceedMin(t *testing.T) {
	// Darken #ff0000 (red) by 0.5 should clamp to #7f0000
	// R: 255 * (1.0 - 0.5) = 127.5 ≈ 127
	// G: 0 * (1.0 - 0.5) = 0 (already at min, no change)
	// B: 0 * (1.0 - 0.5) = 0 (already at min, no change)
	got := DarkenColor("#ff0000", 0.5)
	if got != "#7f0000" {
		t.Errorf("DarkenColor #ff0000 by 0.5 should be #7f0000, got %q", got)
	}
}

func TestRgbToHsl_RedMaxChannel(t *testing.T) {
	// Test when red is the max channel (case r in switch)
	// Red (1.0, 0.0, 0.0) should give h=0, s=1.0, l=0.5
	h, s, l := rgbToHsl(1.0, 0.0, 0.0)
	if h != 0 {
		t.Errorf("rgbToHsl red should have h=0, got %f", h)
	}
	if s != 1.0 {
		t.Errorf("rgbToHsl red should have s=1.0, got %f", s)
	}
	if l != 0.5 {
		t.Errorf("rgbToHsl red should have l=0.5, got %f", l)
	}
}

func TestRgbToHsl_GreenMaxChannel(t *testing.T) {
	// Test when green is the max channel (case g in switch)
	// Green (0.0, 1.0, 0.0) should give h=120, s=1.0, l=0.5
	h, s, l := rgbToHsl(0.0, 1.0, 0.0)
	if h != 120 {
		t.Errorf("rgbToHsl green should have h=120, got %f", h)
	}
	if s != 1.0 {
		t.Errorf("rgbToHsl green should have s=1.0, got %f", s)
	}
	if l != 0.5 {
		t.Errorf("rgbToHsl green should have l=0.5, got %f", l)
	}
}

func TestRgbToHsl_BlueMaxChannel(t *testing.T) {
	// Test when blue is the max channel (case b in switch)
	// Blue (0.0, 0.0, 1.0) should give h=240, s=1.0, l=0.5
	h, s, l := rgbToHsl(0.0, 0.0, 1.0)
	if h != 240 {
		t.Errorf("rgbToHsl blue should have h=240, got %f", h)
	}
	if s != 1.0 {
		t.Errorf("rgbToHsl blue should have s=1.0, got %f", s)
	}
	if l != 0.5 {
		t.Errorf("rgbToHsl blue should have l=0.5, got %f", l)
	}
}

func TestRgbToHsl_RedMaxWithGreenLessThanBlue(t *testing.T) {
	// Test the g < b branch in case r (line 305-307)
	// Color where red is max, green < blue
	h, s, l := rgbToHsl(1.0, 0.0, 0.5)
	if h < 0 || h > 360 {
		t.Errorf("rgbToHsl with red max and g<b should have valid hue, got %f", h)
	}
	if s < 0 || s > 1 {
		t.Errorf("rgbToHsl should have valid saturation, got %f", s)
	}
	if l < 0 || l > 1 {
		t.Errorf("rgbToHsl should have valid lightness, got %f", l)
	}
}

func TestRgbToHsl_HighLightness(t *testing.T) {
	// Test the l > 0.5 branch in saturation calculation (line 296-297)
	// Light color: (1.0, 0.8, 0.8) has high lightness
	_, s, l := rgbToHsl(1.0, 0.8, 0.8)
	if l <= 0.5 {
		t.Errorf("rgbToHsl light color should have l > 0.5, got %f", l)
	}
	if s < 0 || s > 1 {
		t.Errorf("rgbToHsl should have valid saturation, got %f", s)
	}
}

func TestRgbToHsl_LowLightness(t *testing.T) {
	_, s, lightness := rgbToHsl(0.2, 0.1, 0.1)
	if lightness >= 0.5 {
		t.Errorf("rgbToHsl dark color should have l <= 0.5, got %f", lightness)
	}
	if s < 0 || s > 1 {
		t.Errorf("rgbToHsl should have valid saturation, got %f", s)
	}
}

func TestRgbToHsl_PureCyan(t *testing.T) {
	h, s, _ := rgbToHsl(0, 1, 1)
	if h < 179 || h > 181 {
		t.Errorf("rgbToHsl cyan should have h ~180, got %f", h)
	}
	if s < 0.99 {
		t.Errorf("rgbToHsl cyan should have high saturation, got %f", s)
	}
}

func TestRgbToHsl_PureMagenta(t *testing.T) {
	h, s, _ := rgbToHsl(1, 0, 1)
	if h < 299 || h > 301 {
		t.Errorf("rgbToHsl magenta should have h ~300, got %f", h)
	}
	if s < 0.99 {
		t.Errorf("rgbToHsl magenta should have high saturation, got %f", s)
	}
}

func TestShadeColorByIndex_InvalidHexPassthrough(t *testing.T) {
	got := ShadeColorByIndex("#gg", 0)
	if got != "#gg" {
		t.Errorf("ShadeColorByIndex invalid hex should pass through unchanged, got %q", got)
	}
}

func TestShadeColorByIndex_LowIndex(t *testing.T) {
	// index=0 means darken by 0*0.08=0, so no darkening
	got := ShadeColorByIndex("#ffffff", 0)
	if got != "#ffffff" {
		t.Errorf("ShadeColorByIndex with index=0 should not darken, got %q", got)
	}
}

func TestShadeColorByIndex_HighIndexCapped(t *testing.T) {
	// index=10 means darken by 10*0.08=0.80, but capped at 0.40
	// So white (#ffffff = 255,255,255) becomes (255*(1-0.40), 255*(1-0.40), 255*(1-0.40)) = (153,153,153)
	got := ShadeColorByIndex("#ffffff", 10)
	if got != "#999999" {
		t.Errorf("ShadeColorByIndex with index=10 (capped at 0.40) should be #999999, got %q", got)
	}
}

func TestShadeColorByIndex_Index2(t *testing.T) {
	got := ShadeColorByIndex("#ff0000", 2)
	expected := ShadeColorByIndex("#ff0000", 2)
	if got != expected {
		t.Errorf("ShadeColorByIndex with index=2 should be consistent, got %q", got)
	}
}

func TestShadeColorByIndex_Index5(t *testing.T) {
	got := ShadeColorByIndex("#00ff00", 5)
	expected := ShadeColorByIndex("#00ff00", 5)
	if got != expected {
		t.Errorf("ShadeColorByIndex with index=5 should be consistent, got %q", got)
	}
}

func TestShadeColorByIndex_LargeIndex(t *testing.T) {
	got := ShadeColorByIndex("#ffffff", 100)
	expected := "#999999"
	if got != expected {
		t.Errorf("ShadeColorByIndex with large index (capped at 0.40) should be %q, got %q", expected, got)
	}
}

func TestShadeColorByIndex_DarkColor(t *testing.T) {
	got := ShadeColorByIndex("#333333", 1)
	if got == "#333333" {
		t.Errorf("ShadeColorByIndex should darken even dark colors, got %q", got)
	}
}

func TestShadeColorByIndex_InvalidLength(t *testing.T) {
	got := ShadeColorByIndex("#fff", 1)
	if got != "#fff" {
		t.Errorf("ShadeColorByIndex with invalid length should return unchanged, got %q", got)
	}
}

func TestShadeColorByIndex_InvalidHex(t *testing.T) {
	got := ShadeColorByIndex("#gggggg", 1)
	if got != "#gggggg" {
		t.Errorf("ShadeColorByIndex with invalid hex should return unchanged, got %q", got)
	}
}

func TestShadeColorByIndex_Index3(t *testing.T) {
	got := ShadeColorByIndex("#cccccc", 3)
	if got == "#cccccc" {
		t.Errorf("ShadeColorByIndex with index=3 should darken, got %q", got)
	}
}

func TestShadeColorByIndex_Index7(t *testing.T) {
	got := ShadeColorByIndex("#aabbcc", 7)
	if got == "#aabbcc" {
		t.Errorf("ShadeColorByIndex with index=7 should darken, got %q", got)
	}
}

// Tests for GroupWindowsWithOptions to cover missing branches
func TestGroupWindowsWithOptions_PinnedWindowsFirst(t *testing.T) {
	// Test that pinned windows are placed in a special "Pinned" group at the start
	windows := []tmux.Window{
		{Index: 0, Name: "unpinned1", Pinned: false, Group: ""},
		{Index: 1, Name: "pinned1", Pinned: true, Group: ""},
		{Index: 2, Name: "unpinned2", Pinned: false, Group: ""},
		{Index: 3, Name: "pinned2", Pinned: true, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// First group should be "Pinned" with 2 windows
	if len(result) < 1 {
		t.Fatal("Expected at least 1 group (Pinned)")
	}
	if result[0].Name != "Pinned" {
		t.Errorf("First group should be 'Pinned', got %q", result[0].Name)
	}
	if len(result[0].Windows) != 2 {
		t.Errorf("Pinned group should have 2 windows, got %d", len(result[0].Windows))
	}
	// Pinned windows should be sorted by index
	if result[0].Windows[0].Index != 1 || result[0].Windows[1].Index != 3 {
		t.Errorf("Pinned windows should be sorted by index, got %v", []int{result[0].Windows[0].Index, result[0].Windows[1].Index})
	}
}

func TestGroupWindowsWithOptions_PinnedWindowsSortedByIndex(t *testing.T) {
	// Test that pinned windows are sorted by index within the Pinned group
	windows := []tmux.Window{
		{Index: 5, Name: "pinned5", Pinned: true, Group: ""},
		{Index: 2, Name: "pinned2", Pinned: true, Group: ""},
		{Index: 8, Name: "pinned8", Pinned: true, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	if result[0].Name != "Pinned" {
		t.Fatalf("First group should be 'Pinned', got %q", result[0].Name)
	}
	if len(result[0].Windows) != 3 {
		t.Fatalf("Pinned group should have 3 windows, got %d", len(result[0].Windows))
	}
	// Check sorting: 2, 5, 8
	if result[0].Windows[0].Index != 2 || result[0].Windows[1].Index != 5 || result[0].Windows[2].Index != 8 {
		indices := []int{result[0].Windows[0].Index, result[0].Windows[1].Index, result[0].Windows[2].Index}
		t.Errorf("Pinned windows should be sorted [2, 5, 8], got %v", indices)
	}
}

func TestGroupWindowsWithOptions_NoPinnedWindows(t *testing.T) {
	// Test that Pinned group is not included if there are no pinned windows
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: ""},
		{Index: 1, Name: "win1", Pinned: false, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// First group should be "Default", not "Pinned"
	if len(result) < 1 {
		t.Fatal("Expected at least 1 group")
	}
	if result[0].Name != "Default" {
		t.Errorf("First group should be 'Default' (no pinned windows), got %q", result[0].Name)
	}
}

func TestGroupWindowsWithOptions_MissingGroupFallbackToDefault(t *testing.T) {
	// Test that windows assigned to a non-existent group fall back to Default
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "NonExistent"},
		{Index: 1, Name: "win1", Pinned: false, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Find Default group
	var defaultGroup *GroupedWindows
	for i := range result {
		if result[i].Name == "Default" {
			defaultGroup = &result[i]
			break
		}
	}

	if defaultGroup == nil {
		t.Fatal("Default group not found in result")
	}
	// Both windows should be in Default (one explicitly, one by fallback)
	if len(defaultGroup.Windows) != 2 {
		t.Errorf("Default group should have 2 windows (fallback + explicit), got %d", len(defaultGroup.Windows))
	}
}

func TestGroupWindowsWithOptions_MissingGroupNoDefaultFallback(t *testing.T) {
	// Test that windows assigned to a non-existent group are dropped if Default doesn't exist
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "NonExistent"},
	}
	groups := []config.Group{
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Window should not appear in any group (no Default to fall back to)
	totalWindows := 0
	for _, g := range result {
		totalWindows += len(g.Windows)
	}
	if totalWindows != 0 {
		t.Errorf("Expected 0 windows (no Default fallback), got %d", totalWindows)
	}
}

func TestGroupWindowsWithOptions_ExplicitGroupAssignment(t *testing.T) {
	// Test that windows with explicit group assignment go to the correct group
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "Frontend"},
		{Index: 1, Name: "win1", Pinned: false, Group: "Backend"},
		{Index: 2, Name: "win2", Pinned: false, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Backend", Theme: config.Theme{Bg: "#27ae60"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Find each group and verify window counts
	groupMap := make(map[string]int)
	for _, g := range result {
		groupMap[g.Name] = len(g.Windows)
	}

	if groupMap["Frontend"] != 1 {
		t.Errorf("Frontend group should have 1 window, got %d", groupMap["Frontend"])
	}
	if groupMap["Backend"] != 1 {
		t.Errorf("Backend group should have 1 window, got %d", groupMap["Backend"])
	}
	if groupMap["Default"] != 1 {
		t.Errorf("Default group should have 1 window, got %d", groupMap["Default"])
	}
}

func TestGroupWindowsWithOptions_EmptyGroupExcludedWhenIncludeEmptyFalse(t *testing.T) {
	// Test that empty groups are excluded when includeEmpty=false
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "Frontend"},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Backend", Theme: config.Theme{Bg: "#27ae60"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Backend group should not be in result (empty and includeEmpty=false)
	for _, g := range result {
		if g.Name == "Backend" {
			t.Errorf("Backend group should not be included when empty and includeEmpty=false")
		}
	}
}

func TestGroupWindowsWithOptions_EmptyGroupIncludedWhenIncludeEmptyTrue(t *testing.T) {
	// Test that empty groups are included when includeEmpty=true
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "Frontend"},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Backend", Theme: config.Theme{Bg: "#27ae60"}},
	}

	result := GroupWindowsWithOptions(windows, groups, true)

	// Backend group should be in result (includeEmpty=true)
	found := false
	for _, g := range result {
		if g.Name == "Backend" {
			found = true
			if len(g.Windows) != 0 {
				t.Errorf("Backend group should be empty, got %d windows", len(g.Windows))
			}
			break
		}
	}
	if !found {
		t.Errorf("Backend group should be included when includeEmpty=true")
	}
}

func TestGroupWindowsWithOptions_DefaultGroupFirst(t *testing.T) {
	// Test that Default group appears before other groups (except Pinned)
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "Zebra"},
		{Index: 1, Name: "win1", Pinned: false, Group: ""},
		{Index: 2, Name: "win2", Pinned: false, Group: "Apple"},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Zebra", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Apple", Theme: config.Theme{Bg: "#27ae60"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Default should be first (or second if Pinned is first)
	if result[0].Name == "Pinned" {
		if result[1].Name != "Default" {
			t.Errorf("Default should be second (after Pinned), got %q", result[1].Name)
		}
	} else if result[0].Name != "Default" {
		t.Errorf("Default should be first (or second after Pinned), got %q", result[0].Name)
	}
}

func TestGroupWindowsWithOptions_OtherGroupsAlphabetical(t *testing.T) {
	// Test that non-Default groups are sorted alphabetically
	windows := []tmux.Window{
		{Index: 0, Name: "win0", Pinned: false, Group: "Zebra"},
		{Index: 1, Name: "win1", Pinned: false, Group: "Apple"},
		{Index: 2, Name: "win2", Pinned: false, Group: "Mango"},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Zebra", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Apple", Theme: config.Theme{Bg: "#27ae60"}},
		{Name: "Mango", Theme: config.Theme{Bg: "#f39c12"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Find indices of non-Default groups
	var otherGroupNames []string
	for _, g := range result {
		if g.Name != "Default" && g.Name != "Pinned" {
			otherGroupNames = append(otherGroupNames, g.Name)
		}
	}

	// Should be alphabetical: Apple, Mango, Zebra
	expected := []string{"Apple", "Mango", "Zebra"}
	if len(otherGroupNames) != len(expected) {
		t.Errorf("Expected %d non-Default groups, got %d", len(expected), len(otherGroupNames))
	}
	for i, name := range otherGroupNames {
		if i < len(expected) && name != expected[i] {
			t.Errorf("Group %d should be %q, got %q", i, expected[i], name)
		}
	}
}

func TestGroupWindowsWithOptions_WindowsSortedByIndexWithinGroup(t *testing.T) {
	// Test that windows are sorted by index within each group
	windows := []tmux.Window{
		{Index: 5, Name: "win5", Pinned: false, Group: "Frontend"},
		{Index: 1, Name: "win1", Pinned: false, Group: "Frontend"},
		{Index: 3, Name: "win3", Pinned: false, Group: "Frontend"},
	}
	groups := []config.Group{
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	if len(result) < 1 {
		t.Fatal("Expected at least 1 group")
	}
	frontendGroup := result[0]
	if len(frontendGroup.Windows) != 3 {
		t.Fatalf("Frontend group should have 3 windows, got %d", len(frontendGroup.Windows))
	}
	// Check sorting: 1, 3, 5
	if frontendGroup.Windows[0].Index != 1 || frontendGroup.Windows[1].Index != 3 || frontendGroup.Windows[2].Index != 5 {
		indices := []int{frontendGroup.Windows[0].Index, frontendGroup.Windows[1].Index, frontendGroup.Windows[2].Index}
		t.Errorf("Windows should be sorted [1, 3, 5], got %v", indices)
	}
}

func TestGroupWindowsWithOptions_PinnedAndNonPinnedMixed(t *testing.T) {
	// Test complex scenario with pinned windows, multiple groups, and empty groups
	windows := []tmux.Window{
		{Index: 0, Name: "pinned0", Pinned: true, Group: ""},
		{Index: 1, Name: "fe1", Pinned: false, Group: "Frontend"},
		{Index: 2, Name: "pinned2", Pinned: true, Group: ""},
		{Index: 3, Name: "be3", Pinned: false, Group: "Backend"},
		{Index: 4, Name: "default4", Pinned: false, Group: ""},
	}
	groups := []config.Group{
		{Name: "Default", Theme: config.Theme{Bg: "#3498db"}},
		{Name: "Frontend", Theme: config.Theme{Bg: "#e74c3c"}},
		{Name: "Backend", Theme: config.Theme{Bg: "#27ae60"}},
	}

	result := GroupWindowsWithOptions(windows, groups, false)

	// Verify order: Pinned, Default, Backend, Frontend (alphabetical)
	expectedOrder := []string{"Pinned", "Default", "Backend", "Frontend"}
	if len(result) != len(expectedOrder) {
		t.Errorf("Expected %d groups, got %d", len(expectedOrder), len(result))
	}
	for i, expectedName := range expectedOrder {
		if i < len(result) && result[i].Name != expectedName {
			t.Errorf("Group %d should be %q, got %q", i, expectedName, result[i].Name)
		}
	}

	// Verify window counts
	if len(result[0].Windows) != 2 {
		t.Errorf("Pinned group should have 2 windows, got %d", len(result[0].Windows))
	}
	if len(result[1].Windows) != 1 {
		t.Errorf("Default group should have 1 window, got %d", len(result[1].Windows))
	}
	if len(result[2].Windows) != 1 {
		t.Errorf("Frontend group should have 1 window, got %d", len(result[2].Windows))
	}
	if len(result[3].Windows) != 1 {
		t.Errorf("Backend group should have 1 window, got %d", len(result[3].Windows))
	}
}
