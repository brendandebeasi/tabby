#!/usr/bin/env bash
# End-to-end test for the pane dashboard feature. Runs entirely against a
# dedicated tmux server (-L tabbytest) so it never touches the user's real
# tmux/tabby instance. Intended to run inside the OrbStack dev machine where
# the tabby plugin is assembled at ~/.tmux/plugins/tabby.
#
# Flow: boot tabby -> create 3 windows x 2-3 content panes -> `tabby dashboard`
# (gather) -> assert all content panes tiled in one dashboard window with no
# chrome -> `tabby dashboard` (restore) -> assert panes back in windows.
set -u

SOCK=tabbytest
PLUG="${PLUG:-$HOME/.tmux/plugins/tabby}"
TMUXB="tmux -L $SOCK"
BIN="$PLUG/bin/tabby"

pass=0; fail=0
ok()   { echo "  PASS: $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL: $1"; fail=$((fail+1)); }

cleanup() {
  pkill -f "tabby watchdog" 2>/dev/null || true
  pkill -f "tabby daemon"   2>/dev/null || true
  $TMUXB kill-server 2>/dev/null || true
  rm -f /tmp/tabby-daemon-*.sock /tmp/tabby-daemon-*.pid /tmp/tabby-daemon-*.watchdog.pid /tmp/tabby-daemon-*.clean-stop 2>/dev/null || true
}
trap cleanup EXIT

# is_aux <current_cmd> <start_cmd> : 0 (true) if pane is tabby chrome
is_aux() {
  local cur="$1" start="$2" combo
  combo=$(printf '%s|%s' "$cur" "$start" | tr 'A-Z' 'a-z')
  case "$combo" in
    *sidebar*|*renderer*|*pane-header*|*window-header*) return 0 ;;
  esac
  return 1
}

# dash_content_count <window_id> : number of non-aux (content) panes in a window
dash_content_count() {
  $TMUXB list-panes -t "$1" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' 2>/dev/null \
  | while IFS='|' read -r pid cur start; do is_aux "$cur" "$start" || echo x; done | wc -l | tr -d ' '
}

# content_panes : print "<pane_id>\t<window_id>" for every non-aux pane in session
content_panes() {
  $TMUXB list-panes -s -F '#{pane_id}|#{window_id}|#{pane_current_command}|#{pane_start_command}' 2>/dev/null \
  | while IFS='|' read -r pid wid cur start; do
      if ! is_aux "$cur" "$start"; then printf '%s\t%s\n' "$pid" "$wid"; fi
    done
}

# ── boot ──────────────────────────────────────────────────────────────────
cleanup
$TMUXB new-session -d -s main -x 220 -y 60
# Boot tabby in the BACKGROUND (-b): a foreground run-shell would block forever
# waiting for the daemon/watchdog (spawned by the plugin) to close the inherited
# stdout pipe. -b matches how tmux.conf normally sources the plugin via `run -b`.
$TMUXB run-shell -b "$PLUG/tabby.tmux" >/dev/null 2>&1
# Wait for the daemon socket instead of a fixed sleep.
for _ in $(seq 1 50); do
  ls /tmp/tabby-daemon-*.sock >/dev/null 2>&1 && break
  sleep 0.2
done
sleep 1

# Build a workspace: window 1 (2 content panes), window 2 (3), window 3 (1).
W1=$($TMUXB display-message -p -t main "#{window_id}")
$TMUXB split-window -d -h -t "$W1"                       # W1 -> 2 content panes
W2=$($TMUXB new-window -P -F '#{window_id}')
$TMUXB split-window -d -v -t "$W2"
$TMUXB split-window -d -v -t "$W2"                       # W2 -> 3 content panes
W3=$($TMUXB new-window -P -F '#{window_id}')             # W3 -> 1 content pane
sleep 2   # let the daemon spawn chrome for the new windows

# Snapshot the content panes (pane IDs are stable across join-pane).
mapfile -t BEFORE < <(content_panes | sort)
NBEFORE=${#BEFORE[@]}
NWIN_BEFORE=$($TMUXB list-windows -F '#{window_id}|#{window_name}' | grep -vc '_tabby_stash_')
echo "Before: $NBEFORE content panes across $NWIN_BEFORE windows"
[ "$NBEFORE" -ge 6 ] && ok "created >=6 content panes" || bad "expected >=6 content panes, got $NBEFORE"

# ── enter dashboard ─────────────────────────────────────────────────────────
$TMUXB run-shell "$BIN dashboard" >/dev/null 2>&1
# Sidebar spawn for the dashboard runs on the window-check tick (~3s), so give
# it a full tick + margin before checking. (2s was racy.)
sleep 5

DASH=$($TMUXB list-windows -F '#{window_id}|#{@tabby_dashboard}' | awk -F'|' '$2=="1"{print $1}')
if [ -n "$DASH" ]; then ok "dashboard window exists ($DASH)"; else bad "no @tabby_dashboard window"; fi

# All BEFORE content panes should now live in the dashboard window.
in_dash=0; misplaced=0
for row in "${BEFORE[@]}"; do
  pid=${row%%$'\t'*}
  loc=$($TMUXB display-message -p -t "$pid" "#{window_id}" 2>/dev/null)
  if [ "$loc" = "$DASH" ]; then in_dash=$((in_dash+1)); else misplaced=$((misplaced+1)); fi
done
[ "$in_dash" -eq "$NBEFORE" ] && ok "all $NBEFORE content panes gathered into dashboard" \
  || bad "only $in_dash/$NBEFORE panes in dashboard ($misplaced elsewhere)"

# Dashboard view is detached: no sidebar, no overlay headers — just the tiled grid.
auxcount=$($TMUXB list-panes -t "$DASH" -F '#{pane_current_command}|#{pane_start_command}' \
  | while IFS='|' read -r c s; do is_aux "$c" "$s" && echo x; done | wc -l | tr -d ' ')
[ "$auxcount" -eq 0 ] && ok "dashboard is detached (no sidebar/aux panes)" || bad "dashboard has $auxcount aux panes (expected 0)"
hdrs=$($TMUXB list-panes -t "$DASH" -F '#{pane_current_command}|#{pane_start_command}' \
  | grep -cE 'pane-header|window-header')
[ "$hdrs" -eq 0 ] && ok "no overlay header panes in dashboard" || bad "$hdrs overlay header panes present (should be 0)"
# Tiles are labelled via native pane-border-status (set pane-local=top on each
# content tile so it overrides tabby's global 'off'), with the hidden pane-local
# style cleared so the label inherits the visible global border color.
cpane=$($TMUXB list-panes -t "$DASH" -F '#{pane_id}|#{pane_current_command}|#{pane_start_command}' \
  | awk -F'|' 'tolower($2"|"$3) !~ /sidebar|renderer|pane-header|window-header/ {print $1; exit}')
cpbs=$($TMUXB show-options -pqv -t "$cpane" pane-border-status)
[ "$cpbs" = "top" ] && ok "content tile pane-border-status=top ($cpane)" || bad "tile pane-border-status='$cpbs' (expected top)"
cstyle=$($TMUXB show-options -pqv -t "$cpane" pane-border-style)
# Pane-local should be cleared (empty) — the window-level active/inactive styles
# (set in applyDashboardBorders, matching tabby's regular pane-header colors)
# then take effect.
if [ -z "$cstyle" ]; then
  ok "tile pane-local style cleared (inherits window-level)"
else
  case "$cstyle" in
    *fg=#*,bg=#*) bg=${cstyle##*bg=}; fg=${cstyle#fg=}; fg=${fg%%,*}; \
                  if [ "$bg" = "$fg" ]; then bad "tile style hidden (fg=bg): $cstyle"; else ok "tile pane-local style visible: $cstyle"; fi ;;
    fg=*)         ok "tile pane-local style visible: $cstyle" ;;
    *)            bad "tile pane-local style unexpected: '$cstyle'" ;;
  esac
fi
# Window-level active border style should be set (dark-blue bg + white text).
wactive=$($TMUXB show-options -wqv -t "$DASH" pane-active-border-style)
[ -n "$wactive" ] && ok "dashboard pane-active-border-style set ($wactive)" || bad "no pane-active-border-style on dashboard"

# It must hold exactly NBEFORE *content* panes (all gathered).
dcontent=$(dash_content_count "$DASH")
[ "$dcontent" -eq "$NBEFORE" ] && ok "dashboard holds all $NBEFORE content panes" || bad "dashboard holds $dcontent content panes, expected $NBEFORE"

# Origin windows are destroyed while gathered: only the dashboard remains.
realwins=$($TMUXB list-windows -F '#{window_id}|#{window_name}' | grep -vc '_tabby_stash_')
[ "$realwins" -eq 1 ] && ok "origin windows collapsed to dashboard only" || bad "expected 1 real window, found $realwins"

# Soak through a reconcile / window-check tick (8s): chrome must stay present and
# the content-pane count stable (no panes lost or duplicated by reconcile).
sleep 10
soak_aux=$($TMUXB list-panes -t "$DASH" -F '#{pane_current_command}|#{pane_start_command}' \
  | while IFS='|' read -r c s; do is_aux "$c" "$s" && echo x; done | wc -l | tr -d ' ')
soak_content=$(dash_content_count "$DASH")
{ [ "$soak_aux" -eq 0 ] && [ "$soak_content" -eq "$NBEFORE" ]; } \
  && ok "dashboard stable after reconcile (detached, $soak_content content panes, 0 aux)" \
  || bad "after reconcile: aux=$soak_aux content=$soak_content (expected aux=0 content=$NBEFORE)"

# ── in-dashboard nav: [/] cycles the tiles (panes), does NOT exit ───────────
beforeP=$($TMUXB display-message -p -t "$DASH" '#{pane_id}')
$TMUXB run-shell "$BIN hook next-window" >/dev/null 2>&1
sleep 2
stillDash=$($TMUXB list-windows -F '#{window_id}|#{@tabby_dashboard}' | awk -F'|' '$2=="1"{print $1}')
[ -n "$stillDash" ] && ok "nav: dashboard stays open on [/]" || bad "nav: [/] exited the dashboard (should cycle tiles)"
afterP=$($TMUXB display-message -p -t "$DASH" '#{pane_id}')
[ "$afterP" != "$beforeP" ] && ok "nav: [/] moved focus to another tile ($beforeP -> $afterP)" || bad "nav: active tile did not change ($beforeP)"

# cmd+opt+~ (cycle-pane) also cycles tiles in the dashboard, without dimming.
$TMUXB run-shell "$BIN cycle-pane" >/dev/null 2>&1
sleep 1
swapP=$($TMUXB display-message -p -t "$DASH" '#{pane_id}')
[ "$swapP" != "$afterP" ] && ok "swap (~): cycle-pane moved focus in dashboard ($afterP -> $swapP)" || bad "swap (~): active tile did not change ($afterP)"
stillD=$($TMUXB list-windows -F '#{window_id}|#{@tabby_dashboard}' | awk -F'|' '$2=="1"{print $1}')
[ -n "$stillD" ] && ok "swap (~): dashboard stayed open" || bad "swap (~): dashboard closed unexpectedly"

# ── exit via the toggle command ─────────────────────────────────────────────
$TMUXB run-shell "$BIN dashboard" >/dev/null 2>&1
sleep 2
GONE=$($TMUXB list-windows -F '#{window_id}|#{@tabby_dashboard}' | awk -F'|' '$2=="1"{print $1}')
[ -z "$GONE" ] && ok "dashboard window removed on exit" || bad "dashboard still present after exit ($GONE)"
alive=0
for row in "${BEFORE[@]}"; do
  pid=${row%%$'\t'*}
  $TMUXB display-message -p -t "$pid" "#{pane_id}" >/dev/null 2>&1 && alive=$((alive+1))
done
[ "$alive" -eq "$NBEFORE" ] && ok "all $NBEFORE content panes survived round trip" || bad "only $alive/$NBEFORE panes alive after restore"
NWIN_AFTER=$($TMUXB list-windows -F '#{window_id}|#{window_name}' | grep -vc '_tabby_stash_')
echo "After: $alive content panes across $NWIN_AFTER windows"
[ "$NWIN_AFTER" -ge "$NWIN_BEFORE" ] && ok "origin windows recreated ($NWIN_AFTER >= $NWIN_BEFORE)" || bad "expected >= $NWIN_BEFORE windows, found $NWIN_AFTER"

# ── result ──────────────────────────────────────────────────────────────────
echo "-----------------------------------------"
echo "PASS=$pass FAIL=$fail"
[ "$fail" -eq 0 ]
