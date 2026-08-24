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
