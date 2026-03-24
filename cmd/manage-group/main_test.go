package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/stretchr/testify/assert"
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

func TestAddGroup_AddsNewGroup(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	assert.NoError(t, addGroup(configPath, "NewTeam"))

	cfg, err := config.LoadConfig(configPath)
	assert.NoError(t, err)
	assert.NotNil(t, config.FindGroup(cfg, "NewTeam"))
}

func TestAddGroup_DuplicateReturnsError(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := addGroup(configPath, "Work")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupExists))
}

func TestAddGroup_MissingConfigReturnsError(t *testing.T) {
	err := addGroup("/nonexistent/path/config.yaml", "X")
	assert.Error(t, err)
}

func TestDeleteGroup_RemovesGroup(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	assert.NoError(t, deleteGroup(configPath, "Work"))

	cfg, err := config.LoadConfig(configPath)
	assert.NoError(t, err)
	assert.Nil(t, config.FindGroup(cfg, "Work"))
}

func TestDeleteGroup_NonExistentReturnsError(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := deleteGroup(configPath, "Ghost")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupNotFound))
}

func TestDeleteGroup_MissingConfigReturnsError(t *testing.T) {
	err := deleteGroup("/nonexistent/path/config.yaml", "X")
	assert.Error(t, err)
}

func TestRenameGroup_RenamesGroup(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	assert.NoError(t, renameGroup(configPath, "Work", "Personal"))

	cfg, err := config.LoadConfig(configPath)
	assert.NoError(t, err)
	assert.Nil(t, config.FindGroup(cfg, "Work"), "old name should be gone")
	assert.NotNil(t, config.FindGroup(cfg, "Personal"), "new name should exist")
}

func TestRenameGroup_NonExistentReturnsError(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := renameGroup(configPath, "Ghost", "NewName")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupNotFound))
}

func TestRenameGroup_NewNameAlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := renameGroup(configPath, "Work", "Default")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupExists))
}

func TestRenameGroup_MissingConfigReturnsError(t *testing.T) {
	err := renameGroup("/nonexistent/path/config.yaml", "X", "Y")
	assert.Error(t, err)
}

func TestSetGroupColor_GroupNotFound(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := setGroupColor(configPath, "Ghost", "#ff0000")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupNotFound))
}

func TestSetGroupColor_MissingConfigReturnsError(t *testing.T) {
	err := setGroupColor("/nonexistent/path/config.yaml", "Work", "#ff0000")
	assert.Error(t, err)
}

func TestSetGroupMarker_GroupNotFound(t *testing.T) {
	tmp := t.TempDir()
	configPath := writeTestConfig(t, tmp)

	err := setGroupMarker(configPath, "Ghost", "★")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrGroupNotFound))
}

func TestSetGroupMarker_MissingConfigReturnsError(t *testing.T) {
	err := setGroupMarker("/nonexistent/path/config.yaml", "Work", "★")
	assert.Error(t, err)
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
