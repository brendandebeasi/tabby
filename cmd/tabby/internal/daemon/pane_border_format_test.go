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
		"only the button-bar pane carries the phone label, not the sidebar")
	assert.Contains(t, f, emptyStrip,
		"the content pane's unlabelled strip must blank rather than rule the top row")
}

// fromActivePane is the one piece with no plain-text fallback: a typo yields a
// format tmux silently renders as empty, so pin its exact shape.
func TestFromActivePane(t *testing.T) {
	assert.Equal(t, "#{P:#{?pane_active,#{pane_title},}}", fromActivePane("#{pane_title}"))
}
