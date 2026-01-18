package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/b/tmux-tabs/pkg/colors"
	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

func getTerminalWidth() int {
	cmd := exec.Command("tmux", "display-message", "-p", "#{window_width}")
	out, err := cmd.Output()
	if err != nil {
		return 80
	}
	width, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 80
	}
	return width
}

func getActiveWindowIndex(windows []tmux.Window) int {
	for i, win := range windows {
		if win.Active {
			return i
		}
	}
	return 0
}

func calculateTabWidth(win tmux.Window, group grouping.GroupedWindows, cfg *config.Config) int {
	baseLen := len(fmt.Sprintf(" %d:%s [x] ", win.Index, win.Name))
	if group.Theme.Icon != "" {
		baseLen += len(group.Theme.Icon) + 1
	}
	return baseLen + 2
}

func buildIndicators(win tmux.Window, cfg *config.Config) string {
	var indicators strings.Builder
	ind := cfg.Indicators

	if ind.Bell.Enabled && win.Bell {
		indicators.WriteString(" ")
		if ind.Bell.Color != "" {
			indicators.WriteString(fmt.Sprintf("#[fg=%s]", colors.HexToTmuxColor(ind.Bell.Color)))
		}
		indicators.WriteString(ind.Bell.Icon)
		indicators.WriteString("#[fg=default]")
	}
	if ind.Activity.Enabled && win.Activity {
		indicators.WriteString(" ")
		if ind.Activity.Color != "" {
			indicators.WriteString(fmt.Sprintf("#[fg=%s]", colors.HexToTmuxColor(ind.Activity.Color)))
		}
		indicators.WriteString(ind.Activity.Icon)
		indicators.WriteString("#[fg=default]")
	}
	if ind.Silence.Enabled && win.Silence {
		indicators.WriteString(" ")
		if ind.Silence.Color != "" {
			indicators.WriteString(fmt.Sprintf("#[fg=%s]", colors.HexToTmuxColor(ind.Silence.Color)))
		}
		indicators.WriteString(ind.Silence.Icon)
		indicators.WriteString("#[fg=default]")
	}
	if ind.Last.Enabled && win.Last && !win.Active {
		indicators.WriteString(" ")
		if ind.Last.Color != "" {
			indicators.WriteString(fmt.Sprintf("#[fg=%s]", colors.HexToTmuxColor(ind.Last.Color)))
		}
		indicators.WriteString(ind.Last.Icon)
		indicators.WriteString("#[fg=default]")
	}

	return indicators.String()
}

func main() {
	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		fmt.Print("tmux-tabs: config error")
		return
	}

	windows, err := tmux.ListWindows()
	if err != nil {
		fmt.Print("tmux-tabs: not in tmux")
		return
	}

	grouped := grouping.GroupWindows(windows, cfg.Groups)
	termWidth := getTerminalWidth()

	availableWidth := termWidth - 6

	var allWindows []struct {
		Window tmux.Window
		Group  grouping.GroupedWindows
		Width  int
	}

	// Create a map to look up groups by window
	windowGroupMap := make(map[int]grouping.GroupedWindows)
	for _, group := range grouped {
		for _, win := range group.Windows {
			windowGroupMap[win.Index] = group
		}
	}

	// Add windows in their original order (by index) to preserve sequential numbering
	for _, win := range windows {
		group := windowGroupMap[win.Index]
		width := calculateTabWidth(win, group, cfg)
		allWindows = append(allWindows, struct {
			Window tmux.Window
			Group  grouping.GroupedWindows
			Width  int
		}{win, group, width})
	}

	activeIdx := -1
	for i, item := range allWindows {
		if item.Window.Active {
			activeIdx = i
			break
		}
	}

	var startIdx, endIdx int
	if cfg.Overflow.Mode == "scroll" && activeIdx >= 0 {
		totalWidth := 0
		startIdx = activeIdx
		endIdx = activeIdx + 1

		totalWidth = allWindows[activeIdx].Width

		for i := activeIdx - 1; i >= 0 && totalWidth+allWindows[i].Width < availableWidth-10; i-- {
			totalWidth += allWindows[i].Width
			startIdx = i
		}

		for i := activeIdx + 1; i < len(allWindows) && totalWidth+allWindows[i].Width < availableWidth-10; i++ {
			totalWidth += allWindows[i].Width
			endIdx = i + 1
		}

		if totalWidth < availableWidth-10 && endIdx < len(allWindows) {
			for i := endIdx; i < len(allWindows) && totalWidth+allWindows[i].Width < availableWidth-10; i++ {
				totalWidth += allWindows[i].Width
				endIdx = i + 1
			}
		}
	} else {
		totalWidth := 0
		for i, item := range allWindows {
			if totalWidth+item.Width > availableWidth-10 {
				endIdx = i
				break
			}
			totalWidth += item.Width
		}
		if endIdx == 0 {
			endIdx = len(allWindows)
		}
	}

	var sb strings.Builder

	if startIdx > 0 && cfg.Overflow.Mode == "scroll" {
		sb.WriteString(fmt.Sprintf("#[fg=colour242]%s ", cfg.Overflow.Indicator))
	}

	for i := startIdx; i < endIdx && i < len(allWindows); i++ {
		item := allWindows[i]
		win := item.Window
		group := item.Group

		bg := colors.HexToTmuxColor(group.Theme.Bg)
		activeBg := colors.HexToTmuxColor(group.Theme.ActiveBg)

		indicators := buildIndicators(win, cfg)

		if win.Active {
			if group.Theme.Icon != "" {
				sb.WriteString(fmt.Sprintf("#[fg=%s,bg=default,bold] %s %d:%s%s #[fg=%s][x] ",
					activeBg, group.Theme.Icon, win.Index, win.Name, indicators,
					"#e74c3c"))
			} else {
				sb.WriteString(fmt.Sprintf("#[fg=%s,bg=default,bold] %d:%s%s #[fg=%s][x] ",
					activeBg, win.Index, win.Name, indicators,
					"#e74c3c"))
			}
		} else {
			iconStr := ""
			if group.Theme.Icon != "" {
				iconStr = group.Theme.Icon + " "
			}
			sb.WriteString(fmt.Sprintf("#[fg=%s,bg=default,nobold] %s%d:%s%s #[fg=%s][x] ",
				bg, iconStr, win.Index, win.Name, indicators,
				"#95a5a6"))
		}

		if i < endIdx-1 && cfg.Style.SeparatorRight != "" {
			sb.WriteString(cfg.Style.SeparatorRight)
			sb.WriteString(" ")
		}
	}

	if endIdx < len(allWindows) && cfg.Overflow.Mode == "scroll" {
		sb.WriteString(fmt.Sprintf(" #[fg=colour242]%s", cfg.Overflow.Indicator))
	}

	sb.WriteString(" #[fg=#27ae60][+]")

	sb.WriteString("#[default]")
	fmt.Print(sb.String())
}
