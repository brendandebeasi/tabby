#!/usr/bin/env bash
set -euo pipefail

if ! command -v git >/dev/null 2>&1; then
  echo "check-commit-hygiene: git is required" >&2
  exit 1
fi

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

staged_files=$(git diff --cached --name-only --diff-filter=ACMR)
[ -z "$staged_files" ] && exit 0

blocked_path_regex='^(\.sisyphus/|\.omc/|\.claude/|\.entire/|\.archive/)|(^|/)(tmp|logs?)/|\.log$|\.hup$|(^|/)log\.txt$|(^|/)Untitled$|(^|/)todos\.md$|(^|/)\.DS_Store$|(^|/)\.env($|\.)|(^|/)credentials\.json$|\.(pem|key|p12|pfx|crt|cer)$'

blocked_paths=$(printf '%s\n' "$staged_files" | grep -E "$blocked_path_regex" || true)
if [ -n "$blocked_paths" ]; then
  echo ""
  echo "Commit blocked: staged files include local/debug/sensitive artifacts:" >&2
  printf '%s\n' "$blocked_paths" >&2
  echo ""
  echo "Move/delete/untrack these files before committing." >&2
  exit 1
fi

candidate_files=$(printf '%s\n' "$staged_files" | grep -Ev '^(vendor/|docs/|README\.md$|CHANGELOG\.md$|LICENSE$|\.github/)' || true)
if [ -z "$candidate_files" ]; then
  exit 0
fi

tmpfile=$(mktemp)
trap 'rm -f "$tmpfile"' EXIT
printf '%s\n' "$candidate_files" > "$tmpfile"

diff_text=$(git diff --cached --unified=0 --no-color -- $(cat "$tmpfile") || true)

if [ -z "$diff_text" ]; then
  exit 0
fi

if printf '%s\n' "$diff_text" | grep -E '^\+[^+].*(BEGIN (RSA|EC|OPENSSH|DSA) PRIVATE KEY|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})' >/dev/null; then
  echo ""
  echo "Commit blocked: possible secret material detected in staged changes." >&2
  echo "Remove credentials/keys/tokens before committing." >&2
  exit 1
fi

credential_regex="^\\+[^+].*(api[_-]?key|auth[_-]?pass|secret|token|password)[[:space:]]*[:=][[:space:]]*['\"][^'\"]{6,}['\"]"
if printf '%s\n' "$diff_text" | grep -Ei "$credential_regex" >/dev/null; then
  echo ""
  echo "Commit blocked: likely hardcoded credential detected in staged changes." >&2
  echo "Use environment variables or config references instead." >&2
  exit 1
fi

exit 0
