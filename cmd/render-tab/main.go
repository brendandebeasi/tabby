package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 4 {
		os.Exit(1)
	}

	mode := os.Args[1]
	index := os.Args[2]
	name := os.Args[3]
	flags := ""
	if len(os.Args) > 4 {
		flags = os.Args[4]
	}

	color := "colour252"
	activeColor := "colour255"
	icon := ""

	if strings.HasPrefix(name, "SD|") {
		color = "colour167"
		activeColor = "colour196"
	} else if strings.HasPrefix(name, "GP|") {
		color = "colour240"
		activeColor = "colour250"
		icon = "🔫 "
	}

	indicators := parseFlags(flags)
	closeBtn := "#[fg=#95a5a6][x]"
	if mode == "active" {
		closeBtn = "#[fg=#e74c3c][x]"
	}

	if mode == "active" {
		fmt.Printf("#[fg=%s,bg=default,bold] %s%s:%s%s %s ",
			activeColor, icon, index, name, indicators, closeBtn)
	} else {
		fmt.Printf("#[fg=%s,bg=default,nobold] %s%s:%s%s %s ",
			color, icon, index, name, indicators, closeBtn)
	}
}

func parseFlags(flags string) string {
	if flags == "" {
		return ""
	}
	indicators := ""
	if strings.Contains(flags, "M") {
		indicators += " 🔔"
	}
	if strings.Contains(flags, "!") {
		indicators += " ●"
	}
	if strings.Contains(flags, "~") {
		indicators += " 🔇"
	}
	return indicators
}
