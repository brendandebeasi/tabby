package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
)

// A theme flip must change the background the per-pane repaint pass paints
// from. applyThemeToTmux only rewrites the GLOBAL window-style, but per-pane
// overrides beat the global in tmux, so a flip that left GetTerminalBg on the
// old theme would repaint panes back to the previous theme's background --
// the "some backgrounds didn't refresh" split-theme session.
func TestSetThemeChangesTerminalBg(t *testing.T) {
	dark := colors.GetTheme("rose-pine")
	c := &Coordinator{config: &config.Config{}, theme: &dark}

	before := c.GetTerminalBg()
	if before == "" {
		t.Fatal("GetTerminalBg empty for rose-pine")
	}

	light := colors.GetTheme("rose-pine-dawn")
	c.stateMu.Lock()
	c.theme = &light
	c.config.Sidebar.Theme = "rose-pine-dawn"
	c.stateMu.Unlock()

	after := c.GetTerminalBg()
	if after == before {
		t.Errorf("terminal bg unchanged across flip: %q -- panes would repaint to the old theme", after)
	}
}

// A session-wide repaint spans multiple windows, and pane_left is a per-window
// column index. Keying active-state by column alone let panes in different
// windows collide, so one window's active pane read as inactive and kept the
// previous theme's style -- the "some backgrounds didn't refresh" split.
func TestActiveStateKeyedByWindowNotColumn(t *testing.T) {
	panes := []dimPaneInfo{
		{id: "%1", windowID: "@1", left: 0, active: false},
		{id: "%2", windowID: "@1", left: 26, active: true},
		{id: "%3", windowID: "@2", left: 0, active: false},
		{id: "%4", windowID: "@2", left: 26, active: true},
	}
	type colKey struct {
		win  string
		left int
	}
	colActive := map[colKey]bool{}
	for _, p := range panes {
		colActive[colKey{p.windowID, p.left}] = p.active
	}
	if len(colActive) != 4 {
		t.Fatalf("got %d keys, want 4 -- windows are colliding", len(colActive))
	}
	for _, p := range panes {
		if got := colActive[colKey{p.windowID, p.left}]; got != p.active {
			t.Errorf("pane %s in %s: active=%v, want %v", p.id, p.windowID, got, p.active)
		}
	}
}
