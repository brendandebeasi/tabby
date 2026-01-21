package grouping

import (
	"fmt"
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

// GroupWindows organizes windows into groups based on the @tabby_group window option.
// Windows without a group assignment go to "Default".
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
		// Use the @tabby_group window option if set, otherwise Default
		groupName := win.Group
		if groupName == "" {
			groupName = "Default"
		}

		// Find the target group
		if targetGroup, ok := groupMap[groupName]; ok {
			targetGroup.Windows = append(targetGroup.Windows, win)
		} else {
			// Group not found in config, fall back to Default
			if defaultGroup, ok := groupMap["Default"]; ok {
				defaultGroup.Windows = append(defaultGroup.Windows, win)
			}
		}
	}

	// Collect non-empty groups, keeping Default first for stability
	var defaultGroup *GroupedWindows
	var otherGroups []GroupedWindows

	for _, group := range result {
		if len(group.Windows) > 0 {
			// Sort windows by index within each group
			sort.Slice(group.Windows, func(i, j int) bool {
				return group.Windows[i].Index < group.Windows[j].Index
			})
			if group.Name == "Default" {
				g := *group
				defaultGroup = &g
			} else {
				otherGroups = append(otherGroups, *group)
			}
		}
	}

	// Default group first (if exists), then other groups in config order
	var nonEmpty []GroupedWindows
	if defaultGroup != nil {
		nonEmpty = append(nonEmpty, *defaultGroup)
	}
	nonEmpty = append(nonEmpty, otherGroups...)

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

	// Darken by 8% per index, capped at 40%
	darken := float64(index) * 0.08
	if darken > 0.40 {
		darken = 0.40
	}

	nr := int64(float64(r) * (1.0 - darken))
	ng := int64(float64(g) * (1.0 - darken))
	nb := int64(float64(b) * (1.0 - darken))

	return fmt.Sprintf("#%02x%02x%02x", nr, ng, nb)
}

// SaturateColor returns a highly saturated version for active tabs
func SaturateColor(baseColor string) string {
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

	h, s, l := rgbToHsl(float64(r)/255, float64(g)/255, float64(b)/255)

	// Boost saturation and lightness for active tab
	s = s * 1.3
	if s > 1.0 {
		s = 1.0
	}
	l = l * 1.1
	if l > 0.85 {
		l = 0.85
	}
	if l < 0.5 {
		l = 0.5
	}

	nr, ng, nb := hslToRgb(h, s, l)
	return fmt.Sprintf("#%02x%02x%02x", int(nr*255), int(ng*255), int(nb*255))
}

// rgbToHsl converts RGB to HSL
func rgbToHsl(r, g, b float64) (h, s, l float64) {
	max := r
	if g > max {
		max = g
	}
	if b > max {
		max = b
	}
	min := r
	if g < min {
		min = g
	}
	if b < min {
		min = b
	}

	l = (max + min) / 2

	if max == min {
		h = 0
		s = 0
	} else {
		d := max - min
		if l > 0.5 {
			s = d / (2 - max - min)
		} else {
			s = d / (max + min)
		}

		switch max {
		case r:
			h = (g - b) / d
			if g < b {
				h += 6
			}
		case g:
			h = (b-r)/d + 2
		case b:
			h = (r-g)/d + 4
		}
		h *= 60
	}
	return
}

// hslToRgb converts HSL to RGB
func hslToRgb(h, s, l float64) (r, g, b float64) {
	if s == 0 {
		r = l
		g = l
		b = l
		return
	}

	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q

	r = hueToRgb(p, q, h/360+1.0/3)
	g = hueToRgb(p, q, h/360)
	b = hueToRgb(p, q, h/360-1.0/3)
	return
}

func hueToRgb(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6 {
		return p + (q-p)*6*t
	}
	if t < 1.0/2 {
		return q
	}
	if t < 2.0/3 {
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

// InactiveTabColor returns a slightly lighter, desaturated version for inactive tabs
// All inactive tabs use the same shade (no cascading)
// lighten: amount to add to lightness (default 0.04)
// saturate: saturation multiplier (default 0.85)
func InactiveTabColor(baseColor string, lighten, saturate float64) string {
	// Use defaults if zero values passed
	if lighten == 0 {
		lighten = 0.04
	}
	if saturate == 0 {
		saturate = 0.85
	}
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

	h, s, l := rgbToHsl(float64(r)/255, float64(g)/255, float64(b)/255)

	// Subtle adjustment: slightly lighten and minimally desaturate
	l = l + lighten
	if l > 0.75 {
		l = 0.75
	}
	s = s * saturate

	nr, ng, nb := hslToRgb(h, s, l)
	return fmt.Sprintf("#%02x%02x%02x", int(nr*255), int(ng*255), int(nb*255))
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
