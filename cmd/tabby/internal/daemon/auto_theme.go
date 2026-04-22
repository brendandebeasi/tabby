package daemon

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
)

// resolveAutoTheme returns the theme name that should currently be active
// according to the auto_theme config, or "" if auto_theme is disabled or
// nothing overrides the base theme.
func resolveAutoTheme(cfg *config.Config) string {
	if !cfg.AutoTheme.Enabled {
		return ""
	}
	switch cfg.AutoTheme.Mode {
	case "system":
		if isSystemDarkMode() {
			return cfg.AutoTheme.Dark
		}
		return cfg.AutoTheme.Light
	case "time":
		if isTimeInDarkPeriod(cfg.AutoTheme.TimeDark, cfg.AutoTheme.TimeLight) {
			return cfg.AutoTheme.Dark
		}
		return cfg.AutoTheme.Light
	}
	return ""
}

// isSystemDarkMode returns true if the OS is currently in dark mode.
// Resolution order:
//  1. tmux global env CLIENT_DARK_MODE (re-read every call so long-running
//     daemons pick up changes pushed by e.g. a LaunchAgent on the laptop
//     without needing a daemon restart).
//  2. Process env CLIENT_DARK_MODE (imported via SSH + tmux update-environment
//     at daemon spawn; frozen for the daemon's lifetime).
//  3. macOS `defaults read -g AppleInterfaceStyle`.
//  4. Linux GNOME via `gsettings` then KDE via `kreadconfig5`.
func isSystemDarkMode() bool {
	// 1. tmux global env -- dynamic, updated externally.
	if v := readTmuxGlobalEnv("CLIENT_DARK_MODE"); v != "" {
		if b, ok := parseBoolish(v); ok {
			return b
		}
	}

	// 2. Process env -- snapshot at daemon spawn.
	if v := strings.TrimSpace(os.Getenv("CLIENT_DARK_MODE")); v != "" {
		if b, ok := parseBoolish(v); ok {
			return b
		}
	}

	// macOS: `defaults read -g AppleInterfaceStyle` prints "Dark" in dark mode,
	// exits non-zero (and prints an error) when in light mode.
	if out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output(); err == nil {
		return strings.TrimSpace(string(out)) == "Dark"
	}

	// Linux GNOME: gsettings returns 'prefer-dark' or 'prefer-light'
	if out, err := exec.Command("gsettings", "get",
		"org.gnome.desktop.interface", "color-scheme").Output(); err == nil {
		return strings.Contains(string(out), "dark")
	}

	// Linux KDE (Plasma 5): check colour scheme setting
	if out, err := exec.Command("kreadconfig5", "--group", "General",
		"--key", "ColorScheme").Output(); err == nil {
		lower := strings.ToLower(string(out))
		return strings.Contains(lower, "dark") || strings.Contains(lower, "breeze-dark")
	}

	// xdg-desktop-portal (Flatpak / generic freedesktop):
	// org.freedesktop.portal.Settings Read org.freedesktop.appearance color-scheme
	// Returns 0=no preference 1=dark 2=light — too verbose to shell-out easily here.
	// Fall back to light.
	return false
}

// parseBoolish converts a human-typed truthy/falsy string into a bool.
// Empty input or an unrecognized value returns (false, false). This lets
// callers distinguish "user wrote something weird" from "user wrote nothing"
// and fall through to the next detection mechanism.
func parseBoolish(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "dark", "true", "yes", "on":
		return true, true
	case "0", "light", "false", "no", "off":
		return false, true
	}
	return false, false
}

// readTmuxGlobalEnv queries the tmux server's global environment for a
// single variable. Returns "" if tmux isn't reachable, the variable is
// unset, or the server responds with the "-NAME" unset marker.
//
// Called on every isSystemDarkMode check so that changes pushed via
// `tmux set-environment -g CLIENT_DARK_MODE ...` (e.g. from the laptop's
// LaunchAgent watching AppleInterfaceStyle) propagate to the daemon's
// next decision tick without needing a daemon restart.
//
// Cost: one fork+exec of `tmux` per call; ~1-2ms. The daemon's tick
// loop already runs on the order of seconds, so this is negligible.
func readTmuxGlobalEnv(name string) string {
	out, err := exec.Command("tmux", "show-environment", "-g", name).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	// `tmux show-environment -g X` prints either "X=value" or "-X" (unset).
	if prefix := name + "="; strings.HasPrefix(line, prefix) {
		return line[len(prefix):]
	}
	return ""
}

// isTimeInDarkPeriod returns true if the current local time falls inside the
// dark period defined by [darkStart, lightStart).
// Both strings must be "HH:MM" in 24-hour format; empty strings are ignored.
func isTimeInDarkPeriod(darkStart, lightStart string) bool {
	now := time.Now()
	dark := parseHHMM(darkStart, now)
	light := parseHHMM(lightStart, now)
	if dark.IsZero() || light.IsZero() {
		return false
	}
	cur := timeOfDay(now)
	darkM := timeOfDay(dark)
	lightM := timeOfDay(light)

	if darkM <= lightM {
		// Dark period doesn't cross midnight: [dark, light)
		return cur >= darkM && cur < lightM
	}
	// Dark period crosses midnight: [dark, 24h) ∪ [0, light)
	return cur >= darkM || cur < lightM
}

// parseHHMM parses "HH:MM" into a time.Time on the same calendar day as ref.
func parseHHMM(s string, ref time.Time) time.Time {
	if len(s) != 5 || s[2] != ':' {
		return time.Time{}
	}
	var h, m int
	if _, err := parseIntPair(s[0:2], &h); err != nil {
		return time.Time{}
	}
	if _, err := parseIntPair(s[3:5], &m); err != nil {
		return time.Time{}
	}
	return time.Date(ref.Year(), ref.Month(), ref.Day(), h, m, 0, 0, ref.Location())
}

// parseIntPair parses two ASCII digit characters into an int.
func parseIntPair(s string, out *int) (int, error) {
	if len(s) != 2 {
		return 0, &parseError{s}
	}
	hi := int(s[0] - '0')
	lo := int(s[1] - '0')
	if hi < 0 || hi > 9 || lo < 0 || lo > 9 {
		return 0, &parseError{s}
	}
	*out = hi*10 + lo
	return *out, nil
}

// timeOfDay returns minutes-since-midnight for t.
func timeOfDay(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

type parseError struct{ s string }

func (e *parseError) Error() string { return "invalid time: " + e.s }

// SetTheme updates the coordinator's active color theme at runtime.
// Safe to call from any goroutine; acquires stateMu for the write.
func (c *Coordinator) SetTheme(themeName string) {
	t := colors.GetTheme(themeName)
	c.stateMu.Lock()
	c.theme = &t
	c.config.Sidebar.Theme = themeName
	c.stateMu.Unlock()
}

// ActiveThemeName returns the name currently stored in the config under Sidebar.Theme.
func (c *Coordinator) ActiveThemeName() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.config.Sidebar.Theme
}

// ResolveAutoTheme returns the theme name that auto_theme wants right now,
// or "" if auto_theme is disabled. Reads config under stateMu.
func (c *Coordinator) ResolveAutoTheme() string {
	c.stateMu.RLock()
	cfg := c.config
	c.stateMu.RUnlock()
	return resolveAutoTheme(cfg)
}
