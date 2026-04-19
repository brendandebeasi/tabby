package daemon

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// MessageType identifies the type of message
type MessageType string

const (
	MsgSubscribe      MessageType = "subscribe"
	MsgUnsubscribe    MessageType = "unsubscribe"
	MsgRender         MessageType = "render"
	MsgInput          MessageType = "input"
	MsgResize         MessageType = "resize"
	MsgViewportUpdate MessageType = "viewport_update"
	MsgMenu           MessageType = "menu"        // Daemon -> Renderer: show context menu
	MsgMenuSelect     MessageType = "menu_select" // Renderer -> Daemon: menu item selected
	MsgMarkerPicker   MessageType = "marker_picker"
	MsgColorPicker    MessageType = "color_picker"
	MsgPing           MessageType = "ping"
	MsgPong           MessageType = "pong"
)

// TargetKind identifies what KIND of renderer a message is for or from.
// It replaces the old stringly-typed prefix scheme ("window-header:@1", "header:%5", etc).
type TargetKind string

const (
	TargetSidebar      TargetKind = "sidebar"
	TargetWindowHeader TargetKind = "window-header"
	TargetPaneHeader   TargetKind = "pane-header"
	TargetSidebarPopup TargetKind = "sidebar-popup"
	TargetHook         TargetKind = "hook"
)

// RenderTarget identifies a renderer instance: what kind it is and which tmux
// window/pane it is for. Kind + WindowID (sidebar, window-header) or Kind +
// PaneID (pane-header) is enough to uniquely address every renderer.
//
// Instance is an optional tag for cases where multiple renderers share the
// same Kind+WindowID (web clients, popup sidebars, hooks).
type RenderTarget struct {
	Kind     TargetKind `json:"kind"`
	WindowID string     `json:"window,omitempty"` // "@1"
	PaneID   string     `json:"pane,omitempty"`   // "%5"
	Instance string     `json:"instance,omitempty"`
}

// Key returns the canonical string form used as a map key by the server.
// Must be stable: every renderer+daemon agreeing on the same Key() for the
// same logical target is what keeps broadcast routing correct.
//
// Formats:
//
//	sidebar        "@1"                        (backward-compatible bare window id)
//	sidebar + inst "@1#web-abc"
//	window-header  "window-header:@1"
//	pane-header    "header:%5"
//	sidebar-popup  "sidebar-popup:<instance>"
//	hook           "hook:<instance>"
func (t RenderTarget) Key() string {
	switch t.Kind {
	case TargetSidebar:
		if t.Instance != "" {
			return t.WindowID + "#" + t.Instance
		}
		return t.WindowID
	case TargetWindowHeader:
		return "window-header:" + t.WindowID
	case TargetPaneHeader:
		return "header:" + t.PaneID
	case TargetSidebarPopup:
		if t.Instance != "" {
			return "sidebar-popup:" + t.Instance
		}
		return "sidebar-popup"
	case TargetHook:
		if t.Instance != "" {
			return "hook:" + t.Instance
		}
		return "hook"
	}
	return string(t.Kind)
}

// Valid returns nil if the target has enough information to be routed.
// Invalid targets are dropped by the server instead of silently coerced.
func (t RenderTarget) Valid() error {
	switch t.Kind {
	case TargetSidebar, TargetWindowHeader:
		if t.WindowID == "" {
			return fmt.Errorf("%s target missing window", t.Kind)
		}
	case TargetPaneHeader:
		if t.PaneID == "" {
			return fmt.Errorf("pane-header target missing pane")
		}
	case TargetSidebarPopup, TargetHook:
		// These can be instance-only.
	case "":
		return fmt.Errorf("target kind is empty")
	default:
		return fmt.Errorf("unknown target kind %q", t.Kind)
	}
	return nil
}

// KindOf returns the TargetKind for a stringly-typed map key (the value
// Target.Key() produces). Used by code paths that still route by string key
// so they can ask "what kind of renderer is this?" without duplicating the
// prefix format.
//
// Returns "" for unrecognized keys.
func KindOf(key string) TargetKind {
	t, err := ParseLegacyKey(key)
	if err != nil {
		return ""
	}
	return t.Kind
}

// ParseLegacyKey reconstructs a RenderTarget from a legacy string key
// (pre-typed-identity). Used only at system boundaries that still emit the
// old format during migration.
func ParseLegacyKey(s string) (RenderTarget, error) {
	if s == "" {
		return RenderTarget{}, fmt.Errorf("empty key")
	}
	if strings.HasPrefix(s, "window-header:") {
		return RenderTarget{Kind: TargetWindowHeader, WindowID: strings.TrimPrefix(s, "window-header:")}, nil
	}
	if strings.HasPrefix(s, "header:") {
		return RenderTarget{Kind: TargetPaneHeader, PaneID: strings.TrimPrefix(s, "header:")}, nil
	}
	if strings.HasPrefix(s, "sidebar-popup") {
		inst := strings.TrimPrefix(s, "sidebar-popup")
		inst = strings.TrimPrefix(inst, ":")
		return RenderTarget{Kind: TargetSidebarPopup, Instance: inst}, nil
	}
	if strings.HasPrefix(s, "hook") {
		inst := strings.TrimPrefix(s, "hook")
		inst = strings.TrimPrefix(inst, ":")
		return RenderTarget{Kind: TargetHook, Instance: inst}, nil
	}
	if strings.HasPrefix(s, "@") {
		windowID := s
		instance := ""
		if i := strings.Index(s, "#"); i >= 0 {
			windowID = s[:i]
			instance = s[i+1:]
		}
		return RenderTarget{Kind: TargetSidebar, WindowID: windowID, Instance: instance}, nil
	}
	return RenderTarget{}, fmt.Errorf("unrecognized legacy key %q", s)
}

// Message is the base message structure for daemon<->renderer communication.
// Target identifies the renderer this message is for (daemon->renderer) or
// from (renderer->daemon). It replaces the old stringly-typed ClientID.
type Message struct {
	Type    MessageType  `json:"type"`
	Target  RenderTarget `json:"target"`
	Payload interface{}  `json:"payload,omitempty"`
}

// ClickableRegion defines a clickable area in the rendered content
type ClickableRegion struct {
	StartLine int    `json:"start"`     // First line of the region (0-indexed in content)
	EndLine   int    `json:"end"`       // Last line of the region (inclusive)
	StartCol  int    `json:"start_col"` // First column of the region (0 for full-width)
	EndCol    int    `json:"end_col"`   // Last column of the region (0 for full-width, meaning width-1)
	Action    string `json:"action"`    // "select_window", "select_pane", "toggle_group", "button"
	Target    string `json:"target"`    // window index, pane ID, group name, or button action
}

// ActiveClient describes the physical tmux client currently driving the
// session (the tty the daemon elected as "active"). Surfaced in every
// RenderPayload so renderers know what physical client context they're
// rendering for, instead of inferring profile from width alone.
type ActiveClient struct {
	TTY     string `json:"tty,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Profile string `json:"profile,omitempty"` // "phone" or "desktop"
}

// RenderPayload contains pre-rendered content for a renderer
type RenderPayload struct {
	SequenceNum    uint64            `json:"seq"`             // Monotonic sequence for race detection
	Content        string            `json:"content"`         // Pre-rendered scrollable content
	PinnedContent  string            `json:"pinned_content"`  // Pre-rendered pinned widgets
	Width          int               `json:"width"`           // Rendered for this width
	Height         int               `json:"height"`          // Rendered for this height
	TotalLines     int               `json:"total_lines"`     // Total lines in content for scroll calc
	PinnedHeight   int               `json:"pinned_height"`   // Height of pinned section
	ViewportOffset int               `json:"viewport_offset"` // Suggested scroll position
	Regions        []ClickableRegion `json:"regions"`         // Clickable regions for hit testing
	PinnedRegions  []ClickableRegion `json:"pinned_regions"`  // Clickable regions in pinned content (Y relative to pinned start)
	SidebarBg      string            `json:"sidebar_bg,omitempty"`
	TerminalBg     string            `json:"terminal_bg,omitempty"`
	ActiveClient   ActiveClient      `json:"active_client,omitempty"`
}

// InputPayload contains input events from renderer
type InputPayload struct {
	SequenceNum    uint64 `json:"seq"`                       // Render frame this input references
	Type           string `json:"type"`                      // "mouse", "key", or "action"
	MouseX         int    `json:"mouse_x,omitempty"`         // Mouse X coordinate
	MouseY         int    `json:"mouse_y,omitempty"`         // Mouse Y coordinate
	Button         string `json:"button,omitempty"`          // "left", "right", "middle", "wheelup", "wheeldown"
	Action         string `json:"action,omitempty"`          // "press", "release"
	Key            string `json:"key,omitempty"`             // Key string for keyboard events
	ClickedArea    string `json:"clicked_area,omitempty"`    // "scrollable", "pinned", "divider"
	PinnedRelY     int    `json:"pinned_rel_y,omitempty"`    // Y relative to pinned section start
	ViewportOffset int    `json:"viewport_offset,omitempty"` // Current viewport offset
	PaneID         string `json:"pane_id,omitempty"`         // tmux pane ID for context menus
	SourcePaneID   string `json:"source_pane_id,omitempty"`  // tmux pane ID where the click physically occurred (for positioning)
	// Semantic action (resolved by renderer from clickable regions)
	ResolvedAction string `json:"resolved_action,omitempty"` // "select_window", "select_pane", "toggle_group", "button"
	ResolvedTarget string `json:"resolved_target,omitempty"` // window index, pane ID, group name, or button action
	// Context menu control
	IsSimulatedRightClick bool   `json:"is_simulated_right_click,omitempty"` // True if right-click came from long-press or double-tap
	PickerAction          string `json:"picker_action,omitempty"`
	PickerScope           string `json:"picker_scope,omitempty"`
	PickerTarget          string `json:"picker_target,omitempty"`
	PickerValue           string `json:"picker_value,omitempty"`
	PickerQuery           string `json:"picker_query,omitempty"`
}

// ResizePayload contains terminal dimensions and capabilities
type ResizePayload struct {
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	ColorProfile string `json:"color_profile,omitempty"` // "Ascii", "ANSI", "ANSI256", "TrueColor"
	PaneID       string `json:"pane_id,omitempty"`       // tmux pane ID of the renderer
	HeaderHeight int    `json:"header_height,omitempty"` // rows allocated to pane-header; 0 means 1 (backward compat)
}

// ViewportUpdatePayload contains scroll position update
type ViewportUpdatePayload struct {
	ViewportOffset int `json:"viewport_offset"`
}

// GitState holds git repository information
type GitState struct {
	Branch     string    `json:"branch"`
	IsRepo     bool      `json:"is_repo"`
	IsDirty    bool      `json:"is_dirty"`
	Ahead      int       `json:"ahead"`
	Behind     int       `json:"behind"`
	Staged     int       `json:"staged"`
	Unstaged   int       `json:"unstaged"`
	Untracked  int       `json:"untracked"`
	Stashes    int       `json:"stashes"`
	LastUpdate time.Time `json:"last_update"`
}

// StatsState holds system statistics
type StatsState struct {
	CPUPercent     float64   `json:"cpu_percent"`
	MemoryUsed     float64   `json:"memory_used"`
	MemoryTotal    float64   `json:"memory_total"`
	MemoryPercent  float64   `json:"memory_percent"`
	BatteryPercent int       `json:"battery_percent"`
	BatteryStatus  string    `json:"battery_status"`
	LastUpdate     time.Time `json:"last_update"`
}

// PetState holds pet widget state
type PetState struct {
	Hunger            int       `json:"hunger"`
	Happiness         int       `json:"happiness"`
	LastFed           time.Time `json:"last_fed"`
	LastPet           time.Time `json:"last_pet"`
	TotalPets         int       `json:"total_pets"`
	TotalFeedings     int       `json:"total_feedings"`
	TotalPoopsCleaned int       `json:"total_poops_cleaned"`
	TotalYarnPlays    int       `json:"total_yarn_plays"`
	State             string    `json:"state"`
	LastThought       string    `json:"last_thought"`
	PosX              int       `json:"pos_x"`
	YarnPosX          int       `json:"yarn_pos_x"`
	PoopPositions     []int     `json:"poop_positions"`
}

// MenuItemPayload represents a single menu item sent to a renderer
type MenuItemPayload struct {
	Label     string `json:"label"`
	Key       string `json:"key,omitempty"`
	Separator bool   `json:"separator,omitempty"`
	Header    bool   `json:"header,omitempty"`
}

// MenuPayload contains a context menu for the renderer to display
type MenuPayload struct {
	Title string            `json:"title"`
	Items []MenuItemPayload `json:"items"`
	X     int               `json:"x"` // Menu position X (screen coords)
	Y     int               `json:"y"` // Menu position Y (screen coords)
}

// MenuSelectPayload contains the user's menu selection
type MenuSelectPayload struct {
	Index int `json:"index"` // Selected item index (-1 for cancel)
}

type MarkerOptionPayload struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Keywords string `json:"keywords,omitempty"`
}

type MarkerPickerPayload struct {
	Title   string                `json:"title"`
	Scope   string                `json:"scope"`
	Target  string                `json:"target"`
	Options []MarkerOptionPayload `json:"options"`
}

// ColorPickerPayload contains data for the interactive HSL color picker modal
type ColorPickerPayload struct {
	Title        string `json:"title"`
	Scope        string `json:"scope"`         // "window" or "group"
	Target       string `json:"target"`        // window target or group name
	CurrentColor string `json:"current_color"` // Current hex color (for initial position)
}

// runtimePrefix returns an optional prefix for runtime files.
// Set TABBY_RUNTIME_PREFIX to isolate demo/test instances (e.g. "demo-").
func runtimePrefix() string {
	return os.Getenv("TABBY_RUNTIME_PREFIX")
}

// RuntimePath builds a runtime file path: /tmp/{prefix}tabby-daemon-{session}{suffix}
func RuntimePath(sessionID, suffix string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	return fmt.Sprintf("/tmp/%stabby-daemon-%s%s", runtimePrefix(), sessionID, suffix)
}

// SocketPath returns the daemon socket path for a session
func SocketPath(sessionID string) string {
	return RuntimePath(sessionID, ".sock")
}

// PidPath returns the pidfile path for a session
func PidPath(sessionID string) string {
	return RuntimePath(sessionID, ".pid")
}
