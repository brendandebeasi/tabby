#!/usr/bin/env bash
# Kill all windows in a group
# Usage: kill_group.sh <group_pattern>
# Example: kill_group.sh "^SD\\|" for StudioDome group

GROUP_PATTERN="$1"

if [ -z "$GROUP_PATTERN" ]; then
    echo "Usage: kill_group.sh <group_pattern>" >&2
    exit 1
fi

# Get all window indices that match the pattern
windows_to_kill=""
for win_info in $(tmux list-windows -F '#{window_index}|#{window_name}'); do
    win_idx=$(echo "$win_info" | cut -d'|' -f1)
    win_name=$(echo "$win_info" | cut -d'|' -f2)

    if echo "$win_name" | grep -qE "$GROUP_PATTERN"; then
        windows_to_kill="$windows_to_kill $win_idx"
    fi
done

# Kill windows in reverse order (highest index first to avoid reindexing issues)
for idx in $(echo "$windows_to_kill" | tr ' ' '\n' | sort -rn); do
    [ -n "$idx" ] && tmux kill-window -t ":$idx" 2>/dev/null || true
done

# Switch to a remaining window
first_idx=$(tmux list-windows -F '#{window_index}' 2>/dev/null | sort -n | awk 'NR==1{print; exit}')
[ -n "$first_idx" ] && tmux select-window -t ":$first_idx" 2>/dev/null || true

# Focus main pane
main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar' | head -1 | cut -d: -f1)
[ -n "$main_pane" ] && tmux select-pane -t "$main_pane"

# Refresh sidebars
sleep 0.1
for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null || true
done

exit 0
