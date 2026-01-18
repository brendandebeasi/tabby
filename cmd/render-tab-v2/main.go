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

	fg := "colour255"
	bg := "colour236"
	icon := ""

	if strings.HasPrefix(name, "SD|") {
		if mode == "active" {
			fg = "colour255"
			bg = "colour160"
		} else {
			fg = "colour255"
			bg = "colour52"
		}
	} else if strings.HasPrefix(name, "GP|") {
		icon = "🔫 "
		if mode == "active" {
			fg = "colour255"
			bg = "colour60"
		} else {
			fg = "colour255"
			bg = "colour236"
		}
	} else {
		if mode == "active" {
			fg = "colour255"
			bg = "colour31"
		} else {
			fg = "colour255"
			bg = "colour236"
		}
	}

	indicators := parseFlags(flags)

	boldAttr := ""
	if mode == "active" {
		boldAttr = ",bold"
	}

	fmt.Printf("#[fg=%s,bg=%s%s] %s%s:%s%s ",
		fg, bg, boldAttr, icon, index, name, indicators)
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
