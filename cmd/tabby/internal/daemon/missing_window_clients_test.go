package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The daemon notes a client whose window has vanished. It cannot actually
// disconnect that client — the socket closes on its own, and a sidebar process
// that outlives its window (parked in _tabby_limbo) never closes it — so the
// same line repeated on every full refresh. One live log carried 282 copies of
// a single window's line, which is pure noise in exactly the place you go
// looking during an investigation.
func TestNewlyMissingWindowClients_ReportsEachClientOnce(t *testing.T) {
	reported := map[string]bool{}
	live := map[string]bool{"@1": true}

	first := newlyMissingWindowClients([]string{"@1", "@28"}, live, reported)
	assert.Equal(t, []string{"@28"}, first, "the missing window is named the first time")

	second := newlyMissingWindowClients([]string{"@1", "@28"}, live, reported)
	assert.Empty(t, second, "and stays quiet on every refresh after")
}

// Header clients are keyed differently and die with their window/pane, so they
// are never worth reporting.
func TestNewlyMissingWindowClients_IgnoresHeaderClients(t *testing.T) {
	reported := map[string]bool{}
	got := newlyMissingWindowClients([]string{"window-header:@9", "header:%9"}, map[string]bool{}, reported)
	assert.Empty(t, got)
	assert.Empty(t, reported, "no note is kept for a header client either")
}

// A minimized window comes back on un-minimize. If the note survived that, a
// genuine second disappearance would go unrecorded.
func TestNewlyMissingWindowClients_ReArmsWhenTheWindowReturns(t *testing.T) {
	reported := map[string]bool{}

	newlyMissingWindowClients([]string{"@23"}, map[string]bool{}, reported)
	assert.True(t, reported["@23"])

	newlyMissingWindowClients([]string{"@23"}, map[string]bool{"@23": true}, reported)
	assert.False(t, reported["@23"], "the window came back, so the note is cleared")

	again := newlyMissingWindowClients([]string{"@23"}, map[string]bool{}, reported)
	assert.Equal(t, []string{"@23"}, again, "a second disappearance is reported again")
}

// The note is keyed by client id, so it has to be dropped when that client
// disconnects — otherwise the map grows for the daemon's lifetime and a later
// client reusing the id would be silently suppressed.
func TestNewlyMissingWindowClients_ForgetsDisconnectedClients(t *testing.T) {
	reported := map[string]bool{"@28": true}

	newlyMissingWindowClients([]string{"@1"}, map[string]bool{"@1": true}, reported)

	assert.Empty(t, reported, "a client that is no longer connected leaves no note behind")
}
