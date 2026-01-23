# Tabby Widgets Guide

Widgets are optional components that can be added to the sidebar to display additional information.

## Available Widgets

### Clock Widget

Displays the current time and optionally the date.

```yaml
widgets:
  clock:
    enabled: true
    format: "15:04:05"       # Go time format string
    show_date: true
    date_format: "Mon Jan 2"
    fg: "#888888"            # Text color
    bg: ""                   # Background color (optional)
    position: bottom         # "top" or "bottom"
    divider: "─"             # Divider line character
    divider_fg: "#444444"    # Divider color
    margin_top: 1            # Blank lines before widget
    margin_bottom: 0         # Blank lines after widget
    padding_top: 0           # Lines between divider and content
    padding_bottom: 0
```

#### Time Format Examples

Uses Go's time format syntax (reference time: `Mon Jan 2 15:04:05 MST 2006`):

| Format | Output |
|--------|--------|
| `15:04` | 14:30 |
| `15:04:05` | 14:30:45 |
| `3:04 PM` | 2:30 PM |
| `3:04:05 PM` | 2:30:45 PM |
| `15:04 MST` | 14:30 PST |

#### Date Format Examples

| Format | Output |
|--------|--------|
| `Mon Jan 2` | Wed Jan 22 |
| `Monday` | Wednesday |
| `Jan 2, 2006` | Jan 22, 2026 |
| `2006-01-02` | 2026-01-22 |
| `01/02/06` | 01/22/26 |

#### Divider Characters

Common divider options:
- `─` - Light horizontal line
- `━` - Heavy horizontal line
- `=` - Double line
- `-` - ASCII dash
- `·` - Dots
- ` ` - Space (invisible divider, just spacing)

#### Layout Structure

```
[margin_top lines]
[divider line]
[padding_top lines]
[time]
[date if show_date]
[padding_bottom lines]
[margin_bottom lines]
```

## Creating Custom Widgets

To add a new widget:

1. Add widget config struct in `pkg/config/config.go`:
```go
type MyWidget struct {
    Enabled bool   `yaml:"enabled"`
    // ... options
}
```

2. Add to `Widgets` struct:
```go
type Widgets struct {
    Clock    ClockWidget `yaml:"clock"`
    MyWidget MyWidget    `yaml:"my_widget"`
}
```

3. Add tick message if needed in `cmd/sidebar/main.go`:
```go
type myWidgetTickMsg struct{}

func myWidgetTick() tea.Cmd {
    return tea.Tick(time.Second, func(t time.Time) tea.Msg {
        return myWidgetTickMsg{}
    })
}
```

4. Start tick in `Init()` if widget is enabled

5. Handle tick in `Update()`:
```go
case myWidgetTickMsg:
    return m, myWidgetTick()
```

6. Add render function:
```go
func (m model) renderMyWidget() string {
    // Return rendered widget string
}
```

7. Call render in `View()` at appropriate position

## Widget Ideas

Future widgets that could be added:
- **System Stats** - CPU, memory, battery
- **Git Status** - Current branch, dirty state
- **Session Info** - Session name, attached clients count
- **Weather** - Current temperature (via API)
- **Pomodoro Timer** - Work/break timer
- **Custom Command** - Output of user-defined shell command
