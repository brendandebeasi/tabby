package daemon

// newTabSuppressState budgets the post-creation phantom-click guard.
//
// Right after a tab is created, a synthetic mouse report can land on the
// freshly spawned sidebar's window list and select a window the user never
// clicked -- the "new tab jumps to window 1" bug. The guard dropped every
// window select that did not target the new tab for the whole settle window,
// which stopped the phantom but also stopped the user: open a tab, decide you
// wanted a different window, click it, and nothing happens for three seconds.
// Focus appearing to bounce back to the new tab is that drop, not a race.
//
// The sidebar sends both the phantom and a real click as an "action" input
// carrying the same fields, so there is nothing in the payload to tell them
// apart. What does tell them apart is repetition: the phantom fires once and
// is not repeated, while a user whose click did nothing clicks again. So the
// budget is one drop per target per settle window. The phantom is still
// swallowed; the cost of a real click landing in that window is one extra
// click instead of a three-second freeze.
type newTabSuppressState struct {
	pending    string          // window id of the tab currently settling
	suppressed map[string]bool // targets already dropped for that tab
}

// shouldSuppress reports whether a select of target, arriving while pending is
// still settling, should be dropped. The first select of a given target is
// dropped and a repeat is allowed through. Switching to a different pending
// tab resets the budget, so each new tab gets its own one-shot guard.
func (s *newTabSuppressState) shouldSuppress(pending, target string) bool {
	if s.pending != pending {
		s.pending = pending
		s.suppressed = nil
	}
	if s.suppressed[target] {
		return false
	}
	if s.suppressed == nil {
		s.suppressed = make(map[string]bool)
	}
	s.suppressed[target] = true
	return true
}
