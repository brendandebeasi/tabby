# Work Plan: New Group Feature

## Overview
Add the ability to create new groups from the sidebar UI, with persistence to config.yaml.

## Requirements Summary
- **UI Entry Points:**
  1. "New Group" button at bottom of sidebar (below "New Tab")
  2. Right-click context menu option
  3. Keyboard shortcut (find unused one)
- **Creation Flow:** Simple prompt for name, use default theme, customize later
- **Persistence:** Save to config.yaml
- **Editing:** Right-click group header -> "Edit Group" / "Delete Group"

---

## Architecture Decision

**Challenge:** Groups are defined in `config.yaml`, not dynamically in tmux.

**Solution:** Modify config.yaml programmatically and trigger config reload.
- Pros: True persistence, survives tmux restart, user can hand-edit
- Cons: Need YAML parsing/writing, file I/O from Go

**Alternative considered:** Store in tmux global options
- Pros: Simpler implementation
- Cons: Lost on tmux restart, doesn't match current architecture

---

## Implementation Steps

### Phase 1: Config File Management

#### 1.1 Add config file write capability
**File:** `pkg/config/config.go`
- [ ] Add `SaveConfig(path string, cfg *Config) error` function
- [ ] Use `gopkg.in/yaml.v3` for round-trip parsing (preserves comments)
- [ ] Handle file permissions correctly

#### 1.2 Add group CRUD functions
**File:** `pkg/config/config.go`
- [ ] `AddGroup(cfg *Config, group Group) error`
- [ ] `UpdateGroup(cfg *Config, name string, group Group) error`
- [ ] `DeleteGroup(cfg *Config, name string) error`
- [ ] Validate group name uniqueness
- [ ] Don't allow deleting "Default" group

---

### Phase 2: Sidebar UI - New Group Button

#### 2.1 Add config option
**File:** `pkg/config/config.go`
```go
type Sidebar struct {
    NewTabButton   bool `yaml:"new_tab_button"`
    CloseButton    bool `yaml:"close_button"`
    NewGroupButton bool `yaml:"new_group_button"`  // NEW
    SortBy         string `yaml:"sort_by"`
}
```

#### 2.2 Add button rendering
**File:** `cmd/sidebar/main.go`
- [ ] Add `newGroupLine` to `calculateButtonLines()`
- [ ] Render `[+] New Group` below `[+] New Tab` in `View()`
- [ ] Use distinct color (e.g., purple `#9b59b6`)

#### 2.3 Add click handler
**File:** `cmd/sidebar/main.go`
- [ ] Detect click on newGroupLine
- [ ] Call `showNewGroupPrompt()` function

#### 2.4 Implement new group prompt
**File:** `cmd/sidebar/main.go`
- [ ] Create `showNewGroupPrompt()` function
- [ ] Use tmux `command-prompt` for name input
- [ ] Callback script creates group with defaults:
  - Default theme colors (copy from Default group)
  - Auto-generate pattern: `^{Name}\\|`
  - Empty icon
- [ ] Reload config after creation

---

### Phase 3: Right-Click Menu Integration

#### 3.1 Add "New Group" to group context menu
**File:** `cmd/sidebar/main.go` - `showGroupContextMenu()`
- [ ] Add "New Group" menu item
- [ ] Triggers same flow as button click

#### 3.2 Add "Edit Group" to group context menu
**File:** `cmd/sidebar/main.go`
- [ ] Add "Edit Group" submenu with:
  - Rename (prompt for new name)
  - Change Color (color picker submenu)
  - Change Icon (common icons submenu)
- [ ] Update config.yaml on changes

#### 3.3 Add "Delete Group" to group context menu
**File:** `cmd/sidebar/main.go`
- [ ] Add "Delete Group" menu item
- [ ] Confirmation prompt
- [ ] Move windows to Default group before deletion
- [ ] Don't allow deleting Default group

---

### Phase 4: Keyboard Shortcut

#### 4.1 Find unused shortcut
**Current bindings in config.yaml:**
- `prefix + Tab` - toggle sidebar
- `prefix + n` - next tab
- `prefix + p` - prev tab

**Proposed:** `prefix + G` for new Group (capital G, unused)

#### 4.2 Add tmux binding
**File:** `tabby.tmux`
- [ ] Add binding: `bind G run-shell '...'`
- [ ] Script triggers same new group flow

---

### Phase 5: Helper Scripts

#### 5.1 Create group management script
**File:** `scripts/manage_group.sh`
```bash
#!/bin/bash
# Usage: manage_group.sh [create|edit|delete] [args...]
ACTION="$1"
case "$ACTION" in
    create)
        NAME="$2"
        # Add group to config.yaml
        # Signal sidebar to reload
        ;;
    edit)
        OLD_NAME="$2"
        NEW_NAME="$3"
        # Update group in config.yaml
        ;;
    delete)
        NAME="$2"
        # Remove group, move windows to Default
        ;;
esac
```

**Alternative:** Do all in Go (preferred for YAML handling)

---

### Phase 6: Config Reload Signal

#### 6.1 Add reload mechanism
**File:** `cmd/sidebar/main.go`
- [ ] Handle `SIGHUP` or custom signal for config reload
- [ ] Or use file watcher on config.yaml (fsnotify)
- [ ] Existing: `reloadConfigMsg` message type exists, need to trigger it

#### 6.2 Signal all sidebars on config change
- [ ] After writing config, send reload signal to all sidebar processes
- [ ] Pattern: `kill -HUP <pid>` for each sidebar

---

## File Changes Summary

| File | Changes |
|------|---------|
| `pkg/config/config.go` | Add SaveConfig, group CRUD, NewGroupButton field |
| `cmd/sidebar/main.go` | Button rendering, click handler, menu items, reload handling |
| `config.yaml` | Add `new_group_button: true` |
| `tabby.tmux` | Add `prefix + G` binding |
| `scripts/manage_group.sh` | New script (optional if doing in Go) |

---

## Default Theme for New Groups

When creating a new group, use these defaults:
```yaml
- name: "{UserInput}"
  pattern: "^{UserInput}\\|"
  theme:
    bg: "#3498db"      # Blue (same as Default)
    fg: "#ecf0f1"
    active_bg: "#2980b9"
    active_fg: "#ffffff"
    icon: ""
```

User can customize via "Edit Group" later.

---

## Testing Checklist

- [ ] Create group via button click
- [ ] Create group via right-click menu
- [ ] Create group via keyboard shortcut
- [ ] Edit group name
- [ ] Edit group color
- [ ] Edit group icon
- [ ] Delete group (windows move to Default)
- [ ] Cannot delete Default group
- [ ] Config persists after tmux restart
- [ ] Multiple sidebars stay in sync

---

## Estimated Complexity

| Phase | Effort | Notes |
|-------|--------|-------|
| Phase 1 | Medium | YAML round-trip is tricky |
| Phase 2 | Easy | Follow existing button pattern |
| Phase 3 | Medium | Menu items + prompt flows |
| Phase 4 | Easy | Simple tmux binding |
| Phase 5 | Easy | Shell script or Go |
| Phase 6 | Medium | Signal handling |

**Total:** Medium-large feature, ~2-3 focused sessions

---

## Open Questions

1. **Color picker UI:** Use predefined colors or hex input?
   - Suggestion: Predefined palette (8-10 colors) for simplicity

2. **Icon picker UI:** Use predefined icons or text input?
   - Suggestion: Common icons list + "Custom..." option

3. **Pattern auto-generation:** Is `^{Name}\\|` still useful?
   - User mentioned "prefixes may not matter anymore"
   - Could skip pattern entirely, just use explicit `@tabby_group` assignment
