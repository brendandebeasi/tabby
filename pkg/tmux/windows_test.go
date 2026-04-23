package tmux

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockRunner struct {
	responses map[string]mockResp
	calls     [][]string
}

type mockResp struct {
	output []byte
	err    error
}

func (m *mockRunner) Run(args ...string) ([]byte, error) {
	m.calls = append(m.calls, append([]string{}, args...))
	if len(args) == 0 {
		return nil, fmt.Errorf("no args")
	}
	if resp, ok := m.responses[args[0]]; ok {
		return resp.output, resp.err
	}
	return []byte(""), nil
}

func newMock() *mockRunner {
	return &mockRunner{responses: make(map[string]mockResp)}
}

func (m *mockRunner) set(cmd, output string, err error) {
	m.responses[cmd] = mockResp{output: []byte(output), err: err}
}

func restoreState(t *testing.T) {
	t.Helper()
	origRunner := DefaultRunner
	origTarget := sessionTarget
	origAITimeout := aiIdleTimeout
	origAI := make(map[string]bool, len(aiToolCommands))
	for k, v := range aiToolCommands {
		origAI[k] = v
	}
	origIdle := make(map[string]bool, len(idleCommands))
	for k, v := range idleCommands {
		origIdle[k] = v
	}
	t.Cleanup(func() {
		DefaultRunner = origRunner
		sessionTarget = origTarget
		aiIdleTimeout = origAITimeout
		aiToolCommands = origAI
		idleCommands = origIdle
	})
}

// fields joins test inputs with the same separator the production code uses
// for tmux -F format strings. Keeping this in sync with FieldSep is critical:
// in 2026-04 the separator changed from \x1f to "|||" for Linux tmux
// compatibility (Linux tmux escapes non-printable bytes like \037, breaking
// field parsing). Tests that use \x1f directly will split into a single
// element and silently skip all rows as "too few fields".
func fields(parts ...string) string {
	return strings.Join(parts, FieldSep)
}

func TestStripANSI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"\x1b[0m", ""},
		{"\x1b[31;1mred\x1b[0m", "red"},
		{"\x1b]0;title\x07", ""},
		{"[0mtest", "test"},
		{"hello\x1b[32mworld\x1b[0m", "helloworld"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, stripANSI(c.in))
		})
	}
}

func TestIsSidebarCommand(t *testing.T) {
	trueTests := []string{
		"sidebar", "tabby-daemon", "sidebar-renderer",
		"pane-header", "render-status",
		"/usr/local/bin/sidebar-renderer",
		"/opt/tabby/pane-header",
		"some-sidebar-thing",
		"my-tabby-daemon-wrapper",
	}
	for _, cmd := range trueTests {
		cmd := cmd
		t.Run("true/"+cmd, func(t *testing.T) {
			assert.True(t, isSidebarCommand(cmd))
		})
	}

	falseTests := []string{"bash", "zsh", "vim", "nvim", "python", "node", "git", "", "ssh"}
	for _, cmd := range falseTests {
		cmd := cmd
		t.Run("false/"+cmd, func(t *testing.T) {
			assert.False(t, isSidebarCommand(cmd))
		})
	}
}

func TestIsPaneBusy(t *testing.T) {
	idleTests := []string{"bash", "zsh", "fish", "sh", "node", "python", "python3", "nvim", "vim", "ssh", "mosh"}
	for _, cmd := range idleTests {
		cmd := cmd
		t.Run("idle/"+cmd, func(t *testing.T) {
			assert.False(t, isPaneBusy(cmd))
		})
	}

	busyTests := []string{"make", "go", "cargo", "npm", "curl", "wget", "rustc"}
	for _, cmd := range busyTests {
		cmd := cmd
		t.Run("busy/"+cmd, func(t *testing.T) {
			assert.True(t, isPaneBusy(cmd))
		})
	}

	t.Run("configured_ai_tool_not_busy", func(t *testing.T) {
		restoreState(t)
		aiToolCommands["myai"] = true
		assert.False(t, isPaneBusy("myai"))
	})
}

func TestIsAITool(t *testing.T) {
	t.Run("semver_version_is_ai_tool", func(t *testing.T) {
		assert.True(t, IsAITool("2.1.17"))
		assert.True(t, IsAITool("1.0.0"))
		assert.True(t, IsAITool("10.20.30"))
	})

	t.Run("non_semver_not_ai_tool_by_default", func(t *testing.T) {
		assert.False(t, IsAITool("bash"))
		assert.False(t, IsAITool("vim"))
		assert.False(t, IsAITool("claude"))
	})

	t.Run("configured_ai_tool_detected", func(t *testing.T) {
		restoreState(t)
		aiToolCommands["myagent"] = true
		assert.True(t, IsAITool("myagent"))
	})

	t.Run("partial_semver_not_matched", func(t *testing.T) {
		assert.False(t, IsAITool("2.1"))
		assert.False(t, IsAITool("2"))
		assert.False(t, IsAITool("v2.1.17"))
	})
}

func TestHasSpinner(t *testing.T) {
	t.Run("braille_dot_is_spinner", func(t *testing.T) {
		assert.True(t, HasSpinner("⠋working"))
		assert.True(t, HasSpinner("⠿"))
	})

	t.Run("blank_braille_U2800_is_not_spinner", func(t *testing.T) {
		assert.False(t, HasSpinner("\u2800"))
	})

	t.Run("empty_string_not_spinner", func(t *testing.T) {
		assert.False(t, HasSpinner(""))
	})

	t.Run("regular_text_not_spinner", func(t *testing.T) {
		assert.False(t, HasSpinner("normal title"))
	})

	t.Run("idle_star_not_spinner", func(t *testing.T) {
		assert.False(t, HasSpinner("✳ waiting"))
	})
}

func TestHasIdleIcon(t *testing.T) {
	t.Run("star_glyph_is_idle_icon", func(t *testing.T) {
		assert.True(t, HasIdleIcon("✳ idle"))
		assert.True(t, HasIdleIcon("✳"))
	})

	t.Run("empty_string_not_idle_icon", func(t *testing.T) {
		assert.False(t, HasIdleIcon(""))
	})

	t.Run("spinner_not_idle_icon", func(t *testing.T) {
		assert.False(t, HasIdleIcon("⠋spinner"))
	})

	t.Run("regular_text_not_idle_icon", func(t *testing.T) {
		assert.False(t, HasIdleIcon("working"))
	})
}

func TestAIIdleTimeout(t *testing.T) {
	restoreState(t)
	assert.Equal(t, int64(10), AIIdleTimeout())
}

func TestSetSessionTarget(t *testing.T) {
	restoreState(t)

	t.Run("sets_target", func(t *testing.T) {
		SetSessionTarget("$1")
		assert.Equal(t, "$1", sessionTarget)
	})

	t.Run("trims_whitespace", func(t *testing.T) {
		SetSessionTarget("  $2  ")
		assert.Equal(t, "$2", sessionTarget)
	})

	t.Run("clears_with_empty_string", func(t *testing.T) {
		SetSessionTarget("")
		assert.Equal(t, "", sessionTarget)
	})
}

func TestConfigureBusyDetection(t *testing.T) {
	t.Run("adds_extra_idle_commands", func(t *testing.T) {
		restoreState(t)
		ConfigureBusyDetection([]string{"mycmd"}, nil, 0)
		assert.True(t, idleCommands["mycmd"])
		assert.False(t, isPaneBusy("mycmd"))
		assert.True(t, isPaneBusy("make"), "make still busy")
	})

	t.Run("sets_ai_tools", func(t *testing.T) {
		restoreState(t)
		ConfigureBusyDetection(nil, []string{"myai", "assistant"}, 0)
		assert.True(t, aiToolCommands["myai"])
		assert.True(t, aiToolCommands["assistant"])
		assert.True(t, IsAITool("myai"))
	})

	t.Run("updates_ai_idle_timeout", func(t *testing.T) {
		restoreState(t)
		ConfigureBusyDetection(nil, nil, 30)
		assert.Equal(t, int64(30), aiIdleTimeout)
	})

	t.Run("zero_timeout_leaves_timeout_unchanged", func(t *testing.T) {
		restoreState(t)
		aiIdleTimeout = 15
		ConfigureBusyDetection(nil, nil, 0)
		assert.Equal(t, int64(15), aiIdleTimeout)
	})
}

func TestListWindows(t *testing.T) {
	restoreState(t)

	t.Run("single_active_window", func(t *testing.T) {
		mock := newMock()
		line := fields(
			"@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "",
		)
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if !assert.Len(t, windows, 1) {
			return
		}
		w := windows[0]
		assert.Equal(t, "@1", w.ID)
		assert.Equal(t, 0, w.Index)
		assert.Equal(t, "main", w.Name)
		assert.True(t, w.Active)
		assert.False(t, w.Busy)
		assert.True(t, w.SyncWidth)
	})

	t.Run("ansi_stripped_from_name", func(t *testing.T) {
		mock := newMock()
		line := fields("@2", "1", "\x1b[32mcolored\x1b[0m", "0", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Equal(t, "colored", windows[0].Name)
		}
	})

	t.Run("busy_bell_pinned_icon_color_group_parsed", func(t *testing.T) {
		mock := newMock()
		line := fields("@3", "2", "work", "0", "0", "1", "0", "0",
			"#ff0000", "dev", "1", "", "", "", "", "", "", "1", "$0", "1", "🔥", "layout", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			w := windows[0]
			assert.True(t, w.Bell)
			assert.True(t, w.Busy)
			assert.True(t, w.Pinned)
			assert.Equal(t, "🔥", w.Icon)
			assert.Equal(t, "#ff0000", w.CustomColor)
			assert.Equal(t, "dev", w.Group)
			assert.Equal(t, "layout", w.Layout)
		}
	})

	t.Run("minimized_parsed", func(t *testing.T) {
		mock := newMock()
		line := fields("@7", "6", "hidden", "0", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "1")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.True(t, windows[0].Minimized)
		}
	})

	t.Run("tabby_bell_sets_bell_true", func(t *testing.T) {
		mock := newMock()
		line := fields("@4", "3", "win", "0", "0", "0", "0", "0",
			"", "", "0", "1", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.True(t, windows[0].Bell)
		}
	})

	t.Run("sync_width_false_when_zero", func(t *testing.T) {
		mock := newMock()
		line := fields("@5", "4", "win", "0", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "0", "$0", "", "", "", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.False(t, windows[0].SyncWidth)
		}
	})

	t.Run("collapsed_and_input_parsed", func(t *testing.T) {
		mock := newMock()
		line := fields("@6", "5", "win", "0", "0", "0", "0", "0",
			"", "", "0", "", "", "", "1", "1", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.True(t, windows[0].Collapsed)
			assert.True(t, windows[0].Input)
		}
	})

	t.Run("skips_line_with_too_few_fields", func(t *testing.T) {
		mock := newMock()
		mock.set("list-windows", "@1"+FieldSep+"bad\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		assert.Empty(t, windows)
	})

	t.Run("empty_output_returns_empty_slice", func(t *testing.T) {
		mock := newMock()
		mock.set("list-windows", "", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		assert.Empty(t, windows)
	})

	t.Run("runner_error_propagated", func(t *testing.T) {
		mock := newMock()
		mock.set("list-windows", "", fmt.Errorf("tmux died"))
		DefaultRunner = mock

		_, err := ListWindows()
		assert.Error(t, err)
	})

	t.Run("session_filter_skips_foreign_windows", func(t *testing.T) {
		restoreState(t)
		mock := newMock()
		SetSessionTarget("$1")
		line := fields("@1", "0", "mine", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$2", "", "", "", "")
		mock.set("list-windows", line+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		assert.Empty(t, windows, "window from $2 filtered when target is $1")
	})

	t.Run("multiple_windows_parsed", func(t *testing.T) {
		mock := newMock()
		line1 := fields("@1", "0", "alpha", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		line2 := fields("@2", "1", "beta", "0", "0", "0", "0", "1",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", line1+"\n"+line2+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindows()
		assert.NoError(t, err)
		if assert.Len(t, windows, 2) {
			assert.Equal(t, "alpha", windows[0].Name)
			assert.Equal(t, "beta", windows[1].Name)
			assert.True(t, windows[1].Last)
		}
	})
}

func TestListAllPanes(t *testing.T) {
	restoreState(t)

	t.Run("bash_pane_parsed_correctly", func(t *testing.T) {
		mock := newMock()
		line := fields(
			"1",            // window_index
			"%0",           // pane_id
			"0",            // pane_index
			"1",            // pane_active
			"bash",         // pane_current_command
			"My Title",     // pane_title
			"99999",        // pane_pid
			"1700000000",   // pane_last_activity
			"locked-title", // @tabby_pane_title
			"10",           // pane_top
			"20",           // pane_left
			"/home/user",   // pane_current_path
			"",             // @tabby_pane_collapsed
			"",             // @tabby_pane_prev_height
			"bash",         // pane_start_command
			"80",           // pane_width
			"24",           // pane_height
		)
		mock.set("list-panes", line+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		if !assert.Contains(t, result, 1) {
			return
		}
		panes := result[1]
		if !assert.Len(t, panes, 1) {
			return
		}
		p := panes[0]
		assert.Equal(t, "%0", p.ID)
		assert.Equal(t, "bash", p.Command)
		assert.Equal(t, "My Title", p.Title)
		assert.Equal(t, "locked-title", p.LockedTitle)
		assert.False(t, p.Busy)
		assert.False(t, p.Remote)
		assert.Equal(t, 10, p.Top)
		assert.Equal(t, 20, p.Left)
		assert.Equal(t, "/home/user", p.CurrentPath)
		assert.Equal(t, 80, p.Width)
		assert.Equal(t, 24, p.Height)
		assert.True(t, p.Active)
	})

	t.Run("sidebar_pane_filtered", func(t *testing.T) {
		mock := newMock()
		line := fields("1", "%1", "0", "0", "sidebar-renderer", "title",
			"99998", "0", "", "0", "0", "/tmp", "", "", "sidebar-renderer", "80", "24")
		mock.set("list-panes", line+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		assert.Empty(t, result[1])
	})

	t.Run("collapsed_flag_true_parsed", func(t *testing.T) {
		mock := newMock()
		line := fields("2", "%2", "0", "0", "bash", "title",
			"99997", "0", "", "0", "0", "/tmp", "1", "", "bash", "80", "24")
		mock.set("list-panes", line+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		if assert.Contains(t, result, 2) && assert.Len(t, result[2], 1) {
			assert.True(t, result[2][0].Collapsed)
		}
	})

	t.Run("non_idle_command_is_busy", func(t *testing.T) {
		mock := newMock()
		line := fields("3", "%3", "0", "0", "make", "building",
			"99996", "0", "", "0", "0", "/tmp", "", "", "make", "80", "24")
		mock.set("list-panes", line+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		if assert.Contains(t, result, 3) && assert.Len(t, result[3], 1) {
			assert.True(t, result[3][0].Busy)
		}
	})

	t.Run("runner_error_propagated", func(t *testing.T) {
		mock := newMock()
		mock.set("list-panes", "", fmt.Errorf("panes failed"))
		DefaultRunner = mock

		_, err := ListAllPanes()
		assert.Error(t, err)
	})

	t.Run("empty_output_returns_empty_map", func(t *testing.T) {
		mock := newMock()
		mock.set("list-panes", "", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("panes_grouped_by_window_index", func(t *testing.T) {
		mock := newMock()
		line1 := fields("1", "%0", "0", "1", "bash", "t", "99990", "0", "", "0", "0", "/a", "", "", "bash", "80", "24")
		line2 := fields("2", "%1", "0", "0", "vim", "t", "99989", "0", "", "0", "0", "/b", "", "", "vim", "80", "24")
		mock.set("list-panes", line1+"\n"+line2+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		assert.Len(t, result[1], 1)
		assert.Len(t, result[2], 1)
		assert.Equal(t, "bash", result[1][0].Command)
		assert.Equal(t, "vim", result[2][0].Command)
	})
}

func TestListWindowsWithPanes(t *testing.T) {
	restoreState(t)

	t.Run("panes_assigned_to_window", func(t *testing.T) {
		mock := newMock()
		winLine := fields("@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", winLine+"\n", nil)
		paneLine := fields("0", "%0", "0", "1", "bash", "shell",
			"99985", "0", "", "0", "0", "/home", "", "", "bash", "80", "24")
		mock.set("list-panes", paneLine+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 1)
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
		}
	})

	t.Run("panes_sorted_by_visual_position", func(t *testing.T) {
		mock := newMock()
		winLine := fields("@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", winLine+"\n", nil)

		// pane B at left=40; pane A at left=0 — A must sort first
		paneB := fields("0", "%1", "1", "0", "vim", "b",
			"99984", "0", "", "0", "40", "/tmp", "", "", "vim", "40", "24")
		paneA := fields("0", "%0", "0", "1", "bash", "a",
			"99983", "0", "", "0", "0", "/tmp", "", "", "bash", "40", "24")
		mock.set("list-panes", paneA+"\n"+paneB+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) && assert.Len(t, windows[0].Panes, 2) {
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
			assert.Equal(t, "vim", windows[0].Panes[1].Command)
		}
	})

	t.Run("window_busy_set_from_busy_pane", func(t *testing.T) {
		mock := newMock()
		winLine := fields("@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", winLine+"\n", nil)
		paneLine := fields("0", "%0", "0", "1", "make", "build",
			"99982", "0", "", "0", "0", "/tmp", "", "", "make", "80", "24")
		mock.set("list-panes", paneLine+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.True(t, windows[0].Busy)
		}
	})

	t.Run("sidebar_panes_filtered_in_post_pass", func(t *testing.T) {
		mock := newMock()
		winLine := fields("@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", winLine+"\n", nil)

		normalPane := fields("0", "%0", "0", "1", "bash", "t",
			"99981", "0", "", "0", "0", "/tmp", "", "", "bash", "80", "24")
		sidebarPane := fields("0", "%1", "1", "0", "sidebar-renderer", "t",
			"99980", "0", "", "0", "40", "/tmp", "", "", "sidebar-renderer", "10", "24")
		mock.set("list-panes", normalPane+"\n"+sidebarPane+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 1, "sidebar pane should be removed")
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
		}
	})

	t.Run("panes_reindexed_after_sort", func(t *testing.T) {
		mock := newMock()
		winLine := fields("@1", "0", "main", "1", "0", "0", "0", "0",
			"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
		mock.set("list-windows", winLine+"\n", nil)

		pane1 := fields("0", "%0", "0", "1", "bash", "t",
			"99979", "0", "", "10", "0", "/tmp", "", "", "bash", "80", "12")
		pane2 := fields("0", "%1", "1", "0", "vim", "t",
			"99978", "0", "", "0", "0", "/tmp", "", "", "vim", "80", "10")
		mock.set("list-panes", pane1+"\n"+pane2+"\n", nil)
		DefaultRunner = mock

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) && assert.Len(t, windows[0].Panes, 2) {
			assert.Equal(t, 0, windows[0].Panes[0].Index)
			assert.Equal(t, 1, windows[0].Panes[1].Index)
		}
	})
}

func TestListWindowsWithPanes_WindowNotBusyByDefault(t *testing.T) {
	t.Run("window is not busy by default", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := listPanesFields(
			"%0", "0", "0", "bash", "Shell",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "0",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.False(t, windows[0].Busy)
		}
	})
}

func TestListWindowsWithPanes_FiltersSidebarStartCommand(t *testing.T) {
	t.Run("filters out panes with sidebar in start_command", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Sidebar",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "sidebar", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "Shell",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 1)
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
		}
	})
}

func TestListWindowsWithPanes_FiltersPaneHeaderStartCommand(t *testing.T) {
	t.Run("filters out panes with pane-header in start_command", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Header",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "pane-header", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "Shell",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 1)
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
		}
	})
}

func TestListWindowsWithPanes_SortsPanesByTopThenLeft(t *testing.T) {
	t.Run("sorts panes by top position then left position", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Pane0",
			"99990", "1700000000", "", "1", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "Pane1",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%2", "2", "0", "bash", "Pane2",
			"99992", "1700000000", "", "0", "5",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) && assert.Len(t, windows[0].Panes, 3) {
			assert.Equal(t, "Pane1", windows[0].Panes[0].Title)
			assert.Equal(t, "Pane2", windows[0].Panes[1].Title)
			assert.Equal(t, "Pane0", windows[0].Panes[2].Title)
			assert.Equal(t, 0, windows[0].Panes[0].Index)
			assert.Equal(t, 1, windows[0].Panes[1].Index)
			assert.Equal(t, 2, windows[0].Panes[2].Index)
		}
	})
}

func TestListWindowsWithPanes_MultipleWindowsWithDifferentPanes(t *testing.T) {
	t.Run("handles multiple windows with different pane configurations", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "") + "\n" +
			fields("%1", "1", "window1", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "W0P0",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "W0P1",
			"99991", "1700000000", "", "0", "5",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"1", "%2", "0", "1", "bash", "W1P0",
			"99992", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 2) {
			assert.Len(t, windows[0].Panes, 2)
			assert.Len(t, windows[1].Panes, 1)
		}
	})
}

func TestListWindowsWithPanes_ReindexesPanesSequentially(t *testing.T) {
	t.Run("reindexes panes sequentially after filtering and sorting", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "5", "1", "bash", "Pane0",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%1", "10", "0", "bash", "Pane1",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) && assert.Len(t, windows[0].Panes, 2) {
			assert.Equal(t, 0, windows[0].Panes[0].Index)
			assert.Equal(t, 1, windows[0].Panes[1].Index)
		}
	})
}

func TestListWindowsWithPanes_EmptyWindowList(t *testing.T) {
	t.Run("handles empty window list", func(t *testing.T) {
		windowOutput := ""
		paneOutput := ""
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		assert.Len(t, windows, 0)
	})
}

// ── Additional branch-coverage tests ─────────────────────────────────────────

// funcRunner implements Runner via a closure — useful for call-count-based dispatch.
type funcRunner struct {
	fn func(args ...string) ([]byte, error)
}

func (f *funcRunner) Run(args ...string) ([]byte, error) { return f.fn(args...) }

// ListWindows: invalid window index (parts[1] not a number) → window skipped
func TestListWindows_InvalidWindowIndex(t *testing.T) {
	restoreState(t)
	mock := newMock()
	bad := fields("@1", "bad", "win", "1", "0", "0", "0", "0",
		"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
	good := fields("@2", "1", "win2", "0", "0", "0", "0", "0",
		"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
	mock.set("list-windows", bad+"\n"+good+"\n", nil)
	DefaultRunner = mock

	windows, err := ListWindows()
	assert.NoError(t, err)
	if assert.Len(t, windows, 1) {
		assert.Equal(t, 1, windows[0].Index)
	}
}

// ListWindows: @tabby_activity (parts[12]="1") sets Activity=true
func TestListWindows_TabbyActivitySetsActivity(t *testing.T) {
	restoreState(t)
	mock := newMock()
	line := fields("@1", "0", "win", "1", "0", "0", "0", "0",
		"", "", "0", "", "1", "", "", "", "", "1", "$0", "", "", "", "")
	mock.set("list-windows", line+"\n", nil)
	DefaultRunner = mock

	windows, err := ListWindows()
	assert.NoError(t, err)
	if assert.Len(t, windows, 1) {
		assert.True(t, windows[0].Activity)
	}
}

// ListWindows: @tabby_silence (parts[13]="1") sets Silence=true
func TestListWindows_TabbySilenceSetsSilence(t *testing.T) {
	restoreState(t)
	mock := newMock()
	line := fields("@1", "0", "win", "1", "0", "0", "0", "0",
		"", "", "0", "", "", "1", "", "", "", "1", "$0", "", "", "", "")
	mock.set("list-windows", line+"\n", nil)
	DefaultRunner = mock

	windows, err := ListWindows()
	assert.NoError(t, err)
	if assert.Len(t, windows, 1) {
		assert.True(t, windows[0].Silence)
	}
}

// ListPanesForWindow: invalid pane index (parts[1] not a number) → pane skipped
func TestListPanesForWindow_InvalidPaneIndex(t *testing.T) {
	restoreState(t)
	mock := newMock()
	bad := listPanesFields("%0", "bad", "1", "bash", "Shell",
		"99990", "1700000000", "", "0", "0", "/home", "", "", "bash", "0")
	good := listPanesFields("%1", "1", "0", "bash", "Shell2",
		"99991", "1700000000", "", "0", "0", "/home", "", "", "bash", "0")
	mock.set("list-panes", bad+"\n"+good+"\n", nil)
	DefaultRunner = mock

	panes, err := ListPanesForWindow(0)
	assert.NoError(t, err)
	if assert.Len(t, panes, 1) {
		assert.Equal(t, 1, panes[0].Index)
	}
}

// ListAllPanes: session target → uses -s -t flags instead of -a
func TestListAllPanes_SessionTargetUsesDashS(t *testing.T) {
	restoreState(t)
	mock := newMock()
	SetSessionTarget("$1")
	paneLine := fields("0", "%0", "0", "1", "bash", "Shell",
		"99990", "1700000000", "", "0", "0", "/home", "", "", "bash", "80", "24")
	mock.set("list-panes", paneLine+"\n", nil)
	DefaultRunner = mock

	_, err := ListAllPanes()
	assert.NoError(t, err)

	found := false
	for _, call := range mock.calls {
		for _, arg := range call {
			if arg == "-s" {
				found = true
			}
		}
	}
	assert.True(t, found, "session target should use -s flag")
}

// ListAllPanes: invalid pane index (parts[2] not a number) → line skipped
func TestListAllPanes_InvalidPaneIndex(t *testing.T) {
	restoreState(t)
	mock := newMock()
	bad := fields("0", "%0", "bad", "1", "bash", "Shell",
		"99990", "1700000000", "", "0", "0", "/home", "", "", "bash", "80", "24")
	good := fields("0", "%1", "1", "0", "bash", "Shell2",
		"99991", "1700000000", "", "0", "0", "/home", "", "", "bash", "80", "24")
	mock.set("list-panes", bad+"\n"+good+"\n", nil)
	DefaultRunner = mock

	result, err := ListAllPanes()
	assert.NoError(t, err)
	if assert.Contains(t, result, 0) {
		assert.Len(t, result[0], 1)
	}
}

// ListWindowsWithPanes: ListWindows error → propagated
func TestListWindowsWithPanes_ListWindowsError(t *testing.T) {
	restoreState(t)
	mock := newMock()
	mock.set("list-windows", "", fmt.Errorf("tmux died"))
	DefaultRunner = mock

	_, err := ListWindowsWithPanes()
	assert.Error(t, err)
}

// ListWindowsWithPanes: ListAllPanes failure → falls back to per-window ListPanesForWindow
func TestListWindowsWithPanes_ListAllPanesFallback(t *testing.T) {
	restoreState(t)
	winLine := fields("@1", "0", "window0", "1", "0", "0", "0", "0",
		"", "", "0", "", "", "", "", "", "", "1", "$0", "", "", "", "")
	// ListPanesForWindow format (15 fields, no window_index)
	paneLine := listPanesFields("%0", "0", "1", "bash", "Shell",
		"99990", "1700000000", "", "0", "0", "/home", "", "", "bash", "0")

	callCount := 0
	DefaultRunner = &funcRunner{fn: func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "list-windows" {
			return []byte(winLine + "\n"), nil
		}
		if len(args) > 0 && args[0] == "list-panes" {
			callCount++
			if callCount == 1 {
				// First call is ListAllPanes → fail
				return nil, fmt.Errorf("bulk query failed")
			}
			// Subsequent calls are ListPanesForWindow → succeed
			return []byte(paneLine + "\n"), nil
		}
		return []byte(""), nil
	}}

	windows, err := ListWindowsWithPanes()
	assert.NoError(t, err)
	if assert.Len(t, windows, 1) {
		assert.Len(t, windows[0].Panes, 1)
		assert.Equal(t, "bash", windows[0].Panes[0].Command)
	}
}

func TestListWindowsWithPanes_AllPanesFiltered(t *testing.T) {
	t.Run("handles window with all panes filtered out", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Sidebar",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "sidebar", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "Header",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "pane-header", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 0)
		}
	})
}

func TestListAllPanes_RemoteActivityDetection(t *testing.T) {
	restoreState(t)
	t.Run("detects remote command activity within 3 seconds", func(t *testing.T) {
		mock := newMock()
		now := time.Now().Unix()
		recentActivity := now - 2 // 2 seconds ago
		mock.set("list-panes", fields(
			"0", "%0", "0", "0", "ssh", "Shell",
			"99990", fmt.Sprintf("%d", recentActivity), "", "0", "0",
			"/home", "0", "", "ssh", "80", "24")+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		if assert.Len(t, result[0], 1) {
			assert.True(t, result[0][0].Busy, "remote pane with recent activity should be busy")
		}
	})
}

func TestListAllPanes_CollapsedPaneDetection(t *testing.T) {
	restoreState(t)
	t.Run("detects collapsed pane from parts[12]", func(t *testing.T) {
		mock := newMock()
		mock.set("list-panes", fields(
			"0", "%0", "0", "0", "bash", "Shell",
			"99990", "1700000000", "", "0", "0",
			"/home", "1", "", "bash", "80", "24")+"\n", nil)
		DefaultRunner = mock

		result, err := ListAllPanes()
		assert.NoError(t, err)
		if assert.Len(t, result[0], 1) {
			assert.True(t, result[0][0].Collapsed)
		}
	})
}

func TestListWindowsWithPanes_SortsPanesByPosition(t *testing.T) {
	restoreState(t)
	t.Run("sorts panes by top then left position", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		// Create panes with different positions: (top=1,left=0), (top=0,left=0), (top=0,left=1)
		// Use 17-field format: window_index, pane_id, pane_index, pane_active, pane_current_command, pane_title,
		// pane_pid, pane_last_activity, @tabby_pane_title, pane_top, pane_left,
		// pane_current_path, @tabby_pane_collapsed, @tabby_pane_prev_height, pane_start_command, pane_width, pane_height
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Shell",
			"99990", "1700000000", "", "1", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "bash", "Shell",
			"99991", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%2", "2", "0", "bash", "Shell",
			"99992", "1700000000", "", "0", "1",
			"/home", "0", "", "bash", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) && assert.Len(t, windows[0].Panes, 3) {
			// After sorting by top then left, order should be: (0,0), (0,1), (1,0)
			assert.Equal(t, 0, windows[0].Panes[0].Top)
			assert.Equal(t, 0, windows[0].Panes[0].Left)
			assert.Equal(t, 0, windows[0].Panes[1].Top)
			assert.Equal(t, 1, windows[0].Panes[1].Left)
			assert.Equal(t, 1, windows[0].Panes[2].Top)
			assert.Equal(t, 0, windows[0].Panes[2].Left)
		}
	})
}

func TestListWindowsWithPanes_PropagatesWindowBusyFromPane(t *testing.T) {
	restoreState(t)
	t.Run("sets window busy when any pane is busy", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "make", "Shell",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "make", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.True(t, windows[0].Busy)
		}
	})
}

func TestListWindowsWithPanes_FiltersSidebarPanes(t *testing.T) {
	restoreState(t)
	t.Run("filters out sidebar-renderer panes", func(t *testing.T) {
		windowOutput := fields("%0", "0", "window0", "0", "", "", "", "", "")
		paneOutput := fields(
			"0", "%0", "0", "1", "bash", "Shell",
			"99990", "1700000000", "", "0", "0",
			"/home", "0", "", "bash", "80", "24",
		) + "\n" + fields(
			"0", "%1", "1", "0", "sidebar-renderer", "Sidebar",
			"99991", "1700000000", "", "0", "1",
			"/home", "0", "", "sidebar-renderer", "80", "24",
		)
		mock := &mockRunner{
			responses: map[string]mockResp{
				"list-windows": {output: []byte(windowOutput), err: nil},
				"list-panes":   {output: []byte(paneOutput), err: nil},
			},
		}
		DefaultRunner = mock
		defer restoreState(t)

		windows, err := ListWindowsWithPanes()
		assert.NoError(t, err)
		if assert.Len(t, windows, 1) {
			assert.Len(t, windows[0].Panes, 1, "sidebar pane should be filtered out")
			assert.Equal(t, "bash", windows[0].Panes[0].Command)
		}
	})
}
