# Tmux Tabs

A modern, groupable tab bar plugin for tmux with horizontal and vertical modes. Features tab overflow handling, visual indicators, and comprehensive keyboard shortcuts.

## Features

- **Horizontal tab bar** with overflow scrolling
- **Vertical sidebar** with persistent state across windows
- **Tab grouping** with customizable themes and icons
- **Window indicators** for activity, bell, and silence
- **Keyboard navigation** with intuitive shortcuts
- **Tab overflow** with scroll indicators
- **Visual close buttons** and new tab button

## Installation

### Via TPM (Tmux Plugin Manager)
Add to your `~/.tmux.conf`:
```bash
set -g @plugin 'b/tmux-tabs'
set -g @tmux_tabs_test 1  # Currently gated - required for activation
```

Then reload tmux and install:
```bash
tmux source ~/.tmux.conf
# Press prefix + I to install plugins
```

### Manual Installation
```bash
git clone https://github.com/b/tmux-tabs ~/.tmux/plugins/tmux-tabs
cd ~/.tmux/plugins/tmux-tabs
./scripts/install.sh
```

Add to your `~/.tmux.conf`:
```bash
run-shell ~/.tmux/plugins/tmux-tabs/tmux-tabs.tmux
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

### Mouse Support (Vertical Sidebar Only)

- **Left click**: Switch to window
- **Middle click**: Close window  
- **Right click**: Context menu (rename, etc.)

Note: Horizontal tabs do not support mouse clicks due to tmux limitations with custom status formats.

## Configuration

Edit `~/.tmux/plugins/tmux-tabs/config.yaml`:

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
cd ~/.tmux/plugins/tmux-tabs
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
tmux-tabs/
├── cmd/
│   ├── render-status/   # Horizontal tab rendering
│   └── sidebar/         # Vertical sidebar app
├── pkg/
│   ├── config/         # Configuration loading
│   ├── grouping/       # Tab grouping logic
│   └── tmux/           # Tmux integration
├── scripts/
│   ├── install.sh      # Build and install
│   ├── toggle_sidebar.sh
│   └── refresh_status.sh
├── tests/              # Test suites
├── config.yaml         # User configuration
└── tmux-tabs.tmux      # Plugin entry point
```

## Known Limitations

1. **Horizontal tabs are not clickable** - This is a tmux limitation. Custom status formats don't support mouse events. Use keyboard shortcuts or the vertical sidebar for mouse support.

2. **[x] and [+] buttons are visual only** - These buttons appear in horizontal mode but aren't functional. Use keyboard shortcuts instead.

## Troubleshooting

### Tabs not appearing
- Ensure `@tmux_tabs_test` is set to `1` in your tmux config
- Run `tmux source ~/.tmux.conf` to reload
- Check if binaries exist: `ls ~/.tmux/plugins/tmux-tabs/bin/`

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
