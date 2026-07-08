#!/usr/bin/env bash
# vm-screenshot.sh <socket> <session> <out.png> [cols] [rows]
# Attaches a sixel-capable xterm (under Xvfb) to a tmux session and screenshots
# it to a PNG. Lets rendering (box, sixel graphics, gradients) be checked visually.
set -u
SOCK="${1:?socket}"; SESS="${2:?session}"; OUT="${3:?out}"
COLS="${4:-120}"; ROWS="${5:-40}"
TMUXBIN=/usr/local/bin/tmux
DISP=":99"
pkill -f "Xvfb $DISP" >/dev/null 2>&1; sleep 0.3
rm -f /tmp/.X99-lock >/dev/null 2>&1
Xvfb $DISP -screen 0 2200x900x24 >/tmp/xvfb.log 2>&1 &
XVFB=$!
sleep 1.2
DISPLAY=$DISP xterm -ti vt340 \
  -xrm 'XTerm*decTerminalID: vt340' \
  -xrm 'XTerm*sixelScrolling: true' \
  -geometry ${COLS}x${ROWS} \
  -fa 'DejaVu Sans Mono' -fs 12 -bg black -fg white \
  -e "$TMUXBIN" -L "$SOCK" attach -t "$SESS" >/tmp/xterm.log 2>&1 &
XTERM=$!
sleep 4
DISPLAY=$DISP import -window root "$OUT" 2>/tmp/import.log \
  || { DISPLAY=$DISP xwd -root 2>/dev/null | convert xwd:- "$OUT" 2>/dev/null; }
kill $XTERM >/dev/null 2>&1; kill $XVFB >/dev/null 2>&1
if [ -s "$OUT" ]; then echo "screenshot OK: $OUT ($(wc -c < "$OUT") bytes, $(identify -format '%wx%h' "$OUT" 2>/dev/null))"; else echo "screenshot FAILED"; tail -3 /tmp/xterm.log; fi
