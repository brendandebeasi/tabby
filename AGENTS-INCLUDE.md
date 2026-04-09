# Tabby + Tmux - AI Agent Instructions

This file contains tmux/tabby integration instructions for AI coding agents (Claude Code, OpenCode, etc.).
Reference this from your global AGENTS.md or CLAUDE.md.

## Tmux Window Naming with Tabby

Tabby groups tmux windows by name prefix. Within your first few responses, rename the tmux window to reflect the session:

```bash
# Use TMUX_PANE to rename the window where THIS session runs
tmux rename-window -t "$TMUX_PANE" "PREFIX|short-name"
```

**Naming rules** (based on pwd OR topic being discussed):

- Do not prepend project prefixes (for example `SD|` or `GP|`) to window names.
- Use plain, descriptive names by default (for example `auth-fix`, `api-debug`).
- Only add hierarchy when it is needed to disambiguate multiple projects under the same prefix bucket.

**How to determine hierarchy usage:**

1. Check `pwd` for directory context
2. Consider the topic being discussed (overrides pwd if different project)
3. If only one project exists in that prefix bucket, do not use hierarchy
4. If more than one project exists in that prefix bucket, use hierarchy to disambiguate

**When to rename:**

1. After 1st message: rename based on task with a simple name (e.g., `new`)
2. After 3rd-5th message: update with descriptive name once task is clear (e.g., `auth-fix`)

**Guidelines:**

- Use 2-4 words max (e.g., `auth-fix`, `api-debug`, `tmux-config`)
- Use lowercase with hyphens
- `/rename` available for manual renaming

## Tabby Indicator Integration

Tabby shows per-window status indicators. AI agents can set these via hooks:

```bash
# Mark window as busy (agent is working)
~/git/tabby/bin/tabby-hook set-indicator busy 1

# Clear busy indicator (agent stopped)
~/git/tabby/bin/tabby-hook set-indicator busy 0

# Show input-needed indicator
~/git/tabby/bin/tabby-hook set-indicator input 1

# Clear input indicator
~/git/tabby/bin/tabby-hook set-indicator input 0
```

These are typically wired into Claude Code hooks (UserPromptSubmit, Stop, Notification) in `~/.claude/settings.json`.

## Restarting the Sidebar After Changes

If you make changes to the tabby codebase, restart the sidebar:

```bash
pkill -f "tabby-daemon"; pkill -f "sidebar-renderer"; rm -f /tmp/tabby-daemon-*
TABBY_USE_RENDERER=1 /Users/b/git/tabby/scripts/toggle_sidebar_daemon.sh
```
