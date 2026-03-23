package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/brendandebeasi/tabby/pkg/daemon"
)

func (c *Coordinator) renderTabSwitcher(width int) (string, []daemon.ClickableRegion) {
	if width < 2 {
		width = 2
	}

	c.stateMu.RLock()
	viewMode := c.viewMode
	c.stateMu.RUnlock()
	if viewMode == "" {
		viewMode = "current"
	}

	leftLabel := " Window "
	rightLabel := " All "

	accentBg := "#3498db"
	accentFg := "#ffffff"
	mutedFg := "#888888"
	if c.theme != nil {
		if c.theme.DefaultActiveBg != "" {
			accentBg = c.theme.DefaultActiveBg
		}
		if c.theme.DefaultActiveFg != "" {
			accentFg = c.theme.DefaultActiveFg
		}
		if c.theme.InactiveFg != "" {
			mutedFg = c.theme.InactiveFg
		}
	}

	activeStyle := lipgloss.NewStyle().Background(lipgloss.Color(accentBg)).Foreground(lipgloss.Color(accentFg)).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(mutedFg))

	leftWidth := width / 2
	rightWidth := width - leftWidth
	if leftWidth < 1 {
		leftWidth = 1
	}
	if rightWidth < 1 {
		rightWidth = 1
	}

	var leftRendered, rightRendered string
	if viewMode == "current" {
		leftRendered = activeStyle.Width(leftWidth).Render(leftLabel)
		rightRendered = inactiveStyle.Width(rightWidth).Render(rightLabel)
	} else {
		leftRendered = inactiveStyle.Width(leftWidth).Render(leftLabel)
		rightRendered = activeStyle.Width(rightWidth).Render(rightLabel)
	}

	leftMarked := zone.Mark("view:current", leftRendered)
	rightMarked := zone.Mark("view:overview", rightRendered)

	tabRow := leftMarked + rightMarked + "\n"

	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	sep := sepStyle.Render(strings.Repeat("─", width)) + "\n"

	content := tabRow + sep

	regions := []daemon.ClickableRegion{
		{
			StartLine: 0, EndLine: 0,
			StartCol: 0, EndCol: leftWidth - 1,
			Action: "switch_view", Target: "current",
		},
		{
			StartLine: 0, EndLine: 0,
			StartCol: leftWidth, EndCol: width - 1,
			Action: "switch_view", Target: "overview",
		},
	}

	return content, regions
}
