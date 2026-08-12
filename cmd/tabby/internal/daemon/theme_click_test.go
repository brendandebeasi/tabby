package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
)

// A sidebar click on the theme button must flip the persisted mode.
func TestToggleThemeClickFlipsMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TABBY_CONFIG_DIR", dir)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(
		"sidebar:\n  theme: rose-pine\nauto_theme:\n  enabled: true\n  mode: dark\n  light: rose-pine-dawn\n  dark: rose-pine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := config.DefaultConfigPath(); got != cfgPath {
		t.Skipf("config path not redirected by TABBY_CONFIG_DIR (got %s)", got)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	th := colors.GetTheme(cfg.Sidebar.Theme)
	c := &Coordinator{config: cfg, theme: &th}

	c.handleSemanticAction("@0", &daemon.InputPayload{ResolvedAction: "toggle_theme"})

	after, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.AutoTheme.Mode != "light" {
		t.Errorf("after click mode = %q, want light", after.AutoTheme.Mode)
	}
	if c.ActiveThemeName() != "rose-pine-dawn" {
		t.Errorf("active theme = %q, want rose-pine-dawn", c.ActiveThemeName())
	}
}
