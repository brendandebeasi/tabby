# Tabby + Tmux - AI Agent Instructions

This file contains tmux/tabby integration instructions for AI coding agents (Claude Code, OpenCode, etc.).
Reference this from your global AGENTS.md or CLAUDE.md.

## Tmux Window Naming with Tabby

Tabby groups tmux windows by name prefix. Within your first few responses, rename the tmux window to reflect the session:

```bash
# Use TMUX_PANE to rename the window where THIS session runs
tmux rename-window -t "$TMUX_PANE" "PREFIX|short-name"
```

**Prefix rules** (based on pwd OR topic being discussed):

- `SD|` - in studiodome-dev, OR working on StudioDome topic
- `GP|` - in gunpowder or any gunpowder subproject, OR working on Gunpowder topic
- No prefix - other directories/topics (goes to Tabby's Default group)

**How to determine prefix:**

1. Check `pwd` for directory context
2. Consider the topic being discussed (overrides pwd if different project)
   - Example: User in ~ asks about StudioDome -> use `SD|`
   - Example: User in gunpowder/msg -> use `GP|`

**When to rename:**

1. After 1st message: rename based on pwd (e.g., `SD|new`, `GP|new`, or `new`)
2. After 3rd-5th message: update with descriptive name once task is clear (e.g., `SD|auth-fix`)

**Guidelines:**

- Use 2-4 words max (e.g., `SD|auth-fix`, `GP|api-debug`, `tmux-config`)
- Use lowercase with hyphens
- `/rename` available for manual renaming

## Tabby Indicator Integration

Tabby shows per-window status indicators. AI agents can set these via hooks:

```bash
# Mark window as busy (agent is working)
~/git/tabby/scripts/set-tabby-indicator.sh busy 1

# Clear busy indicator (agent stopped)
~/git/tabby/scripts/set-tabby-indicator.sh busy 0

# Show input-needed indicator
~/git/tabby/scripts/set-tabby-indicator.sh input 1

# Clear input indicator
~/git/tabby/scripts/set-tabby-indicator.sh input 0
```

These are typically wired into Claude Code hooks (UserPromptSubmit, Stop, Notification) in `~/.claude/settings.json`.

## Restarting the Sidebar After Changes

If you make changes to the tabby codebase, restart the sidebar:

```bash
pkill -f "tabby-daemon"; pkill -f "sidebar-renderer"; rm -f /tmp/tabby-daemon-*
TABBY_USE_RENDERER=1 /Users/b/git/tabby/scripts/toggle_sidebar_daemon.sh
```
