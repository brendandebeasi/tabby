# Work Plan: Collapse Feature

## Overview
Add the ability to collapse/expand groups (hide windows) and windows (hide panes) in the sidebar.

## Requirements Summary
- **Two collapse levels:**
  1. Collapse group = hide windows in that group
  2. Collapse window = hide panes in that window
- **Toggle:** Click `+` or `-` icon to show/hide
- **Visual:** Collapsed items show just the +/- (no count)
- **Persistence:** State persists across sidebar restarts (tmux options)
- **Default:** Expanded (since new windows have single pane)

---

## Architecture Decision

**Storage:** Use tmux session/window options for persistence
- Group collapse: `@tabby_collapsed_groups` (session option, JSON array of group names)
- Window collapse: `@tabby_collapsed` (window option, boolean)

**Why session/window options:**
- Persists across sidebar restarts
- Survives config reloads
- Each window tracks its own pane visibility
- Session tracks which groups are collapsed

---

## Implementation Steps

### Phase 1: Data Structures

#### 1.1 Add collapse state to model
**File:** `cmd/sidebar/main.go`
```go
type model struct {
    // ... existing fields ...
    collapsedGroups map[string]bool  // groupName -> isCollapsed
}
```

#### 1.2 Add collapse field to Window struct
**File:** `pkg/tmux/windows.go`
```go
type Window struct {
    // ... existing fields ...
    Collapsed bool  // Panes hidden (from @tabby_collapsed)
}
```

#### 1.3 Read collapse state from tmux
**File:** `pkg/tmux/windows.go`
- [ ] Add `#{@tabby_collapsed}` to list-windows format string
- [ ] Parse boolean value in ListWindows()

#### 1.4 Load group collapse state
**File:** `cmd/sidebar/main.go`
- [ ] On init, read `@tabby_collapsed_groups` from tmux session
- [ ] Parse JSON array into `collapsedGroups` map
- [ ] Handle missing/empty option gracefully

---

### Phase 2: Visual Rendering

#### 2.1 Add +/- icons to group headers
**File:** `cmd/sidebar/main.go` - `View()`

**Current:**
```
StudioDome
├─▶ 0. window
```

**Collapsed:**
```
+ StudioDome
```

**Expanded:**
```
- StudioDome
├─▶ 0. window
```

- [ ] Prepend `+ ` or `- ` before group icon/name
- [ ] When collapsed, skip rendering windows in that group
- [ ] Style: same color as group header

#### 2.2 Add +/- icons to windows with multiple panes
**File:** `cmd/sidebar/main.go` - `View()`

**Current (expanded):**
```
├─┬ 0. window
│ ├─ pane 0
│ └─ pane 1
```

**Collapsed:**
```
├─+ 0. window
```

**Expanded:**
```
├─- 0. window
│ ├─ pane 0
│ └─ pane 1
```

- [ ] Replace `┬` with `+` when collapsed, `-` when expanded
- [ ] When collapsed, skip rendering panes
- [ ] Only show +/- if window has 2+ panes

---

### Phase 3: Click Detection

#### 3.1 Define clickable regions for +/-
**File:** `cmd/sidebar/main.go`

**Group header click zones:**
- X=0-1: Toggle collapse (the +/- area)
- X>1: Existing behavior (select group, right-click menu)

**Window click zones:**
- X=0-3: Toggle collapse if has panes (the tree + +/- area)
- X>3: Existing behavior (select window)

#### 3.2 Add toggle handlers
**File:** `cmd/sidebar/main.go` - `Update()` mouse handling

```go
case tea.MouseButtonLeft:
    // Check if clicking on collapse toggle area
    if msg.X <= 1 {
        if groupRef, ok := m.getGroupAtLine(msg.Y); ok {
            m.toggleGroupCollapse(groupRef.group.Name)
            return m, nil
        }
    }
    if msg.X <= 3 {
        if win := m.getWindowAtLine(msg.Y); win != nil && len(win.Panes) > 1 {
            m.toggleWindowCollapse(win)
            return m, nil
        }
    }
    // ... existing click handling ...
```

#### 3.3 Implement toggle functions
**File:** `cmd/sidebar/main.go`

```go
func (m *model) toggleGroupCollapse(groupName string) {
    m.collapsedGroups[groupName] = !m.collapsedGroups[groupName]
    m.saveCollapsedGroups()  // Persist to tmux
    m.buildWindowRefs()      // Rebuild line mappings
}

func (m *model) toggleWindowCollapse(win *tmux.Window) {
    newState := !win.Collapsed
    // Set tmux option
    if newState {
        exec.Command("tmux", "set-window-option", "-t",
            fmt.Sprintf(":%d", win.Index), "@tabby_collapsed", "1").Run()
    } else {
        exec.Command("tmux", "set-window-option", "-t",
            fmt.Sprintf(":%d", win.Index), "-u", "@tabby_collapsed").Run()
    }
    // Trigger refresh
}
```

---

### Phase 4: Line Tracking Updates

#### 4.1 Modify buildWindowRefs() for collapsed state
**File:** `cmd/sidebar/main.go`

```go
func (m *model) buildWindowRefs() {
    line := 0
    for _, group := range m.grouped {
        // Group header always gets a line
        m.groupRefs = append(m.groupRefs, groupRef{group: &group, line: line})
        line++

        // Skip windows if group collapsed
        if m.collapsedGroups[group.Name] {
            continue
        }

        for _, win := range group.Windows {
            m.windowRefs = append(m.windowRefs, windowRef{window: &win, line: line})
            line++

            // Skip panes if window collapsed or only 1 pane
            if win.Collapsed || len(win.Panes) <= 1 {
                continue
            }

            for _, pane := range win.Panes {
                m.paneRefs = append(m.paneRefs, paneRef{...})
                line++
            }
        }
    }
    m.totalLines = line
}
```

---

### Phase 5: Persistence

#### 5.1 Save group collapse state to tmux
**File:** `cmd/sidebar/main.go`

```go
func (m model) saveCollapsedGroups() {
    // Convert map to JSON array of collapsed group names
    var collapsed []string
    for name, isCollapsed := range m.collapsedGroups {
        if isCollapsed {
            collapsed = append(collapsed, name)
        }
    }

    if len(collapsed) == 0 {
        // Unset option if nothing collapsed
        exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
    } else {
        jsonBytes, _ := json.Marshal(collapsed)
        exec.Command("tmux", "set-option", "@tabby_collapsed_groups",
            string(jsonBytes)).Run()
    }
}
```

#### 5.2 Load group collapse state on init
**File:** `cmd/sidebar/main.go`

```go
func loadCollapsedGroups() map[string]bool {
    result := make(map[string]bool)

    out, err := exec.Command("tmux", "show-options", "-v",
        "@tabby_collapsed_groups").Output()
    if err != nil {
        return result  // No collapsed groups
    }

    var collapsed []string
    if json.Unmarshal(bytes.TrimSpace(out), &collapsed) == nil {
        for _, name := range collapsed {
            result[name] = true
        }
    }
    return result
}
```

#### 5.3 Window collapse in ListWindows
**File:** `pkg/tmux/windows.go`

- [ ] Add `#{@tabby_collapsed}` to format string (already pattern exists for other options)
- [ ] Parse in ListWindows() like other boolean options

---

### Phase 6: Right-Click Menu Integration

#### 6.1 Add to group context menu
**File:** `cmd/sidebar/main.go` - `showGroupContextMenu()`

```go
// Add collapse/expand option
if m.collapsedGroups[gr.group.Name] {
    args = append(args, "Expand Group", "e",
        fmt.Sprintf("run-shell '...'"))
} else {
    args = append(args, "Collapse Group", "c",
        fmt.Sprintf("run-shell '...'"))
}
```

#### 6.2 Add to window context menu
**File:** `cmd/sidebar/main.go` - `showContextMenu()`

```go
// Only show if window has 2+ panes
if len(win.Panes) > 1 {
    if win.Collapsed {
        args = append(args, "Show Panes", "p", ...)
    } else {
        args = append(args, "Hide Panes", "p", ...)
    }
}
```

---

### Phase 7: Keyboard Support (Optional)

#### 7.1 Add keyboard toggle
**File:** `cmd/sidebar/main.go` - `Update()` key handling

```go
case "space":
    // Toggle collapse on selected item
    if m.selectedGroup != nil {
        m.toggleGroupCollapse(m.selectedGroup.Name)
    } else if m.selectedWindow != nil && len(m.selectedWindow.Panes) > 1 {
        m.toggleWindowCollapse(m.selectedWindow)
    }
```

---

## Visual Design

### Group Header States

```
Expanded (default):
- StudioDome
├─▶ 0. window-name
└── 1. other-window

Collapsed:
+ StudioDome
```

### Window States (with multiple panes)

```
Expanded (default):
├─- 0. window-name
│ ├─ 0.0 pane-title
│ └─ 0.1 pane-title

Collapsed:
├─+ 0. window-name
```

### Single-Pane Windows (no +/-)

```
├─▶ 0. window-name    (active, single pane - no toggle)
├── 1. other-window   (inactive, single pane - no toggle)
```

---

## File Changes Summary

| File | Changes |
|------|---------|
| `cmd/sidebar/main.go` | Collapse state, rendering, click handlers, persistence |
| `pkg/tmux/windows.go` | Add Collapsed field, read @tabby_collapsed |
| `pkg/grouping/grouper.go` | (optional) Pass through collapse state |

---

## Testing Checklist

### Group Collapse
- [ ] Click `+` on group header expands it
- [ ] Click `-` on group header collapses it
- [ ] Collapsed group hides all windows
- [ ] Collapse state persists after sidebar restart
- [ ] Multiple groups can be collapsed independently
- [ ] Right-click menu shows Expand/Collapse option

### Window Collapse
- [ ] Click `+` on window expands panes
- [ ] Click `-` on window collapses panes
- [ ] Single-pane windows don't show +/-
- [ ] Collapse state persists (tmux option)
- [ ] Right-click menu shows Show/Hide Panes option

### Edge Cases
- [ ] New window starts expanded
- [ ] Deleting collapsed group doesn't break state
- [ ] Creating new group starts expanded
- [ ] Collapse toggle doesn't interfere with selection

---

## Estimated Complexity

| Phase | Effort | Notes |
|-------|--------|-------|
| Phase 1 | Easy | Data structures |
| Phase 2 | Medium | Rendering logic changes |
| Phase 3 | Medium | Click zone detection |
| Phase 4 | Easy | Ref building changes |
| Phase 5 | Easy | JSON to/from tmux |
| Phase 6 | Easy | Menu additions |
| Phase 7 | Easy | Optional keyboard |

**Total:** Medium feature, ~1-2 focused sessions

---

## Dependencies

- None on "New Group" feature - can be implemented independently
- Should work with existing group/window infrastructure
