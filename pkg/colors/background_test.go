package colors

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBackgroundDetector_DarkMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.True(t, d.IsDarkBackground())
}

func TestNewBackgroundDetector_LightMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	assert.False(t, d.IsDarkBackground())
}

func TestNewBackgroundDetector_AutoMode_DoesNotPanic(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeAuto)
	_ = d.IsDarkBackground()
}

func TestIsDarkBackground_CachesResult(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	first := d.IsDarkBackground()
	second := d.IsDarkBackground()
	assert.Equal(t, first, second)
}

func TestGetDetectedColor_EmptyForFreshDetector(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.Equal(t, "", d.GetDetectedColor())
}

func TestAdjustForegroundForBackground_EmptyOnDark(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.Equal(t, "#ffffff", d.AdjustForegroundForBackground(""))
}

func TestAdjustForegroundForBackground_EmptyOnLight(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	assert.Equal(t, "#000000", d.AdjustForegroundForBackground(""))
}

func TestAdjustForegroundForBackground_PassthroughExplicitColor(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.Equal(t, "#aabbcc", d.AdjustForegroundForBackground("#aabbcc"))
}

func TestGetDefaultTextColor_NonEmpty(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEmpty(t, dark.GetDefaultTextColor())
	assert.NotEmpty(t, light.GetDefaultTextColor())
	assert.NotEqual(t, dark.GetDefaultTextColor(), light.GetDefaultTextColor())
}

func TestGetDefaultHeaderTextColor_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEmpty(t, dark.GetDefaultHeaderTextColor())
	assert.NotEmpty(t, light.GetDefaultHeaderTextColor())
	assert.NotEqual(t, dark.GetDefaultHeaderTextColor(), light.GetDefaultHeaderTextColor())
}

func TestGetDefaultInactiveTextColor_NonEmpty(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.NotEmpty(t, d.GetDefaultInactiveTextColor())
}

func TestGetDefaultSidebarBg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultSidebarBg(), light.GetDefaultSidebarBg())
}

func TestGetDefaultDisclosureFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEmpty(t, dark.GetDefaultDisclosureFg())
	assert.NotEmpty(t, light.GetDefaultDisclosureFg())
	assert.NotEqual(t, dark.GetDefaultDisclosureFg(), light.GetDefaultDisclosureFg())
}

func TestGetDefaultTreeFg_Stable(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.Equal(t, dark.GetDefaultTreeFg(), light.GetDefaultTreeFg())
}

func TestGetDefaultPaneHeaderActiveBg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPaneHeaderActiveBg(), light.GetDefaultPaneHeaderActiveBg())
}

func TestGetDefaultPaneHeaderActiveFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPaneHeaderActiveFg(), light.GetDefaultPaneHeaderActiveFg())
}

func TestGetDefaultPaneHeaderInactiveBg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPaneHeaderInactiveBg(), light.GetDefaultPaneHeaderInactiveBg())
}

func TestGetDefaultPaneHeaderInactiveFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPaneHeaderInactiveFg(), light.GetDefaultPaneHeaderInactiveFg())
}

func TestGetDefaultCommandFg_NonEmpty(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.NotEmpty(t, d.GetDefaultCommandFg())
}

func TestGetDefaultButtonFg_NonEmpty(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.NotEmpty(t, d.GetDefaultButtonFg())
}

func TestGetDefaultBorderFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultBorderFg(), light.GetDefaultBorderFg())
}

func TestGetDefaultHandleColor_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEmpty(t, dark.GetDefaultHandleColor())
	assert.NotEmpty(t, light.GetDefaultHandleColor())
}

func TestGetDefaultTerminalBg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultTerminalBg(), light.GetDefaultTerminalBg())
}

func TestGetDefaultDividerFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultDividerFg(), light.GetDefaultDividerFg())
}

func TestGetDefaultPromptFg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPromptFg(), light.GetDefaultPromptFg())
}

func TestGetDefaultPromptBg_DarkVsLight(t *testing.T) {
	dark := NewBackgroundDetector(ThemeModeDark)
	light := NewBackgroundDetector(ThemeModeLight)
	assert.NotEqual(t, dark.GetDefaultPromptBg(), light.GetDefaultPromptBg())
}

func TestGetDefaultWidgetFg_NonEmpty(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	assert.NotEmpty(t, d.GetDefaultWidgetFg())
}

// Light mode tests for functions with 66.7% coverage
func TestGetDefaultInactiveTextColor_LightMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	color := d.GetDefaultInactiveTextColor()
	assert.NotEmpty(t, color)
	assert.NotEqual(t, "#888888", color, "light mode should differ from dark mode")
	assert.Equal(t, "#9893a5", color)
}

func TestGetDefaultCommandFg_LightMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	color := d.GetDefaultCommandFg()
	assert.NotEmpty(t, color)
	assert.NotEqual(t, "#aaaaaa", color, "light mode should differ from dark mode")
	assert.Equal(t, "#797593", color)
}

func TestGetDefaultButtonFg_LightMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	color := d.GetDefaultButtonFg()
	assert.NotEmpty(t, color)
	assert.NotEqual(t, "#888888", color, "light mode should differ from dark mode")
	assert.Equal(t, "#9893a5", color)
}

func TestGetDefaultWidgetFg_LightMode(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeLight)
	color := d.GetDefaultWidgetFg()
	assert.NotEmpty(t, color)
	assert.NotEqual(t, "#aaaaaa", color, "light mode should differ from dark mode")
	assert.Equal(t, "#797593", color)
}

// Tests for checkCOLORFGBG with various inputs
func TestCheckCOLORFGBG_SinglePart(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	t.Setenv("COLORFGBG", "15")
	isDark, ok := d.checkCOLORFGBG()
	assert.False(t, ok, "single part should return ok=false")
	assert.False(t, isDark)
}

func TestCheckCOLORFGBG_InvalidNumber(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	t.Setenv("COLORFGBG", "15;abc")
	isDark, ok := d.checkCOLORFGBG()
	assert.False(t, ok, "invalid number should return ok=false")
	assert.False(t, isDark)
}

func TestCheckCOLORFGBG_DarkBackground(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	t.Setenv("COLORFGBG", "15;0")
	isDark, ok := d.checkCOLORFGBG()
	assert.True(t, ok, "valid bg=0 should return ok=true")
	assert.True(t, isDark, "bg=0 (< 8) should be dark")
}

func TestCheckCOLORFGBG_LightBackground(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	t.Setenv("COLORFGBG", "15;15")
	isDark, ok := d.checkCOLORFGBG()
	assert.True(t, ok, "valid bg=15 should return ok=true")
	assert.False(t, isDark, "bg=15 (>= 8) should be light")
}

func TestCheckCOLORFGBG_SpecialCase16(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeDark)
	t.Setenv("COLORFGBG", "0;16")
	isDark, ok := d.checkCOLORFGBG()
	assert.True(t, ok, "valid bg=16 should return ok=true")
	assert.True(t, isDark, "bg=16 is special case (dark)")
}

// Tests for checkTerminalHints with ITERM_PROFILE
func TestCheckTerminalHints_ITerm_LightProfile(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeAuto)
	t.Setenv("ITERM_PROFILE", "Light Profile")
	isDark, ok := d.checkTerminalHints()
	assert.True(t, ok, "light profile should return ok=true")
	assert.False(t, isDark, "light profile should be light")
}

func TestCheckTerminalHints_ITerm_DarkProfile(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeAuto)
	t.Setenv("ITERM_PROFILE", "Dark Profile")
	isDark, ok := d.checkTerminalHints()
	assert.True(t, ok, "dark profile should return ok=true")
	assert.True(t, isDark, "dark profile should be dark")
}

func TestCheckTerminalHints_ITerm_NeutralProfile(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeAuto)
	t.Setenv("ITERM_PROFILE", "Default")
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	_, ok := d.checkTerminalHints()
	assert.False(t, ok, "neutral profile should return ok=false")
}

// Test for checkGhosttyConfig with comment stripping
func TestCheckGhosttyConfig_WithComment(t *testing.T) {
	d := NewBackgroundDetector(ThemeModeAuto)
	tempDir := t.TempDir()
	t.Setenv("HOME", tempDir)

	// Create config directory and file
	configDir := tempDir + "/.config/ghostty"
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Write config with comment
	configFile := configDir + "/config"
	content := "background = 1a1a2e # dark blue\n"
	err = os.WriteFile(configFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	isDark, ok := d.checkGhosttyConfig()
	assert.True(t, ok, "valid ghostty config should return ok=true")
	assert.True(t, isDark, "dark color should be detected as dark")
	assert.Equal(t, "#1a1a2e", d.GetDetectedColor())
}
