package daemon

import (
	"testing"

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

func TestOwnsGroupLayout_CachesTheElection(t *testing.T) {
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
