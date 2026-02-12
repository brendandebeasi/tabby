package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func writeTestConfig(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	contents := `groups:
  - name: Default
    pattern: ".*"
    theme:
      bg: "#2c3e50"
      fg: "#ecf0f1"
      active_bg: "#34495e"
      active_fg: "#ffffff"
      icon: ""
  - name: Work
    pattern: "^work"
    theme:
      bg: "#3498db"
      fg: "#ecf0f1"
      active_bg: "#2980b9"
      active_fg: "#ffffff"
      icon: ""
`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestSetGroupColorPersistsConfig(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	if err := setGroupColor(configPath, "Work", "#ff00aa"); err != nil {
		t.Fatalf("setGroupColor failed: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	group := config.FindGroup(cfg, "Work")
	if group == nil {
		t.Fatalf("group Work not found")
	}
	if group.Theme.Bg != "#ff00aa" {
		t.Fatalf("expected bg #ff00aa, got %s", group.Theme.Bg)
	}
}

func TestSetGroupMarkerPersistsConfig(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	if err := setGroupMarker(configPath, "Work", "🚀"); err != nil {
		t.Fatalf("setGroupMarker failed: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	group := config.FindGroup(cfg, "Work")
	if group == nil {
		t.Fatalf("group Work not found")
	}
	if group.Theme.Icon != "🚀" {
		t.Fatalf("expected marker 🚀, got %s", group.Theme.Icon)
	}
}
