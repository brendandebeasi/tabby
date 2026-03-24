package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/stretchr/testify/assert"
)

func TestGetTextColorWithFallback_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "#aabbcc", c.getTextColorWithFallback("#aabbcc"))
}

func TestGetTextColorWithFallback_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.ActiveFg, c.getTextColorWithFallback(""))
}

func TestGetTextColorWithFallback_DetectorFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.bgDetector = colors.NewBackgroundDetector(colors.ThemeModeAuto)
	got := c.getTextColorWithFallback("")
	assert.NotEmpty(t, got)
}

func TestGetHeaderTextColorWithFallback_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "#112233", c.getHeaderTextColorWithFallback("#112233"))
}

func TestGetHeaderTextColorWithFallback_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.HeaderFg, c.getHeaderTextColorWithFallback(""))
}

func TestGetInactiveTextColorWithFallback_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "#999999", c.getInactiveTextColorWithFallback("#999999"))
}

func TestGetInactiveTextColorWithFallback_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.InactiveFg, c.getInactiveTextColorWithFallback(""))
}

func TestGetPaneFgWithFallback_ConfigPaneFgWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.Colors.PaneFg = "#ff0000"
	assert.Equal(t, "#ff0000", c.getPaneFgWithFallback())
}

func TestGetPaneFgWithFallback_FallsBackToInactiveFg(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.Colors.PaneFg = ""
	c.config.Sidebar.Colors.InactiveFg = "#cccccc"
	assert.Equal(t, "#cccccc", c.getPaneFgWithFallback())
}

func TestGetPaneFgWithFallback_ThemeWhenBothEmpty(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.Colors.PaneFg = ""
	c.config.Sidebar.Colors.InactiveFg = ""
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.InactiveFg, c.getPaneFgWithFallback())
}

func TestGetTreeFgWithFallback_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "#334455", c.getTreeFgWithFallback("#334455"))
}

func TestGetTreeFgWithFallback_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.TreeFg, c.getTreeFgWithFallback(""))
}

func TestGetDisclosureFgWithFallback_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, "#aaaaaa", c.getDisclosureFgWithFallback("#aaaaaa"))
}

func TestGetDisclosureFgWithFallback_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.DisclosureFg, c.getDisclosureFgWithFallback(""))
}

func TestGetPaneHeaderActiveBg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.ActiveBg = "#001122"
	assert.Equal(t, "#001122", c.getPaneHeaderActiveBg())
}

func TestGetPaneHeaderActiveBg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PaneActiveBg, c.getPaneHeaderActiveBg())
}

func TestGetPaneHeaderActiveFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.ActiveFg = "#ffffff"
	assert.Equal(t, "#ffffff", c.getPaneHeaderActiveFg())
}

func TestGetPaneHeaderActiveFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PaneActiveFg, c.getPaneHeaderActiveFg())
}

func TestGetPaneHeaderInactiveBg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.InactiveBg = "#333333"
	assert.Equal(t, "#333333", c.getPaneHeaderInactiveBg())
}

func TestGetPaneHeaderInactiveBg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PaneInactiveBg, c.getPaneHeaderInactiveBg())
}

func TestGetPaneHeaderInactiveFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.InactiveFg = "#888888"
	assert.Equal(t, "#888888", c.getPaneHeaderInactiveFg())
}

func TestGetPaneHeaderInactiveFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PaneInactiveFg, c.getPaneHeaderInactiveFg())
}

func TestGetCommandFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.CommandFg = "#aaaaaa"
	assert.Equal(t, "#aaaaaa", c.getCommandFg())
}

func TestGetCommandFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.CommandFg, c.getCommandFg())
}

func TestGetButtonFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.ButtonFg = "#bbbbbb"
	assert.Equal(t, "#bbbbbb", c.getButtonFg())
}

func TestGetButtonFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PaneButtonFg, c.getButtonFg())
}

func TestGetBorderFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.BorderFg = "#444444"
	assert.Equal(t, "#444444", c.getBorderFg())
}

func TestGetBorderFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.BorderFg, c.getBorderFg())
}

func TestGetHandleColor_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.HandleColor = "#666666"
	assert.Equal(t, "#666666", c.getHandleColor())
}

func TestGetHandleColor_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.HandleColor, c.getHandleColor())
}

func TestGetTerminalBg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.TerminalBg = "#1e1e1e"
	assert.Equal(t, "#1e1e1e", c.GetTerminalBg())
}

func TestGetTerminalBg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.TerminalBg, c.GetTerminalBg())
}

func TestGetDividerFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.DividerFg = "#555555"
	assert.Equal(t, "#555555", c.getDividerFg())
}

func TestGetDividerFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.DividerFg, c.getDividerFg())
}

func TestGetWidgetFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.WidgetFg, c.getWidgetFg())
}

func TestGetWidgetFg_DetectorFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.bgDetector = colors.NewBackgroundDetector(colors.ThemeModeAuto)
	got := c.getWidgetFg()
	assert.NotEmpty(t, got)
}

func TestGetPromptFg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Prompt.Fg = "#ffffff"
	assert.Equal(t, "#ffffff", c.getPromptFg())
}

func TestGetPromptFg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PromptFg, c.getPromptFg())
}

func TestGetPromptBg_ConfigWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Prompt.Bg = "#000000"
	assert.Equal(t, "#000000", c.getPromptBg())
}

func TestGetPromptBg_ThemeWins(t *testing.T) {
	c := newTestCoordinator(t)
	th := colors.GetTheme("dark")
	c.theme = &th
	assert.Equal(t, th.PromptBg, c.getPromptBg())
}
