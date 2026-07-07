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
	// MsgHook carries a tmux-hook event from `tabby hook` into the daemon
	// event loop. Replaces the SIGUSR1 / SIGUSR2 signaling path used by
	// pre-Step-4 hook subcommands. Step 4 of the daemon refactor; see
	// /Users/b/.claude/plans/nifty-jingling-tulip.md.
	MsgHook MessageType = "hook"
	// MsgPetQA is the request-response envelope used by the `tabby pet`
	// CLI to read or mutate the cat's Q&A state owned by the daemon. The
	// CLI dials the socket, sends one MsgPetQA request, reads one MsgPetQA
	// response on the same connection, then disconnects. The handler is
	// synchronous (no subscribe required) — see PetQARequest /
	// PetQAResponse below. Phase 1 of the Q&A loop; see
	// /Users/b/.claude/plans/wiggly-discovering-starlight.md.
	MsgPetQA MessageType = "pet_qa"
)

// TargetKind identifies what KIND of renderer a message is for or from.
// It replaces the old stringly-typed prefix scheme ("window-header:@1", "header:%5", etc).
type TargetKind string

const (
	TargetSidebar      TargetKind = "sidebar"
	TargetWindowHeader TargetKind = "window-header"
	TargetPaneHeader   TargetKind = "pane-header"
	TargetPaneBorder   TargetKind = "pane-border" // one edge of a pane's custom box (see Edge)
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
	Edge     string     `json:"edge,omitempty"` // pane-border only: "top"|"bottom"|"left"|"right"
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
	case TargetPaneBorder:
		return "border:" + t.Edge + ":" + t.PaneID
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
	case TargetPaneBorder:
		if t.PaneID == "" {
			return fmt.Errorf("pane-border target missing pane")
		}
		switch t.Edge {
		case "top", "bottom", "left", "right":
		default:
			return fmt.Errorf("pane-border target has invalid edge %q", t.Edge)
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
	if strings.HasPrefix(s, "border:") {
		// "border:<edge>:<paneID>"
		rest := strings.TrimPrefix(s, "border:")
		if i := strings.Index(rest, ":"); i >= 0 {
			return RenderTarget{Kind: TargetPaneBorder, Edge: rest[:i], PaneID: rest[i+1:]}, nil
		}
		return RenderTarget{}, fmt.Errorf("malformed pane-border key %q", s)
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

// HookPayload carries a tmux-hook delivery from the `tabby hook` CLI into the
// daemon. Kind is the tmux hook name (e.g. "client-resized",
// "after-select-window"); Args carries hook-specific format-string values
// captured by tmux at hook time (e.g. {"tty": "/dev/ttys003", "width": "180",
// "height": "50"}). The daemon translates this into a TmuxHookEvent on its
// internal event loop. See /Users/b/.claude/plans/nifty-jingling-tulip.md
// Step 4.
type HookPayload struct {
	Kind string            `json:"kind"`
	Args map[string]string `json:"args"`
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

	// Q&A personality-building loop. All omitempty so old pet.json files
	// without these keys still deserialize cleanly into a zero-valued
	// feature state.
	PendingQuestion   *PendingQuestion   `json:"pending_question,omitempty"`
	AnsweredQuestions []AnsweredQuestion `json:"answered_questions,omitempty"`
	Traits            []PersonalityTrait `json:"traits,omitempty"`
	QuestionCooldown  time.Time          `json:"question_cooldown,omitzero"`
	LastQuestionShown time.Time          `json:"last_question_shown,omitzero"`
	// QAOptedOut is set when the user answers "No thanks" to the initial
	// consent question. Suppresses all future Q&A activity at runtime
	// without requiring a config.yaml edit.
	QAOptedOut bool `json:"qa_opted_out,omitempty"`
	// QAFreeTextOptedOut is set when the user picks "Multi-choice only".
	// pickQuestion filters out free-text bank entries while this is true.
	QAFreeTextOptedOut bool `json:"qa_free_text_opted_out,omitempty"`
}

// PendingQuestion is the question the cat is currently waiting on a
// response for. At most one is pending at a time per pet.
type PendingQuestion struct {
	ID      string    `json:"id"`             // stable seed-bank ID (e.g. "morning_or_night")
	Text    string    `json:"text"`           // rendered question text
	Kind    string    `json:"kind"`           // "choice" | "free_text"
	Choices []string  `json:"choices,omitempty"`
	Expires time.Time `json:"expires"`        // after this point pickQuestion may rotate it out
	Source  string    `json:"source,omitempty"` // "bank" (Phase 1) or "llm" (Phase 3)
}

// AnsweredQuestion is one Q&A history entry. The slice on PetState is
// capped at 50 entries (oldest dropped); older entries are distilled
// into traits before being discarded.
type AnsweredQuestion struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Answer    string    `json:"answer"`
	Kind      string    `json:"kind"`
	Timestamp time.Time `json:"timestamp"`
	// Distilled marks free-text answers that have already been processed
	// by the Phase-3 LLM distillation pass (see DistillTraitsLLM in
	// cmd/tabby/internal/daemon/pet_qa.go). Choice answers are distilled
	// synchronously by the rule-based DistillTrait and don't use this
	// flag. omitempty keeps old pet.json files round-tripping cleanly.
	Distilled bool `json:"distilled,omitempty"`
}

// PersonalityTrait is a distilled, prompt-ready fact about the user or
// the cat's chosen character. Capped at 20 per pet; oldest/lowest
// confidence dropped first.
type PersonalityTrait struct {
	Text       string    `json:"text"`           // e.g. "user is a night owl"
	Source     string    `json:"source"`         // question ID that produced it
	Confidence float64   `json:"confidence"`     // 0..1
	AddedAt    time.Time `json:"added_at"`
}

// PetQAOp is the operation discriminator carried in a PetQARequest. Kept
// as a tagged string so the wire format stays human-readable in socket
// logs and new ops don't require renumbering anything.
type PetQAOp string

const (
	PetQAOpGetPending PetQAOp = "get_pending"
	PetQAOpAnswer     PetQAOp = "answer"
	PetQAOpListTraits PetQAOp = "list_traits"
	PetQAOpForget     PetQAOp = "forget"
)

// PetQARequest is the CLI->daemon request envelope for the Q&A loop.
// Only one field is meaningful per Op:
//
//   - get_pending : no other fields used.
//   - answer      : Answer carries the user's response. The daemon
//                   rejects non-empty answers that don't match a choice
//                   for choice-kind questions.
//   - list_traits : no other fields used.
//   - forget      : ID is the AnsweredQuestion ID to remove (along
//                   with any traits derived from it).
type PetQARequest struct {
	Op     PetQAOp `json:"op"`
	Answer string  `json:"answer,omitempty"`
	ID     string  `json:"id,omitempty"`
}

// PetQAResponse is the daemon->CLI reply. OK indicates whether the
// requested op completed; Error carries a human-readable reason when OK
// is false. Other fields are populated per-op:
//
//   - get_pending : Pending set if there is one; nil otherwise.
//   - answer      : NewTrait set if the answer produced a fresh trait
//                   (consent answers and choices with no TraitFor entry
//                   leave it nil).
//   - list_traits : Traits populated (possibly empty slice).
//                   RecentAnswers populated so the CLI can show
//                   "source: <id>" links to recent Q&A history.
//   - forget      : Removed indicates whether the ID matched anything.
type PetQAResponse struct {
	OK            bool               `json:"ok"`
	Error         string             `json:"error,omitempty"`
	Pending       *PendingQuestion   `json:"pending,omitempty"`
	NewTrait      *PersonalityTrait  `json:"new_trait,omitempty"`
	Traits        []PersonalityTrait `json:"traits,omitempty"`
	RecentAnswers []AnsweredQuestion `json:"recent_answers,omitempty"`
	Removed       bool               `json:"removed,omitempty"`
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
