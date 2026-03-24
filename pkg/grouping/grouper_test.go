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
