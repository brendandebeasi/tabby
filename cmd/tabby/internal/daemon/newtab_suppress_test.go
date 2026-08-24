package daemon

import "testing"

func TestNewTabSuppressStateOneShotPerTarget(t *testing.T) {
	var s newTabSuppressState

	// The phantom: first select away from the settling tab is dropped.
	if !s.shouldSuppress("@5", "@1") {
		t.Fatal("first select of @1 should be suppressed")
	}
	// The user clicking again means it: let it through.
	if s.shouldSuppress("@5", "@1") {
		t.Fatal("repeat select of @1 should pass through")
	}
	// And stay through, however many times they click.
	if s.shouldSuppress("@5", "@1") {
		t.Fatal("third select of @1 should pass through")
	}
}

func TestNewTabSuppressStateBudgetIsPerTarget(t *testing.T) {
	var s newTabSuppressState

	if !s.shouldSuppress("@5", "@1") {
		t.Fatal("first select of @1 should be suppressed")
	}
	// A different target has its own budget -- the phantom lands on one row,
	// so spending @1's budget must not hand @2 a free pass.
	if !s.shouldSuppress("@5", "@2") {
		t.Fatal("first select of @2 should be suppressed")
	}
	if s.shouldSuppress("@5", "@2") {
		t.Fatal("repeat select of @2 should pass through")
	}
}

func TestNewTabSuppressStateResetsPerPendingTab(t *testing.T) {
	var s newTabSuppressState

	if !s.shouldSuppress("@5", "@1") {
		t.Fatal("first select of @1 under @5 should be suppressed")
	}
	if s.shouldSuppress("@5", "@1") {
		t.Fatal("repeat under @5 should pass through")
	}

	// A new tab starts a new settle window, so @1 is guarded again -- otherwise
	// one earlier repeat would disarm the guard for the rest of the session.
	if !s.shouldSuppress("@9", "@1") {
		t.Fatal("first select of @1 under @9 should be suppressed again")
	}
	if s.shouldSuppress("@9", "@1") {
		t.Fatal("repeat under @9 should pass through")
	}

	// Returning to the earlier pending id is also a fresh window: the id is
	// only ever reused after that tab was created again.
	if !s.shouldSuppress("@5", "@1") {
		t.Fatal("select of @1 back under @5 should be suppressed again")
	}
}
