package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	kemoji "github.com/kenshaw/emoji"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/navtrace"
	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/teamclaude"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// coordinatorDebugLog is the logger for coordinator debug output
var coordinatorDebugLog *log.Logger

// globalCoordinator is set once in main() so standalone functions can access
// profile-aware settings like desiredWindowHeaderHeight.
var globalCoordinator *Coordinator

// Deadlock detection
var (
	lastHeartbeat     int64 // Unix nano timestamp of last main loop tick
	heartbeatMu       sync.Mutex
	deadlockWatchdog  bool
	deadlockThreshold = 5 * time.Second // Alert if no heartbeat for this long
)

// CWDColorMapping holds the per-directory state that Tabby remembers and
// re-applies to windows whose first pane is in that exact directory: the user's
// chosen color/icon, plus group and pinned state.
//
// NOTE: tab NAMES are deliberately NOT persisted here. Auto names are per-window
// live summaries (set on @tabby_ai_title, recomputed each session) and a manual
// rename lives only on the window's @tabby_name_locked option for the window's
// lifetime. Persisting names keyed by directory caused stale, unrelated names to
// be resurrected onto freshly-opened tabs; the legacy `name`/`nameSource` fields
// are stripped from cwd-colors.json on load by migrateCWDColorsDropNames.
//
// Persisted to cwd-colors.json; all fields use omitempty for back-compat.
type CWDColorMapping struct {
	Color  string `json:"color,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Group  string `json:"group,omitempty"`  // saved @tabby_group
	Pinned bool   `json:"pinned,omitempty"` // saved @tabby_pinned
}

func init() {
	// Default to discard (no logging)
	coordinatorDebugLog = log.New(io.Discard, "", 0)
}

// SetCoordinatorDebugLog sets the debug logger for the coordinator
func SetCoordinatorDebugLog(logger *log.Logger) {
	coordinatorDebugLog = logger
}

// tmuxCmdTimeout is the default timeout for bare tmux commands in the coordinator.
// This prevents indefinite hangs during macOS sleep/wake or tmux server stalls.
const tmuxCmdTimeout = 5 * time.Second

// tmuxRun executes a tmux command with a timeout. Fire-and-forget.
func tmuxRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", args...).Run()
}

// tmuxOutputCtx executes a tmux command with a timeout and returns stdout.
func tmuxOutputCtx(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("tmux %s: timed out after %v", args[0], tmuxCmdTimeout)
	}
	return out, err
}

func tmuxOutputTrimmed(args ...string) string {
	out, err := tmuxOutputCtx(args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func clientTTYForWindow(windowID string) string {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return ""
	}
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}|||#{window_id}|||#{client_activity}").Output()
	if err != nil {
		return ""
	}
	bestTTY := ""
	bestActivity := int64(-1)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|||")
		if len(parts) < 2 {
			continue
		}
		tty := strings.TrimSpace(parts[0])
		win := strings.TrimSpace(parts[1])
		if tty == "" || win != windowID {
			continue
		}
		activity := int64(0)
		if len(parts) >= 3 {
			if v, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil {
				activity = v
			}
		}
		if activity > bestActivity {
			bestActivity = activity
			bestTTY = tty
		}
	}
	return bestTTY
}

// attachedClientWindows returns the set of window IDs that an attached tmux
// client is currently looking at. A window can be its session's "active" window
// while the session is fully detached (nobody is actually watching), so this is
// the real "the user can see it" signal — used to acknowledge AI input
// indicators only once they've genuinely been seen.
func attachedClientWindows() map[string]bool {
	set := map[string]bool{}
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}|||#{window_id}").Output()
	if err != nil {
		return set
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|||")
		if len(parts) < 2 {
			continue
		}
		tty := strings.TrimSpace(parts[0])
		win := strings.TrimSpace(parts[1])
		if tty != "" && win != "" {
			set[win] = true
		}
	}
	return set
}

// tmuxOptionCacheEntry caches a `tmux show-option -gqv` result. These
// @tabby_* settings change only when the user edits config (rare), but
// RunWidthSync re-reads them on every tick — burning 5+ fork/execs per
// call that compounded into ~300ms blocking the event loop. A 30s TTL
// is safely longer than any reasonable runtime change frequency.
type tmuxOptionCacheEntry struct {
	value string
	at    time.Time
}

var tmuxOptionCache sync.Map // key: option name, value: tmuxOptionCacheEntry

const tmuxOptionCacheTTL = 30 * time.Second

// tmuxGlobalOption reads a global tmux option through the cache. Empty
// return value covers both the unset and error cases (matches the
// behavior of -gqv). Callers that branch on emptiness keep the same
// semantics they had with a direct exec.Command call.
func tmuxGlobalOption(name string) string {
	if v, ok := tmuxOptionCache.Load(name); ok {
		e := v.(tmuxOptionCacheEntry)
		if time.Since(e.at) < tmuxOptionCacheTTL {
			return e.value
		}
	}
	out, err := exec.Command("tmux", "show-option", "-gqv", name).Output()
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(out))
	tmuxOptionCache.Store(name, tmuxOptionCacheEntry{value: val, at: time.Now()})
	return val
}

// setTmuxGlobalOption writes a global tmux option AND refreshes the
// tmuxGlobalOption cache so a follow-up read in the same reconcile cycle
// observes the just-written value. Without the cache update, the 30s TTL
// causes drag adoption to snap back: persistSidebarWidthProfile writes
// @tabby_sidebar_width_<profile>=<dragged>, sidebarReasonableMaxForWindow
// reads the stale cached value, computes target=<old>, and the same width
// sync that just adopted reverts the sidebar in the same batched tmux
// command.
func setTmuxGlobalOption(name, value string) error {
	err := tmuxRun("set-option", "-gq", name, value)
	if err == nil {
		tmuxOptionCache.Store(name, tmuxOptionCacheEntry{value: value, at: time.Now()})
	}
	return err
}

// ttyWidthCacheEntry tracks a recent #{client_width} observation for a
// single TTY. The geometry tick refreshes width state at ~250ms cadence,
// so a 500ms TTL on this cache means we serve the nav-suppression check
// from memory without ever being more than one missed tick stale.
type ttyWidthCacheEntry struct {
	width int
	at    time.Time
}

var ttyWidthCache sync.Map // key: tty string, value: ttyWidthCacheEntry

const ttyWidthCacheTTL = 500 * time.Millisecond

func (c *Coordinator) getTTYWidth(tty string) int {
	tty = strings.TrimSpace(tty)
	if tty == "" {
		return 0
	}
	if v, ok := ttyWidthCache.Load(tty); ok {
		e := v.(ttyWidthCacheEntry)
		if time.Since(e.at) < ttyWidthCacheTTL {
			return e.width
		}
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-c", tty, "#{client_width}").Output()
	if err != nil {
		return 0
	}
	w, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	ttyWidthCache.Store(tty, ttyWidthCacheEntry{width: w, at: time.Now()})
	return w
}

// navSourceWindowFromTarget resolves a tabby-hook bare-nav target to a
// window ID for use as the cycle anchor. Targets are either a window
// (@NN) — which we use directly — or a pane (%NN) forwarded from
// TMUX_PANE, which we resolve via cached coordinator state when possible
// (every nav keystroke fires this; tmux fork/exec was ~10ms on the hot
// path) and fall back to a tmux query if the pane isn't in the cache.
// Anchoring on the originating window makes M-}/M-{ navigation consistent
// regardless of which client tmux happens to consider globally active.
// Returns "" on any failure — empty source falls back to the daemon's
// existing default-client heuristic, preserving prior behavior.
func navSourceWindowFromTarget(target string) string {
	t := strings.TrimSpace(target)
	if strings.HasPrefix(t, "@") {
		return t
	}
	if strings.HasPrefix(t, "%") {
		if globalCoordinator != nil {
			if winID := globalCoordinator.windowIDForPaneCached(t); winID != "" {
				return winID
			}
		}
		out, err := exec.Command("tmux", "display-message", "-p", "-t", t, "#{window_id}").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return ""
}

// windowIDForPaneCached looks up the parent window ID for a pane from the
// coordinator's cached window list. Returns "" if not found — caller can
// fall back to a tmux query. Read-locks stateMu briefly.
func (c *Coordinator) windowIDForPaneCached(paneID string) string {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return ""
	}
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for i := range c.windows {
		w := &c.windows[i]
		for j := range w.Panes {
			if w.Panes[j].ID == paneID {
				return w.ID
			}
		}
	}
	return ""
}

// logNavKeyTrigger emits a diagnostic line for bare next_window/prev_window
// actions arriving from tabby-hook (i.e. real M-}/M-{ keystrokes hitting
// tmux's root keytable, normalized from the user's cmd+]/cmd+[ config).
// tabby-hook forwards TMUX_PANE (or a tmux-display fallback) in target and
// the most-recently-active client TTY in pickerValue so we can trace which
// client/pane fired the keystroke — useful for multi-client (phone+desktop)
// debugging where the source is otherwise opaque. The action itself still
// runs unchanged; this is log-only.
func logNavKeyTrigger(action, sourcePane, activeClientTTY string) {
	winID := ""
	if sourcePane != "" {
		if out, err := exec.Command("tmux", "display-message", "-p", "-t", sourcePane, "#{window_id}").Output(); err == nil {
			winID = strings.TrimSpace(string(out))
		}
	}
	resolvedTTY := strings.TrimSpace(clientTTYForWindow(winID))
	logEvent("NAV_KEY_TRIGGER action=%s source_pane=%q source_window=%s pane_owner_tty=%s ctx=%s",
		action, sourcePane, winID, resolvedTTY, activeClientTTY)
}

// isNavAction reports whether a resolved action is a keyboard window-nav request
// (the ones we trace end-to-end via pkg/navtrace).
func isNavAction(action string) bool {
	return action == "next_window" || action == "prev_window"
}

// navIDFromValue extracts the correlation id the hook embedded in an input's
// PickerValue ("...;navid=<id>"), or "" if absent. Lets a daemon-side nav trace
// line be matched to the HOOK_SENT line that produced it.
func navIDFromValue(pickerValue string) string {
	for _, p := range strings.Split(pickerValue, ";") {
		if strings.HasPrefix(p, "navid=") {
			return strings.TrimPrefix(p, "navid=")
		}
	}
	return ""
}

func tmuxClientWindowSnapshot() string {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}=#{window_id}(#{client_flags})").Output()
	if err != nil {
		return ""
	}
	lines := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, ",")
}

func fallbackWindowHeaderAction(windowID string, mouseX int) string {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return ""
	}
	width := 0
	if out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width}").Output(); err == nil {
		width, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	if width <= 0 {
		return ""
	}
	if mouseX < 0 {
		mouseX = 0
	}
	if mouseX >= width {
		mouseX = width - 1
	}

	const buttonCount = 6
	gapW := 1
	cellW := (width - gapW*(buttonCount-1)) / buttonCount
	if cellW < 3 {
		cellW = 3
	}
	used := cellW*buttonCount + gapW*(buttonCount-1)
	if used > width {
		gapW = 0
		cellW = width / buttonCount
		if cellW < 1 {
			cellW = 1
		}
		used = cellW * buttonCount
		if used > width {
			used = width
		}
	}
	leftover := width - used
	if leftover < 0 {
		leftover = 0
	}
	midGapW := gapW + leftover

	p1 := cellW
	h0 := p1 + gapW
	h1 := h0 + cellW
	cy0 := h1 + gapW
	cy1 := cy0 + cellW
	nw0 := cy1 + midGapW
	nw1 := nw0 + cellW
	cl0 := nw1 + gapW
	cl1 := cl0 + cellW
	n0 := cl1 + gapW
	n1 := n0 + cellW

	switch {
	case mouseX >= 0 && mouseX < p1:
		return "window_header:prev_window"
	case mouseX >= h0 && mouseX < h1:
		return "window_header:hamburger"
	case mouseX >= cy0 && mouseX < cy1:
		return "window_header:cycle_pane"
	case mouseX >= nw0 && mouseX < nw1:
		return "window_header:new_window"
	case mouseX >= cl0 && mouseX < cl1:
		return "window_header:close_window"
	case mouseX >= n0 && mouseX < n1:
		return "window_header:next_window"
	default:
		return ""
	}
}

func windowFocusRestoreTarget(activeWindowID, pendingNewWindowID string) string {
	if pendingNewWindowID == "" || pendingNewWindowID == activeWindowID {
		return ""
	}
	return pendingNewWindowID
}

// preferredWindowFocusTarget returns the window the post-spawn focus-restore
// should re-target, or "" to skip the restore. The original implementation
// compared the new window against an unscoped `display-message -p
// #{window_id}` query, but on multi-client sessions that query returns
// whichever client tmux happened to elect as default at that instant — and
// the elector flips between attached clients as their `client_activity`
// changes. Each flip from "phone on @new" to "desktop on @other" was
// detected as drift, restored via global `select-window`, which yanked all
// clients to @new, which generated more activity, which flipped the elector
// again. That feedback loop was the post-`+` cycling bug.
//
// The fix: when a FiringTTY was captured at spawn, query *that* client's
// current window via `display-message -c <tty>`. Only fire the restore if
// the firing client itself drifted off the new window. Other clients that
// stayed on their previous windows are no longer the daemon's concern.
//
// When no FiringTTY is set (legacy callers, or when client lookup failed),
// fall back to the prior global-active behaviour — preserved so we don't
// regress non-multi-client setups, but logged so we can spot the path.
func preferredWindowFocusTarget(c *Coordinator, activeWindowID string) string {
	if c == nil {
		return ""
	}
	status := c.NewWindowStatus()
	if status.State != "ready" {
		if status.WindowID != "" {
			logEvent("FOCUS_TARGET_SKIP pending=%s active=%s state=%s reason=not_ready", status.WindowID, activeWindowID, status.State)
		}
		return ""
	}
	if status.WindowID == "" {
		logEvent("FOCUS_TARGET_CHECK pending= active=%s result= state=%s reason=pending_empty age_ms=%d", activeWindowID, status.State, time.Since(status.Created).Milliseconds())
		return ""
	}

	// Firing-TTY-scoped path: only restore if the originating client drifted.
	if status.FiringTTY != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-c", status.FiringTTY, "#{window_id}").Output()
		cancel()
		if err == nil {
			firingActive := strings.TrimSpace(string(out))
			result := windowFocusRestoreTarget(firingActive, status.WindowID)
			reason := "firing_differs"
			if firingActive == status.WindowID {
				reason = "firing_on_pending"
			} else if firingActive == "" {
				reason = "firing_empty"
			}
			logEvent("FOCUS_TARGET_CHECK pending=%s active=%s firing_tty=%s firing_active=%s result=%s state=%s reason=%s age_ms=%d", status.WindowID, activeWindowID, status.FiringTTY, firingActive, result, status.State, reason, time.Since(status.Created).Milliseconds())
			return result
		}
		// display-message -c failed (client gone, etc.) — skip the restore
		// rather than fall through to the legacy path. The firing client
		// no longer being attached is a benign reason to drop the restore.
		logEvent("FOCUS_TARGET_SKIP pending=%s firing_tty=%s reason=display_message_err err=%v", status.WindowID, status.FiringTTY, err)
		return ""
	}

	// Legacy fallback: no FiringTTY captured. Use the pre-fix behaviour.
	target := windowFocusRestoreTarget(activeWindowID, status.WindowID)
	reason := "pending_differs"
	if status.WindowID == activeWindowID {
		reason = "pending_equals_active"
	}
	logEvent("FOCUS_TARGET_CHECK pending=%s active=%s result=%s state=%s reason=%s age_ms=%d firing_tty=", status.WindowID, activeWindowID, target, status.State, reason, time.Since(status.Created).Milliseconds())
	return target
}

type NewWindowStatus struct {
	State      string
	WindowID   string
	SessionID  string
	Group      string
	WorkingDir string
	Created    time.Time
	// FiringTTY is the tmux client that initiated the new-window action
	// (the phone, in the canonical mobile case). Used by
	// preferredWindowFocusTarget so the post-spawn focus-restore only fires
	// when *this* client has drifted off the new window — not when an
	// unrelated multi-client elector flip moves the daemon's idea of
	// "active" to a desktop client that never followed the new-window
	// switch in the first place. Empty when not captured (e.g. legacy
	// callers); restore falls back to the prior global-active heuristic.
	FiringTTY string
}

type WindowTransition struct {
	TargetWindowID string
	Reason         string
	Source         string
	StartedAt      time.Time
}

// restoreWindowFocus re-targets the firing client back to the just-created
// new window. When a FiringTTY is recorded on the pending status it uses
// `tmux switch-client -c <tty>` so only that client moves; other clients
// are left wherever they happen to be. This is the per-client scoping that
// fixes the "+ then cycles other windows" bug — global `select-window`
// previously yanked every attached client and the resulting activity
// burst flipped tmux's default-client elector, which the daemon then
// mis-read as the firing client drifting again, restarting the loop.
//
// Falls back to the global `select-window` path when no FiringTTY is set
// (legacy spawn paths or when client lookup failed) so single-client setups
// continue to behave identically.
func restoreWindowFocus(windowID string) {
	if windowID == "" {
		return
	}
	firingTTY := ""
	if globalCoordinator != nil {
		firingTTY = strings.TrimSpace(globalCoordinator.NewWindowStatus().FiringTTY)
	}
	if firingTTY != "" {
		if err := tmuxRun("switch-client", "-c", firingTTY, "-t", windowID); err != nil {
			logEvent("RESTORE_WINDOW_FOCUS_PERCLIENT_ERR target=%s tty=%s err=%v", windowID, firingTTY, err)
			// Fall through to global path so we still attempt recovery.
		} else {
			logEvent("RESTORE_WINDOW_FOCUS_PERCLIENT target=%s tty=%s", windowID, firingTTY)
			return
		}
	}
	if globalCoordinator != nil {
		if err := globalCoordinator.SelectWindow(windowID, "restore_window_focus", "refresh_windows"); err == nil {
			return
		} else {
			logEvent("RESTORE_WINDOW_FOCUS_ERR target=%s err=%v", windowID, err)
		}
	}
	_ = tmuxRun("select-window", "-t", windowID)
}

// StartDeadlockWatchdog starts a goroutine that monitors for deadlocks
func StartDeadlockWatchdog() {
	deadlockWatchdog = true
	// Initialize heartbeat
	heartbeatMu.Lock()
	lastHeartbeat = time.Now().UnixNano()
	heartbeatMu.Unlock()

	go func() {
		for deadlockWatchdog {
			time.Sleep(1 * time.Second)

			heartbeatMu.Lock()
			lastBeat := lastHeartbeat
			heartbeatMu.Unlock()

			elapsed := time.Since(time.Unix(0, lastBeat))
			if elapsed > deadlockThreshold {
				coordinatorDebugLog.Printf("DEADLOCK WARNING: No heartbeat for %v", elapsed)

				// Also write to crash log (debug log may be /dev/null in non-debug mode)
				if crashLog != nil {
					crashLog.Printf("DEADLOCK WARNING: No heartbeat for %v", elapsed)
				}
			}
		}
	}()
}

// StopDeadlockWatchdog stops the watchdog
func StopDeadlockWatchdog() {
	deadlockWatchdog = false
}

// recordHeartbeat updates the heartbeat timestamp
func recordHeartbeat() {
	heartbeatMu.Lock()
	lastHeartbeat = time.Now().UnixNano()
	heartbeatMu.Unlock()
}

// Coordinator manages centralized state and rendering for all renderers
type Coordinator struct {
	// Shared state
	windows         []tmux.Window
	grouped         []grouping.GroupedWindows
	windowVisualPos map[string]int // window ID -> visual position in sidebar
	config          *config.Config
	collapsedGroups map[string]bool
	spinnerFrame    int

	// Git state (cached)
	gitBranch string
	gitDirty  int
	gitAhead  int
	gitBehind int
	isGitRepo bool

	// Session state (cached)
	sessionName    string
	sessionClients int
	windowCount    int

	// TeamClaude quota state (cached). Fetched over HTTP in a detached
	// goroutine by RefreshTeamClaude (never on the event loop); read under
	// stateMu by renderTeamClaudeWidget. teamClaudeFetching coalesces so only
	// one HTTP request is ever in flight.
	teamClaudeStatus    *teamclaude.Status
	teamClaudeErr       error
	teamClaudeFetchedAt time.Time
	teamClaudeFetching  atomic.Bool

	// teamClaudeModels is the cached degraded-model map from GET
	// /teamclaude/models, fetched alongside status in the same RefreshTeamClaude
	// goroutine. An actively-degraded model swaps the widget header icon to a
	// warning glyph. Older servers 404 on this endpoint -> empty map (no warning).
	teamClaudeModels    teamclaude.Models
	teamClaudeModelsErr error

	// Pet state
	pet petState
	// petIsOwner reports whether THIS daemon currently owns writing the shared
	// pet.json. pet.json is global but every tmux session runs its own daemon;
	// without ownership they all tick+write it and clobber each other (a stale
	// daemon perpetually resetting hunger). Set each tick by acquirePetOwnership.
	petIsOwner bool

	cwdColors   map[string]CWDColorMapping
	cwdColorsMu sync.RWMutex

	// Parsed config.TabNames.Abbreviations (folder basename -> short code),
	// cached by config pointer identity so it's rebuilt only on config reload.
	// Guarded by its own mutex (never stateMu) so the render path can look up
	// abbreviations while holding stateMu.RLock without risking a deadlock.
	tabAbbrevMu  sync.Mutex
	tabAbbrevCfg *config.Config
	tabAbbrevMap map[string]string // tab_names.abbreviations: folder(lower) -> CODE
	tabProjMap   map[string]string // ai.tab_summary.project_names: folder(lower) -> abbrev

	// gitTopMu guards gitTopCache, which memoizes a local directory -> git
	// repository toplevel (or "" when not in a repo). windowNameKey consults this
	// to key a tab's saved name on the PROJECT ROOT rather than the exact cwd, so
	// the name set at a repo root is reused in its subdirs. Forking `git` every
	// refresh would be wasteful, and the toplevel of a given cwd is effectively
	// immutable, so the cache never needs invalidation. Its own mutex (never
	// stateMu) keeps it safe to consult from anywhere.
	gitTopMu    sync.Mutex
	gitTopCache map[string]string

	// Auto tab-summary generation (ai.tab_summary.auto_generate). The LLM client
	// is built lazily once; summaryFetching coalesces the periodic background
	// refresh; summaryHash skips windows whose pane content hasn't changed so
	// idle tabs don't re-call the LLM. See tab_summary.go.
	summaryClient     promptGenerator
	summaryClientOnce sync.Once
	summaryFetching   atomic.Bool
	summaryHashMu     sync.Mutex
	summaryHash       map[string]string
	// summaryLastAt records when each window was last summarized (guarded by
	// summaryHashMu). A per-window cooldown (summaryCooldown) bounds how often a
	// window can be re-named: without it, an active tab whose pane content keeps
	// changing re-summarizes on every trigger, and because tabby's own
	// rename-window fires the after-rename-window hook, it self-perpetuates into
	// a rename storm.
	summaryLastAt map[string]time.Time

	// Last known width (for pet physics clamping)
	lastWidth int

	// Click debounce for pet widget (prevents render floods from spam clicks)
	lastPetClick time.Time

	// Global width for synchronization
	globalWidth            int
	lastWidthSync          time.Time // Last time we synced widths (for debouncing)
	lastActiveWindowID     string    // Track which window was last active (for detecting window switch)
	activeWindowChangeTime time.Time // When the active window changed (for grace period)
	windowHistory          []string  // LIFO stack of recently visited window IDs (most recent first, max 20)
	widthSyncMu            sync.Mutex
	// Active physical tmux client snapshot. Updated by clientGeometryTicker
	// via SetActiveClient (which also updates activeClientWidth for the hot
	// path that only cares about width). Read by renderers via
	// ActiveClientSnapshot so RenderPayload.ActiveClient is fully populated.
	//
	// Step 5 of the daemon refactor: replaced activeClientWidthMu (RWMutex)
	// with two atomics. activeClient is a small (4-string-field) struct
	// rebuilt fresh on each Set; readers grab a pointer, no torn struct.
	// activeClientWidth is the hot path used by render-time clamps.
	activeClient      atomic.Pointer[daemon.ActiveClient]
	activeClientWidth atomic.Int64

	// Mobile border hide state: tracks whether tmux pane-border-style has been
	// overridden to the terminal background (invisible) because a narrow client
	// is active. Set by syncMobileBorders() — currently dead code, but the
	// fields stay so the struct shape doesn't churn.
	mobileBorderHidden bool
	mobileBorderInit   bool

	// Window-header button press feedback: maps windowID -> (action, timestamp).
	// Used to render a pressed/highlighted button for ~150ms after a tap.
	// Mutex is retained: activeWindowHeaderPress is read from server worker
	// goroutines via RenderHeaderForClient.
	windowHeaderPressMu sync.Mutex
	windowHeaderPress   map[string]windowHeaderPressEntry

	// Per-client widths for accurate click detection
	clientWidths      map[string]int
	clientHeights     map[string]int
	clientPrevWidth   map[string]int
	clientPrevHeight  map[string]int
	keyboardHoldUntil map[string]time.Time
	clientWidthsMu    sync.RWMutex

	// Per-client profile classification ("desktop" | "phone")
	clientProfile   map[string]string
	clientProfileMu sync.RWMutex

	// Auto-zoom ownership: tracks which windows were zoomed by phone profile.
	// (Map currently unreferenced; mutex dropped in Step 5.)
	windowZoomOwner map[string]string // windowID -> "phone" | ""

	// Per-(windowID, width) tmux layout snapshot. Captured before locking
	// every window to a new active-client width; replayed via select-layout
	// when that width becomes active again so multi-pane splits restore to
	// the user's prior proportions instead of being scaled greedily by tmux.
	windowLayouts   map[string]map[int]string
	windowLayoutsMu sync.Mutex

	// Sidebar visibility state (true while the sidebar pane is stashed in a
	// holding window via break-pane; false when it is live in its parent window).
	sidebarHidden bool
	// fullscreenSidebarWinID is the window whose CONTENT is currently stashed while
	// its sidebar pane fills the content area full-width (phone-only "full-width
	// sidebar" mode). Empty = not active. Mirrors the physical @tabby_fullscreen_sidebar
	// window-option so it survives a daemon restart.
	fullscreenSidebarWinID string
	newWindowMu      sync.RWMutex
	newWindowStatus  NewWindowStatus
	windowTransition WindowTransition

	// Pet widget layout (for custom click detection)
	petLayout petWidgetLayout

	// State locks
	stateMu sync.RWMutex

	// Session info
	sessionID string
	baseIndex int

	// Process tree caching
	lastProcessCheck  time.Time
	cachedProcessTree *processTree

	// AI tool state tracking — per-pane (for busy→idle transition detection)
	prevPaneBusy       map[string]bool   // pane ID → was AI tool busy last cycle
	prevPaneTitle      map[string]string // pane ID → AI pane title last cycle
	hookPaneActive     map[string]bool   // pane ID → hooks detected (seen @tabby_busy=1)
	hookPaneBusyIdleAt map[string]int64  // pane ID → unix timestamp when hook-busy but process looks idle
	aiInputAck         map[string]bool   // pane ID → user has viewed this idle/input state (suppress "?" until next busy cycle)
	aiBellUntil        map[int]int64     // window index → unix timestamp when bell expires (window-level)

	// Callback to sync sidebar client widths in the server's client map.
	// Called after a width change (e.g. grow/shrink) to update server-side Width
	// before BroadcastRender.
	OnSyncSidebarClientWidths func(newWidth int)

	// Callback to re-render a specific client. Used to flash button press
	// feedback on the window-header: record the press, call OnRefreshClient
	// so the next render draws the pressed style, then again after the press
	// TTL so it clears on its own.
	OnRefreshClient func(clientID string)

	// Callback to trigger a pane-layout refresh (spawn/kill window-headers,
	// pane-headers, sidebars). Fired on phone<->desktop profile transitions so
	// the header topology follows the active client's form factor.
	OnRefreshLayout func()

	// Callback to synchronously kill the phone-only window-header (fat-touch
	// button bar) panes. Fired on the phone->desktop transition so the bar
	// disappears in the same tick the sidebar is restored, instead of waiting
	// on the debounced doPaneLayoutOps path that loopFullRefreshCooldown can
	// skip.
	OnKillPhoneWindowHeaders func()

	// Context menu state (for in-renderer menus). pendingMenus,
	// lastWindowSelect/lastWindowByClient, and lastPaneMenuOpen are loop-only
	// dedup state — written and read exclusively from HandleInput on the
	// loop goroutine, so no mutex is needed (Step 5 of the daemon refactor).
	OnSendMenu         func(clientID string, menu *daemon.MenuPayload)
	OnSendMarkerPicker func(clientID string, picker *daemon.MarkerPickerPayload)
	OnSendColorPicker  func(clientID string, picker *daemon.ColorPickerPayload)
	pendingMenus       map[string][]menuItemDef

	lastWindowSelect   map[string]time.Time
	lastWindowByClient map[string]time.Time
	lastPaneMenuOpen   map[string]time.Time

	// Diagnostic state for the "+ tap then cycles through other windows" bug.
	// Loop-only: written and read exclusively from handleSemanticAction on the
	// loop goroutine, so no mutex is needed. All three are timestamps of the
	// most recent event of that kind, used to compute deltas in WINDOW_HEADER_CLICK
	// log lines so we can distinguish iOS auto-repeat (tight delta, same x)
	// from drift-to-next_window (delta gap, x jumped) from render-driven replay.
	lastWindowHeaderClickAt time.Time
	lastNewWindowAt         time.Time
	lastNewWindowID         string

	// Dashboard mode state. Loop-only (written/read from handleSemanticAction on
	// the loop goroutine), so no mutex needed. See dashboard.go.
	dashboardWindowID   string                        // window_id of the live "Dashboard" window, "" when inactive
	dashboardOrigins    map[string]dashWindowSnapshot // origin window_id -> snapshot for recreation
	dashboardOrder      []string                      // origin window ids, original index order
	dashboardReturnPane string                        // pane_id to refocus on exit
	// peekedWindowID is the minimized window currently SURFACED for peeking (moved
	// out of the holding session back into the user session while it is focused).
	// It re-parks into the holding session as soon as focus moves elsewhere, so at
	// most one minimized window is ever in-session at a time. Guarded by peekMu.
	peekMu         sync.Mutex
	peekedWindowID string
	// nativeBorderSig caches the last-applied border signature per window so
	// applyNativeBorders can skip its 5-set-option batch when nothing changed.
	// Cleared via InvalidateNativeBorderCache when something outside the
	// function could clobber the per-window options.
	nativeBorderMu  sync.Mutex
	nativeBorderSig map[string]string
	// dashboardBorderSig caches the last-applied dashboard border signature so
	// applyDashboardBorders can skip its per-refresh set-option burst when the
	// tile set / colors haven't changed. Without this the function re-issues
	// window- and pane-level border options every refresh, and each set forces a
	// tmux border redraw — visible flicker once tiles carry distinct colors.
	// Reset to "" on dashboard enter/exit so the next apply always re-asserts.
	dashboardBorderSig string
	// layoutCache stores the last layout string written to tmux per window
	// (via @tabby_layout_<wid>). SaveWindowLayouts skips the tmux set-option
	// round trip when the cached value matches — layouts change only on
	// split / kill / resize-pane, so most refreshes are pure cache hits.
	layoutCacheMu sync.Mutex
	layoutCache   map[string]string
	// statusExclusivityDecision caches the last "status on/off" value
	// EnforceStatusExclusivity applied. The tmux global option only changes
	// when the sidebar is toggled or tabby is disabled, so the steady-state
	// refresh path can skip the set-option round trip.
	statusExclusivityMu       sync.Mutex
	statusExclusivityDecision string
	// loadConfig cache: skips re-reading + re-parsing config.yaml when the
	// file's mtime hasn't moved. Steady-state RefreshWindows hits this every
	// refresh; the user only edits the file occasionally.
	configCacheMu    sync.Mutex
	configCacheMtime time.Time
	configCachePath  string
	configCacheCfg   *config.Config
	// Set true by an action handler when its work doesn't change anything the
	// renderers display (e.g. an in-dashboard pane cycle) — handleRendererInput
	// then skips SendRenderToClient + BroadcastRender to avoid the redraw flicker.
	dashboardSkipBroadcast atomic.Bool

	// Background theme detector (deprecated, kept for fallback)
	bgDetector *colors.BackgroundDetector

	// Color theme (new preset-based system)
	theme *colors.Theme
}

const mobileKeyboardHoldDuration = 4 * time.Second

// GetWindows returns the current list of windows
func (c *Coordinator) GetWindows() []tmux.Window {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]tmux.Window, len(c.windows))
	copy(result, c.windows)
	return result
}

func (c *Coordinator) NewWindowStatus() NewWindowStatus {
	c.newWindowMu.RLock()
	defer c.newWindowMu.RUnlock()
	status := c.newWindowStatus
	if status.State == "" {
		status.State = "none"
	}
	return status
}

func (c *Coordinator) SetNewWindowInFlight(group, workingDir, firingTTY string) {
	c.newWindowMu.Lock()
	c.newWindowStatus = NewWindowStatus{
		State:      "inFlight",
		SessionID:  c.sessionID,
		Group:      strings.TrimSpace(group),
		WorkingDir: strings.TrimSpace(workingDir),
		FiringTTY:  strings.TrimSpace(firingTTY),
		Created:    time.Now(),
	}
	c.newWindowMu.Unlock()
	logEvent("NEW_WINDOW_INFLIGHT session=%s group=%s path=%s firing_tty=%s", c.sessionID, strings.TrimSpace(group), strings.TrimSpace(workingDir), strings.TrimSpace(firingTTY))
}

func (c *Coordinator) SetNewWindowReady(windowID string) {
	windowID = strings.TrimSpace(windowID)
	c.newWindowMu.Lock()
	prev := c.newWindowStatus
	c.newWindowStatus = NewWindowStatus{
		State:      "ready",
		WindowID:   windowID,
		SessionID:  c.sessionID,
		Group:      prev.Group,
		WorkingDir: prev.WorkingDir,
		FiringTTY:  prev.FiringTTY,
		Created:    time.Now(),
	}
	c.newWindowMu.Unlock()
	logEvent("NEW_WINDOW_READY windowID=%s group=%s firing_tty=%s", windowID, prev.Group, prev.FiringTTY)
}

func (c *Coordinator) ClearNewWindowStatus() {
	c.newWindowMu.Lock()
	prev := c.newWindowStatus
	c.newWindowStatus = NewWindowStatus{State: "none"}
	c.newWindowMu.Unlock()
	logEvent("NEW_WINDOW_CLEAR prev_state=%s windowID=%s", prev.State, prev.WindowID)
}

func (c *Coordinator) UpdateClientSizeSnapshot(clientID string, width int, height int) {
	if clientID == "" || isHeaderClient(clientID) || width <= 0 {
		return
	}
	c.clientWidthsMu.Lock()
	var prevW, prevH int
	if c.clientPrevWidth != nil {
		prevW = c.clientPrevWidth[clientID]
	}
	if c.clientPrevHeight != nil {
		prevH = c.clientPrevHeight[clientID]
	}
	if width == prevW && height == prevH && prevW > 0 {
		coordinatorDebugLog.Printf("RESIZE_NOOP client=%s width=%d height=%d", clientID, width, height)
		c.clientWidthsMu.Unlock()
		return
	}
	if c.clientPrevWidth == nil {
		c.clientPrevWidth = make(map[string]int)
	}
	c.clientPrevWidth[clientID] = width
	if height > 0 {
		if c.clientPrevHeight == nil {
			c.clientPrevHeight = make(map[string]int)
		}
		c.clientPrevHeight[clientID] = height
	}
	c.clientWidths[clientID] = width
	if height > 0 {
		if c.clientHeights == nil {
			c.clientHeights = make(map[string]int)
		}
		c.clientHeights[clientID] = height
	}
	c.updateKeyboardHoldLocked(clientID, height)
	c.clientWidthsMu.Unlock()
	c.SetClientProfile(clientID, c.computeProfile(width))
}

// GetGlobalWidth returns the coordinator's current global sidebar width.
// This is the source of truth for sidebar width, used by RunWidthSync.
// Spawn operations should use this instead of reading @tabby_sidebar_width
// directly to ensure consistency.
func (c *Coordinator) GetGlobalWidth() int {
	c.widthSyncMu.Lock()
	defer c.widthSyncMu.Unlock()
	if c.globalWidth <= 0 {
		return 25 // Default
	}
	return c.globalWidth
}

func (c *Coordinator) collapseWindowPanes(windowTarget string, win *tmux.Window) {
	// Use actual window width for header height, not global profile
	winWidthOut, _ := exec.Command("tmux", "display-message", "-p", "-t", windowTarget, "#{window_width}").Output()
	winWidth, _ := strconv.Atoi(strings.TrimSpace(string(winWidthOut)))
	headerHeight := c.desiredWindowHeaderHeightForWidth(winWidth)
	for _, pane := range win.Panes {
		paneID := pane.ID
		if paneID == "" {
			continue
		}
		heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
		prevHeight := strings.TrimSpace(string(heightOut))
		if prevHeight == "" {
			prevHeight = "1"
		}
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_prev_height", prevHeight).Run()
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_collapsed", "1").Run()
		exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", headerHeight)).Run()
	}
	exec.Command("tmux", "set-window-option", "-t", windowTarget, "@tabby_collapsed", "1").Run()
}

func (c *Coordinator) expandWindowPanes(windowTarget string, win *tmux.Window) {
	for _, pane := range win.Panes {
		paneID := pane.ID
		if paneID == "" {
			continue
		}
		prevHeightOut, _ := exec.Command("tmux", "show-options", "-p", "-t", paneID, "@tabby_pane_prev_height").Output()
		prevHeightStr := strings.TrimSpace(string(prevHeightOut))
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_prev_height").Run()
		if prevHeightStr != "" && prevHeightStr != "0" {
			exec.Command("tmux", "resize-pane", "-t", paneID, "-y", prevHeightStr).Run()
		}
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_collapsed").Run()
	}
	exec.Command("tmux", "set-window-option", "-t", windowTarget, "-u", "@tabby_collapsed").Run()
}

func (c *Coordinator) togglePaneCollapse(windowTarget string) bool {
	target := strings.TrimPrefix(windowTarget, ":")
	if target == "" {
		return false
	}
	idx, parseErr := strconv.Atoi(target)
	if parseErr != nil {
		return false
	}

	c.stateMu.RLock()
	var windowCopy tmux.Window
	found := false
	for i := range c.windows {
		if c.windows[i].Index == idx {
			windowCopy = c.windows[i]
			found = true
			break
		}
	}
	c.stateMu.RUnlock()

	if !found {
		return false
	}

	winTarget := fmt.Sprintf(":%d", idx)
	collapsed := false
	if out, err := exec.Command("tmux", "show-window-option", "-v", "-t", winTarget, "@tabby_collapsed").Output(); err == nil {
		val := strings.TrimSpace(string(out))
		if val == "1" || strings.EqualFold(val, "true") {
			collapsed = true
		}
	}

	if collapsed {
		c.expandWindowPanes(winTarget, &windowCopy)
	} else {
		c.collapseWindowPanes(winTarget, &windowCopy)
	}
	return true
}

// petState holds the current state of the pet widget
type petState struct {
	Pos               pos2D
	State             string
	Direction         int
	Hunger            int
	Happiness         int
	YarnPos           pos2D
	YarnExpiresAt     time.Time // When yarn disappears
	YarnPushCount     int       // How many times yarn has been pushed (catch after 2)
	FoodItem          pos2D
	PoopPositions     []int
	NeedsPoopAt       time.Time
	LastFed           time.Time
	LastPet           time.Time
	LastPoop          time.Time
	LastHungerTick    time.Time // wall-clock anchor for hunger decay (predictable, reload-proof)
	LastHappyTick     time.Time // wall-clock anchor for happiness decay
	LastThought       string
	ThoughtScroll     int
	FloatingItems     []floatingItem
	TargetPos         pos2D
	HasTarget         bool
	ActionPending     string
	AnimFrame         int
	CameraX           int // Scrolling background tracker
	TotalPets         int
	TotalFeedings     int
	TotalPoopsCleaned int
	TotalYarnPlays    int
	// Death state
	IsDead        bool
	DeathTime     time.Time
	StarvingStart time.Time // When hunger hit 0 (for death countdown)
	// Mouse state
	MousePos          pos2D     // X: -1 means no mouse present
	MouseDirection    int       // Direction mouse is moving
	MouseAppearsAt    time.Time // When a mouse will appear next
	TotalMouseCatches int
	// Adventure state
	Adventure adventureState
	// Debug state
	DebugThoughtIdx int // Index into debugThoughtCategories for debug bar
	// Dragon friend state
	DragonPos           pos2D     `json:"dragon_pos"`
	DragonState         string    `json:"dragon_state"`
	DragonTargetPos     pos2D     `json:"dragon_target_pos"`
	DragonHasTarget     bool      `json:"dragon_has_target"`
	DragonDirection     int       `json:"dragon_direction"`
	DragonActionPending string    `json:"dragon_action_pending"`
	DragonAppearedAt    time.Time `json:"dragon_appeared_at"`
	DragonDisappearsAt  time.Time `json:"dragon_disappears_at"`
	// Q&A personality-building loop. Mirrors the wire-format
	// daemon.PetState fields (same JSON tags) so a pet.json written by
	// either type deserialises into the other. All omitempty so old pet.json
	// files load with zero-valued feature state. See pet_qa.go for the
	// logic that mutates these and pkg/daemon/protocol.go for the wire
	// counterpart used by CLI<->daemon IPC.
	PendingQuestion    *daemon.PendingQuestion   `json:"pending_question,omitempty"`
	AnsweredQuestions  []daemon.AnsweredQuestion `json:"answered_questions,omitempty"`
	Traits             []daemon.PersonalityTrait `json:"traits,omitempty"`
	QuestionCooldown   time.Time                 `json:"question_cooldown,omitzero"`
	LastQuestionShown  time.Time                 `json:"last_question_shown,omitzero"`
	QAOptedOut         bool                      `json:"qa_opted_out,omitempty"`
	QAFreeTextOptedOut bool                      `json:"qa_free_text_opted_out,omitempty"`
}

type pos2D struct {
	X int
	Y int
}

type floatingItem struct {
	Emoji     string
	Pos       pos2D
	Velocity  pos2D
	ExpiresAt time.Time
}

// Adventure mode types
type adventurePhase string

const (
	advPhaseNone      adventurePhase = ""
	advPhaseDeparting adventurePhase = "departing"
	advPhaseExploring adventurePhase = "exploring"
	advPhaseEncounter adventurePhase = "encounter"
	advPhaseReturning adventurePhase = "returning"
	advPhaseArriving  adventurePhase = "arriving"
)

type adventureState struct {
	Active            bool
	Phase             adventurePhase
	PhaseStart        time.Time
	PhaseDuration     time.Duration
	Biome             string
	SceneOffset       int // How far cat has traveled (for scenery scrolling)
	Wildlife          *wildlifeEncounter
	CatX              int  // Cat position during adventure (relative to play area)
	HomeX             int  // Cat's original position before adventure started
	ManuallyTriggered bool // True if started via debug button, ignores config disable
	LastThought       string
	TotalCatches      int
}

type wildlifeEncounter struct {
	Type         string
	Emoji        string
	X            int // Position in scene
	Y            int // 0=ground, 1=low air, 2=high air
	Speed        int
	CatchChance  int
	Spotted      bool
	Stalking     bool
	Pounced      bool
	PounceFrames int
	WillCatch    bool
	Caught       bool
	Escaped      bool
	Approach     int
}

type biomeData struct {
	Ground   string
	Scenery  []string
	Wildlife []string
}

type wildlifeData struct {
	Emoji       string
	YLevel      int // 0=ground, 1=low air, 2=high air
	Speed       int
	CatchChance int
}

// Biome definitions
var adventureBiomes = map[string]biomeData{
	"forest": {
		Ground:   "~",
		Scenery:  []string{"🌳", "🌲", "🪨", "🍂", "🌿"},
		Wildlife: []string{"squirrel", "bird", "bug"},
	},
	"meadow": {
		Ground:   ",",
		Scenery:  []string{"🌸", "🌾", "🌻", "🦋", "🌿"},
		Wildlife: []string{"butterfly", "bird", "mouse", "bug"},
	},
	"garden": {
		Ground:   ".",
		Scenery:  []string{"🌷", "🌹", "🪴", "🪨", "🍃"},
		Wildlife: []string{"bird", "lizard", "bug", "butterfly"},
	},
	"backyard": {
		Ground:   "_",
		Scenery:  []string{"🪵", "🪨", "🌿", "🍂"},
		Wildlife: []string{"mouse", "bird", "squirrel", "lizard"},
	},
}

// Wildlife definitions
var adventureWildlife = map[string]wildlifeData{
	"squirrel":  {Emoji: "🐿️", YLevel: 0, Speed: 2, CatchChance: 30},
	"bird":      {Emoji: "🐦", YLevel: 2, Speed: 3, CatchChance: 15},
	"butterfly": {Emoji: "🦋", YLevel: 1, Speed: 1, CatchChance: 60},
	"bug":       {Emoji: "🐛", YLevel: 0, Speed: 1, CatchChance: 80},
	"mouse":     {Emoji: "🐭", YLevel: 0, Speed: 2, CatchChance: 50},
	"lizard":    {Emoji: "🦎", YLevel: 0, Speed: 3, CatchChance: 25},
}

// Adventure thoughts by wildlife type and phase
var adventureThoughts = map[string]map[string][]string{
	"squirrel": {
		"spot":   []string{"squirrel.", "prey detected.", "target acquired.", "fluffy tail..."},
		"stalk":  []string{"creeping...", "patience...", "closer...", "silent paws..."},
		"catch":  []string{"got you!", "mine now.", "natural order.", "victory!"},
		"escape": []string{"next time.", "curse you, tree.", "too fast.", "the hunt continues."},
	},
	"bird": {
		"spot":   []string{"bird.", "wings.", "foolish creature.", "come down here..."},
		"stalk":  []string{"watching...", "waiting...", "soon...", "calculating..."},
		"catch":  []string{"impossible!", "got one!", "legendary.", "I am apex."},
		"escape": []string{"fly away then.", "gravity wins.", "next time, bird.", "curse these paws."},
	},
	"butterfly": {
		"spot":   []string{"flutter.", "pretty prey.", "floating snack.", "must catch."},
		"stalk":  []string{"gentle...", "easy...", "almost...", "focus..."},
		"catch":  []string{"got it!", "delicate.", "mine.", "beautiful catch."},
		"escape": []string{"too floaty.", "wind took it.", "next one.", "pretty but quick."},
	},
	"bug": {
		"spot":   []string{"bug.", "crunchy.", "protein.", "easy prey."},
		"stalk":  []string{"sneaking...", "closer...", "simple...", "patience..."},
		"catch":  []string{"crunch.", "tasty.", "got it.", "efficient."},
		"escape": []string{"fast bug.", "under leaf.", "next bug.", "how?"},
	},
	"mouse": {
		"spot":   []string{"mouse!", "classic.", "the chase.", "ancient rivalry."},
		"stalk":  []string{"creeping...", "silent...", "focused...", "instinct guides..."},
		"catch":  []string{"gotcha!", "mouse mine.", "perfect.", "legendary catch."},
		"escape": []string{"quick mouse.", "hole escape.", "rivalry continues.", "next time, mouse."},
	},
	"lizard": {
		"spot":   []string{"lizard.", "scaly one.", "fast prey.", "challenge accepted."},
		"stalk":  []string{"careful...", "they sense heat...", "slow...", "steady..."},
		"catch":  []string{"scales!", "got it!", "cold-blooded victory.", "impressive."},
		"escape": []string{"too quick.", "tail trick?", "slippery.", "lizards cheat."},
	},
}

// petWidgetLayout tracks line offsets for custom click detection
//
// CLICK DETECTION METHODS:
//
// 1. BubbleZone (used for static elements):
//   - Wrap text with zone.Mark("zone_id", text) during rendering
//   - Call zone.Scan() on the full output to process markers
//   - Use zone.Get("zone_id") to retrieve bounds (StartX, EndX, StartY, EndY)
//   - Good for: buttons, fixed-position elements
//   - Limitation: Only ONE zone per ID is tracked (multiple zones with same ID overwrite)
//
// 2. Custom Layout Tracking (used for pet widget play area):
//   - Track line numbers during rendering (currentLine counter)
//   - Store positions in a layout struct (petWidgetLayout)
//   - In click handler, compare input.PinnedRelY against stored line numbers
//   - Use input.MouseX for horizontal position within the line
//   - Good for: complex dynamic content, multi-element interactions, precise hit testing
//   - Requires: manual tracking during render, custom click handler
//
// The pet widget uses BOTH methods:
// - BubbleZone for the Feed button (zone.Mark("pet:drop_food", ...))
// - Custom tracking for play area (air lines, ground line)
//
// Line numbers are relative to the widget output start (0-indexed), except ContentStartLine
// which is the absolute content line where the pet widget begins.
type petWidgetLayout struct {
	ContentStartLine   int // Absolute content line where pet widget starts (set in RenderForClient)
	FeedLine           int // "Feed" button line (relative to widget start)
	HighAirLine        int // High air (Y=2) line - click drops yarn
	LowAirLine         int // Low air (Y=1) line - click drops yarn
	GroundLine         int // Ground (Y=0) line - click on cat pets, click on poop cleans, else drops yarn
	PlayWidth          int // Width of play area (safePlayWidth) - clicks beyond this are ignored
	WidgetHeight       int // Total widget height in lines
	DebugLine1         int // Y position of debug line 1 (mode triggers)
	DebugLine2         int // Y position of debug line 2 (thought controls)
	DebugLine3         int // Y position of debug line 3 (popup / overflow triggers)
	QuestionPromptLine int // Row index of the Q&A teaser bubble line when shown (-1 when not pending)
}

// debugThoughtCategories lists all thought categories for debug bar cycling
var debugThoughtCategories = []string{
	"idle", "happy", "hungry", "sleepy", "yarn", "walking",
	"jumping", "petting", "starving", "guilt", "dead",
	"mouse_spot", "mouse_chase", "mouse_catch", "mouse_kill",
	"poop_jump", "wakeup", "poop",
}

// Pet sprites by style
type petSprites struct {
	Idle, Walking, Jumping, Playing string
	Eating, Sleeping, Happy, Hungry string
	Dead                            string
	Yarn, Food, Poop, Mouse         string
	Blood                           string
	Thought, Heart, Life            string
	HungerIcon, HappyIcon, SadIcon  string
	Ground                          string
}

var petSpritesByStyle = map[string]petSprites{
	"emoji": {
		Idle: "🐱", Walking: "🐱", Jumping: "🐱", Playing: "🐱",
		Eating: "🐱", Sleeping: "😺", Happy: "😻", Hungry: "😿",
		Dead: "💀",
		Yarn: "🧶", Food: "🍖", Poop: "💩", Mouse: "🐭",
		Blood:   "🩸",
		Thought: "💭", Heart: "❤", Life: "💗",
		HungerIcon: "🍖", HappyIcon: "😸", SadIcon: "😿",
		Ground: "·",
	},
	"nerd": {
		Idle: "󰄛", Walking: "󰄛", Jumping: "󰄛", Playing: "󰄛",
		Eating: "󰄛", Sleeping: "󰄛", Happy: "󰄛", Hungry: "󰄛",
		Dead: "",
		Yarn: "", Food: "", Poop: "", Mouse: "",
		Blood:   "",
		Thought: "", Heart: "", Life: "",
		HungerIcon: "", HappyIcon: "", SadIcon: "",
		Ground: "·",
	},
	"ascii": {
		Idle: "=^.^=", Walking: "=^.^=", Jumping: "=^o^=", Playing: "=^.^=",
		Eating: "=^.^=", Sleeping: "=-.~=", Happy: "=^.^=", Hungry: "=;.;=",
		Dead: "x_x",
		Yarn: "@", Food: "o", Poop: ".", Mouse: "<:3",
		Blood:   "x",
		Thought: ">", Heart: "<3", Life: "*",
		HungerIcon: "o", HappyIcon: ":)", SadIcon: ":(",
		Ground: ".",
	},
}

// Session icons by style
var sessionIconsByStyle = map[string]struct{ Session, Clients, Windows string }{
	"nerd":    {Session: "", Clients: "", Windows: ""},
	"emoji":   {Session: "📺", Clients: "👥", Windows: "🪟"},
	"ascii":   {Session: "[tmux]", Clients: "users:", Windows: "wins:"},
	"minimal": {Session: "", Clients: "", Windows: ""},
}

// NewCoordinator creates a new coordinator instance
func NewCoordinator(sessionID string) *Coordinator {
	// Enable TrueColor for accurate theme rendering
	lipgloss.SetColorProfile(termenv.TrueColor)

	// Tell tmux that xterm-like clients support RGB truecolor so hex border/style
	// colors render exactly rather than being quantized to the 256-palette. Without
	// this, pane-border-style=#faf4ed doesn't match true-RGB pane backgrounds,
	// leaving visible border lines even when we "hide" them by color-matching.
	exec.Command("tmux", "set-option", "-sa", "terminal-features", ",xterm*:RGB").Run()

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		cfg = config.DefaultConfig()
	}
	applyContrastConfig(cfg)

	// Set up debug logging from config if enabled
	if cfg.Sidebar.Debug {
		f, err := os.OpenFile("/tmp/tabby-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			coordinatorDebugLog = log.New(f, "[coord] ", log.LstdFlags|log.Lmicroseconds)
		}
	}

	// Initialize background detector based on theme_mode config (deprecated fallback)
	themeMode := cfg.Sidebar.ThemeMode
	if themeMode == "" {
		themeMode = "auto" // Default to auto-detection
	}
	bgDetector := colors.NewBackgroundDetector(colors.ThemeMode(themeMode))

	// Load color theme (new preset-based system)
	var theme *colors.Theme
	if cfg.Sidebar.Theme != "" {
		t := colors.GetTheme(cfg.Sidebar.Theme)
		theme = &t
	}

	baseIndex := 1
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_base_index").Output(); err == nil {
		val := strings.TrimSpace(string(out))
		if val == "0" || strings.EqualFold(val, "false") {
			baseIndex = 0
		}
	}

	c := &Coordinator{
		sessionID:          sessionID,
		baseIndex:          baseIndex,
		config:             cfg,
		bgDetector:         bgDetector,
		theme:              theme,
		cwdColors:          make(map[string]CWDColorMapping),
		gitTopCache:        make(map[string]string),
		collapsedGroups:    make(map[string]bool),
		clientWidths:       make(map[string]int),
		clientHeights:      make(map[string]int),
		clientPrevWidth:    make(map[string]int),
		clientPrevHeight:   make(map[string]int),
		keyboardHoldUntil:  make(map[string]time.Time),
		pendingMenus:       make(map[string][]menuItemDef),
		lastWindowSelect:   make(map[string]time.Time),
		lastWindowByClient: make(map[string]time.Time),
		lastPaneMenuOpen:   make(map[string]time.Time),
		prevPaneBusy:       make(map[string]bool),
		prevPaneTitle:      make(map[string]string),
		aiBellUntil:        make(map[int]int64),
		hookPaneActive:     make(map[string]bool),
		hookPaneBusyIdleAt: make(map[string]int64),
		aiInputAck:         make(map[string]bool),
		clientProfile:      make(map[string]string),
		windowZoomOwner:    make(map[string]string),
		windowLayouts:      make(map[string]map[int]string),
		lastWidth:          25, // Default width for pet physics
		pet: petState{
			Pos:       pos2D{X: 10, Y: 0},
			State:     "idle",
			Direction: 1,
			Hunger:    80,
			Happiness: 80,
			YarnPos:   pos2D{X: -1, Y: 0},
			FoodItem:  pos2D{X: -1, Y: -1},
			MousePos:  pos2D{X: -1, Y: 0},
		},
	}

	// Log theme and background detection if debug enabled
	if cfg.Sidebar.Debug {
		if theme != nil {
			coordinatorDebugLog.Printf("Theme loaded: %s (dark=%v, sidebar_bg=%s)", theme.Name, theme.Dark, theme.SidebarBg)
		} else {
			isDark := bgDetector.IsDarkBackground()
			detectedColor := bgDetector.GetDetectedColor()
			if detectedColor != "" {
				coordinatorDebugLog.Printf("Background detection: theme_mode=%s, detected_dark=%v, color=%s", themeMode, isDark, detectedColor)
			} else {
				coordinatorDebugLog.Printf("Background detection: theme_mode=%s, detected_dark=%v (fallback)", themeMode, isDark)
			}
		}
	}

	// Configure busy detection from config
	tmux.ConfigureBusyDetection(cfg.BusyDetection.ExtraIdle, cfg.BusyDetection.AITools, cfg.BusyDetection.IdleTimeout)

	// Load collapsed groups from tmux option
	c.loadCollapsedGroups()

	// Load pet state from shared file
	c.loadPetState()
	c.loadCWDColors()

	// Initialize LLM if thoughts are enabled
	if cfg.Widgets.Pet.Thoughts {
		if err := initLLM(cfg.Widgets.Pet.LLMProvider, cfg.Widgets.Pet.LLMModel, cfg.Widgets.Pet.LLMAPIKey, cfg.Widgets.Pet.LLMBaseURL); err != nil {
			coordinatorDebugLog.Printf("LLM init failed: %v (using default thoughts)", err)
		} else {
			coordinatorDebugLog.Printf("LLM initialized with provider=%s model=%s", cfg.Widgets.Pet.LLMProvider, cfg.Widgets.Pet.LLMModel)
			// Set thought generation interval from config
			if cfg.Widgets.Pet.ThoughtRefreshHours > 0 {
				SetThoughtGenerationInterval(cfg.Widgets.Pet.ThoughtRefreshHours)
			}
			// Trigger initial thought generation
			triggerThoughtGeneration(&c.pet, cfg.Widgets.Pet.Name)
		}
	}

	// Initial window refresh
	c.RefreshWindows()

	// Initial git refresh
	c.RefreshGit()

	// Initial session refresh
	c.RefreshSession()

	// Initialize global width from tmux option
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width").Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w > 0 {
			c.globalWidth = w
		} else {
			c.globalWidth = 25 // Default
		}
	}

	// On startup, force-resize any 1-column sidebars back to globalWidth.
	// This handles the case where a phone client previously collapsed sidebars
	// and the daemon restarted with desktop active.
	if c.globalWidth >= 10 {
		out, _ := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_id}|#{pane_current_command}|#{pane_width}|#{pane_start_command}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "|", 4)
			if len(parts) >= 4 && isSidebarPaneCommand(parts[1], parts[3]) {
				w, _ := strconv.Atoi(parts[2])
				if w > 0 && w < 5 {
					exec.Command("tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", c.globalWidth)).Run()
				}
			}
		}
	}

	// Clear any stale legacy tmux globals from the old 1-col "collapse" feature.
	exec.Command("tmux", "set-option", "-gqu", "@tabby_sidebar_collapsed").Run()
	exec.Command("tmux", "set-option", "-gqu", "@tabby_sidebar_previous_width").Run()

	// Apply global theme styles to tmux (borders, messages, etc.)
	c.applyThemeToTmux()

	// Initialize sidebarHidden from physical tmux state so spawnRenderersForNewWindows
	// doesn't try to spawn sidebars for windows created without one while the phone
	// profile transition timer hasn't fired yet (750ms window after restart).
	c.sidebarHidden = sidebarIsStashed()
	logEvent("COORDINATOR_INIT sidebarHidden=%v (from stash windows)", c.sidebarHidden)

	// Migrate any pre-existing stash windows that an older daemon left parked
	// in a user session into the detached limbo session, so a restart while the
	// sidebar is hidden ends up in the new in-limbo state (and the stashes are
	// immediately removed from native window cycling).
	parkExistingStashWindows()

	// Park any window still sitting in THIS session flagged @tabby_minimized (a
	// leftover from the old in-session minimize model, or a peeked window that was
	// surfaced when a prior daemon died), so a restart immediately removes it from
	// native window cycling and re-establishes the parked state.
	c.parkExistingMinimizedWindows()

	// Re-learn full-width-sidebar mode from tmux (the physical state — content in
	// limbo, sidebar full-width, @tabby_fullscreen_sidebar set — survives a daemon
	// restart; we just need the in-memory pointer back).
	c.fullscreenSidebarWinID = fullscreenSidebarActiveWindowID(c.dashboardSession())
	if c.fullscreenSidebarWinID != "" {
		logEvent("FULLSCREEN_RECONCILE window=%s", c.fullscreenSidebarWinID)
	}

	return c
}

// parkExistingMinimizedWindows parks any window in the daemon's own session that
// carries @tabby_minimized=1 into the holding session. Idempotent; runs move-window
// only for windows that need relocating.
func (c *Coordinator) parkExistingMinimizedWindows() {
	sess := c.dashboardSession()
	if sess == "" {
		return
	}
	out, err := exec.Command("tmux", "list-windows", "-t", sess, "-F",
		"#{window_id}\t#{@tabby_minimized}").Output()
	if err != nil {
		return
	}
	moved := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(f) < 2 || strings.TrimSpace(f[1]) != "1" {
			continue
		}
		if c.parkWindow(strings.TrimSpace(f[0]), true) {
			moved++
		}
	}
	if moved > 0 {
		logEvent("MINIMIZED_MIGRATE moved=%d -> %s", moved, minimizedHoldingSession)
	}
	cleanupMinimizedSessionIfEmpty()
}

// parkExistingStashWindows migrates any stash windows that live OUTSIDE the
// limbo session (e.g. left in a user session by an older daemon that parked
// stashes at high in-session indices) into the limbo session. Idempotent —
// stashes already in limbo are skipped. Safe to call any time; only runs tmux
// move-window for stashes that need relocating.
func parkExistingStashWindows() {
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}|#{session_name}|#{window_name}").Output()
	if err != nil {
		return
	}
	moved := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) < 3 {
			continue
		}
		winID := parts[0]
		sessName := parts[1]
		name := parts[2]
		if !strings.HasPrefix(name, sidebarStashWindowPrefix) {
			continue
		}
		if sessName == sidebarLimboSession {
			continue // already parked in limbo
		}
		ensureLimboSession()
		if err := exec.Command("tmux", "move-window", "-d", "-a", "-s", winID, "-t", fmt.Sprintf("%s:%d", sidebarLimboSession, sidebarStashParkBase)).Run(); err != nil {
			coordinatorDebugLog.Printf("parkExistingStashWindows: migrate %s (session=%s) failed: %v", winID, sessName, err)
			continue
		}
		moved++
	}
	if moved > 0 {
		logEvent("STASH_MIGRATE moved=%d -> %s", moved, sidebarLimboSession)
	}
}

// GetConfig returns the coordinator's config (for use by main.go)
func (c *Coordinator) GetConfig() *config.Config {
	return c.config
}

// getTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.ActiveFg
	}
	return c.bgDetector.GetDefaultTextColor()
}

// getHeaderTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getHeaderTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.HeaderFg
	}
	return c.bgDetector.GetDefaultHeaderTextColor()
}

// getInactiveTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getInactiveTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.InactiveFg
	}
	return c.bgDetector.GetDefaultInactiveTextColor()
}

// getPaneFgWithFallback returns pane text color, falling back to inactive_fg
func (c *Coordinator) getPaneFgWithFallback() string {
	if c.config.Sidebar.Colors.PaneFg != "" {
		return c.config.Sidebar.Colors.PaneFg
	}
	return c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)
}

// getTreeFgWithFallback returns tree branch color from config, theme, or detector
func (c *Coordinator) getTreeFgWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.TreeFg
	}
	return c.bgDetector.GetDefaultTreeFg()
}

// getDisclosureFgWithFallback returns disclosure icon color from config, theme, or detector
func (c *Coordinator) getDisclosureFgWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.DisclosureFg
	}
	return c.bgDetector.GetDefaultDisclosureFg()
}

// getPaneHeaderActiveBg returns active pane header background from config, theme, or detector
func (c *Coordinator) getPaneHeaderActiveBg() string {
	if c.config.PaneHeader.ActiveBg != "" {
		return c.config.PaneHeader.ActiveBg
	}
	if c.theme != nil {
		return c.theme.PaneActiveBg
	}
	return c.bgDetector.GetDefaultPaneHeaderActiveBg()
}

// getPaneHeaderActiveFg returns active pane header foreground from config, theme, or detector
func (c *Coordinator) getPaneHeaderActiveFg() string {
	if c.config.PaneHeader.ActiveFg != "" {
		return c.config.PaneHeader.ActiveFg
	}
	if c.theme != nil {
		return c.theme.PaneActiveFg
	}
	return c.bgDetector.GetDefaultPaneHeaderActiveFg()
}

// getPaneHeaderInactiveBg returns inactive pane header background from config, theme, or detector
func (c *Coordinator) getPaneHeaderInactiveBg() string {
	if c.config.PaneHeader.InactiveBg != "" {
		return c.config.PaneHeader.InactiveBg
	}
	if c.theme != nil {
		return c.theme.PaneInactiveBg
	}
	return c.bgDetector.GetDefaultPaneHeaderInactiveBg()
}

// getPaneHeaderInactiveFg returns inactive pane header foreground from config, theme, or detector
func (c *Coordinator) getPaneHeaderInactiveFg() string {
	if c.config.PaneHeader.InactiveFg != "" {
		return c.config.PaneHeader.InactiveFg
	}
	if c.theme != nil {
		return c.theme.PaneInactiveFg
	}
	return c.bgDetector.GetDefaultPaneHeaderInactiveFg()
}

// getCommandFg returns command text color from config, theme, or detector
func (c *Coordinator) getCommandFg() string {
	if c.config.PaneHeader.CommandFg != "" {
		return c.config.PaneHeader.CommandFg
	}
	if c.theme != nil {
		return c.theme.CommandFg
	}
	return c.bgDetector.GetDefaultCommandFg()
}

// getButtonFg returns button text color from config, theme, or detector
func (c *Coordinator) getButtonFg() string {
	if c.config.PaneHeader.ButtonFg != "" {
		return c.config.PaneHeader.ButtonFg
	}
	if c.theme != nil {
		return c.theme.PaneButtonFg
	}
	return c.bgDetector.GetDefaultButtonFg()
}

// buildBorderStyle builds a tmux style string from fg and bg colors.
// Returns "" if fg is empty.
func buildBorderStyle(fg, bg string) string {
	if fg == "" {
		return ""
	}
	s := "fg=" + fg
	if bg != "" {
		s += ",bg=" + bg
	}
	return s
}

// getBorderFg returns border color from config, theme, or detector
func (c *Coordinator) getBorderFg() string {
	if c.config.PaneHeader.BorderFg != "" {
		return c.config.PaneHeader.BorderFg
	}
	if c.theme != nil {
		return c.theme.BorderFg
	}
	return c.bgDetector.GetDefaultBorderFg()
}

// getHandleColor returns drag handle color from config, theme, or detector
func (c *Coordinator) getHandleColor() string {
	if c.config.PaneHeader.HandleColor != "" {
		return c.config.PaneHeader.HandleColor
	}
	if c.theme != nil {
		return c.theme.HandleColor
	}
	return c.bgDetector.GetDefaultHandleColor()
}

// GetTerminalBg returns terminal background color from config, theme, or detector
func (c *Coordinator) GetTerminalBg() string {
	if c.config.PaneHeader.TerminalBg != "" {
		return c.config.PaneHeader.TerminalBg
	}
	if c.theme != nil {
		return c.theme.TerminalBg
	}
	return c.bgDetector.GetDefaultTerminalBg()
}

// themedStyle returns a lipgloss style pre-populated with the coordinator's
// resolved terminal background, so any widget-rendered text paints bg on
// every cell it emits.
//
// Without this, widgets that only call .Foreground(...) leave trailing /
// inter-widget cells at "terminal default", which renders whatever
// window-style tmux happens to have at that instant -- which during a
// theme flip momentarily shows the OLD theme's color (the visible "after
// Feed bg goes light" / "after time block shifts" artifact).
//
// Use this as the starting style for any widget text: chain .Foreground(),
// .Bold() etc. on the returned value.
//
// If GetTerminalBg returns "" (no config, no theme, no detector), this
// falls back to lipgloss.NewStyle() with no bg -- same as before -- so
// the helper is always safe to use.
func (c *Coordinator) themedStyle() lipgloss.Style {
	s := lipgloss.NewStyle()
	if bg := c.GetTerminalBg(); bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	return s
}

// clampColorByte clamps an int to the valid 0-255 range for color channels.
func clampColorByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// computeDimBG computes the background color for inactive (dimmed) panes.
// For light terminal backgrounds the inactive pane is shifted toward white,
// making it appear washed-out relative to the active pane's richer color.
// For dark terminal backgrounds the inactive pane is shifted toward black,
// making it appear deeper/less prominent.
// opacity is the fraction of the original color retained (0.0–1.0); the rest
// is contributed by white (light themes) or black (dark themes).
// Returns a hex color string like "#1a1a1a", or "" if terminalBG is empty.
func computeDimBG(terminalBG string, opacity float64) string {
	if terminalBG == "" {
		return ""
	}
	hex := strings.TrimPrefix(terminalBG, "#")
	if len(hex) != 6 {
		return ""
	}
	tbR, _ := strconv.ParseInt(hex[0:2], 16, 32)
	tbG, _ := strconv.ParseInt(hex[2:4], 16, 32)
	tbB, _ := strconv.ParseInt(hex[4:6], 16, 32)

	lum := (int(tbR)*299 + int(tbG)*587 + int(tbB)*114) / 1000

	// target is white for light themes (shift inactive toward white = lighter/washed-out),
	// black for dark themes (shift inactive toward black = deeper/less prominent).
	var targetR, targetG, targetB int
	if lum >= 128 {
		targetR, targetG, targetB = 255, 255, 255
	} else {
		targetR, targetG, targetB = 0, 0, 0
	}

	inv := 1.0 - opacity
	dr := int(math.Round(float64(tbR)*opacity + float64(targetR)*inv))
	dg := int(math.Round(float64(tbG)*opacity + float64(targetG)*inv))
	db := int(math.Round(float64(tbB)*opacity + float64(targetB)*inv))
	return fmt.Sprintf("#%02x%02x%02x", clampColorByte(dr), clampColorByte(dg), clampColorByte(db))
}

// blendHexToward blends fg toward bg by ratio (0 = fg unchanged, 1 = bg).
// Returns fg unchanged if either input isn't a 6-digit hex.
func blendHexToward(fg, bg string, ratio float64) string {
	fHex := strings.TrimPrefix(fg, "#")
	bHex := strings.TrimPrefix(bg, "#")
	if len(fHex) != 6 || len(bHex) != 6 {
		return fg
	}
	fR, _ := strconv.ParseInt(fHex[0:2], 16, 32)
	fG, _ := strconv.ParseInt(fHex[2:4], 16, 32)
	fB, _ := strconv.ParseInt(fHex[4:6], 16, 32)
	bR, _ := strconv.ParseInt(bHex[0:2], 16, 32)
	bG, _ := strconv.ParseInt(bHex[2:4], 16, 32)
	bB, _ := strconv.ParseInt(bHex[4:6], 16, 32)
	inv := 1.0 - ratio
	dr := int(math.Round(float64(fR)*inv + float64(bR)*ratio))
	dg := int(math.Round(float64(fG)*inv + float64(bG)*ratio))
	db := int(math.Round(float64(fB)*inv + float64(bB)*ratio))
	return fmt.Sprintf("#%02x%02x%02x", clampColorByte(dr), clampColorByte(dg), clampColorByte(db))
}

// extractStyleColor pulls a color value for a key ("fg" or "bg") from a
// tmux style string like "fg=#56949f,bg=#56949f".
func extractStyleColor(style, key string) string {
	for _, part := range strings.Split(style, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

// dimSkipCommands are pane commands that should never have dim styles applied (sidebar).
var dimSkipCommands = []string{"sidebar-render"}

// dimHeaderCommand identifies pane-header processes.
const dimHeaderCommand = "pane-header"

func isDimSkip(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, s := range dimSkipCommands {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func isDimHeader(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), dimHeaderCommand)
}

func isDimUtility(cmd string) bool {
	return isDimSkip(cmd) || isDimHeader(cmd)
}

// isDimSkipPane checks both current and start command (post-consolidation safety).
func isDimSkipPane(p dimPaneInfo) bool {
	return isDimSkip(p.command) || isDimSkip(p.startCommand)
}

// isDimHeaderPane checks both current and start command.
func isDimHeaderPane(p dimPaneInfo) bool {
	return isDimHeader(p.command) || isDimHeader(p.startCommand)
}

// isDimUtilityPane checks both current and start command.
func isDimUtilityPane(p dimPaneInfo) bool {
	return isDimSkipPane(p) || isDimHeaderPane(p)
}

// dimPaneInfo holds per-pane data needed for dimming decisions.
type dimPaneInfo struct {
	id           string
	active       bool
	command      string // pane_current_command (may be "tabby" post-consolidation)
	startCommand string // pane_start_command (retains original invocation)
	left         int
}

// listDimPanes queries tmux for panes in the given window, returning info needed for dimming.
// We check both pane_current_command and pane_start_command because after binary consolidation
// all subcommand processes show "tabby" as pane_current_command; pane_start_command still
// contains the original invocation (e.g. "render sidebar", "render pane-header").
func listDimPanes(windowID string) []dimPaneInfo {
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
		"#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_left}\t#{pane_start_command}").Output()
	if err != nil {
		return nil
	}
	var panes []dimPaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		left, _ := strconv.Atoi(parts[3])
		startCmd := ""
		if len(parts) >= 5 {
			startCmd = parts[4]
		}
		panes = append(panes, dimPaneInfo{
			id:           parts[0],
			active:       parts[1] == "1",
			command:      parts[2],
			startCommand: startCmd,
			left:         left,
		})
	}
	return panes
}

// ApplyPaneDimming sets per-pane background styles on inactive content panes
// and desaturates inactive border colors. This replaces the cycle-pane --dim-only
// shell invocation with an in-process implementation on the daemon's fast path.
//
// Logic ported from cmd/cycle-pane/main.go applyDim().
func (c *Coordinator) ApplyPaneDimming(activeWindowID string) {
	// Check spawning guard — during pane creation, data is stale
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}

	cfg := c.GetConfig()
	if cfg == nil {
		return
	}

	panes := listDimPanes(activeWindowID)
	if len(panes) == 0 {
		return
	}

	if !cfg.PaneHeader.DimInactive {
		// Dim disabled — clear any leftover styles and dim flags
		for _, p := range panes {
			if !isDimSkipPane(p) {
				exec.Command("tmux", "set-option", "-p", "-u", "-t", p.id, "window-style").Run()
			}
			if !isDimUtilityPane(p) {
				exec.Command("tmux", "set-option", "-p", "-u", "-t", p.id, "@tabby_pane_dim").Run()
			}
		}
		// Reset border style to match active border (skip when mobile hide is active)
		if !c.borderStylingBlocked() {
			if out, err := exec.Command("tmux", "show-options", "-gqv", "pane-active-border-style").Output(); err == nil {
				if s := strings.TrimSpace(string(out)); s != "" {
					exec.Command("tmux", "set-option", "-g", "pane-border-style", s).Run()
				}
			}
		}
		return
	}

	// Build map: pane_left → whether the content pane at that column is active
	colActive := map[int]bool{}
	hasActiveContent := false
	for _, p := range panes {
		if !isDimUtilityPane(p) {
			colActive[p.left] = p.active
			if p.active {
				hasActiveContent = true
			}
		}
	}

	// If no content pane is active (utility pane has focus), keep existing styles
	if !hasActiveContent {
		return
	}

	termBG := c.GetTerminalBg()
	opacity := cfg.PaneHeader.DimOpacity
	if opacity <= 0 || opacity > 1 {
		opacity = 0.6
	}
	dimBG := computeDimBG(termBG, opacity)

	// Batch all per-pane set-option calls into a single tmux invocation via
	// the `;` separator. For a 2-pane window that drops 4 tmux execs (~20ms)
	// per tab switch down to 1. Skipped panes contribute nothing to the args.
	var argv []string
	addCmd := func(c ...string) {
		if len(argv) > 0 {
			argv = append(argv, ";")
		}
		argv = append(argv, c...)
	}
	for _, p := range panes {
		if isDimSkipPane(p) {
			continue
		}
		// For headers, use their content pane's active state (matched by pane_left)
		active := p.active
		if isDimHeaderPane(p) {
			active = colActive[p.left]
		}
		if isDimHeaderPane(p) {
			// Headers are rendered by the daemon — don't set window-style.
			// The daemon reads @tabby_pane_dim from the content pane to decide colors.
			continue
		}
		// Content pane: set window-style AND dim flag
		if active {
			addCmd("set-option", "-p", "-u", "-t", p.id, "window-style")
			addCmd("set-option", "-p", "-t", p.id, "@tabby_pane_dim", "0")
		} else {
			if dimBG == "" {
				addCmd("set-option", "-p", "-u", "-t", p.id, "window-style")
			} else {
				addCmd("set-option", "-p", "-t", p.id, "window-style", fmt.Sprintf("bg=%s", dimBG))
			}
			addCmd("set-option", "-p", "-t", p.id, "@tabby_pane_dim", "1")
		}
	}
	if len(argv) > 0 {
		exec.Command("tmux", argv...).Run()
	}

	// Dim borders: active = full color, inactive = desaturated
	c.applyBorderDim(opacity)
}

// applyBorderDim reads the global pane-active-border-style fg color and sets
// pane-border-style to a desaturated version so inactive borders look dimmed.
func (c *Coordinator) applyBorderDim(opacity float64) {
	if c.borderStylingBlocked() {
		return
	}
	// Native borders own pane-border-style window-level; the global dim setter
	// would just be overridden, but skipping spares the tmux round-trip.
	if c.config.PaneHeader.Native != nil && *c.config.PaneHeader.Native {
		return
	}
	out, err := exec.Command("tmux", "show-options", "-gqv", "pane-active-border-style").Output()
	if err != nil {
		return
	}
	styleStr := strings.TrimSpace(string(out))
	if styleStr == "" {
		return
	}

	fgColor := extractStyleColor(styleStr, "fg")
	if fgColor == "" {
		return
	}

	termBG := c.GetTerminalBg()
	dimFg := desaturateHex(fgColor, opacity, termBG)
	exec.Command("tmux", "set-option", "-g", "pane-border-style", "fg="+dimFg).Run()
}

// PreserveWindowNames locks automatic-rename for windows whose name contains "|"
// (group prefix). Prevents tmux from overwriting group names after pane splits.
// Replaces scripts/preserve_window_name.sh.
func (c *Coordinator) PreserveWindowNames() {
	c.stateMu.RLock()
	var toLock []string
	for _, w := range c.windows {
		if strings.Contains(w.Name, "|") {
			toLock = append(toLock, w.ID)
		}
	}
	c.stateMu.RUnlock()

	for _, id := range toLock {
		exec.Command("tmux", "set-window-option", "-t", id, "automatic-rename", "off").Run()
	}
}

// ApplyNewWindowGroup applies in-memory new-window group metadata to the
// in-flight/ready window. Replaces scripts/apply_new_window_group.sh.
func (c *Coordinator) ApplyNewWindowGroup() {
	status := c.NewWindowStatus()
	if status.State != "ready" || status.WindowID == "" {
		return
	}
	if status.Group == "" || status.Group == "Default" {
		return
	}
	exec.Command("tmux", "set-window-option", "-t", status.WindowID, "@tabby_group", status.Group).Run()
}

// EnforceStatusExclusivity ensures the tmux status bar is off when the sidebar
// is active and on when the sidebar is disabled. Replaces
// scripts/enforce_status_exclusivity.sh.
func (c *Coordinator) EnforceStatusExclusivity(sessionID string) {
	// Check spawning guard
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}

	// Read sidebar mode from tmux option
	mode := ""
	if out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_sidebar").Output(); err == nil {
		mode = strings.TrimSpace(string(out))
	}
	if mode == "" {
		if out, err := exec.Command("tmux", "show-options", "-qv", "@tabby_sidebar").Output(); err == nil {
			mode = strings.TrimSpace(string(out))
		}
	}

	// Cache the last applied "status on/off" decision per mode. Skip the
	// tmux set-option round trip when we'd be reapplying the same value —
	// this option only flips on user action (toggle sidebar / disable
	// tabby), so steady-state refreshes hit the cache and save 5–10ms.
	c.statusExclusivityMu.Lock()
	prevDecision := c.statusExclusivityDecision
	switch mode {
	case "disabled":
		if prevDecision == "status_on" {
			c.statusExclusivityMu.Unlock()
			return
		}
		c.statusExclusivityDecision = "status_on"
		c.statusExclusivityMu.Unlock()
		exec.Command("tmux", "set-option", "-g", "status", "on").Run()
		return
	case "enabled":
		if prevDecision == "status_off" {
			c.statusExclusivityMu.Unlock()
			return
		}
		c.statusExclusivityDecision = "status_off"
		c.statusExclusivityMu.Unlock()
		exec.Command("tmux", "set-option", "-g", "status", "off").Run()
		return
	}
	c.statusExclusivityMu.Unlock()

	// No explicit mode — check if tabby panes exist
	hasTabbyPanes := false
	if sessionID != "" {
		if out, err := exec.Command("tmux", "list-panes", "-s", "-t", sessionID, "-F",
			"#{pane_current_command}|#{pane_start_command}").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				lower := strings.ToLower(line)
				if strings.Contains(lower, "sidebar-renderer") || strings.Contains(lower, "sidebar") || strings.Contains(lower, "pane-header") {
					hasTabbyPanes = true
					break
				}
			}
		}
	}

	if hasTabbyPanes {
		exec.Command("tmux", "set-option", "-g", "status", "off").Run()
	} else {
		exec.Command("tmux", "set-option", "-g", "status", "on").Run()
	}
}

// HandleWindowSelect performs window-switch housekeeping that was previously
// done by scripts/on_window_select.sh:
//   - Clears @tabby_input and @tabby_bell indicators (user acknowledged notification)
//   - Updates global pane-active-border-style to match active window's tab color
//     when border_from_tab is enabled
//
// Called from signal_refresh when activeWindowID changes.
func (c *Coordinator) HandleWindowSelect(activeWindowID string) {
	// Check spawning guard
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}

	// Clear AI tool input/bell indicators for the active window
	exec.Command("tmux", "set-option", "-w", "-t", activeWindowID, "@tabby_input", "").Run()
	exec.Command("tmux", "set-option", "-w", "-t", activeWindowID, "@tabby_bell", "").Run()

	cfg := c.GetConfig()
	if cfg == nil || !cfg.PaneHeader.BorderFromTab {
		return
	}
	// Native border mode owns active-border-style per-window; skip the global
	// override here so it doesn't fight applyNativeBorders.
	if cfg.PaneHeader.Native != nil && *cfg.PaneHeader.Native {
		return
	}

	// Find active window's tab bg color from grouped state
	c.stateMu.RLock()
	var tabBg string
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == activeWindowID {
				tabBg = group.Theme.Bg
				if win.CustomColor != "" {
					tabBg = win.CustomColor
				}
				break
			}
		}
		if tabBg != "" {
			break
		}
	}
	c.stateMu.RUnlock()

	if tabBg != "" && !c.borderStylingBlocked() {
		exec.Command("tmux", "set-option", "-g", "pane-active-border-style", "fg="+tabBg).Run()
	}
}

func (c *Coordinator) BeginTransition(targetWindowID, reason, source string) error {
	targetWindowID = strings.TrimSpace(targetWindowID)
	reason = strings.TrimSpace(reason)
	source = strings.TrimSpace(source)
	if targetWindowID == "" {
		return fmt.Errorf("begin transition: target window ID is required")
	}
	if reason == "" {
		reason = "unspecified"
	}
	if source == "" {
		source = "unknown"
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.windowTransition.TargetWindowID != "" {
		return fmt.Errorf("begin transition: transition already in progress target=%s reason=%s source=%s", c.windowTransition.TargetWindowID, c.windowTransition.Reason, c.windowTransition.Source)
	}
	c.windowTransition = WindowTransition{
		TargetWindowID: targetWindowID,
		Reason:         reason,
		Source:         source,
		StartedAt:      time.Now(),
	}
	return nil
}

func (c *Coordinator) CompleteTransition() {
	c.stateMu.Lock()
	c.windowTransition = WindowTransition{}
	c.stateMu.Unlock()
}

func (c *Coordinator) IsTransitionInProgress() bool {
	c.stateMu.RLock()
	inProgress := c.windowTransition.TargetWindowID != ""
	c.stateMu.RUnlock()
	return inProgress
}

func (c *Coordinator) SelectWindow(targetWindowID, reason, source string) error {
	targetWindowID = strings.TrimSpace(targetWindowID)
	reason = strings.TrimSpace(reason)
	source = strings.TrimSpace(source)

	if targetWindowID == "" {
		return fmt.Errorf("select window: target window ID is required")
	}

	// If the dashboard is active and the user is navigating to (or clicking) a
	// different window, restore the gathered panes first so they land in a real
	// window rather than an empty husk. Selecting the dashboard window itself is
	// a no-op here. See dashboard.go.
	if err := c.BeginTransition(targetWindowID, reason, source); err != nil {
		return fmt.Errorf("select window: %w", err)
	}
	defer c.CompleteTransition()

	// Peek model: surface a parked minimized target (move it back into the session
	// so select-window can reach it) before selecting.
	c.surfaceForActivate(targetWindowID)

	if err := tmuxRun("select-window", "-t", targetWindowID); err != nil {
		if reason == "" {
			reason = "unspecified"
		}
		if source == "" {
			source = "unknown"
		}
		return fmt.Errorf("select window target=%s reason=%s source=%s: %w", targetWindowID, reason, source, err)
	}

	c.SetActiveWindowOptimistic(targetWindowID)
	c.TrackWindowHistory(targetWindowID)
	c.HandleWindowSelect(targetWindowID)
	// Re-park the previously-peeked window now that the client has moved off it.
	c.settlePeek(targetWindowID)
	return nil
}

// SaveWindowLayouts saves the current layout string for each window to tmux
// options (@tabby_layout_<WID>). Called on every signal_refresh after
// RefreshWindows so that tabby-hook preserve-pane-ratios has fresh data.
// Replaces the former save_pane_layout.sh hook.
// Skips during @tabby_spawning to avoid saving stale mid-creation layouts.
func (c *Coordinator) SaveWindowLayouts() {
	// Check spawning guard
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil {
		if strings.TrimSpace(string(out)) == "1" {
			return
		}
	}

	c.stateMu.RLock()
	windows := make([]struct{ id, layout string }, 0, len(c.windows))
	for _, w := range c.windows {
		if w.Layout != "" {
			windows = append(windows, struct{ id, layout string }{w.ID, w.Layout})
		}
	}
	c.stateMu.RUnlock()

	// Per-window cache so repeat refreshes with unchanged layouts skip the
	// tmux set-option round trip (~5–10ms per window). Layouts only change on
	// split / kill / resize-pane, so most refreshes are pure cache hits.
	c.layoutCacheMu.Lock()
	if c.layoutCache == nil {
		c.layoutCache = make(map[string]string)
	}
	c.layoutCacheMu.Unlock()
	for _, w := range windows {
		// Defense-in-depth: don't push a structurally malformed layout into
		// @tabby_layout_<wid>. preserve-pane-ratios reads that option and
		// replays it via select-layout on after-kill-pane, and a footer-squish
		// layout (window-header bar nested inside the sidebar split) would
		// propagate the corruption into the live window.
		if looksMalformedLayout(w.layout) {
			logEvent("LAYOUT_OPTION_SKIPPED windowID=%s reason=footer_squish layout=%s", w.id, w.layout)
			continue
		}
		c.layoutCacheMu.Lock()
		cached := c.layoutCache[w.id]
		if cached == w.layout {
			c.layoutCacheMu.Unlock()
			continue
		}
		c.layoutCache[w.id] = w.layout
		c.layoutCacheMu.Unlock()
		exec.Command("tmux", "set-option", "-g", fmt.Sprintf("@tabby_layout_%s", w.id), w.layout).Run()
	}
}

// TrackWindowHistory pushes windowID to the front of the history stack,
// removing any duplicate. Caps at 20 entries. Thread-safe.
// Replaces scripts/track_window_history.sh.
func (c *Coordinator) TrackWindowHistory(windowID string) {
	if windowID == "" {
		return
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	// Remove duplicate
	filtered := make([]string, 0, len(c.windowHistory))
	for _, id := range c.windowHistory {
		if id != windowID {
			filtered = append(filtered, id)
		}
	}

	// Prepend and cap at 20
	c.windowHistory = append([]string{windowID}, filtered...)
	if len(c.windowHistory) > 20 {
		c.windowHistory = c.windowHistory[:20]
	}
}

// SelectPreviousWindow finds the most recently visited window that still exists
// and selects it. Called when a window is closed to restore focus to the last
// visited window instead of tmux's default adjacent-window behavior.
// Replaces scripts/select_previous_window.sh.
func (c *Coordinator) SelectPreviousWindow() {
	c.stateMu.RLock()
	history := make([]string, len(c.windowHistory))
	copy(history, c.windowHistory)

	// Build set of existing window IDs
	existing := make(map[string]bool, len(c.windows))
	for _, w := range c.windows {
		existing[w.ID] = true
	}
	c.stateMu.RUnlock()

	// Find first surviving window in history
	for _, id := range history {
		if existing[id] {
			if err := c.SelectWindow(id, "select_previous_window", "window_close"); err != nil {
				logEvent("SELECT_PREVIOUS_WINDOW_ERR target=%s err=%v", id, err)
			}
			break
		}
	}

	// Clean up history: remove dead windows
	c.stateMu.Lock()
	cleaned := make([]string, 0, len(c.windowHistory))
	for _, id := range c.windowHistory {
		if existing[id] {
			cleaned = append(cleaned, id)
		}
	}
	c.windowHistory = cleaned
	c.stateMu.Unlock()
}

// GetWindowHistory returns a copy of the current window history (for testing).
func (c *Coordinator) GetWindowHistory() []string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	result := make([]string, len(c.windowHistory))
	copy(result, c.windowHistory)
	return result
}

// getDividerFg returns divider color from config, theme, or detector
func (c *Coordinator) getDividerFg() string {
	if c.config.PaneHeader.DividerFg != "" {
		return c.config.PaneHeader.DividerFg
	}
	if c.theme != nil {
		return c.theme.DividerFg
	}
	return c.bgDetector.GetDefaultDividerFg()
}

// getWidgetFg returns widget text color from theme or detector
func (c *Coordinator) getWidgetFg() string {
	if c.theme != nil {
		return c.theme.WidgetFg
	}
	return c.bgDetector.GetDefaultWidgetFg()
}

// getPromptFg returns prompt text color from config, theme, or detector
func (c *Coordinator) getPromptFg() string {
	if c.config.Prompt.Fg != "" {
		return c.config.Prompt.Fg
	}
	if c.theme != nil {
		return c.theme.PromptFg
	}
	return c.bgDetector.GetDefaultPromptFg()
}

// getPromptBg returns prompt background color from config, theme, or detector
func (c *Coordinator) getPromptBg() string {
	if c.config.Prompt.Bg != "" {
		return c.config.Prompt.Bg
	}
	if c.theme != nil {
		return c.theme.PromptBg
	}
	return c.bgDetector.GetDefaultPromptBg()
}

// getMainPaneDirection returns the tmux select-pane flag to navigate
// from the sidebar pane to the main content pane.
// If sidebar is on the left, main pane is to the right (-R).
// If sidebar is on the right, main pane is to the left (-L).
func (c *Coordinator) getMainPaneDirection() string {
	if c.config.Sidebar.Position == "right" {
		return "-L"
	}
	return "-R"
}

// loadCollapsedGroups loads collapsed state from tmux options.
// Runs all tmux queries BEFORE acquiring stateMu to avoid holding the lock
// during external I/O.
func (c *Coordinator) loadCollapsedGroups() {
	// Phase 1: Query tmux for legacy format (outside lock)
	legacyOut, legacyErr := tmuxOutputCtx("show-options", "-v", "-q", "@tabby_collapsed_groups")

	var legacyGroups []string
	useLegacy := false
	if legacyErr == nil && len(legacyOut) > 0 {
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(legacyOut))), &legacyGroups); err == nil {
			useLegacy = true
		}
	}

	if useLegacy {
		// Legacy migration path: assign under lock, then save+unset outside lock.
		c.stateMu.Lock()
		c.collapsedGroups = make(map[string]bool)
		for _, g := range legacyGroups {
			c.collapsedGroups[g] = true
		}
		c.stateMu.Unlock()
		// Migrate: save in new format and remove legacy option (outside lock).
		c.saveCollapsedGroups()
		tmuxRun("set-option", "-u", "@tabby_collapsed_groups")
		return
	}

	// Phase 2: Build group names to check (need lock for config/grouped reads)
	c.stateMu.RLock()
	groupsToCheck := make(map[string]bool)
	for _, group := range c.config.Groups {
		groupsToCheck[group.Name] = true
	}
	for _, gw := range c.grouped {
		groupsToCheck[gw.Name] = true
	}
	groupsToCheck["Default"] = true
	c.stateMu.RUnlock()

	// Phase 3: Query tmux for each group's collapsed state (outside lock)
	collapsedResults := make(map[string]bool)
	for groupName := range groupsToCheck {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
		out, err := tmuxOutputCtx("show-options", "-v", "-q", optName)
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			collapsedResults[groupName] = true
		}
	}

	// Phase 4: Assign results under lock (minimal critical section)
	c.stateMu.Lock()
	c.collapsedGroups = collapsedResults
	c.stateMu.Unlock()
}

// saveCollapsedGroups saves collapsed state to tmux options
func (c *Coordinator) saveCollapsedGroups() {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	c.saveCollapsedGroupsLocked()
}

// saveCollapsedGroupsLocked saves collapsed state (caller must hold stateMu)
func (c *Coordinator) saveCollapsedGroupsLocked() {
	// Build a set of all known group names (from config + current grouped windows)
	knownGroups := make(map[string]bool)
	for _, group := range c.config.Groups {
		knownGroups[group.Name] = true
	}
	for _, gw := range c.grouped {
		knownGroups[gw.Name] = true
	}
	knownGroups["Default"] = true

	// Save collapsed state for ALL known groups
	// This ensures we don't lose state for dynamically created groups
	for groupName := range knownGroups {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
		if c.collapsedGroups[groupName] {
			exec.Command("tmux", "set-option", optName, "1").Run()
		} else {
			exec.Command("tmux", "set-option", "-u", optName).Run()
		}
	}

	// Also save any collapsed groups that aren't in knownGroups (edge case)
	for groupName := range c.collapsedGroups {
		if !knownGroups[groupName] {
			optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
			exec.Command("tmux", "set-option", optName, "1").Run()
		}
	}
}

// getCollapsedGroupsJSON returns the collapsed groups as a JSON array string.
func (c *Coordinator) getCollapsedGroupsJSON() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	names := make([]string, 0, len(c.collapsedGroups))
	for name := range c.collapsedGroups {
		names = append(names, name)
	}
	if len(names) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(names)
	return string(data)
}

// petStatePath returns the path to the shared pet state file
func petStatePath() string {
	paths.EnsureStateDir()
	return paths.StatePath("pet.json")
}

func cwdColorsPath() string {
	paths.EnsureStateDir()
	return paths.StatePath("cwd-colors.json")
}

// loadPetState loads pet state from disk (used once at startup for persistence across restarts).
func (c *Coordinator) loadPetState() {
	data, err := os.ReadFile(petStatePath())
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.pet)
	// Don't resume in-progress adventures after restart — PhaseStart times
	// are stale and would cause every phase to immediately complete.
	if c.pet.Adventure.Active {
		c.pet.Adventure = adventureState{
			TotalCatches: c.pet.Adventure.TotalCatches,
		}
		if c.pet.State == "walking" || c.pet.State == "jumping" {
			c.pet.State = "idle"
		}
	}
}

// triggerPetEventThought fires a fresh LLM reaction to a pet EVENT (poop,
// cleaned) in the background and, when it returns, promotes it to the pet's
// current thought. The caller sets a canned line first (instant fallback); this
// UPGRADES it to a unique LLM line a moment later. The pet context is built
// synchronously here — callers MUST hold stateMu — and passed to the goroutine as
// a string, so the goroutine never touches shared pet state until it re-locks to
// write LastThought.
func (c *Coordinator) triggerPetEventThought(event string) {
	if llmClient == nil {
		return
	}
	name := c.config.Widgets.Pet.Name
	petContext := buildPetContext(&c.pet) // safe: caller holds stateMu
	go func() {
		thought := generateEventThought(name, event, petContext)
		if thought == "" {
			return
		}
		c.stateMu.Lock()
		c.pet.LastThought = thought
		c.pet.ThoughtScroll = 0
		c.stateMu.Unlock()
	}()
}

// petOwnerPath is the single-writer lock for the global pet.json.
func petOwnerPath() string {
	paths.EnsureStateDir()
	return paths.StatePath("pet.owner")
}

const petOwnershipTTL = 30 * time.Second

// acquirePetOwnership returns whether this daemon may WRITE the shared pet.json,
// refreshing its lease when it may. pet.json is one global file but each tmux
// session runs its own daemon; letting them all tick+save it means a stale/older
// daemon perpetually overwrites hunger back to its own state (the "food resets on
// reboot" bug). A TTL lock elects one writer; a lease older than the TTL is stale
// and reclaimable, so a crashed owner frees the pet automatically.
func (c *Coordinator) acquirePetOwnership(now time.Time) bool {
	me := c.sessionID
	if data, err := os.ReadFile(petOwnerPath()); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			owner := fields[0]
			exp, _ := strconv.ParseInt(fields[1], 10, 64)
			if owner != me && now.Unix() < exp {
				return false // a different daemon holds a live lease
			}
		}
	}
	os.WriteFile(petOwnerPath(), []byte(fmt.Sprintf("%s %d", me, now.Add(petOwnershipTTL).Unix())), 0644)
	return true
}

// petWriteAllowed gates savePetStateData at the process level: 1 = this daemon
// currently owns the pet and may write pet.json, 0 = it must not (another daemon
// owns it). Set atomically from UpdatePetState each tick and by stealPetOwnership,
// and read inside savePetStateData so EVERY save site is gated with no per-call
// changes. Avoids a data race on the ownership decision across the tick loop and
// the socket-action goroutines.
var petWriteAllowed int32 = 1

// stealPetOwnership forces this daemon to become the pet writer. Called on user
// pet actions (feed/clean/play) so the session the user is actually interacting
// with always persists — even if another daemon currently holds the lease.
func (c *Coordinator) stealPetOwnership() {
	os.WriteFile(petOwnerPath(), []byte(fmt.Sprintf("%s %d", c.sessionID, time.Now().Add(petOwnershipTTL).Unix())), 0644)
	atomic.StoreInt32(&petWriteAllowed, 1)
}

// loadPersistentPetStats refreshes only the SHARED persistent fields (hunger,
// happiness, lifetime totals, poop, death, decay anchors) from disk into c.pet,
// leaving this daemon's local animation state (position, current action) intact.
// A non-owner calls this each tick so its sidebar shows the owner's real stats.
func (c *Coordinator) loadPersistentPetStats() {
	data, err := os.ReadFile(petStatePath())
	if err != nil {
		return
	}
	var d petState
	if json.Unmarshal(data, &d) != nil {
		return
	}
	c.pet.Hunger = d.Hunger
	c.pet.Happiness = d.Happiness
	c.pet.TotalPets = d.TotalPets
	c.pet.TotalFeedings = d.TotalFeedings
	c.pet.TotalPoopsCleaned = d.TotalPoopsCleaned
	c.pet.TotalYarnPlays = d.TotalYarnPlays
	c.pet.TotalMouseCatches = d.TotalMouseCatches
	c.pet.PoopPositions = d.PoopPositions
	c.pet.LastFed = d.LastFed
	c.pet.LastPet = d.LastPet
	c.pet.LastPoop = d.LastPoop
	c.pet.IsDead = d.IsDead
	c.pet.DeathTime = d.DeathTime
	c.pet.StarvingStart = d.StarvingStart
	c.pet.LastHungerTick = d.LastHungerTick
	c.pet.LastHappyTick = d.LastHappyTick
}

// savePetStateData saves the given pet state snapshot to the shared file.
// Safe to call without holding stateMu since it only writes the provided data.
// No-op when this daemon doesn't own the pet (petWriteAllowed==0), so a non-owner
// daemon's ticks/animations never clobber the owning daemon's stats.
func savePetStateData(pet petState) {
	if atomic.LoadInt32(&petWriteAllowed) == 0 {
		return
	}
	data, _ := json.Marshal(pet)
	os.WriteFile(petStatePath(), data, 0644)
}

// savePetState saves the pet state to the shared file.
// Caller must NOT hold stateMu — this performs file I/O.
// For call sites that hold stateMu, snapshot c.pet first, unlock, then call savePetStateData().
func (c *Coordinator) savePetState() {
	data, _ := json.Marshal(c.pet)
	os.WriteFile(petStatePath(), data, 0644)
}

func (c *Coordinator) loadCWDColors() {
	data, err := os.ReadFile(cwdColorsPath())
	if err != nil {
		return
	}

	loaded := make(map[string]CWDColorMapping)
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}

	// CWDColorMapping no longer has Name/NameSource, so json.Unmarshal silently
	// drops any legacy "name"/"nameSource" keys: the loaded map is already
	// name-free. Detect whether the on-disk file still carried those keys and, if
	// so, rewrite it once to purge the legacy data (and drop entries left empty
	// once their name is gone). One-time, idempotent — a clean file rewrites to
	// itself and is skipped.
	migrated := false
	if cwdColorsHasLegacyNameFields(data) {
		migrated = true
	}

	// Color/Icon ARE remembered per directory again — as a "last used" appearance
	// that seeds a future NEW window in the same dir/host (never a per-refresh
	// repaint; see captureCWDAppearance / seedWindowAppearance). So they are
	// loaded verbatim and must survive daemon restarts, exactly like group/pinned.
	// Drop only entries that carry nothing at all (defensive; empties are already
	// pruned on write).
	for k, m := range loaded {
		if cwdMappingEmpty(m) {
			delete(loaded, k)
		}
	}

	c.cwdColorsMu.Lock()
	c.cwdColors = loaded
	c.cwdColorsMu.Unlock()

	if migrated {
		logEvent("CWDCOLORS_MIGRATE_DROP_NAMES entries=%d", len(loaded))
		c.saveCWDColors()
	}
}

// cwdColorsHasLegacyNameFields reports whether the raw cwd-colors.json bytes
// still contain the retired per-directory name fields. A plain substring scan is
// enough: the keys are fixed JSON object keys we control, and a false positive
// (e.g. a directory literally named "name") only triggers a harmless idempotent
// rewrite.
func cwdColorsHasLegacyNameFields(data []byte) bool {
	return strings.Contains(string(data), "\"name\"") ||
		strings.Contains(string(data), "\"nameSource\"")
}

func (c *Coordinator) saveCWDColors() {
	c.cwdColorsMu.RLock()
	cloned := make(map[string]CWDColorMapping, len(c.cwdColors))
	for k, v := range c.cwdColors {
		cloned[k] = v
	}
	c.cwdColorsMu.RUnlock()

	data, err := json.MarshalIndent(cloned, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(cwdColorsPath(), data, 0644)
}

func normalizeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return filepath.Clean(cwd)
}

// daemonHomeDir is the user's home directory (normalized), resolved once. A
// window whose content pane runs directly in $HOME has no project identity:
// keying it on $HOME would make every such window (e.g. a Claude Code session
// started from the home dir) collide on one shared name/color/lock — the
// "$HOME trap". Such windows are treated as keyless so they flow into the
// per-window AI-summary path instead.
var daemonHomeDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return normalizeCWD(h)
}()

// cwdIsHome reports whether cwd is the given home directory (both normalized).
// Pure form for testability; isHomeDir wraps it with the resolved daemonHomeDir.
func cwdIsHome(cwd, home string) bool {
	if cwd == "" || home == "" {
		return false
	}
	return normalizeCWD(cwd) == normalizeCWD(home)
}

func isHomeDir(cwd string) bool {
	return cwdIsHome(cwd, daemonHomeDir)
}

// firstPaneCWD returns the working directory of the window's first CONTENT pane,
// skipping Tabby's own auxiliary panes (the per-window sidebar renderer / header).
// Those aux panes run from $HOME, so using Panes[0] blindly would make every
// per-directory feature (color/icon/name mappings, tab abbreviation) key off
// $HOME instead of the project directory. Falls back to the first pane if no
// content pane is found.
func firstPaneCWD(win tmux.Window) string {
	for i := range win.Panes {
		if isAuxiliaryPane(win.Panes[i]) {
			continue
		}
		if cwd := normalizeCWD(win.Panes[i].CurrentPath); cwd != "" {
			return cwd
		}
	}
	if len(win.Panes) > 0 {
		return normalizeCWD(win.Panes[0].CurrentPath)
	}
	return ""
}

// remoteCWDSep separates the host from the topmost dir in the @tabby_remote_cwd
// payload the remote-cwd shell hook reports (an ASCII Unit Separator, chosen so
// it can't collide with anything in a hostname or path).
const remoteCWDSep = "\x1f"

// firstPaneRemoteCWD returns the (host, topmost) reported by the remote-cwd hook
// for the window's first CONTENT pane carrying a @tabby_remote_cwd value,
// skipping Tabby's auxiliary panes. ok is false when no pane has reported one
// yet (e.g. the hook isn't installed on the remote, or it hasn't fired since the
// pane connected). The payload format is "host\x1ftopmost".
func firstPaneRemoteCWD(win tmux.Window) (host, topmost string, ok bool) {
	for i := range win.Panes {
		if isAuxiliaryPane(win.Panes[i]) {
			continue
		}
		raw := strings.TrimSpace(win.Panes[i].RemoteCWD)
		if raw == "" {
			continue
		}
		parts := strings.SplitN(raw, remoteCWDSep, 2)
		if len(parts) != 2 {
			continue
		}
		h := strings.TrimSpace(parts[0])
		t := strings.TrimSpace(parts[1])
		if h == "" || t == "" {
			continue
		}
		return h, t, true
	}
	return "", "", false
}

// gitToplevel returns the git repository root containing cwd, or "" when cwd
// isn't inside a repo. Results are memoized in gitTopCache (a cwd's toplevel is
// effectively immutable, so no invalidation is needed) to avoid forking `git`
// on every refresh. Safe to call from any goroutine; uses gitTopMu, never
// stateMu.
func (c *Coordinator) gitToplevel(cwd string) string {
	if cwd == "" {
		return ""
	}
	c.gitTopMu.Lock()
	if top, ok := c.gitTopCache[cwd]; ok {
		c.gitTopMu.Unlock()
		return top
	}
	c.gitTopMu.Unlock()

	top := ""
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	cancel()
	if err == nil {
		top = normalizeCWD(strings.TrimSpace(string(out)))
	}

	c.gitTopMu.Lock()
	c.gitTopCache[cwd] = top
	c.gitTopMu.Unlock()
	return top
}

// windowNameKey returns the canonical key under which a window's persisted tab
// name is stored, keyed on the PROJECT ROOT (topmost dir) so a name set at a
// repo root is reused across its subdirectories and future sessions:
//
//   - Remote (ssh/mosh) window whose remote-cwd hook has reported in: keyed
//     "ssh://<host><remote-topmost>". This is distinct from any local path key,
//     so remote and local projects never collide.
//   - Local window: keyed on the git toplevel of the first content pane's cwd,
//     falling back to the cwd itself when it isn't inside a repo.
//
// ok is false when no usable key can be derived (no content cwd yet, or a remote
// window whose hook hasn't reported) — callers skip persisting/applying a name
// in that case rather than keying on a misleading local ssh-launch path.
func (c *Coordinator) windowNameKey(win tmux.Window) (string, bool) {
	if win.RemoteHost != "" || hasRemoteContentPane(win) {
		if host, topmost, ok := firstPaneRemoteCWD(win); ok {
			return "ssh://" + host + topmost, true
		}
		// Remote window but the hook hasn't reported a cwd yet: don't fall back
		// to the local ssh-launch path (every ssh tab launched from $HOME would
		// collide on it). Wait until the hook reports.
		return "", false
	}
	cwd := firstPaneCWD(win)
	if cwd == "" {
		return "", false
	}
	if isHomeDir(cwd) {
		// $HOME has no project identity — keyless, so this window is named
		// per-window via the AI summary rather than sharing one $HOME name.
		// Checked before gitToplevel so a $HOME that is itself a git repo
		// (dotfiles) still doesn't become a shared key.
		return "", false
	}
	if top := c.gitToplevel(cwd); top != "" {
		return top, true
	}
	return cwd, true
}

// hasRemoteContentPane reports whether any of the window's content panes is
// running a remote connection (ssh/mosh). Mirrors firstPaneCWD's aux-pane skip.
func hasRemoteContentPane(win tmux.Window) bool {
	for i := range win.Panes {
		if isAuxiliaryPane(win.Panes[i]) {
			continue
		}
		if win.Panes[i].Remote {
			return true
		}
	}
	return false
}

// parseAbbreviations turns config entries written "CODE>Folder" into a
// folder-basename -> CODE lookup. Folder keys are lower-cased so matching is
// case-insensitive (config "TBY>Tabby" matches a tabby/ or TABBY/ directory).
// Malformed or empty entries are skipped.
func parseAbbreviations(entries []string) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		parts := strings.SplitN(e, ">", 2)
		if len(parts) != 2 {
			continue
		}
		code := strings.TrimSpace(parts[0])
		folder := strings.TrimSpace(parts[1])
		if code == "" || folder == "" {
			continue
		}
		m[strings.ToLower(folder)] = code
	}
	return m
}

// dirAbbreviation returns the configured short code for a directory's folder
// name, if any. Safe to call from the render path: it reads c.config (written
// only under stateMu, which the render path holds as RLock) and guards its
// derived cache with tabAbbrevMu — never stateMu — so there is no lock cycle.
func (c *Coordinator) dirAbbreviation(folder string) (string, bool) {
	if folder == "" {
		return "", false
	}
	cfg := c.config
	if cfg == nil {
		return "", false
	}

	c.tabAbbrevMu.Lock()
	if c.tabAbbrevCfg != cfg {
		c.tabAbbrevMap = parseAbbreviations(cfg.TabNames.Abbreviations)
		c.tabProjMap = parseAbbreviations(cfg.AI.TabSummary.ProjectNames)
		c.tabAbbrevCfg = cfg
	}
	code, ok := c.tabAbbrevMap[strings.ToLower(folder)]
	c.tabAbbrevMu.Unlock()
	return code, ok
}

// projectNameCode returns the configured ai.tab_summary.project_names prefix for
// a directory's folder name, if any. This is the same abbreviation the summary
// pass prepends to a tab's work summary (e.g. "tby", "gp"), so using it for the
// no-summary fallback label keeps the prefix visually stable as a fresh tab
// upgrades from bare code to "code + summary". Shares dirAbbreviation's cache.
func (c *Coordinator) projectNameCode(folder string) (string, bool) {
	if folder == "" {
		return "", false
	}
	cfg := c.config
	if cfg == nil {
		return "", false
	}
	c.tabAbbrevMu.Lock()
	if c.tabAbbrevCfg != cfg {
		c.tabAbbrevMap = parseAbbreviations(cfg.TabNames.Abbreviations)
		c.tabProjMap = parseAbbreviations(cfg.AI.TabSummary.ProjectNames)
		c.tabAbbrevCfg = cfg
	}
	code, ok := c.tabProjMap[strings.ToLower(folder)]
	c.tabAbbrevMu.Unlock()
	return code, ok
}

// windowProjectBasename resolves the human-facing folder name for a window's
// dir code: the basename of the first CONTENT pane's working directory (the
// remote topmost dir for an ssh/mosh window). Returns "" for a $HOME / root /
// unresolved window (no project identity — labeled per-window by its live
// summary instead).
//
// This is the LEAF working directory, deliberately NOT the git toplevel: the
// user works in (and wants the tab to name) the actual directory they are in.
// Collapsing to the git root mislabels worktrees and subdirs — e.g. a session in
// `<repo>/.claude/worktrees/publications-phase1/imgen` should read "imgen", not
// the worktree-root's configured abbreviation. It also matches the window name
// that syncWindowNames derives from the same cwd.
func (c *Coordinator) windowProjectBasename(win tmux.Window) string {
	if _, topmost, ok := firstPaneRemoteCWD(win); ok {
		if b := filepath.Base(topmost); b != "" && b != "." && b != "/" {
			return b
		}
		return ""
	}
	cwd := firstPaneCWD(win)
	if cwd == "" || isHomeDir(cwd) {
		return ""
	}
	if b := filepath.Base(cwd); b != "" && b != "." && b != "/" {
		return b
	}
	return ""
}

// windowDirCode returns the deterministic project prefix for a window: the
// configured project_names abbreviation, else the tab_names.abbreviations
// override, else an auto-derived code (abbreviateFolder) — all derived from the
// window's resolved PROJECT DIRECTORY, never from win.Name (a tmux
// automatic-rename artifact that can be a stale/unrelated label).
//
// Fallback: when no project directory resolves (a $HOME window, or one with no
// content cwd), abbreviate the window name instead. Returns "" only for a
// home/root/unresolved name so such a window shows no prefix.
func (c *Coordinator) windowDirCode(win tmux.Window) string {
	if base := c.windowProjectBasename(win); base != "" {
		if code, ok := c.projectNameCode(base); ok && code != "" {
			return code
		}
		return c.tabAbbreviation(base)
	}
	name := win.Name
	if isRawWindowID(name) {
		name = "" // automatic-rename hasn't fired — no name to abbreviate
	}
	return c.abbreviateWindowName(name)
}

// composeTabBaseName builds a window's inline sidebar label from three
// independent, non-persisted signals (see CWDColorMapping for why nothing is
// persisted by directory):
//
//  1. Manual rename (win.NameLocked, on @tabby_name_locked) — shown verbatim.
//  2. Deterministic project code (windowDirCode, from the resolved DIRECTORY —
//     project_names / abbreviations / auto), the stable prefix.
//  3. Live AI work summary (@tabby_ai_title) — the per-window task topic.
//
// Composition: a locked name wins. Otherwise the label is "CODE summary" (e.g.
// "tby reload config"), or just "CODE" before the first summary, or just the
// summary for AI-tool windows in ai_summary_only mode. $HOME / unresolved
// windows (no code) fall back to the plain window name. The render may word-wrap
// this across up to MaxLines rows; it does NOT apply SSH-host or icon decoration.
func (c *Coordinator) composeTabBaseName(win tmux.Window) string {
	name := win.Name
	if isRawWindowID(name) {
		name = "~" // automatic-rename hasn't fired yet
	}
	summary := strings.TrimSpace(win.AITitle)

	// 1. A hard user rename is the strongest signal: show it verbatim. (A generic
	//    launcher stub that somehow carries a lock is ignored — it falls through
	//    to the deterministic code; applyCWDIdentityMappings also clears it.)
	if win.NameLocked && !isGenericTabName(win.Name) {
		return name
	}

	dirCode := c.windowDirCode(win)

	// 2. A live AI work summary is present: it is the per-window task topic.
	if summary != "" {
		// AI-tool windows (e.g. Claude Code) in ai_summary_only mode show just the
		// topic, with no project prefix.
		if c.config.AI.TabSummary.AISummaryOnly && isAIWindow(win) {
			return summary
		}
		if dirCode != "" {
			return dirCode + " " + summary
		}
		return summary
	}

	// 3. No summary yet (fresh tab, auto_generate off, or LLM unavailable): show
	//    the deterministic project code. Upgrades to "CODE summary" next tick.
	if dirCode != "" {
		return dirCode
	}

	// 4. $HOME / unresolved: fall back to the plain window name.
	return name
}

// isAIWindow reports whether any of the window's content panes runs an AI tool
// (e.g. Claude Code, which IsAITool detects via its semver process name).
func isAIWindow(win tmux.Window) bool {
	for i := range win.Panes {
		if isAuxiliaryPane(win.Panes[i]) {
			continue
		}
		if tmux.IsAITool(win.Panes[i].Command) {
			return true
		}
	}
	return false
}

// wrapTabLabel word-wraps a tab's line-1 text (already including the "N. "
// prefix and any icon) into up to maxLines display lines: the first sized to
// line1Width, continuation lines to contWidth. Whole words are kept together;
// an over-long word is hard-split. If content remains after maxLines, the last
// line ends with "~". Always returns at least one line.
func wrapTabLabel(text string, line1Width, contWidth, maxLines int) []string {
	if maxLines < 1 {
		maxLines = 1
	}
	if text == "" {
		return []string{""}
	}
	budgetFor := func(lineIdx int) int {
		w := contWidth
		if lineIdx == 0 {
			w = line1Width
		}
		if w < 1 {
			w = 1
		}
		return w
	}

	// Wrap at the CHARACTER, not the word: fill each line rune-by-rune up to its
	// budget, then break — even mid-word. A space that would land at the start of a
	// wrapped line is dropped so continuation rows don't begin with a blank.
	var lines []string
	cur := ""
	for _, r := range text {
		if lipgloss.Width(cur+string(r)) > budgetFor(len(lines)) && cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
		if cur == "" && r == ' ' {
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if len(lines) <= maxLines {
		return lines
	}

	// Overflow: keep the first maxLines lines, mark the last with "~".
	kept := lines[:maxLines]
	last := kept[maxLines-1]
	b := budgetFor(maxLines - 1)
	for lipgloss.Width(last) > b-1 {
		r := []rune(last)
		if len(r) == 0 {
			break
		}
		last = string(r[:len(r)-1])
	}
	kept[maxLines-1] = last + "~"
	return kept
}

func (c *Coordinator) getCWDColorMapping(cwd string) (CWDColorMapping, bool) {
	normalized := normalizeCWD(cwd)
	if normalized == "" {
		return CWDColorMapping{}, false
	}

	c.cwdColorsMu.RLock()
	mapping, ok := c.cwdColors[normalized]
	c.cwdColorsMu.RUnlock()
	return mapping, ok
}

// cwdMappingEmpty reports whether a per-dir record carries no remembered state
// and can be dropped from the map entirely. Used by every setter so that, e.g.,
// resetting a directory's color does not also discard a saved tab name.
func cwdMappingEmpty(m CWDColorMapping) bool {
	return strings.TrimSpace(m.Color) == "" &&
		strings.TrimSpace(m.Icon) == "" &&
		strings.TrimSpace(m.Group) == "" &&
		!m.Pinned
}

// captureCWDIdentity records a window's group + pinned state under the given
// project key so it can be restored to future windows in the same directory. key
// is a windowNameKey result (a local git-toplevel path or an "ssh://host/topmost"
// string), NOT a raw pane cwd. It is called every refresh, so it only writes the
// map + persists to disk when something actually changed — steady state does no
// I/O. Color/Icon for the key are left untouched.
//
// NOTE: tab names are intentionally not captured here (see CWDColorMapping).
func (c *Coordinator) captureCWDIdentity(key, group string, pinned bool) {
	normalized := normalizeCWD(key)
	if normalized == "" {
		return
	}
	group = strings.TrimSpace(group)

	c.cwdColorsMu.Lock()
	mapping := c.cwdColors[normalized]
	if mapping.Group == group && mapping.Pinned == pinned {
		c.cwdColorsMu.Unlock()
		return // no change — skip the disk write
	}
	mapping.Group = group
	mapping.Pinned = pinned
	c.cwdColors[normalized] = mapping
	c.cwdColorsMu.Unlock()

	c.saveCWDColors()
}

// captureCWDAppearance records a window's chosen color + marker under the given
// project key so a FUTURE window opened in the same directory (or ssh host+dir)
// can seed its appearance from it. key is a windowNameKey result. Like
// captureCWDIdentity it is diff-guarded — steady state does no disk I/O — and it
// touches only the Color/Icon fields, leaving Group/Pinned intact. Passing empty
// strings clears the remembered appearance (e.g. after the user resets a color),
// which matches the "last used wins" semantic; if that empties the whole record
// the entry is dropped.
//
// NOTE: this is a "last used" cache, NOT the old blanket per-dir restore that
// bled a color onto every sibling window every refresh. The remembered value is
// only ever SEEDED once onto a brand-new window (see the seed pass in
// applyCWDIdentityMappings); it never repaints a window that already exists.
func (c *Coordinator) captureCWDAppearance(key, color, icon string) {
	normalized := normalizeCWD(key)
	if normalized == "" {
		return
	}
	color = strings.TrimSpace(color)
	icon = strings.TrimSpace(icon)

	c.cwdColorsMu.Lock()
	mapping := c.cwdColors[normalized]
	if mapping.Color == color && mapping.Icon == icon {
		c.cwdColorsMu.Unlock()
		return // no change — skip the disk write
	}
	mapping.Color = color
	mapping.Icon = icon
	if cwdMappingEmpty(mapping) {
		delete(c.cwdColors, normalized)
	} else {
		c.cwdColors[normalized] = mapping
	}
	c.cwdColorsMu.Unlock()

	c.saveCWDColors()
}

// getWindowByID returns the tracked window with the given stable tmux window ID.
// Used to resolve a window's project key when a color/marker is set on it.
func (c *Coordinator) getWindowByID(windowID string) (tmux.Window, bool) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for _, win := range c.windows {
		if win.ID == windowID {
			return win, true
		}
	}
	return tmux.Window{}, false
}

// clearCWDIdentity forgets a directory's saved group/pinned state, leaving any
// color/icon mapping intact. If nothing remains, the entry is dropped entirely.
// Backs the "Unlock Name" action: with names no longer persisted, this only
// clears the group/pinned record (the window's own @tabby_name_locked is dropped
// separately by the caller).
func (c *Coordinator) clearCWDIdentity(cwd string) {
	normalized := normalizeCWD(cwd)
	if normalized == "" {
		return
	}

	c.cwdColorsMu.Lock()
	mapping, ok := c.cwdColors[normalized]
	if !ok || (mapping.Group == "" && !mapping.Pinned) {
		c.cwdColorsMu.Unlock()
		return // nothing to clear — skip the disk write
	}
	mapping.Group = ""
	mapping.Pinned = false
	if cwdMappingEmpty(mapping) {
		delete(c.cwdColors, normalized)
	} else {
		c.cwdColors[normalized] = mapping
	}
	c.cwdColorsMu.Unlock()

	c.saveCWDColors()
}

// getWindowFirstPaneCWDByID resolves the first-pane CWD for the window with the
// given stable tmux window ID (e.g. "@123"). Keyed on ID, not index, so it stays
// correct even if window indices have shifted since the caller captured the ID.
func (c *Coordinator) getWindowFirstPaneCWDByID(windowID string) string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for _, win := range c.windows {
		if win.ID == windowID {
			return firstPaneCWD(win)
		}
	}
	return ""
}

func (c *Coordinator) getActiveWindowFirstPaneCWD() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for _, win := range c.windows {
		if win.Active {
			return firstPaneCWD(win)
		}
	}
	return ""
}

func (c *Coordinator) resolveWindowCWDByID(windowID string) string {
	cwd := c.getWindowFirstPaneCWDByID(windowID)
	if cwd != "" {
		return cwd
	}
	return c.getActiveWindowFirstPaneCWD()
}

func (c *Coordinator) setWindowColor(windowID, color string) {
	trimmedColor := strings.TrimSpace(color)
	if trimmedColor == "" {
		return
	}

	// tmux accepts the stable window ID ("@123") as a -t target directly.
	// The color lives on this window's own @tabby_color option (for the
	// window's lifetime).
	exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_color", trimmedColor).Run()

	// Remember the color as this project's "last used" appearance so a future
	// NEW window in the same directory (or ssh host+dir) can seed from it. This
	// is a one-time seed, never a per-refresh repaint of existing siblings —
	// see captureCWDAppearance / the seed pass in applyCWDIdentityMappings.
	if win, ok := c.getWindowByID(windowID); ok {
		if key, keyOK := c.windowNameKey(win); keyOK {
			c.captureCWDAppearance(key, trimmedColor, win.Icon)
			// Re-baseline the transition key to the current dir so this manual set
			// isn't seen as a transition next refresh (which would re-apply the
			// remembered color over the user's choice). The dir didn't change, so
			// key==AppearanceKey holds and restoreAppearanceOnTransition no-ops.
			exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_appearance_key", key).Run()
		}
	}
}

func (c *Coordinator) setWindowIcon(windowID, icon string) {
	trimmedIcon := strings.TrimSpace(icon)
	if trimmedIcon == "" {
		exec.Command("tmux", "set-window-option", "-t", windowID, "-u", "@tabby_icon").Run()
	} else {
		exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_icon", trimmedIcon).Run()
	}
	// The marker lives on this window's own @tabby_icon option. Like color
	// (see setWindowColor), the value is remembered as the project's "last used"
	// appearance so a future NEW window in the same dir/host can seed from it —
	// a one-time seed, never a per-refresh repaint of existing siblings.
	if win, ok := c.getWindowByID(windowID); ok {
		if key, keyOK := c.windowNameKey(win); keyOK {
			c.captureCWDAppearance(key, win.CustomColor, trimmedIcon)
			// Re-baseline the transition key (see setWindowColor) so a manual marker
			// set isn't clobbered by restoreAppearanceOnTransition next refresh.
			exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_appearance_key", key).Run()
		}
	}
}

// applyCWDIdentityMappings persists and restores per-PROJECT group + pinned
// state. Runs each refresh on the same windows slice (which becomes c.windows)
// BEFORE syncWindowNames and the grouping pass — so in-memory mirroring below is
// honored in the same cycle.
//
// NOTE: unlike group/pinned, a tab's custom color and marker are deliberately
// NOT persisted or restored by directory (see setWindowColor/setWindowIcon).
// They live only on the window's own @tabby_color / @tabby_icon options.
//
// The identity is keyed on windowNameKey — the project root (git toplevel) for a
// local window, or "ssh://host/topmost" for a remote one — so group/pinned set
// at a repo root is reused across its subdirs, future sessions, and ssh hosts.
//
// Tab NAMES are deliberately NOT restored here: auto names are per-window live
// summaries and a manual rename lives only on the window's @tabby_name_locked
// option (see CWDColorMapping). The only name-related work left is clearing a
// bogus generic hard-lock.
//
// Every tmux exec is guarded on a value diff, so the steady state and
// already-restored windows do no work.
func (c *Coordinator) applyCWDIdentityMappings(windows []tmux.Window) {
	for i := range windows {
		// A generic stub (claude/zsh/bash/~/...) that got hard-locked is almost
		// always the `r` / right-click rename binding pre-filling the current
		// auto-name and the user accepting it — which sets @tabby_name_locked on
		// the stub. That lock is bogus: a real user lock always has a non-generic
		// name. Clear it UNCONDITIONALLY so the stub flows back into the live
		// summary path instead of freezing on the launcher name.
		if windows[i].NameLocked && isGenericTabName(windows[i].Name) {
			exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "-u", "@tabby_name_locked").Run()
			windows[i].NameLocked = false
			logEvent("IDENTITY_UNLOCK_GENERIC win=%s name=%q", windows[i].ID, windows[i].Name)
		}

		key, ok := c.windowNameKey(windows[i])
		if !ok {
			continue
		}

		if windows[i].NameLocked {
			// A deliberately-named window: keep this project's group/pinned record
			// in sync. captureCWDIdentity no-ops (no disk write) when unchanged.
			c.captureCWDIdentity(key, windows[i].Group, windows[i].Pinned)
		} else if rec, recOK := c.getCWDColorMapping(key); recOK {
			if g := strings.TrimSpace(rec.Group); g != "" && windows[i].Group != g {
				exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "@tabby_group", g).Run()
				windows[i].Group = g
			}

			if rec.Pinned && !windows[i].Pinned {
				exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "@tabby_pinned", "1").Run()
				windows[i].Pinned = true
			}
		}

		// Seed this window's color/marker from the project's remembered "last
		// used" appearance — but only ONCE, when the window is brand-new (see
		// seedWindowAppearance). This is what gives a freshly-opened tab in a
		// known directory/host its remembered look without repainting existing
		// siblings every refresh (the bug that retired the old per-dir restore).
		c.seedWindowAppearance(&windows[i], key)

		// After the one-time seed, re-apply the remembered color/marker if this
		// window has since moved into a DIFFERENT known directory/host (a cd or an
		// ssh). No-ops in the steady state and for a just-seeded window.
		c.restoreAppearanceOnTransition(&windows[i], key)
	}
}

// seedWindowAppearance applies the project's remembered color/marker to a window
// exactly once in the window's lifetime, then records that the decision was made
// via the durable @tabby_color_seeded option. "Once" is the whole point: a
// second concurrent window in the same repo gets the same seed but can be
// recolored freely, and a color the user later CLEARS never comes back on the
// next refresh or after a daemon reload. It never overwrites an appearance the
// window already has — only empty fields are seeded.
func (c *Coordinator) seedWindowAppearance(win *tmux.Window, key string) {
	if win.AppearanceSeeded {
		return
	}

	rec, recOK := c.getCWDColorMapping(key)
	color, icon := seedAppearancePlan(*win, rec, recOK)
	if color != "" {
		exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_color", color).Run()
		win.CustomColor = color
	}
	if icon != "" {
		exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_icon", icon).Run()
		win.Icon = icon
	}

	// Seed the window's GROUP from a configured preset the first (and only) time
	// we see it: a brand-new tab opened inside a group's working_dir (e.g.
	// ~/git/gunpowder) joins that group automatically, while a tab opened
	// anywhere else stays in Default. Only when it doesn't already belong to a
	// group — a cache-restored or user-set @tabby_group always wins — and, like
	// the color/marker seed above, exactly once: gated by @tabby_color_seeded so
	// moving the tab out of the group later makes it stay out.
	if strings.TrimSpace(win.Group) == "" {
		if g := c.presetGroupForWindow(*win); g != "" {
			exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_group", g).Run()
			win.Group = g
		}
	}

	// Mark the one-time decision as made REGARDLESS of whether anything was
	// seeded. If there was no remembered appearance (or the window already had
	// its own), we must not reconsider on a later refresh — that would turn this
	// one-shot seed back into the per-refresh restore we deliberately removed.
	exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_color_seeded", "1").Run()
	win.AppearanceSeeded = true

	// Establish the transition baseline: record the key whose appearance this window
	// now carries so restoreAppearanceOnTransition can later detect a cd/ssh move.
	// Stamp it unconditionally (even if nothing was seeded) so the very first refresh
	// after seeding is never mistaken for a transition.
	exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_appearance_key", key).Run()
	win.AppearanceKey = key
}

// restoreAppearanceOnTransition re-applies a directory's remembered color/marker
// when a window moves INTO that directory (a cd, or an ssh into a new host+dir),
// and only then. It is deliberately gated so it is NOT the per-refresh restore that
// was removed for bleeding siblings: it fires exactly once per transition, on the
// single window whose live key changed, and never on a window merely sitting in a
// directory. Group is intentionally not handled here — applyCWDIdentityMappings
// already keeps @tabby_group in sync every refresh.
func (c *Coordinator) restoreAppearanceOnTransition(win *tmux.Window, key string) {
	if !win.AppearanceSeeded {
		return // a brand-new window is seedWindowAppearance's job, not ours
	}
	if key == win.AppearanceKey {
		return // steady state: no move, no repaint — this is the anti-bleed guard
	}

	// The window transitioned into a new directory/host. Stamp the new key up front
	// so we don't retry the lookup every refresh, then apply that directory's
	// remembered color/marker to THIS window only.
	exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_appearance_key", key).Run()
	win.AppearanceKey = key

	rec, recOK := c.getCWDColorMapping(key)
	if !recOK {
		return
	}
	if color := strings.TrimSpace(rec.Color); color != "" && color != win.CustomColor {
		exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_color", color).Run()
		win.CustomColor = color
	}
	if icon := strings.TrimSpace(rec.Icon); icon != "" && icon != win.Icon {
		exec.Command("tmux", "set-window-option", "-t", win.ID, "@tabby_icon", icon).Run()
		win.Icon = icon
	}
}

// seedAppearancePlan decides which color/marker a not-yet-seeded window should
// inherit from its project's remembered appearance record. An empty return means
// "leave that field as-is": only a field the window lacks of its own is seeded,
// and an already-seeded window inherits nothing. Pure (no tmux) so the seed
// decision is unit-testable independently of the side effects.
func seedAppearancePlan(win tmux.Window, rec CWDColorMapping, recOK bool) (color, icon string) {
	if win.AppearanceSeeded || !recOK {
		return "", ""
	}
	if win.CustomColor == "" {
		color = strings.TrimSpace(rec.Color)
	}
	if win.Icon == "" {
		icon = strings.TrimSpace(rec.Icon)
	}
	return color, icon
}

// presetGroupForWindow returns the name of the configured group whose working_dir
// contains this window's directory, or "" when none matches. This is what lets a
// brand-new tab opened inside a project directory (e.g. ~/git/gunpowder) land in
// that project's group automatically; a tab opened anywhere without a matching
// preset stays in Default. Remote windows are skipped (working_dir is a local
// path). $HOME never matches — it has no project identity. When several groups'
// working_dirs would match (nested), the most specific (longest) one wins.
func (c *Coordinator) presetGroupForWindow(win tmux.Window) string {
	if win.RemoteHost != "" || hasRemoteContentPane(win) {
		return ""
	}
	cwd := firstPaneCWD(win)
	if cwd == "" || isHomeDir(cwd) {
		return ""
	}

	best := ""
	bestLen := -1
	for _, g := range c.config.Groups {
		if g.Name == "" || g.Name == "Default" {
			continue
		}
		dir := expandWorkingDir(g.WorkingDir)
		if dir == "" {
			continue
		}
		if cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator)) {
			if len(dir) > bestLen {
				best = g.Name
				bestLen = len(dir)
			}
		}
	}
	return best
}

// expandWorkingDir normalizes a config working_dir for prefix matching: a leading
// "~/" (or a bare "~") expands to the user's home directory, and the result is
// cleaned. Returns "" for empty input.
func expandWorkingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if dir == "~" {
		return daemonHomeDir
	}
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return normalizeCWD(dir)
}

// effectiveWindowMarker returns the marker to display for a tab: the window's own
// @tabby_icon when it has one, otherwise its group's configured marker. A tab
// without a marker of its own inherits the group's, so the group's identity
// carries onto every tab in it. groupIcon is the tab's group Theme.Icon.
func effectiveWindowMarker(winIcon, groupIcon string) string {
	if m := strings.TrimSpace(winIcon); m != "" {
		return m
	}
	return strings.TrimSpace(groupIcon)
}

// loadConfigCached returns the parsed config, using a mtime-keyed cache so
// the steady-state RefreshWindows path skips the file read + YAML parse when
// the user hasn't touched the config file. Returns nil only when the file
// can't be read AND there's no prior cached copy.
func (c *Coordinator) loadConfigCached() *config.Config {
	path := config.DefaultConfigPath()
	info, statErr := os.Stat(path)
	c.configCacheMu.Lock()
	if statErr == nil && c.configCacheCfg != nil &&
		c.configCachePath == path &&
		c.configCacheMtime.Equal(info.ModTime()) {
		cfg := c.configCacheCfg
		c.configCacheMu.Unlock()
		return cfg
	}
	c.configCacheMu.Unlock()
	newCfg, err := config.LoadConfig(path)
	if err != nil {
		// Fall back to the prior cached value if available; otherwise nil
		// (matches the old `newCfg, _ := config.LoadConfig(...)` semantics).
		c.configCacheMu.Lock()
		prev := c.configCacheCfg
		c.configCacheMu.Unlock()
		return prev
	}
	c.configCacheMu.Lock()
	c.configCacheCfg = newCfg
	c.configCachePath = path
	if statErr == nil {
		c.configCacheMtime = info.ModTime()
	}
	c.configCacheMu.Unlock()
	return newCfg
}

// RefreshWindows fetches current window/pane state from tmux
func (c *Coordinator) RefreshWindows() {
	// Do all external I/O (tmux, config, ps) BEFORE acquiring stateMu.
	// Holding stateMu during slow external calls causes lock contention:
	// leaked task goroutines that timed out continue holding the lock,
	// blocking subsequent tasks that need stateMu.RLock() (e.g. handleWidthSync
	// via BroadcastRender), causing LOOP_STALL and daemon termination.

	newCfg := c.loadConfigCached()

	// Peek catch-all: re-park a surfaced minimized window that lost focus via a
	// path that bypassed settlePeek (direct pane click, external select-window).
	// Runs before listing so a just-re-parked window shows up in the merge below.
	c.maybeReparkPeeked()

	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		logEvent("REFRESH_WINDOWS_ERROR err=%v", err)
		return
	}
	logEvent("REFRESH_WINDOWS_OK count=%d", len(windows))

	// Drop sidebar stash windows from tabby's view entirely. They are holding
	// windows for break-pane'd sidebars while hidden on mobile — they must not
	// appear in the window carousel, be selectable via prev/next, or show up
	// in any rendered list. They're still live in tmux; join-pane brings them
	// back when the user reveals the sidebar.
	if len(windows) > 0 {
		filtered := windows[:0]
		for _, w := range windows {
			if strings.HasPrefix(w.Name, sidebarStashWindowPrefix) || strings.HasPrefix(w.Name, contentStashWindowPrefix) {
				continue
			}
			filtered = append(filtered, w)
		}
		windows = filtered
	}

	// Merge in this session's PARKED minimized windows (they live in the holding
	// session, invisible to native nav, but must still render in the sidebar's
	// Minimized section). They carry Minimized=true so everything downstream
	// (grouping, sidebarRenderGroups, the nav skip filter) treats them correctly.
	// Dedup against the live list: a window mid-peek can momentarily appear in
	// BOTH the live snapshot and the parked snapshot (the two tmux queries race
	// with the surface/park move), which would render it twice for a frame.
	liveIDs := make(map[string]bool, len(windows))
	for _, w := range windows {
		liveIDs[w.ID] = true
	}
	for _, pw := range c.listParkedMinimizedWindows() {
		if !liveIDs[pw.ID] {
			windows = append(windows, pw)
		}
	}

	c.applyCWDIdentityMappings(windows)

	// Pre-load process tree BEFORE acquiring stateMu. loadProcessTree runs
	// ps -A which can be slow; running it inside the lock blocks IncrementSpinner
	// and other stateMu-dependent goroutines, causing LOOP_STALL / daemon crash.
	// Reading c.lastProcessCheck here without the lock is safe: this function
	// runs in a single goroutine (window_tick) and c.lastProcessCheck is only
	// written later by processAIToolStates in this same call.
	var preloadedProcessTree *processTree
	if c.config.Indicators.Busy.Enabled || c.config.Indicators.Input.Enabled {
		if time.Since(c.lastProcessCheck) > 2*time.Second {
			preloadedProcessTree = loadProcessTree()
		}
	}

	prefixModeRaw := ""
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if out, err := exec.CommandContext(ctx, "tmux", "show-option", "-gqv", "@tabby_prefix_mode").Output(); err == nil {
			prefixModeRaw = strings.TrimSpace(string(out))
		}
		cancel()
	}

	c.stateMu.Lock()

	if newCfg != nil {
		c.config = newCfg
		applyContrastConfig(newCfg)
	}

	// Note: collapsed groups state is managed in-memory and synced to tmux options
	// We don't reload here to avoid race conditions with toggle_group action

	c.windows = windows

	activeWindowID := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")

	// Auto-sync window names from active pane title, unless name is locked.
	// Collects pending rename ops for execution after unlock.
	pendingRenames := c.syncWindowNames()

	// Detect AI tool busy/done/idle states using state transitions.
	// Collects pending tmux set-option ops for execution after unlock.
	aiToolOps := c.processAIToolStates(preloadedProcessTree)

	c.grouped = grouping.GroupWindowsWithOptions(windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.computeVisualPositions()
	pendingMoves := c.syncWindowIndices()

	if prefixModeRaw != "" {
		c.config.Sidebar.PrefixMode = (prefixModeRaw == "1" || prefixModeRaw == "true")
	}

	// Build pane header color args while holding the lock (read-only access to state).
	// The actual tmux exec happens AFTER unlock to avoid holding stateMu during
	// slow external calls which causes LOOP_STALL and daemon termination.
	colorArgs := c.buildPaneHeaderColorArgs()
	c.stateMu.Unlock()

	// Run the tmux set-option commands outside the lock with a timeout.
	if len(colorArgs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "tmux", colorArgs...).Run()
	}

	// Execute deferred AI tool state tmux set-option ops outside the lock.
	for _, op := range aiToolOps {
		if op.unset {
			tmuxRun("set-option", "-w", "-t", op.windowID, "-u", op.key)
		} else {
			tmuxRun("set-option", "-w", "-t", op.windowID, op.key, op.value)
		}
	}

	// Execute deferred window rename ops outside the lock.
	for _, op := range pendingRenames {
		tmuxRun("rename-window", "-t", op.windowID, op.desiredName)
		tmuxRun("set-window-option", "-t", op.windowID, "@tabby_name_locked", "0")
	}

	// Execute deferred window move ops outside the lock.
	for _, op := range pendingMoves {
		tmuxRun("move-window", "-s", op.src, "-t", op.dst)
	}
	// Restore focus to the pending new window ONLY when this RefreshWindows
	// actually performed window renumbering via move-window — that's the
	// scenario the restore was originally added for (tmux's renumbering
	// briefly drops the active marker, and we need to re-assert it).
	//
	// Firing on every RefreshWindows during the 3-second "ready" hold (the
	// previous behaviour) caused the post-`+`-tap cycling bug on phone with
	// sidebar detached: an unrelated session-active-window flip (driven by
	// the after-kill-pane storm tmux runs while the new window's pane
	// layout settles) was detected as drift, restored via select-window,
	// which fed activity back into the system, etc. The restore-on-every-
	// refresh was structurally broken for any case other than move-window
	// renumbering. See coordinator.go preferredWindowFocusTarget for the
	// detection-side notes.
	//
	// Single-session multi-client setups don't actually support per-client
	// current windows in tmux 3.x — `switch-client -c <tty> -t <window>`
	// is equivalent to `select-window` for the session — so we cannot
	// scope the restore to just the firing client to dodge the cycle.
	if len(pendingMoves) > 0 {
		if focusTarget := preferredWindowFocusTarget(c, activeWindowID); focusTarget != "" {
			restoreWindowFocus(focusTarget)
			logEvent("RESTORE_WINDOW_FOCUS target=%s active=%s pending_moves=%d", focusTarget, activeWindowID, len(pendingMoves))
		}
	} else {
		// Diagnostic: confirm we're skipping the restore on the cycling path.
		// Cheap because preferredWindowFocusTarget is only consulted to log.
		status := c.NewWindowStatus()
		if status.State == "ready" && status.WindowID != "" {
			logEvent("RESTORE_WINDOW_FOCUS_SKIP reason=no_pending_moves pending=%s active=%s firing_tty=%s age_ms=%d", status.WindowID, activeWindowID, status.FiringTTY, time.Since(status.Created).Milliseconds())
		}
	}
}

// SetActiveWindowOptimistic flips the Active flag on c.windows so the next
// BroadcastRender uses the correct active window immediately, without waiting
// for a full RefreshWindows round-trip through tmux.
func (c *Coordinator) SetActiveWindowOptimistic(windowID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	for i := range c.windows {
		c.windows[i].Active = (c.windows[i].ID == windowID)
	}
	// Re-group so generateSidebarHeader picks up the new active window's colors
	c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.computeVisualPositions()
}

// tmuxSetOption is a pending tmux set-option command collected under lock
// for deferred execution after the lock is released.
type tmuxSetOption struct {
	windowID string
	key      string
	value    string // value to set (ignored when unset=true)
	unset    bool   // true means use -u flag to unset the option
}

// processAIToolStates detects AI tool busy/done/idle states using stateful
// transition tracking. Detection is per-pane: each AI pane gets its own
// busy/input state stored in pane.AIBusy and pane.AIInput.
//
// For multi-pane windows, indicators appear on individual pane lines in the
// sidebar. For single-pane windows, indicators stay at the window tab level.
//
// Detection signals (universal, works for any AI tool):
//   - Braille spinner in pane title (U+2801-U+28FF): tool is working (Claude Code)
//   - Pane title changed since last cycle: tool is active (OpenCode, Gemini, etc.)
//   - Process tree CPU usage > 5%: tool is working (universal)
//
// State machine per pane:
//   - Currently busy -> Busy indicator (animated spinner)
//   - Was busy, now idle (tool still running) -> Input indicator (needs user input)
//   - AI tool exited (was present, now gone) -> Bell indicator at window level
//   - Was idle, still idle -> no indicator
func (c *Coordinator) processAIToolStates(preloaded *processTree) []tmuxSetOption {
	var pending []tmuxSetOption
	now := time.Now().Unix()

	// Load process table once per cycle for CPU-based busy detection.
	// Throttle to max once per 2s; skip if indicators are disabled.
	// preloaded is non-nil when RefreshWindows pre-fetched it outside the lock
	// (the normal path). The fallback inline load should not be reached in
	// practice but is kept as a safety net for direct callers.
	var pt *processTree
	needsProcessTree := c.config.Indicators.Busy.Enabled || c.config.Indicators.Input.Enabled
	if needsProcessTree {
		if preloaded != nil {
			pt = preloaded
			c.cachedProcessTree = pt
			c.lastProcessCheck = time.Now()
		} else if time.Since(c.lastProcessCheck) > 2*time.Second {
			pt = loadProcessTree()
			c.cachedProcessTree = pt
			c.lastProcessCheck = time.Now()
		} else {
			pt = c.cachedProcessTree
		}
	}

	// Track which pane IDs we see this cycle for stale cleanup
	seenPanes := make(map[string]bool)

	// Windows an attached client is actually viewing right now. The AI input
	// "?" is an unseen-attention signal, so we only acknowledge it (and suppress
	// it) for windows that are genuinely on someone's screen — not merely the
	// active window of a detached session. Computed once per cycle.
	viewedWindows := attachedClientWindows()

	for i := range c.windows {
		win := &c.windows[i]
		idx := win.Index
		contentPaneCount := 0
		for j := range win.Panes {
			if isAuxiliaryPane(win.Panes[j]) {
				continue
			}
			contentPaneCount++
		}
		multiPane := contentPaneCount > 1

		// Find all AI tool panes in this window
		var aiPanes []*tmux.Pane
		for j := range win.Panes {
			if isAuxiliaryPane(win.Panes[j]) {
				continue
			}
			if tmux.IsAITool(win.Panes[j].Command) {
				aiPanes = append(aiPanes, &win.Panes[j])
			}
		}

		// Check for expiring bell indicators (window-level, from AI tool exit)
		if expiry, ok := c.aiBellUntil[idx]; ok {
			if now < expiry {
				win.Bell = true
			} else {
				delete(c.aiBellUntil, idx)
			}
		}

		if len(aiPanes) == 0 {
			// No AI tool in this window.
			// Check if any pane in this window WAS an AI tool last cycle (tool exited).
			anyPrevAI := false
			for j := range win.Panes {
				if isAuxiliaryPane(win.Panes[j]) {
					continue
				}
				pid := win.Panes[j].ID
				if c.prevPaneBusy[pid] || c.prevPaneTitle[pid] != "" {
					anyPrevAI = true
					delete(c.prevPaneBusy, pid)
					delete(c.prevPaneTitle, pid)
					delete(c.hookPaneActive, pid)
					delete(c.hookPaneBusyIdleAt, pid)
				}
			}
			if anyPrevAI {
				win.Bell = true
				win.Input = false
				c.aiBellUntil[idx] = now + 30
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_bell", value: "1"})
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", value: ""})
			}
			// Clear stale hook indicators on windows with no AI tools.
			// Handles cases where the daemon wasn't tracking the AI tool
			// (e.g., daemon restart, race between hook and exit) but hooks
			// left indicators set.
			if win.Busy {
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_busy", unset: true})
				win.Busy = false
			}
			if win.Input {
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", unset: true})
				win.Input = false
			}
			continue
		}

		// If this is the active window, clear window-level input indicator
		if win.Active && win.Input {
			win.Input = false
			pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", unset: true})
		}

		// === Per-pane AI detection ===
		// Hook-based: @tabby_busy is set at window level. When hooks are active,
		// attribute busy to the pane with a spinner, or first AI pane as fallback.
		hookBusyPaneID := ""
		if win.Busy {
			// Find which pane the hook likely refers to
			for _, p := range aiPanes {
				if tmux.HasSpinner(p.Title) {
					hookBusyPaneID = p.ID
					break
				}
			}
			if hookBusyPaneID == "" {
				hookBusyPaneID = aiPanes[0].ID
			}
		}

		// Hook-based input: @tabby_input at window level -> attribute to active AI pane or first
		hookInputPaneID := ""
		if win.Input && !win.Active {
			for _, p := range aiPanes {
				if tmux.HasIdleIcon(p.Title) {
					hookInputPaneID = p.ID
					break
				}
			}
			if hookInputPaneID == "" {
				hookInputPaneID = aiPanes[0].ID
			}
		}

		// Staleness check for hook-based busy (window-level @tabby_busy)
		if win.Busy {
			anySpinner := false
			for _, p := range aiPanes {
				if tmux.HasSpinner(p.Title) {
					anySpinner = true
					break
				}
			}
			if !anySpinner {
				stalePID := hookBusyPaneID
				if _, ok := c.hookPaneBusyIdleAt[stalePID]; !ok {
					c.hookPaneBusyIdleAt[stalePID] = now
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d): hook says busy but no spinner, starting staleness timer", stalePID, idx)
				} else if now-c.hookPaneBusyIdleAt[stalePID] > 10 {
					idleSecs := now - c.hookPaneBusyIdleAt[stalePID]
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d): auto-clearing stale @tabby_busy (idle for %ds)", stalePID, idx, idleSecs)
					logEvent("STALE_BUSY_CLEAR pane=%s window=%d idle_secs=%d", stalePID, idx, idleSecs)
					pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_busy", unset: true})
					win.Busy = false
					hookBusyPaneID = ""
					delete(c.hookPaneBusyIdleAt, stalePID)
				}
			} else {
				// Spinner found — reset staleness for the busy pane
				delete(c.hookPaneBusyIdleAt, hookBusyPaneID)
			}
		}

		// Process each AI pane individually
		for _, pane := range aiPanes {
			pid := pane.ID
			seenPanes[pid] = true

			hasSpinner := tmux.HasSpinner(pane.Title)
			hasIdle := tmux.HasIdleIcon(pane.Title)

			// === Hook-based detection for this pane ===
			if win.Busy && pid == hookBusyPaneID {
				// Hook says this pane is busy
				c.hookPaneActive[pid] = true
				pane.AIBusy = true
				pane.AIInput = false
				if !c.prevPaneBusy[pid] {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): -> BUSY (hook)",
						pid, idx, pane.Command)
				}
				c.prevPaneBusy[pid] = true
				delete(c.aiBellUntil, idx)
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			if pid == hookInputPaneID {
				// Hook says this pane needs input
				pane.AIInput = true
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			// Hook-active bypass: when hooks previously controlled this pane
			// and now say idle, trust that unless spinner overrides.
			if c.hookPaneActive[pid] && !win.Busy && !hasSpinner {
				if c.prevPaneBusy[pid] {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): BUSY -> IDLE (hook)",
						pid, idx, pane.Command)
				}
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			// === Passive detection ===
			busy := false

			// Signal 1: Braille spinner in this pane's title
			if hasSpinner {
				busy = true
			}

			// Signal 2: Title changed since last cycle
			prevTitle, hasPrev := c.prevPaneTitle[pid]
			hadSpinner := hasPrev && tmux.HasSpinner(prevTitle)
			spinnerCleared := hadSpinner && !hasSpinner
			if hasPrev && pane.Title != prevTitle && !spinnerCleared && !hasIdle {
				busy = true
			}

			// Signal 3: CPU usage (skip when idle icon present)
			if !busy && pane.PID > 0 && !hasIdle {
				cpuPct := pt.treeCPU(pane.PID)
				if cpuPct > 5.0 {
					busy = true
				}
			}

			// State machine
			wasBusy := c.prevPaneBusy[pid]

			if busy {
				pane.AIBusy = true
				pane.AIInput = false
				c.prevPaneBusy[pid] = true
				delete(c.aiBellUntil, idx)
				if !wasBusy {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): -> BUSY (spinner=%v titleChanged=%v)",
						pid, idx, pane.Command, hasSpinner, hasPrev && pane.Title != prevTitle)
				}
			} else if hasIdle {
				// Claude Code shows its ✳ idle icon (U+2733) when it's waiting for
				// the user. Surface the input indicator directly — not only on a
				// busy->idle transition — so a tab parked at ✳ (wasBusy already
				// false) still flags "?".
				pane.AIInput = true
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				if wasBusy {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): -> INPUT (idle icon)",
						pid, idx, pane.Command)
				}
			} else if wasBusy {
				// busy -> idle: tool waiting for user input
				pane.AIInput = true
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): BUSY -> INPUT (title=%q)",
					pid, idx, pane.Command, pane.Title)
			} else if !hasPrev {
				coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): FIRST SEEN (title=%q)",
					pid, idx, pane.Command, pane.Title)
			}

			c.prevPaneTitle[pid] = pane.Title
		}

		// === Derive window-level state ===
		// Single-pane: promote pane state to window (current behavior)
		// Multi-pane: indicators stay on pane lines; window shows nothing for busy/input
		if !multiPane && len(aiPanes) == 1 {
			pane := aiPanes[0]
			pid := pane.ID
			if pane.AIBusy {
				win.Busy = true
				win.Input = false
				// New activity re-arms the unseen-attention signal.
				delete(c.aiInputAck, pid)
			} else if pane.AIInput {
				// The "?" means "something happened here you haven't seen yet" —
				// not "Claude/AGY is sitting idle". Passive ✳-idle detection fires
				// whenever the tool is simply ready for input, so once the user
				// actually views the window (an attached client is on it) we
				// acknowledge it and keep it quiet until the next busy cycle,
				// instead of re-flagging every time they switch away.
				viewed := viewedWindows[win.ID]
				if viewed {
					c.aiInputAck[pid] = true
				}
				if !viewed && !c.aiInputAck[pid] {
					win.Input = true
					win.Busy = false
				}
			}
		} else if multiPane {
			// Multi-pane: clear window-level busy/input (indicators are on pane lines)
			// But if the window had @tabby_busy from hooks, we already handled it above.
			// Only clear the window-level flags that were set by passive detection.
			anyPaneBusy := false
			anyPaneInput := false
			viewed := viewedWindows[win.ID]
			for _, p := range aiPanes {
				if p.AIBusy {
					anyPaneBusy = true
					delete(c.aiInputAck, p.ID)
				}
				if p.AIInput {
					// Same unseen-attention acknowledgment as the single-pane path:
					// once the window is actually viewed, stay quiet until the next
					// busy cycle.
					if viewed {
						c.aiInputAck[p.ID] = true
					}
					if !c.aiInputAck[p.ID] {
						anyPaneInput = true
					}
				}
			}
			// For collapsed multi-pane: aggregate to window level
			if win.Collapsed {
				win.Busy = anyPaneBusy
				if !anyPaneBusy && anyPaneInput && !viewed {
					win.Input = true
				}
			} else {
				// Expanded multi-pane: no window-level busy/input (pane lines show it)
				win.Busy = false
				win.Input = false
			}
		}

		// Clear input for the focused pane of a window an attached client is
		// actually viewing (expanded multi-pane shows indicators on pane lines).
		if multiPane && viewedWindows[win.ID] {
			for _, pane := range aiPanes {
				if pane.Active {
					pane.AIInput = false
				}
			}
		}
	}

	// Cleanup stale pane state for panes that no longer exist
	for pid := range c.prevPaneBusy {
		if !seenPanes[pid] {
			delete(c.prevPaneBusy, pid)
			delete(c.prevPaneTitle, pid)
			delete(c.hookPaneActive, pid)
			delete(c.hookPaneBusyIdleAt, pid)
			delete(c.aiInputAck, pid)
		}
	}
	for pid := range c.prevPaneTitle {
		if !seenPanes[pid] {
			delete(c.prevPaneTitle, pid)
		}
	}
	return pending
}

// processTree holds pre-parsed process table data for CPU-based busy detection.
// Call loadProcessTree() once per cycle and reuse for all windows.
type processTree struct {
	children map[int][]int   // ppid -> child pids
	cpuByPID map[int]float64 // pid -> cpu%
}

// loadProcessTree reads the system process table once. Returns nil on error.
func loadProcessTree() *processTree {
	t := perf.Start("loadProcessTree")
	defer t.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,%cpu=").Output()
	if err != nil {
		return nil
	}
	pt := &processTree{
		children: make(map[int][]int),
		cpuByPID: make(map[int]float64),
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		cpu, err3 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		pt.children[ppid] = append(pt.children[ppid], pid)
		pt.cpuByPID[pid] = cpu
	}
	return pt
}

// treeCPU returns the total CPU% for a process and all its descendants.
func (pt *processTree) treeCPU(pid int) float64 {
	if pt == nil || pid <= 0 {
		return 0
	}
	visited := make(map[int]bool)
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		queue = append(queue, pt.children[cur]...)
	}
	var total float64
	for p := range visited {
		total += pt.cpuByPID[p]
	}
	return total
}

// computeVisualPositions builds a map of window ID -> visual position in the
// sidebar. Visual position is a sequential counter (0, 1, 2...) based on the
// order windows appear in the grouped display, which may differ from tmux's
// window index when groups reorder windows.
func (c *Coordinator) computeVisualPositions() {
	pos := make(map[string]int)
	n := c.baseIndex
	// Real (non-minimized) windows first, contiguous — these drive tmux index sync
	// (syncWindowIndices) and prefix+N selection.
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Minimized {
				continue
			}
			pos[win.ID] = n
			n++
		}
	}
	// Minimized windows are numbered AFTER the real ones: they still get a sidebar
	// "N." but never shift real-window numbering or tmux indices (they live in the
	// holding session and must not be renumbered/moved).
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Minimized {
				pos[win.ID] = n
				n++
			}
		}
	}
	// While the dashboard is active the sidebar renders the remembered origin
	// windows (synthetic ids not in c.grouped); give them sidebar numbers too so
	// they show "1. 2. 3." not "0.". Real-window logic (syncWindowIndices) only
	// looks up live window ids, so these extra entries are inert there.
	if c.dashboardWindowID != "" {
		dn := c.baseIndex
		for _, id := range c.dashboardOrder {
			pos[id] = dn
			dn++
		}
	}
	c.windowVisualPos = pos
}

// syncWindowIndices renumbers tmux windows so their indices match the visual
// tmuxWindowRename is a pending window rename operation collected under lock
// for deferred execution after the lock is released.
type tmuxWindowRename struct {
	windowID    string
	desiredName string
}

// syncWindowNames updates window names from pane directories for
// windows that haven't been explicitly renamed (NameLocked=false).
// Uses the directory basename; combines with " | " when panes are in different dirs.
// Returns pending rename operations to execute after stateMu is released.
func (c *Coordinator) syncWindowNames() []tmuxWindowRename {
	home := os.Getenv("HOME")
	showSSHHost := c.config.Sidebar.ShowSSHHost
	var pending []tmuxWindowRename

	for i := range c.windows {
		if c.windows[i].NameLocked {
			continue
		}
		if len(c.windows[i].Panes) == 0 {
			continue
		}

		// Collect unique directory basenames from all panes, preserving order.
		seen := make(map[string]bool)
		var dirs []string
		for _, pane := range c.windows[i].Panes {
			var name string
			if showSSHHost && pane.Remote && pane.Command == "ssh" {
				if host := tmux.SSHHostForPane(pane.PID); host != "" {
					name = host
				}
			}
			if name == "" {
				p := pane.CurrentPath
				if p == "" {
					continue
				}
				name = shortenPath(p, home)
			}
			if !seen[name] {
				seen[name] = true
				dirs = append(dirs, name)
			}
		}

		if len(dirs) == 0 {
			continue
		}

		desiredName := strings.Join(dirs, " | ")

		if desiredName == c.windows[i].Name {
			continue
		}

		pending = append(pending, tmuxWindowRename{windowID: c.windows[i].ID, desiredName: desiredName})
		c.windows[i].Name = desiredName
	}
	return pending
}

// genericLauncherNames are window names that are clearly automatic-rename
// artifacts rather than deliberate identities: the bare `claude` CLI name tmux
// uses before Claude Code swaps its proc title to a semver, and bare shells.
// (AI-tool commands like agy/gemini/codex and the semver title are caught by
// tmux.IsAITool, so they're not repeated here.)
var genericLauncherNames = map[string]bool{
	"claude": true,
	"zsh":    true,
	"bash":   true,
	"sh":     true,
	"fish":   true,
}

// isGenericTabName reports whether a window name is a transient,
// automatically-derived label (tmux automatic-rename artifact, an AI tool's
// process title, or a "~"/"/" path stub) rather than a deliberate user
// identity. Such names must never be persisted as a tab identity nor restored:
// once saved they would be re-applied + hard-locked every refresh, freezing the
// tab on the stub and suppressing the live AI summary.
func isGenericTabName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || n == "~" || n == "/" || strings.HasPrefix(n, "~/") {
		return true
	}
	if isRawWindowID(n) {
		return true
	}
	if genericLauncherNames[strings.ToLower(n)] {
		return true
	}
	// semver proc title (Claude Code) or a configured AI-tool command
	// (agy/Antigravity, gemini, codex, opencode, aider, cursor, copilot).
	return tmux.IsAITool(n)
}

// isRawWindowID returns true if s looks like a raw tmux window ID (@N).
// tmux uses @N as the window ID format; if this appears as a window name
// it means automatic-rename hasn't fired yet or allow-rename picked up a
// spurious OSC sequence that echoed back the window ID.
func isRawWindowID(s string) bool {
	if len(s) < 2 || s[0] != '@' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// shortenPath converts a full path to a short display name.
// /Users/b -> ~, /Users/b/git/tabby -> tabby, / -> /
func shortenPath(p, home string) string {
	if p == "/" {
		return "/"
	}
	// Use basename for most paths
	base := filepath.Base(p)
	// If the path IS the home directory, show ~
	if p == home {
		return "~"
	}
	return base
}

// tmuxWindowMove is a pending tmux move-window operation collected under lock
// for deferred execution after the lock is released.
type tmuxWindowMove struct {
	src string // source window ID or :index
	dst string // destination :index
}

// positions shown in the sidebar. This ensures prefix+N selects the window
// the user sees as "N" in the sidebar.
// Returns pending move operations to execute after stateMu is released.
//
// Algorithm: cycle decomposition of the (current → desired) permutation.
// Each non-trivial cycle of length k resolves in k+1 moves (one to a temp
// index, k to the targets, walking the cycle in reverse). Fixed points
// (windows already at their desired index) emit zero moves. Chains
// (target slot empty — e.g., a new window's desired slot has no current
// occupant) walk in reverse with no temp.
//
// Why not the brute-force "all to temp 1000+i, then all to desired" pass?
// Each move-window fires both window-linked and window-unlinked tmux
// hooks, each fires SIGNAL_CMD/USR1, which signals the daemon to refresh
// and re-run syncWindowIndices. With 2*N moves per pass that's 4*N hook
// fires; the next syncWindowIndices runs while the previous batch's moves
// are still settling and emits its own (not-yet-converged) batch. The two
// batches fight, convergence is ~1 window per refresh, and the user sees
// 5+ window flips after a `+` tap on phone with sidebar detached. Cycle
// decomposition emits the minimum N+c moves (c = number of non-trivial
// cycles), which usually means 3 moves for the common new-window case
// (one cycle of length 2: the new window swaps with its visual-order
// neighbour). With one batch per real reorder the hook cascade no longer
// produces overlapping batches.
//
// Temp index allocation: cycles use indices in [tempBase, tempBase+N).
// tempBase is below sidebarStashParkBase (9000), above any plausible
// real-window count, so it never collides with stash windows or real
// windows.
func (c *Coordinator) syncWindowIndices() []tmuxWindowMove {
	type winMapping struct {
		id           string
		currentIndex int
		desiredIndex int
	}

	var mappings []winMapping
	allMatch := true
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			// Never renumber/move minimized windows: parked ones live in the holding
			// session (a move-window would relocate them), and a peeked one is only
			// transiently in-session. They're excluded from prefix+N indexing.
			if win.Minimized {
				continue
			}
			desired := c.windowVisualPos[win.ID]
			mappings = append(mappings, winMapping{
				id:           win.ID,
				currentIndex: win.Index,
				desiredIndex: desired,
			})
			if win.Index != desired {
				allMatch = false
			}
		}
	}

	if allMatch {
		return nil // Already in order
	}

	// Build "who is at each tmux index" + "what does each window want" maps
	// for cycle decomposition.
	idAtIndex := make(map[int]string, len(mappings))
	mapByID := make(map[string]winMapping, len(mappings))
	for _, m := range mappings {
		idAtIndex[m.currentIndex] = m.id
		mapByID[m.id] = m
	}

	visited := make(map[string]bool, len(mappings))
	var pending []tmuxWindowMove
	const tempBase = 8000 // above visual indices, below sidebarStashParkBase
	tempCounter := 0

	for _, start := range mappings {
		if visited[start.id] {
			continue
		}
		if start.currentIndex == start.desiredIndex {
			visited[start.id] = true
			continue
		}

		// Walk the chain/cycle: start.id wants start.desiredIndex; whoever
		// is currently at start.desiredIndex wants their own desiredIndex;
		// keep walking until we either circle back to start.id (a cycle)
		// or fall off the end (a chain — desired slot is currently empty).
		cycle := []winMapping{start}
		visited[start.id] = true
		isCycle := false
		next := start.desiredIndex
		for {
			nextID, ok := idAtIndex[next]
			if !ok {
				// Chain: target slot empty. cycle stays as-is.
				break
			}
			if nextID == start.id {
				isCycle = true
				break
			}
			if visited[nextID] {
				// Defensive: shouldn't happen in a well-formed permutation.
				break
			}
			visited[nextID] = true
			next = mapByID[nextID].desiredIndex
			cycle = append(cycle, mapByID[nextID])
		}

		if isCycle {
			// Cycle resolution: move first to temp, then walk the cycle in
			// reverse moving each window directly to its desired index.
			// Indices vacate in the right order so each move can succeed
			// without `-k` (which would destroy a colliding window).
			tmp := tempBase + tempCounter
			tempCounter++
			pending = append(pending, tmuxWindowMove{
				src: cycle[0].id,
				dst: fmt.Sprintf(":%d", tmp),
			})
			for i := len(cycle) - 1; i >= 1; i-- {
				pending = append(pending, tmuxWindowMove{
					src: cycle[i].id,
					dst: fmt.Sprintf(":%d", cycle[i].desiredIndex),
				})
			}
			pending = append(pending, tmuxWindowMove{
				src: fmt.Sprintf(":%d", tmp),
				dst: fmt.Sprintf(":%d", cycle[0].desiredIndex),
			})
		} else {
			// Chain resolution: walk in reverse, moving each window
			// directly to its desired index. Each step's target was
			// vacated by the previous step (or was already empty for the
			// last entry).
			for i := len(cycle) - 1; i >= 0; i-- {
				pending = append(pending, tmuxWindowMove{
					src: cycle[i].id,
					dst: fmt.Sprintf(":%d", cycle[i].desiredIndex),
				})
			}
		}
	}

	coordinatorDebugLog.Printf("syncWindowIndices: %d windows, %d cycles, %d moves", len(mappings), tempCounter, len(pending))

	// Update local state to reflect new indices
	for i := range c.windows {
		if desired, ok := c.windowVisualPos[c.windows[i].ID]; ok {
			c.windows[i].Index = desired
		}
	}

	return pending
}

// updatePaneHeaderColors sets per-window tmux options for pane header colors
// based on the group theme. Uses @tabby_pane_active and @tabby_pane_inactive.
// When auto_border is enabled, also sets pane-border-style and pane-active-border-style.
// applyThemeToTmux applies the current theme's global styles to tmux options
func (c *Coordinator) applyThemeToTmux() {
	if c.theme == nil {
		return
	}

	// Resolve border colors: config > theme > detector fallback
	borderFg := c.config.PaneHeader.BorderFg
	if borderFg == "" {
		borderFg = c.theme.BorderFg
	}
	borderBg := c.config.PaneHeader.BorderBg

	activeFg := c.config.PaneHeader.ActiveBorderFg
	if activeFg == "" {
		activeFg = borderFg // fallback to inactive fg
	}
	activeBg := c.config.PaneHeader.ActiveBorderBg
	if activeBg == "" {
		activeBg = borderBg // fallback to inactive bg
	}

	// Native border mode owns pane-border-* options per-window via
	// applyNativeBorders. Skip the global setters below so a stale global
	// (esp. fg==bg hide) can't fight with the per-window visible style.
	nativeBorders := c.config.PaneHeader.Native != nil && *c.config.PaneHeader.Native
	if !c.borderStylingBlocked() && !nativeBorders {
		// Apply inactive border style
		if inactiveStyle := buildBorderStyle(borderFg, borderBg); inactiveStyle != "" {
			exec.Command("tmux", "set-option", "-g", "pane-border-style", inactiveStyle).Run()
		}

		// Apply active border style
		if activeStyle := buildBorderStyle(activeFg, activeBg); activeStyle != "" {
			exec.Command("tmux", "set-option", "-g", "pane-active-border-style", activeStyle).Run()
		}

		// With overlay pane headers enabled, tmux border lines add an extra visual row
		// between header and content that looks like a duplicate/non-functional header.
		// Prefer runtime tmux option as source of truth, with config as fallback.
		paneHeadersEnabled := c.config.Sidebar.PaneHeaders
		if out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output(); err == nil {
			paneHeadersEnabled = strings.TrimSpace(string(out)) == "on"
		}
		if paneHeadersEnabled && !c.config.PaneHeader.CustomBorder {
			exec.Command("tmux", "set-option", "-g", "pane-border-lines", "simple").Run()
			if c.config.PaneHeader.TerminalBg != "" {
				style := fmt.Sprintf("fg=%s,bg=%s", c.config.PaneHeader.TerminalBg, c.config.PaneHeader.TerminalBg)
				exec.Command("tmux", "set-option", "-g", "pane-border-style", style).Run()
				exec.Command("tmux", "set-option", "-g", "pane-active-border-style", style).Run()
			}
		} else if c.config.PaneHeader.BorderLines != "" {
			exec.Command("tmux", "set-option", "-g", "pane-border-lines", c.config.PaneHeader.BorderLines).Run()
		}
	}

	// Apply message/mode styles (command prompt)
	if c.theme.PromptBg != "" && c.theme.PromptFg != "" {
		style := fmt.Sprintf("fg=%s,bg=%s", c.theme.PromptFg, c.theme.PromptBg)
		exec.Command("tmux", "set-option", "-g", "message-style", style).Run()
		exec.Command("tmux", "set-option", "-g", "message-command-style", style).Run()
	}

	// Apply inactive pane dimming if enabled
	if c.config.PaneHeader.DimInactive {
		dimOpacity := c.config.PaneHeader.DimOpacity
		if dimOpacity <= 0 || dimOpacity > 1 {
			dimOpacity = 0.5 // Default to 50% brightness
		}
		// Use theme's ActiveFg as base color for dimming
		baseFg := c.theme.ActiveFg
		if baseFg == "" {
			baseFg = "#ffffff" // Default white
		}
		baseBg := c.theme.TerminalBg
		if baseBg == "" {
			baseBg = c.theme.SidebarBg
		}

		// Dim the foreground color for inactive panes
		dimFg := dimColor(baseFg, dimOpacity)

		inactiveStyle := fmt.Sprintf("fg=%s", dimFg)
		if baseBg != "" {
			inactiveStyle += fmt.Sprintf(",bg=%s", baseBg)
		}
		exec.Command("tmux", "set-option", "-g", "window-style", inactiveStyle).Run()

		// Active pane gets full brightness
		activeStyle := fmt.Sprintf("fg=%s", baseFg)
		if baseBg != "" {
			activeStyle += fmt.Sprintf(",bg=%s", baseBg)
		}
		exec.Command("tmux", "set-option", "-g", "window-active-style", activeStyle).Run()
	}
}

// ApplyThemeToPane applies theme-specific styles (like background) to a tmux pane
func (c *Coordinator) ApplyThemeToPane(paneID string) {
	if c.theme == nil || paneID == "" {
		return
	}

	// Use TerminalBg from theme, or fall back to SidebarBg
	bg := c.theme.TerminalBg
	if bg == "" {
		bg = c.theme.SidebarBg
	}

	coordinatorDebugLog.Printf("ApplyThemeToPane: pane=%s bg=%s", paneID, bg)

	if bg != "" {
		// Set pane-specific window-style to match the theme background
		// This makes transparency in renderers work correctly
		style := fmt.Sprintf("bg=%s", bg)
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", style).Run()
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-active-style", style).Run()
	}
}

// buildPaneHeaderColorArgs builds the tmux set-option args for pane header colors.
// Called under stateMu; returns the args without executing (caller runs tmux outside the lock).
func (c *Coordinator) buildPaneHeaderColorArgs() []string {
	grouped := c.grouped
	nativeBorders := c.config.PaneHeader.Native != nil && *c.config.PaneHeader.Native
	// Native mode owns per-window border styling via applyNativeBorders. Skip
	// the autoBorder/borderFromTab paths below so they can't fight it.
	autoBorder := c.config.PaneHeader.AutoBorder && !nativeBorders
	borderFromTab := c.config.PaneHeader.BorderFromTab && !nativeBorders
	borderBg := c.config.PaneHeader.BorderBg
	activeBorderFg := c.config.PaneHeader.ActiveBorderFg
	activeBorderBg := c.config.PaneHeader.ActiveBorderBg
	if activeBorderBg == "" {
		activeBorderBg = borderBg
	}
	// Resolve border fg: config border_fg > group theme fg > same as bg (transparent/solid bar)
	configBorderFg := c.config.PaneHeader.BorderFg
	// Shell prompt integration: default enabled unless explicitly disabled
	shellIntegration := c.config.Prompt.ShellIntegration == nil || *c.config.Prompt.ShellIntegration
	promptFallbackIcon := c.config.Prompt.FallbackIcon
	if promptFallbackIcon == "" {
		promptFallbackIcon = "•"
	}
	// The dashboard window manages its own native pane borders/labels
	// (applyDashboardBorders); exclude it so this per-refresh restyle can't
	// overwrite the tile label colors.
	dashWin := dashboardActiveWindowID(c.dashboardSession())
	var args []string
	for _, group := range grouped {
		baseBg := group.Theme.Bg
		for _, win := range group.Windows {
			if dashWin != "" && win.ID == dashWin {
				continue
			}
			tabBg := baseBg
			if win.CustomColor != "" {
				tabBg = win.CustomColor
			}
			// Border fg: config > group fg > same as bg (solid color bar)
			baseFg := configBorderFg
			if baseFg == "" {
				baseFg = group.Theme.Fg
			}
			if baseFg == "" {
				baseFg = tabBg
			}
			if len(args) > 0 {
				args = append(args, ";")
			}
			args = append(args, "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_active", tabBg)
			args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_inactive", tabBg)
			// Shell prompt integration: store effective icon per window
			if shellIntegration {
				effectiveIcon := group.Theme.Icon
				if win.Icon != "" {
					effectiveIcon = win.Icon
				}
				if effectiveIcon == "" {
					effectiveIcon = promptFallbackIcon
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_prompt_icon", effectiveIcon)
			}

			if autoBorder || borderFromTab {
				// Border fg = tab's text color, border bg = tab's bg color
				bFg := baseFg
				bBg := tabBg

				// Active border: config overrides > tab colors
				aFg := activeBorderFg
				if aFg == "" {
					aFg = bFg
				}
				aBg := activeBorderBg
				if aBg == "" {
					aBg = bBg
				}
				activeStyle := buildBorderStyle(aFg, aBg)
				if activeStyle == "" {
					activeStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index),
					"pane-active-border-style", activeStyle)

				// Inactive border: desaturate when dim_inactive is enabled
				iFg := bFg
				iBg := borderBg
				if iBg == "" {
					iBg = bBg
				}
				if c.config.PaneHeader.DimInactive {
					opacity := c.config.PaneHeader.DimOpacity
					if opacity <= 0 || opacity > 1 {
						opacity = 0.6
					}
					tBg := c.config.PaneHeader.TerminalBg
					iFg = desaturateHex(iFg, opacity, tBg)
					iBg = desaturateHex(iBg, opacity, tBg)
				}
				inactiveStyle := buildBorderStyle(iFg, iBg)
				if inactiveStyle == "" {
					inactiveStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index),
					"pane-border-style", inactiveStyle)
			}

			if autoBorder {
				bFg := baseFg
				bBg := tabBg
				for _, p := range win.Panes {
					if isAuxiliaryPane(p) {
						continue
					}
					iBg := borderBg
					if iBg == "" {
						iBg = bBg
					}
					iFg := bFg
					if c.config.PaneHeader.DimInactive {
						opacity := c.config.PaneHeader.DimOpacity
						if opacity <= 0 || opacity > 1 {
							opacity = 0.6
						}
						tBg := c.config.PaneHeader.TerminalBg
						iFg = desaturateHex(iFg, opacity, tBg)
						iBg = desaturateHex(iBg, opacity, tBg)
					}
					inactiveStyle := buildBorderStyle(iFg, iBg)
					if inactiveStyle == "" {
						inactiveStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
					}
					args = append(args, ";", "set-option", "-p", "-t", p.ID,
						"pane-border-style", inactiveStyle)
				}
			}
		}
	}
	return args
}

// GetWindowsHash returns a hash of current window state for change detection.
// Uses the already-cached c.windows to avoid an extra tmux round-trip after RefreshWindows().
func (c *Coordinator) GetWindowsHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	// Simple hash: count + window IDs + active states + pane active states + indicators
	hash := fmt.Sprintf("%d", len(c.windows))
	for _, w := range c.windows {
		// Include window state and indicators
		hash += fmt.Sprintf(":%s:%v:%d:%v:%v:%v:%v:%v:%v:%s:%s:%v",
			w.ID, w.Active, len(w.Panes),
			w.Busy, w.Input, w.Bell, w.Activity, w.Silence,
			w.Collapsed, w.CustomColor, w.Group, w.Last)
		// Include which pane is active within each window
		for _, p := range w.Panes {
			if p.Active {
				hash += fmt.Sprintf(":p%d", p.Index)
				break
			}
		}
	}
	return hash
}

// RefreshGit updates git state
func (c *Coordinator) RefreshGit() {
	// Run all git commands WITHOUT holding stateMu. Holding stateMu during
	// network-bound git commands (e.g. @{upstream} fetch) can block for seconds,
	// causing git_tick to exceed its timeout and kill the daemon.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree").Output()
	isGitRepo := err == nil && strings.TrimSpace(string(out)) == "true"
	if !isGitRepo {
		c.stateMu.Lock()
		c.isGitRepo = false
		c.stateMu.Unlock()
		return
	}

	// Get branch
	var branch string
	if out, err = exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}

	// Get dirty count
	dirty := 0
	if out, err = exec.CommandContext(ctx, "git", "status", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) > 0 {
				dirty++
			}
		}
	}

	// Get ahead/behind
	var ahead, behind int
	if out, err = exec.CommandContext(ctx, "git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD").Output(); err == nil {
		parts := strings.Fields(string(out))
		if len(parts) == 2 {
			behind, _ = strconv.Atoi(parts[0])
			ahead, _ = strconv.Atoi(parts[1])
		}
	}

	// Store results under lock (minimal critical section)
	c.stateMu.Lock()
	c.isGitRepo = true
	if branch != "" {
		c.gitBranch = branch
	}
	c.gitDirty = dirty
	c.gitAhead = ahead
	c.gitBehind = behind
	c.stateMu.Unlock()
}

// teamClaudeAPIKey resolves the proxy key from config, falling back to an
// environment variable so the secret can stay out of config.yaml.
func (c *Coordinator) teamClaudeAPIKey() string {
	if k := c.config.Widgets.TeamClaude.APIKey; k != "" {
		return k
	}
	return os.Getenv("TABBY_TEAMCLAUDE_API_KEY")
}

// RefreshTeamClaude refreshes the cached per-account quota from the teamclaude
// proxy. It returns IMMEDIATELY: the HTTP request runs in a detached goroutine
// so it can never block the daemon event loop (a slow/unreachable proxy would
// otherwise stall window switching). The fetch is throttled to the configured
// UpdateInterval and coalesced so only one request is ever in flight. When the
// quota state actually changes, it triggers a render via OnRefreshLayout.
// A nil URL or disabled widget is a no-op.
func (c *Coordinator) RefreshTeamClaude() {
	cfg := c.config.Widgets.TeamClaude
	if !cfg.Enabled || cfg.URL == "" {
		return
	}

	// Throttle: skip if the last fetch is still fresh.
	interval := time.Duration(cfg.UpdateInterval) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	c.stateMu.RLock()
	fetchedAt := c.teamClaudeFetchedAt
	c.stateMu.RUnlock()
	if !fetchedAt.IsZero() && time.Since(fetchedAt) < interval {
		return
	}

	// Coalesce: at most one in-flight request.
	if !c.teamClaudeFetching.CompareAndSwap(false, true) {
		return
	}

	url, apiKey := cfg.URL, c.teamClaudeAPIKey()
	prevHash := c.GetTeamClaudeStateHash()
	go func() {
		defer c.teamClaudeFetching.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		status, err := teamclaude.Fetch(ctx, url, apiKey)
		// Degraded-model state shares the same fetch cycle. Best-effort: a
		// models error never blocks the status update (and 404 on older
		// servers is already mapped to an empty map by FetchModels).
		models, modelsErr := teamclaude.FetchModels(ctx, url, apiKey)

		c.stateMu.Lock()
		c.teamClaudeFetchedAt = time.Now()
		c.teamClaudeErr = err
		if err == nil {
			c.teamClaudeStatus = status
		}
		c.teamClaudeModelsErr = modelsErr
		if modelsErr == nil {
			c.teamClaudeModels = models
		}
		c.stateMu.Unlock()

		// Trigger a render only when the displayed state changed.
		if c.GetTeamClaudeStateHash() != prevHash && c.OnRefreshLayout != nil {
			c.OnRefreshLayout()
		}
	}()
}

// GetTeamClaudeStateHash returns a cheap fingerprint of the cached quota state
// so the loop can skip re-rendering when nothing changed.
func (c *Coordinator) GetTeamClaudeStateHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	// Degraded-model fingerprint: only the actively-downgraded set matters for
	// the icon (and it must flip the hash so OnRefreshLayout fires). Computed
	// here so it is reflected even when status hasn't loaded yet.
	degraded := "deg=" + strings.Join(c.teamClaudeModels.ActiveDegradations(time.Now().UnixMilli()), ",") + ";"
	if c.teamClaudeStatus == nil {
		if c.teamClaudeErr != nil {
			return "err:" + c.teamClaudeErr.Error() + ";" + degraded
		}
		return "nil;" + degraded
	}
	var b strings.Builder
	b.WriteString(degraded)
	fmt.Fprintf(&b, "cur=%s;", c.teamClaudeStatus.CurrentAccount)
	for _, a := range c.teamClaudeStatus.Accounts {
		sess, wk := -1.0, -1.0
		if a.Remaining.Session != nil {
			sess = *a.Remaining.Session
		}
		if a.Remaining.Weekly != nil {
			wk = *a.Remaining.Weekly
		}
		fmt.Fprintf(&b, "%s:%.2f:%.2f:%v:%d|", a.Name, sess, wk, a.RateLimited(), a.ActiveRequests)
	}
	return b.String()
}

// RefreshSession updates session state
func (c *Coordinator) RefreshSession() {
	// Run all tmux commands WITHOUT holding stateMu to avoid blocking
	// the lock during potentially slow tmux calls.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sessionName string
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
		sessionName = strings.TrimSpace(string(out))
	}

	// Need sessionName for the list-clients call; read current value if not refreshed
	if sessionName == "" {
		c.stateMu.RLock()
		sessionName = c.sessionName
		c.stateMu.RUnlock()
	}

	sessionClients := 0
	if out, err := exec.CommandContext(ctx, "tmux", "list-clients", "-t", sessionName).Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] != "" {
			sessionClients = len(lines)
		}
	}

	windowCount := 0
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{session_windows}").Output(); err == nil {
		windowCount, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	// Store results under lock (minimal critical section)
	c.stateMu.Lock()
	if sessionName != "" {
		c.sessionName = sessionName
	}
	c.sessionClients = sessionClients
	if windowCount > 0 {
		c.windowCount = windowCount
	}
	c.stateMu.Unlock()
}

// IncrementSpinner advances the spinner frame and returns whether any
// frame-by-frame animation is currently visible plus the resulting
// slow-frame index (spinnerFrame/2).
//
// "Visible" here is narrower than tmux's window-flag set: it covers only
// states that animate frame-by-frame — Busy and per-pane AIBusy/AIInput.
// Bell and Activity are sticky badges, not spinners, and were previously
// (incorrectly) keeping the animation gate true forever once any window
// had output. The active-window indicator on the sidebar's active row
// animates independently via getAnimatedActiveIndicator and is gated by
// HasActiveIndicatorAnimation, not by this function.
func (c *Coordinator) IncrementSpinner() (visible bool, slowFrame int) {
	c.stateMu.Lock()
	c.spinnerFrame++
	slowFrame = c.spinnerFrame / 2
	for _, win := range c.windows {
		if win.Busy {
			visible = true
			break
		}
		for _, pane := range win.Panes {
			if pane.AIBusy || pane.AIInput {
				visible = true
				break
			}
		}
		if visible {
			break
		}
	}
	c.stateMu.Unlock()
	return visible, slowFrame
}

// UpdatePetState updates the pet's state (called periodically)
// Returns true if pet is enabled and visually changed (needs render)
func (c *Coordinator) UpdatePetState() bool {
	c.stateMu.Lock()

	// If pet widget is disabled, nothing to update
	if !c.config.Widgets.Pet.Enabled {
		c.stateMu.Unlock()
		return false
	}

	// Track previous visual state to detect changes
	prevPos := c.pet.Pos
	prevState := c.pet.State
	prevYarnPos := c.pet.YarnPos
	prevFloatingCount := len(c.pet.FloatingItems)
	prevMousePos := c.pet.MousePos

	c.pet.AnimFrame++
	now := time.Now()

	// Single-writer election for the shared pet.json. Only the owner decays and
	// saves; a non-owner mirrors the owner's stats for display so multiple
	// per-session daemons can't clobber each other's hunger.
	c.petIsOwner = c.acquirePetOwnership(now)
	if c.petIsOwner {
		atomic.StoreInt32(&petWriteAllowed, 1)
	} else {
		atomic.StoreInt32(&petWriteAllowed, 0)
		c.loadPersistentPetStats()
	}

	width := c.lastWidth
	if width < 10 {
		width = 25
	}
	adventureEnabled := c.config.Widgets.Pet.AdventureEnabled
	// Account for emoji visual width (2 cols) - use safe play width
	maxX := width - 5 // Reduced from width-2 to match safePlayWidth calculation
	if maxX < 1 {
		maxX = 1
	}

	if c.pet.Adventure.Active && !adventureEnabled && !c.pet.Adventure.ManuallyTriggered {
		c.pet.Adventure = adventureState{}
		if c.pet.State == "walking" || c.pet.State == "jumping" {
			c.pet.State = "idle"
		}
		c.pet.HasTarget = false
		c.pet.ActionPending = ""
		c.pet.LastThought = "back home."
	}

	// === DRAGON MECHANICS ===

	// Manage dragon appearance schedule
	if c.pet.DragonState != "" {
		if c.pet.DragonDisappearsAt.IsZero() {
			c.pet.DragonDisappearsAt = now.Add(time.Hour)
			c.pet.DragonAppearedAt = now
		}
		if !c.pet.DragonDisappearsAt.IsZero() && now.After(c.pet.DragonDisappearsAt) {
			c.pet.DragonState = ""
			c.pet.DragonHasTarget = false
			c.pet.DragonActionPending = ""
			c.pet.LastThought = "bye turbo!"
		}
	} else {
		// Only appear if we haven't appeared today
		if c.pet.DragonAppearedAt.YearDay() != now.YearDay() || c.pet.DragonAppearedAt.Year() != now.Year() {
			c.pet.DragonState = "idle"
			c.pet.DragonPos = pos2D{X: maxX - 2, Y: 0}
			c.pet.DragonAppearedAt = now
			c.pet.DragonDisappearsAt = now.Add(time.Hour)
			c.pet.State = "happy"
			c.pet.LastThought = "turbo is here!"
		}
	}

	// Dragon Gravity
	if c.pet.DragonPos.Y > 0 && c.pet.DragonState != "flying" {
		c.pet.DragonPos.Y--
		if c.pet.DragonPos.Y == 0 && c.pet.DragonState == "jumping" {
			c.pet.DragonState = "idle"
		}
	}

	// Dragon Target Movement
	if c.pet.DragonHasTarget {
		nextX := c.pet.DragonPos.X
		if c.pet.DragonPos.X < c.pet.DragonTargetPos.X {
			nextX++
			c.pet.DragonDirection = 1
		} else if c.pet.DragonPos.X > c.pet.DragonTargetPos.X {
			nextX--
			c.pet.DragonDirection = -1
		}

		// Collision check with Cat
		isCatAhead := false
		if c.pet.DragonPos.Y == c.pet.Pos.Y {
			dist := nextX - c.pet.Pos.X
			if dist < 0 {
				dist = -dist
			}
			if dist < 2 {
				isCatAhead = true
			}
		}

		if isCatAhead && c.pet.DragonPos.Y == 0 && c.pet.DragonActionPending != "cuddle" {
			// Jump over the Cat!
			c.pet.DragonPos.Y = 2
			c.pet.DragonState = "jumping"
		}

		c.pet.DragonPos.X = nextX

		// Clamp
		if c.pet.DragonPos.X > maxX {
			c.pet.DragonPos.X = maxX
		}
		if c.pet.DragonPos.X < 0 {
			c.pet.DragonPos.X = 0
		}

		// Dragon interactions
		if c.pet.DragonActionPending == "play" {
			yarnX := c.pet.YarnPos.X
			if yarnX < 0 {
				yarnX = width - 4
			}
			if c.pet.DragonPos.X == yarnX || c.pet.DragonPos.X == yarnX-1 || c.pet.DragonPos.X == yarnX+1 {
				if c.pet.YarnPushCount >= 2 {
					c.pet.DragonTargetPos = c.pet.DragonPos
				} else {
					newYarnX := yarnX + c.pet.DragonDirection*2
					if newYarnX >= 2 && newYarnX < width-2 {
						c.pet.YarnPos.X = newYarnX
						c.pet.YarnPos.Y = 1
						c.pet.DragonTargetPos.X = newYarnX
						c.pet.YarnPushCount++
					} else {
						c.pet.DragonTargetPos = c.pet.DragonPos
					}
				}
			}
		}

		if c.pet.DragonPos.X == c.pet.DragonTargetPos.X && c.pet.DragonPos.Y == c.pet.DragonTargetPos.Y {
			c.pet.DragonHasTarget = false
			if c.pet.DragonState == "flying" && strings.HasPrefix(c.pet.DragonActionPending, "fly_") {
				countStr := strings.TrimPrefix(c.pet.DragonActionPending, "fly_")
				count, _ := strconv.Atoi(countStr)
				if count > 0 {
					// Fly around in variations of height and distance!
					targetY := 2
					if rand.Intn(100) < 50 {
						targetY = 1 // swoop down!
					}
					c.pet.DragonTargetPos = pos2D{X: safeRandRange(0, maxX), Y: targetY}
					c.pet.DragonHasTarget = true
					c.pet.DragonActionPending = fmt.Sprintf("fly_%d", count-1)

					if rand.Intn(100) < 60 {
						dir := 1
						if c.pet.DragonPos.X > c.pet.DragonTargetPos.X {
							dir = -1
						}
						// Shoot fire! Sometimes it goes straight, sometimes down, sometimes leaves a puff
						fireYVel := 0
						if c.pet.DragonPos.Y == 2 && rand.Intn(100) < 50 {
							fireYVel = -1 // shoot downwards
						}
						emoji := "🔥"
						if rand.Intn(100) < 30 {
							emoji = "💨" // puff of smoke
						}
						c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
							Emoji:     emoji,
							Pos:       pos2D{X: c.pet.DragonPos.X + dir, Y: c.pet.DragonPos.Y},
							Velocity:  pos2D{X: dir, Y: fireYVel},
							ExpiresAt: now.Add(2 * time.Second),
						})
					}
				} else {
					c.pet.DragonState = "idle"
					c.pet.DragonActionPending = ""
				}
			} else if c.pet.DragonActionPending == "cuddle" {
				c.pet.DragonState = "happy"
				c.pet.State = "happy" // Cat also gets happy!
			} else if c.pet.DragonActionPending == "play" {
				c.pet.DragonState = "playing"
				c.pet.YarnPos = pos2D{X: -1, Y: 0}
				c.pet.YarnExpiresAt = time.Time{}
				c.pet.YarnPushCount = 0
			} else {
				c.pet.DragonState = "idle"
			}
			c.pet.DragonActionPending = ""
		}
	} else if c.pet.DragonState == "playing" || c.pet.DragonState == "happy" || c.pet.DragonState == "fire_breathing" {
		if c.pet.AnimFrame%20 == 0 {
			c.pet.DragonState = "idle"
		}
	} else if c.pet.DragonState == "sleeping" {
		if c.pet.AnimFrame%60 == 0 && rand.Intn(100) < 30 {
			c.pet.DragonState = "idle"
		}
	}

	// Dragon Random Behaviors
	if c.pet.DragonState == "idle" && !c.pet.DragonHasTarget && c.pet.AnimFrame%10 == 0 {
		hour := now.Hour()
		if hour >= 2 && hour < 6 && rand.Intn(100) < 80 {
			c.pet.DragonState = "sleeping"
		} else {
			if rand.Intn(100) < 25 {
				action := rand.Intn(7)
				switch action {
				case 0:
					// Walk
					c.pet.DragonState = "walking"
					c.pet.DragonDirection = []int{-1, 1}[rand.Intn(2)]
					c.pet.DragonTargetPos = pos2D{X: rand.Intn(maxX), Y: 0}
					c.pet.DragonHasTarget = true
				case 1:
					// Jump
					c.pet.DragonState = "jumping"
					c.pet.DragonPos.Y = 2
				case 2:
					// Play with yarn
					if c.pet.YarnPos.X >= 0 {
						c.pet.DragonTargetPos = pos2D{X: c.pet.YarnPos.X, Y: 0}
						c.pet.DragonHasTarget = true
						c.pet.DragonActionPending = "play"
						c.pet.DragonState = "walking"
					}
				case 3:
					// Chase/Cuddle Cat
					targetX := c.pet.Pos.X - 2
					if targetX < 0 || c.pet.DragonPos.X > c.pet.Pos.X {
						targetX = c.pet.Pos.X + 2
					}
					if targetX > maxX {
						targetX = c.pet.Pos.X - 2
					}
					c.pet.DragonTargetPos = pos2D{X: targetX, Y: 0}
					c.pet.DragonHasTarget = true
					c.pet.DragonActionPending = "cuddle"
					c.pet.DragonState = "walking"
				case 4:
					// Happy
					c.pet.DragonState = "happy"
				case 5:
					// Fly around
					c.pet.DragonState = "flying"
					c.pet.DragonPos.Y = 1 + rand.Intn(2) // Y=1 or Y=2
					c.pet.DragonDirection = []int{-1, 1}[rand.Intn(2)]
					c.pet.DragonTargetPos = pos2D{X: rand.Intn(maxX), Y: c.pet.DragonPos.Y}
					c.pet.DragonHasTarget = true
				case 6:
					// Breathe fire
					c.pet.DragonState = "fire_breathing"
					dir := c.pet.DragonDirection
					if dir == 0 {
						dir = 1
					}
					fireX := c.pet.DragonPos.X + dir
					if fireX < 0 {
						fireX = 0
					}
					if fireX > maxX {
						fireX = maxX
					}
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "🔥",
						Pos:       pos2D{X: fireX, Y: c.pet.DragonPos.Y},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(2 * time.Second),
					})
				}
			}
		}
	}

	// === GRAVITY ===

	// Yarn gravity - falls if in air
	if c.pet.YarnPos.Y > 0 {
		c.pet.YarnPos.Y--
	}

	// Cat gravity - falls back to ground after jumping
	if c.pet.Pos.Y > 0 {
		c.pet.Pos.Y--
		if c.pet.Pos.Y == 0 && c.pet.State == "jumping" {
			c.pet.State = "idle"
		}
	}

	// Food gravity - falls if in air
	if c.pet.FoodItem.X >= 0 && c.pet.FoodItem.Y > 0 {
		c.pet.FoodItem.Y--
		// When food lands, pet should chase it
		if c.pet.FoodItem.Y == 0 && !c.pet.HasTarget {
			c.pet.TargetPos = pos2D{X: c.pet.FoodItem.X, Y: 0}
			c.pet.HasTarget = true
			c.pet.ActionPending = "eat"
			c.pet.State = "walking"
			c.pet.LastThought = "food!"
		}
	}

	// === ADVENTURE MODE ===
	// If adventure is active, update it and skip normal mechanics
	if c.pet.Adventure.Active {
		c.updateAdventurePhase(now, maxX)

		// Clean up expired floating items (also needed during adventure)
		var activeItems []floatingItem
		for _, item := range c.pet.FloatingItems {
			if now.Before(item.ExpiresAt) {
				activeItems = append(activeItems, item)
			}
		}
		c.pet.FloatingItems = activeItems

		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		// Adventure always triggers visual change
		return true
	}

	// === YARN EXPIRATION ===

	// Yarn disappears after expiration time
	if c.pet.YarnPos.X >= 0 && !c.pet.YarnExpiresAt.IsZero() && now.After(c.pet.YarnExpiresAt) {
		c.pet.YarnPos = pos2D{X: -1, Y: 0}
		c.pet.YarnExpiresAt = time.Time{}
		// If cat was chasing yarn, stop
		if c.pet.ActionPending == "play" {
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			c.pet.State = "idle"
			c.pet.LastThought = "where'd it go?"
		}
	}

	// === POOP MECHANICS ===

	// Check if pet needs to poop
	if !c.pet.NeedsPoopAt.IsZero() && now.After(c.pet.NeedsPoopAt) {
		poopX := c.pet.Pos.X
		c.pet.PoopPositions = append(c.pet.PoopPositions, poopX)
		c.pet.LastPoop = now
		c.pet.NeedsPoopAt = time.Time{}
		c.pet.LastThought = randomThought("poop") // instant fallback; LLM upgrades it below
		c.triggerPetEventThought("poop")
		// Move away from poop after placing it
		if c.pet.Pos.X > maxX/2 {
			c.pet.TargetPos = pos2D{X: c.pet.Pos.X - 3, Y: 0}
		} else {
			c.pet.TargetPos = pos2D{X: c.pet.Pos.X + 3, Y: 0}
		}
		c.pet.HasTarget = true
		c.pet.State = "walking"
	}

	// === POSITION CLAMPING ===

	if c.pet.Pos.X > maxX {
		c.pet.Pos.X = maxX
	}
	if c.pet.Pos.X < 0 {
		c.pet.Pos.X = 0
	}
	if c.pet.TargetPos.X > maxX {
		c.pet.TargetPos.X = maxX
	}
	if c.pet.TargetPos.X < 0 {
		c.pet.TargetPos.X = 0
	}

	// === TARGET MOVEMENT ===

	if c.pet.HasTarget {
		// Move pet toward target X
		nextX := c.pet.Pos.X
		if c.pet.Pos.X < c.pet.TargetPos.X {
			nextX++
			c.pet.Direction = 1
		} else if c.pet.Pos.X > c.pet.TargetPos.X {
			nextX--
			c.pet.Direction = -1
		}

		// Check if next position has poop - if so, jump over it!
		isPoopAhead := false
		for _, poopX := range c.pet.PoopPositions {
			if poopX == nextX || poopX == nextX+1 || poopX == nextX-1 {
				isPoopAhead = true
				break
			}
		}

		isDragonAhead := false
		if c.pet.DragonState != "" && c.pet.DragonPos.Y == c.pet.Pos.Y {
			dist := nextX - c.pet.DragonPos.X
			if dist < 0 {
				dist = -dist
			}
			if dist < 2 {
				isDragonAhead = true
			}
		}

		if (isPoopAhead || isDragonAhead) && c.pet.Pos.Y == 0 {
			// Jump over the obstacle!
			c.pet.Pos.Y = 2
			c.pet.State = "jumping"
			if isPoopAhead {
				c.pet.LastThought = randomThought("poop_jump")
			}
		}

		// Clamp after move
		if nextX > maxX {
			nextX = maxX
		}
		if nextX < 0 {
			nextX = 0
		}

		c.pet.Pos.X = nextX

		// If chasing yarn, push it or catch it when reached
		if c.pet.ActionPending == "play" {
			yarnX := c.pet.YarnPos.X
			if yarnX < 0 {
				yarnX = width - 4
			}
			// Pet reached yarn
			if c.pet.Pos.X == yarnX || c.pet.Pos.X == yarnX-1 || c.pet.Pos.X == yarnX+1 {
				// After 2 pushes, catch the yarn
				if c.pet.YarnPushCount >= 2 {
					// Catch the yarn - don't push, let the target be reached
					c.pet.TargetPos = c.pet.Pos // Target reached
				} else {
					// Push the yarn
					newYarnX := yarnX + c.pet.Direction*2
					if newYarnX >= 2 && newYarnX < width-2 {
						c.pet.YarnPos.X = newYarnX
						c.pet.YarnPos.Y = 1 // Bounce up
						c.pet.TargetPos.X = newYarnX
						c.pet.YarnPushCount++
					} else {
						// Can't push further, catch it
						c.pet.TargetPos = c.pet.Pos
					}
				}
			}
		}

		// Check if reached target
		if c.pet.Pos.X == c.pet.TargetPos.X && c.pet.Pos.Y == c.pet.TargetPos.Y {
			c.pet.HasTarget = false
			switch c.pet.ActionPending {
			case "eat":
				c.pet.Hunger = 100
				c.pet.State = "eating"
				c.pet.LastFed = now
				c.pet.TotalFeedings++
				c.pet.LastThought = "nom nom nom"
				c.pet.FoodItem = pos2D{X: -1, Y: -1}
				// Schedule potential poop based on config chance (default 50%)
				poopChance := c.config.Widgets.Pet.PoopChance
				if poopChance <= 0 {
					poopChance = 50
				}
				if rand.Intn(100) < poopChance {
					c.pet.NeedsPoopAt = now.Add(time.Duration(3+rand.Intn(5)) * time.Second)
				}
			case "play":
				c.pet.State = "playing"
				if c.pet.Happiness < 100 {
					c.pet.Happiness += 5
					if c.pet.Happiness > 100 {
						c.pet.Happiness = 100
					}
				}
				c.pet.TotalYarnPlays++
				c.pet.LastThought = "got it!"
				// Yarn disappears when caught
				c.pet.YarnPos = pos2D{X: -1, Y: 0}
				c.pet.YarnExpiresAt = time.Time{}
				c.pet.YarnPushCount = 0
			default:
				c.pet.State = "idle"
			}
			c.pet.ActionPending = ""
		}
	} else if c.pet.State == "eating" || c.pet.State == "playing" || c.pet.State == "happy" || c.pet.State == "shooting" {
		// Return to idle after a few frames
		if c.pet.AnimFrame%20 == 0 {
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("idle")
		}
	} else if c.pet.State == "sleeping" {
		// Wake up after longer duration (~5-10 seconds at 10fps = 50-100 frames)
		if c.pet.AnimFrame%60 == 0 && rand.Intn(100) < 30 {
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("wakeup")
		}
	}

	// === FLOATING ITEMS ===

	var activeItems []floatingItem
	for _, item := range c.pet.FloatingItems {
		if now.Before(item.ExpiresAt) {
			item.Pos.X += item.Velocity.X
			item.Pos.Y += item.Velocity.Y
			// Keep in bounds
			if item.Pos.X >= 0 && item.Pos.X < width && item.Pos.Y >= 0 && item.Pos.Y <= 2 {
				activeItems = append(activeItems, item)
			}
		}
	}
	c.pet.FloatingItems = activeItems

	// === RANDOM BEHAVIORS (cat mood) ===

	if c.pet.State == "idle" && !c.pet.HasTarget && c.pet.AnimFrame%10 == 0 {
		// Time-based sleeping: cats sleep more at night (2am-6am has 80% sleep chance)
		hour := now.Hour()
		if hour >= 2 && hour < 6 && rand.Intn(100) < 80 {
			c.pet.State = "sleeping"
			c.pet.LastThought = randomThought("sleepy")
		} else {
			// Configurable chance to do something every 10 frames (default: 15%)
			actionChance := c.config.Widgets.Pet.ActionChance
			if actionChance <= 0 {
				actionChance = 15 // Default: less hyper than before
			}
			if rand.Intn(100) < actionChance {
				action := rand.Intn(10)
				switch action {
				case 0:
					// Run across the screen (avoid poop as destination)
					c.pet.State = "walking"
					c.pet.Direction = []int{-1, 1}[rand.Intn(2)]
					targetX := rand.Intn(maxX)
					// Avoid selecting a position with poop as target
					for attempts := 0; attempts < 5; attempts++ {
						hasPoop := false
						for _, poopX := range c.pet.PoopPositions {
							if abs(targetX-poopX) <= 1 {
								hasPoop = true
								break
							}
						}
						if !hasPoop {
							break
						}
						targetX = rand.Intn(maxX) // Try another position
					}
					c.pet.TargetPos = pos2D{X: targetX, Y: 0}
					c.pet.HasTarget = true
					c.pet.LastThought = randomThought("walking")
				case 1:
					// Jump in place
					c.pet.State = "jumping"
					c.pet.Pos.Y = 2
					c.pet.LastThought = randomThought("jumping")
				case 2:
					// Chase the yarn
					if c.pet.YarnPos.X >= 0 {
						c.pet.TargetPos = pos2D{X: c.pet.YarnPos.X, Y: 0}
						c.pet.HasTarget = true
						c.pet.ActionPending = "play"
						c.pet.State = "walking"
						c.pet.LastThought = "yarn calls to me."
					}
				case 3:
					// Bat at yarn (toss it) - avoid poop positions
					tossX := safeRandRange(2, maxX)
					for attempts := 0; attempts < 5; attempts++ {
						hasPoop := false
						for _, poopX := range c.pet.PoopPositions {
							if abs(tossX-poopX) <= 1 {
								hasPoop = true
								break
							}
						}
						if !hasPoop {
							break
						}
						tossX = safeRandRange(2, maxX)
					}
					c.pet.YarnPos = pos2D{X: tossX, Y: 2}
					c.pet.YarnExpiresAt = now.Add(15 * time.Second)
					c.pet.YarnPushCount = 0
					c.pet.TargetPos = pos2D{X: tossX, Y: 0}
					c.pet.HasTarget = true
					c.pet.ActionPending = "play"
					c.pet.State = "walking"
					c.pet.LastThought = "chaos time."
				case 4:
					// Just be happy
					c.pet.State = "happy"
					c.pet.LastThought = randomThought("happy")
				case 5:
					// SHOOT A BANANA!
					c.pet.State = "shooting"
					dir := c.pet.Direction
					if dir == 0 {
						dir = 1
					}
					// Gun appears in the direction the pet is facing, offset by 2 to account for emoji width
					gunX := c.pet.Pos.X + (dir * 2)
					if gunX < 0 {
						gunX = 0
					}
					if gunX > maxX {
						gunX = maxX
					}
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "🔫",
						Pos:       pos2D{X: gunX, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(1200 * time.Millisecond),
					})
					// BANG effect next to gun
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "💥",
						Pos:       pos2D{X: gunX + dir, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(400 * time.Millisecond),
					})
					// Banana flies from gun position
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "🍌",
						Pos:       pos2D{X: gunX + dir, Y: 1},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(3 * time.Second),
					})
					thoughts := []string{"pew pew.", "banana had it coming.", "nothing personal.", "the family sends regards."}
					c.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				case 6:
					// Toss random emoji with context-aware thoughts
					shinyThings := []struct {
						emoji    string
						thoughts []string
					}{
						{"⭐", []string{"a star!", "make a wish.", "star light, star bright."}},
						{"💫", []string{"dizzy.", "sparkly.", "ooh cosmic."}},
						{"✨", []string{"sparkles!", "so shiny.", "glitter everywhere."}},
						{"🎾", []string{"ball!", "must chase.", "tennis anyone?"}},
						{"🏀", []string{"bouncy.", "slam dunk.", "ball is life."}},
						{"🎈", []string{"balloon!", "pop it?", "don't let it fly away."}},
						{"🦋", []string{"butterfly!", "must catch.", "so graceful."}},
						{"🐟", []string{"fish!", "dinner?", "swimming in air."}},
						{"🍎", []string{"apple!", "healthy snack.", "one a day."}},
						{"🧀", []string{"cheese!", "yes please.", "gouda choice."}},
					}
					choice := shinyThings[rand.Intn(len(shinyThings))]
					startX := safeRandRange(2, maxX)
					dir := []int{-1, 1}[rand.Intn(2)]
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     choice.emoji,
						Pos:       pos2D{X: startX, Y: 2},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(3 * time.Second),
					})
					c.pet.LastThought = choice.thoughts[rand.Intn(len(choice.thoughts))]
				case 7:
					// Menacing stare
					emojis := []string{"👁️", "🔪", "💀", "🎯"}
					emoji := emojis[rand.Intn(len(emojis))]
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     emoji,
						Pos:       pos2D{X: c.pet.Pos.X, Y: 2},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(2 * time.Second),
					})
					thoughts := []string{"watching.", "always watching.", "i see you.", "the family knows."}
					c.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				case 8:
					// Spawn a mouse! (if not already present)
					if c.pet.MousePos.X < 0 {
						// Mouse appears at edge of screen
						c.pet.MouseDirection = []int{-1, 1}[rand.Intn(2)]
						if c.pet.MouseDirection == 1 {
							c.pet.MousePos = pos2D{X: 0, Y: 0}
						} else {
							c.pet.MousePos = pos2D{X: maxX, Y: 0}
						}
						c.pet.LastThought = randomThought("mouse_spot")
					}
				case 9:
					// Start an adventure! (if enabled and happy enough)
					if adventureEnabled && c.pet.Happiness >= 50 && !c.pet.Adventure.Active {
						c.startAdventure(maxX)
					}
				}
			}
		}
	}

	// === MOUSE MECHANICS ===

	// Check if it's time to spawn a mouse
	if c.pet.MousePos.X < 0 && !c.pet.MouseAppearsAt.IsZero() && now.After(c.pet.MouseAppearsAt) {
		// Mouse appears at edge of screen
		c.pet.MouseDirection = []int{-1, 1}[rand.Intn(2)]
		if c.pet.MouseDirection == 1 {
			c.pet.MousePos = pos2D{X: 0, Y: 0}
		} else {
			c.pet.MousePos = pos2D{X: maxX, Y: 0}
		}
		c.pet.MouseAppearsAt = time.Time{} // Clear timer
		c.pet.LastThought = randomThought("mouse_spot")
	}

	// If no mouse and no timer set, schedule one (30-90 seconds)
	if c.pet.MousePos.X < 0 && c.pet.MouseAppearsAt.IsZero() {
		c.pet.MouseAppearsAt = now.Add(time.Duration(30+rand.Intn(60)) * time.Second)
	}

	// If there's a mouse, handle mouse behavior
	if c.pet.MousePos.X >= 0 {
		// Mouse runs away from pet
		dist := c.pet.MousePos.X - c.pet.Pos.X
		if dist < 0 {
			dist = -dist
		}

		// If pet catches mouse (within 2 cells), celebrate and remove mouse
		if dist <= 2 && c.pet.Pos.Y == 0 {
			c.pet.MousePos = pos2D{X: -1, Y: 0}
			c.pet.TotalMouseCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+20)
			c.pet.State = "happy"
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			// Creative kill thought!
			c.pet.LastThought = randomThought("mouse_kill")
		} else {
			// Mouse moves away from pet (every 5 frames)
			if c.pet.AnimFrame%5 == 0 {
				// Mouse tries to run away from pet
				if c.pet.MousePos.X < c.pet.Pos.X {
					c.pet.MouseDirection = -1 // Run left
				} else {
					c.pet.MouseDirection = 1 // Run right
				}
				c.pet.MousePos.X += c.pet.MouseDirection

				// If mouse reaches edge, it escapes
				if c.pet.MousePos.X < 0 || c.pet.MousePos.X > maxX {
					c.pet.MousePos = pos2D{X: -1, Y: 0}
					c.pet.LastThought = "it got away..."
					c.pet.HasTarget = false
					c.pet.ActionPending = ""
				}
			}

			// Pet chases mouse (if not already doing something else important)
			if !c.pet.HasTarget && c.pet.MousePos.X >= 0 {
				c.pet.TargetPos = pos2D{X: c.pet.MousePos.X, Y: 0}
				c.pet.HasTarget = true
				c.pet.ActionPending = "hunt"
				c.pet.State = "walking"
				if c.pet.AnimFrame%20 == 0 {
					c.pet.LastThought = randomThought("mouse_chase")
				}
			}
		}
	}

	// === HUNGER/HAPPINESS DECAY ===
	// Only decay when at least one renderer is connected
	c.clientWidthsMu.RLock()
	hasConnectedClients := len(c.clientWidths) > 0
	c.clientWidthsMu.RUnlock()

	if !c.petIsOwner {
		// Not the pet writer: hunger/happiness already came from disk via
		// loadPersistentPetStats. Do NOT decay or touch the anchors — the owning
		// daemon does that; touching them here would fight the owner.
	} else if !hasConnectedClients {
		// No renderer attached: pause decay and keep the anchors current so a long
		// detach doesn't dump a backlog of decrements the moment a client reattaches.
		c.pet.LastHungerTick = now
		c.pet.LastHappyTick = now
	} else {
		// Wall-clock decay anchored to a persisted tick time. This replaces the old
		// AnimFrame%frames coupling, whose decay rate silently tracked the animation
		// FPS and, combined with attach-gated frame counting, made "how fast does the
		// pet get hungry" hard to reason about. Now it is exactly one hunger point per
		// HungerDecay seconds of attached time, and LastHungerTick persists across the
		// (frequent) daemon reloads so a restart isn't a reset.
		hungerDecaySec := c.config.Widgets.Pet.HungerDecay
		if hungerDecaySec <= 0 {
			hungerDecaySec = 1728 // ~2 days to starve at the default
		}
		hungerInterval := time.Duration(hungerDecaySec) * time.Second
		if c.pet.LastHungerTick.IsZero() {
			c.pet.LastHungerTick = now
		}
		if c.pet.LastHappyTick.IsZero() {
			c.pet.LastHappyTick = now
		}
		// Cap catch-up so a stale anchor (host asleep, daemon down for days) can't
		// drain the whole bar in one tick — decay at most a few points per update.
		const maxCatchup = 5
		for n := 0; n < maxCatchup && c.pet.Hunger > 0 && now.Sub(c.pet.LastHungerTick) >= hungerInterval; n++ {
			c.pet.Hunger--
			c.pet.LastHungerTick = c.pet.LastHungerTick.Add(hungerInterval)
		}
		if now.Sub(c.pet.LastHungerTick) >= hungerInterval {
			c.pet.LastHungerTick = now // discard any remaining backlog past the cap
		}
		// Happiness decays 1.5x faster, but only while hungry.
		happyInterval := hungerInterval * 2 / 3
		if happyInterval <= 0 {
			happyInterval = hungerInterval
		}
		if c.pet.Hunger < 15 {
			for n := 0; n < maxCatchup && c.pet.Happiness > 0 && now.Sub(c.pet.LastHappyTick) >= happyInterval; n++ {
				c.pet.Happiness--
				c.pet.LastHappyTick = c.pet.LastHappyTick.Add(happyInterval)
			}
			if now.Sub(c.pet.LastHappyTick) >= happyInterval {
				c.pet.LastHappyTick = now
			}
		} else {
			// Not hungry: keep the happy anchor fresh so it doesn't build a backlog
			// that dumps all at once the instant hunger dips below 30.
			c.pet.LastHappyTick = now
		}
	}

	// === DEATH / STARVATION MECHANICS ===

	// If already dead, just occasionally update thoughts and skip other state changes
	if c.pet.IsDead {
		if c.pet.AnimFrame%100 == 0 {
			c.pet.LastThought = randomThought("dead")
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Dead pet doesn't animate
	}

	// Track starvation time
	if c.pet.Hunger == 0 {
		if c.pet.StarvingStart.IsZero() {
			c.pet.StarvingStart = now
			c.pet.LastThought = randomThought("starving")
		}

		// After 60 seconds of starvation
		starvingDuration := now.Sub(c.pet.StarvingStart)
		if starvingDuration > 60*time.Second {
			if c.config.Widgets.Pet.CanDie {
				// Pet dies
				c.pet.IsDead = true
				c.pet.DeathTime = now
				c.pet.State = "dead"
				c.pet.LastThought = "goodbye..."
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return true // State changed to dead
			} else {
				// Guilt trip mode - passive aggressive thoughts every 10 seconds
				if c.pet.AnimFrame%100 == 0 {
					c.pet.LastThought = randomThought("guilt")
				}
			}
		}
	} else {
		// Reset starvation tracking when fed
		if !c.pet.StarvingStart.IsZero() {
			c.pet.StarvingStart = time.Time{}
		}
	}

	// === LLM THOUGHT GENERATION ===

	// If LLM thoughts are enabled and pet is idle, occasionally get new thoughts
	if c.config.Widgets.Pet.Thoughts && c.pet.State == "idle" && !c.pet.IsDead {
		// Use configured interval or default to 30 seconds
		thoughtInterval := c.config.Widgets.Pet.ThoughtInterval
		if thoughtInterval <= 0 {
			thoughtInterval = 30
		}
		thoughtFrames := thoughtInterval * 10 // Convert seconds to frames (~10fps)
		if c.pet.AnimFrame%thoughtFrames == 0 {
			petName := c.config.Widgets.Pet.Name
			if petName == "" {
				petName = "Whiskers"
			}
			// Try to get an LLM thought (non-blocking, from buffer or triggers generation)
			if thought := generateLLMThought(&c.pet, petName); thought != "" {
				c.pet.LastThought = thought
				c.pet.ThoughtScroll = 0
				// Parse thought for action keywords and trigger matching behavior
				c.triggerActionFromThought(thought, maxX)
			}
		}
	}

	// === THOUGHT MARQUEE ===

	// Use config for thought scroll speed (default: 3 frames per scroll step)
	thoughtSpeed := c.config.Widgets.Pet.ThoughtSpeed
	if thoughtSpeed <= 0 {
		thoughtSpeed = 3
	}
	if c.pet.AnimFrame%thoughtSpeed == 0 {
		thoughtWidth := uniseg.StringWidth(c.pet.LastThought)
		maxThoughtWidth := width - 4
		if thoughtWidth > maxThoughtWidth {
			c.pet.ThoughtScroll++
			if c.pet.ThoughtScroll > thoughtWidth+3 {
				c.pet.ThoughtScroll = 0
			}
		} else {
			c.pet.ThoughtScroll = 0
		}
	}

	// === Q&A LOOP ===
	//
	// Drive the cat's "pending question" lifecycle from the existing tick
	// rather than spawning a new goroutine. Three things happen here:
	//
	//  1. Expire a stale pending question. If the user never clicked the
	//     teaser within the configured expiry window, drop it so the next
	//     pick picks something fresh. We do NOT roll the cooldown forward
	//     here — letting PickQuestion run on the same tick gives the user
	//     a new question immediately if the cooldown has also elapsed.
	//  2. Pick a new question when none is pending and the cooldown is up.
	//     Skip when the pet is in a "bad headspace" — dead, starving,
	//     adventuring — so the cat doesn't pester the user mid-crisis.
	//     PickQuestion further enforces opt-out and per-config gates.
	//  3. The teaser substitution itself lives in renderPetWidget; this
	//     block only mutates state, not visuals.
	//
	// Adventure mode already returned early above, but we re-check Active
	// defensively in case future changes reorder these blocks. We also
	// gate on IsDead and State so the cat stays quiet when starving.
	// Clear any pending question when QA is disabled in config — without
	// this the teaser keeps appearing until Expires elapses, even though
	// PickQuestion will refuse to surface a new one. Also clear when the
	// user has opted out at runtime, for the same reason.
	if c.pet.PendingQuestion != nil && (c.config.Widgets.Pet.QA.Disabled || c.pet.QAOptedOut) {
		c.pet.PendingQuestion = nil
	}
	if c.pet.PendingQuestion != nil && !c.pet.PendingQuestion.Expires.IsZero() && now.After(c.pet.PendingQuestion.Expires) {
		c.pet.PendingQuestion = nil
	}
	if c.pet.PendingQuestion == nil && !c.pet.IsDead && !c.pet.Adventure.Active &&
		c.pet.State != "dead" && c.pet.State != "starving" {
		wire := snapshotPetQAForWire(&c.pet)
		if picked := PickQuestion(&wire, c.config.Widgets.Pet.QA, now); picked != nil {
			wire.PendingQuestion = picked
			applyPetQAFromWire(&c.pet, &wire)
		}
	}
	// Phase-3 LLM distillation. Snapshot Q&A state and let the helper
	// decide if a pass should fire; the apply callback below re-takes
	// c.stateMu in its own critical section once the LLM call returns.
	snap := snapshotPetQAForWire(&c.pet)
	RunLLMDistillationBackground(&snap, c.config.Widgets.Pet.QA, c.config.Widgets.Pet.Name, now, c.applyLLMDistillation)

	petSnap := c.pet
	// Return true if any visual state changed
	changed := c.pet.Pos != prevPos ||
		c.pet.State != prevState ||
		c.pet.YarnPos != prevYarnPos ||
		len(c.pet.FloatingItems) != prevFloatingCount ||
		c.pet.MousePos != prevMousePos
	c.stateMu.Unlock()
	savePetStateData(petSnap)
	return changed
}

// startAdventure initiates a new adventure sequence
func (c *Coordinator) startAdventure(maxX int) {
	c.startAdventureWithOptions(maxX, false)
}

// startAdventureManual initiates a new adventure sequence manually (debug button)
func (c *Coordinator) startAdventureManual(maxX int) {
	c.startAdventureWithOptions(maxX, true)
}

// startAdventureWithOptions initiates a new adventure sequence
func (c *Coordinator) startAdventureWithOptions(maxX int, manual bool) {
	// Pick a random biome
	biomes := []string{"forest", "meadow", "garden", "backyard"}
	biome := biomes[rand.Intn(len(biomes))]

	c.pet.Adventure = adventureState{
		Active:            true,
		Phase:             advPhaseDeparting,
		PhaseStart:        time.Now(),
		PhaseDuration:     time.Duration(2+rand.Intn(2)) * time.Second,
		Biome:             biome,
		SceneOffset:       0,
		CatX:              c.pet.Pos.X,
		HomeX:             c.pet.Pos.X,
		ManuallyTriggered: manual,
		LastThought:       "adventure calls...",
	}
	c.pet.State = "walking"
	c.pet.Direction = 1 // Walking right (departing)
	c.pet.LastThought = "adventure calls..."
}

// updateAdventurePhase handles adventure state transitions and mechanics
func (c *Coordinator) updateAdventurePhase(now time.Time, maxX int) {
	adv := &c.pet.Adventure
	elapsed := now.Sub(adv.PhaseStart)

	switch adv.Phase {
	case advPhaseDeparting:
		if c.pet.AnimFrame%6 == 0 {
			adv.CatX++
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
		}
		if elapsed >= adv.PhaseDuration {
			// Transition to exploring
			adv.Phase = advPhaseExploring
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(10+rand.Intn(20)) * time.Second
			adv.CatX = maxX / 2 // Cat centered during exploration
			c.pet.LastThought = "exploring..."
		}

	case advPhaseExploring:
		// Scenery scrolls past, cat stays centered
		if c.pet.AnimFrame%4 == 0 {
			adv.SceneOffset++
		}

		// Random chance to encounter wildlife
		if adv.Wildlife == nil && rand.Intn(100) < 3 {
			c.spawnWildlife(maxX)
		}

		// Check for transition to encounter or returning
		if adv.Wildlife != nil {
			adv.Phase = advPhaseEncounter
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(10+rand.Intn(10)) * time.Second
		} else if elapsed >= adv.PhaseDuration {
			// No encounter, start returning
			adv.Phase = advPhaseReturning
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(6+rand.Intn(6)) * time.Second
			c.pet.Direction = -1
			c.pet.LastThought = "heading home..."
		}

	case advPhaseEncounter:
		c.updateEncounter(now, maxX)

		// Check if encounter is resolved
		if adv.Wildlife != nil && (adv.Wildlife.Caught || adv.Wildlife.Escaped) {
			// Brief pause then return
			if elapsed >= adv.PhaseDuration {
				adv.Phase = advPhaseReturning
				adv.PhaseStart = now
				adv.PhaseDuration = time.Duration(6+rand.Intn(6)) * time.Second
				adv.Wildlife = nil
				c.pet.Direction = -1
				c.pet.LastThought = "heading home..."
			}
		} else if elapsed >= adv.PhaseDuration*2 {
			// Encounter timed out without resolution — give up and return home
			adv.Phase = advPhaseReturning
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(6+rand.Intn(6)) * time.Second
			adv.Wildlife = nil
			c.pet.Direction = -1
			c.pet.LastThought = "heading home..."
		}

	case advPhaseReturning:
		// Scenery scrolls back
		if c.pet.AnimFrame%4 == 0 && adv.SceneOffset > 0 {
			adv.SceneOffset--
		}

		// Only use timer to transition — SceneOffset may be small if encounter
		// happened early in exploration, which would otherwise skip this phase.
		if elapsed >= adv.PhaseDuration {
			adv.Phase = advPhaseArriving
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(2+rand.Intn(4)) * time.Second
			adv.CatX = maxX
		}

	case advPhaseArriving:
		// Cat walks back to home position
		if c.pet.AnimFrame%6 == 0 && adv.CatX > adv.HomeX {
			adv.CatX--
		}

		if elapsed >= adv.PhaseDuration || adv.CatX <= adv.HomeX {
			// Adventure complete!
			c.pet.Adventure = adventureState{
				TotalCatches: adv.TotalCatches,
			}
			c.pet.State = "happy"
			c.pet.LastThought = "good adventure."
		}
	}
}

// spawnWildlife creates a wildlife encounter based on current biome
func (c *Coordinator) spawnWildlife(maxX int) {
	adv := &c.pet.Adventure
	biome := adventureBiomes[adv.Biome]
	if len(biome.Wildlife) == 0 {
		return
	}

	// Pick random wildlife from biome
	wildlifeType := biome.Wildlife[rand.Intn(len(biome.Wildlife))]
	data := adventureWildlife[wildlifeType]

	adv.Wildlife = &wildlifeEncounter{
		Type:        wildlifeType,
		Emoji:       data.Emoji,
		X:           maxX,
		Y:           data.YLevel,
		Speed:       data.Speed,
		CatchChance: data.CatchChance,
	}

	// Get spot thought
	c.pet.LastThought = c.getAdventureThought(wildlifeType, "spot")
}

// updateEncounter handles the wildlife encounter mechanics
func (c *Coordinator) updateEncounter(now time.Time, maxX int) {
	adv := &c.pet.Adventure
	w := adv.Wildlife
	if w == nil {
		return
	}

	if w.Pounced {
		if w.PounceFrames > 0 {
			adv.CatX = w.X
			if adv.CatX < 0 {
				adv.CatX = 0
			}
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
			c.pet.State = "jumping"
			c.pet.Pos.Y = w.Y
			w.PounceFrames--
			return
		}

		if w.WillCatch {
			w.Caught = true
			adv.TotalCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+10)
			c.pet.Hunger = min(100, c.pet.Hunger+20)
			thought := c.getAdventureThought(w.Type, "catch")
			if thought == "" {
				thought = fmt.Sprintf("caught a %s!", w.Type)
			}
			c.pet.LastThought = thought
			c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
				Emoji:     "🏆",
				Pos:       pos2D{X: adv.CatX, Y: 2},
				Velocity:  pos2D{X: 0, Y: 0},
				ExpiresAt: now.Add(2 * time.Second),
			})
			c.spawnAdventureCatchFX(now, adv.CatX, w.Y)
			if c.config.Widgets.Pet.AdventureBlood {
				blood := "🩸"
				if c.config.Widgets.Pet.Icons.Blood != "" {
					blood = c.config.Widgets.Pet.Icons.Blood
				}
				if blood != "" {
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     blood,
						Pos:       pos2D{X: adv.CatX, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(1200 * time.Millisecond),
					})
				}
			}
		} else {
			w.Escaped = true
			thought := c.getAdventureThought(w.Type, "escape")
			if thought == "" {
				thought = fmt.Sprintf("the %s got away!", w.Type)
			}
			c.pet.LastThought = thought
		}

		c.pet.Pos.Y = 0
		c.pet.State = "idle"
		return
	}

	// Phase 1: Spotting (wildlife enters view)
	if !w.Spotted {
		if c.pet.AnimFrame%6 == 0 {
			w.X--
		}
		// Wildlife is spotted when it enters play area
		if w.X < maxX-2 {
			w.Spotted = true
			c.pet.State = "idle" // Cat freezes
			c.pet.LastThought = fmt.Sprintf("a %s!", w.Type)
			vibe := c.getAdventureVibe()
			switch vibe {
			case "subtle", "noir":
				w.Approach = 0
			case "anime":
				w.Approach = []int{2, 1, 2}[rand.Intn(3)]
			case "pixel":
				w.Approach = []int{1, 0, 1}[rand.Intn(3)]
			default:
				w.Approach = rand.Intn(3)
			}
			if w.Speed >= 3 && w.Approach < 1 {
				w.Approach = 1
			}
			if w.Speed >= 3 && w.Type == "bird" {
				w.Approach = 2
			}
			// Add "!" floating item above cat's actual position
			c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
				Emoji:     "❗",
				Pos:       pos2D{X: adv.CatX, Y: 2},
				Velocity:  pos2D{X: 0, Y: 0},
				ExpiresAt: now.Add(1500 * time.Millisecond),
			})
		}
		return
	}

	// Phase 2: Stalking (cat approaches)
	if !w.Stalking && w.Spotted && !w.Pounced {
		w.Stalking = true
		c.pet.State = "walking"
		c.pet.LastThought = c.getAdventureThought(w.Type, "stalk")
	}

	if w.Stalking && !w.Pounced {
		stepEvery := 10
		stopDist := 2
		pounceDist := 3
		if w.Approach == 1 {
			stepEvery = 6
			stopDist = 1
			pounceDist = 2
		} else if w.Approach == 2 {
			stepEvery = 4
			stopDist = 0
			pounceDist = 1
		}
		if c.pet.AnimFrame%stepEvery == 0 {
			if adv.CatX < w.X-stopDist {
				adv.CatX++
			}
		}

		moveEvery := 14
		if w.Speed >= 3 {
			moveEvery = 6
		} else if w.Speed == 2 {
			moveEvery = 10
		}
		if c.pet.AnimFrame%moveEvery == 0 {
			if adv.CatX <= w.X {
				w.X++
			} else {
				w.X--
			}
			w.X += rand.Intn(3) - 1
			if w.X < 3 {
				w.X = 3
			}
			if w.X > maxX+5 {
				w.X = maxX
			}
		}

		// Check if close enough to pounce
		dist := w.X - adv.CatX
		if dist < 0 {
			dist = -dist
		}
		if dist <= pounceDist {
			w.Pounced = true
			w.PounceFrames = 4
			w.WillCatch = rand.Intn(100) < w.CatchChance
			adv.CatX = w.X
			if adv.CatX < 0 {
				adv.CatX = 0
			}
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
			c.pet.State = "jumping"
			c.pet.Pos.Y = w.Y
			return
		}
	}
}

func (c *Coordinator) getAdventureVibe() string {
	v := strings.ToLower(strings.TrimSpace(c.config.Widgets.Pet.AdventureVibe))
	if v == "" {
		return "ridiculous"
	}
	return v
}

func (c *Coordinator) spawnAdventureCatchFX(now time.Time, contactX int, preyY int) {
	vibe := c.getAdventureVibe()
	add := func(emoji string, x, y int, vx, vy int, d time.Duration) {
		if emoji == "" {
			return
		}
		c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
			Emoji:     emoji,
			Pos:       pos2D{X: x, Y: y},
			Velocity:  pos2D{X: vx, Y: vy},
			ExpiresAt: now.Add(d),
		})
	}

	confetti := []string{"🎉", "✨", "💥", "⭐"}
	comic := []string{"💥", "⚡", "✨"}
	pixel := []string{"*", "+", "x"}

	switch vibe {
	case "subtle":
		return
	case "noir":
		add("✦", contactX, 2, 0, 0, 1200*time.Millisecond)
		add("·", contactX-1, 1, -1, 0, 900*time.Millisecond)
		add("·", contactX+1, 1, 1, 0, 900*time.Millisecond)
	case "pixel":
		for i := 0; i < 3; i++ {
			em := pixel[rand.Intn(len(pixel))]
			dx := []int{-1, 0, 1}[rand.Intn(3)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			add(em, contactX+dx, dy, dx, 0, 900*time.Millisecond)
		}
	case "anime":
		for i := 0; i < 4; i++ {
			em := comic[rand.Intn(len(comic))]
			dx := []int{-1, 1}[rand.Intn(2)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			add(em, contactX, dy, dx, 0, 700*time.Millisecond)
		}
		add("‼", contactX, 2, 0, 0, 600*time.Millisecond)
	default:
		for i := 0; i < 5; i++ {
			em := confetti[rand.Intn(len(confetti))]
			dx := []int{-2, -1, 0, 1, 2}[rand.Intn(5)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			vx := []int{-1, 0, 1}[rand.Intn(3)]
			add(em, contactX+dx, dy, vx, 0, 1200*time.Millisecond)
		}
		add("😹", contactX-1, 2, -1, 0, 900*time.Millisecond)
		add("🍖", contactX+1, 1, 1, 0, 900*time.Millisecond)
	}

	_ = preyY
}

// getAdventureThought returns a random thought for the given wildlife and phase
func (c *Coordinator) getAdventureThought(wildlife, phase string) string {
	if thoughts, ok := adventureThoughts[wildlife]; ok {
		if phaseThoughts, ok := thoughts[phase]; ok && len(phaseThoughts) > 0 {
			return phaseThoughts[rand.Intn(len(phaseThoughts))]
		}
	}
	return ""
}

// renderAdventurePlayArea renders the play area during an adventure
func (c *Coordinator) renderAdventurePlayArea(safePlayWidth int, petSprite string, sprites petSprites) (highAir, lowAir, ground string) {
	adv := &c.pet.Adventure
	biome := adventureBiomes[adv.Biome]

	// Get biome ground character
	groundChar := biome.Ground
	if groundChar == "" {
		groundChar = "·"
	}

	// Build sprite maps for each row
	highAirSprites := make(map[int]string)
	lowAirSprites := make(map[int]string)
	groundSprites := make(map[int]string)

	// Deterministic scenery placement based on scene offset
	// Place scenery elements at fixed intervals, offset by scroll position
	for i := 0; i < safePlayWidth; i++ {
		worldX := i + adv.SceneOffset
		// Ground scenery every 7 columns
		if worldX%7 == 0 && len(biome.Scenery) > 0 {
			idx := (worldX / 7) % len(biome.Scenery)
			emoji := biome.Scenery[idx]
			// Only place on ground if not a flying creature
			if emoji != "🦋" {
				groundSprites[i] = emoji
			}
		}
		// Air scenery every 11 columns (less frequent)
		if worldX%11 == 0 && len(biome.Scenery) > 0 {
			idx := (worldX / 11) % len(biome.Scenery)
			emoji := biome.Scenery[idx]
			// Butterflies and birds in air
			if emoji == "🦋" || emoji == "🐦" {
				lowAirSprites[i] = emoji
			}
		}
	}

	// Place wildlife if present
	if adv.Wildlife != nil && !adv.Wildlife.Escaped && !adv.Wildlife.Caught {
		w := adv.Wildlife
		wx := w.X
		if wx >= 0 && wx < safePlayWidth {
			switch w.Y {
			case 2:
				highAirSprites[wx] = w.Emoji
			case 1:
				lowAirSprites[wx] = w.Emoji
			default:
				groundSprites[wx] = w.Emoji
			}
		}
	}

	catX := adv.CatX
	if catX >= 0 && catX < safePlayWidth {
		if c.pet.Pos.Y >= 2 {
			highAirSprites[catX] = petSprite
		} else if c.pet.Pos.Y == 1 {
			lowAirSprites[catX] = petSprite
		} else {
			groundSprites[catX] = petSprite
		}
	}

	// Place dragon! The dragon follows on the adventure
	if c.pet.DragonState != "" {
		dragonX := c.pet.DragonPos.X
		if dragonX >= 0 && dragonX < safePlayWidth {
			dragonSprite := "🐉"
			if c.pet.DragonState == "sleeping" {
				dragonSprite = "💤"
			}

			// Anti-occlusion for adventure mode
			if c.pet.Pos.Y == 0 && c.pet.DragonPos.Y == 0 {
				if dragonX == catX+1 || dragonX == catX-1 || dragonX == catX {
					if dragonX >= catX {
						dragonX++
					} else {
						dragonX--
					}
				}
			}

			if dragonX >= 0 && dragonX < safePlayWidth {
				if c.pet.DragonPos.Y >= 2 {
					highAirSprites[dragonX] = dragonSprite
				} else if c.pet.DragonPos.Y == 1 {
					lowAirSprites[dragonX] = dragonSprite
				} else {
					groundSprites[dragonX] = dragonSprite
				}
			}
		}
	}

	// Place floating items (like "!" for spotting)
	for _, item := range c.pet.FloatingItems {
		if item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			switch item.Pos.Y {
			case 2:
				highAirSprites[item.Pos.X] = item.Emoji
			case 1:
				lowAirSprites[item.Pos.X] = item.Emoji
			default:
				groundSprites[item.Pos.X] = item.Emoji
			}
		}
	}

	// Build the rows
	highAir = buildAirRow(highAirSprites, safePlayWidth)
	lowAir = buildAirRow(lowAirSprites, safePlayWidth)
	ground = buildSpriteRow(groundSprites, groundChar, safePlayWidth)

	return highAir, lowAir, ground
}

// handleWidthSync checks if the current width matches global state and syncs if needed
func (c *Coordinator) handleWidthSync(clientID string, currentWidth int) {
	if isHeaderClient(clientID) {
		return
	}
	if c.sidebarHidden {
		return
	}

	// Query tmux BEFORE acquiring any lock to prevent deadlock if tmux hangs
	// Use a timeout context to prevent blocking forever
	activeWindowID := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
		activeWindowID = strings.TrimSpace(string(out))
	}
	isActive := (clientID == activeWindowID)

	// Read window sync setting under stateMu BEFORE acquiring widthSyncMu.
	// Lock ordering must always be: stateMu -> widthSyncMu. Acquiring widthSyncMu
	// first and then stateMu.RLock() inside it causes a deadlock with RefreshGit,
	// which holds stateMu.Lock() while git commands run, and then calls
	// BroadcastRender -> handleWidthSync -> widthSyncMu.
	syncWidth := false
	c.stateMu.RLock()
	for i := range c.windows {
		if c.windows[i].ID == clientID {
			syncWidth = c.windows[i].SyncWidth
			break
		}
	}
	c.stateMu.RUnlock()

	c.widthSyncMu.Lock()

	// Detect if this window just became active
	justBecameActive := isActive && c.lastActiveWindowID != clientID
	if justBecameActive {
		coordinatorDebugLog.Printf("Width sync: active window changed to %s", clientID)
	}

	// Debounce: ignore resize events within 500ms of our last sync
	// to avoid cascading syncs when we resize multiple panes
	sinceLast := time.Since(c.lastWidthSync)
	if sinceLast < 500*time.Millisecond {
		// Still update the active window tracker even if debounced
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		c.widthSyncMu.Unlock()
		return
	}

	if !syncWidth {
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		c.widthSyncMu.Unlock()
		return
	}

	if c.globalWidth == 0 {
		c.globalWidth = currentWidth
	}

	// If the active window's sidebar was resized by the user, adopt as new global width.
	// Only reject widths below the absolute minimum (broken state).
	if currentWidth < 10 {
		coordinatorDebugLog.Printf("Width sync: %s below minimum (%d), restoring to global %d", clientID, currentWidth, c.globalWidth)
		c.lastWidthSync = time.Now()
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		targetWidth := c.globalWidth
		if targetWidth < 10 {
			targetWidth = 25
		}
		c.widthSyncMu.Unlock()

		listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer listCancel()
		if out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", clientID, "-F", "#{pane_id}|#{pane_current_command}|#{pane_start_command}").Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(line, "|", 3)
				if len(parts) >= 3 && isSidebarPaneCommand(parts[1], parts[2]) {
					coordinatorDebugLog.Printf("RESIZE_SIDEBAR pane=%s width=%d (inactive sync)", parts[0], targetWidth)
					resizeCtx, resizeCancel := context.WithTimeout(context.Background(), 2*time.Second)
					exec.CommandContext(resizeCtx, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", targetWidth)).Run()
					resizeCancel()
					break
				}
			}
		}
		return
	}

	// User manually resized the active window's sidebar — adopt as new global width.
	// Clamp to a window-relative hard ceiling first, so a too-wide drag (or a
	// spurious large width report during layout thrash) can't become a permanent,
	// self-reinforcing global width that spreads to every window. When we clamp
	// DOWN, the pane is still at the oversized width, so we also resize it back to
	// the clamped value — otherwise the oversized pane keeps re-triggering this
	// adopt path on every sync and the sidebar never shrinks.
	if isActive && currentWidth != c.globalWidth && currentWidth >= 10 {
		adopted := currentWidth
		if ceiling, ok := c.sidebarHardCeilingForWindow(clientID); ok && adopted > ceiling {
			adopted = ceiling
		}
		coordinatorDebugLog.Printf("Width sync: user resized active sidebar %s from %d to %d (adopt %d), updating global", clientID, c.globalWidth, currentWidth, adopted)
		c.globalWidth = adopted
		c.lastWidthSync = time.Now()
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", adopted)).Run()
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		needResize := adopted != currentWidth
		c.widthSyncMu.Unlock()
		c.persistSidebarWidthProfile(clientID, adopted)
		if needResize {
			listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer listCancel()
			if out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", clientID, "-F", "#{pane_id}|#{pane_current_command}|#{pane_start_command}").Output(); err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					parts := strings.SplitN(line, "|", 3)
					if len(parts) >= 3 && isSidebarPaneCommand(parts[1], parts[2]) {
						coordinatorDebugLog.Printf("RESIZE_SIDEBAR pane=%s width=%d (adopt clamp)", parts[0], adopted)
						resizeCtx, resizeCancel := context.WithTimeout(context.Background(), 2*time.Second)
						exec.CommandContext(resizeCtx, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", adopted)).Run()
						resizeCancel()
						break
					}
				}
			}
		}
		return
	}

	currentHeight := 0
	c.clientWidthsMu.RLock()
	if c.clientHeights != nil {
		currentHeight = c.clientHeights[clientID]
	}
	c.clientWidthsMu.RUnlock()
	targetWidth := c.boundedSidebarWidthForWindow(clientID, c.globalWidth, currentHeight)
	if currentHeight > 0 {
		keyboardWidth, keyboardThreshold := c.getMobileKeyboardSettings()
		if currentHeight <= keyboardThreshold && targetWidth > keyboardWidth {
			targetWidth = keyboardWidth
		}
	}
	if currentWidth == targetWidth {
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		c.widthSyncMu.Unlock()
		return
	}

	if justBecameActive {
		c.lastActiveWindowID = clientID
	}
	c.lastWidthSync = time.Now()
	coordinatorDebugLog.Printf("Width sync: window=%s current=%d target=%d", clientID, currentWidth, targetWidth)
	c.widthSyncMu.Unlock()

	listCtx2, listCancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer listCancel2()
	if out, err := exec.CommandContext(listCtx2, "tmux", "list-panes", "-t", clientID, "-F", "#{pane_id}|#{pane_current_command}|#{pane_start_command}").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(line, "|", 3)
			if len(parts) >= 3 && isSidebarPaneCommand(parts[1], parts[2]) {
				coordinatorDebugLog.Printf("RESIZE_SIDEBAR pane=%s width=%d (active sync)", parts[0], targetWidth)
				resizeCtx2, resizeCancel2 := context.WithTimeout(context.Background(), 2*time.Second)
				exec.CommandContext(resizeCtx2, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", targetWidth)).Run()
				resizeCancel2()
				break
			}
		}
	}
}

// Profile transition debounce: during client resize animations (especially
// phone connect), the active client width can change rapidly through many
// intermediate values. We delay profile transitions by profileTransitionDelay
// and cancel if the profile changes back, preventing stash/restore thrash.
var (
	profileTransitionMu    sync.Mutex
	profileTransitionTimer *time.Timer
	pendingProfile         string
)

const profileTransitionDelay = 750 * time.Millisecond

// SetActiveClientWidth records the currently-focused physical tmux client's
// terminal width (in cols). RunWidthSync caps sidebar targets against this so
// that we never ask tmux for more cols than the active client can honor.
//
// When the width transitions across the phone/desktop boundary, we schedule
// a debounced profile transition to avoid reacting to intermediate sizes
// during client resize animations.
func (c *Coordinator) SetActiveClientWidth(w int) {
	prev := int(c.activeClientWidth.Load())
	c.activeClientWidth.Store(int64(w))
	// Keep the snapshot's Width in sync; callers that only have a width
	// (not a full ActiveClient) land here. We swap a fresh struct so readers
	// observe a consistent snapshot (no torn fields).
	cur := c.activeClient.Load()
	var ac daemon.ActiveClient
	if cur != nil {
		ac = *cur
	}
	ac.Width = w
	ac.Profile = c.computeProfile(w)
	c.activeClient.Store(&ac)

	c.maybeScheduleProfileTransition(prev, w)
}

// maybeScheduleProfileTransition compares the profile implied by prevWidth and
// newWidth, and if they differ (or prevWidth is 0, meaning initial boot),
// schedules a debounced PROFILE_TRANSITION_FIRE on the shared timer. Shared
// by SetActiveClientWidth (the resize hook path) and SetActiveClient (the
// geometry-tick elector path) so both routes through which the active client
// can change observe phone⇄desktop flips and run executeProfileTransition.
//
// Without this shared call site, the geometry-tick path (the only one that
// fires on tmux client-attached, e.g. on reattach) would silently flip the
// stored profile and skip executeProfileTransition's stashed-sidebar restore.
func (c *Coordinator) maybeScheduleProfileTransition(prevWidth, newWidth int) {
	prevProfile := c.computeProfile(prevWidth)
	newProfile := c.computeProfile(newWidth)
	// On initial boot (prevWidth == 0), always fire the profile action so
	// phone clients get their sidebar auto-stashed even though computeProfile(0)
	// and computeProfile(<100) are both "phone".
	if prevProfile == newProfile && prevWidth != 0 {
		return
	}

	status := c.NewWindowStatus()
	if status.State != "none" {
		logEvent("PROFILE_TRANSITION_SUPPRESSED prev=%s new=%s target=%s state=%s",
			prevProfile, newProfile, status.WindowID, status.State)
		return
	}

	profileTransitionMu.Lock()
	if profileTransitionTimer != nil {
		profileTransitionTimer.Stop()
	}
	pendingProfile = newProfile
	logEvent("PROFILE_TRANSITION_SCHEDULED prev=%s new=%s delay=%dms",
		prevProfile, newProfile, profileTransitionDelay.Milliseconds())
	profileTransitionTimer = time.AfterFunc(profileTransitionDelay, func() {
		profileTransitionMu.Lock()
		target := pendingProfile
		pendingProfile = ""
		profileTransitionTimer = nil
		profileTransitionMu.Unlock()

		currentProfile := c.ActiveClientProfile()
		if target != currentProfile {
			logEvent("PROFILE_TRANSITION_STALE scheduled=%s current=%s action=skip", target, currentProfile)
			return
		}

		logEvent("PROFILE_TRANSITION_FIRE target=%s", target)
		c.executeProfileTransition(target)
	})
	profileTransitionMu.Unlock()
}

func (c *Coordinator) executeProfileTransition(newProfile string) {
	if c.OnRefreshLayout != nil {
		go c.OnRefreshLayout()
	}

	if newProfile == "desktop" {
		narrow := hasNarrowClient()
		stashed := sidebarIsStashed()
		logEvent("PROFILE_TRANSITION_DESKTOP new=%s hasNarrow=%v stashed=%v sidebarHidden=%v",
			newProfile, narrow, stashed, c.sidebarHidden)
		if narrow {
			logEvent("PROFILE_TRANSITION_SKIP_DESKTOP reason=phone_client_still_attached")
		} else {
			// If a phone full-width sidebar is open, restore its content first — a
			// desktop client must never be shown a content-less window.
			if fs := fullscreenSidebarActiveWindowID(c.dashboardSession()); fs != "" {
				c.closeFullscreenSidebar(fs, false)
				coordinatorDebugLog.Printf("profile transition phone->desktop: closed full-width sidebar %s", fs)
			}
			// Kill the phone window-header (button bar) immediately, in the same
			// tick as the sidebar restore. spawnWindowHeaders would eventually do
			// this on its own, but only after loopFullRefreshCooldown elapses —
			// leaving the bar visible alongside a restored sidebar in the
			// interim.
			if c.OnKillPhoneWindowHeaders != nil {
				go c.OnKillPhoneWindowHeaders()
			}
			if stashed {
				c.sidebarHidden = false
				go func() {
					c.restoreSidebarPanes()
					coordinatorDebugLog.Printf("profile transition phone->desktop: auto-restored stashed sidebars")
				}()
			}
		}
	}
	if newProfile == "phone" {
		// The gathered dashboard grid is unusable on a phone screen — auto-exit
		// it so the user lands back in their normal windows. Run synchronously
		// (this is the profile-transition timer goroutine, not the main loop) so
		// origin windows are restored before the phone sidebar-stash below acts
		// on them. iPads classify as "desktop" (wider than the phone threshold),
		// so this only triggers for true phone-width clients.
		if dashboardActiveWindowID(c.dashboardSession()) != "" {
			c.exitDashboard()
			coordinatorDebugLog.Printf("profile transition -> phone: auto-exited dashboard")
		}

		// Always mark sidebarHidden=true when entering phone profile, even if
		// sidebars are already physically stashed (e.g. daemon restart with
		// existing stash windows). Without this, sidebarHidden stays false and
		// spawnRenderersForNewWindows incorrectly spawns sidebars for windows
		// created with -no-sidebar, causing visible layout thrash.
		c.sidebarHidden = true
		if !sidebarIsStashed() {
			go func() {
				c.hideSidebarPanes()
				coordinatorDebugLog.Printf("profile transition desktop->phone: auto-stashed sidebars")
			}()
		} else {
			logEvent("PROFILE_TRANSITION_PHONE_ALREADY_STASHED sidebarHidden=true (no-op hide)")
		}
	}
}

// hasNarrowClient checks if the ACTIVE tmux client has a phone-sized width.
// Uses activeClientGeometry() to determine the true active client, not just
// any attached client. This prevents spurious sidebar restoration bugs when
// both phone and desktop clients are attached.
func hasNarrowClient() bool {
	width, _, tty, _, ok := activeClientGeometry()
	if !ok {
		logEvent("HASNARROW_CLIENT_NO_ACTIVE_CLIENT")
		return false
	}

	isNarrow := width > 0 && width < 100
	logEvent("HASNARROW_CLIENT_ACTIVE tty=%s width=%d narrow=%v", tty, width, isNarrow)
	return isNarrow
}

// sidebarStashWindowPrefix is the tmux window-name prefix used to park sidebar
// panes off-screen while keeping the sidebar-renderer process alive. On hide we
// call tmux break-pane to move each sidebar pane to its own stash window named
// "{prefix}{window_id_without_@}". On show we list stash windows, match them
// back to the original window, and join-pane them in at the left edge.
const sidebarStashWindowPrefix = "_tabby_stash_"

// contentStashWindowPrefix names holding windows for CONTENT panes parked while a
// window is in full-width-sidebar mode (phone). Kept DISTINCT from
// sidebarStashWindowPrefix so the sidebar stash/restore paths and the content
// stash/restore paths never touch each other's windows. Content stashes live in
// the same detached _tabby_limbo session.
const contentStashWindowPrefix = "_tabby_content_"

func contentStashNameForWindow(windowID string) string {
	return contentStashWindowPrefix + strings.TrimPrefix(windowID, "@")
}

// sidebarStashParkBase is the base tmux window index used for sidebar stash
// windows INSIDE the limbo session (see sidebarLimboSession). Stashes are
// appended at indices >= this value purely to keep them clustered and away
// from index 0 (the limbo session's placeholder window). They no longer need
// to dodge the user session's visual numbering — stashes live in a separate
// session now — but a high base keeps `move-window -a` collision-free.
const sidebarStashParkBase = 9000

// sidebarLimboSession is a dedicated DETACHED tmux session that holds stashed
// sidebar panes while the sidebar is hidden on mobile. Parking the stashes in
// a separate session (instead of high-index windows in the user's own session)
// makes them unreachable from every NATIVE window-cycling path — next/prev
// window, prefix-n/p, choose-tree -w, status-bar clicks — since those are all
// session-scoped. The sidebar-renderer processes keep running here; the
// hamburger "show sidebar" button join-panes them back. The session is created
// on demand (ensureLimboSession) and torn down once empty
// (cleanupLimboSessionIfEmpty). It is invisible to tabby's own window
// management, which is scoped to the daemon's session via SetSessionTarget.
const sidebarLimboSession = "_tabby_limbo"

// ensureLimboSession creates the detached limbo holding session if it does not
// already exist, and pins destroy-unattached off so tmux never reaps it while
// it is parking live sidebar panes.
func ensureLimboSession() {
	if err := exec.Command("tmux", "has-session", "-t", sidebarLimboSession).Run(); err == nil {
		return // already exists
	}
	// Give the detached session an explicit size so tmux's
	// clients_calculate_size path doesn't dereference a null client during
	// spawn (the same null-deref guarded against in tabby.tmux).
	if err := exec.Command("tmux", "new-session", "-d", "-s", sidebarLimboSession, "-x", "80", "-y", "24").Run(); err != nil {
		coordinatorDebugLog.Printf("ensureLimboSession: new-session failed: %v", err)
		return
	}
	exec.Command("tmux", "set-option", "-t", sidebarLimboSession, "destroy-unattached", "off").Run()
	coordinatorDebugLog.Printf("ensureLimboSession: created %s", sidebarLimboSession)
}

// cleanupLimboSessionIfEmpty kills the limbo session once no stash windows
// remain anywhere (across all sessions). This also removes the placeholder
// window new-session created, so the limbo session stops appearing in the
// native session chooser the moment the sidebar is fully restored.
func cleanupLimboSessionIfEmpty() {
	if sidebarIsStashed() || limboHasContentStash() {
		return // sidebar OR content stashes still parked — keep the holder alive
	}
	if err := exec.Command("tmux", "has-session", "-t", sidebarLimboSession).Run(); err == nil {
		exec.Command("tmux", "kill-session", "-t", sidebarLimboSession).Run()
		coordinatorDebugLog.Printf("cleanupLimboSessionIfEmpty: killed %s", sidebarLimboSession)
	}
}

// limboHasContentStash reports whether any full-width-sidebar CONTENT stash window
// exists anywhere — used so cleanupLimboSessionIfEmpty never tears down the holder
// (and the stashed content inside it) while a window is in full-width mode.
func limboHasContentStash() bool {
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_name}").Output()
	if err != nil {
		return false
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(name, contentStashWindowPrefix) {
			return true
		}
	}
	return false
}

// minimizedHoldingSession is a dedicated DETACHED session that holds MINIMIZED
// windows (the whole window, panes intact). Because every native window-cycling
// path — next-window, previous-window, prefix-n/p, choose-tree -w — is
// session-scoped, a window parked here is unreachable from ALL of them, for any
// caller (a rebound key, a script, an external `tmux next-window`). This is the
// only mechanism tmux offers: there is no per-window "skip/hidden" flag. Mirrors
// sidebarLimboSession. Parked windows are tagged @tabby_min_origin (which user
// session to restore into) and @tabby_min_dir (working dir, for the sidebar label
// — other @tabby_* display options travel with move-window automatically).
const minimizedHoldingSession = "_tabby_minimized"

// ensureMinimizedSession creates the detached holding session on demand, pinning
// destroy-unattached off so tmux never reaps it while it holds parked windows.
func ensureMinimizedSession() {
	if err := exec.Command("tmux", "has-session", "-t", minimizedHoldingSession).Run(); err == nil {
		return
	}
	if err := exec.Command("tmux", "new-session", "-d", "-s", minimizedHoldingSession, "-x", "80", "-y", "24").Run(); err != nil {
		coordinatorDebugLog.Printf("ensureMinimizedSession: new-session failed: %v", err)
		return
	}
	exec.Command("tmux", "set-option", "-t", minimizedHoldingSession, "destroy-unattached", "off").Run()
	coordinatorDebugLog.Printf("ensureMinimizedSession: created %s", minimizedHoldingSession)
}

// minimizedSessionHasParkedWindows reports whether any parked window (one tagged
// @tabby_min_origin — i.e. not the new-session placeholder) remains in the
// holding session, across all origin sessions.
func minimizedSessionHasParkedWindows() bool {
	out, err := exec.Command("tmux", "list-windows", "-t", minimizedHoldingSession, "-F",
		"#{@tabby_min_origin}").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// cleanupMinimizedSessionIfEmpty kills the holding session once no parked windows
// remain anywhere (also removing the new-session placeholder window).
func cleanupMinimizedSessionIfEmpty() {
	if minimizedSessionHasParkedWindows() {
		return
	}
	if err := exec.Command("tmux", "has-session", "-t", minimizedHoldingSession).Run(); err == nil {
		exec.Command("tmux", "kill-session", "-t", minimizedHoldingSession).Run()
		coordinatorDebugLog.Printf("cleanupMinimizedSessionIfEmpty: killed %s", minimizedHoldingSession)
	}
}

// isParkedWindow reports whether the window currently lives in the holding
// session (i.e. it is minimized-and-parked, not surfaced for peeking).
func isParkedWindow(windowID string) bool {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return false
	}
	return tmuxOutputTrimmed("display-message", "-p", "-t", windowID, "#{session_name}") == minimizedHoldingSession
}

// parkWindow moves windowID OUT of the user session into the holding session and
// tags it, so native navigation can no longer reach it. Idempotent-ish: a window
// already parked is left alone. captureFlags controls whether to (re)assert the
// @tabby_minimized flag + origin (true on minimize, false on a plain re-park where
// the markers are already present).
func (c *Coordinator) parkWindow(windowID string, setFlag bool) bool {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" || isParkedWindow(windowID) {
		return false
	}
	ensureMinimizedSession()
	// Capture the active pane's dir so the merged sidebar row can rebuild its
	// label (panes are detached in the holding session).
	if dir := tmuxOutputTrimmed("display-message", "-p", "-t", windowID, "#{pane_current_path}"); dir != "" {
		exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_min_dir", dir).Run()
	}
	// Capture the ssh/remote host too, so a parked ssh window keeps its ssh marker
	// in the sidebar (the detached holding pane can't be probed for ssh state).
	if win, ok := c.getWindowByID(windowID); ok && strings.TrimSpace(win.RemoteHost) != "" {
		exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_min_host", win.RemoteHost).Run()
	}
	if setFlag {
		exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_minimized", "1").Run()
		exec.Command("tmux", "set-window-option", "-t", windowID, "@tabby_min_origin", c.dashboardSession()).Run()
	}
	// Root prevention for #23: if a client is attached to this window, move-window
	// would drag it INTO the holding session (where it'd sit on only-minimized
	// windows). Switch any such client back to the origin session's current window
	// FIRST so no client follows the window into _tabby_minimized.
	origin := c.dashboardSession()
	if origin != "" {
		out, _ := exec.Command("tmux", "list-clients", "-F", "#{client_tty}|#{client_session}|#{session_id}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, "|")
			if len(parts) < 3 {
				continue
			}
			// A client whose current window IS the one we're about to park.
			if cw := tmuxOutputTrimmed("display-message", "-p", "-t", parts[0], "#{window_id}"); cw == windowID {
				exec.Command("tmux", "switch-client", "-c", parts[0], "-t", origin).Run()
			}
		}
	}
	if err := exec.Command("tmux", "move-window", "-d", "-s", windowID, "-t", minimizedHoldingSession+":").Run(); err != nil {
		coordinatorDebugLog.Printf("parkWindow: move-window failed for %s: %v", windowID, err)
		return false
	}
	coordinatorDebugLog.Printf("parkWindow: parked %s", windowID)
	return true
}

// surfaceWindow moves a parked window back into its origin session WITHOUT
// clearing @tabby_minimized — it stays flagged so it still shows in the Minimized
// section and re-parks on blur (peek). No-op if not parked.
func (c *Coordinator) surfaceWindow(windowID string) bool {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" || !isParkedWindow(windowID) {
		return false
	}
	origin := tmuxOutputTrimmed("display-message", "-p", "-t", windowID, "#{@tabby_min_origin}")
	if origin == "" {
		origin = c.dashboardSession()
	}
	if err := exec.Command("tmux", "move-window", "-d", "-s", windowID, "-t", origin+":").Run(); err != nil {
		coordinatorDebugLog.Printf("surfaceWindow: move-window failed for %s: %v", windowID, err)
		return false
	}
	coordinatorDebugLog.Printf("surfaceWindow: surfaced %s -> %s", windowID, origin)
	return true
}

// unparkWindow fully un-minimizes: moves the window back to its origin session
// (if parked) and clears every parked marker. Safe to call whether parked or
// surfaced-but-flagged.
func (c *Coordinator) unparkWindow(windowID string) {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return
	}
	if isParkedWindow(windowID) {
		origin := tmuxOutputTrimmed("display-message", "-p", "-t", windowID, "#{@tabby_min_origin}")
		if origin == "" {
			origin = c.dashboardSession()
		}
		if err := exec.Command("tmux", "move-window", "-d", "-s", windowID, "-t", origin+":").Run(); err != nil {
			coordinatorDebugLog.Printf("unparkWindow: move-window failed for %s: %v", windowID, err)
		}
	}
	exec.Command("tmux", "set-window-option", "-t", windowID, "-u", "@tabby_minimized").Run()
	exec.Command("tmux", "set-window-option", "-t", windowID, "-u", "@tabby_min_origin").Run()
	exec.Command("tmux", "set-window-option", "-t", windowID, "-u", "@tabby_min_dir").Run()
	cleanupMinimizedSessionIfEmpty()
	coordinatorDebugLog.Printf("unparkWindow: unminimized %s", windowID)
}

// clearPeekIf drops the peek tracking when windowID is the currently-peeked
// window — called when it is explicitly unminimized, so the blur handler won't
// try to re-park a window that is no longer minimized.
func (c *Coordinator) clearPeekIf(windowID string) {
	windowID = strings.TrimSpace(windowID)
	c.peekMu.Lock()
	if c.peekedWindowID == windowID {
		c.peekedWindowID = ""
	}
	c.peekMu.Unlock()
}

// surfaceForActivate runs JUST BEFORE a window is made active: if the target is a
// parked minimized window, move it back into the session so select-window can
// reach it. No-op for normal (already in-session) windows.
func (c *Coordinator) surfaceForActivate(targetID string) {
	targetID = strings.TrimSpace(targetID)
	if targetID != "" && isParkedWindow(targetID) {
		c.surfaceWindow(targetID)
	}
}

// settlePeek runs AFTER the target window is active (client already moved). It
// re-parks a previously-peeked window that just lost focus, and records the new
// peek when the now-active target is itself a still-minimized (surfaced) window.
// Doing the re-park after the switch avoids dragging a client along with the
// window as it moves to the holding session.
func (c *Coordinator) settlePeek(targetID string) {
	targetID = strings.TrimSpace(targetID)
	newPeek := ""
	if targetID != "" && tmuxOutputTrimmed("show-window-option", "-v", "-t", targetID, "@tabby_minimized") == "1" {
		newPeek = targetID
	}
	c.peekMu.Lock()
	prev := c.peekedWindowID
	c.peekMu.Unlock()

	if prev != "" && prev != targetID {
		// Re-hide the window we just left — but only if it is still flagged
		// minimized (an explicit unminimize clears the flag).
		if tmuxOutputTrimmed("show-window-option", "-v", "-t", prev, "@tabby_minimized") == "1" {
			c.parkWindow(prev, false)
		}
	}
	c.peekMu.Lock()
	c.peekedWindowID = newPeek
	c.peekMu.Unlock()
}

// maybeReparkPeeked is the polling catch-all: if the peeked window is no longer
// the active window (focus changed by a path that bypassed beforeActivate — a
// direct pane click, an external select-window), re-park it. Called from the
// window-refresh tick.
func (c *Coordinator) maybeReparkPeeked() {
	c.peekMu.Lock()
	prev := c.peekedWindowID
	c.peekMu.Unlock()
	if prev == "" {
		return
	}
	active := tmuxOutputTrimmed("display-message", "-p", "-t", c.dashboardSession(), "#{window_id}")
	if active == prev {
		return
	}
	if tmuxOutputTrimmed("show-window-option", "-v", "-t", prev, "@tabby_minimized") == "1" {
		c.parkWindow(prev, false)
	}
	c.peekMu.Lock()
	if c.peekedWindowID == prev {
		c.peekedWindowID = ""
	}
	c.peekMu.Unlock()
}

// listParkedMinimizedWindows returns synthetic Window entries for THIS session's
// windows currently parked in the holding session, so the sidebar's "Minimized"
// section still shows them even though they've left the user session. Display
// fields come from the @tabby_* options that travel with move-window; the working
// dir (captured as @tabby_min_dir at park time) is attached via a synthetic
// content pane so windowDirCode/firstPaneCWD can rebuild the tab label.
func (c *Coordinator) listParkedMinimizedWindows() []tmux.Window {
	if err := exec.Command("tmux", "has-session", "-t", minimizedHoldingSession).Run(); err != nil {
		return nil
	}
	origin := c.dashboardSession()
	out, err := exec.Command("tmux", "list-windows", "-t", minimizedHoldingSession, "-F",
		strings.Join([]string{
			"#{window_id}", "#{window_index}", "#{window_name}", "#{@tabby_min_origin}",
			"#{@tabby_color}", "#{@tabby_group}", "#{@tabby_icon}", "#{@tabby_ai_title}", "#{@tabby_min_dir}", "#{@tabby_min_host}",
		}, "\t")).Output()
	if err != nil {
		return nil
	}
	var parked []tmux.Window
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 9 {
			continue
		}
		if strings.TrimSpace(f[3]) != origin {
			continue // parked by a different user session — not ours to show
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(f[1]))
		w := tmux.Window{
			ID:          strings.TrimSpace(f[0]),
			Index:       idx,
			Name:        f[2],
			CustomColor: strings.TrimSpace(f[4]),
			Group:       strings.TrimSpace(f[5]),
			Icon:        strings.TrimSpace(f[6]),
			AITitle:     strings.TrimSpace(f[7]),
			Minimized:   true,
		}
		// Restore the captured ssh host so a parked ssh window keeps its ssh marker.
		if len(f) >= 10 {
			w.RemoteHost = strings.TrimSpace(f[9])
		}
		cmd := "bash"
		if w.RemoteHost != "" {
			cmd = "ssh"
		}
		if dir := strings.TrimSpace(f[8]); dir != "" {
			w.Panes = []tmux.Pane{{ID: "%parked", Command: cmd, CurrentPath: dir, Remote: w.RemoteHost != ""}}
		}
		parked = append(parked, w)
	}
	return parked
}

func stashNameForWindow(windowID string) string {
	return sidebarStashWindowPrefix + strings.TrimPrefix(windowID, "@")
}

func originalWindowIDFromStash(stashName string) string {
	suffix := strings.TrimPrefix(stashName, sidebarStashWindowPrefix)
	if suffix == "" || suffix == stashName {
		return ""
	}
	return "@" + suffix
}

// hideSidebarPanes moves every live sidebar pane to a dedicated stash window
// (one per original window). The sidebar-renderer process continues running;
// restoreSidebarPanes reattaches it later via join-pane. The sidebar's current
// width is saved on the stash window via @tabby_stashed_width so restore can
// bring it back at the same size the user had before hiding.
func (c *Coordinator) hideSidebarPanes() {
	// Pane count in every window is about to change (sidebar pane is
	// break-pane'd out). Any cached layouts for these windows are now for a
	// stale topology and would snap geometry back to a 2-pane layout when
	// replayed by Loop.Reconcile's OpSelectLayout.
	c.ForgetAllWindowLayouts()
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
		"#{pane_id}|#{window_id}|#{pane_current_command}|#{pane_start_command}|#{pane_width}").Output()
	if err != nil {
		return
	}
	// Make sure the detached holding session exists before we start moving
	// stash windows into it.
	ensureLimboSession()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		paneID := parts[0]
		winID := parts[1]
		cur := parts[2]
		start := parts[3]
		paneW := parts[4]
		if !isSidebarPaneCommand(cur, start) {
			continue
		}
		stashName := stashNameForWindow(winID)
		// break-pane -d: don't switch the current client to the new window.
		// -P -F prints the new stash window's window_id so we can park it
		// at a high tmux index immediately afterwards (see below).
		out, err := exec.Command("tmux", "break-pane", "-d", "-P", "-F", "#{window_id}", "-s", paneID, "-n", stashName).Output()
		if err != nil {
			coordinatorDebugLog.Printf("hideSidebarPanes: break-pane failed for %s: %v", paneID, err)
			continue
		}
		stashWinID := strings.TrimSpace(string(out))
		// Move the stash window OUT of the user's session into the detached
		// limbo session, so it is unreachable from native window cycling
		// (next/prev, prefix-n/p, choose-tree, status clicks) — all of which
		// are session-scoped. `break-pane` created it in the user's session;
		// `move-window -a -t <limbo>:<base>` appends it at the first free index
		// >= base inside limbo, so each stash gets its own slot without us
		// tracking allocations.
		if stashWinID != "" {
			parkArgs := []string{"move-window", "-d", "-a", "-s", stashWinID, "-t", fmt.Sprintf("%s:%d", sidebarLimboSession, sidebarStashParkBase)}
			if err := exec.Command("tmux", parkArgs...).Run(); err != nil {
				coordinatorDebugLog.Printf("hideSidebarPanes: park-stash move-window failed for %s: %v", stashWinID, err)
			}
		}
		// Persist the pre-stash width on the new stash window so restoreSidebarPanes
		// can join the pane back at its original size. Use the stash window_id
		// (not its name) because move-window may have changed the index.
		target := stashWinID
		if target == "" {
			target = stashName
		}
		if paneW != "" {
			if w, convErr := strconv.Atoi(strings.TrimSpace(paneW)); convErr == nil && w > 0 {
				exec.Command("tmux", "set-option", "-w", "-t", target,
					"@tabby_stashed_width", strconv.Itoa(w)).Run()
			}
		}
		coordinatorDebugLog.Printf("hideSidebarPanes: %s (window %s) -> %s width=%s parked_in=%s", paneID, winID, stashName, paneW, sidebarLimboSession)
	}
}

// restoreSidebarPanes reverses hideSidebarPanes: for each stash window, join
// its lone pane back into the original window on the left edge.
func (c *Coordinator) restoreSidebarPanes() {
	// Pane count in every restored window is about to change (sidebar pane is
	// join-pane'd back in). Drop cached layouts — same reasoning as
	// hideSidebarPanes; otherwise the next Reconcile's OpSelectLayout snaps
	// the user back to a 1-pane layout and the sidebar appears to flap open
	// then closed before the saved-layout cycle catches up to reality.
	c.ForgetAllWindowLayouts()
	out, err := exec.Command("tmux", "list-windows", "-a", "-F",
		"#{window_id}|#{window_name}").Output()
	if err != nil {
		return
	}
	defaultWidth := c.globalWidth
	if defaultWidth < 15 {
		defaultWidth = 25
	}
	var restoredWindows []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) < 2 {
			continue
		}
		stashWinID := parts[0]
		stashName := parts[1]
		if !strings.HasPrefix(stashName, sidebarStashWindowPrefix) {
			continue
		}
		origWinID := originalWindowIDFromStash(stashName)
		if origWinID == "" {
			continue
		}
		// Prefer the width the sidebar had right before it was stashed so the
		// user gets their exact layout back. Falls back to globalWidth if the
		// option is missing (e.g. legacy stash created before this code landed).
		width := defaultWidth
		if widthOut, wErr := exec.Command("tmux", "show-options", "-w", "-qv", "-t", stashWinID,
			"@tabby_stashed_width").Output(); wErr == nil {
			if w, convErr := strconv.Atoi(strings.TrimSpace(string(widthOut))); convErr == nil && w > 0 {
				width = w
			}
		}
		// Clamp to the destination window's profile-appropriate max. The stashed
		// width reflects whatever profile was active at hide time; restoring on
		// a smaller profile (e.g. stashed on desktop at 35 cols, restored on
		// phone) would otherwise produce a sidebar that swallows the screen.
		if bounded := c.boundedSidebarWidthForWindow(origWinID, width, 0); bounded > 0 && bounded != width {
			coordinatorDebugLog.Printf("restoreSidebarPanes: clamped width for %s: %d -> %d", origWinID, width, bounded)
			width = bounded
		}
		// Get the sidebar pane inside the stash window.
		pout, err := exec.Command("tmux", "list-panes", "-t", stashWinID, "-F", "#{pane_id}").Output()
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(pout)), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		stashPane := lines[0]
		// Find a content pane in the original window to use as the join target.
		tout, err := exec.Command("tmux", "list-panes", "-t", origWinID, "-F",
			"#{pane_id}|#{pane_current_command}|#{pane_start_command}").Output()
		if err != nil {
			// Original window is gone — drop the stash.
			exec.Command("tmux", "kill-window", "-t", stashWinID).Run()
			continue
		}
		targetPane := ""
		for _, pline := range strings.Split(strings.TrimSpace(string(tout)), "\n") {
			pparts := strings.SplitN(pline, "|", 3)
			if len(pparts) < 3 {
				continue
			}
			if isAuxiliaryPaneCommand(pparts[1]) || isAuxiliaryPaneCommand(pparts[2]) {
				continue
			}
			targetPane = pparts[0]
			break
		}
		if targetPane == "" {
			exec.Command("tmux", "kill-window", "-t", stashWinID).Run()
			continue
		}
		// -d: don't activate the joined pane in its destination window.
		// Without -d, tmux makes the sidebar pane active in the destination
		// window AND follows that focus to switch the session's active
		// window — which means the LAST join in the loop wins, dragging
		// the user from whatever window they were on to whichever window's
		// stash got restored last (alphabetic-by-stash-name order, not
		// any user-meaningful order). The user reported this as "cycle
		// all the way to the topmost window and stop" on hamburger-open.
		// -h -b -l <width>: horizontal split, before target, sized in cols.
		if err := exec.Command("tmux", "join-pane", "-d", "-h", "-b", "-l", fmt.Sprintf("%d", width),
			"-s", stashPane, "-t", targetPane).Run(); err != nil {
			coordinatorDebugLog.Printf("restoreSidebarPanes: join-pane failed for %s: %v", stashPane, err)
			continue
		}
		coordinatorDebugLog.Printf("restoreSidebarPanes: %s -> %s (width=%d)", stashPane, targetPane, width)

		// After sidebar is restored, kill any pane-headers that span the full window width.
		// During phone mode the pane-header expands to full width; after join-pane the sidebar
		// is beside the terminal but the header still spans the whole row. Kill and let respawn.
		restoredWindows = append(restoredWindows, origWinID)
	}

	// Kill full-width pane-headers in restored windows so they can be respawned correctly.
	for _, winID := range restoredWindows {
		// Get window width first.
		winWidthOut, err := exec.Command("tmux", "display-message", "-t", winID, "-p", "#{window_width}").Output()
		if err != nil {
			continue
		}
		winWidth, _ := strconv.Atoi(strings.TrimSpace(string(winWidthOut)))
		if winWidth <= 0 {
			continue
		}

		pout, err := exec.Command("tmux", "list-panes", "-t", winID, "-F",
			"#{pane_id}|#{pane_width}|#{pane_left}|#{pane_current_command}|#{pane_start_command}").Output()
		if err != nil {
			continue
		}
		for _, pl := range strings.Split(strings.TrimSpace(string(pout)), "\n") {
			parts := strings.SplitN(pl, "|", 5)
			if len(parts) < 5 {
				continue
			}
			pID := parts[0]
			pWidth, _ := strconv.Atoi(parts[1])
			pLeft, _ := strconv.Atoi(parts[2])
			curCmd := parts[3]
			startCmd := parts[4]
			isHeader := strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header")
			if !isHeader {
				continue
			}
			// Full-width means header starts at col 0 AND spans the full window width.
			if pLeft == 0 && pWidth >= winWidth-1 {
				logEvent("RESTORE_KILL_FULLWIDTH_HEADER pane=%s window=%s width=%d winWidth=%d", pID, winID, pWidth, winWidth)
				markSkipPreserveForWindow(pID)
				exec.Command("tmux", "kill-pane", "-t", pID).Run()
			}
		}
	}

	// Once every stash has been rejoined, tear down the now-empty limbo holder
	// so it stops lingering in the native session chooser.
	cleanupLimboSessionIfEmpty()
}

// fullscreenSidebarActiveWindowID returns the window currently in full-width
// sidebar mode (tagged @tabby_fullscreen_sidebar=1) for the session, or "".
// Reads from tmux so it is correct across a daemon restart. Mirrors
// dashboardActiveWindowID.
func fullscreenSidebarActiveWindowID(sess string) string {
	if sess == "" {
		return ""
	}
	out := tmuxOutputTrimmed("list-windows", "-t", sess, "-F", "#{window_id}\t#{@tabby_fullscreen_sidebar}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) == "1" {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

// fullscreenSidebarPanes classifies a window's panes into its content panes and
// its window-header footer pane (+ that footer's height). Sidebar/other aux panes
// are ignored.
func fullscreenSidebarPanes(winID string) (content []string, footerPane string, footerHeight int) {
	out := tmuxOutputTrimmed("list-panes", "-t", winID, "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_start_command}\t#{pane_height}")
	for _, line := range dashLines(out) {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		pid, cur, start := parts[0], parts[1], parts[2]
		combined := cur + " " + start
		if strings.Contains(combined, "window-header") {
			footerPane = pid
			footerHeight, _ = strconv.Atoi(strings.TrimSpace(parts[3]))
			continue
		}
		if isSidebarPaneCommand(cur, start) || isAuxiliaryPaneCommand(cur) {
			continue
		}
		content = append(content, pid)
	}
	return content, footerPane, footerHeight
}

// openFullscreenSidebar (phone) hides the window's CONTENT and fills the content
// area with a full-width sidebar renderer, keeping the window-header carousel as a
// bottom footer. The content pane(s) are break-pane'd into the detached _tabby_limbo
// session (tagged @tabby_fs_origin) and restored on close. V1 supports single-pane
// content windows; multi-pane windows are left unchanged (deferred).
func (c *Coordinator) openFullscreenSidebar(winID string) {
	winID = strings.TrimSpace(winID)
	if winID == "" || c.fullscreenSidebarWinID != "" {
		return
	}
	content, footerPane, footerH := fullscreenSidebarPanes(winID)
	if footerPane == "" || len(content) == 0 {
		return // need a footer to sit below and content to hide
	}
	if len(content) > 1 {
		// V1: single content pane only. Leave multi-pane windows on today's
		// behavior; full window_layout round-trip is a follow-up.
		coordinatorDebugLog.Printf("openFullscreenSidebar: %s has %d content panes; V1 single-pane only", winID, len(content))
		return
	}
	c.ForgetAllWindowLayouts()
	// Bracket the whole swap with @tabby_spawning so the daemon's spawn/layout
	// passes don't race us (respawn a narrow sidebar, snap a layout, etc.) while
	// panes are mid-move. Mirrors enterDashboard.
	_ = tmuxRun("set-option", "-g", "@tabby_spawning", "1")
	defer tmuxRun("set-option", "-gu", "@tabby_spawning")
	ensureLimboSession()

	// Remember the footer height so close can pin it back exactly.
	exec.Command("tmux", "set-window-option", "-t", winID, "@tabby_fs_footer_height", strconv.Itoa(footerH)).Run()

	// Stash the content pane into the limbo session, tagged with its origin window.
	stashName := contentStashNameForWindow(winID)
	sw, err := exec.Command("tmux", "break-pane", "-d", "-P", "-F", "#{window_id}", "-s", content[0], "-n", stashName).Output()
	if err != nil {
		coordinatorDebugLog.Printf("openFullscreenSidebar: break-pane failed for %s: %v", content[0], err)
		return
	}
	stashWin := strings.TrimSpace(string(sw))
	if stashWin != "" {
		exec.Command("tmux", "set-window-option", "-t", stashWin, "@tabby_fs_origin", winID).Run()
		exec.Command("tmux", "move-window", "-d", "-a", "-s", stashWin,
			"-t", fmt.Sprintf("%s:%d", sidebarLimboSession, sidebarStashParkBase)).Run()
	}

	// Kill any EXISTING sidebar pane(s) in the window (on a multi-client session the
	// sidebar may be shown, not stashed) so we don't end up with two sidebars
	// squeezing the footer. A fresh full-width one is spawned next.
	killOut := tmuxOutputTrimmed("list-panes", "-t", winID, "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(killOut) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		if isSidebarPaneCommand(parts[1], parts[2]) {
			exec.Command("tmux", "kill-pane", "-t", parts[0]).Run()
		}
	}

	// Spawn a full-width sidebar renderer ABOVE the footer (-v -b -f), then pin the
	// footer back to its height so the sidebar fills the rest. -f = full window
	// width, so the sidebar is full-width with NO width clamp.
	if rendererBin := getRendererBin(); rendererBin != "" {
		cmdStr := fmt.Sprintf("printf '\\033[?25l\\033[2J\\033[H' && %s -session '%s' -window '%s'",
			rendererBin, c.dashboardSession(), winID)
		if err := exec.Command("tmux", "split-window", "-d", "-v", "-b", "-f", "-t", footerPane, cmdStr).Run(); err != nil {
			coordinatorDebugLog.Printf("openFullscreenSidebar: sidebar spawn failed: %v", err)
		}
	}
	if footerH > 0 {
		exec.Command("tmux", "resize-pane", "-t", footerPane, "-y", strconv.Itoa(footerH)).Run()
	}

	exec.Command("tmux", "set-window-option", "-t", winID, "@tabby_fullscreen_sidebar", "1").Run()
	c.fullscreenSidebarWinID = winID
	logEvent("FULLSCREEN_OPEN window=%s", winID)
}

// closeFullscreenSidebar reverses openFullscreenSidebar: kill the full-width
// sidebar pane, rejoin the stashed content above the footer, and clear the flag.
func (c *Coordinator) closeFullscreenSidebar(winID string, focusContent bool) {
	winID = strings.TrimSpace(winID)
	if winID == "" {
		return
	}
	c.ForgetAllWindowLayouts()
	_ = tmuxRun("set-option", "-g", "@tabby_spawning", "1")
	defer tmuxRun("set-option", "-gu", "@tabby_spawning")

	// Kill the full-width sidebar pane(s) in the window (leave the footer).
	content, footerPane, _ := fullscreenSidebarPanes(winID)
	footerH := 0
	if v := tmuxOutputTrimmed("show-window-option", "-v", "-t", winID, "@tabby_fs_footer_height"); v != "" {
		footerH, _ = strconv.Atoi(v)
	}
	_ = content // content should be empty while open; kept for symmetry
	sbOut := tmuxOutputTrimmed("list-panes", "-t", winID, "-F",
		"#{pane_id}\t#{pane_current_command}\t#{pane_start_command}")
	for _, line := range dashLines(sbOut) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		if isSidebarPaneCommand(parts[1], parts[2]) {
			exec.Command("tmux", "kill-pane", "-t", parts[0]).Run()
		}
	}

	// Rejoin the stashed content ABOVE the footer, then pin the footer height.
	if footerPane != "" {
		out := tmuxOutputTrimmed("list-windows", "-t", sidebarLimboSession, "-F",
			"#{window_id}\t#{@tabby_fs_origin}")
		for _, line := range dashLines(out) {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[1]) != winID {
				continue
			}
			stashWin := strings.TrimSpace(parts[0])
			cpane := firstToken(tmuxOutputTrimmed("list-panes", "-t", stashWin, "-F", "#{pane_id}"), "%")
			if cpane != "" {
				exec.Command("tmux", "join-pane", "-d", "-v", "-b", "-s", cpane, "-t", footerPane).Run()
			}
		}
		if footerH > 0 {
			exec.Command("tmux", "resize-pane", "-t", footerPane, "-y", strconv.Itoa(footerH)).Run()
		}
	}

	exec.Command("tmux", "set-window-option", "-t", winID, "-u", "@tabby_fullscreen_sidebar").Run()
	exec.Command("tmux", "set-window-option", "-t", winID, "-u", "@tabby_fs_footer_height").Run()
	if c.fullscreenSidebarWinID == winID {
		c.fullscreenSidebarWinID = ""
	}
	cleanupLimboSessionIfEmpty()
	if focusContent {
		focusContentPaneInActiveWindow()
	}
	logEvent("FULLSCREEN_CLOSE window=%s", winID)
}

// selectNeighborWindow cycles to the prev/next non-stash window relative to
// the currently active window. delta=-1 selects previous, delta=+1 selects
// next. Uses the coordinator's filtered window list (RefreshWindows already
// drops _tabby_stash_ windows) so we never land on a sidebar holding window.
// Wraps around at the ends.
func (c *Coordinator) selectNeighborWindow(delta int) {
	c.selectNeighborWindowFrom("", delta, "internal")
}

func (c *Coordinator) selectNeighborWindowFrom(currentWindowID string, delta int, trigger string) {
	c.stateMu.RLock()
	wins := make([]string, 0, len(c.windows))
	unfiltered := make([]string, 0, len(c.windows))
	active := ""
	srcID := strings.TrimSpace(currentWindowID)
	for _, w := range c.windows {
		if strings.HasPrefix(w.Name, sidebarStashWindowPrefix) {
			continue
		}
		unfiltered = append(unfiltered, w.ID)
		// Skip minimized windows unless it's the one we're cycling from —
		// keeping the source in the list lets modulo arithmetic land on a
		// sensible neighbor when the user is on a minimized window itself.
		if w.Minimized && w.ID != srcID && !w.Active {
			continue
		}
		wins = append(wins, w.ID)
		if w.Active {
			active = w.ID
		}
	}
	// If every window is minimized, fall back to the unfiltered list so the
	// user can still cycle.
	if len(wins) == 0 {
		wins = unfiltered
	}
	c.stateMu.RUnlock()

	if len(wins) == 0 {
		logEvent("WINDOW_NAV_SKIP reason=no_windows delta=%d", delta)
		return
	}
	if len(wins) == 1 {
		// Nothing to cycle to.
		logEvent("WINDOW_NAV_SKIP reason=single_window delta=%d only=%s", delta, wins[0])
		return
	}

	activeTTY := strings.TrimSpace(clientTTYForWindow(currentWindowID))
	if activeTTY == "" {
		if _, _, tty, _, ok := activeClientGeometry(); ok {
			activeTTY = strings.TrimSpace(tty)
		}
	}

	if strings.TrimSpace(currentWindowID) != "" {
		active = strings.TrimSpace(currentWindowID)
	}

	if active == "" {
		args := []string{"display-message"}
		if activeTTY != "" {
			args = append(args, "-c", activeTTY)
		}
		args = append(args, "-p", "#{window_id}")
		activeOut, _ := exec.Command("tmux", args...).Output()
		active = strings.TrimSpace(string(activeOut))
	}

	idx := -1
	for i, id := range wins {
		if id == active {
			idx = i
			break
		}
	}
	if idx == -1 {
		// Active window not in filtered list (e.g. tmux placed us on a stash
		// window). Jump to the first real window instead.
		logEvent("WINDOW_NAV_FALLBACK reason=active_not_in_filtered active=%s delta=%d candidates=%v", active, delta, wins)
		idx = 0
	} else {
		idx = (idx + delta + len(wins)) % len(wins)
	}
	target := wins[idx]
	logEvent("WINDOW_NAV_SELECT trigger=%s delta=%d active=%s source=%s target=%s candidates=%v", trigger, delta, active, strings.TrimSpace(currentWindowID), target, wins)
	before := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
	if err := c.SelectWindow(target, "window_neighbor_nav", trigger); err != nil {
		logEvent("WINDOW_NAV_SELECT_ERR target=%s err=%v", target, err)
		return
	}
	after := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
	logEvent("WINDOW_NAV_RESULT source=%s target=%s before=%s after=%s clients=%s", strings.TrimSpace(currentWindowID), target, before, after, tmuxClientWindowSnapshot())
	if after != target {
		logEvent("WINDOW_NAV_NOOP source=%s target=%s before=%s after=%s", strings.TrimSpace(currentWindowID), target, before, after)
	}
}

// selectNeighborWindowPerClient cycles by `delta` from `sourceWindowID`,
// switching ONLY the tmux clients currently viewing that source window
// via `tmux switch-client -c <tty>` rather than the global `select-window`.
//
// Motivation: `tmux select-window -t @target` (used by selectNeighborWindowFrom
// → SelectWindow) is global — it pulls every attached client to @target. On
// multi-client sessions (e.g. desktop + phone both attached), one client's
// M-}/M-{ keystroke dragged ALL clients along. Per-client switching means
// only the clients on the originating window move; clients viewing a
// different window stay put.
//
// Returns true if at least one client was switched (caller skips the global
// fallback). Returns false if no clients are on `sourceWindowID` or if the
// neighbor calculation yields no target — caller falls back to the global
// path so single-client setups behave identically to before.
func (c *Coordinator) selectNeighborWindowPerClient(sourceWindowID string, delta int, trigger string) bool {
	src := strings.TrimSpace(sourceWindowID)
	if src == "" {
		return false
	}

	c.stateMu.RLock()
	wins := make([]string, 0, len(c.windows))
	unfiltered := make([]string, 0, len(c.windows))
	for _, w := range c.windows {
		if strings.HasPrefix(w.Name, sidebarStashWindowPrefix) {
			continue
		}
		unfiltered = append(unfiltered, w.ID)
		if w.Minimized && w.ID != src && !w.Active {
			continue
		}
		wins = append(wins, w.ID)
	}
	if len(wins) == 0 {
		wins = unfiltered
	}
	c.stateMu.RUnlock()

	if len(wins) < 2 {
		logEvent("WINDOW_NAV_PERCLIENT_SKIP reason=insufficient_windows source=%s count=%d", src, len(wins))
		return false
	}

	idx := -1
	for i, id := range wins {
		if id == src {
			idx = i
			break
		}
	}
	if idx < 0 {
		logEvent("WINDOW_NAV_PERCLIENT_SKIP reason=source_not_in_filtered source=%s candidates=%v", src, wins)
		return false
	}
	target := wins[(idx+delta+len(wins))%len(wins)]

	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}|#{client_session}|#{client_window}").Output()
	if err != nil {
		logEvent("WINDOW_NAV_PERCLIENT_LIST_ERR err=%v", err)
		return false
	}

	// Build an index→@id map from cached coordinator state for the
	// daemon's session. Per-tty `tmux display-message` was the previous
	// resolution path (~10-15ms each) — eliminating it removes the bulk
	// of remaining nav-path fork/exec.
	c.stateMu.RLock()
	idxToID := make(map[string]string, len(c.windows))
	for i := range c.windows {
		w := &c.windows[i]
		idxToID[strconv.Itoa(w.Index)] = w.ID
	}
	c.stateMu.RUnlock()
	daemonSession := strings.TrimSpace(c.sessionID)

	type ttyResolution struct {
		tty    string
		window string
	}
	var fallbackTTYs []string
	resolutions := []ttyResolution{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		tty := strings.TrimSpace(parts[0])
		sess := strings.TrimSpace(parts[1])
		idx := strings.TrimSpace(parts[2])
		if tty == "" {
			continue
		}
		// Cache hit path: client is on our session, index resolves.
		if sess == daemonSession || daemonSession == "" {
			if winID, ok := idxToID[idx]; ok {
				resolutions = append(resolutions, ttyResolution{tty: tty, window: winID})
				continue
			}
		}
		// Fallback: client on a different session (or our cache stale).
		// Resolve via display-message in parallel with any other fallbacks.
		fallbackTTYs = append(fallbackTTYs, tty)
	}

	if len(fallbackTTYs) > 0 {
		fallbackResults := make([]ttyResolution, len(fallbackTTYs))
		var wg sync.WaitGroup
		wg.Add(len(fallbackTTYs))
		for i, tty := range fallbackTTYs {
			i, tty := i, tty
			go func() {
				defer wg.Done()
				curOut, _ := exec.Command("tmux", "display-message", "-p", "-c", tty, "#{window_id}").Output()
				fallbackResults[i] = ttyResolution{tty: tty, window: strings.TrimSpace(string(curOut))}
			}()
		}
		wg.Wait()
		resolutions = append(resolutions, fallbackResults...)
	}

	ttys := []string{}
	for _, r := range resolutions {
		if r.window == src {
			ttys = append(ttys, r.tty)
		}
	}
	if len(ttys) == 0 {
		logEvent("WINDOW_NAV_PERCLIENT_SKIP reason=no_clients_on_source source=%s target=%s", src, target)
		return false
	}

	// Switch matched clients in parallel — each switch-client is
	// independent and the fork/exec cost dominates.
	switched := make([]string, len(ttys))
	switchErrs := make([]error, len(ttys))
	var swg sync.WaitGroup
	swg.Add(len(ttys))
	for i, tty := range ttys {
		i, tty := i, tty
		go func() {
			defer swg.Done()
			if err := exec.Command("tmux", "switch-client", "-c", tty, "-t", target).Run(); err != nil {
				switchErrs[i] = err
				return
			}
			switched[i] = tty
		}()
	}
	swg.Wait()
	successes := switched[:0]
	for i, tty := range switched {
		if switchErrs[i] != nil {
			logEvent("WINDOW_NAV_PERCLIENT_SWITCH_ERR tty=%s target=%s err=%v", ttys[i], target, switchErrs[i])
			continue
		}
		if tty != "" {
			successes = append(successes, tty)
		}
	}
	switched = successes
	logEvent("WINDOW_NAV_PERCLIENT trigger=%s delta=%d source=%s target=%s candidates=%v switched=%v",
		trigger, delta, src, target, wins, switched)
	if len(switched) > 0 {
		// Flip the in-memory active flag NOW so the immediate broadcast (fired
		// by handleRendererInput right after this function returns) renders
		// every sidebar with the new active highlight on the same frame. Without
		// this, BroadcastRender runs while c.windows still has the old active
		// pane flagged, and the corrected render has to wait for the
		// signal_refresh → updateActiveWindow round trip (~300ms) before the
		// sidebar highlights the new tab.
		c.SetActiveWindowOptimistic(target)
		// Re-park the window we navigated away from if it was a peeked minimized
		// one (target is never a parked window here — wins excludes minimized).
		c.settlePeek(target)
	}
	return len(switched) > 0
}

// sidebarIsStashed reports whether any stash window exists.
func sidebarIsStashed() bool {
	out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_name}").Output()
	if err != nil {
		return false
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(name, sidebarStashWindowPrefix) {
			return true
		}
	}
	return false
}

// borderStylingBlocked is a legacy hook from the mobile border-hide experiment.
// That experiment was reverted (color-matching couldn't reclaim the space tmux
// reserves for pane borders), so this always returns false and normal themed
// border styling runs as usual. Kept as a no-op so existing guards compile.
func (c *Coordinator) borderStylingBlocked() bool {
	return false
}

// windowHeaderPressEntry records the last tapped button on a window-header
// pane so renders can briefly highlight it as visual tap feedback.
type windowHeaderPressEntry struct {
	action string
	at     time.Time
}

// windowHeaderPressDuration controls how long a button stays visually
// "pressed" after a tap.
const windowHeaderPressDuration = 180 * time.Millisecond

// recordWindowHeaderPress stores which window-header button was just tapped
// so the next render can highlight it. Schedules a follow-up render so the
// pressed state clears automatically.
func (c *Coordinator) recordWindowHeaderPress(windowID, action string) {
	if windowID == "" || action == "" {
		return
	}
	logEvent("WINDOW_HEADER_PRESS window=%s action=%s", windowID, action)
	c.windowHeaderPressMu.Lock()
	if c.windowHeaderPress == nil {
		c.windowHeaderPress = make(map[string]windowHeaderPressEntry)
	}
	c.windowHeaderPress[windowID] = windowHeaderPressEntry{action: action, at: time.Now()}
	c.windowHeaderPressMu.Unlock()

	// Trigger an immediate re-render so the pressed state is drawn, and a
	// second one after the press window expires so it clears on its own.
	if c.OnRefreshClient != nil {
		clientID := "window-header:" + windowID
		c.OnRefreshClient(clientID)
		go func() {
			time.Sleep(windowHeaderPressDuration + 20*time.Millisecond)
			if c.OnRefreshClient != nil {
				c.OnRefreshClient(clientID)
			}
		}()
	}
}

// activeWindowHeaderPress returns the currently pressed action for a window
// if one was tapped within the press duration, else "".
func (c *Coordinator) activeWindowHeaderPress(windowID string) string {
	c.windowHeaderPressMu.Lock()
	defer c.windowHeaderPressMu.Unlock()
	if c.windowHeaderPress == nil {
		return ""
	}
	entry, ok := c.windowHeaderPress[windowID]
	if !ok {
		return ""
	}
	if time.Since(entry.at) > windowHeaderPressDuration {
		delete(c.windowHeaderPress, windowID)
		return ""
	}
	return entry.action
}

// syncMobileBorders toggles tmux pane border visibility based on the active
// client's width. On narrow clients (<100 cols) the pane-border color is set
// to match the terminal background so borders disappear visually; on desktop
// the override is removed. Tracks last-applied state to avoid spamming tmux.
func (c *Coordinator) syncMobileBorders() {
	if c.config.PaneHeader.Native != nil && *c.config.PaneHeader.Native {
		// Native border-status mode owns pane-border styling globally via
		// applyNativeBorders. Skip the mobile-hide override entirely —
		// otherwise it stomps the visible label colours every loop tick.
		return
	}
	acw := int(c.activeClientWidth.Load())

	wantHidden := acw > 0 && acw < 100

	if c.mobileBorderHidden == wantHidden && c.mobileBorderInit {
		return
	}
	c.mobileBorderHidden = wantHidden
	c.mobileBorderInit = true

	// The dashboard window draws NATIVE pane borders (with label text), so it is
	// exempt from this border-hiding/restoring in BOTH branches — otherwise its
	// labels render in fg=bg (invisible) or its visible style gets unset, which
	// also caused flicker. See applyDashboardBorders.
	dashWin := dashboardActiveWindowID(c.dashboardSession())
	if wantHidden {
		bg := "default"
		if c.theme != nil && c.theme.TerminalBg != "" {
			bg = c.theme.TerminalBg
		}
		style := fmt.Sprintf("fg=%s,bg=%s", bg, bg)
		exec.Command("tmux", "set-option", "-g", "pane-border-style", style).Run()
		exec.Command("tmux", "set-option", "-g", "pane-active-border-style", style).Run()
		exec.Command("tmux", "set-option", "-g", "pane-border-status", "off").Run()
		// Per-window options override globals. Force-apply the hidden style
		// to every window and every pane so buildPaneHeaderColorArgs leftovers
		// (e.g. fg=#98babe) can't keep drawing visible borders.
		if out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Output(); err == nil {
			for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if wid == "" || wid == dashWin {
					continue
				}
				exec.Command("tmux", "set-window-option", "-t", wid, "pane-border-style", style).Run()
				exec.Command("tmux", "set-window-option", "-t", wid, "pane-active-border-style", style).Run()
			}
		}
		if out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}|#{window_id}").Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(line, "|", 2)
				pid := parts[0]
				if pid == "" || (len(parts) == 2 && parts[1] == dashWin) {
					continue
				}
				exec.Command("tmux", "set-option", "-p", "-t", pid, "pane-border-style", style).Run()
			}
		}
		logEvent("MOBILE_BORDERS hidden acw=%d style=%s", acw, style)
	} else {
		// Restore branch UNSETS the hidden style — apply to all windows. The
		// dashboard owns its border styling via pane-local sets in
		// applyDashboardBorders (highest-precedence), so window-level unsets
		// here don't affect it visually.
		exec.Command("tmux", "set-option", "-gu", "pane-border-style").Run()
		exec.Command("tmux", "set-option", "-gu", "pane-active-border-style").Run()
		if out, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Output(); err == nil {
			for _, wid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if wid == "" {
					continue
				}
				exec.Command("tmux", "set-window-option", "-t", wid, "-u", "pane-border-style").Run()
				exec.Command("tmux", "set-window-option", "-t", wid, "-u", "pane-active-border-style").Run()
			}
		}
		if out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}").Output(); err == nil {
			for _, pid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if pid == "" {
					continue
				}
				exec.Command("tmux", "set-option", "-p", "-t", pid, "-u", "pane-border-style").Run()
			}
		}
		logEvent("MOBILE_BORDERS restored acw=%d", acw)
	}
}

// capTargetToActiveClient clamps a desired sidebar width so that the active
// physical tmux client still has at least minContentPaneCols cols left for the
// content pane. Returns target unchanged if no active client info is known.
func (c *Coordinator) capTargetToActiveClient(target int) int {
	const minContentPaneCols = 40
	acw := int(c.activeClientWidth.Load())
	if acw <= 0 {
		return target
	}
	maxSidebar := acw - minContentPaneCols
	if maxSidebar < 10 {
		maxSidebar = 10
	}
	if target > maxSidebar {
		return maxSidebar
	}
	return target
}

// PlanWidthSync computes the resize ops needed to reconcile every sidebar
// pane's width with the global target. Returns ops without executing — the
// caller chooses whether to flush directly (RunWidthSync wrapper) or
// concatenate with header / window-size ops for one batched tmux invocation
// (Loop.Reconcile).
//
// State side-effects: may adopt the active window's measured width as the
// new globalWidth (when the user drags the active sidebar) and persists it
// via persistSidebarWidthProfile. Updates lastWidthSync, keyboardHoldUntil.
//
// When force=true, skips the 500ms debounce (used for immediate restoration
// after layout changes).
func (c *Coordinator) PlanWidthSync(activeWindowID string, force bool) []ResizeOp {
	start := time.Now()
	if c.sidebarHidden {
		logEvent("WIDTH_SYNC_SKIP reason=sidebar_collapsed active=%s force=%v", activeWindowID, force)
		return nil
	}

	// Snapshot client widths/heights
	c.clientWidthsMu.RLock()
	clientSnapshot := make(map[string]int, len(c.clientWidths))
	clientHeightSnapshot := make(map[string]int, len(c.clientWidths))
	keyboardHoldSnapshot := make(map[string]time.Time, len(c.keyboardHoldUntil))
	for id, w := range c.clientWidths {
		clientSnapshot[id] = w
		if c.clientHeights != nil {
			clientHeightSnapshot[id] = c.clientHeights[id]
		}
	}
	if c.keyboardHoldUntil != nil {
		for id, expiry := range c.keyboardHoldUntil {
			keyboardHoldSnapshot[id] = expiry
		}
	}
	c.clientWidthsMu.RUnlock()

	if len(clientSnapshot) == 0 {
		logEvent("WIDTH_SYNC_SKIP reason=no_clients active=%s force=%v duration_ms=%d", activeWindowID, force, time.Since(start).Milliseconds())
		return nil
	}

	// Read per-window SyncWidth settings under stateMu BEFORE acquiring widthSyncMu
	// (lock ordering: stateMu -> widthSyncMu)
	syncSettings := make(map[string]bool)
	c.stateMu.RLock()
	for i := range c.windows {
		syncSettings[c.windows[i].ID] = c.windows[i].SyncWidth
	}
	c.stateMu.RUnlock()

	keyboardWidth, keyboardThreshold := c.getMobileKeyboardSettings()
	expiredHolds := make([]string, 0)
	extendHolds := make([]string, 0)

	c.widthSyncMu.Lock()

	// Detect active window change
	justBecameActive := activeWindowID != "" && c.lastActiveWindowID != activeWindowID
	if justBecameActive {
		coordinatorDebugLog.Printf("Width sync: active window changed to %s", activeWindowID)
		c.lastActiveWindowID = activeWindowID
	}

	// Debounce: ignore resize events within 500ms of our last sync (unless forced)
	var sinceLast time.Duration
	hasLast := !c.lastWidthSync.IsZero()
	if hasLast {
		sinceLast = time.Since(c.lastWidthSync)
	}
	if !force && hasLast && sinceLast < 500*time.Millisecond {
		logEvent("WIDTH_SYNC_SKIP reason=debounce active=%s force=%v since_last_ms=%d", activeWindowID, force, sinceLast.Milliseconds())
		c.widthSyncMu.Unlock()
		return nil
	}

	// Build list of panes to resize (compute under lock, execute after unlock)
	type resizeOp struct {
		clientID    string
		targetWidth int
		paneID      string // resolved upfront from list-panes; empty = skip
	}
	var ops []resizeOp

	// Read actual tmux pane widths AND sidebar pane IDs in one round-trip.
	// Capturing the paneID here means the execution phase doesn't need a
	// per-window list-panes call to resolve the sidebar — it can hand the
	// resolved IDs directly to flushOpsBatched as one chained tmux command.
	// Wrapped in a bounded context so a stalled tmux server cannot hold
	// widthSyncMu indefinitely.
	actualPaneWidths := make(map[string]int)
	sidebarPaneIDs := make(map[string]string)
	paneCtx, paneCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	if paneOut, err := exec.CommandContext(paneCtx, "tmux", "list-panes", "-s", "-F", "#{pane_id}|#{pane_current_command}|#{window_id}|#{pane_width}|#{pane_start_command}").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
			parts := strings.SplitN(line, "|", 5)
			if len(parts) == 5 && isSidebarPaneCommand(parts[1], parts[4]) {
				if w, err := strconv.Atoi(parts[3]); err == nil {
					actualPaneWidths[parts[2]] = w
				}
				if _, dup := sidebarPaneIDs[parts[2]]; !dup {
					sidebarPaneIDs[parts[2]] = parts[0]
				}
			}
		}
	} else if paneCtx.Err() == context.DeadlineExceeded {
		logEvent("WIDTH_SYNC_PANE_LIST_TIMEOUT ms=1500")
	}
	paneCancel()

	// If the active window's sidebar was resized by the user, adopt the new
	// width as global BEFORE computing per-client targets. Without this,
	// RunWidthSync computes targets against the stale global and reverts the
	// drag on the next pass instead of propagating it to other windows.
	// Prefer actual tmux pane width over client-reported width (the latter
	// can lag a drag by a render cycle).
	adoptedActiveWidth := 0
	if activeWindowID != "" {
		effectiveActive := clientSnapshot[activeWindowID]
		if w, ok := actualPaneWidths[activeWindowID]; ok {
			effectiveActive = w
		}
		if effectiveActive >= 10 && c.globalWidth != 0 && effectiveActive != c.globalWidth && syncSettings[activeWindowID] {
			// Don't adopt a measured width that's just the active-client cap.
			// When the active physical client is narrow (phone/touch),
			// capTargetToActiveClient clamps every sidebar's target to
			// (clientWidth - minContentPaneCols). Any window whose sidebar is
			// sitting AT that cap is not a user drag — it's the clamp doing
			// its job. Adopting here would flip globalWidth down to the cap
			// and propagate the clamp to every window (including wider ones
			// that don't need it). That presents as "sidebars look different
			// between windows" the moment a narrow client connects.
			capped := c.capTargetToActiveClient(c.globalWidth)
			atCap := effectiveActive == capped && capped < c.globalWidth
			if justBecameActive {
				// First sync tick after a window switch. The discrepancy is a
				// stale width about to be synced TO this window, not a drag
				// coming FROM it. Adopting here would flip globalWidth to the
				// new active's stored/cached size and ping-pong all sidebars
				// on every window switch.
				logEvent("WIDTH_SYNC_ADOPT_SKIP reason=just_became_active active=%s measured=%d global=%d", activeWindowID, effectiveActive, c.globalWidth)
			} else if atCap {
				logEvent("WIDTH_SYNC_ADOPT_SKIP reason=at_active_client_cap active=%s measured=%d global=%d cap=%d", activeWindowID, effectiveActive, c.globalWidth, capped)
			} else {
				coordinatorDebugLog.Printf("Width sync: user resized active sidebar %s from %d to %d, updating global",
					activeWindowID, c.globalWidth, effectiveActive)
				logEvent("WIDTH_SYNC_ADOPT active=%s from=%d to=%d", activeWindowID, c.globalWidth, effectiveActive)
				c.globalWidth = effectiveActive
				exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", effectiveActive)).Run()
				// Persist the per-profile width NOW (before per-client target
				// computation) so sidebarReasonableMaxForWindow reads the new
				// value, not the stale one. Otherwise the clamp would revert the
				// drag on the same pass.
				c.persistSidebarWidthProfile(activeWindowID, effectiveActive)
				adoptedActiveWidth = effectiveActive
			}
		}
	}

	for clientID, currentWidth := range clientSnapshot {
		currentHeight := clientHeightSnapshot[clientID]
		// Skip header clients
		if isHeaderClient(clientID) {
			continue
		}

		// Check per-window sync opt-out
		if !syncSettings[clientID] {
			continue
		}

		if c.globalWidth == 0 {
			c.globalWidth = currentWidth
		}

		expiry, holdActive := keyboardHoldSnapshot[clientID]
		now := time.Now()
		applyKeyboardClamp := false
		if currentHeight > 0 && currentHeight <= keyboardThreshold {
			applyKeyboardClamp = true
		}
		if holdActive {
			if now.After(expiry) {
				expiredHolds = append(expiredHolds, clientID)
			} else {
				applyKeyboardClamp = true
			}
		}

		// Compute target: preferred globalWidth, bounded by per-window constraints,
		// keyboard clamp, then finally capped to what the active physical client
		// can actually honor. The final cap is the key anti-thrash fix: tmux
		// silently clamps resize-pane on narrow clients, and the clamped result
		// comes back as a new MsgResize -> without this cap, we'd fight tmux in
		// a loop.
		targetWidth := c.boundedSidebarWidthForWindow(clientID, c.globalWidth, currentHeight)
		// NOTE: the old "phone profile -> targetWidth = 1" branch has been
		// removed. Phone hides the sidebar by break-pane'ing it to a holding
		// window (see SetActiveClientWidth + hideSidebarPanes); any live
		// sidebar that reaches this loop should keep its normal bounded width.
		// Forcing 1 here caused a one-frame visible collapse during the race
		// between profile transition and the async stash goroutine.
		if applyKeyboardClamp && targetWidth > keyboardWidth {
			targetWidth = keyboardWidth
		}
		targetWidth = c.capTargetToActiveClient(targetWidth)

		// Use actual tmux pane width if available (client-reported width can be stale)
		if actualW, ok := actualPaneWidths[clientID]; ok && actualW != currentWidth {
			currentWidth = actualW
		}

		// If current width already matches the capped target, nothing to do.
		if currentWidth == targetWidth {
			logEvent("WIDTH_SYNC_SKIP_NOOP client=%s width=%d", clientID, currentWidth)
			continue
		}

		// Only issue a resize when the target differs AND the active client can
		// plausibly hold it. capTargetToActiveClient already enforces the cap,
		// so reaching here means an honest resize is needed.
		coordinatorDebugLog.Printf("Width sync: window=%s current=%d target=%d", clientID, currentWidth, targetWidth)
		logEvent("WIDTH_SYNC_PLAN client=%s active=%s current=%d target=%d", clientID, activeWindowID, currentWidth, targetWidth)
		ops = append(ops, resizeOp{clientID: clientID, targetWidth: targetWidth, paneID: sidebarPaneIDs[clientID]})
		if applyKeyboardClamp {
			extendHolds = append(extendHolds, clientID)
		}
		_ = force // no longer drives separate branch; targets are always honest now
	}

	if len(ops) > 0 {
		c.lastWidthSync = time.Now()
	}

	c.widthSyncMu.Unlock()

	if len(expiredHolds) > 0 || len(extendHolds) > 0 {
		now := time.Now()
		c.clientWidthsMu.Lock()
		if len(expiredHolds) > 0 {
			for _, id := range expiredHolds {
				if expiry, ok := c.keyboardHoldUntil[id]; ok && now.After(expiry) {
					delete(c.keyboardHoldUntil, id)
				}
			}
		}
		if len(extendHolds) > 0 {
			if c.keyboardHoldUntil == nil {
				c.keyboardHoldUntil = make(map[string]time.Time)
			}
			for _, id := range extendHolds {
				c.keyboardHoldUntil[id] = now.Add(mobileKeyboardHoldDuration)
			}
		}
		c.clientWidthsMu.Unlock()
	}

	elapsed := time.Since(start)
	if len(ops) == 0 {
		logEvent("WIDTH_SYNC_NOOP active=%s force=%v duration_ms=%d since_last_ms=%d", activeWindowID, force, elapsed.Milliseconds(), sinceLast.Milliseconds())
	} else {
		logEvent("WIDTH_SYNC_EXEC active=%s force=%v ops=%d duration_ms=%d since_last_ms=%d", activeWindowID, force, len(ops), elapsed.Milliseconds(), sinceLast.Milliseconds())
	}

	_ = adoptedActiveWidth // (profile persistence done inline above, before target computation)

	// Convert internal ops (windowID-keyed) into ResizeOps (paneID-keyed) so
	// the caller can batch them with header / window-size ops in a single
	// chained tmux invocation. Skip ops whose sidebar pane wasn't found in
	// the upfront list-panes (window may have been killed mid-cycle).
	out := make([]ResizeOp, 0, len(ops))
	for _, op := range ops {
		paneID := op.paneID
		if paneID == "" {
			continue
		}
		out = append(out, ResizeOp{
			Kind:    OpResizePaneX,
			Target:  paneID,
			X:       op.targetWidth,
			Reason:  "width_sync",
			Subject: op.clientID,
		})
		coordinatorDebugLog.Printf("RESIZE_SIDEBAR pane=%s width=%d (planned)", paneID, op.targetWidth)
		logEvent("WIDTH_SYNC_RESIZE client=%s pane=%s width=%d", op.clientID, paneID, op.targetWidth)
	}
	return out
}

// RunWidthSync is the standalone-caller wrapper around PlanWidthSync. It
// flushes the planned ops through flushOpsBatched so the per-window
// resize-pane calls land as a single chained tmux command instead of N
// separate invocations.
func (c *Coordinator) RunWidthSync(activeWindowID string, force bool) {
	flushOpsBatched(c.PlanWidthSync(activeWindowID, force), "width_sync")
}

// desiredPaneHeaderHeight returns the number of rows a per-content-pane header pane should occupy.
// Pane-headers are always 1 row (or 2 with CustomBorder). They never grow to 3 on phone —
// the phone button bar lives on the window-header instead.
func (c *Coordinator) desiredPaneHeaderHeight() int {
	if c.config.PaneHeader.CustomBorder {
		return 2
	}
	return 1
}

// desiredWindowHeaderHeight returns the number of rows a window-header pane should
// occupy, based on the active client width. Matches the per-width version's
// touch-client threshold (< 100 cols = 3-row fat-touch button bar) so that spawning
// and height-sync agree; otherwise the spawn-time default of 1 would flicker up to 3.
func (c *Coordinator) desiredWindowHeaderHeight() int {
	acw := int(c.activeClientWidth.Load())
	if acw > 0 && acw < 100 {
		return 3
	}
	if c.config.PaneHeader.CustomBorder {
		return 2
	}
	return 1
}

// desiredWindowHeaderHeightForWidth returns the header height based on a specific
// window width, rather than the global active client profile. This prevents
// phone-profile oscillation from affecting desktop window headers.
func (c *Coordinator) desiredWindowHeaderHeightForWidth(windowWidth int) int {
	// Touch-client threshold is 100 cols; below that we render a 3-row fat-touch header
	// with the hamburger/prev/close/next button bar. Above 100, desktop 1-row title only.
	// Distinct from the sidebar phone threshold (60) which governs sidebar width locking.
	if windowWidth > 0 && windowWidth < 100 {
		return 3
	}
	if c.config.PaneHeader.CustomBorder {
		return 2
	}
	return 1
}

// attachedClientCount returns the number of tmux clients attached to the session.
func (c *Coordinator) attachedClientCount() int {
	out, err := exec.Command("tmux", "list-clients", "-F", "#{client_tty}").Output()
	if err != nil {
		return 1
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			count++
		}
	}
	return count
}

// PlanHeaderHeights iterates pane-header panes across all windows and
// returns ResizeOps for any whose current height differs from the target.
// Uses per-window width (not global profile) to avoid phone/desktop
// oscillation. Returns ops without executing — caller flushes via
// flushOpsBatched (RunHeaderHeightSync wrapper) or concatenates with
// width-sync / window-size ops for one chained tmux command (Loop.Reconcile).
//
// lockedWindowWidth, when > 0, overrides the per-window width returned by
// tmux for window-header height computation. Used by Reconcile when it is
// also planning a resize-window-to-active-client lock in the same batch:
// without the override, listHeaderPanes() reads the pre-flush window
// widths and plans 0 header ops on a wide→narrow client switch, leaving
// the touch tab bar at 1 row when it should be 3.
func (c *Coordinator) PlanHeaderHeights(activeClientID string, lockedWindowWidth int) []ResizeOp {
	logEvent("HEADER_HEIGHT_SYNC activeClient=%s lockedWidth=%d", activeClientID, lockedWindowWidth)

	headers := listHeaderPanes()
	if len(headers) == 0 {
		return nil
	}
	out := make([]ResizeOp, 0, len(headers))
	for _, h := range headers {
		var target int
		if h.IsWindowHdr {
			effectiveWidth := h.WindowWidth
			if lockedWindowWidth > 0 {
				effectiveWidth = lockedWindowWidth
			}
			target = c.desiredWindowHeaderHeightForWidth(effectiveWidth)
		} else {
			// Pane-headers are always 1 row (or 2 with CustomBorder); never 3 on phone.
			target = c.desiredPaneHeaderHeight()
		}
		if h.CurrentHeight == target {
			continue
		}
		logEvent("HEADER_HEIGHT_SYNC pane=%s current=%d target=%d winWidth=%d locked=%d", h.PaneID, h.CurrentHeight, target, h.WindowWidth, lockedWindowWidth)
		out = append(out, ResizeOp{
			Kind:    OpResizePaneY,
			Target:  h.PaneID,
			Y:       target,
			Reason:  "header_height_sync",
			Subject: h.PaneID,
		})
	}
	return out
}

// RunHeaderHeightSync is the standalone-caller wrapper around
// PlanHeaderHeights. It flushes the planned ops through flushOpsBatched so
// the per-header resize-pane calls land as a single chained tmux command.
func (c *Coordinator) RunHeaderHeightSync(activeClientID string) {
	flushOpsBatched(c.PlanHeaderHeights(activeClientID, 0), "header_height_sync")
}

// RunZoomSync is a no-op placeholder. Auto-zoom was removed because tmux zoom
// is per-window, not per-client — it causes constant fighting between phone
// and desktop clients. Mobile support uses the carousel header instead.
func (c *Coordinator) RunZoomSync(activeWindowID string) {
	// Intentionally empty — zoom conflicts with multi-client sessions
}

func (c *Coordinator) persistSidebarWidthProfile(windowID string, width int) {
	if windowID == "" || width < 10 {
		return
	}

	windowWidthOut, err := tmuxOutputCtx("display-message", "-p", "-t", windowID, "#{window_width}")
	if err != nil {
		return
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(windowWidthOut)))
	if err != nil || windowWidth <= 0 {
		return
	}

	mobileMax := 110
	if out, err := tmuxOutputCtx("show-option", "-gqv", "@tabby_sidebar_mobile_max_window_cols"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 60 {
			mobileMax = v
		}
	}

	tabletMax := 170
	if out, err := tmuxOutputCtx("show-option", "-gqv", "@tabby_sidebar_tablet_max_window_cols"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= mobileMax {
			tabletMax = v
		}
	}

	opt := "@tabby_sidebar_width_desktop"
	if windowWidth <= mobileMax {
		opt = "@tabby_sidebar_width_mobile"
	} else if windowWidth <= tabletMax {
		opt = "@tabby_sidebar_width_tablet"
	}
	setTmuxGlobalOption(opt, fmt.Sprintf("%d", width))
}

func (c *Coordinator) isLikelyAutoConstrainedSidebarWidth(windowID string, currentWidth int) bool {
	if c.globalWidth <= 0 || windowID == "" {
		return false
	}

	maxReasonable, ok := c.sidebarReasonableMaxForWindow(windowID, 0)
	if !ok {
		return false
	}

	return currentWidth <= maxReasonable && maxReasonable < c.globalWidth
}

func (c *Coordinator) boundedSidebarWidthForWindow(windowID string, requested int, clientHeight int) int {
	if requested <= 0 {
		return requested
	}
	maxReasonable, ok := c.sidebarReasonableMaxForWindow(windowID, clientHeight)
	if !ok {
		return requested
	}
	if requested > maxReasonable {
		return maxReasonable
	}
	return requested
}

func (c *Coordinator) getMobileKeyboardSettings() (int, int) {
	keyboardWidth := 0
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_width_mobile_keyboard")); err == nil && v >= 6 {
		keyboardWidth = v
	}
	if keyboardWidth <= 0 {
		if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_width_mobile")); err == nil && v >= 10 {
			keyboardWidth = v
		}
	}
	if keyboardWidth <= 0 {
		keyboardWidth = 15
	}
	if keyboardWidth < 10 {
		keyboardWidth = 10
	}

	keyboardThreshold := 38
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_keyboard_rows")); err == nil && v >= 10 {
		keyboardThreshold = v
	}
	return keyboardWidth, keyboardThreshold
}

func (c *Coordinator) updateKeyboardHoldLocked(clientID string, reportedHeight int) {
	if clientID == "" || isHeaderClient(clientID) {
		return
	}
	_, keyboardThreshold := c.getMobileKeyboardSettings()
	height := reportedHeight
	if height <= 0 && c.clientHeights != nil {
		if h, ok := c.clientHeights[clientID]; ok {
			height = h
		}
	}
	now := time.Now()
	if height > 0 && height <= keyboardThreshold {
		if c.keyboardHoldUntil == nil {
			c.keyboardHoldUntil = make(map[string]time.Time)
		}
		c.keyboardHoldUntil[clientID] = now.Add(mobileKeyboardHoldDuration)
	} else if c.keyboardHoldUntil != nil {
		if expiry, ok := c.keyboardHoldUntil[clientID]; ok && now.After(expiry) {
			delete(c.keyboardHoldUntil, clientID)
		}
	}
}

// sidebarHardCeiling is the pure arithmetic behind the window-relative sidebar
// width ceiling: the smaller of maxPercent of the window width and
// (windowWidth - minContentCols), never below 15. Extracted so the clamp logic
// is unit-testable without a live tmux server.
func sidebarHardCeiling(windowWidth, maxPercent, minContentCols int) int {
	ceiling := windowWidth * maxPercent / 100
	if byContent := windowWidth - minContentCols; byContent < ceiling {
		ceiling = byContent
	}
	if ceiling < 15 {
		ceiling = 15
	}
	return ceiling
}

// sidebarHardCeilingForWindow returns the largest sidebar width that is
// reasonable for the given window, derived purely from window geometry — a
// fraction (@tabby_sidebar_mobile_max_percent, default 20%) of the window
// width, floored so content keeps at least @tabby_sidebar_mobile_min_content_cols
// columns. Unlike sidebarReasonableMaxForWindow it deliberately does NOT consider
// any persisted/configured per-profile width, so it can clamp a freshly adopted
// drag width: the user may widen up to this ceiling, but no further. Returns
// (0,false) if the window width can't be read (caller should not clamp).
func (c *Coordinator) sidebarHardCeilingForWindow(windowID string) (int, bool) {
	if windowID == "" {
		return 0, false
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width}").Output()
	if err != nil {
		return 0, false
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || windowWidth <= 0 {
		return 0, false
	}
	maxPercent := 20
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_max_percent")); err == nil && v >= 10 && v <= 60 {
		maxPercent = v
	}
	minContentCols := 40
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_min_content_cols")); err == nil && v >= 20 {
		minContentCols = v
	}
	return sidebarHardCeiling(windowWidth, maxPercent, minContentCols), true
}

func (c *Coordinator) sidebarReasonableMaxForWindow(windowID string, clientHeight int) (int, bool) {
	if windowID == "" {
		return 0, false
	}

	windowWidthOut, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width}").Output()
	if err != nil {
		// Fall back to globalWidth so clamping still works on query failure
		if c.globalWidth > 0 {
			windowWidth := c.globalWidth
			maxPercent := 20
			if v, err2 := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_max_percent")); err2 == nil && v >= 10 && v <= 60 {
				maxPercent = v
			}
			maxWidth := windowWidth * maxPercent / 100
			if maxWidth < 15 {
				maxWidth = 15
			}
			return maxWidth, true
		}
		return 0, false
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(windowWidthOut)))
	if err != nil || windowWidth <= 0 {
		return 0, false
	}

	height := clientHeight
	if height <= 0 {
		if windowHeightOut, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_height}").Output(); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(windowHeightOut))); err == nil && v > 0 {
				height = v
			}
		}
	}

	maxPercent := 20
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_max_percent")); err == nil && v >= 10 && v <= 60 {
		maxPercent = v
	}

	minContentCols := 40
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_min_content_cols")); err == nil && v >= 20 {
		minContentCols = v
	}

	maxWindowCols := 110
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_mobile_max_window_cols")); err == nil && v >= 60 {
		maxWindowCols = v
	}

	tabletMaxWindowCols := 170
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_tablet_max_window_cols")); err == nil && v >= maxWindowCols {
		tabletMaxWindowCols = v
	}

	widthDesktop := c.globalWidth
	if widthDesktop < 15 {
		widthDesktop = 25
	}
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_width_desktop")); err == nil && v >= 15 {
		widthDesktop = v
	}

	// Window-relative hard ceiling, shared by every profile branch below: a
	// sidebar should never exceed maxPercent of the window, nor leave less than
	// minContentCols for content. Mobile/keyboard already enforced this; desktop
	// and tablet did not, so a corrupt persisted width (e.g. a runaway drag
	// adopted as the global) propagated verbatim and the sidebar "got very large"
	// on every window. Clamping here heals such a value on the next sync.
	maxByFraction := windowWidth * maxPercent / 100
	if maxByFraction < 15 {
		maxByFraction = 15
	}
	maxByContent := windowWidth - minContentCols
	if maxByContent < 15 {
		maxByContent = 15
	}
	hardCeiling := sidebarHardCeiling(windowWidth, maxPercent, minContentCols)

	if windowWidth > tabletMaxWindowCols {
		if widthDesktop > hardCeiling {
			return hardCeiling, true
		}
		return widthDesktop, true
	}

	widthTablet := 20
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_width_tablet")); err == nil && v >= 15 {
		widthTablet = v
	}

	if windowWidth > maxWindowCols {
		if widthTablet < 15 {
			widthTablet = 15
		}
		if widthTablet > hardCeiling {
			return hardCeiling, true
		}
		return widthTablet, true
	}

	widthMobile := 15
	if v, err := strconv.Atoi(tmuxGlobalOption("@tabby_sidebar_width_mobile")); err == nil && v >= 10 {
		widthMobile = v
	}

	widthMobileKeyboard := widthMobile
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_mobile_keyboard").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 6 {
			widthMobileKeyboard = v
		}
	}

	keyboardThreshold := 0
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_keyboard_rows").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 10 {
			keyboardThreshold = v
		}
	}
	if keyboardThreshold == 0 {
		keyboardThreshold = 38
	}
	if height > 0 && height <= keyboardThreshold {
		// Keyboard mode: enforce the same fraction/content floors as the
		// non-keyboard mobile branch. Without these, a corrupt user
		// preference (e.g. @tabby_sidebar_width_mobile_keyboard=75 on a
		// 75-col phone) would propagate verbatim, defeating every clamp.
		maxWidth := widthMobileKeyboard
		if maxByFraction < maxWidth {
			maxWidth = maxByFraction
		}
		if maxByContent < maxWidth {
			maxWidth = maxByContent
		}
		if maxWidth < 10 {
			maxWidth = 10
		}
		return maxWidth, true
	}

	maxReasonable := maxByFraction
	if maxByContent < maxReasonable {
		maxReasonable = maxByContent
	}
	if widthMobile < maxReasonable {
		maxReasonable = widthMobile
	}
	if maxReasonable < 10 {
		maxReasonable = 10
	}

	return maxReasonable, true
}

// RenderForClient generates content for a specific client's dimensions
func (c *Coordinator) RenderForClient(clientID string, width, height int) *daemon.RenderPayload {
	// Guard dimensions
	if width < 3 {
		width = 3
	}
	if height < 5 {
		height = 24
	}

	// NOTE: Width sync has been moved off the render path to prevent deadlocks.
	// It now runs from the main event loop via RunWidthSync().
	// The "collapsed 1-col strip" render path has been removed: hide/show is
	// done by break-pane/join-pane so the renderer never renders while hidden.

	// Normal render - guard minimum width
	if width < 10 {
		width = 25
	}

	// Track width for pet physics (safe to update outside lock - advisory)
	c.lastWidth = width

	// Store per-client size for accurate click detection on resize
	c.UpdateClientSizeSnapshot(clientID, width, height)

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Generate sidebar header (session name, gear, position toggle, collapse)
	headerContent, headerRegions := c.generateSidebarHeader(width, clientID)
	headerLines := strings.Count(headerContent, "\n")

	topWidgets, topWRegions, bottomWidgets, bottomWRegions := c.generateWidgetZones(width, false, false)
	topWidgetLines := strings.Count(topWidgets, "\n")
	bottomWidgetLines := strings.Count(bottomWidgets, "\n")

	mainContent, mainRegions, floatLine := c.generateMainContent(clientID, width, height)
	mainContentLines := strings.Count(mainContent, "\n")

	maxMainLines := height - headerLines - topWidgetLines - bottomWidgetLines
	if maxMainLines < 0 {
		maxMainLines = 0
	}

	// Tight-viewport escalation: drop the pet's debug bar before dropping the
	// pet entirely. Debug bar costs 3 lines (divider + 2 status lines), so
	// suppressing it often keeps Whiskers visible when tabs are otherwise
	// just barely overflowing.
	if maxMainLines < mainContentLines && c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.DebugBar {
		topWidgets, topWRegions, bottomWidgets, bottomWRegions = c.generateWidgetZones(width, false, true)
		topWidgetLines = strings.Count(topWidgets, "\n")
		bottomWidgetLines = strings.Count(bottomWidgets, "\n")
		maxMainLines = height - headerLines - topWidgetLines - bottomWidgetLines
		if maxMainLines < 0 {
			maxMainLines = 0
		}
	}

	// Auto-hide pet when viewport is too small to show all tabs even after
	// dropping the debug bar.
	if maxMainLines < mainContentLines && c.config.Widgets.Pet.Enabled {
		topWidgets, topWRegions, bottomWidgets, bottomWRegions = c.generateWidgetZones(width, true, false)
		topWidgetLines = strings.Count(topWidgets, "\n")
		bottomWidgetLines = strings.Count(bottomWidgets, "\n")
		maxMainLines = height - headerLines - topWidgetLines - bottomWidgetLines
		if maxMainLines < 0 {
			maxMainLines = 0
		}
	}

	mainContent, mainRegions = trimContentAndRegions(mainContent, mainRegions, maxMainLines)
	mainLines := strings.Count(mainContent, "\n")

	// Pad main content to pin bottom widgets to the viewport bottom. When there's
	// a floated "Minimized" section, insert the gap BEFORE it (pushing it to the
	// bottom of the tab area) and shift its rows' click regions down to match;
	// otherwise append the gap at the end as usual.
	if mainLines < maxMainLines {
		pad := maxMainLines - mainLines
		if floatLine >= 0 && floatLine <= mainLines {
			lines := strings.Split(mainContent, "\n")
			hadTrailing := strings.HasSuffix(mainContent, "\n")
			if hadTrailing && len(lines) > 0 {
				lines = lines[:len(lines)-1]
			}
			if floatLine <= len(lines) {
				gap := make([]string, pad)
				newLines := make([]string, 0, len(lines)+pad)
				newLines = append(newLines, lines[:floatLine]...)
				newLines = append(newLines, gap...)
				newLines = append(newLines, lines[floatLine:]...)
				mainContent = strings.Join(newLines, "\n")
				if hadTrailing {
					mainContent += "\n"
				}
				for i := range mainRegions {
					if mainRegions[i].StartLine >= floatLine {
						mainRegions[i].StartLine += pad
						mainRegions[i].EndLine += pad
					}
				}
			} else {
				mainContent += strings.Repeat("\n", pad)
			}
		} else {
			mainContent += strings.Repeat("\n", pad)
		}
		mainLines = maxMainLines
	}

	// Offset top widget regions by header height
	for i := range topWRegions {
		topWRegions[i].StartLine += headerLines
		topWRegions[i].EndLine += headerLines
	}

	// Offset main content regions by header + top widgets
	mainOffset := headerLines + topWidgetLines
	for i := range mainRegions {
		mainRegions[i].StartLine += mainOffset
		mainRegions[i].EndLine += mainOffset
	}

	// Store the content start line for pet widget click detection
	// This tells us where the bottom zone starts in absolute content coordinates
	bottomOffset := headerLines + topWidgetLines + mainLines
	c.petLayout.ContentStartLine = bottomOffset

	// Offset bottom widget regions by header + top widgets + main content
	for i := range bottomWRegions {
		bottomWRegions[i].StartLine += bottomOffset
		bottomWRegions[i].EndLine += bottomOffset
	}

	// Combine everything: header + top_widgets + main + bottom_widgets
	fullContent := headerContent + topWidgets + mainContent + bottomWidgets

	// Don't apply background fill - let terminal's natural background (set via ApplyThemeToPane) show through

	allRegions := append(headerRegions, topWRegions...)
	allRegions = append(allRegions, mainRegions...)
	allRegions = append(allRegions, bottomWRegions...)

	// Count total lines
	totalLines := strings.Count(fullContent, "\n")

	// Debug logging
	coordinatorDebugLog.Printf("RenderForClient: client=%s width=%d height=%d", clientID, width, height)
	coordinatorDebugLog.Printf("  Content: %d lines (%d header + %d topW + %d main + %d bottomW)",
		totalLines, headerLines, topWidgetLines, mainLines, bottomWidgetLines)
	coordinatorDebugLog.Printf("  Regions: %d total", len(allRegions))

	sidebarBg := ""
	terminalBg := ""
	if c.theme != nil {
		sidebarBg = c.theme.SidebarBg
		terminalBg = c.theme.TerminalBg
	}

	return &daemon.RenderPayload{
		Content:       fullContent,
		PinnedContent: "", // No longer using pinned content
		Width:         width,
		Height:        height,
		TotalLines:    totalLines,
		PinnedHeight:  0, // No pinned section
		Regions:       allRegions,
		PinnedRegions: nil, // All regions are in main Regions array now
		SidebarBg:     sidebarBg,
		TerminalBg:    terminalBg,
	}
}

func trimContentAndRegions(content string, regions []daemon.ClickableRegion, maxLines int) (string, []daemon.ClickableRegion) {
	if maxLines < 0 {
		maxLines = 0
	}
	if content == "" {
		return "", nil
	}

	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	if hasTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	if len(lines) <= maxLines {
		return content, regions
	}

	if maxLines == 0 {
		return "", nil
	}

	trimmedLines := lines[:maxLines]
	trimmedContent := strings.Join(trimmedLines, "\n") + "\n"

	filteredRegions := make([]daemon.ClickableRegion, 0, len(regions))
	maxIdx := maxLines - 1
	for _, r := range regions {
		if r.StartLine > maxIdx {
			continue
		}
		if r.EndLine > maxIdx {
			r.EndLine = maxIdx
		}
		filteredRegions = append(filteredRegions, r)
	}

	return trimmedContent, filteredRegions
}

// abbreviatePath shortens a path for display in the header.
// It replaces the home directory with ~ and shows only the last 2-3 path components.
func abbreviatePath(path string, maxWidth int) string {
	if path == "" {
		return ""
	}

	// Replace home directory with ~
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}

	// If already short enough, return as-is
	if uniseg.StringWidth(path) <= maxWidth {
		return path
	}

	// Split path into components
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) == 0 {
		return path
	}

	// Start with just the last component
	var result string
	if parts[0] == "~" {
		// Keep the ~ prefix
		result = "~/"
		parts = parts[1:]
	} else if parts[0] == "" && len(parts) > 1 {
		// Absolute path starting with /
		result = "/"
		parts = parts[1:]
	}

	// Build from the end, adding components until we run out of space
	var components []string
	for i := len(parts) - 1; i >= 0; i-- {
		components = append([]string{parts[i]}, components...)
		testPath := result + strings.Join(components, "/")
		if i > 0 {
			testPath = result + ".../" + strings.Join(components, "/")
		}
		if uniseg.StringWidth(testPath) > maxWidth {
			// Too long, use previous iteration
			if len(components) == 1 {
				// Even one component is too long, truncate it
				return runewidth.Truncate(result+components[0], maxWidth, "")
			}
			components = components[1:] // Remove the component we just tried to add
			break
		}
	}

	// Build final path
	if len(components) < len(parts) {
		return result + ".../" + strings.Join(components, "/")
	}
	return result + strings.Join(components, "/")
}

// RenderHeaderForClient renders a 1-line window header for a specific window.
// Each window has one window-header that shows the active pane's label.
// clientID format: "window-header:@123" where @123 is the window ID.
// renderPhoneCarousel builds the 3-row fat-touch phone window-header (the bottom
// button bar: prev, hamburger, cycle, new, close, next). It depends ONLY on the
// window id, the pane width, and the pressed-button state — NOT on any content
// pane. That's important: while the full-width phone sidebar is open the window
// has no content pane (it's stashed in limbo), and RenderHeaderForClient's
// content-pane lookup would otherwise blank the bar. Returns (content, regions,
// totalLines).
func (c *Coordinator) renderPhoneCarousel(windowID string, width int, headerBg string) (string, []daemon.ClickableRegion, int) {
	var regions []daemon.ClickableRegion
	const buttonCount = 6

	fgColor := lipgloss.Color("230") // off-white
	pressFg := lipgloss.Color("16")
	pressBg := lipgloss.Color("255")
	navBg := lipgloss.Color("24")    // blue
	menuBg := lipgloss.Color("240")  // gray
	cycleBg := lipgloss.Color("130") // amber
	newBg := lipgloss.Color("28")    // green
	closeBg := lipgloss.Color("124") // red

	prevStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(navBg)
	hamStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(menuBg)
	cycleStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(cycleBg)
	newStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(newBg)
	closeStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(closeBg)
	nextStyle := lipgloss.NewStyle().Bold(true).Foreground(fgColor).Background(navBg)
	btnPressStyle := lipgloss.NewStyle().Bold(true).Foreground(pressFg).Background(pressBg)
	fillStyle := lipgloss.NewStyle()
	if headerBg != "" {
		fillStyle = fillStyle.Background(lipgloss.Color(headerBg))
	}

	hamburger := "≡"  // ≡
	prevGlyph := "▲"  // ▲
	cycleGlyph := "⛶"      // toggle zoom on the active content pane
	newGlyph := "+"        // new window
	closeGlyph := "✕" // ✕
	nextGlyph := "▼"  // ▼

	gapW := 1
	cellW := (width - gapW*(buttonCount-1)) / buttonCount
	if cellW < 3 {
		cellW = 3
	}
	used := cellW*buttonCount + gapW*(buttonCount-1)
	if used > width {
		gapW = 0
		cellW = width / buttonCount
		if cellW < 1 {
			cellW = 1
		}
		used = cellW * buttonCount
		if used > width {
			used = width
		}
	}
	leftover := width - used
	if leftover < 0 {
		leftover = 0
	}

	pressedAction := c.activeWindowHeaderPress(windowID)
	isPrevPressed := pressedAction == "window_header:prev_window"
	isHamPressed := pressedAction == "window_header:hamburger"
	isCyclePressed := pressedAction == "window_header:cycle_pane"
	isNewPressed := pressedAction == "window_header:new_window"
	isClosePressed := pressedAction == "window_header:close_window"
	isNextPressed := pressedAction == "window_header:next_window"

	styleFor := func(base lipgloss.Style, pressed bool) lipgloss.Style {
		if pressed {
			return btnPressStyle
		}
		return base
	}
	renderCell := func(glyph string, base lipgloss.Style, pressed bool) string {
		style := styleFor(base, pressed)
		gw := uniseg.StringWidth(glyph)
		if gw > cellW {
			return style.Render(runewidth.Truncate(glyph, cellW, ""))
		}
		padTotal := cellW - gw
		leftPad := padTotal / 2
		rightPad := padTotal - leftPad
		return style.Render(strings.Repeat(" ", leftPad) + glyph + strings.Repeat(" ", rightPad))
	}
	renderEmpty := func(base lipgloss.Style, pressed bool) string {
		return styleFor(base, pressed).Render(strings.Repeat(" ", cellW))
	}

	gapStr := fillStyle.Render(strings.Repeat(" ", gapW))
	midGapW := gapW + leftover
	midGapStr := fillStyle.Render(strings.Repeat(" ", midGapW))

	middleBare := renderCell(prevGlyph, prevStyle, isPrevPressed) + gapStr +
		renderCell(hamburger, hamStyle, isHamPressed) + gapStr +
		renderCell(cycleGlyph, cycleStyle, isCyclePressed) + midGapStr +
		renderCell(newGlyph, newStyle, isNewPressed) + gapStr +
		renderCell(closeGlyph, closeStyle, isClosePressed) + gapStr +
		renderCell(nextGlyph, nextStyle, isNextPressed)
	blankRow := renderEmpty(prevStyle, isPrevPressed) + gapStr +
		renderEmpty(hamStyle, isHamPressed) + gapStr +
		renderEmpty(cycleStyle, isCyclePressed) + midGapStr +
		renderEmpty(newStyle, isNewPressed) + gapStr +
		renderEmpty(closeStyle, isClosePressed) + gapStr +
		renderEmpty(nextStyle, isNextPressed)
	emptyRow := blankRow

	p0 := 0
	p1 := cellW
	h0 := p1 + gapW
	h1 := h0 + cellW
	cy0 := h1 + gapW
	cy1 := cy0 + cellW
	nw0 := cy1 + midGapW
	nw1 := nw0 + cellW
	cl0 := nw1 + gapW
	cl1 := cl0 + cellW
	n0 := cl1 + gapW
	n1 := n0 + cellW
	regions = append(regions,
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: p0, EndCol: p1, Action: "window_header:prev_window", Target: windowID},
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: h0, EndCol: h1, Action: "window_header:hamburger", Target: windowID},
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: cy0, EndCol: cy1, Action: "window_header:cycle_pane", Target: windowID},
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: nw0, EndCol: nw1, Action: "window_header:new_window", Target: windowID},
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: cl0, EndCol: cl1, Action: "window_header:close_window", Target: windowID},
		daemon.ClickableRegion{StartLine: 0, EndLine: 2, StartCol: n0, EndCol: n1, Action: "window_header:next_window", Target: windowID},
	)

	content := emptyRow + "\n" + middleBare + "\n" + emptyRow
	return content, regions, 3
}

// phoneCarouselPayload wraps renderPhoneCarousel into a RenderPayload. Used by the
// early-return paths in RenderHeaderForClient so the button bar stays visible +
// clickable even when the window has no content pane (full-width sidebar open).
func (c *Coordinator) phoneCarouselPayload(windowID string, width int) *daemon.RenderPayload {
	// Fill the bar's background with the window's tab colour (matching the normal
	// carousel, which uses the active pane's header bg) so it doesn't render on a
	// transparent background when there's no content pane. Both getters are
	// lock-free reads — safe under the render RLock held by the caller.
	headerBg := ""
	if _, bg, ok := c.getWindowTabColors(windowID, true); ok {
		headerBg = bg
	}
	if headerBg == "" {
		headerBg = c.getPaneHeaderActiveBg()
	}
	content, regions, tl := c.renderPhoneCarousel(windowID, width, headerBg)
	return &daemon.RenderPayload{
		Content:    content,
		Width:      width,
		Height:     tl,
		TotalLines: tl,
		Regions:    regions,
	}
}

// gradientBarPayload builds a 1-line header payload filled with the standard
// lighter->base->dark-tail gradient (see applyGradientFill), used for the
// titlebar/window-header bar when there's no pane content to render on it — so the
// bar reads as part of the same gradient surface as the tabs instead of a flat
// strip. A non-hex/empty bg falls back to a blank line.
func (c *Coordinator) gradientBarPayload(bg string, width int) *daemon.RenderPayload {
	line := strings.Repeat(" ", width)
	if len(bg) == 7 && bg[0] == '#' {
		line = c.applyGradientFill("", gradientEndColor(bg), bg, width)
	}
	return &daemon.RenderPayload{Content: line, Width: width, Height: 1, TotalLines: 1}
}

func (c *Coordinator) RenderHeaderForClient(clientID string, width, height int) *daemon.RenderPayload {
	if width < 5 {
		width = 5
	}

	// Parse window ID from clientID
	windowID := strings.TrimPrefix(clientID, "window-header:")
	if windowID == "" {
		return nil
	}

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the window
	var foundWindow *tmux.Window
	for i := range c.windows {
		if c.windows[i].ID == windowID {
			foundWindow = &c.windows[i]
			break
		}
	}

	// Determine which content pane to show (prefer active, fall back to first non-system)
	paneID := ""
	if foundWindow != nil {
		for i := range foundWindow.Panes {
			p := foundWindow.Panes[i]
			if isAuxiliaryPane(p) {
				continue
			}
			if p.Active {
				paneID = p.ID
				break
			}
			if paneID == "" {
				paneID = p.ID
			}
		}
	}

	if foundWindow == nil {
		// Phone: still render the carousel — it needs only the window id, and while
		// the full-width sidebar is open the window has no content pane to key off.
		if width < 100 {
			return c.phoneCarouselPayload(windowID, width)
		}
		// No window found: gradient a bar in the neutral header colour rather than
		// leaving a flat pale strip.
		return c.gradientBarPayload(c.getPaneHeaderActiveBg(), width)
	}

	// Find the specific pane this header belongs to
	var foundPane *tmux.Pane
	for i := range foundWindow.Panes {
		if foundWindow.Panes[i].ID == paneID {
			foundPane = &foundWindow.Panes[i]
			break
		}
	}
	if foundPane == nil {
		if width < 100 {
			return c.phoneCarouselPayload(windowID, width)
		}
		// Window with no content pane (e.g. the sidebar's own window — all panes are
		// auxiliary). Previously this rendered a FLAT pale strip; instead gradient a
		// bar in the window's tab colour so the titlebar border matches the tab
		// rows/TABBY header surface.
		bg, _, _ := c.getWindowTabColors(windowID, foundWindow.Active)
		if bg == "" {
			bg = c.getPaneHeaderActiveBg()
		}
		return c.gradientBarPayload(bg, width)
	}

	// Detect which resize directions apply to this pane based on neighbors,
	// and whether borders exist below/to-the-right (for arrow direction mapping).
	canResizeH := false // horizontal neighbors (side by side) -> left/right (←→)
	canResizeV := false // vertical neighbors (stacked) -> up/down (↑↓)
	hasBorderBelow := false
	hasBorderRight := false
	for _, op := range foundWindow.Panes {
		if isAuxiliaryPane(op) || op.ID == foundPane.ID {
			continue
		}
		// Horizontal neighbor: vertical ranges overlap -> panes are side by side
		if max(foundPane.Top, op.Top) < min(foundPane.Top+foundPane.Height, op.Top+op.Height) {
			canResizeH = true
			// Neighbor to the right?
			if op.Left >= foundPane.Left+foundPane.Width {
				hasBorderRight = true
			}
		}
		// Vertical neighbor: horizontal ranges overlap -> panes are stacked
		if max(foundPane.Left, op.Left) < min(foundPane.Left+foundPane.Width, op.Left+op.Width) {
			canResizeV = true
			// Neighbor below?
			if op.Top >= foundPane.Top+foundPane.Height {
				hasBorderBelow = true
			}
		}
	}

	headerColors := c.GetHeaderColorsForPane(paneID)
	headerBg := headerColors.Bg
	headerFg := headerColors.Fg

	// Dim header colors for inactive content panes (no exec — use cached pane state)
	if c.config.PaneHeader.DimInactive {
		paneIsDimmed := false
		for _, p := range foundWindow.Panes {
			if p.ID == paneID && !p.Active {
				paneIsDimmed = true
				break
			}
		}
		if paneIsDimmed {
			opacity := c.config.PaneHeader.DimOpacity
			if opacity <= 0 || opacity > 1 {
				opacity = 0.6
			}
			tBg := c.config.PaneHeader.TerminalBg
			headerBg = desaturateHex(headerBg, opacity, tBg)
			headerFg = desaturateHex(headerFg, opacity, tBg)
		}
	}
	groupColor := headerBg
	isWindowActive := foundWindow.Active

	baseStyle := lipgloss.NewStyle()
	if headerBg != "" {
		baseStyle = baseStyle.Background(lipgloss.Color(headerBg))
	}

	collapseBtn := ""
	isCollapsed := false
	// Count content panes (exclude sidebar and header panes)
	contentPaneCount := 0
	for _, p := range foundWindow.Panes {
		if !isAuxiliaryPane(p) {
			contentPaneCount++
		}
	}
	if contentPaneCount > 1 {
		// Check this specific pane's collapsed state
		if out, err := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_collapsed").Output(); err == nil {
			val := strings.TrimSpace(string(out))
			if val == "1" {
				isCollapsed = true
			}
		}
		if isCollapsed {
			collapseBtn = c.config.PaneHeader.CollapseCollapsedIcon
		} else {
			collapseBtn = c.config.PaneHeader.CollapseExpandedIcon
		}
	}
	splitVBtn := "|"
	splitHBtn := "-"
	if contentPaneCount > 1 && collapseBtn == "" {
		collapseBtn = "▾"
		if isCollapsed {
			collapseBtn = "▸"
		}
	}
	vGrowBtn := c.config.PaneHeader.ResizeVerticalGrowIcon
	if vGrowBtn == "" {
		vGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if vGrowBtn == "" {
		vGrowBtn = "↓"
	}
	vShrinkBtn := c.config.PaneHeader.ResizeVerticalShrinkIcon
	if vShrinkBtn == "" {
		vShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if vShrinkBtn == "" {
		vShrinkBtn = "↑"
	}
	hGrowBtn := c.config.PaneHeader.ResizeHorizontalGrowIcon
	if hGrowBtn == "" {
		hGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if hGrowBtn == "" {
		hGrowBtn = "→"
	}
	hShrinkBtn := c.config.PaneHeader.ResizeHorizontalShrinkIcon
	if hShrinkBtn == "" {
		hShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if hShrinkBtn == "" {
		hShrinkBtn = "←"
	}
	resizeSep := c.config.PaneHeader.ResizeSeparator
	if resizeSep == "" {
		resizeSep = "¦"
	}
	menuBtn := "≡"
	compactSplitVBtn := "|"
	compactSplitHBtn := "-"
	compactCloseBtn := "x"
	closeBtn := "×"
	compactMode := width <= 40
	if compactMode {
		// In compact mode show | - x; add collapse button when multiple panes exist
		if contentPaneCount > 1 && collapseBtn != "" {
			menuBtn = collapseBtn + " " + compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		} else {
			menuBtn = compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		}
	}
	showMenuButton := compactMode
	showInlineControls := !compactMode

	showInlineCollapse := collapseBtn != "" && showInlineControls
	showInlineSplits := showInlineControls
	showVerticalResize := contentPaneCount > 1 && showInlineControls && canResizeV
	showHorizontalResize := contentPaneCount > 1 && showInlineControls && canResizeH
	showInlineClose := showInlineControls
	buttonsStr := "  "
	if showMenuButton {
		buttonsStr += menuBtn + "   "
	}
	if showInlineCollapse {
		buttonsStr += collapseBtn + "  "
	}
	if showInlineSplits {
		buttonsStr += splitVBtn + " " + splitHBtn + "  "
	}
	if showVerticalResize || showHorizontalResize {
		if showVerticalResize {
			buttonsStr += vGrowBtn + " " + vShrinkBtn + " "
		}
		if showHorizontalResize {
			buttonsStr += hGrowBtn + " " + hShrinkBtn + " "
		}
		buttonsStr += " "
	}
	if showInlineClose {
		buttonsStr += closeBtn + "  "
	}
	buttonsWidth := uniseg.StringWidth(buttonsStr)

	// Build label for this pane: "win.pane command [path]"
	// Use visual position (matching sidebar order) instead of tmux index
	label := foundPane.Command
	if foundPane.LockedTitle != "" {
		label = foundPane.LockedTitle
	} else if foundPane.Title != "" && foundPane.Title != foundPane.Command && foundPane.Title != foundWindow.Name {
		label = foundPane.Title
	}
	winVisualNum := c.windowVisualPos[foundWindow.ID]
	labelText := fmt.Sprintf("%d.%d %s", winVisualNum, foundPane.Index, label)

	groupIcon := ""
	for _, group := range c.grouped {
		for _, groupWindow := range group.Windows {
			if groupWindow.ID == foundWindow.ID {
				groupIcon = strings.TrimSpace(group.Theme.Icon)
				break
			}
		}
		if groupIcon != "" {
			break
		}
	}
	windowIcon := strings.TrimSpace(foundWindow.Icon)
	if groupIcon != "" {
		labelText = groupIcon + " " + labelText
	}
	if windowIcon != "" {
		labelText = windowIcon + " " + labelText
	}

	// Compute group accent early so we can account for its width in layout calculations
	groupAccent := ""
	if groupColor != "" {
		groupAccent = lipgloss.NewStyle().SetString("▇").Foreground(lipgloss.Color(groupColor)).String()
	}
	groupAccentWidth := uniseg.StringWidth(stripAnsi(groupAccent))

	// Add current path if available
	if foundPane.CurrentPath != "" {
		// Available width for the label
		availWidth := width - groupAccentWidth - 1 - buttonsWidth // groupAccent + leading space
		if availWidth < 4 {
			availWidth = 4
		}

		// Calculate how much space we have for the path after the base label
		baseWidth := uniseg.StringWidth(labelText)
		pathMaxWidth := availWidth - baseWidth - 1 // 1 for space before path

		if pathMaxWidth > 8 { // Only add path if we have reasonable space (at least 8 chars)
			abbrevPath := abbreviatePath(foundPane.CurrentPath, pathMaxWidth)
			if abbrevPath != "" {
				labelText = fmt.Sprintf("%s %s", labelText, abbrevPath)
			}
		}
	}

	// Available width for the label
	availWidth := width - groupAccentWidth - 1 - buttonsWidth // groupAccent + leading space
	if availWidth < 4 {
		availWidth = 4
	}

	// Truncate label if needed (shouldn't be necessary with our path abbreviation, but just in case)
	if uniseg.StringWidth(labelText) > availWidth {
		labelText = runewidth.Truncate(labelText, availWidth, "~")
	}

	// Style: active pane bold+bright, others dimmed
	isActive := foundPane.Active && isWindowActive
	segStyle := baseStyle.Copy()
	btnStyle := baseStyle.Copy()

	// Always use group's fg color - no manipulation
	if headerFg != "" {
		segStyle = segStyle.Foreground(lipgloss.Color(headerFg))
		btnStyle = btnStyle.Foreground(lipgloss.Color(headerFg))
	}
	if isActive {
		segStyle = segStyle.Bold(true)
	}

	// Build rendered line and click regions
	var regions []daemon.ClickableRegion

	labelWidth := uniseg.StringWidth(labelText)
	renderedLabel := segStyle.Render(labelText)
	currentCol := groupAccentWidth + 1 + labelWidth
	btnAreaStart := width - buttonsWidth
	if btnAreaStart < currentCol {
		btnAreaStart = currentCol
	}
	spacerWidth := btnAreaStart - currentCol

	// Pad the full line with the header background
	fullLineStyle := baseStyle.Copy().Width(width)

	line := groupAccent + " " +
		renderedLabel +
		strings.Repeat(" ", spacerWidth) +
		btnStyle.Render(buttonsStr)

	// Ensure the final rendered line has the correct background applied everywhere
	if headerBg != "" {
		// Gradient the header top-border strip (lighter -> base) to match the live
		// sidebar tab/header surface.
		line = c.applyGradientFill(line, gradientEndColor(headerBg), headerBg, width)
	} else {
		line = fullLineStyle.Render(line)
	}

	// buttonsStr always begins with "  " (2 spaces); skip past them so regions
	// align with the actual button characters.
	cursor := btnAreaStart + 2
	if showMenuButton {
		if compactMode {
			// Optional collapse button (when multi-pane)
			if contentPaneCount > 1 && collapseBtn != "" {
				collapseEnd := cursor + uniseg.StringWidth(collapseBtn) + 1
				regions = append(regions, daemon.ClickableRegion{
					StartLine: 0, EndLine: 0,
					StartCol: cursor, EndCol: collapseEnd,
					Action: "toggle_pane_collapse", Target: paneID,
				})
				cursor = collapseEnd
			}
			splitVEnd := cursor + uniseg.StringWidth(compactSplitVBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: splitVEnd,
				// "|" = vertical divider → side-by-side panes → split-window -h
				Action: "header_split_h", Target: paneID,
			})
			splitHEnd := splitVEnd + uniseg.StringWidth(compactSplitHBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitVEnd, EndCol: splitHEnd,
				// "-" = horizontal divider → stacked panes → split-window -v
				Action: "header_split_v", Target: paneID,
			})
			closeEnd := splitHEnd + uniseg.StringWidth(compactCloseBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitHEnd, EndCol: closeEnd,
				Action: "header_close", Target: paneID,
			})
			cursor = closeEnd + 3
		} else {
			menuEnd := cursor + uniseg.StringWidth(menuBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: menuEnd,
				Action: "pane_menu", Target: paneID,
			})
			cursor = menuEnd + 3
		}
	}
	// Non-compact inline buttons: 1 space within groups, 2 spaces between groups.
	if showInlineCollapse {
		collapseEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: collapseEnd,
			Action: "toggle_pane_collapse", Target: paneID,
		})
		cursor = collapseEnd + 1 // extra space for group gap
	}
	if showInlineSplits {
		splitVEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitVEnd,
			// "|" = vertical divider → side-by-side panes → split-window -h
			Action: "header_split_h", Target: paneID,
		})
		cursor = splitVEnd
		splitHEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitHEnd,
			// "-" = horizontal divider → stacked panes → split-window -v
			Action: "header_split_v", Target: paneID,
		})
		cursor = splitHEnd + 1 // extra space for group gap
	}
	if showVerticalResize || showHorizontalResize {
		// no separator — group gap already added after splits
		if showVerticalResize {
			vGrowEnd := cursor + 2
			vShrinkEnd := vGrowEnd + 2
			// Arrow icons are ↓ and ↑. They represent border movement direction:
			// ↓ = "move border down", ↑ = "move border up".
			// For topmost/middle panes (border below): ↓=grow, ↑=shrink.
			// For bottommost panes (no border below): ↓=shrink, ↑=grow (swapped).
			vDownAction := "pane_grow_v"
			vUpAction := "pane_shrink_v"
			if !hasBorderBelow {
				vDownAction = "pane_shrink_v"
				vUpAction = "pane_grow_v"
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: vGrowEnd,
				Action: vDownAction, Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: vGrowEnd, EndCol: vShrinkEnd,
				Action: vUpAction, Target: paneID,
			})
			cursor = vShrinkEnd
		}
		if showHorizontalResize {
			hGrowEnd := cursor + 2
			hShrinkEnd := hGrowEnd + 2
			// Arrow icons are → and ←. They represent border movement direction:
			// → = "move border right", ← = "move border left".
			// For leftmost/middle panes (border right): →=grow, ←=shrink.
			// For rightmost panes (no border right): →=shrink, ←=grow (swapped).
			hRightAction := "pane_grow_h"
			hLeftAction := "pane_shrink_h"
			if !hasBorderRight {
				hRightAction = "pane_shrink_h"
				hLeftAction = "pane_grow_h"
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: hGrowEnd,
				Action: hRightAction, Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: hGrowEnd, EndCol: hShrinkEnd,
				Action: hLeftAction, Target: paneID,
			})
			cursor = hShrinkEnd
		}
		cursor += 1 // account for the extra " " separator before close (line 4543 in buttonsStr)
	}
	if showInlineClose {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: width,
			Action: "header_close", Target: paneID,
		})
	}

	// Full header area context menu region for non-compact mode only.
	// In compact mode, keep menu opening scoped to the unified menu button.
	if !compactMode {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			Action: "header_context", Target: paneID,
		})
	}

	// Determine active client for this header pane.
	activeClient := c.ActiveClientSnapshot()

	// Phone layout: 6-row header with 2x2 minimum touch targets.
	// Driven by the header pane's actual width, not global profile.
	// Rows 0-1: hamburger + title (2 rows tall)
	// Rows 2-3: carousel prev/next (2 rows tall buttons)
	// Rows 4-5: action buttons (2 rows tall)
	headerRows := c.desiredWindowHeaderHeightForWidth(width)
	content := line
	totalLines := 1
	_ = foundWindow // used by phone layout below
	if width < 100 {
		content, regions, totalLines = c.renderPhoneCarousel(windowID, width, headerBg)
	}

	sidebarBg := ""
	terminalBg := ""
	if c.theme != nil {
		sidebarBg = c.theme.SidebarBg
		terminalBg = c.theme.TerminalBg
	}

	if c.config.PaneHeader.CustomBorder {
		return &daemon.RenderPayload{
			Content:      content,
			Width:        width,
			Height:       headerRows,
			TotalLines:   totalLines,
			Regions:      regions,
			ActiveClient: activeClient,
		}
	}

	return &daemon.RenderPayload{
		Content:      content,
		Width:        width,
		Height:       headerRows,
		TotalLines:   totalLines,
		Regions:      regions,
		SidebarBg:    sidebarBg,
		TerminalBg:   terminalBg,
		ActiveClient: activeClient,
	}
}

// RenderPaneHeaderForClient renders a 1-row title strip for a specific content pane.
// Each content pane has its own header showing that pane's label and action buttons.
// clientID format: "header:%123" where %123 is the pane ID the header sits above.
// On phone profile, this still renders as a single row — buttons are on window-header.
func (c *Coordinator) RenderPaneHeaderForClient(clientID string, width, height int) *daemon.RenderPayload {
	if width < 5 {
		width = 5
	}

	// Parse pane ID from clientID
	paneID := strings.TrimPrefix(clientID, "header:")
	if paneID == "" {
		return nil
	}

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the window this header belongs to
	var foundWindow *tmux.Window
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				foundWindow = &c.windows[i]
				break
			}
		}
		if foundWindow != nil {
			break
		}
	}

	blankLine := strings.Repeat(" ", width)
	if foundWindow == nil {
		return &daemon.RenderPayload{Content: blankLine, Width: width, Height: 1, TotalLines: 1}
	}

	var foundPane *tmux.Pane
	for i := range foundWindow.Panes {
		if foundWindow.Panes[i].ID == paneID {
			foundPane = &foundWindow.Panes[i]
			break
		}
	}
	if foundPane == nil {
		return &daemon.RenderPayload{Content: blankLine, Width: width, Height: 1, TotalLines: 1}
	}

	canResizeH := false
	canResizeV := false
	hasBorderBelow := false
	hasBorderRight := false
	for _, op := range foundWindow.Panes {
		if isAuxiliaryPane(op) || op.ID == foundPane.ID {
			continue
		}
		if max(foundPane.Top, op.Top) < min(foundPane.Top+foundPane.Height, op.Top+op.Height) {
			canResizeH = true
			if op.Left >= foundPane.Left+foundPane.Width {
				hasBorderRight = true
			}
		}
		if max(foundPane.Left, op.Left) < min(foundPane.Left+foundPane.Width, op.Left+op.Width) {
			canResizeV = true
			if op.Top >= foundPane.Top+foundPane.Height {
				hasBorderBelow = true
			}
		}
	}

	headerColors := c.GetHeaderColorsForPane(paneID)
	headerBg := headerColors.Bg
	headerFg := headerColors.Fg

	if c.config.PaneHeader.DimInactive {
		paneIsDimmed := false
		for _, p := range foundWindow.Panes {
			if p.ID == paneID && !p.Active {
				paneIsDimmed = true
				break
			}
		}
		if paneIsDimmed {
			opacity := c.config.PaneHeader.DimOpacity
			if opacity <= 0 || opacity > 1 {
				opacity = 0.6
			}
			tBg := c.config.PaneHeader.TerminalBg
			headerBg = desaturateHex(headerBg, opacity, tBg)
			headerFg = desaturateHex(headerFg, opacity, tBg)
		}
	}
	groupColor := headerBg
	isWindowActive := foundWindow.Active

	baseStyle := lipgloss.NewStyle()
	if headerBg != "" {
		baseStyle = baseStyle.Background(lipgloss.Color(headerBg))
	}

	collapseBtn := ""
	isCollapsed := false
	contentPaneCount := 0
	for _, p := range foundWindow.Panes {
		if !isAuxiliaryPane(p) {
			contentPaneCount++
		}
	}
	if contentPaneCount > 1 {
		if out, err := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_collapsed").Output(); err == nil {
			if strings.TrimSpace(string(out)) == "1" {
				isCollapsed = true
			}
		}
		if isCollapsed {
			collapseBtn = c.config.PaneHeader.CollapseCollapsedIcon
		} else {
			collapseBtn = c.config.PaneHeader.CollapseExpandedIcon
		}
	}
	splitVBtn := "|"
	splitHBtn := "-"
	if contentPaneCount > 1 && collapseBtn == "" {
		collapseBtn = "▾"
		if isCollapsed {
			collapseBtn = "▸"
		}
	}
	vGrowBtn := c.config.PaneHeader.ResizeVerticalGrowIcon
	if vGrowBtn == "" {
		vGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if vGrowBtn == "" {
		vGrowBtn = "↓"
	}
	vShrinkBtn := c.config.PaneHeader.ResizeVerticalShrinkIcon
	if vShrinkBtn == "" {
		vShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if vShrinkBtn == "" {
		vShrinkBtn = "↑"
	}
	hGrowBtn := c.config.PaneHeader.ResizeHorizontalGrowIcon
	if hGrowBtn == "" {
		hGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if hGrowBtn == "" {
		hGrowBtn = "→"
	}
	hShrinkBtn := c.config.PaneHeader.ResizeHorizontalShrinkIcon
	if hShrinkBtn == "" {
		hShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if hShrinkBtn == "" {
		hShrinkBtn = "←"
	}
	closeBtn := "×"
	compactSplitVBtn := "|"
	compactSplitHBtn := "-"
	compactCloseBtn := "x"
	menuBtn := "≡"
	compactMode := width <= 40
	if compactMode {
		if contentPaneCount > 1 && collapseBtn != "" {
			menuBtn = collapseBtn + " " + compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		} else {
			menuBtn = compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		}
	}
	// On narrow/touch clients, pane-headers are plain title strips — all action
	// buttons live on the window-header control bar above. Threshold is wider
	// than the sidebar phone threshold (60) because fiddly pane-header buttons
	// are unusable on any touch client, not just sub-60 SSH sessions.
	// Use window content width (not pane width, which may be a split on desktop).
	winContentWidth := 0
	for _, p := range foundWindow.Panes {
		if isAuxiliaryPane(p) {
			continue
		}
		if right := p.Left + p.Width; right > winContentWidth {
			winContentWidth = right
		}
	}
	isPhone := winContentWidth > 0 && winContentWidth < 100
	showMenuButton := compactMode && !isPhone
	showInlineControls := !compactMode && !isPhone

	showInlineCollapse := collapseBtn != "" && showInlineControls
	showInlineSplits := showInlineControls
	showVerticalResize := contentPaneCount > 1 && showInlineControls && canResizeV
	showHorizontalResize := contentPaneCount > 1 && showInlineControls && canResizeH
	showInlineClose := showInlineControls
	buttonsStr := "  "
	if isPhone {
		buttonsStr = ""
	}
	if showMenuButton {
		buttonsStr += menuBtn + "   "
	}
	if showInlineCollapse {
		buttonsStr += collapseBtn + "  "
	}
	if showInlineSplits {
		buttonsStr += splitVBtn + " " + splitHBtn + "  "
	}
	if showVerticalResize || showHorizontalResize {
		if showVerticalResize {
			buttonsStr += vGrowBtn + " " + vShrinkBtn + " "
		}
		if showHorizontalResize {
			buttonsStr += hGrowBtn + " " + hShrinkBtn + " "
		}
		buttonsStr += " "
	}
	if showInlineClose {
		buttonsStr += closeBtn + "  "
	}
	buttonsWidth := uniseg.StringWidth(buttonsStr)

	title := foundPane.Command
	if foundPane.LockedTitle != "" {
		title = foundPane.LockedTitle
	} else if foundPane.Title != "" && foundPane.Title != foundPane.Command && foundPane.Title != foundWindow.Name {
		title = foundPane.Title
	}
	winVisualNum := c.windowVisualPos[foundWindow.ID]
	numPrefix := fmt.Sprintf("%d.%d", winVisualNum, foundPane.Index)

	groupIcon := ""
	for _, group := range c.grouped {
		for _, groupWindow := range group.Windows {
			if groupWindow.ID == foundWindow.ID {
				groupIcon = strings.TrimSpace(group.Theme.Icon)
				break
			}
		}
		if groupIcon != "" {
			break
		}
	}
	windowIcon := strings.TrimSpace(foundWindow.Icon)

	// SSH/mosh: prepend the remote hostname so the header reads
	//   "<icons> N.M HOST: PATH: Title"
	// Local panes get "<icons> N.M PATH: Title".
	hostName := ""
	if foundPane.Remote {
		hostName = tmux.RemoteHostForPane(foundPane.PID)
	}

	// Assemble the icon/number prefix that always appears before host/path.
	prefixText := numPrefix
	if windowIcon != "" {
		prefixText = windowIcon + " " + prefixText
	}
	if groupIcon != "" {
		prefixText = groupIcon + " " + prefixText
	}

	groupAccent := ""
	if groupColor != "" {
		groupAccent = lipgloss.NewStyle().SetString("▇").Foreground(lipgloss.Color(groupColor)).String()
	}
	groupAccentWidth := uniseg.StringWidth(stripAnsi(groupAccent))

	// Available width for the prefix + host + path + title.
	availWidth := width - groupAccentWidth - 1 - buttonsWidth
	if availWidth < 4 {
		availWidth = 4
	}

	// Build the full label: "<prefix> [HOST: ][PATH: ]Title"
	// PATH gets whatever space remains after prefix, host, separators, and title.
	hostSeg := ""
	if hostName != "" {
		hostSeg = hostName + ": "
	}
	titleSeg := title
	usedW := uniseg.StringWidth(prefixText) + 1 + uniseg.StringWidth(hostSeg) + uniseg.StringWidth(titleSeg)
	pathSeg := ""
	if foundPane.CurrentPath != "" {
		pathBudget := availWidth - usedW - 2 // 2 for ": " separator after path
		if pathBudget > 8 {
			if abbrevPath := abbreviatePath(foundPane.CurrentPath, pathBudget); abbrevPath != "" {
				pathSeg = abbrevPath + ": "
			}
		}
	}
	labelText := prefixText + " " + hostSeg + pathSeg + titleSeg

	if uniseg.StringWidth(labelText) > availWidth {
		labelText = runewidth.Truncate(labelText, availWidth, "~")
	}

	isActive := foundPane.Active && isWindowActive
	segStyle := baseStyle.Copy()
	btnStyle2 := baseStyle.Copy()
	if headerFg != "" {
		segStyle = segStyle.Foreground(lipgloss.Color(headerFg))
		btnStyle2 = btnStyle2.Foreground(lipgloss.Color(headerFg))
	}
	if isActive {
		segStyle = segStyle.Bold(true)
	}

	var regions []daemon.ClickableRegion

	labelWidth := uniseg.StringWidth(labelText)
	renderedLabel := segStyle.Render(labelText)
	currentCol := groupAccentWidth + 1 + labelWidth
	btnAreaStart := width - buttonsWidth
	if btnAreaStart < currentCol {
		btnAreaStart = currentCol
	}
	spacerWidth := btnAreaStart - currentCol
	fullLineStyle := baseStyle.Copy().Width(width)

	line := groupAccent + " " +
		renderedLabel +
		strings.Repeat(" ", spacerWidth) +
		btnStyle2.Render(buttonsStr)

	if headerBg != "" {
		// Gradient the header top-border strip (lighter -> base) to match the live
		// sidebar tab/header surface.
		line = c.applyGradientFill(line, gradientEndColor(headerBg), headerBg, width)
	} else {
		line = fullLineStyle.Render(line)
	}

	cursor := btnAreaStart + 2
	if showMenuButton {
		if compactMode {
			if contentPaneCount > 1 && collapseBtn != "" {
				collapseEnd := cursor + uniseg.StringWidth(collapseBtn) + 1
				regions = append(regions, daemon.ClickableRegion{
					StartLine: 0, EndLine: 0,
					StartCol: cursor, EndCol: collapseEnd,
					Action: "toggle_pane_collapse", Target: paneID,
				})
				cursor = collapseEnd
			}
			splitVEnd := cursor + uniseg.StringWidth(compactSplitVBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: splitVEnd,
				Action: "header_split_h", Target: paneID,
			})
			splitHEnd := splitVEnd + uniseg.StringWidth(compactSplitHBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitVEnd, EndCol: splitHEnd,
				Action: "header_split_v", Target: paneID,
			})
			closeEnd := splitHEnd + uniseg.StringWidth(compactCloseBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitHEnd, EndCol: closeEnd,
				Action: "header_close", Target: paneID,
			})
			cursor = closeEnd + 3
		} else {
			menuEnd := cursor + uniseg.StringWidth(menuBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: menuEnd,
				Action: "pane_menu", Target: paneID,
			})
			cursor = menuEnd + 3
		}
	}
	if showInlineCollapse {
		collapseEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: collapseEnd,
			Action: "toggle_pane_collapse", Target: paneID,
		})
		cursor = collapseEnd + 1
	}
	if showInlineSplits {
		splitVEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitVEnd,
			Action: "header_split_h", Target: paneID,
		})
		cursor = splitVEnd
		splitHEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitHEnd,
			Action: "header_split_v", Target: paneID,
		})
		cursor = splitHEnd + 1
	}
	if showVerticalResize || showHorizontalResize {
		if showVerticalResize {
			vGrowEnd := cursor + 2
			vShrinkEnd := vGrowEnd + 2
			vDownAction := "pane_grow_v"
			vUpAction := "pane_shrink_v"
			if !hasBorderBelow {
				vDownAction = "pane_shrink_v"
				vUpAction = "pane_grow_v"
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: vGrowEnd,
				Action: vDownAction, Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: vGrowEnd, EndCol: vShrinkEnd,
				Action: vUpAction, Target: paneID,
			})
			cursor = vShrinkEnd
		}
		if showHorizontalResize {
			hGrowEnd := cursor + 2
			hShrinkEnd := hGrowEnd + 2
			hRightAction := "pane_grow_h"
			hLeftAction := "pane_shrink_h"
			if !hasBorderRight {
				hRightAction = "pane_shrink_h"
				hLeftAction = "pane_grow_h"
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: hGrowEnd,
				Action: hRightAction, Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: hGrowEnd, EndCol: hShrinkEnd,
				Action: hLeftAction, Target: paneID,
			})
			cursor = hShrinkEnd
		}
		cursor += 1
	}
	if showInlineClose {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: width,
			Action: "header_close", Target: paneID,
		})
	}
	if !compactMode && !isPhone {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			Action: "header_context", Target: paneID,
		})
	}

	// Pane-headers always render as a single 1-row title strip.
	// Phone buttons live on window-header; no multi-row layout here.
	headerRows := c.desiredPaneHeaderHeight()
	activeClient := c.ActiveClientSnapshot()
	if isPhone {
		activeClient.Profile = "phone"
	} else {
		activeClient.Profile = "desktop"
	}

	sidebarBg := ""
	terminalBg := ""
	if c.theme != nil {
		sidebarBg = c.theme.SidebarBg
		terminalBg = c.theme.TerminalBg
	}

	if c.config.PaneHeader.CustomBorder {
		return &daemon.RenderPayload{
			Content:      line,
			Width:        width,
			Height:       headerRows,
			TotalLines:   1,
			Regions:      regions,
			ActiveClient: activeClient,
		}
	}
	return &daemon.RenderPayload{
		Content:      line,
		Width:        width,
		Height:       headerRows,
		TotalLines:   1,
		Regions:      regions,
		SidebarBg:    sidebarBg,
		TerminalBg:   terminalBg,
		ActiveClient: activeClient,
	}
}

// hashContent returns a simple hash of content for comparison
func hashContent(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// syncAllSidebarWidths resizes all sidebar panes to match the given width.
// Used for button clicks (grow/shrink) where we want to resize ALL sidebars.
func syncAllSidebarWidths(newWidth int) {
	syncSidebarWidthsExcept(newWidth, "")
}

// ResizeAllWindowsNow immediately resizes every sidebar pane to its
// correct bounded width. Called on client-resized so all windows settle
// at once rather than lazily as the user visits each one.
func (c *Coordinator) ResizeAllWindowsNow() {
	if c.sidebarHidden {
		return
	}

	targetWidth := c.globalWidth
	if targetWidth < 10 {
		targetWidth = 25
	}

	out, err := tmuxOutputCtx("list-panes", "-a", "-F",
		"#{pane_id}|#{window_id}|#{?@tabby_sync_width,#{@tabby_sync_width},1}|#{pane_current_command}|#{pane_start_command}")
	if err != nil {
		return
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 || !isSidebarPaneCommand(parts[3], parts[4]) {
			continue
		}
		paneID := parts[0]
		windowID := parts[1]
		syncSetting := parts[2]
		if syncSetting != "1" && syncSetting != "true" {
			continue
		}

		w := c.boundedSidebarWidthForWindow(windowID, targetWidth, 0)
		if w < 1 {
			w = 1
		}
		coordinatorDebugLog.Printf("ResizeAllWindowsNow: pane=%s window=%s width=%d", paneID, windowID, w)
		tmuxRun("resize-pane", "-t", paneID, "-x", fmt.Sprintf("%d", w))
	}
}

// syncOtherSidebarWidths resizes all sidebar panes EXCEPT the one in skipWindowID.
// Used when user drags a sidebar border - we sync others but don't interrupt the drag.
func syncOtherSidebarWidths(newWidth int, skipWindowID string) {
	syncSidebarWidthsExcept(newWidth, skipWindowID)
}

// syncSidebarWidthsExcept resizes sidebar panes to match the given width.
// If skipWindowID is non-empty, skips the sidebar in that window.
// Respects @tabby_sync_width window option (default true).
func syncSidebarWidthsExcept(newWidth int, skipWindowID string) {
	out, err := tmuxOutputCtx("list-panes", "-a", "-F", "#{pane_id}|#{window_id}|#{?@tabby_sync_width,#{@tabby_sync_width},1}|#{pane_current_command}|#{pane_start_command}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) >= 5 && isSidebarPaneCommand(parts[3], parts[4]) {
			paneID := parts[0]
			windowID := parts[1]
			syncSetting := parts[2]

			if skipWindowID != "" && windowID == skipWindowID {
				continue
			}

			if syncSetting == "1" || syncSetting == "true" {
				tmuxRun("resize-pane", "-t", paneID, "-x", fmt.Sprintf("%d", newWidth))
			}
		}
	}
}

// getBusyFrames returns the busy indicator animation frames
func (c *Coordinator) getBusyFrames() []string {
	if len(c.config.Indicators.Busy.Frames) > 0 {
		return c.config.Indicators.Busy.Frames
	}
	return []string{"◐", "◓", "◑", "◒"}
}

// getSlowSpinnerFrame returns a slowed-down spinner frame index
// This makes each frame visible for 200ms instead of 100ms (fixes #5: animation skips frames)
func (c *Coordinator) getSlowSpinnerFrame() int {
	return c.spinnerFrame / 2
}

func (c *Coordinator) getAnimatedActiveIndicator(fallback string) string {
	frames := c.config.Sidebar.Colors.ActiveIndicatorFrames
	if len(frames) == 0 {
		return fallback
	}
	frame := frames[c.getSlowSpinnerFrame()%len(frames)]
	if frame == "" {
		return " "
	}
	return frame
}

func (c *Coordinator) HasActiveIndicatorAnimation() bool {
	frames := c.config.Sidebar.Colors.ActiveIndicatorFrames
	if len(frames) < 2 {
		return false
	}
	for i := 1; i < len(frames); i++ {
		if frames[i] != frames[0] {
			return true
		}
	}
	return false
}

// getIndicatorIcon returns the icon for an indicator
func (c *Coordinator) getIndicatorIcon(ind config.Indicator) string {
	if ind.Icon != "" {
		return ind.Icon
	}
	return "●"
}

// headerBoolDefault returns the value of a *bool config field, defaulting to true if nil.
func headerBoolDefault(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// getActiveWindowGroupTheme returns the theme of the active window's group.
// Returns nil if no active window or group is found.
func (c *Coordinator) getActiveWindowGroupTheme() *config.Theme {
	// Find the active window
	var activeWin *tmux.Window
	for i := range c.windows {
		if c.windows[i].Active {
			activeWin = &c.windows[i]
			break
		}
	}
	if activeWin == nil {
		return nil
	}

	// Find which group contains this window
	for i, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == activeWin.ID {
				return &c.grouped[i].Theme
			}
		}
	}
	return nil
}

// getWindowTabColors returns the tab fg/bg colors for a window using the same
// logic as the sidebar window list. isActive controls whether active or inactive
// variants are used for group/theme colors.
func (c *Coordinator) getWindowTabColors(windowID string, isActive bool) (string, string, bool) {
	var targetWin *tmux.Window
	for i := range c.windows {
		if c.windows[i].ID == windowID {
			targetWin = &c.windows[i]
			break
		}
	}
	if targetWin == nil {
		return "", "", false
	}

	var theme config.Theme
	var customColor string
	var foundGroup bool
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == targetWin.ID {
				theme = group.Theme
				customColor = win.CustomColor
				foundGroup = true
				break
			}
		}
		if foundGroup {
			break
		}
	}
	if !foundGroup {
		return "", "", false
	}

	isDarkBg := c.bgDetector.IsDarkBackground()
	if c.theme != nil {
		isDarkBg = c.theme.Dark
	}
	theme = grouping.ResolveThemeColors(theme, isDarkBg)

	var bgColor, fgColor string
	isTransparent := customColor == "transparent"

	if isTransparent {
		bgColor = ""
		if isActive {
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			fgColor = theme.Fg
		}
	} else if customColor != "" {
		if isActive {
			bgColor = customColor
		} else {
			bgColor = grouping.ShadeColorByIndex(customColor, 1)
		}
		fgColor = contrastFg(bgColor, isActive)
	} else if isActive {
		bgColor = theme.ActiveBg
		if bgColor == "" {
			bgColor = theme.Bg
		}
		fgColor = theme.ActiveFg
		if fgColor == "" {
			fgColor = theme.Fg
		}
	} else {
		bgColor = theme.Bg
		fgColor = theme.Fg
	}

	return fgColor, bgColor, true
}

// generateSidebarHeader renders the pinned header bar at the top of the sidebar.
// Returns the header content string (with trailing newline) and click regions.
// Left-click collapses sidebar. Right-click opens settings context menu.
func (c *Coordinator) generateSidebarHeader(width int, clientID string) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion

	hdr := c.config.Sidebar.Header
	headerText := hdr.Text
	headerHeight := hdr.Height
	paddingBottom := hdr.PaddingBottom
	centered := headerBoolDefault(hdr.Centered)
	activeColor := headerBoolDefault(hdr.ActiveColor)
	bold := headerBoolDefault(hdr.Bold)

	// Mirror the active tab's marker into the header: if the current window has a
	// marker (@tabby_icon), flank the header text with it on BOTH sides so "TABBY"
	// reads e.g. "🚀 TABBY 🚀". Re-rendered per active window, so it switches on
	// window switch. Read lock-free like the active-color block below (the render
	// path already holds the state RLock; taking it again would deadlock).
	for i := range c.windows {
		if c.windows[i].Active {
			groupIcon := ""
			if gt := c.getActiveWindowGroupTheme(); gt != nil {
				groupIcon = gt.Icon
			}
			if mk := effectiveWindowMarker(c.windows[i].Icon, groupIcon); mk != "" {
				if headerText == "" {
					headerText = mk + " " + mk
				} else {
					headerText = mk + " " + headerText + " " + mk
				}
			}
			break
		}
	}

	// Resolve colors from this window's tab colors
	fgColor := hdr.Fg
	bgColor := hdr.Bg
	if strings.EqualFold(fgColor, "auto") {
		fgColor = ""
	}
	if strings.EqualFold(bgColor, "auto") {
		bgColor = ""
	}
	if activeColor && (fgColor == "" || bgColor == "") {
		activeWindowID := ""
		for i := range c.windows {
			if c.windows[i].Active {
				activeWindowID = c.windows[i].ID
				break
			}
		}
		if activeWindowID != "" {
			if tabFg, tabBg, ok := c.getWindowTabColors(activeWindowID, true); ok {
				if fgColor == "" {
					fgColor = tabFg
				}
				if bgColor == "" {
					bgColor = tabBg
				}
			}
		}
	}
	if fgColor == "" {
		fgColor = c.getHeaderTextColorWithFallback("")
	}

	// Build style
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(fgColor)).
		Bold(bold)

	if bgColor != "" {
		headerStyle = headerStyle.Background(lipgloss.Color(bgColor))
	}

	// Truncate text if needed (leave space for collapse button overlay)
	maxNameWidth := width - 5 // 1 padding + 3 button + 1 gap
	if maxNameWidth < 3 {
		maxNameWidth = 3
	}

	nameWidth := uniseg.StringWidth(headerText)
	if nameWidth > maxNameWidth {
		truncated := ""
		w := 0
		for _, r := range headerText {
			rw := runewidth.RuneWidth(r)
			if w+rw > maxNameWidth-1 {
				break
			}
			truncated += string(r)
			w += rw
		}
		headerText = truncated + "~"
		nameWidth = uniseg.StringWidth(headerText)
	}

	// Row style applies bg color across the full width
	rowStyle := lipgloss.NewStyle()
	if bgColor != "" {
		rowStyle = rowStyle.Background(lipgloss.Color(bgColor))
	}

	// Determine which row gets the text (vertical centering)
	textRow := 0
	if centered && headerHeight > 1 {
		textRow = headerHeight / 2
	}

	// Header text style WITHOUT a background: on the gradient path the per-cell
	// gradient supplies the bg, so a style bg would paint over it.
	headerTextStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor)).Bold(bold)
	_ = rowStyle // retained for the transparent (no-bg) path below

	// Render header rows. When the header has a bg colour, fill each row with the
	// same lighter -> base gradient the live tab rows use so the TABBY header reads
	// as part of the same surface; otherwise keep the flat/transparent layout.
	for line := 0; line < headerHeight; line++ {
		// Build the row's text portion (may carry fg ANSI); the fill pads the rest.
		rowContent := ""
		if line == textRow {
			if centered {
				leftPad := (width - nameWidth) / 2
				if leftPad < 0 {
					leftPad = 0
				}
				rowContent = strings.Repeat(" ", leftPad) + headerTextStyle.Render(headerText)
			} else {
				rowContent = " " + headerTextStyle.Render(headerText)
			}
		}
		if bgColor != "" {
			s.WriteString(c.applyGradientFill(rowContent, gradientEndColor(bgColor), bgColor, width) + "\n")
		} else {
			plainW := uniseg.StringWidth(stripAnsi(rowContent))
			if plainW < width {
				rowContent += strings.Repeat(" ", width-plainW)
			}
			s.WriteString(rowContent + "\n")
		}
	}

	// Transparent padding rows below header (no bg color)
	for i := 0; i < paddingBottom; i++ {
		s.WriteString(strings.Repeat(" ", width) + "\n")
	}

	// Clickable region covers the header rows (not padding)
	regions = append(regions, daemon.ClickableRegion{
		StartLine: 0, EndLine: headerHeight - 1,
		Action: "sidebar_header_area", Target: "",
	})

	return s.String(), regions
}

// dashboardRenderGroups returns the grouped window list the sidebar should
// display. While the dashboard is active the real origin windows are gone
// (gathered into the dashboard window), so we render synthetic groups built
// from the remembered snapshot — this keeps the sidebar list stable instead of
// collapsing to a single "Dashboard" entry. Otherwise it returns c.grouped.
func (c *Coordinator) dashboardRenderGroups() []grouping.GroupedWindows {
	if c.dashboardWindowID == "" {
		return c.grouped
	}
	synth := make([]tmux.Window, 0, len(c.dashboardOrder))
	for _, id := range c.dashboardOrder {
		snap := c.dashboardOrigins[id]
		synth = append(synth, tmux.Window{ID: id, Name: snap.Name, Group: snap.Group, Index: snap.Index})
	}
	return grouping.GroupWindowsWithOptions(synth, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
}

// sidebarRenderGroups returns the group list for the sidebar tab area with every
// minimized window pulled out of its group into a single synthetic "Minimized"
// group rendered at the very bottom — visually separating de-prioritised tabs
// from the active ones (they read as a distinct, muted section below the real
// groups). Display-only: c.grouped (which drives borders, next/prev cycling, the
// dashboard, etc.) is untouched, and when nothing is minimized the result is
// identical to dashboardRenderGroups.
func (c *Coordinator) sidebarRenderGroups() []grouping.GroupedWindows {
	base := c.dashboardRenderGroups()
	var minimized []tmux.Window
	out := make([]grouping.GroupedWindows, 0, len(base)+1)
	for _, g := range base {
		kept := make([]tmux.Window, 0, len(g.Windows))
		for _, w := range g.Windows {
			if w.Minimized {
				minimized = append(minimized, w)
			} else {
				kept = append(kept, w)
			}
		}
		// Drop a group that became empty ONLY because all its windows were
		// minimized (they now live in the Minimized section). Genuinely-empty
		// groups the base list chose to show (ShowEmptyGroups) pass through so
		// that behaviour is unchanged. kept is a fresh slice, so c.grouped's
		// backing arrays are never mutated.
		if len(kept) == 0 && len(g.Windows) > 0 {
			continue
		}
		g.Windows = kept
		out = append(out, g)
	}
	if len(minimized) > 0 {
		// Order by STABLE window ID, not the holding-session window_index — a
		// parked/re-parked window's index jumps every move-window, so an Index sort
		// reshuffled the whole Minimized section on any window switch (settlePeek
		// re-parks even when switching to a normal window). Window IDs never change.
		sort.SliceStable(minimized, func(i, j int) bool {
			return grouping.WindowIDNum(minimized[i].ID) < grouping.WindowIDNum(minimized[j].ID)
		})
		const minimizedBg = "#6e6a86"
		// Give each minimized tab a DESATURATED version of the colour it would have
		// unminimized (its custom colour, else its original group's bg), blended
		// toward the muted section bg — so a minimized tab keeps a hint of its
		// identity (a muted rose for StudioDome, etc.) instead of a uniform grey.
		groupBg := make(map[string]string, len(base))
		for _, g := range base {
			if g.Theme.Bg != "" {
				groupBg[g.Name] = g.Theme.Bg
			}
		}
		for i := range minimized {
			real := strings.TrimSpace(minimized[i].CustomColor)
			if real == "" || real == "transparent" {
				grp := minimized[i].Group
				if grp == "" {
					grp = "Default" // an ungrouped window shows in Default when unminimized
				}
				real = groupBg[grp]
			}
			if len(real) == 7 && real[0] == '#' {
				// 40% toward the real colour: still clearly "minimized", but tinted.
				minimized[i].CustomColor = blendHexToward(minimizedBg, real, 0.4)
			}
		}
		// Muted header so the section reads as set-aside, not a live coloured group.
		out = append(out, grouping.GroupedWindows{
			Name: "Minimized",
			Theme: config.Theme{
				Bg: minimizedBg, Fg: "#faf4ed",
				ActiveBg: minimizedBg, ActiveFg: "#faf4ed",
			},
			Windows: minimized,
		})
	}
	return out
}

// appendDashboardRow renders the persistent, clickable "0. Dashboard" entry at
// the top of the sidebar (above the first group) and registers its click region
// (action dashboard_toggle). It is highlighted while the dashboard is active.
func (c *Coordinator) appendDashboardRow(s *strings.Builder, regions *[]daemon.ClickableRegion, currentLine *int, width int, inactiveFg, activeIndicator string) {
	active := c.dashboardWindowID != ""
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Bold(true)
	label := " 0. Dashboard"
	if active {
		label = " 0. Dashboard " + activeIndicator
	}
	if bg := c.GetTerminalBg(); bg != "" {
		style = style.Background(lipgloss.Color(bg))
	}
	s.WriteString(style.Render(label) + "\n")
	*regions = append(*regions, daemon.ClickableRegion{
		StartLine: *currentLine,
		EndLine:   *currentLine,
		StartCol:  0,
		EndCol:    width - 1,
		Action:    "dashboard_toggle",
		Target:    "",
	})
	*currentLine++
}

// generateMainContent creates the main scrollable area with window list
// clientID is the window ID that this content is being rendered for
// The returned floatLine is the 0-based line index of the synthetic "Minimized"
// group header, or -1 when there is no such section. RenderForClient inserts its
// bottom-padding gap at this line so the Minimized section floats to the bottom of
// the tab area instead of hugging the last real group.
func (c *Coordinator) generateMainContent(clientID string, width, height int) (string, []daemon.ClickableRegion, int) {
	var s strings.Builder
	floatLine := -1
	var regions []daemon.ClickableRegion

	currentLine := 0

	// Configurable tree characters
	treeBranchChar := c.config.Sidebar.Colors.TreeBranch
	if treeBranchChar == "" {
		treeBranchChar = "├─"
	}
	treeBranchLastChar := c.config.Sidebar.Colors.TreeBranchLast
	if treeBranchLastChar == "" {
		treeBranchLastChar = "└─"
	}
	treeContinueChar := c.config.Sidebar.Colors.TreeContinue
	if treeContinueChar == "" {
		treeContinueChar = "│"
	}
	treeConnectorChar := c.config.Sidebar.Colors.TreeConnector
	if treeConnectorChar == "" {
		treeConnectorChar = "─"
	}

	// Disclosure icons
	expandedIcon := c.config.Sidebar.Colors.DisclosureExpanded
	if expandedIcon == "" {
		expandedIcon = "⊟"
	}
	collapsedIcon := c.config.Sidebar.Colors.DisclosureCollapsed
	if collapsedIcon == "" {
		collapsedIcon = "⊞"
	}

	// Tree color
	treeStyle := lipgloss.NewStyle()
	treeFg := c.getTreeFgWithFallback(c.config.Sidebar.Colors.TreeFg)
	treeStyle = treeStyle.Foreground(lipgloss.Color(treeFg))
	treeBg := c.config.Sidebar.Colors.TreeBg
	if treeBg == "" && c.theme != nil {
		treeBg = c.theme.TreeBg
	}
	if strings.EqualFold(treeBg, "transparent") {
		treeBg = ""
	}
	// Fall back to resolved terminal bg when config + theme both empty,
	// so box-drawing chars (│ ─ ├ └ ┌ etc.) don't emit with "default" bg
	// that can show the previous theme's color for a beat during flips.
	if treeBg == "" {
		treeBg = c.GetTerminalBg()
	}
	if treeBg != "" {
		treeStyle = treeStyle.Background(lipgloss.Color(treeBg))
	}

	inactiveFg := c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)

	// Disclosure color (use config or terminal default)
	disclosureColor := c.getDisclosureFgWithFallback(c.config.Sidebar.Colors.DisclosureFg)

	// Active indicator config
	activeIndicator := c.config.Sidebar.Colors.ActiveIndicator
	if activeIndicator == "" {
		activeIndicator = "◀"
	}
	activeIndFgConfig := c.config.Sidebar.Colors.ActiveIndicatorFg
	activeIndBgConfig := c.config.Sidebar.Colors.ActiveIndicatorBg

	if c.config.Sidebar.PrefixMode {
		pc, pr := c.generatePrefixModeContent(clientID, width, height, treeBranchChar, treeBranchLastChar, treeContinueChar, treeConnectorChar, expandedIcon, collapsedIcon, treeStyle, disclosureColor, activeIndicator, activeIndFgConfig, activeIndBgConfig)
		return pc, pr, -1
	}

	// Persistent "0. Dashboard" entry above the first group.
	c.appendDashboardRow(&s, &regions, &currentLine, width, inactiveFg, activeIndicator)

	// Iterate over grouped windows (synthetic remembered list while gathered),
	// with minimized tabs collected into a muted "Minimized" group at the bottom.
	grouped := c.sidebarRenderGroups()
	numGroups := len(grouped)
	for gi, group := range grouped {
		isLastGroup := gi == numGroups-1
		theme := group.Theme

		// Auto-fill missing theme colors with intelligent defaults
		isDarkBg := c.bgDetector.IsDarkBackground()
		if c.theme != nil {
			isDarkBg = c.theme.Dark
		}
		theme = grouping.ResolveThemeColors(theme, isDarkBg)

		isCollapsed := c.collapsedGroups[group.Name]

		// Group header style
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(inactiveFg)).
			Bold(true)

		// Collapse indicator
		collapseIcon := expandedIcon
		if isCollapsed {
			collapseIcon = collapsedIcon
		}
		collapseStyle := lipgloss.NewStyle()
		if disclosureColor != "" {
			collapseStyle = collapseStyle.Foreground(lipgloss.Color(disclosureColor))
		}

		// Build header
		icon := strings.TrimSpace(theme.Icon)
		headerText := group.Name
		if isCollapsed && len(group.Windows) > 0 {
			headerText += fmt.Sprintf(" (%d)", len(group.Windows))
		}
		// Always add a space between prefix and group name for consistent alignment.
		// Icon (if present) is included INSIDE the bg-filled area so backgrounds
		// align across all groups regardless of icon width.
		renderedIcon := ""
		if icon != "" {
			renderedIcon = icon
		}
		headerText = " " + headerText

		// Track group header line
		groupStartLine := currentLine
		hasWindows := len(group.Windows) > 0

		// Remember where the synthetic Minimized section begins so RenderForClient
		// can float it to the bottom of the tab area (see sidebarRenderGroups).
		if group.Name == "Minimized" {
			floatLine = currentLine
		}

		// Render group header
		{
			bg := theme.Bg
			if strings.EqualFold(bg, "transparent") {
				bg = ""
			}
			if hasWindows {
				prefix := collapseStyle.Render(collapseIcon)
				prefixW := uniseg.StringWidth(stripAnsi(prefix))
				menuBtnW := 2 // " ⋮"
				restW := width - prefixW - menuBtnW
				if restW < 1 {
					restW = 1
				}

				// Icon is included INSIDE the bg-filled area so backgrounds
				// align consistently across groups regardless of icon width.
				iconAndText := renderedIcon + headerStyle.Render(headerText)
				iconAndTextW := uniseg.StringWidth(stripAnsi(iconAndText))
				if iconAndTextW > restW {
					renderedIconW := uniseg.StringWidth(stripAnsi(renderedIcon))
					headerMaxW := restW - renderedIconW
					if headerMaxW < 1 {
						headerMaxW = 1
					}
					truncated := ""
					for _, r := range headerText {
						if lipgloss.Width(truncated+string(r)) > headerMaxW-1 {
							break
						}
						truncated += string(r)
					}
					headerText = truncated + "~"
					iconAndText = renderedIcon + headerStyle.Render(headerText)
				}
				if bg != "" {
					iconAndText = c.applyGradientFill(iconAndText, gradientEndColor(bg), bg, restW)
				} else {
					iconAndText = lipgloss.NewStyle().Width(restW).Render(iconAndText)
				}

				// Render hamburger menu button with the gradient's DARK tail colour so
				// the row's right edge continues the shadow instead of popping back to
				// the base colour.
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bg != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, gradientTailColor(bg), menuBtnW)
				}
				s.WriteString(prefix + iconAndText + menuBtn + "\n")
			} else {
				// No windows - show header with group tree branch but no collapse icon
				prefix := " "
				prefixW := uniseg.StringWidth(prefix)
				menuBtnW := 2 // " ⋮"
				restW := width - prefixW - menuBtnW
				if restW < 1 {
					restW = 1
				}
				iconAndText := renderedIcon + headerStyle.Render(headerText)
				iconAndTextW := uniseg.StringWidth(stripAnsi(iconAndText))
				if iconAndTextW > restW {
					renderedIconW := uniseg.StringWidth(stripAnsi(renderedIcon))
					headerMaxW := restW - renderedIconW
					if headerMaxW < 1 {
						headerMaxW = 1
					}
					truncated := ""
					for _, r := range headerText {
						if lipgloss.Width(truncated+string(r)) > headerMaxW-1 {
							break
						}
						truncated += string(r)
					}
					headerText = truncated + "~"
					iconAndText = renderedIcon + headerStyle.Render(headerText)
				}
				if bg != "" {
					iconAndText = c.applyGradientFill(iconAndText, gradientEndColor(bg), bg, restW)
				}

				// Render hamburger menu button with the gradient's DARK tail colour so
				// the row's right edge continues the shadow instead of popping back to
				// the base colour.
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bg != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, gradientTailColor(bg), menuBtnW)
				}
				s.WriteString(prefix + iconAndText + menuBtn + "\n")
			}
			currentLine++
		}

		if hasWindows {
			iconWidth := uniseg.StringWidth(stripAnsi(collapseStyle.Render(collapseIcon)))
			if iconWidth < 1 {
				iconWidth = 1
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    iconWidth,
				Action:    "toggle_group",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  iconWidth,
				EndCol:    width - 2,
				Action:    "group_header",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  width - 2,
				EndCol:    0,
				Action:    "group_menu",
				Target:    group.Name,
			})
		} else {
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    width - 2,
				Action:    "group_header",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  width - 2,
				EndCol:    0,
				Action:    "group_menu",
				Target:    group.Name,
			})
		}

		if isCollapsed {
			continue
		}

		// Show windows
		numWindows := len(group.Windows)
		for wi, win := range group.Windows {
			// For daemon mode: window is active if its ID matches this renderer's clientID
			// clientID is the window ID that the renderer is displaying for
			isActive := (win.ID == clientID)
			isLastInGroup := wi == numWindows-1
			windowStartLine := currentLine

			// Choose colors - custom color overrides group theme
			var bgColor, fgColor string
			isTransparent := win.CustomColor == "transparent"

			if isTransparent {
				bgColor = ""
				if isActive {
					fgColor = theme.ActiveFg
					if fgColor == "" {
						fgColor = theme.Fg
					}
				} else {
					fgColor = inactiveFg
				}
			} else if win.CustomColor != "" {
				if isActive {
					bgColor = win.CustomColor
				} else {
					bgColor = grouping.ShadeColorByIndex(win.CustomColor, 1)
				}
				fgColor = contrastFg(bgColor, isActive)
			} else if isActive {
				bgColor = theme.ActiveBg
				if bgColor == "" {
					bgColor = theme.Bg
				}
				fgColor = theme.ActiveFg
				if fgColor == "" {
					fgColor = theme.Fg
				}
				if fgColor == "" {
					fgColor = contrastFg(bgColor, true) // contrast-aware when the group sets no fg
				}
			} else {
				bgColor = theme.Bg
				fgColor = theme.Fg
				if fgColor == "" {
					// Contrast-aware, and 10% lighter than active (see contrastFg) so
					// inactive tabs read mostly the same as active, just softer.
					fgColor = contrastFg(bgColor, false)
				}
			}
			// Minimized windows read as dimmed — but NOT when they're the active
			// (selected) window, so a selected minimized tab still shows its active
			// state like any other tab. Blend the fg ~50% toward the row bg so the
			// dim is visible even on terminals that collapse Bold+Faint into Bold
			// (macOS Terminal and others). Falls back to plain inactiveFg.
			if win.Minimized && !isActive {
				blendBg := bgColor
				if blendBg == "" {
					blendBg = theme.Bg
				}
				dimmed := blendHexToward(fgColor, blendBg, 0.30)
				if dimmed == fgColor {
					dimmed = inactiveFg
				}
				fgColor = dimmed
			}
			// Build style
			style := lipgloss.NewStyle()
			if fgColor != "" {
				style = style.Foreground(lipgloss.Color(fgColor))
			}

			if isActive {
				style = style.Bold(true)
			}
			if win.Minimized && !isActive {
				style = style.Faint(true)
			}

			// Build alert indicator
			alertIcon := ""
			ind := c.config.Indicators

			if ind.Busy.Enabled && win.Busy {
				alertStyle := indicatorStyle(ind.Busy.Color, win.Minimized, bgColor, theme.Bg)

				busyFrames := c.getBusyFrames()
				alertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
			} else if ind.Input.Enabled && win.Input {
				inputIcon := ind.Input.Icon
				if inputIcon == "" {
					inputIcon = "?"
				}
				alertStyle := indicatorStyle(ind.Input.Color, win.Minimized, bgColor, theme.Bg)

				if len(ind.Input.Frames) > 0 {
					alertIcon = alertStyle.Render(ind.Input.Frames[c.getSlowSpinnerFrame()%len(ind.Input.Frames)])
				} else {
					alertIcon = alertStyle.Render(inputIcon)
				}
			} else if !isActive {
				if ind.Bell.Enabled && win.Bell {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Bell))
				} else if ind.Activity.Enabled && win.Activity {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Activity))
				} else if ind.Silence.Enabled && win.Silence {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Silence))
				}
			}

			var indicatorPart string
			if alertIcon != "" {
				indicatorPart = alertIcon
			} else {
				indicatorPart = " "
			}

			// Window tree branch
			var treeBranch string
			if isLastInGroup {
				treeBranch = treeBranchLastChar
			} else {
				treeBranch = treeBranchChar
			}

			var contentPanes []tmux.Pane
			for _, pane := range win.Panes {
				if isAuxiliaryPane(pane) {
					continue
				}
				contentPanes = append(contentPanes, pane)
			}
			// Window collapse indicator if has panes
			hasPanes := len(contentPanes) > 1
			isWindowCollapsed := win.Collapsed
			var windowCollapseIcon string

			if hasPanes {
				if isWindowCollapsed {
					windowCollapseIcon = collapsedIcon
				} else {
					windowCollapseIcon = expandedIcon
				}
			}

			// Build tab content - use visual position for display (stable sequential
			// numbering that matches sidebar order regardless of tmux renumbering)
			// Display is 1-indexed by default (configurable via @tabby_base_index)
			displayName := c.composeTabBaseName(win)
			// SSH/mosh label. Legacy mode puts the host on the tab line and the
			// local dir(s) on a continuation row; icon mode collapses to one line
			// (glyph + dir). See remoteTabDisplay.
			var remoteContinuation string
			displayName, remoteContinuation = c.remoteTabDisplay(win, displayName)
			// ssh glyph sits ahead of the group marker: "<glyph> <marker> <name>".
			displayName = composeTabMarker(c.sshTabGlyph(win), effectiveWindowMarker(win.Icon, group.Theme.Icon)) + displayName
			visualNum := c.windowVisualPos[win.ID]
			baseContent := fmt.Sprintf("%d. %s", visualNum, displayName)

			// Add pane count if collapsed
			if hasPanes && isWindowCollapsed {
				baseContent = fmt.Sprintf("%s (%d)", baseContent, len(contentPanes))
			}

			// Calculate widths
			// All windows: indicator(1) + branch first char(1) + [collapse icon or branch second char](1) = 3
			prefixWidth := 3
			menuBtnW := 2 // " ⋮"
			windowContentWidth := width - prefixWidth - menuBtnW

			// Word-wrap the inline label across up to MaxLines rows. line 1 = the
			// tab line; the rest become continuation rows. A remote (SSH) window
			// keeps its single host/dir continuation instead of wrapping.
			contRowWidth := width - 4 // " │ " leading (3) + 1-space chip pad
			if contRowWidth < 1 {
				contRowWidth = 1
			}
			maxLines := c.config.AI.TabSummary.MaxLines
			if maxLines < 1 || remoteContinuation != "" {
				maxLines = 1
			}
			wrapped := wrapTabLabel(baseContent, windowContentWidth, contRowWidth, maxLines)
			contentText := wrapped[0]
			contRows := wrapped[1:]
			if remoteContinuation != "" {
				contRows = []string{remoteContinuation}
			}

			// Styles for window collapse icon
			windowCollapseStyle := lipgloss.NewStyle()
			if disclosureColor != "" {
				windowCollapseStyle = windowCollapseStyle.Foreground(lipgloss.Color(disclosureColor))
			}

			contentStyle := style.Copy()
			if bgColor != "" {
				contentStyle = contentStyle.Background(lipgloss.Color(bgColor))
			}

			// Render tab line
			{
				// Build prefix (indicator + tree branch) separately from content
				// so background color only applies to the content portion
				var prefix, content string
				if hasPanes {
					treeBranchRunes := []rune(treeBranch)
					treeBranchFirst := string(treeBranchRunes[0])
					prefix = indicatorPart + treeStyle.Render(treeBranchFirst) + windowCollapseStyle.Render(windowCollapseIcon)
					content = contentText
				} else if isActive {
					treeBranchRunes := []rune(treeBranch)
					treeBranchFirst := string(treeBranchRunes[0])

					var indicatorBg, indicatorFg string
					if activeIndBgConfig == "" || activeIndBgConfig == "auto" {
						if theme.ActiveIndicatorBg != "" {
							indicatorBg = theme.ActiveIndicatorBg
						} else {
							indicatorBg = theme.Bg
						}
					} else {
						indicatorBg = activeIndBgConfig
					}
					if activeIndFgConfig == "" || activeIndFgConfig == "auto" {
						if indicatorBg == "" || strings.EqualFold(indicatorBg, "transparent") {
							indicatorFg = fgColor
						} else {
							indicatorFg = indicatorBg
						}
					} else {
						indicatorFg = activeIndFgConfig
					}

					activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Bold(true)
					if indicatorBg != "" && !strings.EqualFold(indicatorBg, "transparent") {
						activeIndStyle = activeIndStyle.Background(lipgloss.Color(indicatorBg))
					}
					prefix = indicatorPart + treeStyle.Render(treeBranchFirst) + activeIndStyle.Render(c.getAnimatedActiveIndicator(activeIndicator))
					content = contentText
				} else {
					prefix = indicatorPart + treeStyle.Render(treeBranch)
					content = contentText
				}

				// Apply bg color from start of name to the right edge (minus menu button)
				prefixPlain := stripAnsi(prefix)
				prefixWidth := uniseg.StringWidth(prefixPlain)
				contentWidth := width - prefixWidth - menuBtnW
				if contentWidth < 0 {
					contentWidth = 0
				}

				contentRendered := style.Render(content)
				// rowEndBg is the gradient's right-edge colour, reused to fill the
				// menu button so it continues the gradient without a seam.
				rowEndBg := ""
				if bgColor != "" {
					// All tabs gradient lighter -> base — minimized rows use the same
					// direction as live ones (the reverse-for-minimized scheme was
					// dropped); the Minimized section's muted colour still sets it apart.
					fromBg, toBg := gradientEndColor(bgColor), bgColor
					rowEndBg = gradientTailColor(bgColor) // dark tail, so the menu button continues the shadow
					contentRendered = c.applyGradientFill(contentRendered, fromBg, toBg, contentWidth)
				}

				// Render hamburger menu button with matching background
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bgColor != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, rowEndBg, menuBtnW)
				}
				s.WriteString(prefix + contentRendered + menuBtn + "\n")
				currentLine++
			}

			// SSH/mosh continuation row: "│ name" indented one past the prefix.
			// Same bgColor as the tab line so the two rows read as one tab.
			// windowStartLine was captured before the tab render, so the click
			// regions below cover both rows.
			for _, row := range contRows {
				if row == "" {
					continue
				}
				c.writeRemoteNameRow(&s, row, width, bgColor, fgColor, treeStyle, treeContinueChar, win.Minimized, isLastInGroup)
				currentLine++
			}

			// Record window region(s) for click handling
			// For windows with panes, split into three click regions:
			// 1. Left area (indicator + tree branch + collapse icon) -> toggle_panes
			// 2. Middle area (window name) -> select_window
			// 3. Right area (menu button) -> window_menu
			if hasPanes {
				collapseColEnd := 5 // covers indicator(1) + tree(2) + icon(1) + space(1)
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  0,
					EndCol:    collapseColEnd,
					Action:    "toggle_panes",
					Target:    strconv.Itoa(win.Index),
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  collapseColEnd,
					EndCol:    width - 2,
					Action:    "select_window",
					Target:    win.ID,
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  width - 2,
					EndCol:    0,
					Action:    "window_menu",
					Target:    win.ID,
				})
			} else {
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  0,
					EndCol:    width - 2,
					Action:    "select_window",
					Target:    win.ID,
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  width - 2,
					EndCol:    0,
					Action:    "window_menu",
					Target:    win.ID,
				})
			}

			// Show panes if window has multiple panes and is not collapsed
			if len(contentPanes) > 1 && !isWindowCollapsed {
				// Use inactiveFg for sidebar pane text (same as inactive windows)
				paneStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(inactiveFg))

				activePaneStyle := paneStyle
				if isActive {
					activePaneFg := c.getTextColorWithFallback("")
					if win.CustomColor == "" && theme.ActiveFg != "" {
						activePaneFg = theme.ActiveFg
					} else if win.CustomColor != "" {
						// Only use white for custom colors (they have dark backgrounds)
						activePaneFg = "#ffffff"
					}
					activePaneStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(activePaneFg)).
						Bold(true)
				}

				var treeContinue string
				if isLastInGroup {
					treeContinue = " "
				} else {
					treeContinue = treeStyle.Render(treeContinueChar)
				}

				numPanes := len(contentPanes)
				// Suppress non-AI busy icons when an AI pane is actively working
				anyAIBusyA := false
				for _, p := range contentPanes {
					if p.AIBusy {
						anyAIBusyA = true
						break
					}
				}
				for pi, pane := range contentPanes {
					isLastPane := pi == numPanes-1
					paneStartLine := currentLine

					var paneBranchChar string
					if isLastPane {
						for _, r := range treeBranchLastChar {
							paneBranchChar = string(r)
							break
						}
					} else {
						for _, r := range treeBranchChar {
							paneBranchChar = string(r)
							break
						}
					}

					paneNum := fmt.Sprintf("%d.%d", visualNum, pane.Index)
					paneLabel := pane.Command
					if pane.LockedTitle != "" {
						paneLabel = pane.LockedTitle
					} else if pane.Title != "" && pane.Title != pane.Command {
						paneLabel = pane.Title
					}
					// SSH/mosh: avoid duplicating the host (already on row 1).
					// Most remote shells set pane.Title to "user@host" or the
					// hostname — fall back to the command when that happens.
					if win.RemoteHost != "" && paneLabel != pane.Command && titleLooksLikeShellPrompt(paneLabel, win.RemoteHost) {
						paneLabel = pane.Command
					}
					paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

					paneIndentWidth := 5
					paneMenuW := 2
					paneContentWidth := width - paneIndentWidth - paneMenuW

					// Truncate using proper rune width (handles Unicode/emoji)
					if lipgloss.Width(paneText) > paneContentWidth {
						truncated := ""
						for _, r := range paneText {
							if lipgloss.Width(truncated+string(r)) > paneContentWidth-1 {
								break
							}
							truncated += string(r)
						}
						paneText = truncated + "~"
					}

					paneActiveIndicator := c.config.Sidebar.Colors.ActiveIndicator
					if paneActiveIndicator == "" {
						paneActiveIndicator = "█"
					}

					// Per-pane alert indicator (busy/input for multi-pane windows)
					paneAlertIcon := ""
					pInd := c.config.Indicators
					if pane.AIBusy && pInd.Busy.Enabled {
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
						busyFrames := c.getBusyFrames()
						paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
					} else if pane.AIInput && pInd.Input.Enabled {
						inputIcon := pInd.Input.Icon
						if inputIcon == "" {
							inputIcon = "?"
						}
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Input.Color))
						if len(pInd.Input.Frames) > 0 {
							paneAlertIcon = alertStyle.Render(pInd.Input.Frames[c.getSlowSpinnerFrame()%len(pInd.Input.Frames)])
						} else {
							paneAlertIcon = alertStyle.Render(inputIcon)
						}
					} else if pane.Busy && pInd.Busy.Enabled && !tmux.IsAITool(pane.Command) && !anyAIBusyA {
						// Non-AI pane with foreground process; suppress when AI is busy in same window
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
						busyFrames := c.getBusyFrames()
						paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
					}

					paneLeadChar := " "
					if paneAlertIcon != "" {
						paneLeadChar = paneAlertIcon
					}

					var paneLineBg string
					if bgColor != "" {
						paneLineBg = bgColor
					} else {
						paneLineBg = theme.Bg
					}

					// Build prefix (tree parts) and content separately
					// bg color extends from start of pane name to the right edge
					var panePrefix, paneContent string
					if pane.Active && isActive {
						var paneIndicatorBg, paneIndicatorFg string
						if activeIndBgConfig == "" || activeIndBgConfig == "auto" {
							if theme.ActiveIndicatorBg != "" {
								paneIndicatorBg = theme.ActiveIndicatorBg
							} else {
								paneIndicatorBg = theme.Bg
							}
						} else {
							paneIndicatorBg = activeIndBgConfig
						}
						if activeIndFgConfig == "" || activeIndFgConfig == "auto" {
							if paneIndicatorBg == "" || strings.EqualFold(paneIndicatorBg, "transparent") {
								paneIndicatorFg = c.getTextColorWithFallback("")
							} else {
								paneIndicatorFg = paneIndicatorBg
							}
						} else {
							paneIndicatorFg = activeIndFgConfig
						}
						paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Bold(true)
						if paneIndicatorBg != "" && !strings.EqualFold(paneIndicatorBg, "transparent") {
							paneIndStyle = paneIndStyle.Background(lipgloss.Color(paneIndicatorBg))
						}
						panePrefix = paneLeadChar + treeContinue + treeStyle.Render(" "+paneBranchChar) + paneIndStyle.Render(c.getAnimatedActiveIndicator(paneActiveIndicator))
						paneContent = activePaneStyle.Render(paneText)
					} else {
						panePrefix = paneLeadChar + treeContinue + treeStyle.Render(" "+paneBranchChar+treeConnectorChar)
						paneContent = paneStyle.Render(paneText)
					}

					// Apply bg color from start of pane name to right edge (minus buttons)
					panePrefixPlain := stripAnsi(panePrefix)
					panePrefixW := uniseg.StringWidth(panePrefixPlain)
					paneContentW := width - panePrefixW - paneMenuW
					if paneContentW < 0 {
						paneContentW = 0
					}

					// Pane rows get the same gradient as their window's tab row
					// (lighter -> base), minimized windows included.
					paneRowEndBg := ""
					if paneLineBg != "" {
						fromBg, toBg := gradientEndColor(paneLineBg), paneLineBg
						paneRowEndBg = gradientTailColor(paneLineBg) // dark tail, so the pane row's menu button continues the gradient like the tab line
						paneContent = c.applyGradientFill(paneContent, fromBg, toBg, paneContentW)
					}

					var paneBtns string
					menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
					paneBtns = menuBtn
					if paneLineBg != "" {
						paneBtns = c.applyBackgroundFill(paneBtns, paneRowEndBg, paneMenuW)
					}
					s.WriteString(panePrefix + paneContent + paneBtns + "\n")
					currentLine++

					regions = append(regions, daemon.ClickableRegion{
						StartLine: paneStartLine,
						EndLine:   currentLine - 1,
						StartCol:  0,
						EndCol:    width - 2,
						Action:    "select_pane",
						Target:    pane.ID,
					})
					regions = append(regions, daemon.ClickableRegion{
						StartLine: paneStartLine,
						EndLine:   currentLine - 1,
						StartCol:  width - 2,
						EndCol:    0,
						Action:    "pane_menu",
						Target:    pane.ID,
					})
				}

			}

		}

		if !isLastGroup {
			s.WriteString("\n")
			currentLine++
		}
	}

	return s.String(), regions, floatLine
}

// generatePrefixModeContent creates a flat window list with group prefixes (e.g., "SD| WindowName")
// In this mode, windows are not grouped hierarchically, but panes still show tree structure
func (c *Coordinator) generatePrefixModeContent(clientID string, width, height int, treeBranchChar, treeBranchLastChar, treeContinueChar, treeConnectorChar, expandedIcon, collapsedIcon string, treeStyle lipgloss.Style, disclosureColor, activeIndicator, activeIndFgConfig, activeIndBgConfig string) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion
	currentLine := 0

	activeIndFgConf := activeIndFgConfig
	activeIndBgConf := activeIndBgConfig
	inactiveFg := c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)

	// Collect all windows from all groups into a flat list
	type flatWindow struct {
		win        tmux.Window
		groupName  string
		groupTheme config.Theme
	}
	var allWindows []flatWindow
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			allWindows = append(allWindows, flatWindow{
				win:        win,
				groupName:  group.Name,
				groupTheme: group.Theme,
			})
		}
	}

	// Render each window with group prefix
	numWindows := len(allWindows)
	for wi, fw := range allWindows {
		win := fw.win
		groupName := fw.groupName
		theme := fw.groupTheme
		isLastWindow := wi == numWindows-1

		// For daemon mode: window is active if its ID matches this renderer's clientID
		isActive := (win.ID == clientID)
		windowStartLine := currentLine

		// Get group prefix (first 2-3 chars of group name)
		groupPrefix := ""
		if groupName != "Default" && groupName != "" {
			// Take first 2-3 chars or abbreviation
			if len(groupName) >= 3 {
				groupPrefix = groupName[:2] + "| "
			} else if len(groupName) > 0 {
				groupPrefix = groupName[:1] + "| "
			}
		}

		// Choose colors - custom color overrides group theme
		var bgColor, fgColor string
		isTransparent := win.CustomColor == "transparent"

		if isTransparent {
			bgColor = ""
			if isActive {
				fgColor = theme.ActiveFg
				if fgColor == "" {
					fgColor = theme.Fg
				}
			} else {
				fgColor = inactiveFg
			}
		} else if win.CustomColor != "" {
			if isActive {
				bgColor = win.CustomColor
			} else {
				bgColor = grouping.ShadeColorByIndex(win.CustomColor, 1)
			}
			// Contrast-aware: dark text on light custom colours, white on dark.
			fgColor = contrastFg(bgColor, isActive)
		} else if isActive {
			bgColor = theme.ActiveBg
			if bgColor == "" {
				bgColor = theme.Bg
			}
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			bgColor = theme.Bg
			fgColor = inactiveFg
		}

		// Minimized windows read as dimmed — but NOT when they're the active
		// (selected) window, so a selected minimized tab still shows its active
		// state like any other tab. Blend the fg ~50% toward the row bg so the dim
		// is visible even on terminals that collapse Bold+Faint into Bold (macOS
		// Terminal and others). Falls back to plain inactiveFg if we can't blend.
		if win.Minimized && !isActive {
			blendBg := bgColor
			if blendBg == "" {
				blendBg = theme.Bg
			}
			dimmed := blendHexToward(fgColor, blendBg, 0.30)
			if dimmed == fgColor {
				dimmed = inactiveFg
			}
			fgColor = dimmed
		}
		// Build style
		style := lipgloss.NewStyle()
		if fgColor != "" {
			style = style.Foreground(lipgloss.Color(fgColor))
		}

		if isActive {
			style = style.Bold(true)
		}
		if win.Minimized && !isActive {
			style = style.Faint(true)
		}

		// Build alert indicator
		alertIcon := ""
		ind := c.config.Indicators

		if ind.Busy.Enabled && win.Busy {
			alertStyle := indicatorStyle(ind.Busy.Color, win.Minimized, bgColor, theme.Bg)

			busyFrames := c.getBusyFrames()
			alertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
		} else if ind.Input.Enabled && win.Input {
			inputIcon := ind.Input.Icon
			if inputIcon == "" {
				inputIcon = "?"
			}
			alertStyle := indicatorStyle(ind.Input.Color, win.Minimized, bgColor, theme.Bg)

			if len(ind.Input.Frames) > 0 {
				alertIcon = alertStyle.Render(ind.Input.Frames[c.getSlowSpinnerFrame()%len(ind.Input.Frames)])
			} else {
				alertIcon = alertStyle.Render(inputIcon)
			}
		} else if !isActive {
			if ind.Bell.Enabled && win.Bell {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Bell))
			} else if ind.Activity.Enabled && win.Activity {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Activity))
			} else if ind.Silence.Enabled && win.Silence {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Silence))
			}
		}

		// Render indicator at far left
		var indicatorPart string
		if alertIcon != "" {
			indicatorPart = alertIcon
		} else {
			// Use empty space
			indicatorPart = " "
		}

		var contentPanes []tmux.Pane
		for _, pane := range win.Panes {
			if isAuxiliaryPane(pane) {
				continue
			}
			contentPanes = append(contentPanes, pane)
		}
		// Window collapse indicator if has panes
		hasPanes := len(contentPanes) > 1
		isWindowCollapsed := win.Collapsed
		var windowCollapseIcon string

		if hasPanes {
			if isWindowCollapsed {
				windowCollapseIcon = collapsedIcon
			} else {
				windowCollapseIcon = expandedIcon
			}
		}

		// Build tab content with group prefix
		// Display is 0-indexed to match tmux window indices
		displayName := c.composeTabBaseName(win)
		// SSH/mosh label: host+continuation (legacy) or single-line glyph+dir
		// (icon mode). See remoteTabDisplay.
		var remoteContinuation string
		displayName, remoteContinuation = c.remoteTabDisplay(win, displayName)
		// ssh glyph sits ahead of the group marker: "<glyph> <marker> <name>".
		displayName = composeTabMarker(c.sshTabGlyph(win), effectiveWindowMarker(win.Icon, theme.Icon)) + displayName
		visualNum := c.windowVisualPos[win.ID]
		baseContent := fmt.Sprintf("%d. %s%s", visualNum, groupPrefix, displayName)

		// Add pane count if collapsed
		if hasPanes && isWindowCollapsed {
			baseContent = fmt.Sprintf("%s (%d)", baseContent, len(contentPanes))
		}

		// Calculate widths
		prefixWidth := 2 // indicator + space
		if hasPanes {
			prefixWidth += 2 // collapse icon + space
		}
		windowContentWidth := width - prefixWidth

		// Word-wrap the inline label across up to MaxLines rows (line 1 = tab
		// line; remainder = continuation rows). Remote windows keep their single
		// host/dir continuation instead of wrapping.
		contRowWidth := width - 4
		if contRowWidth < 1 {
			contRowWidth = 1
		}
		maxLines := c.config.AI.TabSummary.MaxLines
		if maxLines < 1 || remoteContinuation != "" {
			maxLines = 1
		}
		wrapped := wrapTabLabel(baseContent, windowContentWidth, contRowWidth, maxLines)
		contentText := wrapped[0]
		contRows := wrapped[1:]
		if remoteContinuation != "" {
			contRows = []string{remoteContinuation}
		}

		// Styles for window collapse icon
		windowCollapseStyle := lipgloss.NewStyle()
		if disclosureColor != "" {
			windowCollapseStyle = windowCollapseStyle.Foreground(lipgloss.Color(disclosureColor))
		}

		// Render window line
		{
			windowLineStyle := lipgloss.NewStyle().Width(width)
			effectiveBg := bgColor
			if effectiveBg == "" {
				effectiveBg = theme.Bg
			}

			var lineContent string
			if hasPanes {
				lineContent = indicatorPart + " " + windowCollapseStyle.Render(windowCollapseIcon+" ") + style.Render(contentText)
			} else if isActive {
				var indicatorBg, indicatorFg string
				if activeIndBgConf == "" || activeIndBgConf == "auto" {
					if theme.ActiveIndicatorBg != "" {
						indicatorBg = theme.ActiveIndicatorBg
					} else {
						indicatorBg = theme.Bg
					}
				} else {
					indicatorBg = activeIndBgConf
				}
				if activeIndFgConf == "" || activeIndFgConf == "auto" {
					if indicatorBg == "" || strings.EqualFold(indicatorBg, "transparent") {
						indicatorFg = fgColor
					} else {
						indicatorFg = indicatorBg
					}
				} else {
					indicatorFg = activeIndFgConf
				}

				activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Bold(true)
				lineContent = indicatorPart + " " + activeIndStyle.Render(c.getAnimatedActiveIndicator(activeIndicator)) + style.Render(contentText)
			} else {
				lineContent = indicatorPart + "  " + style.Render(contentText)
			}
			renderedLine := windowLineStyle.Render(lineContent)
			if effectiveBg != "" {
				renderedLine = c.applyBackgroundFill(renderedLine, effectiveBg, width)
			}
			s.WriteString(renderedLine + "\n")
			currentLine++
		}

		// Continuation rows: SSH host/dir, or the wrapped overflow of the label.
		{
			rowBg := bgColor
			if rowBg == "" {
				rowBg = theme.Bg
			}
			for _, row := range contRows {
				if row == "" {
					continue
				}
				c.writeRemoteNameRow(&s, row, width, rowBg, fgColor, treeStyle, treeContinueChar, win.Minimized, isLastWindow)
				currentLine++
			}
		}

		// Record window region for click handling
		if hasPanes {
			collapseColEnd := 4 // indicator + space + icon + space
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    collapseColEnd,
				Action:    "toggle_panes",
				Target:    strconv.Itoa(win.Index),
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				StartCol:  collapseColEnd,
				EndCol:    0,
				Action:    "select_window",
				Target:    win.ID,
			})
		} else {
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				Action:    "select_window",
				Target:    win.ID,
			})
		}

		// Show panes if window has multiple panes and is not collapsed
		// Panes still get hierarchy (tree structure) in prefix mode
		if len(contentPanes) > 1 && !isWindowCollapsed {
			// Use inactiveFg for sidebar pane text (same as inactive windows)
			paneStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(inactiveFg))

			activePaneStyle := paneStyle
			if isActive {
				var activePaneFg string
				if win.CustomColor == "" && theme.ActiveFg != "" {
					activePaneFg = theme.ActiveFg
				} else if win.CustomColor != "" {
					// Custom colors use white text (dark backgrounds)
					activePaneFg = "#ffffff"
				} else {
					// Fall back to theme-aware text color
					activePaneFg = c.getTextColorWithFallback("")
				}
				activePaneStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(activePaneFg)).
					Bold(true)
			}

			numPanes := len(contentPanes)
			// Suppress non-AI busy icons when an AI pane is actively working
			anyAIBusyB := false
			for _, p := range contentPanes {
				if p.AIBusy {
					anyAIBusyB = true
					break
				}
			}
			for pi, pane := range contentPanes {
				isLastPane := pi == numPanes-1
				paneStartLine := currentLine

				var paneBranchChar string
				if isLastPane {
					for _, r := range treeBranchLastChar {
						paneBranchChar = string(r)
						break
					}
				} else {
					for _, r := range treeBranchChar {
						paneBranchChar = string(r)
						break
					}
				}

				paneNum := fmt.Sprintf("%d.%d", visualNum, pane.Index)
				paneLabel := pane.Command
				if pane.LockedTitle != "" {
					paneLabel = pane.LockedTitle
				} else if pane.Title != "" && pane.Title != pane.Command {
					paneLabel = pane.Title
				}
				if win.RemoteHost != "" && paneLabel != pane.Command && titleLooksLikeShellPrompt(paneLabel, win.RemoteHost) {
					paneLabel = pane.Command
				}
				paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

				paneIndentWidth := 5 // " " + space + branch + connector + connector
				paneContentWidth := width - paneIndentWidth

				// Truncate
				if lipgloss.Width(paneText) > paneContentWidth {
					truncated := ""
					for _, r := range paneText {
						if lipgloss.Width(truncated+string(r)) > paneContentWidth-1 {
							break
						}
						truncated += string(r)
					}
					paneText = truncated + "~"
				}

				paneActiveIndicator := c.config.Sidebar.Colors.ActiveIndicator
				if paneActiveIndicator == "" {
					paneActiveIndicator = "█"
				}

				// Per-pane alert indicator
				paneAlertIcon := ""
				pInd := c.config.Indicators
				if pane.AIBusy && pInd.Busy.Enabled {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
					busyFrames := c.getBusyFrames()
					paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
				} else if pane.AIInput && pInd.Input.Enabled {
					inputIcon := pInd.Input.Icon
					if inputIcon == "" {
						inputIcon = "?"
					}
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Input.Color))
					if len(pInd.Input.Frames) > 0 {
						paneAlertIcon = alertStyle.Render(pInd.Input.Frames[c.getSlowSpinnerFrame()%len(pInd.Input.Frames)])
					} else {
						paneAlertIcon = alertStyle.Render(inputIcon)
					}
				} else if pane.Busy && pInd.Busy.Enabled && !tmux.IsAITool(pane.Command) && !anyAIBusyB {
					// Non-AI pane with foreground process; suppress when AI is busy in same window
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
					busyFrames := c.getBusyFrames()
					paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
				}

				paneLeadChar := " "
				if paneAlertIcon != "" {
					paneLeadChar = paneAlertIcon
				}

				var paneLineBg string
				if bgColor != "" {
					paneLineBg = bgColor
				} else {
					paneLineBg = theme.Bg
				}
				paneLineStyle := lipgloss.NewStyle().Background(lipgloss.Color(paneLineBg)).Width(width)

				if pane.Active && isActive {
					var paneIndicatorBg, paneIndicatorFg string
					if activeIndBgConf == "" || activeIndBgConf == "auto" {
						if theme.ActiveIndicatorBg != "" {
							paneIndicatorBg = theme.ActiveIndicatorBg
						} else {
							paneIndicatorBg = theme.Bg
						}
					} else {
						paneIndicatorBg = activeIndBgConf
					}
					if activeIndFgConf == "" || activeIndFgConf == "auto" {
						if paneIndicatorBg == "" || strings.EqualFold(paneIndicatorBg, "transparent") {
							paneIndicatorFg = c.getTextColorWithFallback("")
						} else {
							paneIndicatorFg = paneIndicatorBg
						}
					} else {
						paneIndicatorFg = activeIndFgConf
					}
					paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Bold(true)
					fullWidthPaneStyle := activePaneStyle.Width(paneContentWidth)
					lineContent := paneLeadChar + "  " + treeStyle.Render(paneBranchChar+treeConnectorChar) + paneIndStyle.Render(c.getAnimatedActiveIndicator(paneActiveIndicator)) + fullWidthPaneStyle.Render(paneText)
					renderedPane := paneLineStyle.Render(lineContent)
					if paneLineBg != "" {
						renderedPane = c.applyBackgroundFill(renderedPane, paneLineBg, width)
					}
					s.WriteString(renderedPane + "\n")
				} else {
					lineContent := paneLeadChar + "  " + treeStyle.Render(paneBranchChar+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText)
					renderedPane := paneLineStyle.Render(lineContent)
					if paneLineBg != "" {
						renderedPane = c.applyBackgroundFill(renderedPane, paneLineBg, width)
					}
					s.WriteString(renderedPane + "\n")
				}
				currentLine++

				// Record pane region for click handling
				regions = append(regions, daemon.ClickableRegion{
					StartLine: paneStartLine,
					EndLine:   currentLine - 1,
					Action:    "select_pane",
					Target:    pane.ID,
				})
			}
		}

		// Padding between windows (not after last)
		if !isLastWindow {
			s.WriteString("\n")
			currentLine++
		}
	}

	return s.String(), regions
}

// widgetEntry represents a single widget for the zone layout system.
// Widgets are sorted by zone (top/bottom) then priority within each zone.
type widgetEntry struct {
	name     string
	zone     string // "top" or "bottom"
	priority int
	content  string // pre-rendered content (may contain zone.Mark markers)
}

// collectWidgetEntries gathers all enabled widgets and action buttons into
// a sorted slice of widgetEntry, ready for zone-based rendering.
func (c *Coordinator) collectWidgetEntries(width int, skipPet, skipDebugBar bool) []widgetEntry {
	var entries []widgetEntry

	// Clock widget
	if c.config.Widgets.Clock.Enabled {
		pos := c.config.Widgets.Clock.Position
		if pos == "" {
			pos = "top"
		}
		entries = append(entries, widgetEntry{
			name:     "clock",
			zone:     pos,
			priority: c.config.Widgets.Clock.Priority,
			content:  constrainWidgetWidth(c.renderClockWidget(width), width),
		})
	}

	// Pet widget — skip when viewport is too small for all tabs
	if c.config.Widgets.Pet.Enabled && !skipPet {
		pos := c.config.Widgets.Pet.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "pet",
			zone:     pos,
			priority: c.config.Widgets.Pet.Priority,
			content:  c.renderPetWidget(width, skipDebugBar),
		})
	}

	// Git widget
	if c.config.Widgets.Git.Enabled {
		pos := c.config.Widgets.Git.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "git",
			zone:     pos,
			priority: c.config.Widgets.Git.Priority,
			content:  constrainWidgetWidth(c.renderGitWidget(width), width),
		})
	}

	// Session widget
	if c.config.Widgets.Session.Enabled {
		pos := c.config.Widgets.Session.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "session",
			zone:     pos,
			priority: c.config.Widgets.Session.Priority,
			content:  constrainWidgetWidth(c.renderSessionWidget(width), width),
		})
	}

	// Claude usage widget
	if c.config.Widgets.Claude.Enabled {
		pos := c.config.Widgets.Claude.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "claude",
			zone:     pos,
			priority: c.config.Widgets.Claude.Priority,
			content:  constrainWidgetWidth(c.renderClaudeWidget(width), width),
		})
	}

	// TeamClaude quota widget
	if c.config.Widgets.TeamClaude.Enabled {
		pos := c.config.Widgets.TeamClaude.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "teamclaude",
			zone:     pos,
			priority: c.config.Widgets.TeamClaude.Priority,
			content:  constrainWidgetWidth(c.renderTeamClaudeWidget(width), width),
		})
	}

	// On phone, the window-header button bar already provides prev/next navigation
	// (with matching up/down arrows), so the sidebar's dedicated nav buttons would
	// be redundant.
	if c.ActiveClientProfile() != "phone" {
		entries = append(entries, widgetEntry{
			name:     "nav_buttons",
			zone:     "bottom",
			priority: 9998,
			content:  c.renderNavButtons(width),
		})
	}

	// Action buttons (new tab, new group, close, touch mode toggle)
	actionZone := c.config.Sidebar.ActionZone
	if actionZone == "" {
		actionZone = "bottom"
	}
	actionPriority := c.config.Sidebar.ActionPriority
	if actionPriority == 0 {
		actionPriority = 90
	}
	entries = append(entries, widgetEntry{
		name:     "action_buttons",
		zone:     actionZone,
		priority: actionPriority,
		content:  c.renderPinnedActionButtons(width),
	})

	// Sort by priority within each zone (stable sort preserves insertion order for equal priority)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].priority < entries[j].priority
	})

	return entries
}

// renderWidgetZone renders a list of widget entries into a single content string
// and extracts BubbleZone-based click regions. Positions are relative to the
// returned content (caller must offset them).
func (c *Coordinator) renderWidgetZone(entries []widgetEntry, width int) (string, []daemon.ClickableRegion) {
	if len(entries) == 0 {
		return "", nil
	}

	var s strings.Builder
	for _, entry := range entries {
		s.WriteString(entry.content)
	}

	rawContent := s.String()
	if rawContent == "" {
		return "", nil
	}

	// Scan for zone markers (BubbleZone)
	scannedContent := zone.Scan(rawContent)

	// Extract zone bounds for all known clickable areas.
	// Pet zones must match the zone.Mark calls in renderPetWidget /
	// renderAdventurePlayArea. Dropped entries (drop_yarn, clean_poop,
	// pet_pet) were never marked; pet widget click handling now relies
	// on handlePetWidgetClick's custom hit-testing rather than these
	// regions, but the air/ground/feed entries are kept so the renderer
	// can still route obvious clicks via ResolvedAction.
	knownZones := []string{
		// Pet zones
		"pet:drop_food", "pet:air_high", "pet:air_low", "pet:ground",
		// Button zones
		"sidebar:new_tab", "sidebar:new_group", "sidebar:close_tab",
		// TeamClaude degraded-model warning -> opens popup
		"teamclaude:open_degraded",
		// Sidebar zones
		"sidebar:shrink", "sidebar:grow",
		"sidebar:prev_window", "sidebar:next_window",
	}
	var regions []daemon.ClickableRegion
	for _, zoneID := range knownZones {
		if info := zone.Get(zoneID); info != nil && !info.IsZero() {
			parts := strings.SplitN(zoneID, ":", 2)
			if len(parts) == 2 {
				regions = append(regions, daemon.ClickableRegion{
					StartLine: info.StartY,
					EndLine:   info.EndY,
					StartCol:  info.StartX,
					EndCol:    info.EndX + 1, // Convert from inclusive to exclusive
					Action:    parts[1],
					Target:    parts[0],
				})
				coordinatorDebugLog.Printf("BubbleZone extracted: %s -> lines %d-%d, cols %d-%d (exclusive)",
					zoneID, info.StartY, info.EndY, info.StartX, info.EndX+1)
			}
		}
	}

	coordinatorDebugLog.Printf("BubbleZone: extracted %d widget regions from zone", len(regions))

	// Apply safety constraint to the clean content (after markers are stripped)
	scannedContent = constrainWidgetWidth(scannedContent, width)

	return scannedContent, regions
}

// generateWidgetZones renders all widgets into top and bottom zones,
// plus resize buttons that always appear at the very bottom.
// Returns: topContent, topRegions, bottomContent, bottomRegions
func (c *Coordinator) generateWidgetZones(width int, skipPet, skipDebugBar bool) (string, []daemon.ClickableRegion, string, []daemon.ClickableRegion) {
	entries := c.collectWidgetEntries(width, skipPet, skipDebugBar)

	// Split into top and bottom zones
	var topEntries, bottomEntries []widgetEntry
	for _, e := range entries {
		if e.zone == "top" {
			topEntries = append(topEntries, e)
		} else {
			bottomEntries = append(bottomEntries, e)
		}
	}

	// Render top zone
	topContent, topRegions := c.renderWidgetZone(topEntries, width)

	// Add resize buttons to bottom (always last). Hidden on phone where the
	// sidebar has no room to shrink/grow and the < / > glyphs would look like
	// navigation arrows that duplicate the window-header button bar.
	if c.ActiveClientProfile() != "phone" {
		bottomEntries = append(bottomEntries, widgetEntry{
			name:     "resize_buttons",
			zone:     "bottom",
			priority: 9999,
			content:  c.renderSidebarResizeButtons(width),
		})
	}

	// Render bottom zone
	bottomContent, bottomRegions := c.renderWidgetZone(bottomEntries, width)

	return topContent, topRegions, bottomContent, bottomRegions
}

// renderClockWidget renders the clock/date widget
func (c *Coordinator) renderClockWidget(width int) string {
	clock := c.config.Widgets.Clock
	now := time.Now()

	timeFormat := clock.Format
	if timeFormat == "" {
		timeFormat = "15:04:05"
	}

	// Use clock's Fg, fall back to background-aware default for visibility
	fgColor := c.getInactiveTextColorWithFallback(clock.Fg)
	style := lipgloss.NewStyle()
	if fgColor != "" {
		style = style.Foreground(lipgloss.Color(fgColor))
	}
	// Paint bg explicitly so trailing/inter-widget cells don't revert to
	// terminal default after a theme flip. Uses the coordinator's resolved
	// terminal bg (config override > theme > detector).
	if bg := c.GetTerminalBg(); bg != "" {
		style = style.Background(lipgloss.Color(bg))
	}

	dividerStyle := lipgloss.NewStyle()
	dividerFg := c.getInactiveTextColorWithFallback(clock.DividerFg)
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	if bg := c.GetTerminalBg(); bg != "" {
		dividerStyle = dividerStyle.Background(lipgloss.Color(bg))
	}

	var result strings.Builder

	for i := 0; i < clock.MarginTop; i++ {
		result.WriteString("\n")
	}

	if clock.Divider != "" {
		dividerWidth := lipgloss.Width(clock.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < clock.PaddingTop; i++ {
		result.WriteString("\n")
	}

	timeStr := now.Format(timeFormat)
	timePadding := (width - lipgloss.Width(timeStr)) / 2
	if timePadding < 0 {
		timePadding = 0
	}
	result.WriteString(style.Render(strings.Repeat(" ", timePadding)+timeStr) + "\n")

	if clock.ShowDate {
		dateFormat := clock.DateFmt
		if dateFormat == "" {
			dateFormat = "Mon Jan 2"
		}
		dateStr := now.Format(dateFormat)
		datePadding := (width - lipgloss.Width(dateStr)) / 2
		if datePadding < 0 {
			datePadding = 0
		}
		result.WriteString(style.Render(strings.Repeat(" ", datePadding)+dateStr) + "\n")
	}

	for i := 0; i < clock.PaddingBot; i++ {
		result.WriteString("\n")
	}

	if clock.DividerBottom != "" {
		dividerWidth := lipgloss.Width(clock.DividerBottom)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.DividerBottom, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < clock.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderGitWidget renders git status widget
func (c *Coordinator) renderGitWidget(width int) string {
	git := c.config.Widgets.Git

	if !c.isGitRepo {
		return ""
	}

	// Fall back to background-aware default for visibility
	gitDividerFg := c.getInactiveTextColorWithFallback(git.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if gitDividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(gitDividerFg))
	}

	gitFg := c.getInactiveTextColorWithFallback(git.Fg)
	style := lipgloss.NewStyle()
	if gitFg != "" {
		style = style.Foreground(lipgloss.Color(gitFg))
	}

	var result strings.Builder

	for i := 0; i < git.MarginTop; i++ {
		result.WriteString("\n")
	}

	if git.Divider != "" {
		dividerWidth := lipgloss.Width(git.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(git.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < git.PaddingTop; i++ {
		result.WriteString("\n")
	}

	icon := ""
	branch := c.gitBranch

	// Build status first to know its width
	status := ""
	if c.gitDirty > 0 {
		status += fmt.Sprintf(" *%d", c.gitDirty)
	}
	if c.gitAhead > 0 {
		status += fmt.Sprintf(" ↑%d", c.gitAhead)
	}
	if c.gitBehind > 0 {
		status += fmt.Sprintf(" ↓%d", c.gitBehind)
	}

	// Calculate max branch width (accounting for icon, spacing, and status)
	prefix := fmt.Sprintf("  %s ", icon)
	maxBranch := width - lipgloss.Width(prefix) - lipgloss.Width(status)
	if maxBranch < 5 {
		maxBranch = 5
	}

	// Truncate branch using proper rune width
	if lipgloss.Width(branch) > maxBranch {
		truncated := ""
		for _, r := range branch {
			if lipgloss.Width(truncated+string(r)) > maxBranch-1 {
				break
			}
			truncated += string(r)
		}
		branch = truncated + "~"
	}

	result.WriteString(style.Render(prefix+branch+status) + "\n")

	for i := 0; i < git.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < git.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderSessionWidget renders the session info widget
func (c *Coordinator) renderSessionWidget(width int) string {
	sessionCfg := c.config.Widgets.Session
	if !sessionCfg.Enabled {
		return ""
	}

	style := sessionCfg.Style
	if style == "" {
		style = "nerd"
	}
	icons, ok := sessionIconsByStyle[style]
	if !ok {
		icons = sessionIconsByStyle["nerd"]
	}

	var result strings.Builder

	for i := 0; i < sessionCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := sessionCfg.Divider
	if divider == "" {
		divider = "─"
	}
	// Fall back to background-aware default for visibility
	sessDividerFg := c.getInactiveTextColorWithFallback(sessionCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if sessDividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(sessDividerFg))
	}
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < sessionCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	var parts []string

	// Determine foreground color with fallback chain
	sessFg := sessionCfg.SessionFg
	if sessFg == "" {
		sessFg = sessionCfg.Fg
	}
	sessFg = c.getInactiveTextColorWithFallback(sessFg)
	sessionStyle := lipgloss.NewStyle()
	if sessFg != "" {
		sessionStyle = sessionStyle.Foreground(lipgloss.Color(sessFg))
	}

	// Truncate session name if needed (reserve space for other stats)
	sessionName := c.sessionName
	maxNameWidth := width - 10 // Reserve space for other parts
	if maxNameWidth < 5 {
		maxNameWidth = 5
	}
	if lipgloss.Width(sessionName) > maxNameWidth {
		truncated := ""
		for _, r := range sessionName {
			if lipgloss.Width(truncated+string(r)) > maxNameWidth-1 {
				break
			}
			truncated += string(r)
		}
		sessionName = truncated + "~"
	}

	if icons.Session != "" {
		parts = append(parts, sessionStyle.Render(icons.Session+" "+sessionName))
	} else {
		parts = append(parts, sessionStyle.Render(sessionName))
	}

	if sessionCfg.ShowClients && c.sessionClients > 0 {
		clientStyle := lipgloss.NewStyle()
		if sessFg != "" {
			clientStyle = clientStyle.Foreground(lipgloss.Color(sessFg))
		}
		if icons.Clients != "" {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%s%d", icons.Clients, c.sessionClients)))
		} else {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%d", c.sessionClients)))
		}
	}

	if sessionCfg.ShowWindowCount {
		windowStyle := lipgloss.NewStyle()
		if sessFg != "" {
			windowStyle = windowStyle.Foreground(lipgloss.Color(sessFg))
		}
		if icons.Windows != "" {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%s%d", icons.Windows, c.windowCount)))
		} else {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%d", c.windowCount)))
		}
	}

	result.WriteString(strings.Join(parts, " ") + "\n")

	for i := 0; i < sessionCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < sessionCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderClaudeWidget renders the Claude Code API usage widget
func (c *Coordinator) renderClaudeWidget(width int) string {
	claudeCfg := c.config.Widgets.Claude
	if !claudeCfg.Enabled {
		return ""
	}

	var result strings.Builder

	// Margins and dividers
	for i := 0; i < claudeCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := claudeCfg.Divider
	if divider == "" {
		divider = "-"
	}
	dividerFg := c.getInactiveTextColorWithFallback(claudeCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < claudeCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Get Claude usage data
	dbPath := claudeCfg.DBPath
	if dbPath == "" {
		homeDir, _ := os.UserHomeDir()
		dbPath = filepath.Join(homeDir, ".claude", "__store.db")
	}

	todayCost, weekCost, monthCost, totalCost, msgCount := c.getClaudeUsageStats(dbPath)

	// Style for labels and values
	labelFg := c.getInactiveTextColorWithFallback(claudeCfg.Fg)
	costFg := claudeCfg.CostFg
	if costFg == "" {
		costFg = "#6bcb77" // Green for money
	}

	labelStyle := lipgloss.NewStyle()
	if labelFg != "" {
		labelStyle = labelStyle.Foreground(lipgloss.Color(labelFg))
	}
	costStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(costFg))

	// Icon based on style
	style := claudeCfg.Style
	if style == "" {
		style = "nerd"
	}
	icon := ""
	switch style {
	case "nerd":
		icon = " " // nf-md-robot (Claude)
	case "emoji":
		icon = "$ "
	case "ascii":
		icon = "[CC] "
	}

	// Header
	result.WriteString(labelStyle.Render(icon+"Claude") + "\n")

	// Show requested stats
	showToday := claudeCfg.ShowToday
	// Default to showing today if nothing specified
	if !showToday && !claudeCfg.ShowWeek && !claudeCfg.ShowMonth && !claudeCfg.ShowTotal {
		showToday = true
	}

	if showToday {
		result.WriteString(labelStyle.Render("  Today: ") + costStyle.Render(fmt.Sprintf("$%.2f", todayCost)) + "\n")
	}
	if claudeCfg.ShowWeek {
		result.WriteString(labelStyle.Render("  Week:  ") + costStyle.Render(fmt.Sprintf("$%.2f", weekCost)) + "\n")
	}
	if claudeCfg.ShowMonth {
		result.WriteString(labelStyle.Render("  Month: ") + costStyle.Render(fmt.Sprintf("$%.2f", monthCost)) + "\n")
	}
	if claudeCfg.ShowTotal {
		result.WriteString(labelStyle.Render("  Total: ") + costStyle.Render(fmt.Sprintf("$%.2f", totalCost)) + "\n")
	}
	if claudeCfg.ShowMessages {
		result.WriteString(labelStyle.Render(fmt.Sprintf("  Msgs:  %d", msgCount)) + "\n")
	}

	for i := 0; i < claudeCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < claudeCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// getClaudeUsageStats queries the Claude Code SQLite database for usage stats
// Uses sqlite3 command line tool to avoid adding a Go SQLite dependency
func (c *Coordinator) getClaudeUsageStats(dbPath string) (today, week, month, total float64, msgCount int) {
	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return 0, 0, 0, 0, 0
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	weekStart := todayStart - int64((int(now.Weekday())+6)%7*86400) // Monday
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()

	// Build a single query that returns all stats
	query := fmt.Sprintf(`SELECT
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(cost_usd), 0),
		COUNT(*)
		FROM assistant_messages;`, todayStart, weekStart, monthStart)

	out, err := exec.Command("sqlite3", "-separator", "|", dbPath, query).Output()
	if err != nil {
		return 0, 0, 0, 0, 0
	}

	// Parse result: "today|week|month|total|count"
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 5 {
		today, _ = strconv.ParseFloat(parts[0], 64)
		week, _ = strconv.ParseFloat(parts[1], 64)
		month, _ = strconv.ParseFloat(parts[2], 64)
		total, _ = strconv.ParseFloat(parts[3], 64)
		msgCount, _ = strconv.Atoi(parts[4])
	}

	return today, week, month, total, msgCount
}

// quotaCell is one quota window to render in a TeamClaude account row: the
// remaining fraction (0..1, nil if unknown) and the reset time (ms epoch, 0 if
// unknown).
type quotaCell struct {
	frac  *float64
	reset int64
}

// shortResetDur formats the time until an epoch-millisecond reset as a compact
// string. Returns "" when the time is unknown or already past. Granularity steps
// down as the remaining time shrinks: 1d3h for >=24h (minutes dropped), 1h30m
// for >=1h, 45m for <1h.
func shortResetDur(resetMs int64) string {
	if resetMs <= 0 {
		return ""
	}
	d := time.Until(time.UnixMilli(resetMs))
	if d <= 0 {
		return ""
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		days := int(d / (24 * time.Hour))
		h := int((d % (24 * time.Hour)) / time.Hour)
		if h == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, h)
	}
}

// formatExtraDollars renders a dollar amount compactly for the TeamClaude
// widget's extra-usage suffix ("$rem/$lim"). Sidebar real estate is precious,
// so we drop cents past $10 and use a "k" suffix past $1000.
func formatExtraDollars(v float64) string {
	if v < 0 {
		v = 0
	}
	switch {
	case v >= 1000:
		return fmt.Sprintf("%.1fk", v/1000)
	case v >= 10:
		return fmt.Sprintf("%d", int(v+0.5))
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// teamClaudeDisplayName returns the account's full name for display, dropping
// only the redundant auto-generated personal-org suffix
// "(<email>'s Organization)" (which just repeats the email). A real custom org
// suffix like " (Gunpowder)" is preserved so duplicate emails stay
// distinguishable. The result is NOT shortened — truncation (with an ellipsis)
// happens later in teamClaudeTruncateName once the available width is known.
func teamClaudeDisplayName(name string) string {
	if i := strings.LastIndex(name, " ("); i >= 0 && strings.HasSuffix(name, ")") {
		org := name[i+2 : len(name)-1]
		if strings.Contains(org, "Organization") {
			return name[:i]
		}
	}
	return name
}

// teamClaudeBareEmail extracts just the email address from an account name,
// dropping ANY " (org)" suffix — personal ("'s Organization") or a real team
// name alike. Unlike teamClaudeDisplayName (which keeps a real org suffix for
// display), this collapses a personal+team pair on the SAME email to one key, so
// duplicate-email detection groups them and the personal row can be marked PER.
func teamClaudeBareEmail(name string) string {
	if i := strings.Index(name, " ("); i >= 0 {
		return strings.TrimSpace(name[:i])
	}
	return strings.TrimSpace(name)
}

// teamClaudeTruncateName fits a display name into max columns, adding an "…"
// ellipsis when it must cut. If the name carries a trailing " (org)" suffix,
// that suffix is preserved and the head (email) is ellipsized instead, so the
// distinguishing org isn't the thing that gets dropped.
func teamClaudeTruncateName(name string, max int) string {
	if max <= 0 {
		return ""
	}
	if runewidth.StringWidth(name) <= max {
		return name
	}
	if i := strings.LastIndex(name, " ("); i >= 0 && strings.HasSuffix(name, ")") {
		suffix := name[i:] // " (Gunpowder)"
		sw := runewidth.StringWidth(suffix)
		if max-sw >= 2 { // room for at least one head char + ellipsis + suffix
			return runewidth.Truncate(name[:i], max-sw, "…") + suffix
		}
	}
	return runewidth.Truncate(name, max, "…")
}

// renderTeamClaudeWidget renders per-account Claude quota left, from the cached
// teamclaude proxy status. Data is fetched off the render path by
// RefreshTeamClaude; here we only read the cache.
func (c *Coordinator) renderTeamClaudeWidget(width int) string {
	tcCfg := c.config.Widgets.TeamClaude
	if !tcCfg.Enabled {
		return ""
	}

	// Read cached state directly WITHOUT taking stateMu: the render path
	// (renderSidebar) already holds c.stateMu.RLock() across widget rendering,
	// and RWMutex read locks are NOT recursively safe — a second RLock here
	// deadlocks the moment a writer (the async fetch goroutine's Lock) is
	// pending. This mirrors renderGitWidget/renderSessionWidget, which also read
	// their cached fields under the caller's lock.
	status := c.teamClaudeStatus
	fetchErr := c.teamClaudeErr
	degradedModels := c.teamClaudeModels.ActiveDegradations(time.Now().UnixMilli())
	degraded := len(degradedModels) > 0

	var result strings.Builder

	for i := 0; i < tcCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := tcCfg.Divider
	if divider == "" {
		divider = "-"
	}
	dividerFg := c.getInactiveTextColorWithFallback(tcCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	if dw := lipgloss.Width(divider); dw > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dw)) + "\n")
	}

	for i := 0; i < tcCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	labelFg := c.getInactiveTextColorWithFallback(tcCfg.Fg)
	labelStyle := lipgloss.NewStyle()
	if labelFg != "" {
		labelStyle = labelStyle.Foreground(lipgloss.Color(labelFg))
	}

	// Header icon by style.
	style := tcCfg.Style
	if style == "" {
		style = "nerd"
	}
	icon := ""
	switch style {
	case "nerd":
		icon = "󱙺 " // nf-md-account_group_outline
	case "emoji":
		icon = "👥 "
	case "ascii":
		icon = "[TC] "
	}
	// When a model is actively being downgraded by the proxy, swap the header
	// icon for a warning glyph (same display width, so the ANSI-truncation trap
	// stays irrelevant). The whole header is then rendered in a warning color and
	// made clickable to open the degraded-models popup.
	if degraded {
		switch style {
		case "emoji":
			icon = "⚠️ "
		case "ascii":
			icon = "[!] "
		default:
			icon = " " // nf-fa-warning (U+F071)
		}
	}

	// Which quota windows to show. The header carries a "[S/W]" legend so the
	// per-account rows can drop the labels and just show bars + percentages.
	showSession := tcCfg.ShowSession
	showWeekly := tcCfg.ShowWeekly
	if !showSession && !showWeekly {
		showSession, showWeekly = true, true
	}
	var legendParts []string
	if showSession {
		legendParts = append(legendParts, "S")
	}
	if showWeekly {
		legendParts = append(legendParts, "W")
	}
	// Header decoration when the proxy has fallen onto a paid extra-usage tier:
	// append "[$]" so the sidebar telegraphs "paid territory" even before the
	// per-account row is scanned. Distinct from the degraded-model warning.
	extraActive := status.AnyActiveExtraUsage()
	header := icon + "TeamClaude [" + strings.Join(legendParts, "/") + "]"
	if extraActive {
		header += " [$]"
	}
	// Truncate on the PLAIN text (markers/ANSI added afterward) so width math is
	// correct; zone.Mark's bytes are zero-width and stripped before
	// constrainWidgetWidth.
	headerText := runewidth.Truncate(header, width, "")
	headerStyle := labelStyle
	switch {
	case degraded:
		headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd93d")).Bold(true)
	case extraActive:
		// Amber-but-not-bold so it's clearly a "heads up" without screaming as
		// loudly as a degraded model.
		headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd93d"))
	}
	renderedHeader := headerStyle.Render(headerText)
	if degraded {
		renderedHeader = zone.Mark("teamclaude:open_degraded", renderedHeader)
	}
	result.WriteString(renderedHeader + "\n")

	switch {
	case status == nil && fetchErr != nil:
		result.WriteString(labelStyle.Render("  unreachable") + "\n")
	case status == nil:
		result.WriteString(labelStyle.Render("  …") + "\n")
	default:
		// Compact layout: the shown quota windows on one line under the account
		// name, with the percentage drawn INSIDE each bar so the bars can use the
		// full width. The filled fraction is a solid colored block (dark text);
		// the empty track is a faint tint of the same color (saturated text), so
		// the number stays legible on both light and dark terminals. Bars are
		// sized to width here so the composed line never exceeds `width` and
		// never hits constrainWidgetWidth's non-ANSI-aware truncation.
		termBg := c.GetTerminalBg()
		barColorFor := func(pct int) string {
			if tcCfg.BarFg != "" {
				return tcCfg.BarFg
			}
			// Healthy headroom (and unknown) stays a calm, desaturated gray; the bar
			// only takes on a warning hue once it crosses into yellow/red territory.
			// A neutral gray (zero saturation) reads as muted on any theme while
			// keeping enough contrast for the dark in-bar percentage text.
			const grayHealthy = "#b9bdc2"
			switch {
			case pct < 0:
				return grayHealthy
			case pct < 30:
				return "#ff6b6b" // red — low headroom
			case pct < 60:
				return "#ffd93d" // yellow — getting low
			default:
				return grayHealthy // plenty left — muted gray, not green
			}
		}
		// inBar renders one bar of exactly bw cells with the percentage (and,
		// when it fits, the time until that window resets) centered inside it.
		inBar := func(f *float64, resetMs int64, bw int) string {
			if bw < 1 {
				return ""
			}
			pct := -1
			txt := "--"
			if f != nil {
				pct = int(*f*100 + 0.5)
				txt = fmt.Sprintf("%d%%", pct)
				// Append reset countdown (e.g. "90% 2h") only if it fits.
				if d := shortResetDur(resetMs); d != "" {
					if full := txt + " " + d; runewidth.StringWidth(full) <= bw {
						txt = full
					}
				}
			}
			if runewidth.StringWidth(txt) > bw {
				txt = runewidth.Truncate(txt, bw, "")
			}
			// Center the text within the bar width.
			pad := bw - runewidth.StringWidth(txt)
			left := pad / 2
			content := strings.Repeat(" ", left) + txt + strings.Repeat(" ", pad-left)
			runes := []rune(content)

			filled := 0
			if pct >= 0 {
				filled = (pct*bw + 50) / 100
				if filled > bw {
					filled = bw
				}
			}
			barColor := barColorFor(pct)
			filledStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(barColor)).
				Foreground(lipgloss.Color("#1c1c1c")).Bold(true)
			// Track (empty) portion: faint tint of the bar color as the background
			// so the full bar extent is visible, with the normal label foreground
			// for the digits — readable on any theme (barColor-on-track can be
			// low contrast, e.g. yellow text on a pale-yellow track).
			trackBg := desaturateHex(barColor, 0.28, termBg)
			emptyStyle := lipgloss.NewStyle()
			if labelFg != "" {
				emptyStyle = emptyStyle.Foreground(lipgloss.Color(labelFg))
			}
			if trackBg != "" {
				emptyStyle = emptyStyle.Background(lipgloss.Color(trackBg))
			}
			var b strings.Builder
			if filled > 0 {
				b.WriteString(filledStyle.Render(string(runes[:filled])))
			}
			if filled < len(runes) {
				b.WriteString(emptyStyle.Render(string(runes[filled:])))
			}
			return b.String()
		}
		quotaLine := func(cells []quotaCell) string {
			n := len(cells)
			if n == 0 {
				return ""
			}
			// Available bar columns = width - left indent(1) - right pad(1) -
			// joins(n-1). Distribute evenly, giving the remainder to the leftmost
			// bars. The right column is left blank so bars don't touch the edge.
			avail := width - 2 - (n - 1)
			if avail < n { // too narrow for bars: just show percentages
				var b strings.Builder
				b.WriteString(labelStyle.Render(" "))
				for i, c := range cells {
					if i > 0 {
						b.WriteString(labelStyle.Render(" "))
					}
					if c.frac == nil {
						b.WriteString(labelStyle.Render("--"))
					} else {
						b.WriteString(labelStyle.Render(fmt.Sprintf("%d%%", int(*c.frac*100+0.5))))
					}
				}
				return b.String() + "\n"
			}
			base := avail / n
			rem := avail % n
			var b strings.Builder
			b.WriteString(labelStyle.Render(" "))
			for i, c := range cells {
				if i > 0 {
					b.WriteString(labelStyle.Render(" "))
				}
				bw := base
				if i < rem {
					bw++
				}
				b.WriteString(inBar(c.frac, c.reset, bw))
			}
			return b.String() + "\n"
		}

		// Detect duplicate accounts (same email surfacing as both a personal and
		// an organization account). teamClaudeDisplayName strips the " (… Organization)"
		// suffix, so a personal/org pair collapses to the same base here. When that
		// happens we disambiguate the rows with a trailing [PER]/[ORG] tag.
		baseCounts := map[string]int{}
		for _, a := range status.Accounts {
			if tcCfg.ShowCurrentOnly && a.Name != status.CurrentAccount {
				continue
			}
			baseCounts[teamClaudeBareEmail(a.Name)]++
		}

		nowMs := time.Now().UnixMilli()
		for _, a := range status.Accounts {
			if tcCfg.ShowCurrentOnly && a.Name != status.CurrentAccount {
				continue
			}
			// The proxy load-balances sessions across accounts, so more than one
			// account can be serving traffic at once. "active" mirrors the
			// teamclaude TUI's green rule (in-flight requests, or used in the last
			// 15m); every such account gets the green treatment below — not just
			// the single CurrentAccount.
			active := a.ActivelyUsed(nowMs)
			marker := " "
			if a.Name == status.CurrentAccount {
				marker = "▸"
			}
			// Every actively-serving account gets a left indicator (not just the
			// single CurrentAccount), since the proxy load-balances across accounts.
			// It's colored green below; the account name itself stays uncolored.
			if active && !a.RateLimited() && marker == " " {
				marker = "▸"
			}
			if a.RateLimited() {
				marker = "!"
			}
			// When this account is currently being charged on its extra-usage
			// (overage) budget, swap the marker for a "$" so the row is unmistakably
			// in paid-territory at a glance. Rate-limited still wins.
			if a.IsActiveExtraUsage && !a.RateLimited() {
				marker = "$"
			}
			// The header row is "email | conns | tier":
			//   conns — live connections "active/max" (activeRequests/maxConcurrency),
			//           shown whenever a concurrency cap is known (not just while
			//           busy). Green only when MORE THAN ONE request is in flight
			//           (genuine concurrency); a single/zero active request stays dim.
			//   tier  — the compact subscription token (teamclaude.ShortTier:
			//           "Max 20x"->"20x", "Team 5x"->"5x", "Pro"->"Pro"), prefixed
			//           with "PER" for a PERSONAL account whose email is shared
			//           with a team account (so the personal/team pair is
			//           distinguishable: e.g. "PER20x" vs "5x").
			// Separators are a dim compact "|". The email takes whatever width is
			// left after the (reserved) conns and tier segments, so it — not the
			// distinguishing tier/conns — is what gets ellipsized on a narrow sidebar.
			email := teamClaudeDisplayName(a.Name)
			tierTok := teamclaude.ShortTier(a.Tier)
			if tierTok != "" && a.IsPersonalOrg() && baseCounts[teamClaudeBareEmail(a.Name)] > 1 {
				tierTok = "PER" + tierTok
			}
			conns := ""
			if a.MaxConcurrency > 0 {
				conns = fmt.Sprintf("%d/%d", a.ActiveRequests, a.MaxConcurrency)
			}

			const sep = "|" // compact, no surrounding spaces
			sepW := runewidth.StringWidth(sep)
			markerW := runewidth.StringWidth(marker)
			availForName := width - markerW

			// Reserve the conns segment (rightmost) first, then the tier segment.
			// These are short and the distinguishing info, so they win — the email
			// is truncated (with an ellipsis) to whatever's left, down to 1 col.
			connsSeg, tierSeg := "", ""
			if conns != "" {
				if w := sepW + runewidth.StringWidth(conns); availForName-w >= 1 {
					connsSeg = conns
					availForName -= w
				}
			}
			if tierTok != "" {
				if w := sepW + runewidth.StringWidth(tierTok); availForName-w >= 1 {
					tierSeg = tierTok
					availForName -= w
				}
			}

			nameText := teamClaudeTruncateName(email, availForName)

			// The active state is carried by a green LEFT marker (below), not by
			// coloring the name. Extra-usage amber (paid territory) still tints the
			// whole row so it reads as a clear heads-up.
			nameStyle := labelStyle
			if a.IsActiveExtraUsage {
				nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd93d")).Bold(true)
			}
			// Marker color precedence: extra-usage amber wins, then an actively-
			// serving (non-rate-limited) account goes green so multiple live accounts
			// stand out at a glance.
			markerStyle := nameStyle
			switch {
			case a.IsActiveExtraUsage:
				// keep amber (already set via nameStyle)
			case active && !a.RateLimited():
				markerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6bcb77")).Bold(true)
			}

			// Dim separators / tier; conns green when actively serving (amber under
			// extra usage), dim otherwise. dividerStyle carries the (dim) divider
			// color when configured; it renders plain when not, which is fine.
			sepStyle := dividerStyle
			var b strings.Builder
			b.WriteString(markerStyle.Render(marker))
			b.WriteString(nameStyle.Render(nameText))
			if connsSeg != "" {
				connStyle := labelStyle
				// Green only when more than one request is genuinely in flight.
				if a.ActiveRequests > 1 {
					connStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6bcb77"))
				}
				if a.IsActiveExtraUsage {
					connStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffd93d"))
				}
				b.WriteString(sepStyle.Render(sep) + connStyle.Render(connsSeg))
			}
			if tierSeg != "" {
				b.WriteString(sepStyle.Render(sep) + nameStyle.Render(tierSeg))
			}
			result.WriteString(b.String() + "\n")

			// Extra-usage budget on its OWN line under the name so it stays
			// visible even on a narrow sidebar (where a right-aligned suffix would
			// have been the first thing to get dropped). Indented to align with
			// where the name text starts.
			if a.HasExtraUsageBudget() && a.IsActiveExtraUsage {
				rem := 0.0
				if a.ExtraUsageRemaining != nil {
					rem = *a.ExtraUsageRemaining
				}
				budgetText := fmt.Sprintf("$%s/$%s", formatExtraDollars(rem), formatExtraDollars(*a.ExtraUsageLimit))
				indent := strings.Repeat(" ", markerW)
				// Spend indicator: black text on a cyan background so it reads as a
				// distinct "money" chip. Low headroom escalates the BACKGROUND
				// (cyan -> amber -> red) while keeping black text legible on all
				// three; the active extra-usage row gets a bold cyan chip.
				const chipFg = "#000000"
				chipBg := "#36d6e7" // cyan — normal headroom
				if *a.ExtraUsageLimit > 0 {
					switch frac := rem / *a.ExtraUsageLimit; {
					case frac < 0.15:
						chipBg = "#ff6b6b" // red — almost spent
					case frac < 0.40:
						chipBg = "#ffd93d" // amber — getting low
					}
				}
				bStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(chipFg)).
					Background(lipgloss.Color(chipBg))
				if a.IsActiveExtraUsage {
					bStyle = bStyle.Bold(true)
				}
				// Truncate to fit width (indent + text); the leading "$" makes the
				// purpose obvious even if the trailing limit is cut.
				avail := width - runewidth.StringWidth(indent)
				if avail < 1 {
					avail = 1
				}
				if runewidth.StringWidth(budgetText) > avail {
					budgetText = runewidth.Truncate(budgetText, avail, "")
				}
				result.WriteString(labelStyle.Render(indent) + bStyle.Render(budgetText) + "\n")
			}

			var cells []quotaCell
			if showSession {
				cells = append(cells, quotaCell{a.Remaining.Session, a.Quota.Unified5hReset})
			}
			if showWeekly {
				cells = append(cells, quotaCell{a.Remaining.Weekly, a.Quota.Unified7dReset})
			}
			result.WriteString(quotaLine(cells))
		}
	}

	for i := 0; i < tcCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}
	for i := 0; i < tcCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// constrainWidgetWidth ensures all lines in widget content don't exceed maxWidth
// This prevents widgets from overflowing the sidebar boundary
func constrainWidgetWidth(content string, maxWidth int) string {
	if maxWidth < 1 {
		return content
	}

	lines := strings.Split(content, "\n")
	var result strings.Builder
	hadOverflow := false

	for i, line := range lines {
		// Strip ANSI codes for width calculation (but keep them in output)
		stripped := stripAnsi(line)
		lineWidth := uniseg.StringWidth(stripped)

		if lineWidth > maxWidth {
			if !hadOverflow {
				coordinatorDebugLog.Printf("OVERFLOW DETECTED: line width %d > max %d", lineWidth, maxWidth)
				coordinatorDebugLog.Printf("  Line preview: %s", runewidth.Truncate(stripped, 50, "..."))
				hadOverflow = true
			}
			// Truncate line to maxWidth (accounting for ANSI codes)
			truncated := runewidth.Truncate(line, maxWidth, "")
			result.WriteString(truncated)
		} else {
			result.WriteString(line)
		}

		// Add newline except for last line
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}

	return result.String()
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func safeRandRange(minInclusive, maxInclusive int) int {
	if maxInclusive < minInclusive {
		if maxInclusive < 0 {
			return 0
		}
		return maxInclusive
	}
	if maxInclusive == minInclusive {
		return minInclusive
	}
	return minInclusive + rand.Intn(maxInclusive-minInclusive+1)
}

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	// Simple regex to strip ANSI escape sequences
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}

// clampSpriteX clamps a position so the sprite fits within the given width
func clampSpriteX(x int, sprite string, maxWidth int) int {
	spriteWidth := uniseg.StringWidth(sprite)
	if spriteWidth < 1 {
		spriteWidth = 1
	}
	maxX := maxWidth - spriteWidth
	if maxX < 0 {
		maxX = 0
	}
	if x < 0 {
		return x // preserve negative (hidden) positions
	}
	if x > maxX {
		return maxX
	}
	return x
}

// placeSprite adds a sprite to the map with proper position clamping
// Returns the clamped X position
func placeSprite(sprites map[int]string, x int, sprite string, maxWidth int) int {
	clampedX := clampSpriteX(x, sprite, maxWidth)
	if clampedX >= 0 && clampedX < maxWidth {
		sprites[clampedX] = sprite
	}
	return clampedX
}

// renderStatusBar creates a visual bar representation of a 0-100 value
// Uses block characters: filled (▓) and empty (░)
func renderStatusBar(value int, segments int) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	filled := (value * segments) / 100
	empty := segments - filled

	bar := ""
	for i := 0; i < filled; i++ {
		bar += "▓"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}
	return bar
}

// colorStatusBar applies color to a status bar based on the value level
// Red (<30), Yellow (30-60), Green (>60)
func colorStatusBar(bar string, value int) string {
	var color string
	if value < 30 {
		color = "#ff6b6b" // Red - critical
	} else if value < 60 {
		color = "#ffd93d" // Yellow - warning
	} else {
		color = "#6bcb77" // Green - good
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(bar)
}

// buildSpriteRow builds a row with sprites placed at their positions
// Fills remaining space with the filler character
func buildSpriteRow(sprites map[int]string, filler string, totalWidth int) string {
	var builder strings.Builder
	fillerWidth := uniseg.StringWidth(filler)
	if fillerWidth < 1 {
		fillerWidth = 1
	}

	col := 0
	for col < totalWidth {
		if sprite, hasSprite := sprites[col]; hasSprite {
			spriteWidth := uniseg.StringWidth(sprite)
			if spriteWidth < 1 {
				spriteWidth = 1
			}
			// Only place sprite if it fits within bounds
			if col+spriteWidth <= totalWidth {
				builder.WriteString(sprite)
				col += spriteWidth
			} else {
				// Doesn't fit, use filler
				builder.WriteString(filler)
				col += fillerWidth
			}
		} else {
			builder.WriteString(filler)
			col += fillerWidth
		}
	}
	return builder.String()
}

// buildAirRow builds an air row (for Y=1 or Y=2) with proper width accounting for wide emojis
// sprites is a map of column position -> sprite string
// safePlayWidth is the total width available for the row
func buildAirRow(sprites map[int]string, safePlayWidth int) string {
	return buildSpriteRow(sprites, " ", safePlayWidth)
}

// renderPetWidget renders the pet tamagotchi widget
// Layout:
//   - Divider
//   - Food icon (clickable)
//   - Divider
//   - Thought bubble
//   - Divider
//   - Play area (3 lines: high air, low air, ground)
//   - Divider
//   - Stats: hunger | happiness | life
func (c *Coordinator) renderPetWidget(width int, skipDebugBar bool) string {
	petCfg := c.config.Widgets.Pet
	if !petCfg.Enabled {
		return ""
	}

	// Reset debug-line markers; only set inside the conditional below. Without
	// this reset they'd retain stale values across renders that suppress the
	// debug bar, causing click misrouting.
	c.petLayout.DebugLine1 = 0
	c.petLayout.DebugLine2 = 0
	c.petLayout.DebugLine3 = 0
	// Reset Q&A teaser row; only set when the teaser substitution actually
	// fires below. -1 means "no question line on this render".
	c.petLayout.QuestionPromptLine = -1

	style := petCfg.Style
	if style == "" {
		style = "emoji"
	}
	sprites, ok := petSpritesByStyle[style]
	if !ok {
		sprites = petSpritesByStyle["emoji"]
	}

	// Apply config icon overrides (config takes priority over style preset)
	icons := petCfg.Icons
	if icons.Idle != "" {
		sprites.Idle = icons.Idle
	}
	if icons.Walking != "" {
		sprites.Walking = icons.Walking
	}
	if icons.Jumping != "" {
		sprites.Jumping = icons.Jumping
	}
	if icons.Playing != "" {
		sprites.Playing = icons.Playing
	}
	if icons.Eating != "" {
		sprites.Eating = icons.Eating
	}
	if icons.Sleeping != "" {
		sprites.Sleeping = icons.Sleeping
	}
	if icons.Happy != "" {
		sprites.Happy = icons.Happy
	}
	if icons.Hungry != "" {
		sprites.Hungry = icons.Hungry
	}
	if icons.Yarn != "" {
		sprites.Yarn = icons.Yarn
	}
	if icons.Food != "" {
		sprites.Food = icons.Food
	}
	if icons.Poop != "" {
		sprites.Poop = icons.Poop
	}
	if icons.Thought != "" {
		sprites.Thought = icons.Thought
	}
	if icons.Heart != "" {
		sprites.Heart = icons.Heart
	}
	if icons.Life != "" {
		sprites.Life = icons.Life
	}
	if icons.HungerIcon != "" {
		sprites.HungerIcon = icons.HungerIcon
	}
	if icons.HappyIcon != "" {
		sprites.HappyIcon = icons.HappyIcon
	}
	if icons.SadIcon != "" {
		sprites.SadIcon = icons.SadIcon
	}
	if icons.Ground != "" {
		sprites.Ground = icons.Ground
	}
	if icons.Blood != "" {
		sprites.Blood = icons.Blood
	}

	petSprite := sprites.Idle
	switch c.pet.State {
	case "walking":
		petSprite = sprites.Walking
	case "jumping":
		petSprite = sprites.Jumping
	case "playing":
		petSprite = sprites.Playing
	case "eating":
		petSprite = sprites.Eating
	case "sleeping":
		petSprite = sprites.Sleeping
	case "happy":
		petSprite = sprites.Happy
	case "hungry":
		petSprite = sprites.Hungry
	case "shooting":
		petSprite = sprites.Idle
	case "dead":
		petSprite = sprites.Dead
	}
	// Dead overrides everything
	if c.pet.IsDead {
		petSprite = sprites.Dead
	} else if c.pet.Hunger < 15 {
		petSprite = sprites.Hungry
	}
	if petSprite == "" {
		petSprite = "🐱"
	}

	var result strings.Builder
	currentLine := 0 // Track line offsets for click detection

	for i := 0; i < petCfg.MarginTop; i++ {
		result.WriteString("\n")
		currentLine++
	}

	// Divider style - fall back to sidebar's InactiveFg for visibility
	divider := petCfg.Divider
	if divider == "" {
		divider = "─"
	}
	dividerFg := c.getInactiveTextColorWithFallback(petCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	renderDivider := func() string {
		dividerWidth := uniseg.StringWidth(divider)
		if dividerWidth > 0 {
			repeatCount := (width - 1) / dividerWidth
			if repeatCount < 1 {
				repeatCount = 1
			}
			return dividerStyle.Render(strings.Repeat(divider, repeatCount)) + "\n"
		}
		return ""
	}

	// Top divider
	result.WriteString(renderDivider())
	currentLine++

	// Food icon (clickable to drop food) - track line for click detection
	c.petLayout.FeedLine = currentLine
	petFg := c.getInactiveTextColorWithFallback(petCfg.Fg)
	playStyle := lipgloss.NewStyle()
	if petFg != "" {
		playStyle = playStyle.Foreground(lipgloss.Color(petFg))
	}
	if petCfg.Bg != "" && !strings.EqualFold(petCfg.Bg, "transparent") {
		playStyle = playStyle.Background(lipgloss.Color(petCfg.Bg))
	}
	foodStyle := lipgloss.NewStyle()
	if petFg != "" {
		foodStyle = foodStyle.Foreground(lipgloss.Color(petFg))
	}
	// Match playStyle's bg so the "Feed" line doesn't fall through to the
	// terminal's underlying bg -- that caused visible seams after theme
	// flips where trailing cells used the old theme's bg while the rest of
	// the pet widget painted with the new theme.
	if petCfg.Bg != "" && !strings.EqualFold(petCfg.Bg, "transparent") {
		foodStyle = foodStyle.Background(lipgloss.Color(petCfg.Bg))
	} else if bg := c.GetTerminalBg(); bg != "" {
		foodStyle = foodStyle.Background(lipgloss.Color(bg))
	}
	foodIcon := zone.Mark("pet:drop_food", foodStyle.Render(sprites.Food+" Feed"))
	result.WriteString(foodIcon + "\n")
	currentLine++

	// Divider
	result.WriteString(renderDivider())
	currentLine++

	for i := 0; i < petCfg.PaddingTop; i++ {
		result.WriteString("\n")
		currentLine++
	}

	playWidth := width
	if playWidth < 5 {
		playWidth = 5
	}

	// Thought bubble with marquee
	thought := c.pet.LastThought
	if thought == "" {
		thought = "chillin'."
	}
	thoughtStyle := lipgloss.NewStyle()
	if petFg != "" {
		thoughtStyle = thoughtStyle.Foreground(lipgloss.Color(petFg))
	}
	maxThoughtWidth := playWidth - 4
	if maxThoughtWidth < 5 {
		maxThoughtWidth = 5
	}

	// Decide whether to substitute the teaser thought for the Q&A consent
	// click region this frame. The teaser is non-scrolling so the click row
	// stays stable across renders, and it never appears when QA is opted
	// out (defensive double-gate; PickQuestion already filters this case).
	teaserActive := false
	if c.pet.PendingQuestion != nil && !c.pet.QAOptedOut {
		n := c.config.Widgets.Pet.QA.TeaserEveryNThoughts
		if n > 0 {
			// Cadence: split AnimFrame into ~5s blocks (~50 frames at 10fps).
			// In a window of N blocks, the first block shows the teaser,
			// the remaining N-1 blocks show the normal thought. Independent
			// of thoughtSpeed/LLM tick so the teaser appears predictably
			// even when LLM thoughts are off.
			const teaserBlockFrames = 50
			block := c.pet.AnimFrame / teaserBlockFrames
			if mod := block % n; mod == 0 {
				teaserActive = true
			}
		}
	}

	var thoughtLine string
	if teaserActive {
		// Non-scrolling teaser: emit a stable click region tagged for the
		// click handler to recognise. handlePetWidgetClick prefers row index
		// over zone marker for the pet widget so we set QuestionPromptLine
		// on the layout struct as the source of truth.
		//
		// Width-conditional: the full teaser is 25 cells which overflows
		// the mobile profile's ~10-col thought area, leaving the user
		// staring at "🤔 mind if" with no obvious affordance. Pick the
		// widest variant whose FULL rendered width (sprite + space + body)
		// fits in maxThoughtWidth, so the sidebar's overflow truncator
		// doesn't clip the trailing BubbleZone marker and silently break
		// the click target.
		prefixWidth := uniseg.StringWidth(sprites.Thought) + 1 // sprite + " "
		budget := maxThoughtWidth - prefixWidth
		var teaserBody string
		switch {
		case budget >= 25: // "🤔 mind if I ask? (click)"
			teaserBody = "🤔 mind if I ask? (click)"
		case budget >= 19: // "🤔 ask me? (click)"
			teaserBody = "🤔 ask me? (click)"
		case budget >= 13: // "🤔 ask? click"
			teaserBody = "🤔 ask? click"
		case budget >= 7: // "🤔 ask?"
			teaserBody = "🤔 ask?"
		default:
			teaserBody = "🤔?"
		}
		teaserText := sprites.Thought + " " + teaserBody
		thoughtLine = zone.Mark("pet:question_prompt", teaserText)
		c.petLayout.QuestionPromptLine = currentLine
	} else {
		thoughtWidth := uniseg.StringWidth(thought)
		displayThought := thought
		if thoughtWidth > maxThoughtWidth {
			scrollText := thought + "   " + thought
			scrollRunes := []rune(scrollText)
			startIdx := c.pet.ThoughtScroll % len(scrollRunes)
			visible := ""
			visWidth := 0
			for i := startIdx; i < len(scrollRunes) && visWidth < maxThoughtWidth; i++ {
				r := scrollRunes[i]
				rw := runewidth.RuneWidth(r)
				if visWidth+rw > maxThoughtWidth {
					break // Don't add partial wide char
				}
				visible += string(r)
				visWidth += rw
			}
			displayThought = visible
		}
		// Add "asking" request bubbles when needs are critical
		var requestBubble string
		if c.pet.IsDead {
			requestBubble = "💀"
		} else if len(c.pet.PoopPositions) > 0 {
			requestBubble = "🧹?" // Asking for cleanup
		} else if c.pet.Hunger < 10 {
			requestBubble = "🍖?" // Asking for food (urgent)
		} else if c.pet.Happiness < 20 {
			requestBubble = "🧶?" // Asking for play (urgent)
		} else if c.pet.Hunger < 20 {
			requestBubble = "🍖" // Would like food
		} else if c.pet.Happiness < 40 {
			requestBubble = "🧶" // Would like play
		}

		thoughtLine = sprites.Thought + " " + displayThought
		if requestBubble != "" {
			// Show request bubble at the end of thought line
			thoughtLine = requestBubble + " " + thoughtLine
		}
	}
	result.WriteString(thoughtStyle.Render(thoughtLine) + "\n")
	currentLine++

	// Divider before play area
	result.WriteString(renderDivider())
	currentLine++

	// Get positions, clamped to width accounting for sprite widths
	// Use width - 1 to match divider width for visual consistency
	safePlayWidth := playWidth - 1
	c.petLayout.PlayWidth = safePlayWidth

	// === ADVENTURE MODE RENDERING ===
	// If adventure is active, render adventure play area instead of normal one
	if c.pet.Adventure.Active {
		highAirLine, lowAirLine, groundContent := c.renderAdventurePlayArea(safePlayWidth, petSprite, sprites)

		c.petLayout.HighAirLine = currentLine
		result.WriteString(zone.Mark("pet:air_high", playStyle.Render(highAirLine)) + "\n")
		currentLine++

		c.petLayout.LowAirLine = currentLine
		result.WriteString(zone.Mark("pet:air_low", playStyle.Render(lowAirLine)) + "\n")
		currentLine++

		c.petLayout.GroundLine = currentLine
		groundLine := zone.Mark("pet:ground", playStyle.Render(groundContent))
		result.WriteString(groundLine + "\n")
		currentLine++

		// Divider before stats
		result.WriteString(renderDivider())
		currentLine++

		// Stats line: hunger | happiness with visual bars
		statusStyle := lipgloss.NewStyle()
		if petFg != "" {
			statusStyle = statusStyle.Foreground(lipgloss.Color(petFg))
		}
		hungerIcon := sprites.HungerIcon
		happyIcon := sprites.HappyIcon
		if c.pet.Happiness < 30 {
			happyIcon = sprites.SadIcon
		}
		hungerBar := renderStatusBar(c.pet.Hunger, 5)
		happyBar := renderStatusBar(c.pet.Happiness, 5)
		hungerBarStyled := colorStatusBar(hungerBar, c.pet.Hunger)
		happyBarStyled := colorStatusBar(happyBar, c.pet.Happiness)
		statusLine := fmt.Sprintf("%s%s %s%s", hungerIcon, hungerBarStyled, happyIcon, happyBarStyled)
		result.WriteString(statusStyle.Render(statusLine) + "\n")
		currentLine++

		c.petLayout.WidgetHeight = currentLine

		for i := 0; i < petCfg.PaddingBot; i++ {
			result.WriteString("\n")
		}
		if petCfg.DividerBottom != "" {
			dividerStyle := lipgloss.NewStyle()
			if dividerFg != "" {
				dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
			}
			divWidth := uniseg.StringWidth(petCfg.DividerBottom)
			if divWidth > 0 {
				repeatCount := (width - 1) / divWidth
				if repeatCount < 1 {
					repeatCount = 1
				}
				result.WriteString(dividerStyle.Render(strings.Repeat(petCfg.DividerBottom, repeatCount)) + "\n")
			}
		}
		for i := 0; i < petCfg.MarginBot; i++ {
			result.WriteString("\n")
		}
		return result.String()
	}

	// === NORMAL PLAY AREA RENDERING ===
	// Get raw positions
	petX := c.pet.Pos.X
	if petX < 0 {
		petX = safePlayWidth / 2
	}
	petY := c.pet.Pos.Y

	dragonX := c.pet.DragonPos.X
	if c.pet.DragonState == "" {
		dragonX = -1
	}
	dragonY := c.pet.DragonPos.Y
	dragonSprite := "🐉"
	if c.pet.DragonState == "sleeping" {
		dragonSprite = "💤" // Share the zzz!
	}

	yarnX := c.pet.YarnPos.X
	yarnY := c.pet.YarnPos.Y

	foodX := c.pet.FoodItem.X
	foodY := c.pet.FoodItem.Y

	// Clamp all positions to fit their sprites within bounds
	petX = clampSpriteX(petX, petSprite, safePlayWidth)
	dragonX = clampSpriteX(dragonX, dragonSprite, safePlayWidth)
	yarnX = clampSpriteX(yarnX, sprites.Yarn, safePlayWidth)
	foodX = clampSpriteX(foodX, sprites.Food, safePlayWidth)

	// Anti-occlusion: prevent 2-width emojis from completely hiding each other when adjacent
	if petY == 0 && dragonY == 0 {
		if dragonX == petX+1 || dragonX == petX-1 {
			// Push the dragon away so both are visible
			if dragonX > petX {
				dragonX++
			} else {
				dragonX--
			}
			dragonX = clampSpriteX(dragonX, dragonSprite, safePlayWidth)
		}
	}

	// Line 1: High air (Y=2) - build with proper width accounting
	coordinatorDebugLog.Printf("Pet render: petX=%d, petY=%d, yarnX=%d, yarnY=%d, foodX=%d, foodY=%d, safePlayWidth=%d, petSprite=%q",
		petX, petY, yarnX, yarnY, foodX, foodY, safePlayWidth, petSprite)
	highAirSprites := make(map[int]string)

	// Add scrolling clouds based on AnimFrame (passive wind)
	for i := 0; i < safePlayWidth; i++ {
		// Parallax effect: background moves slowly over time
		bgWorldX := i + (c.pet.AnimFrame / 15)

		cloudMod1 := ((bgWorldX % 15) + 15) % 15
		cloudMod2 := ((bgWorldX % 27) + 27) % 27

		if cloudMod1 == 0 {
			highAirSprites[i] = "☁️"
		} else if cloudMod2 == 0 {
			highAirSprites[i] = "⛅"
		}
	}

	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 2 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			highAirSprites[item.Pos.X] = item.Emoji
		}
	}
	if petY >= 2 && petX >= 0 && petX < safePlayWidth {
		highAirSprites[petX] = petSprite
	}
	if dragonY >= 2 && dragonX >= 0 && dragonX < safePlayWidth {
		highAirSprites[dragonX] = dragonSprite
	}
	if yarnY >= 2 && yarnX >= 0 && yarnX < safePlayWidth {
		highAirSprites[yarnX] = sprites.Yarn
	}
	if foodY >= 2 && foodX >= 0 && foodX < safePlayWidth {
		highAirSprites[foodX] = sprites.Food
	}
	highAirLine := buildAirRow(highAirSprites, safePlayWidth)
	highAirWidth := uniseg.StringWidth(highAirLine)
	coordinatorDebugLog.Printf("High air: sprites=%v, line=%q (len=%d, runewidth=%d)", highAirSprites, highAirLine, len(highAirLine), highAirWidth)
	if highAirWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: High air row width mismatch! expected=%d, actual=%d", safePlayWidth, highAirWidth)
	}
	c.petLayout.HighAirLine = currentLine
	result.WriteString(zone.Mark("pet:air_high", playStyle.Render(highAirLine)) + "\n")
	currentLine++

	// Line 2: Low air (Y=1) - build with proper width accounting
	lowAirSprites := make(map[int]string)
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 1 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			lowAirSprites[item.Pos.X] = item.Emoji
		}
	}
	if petY == 1 && petX >= 0 && petX < safePlayWidth {
		lowAirSprites[petX] = petSprite
	}
	if dragonY == 1 && dragonX >= 0 && dragonX < safePlayWidth {
		lowAirSprites[dragonX] = dragonSprite
	}
	if yarnY == 1 && yarnX >= 0 && yarnX < safePlayWidth {
		lowAirSprites[yarnX] = sprites.Yarn
	}
	if foodY == 1 && foodX >= 0 && foodX < safePlayWidth {
		lowAirSprites[foodX] = sprites.Food
	}
	lowAirLine := buildAirRow(lowAirSprites, safePlayWidth)
	lowAirWidth := uniseg.StringWidth(lowAirLine)
	coordinatorDebugLog.Printf("Low air: sprites=%v, line=%q (len=%d, runewidth=%d)", lowAirSprites, lowAirLine, len(lowAirLine), lowAirWidth)
	if lowAirWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: Low air row width mismatch! expected=%d, actual=%d", safePlayWidth, lowAirWidth)
	}
	c.petLayout.LowAirLine = currentLine
	result.WriteString(zone.Mark("pet:air_low", playStyle.Render(lowAirLine)) + "\n")
	currentLine++

	// Line 3: Ground (Y=0) - single clickable zone, action determined by click position
	// Build ground row with proper width accounting for wide emojis
	groundChar := "·"
	if len(sprites.Ground) > 0 {
		groundChar = sprites.Ground
	}
	groundCharWidth := uniseg.StringWidth(groundChar)
	if groundCharWidth < 1 {
		groundCharWidth = 1
	}

	// Map of positions to sprites (position -> sprite string)
	// Each position represents a display column, not a rune slot
	groundSprites := make(map[int]string)

	// Add static ground texture based on screen position
	for i := 0; i < safePlayWidth; i++ {
		worldX := i

		groundMod1 := ((worldX % 9) + 9) % 9
		groundMod2 := ((worldX % 14) + 14) % 14

		if groundMod1 == 0 {
			groundSprites[i] = "."
		} else if groundMod2 == 0 {
			groundSprites[i] = "_"
		}
	}

	// Place floating items
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 0 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			groundSprites[item.Pos.X] = item.Emoji
		}
	}

	// Place yarn
	if yarnY == 0 && yarnX >= 0 && yarnX < safePlayWidth {
		groundSprites[yarnX] = sprites.Yarn
	}

	// Place food
	if foodY == 0 && foodX >= 0 && foodX < safePlayWidth {
		groundSprites[foodX] = sprites.Food
	}

	// Place poops (clamped to fit within width)
	for _, poopX := range c.pet.PoopPositions {
		placeSprite(groundSprites, poopX, sprites.Poop, safePlayWidth)
	}

	// Place mouse (only if present - MousePos.X >= 0 means mouse exists)
	if c.pet.MousePos.X >= 0 {
		placeSprite(groundSprites, c.pet.MousePos.X, sprites.Mouse, safePlayWidth)
	}

	// Place cat on top (overwrites anything at that position)
	// When sleeping, cat curls up in bottom left corner with zzz
	if petY == 0 {
		if c.pet.State == "sleeping" {
			placeSprite(groundSprites, 0, "💤", safePlayWidth)
		} else {
			placeSprite(groundSprites, petX, petSprite, safePlayWidth)
		}
	}

	// Place dragon on top
	if dragonY == 0 {
		placeSprite(groundSprites, dragonX, dragonSprite, safePlayWidth)
	}

	// Build the ground row using helper
	c.petLayout.GroundLine = currentLine
	groundContent := buildSpriteRow(groundSprites, groundChar, safePlayWidth)
	actualWidth := uniseg.StringWidth(groundContent)
	coordinatorDebugLog.Printf("Ground: width=%d, content=%q (len=%d bytes, runewidth=%d)",
		safePlayWidth, groundContent, len(groundContent), actualWidth)
	if actualWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: Ground row width mismatch! expected=%d, actual=%d", safePlayWidth, actualWidth)
	}
	groundLine := zone.Mark("pet:ground", playStyle.Render(groundContent))
	result.WriteString(groundLine + "\n")
	currentLine++

	// Divider before stats
	result.WriteString(renderDivider())
	currentLine++

	// Stats line: hunger | happiness with visual bars
	statusStyle := lipgloss.NewStyle()
	if petFg != "" {
		statusStyle = statusStyle.Foreground(lipgloss.Color(petFg))
	}
	hungerIcon := sprites.HungerIcon
	happyIcon := sprites.HappyIcon
	if c.pet.Happiness < 30 {
		happyIcon = sprites.SadIcon
	}

	// Visual status bars (5 segments each)
	hungerBar := renderStatusBar(c.pet.Hunger, 5)
	happyBar := renderStatusBar(c.pet.Happiness, 5)

	// Color bars based on level (red if critical, yellow if low, green if good)
	hungerBarStyled := colorStatusBar(hungerBar, c.pet.Hunger)
	happyBarStyled := colorStatusBar(happyBar, c.pet.Happiness)

	statusLine := fmt.Sprintf("%s%s %s%s", hungerIcon, hungerBarStyled, happyIcon, happyBarStyled)
	result.WriteString(statusStyle.Render(statusLine) + "\n")
	currentLine++

	// Debug bar (if enabled and not suppressed by tight-viewport escalation)
	if petCfg.DebugBar && !skipDebugBar {
		result.WriteString(renderDivider())
		currentLine++
		debugLines := c.renderDebugBar(safePlayWidth)
		for i, line := range debugLines {
			result.WriteString(line + "\n")
			switch i {
			case 0:
				c.petLayout.DebugLine1 = currentLine
			case 1:
				c.petLayout.DebugLine2 = currentLine
			case 2:
				c.petLayout.DebugLine3 = currentLine
			}
			currentLine++
		}
	}

	// Store total widget height for click detection
	c.petLayout.WidgetHeight = currentLine

	coordinatorDebugLog.Printf("Pet layout updated: Feed=%d, HighAir=%d, LowAir=%d, Ground=%d, PlayWidth=%d, Height=%d, Debug1=%d, Debug2=%d, Debug3=%d",
		c.petLayout.FeedLine, c.petLayout.HighAirLine, c.petLayout.LowAirLine,
		c.petLayout.GroundLine, c.petLayout.PlayWidth, c.petLayout.WidgetHeight,
		c.petLayout.DebugLine1, c.petLayout.DebugLine2, c.petLayout.DebugLine3)

	// Pet touch buttons removed - using touch input on pet area instead
	// Feed button at top of widget remains for touch access

	return result.String()
}

// renderDebugBar renders the 3-line debug bar for pet widget testing
// Line 1: DBG <state> H:<hunger> F:<food> [adv][slp][die][poo][mse][yrn]
// Line 2: trg:<category> [<<][>>] [H+][H-][F+][F-]
// Line 3: [q?]  (popup / overflow triggers — kept on its own row so line 1
//
//	doesn't get crowded out at narrow sidebar widths)
func (c *Coordinator) renderDebugBar(width int) []string {
	// Line 1: Status + Mode Triggers
	state := c.pet.State
	if c.pet.IsDead {
		state = "dead"
	}
	if len(state) > 4 {
		state = state[:4]
	}

	var line1 string
	if width >= 50 {
		// Full layout
		line1 = fmt.Sprintf("DBG %s H:%d F:%d [adv][slp][die][poo][mse][yrn]",
			state, c.pet.Happiness, c.pet.Hunger)
	} else if width >= 35 {
		// Compact: shorter stat names
		line1 = fmt.Sprintf("%s H%d F%d [adv][slp][die][poo]",
			state, c.pet.Happiness, c.pet.Hunger)
	} else {
		// Minimal: just state and key buttons
		line1 = fmt.Sprintf("%s [adv][slp][die]", state)
	}

	// Line 2: Thought Controls + Stats
	category := "idle"
	if c.pet.DebugThoughtIdx >= 0 && c.pet.DebugThoughtIdx < len(debugThoughtCategories) {
		category = debugThoughtCategories[c.pet.DebugThoughtIdx]
	}
	// Truncate category to 5 chars for display
	if len(category) > 5 {
		category = category[:5]
	}

	var line2 string
	if width >= 35 {
		line2 = fmt.Sprintf("trg:%s [<<][>>] [H+][H-][F+][F-]", category)
	} else {
		line2 = fmt.Sprintf("%s [<<][>>] [H+][F+]", category)
	}

	// Line 3: popup / overflow triggers. Currently just [q?] (launches the
	// pet Q&A popup) — kept on its own row so future debug buttons can pile
	// on here without re-flowing line 1.
	line3 := "[q?]"

	return []string{line1, line2, line3}
}

// handleDebugBarClick handles clicks on the debug bar
// Returns true if click was handled
func (c *Coordinator) handleDebugBarClick(clientID string, clickX, clickY int) bool {
	c.stealPetOwnership() // pet interaction on this session must persist
	layout := c.petLayout

	if clickX < 0 {
		return false
	}

	clientWidth := c.getClientWidth(clientID)
	safeWidth := clientWidth - 1
	if safeWidth < 5 {
		safeWidth = 5
	}
	lines := c.renderDebugBar(safeWidth)
	if len(lines) < 3 {
		return false
	}
	line1 := lines[0]
	line2 := lines[1]
	line3 := lines[2]

	findTokenBounds := func(line, token string) (int, int, bool) {
		idx := strings.Index(line, token)
		if idx < 0 {
			return 0, 0, false
		}
		start := uniseg.StringWidth(line[:idx])
		end := start + uniseg.StringWidth(token)
		return start, end, true
	}
	clickedToken := func(line, token string) bool {
		start, end, ok := findTokenBounds(line, token)
		return ok && clickX >= start && clickX < end
	}

	// Determine which debug line was clicked
	if clickY == layout.DebugLine1 {
		if clickedToken(line1, "[adv]") {
			coordinatorDebugLog.Printf("Debug bar: [adv] clicked, starting manual adventure")
			c.stateMu.Lock()
			c.startAdventureManual(safeWidth)
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line1, "[slp]") {
			coordinatorDebugLog.Printf("Debug bar: [slp] clicked, toggling sleep")
			c.stateMu.Lock()
			if c.pet.State == "sleeping" {
				c.pet.State = "idle"
				c.pet.LastThought = randomThought("wakeup")
			} else {
				c.pet.State = "sleeping"
				c.pet.LastThought = randomThought("sleepy")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[die]") {
			coordinatorDebugLog.Printf("Debug bar: [die] clicked, toggling death")
			c.stateMu.Lock()
			if c.pet.IsDead {
				c.pet.IsDead = false
				c.pet.State = "idle"
				c.pet.Hunger = 50
				c.pet.Happiness = 50
				c.pet.LastThought = "I'm back!"
			} else {
				c.pet.IsDead = true
				c.pet.State = "dead"
				c.pet.DeathTime = time.Now()
				c.pet.LastThought = randomThought("dead")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[poo]") {
			coordinatorDebugLog.Printf("Debug bar: [poo] clicked, spawning poop")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			poopX := safeRandRange(0, w-2)
			c.pet.PoopPositions = append(c.pet.PoopPositions, poopX)
			c.pet.LastThought = randomThought("poop") // instant fallback; LLM upgrades it
			c.triggerPetEventThought("poop")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[mse]") {
			coordinatorDebugLog.Printf("Debug bar: [mse] clicked, spawning mouse")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			c.pet.MousePos = pos2D{X: safeRandRange(0, w-2), Y: 0}
			c.pet.MouseAppearsAt = time.Time{} // Clear timer
			c.pet.LastThought = randomThought("mouse_spot")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[yrn]") {
			coordinatorDebugLog.Printf("Debug bar: [yrn] clicked, spawning yarn")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			c.pet.YarnPos = pos2D{X: safeRandRange(0, w-2), Y: 0}
			c.pet.YarnExpiresAt = time.Now().Add(30 * time.Second)
			c.pet.YarnPushCount = 0
			c.pet.LastThought = randomThought("yarn")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		return false
	}

	if clickY == layout.DebugLine2 {
		firstTokenStart := -1
		for _, tok := range []string{"[<<]", "[>>]", "[H+]", "[H-]", "[F+]", "[F-]"} {
			if s, _, ok := findTokenBounds(line2, tok); ok {
				if firstTokenStart == -1 || s < firstTokenStart {
					firstTokenStart = s
				}
			}
		}
		if firstTokenStart == -1 {
			firstTokenStart = uniseg.StringWidth(line2)
		}
		if clickX >= 0 && clickX < firstTokenStart {
			coordinatorDebugLog.Printf("Debug bar: trg clicked, triggering thought")
			c.stateMu.Lock()
			category := "idle"
			if c.pet.DebugThoughtIdx >= 0 && c.pet.DebugThoughtIdx < len(debugThoughtCategories) {
				category = debugThoughtCategories[c.pet.DebugThoughtIdx]
			}
			c.pet.LastThought = randomThought(category)
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[<<]") {
			coordinatorDebugLog.Printf("Debug bar: [<<] clicked, previous category")
			c.stateMu.Lock()
			c.pet.DebugThoughtIdx--
			if c.pet.DebugThoughtIdx < 0 {
				c.pet.DebugThoughtIdx = len(debugThoughtCategories) - 1
			}
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line2, "[>>]") {
			coordinatorDebugLog.Printf("Debug bar: [>>] clicked, next category")
			c.stateMu.Lock()
			c.pet.DebugThoughtIdx++
			if c.pet.DebugThoughtIdx >= len(debugThoughtCategories) {
				c.pet.DebugThoughtIdx = 0
			}
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line2, "[H+]") {
			coordinatorDebugLog.Printf("Debug bar: [H+] clicked")
			c.stateMu.Lock()
			c.pet.Happiness += 10
			if c.pet.Happiness > 100 {
				c.pet.Happiness = 100
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[H-]") {
			coordinatorDebugLog.Printf("Debug bar: [H-] clicked")
			c.stateMu.Lock()
			c.pet.Happiness -= 10
			if c.pet.Happiness < 0 {
				c.pet.Happiness = 0
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[F+]") {
			coordinatorDebugLog.Printf("Debug bar: [F+] clicked")
			c.stateMu.Lock()
			c.pet.Hunger += 10
			if c.pet.Hunger > 100 {
				c.pet.Hunger = 100
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[F-]") {
			coordinatorDebugLog.Printf("Debug bar: [F-] clicked")
			c.stateMu.Lock()
			c.pet.Hunger -= 10
			if c.pet.Hunger < 0 {
				c.pet.Hunger = 0
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		return false
	}

	if clickY == layout.DebugLine3 {
		if clickedToken(line3, "[q?]") {
			coordinatorDebugLog.Printf("Debug bar: [q?] clicked, opening Q&A popup")
			// Ensure a question is set, then open the popup directly so the
			// debug button is a one-click path (the teaser-armed UX is still
			// available the normal way for users without the debug bar).
			c.forceQuestionTeaser()
			c.launchQuestionPopup(clientID)
			return true
		}
		return false
	}

	return false
}

// forceQuestionTeaser is the [q?] debug-button action: ensure a PendingQuestion
// is set on the pet and reset the teaser cadence so the consent bubble
// ("🤔 mind if I ask? (click)") appears on the very next render rather than
// waiting for the natural cadence window. Clicking the teaser then opens the
// popup, matching the organic flow.
//
// Bypasses the cooldown that PickQuestion normally enforces — this is a debug
// affordance, the whole point is that it fires now.
func (c *Coordinator) forceQuestionTeaser() {
	now := time.Now()
	c.stateMu.Lock()
	// Drop the cooldown so PickQuestion can produce a fresh question even
	// if one was just answered. We don't touch QAOptedOut / QA.Disabled —
	// those represent a user/config decision and the popup binary handles
	// the empty case gracefully if PickQuestion still returns nil.
	c.pet.QuestionCooldown = time.Time{}
	if c.pet.PendingQuestion == nil {
		wire := snapshotPetQAForWire(&c.pet)
		if picked := PickQuestion(&wire, c.config.Widgets.Pet.QA, now); picked != nil {
			wire.PendingQuestion = picked
			applyPetQAFromWire(&c.pet, &wire)
		}
	}
	// Reset AnimFrame so the teaser cadence (block = AnimFrame/50, fires
	// when block % TeaserEveryNThoughts == 0) lands on the teaser block
	// immediately rather than potentially mid-cycle.
	c.pet.AnimFrame = 0
	petSnap := c.pet
	pending := c.pet.PendingQuestion
	c.stateMu.Unlock()
	if pending == nil {
		coordinatorDebugLog.Printf("forceQuestionTeaser: no question available (QA disabled, opted out, or pool exhausted)")
		return
	}
	savePetStateData(petSnap)
	coordinatorDebugLog.Printf("forceQuestionTeaser: teaser armed (question id=%q)", pending.ID)
}

// renderSmallButton renders a single-line flat button with background color.
func renderSmallButton(width int, label string, bgColor, fgColor string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Foreground(lipgloss.Color(fgColor)).
		Bold(true).
		Width(width).
		Align(lipgloss.Center).
		Render(label)
}

// renderPinnedActionButtons renders New Tab, New Group, Close, and Touch Mode toggle
// buttons in the pinned area, above the resize buttons.
// In large/touch mode: 3-line bordered buttons. In small mode: single-line flat buttons.
func (c *Coordinator) renderPinnedActionButtons(width int) string {
	if width < 1 {
		width = 1
	}
	var s strings.Builder

	// Get button colors from theme, with fallbacks
	var primaryBg, primaryFg, secondaryBg, secondaryFg string
	var destructiveBg, destructiveFg string
	if c.theme != nil {
		primaryBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#27ae60")
		primaryFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
		secondaryBg = c.getThemeColor(c.theme.ButtonSecondaryBg, "#9b59b6")
		secondaryFg = c.getThemeColor(c.theme.ButtonSecondaryFg, "#ffffff")
		destructiveBg = c.getThemeColor(c.theme.ButtonDestructiveBg, "#e74c3c")
		destructiveFg = c.getThemeColor(c.theme.ButtonDestructiveFg, "#ffffff")
	} else {
		primaryBg, primaryFg = "#27ae60", "#ffffff"
		secondaryBg, secondaryFg = "#9b59b6", "#ffffff"
		destructiveBg, destructiveFg = "#e74c3c", "#ffffff"
	}

	// New Tab + New Group side by side
	if c.config.Sidebar.NewTabButton && c.config.Sidebar.NewGroupButton {
		leftWidth := width / 2
		rightWidth := width - leftWidth
		leftBtn := renderSmallButton(leftWidth, "+ Tab", primaryBg, primaryFg)
		rightBtn := renderSmallButton(rightWidth, "+ Group", secondaryBg, secondaryFg)
		left := zone.Mark("sidebar:new_tab", leftBtn)
		right := zone.Mark("sidebar:new_group", rightBtn)
		s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n")
	} else if c.config.Sidebar.NewTabButton {
		btn := renderSmallButton(width, "+ New Tab", primaryBg, primaryFg)
		s.WriteString(zone.Mark("sidebar:new_tab", btn) + "\n")
	} else if c.config.Sidebar.NewGroupButton {
		btn := renderSmallButton(width, "+ New Group", secondaryBg, secondaryFg)
		s.WriteString(zone.Mark("sidebar:new_group", btn) + "\n")
	}

	// Close Tab button
	if c.config.Sidebar.CloseButton {
		btn := renderSmallButton(width, "x Close Tab", destructiveBg, destructiveFg)
		s.WriteString(zone.Mark("sidebar:close_tab", btn) + "\n")
	}

	return s.String()
}

// renderSidebarResizeButtons renders resize buttons at bottom of sidebar.
func (c *Coordinator) renderSidebarResizeButtons(width int) string {
	if width < 1 {
		width = 1
	}

	var destructiveBg, destructiveFg, primaryBg, primaryFg string
	if c.theme != nil {
		destructiveBg = c.getThemeColor(c.theme.ButtonDestructiveBg, "#e74c3c")
		destructiveFg = c.getThemeColor(c.theme.ButtonDestructiveFg, "#ffffff")
		primaryBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#27ae60")
		primaryFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
	} else {
		destructiveBg, destructiveFg = "#e74c3c", "#ffffff"
		primaryBg, primaryFg = "#27ae60", "#ffffff"
	}

	leftWidth := width / 2
	rightWidth := width - leftWidth

	shrinkBtn := renderSmallButton(leftWidth, "<", destructiveBg, destructiveFg)
	growBtn := renderSmallButton(rightWidth, ">", primaryBg, primaryFg)

	left := zone.Mark("sidebar:shrink", shrinkBtn)
	right := zone.Mark("sidebar:grow", growBtn)
	combined := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	return combined + "\n"
}

func (c *Coordinator) renderNavButtons(width int) string {
	if width < 1 {
		width = 1
	}

	var prevBg, nextBg, navFg string
	if c.theme != nil {
		prevBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#2563eb")
		nextBg = c.getThemeColor(c.theme.ButtonSecondaryBg, "#16a34a")
		navFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
	} else {
		prevBg, nextBg, navFg = "#2563eb", "#16a34a", "#ffffff"
	}

	leftWidth := width / 2
	rightWidth := width - leftWidth

	prevBtn := renderSmallButton(leftWidth, "▲", prevBg, navFg)
	nextBtn := renderSmallButton(rightWidth, "▼", nextBg, navFg)

	navLeft := zone.Mark("sidebar:prev_window", prevBtn)
	navRight := zone.Mark("sidebar:next_window", nextBtn)
	return lipgloss.JoinHorizontal(lipgloss.Top, navLeft, navRight) + "\n\n"
}
func (c *Coordinator) getThemeColor(themeColor, fallback string) string {
	if c.theme != nil && themeColor != "" {
		return themeColor
	}
	return fallback
}

// getClientWidth returns the width for a specific client, with fallback to lastWidth
func (c *Coordinator) getClientWidth(clientID string) int {
	c.clientWidthsMu.RLock()
	width, ok := c.clientWidths[clientID]
	c.clientWidthsMu.RUnlock()
	if !ok || width < 10 {
		width = c.lastWidth
		if width < 10 {
			width = 25
		}
	}
	return width
}

// RemoveClient cleans up state for a disconnected client
func (c *Coordinator) RemoveClient(clientID string) {
	c.clientWidthsMu.Lock()
	delete(c.clientWidths, clientID)
	if c.clientHeights != nil {
		delete(c.clientHeights, clientID)
	}
	c.clientWidthsMu.Unlock()
	c.clientProfileMu.Lock()
	delete(c.clientProfile, clientID)
	c.clientProfileMu.Unlock()
	coordinatorDebugLog.Printf("Removed client: %s (remaining: %d)", clientID, len(c.clientWidths))
}

// computeProfile classifies a client terminal width as "phone" or "desktop".
// The threshold (100) matches the phone render gate in RenderHeaderForClient
// so that clients that render with the touch layout also route through the
// phone-profile code paths (window-header spawning, auto-unhide, etc.).
func (c *Coordinator) computeProfile(width int) string {
	if width < 100 {
		return "phone"
	}
	return "desktop"
}

// isHeaderClient reports whether the client key identifies either kind of
// header renderer (window-header or pane-header). Used by code paths that
// need to treat headers specially (focus handoff, profile routing).
func isHeaderClient(clientID string) bool {
	kind := daemon.KindOf(clientID)
	return kind == daemon.TargetWindowHeader || kind == daemon.TargetPaneHeader
}

// SetClientProfile records the profile classification for a client.
func (c *Coordinator) SetClientProfile(clientID string, profile string) {
	c.clientProfileMu.Lock()
	if c.clientProfile == nil {
		c.clientProfile = make(map[string]string)
	}
	c.clientProfile[clientID] = profile
	c.clientProfileMu.Unlock()
}

// ActiveClientProfile returns the profile of the active client.
// It computes the profile directly from the active terminal width.
func (c *Coordinator) ActiveClientProfile() string {
	acw := int(c.activeClientWidth.Load())
	if acw <= 0 {
		return "desktop"
	}
	return c.computeProfile(acw)
}

// SetActiveClient caches the full elected-client snapshot (TTY, Width,
// Height, Profile) so RenderPayload.ActiveClient can be populated without
// every render site touching the elector. Call from the geometry tick
// whenever the election result changes.
//
// Routes through maybeScheduleProfileTransition so a width that crosses the
// phone/desktop boundary triggers the same debounced PROFILE_TRANSITION_FIRE
// as SetActiveClientWidth. This is what restores a stashed sidebar after a
// tmux reattach (client-attached only nudges a refresh; the actual profile
// flip is observed here in the geometry tick).
func (c *Coordinator) SetActiveClient(ac daemon.ActiveClient) {
	prevWidth := int(c.activeClientWidth.Load())
	// Store a copy so callers can't mutate our state through the original.
	stored := ac
	c.activeClient.Store(&stored)
	if ac.Width > 0 {
		c.activeClientWidth.Store(int64(ac.Width))
		c.maybeScheduleProfileTransition(prevWidth, ac.Width)
	}
}

// ActiveClientSnapshot returns a copy of the current elected-client state.
// Safe to call from any goroutine.
func (c *Coordinator) ActiveClientSnapshot() daemon.ActiveClient {
	cur := c.activeClient.Load()
	var ac daemon.ActiveClient
	if cur != nil {
		ac = *cur
	}
	if ac.Profile == "" {
		// Fallback for cases where only Width has been set (e.g. legacy
		// sync paths that used SetActiveClientWidth).
		if w := int(c.activeClientWidth.Load()); w > 0 {
			ac.Width = w
			ac.Profile = c.computeProfile(w)
		} else {
			ac.Profile = "desktop"
		}
	}
	return ac
}

// autoPickContentPane focuses a non-auxiliary pane in targetWindow if the
// currently active pane is auxiliary (header/sidebar). Runs off the loop
// goroutine — see select_window in HandleInput. Each tmux call has its own
// 2s timeout; failures are logged but never escalate.
func autoPickContentPane(targetWindow string) {
	defer func() {
		if r := recover(); r != nil {
			logEvent("AUTO_PICK_PANE_PANIC target=%s err=%v", targetWindow, r)
		}
	}()

	activeCtx, activeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	activeOut, activeErr := exec.CommandContext(activeCtx, "tmux", "display-message", "-p", "-t", targetWindow,
		"#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
	activeCancel()
	if activeErr == nil {
		parts := strings.SplitN(strings.TrimSpace(string(activeOut)), "|||", 3)
		if len(parts) == 3 && !isAuxiliaryPaneCommand(parts[1]) && !isAuxiliaryPaneCommand(parts[2]) {
			return
		}
	}

	listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
	out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", targetWindow,
		"-F", "#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
	listCancel()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|||", 3)
		if len(parts) != 3 {
			continue
		}
		paneID := parts[0]
		cmd := parts[1]
		startCmd := parts[2]
		if !isAuxiliaryPaneCommand(cmd) && !isAuxiliaryPaneCommand(startCmd) {
			switchCtx, switchCancel := context.WithTimeout(context.Background(), 2*time.Second)
			exec.CommandContext(switchCtx, "tmux", "select-pane", "-t", paneID).Run()
			switchCancel()
			return
		}
	}
}

// HandleInput processes input events from renderers
// Returns true if window list refresh is needed (expensive tmux calls)
func (c *Coordinator) HandleInput(clientID string, input *daemon.InputPayload) bool {
	// Interacting with the window-header should never leave focus on that pane.
	// Clicking a tmux pane gives it focus before our renderer forwards the event,
	// so restore focus to a content pane once the action has been handled.
	if isHeaderClient(clientID) {
		defer focusContentPaneInActiveWindow()
	}
	switch input.Type {
	case "action":
		return c.handleSemanticAction(clientID, input)
	case "key":
		c.handleKeyInput(clientID, input)
		return true // key inputs might need refresh
	case "menu_select":
		c.HandleMenuSelect(clientID, input.MouseX) // MouseX repurposed as menu item index
		return true
	case "marker_picker":
		return c.handleMarkerPickerInput(input)
	case "color_picker":
		return c.handleColorPickerInput(input)
	}
	return false
}

func (c *Coordinator) handleMarkerPickerInput(input *daemon.InputPayload) bool {
	if input.PickerAction != "apply" {
		return false
	}
	return c.applyMarkerSelection(input.PickerScope, input.PickerTarget, input.PickerValue)
}

func (c *Coordinator) handleColorPickerInput(input *daemon.InputPayload) bool {
	if input.PickerAction != "apply" {
		return false
	}
	return c.applyColorPickerSelection(input.PickerScope, input.PickerTarget, input.PickerValue)
}

func (c *Coordinator) applyColorPickerSelection(scope, target, hexColor string) bool {
	hexColor = strings.TrimSpace(hexColor)
	if hexColor == "" {
		return false
	}

	if scope == "window" {
		windowID := strings.TrimSpace(target)
		if windowID == "" {
			return false
		}
		c.setWindowColor(windowID, hexColor)
		return true
	}

	if scope == "group" {
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err == nil {
			group := config.FindGroup(cfg, target)
			if group != nil {
				group.Theme.Bg = hexColor
				group.Theme.ActiveBg = ""
				group.Theme.Fg = ""
				group.Theme.ActiveFg = ""
				group.Theme.InactiveBg = ""
				group.Theme.InactiveFg = ""
				config.SaveConfig(configPath, cfg)
			}
		}
		return true
	}

	return false
}

func (c *Coordinator) openColorPicker(clientID, scope, target, title, currentColor string) {
	if c.OnSendColorPicker == nil {
		return
	}
	c.OnSendColorPicker(clientID, &daemon.ColorPickerPayload{
		Title:        title,
		Scope:        scope,
		Target:       target,
		CurrentColor: currentColor,
	})
}

// handleSemanticAction processes pre-resolved semantic actions from renderers
// Returns true if window list refresh is needed
func (c *Coordinator) handleSemanticAction(clientID string, input *daemon.InputPayload) bool {
	// Debug logging for semantic actions
	coordinatorDebugLog.Printf("handleSemanticAction: clientID=%s resolvedAction=%s target=%s", clientID, input.ResolvedAction, input.ResolvedTarget)

	// Diagnostic: log every window-header click with the pane width the
	// daemon knows about. Use to investigate region-table-vs-renderer
	// width drift (e.g. mobile + click cycling through windows). Match
	// the click x-coord against where the daemon would expect each cell.
	//
	// Augmented with delta-since-last-click and delta-since-last-new-window
	// fields to distinguish three theories for the post-`+` cycling bug:
	//   - iOS auto-repeat at same x       -> small delta_click_ms, same x
	//   - finger drift to next_window     -> small delta_click_ms, x jumped
	//   - render-driven replay            -> very small delta_click_ms (<50)
	// The since_new_window_ms field shows whether the click happened during
	// the suspected post-spawn window where bursts have been observed.
	if strings.HasPrefix(clientID, "window-header:") && strings.EqualFold(strings.TrimSpace(input.Action), "press") {
		windowID := strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
		paneWidth := 0
		if out, err := exec.Command("tmux", "display-message", "-p", "-t", input.SourcePaneID, "#{pane_width}").Output(); err == nil {
			paneWidth, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
		now := time.Now()
		deltaClickMs := int64(-1)
		if !c.lastWindowHeaderClickAt.IsZero() {
			deltaClickMs = now.Sub(c.lastWindowHeaderClickAt).Milliseconds()
		}
		sinceNewWindowMs := int64(-1)
		recentNewWindow := ""
		if !c.lastNewWindowAt.IsZero() {
			sinceNewWindowMs = now.Sub(c.lastNewWindowAt).Milliseconds()
			recentNewWindow = c.lastNewWindowID
		}
		logEvent("WINDOW_HEADER_CLICK client=%s window=%s pane=%s pane_width=%d x=%d y=%d resolved=%q delta_click_ms=%d since_new_window_ms=%d new_window_id=%s",
			clientID, windowID, input.SourcePaneID, paneWidth, input.MouseX, input.MouseY,
			input.ResolvedAction, deltaClickMs, sinceNewWindowMs, recentNewWindow)
		c.lastWindowHeaderClickAt = now

		// Drop window-header presses with an empty button. The renderer only
		// produces these for non-Left/Right/Middle mouse events (typically
		// MouseButtonNone from tmux's post-select-pane event replay into a
		// freshly-focused pane). They arrive in bursts of 3-6 at the SAME
		// coordinate within a few ms and, when the click lands on the "+"
		// region, cascade through WINDOW_HEADER_ACTION_REMAP to spawn a tower
		// of windows from one tap. Kept here as belt-and-suspenders alongside
		// the renderer-side filter (windowheader.go MouseActionPress branch)
		// so older renderers still attached to this daemon also get protected.
		if input.Button == "" {
			logEvent("WINDOW_HEADER_CLICK_DROPPED_EMPTY_BUTTON client=%s x=%d y=%d resolved=%q", clientID, input.MouseX, input.MouseY, input.ResolvedAction)
			return false
		}
	}

	if input.ResolvedAction == "" && strings.HasPrefix(clientID, "window-header:") && strings.EqualFold(strings.TrimSpace(input.Action), "press") {
		windowID := strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
		if fallback := fallbackWindowHeaderAction(windowID, input.MouseX); fallback != "" {
			input.ResolvedAction = fallback
			input.ResolvedTarget = windowID
			logEvent("WINDOW_HEADER_FALLBACK_RESOLVE client=%s x=%d y=%d action=%s", clientID, input.MouseX, input.MouseY, fallback)
		}
	}

	// Custom pet widget click detection (bypasses BubbleZone).
	// MUST run before the ResolvedAction=="" early-return below: the
	// debug bar buttons ([adv][slp][die][poo][mse][yrn]) and the
	// high/low air rows are rendered without forwarded zone regions,
	// so their clicks arrive with ResolvedAction empty.
	// handlePetWidgetClick does its own hit-testing against petLayout
	// and returns false when the click misses the widget, so it's safe
	// to invoke unconditionally for real mouse clicks.
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin && input.Button != "" {
		if handled := c.handlePetWidgetClick(clientID, input); handled {
			return false // Pet actions don't need window refresh
		}
	}

	if input.ResolvedAction == "" {
		// No action resolved - stay in sidebar (don't steal focus)
		coordinatorDebugLog.Printf("  -> No action resolved, staying in sidebar")
		return false
	}

	actionClass := "general"
	if idx := strings.Index(input.ResolvedAction, ":"); idx > 0 {
		actionClass = input.ResolvedAction[:idx]
	}
	logEvent("SEMANTIC_ACTION_CLASS class=%s action=%s client=%s target=%s", actionClass, input.ResolvedAction, clientID, input.ResolvedTarget)

	if strings.HasPrefix(input.ResolvedAction, "window_header:") {
		sourceWindow := strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
		// When the full-width phone sidebar is open, any carousel button EXCEPT the
		// hamburger must first close it (restore the stashed content) so the action
		// lands on the real window — not the stranded full-width one. The hamburger
		// is the open/close toggle and closes it via its own handler, so exclude it.
		if c.fullscreenSidebarWinID != "" && input.ResolvedAction != "window_header:hamburger" {
			c.closeFullscreenSidebar(c.fullscreenSidebarWinID, false)
		}
		status := c.NewWindowStatus()
		if status.State == "ready" && time.Since(status.Created) > 3*time.Second {
			logEvent("WINDOW_HEADER_READY_TIMEOUT action=%s source=%s ready=%s age_ms=%d", input.ResolvedAction, sourceWindow, status.WindowID, time.Since(status.Created).Milliseconds())
			c.ClearNewWindowStatus()
			status = c.NewWindowStatus()
		}
		// Remap is ONLY for the just-created-new-window flow: the user
		// clicked a header right after spawning, and we want subsequent
		// actions to land on the new window rather than its source.
		// Outside that flow, the click's source window (encoded in the
		// per-header clientID) IS the right target — querying tmux's
		// "active window" without a `-c <tty>` returns whatever client
		// tmux happens to consider current, which on multi-client
		// sessions (desktop + phone) silently retargeted clicks to the
		// wrong window.
		logEvent("WINDOW_HEADER_TRACE phase=entry action=%s client=%s source=%s state=%s ready=%s age_ms=%d", input.ResolvedAction, clientID, sourceWindow, status.State, status.WindowID, time.Since(status.Created).Milliseconds())
		if status.State == "ready" && status.WindowID != "" && sourceWindow != "" && sourceWindow != status.WindowID {
			logEvent("WINDOW_HEADER_ACTION_REMAP action=%s source=%s ready=%s", input.ResolvedAction, sourceWindow, status.WindowID)
			c.ClearNewWindowStatus()
			clientID = "window-header:" + status.WindowID
			input.ResolvedTarget = status.WindowID
			logEvent("WINDOW_HEADER_TRACE phase=remap action=%s remapped_client=%s remapped_target=%s", input.ResolvedAction, clientID, input.ResolvedTarget)
		}
	}

	switch input.ResolvedAction {
	case "select_window":
		// While the dashboard is active the sidebar lists the remembered origin
		// windows (which no longer exist). Clicking one restores everything,
		// then focuses that window (now recreated with a fresh id).
		if c.dashboardWindowID != "" {
			if _, ok := c.dashboardOrigins[input.ResolvedTarget]; ok {
				c.exitDashboardAndSelect(input.ResolvedTarget)
			} else {
				c.exitDashboard()
			}
			return true
		}
		// Tapping a tab in the full-width phone sidebar: close it (restore the
		// content of the window it was covering) and switch to the tapped window.
		if c.fullscreenSidebarWinID != "" {
			target := input.ResolvedTarget
			if win := findWindowByTarget(c.windows, input.ResolvedTarget); win != nil {
				target = win.ID
			}
			c.closeFullscreenSidebar(c.fullscreenSidebarWinID, false)
			if err := c.SelectWindow(target, "fullscreen_select_window", clientID); err != nil {
				logEvent("FULLSCREEN_SELECT_ERR target=%s err=%v", target, err)
			}
			go autoPickContentPane(target)
			return true
		}
		rawTarget := input.ResolvedTarget
		targetWindow := input.ResolvedTarget
		if win := findWindowByTarget(c.windows, input.ResolvedTarget); win != nil {
			targetWindow = win.ID
		}

		now := time.Now()
		selectKey := clientID + "|" + targetWindow
		// Loop-only dedup state (Step 5): HandleInput runs exclusively on the
		// event-loop goroutine, so no mutex is needed.
		if lastAny, ok := c.lastWindowByClient[clientID]; ok && now.Sub(lastAny) < 300*time.Millisecond {
			logEvent("SELECT_WINDOW_DEBOUNCED_CLIENT client=%s raw=%s target=%s age_ms=%d", clientID, rawTarget, targetWindow, now.Sub(lastAny).Milliseconds())
			return false
		}
		if last, ok := c.lastWindowSelect[selectKey]; ok && now.Sub(last) < 450*time.Millisecond {
			logEvent("SELECT_WINDOW_DEBOUNCED client=%s raw=%s target=%s age_ms=%d", clientID, rawTarget, targetWindow, now.Sub(last).Milliseconds())
			return false
		}
		c.lastWindowByClient[clientID] = now
		c.lastWindowSelect[selectKey] = now
		logEvent("SELECT_WINDOW client=%s raw=%s target=%s", clientID, rawTarget, targetWindow)

		if err := c.SelectWindow(targetWindow, "semantic_select_window", clientID); err != nil {
			logEvent("SELECT_WINDOW_ERR client=%s raw=%s target=%s err=%v", clientID, rawTarget, targetWindow, err)
			return false
		}

		// Content-pane auto-pick runs off the loop goroutine. The optimistic
		// render fires from the caller (handleRendererInput) using state
		// already updated by SelectWindow; nothing on the immediate response
		// path depends on which pane is focused. Up to three tmux subprocess
		// calls (display-message, list-panes, select-pane) can stack here
		// under load — keeping them on the loop directly tracked as
		// window-switch lag after the event-loop centralization.
		go autoPickContentPane(targetWindow)
		return true

	case "toggle_panes":
		// Toggle collapse/expand for panes within this window
		winIdx := input.ResolvedTarget
		// Check current collapsed state via tmux option
		out, err := exec.Command("tmux", "show-window-option", "-v", "-t", ":"+winIdx, "@tabby_collapsed").Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			// Currently collapsed -> expand (unset option)
			exec.Command("tmux", "set-window-option", "-t", ":"+winIdx, "-u", "@tabby_collapsed").Run()
		} else {
			// Currently expanded -> collapse
			exec.Command("tmux", "set-window-option", "-t", ":"+winIdx, "@tabby_collapsed", "1").Run()
		}
		return true // Trigger immediate refresh to show collapse/expand change

	case "toggle_pane_collapse":
		// Toggle collapse/expand for individual pane (from pane header button)
		// Target is the pane ID (e.g., "%5") - the CONTENT pane, not the header pane
		paneID := input.ResolvedTarget
		coordinatorDebugLog.Printf("toggle_pane_collapse: paneID=%s", paneID)
		if paneID == "" {
			coordinatorDebugLog.Printf("toggle_pane_collapse: empty paneID, returning false")
			return false
		}
		// Check if pane is currently collapsed
		out, err := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_collapsed").Output()
		isCollapsed := err == nil && strings.TrimSpace(string(out)) == "1"
		coordinatorDebugLog.Printf("toggle_pane_collapse: isCollapsed=%v (out=%q, err=%v)", isCollapsed, strings.TrimSpace(string(out)), err)

		// Minimum height for collapsed pane (1 line - tmux minimum)
		collapsedHeight := 1
		// Header panes are always 1 line tall
		headerHeight := 1

		// Get window ID for this pane so we can fix header heights after resize
		windowIDOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
		windowID := strings.TrimSpace(string(windowIDOut))

		desiredExpandHeight := 0
		if isCollapsed {
			prevHeightOut, _ := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_prev_height").Output()
			prevHeight := strings.TrimSpace(string(prevHeightOut))
			if prevHeight != "" && prevHeight != "0" {
				if n, err := strconv.Atoi(prevHeight); err == nil && n > 0 {
					desiredExpandHeight = n
				}
			}
		} else {
			// Collapse: save height and minimize content pane to 1 line
			heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
			currentHeight := strings.TrimSpace(string(heightOut))
			if currentHeight == "" {
				currentHeight = "10"
			}
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_prev_height", currentHeight).Run()
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_collapsed", "1").Run()
			exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", collapsedHeight)).Run()
		}

		// Fix layout after collapse/expand - ensure headers stay at correct height
		// and other content panes get the freed/taken space.
		if windowID != "" {
			listOut, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}:#{pane_height}").Output()
			var headerPanes []string
			var otherContentPanes []string
			paneHeights := make(map[string]int)
			for _, line := range strings.Split(string(listOut), "\n") {
				parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
				if len(parts) < 2 {
					continue
				}
				pid := parts[0]
				cmd := parts[1]
				if len(parts) >= 3 {
					if h, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
						paneHeights[pid] = h
					}
				}
				if isAuxiliaryPaneCommand(cmd) {
					headerPanes = append(headerPanes, pid)
				} else if pid != paneID {
					otherContentPanes = append(otherContentPanes, pid)
				}
			}

			// If we just collapsed, expand the other content panes to fill space
			if !isCollapsed && len(otherContentPanes) > 0 {
				// Get total window height
				winHeightOut, _ := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_height}").Output()
				totalHeight, _ := strconv.Atoi(strings.TrimSpace(string(winHeightOut)))
				if totalHeight > 0 {
					// Calculate space for other content panes:
					// total - (headers * headerHeight) - collapsedHeight
					numHeaders := len(headerPanes)
					availableForContent := totalHeight - (numHeaders * headerHeight) - collapsedHeight
					if availableForContent > 0 {
						perPane := availableForContent / len(otherContentPanes)
						if perPane > 1 {
							for _, contentID := range otherContentPanes {
								exec.Command("tmux", "resize-pane", "-t", contentID, "-y", fmt.Sprintf("%d", perPane)).Run()
							}
						}
					}
				}
			}

			if isCollapsed && desiredExpandHeight > 0 {
				winHeightOut, _ := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_height}").Output()
				totalHeight, _ := strconv.Atoi(strings.TrimSpace(string(winHeightOut)))
				if totalHeight > 0 {
					minPaneHeight := 1
					maxTarget := totalHeight - (len(headerPanes) * headerHeight) - (len(otherContentPanes) * minPaneHeight)
					if maxTarget < 1 {
						maxTarget = 1
					}
					targetHeight := desiredExpandHeight
					if targetHeight > maxTarget {
						targetHeight = maxTarget
					}

					currentTargetHeight := paneHeights[paneID]
					if currentTargetHeight <= 0 {
						heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
						currentTargetHeight, _ = strconv.Atoi(strings.TrimSpace(string(heightOut)))
					}
					need := targetHeight - currentTargetHeight
					if need > 0 && len(otherContentPanes) > 0 {
						sorted := make([]string, 0, len(otherContentPanes))
						sorted = append(sorted, otherContentPanes...)
						sort.Slice(sorted, func(i, j int) bool {
							return paneHeights[sorted[i]] > paneHeights[sorted[j]]
						})
						remaining := need
						for _, otherID := range sorted {
							if remaining <= 0 {
								break
							}
							h := paneHeights[otherID]
							if h <= 1 {
								continue
							}
							shrinkBy := h - 1
							if shrinkBy > remaining {
								shrinkBy = remaining
							}
							newH := h - shrinkBy
							exec.Command("tmux", "resize-pane", "-t", otherID, "-y", fmt.Sprintf("%d", newH)).Run()
							paneHeights[otherID] = newH
							remaining -= shrinkBy
						}
					}

					exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", targetHeight)).Run()
					heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
					newHeight, _ := strconv.Atoi(strings.TrimSpace(string(heightOut)))
					if newHeight > collapsedHeight {
						exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_collapsed").Run()
						exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_prev_height").Run()
					}
				}
			}

			// Fix all header pane heights LAST (after content pane resizes)
			for _, hdrID := range headerPanes {
				exec.Command("tmux", "resize-pane", "-t", hdrID, "-y", fmt.Sprintf("%d", headerHeight)).Run()
			}
		}
		return true

	case "select_pane":
		// Run synchronously so RefreshWindows() sees the new state
		// First find the window containing this pane and switch to it
		paneID := input.ResolvedTarget
		// Use display-message to get the window ID for this pane
		out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}").Output()
		if err == nil {
			windowID := strings.TrimSpace(string(out))
			if windowID != "" {
				// Select the window first, then the pane
				if err := c.SelectWindow(windowID, "semantic_select_pane_window", clientID); err != nil {
					logEvent("SELECT_PANE_WINDOW_ERR pane=%s window=%s client=%s err=%v", paneID, windowID, clientID, err)
					return false
				}
			}
		}
		exec.Command("tmux", "select-pane", "-t", paneID).Run()
		return true

	case "toggle_group":
		c.stateMu.Lock()
		groupName := input.ResolvedTarget
		if c.collapsedGroups[groupName] {
			delete(c.collapsedGroups, groupName)
		} else {
			c.collapsedGroups[groupName] = true
		}
		c.stateMu.Unlock()
		// Save async - don't block render on multiple tmux round-trips
		go c.saveCollapsedGroups()
		return false // No tmux window state change

	case "open_degraded":
		// Click on the TeamClaude widget's warning icon -> open the
		// degraded-models popup.
		c.launchDegradedModelsPopup(clientID)
		return true

	case "button":
		switch input.ResolvedTarget {
		case "new_tab":
			c.createNewWindowInCurrentGroup(clientID)
			// Don't call selectContentPaneInActiveWindow() here - the new window only has
			// one pane (shell) until the daemon spawns the sidebar. Let spawnRenderers
			// handle focus correctly after creating the sidebar pane.
		case "new_group":
			// Could implement group creation dialog
		case "close_tab":
			exec.Command("tmux", "kill-window").Run()
			// Try to switch to the previously active window rather than tmux's default (next)
			exec.Command("tmux", "last-window").Run()
			selectContentPaneInActiveWindow()
		}
		return true

	case "new_tab":
		c.createNewWindowInCurrentGroup(clientID)
		// Don't call selectContentPaneInActiveWindow() - let spawnRenderers handle focus
		return true

	case "new_group":
		// If a name was provided (from tabby-hook), create directly.
		// Otherwise, prompt the user via tmux command-prompt.
		if input.ResolvedTarget != "" {
			configPath := config.DefaultConfigPath()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
				return false
			}
			newGroup := config.DefaultGroupWithIndex(input.ResolvedTarget, len(cfg.Groups))
			if err := config.AddGroup(cfg, newGroup); err != nil {
				exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
				return false
			}
			if err := config.SaveConfig(configPath, cfg); err != nil {
				exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
				return false
			}
			return true
		}
		// No name provided — prompt user, then call back via `tabby hook new-group`.
		hookPath := c.getHookPath()
		callback := fmt.Sprintf("run-shell '%s new-group %%%% '", hookPath)
		exec.Command("tmux", "command-prompt", "-p", "New group name:", callback).Run()
		return false

	case "close_tab":
		exec.Command("tmux", "kill-window").Run()
		exec.Command("tmux", "last-window").Run()
		selectContentPaneInActiveWindow()
		return true

	case "prev_window", "next_window":
		// Suppress bare M-}/M-{ (the keybind normalized from cmd+]/cmd+[
		// in tabby.tmux:526-545) when the client that fired the binding
		// is phone-sized. The iOS terminal app's touch-gesture
		// interpretation can synthesize these keystrokes from a
		// slightly-imperfect `+` tap on the mobile button bar,
		// producing the "tap + then it cycles over and over" symptom.
		//
		// We check BOTH the globally active profile AND the invoking
		// TTY's profile (sent from tabby-hook) for maximum robustness.
		// The original iOS-synthesized "+ tap → cycle over and over"
		// burst was eliminated at the source in commit 2d9026b, so the
		// historic 300ms input-side debounce is no longer load-bearing.
		// With nav-path execution down to ~45ms per switch, rapid
		// keypresses simply queue and execute in order; no smoothing
		// needed at this layer.
		invokingTTY := ""
		if parts := strings.Split(input.PickerValue, ";"); len(parts) > 0 {
			for _, p := range parts {
				if strings.HasPrefix(p, "invoking=") {
					invokingTTY = strings.TrimPrefix(p, "invoking=")
					break
				}
			}
		}

		// Trace the request the moment the daemon starts handling it. Paired with
		// the hook's HOOK_SENT (same navid) this proves delivery; every return
		// below emits a matching outcome, so a request that reaches here but does
		// not switch is no longer invisible. See pkg/navtrace.
		navID := navIDFromValue(input.PickerValue)
		navtrace.Write("RECV navid=%s action=%s target=%s invoking=%s",
			navID, input.ResolvedAction, strings.TrimSpace(input.ResolvedTarget), invokingTTY)

		suppress := false
		reason := ""
		if c.ActiveClientProfile() == "phone" {
			suppress = true
			reason = "phone_active_client"
		} else if invokingTTY != "" {
			// Fallback: global elector might still pick desktop (e.g. if
			// phone sidebar is stashed and has less activity), so check
			// the invoking TTY's actual width.
			if w := c.getTTYWidth(invokingTTY); w > 0 && w < 100 {
				suppress = true
				reason = "phone_invoking_tty"
			}
		}

		if suppress {
			// Previously phone clients DROPPED key-nav entirely — a guard against
			// iOS touch gestures synthesizing spurious M-]/M-[ bursts. That source
			// was fixed (commit 2d9026b), and native window nav now skips minimized
			// windows on its own (they're parked in a holding session), so there's no
			// longer a reason to disable phone nav. Fall through to the normal
			// per-client path below so the phone's keys navigate (moving only the
			// phone client) and still skip minimized windows.
			logEvent("NAV_KEY_PHONE_FALLTHROUGH action=%s reason=%s target=%s invoking=%s",
				input.ResolvedAction, reason, strings.TrimSpace(input.ResolvedTarget), invokingTTY)
			navtrace.Write("RECV navid=%s note=phone_fallthrough reason=%s", navID, reason)
		}

		delta := -1
		if input.ResolvedAction == "next_window" {
			delta = +1
		}
		// In the dashboard, [/] cycles focus between the grid tiles. Do NOT call
		// focusContentPaneInActiveWindow here — it would re-select a default pane
		// and undo the cycle. dashboardNavStep does its own select-pane. Return
		// FALSE (not true) so we don't trigger SubmitRefresh+BroadcastRender —
		// the sidebar's content doesn't change for an in-dashboard pane cycle,
		// and the broadcast was causing the renderer to repaint (visible judder).
		if c.dashboardNavStep(delta) {
			navtrace.Write("OUTCOME navid=%s result=dashboard_cycle delta=%d", navID, delta)
			return false
		}
		sourceWindowID := navSourceWindowFromTarget(input.ResolvedTarget)
		// logNavKeyTrigger does 2 tmux fork/execs for diagnostics
		// (display-message + list-clients). Off the hot path.
		action := input.ResolvedAction
		rt := strings.TrimSpace(input.ResolvedTarget)
		pv := strings.TrimSpace(input.PickerValue)
		go logNavKeyTrigger(action, rt, pv)
		// focusContentPaneInActiveWindow does up to 4 tmux fork/execs
		// (show-option, display-message, list-panes, select-pane). On the
		// nav hot path the user's screen has already switched by the time
		// we'd run those — defer to a goroutine so the input handler
		// returns and the loop can render the broadcast immediately.
		if sourceWindowID != "" && c.selectNeighborWindowPerClient(sourceWindowID, delta, "global_key") {
			navtrace.Write("OUTCOME navid=%s result=switched path=per_client source=%s delta=%d", navID, sourceWindowID, delta)
			go focusContentPaneInActiveWindow()
			return true
		}
		c.selectNeighborWindowFrom(sourceWindowID, delta, "global_key")
		navtrace.Write("OUTCOME navid=%s result=switched path=global source=%s delta=%d", navID, sourceWindowID, delta)
		go focusContentPaneInActiveWindow()
		return true

	case "drop_food":
		c.stealPetOwnership() // a feed on this session must persist
		// Drop food at a random position for the pet to eat
		c.stateMu.Lock()
		// If dead, food revives the pet!
		if c.pet.IsDead {
			c.pet.IsDead = false
			c.pet.DeathTime = time.Time{}
			c.pet.StarvingStart = time.Time{}
			c.pet.Hunger = 80
			c.pet.Happiness = 50
			c.pet.State = "eating"
			c.pet.LastThought = "life-giving noms!"
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return false
		}
		width := c.getClientWidth(clientID)
		dropX := safeRandRange(2, width-2)
		// Avoid dropping food on poop
		for attempts := 0; attempts < 5; attempts++ {
			hasPoop := false
			for _, poopX := range c.pet.PoopPositions {
				if abs(dropX-poopX) <= 1 {
					hasPoop = true
					break
				}
			}
			if !hasPoop {
				break
			}
			dropX = safeRandRange(2, width-2)
		}
		c.pet.FoodItem = pos2D{X: dropX, Y: 2} // Drop from high air
		c.pet.LastThought = "food!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "drop_yarn":
		// Drop or toss the yarn at click position
		c.stateMu.Lock()
		// Dead pets don't play
		if c.pet.IsDead {
			c.stateMu.Unlock()
			return false
		}
		width := c.getClientWidth(clientID)
		// Use click position, clamped to valid range
		tossX := input.MouseX
		if tossX < 2 {
			tossX = 2
		}
		if tossX >= width-2 {
			tossX = width - 1
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2}                  // Toss high
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second) // Yarn disappears after 15 seconds
		c.pet.YarnPushCount = 0
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "clean_poop":
		c.stealPetOwnership()
		// Clean up poop at the clicked position
		c.stateMu.Lock()
		if len(c.pet.PoopPositions) > 0 {
			// Remove the first poop (or use input.ResolvedTarget for specific position)
			c.pet.PoopPositions = c.pet.PoopPositions[1:]
			c.pet.TotalPoopsCleaned++
			c.pet.LastThought = "much better." // instant fallback; LLM upgrades it
			c.triggerPetEventThought("cleaned")
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "pet_pet":
		c.stealPetOwnership()
		// Pet the pet - increase happiness (and wake up if sleeping)
		c.stateMu.Lock()
		wasSleeping := c.pet.State == "sleeping"
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		if wasSleeping {
			c.pet.LastThought = randomThought("wakeup")
		} else {
			c.pet.LastThought = randomThought("petting")
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "shrink_sidebar", "shrink":
		// Shrink sidebar width by 5 columns (min 15)
		currentWidth := c.getClientWidth(clientID)
		newWidth := currentWidth - 5
		if newWidth < 15 {
			newWidth = 15
		}
		// Save to tmux option and sync all sidebar panes
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", newWidth)).Run()
		c.persistSidebarWidthProfile(clientID, newWidth)
		go syncAllSidebarWidths(newWidth)
		// Update client width tracking
		c.UpdateClientSizeSnapshot(clientID, newWidth, 0)
		coordinatorDebugLog.Printf("Sidebar shrink: %d -> %d (syncing all)", currentWidth, newWidth)
		return false

	case "grow_sidebar", "grow":
		// Grow sidebar width by 5 columns (max 50)
		currentWidth := c.getClientWidth(clientID)
		newWidth := currentWidth + 5
		if newWidth > 50 {
			newWidth = 50
		}
		// Save to tmux option and sync all sidebar panes
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", newWidth)).Run()
		c.persistSidebarWidthProfile(clientID, newWidth)
		go syncAllSidebarWidths(newWidth)
		// Update client width tracking
		c.UpdateClientSizeSnapshot(clientID, newWidth, 0)
		coordinatorDebugLog.Printf("Sidebar grow: %d -> %d (syncing all)", currentWidth, newWidth)
		return false

	case "toggle_minimize_window":
		// Minimize -> PARK the window into the holding session (so native tmux
		// next/previous-window skip it for every caller); unminimize -> restore it.
		// Target may be a window id ("@5"), index ("3"), pane id ("%7"), or empty
		// (current window). Resolve to a stable window id for the park helpers.
		target := strings.TrimSpace(input.ResolvedTarget)
		winID := ""
		switch {
		case target == "":
			winID = tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
		case strings.HasPrefix(target, "@"):
			winID = target
		case strings.HasPrefix(target, "%"):
			winID = tmuxOutputTrimmed("display-message", "-t", target, "-p", "#{window_id}")
		default: // window index
			winID = tmuxOutputTrimmed("display-message", "-t", ":"+target, "-p", "#{window_id}")
		}
		if winID == "" {
			return false
		}
		if tmuxOutputTrimmed("show-window-option", "-v", "-t", winID, "@tabby_minimized") == "1" {
			c.clearPeekIf(winID)
			c.unparkWindow(winID)
		} else {
			// Don't park the focused window out from under the user: move focus to a
			// neighbor first when minimizing the active window.
			if tmuxOutputTrimmed("display-message", "-p", "#{window_id}") == winID {
				c.selectNeighborWindowFrom(winID, +1, "minimize")
			}
			c.parkWindow(winID, true)
		}
		return true

	case "toggle_collapse_sidebar", "collapse_sidebar", "expand_sidebar":
		// Legacy action names from the old 1-col collapse feature. They now
		// toggle sidebar visibility via break-pane stash / join-pane restore,
		// matching the window-header hamburger path.
		if sidebarIsStashed() {
			c.sidebarHidden = false
			c.restoreSidebarPanes()
			coordinatorDebugLog.Printf("toggle_collapse_sidebar: restored stashed sidebars")
		} else {
			c.sidebarHidden = true
			c.hideSidebarPanes()
			coordinatorDebugLog.Printf("toggle_collapse_sidebar: stashed sidebars")
		}
		focusContentPaneInActiveWindow()
		return false

	case "dashboard_toggle":
		// Gather every content pane into one tiled dashboard window, or restore
		// them to their origin windows if already gathered. See dashboard.go.
		sess := c.dashboardSession()
		if active := dashboardActiveWindowID(sess); active != "" {
			c.dashboardWindowID = active
			c.exitDashboard()
			coordinatorDebugLog.Printf("dashboard_toggle: exited dashboard")
		} else {
			c.enterDashboard()
			coordinatorDebugLog.Printf("dashboard_toggle: entered dashboard")
		}
		return true

	case "sidebar_settings":
		// Show sidebar settings context menu
		c.showSidebarSettingsMenu(clientID, menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY})
		return false

	case "header_split_v":
		// Get the pane's current path first, then use it for the split
		pathOut, _ := exec.Command("tmux", "display-message", "-t", input.ResolvedTarget, "-p", "#{pane_current_path}").Output()
		panePath := strings.TrimSpace(string(pathOut))
		if panePath == "" {
			panePath = "~"
		}
		exec.Command("tmux", "split-window", "-v", "-t", input.ResolvedTarget, "-c", panePath).Run()
		return true

	case "header_split_h":
		// Get the pane's current path first, then use it for the split
		pathOut2, _ := exec.Command("tmux", "display-message", "-t", input.ResolvedTarget, "-p", "#{pane_current_path}").Output()
		panePath2 := strings.TrimSpace(string(pathOut2))
		if panePath2 == "" {
			panePath2 = "~"
		}
		exec.Command("tmux", "split-window", "-h", "-t", input.ResolvedTarget, "-c", panePath2).Run()
		return true

	case "kill_pane", "header_close":
		paneID := input.ResolvedTarget
		// Count content panes — if only 1, kill the window instead
		windowIDOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}").Output()
		windowID := strings.TrimSpace(string(windowIDOut))
		if windowID != "" {
			panesOut, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_current_command}").Output()
			contentCount := 0
			for _, cmd := range strings.Split(strings.TrimSpace(string(panesOut)), "\n") {
				if !isAuxiliaryPaneCommand(cmd) {
					contentCount++
				}
			}
			if contentCount <= 1 {
				// Last content pane — kill the window
				exec.Command("tmux", "kill-window", "-t", windowID).Run()
				return true
			}
		}
		saveLayoutBeforeKill(paneID)
		exec.Command("tmux", "kill-pane", "-t", paneID).Run()
		return true

	case "header_select_pane":
		// Click on a pane label in the header -> focus that pane
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		return true

	case "window_header:hamburger":
		// Hamburger tapped on window header -> hide/show the inline sidebar by
		// break-pane'ing it out to a stash window (keeping the renderer process
		// alive), and join-pane'ing it back on the next tap.
		sourceWindowID := strings.TrimPrefix(clientID, "window-header:")
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, sourceWindowID)
		c.recordWindowHeaderPress(sourceWindowID, "window_header:hamburger")
		// Capture the user's actual tmux client (the one whose tap we're
		// servicing) BEFORE we run hide/restore so we can anchor it back to
		// the source window afterward. activeClientGeometry resolves via the
		// ClientElector that was just pinned by the input handler.
		_, _, userTTY, _, _ := activeClientGeometry()
		// On a PHONE, the hamburger opens the full-width sidebar as a display-popup
		// OVERLAY (the sidebar-popup renderer at 100%x100%). This is the SAFE
		// re-enable of the old full-width mode: an overlay never break-panes content
		// out to _tabby_limbo, so the content-loss bug that got the previous
		// full-width mode disabled (nav bypassing the close-first guard -> emptied
		// window reaped with its stashed content) simply cannot happen — the content
		// panes sit untouched behind the popup, which closes on tab-select / Esc.
		// Targeted at the exact phone client (-c <tty>) so it lands on the right
		// screen in a multi-client setup. Desktop keeps the inline hide/show below.
		if c.ActiveClientProfile() == "phone" {
			sessIDOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
			sessID := strings.TrimSpace(string(sessIDOut))
			if sessID == "" {
				sessID = c.sessionID
			}
			popupBin := getPopupBin()
			if popupBin != "" && sessID != "" {
				// display-popup's shell-command must be ONE argv string, or tmux
				// execs it as a literal program name and the popup flashes shut
				// (see launchQuestionPopup).
				escSess := strings.ReplaceAll(sessID, "'", `'\''`)
				popupCmd := fmt.Sprintf("%s --session '%s'", popupBin, escSess)
				// -B: no popup border. -s bg=<sidebarBg>: paint the popup SURFACE with
				// the sidebar background so no dark tmux-default shows through before/
				// around the renderer's own fill (the popup was rendering on a dark bg).
				args := []string{"display-popup", "-E", "-B", "-w", "100%", "-h", "100%"}
				if sbBg := c.GetSidebarBg(); sbBg != "" {
					args = append(args, "-s", fmt.Sprintf("bg=%s", sbBg))
				}
				if userTTY != "" {
					args = append(args, "-c", userTTY)
				}
				args = append(args, "--", popupCmd)
				go exec.Command("tmux", args...).Run()
				logEvent("HAMBURGER_FULLWIDTH_POPUP session=%s tty=%s", sessID, userTTY)
			}
			return false
		}
		// Desktop: hide/show the inline sidebar by break-pane'ing it out to a stash
		// window (renderer stays alive) and join-pane'ing it back on the next tap.
		if sidebarIsStashed() {
			c.sidebarHidden = false
			c.restoreSidebarPanes()
			coordinatorDebugLog.Printf("hamburger show: restored stashed sidebars")
		} else {
			c.sidebarHidden = true
			c.hideSidebarPanes()
			coordinatorDebugLog.Printf("hamburger hide: stashed sidebars")
		}
		// Re-anchor the user's specific tmux client to the window they tapped
		// from. join-pane/break-pane use -d so they shouldn't change focus,
		// but on a phone where each swipe window has its own tmux client,
		// any post-restore refocus that targets the "default client" can
		// land on the wrong one. switch-client -c <tty> -t @<window> is
		// addressed at the exact user-facing client and avoids that
		// ambiguity entirely.
		if userTTY != "" && sourceWindowID != "" {
			if err := exec.Command("tmux", "switch-client", "-c", userTTY, "-t", sourceWindowID).Run(); err != nil {
				logEvent("HAMBURGER_REANCHOR_ERR tty=%s window=%s err=%v", userTTY, sourceWindowID, err)
			} else {
				logEvent("HAMBURGER_REANCHOR tty=%s window=%s", userTTY, sourceWindowID)
			}
		}
		return false

	case "window_header:cycle_pane":
		// Toggle tmux zoom on the active content pane in this window. The bar's
		// own pane (window-header) and other system panes (sidebar, pane-header)
		// are excluded from selection — if for some reason a system pane is the
		// active one, fall back to the first non-system pane in pane order.
		windowID := strings.TrimPrefix(clientID, "window-header:")
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, windowID)
		c.recordWindowHeaderPress(windowID, "window_header:cycle_pane")

		listTarget := windowID
		if listTarget == "" {
			listTarget = input.ResolvedTarget
		}
		if listTarget == "" {
			listTarget = "."
		}

		out, err := exec.Command("tmux", "list-panes", "-t", listTarget, "-F",
			"#{pane_id}|||#{pane_active}|||#{pane_current_command}|||#{pane_start_command}").Output()
		if err != nil {
			return false
		}
		var activeContentID, firstContentID string
		for _, raw := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.SplitN(raw, "|||", 4)
			if len(parts) < 4 {
				continue
			}
			pid := parts[0]
			isActive := strings.TrimSpace(parts[1]) == "1"
			if paneIsSystemPane(parts[2], parts[3]) {
				continue
			}
			if firstContentID == "" {
				firstContentID = pid
			}
			if isActive {
				activeContentID = pid
			}
		}
		target := activeContentID
		if target == "" {
			target = firstContentID
		}
		if target == "" {
			return false
		}
		exec.Command("tmux", "resize-pane", "-Z", "-t", target).Run()
		return true

	case "window_header:prev_window":
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, strings.TrimPrefix(clientID, "window-header:"))
		c.recordWindowHeaderPress(strings.TrimPrefix(clientID, "window-header:"), "window_header:prev_window")
		c.selectNeighborWindowFrom(strings.TrimPrefix(clientID, "window-header:"), -1, "window_header")
		focusContentPaneInActiveWindow()
		return true

	case "window_header:next_window":
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, strings.TrimPrefix(clientID, "window-header:"))
		c.recordWindowHeaderPress(strings.TrimPrefix(clientID, "window-header:"), "window_header:next_window")
		c.selectNeighborWindowFrom(strings.TrimPrefix(clientID, "window-header:"), +1, "window_header")
		focusContentPaneInActiveWindow()
		return true

	case "window_header:new_window":
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, strings.TrimPrefix(clientID, "window-header:"))
		c.recordWindowHeaderPress(strings.TrimPrefix(clientID, "window-header:"), "window_header:new_window")
		// Record the press time + the source window so subsequent
		// WINDOW_HEADER_CLICK lines can show "since_new_window_ms" and
		// "new_window_id" — used to confirm the post-`+` cycling theory.
		// The source window (not the spawned window) is recorded because
		// the bug is about clicks landing on the *original* header right
		// after the spawn switches all clients away from it.
		c.lastNewWindowAt = time.Now()
		c.lastNewWindowID = strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
		c.createNewWindowInCurrentGroup(clientID)
		return true

	case "window_header:close_window":
		logEvent("WINDOW_HEADER_ACTION client=%s action=%s target=%s", clientID, input.ResolvedAction, input.ResolvedTarget)
		c.recordWindowHeaderPress(strings.TrimPrefix(clientID, "window-header:"), "window_header:close_window")
		// Show a tap-friendly confirm dialog before killing. `confirm-before`
		// would work but expects a y/n keypress at the status-line prompt,
		// which is awkward on touch clients; display-menu gives two tappable
		// options instead.
		target := input.ResolvedTarget
		if target == "" {
			if out, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
				target = strings.TrimSpace(string(out))
			}
		}
		if target != "" {
			exec.Command("tmux", "display-menu",
				"-T", "Close window?",
				"-x", "C", "-y", "C",
				"Cancel", "n", "",
				"", "", "",
				"CLOSE", "y", fmt.Sprintf("kill-window -t %s", target),
			).Run()
		}
		return true

	case "window_header:menu":
		// Title area tap -> route to existing header_context handler (no-op for left click)
		input.ResolvedAction = "header_context"
		return c.handleSemanticAction(clientID, input)

	case "pane_header:groups":
		// Groups button on phone header -> show group context menu as popup
		sessIDOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		sessID := strings.TrimSpace(string(sessIDOut))
		popupBin := getPopupBin()
		if popupBin != "" && sessID != "" {
			// display-popup's shell-command must be a single argv string;
			// passing popupBin/--session/sessID separately makes tmux 3.x
			// exec popupBin as a literal program name → popup flashes open
			// then closes. See launchQuestionPopup for the same fix.
			escSess := strings.ReplaceAll(sessID, "'", `'\''`)
			popupCmd := fmt.Sprintf("%s --session '%s'", popupBin, escSess)
			go exec.Command("tmux", "display-popup", "-E", "-w", "100%", "-h", "100%",
				"--", popupCmd).Run()
		}
		return false

	case "pane_header:settings":
		// Settings button on phone header -> open sidebar settings popup
		c.showSidebarSettingsMenu(clientID, menuPosition{PaneID: input.ResolvedTarget, X: 0, Y: 0})
		return false

	case "pane_header:new_pane":
		// New pane button on phone header -> split pane
		exec.Command("tmux", "split-window", "-h", "-c", "#{pane_current_path}").Run()
		return true

	case "header_context":
		// This is the full-width fallback region on pane headers.
		// Right-clicks are already handled by handleRightClick() above.
		// Left-clicks on the spacer area should be a no-op.
		return false

	case "header_drag_resize":
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		return false

	case "header_carat_up":
		exec.Command("tmux", "resize-pane", "-t", input.ResolvedTarget, "-U", "5").Run()
		fixHeaderHeightsInWindow(input.ResolvedTarget)
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		c.RefreshWindows()
		return true

	case "header_carat_down":
		exec.Command("tmux", "resize-pane", "-t", input.ResolvedTarget, "-D", "5").Run()
		fixHeaderHeightsInWindow(input.ResolvedTarget)
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		c.RefreshWindows()
		return true

	case "kill_window":
		target := strings.TrimSpace(input.ResolvedTarget)
		if target == "" {
			return false
		}
		// Accept the stable window ID ("@123", preferred — sent by the context
		// menu so the right tab dies even if indices shifted) or a legacy
		// numeric index. Validate strictly so nothing arbitrary reaches tmux.
		isID := strings.HasPrefix(target, "@")
		digits := target
		if isID {
			digits = target[1:]
		}
		if digits == "" {
			return false
		}
		for _, ch := range digits {
			if ch < '0' || ch > '9' {
				return false
			}
		}
		// Resolve the target window ID and its index from the live list. We key
		// neighbor selection on the index, but identify the target by whichever
		// form we were given.
		listOut, _ := exec.Command("tmux", "list-windows", "-F", "#{window_index}|#{window_id}").Output()
		type winRow struct {
			idx int
			id  string
		}
		var rows []winRow
		var targetID string
		targetIdx := -1
		for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
			parts := strings.SplitN(line, "|", 2)
			if len(parts) != 2 {
				continue
			}
			idx, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			rows = append(rows, winRow{idx: idx, id: parts[1]})
			if (isID && parts[1] == target) || (!isID && parts[0] == target) {
				targetID = parts[1]
				targetIdx = idx
			}
		}
		if targetID == "" {
			return false
		}
		// Pick the nearest neighbor by index (above first, else below) to switch
		// to if we're killing the active window.
		var aboveID, belowID string
		bestAboveIdx := -1
		bestBelowIdx := 999999
		for _, r := range rows {
			if r.idx < targetIdx && r.idx > bestAboveIdx {
				bestAboveIdx = r.idx
				aboveID = r.id
			} else if r.idx > targetIdx && r.idx < bestBelowIdx {
				bestBelowIdx = r.idx
				belowID = r.id
			}
		}
		if aboveID == "" {
			aboveID = belowID
		}
		// If killing the active window, switch to neighbor first
		activeOut, _ := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
		activeID := strings.TrimSpace(string(activeOut))
		if activeID == targetID && aboveID != "" {
			if err := c.SelectWindow(aboveID, "kill_window_neighbor_preselect", "kill_window"); err != nil {
				logEvent("KILL_WINDOW_PRESELECT_ERR target=%s neighbor=%s err=%v", targetID, aboveID, err)
				return false
			}
		}
		exec.Command("tmux", "set-option", "-g", "@tabby_close_select_window", "1").Run()
		exec.Command("tmux", "set-option", "-g", "@tabby_close_select_index", strconv.Itoa(targetIdx)).Run()
		exec.Command("tmux", "kill-window", "-t", targetID).Run()
		go func() {
			time.Sleep(200 * time.Millisecond)
			exec.Command("tmux", "set-option", "-gu", "@tabby_close_select_window").Run()
			exec.Command("tmux", "set-option", "-gu", "@tabby_close_select_index").Run()
		}()
		return true

	case "split_pane":
		direction := input.ResolvedTarget // "v" or "h"
		paneID := input.PickerValue
		if direction != "v" && direction != "h" {
			return false
		}
		if paneID == "" {
			paneID = ":" // current pane
		}
		// If current pane is a pane-header, find the underlying content pane
		metaOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p",
			"#{pane_current_command}|#{pane_start_command}|#{window_id}").Output()
		meta := strings.TrimSpace(string(metaOut))
		metaParts := strings.SplitN(meta, "|", 3)
		if len(metaParts) == 3 {
			curCmd, startCmd, winID := metaParts[0], metaParts[1], metaParts[2]
			if strings.Contains(curCmd, "pane-header") || strings.Contains(startCmd, "pane-header") {
				// Extract the -pane argument from start command
				extracted := extractPaneArg(startCmd)
				if extracted != "" {
					paneID = extracted
				} else if winID != "" {
					// Fallback: find first content pane in window
					paneID = findContentPane(winID, paneID)
				}
			}
		}
		// Validate target pane exists
		if out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_id}").Output(); err == nil {
			paneID = strings.TrimSpace(string(out))
		}
		// Get pane's current path
		pathOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_current_path}").Output()
		panePath := strings.TrimSpace(string(pathOut))
		if panePath == "" {
			pathOut, _ = exec.Command("tmux", "display-message", "-p", "#{pane_current_path}").Output()
			panePath = strings.TrimSpace(string(pathOut))
		}
		// Get pane dimensions for half-size split
		var sizeFlag string
		if direction == "v" {
			hOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_height}").Output()
			h := strings.TrimSpace(string(hOut))
			if hi, err := strconv.Atoi(h); err == nil && hi > 0 {
				half := hi / 2
				if half < 2 {
					half = 2
				}
				sizeFlag = strconv.Itoa(half)
			}
		} else {
			wOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_width}").Output()
			w := strings.TrimSpace(string(wOut))
			if wi, err := strconv.Atoi(w); err == nil && wi > 0 {
				half := wi / 2
				if half < 2 {
					half = 2
				}
				sizeFlag = strconv.Itoa(half)
			}
		}
		splitArgs := []string{"split-window", "-" + direction, "-t", paneID}
		if sizeFlag != "" {
			splitArgs = append(splitArgs, "-l", sizeFlag)
		}
		if panePath != "" {
			splitArgs = append(splitArgs, "-c", panePath)
		}
		exec.Command("tmux", splitArgs...).Run()
		return true

	case "pane_click":
		paneID := input.ResolvedTarget
		coords := input.PickerValue // "mouseX,mouseY,paneLeft,paneTop"
		if paneID == "" || coords == "" {
			return false
		}
		parts := strings.Split(coords, ",")
		if len(parts) != 4 {
			return false
		}
		mouseX, _ := strconv.Atoi(parts[0])
		mouseY, _ := strconv.Atoi(parts[1])
		paneLeft, _ := strconv.Atoi(parts[2])
		paneTop, _ := strconv.Atoi(parts[3])
		localX := mouseX - paneLeft
		localY := mouseY - paneTop
		if localX < 0 {
			localX = 0
		}
		if localY < 0 {
			localY = 0
		}
		exec.Command("tmux", "set-option", "-g", "@tabby_last_click_x", strconv.Itoa(localX)).Run()
		exec.Command("tmux", "set-option", "-g", "@tabby_last_click_y", strconv.Itoa(localY)).Run()
		exec.Command("tmux", "set-option", "-g", "@tabby_last_click_pane", paneID).Run()
		exec.Command("tmux", "select-pane", "-t", paneID).Run()
		return true

	case "exit_if_no_main_windows":
		sessionIDOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
		sessID := strings.TrimSpace(string(sessionIDOut))
		if sessID == "" {
			return false
		}
		panesOut, _ := exec.Command("tmux", "list-panes", "-a", "-t", sessID, "-F",
			"#{pane_current_command}|#{pane_start_command}|#{pane_dead}").Output()
		hasMain := false
		for _, line := range strings.Split(strings.TrimSpace(string(panesOut)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "|", 3)
			if len(parts) != 3 {
				continue
			}
			cmd, startCmd, dead := parts[0], parts[1], parts[2]
			if dead == "1" {
				continue
			}
			combined := cmd + "|" + startCmd
			if !strings.Contains(combined, "sidebar") &&
				!strings.Contains(combined, "renderer") &&
				!strings.Contains(combined, "pane-header") &&
				!strings.Contains(combined, "tabby-daemon") {
				hasMain = true
				break
			}
		}
		if !hasMain {
			exec.Command("tmux", "kill-session", "-t", sessID).Run()
			return true
		}
		return false

	case "delete_group":
		name := input.ResolvedTarget
		if name == "" {
			return false
		}
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
			return false
		}
		if err := config.DeleteGroup(cfg, name); err != nil {
			exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
			return false
		}
		if err := config.SaveConfig(configPath, cfg); err != nil {
			exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
			return false
		}
		return true

	case "rename_group":
		oldName := input.ResolvedTarget
		newName := input.PickerValue
		if oldName == "" || newName == "" {
			return false
		}
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
			return false
		}
		group := config.FindGroup(cfg, oldName)
		if group == nil {
			exec.Command("tmux", "display-message", "Error: group not found").Run()
			return false
		}
		if existing := config.FindGroup(cfg, newName); existing != nil {
			exec.Command("tmux", "display-message", "Error: group already exists").Run()
			return false
		}
		group.Name = newName
		group.Pattern = fmt.Sprintf("^%s\\|", newName)
		if err := config.SaveConfig(configPath, cfg); err != nil {
			exec.Command("tmux", "display-message", fmt.Sprintf("Error: %v", err)).Run()
			return false
		}
		return true

	case "set_group_color":
		name := input.ResolvedTarget
		color := input.PickerValue
		if name == "" || color == "" {
			return false
		}
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return false
		}
		group := config.FindGroup(cfg, name)
		if group == nil {
			return false
		}
		group.Theme.Bg = color
		group.Theme.ActiveBg = ""
		group.Theme.Fg = ""
		group.Theme.ActiveFg = ""
		group.Theme.InactiveBg = ""
		group.Theme.InactiveFg = ""
		config.SaveConfig(configPath, cfg)
		return true

	case "set_group_marker":
		name := input.ResolvedTarget
		marker := input.PickerValue
		if name == "" {
			return false
		}
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return false
		}
		group := config.FindGroup(cfg, name)
		if group == nil {
			return false
		}
		group.Theme.Icon = marker
		config.SaveConfig(configPath, cfg)
		if marker != "" {
			exec.Command("tmux", "display-message", "-d", "1500", fmt.Sprintf("Group marker -> %s", marker)).Run()
		} else {
			exec.Command("tmux", "display-message", "-d", "1500", "Group marker cleared").Run()
		}
		return true

	case "set_group_working_dir":
		name := input.ResolvedTarget
		dir := input.PickerValue
		if name == "" || dir == "" {
			return false
		}
		configPath := config.DefaultConfigPath()
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return false
		}
		group := config.FindGroup(cfg, name)
		if group == nil {
			return false
		}
		group.WorkingDir = dir
		config.SaveConfig(configPath, cfg)
		return true

	case "toggle_group_collapse":
		name := input.ResolvedTarget
		action := input.PickerValue // "collapse" or "expand"
		if name == "" || action == "" {
			return false
		}
		if action == "collapse" {
			c.stateMu.Lock()
			if c.collapsedGroups == nil {
				c.collapsedGroups = make(map[string]bool)
			}
			c.collapsedGroups[name] = true
			c.stateMu.Unlock()
			// Persist to tmux option
			collapsed := c.getCollapsedGroupsJSON()
			exec.Command("tmux", "set-option", "@tabby_collapsed_groups", collapsed).Run()
		} else {
			c.stateMu.Lock()
			delete(c.collapsedGroups, name)
			c.stateMu.Unlock()
			collapsed := c.getCollapsedGroupsJSON()
			if collapsed == "[]" {
				exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
			} else {
				exec.Command("tmux", "set-option", "@tabby_collapsed_groups", collapsed).Run()
			}
		}
		return true

	case "group_menu":
		// Hamburger menu on group header -> show group context menu
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		c.showGroupContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "window_menu":
		// Hamburger menu on window row -> show window context menu
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		c.showWindowContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "pane_menu":
		// Debounce header-triggered menu opens to avoid press/release reopen flashes.
		// Loop-only dedup (Step 5).
		if strings.HasPrefix(clientID, "window-header:") {
			key := clientID + "|" + input.ResolvedTarget
			now := time.Now()
			last := c.lastPaneMenuOpen[key]
			if now.Sub(last) < 900*time.Millisecond {
				return true
			}
			c.lastPaneMenuOpen[key] = now
		}

		// Header panes have BubbleTea mouse capture which intercepts clicks
		// that should dismiss the menu. Target the content pane instead so
		// tmux properly handles click-outside and Esc dismissal.
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		if strings.HasPrefix(clientID, "window-header:") {
			pos = menuPosition{PaneID: input.ResolvedTarget}
		}
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "pane_grow", "pane_shrink", "pane_grow_v", "pane_shrink_v", "pane_grow_h", "pane_shrink_h":
		paneID := input.ResolvedTarget
		action := input.ResolvedAction

		// Look up window and pane for position-aware resize logic.
		c.stateMu.RLock()
		var targetWin *tmux.Window
		var targetPane *tmux.Pane
		for i := range c.windows {
			for j := range c.windows[i].Panes {
				if c.windows[i].Panes[j].ID == paneID {
					targetWin = &c.windows[i]
					targetPane = &c.windows[i].Panes[j]
					break
				}
			}
			if targetWin != nil {
				break
			}
		}
		c.stateMu.RUnlock()

		if action == "pane_grow" || action == "pane_shrink" {
			// Backward compatibility: legacy actions use inferred dominant split axis.
			if targetWin != nil && c.isVerticalStackedPane(targetWin, paneID) {
				if action == "pane_grow" {
					action = "pane_grow_v"
				} else {
					action = "pane_shrink_v"
				}
			} else {
				if action == "pane_grow" {
					action = "pane_grow_h"
				} else {
					action = "pane_shrink_h"
				}
			}
		}

		// Use absolute sizing (-y/-x) instead of directional flags (-D/-U/-R/-L).
		// Directional flags move the "nearest" border, which for content panes
		// may be the 1-line header pane border rather than the adjacent content
		// pane border, causing unexpected or no-op resizes. Absolute sizing
		// tells tmux the desired result and lets it figure out which borders
		// to adjust.
		const resizeStep = 5
		if targetPane != nil {
			logEvent("RESIZE_DEBUG pane=%s action=%s cachedW=%d cachedH=%d top=%d left=%d",
				paneID, action, targetPane.Width, targetPane.Height, targetPane.Top, targetPane.Left)
			switch action {
			case "pane_grow_v":
				newSize := targetPane.Height + resizeStep
				logEvent("RESIZE_EXEC grow_v pane=%s -y %d", paneID, newSize)
				exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprint(newSize)).Run()
			case "pane_shrink_v":
				newH := targetPane.Height - resizeStep
				if newH < 2 {
					newH = 2
				}
				logEvent("RESIZE_EXEC shrink_v pane=%s -y %d", paneID, newH)
				exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprint(newH)).Run()
			case "pane_grow_h":
				newSize := targetPane.Width + resizeStep
				logEvent("RESIZE_EXEC grow_h pane=%s -x %d", paneID, newSize)
				exec.Command("tmux", "resize-pane", "-t", paneID, "-x", fmt.Sprint(newSize)).Run()
			case "pane_shrink_h":
				newW := targetPane.Width - resizeStep
				if newW < 2 {
					newW = 2
				}
				logEvent("RESIZE_EXEC shrink_h pane=%s -x %d", paneID, newW)
				exec.Command("tmux", "resize-pane", "-t", paneID, "-x", fmt.Sprint(newW)).Run()
			}
		} else {
			logEvent("RESIZE_DEBUG targetPane is nil for pane=%s", paneID)
		}

		fixHeaderHeightsInWindow(paneID)
		exec.Command("tmux", "select-pane", "-t", paneID).Run()
		c.RefreshWindows()
		return true

	case "sidebar_toggle_position":
		// Toggle sidebar position between left and right
		currentPos := c.config.Sidebar.Position
		newPos := "right"
		if currentPos == "right" {
			newPos = "left"
		}
		// Use tmux run-shell to restart asynchronously (the daemon dies on toggle-off)
		toggleScript := c.getToggleScript()
		if toggleScript != "" {
			restartCmd := fmt.Sprintf("tmux set-option -g @tabby_sidebar_position %s; %s; sleep 0.3; %s", newPos, toggleScript, toggleScript)
			exec.Command("tmux", "run-shell", "-b", restartCmd).Run()
		}
		return false

	case "toggle_prefix_mode":
		// Toggle prefix mode (flat window list vs grouped hierarchy)
		c.stateMu.Lock()
		c.config.Sidebar.PrefixMode = !c.config.Sidebar.PrefixMode
		newVal := "0"
		if c.config.Sidebar.PrefixMode {
			newVal = "1"
		}
		c.stateMu.Unlock()
		exec.Command("tmux", "set-option", "-gq", "@tabby_prefix_mode", newVal).Run()
		return false

	case "ground":
		// Ground click - determine action based on click X position
		// Click position relative to zone start
		clickX := input.MouseX
		c.stateMu.Lock()

		// Check if clicking on cat (only when cat is on ground, Y=0)
		if c.pet.Pos.Y == 0 && clickX == c.pet.Pos.X {
			// Pet the cat (wake up if sleeping)
			wasSleeping := c.pet.State == "sleeping"
			c.pet.Happiness = min(100, c.pet.Happiness+10)
			c.pet.TotalPets++
			c.pet.LastPet = time.Now()
			c.pet.State = "happy"
			if wasSleeping {
				c.pet.LastThought = randomThought("wakeup")
			} else {
				c.pet.LastThought = randomThought("petting")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return false
		}

		// Check if clicking on poop
		for i, poopX := range c.pet.PoopPositions {
			if clickX == poopX {
				// Clean this poop
				c.pet.PoopPositions = append(c.pet.PoopPositions[:i], c.pet.PoopPositions[i+1:]...)
				c.pet.TotalPoopsCleaned++
				c.pet.LastThought = "much better." // instant fallback; LLM upgrades it
				c.triggerPetEventThought("cleaned")
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return false
			}
		}

		// Otherwise, drop yarn at click position using client-specific width
		width := c.getClientWidth(clientID)
		tossX := clickX
		if tossX < 2 {
			tossX = 2
		}
		if tossX >= width-2 {
			tossX = width - 1
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.YarnPushCount = 0
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false
	}
	return false
}

// handlePetWidgetClick uses custom click detection for the pet widget
// This bypasses BubbleZone and uses tracked line positions for precise hit testing
// Returns true if the click was handled, false otherwise
func (c *Coordinator) handlePetWidgetClick(clientID string, input *daemon.InputPayload) bool {
	c.stealPetOwnership() // a click in the pet widget makes this session the writer
	// Get client-specific width for accurate click detection
	clientWidth := c.getClientWidth(clientID)

	// Calculate content Y from screen position and viewport offset
	contentY := input.MouseY + input.ViewportOffset
	clickX := input.MouseX
	layout := c.petLayout

	// Calculate Y position relative to pet widget start
	clickY := contentY - layout.ContentStartLine

	coordinatorDebugLog.Printf("Pet click detection: screenY=%d viewportOffset=%d contentY=%d petRelativeY=%d X=%d clientWidth=%d",
		input.MouseY, input.ViewportOffset, contentY, clickY, clickX, clientWidth)
	coordinatorDebugLog.Printf("  Layout: ContentStart=%d Feed=%d HighAir=%d LowAir=%d Ground=%d PlayWidth=%d",
		layout.ContentStartLine, layout.FeedLine, layout.HighAirLine, layout.LowAirLine, layout.GroundLine, layout.PlayWidth)

	// Check if click is within the pet widget at all. Run this BEFORE the
	// debounce so non-pet sidebar clicks aren't absorbed for 200ms after a
	// pet click.
	if clickY < 0 || clickY >= layout.WidgetHeight {
		coordinatorDebugLog.Printf("  -> Click outside pet widget bounds (clickY=%d, widgetHeight=%d)", clickY, layout.WidgetHeight)
		return false
	}

	// Debounce rapid clicks (200ms) to prevent render floods
	now := time.Now()
	if now.Sub(c.lastPetClick) < 200*time.Millisecond {
		return true // Absorb the click without processing
	}
	c.lastPetClick = now

	// Q&A teaser takes priority over the underlying thought-bubble line it
	// temporarily replaced. Without this branch first, the click would fall
	// through to whichever line the teaser overlapped (usually the row that
	// would otherwise be a normal thought) and dispatch to the wrong action.
	if c.pet.PendingQuestion != nil && layout.QuestionPromptLine >= 0 && clickY == layout.QuestionPromptLine {
		coordinatorDebugLog.Printf("  -> Q&A teaser clicked, launching popup")
		c.launchQuestionPopup(clientID)
		return true
	}

	// Check if click is on Feed line
	if clickY == layout.FeedLine {
		coordinatorDebugLog.Printf("  -> Feed line clicked, dropping food")
		c.stateMu.Lock()
		dropX := safeRandRange(2, clientWidth-2)
		c.pet.FoodItem = pos2D{X: dropX, Y: 2}
		c.pet.LastThought = "food!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if click is on debug bar lines. Gate on whether the debug bar
	// was actually rendered (DebugLine1>0) rather than config, since the
	// tight-viewport escalation can suppress it even when configured on.
	if c.config.Widgets.Pet.DebugBar && layout.DebugLine1 > 0 {
		if clickY == layout.DebugLine1 || clickY == layout.DebugLine2 || clickY == layout.DebugLine3 {
			coordinatorDebugLog.Printf("  -> Debug bar line clicked (line=%d, X=%d)", clickY, clickX)
			return c.handleDebugBarClick(clientID, clickX, clickY)
		}
	}

	// Calculate safe play width for this client (must match rendering)
	safePlayWidth := clientWidth - 1
	if safePlayWidth < 5 {
		safePlayWidth = 5
	}

	// Check if click is on high air line (Y=2 in pet coordinate space)
	if clickY == layout.HighAirLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> High air line clicked at X=%d, checking for cat/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 2)
	}

	// Check if click is on low air line (Y=1 in pet coordinate space)
	if clickY == layout.LowAirLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> Low air line clicked at X=%d, checking for cat/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 1)
	}

	// Check if click is on ground line (Y=0 in pet coordinate space)
	if clickY == layout.GroundLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> Ground line clicked at X=%d, checking for cat/poop/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 0)
	}

	coordinatorDebugLog.Printf("  -> Click not on pet widget interactive lines")
	return false
}

// launchQuestionMenu shows the pending question as a menu overlay. Each
// choice becomes a tappable entry that shells out to
// `tabby pet ask --answer "..."`, which posts the pick back to the daemon
// socket — same path the popup binary uses, just driven by a native menu
// instead of a full TUI pane.
//
// Routing goes through executeOrSendMenu, NOT a raw `tmux display-menu`:
// the pet widget lives in the sidebar, and sidebar clients render their own
// overlay menu (via OnSendMenu) that cooperates with the sidebar's mouse
// tracking. A bare `tmux display-menu` opened over a sidebar pane that still
// owns SGR mouse tracking gets dismissed by the very next mouse event, so
// the menu flashed open and closed instantly. executeOrSendMenu falls back
// to `tmux display-menu` only for header clients (where the overlay can't
// fit), which is the one place the raw command is safe.
//
// Falls back to launchQuestionPopup for free-text questions (a menu can't
// capture arbitrary text input) and when there is no pending question or no
// choices to show.
func (c *Coordinator) launchQuestionMenu(clientID string, pos menuPosition) {
	c.stateMu.RLock()
	pending := c.pet.PendingQuestion
	c.stateMu.RUnlock()
	if pending == nil || pending.Kind != "choice" || len(pending.Choices) == 0 {
		c.launchQuestionPopup(clientID)
		return
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		coordinatorDebugLog.Printf("launchQuestionMenu: no os.Executable; falling back to popup")
		c.launchQuestionPopup(clientID)
		return
	}
	args := buildQuestionMenuArgs(pending, exe, c.getClientWidth(clientID))
	c.executeOrSendMenu(clientID, args, pos)
}

// buildQuestionMenuArgs constructs the argv (after the "tmux" program
// name) for the `tmux display-menu` invocation used by launchQuestionMenu.
// Extracted from launchQuestionMenu so the menu layout — question wrapping,
// hotkey assignment, single-quote escaping, /dev/null redirect that
// prevents tmux from popping a view-mode buffer for the answer-cli's
// stdout — can be unit-tested without spawning tmux.
//
// width is the client's render width: the question is wrapped into the menu
// BODY (as non-selectable header rows) rather than crammed into the -T title,
// because the overlay renderer hard-truncates the title to the top border and
// a real question is unreadable there. We wrap here, in the daemon, because it
// knows the client width; emitting one header row per wrapped line keeps the
// overlay's one-row-per-item hit-testing valid.
func buildQuestionMenuArgs(pending *daemon.PendingQuestion, exe string, width int) []string {
	// Overlay content area is "│ " + text + " │", i.e. width-4. Floor it so a
	// pathologically narrow sidebar still wraps to something legible rather
	// than one-rune-per-line.
	innerWidth := width - 4
	if innerWidth < 12 {
		innerWidth = 12
	}
	args := []string{
		"display-menu",
		"-T", "the cat asks",
		"-x", "C", "-y", "C",
	}
	// Question rows. The "-" prefix marks each as a header for both tmux
	// display-menu and parseTmuxMenuArgs, so they render dimmed/bold and are
	// skipped by keyboard/mouse navigation (the highlight lands on the first
	// choice). One row per wrapped line preserves the overlay's row→index map.
	for _, line := range wrapToWidth(pending.Text, innerWidth) {
		args = append(args, "-"+line, "", "")
	}
	args = append(args, "", "", "") // separator between question and choices
	// Hotkeys 1-9 for the first nine choices; anything beyond gets no
	// keybind (tmux still lets the user click). The shell command embeds
	// the choice in single quotes — escape any literal single quotes by
	// the standard '\'' trick so a choice like "don't know" round-trips.
	for i, choice := range pending.Choices {
		var key string
		if i < 9 {
			key = fmt.Sprintf("%d", i+1)
		}
		escaped := strings.ReplaceAll(choice, "'", `'\''`)
		// --quiet silences the success print on stdout; the trailing
		// 2>/dev/null swallows errors too (e.g. "no pending question"
		// if the user double-picks before the daemon clears state).
		// Either source landing in tmux's run-shell capture pops a
		// view-mode buffer / spills into the focused pane.
		cmd := fmt.Sprintf("run-shell -b \"%s pet ask --quiet --answer '%s' 2>/dev/null\"", exe, escaped)
		args = append(args, choice, key, cmd)
	}
	args = append(args, "", "", "")        // separator before Cancel
	args = append(args, "Cancel", "q", "") // Cancel sits below the choices
	return args
}

// wrapToWidth word-wraps s into lines no wider than width display cells,
// breaking on whitespace and hard-splitting any single word that exceeds
// width. Always returns at least one (possibly empty) line so callers can
// render unconditionally. Width is measured with uniseg so wide glyphs and
// emoji in question text don't overflow the menu frame.
func wrapToWidth(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curW = 0
	}
	for _, word := range strings.Fields(s) {
		ww := uniseg.StringWidth(word)
		if ww > width {
			// Word can't fit on a line alone: hard-split it by rune.
			if curW > 0 {
				flush()
			}
			for _, r := range word {
				rw := uniseg.StringWidth(string(r))
				if curW+rw > width {
					flush()
				}
				cur.WriteRune(r)
				curW += rw
			}
			continue
		}
		sep := 0
		if curW > 0 {
			sep = 1 // a space separates this word from the previous one
		}
		if curW+sep+ww > width {
			flush()
			sep = 0
		}
		if sep == 1 {
			cur.WriteByte(' ')
			curW++
		}
		cur.WriteString(word)
		curW += ww
	}
	if cur.Len() > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// launchQuestionPopup spawns the pet Q&A popup via `tmux display-popup`,
// mirroring the pane_header:groups pattern at coordinator.go:12725. The
// popup binary (cmd/tabby/internal/petqapopup, dispatched via
// `tabby render pet-qa-popup`) reads the pending question from the daemon
// socket, renders a TUI, and posts the answer back. We don't block on the
// command — `tmux display-popup -E` keeps the popup attached to the user's
// session and exits when they answer or hit Esc.
//
// We use rendererExecPrefix directly (rather than getPopupBin, which is
// hard-wired to the `sidebar-popup` subcommand) so the same tabby binary
// dispatches to the pet-qa-popup render path. The argv[0] override
// (`tabby-pet-qa-popup`) keeps tmux's #{pane_current_command} distinct
// from the sidebar popup for any future heuristic that wants to tell them
// apart.
//
// clientID is unused for now (the popup binds via session ID); kept in the
// signature for symmetry with other widget click handlers and so we can
// pass per-client context (e.g. window/pane scoping) later without
// reshaping the call site.
func (c *Coordinator) launchQuestionPopup(clientID string) {
	_ = clientID
	sessIDOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	sessID := strings.TrimSpace(string(sessIDOut))
	if sessID == "" {
		// Fall back to the coordinator's own session id when the live tmux
		// query fails (e.g. during tests or if tmux isn't responsive).
		sessID = c.sessionID
	}
	popupBin := rendererExecPrefix("tabby-pet-qa-popup", "pet-qa-popup")
	if popupBin == "" || sessID == "" {
		coordinatorDebugLog.Printf("  -> launchQuestionPopup: missing popupBin=%q or sessID=%q", popupBin, sessID)
		return
	}
	// display-popup's shell-command MUST be a single argument. Passing the
	// command and its flags as separate argv entries (e.g. popupBin,
	// "--session", sessID) makes tmux 3.x exec the first arg directly as a
	// program name — `exec -a … render pet-qa-popup` is not a real binary,
	// so the popup opens with nothing to run and closes instantly (the
	// "flash open then disappear" bug). Fold everything into one string and
	// single-quote the session id (escaping embedded quotes) so a session
	// name with shell-special chars round-trips.
	escSess := strings.ReplaceAll(sessID, "'", `'\''`)
	popupCmd := fmt.Sprintf("%s --session '%s'", popupBin, escSess)
	go exec.Command("tmux", "display-popup", "-E", "-w", "60%", "-h", "30%",
		"--", popupCmd).Run()
}

// launchDegradedModelsPopup spawns the TeamClaude degraded-models popup via
// `tmux display-popup`, mirroring launchQuestionPopup. The popup binary
// (cmd/tabby/internal/degradedmodelspopup, dispatched via
// `tabby render degraded-models-popup`) fetches the proxy's degraded-model map
// itself and renders a TUI with the downgraded models, their fallback targets,
// reset countdowns, and quick links to status.anthropic.com / downdetector.
// We don't block on it — display-popup -E keeps it attached and closes on Esc.
func (c *Coordinator) launchDegradedModelsPopup(clientID string) {
	_ = clientID
	sessIDOut, _ := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	sessID := strings.TrimSpace(string(sessIDOut))
	if sessID == "" {
		sessID = c.sessionID
	}
	popupBin := rendererExecPrefix("tabby-degraded-models-popup", "degraded-models-popup")
	if popupBin == "" || sessID == "" {
		coordinatorDebugLog.Printf("  -> launchDegradedModelsPopup: missing popupBin=%q or sessID=%q", popupBin, sessID)
		return
	}
	// display-popup's shell-command MUST be a single argument (see
	// launchQuestionPopup for the flash-open-then-close failure mode otherwise).
	escSess := strings.ReplaceAll(sessID, "'", `'\''`)
	popupCmd := fmt.Sprintf("%s --session '%s'", popupBin, escSess)
	go exec.Command("tmux", "display-popup", "-E", "-w", "60%", "-h", "40%",
		"--", popupCmd).Run()
}

// getSprites returns the pet sprites based on current style and config overrides
func (c *Coordinator) getSprites() petSprites {
	petCfg := c.config.Widgets.Pet
	style := petCfg.Style
	if style == "" {
		style = "emoji"
	}
	sprites, ok := petSpritesByStyle[style]
	if !ok {
		sprites = petSpritesByStyle["emoji"]
	}

	// Apply config icon overrides (config takes priority over style preset)
	icons := petCfg.Icons
	if icons.Idle != "" {
		sprites.Idle = icons.Idle
	}
	if icons.Walking != "" {
		sprites.Walking = icons.Walking
	}
	if icons.Jumping != "" {
		sprites.Jumping = icons.Jumping
	}
	if icons.Playing != "" {
		sprites.Playing = icons.Playing
	}
	if icons.Eating != "" {
		sprites.Eating = icons.Eating
	}
	if icons.Sleeping != "" {
		sprites.Sleeping = icons.Sleeping
	}
	if icons.Happy != "" {
		sprites.Happy = icons.Happy
	}
	if icons.Hungry != "" {
		sprites.Hungry = icons.Hungry
	}
	if icons.Yarn != "" {
		sprites.Yarn = icons.Yarn
	}
	if icons.Food != "" {
		sprites.Food = icons.Food
	}
	if icons.Poop != "" {
		sprites.Poop = icons.Poop
	}
	if icons.Thought != "" {
		sprites.Thought = icons.Thought
	}
	if icons.Heart != "" {
		sprites.Heart = icons.Heart
	}
	if icons.Life != "" {
		sprites.Life = icons.Life
	}
	if icons.HungerIcon != "" {
		sprites.HungerIcon = icons.HungerIcon
	}
	if icons.HappyIcon != "" {
		sprites.HappyIcon = icons.HappyIcon
	}
	if icons.SadIcon != "" {
		sprites.SadIcon = icons.SadIcon
	}
	if icons.Ground != "" {
		sprites.Ground = icons.Ground
	}

	return sprites
}

// handlePetPlayAreaClick handles clicks within the pet play area
// clickX is the X position, petY is the Y in pet coordinate space (0=ground, 1=low air, 2=high air)
func (c *Coordinator) handlePetPlayAreaClick(clientID string, input *daemon.InputPayload, clickX, petY int) bool {
	c.stealPetOwnership()
	c.stateMu.Lock()

	// Get sprite strings for width calculation
	sprites := c.getSprites()

	// Calculate safe play width using client-specific width (must match renderPetWidget)
	playWidth := c.getClientWidth(clientID)
	safePlayWidth := playWidth - 1
	if safePlayWidth < 5 {
		safePlayWidth = 5
	}

	// Get clamped positions (same as rendering does)
	// This ensures click detection matches what's displayed
	catPosX := c.pet.Pos.X
	if catPosX >= safePlayWidth {
		catPosX = safePlayWidth - 1
	}
	if catPosX < 0 {
		catPosX = 0
	}

	yarnPosX := c.pet.YarnPos.X
	if yarnPosX >= safePlayWidth {
		yarnPosX = safePlayWidth - 1
	}

	dragonPosX := c.pet.DragonPos.X
	if dragonPosX >= safePlayWidth {
		dragonPosX = safePlayWidth - 1
	}
	if dragonPosX < 0 {
		dragonPosX = 0
	}
	dragonSprite := "🐉"
	if c.pet.DragonState == "sleeping" {
		dragonSprite = "💤"
	}
	dragonWidth := uniseg.StringWidth(dragonSprite)
	if dragonWidth < 1 {
		dragonWidth = 1
	}

	// Get cat sprite based on current state
	catSprite := sprites.Idle
	switch c.pet.State {
	case "walking":
		catSprite = sprites.Walking
	case "jumping":
		catSprite = sprites.Jumping
	case "playing":
		catSprite = sprites.Playing
	case "eating":
		catSprite = sprites.Eating
	case "sleeping":
		catSprite = sprites.Sleeping
	case "happy":
		catSprite = sprites.Happy
	case "hungry":
		catSprite = sprites.Hungry
	case "dead":
		catSprite = sprites.Dead
	}
	// Dead overrides everything
	if c.pet.IsDead {
		catSprite = sprites.Dead
	}
	catWidth := uniseg.StringWidth(catSprite)
	if catWidth < 1 {
		catWidth = 1
	}

	// Check if clicking on sleeping cat (💤 at position 0 on ground)
	// When sleeping, the cat is represented by 💤 in bottom left corner
	if c.pet.State == "sleeping" && petY == 0 {
		zzzWidth := uniseg.StringWidth("💤")
		if zzzWidth < 1 {
			zzzWidth = 2
		}
		if clickX >= 0 && clickX < zzzWidth {
			coordinatorDebugLog.Printf("    -> Clicked on sleeping cat (💤)! Waking up.")
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("wakeup")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
	}

	// Check if clicking on cat (account for sprite display width)
	// Sprites like emojis display wider than their rune position
	// Use clamped position to match what's rendered on screen
	if c.pet.Pos.Y == petY && clickX >= catPosX && clickX < catPosX+catWidth {
		// If dead, clicking revives the pet
		if c.pet.IsDead {
			coordinatorDebugLog.Printf("    -> Clicked on dead pet! Reviving.")
			c.pet.IsDead = false
			c.pet.DeathTime = time.Time{}
			c.pet.StarvingStart = time.Time{}
			c.pet.Hunger = 50
			c.pet.Happiness = 50
			c.pet.State = "happy"
			c.pet.LastThought = "back from the void!"
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		coordinatorDebugLog.Printf("    -> Clicked on cat at X=%d (cat rendered at %d, width=%d)! Petting.", clickX, catPosX, catWidth)
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		c.pet.LastThought = randomThought("petting")
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if clicking on dragon
	if c.pet.DragonState != "" && c.pet.DragonPos.Y == petY && clickX >= dragonPosX && clickX < dragonPosX+dragonWidth {
		coordinatorDebugLog.Printf("    -> Clicked on dragon at X=%d! Dragon flies and breathes fire.", clickX)
		c.pet.DragonState = "flying"
		c.pet.DragonPos.Y = 2
		c.pet.DragonDirection = []int{-1, 1}[rand.Intn(2)]
		c.pet.DragonTargetPos = pos2D{X: safeRandRange(0, safePlayWidth-5), Y: 2}
		c.pet.DragonHasTarget = true
		c.pet.DragonActionPending = "fly_4"

		// Breathe fire!
		dir := c.pet.DragonDirection
		if dir == 0 {
			dir = 1
		}
		fireX := c.pet.DragonPos.X + dir
		if fireX < 0 {
			fireX = 0
		}
		if fireX >= safePlayWidth {
			fireX = safePlayWidth - 1
		}
		c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
			Emoji:     "🔥",
			Pos:       pos2D{X: fireX, Y: c.pet.DragonPos.Y},
			Velocity:  pos2D{X: dir, Y: 0},
			ExpiresAt: time.Now().Add(2 * time.Second),
		})

		// Spawn some clouds
		for i := 0; i < 2; i++ {
			c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
				Emoji:     "☁️",
				Pos:       pos2D{X: safeRandRange(0, safePlayWidth-1), Y: 1 + rand.Intn(2)},
				Velocity:  pos2D{X: []int{-1, 1}[rand.Intn(2)], Y: 0},
				ExpiresAt: time.Now().Add(4 * time.Second),
			})
		}

		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if clicking on poop (only on ground)
	if petY == 0 {
		poopWidth := uniseg.StringWidth(sprites.Poop)
		if poopWidth < 1 {
			poopWidth = 1
		}
		for i, poopX := range c.pet.PoopPositions {
			// Clamp poop position same as rendering
			clampedPoopX := poopX
			if clampedPoopX >= safePlayWidth {
				clampedPoopX = safePlayWidth - 1
			}
			if clampedPoopX < 0 {
				clampedPoopX = 0
			}
			if clickX >= clampedPoopX && clickX < clampedPoopX+poopWidth {
				coordinatorDebugLog.Printf("    -> Clicked on poop at X=%d (poop rendered at %d, width=%d)! Cleaning.", clickX, clampedPoopX, poopWidth)
				c.pet.PoopPositions = append(c.pet.PoopPositions[:i], c.pet.PoopPositions[i+1:]...)
				c.pet.TotalPoopsCleaned++
				c.pet.LastThought = "much better." // instant fallback; LLM upgrades it
				c.triggerPetEventThought("cleaned")
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return true
			}
		}
	}

	// Check if clicking on mouse - help the pet catch it!
	if c.pet.MousePos.X >= 0 && petY == 0 {
		mouseWidth := uniseg.StringWidth(sprites.Mouse)
		if mouseWidth < 1 {
			mouseWidth = 1
		}
		clampedMouseX := c.pet.MousePos.X
		if clampedMouseX >= safePlayWidth {
			clampedMouseX = safePlayWidth - 1
		}
		if clampedMouseX < 0 {
			clampedMouseX = 0
		}
		if clickX >= clampedMouseX && clickX < clampedMouseX+mouseWidth {
			coordinatorDebugLog.Printf("    -> Clicked on mouse! Pet catches it.")
			c.pet.MousePos = pos2D{X: -1, Y: 0}
			c.pet.TotalMouseCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+20)
			c.pet.State = "happy"
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			c.pet.LastThought = randomThought("mouse_kill")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
	}

	// DEBUG: Click far left on ground (X=0) to spawn a mouse
	if petY == 0 && clickX == 0 && c.pet.MousePos.X < 0 {
		coordinatorDebugLog.Printf("    -> DEBUG: Spawning mouse!")
		c.pet.MouseDirection = 1
		c.pet.MousePos = pos2D{X: 0, Y: 0}
		c.pet.LastThought = randomThought("mouse_spot")
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if clicking on yarn (account for sprite width)
	// Use clamped position to match what's rendered on screen
	yarnWidth := uniseg.StringWidth(sprites.Yarn)
	if yarnWidth < 1 {
		yarnWidth = 1
	}
	if c.pet.YarnPos.Y == petY && clickX >= yarnPosX && clickX < yarnPosX+yarnWidth {
		coordinatorDebugLog.Printf("    -> Clicked on yarn at X=%d (yarn rendered at %d)! Moving it.", clickX, yarnPosX)
		// Toss the yarn to a new position using client-specific width
		width := playWidth
		newX := safeRandRange(2, width-2)
		c.pet.YarnPos = pos2D{X: newX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.TargetPos = pos2D{X: newX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "again!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Otherwise, drop yarn at click position using client-specific width
	coordinatorDebugLog.Printf("    -> Empty space clicked, dropping yarn at X=%d", clickX)
	tossX := clickX
	if tossX < 2 {
		tossX = 2
	}
	if tossX >= playWidth-2 {
		tossX = playWidth - 1
	}
	// Start yarn at high air, let it fall
	c.pet.YarnPos = pos2D{X: tossX, Y: 2}
	c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
	c.pet.YarnPushCount = 0
	c.pet.TargetPos = pos2D{X: tossX, Y: 0}
	c.pet.HasTarget = true
	c.pet.ActionPending = "play"
	c.pet.State = "walking"
	c.pet.LastThought = "yarn!"
	petSnap := c.pet
	c.stateMu.Unlock()
	savePetStateData(petSnap)
	return true
}

// menuPosition carries mouse coordinates for positioning tmux display-menu
// at the exact click location (since renderers capture mouse events before tmux sees them)
type menuPosition struct {
	PaneID string // tmux pane ID where the click occurred
	X      int    // mouse X within the pane
	Y      int    // mouse Y within the pane
}

// menuPosArgs returns the tmux display-menu positioning flags
func (p menuPosition) args() []string {
	if p.PaneID != "" {
		return []string{
			"-t", p.PaneID,
			"-x", fmt.Sprintf("%d", p.X),
			"-y", fmt.Sprintf("%d", p.Y),
		}
	}
	// Fallback if no pane ID (shouldn't happen)
	return []string{"-x", "M", "-y", "M"}
}

// menuItemDef holds a menu item definition with its tmux command
type menuItemDef struct {
	Label     string
	Key       string
	Command   string // tmux command string to execute
	Separator bool
	Header    bool
}

var (
	markerOptionsOnce  sync.Once
	markerOptionsCache []daemon.MarkerOptionPayload
)

func markerOptions() []daemon.MarkerOptionPayload {
	markerOptionsOnce.Do(func() {
		catalog := kemoji.Gemoji()
		seen := make(map[string]struct{}, len(catalog))
		options := make([]daemon.MarkerOptionPayload, 0, len(catalog))
		for _, e := range catalog {
			symbol := strings.TrimSpace(e.Emoji)
			if symbol == "" {
				continue
			}
			name := strings.TrimSpace(e.Description)
			if name == "" && len(e.Aliases) > 0 {
				name = strings.ReplaceAll(strings.TrimSpace(e.Aliases[0]), "_", " ")
			}
			keywords := strings.Join(append(append([]string{}, e.Aliases...), e.Tags...), " ")
			if name == "" && strings.TrimSpace(keywords) == "" {
				continue
			}
			key := symbol + "|" + name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			options = append(options, daemon.MarkerOptionPayload{
				Symbol:   symbol,
				Name:     name,
				Keywords: keywords,
			})
		}
		markerOptionsCache = options
	})
	return markerOptionsCache
}

// parseTmuxMenuArgs extracts menu title and items from tmux display-menu arguments
func parseTmuxMenuArgs(args []string) (string, []menuItemDef) {
	var title string
	var items []menuItemDef

	// Find the title and the start of item triples
	itemStart := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "display-menu", "-O":
			continue
		case "-T":
			if i+1 < len(args) {
				title = args[i+1]
				i++
			}
		case "-t", "-x", "-y":
			i++ // skip value
		default:
			itemStart = i
			goto parseItems
		}
	}
parseItems:
	if itemStart < 0 {
		return title, items
	}
	// Parse triples: label, key, command
	for i := itemStart; i+2 < len(args); i += 3 {
		label := args[i]
		key := args[i+1]
		cmd := args[i+2]

		if label == "" && key == "" && cmd == "" {
			items = append(items, menuItemDef{Separator: true})
		} else if strings.HasPrefix(strings.TrimLeft(label, " "), "-") {
			trimmed := strings.TrimLeft(label, " ")
			indent := len(label) - len(trimmed)
			items = append(items, menuItemDef{
				Label:  strings.Repeat(" ", indent) + strings.TrimPrefix(trimmed, "-"),
				Header: true,
			})
		} else {
			items = append(items, menuItemDef{
				Label:   label,
				Key:     key,
				Command: cmd,
			})
		}
	}

	return title, items
}

// executeOrSendMenu sends the menu to the renderer or falls back to tmux display-menu.
// Pane-header clients (clientID starts with "header:") always use tmux display-menu
// since the 1-line pane is too small for overlay menus.
func (c *Coordinator) executeOrSendMenu(clientID string, args []string, pos menuPosition) {
	// Pane-header clients can't show overlay menus - use tmux display-menu
	isHeaderClient := isHeaderClient(clientID)

	if c.OnSendMenu != nil && !isHeaderClient {
		title, items := parseTmuxMenuArgs(args)
		logEvent("MENU_SEND client=%s title=%s items=%d", clientID, title, len(items))

		// Store items for later execution. Loop-only state (Step 5):
		// executeOrSendMenu and HandleMenuSelect both run on the loop
		// goroutine via HandleInput, so no mutex is needed.
		c.pendingMenus[clientID] = items

		// Convert to protocol items
		protoItems := make([]daemon.MenuItemPayload, len(items))
		for i, item := range items {
			protoItems[i] = daemon.MenuItemPayload{
				Label:     item.Label,
				Key:       item.Key,
				Separator: item.Separator,
				Header:    item.Header,
			}
		}

		c.OnSendMenu(clientID, &daemon.MenuPayload{
			Title: title,
			Items: protoItems,
			X:     pos.X,
			Y:     pos.Y,
		})
	} else {
		exec.Command("tmux", args...).Run()
	}
}

// HandleMenuSelect executes the tmux command for the selected menu item
func (c *Coordinator) HandleMenuSelect(clientID string, index int) {
	logEvent("MENU_SELECT_START client=%s index=%d", clientID, index)
	items, ok := c.pendingMenus[clientID]
	delete(c.pendingMenus, clientID)

	if !ok || index < 0 || index >= len(items) {
		logEvent("MENU_SELECT_SKIP client=%s index=%d ok=%v items=%d", clientID, index, ok, len(items))
		return
	}

	item := items[index]
	logEvent("MENU_SELECT_ITEM client=%s index=%d label=%s cmd=%s", clientID, index, item.Label, item.Command)
	if item.Command == "" || item.Separator || item.Header {
		return
	}

	if strings.HasPrefix(item.Command, "tabby-marker-picker:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			targetBytes, err := base64.StdEncoding.DecodeString(parts[2])
			if err == nil {
				title := "Set Marker"
				if parts[1] == "group" {
					title = "Set Group Marker"
				}
				c.openMarkerPicker(clientID, parts[1], string(targetBytes), title)
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-color-picker:") {
		parts := strings.SplitN(item.Command, ":", 4)
		if len(parts) >= 3 {
			targetBytes, err := base64.StdEncoding.DecodeString(parts[2])
			if err == nil {
				title := "Pick Color"
				if parts[1] == "group" {
					title = "Pick Group Color"
				}
				currentColor := ""
				if len(parts) == 4 {
					currentColor = parts[3]
				}
				c.openColorPicker(clientID, parts[1], string(targetBytes), title, currentColor)
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-set-window-color:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			if windowID := strings.TrimSpace(parts[1]); windowID != "" {
				c.setWindowColor(windowID, parts[2])
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-set-window-icon:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			if windowID := strings.TrimSpace(parts[1]); windowID != "" {
				c.setWindowIcon(windowID, parts[2])
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-new-window:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			groupBytes, gErr := base64.StdEncoding.DecodeString(parts[1])
			pathBytes, pErr := base64.StdEncoding.DecodeString(parts[2])
			if gErr == nil && pErr == nil {
				c.createNewWindowWithOverrides(clientID, string(groupBytes), string(pathBytes))
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-unlock-window-name:") {
		parts := strings.SplitN(item.Command, ":", 2)
		if len(parts) == 2 {
			if windowID := strings.TrimSpace(parts[1]); windowID != "" {
				// Drop the hard name lock so the tab returns to auto naming
				// (deterministic dir code + live summary). Names aren't persisted
				// by directory, so there is nothing on disk to forget — the group/
				// pinned record is left intact. tmux accepts the stable window ID
				// ("@123") as a -t target.
				exec.Command("tmux", "set-window-option", "-t", windowID, "-u", "@tabby_name_locked").Run()
			}
		}
		return
	}

	// Execute the tmux command via temp file (handles complex quoting correctly)
	executeTmuxCommand(item.Command)
}

// executeTmuxCommand executes a tmux command string by writing to a temp file
// and sourcing it, which correctly handles all quoting and escaping
func executeTmuxCommand(cmd string) {
	f, err := os.CreateTemp("", "tabby-cmd-*.conf")
	if err != nil {
		coordinatorDebugLog.Printf("Failed to create temp file for menu command: %v", err)
		return
	}
	defer os.Remove(f.Name())
	f.WriteString(cmd + "\n")
	f.Close()
	exec.Command("tmux", "source-file", f.Name()).Run()
}

func (c *Coordinator) openMarkerPicker(clientID, scope, target, title string) {
	if c.OnSendMarkerPicker == nil {
		return
	}
	options := markerOptions()
	if len(options) == 0 {
		return
	}
	c.OnSendMarkerPicker(clientID, &daemon.MarkerPickerPayload{
		Title:   title,
		Scope:   scope,
		Target:  target,
		Options: options,
	})
}

func (c *Coordinator) applyMarkerSelection(scope, target, markerValue string) bool {
	value := strings.TrimSpace(markerValue)

	if scope == "window" {
		windowID := strings.TrimSpace(target)
		if windowID == "" {
			return false
		}
		c.setWindowIcon(windowID, value)
		return true
	}

	if scope == "group" {
		return c.setGroupMarkerExact(target, value)
	}

	return false
}

func (c *Coordinator) setGroupMarkerExact(groupName, marker string) bool {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return false
	}

	configPath := config.DefaultConfigPath()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return false
	}
	group := config.FindGroup(cfg, groupName)
	if group == nil {
		return false
	}
	group.Theme.Icon = marker
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return false
	}

	c.stateMu.Lock()
	c.config = cfg
	applyContrastConfig(cfg)
	c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.stateMu.Unlock()
	return true
}

// handleRightClick shows appropriate context menu based on what was clicked
func (c *Coordinator) handleRightClick(clientID string, input *daemon.InputPayload) {
	coordinatorDebugLog.Printf("handleRightClick: clientID=%s action=%s target=%s pane=%s x=%d y=%d",
		clientID, input.ResolvedAction, input.ResolvedTarget, input.PaneID, input.MouseX, input.MouseY)

	pos := menuPosition{
		PaneID: input.PaneID,
		X:      input.MouseX,
		Y:      input.MouseY,
	}
	// For header clients, use SourcePaneID (the header pane itself) for positioning
	// so the menu appears at the click location. The content pane comes from ResolvedTarget.
	if strings.HasPrefix(clientID, "window-header:") {
		if input.SourcePaneID != "" {
			pos.PaneID = input.SourcePaneID
			coordinatorDebugLog.Printf("handleRightClick: header client, using SourcePaneID=%s", input.SourcePaneID)
		}
	}
	switch input.ResolvedAction {
	case "select_window", "toggle_panes", "window_menu":
		// If clicking on far left (X < 2), show indicator menu; otherwise show window menu
		if input.ResolvedAction != "window_menu" && input.MouseX < 2 {
			c.showIndicatorContextMenu(clientID, input.ResolvedTarget, pos)
		} else {
			c.showWindowContextMenu(clientID, input.ResolvedTarget, pos)
		}
	case "select_pane", "pane_menu", "pane_grow", "pane_shrink":
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
	case "toggle_group", "group_header", "group_menu":
		c.showGroupContextMenu(clientID, input.ResolvedTarget, pos)
	case "sidebar_header_area", "sidebar_settings":
		c.showSidebarSettingsMenu(clientID, pos)
	case "header_context", "header_split_v", "header_split_h", "header_close", "header_select_pane":
		// Right-click on pane header -> show pane context menu
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
	default:
		coordinatorDebugLog.Printf("handleRightClick: unhandled action=%q target=%q", input.ResolvedAction, input.ResolvedTarget)
	}
}

// showWindowContextMenu displays the context menu for a window
func (c *Coordinator) showWindowContextMenu(clientID string, windowTarget string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	win := findWindowByTarget(c.windows, windowTarget)
	if win == nil {
		return
	}

	// Target every command by the STABLE window ID ("@123"), never the tmux
	// index. The menu's commands run later (after the user picks an item and,
	// for rename, finishes typing); by then an earlier window may have closed or
	// opened and shifted the indices, so a baked-in ":%d" would hit the wrong
	// tab. tmux accepts the window ID as a -t target everywhere an index works.
	wid := win.ID

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
	}, pos.args()...)

	// Rename option - hard-locks the name (@tabby_name_locked) so syncWindowNames
	// and the live summary won't overwrite it. The lock lives on the window for
	// its lifetime; it is not persisted by directory.
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t %s -- '%%%%' ; set-window-option -t %s @tabby_name_locked 1\"", win.Name, wid, wid)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option - drops the per-window hard lock so the tab returns to
	// auto naming (deterministic dir code + live summary).
	unlockCmd := fmt.Sprintf("tabby-unlock-window-name:%s", wid)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Separator before group/appearance section
	args = append(args, "", "", "")

	// --- Group & Appearance section (all window-group-related options together) ---

	// Move to Group submenu
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range c.config.Groups {
		if group.Name == "Default" {
			continue
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			setGroupCmd := fmt.Sprintf("set-window-option -t %s @tabby_group '%s'", wid, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, setGroupCmd)
		}
	}

	// Remove from group option
	if win.Group != "" {
		removeCmd := fmt.Sprintf("set-window-option -t %s -u @tabby_group", wid)
		args = append(args, "  Remove from Group", "0", removeCmd)
	}

	// Set Color submenu
	args = append(args, "-Set Tab Color", "", "")
	colorTarget := base64.StdEncoding.EncodeToString([]byte(wid))
	currentColor := win.CustomColor
	colorPickerCmd := fmt.Sprintf("tabby-color-picker:window:%s:%s", colorTarget, currentColor)
	args = append(args, "  Custom Color...", "h", colorPickerCmd)
	customColorCmd := fmt.Sprintf("command-prompt -p 'Hex color (#rrggbb):' \"set-window-option -t %s @tabby_color '%%%%%%%%'\"", wid)
	args = append(args, "  Custom (Hex)...", "#", customColorCmd)
	resetColorCmd := fmt.Sprintf("set-window-option -t %s -u @tabby_color", wid)
	args = append(args, "  Reset to Default", "d", resetColorCmd)

	// Set Marker — opens the searchable emoji/icon picker (same as group menu)
	if !strings.HasPrefix(clientID, "window-header:") {
		target := base64.StdEncoding.EncodeToString([]byte(wid))
		searchCmd := fmt.Sprintf("tabby-marker-picker:window:%s", target)
		args = append(args, "Set Marker", "s", searchCmd)
		// Show remove option only if a marker is currently set
		if win.Icon != "" {
			resetIconCmd := fmt.Sprintf("tabby-set-window-icon:%s:", wid)
			// Key must differ from "Remove from Group" (also a candidate "0"):
			// tmux display-menu binds a duplicate mnemonic to the FIRST entry, so
			// sharing "0" made this item fire the group removal instead.
			args = append(args, "Remove Marker", "x", resetIconCmd)
		}
	}

	// Pin/Unpin option - pinned windows appear at the top of sidebar
	if win.Pinned {
		unpinCmd := fmt.Sprintf("set-window-option -t %s -u @tabby_pinned", wid)
		args = append(args, "Unpin from Top", "p", unpinCmd)
	} else {
		pinCmd := fmt.Sprintf("set-window-option -t %s @tabby_pinned 1", wid)
		args = append(args, "Pin to Top", "p", pinCmd)
	}

	// Minimize/Unminimize — route through the tabby hook so the window is PARKED
	// into the holding session (skipped by all native window nav), not just flagged.
	toggleMinCmd := fmt.Sprintf("run-shell '%s toggle-minimize-window -t %s'", c.getHookPath(), wid)
	if win.Minimized {
		args = append(args, "Unminimize", "m", toggleMinCmd)
	} else {
		args = append(args, "Minimize", "m", toggleMinCmd)
	}

	// --- Window actions section ---
	args = append(args, "", "", "")

	// Collapse/Expand panes option (only for windows with multiple panes)
	contentPaneCount := 0
	for _, pane := range win.Panes {
		if isAuxiliaryPane(pane) {
			continue
		}
		contentPaneCount++
	}
	if contentPaneCount > 1 {
		if win.Collapsed {
			expandCmd := fmt.Sprintf("set-window-option -t %s -u @tabby_collapsed", wid)
			args = append(args, "Expand Panes", "e", expandCmd)
		} else {
			collapseCmd := fmt.Sprintf("set-window-option -t %s @tabby_collapsed 1", wid)
			args = append(args, "Collapse Panes", "c", collapseCmd)
		}
	}

	// Split options - use active pane ID to avoid index issues with header panes
	activePaneID := ""
	for _, p := range win.Panes {
		if isAuxiliaryPane(p) {
			continue
		}
		if p.Active {
			activePaneID = p.ID
			break
		}
	}
	if activePaneID == "" {
		for _, p := range win.Panes {
			if isAuxiliaryPane(p) {
				continue
			}
			activePaneID = p.ID
			break
		}
	}
	if activePaneID == "" && len(win.Panes) > 0 {
		activePaneID = win.Panes[0].ID
	}
	splitTarget := wid
	if activePaneID != "" {
		splitTarget = activePaneID
	}
	if !c.isVerticalStackedPane(win, activePaneID) {
		splitVCmd := fmt.Sprintf("select-window -t %s ; select-pane -t %s ; split-window -h -c '#{pane_current_path}'", wid, splitTarget)
		splitHCmd := fmt.Sprintf("select-window -t %s ; select-pane -t %s ; split-window -v -c '#{pane_current_path}'", wid, splitTarget)
		args = append(args, "Split Vertical |", "|", splitVCmd)
		args = append(args, "Split Horizontal -", "-", splitHCmd)
	}

	// --- Utilities ---
	args = append(args, "", "", "")

	// Open in Finder
	openFinderCmd := "run-shell 'open \"#{pane_current_path}\"'"
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// --- Destructive ---
	args = append(args, "", "", "")

	hookPath := c.getHookPath()
	killCmd := fmt.Sprintf("confirm-before -p 'Close window? (y/n)' \"run-shell '%s kill-window %s'\"", hookPath, wid)
	args = append(args, "Kill", "k", killCmd)

	c.executeOrSendMenu(clientID, args, pos)
}

func (c *Coordinator) isVerticalStackedPane(win *tmux.Window, paneID string) bool {
	if win == nil || paneID == "" {
		return false
	}

	maxWidth := 0
	var target *tmux.Pane
	for i := range win.Panes {
		pane := &win.Panes[i]
		if isAuxiliaryPane(*pane) {
			continue
		}
		if pane.Width > maxWidth {
			maxWidth = pane.Width
		}
		if pane.ID == paneID {
			target = pane
		}
	}
	if target == nil || maxWidth == 0 {
		return false
	}
	if target.Width < maxWidth {
		return false
	}

	for i := range win.Panes {
		pane := &win.Panes[i]
		if isAuxiliaryPane(*pane) || pane.ID == paneID {
			continue
		}
		if pane.Width == target.Width && pane.Top != target.Top {
			return true
		}
	}

	return false
}

// showPaneContextMenu displays the context menu for a pane
func (c *Coordinator) showPaneContextMenu(clientID string, paneID string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the pane
	var pane *tmux.Pane
	var windowIdx int
	var window *tmux.Window
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				pane = &c.windows[i].Panes[j]
				window = &c.windows[i]
				windowIdx = c.windows[i].Index
				break
			}
		}
		if pane != nil {
			break
		}
	}
	if pane == nil || window == nil {
		return
	}

	// Use locked title, then title, then command for display
	paneLabel := pane.Command
	if pane.LockedTitle != "" {
		paneLabel = pane.LockedTitle
	} else if pane.Title != "" && pane.Title != pane.Command {
		paneLabel = pane.Title
	}

	menuArgs := []string{"display-menu"}
	if pos.PaneID != "" {
		menuArgs = append(menuArgs, "-M")
	}
	menuArgs = append(menuArgs, "-T", fmt.Sprintf("Pane %d.%d: %s", windowIdx, pane.Index, paneLabel))
	args := append(menuArgs, pos.args()...)

	// Rename option
	currentTitle := pane.LockedTitle
	if currentTitle == "" {
		currentTitle = pane.Title
	}
	if currentTitle == "" {
		currentTitle = pane.Command
	}
	renameCmd := fmt.Sprintf("command-prompt -I '%s' -p 'Pane name:' \"set-option -p -t %s @tabby_pane_title '%%%%'\"", currentTitle, pane.ID)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option
	unlockCmd := fmt.Sprintf("set-option -p -t %s -u @tabby_pane_title ; select-pane -t %s -T ''", pane.ID, pane.ID)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// For header clients, -t targets the header pane (for positioning), so #{pane_current_path}
	// would resolve to the header pane's path. Pre-resolve from the content pane instead.
	panePath := "#{pane_current_path}"
	if strings.HasPrefix(clientID, "window-header:") {
		if out, err := exec.Command("tmux", "display-message", "-t", pane.ID, "-p", "#{pane_current_path}").Output(); err == nil {
			resolved := strings.TrimSpace(string(out))
			if resolved != "" {
				panePath = resolved
			}
		}
	}

	// Separator
	args = append(args, "", "", "")

	// Split options
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '%s'", windowIdx, pane.ID, panePath)
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '%s'", windowIdx, pane.ID, panePath)
	args = append(args, "Split Vertical |", "|", splitVCmd)
	args = append(args, "Split Horizontal -", "-", splitHCmd)

	contentCount := 0
	for _, p := range window.Panes {
		if !isAuxiliaryPane(p) {
			contentCount++
		}
	}
	if contentCount > 1 {
		{
			hookPath := c.getHookPath()
			args = append(args, "", "", "")
			collapsedVal, _ := exec.Command("tmux", "show-options", "-pqv", "-t", pane.ID, "@tabby_pane_collapsed").Output()
			collapseLabel := "Collapse Pane"
			if strings.TrimSpace(string(collapsedVal)) == "1" {
				collapseLabel = "Expand Pane"
			}
			args = append(args, collapseLabel, "c", fmt.Sprintf("run-shell '%s toggle-pane-collapse -t %s'", hookPath, pane.ID))
		}
		args = append(args, "", "", "")
		args = append(args, "Resize Down", "j", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -D 5", windowIdx, pane.ID))
		args = append(args, "Resize Up", "k", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -U 5", windowIdx, pane.ID))
		args = append(args, "Resize Right", "l", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -R 5", windowIdx, pane.ID))
		args = append(args, "Resize Left", "h", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -L 5", windowIdx, pane.ID))
	}

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", windowIdx, pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Break pane to new window (preserving group assignment)
	breakCmd := fmt.Sprintf("break-pane -s %s", pane.ID)
	// Find the group this window belongs to and assign it to the new window
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Index == windowIdx && group.Name != "" {
				breakCmd += fmt.Sprintf(" ; set-window-option @tabby_group '%s'", group.Name)
				break
			}
		}
	}
	args = append(args, "Break to New Window", "b", breakCmd)

	// Move to Group submenu
	args = append(args, "", "", "") // Separator
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range c.config.Groups {
		if group.Name == "Default" {
			continue
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			moveCmd := fmt.Sprintf("break-pane -s %s ; set-window-option @tabby_group '%s'", pane.ID, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, moveCmd)
		}
	}

	// Remove from group option (if pane's window has a group)
	windowGroup := ""
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Index == windowIdx && group.Name != "" {
				windowGroup = group.Name
				break
			}
		}
	}
	if windowGroup != "" {
		removeCmd := fmt.Sprintf("break-pane -s %s ; set-window-option -u @tabby_group", pane.ID)
		args = append(args, "  Remove from Group", "0", removeCmd)
	}

	// Open in Finder
	openFinderCmd := fmt.Sprintf("run-shell 'open \"%s\"'", panePath)
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Close pane (save layout first to preserve sibling ratios)
	{
		hookPath := c.getHookPath()
		args = append(args, "Close Pane", "x", fmt.Sprintf("run-shell '%s kill-pane -t %s'", hookPath, pane.ID))
	}

	c.executeOrSendMenu(clientID, args, pos)
}

// showGroupContextMenu displays the context menu for a group header
func (c *Coordinator) showGroupContextMenu(clientID string, groupName string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the group
	var group *grouping.GroupedWindows
	for i := range c.grouped {
		if c.grouped[i].Name == groupName {
			group = &c.grouped[i]
			break
		}
	}
	if group == nil {
		return
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Group: %s (%d windows)", group.Name, len(group.Windows)),
	}, pos.args()...)

	// Get working directory for new windows in this group
	var workingDir string
	coordinatorDebugLog.Printf("showGroupContextMenu: looking for working_dir for group=%s, config has %d groups", group.Name, len(c.config.Groups))
	for _, cfgGroup := range c.config.Groups {
		coordinatorDebugLog.Printf("  checking cfgGroup=%s workingDir=%s", cfgGroup.Name, cfgGroup.WorkingDir)
		if cfgGroup.Name == group.Name && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			coordinatorDebugLog.Printf("  MATCH! workingDir=%s", workingDir)
			// Expand ~ to home directory
			if strings.HasPrefix(workingDir, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					workingDir = filepath.Join(home, workingDir[2:])
					coordinatorDebugLog.Printf("  expanded to=%s", workingDir)
				}
			}
			break
		}
	}

	newWindowPath := "#{pane_current_path}"
	if workingDir != "" {
		newWindowPath = workingDir
	}
	coordinatorDebugLog.Printf("showGroupContextMenu: final path=%s", newWindowPath)

	encodedGroup := base64.StdEncoding.EncodeToString([]byte(group.Name))
	encodedPath := base64.StdEncoding.EncodeToString([]byte(newWindowPath))
	newWindowCmd := fmt.Sprintf("tabby-new-window:%s:%s", encodedGroup, encodedPath)
	if group.Name != "Default" {
		args = append(args, fmt.Sprintf("New %s Window", group.Name), "n", newWindowCmd)
	} else {
		args = append(args, "New Window", "n", newWindowCmd)
	}

	hookPath := c.getHookPath()

	if c.collapsedGroups[group.Name] {
		expandCmd := fmt.Sprintf("run-shell '%s toggle-group-collapse \\\"%s\\\" expand'", hookPath, group.Name)
		args = append(args, "Expand Group", "e", expandCmd)
	} else {
		collapseCmd := fmt.Sprintf("run-shell '%s toggle-group-collapse \\\"%s\\\" collapse'", hookPath, group.Name)
		args = append(args, "Collapse Group", "c", collapseCmd)
	}

	// --- Group settings section ---
	args = append(args, "", "", "")

	renameCmd := fmt.Sprintf(
		"command-prompt -I '%s' -p 'New name:' \"run-shell '%s rename-group \\\"%s\\\" \\\"%%%%\\\"'\"",
		group.Name, hookPath, group.Name,
	)
	args = append(args, "Rename", "r", renameCmd)

	args = append(args, "-Change Color", "", "")
	colorOptions := []struct {
		name string
		hex  string
		key  string
	}{
		{"Red", "#e74c3c", "r"},
		{"Orange", "#e67e22", "o"},
		{"Yellow", "#f1c40f", "y"},
		{"Green", "#27ae60", "g"},
		{"Blue", "#3498db", "b"},
		{"Purple", "#9b59b6", "p"},
		{"Pink", "#e91e63", "i"},
		{"Cyan", "#00bcd4", "c"},
		{"Gray", "#7f8c8d", "a"},
		{"Transparent", "transparent", "t"},
	}
	if !c.config.Sidebar.HidePredefinedColors {
		for _, color := range colorOptions {
			setColorCmd := fmt.Sprintf("run-shell '%s set-group-color \\\"%s\\\" \\\"%s\\\"'", hookPath, group.Name, color.hex)
			args = append(args, fmt.Sprintf("  %s", color.name), color.key, setColorCmd)
		}
	}

	groupColorTarget := base64.StdEncoding.EncodeToString([]byte(group.Name))
	groupCurrentColor := group.Theme.Bg
	colorPickerCmd := fmt.Sprintf("tabby-color-picker:group:%s:%s", groupColorTarget, groupCurrentColor)
	args = append(args, "  Custom Color...", "h", colorPickerCmd)
	customColorCmd := fmt.Sprintf(
		"command-prompt -p 'Hex color (#rrggbb):' \"run-shell '%s set-group-color \\\"%s\\\" \\\"%%%%%%%%\\\"'\"",
		hookPath, group.Name,
	)
	args = append(args, "  Custom (Hex)...", "#", customColorCmd)

	canShowMarkerPicker := c.OnSendMenu != nil && !strings.HasPrefix(clientID, "window-header:")
	if canShowMarkerPicker {
		groupTarget := base64.StdEncoding.EncodeToString([]byte(group.Name))
		searchCmd := fmt.Sprintf("tabby-marker-picker:group:%s", groupTarget)
		args = append(args, "Set Marker", "m", searchCmd)
		currentIcon := strings.TrimSpace(group.Theme.Icon)
		if currentIcon != "" {
			removeIconCmd := fmt.Sprintf("run-shell '%s set-group-marker \\\"%s\\\" \\\"\\\"'", hookPath, group.Name)
			args = append(args, "Remove Marker", "0", removeIconCmd)
		}
	}

	{
		currentWorkingDir := workingDir
		if currentWorkingDir == "" {
			currentWorkingDir = "~"
		}
		setWorkingDirCmd := fmt.Sprintf(
			"command-prompt -I '%s' -p 'Working directory:' \"run-shell '%s set-group-working-dir \\\"%s\\\" \\\"%%%%\\\"'\"",
			currentWorkingDir, hookPath, group.Name,
		)
		args = append(args, "Set Working Directory", "w", setWorkingDirCmd)
	}

	newGroupCmd := fmt.Sprintf(
		"command-prompt -p 'New group name:' \"run-shell '%s new-group %%%%  '\"",
		hookPath,
	)
	args = append(args, "Create New Group", "G", newGroupCmd)

	// --- Destructive actions ---
	args = append(args, "", "", "")

	deleteCmd := fmt.Sprintf(
		"confirm-before -p 'Delete group %s? (y/n)' \"run-shell '%s delete-group \\\"%s\\\"'\"",
		group.Name, hookPath, group.Name,
	)
	args = append(args, "Delete Group", "d", deleteCmd)

	// Close all windows in group (only if group has windows)
	if len(group.Windows) > 0 {
		var killCmds []string
		for _, win := range group.Windows {
			killCmds = append(killCmds, fmt.Sprintf("kill-window -t %s", win.ID))
		}
		killAllCmd := strings.Join(killCmds, " ; ")
		confirmCmd := fmt.Sprintf(`confirm-before -p "Close all %d windows in %s? (y/n)" "%s"`,
			len(group.Windows), group.Name, killAllCmd)
		args = append(args, "Close All Windows", "x", confirmCmd)
	}

	c.executeOrSendMenu(clientID, args, pos)
}

// createNewWindowInCurrentGroup creates a new window in the same group as the
// current window, using the group's configured working_dir if available.
//
// Delegates to the bin/new-window binary for atomic creation: the sidebar
// renderer is spawned BEFORE the user sees the window, eliminating the
// "spazzing" UX issue caused by the old hook-storm approach.
func (c *Coordinator) createNewWindowInCurrentGroup(clientID string) {
	logEvent("NEW_WINDOW_START client=%s", clientID)

	// Query tmux for active window ID BEFORE acquiring the lock to avoid
	// holding stateMu during external I/O.
	windowID := clientID
	if out, err := tmuxOutputCtx("display-message", "-p", "#{window_id}"); err == nil {
		if id := strings.TrimSpace(string(out)); id != "" {
			windowID = id
		}
	}

	c.stateMu.RLock()

	var currentGroup string
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == windowID {
				currentGroup = group.Name
				break
			}
		}
		if currentGroup != "" {
			break
		}
	}

	// Look up working directory from config
	var workingDir string
	for _, cfgGroup := range c.config.Groups {
		if cfgGroup.Name == currentGroup && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			break
		}
	}

	c.stateMu.RUnlock()

	// Resolve working directory (expand ~/)
	if workingDir != "" && strings.HasPrefix(workingDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			workingDir = filepath.Join(home, workingDir[2:])
		}
	}

	c.createNewWindowWithOverrides(clientID, currentGroup, workingDir)
}

func (c *Coordinator) createNewWindowWithOverrides(clientID, currentGroup, workingDir string) {
	logEvent("NEW_WINDOW_CREATE client=%s group=%s path=%s", clientID, currentGroup, workingDir)

	// Capture the firing client's TTY: the click came from a window-header
	// in some window, and the client viewing that window is the one that
	// will follow the new-window switch. preferredWindowFocusTarget uses
	// this to scope the post-spawn focus-restore to that one client and
	// avoid yanking other attached clients on every multi-client elector
	// flip (which produced the "+ then cycles other windows" bug).
	firingTTY := ""
	if strings.HasPrefix(clientID, "window-header:") {
		sourceWindowID := strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
		firingTTY = strings.TrimSpace(clientTTYForWindow(sourceWindowID))
	}

	c.SetNewWindowInFlight(currentGroup, workingDir, firingTTY)

	// Find the new-window binary (sibling of this daemon binary)
	newWindowBin := ""
	if exe, err := os.Executable(); err == nil {
		newWindowBin = filepath.Join(filepath.Dir(exe), "new-window")
	}

	if newWindowBin != "" {
		if _, err := os.Stat(newWindowBin); err == nil {
			args := []string{"-session", c.sessionID}
			sourceWindowID := ""
			if strings.HasPrefix(clientID, "window-header:") {
				sourceWindowID = strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
			}
			if sourceTTY := strings.TrimSpace(clientTTYForWindow(sourceWindowID)); sourceTTY != "" {
				args = append(args, "-client-tty", sourceTTY)
			}
			if currentGroup != "" {
				args = append(args, "-group", currentGroup)
			}
			if workingDir != "" {
				args = append(args, "-path", workingDir)
			}
			if c.sidebarHidden {
				args = append(args, "-no-sidebar")
			}
			logEvent("NEW_WINDOW_BINARY bin=%s session=%s group=%s", newWindowBin, c.sessionID, currentGroup)
			out, err := exec.Command(newWindowBin, args...).CombinedOutput()
			if err != nil {
				logEvent("NEW_WINDOW_BINARY_ERR err=%v (falling back to legacy)", err)
				c.ClearNewWindowStatus()
				// Fall through to legacy path below
				newWindowBin = ""
			} else {
				newID := strings.TrimSpace(string(out))
				if newID == "" {
					logEvent("NEW_WINDOW_BINARY_ERR err=empty_window_id (falling back to legacy)")
					c.ClearNewWindowStatus()
					newWindowBin = ""
				} else {
					c.SetNewWindowReady(newID)
					return
				}
			}
		}
	}

	// Legacy fallback: create window (focused), assign group, let hook chain handle renderer.
	// Used when bin/new-window is not built (e.g. fresh clone without install.sh).
	logEvent("NEW_WINDOW_LEGACY session=%s group=%s", c.sessionID, currentGroup)
	args := []string{"new-window", "-P", "-F", "#{window_id}", "-t", c.sessionID + ":"}
	if workingDir != "" {
		args = append(args, "-c", workingDir)
	}

	out, err := exec.Command("tmux", args...).CombinedOutput()
	newWindowIDLegacy := strings.TrimSpace(string(out))
	logEvent("NEW_WINDOW_LEGACY_RESULT id=%s err=%v", newWindowIDLegacy, err)

	if newWindowIDLegacy != "" && currentGroup != "" && currentGroup != "Default" {
		exec.Command("tmux", "set-window-option", "-t", newWindowIDLegacy, "@tabby_group", currentGroup).Run()
	}

	if newWindowIDLegacy != "" {
		if err := c.SelectWindow(newWindowIDLegacy, "new_window_legacy", "create_new_window"); err != nil {
			logEvent("NEW_WINDOW_LEGACY_SELECT_ERR id=%s err=%v", newWindowIDLegacy, err)
			c.ClearNewWindowStatus()
			return
		}
		c.SetNewWindowReady(newWindowIDLegacy)
		return
	}
	c.ClearNewWindowStatus()
}

// showIndicatorContextMenu displays the context menu for window indicators (busy, bell, etc.)
func (c *Coordinator) showIndicatorContextMenu(clientID string, windowTarget string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	win := findWindowByTarget(c.windows, windowTarget)
	if win == nil {
		return
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Alerts: Window %d", win.Index),
	}, pos.args()...)

	// Busy indicator toggle
	if win.Busy {
		clearBusyCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_busy", win.Index)
		args = append(args, "Clear Busy", "b", clearBusyCmd)
	} else {
		setBusyCmd := fmt.Sprintf("set-window-option -t :%d @tabby_busy 1", win.Index)
		args = append(args, "Set Busy", "b", setBusyCmd)
	}

	// Input indicator toggle
	if win.Input {
		clearInputCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_input", win.Index)
		args = append(args, "Clear Input Needed", "i", clearInputCmd)
	} else {
		setInputCmd := fmt.Sprintf("set-window-option -t :%d @tabby_input 1", win.Index)
		args = append(args, "Set Input Needed", "i", setInputCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Bell indicator toggle
	if win.Bell {
		clearBellCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_bell", win.Index)
		args = append(args, "Clear Bell", "l", clearBellCmd)
	} else {
		setBellCmd := fmt.Sprintf("set-window-option -t :%d @tabby_bell 1", win.Index)
		args = append(args, "Trigger Bell", "l", setBellCmd)
	}

	// Activity indicator toggle
	if win.Activity {
		clearActivityCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_activity", win.Index)
		args = append(args, "Clear Activity", "a", clearActivityCmd)
	} else {
		setActivityCmd := fmt.Sprintf("set-window-option -t :%d @tabby_activity 1", win.Index)
		args = append(args, "Set Activity", "a", setActivityCmd)
	}

	// Silence indicator toggle
	if win.Silence {
		clearSilenceCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_silence", win.Index)
		args = append(args, "Clear Silence", "s", clearSilenceCmd)
	} else {
		setSilenceCmd := fmt.Sprintf("set-window-option -t :%d @tabby_silence 1", win.Index)
		args = append(args, "Set Silence", "s", setSilenceCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Clear all indicators
	clearAllCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_busy ; set-window-option -t :%d -u @tabby_input ; set-window-option -t :%d -u @tabby_bell ; set-window-option -t :%d -u @tabby_activity ; set-window-option -t :%d -u @tabby_silence", win.Index, win.Index, win.Index, win.Index, win.Index)
	args = append(args, "Clear All Alerts", "c", clearAllCmd)

	c.executeOrSendMenu(clientID, args, pos)
}

// getToggleScript returns the `tabby toggle` invocation (binary path + subcommand).
// The standalone tabby-toggle binary no longer exists; everything is one binary.
func (c *Coordinator) getToggleScript() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// Callers run this as an unquoted shell command (it contains a space).
	return filepath.Join(filepath.Dir(exe), "tabby") + " toggle"
}

// getHookPath returns the `tabby hook` invocation (binary path + subcommand).
// The standalone tabby-hook binary no longer exists; everything is one binary.
// Callers append the hook subcommand, e.g. fmt.Sprintf("...'%s kill-pane'", hookPath).
func (c *Coordinator) getHookPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "tabby hook"
	}
	return filepath.Join(filepath.Dir(exe), "tabby") + " hook"
}

func (c *Coordinator) getScriptPath(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "scripts", name)
}

// showSidebarSettingsMenu displays a context menu for sidebar settings
func (c *Coordinator) showSidebarSettingsMenu(clientID string, pos menuPosition) {
	toggleScript := c.getToggleScript()

	// Build restart command: toggle off, wait, toggle on (runs in background via tmux)
	restartCmd := func(setCmd string) string {
		if toggleScript == "" {
			return setCmd
		}
		return fmt.Sprintf("%s; run-shell -b \"%s; sleep 0.3; %s\"", setCmd, toggleScript, toggleScript)
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", "Sidebar Settings",
	}, pos.args()...)

	// Position options (restart sidebar to move it)
	args = append(args, "Position: Left", "l", restartCmd("set-option -g @tabby_sidebar_position left"))
	args = append(args, "Position: Right", "r", restartCmd("set-option -g @tabby_sidebar_position right"))

	// Separator
	args = append(args, "", "", "")

	// Mode options (restart to apply)
	args = append(args, "Mode: Full Height", "f", restartCmd("set-option -g @tabby_sidebar_mode full"))
	args = append(args, "Mode: Partial", "p", restartCmd("set-option -g @tabby_sidebar_mode partial"))

	// Separator
	args = append(args, "", "", "")

	// Pane headers toggle (no restart needed, daemon handles it)
	args = append(args, "Pane Headers: On", "h", "set-option -g @tabby_pane_headers on")
	args = append(args, "Pane Headers: Off", "o", "set-option -g @tabby_pane_headers off")

	// Separator
	args = append(args, "", "", "")

	// Prefix mode toggle (flat window list with group prefixes vs hierarchy)
	c.stateMu.RLock()
	prefixMode := c.config.Sidebar.PrefixMode
	c.stateMu.RUnlock()
	if prefixMode {
		args = append(args, "Display: Prefix Mode", "d", "set-option -g @tabby_prefix_mode 0")
	} else {
		args = append(args, "Display: Grouped", "d", "set-option -g @tabby_prefix_mode 1")
	}

	// Separator
	args = append(args, "", "", "")

	// Reset width (set to 25 and sync all sidebars)
	resetCmd := `set-option -gq @tabby_sidebar_width 25; run-shell -b 'for p in $(tmux list-panes -a -F "#{pane_id}|#{pane_start_command}" | grep sidebar-renderer | cut -d"|" -f1); do tmux resize-pane -t $p -x 25; done'`
	args = append(args, "Reset Width (25)", "w", resetCmd)

	// Sync Width toggle
	var win *tmux.Window
	c.stateMu.RLock()
	for i := range c.windows {
		for _, p := range c.windows[i].Panes {
			if p.ID == pos.PaneID {
				win = &c.windows[i]
				break
			}
		}
		if win != nil {
			break
		}
	}
	c.stateMu.RUnlock()

	if win != nil {
		args = append(args, "", "", "")
		if win.SyncWidth {
			args = append(args, "Sync Width: On", "s", fmt.Sprintf("set-window-option -t :%d @tabby_sync_width 0", win.Index))
		} else {
			snapCmd := fmt.Sprintf("set-window-option -t :%d @tabby_sync_width 1 ; run-shell -b 'tmux resize-pane -t %s -x %d'",
				win.Index, pos.PaneID, c.globalWidth)
			args = append(args, "Sync Width: Off", "s", snapCmd)
		}
	}

	c.executeOrSendMenu(clientID, args, pos)
}

// handleKeyInput processes keyboard events
func (c *Coordinator) handleKeyInput(clientID string, input *daemon.InputPayload) {
	switch input.Key {
	case "r":
		c.RefreshWindows()
	case "R":
		cfg, err := config.LoadConfig(config.DefaultConfigPath())
		if err == nil {
			activeWindowID := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
			c.stateMu.Lock()
			c.config = cfg
			applyContrastConfig(cfg)
			c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
			c.computeVisualPositions()
			moves := c.syncWindowIndices()
			c.stateMu.Unlock()
			// Execute deferred window move ops outside the lock.
			for _, op := range moves {
				tmuxRun("move-window", "-s", op.src, "-t", op.dst)
			}
			// Mirror the gating in RefreshWindows: only restore focus when
			// move-window actually renumbered something, since that's the
			// only case the restore was designed for. See coordinator.go
			// near the matching block in RefreshWindows for the full
			// rationale and the post-`+` cycling bug it fixes.
			if len(moves) > 0 {
				if focusTarget := preferredWindowFocusTarget(c, activeWindowID); focusTarget != "" {
					restoreWindowFocus(focusTarget)
				}
			}
		}
	case "m":
		// Open marker picker for active window
		c.stateMu.RLock()
		activeWindowIndex := -1
		for i := range c.windows {
			if c.windows[i].Active {
				activeWindowIndex = c.windows[i].Index
				break
			}
		}
		c.stateMu.RUnlock()

		if activeWindowIndex >= 0 {
			c.openMarkerPicker(clientID, "window", strconv.Itoa(activeWindowIndex), "Set Marker")
		}
	}
}

// triggerActionFromThought parses an LLM thought and triggers matching pet behavior
func (c *Coordinator) triggerActionFromThought(thought string, maxX int) {
	lowerThought := strings.ToLower(thought)

	// Skip if already doing something
	if c.pet.State != "idle" || c.pet.HasTarget {
		return
	}

	// Map keywords to actions
	// Walking/exploring
	if strings.Contains(lowerThought, "wander") ||
		strings.Contains(lowerThought, "explor") ||
		strings.Contains(lowerThought, "roam") ||
		strings.Contains(lowerThought, "patrol") ||
		strings.Contains(lowerThought, "walk") ||
		strings.Contains(lowerThought, "going") ||
		strings.Contains(lowerThought, "hunt") ||
		strings.Contains(lowerThought, "stalk") ||
		strings.Contains(lowerThought, "prey") ||
		strings.Contains(lowerThought, "creep") ||
		strings.Contains(lowerThought, "sniff") ||
		strings.Contains(lowerThought, "prowl") ||
		strings.Contains(lowerThought, "move") {
		c.pet.State = "walking"
		c.pet.Direction = []int{-1, 1}[rand.Intn(2)]
		targetX := rand.Intn(maxX)
		c.pet.TargetPos = pos2D{X: targetX, Y: 0}
		c.pet.HasTarget = true
		return
	}

	// Jumping
	if strings.Contains(lowerThought, "jump") ||
		strings.Contains(lowerThought, "leap") ||
		strings.Contains(lowerThought, "bounce") ||
		strings.Contains(lowerThought, "pounce") ||
		strings.Contains(lowerThought, "air") ||
		strings.Contains(lowerThought, "zoom") {
		c.pet.State = "jumping"
		c.pet.Pos.Y = 2
		return
	}

	// Playing with yarn
	if strings.Contains(lowerThought, "yarn") ||
		strings.Contains(lowerThought, "play") ||
		strings.Contains(lowerThought, "chase") ||
		strings.Contains(lowerThought, "catch") {
		if c.pet.YarnPos.X >= 0 {
			c.pet.TargetPos = pos2D{X: c.pet.YarnPos.X, Y: 0}
			c.pet.HasTarget = true
			c.pet.ActionPending = "play"
			c.pet.State = "walking"
		}
		return
	}

	// Happy/content
	if strings.Contains(lowerThought, "happy") ||
		strings.Contains(lowerThought, "content") ||
		strings.Contains(lowerThought, "purr") ||
		strings.Contains(lowerThought, "nice") ||
		strings.Contains(lowerThought, "good") {
		c.pet.State = "happy"
		return
	}

	// Sleepy/nap
	if strings.Contains(lowerThought, "nap") ||
		strings.Contains(lowerThought, "sleep") ||
		strings.Contains(lowerThought, "tired") ||
		strings.Contains(lowerThought, "zzz") ||
		strings.Contains(lowerThought, "rest") {
		c.pet.State = "sleeping"
		return
	}
}

// Default pet thoughts by state
var defaultPetThoughts = map[string][]string{
	"hungry":      {"food. now.", "the bowl. it echoes.", "starving. dramatically.", "hunger level: critical."},
	"poop":        {"that won't clean itself.", "i made you a gift.", "cleanup crew needed.", "ahem. the floor."},
	"happy":       {"acceptable.", "fine. you may stay.", "feeling good.", "not bad.", "this is nice."},
	"yarn":        {"the yarn. it calls.", "must... catch...", "yarn acquired.", "got it!"},
	"sleepy":      {"nap time.", "zzz...", "five more minutes.", "so tired."},
	"idle":        {"chillin'.", "vibin'.", "just here.", "sup.", "...", "waiting.", "*yawn*", "hmm."},
	"walking":     {"exploring.", "on the move.", "wandering.", "going places."},
	"jumping":     {"wheee!", "boing!", "up up up!", "airborne."},
	"petting":     {"mmm...", "yes, there.", "acceptable.", "more.", "don't stop.", "nice."},
	"starving":    {"this is it.", "so hungry...", "fading...", "remember me.", "tell them... i was good."},
	"guilt":       {"i trusted you.", "is this how it ends?", "the neglect.", "you did this.", "betrayal."},
	"dead":        {"...", "x_x", "[silence]", "gone.", "rip."},
	"mouse_spot":  {"intruder.", "prey detected.", "nature calls.", "the hunt begins.", "i see you."},
	"mouse_chase": {"can't escape.", "almost...", "you're mine.", "gotcha soon.", "so close..."},
	"mouse_catch": {"victory.", "natural order.", "delicious chaos.", "another conquest.", "the circle of life."},
	"mouse_kill":  {"blender time.", "yeet into void.", "tiny skateboard accident.", "spontaneous combustion.", "piano from above.", "anvil delivery.", "surprise trapdoor.", "rocket malfunction."},
	"poop_jump":   {"ew ew ew!", "not stepping in that.", "parkour!", "leap of faith.", "over the obstacle.", "nope.", "gross.", "hygiene first."},
	"wakeup":      {"*yawn*", "good nap.", "what year is it?", "back online.", "rested.", "that was nice.", "5 more minutes...", "ok ok i'm up."},
}

// randomThought returns a random thought from the given category
func randomThought(category string) string {
	thoughts, ok := defaultPetThoughts[category]
	if !ok || len(thoughts) == 0 {
		thoughts = defaultPetThoughts["idle"]
	}
	return thoughts[rand.Intn(len(thoughts))]
}

// GetSidebarBg returns the configured sidebar background color.
func (c *Coordinator) GetSidebarBg() string {
	// Config override takes priority
	if c.config.Sidebar.Colors.Bg != "" {
		return c.config.Sidebar.Colors.Bg
	}
	// Then use theme
	if c.theme != nil {
		return c.theme.SidebarBg
	}
	// Fallback to detector
	return c.bgDetector.GetDefaultSidebarBg()
}

// titleLooksLikeShellPrompt reports whether a pane title is just an echo of
// a shell prompt ("user@host[: path]" or bare "host") rather than useful
// user-set context. Suppressed on SSH windows since the host is already on
// row 1 and the pattern is almost always just PS1 noise.
var shellPromptRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+(:.*)?$`)

func titleLooksLikeShellPrompt(title, host string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	if strings.EqualFold(t, host) {
		return true
	}
	return shellPromptRe.MatchString(t)
}

// dropHostSegment removes any " | "-separated segment that equals (or after
// stripping "ssh "/"mosh " equals) the given host. Used on auto-generated
// window names like "client-studiodome | hosting-questions" so the row-2
// continuation shows only the local dirs.
func dropHostSegment(name, host string) string {
	if !strings.Contains(name, " | ") {
		return name
	}
	parts := strings.Split(name, " | ")
	keep := parts[:0]
	for _, p := range parts {
		seg := strings.TrimSpace(stripRemotePrefix(p))
		if strings.EqualFold(seg, host) {
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, " | ")
}

// stripRemotePrefix removes a leading "ssh " or "mosh " from a window name so
// we can detect when the name is just the auto-renamed remote command.
func stripRemotePrefix(name string) string {
	for _, p := range []string{"ssh ", "mosh "} {
		if strings.HasPrefix(name, p) {
			return strings.TrimSpace(strings.TrimPrefix(name, p))
		}
	}
	return name
}

// remoteTabDisplay decides how a remote (ssh/mosh) window's label is shown.
//   - Legacy mode (config ssh_icon empty): the tab line shows the HOST and the
//     local dir name(s) drop to a continuation row below it.
//   - Icon mode (ssh_icon set): everything collapses to ONE line — just the local
//     dir name, no host row. The ssh glyph itself is NOT added here; it's placed
//     ahead of the group marker by the caller (see sshTabGlyph) so a remote tab
//     reads "<glyph> <marker> <name>" rather than eating two rows.
//
// It returns the (possibly rewritten) tab name and the continuation-row text; an
// empty continuation means "no second row". Local windows pass through unchanged.
func (c *Coordinator) remoteTabDisplay(win tmux.Window, displayName string) (name, continuation string) {
	if win.RemoteHost == "" {
		return displayName, ""
	}
	local := dropHostSegment(stripRemotePrefix(displayName), win.RemoteHost)
	if strings.EqualFold(local, win.RemoteHost) {
		local = ""
	}
	if icon := strings.TrimSpace(c.config.Sidebar.SSHIcon); icon != "" {
		label := local
		if label == "" {
			label = win.RemoteHost // window name was just the host — keep it as the label
		}
		return label, ""
	}
	return win.RemoteHost, local
}

// sshTabGlyph returns the configured ssh glyph for a remote window in icon mode,
// or "" for a local window / legacy (host-row) mode. Callers place it BEFORE the
// group marker so the remote indicator sits in the slot ahead of the marker
// rather than between the marker and the tab name.
func (c *Coordinator) sshTabGlyph(win tmux.Window) string {
	if win.RemoteHost == "" {
		return ""
	}
	return strings.TrimSpace(c.config.Sidebar.SSHIcon)
}

// composeTabMarker builds the "<ssh glyph> <group marker> " prefix that precedes a
// tab's name. Either piece may be empty; the ssh glyph, when present, always comes
// first so it occupies the slot ahead of the marker.
func composeTabMarker(sshGlyph, marker string) string {
	switch {
	case sshGlyph != "" && marker != "":
		return sshGlyph + " " + marker + " "
	case sshGlyph != "":
		return sshGlyph + " "
	case marker != "":
		return marker + " "
	}
	return ""
}

// writeRemoteNameRow writes the window-name continuation row below the main
// tab line for ssh/mosh windows. Layout: " │ <bg-padded-chip>" — one tree
// pipe in tree color, a sidebar-bg gap, then the chip (tab bg) starting
// with one bg-only column before the name and extending to the right edge.
// When isLast is true (last sibling in its group), the column-1 pipe is
// replaced with a blank so the tree doesn't continue into nothing.
func (c *Coordinator) writeRemoteNameRow(s *strings.Builder, name string, width int, bgColor, fgColor string, treeStyle lipgloss.Style, treeContinueChar string, faint, isLast bool) {
	col1 := treeContinueChar
	if isLast {
		col1 = " "
	}
	leading := " " + col1 + " "
	leadingW := lipgloss.Width(leading)
	if width <= leadingW+1 {
		return
	}
	// Reserve the same 2-col menu-button area the tab (line 1) reserves, and
	// gradient over the SAME width, so the wrapped row's gradient lines up
	// column-for-column with line 1 instead of stretching 2 cols wider.
	const menuBtnW = 2 // " ⋮"
	avail := width - leadingW - menuBtnW
	if avail < 1 {
		avail = 1
	}
	chipText := " " + name // 1 col of bg-only padding before the name (tight, for narrow sidebars)
	if lipgloss.Width(chipText) > avail {
		truncated := ""
		for _, r := range chipText {
			if lipgloss.Width(truncated+string(r)) > avail-1 {
				break
			}
			truncated += string(r)
		}
		chipText = truncated + "~"
	}
	nameStyle := lipgloss.NewStyle()
	if fgColor != "" {
		nameStyle = nameStyle.Foreground(lipgloss.Color(fgColor))
	}
	if bgColor != "" {
		nameStyle = nameStyle.Background(lipgloss.Color(bgColor))
	}
	if faint {
		nameStyle = nameStyle.Faint(true)
		treeStyle = treeStyle.Faint(true)
	}
	leadingRendered := " " + treeStyle.Render(col1) + " "
	chip := nameStyle.Render(chipText)
	if bgColor != "" {
		// Gradient-fill the continuation row (not a flat solid) so a wrapped tab's
		// second row carries the same light->base->dark sheen as its first row —
		// otherwise the gradient appears to "stop" at the wrap. Same from/to/width
		// as line 1 so the shade at every column matches vertically.
		chip = c.applyGradientFill(chip, gradientEndColor(bgColor), bgColor, avail)
		// Fill the reserved menu column with the dark tail, exactly like line 1's
		// menu button, so the right edge continues the shadow (and stays aligned).
		tail := c.applyBackgroundFill(strings.Repeat(" ", menuBtnW), gradientTailColor(bgColor), menuBtnW)
		s.WriteString(leadingRendered + chip + tail + "\n")
		return
	}
	s.WriteString(leadingRendered + chip + "\n")
}

// applyBackgroundFill applies the sidebar background color to all content lines
// ensuring the entire sidebar area has a consistent background.
// It also re-injects the bg escape after any ANSI resets within the line
// so background color survives style resets mid-line.
func (c *Coordinator) applyBackgroundFill(content string, bgColor string, width int) string {
	if bgColor == "" {
		return content
	}

	// Use lipgloss to generate a profile-aware background escape sequence
	// rather than hardcoding TrueColor (\x1b[48;2;R;G;Bm). This lets
	// 256-color clients (e.g. Mosh) receive the correct escape format.
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	// Render a null-byte probe to extract just the opening escape sequence.
	rendered := bgStyle.Render("\x00")
	parts := strings.SplitN(rendered, "\x00", 2)
	bgEsc := ""
	if len(parts) >= 1 {
		bgEsc = parts[0]
	}
	resetEsc := "\x1b[0m"

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Re-inject bg escape after any ANSI resets within the line
		// This ensures the background persists even when styles are reset mid-line
		line = strings.ReplaceAll(line, resetEsc, resetEsc+bgEsc)

		// Get visual width of line (stripping ANSI codes)
		plainLine := stripAnsi(line)
		visualWidth := uniseg.StringWidth(plainLine)

		// Calculate padding needed
		padding := ""
		if visualWidth < width {
			padding = strings.Repeat(" ", width-visualWidth)
		}

		// Wrap entire line: bg color prefix + content + padding + reset
		lines[i] = bgEsc + line + padding + resetEsc
	}

	return strings.Join(lines, "\n")
}

// applyGradientFill is applyBackgroundFill with a subtle left-to-right horizontal
// background gradient (fromHex -> toHex) instead of a flat fill, so tab rows and
// group headers get a gentle sheen rather than reading as flat colour blocks. It
// preserves any foreground ANSI already in content (copying escape sequences
// through verbatim) and pads the remainder with gradient-coloured spaces. Per-cell
// backgrounds use TrueColor escapes directly (cheap — one Sprintf per column),
// matching the raw bg escapes the tab-row renderer already emits. Falls back to a
// solid fromHex fill for any edge case (non-hex colours, empty width, multi-line
// content), so it can never render worse than applyBackgroundFill.
func (c *Coordinator) applyGradientFill(content, fromHex, toHex string, width int) string {
	if width < 1 || strings.Contains(content, "\n") ||
		len(fromHex) != 7 || fromHex[0] != '#' || len(toHex) != 7 || toHex[0] != '#' {
		return c.applyBackgroundFill(content, fromHex, width)
	}
	// Compute a distinct colour PER COLUMN (no banding). The old banding read as
	// visible steps; emit-on-change below (see `emit`) still collapses runs of
	// identical rounded RGB into one escape, so the byte cost stays modest while
	// the gradient looks smooth.
	//
	// The gradient lightens the base over the first ~85% of the row, then DARKENS
	// it over the final ~15% so the tail deepens into a shadowed edge instead of
	// flattening out at the base colour. The tail uses a smoothstep ease so it
	// blends IN gradually (no visible slope seam at the junction). darkEnd is the
	// base pushed toward black — also reused for the menu-button fill so the row's
	// right edge doesn't pop back to the base colour.
	const headEnd = 0.15  // leading-edge highlight zone (mirrors the dark tail)
	const tailStart = 0.85
	darkEnd := gradientTailColor(toHex)
	lightHead := blendHexToward(fromHex, "#ffffff", 0.18) // extra-light left edge
	bgAt := func(x int) string {
		frac := 0.0
		if width > 1 {
			frac = float64(x) / float64(width-1)
		}
		var hex string
		switch {
		case frac <= headEnd:
			// Leading edge: ease from a bright highlight into the normal light start,
			// symmetric to the dark tail so the left gets a slight sheen.
			t := frac / headEnd
			t = t * t * (3 - 2*t) // smoothstep
			hex = blendHexToward(lightHead, fromHex, t)
		case frac <= tailStart:
			t := (frac - headEnd) / (tailStart - headEnd)
			hex = blendHexToward(fromHex, toHex, t) // light -> base
		default:
			t := (frac - tailStart) / (1 - tailStart)
			t = t * t * (3 - 2*t) // smoothstep: ease the darkening in, no seam at the junction
			hex = blendHexToward(toHex, darkEnd, t) // base -> dark tail
		}
		r, g, b := hexToRGB(hex)
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	}
	isTerm := func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
	reset := "\x1b[0m"
	var b strings.Builder
	col := 0
	lastEsc := ""
	emit := func() {
		if col >= width {
			return
		}
		if e := bgAt(col); e != lastEsc {
			b.WriteString(e)
			lastEsc = e
		}
	}
	rs := []rune(content)
	for i := 0; i < len(rs); {
		if rs[i] == '\x1b' {
			// Copy a whole escape sequence (up to and including its final letter)
			// through untouched so embedded fg/bold styling survives. A reset clears
			// our bg, so force the next cell to re-emit it.
			j := i + 1
			for j < len(rs) && !isTerm(rs[j]) {
				j++
			}
			if j < len(rs) {
				j++
			}
			seq := string(rs[i:j])
			b.WriteString(seq)
			if strings.Contains(seq, "\x1b[0m") || seq == "\x1b[m" {
				lastEsc = ""
			}
			i = j
			continue
		}
		emit()
		b.WriteRune(rs[i])
		col += uniseg.StringWidth(string(rs[i]))
		i++
	}
	for ; col < width; col++ {
		emit()
		b.WriteString(" ")
	}
	b.WriteString(reset)
	return b.String()
}

// readableFg returns a legible foreground colour for text drawn on bg: near-black
// on light backgrounds, white on dark ones. Used so a bright/light custom tab
// colour (e.g. a light green) still shows legible tab text instead of white-on-light.
func readableFg(bg string) string {
	if colors.IsLightColor(bg) {
		return "#1a1a1a"
	}
	return "#ffffff"
}

// contrastInactiveLighten is how much lighter inactive text is than active (see
// contrastFg). Tunable via sidebar.colors.inactive_lighten; applyContrastConfig
// updates it on config load. Default 0.15.
var contrastInactiveLighten = 0.15

// applyContrastConfig refreshes contrastInactiveLighten from config. Called
// wherever c.config is (re)assigned so a live config reload retunes the contrast.
func applyContrastConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if v := cfg.Sidebar.Colors.InactiveLighten; v > 0 {
		contrastInactiveLighten = v
	}
}

// contrastFg is readableFg but, for a non-active (unfocused) tab, makes the text
// slightly LIGHTER than the active colour (by contrastInactiveLighten) — so active
// and inactive read as mostly the same, with inactive only de-emphasised, rather
// than washing the text toward the background (which hurt legibility). Active tabs
// keep full contrast. On a dark bg the active colour is already white, so inactive
// stays effectively identical.
func contrastFg(bg string, active bool) string {
	fg := readableFg(bg)
	if !active {
		fg = lightenHex(fg, contrastInactiveLighten)
	}
	return fg
}

// gradientEndColor returns the far end of a header/tab gradient: the base colour
// nudged toward white. Returns the input unchanged when it isn't a hex colour,
// which makes applyGradientFill fall back to a solid fill.
func gradientEndColor(bg string) string { return lightenHex(bg, 0.22) }

// gradientTailColor returns the DARK end of a header/tab gradient: the base colour
// pushed toward black. applyGradientFill deepens the last ~15% of a row to this,
// and the menu-button fill reuses it so a row's right edge continues the shadow
// instead of snapping back to the base colour.
func gradientTailColor(bg string) string {
	if len(bg) != 7 || bg[0] != '#' {
		return bg
	}
	return blendHexToward(bg, "#000000", 0.22)
}

func fixHeaderHeightsInWindow(paneID string) {
	// Get window ID and width so we use per-window width, not global profile
	infoOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}|||#{window_width}").Output()
	infoParts := strings.SplitN(strings.TrimSpace(string(infoOut)), "|||", 2)
	if len(infoParts) < 2 {
		return
	}
	windowID := infoParts[0]
	windowWidth, _ := strconv.Atoi(infoParts[1])
	if windowID == "" {
		return
	}
	target := globalCoordinator.desiredWindowHeaderHeightForWidth(windowWidth)
	targetStr := fmt.Sprintf("%d", target)
	listOut, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}").Output()
	for _, line := range strings.Split(string(listOut), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) < 2 {
			continue
		}
		if isAuxiliaryPaneCommand(parts[1]) {
			exec.Command("tmux", "resize-pane", "-t", parts[0], "-y", targetStr).Run()
		}
	}
}

func isAuxiliaryPaneCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "sidebar") {
		return true
	}
	if strings.Contains(lower, "pane-header") || strings.Contains(lower, "pane header") || strings.Contains(lower, "pane_header") {
		return true
	}
	if strings.Contains(lower, "window-header") || strings.Contains(lower, "window_header") {
		return true
	}
	return false
}

// isSidebarPaneCommand returns true when either the current or start command
// indicates a sidebar-renderer pane. Post-consolidation, pane_current_command
// reports "tabby" for every subcommand, so we also check pane_start_command
// which retains the original "exec -a sidebar-renderer ..." invocation.
func isSidebarPaneCommand(cur, start string) bool {
	for _, s := range []string{cur, start} {
		if s == "" {
			continue
		}
		lower := strings.ToLower(s)
		if strings.Contains(lower, "sidebar-renderer") || strings.Contains(lower, "render sidebar") {
			return true
		}
	}
	return false
}

// extractPaneArg extracts the -pane argument from a pane-header start command.
// e.g. "pane-header -pane '%42'" -> "%42"
func extractPaneArg(startCmd string) string {
	// Try single-quoted
	re := regexp.MustCompile(`-pane\s+'([^']+)'`)
	if m := re.FindStringSubmatch(startCmd); len(m) > 1 {
		return m[1]
	}
	// Try double-quoted
	re2 := regexp.MustCompile(`-pane\s+"([^"]+)"`)
	if m := re2.FindStringSubmatch(startCmd); len(m) > 1 {
		return m[1]
	}
	// Try unquoted
	re3 := regexp.MustCompile(`-pane\s+(\S+)`)
	if m := re3.FindStringSubmatch(startCmd); len(m) > 1 {
		return m[1]
	}
	return ""
}

// findContentPane finds the first non-auxiliary pane in a window, preferring the active one.
func findContentPane(windowID, fallback string) string {
	out, err := exec.Command("tmux", "list-panes", "-t", windowID, "-F",
		"#{pane_id}|#{pane_current_command}|#{pane_start_command}|#{pane_active}").Output()
	if err != nil {
		return fallback
	}
	var firstContent string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		pID, cmd, startCmd, active := parts[0], parts[1], parts[2], parts[3]
		combined := cmd + "|" + startCmd
		if strings.Contains(combined, "sidebar") || strings.Contains(combined, "renderer") ||
			strings.Contains(combined, "pane-header") || strings.Contains(combined, "window-header") || strings.Contains(combined, "tabby-daemon") {
			continue
		}
		if active == "1" {
			return pID
		}
		if firstContent == "" {
			firstContent = pID
		}
	}
	if firstContent != "" {
		return firstContent
	}
	return fallback
}

func isAuxiliaryPane(p tmux.Pane) bool {
	return isAuxiliaryPaneCommand(p.Command) || isAuxiliaryPaneCommand(p.StartCommand)
}

func findWindowByTarget(windows []tmux.Window, target string) *tmux.Window {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	if strings.HasPrefix(target, "@") {
		for i := range windows {
			if windows[i].ID == target {
				return &windows[i]
			}
		}
		return nil
	}

	idx, err := strconv.Atoi(target)
	if err != nil {
		return nil
	}
	for i := range windows {
		if windows[i].Index == idx {
			return &windows[i]
		}
	}
	return nil
}

// selectContentPaneInActiveWindow finds and selects the first non-auxiliary pane
// in the currently active window, ensuring focus goes to a content pane rather
// than a sidebar or pane-header.
func selectContentPaneInActiveWindow() {
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_enable_focus_repair").Output(); err != nil || strings.TrimSpace(string(out)) != "1" {
		return
	}

	windowIDOut, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	windowID := strings.TrimSpace(string(windowIDOut))
	if windowID == "" {
		return
	}
	selectContentPaneInWindow(windowID)
}

// focusContentPaneInActiveWindow ensures focus is in a content pane in the
// currently active window. Not feature-gated — used to keep keyboard focus
// out of auxiliary panes (sidebar/pane-header/window-header) after window
// switches or taps on the window-header.
func focusContentPaneInActiveWindow() {
	if globalCoordinator != nil {
		status := globalCoordinator.NewWindowStatus()
		if status.State == "inFlight" || status.State == "ready" {
			logEvent("FOCUS_CONTENT_PANE_SKIP reason=new_window_status state=%s target=%s", status.State, status.WindowID)
			return
		}
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output(); err == nil && strings.TrimSpace(string(out)) == "1" {
		return
	}
	windowIDOut, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	windowID := strings.TrimSpace(string(windowIDOut))
	if windowID == "" {
		return
	}
	selectContentPaneInWindow(windowID)
}

// selectContentPaneInWindow selects the first non-auxiliary pane in the given
// window. Post-consolidation, pane_current_command is just "tabby" for sidebar /
// pane-header / window-header panes too — the renderer identity lives in
// pane_start_command. Check both so auxiliary panes are correctly skipped.
func selectContentPaneInWindow(windowID string) {
	out, err := exec.Command("tmux", "list-panes", "-t", windowID,
		"-F", "#{pane_id}|||#{pane_current_command}|||#{pane_start_command}").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|||", 3)
		if len(parts) < 2 {
			continue
		}
		cur := parts[1]
		start := ""
		if len(parts) >= 3 {
			start = parts[2]
		}
		if isAuxiliaryPaneCommand(cur) || isAuxiliaryPaneCommand(start) {
			continue
		}
		exec.Command("tmux", "select-pane", "-t", parts[0]).Run()
		return
	}
}

// hexToRGB converts hex color to RGB values
func hexToRGB(hexColor string) (int, int, int) {
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	var r, g, b int64
	r, _ = strconv.ParseInt(hex[0:2], 16, 64)
	g, _ = strconv.ParseInt(hex[2:4], 16, 64)
	b, _ = strconv.ParseInt(hex[4:6], 16, 64)
	return int(r), int(g), int(b)
}

// dimColor reduces the brightness of a hex color by the given opacity (0.0-1.0)
// Opacity of 1.0 = no change, 0.5 = half brightness
func dimColor(hexColor string, opacity float64) string {
	if hexColor == "" {
		return hexColor
	}
	r, g, b := hexToRGB(hexColor)
	// Dim by reducing RGB values toward 0
	r = int(float64(r) * opacity)
	g = int(float64(g) * opacity)
	b = int(float64(b) * opacity)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// indicatorStyle builds the lipgloss style for a busy/input indicator glyph.
// When the window is minimized ("muted"), the glyph is dimmed toward the row
// background (the same 0.55 blend used for minimized tab text) and fainted, so
// the ?/spinner reads as muted instead of standing out at full brightness.
func indicatorStyle(color string, minimized bool, bgColor, themeBg string) lipgloss.Style {
	s := lipgloss.NewStyle()
	if minimized {
		blendBg := bgColor
		if blendBg == "" {
			blendBg = themeBg
		}
		if d := blendHexToward(color, blendBg, 0.55); d != "" {
			color = d
		}
		s = s.Faint(true)
	}
	return s.Foreground(lipgloss.Color(color))
}

// HeaderColors holds the fg/bg colors for a pane header border.
type HeaderColors struct {
	Fg string
	Bg string
}

// GetHeaderColorsForPane returns the fg and bg colors for a pane header.
// Mirrors the tab color logic from sidebar rendering (custom colors, shading, active/inactive).
// Must be called with stateMu at least read-locked (callers hold RLock from RenderHeaderForClient).
func (c *Coordinator) GetHeaderColorsForPane(paneID string) HeaderColors {
	var foundWindow *tmux.Window
	var isWindowActive bool
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				foundWindow = &c.windows[i]
				isWindowActive = c.windows[i].Active
				break
			}
		}
		if foundWindow != nil {
			break
		}
	}
	if foundWindow == nil {
		return HeaderColors{
			Fg: c.getPaneHeaderActiveFg(),
			Bg: c.getPaneHeaderActiveBg(),
		}
	}

	var theme config.Theme
	var customColor string
	var foundGroup bool
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == foundWindow.ID {
				theme = group.Theme
				customColor = win.CustomColor
				foundGroup = true
				break
			}
		}
		if foundGroup {
			break
		}
	}

	isDarkBg := c.bgDetector.IsDarkBackground()
	if c.theme != nil {
		isDarkBg = c.theme.Dark
	}
	theme = grouping.ResolveThemeColors(theme, isDarkBg)

	var bgColor, fgColor string
	isTransparent := customColor == "transparent"

	if isTransparent {
		bgColor = ""
		if isWindowActive {
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			fgColor = theme.Fg
		}
	} else if customColor != "" {
		if isWindowActive {
			bgColor = customColor
		} else {
			bgColor = grouping.ShadeColorByIndex(customColor, 1)
		}
		fgColor = contrastFg(bgColor, isWindowActive)
	} else if isWindowActive {
		bgColor = theme.ActiveBg
		if bgColor == "" {
			bgColor = theme.Bg
		}
		// Mirror the sidebar TAB's fg resolution EXACTLY (theme.ActiveFg -> theme.Fg
		// -> contrastFg), so the titlebar text colour tracks the tab instead of a
		// fixed white for groups that leave fg empty.
		fgColor = theme.ActiveFg
		if fgColor == "" {
			fgColor = theme.Fg
		}
		if fgColor == "" {
			fgColor = contrastFg(bgColor, true)
		}
	} else {
		bgColor = theme.Bg
		fgColor = theme.Fg
		if fgColor == "" {
			fgColor = contrastFg(bgColor, false)
		}
	}

	if bgColor == "" {
		bgColor = c.getPaneHeaderActiveBg()
	}
	if fgColor == "" {
		fgColor = c.getPaneHeaderActiveFg()
	}

	return HeaderColors{Fg: fgColor, Bg: bgColor}
}

// GetGitStateHash returns a hash of current git state for change detection
func (c *Coordinator) GetGitStateHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return fmt.Sprintf("%s:%d:%d:%d:%v", c.gitBranch, c.gitDirty, c.gitAhead, c.gitBehind, c.isGitRepo)
}

// desaturateHex blends a hex color toward a target color by the given opacity.
// opacity=1.0 means original color, opacity=0.0 means full target.
// targetHex is the blend target (e.g. terminal background); if empty, uses a
// luminance-based neutral.
func desaturateHex(hexColor string, opacity float64, targetHex ...string) string {
	if hexColor == "" {
		return hexColor
	}
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return hexColor
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 32)
	g, _ := strconv.ParseInt(hex[2:4], 16, 32)
	b, _ := strconv.ParseInt(hex[4:6], 16, 32)

	var tR, tG, tB int
	if len(targetHex) > 0 && targetHex[0] != "" {
		th := strings.TrimPrefix(targetHex[0], "#")
		if len(th) == 6 {
			tr, _ := strconv.ParseInt(th[0:2], 16, 32)
			tg, _ := strconv.ParseInt(th[2:4], 16, 32)
			tb, _ := strconv.ParseInt(th[4:6], 16, 32)
			tR, tG, tB = int(tr), int(tg), int(tb)
		}
	}
	if tR == 0 && tG == 0 && tB == 0 && (len(targetHex) == 0 || targetHex[0] == "") {
		lum := (int(r)*299 + int(g)*587 + int(b)*114) / 1000
		if lum >= 128 {
			tR, tG, tB = 200, 200, 200
		} else {
			tR, tG, tB = 48, 48, 48
		}
	}

	inv := 1.0 - opacity
	dr := int(math.Round(float64(r)*opacity + float64(tR)*inv))
	dg := int(math.Round(float64(g)*opacity + float64(tG)*inv))
	db := int(math.Round(float64(b)*opacity + float64(tB)*inv))

	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return v
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(dr), clamp(dg), clamp(db))
}
