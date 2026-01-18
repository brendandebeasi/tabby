package tmux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Window struct {
	ID       string
	Index    int
	Name     string
	Active   bool
	Activity bool // Window has unseen activity (monitor-activity)
	Bell     bool // Window has triggered bell
	Silence  bool // Window has been silent (monitor-silence)
	Last     bool // Window was the last active window
}

func ListWindows() ([]Window, error) {
	cmd := exec.Command("tmux", "list-windows", "-F",
		"#{window_id}\x1f#{window_index}\x1f#{window_name}\x1f#{window_active}\x1f#{window_activity_flag}\x1f#{window_bell_flag}\x1f#{window_silence_flag}\x1f#{window_last_flag}")
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
		windows = append(windows, Window{
			ID:       parts[0],
			Index:    index,
			Name:     parts[2],
			Active:   parts[3] == "1",
			Activity: parts[4] == "1",
			Bell:     parts[5] == "1",
			Silence:  parts[6] == "1",
			Last:     parts[7] == "1",
		})
	}

	return windows, nil
}
