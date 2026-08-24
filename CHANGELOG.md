# Changelog

## [Unreleased]

### Added

- `landing.command` points a new tab at a launcher other than tabby's own. The value is a shell command line typed into the new tab's shell, so `command: eval "$(pounce --target current)"` hands the tab to that launcher and nothing in tabby knows or cares what it is. Still gated on `landing.enabled`, still off by default, and a tab that inherits an ssh connection still goes straight to the host. A value naming something absent costs one "command not found" and leaves a usable prompt.

  The sidebar's `+` now honours all of this too. It shares the decision with the `tabby new-window` spawner instead of restating it, which is also a fix: on any install without `bin/new-window` built, `+` fell back to a path that had never learned about landing, so the launcher appeared on `prefix + c` and not on the button.

- The cat keeps a permanent record. Time in each state, adventures and what came back from them, catches and escapes by species, care and adventure streaks, fastest yarn catch, favourite biome, longest nap, worst neglect — it all accumulates under `lifetime_stats` in `~/.local/state/tabby/pet.json`. Rates and rankings are computed on read rather than stored, so a counter fix never leaves a stale number behind. An older `pet.json` loads unchanged: the counters start at zero and the birthday is backdated to first sight (#25).

- Cats bring you things. A successful pounce on an adventure sometimes gets carried home and left on the ground; click it to accept and the cat is pleased with itself, or it takes the gift back after ten minutes. Three at a time, oldest first out (#28).

- On an adventure the cat now hops over rocks, logs and trees instead of walking through them, and about a third of the time climbs one and sits up there a while before carrying on (#28).

- `auto_theme.sync_claude_code` mirrors the light/dark toggle into Claude Code's own theme setting in `~/.claude/settings.json`. Off by default. Preserves the `-ansi` and `-daltonized` variant of whatever theme is already set. Claude Code reloads the file, so running sessions repaint without a restart.

### Changed

- The two AI attention indicators now mean different things and clear differently. The diamond is a notification: an AI pane that finished working while you were on another window raises it, and switching to the window clears it. The `?` is an unanswered question, so it survives you reading it and clears when the tool starts working again — which is what happens when you reply. Deciding between them needs to know whether the tool actually asked anything, so when a pane stops working the daemon reads its last 40 rows once and looks for question phrasing, a boxed list of numbered choices, or a closing line that ends in `?`.

  Before this, `?` appeared on any AI pane that was merely sitting at its idle glyph and was dismissed by looking at the window, which is backwards on both counts: a Claude Code tab parked with nothing to do carried a permanent `?`, and a real question was cleared by switching to the window without answering it. The diamond, meanwhile, only ever meant the tool had exited, and the in-memory expiry re-asserted it every poll — so visiting the window unset the tmux option and the diamond came straight back on the next cycle.

- Whether an AI pane is working is now read from the pane, not just its title and its hooks. Claude Code prints a status line above its input box with a counter that ticks while a turn runs (`✽ Symbioting… (27s · ↓ 1.2k tokens)`) and stops when it ends (`✻ Brewed for 2m 29s`), and that counter now outranks every other signal. It has to: a tool that sets its own pane title sets it to something durable, so a pane titled `✳ fix-manifest-archive-scheme` says exactly that whether it is mid-turn or has been sitting idle for an hour. With nothing to contradict them, the hooks were the only vote — one missed `Stop` and a window kept its diamond for the rest of the session, and a hook-set `?` could sit on a pane that had long since been answered and gone back to work. The reading is cached for a second, so a burst of reconciles still costs one `capture-pane` per pane per second.

### Fixed

- A tmux server running more than one tabby session stops fighting over window sizes. Every daemon enumerated windows with `list-windows -a`, which spans the whole server, and then locked all of them to its own client's geometry — so a session attached to a phone reflowed the windows of every unrelated session on the same server, and each of those sessions' daemons promptly reflowed them back. Window listing is now scoped to the daemon's own session, which under a grouped family still covers every window it shares.

- The daemon no longer resizes a window that is already the right size. `resize-window` fires `after-resize-pane` and `after-resize-window` whether or not anything moved, those hooks signal the daemon, and the next reconcile re-issued the same resize — a permanent storm of one resize plus one hook per window per cycle, with nothing on screen to explain it. Each of those cycles reflows every window and re-pins the client's focus, so on a phone the button bar came and went and focus moved on its own. The resize planner now skips windows already at the target geometry.

- Killing a tmux session retires its daemon and supervisor instead of leaving them to thrash. The daemon only noticed a missing session on its ten-second idle tick, and the watchdog restarted it up to five times after that — a minute of a daemon reconciling against a session that was not there. The daemon now checks at startup and stops cleanly, and the watchdog stops rather than restarting when the session is gone.

- Hooks stop printing `... returned 1` into every attached client. tmux reports the exit status of a hook body as a message to every client on the session, and a hook fired with no current client has commands that cannot succeed: `refresh-client -S` needs one to refresh. The status of a hook body carries no information anyone acts on, since the work it triggers is a signal to the daemon and the daemon reports its own failures, so every hook now ends in a command that succeeds and the body's own status is left to the scripts it calls.

- Opening a new window no longer bounces you back to the one you came from. The daemon brackets a round of pane surgery by noting which window was current before and restoring it after, so that spawning a header or a sidebar somewhere else does not steal focus. A window created during that bracket is made current by tmux the moment it exists, well before the daemon's cached window list or its own new-window flow have caught up, so the restore read it as focus drifting and undid it. The bracket now adopts the current window when it did not exist when the bracket opened, or when a new-window request is still in flight.

- The phone button bar stops flickering, and focus stops jumping around with it, in a grouped session family. `tmux list-panes -a` walks sessions, and every session in a group links the same windows, so a five-session group reports each pane five times over. The duplicate-header sweep read those repeats as five headers in one window and killed four of them — all the same, only, real pane — leaving the window bare until the next tick spawned a replacement, about a hundred and forty times a minute. Every one of those splits and kills reflows the window and re-pins the client's focus, which is what made focus wander on its own. The header and pane-header sweeps now count each pane once.

  Two related hazards go with it, since several daemons attend the same shared windows: a bar whose owning session and daemon are both still running is now left for that peer to reap rather than killed on sight, the oldest pane wins a genuine duplicate so every peer picks the same survivor instead of each killing a different one, and a daemon whose own client is on a desktop profile no longer tears down a peer's bar while that peer is still serving a phone.

- The layout audit no longer asks for a full refresh twice a second when `pane_header.native` is on. Native mode labels panes through tmux's own border format and deliberately never spawns the overlay header panes, but the audit still counted one missing per content pane and requested the relayout that would never produce them. It now skips that check in native mode.

- A window too short to fit the button bar no longer asks for a full layout refresh every five seconds forever. The spawner declines to split a content pane with fewer rows to spare than the bar needs, but the audit that noticed the missing bar kept requesting the refresh that would never produce one — so a single cramped window drove continuous relayouts across the session. The audit now reports the window as too short and leaves it until it grows.

- A window no longer gets declared finished ten seconds into a long turn. The daemon cross-checks a hook that says "busy" against the pane title, and clears the hook as stale when the title shows no spinner — but a title set by the tool itself never shows one, so every turn on such a pane was called stale, which manufactured a finished-working bell while the tool was still going. The cross-check now reads the pane's progress line before overriding a hook.

- An answered question no longer keeps its `?`. Question detection scanned the whole visible tail of the pane, which holds around forty rows and so usually still shows the last question along with the reply that settled it. The search now starts below the last prompt the user typed into and sent.

- Claude Code's spinner is recognized again, so its panes show activity indicators. Current builds cycle through the half-filled circles ◐◓◑◒ rather than the braille dots tabby was matching, so a working pane read as doing nothing. The knock-on was worse than a missing spinner: both attention indicators are raised when a pane stops working, so a pane that never registered as busy never flagged for input or completion either. Both glyph families are now matched.

- AI tools running over SSH now get activity indicators. The local pane command for a remote session is just `ssh`, so nothing identified the tool inside it and the whole per-pane AI detection pass was skipped — even though the remote tool's spinner and idle glyphs arrive in the pane title like any other. A remote pane whose title carries one is now treated as an AI pane. Nothing needs installing on the remote host.

- Dragging the sidebar border resizes it again when a second terminal is attached. With two clients of different widths on one session, tmux reflows the window to whichever acted last, so the daemon could not tell a drag from a reflow and refused to adopt the new width — while still snapping the pane back to the old one, which read as the drag doing nothing. An unattributable width is now held and adopted once a second pass measures the same width in the same window, and the window you are dragging is left alone until then.

- Reattaching to a session now brings its daemon back. The daemon exits once you detach, and nothing restarted it on the way back in: the reattach check saw a sidebar pane in the window and stopped there, without noticing the pane belonged to a grouped peer session's daemon rather than this one. The session was left with no daemon at all, so the sidebar sat on "Loading..." and every window-navigation key failed against a socket that no longer existed. The check now only counts a renderer this session owns.

- A shared window no longer keeps a sidebar stuck on "Loading...". Grouped tmux sessions share their panes, and only one daemon wins the renderer in each shared window; the losing daemon reaches its idle timeout and exits while its renderer keeps running against a dead socket. The surviving daemon spared that pane because the peer session still existed, so nothing ever replaced it. The peer's daemon socket now has to answer, not just its session exist.

- An unused session no longer respawns its daemon forever. When the last client leaves, the daemon idles out after 30 seconds and signals itself, but that signal was indistinguishable from an external kill, so the watchdog restarted it, it found no clients, and quit again roughly every 50 seconds. Because each life outlasted the restart-count window, the attempt counter reset every time and the watchdog never reached its give-up threshold. The idle path now writes the clean-stop sentinel itself; a daemon killed from outside is still respawned.

- Two sidebars stop fighting over the same panes. A grouped tmux session shares its windows with its peers, so each session's daemon saw the other's renderer panes as orphans from a dead daemon, killed them, and respawned its own — endlessly, which showed up as the sidebar redrawing and focus jumping between windows. A renderer belonging to another session is now only killed once tmux confirms that session is gone; the daemon left without clients idles out on its own.

- The cat stays home when there is something to do. It would head off on an adventure the moment its animation state read `idle`, which happens a frame after you drop food or throw yarn, so a toy could land and the cat would walk away from it. It now waits for an empty yard — no food, no yarn, no mouse, no poop, no unanswered question — and a few seconds of actual idling before it goes (#28).

- `adventure_chance` does something. The key was in the config schema and the documentation but nothing ever read it. It is now the percentage gate on the urge to wander; unset or `0` means 100, which is what the cat has always done (#28).

- The cat is no longer painted over by its own scenery. In both the home scene and the adventure play area the dragon, the yarn, the food and the falling debris were placed after the cat and overwrote it, so the pet you are looking at could vanish behind a passing bird. The cat is drawn last now (#28).

- Crash reports carry the crash. The stack trace goes to the daemon's stderr log, which the report never attached; the events log was read from a path with a dot where the filename has a hyphen, so that section was silently missing from every report ever filed; and the pre-crash forensics quoted whichever `.prev` a size rotation had left lying around, days old, instead of the events the dying daemon had just written. A daemon that restarted five times in six seconds produced five copies of the same stale block and nothing about the panic. The `crash` and `auto-triage` labels are also created if absent, which is what the duplicate-suppression query filters on: without them each crash opened its own issue (#47, #52, #60).

- A session whose name contains `tabby`, `sidebar`, or `renderer` no longer destroys itself on a tmux-resurrect restore. Restored panes get a start command that names the capture file after the session (`cat '…/pane-tabby_session:1.0'; exec -l /bin/bash`), which the pane classifier read as tabby's own infrastructure. Every window then looked empty of real panes and got killed a few seconds after the restore. Classification is now anchored to the program name instead of matching the bare word anywhere in the command line. Thanks to @Corosauce for the diagnosis in #59.

- The sidebar context menu follows the theme. Its text was drawn in a hard-coded `#000000` and its highlight in `#2563eb`, so on rose-pine, catppuccin-mocha, dracula, nord, or any other dark preset the menu was black on near-black. The daemon now sends the resolved palette to the renderer. Where a theme leaves a colour unset the menu inherits the terminal's foreground rather than guessing a literal. Fix contributed by @logicalor in #54, for #53.

- `sidebar.header.height: 0` now actually removes the banner, as do `text: ""` and `padding_bottom: 0`. The loader treated a zero value as an absent key and put `TABBY` / `3` / `1` straight back, so there was no way to turn the header off. These three keys are pointers now, matching `centered`/`active_color`/`bold`, and the defaults resolve at render time. Reported in #58.

- A failed first build no longer leaves a silently dead plugin. `tabby.tmux` checked only for `bin/render-status` before deciding the binaries were present, then discarded the build's exit status, so a machine without a Go toolchain got a plugin that loaded and did nothing. It now checks `bin/tabby` too and reports the failure through `tmux display-message`, with the build log at `/tmp/tabby-install.log`. Reported in #55.

- `make build-linux` says what it is for. It cross-compiles into `bin-linux/` for copying to a remote host; on Linux you still want `make build`, which writes the `bin/` that the plugin loads.

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
