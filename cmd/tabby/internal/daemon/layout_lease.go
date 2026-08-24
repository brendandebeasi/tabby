package daemon

import (
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
// The owner is elected, not leased: there is no lock file and no negotiation.
// Every daemon reads the same tmux state and applies the same deterministic
// rule (most recently active client; ties broken by client name), so they all
// independently arrive at the same answer. Ownership follows the user — the
// client you are actually typing in wins, and its daemon lays out the group.

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

// groupLayoutState reads, from the live tmux server, which sessions belong to
// which group and every attached client. Both halves have to come from the
// same server read to be consistent, and it is a var so tests can stub it
// rather than interrogating the developer's own tmux session.
var groupLayoutState = func() (groups map[string]string, clients []groupLayoutClient) {
	groups = map[string]string{}
	if out, err := tmuxOutputCtx("list-sessions", "-F", "#{session_id}|||#{session_group}"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "|||")
			if len(parts) < 2 {
				continue
			}
			if id := strings.TrimSpace(parts[0]); id != "" {
				groups[id] = strings.TrimSpace(parts[1])
			}
		}
	}
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
	defer c.layoutOwner.mu.Unlock()
	if now.Before(c.layoutOwner.checkAt) {
		return c.layoutOwner.owns
	}
	groups, clients := groupLayoutState()
	owner := electGroupLayoutOwner(c.sessionID, groups, clients)
	owns := owner == c.sessionID
	if !c.layoutOwner.elected || owner != c.layoutOwner.owner {
		logEvent("GROUP_LAYOUT_OWNER session=%s owner=%q owns=%v clients=%d",
			c.sessionID, owner, owns, len(clients))
	}
	c.layoutOwner.elected = true
	c.layoutOwner.owner = owner
	c.layoutOwner.owns = owns
	c.layoutOwner.checkAt = now.Add(layoutOwnerRecheck)
	return owns
}
