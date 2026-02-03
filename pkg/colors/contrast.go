package colors

import (
	"math"
	"strconv"
	"strings"
)

// GetLuminance calculates the relative luminance of a color per WCAG formula
// Returns a value between 0 (black) and 1 (white)
func GetLuminance(hexColor string) float64 {
	r, g, b := hexToRGB(hexColor)
	if r < 0 {
		return 0 // Invalid color
	}

	// Convert to 0-1 range
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	// Apply gamma correction
	rs := gammaSRGB(rf)
	gs := gammaSRGB(gf)
	bs := gammaSRGB(bf)

	// Calculate relative luminance using WCAG formula
	return 0.2126*rs + 0.7152*gs + 0.0722*bs
}

// gammaSRGB applies sRGB gamma correction
func gammaSRGB(val float64) float64 {
	if val <= 0.03928 {
		return val / 12.92
	}
	return math.Pow((val+0.055)/1.055, 2.4)
}

// GetContrastRatio calculates the WCAG contrast ratio between two colors
// Returns a value between 1 (no contrast) and 21 (maximum contrast)
func GetContrastRatio(fg, bg string) float64 {
	l1 := GetLuminance(fg)
	l2 := GetLuminance(bg)

	// Ensure l1 is the lighter color
	if l1 < l2 {
		l1, l2 = l2, l1
	}

	return (l1 + 0.05) / (l2 + 0.05)
}

// EnsureContrast adjusts the foreground color to meet minimum contrast ratio
// minRatio should be 4.5 for WCAG AA, 7.0 for WCAG AAA
func EnsureContrast(fg, bg string, minRatio float64) string {
	ratio := GetContrastRatio(fg, bg)
	if ratio >= minRatio {
		return fg // Already meets requirement
	}

	// Determine if we should lighten or darken the fg
	bgLum := GetLuminance(bg)
	fgLum := GetLuminance(fg)

	// Try adjusting in steps
	for adjustment := 0.1; adjustment <= 1.0; adjustment += 0.1 {
		var adjusted string
		if fgLum > bgLum {
			// Foreground is lighter, make it even lighter
			adjusted = lightenColorBy(fg, adjustment)
		} else {
			// Foreground is darker, make it even darker
			adjusted = darkenColorBy(fg, adjustment)
		}

		if GetContrastRatio(adjusted, bg) >= minRatio {
			return adjusted
		}
	}

	// If we still can't meet contrast, use pure white or black
	if bgLum > 0.5 {
		return "#000000" // Dark text on light background
	}
	return "#ffffff" // Light text on dark background
}

// IsLightColor returns true if the color is closer to white than black
func IsLightColor(hexColor string) bool {
	return GetLuminance(hexColor) > 0.5
}

// hexToRGB converts hex color to RGB values (0-255)
// Returns -1, -1, -1 for invalid colors
func hexToRGB(hexColor string) (int64, int64, int64) {
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

// lightenColorBy lightens a color by a given amount (0.0 to 1.0)
func lightenColorBy(hexColor string, amount float64) string {
	r, g, b := hexToRGB(hexColor)
	if r < 0 {
		return hexColor
	}

	// Move towards white
	nr := r + int64(float64(255-r)*amount)
	ng := g + int64(float64(255-g)*amount)
	nb := b + int64(float64(255-b)*amount)

	return rgbToHex(nr, ng, nb)
}

// darkenColorBy darkens a color by a given amount (0.0 to 1.0)
func darkenColorBy(hexColor string, amount float64) string {
	r, g, b := hexToRGB(hexColor)
	if r < 0 {
		return hexColor
	}

	// Move towards black
	multiplier := 1.0 - amount
	nr := int64(float64(r) * multiplier)
	ng := int64(float64(g) * multiplier)
	nb := int64(float64(b) * multiplier)

	return rgbToHex(nr, ng, nb)
}

// rgbToHex converts RGB values to hex color string
func rgbToHex(r, g, b int64) string {
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

	return "#" + toHex(r) + toHex(g) + toHex(b)
}

// toHex converts a single byte to 2-digit hex
func toHex(val int64) string {
	hex := strconv.FormatInt(val, 16)
	if len(hex) == 1 {
		return "0" + hex
	}
	return hex
}
