package daemon

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sessions in a tmux group share their windows AND their panes. Tabby renders
// its chrome as real panes (the phone bottom nav bar, the window/pane headers,
// the sidebar), so two daemons in one group laying out the same windows with
// different client profiles physically cannot both be right — they take turns
// spawning and killing each other's chrome. Observed live as three daemons
// rewriting the same window set inside 300ms:
//
//	12:51:24.522  $2  activeProfile=phone    sidebarHidden=true
//	12:51:24.579  $1  activeProfile=desktop  sidebarHidden=false
//	12:51:24.587  $8  activeProfile=desktop  sidebarHidden=false
//
// with the write order flipping between rounds — the "layout jumps between
// mobile and desktop" and "the header bar is at the bottom" bugs.
//
// HasElectedClient (coordinator.go) already keeps a *clientless* daemon out of
// shared windows. It cannot help here: every daemon above has a real client.
// Someone has to lose, so exactly one daemon per group owns layout at a time.
//
// The owner is elected, not negotiated: there is no lock file. Every daemon
// reads the same tmux state and applies the same deterministic rule (most
// recently active client; ties broken by client name), so they all
// independently arrive at the same answer. On top of the raw election sits a
// sticky lease (stickyGroupLayoutOwner): ownership only moves after a
// challenger has held most-active for layoutOwnerHandoffDelay, so a stray
// keepalive or focus event on the phone stops flipping the whole group's
// chrome. The lease lives in per-group tmux global options, which every
// daemon — fresh-started ones included — reads identically.

// groupLayoutClient is one attached tmux client considered for group layout
// ownership.
type groupLayoutClient struct {
	name     string
	session  string
	activity int64
}

// layoutOwnerRecheck bounds how often the election re-reads tmux. The election
// costs two fork/execs and sits in front of the hot pane-layout path, which
// runs on every refresh tick.
//
// The window is also why a handoff is not instant: two daemons sampling at
// different instants can both believe they own layout for up to this long
// after the active client changes. That transient is bounded and self-clearing,
// and is strictly better than the unbounded every-daemon-every-round fight it
// replaces.
const layoutOwnerRecheck = time.Second

// layoutOwnerHandoffDelay is how long a challenger must hold most-active
// before ownership actually moves. The election keys on client_activity, and
// activity is cheap: a mosh keepalive, a focus event, or one stray keystroke
// on the phone makes it the most-recent client for a moment. Without
// hysteresis each such blip flipped the owner, and a flip is a full chrome
// reflow (kill-pane + split-window across every shared window) — observed
// live as the owner going $12 -> $13 -> $12 inside 7 seconds while the user
// never left the desktop. Three seconds absorbs the blips; a deliberate
// device switch keeps the challenger ahead for far longer than that.
const layoutOwnerHandoffDelay = 3 * time.Second

// sessionGroups reads which sessions belong to which group, keyed by
// #{session_id}. This is the cheaper half of groupLayoutState: the election
// needs the clients too, but callers that only want to place a session in its
// group pay one fork/exec here instead of two. A var so tests can stub it.
var sessionGroups = func() map[string]string {
	groups := map[string]string{}
	out, err := tmuxOutputCtx("list-sessions", "-F", "#{session_id}|||#{session_group}")
	if err != nil {
		return groups
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|||")
		if len(parts) < 2 {
			continue
		}
		if id := strings.TrimSpace(parts[0]); id != "" {
			groups[id] = strings.TrimSpace(parts[1])
		}
	}
	return groups
}

// groupLayoutState reads, from the live tmux server, which sessions belong to
// which group and every attached client. Both halves have to come from the
// same server read to be consistent, and it is a var so tests can stub it
// rather than interrogating the developer's own tmux session.
var groupLayoutState = func() (groups map[string]string, clients []groupLayoutClient) {
	groups = sessionGroups()
	// The election is filtered to our session group below, so no client outside
	// the group can win, and the usual hazards of reading every client
	// (electing a stranger's geometry, trusting its active window) do not
	// apply here — nothing below reads geometry, only client_activity.
	//
	// #{session_id}, not the client-session format that reports a session
	// NAME ("infras-2"): a name never matches the #{session_id} keys ("$2")
	// that list-sessions reports, so every client would fall outside every
	// group and no daemon would elect itself. In a list-clients format the
	// client's session is the format context, so #{session_id} here is that
	// client's session, which is exactly what the group map is keyed by.
	//
	// server-wide list-clients: the whole point is to see our PEERS' clients.
	// Scoped to our own session, every daemon in the group would find only
	// itself, elect itself, and resume the fight this exists to end.
	if out, err := tmuxOutputCtx("list-clients", "-F", "#{client_name}|||#{session_id}|||#{client_activity}"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "|||")
			if len(parts) < 3 {
				continue
			}
			name := strings.TrimSpace(parts[0])
			sess := strings.TrimSpace(parts[1])
			if name == "" || sess == "" {
				continue
			}
			activity, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
			if err != nil {
				continue
			}
			clients = append(clients, groupLayoutClient{name: name, session: sess, activity: activity})
		}
	}
	return groups, clients
}

// electGroupLayoutOwner returns the session ID that owns chrome layout for
// sessionID's window set, or "" when nobody does.
//
// Pure and deterministic on purpose: every daemon in the group feeds it the
// same tmux snapshot and must reach the same verdict without talking to its
// peers. Never break the tie on anything session-local (our own ID, a
// timestamp, map iteration order) — two daemons would then both elect
// themselves and the fight resumes.
func electGroupLayoutOwner(sessionID string, groups map[string]string, clients []groupLayoutClient) string {
	// An ungrouped session shares its windows with nobody, so it always owns
	// its own layout. This is the single-session case: unchanged behaviour.
	group := groups[sessionID]
	if group == "" {
		return sessionID
	}

	var candidates []groupLayoutClient
	for _, cl := range clients {
		if g, ok := groups[cl.session]; ok && g == group {
			candidates = append(candidates, cl)
		}
	}
	if len(candidates) == 0 {
		// Every session in the group is detached. Nobody is watching, so
		// nobody needs to restructure the shared windows.
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].activity != candidates[j].activity {
			return candidates[i].activity > candidates[j].activity
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0].session
}

// The sticky-ownership layer. electGroupLayoutOwner answers "who is active
// right now"; what layout needs is "who has been active long enough to be
// worth a full chrome reflow". The current owner and the pending challenger
// are stored as per-group tmux global options so every daemon in the group —
// including one that just restarted — reads the same lease and reaches the
// same verdict. Daemon-local memory would lose the challenger's age on every
// rebuild restart and hand ownership over instantly, which is exactly the
// judder this layer exists to stop.
//
// Writes happen only on lease transitions (init, challenger noted/changed,
// handoff), never on a steady-state recheck.

func layoutLeaseOptionName(group string, kind string) string {
	var b strings.Builder
	for _, r := range group {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "@tabby_layout_" + kind + "_" + b.String()
}

// groupLayoutLeaseRead fetches every @tabby_layout_* global option in one
// fork/exec. It is a var so tests can stub the tmux server.
var groupLayoutLeaseRead = func() map[string]string {
	lease := map[string]string{}
	out, err := tmuxOutputCtx("show-options", "-g")
	if err != nil {
		return lease
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "@tabby_layout_") {
			continue
		}
		name, value, _ := strings.Cut(line, " ")
		lease[name] = strings.Trim(value, `"`)
	}
	return lease
}

// groupLayoutLeaseWrite sets (or, for an empty value, unsets) one lease
// option. A var so tests never touch the real tmux server.
var groupLayoutLeaseWrite = func(name, value string) {
	var args []string
	if value == "" {
		args = []string{"set-option", "-gu", name}
	} else {
		args = []string{"set-option", "-g", name, value}
	}
	if _, err := tmuxOutputCtx(args...); err != nil {
		logEvent("GROUP_LAYOUT_LEASE_WRITE_ERR option=%s err=%v", name, err)
	}
}

// stickyGroupLayoutOwner turns the instantaneous election winner into a lease.
// group is the session's group name, claimant the electGroupLayoutOwner
// result, groups/clients the same tmux snapshot the election ran on. now is
// injectable for tests.
func stickyGroupLayoutOwner(group, claimant string, groups map[string]string, clients []groupLayoutClient, now time.Time) string {
	if claimant == "" {
		// Fully detached group: nobody is watching, nobody owns. Leave the
		// stored lease alone — when a client reattaches the incumbent resumes
		// instead of a fresh election reflowing for no reason.
		return ""
	}
	lease := groupLayoutLeaseRead()
	ownerOpt := layoutLeaseOptionName(group, "owner")
	challengerOpt := layoutLeaseOptionName(group, "challenger")
	owner := lease[ownerOpt]

	if owner == "" || owner == claimant {
		if owner == "" {
			groupLayoutLeaseWrite(ownerOpt, claimant)
			logEvent("GROUP_LAYOUT_LEASE_INIT group=%s owner=%q", group, claimant)
		}
		if lease[challengerOpt] != "" {
			groupLayoutLeaseWrite(challengerOpt, "")
		}
		return claimant
	}

	// A challenger is more recently active than the incumbent. Hand off at
	// once when the incumbent cannot be using its layout — its client is
	// gone or the session left the group — because waiting out the delay
	// would leave the group laid out for a device nobody is on.
	incumbentAlive := groups[owner] == group
	if incumbentAlive {
		incumbentAlive = false
		for _, cl := range clients {
			if cl.session == owner {
				incumbentAlive = true
				break
			}
		}
	}
	if incumbentAlive {
		challengerSince := int64(0)
		challengerSession := ""
		if raw := lease[challengerOpt]; raw != "" {
			fmt.Sscanf(raw, "%s %d", &challengerSession, &challengerSince)
		}
		if challengerSession != claimant {
			groupLayoutLeaseWrite(challengerOpt, fmt.Sprintf("%s %d", claimant, now.Unix()))
			logEvent("GROUP_LAYOUT_CHALLENGER group=%s owner=%q challenger=%q", group, owner, claimant)
			return owner
		}
		if now.Unix()-challengerSince < int64(layoutOwnerHandoffDelay/time.Second) {
			return owner
		}
		logEvent("GROUP_LAYOUT_HANDOFF group=%s from=%q to=%q", group, owner, claimant)
	}
	groupLayoutLeaseWrite(ownerOpt, claimant)
	groupLayoutLeaseWrite(challengerOpt, "")
	return claimant
}

// layoutOwnerCache memoizes the election for layoutOwnerRecheck.
type layoutOwnerCache struct {
	mu   sync.Mutex
	owns bool
	// elected is false until the first election, so that a first result of
	// "nobody owns" still gets logged rather than being mistaken for the
	// zero value of owner and silently swallowed.
	elected bool
	owner   string
	checkAt time.Time
}

// OwnsGroupLayout reports whether this daemon is the one that should mutate
// the shared window chrome right now. Anything that spawns, kills, or
// restructures panes across the session's windows must gate on this.
//
// Returns true for an ungrouped session, so the common single-session setup is
// unaffected.
func (c *Coordinator) OwnsGroupLayout() bool {
	now := time.Now()
	c.layoutOwner.mu.Lock()
	if now.Before(c.layoutOwner.checkAt) {
		owns := c.layoutOwner.owns
		c.layoutOwner.mu.Unlock()
		return owns
	}
	groups, clients := groupLayoutState()
	owner := electGroupLayoutOwner(c.sessionID, groups, clients)
	// Ungrouped sessions elect themselves; the sticky layer only arbitrates
	// shared window sets.
	if group := groups[c.sessionID]; group != "" {
		owner = stickyGroupLayoutOwner(group, owner, groups, clients, now)
	}
	owns := owner == c.sessionID
	gained := owns && c.layoutOwner.elected && !c.layoutOwner.owns
	if !c.layoutOwner.elected || owner != c.layoutOwner.owner {
		logEvent("GROUP_LAYOUT_OWNER session=%s owner=%q owns=%v clients=%d",
			c.sessionID, owner, owns, len(clients))
	}
	c.layoutOwner.elected = true
	c.layoutOwner.owner = owner
	c.layoutOwner.owns = owns
	c.layoutOwner.checkAt = now.Add(layoutOwnerRecheck)
	c.layoutOwner.mu.Unlock()

	if gained {
		c.onLayoutOwnershipGained()
	}
	return owns
}

// onLayoutOwnershipGained runs when the lease hands this daemon the group's
// layout mid-run. A profile transition that fired while we were not the owner
// had its chrome step skipped (PROFILE_TRANSITION_CHROME_SKIP), and nothing
// re-fired it when the handoff committed — the phone's bottom bar only
// appeared when some later tick happened to notice. The lease's 3s hold has
// already absorbed the activity blips the 750ms debounce exists to catch, so
// apply the current profile's chrome now, skipping that debounce.
func (c *Coordinator) onLayoutOwnershipGained() {
	profileTransitionMu.Lock()
	if profileTransitionTimer != nil {
		profileTransitionTimer.Stop()
		profileTransitionTimer = nil
		pendingProfile = ""
	}
	profileTransitionMu.Unlock()
	target := c.ActiveClientProfile()
	logEvent("PROFILE_TRANSITION_FAST target=%s reason=ownership_gained", target)
	c.executeProfileTransition(target)

	// Chrome is only half of it: the client_resized / geometry_tick reconcile
	// that would have locked every window to this client's size was skipped
	// while we were not the owner, and both paths commit lastResizeKey before
	// reconciling — so the phone's size is already "handled" and no later tick
	// re-applies it. The group stays laid out at the previous owner's
	// geometry (desktop-sized windows on a phone screen) until something else
	// changes size. Ask the loop for one forced re-lock.
	if c.OnGeometryRelock != nil {
		c.OnGeometryRelock("ownership_gained")
	}
}
