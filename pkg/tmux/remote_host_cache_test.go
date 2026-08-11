package tmux

import (
	"testing"
	"time"
)

func TestRemoteHostCacheMemoizesByPID(t *testing.T) {
	remoteHostCacheMu.Lock()
	remoteHostCache = map[int]remoteHostEntry{}
	remoteHostCacheMu.Unlock()

	// Seed a value for a PID that cannot exist, so any cache miss would have to
	// shell out and return "" instead of the seeded host.
	const fakePID = 1 << 30
	remoteHostCacheMu.Lock()
	remoteHostCache[fakePID] = remoteHostEntry{host: "cached-host", at: time.Now()}
	remoteHostCacheMu.Unlock()

	if got := RemoteHostForPane(fakePID); got != "cached-host" {
		t.Fatalf("expected cache hit %q, got %q", "cached-host", got)
	}
}

func TestRemoteHostCacheExpires(t *testing.T) {
	remoteHostCacheMu.Lock()
	remoteHostCache = map[int]remoteHostEntry{}
	remoteHostCacheMu.Unlock()

	const fakePID = 1 << 30
	remoteHostCacheMu.Lock()
	remoteHostCache[fakePID] = remoteHostEntry{host: "stale-host", at: time.Now().Add(-2 * remoteHostCacheTTL)}
	remoteHostCacheMu.Unlock()

	// Expired entry must not be served; the PID is bogus so resolution yields "".
	if got := RemoteHostForPane(fakePID); got == "stale-host" {
		t.Fatalf("expired entry was served: %q", got)
	}
}

func TestInvalidateRemoteHostCache(t *testing.T) {
	const fakePID = 1 << 30
	remoteHostCacheMu.Lock()
	remoteHostCache[fakePID] = remoteHostEntry{host: "doomed", at: time.Now()}
	remoteHostCacheMu.Unlock()

	InvalidateRemoteHostCache(fakePID)

	remoteHostCacheMu.Lock()
	_, ok := remoteHostCache[fakePID]
	remoteHostCacheMu.Unlock()
	if ok {
		t.Fatal("entry survived invalidation")
	}
}
