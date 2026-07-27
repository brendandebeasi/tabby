# Changelog

## [Unreleased]

### 2026-07-27 — New tab no longer jumps to another window after ctrl-b c

- Removed the `M-[` / `M-]` (Option+[/]) window-nav bindings: they collided with terminal `ESC[`/`ESC]` control-sequence replies, so a fresh shell's startup reply fired prev/next-window and navigated off the new tab. cmd+[/], `M-h`/`M-l`, and prefix `p`/`n` still switch windows. — bd
- Ignore a sidebar window-list click that targets a different window while a just-created tab is still settling, so a phantom mouse press on the new sidebar can't snap focus to window 1. — bd

### 2026-07-21 — Switching windows no longer bounces back

- A window switch that lands mid-spawn no longer gets reverted to the old window.
- The post-spawn active-window restore now yields to a deliberate navigation.

### 2026-07-21 — Detect stale multi-focus clients

- Log when more than one client reports terminal focus (only one should).
- Surfaces the phantom (disconnected-without-focus-out) client behind focus fights.

### 2026-07-21 — Tabs no longer jump groups when you close a window

- Tab/group colors and borders are now applied by window id, not tmux index.
- Closing a window renumbers indexes; the old code repainted the wrong tabs.

### 2026-07-21 — New tab keeps focus instead of jumping to the first window

- Keybinding-opened tabs (prefix-c, M-n) now register with the daemon.
- Focus stays on the new tab after the reorder, with multiple clients attached.

### 2026-07-20 — No more tab focus-cycling after ssh

- Switching the active window no longer counts as a structural change.
- Fixes tabs cycling through every window (to the first) after an ssh, with multiple clients attached.

### 2026-07-20 — Mobile full-screen sidebar tabs are tappable

- Tapping any window tab in the phone full-screen sidebar now switches to it.
- Minimized window tabs are drawn in the visible area and select on tap.
- Tapping a tab closes the full-screen sidebar and reveals that window.

### 2026-07-20 — New tabs inherit the ssh session

- New tab from an ssh/mosh tab re-runs that connection so it lands on the same host.
- Such a tab inherits its parent's group, color, and icon immediately.
- Toggle with `sidebar.new_tab_inherit_ssh` (default on).
