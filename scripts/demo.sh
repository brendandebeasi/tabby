#!/usr/bin/env bash
# Demo environment for Tabby screenshots and asciinema recordings.
# Creates an isolated tmux server separate from the user's main session.
#
# Uses TABBY_RUNTIME_PREFIX to namespace daemon files so the demo daemon
# at /tmp/demo-tabby-daemon-$0.* doesn't collide with the main daemon at
# /tmp/tabby-daemon-$0.*. Your main session is completely unaffected.
#
# Usage:
#   ./scripts/demo.sh start    # Create demo tmux server + windows + tabby
#   ./scripts/demo.sh stop     # Kill demo server cleanly
#   ./scripts/demo.sh attach   # Attach to demo server
#   ./scripts/demo.sh record   # Start asciinema recording then attach
#   ./scripts/demo.sh reset    # Stop + start (fresh reset)

set -eu

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DEMO_SOCKET="demo"
DEMO_SESSION="demo"
DEMO_CONFIG_DIR="$REPO_DIR/config"
DEMO_RECORDINGS_DIR="$REPO_DIR/demos"
DEMO_RUNTIME_PREFIX="demo-"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { printf "${CYAN}=>${NC} %s\n" "$*"; }
ok()    { printf "${GREEN}=>${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}=>${NC} %s\n" "$*"; }
err()   { printf "${RED}=>${NC} %s\n" "$*" >&2; }

tmux_demo() {
    # -f /dev/null prevents loading ~/.tmux.conf (which would load tabby
    # with the user's real config). Only matters on server startup; tmux
    # ignores -f once the server is running.
    tmux -f /dev/null -L "$DEMO_SOCKET" "$@"
}

demo_running() {
    tmux_demo has-session -t "$DEMO_SESSION" 2>/dev/null
}

# Create a window in a group with optional simulated content.
#   make_window <name> <group> [fake_output_command]
make_window() {
    local name="$1" group="$2" fake_cmd="${3:-}"

    tmux_demo new-window -t "$DEMO_SESSION" -n "$name"
    # Lock the name so the daemon's auto-rename doesn't overwrite it
    tmux_demo set-window-option -t "$DEMO_SESSION:$name" @tabby_name_locked 1
    tmux_demo set-window-option -t "$DEMO_SESSION:$name" automatic-rename off
    if [ -n "$group" ] && [ "$group" != "Default" ]; then
        tmux_demo set-window-option -t "$DEMO_SESSION:$name" @tabby_group "$group"
    fi
    if [ -n "$fake_cmd" ]; then
        tmux_demo send-keys -t "$DEMO_SESSION:$name" "$fake_cmd" Enter
    fi
}

cmd_start() {
    # Kill existing demo server if running
    if demo_running; then
        warn "Demo server already running, stopping first..."
        cmd_stop
    fi

    # Clean up stale demo runtime files (prefixed to avoid touching main daemon)
    rm -f /tmp/${DEMO_RUNTIME_PREFIX}tabby-daemon-* 2>/dev/null || true

    info "Starting demo tmux server on socket '$DEMO_SOCKET'..."

    # -----------------------------------------------------------
    # Create session with first window (Default group: shell)
    # -----------------------------------------------------------
    tmux_demo new-session -d -s "$DEMO_SESSION" -n "shell" -x 200 -y 50

    # Set environment on the session IMMEDIATELY so all panes inherit it.
    # Must be before any make_window calls (split-window inherits session env).
    tmux_demo set-environment -t "$DEMO_SESSION" TABBY_CONFIG_DIR "$DEMO_CONFIG_DIR"
    tmux_demo set-environment -t "$DEMO_SESSION" TABBY_USE_RENDERER "1"
    tmux_demo set-environment -t "$DEMO_SESSION" TABBY_RUNTIME_PREFIX "$DEMO_RUNTIME_PREFIX"

    # Hide the default tmux status bar (tabby provides its own sidebar)
    tmux_demo set-option -t "$DEMO_SESSION" status off

    # Enable overlay pane headers (normally set by tabby.tmux which demo bypasses)
    tmux_demo set-option -g @tabby_pane_headers on
    tmux_demo set-option -g pane-border-status off

    # Pane header colors (read from demo config)
    DEMO_CONFIG_FILE="$DEMO_CONFIG_DIR/config.yaml"
    PANE_ACTIVE_FG=$(grep -A10 "^pane_header:" "$DEMO_CONFIG_FILE" 2>/dev/null | grep "^  active_fg:" | awk '{print $2}' | tr -d '"' || echo "#ffffff")
    PANE_INACTIVE_FG=$(grep -A10 "^pane_header:" "$DEMO_CONFIG_FILE" 2>/dev/null | grep "^  inactive_fg:" | awk '{print $2}' | tr -d '"' || echo "#ffffff")
    BORDER_FROM_TAB=$(grep -A15 "^pane_header:" "$DEMO_CONFIG_FILE" 2>/dev/null | grep "border_from_tab:" | awk '{print $2}' || echo "true")
    tmux_demo set-option -g @tabby_pane_active_fg "${PANE_ACTIVE_FG:-#ffffff}"
    tmux_demo set-option -g @tabby_pane_active_bg_default "#3498db"
    tmux_demo set-option -g @tabby_pane_inactive_fg "${PANE_INACTIVE_FG:-#cccccc}"
    tmux_demo set-option -g @tabby_pane_inactive_bg_default "#333333"
    tmux_demo set-option -g @tabby_pane_command_fg "#aaaaaa"
    tmux_demo set-option -g @tabby_border_from_tab "${BORDER_FROM_TAB:-true}"
    tmux_demo set-option -g @tabby_sidebar "enabled"

    # Lock initial window name too
    tmux_demo set-window-option -t "$DEMO_SESSION:shell" @tabby_name_locked 1
    tmux_demo set-window-option -t "$DEMO_SESSION:shell" automatic-rename off

    # -----------------------------------------------------------
    # Frontend group -- 3 windows, one with pane splits
    # -----------------------------------------------------------
    make_window "web" "Frontend" \
        "printf '\\033[36m  vite v5.4.2\\033[0m dev server running at:\\n\\n  > Local:   \\033[36mhttp://localhost:5173/\\033[0m\\n  > Network: \\033[36mhttp://192.168.1.42:5173/\\033[0m\\n\\n  ready in \\033[1m142ms\\033[0m.\\n'; read"

    make_window "styles" "Frontend"

    make_window "tests" "Frontend" \
        "printf '\\033[32m PASS \\033[0m src/components/Button.test.tsx\\n\\033[32m PASS \\033[0m src/hooks/useAuth.test.ts\\n\\033[32m PASS \\033[0m src/utils/format.test.ts\\n\\n\\033[1mTest Suites:\\033[0m \\033[32m3 passed\\033[0m, 3 total\\n\\033[1mTests:       \\033[0m \\033[32m14 passed\\033[0m, 14 total\\n\\033[1mTime:        \\033[0m 2.341s\\n'; read"

    # -----------------------------------------------------------
    # Backend group -- 2 windows, one with split panes to demo pane headers
    # -----------------------------------------------------------
    make_window "api" "Backend" \
        "printf '\\033[35m[server]\\033[0m listening on :8080\\n\\033[35m[server]\\033[0m GET    /api/v1/users     --> handlers.ListUsers\\n\\033[35m[server]\\033[0m POST   /api/v1/users     --> handlers.CreateUser\\n\\033[35m[server]\\033[0m GET    /api/v1/health    --> handlers.HealthCheck\\n'; read"

    # Split the api window horizontally for log tail
    tmux_demo split-window -t "$DEMO_SESSION:api" -v \
        "printf '\\033[90m[2025-02-23 10:04:12]\\033[0m 200 GET  /api/v1/health  1.2ms\\n\\033[90m[2025-02-23 10:04:15]\\033[0m 200 GET  /api/v1/users   4.8ms\\n\\033[90m[2025-02-23 10:04:18]\\033[0m 201 POST /api/v1/users   12.3ms\\n\\033[90m[2025-02-23 10:04:22]\\033[0m 200 GET  /api/v1/users   3.1ms\\n'; read"
    # Focus back to top pane
    tmux_demo select-pane -t "$DEMO_SESSION:api" -U

    make_window "db" "Backend" \
        "printf '\\033[33mpsql\\033[0m (16.1)\\nType \"help\" for help.\\n\\n\\033[1mdemo=#\\033[0m '; read"

    # -----------------------------------------------------------
    # Docs group -- 2 windows
    # -----------------------------------------------------------
    make_window "readme" "Docs" \
        "printf '\\033[1m# Tabby\\033[0m\\n\\nA tmux plugin with a daemon-driven vertical sidebar UI,\\ncomplete with window groups, widgets, and pane headers.\\n\\n\\033[36m## Features\\033[0m\\n- Window groups with custom colors\\n- Virtual pet widget\\n- System stats (CPU/RAM/battery)\\n- Git integration\\n- Pane headers with drag-to-resize\\n'; read"

    make_window "changelog" "Docs"

    # -----------------------------------------------------------
    # Infra group -- 2 windows (shows the 4th group color)
    # -----------------------------------------------------------
    make_window "docker" "Infra" \
        "printf '\\033[32mCONTAINER ID   IMAGE           STATUS          PORTS\\033[0m\\nabc123def456   app:latest      Up 3 hours      0.0.0.0:8080->8080\\n789ghi012jkl   postgres:16     Up 3 hours      0.0.0.0:5432->5432\\nmno345pqr678   redis:7         Up 3 hours      0.0.0.0:6379->6379\\n'; read"

    make_window "logs" "Infra" \
        "printf '\\033[90m[nginx]\\033[0m  192.168.1.1 - - \"GET / HTTP/1.1\" 200 612\\n\\033[90m[nginx]\\033[0m  192.168.1.1 - - \"GET /api HTTP/1.1\" 200 1024\\n\\033[90m[app]\\033[0m    connected to database\\n\\033[90m[redis]\\033[0m  ready to accept connections\\n'; read"

    # -----------------------------------------------------------
    # Default group -- the shell window is already here
    # -----------------------------------------------------------
    make_window "htop" "Default" \
        "printf '\\033[1;37m  CPU[\\033[32m||||||||||||\\033[90m              \\033[37m] 38.2%%\\033[0m\\n\\033[1;37m  Mem[\\033[34m||||||||||||||||||\\033[90m    \\033[37m] 62.1%%  4.97G/8.00G\\033[0m\\n\\033[1;37m  Swp[\\033[33m||\\033[90m                    \\033[37m]  3.4%%  0.12G/4.00G\\033[0m\\n'; read"

    # -----------------------------------------------------------
    # Simulate indicator states on select windows
    # -----------------------------------------------------------
    # Busy spinner on "web" (dev server compiling)
    tmux_demo set-window-option -t "$DEMO_SESSION:web" @tabby_busy 1
    # Bell on "db" (query finished)
    tmux_demo set-window-option -t "$DEMO_SESSION:db" @tabby_bell 1
    # Silence on "changelog" (idle for a while)
    tmux_demo set-window-option -t "$DEMO_SESSION:changelog" @tabby_silence 1
    # Input waiting on "api" (waiting for user input)
    tmux_demo set-window-option -t "$DEMO_SESSION:api" @tabby_input 1

    # -----------------------------------------------------------
    # Select the first Frontend window for a nice initial view
    # -----------------------------------------------------------
    tmux_demo select-window -t "$DEMO_SESSION:web"

    # -----------------------------------------------------------
    # Start the daemon directly (skip tabby.tmux hooks entirely).
    # tabby.tmux hooks use #{session_id} which expands to "$0" then
    # to "sh" in run-shell context, spawning rogue daemons/renderers
    # that connect to the main daemon. The daemon handles renderer
    # spawning on its own -- no hooks needed.
    # -----------------------------------------------------------

    info "Starting daemon..."

    # Get the TMUX env var from the demo server (has correct socket path)
    tmux_demo run-shell "printenv TMUX > /tmp/tabby-demo-tmux-env" 2>/dev/null || true
    DEMO_TMUX=$(cat /tmp/tabby-demo-tmux-env 2>/dev/null || echo "")
    rm -f /tmp/tabby-demo-tmux-env

    if [ -z "$DEMO_TMUX" ]; then
        DEMO_TMUX="/private/tmp/tmux-$(id -u)/demo,0,0"
    fi

    # Session ID is always $0 (first session in the demo server).
    DEMO_SESSION_ID='$0'

    # Start daemon with runtime prefix so files go to /tmp/demo-tabby-daemon-$0.*
    TMUX="$DEMO_TMUX" \
    TABBY_CONFIG_DIR="$DEMO_CONFIG_DIR" \
    TABBY_RUNTIME_PREFIX="$DEMO_RUNTIME_PREFIX" \
    TABBY_USE_RENDERER="1" \
        "$REPO_DIR/bin/tabby-daemon" -session "$DEMO_SESSION_ID" >/dev/null 2>&1 &

    # Wait for daemon socket (prefixed path)
    DEMO_SOCK="/tmp/${DEMO_RUNTIME_PREFIX}tabby-daemon-\$0.sock"
    for _ in $(seq 1 30); do
        [ -S "$DEMO_SOCK" ] && break
        sleep 0.2
    done

    if [ ! -S "$DEMO_SOCK" ]; then
        warn "Daemon socket not ready (may still be starting)."
    fi

    # Give daemon time to spawn sidebar renderers
    sleep 2

    ok "Demo environment ready!"
    echo ""
    echo "  Windows created:"
    echo "    Frontend : web (dev server), styles, tests (passing)"
    echo "    Backend  : api (split: server + logs), db (psql)"
    echo "    Docs     : readme, changelog"
    echo "    Infra    : docker (containers), logs"
    echo "    Default  : shell, htop"
    echo ""
    printf "  ${BOLD}Attach:${NC}  ./scripts/demo.sh attach\n"
    printf "  ${BOLD}Record:${NC}  ./scripts/demo.sh record\n"
    printf "  ${BOLD}Stop:${NC}    ./scripts/demo.sh stop\n"
    printf "  ${BOLD}Reset:${NC}   ./scripts/demo.sh reset\n"
    echo ""
    printf "  ${BOLD}Direct:${NC}  tmux -L $DEMO_SOCKET attach -t $DEMO_SESSION\n"
    echo ""
}

cmd_stop() {
    if ! demo_running; then
        warn "Demo server is not running."
        return 0
    fi

    info "Stopping demo server..."

    # Kill demo daemon via its prefixed PID file
    DEMO_PID_FILE="/tmp/${DEMO_RUNTIME_PREFIX}tabby-daemon-\$0.pid"
    if [ -f "$DEMO_PID_FILE" ]; then
        local dpid
        dpid=$(cat "$DEMO_PID_FILE" 2>/dev/null || echo "")
        [ -n "$dpid" ] && kill "$dpid" 2>/dev/null || true
    fi

    # Kill the tmux server
    tmux_demo kill-server 2>/dev/null || true

    # Clean up demo runtime files
    rm -f /tmp/${DEMO_RUNTIME_PREFIX}tabby-daemon-* 2>/dev/null || true
    # Clean any stale non-prefixed artifacts from hook-spawned daemons
    rm -f /tmp/tabby-daemon-sh* /tmp/tabby-daemon-.* /tmp/tabby-daemon--* 2>/dev/null || true

    ok "Demo server stopped."
}

cmd_attach() {
    if ! demo_running; then
        err "Demo server is not running. Start it first:"
        echo "  ./scripts/demo.sh start"
        exit 1
    fi

    info "Attaching to demo server..."
    exec tmux -f /dev/null -L "$DEMO_SOCKET" attach -t "$DEMO_SESSION"
}

cmd_record() {
    if ! command -v asciinema >/dev/null 2>&1; then
        err "asciinema is not installed."
        echo "  Install: brew install asciinema"
        exit 1
    fi

    if ! demo_running; then
        info "Demo server not running, starting it first..."
        cmd_start
    fi

    mkdir -p "$DEMO_RECORDINGS_DIR"
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    RECORDING_FILE="$DEMO_RECORDINGS_DIR/tabby-demo-${TIMESTAMP}.cast"

    info "Recording to: $RECORDING_FILE"
    info "Press Ctrl-D or type 'exit' to stop recording."
    echo ""

    asciinema rec "$RECORDING_FILE" --command "tmux -f /dev/null -L $DEMO_SOCKET attach -t $DEMO_SESSION"

    ok "Recording saved: $RECORDING_FILE"
}

cmd_reset() {
    info "Resetting demo environment..."
    cmd_stop
    cmd_start
}

cmd_help() {
    cat <<EOF
Tabby Demo Environment

Usage: $(basename "$0") <command>

Commands:
  start    Create isolated demo tmux server with curated windows and tabby
  stop     Tear down the demo server cleanly
  attach   Attach to the demo server
  record   Start an asciinema recording of the demo server
  reset    Stop and restart (fresh reset)
  help     Show this help message

The demo server runs on a separate tmux socket ('$DEMO_SOCKET') and uses
its own config at config/demo.yaml. Your main tmux session is unaffected.

Runtime files are namespaced with prefix '$DEMO_RUNTIME_PREFIX' to avoid
collisions with the main daemon.
EOF
}

# -- Main --
COMMAND="${1:-help}"

case "$COMMAND" in
    start)   cmd_start ;;
    stop)    cmd_stop ;;
    attach)  cmd_attach ;;
    record)  cmd_record ;;
    reset)   cmd_reset ;;
    help|-h|--help) cmd_help ;;
    *)
        err "Unknown command: $COMMAND"
        cmd_help
        exit 1
        ;;
esac
