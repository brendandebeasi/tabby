package daemon

import (
	"strings"
	"testing"

	zone "github.com/lrstanley/bubblezone"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestMain(m *testing.M) {
	zone.NewGlobal()
	m.Run()
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
