package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/b/tmux-tabs/pkg/colors"
	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

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
	if len(os.Args) < 2 {
		return
	}

	targetIndex, err := strconv.Atoi(os.Args[1])
	if err != nil {
		return
	}

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		return
	}

	windows, err := tmux.ListWindows()
	if err != nil {
		return
	}

	grouped := grouping.GroupWindows(windows, cfg.Groups)

	for _, group := range grouped {
		fg := colors.HexToTmuxColor(group.Theme.Fg)
		bg := colors.HexToTmuxColor(group.Theme.Bg)
		activeFg := colors.HexToTmuxColor(group.Theme.ActiveFg)
		activeBg := colors.HexToTmuxColor(group.Theme.ActiveBg)

		for _, win := range group.Windows {
			if win.Index == targetIndex {
				indicators := buildIndicators(win, cfg)

				if win.Active {
					fmt.Printf("#[fg=%s,bg=%s,bold] %s %d:%s%s ", activeFg, activeBg, group.Theme.Icon, win.Index, win.Name, indicators)
				} else {
					fmt.Printf("#[fg=%s,bg=%s,nobold] %d:%s%s ", fg, bg, win.Index, win.Name, indicators)
				}
				fmt.Print(cfg.Style.SeparatorRight)
				fmt.Print(" ")
				return
			}
		}
	}
}
