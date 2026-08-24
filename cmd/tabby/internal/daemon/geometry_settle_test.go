package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The bug these cover: a phone under mosh renegotiates through intermediate
// sizes (43x34 was observed at 82x13 on the way), and the daemon reflowed every
// window's chrome for each one, leaving the sidebar laid out for a width the
// client never settled at.

func TestGeometrySettled_ActivityOnlyChangeIsNotDelayed(t *testing.T) {
	l := &Loop{lastResizeKey: "/dev/ttys051:43x34"}
	t0 := time.Unix(1000, 0)

	assert.True(t, l.geometrySettled("/dev/ttys051:43x34", t0),
		"the size did not move, so there is nothing to debounce")
	assert.Empty(t, l.pendingGeom, "an unchanged size must not become pending")
}

func TestGeometrySettled_FirstGeometryAppliesImmediately(t *testing.T) {
	l := &Loop{} // nothing laid out yet
	t0 := time.Unix(1000, 0)

	assert.True(t, l.geometrySettled("/dev/ttys051:43x34", t0),
		"on first attach there is no correct chrome to protect, so waiting only shows unstyled chrome")
}

func TestGeometrySettled_NewSizeWaitsForTheSettleWindow(t *testing.T) {
	l := &Loop{lastResizeKey: "/dev/ttys051:95x40"}
	t0 := time.Unix(1000, 0)

	assert.False(t, l.geometrySettled("/dev/ttys051:82x13", t0),
		"a size seen for the first time is not yet believable")
	assert.False(t, l.geometrySettled("/dev/ttys051:82x13", t0.Add(clientGeomSettle-time.Millisecond)),
		"still inside the settle window")
	assert.True(t, l.geometrySettled("/dev/ttys051:82x13", t0.Add(clientGeomSettle)),
		"a size that held still for the whole window is real")
}

func TestGeometrySettled_RenegotiationBurstNeverReflows(t *testing.T) {
	l := &Loop{lastResizeKey: "/dev/ttys051:95x40"}
	now := time.Unix(1000, 0)

	// The burst: each intermediate size arrives on its own 250ms tick and is
	// replaced before the settle window elapses. None of them should be
	// believed -- reflowing for 82x13 is what mangled the sidebar.
	for _, size := range []string{"82x13", "43x13", "82x34"} {
		assert.False(t, l.geometrySettled("/dev/ttys051:"+size, now),
			"intermediate size %s must not reflow chrome", size)
		now = now.Add(250 * time.Millisecond)
	}

	// The size it actually settles at still has to earn its own window...
	assert.False(t, l.geometrySettled("/dev/ttys051:43x34", now))
	// ...and then it lands.
	assert.True(t, l.geometrySettled("/dev/ttys051:43x34", now.Add(clientGeomSettle)))
}

func TestGeometrySettled_ReturningToTheLaidOutSizeIsImmediate(t *testing.T) {
	l := &Loop{lastResizeKey: "/dev/ttys051:43x34"}
	t0 := time.Unix(1000, 0)

	// Keyboard opens: a new size shows up and starts settling.
	assert.False(t, l.geometrySettled("/dev/ttys051:43x13", t0))
	// Keyboard closes before it ever settled. We are back at the geometry the
	// chrome is already correct for, so there is nothing to wait for.
	assert.True(t, l.geometrySettled("/dev/ttys051:43x34", t0.Add(100*time.Millisecond)),
		"a bounce back to the current layout must not stall behind the settle window")
}
