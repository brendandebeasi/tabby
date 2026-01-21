# Tabby

A friendly cat watching over your tmux tabs.

Modern, groupable tab bar plugin for tmux with horizontal and vertical modes. Features tab overflow handling, visual indicators, and comprehensive keyboard shortcuts.

## Features

- **Horizontal tab bar** with overflow scrolling
- **Vertical sidebar** with persistent state across windows
- **Tab grouping** with customizable themes and icons
- **Automatic window naming** - shows running command (ssh, vim, etc.), locks on manual rename
- **Strict index ordering** - windows always display 0, 1, 2... from top to bottom
- **Window indicators** for activity, bell, and silence
- **Keyboard navigation** with intuitive shortcuts
- **Mouse support** - click to switch, right-click context menu, middle-click close
- **Custom tab colors** - set per-window colors via context menu
- **Auto-renumbering** - windows renumber when closed or on mode switch

## Installation

### Via TPM (Tmux Plugin Manager)
Add to your `~/.tmux.conf`:
```bash
set -g @plugin 'brendandebeasi/tabby'
set -g @tmux_tabs_test 1  # Currently gated - required for activation
```

Then reload tmux and install:
```bash
tmux source ~/.tmux.conf
# Press prefix + I to install plugins
```

### Manual Installation
```bash
git clone https://github.com/brendandebeasi/tabby ~/.tmux/plugins/tabby
cd ~/.tmux/plugins/tabby
./scripts/install.sh
```

Add to your `~/.tmux.conf`:
```bash
run-shell ~/.tmux/plugins/tabby/tabby.tmux
set -g @tmux_tabs_test 1  # Currently gated - required for activation
```

## Usage

### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `prefix + Tab` | Toggle vertical sidebar |
| `Alt + h` | Previous window |
| `Alt + l` | Next window |
| `Alt + n` | Create new window |
| `Alt + x` | Kill current pane |
| `Alt + q` | Display pane numbers |
| `Alt + 1-9,0` | Switch to window by number |

### Mouse Support (Vertical Sidebar)

- **Left click**: Switch to window/pane
- **Middle click**: Close window (with confirmation)
- **Right click**: Context menu with options:
  - Rename (preserves group prefix)
  - Auto-name (re-enable automatic naming)
  - Move to Group (apply group prefix)
  - Set Tab Color (custom colors)
  - Split Horizontal/Vertical
  - Kill window

Note: Horizontal tabs have limited mouse support due to tmux status bar limitations.

## Configuration

Edit `~/.tmux/plugins/tabby/config.yaml`:

```yaml
# Tab bar position: top, bottom, or off
position: top

# Tab grouping rules
grouping:
  enabled: true
  rules:
    - name: "StudioDome"
      pattern: "SD|*"
      theme:
        bg: "#e74c3c"
        fg: "#ffffff"
        active_bg: "colour203"
        active_fg: "#ffffff"
        icon: ""

    - name: "Gunpowder"  
      pattern: "GP|*"
      theme:
        bg: "#7f8c8d"
        fg: "#ffffff"
        active_bg: "colour60"
        active_fg: "#ffffff"
        icon: "🔫"

    - name: "Default"
      pattern: "*"
      theme:
        bg: "#3498db"
        fg: "#ffffff"
        active_bg: "colour38"
        active_fg: "#ffffff"
        icon: ""

# Indicators
indicators:
  activity: true   # Show 🔔 for windows with activity
  bell: true       # Show ● for bell alerts
  silence: true    # Show 🔇 for silent windows

# Tab overflow behavior
overflow:
  mode: scroll     # scroll or truncate
  indicator: "›"   # Character shown when tabs overflow

# Vertical sidebar settings
sidebar:
  width: 25
  position: left
```

## Tab Grouping

Windows are automatically grouped based on their names:
- Names starting with `SD|` → StudioDome group (red)
- Names starting with `GP|` → Gunpowder group (gray with 🔫 icon)
- All others → Default group (blue)

Example window names:
- `SD|frontend` - StudioDome frontend window
- `GP|MSG|service` - Gunpowder MSG service
- `my-project` - Default group

## Tab Overflow

When you have many windows, tabs automatically scroll to keep the active window visible:
- `›` indicator shows hidden tabs on left/right
- Active window is centered when possible
- Smooth scrolling as you switch windows

## Development

### Building from Source
```bash
cd ~/.tmux/plugins/tabby
./scripts/install.sh
```

### Running Tests
```bash
# Comprehensive visual tests
./tests/e2e/test_visual_comprehensive.sh

# Tab stability tests  
./tests/e2e/test_tab_stability.sh

# Edge case tests
./tests/e2e/test_edge_cases.sh
```

### Project Structure
```
tabby/
├── cmd/
│   ├── render-status/   # Horizontal tab rendering
│   ├── sidebar/         # Vertical sidebar app
│   └── tabbar/          # Horizontal tabbar TUI
├── pkg/
│   ├── config/         # Configuration loading
│   ├── grouping/       # Tab grouping logic
│   └── tmux/           # Tmux integration
├── scripts/
│   ├── install.sh      # Build and install
│   ├── toggle_sidebar.sh
│   └── ensure_sidebar.sh
├── tests/              # Test suites
├── config.yaml         # User configuration
└── tabby.tmux          # Plugin entry point
```

## macOS Notifications with Deep Links

Tabby includes a helper script for creating notifications that deep-link back to specific tmux windows/panes. When clicked, the notification brings your terminal to the foreground and navigates to the target location.

### Requirements

1. Install terminal-notifier via Homebrew:
```bash
brew install terminal-notifier
```

2. Configure your terminal app in `config.yaml`:
```yaml
# Options: Ghostty, iTerm, Terminal, Alacritty, kitty, WezTerm
terminal_app: Ghostty
```

### Basic Usage

The `focus_pane.sh` script activates your terminal and navigates tmux:

```bash
# Focus window 2, pane 0
~/.tmux/plugins/tabby/scripts/focus_pane.sh 2

# Focus window 1, pane 2
~/.tmux/plugins/tabby/scripts/focus_pane.sh 1.2

# Focus specific session, window, and pane
~/.tmux/plugins/tabby/scripts/focus_pane.sh main:2.1
```

### Sending Notifications with Deep Links

```bash
# Simple notification that jumps to window 2
terminal-notifier -title "Build Complete" -message "Click to view" \
  -execute "$HOME/.tmux/plugins/tabby/scripts/focus_pane.sh 2"

# Notification with current location (useful in scripts/hooks)
TARGET=$(tmux display-message -p '#{window_index}.#{pane_index}')
terminal-notifier -title "Task Done" -message "Click to return" \
  -execute "$HOME/.tmux/plugins/tabby/scripts/focus_pane.sh $TARGET"
```

### Integration with Claude Code

Create a notification hook script for Claude Code that includes deep links:

```bash
#!/usr/bin/env bash
# ~/.claude/hooks/notify.sh

MESSAGE="${1:-Task complete}"
TABBY_DIR="${HOME}/.tmux/plugins/tabby"

if [ -n "$TMUX" ]; then
  # Capture current tmux location for deep link
  TARGET=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}')
  terminal-notifier \
    -title "Claude Code" \
    -message "$MESSAGE" \
    -sound default \
    -execute "$TABBY_DIR/scripts/focus_pane.sh $TARGET"
else
  terminal-notifier -title "Claude Code" -message "$MESSAGE" -sound default
fi
```

## Known Limitations

1. **Horizontal tabs are not clickable** - This is a tmux limitation. Custom status formats don't support mouse events. Use keyboard shortcuts or the vertical sidebar for mouse support.

2. **[x] and [+] buttons are visual only** - These buttons appear in horizontal mode but aren't functional. Use keyboard shortcuts instead.

## Troubleshooting

### Tabs not appearing
- Ensure `@tmux_tabs_test` is set to `1` in your tmux config
- Run `tmux source ~/.tmux.conf` to reload
- Check if binaries exist: `ls ~/.tmux/plugins/tabby/bin/`

### Sidebar not toggling
- Verify the toggle key binding: `tmux list-keys | grep toggle_sidebar`
- Check if the sidebar binary is running: `ps aux | grep sidebar`

### Tab overflow not working
- Ensure you have tmux 3.2+ for proper Unicode support
- Try adjusting terminal width or font size

## Contributing

Contributions welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Run the test suite
4. Submit a pull request

## License

MIT License - see LICENSE file for details
