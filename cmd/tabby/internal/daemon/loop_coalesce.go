package daemon

import "sync/atomic"

// tickFlags holds per-tick atomic.Bool flags. The flag is set when an event
// has been enqueued for a given tick kind and not yet picked up by the
// handler. Subsequent ticks observed before the handler runs are dropped at
// the producer side, preventing a slow handler from accumulating a backlog of
// duplicate ticks. Each tick handler's first action is to clear its flag, so
// the next tick to fire while the handler is mid-run is allowed to enqueue
// (and will run after the current handler returns).
type tickFlags struct {
	geom      atomic.Bool
	window    atomic.Bool
	anim      atomic.Bool
	refresh   atomic.Bool
	git       atomic.Bool
	autoTheme atomic.Bool
	watchdog  atomic.Bool
	idle      atomic.Bool
	socket    atomic.Bool
}

// submitCoalesced enqueues ev only if flag was previously clear. If the flag
// is already set (a prior tick is still queued or running), this fire is
// dropped silently — coalescing is the explicit goal. If the events channel
// is full we also clear the flag so a future tick can retry, and log a
// LOOP_DROP for diagnostics.
func (l *Loop) submitCoalesced(flag *atomic.Bool, ev Event) {
	if !flag.CompareAndSwap(false, true) {
		return
	}
	select {
	case l.events <- ev:
	default:
		flag.Store(false)
		l.drops.Add(1)
		logEvent("LOOP_DROP kind=%s queue_full drops_total=%d", ev.kind(), l.drops.Load())
	}
}
