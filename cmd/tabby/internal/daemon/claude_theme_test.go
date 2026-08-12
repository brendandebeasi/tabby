package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeThemeForModePreservesVariant(t *testing.T) {
	cases := []struct {
		current, mode, want string
	}{
		{"light-ansi", "dark", "dark-ansi"},
		{"dark-ansi", "light", "light-ansi"},
		{"light-daltonized", "dark", "dark-daltonized"},
		{"dark-daltonized", "light", "light-daltonized"},
		{"light", "dark", "dark"},
		{"dark", "light", "light"},
		{"", "dark", "dark"},
		{"nonsense-value", "light", "light"},
	}
	for _, c := range cases {
		if got := claudeThemeForMode(c.current, c.mode); got != c.want {
			t.Errorf("claudeThemeForMode(%q,%q) = %q, want %q", c.current, c.mode, got, c.want)
		}
	}
}

func TestSyncClaudeCodeThemePreservesOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	orig := `{"theme":"light-ansi","hooks":{"a":[1,2]},"permissions":{"allow":["x"]}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := syncClaudeCodeTheme("dark"); err != nil {
		t.Fatalf("sync: %v", err)
	}

	var got map[string]any
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result is not valid json: %v", err)
	}
	if got["theme"] != "dark-ansi" {
		t.Errorf("theme = %v, want dark-ansi", got["theme"])
	}
	if _, ok := got["hooks"]; !ok {
		t.Error("hooks key was dropped")
	}
	if _, ok := got["permissions"]; !ok {
		t.Error("permissions key was dropped")
	}
	if _, err := os.Stat(path + ".tabby.tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
}

func TestSyncClaudeCodeThemeNoFileIsNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := syncClaudeCodeTheme("dark"); err != nil {
		t.Errorf("missing settings.json should be a no-op, got %v", err)
	}
}

func TestSyncClaudeCodeThemeMalformedJSONErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syncClaudeCodeTheme("dark"); err == nil {
		t.Error("expected an error on malformed json")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{not json" {
		t.Error("malformed file must be left untouched, not rewritten")
	}
}
