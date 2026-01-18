#!/usr/bin/env bash

echo "=== TMUX TABS DIAGNOSTIC ==="
echo ""

echo "1. CURRENT WINDOWS:"
tmux list-windows -F "#I:#W (active=#{window_active})"
echo ""

echo "2. STATUS BAR SETTINGS:"
echo "   Status: $(tmux show-option -g status | cut -d' ' -f2)"
echo "   Position: $(tmux show-option -g status-position | cut -d' ' -f2)"
echo ""

echo "3. WINDOW FORMAT TEST:"
echo "   Current format: $(tmux show-window-option -g window-status-format)"
echo ""

echo "4. RENDER-TAB OUTPUT TEST:"
for win in $(tmux list-windows -F "#I:#W"); do
    idx=$(echo "$win" | cut -d: -f1)
    name=$(echo "$win" | cut -d: -f2-)
    echo -n "   Window $idx ($name): "
    /Users/b/git/tmux-tabs/bin/render-tab normal "$idx" "$name" ""
done
echo ""

echo "5. STYLE OVERRIDES:"
echo "   window-status-style: $(tmux show-window-option -g window-status-style | cut -d' ' -f2-)"
echo "   window-status-current-style: $(tmux show-window-option -g window-status-current-style | cut -d' ' -f2-)"
echo ""

echo "6. POTENTIAL CONFLICTS:"
tmux show-options -g | grep -E "status-format|@theme" || echo "   No status-format conflicts found"
echo ""

echo "7. TERMINAL COLORS:"
echo "   TERM=$TERM"
echo "   Color test: "
for i in 160 167 196 240 242 250 252; do
    echo -n "   colour$i: "
    tput setaf "$i" 2>/dev/null && echo -n "████" && tput sgr0
    echo " "
done
