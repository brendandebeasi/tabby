# Changelog

## [Unreleased]

### Added

- `auto_theme.sync_claude_code` mirrors the light/dark toggle into Claude Code's own theme setting in `~/.claude/settings.json`. Off by default. Preserves the `-ansi` and `-daltonized` variant of whatever theme is already set. Claude Code reloads the file, so running sessions repaint without a restart.

### Fixed

- A session whose name contains `tabby`, `sidebar`, or `renderer` no longer destroys itself on a tmux-resurrect restore. Restored panes get a start command that names the capture file after the session (`cat '…/pane-tabby_session:1.0'; exec -l /bin/bash`), which the pane classifier read as tabby's own infrastructure. Every window then looked empty of real panes and got killed a few seconds after the restore. Classification is now anchored to the program name instead of matching the bare word anywhere in the command line. Thanks to @Corosauce for the diagnosis in #59.

- Nav keys (`M-;` / `M-'`) work in grouped sessions. A session created with `tmux new-session -t <existing>` had no daemon of its own, so every keypress dialed a socket that was never there, hung for the full 1.5s retry budget, and then did nothing. `session-created` and `client-attached` now both ensure a daemon for the session they fire in, via the new `scripts/ensure-daemon.sh`.

### 2026-08-11 — Light and dark themes now switch on a keypress

- The theme no longer follows your OS appearance setting. Pair a light theme with a dark one in `config.yaml` and switch between them with `prefix + T`, or `tabby theme toggle` from a shell. — bd
- New `tabby theme` command: no arguments reports the current selection, `toggle` flips it, and `light`/`dark` select one directly. — bd
- The choice is written back to `config.yaml`, so it survives a daemon restart, and it works the same over SSH without forwarding anything from the machine you connected from. — bd
- Configs that still say `mode: system` or `mode: time` keep loading; both are read as dark until you toggle. — bd
- The sidebar has a theme button between the nav arrows and the resize row, filling what was a blank line. It names the theme it will switch to, and is hidden when no light/dark pair is configured. — bd

### 2026-07-30 — Un-minimize is safer, and a stale reindex bug is fixed

- Un-minimizing a window now clears its markers only after confirming it actually left the holding session, so a failed restore can't strand it invisibly. — bd
- Un-minimize also clears a previously-leaked `@tabby_min_host` marker. — bd
- A new startup sweep rehomes windows stranded in the holding session and adopts ones whose origin session no longer exists. — bd
- Window reindexing now targets its own session explicitly, fixing windows getting dragged into another session when more than one session is live. — bd

### 2026-07-30 — Sidebar width no longer shrinks when a phone attaches

- Switching from a phone (or any narrow client) to the desktop no longer leaves the sidebar stuck at ~10 columns. A narrow client's transiently-clamped sidebar was being mistaken for a deliberate resize and adopted as the global width for every window; a client-size change is now never adopted as your chosen width. — bd

### 2026-07-27 — Focus stays put when a tab regroups after ssh

- After `ssh`-ing into a grouped host from a new tab, the tab no longer loses focus to window 1. The daemon's own park/unlink churn during the regroup could make tmux re-elect the first window; focus is now re-asserted unless you actually switched windows yourself. — bd

### 2026-07-27 — Splitting a remote pane stays on the same host

- Splitting a pane that's SSH'd into a box (`prefix-|`/`-` or the header split buttons) re-runs that connection in the new pane, so the split lands on the same host. Scoped to splits — new tabs/windows are unaffected. `split_inherit_ssh` (default on). — bd

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
