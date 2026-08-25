package daemon

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The election has to be reached independently by every daemon in the group
// from the same tmux snapshot, so these tests assert the verdict from each
// member's point of view, not just the winner's.

func TestElectGroupLayoutOwner_UngroupedSessionAlwaysOwnsItself(t *testing.T) {
	groups := map[string]string{"$1": ""}
	// A client on some unrelated session must not matter.
	clients := []groupLayoutClient{{name: "/dev/ttys9", session: "$9", activity: 999}}

	assert.Equal(t, "$1", electGroupLayoutOwner("$1", groups, clients),
		"the single-session setup must be unaffected by group arbitration")
}

func TestElectGroupLayoutOwner_UngroupedOwnsEvenWithNoClients(t *testing.T) {
	groups := map[string]string{"$1": ""}

	assert.Equal(t, "$1", electGroupLayoutOwner("$1", groups, nil),
		"an ungrouped session shares panes with nobody, so nobody can be fighting it")
}

func TestElectGroupLayoutOwner_MostRecentlyActiveClientWins(t *testing.T) {
	groups := map[string]string{"$1": "infras", "$2": "infras", "$8": "infras"}
	clients := []groupLayoutClient{
		{name: "/dev/ttys001", session: "$2", activity: 100},
		{name: "/dev/ttys054", session: "$10", activity: 300}, // not in the group
		{name: "/dev/ttys051", session: "$8", activity: 200},
	}

	assert.Equal(t, "$8", electGroupLayoutOwner("$1", groups, clients),
		"$10 is outside the group and must not be able to win it")
}

// The crux of the design: no lock file, no negotiation. Every daemon must
// independently reach the SAME verdict, otherwise two elect themselves and the
// layout fight resumes.
func TestElectGroupLayoutOwner_EveryMemberAgreesOnOneOwner(t *testing.T) {
	groups := map[string]string{"$1": "infras", "$2": "infras", "$8": "infras"}
	clients := []groupLayoutClient{
		{name: "/dev/ttys001", session: "$2", activity: 100},
		{name: "/dev/ttys051", session: "$8", activity: 200},
	}

	for _, member := range []string{"$1", "$2", "$8"} {
		assert.Equal(t, "$8", electGroupLayoutOwner(member, groups, clients),
			"daemon %s disagreed about the owner", member)
	}
}

// Equal activity must not resolve by anything session-local (our own ID, map
// order), or each daemon picks itself.
func TestElectGroupLayoutOwner_TieBreaksDeterministicallyNotLocally(t *testing.T) {
	groups := map[string]string{"$1": "infras", "$2": "infras"}
	clients := []groupLayoutClient{
		{name: "/dev/ttys051", session: "$2", activity: 500},
		{name: "/dev/ttys001", session: "$1", activity: 500},
	}

	// Lowest client name wins the tie: /dev/ttys001 on $1.
	for _, member := range []string{"$1", "$2"} {
		assert.Equal(t, "$1", electGroupLayoutOwner(member, groups, clients),
			"daemon %s broke the tie in its own favour", member)
	}
}

func TestElectGroupLayoutOwner_FullyDetachedGroupHasNoOwner(t *testing.T) {
	groups := map[string]string{"$1": "infras", "$2": "infras"}

	assert.Equal(t, "", electGroupLayoutOwner("$1", groups, nil),
		"nobody is watching the shared windows, so nobody should restructure them")
}

// The live bug: a phone client and a desktop client on grouped sessions. Both
// daemons have a real client, so HasElectedClient passes for both and cannot
// break the tie — exactly one must still lose.
func TestElectGroupLayoutOwner_PhoneAndDesktopPeerYieldToOne(t *testing.T) {
	groups := map[string]string{"$1": "infras", "$2": "infras"}
	phone := groupLayoutClient{name: "/dev/ttys001", session: "$2", activity: 900}
	desktop := groupLayoutClient{name: "/dev/ttys054", session: "$1", activity: 800}

	assert.Equal(t, "$2", electGroupLayoutOwner("$1", groups, []groupLayoutClient{phone, desktop}),
		"the phone was active most recently, so the desktop daemon must stand down")

	// The user types on the desktop; ownership follows them.
	desktop.activity = 1000
	assert.Equal(t, "$1", electGroupLayoutOwner("$1", groups, []groupLayoutClient{phone, desktop}))
}

// Regression: the first cut asked tmux for #{client_session}, which is the
// session NAME ("infras-2"), while list-sessions keys the group map by
// #{session_id} ("$2"). Nothing matched, every daemon elected nobody, and all
// chrome layout stopped group-wide. The mismatch is invisible in the election
// logic itself, so guard the format string that feeds it.
func TestGroupLayoutStateAsksTmuxForSessionIDNotName(t *testing.T) {
	src, err := os.ReadFile("layout_lease.go")
	if err != nil {
		t.Fatalf("read layout_lease.go: %v", err)
	}
	body := string(src)

	assert.Contains(t, body, "#{session_id}|||#{client_activity}",
		"the client read must key sessions by id to match the group map")
	assert.NotContains(t, body, "#{client_session}",
		"#{client_session} is the session name and will not match #{session_id} keys")
}

func TestOwnsGroupLayout_CachesTheElection(t *testing.T) {
	stubGroupLayoutLease(t, nil)
	calls := 0
	orig := groupLayoutState
	groupLayoutState = func() (map[string]string, []groupLayoutClient) {
		calls++
		return map[string]string{"$1": "infras", "$2": "infras"},
			[]groupLayoutClient{{name: "/dev/ttys001", session: "$1", activity: 10}}
	}
	defer func() { groupLayoutState = orig }()

	c := NewCoordinator("$1")
	assert.True(t, c.OwnsGroupLayout())
	for i := 0; i < 20; i++ {
		assert.True(t, c.OwnsGroupLayout())
	}
	assert.Equal(t, 1, calls,
		"the election sits in front of the hot layout path and must not shell out per tick")
}

func TestOwnsGroupLayout_FalseWhenAPeerIsMoreRecentlyActive(t *testing.T) {
	stubGroupLayoutLease(t, nil)
	orig := groupLayoutState
	groupLayoutState = func() (map[string]string, []groupLayoutClient) {
		return map[string]string{"$1": "infras", "$2": "infras"},
			[]groupLayoutClient{
				{name: "/dev/ttys001", session: "$2", activity: 999},
				{name: "/dev/ttys054", session: "$1", activity: 1},
			}
	}
	defer func() { groupLayoutState = orig }()

	assert.False(t, NewCoordinator("$1").OwnsGroupLayout())
	assert.True(t, NewCoordinator("$2").OwnsGroupLayout())
}

// stubGroupLayoutLease replaces the lease-option read/write with an in-memory
// map for the duration of one test, so the sticky layer never touches the
// developer's real tmux server. Returns the map so a test can seed or inspect
// the lease.
func stubGroupLayoutLease(t *testing.T, initial map[string]string) map[string]string {
	t.Helper()
	if initial == nil {
		initial = map[string]string{}
	}
	origRead := groupLayoutLeaseRead
	origWrite := groupLayoutLeaseWrite
	groupLayoutLeaseRead = func() map[string]string {
		snap := map[string]string{}
		for k, v := range initial {
			snap[k] = v
		}
		return snap
	}
	groupLayoutLeaseWrite = func(name, value string) {
		if value == "" {
			delete(initial, name)
		} else {
			initial[name] = value
		}
	}
	t.Cleanup(func() {
		groupLayoutLeaseRead = origRead
		groupLayoutLeaseWrite = origWrite
	})
	return initial
}

func leaseGroup() map[string]string {
	return map[string]string{"$12": "infras", "$13": "infras"}
}

func leaseClients() []groupLayoutClient {
	return []groupLayoutClient{
		{name: "/dev/ttys001", session: "$12", activity: 100},
		{name: "/dev/ttys002", session: "$13", activity: 200},
	}
}

// The first election in a group has no incumbent to protect, so the claimant
// takes ownership immediately — hysteresis must not add delay where there is
// nothing to judder against.
func TestStickyGroupLayoutOwner_FirstElectionIsImmediate(t *testing.T) {
	lease := stubGroupLayoutLease(t, nil)

	owner := stickyGroupLayoutOwner("infras", "$13", leaseGroup(), leaseClients(), time.Now())

	assert.Equal(t, "$13", owner)
	assert.Equal(t, "$13", lease["@tabby_layout_owner_infras"])
}

// The live judder: a stray bit of phone activity (mosh keepalive, focus
// event) made it the most-recent client for a moment and the owner flipped
// $12 -> $13 -> $12 inside 7 seconds, reflowing the whole group each way. A
// challenger must not take the lease the instant it wins an election.
func TestStickyGroupLayoutOwner_ChallengerBlipDoesNotFlip(t *testing.T) {
	now := time.Now()
	stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras": "$12",
	})

	owner := stickyGroupLayoutOwner("infras", "$13", leaseGroup(), leaseClients(), now)

	assert.Equal(t, "$12", owner, "a challenger must hold most-active before the chrome reflows for it")
}

func TestStickyGroupLayoutOwner_ChallengerHeldPastDelayTakesOver(t *testing.T) {
	now := time.Now()
	lease := stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras":      "$12",
		"@tabby_layout_challenger_infras": fmt.Sprintf("$13 %d", now.Add(-layoutOwnerHandoffDelay-time.Second).Unix()),
	})

	owner := stickyGroupLayoutOwner("infras", "$13", leaseGroup(), leaseClients(), now)

	assert.Equal(t, "$13", owner)
	assert.Equal(t, "$13", lease["@tabby_layout_owner_infras"])
	assert.Empty(t, lease["@tabby_layout_challenger_infras"], "the challenger record is spent once it wins")
}

// The challenger clock restarts when the most-active session changes again —
// two devices alternating activity must never accumulate toward a handoff.
func TestStickyGroupLayoutOwner_ChallengerClockRestartsPerChallenger(t *testing.T) {
	now := time.Now()
	lease := stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras":      "$12",
		"@tabby_layout_challenger_infras": fmt.Sprintf("$14 %d", now.Add(-time.Hour).Unix()),
	})
	groups := map[string]string{"$12": "infras", "$13": "infras", "$14": "infras"}
	clients := []groupLayoutClient{
		{name: "/dev/ttys001", session: "$12", activity: 100},
		{name: "/dev/ttys002", session: "$13", activity: 200},
		{name: "/dev/ttys003", session: "$14", activity: 150},
	}

	owner := stickyGroupLayoutOwner("infras", "$13", groups, clients, now)

	assert.Equal(t, "$12", owner, "$13 only just became the challenger; $14's stale clock must not hand it the win")
	assert.Contains(t, lease["@tabby_layout_challenger_infras"], "$13")
}

// When the incumbent's client is gone, nobody is using its layout — holding
// the lease for the delay would leave the group drawn for a detached device.
func TestStickyGroupLayoutOwner_DetachedIncumbentHandsOffImmediately(t *testing.T) {
	now := time.Now()
	stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras": "$12",
	})
	clients := []groupLayoutClient{
		{name: "/dev/ttys002", session: "$13", activity: 200}, // $12 detached
	}

	owner := stickyGroupLayoutOwner("infras", "$13", leaseGroup(), clients, now)

	assert.Equal(t, "$13", owner)
}

// An incumbent that regains most-active clears any pending challenger.
func TestStickyGroupLayoutOwner_IncumbentRecoveryClearsChallenger(t *testing.T) {
	now := time.Now()
	lease := stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras":      "$12",
		"@tabby_layout_challenger_infras": fmt.Sprintf("$13 %d", now.Unix()),
	})

	owner := stickyGroupLayoutOwner("infras", "$12", leaseGroup(), leaseClients(), now)

	assert.Equal(t, "$12", owner)
	assert.Empty(t, lease["@tabby_layout_challenger_infras"])
}

// A fully detached group owns nothing and must not disturb the stored lease —
// when a client reattaches, the incumbent resumes instead of a fresh election
// reflowing the chrome for no reason.
func TestStickyGroupLayoutOwner_FullyDetachedKeepsLease(t *testing.T) {
	lease := stubGroupLayoutLease(t, map[string]string{
		"@tabby_layout_owner_infras": "$12",
	})

	owner := stickyGroupLayoutOwner("infras", "", leaseGroup(), nil, time.Now())

	assert.Equal(t, "", owner)
	assert.Equal(t, "$12", lease["@tabby_layout_owner_infras"])
}
