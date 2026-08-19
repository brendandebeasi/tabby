package sidebar

import "testing"

// A renderer whose daemon is gone must keep trying to recover, then give
// up in bounded time rather than reconnecting to a dead socket forever.
func TestDaemonRestartRetrySchedule(t *testing.T) {
	// Attempt n waits this many ~1s disconnect ticks before firing.
	want := []int{5, 10, 20, 40, 60, 60, 60, 60, 60, 60}
	total := 0
	for n, w := range want {
		if got := daemonRestartRetryAfter(n); got != w {
			t.Errorf("daemonRestartRetryAfter(%d) = %d, want %d", n, got, w)
		}
		total += w
	}
	if len(want) != daemonRestartMaxAttempts {
		t.Fatalf("schedule covers %d attempts, max is %d", len(want), daemonRestartMaxAttempts)
	}
	// Total time to giving up should be minutes, not days: the bug this
	// guards against ran for 11 days.
	if total < 60 || total > 15*60 {
		t.Errorf("time to give up = %ds, want between 60s and 900s", total)
	}
	t.Logf("gives up after ~%ds (%d attempts)", total, daemonRestartMaxAttempts)
}

// The backoff must never regress to zero or negative, which would spin
// ensure-sidebar in a tight loop.
func TestDaemonRestartRetryAlwaysPositive(t *testing.T) {
	for n := 0; n < 64; n++ {
		if got := daemonRestartRetryAfter(n); got <= 0 {
			t.Fatalf("daemonRestartRetryAfter(%d) = %d, want > 0", n, got)
		}
	}
}
