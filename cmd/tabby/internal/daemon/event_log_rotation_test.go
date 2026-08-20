package daemon

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestEventLogRotationUnderConcurrentWrites drives logEvent from several
// goroutines with thresholds small enough that rotation fires repeatedly
// mid-run. Under -race this catches unsynchronized access to the eventLog /
// eventLogFile globals that rotation swaps out from under concurrent writers.
func TestEventLogRotationUnderConcurrentWrites(t *testing.T) {
	dir := t.TempDir()

	prevPath, prevFile, prevLog := eventLogPath, eventLogFile, eventLog
	prevMax, prevEvery := eventLogMaxBytes, eventLogCheckEvery
	t.Cleanup(func() {
		if eventLogFile != nil && eventLogFile != prevFile {
			eventLogFile.Close()
		}
		eventLogPath, eventLogFile, eventLog = prevPath, prevFile, prevLog
		eventLogMaxBytes, eventLogCheckEvery = prevMax, prevEvery
	})

	eventLogPath = filepath.Join(dir, "events.log")
	f, err := os.OpenFile(eventLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	eventLogFile = f
	eventLog = log.New(f, "[event] ", log.LstdFlags|log.Lmicroseconds)
	eventLogMaxBytes = 4 * 1024
	eventLogCheckEvery = 4

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				logEvent("ROTATION_SPAM writer=%d seq=%d payload=%s", id, j, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
			}
		}(i)
	}
	wg.Wait()

	// The live log must be bounded: rotation has to reopen the path, not keep
	// writing into the renamed inode.
	info, err := os.Stat(eventLogPath)
	if err != nil {
		t.Fatalf("stat live log: %v", err)
	}
	if info.Size() > 8*eventLogMaxBytes {
		t.Errorf("live log unbounded: %d bytes (max %d)", info.Size(), eventLogMaxBytes)
	}
}

// TestRecentEventLogsPrefersFreshest guards the crash-forensics source pick.
// A .prev left behind by an old size rotation used to win unconditionally, so a
// daemon that crash-looped quoted the same days-old events on every restart and
// the report never showed what preceded the crash.
func TestRecentEventLogsPrefersFreshest(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "events.log")
	prev := live + ".prev"

	write := func(path string, modAgo time.Duration) {
		if err := os.WriteFile(path, []byte("x\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		ts := time.Now().Add(-modAgo)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	// Stale .prev, live log written just now: the live log must come first.
	write(prev, 72*time.Hour)
	write(live, 0)
	if got := recentEventLogs(live); got[0] != live {
		t.Fatalf("stale prev won: got %v, want %s first", got, live)
	}

	// Genuinely just rotated: .prev holds the dead daemon's events.
	write(live, 0)
	write(prev, 0)
	ts := time.Now().Add(time.Second)
	if err := os.Chtimes(prev, ts, ts); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if got := recentEventLogs(live); got[0] != prev {
		t.Fatalf("fresh prev lost: got %v, want %s first", got, prev)
	}

	// Missing .prev must not displace the live log.
	os.Remove(prev)
	if got := recentEventLogs(live); got[0] != live {
		t.Fatalf("missing prev won: got %v, want %s first", got, live)
	}
}
