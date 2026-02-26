package main

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

// skipPanes are never dimmed and never cycled (sidebar, tabbar, pane-bar).
var skipCommands = []string{"sidebar-render", "tabbar", "pane-bar"}

// headerCommand identifies pane-header processes (dimmed with their content pane).
const headerCommand = "pane-header"

type paneInfo struct {
	id      string
	active  bool
	command string
	left    int // pane_left: used to match headers with content panes
}

func main() {
	dimOnly := len(os.Args) > 1 && os.Args[1] == "--dim-only"

	panes := listPanes()
	content := filterContent(panes)

	if !dimOnly && len(content) >= 2 {
		cyclePane(content)
	}

	applyDim()
	signalDaemon()
}

func listPanes() []paneInfo {
	out, err := exec.Command("tmux", "list-panes", "-F",
		"#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_left}").Output()
	if err != nil {
		return nil
	}
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		left, _ := strconv.Atoi(parts[3])
		panes = append(panes, paneInfo{
			id:      parts[0],
			active:  parts[1] == "1",
			command: parts[2],
			left:    left,
		})
	}
	return panes
}

func isSkip(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, s := range skipCommands {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func isHeader(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), headerCommand)
}

func isUtility(cmd string) bool {
	return isSkip(cmd) || isHeader(cmd)
}

func filterContent(panes []paneInfo) []paneInfo {
	var out []paneInfo
	for _, p := range panes {
		if !isUtility(p.command) {
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

// applyDim re-reads panes to get fresh active state after potential cycle,
// then sets per-pane background on inactive content panes AND their headers.
// Uses set-option -p (not select-pane -P) to avoid triggering after-select-pane hook.
// Sidebar/tabbar/pane-bar panes are never touched.
func applyDim() {
	cfgPath := config.DefaultConfigPath()
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return
	}

	panes := listPanes()

	if !cfg.PaneHeader.DimInactive {
		// Dim disabled — clear any leftover styles and dim flags
		for _, p := range panes {
			if !isSkip(p.command) {
				setPaneStyle(p.id, "default")
			}
			if !isUtility(p.command) {
				clearPaneDimFlag(p.id)
			}
		}
		// Reset border style to match active border (remove dim effect)
		if out, err := exec.Command("tmux", "show-options", "-gqv", "pane-active-border-style").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				_ = exec.Command("tmux", "set-option", "-g", "pane-border-style", s).Run()
			}
		}
		return
	}

	// Build map: pane_left → whether the content pane at that column is active
	colActive := map[int]bool{}
	hasActiveContent := false
	for _, p := range panes {
		if !isUtility(p.command) {
			colActive[p.left] = p.active
			if p.active {
				hasActiveContent = true
			}
		}
	}

	// If no content pane is active (utility pane has focus), don't touch styles.
	// The existing styles from the last content-pane focus are still correct.
	if !hasActiveContent {
		return
	}

	dimBG := computeDimBG(cfg.PaneHeader.TerminalBg, cfg.PaneHeader.DimOpacity)

	for _, p := range panes {
		if isSkip(p.command) {
			continue
		}

		// For headers, use their content pane's active state (matched by pane_left)
		active := p.active
		if isHeader(p.command) {
			active = colActive[p.left]
		}

		if isHeader(p.command) {
			// Headers are rendered by the daemon — don't set window-style.
			// The daemon reads @tabby_pane_dim from the content pane to decide colors.
			continue
		}

		// Content pane: set window-style AND dim flag
		if active {
			setPaneStyle(p.id, "default")
			setPaneDimFlag(p.id, false)
		} else {
			setPaneStyle(p.id, fmt.Sprintf("bg=%s", dimBG))
			setPaneDimFlag(p.id, true)
		}
}
	// Dim borders: active = full color, inactive = desaturated
	applyBorderDim(cfg)
}

// setPaneStyle sets per-pane window-style via set-option -p.
// This does NOT trigger after-select-pane hook (unlike select-pane -P).
func setPaneStyle(paneID, style string) {
	_ = exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", style).Run()
}

// setPaneDimFlag marks a content pane as dimmed or active.
// The daemon reads @tabby_pane_dim to render its header with desaturated colors.
func setPaneDimFlag(paneID string, dimmed bool) {
	val := "0"
	if dimmed {
		val = "1"
	}
	_ = exec.Command("tmux", "set-option", "-p", "-t", paneID, "@tabby_pane_dim", val).Run()
}

// clearPaneDimFlag removes the @tabby_pane_dim option from a content pane.
func clearPaneDimFlag(paneID string) {
	_ = exec.Command("tmux", "set-option", "-p", "-u", "-t", paneID, "@tabby_pane_dim").Run()
}

func computeDimBG(terminalBG string, opacity float64) string {
	if terminalBG == "" {
		terminalBG = "#1a1b26"
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

// applyBorderDim reads the global pane-active-border-style fg color and sets
// pane-border-style to a desaturated version so inactive pane borders look dimmed.
func applyBorderDim(cfg *config.Config) {
	// Read the global active border style (set by tabby.tmux / on_window_select.sh)
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

// extractStyleColor pulls a color value for a key ("fg" or "bg") from a
// tmux style string like "fg=#56949f,bg=#56949f".
func extractStyleColor(style, key string) string {
	for _, part := range strings.Split(style, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

// desaturateColor blends a hex color toward a target (terminal bg).
// opacity=1.0 = original, opacity=0.0 = full target.
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
