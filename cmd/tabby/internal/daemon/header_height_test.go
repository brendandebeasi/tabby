package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The reported symptom was a window header sitting taller than one row on a
// wide window. Live logs from a session with a phone attached show the two
// enforcers fighting over the same pane within one second:
//
//	HEADER_HEIGHT_SYNC    pane=%36087 current=45 target=1 winWidth=167
//	HEADER_HEIGHT_ANOMALY client=window-header:@2105 height=45 desired=3
//
// PlanHeaderHeights asked for 1 (per-window: 167 cols is desktop);
// syncClientSizesFromTmux asked for 3 because the GLOBAL
// desiredWindowHeaderHeight() reports the touch profile whenever the most
// recently active client is narrow. This test pins the distinction that made
// them disagree, so nobody swaps the per-width call back for the global one.
func TestWindowHeaderHeightPerWidthIgnoresPhoneProfile(t *testing.T) {
	c := newTestCoordinator(t)
	c.activeClientWidth.Store(50) // a phone client is the active one

	assert.Equal(t, 3, c.desiredWindowHeaderHeight(),
		"the global profile follows the active client and is only for spawn-time defaults")
	assert.Equal(t, 1, c.desiredWindowHeaderHeightForWidth(167),
		"a 167-col window gets a 1-row desktop header regardless of the phone client")
	assert.Equal(t, 3, c.desiredWindowHeaderHeightForWidth(43),
		"a genuinely narrow window still gets the 3-row touch header")
}

// headerTargetHeight is the single source of truth every enforcer routes
// through. Pane-headers never grow to 3 on phone — the touch button bar lives
// on the window-header instead.
func TestHeaderTargetHeight(t *testing.T) {
	c := newTestCoordinator(t)
	c.activeClientWidth.Store(50)

	tests := []struct {
		name   string
		hdr    headerPaneInfo
		locked int
		want   int
	}{
		{"wide window header", headerPaneInfo{IsWindowHdr: true, WindowWidth: 167}, 0, 1},
		{"narrow window header", headerPaneInfo{IsWindowHdr: true, WindowWidth: 43}, 0, 3},
		{"pane header on a narrow window", headerPaneInfo{WindowWidth: 43}, 0, 1},
		{"pane header on a wide window", headerPaneInfo{WindowWidth: 167}, 0, 1},
		{"locked width overrides a wide window", headerPaneInfo{IsWindowHdr: true, WindowWidth: 167}, 43, 3},
		{"locked width overrides a narrow window", headerPaneInfo{IsWindowHdr: true, WindowWidth: 43}, 167, 1},
		{"locked width is ignored for pane headers", headerPaneInfo{WindowWidth: 167}, 43, 1},
		// A width of 0 means tmux told us nothing; desiredWindowHeaderHeightForWidth
		// treats that as desktop rather than flipping every header to touch.
		{"unknown window width", headerPaneInfo{IsWindowHdr: true}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, c.headerTargetHeight(tt.hdr, tt.locked))
		})
	}
}

func TestHeaderTargetHeightCustomBorder(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.PaneHeader.CustomBorder = true

	assert.Equal(t, 2, c.headerTargetHeight(headerPaneInfo{IsWindowHdr: true, WindowWidth: 167}, 0))
	assert.Equal(t, 2, c.headerTargetHeight(headerPaneInfo{WindowWidth: 167}, 0))
	assert.Equal(t, 3, c.headerTargetHeight(headerPaneInfo{IsWindowHdr: true, WindowWidth: 43}, 0),
		"the touch header wins over CustomBorder on a narrow window")
}

// Grouped sessions link the same windows, so `tmux list-panes -a` reports each
// shared pane once per session in the group. listHeaderPanes reads a row as a
// header to resize, so a duplicated row became a duplicate ResizeOp for the
// same pane in the same batch — six of them, in the live log above.
func TestUniqueByPaneIDDropsGroupedSessionDuplicates(t *testing.T) {
	rows := []string{
		"%1|||45|||window-header|||tabby window-header|||167",
		"%1|||45|||window-header|||tabby window-header|||167",
		"%1|||45|||window-header|||tabby window-header|||167",
		"%2|||1|||pane-header|||tabby pane-header|||167",
		"%2|||1|||pane-header|||tabby pane-header|||167",
	}
	got := uniqueByPaneID(rows, 0)
	assert.Len(t, got, 2)
	assert.Equal(t, rows[0], got[0])
	assert.Equal(t, rows[3], got[1])
}
