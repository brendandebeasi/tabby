package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestDesaturateHex(t *testing.T) {
	t.Run("empty_input_returned_unchanged", func(t *testing.T) {
		assert.Equal(t, "", desaturateHex("", 0.5))
	})

	t.Run("invalid_hex_returned_unchanged", func(t *testing.T) {
		assert.Equal(t, "bad", desaturateHex("bad", 0.5))
	})

	t.Run("opacity_1_returns_original_color", func(t *testing.T) {
		got := desaturateHex("#ff0000", 1.0)
		assert.Equal(t, "#ff0000", got)
	})

	t.Run("opacity_0_blends_fully_to_target", func(t *testing.T) {
		got := desaturateHex("#ff0000", 0.0, "#000000")
		assert.Equal(t, "#000000", got)
	})

	t.Run("returns_valid_hex_string", func(t *testing.T) {
		got := desaturateHex("#3498db", 0.5)
		assert.Len(t, got, 7)
		assert.Equal(t, '#', rune(got[0]))
	})

	t.Run("auto_neutral_target_for_light_color", func(t *testing.T) {
		got := desaturateHex("#ffffff", 0.5)
		assert.Len(t, got, 7)
	})

	t.Run("auto_neutral_target_for_dark_color", func(t *testing.T) {
		got := desaturateHex("#000000", 0.5)
		assert.Len(t, got, 7)
	})
}

func TestHasActiveIndicatorAnimation(t *testing.T) {
	t.Run("returns_true_when_multiple_frames_configured", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = []string{"a", "b"}
		assert.True(t, c.HasActiveIndicatorAnimation())
	})

	t.Run("returns_false_with_single_frame", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = []string{"a"}
		assert.False(t, c.HasActiveIndicatorAnimation())
	})

	t.Run("returns_false_with_no_frames", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = nil
		assert.False(t, c.HasActiveIndicatorAnimation())
	})
}

func TestGetSlowSpinnerFrame(t *testing.T) {
	c := newTestCoordinator(t)

	c.spinnerFrame = 0
	assert.Equal(t, 0, c.getSlowSpinnerFrame())

	c.spinnerFrame = 2
	assert.Equal(t, 1, c.getSlowSpinnerFrame())

	c.spinnerFrame = 5
	assert.Equal(t, 2, c.getSlowSpinnerFrame())
}

func TestGetAnimatedActiveIndicator(t *testing.T) {
	t.Run("cycles_through_frames", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = []string{"A", "B", "C"}

		c.spinnerFrame = 0
		assert.Equal(t, "A", c.getAnimatedActiveIndicator("fallback"))

		c.spinnerFrame = 2
		assert.Equal(t, "B", c.getAnimatedActiveIndicator("fallback"))
	})

	t.Run("wraps_around_frame_count", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = []string{"X", "Y"}
		c.spinnerFrame = 4
		got := c.getAnimatedActiveIndicator("fallback")
		assert.Contains(t, []string{"X", "Y"}, got)
	})

	t.Run("empty_frames_returns_fallback", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.Sidebar.Colors.ActiveIndicatorFrames = nil
		assert.Equal(t, "fallback", c.getAnimatedActiveIndicator("fallback"))
	})
}

func TestGetGitStateHash(t *testing.T) {
	t.Run("stable_for_same_state", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.gitBranch = "main"
		c.gitDirty = 0
		assert.Equal(t, c.GetGitStateHash(), c.GetGitStateHash())
	})

	t.Run("changes_with_branch", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.gitBranch = "main"
		h1 := c.GetGitStateHash()
		c.gitBranch = "dev"
		h2 := c.GetGitStateHash()
		assert.NotEqual(t, h1, h2)
	})

	t.Run("contains_branch_name", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.gitBranch = "feature-xyz"
		assert.Contains(t, c.GetGitStateHash(), "feature-xyz")
	})
}

func TestConstrainWidgetWidth(t *testing.T) {
	t.Run("zero_width_returns_content_unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", constrainWidgetWidth("hello", 0))
	})

	t.Run("content_shorter_than_max_unchanged", func(t *testing.T) {
		assert.Equal(t, "hi", constrainWidgetWidth("hi", 100))
	})

	t.Run("long_line_truncated", func(t *testing.T) {
		long := "abcdefghijklmnopqrstuvwxyz"
		got := constrainWidgetWidth(long, 10)
		assert.LessOrEqual(t, len(got), 10)
	})

	t.Run("multiline_each_line_independent", func(t *testing.T) {
		content := "short\nthis is a very long line that exceeds width"
		got := constrainWidgetWidth(content, 8)
		lines := splitLines(got)
		for _, line := range lines {
			assert.LessOrEqual(t, len(line), 8)
		}
	})
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestClampSpriteX(t *testing.T) {
	t.Run("negative_x_preserved", func(t *testing.T) {
		assert.Equal(t, -1, clampSpriteX(-1, "X", 10))
	})

	t.Run("x_within_bounds_unchanged", func(t *testing.T) {
		assert.Equal(t, 3, clampSpriteX(3, "X", 10))
	})

	t.Run("x_exceeding_max_clamped", func(t *testing.T) {
		assert.Equal(t, 9, clampSpriteX(20, "X", 10))
	})

	t.Run("zero_max_returns_zero", func(t *testing.T) {
		assert.Equal(t, 0, clampSpriteX(5, "X", 1))
	})
}

func TestFindWindowByTarget(t *testing.T) {
	windows := []tmux.Window{
		{ID: "@1", Index: 0, Name: "alpha"},
		{ID: "@2", Index: 1, Name: "beta"},
		{ID: "@3", Index: 2, Name: "gamma"},
	}

	t.Run("find_by_id", func(t *testing.T) {
		w := findWindowByTarget(windows, "@2")
		assert.NotNil(t, w)
		assert.Equal(t, "beta", w.Name)
	})

	t.Run("find_by_index", func(t *testing.T) {
		w := findWindowByTarget(windows, "2")
		assert.NotNil(t, w)
		assert.Equal(t, "gamma", w.Name)
	})

	t.Run("empty_target_returns_nil", func(t *testing.T) {
		assert.Nil(t, findWindowByTarget(windows, ""))
	})

	t.Run("whitespace_only_returns_nil", func(t *testing.T) {
		assert.Nil(t, findWindowByTarget(windows, "   "))
	})

	t.Run("unknown_id_returns_nil", func(t *testing.T) {
		assert.Nil(t, findWindowByTarget(windows, "@99"))
	})

	t.Run("invalid_index_returns_nil", func(t *testing.T) {
		assert.Nil(t, findWindowByTarget(windows, "notanumber"))
	})
}

func TestIsVerticalStackedPane(t *testing.T) {
	t.Run("nil_window_returns_false", func(t *testing.T) {
		c := newTestCoordinator(t)
		assert.False(t, c.isVerticalStackedPane(nil, "%1"))
	})

	t.Run("empty_pane_id_returns_false", func(t *testing.T) {
		c := newTestCoordinator(t)
		w := testWindow("1", true, "bash")
		assert.False(t, c.isVerticalStackedPane(&w, ""))
	})

	t.Run("single_pane_not_stacked", func(t *testing.T) {
		c := newTestCoordinator(t)
		w := tmux.Window{
			Panes: []tmux.Pane{{ID: "%1", Width: 80, Height: 24}},
		}
		assert.False(t, c.isVerticalStackedPane(&w, "%1"))
	})

	t.Run("panes_with_same_width_at_different_top_is_stacked", func(t *testing.T) {
		c := newTestCoordinator(t)
		w := tmux.Window{
			Panes: []tmux.Pane{
				{ID: "%1", Width: 80, Height: 12, Top: 0, Left: 0},
				{ID: "%2", Width: 80, Height: 12, Top: 12, Left: 0},
			},
		}
		assert.True(t, c.isVerticalStackedPane(&w, "%1"))
	})

	t.Run("panes_with_different_widths_not_stacked", func(t *testing.T) {
		c := newTestCoordinator(t)
		w := tmux.Window{
			Panes: []tmux.Pane{
				{ID: "%1", Width: 80, Height: 24, Top: 0, Left: 0},
				{ID: "%2", Width: 40, Height: 24, Top: 0, Left: 40},
			},
		}
		assert.False(t, c.isVerticalStackedPane(&w, "%1"))
	})
}

func TestRenderStatusBar(t *testing.T) {
	t.Run("zero_value_all_empty", func(t *testing.T) {
		got := renderStatusBar(0, 5)
		assert.Equal(t, "░░░░░", got)
	})

	t.Run("full_value_all_filled", func(t *testing.T) {
		got := renderStatusBar(100, 5)
		assert.Equal(t, "▓▓▓▓▓", got)
	})

	t.Run("half_value_half_filled", func(t *testing.T) {
		got := renderStatusBar(50, 4)
		assert.Equal(t, "▓▓░░", got)
	})

	t.Run("clamps_below_zero", func(t *testing.T) {
		got := renderStatusBar(-10, 3)
		assert.Equal(t, "░░░", got)
	})

	t.Run("clamps_above_100", func(t *testing.T) {
		got := renderStatusBar(150, 3)
		assert.Equal(t, "▓▓▓", got)
	})
}

func TestPaneIsSystemPane(t *testing.T) {
	trueTests := [][2]string{
		{"sidebar", ""},
		{"sidebar-renderer", ""},
		{"", "sidebar-renderer"},
		{"tabby-daemon", ""},
		{"pane-header", ""},
		{"my-renderer", ""},
	}
	for _, tt := range trueTests {
		cmd, start := tt[0], tt[1]
		t.Run("true/"+cmd+"/"+start, func(t *testing.T) {
			assert.True(t, paneIsSystemPane(cmd, start))
		})
	}

	falseTests := [][2]string{
		{"bash", "bash"},
		{"vim", ""},
		{"git", "git"},
		{"", ""},
	}
	for _, tt := range falseTests {
		cmd, start := tt[0], tt[1]
		t.Run("false/"+cmd+"/"+start, func(t *testing.T) {
			assert.False(t, paneIsSystemPane(cmd, start))
		})
	}
}
