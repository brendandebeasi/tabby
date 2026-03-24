package colors

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func isValidHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func TestDeriveThemeColors_ReturnsValidHex(t *testing.T) {
	bg, fg, activeBg, activeFg, inactiveBg, inactiveFg := DeriveThemeColors("#3498db", true)
	for _, color := range []string{bg, fg, activeBg, activeFg, inactiveBg, inactiveFg} {
		assert.True(t, isValidHex(color), "expected valid hex, got %q", color)
	}
}

func TestDeriveThemeColors_BaseColorIsBackground(t *testing.T) {
	bg, _, _, _, _, _ := DeriveThemeColors("#3498db", true)
	assert.Equal(t, "#3498db", bg)
}

func TestDeriveThemeColors_DarkVsLightTerminal(t *testing.T) {
	_, _, activeBgDark, _, _, _ := DeriveThemeColors("#3498db", true)
	_, _, activeBgLight, _, _, _ := DeriveThemeColors("#3498db", false)
	assert.NotEqual(t, activeBgDark, activeBgLight)
}

func TestDeriveActiveBg_ValidHex(t *testing.T) {
	got := DeriveActiveBg("#3498db", true)
	assert.True(t, isValidHex(got), "expected valid hex, got %q", got)
}

func TestDeriveActiveBg_InvalidColor(t *testing.T) {
	assert.Equal(t, "notahex", DeriveActiveBg("notahex", true))
}

func TestDeriveActiveBg_DifferentForDarkAndLight(t *testing.T) {
	dark := DeriveActiveBg("#9b59b6", true)
	light := DeriveActiveBg("#9b59b6", false)
	assert.NotEqual(t, dark, light)
}

func TestDeriveInactiveBg_ValidHex(t *testing.T) {
	got := DeriveInactiveBg("#2ecc71", false)
	assert.True(t, isValidHex(got), "expected valid hex, got %q", got)
}

func TestDeriveInactiveBg_InvalidColor(t *testing.T) {
	assert.Equal(t, "bad", DeriveInactiveBg("bad", false))
}

func TestHexToHSL_RoundTrip(t *testing.T) {
	tests := []string{"#ff0000", "#00ff00", "#0000ff", "#3498db", "#9b59b6"}
	for _, hex := range tests {
		h, s, l := hexToHSL(hex)
		assert.True(t, h >= 0, "hue should be non-negative for %q", hex)
		roundTripped := hslToHex(h, s, l)
		assert.True(t, isValidHex(roundTripped), "round-tripped hex should be valid for %q", hex)
	}
}

func TestHexToHSL_Invalid(t *testing.T) {
	h, s, l := hexToHSL("notvalid")
	assert.Equal(t, -1.0, h)
	assert.Equal(t, 0.0, s)
	assert.Equal(t, 0.0, l)
}

func TestHexToHSL_GrayIsAchromatic(t *testing.T) {
	_, s, _ := hexToHSL("#808080")
	assert.Equal(t, 0.0, s)
}

func TestGetDefaultGroupColor_IndexWraps(t *testing.T) {
	c0 := GetDefaultGroupColor(0)
	c12 := GetDefaultGroupColor(12)
	assert.Equal(t, c0, c12, "palette should wrap at 12")
}

func TestGetDefaultGroupColor_ReturnsValidHex(t *testing.T) {
	for i := 0; i < 12; i++ {
		got := GetDefaultGroupColor(i)
		assert.True(t, isValidHex(got), "index %d: expected valid hex, got %q", i, got)
	}
}

func TestSmartTextColor_WhiteOnDark(t *testing.T) {
	assert.Equal(t, "#ffffff", SmartTextColor("#000000", false))
}

func TestSmartTextColor_BlackOnLight(t *testing.T) {
	assert.Equal(t, "#000000", SmartTextColor("#ffffff", false))
}

func TestSmartTextColor_WarmOnDark(t *testing.T) {
	got := SmartTextColor("#000000", true)
	assert.True(t, strings.HasPrefix(got, "#"), "warm result should be a hex color")
}

func TestSmartTextColor_WarmOnLight(t *testing.T) {
	got := SmartTextColor("#ffffff", true)
	assert.True(t, strings.HasPrefix(got, "#"), "warm result should be a hex color")
}

func TestAutoFillTheme_EmptyBgUsesDefault(t *testing.T) {
	bg, fg, activeBg, activeFg, inactiveBg, inactiveFg := AutoFillTheme("", "", "", "", "", "", true)
	for _, color := range []string{bg, fg, activeBg, activeFg, inactiveBg, inactiveFg} {
		assert.True(t, isValidHex(color), "expected valid hex, got %q", color)
	}
	assert.Equal(t, GetDefaultGroupColor(0), bg)
}

func TestAutoFillTheme_PreservesExplicitValues(t *testing.T) {
	bg, fg, _, _, _, _ := AutoFillTheme("#3498db", "#ffffff", "", "", "", "", true)
	assert.Equal(t, "#3498db", bg)
	assert.Equal(t, "#ffffff", fg)
}

func TestAutoFillTheme_FillsMissingColors(t *testing.T) {
	_, _, activeBg, activeFg, inactiveBg, inactiveFg := AutoFillTheme("#3498db", "#ffffff", "", "", "", "", true)
	assert.True(t, isValidHex(activeBg), "activeBg should be filled")
	assert.True(t, isValidHex(activeFg), "activeFg should be filled")
	assert.True(t, isValidHex(inactiveBg), "inactiveBg should be filled")
	assert.True(t, isValidHex(inactiveFg), "inactiveFg should be filled")
}

func TestGetTheme_KnownTheme(t *testing.T) {
	th := GetTheme("dark")
	assert.Equal(t, "Dark", th.Name)
	assert.True(t, th.Dark)
	assert.NotEmpty(t, th.SidebarBg)
}

func TestGetTheme_UnknownFallsBackToDark(t *testing.T) {
	th := GetTheme("totally-nonexistent-theme-xyz")
	assert.Equal(t, GetTheme("dark"), th)
}

func TestGetTheme_RosePine(t *testing.T) {
	th := GetTheme("rose-pine")
	assert.Equal(t, "Rose Pine", th.Name)
	assert.True(t, th.Dark)
}

func TestGetTheme_CatppuccinLatte_IsLight(t *testing.T) {
	th := GetTheme("catppuccin-latte")
	assert.False(t, th.Dark)
}

func TestListThemes_NonEmpty(t *testing.T) {
	names := ListThemes()
	assert.NotEmpty(t, names)
}

func TestListThemes_ContainsBuiltins(t *testing.T) {
	names := ListThemes()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	for _, expected := range []string{"dark", "rose-pine", "dracula", "nord", "catppuccin-mocha"} {
		assert.True(t, nameSet[expected], "expected %q in theme list", expected)
	}
}

func TestHexToRGBInternal_Valid(t *testing.T) {
	r, g, b := hexToRGBInternal("#ff8800")
	assert.Equal(t, int64(255), r)
	assert.Equal(t, int64(136), g)
	assert.Equal(t, int64(0), b)
}

func TestHexToRGBInternal_Invalid(t *testing.T) {
	r, g, b := hexToRGBInternal("short")
	assert.Equal(t, int64(-1), r)
	assert.Equal(t, int64(-1), g)
	assert.Equal(t, int64(-1), b)
}

func TestRgbToHexInternal_Basic(t *testing.T) {
	assert.Equal(t, "#ff0000", rgbToHexInternal(255, 0, 0))
	assert.Equal(t, "#000000", rgbToHexInternal(0, 0, 0))
	assert.Equal(t, "#ffffff", rgbToHexInternal(255, 255, 255))
}

func TestRgbToHexInternal_Clamps(t *testing.T) {
	assert.Equal(t, "#ffffff", rgbToHexInternal(300, 300, 300))
	assert.Equal(t, "#000000", rgbToHexInternal(-1, -1, -1))
}
