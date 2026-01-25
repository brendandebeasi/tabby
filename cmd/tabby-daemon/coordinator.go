package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/daemon"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

// coordinatorDebugLog is the logger for coordinator debug output
var coordinatorDebugLog *log.Logger

func init() {
	// Default to discard (no logging)
	coordinatorDebugLog = log.New(io.Discard, "", 0)
}

// SetCoordinatorDebugLog sets the debug logger for the coordinator
func SetCoordinatorDebugLog(logger *log.Logger) {
	coordinatorDebugLog = logger
}

// Coordinator manages centralized state and rendering for all renderers
type Coordinator struct {
	// Shared state
	windows         []tmux.Window
	grouped         []grouping.GroupedWindows
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

	// Last known width (for pet physics clamping)
	lastWidth int

	// Per-client widths for accurate click detection
	clientWidths   map[string]int
	clientWidthsMu sync.RWMutex

	// Pet widget layout (for custom click detection)
	petLayout petWidgetLayout

	// State locks
	stateMu sync.RWMutex

	// Session info
	sessionID string

	// Pending group for next new window (for optimistic UI)
	pendingNewWindowGroup string
	pendingNewWindowTime  time.Time
}

// petState holds the current state of the pet widget
type petState struct {
	Pos           pos2D
	State         string
	Direction     int
	Hunger        int
	Happiness     int
	YarnPos       pos2D
	YarnExpiresAt time.Time // When yarn disappears
	FoodItem      pos2D
	PoopPositions []int
	NeedsPoopAt   time.Time
	LastFed       time.Time
	LastPet       time.Time
	LastPoop      time.Time
	LastThought   string
	ThoughtScroll int
	FloatingItems []floatingItem
	TargetPos     pos2D
	HasTarget     bool
	ActionPending string
	AnimFrame     int
	TotalPets         int
	TotalFeedings     int
	TotalPoopsCleaned int
	TotalYarnPlays    int
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

// petWidgetLayout tracks line offsets for custom click detection
//
// CLICK DETECTION METHODS:
//
// 1. BubbleZone (used for static elements):
//    - Wrap text with zone.Mark("zone_id", text) during rendering
//    - Call zone.Scan() on the full output to process markers
//    - Use zone.Get("zone_id") to retrieve bounds (StartX, EndX, StartY, EndY)
//    - Good for: buttons, fixed-position elements
//    - Limitation: Only ONE zone per ID is tracked (multiple zones with same ID overwrite)
//
// 2. Custom Layout Tracking (used for pet widget play area):
//    - Track line numbers during rendering (currentLine counter)
//    - Store positions in a layout struct (petWidgetLayout)
//    - In click handler, compare input.PinnedRelY against stored line numbers
//    - Use input.MouseX for horizontal position within the line
//    - Good for: complex dynamic content, multi-element interactions, precise hit testing
//    - Requires: manual tracking during render, custom click handler
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
}

// Pet sprites by style
type petSprites struct {
	Idle, Walking, Jumping, Playing  string
	Eating, Sleeping, Happy, Hungry  string
	Yarn, Food, Poop                 string
	Thought, Heart, Life             string
	HungerIcon, HappyIcon, SadIcon   string
	Ground                           string
}

var petSpritesByStyle = map[string]petSprites{
	"emoji": {
		Idle: "🐱", Walking: "🐱", Jumping: "🐱", Playing: "🐱",
		Eating: "🐱", Sleeping: "😺", Happy: "😻", Hungry: "😿",
		Yarn: "🧶", Food: "🍖", Poop: "💩",
		Thought: "💭", Heart: "❤", Life: "💗",
		HungerIcon: "🍖", HappyIcon: "😸", SadIcon: "😿",
		Ground: "·",
	},
	"nerd": {
		Idle: "󰄛", Walking: "󰄛", Jumping: "󰄛", Playing: "󰄛",
		Eating: "󰄛", Sleeping: "󰄛", Happy: "󰄛", Hungry: "󰄛",
		Yarn: "", Food: "", Poop: "",
		Thought: "", Heart: "", Life: "",
		HungerIcon: "", HappyIcon: "", SadIcon: "",
		Ground: "·",
	},
	"ascii": {
		Idle: "=^.^=", Walking: "=^.^=", Jumping: "=^o^=", Playing: "=^.^=",
		Eating: "=^.^=", Sleeping: "=-.~=", Happy: "=^.^=", Hungry: "=;.;=",
		Yarn: "@", Food: "o", Poop: ".",
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
	// Force ANSI256 color mode
	lipgloss.SetColorProfile(termenv.ANSI256)

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		cfg = &config.Config{}
	}

	c := &Coordinator{
		sessionID:       sessionID,
		config:          cfg,
		collapsedGroups: make(map[string]bool),
		clientWidths:    make(map[string]int),
		lastWidth:       25, // Default width for pet physics
		pet: petState{
			Pos:       pos2D{X: 10, Y: 0},
			State:     "idle",
			Direction: 1,
			Hunger:    80,
			Happiness: 80,
			YarnPos:   pos2D{X: -1, Y: 0},
			FoodItem:  pos2D{X: -1, Y: -1},
		},
	}

	// Load collapsed groups from tmux option
	c.loadCollapsedGroups()

	// Load pet state from shared file
	c.loadPetState()

	// Initial window refresh
	c.RefreshWindows()

	// Initial git refresh
	c.RefreshGit()

	// Initial session refresh
	c.RefreshSession()

	return c
}

// loadCollapsedGroups loads collapsed state from tmux options
func (c *Coordinator) loadCollapsedGroups() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.loadCollapsedGroupsLocked()
}

// loadCollapsedGroupsLocked loads collapsed state (caller must hold stateMu)
func (c *Coordinator) loadCollapsedGroupsLocked() {
	// Clear existing state
	c.collapsedGroups = make(map[string]bool)

	// First try legacy JSON format for backwards compatibility
	out, err := exec.Command("tmux", "show-options", "-v", "-q", "@tabby_collapsed_groups").Output()
	if err == nil && len(out) > 0 {
		var groups []string
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &groups); err == nil {
			for _, g := range groups {
				c.collapsedGroups[g] = true
			}
			// Migrate to new per-group format
			c.saveCollapsedGroupsLocked()
			exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
			return
		}
	}

	// Load per-group options (new format)
	// Check all configured groups
	for _, group := range c.config.Groups {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(group.Name, " ", "_"))
		out, err := exec.Command("tmux", "show-options", "-v", "-q", optName).Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			c.collapsedGroups[group.Name] = true
		}
	}
	// Also check "Default" group
	out, err = exec.Command("tmux", "show-options", "-v", "-q", "@tabby_grp_collapsed_Default").Output()
	if err == nil && strings.TrimSpace(string(out)) == "1" {
		c.collapsedGroups["Default"] = true
	}
}

// saveCollapsedGroups saves collapsed state to tmux options
func (c *Coordinator) saveCollapsedGroups() {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	c.saveCollapsedGroupsLocked()
}

// saveCollapsedGroupsLocked saves collapsed state (caller must hold stateMu)
func (c *Coordinator) saveCollapsedGroupsLocked() {
	// Save per-group options for ALL configured groups
	// This ensures expanded groups get their option unset
	for _, group := range c.config.Groups {
		optName := fmt.Sprintf("@tabby_grp_collapsed_%s", strings.ReplaceAll(group.Name, " ", "_"))
		if c.collapsedGroups[group.Name] {
			exec.Command("tmux", "set-option", optName, "1").Run()
		} else {
			exec.Command("tmux", "set-option", "-u", optName).Run()
		}
	}
	// Also handle Default group
	optName := "@tabby_grp_collapsed_Default"
	if c.collapsedGroups["Default"] {
		exec.Command("tmux", "set-option", optName, "1").Run()
	} else {
		exec.Command("tmux", "set-option", "-u", optName).Run()
	}
}

// petStatePath returns the path to the shared pet state file
func petStatePath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "tabby")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "pet.json")
}

// loadPetState loads the pet state from the shared file
func (c *Coordinator) loadPetState() {
	data, err := os.ReadFile(petStatePath())
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.pet)
}

// savePetState saves the pet state to the shared file
func (c *Coordinator) savePetState() {
	data, _ := json.Marshal(c.pet)
	os.WriteFile(petStatePath(), data, 0644)
}

// RefreshWindows fetches current window/pane state from tmux
func (c *Coordinator) RefreshWindows() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	// Note: collapsed groups state is managed in-memory and synced to tmux options
	// We don't reload here to avoid race conditions with toggle_group action

	// Track old window IDs to detect new windows
	oldWindowIDs := make(map[string]bool)
	for _, w := range c.windows {
		oldWindowIDs[w.ID] = true
	}

	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		return
	}

	// Check for pending group assignment (optimistic UI for new windows in groups)
	if c.pendingNewWindowGroup != "" && time.Since(c.pendingNewWindowTime) < 5*time.Second {
		for i := range windows {
			// Find new window without a group
			if !oldWindowIDs[windows[i].ID] && windows[i].Group == "" {
				// Assign the pending group
				windows[i].Group = c.pendingNewWindowGroup
				// Also set it in tmux so it persists
				exec.Command("tmux", "set-window-option", "-t", windows[i].ID, "@tabby_group", c.pendingNewWindowGroup).Run()
				// Clear pending
				c.pendingNewWindowGroup = ""
				break
			}
		}
	} else if c.pendingNewWindowGroup != "" {
		// Pending group expired, clear it
		c.pendingNewWindowGroup = ""
	}

	c.windows = windows
	c.grouped = grouping.GroupWindowsWithOptions(windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
}

// GetWindowsHash returns a hash of current window state for change detection
func (c *Coordinator) GetWindowsHash() string {
	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		return ""
	}
	// Simple hash: count + window IDs + active states + pane active states + indicators
	hash := fmt.Sprintf("%d", len(windows))
	for _, w := range windows {
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
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	out, err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Output()
	c.isGitRepo = err == nil && strings.TrimSpace(string(out)) == "true"
	if !c.isGitRepo {
		return
	}

	// Get branch
	out, err = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		c.gitBranch = strings.TrimSpace(string(out))
	}

	// Get dirty count
	c.gitDirty = 0
	out, err = exec.Command("git", "status", "--porcelain").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) > 0 {
				c.gitDirty++
			}
		}
	}

	// Get ahead/behind
	out, err = exec.Command("git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD").Output()
	if err == nil {
		parts := strings.Fields(string(out))
		if len(parts) == 2 {
			c.gitBehind, _ = strconv.Atoi(parts[0])
			c.gitAhead, _ = strconv.Atoi(parts[1])
		}
	}
}

// RefreshSession updates session state
func (c *Coordinator) RefreshSession() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err == nil {
		c.sessionName = strings.TrimSpace(string(out))
	}

	out, err = exec.Command("tmux", "list-clients", "-t", c.sessionName).Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] == "" {
			c.sessionClients = 0
		} else {
			c.sessionClients = len(lines)
		}
	}

	out, err = exec.Command("tmux", "display-message", "-p", "#{session_windows}").Output()
	if err == nil {
		c.windowCount, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
}

// IncrementSpinner advances the spinner frame
func (c *Coordinator) IncrementSpinner() {
	c.stateMu.Lock()
	c.spinnerFrame++
	c.stateMu.Unlock()
}

// UpdatePetState updates the pet's state (called periodically)
func (c *Coordinator) UpdatePetState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	// Reload shared state periodically (for cross-sidebar sync)
	if c.pet.AnimFrame%5 == 0 {
		c.loadPetState()
	}

	c.pet.AnimFrame++
	now := time.Now()
	width := c.lastWidth
	if width < 10 {
		width = 25
	}
	// Account for emoji visual width (2 cols) - use safe play width
	maxX := width - 5 // Reduced from width-2 to match safePlayWidth calculation
	if maxX < 1 {
		maxX = 1
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
		if poopX > 0 {
			poopX-- // Offset slightly
		}
		c.pet.PoopPositions = append(c.pet.PoopPositions, poopX)
		c.pet.LastPoop = now
		c.pet.NeedsPoopAt = time.Time{}
		c.pet.LastThought = randomThought("poop")
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
		if c.pet.Pos.X < c.pet.TargetPos.X {
			c.pet.Pos.X++
			c.pet.Direction = 1
		} else if c.pet.Pos.X > c.pet.TargetPos.X {
			c.pet.Pos.X--
			c.pet.Direction = -1
		}
		// Clamp after move
		if c.pet.Pos.X > maxX {
			c.pet.Pos.X = maxX
		}
		if c.pet.Pos.X < 0 {
			c.pet.Pos.X = 0
		}

		// If chasing yarn, push it when reached
		if c.pet.ActionPending == "play" {
			yarnX := c.pet.YarnPos.X
			if yarnX < 0 {
				yarnX = width - 4
			}
			// Pet pushes yarn when it reaches it
			if c.pet.Pos.X == yarnX || c.pet.Pos.X == yarnX-1 || c.pet.Pos.X == yarnX+1 {
				newYarnX := yarnX + c.pet.Direction*2
				if newYarnX >= 2 && newYarnX < width-2 {
					c.pet.YarnPos.X = newYarnX
					c.pet.YarnPos.Y = 1 // Bounce up
					c.pet.TargetPos.X = newYarnX
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
				// 50% chance yarn disappears after being played with
				if rand.Intn(100) < 50 {
					c.pet.YarnPos = pos2D{X: -1, Y: 0}
					c.pet.YarnExpiresAt = time.Time{}
				}
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
		// Configurable chance to do something every 10 frames (default: 15%)
		actionChance := c.config.Widgets.Pet.ActionChance
		if actionChance <= 0 {
			actionChance = 15 // Default: less hyper than before
		}
		if rand.Intn(100) < actionChance {
			action := rand.Intn(8)
			switch action {
			case 0:
				// Run across the screen
				c.pet.State = "walking"
				c.pet.Direction = []int{-1, 1}[rand.Intn(2)]
				targetX := rand.Intn(maxX)
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
				// Bat at yarn (toss it)
				tossX := rand.Intn(maxX-2) + 2
				c.pet.YarnPos = pos2D{X: tossX, Y: 2}
				c.pet.YarnExpiresAt = now.Add(15 * time.Second)
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
				// Gun to the left of pet (always)
				gunX := c.pet.Pos.X - 1
				if gunX < 0 {
					gunX = 0
				}
				c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
					Emoji:     "🔫",
					Pos:       pos2D{X: gunX, Y: 0},
					Velocity:  pos2D{X: 0, Y: 0},
					ExpiresAt: now.Add(1200 * time.Millisecond),
				})
				// BANG effect
				c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
					Emoji:     "💥",
					Pos:       pos2D{X: c.pet.Pos.X + dir, Y: 0},
					Velocity:  pos2D{X: 0, Y: 0},
					ExpiresAt: now.Add(400 * time.Millisecond),
				})
				// Banana flies slower (velocity 1 instead of 2)
				c.pet.FloatingItems = append(c.pet.FloatingItems, floatingItem{
					Emoji:     "🍌",
					Pos:       pos2D{X: c.pet.Pos.X + dir*2, Y: 1},
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
				startX := rand.Intn(maxX-2) + 2
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
			}
		}
	}

	// === HUNGER/HAPPINESS DECAY ===

	// Use config for hunger decay rate (frames = seconds * 10 since ~10fps)
	hungerDecayFrames := c.config.Widgets.Pet.HungerDecay * 10
	if hungerDecayFrames <= 0 {
		hungerDecayFrames = 600 // Default: 60 seconds (less needy)
	}
	if c.pet.Hunger > 0 && c.pet.AnimFrame%hungerDecayFrames == 0 {
		c.pet.Hunger--
	}
	// Happiness decays 1.5x faster when hungry
	happyDecayFrames := hungerDecayFrames * 2 / 3
	if happyDecayFrames <= 0 {
		happyDecayFrames = 400 // Default: 40 seconds when hungry
	}
	if c.pet.Hunger < 30 && c.pet.Happiness > 0 && c.pet.AnimFrame%happyDecayFrames == 0 {
		c.pet.Happiness--
	}

	// === THOUGHT MARQUEE ===

	// Use config for thought scroll speed (default: 3 frames per scroll step)
	thoughtSpeed := c.config.Widgets.Pet.ThoughtSpeed
	if thoughtSpeed <= 0 {
		thoughtSpeed = 3
	}
	if c.pet.AnimFrame%thoughtSpeed == 0 {
		thoughtWidth := runewidth.StringWidth(c.pet.LastThought)
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

	c.savePetState()
}

// RenderForClient generates content for a specific client's dimensions
func (c *Coordinator) RenderForClient(clientID string, width, height int) *daemon.RenderPayload {
	// Guard dimensions
	if width < 10 {
		width = 25
	}
	if height < 5 {
		height = 24
	}

	// Track width for pet physics (safe to update outside lock - advisory)
	c.lastWidth = width

	// Store per-client width for accurate click detection on resize
	c.clientWidthsMu.Lock()
	c.clientWidths[clientID] = width
	c.clientWidthsMu.Unlock()

	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Generate main content (window/pane list) with clickable regions
	// Pass clientID so we can show this client's window as active
	mainContent, regions := c.generateMainContent(clientID, width, height)

	// SIMPLIFIED: Stack widgets at the bottom of main content (no more pinned/scrollable split)
	// Calculate current line offset for widget regions
	// Count lines in main content (lines = newlines, since each line except last ends with \n)
	currentLine := strings.Count(mainContent, "\n")

	// Store the content start line for pet widget click detection
	// This tells us where the pet widget starts in absolute content coordinates
	c.petLayout.ContentStartLine = currentLine

	// Generate widgets and adjust their regions to account for main content offset
	widgetContent, widgetRegions := c.generatePinnedContent(width)
	if len(widgetRegions) > 0 {
		coordinatorDebugLog.Printf("Widget region offset: mainContent has %d newlines, offsetting widget regions by %d", currentLine, currentLine)
		coordinatorDebugLog.Printf("  Before offset: first widget region %+v", widgetRegions[0])
		for i := range widgetRegions {
			widgetRegions[i].StartLine += currentLine
			widgetRegions[i].EndLine += currentLine
		}
		coordinatorDebugLog.Printf("  After offset: first widget region %+v", widgetRegions[0])
	}

	// Combine everything into one content string
	fullContent := mainContent + widgetContent
	allRegions := append(regions, widgetRegions...)

	// Count total lines
	totalLines := strings.Count(fullContent, "\n")

	// Debug logging
	coordinatorDebugLog.Printf("RenderForClient: client=%s width=%d height=%d", clientID, width, height)
	coordinatorDebugLog.Printf("  Content: %d lines, %d bytes", totalLines, len(fullContent))
	coordinatorDebugLog.Printf("  Regions: %d total", len(allRegions))

	return &daemon.RenderPayload{
		Content:       fullContent,
		PinnedContent: "", // No longer using pinned content
		Width:         width,
		Height:        height,
		TotalLines:    totalLines,
		PinnedHeight:  0, // No pinned section
		Regions:       allRegions,
		PinnedRegions: nil, // All regions are in main Regions array now
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

// isTouchMode checks if touch mode is enabled
func (c *Coordinator) isTouchMode(width int) bool {
	if c.config.Sidebar.TouchMode {
		return true
	}
	if os.Getenv("TABBY_TOUCH") == "1" || width < 40 {
		return true
	}
	return false
}

// touchDivider returns a divider for touch mode
func (c *Coordinator) touchDivider(width int, bgColor string) string {
	if !c.isTouchMode(width) {
		return ""
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	if bgColor != "" {
		dividerStyle = dividerStyle.Background(lipgloss.Color(bgColor))
	}
	return dividerStyle.Render(strings.Repeat("─", width)) + "\n"
}

// lineSpacing returns extra line spacing for touch mode
func (c *Coordinator) lineSpacing() string {
	if c.config.Sidebar.LineHeight > 0 {
		return strings.Repeat("\n", c.config.Sidebar.LineHeight)
	}
	return ""
}

// getBusyFrames returns the busy indicator animation frames
func (c *Coordinator) getBusyFrames() []string {
	if len(c.config.Indicators.Busy.Frames) > 0 {
		return c.config.Indicators.Busy.Frames
	}
	return []string{"◐", "◓", "◑", "◒"}
}

// getIndicatorIcon returns the icon for an indicator
func (c *Coordinator) getIndicatorIcon(ind config.Indicator) string {
	if ind.Icon != "" {
		return ind.Icon
	}
	return "●"
}

// generateMainContent creates the main scrollable area with window list
// clientID is the window ID that this content is being rendered for
func (c *Coordinator) generateMainContent(clientID string, width, height int) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion

	currentLine := 0

	// Clock widget at top if configured
	if c.config.Widgets.Clock.Enabled && c.config.Widgets.Clock.Position == "top" {
		clockContent := c.renderClockWidget(width)
		clockContent = constrainWidgetWidth(clockContent, width)
		s.WriteString(clockContent)
		currentLine += strings.Count(clockContent, "\n")
	}

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
	treeFg := c.config.Sidebar.Colors.TreeFg
	if treeFg == "" {
		treeFg = "#888888"
	}
	treeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(treeFg))

	// Disclosure color
	disclosureColor := c.config.Sidebar.Colors.DisclosureFg
	if disclosureColor == "" {
		disclosureColor = "#000000"
	}

	// Active indicator config
	activeIndicator := c.config.Sidebar.Colors.ActiveIndicator
	if activeIndicator == "" {
		activeIndicator = "◀"
	}
	activeIndFgConfig := c.config.Sidebar.Colors.ActiveIndicatorFg
	activeIndBgConfig := c.config.Sidebar.Colors.ActiveIndicatorBg

	// Visual position counter (for display numbering)
	visualPos := 0

	// Iterate over grouped windows
	numGroups := len(c.grouped)
	for gi, group := range c.grouped {
		isLastGroup := gi == numGroups-1
		theme := group.Theme
		isCollapsed := c.collapsedGroups[group.Name]

		// Group header style
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Fg)).
			Background(lipgloss.Color(theme.Bg)).
			Bold(true)

		// Collapse indicator
		collapseIcon := expandedIcon
		if isCollapsed {
			collapseIcon = collapsedIcon
		}
		collapseStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(disclosureColor)).
			Background(lipgloss.Color(theme.Bg))

		// Build header
		icon := theme.Icon
		if icon != "" {
			icon += " "
		}
		headerText := icon + group.Name
		if isCollapsed && len(group.Windows) > 0 {
			headerText += fmt.Sprintf(" (%d)", len(group.Windows))
		}

		// Track group header line
		groupStartLine := currentLine
		hasWindows := len(group.Windows) > 0

		// Only show collapse icon if group has windows
		if hasWindows {
			// Truncate header text to fit width (accounting for collapse icon)
			headerMaxWidth := width - 2
			if lipgloss.Width(headerText) > headerMaxWidth {
				truncated := ""
				for _, r := range headerText {
					if lipgloss.Width(truncated+string(r)) > headerMaxWidth-1 {
						break
					}
					truncated += string(r)
				}
				headerText = truncated + "~"
			}
			headerContentStyle := headerStyle.Width(headerMaxWidth)
			s.WriteString(collapseStyle.Render(collapseIcon+" ") + headerContentStyle.Render(headerText) + "\n")
		} else {
			// No windows - just show header without collapse icon
			headerMaxWidth := width - 2
			if lipgloss.Width(headerText) > headerMaxWidth {
				truncated := ""
				for _, r := range headerText {
					if lipgloss.Width(truncated+string(r)) > headerMaxWidth-1 {
						break
					}
					truncated += string(r)
				}
				headerText = truncated + "~"
			}
			headerContentStyle := headerStyle.Width(width)
			s.WriteString(headerContentStyle.Render("  "+headerText) + "\n")
		}
		currentLine++

		// Record group region for click handling
		regions = append(regions, daemon.ClickableRegion{
			StartLine: groupStartLine,
			EndLine:   currentLine - 1,
			Action:    "toggle_group",
			Target:    group.Name,
		})

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
					fgColor = "#ffffff"
				} else {
					fgColor = "#888888"
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
				fgColor = theme.Fg
			}
			if fgColor == "" {
				fgColor = "#ffffff"
			}

			// Build style
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(fgColor))
			if bgColor != "" {
				style = style.Background(lipgloss.Color(bgColor))
			}
			if isActive {
				style = style.Bold(true)
			}

			// Build alert indicator
			alertIcon := ""
			ind := c.config.Indicators

			if ind.Busy.Enabled && win.Busy {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Busy.Color))
				if ind.Busy.Bg != "" {
					alertStyle = alertStyle.Background(lipgloss.Color(ind.Busy.Bg))
				}
				busyFrames := c.getBusyFrames()
				alertIcon = alertStyle.Render(busyFrames[c.spinnerFrame%len(busyFrames)])
			} else if ind.Input.Enabled && win.Input {
				inputIcon := ind.Input.Icon
				if inputIcon == "" {
					inputIcon = "?"
				}
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Input.Color))
				if ind.Input.Bg != "" {
					alertStyle = alertStyle.Background(lipgloss.Color(ind.Input.Bg))
				}
				if len(ind.Input.Frames) > 0 {
					alertIcon = alertStyle.Render(ind.Input.Frames[c.spinnerFrame%len(ind.Input.Frames)])
				} else {
					alertIcon = alertStyle.Render(inputIcon)
				}
			} else if !isActive {
				if ind.Bell.Enabled && win.Bell {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))
					if ind.Bell.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Bell.Bg))
					}
					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Bell))
				} else if ind.Activity.Enabled && win.Activity {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))
					if ind.Activity.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Activity.Bg))
					}
					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Activity))
				} else if ind.Silence.Enabled && win.Silence {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))
					if ind.Silence.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Silence.Bg))
					}
					alertIcon = alertStyle.Render(c.getIndicatorIcon(ind.Silence))
				}
			}

			// Render indicator at far left
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

			// Window collapse indicator if has panes
			hasPanes := len(win.Panes) > 1
			isWindowCollapsed := win.Collapsed
			var windowCollapseIcon string

			if hasPanes {
				if isWindowCollapsed {
					windowCollapseIcon = collapsedIcon
				} else {
					windowCollapseIcon = expandedIcon
				}
			}

			// Build tab content
			displayName := win.Name
			baseContent := fmt.Sprintf("%d. %s", visualPos, displayName)

			// Add pane count if collapsed
			if hasPanes && isWindowCollapsed {
				baseContent = fmt.Sprintf("%s (%d)", baseContent, len(win.Panes))
			}

			// Calculate widths
			prefixWidth := 3 // indicator + tree branch
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
			windowCollapseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(disclosureColor))
			if bgColor != "" {
				windowCollapseStyle = windowCollapseStyle.Background(lipgloss.Color(bgColor))
			}
			contentStyle := style.Width(windowContentWidth)

			// Render tab line
			if hasPanes {
				s.WriteString(indicatorPart + treeStyle.Render(treeBranch) + windowCollapseStyle.Render(windowCollapseIcon+" ") + contentStyle.Render(contentText) + "\n")
			} else if isActive {
				// Active indicator replaces part of tree branch
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
					indicatorFg = indicatorBg
				} else {
					indicatorFg = activeIndFgConfig
				}

				activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Background(lipgloss.Color(indicatorBg)).Bold(true)
				s.WriteString(indicatorPart + treeStyle.Render(treeBranchFirst) + activeIndStyle.Render(activeIndicator) + contentStyle.Render(contentText) + "\n")
			} else {
				s.WriteString(indicatorPart + treeStyle.Render(treeBranch) + contentStyle.Render(contentText) + "\n")
			}
			currentLine++

			// Record window region for click handling
			// For windows with panes, use special action that can toggle collapse
			windowAction := "select_window"
			if hasPanes {
				windowAction = "toggle_or_select_window"
			}
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				Action:    windowAction,
				Target:    strconv.Itoa(win.Index),
			})

			// Show panes if window has multiple panes and is not collapsed
			if len(win.Panes) > 1 && !isWindowCollapsed {
				var paneBg, paneFg, activePaneBg string
				if win.CustomColor != "" {
					paneBg = grouping.LightenColor(win.CustomColor, 0.3)
					activePaneBg = win.CustomColor
					paneFg = "#ffffff"
				} else {
					paneBg = grouping.LightenColor(theme.Bg, 0.3)
					activePaneBg = theme.ActiveBg
					paneFg = theme.Fg
					if paneFg == "" {
						paneFg = "#ffffff"
					}
				}

				paneStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color(paneFg)).
					Background(lipgloss.Color(paneBg))

				activePaneStyle := paneStyle
				if isActive {
					activePaneFg := "#ffffff"
					if win.CustomColor == "" && theme.ActiveFg != "" {
						activePaneFg = theme.ActiveFg
					}
					activePaneStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color(activePaneFg)).
						Background(lipgloss.Color(activePaneBg)).
						Bold(true)
				}

				var treeContinue string
				if isLastInGroup {
					treeContinue = " "
				} else {
					treeContinue = treeStyle.Render(treeContinueChar)
				}

				numPanes := len(win.Panes)
				for pi, pane := range win.Panes {
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

					paneNum := fmt.Sprintf("%d.%d", visualPos, pane.Index)
					paneLabel := pane.Command
					if pane.LockedTitle != "" {
						paneLabel = pane.LockedTitle
					} else if pane.Title != "" && pane.Title != pane.Command {
						paneLabel = pane.Title
					}
					paneText := fmt.Sprintf("%s %s", paneNum, paneLabel)

					paneIndentWidth := 6
					paneContentWidth := width - paneIndentWidth

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
							paneIndicatorFg = paneIndicatorBg
						} else {
							paneIndicatorFg = activeIndFgConfig
						}
						paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Background(lipgloss.Color(paneIndicatorBg)).Bold(true)
						fullWidthPaneStyle := activePaneStyle.Width(paneContentWidth)
						s.WriteString(" " + treeContinue + treeStyle.Render(" "+paneBranchChar+treeConnectorChar) + paneIndStyle.Render(paneActiveIndicator) + fullWidthPaneStyle.Render(paneText) + "\n")
					} else {
						s.WriteString(" " + treeContinue + treeStyle.Render(" "+paneBranchChar+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText) + "\n")
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

			visualPos++
		}

		// Padding after group (before next group)
		if !isLastGroup && hasWindows && !isCollapsed {
			s.WriteString("\n")
			currentLine++
		}
	}

	// Buttons - full width with backgrounds, connected (no spacing)
	if c.config.Sidebar.NewTabButton || c.config.Sidebar.NewGroupButton || c.config.Sidebar.CloseButton {
		s.WriteString("\n")
		currentLine++
	}

	if c.config.Sidebar.NewTabButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#27ae60")).
			Bold(true).
			Width(width)
		label := " + New Tab"
		s.WriteString(buttonStyle.Render(label) + "\n")
		currentLine++
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "new_tab",
		})
	}

	if c.config.Sidebar.NewGroupButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#9b59b6")).
			Bold(true).
			Width(width)
		label := " + New Group"
		s.WriteString(buttonStyle.Render(label) + "\n")
		currentLine++
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "new_group",
		})
	}

	if c.config.Sidebar.CloseButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#e74c3c")).
			Bold(true).
			Width(width)
		label := " x Close Tab"
		s.WriteString(buttonStyle.Render(label) + "\n")
		currentLine++
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "close_tab",
		})
	}

	// Non-pinned clock widget at bottom position
	if c.config.Widgets.Clock.Enabled && c.config.Widgets.Clock.Position != "top" && !c.config.Widgets.Clock.Pin {
		clockContent := c.renderClockWidget(width)
		clockContent = constrainWidgetWidth(clockContent, width)
		s.WriteString(clockContent)
	}

	return s.String(), regions
}

// generatePinnedContent creates the pinned widgets at bottom
func (c *Coordinator) generatePinnedContent(width int) (string, []daemon.ClickableRegion) {
	var s strings.Builder

	// Pinned pet widget at bottom (with zone.Mark wrappers for clicks)
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin {
		content := c.renderPetWidget(width)
		// Do not constrain width here, as it messes up ANSI zone markers
		s.WriteString(content)
	}

	// Scan content for zone markers and extract click regions automatically
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin {
		// Build full content and scan for zones
		fullContent := s.String()
		scannedContent := zone.Scan(fullContent)

		// Extract zone bounds for pet touch buttons
		petZones := []string{"pet:drop_food", "pet:drop_yarn", "pet:clean_poop", "pet:pet_pet", "pet:ground"}
		var zoneRegions []daemon.ClickableRegion

		for _, zoneID := range petZones {
			if info := zone.Get(zoneID); info != nil && !info.IsZero() {
				// Parse action from zone ID (format: "category:action")
				parts := strings.SplitN(zoneID, ":", 2)
				if len(parts) == 2 {
					// Note: BubbleZone EndX is inclusive, but ClickableRegion EndCol is exclusive
					// So we add 1 to convert from inclusive to exclusive
					zoneRegions = append(zoneRegions, daemon.ClickableRegion{
						StartLine: info.StartY,
						EndLine:   info.EndY,
						StartCol:  info.StartX,
						EndCol:    info.EndX + 1, // Convert from inclusive to exclusive
						Action:    parts[1],      // e.g., "drop_food"
						Target:    parts[0],      // e.g., "pet"
					})
					coordinatorDebugLog.Printf("BubbleZone extracted: %s -> lines %d-%d, cols %d-%d (exclusive)",
						zoneID, info.StartY, info.EndY, info.StartX, info.EndX+1)
				}
			}
		}

		coordinatorDebugLog.Printf("BubbleZone: extracted %d widget regions automatically", len(zoneRegions))

		// Apply safety constraint to the clean content (after markers are stripped)
		scannedContent = constrainWidgetWidth(scannedContent, width)

		// Use the scanned content (with zone markers stripped) for display
		s.Reset()
		s.WriteString(scannedContent)

		return s.String(), zoneRegions
	}

	// Pinned clock widget at bottom
	if c.config.Widgets.Clock.Enabled && c.config.Widgets.Clock.Position != "top" && c.config.Widgets.Clock.Pin {
		content := c.renderClockWidget(width)
		content = constrainWidgetWidth(content, width)
		s.WriteString(content)
	}

	// Pinned git widget at bottom
	if c.config.Widgets.Git.Enabled && c.config.Widgets.Git.Pin {
		content := c.renderGitWidget(width)
		content = constrainWidgetWidth(content, width)
		s.WriteString(content)
	}

	// Pinned session widget at bottom
	if c.config.Widgets.Session.Enabled && c.config.Widgets.Session.Pin {
		content := c.renderSessionWidget(width)
		content = constrainWidgetWidth(content, width)
		s.WriteString(content)
	}

	return s.String(), nil
}

// renderClockWidget renders the clock/date widget
func (c *Coordinator) renderClockWidget(width int) string {
	clock := c.config.Widgets.Clock
	now := time.Now()

	timeFormat := clock.Format
	if timeFormat == "" {
		timeFormat = "15:04:05"
	}

	fg := clock.Fg
	if fg == "" {
		fg = "#888888"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if clock.Bg != "" {
		style = style.Background(lipgloss.Color(clock.Bg))
	}

	dividerFg := clock.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))

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

	dividerFg := git.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))

	fg := git.Fg
	if fg == "" {
		fg = "#888888"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))

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
	dividerFg := sessionCfg.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < sessionCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	var parts []string

	sessionFg := sessionCfg.SessionFg
	if sessionFg == "" {
		sessionFg = "#aaaaaa"
	}
	sessionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(sessionFg))

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
		clientStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
		if icons.Clients != "" {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%s%d", icons.Clients, c.sessionClients)))
		} else {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%d", c.sessionClients)))
		}
	}

	if sessionCfg.ShowWindowCount {
		windowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
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
		lineWidth := runewidth.StringWidth(stripped)

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

// stripAnsi removes ANSI escape codes from a string for accurate width calculation
func stripAnsi(s string) string {
	// Simple regex to strip ANSI escape sequences
	ansiRegex := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansiRegex.ReplaceAllString(s, "")
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
	}
	if c.pet.Hunger < 30 {
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

	// Divider style
	divider := petCfg.Divider
	if divider == "" {
		divider = "─"
	}
	dividerFg := petCfg.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))
	renderDivider := func() string {
		dividerWidth := runewidth.StringWidth(divider)
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
	foodStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f39c12"))
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
	thoughtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	maxThoughtWidth := playWidth - 4
	if maxThoughtWidth < 5 {
		maxThoughtWidth = 5
	}
	thoughtWidth := runewidth.StringWidth(thought)
	displayThought := thought
	if thoughtWidth > maxThoughtWidth {
		scrollText := thought + "   " + thought
		scrollRunes := []rune(scrollText)
		startIdx := c.pet.ThoughtScroll % len(scrollRunes)
		visible := ""
		visWidth := 0
		for i := startIdx; i < len(scrollRunes) && visWidth < maxThoughtWidth; i++ {
			r := scrollRunes[i]
			visible += string(r)
			visWidth++
		}
		displayThought = visible
	}
	thoughtLine := sprites.Thought + " " + displayThought
	result.WriteString(thoughtStyle.Render(thoughtLine) + "\n")
	currentLine++

	// Divider before play area
	result.WriteString(renderDivider())
	currentLine++

	// Get positions, clamped to width
	safePlayWidth := playWidth - 3
	c.petLayout.PlayWidth = safePlayWidth

	petX := c.pet.Pos.X
	if petX < 0 {
		petX = safePlayWidth / 2
	}
	if petX >= safePlayWidth {
		petX = safePlayWidth - 1
	}
	petY := c.pet.Pos.Y
	petRunes := []rune(petSprite)

	yarnX := c.pet.YarnPos.X
	if yarnX < 0 {
		yarnX = safePlayWidth - 4
	}
	if yarnX >= safePlayWidth {
		yarnX = safePlayWidth - 1
	}
	yarnY := c.pet.YarnPos.Y
	yarnRunes := []rune(sprites.Yarn)

	foodX := c.pet.FoodItem.X
	if foodX >= safePlayWidth {
		foodX = safePlayWidth - 1
	}
	foodY := c.pet.FoodItem.Y
	foodRunes := []rune(sprites.Food)

	// Line 1: High air (Y=2)
	highAir := make([]rune, safePlayWidth)
	for i := range highAir {
		highAir[i] = ' '
	}
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 2 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				highAir[item.Pos.X] = itemRunes[0]
			}
		}
	}
	if petY >= 2 && petX < safePlayWidth && len(petRunes) > 0 {
		highAir[petX] = petRunes[0]
	}
	if yarnY >= 2 && yarnX >= 0 && yarnX < safePlayWidth && len(yarnRunes) > 0 {
		highAir[yarnX] = yarnRunes[0]
	}
	if foodY >= 2 && foodX >= 0 && foodX < safePlayWidth && len(foodRunes) > 0 {
		highAir[foodX] = foodRunes[0]
	}
	// Mark high air for yarn throwing (click empty space) - track line for click detection
	c.petLayout.HighAirLine = currentLine
	highAirLine := zone.Mark("pet:air_high", string(highAir))
	result.WriteString(highAirLine + "\n")
	currentLine++

	// Line 2: Low air (Y=1)
	lowAir := make([]rune, safePlayWidth)
	for i := range lowAir {
		lowAir[i] = ' '
	}
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 1 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				lowAir[item.Pos.X] = itemRunes[0]
			}
		}
	}
	if petY == 1 && petX < safePlayWidth && len(petRunes) > 0 {
		lowAir[petX] = petRunes[0]
	}
	if yarnY == 1 && yarnX >= 0 && yarnX < safePlayWidth && len(yarnRunes) > 0 {
		lowAir[yarnX] = yarnRunes[0]
	}
	if foodY == 1 && foodX >= 0 && foodX < safePlayWidth && len(foodRunes) > 0 {
		lowAir[foodX] = foodRunes[0]
	}
	// Mark low air for yarn throwing - track line for click detection
	c.petLayout.LowAirLine = currentLine
	lowAirLine := zone.Mark("pet:air_low", string(lowAir))
	result.WriteString(lowAirLine + "\n")
	currentLine++

	// Line 3: Ground (Y=0) - single clickable zone, action determined by click position
	groundChar := '·'
	if len(sprites.Ground) > 0 {
		groundChar = []rune(sprites.Ground)[0]
	}
	groundRow := make([]rune, safePlayWidth)
	for i := range groundRow {
		groundRow[i] = groundChar
	}

	// Place floating items
	for _, item := range c.pet.FloatingItems {
		if item.Pos.Y == 0 && item.Pos.X >= 0 && item.Pos.X < safePlayWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				groundRow[item.Pos.X] = itemRunes[0]
			}
		}
	}

	// Place yarn
	if yarnY == 0 && yarnX >= 0 && yarnX < safePlayWidth && len(yarnRunes) > 0 {
		groundRow[yarnX] = yarnRunes[0]
	}

	// Place food
	if foodY == 0 && foodX >= 0 && foodX < safePlayWidth && len(foodRunes) > 0 {
		groundRow[foodX] = foodRunes[0]
	}

	// Place poops
	poopRunes := []rune(sprites.Poop)
	for _, poopX := range c.pet.PoopPositions {
		if poopX >= 0 && poopX < safePlayWidth && len(poopRunes) > 0 {
			groundRow[poopX] = poopRunes[0]
		}
	}

	// Place cat on top
	if petY == 0 && petX >= 0 && petX < safePlayWidth && len(petRunes) > 0 {
		groundRow[petX] = petRunes[0]
	}

	// Single zone for entire ground - action determined by click handler
	// Track line for click detection
	c.petLayout.GroundLine = currentLine
	groundLine := zone.Mark("pet:ground", string(groundRow))
	result.WriteString(groundLine + "\n")
	currentLine++

	// Divider before stats
	result.WriteString(renderDivider())
	currentLine++

	// Stats line: hunger | happiness | life
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	hungerIcon := sprites.HungerIcon
	happyIcon := sprites.HappyIcon
	if c.pet.Happiness < 30 {
		happyIcon = sprites.SadIcon
	}
	lifeIcon := sprites.Life
	// Calculate life based on hunger + happiness average
	life := (c.pet.Hunger + c.pet.Happiness) / 2
	statusLine := fmt.Sprintf("%s%d %s%d %s%d", hungerIcon, c.pet.Hunger, happyIcon, c.pet.Happiness, lifeIcon, life)
	result.WriteString(statusStyle.Render(statusLine) + "\n")
	currentLine++

	for i := 0; i < petCfg.PaddingBot; i++ {
		result.WriteString("\n")
		currentLine++
	}

	for i := 0; i < petCfg.MarginBot; i++ {
		result.WriteString("\n")
		currentLine++
	}

	// Store total widget height for click detection
	c.petLayout.WidgetHeight = currentLine

	coordinatorDebugLog.Printf("Pet layout updated: Feed=%d, HighAir=%d, LowAir=%d, Ground=%d, PlayWidth=%d, Height=%d",
		c.petLayout.FeedLine, c.petLayout.HighAirLine, c.petLayout.LowAirLine,
		c.petLayout.GroundLine, c.petLayout.PlayWidth, c.petLayout.WidgetHeight)

	return result.String()
}

// renderPetTouchButtons renders touch-friendly action buttons for the pet widget
func (c *Coordinator) renderPetTouchButtons(width int, sprites petSprites) string {
	var result strings.Builder

	// Simple button style without Width/Padding (we'll handle width manually)
	buttonBg := lipgloss.Color("#333333")
	buttonFg := lipgloss.Color("#ffffff")
	buttonStyle := lipgloss.NewStyle().
		Foreground(buttonFg).
		Background(buttonBg)

	// Divider line (constrain to exact width)
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	dividerWidth := width
	if dividerWidth < 1 {
		dividerWidth = 1
	}
	result.WriteString(dividerStyle.Render(strings.Repeat("─", dividerWidth)) + "\n")

	// Define buttons
	buttons := []struct {
		emoji  string
		label  string
		action string
		show   bool
	}{
		{sprites.Food, "Feed", "drop_food", true},
		{sprites.Yarn, "Play", "drop_yarn", true},
		{sprites.Poop, "Clean", "clean_poop", len(c.pet.PoopPositions) > 0},
		{sprites.Heart, "Pet", "pet_pet", true},
	}

	for _, btn := range buttons {
		if btn.show {
			// Format: emoji  label, manually padded to fit width
			content := fmt.Sprintf("%s  %s", btn.emoji, btn.label)
			// Calculate visual width (emoji is 2 cols, spaces are 1 col each)
			contentWidth := runewidth.StringWidth(content)
			// Add padding to reach target width (account for background color padding)
			targetWidth := width - 2 // Leave 1 char margin on each side
			if targetWidth < contentWidth {
				targetWidth = contentWidth
			}
			padding := targetWidth - contentWidth
			if padding > 0 {
				content = content + strings.Repeat(" ", padding)
			}
			// Add single space padding on left and right
			content = " " + content + " "

			// Apply style and wrap with zone for click detection
			zoneID := fmt.Sprintf("pet:%s", btn.action)
			styledContent := buttonStyle.Render(content)
			markedContent := zone.Mark(zoneID, styledContent)
			coordinatorDebugLog.Printf("zone.Mark(%q) input len=%d, output len=%d, hasMarker=%v",
				zoneID, len(styledContent), len(markedContent), len(markedContent) > len(styledContent))
			result.WriteString(markedContent + "\n")
		}
	}

	return result.String()
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

// HandleInput processes input events from renderers
// Returns true if window list refresh is needed (expensive tmux calls)
func (c *Coordinator) HandleInput(clientID string, input *daemon.InputPayload) bool {
	switch input.Type {
	case "action":
		return c.handleSemanticAction(clientID, input)
	case "key":
		c.handleKeyInput(clientID, input)
		return true // key inputs might need refresh
	}
	return false
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
	if input.Button == "right" && input.ResolvedAction != "" {
		coordinatorDebugLog.Printf("  -> Showing context menu for right-click")
		c.handleRightClick(clientID, input)
		return true
	}

	// Custom pet widget click detection (bypasses BubbleZone)
	// Uses tracked line positions from renderPetWidget for precise hit testing
	// Note: We try this for ALL clicks if pet is enabled - handlePetWidgetClick will
	// check if the click is actually within the pet widget bounds
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin {
		if handled := c.handlePetWidgetClick(clientID, input); handled {
			return false // Pet actions don't need window refresh
		}
	}

	if input.ResolvedAction == "" {
		// No action resolved - just release focus back to main pane
		coordinatorDebugLog.Printf("  -> No action resolved, releasing focus")
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false
	}

	switch input.ResolvedAction {
	case "select_window":
		// Run synchronously so RefreshWindows() sees the new state
		exec.Command("tmux", "select-window", "-t", input.ResolvedTarget).Run()
		exec.Command("tmux", "select-pane", "-R").Run()
		return true

	case "toggle_or_select_window":
		// Always select window - collapse/expand is available via right-click context menu
		exec.Command("tmux", "select-window", "-t", input.ResolvedTarget).Run()
		exec.Command("tmux", "select-pane", "-R").Run()
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
		c.saveCollapsedGroups()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false // No tmux window state change

	case "button":
		switch input.ResolvedTarget {
		case "new_tab":
			// Run synchronously so RefreshWindows() sees the new window
			exec.Command("tmux", "new-window").Run()
		case "new_group":
			// Could implement group creation dialog
		case "close_tab":
			// Run synchronously so RefreshWindows() sees the removal
			exec.Command("tmux", "kill-window").Run()
		}
		return true

	case "drop_food":
		// Drop food at a random position for the pet to eat
		c.stateMu.Lock()
		width := c.getClientWidth(clientID)
		dropX := rand.Intn(width - 4) + 2
		c.pet.FoodItem = pos2D{X: dropX, Y: 2} // Drop from high air
		c.pet.LastThought = "food!"
		c.savePetState()
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false // Pet action, no window refresh needed

	case "drop_yarn":
		// Drop or toss the yarn at click position
		c.stateMu.Lock()
		width := c.getClientWidth(clientID)
		// Use click position, clamped to valid range
		tossX := input.MouseX
		if tossX < 2 {
			tossX = 2
		}
		if tossX >= width-2 {
			tossX = width - 3
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2} // Toss high
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second) // Yarn disappears after 15 seconds
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		c.savePetState()
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false // Pet action, no window refresh needed

	case "clean_poop":
		// Clean up poop at the clicked position
		c.stateMu.Lock()
		if len(c.pet.PoopPositions) > 0 {
			// Remove the first poop (or use input.ResolvedTarget for specific position)
			c.pet.PoopPositions = c.pet.PoopPositions[1:]
			c.pet.TotalPoopsCleaned++
			c.pet.LastThought = "much better."
			c.savePetState()
		}
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false // Pet action, no window refresh needed

	case "pet_pet":
		// Pet the pet - increase happiness
		c.stateMu.Lock()
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		c.pet.LastThought = randomThought("petting")
		c.savePetState()
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false // Pet action, no window refresh needed

	case "ground":
		// Ground click - determine action based on click X position
		// Click position relative to zone start
		clickX := input.MouseX
		c.stateMu.Lock()

		// Check if clicking on cat (only when cat is on ground, Y=0)
		if c.pet.Pos.Y == 0 && clickX == c.pet.Pos.X {
			// Pet the cat
			c.pet.Happiness = min(100, c.pet.Happiness+10)
			c.pet.TotalPets++
			c.pet.LastPet = time.Now()
			c.pet.State = "happy"
			thoughts := []string{"purrrr.", "yes, there.", "acceptable.", "more.", "don't stop."}
			c.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
			c.savePetState()
			c.stateMu.Unlock()
			if input.PaneID != "" {
				exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
			}
			return false
		}

		// Check if clicking on poop
		for i, poopX := range c.pet.PoopPositions {
			if clickX == poopX {
				// Clean this poop
				c.pet.PoopPositions = append(c.pet.PoopPositions[:i], c.pet.PoopPositions[i+1:]...)
				c.pet.TotalPoopsCleaned++
				c.pet.LastThought = "much better."
				c.savePetState()
				c.stateMu.Unlock()
				if input.PaneID != "" {
					exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
				}
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
			tossX = width - 3
		}
		c.pet.YarnPos = pos2D{X: tossX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.TargetPos = pos2D{X: tossX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "yarn!"
		c.savePetState()
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return false
	}
	return false
}

// handlePetWidgetClick uses custom click detection for the pet widget
// This bypasses BubbleZone and uses tracked line positions for precise hit testing
// Returns true if the click was handled, false otherwise
func (c *Coordinator) handlePetWidgetClick(clientID string, input *daemon.InputPayload) bool {
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
		dropX := rand.Intn(clientWidth-4) + 2
		c.pet.FoodItem = pos2D{X: dropX, Y: 2}
		c.pet.LastThought = "food!"
		c.savePetState()
		c.stateMu.Unlock()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return true
	}

	// Calculate safe play width for this client (must match rendering)
	safePlayWidth := clientWidth - 3
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
	defer c.stateMu.Unlock()

	// Get sprite strings for width calculation
	sprites := c.getSprites()

	// Calculate safe play width using client-specific width (must match renderPetWidget)
	playWidth := c.getClientWidth(clientID)
	safePlayWidth := playWidth - 3
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
	}
	catWidth := runewidth.StringWidth(catSprite)
	if catWidth < 1 {
		catWidth = 1
	}

	// Check if clicking on cat (account for sprite display width)
	// Sprites like emojis display wider than their rune position
	// Use clamped position to match what's rendered on screen
	if c.pet.Pos.Y == petY && clickX >= catPosX && clickX < catPosX+catWidth {
		coordinatorDebugLog.Printf("    -> Clicked on cat at X=%d (cat rendered at %d, width=%d)! Petting.", clickX, catPosX, catWidth)
		c.pet.Happiness = min(100, c.pet.Happiness+10)
		c.pet.TotalPets++
		c.pet.LastPet = time.Now()
		c.pet.State = "happy"
		c.pet.LastThought = randomThought("petting")
		c.savePetState()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return true
	}

	// Check if clicking on poop (only on ground)
	if petY == 0 {
		poopWidth := runewidth.StringWidth(sprites.Poop)
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
				c.savePetState()
				if input.PaneID != "" {
					exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
				}
				return true
			}
		}
	}

	// Check if clicking on yarn (account for sprite width)
	// Use clamped position to match what's rendered on screen
	yarnWidth := runewidth.StringWidth(sprites.Yarn)
	if yarnWidth < 1 {
		yarnWidth = 1
	}
	if c.pet.YarnPos.Y == petY && clickX >= yarnPosX && clickX < yarnPosX+yarnWidth {
		coordinatorDebugLog.Printf("    -> Clicked on yarn at X=%d (yarn rendered at %d)! Moving it.", clickX, yarnPosX)
		// Toss the yarn to a new position using client-specific width
		width := playWidth
		newX := rand.Intn(width-4) + 2
		c.pet.YarnPos = pos2D{X: newX, Y: 2}
		c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
		c.pet.TargetPos = pos2D{X: newX, Y: 0}
		c.pet.HasTarget = true
		c.pet.ActionPending = "play"
		c.pet.State = "walking"
		c.pet.LastThought = "again!"
		c.savePetState()
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return true
	}

	// Otherwise, drop yarn at click position using client-specific width
	coordinatorDebugLog.Printf("    -> Empty space clicked, dropping yarn at X=%d", clickX)
	tossX := clickX
	if tossX < 2 {
		tossX = 2
	}
	if tossX >= playWidth-2 {
		tossX = playWidth - 3
	}
	// Start yarn at high air, let it fall
	c.pet.YarnPos = pos2D{X: tossX, Y: 2}
	c.pet.YarnExpiresAt = time.Now().Add(15 * time.Second)
	c.pet.TargetPos = pos2D{X: tossX, Y: 0}
	c.pet.HasTarget = true
	c.pet.ActionPending = "play"
	c.pet.State = "walking"
	c.pet.LastThought = "yarn!"
	c.savePetState()
	if input.PaneID != "" {
		exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
	}
	return true
}

// handleRightClick shows appropriate context menu based on what was clicked
func (c *Coordinator) handleRightClick(clientID string, input *daemon.InputPayload) {
	switch input.ResolvedAction {
	case "select_window", "toggle_or_select_window":
		// If clicking on far left (X < 2), show indicator menu; otherwise show window menu
		if input.MouseX < 2 {
			c.showIndicatorContextMenu(input.ResolvedTarget)
		} else {
			c.showWindowContextMenu(input.ResolvedTarget)
		}
	case "select_pane":
		c.showPaneContextMenu(input.ResolvedTarget)
	case "toggle_group":
		c.showGroupContextMenu(input.ResolvedTarget)
	}
}

// showWindowContextMenu displays the context menu for a window
func (c *Coordinator) showWindowContextMenu(windowIdx string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the window
	idx, err := strconv.Atoi(windowIdx)
	if err != nil {
		return
	}
	var win *tmux.Window
	for i := range c.windows {
		if c.windows[i].Index == idx {
			win = &c.windows[i]
			break
		}
	}
	if win == nil {
		return
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
		"-x", "M",
		"-y", "M",
	}

	// Rename option
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%' ; set-window-option -t :%d automatic-rename off\"", win.Name, win.Index, win.Index)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option
	unlockCmd := fmt.Sprintf("set-window-option -t :%d automatic-rename on", win.Index)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Collapse/Expand panes option (only for windows with multiple panes)
	if len(win.Panes) > 1 {
		args = append(args, "", "", "") // Separator
		if win.Collapsed {
			expandCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_collapsed", win.Index)
			args = append(args, "Expand Panes", "e", expandCmd)
		} else {
			collapseCmd := fmt.Sprintf("set-window-option -t :%d @tabby_collapsed 1", win.Index)
			args = append(args, "Collapse Panes", "c", collapseCmd)
		}
	}

	// Separator
	args = append(args, "", "", "")

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

	// Separator
	args = append(args, "", "", "")

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
	for _, color := range colorOptions {
		setColorCmd := fmt.Sprintf("set-window-option -t :%d @tabby_color '%s'", win.Index, color.hex)
		args = append(args, fmt.Sprintf("  %s", color.name), color.key, setColorCmd)
	}
	resetColorCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_color", win.Index)
	args = append(args, "  Reset to Default", "d", resetColorCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -h -c '#{pane_current_path}'", win.Index, win.Index)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -v -c '#{pane_current_path}'", win.Index, win.Index)
	args = append(args, "Split Horizontal |", "|", splitHCmd)
	args = append(args, "Split Vertical -", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Open in Finder
	openFinderCmd := "run-shell 'open \"#{pane_current_path}\"'"
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Kill option
	killCmd := fmt.Sprintf("kill-window -t :%d", win.Index)
	args = append(args, "Kill", "k", killCmd)

	exec.Command("tmux", args...).Run()
}

// showPaneContextMenu displays the context menu for a pane
func (c *Coordinator) showPaneContextMenu(paneID string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the pane
	var pane *tmux.Pane
	var windowIdx int
	for _, win := range c.windows {
		for i := range win.Panes {
			if win.Panes[i].ID == paneID {
				pane = &win.Panes[i]
				windowIdx = win.Index
				break
			}
		}
		if pane != nil {
			break
		}
	}
	if pane == nil {
		return
	}

	// Use locked title, then title, then command for display
	paneLabel := pane.Command
	if pane.LockedTitle != "" {
		paneLabel = pane.LockedTitle
	} else if pane.Title != "" && pane.Title != pane.Command {
		paneLabel = pane.Title
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Pane %d.%d: %s", windowIdx, pane.Index, paneLabel),
		"-x", "M",
		"-y", "M",
	}

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

	// Separator
	args = append(args, "", "", "")

	// Split options
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '#{pane_current_path}'", windowIdx, pane.ID)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '#{pane_current_path}'", windowIdx, pane.ID)
	args = append(args, "Split Horizontal |", "|", splitHCmd)
	args = append(args, "Split Vertical -", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", windowIdx, pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Break pane to new window
	breakCmd := fmt.Sprintf("break-pane -s %s", pane.ID)
	args = append(args, "Break to New Window", "b", breakCmd)

	// Open in Finder
	args = append(args, "Open in Finder", "o", "run-shell 'open \"#{pane_current_path}\"'")

	// Separator
	args = append(args, "", "", "")

	// Close pane
	args = append(args, "Close Pane", "x", fmt.Sprintf("kill-pane -t %s", pane.ID))

	exec.Command("tmux", args...).Run()
}

// showGroupContextMenu displays the context menu for a group header
func (c *Coordinator) showGroupContextMenu(groupName string) {
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

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Group: %s (%d windows)", group.Name, len(group.Windows)),
		"-x", "M",
		"-y", "M",
	}

	// Get working directory for new windows in this group
	var workingDir string
	for _, cfgGroup := range c.config.Groups {
		if cfgGroup.Name == group.Name && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			break
		}
	}

	dirArg := "'#{pane_current_path}'"
	if workingDir != "" {
		dirArg = fmt.Sprintf("'%s'", workingDir)
	}

	if group.Name != "Default" {
		// Set pending group for optimistic UI - if the new window appears
		// before tmux sets the option, we'll assign it ourselves
		c.pendingNewWindowGroup = group.Name
		c.pendingNewWindowTime = time.Now()

		// Create window and set group option
		newWindowCmd := fmt.Sprintf("new-window -c %s ; set-window-option @tabby_group '%s'", dirArg, group.Name)
		args = append(args, fmt.Sprintf("New %s Window", group.Name), "n", newWindowCmd)
	} else {
		newWindowCmd := fmt.Sprintf("new-window -c %s", dirArg)
		args = append(args, "New Window", "n", newWindowCmd)
	}

	// Note: Collapse/Expand is done by clicking on the group header (left click)
	// No need for context menu option since it would bypass in-memory state

	// Close all windows in group (only if group has windows)
	if len(group.Windows) > 0 {
		// Separator
		args = append(args, "", "", "")

		// Build command to kill all windows in this group
		var killCmds []string
		for _, win := range group.Windows {
			killCmds = append(killCmds, fmt.Sprintf("kill-window -t %s", win.ID))
		}
		killAllCmd := strings.Join(killCmds, " \\; ")

		// Use confirm-before for safety
		confirmCmd := fmt.Sprintf(`confirm-before -p "Close all %d windows in %s? (y/n)" "%s"`,
			len(group.Windows), group.Name, killAllCmd)
		args = append(args, fmt.Sprintf("Close All (%d)", len(group.Windows)), "X", confirmCmd)
	}

	exec.Command("tmux", args...).Run()
}

// showIndicatorContextMenu displays the context menu for window indicators (busy, bell, etc.)
func (c *Coordinator) showIndicatorContextMenu(windowIdx string) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Find the window
	idx, err := strconv.Atoi(windowIdx)
	if err != nil {
		return
	}
	var win *tmux.Window
	for i := range c.windows {
		if c.windows[i].Index == idx {
			win = &c.windows[i]
			break
		}
	}
	if win == nil {
		return
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Alerts: Window %d", win.Index),
		"-x", "M",
		"-y", "M",
	}

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

	exec.Command("tmux", args...).Run()
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
			c.stateMu.Unlock()
		}
	}
}

// Default pet thoughts by state
var defaultPetThoughts = map[string][]string{
	"hungry":  {"food. now.", "the bowl. it echoes.", "starving. dramatically.", "hunger level: critical."},
	"poop":    {"that won't clean itself.", "i made you a gift.", "cleanup crew needed.", "ahem. the floor."},
	"happy":   {"acceptable.", "fine. you may stay.", "feeling good.", "not bad.", "this is nice."},
	"yarn":    {"the yarn. it calls.", "must... catch...", "yarn acquired.", "got it!"},
	"sleepy":  {"nap time.", "zzz...", "five more minutes.", "so tired."},
	"idle":    {"chillin'.", "vibin'.", "just here.", "sup.", "...", "waiting.", "*yawn*", "hmm."},
	"walking": {"exploring.", "on the move.", "wandering.", "going places."},
	"jumping": {"wheee!", "boing!", "up up up!", "airborne."},
	"petting": {"mmm...", "yes, there.", "acceptable.", "more.", "don't stop.", "nice."},
}

// randomThought returns a random thought from the given category
func randomThought(category string) string {
	thoughts, ok := defaultPetThoughts[category]
	if !ok || len(thoughts) == 0 {
		thoughts = defaultPetThoughts["idle"]
	}
	return thoughts[rand.Intn(len(thoughts))]
}

// GetGitStateHash returns a hash of current git state for change detection
func (c *Coordinator) GetGitStateHash() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return fmt.Sprintf("%s:%d:%d:%d:%v", c.gitBranch, c.gitDirty, c.gitAhead, c.gitBehind, c.isGitRepo)
}
