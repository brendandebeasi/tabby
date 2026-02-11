// Package paths provides centralized path resolution for Tabby's config and state files.
//
// Target layout (XDG-style):
//
//	Config:  ~/.config/tabby/config.yaml   (override: TABBY_CONFIG_DIR)
//	State:   ~/.local/state/tabby/         (override: TABBY_STATE_DIR)
//	Runtime: /tmp/tabby-*                  (unchanged)
//
// Legacy paths are supported transparently: if the new path doesn't exist but
// the legacy path does, the legacy path is used with a one-time deprecation
// notice to stderr.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	configDirOnce   sync.Once
	configDirCached string

	stateDirOnce   sync.Once
	stateDirCached string
)

// legacyConfigDir returns the old config path: ~/.tmux/plugins/tmux-tabs/
func legacyConfigDir(home string) string {
	return filepath.Join(home, ".tmux", "plugins", "tmux-tabs")
}

// legacyStateDir returns the old state path (state files lived alongside config): ~/.config/tabby/
func legacyStateDir(home string) string {
	return filepath.Join(home, ".config", "tabby")
}

// ConfigDir resolves the config directory.
// Priority: TABBY_CONFIG_DIR env > ~/.config/tabby/ > legacy ~/.tmux/plugins/tmux-tabs/
func ConfigDir() string {
	configDirOnce.Do(func() {
		configDirCached = resolveConfigDir()
	})
	return configDirCached
}

func resolveConfigDir() string {
	if env := os.Getenv("TABBY_CONFIG_DIR"); env != "" {
		return env
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	newDir := filepath.Join(home, ".config", "tabby")
	legacyDir := legacyConfigDir(home)
	legacyConfig := filepath.Join(legacyDir, "config.yaml")

	// Legacy path takes priority when it exists -- this is what was
	// actually in use before the migration. New path only wins once
	// the user has explicitly removed the legacy config.
	if fileExists(legacyConfig) {
		newConfig := filepath.Join(newDir, "config.yaml")
		if fileExists(newConfig) {
			fmt.Fprintf(os.Stderr, "[tabby] deprecation: config exists at both %s and %s -- using legacy; delete legacy when ready to migrate\n", legacyDir, newDir)
		} else {
			fmt.Fprintf(os.Stderr, "[tabby] deprecation: reading config from %s -- move it to %s\n", legacyDir, newDir)
		}
		return legacyDir
	}

	// No legacy config: use new path (migrated or fresh install)
	return newDir
}

// StateDir resolves the state directory.
// Priority: TABBY_STATE_DIR env > ~/.local/state/tabby/ > legacy ~/.config/tabby/
func StateDir() string {
	stateDirOnce.Do(func() {
		stateDirCached = resolveStateDir()
	})
	return stateDirCached
}

func resolveStateDir() string {
	if env := os.Getenv("TABBY_STATE_DIR"); env != "" {
		return env
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	newDir := filepath.Join(home, ".local", "state", "tabby")

	if dirExists(newDir) {
		return newDir
	}

	// Check if any state files exist at the legacy location
	legDir := legacyStateDir(home)
	stateFiles := []string{"pet.json", "thought_buffer.txt", "web-token"}
	for _, f := range stateFiles {
		if fileExists(filepath.Join(legDir, f)) {
			fmt.Fprintf(os.Stderr, "[tabby] deprecation: reading state from %s -- move state files to %s\n", legDir, newDir)
			return legDir
		}
	}

	// Fresh install: return the new canonical path
	return newDir
}

// ConfigPath returns the full path to config.yaml.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

// StatePath returns the full path to a state file (e.g. "pet.json").
func StatePath(filename string) string {
	return filepath.Join(StateDir(), filename)
}

// EnsureConfigDir creates the config directory if it doesn't exist and returns its path.
func EnsureConfigDir() (string, error) {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", dir, err)
	}
	return dir, nil
}

// EnsureStateDir creates the state directory if it doesn't exist and returns its path.
func EnsureStateDir() (string, error) {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create state dir %s: %w", dir, err)
	}
	return dir, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ResetForTest clears cached values so tests can re-run resolution logic.
// Only use in tests.
func ResetForTest() {
	configDirOnce = sync.Once{}
	configDirCached = ""
	stateDirOnce = sync.Once{}
	stateDirCached = ""
}
