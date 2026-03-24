package tmux

import (
	"os/exec"
	"strconv"
	"strings"
)

// ComputeResponsiveSidebarWidth is the pure, testable core of ResponsiveSidebarWidth.
func ComputeResponsiveSidebarWidth(windowWidth, mobileMax, tabletMax, mobileWidth, tabletWidth, desktopWidth, maxPercent, minContentCols int) int {
	// desktop
	if windowWidth > tabletMax {
		return desktopWidth
	}
	// tablet
	if windowWidth > mobileMax {
		if tabletWidth < 15 {
			tabletWidth = 15
		}
		return tabletWidth
	}
	// mobile: apply fraction and content caps
	maxByFraction := windowWidth * maxPercent / 100
	if maxByFraction < 15 {
		maxByFraction = 15
	}

	maxByContent := windowWidth - minContentCols
	if maxByContent < 15 {
		maxByContent = 15
	}

	maxReasonable := maxByFraction
	if maxByContent < maxReasonable {
		maxReasonable = maxByContent
	}
	if mobileWidth < maxReasonable {
		maxReasonable = mobileWidth
	}
	if maxReasonable < 10 {
		maxReasonable = 10
	}

	return maxReasonable
}

// ResponsiveSidebarWidth computes the appropriate sidebar width for a given window,
// accounting for mobile/tablet/desktop breakpoints and content constraints.
// windowID: the tmux window ID to compute width for
// globalWidth: the saved global sidebar width (typically 25)
// Returns the responsive width as an int.
func ResponsiveSidebarWidth(windowID string, globalWidth int) int {
	// Get window width
	windowWidthOut, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width}").Output()
	if err != nil {
		// Fall back to globalWidth on query failure
		return globalWidth
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(windowWidthOut)))
	if err != nil || windowWidth <= 0 {
		return globalWidth
	}

	// Read mobile/tablet thresholds
	mobileMaxWindowCols := 110
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_max_window_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 60 {
			mobileMaxWindowCols = v
		}
	}

	tabletMaxWindowCols := 170
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_tablet_max_window_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= mobileMaxWindowCols {
			tabletMaxWindowCols = v
		}
	}

	// Read width presets for each tier
	widthDesktop := globalWidth
	if widthDesktop < 15 {
		widthDesktop = 25
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_desktop").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 15 {
			widthDesktop = v
		}
	}

	// Desktop: window > tabletMaxWindowCols
	if windowWidth > tabletMaxWindowCols {
		return widthDesktop
	}

	// Tablet: window > mobileMaxWindowCols
	widthTablet := 20
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_tablet").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 15 {
			widthTablet = v
		}
	}
	if windowWidth > mobileMaxWindowCols {
		if widthTablet < 15 {
			widthTablet = 15
		}
		return widthTablet
	}

	// Mobile: window <= mobileMaxWindowCols
	widthMobile := 15
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_mobile").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 10 {
			widthMobile = v
		}
	}

	// Apply mobile constraints: maxByFraction and maxByContent
	maxPercent := 20
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_max_percent").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 10 && v <= 60 {
			maxPercent = v
		}
	}

	minContentCols := 40
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_min_content_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 20 {
			minContentCols = v
		}
	}

	return ComputeResponsiveSidebarWidth(windowWidth, mobileMaxWindowCols, tabletMaxWindowCols, widthMobile, widthTablet, widthDesktop, maxPercent, minContentCols)
}
