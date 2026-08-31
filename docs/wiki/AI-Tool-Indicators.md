[Home](Home.md) › AI Tool Indicators

# AI Tool Indicators

Tabby marks a window when something in it wants attention: a job finished, a
tool is thinking, a prompt is waiting for an answer. It works out state two
ways.

**Passive detection** needs no configuration. The daemon watches pane titles for
spinner characters and title changes and infers busy or idle from them. It
covers any tool.

**Hook-based detection** is precise. The tool calls
`tabby hook set-indicator` at its own turn boundaries, so the indicator flips
exactly when the state changes rather than when output heuristics catch up.

```
AI tool event -> hook fires -> tabby hook set-indicator -> tmux option set
                                                       -> daemon USR1 signal
                                                       -> sidebar re-renders
```

`tabby hook set-indicator` sets `@tabby_busy`, `@tabby_bell` or `@tabby_input`
on the right window, finding it from `$TMUX_PANE` or by walking the process
tree, then signals the daemon so the sidebar repaints immediately.

## Indicator states

| Call | Meaning | Shows |
|---|---|---|
| `set-indicator busy 1` | Working | Spinner |
| `set-indicator busy 0` | Stopped working | Clears |
| `set-indicator input 1` | Waiting for you | `?` |
| `set-indicator input 0` | You replied | Clears |
| `set-indicator bell 1` | Finished or exited | Diamond |
| `set-indicator bell 0` | Acknowledged | Clears |

Typical mappings:

| Event | Calls |
|---|---|
| Prompt submitted | `busy 1`, `input 0` |
| Response finished | `busy 0`, `input 1` |
| Permission or question | `busy 0`, `input 1` |
| Task complete, want a bell | `busy 0`, `bell 1` |

## Appearance

```yaml
indicators:
  activity:
    enabled: false      # needs tmux monitor-activity on; noisy
    icon: "!"
    color: "#000000"
  bell:
    enabled: true
    icon: "◆"
    color: "#000000"
    bg: "#ffff00"
  silence:
    enabled: true
    icon: "○"
    color: "#000000"
  busy:
    enabled: true
    icon: "◐"
    color: "#ff0000"
    frames: ["◐", "◓", "◑", "◒"]
  input:
    enabled: true
    icon: "?"
    color: "#6a1b9a"
    frames: ["?", "?"]
```

`frames` animates the icon by cycling through the list. The busy default spins a
quarter circle. Two identical frames, as `input` uses, means no animation;
change it to `["?", " "]` to blink.

`activity` is off by default because it needs tmux's `monitor-activity`, which
fires on any output at all and marks half your windows during a build.

## Passive detection

```yaml
busy_detection:
  ai_tools:
    - opencode
    - gemini
    - codex
    - aider
    - cursor
    - copilot
    - grok
    - droid
  idle_timeout: 10
  # extra_idle: []
```

A pane whose command matches one of `ai_tools` gets AI treatment: busy and idle
states, and the live AI tab summary. Add your own tool by process name.

`idle_timeout` is how many seconds of silence turn a busy AI pane into the input
`?`. Raise it if tools that pause to think get marked as waiting for you.

`extra_idle` lists commands that should never be treated as busy. Add long
builds here if the spinner is a distraction:

```yaml
busy_detection:
  extra_idle:
    - make
    - cargo
```

Hooks and passive detection coexist. A hooked tool gets exact transitions, and
still gets the passive treatment before any hook fires.

## Setting an indicator by hand

Any script can do this, not only AI tools:

```bash
long-running-job
~/.tmux/plugins/tabby/bin/tabby hook set-indicator bell 1
```

Or from inside a tmux binding, wrapping something slow:

```bash
tmux bind-key B run-shell 'tabby hook set-indicator busy 1; \
  ~/bin/deploy.sh; tabby hook set-indicator busy 0; \
  tabby hook set-indicator bell 1'
```

Clear an indicator by switching to the window, or with `0`.

## Per-tool hook setup

Claude Code, OpenCode, Grok and Droid configurations are in
[Notifications and Deep Links](Notifications-and-Deep-Links.md), alongside the
notification wiring, because in practice you set both up at once.

`docs/AI_TOOL_HOOKS.md` in this repository has the longer per-tool reference.
