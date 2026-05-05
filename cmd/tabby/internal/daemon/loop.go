package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/perf"
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
type AutoThemeTickEvent struct{}
type WatchdogTickEvent struct{}
type IdleTickEvent struct{}
type SocketCheckTickEvent struct{}

func (ClientGeomTickEvent) kind() string  { return "tick:client_geom" }
func (WindowCheckTickEvent) kind() string { return "tick:window_check" }
func (AnimationTickEvent) kind() string   { return "tick:animation" }
func (RefreshTickEvent) kind() string     { return "tick:refresh" }
func (GitTickEvent) kind() string         { return "tick:git" }
func (AutoThemeTickEvent) kind() string   { return "tick:auto_theme" }
func (WatchdogTickEvent) kind() string    { return "tick:watchdog" }
func (IdleTickEvent) kind() string        { return "tick:idle" }
func (SocketCheckTickEvent) kind() string { return "tick:socket_check" }

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
	UpdateActiveWindow  func()

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
	events chan Event
	drops  atomic.Uint64

	coord     *Coordinator
	server    *daemon.Server
	elector   *daemon.ClientElector
	refreshCh chan<- struct{}

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

	// Tick-handler state. These were locals in the surrounding Daemon
	// closure pre-migration. activeWindowID and lastWindowsHash are still
	// mutated by the refreshCh consumer in main.go's `Run` (a separate
	// goroutine from the loop), which mirrors them back via SetActiveWindowID
	// / SetLastWindowsHash. The mirror exists because there is one off-loop
	// writer (the spawn/cleanup pipeline). Step 5 chose Option B (keep the
	// mirror, rename the mutex to make the cross-goroutine intent explicit)
	// rather than Option A (migrate the refreshCh consumer onto the loop) —
	// the latter is a much larger diff that would not fit safely in this
	// step. The remaining fields are touched only from loop-goroutine
	// handlers and need no synchronization.
	sharedStateMu   sync.RWMutex
	activeWindowID  string
	lastWindowsHash string
	lastGitState    string
	lastAutoTheme   string
	lastClientGeom  string
	lastResizeKey   string
	lastWindowCheck string

	// Off-loop ticker state.
	idleStart time.Time
}

// NewLoop constructs a Loop. refreshCh is the existing main-loop refresh
// channel; the loop sends a non-blocking poke on it after a renderer input
// that needs a full refresh.
func NewLoop(coord *Coordinator, server *daemon.Server, elector *daemon.ClientElector, refreshCh chan<- struct{}) *Loop {
	return &Loop{
		events:    make(chan Event, 256),
		coord:     coord,
		server:    server,
		elector:   elector,
		refreshCh: refreshCh,
	}
}

// SetTickDeps wires closures from the Daemon scope (runLoopTask,
// updateActiveWindow, etc.) onto the Loop so handle*Tick methods can call
// them. Must be called before any tick events are enqueued.
func (l *Loop) SetTickDeps(deps LoopTickDeps) {
	l.deps = deps
}

// SetActiveWindowID is used by the main-goroutine refreshCh handler to
// publish the latest active-window observation to the loop. Step 3 will move
// this writer onto the loop itself, at which point the mutex can drop.
func (l *Loop) SetActiveWindowID(id string) {
	l.sharedStateMu.Lock()
	l.activeWindowID = id
	l.sharedStateMu.Unlock()
}

// ActiveWindowID returns the currently-tracked active window ID.
func (l *Loop) ActiveWindowID() string {
	l.sharedStateMu.RLock()
	defer l.sharedStateMu.RUnlock()
	return l.activeWindowID
}

// SetLastWindowsHash is used by the main-goroutine refreshCh handler.
func (l *Loop) SetLastWindowsHash(h string) {
	l.sharedStateMu.Lock()
	l.lastWindowsHash = h
	l.sharedStateMu.Unlock()
}

// LastWindowsHash returns the most recently observed windows hash.
func (l *Loop) LastWindowsHash() string {
	l.sharedStateMu.RLock()
	defer l.sharedStateMu.RUnlock()
	return l.lastWindowsHash
}

// SetLastAutoTheme primes the auto-theme tracker so the first tick after
// startup compares against the theme that was active at boot, not the empty
// string. Called once by main.go before tick goroutines start.
func (l *Loop) SetLastAutoTheme(name string) {
	l.lastAutoTheme = name
}

// Submit enqueues an event for the loop. If the queue is full, the event is
// dropped and a LOOP_DROP line is logged. This is intentional: a backed-up
// loop dropping a redundant tick is preferable to blocking the producer.
func (l *Loop) Submit(ev Event) {
	select {
	case l.events <- ev:
	default:
		l.drops.Add(1)
		logEvent("LOOP_DROP kind=%s queue_full drops_total=%d", ev.kind(), l.drops.Load())
	}
}

// Run dispatches events sequentially until ctx is cancelled. The heartbeat
// is bumped on each iteration so the deadlock watchdog (5s threshold) sees
// liveness from this goroutine — pre-Step-2 the heartbeat lived in the
// main-goroutine for-select that fired tickers up to 10 Hz; with tickers
// now driving the loop, the loop is the natural heartbeat source.
func (l *Loop) Run(ctx context.Context) {
	logEvent("LOOP_START")
	for {
		recordHeartbeat()
		select {
		case <-ctx.Done():
			logEvent("LOOP_STOP drops=%d", l.drops.Load())
			return
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
	case AutoThemeTickEvent:
		l.handleAutoThemeTick()
	case WatchdogTickEvent:
		l.handleWatchdogTick()
	case IdleTickEvent:
		l.handleIdleTick()
	case SocketCheckTickEvent:
		l.handleSocketCheckTick()
	case SignalEvent:
		l.handleSignal(e)
	case TmuxHookEvent:
		l.handleTmuxHook(e)
	default:
		logEvent("LOOP_UNKNOWN_EVENT kind=%s", ev.kind())
	}
}

// runTicker drives a fn at cadence d until ctx is cancelled. Used by main.go
// to fire one of the per-tick submitCoalesced calls.
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
	if daemon.KindOf(clientID) == daemon.TargetHook && (resolvedAction == "" || resolvedAction == "exit_if_no_main" || resolvedAction == "exit_if_no_main_windows") {
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
	if needsRefresh {
		// Immediate optimistic render: HandleInput already updated the
		// coordinator state (e.g. SetActiveWindowOptimistic for select_window)
		// so rendering NOW gives the requesting client the correct header
		// color without waiting for the full BroadcastRender round-trip.
		l.server.SendRenderToClient(clientID)
		// Broadcast to remaining clients asynchronously so the loop
		// goroutine is not blocked by O(n) renders before returning.
		go l.server.BroadcastRender()
		// Signal the main refresh loop for full state sync
		// (spawn/cleanup renderers, update pane colors, etc.)
		select {
		case l.refreshCh <- struct{}{}:
		default:
			// Channel full, refresh already pending
		}
		logEvent("INPUT_SIGNALED_REFRESH client=%s", clientID)
	} else {
		// Internal-only state change (e.g. toggle_group) - render the
		// requesting client immediately for snappy response, then broadcast
		// to remaining clients asynchronously.
		l.server.SendRenderToClient(clientID)
		go l.server.BroadcastRender()
	}
	logEvent("INPUT_DONE client=%s", clientID)
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

		spawnRenderersForNewWindows(l.server, l.deps.SessionID, windows, l.coord)
		cleanupOrphanedSidebars(windows, l.coord)
		cleanupOrphanWindowsByTmux(l.deps.SessionID, l.coord)
		// Persist current layouts to disk for restart recovery
		saveLayoutsToDisk(windows)
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
			l.coord.RunWidthSync(activeWindowID, false)
			l.lastWindowCheck = syncKey
		} else {
			logEvent("WIDTH_SYNC_SKIP trigger=window_check reason=stable_context key=%s", syncKey)
		}
	})
}

// handleClientGeomTick is the migrated body of the clientGeometryTicker case.
func (l *Loop) handleClientGeomTick() {
	l.flags.geom.Store(false)
	l.deps.RunLoopTaskNonFatal("client_geometry_tick", 2*time.Second, func() {
		res := l.elector.Elect()
		if !res.OK {
			return
		}
		ac := res.Client
		geomKey := fmt.Sprintf("%s:%dx%d:%d", ac.TTY, ac.Width, ac.Height, res.Activity/5)
		if geomKey == l.lastClientGeom {
			return
		}
		l.lastClientGeom = geomKey
		logEvent("CLIENT_GEOMETRY_CHANGE tty=%s size=%dx%d activity=%d", ac.TTY, ac.Width, ac.Height, res.Activity)
		l.coord.SetActiveClient(ac)
		resizeKey := fmt.Sprintf("%s:%dx%d", ac.TTY, ac.Width, ac.Height)
		if resizeKey != l.lastResizeKey {
			l.lastResizeKey = resizeKey
			resizeAllWindowsToClient(ac.Width, ac.Height, "geometry_tick")
		}
		syncClientSizesFromTmux(l.server, l.coord, "geometry_tick")
		activeWin := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
		logEvent("WIDTH_SYNC_REQUEST trigger=geometry_tick active=%s force=1", activeWin)
		l.coord.RunWidthSync(activeWin, true)
		l.coord.RunHeaderHeightSync(activeWin)
		l.coord.RunZoomSync(activeWin)
		l.server.BroadcastRender()
	})
}

// handleWatchdogTick is the migrated body of the watchdogTicker case.
func (l *Loop) handleWatchdogTick() {
	l.flags.watchdog.Store(false)
	l.deps.RunLoopTask("watchdog", 6*time.Second, func() {
		logInput("HEALTH clients=%d", l.server.ClientCount())
		watchdogCheckRenderers(l.server, l.deps.SessionID, l.coord)
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
		if currentHash != l.LastWindowsHash() {
			updateHeaderBorderStyles(l.coord)
			l.server.BroadcastRender()
			l.SetLastWindowsHash(currentHash)
		}
	})
}

// handleAnimationTick is the migrated body of the animationTicker case.
//
// The render gate combines three signals: a visible spinner (busy/bell/
// activity/AIBusy/AIInput on any window or pane), a pet-state change, and an
// "indicator animation configured" capability check. The third is a static
// config check that returns true whenever multi-frame active-indicator
// frames are configured — which is the default — so the gate effectively
// runs at full 10 Hz on most installs. Logged below so we can confirm the
// driver in events.log when investigating render churn.
func (l *Loop) handleAnimationTick() {
	l.flags.anim.Store(false)
	// Combined spinner + pet animation tick with timeout protection.
	// Animation is cosmetic — a stall just skips the frame (non-fatal).
	l.deps.RunLoopTaskNonFatal("animation_tick", 2*time.Second, func() {
		spinnerVisible := l.coord.IncrementSpinner()
		petChanged := l.coord.UpdatePetState()
		indicatorAnimated := l.coord.HasActiveIndicatorAnimation()
		if spinnerVisible || petChanged || indicatorAnimated {
			logEvent("ANIMATION_TICK_RENDER spinner=%v pet=%v indicator=%v",
				spinnerVisible, petChanged, indicatorAnimated)
			perf.Log("animationTick (render)")
			l.server.RenderActiveWindowOnly(l.ActiveWindowID())
		}
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

// handleAutoThemeTick is the migrated body of the autoThemeTicker case.
func (l *Loop) handleAutoThemeTick() {
	l.flags.autoTheme.Store(false)
	l.deps.RunLoopTaskNonFatal("auto_theme_tick", 5*time.Second, func() {
		want := l.coord.ResolveAutoTheme()
		if want != "" && want != l.lastAutoTheme {
			logEvent("AUTO_THEME_SWITCH from=%s to=%s", l.lastAutoTheme, want)
			l.coord.SetTheme(want)
			l.server.BroadcastRender()
			l.lastAutoTheme = want
		}
	})
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

// handleSignal dispatches SIGUSR1 / SIGUSR2 events on the loop goroutine.
// The flag is cleared at entry (matching the tick-handler convention) so a
// signal arriving while this handler is mid-run can re-queue.
func (l *Loop) handleSignal(e SignalEvent) {
	switch e.Sig {
	case syscall.SIGUSR1:
		l.flags.usr1.Store(false)
		l.handleRefreshSignal()
	case syscall.SIGUSR2:
		l.flags.usr2.Store(false)
		l.handleClientResized()
	default:
		logEvent("LOOP_UNKNOWN_SIGNAL sig=%v", e.Sig)
	}
}

// handleRefreshSignal is the migrated body of the former SIGUSR1 goroutine in
// main.go. refreshCh is still sourced by coordinator.OnRefreshLayout and by
// handleRendererInput, so we keep the channel and just poke it from here;
// the heavy signal_refresh handler still lives in main.go's for-select for
// now (its activeWindowID / lastWindowsHash mutations are migrated by Step 5).
func (l *Loop) handleRefreshSignal() {
	logEvent("SIGNAL_USR1")
	// Non-blocking send: l.flags.usr1 already gives us at-most-one-pending
	// semantics at the loop level, so the prior burst-collapse dance in
	// main.go is redundant here. If refreshCh is full, the existing pending
	// refresh will pick up our intent.
	select {
	case l.refreshCh <- struct{}{}:
	default:
		logEvent("SIGNAL_USR1 queue=full action=skipped")
	}
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
	resizeAllWindowsToClient(w, h, "sigusr2")
	logEvent("SIGUSR2_ACTIVE_CLIENT tty=%s size=%dx%d", tty, w, h)
	syncClientSizesFromTmux(l.server, l.coord, "sigusr2")
	activeWin := tmuxOutputTrimmed("display-message", "-p", "#{window_id}")
	logEvent("WIDTH_SYNC_REQUEST trigger=sigusr2 active=%s force=1", activeWin)
	l.coord.RunWidthSync(activeWin, true /* force */)
	go l.server.BroadcastRender()
	logEvent("SIGNAL_USR2_DONE active=%s", activeWin)
}

// handleIdleTick is the migrated body of the idleTicker case in the
// idle-monitor goroutine. See handleSocketCheckTick for the goroutine-return
// vs SIGTERM semantics.
func (l *Loop) handleIdleTick() {
	l.flags.idle.Store(false)
	// Check if session still exists
	if _, err := exec.Command("tmux", "has-session", "-t", l.deps.SessionID).Output(); err != nil {
		logEvent("SHUTDOWN_REASON session=%s reason=session_gone", l.deps.SessionID)
		debugLog.Printf("Session %s no longer exists, shutting down", l.deps.SessionID)
		select {
		case l.deps.SigCh <- syscall.SIGTERM:
		default:
		}
		return
	}

	// Check if any windows remain
	out, err := exec.Command("tmux", "list-windows", "-t", l.deps.SessionID, "-F", "#{window_id}").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		logEvent("SHUTDOWN_REASON session=%s reason=no_windows", l.deps.SessionID)
		debugLog.Printf("No windows remaining, shutting down")
		select {
		case l.deps.SigCh <- syscall.SIGTERM:
		default:
		}
		return
	}

	// Idle timeout if no clients
	if l.server.ClientCount() == 0 {
		if l.idleStart.IsZero() {
			l.idleStart = time.Now()
		} else if time.Since(l.idleStart) > 30*time.Second {
			logEvent("SHUTDOWN_REASON session=%s reason=idle_timeout clients=0", l.deps.SessionID)
			debugLog.Printf("No clients for 30s, shutting down")
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
		// Mirror the SIGUSR1 path: poke refresh so spawn/cleanup runs.
		l.handleRefreshSignal()
	case "after-resize-pane":
		// The hook fires for any pane resize; the `tabby hook on-pane-resize`
		// CLI side already filters to sidebar/header panes before sending,
		// so if the daemon sees this hook the filter has already passed.
		l.handleRefreshSignal()
	case "client-attached":
		// `tabby cycle-pane --ensure-content` runs from the tmux-hook
		// command string itself (not via the daemon); the daemon-side hook
		// event is just a refresh poke so spawn/cleanup observes the new
		// client immediately.
		l.handleRefreshSignal()
	default:
		logEvent("HOOK_UNKNOWN_KIND kind=%s", e.Kind)
	}
}
