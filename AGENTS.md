# AGENTS.md - Claude Code Guide

## Project Overview

**Tabby** is a modern tmux tab bar plugin written in Go with shell scripts for tmux integration. It provides both horizontal (status bar) and vertical (sidebar) modes with tab grouping, theming, and mouse support.

## Architecture

```
tabby/
├── cmd/                    # Go binaries
│   ├── render-status/      # Renders individual tabs for horizontal status bar
│   ├── render-tab/         # Tab rendering helper
│   ├── sidebar-renderer/   # BubbleTea renderer app for vertical sidebar panes
│   ├── tabby-daemon/       # Central daemon coordinating state + render payloads
│   └── tabbar/             # Horizontal tabbar TUI (alternative to status bar)
├── pkg/                    # Shared Go packages
│   ├── config/             # YAML config loading (config.yaml)
│   ├── grouping/           # Window grouping logic and color utilities
│   └── tmux/               # Tmux command wrappers (list windows, panes, etc.)
├── scripts/                # Shell scripts for tmux integration
│   ├── toggle_sidebar.sh   # Toggle vertical sidebar on/off
│   ├── ensure_sidebar.sh   # Add sidebar to windows that don't have one
│   ├── switch_to_*.sh      # Mode switching (vertical/horizontal)
│   └── cleanup_*.sh        # Cleanup orphan panes
├── config.yaml             # User configuration (groups, themes, indicators)
└── tabby.tmux              # Plugin entry point (sets hooks, keybindings)
```

## Key Concepts

### Display Modes
- **Vertical sidebar** (`enabled`): Full TUI sidebar on left side of each window
- **Horizontal tabbar** (`horizontal`): Pane at top of each window
- **Disabled**: No custom UI, uses tmux's built-in status bar

### Window Grouping
Windows are grouped by name patterns (regex). Each group has a theme (colors, icon). Windows display in **strict index order** (0, 1, 2...) with group headers appearing inline when the group changes.

### State Management
- State stored in tmux option `@tabby_sidebar` and file `/tmp/tabby-sidebar-${SESSION_ID}.state`
- The tmux option is the source of truth for toggle behavior

## Building

```bash
# Build all binaries
go build -o bin/sidebar-renderer ./cmd/sidebar-renderer/
go build -o bin/tabby-daemon ./cmd/tabby-daemon/
go build -o bin/tabbar ./cmd/tabbar/
go build -o bin/render-status ./cmd/render-status/
go build -o bin/render-tab ./cmd/render-tab/

# Or use the install script
./scripts/install.sh
```

## Key Files to Understand

| File | Purpose |
|------|---------|
| `cmd/sidebar-renderer/main.go` | Sidebar renderer TUI - handles View(), Update(), mouse/keyboard |
| `cmd/tabby-daemon/coordinator.go` | Central state coordinator and render logic |
| `pkg/grouping/grouper.go` | Groups windows by pattern, provides color utilities |
| `pkg/tmux/tmux.go` | Wraps tmux commands to list windows/panes |
| `scripts/toggle_sidebar.sh` | Toggles sidebar on/off using state |
| `tabby.tmux` | Sets up tmux hooks, keybindings, options |
| `config.yaml` | User-editable configuration |

## Common Patterns

### Adding a new feature to sidebar
1. Update `rendererModel` in `cmd/sidebar-renderer/main.go` if new state needed
2. Handle in `Update()` for keyboard/mouse events
3. Render in `View()` using lipgloss styles
4. Update `buildWindowRefs()` if display layout changes (for click targets)

### Modifying window display order
- `View()` iterates `m.windows` sorted by index
- `buildWindowRefs()` must match View() exactly for click targets to work
- Both use `findWindowGroup()` to determine group headers

### Shell script conventions
- Use `set -eu` for error handling
- Get session ID: `SESSION_ID=$(tmux display-message -p '#{session_id}')`
- State file: `/tmp/tabby-sidebar-${SESSION_ID}.state`
- Use process substitution `< <(cmd)` instead of `cmd | while` to avoid subshell issues

## Testing

```bash
# Manual testing - reload the plugin
tmux run-shell ~/.tmux/plugins/tabby/tabby.tmux

# Toggle sidebar
# prefix + Tab

# Check sidebar state
tmux show-options -v @tabby_sidebar
```

## Common Issues

1. **Sidebar disappears**: Check state with `tmux show-options -v @tabby_sidebar`
2. **Click targets wrong**: Ensure `buildWindowRefs()` matches `View()` display order
3. **Windows out of order**: Check sorting in both `View()` and `buildWindowRefs()`
4. **ANSI codes visible**: Use `lipgloss.Width()` for ANSI-aware string measurement

## Dependencies

- Go 1.21+
- [BubbleTea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Styling
- [fsnotify](https://github.com/fsnotify/fsnotify) - Config file watching
- tmux 3.2+ (for Unicode support)
