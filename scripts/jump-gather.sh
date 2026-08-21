#!/usr/bin/env bash
# Emit a bounded, secret-filtered digest of this host's ssh targets, hot
# directories and command usage, for an LLM to turn into a landing config.
# Reads only; writes nothing but stdout.
set -uo pipefail

TOP_HOSTS=${TOP_HOSTS:-25}
TOP_DIRS=${TOP_DIRS:-25}
TOP_CMDS=${TOP_CMDS:-30}

# Anything matching this never leaves the machine. Err toward dropping.
SECRET_RE='(pass(wo?rd)?|secret|token|api[_-]?key|apikey|authoriz|authent|bearer|credential|private[_-]?key|AKIA[0-9A-Z]{8}|gh[pousr]_[A-Za-z0-9]{16}|sk-[A-Za-z0-9]{16}|xox[baprs]-|-----BEGIN)'
# Long opaque blobs are almost always pasted keys. No '/' in the class: paths
# routinely run past 40 chars and dropping them would gut the digest.
BLOB_RE='[A-Za-z0-9+_-]{40,}'

hist_lines() {
  local f seen=""
  for f in "${HISTFILE:-}" "$HOME/.zsh_history" "$HOME/.bash_history"; do
    [ -n "$f" ] && [ -r "$f" ] || continue
    case " $seen " in *" $f "*) continue ;; esac
    seen="$seen $f"
    # zsh EXTENDED_HISTORY prefix is ": <epoch>:<elapsed>;"
    sed -E 's/^: [0-9]+:[0-9]+;//' "$f"
  done 2>/dev/null
}

clean_hist() {
  hist_lines \
    | grep -Eiv "$SECRET_RE" \
    | grep -Ev "$BLOB_RE" \
    | sed -E 's/[[:space:]]+$//' \
    | grep -Ev '^[[:space:]]*$'
}

echo "# tabby landing digest"
echo "host: $(hostname -s 2>/dev/null || echo unknown)"
echo "user: ${USER:-unknown}"
echo "os: $(uname -s)"
echo "generated_from: shell history + ssh config + zoxide (no cwd correlation available)"
echo

echo "## ssh destinations, by history frequency"
clean_hist \
  | grep -E '^(ssh|mosh|et) ' \
  | sed -E 's/^(ssh|mosh|et) +//' \
  | awk 'BEGIN{split("-o -i -p -l -L -R -D -F -J -b -c -e -m -O -Q -S -W -w",a," ");for(k in a)takesarg[a[k]]=1}
         {for(i=1;i<=NF;i++){
            if($i ~ /^-/){ if($i in takesarg) i++; continue }
            print $i; next }}' \
  | sed -E 's/^[^@]+@//' \
  | grep -Ev '^$|^[0-9.]+$' \
  | sort | uniq -c | sort -rn | head -"$TOP_HOSTS"
echo

echo "## remote invocations of interactive tools (what you go there to DO)"
clean_hist \
  | grep -E '^(ssh|mosh) ' \
  | grep -E '(claude|opencode|codex|aider|nvim|vim|lazygit|k9s|tmux|htop)' \
  | cut -c1-160 \
  | sort | uniq -c | sort -rn | head -15
echo

echo "## hosts declared in ~/.ssh/config (wildcards excluded)"
if [ -r "$HOME/.ssh/config" ]; then
  grep -Ei '^[[:space:]]*Host[[:space:]]+' "$HOME/.ssh/config" \
    | sed -E 's/^[[:space:]]*[Hh]ost[[:space:]]+//' \
    | tr ' ' '\n' | grep -Ev '[*?]|^$' | sort -u | head -60
else
  echo "(none)"
fi
echo

echo "## hot directories (zoxide frecency, score first)"
if command -v zoxide >/dev/null 2>&1; then
  zoxide query -l -s 2>/dev/null | head -"$TOP_DIRS"
else
  clean_hist | grep -E '^cd ' \
    | sed -E 's/^cd +//; s/[[:space:]]*(&&|\|\||;|\|).*$//; s/[[:space:]]+$//' \
    | grep -Ev '^$|^-' | sort | uniq -c | sort -rn | head -"$TOP_DIRS"
fi
echo

echo "## git repos under common project roots"
for root in /projects "$HOME/projects" "$HOME/git" "$HOME/src" "$HOME/work"; do
  [ -d "$root" ] || continue
  find "$root" -maxdepth 3 -name .git -type d -prune 2>/dev/null \
    | sed -E 's:/\.git$::' | head -40
done
echo

echo "## top commands overall"
clean_hist \
  | awk '{ if ($1 ~ /^(sudo|command|time|nohup)$/) print $1" "$2; else print $1 }' \
  | grep -Ev '^$' | sort | uniq -c | sort -rn | head -"$TOP_CMDS"
echo

echo "## long-running / interactive tools seen (landing-page candidates)"
clean_hist \
  | grep -Eo '^(claude|opencode|codex|aider|nvim|vim|hx|lazygit|k9s|htop|tmux|docker|kubectl|npm|pnpm|yarn|make|cargo|go|python3?|uv|rails|psql)( +[a-z:-]+)?' \
  | sort | uniq -c | sort -rn | head -20
echo

echo "## tabby appearance already configured (color/icon/group per dir)"
CWDC="${TABBY_STATE_DIR:-$HOME/.local/state/tabby}/cwd-colors.json"
if [ -r "$CWDC" ]; then
  python3 -c 'import json,sys
d=json.load(open(sys.argv[1]))
for k,v in sorted(d.items()):
    print(k, "->", " ".join(f"{a}={b}" for a,b in v.items()))' "$CWDC" 2>/dev/null | head -40
else
  echo "(none)"
fi
echo

echo "## tabby remote_hosts rules (color/icon/group per ssh glob)"
TCFG="${TABBY_CONFIG_DIR:-$HOME/.config/tabby}/config.yaml"
if [ -r "$TCFG" ]; then
  python3 -c 'import yaml,sys
c=yaml.safe_load(open(sys.argv[1])) or {}
def walk(o):
    if isinstance(o,dict):
        for k,v in o.items():
            if k=="remote_hosts" and isinstance(v,list): yield from v
            else: yield from walk(v)
    elif isinstance(o,list):
        for i in o: yield from walk(i)
for r in walk(c):
    print(r.get("match",""), "->", "color="+str(r.get("color","")), "icon="+str(r.get("icon","")), "group="+str(r.get("group","")))' "$TCFG" 2>/dev/null | head -20
else
  echo "(none)"
fi
echo

echo "## live tmux windows (if any)"
tmux list-windows -a -F '#{session_name}:#{window_name} #{pane_current_path}' 2>/dev/null | head -20 || echo "(no tmux server)"
