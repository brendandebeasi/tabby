package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// isRawWindowID returns true if s is a raw tmux window ID (@N) — seen briefly
// after window creation before automatic-rename fires.
func isRawWindowID(s string) bool {
	if len(s) < 2 || s[0] != '@' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ansiEscapeRegex matches ANSI escape sequences
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?(?:\x07|\x1b\\)`)

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

func main() {
	if len(os.Args) < 4 {
		os.Exit(1)
	}

	mode := os.Args[1]
	index := os.Args[2]
	name := stripANSI(os.Args[3])
	if isRawWindowID(name) {
		name = "~"
	}
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
