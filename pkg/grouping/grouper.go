package grouping

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/tmux"
)

type GroupedWindows struct {
	Name    string
	Theme   config.Theme
	Windows []tmux.Window
}

func GroupWindows(windows []tmux.Window, groups []config.Group) []GroupedWindows {
	var result []*GroupedWindows
	groupMap := make(map[string]*GroupedWindows)

	for _, group := range groups {
		gw := &GroupedWindows{
			Name:    group.Name,
			Theme:   group.Theme,
			Windows: []tmux.Window{},
		}
		groupMap[group.Name] = gw
		result = append(result, gw)
	}

	for _, win := range windows {
		matched := false
		for _, group := range groups {
			re, err := regexp.Compile(group.Pattern)
			if err != nil {
				continue
			}
			if re.MatchString(win.Name) {
				groupMap[group.Name].Windows = append(groupMap[group.Name].Windows, win)
				matched = true
				break
			}
		}
		if !matched {
			if defaultGroup, ok := groupMap["Default"]; ok {
				defaultGroup.Windows = append(defaultGroup.Windows, win)
			}
		}
	}

	var nonEmpty []GroupedWindows
	for _, group := range result {
		if len(group.Windows) > 0 {
			// Sort windows by index within each group
			sort.Slice(group.Windows, func(i, j int) bool {
				return group.Windows[i].Index < group.Windows[j].Index
			})
			nonEmpty = append(nonEmpty, *group)
		}
	}

	return nonEmpty
}

func FindGroupTheme(groupName string, groups []config.Group) config.Theme {
	for _, group := range groups {
		if group.Name == groupName {
			return group.Theme
		}
	}
	return config.Theme{
		Bg:       "#000000",
		Fg:       "#ffffff",
		ActiveBg: "#333333",
		ActiveFg: "#ffffff",
		Icon:     "",
	}
}

func ShadeColorByIndex(baseColor string, index int) string {
	// Convert hex to RGB
	hex := baseColor
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return baseColor
	}

	r, errR := strconv.ParseInt(hex[0:2], 16, 64)
	g, errG := strconv.ParseInt(hex[2:4], 16, 64)
	b, errB := strconv.ParseInt(hex[4:6], 16, 64)
	if errR != nil || errG != nil || errB != nil {
		return baseColor
	}

	// Apply shade based on index (darker for higher indices)
	shadeAmount := float64(index) * 0.1
	if shadeAmount > 0.5 {
		shadeAmount = 0.5
	}

	// Darken the color
	nr := int64(float64(r) * (1.0 - shadeAmount))
	ng := int64(float64(g) * (1.0 - shadeAmount))
	nb := int64(float64(b) * (1.0 - shadeAmount))

	return fmt.Sprintf("#%02x%02x%02x", nr, ng, nb)
}

// LightenColor lightens a hex color by the given amount (0.0 to 1.0)
func LightenColor(baseColor string, amount float64) string {
	hex := baseColor
	if len(hex) > 0 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return baseColor
	}

	r, errR := strconv.ParseInt(hex[0:2], 16, 64)
	g, errG := strconv.ParseInt(hex[2:4], 16, 64)
	b, errB := strconv.ParseInt(hex[4:6], 16, 64)
	if errR != nil || errG != nil || errB != nil {
		return baseColor
	}

	// Lighten by moving towards white (255)
	nr := r + int64(float64(255-r)*amount)
	ng := g + int64(float64(255-g)*amount)
	nb := b + int64(float64(255-b)*amount)

	// Clamp to 255
	if nr > 255 {
		nr = 255
	}
	if ng > 255 {
		ng = 255
	}
	if nb > 255 {
		nb = 255
	}

	return fmt.Sprintf("#%02x%02x%02x", nr, ng, nb)
}
