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

	"github.com/brendandebeasi/tabby/pkg/config"
)

// skipCommands are never dimmed and never cycled (sidebar, pane-header).
// We match against BOTH pane_current_command and pane_start_command: after
// the binary consolidation, pane_current_command reports "tabby" for every
// subcommand process (tmux reads it from the executable name, not argv[0],
// so `exec -a sidebar-renderer` doesn't help). pane_start_command still
// contains the original `render sidebar` / `render pane-header` invocation.
var skipCommands = []string{"sidebar-render", "render sidebar", "sidebar-renderer"}

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
	dimOnly := len(args) > 0 && args[0] == "--dim-only"

	panes := listPanes()
	content := filterContent(panes)

	if !dimOnly && len(content) >= 2 {
		cyclePane(content)
	}

	applyDim()
	signalDaemon()
	return 0
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

func cyclePane(content []paneInfo) {
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
	_ = exec.Command("tmux", "select-pane", "-t", content[nextIdx].id).Run()
}

func isSpawning() bool {
	out, err := exec.Command("tmux", "show-option", "-gqv", "@tabby_spawning").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

func applyDim() {
	if isSpawning() {
		return
	}

	cfgPath := config.DefaultConfigPath()
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return
	}

	panes := listPanes()

	if !cfg.PaneHeader.DimInactive {
		for _, p := range panes {
			if !isSkip(p) {
				unsetPaneStyle(p.id)
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

	dimBG := computeDimBG(cfg.PaneHeader.TerminalBg, cfg.PaneHeader.DimOpacity)

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
			unsetPaneStyle(p.id)
			setPaneDimFlag(p.id, false)
		} else {
			if dimBG == "" {
				unsetPaneStyle(p.id)
			} else {
				setPaneStyle(p.id, fmt.Sprintf("bg=%s", dimBG))
			}
			setPaneDimFlag(p.id, true)
		}
	}
	applyBorderDim(cfg)
}

func setPaneStyle(paneID, style string) {
	_ = exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", style).Run()
}

func unsetPaneStyle(paneID string) {
	_ = exec.Command("tmux", "set-option", "-p", "-u", "-t", paneID, "window-style").Run()
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

	var grayR, grayG, grayB int
	if lum >= 128 {
		grayR, grayG, grayB = 176, 176, 176
	} else {
		grayR, grayG, grayB = 64, 64, 64
	}

	inv := 1.0 - opacity
	dr := int(math.Round(float64(tbR)*opacity + float64(grayR)*inv))
	dg := int(math.Round(float64(tbG)*opacity + float64(grayG)*inv))
	db := int(math.Round(float64(tbB)*opacity + float64(grayB)*inv))
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
