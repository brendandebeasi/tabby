package tmux

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ansiEscapeRegex matches ANSI escape sequences including:
// - Standard CSI sequences: \x1b[...m (colors, styles)
// - OSC sequences: \x1b]...BEL or \x1b]...\x1b\ (titles, etc)
// - Partial/orphaned CSI: \[[0-9;]+[a-zA-Z] (missing ESC char)
var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]|\x1b\].*?(?:\x07|\x1b\\)|\[[0-9;:]+[a-zA-Z]`)

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

type Pane struct {
	ID      string
	Index   int
	Active  bool
	Command string // Current command running in pane
	Title   string // Pane title if set
}

type Window struct {
	ID          string
	Index       int
	Name        string
	Active      bool
	Activity    bool   // Window has unseen activity (monitor-activity)
	Bell        bool   // Window has triggered bell
	Silence     bool   // Window has been silent (monitor-silence)
	Last        bool   // Window was the last active window
	CustomColor string // User-defined tab color (set via @tabby_color option)
	Group       string // User-assigned group name (set via @tabby_group option)
	Panes       []Pane
}

func ListWindows() ([]Window, error) {
	cmd := exec.Command("tmux", "list-windows", "-F",
		"#{window_id}\x1f#{window_index}\x1f#{window_name}\x1f#{window_active}\x1f#{window_activity_flag}\x1f#{window_bell_flag}\x1f#{window_silence_flag}\x1f#{window_last_flag}\x1f#{@tabby_color}\x1f#{@tabby_group}")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-windows failed: %w", err)
	}

	var windows []Window
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 8 {
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		customColor := ""
		if len(parts) >= 9 {
			customColor = strings.TrimSpace(parts[8])
		}
		group := ""
		if len(parts) >= 10 {
			group = strings.TrimSpace(parts[9])
		}
		windows = append(windows, Window{
			ID:          parts[0],
			Index:       index,
			Name:        stripANSI(parts[2]),
			Active:      parts[3] == "1",
			Activity:    parts[4] == "1",
			Bell:        parts[5] == "1",
			Silence:     parts[6] == "1",
			Last:        parts[7] == "1",
			CustomColor: customColor,
			Group:       group,
		})
	}

	return windows, nil
}

// ListPanesForWindow returns all panes in a specific window
func ListPanesForWindow(windowIndex int) ([]Pane, error) {
	cmd := exec.Command("tmux", "list-panes", "-t", fmt.Sprintf(":%d", windowIndex), "-F",
		"#{pane_id}\x1f#{pane_index}\x1f#{pane_active}\x1f#{pane_current_command}\x1f#{pane_title}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var panes []Pane
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		// Skip sidebar panes
		if parts[3] == "sidebar" || parts[3] == "tabbar" {
			continue
		}
		panes = append(panes, Pane{
			ID:      parts[0],
			Index:   index,
			Active:  parts[2] == "1",
			Command: stripANSI(parts[3]),
			Title:   stripANSI(parts[4]),
		})
	}
	return panes, nil
}

// ListWindowsWithPanes returns all windows with their panes
func ListWindowsWithPanes() ([]Window, error) {
	windows, err := ListWindows()
	if err != nil {
		return nil, err
	}

	for i := range windows {
		panes, _ := ListPanesForWindow(windows[i].Index)
		windows[i].Panes = panes
	}

	return windows, nil
}
