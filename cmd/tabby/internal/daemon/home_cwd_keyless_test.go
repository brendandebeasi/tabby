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

// TestWindowDirCode_HomeIsEmpty verifies a $HOME window gets no deterministic
// project code (so composeTabBaseName shows the live summary alone, never a
// "b ..." derived from the home dir), while a sub-directory resolves to its
// basename code.
func TestWindowDirCode_HomeIsEmpty(t *testing.T) {
	if daemonHomeDir == "" {
		t.Skip("home dir unresolved in this environment")
	}
	c := newTestCoordinator(t)

	atHome := tmux.Window{Panes: []tmux.Pane{{ID: "%1", Command: "zsh", CurrentPath: daemonHomeDir}}}
	if got := c.windowProjectBasename(atHome); got != "" {
		t.Errorf("windowProjectBasename for a $HOME window = %q, want empty", got)
	}
	if got := c.windowDirCode(atHome); got != "" {
		t.Errorf("windowDirCode for a $HOME window = %q, want empty", got)
	}

	// A non-repo sub-directory resolves to its basename (prime gitTopCache so we
	// don't fork git): "some-project" -> auto code "SP" (multi-word initials).
	sub := normalizeCWD(filepath.Join(daemonHomeDir, "some-project"))
	c.gitTopMu.Lock()
	c.gitTopCache[sub] = ""
	c.gitTopMu.Unlock()
	below := tmux.Window{Panes: []tmux.Pane{{ID: "%2", Command: "zsh", CurrentPath: sub}}}
	if got := c.windowProjectBasename(below); got != "some-project" {
		t.Errorf("windowProjectBasename for a sub-dir = %q, want \"some-project\"", got)
	}
	if got := c.windowDirCode(below); got != "SP" {
		t.Errorf("windowDirCode for some-project = %q, want \"SP\"", got)
	}
}
