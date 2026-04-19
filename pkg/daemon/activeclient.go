package daemon

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ElectionResult is what ClientElector.Elect returns: the elected active client
// plus the raw tmux activity timestamp (useful for dedup keys outside the
// ActiveClient wire type) and an OK flag.
type ElectionResult struct {
	Client   ActiveClient
	Activity int64
	OK       bool
}

// ClientElector owns the heuristic that decides which attached tmux client
// is "active" — the physical tty currently driving the session. Previously
// this logic lived as a set of package globals plus a free function in
// cmd/tabby-daemon/main.go; consolidating it behind a type makes the pin
// lifetime and sticky-hold state inspectable and testable.
//
// Zero value is unusable; construct with NewClientElector.
type ClientElector struct {
	mu sync.Mutex

	// Pin: a pin is a short-lived override that forces a specific tty to
	// win the election regardless of tmux activity. Used to prevent
	// flip-flop during input bursts (e.g. right after a sidebar click,
	// we want the driving client to keep winning even if another client
	// has more recent activity because of some unrelated hook).
	preferredTTY    string
	preferredTime   time.Time
	preferredReason string
	preferredMaxAge time.Duration

	// Sticky: once we elect a tty, it's recorded here so callers can
	// detect when the elected client changes. Not currently used to
	// influence the election itself — retained so future logic can add
	// minimum-hold semantics without restructuring the API.
	stickyTTY  string
	stickyTime time.Time

	log func(format string, args ...interface{})
}

// NewClientElector builds an elector. `log` is called for CLIENT_* events
// (pin, pin-expired, selection); pass a no-op func to disable logging.
// `preferredMaxAge` bounds how long a pin is honored; 0 means use the
// default (8 seconds).
func NewClientElector(log func(format string, args ...interface{}), preferredMaxAge time.Duration) *ClientElector {
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if preferredMaxAge <= 0 {
		preferredMaxAge = 8 * time.Second
	}
	return &ClientElector{
		log:             log,
		preferredMaxAge: preferredMaxAge,
	}
}

// Pin forces the given tty to be chosen as active for up to preferredMaxAge.
// `reason` is logged and shown in pin-expired log lines — it should identify
// the triggering action (e.g. "input:@1:select_window", "connect:header:%5").
func (e *ClientElector) Pin(tty, reason string) {
	tty = strings.TrimSpace(tty)
	if tty == "" {
		return
	}
	e.mu.Lock()
	e.preferredTTY = tty
	e.preferredTime = time.Now()
	e.preferredReason = reason
	e.mu.Unlock()
	e.log("CLIENT_FOCUS_PIN tty=%s reason=%s", tty, reason)
}

// Elect queries tmux for currently-attached clients, picks the one that's
// most likely to be the user's focus, and returns it along with the raw
// activity timestamp. OK is false if tmux reports no attached clients.
//
// The heuristic:
//  1. Prefer clients with activity in the last 1.5s over idle ones.
//  2. Among the preferred group, take the most recently active.
//  3. Ties broken by the `focused` flag.
//  4. An active pin (set via Pin within preferredMaxAge) overrides the
//     above entirely.
func (e *ClientElector) Elect() ElectionResult {
	const idleWindow = int64(1500)
	now := time.Now().Unix()
	out, err := exec.Command("tmux", "list-clients", "-F",
		"#{client_tty}|||#{client_width}|||#{client_height}|||#{client_flags}|||#{client_activity}").Output()
	if err != nil {
		return ElectionResult{}
	}

	type clientInfo struct {
		tty      string
		width    int
		height   int
		focused  bool
		activity int64
	}

	var attached []clientInfo
	focusedCount := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|||")
		if len(parts) < 5 {
			continue
		}
		w, errW := strconv.Atoi(parts[1])
		h, errH := strconv.Atoi(parts[2])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue
		}
		flags := parts[3]
		activity, _ := strconv.ParseInt(parts[4], 10, 64)
		info := clientInfo{
			tty:      parts[0],
			width:    w,
			height:   h,
			focused:  strings.Contains(flags, "focused"),
			activity: activity,
		}
		if strings.Contains(flags, "attached") {
			attached = append(attached, info)
			if info.focused {
				focusedCount++
			}
		}
	}
	if len(attached) == 0 {
		return ElectionResult{}
	}

	bestIdx := 0
	for i := 1; i < len(attached); i++ {
		c := attached[i]
		deltaBest := now - attached[bestIdx].activity
		deltaCur := now - c.activity
		bestActive := deltaBest <= idleWindow
		curActive := deltaCur <= idleWindow
		if bestActive != curActive {
			if curActive {
				bestIdx = i
			}
			continue
		}
		if bestActive && curActive {
			if c.activity > attached[bestIdx].activity {
				bestIdx = i
			}
			continue
		}
		if c.activity > attached[bestIdx].activity {
			bestIdx = i
			continue
		}
		if c.activity == attached[bestIdx].activity {
			if c.focused && !attached[bestIdx].focused {
				bestIdx = i
			}
		}
	}
	best := attached[bestIdx]

	e.mu.Lock()
	if e.preferredTTY != "" {
		if time.Since(e.preferredTime) > e.preferredMaxAge {
			e.log("CLIENT_FOCUS_PIN_EXPIRED tty=%s age_ms=%d reason=%s",
				e.preferredTTY, time.Since(e.preferredTime).Milliseconds(), e.preferredReason)
			e.preferredTTY = ""
			e.preferredReason = ""
		} else {
			for i, c := range attached {
				if c.tty == e.preferredTTY {
					bestIdx = i
					best = attached[bestIdx]
					break
				}
			}
		}
	}
	if best.tty != e.stickyTTY {
		e.stickyTTY = best.tty
		e.stickyTime = time.Now()
	}
	e.mu.Unlock()

	reason := "activity"
	if now-best.activity > idleWindow {
		reason = "stale_activity"
	}
	if best.focused {
		reason += "+focused"
	}
	e.log("CLIENT_GEOM_SELECT tty=%s size=%dx%d reason=%s activity=%d attached=%d focused=%d",
		best.tty, best.width, best.height, reason, best.activity, len(attached), focusedCount)

	profile := "desktop"
	if best.width > 0 && best.width < 100 {
		profile = "phone"
	}
	return ElectionResult{
		Client: ActiveClient{
			TTY:     best.tty,
			Width:   best.width,
			Height:  best.height,
			Profile: profile,
		},
		Activity: best.activity,
		OK:       true,
	}
}

// LatestAttachedTTY returns the tty of the attached client with the most
// recent activity, without running the full election. Used for pin targets
// when we know we want "whoever just did something" rather than "whoever is
// active right now" — subtly different after heuristic kicks in.
func (e *ClientElector) LatestAttachedTTY() string {
	out, err := exec.Command("tmux", "list-clients", "-F",
		"#{client_tty}|||#{client_flags}|||#{client_activity}").Output()
	if err != nil {
		return ""
	}
	bestTTY := ""
	var bestActivity int64 = -1
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|||")
		if len(parts) < 3 {
			continue
		}
		if !strings.Contains(parts[1], "attached") {
			continue
		}
		activity, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if activity > bestActivity {
			bestActivity = activity
			bestTTY = strings.TrimSpace(parts[0])
		}
	}
	return bestTTY
}
