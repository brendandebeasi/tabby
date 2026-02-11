# Tabby

A modern tab manager for tmux with grouping, a clickable vertical sidebar, and deep linking for notifications.

## About This Project

Tabby started as an opinionated solution to a personal problem: managing dozens of tmux windows across multiple projects without losing context. It grew into something others might find useful.

**Design Philosophy:**
- Customizable - support for Nerd Fonts, emoji, ASCII, and various terminal features
- Modular - enable only the features you need (sidebar, pane headers, widgets, etc.)
- Extensible - widget system for adding custom sidebar content (clock, git status, pet, stats, Claude usage)
- Terminal-agnostic - works with most modern terminals (Ghostty, iTerm, Kitty, Alacritty, etc.)

**Contributing:** PRs are welcome. This is actively developed but cannot promise support for all terminal emulators or use cases. If you find Tabby useful or have ideas, contributions are appreciated.

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

- **Vertical sidebar** - clickable, persistent across windows with collapse/expand
- **Window grouping** - color-coded project organization with working directories
- **Deep link navigation** - click notifications to jump to exact pane
- **Horizontal tab bar** - alternative mode with overflow scrolling
- **Automatic window naming** - shows running command, locks on manual rename
- **Activity indicators** - bell, activity, silence, busy, and input alerts
- **Mouse support** - click, right-click menus, middle-click close, double-click to collapse
- **Custom tab colors** - per-window color overrides, including transparent mode
- **Pane management** - rename panes with title locking
- **Group management** - create, rename, color, collapse, and set working directories
- **SSH bell notifications** - auto-enable bells on remote command completion
- **Keyboard navigation** - intuitive shortcuts for everything

## Installation

### Via TPM (Tmux Plugin Manager)
Add to your `~/.tmux.conf`:
```bash
set -g @plugin 'brendandebeasi/tabby'
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
```

## Usage

### Keyboard Shortcuts

Tabby follows standard tmux keybindings. All standard tmux shortcuts work as expected.

#### Standard tmux shortcuts (prefix + key)

| Key | Action |
|-----|--------|
| `prefix + c` | Create new window |
| `prefix + n` | Next window |
| `prefix + p` | Previous window |
| `prefix + x` | Kill current pane |
| `prefix + q` | Display pane numbers |
| `prefix + w` | Window list |
| `prefix + ,` | Rename window |
| `prefix + "` | Split horizontal |
| `prefix + %` | Split vertical |
| `prefix + d` | Detach from session |
| `prefix + 1-9,0` | Switch to window by number |

#### Tabby-specific shortcuts

| Key | Action |
|-----|--------|
| `prefix + Tab` | Toggle vertical sidebar |
| `prefix + G` | Create new group |
| `prefix + M` | Toggle mode |
| `prefix + V` | Switch to vertical mode |
| `prefix + H` | Switch to horizontal mode |
| `Ctrl + <` or `Alt + <` | Collapse/expand sidebar |

### Mouse Support (Vertical Sidebar)

- **Left click**: Switch to window/pane
- **Double-click**: Collapse/expand sidebar
- **Click right edge**: Click the divider to collapse sidebar
- **Middle click**: Close window (with confirmation)
- **Right click on window**: Context menu with options:
  - Rename (with title locking)
  - Unlock Name (restore automatic naming)
  - Collapse/Expand Panes
  - Move to Group
  - Set Tab Color (including transparent)
  - Split Horizontal/Vertical
  - Open in Finder
  - Kill window
- **Right click on pane**: Pane-specific options:
  - Rename pane (with title locking)
  - Unlock pane name
  - Split pane
  - Focus pane
  - Break to new window
  - Close pane
- **Right click on group**: Group management:
  - New window in group
  - Collapse/Expand group
  - Rename group
  - Change group color
  - Set working directory
  - Delete group
  - Close all windows in group

Note: Horizontal tabs have limited mouse support due to tmux status bar limitations.

### Sidebar Position and Mode

The sidebar can be placed on either side of the window and can span the full height or attach to a single pane.

**Set via tmux options:**
```bash
# Position: left (default) or right
tmux set-option -g @tabby_sidebar_position right

# Mode: full (default, spans full window) or partial (attaches to one pane)
tmux set-option -g @tabby_sidebar_mode partial
```

Toggle the sidebar off and on (`prefix + Tab` twice) after changing these options.

### Sidebar Collapse

The sidebar can be collapsed to maximize screen space:

- **Double-click** anywhere on the sidebar to toggle collapse/expand
- **Click the right edge** (divider area) to collapse
- **Keyboard**: `Ctrl+<` or `Alt+<` to toggle
- **Collapsed state**: Shows `>` down the entire height - click anywhere to expand

When collapsed, the sidebar takes only 2 characters of width. When expanded, it restores to your configured width.

### SSH Bell Notifications

Automatically receive bell notifications when commands complete in SSH sessions.

#### Auto-enable for all SSH connections

Add to your `~/.ssh/config`:

```ssh
Host *
  RemoteCommand bash -c 'PROMPT_COMMAND="printf \"\a\""; exec bash -l'
  RequestTTY force
```

This works by injecting a bell into the remote shell's prompt, so you get a notification after every command.

**Note:** This uses `RemoteCommand` which may interfere with tools like `scp`, `rsync`, and `git` over SSH. If you encounter issues, override for specific hosts:

```ssh
Host github.com gitlab.com bitbucket.org
  RemoteCommand none
  RequestTTY auto
```

#### Alternative: Add to remote servers

If you control the remote servers, add this to `~/.bashrc` on each server:

```bash
export PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }printf '\a'"
```

This approach doesn't require SSH config changes and won't interfere with other tools.

## Configuration

Edit `~/.tmux/plugins/tabby/config.yaml`:

```yaml
# Tab bar position: top, bottom, or off
position: top

# Tab grouping rules (first match wins)
groups:
  - name: "Frontend"
    pattern: "^FE|"
    working_dir: "~/projects/frontend"  # Default dir for new windows
    theme:
      bg: "#e74c3c"
      fg: "#ffffff"
      active_bg: "#c0392b"
      active_fg: "#ffffff"
      icon: ""

  - name: "Backend"
    pattern: "^BE|"
    working_dir: "~/projects/backend"
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
  activity:
    enabled: false
    icon: "!"
    color: "#000000"
  bell:
    enabled: true
    icon: "◆"
    color: "#000000"
    bg: "#ffff00"  # Yellow background for visibility
  silence:
    enabled: true
    icon: "○"
    color: "#000000"
  busy:
    enabled: true
    icon: "◐"
    color: "#ff0000"
    frames: ["◐", "◓", "◑", "◒"]  # Animation frames
  input:
    enabled: true
    icon: "?"
    color: "#ffffff"
    bg: "#9b59b6"  # Purple - needs attention
    frames: ["?", "?"]  # Can add blinking: ["?", " "]

# Tab overflow behavior
overflow:
  mode: scroll     # scroll or truncate
  indicator: "›"   # Character shown when tabs overflow

# Vertical sidebar settings
sidebar:
  position: left      # "left" or "right"
  mode: full          # "full" (full window height) or "partial" (attach to pane)
  new_tab_button: true
  new_group_button: true
  show_empty_groups: true
  close_button: false
  sort_by: "group"  # "group" or "index"
  colors:
    disclosure_fg: "#000000"
    disclosure_expanded: "⊟"
    disclosure_collapsed: "⊞"
    active_indicator: "◀"  # Active window/pane indicator
    active_indicator_fg: "auto"  # "auto" uses group/window bg color
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

### Custom Colors and Transparent Mode

Set custom colors for individual windows or groups:

**Window colors** - Right-click window → Set Tab Color:
- Predefined colors: Red, Orange, Yellow, Green, Blue, Purple, Pink, Cyan, Gray
- **Transparent**: No background, simple text color (minimal visual)
- Reset to default group color

**Group colors** - Right-click group → Edit Group → Change Color:
- Same color options as windows
- **Transparent**: Clean text-only display for the entire group
- Affects all windows in the group (unless they have custom colors)

**Set programmatically**:
```bash
# Set window to transparent
tmux set-window-option -t :0 @tabby_color "transparent"

# Set window to custom color
tmux set-window-option -t :0 @tabby_color "#e91e63"

# Reset to group color
tmux set-window-option -t :0 -u @tabby_color
```

### Group Working Directories

Set a default working directory for each group. New windows created in the group will automatically use this directory:

**Via context menu**: Right-click group → Edit Group → Set Working Directory

**In config.yaml**:
```yaml
groups:
  - name: "MyProject"
    working_dir: "~/projects/myproject"
    # ...
```

### Pane Management

**Rename panes** with title locking (like window names):
- Right-click pane → Rename
- Locked titles persist until manually unlocked
- Right-click pane → Unlock Name to restore automatic naming

**Set programmatically**:
```bash
# Set locked pane title
tmux set-option -p -t %123 @tabby_pane_title "My Pane"

# Clear locked title
tmux set-option -p -t %123 -u @tabby_pane_title
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

## Tabby Web (Local-Only)

Tabby Web runs a local-only bridge that exposes tmux + sidebar over WebSocket. The bridge only binds to loopback and requires user/password for access.

### Enable in config (default disabled)
Add this to `~/.tmux/plugins/tmux-tabs/config.yaml`:
```yaml
web:
  enabled: true
  host: "127.0.0.1"
  port: 8080
  auth_user: "tabby"
  auth_pass: "testpass"
```

### Start the bridge
```bash
# Start daemon for a session
go run ./cmd/tabby-daemon -session tabby-web-test

# Start web bridge (loopback only) with auth
go run ./cmd/tabby-web-bridge -session tabby-web-test -host 127.0.0.1 -port 8080 -auth-user tabby -auth-pass testpass
```

### Start the web client
```bash
cd web
npm install
npm run dev
```

### Connect in browser
Open `http://127.0.0.1:5173/?token=<token>&user=<user>&pass=<pass>&pane=<pane_id>&ws=127.0.0.1:8080`

- Token is stored at `~/.config/tabby/web-token`
- Pane ID can be retrieved with `tmux list-panes -t tabby-web-test -F '#{pane_id}'`
- The bridge rejects non-loopback requests

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
- Ensure Tabby is not explicitly disabled: `tmux show -gv @tabby_enabled` (should not be `0`)
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
