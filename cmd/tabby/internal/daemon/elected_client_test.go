package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A detached daemon reads width 0. ActiveClientProfile has to answer something,
// and it answers "desktop" — which is indistinguishable from a real desktop
// client unless the caller asks HasElectedClient. Every shared-window mutation
// gates on the latter, so pin the difference.
func TestHasElectedClientSeparatesDetachedFromDesktop(t *testing.T) {
	c := &Coordinator{}

	assert.False(t, c.HasElectedClient(), "no width stored yet: nothing elected")
	assert.Equal(t, "desktop", c.ActiveClientProfile(),
		"the profile fallback is why HasElectedClient has to exist")

	c.activeClientWidth.Store(200)
	assert.True(t, c.HasElectedClient())
	assert.Equal(t, "desktop", c.ActiveClientProfile())

	c.activeClientWidth.Store(43)
	assert.True(t, c.HasElectedClient(), "a phone is still an elected client")
	assert.Equal(t, "phone", c.ActiveClientProfile())
}

// A client that detaches must take the daemon back out of the set allowed to
// restyle shared windows, or the stale width keeps it acting on a peer's tab.
func TestHasElectedClientGoesFalseWhenWidthClears(t *testing.T) {
	c := &Coordinator{}
	c.activeClientWidth.Store(43)
	assert.True(t, c.HasElectedClient())

	c.activeClientWidth.Store(0)
	assert.False(t, c.HasElectedClient())
}

// The geometry tick early-returns on an empty election, so without an explicit
// clear a departed client's width (and phone profile) would persist forever —
// and a later layout-lease handoff would apply that stale profile's chrome to
// the group's windows. ClearActiveClient must drop the snapshot and return the
// profile to the desktop fallback.
func TestClearActiveClientDropsStalePhoneProfile(t *testing.T) {
	c := &Coordinator{}
	c.activeClientWidth.Store(67)
	assert.Equal(t, "phone", c.ActiveClientProfile())

	c.ClearActiveClient("test")
	assert.False(t, c.HasElectedClient())
	assert.Equal(t, "desktop", c.ActiveClientProfile())

	snap := c.ActiveClientSnapshot()
	assert.Equal(t, "desktop", snap.Profile)
	assert.Equal(t, 0, snap.Width)

	// Clearing schedules a debounced profile transition that would fire real
	// tmux commands against the developer's server mid-suite; stop it.
	profileTransitionMu.Lock()
	if profileTransitionTimer != nil {
		profileTransitionTimer.Stop()
		profileTransitionTimer = nil
		pendingProfile = ""
	}
	profileTransitionMu.Unlock()
}

// Clearing with nothing stored is a no-op (the geom tick calls it every time
// an election comes back empty).
func TestClearActiveClientNoopWhenUnset(t *testing.T) {
	c := &Coordinator{}
	c.ClearActiveClient("test")
	assert.False(t, c.HasElectedClient())
	assert.Equal(t, "desktop", c.ActiveClientProfile())
}
