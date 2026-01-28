package daemon

import (
	"fmt"
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
	MsgMenuSelect     MessageType = "menu_select"  // Renderer -> Daemon: menu item selected
	MsgPing           MessageType = "ping"
	MsgPong           MessageType = "pong"
)

// Message is the base message structure for daemon<->renderer communication
type Message struct {
	Type     MessageType `json:"type"`
	ClientID string      `json:"client_id,omitempty"`
	Payload  interface{} `json:"payload,omitempty"`
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
}

// ResizePayload contains terminal dimensions and capabilities
type ResizePayload struct {
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	ColorProfile string `json:"color_profile,omitempty"` // "Ascii", "ANSI", "ANSI256", "TrueColor"
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

// SocketPath returns the daemon socket path for a session
func SocketPath(sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	return fmt.Sprintf("/tmp/tabby-daemon-%s.sock", sessionID)
}

// PidPath returns the pidfile path for a session
func PidPath(sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	return fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID)
}
