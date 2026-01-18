package main

import (
	"fmt"
	"github.com/b/tmux-tabs/pkg/tmux"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(1)
	}

	event := os.Args[1]
	x, _ := strconv.Atoi(os.Args[2])

	windows, err := tmux.ListWindows()
	if err != nil {
		os.Exit(1)
	}

	statusOutput, err := exec.Command(os.ExpandEnv("$HOME/git/tmux-tabs/bin/render-status")).Output()
	if err != nil {
		os.Exit(1)
	}

	tabPositions := parseTabPositions(string(statusOutput), windows)

	for _, tab := range tabPositions {
		if x >= tab.StartX && x <= tab.EndX {
			switch event {
			case "click":
				exec.Command("tmux", "select-window", "-t", fmt.Sprintf("%d", tab.WindowIndex)).Run()
			case "middleclick":
				exec.Command("tmux", "kill-window", "-t", fmt.Sprintf("%d", tab.WindowIndex)).Run()
			case "rightclick":
				exec.Command("tmux", "command-prompt", "-I", tab.WindowName, "rename-window '%%'").Run()
			}
			return
		}
	}

	if strings.Contains(string(statusOutput), "[+]") {
		plusPos := strings.Index(string(statusOutput), "[+]")
		if x >= plusPos && x <= plusPos+3 {
			exec.Command("tmux", "new-window").Run()
		}
	}
}

type TabPosition struct {
	WindowIndex int
	WindowName  string
	StartX      int
	EndX        int
}

func parseTabPositions(statusLine string, windows []tmux.Window) []TabPosition {
	positions := []TabPosition{}
	currentX := 0

	for _, win := range windows {
		tabText := fmt.Sprintf("%d:%s", win.Index, win.Name)
		startX := strings.Index(statusLine[currentX:], tabText)
		if startX >= 0 {
			startX += currentX
			endX := startX + len(tabText) + 3
			positions = append(positions, TabPosition{
				WindowIndex: win.Index,
				WindowName:  win.Name,
				StartX:      startX,
				EndX:        endX,
			})
			currentX = endX
		}
	}

	return positions
}
