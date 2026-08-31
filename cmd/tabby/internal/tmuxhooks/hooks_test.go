package tmuxhooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
		// A gate's own #{session_id} is exempt, and only that one. It sits in
		// an -F condition, which the server expands itself to decide whether to
		// run the hook at all — no shell ever sees it. The rule applies to the
		// body, so the gates come out before scanning.
		body := strings.ReplaceAll(def.Cmd, muteGate, "")
		body = strings.ReplaceAll(body, daemonGate, "")
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
//
// daemonGate counts as mute-gated: it is muteGate's condition with a second
// clause ANDed onto it, so a hook behind it is silenced everywhere muteGate
// would have silenced it and in daemonless sessions besides.
func TestEveryJobIsMuteGatedExceptSelections(t *testing.T) {
	ungated := map[string]bool{"after-select-window": true, "after-select-pane": true}
	muted := "if-shell -F '" + muteGate + "'"
	daemoned := "if-shell -F '" + daemonGate + "'"
	for _, def := range Definitions(testExe) {
		gates := strings.Count(def.Cmd, muted) + strings.Count(def.Cmd, daemoned)
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
		if jobs > 0 && !strings.HasPrefix(def.Cmd, muted) && !strings.HasPrefix(def.Cmd, daemoned) {
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

func TestDaemonGateMatchesTabbyTmux(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tabby.tmux"))
	if err != nil {
		t.Fatalf("read tabby.tmux: %v", err)
	}
	const decl = "DAEMON_GATE='"
	i := strings.Index(string(src), decl)
	if i < 0 {
		t.Fatal("tabby.tmux no longer declares DAEMON_GATE; its window and pane hooks fire in daemonless sessions again")
	}
	rest := string(src)[i+len(decl):]
	got := rest[:strings.Index(rest, "'")]
	if got != daemonGate {
		t.Errorf("gate drift:\n  hooks.go:   %s\n  tabby.tmux: %s", daemonGate, got)
	}
}

// TestDaemonGatedHooksInTabbyTmux pins which of tabby.tmux's hooks sit behind
// which gate. Moving one across this line is a behaviour change, not a tidy-up:
// daemon_gated_hook on session-created or client-session-changed would stop the
// only two hooks that can START a daemon from ever running without one.
func TestDaemonGatedHooksInTabbyTmux(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tabby.tmux"))
	if err != nil {
		t.Fatalf("read tabby.tmux: %v", err)
	}
	want := map[string]string{
		"window-linked":          "daemon_gated_hook",
		"window-unlinked":        "daemon_gated_hook",
		"after-new-window":       "daemon_gated_hook",
		"after-split-window":     "daemon_gated_hook",
		"after-kill-pane":        "daemon_gated_hook",
		"session-created":        "gated_hook",
		"client-session-changed": "gated_hook",
	}
	for hook, fn := range want {
		if !strings.Contains(string(src), fn+" "+hook+" ") {
			t.Errorf("%s is no longer installed with %s", hook, fn)
		}
	}
}

// TestDaemonGateOnlyOnDaemonOnlyHooks guards the same line on the Go side. The
// client-* hooks fire in exactly one session, which may be a daemonless peer
// whose request dialDaemon hands to a peer daemon, and client-attached runs
// ensure-daemon.sh — gating it on a daemon existing would be a deadlock.
func TestDaemonGateOnlyOnDaemonOnlyHooks(t *testing.T) {
	want := map[string]bool{
		"after-resize-pane":   true,
		"after-resize-window": true,
		"client-resized":      false,
		"client-attached":     false,
		"after-select-window": false,
	}
	for _, d := range Definitions("/opt/tabby/bin/tabby") {
		gated, known := want[d.Name]
		if !known {
			t.Errorf("hook %q is not classified; decide which gate it belongs behind", d.Name)
			continue
		}
		if got := strings.Contains(d.Cmd, daemonGate); got != gated {
			t.Errorf("hook %q behind daemonGate = %v, want %v", d.Name, got, gated)
		}
	}
}

// The gates are tmux format strings, and nothing else in the build ever asks
// tmux whether they mean what the comments claim. A typo inside #{&&:...} does
// not fail to compile, it silently evaluates to the wrong branch — and the
// failure mode of the daemon gate specifically is that every window and pane
// hook stops firing everywhere, which looks like tabby having frozen. So this
// runs both gates through a real server on its own socket.
func TestGatesEvaluateInRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := "tabbygate" + strconv.Itoa(os.Getpid())
	env := append(os.Environ(), "TMUX_TMPDIR="+tmuxTmpDir(t))
	tmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("tmux %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	cmd := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "gate", "-x", "80", "-y", "24")
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot start a tmux server here: %v", err)
	}
	t.Cleanup(func() {
		c := exec.Command("tmux", "-L", socket, "kill-server")
		c.Env = env
		c.Run()
	})
	self := tmux("display-message", "-p", "#{session_id}")

	for _, tc := range []struct {
		name             string
		daemon, mute     string // "" means leave the option unset
		wantMute, wantDm string
	}{
		{"no daemon, no mute", "", "", "1", "0"},
		{"daemon, no mute", "1", "", "1", "1"},
		{"daemon, muted by us", "1", self, "0", "0"},
		{"daemon, muted by a peer", "1", "$999", "1", "1"},
		{"daemon, mute cleared", "1", clearedMuteValue, "1", "1"},
		// The case the whole change is for: a grouped peer with no daemon of
		// its own still fires every window hook today.
		{"no daemon, mute cleared", "", clearedMuteValue, "1", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for opt, v := range map[string]string{DaemonOption: tc.daemon, MuteOption: tc.mute} {
				if v == "" {
					tmux("set-option", "-t", "gate", "-qu", opt)
				} else {
					tmux("set-option", "-t", "gate", opt, v)
				}
			}
			if got := tmux("display-message", "-p", "-t", "gate", muteGate); got != tc.wantMute {
				t.Errorf("muteGate = %s, want %s", got, tc.wantMute)
			}
			if got := tmux("display-message", "-p", "-t", "gate", daemonGate); got != tc.wantDm {
				t.Errorf("daemonGate = %s, want %s", got, tc.wantDm)
			}
		})
	}
}

// The daemon's flag has to be a SESSION option. As a server option it would say
// only "some daemon is running somewhere", which in a grouped set is true the
// moment any one session starts one — and the gate would be open for all nine
// again, which is the entire thing it exists to stop.
func TestDaemonOptionDoesNotLeakToOtherSessions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := "tabbyscope" + strconv.Itoa(os.Getpid())
	env := append(os.Environ(), "TMUX_TMPDIR="+tmuxTmpDir(t))
	tmux := func(args ...string) (string, error) {
		cmd := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		cmd.Env = env
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := tmux("-f", "/dev/null", "new-session", "-d", "-s", "withd", "-x", "80", "-y", "24"); err != nil {
		t.Skipf("cannot start a tmux server here: %v", err)
	}
	t.Cleanup(func() { tmux("kill-server") })
	// grouped shares the window list with withd, the way the peers in a real
	// grouped set do, and that is exactly where the wasted fires come from.
	if _, err := tmux("new-session", "-d", "-s", "grouped", "-t", "withd"); err != nil {
		t.Fatalf("new grouped session: %v", err)
	}
	if _, err := tmux("set-option", "-t", "withd", DaemonOption, "1"); err != nil {
		t.Fatalf("set %s: %v", DaemonOption, err)
	}
	for _, tc := range []struct{ session, want string }{
		{"withd", "1"},
		{"grouped", "0"},
	} {
		got, err := tmux("display-message", "-p", "-t", tc.session, daemonGate)
		if err != nil {
			t.Fatalf("display-message -t %s: %v", tc.session, err)
		}
		if got != tc.want {
			t.Errorf("daemonGate in %s = %s, want %s", tc.session, got, tc.want)
		}
	}
}

// tmuxTmpDir is t.TempDir() for a directory a tmux socket can live in. A unix
// socket path is capped at 104 bytes on darwin and t.TempDir() spends most of
// that on /var/folders/... plus the test's own name, so the server never starts
// and the test silently skips. /tmp keeps the prefix to five characters.
func tmuxTmpDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tbg")
	if err != nil {
		t.Skipf("no temp dir for a tmux socket: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
