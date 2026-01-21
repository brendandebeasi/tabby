package grouping

import (
	"testing"

	"github.com/b/tmux-tabs/pkg/config"
	"github.com/b/tmux-tabs/pkg/tmux"
)

func TestGroupWindows(t *testing.T) {
	// Windows use @tabby_group option stored in Group field
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "StudioDome"},
		{Name: "tool", Index: 1, Group: "Gunpowder"},
		{Name: "notes", Index: 2, Group: ""},  // Empty Group means Default
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: "^SD\\|"},  // Pattern kept for backwards compat but not used
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

func TestGroupWindowsFallbackToDefault(t *testing.T) {
	// Windows with unknown group name fall back to Default
	windows := []tmux.Window{
		{Name: "app", Index: 0, Group: "UnknownGroup"},
	}
	groups := []config.Group{
		{Name: "StudioDome", Pattern: ""},
		{Name: "Default", Pattern: ""},
	}

	result := GroupWindows(windows, groups)

	if len(result) != 1 {
		t.Fatalf("expected 1 group, got %d", len(result))
	}
	if result[0].Name != "Default" {
		t.Fatalf("expected Default group, got %s", result[0].Name)
	}
	if len(result[0].Windows) != 1 {
		t.Fatalf("expected 1 window in Default, got %d", len(result[0].Windows))
	}
}
