package daemon

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	zone "github.com/lrstanley/bubblezone"
)

func TestRenderThemeButton(t *testing.T) {
	zone.NewGlobal()
	c := &Coordinator{config: &config.Config{}}

	c.config.AutoTheme = config.AutoTheme{Enabled: true, Mode: "dark", Light: "l", Dark: "d"}
	got := zone.Scan(c.renderThemeButton(20))
	if !strings.Contains(got, "Light") {
		t.Errorf("mode=dark should offer Light, got %q", got)
	}

	c.config.AutoTheme.Mode = "light"
	got = zone.Scan(c.renderThemeButton(20))
	if !strings.Contains(got, "Dark") {
		t.Errorf("mode=light should offer Dark, got %q", got)
	}

	// Narrow sidebar drops the glyph.
	got = zone.Scan(c.renderThemeButton(8))
	if strings.Contains(got, "◐") {
		t.Errorf("width=8 should drop the glyph, got %q", got)
	}

	// No pair configured -> no button.
	c.config.AutoTheme.Enabled = false
	if got := c.renderThemeButton(20); got != "" {
		t.Errorf("disabled should render nothing, got %q", got)
	}
}
