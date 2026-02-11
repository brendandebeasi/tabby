package main

import (
	"fmt"
	"strings"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func main() {
	// Get active window
	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		return
	}

	var activeWindow *tmux.Window
	for i := range windows {
		if windows[i].Active {
			activeWindow = &windows[i]
			break
		}
	}

	if activeWindow == nil || len(activeWindow.Panes) <= 1 {
		// No panes to show or only one pane
		return
	}

	var sb strings.Builder
	sb.WriteString("  ") // Indent to align with tabs

	for i, pane := range activeWindow.Panes {
		// Format: window.pane command
		paneNum := fmt.Sprintf("%d.%d", activeWindow.Index, pane.Index)

		if pane.Active {
			// Active pane - highlighted
			sb.WriteString(fmt.Sprintf("#[fg=#00ff00,bold]>>> %s %s #[default]", paneNum, pane.Command))
		} else {
			// Inactive pane
			sb.WriteString(fmt.Sprintf("#[fg=#888888]    %s %s #[default]", paneNum, pane.Command))
		}

		if i < len(activeWindow.Panes)-1 {
			sb.WriteString("  ") // Separator between panes
		}
	}

	fmt.Print(sb.String())
}
