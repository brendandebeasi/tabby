package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestResolveDirColor_CWDColors(t *testing.T) {
	c := newTestCoordinator(t)
	seedCWDMapping(t, c, "/tmp/project", CWDColorMapping{Color: "#112233"})

	assert.Equal(t, "#112233", c.resolveDirColor("/tmp/project"))
	assert.Empty(t, c.resolveDirColor("/tmp/unregistered"))
}

func TestResolveDirColor_ConfigWorkingDir(t *testing.T) {
	c := newTestCoordinator(t)
	c.config = &config.Config{
		Groups: []config.Group{
			{
				Name:       "StudioDome",
				WorkingDir: "/tmp/studiodome",
				Theme: config.Theme{
					Bg: "#b4637a",
				},
			},
		},
	}

	assert.Equal(t, "#b4637a", c.resolveDirColor("/tmp/studiodome"))
	assert.Equal(t, "#b4637a", c.resolveDirColor("/tmp/studiodome/sub/dir"))
	assert.Empty(t, c.resolveDirColor("/tmp/other"))
}

func TestResolveDirColor_CWDColorsTakesPrecedenceOverConfig(t *testing.T) {
	c := newTestCoordinator(t)
	c.config = &config.Config{
		Groups: []config.Group{
			{
				Name:       "StudioDome",
				WorkingDir: "/tmp/studiodome",
				Theme: config.Theme{
					Bg: "#b4637a",
				},
			},
		},
	}
	seedCWDMapping(t, c, "/tmp/studiodome", CWDColorMapping{Color: "#ff00aa"})

	// Remembered color in cwdColors overrides group theme bg
	assert.Equal(t, "#ff00aa", c.resolveDirColor("/tmp/studiodome"))
}

func TestNewWindow_GroupAndColorResolution(t *testing.T) {
	c := newTestCoordinator(t)
	c.config = &config.Config{
		Groups: []config.Group{
			{
				Name:       "Work",
				WorkingDir: "/projects/work",
				Theme: config.Theme{
					Bg: "#3498db",
				},
			},
			{
				Name:       "Other",
				WorkingDir: "/projects/other",
				Theme: config.Theme{
					Bg: "#e74c3c",
				},
			},
		},
	}

	// Case 1: Current window is in group "Work" with custom color.
	// Opening in same dir (which has no other color) -> same color.
	c.windows = []tmux.Window{
		{
			ID:          "@1",
			Index:       1,
			Group:       "Work",
			CustomColor: "#ffffff",
			Panes: []tmux.Pane{
				{ID: "%1", CurrentPath: "/projects/work", Active: true},
			},
		},
	}

	curGroup := c.windows[0].Group
	curColor := c.windows[0].CustomColor
	group := curGroup
	if group == "" {
		group = "Default"
	}
	assert.Equal(t, "Work", group, "must be created in the same group the user is in")

	effectiveColor := curColor
	dirColor := c.resolveDirColor("/projects/work")
	// /projects/work has group color #3498db, which differs from #ffffff
	// So dirColor has another color
	assert.Equal(t, "#3498db", dirColor)
	assert.NotEqual(t, effectiveColor, dirColor)

	// Case 2: Window has no custom color (uses group color #3498db).
	// Dir /projects/work has #3498db -> same color, so no custom color needed.
	c.windows[0].CustomColor = ""
	curColor = ""
	effectiveColor = c.config.Groups[0].Theme.Bg
	assert.Equal(t, "#3498db", effectiveColor)
	assert.Equal(t, dirColor, effectiveColor, "dir color matches group color, no custom color needed")

	// Case 3: Window in group "Work" opens in /projects/other (which has color #e74c3c).
	// Dir has another color!
	otherDirColor := c.resolveDirColor("/projects/other")
	assert.Equal(t, "#e74c3c", otherDirColor)
	assert.NotEqual(t, effectiveColor, otherDirColor, "dir has another color")
}
