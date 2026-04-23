#!/usr/bin/env bash
# Isolated tabby test harness for asciinema demos.
#
# Creates a tmux server on a private socket and a tabby daemon with a unique
# TABBY_RUNTIME_PREFIX so nothing collides with the user's live session or
# daemon. Designed to be sourced by scenario scripts.
#
# Env variables exported after ss_setup():
#   SS_SOCKET       -- tmux -L socket name for the isolated server
#   SS_PREFIX       -- TABBY_RUNTIME_PREFIX value used for this run
#   SS_SESSION      -- tmux session name (always "demo")
#   SS_SESSION_ID   -- tmux-assigned session_id (e.g. "$0")
#   SS_DAEMON_PID   -- PID of the daemon we started
#   SS_SHIM_DIR     -- dir of the tmux-wrapper shim used by the daemon
#   SS_LOG_DIR      -- where per-run logs land

set -euo pipefail

TABBY_REPO="${TABBY_REPO:-/Users/b/git/tabby}"
TABBY_BIN="${TABBY_BIN:-$TABBY_REPO/bin/tabby}"

# ── private state for cleanup ────────────────────────────────────────────
SS_SOCKET=""
SS_PREFIX=""
SS_SESSION="demo"
SS_SESSION_ID=""
SS_DAEMON_PID=""
SS_SHIM_DIR=""
SS_LOG_DIR=""
SS_TMUX_CONF=""

ss_tmux() {
    command tmux -L "$SS_SOCKET" -f "$SS_TMUX_CONF" "$@"
}

ss_log() { echo "[harness] $*" >&2; }

ss_cleanup() {
    local ec=$?
    ss_log "cleanup (exit=$ec)"
    if [ -n "${SS_DAEMON_PID:-}" ]; then
        kill "$SS_DAEMON_PID" 2>/dev/null || true
        wait "$SS_DAEMON_PID" 2>/dev/null || true
    fi
    if [ -n "${SS_SOCKET:-}" ]; then
        command tmux -L "$SS_SOCKET" kill-server 2>/dev/null || true
    fi
    if [ -n "${SS_PREFIX:-}" ]; then
        rm -f /tmp/${SS_PREFIX}tabby-daemon-*.sock \
              /tmp/${SS_PREFIX}tabby-daemon-*.pid \
              /tmp/${SS_PREFIX}tabby-daemon-*.heartbeat \
              /tmp/${SS_PREFIX}tabby-daemon-*.watchdog.pid 2>/dev/null || true
    fi
    if [ -n "${SS_SHIM_DIR:-}" ] && [ -d "$SS_SHIM_DIR" ]; then
        rm -rf "$SS_SHIM_DIR"
    fi
    return 0
}

# ss_setup <cols> <rows> [label]
#
# Spin up an isolated tmux server at the given size, start a tabby daemon
# pointed at it, and populate a session with some fake windows. The scenario
# can then drive interactions via ss_tmux send-keys.
ss_setup() {
    local cols="${1:-200}"
    local rows="${2:-50}"
    local label="${3:-demo}"

    local nonce="ss-$$-$(date +%s%N | tail -c7)"
    SS_SOCKET="tabbyss-${nonce}"
    SS_PREFIX="${nonce}-"
    SS_SHIM_DIR="$(mktemp -d "/tmp/${SS_PREFIX}shim.XXXXXX")"
    SS_LOG_DIR="$(mktemp -d "/tmp/${SS_PREFIX}logs.XXXXXX")"
    SS_TMUX_CONF="$SS_LOG_DIR/tmux.conf"
    SS_CONFIG_DIR="$SS_LOG_DIR/tabby-config"

    export TABBY_RUNTIME_PREFIX="$SS_PREFIX"
    export TABBY_CONFIG_DIR="$SS_CONFIG_DIR"
    export TABBY_STATE_DIR="$SS_LOG_DIR/tabby-state"
    mkdir -p "$SS_CONFIG_DIR" "$TABBY_STATE_DIR"

    # Fixed demo config — reproducible regardless of the user's live config.
    # Groups: StudioDome (SD|*, rose-pink), Gunpowder (GP|*, iris-purple),
    # Default (everything else, teal).
    cat > "$SS_CONFIG_DIR/config.yaml" <<'EOF'
sidebar:
  theme: rose-pine-dawn
  new_tab_button: false
  new_group_button: false
  close_button: false
  sort_by: group
  line_height: 0
  width_desktop: 25
  width_tablet: 20
  width_mobile: 15
  mobile_max_window_cols: 110
  tablet_max_window_cols: 170
  pane_headers: true
groups:
  - name: StudioDome
    pattern: '^SD\|'
    theme:
      bg: '#b4637a'
      active_bg: '#9d4e6a'
      icon: 🎬
  - name: Gunpowder
    pattern: '^GP\|'
    theme:
      bg: '#907aa9'
      active_bg: '#7a6593'
      icon: 💥
  - name: Default
    pattern: '.*'
    theme:
      bg: '#56949f'
      active_bg: '#286983'
      icon: •
EOF

    # Minimal tmux config applied from server startup so window names aren't
    # rewritten by pane_current_command heuristics.
    cat > "$SS_TMUX_CONF" <<'EOF'
set-option -g allow-rename off
set-option -g automatic-rename off
set-window-option -g allow-rename off
set-window-option -g automatic-rename off
EOF

    # PATH shim: the daemon shells out to `tmux ...` for its own queries. We
    # need those to hit OUR isolated server, not the user's. The shim forces
    # -L $SS_SOCKET on every invocation.
    local real_tmux
    real_tmux="$(command -v tmux)"
    cat > "$SS_SHIM_DIR/tmux" <<EOF
#!/usr/bin/env bash
exec "$real_tmux" -L "$SS_SOCKET" -f "$SS_TMUX_CONF" "\$@"
EOF
    chmod +x "$SS_SHIM_DIR/tmux"

    ss_log "setup cols=${cols} rows=${rows} prefix=${SS_PREFIX} socket=${SS_SOCKET}"

    # Fresh tmux server, session at target size. Use a long-sleep command so
    # the pane stays alive without needing a tty / interactive shell.
    ss_tmux new-session -d -s "$SS_SESSION" -x "$cols" -y "$rows" \
        -n "SD|app" 'sleep 9999'
    # Lock the initial window's name so the daemon's syncWindowNames doesn't
    # rewrite it to the pane CWD basename ("tabby").
    ss_tmux set-window-option -t "$SS_SESSION:0" @tabby_name_locked 1

    SS_SESSION_ID="$(ss_tmux display-message -p '#{session_id}')"
    ss_log "session_id=${SS_SESSION_ID}"

    # Enable tabby sidebar mode (what the sidebar renderer reads to decide it
    # should activate). Matches the @tabby_sidebar option tabby.tmux sets.
    ss_tmux set-option -g @tabby_sidebar enabled

    # Launch daemon. PATH shim routes its tmux subprocesses to our socket;
    # TABBY_RUNTIME_PREFIX isolates its socket/pid path;
    # TABBY_CONFIG_DIR / TABBY_STATE_DIR isolate config + state.
    (
        PATH="$SS_SHIM_DIR:$PATH" \
        TABBY_RUNTIME_PREFIX="$SS_PREFIX" \
        TABBY_CONFIG_DIR="$SS_CONFIG_DIR" \
        TABBY_STATE_DIR="$TABBY_STATE_DIR" \
        "$TABBY_BIN" daemon -session "$SS_SESSION_ID" \
            > "$SS_LOG_DIR/daemon.log" 2>&1
    ) &
    SS_DAEMON_PID=$!

    # Wait for socket to exist. Session IDs include a literal "$" (e.g. "$0")
    # and the daemon uses that verbatim in the filename.
    local sock="/tmp/${SS_PREFIX}tabby-daemon-${SS_SESSION_ID}.sock"
    local i
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
        if [ -S "$sock" ]; then break; fi
        sleep 0.2
    done
    if [ ! -S "$sock" ]; then
        ss_log "ERROR: daemon socket never appeared at $sock"
        tail -20 "$SS_LOG_DIR/daemon.log" >&2 || true
        return 1
    fi
    ss_log "daemon up (pid=$SS_DAEMON_PID, socket=$sock)"
}

# ss_add_windows <name>...
# Create extra windows with given names. Each window runs `sleep 9999` so the
# pane stays alive without needing interactive shell input. Every window gets
# @tabby_name_locked so the daemon's syncWindowNames doesn't rewrite its name
# based on pane CWD.
ss_add_windows() {
    local name
    for name in "$@"; do
        ss_tmux new-window -t "$SS_SESSION:" -n "$name" 'sleep 9999'
        ss_tmux set-window-option -t "$SS_SESSION:" @tabby_name_locked 1
    done
}

# ss_poke_daemon — force the daemon to refresh its window list now rather than
# waiting for its next polling tick. Use after ss_add_windows, toggling options,
# etc. so the sidebar reflects the new state promptly.
ss_poke_daemon() {
    kill -USR1 "$SS_DAEMON_PID" 2>/dev/null || true
    sleep 0.5
}

# ss_set_group <window_index> <group_name>
# Tag a window so it renders under the given group in the sidebar. Uses the
# same @tabby_group option the real UI sets via its context menu.
ss_set_group() {
    local idx="$1" name="$2"
    ss_tmux set-window-option -t "$SS_SESSION:$idx" @tabby_group "$name"
}

# ss_set_minimized <window_index> <1|0>
ss_set_minimized() {
    local idx="$1" val="$2"
    if [ "$val" = "1" ]; then
        ss_tmux set-window-option -t "$SS_SESSION:$idx" @tabby_minimized 1
    else
        ss_tmux set-window-option -t "$SS_SESSION:$idx" -u @tabby_minimized
    fi
}

# ss_set_pinned <window_index> <1|0>
ss_set_pinned() {
    local idx="$1" val="$2"
    if [ "$val" = "1" ]; then
        ss_tmux set-window-option -t "$SS_SESSION:$idx" @tabby_pinned 1
    else
        ss_tmux set-window-option -t "$SS_SESSION:$idx" -u @tabby_pinned
    fi
}

# ss_sidebar_pane [window_target] -- returns the sidebar pane id in that window
ss_sidebar_pane() {
    local wtarget="${1:-$SS_SESSION:0}"
    ss_tmux list-panes -t "$wtarget" \
        -F '#{pane_id} #{pane_start_command}' \
        | awk '/sidebar/{print $1; exit}'
}

# ss_bind_demo_keys — install the subset of tabby key bindings we want to
# demonstrate, routed through tabby-hook so they hit our isolated daemon. Must
# be called AFTER ss_setup. Bindings applied:
#   M-}        -- next-window (skips minimized)
#   M-{        -- prev-window (skips minimized)
#   M-S-m      -- toggle minimize on active window
#   prefix+r   -- rename window
#   prefix+k   -- kill window (with confirm)
ss_bind_demo_keys() {
    local hook="PATH='$SS_SHIM_DIR:\$PATH' TABBY_RUNTIME_PREFIX='$SS_PREFIX' '$TABBY_BIN' hook"
    ss_tmux bind-key -n M-\} run-shell -b "$hook next-window"
    ss_tmux bind-key -n M-\{ run-shell -b "$hook prev-window"
    ss_tmux bind-key -n M-S-m run-shell -b "$hook toggle-minimize-window"
    ss_tmux bind-key r command-prompt -I '#W' "rename-window '%%' ; set-window-option @tabby_name_locked 1"
    ss_tmux bind-key k confirm-before -p 'Close window? (y/n)' "run-shell '$hook kill-window #{window_index}'"
}

# ss_attach_cmd — path to a wrapper script for `asciinema rec -c ...`. The
# wrapper attaches to the isolated tmux session; when the script exits (e.g.
# because we detach-client from outside), asciinema stops recording.
ss_attach_cmd() {
    local script="$SS_LOG_DIR/attach.sh"
    cat > "$script" <<EOF
#!/usr/bin/env bash
export PATH="$SS_SHIM_DIR:\$PATH"
export TABBY_RUNTIME_PREFIX="$SS_PREFIX"
exec "$(command -v tmux)" -L "$SS_SOCKET" -f "$SS_TMUX_CONF" attach -t "$SS_SESSION"
EOF
    chmod +x "$script"
    printf '%s\n' "$script"
}

# ss_spawn_sidebar [window_target] [width]
# Split a sidebar-renderer pane into the given window (default: active one)
# at the given column width (default: 25).
ss_spawn_sidebar() {
    local wtarget="${1:-$SS_SESSION:0}"
    local width="${2:-25}"
    local wid
    wid="$(ss_tmux display-message -t "$wtarget" -p '#{window_id}')"
    ss_tmux split-window -h -b -t "$wtarget" -l "$width" \
        "exec -a sidebar-renderer '$TABBY_BIN' render sidebar -session '$SS_SESSION_ID' -window '$wid'"
    # give renderer a moment to connect
    sleep 0.4
}

# Return the path to the daemon log for this run
ss_daemon_log() { echo "$SS_LOG_DIR/daemon.log"; }

trap ss_cleanup EXIT INT TERM
