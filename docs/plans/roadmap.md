# Roadmap — active backlog

Done items are cut to `roadmap-done.md` (never left checked here).

## 2026-07-31

- [ ] **PERF-01** `RefreshWindows` (~86ms p50 of tmux round-trips) still runs on the loop goroutine, so it queues window switches behind it. Moving it off is a real design change, not just a `go` — its results feed the reconcile, so it is not write-only the way the housekeeping move in `506fbfd` was. pri:P2 · status:todo · ev:cmd/tabby/internal/daemon/loop.go:968,1162
  No single hotspot remains after `43f41af` and `1be8d6d` (merge_ms 121→7, total 220→56); the residual is four independent ~6ms subprocess round-trips, each spiking on its own, and `wait_ms=0` at every percentile rules out lock contention. Perceived first-repaint latency is already fine thanks to the optimistic render plus `go BroadcastRender()` at loop.go:1152-1159, so this is about switch queueing under load, not paint latency. Dependencies to map before changing: `GetWindows` consumers at loop.go 504/541/669/738-740/1244 and the window-close-restore logic at loop.go:1165.

## 2026-07-30

- [ ] **BUG-06** A window in a normal session can carry a stale `@tabby_minimized=1` tag (e.g. a peek whose blur handler never cleared it), and the startup `parkExistingMinimizedWindows()` sweep will park it as if minimized on the next daemon restart, hiding a live window. pri:P1 · status:todo · ev:cmd/tabby/internal/daemon/coordinator.go
  Observed live on 2026-07-30 on window @393; it was swept into `_tabby_minimized` and had to be manually restored. Direction: `parkExistingMinimizedWindows` should require corroborating evidence before parking, or peek/blur must guarantee marker cleanup.

## 2026-07-20

_No open items._
