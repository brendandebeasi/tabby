package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/perf"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

var debugLog *log.Logger
var debugEnabled bool

// clickDebugEnabled enables detailed click/view logging to /tmp/tabby-click.log
// Set TABBY_CLICK_DEBUG=1 to enable
var clickDebugEnabled = os.Getenv("TABBY_CLICK_DEBUG") == "1"

// abs returns absolute value of an int
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func initDebugLog(enabled bool) {
	debugEnabled = enabled
	if !enabled {
		return
	}
	f, err := os.OpenFile("/tmp/tabby-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}
	debugLog = log.New(f, "", log.Ltime|log.Lmicroseconds)
}

func debug(format string, args ...interface{}) {
	if debugEnabled && debugLog != nil {
		debugLog.Printf(format, args...)
	}
}

// Shared state file for broadcasting active window across all sidebars
// This allows instant updates when switching windows without waiting for tmux hooks
var sharedStateFile string

// sharedState represents the active window broadcast from any sidebar
type sharedState struct {
	Timestamp int64 `json:"t"`
	WindowIdx int   `json:"w"`
}

func initSharedState() {
	// Get tmux session ID to make file unique per session
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	if err != nil {
		sharedStateFile = "/tmp/tabby-active"
	} else {
		sessionID := strings.TrimSpace(string(out))
		sessionID = strings.ReplaceAll(sessionID, "$", "")
		sharedStateFile = fmt.Sprintf("/tmp/tabby-active-%s", sessionID)
	}
}

// writeSharedActive broadcasts the new active window to all sidebars
func writeSharedActive(windowIdx int) {
	if sharedStateFile == "" {
		initSharedState()
	}
	state := sharedState{
		Timestamp: time.Now().UnixMilli(),
		WindowIdx: windowIdx,
	}
	data, _ := json.Marshal(state)
	_ = os.WriteFile(sharedStateFile, data, 0644)
	if clickDebugEnabled {
		if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintf(f, "%s [%d] BROADCAST: active=%d to %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), windowIdx, sharedStateFile)
			f.Close()
		}
	}
}

// readSharedActive reads the broadcasted active window if it's recent (< 500ms old)
// Returns the window index and true if valid, or -1 and false if no valid state
func readSharedActive() (int, bool) {
	if sharedStateFile == "" {
		initSharedState()
	}
	data, err := os.ReadFile(sharedStateFile)
	if err != nil {
		return -1, false
	}
	var state sharedState
	if err := json.Unmarshal(data, &state); err != nil {
		return -1, false
	}
	// Only use if less than 500ms old
	age := time.Now().UnixMilli() - state.Timestamp
	if age > 500 {
		return -1, false
	}
	if clickDebugEnabled {
		if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			fmt.Fprintf(f, "%s [%d] READ_SHARED: active=%d age=%dms\n", time.Now().Format("15:04:05.000"), os.Getpid(), state.WindowIdx, age)
			f.Close()
		}
	}
	return state.WindowIdx, true
}

// getCurrentDir returns the directory containing the sidebar binary
func getCurrentDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	// Go up one level from bin/ to the plugin root
	return filepath.Dir(filepath.Dir(exe))
}

// windowRef stores visual position range and window reference
// Uses line ranges for multi-line touch targets
type windowRef struct {
	window    *tmux.Window
	startLine int // First line of this tab
	endLine   int // Last line (inclusive) of this tab
}

// paneRef stores visual position range and pane reference
type paneRef struct {
	pane       *tmux.Pane
	window     *tmux.Window
	windowIdx  int
	startLine  int
	endLine    int
}

// groupRef stores visual position range and group reference
type groupRef struct {
	group     *grouping.GroupedWindows
	startLine int
	endLine   int
}

// Default spinner frames if not configured
var defaultSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

// Pet widget sprites by style
type petSprites struct {
	Idle     string
	Walking  string
	Jumping  string
	Playing  string
	Eating   string
	Sleeping string
	Happy    string
	Hungry   string
	Yarn     string
	Food     string
	Poop     string
}

var petSpritesByStyle = map[string]petSprites{
	"emoji": {
		Idle: "🐱", Walking: "🐱", Jumping: "🐱⬆", Playing: "🐱",
		Eating: "🐱", Sleeping: "😺", Happy: "😻", Hungry: "😿",
		Yarn: "🧶", Food: "🍖", Poop: "💩",
	},
	"nerd": {
		Idle: "󰄛", Walking: "󰄛", Jumping: "󰄛^", Playing: "󰄛",
		Eating: "󰄛", Sleeping: "󰄛", Happy: "󰄛", Hungry: "󰄛",
		Yarn: "@", Food: "♨", Poop: ".",
	},
	"ascii": {
		Idle: "=^.^=", Walking: "=^.^=", Jumping: "=^o^=", Playing: "=^.^=",
		Eating: "=^.^=", Sleeping: "=-.~=", Happy: "=^.^=", Hungry: "=;.;=",
		Yarn: "@", Food: "o", Poop: ".",
	},
}

// pos2D represents a 2D position in the play area
type pos2D struct {
	X int // horizontal position (0 = left)
	Y int // vertical position (0 = bottom/ground, 1+ = above)
}

// gitState holds the current git repository status
type gitState struct {
	InRepo     bool   // Whether we're in a git repo
	Branch     string // Current branch name
	Detached   bool   // HEAD is detached
	CommitHash string // Short commit hash (for detached HEAD)
	Dirty      int    // Number of modified files
	Staged     int    // Number of staged files
	Untracked  int    // Number of untracked files
	Ahead      int    // Commits ahead of upstream
	Behind     int    // Commits behind upstream
	Stashes    int    // Number of stashes
	Conflict   bool   // Merge conflict in progress
	Insertions int    // Lines added
	Deletions  int    // Lines removed
	LastUpdate time.Time
}

// gitIcons holds the icons for different git states by style
type gitIcons struct {
	Branch   string
	Clean    string
	Dirty    string
	Staged   string
	Ahead    string
	Behind   string
	Stash    string
	Conflict string
	Detached string
}

var gitIconsByStyle = map[string]gitIcons{
	"nerd": {
		Branch: "", Clean: "", Dirty: "", Staged: "",
		Ahead: "", Behind: "", Stash: "", Conflict: "", Detached: "",
	},
	"emoji": {
		Branch: "🌿", Clean: "✓", Dirty: "●", Staged: "✚",
		Ahead: "↑", Behind: "↓", Stash: "📦", Conflict: "⚠️", Detached: "📍",
	},
	"ascii": {
		Branch: "[git]", Clean: "ok", Dirty: "*", Staged: "+",
		Ahead: "+", Behind: "-", Stash: "s", Conflict: "!!", Detached: "@",
	},
	"minimal": {
		Branch: "", Clean: "", Dirty: "*", Staged: "+",
		Ahead: "↑", Behind: "↓", Stash: "", Conflict: "!", Detached: "@",
	},
}

// floatingItem represents an emoji flying through the pet's space
type floatingItem struct {
	Emoji     string
	Pos       pos2D
	Velocity  pos2D // direction of movement
	ExpiresAt time.Time
}

// petState holds the current state of the pet widget
// Generic to support any creature type (cat, dog, etc.)
type petState struct {
	Pos           pos2D    // Creature's current position
	State         string    // idle, walking, jumping, playing, eating, sleeping, happy, shooting
	Direction     int       // -1 left, 1 right
	Hunger        int       // 0-100 (100 = full) - "Energy"
	Happiness     int       // 0-100 - "Mood"
	YarnPos       pos2D    // Toy position (can be pushed around)
	FoodItem      pos2D    // Dropped food position (-1,-1 = no food dropped)
	PoopPositions []int      // Where the poops are (X positions, always Y=0)
	NeedsPoopAt   time.Time  // When the pet will poop next (zero = doesn't need to)
	LastFed       time.Time
	LastPet       time.Time
	LastPoop      time.Time
	LastThought   string
	ThoughtScroll int // Scroll offset for marquee effect
	// Floating items in play space
	FloatingItems []floatingItem
	// Movement
	TargetPos     pos2D    // Where creature is moving to
	HasTarget     bool      // Whether creature has a movement target
	ActionPending string    // "eat", "play", "pet" - what to do when reaching target
	AnimFrame     int       // Animation frame counter
	// Lifetime stats
	TotalPets         int
	TotalFeedings     int
	TotalPoopsCleaned int
	TotalYarnPlays    int
}

// petStatePath returns the path to the shared pet state file
func petStatePath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "tabby")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "pet.json")
}

// loadPetState loads the pet state from the shared file
func loadPetState() petState {
	data, err := os.ReadFile(petStatePath())
	if err != nil {
		// Return default state if file doesn't exist
		return petState{
			Pos:       pos2D{X: 10, Y: 0},
			State:     "idle",
			Direction: 1,
			Hunger:    80,
			Happiness: 70,
			YarnPos:   pos2D{X: -1, Y: 0},
			FoodItem:  pos2D{X: -1, Y: -1}, // No food dropped
			HasTarget: false,
			LastFed:   time.Now(),
			LastPet:   time.Now(),
		}
	}
	var state petState
	if err := json.Unmarshal(data, &state); err != nil {
		return petState{
			Pos:       pos2D{X: 10, Y: 0},
			State:     "idle",
			Direction: 1,
			Hunger:    80,
			Happiness: 70,
			YarnPos:   pos2D{X: -1, Y: 0},
			FoodItem:  pos2D{X: -1, Y: -1}, // No food dropped
			HasTarget: false,
			LastFed:   time.Now(),
			LastPet:   time.Now(),
		}
	}
	return state
}

// savePetState saves the pet state to the shared file
func savePetState(state petState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	os.WriteFile(petStatePath(), data, 0644)
}

// clickedOnPoop checks if the click is on a poop (ground row only)
func (m model) clickedOnPoop(x int) bool {
	// Check if x is near any poop position (called only when on ground row)
	for _, poopX := range m.pet.PoopPositions {
		if x >= poopX-1 && x <= poopX+1 {
			return true
		}
	}
	return false
}

// cleanPoopAt removes a poop at or near the given X position
func (m *model) cleanPoopAt(x int) {
	for i, poopX := range m.pet.PoopPositions {
		if x >= poopX-1 && x <= poopX+1 {
			// Remove this poop
			m.pet.PoopPositions = append(m.pet.PoopPositions[:i], m.pet.PoopPositions[i+1:]...)
			return
		}
	}
}

// randomThought returns a random thought from the given category
func randomThought(category string) string {
	thoughts, ok := defaultPetThoughts[category]
	if !ok || len(thoughts) == 0 {
		thoughts = defaultPetThoughts["idle"]
	}
	return thoughts[rand.Intn(len(thoughts))]
}

// Default pet thoughts by state
var defaultPetThoughts = map[string][]string{
	"hungry": {
		"food. now.",
		"the bowl. it echoes.",
		"starving. dramatically.",
		"hunger level: critical.",
	},
	"poop": {
		"that won't clean itself.",
		"i made you a gift.",
		"cleanup crew needed.",
		"ahem. the floor.",
	},
	"happy": {
		"acceptable.",
		"fine. you may stay.",
		"purr engaged.",
		"not bad.",
	},
	"yarn": {
		"the yarn. it calls.",
		"must... catch...",
		"yarn acquired.",
	},
	"sleepy": {
		"nap time.",
		"zzz...",
		"five more minutes.",
	},
	"idle": {
		"chillin'.",
		"vibin'.",
		"just here.",
		"sup.",
		"...",
		"waiting.",
		"*yawn*",
		"hmm.",
	},
}

type model struct {
	windows    []tmux.Window
	grouped    []grouping.GroupedWindows
	config     *config.Config
	cursor     int         // Visual line position of cursor
	windowRefs []windowRef // Maps visual lines to windows
	paneRefs   []paneRef   // Maps visual lines to panes
	groupRefs  []groupRef  // Maps visual lines to groups
	totalLines int         // Total number of visual lines

	// Terminal size
	width  int // Terminal width (for dynamic resizing)
	height int // Terminal height

	// Confirmation dialog state
	confirmClose  bool         // Whether we're showing close confirmation
	confirmWindow *tmux.Window // Window pending close confirmation

	// Spinner animation state
	spinnerFrame  int  // Current frame index for busy spinner
	spinnerActive bool // Whether spinner ticker is running

	// Collapse state
	collapsedGroups      map[string]bool // groupName -> isCollapsed
	sidebarCollapsed     bool            // Whether the sidebar itself is collapsed to 1 char
	sidebarExpandedWidth int             // Remembered width when expanded

	// Double-click tracking
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int

	// Refresh suppression - after optimistic UI, ignore redundant USR1 signals
	lastOptimisticUpdate time.Time

	// Viewport for scrolling
	viewport viewport.Model
	ready    bool // Whether viewport is initialized

	// Pet widget state (generic creature)
	pet petState

	// Git widget state
	git gitState

	// Session widget state
	session sessionState

	// Stats widget state
	stats statsState
}

type refreshMsg struct{}

type reloadConfigMsg struct{}

type spinnerTickMsg struct{}

type periodicRefreshMsg struct{}

// sharedStateTickMsg is for fast shared state checks (30fps)
type sharedStateTickMsg struct{}

// clockTickMsg updates the clock widget every second
type clockTickMsg struct{}
type thoughtTickMsg struct{}
type catMoodTickMsg struct{}
type gitTickMsg struct{}

// triggerRefresh returns a command that triggers a refresh
func triggerRefresh() tea.Cmd {
	return func() tea.Msg {
		return refreshMsg{}
	}
}

// delayedRefresh waits a bit before refreshing (for operations like kill-window)
func delayedRefresh() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return refreshMsg{}
	})
}

// signalSidebarsDelayed signals all sidebars to refresh after a short delay
// This runs in a goroutine to avoid blocking the UI
func signalSidebarsDelayed() {
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("bash", "-c", `
		for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
			kill -USR1 "$pid" 2>/dev/null || true
		done
	`).Run()
}

// spinnerTick schedules the next spinner frame update
func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// periodicRefresh schedules periodic data refresh (for pane titles, etc.)
func periodicRefresh() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return periodicRefreshMsg{}
	})
}

// sharedStateTick schedules fast shared state checks (30fps for responsive UI)
func sharedStateTick() tea.Cmd {
	return tea.Tick(32*time.Millisecond, func(t time.Time) tea.Msg {
		return sharedStateTickMsg{}
	})
}

// clockTick schedules clock updates every second
func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return clockTickMsg{}
	})
}

// thoughtTick schedules periodic LLM thought generation
func thoughtTick(intervalSecs int) tea.Cmd {
	if intervalSecs <= 0 {
		intervalSecs = 300 // default 5 minutes
	}
	return tea.Tick(time.Duration(intervalSecs)*time.Second, func(t time.Time) tea.Msg {
		return thoughtTickMsg{}
	})
}

// catMoodTick schedules random cat mood/activity changes
func catMoodTick() tea.Cmd {
	// Random interval between 5-30 seconds
	interval := time.Duration(5+rand.Intn(25)) * time.Second
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return catMoodTickMsg{}
	})
}

// gitTick schedules periodic git status updates
func gitTick(intervalSecs int) tea.Cmd {
	if intervalSecs <= 0 {
		intervalSecs = 5 // default 5 seconds
	}
	return tea.Tick(time.Duration(intervalSecs)*time.Second, func(t time.Time) tea.Msg {
		return gitTickMsg{}
	})
}

// hasAnimatedIndicators checks if any window has an animated indicator (busy or input with frames)
func (m model) hasAnimatedIndicators() bool {
	for _, w := range m.windows {
		if w.Busy {
			return true
		}
		// Input indicator is animated if it has frames configured
		if w.Input && len(m.config.Indicators.Input.Frames) > 0 {
			return true
		}
	}
	return false
}

// loadCollapsedGroups reads collapsed group state from tmux session option @tabby_collapsed_groups
// Returns a map of group names to collapsed state
func loadCollapsedGroups() map[string]bool {
	result := make(map[string]bool)
	out, err := exec.Command("tmux", "show-options", "-v", "-q", "@tabby_collapsed_groups").Output()
	if err != nil || len(out) == 0 {
		return result
	}
	var groups []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &groups); err != nil {
		return result
	}
	for _, g := range groups {
		result[g] = true
	}
	return result
}

// saveCollapsedGroups writes collapsed group state to tmux session option @tabby_collapsed_groups
func saveCollapsedGroups(collapsed map[string]bool) {
	var groups []string
	for name, isCollapsed := range collapsed {
		if isCollapsed {
			groups = append(groups, name)
		}
	}
	if len(groups) == 0 {
		// Clear the option if nothing is collapsed
		_ = exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
		return
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return
	}
	_ = exec.Command("tmux", "set-option", "@tabby_collapsed_groups", string(data)).Run()
}

// toggleGroupCollapse toggles the collapsed state for a group
func (m *model) toggleGroupCollapse(groupName string) {
	if m.collapsedGroups == nil {
		m.collapsedGroups = make(map[string]bool)
	}
	m.collapsedGroups[groupName] = !m.collapsedGroups[groupName]
	saveCollapsedGroups(m.collapsedGroups)
}

// isGroupCollapsed returns whether a group is collapsed
func (m model) isGroupCollapsed(groupName string) bool {
	if m.collapsedGroups == nil {
		return false
	}
	return m.collapsedGroups[groupName]
}

// toggleSidebarCollapse collapses or expands the sidebar
func (m *model) toggleSidebarCollapse() {
	if m.sidebarCollapsed {
		// Expand: restore previous width
		width := m.sidebarExpandedWidth
		if width < 20 {
			width = 25 // Default width
		}
		_ = exec.Command("tmux", "resize-pane", "-x", fmt.Sprintf("%d", width)).Run()
		m.sidebarCollapsed = false
	} else {
		// Collapse: save current width and shrink to 2 chars
		m.sidebarExpandedWidth = m.width
		_ = exec.Command("tmux", "resize-pane", "-x", "2").Run()
		m.sidebarCollapsed = true
	}
}

// toggleWindowCollapse toggles the collapsed state for a window (hides/shows panes)
func toggleWindowCollapse(windowIndex int, collapsed bool) {
	if collapsed {
		_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", windowIndex), "@tabby_collapsed", "1").Run()
	} else {
		_ = exec.Command("tmux", "set-window-option", "-t", fmt.Sprintf(":%d", windowIndex), "-u", "@tabby_collapsed").Run()
	}
}

// getIndicatorIcon returns the icon for an indicator, using animation frames if available
func (m model) getIndicatorIcon(ind config.Indicator) string {
	// If frames are configured, use the current animation frame
	if len(ind.Frames) > 0 {
		return ind.Frames[m.spinnerFrame%len(ind.Frames)]
	}
	// Fall back to single icon
	return ind.Icon
}

// getBusyFrames returns the spinner frames for the busy indicator
func (m model) getBusyFrames() []string {
	if len(m.config.Indicators.Busy.Frames) > 0 {
		return m.config.Indicators.Busy.Frames
	}
	return defaultSpinnerFrames
}

// isTouchMode returns true if touch mode is active
// Currently disabled - only explicit config or env var enables it
func (m model) isTouchMode() bool {
	// Only explicit config or env var enables touch mode
	if m.config.Sidebar.TouchMode {
		return true
	}
	if os.Getenv("TABBY_TOUCH") == "1" {
		return true
	}
	return false
}

// touchLineHeight returns number of lines per tab in touch mode
func (m model) touchLineHeight() int {
	// Always 1 line per tab for now (touch mode disabled)
	return 1
}

// lineSpacing returns extra newlines for touch mode / line_height setting
func (m model) lineSpacing() string {
	if m.isTouchMode() {
		return "\n" // Extra line in touch mode
	}
	if m.config.Sidebar.LineHeight > 0 {
		return strings.Repeat("\n", m.config.Sidebar.LineHeight)
	}
	return ""
}

// touchDivider returns a horizontal divider line for touch mode
func (m model) touchDivider(color string) string {
	if !m.isTouchMode() {
		return ""
	}
	if color == "" {
		color = "#444444"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	return style.Render(strings.Repeat("─", m.width)) + "\n"
}

// touchGroupEnd returns spacing after the last item in a group
func (m model) touchGroupEnd() string {
	if !m.isTouchMode() {
		return ""
	}
	return "\n" // Blank line between groups
}

func (m model) Init() tea.Cmd {
	// Start periodic refresh for pane titles, fast shared state checks, and clock updates
	cmds := []tea.Cmd{periodicRefresh(), sharedStateTick()}
	if m.config.Widgets.Clock.Enabled {
		cmds = append(cmds, clockTick())
	}

	// Start spinner for pet widget animation
	if m.config.Widgets.Pet.Enabled {
		cmds = append(cmds, spinnerTick())
		// Initialize LLM for pet thoughts if enabled
		if m.config.Widgets.Pet.Thoughts {
			pet := m.config.Widgets.Pet
			if err := initLLM(pet.LLMProvider, pet.LLMModel, pet.LLMAPIKey); err != nil {
				if m.config.Sidebar.Debug && debugLog != nil {
					debugLog.Printf("LLM init failed: %v", err)
				}
			} else {
				if m.config.Sidebar.Debug && debugLog != nil {
					debugLog.Printf("LLM initialized successfully, starting thought tick")
				}
				cmds = append(cmds, thoughtTick(pet.ThoughtInterval))
			}
		}
		// Start random cat mood/activity timer
		cmds = append(cmds, catMoodTick())
	}
	// Start git status updates
	if m.config.Widgets.Git.Enabled {
		m.updateGitStatus() // Initial update
		cmds = append(cmds, gitTick(m.config.Widgets.Git.UpdateInterval))
	}
	// Start system stats updates
	if m.config.Widgets.Stats.Enabled {
		m.updateStats() // Initial update
		cmds = append(cmds, statsTick(m.config.Widgets.Stats.UpdateInterval))
	}
	return tea.Batch(cmds...)
}

func (m *model) buildWindowRefs() {
	m.windowRefs = make([]windowRef, 0)
	m.paneRefs = make([]paneRef, 0)
	m.groupRefs = make([]groupRef, 0)
	line := 0

	// Get line height for touch mode (1 in normal mode, 3+ in touch mode)
	tabHeight := m.touchLineHeight()

	// Iterate over grouped windows - this keeps each group together
	// Windows within each group are already sorted by index
	for gi := range m.grouped {
		group := &m.grouped[gi]

		// Group header line - track for right-click menu
		groupStart := line
		m.groupRefs = append(m.groupRefs, groupRef{
			group:     group,
			startLine: groupStart,
			endLine:   groupStart, // Single line for group headers
		})
		line++

		// Skip windows if group is collapsed
		if m.isGroupCollapsed(group.Name) {
			continue
		}

		for wi := range group.Windows {
			win := &group.Windows[wi]

			windowStart := line
			m.windowRefs = append(m.windowRefs, windowRef{
				window:    win,
				startLine: windowStart,
				endLine:   windowStart + tabHeight - 1, // Multi-line in touch mode
			})
			line += tabHeight

			// Track pane lines if window has multiple panes and window is not collapsed
			if len(win.Panes) > 1 && !win.Collapsed {
				for pi := range win.Panes {
					paneStart := line
					m.paneRefs = append(m.paneRefs, paneRef{
						pane:      &win.Panes[pi],
						window:    win,
						windowIdx: win.Index,
						startLine: paneStart,
						endLine:   paneStart + tabHeight - 1,
					})
					line += tabHeight
				}
			}
		}
	}
	m.totalLines = line
}

// getWindowAtLine returns the window at the given visual line number
// Uses range checks for multi-line touch targets
func (m model) getWindowAtLine(y int) *tmux.Window {
	for _, ref := range m.windowRefs {
		if y >= ref.startLine && y <= ref.endLine {
			return ref.window
		}
	}
	return nil
}

// getPaneAtLine returns the pane at the given visual line number
func (m model) getPaneAtLine(y int) (*paneRef, bool) {
	for i, ref := range m.paneRefs {
		if y >= ref.startLine && y <= ref.endLine {
			return &m.paneRefs[i], true
		}
	}
	return nil, false
}

// getGroupAtLine returns the group at the given visual line number
func (m model) getGroupAtLine(y int) (*groupRef, bool) {
	for i, ref := range m.groupRefs {
		if y >= ref.startLine && y <= ref.endLine {
			return &m.groupRefs[i], true
		}
	}
	return nil, false
}

// getSelectedWindow returns the window at the current cursor position
func (m model) getSelectedWindow() *tmux.Window {
	for _, ref := range m.windowRefs {
		if m.cursor >= ref.startLine && m.cursor <= ref.endLine {
			return ref.window
		}
	}
	return nil
}

// isWindowLine checks if the given line contains a window (not a group header)
func (m model) isWindowLine(y int) bool {
	for _, ref := range m.windowRefs {
		if y >= ref.startLine && y <= ref.endLine {
			return true
		}
	}
	return false
}

// translateMouseY converts screen Y coordinate to content Y coordinate
// accounting for viewport scroll offset
func (m model) translateMouseY(screenY int) int {
	if !m.ready {
		return screenY
	}
	return screenY + m.viewport.YOffset
}

// calculateButtonLines returns the line numbers for New Tab, New Group, and Close Tab buttons
func (m model) calculateButtonLines() (newTabLine, newGroupLine, closeTabLine, collapseLine int) {
	// Buttons appear after all groups with a blank line
	baseLine := m.totalLines + 1 // +1 for blank line

	newTabLine = -1
	newGroupLine = -1
	closeTabLine = -1
	collapseLine = -1 // Not used but kept for compatibility

	if m.config.Sidebar.NewTabButton {
		newTabLine = baseLine
		baseLine++
	}
	if m.config.Sidebar.NewGroupButton {
		newGroupLine = baseLine
		baseLine++
	}
	if m.config.Sidebar.CloseButton {
		closeTabLine = baseLine
	}
	return
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Log message types when TABBY_CLICK_DEBUG=1
	if clickDebugEnabled {
		if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			activeIdx := -1
			for _, w := range m.windows {
				if w.Active {
					activeIdx = w.Index
					break
				}
			}
			fmt.Fprintf(f, "%s [%d] UPDATE: msg=%T active=%d\n", time.Now().Format("15:04:05.000"), os.Getpid(), msg, activeIdx)
			f.Close()
		}
	}

	// Handle viewport scrolling (mouse wheel, etc.)
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	if cmd != nil {
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle confirmation dialog first
		if m.confirmClose && m.confirmWindow != nil {
			switch msg.String() {
			case "y", "Y":
				// Confirmed - kill the window and switch to last window
				windowIdx := m.confirmWindow.Index
				m.confirmClose = false
				m.confirmWindow = nil
				// Kill window, switch to last window, then focus main pane
				_ = exec.Command("bash", "-c", fmt.Sprintf(`
					tmux kill-window -t :%d
					tmux last-window 2>/dev/null || tmux select-window -t :0
					main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
					if [ -n "$main_pane" ]; then
						tmux select-pane -t "$main_pane"
					fi
				`, windowIdx)).Run()
				// Signal all sidebars to refresh after brief delay
				go func() {
					time.Sleep(100 * time.Millisecond)
					_ = exec.Command("bash", "-c", `
						for pid in $(tmux list-panes -s -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
							kill -USR1 "$pid" 2>/dev/null || true
						done
					`).Run()
				}()
				return m, delayedRefresh()
			case "n", "N", "esc", "escape":
				// Cancelled
				m.confirmClose = false
				m.confirmWindow = nil
				return m, nil
			default:
				// Any other key cancels
				m.confirmClose = false
				m.confirmWindow = nil
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc", "escape":
			_ = exec.Command("tmux", "last-pane").Run()
			return m, nil
		case "j", "down":
			// Move cursor down to next window line
			for _, ref := range m.windowRefs {
				if ref.startLine > m.cursor {
					m.cursor = ref.startLine
					break
				}
			}
		case "k", "up":
			// Move cursor up to previous window line
			for i := len(m.windowRefs) - 1; i >= 0; i-- {
				if m.windowRefs[i].startLine < m.cursor {
					m.cursor = m.windowRefs[i].startLine
					break
				}
			}
		case "enter":
			if win := m.getSelectedWindow(); win != nil {
				// OPTIMISTIC UI: Update local state immediately for instant feedback
				selectedIndex := win.Index
				for i := range m.windows {
					m.windows[i].Active = (m.windows[i].Index == selectedIndex)
				}
				// Broadcast to all sidebars via shared state file
				writeSharedActive(selectedIndex)
				m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
				m.buildWindowRefs()
				m.lastOptimisticUpdate = time.Now() // Suppress redundant USR1 refresh

				// Run tmux commands async - tmux hooks will signal sidebars
				go func(idx int) {
					exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", idx)).Run()
					// Select the pane that isn't running sidebar
					exec.Command("bash", "-c", `
						main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
						if [ -n "$main_pane" ]; then
							tmux select-pane -t "$main_pane"
						fi
					`).Run()
				}(selectedIndex)
				return m, nil
			}
		case "d", "x":
			if win := m.getSelectedWindow(); win != nil {
				// Enter confirmation mode
				m.confirmClose = true
				m.confirmWindow = win
				return m, nil
			}
		case "c", "n":
			// Just create new window - the after-new-window hook adds the sidebar
			// 'c' matches tmux default, 'n' kept for compatibility
			_ = exec.Command("tmux", "new-window").Run()
			return m, delayedRefresh()
		case "|", "%":
			// Horizontal split (left/right) - matches tmux prefix + %
			_ = exec.Command("bash", "-c", `
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux split-window -h -t "$main_pane" -c "#{pane_current_path}"
				fi
			`).Run()
			return m, nil
		case "-", "\"":
			// Vertical split (top/bottom) - matches tmux prefix + "
			_ = exec.Command("bash", "-c", `
				main_pane=$(tmux list-panes -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
				if [ -n "$main_pane" ]; then
					tmux split-window -v -t "$main_pane" -c "#{pane_current_path}"
				fi
			`).Run()
			return m, nil
		case "ctrl+<", "ctrl+[", "alt+<", "alt+[":
			// Collapse/expand sidebar (requires modifier key)
			m.toggleSidebarCollapse()
			return m, nil
		}

	case tea.MouseMsg:
		// If sidebar is collapsed, any click expands it
		if m.sidebarCollapsed {
			if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
				m.toggleSidebarCollapse()
				return m, nil
			}
			return m, nil // Ignore other mouse events when collapsed
		}

		// Translate Y coordinate for viewport scroll offset
		contentY := m.translateMouseY(msg.Y)

		// Update cursor on hover if it's a window line
		if m.isWindowLine(contentY) {
			m.cursor = contentY
		}

		clicked := m.getWindowAtLine(contentY)
		newTabLine, newGroupLine, closeTabLine, _ := m.calculateButtonLines()

		// Log mouse events when TABBY_CLICK_DEBUG=1
		if clickDebugEnabled {
			if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				fmt.Fprintf(f, "%s MOUSE: action=%v button=%v x=%d y=%d contentY=%d\n", time.Now().Format("15:04:05.000"), msg.Action, msg.Button, msg.X, msg.Y, contentY)
				f.Close()
			}
		}

		// Double-click collapse disabled - was too easy to trigger accidentally

		// Check for click on right edge (divider area) - collapse sidebar
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.width > 0 && msg.X >= m.width-1 {
				m.toggleSidebarCollapse()
				return m, nil
			}
		}

		// Check for click on pet widget (in pinned area at bottom)
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			pinnedHeight := m.pinnedContentHeight()
			pinnedStart := m.height - pinnedHeight
			// Debug: log to file
			if f, err := os.OpenFile("/tmp/pet-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				pet := m.config.Widgets.Pet
				contentStart := pet.MarginTop + 1 + pet.PaddingTop
				relY := msg.Y - pinnedStart - contentStart
				fmt.Fprintf(f, "Y=%d pinnedStart=%d pinnedHeight=%d contentStart=%d relY=%d width=%d\n",
					msg.Y, pinnedStart, pinnedHeight, contentStart, relY, m.width)
				f.Close()
			}
			if msg.Y >= pinnedStart && m.config.Widgets.Pet.Enabled {
				// Determine which row was clicked (relative to pet widget)
				// Account for margins, padding, and divider
				pet := m.config.Widgets.Pet
				contentStart := pet.MarginTop + 1 + pet.PaddingTop // margin + divider + padding
				relY := msg.Y - pinnedStart - contentStart
				x := msg.X

				// Get yarn position for comparison
				yarnX := m.pet.YarnPos.X
				if yarnX < 0 {
					yarnX = m.width - 4
				}

				// Layout (after divider): 0=thought, 1=high air, 2=low air/ground, 3=items (4=bars if narrow)
				// Note: visual offset -1 due to how terminal renders pinned content
				groundRowY := 2
				itemsRowY := 3
				if m.width < 18 {
					itemsRowY = 4 // Narrow mode: icons on 3, bars on 4
				}

				// Items bar clicks
				iconsRowY := 3 // icons are on row 3
				if relY == iconsRowY || relY == itemsRowY {
					if x <= 2 {
						// Food icon - drop food from sky
						dropX := m.pet.Pos.X + 3 + rand.Intn(5)
						if dropX >= m.width-2 {
							dropX = m.width / 2
						}
						if dropX < 2 {
							dropX = 2
						}
						m.pet.FoodItem = pos2D{X: dropX, Y: 2}
						m.pet.LastThought = "food incoming!"
					} else if x >= 3 && x <= 5 {
						// Yarn icon - toss yarn to random spot
						tossX := rand.Intn(m.width-4) + 2
						if tossX < 2 {
							tossX = 2
						}
						m.pet.YarnPos = pos2D{X: tossX, Y: 2}
						m.pet.TargetPos = pos2D{X: tossX, Y: 0}
						m.pet.HasTarget = true
						m.pet.ActionPending = "play"
						m.pet.State = "walking"
						m.pet.LastThought = "yarn!"
					}
				} else if relY == groundRowY {
					// Ground row - check poop, pet, yarn, then empty space
					petX := m.pet.Pos.X
					if petX >= m.width {
						petX = m.width - 1
					}
					if m.clickedOnPoop(x) {
						// Clicked on poop - clean it
						m.cleanPoopAt(x)
						m.pet.TotalPoopsCleaned++
						m.pet.Happiness = min(100, m.pet.Happiness+5)
						m.pet.LastThought = "finally."
					} else if x >= petX-1 && x <= petX+1 {
						// Clicked on pet - pet it
						m.pet.Happiness = min(100, m.pet.Happiness+10)
						m.pet.State = "happy"
						m.pet.LastPet = time.Now()
						m.pet.TotalPets++
						m.pet.LastThought = "purrrr"
					} else if x >= yarnX-1 && x <= yarnX+1 {
						// Clicked on yarn - chase it
						m.pet.TargetPos = pos2D{X: yarnX, Y: 0}
						m.pet.HasTarget = true
						m.pet.ActionPending = "play"
						m.pet.State = "walking"
						m.pet.LastThought = "yarn!"
					} else {
						// Clicked elsewhere on ground - throw yarn there
						m.pet.YarnPos = pos2D{X: x, Y: 2}
						m.pet.TargetPos = pos2D{X: x, Y: 0}
						m.pet.HasTarget = true
						m.pet.ActionPending = "play"
						m.pet.State = "walking"
						m.pet.LastThought = "the yarn!"
					}
				} else if relY == 0 {
					// Thought bubble - generate new thought
					if m.config.Widgets.Pet.Thoughts {
						if thought := generateLLMThought(&m.pet, m.config.Widgets.Pet.Name); thought != "" {
							m.pet.LastThought = thought
						} else {
							m.pet.LastThought = randomThought("idle")
						}
					} else {
						m.pet.LastThought = randomThought("idle")
					}
				} else if relY == 1 {
					// High air row - throw yarn
					m.pet.YarnPos = pos2D{X: x, Y: 2}
					m.pet.TargetPos = pos2D{X: x, Y: 0}
					m.pet.HasTarget = true
					m.pet.ActionPending = "play"
					m.pet.State = "walking"
					m.pet.LastThought = "the yarn!"
				}
				// Save state after any click action
				savePetState(m.pet)
				return m, nil
			}
		}

		// Handle mouse clicks - check for press action
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonLeft:
				// Check if clicking on group header collapse toggle (first 2 chars)
				if groupRef, ok := m.getGroupAtLine(contentY); ok && msg.X < 2 {
					m.toggleGroupCollapse(groupRef.group.Name)
					m.buildWindowRefs()
					return m, nil
				}
				// Check if clicking on window collapse toggle (chars 3-4, after tree branch)
				// This only works for windows that have multiple panes
				if clicked != nil && msg.X >= 3 && msg.X <= 4 && len(clicked.Panes) > 1 {
					toggleWindowCollapse(clicked.Index, !clicked.Collapsed)
					// Signal all sidebars to refresh
					go signalSidebarsDelayed()
					return m, delayedRefresh()
				}
				// Check if clicking on a pane first
				if paneRef, ok := m.getPaneAtLine(contentY); ok {
					// OPTIMISTIC UI: Update local state immediately
					windowIdx := paneRef.windowIdx
					paneID := paneRef.pane.ID
					for i := range m.windows {
						m.windows[i].Active = (m.windows[i].Index == windowIdx)
						// Also update active pane within the window
						for j := range m.windows[i].Panes {
							m.windows[i].Panes[j].Active = (m.windows[i].Panes[j].ID == paneID)
						}
					}
					// Broadcast to all sidebars via shared state file
					writeSharedActive(windowIdx)
					m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
					m.buildWindowRefs()
					m.lastOptimisticUpdate = time.Now() // Suppress redundant USR1 refresh

					// Send tmux command async - tmux hooks will signal sidebars
					go exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", windowIdx), ";",
						"select-pane", "-t", paneID).Run()
					return m, nil
				} else if clicked != nil {
					// OPTIMISTIC UI: Update local state immediately for instant feedback
					clickedIndex := clicked.Index
					if clickDebugEnabled {
						if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
							fmt.Fprintf(f, "%s CLICK: window %d, updating Active states\n", time.Now().Format("15:04:05.000"), clickedIndex)
							f.Close()
						}
					}
					for i := range m.windows {
						m.windows[i].Active = (m.windows[i].Index == clickedIndex)
					}
					// Broadcast to all sidebars via shared state file
					writeSharedActive(clickedIndex)
					// Recompute grouped state with optimistic update
					m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
					m.buildWindowRefs()
					m.lastOptimisticUpdate = time.Now() // Suppress redundant USR1 refresh

					// Send tmux command async - tmux hooks will signal sidebars
					go exec.Command("tmux", "select-window", "-t", fmt.Sprintf(":%d", clickedIndex)).Run()

					// Return immediately with updated model - View() should be called next
					return m, nil
				} else if m.config.Sidebar.NewTabButton && contentY == newTabLine {
					// Just create new window - the after-new-window hook adds the sidebar
					_ = exec.Command("tmux", "new-window").Run()
					return m, delayedRefresh()
				} else if m.config.Sidebar.NewGroupButton && contentY == newGroupLine {
					// Open new group prompt
					m.showNewGroupPrompt()
					return m, nil
				} else if m.config.Sidebar.CloseButton && contentY == closeTabLine {
					// Close currently selected window (cursor position) - with confirmation
					if win := m.getSelectedWindow(); win != nil {
						m.confirmClose = true
						m.confirmWindow = win
						return m, nil
					}
				}
			case tea.MouseButtonMiddle:
				if clicked != nil {
					// Middle-click closes the clicked window - with confirmation
					m.confirmClose = true
					m.confirmWindow = clicked
					return m, nil
				}
			case tea.MouseButtonRight:
				// Check if clicking on a group header first
				if groupRef, ok := m.getGroupAtLine(contentY); ok {
					m.showGroupContextMenu(groupRef)
					return m, triggerRefresh()
				}
				// Check if clicking on a pane
				if paneRef, ok := m.getPaneAtLine(contentY); ok {
					m.showPaneContextMenu(paneRef)
					return m, triggerRefresh()
				}
				// Check if right-clicking on indicator column (X=0) with an alert
				if clicked != nil && msg.X == 0 && (clicked.Activity || clicked.Bell || clicked.Busy) {
					m.showAlertPopup(clicked)
					return m, nil
				}
				// Otherwise check for window context menu
				if clicked != nil {
					m.showContextMenu(clicked)
					return m, triggerRefresh()
				}
			}
		}

	case refreshMsg:
		// Always refresh on USR1 - the new sidebar needs current state
		// (optimistic UI only helps the sidebar that was clicked)

		t := perf.Start("Update.refreshMsg")
		defer t.Stop()
		windows, _ := tmux.ListWindowsWithPanes()

		// Check for shared state from another sidebar's click
		if sharedIdx, ok := readSharedActive(); ok {
			for i := range windows {
				windows[i].Active = (windows[i].Index == sharedIdx)
			}
		}

		// Clear bell flag for active windows (user has seen it) - async
		for _, w := range windows {
			if w.Active && w.Bell {
				go exec.Command("tmux", "set-option", "-t", fmt.Sprintf(":%d", w.Index), "-wu", "@tabby_bell").Run()
			}
		}
		m.windows = windows
		m.grouped = grouping.GroupWindowsWithOptions(windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
		// Always update pane colors (custom colors can change anytime)
		updatePaneHeaderColors(m.grouped)
		m.buildWindowRefs()
		// Ensure cursor is still on a valid window line
		if !m.isWindowLine(m.cursor) && len(m.windowRefs) > 0 {
			m.cursor = m.windowRefs[0].startLine
		}
		// Sync viewport content
		if m.ready {
			m.viewport.SetContent(m.generateMainContent())
		}
		// Start spinner animation if any windows are busy and not already running
		if m.hasAnimatedIndicators() && !m.spinnerActive {
			m.spinnerActive = true
			return m, spinnerTick()
		}
		return m, nil

	case spinnerTickMsg:
		// Advance spinner frame
		busyFrames := m.getBusyFrames()
		m.spinnerFrame = (m.spinnerFrame + 1) % len(busyFrames)

		// Pet widget animation
		if m.config.Widgets.Pet.Enabled {
			// Reload shared state periodically (every 5 frames = 500ms)
			if m.pet.AnimFrame%5 == 0 {
				sharedState := loadPetState()
				// Only sync if we don't have an active action (let the active sidebar drive)
				if !m.pet.HasTarget && m.pet.State == "idle" {
					m.pet = sharedState
				}
			}

			m.pet.AnimFrame++
			catChanged := false

			// Yarn gravity - falls if in air
			if m.pet.YarnPos.Y > 0 {
				m.pet.YarnPos.Y--
				catChanged = true
			}

			// Cat gravity - falls back to ground after jumping
			if m.pet.Pos.Y > 0 {
				m.pet.Pos.Y--
				catChanged = true
				if m.pet.Pos.Y == 0 && m.pet.State == "jumping" {
					m.pet.State = "idle"
				}
			}

			// Food gravity - falls if in air
			if m.pet.FoodItem.X >= 0 && m.pet.FoodItem.Y > 0 {
				m.pet.FoodItem.Y--
				catChanged = true
				// When food lands, pet should chase it
				if m.pet.FoodItem.Y == 0 && !m.pet.HasTarget {
					m.pet.TargetPos = pos2D{X: m.pet.FoodItem.X, Y: 0}
					m.pet.HasTarget = true
					m.pet.ActionPending = "eat"
					m.pet.State = "walking"
					m.pet.LastThought = "food!"
				}
			}

			// Check if pet needs to poop
			if !m.pet.NeedsPoopAt.IsZero() && time.Now().After(m.pet.NeedsPoopAt) {
				// Spawn poop at pet's current position
				poopX := m.pet.Pos.X
				// Make sure it's not right on top of pet, offset slightly
				if poopX > 0 {
					poopX--
				}
				m.pet.PoopPositions = append(m.pet.PoopPositions, poopX)
				m.pet.LastPoop = time.Now()
				m.pet.NeedsPoopAt = time.Time{} // Clear the scheduled poop
				m.pet.LastThought = randomThought("poop")
				catChanged = true
			}

			// Clamp pet position to current width
			maxX := m.width - 1
			if maxX < 1 {
				maxX = 1
			}
			if m.pet.Pos.X > maxX {
				m.pet.Pos.X = maxX
				catChanged = true
			}
			if m.pet.Pos.X < 0 {
				m.pet.Pos.X = 0
				catChanged = true
			}

			// Clamp target to bounds too
			if m.pet.TargetPos.X > maxX {
				m.pet.TargetPos.X = maxX
			}
			if m.pet.TargetPos.X < 0 {
				m.pet.TargetPos.X = 0
			}

			// Pet movement toward target
			if m.pet.HasTarget {
				catChanged = true
				// Move pet toward target X
				if m.pet.Pos.X < m.pet.TargetPos.X {
					m.pet.Pos.X++
					m.pet.Direction = 1
				} else if m.pet.Pos.X > m.pet.TargetPos.X {
					m.pet.Pos.X--
					m.pet.Direction = -1
				}
				// Clamp after move
				if m.pet.Pos.X > maxX {
					m.pet.Pos.X = maxX
				}
				if m.pet.Pos.X < 0 {
					m.pet.Pos.X = 0
				}

				// If yarn is ahead and pet is chasing, push it
				if m.pet.ActionPending == "play" {
					yarnX := m.pet.YarnPos.X
					if yarnX < 0 {
						yarnX = m.width - 4
					}
					// Pet pushes yarn when it reaches it
					if m.pet.Pos.X == yarnX || m.pet.Pos.X == yarnX-1 || m.pet.Pos.X == yarnX+1 {
						// Push yarn in direction of movement
						newYarnX := yarnX + m.pet.Direction*2
						if newYarnX >= 2 && newYarnX < m.width-2 {
							m.pet.YarnPos.X = newYarnX
							m.pet.YarnPos.Y = 1 // Bounce up
							m.pet.TargetPos.X = newYarnX
						}
					}
				}

				// Check if reached target
				if m.pet.Pos.X == m.pet.TargetPos.X && m.pet.Pos.Y == m.pet.TargetPos.Y {
					m.pet.HasTarget = false
					// Perform pending action
					switch m.pet.ActionPending {
					case "eat":
						m.pet.Hunger = 100
						m.pet.State = "eating"
						m.pet.LastFed = time.Now()
						m.pet.TotalFeedings++
						m.pet.LastThought = "nom nom nom"
						// Clear the dropped food item
						m.pet.FoodItem = pos2D{X: -1, Y: -1}
						// Schedule potential poop (based on config chance)
						poopChance := m.config.Widgets.Pet.PoopChance
						if poopChance <= 0 {
							poopChance = 50 // default 50%
						}
						if rand.Intn(100) < poopChance {
							// Will poop in 3-8 seconds
							m.pet.NeedsPoopAt = time.Now().Add(time.Duration(3+rand.Intn(5)) * time.Second)
						}
					case "play":
						m.pet.State = "playing"
						m.pet.Happiness = min(100, m.pet.Happiness+5)
						m.pet.TotalYarnPlays++
						m.pet.LastThought = "got it!"
					default:
						m.pet.State = "idle"
					}
					m.pet.ActionPending = ""
				}
			} else if m.pet.State == "eating" || m.pet.State == "playing" || m.pet.State == "happy" || m.pet.State == "shooting" {
				// Return to idle after a few frames
				if m.pet.AnimFrame%20 == 0 {
					m.pet.State = "idle"
					m.pet.LastThought = randomThought("idle")
					catChanged = true
				}
			}

			// Thought marquee - scroll every 3 frames (300ms)
			if m.pet.AnimFrame%3 == 0 {
				thoughtWidth := runewidth.StringWidth(m.pet.LastThought)
				maxWidth := m.width - 4 // account for 💭 icon
				if thoughtWidth > maxWidth {
					// Scroll through the thought with wrap-around
					m.pet.ThoughtScroll++
					if m.pet.ThoughtScroll > thoughtWidth+3 { // +3 for gap before repeat
						m.pet.ThoughtScroll = 0
					}
				} else {
					m.pet.ThoughtScroll = 0
				}
			}

			// Save state if changed
			if catChanged {
				savePetState(m.pet)
			}
		}

		// Continue animation if still have busy windows or pet widget is active
		if m.hasAnimatedIndicators() || m.config.Widgets.Pet.Enabled {
			return m, spinnerTick()
		}
		// Stop animation
		m.spinnerActive = false
		return m, nil

	case reloadConfigMsg:
		cfg, err := config.LoadConfig(config.DefaultConfigPath())
		if err == nil {
			m.config = cfg
			m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
			updatePaneHeaderColors(m.grouped)
			m.buildWindowRefs()
			// Sync viewport content
			if m.ready {
				m.viewport.SetContent(m.generateMainContent())
			}
		}
		return m, nil

	case periodicRefreshMsg:
		// Periodic refresh for pane titles and other dynamic data
		windows, _ := tmux.ListWindowsWithPanes()

		// Check for shared state from another sidebar's click
		// This allows instant cross-sidebar updates without waiting for tmux
		if sharedIdx, ok := readSharedActive(); ok {
			// Override Active flags to match shared state
			for i := range windows {
				windows[i].Active = (windows[i].Index == sharedIdx)
			}
		}

		// Clear bell flag for active windows (user has seen it) - async
		for _, w := range windows {
			if w.Active && w.Bell {
				go exec.Command("tmux", "set-option", "-t", fmt.Sprintf(":%d", w.Index), "-wu", "@tabby_bell").Run()
			}
		}
		m.windows = windows
		m.grouped = grouping.GroupWindowsWithOptions(windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
		updatePaneHeaderColors(m.grouped)
		m.buildWindowRefs()
		// Update session info for widget
		if m.config.Widgets.Session.Enabled {
			m.updateSessionInfo()
		}
		// Sync viewport content
		if m.ready {
			m.viewport.SetContent(m.generateMainContent())
		}
		// Schedule next periodic refresh
		return m, periodicRefresh()

	case sharedStateTickMsg:
		// Fast tick (30fps) - only check shared state, no tmux queries
		if sharedIdx, ok := readSharedActive(); ok {
			// Find current active window
			currentActive := -1
			for _, w := range m.windows {
				if w.Active {
					currentActive = w.Index
					break
				}
			}
			// Only update if different
			if sharedIdx != currentActive {
				for i := range m.windows {
					m.windows[i].Active = (m.windows[i].Index == sharedIdx)
				}
				m.grouped = grouping.GroupWindowsWithOptions(m.windows, m.config.Groups, m.config.Sidebar.ShowEmptyGroups)
				m.buildWindowRefs()
				// Sync viewport content
				if m.ready {
					m.viewport.SetContent(m.generateMainContent())
				}
			}
		}
		return m, sharedStateTick()

	case clockTickMsg:
		// Clock updates every second - just schedule next tick, View() uses time.Now()
		return m, clockTick()

	case thoughtTickMsg:
		// Generate a new LLM thought for the pet
		if m.config.Widgets.Pet.Enabled && m.config.Widgets.Pet.Thoughts {
			if thought := generateLLMThought(&m.pet, m.config.Widgets.Pet.Name); thought != "" {
				m.pet.LastThought = thought
				savePetState(m.pet)
			}
			return m, thoughtTick(m.config.Widgets.Pet.ThoughtInterval)
		}
		return m, nil

	case catMoodTickMsg:
		// Update floating items - move them and remove expired ones
		now := time.Now()
		var activeItems []floatingItem
		for _, item := range m.pet.FloatingItems {
			if now.Before(item.ExpiresAt) {
				item.Pos.X += item.Velocity.X
				item.Pos.Y += item.Velocity.Y
				// Keep in bounds
				if item.Pos.X >= 0 && item.Pos.X < m.width && item.Pos.Y >= 0 && item.Pos.Y <= 2 {
					activeItems = append(activeItems, item)
				}
			}
		}
		m.pet.FloatingItems = activeItems

		// Random cat mood/activity - make the cat do things on its own
		if m.config.Widgets.Pet.Enabled && m.pet.State == "idle" && !m.pet.HasTarget {
			// 30% chance to do something
			if rand.Intn(100) < 30 {
				action := rand.Intn(8)
				switch action {
				case 0:
					// Run across the screen
					m.pet.State = "walking"
					m.pet.Direction = []int{-1, 1}[rand.Intn(2)]
					targetX := rand.Intn(m.width - 2)
					m.pet.TargetPos = pos2D{X: targetX, Y: 0}
					m.pet.HasTarget = true
					m.pet.LastThought = randomThought("walking")
				case 1:
					// Jump in place
					m.pet.State = "jumping"
					m.pet.Pos.Y = 2
					m.pet.LastThought = randomThought("jumping")
				case 2:
					// Chase the yarn
					if m.pet.YarnPos.X >= 0 {
						m.pet.TargetPos = pos2D{X: m.pet.YarnPos.X, Y: 0}
						m.pet.HasTarget = true
						m.pet.ActionPending = "play"
						m.pet.State = "walking"
						m.pet.LastThought = "yarn calls to me."
					}
				case 3:
					// Bat at yarn (toss it)
					tossX := rand.Intn(m.width-4) + 2
					m.pet.YarnPos = pos2D{X: tossX, Y: 2}
					m.pet.TargetPos = pos2D{X: tossX, Y: 0}
					m.pet.HasTarget = true
					m.pet.ActionPending = "play"
					m.pet.State = "walking"
					m.pet.LastThought = "chaos time."
				case 4:
					// Just be happy for a moment
					m.pet.State = "happy"
					m.pet.LastThought = randomThought("happy")
				case 5:
					// SHOOT A BANANA! 🔫🍌
					m.pet.State = "shooting"
					dir := m.pet.Direction
					if dir == 0 {
						dir = 1
					}
					// Add gun emoji at cat position
					m.pet.FloatingItems = append(m.pet.FloatingItems, floatingItem{
						Emoji:     "🔫",
						Pos:       pos2D{X: m.pet.Pos.X + dir, Y: 0},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(800 * time.Millisecond),
					})
					// Add banana flying away
					m.pet.FloatingItems = append(m.pet.FloatingItems, floatingItem{
						Emoji:     "🍌",
						Pos:       pos2D{X: m.pet.Pos.X + dir*2, Y: 1},
						Velocity:  pos2D{X: dir * 2, Y: 0},
						ExpiresAt: now.Add(2 * time.Second),
					})
					thoughts := []string{"pew pew.", "banana had it coming.", "nothing personal.", "the family sends regards."}
					m.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				case 6:
					// Toss random emoji
					emojis := []string{"⭐", "💫", "✨", "🎾", "🏀", "🎈", "🦋", "🐟", "🍎", "🧀"}
					emoji := emojis[rand.Intn(len(emojis))]
					startX := rand.Intn(m.width - 4) + 2
					dir := []int{-1, 1}[rand.Intn(2)]
					m.pet.FloatingItems = append(m.pet.FloatingItems, floatingItem{
						Emoji:     emoji,
						Pos:       pos2D{X: startX, Y: 2},
						Velocity:  pos2D{X: dir, Y: 0},
						ExpiresAt: now.Add(3 * time.Second),
					})
					m.pet.LastThought = "ooh shiny."
				case 7:
					// Menacing stare with floating emoji
					m.pet.State = "idle"
					emojis := []string{"👁️", "🔪", "💀", "🎯"}
					emoji := emojis[rand.Intn(len(emojis))]
					m.pet.FloatingItems = append(m.pet.FloatingItems, floatingItem{
						Emoji:     emoji,
						Pos:       pos2D{X: m.pet.Pos.X, Y: 2},
						Velocity:  pos2D{X: 0, Y: 0},
						ExpiresAt: now.Add(2 * time.Second),
					})
					thoughts := []string{"watching.", "always watching.", "i see you.", "the family knows."}
					m.pet.LastThought = thoughts[rand.Intn(len(thoughts))]
				}
				savePetState(m.pet)
			}
		}
		return m, catMoodTick()

	case gitTickMsg:
		// Update git status
		if m.config.Widgets.Git.Enabled {
			m.updateGitStatus()
			return m, gitTick(m.config.Widgets.Git.UpdateInterval)
		}
		return m, nil

	case statsTickMsg:
		// Update system stats
		if m.config.Widgets.Stats.Enabled {
			m.updateStats()
			return m, statsTick(m.config.Widgets.Stats.UpdateInterval)
		}
		return m, nil

	case tea.WindowSizeMsg:
		// Update terminal size for dynamic resizing
		m.width = msg.Width
		m.height = msg.Height

		// Calculate scrollable area height (total height minus pinned content)
		pinnedHeight := m.pinnedContentHeight()
		scrollableHeight := msg.Height - pinnedHeight
		if scrollableHeight < 1 {
			scrollableHeight = 1
		}

		// Initialize or update viewport
		if !m.ready {
			m.viewport = viewport.New(msg.Width, scrollableHeight)
			m.viewport.MouseWheelEnabled = true
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = scrollableHeight
		}

		// Update viewport content
		m.viewport.SetContent(m.generateMainContent())
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	t := perf.Start("View")
	defer t.Stop()

	// Log View() calls when TABBY_CLICK_DEBUG=1
	if clickDebugEnabled {
		if f, err := os.OpenFile("/tmp/tabby-click.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			activeIdx := -1
			for _, w := range m.windows {
				if w.Active {
					activeIdx = w.Index
					break
				}
			}
			fmt.Fprintf(f, "%s [%d] VIEW: active=%d ready=%v\n", time.Now().Format("15:04:05.000"), os.Getpid(), activeIdx, m.ready)
			f.Close()
		}
	}

	// Show confirmation dialog if active
	if m.confirmClose && m.confirmWindow != nil {
		confirmStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#e74c3c")).
			Padding(1, 1)
		windowStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#f1c40f"))
		promptStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff"))

		return confirmStyle.Render("Close window?") + "\n\n" +
			windowStyle.Render(fmt.Sprintf("  %d. %s", m.confirmWindow.Index, m.confirmWindow.Name)) + "\n\n" +
			promptStyle.Render("  Press y to confirm, n to cancel")
	}

	// Show collapsed view if sidebar is collapsed
	if m.sidebarCollapsed {
		// Render vertical ">" characters down the sidebar
		expandIcon := ">"
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Bold(true)
		var s string
		for i := 0; i < m.height; i++ {
			s += style.Render(expandIcon) + "\n"
		}
		return s
	}

	// Use viewport for scrollable content with pinned footer
	if m.ready {
		// Update viewport content (in case it changed since last resize)
		m.viewport.SetContent(m.generateMainContent())
		return m.viewport.View() + m.generatePinnedContent()
	}

	// Fallback if viewport not ready yet
	return m.generateMainContent() + m.generatePinnedContent()
}

// syncViewport updates the viewport with the current rendered content
func (m *model) syncViewport() {
	if !m.ready {
		return
	}
	content := m.generateMainContent()
	m.viewport.SetContent(content)
}

// generateMainContent creates the scrollable sidebar content (groups, windows, buttons)
// Excludes pinned widgets which are rendered separately
func (m model) generateMainContent() string {
	var s string

	// Clock widget (top position - not pinned)
	if m.config.Widgets.Clock.Enabled && m.config.Widgets.Clock.Position == "top" {
		s += m.renderClockWidget()
	}

	// Use terminal width if available, otherwise default to 25
	sidebarWidth := m.width
	if sidebarWidth < 20 {
		sidebarWidth = 25 // Default minimum width
	}
	contentWidth := sidebarWidth - 4 // Space for tree chars and arrow

	// Visual position counter (0-n from top to bottom)
	visualPos := 0

	// Iterate over grouped windows - keeps each group together
	for _, group := range m.grouped {
		theme := group.Theme
		isCollapsed := m.isGroupCollapsed(group.Name)

		// Show group header with collapse indicator at start
		headerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Fg)).
			Background(lipgloss.Color(theme.Bg)).
			Bold(true)

		// Collapse indicator: ⊟ expanded, ⊞ collapsed (at start)
		expandedIcon := m.config.Sidebar.Colors.DisclosureExpanded
		if expandedIcon == "" {
			expandedIcon = "⊟"
		}
		collapsedIcon := m.config.Sidebar.Colors.DisclosureCollapsed
		if collapsedIcon == "" {
			collapsedIcon = "⊞"
		}
		collapseIcon := expandedIcon
		if isCollapsed {
			collapseIcon = collapsedIcon
		}
		disclosureColor := m.config.Sidebar.Colors.DisclosureFg
		if disclosureColor == "" {
			disclosureColor = "#000000"
		}
		collapseStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(disclosureColor)).
			Background(lipgloss.Color(theme.Bg))

		icon := theme.Icon
		if icon != "" {
			icon += " "
		}

		// Build header: [collapse] [icon] Name [count if collapsed]
		headerText := icon + group.Name
		if isCollapsed && len(group.Windows) > 0 {
			headerText += fmt.Sprintf(" (%d)", len(group.Windows))
		}

		// Width: 2 for collapse icon + space, rest for content
		headerContentStyle := headerStyle.Width(sidebarWidth - 2)
		s += collapseStyle.Render(collapseIcon+" ") + headerContentStyle.Render(headerText) + "\n"
		s += m.touchDivider(theme.Bg) // Divider under group header in touch mode
		s += m.lineSpacing()

		// Skip windows if group is collapsed
		if isCollapsed {
			s += m.touchGroupEnd() // Space after collapsed group
			continue
		}

		// Tree characters - configurable color
		treeFg := m.config.Sidebar.Colors.TreeFg
		if treeFg == "" {
			treeFg = "#888888"
		}
		treeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(treeFg))

		// Active indicator color (can be "auto" to use window/group bg color)
		activeIndFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg

		// Tree branch characters
		treeBranchChar := m.config.Sidebar.Colors.TreeBranch
		if treeBranchChar == "" {
			treeBranchChar = "├─"
		}
		treeBranchLastChar := m.config.Sidebar.Colors.TreeBranchLast
		if treeBranchLastChar == "" {
			treeBranchLastChar = "└─"
		}
		treeConnectorChar := m.config.Sidebar.Colors.TreeConnector
		if treeConnectorChar == "" {
			treeConnectorChar = "─"
		}
		treeConnectorPanesChar := m.config.Sidebar.Colors.TreeConnectorPanes
		if treeConnectorPanesChar == "" {
			treeConnectorPanesChar = "┬"
		}
		treeContinueChar := m.config.Sidebar.Colors.TreeContinue
		if treeContinueChar == "" {
			treeContinueChar = "│"
		}

		// Show windows in this group
		numWindows := len(group.Windows)
		for wi, win := range group.Windows {
			isActive := win.Active
			isLastInGroup := wi == numWindows-1

			// Choose colors - custom color overrides group theme
			var bgColor, fgColor string
			isTransparent := win.CustomColor == "transparent"
			if isTransparent {
				// Transparent mode: no background, just text color
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

			// Display name is the window name
			displayName := win.Name

			// Build alert indicator (shown at start of tab if any alert)
			alertIcon := ""
			ind := m.config.Indicators

			if ind.Busy.Enabled && win.Busy {
				alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Busy.Color))
				if ind.Busy.Bg != "" {
					alertStyle = alertStyle.Background(lipgloss.Color(ind.Busy.Bg))
				}
				busyFrames := m.getBusyFrames()
				alertIcon = alertStyle.Render(busyFrames[m.spinnerFrame%len(busyFrames)])
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
					alertIcon = alertStyle.Render(ind.Input.Frames[m.spinnerFrame%len(ind.Input.Frames)])
				} else {
					alertIcon = alertStyle.Render(inputIcon)
				}
			} else if !isActive {
				if ind.Bell.Enabled && win.Bell {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Bell.Color))
					if ind.Bell.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Bell.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Bell))
				} else if ind.Activity.Enabled && win.Activity {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Activity.Color))
					if ind.Activity.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Activity.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Activity))
				} else if ind.Silence.Enabled && win.Silence {
					alertStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ind.Silence.Color))
					if ind.Silence.Bg != "" {
						alertStyle = alertStyle.Background(lipgloss.Color(ind.Silence.Bg))
					}
					alertIcon = alertStyle.Render(m.getIndicatorIcon(ind.Silence))
				}
			}

			// Build tab content (use visual position, not tmux index)
			baseContent := fmt.Sprintf("%d. %s", visualPos, displayName)
			availableWidth := contentWidth - 2
			if lipgloss.Width(baseContent) > availableWidth {
				truncated := ""
				for _, r := range baseContent {
					if lipgloss.Width(truncated+string(r)) > availableWidth-1 {
						break
					}
					truncated += string(r)
				}
				baseContent = truncated + "~"
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

			// Window line - show collapse indicator if has panes
			hasPanes := len(win.Panes) > 1
			isWindowCollapsed := win.Collapsed
			var windowCollapseIcon string

			windowExpandedIcon := m.config.Sidebar.Colors.DisclosureExpanded
			if windowExpandedIcon == "" {
				windowExpandedIcon = "⊟"
			}
			windowCollapsedIcon := m.config.Sidebar.Colors.DisclosureCollapsed
			if windowCollapsedIcon == "" {
				windowCollapsedIcon = "⊞"
			}

			if hasPanes {
				if isWindowCollapsed {
					windowCollapseIcon = windowCollapsedIcon
				} else {
					windowCollapseIcon = windowExpandedIcon
				}
			}

			// Add pane count to window name if collapsed and has multiple panes
			displayContent := baseContent
			if hasPanes && isWindowCollapsed {
				displayContent = fmt.Sprintf("%s (%d)", baseContent, len(win.Panes))
			}

			// Style for window collapse icon
			disclosureFgColor := m.config.Sidebar.Colors.DisclosureFg
			if disclosureFgColor == "" {
				disclosureFgColor = "#000000"
			}
			windowCollapseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(disclosureFgColor))
			if bgColor != "" {
				windowCollapseStyle = windowCollapseStyle.Background(lipgloss.Color(bgColor))
			}

			// Calculate content width
			prefixWidth := 3 // indicator + tree branch
			if hasPanes {
				prefixWidth += 2 // collapse icon + space
			}
			windowContentWidth := sidebarWidth - prefixWidth

			// Truncate content if needed
			contentText := displayContent
			contentLen := lipgloss.Width(contentText)
			if contentLen > windowContentWidth {
				truncated := ""
				for _, r := range contentText {
					if lipgloss.Width(truncated+string(r)) > windowContentWidth-1 {
						break
					}
					truncated += string(r)
				}
				contentText = truncated + "~"
			}
			contentStyle := style.Width(windowContentWidth)

			// Get active indicator icon and style
			activeIndicator := m.config.Sidebar.Colors.ActiveIndicator
			if activeIndicator == "" {
				activeIndicator = "◀"
			}

			// Determine active indicator color
			var activeIndFg string
			if activeIndFgConfig == "auto" || activeIndFgConfig == "" {
				if bgColor != "" {
					activeIndFg = bgColor
				} else {
					activeIndFg = "#ffffff"
				}
			} else {
				activeIndFg = activeIndFgConfig
			}
			arrowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(activeIndFg)).Bold(true)
			activeIndBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
			if activeIndBgConfig != "" {
				arrowStyle = arrowStyle.Background(lipgloss.Color(activeIndBgConfig))
			}

			// Render tab line
			if hasPanes {
				s += indicatorPart + treeStyle.Render(treeBranch) + windowCollapseStyle.Render(windowCollapseIcon+" ") + contentStyle.Render(contentText) + "\n"
			} else if isActive {
				treeBranchRunes := []rune(treeBranch)
				treeBranchFirst := string(treeBranchRunes[0])

				indicatorBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
				var indicatorBg string
				if indicatorBgConfig == "" || indicatorBgConfig == "auto" {
					if theme.ActiveIndicatorBg != "" {
						indicatorBg = theme.ActiveIndicatorBg
					} else {
						indicatorBg = theme.Bg
					}
				} else {
					indicatorBg = indicatorBgConfig
				}

				indicatorFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg
				var indicatorFg string
				if indicatorFgConfig == "" || indicatorFgConfig == "auto" {
					indicatorFg = indicatorBg
				} else {
					indicatorFg = indicatorFgConfig
				}

				activeIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(indicatorFg)).Background(lipgloss.Color(indicatorBg)).Bold(true)
				s += indicatorPart + treeStyle.Render(treeBranchFirst) + activeIndStyle.Render(activeIndicator) + contentStyle.Render(contentText) + "\n"
			} else {
				s += indicatorPart + treeStyle.Render(treeBranch) + contentStyle.Render(contentText) + "\n"
			}

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

					var paneBranch string
					if isLastPane {
						for _, r := range treeBranchLastChar {
							paneBranch = string(r)
							break
						}
					} else {
						for _, r := range treeBranchChar {
							paneBranch = string(r)
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
					paneContentWidth := sidebarWidth - paneIndentWidth

					if len(paneText) > paneContentWidth {
						paneText = paneText[:paneContentWidth-1] + "~"
					}

					paneActiveIndicator := m.config.Sidebar.Colors.ActiveIndicator
					if paneActiveIndicator == "" {
						paneActiveIndicator = "█"
					}

					if pane.Active && isActive {
						paneIndicatorBgConfig := m.config.Sidebar.Colors.ActiveIndicatorBg
						var paneIndicatorBg string
						if paneIndicatorBgConfig == "" || paneIndicatorBgConfig == "auto" {
							if theme.ActiveIndicatorBg != "" {
								paneIndicatorBg = theme.ActiveIndicatorBg
							} else {
								paneIndicatorBg = theme.Bg
							}
						} else {
							paneIndicatorBg = paneIndicatorBgConfig
						}
						paneIndicatorFgConfig := m.config.Sidebar.Colors.ActiveIndicatorFg
						var paneIndicatorFg string
						if paneIndicatorFgConfig == "" || paneIndicatorFgConfig == "auto" {
							paneIndicatorFg = paneIndicatorBg
						} else {
							paneIndicatorFg = paneIndicatorFgConfig
						}
						paneIndStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(paneIndicatorFg)).Background(lipgloss.Color(paneIndicatorBg)).Bold(true)
						fullWidthPaneStyle := activePaneStyle.Width(paneContentWidth)
						s += " " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar) + paneIndStyle.Render(paneActiveIndicator) + fullWidthPaneStyle.Render(paneText) + "\n"
					} else {
						s += " " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText) + "\n"
					}
				}
			}

			visualPos++
		}
		s += m.touchGroupEnd()
	}

	// Buttons
	if m.config.Sidebar.NewTabButton || m.config.Sidebar.NewGroupButton || m.config.Sidebar.CloseButton {
		s += m.touchDivider("#444444")
		s += "\n"
	}

	if m.config.Sidebar.NewTabButton {
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#27ae60"))
		if m.isTouchMode() {
			s += "\n"
			s += buttonStyle.Render("  [+] New Tab") + "\n"
			s += "\n"
		} else {
			s += buttonStyle.Render("[+] New Tab") + "\n" + m.lineSpacing()
		}
	}

	if m.config.Sidebar.NewGroupButton {
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b59b6"))
		if m.isTouchMode() {
			s += buttonStyle.Render("  [+] New Group") + "\n"
			s += "\n"
		} else {
			s += buttonStyle.Render("[+] New Group") + "\n" + m.lineSpacing()
		}
	}

	if m.config.Sidebar.CloseButton {
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))
		if m.isTouchMode() {
			s += buttonStyle.Render("  [x] Close Tab") + "\n"
			s += "\n"
		} else {
			s += buttonStyle.Render("[x] Close Tab") + "\n" + m.lineSpacing()
		}
	}

	// Non-pinned clock widget at bottom position
	if m.config.Widgets.Clock.Enabled && m.config.Widgets.Clock.Position != "top" && !m.config.Widgets.Clock.Pin {
		s += m.renderClockWidget()
	}

	return s
}

// generatePinnedContent returns content that should be pinned at the bottom (not scrolled)
func (m model) generatePinnedContent() string {
	var s string

	// Pinned pet widget at bottom
	if m.config.Widgets.Pet.Enabled && m.config.Widgets.Pet.Pin {
		s += m.renderPetWidget()
	}

	// Pinned clock widget at bottom
	if m.config.Widgets.Clock.Enabled && m.config.Widgets.Clock.Position != "top" && m.config.Widgets.Clock.Pin {
		s += m.renderClockWidget()
	}

	// Pinned git widget at bottom
	if m.config.Widgets.Git.Enabled && m.config.Widgets.Git.Pin {
		s += m.renderGitWidget()
	}

	// Pinned session widget at bottom
	if m.config.Widgets.Session.Enabled && m.config.Widgets.Session.Pin {
		s += m.renderSessionWidget()
	}

	// Pinned stats widget at bottom
	if m.config.Widgets.Stats.Enabled && m.config.Widgets.Stats.Pin {
		s += m.renderStatsWidget()
	}

	return s
}

// pinnedContentHeight returns the number of lines needed for pinned content
func (m model) pinnedContentHeight() int {
	height := 0

	// Pet widget height
	if m.config.Widgets.Pet.Enabled && m.config.Widgets.Pet.Pin {
		pet := m.config.Widgets.Pet

		// Margin top
		height += pet.MarginTop

		// Divider (always present, default "─")
		height++

		// Padding top
		height += pet.PaddingTop

		// Core content: thought + high air + low air + ground + items bar
		// Narrow (< 18 width) adds extra line for stacked bars
		if m.width < 18 {
			height += 6 // thought + high air + low air + ground + icons + bars
		} else {
			height += 5 // thought + high air + low air + ground + items
		}

		// Padding bottom
		height += pet.PaddingBot

		// Bottom divider (if configured)
		if pet.DividerBottom != "" {
			height++
		}

		// Margin bottom
		height += pet.MarginBot
	}

	// Clock widget height
	if m.config.Widgets.Clock.Enabled && m.config.Widgets.Clock.Position != "top" && m.config.Widgets.Clock.Pin {
		clock := m.config.Widgets.Clock

		// Margin top
		height += clock.MarginTop

		// Divider line
		if clock.Divider != "" {
			height++
		}

		// Padding top
		height += clock.PaddingTop

		// Time line
		height++

		// Date line (if enabled)
		if clock.ShowDate {
			height++
		}

		// Padding bottom
		height += clock.PaddingBot

		// Bottom divider
		if clock.DividerBottom != "" {
			height++
		}

		// Margin bottom
		height += clock.MarginBot
	}

	// Git widget height
	if m.config.Widgets.Git.Enabled && m.config.Widgets.Git.Pin {
		height += m.gitWidgetHeight()
	}

	// Session widget height
	if m.config.Widgets.Session.Enabled && m.config.Widgets.Session.Pin {
		height += m.sessionWidgetHeight()
	}

	// Stats widget height
	if m.config.Widgets.Stats.Enabled && m.config.Widgets.Stats.Pin {
		height += m.statsWidgetHeight()
	}

	return height
}

// renderClockWidget renders the clock/date widget
func (m model) renderClockWidget() string {
	clock := m.config.Widgets.Clock
	now := time.Now()

	// Default format (with seconds)
	timeFormat := clock.Format
	if timeFormat == "" {
		timeFormat = "15:04:05"
	}

	// Build style
	fg := clock.Fg
	if fg == "" {
		fg = "#888888"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))
	if clock.Bg != "" {
		style = style.Background(lipgloss.Color(clock.Bg))
	}

	// Divider style
	dividerFg := clock.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))

	var result string

	// Margin top
	for i := 0; i < clock.MarginTop; i++ {
		result += "\n"
	}

	// Divider (top of widget)
	if clock.Divider != "" {
		// Use lipgloss.Width for proper Unicode width calculation
		dividerWidth := lipgloss.Width(clock.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.Divider, m.width/dividerWidth)
		result += dividerStyle.Render(dividerLine) + "\n"
	}

	// Padding top
	for i := 0; i < clock.PaddingTop; i++ {
		result += "\n"
	}

	// Render time centered
	timeStr := now.Format(timeFormat)
	timePadding := (m.width - len(timeStr)) / 2
	if timePadding < 0 {
		timePadding = 0
	}
	result += style.Render(strings.Repeat(" ", timePadding) + timeStr) + "\n"

	// Optionally show date
	if clock.ShowDate {
		dateFormat := clock.DateFmt
		if dateFormat == "" {
			dateFormat = "Mon Jan 2"
		}
		dateStr := now.Format(dateFormat)
		datePadding := (m.width - len(dateStr)) / 2
		if datePadding < 0 {
			datePadding = 0
		}
		result += style.Render(strings.Repeat(" ", datePadding) + dateStr) + "\n"
	}

	// Padding bottom
	for i := 0; i < clock.PaddingBot; i++ {
		result += "\n"
	}

	// Bottom divider
	if clock.DividerBottom != "" {
		dividerWidth := lipgloss.Width(clock.DividerBottom)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.DividerBottom, m.width/dividerWidth)
		result += dividerStyle.Render(dividerLine) + "\n"
	}

	// Margin bottom
	for i := 0; i < clock.MarginBot; i++ {
		result += "\n"
	}

	return result
}

// renderPetWidget renders the pet tamagotchi widget
// Layout (5 lines):
//   Line 1: Thought bubble
//   Line 2: High air (Y=2)
//   Line 3: Low air (Y=1)
//   Line 4: Ground (Y=0) - pet, yarn, poops
//   Line 5: Items bar - food, toys, actions
func (m model) renderPetWidget() string {
	petCfg := m.config.Widgets.Pet
	if !petCfg.Enabled {
		return ""
	}

	// Get sprite set for current style
	style := petCfg.Style
	if style == "" {
		style = "emoji"
	}
	sprites, ok := petSpritesByStyle[style]
	if !ok {
		sprites = petSpritesByStyle["emoji"]
	}

	// Determine pet sprite based on state
	petSprite := sprites.Idle
	switch m.pet.State {
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
	}
	if m.pet.Hunger < 30 {
		petSprite = sprites.Hungry
	}
	if petSprite == "" {
		petSprite = "🐱"
	}

	var result string

	// Margin top
	for i := 0; i < petCfg.MarginTop; i++ {
		result += "\n"
	}

	// Divider above (configurable)
	divider := petCfg.Divider
	if divider == "" {
		divider = "─" // default
	}
	dividerFg := petCfg.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))
	dividerWidth := runewidth.StringWidth(divider)
	if dividerWidth > 0 {
		result += dividerStyle.Render(strings.Repeat(divider, m.width/dividerWidth)) + "\n"
	}

	// Padding top
	for i := 0; i < petCfg.PaddingTop; i++ {
		result += "\n"
	}

	// Play area width - use actual width, minimum 5
	playWidth := m.width
	if playWidth < 5 {
		playWidth = 5
	}

	// Get yarn position, default to near right edge, clamp to width
	yarnX := m.pet.YarnPos.X
	if yarnX < 0 {
		yarnX = playWidth - 4
	}
	if yarnX >= playWidth {
		yarnX = playWidth - 1
	}
	yarnY := m.pet.YarnPos.Y

	// Pet position, clamp to width
	petX := m.pet.Pos.X
	if petX < 0 {
		petX = playWidth / 2
	}
	if petX >= playWidth {
		petX = playWidth - 1
	}
	petY := m.pet.Pos.Y
	petRunes := []rune(petSprite)

	// Line 1: Thought bubble (marquee if too wide)
	thought := m.pet.LastThought
	if thought == "" {
		thought = "chillin'."
	}
	thoughtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	// Max width for thought text (💭 takes 2 cols + 1 space = 3)
	maxThoughtWidth := playWidth - 4
	if maxThoughtWidth < 5 {
		maxThoughtWidth = 5
	}
	thoughtWidth := runewidth.StringWidth(thought)
	displayThought := thought
	if thoughtWidth > maxThoughtWidth {
		// Marquee effect - create scrolling view
		// Add spaces for smooth wrap-around
		scrollText := thought + "   " + thought
		scrollRunes := []rune(scrollText)
		// Find start position based on scroll offset
		startIdx := 0
		accWidth := 0
		for i, r := range scrollRunes {
			if accWidth >= m.pet.ThoughtScroll {
				startIdx = i
				break
			}
			accWidth += runewidth.RuneWidth(r)
		}
		// Extract visible portion
		visible := ""
		visWidth := 0
		for i := startIdx; i < len(scrollRunes) && visWidth < maxThoughtWidth; i++ {
			r := scrollRunes[i]
			rw := runewidth.RuneWidth(r)
			if visWidth+rw > maxThoughtWidth {
				break
			}
			visible += string(r)
			visWidth += rw
		}
		displayThought = visible
	}
	thoughtLine := "💭 " + displayThought
	result += thoughtStyle.Render(thoughtLine) + "\n"

	// Food position, clamp to width
	foodX := m.pet.FoodItem.X
	if foodX >= playWidth {
		foodX = playWidth - 1
	}
	foodY := m.pet.FoodItem.Y
	foodRunes := []rune(sprites.Food)

	// Line 2: High air (Y=2)
	highAir := make([]rune, playWidth)
	for i := range highAir {
		highAir[i] = ' '
	}
	// Place floating items at Y=2
	for _, item := range m.pet.FloatingItems {
		if item.Pos.Y == 2 && item.Pos.X >= 0 && item.Pos.X < playWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				highAir[item.Pos.X] = itemRunes[0]
			}
		}
	}
	if petY >= 2 && petX < playWidth && len(petRunes) > 0 {
		highAir[petX] = petRunes[0]
	}
	if yarnY >= 2 && yarnX >= 0 && yarnX < playWidth {
		yarnRunes := []rune(sprites.Yarn)
		if len(yarnRunes) > 0 {
			highAir[yarnX] = yarnRunes[0]
		}
	}
	if foodY >= 2 && foodX >= 0 && foodX < playWidth && len(foodRunes) > 0 {
		highAir[foodX] = foodRunes[0]
	}
	result += string(highAir) + "\n"

	// Line 3: Low air (Y=1)
	lowAir := make([]rune, playWidth)
	for i := range lowAir {
		lowAir[i] = ' '
	}
	// Place floating items at Y=1
	for _, item := range m.pet.FloatingItems {
		if item.Pos.Y == 1 && item.Pos.X >= 0 && item.Pos.X < playWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				lowAir[item.Pos.X] = itemRunes[0]
			}
		}
	}
	if petY == 1 && petX < playWidth && len(petRunes) > 0 {
		lowAir[petX] = petRunes[0]
	}
	if yarnY == 1 && yarnX >= 0 && yarnX < playWidth {
		yarnRunes := []rune(sprites.Yarn)
		if len(yarnRunes) > 0 {
			lowAir[yarnX] = yarnRunes[0]
		}
	}
	if foodY == 1 && foodX >= 0 && foodX < playWidth && len(foodRunes) > 0 {
		lowAir[foodX] = foodRunes[0]
	}
	result += string(lowAir) + "\n"

	// Line 4: Ground (Y=0)
	groundRow := make([]rune, playWidth)
	for i := range groundRow {
		groundRow[i] = '·'
	}

	// Place floating items at Y=0
	for _, item := range m.pet.FloatingItems {
		if item.Pos.Y == 0 && item.Pos.X >= 0 && item.Pos.X < playWidth {
			itemRunes := []rune(item.Emoji)
			if len(itemRunes) > 0 {
				groundRow[item.Pos.X] = itemRunes[0]
			}
		}
	}

	// Place yarn on ground if Y=0
	if yarnY == 0 && yarnX >= 0 && yarnX < playWidth {
		yarnRunes := []rune(sprites.Yarn)
		if len(yarnRunes) > 0 {
			groundRow[yarnX] = yarnRunes[0]
		}
	}

	// Place food on ground if Y=0
	if foodY == 0 && foodX >= 0 && foodX < playWidth && len(foodRunes) > 0 {
		groundRow[foodX] = foodRunes[0]
	}

	// Place poops
	poopRunes := []rune(sprites.Poop)
	for _, poopX := range m.pet.PoopPositions {
		if poopX >= 0 && poopX < playWidth && len(poopRunes) > 0 {
			groundRow[poopX] = poopRunes[0]
		}
	}

	// Place pet on ground if Y=0
	if petY == 0 && petX < playWidth && len(petRunes) > 0 {
		groundRow[petX] = petRunes[0]
	}
	result += string(groundRow) + "\n"

	// Line 5: Items bar - just clickable icons (no pressure bars)
	itemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	itemsLine := fmt.Sprintf("%s  %s", sprites.Food, sprites.Yarn)
	result += itemStyle.Render(itemsLine) + "\n"

	// Padding bottom
	for i := 0; i < petCfg.PaddingBot; i++ {
		result += "\n"
	}

	// Divider below (if configured)
	if petCfg.DividerBottom != "" {
		dividerBottomWidth := runewidth.StringWidth(petCfg.DividerBottom)
		if dividerBottomWidth > 0 {
			result += dividerStyle.Render(strings.Repeat(petCfg.DividerBottom, m.width/dividerBottomWidth)) + "\n"
		}
	}

	// Margin bottom
	for i := 0; i < petCfg.MarginBot; i++ {
		result += "\n"
	}

	return result
}

func (m model) showContextMenu(win *tmux.Window) {
	// Build menu arguments dynamically
	// -O keeps menu open when mouse exits (allows click to select)
	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Window %d: %s", win.Index, win.Name),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - simple rename without prefix manipulation
	// Group assignment is now handled by @tabby_group option, not window name prefixes
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"rename-window -t :%d -- '%%%%' ; set-window-option -t :%d automatic-rename off\"", win.Name, win.Index, win.Index)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option (restore automatic naming)
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

	// Move to Group submenu - sets @tabby_group window option
	args = append(args, "-Move to Group", "", "")
	keyNum := 1
	for _, group := range m.config.Groups {
		if group.Name == "Default" {
			continue // Skip default group in the submenu (use "Remove from Group" instead)
		}
		key := fmt.Sprintf("%d", keyNum)
		keyNum++
		if keyNum <= 10 {
			// Set the @tabby_group window option
			setGroupCmd := fmt.Sprintf("set-window-option -t :%d @tabby_group '%s'", win.Index, group.Name)
			args = append(args, fmt.Sprintf("  %s %s", group.Theme.Icon, group.Name), key, setGroupCmd)
		}
	}

	// Option to remove from group (move to Default) - unsets @tabby_group
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
	// Reset option
	resetColorCmd := fmt.Sprintf("set-window-option -t :%d -u @tabby_color", win.Index)
	args = append(args, "  Reset to Default", "d", resetColorCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options - target pane 1 (sidebar is pane 0, main content is pane 1+)
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -h -c '#{pane_current_path}'", win.Index, win.Index)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t :%d.1 ; split-window -v -c '#{pane_current_path}'", win.Index, win.Index)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Open in Finder - opens the pane's current directory
	openFinderCmd := fmt.Sprintf("run-shell 'open \"#{pane_current_path}\"'")
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Kill option - uses helper script to avoid complex quoting issues
	killCmd := fmt.Sprintf("run-shell '%s/scripts/kill_window.sh %d'", getCurrentDir(), win.Index)
	args = append(args, "Kill", "k", killCmd)

	_ = exec.Command("tmux", args...).Run()
}

func (m model) showPaneContextMenu(pr *paneRef) {
	// Use locked title, then title, then command for display
	paneLabel := pr.pane.Command
	if pr.pane.LockedTitle != "" {
		paneLabel = pr.pane.LockedTitle
	} else if pr.pane.Title != "" && pr.pane.Title != pr.pane.Command {
		paneLabel = pr.pane.Title
	}

	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Pane %d.%d: %s", pr.windowIdx, pr.pane.Index, paneLabel),
		"-x", "M",
		"-y", "M",
	}

	// Rename option - sets @tabby_pane_title to lock the name
	currentTitle := pr.pane.LockedTitle
	if currentTitle == "" {
		currentTitle = pr.pane.Title
	}
	if currentTitle == "" {
		currentTitle = pr.pane.Command
	}
	// Use helper script to handle the rename and lock
	renameCmd := fmt.Sprintf("command-prompt -I '%s' \"run-shell '%s/scripts/rename_pane.sh %s \\\"%%%%\\\"'\"", currentTitle, getCurrentDir(), pr.pane.ID)
	args = append(args, "Rename", "r", renameCmd)

	// Unlock name option (clear locked title, show command instead)
	unlockCmd := fmt.Sprintf("set-option -p -t %s -u @tabby_pane_title ; select-pane -t %s -T ''", pr.pane.ID, pr.pane.ID)
	args = append(args, "Unlock Name", "u", unlockCmd)

	// Separator
	args = append(args, "", "", "")

	// Split options for this pane
	splitHCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -h -c '#{pane_current_path}'", pr.windowIdx, pr.pane.ID)
	splitVCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s ; split-window -v -c '#{pane_current_path}'", pr.windowIdx, pr.pane.ID)
	args = append(args, "Split Horizontal │", "|", splitHCmd)
	args = append(args, "Split Vertical ─", "-", splitVCmd)

	// Separator
	args = append(args, "", "", "")

	// Focus this pane
	focusCmd := fmt.Sprintf("select-window -t :%d ; select-pane -t %s", pr.windowIdx, pr.pane.ID)
	args = append(args, "Focus", "f", focusCmd)

	// Break pane to new window
	breakCmd := fmt.Sprintf("break-pane -s %s", pr.pane.ID)
	args = append(args, "Break to New Window", "b", breakCmd)

	// Open in Finder - opens this pane's current directory
	openFinderCmd := fmt.Sprintf("run-shell 'open \"#(tmux display-message -t %s -p \"#{pane_current_path}\")\"'", pr.pane.ID)
	args = append(args, "Open in Finder", "o", openFinderCmd)

	// Separator
	args = append(args, "", "", "")

	// Close pane
	args = append(args, "Close Pane", "x", fmt.Sprintf("kill-pane -t %s", pr.pane.ID))

	_ = exec.Command("tmux", args...).Run()
}

// showAlertPopup shows recent output from a window with an alert indicator
func (m model) showAlertPopup(win *tmux.Window) {
	// Determine alert type for title
	alertType := "Activity"
	if win.Bell {
		alertType = "Bell"
	} else if win.Busy {
		alertType = "Busy"
	}

	// Use tmux popup to show recent output captured from the window's main pane
	// Find the non-sidebar pane in the target window
	// Use less with -R for colors, ESC or q to quit
	popupCmd := fmt.Sprintf(`
		target_pane=$(tmux list-panes -t :%d -F '#{pane_id}:#{pane_current_command}' | grep -v ':sidebar$' | head -1 | cut -d: -f1)
		if [ -n "$target_pane" ]; then
			tmux capture-pane -t "$target_pane" -p -e -S -50 > /tmp/tabby-alert-$$.txt
			tmux display-popup -w 80 -h 25 -T " %s: %s (ESC/q to close) " -E "less -R +G /tmp/tabby-alert-$$.txt; rm -f /tmp/tabby-alert-$$.txt"
		fi
	`, win.Index, alertType, win.Name)

	_ = exec.Command("bash", "-c", popupCmd).Run()
}

// showGroupContextMenu shows a context menu for a group header
func (m model) showGroupContextMenu(gr *groupRef) {
	args := []string{
		"display-menu",
		"-O",
		"-T", fmt.Sprintf("Group: %s (%d windows)", gr.group.Name, len(gr.group.Windows)),
		"-x", "M",
		"-y", "M",
	}

	// Build list of window indices in this group
	var indices []string
	for _, win := range gr.group.Windows {
		indices = append(indices, fmt.Sprintf("%d", win.Index))
	}
	indicesStr := strings.Join(indices, " ")

	// Add new window in this group - set @tabby_group option and use working_dir if configured
	var workingDir string
	for _, cfgGroup := range m.config.Groups {
		if cfgGroup.Name == gr.group.Name && cfgGroup.WorkingDir != "" {
			workingDir = cfgGroup.WorkingDir
			break
		}
	}

	// Use configured working_dir, or fall back to current pane's path
	dirArg := "'#{pane_current_path}'"
	if workingDir != "" {
		dirArg = fmt.Sprintf("'%s'", workingDir)
	}

	if gr.group.Name != "Default" {
		newWindowCmd := fmt.Sprintf("new-window -c %s ; set-window-option @tabby_group '%s'", dirArg, gr.group.Name)
		args = append(args, fmt.Sprintf("New %s Window", gr.group.Name), "n", newWindowCmd)
	} else {
		newWindowCmd := fmt.Sprintf("new-window -c %s", dirArg)
		args = append(args, "New Window", "n", newWindowCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Collapse/Expand option
	if m.isGroupCollapsed(gr.group.Name) {
		expandCmd := fmt.Sprintf("run-shell '%s/scripts/toggle_group_collapse.sh \"%s\" expand'", getCurrentDir(), gr.group.Name)
		args = append(args, "Expand Group", "e", expandCmd)
	} else {
		collapseCmd := fmt.Sprintf("run-shell '%s/scripts/toggle_group_collapse.sh \"%s\" collapse'", getCurrentDir(), gr.group.Name)
		args = append(args, "Collapse Group", "c", collapseCmd)
	}

	// Only show Edit/Delete for non-Default groups
	if gr.group.Name != "Default" {
		// Separator
		args = append(args, "", "", "")

		// Edit Group submenu
		args = append(args, "-Edit Group", "", "")

		// Rename
		renameCmd := fmt.Sprintf("command-prompt -I '%s' -p 'New name:' \"run-shell '%s/scripts/rename_group.sh %s %%%%'\"",
			gr.group.Name, getCurrentDir(), gr.group.Name)
		args = append(args, "  Rename", "r", renameCmd)

		// Change Color submenu
		args = append(args, "  -Change Color", "", "")
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
			setColorCmd := fmt.Sprintf("run-shell '%s/scripts/set_group_color.sh \"%s\" \"%s\"'",
				getCurrentDir(), gr.group.Name, color.hex)
			args = append(args, fmt.Sprintf("    %s", color.name), color.key, setColorCmd)
		}

		// Set Working Directory option
		currentWorkingDir := workingDir
		if currentWorkingDir == "" {
			currentWorkingDir = "~"
		}
		setWorkingDirCmd := fmt.Sprintf("command-prompt -I '%s' -p 'Working directory:' \"run-shell '%s/scripts/set_group_working_dir.sh \\\"%s\\\" \\\"%%%%\\\"'\"",
			currentWorkingDir, getCurrentDir(), gr.group.Name)
		args = append(args, "  Set Working Directory", "w", setWorkingDirCmd)

		// Separator before Delete
		args = append(args, "", "", "")

		// Delete Group
		deleteCmd := fmt.Sprintf("confirm-before -p 'Delete group %s? (y/n)' \"run-shell '%s/scripts/delete_group.sh %s'\"",
			gr.group.Name, getCurrentDir(), gr.group.Name)
		args = append(args, "Delete Group", "d", deleteCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Close all windows in this group - pass window indices directly
	if len(indices) > 0 {
		closeAllCmd := fmt.Sprintf("run-shell '%s/scripts/kill_windows.sh %s'", getCurrentDir(), indicesStr)
		args = append(args, "Close All Windows", "x", closeAllCmd)
	}

	_ = exec.Command("tmux", args...).Run()
}

// showNewGroupPrompt shows a prompt to create a new group
func (m model) showNewGroupPrompt() {
	// Use tmux command-prompt to get the group name
	// The script will add the group to config.yaml and trigger a config reload
	scriptPath := getCurrentDir() + "/scripts/new_group.sh"
	promptCmd := fmt.Sprintf("command-prompt -p 'New group name:' \"run-shell '%s %%%%'\"", scriptPath)
	_ = exec.Command("tmux", "run-shell", "-b", fmt.Sprintf("tmux %s", promptCmd)).Run()
}

// extractGroupPrefix extracts the window name prefix from a regex pattern
// e.g., "^SD\\|" -> "SD|", "^GP\\|" -> "GP|"
func extractGroupPrefix(pattern string) string {
	if len(pattern) < 2 {
		return ""
	}
	// Remove leading ^ if present
	if pattern[0] == '^' {
		pattern = pattern[1:]
	}
	// Unescape common patterns
	pattern = strings.ReplaceAll(pattern, "\\|", "|")
	pattern = strings.ReplaceAll(pattern, "\\.", ".")
	// If it still has regex chars, it's not a simple prefix
	if strings.ContainsAny(pattern, ".*+?[](){}$") {
		return ""
	}
	return pattern
}

// findWindowGroup returns the group name and theme for a window based on @tabby_group option
func findWindowGroup(win *tmux.Window, groups []config.Group) (string, config.Theme) {
	var defaultTheme config.Theme
	defaultName := "Default"

	// Get group name from window option (set via @tabby_group)
	groupName := win.Group
	if groupName == "" {
		groupName = "Default"
	}

	// Find the matching group config
	for _, group := range groups {
		if group.Name == "Default" {
			defaultTheme = group.Theme
		}
		if group.Name == groupName {
			return group.Name, group.Theme
		}
	}

	// Group not found in config, fall back to Default
	if defaultTheme.Bg != "" {
		return defaultName, defaultTheme
	}
	return defaultName, config.Theme{
		Bg:       "#3498db",
		Fg:       "#ffffff",
		ActiveBg: "#2980b9",
		ActiveFg: "#ffffff",
	}
}



func watchConfig(p *tea.Program, configPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	_ = watcher.Add(configPath)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Write == fsnotify.Write {
					p.Send(reloadConfigMsg{})
				}
			case <-watcher.Errors:
				return
			}
		}
	}()
}

// updatePaneHeaderColors sets pane header colors on each window for pane-border-format
func updatePaneHeaderColors(grouped []grouping.GroupedWindows) {
	// Run async to avoid blocking UI - pane colors can update slightly after
	go func() {
		// Build a single batched tmux command for all windows
		var args []string
		for _, group := range grouped {
			baseColor := group.Theme.Bg
			for _, win := range group.Windows {
				color := baseColor
				if win.CustomColor != "" {
					color = win.CustomColor
				}
				inactive := grouping.LightenColor(color, 0.15)
				// Chain commands with semicolons
				if len(args) > 0 {
					args = append(args, ";")
				}
				args = append(args, "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_active", color)
				args = append(args, ";", "set-window-option", "-t", fmt.Sprintf(":%d", win.Index), "@tabby_pane_inactive", inactive)
			}
		}
		if len(args) > 0 {
			exec.Command("tmux", args...).Run()
		}
	}()
}

func main() {
	// Force ANSI256 color mode to avoid partial 24-bit escape code issues
	lipgloss.SetColorProfile(termenv.ANSI256)

	cfg, _ := config.LoadConfig(config.DefaultConfigPath())

	// Initialize debug logging based on config
	initDebugLog(cfg.Sidebar.Debug)
	debug("=== Sidebar starting ===")
	windows, _ := tmux.ListWindowsWithPanes()
	grouped := grouping.GroupWindowsWithOptions(windows, cfg.Groups, cfg.Sidebar.ShowEmptyGroups)

	// Set initial pane header colors
	updatePaneHeaderColors(grouped)

	m := model{
		windows:         windows,
		grouped:         grouped,
		config:          cfg,
		collapsedGroups: loadCollapsedGroups(),
		pet:             loadPetState(), // Load shared pet state
	}
	m.buildWindowRefs()

	// Set initial cursor to first window
	if len(m.windowRefs) > 0 {
		m.cursor = m.windowRefs[0].startLine
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	watchConfig(p, config.DefaultConfigPath())

	go func() {
		for range sigChan {
			p.Send(refreshMsg{})
		}
	}()

	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
