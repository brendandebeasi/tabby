package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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
