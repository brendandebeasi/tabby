package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
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

	// nav-settle state, written by handleRendererInput and read both by the
	// loop itself and by the main select loop in main.go.
	navMu                 sync.RWMutex
	lastExplicitNavAt     time.Time
	lastExplicitNavWindow string
	navSettleUntil        time.Time
	navSettledWindow      string
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

// Run dispatches events sequentially until ctx is cancelled.
func (l *Loop) Run(ctx context.Context) {
	logEvent("LOOP_START")
	for {
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
	default:
		logEvent("LOOP_UNKNOWN_EVENT kind=%s", ev.kind())
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
