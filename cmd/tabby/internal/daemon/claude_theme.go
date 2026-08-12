package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// claudeSettingsPath is where Claude Code keeps its user-scope settings.
func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// claudeThemeForMode maps a tabby light/dark mode onto a Claude Code theme
// name, preserving the variant family of whatever is already set.
//
// Claude Code ships three pairs: plain, -ansi, and -daltonized. Only the
// dark/light prefix encodes the mode; the suffix is a user preference that a
// theme flip must not silently discard -- dropping -daltonized would take
// away a colorblind user's palette, and dropping -ansi would override a
// deliberate choice to defer to the terminal's own 16 colors.
func claudeThemeForMode(current, mode string) string {
	suffix := ""
	if i := strings.IndexByte(current, '-'); i >= 0 {
		switch current[i:] {
		case "-ansi", "-daltonized":
			suffix = current[i:]
		}
	}
	if mode == "light" {
		return "light" + suffix
	}
	return "dark" + suffix
}

// syncClaudeCodeTheme rewrites the "theme" key in Claude Code's settings.json
// to match tabby's light/dark mode.
//
// Claude Code watches this file and reloads it, so a running session repaints
// without a restart (only "model" and "outputStyle" are read once at start).
//
// The file belongs to another tool and holds unrelated keys, so the whole
// document is decoded into a generic map and re-encoded with just that one key
// changed. Writing via a temp file and rename keeps a crash from truncating
// someone's hooks and permissions.
func syncClaudeCodeTheme(mode string) error {
	path := claudeSettingsPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// No Claude Code install is a normal state, not a theme-flip failure.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// A plain map preserves keys this build of tabby knows nothing about;
	// a typed struct would drop them on the round-trip.
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return err
	}

	current := ""
	if v, ok := settings["theme"]; ok {
		_ = json.Unmarshal(v, &current)
	}
	next := claudeThemeForMode(current, mode)
	if next == current {
		return nil
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	settings["theme"] = encoded

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	tmp := path + ".tabby.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
