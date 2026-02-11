package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
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
		fg := colors.HexToTmuxColor(group.Theme.Fg)
		activeFg := colors.HexToTmuxColor(group.Theme.ActiveFg)

		// Window name is now clean (no prefix stripping needed - groups use @tabby_group option)
		displayName := win.Name
		indicators := buildIndicators(win, cfg)

		iconStr := ""
		if group.Theme.Icon != "" {
			iconStr = group.Theme.Icon + " "
		}

		if win.Active {
			// Active tab: prominent background and foreground
			sb.WriteString(fmt.Sprintf("#[fg=%s,bg=%s,bold] %s%s%s ",
				activeFg, activeBg, iconStr, displayName, indicators))
		} else {
			// Inactive tab: subtle styling
			sb.WriteString(fmt.Sprintf("#[fg=%s,bg=%s,nobold] %s%s%s ",
				fg, bg, iconStr, displayName, indicators))
		}
	}

	if endIdx < len(allWindows) && cfg.Overflow.Mode == "scroll" {
		sb.WriteString(fmt.Sprintf(" #[fg=colour242]%s", cfg.Overflow.Indicator))
	}

	sb.WriteString(" #[fg=#27ae60][+]")

	sb.WriteString("#[default]")
	fmt.Print(sb.String())
}
