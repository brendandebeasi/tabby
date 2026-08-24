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
	const prefix = "run-shell -b "
	for _, def := range Definitions(testExe) {
		rest := def.Cmd
		for {
			at := strings.Index(rest, prefix)
			if at < 0 {
				break
			}
			// The body may be quoted either way: bgQuoted uses double quotes so
			// the body itself can contain the single quotes a session id needs.
			rest = rest[at+len(prefix):]
			quote := rest[:1]
			body, after, ok := strings.Cut(rest[1:], quote)
			if !ok {
				t.Fatalf("hook %s: unterminated %s-quoted body in %q", def.Name, quote, def.Cmd)
			}
			if !strings.HasSuffix(body, okGuard) {
				t.Errorf("hook %s: body %q does not end in %q", def.Name, body, okGuard)
			}
			rest = after
		}
	}
}

// tmux expands #{session_id} to text like `$246`. The shell that runs the hook
// body then reads that as a positional parameter and substitutes it away — to
// the empty string, or for session $0 to the shell's own name — and every step
// taking a session id silently no-ops (ensureSidebar returns immediately on an
// empty id). Double quotes do not stop the expansion; only single quotes at the
// shell level do, which is what bgQuoted exists to allow.
func TestSessionIDFormatsAreSingleQuotedForTheShell(t *testing.T) {
	for _, def := range Definitions(testExe) {
		for _, bad := range []string{`"#{session_id}"`, ` #{session_id} `} {
			if strings.Contains(def.Cmd, bad) {
				t.Errorf("hook %s: session id must be single-quoted at the shell level, got %s in %q",
					def.Name, bad, def.Cmd)
			}
		}
		if strings.Contains(def.Cmd, "#{session_id}") && !strings.Contains(def.Cmd, `'#{session_id}'`) {
			t.Errorf("hook %s: uses #{session_id} without single quotes: %q", def.Name, def.Cmd)
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
