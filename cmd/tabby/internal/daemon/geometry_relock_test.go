package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A relock request must both raise the flag the geometry tick reads and wake
// that tick: the lease handoff it fires from is not itself a size change, so
// nothing else would schedule one until the user resized the terminal again.
func TestRequestGeometryRelock_SetsFlagAndWakesTheGeometryTick(t *testing.T) {
	l := NewLoop(nil, nil, nil)

	l.RequestGeometryRelock("ownership_gained")

	assert.True(t, l.relockGeom.Load())
	assert.True(t, l.flags.geom.Load())
	select {
	case ev := <-l.events:
		assert.IsType(t, ClientGeomTickEvent{}, ev)
	default:
		t.Fatal("relock request enqueued no geometry tick")
	}
}
