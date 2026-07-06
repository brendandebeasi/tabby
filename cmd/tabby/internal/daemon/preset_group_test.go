package daemon

import (
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// TestEffectiveWindowMarker covers marker inheritance: a window's own marker wins,
// otherwise it falls back to the group's marker, and whitespace-only values are
// treated as empty.
func TestEffectiveWindowMarker(t *testing.T) {
	cases := []struct {
		winIcon, groupIcon, want string
	}{
		{"🚀", "💥", "🚀"},   // own marker wins
		{"", "💥", "💥"},    // inherit group marker
		{"", "", ""},      // nothing to show
		{"  ", "💥", "💥"},  // blank own marker -> inherit
		{"🚀", "", "🚀"},    // own marker, no group marker
		{" 🚀 ", "💥", "🚀"}, // trimmed
	}
	for _, tc := range cases {
		if got := effectiveWindowMarker(tc.winIcon, tc.groupIcon); got != tc.want {
			t.Errorf("effectiveWindowMarker(%q,%q)=%q want %q", tc.winIcon, tc.groupIcon, got, tc.want)
		}
	}
}

// TestExpandWorkingDir covers tilde expansion + normalization used for matching a
// window's cwd against a group's working_dir.
func TestExpandWorkingDir(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"~", daemonHomeDir},
		{"~/git/gunpowder", filepath.Join(daemonHomeDir, "git", "gunpowder")},
		{"/abs/path/", "/abs/path"},          // cleaned trailing slash
		{"/abs/path/./sub", "/abs/path/sub"}, // cleaned
	}
	for _, tc := range cases {
		if got := expandWorkingDir(tc.in); got != tc.want {
			t.Errorf("expandWorkingDir(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

// TestPresetGroupForWindow verifies a window is matched to the configured group
// whose working_dir contains its cwd — including subdirectories and nested (most
// specific wins) working_dirs — while $HOME, unmatched dirs, and remote windows
// match nothing.
func TestPresetGroupForWindow(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	c := newTestCoordinator(t)
	gp := filepath.Join(daemonHomeDir, "git", "gunpowder")
	sd := filepath.Join(daemonHomeDir, "git", "studiodome")
	nested := filepath.Join(gp, "arsenal") // a deeper group working_dir under gp
	c.config.Groups = []config.Group{
		{Name: "Gunpowder", WorkingDir: "~/git/gunpowder"},
		{Name: "Arsenal", WorkingDir: "~/git/gunpowder/arsenal"},
		{Name: "StudioDome", WorkingDir: "~/git/studiodome"},
		{Name: "NoDir"}, // group without a working_dir never matches
		{Name: "Default", WorkingDir: "~"},
	}

	// Prime gitTopCache so firstPaneCWD-derived paths don't fork git and the cwd
	// is used verbatim (windowNameKey is not involved here, but keep paths clean).
	win := func(path string) tmux.Window {
		return tmux.Window{Panes: []tmux.Pane{{ID: "%1", Command: "zsh", CurrentPath: path}}}
	}

	cases := []struct {
		name string
		w    tmux.Window
		want string
	}{
		{"exact working_dir", win(gp), "Gunpowder"},
		{"subdir of working_dir", win(filepath.Join(gp, "cmd", "foo")), "Gunpowder"},
		{"nested most-specific wins", win(nested), "Arsenal"},
		{"other group", win(sd), "StudioDome"},
		{"home is keyless", win(daemonHomeDir), ""},
		{"unmatched dir", win(filepath.Join(daemonHomeDir, "elsewhere")), ""},
		{"remote window skipped", tmux.Window{RemoteHost: "box", Panes: []tmux.Pane{{ID: "%1", CurrentPath: gp}}}, ""},
		{"no cwd", tmux.Window{}, ""},
	}
	for _, tc := range cases {
		if got := c.presetGroupForWindow(tc.w); got != tc.want {
			t.Errorf("%s: presetGroupForWindow=%q want %q", tc.name, got, tc.want)
		}
	}
}

// TestPresetGroupSubstringNotPrefix guards against a false match when one dir is a
// string prefix of another but not a path prefix (e.g. ~/git/gun vs ~/git/gunpowder).
func TestPresetGroupSubstringNotPrefix(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	c := newTestCoordinator(t)
	c.config.Groups = []config.Group{
		{Name: "Gun", WorkingDir: "~/git/gun"},
		{Name: "Default", WorkingDir: "~"},
	}
	w := tmux.Window{Panes: []tmux.Pane{{ID: "%1", CurrentPath: filepath.Join(daemonHomeDir, "git", "gunpowder")}}}
	if got := c.presetGroupForWindow(w); got != "" {
		t.Errorf("presetGroupForWindow for ~/git/gunpowder matched %q; must not match ~/git/gun", got)
	}
}
