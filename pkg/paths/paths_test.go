package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestDirs(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	// Unset env vars so they don't interfere
	t.Setenv("TABBY_CONFIG_DIR", "")
	t.Setenv("TABBY_STATE_DIR", "")
	// Override HOME so resolution uses our temp dirs
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

	got := ConfigDir()
	if got != override {
		t.Errorf("ConfigDir() = %q, want %q", got, override)
	}
}

func TestConfigDir_LegacyWinsWhenBothExist(t *testing.T) {
	tmp := setupTestDirs(t)

	// Create config at both new and legacy paths -- legacy should win
	newDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(newDir, 0755)
	os.WriteFile(filepath.Join(newDir, "config.yaml"), []byte("new"), 0644)

	legDir := filepath.Join(tmp, ".tmux", "plugins", "tmux-tabs")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "config.yaml"), []byte("old"), 0644)

	got := ConfigDir()
	if got != legDir {
		t.Errorf("ConfigDir() = %q, want legacy path %q (legacy wins when both exist)", got, legDir)
	}
}

func TestConfigDir_NewPathWinsAfterMigration(t *testing.T) {
	tmp := setupTestDirs(t)

	// Only new path exists (legacy removed = migration complete)
	newDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(newDir, 0755)
	os.WriteFile(filepath.Join(newDir, "config.yaml"), []byte("new"), 0644)

	got := ConfigDir()
	if got != newDir {
		t.Errorf("ConfigDir() = %q, want new path %q", got, newDir)
	}
}

func TestConfigDir_FallbackToLegacy(t *testing.T) {
	tmp := setupTestDirs(t)

	// Only create legacy path
	legDir := filepath.Join(tmp, ".tmux", "plugins", "tmux-tabs")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "config.yaml"), []byte("old"), 0644)

	got := ConfigDir()
	if got != legDir {
		t.Errorf("ConfigDir() = %q, want legacy path %q", got, legDir)
	}
}

func TestConfigDir_FreshInstall(t *testing.T) {
	tmp := setupTestDirs(t)

	// Neither path exists
	want := filepath.Join(tmp, ".config", "tabby")
	got := ConfigDir()
	if got != want {
		t.Errorf("ConfigDir() = %q, want canonical %q", got, want)
	}
}

func TestStateDir_EnvOverride(t *testing.T) {
	tmp := setupTestDirs(t)
	override := filepath.Join(tmp, "custom-state")
	os.MkdirAll(override, 0755)
	t.Setenv("TABBY_STATE_DIR", override)
	ResetForTest()

	got := StateDir()
	if got != override {
		t.Errorf("StateDir() = %q, want %q", got, override)
	}
}

func TestStateDir_NewPathWins(t *testing.T) {
	tmp := setupTestDirs(t)

	// Create new state dir (just needs to exist as a directory)
	newDir := filepath.Join(tmp, ".local", "state", "tabby")
	os.MkdirAll(newDir, 0755)

	// Also create legacy state file
	legDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "pet.json"), []byte("{}"), 0644)

	got := StateDir()
	if got != newDir {
		t.Errorf("StateDir() = %q, want new path %q", got, newDir)
	}
}

func TestStateDir_FallbackToLegacy(t *testing.T) {
	tmp := setupTestDirs(t)

	// Only create legacy state file
	legDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "pet.json"), []byte("{}"), 0644)

	got := StateDir()
	if got != legDir {
		t.Errorf("StateDir() = %q, want legacy path %q", got, legDir)
	}
}

func TestStateDir_FreshInstall(t *testing.T) {
	tmp := setupTestDirs(t)

	want := filepath.Join(tmp, ".local", "state", "tabby")
	got := StateDir()
	if got != want {
		t.Errorf("StateDir() = %q, want canonical %q", got, want)
	}
}

func TestConfigPath(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".config", "tabby", "config.yaml")
	got := ConfigPath()
	if got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestStatePath(t *testing.T) {
	tmp := setupTestDirs(t)
	want := filepath.Join(tmp, ".local", "state", "tabby", "pet.json")
	got := StatePath("pet.json")
	if got != want {
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
	if !dirExists(expected) {
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
	if !dirExists(expected) {
		t.Errorf("EnsureStateDir() did not create directory %q", expected)
	}
}

func TestStateDir_FallbackOnThoughtBuffer(t *testing.T) {
	tmp := setupTestDirs(t)

	legDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "thought_buffer.txt"), []byte("hello"), 0644)

	got := StateDir()
	if got != legDir {
		t.Errorf("StateDir() = %q, want legacy path %q (triggered by thought_buffer.txt)", got, legDir)
	}
}

func TestStateDir_FallbackOnWebToken(t *testing.T) {
	tmp := setupTestDirs(t)

	legDir := filepath.Join(tmp, ".config", "tabby")
	os.MkdirAll(legDir, 0755)
	os.WriteFile(filepath.Join(legDir, "web-token"), []byte("tok"), 0644)

	got := StateDir()
	if got != legDir {
		t.Errorf("StateDir() = %q, want legacy path %q (triggered by web-token)", got, legDir)
	}
}
