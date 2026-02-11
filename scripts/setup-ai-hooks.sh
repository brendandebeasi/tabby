#!/usr/bin/env bash
# setup-ai-hooks.sh -- Configure AI coding tools to send tabby indicators
#
# Usage: ./setup-ai-hooks.sh [--dry-run] [--tool TOOL]
#
# Configures hooks for: claude, gemini, codex, aider, opencode
# Each tool calls set-tabby-indicator.sh on state transitions:
#   UserPromptSubmit / BeforeAgent  -->  busy 1
#   Stop / AfterAgent              -->  busy 0, input 1
#   SessionEnd / exit              -->  bell 1

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)"
TABBY_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P)"
INDICATOR="$TABBY_DIR/scripts/set-tabby-indicator.sh"

DRY_RUN=false
TOOL_FILTER=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        --tool)    TOOL_FILTER="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: $0 [--dry-run] [--tool claude|gemini|codex|aider|opencode]"
            exit 0 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

should_run() {
    [[ -z "$TOOL_FILTER" || "$TOOL_FILTER" == "$1" ]]
}

backup_file() {
    local f="$1"
    if [[ -f "$f" ]]; then
        cp "$f" "${f}.tabby-backup-$(date +%Y%m%d%H%M%S)"
        echo "  Backed up $f"
    fi
}

info()  { echo "[*] $*"; }
ok()    { echo "[+] $*"; }
skip()  { echo "[-] $*"; }
warn()  { echo "[!] $*"; }

# ─────────────────────────────────────────────────────────────────────────────
# Claude Code -- ~/.claude/settings.json
# ─────────────────────────────────────────────────────────────────────────────
setup_claude() {
    local config="$HOME/.claude/settings.json"
    info "Claude Code: $config"

    if ! command -v claude &>/dev/null; then
        skip "  claude not found, skipping"
        return
    fi

    # Check if hooks already reference set-tabby-indicator
    if [[ -f "$config" ]] && grep -q "set-tabby-indicator" "$config" 2>/dev/null; then
        ok "  Already configured (found set-tabby-indicator in hooks)"
        return
    fi

    if [[ "$DRY_RUN" == true ]]; then
        info "  [dry-run] Would add UserPromptSubmit, Stop, Notification hooks"
        return
    fi

    backup_file "$config"

    # Use python3 to merge hooks into existing settings.json
    python3 -c "
import json, sys

config_path = '$config'
indicator = '$INDICATOR'

try:
    with open(config_path) as f:
        cfg = json.load(f)
except (FileNotFoundError, json.JSONDecodeError):
    cfg = {}

hooks = cfg.setdefault('hooks', {})

hooks['UserPromptSubmit'] = [{
    'matcher': '',
    'hooks': [
        {'type': 'command', 'command': f'{indicator} busy 1'},
        {'type': 'command', 'command': f'{indicator} input 0'}
    ]
}]

hooks['Stop'] = [{
    'matcher': '',
    'hooks': [
        {'type': 'command', 'command': f'{indicator} busy 0'},
        {'type': 'command', 'command': f'{indicator} input 1'}
    ]
}]

hooks['Notification'] = [{
    'matcher': 'tool_use:AskUserQuestion',
    'hooks': [
        {'type': 'command', 'command': f'{indicator} input 1'}
    ]
}]

with open(config_path, 'w') as f:
    json.dump(cfg, f, indent=2)
    f.write('\n')

print('  Updated hooks: UserPromptSubmit, Stop, Notification')
"
    ok "  Claude Code configured"
}

# ─────────────────────────────────────────────────────────────────────────────
# Gemini CLI -- ~/.gemini/settings.json
# IMPORTANT: Gemini hooks MUST output valid JSON to stdout
# ─────────────────────────────────────────────────────────────────────────────
setup_gemini() {
    local config="$HOME/.gemini/settings.json"
    info "Gemini CLI: $config"

    if ! command -v gemini &>/dev/null; then
        skip "  gemini not found, skipping"
        return
    fi

    if [[ -f "$config" ]] && grep -q "set-tabby-indicator" "$config" 2>/dev/null; then
        ok "  Already configured (found set-tabby-indicator in hooks)"
        return
    fi

    if [[ "$DRY_RUN" == true ]]; then
        info "  [dry-run] Would add BeforeAgent, AfterAgent, SessionEnd hooks"
        return
    fi

    backup_file "$config"

    python3 -c "
import json

config_path = '$config'
indicator = '$INDICATOR'

try:
    with open(config_path) as f:
        cfg = json.load(f)
except (FileNotFoundError, json.JSONDecodeError):
    cfg = {}

# Gemini hooks must echo '{}' to stdout (JSON required)
hooks = cfg.setdefault('hooks', {})

hooks['BeforeAgent'] = [{
    'hooks': [{
        'type': 'command',
        'command': f'{indicator} busy 1; echo \"{{}}\"',
        'timeout': 5000
    }]
}]

hooks['AfterAgent'] = [{
    'hooks': [{
        'type': 'command',
        'command': f'{indicator} busy 0; {indicator} input 1; echo \"{{}}\"',
        'timeout': 5000
    }]
}]

hooks['SessionEnd'] = [{
    'hooks': [{
        'type': 'command',
        'command': f'{indicator} bell 1; echo \"{{}}\"',
        'timeout': 5000
    }]
}]

with open(config_path, 'w') as f:
    json.dump(cfg, f, indent=2)
    f.write('\n')

print('  Updated hooks: BeforeAgent, AfterAgent, SessionEnd')
"
    ok "  Gemini CLI configured"
}

# ─────────────────────────────────────────────────────────────────────────────
# OpenAI Codex CLI -- ~/.codex/config.toml
# Only has agent-turn-complete event; no "start" event
# ─────────────────────────────────────────────────────────────────────────────
setup_codex() {
    local config="$HOME/.codex/config.toml"
    info "Codex CLI: $config"

    if ! command -v codex &>/dev/null; then
        skip "  codex not found, skipping"
        return
    fi

    if [[ -f "$config" ]] && grep -q "set-tabby-indicator" "$config" 2>/dev/null; then
        ok "  Already configured (found set-tabby-indicator in config)"
        return
    fi

    if [[ "$DRY_RUN" == true ]]; then
        info "  [dry-run] Would add notify command"
        return
    fi

    backup_file "$config"

    # Append notify line if not present (preserves existing TOML)
    if ! grep -q '^\s*notify\s*=' "$config" 2>/dev/null; then
        # Add notify at the top of file, before any [section] headers
        python3 -c "
import re

config_path = '$config'
indicator = '$INDICATOR'

with open(config_path) as f:
    content = f.read()

notify_line = f'notify = [\"bash\", \"-c\", \"{indicator} busy 0; {indicator} input 1\"]'

# Insert before the first [section] or at the top
match = re.search(r'^\[', content, re.MULTILINE)
if match:
    content = content[:match.start()] + notify_line + '\n' + content[match.start():]
else:
    content = notify_line + '\n' + content

with open(config_path, 'w') as f:
    f.write(content)

print('  Added notify command for agent-turn-complete')
"
    else
        warn "  notify already set in config, not overwriting"
    fi

    ok "  Codex CLI configured"
}

# ─────────────────────────────────────────────────────────────────────────────
# Aider -- ~/.aider.conf.yml
# Single notification event when aider waits for input
# ─────────────────────────────────────────────────────────────────────────────
setup_aider() {
    local config="$HOME/.aider.conf.yml"
    info "Aider: $config"

    if ! command -v aider &>/dev/null; then
        skip "  aider not found, skipping"
        return
    fi

    if [[ -f "$config" ]] && grep -q "set-tabby-indicator" "$config" 2>/dev/null; then
        ok "  Already configured (found set-tabby-indicator in config)"
        return
    fi

    if [[ "$DRY_RUN" == true ]]; then
        info "  [dry-run] Would add notifications_command"
        return
    fi

    if [[ -f "$config" ]]; then
        backup_file "$config"
    fi

    # Append notification settings (YAML)
    {
        echo ""
        echo "# Tabby sidebar integration"
        echo "notifications: true"
        echo "notifications_command: \"$INDICATOR input 1\""
    } >> "$config"

    ok "  Aider configured (notifications_command added to $config)"
}

# ─────────────────────────────────────────────────────────────────────────────
# OpenCode -- plugin-based, needs a wrapper script
# ─────────────────────────────────────────────────────────────────────────────
setup_opencode() {
    local config="$HOME/.config/opencode/opencode.json"
    info "OpenCode: $config"

    if ! command -v opencode &>/dev/null; then
        skip "  opencode not found, skipping"
        return
    fi

    # Create the wrapper script for opencode-notifier
    local wrapper="$TABBY_DIR/scripts/opencode-tabby-hook.sh"
    if [[ "$DRY_RUN" == true ]]; then
        info "  [dry-run] Would create $wrapper and configure opencode-notifier"
        return
    fi

    cat > "$wrapper" << 'HOOKEOF'
#!/usr/bin/env bash
# opencode-tabby-hook.sh -- Bridge opencode-notifier events to tabby indicators
EVENT="${1:-}"
INDICATOR="__INDICATOR__"
case "$EVENT" in
    complete|permission|question|subagent_complete)
        "$INDICATOR" busy 0
        "$INDICATOR" input 1
        ;;
    error)
        "$INDICATOR" busy 0
        "$INDICATOR" bell 1
        ;;
esac
HOOKEOF
    sed -i '' "s|__INDICATOR__|$INDICATOR|g" "$wrapper"
    chmod +x "$wrapper"
    echo "  Created $wrapper"

    # Ensure opencode-notifier plugin is present in opencode.json
    python3 -c "
import json
from pathlib import Path

config_path = Path('$config')
plugin_name = '@mohak34/opencode-notifier@latest'

try:
    cfg = json.loads(config_path.read_text())
except Exception:
    cfg = {}

plugins = cfg.get('plugin', [])
if not isinstance(plugins, list):
    plugins = []

if plugin_name not in plugins:
    plugins.append(plugin_name)
    cfg['plugin'] = plugins
    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(json.dumps(cfg, indent=2) + '\n')
    print('  Added opencode-notifier plugin to opencode.json')
else:
    print('  opencode-notifier plugin already in opencode.json')
"

    # Create or update notifier config
    local notifier_config="$HOME/.config/opencode/opencode-notifier.json"
    if [[ -f "$notifier_config" ]] && grep -q "set-tabby-indicator\|opencode-tabby-hook" "$notifier_config" 2>/dev/null; then
        ok "  opencode-notifier already configured for tabby"
        return
    fi

    if [[ -f "$notifier_config" ]]; then
        backup_file "$notifier_config"
    fi

    cat > "$notifier_config" << NOTIFIEREOF
{
  "sound": false,
  "notification": false,
  "command": {
    "enabled": true,
    "path": "$wrapper",
    "args": ["{event}"],
    "minDuration": 0
  },
  "events": {
    "permission": { "sound": false, "notification": false },
    "complete": { "sound": false, "notification": false },
    "subagent_complete": { "sound": false, "notification": false },
    "error": { "sound": false, "notification": false },
    "question": { "sound": false, "notification": false }
  }
}
NOTIFIEREOF
    echo "  Created $notifier_config"
    ok "  OpenCode configured"
}

# ─────────────────────────────────────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────────────────────────────────────
echo "Tabby AI Hook Setup"
echo "Indicator script: $INDICATOR"
echo "===================="
echo ""

should_run "claude"   && setup_claude
should_run "gemini"   && setup_gemini
should_run "codex"    && setup_codex
should_run "aider"    && setup_aider
should_run "opencode" && setup_opencode

echo ""
echo "Done. Restart your AI tools to pick up the new hooks."
echo "Debug log: /tmp/tabby-indicator-debug.log"
