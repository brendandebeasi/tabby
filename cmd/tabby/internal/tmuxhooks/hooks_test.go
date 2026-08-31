package tmuxhooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testExe = "/plugins/tabby/bin/tabby"

const jobPrefix = `if-shell -b "`

// Every job body must end in the guard, and must be an if-shell rather than a
// run-shell: tmux reports a nonzero *run-shell* body as "'<cmd>' returned N" to
// every attached client, while if-shell silently takes its empty else branch.
// Hook steps are best-effort housekeeping whose status nothing consumes.
func TestEveryJobBodyIsGuarded(t *testing.T) {
	for _, def := range Definitions(testExe) {
		if strings.Contains(def.Cmd, "run-shell") {
			t.Errorf("hook %s: run-shell reports failures to the client, use if-shell: %q", def.Name, def.Cmd)
		}
		rest := def.Cmd
		for {
			at := strings.Index(rest, jobPrefix)
			if at < 0 {
				break
			}
			rest = rest[at+len(jobPrefix):]
			body, after, ok := strings.Cut(rest, `"`)
			if !ok {
				t.Fatalf("hook %s: unterminated body in %q", def.Name, def.Cmd)
			}
			if !strings.HasSuffix(body, okGuard) {
				t.Errorf("hook %s: body %q does not end in %q", def.Name, body, okGuard)
			}
			rest = after
		}
	}
}

// Each backgrounded body is a separate tmux job, costing a socketpair in the
// server and a forked shell. Hooks fire once per session a window is linked
// into, so in an 8-session grouped set a three-body hook is 24 socketpairs per
// window switch — enough to exhaust the server's default 256-fd soft limit, at
// which point socketpair() fails and tmux drops the command outright. Steps
// belong in one body.
func TestEachHookRunsExactlyOneJob(t *testing.T) {
	for _, def := range Definitions(testExe) {
		if n := strings.Count(def.Cmd, jobPrefix); n != 1 {
			t.Errorf("hook %s: want 1 job, got %d: %q", def.Name, n, def.Cmd)
		}
	}
}

// Likewise for process spawns: steps that are tabby subcommands must go through
// `tabby batch` so one fork/exec covers them all.
func TestTabbySubcommandsAreBatched(t *testing.T) {
	for _, def := range Definitions(testExe) {
		if n := strings.Count(def.Cmd, testExe+" "); n > 1 {
			t.Errorf("hook %s: %d separate tabby execs, batch them: %q", def.Name, n, def.Cmd)
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
		// The mute gate's own #{session_id} is exempt, and only that one. It
		// sits in an -F condition, which the server expands itself to decide
		// whether to run the hook at all — no shell ever sees it. The rule
		// applies to the body, so the gate comes out before scanning.
		body := strings.ReplaceAll(def.Cmd, muteGate, "")
		for _, bad := range []string{`"#{session_id}"`, ` #{session_id} `} {
			if strings.Contains(body, bad) {
				t.Errorf("hook %s: session id must be single-quoted at the shell level, got %s in %q",
					def.Name, bad, def.Cmd)
			}
		}
		if strings.Contains(body, "#{session_id}") && !strings.Contains(body, `'#{session_id}'`) {
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
//
// Textual order only implies execution order because these steps share one job
// and so run sequentially in one shell. As separate backgrounded bodies they
// were concurrent jobs with no ordering at all, and this test's premise was
// false — TestEachHookRunsExactlyOneJob is what keeps it true.
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

// Every job body must sit behind the mute gate, except the selection hooks:
// the gate is for the window-list churn a daemon batch replays to every session
// in a grouped set, and a selection fires one hook in one session. Gating them
// is also visible — the daemon does window-list work while servicing a sidebar
// click, which closes the gate, and the selection's own refresh-client never
// runs, so the window you clicked draws the previous window's contents.
func TestEveryJobIsMuteGatedExceptSelections(t *testing.T) {
	ungated := map[string]bool{"after-select-window": true, "after-select-pane": true}
	gate := "if-shell -F '" + muteGate + "'"
	for _, def := range Definitions(testExe) {
		gates := strings.Count(def.Cmd, gate)
		if ungated[def.Name] {
			if gates != 0 {
				t.Errorf("hook %s must not be mute-gated: %q", def.Name, def.Cmd)
			}
			continue
		}
		jobs := strings.Count(def.Cmd, jobPrefix)
		if gates != jobs {
			t.Errorf("hook %s: %d job bodies but %d mute gates: %q", def.Name, jobs, gates, def.Cmd)
		}
		if jobs > 0 && !strings.HasPrefix(def.Cmd, gate) {
			t.Errorf("hook %s: first job is not mute-gated: %q", def.Name, def.Cmd)
		}
	}
}

// The gate must compare @tabby_mute against #{session_id}, not read it as a
// boolean. A boolean gate is a real bug and not a visible one: options are
// per-server and daemons are per-session, so one daemon muting its own batch
// silences every peer daemon's hooks too and their window-list models go stale.
// Assert the exact scoped form — anything that merely mentions the option would
// let the boolean version back in.
func TestMuteGateIsScopedToTheMutingSession(t *testing.T) {
	want := "#{?#{==:#{" + MuteOption + "},#{session_id}},0,1}"
	if muteGate != want {
		t.Errorf("muteGate = %q, want %q", muteGate, want)
	}
	if !strings.Contains(muteGate, "#{session_id}") {
		t.Error("muteGate must compare against #{session_id}; a server-wide flag mutes peer daemons")
	}
}

// Both of the no-daemon values have to read as "run", or a server that has never
// started a daemon would have every hook muted. Neither an unset option (empty)
// nor the cleared "0" that ClearMute writes can equal a tmux session id, which
// is always `$N` — so the gate's comparison fails, and the body runs. This
// pins the cleared value to something that cannot collide.
func TestClearedMuteValueCannotMatchASessionID(t *testing.T) {
	if strings.HasPrefix(clearedMuteValue, "$") {
		t.Errorf("cleared value %q looks like a session id; it would mute that session forever", clearedMuteValue)
	}
}

// The gate is written twice — here, for the hooks Go registers, and as
// MUTE_GATE in tabby.tmux for the ones the config registers. Both sets guard
// the same @tabby_mute option, so if the two spellings drift the config's hooks
// read a gate the daemon never closes (or, worse, one it closes and never
// opens). `set-hook -g` replaces rather than merges and the two halves are
// registered by different processes, so drift is silent at runtime; this is the
// only place it can be caught.
func TestMuteGateMatchesTabbyTmux(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tabby.tmux"))
	if err != nil {
		t.Fatalf("read tabby.tmux: %v", err)
	}
	const decl = "MUTE_GATE='"
	i := strings.Index(string(src), decl)
	if i < 0 {
		t.Fatal("tabby.tmux no longer declares MUTE_GATE; the config hooks are ungated")
	}
	rest := string(src)[i+len(decl):]
	got := rest[:strings.Index(rest, "'")]
	if got != muteGate {
		t.Errorf("gate drift:\n  hooks.go:   %s\n  tabby.tmux: %s", muteGate, got)
	}
}
