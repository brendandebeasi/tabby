# Roadmap — active backlog

Done items are cut to `roadmap-done.md` (never left checked here).

## 2026-07-30

- [ ] **BUG-06** A window in a normal session can carry a stale `@tabby_minimized=1` tag (e.g. a peek whose blur handler never cleared it), and the startup `parkExistingMinimizedWindows()` sweep will park it as if minimized on the next daemon restart, hiding a live window. pri:P1 · status:todo · ev:cmd/tabby/internal/daemon/coordinator.go
  Observed live on 2026-07-30 on window @393; it was swept into `_tabby_minimized` and had to be manually restored. Direction: `parkExistingMinimizedWindows` should require corroborating evidence before parking, or peek/blur must guarantee marker cleanup.

## 2026-07-20

_No open items._
