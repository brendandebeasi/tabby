// Package paths provides centralized path resolution for Tabby's config and state files.
//
// Layout (XDG-style):
//
//	Config:  ~/.config/tabby/config.yaml   (override: TABBY_CONFIG_DIR)
//	State:   ~/.local/state/tabby/         (override: TABBY_STATE_DIR)
//	Runtime: /tmp/tabby-*                  (unchanged)
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

// ConfigDir resolves the config directory.
// Priority: TABBY_CONFIG_DIR env > ~/.config/tabby/
func ConfigDir() string {
	configDirOnce.Do(func() {
		if env := os.Getenv("TABBY_CONFIG_DIR"); env != "" {
			configDirCached = env
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				configDirCached = "."
			} else {
				configDirCached = filepath.Join(home, ".config", "tabby")
			}
		}
	})
	return configDirCached
}

// StateDir resolves the state directory.
// Priority: TABBY_STATE_DIR env > ~/.local/state/tabby/
func StateDir() string {
	stateDirOnce.Do(func() {
		if env := os.Getenv("TABBY_STATE_DIR"); env != "" {
			stateDirCached = env
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				stateDirCached = "."
			} else {
				stateDirCached = filepath.Join(home, ".local", "state", "tabby")
			}
		}
	})
	return stateDirCached
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

// ResetForTest clears cached values so tests can re-run resolution logic.
// Only use in tests.
func ResetForTest() {
	configDirOnce = sync.Once{}
	configDirCached = ""
	stateDirOnce = sync.Once{}
	stateDirCached = ""
}
