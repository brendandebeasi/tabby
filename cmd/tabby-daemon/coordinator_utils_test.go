package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeCWD(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"/home/user/projects", "/home/user/projects"},
		{"/home/user/projects/", "/home/user/projects"},
		{"  /tmp/foo  ", "/tmp/foo"},
		{"/a/b/../c", "/a/c"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, normalizeCWD(c.in))
		})
	}
}

func TestFirstPaneCWD(t *testing.T) {
	t.Run("no_panes_returns_empty", func(t *testing.T) {
		w := tmux.Window{}
		assert.Equal(t, "", firstPaneCWD(w))
	})

	t.Run("returns_first_pane_path", func(t *testing.T) {
		w := tmux.Window{
			Panes: []tmux.Pane{
				{CurrentPath: "/home/user"},
				{CurrentPath: "/tmp"},
			},
		}
		assert.Equal(t, "/home/user", firstPaneCWD(w))
	})

	t.Run("normalizes_trailing_slash", func(t *testing.T) {
		w := tmux.Window{Panes: []tmux.Pane{{CurrentPath: "/home/user/"}}}
		assert.Equal(t, "/home/user", firstPaneCWD(w))
	})
}

func TestHeaderBoolDefault(t *testing.T) {
	t.Run("nil_returns_true", func(t *testing.T) {
		assert.True(t, headerBoolDefault(nil))
	})

	t.Run("pointer_to_true", func(t *testing.T) {
		v := true
		assert.True(t, headerBoolDefault(&v))
	})

	t.Run("pointer_to_false", func(t *testing.T) {
		v := false
		assert.False(t, headerBoolDefault(&v))
	})
}

func TestAbs(t *testing.T) {
	assert.Equal(t, 5, abs(5))
	assert.Equal(t, 5, abs(-5))
	assert.Equal(t, 0, abs(0))
}

func TestSafeRandRange(t *testing.T) {
	t.Run("equal_min_max", func(t *testing.T) {
		assert.Equal(t, 7, safeRandRange(7, 7))
	})

	t.Run("max_less_than_min_returns_max", func(t *testing.T) {
		assert.Equal(t, 3, safeRandRange(10, 3))
	})

	t.Run("max_negative_returns_zero", func(t *testing.T) {
		assert.Equal(t, 0, safeRandRange(10, -1))
	})

	t.Run("result_in_range", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			v := safeRandRange(0, 5)
			assert.GreaterOrEqual(t, v, 0)
			assert.LessOrEqual(t, v, 5)
		}
	})
}

func TestStripAnsiCoordinator(t *testing.T) {
	assert.Equal(t, "plain", stripAnsi("plain"))
	assert.Equal(t, "red text", stripAnsi("\x1b[31mred text\x1b[0m"))
	assert.Equal(t, "", stripAnsi("\x1b[1;32m"))
	assert.Equal(t, "hello world", stripAnsi("hello\x1b[0m world"))
}

func TestGetIndicatorIcon(t *testing.T) {
	c := newTestCoordinator(t)

	t.Run("custom_icon_returned", func(t *testing.T) {
		ind := config.Indicator{Icon: "★"}
		assert.Equal(t, "★", c.getIndicatorIcon(ind))
	})

	t.Run("empty_icon_returns_default", func(t *testing.T) {
		ind := config.Indicator{}
		assert.Equal(t, "●", c.getIndicatorIcon(ind))
	})
}

func TestGetBusyFrames(t *testing.T) {
	t.Run("default_frames_when_config_empty", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Indicators.Busy.Frames = nil
		frames := c.getBusyFrames()
		assert.Len(t, frames, 4)
	})

	t.Run("config_frames_used_when_set", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Indicators.Busy.Frames = []string{"a", "b", "c"}
		frames := c.getBusyFrames()
		assert.Equal(t, []string{"a", "b", "c"}, frames)
	})
}

func TestGetThemeColor(t *testing.T) {
	t.Run("no_theme_returns_fallback", func(t *testing.T) {
		c := newTestCoordinator(t)
		assert.Equal(t, "#fallback", c.getThemeColor("#unused", "#fallback"))
	})

	t.Run("with_theme_and_non_empty_color_returns_color", func(t *testing.T) {
		c := newTestCoordinator(t)
		th := &colors.Theme{}
		c.theme = th
		assert.Equal(t, "#custom", c.getThemeColor("#custom", "#fallback"))
	})

	t.Run("with_theme_but_empty_color_returns_fallback", func(t *testing.T) {
		c := newTestCoordinator(t)
		th := &colors.Theme{}
		c.theme = th
		assert.Equal(t, "#fallback", c.getThemeColor("", "#fallback"))
	})
}

func TestHexToRGB(t *testing.T) {
	r, g, b := hexToRGB("#ff0000")
	assert.Equal(t, 255, r)
	assert.Equal(t, 0, g)
	assert.Equal(t, 0, b)

	r, g, b = hexToRGB("#000000")
	assert.Equal(t, 0, r)
	assert.Equal(t, 0, g)
	assert.Equal(t, 0, b)

	r, g, b = hexToRGB("#ffffff")
	assert.Equal(t, 255, r)
	assert.Equal(t, 255, g)
	assert.Equal(t, 255, b)

	r, g, b = hexToRGB("bad")
	assert.Equal(t, 0, r)
	assert.Equal(t, 0, g)
	assert.Equal(t, 0, b)
}

func TestDimColor(t *testing.T) {
	t.Run("empty_returns_empty", func(t *testing.T) {
		assert.Equal(t, "", dimColor("", 0.5))
	})

	t.Run("opacity_1_no_change", func(t *testing.T) {
		assert.Equal(t, "#ffffff", dimColor("#ffffff", 1.0))
	})

	t.Run("opacity_0_returns_black", func(t *testing.T) {
		assert.Equal(t, "#000000", dimColor("#ffffff", 0.0))
	})

	t.Run("opacity_0_5_halves_values", func(t *testing.T) {
		got := dimColor("#ffffff", 0.5)
		r, g, b := hexToRGB(got)
		assert.InDelta(t, 127, r, 2)
		assert.InDelta(t, 127, g, 2)
		assert.InDelta(t, 127, b, 2)
	})
}

func TestIsAuxiliaryPaneCommand(t *testing.T) {
	trueTests := []string{
		"sidebar", "sidebar-renderer", "/path/to/sidebar",
		"pane-header", "pane_header", "pane header",
		"SIDEBAR", "Pane-Header",
	}
	for _, cmd := range trueTests {
		cmd := cmd
		t.Run("true/"+cmd, func(t *testing.T) {
			assert.True(t, isAuxiliaryPaneCommand(cmd))
		})
	}

	falseTests := []string{"bash", "vim", "git", "", "ssh", "node"}
	for _, cmd := range falseTests {
		cmd := cmd
		t.Run("false/"+cmd, func(t *testing.T) {
			assert.False(t, isAuxiliaryPaneCommand(cmd))
		})
	}
}

func TestIsAuxiliaryPane(t *testing.T) {
	t.Run("sidebar_command_is_auxiliary", func(t *testing.T) {
		p := tmux.Pane{Command: "sidebar-renderer"}
		assert.True(t, isAuxiliaryPane(p))
	})

	t.Run("start_command_checked_too", func(t *testing.T) {
		p := tmux.Pane{Command: "bash", StartCommand: "pane-header"}
		assert.True(t, isAuxiliaryPane(p))
	})

	t.Run("normal_pane_not_auxiliary", func(t *testing.T) {
		p := tmux.Pane{Command: "vim", StartCommand: "vim"}
		assert.False(t, isAuxiliaryPane(p))
	})
}

func TestGetClientWidth(t *testing.T) {
	t.Run("returns_stored_width_when_valid", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.clientWidths["client-1"] = 80
		assert.Equal(t, 80, c.getClientWidth("client-1"))
	})

	t.Run("falls_back_to_lastWidth_when_client_unknown", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.lastWidth = 60
		assert.Equal(t, 60, c.getClientWidth("unknown"))
	})

	t.Run("falls_back_to_25_when_lastWidth_too_small", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.lastWidth = 5
		assert.Equal(t, 25, c.getClientWidth("unknown"))
	})

	t.Run("stored_width_below_10_falls_back_to_lastWidth", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.clientWidths["c1"] = 5
		c.lastWidth = 40
		assert.Equal(t, 40, c.getClientWidth("c1"))
	})
}
