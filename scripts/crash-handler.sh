#!/usr/bin/env bash
# crash-handler.sh — Handle tabby-daemon crashes with notifications + investigation
#
# Developer-mode feature: only creates GH issues and kicks off OpenCode investigation
# when the user has gh CLI authenticated with write access to the upstream repo.
#
# Called by watchdog_daemon.sh on abnormal exit.
#
# Usage: crash-handler.sh <session_id> <exit_code> <restart_count> <max_restarts>
#
# Two tiers:
#   - Transient crash (restart_count <= max_restarts): notification + bell indicator
#   - Give-up (restart_count > max_restarts): notification + GH issue + OpenCode investigation

set -u

SESSION_ID="${1:-}"
EXIT_CODE="${2:-1}"
RESTART_COUNT="${3:-1}"
MAX_RESTARTS="${4:-5}"

# shellcheck disable=SC1007
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck disable=SC1007
TABBY_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
INDICATOR="$TABBY_DIR/bin/tabby hook set-indicator"
LOG="/tmp/tabby-crash-handler.log"
CRASH_LOG="/tmp/tabby-daemon-${SESSION_ID}-crash.log"
EVENTS_LOG="/tmp/tabby-daemon-${SESSION_ID}.events.log"

# ── Logging ───────────────────────────────────────────────────────────────
log() {
    printf "%s %s\n" "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >> "$LOG"
}

log "crash-handler invoked: session=$SESSION_ID exit=$EXIT_CODE restart=$RESTART_COUNT/$MAX_RESTARTS"

# ── Crash reason from exit code ───────────────────────────────────────────
crash_reason() {
    case "$EXIT_CODE" in
        139) echo "segmentation fault (SIGSEGV)" ;;
        137) echo "killed (SIGKILL / OOM)" ;;
        143) echo "terminated (SIGTERM)" ;;
        134) echo "abort (SIGABRT)" ;;
        130) echo "interrupted (SIGINT)" ;;
          2) echo "panic / fatal error" ;;
          1) echo "general error" ;;
          *) echo "exit code $EXIT_CODE" ;;
    esac
}

REASON=$(crash_reason)
IS_GIVE_UP=false
if [ "$RESTART_COUNT" -gt "$MAX_RESTARTS" ]; then
    IS_GIVE_UP=true
fi

# ── Developer mode detection ─────────────────────────────────────────────
# Feature auto-enables when user has:
#   1. gh CLI installed and authenticated
#   2. Write/push access to the upstream tabby repo
#
# Caches the result for 1 hour to avoid hammering the GH API.

UPSTREAM_REPO="brendandebeasi/tabby"
DEV_MODE_CACHE="/tmp/tabby-dev-mode-check"
DEV_MODE_TTL=3600  # 1 hour

is_developer_mode() {
    # Check cache first
    if [ -f "$DEV_MODE_CACHE" ]; then
        local cache_age
        cache_age=$(( $(date +%s) - $(stat -f '%m' "$DEV_MODE_CACHE" 2>/dev/null || echo 0) ))
        if [ "$cache_age" -lt "$DEV_MODE_TTL" ]; then
            local cached
            cached=$(cat "$DEV_MODE_CACHE" 2>/dev/null)
            [ "$cached" = "1" ] && return 0
            return 1
        fi
    fi

    # gh CLI available?
    command -v gh &>/dev/null || { echo "0" > "$DEV_MODE_CACHE"; return 1; }

    # Authenticated?
    gh auth status &>/dev/null || { echo "0" > "$DEV_MODE_CACHE"; return 1; }

    # Has push access to upstream repo?
    local perms
    perms=$(gh api "repos/${UPSTREAM_REPO}" --jq '.permissions.push' 2>/dev/null || echo "false")
    if [ "$perms" = "true" ]; then
        echo "1" > "$DEV_MODE_CACHE"
        return 0
    fi

    echo "0" > "$DEV_MODE_CACHE"
    return 1
}

# ── Notification helpers ──────────────────────────────────────────────────
send_notification() {
    local title="$1"
    local subtitle="$2"
    local message="$3"
    local sound="${4:-}"

    if command -v growlrrr &>/dev/null; then
        local args=(send --appId Tabby --title "$title" --subtitle "$subtitle")
        [ -n "$sound" ] && args+=(--sound "$sound")
        args+=("$message")
        growlrrr "${args[@]}" &>/dev/null &
    elif command -v terminal-notifier &>/dev/null; then
        local args=(-title "$title" -subtitle "$subtitle" -message "$message")
        [ -n "$sound" ] && args+=(-sound "$sound")
        terminal-notifier "${args[@]}" &>/dev/null &
    fi
}

# ── Collect crash context ─────────────────────────────────────────────────
collect_crash_context() {
    local max_lines="${1:-80}"
    local ctx=""

    ctx+="## Crash Report\n\n"
    ctx+="| Field | Value |\n"
    ctx+="|-------|-------|\n"
    ctx+="| Session | \`$SESSION_ID\` |\n"
    ctx+="| Exit code | \`$EXIT_CODE\` ($REASON) |\n"
    ctx+="| Restart attempt | $RESTART_COUNT / $MAX_RESTARTS |\n"
    ctx+="| Timestamp | $(date -u '+%Y-%m-%dT%H:%M:%SZ') |\n"

    # Git revision
    local git_rev
    git_rev=$(git -C "$TABBY_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")
    local git_dirty=""
    if ! git -C "$TABBY_DIR" diff --quiet HEAD 2>/dev/null; then
        git_dirty=" (dirty)"
    fi
    ctx+="| Git rev | \`${git_rev}${git_dirty}\` |\n"

    # Go version
    local go_ver
    go_ver=$(go version 2>/dev/null | awk '{print $3}' || echo "unknown")
    ctx+="| Go | \`$go_ver\` |\n"

    # System info
    ctx+="| OS | $(uname -s) $(uname -r) |\n"
    ctx+="| tmux | $(tmux -V 2>/dev/null || echo unknown) |\n"
    ctx+="\n"

    # Crash log (last N lines — contains stack traces, LOOP_STALL, DEADLOCK info)
    if [ -f "$CRASH_LOG" ]; then
        ctx+="### Crash Log (last $max_lines lines)\n\n"
        ctx+="\`\`\`\n"
        ctx+="$(tail -"$max_lines" "$CRASH_LOG" 2>/dev/null || echo '(empty)')\n"
        ctx+="\`\`\`\n\n"
    fi

    # Events log (last 30 lines — recent daemon activity before crash)
    if [ -f "$EVENTS_LOG" ]; then
        ctx+="### Events Log (last 30 lines)\n\n"
        ctx+="\`\`\`\n"
        ctx+="$(tail -30 "$EVENTS_LOG" 2>/dev/null || echo '(empty)')\n"
        ctx+="\`\`\`\n\n"
    fi

    printf '%b' "$ctx"
}

# ── Set indicators ────────────────────────────────────────────────────────
set_indicator() {
    $INDICATOR "$1" "$2" 2>/dev/null || true
}

# ── Tier 1: Transient crash (every crash) ─────────────────────────────────
handle_transient_crash() {
    log "transient crash: restarting ($RESTART_COUNT/$MAX_RESTARTS)"

    set_indicator bell 1

    send_notification \
        "Tabby restarting" \
        "$REASON" \
        "Crash $RESTART_COUNT/$MAX_RESTARTS — restarting automatically" \
        ""
}

# ── Tier 2: Give-up (daemon can't self-recover) ──────────────────────────
handle_give_up() {
    log "give-up: $MAX_RESTARTS crashes in restart window"

    set_indicator bell 1

    send_notification \
        "Tabby crashed" \
        "$REASON" \
        "Failed $MAX_RESTARTS restart attempts. Investigating..." \
        "default"

    # Developer mode: create GH issue + kick off investigation
    if is_developer_mode; then
        log "developer mode active — creating GH issue + investigation"
        create_github_issue
        start_investigation
    else
        log "developer mode inactive — skipping GH issue + investigation"
    fi
}

# ── GitHub issue creation ─────────────────────────────────────────────────
create_github_issue() {
    local git_rev
    git_rev=$(git -C "$TABBY_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")

    local title
    title="Crash: ${REASON} (${git_rev}, $(date '+%Y-%m-%d %H:%M'))"
    local body
    body=$(collect_crash_context 120)

    # Check for existing open crash issues to avoid duplicates
    local existing
    existing=$(gh issue list --repo "$UPSTREAM_REPO" --label "crash" --state open --limit 5 --json number,title -q '.[].number' 2>/dev/null || echo "")

    if [ -n "$existing" ]; then
        # Append to most recent open crash issue instead of creating a new one
        local latest_issue
        latest_issue=$(echo "$existing" | head -1)
        log "appending to existing crash issue #$latest_issue"
        gh issue comment "$latest_issue" --repo "$UPSTREAM_REPO" --body "$body" 2>/dev/null || true
    else
        log "creating new crash issue"
        gh issue create \
            --repo "$UPSTREAM_REPO" \
            --title "$title" \
            --body "$body" \
            --label "crash,auto-triage" 2>/dev/null || {
            log "gh issue create failed (labels may not exist, retrying without labels)"
            gh issue create \
                --repo "$UPSTREAM_REPO" \
                --title "$title" \
                --body "$body" 2>/dev/null || log "gh issue create failed completely"
        }
    fi
}

# ── OpenCode investigation ────────────────────────────────────────────────
start_investigation() {
    # Check if opencode is available
    if ! command -v opencode &>/dev/null; then
        log "opencode not found — skipping investigation"
        return
    fi

    local crash_context
    crash_context=$(collect_crash_context 200)

    local prompt="A Tabby daemon crash just occurred and I need your help investigating.

Here is the crash report:

${crash_context}

Please:
1. Analyze the crash log and stack trace to identify the root cause
2. Check the relevant source files in the codebase for the bug
3. Suggest a minimal fix
4. If you're confident in the fix, create a PR

Key source files to check:
- cmd/tabby/internal/daemon/main.go (daemon main loop, panic recovery)
- cmd/tabby/internal/daemon/coordinator.go (render coordination, locking)
- cmd/tabby/internal/sidebar/sidebar.go (renderer client)

Focus on PANIC, CRASH, LOOP_STALL, and DEADLOCK entries in the crash log."

    log "starting opencode investigation"

    send_notification \
        "Tabby: Investigation Started" \
        "$REASON" \
        "OpenCode is analyzing the crash and will suggest a fix" \
        "default"

    # Run in background — don't block the watchdog's exit
    # Use nohup to survive watchdog exit
    nohup opencode --message "$prompt" </dev/null >>/tmp/tabby-crash-investigation.log 2>&1 &
    log "opencode investigation started (pid=$!)"
}

# ── Main ──────────────────────────────────────────────────────────────────
if [ "$IS_GIVE_UP" = "true" ]; then
    handle_give_up
else
    handle_transient_crash
fi

log "crash-handler done"
