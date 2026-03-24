package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	kemoji "github.com/kenshaw/emoji"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// coordinatorDebugLog is the logger for coordinator debug output
var coordinatorDebugLog *log.Logger

// Deadlock detection
var (
	lastHeartbeat     int64 // Unix nano timestamp of last main loop tick
	heartbeatMu       sync.Mutex
	lockHolders       = make(map[string]lockInfo) // lock name -> holder info
	lockHoldersMu     sync.Mutex
	deadlockWatchdog  bool
	deadlockThreshold = 5 * time.Second // Alert if no heartbeat for this long
)

type lockInfo struct {
	goroutine string
	acquired  time.Time
	location  string
}

type CWDColorMapping struct {
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

func init() {
	// Default to discard (no logging)
	coordinatorDebugLog = log.New(io.Discard, "", 0)
}

// SetCoordinatorDebugLog sets the debug logger for the coordinator
func SetCoordinatorDebugLog(logger *log.Logger) {
	coordinatorDebugLog = logger
}

// tmuxCmdTimeout is the default timeout for bare tmux commands in the coordinator.
// This prevents indefinite hangs during macOS sleep/wake or tmux server stalls.
const tmuxCmdTimeout = 5 * time.Second

// tmuxRun executes a tmux command with a timeout. Fire-and-forget.
func tmuxRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", args...).Run()
}

// tmuxOutputCtx executes a tmux command with a timeout and returns stdout.
func tmuxOutputCtx(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("tmux %s: timed out after %v", args[0], tmuxCmdTimeout)
	}
	return out, err
}

// StartDeadlockWatchdog starts a goroutine that monitors for deadlocks
func StartDeadlockWatchdog() {
	deadlockWatchdog = true
	// Initialize heartbeat
	heartbeatMu.Lock()
	lastHeartbeat = time.Now().UnixNano()
	heartbeatMu.Unlock()

	go func() {
		for deadlockWatchdog {
			time.Sleep(1 * time.Second)

			heartbeatMu.Lock()
			lastBeat := lastHeartbeat
			heartbeatMu.Unlock()

			elapsed := time.Since(time.Unix(0, lastBeat))
			if elapsed > deadlockThreshold {
				coordinatorDebugLog.Printf("DEADLOCK WARNING: No heartbeat for %v", elapsed)

				// Dump lock holders
				lockHoldersMu.Lock()
				holders := make(map[string]lockInfo, len(lockHolders))
				for k, v := range lockHolders {
					holders[k] = v
				}
				lockHoldersMu.Unlock()

				if len(holders) > 0 {
					coordinatorDebugLog.Printf("DEADLOCK: Current lock holders:")
					for name, info := range holders {
						coordinatorDebugLog.Printf("  %s: held by %s at %s for %v",
							name, info.goroutine, info.location, time.Since(info.acquired))
					}
				}

				// Also write to crash log (debug log may be /dev/null in non-debug mode)
				if crashLog != nil {
					crashLog.Printf("DEADLOCK WARNING: No heartbeat for %v", elapsed)
					if len(holders) > 0 {
						crashLog.Printf("DEADLOCK: Lock holders:")
						for name, info := range holders {
							crashLog.Printf("  %s: held by %s at %s for %v",
								name, info.goroutine, info.location, time.Since(info.acquired))
						}
					}
				}
			}
		}
	}()
}

// StopDeadlockWatchdog stops the watchdog
func StopDeadlockWatchdog() {
	deadlockWatchdog = false
}

// recordHeartbeat updates the heartbeat timestamp
func recordHeartbeat() {
	heartbeatMu.Lock()
	lastHeartbeat = time.Now().UnixNano()
	heartbeatMu.Unlock()
}

// trackLock records when a lock is acquired
func trackLock(name, location string) {
	lockHoldersMu.Lock()
	lockHolders[name] = lockInfo{
		goroutine: fmt.Sprintf("goroutine-%d", time.Now().UnixNano()%10000),
		acquired:  time.Now(),
		location:  location,
	}
	lockHoldersMu.Unlock()
}

// untrackLock removes lock tracking when released
func untrackLock(name string) {
	lockHoldersMu.Lock()
	delete(lockHolders, name)
	lockHoldersMu.Unlock()
}

// Coordinator manages centralized state and rendering for all renderers
type Coordinator struct {
	// Shared state
	windows         []tmux.Window
	grouped         []grouping.GroupedWindows
	windowVisualPos map[string]int // window ID -> visual position in sidebar
	config          *config.Config
	collapsedGroups map[string]bool
	spinnerFrame    int

	// Git state (cached)
	gitBranch string
	gitDirty  int
	gitAhead  int
	gitBehind int
	isGitRepo bool

	// Session state (cached)
	sessionName    string
	sessionClients int
	windowCount    int

	// Pet state
	pet petState

	cwdColors   map[string]CWDColorMapping
	cwdColorsMu sync.RWMutex

	// Last known width (for pet physics clamping)
	lastWidth int

	// Click debounce for pet widget (prevents render floods from spam clicks)
	lastPetClick time.Time

	// Global width for synchronization
	globalWidth            int
	lastWidthSync          time.Time // Last time we synced widths (for debouncing)
	lastActiveWindowID     string    // Track which window was last active (for detecting window switch)
	activeWindowChangeTime time.Time // When the active window changed (for grace period)
	widthSyncMu            sync.Mutex

	// Per-client widths for accurate click detection
	clientWidths   map[string]int
	clientWidthsMu sync.RWMutex

	// Sidebar collapse state
	sidebarCollapsed     bool
	sidebarPreviousWidth int

	// Pet widget layout (for custom click detection)
	petLayout petWidgetLayout

	// State locks
	stateMu sync.RWMutex

	// Session info
	sessionID string
	baseIndex int

	// Process tree caching
	lastProcessCheck  time.Time
	cachedProcessTree *processTree

	// AI tool state tracking — per-pane (for busy→idle transition detection)
	prevPaneBusy       map[string]bool   // pane ID → was AI tool busy last cycle
	prevPaneTitle      map[string]string // pane ID → AI pane title last cycle
	hookPaneActive     map[string]bool   // pane ID → hooks detected (seen @tabby_busy=1)
	hookPaneBusyIdleAt map[string]int64  // pane ID → unix timestamp when hook-busy but process looks idle
	aiBellUntil        map[int]int64     // window index → unix timestamp when bell expires (window-level)

	// Callback to sync sidebar client widths in the server's client map
	// Called during expand_sidebar to update server-side Width before BroadcastRender
	OnSyncSidebarClientWidths func(newWidth int)

	// Context menu state (for in-renderer menus)
	OnSendMenu         func(clientID string, menu *daemon.MenuPayload)
	OnSendMarkerPicker func(clientID string, picker *daemon.MarkerPickerPayload)
	OnSendColorPicker  func(clientID string, picker *daemon.ColorPickerPayload)
	pendingMenus       map[string][]menuItemDef
	pendingMenusMu     sync.Mutex

	lastWindowSelect   map[string]time.Time
	lastWindowByClient map[string]time.Time
	lastWindowSelectMu sync.Mutex
	lastPaneMenuOpen   map[string]time.Time
	lastPaneMenuOpenMu sync.Mutex

	// Background theme detector (deprecated, kept for fallback)
	bgDetector *colors.BackgroundDetector

	// Color theme (new preset-based system)
	theme *colors.Theme
}

// GetWindows returns the current list of windows
func (c *Coordinator) GetWindows() []tmux.Window {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]tmux.Window, len(c.windows))
	copy(result, c.windows)
	return result
}

func (c *Coordinator) collapseWindowPanes(windowTarget string, win *tmux.Window) {
	headerHeight := 1
	if c.config.PaneHeader.CustomBorder {
		headerHeight = 2
	}
	for _, pane := range win.Panes {
		paneID := pane.ID
		if paneID == "" {
			continue
		}
		heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
		prevHeight := strings.TrimSpace(string(heightOut))
		if prevHeight == "" {
			prevHeight = "1"
		}
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_prev_height", prevHeight).Run()
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_collapsed", "1").Run()
		exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", headerHeight)).Run()
	}
	exec.Command("tmux", "set-window-option", "-t", windowTarget, "@tabby_collapsed", "1").Run()
}

func (c *Coordinator) expandWindowPanes(windowTarget string, win *tmux.Window) {
	for _, pane := range win.Panes {
		paneID := pane.ID
		if paneID == "" {
			continue
		}
		prevHeightOut, _ := exec.Command("tmux", "show-options", "-p", "-t", paneID, "@tabby_pane_prev_height").Output()
		prevHeightStr := strings.TrimSpace(string(prevHeightOut))
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_prev_height").Run()
		if prevHeightStr != "" && prevHeightStr != "0" {
			exec.Command("tmux", "resize-pane", "-t", paneID, "-y", prevHeightStr).Run()
		}
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_collapsed").Run()
	}
	exec.Command("tmux", "set-window-option", "-t", windowTarget, "-u", "@tabby_collapsed").Run()
}

func (c *Coordinator) togglePaneCollapse(windowTarget string) bool {
	target := strings.TrimPrefix(windowTarget, ":")
	if target == "" {
		return false
	}
	idx, parseErr := strconv.Atoi(target)
	if parseErr != nil {
		return false
	}

	c.stateMu.RLock()
	var windowCopy tmux.Window
	found := false
	for i := range c.windows {
		if c.windows[i].Index == idx {
			windowCopy = c.windows[i]
			found = true
			break
		}
	}
	c.stateMu.RUnlock()

	if !found {
		return false
	}

	winTarget := fmt.Sprintf(":%d", idx)
	collapsed := false
	if out, err := exec.Command("tmux", "show-window-option", "-v", "-t", winTarget, "@tabby_collapsed").Output(); err == nil {
		val := strings.TrimSpace(string(out))
		if val == "1" || strings.EqualFold(val, "true") {
			collapsed = true
		}
	}

	if collapsed {
		c.expandWindowPanes(winTarget, &windowCopy)
	} else {
		c.collapseWindowPanes(winTarget, &windowCopy)
	}
	return true
}

// petState holds the current state of the pet widget
type petState struct {
	Pos               pos2D
	State             string
	Direction         int
	Hunger            int
	Happiness         int
	YarnPos           pos2D
	YarnExpiresAt     time.Time // When yarn disappears
	YarnPushCount     int       // How many times yarn has been pushed (catch after 2)
	FoodItem          pos2D
	PoopPositions     []int
	NeedsPoopAt       time.Time
	LastFed           time.Time
	LastPet           time.Time
	LastPoop          time.Time
	LastThought       string
	ThoughtScroll     int
	FloatingItems     []floatingItem
	TargetPos         pos2D
	HasTarget         bool
	ActionPending     string
	AnimFrame         int
	TotalPets         int
	TotalFeedings     int
	TotalPoopsCleaned int
	TotalYarnPlays    int
	// Death state
	IsDead        bool
	DeathTime     time.Time
	StarvingStart time.Time // When hunger hit 0 (for death countdown)
	// Mouse state
	MousePos          pos2D     // X: -1 means no mouse present
	MouseDirection    int       // Direction mouse is moving
	MouseAppearsAt    time.Time // When a mouse will appear next
	TotalMouseCatches int
	// Adventure state
	Adventure adventureState
	// Debug state
	DebugThoughtIdx int // Index into debugThoughtCategories for debug bar
}

type pos2D struct {
	X int
	Y int
}

type floatingItem struct {
	Emoji     string
	Pos       pos2D
	Velocity  pos2D
	ExpiresAt time.Time
}

// Adventure mode types
type adventurePhase string

const (
	advPhaseNone      adventurePhase = ""
	advPhaseDeparting adventurePhase = "departing"
	advPhaseExploring adventurePhase = "exploring"
	advPhaseEncounter adventurePhase = "encounter"
	advPhaseReturning adventurePhase = "returning"
	advPhaseArriving  adventurePhase = "arriving"
)

type adventureState struct {
	Active        bool
	Phase         adventurePhase
	PhaseStart    time.Time
	PhaseDuration time.Duration
	Biome         string
	SceneOffset   int // How far cat has traveled (for scenery scrolling)
	Wildlife      *wildlifeEncounter
	CatX          int // Cat position during adventure (relative to play area)
	LastThought   string
	TotalCatches  int
}

type wildlifeEncounter struct {
	Type         string
	Emoji        string
	X            int // Position in scene
	Y            int // 0=ground, 1=low air, 2=high air
	Speed        int
	CatchChance  int
	Spotted      bool
	Stalking     bool
	Pounced      bool
	PounceFrames int
	WillCatch    bool
	Caught       bool
	Escaped      bool
	Approach     int
}

type biomeData struct {
	Ground   string
	Scenery  []string
	Wildlife []string
}

type wildlifeData struct {
	Emoji       string
	YLevel      int // 0=ground, 1=low air, 2=high air
	Speed       int
	CatchChance int
}

// Biome definitions
var adventureBiomes = map[string]biomeData{
	"forest": {
		Ground:   "~",
		Scenery:  []string{"🌳", "🌲", "🪨", "🍂", "🌿"},
		Wildlife: []string{"squirrel", "bird", "bug"},
	},
	"meadow": {
		Ground:   ",",
		Scenery:  []string{"🌸", "🌾", "🌻", "🦋", "🌿"},
		Wildlife: []string{"butterfly", "bird", "mouse", "bug"},
	},
	"garden": {
		Ground:   ".",
		Scenery:  []string{"🌷", "🌹", "🪴", "🪨", "🍃"},
		Wildlife: []string{"bird", "lizard", "bug", "butterfly"},
	},
	"backyard": {
		Ground:   "_",
		Scenery:  []string{"🪵", "🪨", "🌿", "🍂"},
		Wildlife: []string{"mouse", "bird", "squirrel", "lizard"},
	},
}

// Wildlife definitions
var adventureWildlife = map[string]wildlifeData{
	"squirrel":  {Emoji: "🐿️", YLevel: 0, Speed: 2, CatchChance: 30},
	"bird":      {Emoji: "🐦", YLevel: 2, Speed: 3, CatchChance: 15},
	"butterfly": {Emoji: "🦋", YLevel: 1, Speed: 1, CatchChance: 60},
	"bug":       {Emoji: "🐛", YLevel: 0, Speed: 1, CatchChance: 80},
	"mouse":     {Emoji: "🐭", YLevel: 0, Speed: 2, CatchChance: 50},
	"lizard":    {Emoji: "🦎", YLevel: 0, Speed: 3, CatchChance: 25},
}

// Adventure thoughts by wildlife type and phase
var adventureThoughts = map[string]map[string][]string{
	"squirrel": {
		"spot":   []string{"squirrel.", "prey detected.", "target acquired.", "fluffy tail..."},
		"stalk":  []string{"creeping...", "patience...", "closer...", "silent paws..."},
		"catch":  []string{"got you!", "mine now.", "natural order.", "victory!"},
		"escape": []string{"next time.", "curse you, tree.", "too fast.", "the hunt continues."},
	},
	"bird": {
		"spot":   []string{"bird.", "wings.", "foolish creature.", "come down here..."},
		"stalk":  []string{"watching...", "waiting...", "soon...", "calculating..."},
		"catch":  []string{"impossible!", "got one!", "legendary.", "I am apex."},
		"escape": []string{"fly away then.", "gravity wins.", "next time, bird.", "curse these paws."},
	},
	"butterfly": {
		"spot":   []string{"flutter.", "pretty prey.", "floating snack.", "must catch."},
		"stalk":  []string{"gentle...", "easy...", "almost...", "focus..."},
		"catch":  []string{"got it!", "delicate.", "mine.", "beautiful catch."},
		"escape": []string{"too floaty.", "wind took it.", "next one.", "pretty but quick."},
	},
	"bug": {
		"spot":   []string{"bug.", "crunchy.", "protein.", "easy prey."},
		"stalk":  []string{"sneaking...", "closer...", "simple...", "patience..."},
		"catch":  []string{"crunch.", "tasty.", "got it.", "efficient."},
		"escape": []string{"fast bug.", "under leaf.", "next bug.", "how?"},
	},
	"mouse": {
		"spot":   []string{"mouse!", "classic.", "the chase.", "ancient rivalry."},
		"stalk":  []string{"creeping...", "silent...", "focused...", "instinct guides..."},
		"catch":  []string{"gotcha!", "mouse mine.", "perfect.", "legendary catch."},
		"escape": []string{"quick mouse.", "hole escape.", "rivalry continues.", "next time, mouse."},
	},
	"lizard": {
		"spot":   []string{"lizard.", "scaly one.", "fast prey.", "challenge accepted."},
		"stalk":  []string{"careful...", "they sense heat...", "slow...", "steady..."},
		"catch":  []string{"scales!", "got it!", "cold-blooded victory.", "impressive."},
		"escape": []string{"too quick.", "tail trick?", "slippery.", "lizards cheat."},
	},
}

// petWidgetLayout tracks line offsets for custom click detection
//
// CLICK DETECTION METHODS:
//
// 1. BubbleZone (used for static elements):
//   - Wrap text with zone.Mark("zone_id", text) during rendering
//   - Call zone.Scan() on the full output to process markers
//   - Use zone.Get("zone_id") to retrieve bounds (StartX, EndX, StartY, EndY)
//   - Good for: buttons, fixed-position elements
//   - Limitation: Only ONE zone per ID is tracked (multiple zones with same ID overwrite)
//
// 2. Custom Layout Tracking (used for pet widget play area):
//   - Track line numbers during rendering (currentLine counter)
//   - Store positions in a layout struct (petWidgetLayout)
//   - In click handler, compare input.PinnedRelY against stored line numbers
//   - Use input.MouseX for horizontal position within the line
//   - Good for: complex dynamic content, multi-element interactions, precise hit testing
//   - Requires: manual tracking during render, custom click handler
//
// The pet widget uses BOTH methods:
// - BubbleZone for the Feed button (zone.Mark("pet:drop_food", ...))
// - Custom tracking for play area (air lines, ground line)
//
// Line numbers are relative to the widget output start (0-indexed), except ContentStartLine
// which is the absolute content line where the pet widget begins.
type petWidgetLayout struct {
	ContentStartLine int // Absolute content line where pet widget starts (set in RenderForClient)
	FeedLine         int // "Feed" button line (relative to widget start)
	HighAirLine      int // High air (Y=2) line - click drops yarn
	LowAirLine       int // Low air (Y=1) line - click drops yarn
	GroundLine       int // Ground (Y=0) line - click on cat pets, click on poop cleans, else drops yarn
	PlayWidth        int // Width of play area (safePlayWidth) - clicks beyond this are ignored
	WidgetHeight     int // Total widget height in lines
	DebugLine1       int // Y position of debug line 1 (mode triggers)
	DebugLine2       int // Y position of debug line 2 (thought controls)
}

// debugThoughtCategories lists all thought categories for debug bar cycling
var debugThoughtCategories = []string{
	"idle", "happy", "hungry", "sleepy", "yarn", "walking",
	"jumping", "petting", "starving", "guilt", "dead",
	"mouse_spot", "mouse_chase", "mouse_catch", "mouse_kill",
	"poop_jump", "wakeup", "poop",
}

// Pet sprites by style
type petSprites struct {
	Idle, Walking, Jumping, Playing string
	Eating, Sleeping, Happy, Hungry string
	Dead                            string
	Yarn, Food, Poop, Mouse         string
	Blood                           string
	Thought, Heart, Life            string
	HungerIcon, HappyIcon, SadIcon  string
	Ground                          string
}

var petSpritesByStyle = map[string]petSprites{
	"emoji": {
		Idle: "🐱", Walking: "🐱", Jumping: "🐱", Playing: "🐱",
		Eating: "🐱", Sleeping: "😺", Happy: "😻", Hungry: "😿",
		Dead: "💀",
		Yarn: "🧶", Food: "🍖", Poop: "💩", Mouse: "🐭",
		Blood:   "🩸",
		Thought: "💭", Heart: "❤", Life: "💗",
		HungerIcon: "🍖", HappyIcon: "😸", SadIcon: "😿",
		Ground: "·",
	},
	"nerd": {
		Idle: "󰄛", Walking: "󰄛", Jumping: "󰄛", Playing: "󰄛",
		Eating: "󰄛", Sleeping: "󰄛", Happy: "󰄛", Hungry: "󰄛",
		Dead: "",
		Yarn: "", Food: "", Poop: "", Mouse: "",
		Blood:   "",
		Thought: "", Heart: "", Life: "",
		HungerIcon: "", HappyIcon: "", SadIcon: "",
		Ground: "·",
	},
	"ascii": {
		Idle: "=^.^=", Walking: "=^.^=", Jumping: "=^o^=", Playing: "=^.^=",
		Eating: "=^.^=", Sleeping: "=-.~=", Happy: "=^.^=", Hungry: "=;.;=",
		Dead: "x_x",
		Yarn: "@", Food: "o", Poop: ".", Mouse: "<:3",
		Blood:   "x",
		Thought: ">", Heart: "<3", Life: "*",
		HungerIcon: "o", HappyIcon: ":)", SadIcon: ":(",
		Ground: ".",
	},
}

// Session icons by style
var sessionIconsByStyle = map[string]struct{ Session, Clients, Windows string }{
	"nerd":    {Session: "", Clients: "", Windows: ""},
	"emoji":   {Session: "📺", Clients: "👥", Windows: "🪟"},
	"ascii":   {Session: "[tmux]", Clients: "users:", Windows: "wins:"},
	"minimal": {Session: "", Clients: "", Windows: ""},
}

// NewCoordinator creates a new coordinator instance
func NewCoordinator(sessionID string) *Coordinator {
	// Enable TrueColor for accurate theme rendering
	lipgloss.SetColorProfile(termenv.TrueColor)

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		cfg = &config.Config{}
	}

	// Set up debug logging from config if enabled
	if cfg.Sidebar.Debug {
		f, err := os.OpenFile("/tmp/tabby-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			coordinatorDebugLog = log.New(f, "[coord] ", log.LstdFlags|log.Lmicroseconds)
		}
	}

	// Initialize background detector based on theme_mode config (deprecated fallback)
	themeMode := cfg.Sidebar.ThemeMode
	if themeMode == "" {
		themeMode = "auto" // Default to auto-detection
	}
	bgDetector := colors.NewBackgroundDetector(colors.ThemeMode(themeMode))

	// Load color theme (new preset-based system)
	var theme *colors.Theme
	if cfg.Sidebar.Theme != "" {
		t := colors.GetTheme(cfg.Sidebar.Theme)
		theme = &t
	}

	baseIndex := 0
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_base_index").Output(); err == nil {
		val := strings.TrimSpace(string(out))
		if val == "1" || strings.EqualFold(val, "true") {
			baseIndex = 1
		}
	}

	c := &Coordinator{
		sessionID:          sessionID,
		baseIndex:          baseIndex,
		config:             cfg,
		bgDetector:         bgDetector,
		theme:              theme,
		cwdColors:          make(map[string]CWDColorMapping),
		collapsedGroups:    make(map[string]bool),
		clientWidths:       make(map[string]int),
		pendingMenus:       make(map[string][]menuItemDef),
		lastWindowSelect:   make(map[string]time.Time),
		lastWindowByClient: make(map[string]time.Time),
		lastPaneMenuOpen:   make(map[string]time.Time),
		prevPaneBusy:       make(map[string]bool),
		prevPaneTitle:      make(map[string]string),
		aiBellUntil:        make(map[int]int64),
		hookPaneActive:     make(map[string]bool),
		hookPaneBusyIdleAt: make(map[string]int64),
		lastWidth:          25, // Default width for pet physics
		pet: petState{
			Pos:       pos2D{X: 10, Y: 0},
			State:     "idle",
			Direction: 1,
			Hunger:    80,
			Happiness: 80,
			YarnPos:   pos2D{X: -1, Y: 0},
			FoodItem:  pos2D{X: -1, Y: -1},
			MousePos:  pos2D{X: -1, Y: 0},
		},
	}

	// Log theme and background detection if debug enabled
	if cfg.Sidebar.Debug {
		if theme != nil {
			coordinatorDebugLog.Printf("Theme loaded: %s (dark=%v, sidebar_bg=%s)", theme.Name, theme.Dark, theme.SidebarBg)
		} else {
			isDark := bgDetector.IsDarkBackground()
			detectedColor := bgDetector.GetDetectedColor()
			if detectedColor != "" {
				coordinatorDebugLog.Printf("Background detection: theme_mode=%s, detected_dark=%v, color=%s", themeMode, isDark, detectedColor)
			} else {
				coordinatorDebugLog.Printf("Background detection: theme_mode=%s, detected_dark=%v (fallback)", themeMode, isDark)
			}
		}
	}

	// Configure busy detection from config
	tmux.ConfigureBusyDetection(cfg.BusyDetection.ExtraIdle, cfg.BusyDetection.AITools, cfg.BusyDetection.IdleTimeout)

	// Load collapsed groups from tmux option
	c.loadCollapsedGroups()

	// Load pet state from shared file
	c.loadPetState()
	c.loadCWDColors()

	// Initialize LLM if thoughts are enabled
	if cfg.Widgets.Pet.Thoughts {
		if err := initLLM(cfg.Widgets.Pet.LLMProvider, cfg.Widgets.Pet.LLMModel, cfg.Widgets.Pet.LLMAPIKey); err != nil {
			coordinatorDebugLog.Printf("LLM init failed: %v (using default thoughts)", err)
		} else {
			coordinatorDebugLog.Printf("LLM initialized with provider=%s model=%s", cfg.Widgets.Pet.LLMProvider, cfg.Widgets.Pet.LLMModel)
			// Set thought generation interval from config
			if cfg.Widgets.Pet.ThoughtRefreshHours > 0 {
				SetThoughtGenerationInterval(cfg.Widgets.Pet.ThoughtRefreshHours)
			}
			// Trigger initial thought generation
			triggerThoughtGeneration(&c.pet, cfg.Widgets.Pet.Name)
		}
	}

	// Initial window refresh
	c.RefreshWindows()

	// Initial git refresh
	c.RefreshGit()

	// Initial session refresh
	c.RefreshSession()

	// Initialize global width from tmux option
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width").Output(); err == nil {
		if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w > 0 {
			c.globalWidth = w
		} else {
			c.globalWidth = 25 // Default
		}
	}

	// Read collapse state from tmux option
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_collapsed").Output(); err == nil {
		collapsed := strings.TrimSpace(string(out))
		if collapsed == "1" {
			c.sidebarCollapsed = true
			// Also read the previous width for restoring
			if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_previous_width").Output(); err == nil {
				if w, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && w >= 15 {
					c.sidebarPreviousWidth = w
				}
			}
		}
	}

	// Apply global theme styles to tmux (borders, messages, etc.)
	c.applyThemeToTmux()

	return c
}

// GetConfig returns the coordinator's config (for use by main.go)
func (c *Coordinator) GetConfig() *config.Config {
	return c.config
}

// getTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.ActiveFg
	}
	return c.bgDetector.GetDefaultTextColor()
}

// getHeaderTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getHeaderTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.HeaderFg
	}
	return c.bgDetector.GetDefaultHeaderTextColor()
}

// getInactiveTextColorWithFallback returns the specified color, or a theme/background-aware default if empty
func (c *Coordinator) getInactiveTextColorWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.InactiveFg
	}
	return c.bgDetector.GetDefaultInactiveTextColor()
}

// getPaneFgWithFallback returns pane text color, falling back to inactive_fg
func (c *Coordinator) getPaneFgWithFallback() string {
	if c.config.Sidebar.Colors.PaneFg != "" {
		return c.config.Sidebar.Colors.PaneFg
	}
	return c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)
}

// getTreeFgWithFallback returns tree branch color from config, theme, or detector
func (c *Coordinator) getTreeFgWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.TreeFg
	}
	return c.bgDetector.GetDefaultTreeFg()
}

// getDisclosureFgWithFallback returns disclosure icon color from config, theme, or detector
func (c *Coordinator) getDisclosureFgWithFallback(configColor string) string {
	if configColor != "" {
		return configColor
	}
	if c.theme != nil {
		return c.theme.DisclosureFg
	}
	return c.bgDetector.GetDefaultDisclosureFg()
}

// getPaneHeaderActiveBg returns active pane header background from config, theme, or detector
func (c *Coordinator) getPaneHeaderActiveBg() string {
	if c.config.PaneHeader.ActiveBg != "" {
		return c.config.PaneHeader.ActiveBg
	}
	if c.theme != nil {
		return c.theme.PaneActiveBg
	}
	return c.bgDetector.GetDefaultPaneHeaderActiveBg()
}

// getPaneHeaderActiveFg returns active pane header foreground from config, theme, or detector
func (c *Coordinator) getPaneHeaderActiveFg() string {
	if c.config.PaneHeader.ActiveFg != "" {
		return c.config.PaneHeader.ActiveFg
	}
	if c.theme != nil {
		return c.theme.PaneActiveFg
	}
	return c.bgDetector.GetDefaultPaneHeaderActiveFg()
}

// getPaneHeaderInactiveBg returns inactive pane header background from config, theme, or detector
func (c *Coordinator) getPaneHeaderInactiveBg() string {
	if c.config.PaneHeader.InactiveBg != "" {
		return c.config.PaneHeader.InactiveBg
	}
	if c.theme != nil {
		return c.theme.PaneInactiveBg
	}
	return c.bgDetector.GetDefaultPaneHeaderInactiveBg()
}

// getPaneHeaderInactiveFg returns inactive pane header foreground from config, theme, or detector
func (c *Coordinator) getPaneHeaderInactiveFg() string {
	if c.config.PaneHeader.InactiveFg != "" {
		return c.config.PaneHeader.InactiveFg
	}
	if c.theme != nil {
		return c.theme.PaneInactiveFg
	}
	return c.bgDetector.GetDefaultPaneHeaderInactiveFg()
}

// getCommandFg returns command text color from config, theme, or detector
func (c *Coordinator) getCommandFg() string {
	if c.config.PaneHeader.CommandFg != "" {
		return c.config.PaneHeader.CommandFg
	}
	if c.theme != nil {
		return c.theme.CommandFg
	}
	return c.bgDetector.GetDefaultCommandFg()
}

// getButtonFg returns button text color from config, theme, or detector
func (c *Coordinator) getButtonFg() string {
	if c.config.PaneHeader.ButtonFg != "" {
		return c.config.PaneHeader.ButtonFg
	}
	if c.theme != nil {
		return c.theme.PaneButtonFg
	}
	return c.bgDetector.GetDefaultButtonFg()
}

// buildBorderStyle builds a tmux style string from fg and bg colors.
// Returns "" if fg is empty.
func buildBorderStyle(fg, bg string) string {
	if fg == "" {
		return ""
	}
	s := "fg=" + fg
	if bg != "" {
		s += ",bg=" + bg
	}
	return s
}

// getBorderFg returns border color from config, theme, or detector
func (c *Coordinator) getBorderFg() string {
	if c.config.PaneHeader.BorderFg != "" {
		return c.config.PaneHeader.BorderFg
	}
	if c.theme != nil {
		return c.theme.BorderFg
	}
	return c.bgDetector.GetDefaultBorderFg()
}

// getHandleColor returns drag handle color from config, theme, or detector
func (c *Coordinator) getHandleColor() string {
	if c.config.PaneHeader.HandleColor != "" {
		return c.config.PaneHeader.HandleColor
	}
	if c.theme != nil {
		return c.theme.HandleColor
	}
	return c.bgDetector.GetDefaultHandleColor()
}

// GetTerminalBg returns terminal background color from config, theme, or detector
func (c *Coordinator) GetTerminalBg() string {
	if c.config.PaneHeader.TerminalBg != "" {
		return c.config.PaneHeader.TerminalBg
	}
	if c.theme != nil {
		return c.theme.TerminalBg
	}
	return c.bgDetector.GetDefaultTerminalBg()
}

// getDividerFg returns divider color from config, theme, or detector
func (c *Coordinator) getDividerFg() string {
	if c.config.PaneHeader.DividerFg != "" {
		return c.config.PaneHeader.DividerFg
	}
	if c.theme != nil {
		return c.theme.DividerFg
	}
	return c.bgDetector.GetDefaultDividerFg()
}

// getWidgetFg returns widget text color from theme or detector
func (c *Coordinator) getWidgetFg() string {
	if c.theme != nil {
		return c.theme.WidgetFg
	}
	return c.bgDetector.GetDefaultWidgetFg()
}

// getPromptFg returns prompt text color from config, theme, or detector
func (c *Coordinator) getPromptFg() string {
	if c.config.Prompt.Fg != "" {
		return c.config.Prompt.Fg
	}
	if c.theme != nil {
		return c.theme.PromptFg
	}
	return c.bgDetector.GetDefaultPromptFg()
}

// getPromptBg returns prompt background color from config, theme, or detector
func (c *Coordinator) getPromptBg() string {
	if c.config.Prompt.Bg != "" {
		return c.config.Prompt.Bg
	}
	if c.theme != nil {
		return c.theme.PromptBg
	}
	return c.bgDetector.GetDefaultPromptBg()
}

// getMainPaneDirection returns the tmux select-pane flag to navigate
// from the sidebar pane to the main content pane.
// If sidebar is on the left, main pane is to the right (-R).
// If sidebar is on the right, main pane is to the left (-L).
func (c *Coordinator) getMainPaneDirection() string {
	if c.config.Sidebar.Position == "right" {
		return "-L"
	}
	return "-R"
}

// loadCollapsedGroups loads collapsed state from tmux options.
// Runs all tmux queries BEFORE acquiring stateMu to avoid holding the lock
// during external I/O.
func (c *Coordinator) loadCollapsedGroups() {
	// Phase 1: Query tmux for legacy format (outside lock)
	legacyOut, legacyErr := tmuxOutputCtx("show-options", "-v", "-q", "@tabby_collapsed_groups")

	var legacyGroups []string
	useLegacy := false
	if legacyErr == nil && len(legacyOut) > 0 {
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(legacyOut))), &legacyGroups); err == nil {
			useLegacy = true
		}
	}

	if useLegacy {
		// Legacy migration path: assign under lock, then save+unset outside lock.
		c.stateMu.Lock()
		c.collapsedGroups = make(map[string]bool)
		for _, g := range legacyGroups {
			c.collapsedGroups[g] = true
		}
		c.stateMu.Unlock()
		// Migrate: save in new format and remove legacy option (outside lock).
		c.saveCollapsedGroups()
		tmuxRun("set-option", "-u", "@tabby_collapsed_groups")
		return
	}

	// Phase 2: Build group names to check (need lock for config/grouped reads)
	c.stateMu.RLock()
	groupsToCheck := make(map[string]bool)
	for _, group := range c.config.Groups {
		groupsToCheck[group.Name] = true
	}
	for _, gw := range c.grouped {
		groupsToCheck[gw.Name] = true
	}
	groupsToCheck["Default"] = true
	c.stateMu.RUnlock()

	// Phase 3: Query tmux for each group's collapsed state (outside lock)
	collapsedResults := make(map[string]bool)
	for groupName := range groupsToCheck {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
		out, err := tmuxOutputCtx("show-options", "-v", "-q", optName)
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			collapsedResults[groupName] = true
		}
	}

	// Phase 4: Assign results under lock (minimal critical section)
	c.stateMu.Lock()
	c.collapsedGroups = collapsedResults
	c.stateMu.Unlock()
}

// saveCollapsedGroups saves collapsed state to tmux options
func (c *Coordinator) saveCollapsedGroups() {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	c.saveCollapsedGroupsLocked()
}

// saveCollapsedGroupsLocked saves collapsed state (caller must hold stateMu)
func (c *Coordinator) saveCollapsedGroupsLocked() {
	// Build a set of all known group names (from config + current grouped windows)
	knownGroups := make(map[string]bool)
	for _, group := range c.config.Groups {
		knownGroups[group.Name] = true
	}
	for _, gw := range c.grouped {
		knownGroups[gw.Name] = true
	}
	knownGroups["Default"] = true

	// Save collapsed state for ALL known groups
	// This ensures we don't lose state for dynamically created groups
	for groupName := range knownGroups {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
		if c.collapsedGroups[groupName] {
			exec.Command("tmux", "set-option", optName, "1").Run()
		} else {
			exec.Command("tmux", "set-option", "-u", optName).Run()
		}
	}

	// Also save any collapsed groups that aren't in knownGroups (edge case)
	for groupName := range c.collapsedGroups {
		if !knownGroups[groupName] {
			optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(groupName, " ", "_"))
			exec.Command("tmux", "set-option", optName, "1").Run()
		}
	}
}

// petStatePath returns the path to the shared pet state file
func petStatePath() string {
	paths.EnsureStateDir()
	return paths.StatePath("pet.json")
}

func cwdColorsPath() string {
	paths.EnsureStateDir()
	return paths.StatePath("cwd-colors.json")
}

// loadPetState loads pet state from disk (used once at startup for persistence across restarts).
func (c *Coordinator) loadPetState() {
	data, err := os.ReadFile(petStatePath())
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.pet)
}

// savePetStateData saves the given pet state snapshot to the shared file.
// Safe to call without holding stateMu since it only writes the provided data.
func savePetStateData(pet petState) {
	data, _ := json.Marshal(pet)
	os.WriteFile(petStatePath(), data, 0644)
}

// savePetState saves the pet state to the shared file.
// Caller must NOT hold stateMu — this performs file I/O.
// For call sites that hold stateMu, snapshot c.pet first, unlock, then call savePetStateData().
func (c *Coordinator) savePetState() {
	data, _ := json.Marshal(c.pet)
	os.WriteFile(petStatePath(), data, 0644)
}

func (c *Coordinator) loadCWDColors() {
	data, err := os.ReadFile(cwdColorsPath())
	if err != nil {
		return
	}

	loaded := make(map[string]CWDColorMapping)
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}

	c.cwdColorsMu.Lock()
	c.cwdColors = loaded
	c.cwdColorsMu.Unlock()
}

func (c *Coordinator) saveCWDColors() {
	c.cwdColorsMu.RLock()
	cloned := make(map[string]CWDColorMapping, len(c.cwdColors))
	for k, v := range c.cwdColors {
		cloned[k] = v
	}
	c.cwdColorsMu.RUnlock()

	data, err := json.MarshalIndent(cloned, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(cwdColorsPath(), data, 0644)
}

func normalizeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return filepath.Clean(cwd)
}

func firstPaneCWD(win tmux.Window) string {
	if len(win.Panes) == 0 {
		return ""
	}
	return normalizeCWD(win.Panes[0].CurrentPath)
}

func (c *Coordinator) getCWDColorMapping(cwd string) (CWDColorMapping, bool) {
	normalized := normalizeCWD(cwd)
	if normalized == "" {
		return CWDColorMapping{}, false
	}

	c.cwdColorsMu.RLock()
	mapping, ok := c.cwdColors[normalized]
	c.cwdColorsMu.RUnlock()
	return mapping, ok
}

func (c *Coordinator) setCWDColor(cwd, color string) {
	normalized := normalizeCWD(cwd)
	if normalized == "" {
		return
	}

	c.cwdColorsMu.Lock()
	mapping := c.cwdColors[normalized]
	mapping.Color = strings.TrimSpace(color)
	if mapping.Color == "" && strings.TrimSpace(mapping.Icon) == "" {
		delete(c.cwdColors, normalized)
	} else {
		c.cwdColors[normalized] = mapping
	}
	c.cwdColorsMu.Unlock()

	c.saveCWDColors()
}

func (c *Coordinator) setCWDIcon(cwd, icon string) {
	normalized := normalizeCWD(cwd)
	if normalized == "" {
		return
	}

	c.cwdColorsMu.Lock()
	mapping := c.cwdColors[normalized]
	mapping.Icon = strings.TrimSpace(icon)
	if strings.TrimSpace(mapping.Color) == "" && mapping.Icon == "" {
		delete(c.cwdColors, normalized)
	} else {
		c.cwdColors[normalized] = mapping
	}
	c.cwdColorsMu.Unlock()

	c.saveCWDColors()
}

func (c *Coordinator) getWindowFirstPaneCWDByIndex(windowIndex int) string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for _, win := range c.windows {
		if win.Index == windowIndex {
			return firstPaneCWD(win)
		}
	}
	return ""
}

func (c *Coordinator) getActiveWindowFirstPaneCWD() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	for _, win := range c.windows {
		if win.Active {
			return firstPaneCWD(win)
		}
	}
	return ""
}

func (c *Coordinator) resolveWindowCWD(windowIndex int) string {
	cwd := c.getWindowFirstPaneCWDByIndex(windowIndex)
	if cwd != "" {
		return cwd
	}
	return c.getActiveWindowFirstPaneCWD()
}

func (c *Coordinator) windowTargetForIndex(windowIndex int) string {
	target := fmt.Sprintf(":%d", windowIndex)
	if c.sessionID != "" {
		target = fmt.Sprintf("%s:%d", c.sessionID, windowIndex)
	}
	return target
}

func (c *Coordinator) setWindowColor(windowIndex int, color string) {
	trimmedColor := strings.TrimSpace(color)
	if trimmedColor == "" {
		return
	}

	windowTarget := c.windowTargetForIndex(windowIndex)
	exec.Command("tmux", "set-window-option", "-t", windowTarget, "@tabby_color", trimmedColor).Run()

	if cwd := c.resolveWindowCWD(windowIndex); cwd != "" {
		c.setCWDColor(cwd, trimmedColor)
	}
}

func (c *Coordinator) setWindowIcon(windowIndex int, icon string) {
	trimmedIcon := strings.TrimSpace(icon)
	windowTarget := c.windowTargetForIndex(windowIndex)
	if trimmedIcon == "" {
		exec.Command("tmux", "set-window-option", "-t", windowTarget, "-u", "@tabby_icon").Run()
	} else {
		exec.Command("tmux", "set-window-option", "-t", windowTarget, "@tabby_icon", trimmedIcon).Run()
	}

	if cwd := c.resolveWindowCWD(windowIndex); cwd != "" {
		c.setCWDIcon(cwd, trimmedIcon)
	}
}

func (c *Coordinator) applyCWDColorIconMappings(windows []tmux.Window) {
	for i := range windows {
		cwd := firstPaneCWD(windows[i])
		if cwd == "" {
			continue
		}

		mapping, ok := c.getCWDColorMapping(cwd)
		if !ok {
			continue
		}

		if windows[i].CustomColor == "" && strings.TrimSpace(mapping.Color) != "" {
			exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "@tabby_color", mapping.Color).Run()
			windows[i].CustomColor = mapping.Color
		}

		if strings.TrimSpace(windows[i].Icon) == "" && strings.TrimSpace(mapping.Icon) != "" {
			exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "@tabby_icon", mapping.Icon).Run()
			windows[i].Icon = mapping.Icon
		}
	}
}

// RefreshWindows fetches current window/pane state from tmux
func (c *Coordinator) RefreshWindows() {
	// Do all external I/O (tmux, config, ps) BEFORE acquiring stateMu.
	// Holding stateMu during slow external calls causes lock contention:
	// leaked task goroutines that timed out continue holding the lock,
	// blocking subsequent tasks that need stateMu.RLock() (e.g. handleWidthSync
	// via BroadcastRender), causing LOOP_STALL and daemon termination.

	newCfg, _ := config.LoadConfig(config.DefaultConfigPath())

	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		return
	}

	c.applyCWDColorIconMappings(windows)

	// Pre-load process tree BEFORE acquiring stateMu. loadProcessTree runs
	// ps -A which can be slow; running it inside the lock blocks IncrementSpinner
	// and other stateMu-dependent goroutines, causing LOOP_STALL / daemon crash.
	// Reading c.lastProcessCheck here without the lock is safe: this function
	// runs in a single goroutine (window_tick) and c.lastProcessCheck is only
	// written later by processAIToolStates in this same call.
	var preloadedProcessTree *processTree
	if c.config.Indicators.Busy.Enabled || c.config.Indicators.Input.Enabled {
		if time.Since(c.lastProcessCheck) > 2*time.Second {
			preloadedProcessTree = loadProcessTree()
		}
	}

	prefixModeRaw := ""
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if out, err := exec.CommandContext(ctx, "tmux", "show-option", "-gqv", "@tabby_prefix_mode").Output(); err == nil {
			prefixModeRaw = strings.TrimSpace(string(out))
		}
		cancel()
	}

	c.stateMu.Lock()

	if newCfg != nil {
		c.config = newCfg
	}

	// Note: collapsed groups state is managed in-memory and synced to tmux options
	// We don't reload here to avoid race conditions with toggle_group action

	c.windows = windows

	// Auto-sync window names from active pane title, unless name is locked.
	// Collects pending rename ops for execution after unlock.
	pendingRenames := c.syncWindowNames()

	// Detect AI tool busy/done/idle states using state transitions.
	// Collects pending tmux set-option ops for execution after unlock.
	aiToolOps := c.processAIToolStates(preloadedProcessTree)

	c.grouped = grouping.GroupWindowsWithOptions(windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.computeVisualPositions()
	pendingMoves := c.syncWindowIndices()

	if prefixModeRaw != "" {
		c.config.Sidebar.PrefixMode = (prefixModeRaw == "1" || prefixModeRaw == "true")
	}

	// Build pane header color args while holding the lock (read-only access to state).
	// The actual tmux exec happens AFTER unlock to avoid holding stateMu during
	// slow external calls which causes LOOP_STALL and daemon termination.
	colorArgs := c.buildPaneHeaderColorArgs()
	c.stateMu.Unlock()

	// Run the tmux set-option commands outside the lock with a timeout.
	if len(colorArgs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "tmux", colorArgs...).Run()
	}

	// Execute deferred AI tool state tmux set-option ops outside the lock.
	for _, op := range aiToolOps {
		if op.unset {
			tmuxRun("set-option", "-w", "-t", op.windowID, "-u", op.key)
		} else {
			tmuxRun("set-option", "-w", "-t", op.windowID, op.key, op.value)
		}
	}

	// Execute deferred window rename ops outside the lock.
	for _, op := range pendingRenames {
		tmuxRun("rename-window", "-t", op.windowID, op.desiredName)
		tmuxRun("set-window-option", "-t", op.windowID, "@tabby_name_locked", "0")
	}

	// Execute deferred window move ops outside the lock.
	for _, op := range pendingMoves {
		tmuxRun("move-window", "-s", op.src, "-t", op.dst)
	}
}

// SetActiveWindowOptimistic flips the Active flag on c.windows so the next
// BroadcastRender uses the correct active window immediately, without waiting
// for a full RefreshWindows round-trip through tmux.
func (c *Coordinator) SetActiveWindowOptimistic(windowID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	for i := range c.windows {
		c.windows[i].Active = (c.windows[i].ID == windowID)
	}
	// Re-group so generateSidebarHeader picks up the new active window's colors
	c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.computeVisualPositions()
}

// tmuxSetOption is a pending tmux set-option command collected under lock
// for deferred execution after the lock is released.
type tmuxSetOption struct {
	windowID string
	key      string
	value    string // value to set (ignored when unset=true)
	unset    bool   // true means use -u flag to unset the option
}

// processAIToolStates detects AI tool busy/done/idle states using stateful
// transition tracking. Detection is per-pane: each AI pane gets its own
// busy/input state stored in pane.AIBusy and pane.AIInput.
//
// For multi-pane windows, indicators appear on individual pane lines in the
// sidebar. For single-pane windows, indicators stay at the window tab level.
//
// Detection signals (universal, works for any AI tool):
//   - Braille spinner in pane title (U+2801-U+28FF): tool is working (Claude Code)
//   - Pane title changed since last cycle: tool is active (OpenCode, Gemini, etc.)
//   - Process tree CPU usage > 5%: tool is working (universal)
//
// State machine per pane:
//   - Currently busy -> Busy indicator (animated spinner)
//   - Was busy, now idle (tool still running) -> Input indicator (needs user input)
//   - AI tool exited (was present, now gone) -> Bell indicator at window level
//   - Was idle, still idle -> no indicator
func (c *Coordinator) processAIToolStates(preloaded *processTree) []tmuxSetOption {
	var pending []tmuxSetOption
	now := time.Now().Unix()

	// Load process table once per cycle for CPU-based busy detection.
	// Throttle to max once per 2s; skip if indicators are disabled.
	// preloaded is non-nil when RefreshWindows pre-fetched it outside the lock
	// (the normal path). The fallback inline load should not be reached in
	// practice but is kept as a safety net for direct callers.
	var pt *processTree
	needsProcessTree := c.config.Indicators.Busy.Enabled || c.config.Indicators.Input.Enabled
	if needsProcessTree {
		if preloaded != nil {
			pt = preloaded
			c.cachedProcessTree = pt
			c.lastProcessCheck = time.Now()
		} else if time.Since(c.lastProcessCheck) > 2*time.Second {
			pt = loadProcessTree()
			c.cachedProcessTree = pt
			c.lastProcessCheck = time.Now()
		} else {
			pt = c.cachedProcessTree
		}
	}

	// Track which pane IDs we see this cycle for stale cleanup
	seenPanes := make(map[string]bool)

	for i := range c.windows {
		win := &c.windows[i]
		idx := win.Index
		contentPaneCount := 0
		for j := range win.Panes {
			if isAuxiliaryPane(win.Panes[j]) {
				continue
			}
			contentPaneCount++
		}
		multiPane := contentPaneCount > 1

		// Find all AI tool panes in this window
		var aiPanes []*tmux.Pane
		for j := range win.Panes {
			if isAuxiliaryPane(win.Panes[j]) {
				continue
			}
			if tmux.IsAITool(win.Panes[j].Command) {
				aiPanes = append(aiPanes, &win.Panes[j])
			}
		}

		// Check for expiring bell indicators (window-level, from AI tool exit)
		if expiry, ok := c.aiBellUntil[idx]; ok {
			if now < expiry {
				win.Bell = true
			} else {
				delete(c.aiBellUntil, idx)
			}
		}

		if len(aiPanes) == 0 {
			// No AI tool in this window.
			// Check if any pane in this window WAS an AI tool last cycle (tool exited).
			anyPrevAI := false
			for j := range win.Panes {
				if isAuxiliaryPane(win.Panes[j]) {
					continue
				}
				pid := win.Panes[j].ID
				if c.prevPaneBusy[pid] || c.prevPaneTitle[pid] != "" {
					anyPrevAI = true
					delete(c.prevPaneBusy, pid)
					delete(c.prevPaneTitle, pid)
					delete(c.hookPaneActive, pid)
					delete(c.hookPaneBusyIdleAt, pid)
				}
			}
			if anyPrevAI {
				win.Bell = true
				win.Input = false
				c.aiBellUntil[idx] = now + 30
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_bell", value: "1"})
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", value: ""})
			}
			// Clear stale hook indicators on windows with no AI tools.
			// Handles cases where the daemon wasn't tracking the AI tool
			// (e.g., daemon restart, race between hook and exit) but hooks
			// left indicators set.
			if win.Busy {
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_busy", unset: true})
				win.Busy = false
			}
			if win.Input {
				pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", unset: true})
				win.Input = false
			}
			continue
		}

		// If this is the active window, clear window-level input indicator
		if win.Active && win.Input {
			win.Input = false
			pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_input", unset: true})
		}

		// === Per-pane AI detection ===
		// Hook-based: @tabby_busy is set at window level. When hooks are active,
		// attribute busy to the pane with a spinner, or first AI pane as fallback.
		hookBusyPaneID := ""
		if win.Busy {
			// Find which pane the hook likely refers to
			for _, p := range aiPanes {
				if tmux.HasSpinner(p.Title) {
					hookBusyPaneID = p.ID
					break
				}
			}
			if hookBusyPaneID == "" {
				hookBusyPaneID = aiPanes[0].ID
			}
		}

		// Hook-based input: @tabby_input at window level -> attribute to active AI pane or first
		hookInputPaneID := ""
		if win.Input && !win.Active {
			for _, p := range aiPanes {
				if tmux.HasIdleIcon(p.Title) {
					hookInputPaneID = p.ID
					break
				}
			}
			if hookInputPaneID == "" {
				hookInputPaneID = aiPanes[0].ID
			}
		}

		// Staleness check for hook-based busy (window-level @tabby_busy)
		if win.Busy {
			anySpinner := false
			for _, p := range aiPanes {
				if tmux.HasSpinner(p.Title) {
					anySpinner = true
					break
				}
			}
			if !anySpinner {
				stalePID := hookBusyPaneID
				if _, ok := c.hookPaneBusyIdleAt[stalePID]; !ok {
					c.hookPaneBusyIdleAt[stalePID] = now
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d): hook says busy but no spinner, starting staleness timer", stalePID, idx)
				} else if now-c.hookPaneBusyIdleAt[stalePID] > 10 {
					idleSecs := now - c.hookPaneBusyIdleAt[stalePID]
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d): auto-clearing stale @tabby_busy (idle for %ds)", stalePID, idx, idleSecs)
					logEvent("STALE_BUSY_CLEAR pane=%s window=%d idle_secs=%d", stalePID, idx, idleSecs)
					pending = append(pending, tmuxSetOption{windowID: win.ID, key: "@tabby_busy", unset: true})
					win.Busy = false
					hookBusyPaneID = ""
					delete(c.hookPaneBusyIdleAt, stalePID)
				}
			} else {
				// Spinner found — reset staleness for the busy pane
				delete(c.hookPaneBusyIdleAt, hookBusyPaneID)
			}
		}

		// Process each AI pane individually
		for _, pane := range aiPanes {
			pid := pane.ID
			seenPanes[pid] = true

			hasSpinner := tmux.HasSpinner(pane.Title)
			hasIdle := tmux.HasIdleIcon(pane.Title)

			// === Hook-based detection for this pane ===
			if win.Busy && pid == hookBusyPaneID {
				// Hook says this pane is busy
				c.hookPaneActive[pid] = true
				pane.AIBusy = true
				pane.AIInput = false
				if !c.prevPaneBusy[pid] {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): -> BUSY (hook)",
						pid, idx, pane.Command)
				}
				c.prevPaneBusy[pid] = true
				delete(c.aiBellUntil, idx)
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			if pid == hookInputPaneID {
				// Hook says this pane needs input
				pane.AIInput = true
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			// Hook-active bypass: when hooks previously controlled this pane
			// and now say idle, trust that unless spinner overrides.
			if c.hookPaneActive[pid] && !win.Busy && !hasSpinner {
				if c.prevPaneBusy[pid] {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): BUSY -> IDLE (hook)",
						pid, idx, pane.Command)
				}
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				c.prevPaneTitle[pid] = pane.Title
				continue
			}

			// === Passive detection ===
			busy := false

			// Signal 1: Braille spinner in this pane's title
			if hasSpinner {
				busy = true
			}

			// Signal 2: Title changed since last cycle
			prevTitle, hasPrev := c.prevPaneTitle[pid]
			hadSpinner := hasPrev && tmux.HasSpinner(prevTitle)
			spinnerCleared := hadSpinner && !hasSpinner
			if hasPrev && pane.Title != prevTitle && !spinnerCleared && !hasIdle {
				busy = true
			}

			// Signal 3: CPU usage (skip when idle icon present)
			if !busy && pane.PID > 0 && !hasIdle {
				cpuPct := pt.treeCPU(pane.PID)
				if cpuPct > 5.0 {
					busy = true
				}
			}

			// State machine
			wasBusy := c.prevPaneBusy[pid]

			if busy {
				pane.AIBusy = true
				pane.AIInput = false
				c.prevPaneBusy[pid] = true
				delete(c.aiBellUntil, idx)
				if !wasBusy {
					coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): -> BUSY (spinner=%v titleChanged=%v)",
						pid, idx, pane.Command, hasSpinner, hasPrev && pane.Title != prevTitle)
				}
			} else if wasBusy {
				// busy -> idle: tool waiting for user input
				pane.AIInput = true
				pane.AIBusy = false
				c.prevPaneBusy[pid] = false
				coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): BUSY -> INPUT (title=%q)",
					pid, idx, pane.Command, pane.Title)
			} else if !hasPrev {
				coordinatorDebugLog.Printf("[AI] Pane %s (win %d, %s): FIRST SEEN (title=%q)",
					pid, idx, pane.Command, pane.Title)
			}

			c.prevPaneTitle[pid] = pane.Title
		}

		// === Derive window-level state ===
		// Single-pane: promote pane state to window (current behavior)
		// Multi-pane: indicators stay on pane lines; window shows nothing for busy/input
		if !multiPane && len(aiPanes) == 1 {
			pane := aiPanes[0]
			if pane.AIBusy {
				win.Busy = true
				win.Input = false
			} else if pane.AIInput && !win.Active {
				win.Input = true
				win.Busy = false
			}
		} else if multiPane {
			// Multi-pane: clear window-level busy/input (indicators are on pane lines)
			// But if the window had @tabby_busy from hooks, we already handled it above.
			// Only clear the window-level flags that were set by passive detection.
			anyPaneBusy := false
			anyPaneInput := false
			for _, p := range aiPanes {
				if p.AIBusy {
					anyPaneBusy = true
				}
				if p.AIInput {
					anyPaneInput = true
				}
			}
			// For collapsed multi-pane: aggregate to window level
			if win.Collapsed {
				win.Busy = anyPaneBusy
				if !anyPaneBusy && anyPaneInput && !win.Active {
					win.Input = true
				}
			} else {
				// Expanded multi-pane: no window-level busy/input (pane lines show it)
				win.Busy = false
				win.Input = false
			}
		}

		// Clear window-level input for active panes in active window
		if win.Active && multiPane {
			for _, pane := range aiPanes {
				if pane.Active {
					pane.AIInput = false
				}
			}
		}
	}

	// Cleanup stale pane state for panes that no longer exist
	for pid := range c.prevPaneBusy {
		if !seenPanes[pid] {
			delete(c.prevPaneBusy, pid)
			delete(c.prevPaneTitle, pid)
			delete(c.hookPaneActive, pid)
			delete(c.hookPaneBusyIdleAt, pid)
		}
	}
	for pid := range c.prevPaneTitle {
		if !seenPanes[pid] {
			delete(c.prevPaneTitle, pid)
		}
	}
	return pending
}

// processTree holds pre-parsed process table data for CPU-based busy detection.
// Call loadProcessTree() once per cycle and reuse for all windows.
type processTree struct {
	children map[int][]int   // ppid -> child pids
	cpuByPID map[int]float64 // pid -> cpu%
}

// loadProcessTree reads the system process table once. Returns nil on error.
func loadProcessTree() *processTree {
	t := perf.Start("loadProcessTree")
	defer t.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,%cpu=").Output()
	if err != nil {
		return nil
	}
	pt := &processTree{
		children: make(map[int][]int),
		cpuByPID: make(map[int]float64),
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		cpu, err3 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		pt.children[ppid] = append(pt.children[ppid], pid)
		pt.cpuByPID[pid] = cpu
	}
	return pt
}

// treeCPU returns the total CPU% for a process and all its descendants.
func (pt *processTree) treeCPU(pid int) float64 {
	if pt == nil || pid <= 0 {
		return 0
	}
	visited := make(map[int]bool)
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		queue = append(queue, pt.children[cur]...)
	}
	var total float64
	for p := range visited {
		total += pt.cpuByPID[p]
	}
	return total
}

// computeVisualPositions builds a map of window ID -> visual position in the
// sidebar. Visual position is a sequential counter (0, 1, 2...) based on the
// order windows appear in the grouped display, which may differ from tmux's
// window index when groups reorder windows.
func (c *Coordinator) computeVisualPositions() {
	pos := make(map[string]int)
	n := c.baseIndex
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			pos[win.ID] = n
			n++
		}
	}
	c.windowVisualPos = pos
}

// syncWindowIndices renumbers tmux windows so their indices match the visual
// tmuxWindowRename is a pending window rename operation collected under lock
// for deferred execution after the lock is released.
type tmuxWindowRename struct {
	windowID    string
	desiredName string
}

// syncWindowNames updates window names from pane directories for
// windows that haven't been explicitly renamed (NameLocked=false).
// Uses the directory basename; combines with " | " when panes are in different dirs.
// Returns pending rename operations to execute after stateMu is released.
func (c *Coordinator) syncWindowNames() []tmuxWindowRename {
	home := os.Getenv("HOME")
	showSSHHost := c.config.Sidebar.ShowSSHHost
	var pending []tmuxWindowRename

	for i := range c.windows {
		if c.windows[i].NameLocked {
			continue
		}
		if len(c.windows[i].Panes) == 0 {
			continue
		}

		// Collect unique directory basenames from all panes, preserving order.
		seen := make(map[string]bool)
		var dirs []string
		for _, pane := range c.windows[i].Panes {
			var name string
			if showSSHHost && pane.Remote && pane.Command == "ssh" {
				if host := tmux.SSHHostForPane(pane.PID); host != "" {
					name = host
				}
			}
			if name == "" {
				p := pane.CurrentPath
				if p == "" {
					continue
				}
				name = shortenPath(p, home)
			}
			if !seen[name] {
				seen[name] = true
				dirs = append(dirs, name)
			}
		}

		if len(dirs) == 0 {
			continue
		}

		desiredName := strings.Join(dirs, " | ")

		if desiredName == c.windows[i].Name {
			continue
		}

		pending = append(pending, tmuxWindowRename{windowID: c.windows[i].ID, desiredName: desiredName})
		c.windows[i].Name = desiredName
	}
	return pending
}

// shortenPath converts a full path to a short display name.
// /Users/b -> ~, /Users/b/git/tabby -> tabby, / -> /
func shortenPath(p, home string) string {
	if p == "/" {
		return "/"
	}
	// Use basename for most paths
	base := filepath.Base(p)
	// If the path IS the home directory, show ~
	if p == home {
		return "~"
	}
	return base
}

// tmuxWindowMove is a pending tmux move-window operation collected under lock
// for deferred execution after the lock is released.
type tmuxWindowMove struct {
	src string // source window ID or :index
	dst string // destination :index
}

// positions shown in the sidebar. This ensures prefix+N selects the window
// the user sees as "N" in the sidebar.
// Returns pending move operations to execute after stateMu is released.
func (c *Coordinator) syncWindowIndices() []tmuxWindowMove {
	// Build desired mapping: visual position -> window ID
	type winMapping struct {
		id           string
		currentIndex int
		desiredIndex int
	}

	var mappings []winMapping
	allMatch := true
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			desired := c.windowVisualPos[win.ID]
			mappings = append(mappings, winMapping{
				id:           win.ID,
				currentIndex: win.Index,
				desiredIndex: desired,
			})
			if win.Index != desired {
				allMatch = false
			}
		}
	}

	if allMatch {
		return nil // Already in order
	}

	coordinatorDebugLog.Printf("syncWindowIndices: reordering %d windows", len(mappings))

	// Collect pending moves: Phase 1 (to temp indices) + Phase 2 (to desired indices).
	var pending []tmuxWindowMove

	// Phase 1: Move all windows to high temporary indices to avoid conflicts.
	for i, m := range mappings {
		tmpIdx := 1000 + i
		if m.currentIndex != tmpIdx {
			pending = append(pending, tmuxWindowMove{
				src: m.id,
				dst: fmt.Sprintf(":%d", tmpIdx),
			})
		}
	}

	// Phase 2: Move windows from temp indices to their desired positions.
	for i, m := range mappings {
		tmpIdx := 1000 + i
		pending = append(pending, tmuxWindowMove{
			src: fmt.Sprintf(":%d", tmpIdx),
			dst: fmt.Sprintf(":%d", m.desiredIndex),
		})
	}

	// Update local state to reflect new indices
	for i := range c.windows {
		if desired, ok := c.windowVisualPos[c.windows[i].ID]; ok {
			c.windows[i].Index = desired
		}
	}

	coordinatorDebugLog.Printf("syncWindowIndices: done")
	return pending
}

// updatePaneHeaderColors sets per-window tmux options for pane header colors
// based on the group theme. Uses @tabby_pane_active and @tabby_pane_inactive.
// When auto_border is enabled, also sets pane-border-style and pane-active-border-style.
// applyThemeToTmux applies the current theme's global styles to tmux options
func (c *Coordinator) applyThemeToTmux() {
	if c.theme == nil {
		return
	}

	// Resolve border colors: config > theme > detector fallback
	borderFg := c.config.PaneHeader.BorderFg
	if borderFg == "" {
		borderFg = c.theme.BorderFg
	}
	borderBg := c.config.PaneHeader.BorderBg

	activeFg := c.config.PaneHeader.ActiveBorderFg
	if activeFg == "" {
		activeFg = borderFg // fallback to inactive fg
	}
	activeBg := c.config.PaneHeader.ActiveBorderBg
	if activeBg == "" {
		activeBg = borderBg // fallback to inactive bg
	}

	// Apply inactive border style
	if inactiveStyle := buildBorderStyle(borderFg, borderBg); inactiveStyle != "" {
		exec.Command("tmux", "set-option", "-g", "pane-border-style", inactiveStyle).Run()
	}

	// Apply active border style
	if activeStyle := buildBorderStyle(activeFg, activeBg); activeStyle != "" {
		exec.Command("tmux", "set-option", "-g", "pane-active-border-style", activeStyle).Run()
	}

	// With overlay pane headers enabled, tmux border lines add an extra visual row
	// between header and content that looks like a duplicate/non-functional header.
	// Prefer runtime tmux option as source of truth, with config as fallback.
	paneHeadersEnabled := c.config.Sidebar.PaneHeaders
	if out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_pane_headers").Output(); err == nil {
		paneHeadersEnabled = strings.TrimSpace(string(out)) == "on"
	}
	if paneHeadersEnabled && !c.config.PaneHeader.CustomBorder {
		exec.Command("tmux", "set-option", "-g", "pane-border-lines", "simple").Run()
		if c.config.PaneHeader.TerminalBg != "" {
			style := fmt.Sprintf("fg=%s,bg=%s", c.config.PaneHeader.TerminalBg, c.config.PaneHeader.TerminalBg)
			exec.Command("tmux", "set-option", "-g", "pane-border-style", style).Run()
			exec.Command("tmux", "set-option", "-g", "pane-active-border-style", style).Run()
		}
	} else if c.config.PaneHeader.BorderLines != "" {
		exec.Command("tmux", "set-option", "-g", "pane-border-lines", c.config.PaneHeader.BorderLines).Run()
	}

	// Apply message/mode styles (command prompt)
	if c.theme.PromptBg != "" && c.theme.PromptFg != "" {
		style := fmt.Sprintf("fg=%s,bg=%s", c.theme.PromptFg, c.theme.PromptBg)
		exec.Command("tmux", "set-option", "-g", "message-style", style).Run()
		exec.Command("tmux", "set-option", "-g", "message-command-style", style).Run()
	}

	// Apply inactive pane dimming if enabled
	if c.config.PaneHeader.DimInactive {
		dimOpacity := c.config.PaneHeader.DimOpacity
		if dimOpacity <= 0 || dimOpacity > 1 {
			dimOpacity = 0.5 // Default to 50% brightness
		}
		// Use theme's ActiveFg as base color for dimming
		baseFg := c.theme.ActiveFg
		if baseFg == "" {
			baseFg = "#ffffff" // Default white
		}
		baseBg := c.theme.TerminalBg
		if baseBg == "" {
			baseBg = c.theme.SidebarBg
		}

		// Dim the foreground color for inactive panes
		dimFg := dimColor(baseFg, dimOpacity)

		inactiveStyle := fmt.Sprintf("fg=%s", dimFg)
		if baseBg != "" {
			inactiveStyle += fmt.Sprintf(",bg=%s", baseBg)
		}
		exec.Command("tmux", "set-option", "-g", "window-style", inactiveStyle).Run()

		// Active pane gets full brightness
		activeStyle := fmt.Sprintf("fg=%s", baseFg)
		if baseBg != "" {
			activeStyle += fmt.Sprintf(",bg=%s", baseBg)
		}
		exec.Command("tmux", "set-option", "-g", "window-active-style", activeStyle).Run()
	}
}

// ApplyThemeToPane applies theme-specific styles (like background) to a tmux pane
func (c *Coordinator) ApplyThemeToPane(paneID string) {
	if c.theme == nil || paneID == "" {
		return
	}

	// Use TerminalBg from theme, or fall back to SidebarBg
	bg := c.theme.TerminalBg
	if bg == "" {
		bg = c.theme.SidebarBg
	}

	coordinatorDebugLog.Printf("ApplyThemeToPane: pane=%s bg=%s", paneID, bg)

	if bg != "" {
		// Set pane-specific window-style to match the theme background
		// This makes transparency in renderers work correctly
		style := fmt.Sprintf("bg=%s", bg)
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", style).Run()
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-active-style", style).Run()
	}
}

// buildPaneHeaderColorArgs builds the tmux set-option args for pane header colors.
// Called under stateMu; returns the args without executing (caller runs tmux outside the lock).
func (c *Coordinator) buildPaneHeaderColorArgs() []string {
	grouped := c.grouped
	autoBorder := c.config.PaneHeader.AutoBorder
	borderFromTab := c.config.PaneHeader.BorderFromTab
	borderBg := c.config.PaneHeader.BorderBg
	activeBorderFg := c.config.PaneHeader.ActiveBorderFg
	activeBorderBg := c.config.PaneHeader.ActiveBorderBg
	if activeBorderBg == "" {
		activeBorderBg = borderBg
	}
	// Resolve border fg: config border_fg > group theme fg > same as bg (transparent/solid bar)
	configBorderFg := c.config.PaneHeader.BorderFg
	// Shell prompt integration: default enabled unless explicitly disabled
	shellIntegration := c.config.Prompt.ShellIntegration == nil || *c.config.Prompt.ShellIntegration
	promptFallbackIcon := c.config.Prompt.FallbackIcon
	if promptFallbackIcon == "" {
		promptFallbackIcon = "•"
	}
	var args []string
	for _, group := range grouped {
		baseBg := group.Theme.Bg
		for _, win := range group.Windows {
			tabBg := baseBg
			if win.CustomColor != "" {
				tabBg = win.CustomColor
			}
			// Border fg: config > group fg > same as bg (solid color bar)
			baseFg := configBorderFg
			if baseFg == "" {
				baseFg = group.Theme.Fg
			}
			if baseFg == "" {
				baseFg = tabBg
			}
			if len(args) > 0 {
				args = append(args, ";")
			}
			args = append(args, "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_active", tabBg)
			args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_inactive", tabBg)
			// Shell prompt integration: store effective icon per window
			if shellIntegration {
				effectiveIcon := group.Theme.Icon
				if win.Icon != "" {
					effectiveIcon = win.Icon
				}
				if effectiveIcon == "" {
					effectiveIcon = promptFallbackIcon
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_prompt_icon", effectiveIcon)
			}

			if autoBorder || borderFromTab {
				// Border fg = tab's text color, border bg = tab's bg color
				bFg := baseFg
				bBg := tabBg

				// Active border: config overrides > tab colors
				aFg := activeBorderFg
				if aFg == "" {
					aFg = bFg
				}
				aBg := activeBorderBg
				if aBg == "" {
					aBg = bBg
				}
				activeStyle := buildBorderStyle(aFg, aBg)
				if activeStyle == "" {
					activeStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index),
					"pane-active-border-style", activeStyle)

				// Inactive border: desaturate when dim_inactive is enabled
				iFg := bFg
				iBg := borderBg
				if iBg == "" {
					iBg = bBg
				}
				if c.config.PaneHeader.DimInactive {
					opacity := c.config.PaneHeader.DimOpacity
					if opacity <= 0 || opacity > 1 {
						opacity = 0.6
					}
					tBg := c.config.PaneHeader.TerminalBg
					iFg = desaturateHex(iFg, opacity, tBg)
					iBg = desaturateHex(iBg, opacity, tBg)
				}
				inactiveStyle := buildBorderStyle(iFg, iBg)
				if inactiveStyle == "" {
					inactiveStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
				}
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index),
					"pane-border-style", inactiveStyle)
			}

			if autoBorder {
				bFg := baseFg
				bBg := tabBg
				for _, p := range win.Panes {
					if isAuxiliaryPane(p) {
						continue
					}
					iBg := borderBg
					if iBg == "" {
						iBg = bBg
					}
					iFg := bFg
					if c.config.PaneHeader.DimInactive {
						opacity := c.config.PaneHeader.DimOpacity
						if opacity <= 0 || opacity > 1 {
							opacity = 0.6
						}
						tBg := c.config.PaneHeader.TerminalBg
						iFg = desaturateHex(iFg, opacity, tBg)
						iBg = desaturateHex(iBg, opacity, tBg)
					}
					inactiveStyle := buildBorderStyle(iFg, iBg)
					if inactiveStyle == "" {
						inactiveStyle = fmt.Sprintf("fg=%s,bg=%s", bFg, bBg)
					}
					args = append(args, ";", "set-option", "-p", "-t", p.ID,
						"pane-border-style", inactiveStyle)
				}
			}
		}
	}
	return args
}

// GetWindowsHash returns a hash of current window state for change detection.
// Uses the already-cached c.windows to avoid an extra tmux round-trip after RefreshWindows().
func (c *Coordinator) GetWindowsHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	// Simple hash: count + window IDs + active states + pane active states + indicators
	hash := fmt.Sprintf("%d", len(c.windows))
	for _, w := range c.windows {
		// Include window state and indicators
		hash += fmt.Sprintf(":%s:%v:%d:%v:%v:%v:%v:%v:%v:%s:%s:%v",
			w.ID, w.Active, len(w.Panes),
			w.Busy, w.Input, w.Bell, w.Activity, w.Silence,
			w.Collapsed, w.CustomColor, w.Group, w.Last)
		// Include which pane is active within each window
		for _, p := range w.Panes {
			if p.Active {
				hash += fmt.Sprintf(":p%d", p.Index)
				break
			}
		}
	}
	return hash
}

// RefreshGit updates git state
func (c *Coordinator) RefreshGit() {
	// Run all git commands WITHOUT holding stateMu. Holding stateMu during
	// network-bound git commands (e.g. @{upstream} fetch) can block for seconds,
	// causing git_tick to exceed its timeout and kill the daemon.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree").Output()
	isGitRepo := err == nil && strings.TrimSpace(string(out)) == "true"
	if !isGitRepo {
		c.stateMu.Lock()
		c.isGitRepo = false
		c.stateMu.Unlock()
		return
	}

	// Get branch
	var branch string
	if out, err = exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}

	// Get dirty count
	dirty := 0
	if out, err = exec.CommandContext(ctx, "git", "status", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) > 0 {
				dirty++
			}
		}
	}

	// Get ahead/behind
	var ahead, behind int
	if out, err = exec.CommandContext(ctx, "git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD").Output(); err == nil {
		parts := strings.Fields(string(out))
		if len(parts) == 2 {
			behind, _ = strconv.Atoi(parts[0])
			ahead, _ = strconv.Atoi(parts[1])
		}
	}

	// Store results under lock (minimal critical section)
	c.stateMu.Lock()
	c.isGitRepo = true
	if branch != "" {
		c.gitBranch = branch
	}
	c.gitDirty = dirty
	c.gitAhead = ahead
	c.gitBehind = behind
	c.stateMu.Unlock()
}

// RefreshSession updates session state
func (c *Coordinator) RefreshSession() {
	// Run all tmux commands WITHOUT holding stateMu to avoid blocking
	// the lock during potentially slow tmux calls.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sessionName string
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{session_name}").Output(); err == nil {
		sessionName = strings.TrimSpace(string(out))
	}

	// Need sessionName for the list-clients call; read current value if not refreshed
	if sessionName == "" {
		c.stateMu.RLock()
		sessionName = c.sessionName
		c.stateMu.RUnlock()
	}

	sessionClients := 0
	if out, err := exec.CommandContext(ctx, "tmux", "list-clients", "-t", sessionName).Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] != "" {
			sessionClients = len(lines)
		}
	}

	windowCount := 0
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{session_windows}").Output(); err == nil {
		windowCount, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}

	// Store results under lock (minimal critical section)
	c.stateMu.Lock()
	if sessionName != "" {
		c.sessionName = sessionName
	}
	c.sessionClients = sessionClients
	if windowCount > 0 {
		c.windowCount = windowCount
	}
	c.stateMu.Unlock()
}

// IncrementSpinner advances the spinner frame and returns true if any spinner is visible
func (c *Coordinator) IncrementSpinner() bool {
	c.stateMu.Lock()
	c.spinnerFrame++
	// Check if any pane has a visible spinner (AIBusy or AIInput)
	hasVisibleSpinner := false
	for _, win := range c.windows {
		if win.Busy || win.Bell || win.Activity {
			hasVisibleSpinner = true
			break
		}
		for _, pane := range win.Panes {
			if pane.AIBusy || pane.AIInput {
				hasVisibleSpinner = true
				break
			}
		}
		if hasVisibleSpinner {
			break
		}
	}
	c.stateMu.Unlock()
	return hasVisibleSpinner
}

// UpdatePetState updates the pet's state (called periodically)
// Returns true if pet is enabled and visually changed (needs render)
func (c *Coordinator) UpdatePetState() bool {
	c.stateMu.Lock()

	// If pet widget is disabled, nothing to update
	if !c.config.Widgets.Pet.Enabled {
		c.stateMu.Unlock()
		return false
	}

	// Track previous visual state to detect changes
	prevPos := c.pet.Pos
	prevState := c.pet.State
	prevYarnPos := c.pet.YarnPos
	prevFloatingCount := len(c.pet.FloatingItems)
	prevMousePos := c.pet.MousePos

	c.pet.AnimFrame++
	now := time.Now()
	width := c.lastWidth
	if width < 10 {
		width = 25
	}
	adventureEnabled := c.config.Widgets.Pet.AdventureEnabled
	// Account for emoji visual width (2 cols) - use safe play width
	maxX := width - 5 // Reduced from width-2 to match safePlayWidth calculation
	if maxX < 1 {
		maxX = 1
	}

	if c.pet.Adventure.Active && !adventureEnabled {
		c.pet.Adventure = adventureState{}
		if c.pet.State == "walking" || c.pet.State == "jumping" {
			c.pet.State = "idle"
		}
		c.pet.HasTarget = false
		c.pet.ActionPending = ""
		c.pet.LastThought = "back home."
	}

	// === ADVENTURE MODE ===
	// If adventure is active, update it and skip normal mechanics
	if c.pet.Adventure.Active {
		c.updateAdventurePhase(now, maxX)

		// Clean up expired floating items (also needed during adventure)
		var activeItems []floatingItem
		for _, item := range c.pet.FloatingItems {
			if now.Before(item.ExpiresAt) {
				activeItems = append(activeItems, item)
			}
		}
		c.pet.FloatingItems = activeItems

		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		// Adventure always triggers visual change
		return true
	}

	// === YARN EXPIRATION ===

	// Yarn disappears after expiration time
	if c.pet.YarnPos.X >= 0 && !c.pet.YarnExpiresAt.IsZero() && now.After(c.pet.YarnExpiresAt) {
		c.pet.YarnPos = pos2D{X: -1, Y: 0}
		c.pet.YarnExpiresAt = time.Time{}
		// If cat was chasing yarn, stop
		if c.pet.ActionPending == "play" {
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			c.pet.State = "idle"
			c.pet.LastThought = "where'd it go?"
		}
	}

	// === GRAVITY ===

	// Yarn gravity - falls if in air
	if c.pet.YarnPos.Y > 0 {
		c.pet.YarnPos.Y--
	}

	// Cat gravity - falls back to ground after jumping
	if c.pet.Pos.Y > 0 {
		c.pet.Pos.Y--
		if c.pet.Pos.Y == 0 && c.pet.State == "jumping" {
			c.pet.State = "idle"
		}
	}

	// Food gravity - falls if in air
	if c.pet.FoodItem.X >= 0 && c.pet.FoodItem.Y > 0 {
		c.pet.FoodItem.Y--
		// When food lands, pet should chase it
		if c.pet.FoodItem.Y == 0 && !c.pet.HasTarget {
			c.pet.TargetPos = pos2D{X: c.pet.FoodItem.X, Y: 0}
			c.pet.HasTarget = true
			c.pet.ActionPending = "eat"
			c.pet.State = "walking"
			c.pet.LastThought = "food!"
		}
	}

	// === POOP MECHANICS ===

	// Check if pet needs to poop
	if !c.pet.NeedsPoopAt.IsZero() && now.After(c.pet.NeedsPoopAt) {
		poopX := c.pet.Pos.X
		c.pet.PoopPositions = append(c.pet.PoopPositions, poopX)
		c.pet.LastPoop = now
		c.pet.NeedsPoopAt = time.Time{}
		c.pet.LastThought = randomThought("poop")
		// Move away from poop after placing it
		if c.pet.Pos.X > maxX/2 {
			c.pet.TargetPos = pos2D{X: c.pet.Pos.X - 3, Y: 0}
		} else {
			c.pet.TargetPos = pos2D{X: c.pet.Pos.X + 3, Y: 0}
		}
		c.pet.HasTarget = true
		c.pet.State = "walking"
	}

	// === POSITION CLAMPING ===

	if c.pet.Pos.X > maxX {
		c.pet.Pos.X = maxX
	}
	if c.pet.Pos.X < 0 {
		c.pet.Pos.X = 0
	}
	if c.pet.TargetPos.X > maxX {
		c.pet.TargetPos.X = maxX
	}
	if c.pet.TargetPos.X < 0 {
		c.pet.TargetPos.X = 0
	}

	// === TARGET MOVEMENT ===

	if c.pet.HasTarget {
		// Move pet toward target X
		nextX := c.pet.Pos.X
		if c.pet.Pos.X < c.pet.TargetPos.X {
			nextX++
			c.pet.Direction = 1
		} else if c.pet.Pos.X > c.pet.TargetPos.X {
			nextX--
			c.pet.Direction = -1
		}

		// Check if next position has poop - if so, jump over it!
		isPoopAhead := false
		for _, poopX := range c.pet.PoopPositions {
			if poopX == nextX || poopX == nextX+1 || poopX == nextX-1 {
				isPoopAhead = true
				break
			}
		}
		if isPoopAhead && c.pet.Pos.Y == 0 {
			// Jump over the poop!
			c.pet.Pos.Y = 2
			c.pet.State = "jumping"
			c.pet.LastThought = randomThought("poop_jump")
		}

		c.pet.Pos.X = nextX

		// Clamp after move
		if c.pet.Pos.X > maxX {
			c.pet.Pos.X = maxX
		}
		if c.pet.Pos.X < 0 {
			c.pet.Pos.X = 0
		}

		// If chasing yarn, push it or catch it when reached
		if c.pet.ActionPending == "play" {
			yarnX := c.pet.YarnPos.X
			if yarnX < 0 {
				yarnX = width - 4
			}
			// Pet reached yarn
			if c.pet.Pos.X == yarnX || c.pet.Pos.X == yarnX-1 || c.pet.Pos.X == yarnX+1 {
				// After 2 pushes, catch the yarn
				if c.pet.YarnPushCount >= 2 {
					// Catch the yarn - don't push, let the target be reached
					c.pet.TargetPos = c.pet.Pos // Target reached
				} else {
					// Push the yarn
					newYarnX := yarnX + c.pet.Direction*2
					if newYarnX >= 2 && newYarnX < width-2 {
						c.pet.YarnPos.X = newYarnX
						c.pet.YarnPos.Y = 1 // Bounce up
						c.pet.TargetPos.X = newYarnX
						c.pet.YarnPushCount++
					} else {
						// Can't push further, catch it
						c.pet.TargetPos = c.pet.Pos
					}
				}
			}
		}

		// Check if reached target
		if c.pet.Pos.X == c.pet.TargetPos.X && c.pet.Pos.Y == c.pet.TargetPos.Y {
			c.pet.HasTarget = false
			switch c.pet.ActionPending {
			case "eat":
				c.pet.Hunger = 100
				c.pet.State = "eating"
				c.pet.LastFed = now
				c.pet.TotalFeedings++
				c.pet.LastThought = "nom nom nom"
				c.pet.FoodItem = pos2D{X: -1, Y: -1}
				// Schedule potential poop based on config chance (default 50%)
				poopChance := c.config.Widgets.Pet.PoopChance
				if poopChance <= 0 {
					poopChance = 50
				}
				if rand.Intn(100) < poopChance {
					c.pet.NeedsPoopAt = now.Add(time.Duration(3+rand.Intn(5)) * time.Second)
				}
			case "play":
				c.pet.State = "playing"
				if c.pet.Happiness < 100 {
					c.pet.Happiness += 5
					if c.pet.Happiness > 100 {
						c.pet.Happiness = 100
					}
				}
				c.pet.TotalYarnPlays++
				c.pet.LastThought = "got it!"
				// Yarn disappears when caught
				c.pet.YarnPos = pos2D{X: -1, Y: 0}
				c.pet.YarnExpiresAt = time.Time{}
				c.pet.YarnPushCount = 0
			default:
				c.pet.State = "idle"
			}
			c.pet.ActionPending = ""
		}
	} else if c.pet.State == "eating" || c.pet.State == "playing" || c.pet.State == "happy" || c.pet.State == "shooting" {
		// Return to idle after a few frames
		if c.pet.AnimFrame%20 == 0 {
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("idle")
		}
	} else if c.pet.State == "sleeping" {
		// Wake up after longer duration (~5-10 seconds at 10fps = 50-100 frames)
		if c.pet.AnimFrame%60 == 0 && rand.Intn(100) < 30 {
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("wakeup")
		}
	}

	// === FLOATING ITEMS ===

	var activeItems []floatingItem
	for _, item := range c.pet.FloatingItems {
		if now.Before(item.ExpiresAt) {
			item.Pos.X += item.Velocity.X
			item.Pos.Y += item.Velocity.Y
			// Keep in bounds
			if item.Pos.X >= 0 && item.Pos.X < width && item.Pos.Y >= 0 && item.Pos.Y <= 2 {
				activeItems = append(activeItems, item)
			}
		}
	}
	c.pet.FloatingItems = activeItems

	// === RANDOM BEHAVIORS (cat mood) ===

	if c.pet.State == "idle" && !c.pet.HasTarget && c.pet.AnimFrame%10 == 0 {
		// Time-based sleeping: cats sleep more at night (2am-6am has 80% sleep chance)
		hour := now.Hour()
		if hour >= 2 && hour < 6 && rand.Intn(100) < 80 {
			c.pet.State = "sleeping"
			c.pet.LastThought = randomThought("sleepy")
		} else {
			// Configurable chance to do something every 10 frames (default: 15%)
			actionChance := c.config.Widgets.Pet.ActionChance
			if actionChance <= 0 {
				actionChance = 15 // Default: less hyper than before
			}
			if rand.Intn(100) < actionChance {
				action := rand.Intn(10)
				switch action {
				case 0:
					// Run across the screen (avoid poop as destination)
					c.pet.State = "walking"
					c.pet.Direction = []int{-1, 1}[rand.Intn(2)]
					targetX := rand.Intn(maxX)
					// Avoid selecting a position with poop as target
					for attempts := 0; attempts < 5; attempts++ {
						hasPoop := false
						for _, poopX := range c.pet.PoopPositions {
							if abs(targetX-poopX) <= 1 {
								hasPoop = true
								break
							}
						}
						if !hasPoop {
							break
						}
						targetX = rand.Intn(maxX) // Try another position
					}
					c.pet.TargetPos = pos2D{X: targetX, Y: 0}
					c.pet.HasTarget = true
					c.pet.LastThought = randomThought("walking")
				case 1:
					// Jump in place
					c.pet.State = "jumping"
					c.pet.Pos.Y = 2
					c.pet.LastThought = randomThought("jumping")
				case 2:
					// Chase the yarn
					if c.pet.YarnPos.X >= 0 {
						c.pet.TargetPos = pos2D{X: c.pet.YarnPos.X, Y: 0}
						c.pet.HasTarget = true
						c.pet.ActionPending = "play"
						c.pet.State = "walking"
						c.pet.LastThought = "yarn calls to me."
					}
				case 3:
					// Bat at yarn (toss it) - avoid poop positions
					tossX := safeRandRange(2, maxX)
					for attempts := 0; attempts < 5; attempts++ {
						hasPoop := false
						for _, poopX := range c.pet.PoopPositions {
							if abs(tossX-poopX) <= 1 {
								hasPoop = true
								break
							}
						}
						if !hasPoop {
							break
						}
						tossX = safeRandRange(2, maxX)
					}
					c.pet.YarnPos = pos2D{X: tossX, Y: 2}
					c.pet.YarnExpiresAt = now.Add(15 * time.Second)
					c.pet.YarnPushCount = 0
					c.pet.TargetPos = pos2D{X: tossX, Y: 0}
					c.pet.HasTarget = true
					c.pet.ActionPending = "play"
					c.pet.State = "walking"
					c.pet.LastThought = "chaos time."
				case 4:
					// Just be happy
					c.pet.State = "happy"
					c.pet.LastThought = randomThought("happy")
				case 5:
					// SHOOT A BANANA!
					c.pet.State = "shooting"
					dir := c.pet.Direction
					if dir == 0 {
						dir = 1
					}
					// Gun appears in the direction the pet is facing (fixes #23: physics make sense now)
					gunX := c.pet.Pos.X + dir
					if gunX < 0 {
						gunX = 0
					}
					if gunX > maxX {
						gunX = maxX
					}
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "🔫",
						Pos:       pos2D{X: gunX, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(1200 * time.Millisecond),
					})
					// BANG effect next to gun
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "💥",
						Pos:       pos2D{X: gunX + dir, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(400 * time.Millisecond),
					})
					// Banana flies from gun position
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     "🍌",
						Pos:       pos2D{X: gunX + dir, Y: 1},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(3 * time.Second),
					})
					thoughts := []string{"pew pew.", "banana had it coming.", "nothing personal.", "the family sends regards."}
					c.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				case 6:
					// Toss random emoji with context-aware thoughts
					shinyThings := []struct {
						emoji    string
						thoughts []string
					}{
						{"⭐", []string{"a star!", "make a wish.", "star light, star bright."}},
						{"💫", []string{"dizzy.", "sparkly.", "ooh cosmic."}},
						{"✨", []string{"sparkles!", "so shiny.", "glitter everywhere."}},
						{"🎾", []string{"ball!", "must chase.", "tennis anyone?"}},
						{"🏀", []string{"bouncy.", "slam dunk.", "ball is life."}},
						{"🎈", []string{"balloon!", "pop it?", "don't let it fly away."}},
						{"🦋", []string{"butterfly!", "must catch.", "so graceful."}},
						{"🐟", []string{"fish!", "dinner?", "swimming in air."}},
						{"🍎", []string{"apple!", "healthy snack.", "one a day."}},
						{"🧀", []string{"cheese!", "yes please.", "gouda choice."}},
					}
					choice := shinyThings[rand.Intn(len(shinyThings))]
					startX := safeRandRange(2, maxX)
					dir := []int{-1, 1}[rand.Intn(2)]
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     choice.emoji,
						Pos:       pos2D{X: startX, Y: 2},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(3 * time.Second),
					})
					c.pet.LastThought = choice.thoughts[rand.Intn(len(choice.thoughts))]
				case 7:
					// Menacing stare
					emojis := []string{"👁️", "🔪", "💀", "🎯"}
					emoji := emojis[rand.Intn(len(emojis))]
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     emoji,
						Pos:       pos2D{X: c.pet.Pos.X, Y: 2},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(2 * time.Second),
					})
					thoughts := []string{"watching.", "always watching.", "i see you.", "the family knows."}
					c.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				case 8:
					// Spawn a mouse! (if not already present)
					if c.pet.MousePos.X < 0 {
						// Mouse appears at edge of screen
						c.pet.MouseDirection = []int{-1, 1}[rand.Intn(2)]
						if c.pet.MouseDirection == 1 {
							c.pet.MousePos = pos2D{X: 0, Y: 0}
						} else {
							c.pet.MousePos = pos2D{X: maxX, Y: 0}
						}
						c.pet.LastThought = randomThought("mouse_spot")
					}
				case 9:
					// Start an adventure! (if enabled and happy enough)
					if adventureEnabled && c.pet.Happiness >= 50 && !c.pet.Adventure.Active {
						c.startAdventure(maxX)
					}
				}
			}
		}
	}

	// === MOUSE MECHANICS ===

	// Check if it's time to spawn a mouse
	if c.pet.MousePos.X < 0 && !c.pet.MouseAppearsAt.IsZero() && now.After(c.pet.MouseAppearsAt) {
		// Mouse appears at edge of screen
		c.pet.MouseDirection = []int{-1, 1}[rand.Intn(2)]
		if c.pet.MouseDirection == 1 {
			c.pet.MousePos = pos2D{X: 0, Y: 0}
		} else {
			c.pet.MousePos = pos2D{X: maxX, Y: 0}
		}
		c.pet.MouseAppearsAt = time.Time{} // Clear timer
		c.pet.LastThought = randomThought("mouse_spot")
	}

	// If no mouse and no timer set, schedule one (30-90 seconds)
	if c.pet.MousePos.X < 0 && c.pet.MouseAppearsAt.IsZero() {
		c.pet.MouseAppearsAt = now.Add(time.Duration(30+rand.Intn(60)) * time.Second)
	}

	// If there's a mouse, handle mouse behavior
	if c.pet.MousePos.X >= 0 {
		// Mouse runs away from pet
		dist := c.pet.MousePos.X - c.pet.Pos.X
		if dist < 0 {
			dist = -dist
		}

		// If pet catches mouse (within 2 cells), celebrate and remove mouse
		if dist <= 2 && c.pet.Pos.Y == 0 {
			c.pet.MousePos = pos2D{X: -1, Y: 0}
			c.pet.TotalMouseCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+20)
			c.pet.State = "happy"
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			// Creative kill thought!
			c.pet.LastThought = randomThought("mouse_kill")
		} else {
			// Mouse moves away from pet (every 5 frames)
			if c.pet.AnimFrame%5 == 0 {
				// Mouse tries to run away from pet
				if c.pet.MousePos.X < c.pet.Pos.X {
					c.pet.MouseDirection = -1 // Run left
				} else {
					c.pet.MouseDirection = 1 // Run right
				}
				c.pet.MousePos.X += c.pet.MouseDirection

				// If mouse reaches edge, it escapes
				if c.pet.MousePos.X < 0 || c.pet.MousePos.X > maxX {
					c.pet.MousePos = pos2D{X: -1, Y: 0}
					c.pet.LastThought = "it got away..."
					c.pet.HasTarget = false
					c.pet.ActionPending = ""
				}
			}

			// Pet chases mouse (if not already doing something else important)
			if !c.pet.HasTarget && c.pet.MousePos.X >= 0 {
				c.pet.TargetPos = pos2D{X: c.pet.MousePos.X, Y: 0}
				c.pet.HasTarget = true
				c.pet.ActionPending = "hunt"
				c.pet.State = "walking"
				if c.pet.AnimFrame%20 == 0 {
					c.pet.LastThought = randomThought("mouse_chase")
				}
			}
		}
	}

	// === HUNGER/HAPPINESS DECAY ===
	// Only decay when at least one renderer is connected
	c.clientWidthsMu.RLock()
	hasConnectedClients := len(c.clientWidths) > 0
	c.clientWidthsMu.RUnlock()

	if hasConnectedClients {
		// Use config for hunger decay rate (frames = seconds * 10 since ~10fps)
		hungerDecayFrames := c.config.Widgets.Pet.HungerDecay * 10
		if hungerDecayFrames <= 0 {
			hungerDecayFrames = 17280 // Default: ~2 days to starve (1728 sec/tick)
		}
		if c.pet.Hunger > 0 && c.pet.AnimFrame%hungerDecayFrames == 0 {
			c.pet.Hunger--
		}
		// Happiness decays 1.5x faster when hungry
		happyDecayFrames := hungerDecayFrames * 2 / 3
		if happyDecayFrames <= 0 {
			happyDecayFrames = 11520 // Default: proportional to hunger decay
		}
		if c.pet.Hunger < 30 && c.pet.Happiness > 0 && c.pet.AnimFrame%happyDecayFrames == 0 {
			c.pet.Happiness--
		}
	}

	// === DEATH / STARVATION MECHANICS ===

	// If already dead, just occasionally update thoughts and skip other state changes
	if c.pet.IsDead {
		if c.pet.AnimFrame%100 == 0 {
			c.pet.LastThought = randomThought("dead")
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Dead pet doesn't animate
	}

	// Track starvation time
	if c.pet.Hunger == 0 {
		if c.pet.StarvingStart.IsZero() {
			c.pet.StarvingStart = now
			c.pet.LastThought = randomThought("starving")
		}

		// After 60 seconds of starvation
		starvingDuration := now.Sub(c.pet.StarvingStart)
		if starvingDuration > 60*time.Second {
			if c.config.Widgets.Pet.CanDie {
				// Pet dies
				c.pet.IsDead = true
				c.pet.DeathTime = now
				c.pet.State = "dead"
				c.pet.LastThought = "goodbye..."
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return true // State changed to dead
			} else {
				// Guilt trip mode - passive aggressive thoughts every 10 seconds
				if c.pet.AnimFrame%100 == 0 {
					c.pet.LastThought = randomThought("guilt")
				}
			}
		}
	} else {
		// Reset starvation tracking when fed
		if !c.pet.StarvingStart.IsZero() {
			c.pet.StarvingStart = time.Time{}
		}
	}

	// === LLM THOUGHT GENERATION ===

	// If LLM thoughts are enabled and pet is idle, occasionally get new thoughts
	if c.config.Widgets.Pet.Thoughts && c.pet.State == "idle" && !c.pet.IsDead {
		// Use configured interval or default to 30 seconds
		thoughtInterval := c.config.Widgets.Pet.ThoughtInterval
		if thoughtInterval <= 0 {
			thoughtInterval = 30
		}
		thoughtFrames := thoughtInterval * 10 // Convert seconds to frames (~10fps)
		if c.pet.AnimFrame%thoughtFrames == 0 {
			petName := c.config.Widgets.Pet.Name
			if petName == "" {
				petName = "Whiskers"
			}
			// Try to get an LLM thought (non-blocking, from buffer or triggers generation)
			if thought := generateLLMThought(&c.pet, petName); thought != "" {
				c.pet.LastThought = thought
				c.pet.ThoughtScroll = 0
				// Parse thought for action keywords and trigger matching behavior
				c.triggerActionFromThought(thought, maxX)
			}
		}
	}

	// === THOUGHT MARQUEE ===

	// Use config for thought scroll speed (default: 3 frames per scroll step)
	thoughtSpeed := c.config.Widgets.Pet.ThoughtSpeed
	if thoughtSpeed <= 0 {
		thoughtSpeed = 3
	}
	if c.pet.AnimFrame%thoughtSpeed == 0 {
		thoughtWidth := uniseg.StringWidth(c.pet.LastThought)
		maxThoughtWidth := width - 4
		if thoughtWidth > maxThoughtWidth {
			c.pet.ThoughtScroll++
			if c.pet.ThoughtScroll > thoughtWidth+3 {
				c.pet.ThoughtScroll = 0
			}
		} else {
			c.pet.ThoughtScroll = 0
		}
	}

	petSnap := c.pet
	// Return true if any visual state changed
	changed := c.pet.Pos != prevPos ||
		c.pet.State != prevState ||
		c.pet.YarnPos != prevYarnPos ||
		len(c.pet.FloatingItems) != prevFloatingCount ||
		c.pet.MousePos != prevMousePos
	c.stateMu.Unlock()
	savePetStateData(petSnap)
	return changed
}

// startAdventure initiates a new adventure sequence
func (c *Coordinator) startAdventure(maxX int) {
	// Pick a random biome
	biomes := []string{"forest", "meadow", "garden", "backyard"}
	biome := biomes[rand.Intn(len(biomes))]

	c.pet.Adventure = adventureState{
		Active:        true,
		Phase:         advPhaseDeparting,
		PhaseStart:    time.Now(),
		PhaseDuration: time.Duration(2+rand.Intn(2)) * time.Second,
		Biome:         biome,
		SceneOffset:   0,
		CatX:          c.pet.Pos.X,
		LastThought:   "adventure calls...",
	}
	c.pet.State = "walking"
	c.pet.Direction = 1 // Walking right (departing)
	c.pet.LastThought = "adventure calls..."
}

// updateAdventurePhase handles adventure state transitions and mechanics
func (c *Coordinator) updateAdventurePhase(now time.Time, maxX int) {
	adv := &c.pet.Adventure
	elapsed := now.Sub(adv.PhaseStart)

	switch adv.Phase {
	case advPhaseDeparting:
		if c.pet.AnimFrame%3 == 0 {
			adv.CatX++
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
		}
		if elapsed >= adv.PhaseDuration {
			// Transition to exploring
			adv.Phase = advPhaseExploring
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(5+rand.Intn(10)) * time.Second
			adv.CatX = maxX / 2 // Cat centered during exploration
			c.pet.LastThought = "exploring..."
		}

	case advPhaseExploring:
		// Scenery scrolls past, cat stays centered
		if c.pet.AnimFrame%2 == 0 {
			adv.SceneOffset++
		}

		// Random chance to encounter wildlife
		if adv.Wildlife == nil && rand.Intn(100) < 3 {
			c.spawnWildlife(maxX)
		}

		// Check for transition to encounter or returning
		if adv.Wildlife != nil {
			adv.Phase = advPhaseEncounter
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(5+rand.Intn(5)) * time.Second
		} else if elapsed >= adv.PhaseDuration {
			// No encounter, start returning
			adv.Phase = advPhaseReturning
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(3+rand.Intn(3)) * time.Second
			c.pet.Direction = -1
			c.pet.LastThought = "heading home..."
		}

	case advPhaseEncounter:
		c.updateEncounter(now, maxX)

		// Check if encounter is resolved
		if adv.Wildlife != nil && (adv.Wildlife.Caught || adv.Wildlife.Escaped) {
			// Brief pause then return
			if elapsed >= adv.PhaseDuration {
				adv.Phase = advPhaseReturning
				adv.PhaseStart = now
				adv.PhaseDuration = time.Duration(3+rand.Intn(3)) * time.Second
				adv.Wildlife = nil
				c.pet.Direction = -1
				c.pet.LastThought = "heading home..."
			}
		}

	case advPhaseReturning:
		// Scenery scrolls back
		if c.pet.AnimFrame%2 == 0 && adv.SceneOffset > 0 {
			adv.SceneOffset--
		}

		if elapsed >= adv.PhaseDuration || adv.SceneOffset <= 0 {
			adv.Phase = advPhaseArriving
			adv.PhaseStart = now
			adv.PhaseDuration = time.Duration(1+rand.Intn(2)) * time.Second
			adv.CatX = maxX
		}

	case advPhaseArriving:
		// Cat walks back to normal position
		if c.pet.AnimFrame%3 == 0 && adv.CatX > c.pet.Pos.X {
			adv.CatX--
		}

		if elapsed >= adv.PhaseDuration || adv.CatX <= c.pet.Pos.X {
			// Adventure complete!
			c.pet.Adventure = adventureState{
				TotalCatches: adv.TotalCatches,
			}
			c.pet.State = "happy"
			c.pet.LastThought = "good adventure."
		}
	}
}

// spawnWildlife creates a wildlife encounter based on current biome
func (c *Coordinator) spawnWildlife(maxX int) {
	adv := &c.pet.Adventure
	biome := adventureBiomes[adv.Biome]
	if len(biome.Wildlife) == 0 {
		return
	}

	// Pick random wildlife from biome
	wildlifeType := biome.Wildlife[rand.Intn(len(biome.Wildlife))]
	data := adventureWildlife[wildlifeType]

	adv.Wildlife = &wildlifeEncounter{
		Type:        wildlifeType,
		Emoji:       data.Emoji,
		X:           maxX,
		Y:           data.YLevel,
		Speed:       data.Speed,
		CatchChance: data.CatchChance,
	}

	// Get spot thought
	c.pet.LastThought = c.getAdventureThought(wildlifeType, "spot")
}

// updateEncounter handles the wildlife encounter mechanics
func (c *Coordinator) updateEncounter(now time.Time, maxX int) {
	adv := &c.pet.Adventure
	w := adv.Wildlife
	if w == nil {
		return
	}

	if w.Pounced {
		if w.PounceFrames > 0 {
			adv.CatX = w.X
			if adv.CatX < 0 {
				adv.CatX = 0
			}
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
			c.pet.State = "jumping"
			c.pet.Pos.Y = w.Y
			w.PounceFrames--
			return
		}

		if w.WillCatch {
			w.Caught = true
			adv.TotalCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+10)
			c.pet.Hunger = min(100, c.pet.Hunger+20)
			thought := c.getAdventureThought(w.Type, "catch")
			if thought == "" {
				thought = fmt.Sprintf("caught a %s!", w.Type)
			}
			c.pet.LastThought = thought
			c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
				Emoji:     "🏆",
				Pos:       pos2D{X: adv.CatX, Y: 2},
				Velocity:  pos2D{X: 0, Y: 0},
				ExpiresAt: now.Add(2 * time.Second),
			})
			c.spawnAdventureCatchFX(now, adv.CatX, w.Y)
			if c.config.Widgets.Pet.AdventureBlood {
				blood := "🩸"
				if c.config.Widgets.Pet.Icons.Blood != "" {
					blood = c.config.Widgets.Pet.Icons.Blood
				}
				if blood != "" {
					c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
						Emoji:     blood,
						Pos:       pos2D{X: adv.CatX, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(1200 * time.Millisecond),
					})
				}
			}
		} else {
			w.Escaped = true
			thought := c.getAdventureThought(w.Type, "escape")
			if thought == "" {
				thought = fmt.Sprintf("the %s got away!", w.Type)
			}
			c.pet.LastThought = thought
		}

		c.pet.Pos.Y = 0
		c.pet.State = "idle"
		return
	}

	// Phase 1: Spotting (wildlife enters view)
	if !w.Spotted {
		if c.pet.AnimFrame%3 == 0 {
			w.X--
		}
		// Wildlife is spotted when it enters play area
		if w.X < maxX-2 {
			w.Spotted = true
			c.pet.State = "idle" // Cat freezes
			c.pet.LastThought = fmt.Sprintf("a %s!", w.Type)
			vibe := c.getAdventureVibe()
			switch vibe {
			case "subtle", "noir":
				w.Approach = 0
			case "anime":
				w.Approach = []int{2, 1, 2}[rand.Intn(3)]
			case "pixel":
				w.Approach = []int{1, 0, 1}[rand.Intn(3)]
			default:
				w.Approach = rand.Intn(3)
			}
			if w.Speed >= 3 && w.Approach < 1 {
				w.Approach = 1
			}
			if w.Speed >= 3 && w.Type == "bird" {
				w.Approach = 2
			}
			// Add "!" floating item above cat's actual position
			c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
				Emoji:     "❗",
				Pos:       pos2D{X: adv.CatX, Y: 2},
				Velocity:  pos2D{X: 0, Y: 0},
				ExpiresAt: now.Add(1500 * time.Millisecond),
			})
		}
		return
	}

	// Phase 2: Stalking (cat approaches)
	if !w.Stalking && w.Spotted && !w.Pounced {
		w.Stalking = true
		c.pet.State = "walking"
		c.pet.LastThought = c.getAdventureThought(w.Type, "stalk")
	}

	if w.Stalking && !w.Pounced {
		stepEvery := 5
		stopDist := 2
		pounceDist := 3
		if w.Approach == 1 {
			stepEvery = 3
			stopDist = 1
			pounceDist = 2
		} else if w.Approach == 2 {
			stepEvery = 2
			stopDist = 0
			pounceDist = 1
		}
		if c.pet.AnimFrame%stepEvery == 0 {
			if adv.CatX < w.X-stopDist {
				adv.CatX++
			}
		}

		moveEvery := 7
		if w.Speed >= 3 {
			moveEvery = 3
		} else if w.Speed == 2 {
			moveEvery = 5
		}
		if c.pet.AnimFrame%moveEvery == 0 {
			if adv.CatX <= w.X {
				w.X++
			} else {
				w.X--
			}
			w.X += rand.Intn(3) - 1
			if w.X < 3 {
				w.X = 3
			}
			if w.X > maxX+5 {
				w.X = maxX
			}
		}

		// Check if close enough to pounce
		dist := w.X - adv.CatX
		if dist < 0 {
			dist = -dist
		}
		if dist <= pounceDist {
			w.Pounced = true
			w.PounceFrames = 4
			w.WillCatch = rand.Intn(100) < w.CatchChance
			adv.CatX = w.X
			if adv.CatX < 0 {
				adv.CatX = 0
			}
			if adv.CatX > maxX {
				adv.CatX = maxX
			}
			c.pet.State = "jumping"
			c.pet.Pos.Y = w.Y
			return
		}
	}
}

func (c *Coordinator) getAdventureVibe() string {
	v := strings.ToLower(strings.TrimSpace(c.config.Widgets.Pet.AdventureVibe))
	if v == "" {
		return "ridiculous"
	}
	return v
}

func (c *Coordinator) spawnAdventureCatchFX(now time.Time, contactX int, preyY int) {
	vibe := c.getAdventureVibe()
	add := func(emoji string, x, y int, vx, vy int, d time.Duration) {
		if emoji == "" {
			return
		}
		c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
			Emoji:     emoji,
			Pos:       pos2D{X: x, Y: y},
			Velocity:  pos2D{X: vx, Y: vy},
			ExpiresAt: now.Add(d),
		})
	}

	confetti := []string{"🎉", "✨", "💥", "⭐"}
	comic := []string{"💥", "⚡", "✨"}
	pixel := []string{"*", "+", "x"}

	switch vibe {
	case "subtle":
		return
	case "noir":
		add("✦", contactX, 2, 0, 0, 1200*time.Millisecond)
		add("·", contactX-1, 1, -1, 0, 900*time.Millisecond)
		add("·", contactX+1, 1, 1, 0, 900*time.Millisecond)
	case "pixel":
		for i := 0; i < 3; i++ {
			em := pixel[rand.Intn(len(pixel))]
			dx := []int{-1, 0, 1}[rand.Intn(3)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			add(em, contactX+dx, dy, dx, 0, 900*time.Millisecond)
		}
	case "anime":
		for i := 0; i < 4; i++ {
			em := comic[rand.Intn(len(comic))]
			dx := []int{-1, 1}[rand.Intn(2)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			add(em, contactX, dy, dx, 0, 700*time.Millisecond)
		}
		add("‼", contactX, 2, 0, 0, 600*time.Millisecond)
	default:
		for i := 0; i < 5; i++ {
			em := confetti[rand.Intn(len(confetti))]
			dx := []int{-2, -1, 0, 1, 2}[rand.Intn(5)]
			dy := []int{0, 1, 2}[rand.Intn(3)]
			vx := []int{-1, 0, 1}[rand.Intn(3)]
			add(em, contactX+dx, dy, vx, 0, 1200*time.Millisecond)
		}
		add("😹", contactX-1, 2, -1, 0, 900*time.Millisecond)
		add("🍖", contactX+1, 1, 1, 0, 900*time.Millisecond)
	}

	_ = preyY
}

// getAdventureThought returns a random thought for the given wildlife and phase
func (c *Coordinator) getAdventureThought(wildlife, phase string) string {
	if thoughts, ok := adventureThoughts[wildlife]; ok {
		if phaseThoughts, ok := thoughts[phase]; ok && len(phaseThoughts) > 0 {
			return phaseThoughts[rand.Intn(len(phaseThoughts))]
		}
	}
	return ""
}

// renderAdventurePlayArea renders the play area during an adventure
func (c *Coordinator) renderAdventurePlayArea(safePlayWidth int, petSprite string, sprites petSprites) (highAir, lowAir, ground string) {
	adv := &c.pet.Adventure
	biome := adventureBiomes[adv.Biome]

	// Get biome ground character
	groundChar := biome.Ground
	if groundChar == "" {
		groundChar = "·"
	}

	// Build sprite maps for each row
	highAirSprites := make(map[int]string)
	lowAirSprites := make(map[int]string)
	groundSprites := make(map[int]string)

	// Deterministic scenery placement based on scene offset
	// Place scenery elements at fixed intervals, offset by scroll position
	for i := 0; i < safePlayWidth; i++ {
		worldX := i + adv.SceneOffset
		// Ground scenery every 7 columns
		if worldX%7 == 0 && len(biome.Scenery) > 0 {
			idx := (worldX / 7) % len(biome.Scenery)
			emoji := biome.Scenery[idx]
			// Only place on ground if not a flying creature
			if emoji != "🦋" {
				groundSprites[i] = emoji
			}
		}
		// Air scenery every 11 columns (less frequent)
		if worldX%11 == 0 && len(biome.Scenery) > 0 {
			idx := (worldX / 11) % len(biome.Scenery)
			emoji := biome.Scenery[idx]
			// Butterflies and birds in air
			if emoji == "🦋" || emoji == "🐦" {
				lowAirSprites[i] = emoji
			}
		}
	}

	// Place wildlife if present
	if adv.Wildlife != nil && !adv.Wildlife.Escaped && !adv.Wildlife.Caught {
		w := adv.Wildlife
		wx := w.X
		if wx >= 0 && wx < safePlayWidth {
			switch w.Y {
			case 2:
				highAirSprites[wx] = w.Emoji
			case 1:
				lowAirSprites[wx] = w.Emoji
			default:
				groundSprites[wx] = w.Emoji
			}
		}
	}

	catX := adv.CatX
	if catX >= 0 && catX < safePlayWidth {
		if c.pet.Pos.Y >= 2 {
			highAirSprites[catX] = petSprite
		} else if c.pet.Pos.Y == 1 {
			lowAirSprites[catX] = petSprite
		} else {
			groundSprites[catX] = petSprite
		}
	}

	// Place floating items (like "!" for spotting)
	for _, item := range c.pet.FloatingItems {
		if item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			switch item.Pos.Y {
			case 2:
				highAirSprites[item.Pos.X] = item.Emoji
			case 1:
				lowAirSprites[item.Pos.X] = item.Emoji
			default:
				groundSprites[item.Pos.X] = item.Emoji
			}
		}
	}

	// Build the rows
	highAir = buildAirRow(highAirSprites, safePlayWidth)
	lowAir = buildAirRow(lowAirSprites, safePlayWidth)
	ground = buildSpriteRow(groundSprites, groundChar, safePlayWidth)

	return highAir, lowAir, ground
}

// handleWidthSync checks if the current width matches global state and syncs if needed
func (c *Coordinator) handleWidthSync(clientID string, currentWidth int) {
	if strings.HasPrefix(clientID, "header:") {
		return
	}
	if c.sidebarCollapsed {
		return
	}

	// Query tmux BEFORE acquiring any lock to prevent deadlock if tmux hangs
	// Use a timeout context to prevent blocking forever
	activeWindowID := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{window_id}").Output(); err == nil {
		activeWindowID = strings.TrimSpace(string(out))
	}
	isActive := (clientID == activeWindowID)

	// Read window sync setting under stateMu BEFORE acquiring widthSyncMu.
	// Lock ordering must always be: stateMu -> widthSyncMu. Acquiring widthSyncMu
	// first and then stateMu.RLock() inside it causes a deadlock with RefreshGit,
	// which holds stateMu.Lock() while git commands run, and then calls
	// BroadcastRender -> handleWidthSync -> widthSyncMu.
	syncWidth := false
	c.stateMu.RLock()
	for i := range c.windows {
		if c.windows[i].ID == clientID {
			syncWidth = c.windows[i].SyncWidth
			break
		}
	}
	c.stateMu.RUnlock()

	trackLock("widthSyncMu", "handleWidthSync")
	c.widthSyncMu.Lock()

	// Detect if this window just became active
	justBecameActive := isActive && c.lastActiveWindowID != clientID
	if justBecameActive {
		coordinatorDebugLog.Printf("Width sync: active window changed to %s", clientID)
	}

	// Debounce: ignore resize events within 500ms of our last sync
	// to avoid cascading syncs when we resize multiple panes
	sinceLast := time.Since(c.lastWidthSync)
	if sinceLast < 500*time.Millisecond {
		// Still update the active window tracker even if debounced
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		untrackLock("widthSyncMu")
		c.widthSyncMu.Unlock()
		return
	}

	if !syncWidth {
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		untrackLock("widthSyncMu")
		c.widthSyncMu.Unlock()
		return
	}

	if c.globalWidth == 0 {
		c.globalWidth = currentWidth
	}

	// If the active window's sidebar was resized by the user, adopt as new global width.
	// Only reject widths below the absolute minimum (broken state).
	if currentWidth < 10 {
		coordinatorDebugLog.Printf("Width sync: %s below minimum (%d), restoring to global %d", clientID, currentWidth, c.globalWidth)
		c.lastWidthSync = time.Now()
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		targetWidth := c.globalWidth
		if targetWidth < 10 {
			targetWidth = 25
		}
		untrackLock("widthSyncMu")
		c.widthSyncMu.Unlock()

		listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer listCancel()
		if out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", clientID, "-F", "#{pane_id} #{pane_current_command}").Output(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.Split(line, " ")
				if len(parts) >= 2 && strings.HasPrefix(parts[1], "sidebar") {
					resizeCtx, resizeCancel := context.WithTimeout(context.Background(), 2*time.Second)
					exec.CommandContext(resizeCtx, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", targetWidth)).Run()
					resizeCancel()
					break
				}
			}
		}
		return
	}

	// User manually resized the active window's sidebar — adopt as new global width
	if isActive && currentWidth != c.globalWidth && currentWidth >= 10 {
		coordinatorDebugLog.Printf("Width sync: user resized active sidebar %s from %d to %d, updating global", clientID, c.globalWidth, currentWidth)
		c.globalWidth = currentWidth
		c.lastWidthSync = time.Now()
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", currentWidth)).Run()
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		untrackLock("widthSyncMu")
		c.widthSyncMu.Unlock()
		c.persistSidebarWidthProfile(clientID, currentWidth)
		return
	}

	targetWidth := c.boundedSidebarWidthForWindow(clientID, c.globalWidth)
	if currentWidth == targetWidth {
		if justBecameActive {
			c.lastActiveWindowID = clientID
		}
		untrackLock("widthSyncMu")
		c.widthSyncMu.Unlock()
		return
	}

	if justBecameActive {
		c.lastActiveWindowID = clientID
	}
	c.lastWidthSync = time.Now()
	coordinatorDebugLog.Printf("Width sync: window=%s current=%d target=%d", clientID, currentWidth, targetWidth)
	untrackLock("widthSyncMu")
	c.widthSyncMu.Unlock()

	listCtx2, listCancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer listCancel2()
	if out, err := exec.CommandContext(listCtx2, "tmux", "list-panes", "-t", clientID, "-F", "#{pane_id} #{pane_current_command}").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, " ")
			if len(parts) >= 2 && strings.HasPrefix(parts[1], "sidebar") {
				resizeCtx2, resizeCancel2 := context.WithTimeout(context.Background(), 2*time.Second)
				exec.CommandContext(resizeCtx2, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", targetWidth)).Run()
				resizeCancel2()
				break
			}
		}
	}
}

// RunWidthSync checks all connected sidebar clients and resizes any whose width
// doesn't match the global target. Called from the main event loop (not the render path)
// to avoid blocking BroadcastRender with tmux subprocess calls.
// When force=true, skips the 500ms debounce (used for immediate restoration after layout changes).
func (c *Coordinator) RunWidthSync(activeWindowID string, force bool) {
	if c.sidebarCollapsed {
		return
	}

	// Snapshot client widths
	c.clientWidthsMu.RLock()
	clientSnapshot := make(map[string]int, len(c.clientWidths))
	for id, w := range c.clientWidths {
		clientSnapshot[id] = w
	}
	c.clientWidthsMu.RUnlock()

	if len(clientSnapshot) == 0 {
		return
	}

	// Read per-window SyncWidth settings under stateMu BEFORE acquiring widthSyncMu
	// (lock ordering: stateMu -> widthSyncMu)
	syncSettings := make(map[string]bool)
	c.stateMu.RLock()
	for i := range c.windows {
		syncSettings[c.windows[i].ID] = c.windows[i].SyncWidth
	}
	c.stateMu.RUnlock()

	trackLock("widthSyncMu", "RunWidthSync")
	c.widthSyncMu.Lock()

	// Detect active window change
	justBecameActive := activeWindowID != "" && c.lastActiveWindowID != activeWindowID
	if justBecameActive {
		coordinatorDebugLog.Printf("Width sync: active window changed to %s", activeWindowID)
		c.lastActiveWindowID = activeWindowID
	}

	// Debounce: ignore resize events within 500ms of our last sync (unless forced)
	if !force {
		sinceLast := time.Since(c.lastWidthSync)
		if sinceLast < 500*time.Millisecond {
			untrackLock("widthSyncMu")
			c.widthSyncMu.Unlock()
			return
		}
	}

	// Build list of panes to resize (compute under lock, execute after unlock)
	type resizeOp struct {
		clientID    string
		targetWidth int
	}
	var ops []resizeOp

	for clientID, currentWidth := range clientSnapshot {
		// Skip header clients
		if strings.HasPrefix(clientID, "header:") {
			continue
		}

		// Check per-window sync opt-out
		if !syncSettings[clientID] {
			continue
		}

		if c.globalWidth == 0 {
			c.globalWidth = currentWidth
		}

		if currentWidth < 10 {
			coordinatorDebugLog.Printf("Width sync: %s below minimum (%d), restoring to global %d", clientID, currentWidth, c.globalWidth)
			targetWidth := c.boundedSidebarWidthForWindow(clientID, c.globalWidth)
			ops = append(ops, resizeOp{clientID: clientID, targetWidth: targetWidth})
			continue
		}

		targetWidth := c.boundedSidebarWidthForWindow(clientID, c.globalWidth)
		if currentWidth != targetWidth {
			coordinatorDebugLog.Printf("Width sync: window=%s current=%d target=%d", clientID, currentWidth, targetWidth)
			ops = append(ops, resizeOp{clientID: clientID, targetWidth: targetWidth})
		}
	}

	if len(ops) > 0 {
		c.lastWidthSync = time.Now()
	}

	untrackLock("widthSyncMu")
	c.widthSyncMu.Unlock()

	// Execute tmux resize operations AFTER releasing all locks
	for _, op := range ops {
		listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", op.clientID, "-F", "#{pane_id} #{pane_current_command}").Output()
		listCancel()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, " ")
			if len(parts) >= 2 && strings.HasPrefix(parts[1], "sidebar") {
				resizeCtx, resizeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				exec.CommandContext(resizeCtx, "tmux", "resize-pane", "-t", parts[0], "-x", fmt.Sprintf("%d", op.targetWidth)).Run()
				resizeCancel()
				break
			}
		}
	}
}

func (c *Coordinator) persistSidebarWidthProfile(windowID string, width int) {
	if windowID == "" || width < 10 {
		return
	}

	windowWidthOut, err := tmuxOutputCtx("display-message", "-p", "-t", windowID, "#{window_width}")
	if err != nil {
		return
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(windowWidthOut)))
	if err != nil || windowWidth <= 0 {
		return
	}

	mobileMax := 110
	if out, err := tmuxOutputCtx("show-option", "-gqv", "@tabby_sidebar_mobile_max_window_cols"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 60 {
			mobileMax = v
		}
	}

	tabletMax := 170
	if out, err := tmuxOutputCtx("show-option", "-gqv", "@tabby_sidebar_tablet_max_window_cols"); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= mobileMax {
			tabletMax = v
		}
	}

	opt := "@tabby_sidebar_width_desktop"
	if windowWidth <= mobileMax {
		opt = "@tabby_sidebar_width_mobile"
	} else if windowWidth <= tabletMax {
		opt = "@tabby_sidebar_width_tablet"
	}
	tmuxRun("set-option", "-gq", opt, fmt.Sprintf("%d", width))
}

func (c *Coordinator) isLikelyAutoConstrainedSidebarWidth(windowID string, currentWidth int) bool {
	if c.globalWidth <= 0 || windowID == "" {
		return false
	}

	maxReasonable, ok := c.sidebarReasonableMaxForWindow(windowID)
	if !ok {
		return false
	}

	return currentWidth <= maxReasonable && maxReasonable < c.globalWidth
}

func (c *Coordinator) boundedSidebarWidthForWindow(windowID string, requested int) int {
	if requested <= 0 {
		return requested
	}
	maxReasonable, ok := c.sidebarReasonableMaxForWindow(windowID)
	if !ok {
		return requested
	}
	if requested > maxReasonable {
		return maxReasonable
	}
	return requested
}

func (c *Coordinator) sidebarReasonableMaxForWindow(windowID string) (int, bool) {
	if windowID == "" {
		return 0, false
	}

	windowWidthOut, err := exec.Command("tmux", "display-message", "-p", "-t", windowID, "#{window_width}").Output()
	if err != nil {
		// Fall back to globalWidth so clamping still works on query failure
		if c.globalWidth > 0 {
			windowWidth := c.globalWidth
			maxPercent := 20
			if out, err2 := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_max_percent").Output(); err2 == nil {
				if v, err3 := strconv.Atoi(strings.TrimSpace(string(out))); err3 == nil && v >= 10 && v <= 60 {
					maxPercent = v
				}
			}
			maxWidth := windowWidth * maxPercent / 100
			if maxWidth < 15 {
				maxWidth = 15
			}
			return maxWidth, true
		}
		return 0, false
	}
	windowWidth, err := strconv.Atoi(strings.TrimSpace(string(windowWidthOut)))
	if err != nil || windowWidth <= 0 {
		return 0, false
	}

	maxPercent := 20
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_max_percent").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 10 && v <= 60 {
			maxPercent = v
		}
	}

	minContentCols := 40
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_min_content_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 20 {
			minContentCols = v
		}
	}

	maxWindowCols := 110
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_mobile_max_window_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 60 {
			maxWindowCols = v
		}
	}

	tabletMaxWindowCols := 170
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_tablet_max_window_cols").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= maxWindowCols {
			tabletMaxWindowCols = v
		}
	}

	widthDesktop := c.globalWidth
	if widthDesktop < 15 {
		widthDesktop = 25
	}
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_desktop").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 15 {
			widthDesktop = v
		}
	}

	if windowWidth > tabletMaxWindowCols {
		return widthDesktop, true
	}

	widthTablet := 20
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_tablet").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 15 {
			widthTablet = v
		}
	}

	if windowWidth > maxWindowCols {
		if widthTablet < 15 {
			widthTablet = 15
		}
		return widthTablet, true
	}

	widthMobile := 15
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_sidebar_width_mobile").Output(); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && v >= 10 {
			widthMobile = v
		}
	}

	maxByFraction := windowWidth * maxPercent / 100
	if maxByFraction < 15 {
		maxByFraction = 15
	}

	maxByContent := windowWidth - minContentCols
	if maxByContent < 15 {
		maxByContent = 15
	}

	maxReasonable := maxByFraction
	if maxByContent < maxReasonable {
		maxReasonable = maxByContent
	}
	if widthMobile < maxReasonable {
		maxReasonable = widthMobile
	}
	if maxReasonable < 10 {
		maxReasonable = 10
	}

	return maxReasonable, true
}

// RenderForClient generates content for a specific client's dimensions
func (c *Coordinator) RenderForClient(clientID string, width, height int) *daemon.RenderPayload {
	// Guard dimensions
	if c.sidebarCollapsed {
		if width < 1 {
			width = 1
		}
	} else if width < 3 {
		width = 3
	}
	if height < 5 {
		height = 24
	}

	// NOTE: Width sync has been moved off the render path to prevent deadlocks.
	// It now runs from the main event loop via RunWidthSync().

	// If sidebar is collapsed, render minimal expand button only
	if c.sidebarCollapsed {
		var s strings.Builder
		expandStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(c.getTextColorWithFallback(""))).
			Bold(true)
		dimStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(c.getInactiveTextColorWithFallback("")))

		// 3-row tall expand button near the top for easy touch access
		// Render as a single column when collapsed.
		buttonStart := 1             // Start at row 1 for visibility
		buttonEnd := buttonStart + 2 // 3 rows total

		for i := 0; i < height; i++ {
			if i >= buttonStart && i <= buttonEnd {
				// Button rows - bright and clickable
				s.WriteString(expandStyle.Render(">") + "\n")
			} else {
				// Non-button rows - dim background
				s.WriteString(dimStyle.Render(" ") + "\n")
			}
		}

		content := s.String()
		return &daemon.RenderPayload{
			Content:    content,
			Width:      width,
			Height:     height,
			TotalLines: height,
			Regions: []daemon.ClickableRegion{
				// Main button area (3 rows) - primary click target
				{StartLine: buttonStart, EndLine: buttonEnd, Action: "expand_sidebar", Target: ""},
				// Entire sidebar is also clickable as fallback
				{StartLine: 0, EndLine: height - 1, Action: "expand_sidebar", Target: ""},
			},
		}
	}

	// Normal render - guard minimum width
	if width < 10 {
		width = 25
	}

	// Track width for pet physics (safe to update outside lock - advisory)
	c.lastWidth = width

	// Store per-client width for accurate click detection on resize
	c.clientWidthsMu.Lock()
	c.clientWidths[clientID] = width
	c.clientWidthsMu.Unlock()

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Generate sidebar header (session name, gear, position toggle, collapse)
	headerContent, headerRegions := c.generateSidebarHeader(width, clientID)
	headerLines := strings.Count(headerContent, "\n")

	topWidgets, topWRegions, bottomWidgets, bottomWRegions := c.generateWidgetZones(width, false)
	topWidgetLines := strings.Count(topWidgets, "\n")
	bottomWidgetLines := strings.Count(bottomWidgets, "\n")

	mainContent, mainRegions := c.generateMainContent(clientID, width, height)
	mainContentLines := strings.Count(mainContent, "\n")

	maxMainLines := height - headerLines - topWidgetLines - bottomWidgetLines
	if maxMainLines < 0 {
		maxMainLines = 0
	}

	// Auto-hide pet when viewport is too small to show all tabs
	if maxMainLines < mainContentLines && c.config.Widgets.Pet.Enabled {
		topWidgets, topWRegions, bottomWidgets, bottomWRegions = c.generateWidgetZones(width, true)
		topWidgetLines = strings.Count(topWidgets, "\n")
		bottomWidgetLines = strings.Count(bottomWidgets, "\n")
		maxMainLines = height - headerLines - topWidgetLines - bottomWidgetLines
		if maxMainLines < 0 {
			maxMainLines = 0
		}
	}

	mainContent, mainRegions = trimContentAndRegions(mainContent, mainRegions, maxMainLines)
	mainLines := strings.Count(mainContent, "\n")

	// Pad main content to pin bottom widgets to the viewport bottom
	if mainLines < maxMainLines {
		mainContent += strings.Repeat("\n", maxMainLines-mainLines)
		mainLines = maxMainLines
	}

	// Offset top widget regions by header height
	for i := range topWRegions {
		topWRegions[i].StartLine += headerLines
		topWRegions[i].EndLine += headerLines
	}

	// Offset main content regions by header + top widgets
	mainOffset := headerLines + topWidgetLines
	for i := range mainRegions {
		mainRegions[i].StartLine += mainOffset
		mainRegions[i].EndLine += mainOffset
	}

	// Store the content start line for pet widget click detection
	// This tells us where the bottom zone starts in absolute content coordinates
	bottomOffset := headerLines + topWidgetLines + mainLines
	c.petLayout.ContentStartLine = bottomOffset

	// Offset bottom widget regions by header + top widgets + main content
	for i := range bottomWRegions {
		bottomWRegions[i].StartLine += bottomOffset
		bottomWRegions[i].EndLine += bottomOffset
	}

	// Combine everything: header + top_widgets + main + bottom_widgets
	fullContent := headerContent + topWidgets + mainContent + bottomWidgets

	// Don't apply background fill - let terminal's natural background (set via ApplyThemeToPane) show through

	allRegions := append(headerRegions, topWRegions...)
	allRegions = append(allRegions, mainRegions...)
	allRegions = append(allRegions, bottomWRegions...)

	// Overlay floating collapse button in top-right corner of header
	// This makes the collapse button easy to tap on mobile
	// Button is 2 columns wide, spans header height rows
	btnRows := c.config.Sidebar.Header.Height
	if btnRows < 1 {
		btnRows = 3
	}
	if width >= 6 { // Only show if sidebar is wide enough
		collapseBtn := lipgloss.NewStyle().
			Foreground(lipgloss.Color(c.getTextColorWithFallback(""))).
			Bold(true)

		btnWidth := 2

		lines := strings.Split(fullContent, "\n")
		for row := 0; row < btnRows && row < len(lines); row++ {
			// Strip any trailing whitespace/newline from line
			line := strings.TrimRight(lines[row], " \t")

			// Calculate visual width by stripping ANSI codes
			plainLine := stripAnsi(line)
			visualWidth := uniseg.StringWidth(plainLine)

			// Build new line: original content + padding + button
			targetCol := width - btnWidth
			if visualWidth < targetCol {
				// Need to pad
				line = line + strings.Repeat(" ", targetCol-visualWidth)
			}
			// Note: if visualWidth >= targetCol, button may overlap content
			// That's acceptable for the floating overlay effect

			lines[row] = line + collapseBtn.Render("< ")
		}
		fullContent = strings.Join(lines, "\n")

		// Add collapse button click region in top-right
		allRegions = append([]daemon.ClickableRegion{{
			StartLine: 0, EndLine: btnRows - 1,
			StartCol: width - btnWidth, EndCol: width,
			Action: "collapse_sidebar", Target: "",
		}}, allRegions...) // Prepend so it has priority
	}

	// Count total lines
	totalLines := strings.Count(fullContent, "\n")

	// Debug logging
	coordinatorDebugLog.Printf("RenderForClient: client=%s width=%d height=%d", clientID, width, height)
	coordinatorDebugLog.Printf("  Content: %d lines (%d header + %d topW + %d main + %d bottomW)",
		totalLines, headerLines, topWidgetLines, mainLines, bottomWidgetLines)
	coordinatorDebugLog.Printf("  Regions: %d total", len(allRegions))

	sidebarBg := ""
	terminalBg := ""
	if c.theme != nil {
		sidebarBg = c.theme.SidebarBg
		terminalBg = c.theme.TerminalBg
	}

	return &daemon.RenderPayload{
		Content:       fullContent,
		PinnedContent: "", // No longer using pinned content
		Width:         width,
		Height:        height,
		TotalLines:    totalLines,
		PinnedHeight:  0, // No pinned section
		Regions:       allRegions,
		PinnedRegions: nil, // All regions are in main Regions array now
		SidebarBg:     sidebarBg,
		TerminalBg:    terminalBg,
	}
}

func trimContentAndRegions(content string, regions []daemon.ClickableRegion, maxLines int) (string, []daemon.ClickableRegion) {
	if maxLines < 0 {
		maxLines = 0
	}
	if content == "" {
		return "", nil
	}

	lines := strings.Split(content, "\n")
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	if hasTrailingNewline && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}

	if len(lines) <= maxLines {
		return content, regions
	}

	if maxLines == 0 {
		return "", nil
	}

	trimmedLines := lines[:maxLines]
	trimmedContent := strings.Join(trimmedLines, "\n") + "\n"

	filteredRegions := make([]daemon.ClickableRegion, 0, len(regions))
	maxIdx := maxLines - 1
	for _, r := range regions {
		if r.StartLine > maxIdx {
			continue
		}
		if r.EndLine > maxIdx {
			r.EndLine = maxIdx
		}
		filteredRegions = append(filteredRegions, r)
	}

	return trimmedContent, filteredRegions
}

// abbreviatePath shortens a path for display in the header.
// It replaces the home directory with ~ and shows only the last 2-3 path components.
func abbreviatePath(path string, maxWidth int) string {
	if path == "" {
		return ""
	}

	// Replace home directory with ~
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + strings.TrimPrefix(path, home)
	}

	// If already short enough, return as-is
	if uniseg.StringWidth(path) <= maxWidth {
		return path
	}

	// Split path into components
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) == 0 {
		return path
	}

	// Start with just the last component
	var result string
	if parts[0] == "~" {
		// Keep the ~ prefix
		result = "~/"
		parts = parts[1:]
	} else if parts[0] == "" && len(parts) > 1 {
		// Absolute path starting with /
		result = "/"
		parts = parts[1:]
	}

	// Build from the end, adding components until we run out of space
	var components []string
	for i := len(parts) - 1; i >= 0; i-- {
		components = append([]string{parts[i]}, components...)
		testPath := result + strings.Join(components, "/")
		if i > 0 {
			testPath = result + ".../" + strings.Join(components, "/")
		}
		if uniseg.StringWidth(testPath) > maxWidth {
			// Too long, use previous iteration
			if len(components) == 1 {
				// Even one component is too long, truncate it
				return runewidth.Truncate(result+components[0], maxWidth, "")
			}
			components = components[1:] // Remove the component we just tried to add
			break
		}
	}

	// Build final path
	if len(components) < len(parts) {
		return result + ".../" + strings.Join(components, "/")
	}
	return result + strings.Join(components, "/")
}

// RenderHeaderForClient renders a 1-line pane header for a specific content pane.
// Each content pane has its own header showing that pane's label and action buttons.
// clientID format: "header:%123" where %123 is the pane ID the header sits above.
func (c *Coordinator) RenderHeaderForClient(clientID string, width, height int) *daemon.RenderPayload {
	if width < 5 {
		width = 5
	}

	// Parse pane ID from clientID
	paneID := strings.TrimPrefix(clientID, "header:")
	if paneID == "" {
		return nil
	}

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the window this header belongs to
	var foundWindow *tmux.Window
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				foundWindow = &c.windows[i]
				break
			}
		}
		if foundWindow != nil {
			break
		}
	}

	if foundWindow == nil {
		blankLine := strings.Repeat(" ", width)
		return &daemon.RenderPayload{
			Content:    blankLine,
			Width:      width,
			Height:     1,
			TotalLines: 1,
		}
	}

	// Find the specific pane this header belongs to
	var foundPane *tmux.Pane
	for i := range foundWindow.Panes {
		if foundWindow.Panes[i].ID == paneID {
			foundPane = &foundWindow.Panes[i]
			break
		}
	}
	if foundPane == nil {
		blankLine := strings.Repeat(" ", width)
		return &daemon.RenderPayload{
			Content:    blankLine,
			Width:      width,
			Height:     1,
			TotalLines: 1,
		}
	}

	// Detect which resize directions apply to this pane based on neighbors
	canResizeH := false // horizontal neighbors (side by side) -> left/right (←→)
	canResizeV := false // vertical neighbors (stacked) -> up/down (↑↓)
	for _, op := range foundWindow.Panes {
		if isAuxiliaryPane(op) || op.ID == foundPane.ID {
			continue
		}
		// Horizontal neighbor: vertical ranges overlap -> panes are side by side
		if max(foundPane.Top, op.Top) < min(foundPane.Top+foundPane.Height, op.Top+op.Height) {
			canResizeH = true
		}
		// Vertical neighbor: horizontal ranges overlap -> panes are stacked
		if max(foundPane.Left, op.Left) < min(foundPane.Left+foundPane.Width, op.Left+op.Width) {
			canResizeV = true
		}
	}

	headerColors := c.GetHeaderColorsForPane(paneID)
	headerBg := headerColors.Bg
	headerFg := headerColors.Fg

	// Dim header colors for inactive content panes (no exec — use cached pane state)
	if c.config.PaneHeader.DimInactive {
		paneIsDimmed := false
		for _, p := range foundWindow.Panes {
			if p.ID == paneID && !p.Active {
				paneIsDimmed = true
				break
			}
		}
		if paneIsDimmed {
			opacity := c.config.PaneHeader.DimOpacity
			if opacity <= 0 || opacity > 1 {
				opacity = 0.6
			}
			tBg := c.config.PaneHeader.TerminalBg
			headerBg = desaturateHex(headerBg, opacity, tBg)
			headerFg = desaturateHex(headerFg, opacity, tBg)
		}
	}
	groupColor := headerBg
	isWindowActive := foundWindow.Active

	baseStyle := lipgloss.NewStyle()
	if headerBg != "" {
		baseStyle = baseStyle.Background(lipgloss.Color(headerBg))
	}

	collapseBtn := ""
	isCollapsed := false
	// Count content panes (exclude sidebar and header panes)
	contentPaneCount := 0
	for _, p := range foundWindow.Panes {
		if !isAuxiliaryPane(p) {
			contentPaneCount++
		}
	}
	if contentPaneCount > 1 {
		// Check this specific pane's collapsed state
		if out, err := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_collapsed").Output(); err == nil {
			val := strings.TrimSpace(string(out))
			if val == "1" {
				isCollapsed = true
			}
		}
		if isCollapsed {
			collapseBtn = c.config.PaneHeader.CollapseCollapsedIcon
		} else {
			collapseBtn = c.config.PaneHeader.CollapseExpandedIcon
		}
	}
	splitVBtn := "|"
	splitHBtn := "-"
	if contentPaneCount > 1 && collapseBtn == "" {
		collapseBtn = "▾"
		if isCollapsed {
			collapseBtn = "▸"
		}
	}
	vGrowBtn := c.config.PaneHeader.ResizeVerticalGrowIcon
	if vGrowBtn == "" {
		vGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if vGrowBtn == "" {
		vGrowBtn = "↓"
	}
	vShrinkBtn := c.config.PaneHeader.ResizeVerticalShrinkIcon
	if vShrinkBtn == "" {
		vShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if vShrinkBtn == "" {
		vShrinkBtn = "↑"
	}
	hGrowBtn := c.config.PaneHeader.ResizeHorizontalGrowIcon
	if hGrowBtn == "" {
		hGrowBtn = c.config.PaneHeader.ResizeGrowIcon
	}
	if hGrowBtn == "" {
		hGrowBtn = "→"
	}
	hShrinkBtn := c.config.PaneHeader.ResizeHorizontalShrinkIcon
	if hShrinkBtn == "" {
		hShrinkBtn = c.config.PaneHeader.ResizeShrinkIcon
	}
	if hShrinkBtn == "" {
		hShrinkBtn = "←"
	}
	resizeSep := c.config.PaneHeader.ResizeSeparator
	if resizeSep == "" {
		resizeSep = "¦"
	}
	menuBtn := "≡"
	compactSplitVBtn := "|"
	compactSplitHBtn := "-"
	compactCloseBtn := "x"
	closeBtn := "×"
	compactMode := width <= 40
	if compactMode {
		// In compact mode show | - x; add collapse button when multiple panes exist
		if contentPaneCount > 1 && collapseBtn != "" {
			menuBtn = collapseBtn + " " + compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		} else {
			menuBtn = compactSplitVBtn + " " + compactSplitHBtn + " " + compactCloseBtn
		}
	}
	showMenuButton := compactMode
	showInlineControls := !compactMode

	showInlineCollapse := collapseBtn != "" && showInlineControls
	showInlineSplits := showInlineControls
	showVerticalResize := contentPaneCount > 1 && showInlineControls && canResizeV
	showHorizontalResize := contentPaneCount > 1 && showInlineControls && canResizeH
	showInlineClose := showInlineControls
	buttonsStr := "  "
	if showMenuButton {
		buttonsStr += menuBtn + "   "
	}
	if showInlineCollapse {
		buttonsStr += collapseBtn + "  "
	}
	if showInlineSplits {
		buttonsStr += splitVBtn + " " + splitHBtn + "  "
	}
	if showVerticalResize || showHorizontalResize {
		if showVerticalResize {
			buttonsStr += vGrowBtn + " " + vShrinkBtn + " "
		}
		if showHorizontalResize {
			buttonsStr += hGrowBtn + " " + hShrinkBtn + " "
		}
		buttonsStr += " "
	}
	if showInlineClose {
		buttonsStr += closeBtn + "  "
	}
	buttonsWidth := uniseg.StringWidth(buttonsStr)

	// Build label for this pane: "win.pane command [path]"
	// Use visual position (matching sidebar order) instead of tmux index
	label := foundPane.Command
	if foundPane.LockedTitle != "" {
		label = foundPane.LockedTitle
	} else if foundPane.Title != "" && foundPane.Title != foundPane.Command && foundPane.Title != foundWindow.Name {
		label = foundPane.Title
	}
	winVisualNum := c.windowVisualPos[foundWindow.ID]
	labelText := fmt.Sprintf("%d.%d %s", winVisualNum, foundPane.Index, label)

	groupIcon := ""
	for _, group := range c.grouped {
		for _, groupWindow := range group.Windows {
			if groupWindow.ID == foundWindow.ID {
				groupIcon = strings.TrimSpace(group.Theme.Icon)
				break
			}
		}
		if groupIcon != "" {
			break
		}
	}
	windowIcon := strings.TrimSpace(foundWindow.Icon)
	if groupIcon != "" {
		labelText = groupIcon + " " + labelText
	}
	if windowIcon != "" {
		labelText = windowIcon + " " + labelText
	}

	// Compute group accent early so we can account for its width in layout calculations
	groupAccent := ""
	if groupColor != "" {
		groupAccent = lipgloss.NewStyle().SetString("▇").Foreground(lipgloss.Color(groupColor)).String()
	}
	groupAccentWidth := uniseg.StringWidth(stripAnsi(groupAccent))

	// Add current path if available
	if foundPane.CurrentPath != "" {
		// Available width for the label
		availWidth := width - groupAccentWidth - 1 - buttonsWidth // groupAccent + leading space
		if availWidth < 4 {
			availWidth = 4
		}

		// Calculate how much space we have for the path after the base label
		baseWidth := uniseg.StringWidth(labelText)
		pathMaxWidth := availWidth - baseWidth - 1 // 1 for space before path

		if pathMaxWidth > 8 { // Only add path if we have reasonable space (at least 8 chars)
			abbrevPath := abbreviatePath(foundPane.CurrentPath, pathMaxWidth)
			if abbrevPath != "" {
				labelText = fmt.Sprintf("%s %s", labelText, abbrevPath)
			}
		}
	}

	// Available width for the label
	availWidth := width - groupAccentWidth - 1 - buttonsWidth // groupAccent + leading space
	if availWidth < 4 {
		availWidth = 4
	}

	// Truncate label if needed (shouldn't be necessary with our path abbreviation, but just in case)
	if uniseg.StringWidth(labelText) > availWidth {
		labelText = runewidth.Truncate(labelText, availWidth, "~")
	}

	// Style: active pane bold+bright, others dimmed
	isActive := foundPane.Active && isWindowActive
	segStyle := baseStyle.Copy()
	btnStyle := baseStyle.Copy()

	// Always use group's fg color - no manipulation
	if headerFg != "" {
		segStyle = segStyle.Foreground(lipgloss.Color(headerFg))
		btnStyle = btnStyle.Foreground(lipgloss.Color(headerFg))
	}
	if isActive {
		segStyle = segStyle.Bold(true)
	}

	// Build rendered line and click regions
	var regions []daemon.ClickableRegion

	labelWidth := uniseg.StringWidth(labelText)
	renderedLabel := segStyle.Render(labelText)
	currentCol := groupAccentWidth + 1 + labelWidth
	btnAreaStart := width - buttonsWidth
	if btnAreaStart < currentCol {
		btnAreaStart = currentCol
	}
	spacerWidth := btnAreaStart - currentCol

	// Pad the full line with the header background
	fullLineStyle := baseStyle.Copy().Width(width)

	line := groupAccent + " " +
		renderedLabel +
		strings.Repeat(" ", spacerWidth) +
		btnStyle.Render(buttonsStr)

	// Ensure the final rendered line has the correct background applied everywhere
	if headerBg != "" {
		line = c.applyBackgroundFill(line, headerBg, width)
	} else {
		line = fullLineStyle.Render(line)
	}

	// buttonsStr always begins with "  " (2 spaces); skip past them so regions
	// align with the actual button characters.
	cursor := btnAreaStart + 2
	if showMenuButton {
		if compactMode {
			// Optional collapse button (when multi-pane)
			if contentPaneCount > 1 && collapseBtn != "" {
				collapseEnd := cursor + uniseg.StringWidth(collapseBtn) + 1
				regions = append(regions, daemon.ClickableRegion{
					StartLine: 0, EndLine: 0,
					StartCol: cursor, EndCol: collapseEnd,
					Action: "toggle_pane_collapse", Target: paneID,
				})
				cursor = collapseEnd
			}
			splitVEnd := cursor + uniseg.StringWidth(compactSplitVBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: splitVEnd,
				// "|" = vertical divider → side-by-side panes → split-window -h
				Action: "header_split_h", Target: paneID,
			})
			splitHEnd := splitVEnd + uniseg.StringWidth(compactSplitHBtn) + 1
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitVEnd, EndCol: splitHEnd,
				// "-" = horizontal divider → stacked panes → split-window -v
				Action: "header_split_v", Target: paneID,
			})
			closeEnd := splitHEnd + uniseg.StringWidth(compactCloseBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: splitHEnd, EndCol: closeEnd,
				Action: "header_close", Target: paneID,
			})
			cursor = closeEnd + 3
		} else {
			menuEnd := cursor + uniseg.StringWidth(menuBtn)
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: menuEnd,
				Action: "pane_menu", Target: paneID,
			})
			cursor = menuEnd + 3
		}
	}
	// Non-compact inline buttons: 1 space within groups, 2 spaces between groups.
	if showInlineCollapse {
		collapseEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: collapseEnd,
			Action: "toggle_pane_collapse", Target: paneID,
		})
		cursor = collapseEnd + 1 // extra space for group gap
	}
	if showInlineSplits {
		splitVEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitVEnd,
			// "|" = vertical divider → side-by-side panes → split-window -h
			Action: "header_split_h", Target: paneID,
		})
		cursor = splitVEnd
		splitHEnd := cursor + 2
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: splitHEnd,
			// "-" = horizontal divider → stacked panes → split-window -v
			Action: "header_split_v", Target: paneID,
		})
		cursor = splitHEnd + 1 // extra space for group gap
	}
	if showVerticalResize || showHorizontalResize {
		// no separator — group gap already added after splits
		if showVerticalResize {
			vGrowEnd := cursor + 2
			vShrinkEnd := vGrowEnd + 2
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: vGrowEnd,
				Action: "pane_grow_v", Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: vGrowEnd, EndCol: vShrinkEnd,
				Action: "pane_shrink_v", Target: paneID,
			})
			cursor = vShrinkEnd
		}
		if showHorizontalResize {
			hGrowEnd := cursor + 2
			hShrinkEnd := hGrowEnd + 2
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: cursor, EndCol: hGrowEnd,
				Action: "pane_grow_h", Target: paneID,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: 0, EndLine: 0,
				StartCol: hGrowEnd, EndCol: hShrinkEnd,
				Action: "pane_shrink_h", Target: paneID,
			})
			cursor = hShrinkEnd
		}
		cursor += 2 // extra space for group gap before close
	}
	if showInlineClose {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			StartCol: cursor, EndCol: width,
			Action: "header_close", Target: paneID,
		})
	}

	// Full header area context menu region for non-compact mode only.
	// In compact mode, keep menu opening scoped to the unified menu button.
	if !compactMode {
		regions = append(regions, daemon.ClickableRegion{
			StartLine: 0, EndLine: 0,
			Action: "header_context", Target: paneID,
		})
	}

	if c.config.PaneHeader.CustomBorder {
		return &daemon.RenderPayload{
			Content:    line,
			Width:      width,
			Height:     1,
			TotalLines: 1,
			Regions:    regions,
		}
	}

	sidebarBg := ""
	terminalBg := ""
	if c.theme != nil {
		sidebarBg = c.theme.SidebarBg
		terminalBg = c.theme.TerminalBg
	}

	return &daemon.RenderPayload{
		Content:    line,
		Width:      width,
		Height:     1,
		TotalLines: 1,
		Regions:    regions,
		SidebarBg:  sidebarBg,
		TerminalBg: terminalBg,
	}
}

// hashContent returns a simple hash of content for comparison
func hashContent(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// syncAllSidebarWidths resizes all sidebar panes to match the given width.
// Used for button clicks (grow/shrink) where we want to resize ALL sidebars.
func syncAllSidebarWidths(newWidth int) {
	syncSidebarWidthsExcept(newWidth, "")
}

// syncOtherSidebarWidths resizes all sidebar panes EXCEPT the one in skipWindowID.
// Used when user drags a sidebar border - we sync others but don't interrupt the drag.
func syncOtherSidebarWidths(newWidth int, skipWindowID string) {
	syncSidebarWidthsExcept(newWidth, skipWindowID)
}

// syncSidebarWidthsExcept resizes sidebar panes to match the given width.
// If skipWindowID is non-empty, skips the sidebar in that window.
// Respects @tabby_sync_width window option (default true).
func syncSidebarWidthsExcept(newWidth int, skipWindowID string) {
	out, err := tmuxOutputCtx("list-panes", "-a", "-F", "#{pane_id}|#{window_id}|#{?@tabby_sync_width,#{@tabby_sync_width},1}|#{pane_current_command}")
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) >= 4 && strings.HasPrefix(parts[3], "sidebar") {
			paneID := parts[0]
			windowID := parts[1]
			syncSetting := parts[2]

			if skipWindowID != "" && windowID == skipWindowID {
				continue
			}

			if syncSetting == "1" || syncSetting == "true" {
				tmuxRun("resize-pane", "-t", paneID, "-x", fmt.Sprintf("%d", newWidth))
			}
		}
	}
}

// getBusyFrames returns the busy indicator animation frames
func (c *Coordinator) getBusyFrames() []string {
	if len(c.config.Indicators.Busy.Frames) > 0 {
		return c.config.Indicators.Busy.Frames
	}
	return []string{"◐", "◓", "◑", "◒"}
}

// getSlowSpinnerFrame returns a slowed-down spinner frame index
// This makes each frame visible for 200ms instead of 100ms (fixes #5: animation skips frames)
func (c *Coordinator) getSlowSpinnerFrame() int {
	return c.spinnerFrame / 2
}

func (c *Coordinator) getAnimatedActiveIndicator(fallback string) string {
	frames := c.config.Sidebar.Colors.ActiveIndicatorFrames
	if len(frames) == 0 {
		return fallback
	}
	frame := frames[c.getSlowSpinnerFrame()%len(frames)]
	if frame == "" {
		return " "
	}
	return frame
}

func (c *Coordinator) HasActiveIndicatorAnimation() bool {
	return len(c.config.Sidebar.Colors.ActiveIndicatorFrames) > 1
}

// getIndicatorIcon returns the icon for an indicator
func (c *Coordinator) getIndicatorIcon(ind config.Indicator) string {
	if ind.Icon != "" {
		return ind.Icon
	}
	return "●"
}

// headerBoolDefault returns the value of a *bool config field, defaulting to true if nil.
func headerBoolDefault(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// getActiveWindowGroupTheme returns the theme of the active window's group.
// Returns nil if no active window or group is found.
func (c *Coordinator) getActiveWindowGroupTheme() *config.Theme {
	// Find the active window
	var activeWin *tmux.Window
	for i := range c.windows {
		if c.windows[i].Active {
			activeWin = &c.windows[i]
			break
		}
	}
	if activeWin == nil {
		return nil
	}

	// Find which group contains this window
	for i, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == activeWin.ID {
				return &c.grouped[i].Theme
			}
		}
	}
	return nil
}

// getWindowTabColors returns the tab fg/bg colors for a window using the same
// logic as the sidebar window list. isActive controls whether active or inactive
// variants are used for group/theme colors.
func (c *Coordinator) getWindowTabColors(windowID string, isActive bool) (string, string, bool) {
	var targetWin *tmux.Window
	for i := range c.windows {
		if c.windows[i].ID == windowID {
			targetWin = &c.windows[i]
			break
		}
	}
	if targetWin == nil {
		return "", "", false
	}

	var theme config.Theme
	var customColor string
	var foundGroup bool
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == targetWin.ID {
				theme = group.Theme
				customColor = win.CustomColor
				foundGroup = true
				break
			}
		}
		if foundGroup {
			break
		}
	}
	if !foundGroup {
		return "", "", false
	}

	isDarkBg := c.bgDetector.IsDarkBackground()
	if c.theme != nil {
		isDarkBg = c.theme.Dark
	}
	theme = grouping.ResolveThemeColors(theme, isDarkBg)

	var bgColor, fgColor string
	isTransparent := customColor == "transparent"

	if isTransparent {
		bgColor = ""
		if isActive {
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			fgColor = theme.Fg
		}
	} else if customColor != "" {
		if isActive {
			bgColor = customColor
		} else {
			bgColor = grouping.ShadeColorByIndex(customColor, 1)
		}
		fgColor = "#ffffff"
	} else if isActive {
		bgColor = theme.ActiveBg
		if bgColor == "" {
			bgColor = theme.Bg
		}
		fgColor = theme.ActiveFg
		if fgColor == "" {
			fgColor = theme.Fg
		}
	} else {
		bgColor = theme.Bg
		fgColor = theme.Fg
	}

	return fgColor, bgColor, true
}

// generateSidebarHeader renders the pinned header bar at the top of the sidebar.
// Returns the header content string (with trailing newline) and click regions.
// Left-click collapses sidebar. Right-click opens settings context menu.
func (c *Coordinator) generateSidebarHeader(width int, clientID string) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion

	hdr := c.config.Sidebar.Header
	headerText := hdr.Text
	headerHeight := hdr.Height
	paddingBottom := hdr.PaddingBottom
	centered := headerBoolDefault(hdr.Centered)
	activeColor := headerBoolDefault(hdr.ActiveColor)
	bold := headerBoolDefault(hdr.Bold)

	// Resolve colors from this window's tab colors
	fgColor := hdr.Fg
	bgColor := hdr.Bg
	if strings.EqualFold(fgColor, "auto") {
		fgColor = ""
	}
	if strings.EqualFold(bgColor, "auto") {
		bgColor = ""
	}
	if activeColor && (fgColor == "" || bgColor == "") {
		activeWindowID := ""
		for i := range c.windows {
			if c.windows[i].Active {
				activeWindowID = c.windows[i].ID
				break
			}
		}
		if activeWindowID != "" {
			if tabFg, tabBg, ok := c.getWindowTabColors(activeWindowID, true); ok {
				if fgColor == "" {
					fgColor = tabFg
				}
				if bgColor == "" {
					bgColor = tabBg
				}
			}
		}
	}
	if fgColor == "" {
		fgColor = c.getHeaderTextColorWithFallback("")
	}

	// Build style
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(fgColor)).
		Bold(bold)

	if bgColor != "" {
		headerStyle = headerStyle.Background(lipgloss.Color(bgColor))
	}

	// Truncate text if needed (leave space for collapse button overlay)
	maxNameWidth := width - 5 // 1 padding + 3 button + 1 gap
	if maxNameWidth < 3 {
		maxNameWidth = 3
	}

	nameWidth := uniseg.StringWidth(headerText)
	if nameWidth > maxNameWidth {
		truncated := ""
		w := 0
		for _, r := range headerText {
			rw := runewidth.RuneWidth(r)
			if w+rw > maxNameWidth-1 {
				break
			}
			truncated += string(r)
			w += rw
		}
		headerText = truncated + "~"
		nameWidth = uniseg.StringWidth(headerText)
	}

	// Row style applies bg color across the full width
	rowStyle := lipgloss.NewStyle()
	if bgColor != "" {
		rowStyle = rowStyle.Background(lipgloss.Color(bgColor))
	}

	// Determine which row gets the text (vertical centering)
	textRow := 0
	if centered && headerHeight > 1 {
		textRow = headerHeight / 2
	}

	// Render header rows
	for line := 0; line < headerHeight; line++ {
		if line == textRow {
			if centered {
				// Horizontal centering
				leftPad := (width - nameWidth) / 2
				if leftPad < 0 {
					leftPad = 0
				}
				rightPad := width - leftPad - nameWidth
				if rightPad < 0 {
					rightPad = 0
				}
				s.WriteString(rowStyle.Render(strings.Repeat(" ", leftPad)) + headerStyle.Render(headerText) + rowStyle.Render(strings.Repeat(" ", rightPad)) + "\n")
			} else {
				// Left-aligned (legacy layout)
				spacerWidth := width - 1 - nameWidth
				if spacerWidth < 0 {
					spacerWidth = 0
				}
				s.WriteString(rowStyle.Render(" ") + headerStyle.Render(headerText) + rowStyle.Render(strings.Repeat(" ", spacerWidth)) + "\n")
			}
		} else {
			s.WriteString(rowStyle.Render(strings.Repeat(" ", width)) + "\n")
		}
	}

	// Transparent padding rows below header (no bg color)
	for i := 0; i < paddingBottom; i++ {
		s.WriteString(strings.Repeat(" ", width) + "\n")
	}

	// Clickable region covers the header rows (not padding)
	regions = append(regions, daemon.ClickableRegion{
		StartLine: 0, EndLine: headerHeight - 1,
		Action: "sidebar_header_area", Target: "",
	})

	return s.String(), regions
}

// generateMainContent creates the main scrollable area with window list
// clientID is the window ID that this content is being rendered for
func (c *Coordinator) generateMainContent(clientID string, width, height int) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion

	currentLine := 0

	// Configurable tree characters
	treeBranchChar := c.config.Sidebar.Colors.TreeBranch
	if treeBranchChar == "" {
		treeBranchChar = "├─"
	}
	treeBranchLastChar := c.config.Sidebar.Colors.TreeBranchLast
	if treeBranchLastChar == "" {
		treeBranchLastChar = "└─"
	}
	treeContinueChar := c.config.Sidebar.Colors.TreeContinue
	if treeContinueChar == "" {
		treeContinueChar = "│"
	}
	treeConnectorChar := c.config.Sidebar.Colors.TreeConnector
	if treeConnectorChar == "" {
		treeConnectorChar = "─"
	}

	// Disclosure icons
	expandedIcon := c.config.Sidebar.Colors.DisclosureExpanded
	if expandedIcon == "" {
		expandedIcon = "⊟"
	}
	collapsedIcon := c.config.Sidebar.Colors.DisclosureCollapsed
	if collapsedIcon == "" {
		collapsedIcon = "⊞"
	}

	// Tree color
	treeStyle := lipgloss.NewStyle()
	treeFg := c.getTreeFgWithFallback(c.config.Sidebar.Colors.TreeFg)
	treeStyle = treeStyle.Foreground(lipgloss.Color(treeFg))
	treeBg := c.config.Sidebar.Colors.TreeBg
	if treeBg == "" && c.theme != nil {
		treeBg = c.theme.TreeBg
	}
	if strings.EqualFold(treeBg, "transparent") {
		treeBg = ""
	}
	if treeBg != "" {
		treeStyle = treeStyle.Background(lipgloss.Color(treeBg))
	}

	inactiveFg := c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)

	// Disclosure color (use config or terminal default)
	disclosureColor := c.getDisclosureFgWithFallback(c.config.Sidebar.Colors.DisclosureFg)

	// Active indicator config
	activeIndicator := c.config.Sidebar.Colors.ActiveIndicator
	if activeIndicator == "" {
		activeIndicator = "◀"
	}
	activeIndFgConfig := c.config.Sidebar.Colors.ActiveIndicatorFg
	activeIndBgConfig := c.config.Sidebar.Colors.ActiveIndicatorBg

	if c.config.Sidebar.PrefixMode {
		return c.generatePrefixModeContent(clientID, width, height, treeBranchChar, treeBranchLastChar, treeContinueChar, treeConnectorChar, expandedIcon, collapsedIcon, treeStyle, disclosureColor, activeIndicator, activeIndFgConfig, activeIndBgConfig)
	}

	// Iterate over grouped windows
	numGroups := len(c.grouped)
	for gi, group := range c.grouped {
		isLastGroup := gi == numGroups-1
		theme := group.Theme

		// Auto-fill missing theme colors with intelligent defaults
		isDarkBg := c.bgDetector.IsDarkBackground()
		if c.theme != nil {
			isDarkBg = c.theme.Dark
		}
		theme = grouping.ResolveThemeColors(theme, isDarkBg)

		isCollapsed := c.collapsedGroups[group.Name]

		// Group header style
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(inactiveFg)).
			Bold(true)

		// Collapse indicator
		collapseIcon := expandedIcon
		if isCollapsed {
			collapseIcon = collapsedIcon
		}
		collapseStyle := lipgloss.NewStyle()
		if disclosureColor != "" {
			collapseStyle = collapseStyle.Foreground(lipgloss.Color(disclosureColor))
		}

		// Build header
		icon := strings.TrimSpace(theme.Icon)
		headerText := group.Name
		if isCollapsed && len(group.Windows) > 0 {
			headerText += fmt.Sprintf(" (%d)", len(group.Windows))
		}
		// Always add a space between prefix and group name for consistent alignment.
		// Icon (if present) is included INSIDE the bg-filled area so backgrounds
		// align across all groups regardless of icon width.
		renderedIcon := ""
		if icon != "" {
			renderedIcon = icon
		}
		headerText = " " + headerText

		// Track group header line
		groupStartLine := currentLine
		hasWindows := len(group.Windows) > 0

		// Render group header
		{
			bg := theme.Bg
			if strings.EqualFold(bg, "transparent") {
				bg = ""
			}
			if hasWindows {
				prefix := collapseStyle.Render(collapseIcon)
				prefixW := uniseg.StringWidth(stripAnsi(prefix))
				menuBtnW := 2 // " ⋮"
				restW := width - prefixW - menuBtnW
				if restW < 1 {
					restW = 1
				}

				// Icon is included INSIDE the bg-filled area so backgrounds
				// align consistently across groups regardless of icon width.
				iconAndText := renderedIcon + headerStyle.Render(headerText)
				iconAndTextW := uniseg.StringWidth(stripAnsi(iconAndText))
				if iconAndTextW > restW {
					renderedIconW := uniseg.StringWidth(stripAnsi(renderedIcon))
					headerMaxW := restW - renderedIconW
					if headerMaxW < 1 {
						headerMaxW = 1
					}
					truncated := ""
					for _, r := range headerText {
						if lipgloss.Width(truncated+string(r)) > headerMaxW-1 {
							break
						}
						truncated += string(r)
					}
					headerText = truncated + "~"
					iconAndText = renderedIcon + headerStyle.Render(headerText)
				}
				if bg != "" {
					iconAndText = c.applyBackgroundFill(iconAndText, bg, restW)
				} else {
					iconAndText = lipgloss.NewStyle().Width(restW).Render(iconAndText)
				}

				// Render hamburger menu button with matching background
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bg != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, bg, menuBtnW)
				}
				s.WriteString(prefix + iconAndText + menuBtn + "\n")
			} else {
				// No windows - show header with group tree branch but no collapse icon
				prefix := " "
				prefixW := uniseg.StringWidth(prefix)
				menuBtnW := 2 // " ⋮"
				restW := width - prefixW - menuBtnW
				if restW < 1 {
					restW = 1
				}
				iconAndText := renderedIcon + headerStyle.Render(headerText)
				iconAndTextW := uniseg.StringWidth(stripAnsi(iconAndText))
				if iconAndTextW > restW {
					renderedIconW := uniseg.StringWidth(stripAnsi(renderedIcon))
					headerMaxW := restW - renderedIconW
					if headerMaxW < 1 {
						headerMaxW = 1
					}
					truncated := ""
					for _, r := range headerText {
						if lipgloss.Width(truncated+string(r)) > headerMaxW-1 {
							break
						}
						truncated += string(r)
					}
					headerText = truncated + "~"
					iconAndText = renderedIcon + headerStyle.Render(headerText)
				}
				if bg != "" {
					iconAndText = c.applyBackgroundFill(iconAndText, bg, restW)
				}

				// Render hamburger menu button with matching background
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bg != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, bg, menuBtnW)
				}
				s.WriteString(prefix + iconAndText + menuBtn + "\n")
			}
			currentLine++
		}

		if hasWindows {
			iconWidth := uniseg.StringWidth(stripAnsi(collapseStyle.Render(collapseIcon)))
			if iconWidth < 1 {
				iconWidth = 1
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    iconWidth,
				Action:    "toggle_group",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  iconWidth,
				EndCol:    width - 2,
				Action:    "group_header",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  width - 2,
				EndCol:    0,
				Action:    "group_menu",
				Target:    group.Name,
			})
		} else {
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    width - 2,
				Action:    "group_header",
				Target:    group.Name,
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: groupStartLine,
				EndLine:   currentLine - 1,
				StartCol:  width - 2,
				EndCol:    0,
				Action:    "group_menu",
				Target:    group.Name,
			})
		}

		if isCollapsed {
			continue
		}

		// Show windows
		numWindows := len(group.Windows)
		for wi, win := range group.Windows {
			// For daemon mode: window is active if its ID matches this renderer's clientID
			// clientID is the window ID that the renderer is displaying for
			isActive := (win.ID == clientID)
			isLastInGroup := wi == numWindows-1
			windowStartLine := currentLine

			// Choose colors - custom color overrides group theme
			var bgColor, fgColor string
			isTransparent := win.CustomColor == "transparent"

			if isTransparent {
				bgColor = ""
				if isActive {
					fgColor = theme.ActiveFg
					if fgColor == "" {
						fgColor = theme.Fg
					}
				} else {
					fgColor = inactiveFg
				}
			} else if win.CustomColor != "" {
				if isActive {
					bgColor = win.CustomColor
				} else {
					bgColor = grouping.ShadeColorByIndex(win.CustomColor, 1)
				}
				fgColor = "#ffffff"
			} else if isActive {
				bgColor = theme.ActiveBg
				if bgColor == "" {
					bgColor = theme.Bg
				}
				fgColor = theme.ActiveFg
				if fgColor == "" {
					fgColor = theme.Fg
				}
			} else {
				bgColor = theme.Bg
				fgColor = inactiveFg
			}
			// Build style
			style := lipgloss.NewStyle()
			if fgColor != "" {
				style = style.Foreground(lipgloss.Color(fgColor))
			}

			if isActive {
				style = style.Bold(true)
			}

			// Build alert indicator
			alertIcon := ""
			ind := c.config.Indicators

			if ind.Busy.Enabled && win.Busy {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Busy.Color))

				busyFrames := c.getBusyFrames()
				alertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
			} else if ind.Input.Enabled && win.Input {
				inputIcon := ind.Input.Icon
				if inputIcon == "" {
					inputIcon = "?"
				}
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Input.Color))

				if len(ind.Input.Frames) > 0 {
					alertIcon = alertStyle.Render(ind.Input.Frames[c.getSlowSpinnerFrame()%len(ind.Input.Frames)])
				} else {
					alertIcon = alertStyle.Render(inputIcon)
				}
			} else if !isActive {
				if ind.Bell.Enabled && win.Bell {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Bell))
				} else if ind.Activity.Enabled && win.Activity {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Activity))
				} else if ind.Silence.Enabled && win.Silence {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))

					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Silence))
				}
			}

			var indicatorPart string
			if alertIcon != "" {
				indicatorPart = alertIcon
			} else {
				indicatorPart = " "
			}

			// Window tree branch
			var treeBranch string
			if isLastInGroup {
				treeBranch = treeBranchLastChar
			} else {
				treeBranch = treeBranchChar
			}

			var contentPanes []tmux.Pane
			for _, pane := range win.Panes {
				if isAuxiliaryPane(pane) {
					continue
				}
				contentPanes = append(contentPanes, pane)
			}
			// Window collapse indicator if has panes
			hasPanes := len(contentPanes) > 1
			isWindowCollapsed := win.Collapsed
			var windowCollapseIcon string

			if hasPanes {
				if isWindowCollapsed {
					windowCollapseIcon = collapsedIcon
				} else {
					windowCollapseIcon = expandedIcon
				}
			}

			// Build tab content - use visual position for display (stable sequential
			// numbering that matches sidebar order regardless of tmux renumbering)
			// Display is 0-indexed to match tmux window indices
			displayName := win.Name
			if win.Icon != "" {
				displayName = win.Icon + " " + displayName
			}
			visualNum := c.windowVisualPos[win.ID]
			baseContent := fmt.Sprintf("%d. %s", visualNum, displayName)

			// Add pane count if collapsed
			if hasPanes && isWindowCollapsed {
				baseContent = fmt.Sprintf("%s (%d)", baseContent, len(contentPanes))
			}

			// Calculate widths
			// All windows: indicator(1) + branch first char(1) + [collapse icon or branch second char](1) = 3
			prefixWidth := 3
			menuBtnW := 2 // " ⋮"
			windowContentWidth := width - prefixWidth - menuBtnW

			// Truncate if needed
			contentText := baseContent
			if lipgloss.Width(contentText) > windowContentWidth {
				truncated := ""
				for _, r := range contentText {
					if lipgloss.Width(truncated+string(r)) > windowContentWidth-1 {
						break
					}
					truncated += string(r)
				}
				contentText = truncated + "~"
			}

			// Styles for window collapse icon
			windowCollapseStyle := lipgloss.NewStyle()
			if disclosureColor != "" {
				windowCollapseStyle = windowCollapseStyle.Foreground(lipgloss.Color(disclosureColor))
			}

			contentStyle := style.Copy()
			if bgColor != "" {
				contentStyle = contentStyle.Background(lipgloss.Color(bgColor))
			}

			// Render tab line
			{
				// Build prefix (indicator + tree branch) separately from content
				// so background color only applies to the content portion
				var prefix, content string
				if hasPanes {
					treeBranchRunes := []rune(treeBranch)
					treeBranchFirst := string(treeBranchRunes[0])
					prefix = indicatorPart + treeStyle.Render(treeBranchFirst) + windowCollapseStyle.Render(windowCollapseIcon)
					content = contentText
				} else if isActive {
					treeBranchRunes := []rune(treeBranch)
					treeBranchFirst := string(treeBranchRunes[0])

					var indicatorBg, indicatorFg string
					if activeIndBgConfig == "" || activeIndBgConfig == "auto" {
						if theme.ActiveIndicatorBg != "" {
							indicatorBg = theme.ActiveIndicatorBg
						} else {
							indicatorBg = theme.Bg
						}
					} else {
						indicatorBg = activeIndBgConfig
					}
					if activeIndFgConfig == "" || activeIndFgConfig == "auto" {
						if indicatorBg == "" || strings.EqualFold(indicatorBg, "transparent") {
							indicatorFg = fgColor
						} else {
							indicatorFg = indicatorBg
						}
					} else {
						indicatorFg = activeIndFgConfig
					}

					activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Bold(true)
					if indicatorBg != "" && !strings.EqualFold(indicatorBg, "transparent") {
						activeIndStyle = activeIndStyle.Background(lipgloss.Color(indicatorBg))
					}
					prefix = indicatorPart + treeStyle.Render(treeBranchFirst) + activeIndStyle.Render(c.getAnimatedActiveIndicator(activeIndicator))
					content = contentText
				} else {
					prefix = indicatorPart + treeStyle.Render(treeBranch)
					content = contentText
				}

				// Apply bg color from start of name to the right edge (minus menu button)
				prefixPlain := stripAnsi(prefix)
				prefixWidth := uniseg.StringWidth(prefixPlain)
				contentWidth := width - prefixWidth - menuBtnW
				if contentWidth < 0 {
					contentWidth = 0
				}

				contentRendered := style.Render(content)
				if bgColor != "" {
					r, g, b := hexToRGB(bgColor)
					bgEsc := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
					resetEsc := "\x1b[0m"
					// Re-inject bg after any ANSI resets so bg persists through style changes
					contentRendered = strings.ReplaceAll(contentRendered, resetEsc, resetEsc+bgEsc)
					contentPlain := stripAnsi(contentRendered)
					contentVisualWidth := uniseg.StringWidth(contentPlain)
					pad := contentWidth - contentVisualWidth
					if pad < 0 {
						pad = 0
					}
					contentRendered = bgEsc + contentRendered + strings.Repeat(" ", pad) + resetEsc
				}

				// Render hamburger menu button with matching background
				menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
				if bgColor != "" {
					menuBtn = c.applyBackgroundFill(menuBtn, bgColor, menuBtnW)
				}
				s.WriteString(prefix + contentRendered + menuBtn + "\n")
				currentLine++
			}

			// Record window region(s) for click handling
			// For windows with panes, split into three click regions:
			// 1. Left area (indicator + tree branch + collapse icon) -> toggle_panes
			// 2. Middle area (window name) -> select_window
			// 3. Right area (menu button) -> window_menu
			if hasPanes {
				collapseColEnd := 5 // covers indicator(1) + tree(2) + icon(1) + space(1)
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  0,
					EndCol:    collapseColEnd,
					Action:    "toggle_panes",
					Target:    strconv.Itoa(win.Index),
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  collapseColEnd,
					EndCol:    width - 2,
					Action:    "select_window",
					Target:    win.ID,
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  width - 2,
					EndCol:    0,
					Action:    "window_menu",
					Target:    win.ID,
				})
			} else {
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  0,
					EndCol:    width - 2,
					Action:    "select_window",
					Target:    win.ID,
				})
				regions = append(regions, daemon.ClickableRegion{
					StartLine: windowStartLine,
					EndLine:   currentLine - 1,
					StartCol:  width - 2,
					EndCol:    0,
					Action:    "window_menu",
					Target:    win.ID,
				})
			}

			// Show panes if window has multiple panes and is not collapsed
			if len(contentPanes) > 1 && !isWindowCollapsed {
				// Use inactiveFg for sidebar pane text (same as inactive windows)
				paneStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(inactiveFg))

				activePaneStyle := paneStyle
				if isActive {
					activePaneFg := c.getTextColorWithFallback("")
					if win.CustomColor == "" && theme.ActiveFg != "" {
						activePaneFg = theme.ActiveFg
					} else if win.CustomColor != "" {
						// Only use white for custom colors (they have dark backgrounds)
						activePaneFg = "#ffffff"
					}
					activePaneStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(activePaneFg)).
						Bold(true)
				}

				var treeContinue string
				if isLastInGroup {
					treeContinue = " "
				} else {
					treeContinue = treeStyle.Render(treeContinueChar)
				}

				numPanes := len(contentPanes)
				// Suppress non-AI busy icons when an AI pane is actively working
				anyAIBusyA := false
				for _, p := range contentPanes {
					if p.AIBusy {
						anyAIBusyA = true
						break
					}
				}
				for pi, pane := range contentPanes {
					isLastPane := pi == numPanes-1
					paneStartLine := currentLine

					var paneBranchChar string
					if isLastPane {
						for _, r := range treeBranchLastChar {
							paneBranchChar = string(r)
							break
						}
					} else {
						for _, r := range treeBranchChar {
							paneBranchChar = string(r)
							break
						}
					}

					paneNum := fmt.Sprintf("%d.%d", visualNum, pane.Index)
					paneLabel := pane.Command
					if pane.LockedTitle != "" {
						paneLabel = pane.LockedTitle
					} else if pane.Title != "" && pane.Title != pane.Command {
						paneLabel = pane.Title
					}
					paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

					paneIndentWidth := 5
					paneMenuW := 2
					paneContentWidth := width - paneIndentWidth - paneMenuW

					// Truncate using proper rune width (handles Unicode/emoji)
					if lipgloss.Width(paneText) > paneContentWidth {
						truncated := ""
						for _, r := range paneText {
							if lipgloss.Width(truncated+string(r)) > paneContentWidth-1 {
								break
							}
							truncated += string(r)
						}
						paneText = truncated + "~"
					}

					paneActiveIndicator := c.config.Sidebar.Colors.ActiveIndicator
					if paneActiveIndicator == "" {
						paneActiveIndicator = "█"
					}

					// Per-pane alert indicator (busy/input for multi-pane windows)
					paneAlertIcon := ""
					pInd := c.config.Indicators
					if pane.AIBusy && pInd.Busy.Enabled {
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
						busyFrames := c.getBusyFrames()
						paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
					} else if pane.AIInput && pInd.Input.Enabled {
						inputIcon := pInd.Input.Icon
						if inputIcon == "" {
							inputIcon = "?"
						}
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Input.Color))
						if len(pInd.Input.Frames) > 0 {
							paneAlertIcon = alertStyle.Render(pInd.Input.Frames[c.getSlowSpinnerFrame()%len(pInd.Input.Frames)])
						} else {
							paneAlertIcon = alertStyle.Render(inputIcon)
						}
					} else if pane.Busy && pInd.Busy.Enabled && !tmux.IsAITool(pane.Command) && !anyAIBusyA {
						// Non-AI pane with foreground process; suppress when AI is busy in same window
						alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
						busyFrames := c.getBusyFrames()
						paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
					}

					paneLeadChar := " "
					if paneAlertIcon != "" {
						paneLeadChar = paneAlertIcon
					}

					var paneLineBg string
					if bgColor != "" {
						paneLineBg = bgColor
					} else {
						paneLineBg = theme.Bg
					}

					// Build prefix (tree parts) and content separately
					// bg color extends from start of pane name to the right edge
					var panePrefix, paneContent string
					if pane.Active && isActive {
						var paneIndicatorBg, paneIndicatorFg string
						if activeIndBgConfig == "" || activeIndBgConfig == "auto" {
							if theme.ActiveIndicatorBg != "" {
								paneIndicatorBg = theme.ActiveIndicatorBg
							} else {
								paneIndicatorBg = theme.Bg
							}
						} else {
							paneIndicatorBg = activeIndBgConfig
						}
						if activeIndFgConfig == "" || activeIndFgConfig == "auto" {
							if paneIndicatorBg == "" || strings.EqualFold(paneIndicatorBg, "transparent") {
								paneIndicatorFg = c.getTextColorWithFallback("")
							} else {
								paneIndicatorFg = paneIndicatorBg
							}
						} else {
							paneIndicatorFg = activeIndFgConfig
						}
						paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Bold(true)
						if paneIndicatorBg != "" && !strings.EqualFold(paneIndicatorBg, "transparent") {
							paneIndStyle = paneIndStyle.Background(lipgloss.Color(paneIndicatorBg))
						}
						panePrefix = paneLeadChar + treeContinue + treeStyle.Render(" "+paneBranchChar) + paneIndStyle.Render(c.getAnimatedActiveIndicator(paneActiveIndicator))
						paneContent = activePaneStyle.Render(paneText)
					} else {
						panePrefix = paneLeadChar + treeContinue + treeStyle.Render(" "+paneBranchChar+treeConnectorChar)
						paneContent = paneStyle.Render(paneText)
					}

					// Apply bg color from start of pane name to right edge (minus buttons)
					panePrefixPlain := stripAnsi(panePrefix)
					panePrefixW := uniseg.StringWidth(panePrefixPlain)
					paneContentW := width - panePrefixW - paneMenuW
					if paneContentW < 0 {
						paneContentW = 0
					}

					if paneLineBg != "" {
						r, g, b := hexToRGB(paneLineBg)
						bgEsc := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
						resetEsc := "\x1b[0m"
						paneContent = strings.ReplaceAll(paneContent, resetEsc, resetEsc+bgEsc)
						paneContentPlain := stripAnsi(paneContent)
						paneContentVisualW := uniseg.StringWidth(paneContentPlain)
						panePad := paneContentW - paneContentVisualW
						if panePad < 0 {
							panePad = 0
						}
						paneContent = bgEsc + paneContent + strings.Repeat(" ", panePad) + resetEsc
					}

					var paneBtns string
					menuBtn := lipgloss.NewStyle().Foreground(lipgloss.Color(inactiveFg)).Render(" ⋮")
					paneBtns = menuBtn
					if paneLineBg != "" {
						paneBtns = c.applyBackgroundFill(paneBtns, paneLineBg, paneMenuW)
					}
					s.WriteString(panePrefix + paneContent + paneBtns + "\n")
					currentLine++

					regions = append(regions, daemon.ClickableRegion{
						StartLine: paneStartLine,
						EndLine:   currentLine - 1,
						StartCol:  0,
						EndCol:    width - 2,
						Action:    "select_pane",
						Target:    pane.ID,
					})
					regions = append(regions, daemon.ClickableRegion{
						StartLine: paneStartLine,
						EndLine:   currentLine - 1,
						StartCol:  width - 2,
						EndCol:    0,
						Action:    "pane_menu",
						Target:    pane.ID,
					})
				}

			}

		}

		if !isLastGroup {
			s.WriteString("\n")
			currentLine++
		}
	}

	return s.String(), regions
}

// generatePrefixModeContent creates a flat window list with group prefixes (e.g., "SD| WindowName")
// In this mode, windows are not grouped hierarchically, but panes still show tree structure
func (c *Coordinator) generatePrefixModeContent(clientID string, width, height int, treeBranchChar, treeBranchLastChar, treeContinueChar, treeConnectorChar, expandedIcon, collapsedIcon string, treeStyle lipgloss.Style, disclosureColor, activeIndicator, activeIndFgConfig, activeIndBgConfig string) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion
	currentLine := 0

	activeIndFgConf := activeIndFgConfig
	activeIndBgConf := activeIndBgConfig
	inactiveFg := c.getInactiveTextColorWithFallback(c.config.Sidebar.Colors.InactiveFg)

	// Collect all windows from all groups into a flat list
	type flatWindow struct {
		win        tmux.Window
		groupName  string
		groupTheme config.Theme
	}
	var allWindows []flatWindow
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			allWindows = append(allWindows, flatWindow{
				win:        win,
				groupName:  group.Name,
				groupTheme: group.Theme,
			})
		}
	}

	// Render each window with group prefix
	numWindows := len(allWindows)
	for wi, fw := range allWindows {
		win := fw.win
		groupName := fw.groupName
		theme := fw.groupTheme
		isLastWindow := wi == numWindows-1

		// For daemon mode: window is active if its ID matches this renderer's clientID
		isActive := (win.ID == clientID)
		windowStartLine := currentLine

		// Get group prefix (first 2-3 chars of group name)
		groupPrefix := ""
		if groupName != "Default" && groupName != "" {
			// Take first 2-3 chars or abbreviation
			if len(groupName) >= 3 {
				groupPrefix = groupName[:2] + "| "
			} else if len(groupName) > 0 {
				groupPrefix = groupName[:1] + "| "
			}
		}

		// Choose colors - custom color overrides group theme
		var bgColor, fgColor string
		isTransparent := win.CustomColor == "transparent"

		if isTransparent {
			bgColor = ""
			if isActive {
				fgColor = theme.ActiveFg
				if fgColor == "" {
					fgColor = theme.Fg
				}
			} else {
				fgColor = inactiveFg
			}
		} else if win.CustomColor != "" {
			if isActive {
				bgColor = win.CustomColor
			} else {
				bgColor = grouping.ShadeColorByIndex(win.CustomColor, 1)
			}
			// Custom colors typically have dark backgrounds, use white text
			fgColor = "#ffffff"
		} else if isActive {
			bgColor = theme.ActiveBg
			if bgColor == "" {
				bgColor = theme.Bg
			}
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			bgColor = theme.Bg
			fgColor = inactiveFg
		}

		// Build style
		style := lipgloss.NewStyle()
		if fgColor != "" {
			style = style.Foreground(lipgloss.Color(fgColor))
		}

		if isActive {
			style = style.Bold(true)
		}

		// Build alert indicator
		alertIcon := ""
		ind := c.config.Indicators

		if ind.Busy.Enabled && win.Busy {
			alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Busy.Color))

			busyFrames := c.getBusyFrames()
			alertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
		} else if ind.Input.Enabled && win.Input {
			inputIcon := ind.Input.Icon
			if inputIcon == "" {
				inputIcon = "?"
			}
			alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Input.Color))

			if len(ind.Input.Frames) > 0 {
				alertIcon = alertStyle.Render(ind.Input.Frames[c.getSlowSpinnerFrame()%len(ind.Input.Frames)])
			} else {
				alertIcon = alertStyle.Render(inputIcon)
			}
		} else if !isActive {
			if ind.Bell.Enabled && win.Bell {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Bell))
			} else if ind.Activity.Enabled && win.Activity {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Activity))
			} else if ind.Silence.Enabled && win.Silence {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))

				alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Silence))
			}
		}

		// Render indicator at far left
		var indicatorPart string
		if alertIcon != "" {
			indicatorPart = alertIcon
		} else {
			// Use empty space
			indicatorPart = " "
		}

		var contentPanes []tmux.Pane
		for _, pane := range win.Panes {
			if isAuxiliaryPane(pane) {
				continue
			}
			contentPanes = append(contentPanes, pane)
		}
		// Window collapse indicator if has panes
		hasPanes := len(contentPanes) > 1
		isWindowCollapsed := win.Collapsed
		var windowCollapseIcon string

		if hasPanes {
			if isWindowCollapsed {
				windowCollapseIcon = collapsedIcon
			} else {
				windowCollapseIcon = expandedIcon
			}
		}

		// Build tab content with group prefix
		// Display is 0-indexed to match tmux window indices
		displayName := win.Name
		if win.Icon != "" {
			displayName = win.Icon + " " + displayName
		}
		visualNum := c.windowVisualPos[win.ID]
		baseContent := fmt.Sprintf("%d. %s%s", visualNum, groupPrefix, displayName)

		// Add pane count if collapsed
		if hasPanes && isWindowCollapsed {
			baseContent = fmt.Sprintf("%s (%d)", baseContent, len(contentPanes))
		}

		// Calculate widths
		prefixWidth := 2 // indicator + space
		if hasPanes {
			prefixWidth += 2 // collapse icon + space
		}
		windowContentWidth := width - prefixWidth

		// Truncate if needed
		contentText := baseContent
		if lipgloss.Width(contentText) > windowContentWidth {
			truncated := ""
			for _, r := range contentText {
				if lipgloss.Width(truncated+string(r)) > windowContentWidth-1 {
					break
				}
				truncated += string(r)
			}
			contentText = truncated + "~"
		}

		// Styles for window collapse icon
		windowCollapseStyle := lipgloss.NewStyle()
		if disclosureColor != "" {
			windowCollapseStyle = windowCollapseStyle.Foreground(lipgloss.Color(disclosureColor))
		}

		// Render window line
		{
			windowLineStyle := lipgloss.NewStyle().Width(width)
			effectiveBg := bgColor
			if effectiveBg == "" {
				effectiveBg = theme.Bg
			}

			var lineContent string
			if hasPanes {
				lineContent = indicatorPart + " " + windowCollapseStyle.Render(windowCollapseIcon+" ") + style.Render(contentText)
			} else if isActive {
				var indicatorBg, indicatorFg string
				if activeIndBgConf == "" || activeIndBgConf == "auto" {
					if theme.ActiveIndicatorBg != "" {
						indicatorBg = theme.ActiveIndicatorBg
					} else {
						indicatorBg = theme.Bg
					}
				} else {
					indicatorBg = activeIndBgConf
				}
				if activeIndFgConf == "" || activeIndFgConf == "auto" {
					if indicatorBg == "" || strings.EqualFold(indicatorBg, "transparent") {
						indicatorFg = fgColor
					} else {
						indicatorFg = indicatorBg
					}
				} else {
					indicatorFg = activeIndFgConf
				}

				activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Bold(true)
				lineContent = indicatorPart + " " + activeIndStyle.Render(c.getAnimatedActiveIndicator(activeIndicator)) + style.Render(contentText)
			} else {
				lineContent = indicatorPart + "  " + style.Render(contentText)
			}
			renderedLine := windowLineStyle.Render(lineContent)
			if effectiveBg != "" {
				renderedLine = c.applyBackgroundFill(renderedLine, effectiveBg, width)
			}
			s.WriteString(renderedLine + "\n")
			currentLine++
		}

		// Record window region for click handling
		if hasPanes {
			collapseColEnd := 4 // indicator + space + icon + space
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				StartCol:  0,
				EndCol:    collapseColEnd,
				Action:    "toggle_panes",
				Target:    strconv.Itoa(win.Index),
			})
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				StartCol:  collapseColEnd,
				EndCol:    0,
				Action:    "select_window",
				Target:    win.ID,
			})
		} else {
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				Action:    "select_window",
				Target:    win.ID,
			})
		}

		// Show panes if window has multiple panes and is not collapsed
		// Panes still get hierarchy (tree structure) in prefix mode
		if len(contentPanes) > 1 && !isWindowCollapsed {
			// Use inactiveFg for sidebar pane text (same as inactive windows)
			paneStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(inactiveFg))

			activePaneStyle := paneStyle
			if isActive {
				var activePaneFg string
				if win.CustomColor == "" && theme.ActiveFg != "" {
					activePaneFg = theme.ActiveFg
				} else if win.CustomColor != "" {
					// Custom colors use white text (dark backgrounds)
					activePaneFg = "#ffffff"
				} else {
					// Fall back to theme-aware text color
					activePaneFg = c.getTextColorWithFallback("")
				}
				activePaneStyle = lipgloss.NewStyle().
					Foreground(lipgloss.Color(activePaneFg)).
					Bold(true)
			}

			numPanes := len(contentPanes)
			// Suppress non-AI busy icons when an AI pane is actively working
			anyAIBusyB := false
			for _, p := range contentPanes {
				if p.AIBusy {
					anyAIBusyB = true
					break
				}
			}
			for pi, pane := range contentPanes {
				isLastPane := pi == numPanes-1
				paneStartLine := currentLine

				var paneBranchChar string
				if isLastPane {
					for _, r := range treeBranchLastChar {
						paneBranchChar = string(r)
						break
					}
				} else {
					for _, r := range treeBranchChar {
						paneBranchChar = string(r)
						break
					}
				}

				paneNum := fmt.Sprintf("%d.%d", visualNum, pane.Index)
				paneLabel := pane.Command
				if pane.LockedTitle != "" {
					paneLabel = pane.LockedTitle
				} else if pane.Title != "" && pane.Title != pane.Command {
					paneLabel = pane.Title
				}
				paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

				paneIndentWidth := 5 // " " + space + branch + connector + connector
				paneContentWidth := width - paneIndentWidth

				// Truncate
				if lipgloss.Width(paneText) > paneContentWidth {
					truncated := ""
					for _, r := range paneText {
						if lipgloss.Width(truncated+string(r)) > paneContentWidth-1 {
							break
						}
						truncated += string(r)
					}
					paneText = truncated + "~"
				}

				paneActiveIndicator := c.config.Sidebar.Colors.ActiveIndicator
				if paneActiveIndicator == "" {
					paneActiveIndicator = "█"
				}

				// Per-pane alert indicator
				paneAlertIcon := ""
				pInd := c.config.Indicators
				if pane.AIBusy && pInd.Busy.Enabled {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
					busyFrames := c.getBusyFrames()
					paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
				} else if pane.AIInput && pInd.Input.Enabled {
					inputIcon := pInd.Input.Icon
					if inputIcon == "" {
						inputIcon = "?"
					}
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Input.Color))
					if len(pInd.Input.Frames) > 0 {
						paneAlertIcon = alertStyle.Render(pInd.Input.Frames[c.getSlowSpinnerFrame()%len(pInd.Input.Frames)])
					} else {
						paneAlertIcon = alertStyle.Render(inputIcon)
					}
				} else if pane.Busy && pInd.Busy.Enabled && !tmux.IsAITool(pane.Command) && !anyAIBusyB {
					// Non-AI pane with foreground process; suppress when AI is busy in same window
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(pInd.Busy.Color))
					busyFrames := c.getBusyFrames()
					paneAlertIcon = alertStyle.Render(busyFrames[c.getSlowSpinnerFrame()%len(busyFrames)])
				}

				paneLeadChar := " "
				if paneAlertIcon != "" {
					paneLeadChar = paneAlertIcon
				}

				var paneLineBg string
				if bgColor != "" {
					paneLineBg = bgColor
				} else {
					paneLineBg = theme.Bg
				}
				paneLineStyle := lipgloss.NewStyle().Background(lipgloss.Color(paneLineBg)).Width(width)

				if pane.Active && isActive {
					var paneIndicatorBg, paneIndicatorFg string
					if activeIndBgConf == "" || activeIndBgConf == "auto" {
						if theme.ActiveIndicatorBg != "" {
							paneIndicatorBg = theme.ActiveIndicatorBg
						} else {
							paneIndicatorBg = theme.Bg
						}
					} else {
						paneIndicatorBg = activeIndBgConf
					}
					if activeIndFgConf == "" || activeIndFgConf == "auto" {
						if paneIndicatorBg == "" || strings.EqualFold(paneIndicatorBg, "transparent") {
							paneIndicatorFg = c.getTextColorWithFallback("")
						} else {
							paneIndicatorFg = paneIndicatorBg
						}
					} else {
						paneIndicatorFg = activeIndFgConf
					}
					paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Bold(true)
					fullWidthPaneStyle := activePaneStyle.Width(paneContentWidth)
					lineContent := paneLeadChar + "  " + treeStyle.Render(paneBranchChar+treeConnectorChar) + paneIndStyle.Render(c.getAnimatedActiveIndicator(paneActiveIndicator)) + fullWidthPaneStyle.Render(paneText)
					renderedPane := paneLineStyle.Render(lineContent)
					if paneLineBg != "" {
						renderedPane = c.applyBackgroundFill(renderedPane, paneLineBg, width)
					}
					s.WriteString(renderedPane + "\n")
				} else {
					lineContent := paneLeadChar + "  " + treeStyle.Render(paneBranchChar+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText)
					renderedPane := paneLineStyle.Render(lineContent)
					if paneLineBg != "" {
						renderedPane = c.applyBackgroundFill(renderedPane, paneLineBg, width)
					}
					s.WriteString(renderedPane + "\n")
				}
				currentLine++

				// Record pane region for click handling
				regions = append(regions, daemon.ClickableRegion{
					StartLine: paneStartLine,
					EndLine:   currentLine - 1,
					Action:    "select_pane",
					Target:    pane.ID,
				})
			}
		}

		// Padding between windows (not after last)
		if !isLastWindow {
			s.WriteString("\n")
			currentLine++
		}
	}

	return s.String(), regions
}

// widgetEntry represents a single widget for the zone layout system.
// Widgets are sorted by zone (top/bottom) then priority within each zone.
type widgetEntry struct {
	name     string
	zone     string // "top" or "bottom"
	priority int
	content  string // pre-rendered content (may contain zone.Mark markers)
}

// collectWidgetEntries gathers all enabled widgets and action buttons into
// a sorted slice of widgetEntry, ready for zone-based rendering.
func (c *Coordinator) collectWidgetEntries(width int, skipPet bool) []widgetEntry {
	var entries []widgetEntry

	// Clock widget
	if c.config.Widgets.Clock.Enabled {
		pos := c.config.Widgets.Clock.Position
		if pos == "" {
			pos = "top"
		}
		entries = append(entries, widgetEntry{
			name:     "clock",
			zone:     pos,
			priority: c.config.Widgets.Clock.Priority,
			content:  constrainWidgetWidth(c.renderClockWidget(width), width),
		})
	}

	// Pet widget — skip when viewport is too small for all tabs
	if c.config.Widgets.Pet.Enabled && !skipPet {
		pos := c.config.Widgets.Pet.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "pet",
			zone:     pos,
			priority: c.config.Widgets.Pet.Priority,
			content:  c.renderPetWidget(width),
		})
	}

	// Git widget
	if c.config.Widgets.Git.Enabled {
		pos := c.config.Widgets.Git.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "git",
			zone:     pos,
			priority: c.config.Widgets.Git.Priority,
			content:  constrainWidgetWidth(c.renderGitWidget(width), width),
		})
	}

	// Session widget
	if c.config.Widgets.Session.Enabled {
		pos := c.config.Widgets.Session.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "session",
			zone:     pos,
			priority: c.config.Widgets.Session.Priority,
			content:  constrainWidgetWidth(c.renderSessionWidget(width), width),
		})
	}

	// Claude usage widget
	if c.config.Widgets.Claude.Enabled {
		pos := c.config.Widgets.Claude.Position
		if pos == "" {
			pos = "bottom"
		}
		entries = append(entries, widgetEntry{
			name:     "claude",
			zone:     pos,
			priority: c.config.Widgets.Claude.Priority,
			content:  constrainWidgetWidth(c.renderClaudeWidget(width), width),
		})
	}

	entries = append(entries, widgetEntry{
		name:     "nav_buttons",
		zone:     "bottom",
		priority: 9998,
		content:  c.renderNavButtons(width),
	})

	// Action buttons (new tab, new group, close, touch mode toggle)
	actionZone := c.config.Sidebar.ActionZone
	if actionZone == "" {
		actionZone = "bottom"
	}
	actionPriority := c.config.Sidebar.ActionPriority
	if actionPriority == 0 {
		actionPriority = 90
	}
	entries = append(entries, widgetEntry{
		name:     "action_buttons",
		zone:     actionZone,
		priority: actionPriority,
		content:  c.renderPinnedActionButtons(width),
	})

	// Sort by priority within each zone (stable sort preserves insertion order for equal priority)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].priority < entries[j].priority
	})

	return entries
}

// renderWidgetZone renders a list of widget entries into a single content string
// and extracts BubbleZone-based click regions. Positions are relative to the
// returned content (caller must offset them).
func (c *Coordinator) renderWidgetZone(entries []widgetEntry, width int) (string, []daemon.ClickableRegion) {
	if len(entries) == 0 {
		return "", nil
	}

	var s strings.Builder
	for _, entry := range entries {
		s.WriteString(entry.content)
	}

	rawContent := s.String()
	if rawContent == "" {
		return "", nil
	}

	// Scan for zone markers (BubbleZone)
	scannedContent := zone.Scan(rawContent)

	// Extract zone bounds for all known clickable areas
	knownZones := []string{
		// Pet zones
		"pet:drop_food", "pet:drop_yarn", "pet:clean_poop", "pet:pet_pet", "pet:ground",
		// Button zones
		"sidebar:new_tab", "sidebar:new_group", "sidebar:close_tab",
		// Sidebar zones
		"sidebar:shrink", "sidebar:grow",
		"sidebar:prev_window", "sidebar:next_window",
	}
	var regions []daemon.ClickableRegion
	for _, zoneID := range knownZones {
		if info := zone.Get(zoneID); info != nil && !info.IsZero() {
			parts := strings.SplitN(zoneID, ":", 2)
			if len(parts) == 2 {
				regions = append(regions, daemon.ClickableRegion{
					StartLine: info.StartY,
					EndLine:   info.EndY,
					StartCol:  info.StartX,
					EndCol:    info.EndX + 1, // Convert from inclusive to exclusive
					Action:    parts[1],
					Target:    parts[0],
				})
				coordinatorDebugLog.Printf("BubbleZone extracted: %s -> lines %d-%d, cols %d-%d (exclusive)",
					zoneID, info.StartY, info.EndY, info.StartX, info.EndX+1)
			}
		}
	}

	coordinatorDebugLog.Printf("BubbleZone: extracted %d widget regions from zone", len(regions))

	// Apply safety constraint to the clean content (after markers are stripped)
	scannedContent = constrainWidgetWidth(scannedContent, width)

	return scannedContent, regions
}

// generateWidgetZones renders all widgets into top and bottom zones,
// plus resize buttons that always appear at the very bottom.
// Returns: topContent, topRegions, bottomContent, bottomRegions
func (c *Coordinator) generateWidgetZones(width int, skipPet bool) (string, []daemon.ClickableRegion, string, []daemon.ClickableRegion) {
	entries := c.collectWidgetEntries(width, skipPet)

	// Split into top and bottom zones
	var topEntries, bottomEntries []widgetEntry
	for _, e := range entries {
		if e.zone == "top" {
			topEntries = append(topEntries, e)
		} else {
			bottomEntries = append(bottomEntries, e)
		}
	}

	// Render top zone
	topContent, topRegions := c.renderWidgetZone(topEntries, width)

	// Add resize buttons to bottom (always last)
	bottomEntries = append(bottomEntries, widgetEntry{
		name:     "resize_buttons",
		zone:     "bottom",
		priority: 9999,
		content:  c.renderSidebarResizeButtons(width),
	})

	// Render bottom zone
	bottomContent, bottomRegions := c.renderWidgetZone(bottomEntries, width)

	return topContent, topRegions, bottomContent, bottomRegions
}

// renderClockWidget renders the clock/date widget
func (c *Coordinator) renderClockWidget(width int) string {
	clock := c.config.Widgets.Clock
	now := time.Now()

	timeFormat := clock.Format
	if timeFormat == "" {
		timeFormat = "15:04:05"
	}

	// Use clock's Fg, fall back to background-aware default for visibility
	fgColor := c.getInactiveTextColorWithFallback(clock.Fg)
	style := lipgloss.NewStyle()
	if fgColor != "" {
		style = style.Foreground(lipgloss.Color(fgColor))
	}

	dividerStyle := lipgloss.NewStyle()
	dividerFg := c.getInactiveTextColorWithFallback(clock.DividerFg)
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}

	var result strings.Builder

	for i := 0; i < clock.MarginTop; i++ {
		result.WriteString("\n")
	}

	if clock.Divider != "" {
		dividerWidth := lipgloss.Width(clock.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < clock.PaddingTop; i++ {
		result.WriteString("\n")
	}

	timeStr := now.Format(timeFormat)
	timePadding := (width - lipgloss.Width(timeStr)) / 2
	if timePadding < 0 {
		timePadding = 0
	}
	result.WriteString(style.Render(strings.Repeat(" ", timePadding)+timeStr) + "\n")

	if clock.ShowDate {
		dateFormat := clock.DateFmt
		if dateFormat == "" {
			dateFormat = "Mon Jan 2"
		}
		dateStr := now.Format(dateFormat)
		datePadding := (width - lipgloss.Width(dateStr)) / 2
		if datePadding < 0 {
			datePadding = 0
		}
		result.WriteString(style.Render(strings.Repeat(" ", datePadding)+dateStr) + "\n")
	}

	for i := 0; i < clock.PaddingBot; i++ {
		result.WriteString("\n")
	}

	if clock.DividerBottom != "" {
		dividerWidth := lipgloss.Width(clock.DividerBottom)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.DividerBottom, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < clock.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderGitWidget renders git status widget
func (c *Coordinator) renderGitWidget(width int) string {
	git := c.config.Widgets.Git

	if !c.isGitRepo {
		return ""
	}

	// Fall back to background-aware default for visibility
	gitDividerFg := c.getInactiveTextColorWithFallback(git.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if gitDividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(gitDividerFg))
	}

	gitFg := c.getInactiveTextColorWithFallback(git.Fg)
	style := lipgloss.NewStyle()
	if gitFg != "" {
		style = style.Foreground(lipgloss.Color(gitFg))
	}

	var result strings.Builder

	for i := 0; i < git.MarginTop; i++ {
		result.WriteString("\n")
	}

	if git.Divider != "" {
		dividerWidth := lipgloss.Width(git.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(git.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	for i := 0; i < git.PaddingTop; i++ {
		result.WriteString("\n")
	}

	icon := ""
	branch := c.gitBranch

	// Build status first to know its width
	status := ""
	if c.gitDirty > 0 {
		status += fmt.Sprintf(" *%d", c.gitDirty)
	}
	if c.gitAhead > 0 {
		status += fmt.Sprintf(" ↑%d", c.gitAhead)
	}
	if c.gitBehind > 0 {
		status += fmt.Sprintf(" ↓%d", c.gitBehind)
	}

	// Calculate max branch width (accounting for icon, spacing, and status)
	prefix := fmt.Sprintf("  %s ", icon)
	maxBranch := width - lipgloss.Width(prefix) - lipgloss.Width(status)
	if maxBranch < 5 {
		maxBranch = 5
	}

	// Truncate branch using proper rune width
	if lipgloss.Width(branch) > maxBranch {
		truncated := ""
		for _, r := range branch {
			if lipgloss.Width(truncated+string(r)) > maxBranch-1 {
				break
			}
			truncated += string(r)
		}
		branch = truncated + "~"
	}

	result.WriteString(style.Render(prefix+branch+status) + "\n")

	for i := 0; i < git.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < git.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderSessionWidget renders the session info widget
func (c *Coordinator) renderSessionWidget(width int) string {
	sessionCfg := c.config.Widgets.Session
	if !sessionCfg.Enabled {
		return ""
	}

	style := sessionCfg.Style
	if style == "" {
		style = "nerd"
	}
	icons, ok := sessionIconsByStyle[style]
	if !ok {
		icons = sessionIconsByStyle["nerd"]
	}

	var result strings.Builder

	for i := 0; i < sessionCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := sessionCfg.Divider
	if divider == "" {
		divider = "─"
	}
	// Fall back to background-aware default for visibility
	sessDividerFg := c.getInactiveTextColorWithFallback(sessionCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if sessDividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(sessDividerFg))
	}
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < sessionCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	var parts []string

	// Determine foreground color with fallback chain
	sessFg := sessionCfg.SessionFg
	if sessFg == "" {
		sessFg = sessionCfg.Fg
	}
	sessFg = c.getInactiveTextColorWithFallback(sessFg)
	sessionStyle := lipgloss.NewStyle()
	if sessFg != "" {
		sessionStyle = sessionStyle.Foreground(lipgloss.Color(sessFg))
	}

	// Truncate session name if needed (reserve space for other stats)
	sessionName := c.sessionName
	maxNameWidth := width - 10 // Reserve space for other parts
	if maxNameWidth < 5 {
		maxNameWidth = 5
	}
	if lipgloss.Width(sessionName) > maxNameWidth {
		truncated := ""
		for _, r := range sessionName {
			if lipgloss.Width(truncated+string(r)) > maxNameWidth-1 {
				break
			}
			truncated += string(r)
		}
		sessionName = truncated + "~"
	}

	if icons.Session != "" {
		parts = append(parts, sessionStyle.Render(icons.Session+" "+sessionName))
	} else {
		parts = append(parts, sessionStyle.Render(sessionName))
	}

	if sessionCfg.ShowClients && c.sessionClients > 0 {
		clientStyle := lipgloss.NewStyle()
		if sessFg != "" {
			clientStyle = clientStyle.Foreground(lipgloss.Color(sessFg))
		}
		if icons.Clients != "" {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%s%d", icons.Clients, c.sessionClients)))
		} else {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%d", c.sessionClients)))
		}
	}

	if sessionCfg.ShowWindowCount {
		windowStyle := lipgloss.NewStyle()
		if sessFg != "" {
			windowStyle = windowStyle.Foreground(lipgloss.Color(sessFg))
		}
		if icons.Windows != "" {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%s%d", icons.Windows, c.windowCount)))
		} else {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%d", c.windowCount)))
		}
	}

	result.WriteString(strings.Join(parts, " ") + "\n")

	for i := 0; i < sessionCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < sessionCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderClaudeWidget renders the Claude Code API usage widget
func (c *Coordinator) renderClaudeWidget(width int) string {
	claudeCfg := c.config.Widgets.Claude
	if !claudeCfg.Enabled {
		return ""
	}

	var result strings.Builder

	// Margins and dividers
	for i := 0; i < claudeCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := claudeCfg.Divider
	if divider == "" {
		divider = "-"
	}
	dividerFg := c.getInactiveTextColorWithFallback(claudeCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < claudeCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Get Claude usage data
	dbPath := claudeCfg.DBPath
	if dbPath == "" {
		homeDir, _ := os.UserHomeDir()
		dbPath = filepath.Join(homeDir, ".claude", "__store.db")
	}

	todayCost, weekCost, monthCost, totalCost, msgCount := c.getClaudeUsageStats(dbPath)

	// Style for labels and values
	labelFg := c.getInactiveTextColorWithFallback(claudeCfg.Fg)
	costFg := claudeCfg.CostFg
	if costFg == "" {
		costFg = "#6bcb77" // Green for money
	}

	labelStyle := lipgloss.NewStyle()
	if labelFg != "" {
		labelStyle = labelStyle.Foreground(lipgloss.Color(labelFg))
	}
	costStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(costFg))

	// Icon based on style
	style := claudeCfg.Style
	if style == "" {
		style = "nerd"
	}
	icon := ""
	switch style {
	case "nerd":
		icon = " " // nf-md-robot (Claude)
	case "emoji":
		icon = "$ "
	case "ascii":
		icon = "[CC] "
	}

	// Header
	result.WriteString(labelStyle.Render(icon+"Claude") + "\n")

	// Show requested stats
	showToday := claudeCfg.ShowToday
	// Default to showing today if nothing specified
	if !showToday && !claudeCfg.ShowWeek && !claudeCfg.ShowMonth && !claudeCfg.ShowTotal {
		showToday = true
	}

	if showToday {
		result.WriteString(labelStyle.Render("  Today: ") + costStyle.Render(fmt.Sprintf("$%.2f", todayCost)) + "\n")
	}
	if claudeCfg.ShowWeek {
		result.WriteString(labelStyle.Render("  Week:  ") + costStyle.Render(fmt.Sprintf("$%.2f", weekCost)) + "\n")
	}
	if claudeCfg.ShowMonth {
		result.WriteString(labelStyle.Render("  Month: ") + costStyle.Render(fmt.Sprintf("$%.2f", monthCost)) + "\n")
	}
	if claudeCfg.ShowTotal {
		result.WriteString(labelStyle.Render("  Total: ") + costStyle.Render(fmt.Sprintf("$%.2f", totalCost)) + "\n")
	}
	if claudeCfg.ShowMessages {
		result.WriteString(labelStyle.Render(fmt.Sprintf("  Msgs:  %d", msgCount)) + "\n")
	}

	for i := 0; i < claudeCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < claudeCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// getClaudeUsageStats queries the Claude Code SQLite database for usage stats
// Uses sqlite3 command line tool to avoid adding a Go SQLite dependency
func (c *Coordinator) getClaudeUsageStats(dbPath string) (today, week, month, total float64, msgCount int) {
	// Check if database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return 0, 0, 0, 0, 0
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	weekStart := todayStart - int64((int(now.Weekday())+6)%7*86400) // Monday
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()

	// Build a single query that returns all stats
	query := fmt.Sprintf(`SELECT
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN timestamp >= %d THEN cost_usd ELSE 0 END), 0),
		COALESCE(SUM(cost_usd), 0),
		COUNT(*)
		FROM assistant_messages;`, todayStart, weekStart, monthStart)

	out, err := exec.Command("sqlite3", "-separator", "|", dbPath, query).Output()
	if err != nil {
		return 0, 0, 0, 0, 0
	}

	// Parse result: "today|week|month|total|count"
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 5 {
		today, _ = strconv.ParseFloat(parts[0], 64)
		week, _ = strconv.ParseFloat(parts[1], 64)
		month, _ = strconv.ParseFloat(parts[2], 64)
		total, _ = strconv.ParseFloat(parts[3], 64)
		msgCount, _ = strconv.Atoi(parts[4])
	}

	return today, week, month, total, msgCount
}

// constrainWidgetWidth ensures all lines in widget content don't exceed maxWidth
// This prevents widgets from overflowing the sidebar boundary
func constrainWidgetWidth(content string, maxWidth int) string {
	if maxWidth < 1 {
		return content
	}

	lines := strings.Split(content, "\n")
	var result strings.Builder
	hadOverflow := false

	for i, line := range lines {
		// Strip ANSI codes for width calculation (but keep them in output)
		stripped := stripAnsi(line)
		lineWidth := uniseg.StringWidth(stripped)

		if lineWidth > maxWidth {
			if !hadOverflow {
				coordinatorDebugLog.Printf("OVERFLOW DETECTED: line width %d > max %d", lineWidth, maxWidth)
				coordinatorDebugLog.Printf("  Line preview: %s", runewidth.Truncate(stripped, 50, "..."))
				hadOverflow = true
			}
			// Truncate line to maxWidth (accounting for ANSI codes)
			truncated := runewidth.Truncate(line, maxWidth, "")
			result.WriteString(truncated)
		} else {
			result.WriteString(line)
		}

		// Add newline except for last line
		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}

	return result.String()
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func safeRandRange(minInclusive, maxInclusive int) int {
	if maxInclusive < minInclusive {
		if maxInclusive < 0 {
			return 0
		}
		return maxInclusive
	}
	if maxInclusive == minInclusive {
		return minInclusive
	}
	return minInclusive + rand.Intn(maxInclusive-minInclusive+1)
}

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	// Simple regex to strip ANSI escape sequences
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
}

// clampSpriteX clamps a position so the sprite fits within the given width
func clampSpriteX(x int, sprite string, maxWidth int) int {
	spriteWidth := uniseg.StringWidth(sprite)
	if spriteWidth < 1 {
		spriteWidth = 1
	}
	maxX := maxWidth - spriteWidth
	if maxX < 0 {
		maxX = 0
	}
	if x < 0 {
		return x // preserve negative (hidden) positions
	}
	if x > maxX {
		return maxX
	}
	return x
}

// placeSprite adds a sprite to the map with proper position clamping
// Returns the clamped X position
func placeSprite(sprites map[int]string, x int, sprite string, maxWidth int) int {
	clampedX := clampSpriteX(x, sprite, maxWidth)
	if clampedX >= 0 && clampedX < maxWidth {
		sprites[clampedX] = sprite
	}
	return clampedX
}

// renderStatusBar creates a visual bar representation of a 0-100 value
// Uses block characters: filled (▓) and empty (░)
func renderStatusBar(value int, segments int) string {
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	filled := (value * segments) / 100
	empty := segments - filled

	bar := ""
	for i := 0; i < filled; i++ {
		bar += "▓"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}
	return bar
}

// colorStatusBar applies color to a status bar based on the value level
// Red (<30), Yellow (30-60), Green (>60)
func colorStatusBar(bar string, value int) string {
	var color string
	if value < 30 {
		color = "#ff6b6b" // Red - critical
	} else if value < 60 {
		color = "#ffd93d" // Yellow - warning
	} else {
		color = "#6bcb77" // Green - good
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(bar)
}

// buildSpriteRow builds a row with sprites placed at their positions
// Fills remaining space with the filler character
func buildSpriteRow(sprites map[int]string, filler string, totalWidth int) string {
	var builder strings.Builder
	fillerWidth := uniseg.StringWidth(filler)
	if fillerWidth < 1 {
		fillerWidth = 1
	}

	col := 0
	for col < totalWidth {
		if sprite, hasSprite := sprites[col]; hasSprite {
			spriteWidth := uniseg.StringWidth(sprite)
			if spriteWidth < 1 {
				spriteWidth = 1
			}
			// Only place sprite if it fits within bounds
			if col+spriteWidth <= totalWidth {
				builder.WriteString(sprite)
				col += spriteWidth
			} else {
				// Doesn't fit, use filler
				builder.WriteString(filler)
				col += fillerWidth
			}
		} else {
			builder.WriteString(filler)
			col += fillerWidth
		}
	}
	return builder.String()
}

// buildAirRow builds an air row (for Y=1 or Y=2) with proper width accounting for wide emojis
// sprites is a map of column position -> sprite string
// safePlayWidth is the total width available for the row
func buildAirRow(sprites map[int]string, safePlayWidth int) string {
	return buildSpriteRow(sprites, " ", safePlayWidth)
}

// renderPetWidget renders the pet tamagotchi widget
// Layout:
//   - Divider
//   - Food icon (clickable)
//   - Divider
//   - Thought bubble
//   - Divider
//   - Play area (3 lines: high air, low air, ground)
//   - Divider
//   - Stats: hunger | happiness | life
func (c *Coordinator) renderPetWidget(width int) string {
	petCfg := c.config.Widgets.Pet
	if !petCfg.Enabled {
		return ""
	}

	style := petCfg.Style
	if style == "" {
		style = "emoji"
	}
	sprites, ok := petSpritesByStyle[style]
	if !ok {
		sprites = petSpritesByStyle["emoji"]
	}

	// Apply config icon overrides (config takes priority over style preset)
	icons := petCfg.Icons
	if icons.Idle != "" {
		sprites.Idle = icons.Idle
	}
	if icons.Walking != "" {
		sprites.Walking = icons.Walking
	}
	if icons.Jumping != "" {
		sprites.Jumping = icons.Jumping
	}
	if icons.Playing != "" {
		sprites.Playing = icons.Playing
	}
	if icons.Eating != "" {
		sprites.Eating = icons.Eating
	}
	if icons.Sleeping != "" {
		sprites.Sleeping = icons.Sleeping
	}
	if icons.Happy != "" {
		sprites.Happy = icons.Happy
	}
	if icons.Hungry != "" {
		sprites.Hungry = icons.Hungry
	}
	if icons.Yarn != "" {
		sprites.Yarn = icons.Yarn
	}
	if icons.Food != "" {
		sprites.Food = icons.Food
	}
	if icons.Poop != "" {
		sprites.Poop = icons.Poop
	}
	if icons.Thought != "" {
		sprites.Thought = icons.Thought
	}
	if icons.Heart != "" {
		sprites.Heart = icons.Heart
	}
	if icons.Life != "" {
		sprites.Life = icons.Life
	}
	if icons.HungerIcon != "" {
		sprites.HungerIcon = icons.HungerIcon
	}
	if icons.HappyIcon != "" {
		sprites.HappyIcon = icons.HappyIcon
	}
	if icons.SadIcon != "" {
		sprites.SadIcon = icons.SadIcon
	}
	if icons.Ground != "" {
		sprites.Ground = icons.Ground
	}
	if icons.Blood != "" {
		sprites.Blood = icons.Blood
	}

	petSprite := sprites.Idle
	switch c.pet.State {
	case "walking":
		petSprite = sprites.Walking
	case "jumping":
		petSprite = sprites.Jumping
	case "playing":
		petSprite = sprites.Playing
	case "eating":
		petSprite = sprites.Eating
	case "sleeping":
		petSprite = sprites.Sleeping
	case "happy":
		petSprite = sprites.Happy
	case "hungry":
		petSprite = sprites.Hungry
	case "shooting":
		petSprite = sprites.Idle
	case "dead":
		petSprite = sprites.Dead
	}
	// Dead overrides everything
	if c.pet.IsDead {
		petSprite = sprites.Dead
	} else if c.pet.Hunger < 30 {
		petSprite = sprites.Hungry
	}
	if petSprite == "" {
		petSprite = "🐱"
	}

	var result strings.Builder
	currentLine := 0 // Track line offsets for click detection

	for i := 0; i < petCfg.MarginTop; i++ {
		result.WriteString("\n")
		currentLine++
	}

	// Divider style - fall back to sidebar's InactiveFg for visibility
	divider := petCfg.Divider
	if divider == "" {
		divider = "─"
	}
	dividerFg := c.getInactiveTextColorWithFallback(petCfg.DividerFg)
	dividerStyle := lipgloss.NewStyle()
	if dividerFg != "" {
		dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
	}
	renderDivider := func() string {
		dividerWidth := uniseg.StringWidth(divider)
		if dividerWidth > 0 {
			repeatCount := (width - 1) / dividerWidth
			if repeatCount < 1 {
				repeatCount = 1
			}
			return dividerStyle.Render(strings.Repeat(divider, repeatCount)) + "\n"
		}
		return ""
	}

	// Top divider
	result.WriteString(renderDivider())
	currentLine++

	// Food icon (clickable to drop food) - track line for click detection
	c.petLayout.FeedLine = currentLine
	petFg := c.getInactiveTextColorWithFallback(petCfg.Fg)
	playStyle := lipgloss.NewStyle()
	if petFg != "" {
		playStyle = playStyle.Foreground(lipgloss.Color(petFg))
	}
	if petCfg.Bg != "" && !strings.EqualFold(petCfg.Bg, "transparent") {
		playStyle = playStyle.Background(lipgloss.Color(petCfg.Bg))
	}
	foodStyle := lipgloss.NewStyle()
	if petFg != "" {
		foodStyle = foodStyle.Foreground(lipgloss.Color(petFg))
	}
	foodIcon := zone.Mark("pet:drop_food", foodStyle.Render(sprites.Food+" Feed"))
	result.WriteString(foodIcon + "\n")
	currentLine++

	// Divider
	result.WriteString(renderDivider())
	currentLine++

	for i := 0; i < petCfg.PaddingTop; i++ {
		result.WriteString("\n")
		currentLine++
	}

	playWidth := width
	if playWidth < 5 {
		playWidth = 5
	}

	// Thought bubble with marquee
	thought := c.pet.LastThought
	if thought == "" {
		thought = "chillin'."
	}
	thoughtStyle := lipgloss.NewStyle()
	if petFg != "" {
		thoughtStyle = thoughtStyle.Foreground(lipgloss.Color(petFg))
	}
	maxThoughtWidth := playWidth - 4
	if maxThoughtWidth < 5 {
		maxThoughtWidth = 5
	}
	thoughtWidth := uniseg.StringWidth(thought)
	displayThought := thought
	if thoughtWidth > maxThoughtWidth {
		scrollText := thought + "   " + thought
		scrollRunes := []rune(scrollText)
		startIdx := c.pet.ThoughtScroll % len(scrollRunes)
		visible := ""
		visWidth := 0
		for i := startIdx; i < len(scrollRunes) && visWidth < maxThoughtWidth; i++ {
			r := scrollRunes[i]
			rw := runewidth.RuneWidth(r)
			if visWidth+rw > maxThoughtWidth {
				break // Don't add partial wide char
			}
			visible += string(r)
			visWidth += rw
		}
		displayThought = visible
	}
	// Add "asking" request bubbles when needs are critical
	var requestBubble string
	if c.pet.IsDead {
		requestBubble = "💀"
	} else if len(c.pet.PoopPositions) > 0 {
		requestBubble = "🧹?" // Asking for cleanup
	} else if c.pet.Hunger < 20 {
		requestBubble = "🍖?" // Asking for food (urgent)
	} else if c.pet.Happiness < 20 {
		requestBubble = "🧶?" // Asking for play (urgent)
	} else if c.pet.Hunger < 40 {
		requestBubble = "🍖" // Would like food
	} else if c.pet.Happiness < 40 {
		requestBubble = "🧶" // Would like play
	}

	thoughtLine := sprites.Thought + " " + displayThought
	if requestBubble != "" {
		// Show request bubble at the end of thought line
		thoughtLine = requestBubble + " " + thoughtLine
	}
	result.WriteString(thoughtStyle.Render(thoughtLine) + "\n")
	currentLine++

	// Divider before play area
	result.WriteString(renderDivider())
	currentLine++

	// Get positions, clamped to width accounting for sprite widths
	// Use width - 1 to match divider width for visual consistency
	safePlayWidth := playWidth - 1
	c.petLayout.PlayWidth = safePlayWidth

	// === ADVENTURE MODE RENDERING ===
	// If adventure is active, render adventure play area instead of normal one
	if c.pet.Adventure.Active {
		highAirLine, lowAirLine, groundContent := c.renderAdventurePlayArea(safePlayWidth, petSprite, sprites)

		c.petLayout.HighAirLine = currentLine
		result.WriteString(zone.Mark("pet:air_high", playStyle.Render(highAirLine)) + "\n")
		currentLine++

		c.petLayout.LowAirLine = currentLine
		result.WriteString(zone.Mark("pet:air_low", playStyle.Render(lowAirLine)) + "\n")
		currentLine++

		c.petLayout.GroundLine = currentLine
		groundLine := zone.Mark("pet:ground", playStyle.Render(groundContent))
		result.WriteString(groundLine + "\n")
		currentLine++

		// Divider before stats
		result.WriteString(renderDivider())
		currentLine++

		// Stats line: hunger | happiness with visual bars
		statusStyle := lipgloss.NewStyle()
		if petFg != "" {
			statusStyle = statusStyle.Foreground(lipgloss.Color(petFg))
		}
		hungerIcon := sprites.HungerIcon
		happyIcon := sprites.HappyIcon
		if c.pet.Happiness < 30 {
			happyIcon = sprites.SadIcon
		}
		hungerBar := renderStatusBar(c.pet.Hunger, 5)
		happyBar := renderStatusBar(c.pet.Happiness, 5)
		hungerBarStyled := colorStatusBar(hungerBar, c.pet.Hunger)
		happyBarStyled := colorStatusBar(happyBar, c.pet.Happiness)
		statusLine := fmt.Sprintf("%s%s %s%s", hungerIcon, hungerBarStyled, happyIcon, happyBarStyled)
		result.WriteString(statusStyle.Render(statusLine) + "\n")
		currentLine++

		c.petLayout.WidgetHeight = currentLine

		for i := 0; i < petCfg.PaddingBot; i++ {
			result.WriteString("\n")
		}
		if petCfg.DividerBottom != "" {
			dividerStyle := lipgloss.NewStyle()
			if dividerFg != "" {
				dividerStyle = dividerStyle.Foreground(lipgloss.Color(dividerFg))
			}
			divWidth := uniseg.StringWidth(petCfg.DividerBottom)
			if divWidth > 0 {
				repeatCount := (width - 1) / divWidth
				if repeatCount < 1 {
					repeatCount = 1
				}
				result.WriteString(dividerStyle.Render(strings.Repeat(petCfg.DividerBottom, repeatCount)) + "\n")
			}
		}
		for i := 0; i < petCfg.MarginBot; i++ {
			result.WriteString("\n")
		}
		return result.String()
	}

	// === NORMAL PLAY AREA RENDERING ===
	// Get raw positions
	petX := c.pet.Pos.X
	if petX < 0 {
		petX = safePlayWidth / 2
	}
	petY := c.pet.Pos.Y

	yarnX := c.pet.YarnPos.X
	yarnY := c.pet.YarnPos.Y

	foodX := c.pet.FoodItem.X
	foodY := c.pet.FoodItem.Y

	// Clamp all positions to fit their sprites within bounds
	petX = clampSpriteX(petX, petSprite, safePlayWidth)
	yarnX = clampSpriteX(yarnX, sprites.Yarn, safePlayWidth)
	foodX = clampSpriteX(foodX, sprites.Food, safePlayWidth)

	// Line 1: High air (Y=2) - build with proper width accounting
	coordinatorDebugLog.Printf("Pet render: petX=%d, petY=%d, yarnX=%d, yarnY=%d, foodX=%d, foodY=%d, safePlayWidth=%d, petSprite=%q",
		petX, petY, yarnX, yarnY, foodX, foodY, safePlayWidth, petSprite)
	highAirSprites := make(map[int]string)
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 2 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			highAirSprites[item.Pos.X] = item.Emoji
		}
	}
	if petY >= 2 && petX >= 0 && petX < safePlayWidth {
		highAirSprites[petX] = petSprite
	}
	if yarnY >= 2 && yarnX >= 0 && yarnX < safePlayWidth {
		highAirSprites[yarnX] = sprites.Yarn
	}
	if foodY >= 2 && foodX >= 0 && foodX < safePlayWidth {
		highAirSprites[foodX] = sprites.Food
	}
	highAirLine := buildAirRow(highAirSprites, safePlayWidth)
	highAirWidth := uniseg.StringWidth(highAirLine)
	coordinatorDebugLog.Printf("High air: sprites=%v, line=%q (len=%d, runewidth=%d)", highAirSprites, highAirLine, len(highAirLine), highAirWidth)
	if highAirWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: High air row width mismatch! expected=%d, actual=%d", safePlayWidth, highAirWidth)
	}
	c.petLayout.HighAirLine = currentLine
	result.WriteString(zone.Mark("pet:air_high", playStyle.Render(highAirLine)) + "\n")
	currentLine++

	// Line 2: Low air (Y=1) - build with proper width accounting
	lowAirSprites := make(map[int]string)
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 1 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			lowAirSprites[item.Pos.X] = item.Emoji
		}
	}
	if petY == 1 && petX >= 0 && petX < safePlayWidth {
		lowAirSprites[petX] = petSprite
	}
	if yarnY == 1 && yarnX >= 0 && yarnX < safePlayWidth {
		lowAirSprites[yarnX] = sprites.Yarn
	}
	if foodY == 1 && foodX >= 0 && foodX < safePlayWidth {
		lowAirSprites[foodX] = sprites.Food
	}
	lowAirLine := buildAirRow(lowAirSprites, safePlayWidth)
	lowAirWidth := uniseg.StringWidth(lowAirLine)
	coordinatorDebugLog.Printf("Low air: sprites=%v, line=%q (len=%d, runewidth=%d)", lowAirSprites, lowAirLine, len(lowAirLine), lowAirWidth)
	if lowAirWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: Low air row width mismatch! expected=%d, actual=%d", safePlayWidth, lowAirWidth)
	}
	c.petLayout.LowAirLine = currentLine
	result.WriteString(zone.Mark("pet:air_low", playStyle.Render(lowAirLine)) + "\n")
	currentLine++

	// Line 3: Ground (Y=0) - single clickable zone, action determined by click position
	// Build ground row with proper width accounting for wide emojis
	groundChar := "·"
	if len(sprites.Ground) > 0 {
		groundChar = sprites.Ground
	}
	groundCharWidth := uniseg.StringWidth(groundChar)
	if groundCharWidth < 1 {
		groundCharWidth = 1
	}

	// Map of positions to sprites (position -> sprite string)
	// Each position represents a display column, not a rune slot
	groundSprites := make(map[int]string)

	// Place floating items
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 0 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			groundSprites[item.Pos.X] = item.Emoji
		}
	}

	// Place yarn
	if yarnY == 0 && yarnX >= 0 && yarnX < safePlayWidth {
		groundSprites[yarnX] = sprites.Yarn
	}

	// Place food
	if foodY == 0 && foodX >= 0 && foodX < safePlayWidth {
		groundSprites[foodX] = sprites.Food
	}

	// Place poops (clamped to fit within width)
	for _, poopX := range c.pet.PoopPositions {
		placeSprite(groundSprites, poopX, sprites.Poop, safePlayWidth)
	}

	// Place mouse (only if present - MousePos.X >= 0 means mouse exists)
	if c.pet.MousePos.X >= 0 {
		placeSprite(groundSprites, c.pet.MousePos.X, sprites.Mouse, safePlayWidth)
	}

	// Place cat on top (overwrites anything at that position)
	// When sleeping, cat curls up in bottom left corner with zzz
	if petY == 0 {
		if c.pet.State == "sleeping" {
			placeSprite(groundSprites, 0, "💤", safePlayWidth)
		} else {
			placeSprite(groundSprites, petX, petSprite, safePlayWidth)
		}
	}

	// Build the ground row using helper
	c.petLayout.GroundLine = currentLine
	groundContent := buildSpriteRow(groundSprites, groundChar, safePlayWidth)
	actualWidth := uniseg.StringWidth(groundContent)
	coordinatorDebugLog.Printf("Ground: width=%d, content=%q (len=%d bytes, runewidth=%d)",
		safePlayWidth, groundContent, len(groundContent), actualWidth)
	if actualWidth != safePlayWidth {
		coordinatorDebugLog.Printf("WARNING: Ground row width mismatch! expected=%d, actual=%d", safePlayWidth, actualWidth)
	}
	groundLine := zone.Mark("pet:ground", playStyle.Render(groundContent))
	result.WriteString(groundLine + "\n")
	currentLine++

	// Divider before stats
	result.WriteString(renderDivider())
	currentLine++

	// Stats line: hunger | happiness with visual bars
	statusStyle := lipgloss.NewStyle()
	if petFg != "" {
		statusStyle = statusStyle.Foreground(lipgloss.Color(petFg))
	}
	hungerIcon := sprites.HungerIcon
	happyIcon := sprites.HappyIcon
	if c.pet.Happiness < 30 {
		happyIcon = sprites.SadIcon
	}

	// Visual status bars (5 segments each)
	hungerBar := renderStatusBar(c.pet.Hunger, 5)
	happyBar := renderStatusBar(c.pet.Happiness, 5)

	// Color bars based on level (red if critical, yellow if low, green if good)
	hungerBarStyled := colorStatusBar(hungerBar, c.pet.Hunger)
	happyBarStyled := colorStatusBar(happyBar, c.pet.Happiness)

	statusLine := fmt.Sprintf("%s%s %s%s", hungerIcon, hungerBarStyled, happyIcon, happyBarStyled)
	result.WriteString(statusStyle.Render(statusLine) + "\n")
	currentLine++

	// Debug bar (if enabled)
	if petCfg.DebugBar {
		result.WriteString(renderDivider())
		currentLine++
		debugLines := c.renderDebugBar(safePlayWidth)
		for i, line := range debugLines {
			result.WriteString(line + "\n")
			if i == 0 {
				c.petLayout.DebugLine1 = currentLine
			} else if i == 1 {
				c.petLayout.DebugLine2 = currentLine
			}
			currentLine++
		}
	}

	// Store total widget height for click detection
	c.petLayout.WidgetHeight = currentLine

	coordinatorDebugLog.Printf("Pet layout updated: Feed=%d, HighAir=%d, LowAir=%d, Ground=%d, PlayWidth=%d, Height=%d, Debug1=%d, Debug2=%d",
		c.petLayout.FeedLine, c.petLayout.HighAirLine, c.petLayout.LowAirLine,
		c.petLayout.GroundLine, c.petLayout.PlayWidth, c.petLayout.WidgetHeight,
		c.petLayout.DebugLine1, c.petLayout.DebugLine2)

	// Pet touch buttons removed - using touch input on pet area instead
	// Feed button at top of widget remains for touch access

	return result.String()
}

// renderDebugBar renders the 2-line debug bar for pet widget testing
// Line 1: DBG <state> H:<hunger> F:<food> [adv][slp][die][poo][mse][yrn]
// Line 2: trg:<category> [<<][>>] [H+][H-][F+][F-]
func (c *Coordinator) renderDebugBar(width int) []string {
	// Line 1: Status + Mode Triggers
	state := c.pet.State
	if c.pet.IsDead {
		state = "dead"
	}
	if len(state) > 4 {
		state = state[:4]
	}

	var line1 string
	if width >= 50 {
		// Full layout
		line1 = fmt.Sprintf("DBG %s H:%d F:%d [adv][slp][die][poo][mse][yrn]",
			state, c.pet.Happiness, c.pet.Hunger)
	} else if width >= 35 {
		// Compact: shorter stat names
		line1 = fmt.Sprintf("%s H%d F%d [adv][slp][die][poo]",
			state, c.pet.Happiness, c.pet.Hunger)
	} else {
		// Minimal: just state and key buttons
		line1 = fmt.Sprintf("%s [adv][slp][die]", state)
	}

	// Line 2: Thought Controls + Stats
	category := "idle"
	if c.pet.DebugThoughtIdx >= 0 && c.pet.DebugThoughtIdx < len(debugThoughtCategories) {
		category = debugThoughtCategories[c.pet.DebugThoughtIdx]
	}
	// Truncate category to 5 chars for display
	if len(category) > 5 {
		category = category[:5]
	}

	var line2 string
	if width >= 35 {
		line2 = fmt.Sprintf("trg:%s [<<][>>] [H+][H-][F+][F-]", category)
	} else {
		line2 = fmt.Sprintf("%s [<<][>>] [H+][F+]", category)
	}

	return []string{line1, line2}
}

// handleDebugBarClick handles clicks on the debug bar
// Returns true if click was handled
func (c *Coordinator) handleDebugBarClick(clientID string, clickX, clickY int) bool {
	layout := c.petLayout

	if clickX < 0 {
		return false
	}

	clientWidth := c.getClientWidth(clientID)
	safeWidth := clientWidth - 1
	if safeWidth < 5 {
		safeWidth = 5
	}
	lines := c.renderDebugBar(safeWidth)
	if len(lines) < 2 {
		return false
	}
	line1 := lines[0]
	line2 := lines[1]

	findTokenBounds := func(line, token string) (int, int, bool) {
		idx := strings.Index(line, token)
		if idx < 0 {
			return 0, 0, false
		}
		start := uniseg.StringWidth(line[:idx])
		end := start + uniseg.StringWidth(token)
		return start, end, true
	}
	clickedToken := func(line, token string) bool {
		start, end, ok := findTokenBounds(line, token)
		return ok && clickX >= start && clickX < end
	}

	// Determine which debug line was clicked
	if clickY == layout.DebugLine1 {
		if clickedToken(line1, "[adv]") {
			coordinatorDebugLog.Printf("Debug bar: [adv] clicked, starting adventure")
			c.stateMu.Lock()
			c.startAdventure(safeWidth)
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line1, "[slp]") {
			coordinatorDebugLog.Printf("Debug bar: [slp] clicked, toggling sleep")
			c.stateMu.Lock()
			if c.pet.State == "sleeping" {
				c.pet.State = "idle"
				c.pet.LastThought = randomThought("wakeup")
			} else {
				c.pet.State = "sleeping"
				c.pet.LastThought = randomThought("sleepy")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[die]") {
			coordinatorDebugLog.Printf("Debug bar: [die] clicked, toggling death")
			c.stateMu.Lock()
			if c.pet.IsDead {
				c.pet.IsDead = false
				c.pet.State = "idle"
				c.pet.Hunger = 50
				c.pet.Happiness = 50
				c.pet.LastThought = "I'm back!"
			} else {
				c.pet.IsDead = true
				c.pet.State = "dead"
				c.pet.DeathTime = time.Now()
				c.pet.LastThought = randomThought("dead")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[poo]") {
			coordinatorDebugLog.Printf("Debug bar: [poo] clicked, spawning poop")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			poopX := safeRandRange(0, w-2)
			c.pet.PoopPositions = append(c.pet.PoopPositions, poopX)
			c.pet.LastThought = randomThought("poop")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[mse]") {
			coordinatorDebugLog.Printf("Debug bar: [mse] clicked, spawning mouse")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			c.pet.MousePos = pos2D{X: safeRandRange(0, w-2), Y: 0}
			c.pet.MouseAppearsAt = time.Time{} // Clear timer
			c.pet.LastThought = randomThought("mouse_spot")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line1, "[yrn]") {
			coordinatorDebugLog.Printf("Debug bar: [yrn] clicked, spawning yarn")
			c.stateMu.Lock()
			w := c.getClientWidth(clientID)
			if w < 3 {
				w = 3
			}
			c.pet.YarnPos = pos2D{X: safeRandRange(0, w-2), Y: 0}
			c.pet.YarnExpiresAt = time.Now().Add(30 * time.Second)
			c.pet.YarnPushCount = 0
			c.pet.LastThought = randomThought("yarn")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		return false
	}

	if clickY == layout.DebugLine2 {
		firstTokenStart := -1
		for _, tok := range []string{"[<<]", "[>>]", "[H+]", "[H-]", "[F+]", "[F-]"} {
			if s, _, ok := findTokenBounds(line2, tok); ok {
				if firstTokenStart == -1 || s < firstTokenStart {
					firstTokenStart = s
				}
			}
		}
		if firstTokenStart == -1 {
			firstTokenStart = uniseg.StringWidth(line2)
		}
		if clickX >= 0 && clickX < firstTokenStart {
			coordinatorDebugLog.Printf("Debug bar: trg clicked, triggering thought")
			c.stateMu.Lock()
			category := "idle"
			if c.pet.DebugThoughtIdx >= 0 && c.pet.DebugThoughtIdx < len(debugThoughtCategories) {
				category = debugThoughtCategories[c.pet.DebugThoughtIdx]
			}
			c.pet.LastThought = randomThought(category)
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[<<]") {
			coordinatorDebugLog.Printf("Debug bar: [<<] clicked, previous category")
			c.stateMu.Lock()
			c.pet.DebugThoughtIdx--
			if c.pet.DebugThoughtIdx < 0 {
				c.pet.DebugThoughtIdx = len(debugThoughtCategories) - 1
			}
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line2, "[>>]") {
			coordinatorDebugLog.Printf("Debug bar: [>>] clicked, next category")
			c.stateMu.Lock()
			c.pet.DebugThoughtIdx++
			if c.pet.DebugThoughtIdx >= len(debugThoughtCategories) {
				c.pet.DebugThoughtIdx = 0
			}
			c.stateMu.Unlock()
			return true
		}
		if clickedToken(line2, "[H+]") {
			coordinatorDebugLog.Printf("Debug bar: [H+] clicked")
			c.stateMu.Lock()
			c.pet.Happiness += 10
			if c.pet.Happiness > 100 {
				c.pet.Happiness = 100
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[H-]") {
			coordinatorDebugLog.Printf("Debug bar: [H-] clicked")
			c.stateMu.Lock()
			c.pet.Happiness -= 10
			if c.pet.Happiness < 0 {
				c.pet.Happiness = 0
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[F+]") {
			coordinatorDebugLog.Printf("Debug bar: [F+] clicked")
			c.stateMu.Lock()
			c.pet.Hunger += 10
			if c.pet.Hunger > 100 {
				c.pet.Hunger = 100
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		if clickedToken(line2, "[F-]") {
			coordinatorDebugLog.Printf("Debug bar: [F-] clicked")
			c.stateMu.Lock()
			c.pet.Hunger -= 10
			if c.pet.Hunger < 0 {
				c.pet.Hunger = 0
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		return false
	}

	return false
}

// renderSmallButton renders a single-line flat button with background color.
func renderSmallButton(width int, label string, bgColor, fgColor string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Foreground(lipgloss.Color(fgColor)).
		Bold(true).
		Width(width).
		Align(lipgloss.Center).
		Render(label)
}

// renderPinnedActionButtons renders New Tab, New Group, Close, and Touch Mode toggle
// buttons in the pinned area, above the resize buttons.
// In large/touch mode: 3-line bordered buttons. In small mode: single-line flat buttons.
func (c *Coordinator) renderPinnedActionButtons(width int) string {
	if width < 1 {
		width = 1
	}
	var s strings.Builder

	// Get button colors from theme, with fallbacks
	var primaryBg, primaryFg, secondaryBg, secondaryFg string
	var destructiveBg, destructiveFg string
	if c.theme != nil {
		primaryBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#27ae60")
		primaryFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
		secondaryBg = c.getThemeColor(c.theme.ButtonSecondaryBg, "#9b59b6")
		secondaryFg = c.getThemeColor(c.theme.ButtonSecondaryFg, "#ffffff")
		destructiveBg = c.getThemeColor(c.theme.ButtonDestructiveBg, "#e74c3c")
		destructiveFg = c.getThemeColor(c.theme.ButtonDestructiveFg, "#ffffff")
	} else {
		primaryBg, primaryFg = "#27ae60", "#ffffff"
		secondaryBg, secondaryFg = "#9b59b6", "#ffffff"
		destructiveBg, destructiveFg = "#e74c3c", "#ffffff"
	}

	// New Tab + New Group side by side
	if c.config.Sidebar.NewTabButton && c.config.Sidebar.NewGroupButton {
		leftWidth := width / 2
		rightWidth := width - leftWidth
		leftBtn := renderSmallButton(leftWidth, "+ Tab", primaryBg, primaryFg)
		rightBtn := renderSmallButton(rightWidth, "+ Group", secondaryBg, secondaryFg)
		left := zone.Mark("sidebar:new_tab", leftBtn)
		right := zone.Mark("sidebar:new_group", rightBtn)
		s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n")
	} else if c.config.Sidebar.NewTabButton {
		btn := renderSmallButton(width, "+ New Tab", primaryBg, primaryFg)
		s.WriteString(zone.Mark("sidebar:new_tab", btn) + "\n")
	} else if c.config.Sidebar.NewGroupButton {
		btn := renderSmallButton(width, "+ New Group", secondaryBg, secondaryFg)
		s.WriteString(zone.Mark("sidebar:new_group", btn) + "\n")
	}

	// Close Tab button
	if c.config.Sidebar.CloseButton {
		btn := renderSmallButton(width, "x Close Tab", destructiveBg, destructiveFg)
		s.WriteString(zone.Mark("sidebar:close_tab", btn) + "\n")
	}

	return s.String()
}

// renderSidebarResizeButtons renders resize buttons at bottom of sidebar.
func (c *Coordinator) renderSidebarResizeButtons(width int) string {
	if width < 1 {
		width = 1
	}

	var destructiveBg, destructiveFg, primaryBg, primaryFg string
	if c.theme != nil {
		destructiveBg = c.getThemeColor(c.theme.ButtonDestructiveBg, "#e74c3c")
		destructiveFg = c.getThemeColor(c.theme.ButtonDestructiveFg, "#ffffff")
		primaryBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#27ae60")
		primaryFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
	} else {
		destructiveBg, destructiveFg = "#e74c3c", "#ffffff"
		primaryBg, primaryFg = "#27ae60", "#ffffff"
	}

	leftWidth := width / 2
	rightWidth := width - leftWidth

	shrinkBtn := renderSmallButton(leftWidth, "<", destructiveBg, destructiveFg)
	growBtn := renderSmallButton(rightWidth, ">", primaryBg, primaryFg)

	left := zone.Mark("sidebar:shrink", shrinkBtn)
	right := zone.Mark("sidebar:grow", growBtn)
	combined := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	return combined + "\n"
}

func (c *Coordinator) renderNavButtons(width int) string {
	if width < 1 {
		width = 1
	}

	var prevBg, nextBg, navFg string
	if c.theme != nil {
		prevBg = c.getThemeColor(c.theme.ButtonPrimaryBg, "#2563eb")
		nextBg = c.getThemeColor(c.theme.ButtonSecondaryBg, "#16a34a")
		navFg = c.getThemeColor(c.theme.ButtonPrimaryFg, "#ffffff")
	} else {
		prevBg, nextBg, navFg = "#2563eb", "#16a34a", "#ffffff"
	}

	leftWidth := width / 2
	rightWidth := width - leftWidth

	prevBtn := renderSmallButton(leftWidth, "▲", prevBg, navFg)
	nextBtn := renderSmallButton(rightWidth, "▼", nextBg, navFg)

	navLeft := zone.Mark("sidebar:prev_window", prevBtn)
	navRight := zone.Mark("sidebar:next_window", nextBtn)
	return lipgloss.JoinHorizontal(lipgloss.Top, navLeft, navRight) + "\n\n"
}
func (c *Coordinator) getThemeColor(themeColor, fallback string) string {
	if c.theme != nil && themeColor != "" {
		return themeColor
	}
	return fallback
}

// getClientWidth returns the width for a specific client, with fallback to lastWidth
func (c *Coordinator) getClientWidth(clientID string) int {
	c.clientWidthsMu.RLock()
	width, ok := c.clientWidths[clientID]
	c.clientWidthsMu.RUnlock()
	if !ok || width < 10 {
		width = c.lastWidth
		if width < 10 {
			width = 25
		}
	}
	return width
}

// RemoveClient cleans up state for a disconnected client
func (c *Coordinator) RemoveClient(clientID string) {
	c.clientWidthsMu.Lock()
	delete(c.clientWidths, clientID)
	c.clientWidthsMu.Unlock()
	coordinatorDebugLog.Printf("Removed client: %s (remaining: %d)", clientID, len(c.clientWidths))
}

// HandleInput processes input events from renderers
// Returns true if window list refresh is needed (expensive tmux calls)
func (c *Coordinator) HandleInput(clientID string, input *daemon.InputPayload) bool {
	switch input.Type {
	case "action":
		return c.handleSemanticAction(clientID, input)
	case "key":
		c.handleKeyInput(clientID, input)
		return true // key inputs might need refresh
	case "menu_select":
		c.HandleMenuSelect(clientID, input.MouseX) // MouseX repurposed as menu item index
		return true
	case "marker_picker":
		return c.handleMarkerPickerInput(input)
	case "color_picker":
		return c.handleColorPickerInput(input)
	}
	return false
}

func (c *Coordinator) handleMarkerPickerInput(input *daemon.InputPayload) bool {
	if input.PickerAction != "apply" {
		return false
	}
	return c.applyMarkerSelection(input.PickerScope, input.PickerTarget, input.PickerValue)
}

func (c *Coordinator) handleColorPickerInput(input *daemon.InputPayload) bool {
	if input.PickerAction != "apply" {
		return false
	}
	return c.applyColorPickerSelection(input.PickerScope, input.PickerTarget, input.PickerValue)
}

func (c *Coordinator) applyColorPickerSelection(scope, target, hexColor string) bool {
	hexColor = strings.TrimSpace(hexColor)
	if hexColor == "" {
		return false
	}

	if scope == "window" {
		windowIndex, err := strconv.Atoi(strings.TrimSpace(target))
		if err != nil {
			return false
		}
		c.setWindowColor(windowIndex, hexColor)
		return true
	}

	if scope == "group" {
		colorScript := c.getScriptPath("set_group_color.sh")
		if colorScript != "" {
			exec.Command("bash", colorScript, target, hexColor).Run()
		}
		return true
	}

	return false
}

func (c *Coordinator) openColorPicker(clientID, scope, target, title, currentColor string) {
	if c.OnSendColorPicker == nil {
		return
	}
	c.OnSendColorPicker(clientID, &daemon.ColorPickerPayload{
		Title:        title,
		Scope:        scope,
		Target:       target,
		CurrentColor: currentColor,
	})
}

// handleSemanticAction processes pre-resolved semantic actions from renderers
// Returns true if window list refresh is needed
func (c *Coordinator) handleSemanticAction(clientID string, input *daemon.InputPayload) bool {
	coordinatorDebugLog.Printf("=== SEMANTIC ACTION ===")
	coordinatorDebugLog.Printf("  Client: %s", clientID)
	coordinatorDebugLog.Printf("  Action: %s", input.ResolvedAction)
	coordinatorDebugLog.Printf("  Target: %s", input.ResolvedTarget)
	coordinatorDebugLog.Printf("  Button: %s", input.Button)
	coordinatorDebugLog.Printf("  Mouse: X=%d Y=%d ViewportOffset=%d", input.MouseX, input.MouseY, input.ViewportOffset)
	coordinatorDebugLog.Printf("  SequenceNum: %d", input.SequenceNum)

	// Handle right-click for context menus
	// Show context menu for all right-clicks (regular, simulated, or touch mode)
	if input.Button == "right" && input.ResolvedAction != "" {
		coordinatorDebugLog.Printf("  -> Right-click: simulated=%v -> showing context menu",
			input.IsSimulatedRightClick)
		c.handleRightClick(clientID, input)
		return true
	}

	// Custom pet widget click detection (bypasses BubbleZone)
	// Uses tracked line positions from renderPetWidget for precise hit testing
	// Only run for actual mouse clicks (Button is set), not programmatic actions
	// sent via socket (e.g. toggle_collapse_sidebar from keybinding scripts)
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin && input.Button != "" {
		if handled := c.handlePetWidgetClick(clientID, input); handled {
			return false // Pet actions don't need window refresh
		}
	}

	if input.ResolvedAction == "" {
		// No action resolved - stay in sidebar (don't steal focus)
		coordinatorDebugLog.Printf("  -> No action resolved, staying in sidebar")
		return false
	}

	switch input.ResolvedAction {
	case "select_window":
		rawTarget := input.ResolvedTarget
		targetWindow := input.ResolvedTarget
		if win := findWindowByTarget(c.windows, input.ResolvedTarget); win != nil {
			targetWindow = win.ID
		}

		now := time.Now()
		selectKey := clientID + "|" + targetWindow
		c.lastWindowSelectMu.Lock()
		if lastAny, ok := c.lastWindowByClient[clientID]; ok && now.Sub(lastAny) < 300*time.Millisecond {
			c.lastWindowSelectMu.Unlock()
			logEvent("SELECT_WINDOW_DEBOUNCED_CLIENT client=%s raw=%s target=%s age_ms=%d", clientID, rawTarget, targetWindow, now.Sub(lastAny).Milliseconds())
			return false
		}
		if last, ok := c.lastWindowSelect[selectKey]; ok && now.Sub(last) < 450*time.Millisecond {
			c.lastWindowSelectMu.Unlock()
			logEvent("SELECT_WINDOW_DEBOUNCED client=%s raw=%s target=%s age_ms=%d", clientID, rawTarget, targetWindow, now.Sub(last).Milliseconds())
			return false
		}
		c.lastWindowByClient[clientID] = now
		c.lastWindowSelect[selectKey] = now
		c.lastWindowSelectMu.Unlock()
		logEvent("SELECT_WINDOW client=%s raw=%s target=%s", clientID, rawTarget, targetWindow)

		selectCtx, selectCancel := context.WithTimeout(context.Background(), 2*time.Second)
		exec.CommandContext(selectCtx, "tmux", "select-window", "-t", targetWindow).Run()
		selectCancel()
		// Optimistic: immediately flip active window so the next BroadcastRender
		// (triggered by refreshCh) renders the TABBY header with the correct color.
		c.SetActiveWindowOptimistic(targetWindow)

		activeCtx, activeCancel := context.WithTimeout(context.Background(), 2*time.Second)
		activeOut, activeErr := exec.CommandContext(activeCtx, "tmux", "display-message", "-p", "-t", targetWindow,
			"#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		activeCancel()
		if activeErr == nil {
			parts := strings.SplitN(strings.TrimSpace(string(activeOut)), "\x1f", 3)
			if len(parts) == 3 && !isAuxiliaryPaneCommand(parts[1]) && !isAuxiliaryPaneCommand(parts[2]) {
				return true
			}
		}

		listCtx, listCancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(listCtx, "tmux", "list-panes", "-t", targetWindow,
			"-F", "#{pane_id}\x1f#{pane_current_command}\x1f#{pane_start_command}").Output()
		listCancel()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				parts := strings.SplitN(line, "\x1f", 3)
				if len(parts) != 3 {
					continue
				}
				paneID := parts[0]
				cmd := parts[1]
				startCmd := parts[2]
				if !isAuxiliaryPaneCommand(cmd) && !isAuxiliaryPaneCommand(startCmd) {
					switchCtx, switchCancel := context.WithTimeout(context.Background(), 2*time.Second)
					exec.CommandContext(switchCtx, "tmux", "select-pane", "-t", paneID).Run()
					switchCancel()
					break
				}
			}
		}
		return true

	case "toggle_panes":
		// Toggle collapse/expand for panes within this window
		winIdx := input.ResolvedTarget
		// Check current collapsed state via tmux option
		out, err := exec.Command("tmux", "show-window-option", "-v", "-t", ":"+winIdx, "@tabby_collapsed").Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			// Currently collapsed -> expand (unset option)
			exec.Command("tmux", "set-window-option", "-t", ":"+winIdx, "-u", "@tabby_collapsed").Run()
		} else {
			// Currently expanded -> collapse
			exec.Command("tmux", "set-window-option", "-t", ":"+winIdx, "@tabby_collapsed", "1").Run()
		}
		return true // Trigger immediate refresh to show collapse/expand change

	case "toggle_pane_collapse":
		// Toggle collapse/expand for individual pane (from pane header button)
		// Target is the pane ID (e.g., "%5") - the CONTENT pane, not the header pane
		paneID := input.ResolvedTarget
		coordinatorDebugLog.Printf("toggle_pane_collapse: paneID=%s", paneID)
		if paneID == "" {
			coordinatorDebugLog.Printf("toggle_pane_collapse: empty paneID, returning false")
			return false
		}
		// Check if pane is currently collapsed
		out, err := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_collapsed").Output()
		isCollapsed := err == nil && strings.TrimSpace(string(out)) == "1"
		coordinatorDebugLog.Printf("toggle_pane_collapse: isCollapsed=%v (out=%q, err=%v)", isCollapsed, strings.TrimSpace(string(out)), err)

		// Minimum height for collapsed pane (1 line - tmux minimum)
		collapsedHeight := 1
		// Header panes are always 1 line tall
		headerHeight := 1

		// Get window ID for this pane so we can fix header heights after resize
		windowIDOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
		windowID := strings.TrimSpace(string(windowIDOut))

		desiredExpandHeight := 0
		if isCollapsed {
			prevHeightOut, _ := exec.Command("tmux", "show-options", "-pqv", "-t", paneID, "@tabby_pane_prev_height").Output()
			prevHeight := strings.TrimSpace(string(prevHeightOut))
			if prevHeight != "" && prevHeight != "0" {
				if n, err := strconv.Atoi(prevHeight); err == nil && n > 0 {
					desiredExpandHeight = n
				}
			}
		} else {
			// Collapse: save height and minimize content pane to 1 line
			heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
			currentHeight := strings.TrimSpace(string(heightOut))
			if currentHeight == "" {
				currentHeight = "10"
			}
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_prev_height", currentHeight).Run()
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_collapsed", "1").Run()
			exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", collapsedHeight)).Run()
		}

		// Fix layout after collapse/expand - ensure headers stay at correct height
		// and other content panes get the freed/taken space.
		if windowID != "" {
			listOut, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}:#{pane_height}").Output()
			var headerPanes []string
			var otherContentPanes []string
			paneHeights := make(map[string]int)
			for _, line := range strings.Split(string(listOut), "\n") {
				parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
				if len(parts) < 2 {
					continue
				}
				pid := parts[0]
				cmd := parts[1]
				if len(parts) >= 3 {
					if h, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
						paneHeights[pid] = h
					}
				}
				if isAuxiliaryPaneCommand(cmd) {
					headerPanes = append(headerPanes, pid)
				} else if pid != paneID {
					otherContentPanes = append(otherContentPanes, pid)
				}
			}

			// If we just collapsed, expand the other content panes to fill space
			if !isCollapsed && len(otherContentPanes) > 0 {
				// Get total window height
				winHeightOut, _ := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_height}").Output()
				totalHeight, _ := strconv.Atoi(strings.TrimSpace(string(winHeightOut)))
				if totalHeight > 0 {
					// Calculate space for other content panes:
					// total - (headers * headerHeight) - collapsedHeight
					numHeaders := len(headerPanes)
					availableForContent := totalHeight - (numHeaders * headerHeight) - collapsedHeight
					if availableForContent > 0 {
						perPane := availableForContent / len(otherContentPanes)
						if perPane > 1 {
							for _, contentID := range otherContentPanes {
								exec.Command("tmux", "resize-pane", "-t", contentID, "-y", fmt.Sprintf("%d", perPane)).Run()
							}
						}
					}
				}
			}

			if isCollapsed && desiredExpandHeight > 0 {
				winHeightOut, _ := exec.Command("tmux", "display-message", "-t", windowID, "-p", "#{window_height}").Output()
				totalHeight, _ := strconv.Atoi(strings.TrimSpace(string(winHeightOut)))
				if totalHeight > 0 {
					minPaneHeight := 1
					maxTarget := totalHeight - (len(headerPanes) * headerHeight) - (len(otherContentPanes) * minPaneHeight)
					if maxTarget < 1 {
						maxTarget = 1
					}
					targetHeight := desiredExpandHeight
					if targetHeight > maxTarget {
						targetHeight = maxTarget
					}

					currentTargetHeight := paneHeights[paneID]
					if currentTargetHeight <= 0 {
						heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
						currentTargetHeight, _ = strconv.Atoi(strings.TrimSpace(string(heightOut)))
					}
					need := targetHeight - currentTargetHeight
					if need > 0 && len(otherContentPanes) > 0 {
						sorted := make([]string, 0, len(otherContentPanes))
						sorted = append(sorted, otherContentPanes...)
						sort.Slice(sorted, func(i, j int) bool {
							return paneHeights[sorted[i]] > paneHeights[sorted[j]]
						})
						remaining := need
						for _, otherID := range sorted {
							if remaining <= 0 {
								break
							}
							h := paneHeights[otherID]
							if h <= 1 {
								continue
							}
							shrinkBy := h - 1
							if shrinkBy > remaining {
								shrinkBy = remaining
							}
							newH := h - shrinkBy
							exec.Command("tmux", "resize-pane", "-t", otherID, "-y", fmt.Sprintf("%d", newH)).Run()
							paneHeights[otherID] = newH
							remaining -= shrinkBy
						}
					}

					exec.Command("tmux", "resize-pane", "-t", paneID, "-y", fmt.Sprintf("%d", targetHeight)).Run()
					heightOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_height}").Output()
					newHeight, _ := strconv.Atoi(strings.TrimSpace(string(heightOut)))
					if newHeight > collapsedHeight {
						exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_collapsed").Run()
						exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "@tabby_pane_prev_height").Run()
					}
				}
			}

			// Fix all header pane heights LAST (after content pane resizes)
			for _, hdrID := range headerPanes {
				exec.Command("tmux", "resize-pane", "-t", hdrID, "-y", fmt.Sprintf("%d", headerHeight)).Run()
			}
		}
		return true

	case "select_pane":
		// Run synchronously so RefreshWindows() sees the new state
		// First find the window containing this pane and switch to it
		paneID := input.ResolvedTarget
		// Use display-message to get the window ID for this pane
		out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_id}").Output()
		if err == nil {
			windowID := strings.TrimSpace(string(out))
			if windowID != "" {
				// Select the window first, then the pane
				exec.Command("tmux", "select-window", "-t", windowID).Run()
			}
		}
		exec.Command("tmux", "select-pane", "-t", paneID).Run()
		return true

	case "toggle_group":
		c.stateMu.Lock()
		groupName := input.ResolvedTarget
		if c.collapsedGroups[groupName] {
			delete(c.collapsedGroups, groupName)
		} else {
			c.collapsedGroups[groupName] = true
		}
		c.stateMu.Unlock()
		// Save async - don't block render on multiple tmux round-trips
		go c.saveCollapsedGroups()
		return false // No tmux window state change

	case "button":
		switch input.ResolvedTarget {
		case "new_tab":
			c.createNewWindowInCurrentGroup(clientID)
			// Don't call selectContentPaneInActiveWindow() here - the new window only has
			// one pane (shell) until the daemon spawns the sidebar. Let spawnRenderers
			// handle focus correctly after creating the sidebar pane.
		case "new_group":
			// Could implement group creation dialog
		case "close_tab":
			exec.Command("tmux", "kill-window").Run()
			// Try to switch to the previously active window rather than tmux's default (next)
			exec.Command("tmux", "last-window").Run()
			selectContentPaneInActiveWindow()
		}
		return true

	case "new_tab":
		c.createNewWindowInCurrentGroup(clientID)
		// Don't call selectContentPaneInActiveWindow() - let spawnRenderers handle focus
		return true

	case "new_group":
		exe, _ := os.Executable()
		pluginDir := filepath.Join(filepath.Dir(exe), "..")
		scriptPath := filepath.Join(pluginDir, "scripts", "new_group.sh")
		cmd := fmt.Sprintf("command-prompt -p 'New group name:' \"run-shell '%s %%%% '\"", scriptPath)
		exec.Command("tmux", strings.Split(cmd, " ")...).Run()
		return false

	case "close_tab":
		exec.Command("tmux", "kill-window").Run()
		exec.Command("tmux", "last-window").Run()
		selectContentPaneInActiveWindow()
		return true

	case "prev_window":
		exec.Command("tmux", "previous-window").Run()
		return true

	case "next_window":
		exec.Command("tmux", "next-window").Run()
		return true

	case "drop_food":
		// Drop food at a random position for the pet to eat
		c.stateMu.Lock()
		// If dead, food revives the pet!
		if c.pet.IsDead {
			c.pet.IsDead = false
			c.pet.DeathTime = time.Time{}
			c.pet.StarvingStart = time.Time{}
			c.pet.Hunger = 80
			c.pet.Happiness = 50
			c.pet.State = "eating"
			c.pet.LastThought = "life-giving noms!"
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return false
		}
		width := c.getClientWidth(clientID)
		dropX := safeRandRange(2, width-2)
		// Avoid dropping food on poop
		for attempts := 0; attempts < 5; attempts++ {
			hasPoop := false
			for _, poopX := range c.pet.PoopPositions {
				if abs(dropX-poopX) <= 1 {
					hasPoop = true
					break
				}
			}
			if !hasPoop {
				break
			}
			dropX = safeRandRange(2, width-2)
		}
		c.pet.FoodItem = pos2D{X: dropX, Y: 2} // Drop from high air
		c.pet.LastThought = "food!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "drop_yarn":
		// Drop or toss the yarn at click position
		c.stateMu.Lock()
		// Dead pets don't play
		if c.pet.IsDead {
			c.stateMu.Unlock()
			return false
		}
		width := c.getClientWidth(clientID)
		// Use click position, clamped to valid range
		tossX := input.MouseX
		if tossX < 2 {
			tossX = 2
		}
		if tossX >= width-2 {
			tossX = width - 1
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2}                  // Toss high
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second) // Yarn disappears after 15 seconds
		c.pet.YarnPushCount = 0
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "clean_poop":
		// Clean up poop at the clicked position
		c.stateMu.Lock()
		if len(c.pet.PoopPositions) > 0 {
			// Remove the first poop (or use input.ResolvedTarget for specific position)
			c.pet.PoopPositions = c.pet.PoopPositions[1:]
			c.pet.TotalPoopsCleaned++
			c.pet.LastThought = "much better."
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "pet_pet":
		// Pet the pet - increase happiness (and wake up if sleeping)
		c.stateMu.Lock()
		wasSleeping := c.pet.State == "sleeping"
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		if wasSleeping {
			c.pet.LastThought = randomThought("wakeup")
		} else {
			c.pet.LastThought = randomThought("petting")
		}
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false // Pet action, no window refresh needed

	case "shrink_sidebar", "shrink":
		// Shrink sidebar width by 5 columns (min 15)
		currentWidth := c.getClientWidth(clientID)
		newWidth := currentWidth - 5
		if newWidth < 15 {
			newWidth = 15
		}
		// Save to tmux option and sync all sidebar panes
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", newWidth)).Run()
		c.persistSidebarWidthProfile(clientID, newWidth)
		go syncAllSidebarWidths(newWidth)
		// Update client width tracking
		c.clientWidthsMu.Lock()
		c.clientWidths[clientID] = newWidth
		c.clientWidthsMu.Unlock()
		coordinatorDebugLog.Printf("Sidebar shrink: %d -> %d (syncing all)", currentWidth, newWidth)
		return false

	case "grow_sidebar", "grow":
		// Grow sidebar width by 5 columns (max 50)
		currentWidth := c.getClientWidth(clientID)
		newWidth := currentWidth + 5
		if newWidth > 50 {
			newWidth = 50
		}
		// Save to tmux option and sync all sidebar panes
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", newWidth)).Run()
		c.persistSidebarWidthProfile(clientID, newWidth)
		go syncAllSidebarWidths(newWidth)
		// Update client width tracking
		c.clientWidthsMu.Lock()
		c.clientWidths[clientID] = newWidth
		c.clientWidthsMu.Unlock()
		coordinatorDebugLog.Printf("Sidebar grow: %d -> %d (syncing all)", currentWidth, newWidth)
		return false

	case "toggle_collapse_sidebar":
		if c.sidebarCollapsed {
			input.ResolvedAction = "expand_sidebar"
		} else {
			input.ResolvedAction = "collapse_sidebar"
		}
		return c.handleSemanticAction(clientID, input)

	case "collapse_sidebar":
		currentWidth := c.getClientWidth(clientID)
		c.sidebarPreviousWidth = currentWidth
		c.sidebarCollapsed = true
		exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_collapsed", "1").Run()
		go exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_previous_width", fmt.Sprintf("%d", currentWidth)).Run()
		go exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", "1").Run()
		syncAllSidebarWidths(1)
		coordinatorDebugLog.Printf("Sidebar collapsed: saved width=%d, synced all to 1", currentWidth)
		return true

	case "expand_sidebar":
		c.sidebarCollapsed = false
		newWidth := c.sidebarPreviousWidth
		if newWidth < 15 {
			newWidth = 25
		}
		newWidth = c.boundedSidebarWidthForWindow(clientID, newWidth)
		exec.Command("tmux", "set-option", "-gqu", "@tabby_sidebar_collapsed").Run()
		go exec.Command("tmux", "set-option", "-gq", "@tabby_sidebar_width", fmt.Sprintf("%d", newWidth)).Run()
		syncAllSidebarWidths(newWidth)
		c.clientWidthsMu.Lock()
		c.clientWidths[clientID] = newWidth
		c.clientWidthsMu.Unlock()
		if c.OnSyncSidebarClientWidths != nil {
			c.OnSyncSidebarClientWidths(newWidth)
		}
		coordinatorDebugLog.Printf("Sidebar expanded: restoring width=%d, synced all", newWidth)
		return true

	case "sidebar_settings":
		// Show sidebar settings context menu
		c.showSidebarSettingsMenu(clientID, menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY})
		return false

	case "header_split_v":
		// Get the pane's current path first, then use it for the split
		pathOut, _ := exec.Command("tmux", "display-message", "-t", input.ResolvedTarget, "-p", "#{pane_current_path}").Output()
		panePath := strings.TrimSpace(string(pathOut))
		if panePath == "" {
			panePath = "~"
		}
		exec.Command("tmux", "split-window", "-v", "-t", input.ResolvedTarget, "-c", panePath).Run()
		return true

	case "header_split_h":
		// Get the pane's current path first, then use it for the split
		pathOut2, _ := exec.Command("tmux", "display-message", "-t", input.ResolvedTarget, "-p", "#{pane_current_path}").Output()
		panePath2 := strings.TrimSpace(string(pathOut2))
		if panePath2 == "" {
			panePath2 = "~"
		}
		exec.Command("tmux", "split-window", "-h", "-t", input.ResolvedTarget, "-c", panePath2).Run()
		return true

	case "header_close":
		paneID := input.ResolvedTarget
		killWrapper := c.getScriptPath("kill_pane_wrapper.sh")
		if killWrapper != "" {
			exec.Command("tmux", "run-shell", fmt.Sprintf("'%s' -t %s", killWrapper, paneID)).Run()
			return true
		}

		// Fallback: preserve layout then close pane directly.
		saveLayoutBeforeKill(paneID)
		exec.Command("tmux", "kill-pane", "-t", paneID).Run()
		return true

	case "header_select_pane":
		// Click on a pane label in the header -> focus that pane
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		return true

	case "header_context":
		// This is the full-width fallback region on pane headers.
		// Right-clicks are already handled by handleRightClick() above.
		// Left-clicks on the spacer area should be a no-op.
		return false

	case "header_drag_resize":
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		return false

	case "header_carat_up":
		exec.Command("tmux", "resize-pane", "-t", input.ResolvedTarget, "-U", "5").Run()
		fixHeaderHeightsInWindow(input.ResolvedTarget)
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		c.RefreshWindows()
		return true

	case "header_carat_down":
		exec.Command("tmux", "resize-pane", "-t", input.ResolvedTarget, "-D", "5").Run()
		fixHeaderHeightsInWindow(input.ResolvedTarget)
		exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()
		c.RefreshWindows()
		return true

	case "group_menu":
		// Hamburger menu on group header -> show group context menu
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		c.showGroupContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "window_menu":
		// Hamburger menu on window row -> show window context menu
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		c.showWindowContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "pane_menu":
		// Debounce header-triggered menu opens to avoid press/release reopen flashes.
		if strings.HasPrefix(clientID, "header:") {
			key := clientID + "|" + input.ResolvedTarget
			now := time.Now()
			c.lastPaneMenuOpenMu.Lock()
			last := c.lastPaneMenuOpen[key]
			if now.Sub(last) < 900*time.Millisecond {
				c.lastPaneMenuOpenMu.Unlock()
				return true
			}
			c.lastPaneMenuOpen[key] = now
			c.lastPaneMenuOpenMu.Unlock()
		}

		// Header panes have BubbleTea mouse capture which intercepts clicks
		// that should dismiss the menu. Target the content pane instead so
		// tmux properly handles click-outside and Esc dismissal.
		pos := menuPosition{PaneID: input.PaneID, X: input.MouseX, Y: input.MouseY}
		if strings.HasPrefix(clientID, "header:") {
			pos = menuPosition{PaneID: input.ResolvedTarget}
		}
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
		return true

	case "pane_grow", "pane_shrink", "pane_grow_v", "pane_shrink_v", "pane_grow_h", "pane_shrink_h":
		paneID := input.ResolvedTarget
		action := input.ResolvedAction

		if action == "pane_grow" || action == "pane_shrink" {
			// Backward compatibility: legacy actions use inferred dominant split axis.
			c.stateMu.RLock()
			var targetWin *tmux.Window
			for i := range c.windows {
				for _, p := range c.windows[i].Panes {
					if p.ID == paneID {
						targetWin = &c.windows[i]
						break
					}
				}
				if targetWin != nil {
					break
				}
			}
			c.stateMu.RUnlock()
			if targetWin != nil && c.isVerticalStackedPane(targetWin, paneID) {
				if action == "pane_grow" {
					action = "pane_grow_v"
				} else {
					action = "pane_shrink_v"
				}
			} else {
				if action == "pane_grow" {
					action = "pane_grow_h"
				} else {
					action = "pane_shrink_h"
				}
			}
		}

		switch action {
		case "pane_grow_v":
			exec.Command("tmux", "resize-pane", "-t", paneID, "-D", "5").Run()
		case "pane_shrink_v":
			exec.Command("tmux", "resize-pane", "-t", paneID, "-U", "5").Run()
		case "pane_grow_h":
			exec.Command("tmux", "resize-pane", "-t", paneID, "-R", "5").Run()
		case "pane_shrink_h":
			wOut, _ := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_width}").Output()
			if w, err := strconv.Atoi(strings.TrimSpace(string(wOut))); err == nil && w > 10 {
				exec.Command("tmux", "resize-pane", "-t", paneID, "-x", fmt.Sprintf("%d", w-5)).Run()
			}
		}

		fixHeaderHeightsInWindow(paneID)
		exec.Command("tmux", "select-pane", "-t", paneID).Run()
		c.RefreshWindows()
		return true

	case "sidebar_toggle_position":
		// Toggle sidebar position between left and right
		currentPos := c.config.Sidebar.Position
		newPos := "right"
		if currentPos == "right" {
			newPos = "left"
		}
		// Use tmux run-shell to restart asynchronously (the daemon dies on toggle-off)
		toggleScript := c.getToggleScript()
		if toggleScript != "" {
			restartCmd := fmt.Sprintf("tmux set-option -g @tabby_sidebar_position %s; '%s'; sleep 0.3; '%s'", newPos, toggleScript, toggleScript)
			exec.Command("tmux", "run-shell", "-b", restartCmd).Run()
		}
		return false

	case "toggle_prefix_mode":
		// Toggle prefix mode (flat window list vs grouped hierarchy)
		c.stateMu.Lock()
		c.config.Sidebar.PrefixMode = !c.config.Sidebar.PrefixMode
		newVal := "0"
		if c.config.Sidebar.PrefixMode {
			newVal = "1"
		}
		c.stateMu.Unlock()
		exec.Command("tmux", "set-option", "-gq", "@tabby_prefix_mode", newVal).Run()
		return false

	case "ground":
		// Ground click - determine action based on click X position
		// Click position relative to zone start
		clickX := input.MouseX
		c.stateMu.Lock()

		// Check if clicking on cat (only when cat is on ground, Y=0)
		if c.pet.Pos.Y == 0 && clickX == c.pet.Pos.X {
			// Pet the cat (wake up if sleeping)
			wasSleeping := c.pet.State == "sleeping"
			c.pet.Happiness = min(100, c.pet.Happiness+10)
			c.pet.TotalPets++
			c.pet.LastPet = time.Now()
			c.pet.State = "happy"
			if wasSleeping {
				c.pet.LastThought = randomThought("wakeup")
			} else {
				c.pet.LastThought = randomThought("petting")
			}
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return false
		}

		// Check if clicking on poop
		for i, poopX := range c.pet.PoopPositions {
			if clickX == poopX {
				// Clean this poop
				c.pet.PoopPositions = append(c.pet.PoopPositions[:i], c.pet.PoopPositions[i+1:]...)
				c.pet.TotalPoopsCleaned++
				c.pet.LastThought = "much better."
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return false
			}
		}

		// Otherwise, drop yarn at click position using client-specific width
		width := c.getClientWidth(clientID)
		tossX := clickX
		if tossX < 2 {
			tossX = 2
		}
		if tossX >= width-2 {
			tossX = width - 1
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.YarnPushCount = 0
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return false
	}
	return false
}

// handlePetWidgetClick uses custom click detection for the pet widget
// This bypasses BubbleZone and uses tracked line positions for precise hit testing
// Returns true if the click was handled, false otherwise
func (c *Coordinator) handlePetWidgetClick(clientID string, input *daemon.InputPayload) bool {
	// Debounce rapid clicks (200ms) to prevent render floods
	now := time.Now()
	if now.Sub(c.lastPetClick) < 200*time.Millisecond {
		return true // Absorb the click without processing
	}
	c.lastPetClick = now

	// Get client-specific width for accurate click detection
	clientWidth := c.getClientWidth(clientID)

	// Calculate content Y from screen position and viewport offset
	contentY := input.MouseY + input.ViewportOffset
	clickX := input.MouseX
	layout := c.petLayout

	// Calculate Y position relative to pet widget start
	clickY := contentY - layout.ContentStartLine

	coordinatorDebugLog.Printf("Pet click detection: screenY=%d viewportOffset=%d contentY=%d petRelativeY=%d X=%d clientWidth=%d",
		input.MouseY, input.ViewportOffset, contentY, clickY, clickX, clientWidth)
	coordinatorDebugLog.Printf("  Layout: ContentStart=%d Feed=%d HighAir=%d LowAir=%d Ground=%d PlayWidth=%d",
		layout.ContentStartLine, layout.FeedLine, layout.HighAirLine, layout.LowAirLine, layout.GroundLine, layout.PlayWidth)

	// Check if click is within the pet widget at all
	if clickY < 0 || clickY >= layout.WidgetHeight {
		coordinatorDebugLog.Printf("  -> Click outside pet widget bounds (clickY=%d, widgetHeight=%d)", clickY, layout.WidgetHeight)
		return false
	}

	// Check if click is on Feed line
	if clickY == layout.FeedLine {
		coordinatorDebugLog.Printf("  -> Feed line clicked, dropping food")
		c.stateMu.Lock()
		dropX := safeRandRange(2, clientWidth-2)
		c.pet.FoodItem = pos2D{X: dropX, Y: 2}
		c.pet.LastThought = "food!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if click is on debug bar lines (if debug bar is enabled)
	if c.config.Widgets.Pet.DebugBar {
		if clickY == layout.DebugLine1 || clickY == layout.DebugLine2 {
			coordinatorDebugLog.Printf("  -> Debug bar line clicked (line=%d, X=%d)", clickY, clickX)
			return c.handleDebugBarClick(clientID, clickX, clickY)
		}
	}

	// Calculate safe play width for this client (must match rendering)
	safePlayWidth := clientWidth - 1
	if safePlayWidth < 5 {
		safePlayWidth = 5
	}

	// Check if click is on high air line (Y=2 in pet coordinate space)
	if clickY == layout.HighAirLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> High air line clicked at X=%d, checking for cat/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 2)
	}

	// Check if click is on low air line (Y=1 in pet coordinate space)
	if clickY == layout.LowAirLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> Low air line clicked at X=%d, checking for cat/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 1)
	}

	// Check if click is on ground line (Y=0 in pet coordinate space)
	if clickY == layout.GroundLine && clickX < safePlayWidth {
		coordinatorDebugLog.Printf("  -> Ground line clicked at X=%d, checking for cat/poop/yarn", clickX)
		return c.handlePetPlayAreaClick(clientID, input, clickX, 0)
	}

	coordinatorDebugLog.Printf("  -> Click not on pet widget interactive lines")
	return false
}

// getSprites returns the pet sprites based on current style and config overrides
func (c *Coordinator) getSprites() petSprites {
	petCfg := c.config.Widgets.Pet
	style := petCfg.Style
	if style == "" {
		style = "emoji"
	}
	sprites, ok := petSpritesByStyle[style]
	if !ok {
		sprites = petSpritesByStyle["emoji"]
	}

	// Apply config icon overrides (config takes priority over style preset)
	icons := petCfg.Icons
	if icons.Idle != "" {
		sprites.Idle = icons.Idle
	}
	if icons.Walking != "" {
		sprites.Walking = icons.Walking
	}
	if icons.Jumping != "" {
		sprites.Jumping = icons.Jumping
	}
	if icons.Playing != "" {
		sprites.Playing = icons.Playing
	}
	if icons.Eating != "" {
		sprites.Eating = icons.Eating
	}
	if icons.Sleeping != "" {
		sprites.Sleeping = icons.Sleeping
	}
	if icons.Happy != "" {
		sprites.Happy = icons.Happy
	}
	if icons.Hungry != "" {
		sprites.Hungry = icons.Hungry
	}
	if icons.Yarn != "" {
		sprites.Yarn = icons.Yarn
	}
	if icons.Food != "" {
		sprites.Food = icons.Food
	}
	if icons.Poop != "" {
		sprites.Poop = icons.Poop
	}
	if icons.Thought != "" {
		sprites.Thought = icons.Thought
	}
	if icons.Heart != "" {
		sprites.Heart = icons.Heart
	}
	if icons.Life != "" {
		sprites.Life = icons.Life
	}
	if icons.HungerIcon != "" {
		sprites.HungerIcon = icons.HungerIcon
	}
	if icons.HappyIcon != "" {
		sprites.HappyIcon = icons.HappyIcon
	}
	if icons.SadIcon != "" {
		sprites.SadIcon = icons.SadIcon
	}
	if icons.Ground != "" {
		sprites.Ground = icons.Ground
	}

	return sprites
}

// handlePetPlayAreaClick handles clicks within the pet play area
// clickX is the X position, petY is the Y in pet coordinate space (0=ground, 1=low air, 2=high air)
func (c *Coordinator) handlePetPlayAreaClick(clientID string, input *daemon.InputPayload, clickX, petY int) bool {
	c.stateMu.Lock()

	// Get sprite strings for width calculation
	sprites := c.getSprites()

	// Calculate safe play width using client-specific width (must match renderPetWidget)
	playWidth := c.getClientWidth(clientID)
	safePlayWidth := playWidth - 1
	if safePlayWidth < 5 {
		safePlayWidth = 5
	}

	// Get clamped positions (same as rendering does)
	// This ensures click detection matches what's displayed
	catPosX := c.pet.Pos.X
	if catPosX >= safePlayWidth {
		catPosX = safePlayWidth - 1
	}
	if catPosX < 0 {
		catPosX = 0
	}

	yarnPosX := c.pet.YarnPos.X
	if yarnPosX >= safePlayWidth {
		yarnPosX = safePlayWidth - 1
	}

	// Get cat sprite based on current state
	catSprite := sprites.Idle
	switch c.pet.State {
	case "walking":
		catSprite = sprites.Walking
	case "jumping":
		catSprite = sprites.Jumping
	case "playing":
		catSprite = sprites.Playing
	case "eating":
		catSprite = sprites.Eating
	case "sleeping":
		catSprite = sprites.Sleeping
	case "happy":
		catSprite = sprites.Happy
	case "hungry":
		catSprite = sprites.Hungry
	case "dead":
		catSprite = sprites.Dead
	}
	// Dead overrides everything
	if c.pet.IsDead {
		catSprite = sprites.Dead
	}
	catWidth := uniseg.StringWidth(catSprite)
	if catWidth < 1 {
		catWidth = 1
	}

	// Check if clicking on sleeping cat (💤 at position 0 on ground)
	// When sleeping, the cat is represented by 💤 in bottom left corner
	if c.pet.State == "sleeping" && petY == 0 {
		zzzWidth := uniseg.StringWidth("💤")
		if zzzWidth < 1 {
			zzzWidth = 2
		}
		if clickX >= 0 && clickX < zzzWidth {
			coordinatorDebugLog.Printf("    -> Clicked on sleeping cat (💤)! Waking up.")
			c.pet.State = "idle"
			c.pet.LastThought = randomThought("wakeup")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
	}

	// Check if clicking on cat (account for sprite display width)
	// Sprites like emojis display wider than their rune position
	// Use clamped position to match what's rendered on screen
	if c.pet.Pos.Y == petY && clickX >= catPosX && clickX < catPosX+catWidth {
		// If dead, clicking revives the pet
		if c.pet.IsDead {
			coordinatorDebugLog.Printf("    -> Clicked on dead pet! Reviving.")
			c.pet.IsDead = false
			c.pet.DeathTime = time.Time{}
			c.pet.StarvingStart = time.Time{}
			c.pet.Hunger = 50
			c.pet.Happiness = 50
			c.pet.State = "happy"
			c.pet.LastThought = "back from the void!"
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
		coordinatorDebugLog.Printf("    -> Clicked on cat at X=%d (cat rendered at %d, width=%d)! Petting.", clickX, catPosX, catWidth)
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		c.pet.LastThought = randomThought("petting")
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if clicking on poop (only on ground)
	if petY == 0 {
		poopWidth := uniseg.StringWidth(sprites.Poop)
		if poopWidth < 1 {
			poopWidth = 1
		}
		for i, poopX := range c.pet.PoopPositions {
			// Clamp poop position same as rendering
			clampedPoopX := poopX
			if clampedPoopX >= safePlayWidth {
				clampedPoopX = safePlayWidth - 1
			}
			if clampedPoopX < 0 {
				clampedPoopX = 0
			}
			if clickX >= clampedPoopX && clickX < clampedPoopX+poopWidth {
				coordinatorDebugLog.Printf("    -> Clicked on poop at X=%d (poop rendered at %d, width=%d)! Cleaning.", clickX, clampedPoopX, poopWidth)
				c.pet.PoopPositions = append(c.pet.PoopPositions[:i], c.pet.PoopPositions[i+1:]...)
				c.pet.TotalPoopsCleaned++
				c.pet.LastThought = "much better."
				petSnap := c.pet
				c.stateMu.Unlock()
				savePetStateData(petSnap)
				return true
			}
		}
	}

	// Check if clicking on mouse - help the pet catch it!
	if c.pet.MousePos.X >= 0 && petY == 0 {
		mouseWidth := uniseg.StringWidth(sprites.Mouse)
		if mouseWidth < 1 {
			mouseWidth = 1
		}
		clampedMouseX := c.pet.MousePos.X
		if clampedMouseX >= safePlayWidth {
			clampedMouseX = safePlayWidth - 1
		}
		if clampedMouseX < 0 {
			clampedMouseX = 0
		}
		if clickX >= clampedMouseX && clickX < clampedMouseX+mouseWidth {
			coordinatorDebugLog.Printf("    -> Clicked on mouse! Pet catches it.")
			c.pet.MousePos = pos2D{X: -1, Y: 0}
			c.pet.TotalMouseCatches++
			c.pet.Happiness = min(100, c.pet.Happiness+20)
			c.pet.State = "happy"
			c.pet.HasTarget = false
			c.pet.ActionPending = ""
			c.pet.LastThought = randomThought("mouse_kill")
			petSnap := c.pet
			c.stateMu.Unlock()
			savePetStateData(petSnap)
			return true
		}
	}

	// DEBUG: Click far left on ground (X=0) to spawn a mouse
	if petY == 0 && clickX == 0 && c.pet.MousePos.X < 0 {
		coordinatorDebugLog.Printf("    -> DEBUG: Spawning mouse!")
		c.pet.MouseDirection = 1
		c.pet.MousePos = pos2D{X: 0, Y: 0}
		c.pet.LastThought = randomThought("mouse_spot")
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Check if clicking on yarn (account for sprite width)
	// Use clamped position to match what's rendered on screen
	yarnWidth := uniseg.StringWidth(sprites.Yarn)
	if yarnWidth < 1 {
		yarnWidth = 1
	}
	if c.pet.YarnPos.Y == petY && clickX >= yarnPosX && clickX < yarnPosX+yarnWidth {
		coordinatorDebugLog.Printf("    -> Clicked on yarn at X=%d (yarn rendered at %d)! Moving it.", clickX, yarnPosX)
		// Toss the yarn to a new position using client-specific width
		width := playWidth
		newX := safeRandRange(2, width-2)
		c.pet.YarnPos = pos2D{X: newX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.TargetPos = pos2D{X: newX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "again!"
		petSnap := c.pet
		c.stateMu.Unlock()
		savePetStateData(petSnap)
		return true
	}

	// Otherwise, drop yarn at click position using client-specific width
	coordinatorDebugLog.Printf("    -> Empty space clicked, dropping yarn at X=%d", clickX)
	tossX := clickX
	if tossX < 2 {
		tossX = 2
	}
	if tossX >= playWidth-2 {
		tossX = playWidth - 1
	}
	// Start yarn at high air, let it fall
	c.pet.YarnPos = pos2D{X: tossX, Y: 2}
	c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
	c.pet.YarnPushCount = 0
	c.pet.TargetPos = pos2D{X: tossX, Y: 0}
	c.pet.HasTarget = true
	c.pet.ActionPending = "play"
	c.pet.State = "walking"
	c.pet.LastThought = "yarn!"
	petSnap := c.pet
	c.stateMu.Unlock()
	savePetStateData(petSnap)
	return true
}

// menuPosition carries mouse coordinates for positioning tmux display-menu
// at the exact click location (since renderers capture mouse events before tmux sees them)
type menuPosition struct {
	PaneID string // tmux pane ID where the click occurred
	X      int    // mouse X within the pane
	Y      int    // mouse Y within the pane
}

// menuPosArgs returns the tmux display-menu positioning flags
func (p menuPosition) args() []string {
	if p.PaneID != "" {
		return []string{
			"-t", p.PaneID,
			"-x", fmt.Sprintf("%d", p.X),
			"-y", fmt.Sprintf("%d", p.Y),
		}
	}
	// Fallback if no pane ID (shouldn't happen)
	return []string{"-x", "M", "-y", "M"}
}

// menuItemDef holds a menu item definition with its tmux command
type menuItemDef struct {
	Label     string
	Key       string
	Command   string // tmux command string to execute
	Separator bool
	Header    bool
}

var (
	markerOptionsOnce  sync.Once
	markerOptionsCache []daemon.MarkerOptionPayload
)

func markerOptions() []daemon.MarkerOptionPayload {
	markerOptionsOnce.Do(func() {
		catalog := kemoji.Gemoji()
		seen := make(map[string]struct{}, len(catalog))
		options := make([]daemon.MarkerOptionPayload, 0, len(catalog))
		for _, e := range catalog {
			symbol := strings.TrimSpace(e.Emoji)
			if symbol == "" {
				continue
			}
			name := strings.TrimSpace(e.Description)
			if name == "" && len(e.Aliases) > 0 {
				name = strings.ReplaceAll(strings.TrimSpace(e.Aliases[0]), "_", " ")
			}
			keywords := strings.Join(append(append([]string{}, e.Aliases...), e.Tags...), " ")
			if name == "" && strings.TrimSpace(keywords) == "" {
				continue
			}
			key := symbol + "|" + name
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			options = append(options, daemon.MarkerOptionPayload{
				Symbol:   symbol,
				Name:     name,
				Keywords: keywords,
			})
		}
		markerOptionsCache = options
	})
	return markerOptionsCache
}

// parseTmuxMenuArgs extracts menu title and items from tmux display-menu arguments
func parseTmuxMenuArgs(args []string) (string, []menuItemDef) {
	var title string
	var items []menuItemDef

	// Find the title and the start of item triples
	itemStart := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "display-menu", "-O":
			continue
		case "-T":
			if i+1 < len(args) {
				title = args[i+1]
				i++
			}
		case "-t", "-x", "-y":
			i++ // skip value
		default:
			itemStart = i
			goto parseItems
		}
	}
parseItems:
	if itemStart < 0 {
		return title, items
	}
	// Parse triples: label, key, command
	for i := itemStart; i+2 < len(args); i += 3 {
		label := args[i]
		key := args[i+1]
		cmd := args[i+2]

		if label == "" && key == "" && cmd == "" {
			items = append(items, menuItemDef{Separator: true})
		} else if strings.HasPrefix(strings.TrimLeft(label, " "), "-") {
			trimmed := strings.TrimLeft(label, " ")
			indent := len(label) - len(trimmed)
			items = append(items, menuItemDef{
				Label:  strings.Repeat(" ", indent) + strings.TrimPrefix(trimmed, "-"),
				Header: true,
			})
		} else {
			items = append(items, menuItemDef{
				Label:   label,
				Key:     key,
				Command: cmd,
			})
		}
	}

	return title, items
}

// executeOrSendMenu sends the menu to the renderer or falls back to tmux display-menu.
// Pane-header clients (clientID starts with "header:") always use tmux display-menu
// since the 1-line pane is too small for overlay menus.
func (c *Coordinator) executeOrSendMenu(clientID string, args []string, pos menuPosition) {
	// Pane-header clients can't show overlay menus - use tmux display-menu
	isHeaderClient := strings.HasPrefix(clientID, "header:")

	if c.OnSendMenu != nil && !isHeaderClient {
		title, items := parseTmuxMenuArgs(args)
		logEvent("MENU_SEND client=%s title=%s items=%d", clientID, title, len(items))

		// Store items for later execution
		c.pendingMenusMu.Lock()
		c.pendingMenus[clientID] = items
		c.pendingMenusMu.Unlock()

		// Convert to protocol items
		protoItems := make([]daemon.MenuItemPayload, len(items))
		for i, item := range items {
			protoItems[i] = daemon.MenuItemPayload{
				Label:     item.Label,
				Key:       item.Key,
				Separator: item.Separator,
				Header:    item.Header,
			}
		}

		c.OnSendMenu(clientID, &daemon.MenuPayload{
			Title: title,
			Items: protoItems,
			X:     pos.X,
			Y:     pos.Y,
		})
	} else {
		exec.Command("tmux", args...).Run()
	}
}

// HandleMenuSelect executes the tmux command for the selected menu item
func (c *Coordinator) HandleMenuSelect(clientID string, index int) {
	logEvent("MENU_SELECT_START client=%s index=%d", clientID, index)
	c.pendingMenusMu.Lock()
	items, ok := c.pendingMenus[clientID]
	delete(c.pendingMenus, clientID)
	c.pendingMenusMu.Unlock()

	if !ok || index < 0 || index >= len(items) {
		logEvent("MENU_SELECT_SKIP client=%s index=%d ok=%v items=%d", clientID, index, ok, len(items))
		return
	}

	item := items[index]
	logEvent("MENU_SELECT_ITEM client=%s index=%d label=%s cmd=%s", clientID, index, item.Label, item.Command)
	if item.Command == "" || item.Separator || item.Header {
		return
	}

	if strings.HasPrefix(item.Command, "tabby-marker-picker:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			targetBytes, err := base64.StdEncoding.DecodeString(parts[2])
			if err == nil {
				title := "Set Marker"
				if parts[1] == "group" {
					title = "Set Group Marker"
				}
				c.openMarkerPicker(clientID, parts[1], string(targetBytes), title)
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-color-picker:") {
		parts := strings.SplitN(item.Command, ":", 4)
		if len(parts) >= 3 {
			targetBytes, err := base64.StdEncoding.DecodeString(parts[2])
			if err == nil {
				title := "Pick Color"
				if parts[1] == "group" {
					title = "Pick Group Color"
				}
				currentColor := ""
				if len(parts) == 4 {
					currentColor = parts[3]
				}
				c.openColorPicker(clientID, parts[1], string(targetBytes), title, currentColor)
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-set-window-color:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			windowIndex, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err == nil {
				c.setWindowColor(windowIndex, parts[2])
			}
		}
		return
	}

	if strings.HasPrefix(item.Command, "tabby-set-window-icon:") {
		parts := strings.SplitN(item.Command, ":", 3)
		if len(parts) == 3 {
			windowIndex, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err == nil {
				c.setWindowIcon(windowIndex, parts[2])
			}
		}
		return
	}

	// Execute the tmux command via temp file (handles complex quoting correctly)
	executeTmuxCommand(item.Command)
}

// executeTmuxCommand executes a tmux command string by writing to a temp file
// and sourcing it, which correctly handles all quoting and escaping
func executeTmuxCommand(cmd string) {
	f, err := os.CreateTemp("", "tabby-cmd-*.conf")
	if err != nil {
		coordinatorDebugLog.Printf("Failed to create temp file for menu command: %v", err)
		return
	}
	defer os.Remove(f.Name())
	f.WriteString(cmd + "\n")
	f.Close()
	exec.Command("tmux", "source-file", f.Name()).Run()
}

func (c *Coordinator) openMarkerPicker(clientID, scope, target, title string) {
	if c.OnSendMarkerPicker == nil {
		return
	}
	options := markerOptions()
	if len(options) == 0 {
		return
	}
	c.OnSendMarkerPicker(clientID, &daemon.MarkerPickerPayload{
		Title:   title,
		Scope:   scope,
		Target:  target,
		Options: options,
	})
}

func (c *Coordinator) applyMarkerSelection(scope, target, markerValue string) bool {
	value := strings.TrimSpace(markerValue)

	if scope == "window" {
		windowIndex, err := strconv.Atoi(strings.TrimSpace(target))
		if err != nil {
			return false
		}
		c.setWindowIcon(windowIndex, value)
		return true
	}

	if scope == "group" {
		return c.setGroupMarkerExact(target, value)
	}

	return false
}

func (c *Coordinator) setGroupMarkerExact(groupName, marker string) bool {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return false
	}

	configPath := config.DefaultConfigPath()
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return false
	}
	group := config.FindGroup(cfg, groupName)
	if group == nil {
		return false
	}
	group.Theme.Icon = marker
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return false
	}

	c.stateMu.Lock()
	c.config = cfg
	c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	c.stateMu.Unlock()
	return true
}

// handleRightClick shows appropriate context menu based on what was clicked
func (c *Coordinator) handleRightClick(clientID string, input *daemon.InputPayload) {
	coordinatorDebugLog.Printf("handleRightClick: clientID=%s action=%s target=%s pane=%s x=%d y=%d",
		clientID, input.ResolvedAction, input.ResolvedTarget, input.PaneID, input.MouseX, input.MouseY)

	pos := menuPosition{
		PaneID: input.PaneID,
		X:      input.MouseX,
		Y:      input.MouseY,
	}
	// For header clients, use SourcePaneID (the header pane itself) for positioning
	// so the menu appears at the click location. The content pane comes from ResolvedTarget.
	if strings.HasPrefix(clientID, "header:") {
		if input.SourcePaneID != "" {
			pos.PaneID = input.SourcePaneID
			coordinatorDebugLog.Printf("handleRightClick: header client, using SourcePaneID=%s", input.SourcePaneID)
		}
	}
	switch input.ResolvedAction {
	case "select_window", "toggle_panes", "window_menu":
		// If clicking on far left (X < 2), show indicator menu; otherwise show window menu
		if input.ResolvedAction != "window_menu" && input.MouseX < 2 {
			c.showIndicatorContextMenu(clientID, input.ResolvedTarget, pos)
		} else {
			c.showWindowContextMenu(clientID, input.ResolvedTarget, pos)
		}
	case "select_pane", "pane_menu", "pane_grow", "pane_shrink":
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
	case "toggle_group", "group_header", "group_menu":
		c.showGroupContextMenu(clientID, input.ResolvedTarget, pos)
	case "sidebar_header_area", "sidebar_settings":
		c.showSidebarSettingsMenu(clientID, pos)
	case "header_context", "header_split_v", "header_split_h", "header_close", "header_select_pane":
		// Right-click on pane header -> show pane context menu
		c.showPaneContextMenu(clientID, input.ResolvedTarget, pos)
	default:
		coordinatorDebugLog.Printf("handleRightClick: unhandled action=%q target=%q", input.ResolvedAction, input.ResolvedTarget)
	}
}

// showWindowContextMenu displays the context menu for a window
func (c *Coordinator) showWindowContextMenu(clientID string, windowTarget string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	win := findWindowByTarget(c.windows, windowTarget)
	if win == nil {
		return
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
	}, pos.args()...)

	// Rename option - locks the name so syncWindowNames won't overwrite it
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%' ; set-window-option -t :%d @tabby_name_locked 1\"", win.Name, win.Index, win.Index)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option - allows syncWindowNames to auto-update from pane title
	unlockCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_name_locked", win.Index)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Separator before group/appearance section
	args = append(args, "", "", "")

	// --- Group & Appearance section (all window-group-related options together) ---

	// Move to Group submenu
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range c.config.Groups {
		if group.Name == "Default" {
			continue
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			setGroupCmd := fmt.Sprintf("set-window-option -t :%d @tabby_group '%s'", win.Index, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, setGroupCmd)
		}
	}

	// Remove from group option
	if win.Group != "" {
		removeCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_group", win.Index)
		args = append(args, "  Remove from Group", "0", removeCmd)
	}

	// Set Color submenu
	args = append(args, "-Set Tab Color", "", "")
	colorOptions := []struct {
		name string
		hex  string
		key  string
	}{
		{"Red", "#e74c3c", "r"},
		{"Orange", "#e67e22", "o"},
		{"Yellow", "#f1c40f", "y"},
		{"Green", "#27ae60", "g"},
		{"Blue", "#3498db", "b"},
		{"Purple", "#9b59b6", "p"},
		{"Pink", "#e91e63", "i"},
		{"Cyan", "#00bcd4", "c"},
		{"Gray", "#7f8c8d", "a"},
		{"Transparent", "transparent", "t"},
	}
	if !c.config.Sidebar.HidePredefinedColors {
		for _, color := range colorOptions {
			setColorCmd := fmt.Sprintf("tabby-set-window-color:%d:%s", win.Index, color.hex)
			args = append(args, fmt.Sprintf("  %s", color.name), color.key, setColorCmd)
		}
	}

	colorTarget := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(win.Index)))
	currentColor := win.CustomColor
	colorPickerCmd := fmt.Sprintf("tabby-color-picker:window:%s:%s", colorTarget, currentColor)
	args = append(args, "  Custom Color...", "h", colorPickerCmd)
	customColorCmd := fmt.Sprintf("command-prompt -p 'Hex color (#rrggbb):' \"set-window-option -t %s @tabby_color '%%%%%%%%'\"", windowTarget)
	args = append(args, "  Custom (Hex)...", "#", customColorCmd)
	resetColorCmd := fmt.Sprintf("set-window-option -t %s -u @tabby_color", windowTarget)
	args = append(args, "  Reset to Default", "d", resetColorCmd)

	// Set Marker — opens the searchable emoji/icon picker (same as group menu)
	if !strings.HasPrefix(clientID, "header:") {
		target := base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(win.Index)))
		searchCmd := fmt.Sprintf("tabby-marker-picker:window:%s", target)
		args = append(args, "Set Marker", "m", searchCmd)
		// Show remove option only if a marker is currently set
		if win.Icon != "" {
			resetIconCmd := fmt.Sprintf("tabby-set-window-icon:%d:", win.Index)
			args = append(args, "Remove Marker", "0", resetIconCmd)
		}
	}

	// Pin/Unpin option - pinned windows appear at the top of sidebar
	if win.Pinned {
		unpinCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_pinned", win.Index)
		args = append(args, "Unpin from Top", "p", unpinCmd)
	} else {
		pinCmd := fmt.Sprintf("set-window-option -t :%d @tabby_pinned 1", win.Index)
		args = append(args, "Pin to Top", "p", pinCmd)
	}

	// --- Window actions section ---
	args = append(args, "", "", "")

	// Collapse/Expand panes option (only for windows with multiple panes)
	contentPaneCount := 0
	for _, pane := range win.Panes {
		if isAuxiliaryPane(pane) {
			continue
		}
		contentPaneCount++
	}
	if contentPaneCount > 1 {
		if win.Collapsed {
			expandCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_collapsed", win.Index)
			args = append(args, "Expand Panes", "e", expandCmd)
		} else {
			collapseCmd := fmt.Sprintf("set-window-option -t :%d @tabby_collapsed 1", win.Index)
			args = append(args, "Collapse Panes", "c", collapseCmd)
		}
	}

	// Split options - use active pane ID to avoid index issues with header panes
	activePaneID := ""
	for _, p := range win.Panes {
		if isAuxiliaryPane(p) {
			continue
		}
		if p.Active {
			activePaneID = p.ID
			break
		}
	}
	if activePaneID == "" {
		for _, p := range win.Panes {
			if isAuxiliaryPane(p) {
				continue
			}
			activePaneID = p.ID
			break
		}
	}
	if activePaneID == "" && len(win.Panes) > 0 {
		activePaneID = win.Panes[0].ID
	}
	splitTarget := fmt.Sprintf(":%d", win.Index)
	if activePaneID != "" {
		splitTarget = activePaneID
	}
	if !c.isVerticalStackedPane(win, activePaneID) {
		splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '#{pane_current_path}'", win.Index, splitTarget)
		splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '#{pane_current_path}'", win.Index, splitTarget)
		args = append(args, "Split Vertical |", "|", splitVCmd)
		args = append(args, "Split Horizontal -", "-", splitHCmd)
	}

	// --- Utilities ---
	args = append(args, "", "", "")

	// Open in Finder
	openFinderCmd := "run-shell 'open \"#{pane_current_path}\"'"
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// --- Destructive ---
	args = append(args, "", "", "")

	exe, _ := os.Executable()
	killWindowScript := filepath.Join(filepath.Dir(exe), "..", "scripts", "kill_window.sh")
	killCmd := fmt.Sprintf("run-shell '%s %d'", killWindowScript, win.Index)
	args = append(args, "Kill", "k", killCmd)

	c.executeOrSendMenu(clientID, args, pos)
}

func (c *Coordinator) isVerticalStackedPane(win *tmux.Window, paneID string) bool {
	if win == nil || paneID == "" {
		return false
	}

	maxWidth := 0
	var target *tmux.Pane
	for i := range win.Panes {
		pane := &win.Panes[i]
		if isAuxiliaryPane(*pane) {
			continue
		}
		if pane.Width > maxWidth {
			maxWidth = pane.Width
		}
		if pane.ID == paneID {
			target = pane
		}
	}
	if target == nil || maxWidth == 0 {
		return false
	}
	if target.Width < maxWidth {
		return false
	}

	for i := range win.Panes {
		pane := &win.Panes[i]
		if isAuxiliaryPane(*pane) || pane.ID == paneID {
			continue
		}
		if pane.Width == target.Width && pane.Top != target.Top {
			return true
		}
	}

	return false
}

// showPaneContextMenu displays the context menu for a pane
func (c *Coordinator) showPaneContextMenu(clientID string, paneID string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the pane
	var pane *tmux.Pane
	var windowIdx int
	var window *tmux.Window
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				pane = &c.windows[i].Panes[j]
				window = &c.windows[i]
				windowIdx = c.windows[i].Index
				break
			}
		}
		if pane != nil {
			break
		}
	}
	if pane == nil || window == nil {
		return
	}

	// Use locked title, then title, then command for display
	paneLabel := pane.Command
	if pane.LockedTitle != "" {
		paneLabel = pane.LockedTitle
	} else if pane.Title != "" && pane.Title != pane.Command {
		paneLabel = pane.Title
	}

	menuArgs := []string{"display-menu"}
	if pos.PaneID != "" {
		menuArgs = append(menuArgs, "-M")
	}
	menuArgs = append(menuArgs, "-T", fmt.Sprintf("Pane %d.%d: %s", windowIdx, pane.Index, paneLabel))
	args := append(menuArgs, pos.args()...)

	// Rename option
	currentTitle := pane.LockedTitle
	if currentTitle == "" {
		currentTitle = pane.Title
	}
	if currentTitle == "" {
		currentTitle = pane.Command
	}
	renameCmd := fmt.Sprintf("command-prompt -I '%s' -p 'Pane name:' \"set-option -p -t %s @tabby_pane_title '%%%%'\"", currentTitle, pane.ID)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option
	unlockCmd := fmt.Sprintf("set-option -p -t %s -u @tabby_pane_title ; select-pane -t %s -T ''", pane.ID, pane.ID)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// For header clients, -t targets the header pane (for positioning), so #{pane_current_path}
	// would resolve to the header pane's path. Pre-resolve from the content pane instead.
	panePath := "#{pane_current_path}"
	if strings.HasPrefix(clientID, "header:") {
		if out, err := exec.Command("tmux", "display-message", "-t", pane.ID, "-p", "#{pane_current_path}").Output(); err == nil {
			resolved := strings.TrimSpace(string(out))
			if resolved != "" {
				panePath = resolved
			}
		}
	}

	// Separator
	args = append(args, "", "", "")

	// Split options
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '%s'", windowIdx, pane.ID, panePath)
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '%s'", windowIdx, pane.ID, panePath)
	args = append(args, "Split Vertical |", "|", splitVCmd)
	args = append(args, "Split Horizontal -", "-", splitHCmd)

	contentCount := 0
	for _, p := range window.Panes {
		if !isAuxiliaryPane(p) {
			contentCount++
		}
	}
	if contentCount > 1 {
		collapseScript := c.getScriptPath("toggle_pane_collapse.sh")
		if collapseScript != "" {
			args = append(args, "", "", "")
			collapsedVal, _ := exec.Command("tmux", "show-options", "-pqv", "-t", pane.ID, "@tabby_pane_collapsed").Output()
			collapseLabel := "Collapse Pane"
			if strings.TrimSpace(string(collapsedVal)) == "1" {
				collapseLabel = "Expand Pane"
			}
			args = append(args, collapseLabel, "c", fmt.Sprintf("run-shell '%s -t %s'", collapseScript, pane.ID))
		}
		args = append(args, "", "", "")
		args = append(args, "Resize Down", "j", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -D 5", windowIdx, pane.ID))
		args = append(args, "Resize Up", "k", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -U 5", windowIdx, pane.ID))
		args = append(args, "Resize Right", "l", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -R 5", windowIdx, pane.ID))
		args = append(args, "Resize Left", "h", fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; resize-pane -L 5", windowIdx, pane.ID))
	}

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", windowIdx, pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Break pane to new window (preserving group assignment)
	breakCmd := fmt.Sprintf("break-pane -s %s", pane.ID)
	// Find the group this window belongs to and assign it to the new window
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Index == windowIdx && group.Name != "" {
				breakCmd += fmt.Sprintf(" ; set-window-option @tabby_group '%s'", group.Name)
				break
			}
		}
	}
	args = append(args, "Break to New Window", "b", breakCmd)

	// Move to Group submenu
	args = append(args, "", "", "") // Separator
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range c.config.Groups {
		if group.Name == "Default" {
			continue
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			moveCmd := fmt.Sprintf("break-pane -s %s ; set-window-option @tabby_group '%s'", pane.ID, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, moveCmd)
		}
	}

	// Remove from group option (if pane's window has a group)
	windowGroup := ""
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.Index == windowIdx && group.Name != "" {
				windowGroup = group.Name
				break
			}
		}
	}
	if windowGroup != "" {
		removeCmd := fmt.Sprintf("break-pane -s %s ; set-window-option -u @tabby_group", pane.ID)
		args = append(args, "  Remove from Group", "0", removeCmd)
	}

	// Open in Finder
	openFinderCmd := fmt.Sprintf("run-shell 'open \"%s\"'", panePath)
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Close pane (save layout first to preserve sibling ratios)
	exe, _ := os.Executable()
	killWrapper := filepath.Join(filepath.Dir(exe), "..", "scripts", "kill_pane_wrapper.sh")
	args = append(args, "Close Pane", "x", fmt.Sprintf("run-shell '%s -t %s'", killWrapper, pane.ID))

	c.executeOrSendMenu(clientID, args, pos)
}

// showGroupContextMenu displays the context menu for a group header
func (c *Coordinator) showGroupContextMenu(clientID string, groupName string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the group
	var group *grouping.GroupedWindows
	for i := range c.grouped {
		if c.grouped[i].Name == groupName {
			group = &c.grouped[i]
			break
		}
	}
	if group == nil {
		return
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Group: %s (%d windows)", group.Name, len(group.Windows)),
	}, pos.args()...)

	// Get working directory for new windows in this group
	var workingDir string
	coordinatorDebugLog.Printf("showGroupContextMenu: looking for working_dir for group=%s, config has %d groups", group.Name, len(c.config.Groups))
	for _, cfgGroup := range c.config.Groups {
		coordinatorDebugLog.Printf("  checking cfgGroup=%s workingDir=%s", cfgGroup.Name, cfgGroup.WorkingDir)
		if cfgGroup.Name == group.Name && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			coordinatorDebugLog.Printf("  MATCH! workingDir=%s", workingDir)
			// Expand ~ to home directory
			if strings.HasPrefix(workingDir, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					workingDir = filepath.Join(home, workingDir[2:])
					coordinatorDebugLog.Printf("  expanded to=%s", workingDir)
				}
			}
			break
		}
	}

	newWindowPath := "#{pane_current_path}"
	if workingDir != "" {
		newWindowPath = workingDir
	}
	coordinatorDebugLog.Printf("showGroupContextMenu: final path=%s", newWindowPath)

	newWindowScript := c.getScriptPath("new_window_with_group.sh")
	groupEsc := strings.ReplaceAll(group.Name, "'", "'\"'\"'")
	pathEsc := strings.ReplaceAll(newWindowPath, "'", "'\"'\"'")
	if newWindowScript != "" {
		if group.Name != "Default" {
			newWindowCmd := fmt.Sprintf("set-option -g @tabby_new_window_group '%s' ; set-option -g @tabby_new_window_path '%s' ; run-shell '%s'", groupEsc, pathEsc, newWindowScript)
			args = append(args, fmt.Sprintf("New %s Window", group.Name), "n", newWindowCmd)
		} else {
			newWindowCmd := fmt.Sprintf("set-option -g @tabby_new_window_group 'Default' ; set-option -g @tabby_new_window_path '%s' ; run-shell '%s'", pathEsc, newWindowScript)
			args = append(args, "New Window", "n", newWindowCmd)
		}
	} else {
		dirArg := fmt.Sprintf("'%s'", pathEsc)
		if group.Name != "Default" {
			newWindowCmd := fmt.Sprintf("new-window -c %s ; set-window-option @tabby_group '%s'", dirArg, groupEsc)
			args = append(args, fmt.Sprintf("New %s Window", group.Name), "n", newWindowCmd)
		} else {
			newWindowCmd := fmt.Sprintf("new-window -c %s", dirArg)
			args = append(args, "New Window", "n", newWindowCmd)
		}
	}

	toggleGroupScript := c.getScriptPath("toggle_group_collapse.sh")
	if toggleGroupScript != "" {
		if c.collapsedGroups[group.Name] {
			expandCmd := fmt.Sprintf("run-shell '%s \"%s\" expand'", toggleGroupScript, group.Name)
			args = append(args, "Expand Group", "e", expandCmd)
		} else {
			collapseCmd := fmt.Sprintf("run-shell '%s \"%s\" collapse'", toggleGroupScript, group.Name)
			args = append(args, "Collapse Group", "c", collapseCmd)
		}
	}

	// --- Group settings section ---
	renameScript := c.getScriptPath("rename_group.sh")
	colorScript := c.getScriptPath("set_group_color.sh")
	markerScript := c.getScriptPath("set_group_marker.sh")
	workingDirScript := c.getScriptPath("set_group_working_dir.sh")
	newGroupScript := c.getScriptPath("new_group.sh")
	deleteScript := c.getScriptPath("delete_group.sh")

	hasGroupSettings := renameScript != "" || colorScript != "" || markerScript != "" || workingDirScript != "" || newGroupScript != ""
	if hasGroupSettings {
		args = append(args, "", "", "")
	}

	if renameScript != "" {
		renameCmd := fmt.Sprintf(
			"command-prompt -I '%s' -p 'New name:' \"run-shell '%s \\\"%s\\\" \\\"%%%%\\\"'\"",
			group.Name,
			renameScript,
			group.Name,
		)
		args = append(args, "Rename", "r", renameCmd)
	}

	if colorScript != "" {
		args = append(args, "-Change Color", "", "")
		colorOptions := []struct {
			name string
			hex  string
			key  string
		}{
			{"Red", "#e74c3c", "r"},
			{"Orange", "#e67e22", "o"},
			{"Yellow", "#f1c40f", "y"},
			{"Green", "#27ae60", "g"},
			{"Blue", "#3498db", "b"},
			{"Purple", "#9b59b6", "p"},
			{"Pink", "#e91e63", "i"},
			{"Cyan", "#00bcd4", "c"},
			{"Gray", "#7f8c8d", "a"},
			{"Transparent", "transparent", "t"},
		}
		if !c.config.Sidebar.HidePredefinedColors {
			for _, color := range colorOptions {
				setColorCmd := fmt.Sprintf("run-shell '%s \\\"%s\\\" \\\"%s\\\"'", colorScript, group.Name, color.hex)
				args = append(args, fmt.Sprintf("  %s", color.name), color.key, setColorCmd)
			}
		}

		groupColorTarget := base64.StdEncoding.EncodeToString([]byte(group.Name))
		groupCurrentColor := group.Theme.Bg
		colorPickerCmd := fmt.Sprintf("tabby-color-picker:group:%s:%s", groupColorTarget, groupCurrentColor)
		args = append(args, "  Custom Color...", "h", colorPickerCmd)
		customColorCmd := fmt.Sprintf(
			"command-prompt -p 'Hex color (#rrggbb):' \"run-shell '%s \\\"%s\\\" \\\"%%%%%%%%\\\"'\"",
			colorScript, group.Name,
		)
		args = append(args, "  Custom (Hex)...", "#", customColorCmd)
	}

	canShowMarkerPicker := c.OnSendMenu != nil && !strings.HasPrefix(clientID, "header:")
	if canShowMarkerPicker {
		groupTarget := base64.StdEncoding.EncodeToString([]byte(group.Name))
		searchCmd := fmt.Sprintf("tabby-marker-picker:group:%s", groupTarget)
		args = append(args, "Set Marker", "m", searchCmd)
		currentIcon := strings.TrimSpace(group.Theme.Icon)
		if currentIcon != "" && markerScript != "" {
			removeIconCmd := fmt.Sprintf("run-shell '%s \\\"%s\\\" \\\"\\\"'", markerScript, group.Name)
			args = append(args, "Remove Marker", "0", removeIconCmd)
		}
	}

	if workingDirScript != "" {
		currentWorkingDir := workingDir
		if currentWorkingDir == "" {
			currentWorkingDir = "~"
		}
		setWorkingDirCmd := fmt.Sprintf(
			"command-prompt -I '%s' -p 'Working directory:' \"run-shell '%s \\\"%s\\\" \\\"%%%%\\\"'\"",
			currentWorkingDir,
			workingDirScript,
			group.Name,
		)
		args = append(args, "Set Working Directory", "w", setWorkingDirCmd)
	}

	if newGroupScript != "" {
		newGroupCmd := fmt.Sprintf(
			"command-prompt -p 'New group name:' \"run-shell '%s %%%%  '\"",
			newGroupScript,
		)
		args = append(args, "Create New Group", "G", newGroupCmd)
	}

	// --- Destructive actions ---
	hasDestructive := deleteScript != "" || len(group.Windows) > 0
	if hasDestructive {
		args = append(args, "", "", "")
	}

	if deleteScript != "" {
		deleteCmd := fmt.Sprintf(
			"confirm-before -p 'Delete group %s? (y/n)' \"run-shell '%s \\\"%s\\\"'\"",
			group.Name,
			deleteScript,
			group.Name,
		)
		args = append(args, "Delete Group", "d", deleteCmd)
	}

	// Close all windows in group (only if group has windows)
	if len(group.Windows) > 0 {
		var killCmds []string
		for _, win := range group.Windows {
			killCmds = append(killCmds, fmt.Sprintf("kill-window -t %s", win.ID))
		}
		killAllCmd := strings.Join(killCmds, " ; ")
		confirmCmd := fmt.Sprintf(`confirm-before -p "Close all %d windows in %s? (y/n)" "%s"`,
			len(group.Windows), group.Name, killAllCmd)
		args = append(args, "Close All Windows", "x", confirmCmd)
	}

	c.executeOrSendMenu(clientID, args, pos)
}

// createNewWindowInCurrentGroup creates a new window in the same group as the
// current window, using the group's configured working_dir if available.
//
// Delegates to the bin/new-window binary for atomic creation: the sidebar
// renderer is spawned BEFORE the user sees the window, eliminating the
// "spazzing" UX issue caused by the old hook-storm approach.
func (c *Coordinator) createNewWindowInCurrentGroup(clientID string) {
	logEvent("NEW_WINDOW_START client=%s", clientID)

	// Query tmux for active window ID BEFORE acquiring the lock to avoid
	// holding stateMu during external I/O.
	windowID := clientID
	if out, err := tmuxOutputCtx("display-message", "-p", "#{window_id}"); err == nil {
		if id := strings.TrimSpace(string(out)); id != "" {
			windowID = id
		}
	}

	c.stateMu.RLock()

	var currentGroup string
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == windowID {
				currentGroup = group.Name
				break
			}
		}
		if currentGroup != "" {
			break
		}
	}

	// Look up working directory from config
	var workingDir string
	for _, cfgGroup := range c.config.Groups {
		if cfgGroup.Name == currentGroup && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			break
		}
	}

	c.stateMu.RUnlock()

	// Resolve working directory (expand ~/)
	if workingDir != "" && strings.HasPrefix(workingDir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			workingDir = filepath.Join(home, workingDir[2:])
		}
	}

	// Set override options that the new-window binary reads.
	// These are cleared by the binary after use.
	if currentGroup != "" && currentGroup != "Default" {
		exec.Command("tmux", "set-option", "-g", "@tabby_new_window_group", currentGroup).Run()
	}
	if workingDir != "" {
		exec.Command("tmux", "set-option", "-g", "@tabby_new_window_path", workingDir).Run()
	}

	// Find the new-window binary (sibling of this daemon binary)
	newWindowBin := ""
	if exe, err := os.Executable(); err == nil {
		newWindowBin = filepath.Join(filepath.Dir(exe), "new-window")
	}

	if newWindowBin != "" {
		if _, err := os.Stat(newWindowBin); err == nil {
			// Atomic path: binary spawns sidebar before switching, no hook storm.
			args := []string{"-session", c.sessionID}
			logEvent("NEW_WINDOW_BINARY bin=%s session=%s group=%s", newWindowBin, c.sessionID, currentGroup)
			if err := exec.Command(newWindowBin, args...).Run(); err != nil {
				logEvent("NEW_WINDOW_BINARY_ERR err=%v (falling back to legacy)", err)
				// Fall through to legacy path below
				newWindowBin = ""
			} else {
				lastNewWindowCreation = time.Now()
				return
			}
		}
	}

	// Legacy fallback: create window (focused), assign group, let hook chain handle renderer.
	// Used when bin/new-window is not built (e.g. fresh clone without install.sh).
	logEvent("NEW_WINDOW_LEGACY session=%s group=%s", c.sessionID, currentGroup)
	args := []string{"new-window", "-P", "-F", "#{window_id}", "-t", c.sessionID + ":"}
	if workingDir != "" {
		args = append(args, "-c", workingDir)
	}

	out, err := exec.Command("tmux", args...).CombinedOutput()
	newWindowIDLegacy := strings.TrimSpace(string(out))
	logEvent("NEW_WINDOW_LEGACY_RESULT id=%s err=%v", newWindowIDLegacy, err)

	if newWindowIDLegacy != "" && currentGroup != "" && currentGroup != "Default" {
		exec.Command("tmux", "set-window-option", "-t", newWindowIDLegacy, "@tabby_group", currentGroup).Run()
	}

	if newWindowIDLegacy != "" {
		exec.Command("tmux", "set-option", "-g", "@tabby_new_window_id", newWindowIDLegacy).Run()
		exec.Command("tmux", "select-window", "-t", newWindowIDLegacy).Run()

		go func(id string) {
			time.Sleep(2 * time.Second)
			if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_new_window_id").Output(); err == nil {
				if strings.TrimSpace(string(out)) == id {
					exec.Command("tmux", "set-option", "-gu", "@tabby_new_window_id").Run()
				}
			}
		}(newWindowIDLegacy)
	}

	lastNewWindowCreation = time.Now()
}

// showIndicatorContextMenu displays the context menu for window indicators (busy, bell, etc.)
func (c *Coordinator) showIndicatorContextMenu(clientID string, windowTarget string, pos menuPosition) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	win := findWindowByTarget(c.windows, windowTarget)
	if win == nil {
		return
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Alerts: Window %d", win.Index),
	}, pos.args()...)

	// Busy indicator toggle
	if win.Busy {
		clearBusyCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_busy", win.Index)
		args = append(args, "Clear Busy", "b", clearBusyCmd)
	} else {
		setBusyCmd := fmt.Sprintf("set-window-option -t :%d @tabby_busy 1", win.Index)
		args = append(args, "Set Busy", "b", setBusyCmd)
	}

	// Input indicator toggle
	if win.Input {
		clearInputCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_input", win.Index)
		args = append(args, "Clear Input Needed", "i", clearInputCmd)
	} else {
		setInputCmd := fmt.Sprintf("set-window-option -t :%d @tabby_input 1", win.Index)
		args = append(args, "Set Input Needed", "i", setInputCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Bell indicator toggle
	if win.Bell {
		clearBellCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_bell", win.Index)
		args = append(args, "Clear Bell", "l", clearBellCmd)
	} else {
		setBellCmd := fmt.Sprintf("set-window-option -t :%d @tabby_bell 1", win.Index)
		args = append(args, "Trigger Bell", "l", setBellCmd)
	}

	// Activity indicator toggle
	if win.Activity {
		clearActivityCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_activity", win.Index)
		args = append(args, "Clear Activity", "a", clearActivityCmd)
	} else {
		setActivityCmd := fmt.Sprintf("set-window-option -t :%d @tabby_activity 1", win.Index)
		args = append(args, "Set Activity", "a", setActivityCmd)
	}

	// Silence indicator toggle
	if win.Silence {
		clearSilenceCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_silence", win.Index)
		args = append(args, "Clear Silence", "s", clearSilenceCmd)
	} else {
		setSilenceCmd := fmt.Sprintf("set-window-option -t :%d @tabby_silence 1", win.Index)
		args = append(args, "Set Silence", "s", setSilenceCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Clear all indicators
	clearAllCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_busy ; set-window-option -t :%d -u @tabby_input ; set-window-option -t :%d -u @tabby_bell ; set-window-option -t :%d -u @tabby_activity ; set-window-option -t :%d -u @tabby_silence", win.Index, win.Index, win.Index, win.Index, win.Index)
	args = append(args, "Clear All Alerts", "c", clearAllCmd)

	c.executeOrSendMenu(clientID, args, pos)
}

// getToggleScript returns the path to the toggle_sidebar_daemon.sh script
func (c *Coordinator) getToggleScript() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "scripts", "toggle_sidebar_daemon.sh")
}

func (c *Coordinator) getScriptPath(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "scripts", name)
}

// showSidebarSettingsMenu displays a context menu for sidebar settings
func (c *Coordinator) showSidebarSettingsMenu(clientID string, pos menuPosition) {
	toggleScript := c.getToggleScript()

	// Build restart command: toggle off, wait, toggle on (runs in background via tmux)
	restartCmd := func(setCmd string) string {
		if toggleScript == "" {
			return setCmd
		}
		return fmt.Sprintf("%s; run-shell -b \"'%s'; sleep 0.3; '%s'\"", setCmd, toggleScript, toggleScript)
	}

	args := append([]string{
		"display-menu",
		"-O",
		"-T", "Sidebar Settings",
	}, pos.args()...)

	// Position options (restart sidebar to move it)
	args = append(args, "Position: Left", "l", restartCmd("set-option -g @tabby_sidebar_position left"))
	args = append(args, "Position: Right", "r", restartCmd("set-option -g @tabby_sidebar_position right"))

	// Separator
	args = append(args, "", "", "")

	// Mode options (restart to apply)
	args = append(args, "Mode: Full Height", "f", restartCmd("set-option -g @tabby_sidebar_mode full"))
	args = append(args, "Mode: Partial", "p", restartCmd("set-option -g @tabby_sidebar_mode partial"))

	// Separator
	args = append(args, "", "", "")

	// Pane headers toggle (no restart needed, daemon handles it)
	args = append(args, "Pane Headers: On", "h", "set-option -g @tabby_pane_headers on")
	args = append(args, "Pane Headers: Off", "o", "set-option -g @tabby_pane_headers off")

	// Separator
	args = append(args, "", "", "")

	// Prefix mode toggle (flat window list with group prefixes vs hierarchy)
	c.stateMu.RLock()
	prefixMode := c.config.Sidebar.PrefixMode
	c.stateMu.RUnlock()
	if prefixMode {
		args = append(args, "Display: Prefix Mode", "d", "set-option -g @tabby_prefix_mode 0")
	} else {
		args = append(args, "Display: Grouped", "d", "set-option -g @tabby_prefix_mode 1")
	}

	// Separator
	args = append(args, "", "", "")

	// Reset width (set to 25 and sync all sidebars)
	resetCmd := `set-option -gq @tabby_sidebar_width 25; run-shell -b 'for p in $(tmux list-panes -a -F "#{pane_id} #{pane_current_command}" | grep sidebar-renderer | cut -d" " -f1); do tmux resize-pane -t $p -x 25; done'`
	args = append(args, "Reset Width (25)", "w", resetCmd)

	// Sync Width toggle
	var win *tmux.Window
	c.stateMu.RLock()
	for i := range c.windows {
		for _, p := range c.windows[i].Panes {
			if p.ID == pos.PaneID {
				win = &c.windows[i]
				break
			}
		}
		if win != nil {
			break
		}
	}
	c.stateMu.RUnlock()

	if win != nil {
		args = append(args, "", "", "")
		if win.SyncWidth {
			args = append(args, "Sync Width: On", "s", fmt.Sprintf("set-window-option -t :%d @tabby_sync_width 0", win.Index))
		} else {
			snapCmd := fmt.Sprintf("set-window-option -t :%d @tabby_sync_width 1 ; run-shell -b 'tmux resize-pane -t %s -x %d'",
				win.Index, pos.PaneID, c.globalWidth)
			args = append(args, "Sync Width: Off", "s", snapCmd)
		}
	}

	c.executeOrSendMenu(clientID, args, pos)
}

// handleKeyInput processes keyboard events
func (c *Coordinator) handleKeyInput(clientID string, input *daemon.InputPayload) {
	switch input.Key {
	case "r":
		c.RefreshWindows()
	case "R":
		cfg, err := config.LoadConfig(config.DefaultConfigPath())
		if err == nil {
			c.stateMu.Lock()
			c.config = cfg
			c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
			c.computeVisualPositions()
			moves := c.syncWindowIndices()
			c.stateMu.Unlock()
			// Execute deferred window move ops outside the lock.
			for _, op := range moves {
				tmuxRun("move-window", "-s", op.src, "-t", op.dst)
			}
		}
	case "m":
		// Open marker picker for active window
		c.stateMu.RLock()
		activeWindowIndex := -1
		for i := range c.windows {
			if c.windows[i].Active {
				activeWindowIndex = c.windows[i].Index
				break
			}
		}
		c.stateMu.RUnlock()

		if activeWindowIndex >= 0 {
			c.openMarkerPicker(clientID, "window", strconv.Itoa(activeWindowIndex), "Set Marker")
		}
	}
}

// triggerActionFromThought parses an LLM thought and triggers matching pet behavior
func (c *Coordinator) triggerActionFromThought(thought string, maxX int) {
	lowerThought := strings.ToLower(thought)

	// Skip if already doing something
	if c.pet.State != "idle" || c.pet.HasTarget {
		return
	}

	// Map keywords to actions
	// Walking/exploring
	if strings.Contains(lowerThought, "wander") ||
		strings.Contains(lowerThought, "explor") ||
		strings.Contains(lowerThought, "roam") ||
		strings.Contains(lowerThought, "patrol") ||
		strings.Contains(lowerThought, "walk") ||
		strings.Contains(lowerThought, "going") ||
		strings.Contains(lowerThought, "move") {
		c.pet.State = "walking"
		c.pet.Direction = []int{-1, 1}[rand.Intn(2)]
		targetX := rand.Intn(maxX)
		c.pet.TargetPos = pos2D{X: targetX, Y: 0}
		c.pet.HasTarget = true
		return
	}

	// Jumping
	if strings.Contains(lowerThought, "jump") ||
		strings.Contains(lowerThought, "leap") ||
		strings.Contains(lowerThought, "bounce") ||
		strings.Contains(lowerThought, "air") ||
		strings.Contains(lowerThought, "zoom") {
		c.pet.State = "jumping"
		c.pet.Pos.Y = 2
		return
	}

	// Playing with yarn
	if strings.Contains(lowerThought, "yarn") ||
		strings.Contains(lowerThought, "play") ||
		strings.Contains(lowerThought, "chase") ||
		strings.Contains(lowerThought, "catch") {
		if c.pet.YarnPos.X >= 0 {
			c.pet.TargetPos = pos2D{X: c.pet.YarnPos.X, Y: 0}
			c.pet.HasTarget = true
			c.pet.ActionPending = "play"
			c.pet.State = "walking"
		}
		return
	}

	// Happy/content
	if strings.Contains(lowerThought, "happy") ||
		strings.Contains(lowerThought, "content") ||
		strings.Contains(lowerThought, "purr") ||
		strings.Contains(lowerThought, "nice") ||
		strings.Contains(lowerThought, "good") {
		c.pet.State = "happy"
		return
	}

	// Sleepy/nap
	if strings.Contains(lowerThought, "nap") ||
		strings.Contains(lowerThought, "sleep") ||
		strings.Contains(lowerThought, "tired") ||
		strings.Contains(lowerThought, "zzz") ||
		strings.Contains(lowerThought, "rest") {
		c.pet.State = "sleeping"
		return
	}
}

// Default pet thoughts by state
var defaultPetThoughts = map[string][]string{
	"hungry":      {"food. now.", "the bowl. it echoes.", "starving. dramatically.", "hunger level: critical."},
	"poop":        {"that won't clean itself.", "i made you a gift.", "cleanup crew needed.", "ahem. the floor."},
	"happy":       {"acceptable.", "fine. you may stay.", "feeling good.", "not bad.", "this is nice."},
	"yarn":        {"the yarn. it calls.", "must... catch...", "yarn acquired.", "got it!"},
	"sleepy":      {"nap time.", "zzz...", "five more minutes.", "so tired."},
	"idle":        {"chillin'.", "vibin'.", "just here.", "sup.", "...", "waiting.", "*yawn*", "hmm."},
	"walking":     {"exploring.", "on the move.", "wandering.", "going places."},
	"jumping":     {"wheee!", "boing!", "up up up!", "airborne."},
	"petting":     {"mmm...", "yes, there.", "acceptable.", "more.", "don't stop.", "nice."},
	"starving":    {"this is it.", "so hungry...", "fading...", "remember me.", "tell them... i was good."},
	"guilt":       {"i trusted you.", "is this how it ends?", "the neglect.", "you did this.", "betrayal."},
	"dead":        {"...", "x_x", "[silence]", "gone.", "rip."},
	"mouse_spot":  {"intruder.", "prey detected.", "nature calls.", "the hunt begins.", "i see you."},
	"mouse_chase": {"can't escape.", "almost...", "you're mine.", "gotcha soon.", "so close..."},
	"mouse_catch": {"victory.", "natural order.", "delicious chaos.", "another conquest.", "the circle of life."},
	"mouse_kill":  {"blender time.", "yeet into void.", "tiny skateboard accident.", "spontaneous combustion.", "piano from above.", "anvil delivery.", "surprise trapdoor.", "rocket malfunction."},
	"poop_jump":   {"ew ew ew!", "not stepping in that.", "parkour!", "leap of faith.", "over the obstacle.", "nope.", "gross.", "hygiene first."},
	"wakeup":      {"*yawn*", "good nap.", "what year is it?", "back online.", "rested.", "that was nice.", "5 more minutes...", "ok ok i'm up."},
}

// randomThought returns a random thought from the given category
func randomThought(category string) string {
	thoughts, ok := defaultPetThoughts[category]
	if !ok || len(thoughts) == 0 {
		thoughts = defaultPetThoughts["idle"]
	}
	return thoughts[rand.Intn(len(thoughts))]
}

// GetSidebarBg returns the configured sidebar background color.
func (c *Coordinator) GetSidebarBg() string {
	// Config override takes priority
	if c.config.Sidebar.Colors.Bg != "" {
		return c.config.Sidebar.Colors.Bg
	}
	// Then use theme
	if c.theme != nil {
		return c.theme.SidebarBg
	}
	// Fallback to detector
	return c.bgDetector.GetDefaultSidebarBg()
}

// applyBackgroundFill applies the sidebar background color to all content lines
// ensuring the entire sidebar area has a consistent background.
// It also re-injects the bg escape after any ANSI resets within the line
// so background color survives style resets mid-line.
func (c *Coordinator) applyBackgroundFill(content string, bgColor string, width int) string {
	if bgColor == "" {
		return content
	}

	// Use lipgloss to generate a profile-aware background escape sequence
	// rather than hardcoding TrueColor (\x1b[48;2;R;G;Bm). This lets
	// 256-color clients (e.g. Mosh) receive the correct escape format.
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	// Render a null-byte probe to extract just the opening escape sequence.
	rendered := bgStyle.Render("\x00")
	parts := strings.SplitN(rendered, "\x00", 2)
	bgEsc := ""
	if len(parts) >= 1 {
		bgEsc = parts[0]
	}
	resetEsc := "\x1b[0m"

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Re-inject bg escape after any ANSI resets within the line
		// This ensures the background persists even when styles are reset mid-line
		line = strings.ReplaceAll(line, resetEsc, resetEsc+bgEsc)

		// Get visual width of line (stripping ANSI codes)
		plainLine := stripAnsi(line)
		visualWidth := uniseg.StringWidth(plainLine)

		// Calculate padding needed
		padding := ""
		if visualWidth < width {
			padding = strings.Repeat(" ", width-visualWidth)
		}

		// Wrap entire line: bg color prefix + content + padding + reset
		lines[i] = bgEsc + line + padding + resetEsc
	}

	return strings.Join(lines, "\n")
}

func fixHeaderHeightsInWindow(paneID string) {
	windowIDOut, _ := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{window_id}").Output()
	windowID := strings.TrimSpace(string(windowIDOut))
	if windowID == "" {
		return
	}
	listOut, _ := exec.Command("tmux", "list-panes", "-t", windowID, "-F", "#{pane_id}:#{pane_current_command}").Output()
	for _, line := range strings.Split(string(listOut), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) < 2 {
			continue
		}
		if isAuxiliaryPaneCommand(parts[1]) {
			exec.Command("tmux", "resize-pane", "-t", parts[0], "-y", "1").Run()
		}
	}
}

func isAuxiliaryPaneCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "sidebar") {
		return true
	}
	if strings.Contains(lower, "pane-header") || strings.Contains(lower, "pane header") || strings.Contains(lower, "pane_header") {
		return true
	}
	return false
}

func isAuxiliaryPane(p tmux.Pane) bool {
	return isAuxiliaryPaneCommand(p.Command) || isAuxiliaryPaneCommand(p.StartCommand)
}

func findWindowByTarget(windows []tmux.Window, target string) *tmux.Window {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}

	if strings.HasPrefix(target, "@") {
		for i := range windows {
			if windows[i].ID == target {
				return &windows[i]
			}
		}
		return nil
	}

	idx, err := strconv.Atoi(target)
	if err != nil {
		return nil
	}
	for i := range windows {
		if windows[i].Index == idx {
			return &windows[i]
		}
	}
	return nil
}

// selectContentPaneInActiveWindow finds and selects the first non-auxiliary pane
// in the currently active window, ensuring focus goes to a content pane rather
// than a sidebar or pane-header.
func selectContentPaneInActiveWindow() {
	if out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_enable_focus_repair").Output(); err != nil || strings.TrimSpace(string(out)) != "1" {
		return
	}

	windowIDOut, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return
	}
	windowID := strings.TrimSpace(string(windowIDOut))
	if windowID == "" {
		return
	}
	selectContentPaneInWindow(windowID)
}

// selectContentPaneInWindow selects the first non-auxiliary pane in the given window.
func selectContentPaneInWindow(windowID string) {
	out, err := exec.Command("tmux", "list-panes", "-t", windowID,
		"-F", "#{pane_id}\x1f#{pane_current_command}").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\x1f", 2)
		if len(parts) == 2 {
			if !isAuxiliaryPaneCommand(parts[1]) {
				exec.Command("tmux", "select-pane", "-t", parts[0]).Run()
				return
			}
		}
	}
}

// hexToRGB converts hex color to RGB values
func hexToRGB(hexColor string) (int, int, int) {
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	var r, g, b int64
	r, _ = strconv.ParseInt(hex[0:2], 16, 64)
	g, _ = strconv.ParseInt(hex[2:4], 16, 64)
	b, _ = strconv.ParseInt(hex[4:6], 16, 64)
	return int(r), int(g), int(b)
}

// dimColor reduces the brightness of a hex color by the given opacity (0.0-1.0)
// Opacity of 1.0 = no change, 0.5 = half brightness
func dimColor(hexColor string, opacity float64) string {
	if hexColor == "" {
		return hexColor
	}
	r, g, b := hexToRGB(hexColor)
	// Dim by reducing RGB values toward 0
	r = int(float64(r) * opacity)
	g = int(float64(g) * opacity)
	b = int(float64(b) * opacity)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// HeaderColors holds the fg/bg colors for a pane header border.
type HeaderColors struct {
	Fg string
	Bg string
}

// GetHeaderColorsForPane returns the fg and bg colors for a pane header.
// Mirrors the tab color logic from sidebar rendering (custom colors, shading, active/inactive).
// Must be called with stateMu at least read-locked (callers hold RLock from RenderHeaderForClient).
func (c *Coordinator) GetHeaderColorsForPane(paneID string) HeaderColors {
	var foundWindow *tmux.Window
	var isWindowActive bool
	for i := range c.windows {
		for j := range c.windows[i].Panes {
			if c.windows[i].Panes[j].ID == paneID {
				foundWindow = &c.windows[i]
				isWindowActive = c.windows[i].Active
				break
			}
		}
		if foundWindow != nil {
			break
		}
	}
	if foundWindow == nil {
		return HeaderColors{
			Fg: c.getPaneHeaderActiveFg(),
			Bg: c.getPaneHeaderActiveBg(),
		}
	}

	var theme config.Theme
	var customColor string
	var foundGroup bool
	for _, group := range c.grouped {
		for _, win := range group.Windows {
			if win.ID == foundWindow.ID {
				theme = group.Theme
				customColor = win.CustomColor
				foundGroup = true
				break
			}
		}
		if foundGroup {
			break
		}
	}

	isDarkBg := c.bgDetector.IsDarkBackground()
	if c.theme != nil {
		isDarkBg = c.theme.Dark
	}
	theme = grouping.ResolveThemeColors(theme, isDarkBg)

	var bgColor, fgColor string
	isTransparent := customColor == "transparent"

	if isTransparent {
		bgColor = ""
		if isWindowActive {
			fgColor = theme.ActiveFg
			if fgColor == "" {
				fgColor = theme.Fg
			}
		} else {
			fgColor = theme.Fg
		}
	} else if customColor != "" {
		if isWindowActive {
			bgColor = customColor
		} else {
			bgColor = grouping.ShadeColorByIndex(customColor, 1)
		}
		fgColor = "#ffffff"
	} else if isWindowActive {
		bgColor = theme.ActiveBg
		if bgColor == "" {
			bgColor = theme.Bg
		}
		// Use base group fg for consistency — active/inactive distinction
		// comes from bg color + bold, not text color flipping white↔black
		fgColor = theme.Fg
	} else {
		bgColor = theme.Bg
		fgColor = theme.Fg
	}

	if bgColor == "" {
		bgColor = c.getPaneHeaderActiveBg()
	}
	if fgColor == "" {
		fgColor = c.getPaneHeaderActiveFg()
	}

	return HeaderColors{Fg: fgColor, Bg: bgColor}
}

// GetGitStateHash returns a hash of current git state for change detection
func (c *Coordinator) GetGitStateHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return fmt.Sprintf("%s:%d:%d:%d:%v", c.gitBranch, c.gitDirty, c.gitAhead, c.gitBehind, c.isGitRepo)
}

// desaturateHex blends a hex color toward a target color by the given opacity.
// opacity=1.0 means original color, opacity=0.0 means full target.
// targetHex is the blend target (e.g. terminal background); if empty, uses a
// luminance-based neutral.
func desaturateHex(hexColor string, opacity float64, targetHex ...string) string {
	if hexColor == "" {
		return hexColor
	}
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return hexColor
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 32)
	g, _ := strconv.ParseInt(hex[2:4], 16, 32)
	b, _ := strconv.ParseInt(hex[4:6], 16, 32)

	var tR, tG, tB int
	if len(targetHex) > 0 && targetHex[0] != "" {
		th := strings.TrimPrefix(targetHex[0], "#")
		if len(th) == 6 {
			tr, _ := strconv.ParseInt(th[0:2], 16, 32)
			tg, _ := strconv.ParseInt(th[2:4], 16, 32)
			tb, _ := strconv.ParseInt(th[4:6], 16, 32)
			tR, tG, tB = int(tr), int(tg), int(tb)
		}
	}
	if tR == 0 && tG == 0 && tB == 0 && (len(targetHex) == 0 || targetHex[0] == "") {
		lum := (int(r)*299 + int(g)*587 + int(b)*114) / 1000
		if lum >= 128 {
			tR, tG, tB = 200, 200, 200
		} else {
			tR, tG, tB = 48, 48, 48
		}
	}

	inv := 1.0 - opacity
	dr := int(math.Round(float64(r)*opacity + float64(tR)*inv))
	dg := int(math.Round(float64(g)*opacity + float64(tG)*inv))
	db := int(math.Round(float64(b)*opacity + float64(tB)*inv))

	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return v
	}
	return fmt.Sprintf("#%02x%02x%02x", clamp(dr), clamp(dg), clamp(db))
}
