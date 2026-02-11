# Tabby Bug Analysis & E2E Test Plan

## Critical Bugs

### BUG-001: Toggle Sidebar PID File Race Condition
**Severity**: Critical
**File**: `scripts/toggle_sidebar.sh`

**Problem**: 
The PID file uses `$$` (shell process ID) which changes on every script invocation:
```bash
SIDEBAR_PID_FILE="/tmp/tabby-sidebar-$$-${TMUX_PANE}.pid"
```

**Impact**:
- Multiple sidebars can spawn because detection fails
- PID files become orphaned (never cleaned up)
- Signal-based refresh (SIGUSR1) never reaches sidebar

**Evidence**:
```
/tmp/tabby-sidebar-56745-.pid
/tmp/tabby-sidebar-57132-.pid
/tmp/tabby-sidebar-59130-.pid
... (11+ orphaned files found)
```

**Fix**: Use session-based identifier:
```bash
SESSION_ID=$(tmux display-message -p '#{session_id}')
WINDOW_ID=$(tmux display-message -p '#{window_id}')
SIDEBAR_PID_FILE="/tmp/tabby-sidebar-${SESSION_ID}-${WINDOW_ID}.pid"
```

---

### BUG-002: Sidebar Detection Uses Wrong Pane Scope
**Severity**: Critical  
**File**: `scripts/toggle_sidebar.sh`

**Problem**:
```bash
SIDEBAR_PANE=$(tmux list-panes -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar|" | cut -d'|' -f2)
```
This only searches the **current window's panes**, not the session.

**Impact**:
- Toggling sidebar in Window A doesn't detect sidebar in Window B
- Multiple sidebars can exist across windows
- User expects one sidebar per session, not per window

**Fix**: Add `-a` flag for all panes or `-s` for session:
```bash
SIDEBAR_PANE=$(tmux list-panes -s -F "#{pane_current_command}|#{pane_id}" | grep "^sidebar|" | cut -d'|' -f2)
```

---

### BUG-003: Tmux Hooks Reference Invalid PID Files
**Severity**: High
**File**: `tabby.tmux`

**Problem**:
```bash
tmux set-hook -g window-linked 'run-shell "[ -f /tmp/tabby-sidebar-$$-${TMUX_PANE}.pid ] && ..."'
```
The `$$` in hooks expands to tmux server's PID (or hook runner's PID), not the sidebar's PID file.

**Impact**:
- Window refresh signals never reach sidebar
- Sidebar doesn't update when windows are created/renamed/killed

**Fix**: Use session-based discovery or a well-known socket:
```bash
# Option 1: Session-based PID file
SESSION_ID=#{session_id}
WINDOW_ID=#{window_id}

# Option 2: Unix socket per session
/tmp/tabby-daemon-${SESSION_ID}.sock
```

---

### BUG-004: Sidebar Click Y-Coordinate Off-by-One
**Severity**: High
**File**: `cmd/sidebar/main.go`

**Problem**:
`getWindowAtY()` and `View()` have mismatched line counting:

```go
// View() - line starts at 0
for _, group := range m.grouped {
    s += fmt.Sprintf("%s %s\n", group.Theme.Icon, group.Name)  // line 0
    line++
    for i, win := range group.Windows {
        // line 1, 2, 3...
    }
}

// getWindowAtY() - also starts at 0, but increments AFTER group header
line := 0
for _, group := range m.grouped {
    line++  // Now line=1 for first window, but View() shows it at line=1 too
    ...
}
```

**Impact**:
- Clicking on window X may select window X-1 or X+1
- Group headers may be treated as windows

**Fix**: Align line counting between View() and getWindowAtY()

---

### BUG-005: Cursor vs Window Index Confusion
**Severity**: High
**File**: `cmd/sidebar/main.go`

**Problem**:
```go
case tea.MouseMiddle:
    if clicked != nil {
        _ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", clicked.Index)).Run()
    }

// But close button uses:
} else if m.config.Sidebar.CloseButton && msg.Y == len(m.grouped)+2 {
    _ = exec.Command("tmux", "kill-window", "-t", fmt.Sprintf(":%d", m.windows[m.cursor].Index)).Run()
}
```

`m.cursor` is a visual line position, not an index into `m.windows`. After grouping, the flat windows array doesn't match visual positions.

**Impact**:
- Close button may kill wrong window
- Cursor position doesn't map to actual window

---

### BUG-006: Sidebar Process Leaks
**Severity**: Medium
**File**: `scripts/toggle_sidebar.sh`

**Problem**:
```bash
tmux split-window -h -b -l 25 "exec \"$CURRENT_DIR/bin/sidebar\"" &
echo $! > "$SIDEBAR_PID_FILE"
```
The `&` backgrounds `tmux split-window`, not the sidebar. The PID saved is tmux's, not sidebar's.

**Impact**:
- Can't properly signal sidebar
- Can't track if sidebar is still running

**Fix**: Let tmux manage the pane lifecycle, track by pane ID not process PID.

---

## Medium Bugs

### BUG-007: Config Path Hardcoded -- RESOLVED
**File**: `pkg/config/config.go`

**Resolution**: Centralized in `pkg/paths/paths.go` with XDG-style layout:
- Config: `~/.config/tabby/config.yaml` (env: `TABBY_CONFIG_DIR`)
- State: `~/.local/state/tabby/` (env: `TABBY_STATE_DIR`)
- No legacy fallback -- config reads exclusively from the XDG path

---

### BUG-008: No Error Handling in Render Output
**File**: `cmd/render-status/main.go`

**Problem**:
```go
cfg, err := config.LoadConfig(config.DefaultConfigPath())
if err != nil {
    fmt.Print("tabby: config error")
    return
}
```

Error message isn't tmux-formatted, may corrupt status line.

---

## E2E Test Plan

### Test Infrastructure

1. **Terminal Capture**
   - Use `tmux capture-pane -e -p` for ANSI output
   - Parse with Go/Python ANSI parser for color verification
   - Store baseline captures in `tests/screenshots/baseline/`

2. **Visual Regression**
   - Convert ANSI to HTML with `ansi2html`
   - Screenshot HTML with headless browser
   - Compare with pixelmatch or similar

3. **Automated Test Runner**
   ```bash
   tests/run_e2e.sh
   ├── setup_test_session.sh
   ├── test_horizontal_tabs.sh
   ├── test_sidebar_toggle.sh
   ├── test_window_operations.sh
   └── cleanup.sh
   ```

### Test Cases

| ID | Test | Expected | Automated? |
|----|------|----------|-----------|
| E2E-001 | Horizontal tabs render with correct colors | SD=red, GP=gray, Default=blue | Yes |
| E2E-002 | Active window shows bold + active_bg | Active window distinguishable | Yes |
| E2E-003 | Toggle sidebar opens sidebar pane | Sidebar pane appears on left | Yes |
| E2E-004 | Toggle sidebar closes existing sidebar | Sidebar pane disappears | Yes |
| E2E-005 | Click window in sidebar switches window | Window changes | Yes (send-keys) |
| E2E-006 | Middle-click window closes it | Window count decreases | Yes |
| E2E-007 | Right-click shows context menu | display-menu appears | Manual |
| E2E-008 | New window appears in sidebar | Window list updates | Yes |
| E2E-009 | Renamed window updates in sidebar | Name changes | Yes |
| E2E-010 | Config change hot-reloads | Colors change without restart | Yes |
| E2E-011 | Multiple sessions don't interfere | Sidebars independent | Yes |

### Visual Test Captures

```
tests/screenshots/
├── baseline/
│   ├── horizontal-3-groups.txt     # ANSI capture
│   ├── horizontal-3-groups.html    # Rendered HTML
│   ├── horizontal-3-groups.png     # Screenshot
│   ├── sidebar-open.txt
│   ├── sidebar-open.html
│   └── sidebar-open.png
├── current/
│   └── (same structure)
└── diffs/
    └── (visual diffs)
```

### Test Runner Commands

```bash
# Run all E2E tests
make test-e2e

# Run visual capture
make capture-visual

# Compare visuals
make compare-visual

# Run in Docker (CI)
make test-docker
```

---

## New Feature Requirements

### FEAT-001: Window Activity/Alert Indicators
**Priority**: High

Tmux provides flags for window activity states that should be displayed visually:

```bash
# Available tmux window flags:
#{window_activity_flag}  # 1 if window has activity
#{window_bell_flag}      # 1 if window has bell alert
#{window_silence_flag}   # 1 if window is silent
#{window_last_flag}      # 1 if window was last active
#{window_zoomed_flag}    # 1 if window is zoomed
```

**Required Indicators**:
| State | Icon | Color | Description |
|-------|------|-------|-------------|
| Activity | `󱅫` or `●` | Yellow pulse | Process output in background window |
| Bell | `󰂞` or `🔔` | Red | Bell character received |
| Silence | `󰂛` or `🔇` | Gray | No activity for silence-threshold |
| Running | `⟳` or spinner | Blue | Long-running process active |

**Implementation**:
1. Extend `pkg/tmux/windows.go` to capture flags:
```go
type Window struct {
    ID           string
    Index        int
    Name         string
    Active       bool
    ActivityFlag bool  // NEW
    BellFlag     bool  // NEW
    SilenceFlag  bool  // NEW
    LastFlag     bool  // NEW
}
```

2. Update render-status to show indicators
3. Update sidebar to show indicators with colors

**Config Extension**:
```yaml
indicators:
  activity:
    icon: "󱅫"
    color: "#f1c40f"
  bell:
    icon: "󰂞" 
    color: "#e74c3c"
  silence:
    icon: "󰂛"
    color: "#95a5a6"
  running:
    icon: "⟳"
    color: "#3498db"
```

---

### FEAT-002: Hook System for Notifications
**Priority**: High

Enable external integrations (macOS notifications, Ghostty deeplinks) when window events occur.

**Events to Hook**:
| Event | Trigger | Data Passed |
|-------|---------|-------------|
| `on_bell` | Bell character received | session, window, pane |
| `on_activity` | Activity in background window | session, window |
| `on_silence` | Window goes silent | session, window |
| `on_window_created` | New window | session, window |
| `on_window_closed` | Window killed | session, window_name |
| `on_window_renamed` | Window renamed | session, window, old_name, new_name |

**Config Extension**:
```yaml
hooks:
  on_bell:
    - type: notification
      title: "🔔 Bell in #{window_name}"
      message: "Session: #{session_name}"
      deeplink: "tmux://select-window?session=#{session_id}&window=#{window_id}"
    - type: exec
      command: "/path/to/custom-script.sh"
      args: ["#{session_name}", "#{window_name}"]
  
  on_activity:
    - type: notification
      title: "Activity in #{window_name}"
      sound: "default"  # macOS sound
```

**Notification Implementation (macOS)**:
```bash
# Using terminal-notifier or osascript
terminal-notifier \
  -title "tabby" \
  -subtitle "Bell in SD|app" \
  -message "Session: main" \
  -open "tmux://select?session=\$0&window=@5"

# Or AppleScript
osascript -e 'display notification "Activity detected" with title "tabby"'
```

**Deeplink Strategy**:

Since Ghostty doesn't have URL scheme support yet (discussions #3021, #4379, #5931), use a multi-tier approach:

1. **Tier 1: tmux command** (always works)
   ```bash
   # Deeplink handler script: ~/.local/bin/tabby-open
   tmux select-window -t "$SESSION:$WINDOW"
   tmux switch-client -t "$SESSION"
   ```

2. **Tier 2: Terminal-specific** (when supported)
   ```yaml
   deeplink:
     # Future Ghostty support
     ghostty: "ghostty://run?cmd=tmux+select-window+-t+#{session}:#{window}"
     # iTerm2 (works now)
     iterm2: "iterm2://send-text?text=tmux+select-window+-t+#{session}:#{window}"
     # Fallback
     default: "exec:tmux select-window -t #{session}:#{window}"
   ```

3. **Tier 3: Custom URL handler**
   Register `tabby://` URL scheme on macOS:
   ```xml
   <!-- Info.plist for helper app -->
   <key>CFBundleURLTypes</key>
   <array>
     <dict>
       <key>CFBundleURLSchemes</key>
       <array>
         <string>tabby</string>
       </array>
     </dict>
   </array>
   ```

**Hook Execution Flow**:
```
tmux hook fires
    ↓
tabby receives signal/event
    ↓
Parse event type + window data
    ↓
For each configured hook:
    ├── notification → terminal-notifier/osascript
    ├── exec → spawn subprocess
    └── deeplink → construct URL + open
```

---

### FEAT-003: Running Process Indicator
**Priority**: Medium

Detect when a pane is running a "long" process vs waiting at prompt.

**Detection Methods**:
1. Check `#{pane_current_command}` != shell
2. Track command start time
3. User-defined process patterns

**Config**:
```yaml
running_indicator:
  enabled: true
  # Commands that count as "running" (not idle shell)
  patterns:
    - "npm|yarn|pnpm"
    - "go build|go run|go test"
    - "cargo|rustc"
    - "python|node|ruby"
    - "make|cmake"
  # Minimum time before showing indicator
  threshold_seconds: 3
  icon: "⟳"
  color: "#3498db"
```

---

## E2E Test Cases (Extended)

| ID | Test | Expected | Automated? |
|----|------|----------|-----------|
| E2E-012 | Activity flag shows indicator | Yellow dot on tab | Yes |
| E2E-013 | Bell flag shows indicator | Red bell on tab | Yes |
| E2E-014 | Bell triggers notification hook | macOS notification appears | Manual |
| E2E-015 | Notification deeplink switches window | Click notification → window focused | Manual |
| E2E-016 | Running process shows spinner | Spinner while npm runs | Yes |
| E2E-017 | Process completes clears spinner | Spinner disappears | Yes |
