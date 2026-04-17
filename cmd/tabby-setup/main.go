package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brendandebeasi/tabby/pkg/colors"
	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

// ── ANSI helpers ─────────────────────────────────────────────────────────────

const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	italic    = "\033[3m"
	clearLine = "\033[2K\r"
	up1       = "\033[1A"
	hideCur   = "\033[?25l"
	showCur   = "\033[?25h"
	clrScr    = "\033[2J\033[H"
)

func fg(hex string) string {
	r, g, b := hexToRGB(hex)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func bg(hex string) string {
	r, g, b := hexToRGB(hex)
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

func hexToRGB(h string) (int, int, int) {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return 128, 128, 128
	}
	var r, g, b int
	fmt.Sscanf(h[0:2], "%x", &r)
	fmt.Sscanf(h[2:4], "%x", &g)
	fmt.Sscanf(h[4:6], "%x", &b)
	return r, g, b
}

func colorSwatch(bgHex, fgHex, label string) string {
	b := bg(bgHex)
	f := fg(fgHex)
	if fgHex == "" {
		f = "\033[38;2;255;255;255m"
	}
	return fmt.Sprintf("%s%s  %s  %s", b, f, label, reset)
}

// ── Raw terminal input ────────────────────────────────────────────────────────

type rawTerm struct {
	fd    int
	state *term.State
}

func makeRaw() *rawTerm {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(uintptr(fd))
	if err != nil {
		return nil
	}
	return &rawTerm{fd: fd, state: state}
}

func (r *rawTerm) restore() {
	if r != nil && r.state != nil {
		term.Restore(uintptr(r.fd), r.state)
	}
}

func readKey() string {
	buf := make([]byte, 4)
	n, _ := unix.Read(0, buf)
	if n == 0 {
		return ""
	}
	b := buf[:n]
	// Escape sequences
	if b[0] == 27 && n > 1 && b[1] == '[' {
		switch b[2] {
		case 'A':
			return "up"
		case 'B':
			return "down"
		case 'C':
			return "right"
		case 'D':
			return "left"
		}
	}
	switch b[0] {
	case 13, 10:
		return "enter"
	case 27:
		return "esc"
	case 'q':
		return "q"
	case 'j':
		return "down"
	case 'k':
		return "up"
	}
	return string(b[:1])
}

// ── Menu widget ───────────────────────────────────────────────────────────────

type option struct {
	id    string
	label string
	desc  string
	extra string // extra preview/swatch printed on the right
}

// menu renders a vertical list of options with arrow-key navigation.
// Returns the selected option id and index.
func menu(title, subtitle string, opts []option, initial int) (string, int) {
	sel := initial
	if sel < 0 || sel >= len(opts) {
		sel = 0
	}

	render := func() {
		fmt.Print(clrScr)
		printHeader()
		fmt.Printf("\n  %s%s%s\n", bold, title, reset)
		if subtitle != "" {
			fmt.Printf("  %s%s%s\n", dim, subtitle, reset)
		}
		fmt.Println()
		for i, o := range opts {
			cursor := "  "
			labelStyle := dim
			if i == sel {
				cursor = bold + "▶ " + reset
				labelStyle = bold
			}
			extra := ""
			if o.extra != "" {
				extra = "  " + o.extra
			}
			fmt.Printf("  %s%s%s%s%s\n", cursor, labelStyle, o.label, reset, extra)
			if o.desc != "" {
				fmt.Printf("      %s%s%s\n", dim+italic, o.desc, reset)
			}
		}
		fmt.Printf("\n  %s↑↓ navigate  Enter select  q quit%s\n", dim, reset)
	}

	render()
	for {
		k := readKey()
		switch k {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(opts)-1 {
				sel++
			}
		case "enter":
			return opts[sel].id, sel
		case "q", "esc":
			return "", -1
		default:
			// Number shortcut
			for i, o := range opts {
				if k == fmt.Sprintf("%d", i+1) {
					return o.id, i
				}
			}
			continue
		}
		render()
	}
}

// confirm shows a yes/no prompt, returns true for yes.
func confirm(question string) bool {
	fmt.Printf("\n  %s%s%s [%sy%s/%sn%s]: ", bold, question, reset, bold, reset, bold, reset)
	buf := make([]byte, 1)
	for {
		unix.Read(0, buf)
		switch buf[0] {
		case 'y', 'Y', 13:
			fmt.Println("yes")
			return true
		case 'n', 'N', 27:
			fmt.Println("no")
			return false
		}
	}
}

// ── UI chrome ─────────────────────────────────────────────────────────────────

func printHeader() {
	logoFg := "\033[38;2;180;99;122m" // rose-pine love
	accentFg := "\033[38;2;144;122;169m"
	fmt.Printf("%s%s", logoFg, bold)
	fmt.Println(`  ████████╗ █████╗ ██████╗ ██████╗ ██╗   ██╗`)
	fmt.Println(`     ██╔══╝██╔══██╗██╔══██╗██╔══██╗╚██╗ ██╔╝`)
	fmt.Println(`     ██║   ███████║██████╔╝██████╔╝ ╚████╔╝ `)
	fmt.Println(`     ██║   ██╔══██║██╔══██╗██╔══██╗  ╚██╔╝  `)
	fmt.Println(`     ██║   ██║  ██║██████╔╝██████╔╝   ██║   `)
	fmt.Println(`     ╚═╝   ╚═╝  ╚═╝╚═════╝ ╚═════╝    ╚═╝   `)
	fmt.Printf("%s  Setup Wizard%s\n", accentFg, reset)
}

// ── Theme picker ──────────────────────────────────────────────────────────────

// themeOrder is the display order for themes, grouped by style.
var themeOrder = []string{
	"rose-pine-dawn", "catppuccin-latte", "solarized-light", "gruvbox-light",
	"rose-pine", "rose-pine-moon",
	"catppuccin-mocha", "dracula", "tokyo-night", "one-dark",
	"nord", "gruvbox-dark", "solarized-dark",
	"dark", "default",
}

func themeSwatchLine(t colors.Theme) string {
	if t.SidebarBg == "" {
		return dim + "(terminal default)" + reset
	}
	swatch := colorSwatch(t.SidebarBg, t.ActiveFg, " sidebar ")
	tab := colorSwatch(t.DefaultGroupBg, t.DefaultGroupFg, " tab ")
	activeTab := colorSwatch(t.DefaultActiveBg, t.DefaultActiveFg, " active ")
	indicator := colorSwatch(t.DefaultIndicatorBg, "#ffffff", " • ")
	return swatch + tab + activeTab + indicator
}

func pickTheme(current string) string {
	// Build ordered option list
	opts := make([]option, 0, len(themeOrder))
	currentIdx := 0
	for i, id := range themeOrder {
		t, ok := colors.Themes[id]
		if !ok {
			continue
		}
		mode := "dark"
		if !t.Dark {
			mode = "light"
		}
		opts = append(opts, option{
			id:    id,
			label: t.Name,
			desc:  fmt.Sprintf("%s  [%s]", t.Description, mode),
			extra: themeSwatchLine(t),
		})
		if id == current {
			currentIdx = i
		}
	}

	id, idx := menu("Choose a theme", "Use ↑↓ to browse, Enter to select", opts, currentIdx)
	_ = idx
	if id == "" {
		return current
	}
	return id
}

// ── Icon style picker ─────────────────────────────────────────────────────────

func pickIconStyle(current string) string {
	opts := []option{
		{
			id:    "box",
			label: "Box Drawing  (recommended)",
			desc:  "Unicode box-drawing characters — works in any terminal",
			extra: dim + "▾ groups  ▸ collapsed  ├─ tree  │ continue" + reset,
		},
		{
			id:    "emoji",
			label: "Emoji",
			desc:  "Emoji indicators — widely supported",
			extra: dim + "v groups  > collapsed  ! activity  ? input" + reset,
		},
		{
			id:    "nerd",
			label: "Nerd Fonts",
			desc:  "Requires a patched Nerd Font installed in your terminal",
			extra: dim + " groups   collapsed   bell   spinner" + reset,
		},
		{
			id:    "ascii",
			label: "ASCII Only",
			desc:  "Pure ASCII — works everywhere, even over slow connections",
			extra: dim + "[-] expanded  [+] collapsed  +- tree  | continue" + reset,
		},
	}

	currentIdx := 0
	for i, o := range opts {
		if o.id == current {
			currentIdx = i
		}
	}

	id, _ := menu("Choose an icon style", "Pick the style that matches your terminal font", opts, currentIdx)
	if id == "" {
		return current
	}
	return id
}

// ── Widget toggles ────────────────────────────────────────────────────────────

type widgetChoice struct {
	stats  bool
	clock  bool
	pet    bool
	claude bool
}

func pickWidgets(cur widgetChoice) widgetChoice {
	type toggle struct {
		label string
		desc  string
		ptr   *bool
	}
	toggles := []toggle{
		{"Stats widget", "CPU, memory, and battery in the sidebar", &cur.stats},
		{"Clock widget", "Date and time at the bottom of the sidebar", &cur.clock},
		{"Pet mascot", "A small tamagotchi-style pet (Whiskers 🐱)", &cur.pet},
		{"Claude usage", "Show today's Claude API cost", &cur.claude},
	}

	sel := 0
	render := func() {
		fmt.Print(clrScr)
		printHeader()
		fmt.Printf("\n  %sEnable sidebar widgets%s\n", bold, reset)
		fmt.Printf("  %sToggle with Space, confirm with Enter%s\n\n", dim, reset)
		for i, t := range toggles {
			cursor := "  "
			labelStyle := dim
			if i == sel {
				cursor = bold + "▶ " + reset
				labelStyle = bold
			}
			check := "○"
			checkColor := dim
			if *t.ptr {
				check = "●"
				checkColor = "\033[38;2;144;207;152m" // green
			}
			fmt.Printf("  %s%s%s %s%s%s\n", cursor, checkColor, check, labelStyle, t.label, reset)
			fmt.Printf("      %s%s%s\n", dim+italic, t.desc, reset)
		}
		fmt.Printf("\n  %s↑↓ navigate  Space toggle  Enter confirm  q quit%s\n", dim, reset)
	}

	render()
	for {
		k := readKey()
		switch k {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(toggles)-1 {
				sel++
			}
		case " ":
			*toggles[sel].ptr = !*toggles[sel].ptr
		case "enter":
			return cur
		case "q", "esc":
			return cur
		default:
			continue
		}
		render()
	}
}

// ── Auto-theme picker ─────────────────────────────────────────────────────────

type autoThemeChoice struct {
	enabled   bool
	mode      string // "system" | "time"
	light     string
	dark      string
	timeLight string
	timeDark  string
}

func pickAutoTheme(cur autoThemeChoice) autoThemeChoice {
	// Step A: enable?
	modeOpts := []option{
		{id: "off", label: "Off", desc: "Use a fixed theme (configured in the previous step)"},
		{id: "system", label: "Follow OS dark/light mode", desc: "macOS: System Preferences → Appearance  |  Linux: GNOME/KDE color scheme"},
		{id: "time", label: "Schedule by time of day", desc: "Light theme in the morning, dark theme in the evening"},
	}
	currentModeIdx := 0
	if cur.enabled {
		switch cur.mode {
		case "system":
			currentModeIdx = 1
		case "time":
			currentModeIdx = 2
		}
	}

	modeID, modeIdx := menu(
		"Auto-switch theme",
		"Automatically change the sidebar theme based on environment",
		modeOpts, currentModeIdx,
	)
	_ = modeIdx
	if modeID == "" || modeID == "off" {
		cur.enabled = false
		return cur
	}
	cur.enabled = true
	cur.mode = modeID

	// Step B: pick the light theme
	if cur.light == "" {
		cur.light = "rose-pine-dawn"
	}
	light := pickThemeForMode("Choose light theme", "Used during the day / when OS is in light mode", cur.light)
	if light != "" {
		cur.light = light
	}

	// Step C: pick the dark theme
	if cur.dark == "" {
		cur.dark = "rose-pine"
	}
	dark := pickThemeForMode("Choose dark theme", "Used at night / when OS is in dark mode", cur.dark)
	if dark != "" {
		cur.dark = dark
	}

	// Step D (time mode only): pick transition times
	if modeID == "time" {
		if cur.timeLight == "" {
			cur.timeLight = "08:00"
		}
		if cur.timeDark == "" {
			cur.timeDark = "20:00"
		}
		timeOpts := []option{
			{id: "06:00|22:00", label: "Early bird  (light 6am – 10pm)", desc: ""},
			{id: "08:00|20:00", label: "Standard    (light 8am – 8pm)", desc: ""},
			{id: "09:00|18:00", label: "Office hours (light 9am – 6pm)", desc: ""},
			{id: "07:00|21:00", label: "Flexible    (light 7am – 9pm)", desc: ""},
		}
		current := cur.timeLight + "|" + cur.timeDark
		currentTimeIdx := 1 // default to Standard
		for i, o := range timeOpts {
			if o.id == current {
				currentTimeIdx = i
			}
		}
		timeID, _ := menu("Light/dark schedule", "When does each theme take effect?", timeOpts, currentTimeIdx)
		if timeID != "" {
			parts := strings.SplitN(timeID, "|", 2)
			if len(parts) == 2 {
				cur.timeLight = parts[0]
				cur.timeDark = parts[1]
			}
		}
	}

	return cur
}

// pickThemeForMode is a variant of pickTheme used when selecting light/dark halves.
func pickThemeForMode(title, subtitle, current string) string {
	opts := make([]option, 0, len(themeOrder))
	currentIdx := 0
	for i, id := range themeOrder {
		t, ok := colors.Themes[id]
		if !ok {
			continue
		}
		mode := "dark"
		if !t.Dark {
			mode = "light"
		}
		opts = append(opts, option{
			id:    id,
			label: t.Name,
			desc:  fmt.Sprintf("%s  [%s]", t.Description, mode),
			extra: themeSwatchLine(t),
		})
		if id == current {
			currentIdx = i
		}
	}
	id, _ := menu(title, subtitle, opts, currentIdx)
	if id == "" {
		return current
	}
	return id
}

// ── Summary & save ────────────────────────────────────────────────────────────

func printSummary(theme, iconStyle string, widgets widgetChoice, at autoThemeChoice) {
	fmt.Print(clrScr)
	printHeader()
	fmt.Printf("\n  %sConfiguration Summary%s\n\n", bold, reset)

	t := colors.Themes[theme]
	if at.enabled {
		lt := colors.Themes[at.light]
		dt := colors.Themes[at.dark]
		fmt.Printf("  Theme:       %sauto%s\n", bold, reset)
		fmt.Printf("    light  →   %s%s%s %s\n", bold, lt.Name, reset, themeSwatchLine(lt))
		fmt.Printf("    dark   →   %s%s%s %s\n", bold, dt.Name, reset, themeSwatchLine(dt))
		if at.mode == "time" {
			fmt.Printf("    schedule:  light from %s, dark from %s\n", at.timeLight, at.timeDark)
		} else {
			fmt.Printf("    mode:      follow OS\n")
		}
	} else {
		fmt.Printf("  Theme:       %s%s%s %s\n", bold, t.Name, reset, themeSwatchLine(t))
	}
	fmt.Printf("  Icon style:  %s%s%s\n", bold, iconStyle, reset)

	widgetsOn := []string{}
	if widgets.stats {
		widgetsOn = append(widgetsOn, "stats")
	}
	if widgets.clock {
		widgetsOn = append(widgetsOn, "clock")
	}
	if widgets.pet {
		widgetsOn = append(widgetsOn, "pet")
	}
	if widgets.claude {
		widgetsOn = append(widgetsOn, "claude usage")
	}
	if len(widgetsOn) == 0 {
		widgetsOn = []string{"none"}
	}
	fmt.Printf("  Widgets:     %s%s%s\n", bold, strings.Join(widgetsOn, ", "), reset)

	cfgPath := configPath()
	fmt.Printf("\n  Will write:  %s%s%s\n", dim, cfgPath, reset)
}

// ── Config path ───────────────────────────────────────────────────────────────

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tabby", "config.yaml")
}

// ── Apply choices to config ───────────────────────────────────────────────────

func applyChoices(cfg *config.Config, theme, iconStyle string, widgets widgetChoice, at autoThemeChoice) {
	cfg.Sidebar.Theme = theme
	cfg.Sidebar.IconStyle = iconStyle

	// Clear any per-field icon overrides so the new preset takes full effect.
	// Users who want custom overrides can edit config.yaml directly afterwards.
	cfg.Sidebar.Colors.DisclosureExpanded = ""
	cfg.Sidebar.Colors.DisclosureCollapsed = ""
	cfg.Sidebar.Colors.ActiveIndicator = ""
	cfg.Sidebar.Colors.TreeBranch = ""
	cfg.Sidebar.Colors.TreeBranchLast = ""
	cfg.Sidebar.Colors.TreeConnector = ""
	cfg.Sidebar.Colors.TreeConnectorPanes = ""
	cfg.Sidebar.Colors.TreeContinue = ""
	cfg.Indicators.Activity.Icon = ""
	cfg.Indicators.Bell.Icon = ""
	cfg.Indicators.Silence.Icon = ""
	cfg.Indicators.Busy.Icon = ""
	cfg.Indicators.Input.Icon = ""

	cfg.Widgets.Stats.Enabled = widgets.stats
	cfg.Widgets.Clock.Enabled = widgets.clock
	cfg.Widgets.Pet.Enabled = widgets.pet
	cfg.Widgets.Claude.Enabled = widgets.claude

	// Apply sensible widget defaults if enabling for first time
	if widgets.stats && cfg.Widgets.Stats.Style == "" {
		cfg.Widgets.Stats.Style = "emoji"
		cfg.Widgets.Stats.ShowCPU = true
		cfg.Widgets.Stats.ShowMemory = true
		cfg.Widgets.Stats.ShowBattery = true
	}
	if widgets.clock && cfg.Widgets.Clock.Format == "" {
		cfg.Widgets.Clock.Format = "15:04:05"
		cfg.Widgets.Clock.ShowDate = true
		cfg.Widgets.Clock.DateFmt = "Mon Jan 2"
		cfg.Widgets.Clock.Position = "bottom"
		cfg.Widgets.Clock.Pin = true
	}
	if widgets.pet && cfg.Widgets.Pet.Name == "" {
		cfg.Widgets.Pet.Name = "Whiskers"
		cfg.Widgets.Pet.Style = "graphics"
		cfg.Widgets.Pet.Rows = 2
		cfg.Widgets.Pet.Thoughts = true
		cfg.Widgets.Pet.ThoughtInterval = 60
		cfg.Widgets.Pet.ThoughtSpeed = 3
		cfg.Widgets.Pet.Position = "bottom"
		cfg.Widgets.Pet.Pin = true
	}
	if widgets.claude {
		cfg.Widgets.Claude.ShowToday = true
		cfg.Widgets.Claude.ShowMessages = true
		cfg.Widgets.Claude.Position = "bottom"
	}

	// Auto-theme
	cfg.AutoTheme.Enabled = at.enabled
	if at.enabled {
		cfg.AutoTheme.Mode = at.mode
		cfg.AutoTheme.Light = at.light
		cfg.AutoTheme.Dark = at.dark
		cfg.AutoTheme.TimeLight = at.timeLight
		cfg.AutoTheme.TimeDark = at.timeDark
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// Enter raw mode for interactive input
	rt := makeRaw()
	defer rt.restore()
	fmt.Print(hideCur)
	defer fmt.Print(showCur)

	cfgPath := configPath()

	// Load existing config or start fresh
	var cfg *config.Config
	if _, err := os.Stat(cfgPath); err == nil {
		cfg, err = config.LoadConfig(cfgPath)
		if err != nil {
			rt.restore()
			fmt.Print(showCur)
			fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg = config.DefaultConfig()
	}

	// Determine current values as defaults
	currentTheme := cfg.Sidebar.Theme
	if currentTheme == "" {
		currentTheme = "rose-pine-dawn"
	}
	currentIconStyle := cfg.Sidebar.IconStyle
	if currentIconStyle == "" {
		currentIconStyle = "box"
	}
	currentWidgets := widgetChoice{
		stats:  cfg.Widgets.Stats.Enabled,
		clock:  cfg.Widgets.Clock.Enabled,
		pet:    cfg.Widgets.Pet.Enabled,
		claude: cfg.Widgets.Claude.Enabled,
	}
	currentAutoTheme := autoThemeChoice{
		enabled:   cfg.AutoTheme.Enabled,
		mode:      cfg.AutoTheme.Mode,
		light:     cfg.AutoTheme.Light,
		dark:      cfg.AutoTheme.Dark,
		timeLight: cfg.AutoTheme.TimeLight,
		timeDark:  cfg.AutoTheme.TimeDark,
	}

	// ── Welcome ───────────────────────────────────────────────────────────────
	fmt.Print(clrScr)
	printHeader()
	fmt.Printf("\n  Welcome to the %stabby%s setup wizard!\n\n", bold, reset)
	fmt.Printf("  This will guide you through configuring your sidebar:\n")
	fmt.Printf("    %s•%s Theme & colors\n", bold, reset)
	fmt.Printf("    %s•%s Auto dark/light mode\n", bold, reset)
	fmt.Printf("    %s•%s Icon style\n", bold, reset)
	fmt.Printf("    %s•%s Sidebar widgets\n", bold, reset)
	fmt.Printf("\n  Config file: %s%s%s\n", dim, cfgPath, reset)
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("  %s(existing config detected — your customizations will be preserved)%s\n", dim, reset)
	}
	fmt.Printf("\n  %sPress Enter to begin, q to quit...%s", dim, reset)
	for {
		k := readKey()
		if k == "enter" || k == " " {
			break
		}
		if k == "q" || k == "esc" {
			rt.restore()
			fmt.Print(showCur, "\n")
			os.Exit(0)
		}
	}

	// ── Step 1: Theme ─────────────────────────────────────────────────────────
	theme := pickTheme(currentTheme)
	if theme == "" {
		theme = currentTheme
	}

	// ── Step 2: Auto-theme ───────────────────────────────────────────────────
	autoTheme := pickAutoTheme(currentAutoTheme)

	// ── Step 3: Icon style ────────────────────────────────────────────────────
	iconStyle := pickIconStyle(currentIconStyle)
	if iconStyle == "" {
		iconStyle = currentIconStyle
	}

	// ── Step 4: Widgets ───────────────────────────────────────────────────────
	widgets := pickWidgets(currentWidgets)

	// ── Step 5: Review & save ─────────────────────────────────────────────────
	printSummary(theme, iconStyle, widgets, autoTheme)

	rt.restore()
	fmt.Print(showCur)

	fmt.Printf("\n")
	var ans [1]byte
	fmt.Printf("  Save this configuration? [Y/n]: ")
	os.Stdout.Sync()
	rt2 := makeRaw()
	unix.Read(0, ans[:])
	rt2.restore()
	fmt.Println()

	if ans[0] == 'n' || ans[0] == 'N' {
		fmt.Println("  Cancelled — no changes written.")
		return
	}

	// Apply and save
	applyChoices(cfg, theme, iconStyle, widgets, autoTheme)

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating config dir: %v\n", err)
		os.Exit(1)
	}
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error saving config: %v\n", err)
		os.Exit(1)
	}

	t := colors.Themes[theme]
	fmt.Printf("\n  %s✓ Config saved!%s\n\n", "\033[38;2;144;207;152m", reset)
	fmt.Printf("  Theme:  %s%s\n", bold, t.Name+reset)
	fmt.Printf("  Icons:  %s%s\n", bold, iconStyle+reset)
	fmt.Printf("  Reload tmux to apply: %stmux source ~/.tmux.conf%s\n\n", dim, reset)
}
