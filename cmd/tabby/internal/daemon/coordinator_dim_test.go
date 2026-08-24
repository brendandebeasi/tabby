package daemon

import (
	"testing"
)

func TestClampColorByte(t *testing.T) {
	tests := []struct {
		input    int
		expected int
	}{
		{0, 0},
		{128, 128},
		{255, 255},
		{-1, 0},
		{-100, 0},
		{256, 255},
		{999, 255},
	}
	for _, tt := range tests {
		if got := clampColorByte(tt.input); got != tt.expected {
			t.Errorf("clampColorByte(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestComputeDimBG(t *testing.T) {
	tests := []struct {
		name     string
		termBG   string
		opacity  float64
		expected string
	}{
		{"empty bg returns empty", "", 0.9, ""},
		{"invalid hex returns empty", "#xyz", 0.9, ""},
		{"dark bg shifts toward black", "#1a1a1a", 0.9, "#171717"},
		{"light bg shifts toward white", "#f0f0f0", 0.9, "#f2f2f2"},
		{"full opacity returns original", "#1a1a1a", 1.0, "#1a1a1a"},
		{"zero opacity dark returns black", "#1a1a1a", 0.0, "#000000"},
		{"mid opacity dark", "#000000", 0.5, "#000000"},
		{"mid opacity light", "#ffffff", 0.5, "#ffffff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDimBG(tt.termBG, tt.opacity)
			if got != tt.expected {
				t.Errorf("computeDimBG(%q, %v) = %q, want %q", tt.termBG, tt.opacity, got, tt.expected)
			}
		})
	}
}

func TestExtractStyleColor(t *testing.T) {
	tests := []struct {
		name     string
		style    string
		key      string
		expected string
	}{
		{"fg from simple", "fg=#56949f", "fg", "#56949f"},
		{"bg from simple", "bg=#1a1a1a", "bg", "#1a1a1a"},
		{"fg from compound", "fg=#56949f,bg=#1a1a1a", "fg", "#56949f"},
		{"bg from compound", "fg=#56949f,bg=#1a1a1a", "bg", "#1a1a1a"},
		{"missing key", "fg=#56949f", "bg", ""},
		{"empty style", "", "fg", ""},
		{"spaces in compound", "fg=#aabbcc , bg=#112233", "bg", "#112233"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStyleColor(tt.style, tt.key)
			if got != tt.expected {
				t.Errorf("extractStyleColor(%q, %q) = %q, want %q", tt.style, tt.key, got, tt.expected)
			}
		})
	}
}

func TestIsDimSkip(t *testing.T) {
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"sidebar-renderer", true},
		{"sidebar-render", true},
		{"bash", false},
		{"zsh", false},
		{"pane-header", false},
	}
	for _, tt := range tests {
		if got := isDimSkip(tt.cmd); got != tt.expected {
			t.Errorf("isDimSkip(%q) = %v, want %v", tt.cmd, got, tt.expected)
		}
	}
}

func TestIsDimHeader(t *testing.T) {
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"pane-header", true},
		{"Pane-Header", true},
		{"bash", false},
		{"sidebar-renderer", false},
	}
	for _, tt := range tests {
		if got := isDimHeader(tt.cmd); got != tt.expected {
			t.Errorf("isDimHeader(%q) = %v, want %v", tt.cmd, got, tt.expected)
		}
	}
}

func TestIsDimUtility(t *testing.T) {
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"sidebar-renderer", true},
		{"pane-header", true},
		{"bash", false},
		{"zsh", false},
		{"vim", false},
	}
	for _, tt := range tests {
		if got := isDimUtility(tt.cmd); got != tt.expected {
			t.Errorf("isDimUtility(%q) = %v, want %v", tt.cmd, got, tt.expected)
		}
	}
}

// A daemon must never style panes in another session. The active window id it
// receives is derived from the most recently active client on the SERVER, so
// with two sessions each running a daemon, both are handed whichever window the
// user is focused in. Without this filter the foreign daemon cannot resolve a
// tint for that window and unsets window-style / @tabby_tint_bg on panes the
// owning daemon just painted — the background flickers on a ~5s cycle.
func TestPanesInSession(t *testing.T) {
	panes := []dimPaneInfo{
		{id: "%1", sessionID: "$0"},
		{id: "%2", sessionID: "$23"},
		{id: "%3", sessionID: ""},
		{id: "%4", sessionID: "$0"},
	}

	got := panesInSession(append([]dimPaneInfo(nil), panes...), "$0")
	var ids []string
	for _, p := range got {
		ids = append(ids, p.id)
	}
	want := []string{"%1", "%3", "%4"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}

	// No session scope => no filtering.
	if all := panesInSession(append([]dimPaneInfo(nil), panes...), ""); len(all) != 4 {
		t.Fatalf("unscoped filter dropped panes: got %d, want 4", len(all))
	}
}
