package daemon

import (
	"path/filepath"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

// TestCwdIsHome covers the pure home-dir comparison: exact match (normalized),
// and the non-matches that must still be keyed normally.
func TestCwdIsHome(t *testing.T) {
	home := "/Users/b"
	cases := []struct {
		cwd, home string
		want      bool
	}{
		{"/Users/b", "/Users/b", true},
		{"/Users/b/", "/Users/b", true},      // trailing slash normalized
		{"/Users/b/./", "/Users/b", true},    // cleaned
		{"/Users/b/git/tabby", home, false},  // a project under home
		{"/Users/bob", home, false},          // prefix but different dir
		{"", home, false},                    // empty cwd
		{"/Users/b", "", false},              // unresolved home -> never home
	}
	for _, tc := range cases {
		if got := cwdIsHome(tc.cwd, tc.home); got != tc.want {
			t.Errorf("cwdIsHome(%q,%q)=%v want %v", tc.cwd, tc.home, got, tc.want)
		}
	}
}

// TestWindowNameKey_HomeIsKeyless verifies a window whose content pane runs in
// $HOME yields no key (so it is named per-window, not on the shared $HOME key),
// while a window in a sub-directory is still keyed.
func TestWindowNameKey_HomeIsKeyless(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	c := newTestCoordinator(t)

	atHome := tmux.Window{Panes: []tmux.Pane{{ID: "%1", Command: "zsh", CurrentPath: daemonHomeDir}}}
	if _, ok := c.windowNameKey(atHome); ok {
		t.Errorf("windowNameKey for a $HOME window should be keyless, got ok=true")
	}

	sub := filepath.Join(daemonHomeDir, "some-non-repo-dir")
	below := tmux.Window{Panes: []tmux.Pane{{ID: "%2", Command: "zsh", CurrentPath: sub}}}
	if _, ok := c.windowNameKey(below); !ok {
		t.Errorf("windowNameKey for a sub-directory of $HOME should be keyed, got ok=false")
	}
}

// TestProjectBasename_HomeIsEmpty verifies the summary prompt gets no project
// prefix for a $HOME window (so the LLM produces a pure task label, not "b ...").
func TestProjectBasename_HomeIsEmpty(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	atHome := tmux.Window{Panes: []tmux.Pane{{ID: "%1", Command: "zsh", CurrentPath: daemonHomeDir}}}
	if got := projectBasename(atHome, ""); got != "" {
		t.Errorf("projectBasename for a $HOME window = %q, want empty", got)
	}
	// An explicit key still wins.
	if got := projectBasename(atHome, "/Users/b/git/tabby"); got != "tabby" {
		t.Errorf("projectBasename with key = %q, want \"tabby\"", got)
	}
}
