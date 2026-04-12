# Tabby Renderer Architecture: Feature Set + Engine Design

## Part 1: Complete Feature Set

Everything tabby should be able to do, organized by category. Features marked with their current status.

---

### 1. Window Management

| Feature | Status | Description |
|---------|--------|-------------|
| Tiled pane layouts | DONE (tmux native) | Standard tmux horizontal/vertical splits |
| Window groups with colors | DONE | Windows organized into named, color-coded groups |
| Window history navigation | DONE | Close window -> return to previously viewed, not adjacent |
| Layout save/restore | DONE | Kill pane -> remaining panes preserve ratios |
| Pane dimming | DONE | Inactive panes dim, active pane bright, system panes excluded |
| Border color from group | DONE | Active border matches tab group color |
| Auto-zoom on phone | STUBBED | Zoom active pane on phone, unzoom on desktop return |
| Carousel navigation | DONE | Prev/next pane cycling via fat tap targets |
| Floating panes | COMPOSITOR | Overlay panes not tied to tmux layout (requires compositor validation) |
| Stacked/tabbed panes | NEW | Multiple panes sharing the same screen area, tab-switched |

### 2. Chrome / UI Elements

| Feature | Status | Description |
|---------|--------|-------------|
| Sidebar tree view | DONE | Hierarchical window/pane list with group headers |
| Sidebar git status | DONE | Branch, dirty state, ahead/behind per pane |
| Sidebar pet widget | DONE | Interactive animated pet in sidebar |
| Sidebar collapse/expand | DONE | Toggle sidebar visibility, collapse to 1-col strip |
| Responsive sidebar width | DONE | Width adapts to mobile/tablet/desktop breakpoints |
| Pane headers (desktop) | DONE | Single-line header showing pane title, cwd, indicators |
| Pane headers (phone) | DONE | 2-3 row header with hamburger, carousel controls |
| Hamburger menu | DONE | Fat tappable button opens sidebar as popup |
| Context menus | DONE | Right-click menus on windows, panes, groups |
| Color picker | DONE | Inline color selection for groups |
| Marker picker | DONE | Icon/marker selection for groups |
| Custom borders | NEW | Colored, rounded, thick, labeled borders per pane |
| Rich tab bar | NEW | Tab bar with icons, activity badges, group indicators |
| Inline status badges | PARTIAL | Bell, input, AI indicators in headers |
| Breadcrumb bar | NEW | Hierarchical path: session > group > window > pane |

### 3. Transient / Overlay UI

| Feature | Status | Description |
|---------|--------|-------------|
| Sidebar popup (phone) | DONE | Full-screen display-popup hosting sidebar |
| Notification toasts | NEW | Auto-dismiss popups for build completion, errors, alerts |
| Pane picker | DESIGNED | Jump-to-any-pane list with command/cwd display |
| Command palette | NEW | Fuzzy finder for all tabby actions (a la Ctrl+Shift+P) |
| Tooltips | NEW | Hover info on pane headers, sidebar items |
| Quick switcher | NEW | Fast window/group switch with type-ahead filtering |
| Confirmation dialogs | NEW | "Kill this window?" with accept/cancel |

### 4. Overview / Pane Capture

| Feature | Status | Description |
|---------|--------|-------------|
| Overview popup | NEW | Grid of pane thumbnails via capture-pane, launched as display-popup |
| Pane capture widget | NEW | Snapshot a pane's content and display it in the sidebar or another window |
| Activity indicators | PARTIAL | Visual indicators for output activity, bells, errors |
| Click-to-jump | NEW | Select a thumbnail to navigate to that pane/window |

### 5. Responsive Design

All clients share a single tmux session and see the same pane layout. Panes resize to the active client's terminal size (standard tmux behavior). Tabby's chrome (sidebar, headers, overlays) adapts per profile.

| Feature | Status | Description |
|---------|--------|-------------|
| Phone detection (< 80 cols) | DONE | Width-based client classification |
| Desktop detection (>= 80 cols) | DONE | Default profile |
| Tablet tier (80-170 cols) | PARTIAL | Sidebar width adjusts, no distinct layout |
| Profile hysteresis | NEW | 5-col buffer prevents oscillation at breakpoints |
| Per-profile sidebar content | NEW | Different sidebar density/widgets per profile |
| Per-profile header style | DONE | Phone gets interactive 3-row, desktop gets 1-row |

### 6. Rich Rendering

| Feature | Status | Description |
|---------|--------|-------------|
| Lipgloss styled content | DONE | ANSI color/style rendering via lipgloss |
| Color profile adaptation | DONE | TrueColor, ANSI256, ANSI, Ascii detection |
| Theme system | DONE | Sidebar/terminal background colors, group colors |
| Animated elements | DONE | Pet widget animations, sprite system |
| Kitty graphics protocol | PLANNED | Image rendering in Kitty-compatible terminals |
| Sixel graphics | NEW | Image rendering in Sixel-compatible terminals |
| Unicode density rendering | NEW | Half-blocks, braille for overview thumbnails |
| Synchronized output | NEW | `\033[?2026h` to prevent tearing during redraws |

### 7. Input System

| Feature | Status | Description |
|---------|--------|-------------|
| Mouse click regions | DONE | ClickableRegion hit testing in renderers |
| Right-click context menus | DONE | Region-aware context menus |
| Long-press detection | DONE | 350ms threshold, simulated right-click |
| Double-tap detection | DONE | 600ms window, simulated right-click |
| Mobile touch zones | DONE | Rightmost 3 cols auto-trigger context menu |
| Scroll (viewport) | DONE | Scrollable sidebar with viewport tracking |
| Keyboard shortcuts | PARTIAL | Key forwarding to daemon, limited mappings |
| Drag interactions | NEW | Drag to resize panes, reorder tabs, move floating panes |
| Gesture detection | NEW | Swipe to cycle panes (terminal-dependent) |
| Input routing (compositor) | COMPOSITOR | Route input to correct layer/surface |

### 8. Integration / External

| Feature | Status | Description |
|---------|--------|-------------|
| Web bridge | PLANNED | WebSocket-based browser UI |
| tmux control mode | NEW | Synchronous event handling via `tmux -C` |
| LLM integration | DONE | AI indicators, context awareness |
| Session persistence | PARTIAL | Layout/state recovery on daemon restart |

---

## Part 2: Renderer Engine Architecture

### Design Principles

1. **The daemon renders; surfaces display.** Content generation lives in the daemon. Surfaces are dumb pipes that receive frames and forward input. This is already true today and stays true.

2. **Surfaces are interchangeable.** The same widget content can be delivered to a tmux pane, a tmux border, a popup, a web client, or a compositor. The surface doesn't change the content semantics, only the delivery mechanism and rendering constraints.

3. **Widgets are composable.** A sidebar is a widget. A header is a widget. A notification is a widget. The overview grid is a widget containing thumbnail sub-widgets. Layout determines which widgets appear where.

4. **One session, one pane layout.** All clients see the same tmux pane layout (standard tmux behavior). Tabby adapts its own chrome (sidebar, headers, overlays) per client profile, but pane content and tiling are shared.

5. **Input flows back through the same path.** Surface receives input -> routes to widget -> widget produces semantic action -> coordinator handles action.

---

### Core Abstractions

```
┌──────────────────────────────────────────────────────────┐
│                     Coordinator                          │
│              (application state, business logic)         │
│                                                          │
│  windows[] groups[] panes[] clientProfiles[] pets[]      │
└─────────────────────┬────────────────────────────────────┘
                      │ state queries
          ┌───────────▼────────────┐
          │    Widget Registry     │
          │                        │
          │  sidebar    header     │
          │  overview   notification│
          │  picker     palette    │
          │  tabbar     breadcrumb │
          └───────────┬────────────┘
                      │ WidgetFrame[]
          ┌───────────▼────────────┐
          │    Layout Engine       │
          │  (per ClientProfile)   │
          │                        │
          │  phone:   carousel     │
          │  tablet:  compact      │
          │  desktop: full         │
          └───────────┬────────────┘
                      │ SurfaceAssignment[]
          ┌───────────▼────────────┐
          │    Surface Router      │
          │                        │
          │  assigns widgets to    │
          │  concrete surfaces     │
          └──┬───┬───┬───┬───┬────┘
             │   │   │   │   │
           Pane Pop Bdr Web Comp
```

### 1. Widget Interface

A Widget produces visual content for a region of the UI. The daemon owns all widgets.

```go
// Widget produces renderable content for a UI element.
type Widget interface {
    // ID returns a unique identifier (e.g., "sidebar", "header:@1:%5")
    ID() string

    // Type returns the widget category for layout decisions.
    Type() WidgetType

    // Render produces a frame for the given constraints.
    // The same widget may be rendered multiple times at different sizes
    // for different clients.
    Render(ctx RenderContext) *WidgetFrame

    // HandleInput processes a semantic input event.
    // Returns true if the event was consumed and a re-render is needed.
    HandleInput(input *InputEvent) bool

    // Capabilities returns what this widget needs from a surface.
    Capabilities() WidgetCapabilities
}

type WidgetType string

const (
    WidgetSidebar      WidgetType = "sidebar"
    WidgetHeader       WidgetType = "header"
    WidgetOverview     WidgetType = "overview"
    WidgetNotification WidgetType = "notification"
    WidgetPicker       WidgetType = "picker"
    WidgetPalette      WidgetType = "palette"
    WidgetTabbar       WidgetType = "tabbar"
    WidgetBreadcrumb   WidgetType = "breadcrumb"
    WidgetCustom       WidgetType = "custom"
)

type WidgetCapabilities struct {
    NeedsMouse    bool // Widget has clickable regions
    NeedsScroll   bool // Widget has scrollable content
    NeedsGraphics bool // Widget uses Kitty/Sixel images
    NeedsOverlay  bool // Widget renders on top of other content
    MinWidth      int  // Minimum usable width
    MinHeight     int  // Minimum usable height
    Transient     bool // Widget auto-dismisses (notification, tooltip)
}
```

### 2. WidgetFrame

The output of a Widget.Render() call. This replaces/extends the current `RenderPayload`.

```go
// WidgetFrame is the rendered output of a widget for specific constraints.
type WidgetFrame struct {
    // Identity
    WidgetID string
    Sequence uint64 // Monotonic, for race detection

    // Content (multiple formats for different surfaces)
    Content     ContentBlock   // Primary scrollable content
    Pinned      ContentBlock   // Pinned (non-scrollable) content
    Simplified  string         // Simplified version for border-format surfaces
    Structured  *StructuredContent // Structured version for web surfaces

    // Layout hints
    DesiredWidth  int // Widget's preferred width (0 = flexible)
    DesiredHeight int // Widget's preferred height (0 = flexible)
    TotalLines    int // Total scrollable lines
    PinnedHeight  int // Height of pinned section

    // Interactivity
    Regions       []ClickableRegion // Clickable areas in scrollable content
    PinnedRegions []ClickableRegion // Clickable areas in pinned content

    // Visual metadata
    Background string // Hex color for surface background
    ZIndex     int    // Layering order (0 = base, higher = on top)

    // Viewport suggestion
    ViewportOffset int // Suggested scroll position
}

// ContentBlock holds pre-rendered content in one or more formats.
type ContentBlock struct {
    ANSI string // Pre-rendered ANSI string (for terminal surfaces)
    // Future: add HTML, Kitty, Sixel variants as needed
}

// StructuredContent for web/API surfaces that need semantic data, not ANSI.
type StructuredContent struct {
    Windows []WindowData
    Panes   []PaneData
    Groups  []GroupData
    // ... structured representations of sidebar/header content
}
```

### 3. RenderContext

Passed to Widget.Render() to describe the constraints.

```go
// RenderContext provides rendering constraints and client info.
type RenderContext struct {
    // Dimensions available for this widget
    Width  int
    Height int

    // Client information
    ClientID      string
    ClientProfile string // "phone", "tablet", or "desktop"
    ColorProfile  string // "TrueColor", "ANSI256", "ANSI", "Ascii"

    // Surface capabilities (what the target surface can handle)
    SurfaceType    SurfaceType
    SupportsGraphics bool
    SupportsMouse    bool

    // Viewport state (for scrollable widgets)
    ViewportOffset int

    // Coordinator state access (read-only snapshot)
    State *StateSnapshot
}

// StateSnapshot is a read-only view of coordinator state for rendering.
// Avoids holding locks during render. Populated once per render cycle.
type StateSnapshot struct {
    Windows        []WindowState
    Groups         []GroupState
    ActiveWindowID string
    ActivePaneID   string
    PetState       *PetState
    GitState       map[string]*GitInfo
    // ... other state needed by widgets
}
```

### 4. Surface Interface

A Surface is a delivery target for rendered frames.

```go
// Surface delivers rendered frames to a display target.
type Surface interface {
    // ID returns a unique identifier.
    ID() string

    // Type returns the surface backend type.
    Type() SurfaceType

    // Capabilities returns what this surface supports.
    Capabilities() SurfaceCapabilities

    // Deliver sends a rendered frame to the display.
    Deliver(frame *WidgetFrame) error

    // Dimensions returns current width and height.
    Dimensions() (width, height int)

    // SetInputHandler registers a callback for input from this surface.
    SetInputHandler(handler func(input *InputEvent))

    // Close tears down the surface and releases resources.
    Close() error

    // Alive returns false if the surface has disconnected or crashed.
    Alive() bool
}

type SurfaceType string

const (
    SurfaceTmuxPane   SurfaceType = "tmux-pane"    // BubbleTea process in split-window
    SurfaceTmuxPopup  SurfaceType = "tmux-popup"   // BubbleTea process in display-popup
    SurfaceTmuxBorder SurfaceType = "tmux-border"  // pane-border-format string
    SurfaceWebSocket  SurfaceType = "websocket"    // JSON over WebSocket to browser
    SurfaceOverlay    SurfaceType = "overlay"       // Compositor overlay region
)

type SurfaceCapabilities struct {
    SupportsColor    bool
    SupportsGraphics bool   // Kitty/Sixel
    SupportsMouse    bool   // Click events
    SupportsScroll   bool   // Scroll events
    SupportsResize   bool   // Dynamic resize notifications
    SupportsOverlay  bool   // Can render on top of other content
    MaxFPS           int    // Render rate limit
    ColorProfile     string
}
```

### 5. Surface Implementations

#### TmuxPaneSurface (current model)

What exists today: BubbleTea process in a tmux split-window pane, connected via Unix socket.

```go
type TmuxPaneSurface struct {
    id         string
    paneID     string       // tmux pane ID
    clientID   string       // daemon client ID
    conn       net.Conn     // Unix socket to BubbleTea process
    width      int
    height     int
    colorProf  string
    inputCb    func(*InputEvent)
    lastHash   uint32       // Content dedup
}

// Deliver sends RenderPayload over Unix socket (current MsgRender flow).
// The BubbleTea renderer (sidebar-renderer, pane-header) receives and displays it.
func (s *TmuxPaneSurface) Deliver(frame *WidgetFrame) error {
    // Convert WidgetFrame to current RenderPayload format for backwards compat
    // Send as MsgRender over Unix socket
    // Dedup on content hash
}
```

Handles: sidebar, header (phone), popup, overview, picker, palette, notifications.

#### TmuxBorderSurface (new -- Phase E)

Uses `pane-border-format` for zero-process desktop headers.

```go
type TmuxBorderSurface struct {
    id       string
    paneID   string // tmux pane for which this border renders
    width    int
    runner   tmux.Runner
}

// Deliver sets pane-border-format on the target pane.
// Uses WidgetFrame.Simplified (tmux format string, not ANSI).
func (s *TmuxBorderSurface) Deliver(frame *WidgetFrame) error {
    // tmux set-option -p -t {paneID} pane-border-format {frame.Simplified}
    // Only writes if content changed (dedup)
}

// No mouse support. SupportsMouse = false.
// No scroll support.
// Cannot deliver ANSI content -- only tmux format strings.
```

Handles: desktop pane headers (simplified display-only).

#### TmuxPopupSurface (exists, formalize)

Wraps `tmux display-popup` with lifecycle management.

```go
type TmuxPopupSurface struct {
    id        string
    sessionID string
    process   *exec.Cmd  // The popup BubbleTea process
    conn      net.Conn   // Unix socket (once popup connects)
    width     int
    height    int
    timeout   time.Duration // Auto-dismiss (0 = manual close)
    inputCb   func(*InputEvent)
}

// Deliver sends frame over Unix socket to popup process.
// If popup is dead, respawn it.
func (s *TmuxPopupSurface) Deliver(frame *WidgetFrame) error { ... }

// Close kills the popup process and closes the tmux popup.
func (s *TmuxPopupSurface) Close() error { ... }
```

Handles: phone sidebar, pane picker, notifications, command palette, overview popup.

#### WebSocketSurface (new -- for web bridge)

```go
type WebSocketSurface struct {
    id       string
    ws       *websocket.Conn
    width    int
    height   int
    inputCb  func(*InputEvent)
}

// Deliver sends WidgetFrame.Structured as JSON over WebSocket.
// Does NOT send ANSI -- sends semantic data the browser renders.
func (s *WebSocketSurface) Deliver(frame *WidgetFrame) error { ... }
```

Handles: web UI, custom dashboards, remote monitoring.

#### OverlaySurface (future -- compositor)

```go
type OverlaySurface struct {
    id       string
    region   ScreenRegion // Row, Col, Width, Height on the terminal
    zIndex   int
    renderer *CompositorProcess
    inputCb  func(*InputEvent)
}

type ScreenRegion struct {
    Row    int
    Col    int
    Width  int
    Height int
}

// Deliver sends positioned content to the compositor process.
// Content is painted at the specified ScreenRegion using CSI cursor positioning.
func (s *OverlaySurface) Deliver(frame *WidgetFrame) error { ... }
```

Handles: floating UI elements, custom borders, notification overlays.

---

### 6. Layout Engine

The Layout Engine decides which widgets appear on which surfaces for a given client.

```go
// LayoutEngine computes widget-to-surface assignments for a client.
type LayoutEngine interface {
    // Compute takes the available screen geometry and client profile,
    // and returns assignments of widgets to surfaces.
    Compute(ctx LayoutContext) []SurfaceAssignment
}

type LayoutContext struct {
    ClientID      string
    ClientProfile string          // "phone", "tablet", or "desktop"
    ScreenWidth   int
    ScreenHeight  int
    ActiveWindow  string
    WindowCount   int
    PaneCount     int             // Panes in active window
    AvailableSurfaces []SurfaceType // What surface types this client supports
    Config        *LayoutConfig   // User preferences
}

type SurfaceAssignment struct {
    Widget      Widget
    SurfaceType SurfaceType
    Region      ScreenRegion    // Where on screen (for compositor/overlay)
    Width       int             // Allocated width
    Height      int             // Allocated height
    Priority    int             // Render priority (higher = render first)
}

type LayoutConfig struct {
    // Per-profile overrides
    Profiles map[string]ProfileLayout

    // Breakpoints (configurable)
    PhoneMaxCols    int // default: 80
    TabletMaxCols   int // default: 170
    // Above TabletMaxCols = desktop

    // Hysteresis buffer
    ProfileSwitchBuffer int // default: 5 cols
}

type ProfileLayout struct {
    Sidebar        SidebarLayout
    Header         HeaderLayout
    Overview       *OverviewLayout // nil = not shown
    Notifications  NotificationLayout
}

type SidebarLayout struct {
    Visible     bool
    Position    string // "left", "right"
    Width       int    // 0 = auto
    MinWidth    int
    MaxWidth    int
    Collapsed   bool   // Start collapsed
    Surface     SurfaceType // which surface to use
}

type HeaderLayout struct {
    Visible     bool
    Height      int    // rows
    Surface     SurfaceType
    Interactive bool   // show clickable regions
}
```

#### Built-in Layout Profiles

```go
// PhoneLayout: carousel mode, minimal chrome
var PhoneLayout = ProfileLayout{
    Sidebar: SidebarLayout{
        Visible: false, // accessed via hamburger popup
    },
    Header: HeaderLayout{
        Visible:     true,
        Height:      3,
        Surface:     SurfaceTmuxPane, // needs touch interaction
        Interactive: true,
    },
    Notifications: NotificationLayout{
        Position: "top",
        Surface:  SurfaceTmuxPopup,
    },
}

// TabletLayout: compact sidebar, interactive headers
var TabletLayout = ProfileLayout{
    Sidebar: SidebarLayout{
        Visible:  true,
        Position: "left",
        Width:    20,
        Surface:  SurfaceTmuxPane,
    },
    Header: HeaderLayout{
        Visible:     true,
        Height:      1,
        Surface:     SurfaceTmuxBorder, // no mouse needed
        Interactive: false,
    },
}

// DesktopLayout: full sidebar, border-format headers
var DesktopLayout = ProfileLayout{
    Sidebar: SidebarLayout{
        Visible:  true,
        Position: "left",
        Width:    0, // auto (responsive based on window width)
        Surface:  SurfaceTmuxPane,
    },
    Header: HeaderLayout{
        Visible:     true,
        Height:      1,
        Surface:     SurfaceTmuxBorder,
        Interactive: false,
    },
}
```

---

### 7. Input Router

Routes input from surfaces back through widgets to the coordinator.

```go
// InputEvent is the unified input type across all surfaces.
type InputEvent struct {
    // Source
    SurfaceID string
    WidgetID  string // Which widget the surface is displaying

    // Event
    Type      InputType // mouse, key, action, scroll
    MouseX    int
    MouseY    int
    Button    string    // left, right, middle, wheelup, wheeldown
    Action    string    // press, release, drag
    Key       string    // For keyboard events
    Modifiers []string  // ctrl, alt, shift

    // Resolved (after region matching)
    ResolvedAction string
    ResolvedTarget string

    // Context
    SequenceNum    uint64 // Render frame this input references
    ViewportOffset int    // Current scroll position
    ClientProfile  string

    // Mobile extensions
    IsSimulatedRightClick bool
    IsLongPress           bool
    IsDoubleTap           bool
}

type InputType string

const (
    InputMouse  InputType = "mouse"
    InputKey    InputType = "key"
    InputAction InputType = "action"
    InputScroll InputType = "scroll"
    InputDrag   InputType = "drag"
)

// InputRouter manages input flow from surfaces to widgets.
type InputRouter struct {
    // Surface -> Widget mapping (from layout engine)
    assignments map[string]string // surfaceID -> widgetID

    // Widget handlers
    widgets map[string]Widget

    // Fallback handler (coordinator)
    fallback func(*InputEvent) bool
}

// Route processes an input event from a surface.
func (r *InputRouter) Route(event *InputEvent) {
    // 1. Look up which widget this surface is displaying
    widgetID := r.assignments[event.SurfaceID]
    event.WidgetID = widgetID

    // 2. Let the widget handle it
    if widget, ok := r.widgets[widgetID]; ok {
        if widget.HandleInput(event) {
            return // consumed
        }
    }

    // 3. Fall back to coordinator for global actions
    r.fallback(event)
}
```

---

### 8. Render Pipeline

The complete render cycle from state change to pixels.

```go
// RenderPipeline orchestrates the full render cycle.
type RenderPipeline struct {
    coordinator  *Coordinator
    widgets      *WidgetRegistry
    layouts      map[string]LayoutEngine  // per profile
    surfaces     *SurfaceManager
    inputRouter  *InputRouter
    batchDelay   time.Duration // 16ms
}

// OnStateChange is called when coordinator state changes.
// Triggers a batched render cycle.
func (p *RenderPipeline) OnStateChange() {
    // Debounced: coalesce rapid changes into single render
    p.scheduleBatchRender()
}

// renderCycle executes one full render pass.
func (p *RenderPipeline) renderCycle() {
    // 1. Snapshot coordinator state (minimizes lock time)
    state := p.coordinator.Snapshot()

    // 2. For each connected client, compute layout
    for _, client := range p.surfaces.Clients() {
        profile := p.coordinator.ProfileForClient(client.ID)
        layout := p.layouts[profile]
        assignments := layout.Compute(LayoutContext{
            ClientID:      client.ID,
            ClientProfile: profile,
            ScreenWidth:   client.Width,
            ScreenHeight:  client.Height,
            ActiveWindow:  state.ActiveWindowID,
            WindowCount:   len(state.Windows),
            PaneCount:     state.ActivePaneCount,
        })

        // 3. Render each assigned widget at its allocated size
        for _, assignment := range assignments {
            frame := assignment.Widget.Render(RenderContext{
                Width:         assignment.Width,
                Height:        assignment.Height,
                ClientID:      client.ID,
                ClientProfile: profile,
                ColorProfile:  client.ColorProfile,
                SurfaceType:   assignment.SurfaceType,
                State:         state,
            })

            // 4. Deliver frame to the assigned surface
            surface := p.surfaces.Get(client.ID, assignment.SurfaceType)
            if surface != nil && surface.Alive() {
                surface.Deliver(frame)
            }
        }
    }
}
```

---

### 9. Surface Manager

Manages the lifecycle of all surfaces across all clients.

```go
// SurfaceManager creates, tracks, and destroys surfaces.
type SurfaceManager struct {
    surfaces   map[string]Surface          // surfaceID -> Surface
    byClient   map[string][]string         // clientID -> []surfaceID
    byType     map[SurfaceType][]string    // type -> []surfaceID
    mu         sync.RWMutex

    // Factory functions for each surface type
    factories  map[SurfaceType]SurfaceFactory

    // Lifecycle hooks
    onSurfaceCreated   func(Surface)
    onSurfaceDestroyed func(Surface)
}

type SurfaceFactory func(config SurfaceConfig) (Surface, error)

type SurfaceConfig struct {
    ID        string
    ClientID  string
    Type      SurfaceType
    Width     int
    Height    int
    Region    ScreenRegion // For overlay surfaces
    Timeout   time.Duration // For transient surfaces
    PaneID    string       // For tmux-pane/border surfaces
    SessionID string       // For popup surfaces
}

// Reconcile ensures the active surfaces match the layout assignments.
// Creates new surfaces, destroys orphaned ones.
func (m *SurfaceManager) Reconcile(clientID string, assignments []SurfaceAssignment) {
    // 1. Build set of desired surfaces from assignments
    // 2. Compare against existing surfaces for this client
    // 3. Create missing surfaces via factories
    // 4. Destroy surfaces that are no longer assigned
    // 5. Resize surfaces whose dimensions changed
}
```

---

### 10. Migration Path from Current Architecture

The renderer architecture must be adoptable incrementally. No big-bang rewrite.

#### Step 1: Extract Widget Interface (wrap existing code)
- `SidebarWidget` wraps `RenderForClient()` -- calls the existing method, returns `WidgetFrame`
- `HeaderWidget` wraps `RenderHeaderForClient()` -- same pattern
- No behavior change. The widget interface is an adapter over existing code.

#### Step 2: Extract Surface Interface (wrap existing server.go)
- `TmuxPaneSurface` wraps the existing Unix socket client connection
- `sendRenderToClientImmediate()` becomes `TmuxPaneSurface.Deliver()`
- Server.go's client map becomes SurfaceManager
- No behavior change. The surface interface is an adapter over existing code.

#### Step 3: Introduce Layout Engine
- Start with a single `DefaultLayout` that replicates current behavior exactly
- Phone clients get current phone layout. Desktop gets current desktop layout.
- Layout decisions move out of coordinator into the layout engine.

#### Step 4: Add new surface types
- `TmuxBorderSurface` for desktop headers (Phase E from roadmap)
- `TmuxPopupSurface` formalized with lifecycle management
- Each new surface type is additive -- existing surfaces keep working.

#### Step 5: Add new widgets
- `OverviewWidget` for pane thumbnail popup
- `NotificationWidget` for toasts
- `PaletteWidget` for command palette
- Each widget is independent -- doesn't affect existing widgets.

#### Step 6: Refine layout profiles
- Tune phone, tablet, desktop profiles based on real usage
- Add config overrides for sidebar width, header style, etc.

---

### 11. Protocol Evolution

The current protocol (`pkg/daemon/protocol.go`) evolves to support the new architecture.

```go
// Extended message types
const (
    // Existing (unchanged)
    MsgSubscribe      = "subscribe"
    MsgRender         = "render"
    MsgInput          = "input"
    MsgResize         = "resize"
    MsgViewportUpdate = "viewport_update"
    MsgMenu           = "menu"
    MsgMenuSelect     = "menu_select"
    MsgPing           = "ping"
    MsgPong           = "pong"

    // New
    MsgCapabilities   = "capabilities"   // Surface -> Daemon: what I support
    MsgLayoutChange   = "layout_change"  // Daemon -> Surface: layout updated
    MsgSurfaceReady   = "surface_ready"  // Surface -> Daemon: ready to receive
    MsgSurfaceClose   = "surface_close"  // Daemon -> Surface: shut down
)

// Extended subscribe with version and capabilities
type SubscribePayload struct {
    // Existing fields
    Width        int    `json:"width"`
    Height       int    `json:"height"`
    ColorProfile string `json:"color_profile"`
    PaneID       string `json:"pane_id"`

    // New fields
    ProtocolVersion int               `json:"protocol_version"` // 1 = legacy, 2 = new
    SurfaceType     SurfaceType       `json:"surface_type"`
    Capabilities    SurfaceCapabilities `json:"capabilities"`
}
```

Version negotiation: renderer sends `ProtocolVersion` in subscribe. Daemon responds with frames in the appropriate format. Version 1 = current `RenderPayload`. Version 2 = `WidgetFrame`. Backwards compatible -- old renderers keep working until replaced.

---

### 12. Responsive Design Integration

The layout engine is the central point for all responsive behavior.

```
Client connects
    │
    ▼
ComputeProfile(width, height)     ◄── with hysteresis
    │
    ▼
SelectLayout(profile, config)      ◄── user overrides apply here
    │
    ▼
Layout.Compute(context)            ◄── widget assignments
    │
    ▼
SurfaceManager.Reconcile()         ◄── create/destroy surfaces
    │
    ▼
RenderPipeline.renderCycle()       ◄── render widgets at assigned sizes
    │
    ▼
Surface.Deliver()                  ◄── send frames to displays
```

Profile transitions (e.g., phone -> desktop on resize):
1. `computeProfile()` detects new profile (with hysteresis)
2. Layout engine computes new assignments
3. Surface manager reconciles: destroys phone-only surfaces (popup sidebar), creates desktop-only surfaces (border headers)
4. Render pipeline runs with new assignments
5. All happens within one render cycle (~16ms batch window)

---

### 13. Overview Widget Design

A popup that shows a grid of pane thumbnails for quick navigation. Launched via keyboard shortcut or sidebar button. Opens as a `display-popup`, not a persistent view.

```go
type OverviewWidget struct {
    thumbnails map[string]*Thumbnail // paneID -> captured content
    selected   int                   // Currently highlighted thumbnail
    gridCols   int                   // Computed from popup width
    gridRows   int                   // Computed from popup height
}

type Thumbnail struct {
    PaneID     string
    WindowID   string
    Command    string
    CWD        string
    Content    []string // Raw captured lines from tmux capture-pane
    Downscaled string   // Rendered at thumbnail size using half-blocks
    Active     bool     // Currently active pane?
}

func (w *OverviewWidget) Render(ctx RenderContext) *WidgetFrame {
    // 1. Capture visible panes via tmux capture-pane -t <pane> -p -e
    // 2. Downscale each using half-block characters (2x vertical density)
    // 3. Arrange in grid with command label per thumbnail
    // 4. Highlight active pane, arrow keys to navigate, enter to jump
    // 5. Build ClickableRegions for each thumbnail
}
```

Practical limits: ~20 panes before capture overhead matters. Beyond that, show badge-only (command + activity dot) for less-recent panes.

---

### 14. Leveraging Existing TUI Libraries

Widgets should use existing Go TUI libraries internally -- not reinvent rendering primitives. The daemon already depends on most of these.

**Already in use:**
- **[lipgloss](https://github.com/charmbracelet/lipgloss)** -- Styling, colors, borders, layout. Used throughout `RenderForClient` and `RenderHeaderForClient` for all styled output. Widgets continue using lipgloss for all content styling.
- **[bubblezone](https://github.com/lrstanley/bubblezone)** -- Clickable region marking. Used to create `ClickableRegion` entries in `RenderPayload`. Widgets use `zone.Mark()` during render, zones are extracted into regions automatically.
- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)** -- TUI framework powering all renderer processes (sidebar-renderer, pane-header, popup). New popup-based widgets (overview, pane picker, command palette, notifications) are Bubble Tea apps.
- **[lipgloss/table](https://github.com/charmbracelet/lipgloss)** -- Table rendering for structured data display.

**Available to adopt:**
- **[glamour](https://github.com/charmbracelet/glamour)** -- Markdown rendering. Useful for notification content, help text, or documentation widgets.
- **[huh](https://github.com/charmbracelet/huh)** -- Form/input components (text inputs, selects, confirms). Useful for command palette, confirmation dialogs, settings UI.
- **[bubbles](https://github.com/charmbracelet/bubbles)** -- Component library (list, textinput, viewport, spinner, progress, paginator, filepicker). Key components:
  - `list` -- for pane picker, command palette, window switcher
  - `textinput` -- for palette search, rename dialogs
  - `viewport` -- already used implicitly; could formalize for scrollable widgets
  - `progress` -- for build/task progress in notifications
  - `spinner` -- for loading states in popups

**How libraries fit the Widget interface:**
- A Widget's `Render()` method can use any library internally to produce its `WidgetFrame.Content.ANSI` string
- lipgloss renders styled content -> string goes into `ContentBlock.ANSI`
- bubblezone marks interactive regions -> zones extracted into `WidgetFrame.Regions`
- For popup-based widgets (pane picker, overview, palette), the widget IS a Bubble Tea app running in a `TmuxPopupSurface` -- it uses bubbles components directly
- For daemon-rendered widgets (sidebar, headers), the coordinator calls lipgloss/bubblezone during `Render()` and sends pre-rendered ANSI to the surface

**Principle:** Widgets are the integration point for TUI libraries. The Surface/Layout/InputRouter layers are library-agnostic -- they only see `WidgetFrame` output. This means any Go TUI library that produces strings or ANSI output can be used inside a widget without changes to the rendering pipeline.

---

### 15. Compositor Integration Points

When/if the compositor (Phase R) is validated, it plugs into the existing architecture:

1. **OverlaySurface** becomes a new surface type in `SurfaceManager`
2. **CompositorProcess** is a single Go process that:
   - Receives `WidgetFrame` from multiple overlay surfaces
   - Composites them using CSI cursor positioning
   - Handles input by checking which overlay region was clicked
3. **Layout engine** gains overlay-aware assignments:
   - Headers can be overlays instead of pane splits or borders
   - Notifications are overlays with z-index > 0
   - Custom borders rendered as overlay regions around content panes

The key: the compositor is just another surface type. Widgets don't know or care whether they're being delivered to a tmux pane, a border format, or a compositor overlay. The abstraction holds.

---

## Part 3: Implementation Sequence

Grounded in the roadmap from COMPOSITOR_ROADMAP.md, with renderer work woven in.

### Batch 1: Foundation (Phases A + C)
- Resize debounce, profile hysteresis, protocol versioning
- Coordinator decomposition into separate files
- **No renderer interface changes yet** -- just stability and structure

### Batch 2: Interfaces (Phase D)
- Extract Widget interface (wrap existing RenderForClient/RenderHeaderForClient)
- Extract Surface interface (wrap existing server.go client handling)
- Introduce SurfaceManager, InputRouter
- Introduce LayoutEngine with single DefaultLayout that replicates current behavior
- **Zero behavior change** -- pure refactor with new abstractions

### Batch 3: New Surfaces (Phase E)
- TmuxBorderSurface for desktop headers
- TmuxPopupSurface formalized with lifecycle
- Layout profiles: phone, tablet, desktop (using new surfaces)

### Batch 4: New Widgets (Phases F + G)
- NotificationWidget + auto-dismiss popup surface
- PanePickerWidget
- OverviewWidget (popup with capture-pane thumbnails)
- CommandPaletteWidget (stretch)

### Batch 5: Compositor (Phase R -- if validated)
- OverlaySurface implementation
- CompositorProcess
- Floating pane support
- Custom border rendering

---

## Appendix: Current vs New Architecture Mapping

| Current | New Architecture |
|---------|-----------------|
| `RenderForClient()` | `SidebarWidget.Render()` returning `WidgetFrame` |
| `RenderHeaderForClient()` | `HeaderWidget.Render()` returning `WidgetFrame` |
| `server.SendRenderToClient()` | `Surface.Deliver(frame)` |
| `server.BroadcastRender()` | `RenderPipeline.OnStateChange()` |
| `server.OnRenderNeeded` callback | `RenderPipeline.renderCycle()` |
| `server.OnInput` callback | `InputRouter.Route()` |
| `server.clients` map | `SurfaceManager.surfaces` map |
| `ClientInfo` struct | `Surface` interface + `SurfaceCapabilities` |
| `RenderPayload` | `WidgetFrame` |
| `ClickableRegion` | `ClickableRegion` (unchanged) |
| `InputPayload` | `InputEvent` |
| `coordinator.ActiveClientProfile()` | `LayoutEngine` profile selection (phone/tablet/desktop chrome) |
| Hardcoded phone/desktop conditionals | `ProfileLayout` structs in `LayoutConfig` |
| `spawnRenderersForNewWindows()` | `SurfaceManager.Reconcile()` |

Note: all clients share the same tmux pane layout. The layout engine only controls tabby's chrome (sidebar, headers, overlays), not pane tiling.
