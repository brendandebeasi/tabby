package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/daemon"
	"github.com/b/tmux-tabs/pkg/grouping"
	"github.com/b/tmux-tabs/pkg/tmux"
)

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

	// State locks
	stateMu sync.RWMutex

	// Session info
	sessionID string
}

// petState holds the current state of the pet widget
type petState struct {
	Pos           pos2D
	State         string
	Direction     int
	Hunger        int
	Happiness     int
	YarnPos       pos2D
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

// Pet sprites by style
type petSprites struct {
	Idle, Walking, Jumping, Playing  string
	Eating, Sleeping, Happy, Hungry  string
	Yarn, Food, Poop                 string
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

// loadCollapsedGroups loads collapsed state from tmux option
func (c *Coordinator) loadCollapsedGroups() {
	out, err := exec.Command("tmux", "show-options", "-v", "-q", "@tabby_collapsed_groups").Output()
	if err != nil || len(out) == 0 {
		return
	}
	var groups []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &groups); err != nil {
		return
	}
	for _, g := range groups {
		c.collapsedGroups[g] = true
	}
}

// saveCollapsedGroups saves collapsed state to tmux option
func (c *Coordinator) saveCollapsedGroups() {
	var groups []string
	for name, isCollapsed := range c.collapsedGroups {
		if isCollapsed {
			groups = append(groups, name)
		}
	}
	if len(groups) == 0 {
		exec.Command("tmux", "set-option", "-u", "@tabby_collapsed_groups").Run()
		return
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return
	}
	exec.Command("tmux", "set-option", "@tabby_collapsed_groups", string(data)).Run()
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

	windows, err := tmux.ListWindowsWithPanes()
	if err != nil {
		return
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

	// Reload shared state periodically
	if c.pet.AnimFrame%5 == 0 {
		c.loadPetState()
	}

	c.pet.AnimFrame++

	// Decrease hunger over time
	if c.pet.Hunger > 0 && c.pet.AnimFrame%30 == 0 {
		c.pet.Hunger--
	}

	// Decrease happiness if hungry
	if c.pet.Hunger < 30 && c.pet.Happiness > 0 && c.pet.AnimFrame%20 == 0 {
		c.pet.Happiness--
	}

	// Thought marquee - scroll every 3 frames
	if c.pet.AnimFrame%3 == 0 {
		c.pet.ThoughtScroll++
	}

	c.savePetState()
}

// RenderForClient generates content for a specific client's dimensions
func (c *Coordinator) RenderForClient(clientID string, width, height int) *daemon.RenderPayload {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()

	// Guard dimensions
	if width < 10 {
		width = 25
	}
	if height < 5 {
		height = 24
	}

	// Generate main content (window/pane list) with clickable regions
	// Pass clientID so we can show this client's window as active
	mainContent, regions := c.generateMainContent(clientID, width, height)

	// Generate pinned content (widgets at bottom)
	pinnedContent := c.generatePinnedContent(width)
	pinnedHeight := strings.Count(pinnedContent, "\n")
	if pinnedContent != "" && !strings.HasSuffix(pinnedContent, "\n") {
		pinnedHeight++
	}

	// Count total lines for scroll calculation
	totalLines := strings.Count(mainContent, "\n")

	return &daemon.RenderPayload{
		Content:       mainContent,
		PinnedContent: pinnedContent,
		Width:         width,
		Height:        height,
		TotalLines:    totalLines,
		PinnedHeight:  pinnedHeight,
		Regions:       regions,
	}
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
			headerContentStyle := headerStyle.Width(width - 2)
			s.WriteString(collapseStyle.Render(collapseIcon+" ") + headerContentStyle.Render(headerText) + "\n")
		} else {
			// No windows - just show header without collapse icon
			headerContentStyle := headerStyle.Width(width)
			s.WriteString(headerContentStyle.Render("  "+headerText) + "\n")
		}
		currentLine++

		// Touch divider (skip for last group)
		if !isLastGroup {
			touchDiv := c.touchDivider(width, theme.Bg)
			if touchDiv != "" {
				s.WriteString(touchDiv)
				currentLine++
			}

			// Line spacing
			spacing := c.lineSpacing()
			if spacing != "" {
				s.WriteString(spacing)
				currentLine += strings.Count(spacing, "\n")
			}
		}

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

					if len(paneText) > paneContentWidth {
						paneText = paneText[:paneContentWidth-1] + "~"
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
	}

	// Buttons
	if c.config.Sidebar.NewTabButton || c.config.Sidebar.NewGroupButton || c.config.Sidebar.CloseButton {
		touchDiv := c.touchDivider(width, "#444444")
		if touchDiv != "" {
			s.WriteString(touchDiv)
			currentLine++
		}
		s.WriteString("\n")
		currentLine++
	}

	if c.config.Sidebar.NewTabButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#27ae60"))
		if c.isTouchMode(width) {
			s.WriteString("\n")
			currentLine++
			s.WriteString(buttonStyle.Render("  [+] New Tab") + "\n")
			currentLine++
			s.WriteString("\n")
			currentLine++
		} else {
			s.WriteString(buttonStyle.Render("[+] New Tab") + "\n")
			currentLine++
			spacing := c.lineSpacing()
			if spacing != "" {
				s.WriteString(spacing)
				currentLine += strings.Count(spacing, "\n")
			}
		}
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "new_tab",
		})
	}

	if c.config.Sidebar.NewGroupButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#9b59b6"))
		if c.isTouchMode(width) {
			s.WriteString(buttonStyle.Render("  [+] New Group") + "\n")
			currentLine++
			s.WriteString("\n")
			currentLine++
		} else {
			s.WriteString(buttonStyle.Render("[+] New Group") + "\n")
			currentLine++
			spacing := c.lineSpacing()
			if spacing != "" {
				s.WriteString(spacing)
				currentLine += strings.Count(spacing, "\n")
			}
		}
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "new_group",
		})
	}

	if c.config.Sidebar.CloseButton {
		buttonStartLine := currentLine
		buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c"))
		if c.isTouchMode(width) {
			s.WriteString(buttonStyle.Render("  [x] Close Tab") + "\n")
			currentLine++
			s.WriteString("\n")
			currentLine++
		} else {
			s.WriteString(buttonStyle.Render("[x] Close Tab") + "\n")
			currentLine++
			spacing := c.lineSpacing()
			if spacing != "" {
				s.WriteString(spacing)
				currentLine += strings.Count(spacing, "\n")
			}
		}
		regions = append(regions, daemon.ClickableRegion{
			StartLine: buttonStartLine,
			EndLine:   currentLine - 1,
			Action:    "button",
			Target:    "close_tab",
		})
	}

	// Non-pinned clock widget at bottom position
	if c.config.Widgets.Clock.Enabled && c.config.Widgets.Clock.Position != "top" && !c.config.Widgets.Clock.Pin {
		s.WriteString(c.renderClockWidget(width))
	}

	return s.String(), regions
}

// generatePinnedContent creates the pinned widgets at bottom
func (c *Coordinator) generatePinnedContent(width int) string {
	var s strings.Builder

	// Pinned pet widget at bottom
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin {
		s.WriteString(c.renderPetWidget(width))
	}

	// Pinned clock widget at bottom
	if c.config.Widgets.Clock.Enabled && c.config.Widgets.Clock.Position != "top" && c.config.Widgets.Clock.Pin {
		s.WriteString(c.renderClockWidget(width))
	}

	// Pinned git widget at bottom
	if c.config.Widgets.Git.Enabled && c.config.Widgets.Git.Pin {
		s.WriteString(c.renderGitWidget(width))
	}

	// Pinned session widget at bottom
	if c.config.Widgets.Session.Enabled && c.config.Widgets.Session.Pin {
		s.WriteString(c.renderSessionWidget(width))
	}

	return s.String()
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
	maxBranch := width - 8
	if len(branch) > maxBranch {
		branch = branch[:maxBranch-1] + "~"
	}

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

	result.WriteString(style.Render(fmt.Sprintf(" %s %s%s", icon, branch, status)) + "\n")

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

	if icons.Session != "" {
		parts = append(parts, sessionStyle.Render(icons.Session+" "+c.sessionName))
	} else {
		parts = append(parts, sessionStyle.Render(c.sessionName))
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

// renderPetWidget renders the pet tamagotchi widget
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

	petSprite := sprites.Idle
	switch c.pet.State {
	case "walking":
		petSprite = sprites.Walking
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
	if c.pet.Hunger < 30 {
		petSprite = sprites.Hungry
	}
	if petSprite == "" {
		petSprite = "🐱"
	}

	var result strings.Builder

	for i := 0; i < petCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	divider := petCfg.Divider
	if divider == "" {
		divider = "─"
	}
	dividerFg := petCfg.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))
	dividerWidth := runewidth.StringWidth(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	for i := 0; i < petCfg.PaddingTop; i++ {
		result.WriteString("\n")
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
	thoughtLine := "💭 " + displayThought
	result.WriteString(thoughtStyle.Render(thoughtLine) + "\n")

	// Pet position
	petX := c.pet.Pos.X
	if petX < 0 {
		petX = playWidth / 2
	}
	if petX >= playWidth-2 {
		petX = playWidth - 3
	}

	// Ground line with pet
	groundLine := make([]rune, playWidth)
	for i := range groundLine {
		groundLine[i] = ' '
	}
	petRunes := []rune(petSprite)
	if len(petRunes) > 0 && petX < playWidth {
		groundLine[petX] = petRunes[0]
	}
	result.WriteString(string(groundLine) + "\n")

	// Floor
	floorChar := "_"
	if style == "emoji" {
		floorChar = "▁"
	}
	result.WriteString(strings.Repeat(floorChar, playWidth) + "\n")

	// Status line
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	hungerBar := "❤"
	if c.pet.Hunger < 30 {
		hungerBar = "💔"
	}
	happyBar := "😸"
	if c.pet.Happiness < 30 {
		happyBar = "😿"
	}
	statusLine := fmt.Sprintf("%s%d%% %s%d%%", hungerBar, c.pet.Hunger, happyBar, c.pet.Happiness)
	result.WriteString(statusStyle.Render(statusLine) + "\n")

	for i := 0; i < petCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	for i := 0; i < petCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// HandleInput processes input events from renderers
func (c *Coordinator) HandleInput(clientID string, input *daemon.InputPayload) {
	switch input.Type {
	case "action":
		c.handleSemanticAction(clientID, input)
	case "key":
		c.handleKeyInput(clientID, input)
	}
}

// handleSemanticAction processes pre-resolved semantic actions from renderers
func (c *Coordinator) handleSemanticAction(clientID string, input *daemon.InputPayload) {
	// Handle right-click for context menus
	if input.Button == "right" && input.ResolvedAction != "" {
		c.handleRightClick(clientID, input)
		return
	}

	if input.ResolvedAction == "" {
		// No action resolved - just release focus back to main pane
		if input.PaneID != "" {
			exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return
	}

	switch input.ResolvedAction {
	case "select_window":
		// Run synchronously so RefreshWindows() sees the new state
		exec.Command("tmux", "select-window", "-t", input.ResolvedTarget).Run()
		exec.Command("tmux", "select-pane", "-R").Run()

	case "toggle_or_select_window":
		// For windows with panes: clicking left side (X < 6) toggles collapse, right side selects
		if input.MouseX < 6 {
			// Toggle window collapse state
			windowIdx := input.ResolvedTarget
			// Check current state first
			out, err := exec.Command("tmux", "show-window-options", "-v", "-t", ":"+windowIdx, "@tabby_collapsed").Output()
			if err == nil && strings.TrimSpace(string(out)) == "1" {
				// Currently collapsed, expand it
				exec.Command("tmux", "set-window-option", "-t", ":"+windowIdx, "-u", "@tabby_collapsed").Run()
			} else {
				// Currently expanded, collapse it
				exec.Command("tmux", "set-window-option", "-t", ":"+windowIdx, "@tabby_collapsed", "1").Run()
			}
			if input.PaneID != "" {
				exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
			}
		} else {
			// Click was on the right side - select window normally
			exec.Command("tmux", "select-window", "-t", input.ResolvedTarget).Run()
			exec.Command("tmux", "select-pane", "-R").Run()
		}

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
	}
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
		newWindowCmd := fmt.Sprintf("new-window -c %s ; set-window-option @tabby_group '%s'", dirArg, group.Name)
		args = append(args, fmt.Sprintf("New %s Window", group.Name), "n", newWindowCmd)
	} else {
		newWindowCmd := fmt.Sprintf("new-window -c %s", dirArg)
		args = append(args, "New Window", "n", newWindowCmd)
	}

	// Separator
	args = append(args, "", "", "")

	// Collapse/Expand option
	if c.collapsedGroups[group.Name] {
		args = append(args, "Expand Group", "e", "")
	} else {
		args = append(args, "Collapse Group", "c", "")
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
	"hungry": {"food. now.", "the bowl. it echoes.", "starving. dramatically.", "hunger level: critical."},
	"poop":   {"that won't clean itself.", "i made you a gift.", "cleanup crew needed.", "ahem. the floor."},
	"happy":  {"acceptable.", "fine. you may stay.", "purr engaged.", "not bad."},
	"yarn":   {"the yarn. it calls.", "must... catch...", "yarn acquired."},
	"sleepy": {"nap time.", "zzz...", "five more minutes."},
	"idle":   {"chillin'.", "vibin'.", "just here.", "sup.", "...", "waiting.", "*yawn*", "hmm."},
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
