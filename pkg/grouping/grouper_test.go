package grouping

import (
	"testing"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/tmux"
)

func TestGroupWindows(t *testing.T) {
	windows := []tmux.Window{
		{Name: "SD|app", Index: 0},
		{Name: "GP|tool", Index: 1},
		{Name: "notes", Index: 2},
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: "^SD\\|"},
		{Name: "Gunpowder", Pattern: "^GP\\|"},
		{Name: "Default", Pattern: ".*"},
	}

	result := GroupWindows(windows, groups)

	counts := map[string]int{}
	for _, group := range result {
		counts[group.Name] = len(group.Windows)
	}

	if counts["StudioDome"] != 1 {
		t.Fatalf("expected StudioDome count 1, got %d", counts["StudioDome"])
	}
	if counts["Gunpowder"] != 1 {
		t.Fatalf("expected Gunpowder count 1, got %d", counts["Gunpowder"])
	}
	if counts["Default"] != 1 {
		t.Fatalf("expected Default count 1, got %d", counts["Default"])
	}
}
