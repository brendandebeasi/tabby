package colors

import (
	"fmt"
	"strconv"
	"strings"
)

// DeriveThemeColors generates a complete theme from a single base color
// Returns bg, fg, activeBg, activeFg, inactiveBg, inactiveFg
func DeriveThemeColors(baseColor string, isDarkTerminalBg bool) (string, string, string, string, string, string) {
	// Base color becomes the group background
	bg := baseColor

	// Derive active background (saturated and slightly adjusted)
	activeBg := DeriveActiveBg(baseColor, isDarkTerminalBg)

	// Derive inactive background (desaturated, subtle)
	inactiveBg := DeriveInactiveBg(baseColor, isDarkTerminalBg)

	// Derive foreground colors with good contrast
	fg := DeriveTextColor(bg)
	activeFg := DeriveTextColor(activeBg)
	inactiveFg := DeriveTextColor(inactiveBg)

	return bg, fg, activeBg, activeFg, inactiveBg, inactiveFg
}

// DeriveActiveBg creates a saturated, vibrant version for active tabs
func DeriveActiveBg(baseColor string, isDarkTerminalBg bool) string {
	h, s, l := hexToHSL(baseColor)
	if h < 0 {
		return baseColor // Invalid color
	}

	// Boost saturation significantly for active state
	s = s * 1.4
	if s > 1.0 {
		s = 1.0
	}

	// Adjust lightness based on terminal background
	// On dark terminals, make active tabs brighter
	// On light terminals, keep them moderately saturated
	if isDarkTerminalBg {
		l = l * 1.2
		if l > 0.6 {
			l = 0.6
		}
		if l < 0.35 {
			l = 0.35
		}
	} else {
		l = l * 0.9
		if l > 0.5 {
			l = 0.5
		}
		if l < 0.25 {
			l = 0.25
		}
	}

	return hslToHex(h, s, l)
}

// DeriveInactiveBg creates a subtle, desaturated version for inactive tabs
func DeriveInactiveBg(baseColor string, isDarkTerminalBg bool) string {
	h, s, l := hexToHSL(baseColor)
	if h < 0 {
		return baseColor // Invalid color
	}

	// Desaturate for inactive state
	s = s * 0.7

	// Adjust lightness to be less prominent
	if isDarkTerminalBg {
		l = l * 1.1
		if l > 0.45 {
			l = 0.45
		}
	} else {
		l = l * 0.95
		if l > 0.4 {
			l = 0.4
		}
	}

	return hslToHex(h, s, l)
}

// DeriveTextColor determines the best text color (white or black) for a background.
// Uses WCAG AA large-text threshold (3:1) to prefer white on colored backgrounds,
// falling back to black for truly light backgrounds.
func DeriveTextColor(bgColor string) string {
	// Prefer white on colored/dark backgrounds (3:1 = WCAG AA large text)
	if GetContrastRatio("#ffffff", bgColor) >= 3.0 {
		return "#ffffff"
	}

	// Fall back to black for light backgrounds
	if GetContrastRatio("#000000", bgColor) >= 3.0 {
		return "#000000"
	}

	// If neither works perfectly, choose based on luminance
	if IsLightColor(bgColor) {
		return "#000000" // Dark text on light bg
	}
	return "#ffffff" // Light text on dark bg
}

// hexToHSL converts hex color to HSL (hue 0-360, saturation 0-1, lightness 0-1)
// Returns -1, 0, 0 for invalid colors
func hexToHSL(hexColor string) (float64, float64, float64) {
	r, g, b := hexToRGBInternal(hexColor)
	if r < 0 {
		return -1, 0, 0
	}

	// Convert to 0-1 range
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	max := rf
	if gf > max {
		max = gf
	}
	if bf > max {
		max = bf
	}

	min := rf
	if gf < min {
		min = gf
	}
	if bf < min {
		min = bf
	}

	// Calculate lightness
	l := (max + min) / 2.0

	// Calculate saturation and hue
	var h, s float64
	if max == min {
		h = 0
		s = 0 // Achromatic (gray)
	} else {
		d := max - min

		// Calculate saturation
		if l > 0.5 {
			s = d / (2.0 - max - min)
		} else {
			s = d / (max + min)
		}

		// Calculate hue
		switch max {
		case rf:
			h = (gf - bf) / d
			if gf < bf {
				h += 6
			}
		case gf:
			h = (bf-rf)/d + 2
		case bf:
			h = (rf-gf)/d + 4
		}
		h *= 60 // Convert to degrees
	}

	return h, s, l
}

// hslToHex converts HSL to hex color
func hslToHex(h, s, l float64) string {
	r, g, b := hslToRGB(h, s, l)

	// Convert to 0-255 range
	rInt := int64(r * 255.0)
	gInt := int64(g * 255.0)
	bInt := int64(b * 255.0)

	return rgbToHexInternal(rInt, gInt, bInt)
}

// hslToRGB converts HSL to RGB (0-1 range)
func hslToRGB(h, s, l float64) (float64, float64, float64) {
	if s == 0 {
		// Achromatic (gray)
		return l, l, l
	}

	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q

	r := hueToRGB(p, q, h/360.0+1.0/3.0)
	g := hueToRGB(p, q, h/360.0)
	b := hueToRGB(p, q, h/360.0-1.0/3.0)

	return r, g, b
}

// hueToRGB helper for HSL to RGB conversion
func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6.0 {
		return p + (q-p)*6*t
	}
	if t < 1.0/2.0 {
		return q
	}
	if t < 2.0/3.0 {
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

// GetDefaultGroupColor returns a pleasant default color for a group
// Uses a predefined palette that works on both dark and light backgrounds
func GetDefaultGroupColor(groupIndex int) string {
	palette := []string{
		"#3498db", // Blue
		"#2ecc71", // Green
		"#e74c3c", // Red
		"#9b59b6", // Purple
		"#f39c12", // Orange
		"#1abc9c", // Turquoise
		"#e67e22", // Carrot
		"#34495e", // Dark blue-gray
		"#16a085", // Green sea
		"#c0392b", // Pomegranate
		"#8e44ad", // Wisteria
		"#27ae60", // Nephritis
	}

	return palette[groupIndex%len(palette)]
}

// SmartTextColor returns an intelligent text color based on background
// Uses both contrast checking and color temperature
func SmartTextColor(bgColor string, preferWarm bool) string {
	baseText := DeriveTextColor(bgColor)

	// If pure white/black works, we can optionally warm/cool it
	if !preferWarm {
		return baseText
	}

	// For warm preference, use slightly warm whites/blacks
	if baseText == "#ffffff" {
		// Warm white (very subtle cream)
		return "#fefefe"
	} else {
		// Warm black (very subtle brown)
		return "#0a0a0a"
	}
}

// AutoFillTheme fills in missing theme colors using intelligent derivation
// Takes a partial theme and terminal background context
func AutoFillTheme(bg, fg, activeBg, activeFg, inactiveBg, inactiveFg string, isDarkTerminalBg bool) (string, string, string, string, string, string) {
	// If no base color at all, use a default
	if bg == "" {
		bg = GetDefaultGroupColor(0)
	}

	// Derive all colors from base if they're missing
	_, derivedFg, derivedActiveBg, derivedActiveFg, derivedInactiveBg, derivedInactiveFg := DeriveThemeColors(bg, isDarkTerminalBg)

	// Use derived values only for missing fields
	if fg == "" {
		fg = derivedFg
	}
	if activeBg == "" {
		activeBg = derivedActiveBg
	}
	if activeFg == "" {
		activeFg = derivedActiveFg
	}
	if inactiveBg == "" {
		inactiveBg = derivedInactiveBg
	}
	if inactiveFg == "" {
		inactiveFg = derivedInactiveFg
	}

	// Ensure all foreground colors have sufficient contrast
	fg = EnsureContrast(fg, bg, 4.5)
	activeFg = EnsureContrast(activeFg, activeBg, 4.5)
	inactiveFg = EnsureContrast(inactiveFg, inactiveBg, 4.5)

	return bg, fg, activeBg, activeFg, inactiveBg, inactiveFg
}

// Internal helpers (avoid duplication with contrast.go exports)

// hexToRGBInternal converts hex color to RGB values (0-255)
func hexToRGBInternal(hexColor string) (int64, int64, int64) {
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return -1, -1, -1
	}

	r, errR := strconv.ParseInt(hex[0:2], 16, 64)
	g, errG := strconv.ParseInt(hex[2:4], 16, 64)
	b, errB := strconv.ParseInt(hex[4:6], 16, 64)

	if errR != nil || errG != nil || errB != nil {
		return -1, -1, -1
	}

	return r, g, b
}

// rgbToHexInternal converts RGB values to hex color string
func rgbToHexInternal(r, g, b int64) string {
	// Clamp values
	if r > 255 {
		r = 255
	}
	if r < 0 {
		r = 0
	}
	if g > 255 {
		g = 255
	}
	if g < 0 {
		g = 0
	}
	if b > 255 {
		b = 255
	}
	if b < 0 {
		b = 0
	}

	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
