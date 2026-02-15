# AI Tool Hooks -- Tabby Indicator Integration

Tabby detects AI coding tool states two ways:

1. **Passive detection** (automatic) -- The daemon watches pane titles for spinner
   characters and title changes. Works for all tools, no configuration needed.
2. **Hook-based detection** (precise) -- AI tools call `set-tabby-indicator.sh`
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
  AI tool event ──> hook fires ──> set-tabby-indicator.sh ──> tmux option set
                                                          ──> daemon USR1 signal
                                                          ──> sidebar re-renders
```

The helper script `scripts/set-tabby-indicator.sh` sets tmux window options
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

| What happened              | Hook call                                      |
|----------------------------|-------------------------------------------------|
| User submitted a prompt    | `set-tabby-indicator.sh busy 1`                |
| Tool finished responding   | `set-tabby-indicator.sh busy 0`                |
|                            | `set-tabby-indicator.sh input 1`               |
| Tool exited / session end  | `set-tabby-indicator.sh bell 1`                |

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
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 1"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 0 && /path/to/tabby/scripts/set-tabby-indicator.sh input 1"
          }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 1"
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
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 1; echo '{}'",
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
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 0; /path/to/tabby/scripts/set-tabby-indicator.sh input 1; echo '{}'",
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
            "command": "/path/to/tabby/scripts/set-tabby-indicator.sh bell 1; echo '{}'",
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

### OpenAI Codex CLI

**Config**: `~/.codex/config.toml`

Codex has a simpler system -- a `notify` command that receives a JSON payload as
a CLI argument (not stdin) when the agent finishes a turn.

```toml
notify = ["bash", "-c", """
PAYLOAD="$1"
/path/to/tabby/scripts/set-tabby-indicator.sh busy 0
/path/to/tabby/scripts/set-tabby-indicator.sh input 1
""", "--"]
```

Codex has no "start working" event. Pair with passive detection (the daemon
watches for title changes) or add a shell alias:

```bash
# ~/.bashrc or ~/.zshrc
codex() {
    /path/to/tabby/scripts/set-tabby-indicator.sh busy 1
    command codex "$@"
    /path/to/tabby/scripts/set-tabby-indicator.sh bell 1
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
notifications_command: "/path/to/tabby/scripts/set-tabby-indicator.sh input 1"
```

Or via command line:
```bash
aider --notifications --notifications-command "/path/to/tabby/scripts/set-tabby-indicator.sh input 1"
```

Or via environment variable:
```bash
export AIDER_NOTIFICATIONS=true
export AIDER_NOTIFICATIONS_COMMAND="/path/to/tabby/scripts/set-tabby-indicator.sh input 1"
```

Aider has no "start" event. Pair with passive detection or a shell wrapper:

```bash
# ~/.bashrc or ~/.zshrc
aider() {
    /path/to/tabby/scripts/set-tabby-indicator.sh busy 1
    command aider "$@"
    /path/to/tabby/scripts/set-tabby-indicator.sh bell 1
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
    "path": "/path/to/tabby/scripts/set-tabby-indicator.sh",
    "args": ["input", "1"],
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
        /path/to/tabby/scripts/set-tabby-indicator.sh busy 0
        /path/to/tabby/scripts/set-tabby-indicator.sh input 1
        ;;
    error)
        /path/to/tabby/scripts/set-tabby-indicator.sh busy 0
        /path/to/tabby/scripts/set-tabby-indicator.sh bell 1
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
        "bash": "/path/to/tabby/scripts/set-tabby-indicator.sh busy 1"
      }
    ],
    "sessionEnd": [
      {
        "type": "command",
        "bash": "/path/to/tabby/scripts/set-tabby-indicator.sh bell 1"
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
./scripts/set-tabby-indicator.sh busy 1

# Check it appeared in the sidebar, then clear it
./scripts/set-tabby-indicator.sh busy 0
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
| Codex CLI   | 1           | No*        | Yes      | Yes   | No*   | Use shell wrapper for start    |
| Aider       | 1           | No*        | N/A      | Yes   | No*   | Use shell wrapper for start    |
| OpenCode    | 5+          | No*        | Yes      | Yes   | Yes   | Plugin system, needs wrapper   |
| Copilot CLI | 6           | Yes        | No       | No    | Yes   | Project-scoped only            |

*Use shell alias/wrapper or rely on passive detection.
