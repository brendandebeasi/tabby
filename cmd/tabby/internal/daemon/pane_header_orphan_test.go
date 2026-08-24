package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// hdr builds a `list-panes -a` row in the format spawnPaneHeaders asks for.
func hdr(paneID, curCmd, startCmd string) string {
	return paneID + "|||" + curCmd + "|||" + startCmd
}

// paneHeaderStart is what tmux reports as pane_start_command for a header
// spawned by spawnPaneHeaders.
func paneHeaderStart(target string) string {
	return "printf '\\033[?25l\\033[2J\\033[H' && /opt/tabby/bin/tabby render pane-header -session '$1' -pane '" + target + "'"
}

// orphanTargets is the classification spawnPaneHeaders acts on: a header whose
// target pane is absent from the whole server listing.
func orphanTargets(lines []string) map[string][]string {
	headersByTarget, _, livePaneIDs := classifyPaneHeaders(lines)
	orphans := map[string][]string{}
	for target, panes := range headersByTarget {
		if !livePaneIDs[target] {
			orphans[target] = panes
		}
	}
	return orphans
}

func TestClassifyPaneHeadersGroupsByTargetPane(t *testing.T) {
	lines := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "tabby", paneHeaderStart("%1")),
		hdr("%3", "vim", "vim"),
		hdr("%4", "tabby", paneHeaderStart("%3")),
	}
	byTarget, startCmds, live := classifyPaneHeaders(lines)

	assert.Equal(t, map[string][]string{"%1": {"%2"}, "%3": {"%4"}}, byTarget)
	assert.Equal(t, paneHeaderStart("%1"), startCmds["%2"])
	// Every row contributes to the live set, headers included — a header is a
	// real pane and can itself be another pass's target of a kill.
	assert.Equal(t, map[string]bool{"%1": true, "%2": true, "%3": true, "%4": true}, live)
}

func TestClassifyPaneHeadersFindsOrphanAfterSplitClosed(t *testing.T) {
	// A two-pane window: %1 and %3, each with a header. The user closes %3.
	// tmux takes the content pane and leaves its header (%4) standing, grown
	// into the space %3 vacated — the ghost split.
	afterClose := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "tabby", paneHeaderStart("%1")),
		hdr("%4", "tabby", paneHeaderStart("%3")),
	}

	assert.Equal(t, map[string][]string{"%3": {"%4"}}, orphanTargets(afterClose))
}

func TestClassifyPaneHeadersLeavesLiveHeadersAlone(t *testing.T) {
	// Nothing was closed, so nothing may be reaped. A pass that got this wrong
	// would strip the chrome off every pane in the session.
	lines := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "tabby", paneHeaderStart("%1")),
		hdr("%3", "vim", "vim"),
		hdr("%4", "tabby", paneHeaderStart("%3")),
	}

	assert.Empty(t, orphanTargets(lines))
}

func TestClassifyPaneHeadersKeepsStashedPanesLive(t *testing.T) {
	// A pane parked in _tabby_limbo or _tabby_minimized is still in the
	// `list-panes -a` output, because that walks every session on the server.
	// It is coming back, so its header must survive — reaping it here would
	// leave the pane bare the moment it was restored.
	lines := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "tabby", paneHeaderStart("%1")),
		hdr("%9", "zsh", "zsh"), // stashed in _tabby_limbo
		hdr("%10", "tabby", paneHeaderStart("%9")),
	}

	assert.Empty(t, orphanTargets(lines))
}

func TestClassifyPaneHeadersReapsEveryOrphanForOneTarget(t *testing.T) {
	// Two daemons in a grouped session each spawned a header for %3 before it
	// was closed. Both are orphans: deduping them down to one survivor would
	// leave that survivor to time itself out on its own five-second tick,
	// which is the delay this pass exists to remove.
	lines := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "tabby", paneHeaderStart("%1")),
		hdr("%4", "tabby", paneHeaderStart("%3")),
		hdr("%5", "tabby", paneHeaderStart("%3")),
	}

	assert.Equal(t, map[string][]string{"%3": {"%4", "%5"}}, orphanTargets(lines))
}

func TestClassifyPaneHeadersIgnoresUnparseableRows(t *testing.T) {
	lines := []string{
		"",
		"%1|||zsh", // truncated: no start command field
		hdr("%2", "tabby", "tabby render pane-header -session '$1'"), // header with no -pane
		hdr("%3", "zsh", "zsh"),
	}
	byTarget, _, live := classifyPaneHeaders(lines)

	// A header naming no pane is not attributed to any target, so it is never
	// reaped as an orphan — it is left for the dedup/spawn passes to sort out.
	assert.Empty(t, byTarget)
	assert.Equal(t, map[string]bool{"%2": true, "%3": true}, live)
}

func TestClassifyPaneHeadersDetectsHeaderByCurrentCommand(t *testing.T) {
	// pane_current_command is "tabby" for every renderer post-consolidation,
	// but an older or differently-spawned header can still report
	// "pane-header" there while its start command is something else.
	lines := []string{
		hdr("%1", "zsh", "zsh"),
		hdr("%2", "pane-header", "/opt/tabby/bin/pane-header -pane %1"),
	}
	byTarget, _, _ := classifyPaneHeaders(lines)

	assert.Equal(t, map[string][]string{"%1": {"%2"}}, byTarget)
}
