package daemon

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/navtrace"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// Event is the interface implemented by all loop events. The kind() string is
// used for diagnostics (LOOP_DROP, LOOP_UNKNOWN_EVENT) only.
type Event interface{ kind() string }

// RendererInputEvent carries an input message from a renderer client into the
// loop for serial dispatch.
type RendererInputEvent struct {
	ClientID string
	Input    *daemon.InputPayload
}

func (RendererInputEvent) kind() string { return "renderer_input" }

// Tick events. Each corresponds to one of the tickers that previously lived
// in the main select loop or in the idle-monitor goroutine in main.go.
type ClientGeomTickEvent struct{}
type WindowCheckTickEvent struct{}
type AnimationTickEvent struct{}
type RefreshTickEvent struct{}
type GitTickEvent struct{}
type TeamClaudeTickEvent struct{}
type KimiTickEvent struct{}
type WatchdogTickEvent struct{}
type IdleTickEvent struct{}
type SocketCheckTickEvent struct{}
type TabSummaryTickEvent struct{}

// RefreshSignalEvent carries a refresh request — historically delivered
// over the refreshCh channel from the main goroutine. Producers are: the
// SIGUSR1 handler, tmux hooks (after-select-window / after-resize-pane /
// client-attached), renderer input flagged needsRefresh, and
// coordinator.OnRefreshLayout. All converge on flags.usr1 for at-most-one-
// pending coalescing.
type RefreshSignalEvent struct{}

func (ClientGeomTickEvent) kind() string  { return "tick:client_geom" }
func (WindowCheckTickEvent) kind() string { return "tick:window_check" }
func (AnimationTickEvent) kind() string   { return "tick:animation" }
func (RefreshTickEvent) kind() string     { return "tick:refresh" }
func (GitTickEvent) kind() string         { return "tick:git" }
func (TeamClaudeTickEvent) kind() string  { return "tick:teamclaude" }
func (KimiTickEvent) kind() string        { return "tick:kimi" }
func (WatchdogTickEvent) kind() string    { return "tick:watchdog" }
func (IdleTickEvent) kind() string        { return "tick:idle" }
func (SocketCheckTickEvent) kind() string { return "tick:socket_check" }
func (TabSummaryTickEvent) kind() string  { return "tick:tab_summary" }
func (RefreshSignalEvent) kind() string   { return "signal:refresh" }

// SignalEvent carries a SIGUSR1 / SIGUSR2 delivery into the loop. Step 3 of
// the daemon refactor (see /Users/b/.claude/plans/nifty-jingling-tulip.md)
// migrates the two former signal-handler goroutines onto the loop so the
// SIGUSR2 path can dedup against lastResizeKey (the geom-tick path already
// dedups; SIGUSR2 was the bypass that today causes redundant resize storms
// on opencode launch).
type SignalEvent struct{ Sig syscall.Signal }

func (SignalEvent) kind() string { return "signal" }

// TmuxHookEvent carries a tmux-hook delivery from the `tabby hook` CLI into
// the loop. Step 4 of the daemon refactor (see
// /Users/b/.claude/plans/nifty-jingling-tulip.md): tmux hooks now flow over
// the daemon socket as MsgHook instead of `kill -USR1`/`kill -USR2`. The
// SIGUSR1/SIGUSR2 paths remain intact for backward compatibility during
// rollout — `lastResizeKey` dedup absorbs any duplicate signal+hook fires.
type TmuxHookEvent struct {
	Kind string
	Args map[string]string
}

func (e TmuxHookEvent) kind() string { return "hook:" + e.Kind }

// LoopTickDeps bundles the closures and references that the migrated ticker
// handlers (handle*Tick methods on Loop) need from the surrounding Daemon
// scope. They are wired in by main.go after the Daemon-local closures
// (runLoopTask, updateActiveWindow, etc.) are constructed. Keeping these as
// fields rather than inlining them on Loop preserves the existing semantics
// of those closures (they capture daemonStartTime, crashLog, sigCh, etc.)
// without forcing those globals onto the Loop type.
type LoopTickDeps struct {
	RunLoopTask         func(task string, timeout time.Duration, fn func()) bool
	RunLoopTaskNonFatal func(task string, timeout time.Duration, fn func())

	// Off-loop ticker dependencies (idle / socket-check). These were locals
	// in the idle-monitor goroutine before the migration. SigCh is the
	// shutdown channel; when a watchdog condition is detected we send
	// SIGTERM and the main goroutine handles the actual stop.
	SessionID  string
	MyPid      int
	SocketPath string
	SigCh      chan<- os.Signal
}

// Loop owns coordinator mutations driven by external events. All event
// handlers run sequentially on the goroutine that calls Run, so they observe
// each other's writes without further synchronization. State that must be
// observed from other goroutines (e.g. nav-settle hints read by the main
// select loop in main.go) is exposed via accessor methods that take an
// internal mutex.
type Loop struct {
	// inputs carries priority events (renderer inputs, tmux hooks) — events
	// directly downstream of a user action. Run() drains inputs ahead of
	// events so a queued cmd+]/cmd+[ keystroke jumps any backlog of
	// background ticks. A small per-iteration budget prevents sustained
	// input pressure from starving background work entirely.
	inputs chan Event
	// events carries background work (ticks, signals). submitCoalesced
	// always targets this channel.
	events chan Event
	drops  atomic.Uint64

	coord   *Coordinator
	server  *daemon.Server
	elector *daemon.ClientElector

	// flags coalesces duplicate tick events at the producer side.
	flags tickFlags

	// deps holds the wiring closures required by handle*Tick methods. It is
	// populated by main.go via SetTickDeps before the first tick is enqueued.
	deps LoopTickDeps

	// nav-settle state, written by handleRendererInput and read both by the
	// loop itself and by the main select loop in main.go.
	navMu                 sync.RWMutex
	lastExplicitNavAt     time.Time
	lastExplicitNavWindow string
	navSettleUntil        time.Time
	navSettledWindow      string

	// Tick-handler and refresh-handler state. All fields are touched only
	// from loop-goroutine handlers — no synchronization needed. The former
	// sharedStateMu went away when handleRefreshSignal moved off the
	// refresh-loop goroutine in main.go onto the loop itself.
	activeWindowID     string
	lastWindowsHash    string
	lastStructuralHash string
	lastGitState       string
	lastClientGeom     string
	lastResizeKey      string
	// pendingGeom is a client size seen but not yet believed, and
	// pendingGeomSince is when we first saw it. See geometrySettled.
	pendingGeom      string
	pendingGeomSince time.Time
	// relockGeom, when set, makes the next geometry tick lock every window to
	// the elected client even though lastClientGeom / lastResizeKey say the
	// size is already handled. Written from the lease-election goroutine via
	// RequestGeometryRelock, so it is atomic; every other field here is
	// loop-goroutine only.
	relockGeom atomic.Bool
	// geomIdleTicks counts consecutive geometry ticks that elected the same
	// single client and found nothing to do. It drives the tick's backoff
	// (see geomTickInterval): every tick forks a `tmux list-clients`, so an
	// idle daemon polling at the base rate forever is pure waste. Written on
	// the loop goroutine, read by the ticker goroutine, hence atomic.
	geomIdleTicks atomic.Int64
	// lastActivityReconcile rate-limits the reconcile that an activity-only
	// geometry wake would otherwise trigger every few seconds forever. See
	// handleClientGeomTick.
	lastActivityReconcile time.Time
	// lastStaleClientPrune rate-limits PruneStaleClients; the geometry tick
	// itself runs far too often to scan clients on every pass.
	lastStaleClientPrune time.Time
	lastWindowCheck      string
	lastSlowFrame        int
	displayedWins        []string  // windows some attached client is looking at
	displayedWinsAt      time.Time // when displayedWins was last refreshed
	lastWindowCount      int       // count of coordinator windows last seen by signal_refresh
	lastFullRefresh      time.Time // last time signal_refresh ran the heavy spawn/cleanup path
	lastReadyWindowID    string    // last new-window-ready windowID observed (for tmux-active suppression)
	lastReadyClearedAt   time.Time // when the new-window ready state was last cleared
	lastPaneLayoutOps    time.Time // debounce for the spawn/cleanup heavy path
	lastReassertAt       time.Time // rate-limit for reassertActiveWindow
	lastReassertTarget   string    // target of the last active-window reassert
	// housekeepingMu serializes the async housekeeping pass kicked off by
	// signal_refresh. Held for the duration of one heavy run; TryLock is used
	// at submit time so a rapid burst of signals skips when a prior pass is
	// still in flight (the in-flight pass will see fresh tmux state).
	housekeepingMu sync.Mutex

	// Off-loop ticker state.
	idleStart time.Time
}

// NewLoop constructs a Loop. The refresh trigger that previously flowed
// over an external refreshCh channel is now an in-loop RefreshSignalEvent
// queued via SubmitRefresh.
func NewLoop(coord *Coordinator, server *daemon.Server, elector *daemon.ClientElector) *Loop {
	return &Loop{
		inputs:  make(chan Event, 256),
		events:  make(chan Event, 256),
		coord:   coord,
		server:  server,
		elector: elector,
	}
}

// isPriorityEvent reports whether ev belongs on the priority (inputs) lane.
// Priority events are anything directly tied to a user action: renderer
// clicks/keystrokes and tmux hooks (which fire in response to user-driven
// tmux commands). Ticks and signals stay on the background lane.
func isPriorityEvent(ev Event) bool {
	switch ev.(type) {
	case RendererInputEvent, TmuxHookEvent:
		return true
	default:
		return false
	}
}

// SetTickDeps wires closures from the Daemon scope (runLoopTask,
// updateActiveWindow, etc.) onto the Loop so handle*Tick methods can call
// them. Must be called before any tick events are enqueued.
func (l *Loop) SetTickDeps(deps LoopTickDeps) {
	l.deps = deps
}

// SetActiveWindowID assigns the active-window observation. All callers run
// on the loop goroutine now, so this is a plain write — the former mutex
// is gone. Kept as a method (rather than direct field write) so call sites
// read uniformly across loop.go and the residual Run() initialization
// path in main.go.
func (l *Loop) SetActiveWindowID(id string) {
	l.activeWindowID = id
}

// SetLastWindowCount primes the window-count tracker from main.go's
// initialization path, before the first refresh body runs.
func (l *Loop) SetLastWindowCount(n int) {
	l.lastWindowCount = n
}

// ActiveWindowID returns the currently-tracked active window ID. Loop
// goroutine read; safe without synchronization.
func (l *Loop) ActiveWindowID() string {
	return l.activeWindowID
}

// Submit enqueues an event for the loop. Priority events (renderer inputs,
// tmux hooks) go on the inputs lane and are dispatched ahead of background
// work in Run(). All other events go on the background lane. If the chosen
// queue is full, the event is dropped and a LOOP_DROP line is logged: a
// backed-up loop dropping a redundant event is preferable to blocking the
// producer.
func (l *Loop) Submit(ev Event) {
	ch := l.events
	if isPriorityEvent(ev) {
		ch = l.inputs
		// A user just did something. Whatever gap the geometry tick had
		// widened to while idle, collapse it now, so the poll is back at full
		// rate before the consequences of that action (a resize, a client
		// switch) need noticing. This is what makes the backoff safe with
		// several clients attached.
		l.geomIdleTicks.Store(0)
	}
	select {
	case ch <- ev:
	default:
		l.drops.Add(1)
		// Attribute the drop. A bare "kind=renderer_input" hid which input was
		// lost — for a window-nav keypress that's exactly the dropped request the
		// user notices, so surface the action/target and mirror it into the nav
		// trace (correlatable with the hook's HOOK_SENT via navid).
		if rie, ok := ev.(RendererInputEvent); ok && rie.Input != nil {
			logEvent("LOOP_DROP kind=%s action=%s target=%s queue_full drops_total=%d",
				ev.kind(), rie.Input.ResolvedAction, strings.TrimSpace(rie.Input.ResolvedTarget), l.drops.Load())
			if isNavAction(rie.Input.ResolvedAction) {
				navtrace.Write("LOOP_DROP navid=%s action=%s target=%s queue_full",
					navIDFromValue(rie.Input.PickerValue), rie.Input.ResolvedAction, strings.TrimSpace(rie.Input.ResolvedTarget))
			}
		} else {
			logEvent("LOOP_DROP kind=%s queue_full drops_total=%d", ev.kind(), l.drops.Load())
		}
	}
}

// Run dispatches events sequentially until ctx is cancelled. The heartbeat
// is bumped on each iteration so the deadlock watchdog (5s threshold) sees
// liveness from this goroutine — pre-Step-2 the heartbeat lived in the
// main-goroutine for-select that fired tickers up to 10 Hz; with tickers
// now driving the loop, the loop is the natural heartbeat source.
func (l *Loop) Run(ctx context.Context) {
	logEvent("LOOP_START")
	// Priority budget: at most this many consecutive priority events before
	// we yield to the combined select, where the background lane gets a
	// fair shot. Prevents pathological keystroke storms from starving
	// ticks. Human keystroke rates are well below this threshold so the
	// budget is invisible in normal use.
	const priorityBudget = 4
	priorityRun := 0
	for {
		recordHeartbeat()
		select {
		case <-ctx.Done():
			logEvent("LOOP_STOP drops=%d", l.drops.Load())
			return
		default:
		}
		if priorityRun < priorityBudget {
			select {
			case ev := <-l.inputs:
				priorityRun++
				l.dispatch(ev)
				continue
			default:
			}
		}
		priorityRun = 0
		select {
		case <-ctx.Done():
			logEvent("LOOP_STOP drops=%d", l.drops.Load())
			return
		case ev := <-l.inputs:
			l.dispatch(ev)
		case ev := <-l.events:
			l.dispatch(ev)
		}
	}
}

func (l *Loop) dispatch(ev Event) {
	switch e := ev.(type) {
	case RendererInputEvent:
		l.handleRendererInput(e)
	case ClientGeomTickEvent:
		l.handleClientGeomTick()
	case WindowCheckTickEvent:
		l.handleWindowCheckTick()
	case AnimationTickEvent:
		l.handleAnimationTick()
	case RefreshTickEvent:
		l.handleRefreshTick()
	case GitTickEvent:
		l.handleGitTick()
	case TeamClaudeTickEvent:
		l.handleTeamClaudeTick()
	case KimiTickEvent:
		l.handleKimiTick()
	case WatchdogTickEvent:
		l.handleWatchdogTick()
	case IdleTickEvent:
		l.handleIdleTick()
	case SocketCheckTickEvent:
		l.handleSocketCheckTick()
	case TabSummaryTickEvent:
		l.handleTabSummaryTick()
	case SignalEvent:
		l.handleSignal(e)
	case TmuxHookEvent:
		l.handleTmuxHook(e)
	case RefreshSignalEvent:
		l.handleRefreshSignal()
	default:
		logEvent("LOOP_UNKNOWN_EVENT kind=%s", ev.kind())
	}
}

// runTicker drives a fn at cadence d until ctx is cancelled. Used by main.go
// to fire one of the per-tick submitCoalesced calls.
// runBackoffTicker is runTicker with an interval recomputed before every wait,
// so a handler can widen its own polling gap as it goes idle. `interval` is
// called on this goroutine and must not block.
func runBackoffTicker(ctx context.Context, interval func() time.Duration, fn func()) {
	for {
		t := time.NewTimer(interval())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			fn()
		}
	}
}

func runTicker(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// NavSettleState returns a snapshot of the explicit-nav state for readers
// outside the loop goroutine. Returns (lastExplicitNavAt, lastExplicitNavWindow,
// navSettleUntil, navSettledWindow).
func (l *Loop) NavSettleState() (time.Time, string, time.Time, string) {
	l.navMu.RLock()
	defer l.navMu.RUnlock()
	return l.lastExplicitNavAt, l.lastExplicitNavWindow, l.navSettleUntil, l.navSettledWindow
}

// handleRendererInput is the migrated body of the former server.OnInput
// closure. It runs on the loop goroutine.
func (l *Loop) handleRendererInput(e RendererInputEvent) {
	clientID := e.ClientID
	input := e.Input
	resolvedAction := strings.TrimSpace(input.ResolvedAction)
	pinFocus := true
	isExitCheck := daemon.KindOf(clientID) == daemon.TargetHook && (resolvedAction == "" || resolvedAction == "exit_if_no_main" || resolvedAction == "exit_if_no_main_windows")
	if isExitCheck {
		pinFocus = false
	}
	if pinFocus {
		sourceWin := sourceWindowIDFromClientID(clientID)
		sourceTTY := ""
		if input.PaneID != "" {
			sourceTTY = clientTTYForPane(input.PaneID)
		}
		if sourceTTY == "" && sourceWin != "" {
			sourceTTY = clientTTYForWindow(sourceWin)
		}
		if sourceTTY == "" {
			sourceTTY = latestAttachedClientTTY()
		}
		if sourceTTY != "" {
			setPreferredClientTTY(sourceTTY, fmt.Sprintf("input:%s:%s", clientID, input.ResolvedAction))
		}
	} else {
		logEvent("CLIENT_FOCUS_PIN_SKIP client=%s resolved=%s", clientID, input.ResolvedAction)
	}
	defer func() {
		if r := recover(); r != nil {
			debugLog.Printf("PANIC in OnInput handler (client=%s): %v", clientID, r)
			logEvent("PANIC_INPUT client=%s err=%v", clientID, r)
		}
	}()
	if daemon.KindOf(clientID) == daemon.TargetWindowHeader {
		if resolvedAction == "window_header:prev_window" || resolvedAction == "window_header:next_window" || resolvedAction == "window_header:new_window" {
			now := time.Now()
			window := strings.TrimSpace(strings.TrimPrefix(clientID, "window-header:"))
			settleUntil := now.Add(1200 * time.Millisecond)
			l.navMu.Lock()
			l.lastExplicitNavAt = now
			l.lastExplicitNavWindow = window
			l.navSettledWindow = window
			l.navSettleUntil = settleUntil
			l.navMu.Unlock()
			logEvent("EXPLICIT_NAV_MARK action=%s window=%s settle_until_ms=%d", resolvedAction, window, time.Until(settleUntil).Milliseconds())
		}
	}
	needsRefresh := l.coord.HandleInput(clientID, input)
	logEvent("INPUT_HANDLED client=%s needsRefresh=%v", clientID, needsRefresh)
	// dashboardSkipBroadcast is set by handlers whose work doesn't affect what
	// renderers display (e.g. in-dashboard pane cycling). Skip the redraw so the
	// sidebar doesn't judder on every cmd+]/cmd+[/cmd+~.
	if l.coord.dashboardSkipBroadcast.CompareAndSwap(true, false) {
		logEvent("INPUT_SKIP_BROADCAST client=%s", clientID)
		return
	}
	if needsRefresh {
		// Immediate optimistic render: HandleInput already updated the
		// coordinator state (e.g. SetActiveWindowOptimistic for select_window)
		// so rendering NOW gives the requesting client the correct header
		// color without waiting for the full BroadcastRender round-trip.
		l.server.SendRenderToClient(clientID)
		// Broadcast to remaining clients asynchronously so the loop
		// goroutine is not blocked by O(n) renders before returning.
		go l.server.BroadcastRender()
		// Queue a full refresh — coalesced via flags.usr1, so a burst
		// of inputs flagged needsRefresh runs the heavy refresh exactly
		// once.
		l.SubmitRefresh()
		logEvent("INPUT_SIGNALED_REFRESH client=%s", clientID)
	} else if isExitCheck {
		// The exit check is a liveness probe, not a state change: it either
		// kills the session or does nothing visible. tmux fires window-unlinked
		// once per window, so opening a window in a grouped session delivers a
		// burst of these — eight in under a second, live — and each one used to
		// fan out a full render to every attached client right as the new
		// window was settling. That storm is what made the new window judder.
		// The requesting client is a short-lived hook process with no renderer
		// (RENDER_SKIP reason=not_found), so there is nothing to send it either.
		logEvent("INPUT_SKIP_BROADCAST client=%s reason=exit_check", clientID)
	} else {
		// Internal-only state change (e.g. toggle_group) - render the
		// requesting client immediately for snappy response, then broadcast
		// to remaining clients asynchronously.
		l.server.SendRenderToClient(clientID)
		go l.server.BroadcastRender()
	}
	logEvent("INPUT_DONE client=%s", clientID)
}

// Cooldowns and grace periods used by the refresh-signal pipeline. Promoted
// from local vars in the refresh-loop closure so the methods migrated onto
// Loop (updateActiveWindow, doPaneLayoutOps) can reference them by name.
const (
	loopNewWindowReadyHold    = 900 * time.Millisecond
	loopNewWindowReadyTimeout = 3 * time.Second

	// How long the set of on-screen windows is reused before asking tmux
	// again. Keeps the animation tick fork-free at 10 Hz.
	displayedWindowsTTL = time.Second
	// loopNewWindowInFlightTimeout bounds the "inFlight" half of the
	// new-window handshake, which had no expiry at all while "ready" has had
	// one since the start. See NewWindowStatus for why an unbounded inFlight
	// freezes the loop. Deliberately much longer than the ready timeout: an
	// early clear only costs one window's worth of phantom-click suppression
	// and lets pane-layout ops race the spawn, whereas a legitimate spawn
	// finishes in well under a second even on a loaded box.
	loopNewWindowInFlightTimeout = 10 * time.Second
	loopPostReadyStabilize       = 2500 * time.Millisecond
	loopPaneLayoutCooldown       = 150 * time.Millisecond
	loopFullRefreshCooldown      = 100 * time.Millisecond
	// loopStructuralDriftWindow bounds the "was this active-window drift caused
	// by our own park/unlink churn?" test. If tmux's active drifts off the
	// window the daemon knows is active within this window of structural churn
	// AND with no user window switch in the same window, we treat it as churn
	// fallout (tmux re-electing window 1 during a regroup) and put focus back.
	loopStructuralDriftWindow = 700 * time.Millisecond
	// loopReassertCooldown rate-limits active-window re-selects so a flapping
	// multi-client elector can't spin the historical select-window cycle: at
	// most one correction per target per this interval.
	loopReassertCooldown = 400 * time.Millisecond
)

// reassertActiveWindow pulls tmux back onto windowID after our own structural
// churn drifted the session's active window away from it. Rate-limited per
// target so a genuinely flapping elector settles into a slow correction rather
// than a tight select-window loop. No-op (returns false) if the window no
// longer exists — a closed active window must let the drift through. Returns
// true when the drift should NOT be accepted by the caller (either we just
// re-selected, or we corrected this target moments ago and are holding).
func (l *Loop) reassertActiveWindow(windowID, reason, driftedTo string) bool {
	if windowID == "" || !l.coord.HasWindow(windowID) {
		return false
	}
	now := time.Now()
	if l.lastReassertTarget == windowID && now.Sub(l.lastReassertAt) < loopReassertCooldown {
		return true
	}
	l.lastReassertAt = now
	l.lastReassertTarget = windowID
	logEvent("ACTIVE_DRIFT_CORRECTED reason=%s tmux_drifted_to=%s restoring=%s", reason, driftedTo, windowID)
	_ = l.coord.SelectWindow(windowID, "active_drift_"+reason, "update_active_window")
	return true
}

// coordinatorActiveWindowID returns the windowID the coordinator currently
// considers active, or empty when no window is marked active.
// windowKnownAtBracketStart reports whether id appears in the window list the
// layout bracket captured before it began mutating panes. A window that does
// not is one tmux created mid-bracket, so tmux selecting it is the user's
// intent rather than a silent flip caused by our own kill-pane/split-window.
func windowKnownAtBracketStart(windows []tmux.Window, id string) bool {
	if id == "" {
		return true
	}
	for _, w := range windows {
		if w.ID == id {
			return true
		}
	}
	return false
}

// newWindowFlowActive reports whether tabby's own new-window flow is still
// running. The coordinator's active flag lags the flow, so a window opened
// through it can already be in the cached list while still looking unintended.
func newWindowFlowActive(coord *Coordinator) bool {
	if coord == nil {
		return false
	}
	switch coord.NewWindowStatus().State {
	case "inFlight", "ready":
		return true
	}
	return false
}

// displayedWindowIDs returns every window an attached client is looking at,
// for the animation tick to paint.
//
// The answer only changes when somebody switches window, attaches or detaches,
// so it is cached: the tick runs at 10 Hz and asking tmux that often would put
// a fork on the hot path to save nothing. A second of staleness costs at worst
// a few frames on a sidebar that was just switched to, and the window-change
// hook repaints it anyway.
//
// Falls back to this session's own active window, which is the pre-existing
// behaviour and always correct for a single attached terminal.
func (l *Loop) displayedWindowIDs() []string {
	if time.Since(l.displayedWinsAt) < displayedWindowsTTL && l.displayedWins != nil {
		return l.displayedWins
	}
	ids, err := tmux.DisplayedWindowIDs()
	if err != nil || len(ids) == 0 {
		return []string{l.ActiveWindowID()}
	}
	l.displayedWins = ids
	l.displayedWinsAt = time.Now()
	return ids
}

func (l *Loop) coordinatorActiveWindowID() string {
	for _, w := range l.coord.GetWindows() {
		if w.Active {
			return w.ID
		}
	}
	return ""
}

// updateActiveWindow synchronizes l.activeWindowID with tmux's active-window
// observation, applying the new-window-ready / explicit-nav-settle
// suppression rules. Was a local closure in the refresh-loop goroutine
// (main.go); promoting it onto Loop is the first step of the
// signal_refresh migration. Call sites continue to use it from the
// refresh-loop closure for now; the next commit moves the entire refresh
// body onto the loop and this becomes a pure loop-goroutine method.
func (l *Loop) updateActiveWindow() {
	status := l.coord.NewWindowStatus()
	coordActive := l.coordinatorActiveWindowID()
	logEvent("READY_STATE_TRACE phase=update_active_start state=%s ready=%s age_ms=%d daemon_active=%s coordinator_active=%s", status.State, status.WindowID, time.Since(status.Created).Milliseconds(), l.activeWindowID, coordActive)
	if status.State == "inFlight" {
		logEvent("UPDATE_ACTIVE_WINDOW_WAIT reason=new_window_inflight daemon_active=%s coordinator_active=%s", l.activeWindowID, coordActive)
		return
	}
	if status.State == "ready" {
		if status.WindowID != "" {
			l.lastReadyWindowID = status.WindowID
		}
		ageMs := time.Since(status.Created).Milliseconds()
		if time.Since(status.Created) > loopNewWindowReadyTimeout {
			logEvent("NEW_WINDOW_READY_TIMEOUT_CLEAR window=%s age_ms=%d", status.WindowID, ageMs)
			l.coord.ClearNewWindowStatus()
			if status.WindowID != "" {
				l.lastReadyWindowID = status.WindowID
			}
			l.lastReadyClearedAt = time.Now()
		} else {
			hasWindow := false
			for _, w := range l.coord.GetWindows() {
				if w.ID == status.WindowID {
					hasWindow = true
					break
				}
			}
			if hasWindow && status.WindowID != "" && l.activeWindowID != status.WindowID {
				logEvent("WINDOW_STATE_DRIFT source=new_window_ready tmux_active=unknown daemon_active=%s coordinator_active=%s ready_window=%s", l.activeWindowID, coordActive, status.WindowID)
			}
			logEvent("READY_STATE_TRACE phase=update_active_ready_observe state=%s ready=%s age_ms=%d daemon_active=%s coordinator_active=%s hasWindow=%v", status.State, status.WindowID, ageMs, l.activeWindowID, coordActive, hasWindow)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// With a client TTY we ask that client directly. Without one we still have
	// to name our own session: an unqualified display-message answers for
	// whichever client was most recently active anywhere on the server, which
	// in a grouped session is routinely another session's client sitting on
	// another window. A session with no clients of its own hits this branch
	// every tick, so the fallback has to be qualified, not bare.
	activeTTY := ""
	var args []string
	if _, _, tty, _, ok := activeClientGeometry(); ok && strings.TrimSpace(tty) != "" {
		activeTTY = strings.TrimSpace(tty)
		args = []string{"display-message", "-c", activeTTY, "-p", "#{window_id}"}
	} else {
		args = l.coord.displayMessageArgs("#{window_id}")
	}
	if out, err := tmux.CmdContext(ctx, args...).Output(); err == nil {
		newID := strings.TrimSpace(string(out))
		if newID != "" {
			logEvent("UPDATE_ACTIVE_WINDOW_TMUX_QUERY daemon_old=%s tmux_new=%s coordinator_active=%s", l.activeWindowID, newID, coordActive)
		}
		logEvent("READY_STATE_TRACE phase=update_active_tmux_query state=%s ready=%s daemon_active=%s tmux_active=%s coordinator_active=%s", status.State, status.WindowID, l.activeWindowID, newID, coordActive)
		if newID != "" {
			if newID != l.activeWindowID || newID != coordActive {
				logEvent("WINDOW_STATE_DRIFT source=tmux_query tmux_active=%s daemon_active=%s coordinator_active=%s", newID, l.activeWindowID, coordActive)
			}
			if newID != l.activeWindowID {
				if !l.lastReadyClearedAt.IsZero() && l.lastReadyWindowID != "" {
					sinceClear := time.Since(l.lastReadyClearedAt)
					if sinceClear <= loopPostReadyStabilize && l.activeWindowID == l.lastReadyWindowID && newID != l.lastReadyWindowID {
						// The just-created window lost the active marker to our own
						// settle churn (e.g. a regroup when ssh connects) within the
						// post-ready hold. The old behaviour only suppressed the
						// daemon's bookkeeping and left tmux stranded on window 1 for
						// the whole hold; instead pull tmux back onto the new window,
						// unless the user themselves just switched away.
						userAct := l.coord.LastUserWindowActionAt()
						if userAct.IsZero() || time.Since(userAct) > loopStructuralDriftWindow {
							if l.reassertActiveWindow(l.lastReadyWindowID, "post_ready", newID) {
								return
							}
						}
						logEvent("UPDATE_ACTIVE_WINDOW_TMUX_SUPPRESS old=%s new=%s last_ready=%s since_clear_ms=%d", l.activeWindowID, newID, l.lastReadyWindowID, sinceClear.Milliseconds())
						return
					}
				}
				navAt, navWindow, settleUntil, settledWindow := l.NavSettleState()
				if !settleUntil.IsZero() && time.Now().Before(settleUntil) && settledWindow != "" {
					if newID != settledWindow {
						logEvent("UPDATE_ACTIVE_WINDOW_TMUX_SUPPRESS_NAV old=%s new=%s settled=%s remaining_ms=%d marked_window=%s", l.activeWindowID, newID, settledWindow, time.Until(settleUntil).Milliseconds(), navWindow)
						return
					}
					logEvent("UPDATE_ACTIVE_WINDOW_TMUX_NAV_CONFIRMED old=%s new=%s settled=%s age_ms=%d", l.activeWindowID, newID, settledWindow, time.Since(navAt).Milliseconds())
				}
				// Structural-churn drift correction. Our own park/unlink churn (a
				// tab regrouping after ssh connects, etc.) can make tmux re-elect
				// the session's active window onto window 1. When the drift lands
				// within loopStructuralDriftWindow of that churn AND the user did
				// not switch windows in the same span AND the window we were on
				// still exists, put focus back instead of accepting the drift.
				// This is the "new tab + ssh into a grouped host jumps to window
				// 1" bug, which the new-window focus guard misses because it has
				// long since expired by the time ssh connects.
				if l.activeWindowID != "" {
					churn := l.coord.LastStructuralChurnAt()
					userAct := l.coord.LastUserWindowActionAt()
					if !churn.IsZero() && time.Since(churn) < loopStructuralDriftWindow &&
						(userAct.IsZero() || time.Since(userAct) > loopStructuralDriftWindow) {
						if l.reassertActiveWindow(l.activeWindowID, "churn", newID) {
							return
						}
					}
				}
				logEvent("UPDATE_ACTIVE_WINDOW_TMUX_OBSERVE old=%s new=%s coordinator_active=%s", l.activeWindowID, newID, coordActive)
			}
			// Record the visit here, not just in SelectWindow. tmux moves focus
			// on its own for new-window and its adjacent-window close default,
			// which never route through SelectWindow -- so those windows never
			// entered the history and SelectPreviousWindow had nothing usable
			// to restore to on close, silently leaving tmux's choice in place.
			if newID != "" && newID != l.activeWindowID {
				// Do not record the focus move that tmux performs as a RESULT
				// of a window closing. That switch happens before the restore
				// runs, so recording it put tmux's own adjacent-window pick at
				// the head of the stack and the restore then "restored" to it
				// -- chasing its own tail. Only record a switch when the
				// window we are leaving still exists, i.e. a real navigation.
				leavingStillExists := l.activeWindowID == ""
				for _, w := range l.coord.GetWindows() {
					if w.ID == l.activeWindowID {
						leavingStillExists = true
						break
					}
				}
				if leavingStillExists {
					// Key the history by the client that made the switch. A
					// single shared stack mixes both clients' navigation when
					// two terminals are attached to one session.
					l.coord.TrackWindowHistoryForClient(activeTTY, newID)
				} else {
					logEvent("WINDOW_HISTORY_SKIP reason=post_close old=%s new=%s", l.activeWindowID, newID)
				}
			}
			l.SetActiveWindowID(newID)
		}
	} else {
		logEvent("UPDATE_ACTIVE_WINDOW_TMUX_ERR err=%v", err)
	}
}

// doPaneLayoutOps runs the spawn/cleanup heavy path inside @tabby_spawning,
// gated by loopPaneLayoutCooldown to prevent feedback loops where the tmux
// commands it issues fire pane-focus-in hooks → USR1 → another refresh
// cycle → re-entry. Was a local closure in the refresh-loop goroutine.
func (l *Loop) doPaneLayoutOps() {
	now := time.Now()
	status := l.coord.NewWindowStatus()
	logEvent("READY_STATE_TRACE phase=pane_layout_start state=%s ready=%s age_ms=%d active=%s", status.State, status.WindowID, time.Since(status.Created).Milliseconds(), l.activeWindowID)
	if status.State == "inFlight" {
		logEvent("PANE_LAYOUT_SKIP reason=new_window_inflight")
		return
	}
	if status.State == "ready" {
		age := time.Since(status.Created)
		if age > loopNewWindowReadyTimeout {
			logEvent("PANE_LAYOUT_READY_TIMEOUT_CLEAR window=%s age_ms=%d", status.WindowID, age.Milliseconds())
			l.coord.ClearNewWindowStatus()
			status = l.coord.NewWindowStatus()
		} else if age > loopNewWindowReadyHold {
			logEvent("PANE_LAYOUT_SKIP reason=new_window_ready window=%s age_ms=%d", status.WindowID, age.Milliseconds())
			return
		}
	}
	// Sessions in a group share their panes, so only the group's elected
	// layout owner may spawn/kill chrome here. Without this, every daemon in
	// the group rewrites the same windows with its own client profile and they
	// undo each other every round. See layout_lease.go.
	if !l.coord.OwnsGroupLayout() {
		logEvent("PANE_LAYOUT_SKIP reason=not_group_layout_owner")
		return
	}
	if now.Sub(l.lastPaneLayoutOps) < loopPaneLayoutCooldown {
		logEvent("PANE_LAYOUT_SKIP cooldown_remaining=%dms", (loopPaneLayoutCooldown - now.Sub(l.lastPaneLayoutOps)).Milliseconds())
		return
	}
	l.lastPaneLayoutOps = now
	logEvent("PANE_LAYOUT_START activeProfile=%s sidebarHidden=%v newWindowState=%s",
		l.coord.ActiveClientProfile(), l.coord.sidebarHidden,
		status.State)
	cfg := l.coord.GetConfig()
	customBorder := cfg.PaneHeader.CustomBorder
	nativeBorders := cfg.PaneHeader.Native != nil && *cfg.PaneHeader.Native
	if nativeBorders {
		// Kill any leftover per-content-pane aux header panes from before
		// native mode was active. Do NOT touch @tabby_pane_headers — that
		// option also gates spawnWindowHeaders (which renders the phone-mode
		// bottom navigation bar). Window-headers must keep spawning.
		killLeftoverPaneHeaders()
	}
	pl0 := time.Now()
	preActive := l.coord.ActiveWindowID()
	tmuxCmd("set-option", "-g", "@tabby_spawning", "1").Run()
	windows := l.coord.GetWindows()
	pl1 := time.Now()
	spawnWindowHeaders(l.server, l.deps.SessionID, customBorder, l.coord.desiredWindowHeaderHeight(), windows, l.coord)
	pl2 := time.Now()
	if !nativeBorders {
		spawnPaneHeaders(l.server, l.deps.SessionID, customBorder, l.coord.desiredPaneHeaderHeight(), windows)
	}
	pl3 := time.Now()
	// Re-assert native pane-border labels on the dashboard window (the chrome
	// passes above reset pane-border-status/style each cycle). No-op when inactive.
	l.coord.applyDashboardBorders()
	if nativeBorders {
		dashWin := dashboardActiveWindowID(l.coord.dashboardSession())
		for _, w := range windows {
			if w.ID == dashWin {
				continue
			}
			l.coord.applyNativeBorders(w.ID, w.Group)
		}
	}
	pl4 := time.Now()
	// Profile transitions (desktop ↔ phone) issue kill-pane + split-window
	// across multiple windows during this bracket. Even with `split-window -d`
	// the kill-pane sequence can silently flip the session's current-window
	// to a sibling (same class of bug documented at coordinator.go:5535 for
	// join-pane). Tmux does NOT emit `after-select-window` for the silent
	// flip, so the next refresh-tick reads tmux's current window and adopts
	// the wrong active. Detect by comparing pre/post and undo before lifting
	// the spawning bracket.
	if preActive != "" {
		postActive := l.coord.ActiveWindowID()
		if postActive != "" && postActive != preActive {
			// The current window changed during the bracket. Distinguish two
			// causes:
			//  - A SILENT flip from our own kill-pane/split-window churn — tmux
			//    moved current-window to a sibling the coordinator never chose.
			//    That is what this restore exists to undo.
			//  - A DELIBERATE user nav (prev/next-window, select-window) that
			//    landed mid-bracket. The nav path flips the coordinator's
			//    in-memory active flag synchronously (SetActiveWindowOptimistic),
			//    so postActive matching the coordinator's active means the user
			//    meant to be there — reverting it yanks them back to the old
			//    window (the "I switch and it switches back" bug).
			//  - A window CREATED during the bracket. tmux selects a new window
			//    the moment it exists, well before the coordinator's cached
			//    window list carries it or its active flag flips, so the
			//    coordinator-intends test above says "not intended" and the
			//    restore yanks the user straight back off the window they just
			//    opened (the "new windows disappear when I focus them" bug).
			// Only revert when the coordinator does NOT already intend
			// postActive AND postActive is a window that predates the bracket.
			switch {
			case postActive == l.coordinatorActiveWindowID():
				logEvent("PANE_LAYOUT_ADOPT_ACTIVE pre=%s post=%s reason=coordinator_intends", preActive, postActive)
			case !windowKnownAtBracketStart(windows, postActive):
				logEvent("PANE_LAYOUT_ADOPT_ACTIVE pre=%s post=%s reason=new_window", preActive, postActive)
			case newWindowFlowActive(l.coord):
				logEvent("PANE_LAYOUT_ADOPT_ACTIVE pre=%s post=%s reason=new_window_in_flight", preActive, postActive)
			default:
				logEvent("PANE_LAYOUT_RESTORE_ACTIVE pre=%s post=%s", preActive, postActive)
				_ = tmuxCmd("select-window", "-t", preActive).Run()
			}
		}
	}
	pl5 := time.Now()
	tmuxCmd("set-option", "-g", "@tabby_spawning", "0").Run()
	startOSCPipes(windows)
	pl6 := time.Now()
	cleanupOrphanedHeaders(customBorder, l.coord, l.activeWindowID)
	pl7 := time.Now()
	logEvent("PERF_PANE_LAYOUT pre_ms=%d winhdr_ms=%d panehdr_ms=%d borders_ms=%d active_ms=%d osc_ms=%d orphanhdr_ms=%d total_ms=%d windows=%d",
		pl1.Sub(pl0).Milliseconds(), pl2.Sub(pl1).Milliseconds(), pl3.Sub(pl2).Milliseconds(),
		pl4.Sub(pl3).Milliseconds(), pl5.Sub(pl4).Milliseconds(), pl6.Sub(pl5).Milliseconds(),
		pl7.Sub(pl6).Milliseconds(), pl7.Sub(pl0).Milliseconds(), len(windows))
	// NOTE: updateHeaderBorderStyles is NOT called here to avoid border
	// flickering. It's only called when windows hash changes (on refresh
	// + hash change) which is when groups/colors change.
	//
	// The legacy "drain refreshCh after spawn ops" loop is gone: once the
	// refresh body itself runs on the loop, flags.usr1 already provides
	// at-most-one-pending coalescing for the follow-up signal that our
	// tmux commands trigger.
}

// handleWindowCheckTick is the migrated body of the windowCheckTicker case in
// the daemon main select loop.
func (l *Loop) handleWindowCheckTick() {
	l.flags.window.Store(false)
	// Window check is a polling task — stalls are non-fatal (skip and retry next tick)
	l.deps.RunLoopTaskNonFatal("window_check", 8*time.Second, func() {
		logEvent("WINDOW_CHECK_TICK")
		// Use cached window state — signal_refresh keeps it fresh via USR1.
		// Calling RefreshWindows() here added a redundant ListWindowsWithPanes()
		// tmux round-trip that caused lock contention and task stalls under load.
		windows := l.coord.GetWindows()
		windowIDs := make([]string, len(windows))
		for i, w := range windows {
			windowIDs[i] = w.ID
		}
		logEvent("WINDOW_CHECK_LIST count=%d ids=%v", len(windows), windowIDs)

		if l.coord.OwnsGroupLayout() {
			spawnRenderersForNewWindows(l.server, l.deps.SessionID, windows, l.coord)
			cleanupOrphanedSidebars(windows, l.coord)
			cleanupOrphanWindowsByTmux(l.deps.SessionID, l.coord)
		}
		// Width sync as fallback for missed events, only when active context changed
		activeTTY := ""
		activeW := 0
		if w, _, tty, _, ok := activeClientGeometry(); ok {
			activeTTY = strings.TrimSpace(tty)
			activeW = w
		}
		activeWindowID := l.ActiveWindowID()
		syncKey := fmt.Sprintf("%s|%s|%d", activeWindowID, activeTTY, activeW)
		if syncKey != l.lastWindowCheck {
			logEvent("WIDTH_SYNC_REQUEST trigger=window_check active=%s force=0 key=%s", activeWindowID, syncKey)
			// Fallback path: width-only reconcile. SkipBroadcast — window-check
			// is a polling task, not a user-driven event; if no ops are needed
			// nothing changed worth re-rendering for.
			res := l.Reconcile(ReconcileOpts{
				Reason:         "window_check",
				ActiveWindowID: activeWindowID,
				ForceWidthSync: false,
				SkipBroadcast:  true,
			})
			if res.WindowOps+res.WidthOps+res.HeaderOps > 0 {
				l.server.BroadcastRender()
			}
			l.lastWindowCheck = syncKey
		} else {
			logEvent("WIDTH_SYNC_SKIP trigger=window_check reason=stable_context key=%s", syncKey)
		}
	})
}

// ReconcileOpts controls a single reconcile cycle. Reason is recorded in
// log lines; SkipBroadcast suppresses the trailing render (used when the
// caller will broadcast itself, e.g. signal_refresh after spawn/cleanup).
// If LockWindowsToActive is non-nil, every window is forced to that
// geometry as part of the chained tmux command.
type ReconcileOpts struct {
	Reason              string
	ActiveWindowID      string
	ForceWidthSync      bool
	LockWindowsToActive *daemon.ActiveClient
	SkipBroadcast       bool
}

// ReconcileResult reports counts so callers can surface diagnostics.
type ReconcileResult struct {
	WindowOps int
	WidthOps  int
	HeaderOps int
}

// Reconcile is the single entry point for "compute desired tmux geometry,
// emit one batched tmux command, then broadcast once." Replaces the
// previously interleaved sequence of resizeAllWindowsToClient + RunWidthSync
// + RunHeaderHeightSync + multiple BroadcastRenders that fired one
// after-resize-pane hook per resize-pane subprocess.
//
// All three planners run, their ops are concatenated, and a single chained
// `tmux ... ; ... ; ...` command applies them all under @tabby_spawning=1
// so the spawn / focus-restore paths are suppressed during the cycle and
// hooks coalesce to one trailing fire (which the loop's flags.usr1 dedup
// then collapses to at most one follow-up signal_refresh).
func (l *Loop) Reconcile(opts ReconcileOpts) ReconcileResult {
	activeWin := strings.TrimSpace(opts.ActiveWindowID)
	if activeWin == "" {
		// Ask the elected physical client, not tmux's idea of "the current
		// session". Unqualified display-message answers for this daemon's own
		// session, which in a grouped pair is not the session the client is
		// attached to — so a reconcile fired seconds after a new window opened
		// planned widths and header heights against the OLD window while the
		// user was already looking at the new one, and the two passes fought
		// (observed as back-to-back HEADER_HEIGHT_SYNC activeClient=@9 then
		// activeClient=@1). See clientDisplayedWindowID.
		activeWin = clientDisplayedWindowID()
	}
	if activeWin == "" {
		activeWin = l.coord.ActiveWindowID()
	}

	var ops []ResizeOp
	var windowOps int
	var layoutOps int

	lockedWidth := 0
	// Grouped sessions share these windows: a non-owner reconcile resizes
	// sidebars to ITS client's geometry, the owner measures the result and
	// adopts it, and the two daemons ping-pong the global width (observed
	// live as WIDTH_SYNC_ADOPT 15 -> 72 -> 15 within a minute, sidebars
	// visibly flapping). All tmux mutations below belong to the elected
	// layout owner, same as doPaneLayoutOps; rendering and broadcast are
	// unaffected.
	mutatesAllowed := l.coord.OwnsGroupLayout()
	if !mutatesAllowed {
		logEvent("RECONCILE_SKIP reason=not_group_layout_owner trigger=%s active=%s", opts.Reason, activeWin)
	}
	if mutatesAllowed && opts.LockWindowsToActive != nil {
		ac := opts.LockWindowsToActive
		if ac.Width > 0 && ac.Height > 0 {
			lockedWidth = ac.Width

			// Snapshot every window's current layout under its current width
			// BEFORE we plan the resize. tmux scales splits greedily on
			// resize-window, so the only way to preserve user-visible
			// proportions across active-client switches is to remember the
			// pre-resize layout per (windowID, width) bucket and replay it
			// via select-layout when that width comes back.
			//
			// Single tmux read; cache writes happen in-process. Layouts for
			// single-pane windows are skipped (nothing to preserve).
			snaps := snapshotWindowLayouts()
			for _, s := range snaps {
				if s.Panes <= 1 {
					continue
				}
				if s.Width == lockedWidth {
					// Don't overwrite the saved layout for the target width
					// with the about-to-be-stale current layout — the
					// snapshot for the target width should reflect what the
					// user last left at that width, not what tmux just
					// scaled to during a transient mid-batch state.
					continue
				}
				l.coord.SaveWindowLayout(s.WindowID, s.Width, s.Layout)
			}

			windowOpsList := planAllWindowsToClient(ac.Width, ac.Height, "reconcile:"+opts.Reason)
			windowOps = len(windowOpsList)

			// Interleave: each OpResizeWindow is followed immediately by an
			// OpSelectLayout for that window if we have a cached layout at
			// the target width. The single chained tmux command then runs
			// `resize-window @1 ; select-layout @1 "..." ; resize-window @2 ; ...`
			// — one invocation, one SIGWINCH cascade, proportional restore.
			for _, op := range windowOpsList {
				ops = append(ops, op)
				if op.Kind != OpResizeWindow {
					continue
				}
				cached := l.coord.GetWindowLayout(op.Target, lockedWidth)
				if cached == "" {
					continue
				}
				// The cache is keyed by width only, but select-layout scales a
				// height-mismatched layout to fit the window — and that scaling
				// squashes the fixed-height phone button bar (3 rows) down,
				// which the next header-height-sync then grows back, producing a
				// visible flicker. With several differently-sized clients sharing
				// a grouped session the cached height routinely disagrees with the
				// window's current height, so replay only when the heights match;
				// the resize-window above already gives a sane layout otherwise.
				if h := layoutOuterHeight(cached); h > 0 && h != ac.Height {
					logEvent("RESTORE_LAYOUT_SKIP window=%s width=%d cachedH=%d targetH=%d reason=height_mismatch",
						op.Target, lockedWidth, h, ac.Height)
					continue
				}
				ops = append(ops, ResizeOp{
					Kind:    OpSelectLayout,
					Target:  op.Target,
					Layout:  cached,
					Reason:  "restore_layout_at_width:" + opts.Reason,
					Subject: op.Target,
				})
				layoutOps++
			}
		}
	}

	logEvent("RECONCILE_START reason=%s active=%s force=%v lock_windows=%v locked_width=%d",
		opts.Reason, activeWin, opts.ForceWidthSync, opts.LockWindowsToActive != nil, lockedWidth)

	// PlanWidthSync/PlanHeaderHeights are not pure: planning measures the
	// shared windows and can ADOPT a measured sidebar width into the global
	// option. A non-owner running it still poisons the group width even with
	// its ops unflushed, so planning is owner-only too.
	var widthOps, headerOps []ResizeOp
	if mutatesAllowed {
		widthOps = l.coord.PlanWidthSync(activeWin, opts.ForceWidthSync)
		// Header heights need the POST-lock window width: this same batch will
		// resize every window to lockedWidth before the resize-pane ops fire,
		// so window-headers must target desiredHeight(lockedWidth), not
		// desiredHeight(current_tmux_width). Pass lockedWidth through so the
		// touch tab bar follows a desktop→phone switch in the same frame.
		headerOps = l.coord.PlanHeaderHeights(activeWin, lockedWidth)
		ops = append(ops, widthOps...)
		ops = append(ops, headerOps...)
	}

	// Sync the in-memory client snapshot against the geometry we are about
	// to apply. Done after planning, before flush, so render-time clamps
	// see the correct widths even if the tmux command races a redraw.
	syncClientSizesFromTmux(l.server, l.coord, "reconcile:"+opts.Reason)

	if len(ops) > 0 {
		flushOpsBatched(ops, "reconcile:"+opts.Reason)
	} else if mutatesAllowed {
		logEvent("RECONCILE_NOOP reason=%s active=%s", opts.Reason, activeWin)
	}

	if !opts.SkipBroadcast {
		l.server.BroadcastRender()
	}

	logEvent("RECONCILE_END reason=%s window_ops=%d layout_ops=%d width_ops=%d header_ops=%d total=%d skip_broadcast=%v",
		opts.Reason, windowOps, layoutOps, len(widthOps), len(headerOps), len(ops), opts.SkipBroadcast)

	return ReconcileResult{
		WindowOps: windowOps,
		WidthOps:  len(widthOps),
		HeaderOps: len(headerOps),
	}
}

// staleClientPruneInterval is how often the geometry tick scans for stale
// clients. Detaching is user-visible, so this stays deliberately infrequent.
const staleClientPruneInterval = time.Hour

// clientGeomBase is the geometry tick's normal period, and clientGeomMax the
// slowest it backs off to while idle. clientGeomIdleGrace is how many
// consecutive do-nothing ticks must pass before backoff starts, so a brief
// lull between keystrokes never costs responsiveness.
//
// Backoff is safe because the poll is a safety net rather than the primary
// signal, and because it only survives genuine inactivity:
//
//   - A real resize arrives through tmux's own client-resized hook, which the
//     plugin registers; the poll is the belt to that hook's braces.
//   - Any priority event — a renderer input or a tmux hook, i.e. anything the
//     user actually did — resets the count in Submit, so the tick is back at
//     the base rate before that action's consequences need noticing.
//
// That second rule is what makes backoff safe with several clients attached:
// the case the poll uniquely catches is the active-client election flipping
// between them, and a client the user is touching generates priority events.
const (
	clientGeomBase      = 250 * time.Millisecond
	clientGeomMax       = 2 * time.Second
	clientGeomIdleGrace = 12
)

// activityReconcileInterval is the floor between reconciles triggered purely
// by the client-activity clock advancing, with nothing else changed. Observed
// before this floor: one every 5-13s, indefinitely. Kept well under a minute
// so it still serves as the periodic full refresh it had become.
const activityReconcileInterval = 30 * time.Second

// geomTickInterval maps the idle-tick count to the next polling gap, doubling
// from clientGeomBase up to clientGeomMax once the grace period is past.
func (l *Loop) geomTickInterval() time.Duration {
	idle := l.geomIdleTicks.Load() - clientGeomIdleGrace
	if idle <= 0 {
		return clientGeomBase
	}
	d := clientGeomBase
	for i := int64(0); i < idle && d < clientGeomMax; i++ {
		d *= 2
	}
	if d > clientGeomMax {
		d = clientGeomMax
	}
	return d
}

// clientGeomSettle is how long a client's reported size must hold still before
// chrome is reflowed for it. The geometry tick runs every 250ms, so this is two
// ticks: long enough to swallow a renegotiation burst, short enough that a real
// resize still lands within a frame or two of the user letting go.
const clientGeomSettle = 500 * time.Millisecond

// geometrySettled reports whether resizeKey is a client size worth reflowing
// every window's chrome for, and records it as pending when it is not yet.
//
// A client's reported size is not trustworthy the instant it changes. mosh
// renegotiates through intermediate geometries whenever a phone keyboard opens
// or the device rotates, and each one reaches us as a perfectly ordinary
// resize: a phone that settles at 43x34 was observed at 82x13 on the way there.
// Reflowing for a size the client is about to stop having lays the sidebar and
// window headers out for a width it never really has, and the mismatch is
// visible — a sidebar rendered for 34 columns clipped into a 15-column pane.
//
// Waiting costs a few hundred milliseconds of slightly-wrong chrome on a real
// resize. Not waiting costs arbitrarily-wrong chrome until something else
// happens to trigger a reconcile, which on a quiet session can be a long time.
func (l *Loop) geometrySettled(resizeKey string, now time.Time) bool {
	// Only the size is debounced. An activity-only change reaches here with
	// the same size we already laid out for, and must not be delayed.
	if resizeKey == l.lastResizeKey {
		return true
	}
	// Nothing has been laid out yet (daemon start, first attach). There is no
	// correct chrome to protect, so believing the first size immediately beats
	// showing unstyled chrome for half a second.
	if l.lastResizeKey == "" {
		return true
	}
	if resizeKey != l.pendingGeom {
		l.pendingGeom = resizeKey
		l.pendingGeomSince = now
		logEvent("CLIENT_GEOMETRY_SETTLING key=%s", resizeKey)
		return false
	}
	return now.Sub(l.pendingGeomSince) >= clientGeomSettle
}

// handleClientGeomTick is the migrated body of the clientGeometryTicker case.
func (l *Loop) handleClientGeomTick() {
	l.flags.geom.Store(false)
	l.deps.RunLoopTaskNonFatal("client_geometry_tick", 2*time.Second, func() {
		// Prune before the unchanged-geometry early return below: a stale
		// client fighting over window size shows up precisely as geometry
		// that keeps flipping, and we still want it gone during the quiet
		// stretches in between.
		if time.Since(l.lastStaleClientPrune) >= staleClientPruneInterval {
			l.lastStaleClientPrune = time.Now()
			l.coord.PruneStaleClients()
		}
		res := l.elector.Elect()
		if res.Attached < 0 {
			// A tmux error, not an answer. Don't let a failing query look like
			// a quiet one and coast into the slow rate.
			l.geomIdleTicks.Store(0)
		}
		if !res.OK {
			// A genuine zero-attached-clients election (not a tmux error) means
			// the last client detached: drop the stale active-client snapshot so
			// the profile falls back to desktop instead of pinning whatever the
			// departed client (e.g. a phone) left behind.
			if res.Attached == 0 {
				l.coord.ClearActiveClient("no_attached_clients")
			}
			return
		}
		ac := res.Client
		// Cleared only once the forced reconcile below actually runs: an
		// early return here (unsettled geometry) must leave the request
		// standing for the next tick.
		relock := l.relockGeom.Load()
		geomKey := fmt.Sprintf("%s:%dx%d:%d", ac.TTY, ac.Width, ac.Height, res.Activity/5)
		if geomKey == l.lastClientGeom && !relock {
			// Nothing changed, and no user action has arrived to reset us:
			// this is the only path that may widen the gap.
			l.geomIdleTicks.Add(1)
			return
		}
		resizeKey := fmt.Sprintf("%s:%dx%d", ac.TTY, ac.Width, ac.Height)
		// An activity-only wake: same client, same size, and the only thing
		// that moved is the res.Activity/5 bucket in geomKey. On a machine
		// where some pane is always drawing (a log tail, an AI session) that
		// bucket rolls forever, so this fires every few seconds indefinitely
		// and drags a full ForceWidthSync reconcile along with it — re-syncing
		// widths to the values they already hold.
		//
		// The reconcile is not dropped, only rate-limited, because it doubles
		// as a periodic full refresh that other state leans on. Between those,
		// take the cheap half (the active-client snapshot) and skip the rest.
		activityOnly := !relock && l.lastResizeKey != "" && resizeKey == l.lastResizeKey
		if activityOnly && time.Since(l.lastActivityReconcile) < activityReconcileInterval {
			l.lastClientGeom = geomKey
			l.coord.SetActiveClient(ac)
			// A rolling clock is not user activity, so this must not count as
			// work: letting it reset the backoff would peg the poll at the
			// base rate forever on exactly those always-drawing machines.
			l.geomIdleTicks.Add(1)
			return
		}
		// Past here the tick has real work, so snap back to the base rate.
		l.geomIdleTicks.Store(0)
		if activityOnly {
			l.lastActivityReconcile = time.Now()
		}
		// Checked before lastClientGeom is committed: a deferred geometry must
		// stay "changed" so the next tick re-examines it. Recording it here
		// would make the settled size look already-handled, and the reflow it
		// is waiting for would never run.
		if !l.geometrySettled(resizeKey, time.Now()) {
			return
		}

		l.lastClientGeom = geomKey
		logEvent("CLIENT_GEOMETRY_CHANGE tty=%s size=%dx%d activity=%d", ac.TTY, ac.Width, ac.Height, res.Activity)
		l.coord.SetActiveClient(ac)
		var lockTo *daemon.ActiveClient
		if resizeKey != l.lastResizeKey || relock {
			l.lastResizeKey = resizeKey
			ac := ac // copy so we can take its address safely
			lockTo = &ac
		}
		if relock {
			l.relockGeom.Store(false)
			logEvent("GEOMETRY_RELOCK tty=%s size=%dx%d", ac.TTY, ac.Width, ac.Height)
		}
		l.Reconcile(ReconcileOpts{
			Reason: "geometry_tick",
			// The client we just elected is the one whose geometry changed, so
			// plan against the window IT is showing. Letting Reconcile fall back
			// to an unqualified query re-answers for the daemon's own session
			// and, in a grouped pair, names the wrong window.
			ActiveWindowID:      windowIDForClientTTY(ac.TTY),
			ForceWidthSync:      true,
			LockWindowsToActive: lockTo,
		})
		l.coord.RunZoomSync("") // intentional no-op (kept for symmetry / future use)
	})
}

// RequestGeometryRelock forces the next geometry tick to re-lock every window
// to the elected client's size, ignoring the lastClientGeom / lastResizeKey
// dedup. Also kicks a geometry tick so the relock lands now rather than at the
// next size change. Safe to call from any goroutine.
func (l *Loop) RequestGeometryRelock(reason string) {
	l.relockGeom.Store(true)
	// The submit below lands the relock immediately; this just returns the
	// ticker itself to the base rate rather than leaving it backed off.
	l.geomIdleTicks.Store(0)
	logEvent("GEOMETRY_RELOCK_REQUEST reason=%s", reason)
	l.submitCoalesced(&l.flags.geom, ClientGeomTickEvent{})
}

// handleWatchdogTick is the migrated body of the watchdogTicker case.
func (l *Loop) handleWatchdogTick() {
	l.flags.watchdog.Store(false)
	l.deps.RunLoopTask("watchdog", 6*time.Second, func() {
		logInput("HEALTH clients=%d", l.server.ClientCount())
		// watchdogCheckRenderers kills/respawns panes — chrome mutation,
		// owner-only like every other repair path (a non-owner daemon killed
		// the owner's full-width sidebar as "corrupt" mid-session).
		if l.coord.OwnsGroupLayout() {
			l.coord.ReconcileStashedSidebars()
			// Minimized windows stranded by a peer session that died still
			// holding the only tag pointing at them. Owner-gated so peers don't
			// race to adopt the same window.
			l.coord.reconcileOrphanedMinimizedWindows()
			watchdogCheckRenderers(l.server, l.deps.SessionID, l.coord)
		}
		panelAudit(l.deps.SessionID, l.coord)
	})
}

// handleRefreshTick is the migrated body of the refreshTicker case.
func (l *Loop) handleRefreshTick() {
	l.flags.refresh.Store(false)
	l.deps.RunLoopTask("refresh_tick", 8*time.Second, func() {
		// Fallback polling: always refresh windows (needed for staleness
		// detection of stuck @tabby_busy), but only broadcast render and
		// update header styles if the hash actually changed.
		l.coord.RefreshWindows()
		currentHash := l.coord.GetWindowsHash()
		if currentHash != l.lastWindowsHash {
			updateHeaderBorderStyles(l.coord)
			l.server.BroadcastRender()
			l.lastWindowsHash = currentHash
		}
	})
}

// handleAnimationTick is the migrated body of the animationTicker case.
//
// Render gate: any of three signals triggers a render — a visible spinner
// (Busy / AIBusy / AIInput on any window or pane), a pet-state change, or
// an animated active indicator (multi-frame frames configured).
//
// Frame-rate gate: spinner frames advance at 5 Hz visible (slowFrame =
// spinnerFrame/2) but the ticker runs at 10 Hz, so half the time we'd be
// repainting the same frame. We skip the render when the slow-frame index
// hasn't changed since the last animation render. Pet changes always
// render (pet animation isn't tied to the spinner frame).
func (l *Loop) handleAnimationTick() {
	l.flags.anim.Store(false)
	// Combined spinner + pet animation tick with timeout protection.
	// Animation is cosmetic — a stall just skips the frame (non-fatal).
	l.deps.RunLoopTaskNonFatal("animation_tick", 2*time.Second, func() {
		spinnerVisible, slowFrame := l.coord.IncrementSpinner()
		petChanged := l.coord.UpdatePetState()
		indicatorAnimated := l.coord.HasActiveIndicatorAnimation()
		anyAnim := spinnerVisible || indicatorAnimated
		if !anyAnim && !petChanged {
			return
		}
		// Frame dedup: render only when the slow frame index actually
		// advances, unless the pet changed (which is independent of the
		// spinner clock).
		if !petChanged && slowFrame == l.lastSlowFrame {
			return
		}
		l.lastSlowFrame = slowFrame
		// Every on-screen sidebar is painted from this one frame index, so a
		// second attached terminal stays in step with the first instead of
		// running its own clock.
		displayed := l.displayedWindowIDs()
		logRenderEvent("ANIMATION_TICK_RENDER spinner=%v pet=%v indicator=%v frame=%d windows=%v",
			spinnerVisible, petChanged, indicatorAnimated, slowFrame, displayed)
		perf.Log("animationTick (render)")
		l.server.RenderWindowsOnly(displayed...)
	})
}

// handleGitTick is the migrated body of the gitTicker case.
func (l *Loop) handleGitTick() {
	l.flags.git.Store(false)
	l.deps.RunLoopTask("git_tick", 6*time.Second, func() {
		// Only broadcast if git state changed
		currentGitState := l.coord.GetGitStateHash()
		if currentGitState != l.lastGitState {
			perf.Log("gitTick (changed)")
			l.coord.RefreshGit()
			l.coord.RefreshSession()
			l.server.BroadcastRender()
			l.lastGitState = currentGitState
		}
	})
}

// handleTeamClaudeTick kicks off a (throttled, coalesced) teamclaude quota
// refresh. RefreshTeamClaude returns immediately and does the HTTP fetch in a
// detached goroutine that triggers its own render on change, so this handler
// never blocks the event loop — no RunLoopTask wrapper needed.
func (l *Loop) handleTeamClaudeTick() {
	l.flags.teamClaude.Store(false)
	l.coord.RefreshTeamClaude()
}

// handleKimiTick mirrors handleTeamClaudeTick for the Kimi for Coding quota
// fetch: RefreshKimi returns immediately and does the HTTP fetch in a detached
// goroutine, so the loop is never blocked by the network.
func (l *Loop) handleKimiTick() {
	l.flags.kimi.Store(false)
	l.coord.RefreshKimi()
}

// handleTabSummaryTick triggers auto tab-summary generation. Like the TeamClaude
// handler, RefreshTabSummaries returns immediately and does the capture + LLM
// work in a coalesced goroutine, so the event loop never blocks.
func (l *Loop) handleTabSummaryTick() {
	l.flags.tabSummary.Store(false)
	l.coord.RefreshTabSummaries()
}

// handleSocketCheckTick is the migrated body of the socketCheckTicker case in
// the idle-monitor goroutine. Originally the goroutine returned after sending
// SIGTERM; here we just send the signal and let loopCtx cancellation stop
// further ticks at the runTicker level. sigCh has buffer 1 so a duplicate
// send is dropped via the default arm.
func (l *Loop) handleSocketCheckTick() {
	l.flags.socket.Store(false)
	// Check if our socket still exists
	if _, err := os.Stat(l.deps.SocketPath); os.IsNotExist(err) {
		logEvent("SHUTDOWN_REASON session=%s reason=socket_gone pid=%d", l.deps.SessionID, l.deps.MyPid)
		debugLog.Printf("Socket %s no longer exists, shutting down", l.deps.SocketPath)
		select {
		case l.deps.SigCh <- syscall.SIGTERM:
		default:
		}
		return
	}

	// Check if PID file still has our PID (another daemon may have taken over)
	pidPath := daemon.RuntimePath(l.deps.SessionID, ".pid")
	if data, err := os.ReadFile(pidPath); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid != l.deps.MyPid {
			logEvent("SHUTDOWN_REASON session=%s reason=pid_replaced our=%d new=%d", l.deps.SessionID, l.deps.MyPid, pid)
			debugLog.Printf("PID file replaced (ours=%d, new=%d), shutting down", l.deps.MyPid, pid)
			select {
			case l.deps.SigCh <- syscall.SIGTERM:
			default:
			}
			return
		}
	}
}

// handleSignal dispatches SIGUSR2 events on the loop goroutine. SIGUSR1
// is now delivered as RefreshSignalEvent directly (see SubmitRefresh) so
// it goes through the same coalescing path as renderer-input refresh and
// tmux-hook refresh requests.
func (l *Loop) handleSignal(e SignalEvent) {
	switch e.Sig {
	case syscall.SIGUSR2:
		l.flags.usr2.Store(false)
		l.handleClientResized()
	default:
		logEvent("LOOP_UNKNOWN_SIGNAL sig=%v", e.Sig)
	}
}

// SubmitRefresh enqueues a RefreshSignalEvent via flags.usr1 so the next
// handler iteration runs handleRefreshSignal. Producers (renderer-input
// path, coordinator.OnRefreshLayout, signal/hook routers) call this
// instead of poking a channel; the at-most-one-pending coalescing means
// rapid-fire triggers (e.g. a USR1 storm during spawn) collapse to one
// loop-side event.
func (l *Loop) SubmitRefresh() {
	l.submitCoalesced(&l.flags.usr1, RefreshSignalEvent{})
}

// handleRefreshSignal is the migrated body of the former signal_refresh
// for-select consumer goroutine in main.go. It runs entirely on the loop
// goroutine — no more cross-goroutine state mirror, no more channel.
//
// The handler updates the active-window snapshot, refreshes the
// coordinator's window list, runs the gated spawn/cleanup heavy path,
// and emits one batched Reconcile (which itself flushes a single tmux
// chained command and one trailing BroadcastRender). Wrapped in
// RunLoopTask for the existing 20s timeout protection.
func (l *Loop) handleRefreshSignal() {
	l.flags.usr1.Store(false)
	if l.deps.RunLoopTask == nil {
		// USR1 from a tmux hook can land before SetTickDeps wires l.deps.
		// Re-submit shortly — by then deps should be ready. Re-storing the
		// flag in place would wedge the pipeline: every future SubmitRefresh
		// CAS(false,true) would fail and silently drop, so handleRefreshSignal
		// would never run again and doPaneLayoutOps (the only place that
		// spawns window-headers) would never fire.
		time.AfterFunc(50*time.Millisecond, l.SubmitRefresh)
		return
	}
	l.deps.RunLoopTask("signal_refresh", 20*time.Second, func() {
		start := time.Now()
		logEvent("SIGNAL_REFRESH session=%s", l.deps.SessionID)

		prevActive := l.activeWindowID
		l.updateActiveWindow()
		windowChanged := l.activeWindowID != prevActive
		// Sync client sizes first so width sync sees real tmux dimensions
		// for both active and inactive windows after a client resize.
		sizesChanged := syncClientSizesFromTmux(l.server, l.coord, "signal_refresh")

		// Optimistic render is the 972d718 perf trick: flip the active
		// window flag and send only to the active sidebar so the
		// highlight follows Cmd+[/] before the full RefreshWindows
		// round-trip completes. Gate on actual window change so unrelated
		// refreshes don't pay the per-client send.
		if windowChanged {
			l.coord.SetActiveWindowOptimistic(l.activeWindowID)
			l.server.SendRenderToClient(l.activeWindowID)
			// The active window may be contested by an idle grouped session
			// pinning it to the wrong geometry — park it. Election changes
			// cover the device flip; this covers the desktop walking onto a
			// window the phone's session points at.
			go l.coord.EnforceSingleActiveClient("window:" + l.activeWindowID)
		}

		l.coord.RefreshWindows()
		t1 := time.Now()

		windowsAfterRefresh := l.coord.GetWindows()
		currentWindowCount := len(windowsAfterRefresh)
		if currentWindowCount < l.lastWindowCount && l.lastWindowCount > 0 {
			// Test the window that was active BEFORE updateActiveWindow() ran
			// at the top of this body. That call has already re-pointed
			// l.activeWindowID at whatever tmux focused after the close, so
			// checking it here always found a live window and the restore
			// always skipped with reason=active_exists -- the branch was
			// effectively dead. prevActive is the window that actually closed.
			closedActive := prevActive
			if closedActive == "" {
				closedActive = l.activeWindowID
			}
			activeStillExists := false
			for _, w := range windowsAfterRefresh {
				if w.ID == closedActive {
					activeStillExists = true
					break
				}
			}
			if !activeStillExists {
				logEvent("WINDOW_CLOSE_RESTORE_TRIGGER active=%s prev_count=%d count=%d", closedActive, l.lastWindowCount, currentWindowCount)
				l.coord.SelectPreviousWindow()
				l.updateActiveWindow() // Re-fetch after selecting
			} else {
				logEvent("WINDOW_CLOSE_RESTORE_SKIP reason=active_exists active=%s prev_count=%d count=%d", closedActive, l.lastWindowCount, currentWindowCount)
			}
		}
		l.lastWindowCount = currentWindowCount

		// SaveWindowLayouts / ApplyPaneDimming / EnforceStatusExclusivity used
		// to run here, on the loop goroutine, costing ~39ms p50 (p99 78ms) that
		// every queued keypress waited behind. All three are write-only tmux
		// side effects — nothing below consumes their results — so they now run
		// inside runHousekeeping alongside the other heavy work.

		// Heavy ops (spawn/cleanup/layout) only if enough time has
		// passed since the last full refresh. This breaks the feedback
		// loop: doPaneLayoutOps triggers tmux hooks → USR1 → signal_refresh
		// → doPaneLayoutOps again. With debounce, rapid signals only do
		// the fast path (Reconcile + final broadcast).
		// Broadcast NOW with the fresh active flag so every sidebar repaints
		// with the new highlight before the heavy housekeeping kicks in.
		go l.server.BroadcastRender()

		// Heavy housekeeping (spawn / cleanup / layout / reconcile) runs
		// async so the loop goroutine returns and the next queued input
		// (e.g. another tab nav) can dispatch immediately. Without this,
		// rapid cmd+option+[/] presses queue behind the ~300ms heavy body
		// and the user perceives every switch as laggy.
		//
		// TryLock(): if a previous housekeeping pass is still in flight
		// (e.g. user mashing keys), skip — the in-flight pass will pick up
		// the latest tmux state when it gets there.
		if l.housekeepingMu.TryLock() {
			snapActiveWindow := l.activeWindowID
			snapSizesChanged := sizesChanged
			snapWindowChanged := windowChanged
			go func() {
				defer l.housekeepingMu.Unlock()
				l.runHousekeeping(start, t1, snapActiveWindow, snapSizesChanged, snapWindowChanged)
			}()
		} else {
			logEvent("PERF_REFRESH_SKIP_HOUSEKEEPING reason=in_flight")
		}
	})
}

// runHousekeeping is the heavy spawn / cleanup / layout / reconcile pass that
// used to run synchronously inside signal_refresh's loop-task body. It now
// runs in a goroutine kicked off by handleRefreshSignal so the loop goroutine
// is free to process the next input (e.g. another tab nav) immediately. The
// caller holds l.housekeepingMu via TryLock, which serializes successive
// passes — rapid-fire signals coalesce into "the in-flight pass + at most one
// more" rather than queueing arbitrarily.
func (l *Loop) runHousekeeping(start, t1 time.Time, activeWindowID string, sizesChanged, windowChanged bool) {
	tA := time.Now()
	l.coord.SaveWindowLayouts()
	tB := time.Now()
	l.coord.ApplyPaneDimming(activeWindowID)
	tC := time.Now()
	l.coord.EnforceStatusExclusivity(l.deps.SessionID)
	tD := time.Now()
	logEvent("PERF_PRE_SPAWN saveLayouts_ms=%d dim_ms=%d statusEx_ms=%d total_ms=%d",
		tB.Sub(tA).Milliseconds(), tC.Sub(tB).Milliseconds(),
		tD.Sub(tC).Milliseconds(), tD.Sub(tA).Milliseconds())

	structureChanged := false
	if time.Since(l.lastFullRefresh) >= loopFullRefreshCooldown {
		windows := l.coord.GetWindows()
		spawnedRenderer := spawnRenderersForNewWindows(l.server, l.deps.SessionID, windows, l.coord)
		t2 := time.Now()

		cleanupOrphanedSidebars(windows, l.coord)
		cleanupOrphanWindowsByTmux(l.deps.SessionID, l.coord)
		t3 := time.Now()

		cleanupSidebarsForClosedWindows(l.server, windows, l.coord)
		t4 := time.Now()

		l.doPaneLayoutOps()
		t5 := time.Now()

		_ = spawnedRenderer

		l.coord.ApplyNewWindowGroup()
		l.coord.PreserveWindowNames()

		// Two distinct triggers off two hashes:
		//  - The FULL hash (includes each window's active state + colors)
		//    drives updateHeaderBorderStyles: pane borders / header follow the
		//    active tab's color, so a mere active-window flip must repaint.
		//  - The STRUCTURAL hash (window set / names / groups / colors / pane
		//    counts, but NOT active-window or active-pane state) drives
		//    structureChanged, which gates the heavy LockWindowsToActive
		//    reconcile that resizes EVERY window to the elected client.
		//
		// Keeping active-window state OUT of structureChanged is what stops
		// the multi-client focus-cycling loop: with >1 client attached, tmux
		// 3.x flips the session's active window as the elector churns, and
		// when active state was in the structural hash each flip re-triggered
		// the resize-all-windows lock, which kicked the elector again — a
		// self-sustaining cascade seeded by any window mutation (e.g. ssh-in
		// renaming/recoloring a tab). Genuine client-geometry width-locks
		// (phone<->desktop) are already handled, dedup'd on lastResizeKey, by
		// handleClientGeomTick and handleClientResized — not this path.
		currentHash := l.coord.GetWindowsHash()
		if currentHash != l.lastWindowsHash {
			updateHeaderBorderStyles(l.coord)
		}
		l.lastWindowsHash = currentHash

		structHash := l.coord.GetWindowsStructuralHash()
		structureChanged = spawnedRenderer || structHash != l.lastStructuralHash
		l.lastStructuralHash = structHash
		l.lastFullRefresh = time.Now()

		logEvent("PERF_REFRESH refresh_ms=%d spawn_ms=%d cleanup1_ms=%d cleanup2_ms=%d layout_ms=%d total_ms=%d",
			t1.Sub(start).Milliseconds(), t2.Sub(t1).Milliseconds(),
			t3.Sub(t2).Milliseconds(), t4.Sub(t3).Milliseconds(),
			t5.Sub(t4).Milliseconds(), t5.Sub(start).Milliseconds())
	} else {
		logEvent("PERF_REFRESH_FAST refresh_ms=%d", t1.Sub(start).Milliseconds())
	}

	var lockTo *daemon.ActiveClient
	if structureChanged {
		if w, h, tty, _, ok := activeClientGeometry(); ok {
			lockTo = &daemon.ActiveClient{TTY: tty, Width: w, Height: h}
		}
	}
	reason := "signal_refresh"
	if structureChanged {
		reason = "signal_refresh.structure"
	}
	logEvent("WIDTH_SYNC_REQUEST trigger=%s active=%s force=%v window_changed=%v",
		reason, activeWindowID, sizesChanged, windowChanged)
	l.Reconcile(ReconcileOpts{
		Reason:              reason,
		ActiveWindowID:      activeWindowID,
		ForceWidthSync:      sizesChanged,
		LockWindowsToActive: lockTo,
	})
}

// handleClientResized is the migrated body of the former SIGUSR2 goroutine
// in main.go, with the lastResizeKey dedup applied BEFORE resize work. The
// geom-tick handler at handleClientGeomTick already writes lastResizeKey;
// both paths share the same field so SIGUSR2 and the 250ms geom tick dedup
// against each other. This is the deliberate behavior change in Step 3.
func (l *Loop) handleClientResized() {
	logEvent("SIGNAL_USR2_CLIENT_RESIZED")
	w, h, tty, _, ok := activeClientGeometry()
	if !ok {
		return
	}
	key := fmt.Sprintf("%s:%dx%d", tty, w, h)
	if key == l.lastResizeKey {
		logEvent("CLIENT_RESIZED_NOOP key=%s", key)
		return
	}
	l.lastResizeKey = key

	l.coord.SetActiveClientWidth(w)
	logEvent("SIGUSR2_ACTIVE_CLIENT tty=%s size=%dx%d", tty, w, h)
	ac := daemon.ActiveClient{TTY: tty, Width: w, Height: h}
	l.Reconcile(ReconcileOpts{
		Reason:              "client_resized",
		ForceWidthSync:      true,
		LockWindowsToActive: &ac,
	})
	logEvent("SIGNAL_USR2_DONE")
}

// handleIdleTick is the migrated body of the idleTicker case in the
// idle-monitor goroutine. See handleSocketCheckTick for the goroutine-return
// vs SIGTERM semantics.
func (l *Loop) handleIdleTick() {
	l.flags.idle.Store(false)
	// Check if session still exists
	if _, err := tmuxCmd("has-session", "-t", l.deps.SessionID).Output(); err != nil {
		logEvent("SHUTDOWN_REASON session=%s reason=session_gone", l.deps.SessionID)
		debugLog.Printf("Session %s no longer exists, shutting down", l.deps.SessionID)
		select {
		case l.deps.SigCh <- syscall.SIGTERM:
		default:
		}
		return
	}

	// Check if any windows remain
	out, err := tmuxCmd("list-windows", "-t", l.deps.SessionID, "-F", "#{window_id}").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		logEvent("SHUTDOWN_REASON session=%s reason=no_windows", l.deps.SessionID)
		debugLog.Printf("No windows remaining, shutting down")
		select {
		case l.deps.SigCh <- syscall.SIGTERM:
		default:
		}
		return
	}

	// Idle timeout only when nobody is using the session: no daemon render
	// clients (server socket) AND no tmux clients attached. The tmux check
	// guards against suiciding while you're still attached but the render panes
	// are briefly disconnected (e.g. right after a daemon restart) — which would
	// otherwise strand the session with no daemon left to drive it. tmux is only
	// queried in the already-rare 0-render-client case (short-circuit).
	if l.server.ClientCount() == 0 && tmuxAttachedClients(l.deps.SessionID) == 0 {
		if l.idleStart.IsZero() {
			l.idleStart = time.Now()
		} else if time.Since(l.idleStart) > 30*time.Second {
			logEvent("SHUTDOWN_REASON session=%s reason=idle_timeout clients=0", l.deps.SessionID)
			debugLog.Printf("No clients for 30s, shutting down")
			// Idle timeout is an intentional stop, so claim the clean-stop
			// sentinel the SIGTERM handler deliberately never writes.
			// Without it an unused grouped session respawns forever: the
			// watchdog restarts us, we find no clients again, quit again.
			os.WriteFile(daemon.RuntimePath(l.deps.SessionID, ".clean-stop"), []byte("idle-timeout"), 0644)
			select {
			case l.deps.SigCh <- syscall.SIGTERM:
			default:
			}
			return
		}
	} else {
		l.idleStart = time.Time{}
	}
}

// tmuxAttachedClients returns how many tmux clients are attached to the session.
// Used as a safety guard before idle-shutdown: a daemon should never exit while
// a real user is attached, even if the render panes are momentarily disconnected.
func tmuxAttachedClients(sessionID string) int {
	out, err := tmuxCmd("list-clients", "-t", sessionID, "-F", "#{client_tty}").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// handleTmuxHook routes a tmux-hook delivery (now arriving as a socket
// message) into the existing loop-side handlers. Each hook ultimately wants
// to either trigger a refresh poke (USR1 path) or a resize-recheck (USR2
// path); both paths already exist from Step 3, so this is just routing.
//
// Backward compat: the daemon still accepts SIGUSR1/SIGUSR2, and
// `lastResizeKey` (shared with handleClientGeomTick / handleClientResized)
// dedups any duplicate signal+hook fires during a partial-upgrade window
// where an older `tabby hook` binary still uses `kill -USR2`.
func (l *Loop) handleTmuxHook(e TmuxHookEvent) {
	logEvent("HOOK_RECV kind=%s args=%v", e.Kind, e.Args)
	switch e.Kind {
	case "client-resized":
		// Mirror the SIGUSR2 path. The args carry tty/width/height directly,
		// but handleClientResized re-queries activeClientGeometry() because
		// the elector may have a more current pin than the firing client's
		// raw geometry. lastResizeKey dedup applies.
		l.handleClientResized()
	case "after-select-window":
		// Queue a refresh so spawn/cleanup runs. Submitting through
		// flags.usr1 collapses bursts (e.g. cmd+] mash) to one body run.
		l.SubmitRefresh()
	case "after-rename-window":
		// A window rename (CWD/project switch, manual rename, or our OWN AI
		// rename echoing back) — schedule a tab-summary pass. Route through the
		// same coalescing flag as the interval tick so a burst of renames (an
		// active tab whose title updates rapidly, or our own rename re-firing this
		// hook) collapses to a single pass instead of hammering the loop. The
		// per-window summaryCooldown in RefreshTabSummaries then bounds how often
		// any one window is actually re-named, which is what breaks the storm.
		l.submitCoalesced(&l.flags.tabSummary, TabSummaryTickEvent{})
	case "after-resize-pane":
		// The hook fires for any pane resize; the `tabby hook on-pane-resize`
		// CLI side already filters to sidebar/header panes before sending,
		// so if the daemon sees this hook the filter has already passed.
		l.SubmitRefresh()
	case "client-attached":
		// `tabby cycle-pane --ensure-content` runs from the tmux-hook
		// command string itself (not via the daemon); the daemon-side hook
		// event is just a refresh poke so spawn/cleanup observes the new
		// client immediately.
		l.SubmitRefresh()
		// Phone-class client (< 100 cols) attaching while the gathered
		// dashboard grid is open: auto-exit so the phone user lands back in
		// their normal windows. The existing profile-transition path only
		// fires when the phone client becomes the active one — if a desktop
		// stays active, the phone would see the dashboard until then.
		l.coord.maybeExitDashboardForPhone()
	case "new-window-pending":
		// A keybinding-spawned new tab (bin/tabby new-window via prefix-c /
		// M-n) registering its pending status. The daemon "+"-click path sets
		// this in-process; the keybinding path lives in a SEPARATE process, so
		// without this hook the daemon never learns the firing client and the
		// post-reorder focus re-assert (preferredWindowFocusTarget, gated on
		// State=="ready") is skipped — tmux's fallback election then lands on
		// the first window (the "new tab cycles to window 1" bug). Registering
		// here lets restoreWindowFocus re-select the new window after the
		// move-window renumber shuffle. Idempotent with the in-process set.
		l.coord.SetNewWindowInFlight(e.Args["group"], e.Args["path"], e.Args["tty"])
	case "new-window-ready":
		// Marks the just-created window's id so the focus re-assert can fire.
		// Sent by bin/new-window right after creation, before it clears
		// @tabby_spawning (which gates the reorder), so State is "ready" by the
		// time the renumber refresh runs.
		l.coord.SetNewWindowReady(e.Args["window"])
	default:
		logEvent("HOOK_UNKNOWN_KIND kind=%s", e.Kind)
	}
}
