# Tabby

A modern tab manager for tmux with grouping, a clickable vertical sidebar, and deep linking for notifications.

```
+---------------------------+----------------------------------------+
| Frontend                  |                                        |
|   0. dashboard            |  $ vim src/app.tsx                     |
|   1. components           |                                        |
| Backend                   |                                        |
|   2. api                  |                                        |
|   3. tests                |                                        |
| Default                   |                                        |
| > 4. vim  <-- active      |                                        |
|   5. notes                |                                        |
|                           |                                        |
| [+] New Tab               |                                        |
+---------------------------+----------------------------------------+
```

## Key Features

### Vertical Sidebar
A persistent, clickable sidebar that works across all your windows. Left-click to switch, right-click for context menus, middle-click to close. Full mouse support that tmux's native status bar can't provide.

### Window Grouping
Organize windows by project with color-coded groups. Windows are automatically grouped by pattern matching or manually assigned via right-click menu. Each group has its own theme colors and optional icon.

### Deep Links for Notifications
Click a notification to jump directly to the right tmux session, window, and pane. Perfect for long-running tasks - get notified when done and click to return instantly.

```bash
# Example: notification that deep-links back to tmux
terminal-notifier -title "Build Done" -message "Click to return" \
  -execute "~/.tmux/plugins/tabby/scripts/focus_pane.sh main:2.1"
```

## All Features

- **Vertical sidebar** - clickable, persistent across windows
- **Window grouping** - color-coded project organization
- **Deep link navigation** - click notifications to jump to exact pane
- **Horizontal tab bar** - alternative mode with overflow scrolling
- **Automatic window naming** - shows running command, locks on manual rename
- **Activity indicators** - bell, activity, and silence alerts
- **Mouse support** - click, right-click menus, middle-click close
- **Custom tab colors** - per-window color overrides
- **Keyboard navigation** - intuitive shortcuts for everything

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

# Tab grouping rules (first match wins)
groups:
  - name: "Frontend"
    pattern: "^FE|"
    theme:
      bg: "#e74c3c"
      fg: "#ffffff"
      active_bg: "#c0392b"
      active_fg: "#ffffff"
      icon: ""

  - name: "Backend"
    pattern: "^BE|"
    theme:
      bg: "#27ae60"
      fg: "#ffffff"
      active_bg: "#1e8449"
      active_fg: "#ffffff"
      icon: ""

  - name: "Default"
    pattern: ".*"
    theme:
      bg: "#3498db"
      fg: "#ffffff"
      active_bg: "#2980b9"
      active_fg: "#ffffff"

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

Windows are organized into groups based on name patterns or manual assignment:

```
+---------------------------+
|  SIDEBAR                  |      SESSION
|                           |         |
|  Frontend  [group]        |         +-- Frontend (group)
|    0. dashboard           |         |     +-- 0. dashboard (window)
|    1. components          |         |     |     +-- pane 0: vim
|                           |         |     |     +-- pane 1: terminal
|  Backend   [group]        |         |     +-- 1. components (window)
|    2. api                 |         |           +-- pane 0: npm run dev
|    3. tests               |         |
|                           |         +-- Backend (group)
|  Default   [group]        |         |     +-- 2. api (window)
|  > 4. vim                 |         |     +-- 3. tests (window)
|    5. notes               |         |
|                           |         +-- Default (group)
|  [+] New Tab              |               +-- 4. vim (window) <- active
+---------------------------+               +-- 5. notes (window)
```

### Assigning Groups

**By pattern** - Windows matching a regex are auto-grouped:
- `^FE|` matches `FE|dashboard`, `FE|components`
- `^BE|` matches `BE|api`, `BE|tests`
- `.*` catches everything else in Default

**By right-click menu** - Select "Move to Group" to manually assign

**By tmux option** - Set programmatically:
```bash
tmux set-window-option -t :0 @tabby_group "Frontend"
```

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

Claude Code hooks run as subprocesses, so you need to capture the correct pane - not the currently focused one. The key is using the `TMUX_PANE` environment variable with `tmux display-message -t`.

**Important:** Using `tmux display-message -p` (without `-t`) returns the *currently focused* pane, which may have changed while Claude was working. Using `-t "$TMUX_PANE"` queries the *specific pane* where the hook originated.

#### Hook Configuration

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/claude-stop-notify.sh"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/claude-notification.sh"
          }
        ]
      }
    ]
  }
}
```

#### Example Hook Script

```bash
#!/usr/bin/env bash
# claude-stop-notify.sh - Notification with deep link to correct pane

TABBY_DIR="${HOME}/.tmux/plugins/tabby"

# Read hook JSON from stdin (Claude provides session info)
HOOK_JSON=$(cat)
TRANSCRIPT_PATH=$(echo "$HOOK_JSON" | jq -r '.transcript_path // empty')

# Get tmux info for the SPECIFIC pane where Claude runs
# CRITICAL: Use -t "$TMUX_PANE" to query the originating pane, not current focus
if [[ -n "${TMUX:-}" && -n "${TMUX_PANE:-}" ]]; then
    WINDOW_NAME=$(tmux display-message -t "$TMUX_PANE" -p '#W')
    TMUX_TARGET=$(tmux display-message -t "$TMUX_PANE" -p '#{session_name}:#{window_index}.#{pane_index}')
    WINDOW_INDEX=$(tmux display-message -t "$TMUX_PANE" -p '#I')
    PANE_NUM=$(tmux display-message -t "$TMUX_PANE" -p '#P')
elif [[ -n "${TMUX:-}" ]]; then
    # Fallback (shouldn't happen in normal tmux usage)
    WINDOW_NAME=$(tmux display-message -p '#W')
    TMUX_TARGET=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}')
    WINDOW_INDEX=$(tmux display-message -p '#I')
    PANE_NUM=$(tmux display-message -p '#P')
else
    WINDOW_NAME="no-tmux"
    TMUX_TARGET=""
    WINDOW_INDEX="?"
    PANE_NUM="0"
fi

# Extract Claude's last message from transcript (optional)
MESSAGE="Session complete"
if [[ -n "$TRANSCRIPT_PATH" && -f "$TRANSCRIPT_PATH" ]]; then
    LAST_MSG=$(tac "$TRANSCRIPT_PATH" | grep -m1 '"type":"assistant"' | jq -r '
        .message.content |
        if type == "array" then
            [.[] | select(.type == "text") | .text] | join(" ")
        elif type == "string" then .
        else empty end
    ' 2>/dev/null)
    if [[ -n "$LAST_MSG" && "$LAST_MSG" != "null" ]]; then
        MESSAGE=$(echo "$LAST_MSG" | tr '\n' ' ' | sed 's/  */ /g' | cut -c1-200)
    fi
fi

# Send notification with click-to-focus
if [[ -n "$TMUX_TARGET" ]]; then
    terminal-notifier \
        -title "Claude [$WINDOW_NAME]" \
        -subtitle "Window ${WINDOW_INDEX}:${PANE_NUM}" \
        -message "$MESSAGE" \
        -sound default \
        -group "claude-${WINDOW_INDEX}" \
        -execute "$TABBY_DIR/scripts/focus_pane.sh $TMUX_TARGET"
else
    terminal-notifier \
        -title "Claude" \
        -message "$MESSAGE" \
        -sound default
fi
```

#### Notification Persistence

By default, macOS banner notifications disappear after ~5 seconds. To make them persist until clicked:

1. Open **System Settings** -> **Notifications** -> **terminal-notifier**
2. Change notification style from **Banners** to **Alerts**

#### Disabling Claude's Built-in Notifications

To avoid duplicate notifications (your custom hook + Claude's default), add to `~/.claude.json`:

```json
{
  "preferredNotifChannel": "none"
}
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
