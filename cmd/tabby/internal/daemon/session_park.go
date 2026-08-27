package daemon

import (
	"strconv"
	"strings"
	"time"
)

// Only one client can own geometry at a time. tmux clamps a window's size to
// the smallest session whose CURRENT window it is, and a session keeps
// pointing at its last window even with no client attached (a sleeping
// phone's mosh session, a detached terminal). That stale pointer pins a
// shared window at the idle client's size for every other client — the
// "window thinks it's mobile" state. On an elected-client or active-window
// change the layout owner runs EnforceSingleActiveClient: any non-elected
// grouped session pointing at the active window is parked — its current
// window moved aside and recorded — and a session whose client comes back
// without having navigated is restored to the window it was parked from.

const defaultParkIdleSeconds = 15

func parkIdleSeconds() int {
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_park_idle_seconds")); err == nil && v >= 0 {
		return v
	}
	return defaultParkIdleSeconds
}

func parkedOptionName(sessionID string) string {
	var b strings.Builder
	for _, r := range sessionID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return "@tabby_parked_" + b.String()
}

func windowAlive(windowID string) bool {
	out, err := tmuxOutputCtx("display-message", "-p", "-t", windowID, "#{window_id}")
	return err == nil && strings.TrimSpace(string(out)) == windowID
}

type parkSessionInfo struct {
	id, name, currentWindow string
	parkedFrom, parkedTo    string
	eligible                bool // no client, or every client idle past the threshold
	clientTTY               string
}

// EnforceSingleActiveClient restores the elected session if it was parked,
// then parks any non-elected grouped session contesting the active window.
// Runs in a goroutine from election/window-change triggers; owner-gated.
func (c *Coordinator) EnforceSingleActiveClient(trigger string) {
	if !c.OwnsGroupLayout() {
		return
	}
	c.parkMu.Lock()
	defer c.parkMu.Unlock()

	ac := c.ActiveClientSnapshot()
	if ac.TTY == "" {
		return
	}
	out, err := tmuxOutputCtx("display-message", "-p", "-t", ac.TTY, "#{session_id}|#{client_session}|#{session_group}|#{window_id}")
	if err != nil {
		return
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 4)
	if len(parts) != 4 {
		return
	}
	activeSID, activeSess, activeGroup, activeWin := parts[0], parts[1], parts[2], parts[3]
	if activeWin == "" {
		return
	}

	idleLimit := parkIdleSeconds()
	sessions := listParkCandidateSessions(activeGroup, activeSID, idleLimit)

	// Restore the elected session first: if it was parked and hasn't
	// navigated since, put it back on the window it was taken from. The
	// active window for the parking pass below is read AFTER this.
	if marker := strings.TrimSpace(tmuxGlobalOption(parkedOptionName(activeSID))); marker != "" {
		from, to, _ := strings.Cut(marker, ":")
		if from != "" && to != "" && from != to {
			if cur := sessionCurrentWindow(activeSess); cur == to && windowAlive(from) {
				if err := tmuxCmd("select-window", "-t", activeSess+":"+from).Run(); err == nil {
					logEvent("PARK_RESTORE session=%s from=%s to=%s trigger=%s", activeSess, to, from, trigger)
					activeWin = from
				}
			}
		}
		setTmuxGlobalOption(parkedOptionName(activeSID), "")
	}

	for _, s := range sessions {
		if !s.eligible || s.currentWindow != activeWin {
			continue
		}
		// Re-read the session's pointer before moving it: the active window
		// was sampled at the top of this pass, and a nav landing mid-pass
		// makes a stale contest park a session off a window nobody contests.
		if cur := sessionCurrentWindow(s.name); cur != activeWin {
			logEvent("PARK_SKIP session=%s reason=stale_contest sampled=%s current=%s trigger=%s", s.name, activeWin, cur, trigger)
			continue
		}
		// Never park onto a window another session in the group points at —
		// that just moves the clamp (and the visible reflow) elsewhere.
		taken := map[string]bool{activeWin: true}
		for _, other := range sessions {
			if other.currentWindow != "" {
				taken[other.currentWindow] = true
			}
		}
		dest := c.parkDestinationLocked(s, activeWin, taken)
		if dest == "" {
			logEvent("PARK_SKIP session=%s reason=no_destination win=%s", s.name, activeWin)
			continue
		}
		// Chained parks keep the ORIGINAL window as the restore target:
		// a session evicted from @120 onto @115 and then evicted again
		// restores to @120, not @115.
		from := activeWin
		if s.parkedFrom != "" && windowAlive(s.parkedFrom) {
			from = s.parkedFrom
		}
		if err := tmuxCmd("select-window", "-t", s.name+":"+dest).Run(); err != nil {
			logEvent("PARK_ERR session=%s dest=%s err=%v", s.name, dest, err)
			continue
		}
		setTmuxGlobalOption(parkedOptionName(s.id), from+":"+dest)
		logEvent("PARK_SESSION session=%s win=%s dest=%s restore_to=%s trigger=%s", s.name, activeWin, dest, from, trigger)
	}
}

// listParkCandidateSessions returns the sessions sharing the active session's
// group, excluding the active session itself, with eligibility decided: a
// clientless session is always eligible; an attached one only when every
// client has been idle past idleLimit seconds.
func listParkCandidateSessions(activeGroup, activeSID string, idleLimit int) []parkSessionInfo {
	out, err := tmuxOutputCtx("list-sessions", "-F", "#{session_id}|#{session_name}|#{session_group}|#{session_attached}")
	if err != nil {
		return nil
	}
	var sessions []parkSessionInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 || parts[0] == activeSID || parts[2] != activeGroup {
			continue
		}
		s := parkSessionInfo{id: parts[0], name: parts[1]}
		s.currentWindow = sessionCurrentWindow(s.name)
		if marker := strings.TrimSpace(tmuxGlobalOption(parkedOptionName(s.id))); marker != "" {
			s.parkedFrom, s.parkedTo, _ = strings.Cut(marker, ":")
		}
		if parts[3] == "0" {
			s.eligible = true
		} else {
			s.eligible, s.clientTTY = sessionClientsIdle(s.name, idleLimit)
		}
		sessions = append(sessions, s)
	}
	return sessions
}

func sessionCurrentWindow(sessionName string) string {
	out, err := tmuxOutputCtx("display-message", "-p", "-t", sessionName, "#{window_id}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sessionClientsIdle(sessionName string, idleLimit int) (bool, string) {
	out, err := tmuxOutputCtx("list-clients", "-t", sessionName, "-F", "#{client_tty}|#{client_activity}")
	if err != nil {
		return false, ""
	}
	now := time.Now().Unix()
	tty := ""
	eligible := true
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		if tty == "" {
			tty = parts[0]
		}
		activity, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			eligible = false
			continue
		}
		if now-activity < int64(idleLimit) {
			eligible = false
		}
	}
	return eligible, tty
}

// parkDestinationLocked picks where a parked session should point instead.
// The destination must not be "taken" (pointed at by any session in the
// group) — parking onto someone's current window just relocates the size
// clamp and the reflow judder that comes with it. Preference order:
// least-recently-used alive window first (the window the user is least
// likely to visit next), then any untaken alive window, then the old
// anything-alive behavior for window-starved cases.
func (c *Coordinator) parkDestinationLocked(s parkSessionInfo, contested string, taken map[string]bool) string {
	c.stateMu.RLock()
	alive := make(map[string]bool, len(c.windows))
	ids := make([]string, 0, len(c.windows))
	for _, w := range c.windows {
		alive[w.ID] = true
		ids = append(ids, w.ID)
	}
	history := append([]string(nil), c.windowHistory...)
	var clientHistory []string
	if s.clientTTY != "" {
		clientHistory = append(clientHistory, c.clientWindowHistory[s.clientTTY]...)
	}
	c.stateMu.RUnlock()

	free := func(id string) bool {
		return id != "" && alive[id] && !taken[id]
	}

	// LRU pass: windowHistory is MRU-first, so walk it backwards, oldest
	// first; windows never visited come last, in list order.
	seen := map[string]bool{contested: true}
	for i := len(history) - 1; i >= 0; i-- {
		id := history[i]
		if seen[id] {
			continue
		}
		seen[id] = true
		if free(id) {
			return id
		}
	}
	for _, id := range ids {
		if !seen[id] && free(id) {
			return id
		}
		seen[id] = true
	}

	// Starved fallback: everything alive is taken — keep the session on any
	// window other than the contested one (previous behavior).
	mru := append(clientHistory, history...)
	for id := range alive {
		mru = append(mru, id)
	}
	for _, id := range mru {
		if id == "" || id == contested {
			continue
		}
		if alive[id] {
			return id
		}
	}
	return ""
}
