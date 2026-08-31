package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// What these cover: the geometry tick forks a `tmux list-clients` on every
// pass, so an idle daemon polling at the base rate burns a subprocess four
// times a second forever. geomTickInterval widens that gap while nothing is
// happening. The safety property is that any user action resets it — see
// TestPriorityEventResetsTheGeomBackoff, which is what keeps a backed-off
// daemon responsive when the user picks up a second attached client.

// activityOnlyThrottled mirrors the decision handleClientGeomTick makes for a
// wake where the elected client and its size are unchanged and only the
// activity clock moved. Kept in the test rather than exported from the loop so
// the production path stays a straight line.
func activityOnlyThrottled(l *Loop, resizeKey string, relock bool, now time.Time) bool {
	activityOnly := !relock && l.lastResizeKey != "" && resizeKey == l.lastResizeKey
	return activityOnly && now.Sub(l.lastActivityReconcile) < activityReconcileInterval
}

func TestActivityOnlyWakeIsThrottledNotDropped(t *testing.T) {
	const key = "/dev/ttys024:171x50"
	t0 := time.Unix(2000, 0)
	l := &Loop{lastResizeKey: key, lastActivityReconcile: t0}

	// The observed pathology: same tty, same size, every few seconds forever.
	assert.True(t, activityOnlyThrottled(l, key, false, t0.Add(5*time.Second)),
		"a 5s activity-bucket roll must not drag a reconcile along")
	assert.True(t, activityOnlyThrottled(l, key, false, t0.Add(29*time.Second)))

	// But it is a floor, not a mute: the reconcile doubles as a periodic
	// refresh, so it still has to come around.
	assert.False(t, activityOnlyThrottled(l, key, false, t0.Add(31*time.Second)),
		"past the interval the periodic refresh must still run")
}

func TestRealGeometryChangeIsNeverThrottled(t *testing.T) {
	t0 := time.Unix(2000, 0)
	l := &Loop{lastResizeKey: "/dev/ttys024:171x50", lastActivityReconcile: t0}
	now := t0.Add(time.Second)

	assert.False(t, activityOnlyThrottled(l, "/dev/ttys024:100x50", false, now),
		"an actual resize must reconcile immediately")
	assert.False(t, activityOnlyThrottled(l, "/dev/ttys099:171x50", false, now),
		"switching to another client must reconcile immediately")
	assert.False(t, activityOnlyThrottled(l, "/dev/ttys024:171x50", true, now),
		"a relock request must never be throttled")
}

func TestFirstGeometryIsNeverThrottled(t *testing.T) {
	// A daemon that has not laid anything out yet has no lastResizeKey; it
	// must reconcile rather than sit on an empty layout for 30s.
	l := &Loop{lastActivityReconcile: time.Unix(2000, 0)}
	assert.False(t, activityOnlyThrottled(l, "/dev/ttys024:171x50", false, time.Unix(2001, 0)))
}

func TestGeomTickStaysAtBaseRateThroughTheGracePeriod(t *testing.T) {
	l := &Loop{}

	for i := 0; i < clientGeomIdleGrace; i++ {
		assert.Equal(t, clientGeomBase, l.geomTickInterval(),
			"a short lull between keystrokes must not cost responsiveness")
		l.geomIdleTicks.Add(1)
	}
}

func TestGeomTickBacksOffOnceIdlePastTheGrace(t *testing.T) {
	l := &Loop{}

	l.geomIdleTicks.Store(clientGeomIdleGrace + 1)
	assert.Equal(t, 500*time.Millisecond, l.geomTickInterval())

	l.geomIdleTicks.Store(clientGeomIdleGrace + 2)
	assert.Equal(t, time.Second, l.geomTickInterval())
}

func TestGeomTickBackoffIsCappedAndNeverRunsAway(t *testing.T) {
	l := &Loop{}

	// A daemon left alone overnight must not drift to an hour-long gap.
	for _, idle := range []int64{clientGeomIdleGrace + 3, clientGeomIdleGrace + 50, 1 << 40} {
		l.geomIdleTicks.Store(idle)
		assert.Equal(t, clientGeomMax, l.geomTickInterval(),
			"idle=%d should clamp to the ceiling", idle)
	}
}

func TestGeomTickReturnsToBaseRateWhenWorkResumes(t *testing.T) {
	l := &Loop{}
	l.geomIdleTicks.Store(clientGeomIdleGrace + 99)
	assert.Equal(t, clientGeomMax, l.geomTickInterval())

	// This is the store the handler makes as soon as a tick finds real work.
	l.geomIdleTicks.Store(0)
	assert.Equal(t, clientGeomBase, l.geomTickInterval(),
		"a resize must be polled for at full rate again immediately")
}

func TestPriorityEventResetsTheGeomBackoff(t *testing.T) {
	// The whole safety argument for backing off with several clients attached
	// is that touching one of them generates a priority event.
	for _, ev := range []Event{RendererInputEvent{}, TmuxHookEvent{}} {
		l := &Loop{
			events: make(chan Event, 4),
			inputs: make(chan Event, 4),
		}
		l.geomIdleTicks.Store(clientGeomIdleGrace + 99)

		l.Submit(ev)

		assert.Equal(t, clientGeomBase, l.geomTickInterval(),
			"%T is a user action and must restore full-rate polling", ev)
	}
}

func TestBackgroundEventDoesNotResetTheGeomBackoff(t *testing.T) {
	// Ticks are not user actions; if they reset the count, the daemon could
	// never back off at all and the fix would be inert.
	l := &Loop{
		events: make(chan Event, 4),
		inputs: make(chan Event, 4),
	}
	l.geomIdleTicks.Store(clientGeomIdleGrace + 99)

	l.Submit(AnimationTickEvent{})

	assert.Equal(t, clientGeomMax, l.geomTickInterval(),
		"a background tick must not masquerade as user activity")
}

func TestGeometryRelockRequestResetsTheBackoff(t *testing.T) {
	l := &Loop{}
	l.geomIdleTicks.Store(clientGeomIdleGrace + 99)

	l.RequestGeometryRelock("test")

	assert.Equal(t, clientGeomBase, l.geomTickInterval(),
		"a relock arrives on a backed-off ticker and must speed it back up")
}

func TestBackoffTickerRecomputesTheIntervalEveryPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intervals := make(chan time.Duration, 8)
	fired := make(chan struct{}, 8)
	runs := 0

	go runBackoffTicker(ctx, func() time.Duration {
		// Widen after the first pass, so a fixed-interval implementation
		// (one that samples the interval once) fails this.
		runs++
		d := time.Millisecond
		if runs > 1 {
			d = 2 * time.Millisecond
		}
		intervals <- d
		return d
	}, func() { fired <- struct{}{} })

	for i := 0; i < 2; i++ {
		select {
		case <-fired:
		case <-time.After(2 * time.Second):
			t.Fatal("ticker stopped firing")
		}
	}
	assert.Equal(t, time.Millisecond, <-intervals)
	assert.Equal(t, 2*time.Millisecond, <-intervals)
}

func TestBackoffTickerStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		runBackoffTicker(ctx, func() time.Duration { return time.Hour }, func() {})
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runBackoffTicker ignored context cancellation")
	}
}
