#!/usr/bin/env bash
# Narrated driver for the minimize scenario. Prints captions + inline sidebar
# snapshots between actions. Intended to be invoked from a vhs tape so the
# whole sequence becomes a gif.

set -euo pipefail

REPO="${TABBY_REPO:-/Users/b/git/tabby}"
source "$REPO/tests/demos/lib/harness.sh"
source "$REPO/tests/demos/lib/narrate.sh"

ss_setup 150 42 minimize
ss_add_windows "SD|debug" "GP|server" "GP|client" "notes" "vim"
ss_set_group 0 StudioDome
ss_set_group 1 StudioDome
ss_set_group 2 Gunpowder
ss_set_group 3 Gunpowder
ss_tmux select-window -t demo:0
ss_poke_daemon
sleep 0.7

SIDEBAR="$(ss_sidebar_pane demo:0)"

snap() { narr_snapshot "$SIDEBAR"; }

narr_caption "Five windows across StudioDome / Gunpowder / Default groups."
snap
narr_pause 2.5

narr_caption "Focus SD|debug, then minimize it with cmd+shift+m."
ss_tmux select-window -t demo:1
ss_poke_daemon
sleep 0.5
snap
narr_pause 1.6
ss_set_minimized 1 1
ss_poke_daemon
sleep 0.5
snap
narr_pause 2.2

narr_caption "Back to SD|app, then cmd+] cycles forward — watch it skip SD|debug."
ss_tmux select-window -t demo:0
ss_poke_daemon
sleep 0.4
snap
narr_pause 1.4
# Simulate forward cycling; skip window 1 because it's minimized.
for idx in 2 3 4 5 0; do
    ss_tmux select-window -t "demo:$idx"
    ss_poke_daemon
    sleep 0.25
    snap
    narr_pause 1.0
done

narr_caption "Unminimize: row restores and is back in the cycle."
ss_tmux select-window -t demo:1
ss_set_minimized 1 0
ss_poke_daemon
sleep 0.5
snap
narr_pause 2.0

narr_caption "Done."
narr_pause 1.2
