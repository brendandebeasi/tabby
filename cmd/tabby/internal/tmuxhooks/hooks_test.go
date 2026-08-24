package tmuxhooks

import (
	"strings"
	"testing"
)

const testExe = "/plugins/tabby/bin/tabby"

// Every run-shell body must end in the guard: tmux reports a nonzero hook body
// as "'<cmd>' returned N" to every attached client, and hook steps are
// best-effort housekeeping whose status nothing consumes.
func TestEveryRunShellBodyIsGuarded(t *testing.T) {
	for _, def := range Definitions(testExe) {
		for _, segment := range strings.Split(def.Cmd, "run-shell -b '") {
			body, _, ok := strings.Cut(segment, "'")
			if !ok {
				continue // leading fragment before the first run-shell
			}
			if !strings.HasSuffix(body, okGuard) {
				t.Errorf("hook %s: body %q does not end in %q", def.Name, body, okGuard)
			}
		}
	}
}

// tabby.tmux documents after-rename-window as forbidden: the daemon renames
// windows itself while refreshing tab titles, so binding it feeds each rename
// back into another refresh.
func TestAfterRenameWindowIsNotRegistered(t *testing.T) {
	for _, def := range Definitions(testExe) {
		if def.Name == "after-rename-window" {
			t.Fatalf("after-rename-window must not be registered: %s", def.Cmd)
		}
	}
}

// A name cannot be both registered and unset — the two loops would race.
func TestRetiredNamesAreNotRegistered(t *testing.T) {
	for _, name := range Retired() {
		for _, def := range Definitions(testExe) {
			if def.Name == name {
				t.Errorf("%s is both retired and registered", name)
			}
		}
	}
}

// Without refresh-client the client keeps serving the previous window's layout,
// so mouse coordinates map to stale pane boundaries and clicks are misrouted.
func TestAfterSelectWindowRefreshesTheClient(t *testing.T) {
	cmd := cmdFor(t, "after-select-window")
	if !strings.Contains(cmd, "refresh-client -S") {
		t.Errorf("after-select-window must refresh the client, got %q", cmd)
	}
}

// A reattaching client can land on a session whose daemon has already
// idle-quit, so the steps that need one must be preceded by ensure-daemon.
func TestClientAttachedEnsuresDaemonBeforeDependentSteps(t *testing.T) {
	cmd := cmdFor(t, "client-attached")
	daemon := strings.Index(cmd, "ensure-daemon.sh")
	if daemon < 0 {
		t.Fatalf("client-attached must run ensure-daemon, got %q", cmd)
	}
	for _, dependent := range []string{"ensure-sidebar", "stabilize-client-resize"} {
		at := strings.Index(cmd, dependent)
		if at < 0 {
			t.Errorf("client-attached must run %s, got %q", dependent, cmd)
			continue
		}
		if at < daemon {
			t.Errorf("client-attached runs %s before ensure-daemon", dependent)
		}
	}
}

// Sibling scripts resolve against the plugin root, not the bin/ directory.
func TestEnsureDaemonResolvesRelativeToPluginRoot(t *testing.T) {
	if cmd := cmdFor(t, "client-attached"); !strings.Contains(cmd, "/plugins/tabby/scripts/ensure-daemon.sh") {
		t.Errorf("ensure-daemon path not resolved against plugin root: %q", cmd)
	}
}

func cmdFor(t *testing.T, name string) string {
	t.Helper()
	for _, def := range Definitions(testExe) {
		if def.Name == name {
			return def.Cmd
		}
	}
	t.Fatalf("hook %s not registered", name)
	return ""
}
