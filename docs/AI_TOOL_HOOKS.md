# AI Tool Hooks -- Tabby Indicator Integration

Tabby detects AI coding tool states two ways:

1. **Passive detection** (automatic) -- The daemon watches pane titles for spinner
   characters and title changes. Works for all tools, no configuration needed.
2. **Hook-based detection** (precise) -- AI tools call `tabby hook set-indicator`
   via their hook/notification systems. Gives instant, accurate state transitions.

This guide covers option 2: configuring each tool's hook system.

## Table of Contents

- [How It Works](#how-it-works)
- [Tool Configurations](#tool-configurations)
- [Quick Setup](#quick-setup)
- [Passive Detection (No Hooks Needed)](#passive-detection-no-hooks-needed)
- [Comparison](#comparison)

---

## How It Works

```
  AI tool event ──> hook fires ──> tabby hook set-indicator ──> tmux option set
                                                            ──> daemon USR1 signal
                                                            ──> sidebar re-renders
```

The helper binary `bin/tabby hook set-indicator` sets tmux window options
(`@tabby_busy`, `@tabby_bell`, `@tabby_input`) and signals the daemon for
instant refresh. It auto-detects which tmux window the tool is running in via
`$TMUX_PANE` or process tree walking.

### Indicator States

| Indicator | Meaning                    | Visual    |
|-----------|----------------------------|-----------|
| `busy 1`  | Tool is working            | Spinner   |
| `busy 0`  | Tool stopped working       | (clears)  |
| `input 1` | Tool needs user input      | ?         |
| `input 0` | User resumed interaction   | (clears)  |
| `bell 1`  | Task completed / exited    | Diamond   |
| `bell 0`  | Acknowledged               | (clears)  |

Common semantic states:
- `working` => `busy 1`
- `question` => `busy 0` then `input 1`
- `done` => `busy 0` then `input 1` (or `bell 1` for completion alert)

### Event Mapping

Every AI tool has slightly different event names, but they map to the same
indicators:

| What happened              | Hook call                                           |
|----------------------------|-----------------------------------------------------|
| User submitted a prompt    | `tabby hook set-indicator busy 1`                   |
| Tool finished responding   | `tabby hook set-indicator busy 0`                   |
|                            | `tabby hook set-indicator input 1`                  |
| Tool exited / session end  | `tabby hook set-indicator bell 1`                   |

---

## Tool Configurations

### Claude Code

**Config**: `~/.claude/settings.json`

Add to the `"hooks"` key:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator busy 1"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator busy 0 && /path/to/tabby/bin/tabby hook set-indicator input 1"
          }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator busy 1"
          }
        ]
      }
    ]
  }
}
```

**Events available**: `SessionStart`, `SessionEnd`, `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `SubagentStart`,
`SubagentStop`, `Stop`, `PreCompact`, `Setup`, `Notification`,
`PermissionRequest`

**Context**: JSON on stdin with `session_id`, `cwd`, `hook_event_name`,
`tool_name`, `tool_input`, etc.

**Exit codes**: 0 = success, 2 = blocking error (stderr shown to Claude)

---

### Gemini CLI

**Config**: `~/.gemini/settings.json`

**IMPORTANT**: Gemini hooks MUST output valid JSON to stdout. Plain text will
cause errors.

```json
{
  "hooks": {
    "BeforeAgent": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator busy 1; echo '{}'",
            "timeout": 5000
          }
        ]
      }
    ],
    "AfterAgent": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator busy 0; /path/to/tabby/bin/tabby hook set-indicator input 1; echo '{}'",
            "timeout": 5000
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/bin/tabby hook set-indicator bell 1; echo '{}'",
            "timeout": 5000
          }
        ]
      }
    ]
  }
}
```

**Events available**: `SessionStart`, `SessionEnd`, `BeforeAgent`, `AfterAgent`,
`BeforeTool`, `AfterTool`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`,
`Notification`, `PreCompress`

**Context**: JSON on stdin with `session_id`, `cwd`, `hook_event_name`, etc.

**Exit codes**: 0 = success, 2 = blocking error

---

### Antigravity CLI (`agy`)

Google's Antigravity CLI (the `agy` binary, Gemini-based, config under
`~/.gemini/antigravity-cli/`) has no `tabby hook`-style lifecycle hooks, but it
has something better for our purposes: a **`statusLine` command** that the TUI
runs **on every agent-state change**, piping a JSON payload to the script's
stdin and rendering the script's stdout in its status bar.

We exploit that invocation to mirror the agent state into the terminal **title**
using the same glyphs Claude Code uses, so tabby's existing passive detection
lights up the busy/input indicator -- no `tabby hook`, no daemon changes, and it
rides over SSH like any other title.

```
  agy agent-state change
        │  (JSON on stdin: agent_state, model, vcs, context_window, …)
        ▼
  tabby-statusline.sh  ── reads agent_state
        │                  "idle"|"" -> ✳   else -> ⠿ (braille)
        ▼
  OSC 0 title -> /dev/tty   (NOT stdout: agy consumes stdout)
        ▼
  tabby passive detection (HasSpinner / HasIdleIcon)
        ▼
  sidebar busy (spinner) / input (?) indicator
```

The `agent_state` mapping mirrors the `agy-hud` plugin's rule: `idle`
(case-insensitive) means settled / your turn; any other non-empty value means
actively working.

**1. Register `agy` as an AI tool** in tabby's `config.yaml` -- its process name
is `agy` (not a semver like Claude Code), so it isn't auto-detected:

```yaml
busy_detection:
  ai_tools:
    - agy
```

**2. Bridge script** at `~/.gemini/antigravity-cli/tabby-statusline.sh`
(`chmod +x`):

```bash
#!/usr/bin/env bash
set -u
payload="$(cat)"
state="" model="" branch=""
if command -v jq >/dev/null 2>&1 && [ -n "$payload" ]; then
  state="$(printf '%s' "$payload"  | jq -r '(.agent_state // "") | ascii_downcase | gsub("\\s";"")' 2>/dev/null)"
  model="$(printf '%s' "$payload"  | jq -r '(.model.display_name // .model.id // "")' 2>/dev/null)"
  branch="$(printf '%s' "$payload" | jq -r '(.vcs.branch // "")' 2>/dev/null)"
fi
case "$state" in
  ""|idle) glyph="✳" ;;   # U+2733 -> tabby INPUT ("?")
  *)       glyph="⠿" ;;   # braille U+28xx -> tabby BUSY (spinner)
esac
label="${model:-agy}"; [ -n "$branch" ] && label="${label}  ${branch}"
# Write to the controlling tty, NOT stdout (agy renders stdout in its bar):
{ printf '\033]0;%s %s\007' "$glyph" "$label" > /dev/tty; } 2>/dev/null || true
printf '%s %s' "$glyph" "$label"
```

**3. Wire it into** `~/.gemini/antigravity-cli/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/absolute/path/to/.gemini/antigravity-cli/tabby-statusline.sh",
    "stack_with_default": true
  }
}
```

`stack_with_default: true` keeps agy's native status bar and stacks our line
above it. Restart any already-running `agy` sessions -- `settings.json` is read
at launch.

**Payload fields** (top-level): `cwd`, `conversation_id`, `model` (`id`,
`display_name`), `workspace` (`current_dir`, `project_dir`), `version`,
`context_window` (`used_percentage`, …), `agent_state`, `vcs` (`type`, `branch`,
`dirty`), `sandbox`, `plan_tier`, `email`, `terminal_width`.

**SSH / remote agy**: install the same bridge + `settings.json` on the remote
host. Over SSH the local pane command is `ssh` (not `agy`), so tabby won't
compute its own indicator -- but the remote-set title still propagates into the
sidebar label, so the ⠿/✳ glyph shows there exactly like a remote Claude Code
session.

**Alternative (explicit hook path)**: instead of setting the title, the bridge
can call `tabby hook set-indicator` directly (`busy 1` when `state` is non-idle,
`busy 0` + `input 1` when idle). That gives a true tmux-option indicator even
over SSH, but requires `tabby` on the remote PATH; the title approach does
not.

---

### OpenAI Codex CLI

**Config**: `~/.codex/config.toml`

Codex has a simpler system -- a `notify` command that receives a JSON payload as
a CLI argument (not stdin) when the agent finishes a turn.

```toml
notify = ["bash", "-c", """
PAYLOAD="$1"
/path/to/tabby/bin/tabby hook set-indicator busy 0
/path/to/tabby/bin/tabby hook set-indicator input 1
""", "--"]
```

Codex has no "start working" event. Pair with passive detection (the daemon
watches for title changes) or add a shell alias:

```bash
# ~/.bashrc or ~/.zshrc
codex() {
    /path/to/tabby/bin/tabby hook set-indicator busy 1
    command codex "$@"
    /path/to/tabby/bin/tabby hook set-indicator bell 1
}
```

**Event**: `agent-turn-complete` (only event currently supported)

**Context**: JSON as first CLI argument with `type`, `thread-id`, `cwd`,
`last-assistant-message`

---

### Aider

**Config**: `.aider.conf.yml` or CLI flags or env vars

Aider fires a single notification when it finishes and waits for user input.

```yaml
# .aider.conf.yml (project root or ~/.aider.conf.yml)
notifications: true
notifications_command: "/path/to/tabby/bin/tabby hook set-indicator input 1"
```

Or via command line:
```bash
aider --notifications --notifications-command "/path/to/tabby/bin/tabby hook set-indicator input 1"
```

Or via environment variable:
```bash
export AIDER_NOTIFICATIONS=true
export AIDER_NOTIFICATIONS_COMMAND="/path/to/tabby/bin/tabby hook set-indicator input 1"
```

Aider has no "start" event. Pair with passive detection or a shell wrapper:

```bash
# ~/.bashrc or ~/.zshrc
aider() {
    /path/to/tabby/bin/tabby hook set-indicator busy 1
    command aider "$@"
    /path/to/tabby/bin/tabby hook set-indicator bell 1
}
```

**Context**: None. The command is executed with no arguments or stdin.

---

### OpenCode

**Config**: `opencode.json` (project root) + `~/.config/opencode/opencode-notifier.json`

OpenCode uses a plugin system. The `opencode-notifier` plugin supports custom
commands.

1. Enable the plugin in `opencode.json`:
```json
{
  "plugin": ["@mohak34/opencode-notifier@latest"]
}
```

2. Configure in `~/.config/opencode/opencode-notifier.json`:
```json
{
  "sound": false,
  "notification": false,
  "command": {
    "enabled": true,
    "path": "/path/to/tabby/bin/tabby",
    "args": ["hook", "set-indicator", "input", "1"],
    "minDuration": 0
  },
  "events": {
    "complete": { "sound": false, "notification": false },
    "permission": { "sound": false, "notification": false },
    "question": { "sound": false, "notification": false },
    "error": { "sound": false, "notification": false }
  }
}
```

Note: The plugin's command feature uses token replacement (`{event}`,
`{message}`) but doesn't directly map to busy/input. For full control, write a
small wrapper:

```bash
#!/bin/bash
# ~/.local/bin/opencode-tabby-hook.sh
EVENT="$1"
case "$EVENT" in
    complete|permission|question)
        /path/to/tabby/bin/tabby hook set-indicator busy 0
        /path/to/tabby/bin/tabby hook set-indicator input 1
        ;;
    error)
        /path/to/tabby/bin/tabby hook set-indicator busy 0
        /path/to/tabby/bin/tabby hook set-indicator bell 1
        ;;
esac
```

Then in the notifier config:
```json
{
  "command": {
    "enabled": true,
    "path": "/path/to/opencode-tabby-hook.sh",
    "args": ["{event}"],
    "minDuration": 0
  }
}
```

**Events**: `complete`, `permission`, `question`, `error`, `subagent_complete`

---

### GitHub Copilot CLI (coding agent)

**Config**: `hooks.json` in project root (no global hooks yet)

```json
{
  "version": 1,
  "hooks": {
    "userPromptSubmitted": [
      {
        "type": "command",
        "bash": "/path/to/tabby/bin/tabby hook set-indicator busy 1"
      }
    ],
    "sessionEnd": [
      {
        "type": "command",
        "bash": "/path/to/tabby/bin/tabby hook set-indicator bell 1"
      }
    ]
  }
}
```

**Events available**: `sessionStart`, `sessionEnd`, `userPromptSubmitted`,
`preToolUse`, `postToolUse`, `errorOccurred`

**Context**: JSON on stdin with timestamp, cwd, tool info

**Limitation**: Project-scoped only. No `~/.config` global hooks yet.

---

## Quick Setup

Replace `/path/to/tabby` with your actual tabby installation path in all configs
above. Typically `~/.tmux/plugins/tabby` or wherever you cloned it.

To verify the script works:

```bash
# Set busy indicator on current window
./bin/tabby hook set-indicator busy 1

# Check it appeared in the sidebar, then clear it
./bin/tabby hook set-indicator busy 0
```

Debug logs are written to `/tmp/tabby-indicator-debug.log`.

Look for skipped updates with:

```bash
rg "CLAUDE_WIN=\(none, skipping\)" /tmp/tabby-indicator-debug.log
```

Recent versions include state-recovery fallback to reduce skipped `busy 0`/`input 1`/`bell 1` transitions.

---

## Passive Detection (No Hooks Needed)

Even without hooks, the daemon detects AI tool states by:

1. **Braille spinner** (U+2801-U+28FF) in pane title -- Claude Code uses these
   while thinking.
2. **Title change between poll cycles** -- Any tool that updates its title while
   working (every 5 seconds).
3. **Process exit** -- When the AI tool command exits, the daemon fires a bell.

The passive system works for all tools automatically. Hooks add precision: instant
state changes instead of 5-second polling, and no false positives from title
changes that aren't work-related.

---

## Comparison

| Tool        | Hook events | Busy start | Busy end | Input | Bell  | Notes                          |
|-------------|-------------|------------|----------|-------|-------|--------------------------------|
| Claude Code | 13          | Yes        | Yes      | Yes   | Yes   | Most comprehensive             |
| Gemini CLI  | 11          | Yes        | Yes      | Yes   | Yes   | Must echo '{}' to stdout       |
| Antigravity (`agy`) | statusLine | Yes | Yes  | Yes   | No    | statusLine->title bridge; add to ai_tools |
| Codex CLI   | 1           | No*        | Yes      | Yes   | No*   | Use shell wrapper for start    |
| Aider       | 1           | No*        | N/A      | Yes   | No*   | Use shell wrapper for start    |
| OpenCode    | 5+          | No*        | Yes      | Yes   | Yes   | Plugin system, needs wrapper   |
| Copilot CLI | 6           | Yes        | No       | No    | Yes   | Project-scoped only            |

*Use shell alias/wrapper or rely on passive detection.
