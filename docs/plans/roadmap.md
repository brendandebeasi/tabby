# Roadmap — active backlog

Done items are cut to `roadmap-done.md` (never left checked here).

## 2026-07-31

- [ ] **PERF-01** `RefreshWindows` (~86ms p50 of tmux round-trips) still runs on the loop goroutine, so it queues window switches behind it. Moving it off is a real design change, not just a `go` — its results feed the reconcile, so it is not write-only the way the housekeeping move in `506fbfd` was. pri:P2 · status:todo · ev:cmd/tabby/internal/daemon/loop.go:968,1162
  **Plan (2026-07-31, reviewed).** Framing correction: the "four independent ~6ms subprocess round-trips" reading was wrong. Inside `stateMu` the only unconditional fork was the activeq `display-message`; `syncWindowNames`, `syncWindowIndices` and `computeVisualPositions` contain no exec at all, and `processAIToolStates` forks only on a fallback the pre-lock preload normally prevents. The remaining per-stage spikes are CPU/GC/scheduler noise or option-cache misses, so parallelizing the locked stages buys ~nothing.
  Chosen approach — **fetch/apply pipeline**: split at `rwPreLock`. A goroutine does all pre-lock I/O into a snapshot struct and submits a coalesced apply event; the apply runs *on the loop goroutine* (merge under `stateMu`, deferred writes, then close-restore and hash-compare inline). Preserves single-goroutine mutation and keeps zero staleness for the only two consumers needing fresh state — close-restore (loop.go:1165-1183) and the refresh_tick hash-compare (loop.go:968-975); every other consumer already tolerates last-completed-refresh state. Expected loop occupancy ~86ms → ~15-20ms.
  Rejected: fully-async refresh (merge races `SetActiveWindowOptimistic` → highlight flicker, and deferred `move-window` + focus-restore race user navs — the reverted-nav bug class; ~20ms more for our two worst historical race classes) and stale-consume (close-restore can double-fire `SelectPreviousWindow`).
  Steps: 1. hoist activeq out of `stateMu` — **DONE `0f838de`**, locked_ms p50 18→12, max 54→16. 2. pure refactor to `fetchRefreshSnapshot()` / `applyRefreshSnapshot()`, byte-equivalent for all callers. 3. Active-overlay in apply (`Active := w.ID == l.activeWindowID`) so a snapshot listed before a nav can't stomp the optimistic highlight; active-flip stays out of the structural hash. 4. pipeline `signal_refresh` (TryLock + refetch bit — resubmit via `time.AfterFunc`, never re-Store the drained coalescing flag) and move close-restore + hash-compare into the apply handler. 5. optional: deferred colorArgs/aiToolOps/renames off-loop, keeping pendingMoves + focus-restore synchronous.
  **Off-ramp: after steps 1-2, re-measure. If on-loop p50 is already under ~30ms, stop before step 4 — that is where all the risk lives.** Payoff is bounded to the second-queued-keypress case since first repaint is already optimistic. Verify with `scripts/bench-refresh.sh` per step plus a new `PERF_REFRESH_APPLY apply_ms= snapshot_age_ms=`; success = on-loop p50 <25ms with no growth in `RESTORE_WINDOW_FOCUS` / `ACTIVE_DRIFT_CORRECTED`.
  No single hotspot remains after `43f41af` and `1be8d6d` (merge_ms 121→7, total 220→56); the residual is four independent ~6ms subprocess round-trips, each spiking on its own, and `wait_ms=0` at every percentile rules out lock contention. Perceived first-repaint latency is already fine thanks to the optimistic render plus `go BroadcastRender()` at loop.go:1152-1159, so this is about switch queueing under load, not paint latency. Dependencies to map before changing: `GetWindows` consumers at loop.go 504/541/669/738-740/1244 and the window-close-restore logic at loop.go:1165.

## 2026-07-30

- [ ] **BUG-06** A window in a normal session can carry a stale `@tabby_minimized=1` tag (e.g. a peek whose blur handler never cleared it), and the startup `parkExistingMinimizedWindows()` sweep will park it as if minimized on the next daemon restart, hiding a live window. pri:P1 · status:todo · ev:cmd/tabby/internal/daemon/coordinator.go
  Observed live on 2026-07-30 on window @393; it was swept into `_tabby_minimized` and had to be manually restored. Direction: `parkExistingMinimizedWindows` should require corroborating evidence before parking, or peek/blur must guarantee marker cleanup.

## 2026-07-20

_No open items._
