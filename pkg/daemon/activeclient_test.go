package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestReportMultiFocus verifies the multi-focused-client anomaly detector:
// it logs when >1 client carries tmux's `focused` flag, marks the elected
// (genuine) client vs stale phantoms, rate-limits an unchanged focused set,
// and re-logs when the set changes.
func TestReportMultiFocus(t *testing.T) {
	var logs []string
	e := NewClientElector(func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}, 0)

	now := int64(1000)
	// Two focused clients: ttys013 is fresh (elected), ttys002 idle 900s (phantom).
	focused := []focusedClientRef{
		{tty: "/dev/ttys013", activity: now}, // elected, idle 0s
		{tty: "/dev/ttys002", activity: now - 900},
	}

	e.reportMultiFocus(focused, "/dev/ttys013", now)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log on first multi-focus, got %d: %v", len(logs), logs)
	}
	got := logs[0]
	for _, want := range []string{
		"MULTI_FOCUS_DETECTED", "count=2", "elected=/dev/ttys013",
		"/dev/ttys013(idle=0s,elected)", "/dev/ttys002(idle=900s,stale)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q; got: %s", want, got)
		}
	}

	// Same focused set again immediately -> rate-limited, no new log.
	e.reportMultiFocus(focused, "/dev/ttys013", now+1)
	if len(logs) != 1 {
		t.Fatalf("expected rate-limit (still 1 log), got %d: %v", len(logs), logs)
	}

	// A changed focused set -> re-logs immediately (order-independent key).
	changed := []focusedClientRef{
		{tty: "/dev/ttys013", activity: now},
		{tty: "/dev/ttys009", activity: now - 10},
	}
	e.reportMultiFocus(changed, "/dev/ttys013", now+2)
	if len(logs) != 2 {
		t.Fatalf("expected re-log on changed set (2 logs), got %d: %v", len(logs), logs)
	}

	// Same set as the change, but after the re-log interval -> re-logs.
	e.mu.Lock()
	e.lastMultiFocusTime = time.Now().Add(-multiFocusReLogInterval - time.Second)
	e.mu.Unlock()
	e.reportMultiFocus(changed, "/dev/ttys013", now+3)
	if len(logs) != 3 {
		t.Fatalf("expected re-log after interval (3 logs), got %d: %v", len(logs), logs)
	}

	// Single focused client is not an anomaly -> no log.
	e.reportMultiFocus(focused[:1], "/dev/ttys013", now+4)
	if len(logs) != 3 {
		t.Fatalf("single focused client should not log; got %d: %v", len(logs), logs)
	}
}
