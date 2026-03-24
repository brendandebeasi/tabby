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

func TestDeriveTextColor_WhiteOnDarkBg(t *testing.T) {
	got := DeriveTextColor("#000000")
	assert.Equal(t, "#ffffff", got, "should return white on pure black")
}

func TestDeriveTextColor_BlackOnLightBg(t *testing.T) {
	got := DeriveTextColor("#ffffff")
	assert.Equal(t, "#000000", got, "should return black on pure white")
}

func TestDeriveTextColor_YellowFallback(t *testing.T) {
	got := DeriveTextColor("#ffff00")
	assert.Equal(t, "#000000", got, "yellow is light, should return black")
}

func TestDeriveTextColor_GrayFallback(t *testing.T) {
	got := DeriveTextColor("#808080")
	assert.True(t, isValidHex(got), "gray should return valid hex")
	assert.True(t, got == "#ffffff" || got == "#000000", "gray should return white or black")
}

func TestDeriveActiveBg_DarkTerminalBrighter(t *testing.T) {
	darkResult := DeriveActiveBg("#3498db", true)
	lightResult := DeriveActiveBg("#3498db", false)
	assert.NotEqual(t, darkResult, lightResult, "dark and light terminal should produce different results")
	assert.True(t, isValidHex(darkResult), "dark terminal result should be valid hex")
	assert.True(t, isValidHex(lightResult), "light terminal result should be valid hex")
}

func TestDeriveActiveBg_InvalidColorPassthrough(t *testing.T) {
	got := DeriveActiveBg("notahex", true)
	assert.Equal(t, "notahex", got, "invalid color should pass through unchanged")
}

func TestDeriveActiveBg_SaturationBoost(t *testing.T) {
	result := DeriveActiveBg("#808080", true)
	assert.True(t, isValidHex(result), "grayscale input should produce valid hex")
}

func TestDeriveActiveBg_LightnessConstraints(t *testing.T) {
	darkResult := DeriveActiveBg("#000000", true)
	assert.True(t, isValidHex(darkResult), "pure black on dark terminal should produce valid hex")
	lightResult := DeriveActiveBg("#ffffff", false)
	assert.True(t, isValidHex(lightResult), "pure white on light terminal should produce valid hex")
}

func TestDeriveTextColor_DarkBlue(t *testing.T) {
	got := DeriveTextColor("#003366")
	assert.Equal(t, "#ffffff", got, "dark blue should get white text")
}

func TestDeriveTextColor_LightGreen(t *testing.T) {
	got := DeriveTextColor("#90ee90")
	assert.Equal(t, "#000000", got, "light green should get black text")
}

func TestDeriveTextColor_MidtoneColor(t *testing.T) {
	got := DeriveTextColor("#808080")
	assert.True(t, got == "#ffffff" || got == "#000000", "midtone should return white or black")
	assert.True(t, isValidHex(got), "result should be valid hex")
}

func TestDeriveTextColor_InvalidColor(t *testing.T) {
	got := DeriveTextColor("notahex")
	assert.True(t, got == "#ffffff" || got == "#000000", "invalid color should still return white or black")
}

func TestDeriveTextColor_Red(t *testing.T) {
	got := DeriveTextColor("#ff0000")
	assert.True(t, isValidHex(got), "red should return valid hex")
}

func TestDeriveTextColor_Blue(t *testing.T) {
	got := DeriveTextColor("#0000ff")
	assert.True(t, isValidHex(got), "blue should return valid hex")
}

func TestDeriveTextColor_Green(t *testing.T) {
	got := DeriveTextColor("#00ff00")
	assert.True(t, isValidHex(got), "green should return valid hex")
}

func TestDeriveActiveBg_DarkTerminalLightness(t *testing.T) {
	result := DeriveActiveBg("#404040", true)
	assert.True(t, isValidHex(result), "dark gray on dark terminal should produce valid hex")
}

func TestDeriveActiveBg_LightTerminalLightness(t *testing.T) {
	result := DeriveActiveBg("#c0c0c0", false)
	assert.True(t, isValidHex(result), "light gray on light terminal should produce valid hex")
}

func TestDeriveActiveBg_DarkTerminalMinLightness(t *testing.T) {
	result := DeriveActiveBg("#000000", true)
	assert.True(t, isValidHex(result), "pure black on dark terminal should clamp to min lightness")
}

func TestDeriveActiveBg_DarkTerminalMaxLightness(t *testing.T) {
	result := DeriveActiveBg("#ffffff", true)
	assert.True(t, isValidHex(result), "pure white on dark terminal should clamp to max lightness")
}

func TestDeriveActiveBg_LightTerminalMinLightness(t *testing.T) {
	result := DeriveActiveBg("#000000", false)
	assert.True(t, isValidHex(result), "pure black on light terminal should clamp to min lightness")
}

func TestDeriveActiveBg_LightTerminalMaxLightness(t *testing.T) {
	result := DeriveActiveBg("#ffffff", false)
	assert.True(t, isValidHex(result), "pure white on light terminal should clamp to max lightness")
}

func TestDeriveActiveBg_SaturationClamping(t *testing.T) {
	result := DeriveActiveBg("#ff0000", true)
	assert.True(t, isValidHex(result), "pure red should clamp saturation to 1.0")
}

func TestHexToHSL_Black(t *testing.T) {
	h, _, l := hexToHSL("#000000")
	assert.True(t, h >= 0, "black should have valid hue")
	assert.Equal(t, 0.0, l, "black should have lightness 0")
}

func TestHexToHSL_White(t *testing.T) {
	h, _, l := hexToHSL("#ffffff")
	assert.True(t, h >= 0, "white should have valid hue")
	assert.Equal(t, 1.0, l, "white should have lightness 1")
}

func TestHexToHSL_Red(t *testing.T) {
	h, s, _ := hexToHSL("#ff0000")
	assert.True(t, h >= 0 && h < 360, "red should have hue in range")
	assert.Equal(t, 1.0, s, "pure red should have saturation 1")
}

func TestHexToHSL_Green(t *testing.T) {
	h, s, _ := hexToHSL("#00ff00")
	assert.True(t, h >= 0 && h < 360, "green should have hue in range")
	assert.Equal(t, 1.0, s, "pure green should have saturation 1")
}

func TestHexToHSL_Blue(t *testing.T) {
	h, s, _ := hexToHSL("#0000ff")
	assert.True(t, h >= 0 && h < 360, "blue should have hue in range")
	assert.Equal(t, 1.0, s, "pure blue should have saturation 1")
}

func TestHexToRGBInternal_Black(t *testing.T) {
	r, g, b := hexToRGBInternal("#000000")
	assert.Equal(t, int64(0), r)
	assert.Equal(t, int64(0), g)
	assert.Equal(t, int64(0), b)
}

func TestHexToRGBInternal_White(t *testing.T) {
	r, g, b := hexToRGBInternal("#ffffff")
	assert.Equal(t, int64(255), r)
	assert.Equal(t, int64(255), g)
	assert.Equal(t, int64(255), b)
}

func TestHexToRGBInternal_Red(t *testing.T) {
	r, g, b := hexToRGBInternal("#ff0000")
	assert.Equal(t, int64(255), r)
	assert.Equal(t, int64(0), g)
	assert.Equal(t, int64(0), b)
}

func TestHexToRGBInternal_Green(t *testing.T) {
	r, g, b := hexToRGBInternal("#00ff00")
	assert.Equal(t, int64(0), r)
	assert.Equal(t, int64(255), g)
	assert.Equal(t, int64(0), b)
}

func TestHexToRGBInternal_Blue(t *testing.T) {
	r, g, b := hexToRGBInternal("#0000ff")
	assert.Equal(t, int64(0), r)
	assert.Equal(t, int64(0), g)
	assert.Equal(t, int64(255), b)
}

func TestHexToRGBInternal_Gray(t *testing.T) {
	r, g, b := hexToRGBInternal("#808080")
	assert.Equal(t, int64(128), r)
	assert.Equal(t, int64(128), g)
	assert.Equal(t, int64(128), b)
}

func TestHexToRGBInternal_NoHash(t *testing.T) {
	r, g, b := hexToRGBInternal("ff0000")
	assert.Equal(t, int64(255), r)
	assert.Equal(t, int64(0), g)
	assert.Equal(t, int64(0), b)
}

func TestHexToRGBInternal_TooShort(t *testing.T) {
	r, g, b := hexToRGBInternal("#fff")
	assert.Equal(t, int64(-1), r)
	assert.Equal(t, int64(-1), g)
	assert.Equal(t, int64(-1), b)
}

func TestHexToRGBInternal_TooLong(t *testing.T) {
	r, g, b := hexToRGBInternal("#fffffff")
	assert.Equal(t, int64(-1), r)
	assert.Equal(t, int64(-1), g)
	assert.Equal(t, int64(-1), b)
}

func TestHexToRGBInternal_InvalidChars(t *testing.T) {
	r, g, b := hexToRGBInternal("#gggggg")
	assert.Equal(t, int64(-1), r)
	assert.Equal(t, int64(-1), g)
	assert.Equal(t, int64(-1), b)
}

func TestHexToHSL_Cyan(t *testing.T) {
	h, s, l := hexToHSL("#00ffff")
	assert.True(t, h >= 0 && h < 360, "cyan should have valid hue")
	assert.Equal(t, 1.0, s, "pure cyan should have saturation 1")
	assert.Equal(t, 0.5, l, "cyan should have lightness 0.5")
}

func TestHexToHSL_Magenta(t *testing.T) {
	h, s, l := hexToHSL("#ff00ff")
	assert.True(t, h >= 0 && h < 360, "magenta should have valid hue")
	assert.Equal(t, 1.0, s, "pure magenta should have saturation 1")
	assert.Equal(t, 0.5, l, "magenta should have lightness 0.5")
}

func TestHexToHSL_Yellow(t *testing.T) {
	h, s, l := hexToHSL("#ffff00")
	assert.True(t, h >= 0 && h < 360, "yellow should have valid hue")
	assert.Equal(t, 1.0, s, "pure yellow should have saturation 1")
	assert.Equal(t, 0.5, l, "yellow should have lightness 0.5")
}

func TestDeriveTextColor_LuminanceFallback(t *testing.T) {
	got := DeriveTextColor("#777777")
	assert.True(t, got == "#ffffff" || got == "#000000", "luminance fallback should return white or black")
	assert.True(t, isValidHex(got), "result should be valid hex")
}

func TestDeriveTextColor_WhiteContrastFails(t *testing.T) {
	got := DeriveTextColor("#ffff00")
	assert.Equal(t, "#000000", got, "should return black when white contrast fails")
}

func TestDeriveTextColor_BothContrastFail(t *testing.T) {
	got := DeriveTextColor("#888888")
	assert.True(t, got == "#ffffff" || got == "#000000", "should fall back to luminance")
}

func TestDeriveTextColor_VeryDarkColor(t *testing.T) {
	got := DeriveTextColor("#111111")
	assert.Equal(t, "#ffffff", got, "very dark color should use white text")
}

func TestDeriveTextColor_VeryLightColor(t *testing.T) {
	got := DeriveTextColor("#eeeeee")
	assert.Equal(t, "#000000", got, "very light color should use black text")
}

func TestDeriveTextColor_ContrastRatioExactly3(t *testing.T) {
	got := DeriveTextColor("#7f7f7f")
	assert.True(t, got == "#ffffff" || got == "#000000", "midtone should return white or black")
	assert.True(t, isValidHex(got), "result should be valid hex")
}

func TestDeriveTextColor_OrangeBackground(t *testing.T) {
	got := DeriveTextColor("#ff9900")
	assert.True(t, isValidHex(got), "orange should return valid hex")
	assert.True(t, got == "#ffffff" || got == "#000000", "should return white or black")
}

func TestDeriveTextColor_PurpleBackground(t *testing.T) {
	got := DeriveTextColor("#9933cc")
	assert.True(t, isValidHex(got), "purple should return valid hex")
	assert.True(t, got == "#ffffff" || got == "#000000", "should return white or black")
}

func TestDeriveTextColor_CyanBackground(t *testing.T) {
	got := DeriveTextColor("#00ffff")
	assert.True(t, isValidHex(got), "cyan should return valid hex")
	assert.True(t, got == "#ffffff" || got == "#000000", "should return white or black")
}

func TestDeriveTextColor_MagentaBackground(t *testing.T) {
	got := DeriveTextColor("#ff00ff")
	assert.True(t, isValidHex(got), "magenta should return valid hex")
	assert.True(t, got == "#ffffff" || got == "#000000", "should return white or black")
}

func TestDeriveTextColor_DarkGrayBackground(t *testing.T) {
	got := DeriveTextColor("#333333")
	assert.Equal(t, "#ffffff", got, "dark gray should use white text")
}

func TestDeriveTextColor_LightGrayBackground(t *testing.T) {
	got := DeriveTextColor("#cccccc")
	assert.Equal(t, "#000000", got, "light gray should use black text")
}

func TestDeriveTextColor_ContrastRatioBoundary(t *testing.T) {
	got := DeriveTextColor("#666666")
	assert.True(t, got == "#ffffff" || got == "#000000", "midtone gray should return white or black")
	ratio := GetContrastRatio(got, "#666666")
	assert.True(t, ratio >= 1.0, "contrast ratio should be at least 1.0")
}
