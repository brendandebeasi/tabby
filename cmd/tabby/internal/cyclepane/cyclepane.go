// Package cyclepane cycles the active content pane in the current window
// and applies dim-style to inactive panes. Exported as the
// `tabby cycle-pane` subcommand.
package cyclepane

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/brendandebeasi/tabby/cmd/tabby/internal/dashlayout"
	"github.com/brendandebeasi/tabby/pkg/config"
)

// skipCommands are never dimmed and never cycled (sidebar, pane-header).
// We match against BOTH pane_current_command and pane_start_command: after
// the binary consolidation, pane_current_command reports "tabby" for every
// subcommand process (tmux reads it from the executable name, not argv[0],
// so `exec -a sidebar-renderer` doesn't help). pane_start_command still
// contains the original `render sidebar` / `render pane-header` invocation.
var skipCommands = []string{"sidebar-render", "render sidebar", "sidebar-renderer", "window-header", "render window-header"}

// headerCommand identifies pane-header processes (dimmed with their content pane).
// Same rationale: match via either command field.
var headerCommands = []string{"pane-header", "render pane-header"}

type paneInfo struct {
	id           string
	active       bool
	command      string // pane_current_command — post-consolidation often "tabby"
	startCommand string // pane_start_command — retains the original invocation
	left         int    // pane_left: used to match headers with content panes
}

func Run(args []string) int {
	// --move <promote|next|prev> reorders the focused CONTENT pane instead of
	// cycling focus. Handled before the focus-cycle path below.
	if len(args) >= 2 && args[0] == "--move" {
		return movePane(args[1])
	}

	// --main-follow keeps the active pane in the main/largest slot when the
	// dashboard is in an "-auto" layout (Main+stack/row, active). Wired to the
	// after-select-pane hook so the big pane follows focus. No-op otherwise.
	if len(args) >= 1 && args[0] == "--main-follow" {
		return mainFollow()
	}

	dimOnly := len(args) > 0 && args[0] == "--dim-only"
	ensureContent := len(args) > 0 && args[0] == "--ensure-content"

	panes := listPanes()
	content := filterContent(panes)

	// In the dashboard window (cmd+opt+~), cycle tiles but do NOT dim the others —
	// dimming the grid fights the at-a-glance view. Mirrors the [/] behavior.
	// Also skip signalDaemon: it triggers a full refresh+broadcast that makes
	// the sidebar renderer redraw on every cycle (visible as an up/down flicker)
	// even though the sidebar content doesn't change with pane focus.
	if inDashboardWindow() {
		if !dimOnly && !ensureContent && len(content) >= 2 {
			cyclePane(content)
		}
		return 0
	}

	selected := ""
	switch {
	case ensureContent:
		if len(content) >= 1 && activeIsUtility(panes) {
			_ = exec.Command("tmux", "select-pane", "-t", content[0].id).Run()
			selected = content[0].id
		}
	case !dimOnly && len(content) >= 2:
		selected = cyclePane(content)
	}

	applyDim(selected)
	signalDaemon()
	return 0
}

// inDashboardWindow reports whether the current window is the tabby dashboard
// (window-option @tabby_dashboard=1).
func inDashboardWindow() bool {
	out, err := exec.Command("tmux", "show-options", "-wqv", "@tabby_dashboard").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

func activeIsUtility(panes []paneInfo) bool {
	for _, p := range panes {
		if p.active {
			return isUtility(p)
		}
	}
	return false
}

func listPanes() []paneInfo {
	out, err := exec.Command("tmux", "list-panes", "-F",
		"#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_left}\t#{pane_start_command}").Output()
	if err != nil {
		return nil
	}
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		left, _ := strconv.Atoi(parts[3])
		info := paneInfo{
			id:      parts[0],
			active:  parts[1] == "1",
			command: parts[2],
			left:    left,
		}
		if len(parts) >= 5 {
			info.startCommand = parts[4]
		}
		panes = append(panes, info)
	}
	return panes
}

func matchesAny(haystack string, needles []string) bool {
	lower := strings.ToLower(haystack)
	for _, s := range needles {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// isSkip returns true for panes that should never be cycled or dimmed
// (sidebar, pane-header). Matches against both current and start command.
func isSkip(p paneInfo) bool {
	return matchesAny(p.command, skipCommands) ||
		matchesAny(p.startCommand, skipCommands)
}

// isHeader returns true for pane-header panes (current or start command match).
func isHeader(p paneInfo) bool {
	return matchesAny(p.command, headerCommands) ||
		matchesAny(p.startCommand, headerCommands)
}

func isUtility(p paneInfo) bool {
	return isSkip(p) || isHeader(p)
}

func filterContent(panes []paneInfo) []paneInfo {
	var out []paneInfo
	for _, p := range panes {
		if !isUtility(p) {
			out = append(out, p)
		}
	}
	return out
}

// cyclePane moves focus to the next content pane and returns its id. The caller
// passes that id to applyDim: re-reading pane_active straight after select-pane
// can still report the OLD active pane, which would style the pane the user just
// focused as the inactive one.
func cyclePane(content []paneInfo) string {
	activeIdx := -1
	for i, p := range content {
		if p.active {
			activeIdx = i
			break
		}
	}
	if activeIdx < 0 {
		activeIdx = 0
	}
	nextIdx := (activeIdx + 1) % len(content)
	target := content[nextIdx].id
	_ = exec.Command("tmux", "select-pane", "-t", target).Run()
	return target
}

// moveTargetIndex computes the destination slot for a move within the content-
// pane list. Pure so it can be unit-tested without tmux. Returns (idx, true)
// when a swap should happen, or (_, false) for a no-op (too few panes, active
// not found, already primary on promote, or an unknown direction).
//
//   - promote: slot 0 (the first/main content pane); no-op if already there.
//   - next:    one slot forward, wrapping.
//   - prev:    one slot back, wrapping.
func moveTargetIndex(activeIdx, n int, dir string) (int, bool) {
	if n < 2 || activeIdx < 0 || activeIdx >= n {
		return 0, false
	}
	switch dir {
	case "promote":
		if activeIdx == 0 {
			return 0, false
		}
		return 0, true
	case "next":
		return (activeIdx + 1) % n, true
	case "prev":
		return (activeIdx - 1 + n) % n, true
	default:
		return 0, false
	}
}

// movePane swaps the focused content pane with another content slot, keeping
// focus on the moved pane. Aux panes (sidebar / pane-header) are excluded from
// the slot list via filterContent, so the sidebar is never a swap target. In
// the dashboard window it reflows to the active @tabby_dash_layout so main-*
// arrangements rebuild with the moved pane in its new slot.
func movePane(dir string) int {
	content := filterContent(listPanes())
	activeIdx := -1
	for i, p := range content {
		if p.active {
			activeIdx = i
			break
		}
	}
	// activeIdx == -1 means a non-content pane (e.g. the sidebar) is focused;
	// there's nothing to promote/move.
	targetIdx, ok := moveTargetIndex(activeIdx, len(content), dir)
	if !ok {
		return 0
	}

	activeID := content[activeIdx].id
	targetID := content[targetIdx].id
	// swap-pane exchanges the two panes' on-screen POSITIONS (pane ids are
	// stable). That alone is the move/promote: the focused pane lands in the
	// target slot — slot 0 (the main/primary position) for promote, the
	// adjacent slot for next/prev. We deliberately do NOT re-run
	// `select-layout <named>` afterward: named tmux layouts assign the main
	// pane by pane *index*, not position, so a reflow would immediately revert
	// the swap and snap the original pane back to primary.
	_ = exec.Command("tmux", "swap-pane", "-s", activeID, "-t", targetID).Run()
	// Keep focus following the pane we moved into its new slot.
	_ = exec.Command("tmux", "select-pane", "-t", activeID).Run()

	// In the dashboard, skip dim + daemon signal (mirrors the focus-cycle
	// dashboard branch — the sidebar content doesn't change on a pane reorder).
	if inDashboardWindow() {
		return 0
	}
	signalDaemon()
	return 0
}

// dashLayout returns the persisted dashboard arrangement (global
// @tabby_dash_layout option), defaulting to "tiled" when unset.
func dashLayout() string {
	out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_dash_layout").Output()
	if err != nil {
		return "tiled"
	}
	if v := strings.TrimSpace(string(out)); v != "" {
		return v
	}
	return "tiled"
}

// mainFollow rearranges the dashboard so the active content pane occupies the
// main/big slot and the remaining panes keep a stable, pane-id-sorted order in
// the stack (so a pane always falls back to the same slot rather than drifting
// to wherever focus came from). Only acts in the dashboard when the saved
// layout is an "-auto" mode (main-vertical-auto / main-horizontal-auto);
// otherwise it's a cheap no-op. Wired to after-select-pane so the main pane
// tracks focus. swap-pane preserves the window geometry — only which pane
// occupies each slot changes.
func mainFollow() int {
	if !inDashboardWindow() || !strings.HasSuffix(dashLayout(), "-auto") {
		return 0
	}
	content := filterContent(listPanes()) // position order, sidebar/aux excluded
	ids := make([]string, 0, len(content))
	active := ""
	for _, p := range content {
		ids = append(ids, p.id)
		if p.active {
			active = p.id
		}
	}
	swaps := dashlayout.PlanActiveMainSwaps(ids, active)
	if len(swaps) == 0 {
		return 0
	}
	// One chained tmux command (swap … ; swap … ; select-pane) so tmux redraws
	// once at the final arrangement instead of flickering per swap.
	args := make([]string, 0, len(swaps)*5+4)
	for i, sw := range swaps {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "swap-pane", "-s", sw[0], "-t", sw[1])
	}
	args = append(args, ";", "select-pane", "-t", active)
	_ = exec.Command("tmux", args...).Run()
	return 0
}

func isSpawning() bool {
	out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// applyDim styles every pane in the window. activePaneID, when non-empty,
// overrides the pane_active tmux reports -- see cyclePane.
func applyDim(activePaneID string) {
	if isSpawning() {
		return
	}

	cfgPath := config.DefaultConfigPath()
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return
	}

	panes := listPanes()
	if activePaneID != "" {
		for i := range panes {
			panes[i].active = panes[i].id == activePaneID
		}
	}

	// The daemon resolves each window's tab color (custom color, else group
	// theme) and publishes the blended background on @tabby_tint_bg. We cannot
	// recompute it here — that state lives in the daemon's memory — so read it.
	// Empty means "no tint": either the feature is off or the window has no
	// color, and in both cases window-style must be left alone.
	tintBG := windowTintBG()
	baseFg, inactiveFg := dimFgPair(cfg)

	if !cfg.PaneHeader.DimInactive {
		for _, p := range panes {
			if !isSkip(p) {
				// Dimming is off, but the tint (if any) still applies — a blanket
				// unset here would wipe it every time this ran.
				if tintBG != "" {
					s := styleWithFg(baseFg, tintBG)
					setPaneStyles(p.id, s, s)
				} else {
					unsetPaneStyle(p.id)
				}
			}
			if !isUtility(p) {
				clearPaneDimFlag(p.id)
			}
		}
		if out, err := exec.Command("tmux", "show-options", "-gqv", "pane-active-border-style").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				_ = exec.Command("tmux", "set-option", "-g", "pane-border-style", s).Run()
			}
		}
		return
	}

	colActive := map[int]bool{}
	hasActiveContent := false
	for _, p := range panes {
		if !isUtility(p) {
			colActive[p.left] = p.active
			if p.active {
				hasActiveContent = true
			}
		}
	}

	if !hasActiveContent {
		return
	}

	// Dim the TINTED base, not the raw terminal bg, so an inactive pane keeps
	// its session color while still reading as unfocused. Matches the daemon's
	// ApplyPaneDimming so the two writers agree on every pane.
	dimBase := cfg.PaneHeader.TerminalBg
	if tintBG != "" {
		dimBase = tintBG
	}
	dimBG := computeDimBG(dimBase, cfg.PaneHeader.DimOpacity)

	for _, p := range panes {
		if isSkip(p) {
			continue
		}

		active := p.active
		if isHeader(p) {
			active = colActive[p.left]
		}

		if isHeader(p) {
			// Headers are rendered by the daemon — don't set window-style.
			continue
		}

		if active {
			// The focused pane shows the undimmed base: the tint when there is
			// one, otherwise tmux's inherited default.
			if tintBG != "" {
				setPaneStyles(p.id, styleWithFg(inactiveFg, tintBG), styleWithFg(baseFg, tintBG))
			} else {
				unsetPaneStyle(p.id)
			}
			setPaneDimFlag(p.id, false)
		} else {
			switch {
			case dimBG == "" && tintBG == "":
				unsetPaneStyle(p.id)
			case dimBG == "":
				s := styleWithFg(baseFg, tintBG)
				setPaneStyles(p.id, s, s)
			case tintBG != "":
				// Keep the active-style on the undimmed tint: this pane paints
				// from it the instant it takes focus.
				setPaneStyles(p.id, styleWithFg(inactiveFg, dimBG), styleWithFg(baseFg, tintBG))
			default:
				setPaneStyle(p.id, styleWithFg(inactiveFg, dimBG))
			}
			setPaneDimFlag(p.id, true)
		}
	}
	applyBorderDim(cfg)
}

// setPaneStyle writes window-style, and keeps window-active-style in step when
// a tint is in play. tmux paints the FOCUSED pane from window-active-style and
// every other pane from window-style; writing only the former would leave the
// pane the user is actually looking at untinted.
func setPaneStyle(paneID, style string) {
	_ = exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", style).Run()
}

func setPaneStyles(paneID, inactive, active string) {
	_ = exec.Command("tmux",
		"set-option", "-p", "-t", paneID, "window-style", inactive, ";",
		"set-option", "-p", "-t", paneID, "window-active-style", active).Run()
}

func unsetPaneStyle(paneID string) {
	_ = exec.Command("tmux",
		"set-option", "-p", "-u", "-t", paneID, "window-style", ";",
		"set-option", "-p", "-u", "-t", paneID, "window-active-style").Run()
}

// windowTintBG reads the tint background the daemon resolved for the current
// window. The daemon owns the color/group state, so this is a read-only handoff
// rather than a second implementation of the same resolution.
func windowTintBG() string {
	out, err := exec.Command("tmux", "show-options", "-wqv", "@tabby_tint_bg").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// dimFgPair returns the foreground the undimmed and dimmed styles must carry.
// The global window-style sets a fg=; a per-pane style that writes only bg=
// therefore CHANGES the foreground, so programs that emit no color of their own
// visibly recolor as panes are styled and unstyled. Mirror the fg in every
// write so only the background ever moves.
//
// The values come from the daemon (@tabby_pane_fg/@tabby_pane_fg_dim), which
// derives them from the active THEME. Deriving them from config here instead
// would produce a different pair, and the foreground would then flip between
// the daemon's value and ours on every pane switch. Config is only a fallback
// for the window where the daemon has not painted yet.
func dimFgPair(cfg *config.Config) (string, string) {
	base := strings.TrimSpace(readGlobalOption("@tabby_pane_fg"))
	dim := strings.TrimSpace(readGlobalOption("@tabby_pane_fg_dim"))
	if base != "" && dim != "" {
		return base, dim
	}
	if base == "" {
		base = cfg.PaneHeader.ActiveFg
	}
	if base == "" {
		base = "#ffffff"
	}
	opacity := cfg.PaneHeader.DimOpacity
	if opacity <= 0 || opacity > 1 {
		opacity = 0.6
	}
	if dim == "" {
		dim = desaturateColor(base, opacity, cfg.PaneHeader.TerminalBg)
	}
	return base, dim
}

func readGlobalOption(name string) string {
	out, err := exec.Command("tmux", "show-options", "-gqv", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func styleWithFg(fg, bg string) string {
	if fg == "" {
		return fmt.Sprintf("bg=%s", bg)
	}
	return fmt.Sprintf("fg=%s,bg=%s", fg, bg)
}

func setPaneDimFlag(paneID string, dimmed bool) {
	val := "0"
	if dimmed {
		val = "1"
	}
	_ = exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_dim", val).Run()
}

func clearPaneDimFlag(paneID string) {
	_ = exec.Command("tmux", "set-option", "-p", "-u", "-t", paneID, "@tabby_pane_dim").Run()
}

func computeDimBG(terminalBG string, opacity float64) string {
	if terminalBG == "" {
		return ""
	}
	tbR, tbG, tbB := parseHex(terminalBG)
	lum := (tbR*299 + tbG*587 + tbB*114) / 1000

	// Light themes: shift toward white (inactive looks washed-out/lighter).
	// Dark themes: shift toward black (inactive looks deeper/less prominent).
	var targetR, targetG, targetB int
	if lum >= 128 {
		targetR, targetG, targetB = 255, 255, 255
	} else {
		targetR, targetG, targetB = 0, 0, 0
	}

	inv := 1.0 - opacity
	dr := int(math.Round(float64(tbR)*opacity + float64(targetR)*inv))
	dg := int(math.Round(float64(tbG)*opacity + float64(targetG)*inv))
	db := int(math.Round(float64(tbB)*opacity + float64(targetB)*inv))
	return fmt.Sprintf("#%02x%02x%02x", clamp(dr), clamp(dg), clamp(db))
}

func parseHex(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 32)
	g, _ := strconv.ParseInt(hex[2:4], 16, 32)
	b, _ := strconv.ParseInt(hex[4:6], 16, 32)
	return int(r), int(g), int(b)
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func signalDaemon() {
	if isSpawning() {
		return
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_id}").Output()
	if err != nil {
		return
	}
	sessionID := strings.TrimSpace(string(out))
	data, err := os.ReadFile(fmt.Sprintf("/tmp/tabby-daemon-%s.pid", sessionID))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Signal(syscall.SIGUSR1)
	}
}

func applyBorderDim(cfg *config.Config) {
	out, err := exec.Command("tmux", "show-options", "-gqv", "pane-active-border-style").Output()
	if err != nil {
		return
	}
	styleStr := strings.TrimSpace(string(out))
	if styleStr == "" {
		return
	}

	fgColor := extractStyleColor(styleStr, "fg")
	if fgColor == "" {
		return
	}

	opacity := cfg.PaneHeader.DimOpacity
	if opacity <= 0 || opacity > 1 {
		opacity = 0.6
	}

	dimFg := desaturateColor(fgColor, opacity, cfg.PaneHeader.TerminalBg)
	_ = exec.Command("tmux", "set-option", "-g", "pane-border-style", "fg="+dimFg).Run()
}

func extractStyleColor(style, key string) string {
	for _, part := range strings.Split(style, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

func desaturateColor(hexColor string, opacity float64, targetBg string) string {
	hex := strings.TrimPrefix(hexColor, "#")
	if len(hex) != 6 {
		return hexColor
	}
	r, g, b := parseHex(hexColor)

	var tR, tG, tB int
	if targetBg != "" {
		tR, tG, tB = parseHex(targetBg)
	}
	if tR == 0 && tG == 0 && tB == 0 && targetBg == "" {
		lum := (r*299 + g*587 + b*114) / 1000
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
	return fmt.Sprintf("#%02x%02x%02x", clamp(dr), clamp(dg), clamp(db))
}
