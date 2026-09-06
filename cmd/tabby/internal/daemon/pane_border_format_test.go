package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The "[prefix+, for actions]" hint is right-aligned against the window/pane
// label. It is 23 columns wide, so on a 49-column phone window it would leave
// the label nothing to occupy; the format drops it below the same 100-column
// boundary computeProfile uses. Verified live at 49 and 140 columns.
func TestPaneBorderFormatDropsHintOnNarrowWindows(t *testing.T) {
	f := paneBorderFormat()

	assert.Contains(t, f, "#[align=right]#{?#{e|>=:#{window_width},100},[prefix+#, for actions] ,}",
		"the hint must stay inside a window_width guard")
	assert.False(t, strings.Contains(f, "#[align=right][prefix+"),
		"an unguarded hint would swallow a phone-width label")
}

// On a phone the labelled strip is the divider above the button bar, which
// physically belongs to the window-header pane. Labelling it with the plain
// pane accessors would describe that pane ("tabby | window-header  b"), so the
// phone branch re-sources them from the window's active pane.
func TestPaneBorderFormatPhoneDividerDescribesTheActivePane(t *testing.T) {
	f := paneBorderFormat()

	assert.Contains(t, f, "#{P:#{?pane_active,#{pane_current_command},}}",
		"the divider label must read across to the active pane")
	assert.Contains(t, f, windowHeaderMatch,
		"the button-bar pane carries the bottom phone label, not the sidebar")
}

// The phone shows the label twice: on the content pane's own strip at the top
// of the screen, and above the button bar at the bottom. The top one is the
// plain pane-scoped label, with no narrow-window guard blanking it.
func TestPaneBorderFormatLabelsContentPaneAtEveryWidth(t *testing.T) {
	f := paneBorderFormat()

	direct := paneBorderLabel("#{pane_title}", "#{pane_current_command}", "#{b:pane_current_path}")
	assert.Contains(t, f, direct)
	assert.Equal(t, 1, strings.Count(f, narrowWindow),
		"the only width test left is the one picking the button bar's label source")
}

// fromActivePane is the one piece with no plain-text fallback: a typo yields a
// format tmux silently renders as empty, so pin its exact shape.
func TestFromActivePane(t *testing.T) {
	assert.Equal(t, "#{P:#{?pane_active,#{pane_title},}}", fromActivePane("#{pane_title}"))
}
