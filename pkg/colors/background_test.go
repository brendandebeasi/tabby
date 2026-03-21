package colors

import (
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
