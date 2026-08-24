package daemon

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

// tmux aborts a ";"-separated command sequence at the first command that
// fails, so the pane header writes must not share one argv across windows: a
// single window that died between the refresh snapshot and this write would
// drop the styling for every window after it, leaving their pane headers with
// @tabby_pane_active unset (black text). One batch per window keeps the damage
// local and lets the caller retry the survivors.
func TestBuildPaneHeaderColorArgsBatchesPerWindow(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.AutoBorder = true
	c.grouped = []grouping.GroupedWindows{{
		Name:  "Default",
		Theme: config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1"},
		Windows: []tmux.Window{
			{ID: "@1", Index: 1, Panes: []tmux.Pane{{ID: "%1", Command: "bash"}}},
			{ID: "@2", Index: 2, Panes: []tmux.Pane{{ID: "%2", Command: "bash"}}},
			{ID: "@3", Index: 3, Panes: []tmux.Pane{{ID: "%3", Command: "bash"}}},
		},
	}}

	batches := c.buildPaneHeaderColorArgs()
	if !assert.Len(t, batches, 3, "one batch per window so a stale id costs only that window") {
		return
	}

	for i, want := range []string{"@1", "@2", "@3"} {
		joined := strings.Join(batches[i], " ")
		assert.Contains(t, joined, want)
		for _, other := range []string{"@1", "@2", "@3"} {
			if other != want {
				assert.NotContains(t, joined, other,
					"batch %d must target only %s", i, want)
			}
		}
	}
}

// Each batch is spliced into a larger argv by flattenTmuxBatches, which adds
// the separators. A batch that carried its own leading or trailing ";" would
// produce an empty command and make tmux reject the whole sequence.
func TestBuildPaneHeaderColorArgsBatchSeparators(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.AutoBorder = true
	c.grouped = []grouping.GroupedWindows{{
		Theme: config.Theme{Bg: "#3498db", Fg: "#ffffff"},
		Windows: []tmux.Window{
			{ID: "@1", Index: 1, Panes: []tmux.Pane{{ID: "%1", Command: "bash"}}},
			{ID: "@2", Index: 2, Panes: []tmux.Pane{{ID: "%2", Command: "bash"}}},
		},
	}}

	batches := c.buildPaneHeaderColorArgs()
	assert.NotEmpty(t, batches)
	for i, b := range batches {
		assert.NotEmpty(t, b, "batch %d", i)
		assert.NotEqual(t, ";", b[0], "batch %d has a leading separator", i)
		assert.NotEqual(t, ";", b[len(b)-1], "batch %d has a trailing separator", i)
	}
}

// Ordering within a batch is load-bearing: an aborted sequence still applies
// the commands before the failure, so the header colors must be written before
// the per-pane border styles. That way a pane that died can only cost the
// trailing pane styling, never the header the user actually looks at.
func TestBuildPaneHeaderColorArgsHeaderBeforePaneStyles(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.AutoBorder = true
	c.grouped = []grouping.GroupedWindows{{
		Theme: config.Theme{Bg: "#3498db", Fg: "#ffffff"},
		Windows: []tmux.Window{
			{ID: "@1", Index: 1, Panes: []tmux.Pane{
				{ID: "%1", Command: "bash"},
				{ID: "%2", Command: "bash"},
			}},
		},
	}}

	batches := c.buildPaneHeaderColorArgs()
	assert.Len(t, batches, 1)

	header, paneStyle := -1, -1
	for i, arg := range batches[0] {
		if arg == "@tabby_pane_active" && header < 0 {
			header = i
		}
		// "-p" only appears on the per-pane set-option commands.
		if arg == "-p" && paneStyle < 0 {
			paneStyle = i
		}
	}
	assert.GreaterOrEqual(t, header, 0, "expected an @tabby_pane_active write")
	assert.GreaterOrEqual(t, paneStyle, 0, "expected a per-pane border write")
	assert.Less(t, header, paneStyle, "header color must be written before pane styles")
}

func TestFlattenTmuxBatches(t *testing.T) {
	tests := []struct {
		name string
		in   [][]string
		want []string
	}{
		{"nil", nil, nil},
		{"empty batches dropped", [][]string{{}, {}}, nil},
		{"single", [][]string{{"set-option", "a", "b"}}, []string{"set-option", "a", "b"}},
		{
			"joined with separator",
			[][]string{{"set-option", "a"}, {"set-option", "b"}},
			[]string{"set-option", "a", ";", "set-option", "b"},
		},
		{
			"leading empty does not emit a separator",
			[][]string{{}, {"set-option", "a"}, {}, {"set-option", "b"}},
			[]string{"set-option", "a", ";", "set-option", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, flattenTmuxBatches(tt.in))
		})
	}
}
