package daemon

import (
	"os"
	"strings"
	"testing"

	zone "github.com/lrstanley/bubblezone"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestMain(m *testing.M) {
	// Isolate persisted state (cwd-colors.json, pet.json, ...) into a throwaway
	// dir so tests that exercise saveCWDColors/captureCWDIdentity never clobber
	// the developer's real ~/.local/state/tabby. Must run before any
	// paths.StateDir() call caches its sync.Once value.
	stateDir, _ := os.MkdirTemp("", "tabby-test-state")
	if stateDir != "" {
		os.Setenv("TABBY_STATE_DIR", stateDir)
	}
	zone.NewGlobal()
	code := m.Run()
	if stateDir != "" {
		os.RemoveAll(stateDir)
	}
	os.Exit(code)
}

func TestRenderNavButtonsHasArrows(t *testing.T) {
	c := &Coordinator{config: &config.Config{}}
	out := c.renderNavButtons(40)
	if !strings.Contains(out, "▲") {
		t.Error("expected ▲ in nav buttons output")
	}
	if !strings.Contains(out, "▼") {
		t.Error("expected ▼ in nav buttons output")
	}
}

func TestRenderSidebarResizeButtonsNoNavArrows(t *testing.T) {
	c := &Coordinator{config: &config.Config{}}
	out := c.renderSidebarResizeButtons(40)
	if strings.Contains(out, "▲") || strings.Contains(out, "▼") {
		t.Error("resize buttons should not contain nav arrows — they are now a separate widget")
	}
	if !strings.Contains(out, "<") {
		t.Error("expected < shrink button present")
	}
}
