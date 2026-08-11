#!/usr/bin/env bash
# Synthetic RefreshWindows benchmark. Spins a private tmux server at a range of
# window counts and times the real subprocess work behind RefreshWindows.
#
# Isolation: the server lives on its own socket (-L) with an empty config, and
# the harness reaches it through a PATH shim that injects -L into every tmux
# call. It cannot see or touch the user's live tabby session.
set -uo pipefail

SOCKET="tabby-bench-$$"
COUNTS="${COUNTS:-5 10 20 40}"
ITERS="${ITERS:-20}"
SPLIT_EVERY="${SPLIT_EVERY:-3}"
TM=(tmux -L "$SOCKET" -f /dev/null)
SHIMDIR="$(mktemp -d)"

cleanup() {
  "${TM[@]}" kill-server 2>/dev/null
  rm -rf "$SHIMDIR"
}
trap cleanup EXIT

cat > "$SHIMDIR/tmux" <<SHIM
#!/usr/bin/env bash
exec $(command -v tmux) -L "$SOCKET" -f /dev/null "\$@"
SHIM
chmod +x "$SHIMDIR/tmux"

timeout 30 "${TM[@]}" new-session -d -s bench -x 200 -y 50 || { echo "tmux start failed" >&2; exit 1; }

for n in $COUNTS; do
  have=$("${TM[@]}" list-windows -t bench 2>/dev/null | wc -l | tr -d ' ')
  while [ "$have" -lt "$n" ]; do
    timeout 10 "${TM[@]}" new-window -t bench -d 2>/dev/null
    have=$((have + 1))
  done

  i=0
  for w in $("${TM[@]}" list-windows -t bench -F '#{window_id}'); do
    i=$((i + 1))
    if [ $((i % SPLIT_EVERY)) -eq 0 ]; then
      timeout 10 "${TM[@]}" split-window -t "$w" -d 2>/dev/null
    fi
  done

  PATH="$SHIMDIR:$PATH" timeout 180 go run ./scripts/benchrefresh -iters "$ITERS" -label "windows=$n panes=+$((n / SPLIT_EVERY))"
done
