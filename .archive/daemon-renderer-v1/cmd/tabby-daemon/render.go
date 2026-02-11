package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// lineRef types for click handling
type windowRef struct {
	windowIdx int
	groupIdx  int
	startLine int
	endLine   int
}

type paneRef struct {
	paneID     string
	windowIdx  int
	groupIdx   int
	paneIdx    int
	startLine  int
	endLine    int
}

type groupRef struct {
	groupName string
	groupIdx  int
	startLine int
	endLine   int
}

type buttonRef struct {
	action    string // "new_tab", "new_group", "close_tab"
	startLine int
	endLine   int
}

// SetColorProfile sets the lipgloss color profile based on client capabilities
func SetColorProfile(profile string) {
	switch profile {
	case "Ascii":
		lipgloss.SetColorProfile(termenv.Ascii)
	case "ANSI":
		lipgloss.SetColorProfile(termenv.ANSI)
	case "ANSI256":
		lipgloss.SetColorProfile(termenv.ANSI256)
	case "TrueColor":
		lipgloss.SetColorProfile(termenv.TrueColor)
	default:
		lipgloss.SetColorProfile(termenv.ANSI256)
	}
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
	petState      string // idle, walking, playing, eating, sleeping, happy, hungry
	petThought    string
	petHunger     int
	petHappiness  int
	petPosX       int
	thoughtScroll int

	// Line references for click handling (rebuilt on each render)
	windowRefs []windowRef
	paneRefs   []paneRef
	groupRefs  []groupRef
	buttonRefs []buttonRef

	// State locks
	stateMu sync.RWMutex

	// Session info
	sessionID string
}

// NewCoordinator creates a new coordinator instance
func NewCoordinator(sessionID string) *Coordinator {
	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		cfg = &config.Config{}
	}

	c := &Coordinator{
		sessionID:       sessionID,
		config:          cfg,
		collapsedGroups: make(map[string]bool),
		// Initialize pet state
		petState:     "idle",
		petThought:   "chillin'.",
		petHunger:    80,
		petHappiness: 80,
		petPosX:      5,
	}

	// Load collapsed groups from tmux option
	c.loadCollapsedGroups()

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
	out, err := exec.Command("tmux", "show-options", "-gqv", "@tabby_collapsed_groups").Output()
	if err != nil {
		return
	}
	groups := strings.TrimSpace(string(out))
	if groups != "" {
		for _, g := range strings.Split(groups, ",") {
			g = strings.TrimSpace(g)
			if g != "" {
				c.collapsedGroups[g] = true
			}
		}
	}
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

	// Get session name
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err == nil {
		c.sessionName = strings.TrimSpace(string(out))
	}

	// Get client count
	out, err = exec.Command("tmux", "list-clients", "-t", c.sessionName).Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] == "" {
			c.sessionClients = 0
		} else {
			c.sessionClients = len(lines)
		}
	}

	// Get window count
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

// ReloadConfig reloads the config file
func (c *Coordinator) ReloadConfig() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	cfg, err := config.LoadConfig(config.DefaultConfigPath())
	if err == nil {
		c.config = cfg
		c.grouped = grouping.GroupWindowsWithOptions(c.windows, c.config.Groups, c.config.Sidebar.ShowEmptyGroups)
	}
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
	mainContent, regions := c.generateMainContent(width, height)

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
	// Auto-detect for narrow terminals
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
// Returns the content string and clickable regions for this specific render
func (c *Coordinator) generateMainContent(width, height int) (string, []daemon.ClickableRegion) {
	var s strings.Builder
	var regions []daemon.ClickableRegion

	// Current line counter for reference tracking
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
	for _, group := range c.grouped {
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
		headerContentStyle := headerStyle.Width(width - 2)
		s.WriteString(collapseStyle.Render(collapseIcon+" ") + headerContentStyle.Render(headerText) + "\n")
		currentLine++

		// Touch divider
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
			isActive := win.Active
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
			regions = append(regions, daemon.ClickableRegion{
				StartLine: windowStartLine,
				EndLine:   currentLine - 1,
				Action:    "select_window",
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
						s.WriteString(" " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar) + paneIndStyle.Render(paneActiveIndicator) + fullWidthPaneStyle.Render(paneText) + "\n")
					} else {
						s.WriteString(" " + treeContinue + treeStyle.Render(" "+paneBranch+treeConnectorChar+treeConnectorChar) + paneStyle.Render(paneText) + "\n")
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

	// Pinned stats widget at bottom
	if c.config.Widgets.Stats.Enabled && c.config.Widgets.Stats.Pin {
		s.WriteString(c.renderStatsWidget(width))
	}

	// Pinned pet widget at bottom
	if c.config.Widgets.Pet.Enabled && c.config.Widgets.Pet.Pin {
		s.WriteString(c.renderPetWidget(width))
	}

	return s.String()
}

// renderClockWidget renders the clock/date widget
func (c *Coordinator) renderClockWidget(width int) string {
	clock := c.config.Widgets.Clock
	now := time.Now()

	// Default format
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

	var result strings.Builder

	// Margin top
	for i := 0; i < clock.MarginTop; i++ {
		result.WriteString("\n")
	}

	// Divider (top of widget)
	if clock.Divider != "" {
		dividerWidth := lipgloss.Width(clock.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	// Padding top
	for i := 0; i < clock.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Render time centered
	timeStr := now.Format(timeFormat)
	timePadding := (width - lipgloss.Width(timeStr)) / 2
	if timePadding < 0 {
		timePadding = 0
	}
	result.WriteString(style.Render(strings.Repeat(" ", timePadding)+timeStr) + "\n")

	// Optionally show date
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

	// Padding bottom
	for i := 0; i < clock.PaddingBot; i++ {
		result.WriteString("\n")
	}

	// Bottom divider
	if clock.DividerBottom != "" {
		dividerWidth := lipgloss.Width(clock.DividerBottom)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(clock.DividerBottom, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	// Margin bottom
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

	// Divider style
	dividerFg := git.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))

	// Main style
	fg := git.Fg
	if fg == "" {
		fg = "#888888"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))

	var result strings.Builder

	// Margin top
	for i := 0; i < git.MarginTop; i++ {
		result.WriteString("\n")
	}

	// Divider
	if git.Divider != "" {
		dividerWidth := lipgloss.Width(git.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(git.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	// Padding top
	for i := 0; i < git.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Git content
	icon := ""
	branch := c.gitBranch

	// Truncate branch if too long
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

	// Padding bottom
	for i := 0; i < git.PaddingBot; i++ {
		result.WriteString("\n")
	}

	// Margin bottom
	for i := 0; i < git.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// renderStatsWidget renders system stats widget
func (c *Coordinator) renderStatsWidget(width int) string {
	stats := c.config.Widgets.Stats

	// Divider style
	dividerFg := stats.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))

	// Main style
	fg := stats.Fg
	if fg == "" {
		fg = "#888888"
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(fg))

	var result strings.Builder

	// Margin top
	for i := 0; i < stats.MarginTop; i++ {
		result.WriteString("\n")
	}

	// Divider
	if stats.Divider != "" {
		dividerWidth := lipgloss.Width(stats.Divider)
		if dividerWidth == 0 {
			dividerWidth = 1
		}
		dividerLine := strings.Repeat(stats.Divider, width/dividerWidth)
		result.WriteString(dividerStyle.Render(dividerLine) + "\n")
	}

	// Padding top
	for i := 0; i < stats.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Stats content (simplified - could be enhanced with real stats)
	cpuIcon := ""
	memIcon := ""
	result.WriteString(style.Render(fmt.Sprintf(" %s --%%  %s --%%", cpuIcon, memIcon)) + "\n")

	// Padding bottom
	for i := 0; i < stats.PaddingBot; i++ {
		result.WriteString("\n")
	}

	// Margin bottom
	for i := 0; i < stats.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// sessionIcons holds the icons for session info by style
var sessionIconsByStyle = map[string]struct{ Session, Clients, Windows string }{
	"nerd":    {Session: "", Clients: "", Windows: ""},
	"emoji":   {Session: "📺", Clients: "👥", Windows: "🪟"},
	"ascii":   {Session: "[tmux]", Clients: "users:", Windows: "wins:"},
	"minimal": {Session: "", Clients: "", Windows: ""},
}

// petSprites defines the sprites for each style
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

// renderSessionWidget renders the session info widget
func (c *Coordinator) renderSessionWidget(width int) string {
	sessionCfg := c.config.Widgets.Session
	if !sessionCfg.Enabled {
		return ""
	}

	// Get icons for current style
	style := sessionCfg.Style
	if style == "" {
		style = "nerd"
	}
	icons, ok := sessionIconsByStyle[style]
	if !ok {
		icons = sessionIconsByStyle["nerd"]
	}

	var result strings.Builder

	// Margin top
	for i := 0; i < sessionCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	// Divider
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

	// Padding top
	for i := 0; i < sessionCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Build status line
	var parts []string

	// Session icon and name
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

	// Clients count
	if sessionCfg.ShowClients && c.sessionClients > 0 {
		clientStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
		if icons.Clients != "" {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%s%d", icons.Clients, c.sessionClients)))
		} else {
			parts = append(parts, clientStyle.Render(fmt.Sprintf("%d", c.sessionClients)))
		}
	}

	// Window count
	if sessionCfg.ShowWindowCount {
		windowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
		if icons.Windows != "" {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%s%d", icons.Windows, c.windowCount)))
		} else {
			parts = append(parts, windowStyle.Render(fmt.Sprintf("%d", c.windowCount)))
		}
	}

	// Join all parts
	result.WriteString(strings.Join(parts, " ") + "\n")

	// Padding bottom
	for i := 0; i < sessionCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	// Margin bottom
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
	switch c.petState {
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
	if c.petHunger < 30 {
		petSprite = sprites.Hungry
	}
	if petSprite == "" {
		petSprite = "🐱"
	}

	var result strings.Builder

	// Margin top
	for i := 0; i < petCfg.MarginTop; i++ {
		result.WriteString("\n")
	}

	// Divider
	divider := petCfg.Divider
	if divider == "" {
		divider = "─"
	}
	dividerFg := petCfg.DividerFg
	if dividerFg == "" {
		dividerFg = "#444444"
	}
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(dividerFg))
	dividerWidth := lipgloss.Width(divider)
	if dividerWidth > 0 {
		result.WriteString(dividerStyle.Render(strings.Repeat(divider, width/dividerWidth)) + "\n")
	}

	// Padding top
	for i := 0; i < petCfg.PaddingTop; i++ {
		result.WriteString("\n")
	}

	// Thought bubble with marquee effect
	thought := c.petThought
	if thought == "" {
		thought = "chillin'."
	}
	thoughtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	maxThoughtWidth := width - 4
	if maxThoughtWidth < 5 {
		maxThoughtWidth = 5
	}
	thoughtWidth := lipgloss.Width(thought)
	displayThought := thought
	if thoughtWidth > maxThoughtWidth {
		// Marquee effect - scroll through text
		scrollText := thought + "   " + thought
		scrollRunes := []rune(scrollText)
		startIdx := c.thoughtScroll % len(scrollRunes)
		// Extract visible portion
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

	// Play area - simple ground line with pet
	playWidth := width
	if playWidth < 5 {
		playWidth = 5
	}

	// Pet position (clamp to width)
	petX := c.petPosX
	if petX < 0 {
		petX = playWidth / 2
	}
	if petX >= playWidth-2 {
		petX = playWidth - 3
	}

	// Build ground line with pet
	groundLine := make([]rune, playWidth)
	for i := range groundLine {
		groundLine[i] = ' '
	}
	petRunes := []rune(petSprite)
	if len(petRunes) > 0 && petX < playWidth {
		groundLine[petX] = petRunes[0]
	}
	result.WriteString(string(groundLine) + "\n")

	// Ground floor indicator
	floorChar := "_"
	if style == "emoji" {
		floorChar = "▁"
	}
	result.WriteString(strings.Repeat(floorChar, playWidth) + "\n")

	// Status line (hunger/happiness)
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	hungerBar := "❤"
	if c.petHunger < 30 {
		hungerBar = "💔"
	}
	happyBar := "😸"
	if c.petHappiness < 30 {
		happyBar = "😿"
	}
	statusLine := fmt.Sprintf("%s%d%% %s%d%%", hungerBar, c.petHunger, happyBar, c.petHappiness)
	result.WriteString(statusStyle.Render(statusLine) + "\n")

	// Padding bottom
	for i := 0; i < petCfg.PaddingBot; i++ {
		result.WriteString("\n")
	}

	// Margin bottom
	for i := 0; i < petCfg.MarginBot; i++ {
		result.WriteString("\n")
	}

	return result.String()
}

// UpdatePetState updates the pet's state (called periodically)
func (c *Coordinator) UpdatePetState() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	// Decrease hunger over time
	if c.petHunger > 0 {
		c.petHunger--
	}

	// Decrease happiness if hungry
	if c.petHunger < 30 && c.petHappiness > 0 {
		c.petHappiness--
	}

	// Update state based on hunger/happiness
	if c.petHunger < 20 {
		c.petState = "hungry"
	} else if c.petHappiness > 80 {
		c.petState = "happy"
	} else {
		// Random state change occasionally
		states := []string{"idle", "walking", "idle", "idle"}
		c.petState = states[c.spinnerFrame%len(states)]
	}

	// Move pet position
	if c.petState == "walking" {
		c.petPosX += 1
		if c.petPosX > 20 {
			c.petPosX = 0
		}
	}

	// Scroll thought bubble
	c.thoughtScroll++
}

// FeedPet feeds the pet
func (c *Coordinator) FeedPet() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.petHunger = 100
	c.petState = "eating"
	c.petThought = "yum! food!"
}

// PetThePet pets the pet
func (c *Coordinator) PetThePet() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.petHappiness = 100
	c.petState = "happy"
	c.petThought = "*purrs*"
}

// HandleInput processes input events from renderers
func (c *Coordinator) HandleInput(clientID string, input *daemon.InputPayload) {
	switch input.Type {
	case "action":
		c.handleSemanticAction(clientID, input)
	case "mouse":
		c.handleMouseInput(clientID, input)
	case "key":
		c.handleKeyInput(clientID, input)
	}
}

// handleSemanticAction processes pre-resolved semantic actions from renderers
func (c *Coordinator) handleSemanticAction(clientID string, input *daemon.InputPayload) {
	// Debug logging
	fmt.Fprintf(os.Stderr, "[daemon] handleSemanticAction: action=%q target=%q paneID=%q\n",
		input.ResolvedAction, input.ResolvedTarget, input.PaneID)

	if input.ResolvedAction == "" {
		// No action resolved - just release focus back to main pane
		if input.PaneID != "" {
			go exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
		return
	}

	switch input.ResolvedAction {
	case "select_window":
		go func() {
			exec.Command("tmux", "select-window", "-t", input.ResolvedTarget).Run()
			// After selecting window, focus the main pane (right of sidebar)
			exec.Command("tmux", "select-pane", "-R").Run()
		}()

	case "select_pane":
		go exec.Command("tmux", "select-pane", "-t", input.ResolvedTarget).Run()

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
		// Release focus back to main pane
		if input.PaneID != "" {
			go exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}

	case "button":
		switch input.ResolvedTarget {
		case "new_tab":
			go exec.Command("tmux", "new-window").Run()
		case "new_group":
			// Could implement group creation dialog
		case "close_tab":
			go exec.Command("tmux", "kill-window").Run()
		}
	}
}

// handleMouseInput processes mouse events
func (c *Coordinator) handleMouseInput(clientID string, input *daemon.InputPayload) {
	if input.Action != "press" || input.Button != "left" {
		return
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	// Helper to release focus back to main pane after sidebar action
	releaseFocus := func() {
		// Select the pane to the right of the sidebar (the main content pane)
		if input.PaneID != "" {
			go exec.Command("tmux", "select-pane", "-t", input.PaneID, "-R").Run()
		}
	}

	// Calculate which visual line was clicked
	clickY := input.MouseY + input.ViewportOffset

	// Check panes first (more specific)
	for _, ref := range c.paneRefs {
		if clickY >= ref.startLine && clickY <= ref.endLine {
			go exec.Command("tmux", "select-pane", "-t", ref.paneID).Run()
			return
		}
	}

	// Check windows
	for _, ref := range c.windowRefs {
		if clickY >= ref.startLine && clickY <= ref.endLine {
			go func(idx int) {
				exec.Command("tmux", "select-window", "-t", strconv.Itoa(idx)).Run()
				// After selecting window, focus the main pane (right of sidebar)
				exec.Command("tmux", "select-pane", "-R").Run()
			}(ref.windowIdx)
			return
		}
	}

	// Check groups (for collapse toggle)
	for _, ref := range c.groupRefs {
		if clickY >= ref.startLine && clickY <= ref.endLine {
			// Click on group header - toggle collapse
			if c.collapsedGroups[ref.groupName] {
				delete(c.collapsedGroups, ref.groupName)
			} else {
				c.collapsedGroups[ref.groupName] = true
			}
			c.saveCollapsedGroups()
			releaseFocus()
			return
		}
	}

	// Check buttons
	for _, ref := range c.buttonRefs {
		if clickY >= ref.startLine && clickY <= ref.endLine {
			switch ref.action {
			case "new_tab":
				go exec.Command("tmux", "new-window").Run()
			case "new_group":
				// Could implement group creation dialog
			case "close_tab":
				go exec.Command("tmux", "kill-window").Run()
			}
			return
		}
	}

	// No specific element clicked - release focus to main pane anyway
	releaseFocus()
}

// saveCollapsedGroups saves collapsed state to tmux option
func (c *Coordinator) saveCollapsedGroups() {
	groups := make([]string, 0, len(c.collapsedGroups))
	for g := range c.collapsedGroups {
		groups = append(groups, g)
	}
	value := strings.Join(groups, ",")
	exec.Command("tmux", "set-option", "-g", "@tabby_collapsed_groups", value).Run()
}

// handleKeyInput processes keyboard events
func (c *Coordinator) handleKeyInput(clientID string, input *daemon.InputPayload) {
	switch input.Key {
	case "r":
		// Refresh
		c.RefreshWindows()
	case "R":
		// Reload config
		c.ReloadConfig()
	case "g":
		// Could toggle focused group collapse
	}
}

// ToggleGroupCollapse toggles a group's collapsed state
func (c *Coordinator) ToggleGroupCollapse(groupName string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if c.collapsedGroups[groupName] {
		delete(c.collapsedGroups, groupName)
	} else {
		c.collapsedGroups[groupName] = true
	}
	c.saveCollapsedGroups()
}
