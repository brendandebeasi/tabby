#!/usr/bin/env bash
# Reset terminal mouse modes after tabby restart
# Run this if mouse/keyboard stops working

# Disable all mouse tracking modes
printf '\033[?1000l'  # Basic mouse tracking
printf '\033[?1002l'  # Button event tracking
printf '\033[?1003l'  # Any event tracking (cell motion)
printf '\033[?1004l'  # Focus events
printf '\033[?1005l'  # UTF-8 mouse mode
printf '\033[?1006l'  # SGR mouse mode
printf '\033[?1015l'  # URXVT mouse mode
printf '\033[?2004l'  # Bracketed paste

# Reset tmux mouse
tmux set -g mouse off 2>/dev/null
tmux set -g mouse on 2>/dev/null

# Refresh tmux client
tmux refresh-client -S 2>/dev/null

echo "Terminal reset complete"
