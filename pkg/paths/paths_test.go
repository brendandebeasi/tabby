package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestDirs(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TABBY_CONFIG_DIR", "")
	t.Setenv("TABBY_STATE_DIR", "")
	t.Setenv("HOME", tmp)
	ResetForTest()
	return tmp
}

func TestConfigDir_EnvOverride(t *testing.T) {
	tmp := setupTestDirs(t)
	override := filepath.Join(tmp, "custom-config")
	os.MkdirAll(override, 0755)
	t.Setenv("TABBY_CONFIG_DIR", override)
	ResetForTest()

	if got := ConfigDir(); got != override {
		t.Errorf("ConfigDir() = %q, want %q", got, override)
	}
}

func TestConfigDir_Default(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".config", "tabby")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestStateDir_EnvOverride(t *testing.T) {
	tmp := setupTestDirs(t)
	override := filepath.Join(tmp, "custom-state")
	os.MkdirAll(override, 0755)
	t.Setenv("TABBY_STATE_DIR", override)
	ResetForTest()

	if got := StateDir(); got != override {
		t.Errorf("StateDir() = %q, want %q", got, override)
	}
}

func TestStateDir_Default(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".local", "state", "tabby")
	if got := StateDir(); got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

func TestConfigPath(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".config", "tabby", "config.yaml")
	if got := ConfigPath(); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestStatePath(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".local", "state", "tabby", "pet.json")
	if got := StatePath("pet.json"); got != want {
		t.Errorf("StatePath(\"pet.json\") = %q, want %q", got, want)
	}
}

func TestEnsureConfigDir_Creates(t *testing.T) {
	tmp := setupTestDirs(t)
	expected := filepath.Join(tmp, ".config", "tabby")

	dir, err := EnsureConfigDir()
	if err != nil {
		t.Fatalf("EnsureConfigDir() error: %v", err)
	}
	if dir != expected {
		t.Errorf("EnsureConfigDir() = %q, want %q", dir, expected)
	}
	info, err := os.Stat(expected)
	if err != nil || !info.IsDir() {
		t.Errorf("EnsureConfigDir() did not create directory %q", expected)
	}
}

func TestEnsureStateDir_Creates(t *testing.T) {
	tmp := setupTestDirs(t)
	expected := filepath.Join(tmp, ".local", "state", "tabby")

	dir, err := EnsureStateDir()
	if err != nil {
		t.Fatalf("EnsureStateDir() error: %v", err)
	}
	if dir != expected {
		t.Errorf("EnsureStateDir() = %q, want %q", dir, expected)
	}
	info, err := os.Stat(expected)
	if err != nil || !info.IsDir() {
		t.Errorf("EnsureStateDir() did not create directory %q", expected)
	}
}
