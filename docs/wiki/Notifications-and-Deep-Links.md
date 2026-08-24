[Home](Home.md) › Notifications and Deep Links

# Notifications and Deep Links

A deep link is a notification that, when clicked, raises your terminal and
selects the exact tmux session, window and pane that sent it. The payload is a
single command:

```bash
tabby hook focus-pane main:2.1
```

## Setup

Install a notifier:

```bash
brew install growlrrr          # supports custom emoji icons as thumbnails
brew install terminal-notifier # simpler alternative
```

Tell Tabby which application to raise:

```yaml
terminal_app: Ghostty
```

Accepted values: `Ghostty`, `iTerm`, `Terminal`, `Alacritty`, `kitty`,
`WezTerm`.

## focus-pane targets

```bash
tabby hook focus-pane 2        # window 2, pane 0 in the current session
tabby hook focus-pane 1.2      # window 1, pane 2
tabby hook focus-pane main:2.1 # session main, window 2, pane 1
```

## Sending one

```bash
terminal-notifier -title "Build complete" -message "Click to view" \
  -execute "$HOME/.tmux/plugins/tabby/bin/tabby hook focus-pane 2"
```

In a script, capture the target rather than hardcoding it:

```bash
TARGET=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}')
terminal-notifier -title "Task done" -message "Click to return" \
  -execute "$HOME/.tmux/plugins/tabby/bin/tabby hook focus-pane $TARGET"
```

## Capturing the right pane from a hook

This is the part that bites people. Hooks from tools like Claude Code run as
subprocesses, potentially minutes after you last touched the keyboard. By then
the focused pane may not be the pane the tool is running in.

`tmux display-message -p` returns the *currently focused* pane. What you want is
the pane the hook came from, which is in `$TMUX_PANE`:

```bash
tmux display-message -t "$TMUX_PANE" -p '#{session_name}:#{window_index}.#{pane_index}'
```

The `-t "$TMUX_PANE"` is the whole trick. Without it, your notification will
sometimes jump to the wrong window, and only when you happened to switch away
while the job ran, which makes it maddening to reproduce.

## Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator busy 1" },
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator input 0" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "/path/to/claude-stop-notify.sh" }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "/path/to/claude-notification.sh" }
        ]
      }
    ]
  }
}
```

Replace `<tabby-dir>` with your install path, usually
`~/.tmux/plugins/tabby`.

A notify script that reads the transcript, picks up the originating pane, and
sets the sidebar indicators:

```bash
#!/usr/bin/env bash
# claude-stop-notify.sh
set -u

TABBY_DIR="${HOME}/.tmux/plugins/tabby"
INDICATOR="$TABBY_DIR/bin/tabby hook set-indicator"

HOOK_JSON=$(cat)
TRANSCRIPT_PATH=$(echo "$HOOK_JSON" | jq -r '.transcript_path // empty')

if [[ -n "${TMUX:-}" && -n "${TMUX_PANE:-}" ]]; then
    WINDOW_NAME=$(tmux display-message -t "$TMUX_PANE" -p '#W')
    TMUX_TARGET=$(tmux display-message -t "$TMUX_PANE" -p \
        '#{session_name}:#{window_index}.#{pane_index}')
fi

MESSAGE="Session complete"
if [[ -n "$TRANSCRIPT_PATH" && -f "$TRANSCRIPT_PATH" ]]; then
    LAST_MSG=$(tac "$TRANSCRIPT_PATH" | grep -m1 '"type":"assistant"' | jq -r '
        .message.content |
        if type == "array" then
            [.[] | select(.type == "text") | .text] | join(" ")
        elif type == "string" then .
        else empty end
    ' 2>/dev/null)
    [[ -n "$LAST_MSG" && "$LAST_MSG" != "null" ]] && \
        MESSAGE=$(echo "$LAST_MSG" | tr '\n' ' ' | sed 's/  */ /g' | cut -c1-300)
fi

if command -v growlrrr &>/dev/null; then
    growlrrr send --appId ClaudeCode --title "$WINDOW_NAME" \
        --subtitle "Task complete" --sound default \
        --execute "$TABBY_DIR/bin/tabby hook focus-pane $TMUX_TARGET" \
        "$MESSAGE" &>/dev/null &
elif command -v terminal-notifier &>/dev/null; then
    terminal-notifier -title "$WINDOW_NAME" -message "$MESSAGE" \
        -sound default \
        -execute "$TABBY_DIR/bin/tabby hook focus-pane $TMUX_TARGET" &>/dev/null &
fi

"$INDICATOR" busy 0
"$INDICATOR" bell 1
```

## OpenCode

Tabby ships a hook at `scripts/opencode-tabby-hook.sh` that handles every
OpenCode event, extracts the message from OpenCode's sqlite database, walks the
process tree to find the right `TMUX_PANE`, and sets the sidebar indicators.

Create `~/.config/opencode/opencode-notifier.json`:

```json
{
  "sound": false,
  "notification": false,
  "command": {
    "enabled": true,
    "path": "<tabby-dir>/scripts/opencode-tabby-hook.sh",
    "args": ["{event}", "{projectName}", "{sessionTitle}", "{message}"],
    "minDuration": 0
  },
  "events": {
    "complete": { "sound": false, "notification": false },
    "permission": { "sound": false, "notification": false },
    "error": { "sound": false, "notification": false }
  }
}
```

Leave `sound` and `notification` false. The hook script sends the notification
itself, and leaving OpenCode's own enabled gives you two for every event.

Add `"{sessionID}"` as a fifth argument if your notifier build exposes it. The
hook uses it to read the right transcript; without it, it identifies the
session by matching the firing pane's working directory against the directory
each OpenCode session records, and falls back to the session title.

### Where the notification body comes from

In order of preference:

1. `{message}` — whatever OpenCode passed for this event.
2. The prose in the **newest** assistant message of the resolved session.
3. A fixed per-event string ("Task complete", "Needs input", "Error occurred").

Step 2 is deliberately restricted to the newest message. Most assistant
messages in a working session are tool calls carrying no prose, so a search
that walks back through the session for the last message that *did* have some
will happily return an answer from several turns ago — which is what used to
make a permission prompt quote a summary the user had already read. When the
newest message is a tool call, there is nothing current to say and the hook
uses the fixed string instead.

If no session can be matched to the firing pane, the hook still deep-links to
the most recently updated session but refuses to quote it, because that
transcript may belong to a different agent. `/tmp/tabby-opencode-hook.log`
records which path was taken as `session=<source>/<id> body=<origin>`.

## Grok CLI

xAI's Grok Build ships a Claude-compatible hooks system, so the wiring is
identical. The file is `~/.grok/user-settings.json` instead:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator busy 1" },
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator input 0" }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator busy 0" },
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator input 1" }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          { "type": "command", "command": "<tabby-dir>/bin/tabby hook set-indicator bell 1" }
        ]
      }
    ]
  }
}
```

Point `Stop` and `Notification` at the same notify script you use for Claude
Code; the stdin contract and the `TMUX_PANE` rule are the same.

## Making notifications persist

macOS banners vanish after about five seconds, which is no use for a deep link
you were not watching for. In System Settings → Notifications, find
`terminal-notifier` or `growlrrr` and change the style from Banners to Alerts.

## Avoiding duplicates

With your own hooks in place, turn the tool's built-in notifications off.

Claude Code, in `~/.claude/settings.json`:

```json
{ "preferredNotifChannel": "none" }
```

OpenCode: the `sound` and `notification` false settings shown above.

## Related

Indicator semantics and passive detection are in
[AI Tool Indicators](AI-Tool-Indicators.md).
