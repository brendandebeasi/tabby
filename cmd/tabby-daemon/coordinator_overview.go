package main

import (
	"fmt"
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

func (c *Coordinator) renderOverviewContent(width int) (string, []daemon.ClickableRegion) {
	if width < 2 {
		width = 2
	}

	var s strings.Builder
	var regions []daemon.ClickableRegion
	currentLine := 0

	accentBg := "#3498db"
	accentFg := "#ffffff"
	mutedFg := "#888888"
	treeFg := "#555555"
	groupHeaderFg := "#aaaaaa"
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
		if c.theme.DividerFg != "" {
			treeFg = c.theme.DividerFg
		}
		if c.theme.HeaderFg != "" {
			groupHeaderFg = c.theme.HeaderFg
		}
	}

	activeStyle := lipgloss.NewStyle().Bold(true).
		Background(lipgloss.Color(accentBg)).
		Foreground(lipgloss.Color(accentFg))
	inactiveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(mutedFg))
	treeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(treeFg))
	groupStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(groupHeaderFg)).Bold(true)

	for _, group := range c.grouped {
		if len(group.Windows) == 0 {
			continue
		}

		s.WriteString(groupStyle.Width(width).Render(" "+group.Name) + "\n")
		currentLine++

		for _, win := range group.Windows {
			winStartLine := currentLine

			contentPaneCount := 0
			for _, p := range win.Panes {
				if !isAuxiliaryPane(p) {
					contentPaneCount++
				}
			}

			isCollapsed := c.isOverviewWindowCollapsed(win.ID)

			collapseIcon := " "
			if contentPaneCount > 1 {
				if isCollapsed {
					collapseIcon = "▸"
				} else {
					collapseIcon = "▾"
				}
			}

			nameText := fmt.Sprintf("%d. %s", win.Index, win.Name)
			if contentPaneCount > 1 && isCollapsed {
				nameText += fmt.Sprintf(" (%d)", contentPaneCount)
			}

			maxNameW := width - 2
			if maxNameW < 1 {
				maxNameW = 1
			}
			if lipgloss.Width(nameText) > maxNameW {
				truncated := ""
				for _, r := range nameText {
					if lipgloss.Width(truncated+string(r)) > maxNameW-1 {
						break
					}
					truncated += string(r)
				}
				nameText = truncated + "~"
			}

			iconPart := treeStyle.Render(collapseIcon) + " "
			var rowContent string
			if win.Active {
				rowContent = iconPart + activeStyle.Width(maxNameW).Render(nameText)
			} else {
				rowContent = iconPart + inactiveStyle.Width(maxNameW).Render(nameText)
			}
			s.WriteString(zone.Mark("overview:win:"+win.ID, rowContent) + "\n")
			currentLine++

			regions = append(regions, daemon.ClickableRegion{
				StartLine: winStartLine, EndLine: winStartLine,
				StartCol: 2, EndCol: width - 1,
				Action: "select_window", Target: win.ID,
			})
			if contentPaneCount > 1 {
				regions = append(regions, daemon.ClickableRegion{
					StartLine: winStartLine, EndLine: winStartLine,
					StartCol: 0, EndCol: 1,
					Action: "overview_toggle_window", Target: win.ID,
				})
			}

			if contentPaneCount > 1 && !isCollapsed {
				renderedCount := 0
				for _, p := range win.Panes {
					if isAuxiliaryPane(p) {
						continue
					}
					renderedCount++
					isLast := renderedCount == contentPaneCount

					branchChar := "├─"
					if isLast {
						branchChar = "└─"
					}

					paneLabel := p.Command
					if p.LockedTitle != "" {
						paneLabel = p.LockedTitle
					} else if p.Title != "" && p.Title != p.Command {
						paneLabel = p.Title
					}

					paneText := fmt.Sprintf("  %s %s", branchChar, paneLabel)
					maxPaneW := width - 1
					if maxPaneW < 1 {
						maxPaneW = 1
					}
					if lipgloss.Width(paneText) > maxPaneW {
						truncated := ""
						for _, r := range paneText {
							if lipgloss.Width(truncated+string(r)) > maxPaneW-1 {
								break
							}
							truncated += string(r)
						}
						paneText = truncated + "~"
					}

					s.WriteString(treeStyle.Render(paneText) + "\n")
					regions = append(regions, daemon.ClickableRegion{
						StartLine: currentLine, EndLine: currentLine,
						StartCol: 0, EndCol: width - 1,
						Action: "select_pane", Target: p.ID,
					})
					currentLine++
				}
			}
		}
	}

	return s.String(), regions
}
