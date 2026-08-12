package daemon

import (
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// HandleTheme is the synchronous entry point invoked by server.OnTheme. It
// serves the `tabby theme` CLI: reporting the current light/dark selection
// and flipping it.
//
// A flip writes config.yaml so the choice survives a daemon restart, then
// applies the newly selected theme to the live session. The on-disk config is
// re-read rather than snapshotted from c.config so a concurrent hand-edit to
// an unrelated key isn't clobbered by the write-back.
func (c *Coordinator) HandleTheme(req *daemon.ThemeRequest) *daemon.ThemeResponse {
	if req == nil {
		return &daemon.ThemeResponse{OK: false, Error: "nil request"}
	}

	switch req.Op {
	case daemon.ThemeOpGet:
		c.stateMu.RLock()
		mode := c.config.AutoTheme.Mode
		enabled := c.config.AutoTheme.Enabled
		c.stateMu.RUnlock()
		return &daemon.ThemeResponse{OK: true, Mode: mode, Theme: c.ActiveThemeName(), Enabled: enabled}

	case daemon.ThemeOpSet, daemon.ThemeOpToggle:
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return &daemon.ThemeResponse{OK: false, Error: "read config: " + err.Error()}
		}

		mode := cfg.AutoTheme.Mode
		if req.Op == daemon.ThemeOpToggle {
			if mode == "light" {
				mode = "dark"
			} else {
				mode = "light"
			}
		} else {
			if req.Mode != "light" && req.Mode != "dark" {
				return &daemon.ThemeResponse{OK: false, Error: `mode must be "light" or "dark"`}
			}
			mode = req.Mode
		}

		// Toggling is the act of choosing a variant, so it implies the
		// light/dark pair is in play even if the config had it off.
		cfg.AutoTheme.Mode = mode
		cfg.AutoTheme.Enabled = true
		if err := config.SaveConfig(configPath, cfg); err != nil {
			return &daemon.ThemeResponse{OK: false, Error: "write config: " + err.Error()}
		}

		c.stateMu.Lock()
		c.config.AutoTheme = cfg.AutoTheme
		c.stateMu.Unlock()

		// Best-effort: a failure here must not fail the tmux theme flip, which
		// has already been persisted and is the actual job.
		if cfg.AutoTheme.SyncClaudeCode {
			if err := syncClaudeCodeTheme(mode); err != nil {
				coordinatorDebugLog.Printf("claude theme sync: %v", err)
			}
		}

		name := resolveAutoTheme(cfg)
		if name == "" {
			return &daemon.ThemeResponse{OK: false, Error: "no theme configured for mode " + mode}
		}
		c.SetTheme(name)
		return &daemon.ThemeResponse{OK: true, Mode: mode, Theme: name, Enabled: true}
	}

	return &daemon.ThemeResponse{OK: false, Error: "unknown op " + string(req.Op)}
}
