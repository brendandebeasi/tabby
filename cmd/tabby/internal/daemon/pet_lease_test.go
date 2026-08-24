package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/stretchr/testify/assert"
)

// useTempStateDir points pet.owner at a scratch dir so the test never touches
// the developer's real lease — a live daemon is holding it.
func useTempStateDir(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	paths.ResetForTest()
	t.Cleanup(paths.ResetForTest)
	paths.EnsureStateDir()
}

// petOwnerWrites counts how many times the lock file has been rewritten.
func petOwnerWrites(t *testing.T, before int64) int64 {
	t.Helper()
	fi, err := os.Stat(petOwnerPath())
	if err != nil {
		return before
	}
	return fi.ModTime().UnixNano()
}

// The pet lease lives 30s but acquirePetOwnership is called from the 10 Hz
// animation tick, so it used to read AND rewrite the lock file every 100ms —
// 10.7 disk writes/second, measured on a live daemon, from inside the global
// stateMu critical section. A hitch in that write stalls stateMu and with it
// the refresh loop, which is exactly what a frozen pet animation looks like.
func TestAcquirePetOwnership_DoesNotTouchDiskEveryTick(t *testing.T) {
	useTempStateDir(t)

	c := &Coordinator{sessionID: "$1"}
	start := time.Now()

	assert.True(t, c.acquirePetOwnership(start), "first call claims the free lease")
	firstWrite := petOwnerWrites(t, 0)

	// A second of ticks at 10 Hz.
	for i := 1; i <= 10; i++ {
		assert.True(t, c.acquirePetOwnership(start.Add(time.Duration(i)*100*time.Millisecond)),
			"the cached decision holds between renewals")
	}
	assert.Equal(t, firstWrite, petOwnerWrites(t, firstWrite),
		"none of those ticks rewrote the lock file")

	// Past the renew window, the lease is touched again.
	c.acquirePetOwnership(start.Add(petLeaseRenewEvery + time.Second))
	assert.NotEqual(t, firstWrite, petOwnerWrites(t, firstWrite),
		"the lease is still renewed once its window comes round")
}

// Renewing at a third of the TTL has to leave room for retries before the
// lease actually expires, or a transient failure hands the pet to a peer.
func TestPetLeaseRenewsWellBeforeExpiry(t *testing.T) {
	assert.Less(t, petLeaseRenewEvery*2, petOwnershipTTL,
		"at least two renewal attempts fit inside one lease")
}

// A user action (feed/play/click) makes this session the writer immediately.
// If the steal did not publish into the memoized decision, the next tick would
// keep returning the stale cached "not ours" until its renew window came round.
func TestStealPetOwnership_PublishesIntoTheCachedDecision(t *testing.T) {
	useTempStateDir(t)

	c := &Coordinator{sessionID: "$2"}
	c.petLeaseOwner = false
	c.petLeaseCheckAt = time.Now().Add(petLeaseRenewEvery)

	c.stealPetOwnership()

	assert.True(t, c.acquirePetOwnership(time.Now()), "the very next tick sees this session as the writer")
}
