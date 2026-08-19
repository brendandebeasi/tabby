package daemon

import (
	"strings"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
)

// resolveAutoTheme returns the theme name the light/dark setting currently
// selects, or "" if it is disabled and the base theme should stand.
func resolveAutoTheme(cfg *config.Config) string {
	if !cfg.AutoTheme.Enabled {
		return ""
	}
	if cfg.AutoTheme.Mode == "light" {
		return cfg.AutoTheme.Light
	}
	return cfg.AutoTheme.Dark
}

// SetTheme updates the coordinator's active color theme at runtime.
// Safe to call from any goroutine; acquires stateMu for the write.
//
// After swapping the theme pointer, re-runs applyThemeToTmux so that
// global tmux options (window-style, window-active-style, message-style,
// pane-border-style, etc.) reflect the new theme. Without this, active
// panes fall through to a stale window-active-style from the previous
// theme -- e.g. after a flip from rose-pine-dawn to rose-pine, inactive
// panes dim toward black correctly but active panes keep the light
// theme's terminal bg.
func (c *Coordinator) SetTheme(themeName string) {
	t := colors.GetTheme(themeName)
	c.stateMu.Lock()
	c.theme = &t
	c.config.Sidebar.Theme = themeName
	c.stateMu.Unlock()
	c.applyThemeToTmux()
	c.repaintPanesForTheme()
}

// repaintPanesForTheme re-applies the per-pane window-style overrides after a
// theme change.
//
// applyThemeToTmux only rewrites the GLOBAL window-style/window-active-style,
// but every pane the daemon has painted carries a per-pane override (written by
// ApplyThemeToPane and ApplyPaneDimming), and in tmux a per-pane option always
// beats the global one. Without this pass a flip leaves already-painted panes
// on the previous theme's background while untouched panes pick up the new
// global -- the session ends up visibly split between two themes.
//
// ApplyPaneDimming is the right pass to reuse: it recomputes the tab-color tint
// against the NEW theme's terminal bg and re-composes inactive-pane dimming, so
// tinted panes land on correctly re-blended colors rather than the flat theme bg.
func (c *Coordinator) repaintPanesForTheme() {
	// Scope to this daemon's session: ApplyPaneDimming treats a window id from
	// another session as "no tint" and would unset styles another daemon owns.
	out, err := tmuxCmd("display-message", "-p", "-t", c.sessionID,
		"#{window_id}").Output()
	if err != nil {
		return
	}
	activeWindowID := strings.TrimSpace(string(out))
	if activeWindowID == "" {
		return
	}
	c.applyPaneDimming(activeWindowID, true)

	// The dimming pass deliberately skips sidebar/utility panes
	// (isDimSkipPane), so their background is owned by ApplyThemeToPane and
	// would otherwise keep the previous theme's color after a flip -- a
	// sidebar column visibly out of step with the content panes beside it.
	for _, p := range listDimPanesSession(c.sessionID) {
		if isDimSkipPane(p) {
			c.ApplyThemeToPane(p.id)
		}
	}
}

// ActiveThemeName returns the name currently stored in the config under Sidebar.Theme.
func (c *Coordinator) ActiveThemeName() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.config.Sidebar.Theme
}

// ResolveAutoTheme returns the theme name the light/dark setting wants right
// now, or "" if it is disabled. Reads config under stateMu.
func (c *Coordinator) ResolveAutoTheme() string {
	c.stateMu.RLock()
	cfg := c.config
	c.stateMu.RUnlock()
	return resolveAutoTheme(cfg)
}
