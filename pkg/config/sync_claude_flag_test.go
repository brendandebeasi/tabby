package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoThemeSyncClaudeCodeParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "auto_theme:\n  enabled: true\n  mode: dark\n  light: rose-pine-dawn\n  dark: rose-pine\n  sync_claude_code: true\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoTheme.SyncClaudeCode {
		t.Error("sync_claude_code did not round-trip as true")
	}
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AutoTheme.SyncClaudeCode {
		t.Error("sync_claude_code lost on save/reload round-trip")
	}
}
