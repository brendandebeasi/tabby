package daemon

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	zone "github.com/lrstanley/bubblezone"
)

// The theme button must occupy the blank padding row that renderNavButtons
// used to emit, not add a row of its own.
func TestNavButtonsHeightUnchangedByThemeButton(t *testing.T) {
	zone.NewGlobal()

	withPair := &Coordinator{config: &config.Config{}}
	withPair.config.AutoTheme = config.AutoTheme{Enabled: true, Mode: "dark", Light: "l", Dark: "d"}

	noPair := &Coordinator{config: &config.Config{}}
	noPair.config.AutoTheme = config.AutoTheme{Enabled: false}

	a := strings.Count(zone.Scan(withPair.renderNavButtons(20)), "\n")
	b := strings.Count(zone.Scan(noPair.renderNavButtons(20)), "\n")
	if a != b {
		t.Errorf("theme button changed nav block height: with=%d without=%d", a, b)
	}
}
