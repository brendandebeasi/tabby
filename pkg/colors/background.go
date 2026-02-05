package colors

import (
	"os"
	"strconv"
	"strings"

	"github.com/muesli/termenv"
)

// ThemeMode represents the theme detection mode
type ThemeMode string

const (
	ThemeModeAuto  ThemeMode = "auto"
	ThemeModeDark  ThemeMode = "dark"
	ThemeModeLight ThemeMode = "light"
)

// BackgroundDetector handles detection of terminal background theme
type BackgroundDetector struct {
	mode          ThemeMode
	cachedIsDark  *bool
	detectedColor string
}

// NewBackgroundDetector creates a new background detector with the given mode
func NewBackgroundDetector(mode ThemeMode) *BackgroundDetector {
	return &BackgroundDetector{
		mode: mode,
	}
}

// IsDarkBackground returns true if the background is dark
func (d *BackgroundDetector) IsDarkBackground() bool {
	// If already detected, return cached value
	if d.cachedIsDark != nil {
		return *d.cachedIsDark
	}

	var isDark bool

	switch d.mode {
	case ThemeModeDark:
		isDark = true
	case ThemeModeLight:
		isDark = false
	case ThemeModeAuto:
		isDark = d.detectDarkBackground()
	default:
		isDark = d.detectDarkBackground()
	}

	// Cache the result
	d.cachedIsDark = &isDark
	return isDark
}

// detectDarkBackground uses multiple methods to detect if background is dark
func (d *BackgroundDetector) detectDarkBackground() bool {
	// Method 1: Try COLORFGBG environment variable (fastest, most reliable)
	if isDark, ok := d.checkCOLORFGBG(); ok {
		return isDark
	}

	// Method 2: Try termenv's background detection (uses OSC queries)
	if isDark, ok := d.checkTermenvBackground(); ok {
		return isDark
	}

	// Method 3: Check common terminal environment variables
	if isDark, ok := d.checkTerminalHints(); ok {
		return isDark
	}

	// Default: assume dark background (most common for terminal users)
	return true
}

// checkCOLORFGBG checks the COLORFGBG environment variable
// Format is typically "foreground;background" where values are ANSI color codes
// Values 0-7 are considered dark, 8-15 are light
func (d *BackgroundDetector) checkCOLORFGBG() (bool, bool) {
	colorFGBG := os.Getenv("COLORFGBG")
	if colorFGBG == "" {
		return false, false
	}

	parts := strings.Split(colorFGBG, ";")
	if len(parts) < 2 {
		return false, false
	}

	// Get the background value (last part)
	bgStr := parts[len(parts)-1]
	bg, err := strconv.Atoi(bgStr)
	if err != nil {
		return false, false
	}

	// ANSI colors: 0-7 are dark, 8-15 are light
	// 0=black, 7=light gray (both dark backgrounds)
	// 15=white, bright colors are light backgrounds
	isDark := bg < 8 || bg == 16

	return isDark, true
}

// checkTermenvBackground uses termenv to query the terminal background
// This sends OSC escape sequences to the terminal, which may not work in all environments
func (d *BackgroundDetector) checkTermenvBackground() (bool, bool) {
	// Note: This won't work in tmux/screen as they don't support OSC queries
	// termenv already handles this internally
	output := termenv.NewOutput(os.Stdout)

	// Try to get background color
	bgColor := output.BackgroundColor()

	// Check if we got a valid color (not NoColor)
	if bgColor == nil {
		return false, false
	}
	if _, ok := bgColor.(termenv.NoColor); ok {
		return false, false
	}

	// Use termenv's built-in dark background detection
	isDark := output.HasDarkBackground()

	// Store detected color for debugging
	rgb := termenv.ConvertToRGB(bgColor)
	d.detectedColor = rgb.Hex()

	return isDark, true
}

// checkTerminalHints checks for terminal-specific hints about theme
func (d *BackgroundDetector) checkTerminalHints() (bool, bool) {
	// iTerm2 sets this variable
	if os.Getenv("ITERM_PROFILE") != "" {
		profile := os.Getenv("ITERM_PROFILE")
		profileLower := strings.ToLower(profile)
		if strings.Contains(profileLower, "light") {
			return false, true
		}
		if strings.Contains(profileLower, "dark") {
			return true, true
		}
	}

	// Ghostty - try to read config file (works even inside tmux)
	// Check env var first, but also try config file as fallback
	if isDark, ok := d.checkGhosttyConfig(); ok {
		return isDark, true
	}

	// VS Code terminal
	if os.Getenv("TERM_PROGRAM") == "vscode" {
		// VS Code doesn't expose theme, but we can check COLORFGBG which it sets
		// Already checked above, so no additional info here
	}

	// Alacritty doesn't set any theme indicators unfortunately

	return false, false
}

// checkGhosttyConfig reads Ghostty config to detect background color
func (d *BackgroundDetector) checkGhosttyConfig() (bool, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, false
	}

	configPath := home + "/.config/ghostty/config"
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, false
	}

	// Parse config for background color
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "background") && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				bgColor := strings.TrimSpace(parts[1])
				// Remove any comments
				if idx := strings.Index(bgColor, "#"); idx > 0 {
					bgColor = strings.TrimSpace(bgColor[:idx])
				}
				// Ensure it's a hex color
				if !strings.HasPrefix(bgColor, "#") {
					bgColor = "#" + bgColor
				}
				d.detectedColor = bgColor
				// Check if it's a light color
				isDark := !IsLightColor(bgColor)
				return isDark, true
			}
		}
	}

	return false, false
}

// GetDetectedColor returns the detected background color hex if available
func (d *BackgroundDetector) GetDetectedColor() string {
	return d.detectedColor
}

// AdjustForegroundForBackground adjusts a foreground color to be legible on the detected background
// If color is empty, returns a sensible default based on background
func (d *BackgroundDetector) AdjustForegroundForBackground(fgColor string) string {
	isDark := d.IsDarkBackground()

	// If no color specified, return appropriate default
	if fgColor == "" {
		if isDark {
			return "#ffffff" // White text on dark background
		}
		return "#000000" // Black text on light background
	}

	// If color is already specified, return as-is
	// (User has explicitly configured it, respect their choice)
	return fgColor
}

// GetDefaultTextColor returns appropriate text color for the background
func (d *BackgroundDetector) GetDefaultTextColor() string {
	if d.IsDarkBackground() {
		return "#cccccc" // Light gray for dark backgrounds
	}
	return "#333333" // Dark gray for light backgrounds
}

// GetDefaultHeaderTextColor returns appropriate header text color for the background
func (d *BackgroundDetector) GetDefaultHeaderTextColor() string {
	if d.IsDarkBackground() {
		return "#ffffff" // White for dark backgrounds
	}
	return "#000000" // Black for light backgrounds
}

// GetDefaultInactiveTextColor returns appropriate inactive text color for the background
func (d *BackgroundDetector) GetDefaultInactiveTextColor() string {
	if d.IsDarkBackground() {
		return "#888888" // Mid gray for dark backgrounds
	}
	return "#9893a5" // Muted purple-gray for light backgrounds
}

// GetDefaultSidebarBg returns appropriate sidebar background color
func (d *BackgroundDetector) GetDefaultSidebarBg() string {
	if d.IsDarkBackground() {
		return "#1a1a2e" // Dark blue-gray
	}
	return "#faf4ed" // Warm cream (Rose Pine Dawn base)
}

// GetDefaultDisclosureFg returns appropriate disclosure icon color
func (d *BackgroundDetector) GetDefaultDisclosureFg() string {
	if d.IsDarkBackground() {
		return "#e8e8e8" // Off white
	}
	return "#797593" // Subtle purple-gray
}

// GetDefaultTreeFg returns appropriate tree branch color
func (d *BackgroundDetector) GetDefaultTreeFg() string {
	return "#666666" // Mid gray
}

// GetDefaultPaneHeaderActiveBg returns default active pane header background
func (d *BackgroundDetector) GetDefaultPaneHeaderActiveBg() string {
	if d.IsDarkBackground() {
		return "#3498db" // Blue
	}
	return "#f2e9e1" // Warm overlay
}

// GetDefaultPaneHeaderActiveFg returns default active pane header text
func (d *BackgroundDetector) GetDefaultPaneHeaderActiveFg() string {
	if d.IsDarkBackground() {
		return "#ffffff" // White
	}
	return "#575279" // Dark purple text
}

// GetDefaultPaneHeaderInactiveBg returns default inactive pane header background
func (d *BackgroundDetector) GetDefaultPaneHeaderInactiveBg() string {
	if d.IsDarkBackground() {
		return "#333333" // Dark gray
	}
	return "#fffaf3" // Light surface
}

// GetDefaultPaneHeaderInactiveFg returns default inactive pane header text
func (d *BackgroundDetector) GetDefaultPaneHeaderInactiveFg() string {
	if d.IsDarkBackground() {
		return "#cccccc" // Light gray
	}
	return "#9893a5" // Muted text
}

// GetDefaultCommandFg returns default command/dimmed text color
func (d *BackgroundDetector) GetDefaultCommandFg() string {
	if d.IsDarkBackground() {
		return "#aaaaaa" // Light gray
	}
	return "#797593" // Subtle text
}

// GetDefaultButtonFg returns default button text color
func (d *BackgroundDetector) GetDefaultButtonFg() string {
	if d.IsDarkBackground() {
		return "#888888" // Mid gray
	}
	return "#9893a5" // Muted
}

// GetDefaultBorderFg returns default border color
func (d *BackgroundDetector) GetDefaultBorderFg() string {
	if d.IsDarkBackground() {
		return "#444444" // Dark gray
	}
	return "#dfdad9" // Warm gray border
}

// GetDefaultHandleColor returns default drag handle color
func (d *BackgroundDetector) GetDefaultHandleColor() string {
	if d.IsDarkBackground() {
		return "#666666" // Gray
	}
	return "#9893a5" // Muted
}

// GetDefaultTerminalBg returns terminal background for hiding borders
func (d *BackgroundDetector) GetDefaultTerminalBg() string {
	if d.IsDarkBackground() {
		return "#1e1e1e" // Dark
	}
	return "#faf4ed" // Light cream
}

// GetDefaultDividerFg returns default divider color
func (d *BackgroundDetector) GetDefaultDividerFg() string {
	if d.IsDarkBackground() {
		return "#444444" // Dark gray
	}
	return "#dfdad9" // Warm gray
}

// GetDefaultPromptFg returns default prompt text color
func (d *BackgroundDetector) GetDefaultPromptFg() string {
	if d.IsDarkBackground() {
		return "#000000" // Black on light prompt bg
	}
	return "#575279" // Dark purple text
}

// GetDefaultPromptBg returns default prompt background color
func (d *BackgroundDetector) GetDefaultPromptBg() string {
	if d.IsDarkBackground() {
		return "#f0f0f0" // Light gray
	}
	return "#f2e9e1" // Warm overlay
}

// GetDefaultWidgetFg returns default widget text color
func (d *BackgroundDetector) GetDefaultWidgetFg() string {
	if d.IsDarkBackground() {
		return "#aaaaaa" // Light gray
	}
	return "#797593" // Subtle text
}
