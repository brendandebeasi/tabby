# Performance Overhaul & Widget System Plan

## Executive Summary

Two-phase architectural improvement to tmux-tabs:
1. **Phase 1: Performance Overhaul** - Reduce click-to-update latency from ~250ms to <50ms
2. **Phase 2: Widget System** - Abstract sidebar into pluggable widget architecture

---

## Phase 1: Performance Overhaul

### Current State Analysis

**Symptoms:**
- ~250ms latency when clicking tabs before active indicator updates
- Slow propagation of process/state updates
- General architectural inefficiencies

**Likely Bottlenecks:**
1. Tmux command execution (shelling out for each query)
2. Full re-render on every update
3. No caching or diff-based updates
4. Sequential rather than parallel data fetching
5. USR1 signal triggers full refresh cycle

### Architecture Goals

1. **Event-driven updates** - Only update what changed
2. **Batched tmux queries** - Minimize shell-outs
3. **Incremental rendering** - Diff-based UI updates
4. **Optimistic UI** - Update immediately on click, verify async
5. **Caching layer** - Cache stable data (window names, group assignments)

### Design Patterns & Principles

#### Patterns to Apply

1. **Observer Pattern** - For event propagation
   - State changes publish events
   - UI components subscribe to relevant events
   - Decouples data layer from presentation

2. **Command Pattern** - For tmux operations
   - Encapsulate tmux commands as objects
   - Enable queuing, batching, undo
   - Separate command creation from execution

3. **Repository Pattern** - For data access
   - Abstract tmux queries behind repository interface
   - Enable caching, mocking for tests
   - Single source of truth for state

4. **Mediator Pattern** - For component communication
   - Central event bus/mediator
   - Components don't know about each other
   - Reduces coupling

5. **Strategy Pattern** - For rendering optimizations
   - Swap between full render vs incremental
   - Different strategies for different update types

#### SOLID Principles

- **S**ingle Responsibility: Separate concerns (data fetching, caching, rendering, event handling)
- **O**pen/Closed: Extensible for new event types without modifying core
- **L**iskov Substitution: Interfaces for tmux operations (real vs mock)
- **I**nterface Segregation: Small, focused interfaces
- **D**ependency Inversion: Depend on abstractions, not concretions

#### Proposed Package Structure

```
pkg/
  events/           # Event types and bus
    bus.go          # Event bus implementation
    types.go        # Event type definitions

  tmux/
    repository.go   # Repository interface
    client.go       # Real tmux client
    cache.go        # Caching decorator
    batch.go        # Command batching

  state/
    store.go        # Central state store
    diff.go         # State diffing utilities

  render/
    strategy.go     # Render strategy interface
    full.go         # Full render implementation
    incremental.go  # Incremental render implementation
```

#### Key Interfaces

```go
// Event system
type EventBus interface {
    Publish(event Event)
    Subscribe(eventType string, handler EventHandler)
    Unsubscribe(eventType string, handler EventHandler)
}

// Tmux data access
type TmuxRepository interface {
    GetWindows() ([]Window, error)
    GetPanes(windowIndex int) ([]Pane, error)
    GetActiveWindow() (int, error)
    SelectWindow(index int) error
    // ... etc
}

// Caching decorator
type CachedRepository struct {
    inner    TmuxRepository
    cache    *StateCache
    ttl      time.Duration
}

// Command batching
type CommandBatcher interface {
    Queue(cmd TmuxCommand)
    Flush() ([]Result, error)
}

// Render strategy
type RenderStrategy interface {
    Render(state *State, prevState *State) string
    ShouldFullRender(changes []Change) bool
}
```

### Implementation Approach

**Architecture-first methodology:**
1. Design interfaces and contracts before implementation
2. Build skeleton/scaffolding with proper package structure
3. Implement incrementally with tests
4. Refactor existing code to use new architecture
5. Validate performance improvements at each step

**Code quality gates:**
- No direct tmux calls outside repository layer
- All components communicate via events or defined interfaces
- State mutations only through designated pathways
- Each package has clear, documented responsibilities

### Implementation Tasks

#### 1.1 Audit Current Performance
- [ ] Add timing instrumentation to key paths
- [ ] Profile tmux command execution times
- [ ] Profile render cycle times
- [ ] Identify top 3 bottlenecks with data

#### 1.2 Optimize Tmux Communication
- [ ] Batch multiple queries into single tmux command where possible
- [ ] Use `tmux display-message -p` with multiple format specifiers
- [ ] Cache window/pane metadata that rarely changes
- [ ] Implement smart invalidation (only refetch what might have changed)

#### 1.3 Optimistic UI Updates
- [ ] On tab click: immediately update active indicator
- [ ] Send tmux select-window command async
- [ ] Verify state after command completes
- [ ] Rollback on failure (rare edge case)

#### 1.4 Incremental Rendering
- [ ] Track previous render state
- [ ] Diff new state against previous
- [ ] Only re-render changed portions
- [ ] Consider Bubble Tea's built-in optimization patterns

#### 1.5 Event Architecture Refactor
- [ ] Define clear event types (WindowChanged, PaneChanged, etc.)
- [ ] Implement event bus/channel for internal communication
- [ ] Decouple data fetching from rendering
- [ ] Allow targeted updates (e.g., "just update window 3's indicator")

#### 1.6 Parallel Data Fetching
- [ ] Fetch independent data concurrently (goroutines)
- [ ] Use sync.WaitGroup or channels for coordination
- [ ] Timeout handling for stuck queries

### Performance Targets

| Metric | Current | Target |
|--------|---------|--------|
| Click-to-indicator update | ~250ms | <50ms |
| Full sidebar refresh | ? | <100ms |
| Process status update | ? | <100ms |

---

## Phase 2: Widget System

### Architecture Vision

The sidebar becomes a **widget container** that hosts multiple widgets:
- **TabListWidget** - Current window/pane tree (extracted from monolith)
- **SystemStatsWidget** - CPU, memory, etc.
- **NotificationsWidget** - Alert/notification display
- **CustomWidget** - User-defined widgets
- **TamagotchiWidget** - ASCII kitty pet (why not!)

### Core Concepts

#### Widget Interface
```go
type Widget interface {
    // Identity
    ID() string
    Name() string

    // Lifecycle
    Init() tea.Cmd
    Update(msg tea.Msg) (Widget, tea.Cmd)
    View() string

    // Layout
    Height() int              // Current height (0 = flexible)
    MinHeight() int           // Minimum height
    MaxHeight() int           // Maximum height (0 = unlimited)

    // State
    IsCollapsed() bool
    SetCollapsed(bool)

    // Persistence
    SaveState() ([]byte, error)
    LoadState([]byte) error
}
```

#### Widget Container
```go
type WidgetContainer struct {
    widgets    []Widget
    order      []string        // Widget IDs in display order
    focus      int             // Currently focused widget index
    height     int             // Total available height
}
```

#### Widget Registry
```go
type WidgetRegistry struct {
    builtIn   map[string]WidgetFactory
    external  map[string]ExternalWidgetConfig
}

type WidgetFactory func(config map[string]any) Widget

type ExternalWidgetConfig struct {
    Command   string            // Script/binary to execute
    Interval  time.Duration     // Refresh interval
    Height    int               // Fixed height for output
}
```

### Widget Types

#### 1. Built-in Go Widgets (Preferred)
- Compiled into sidebar binary
- Full Bubble Tea integration
- Access to tmux state, system state
- Can persist state

#### 2. External Script Widgets
- Execute external command
- Capture stdout as widget content
- Support ANSI colors
- Refresh on interval or trigger
- Sandboxed (no direct state access)

### Implementation Tasks

#### 2.1 Extract TabListWidget
- [ ] Define Widget interface
- [ ] Refactor current tab list code into TabListWidget
- [ ] Ensure all existing functionality preserved
- [ ] Add collapse/expand support for the widget itself

#### 2.2 Build WidgetContainer
- [ ] Implement container that manages multiple widgets
- [ ] Handle vertical layout with flexible sizing
- [ ] Route events to appropriate widgets
- [ ] Manage focus between widgets

#### 2.3 Widget Configuration
- [ ] Add `widgets` section to config.yaml
- [ ] Define widget order, enabled/disabled
- [ ] Per-widget configuration options
- [ ] Hot-reload widget config on USR1

#### 2.4 Widget State Persistence
- [ ] Define state storage location (~/.tmux-tabs/state/)
- [ ] Save widget state on sidebar close
- [ ] Restore widget state on sidebar open
- [ ] Per-session vs global state options

#### 2.5 Inter-Widget Communication
- [ ] Define message types widgets can broadcast
- [ ] Implement pub/sub or event bus
- [ ] Allow widgets to subscribe to specific events
- [ ] Example: TabList publishes "window-changed", Stats widget updates

#### 2.6 External Widget Support
- [ ] Implement ExternalWidget wrapper
- [ ] Execute command, capture output
- [ ] Handle refresh intervals
- [ ] ANSI color passthrough
- [ ] Error handling (command fails, timeout)

#### 2.7 Built-in Widgets
- [ ] SystemStatsWidget (CPU, memory, load)
- [ ] ClockWidget (simple clock display)
- [ ] NotificationsWidget (queue of alerts)
- [ ] TamagotchiWidget (ASCII pet - stretch goal)

### Configuration Example

```yaml
widgets:
  order:
    - tabs
    - stats
    - notifications
    - tamagotchi

  tabs:
    enabled: true
    collapsed: false
    # existing sidebar config moves here

  stats:
    enabled: true
    show_cpu: true
    show_memory: true
    refresh_interval: 5s

  notifications:
    enabled: true
    max_visible: 3
    auto_dismiss: 30s

  tamagotchi:
    enabled: false
    pet_name: "Whiskers"
    state_file: "~/.tmux-tabs/whiskers.json"

  custom:
    - name: "git-status"
      command: "git status --short 2>/dev/null | head -5"
      interval: 10s
      height: 6
```

---

## Implementation Order

### Pre-Implementation
1. **Commit and push all current changes**
2. Create feature branch: `feat/performance-and-widgets`

### Phase 1: Performance (Estimated: 2-3 sessions)
1. Audit and instrument (gather baseline data)
2. Optimize tmux communication (biggest win expected)
3. Implement optimistic UI for clicks
4. Add caching layer
5. Refactor event architecture
6. Validate targets met

### Phase 2: Widgets (Estimated: 3-4 sessions)
1. Define Widget interface
2. Extract TabListWidget (largest refactor)
3. Build WidgetContainer
4. Add configuration support
5. Implement state persistence
6. Add external widget support
7. Build 1-2 example widgets
8. (Stretch) Tamagotchi widget

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Performance targets not achievable | High | Profile first, set realistic targets based on data |
| Widget refactor breaks existing functionality | High | Comprehensive testing before/after, feature flags |
| External widgets security concerns | Medium | Sandboxing, no direct state access, timeout enforcement |
| Scope creep with widget features | Medium | MVP widget system first, iterate |

---

## Success Criteria

### Phase 1: Performance
- [ ] Click-to-indicator update <50ms (measured)
- [ ] No regression in functionality
- [ ] Clean event-driven architecture documented

### Phase 2: Widgets
- [ ] TabListWidget works identically to current sidebar
- [ ] At least 2 built-in widgets functional
- [ ] External widget support working
- [ ] State persistence working
- [ ] Configuration documented

---

## Notes

- Both phases focus on **architecture first** per user request
- Follow Go/Bubble Tea best practices
- Keep widget interface minimal but extensible
- Performance optimizations should not sacrifice code clarity
