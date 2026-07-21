# Roadmap — completed

Finished items, cut from `roadmap.md` and pasted here under a dated heading (newest first).

## 2026-07-21

- [x] **BUG-04** Switching windows sometimes bounced straight back to the previous window (multi-client). pri:P1 · status:done · ev:cmd/tabby/internal/daemon/loop.go:629
  done: 2026-07-21 — `doPaneLayoutOps` snapshots the current window at spawn-bracket start and "restores" it at bracket end to undo silent kill-pane/split-window flips; a deliberate user nav landing mid-bracket was reverted as collateral. Now it only reverts when the post-bracket window is NOT what the coordinator already intends (`coordinatorActiveWindowID`), so a real nav is adopted, not undone. Root cause via Fable; symptom also amplified by a phantom focused client (see BUG-05).

- [x] **BUG-05** Multiple clients reported tmux `focused` at once (stale phantom), driving focus fights. pri:P2 · status:done · ev:pkg/daemon/activeclient.go
  done: 2026-07-21 — a client that disconnects without sending focus-out keeps a stale `focused` flag. The elector now logs `MULTI_FOCUS_DETECTED` (rate-limited) with each focused client's idle age and the elected vs stale role, so the phantom is diagnosable. Unit-tested.

- [x] **BUG-02** Tabs jumped to different groups when another window was closed. pri:P1 · status:done · ev:cmd/tabby/internal/daemon/coordinator.go:5122
  done: 2026-07-21 — `buildPaneHeaderColorArgs` painted tab/group color + borders by tmux INDEX on every refresh; closing a window renumbers indexes so the group theme landed on the wrong window. Now targets `@window_id` (stable across renumber), with an empty-id skip.

- [x] **BUG-03** New tab (keybinding: prefix-c / M-n) sometimes cycled focus to the first window with multiple clients attached. pri:P1 · status:done · ev:cmd/tabby/internal/newwindow/newwindow.go · cmd/tabby/internal/daemon/loop.go
  done: 2026-07-21 — `bin/tabby new-window` runs out-of-process and never registered a pending status, so the post-add move-window renumber dropped tmux's active marker and the ready-gated focus re-assert was skipped (fallback election → first window). It now sends `new-window-pending`/`new-window-ready` over the MsgHook socket → SetNewWindowInFlight/Ready, so the existing per-client restore re-selects the new tab.

## 2026-07-20

- [x] **BUG-01** Tab focus cycled through every window after an ssh (multi-client sessions). pri:P1 · status:done · ev:cmd/tabby/internal/daemon/loop.go · cmd/tabby/internal/daemon/coordinator.go
  done: 2026-07-20 — split a structural windows-hash (window set/panes/group/color/collapsed) from the active-sensitive full hash; an active-window flip no longer reads as structural, so it stops firing the resize-all-windows lock that kicked tmux's multi-client elector into a self-sustaining cascade. Also fixed the `remote_hosts` glob so bare `client-gunpowder` groups as Gunpowder not StudioDome.

- [x] **FEAT-01** New tab opened from an ssh tab re-runs the same connection and inherits the parent's group/color/icon. pri:P2 · status:done · ev:cmd/tabby/internal/newwindow/newwindow.go
  done: 2026-07-20 — spawner detects the firing pane's ssh/mosh argv and re-runs it; daemon propagates the remote parent's appearance; gated by `sidebar.new_tab_inherit_ssh`.
