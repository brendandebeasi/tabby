package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

var (
	ErrGroupNotFound     = errors.New("group not found")
	ErrGroupExists       = errors.New("group already exists")
	ErrCannotDeleteGroup = errors.New("cannot delete this group")
)

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

// SaveConfig writes the config to the specified path
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// AddGroup adds a new group to the config.
// The group is inserted before the "Default" group to ensure pattern matching order.
func AddGroup(cfg *Config, group Group) error {
	// Check if group name already exists
	for _, g := range cfg.Groups {
		if g.Name == group.Name {
			return ErrGroupExists
		}
	}

	// Find the Default group index and insert before it
	defaultIdx := -1
	for i, g := range cfg.Groups {
		if g.Name == "Default" {
			defaultIdx = i
			break
		}
	}

	if defaultIdx == -1 {
		// No Default group, just append
		cfg.Groups = append(cfg.Groups, group)
	} else {
		// Insert before Default
		cfg.Groups = append(cfg.Groups[:defaultIdx], append([]Group{group}, cfg.Groups[defaultIdx:]...)...)
	}
	return nil
}

// UpdateGroup updates an existing group by name
func UpdateGroup(cfg *Config, oldName string, group Group) error {
	for i, g := range cfg.Groups {
		if g.Name == oldName {
			cfg.Groups[i] = group
			return nil
		}
	}
	return ErrGroupNotFound
}

// DeleteGroup removes a group by name.
// The "Default" group cannot be deleted.
func DeleteGroup(cfg *Config, name string) error {
	if name == "Default" {
		return ErrCannotDeleteGroup
	}

	for i, g := range cfg.Groups {
		if g.Name == name {
			cfg.Groups = append(cfg.Groups[:i], cfg.Groups[i+1:]...)
			return nil
		}
	}
	return ErrGroupNotFound
}

// FindGroup returns a pointer to the group with the given name, or nil if not found
func FindGroup(cfg *Config, name string) *Group {
	for i := range cfg.Groups {
		if cfg.Groups[i].Name == name {
			return &cfg.Groups[i]
		}
	}
	return nil
}

// DefaultGroup returns a new group with default theme colors
func DefaultGroup(name string) Group {
	return Group{
		Name:    name,
		Pattern: fmt.Sprintf("^%s\\|", name),
		Theme: Theme{
			Bg:       "#3498db",
			Fg:       "#ecf0f1",
			ActiveBg: "#2980b9",
			ActiveFg: "#ffffff",
			Icon:     "",
		},
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Indicators.Activity.Icon == "" {
		cfg.Indicators.Activity.Icon = "●"
		cfg.Indicators.Activity.Color = "#f39c12"
	}
	if cfg.Indicators.Bell.Icon == "" {
		cfg.Indicators.Bell.Icon = "🔔"
		cfg.Indicators.Bell.Color = "#e74c3c"
	}
	if cfg.Indicators.Silence.Icon == "" {
		cfg.Indicators.Silence.Icon = "🔇"
		cfg.Indicators.Silence.Color = "#95a5a6"
	}
	if cfg.Indicators.Last.Icon == "" {
		cfg.Indicators.Last.Icon = "-"
		cfg.Indicators.Last.Color = "#3498db"
	}
}
