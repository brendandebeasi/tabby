package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestGetWindows_InitiallyEmpty(t *testing.T) {
	c := newTestCoordinator(t)
	wins := c.GetWindows()
	assert.Empty(t, wins)
}

func TestGetWindows_ReturnsCopy(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()

	got := c.GetWindows()
	assert.Equal(t, 1, len(got))

	got[0].Name = "mutated"
	assert.Equal(t, "W1", c.GetWindows()[0].Name, "modifying returned slice must not affect internal state")
}

func TestGetWindows_MultipleWindows(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("alpha", true, "bash"),
		testWindow("beta", false, "vim", "htop"),
	}
	c.stateMu.Unlock()

	got := c.GetWindows()
	assert.Equal(t, 2, len(got))
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "beta", got[1].Name)
	assert.Equal(t, 2, len(got[1].Panes))
}

func TestGetWindowsHash_ConsistentForSameState(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()

	h1 := c.GetWindowsHash()
	h2 := c.GetWindowsHash()
	assert.Equal(t, h1, h2)
}

func TestGetWindowsHash_ChangesWithWindowState(t *testing.T) {
	c := newTestCoordinator(t)

	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("W1", true, "bash")}
	c.stateMu.Unlock()
	h1 := c.GetWindowsHash()

	c.stateMu.Lock()
	c.windows = []tmux.Window{
		testWindow("W1", true, "bash"),
		testWindow("W2", false, "vim"),
	}
	c.stateMu.Unlock()
	h2 := c.GetWindowsHash()

	assert.NotEqual(t, h1, h2)
}

func TestGetWindowsHash_EmptyIsStable(t *testing.T) {
	c := newTestCoordinator(t)
	assert.Equal(t, c.GetWindowsHash(), c.GetWindowsHash())
}

func TestIncrementSpinner_ReturnsFalseWhenNoActivity(t *testing.T) {
	c := newTestCoordinator(t)
	c.stateMu.Lock()
	c.windows = []tmux.Window{testWindow("idle", false, "bash")}
	c.stateMu.Unlock()

	before := c.spinnerFrame
	visible, slowFrame := c.IncrementSpinner()
	assert.False(t, visible)
	assert.Equal(t, before+1, c.spinnerFrame)
	assert.Equal(t, c.spinnerFrame/2, slowFrame)
}

func TestIncrementSpinner_ReturnsTrueWhenWindowBusy(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("busy", true, "make")
	w.Busy = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	visible, _ := c.IncrementSpinner()
	assert.True(t, visible)
}

// Bell is a sticky badge, not a frame-by-frame spinner — IncrementSpinner
// must NOT report it as visible animation, otherwise the animation tick
// would render at 10 Hz forever after any beep until the user acks it.
func TestIncrementSpinner_IgnoresWindowBell(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("bell", false, "bash")
	w.Bell = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	visible, _ := c.IncrementSpinner()
	assert.False(t, visible)
}

// Activity is also a sticky badge (tmux's window-activity flag persists
// until the user visits the window). Same rationale as the Bell test.
func TestIncrementSpinner_IgnoresWindowActivity(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("activity", false, "bash")
	w.Activity = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	visible, _ := c.IncrementSpinner()
	assert.False(t, visible)
}

func TestIncrementSpinner_ReturnsTrueWhenPaneAIBusy(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("ai", true, "claude")
	w.Panes[0].AIBusy = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	visible, _ := c.IncrementSpinner()
	assert.True(t, visible)
}

func TestIncrementSpinner_ReturnsTrueWhenPaneAIInput(t *testing.T) {
	c := newTestCoordinator(t)
	w := testWindow("ai", true, "claude")
	w.Panes[0].AIInput = true
	c.stateMu.Lock()
	c.windows = []tmux.Window{w}
	c.stateMu.Unlock()

	visible, _ := c.IncrementSpinner()
	assert.True(t, visible)
}

func TestIncrementSpinner_IncrementsMonotonically(t *testing.T) {
	c := newTestCoordinator(t)
	for i := 1; i <= 5; i++ {
		c.IncrementSpinner()
		assert.Equal(t, i, c.spinnerFrame)
	}
}

func TestGetCWDColorMapping_MissingReturnsNotFound(t *testing.T) {
	c := newTestCoordinator(t)
	_, ok := c.getCWDColorMapping("/some/path")
	assert.False(t, ok)
}

func TestGetCWDColorMapping_EmptyCWDReturnsFalse(t *testing.T) {
	c := newTestCoordinator(t)
	_, ok := c.getCWDColorMapping("")
	assert.False(t, ok)
}

// seedCWDMapping writes a raw per-directory record directly, bypassing the
// setWindowColor/setWindowIcon capture path. Color/marker are remembered per
// directory as a "last used" appearance that seeds a future NEW window in the
// same dir (see captureCWDAppearance / seedWindowAppearance), never a per-refresh
// repaint. These helpers let the tests below drive that record directly.
func seedCWDMapping(t *testing.T, c *Coordinator, cwd string, m CWDColorMapping) {
	t.Helper()
	c.cwdColorsMu.Lock()
	c.cwdColors[normalizeCWD(cwd)] = m
	c.cwdColorsMu.Unlock()
}

func TestCaptureCWDIdentity_StoresGroupPinned(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/home/user/project", "  Work  ", true)
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "Work", m.Group, "group should be trimmed and stored")
	assert.True(t, m.Pinned)
}

func TestCaptureCWDIdentity_EmptyGroupUnpinnedIsNoOp(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	// Nothing to persist: no group, not pinned -> no entry created.
	c.captureCWDIdentity("/tmp/p", "   ", false)
	_, ok := c.getCWDColorMapping("/tmp/p")
	assert.False(t, ok, "an empty group + unpinned carries nothing to capture")
}

func TestCaptureCWDIdentity_PreservesColorIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	seedCWDMapping(t, c, "/tmp/x", CWDColorMapping{Color: "#aabbcc", Icon: "🌟"})
	c.captureCWDIdentity("/tmp/x", "Infra", true)

	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok)
	assert.Equal(t, "#aabbcc", m.Color, "capture must not disturb a legacy color")
	assert.Equal(t, "🌟", m.Icon, "capture must not disturb a legacy icon")
	assert.Equal(t, "Infra", m.Group)
	assert.True(t, m.Pinned)
}

func TestClearCWDIdentity_RemovesGroupPinnedKeepsColorIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	seedCWDMapping(t, c, "/tmp/x", CWDColorMapping{Color: "#aabbcc"})
	c.captureCWDIdentity("/tmp/x", "Infra", true)

	c.clearCWDIdentity("/tmp/x")
	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok, "a legacy color mapping should survive a group/pinned clear")
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "", m.Group)
	assert.False(t, m.Pinned)
}

func TestClearCWDIdentity_DeletesEntryWhenNothingRemains(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/tmp/only-group", "Infra", false)
	_, ok := c.getCWDColorMapping("/tmp/only-group")
	assert.True(t, ok)

	c.clearCWDIdentity("/tmp/only-group")
	_, ok = c.getCWDColorMapping("/tmp/only-group")
	assert.False(t, ok, "entry should be removed when no color/icon/group/pinned remains")
}

func TestCaptureCWDAppearance_StoresColorIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDAppearance("/home/user/project", "  #112233  ", "  🚀  ")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "#112233", m.Color, "color should be trimmed and stored")
	assert.Equal(t, "🚀", m.Icon, "icon should be trimmed and stored")
}

func TestCaptureCWDAppearance_PreservesGroupPinned(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	seedCWDMapping(t, c, "/tmp/x", CWDColorMapping{Group: "Infra", Pinned: true})
	c.captureCWDAppearance("/tmp/x", "#abcdef", "★")

	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok)
	assert.Equal(t, "#abcdef", m.Color)
	assert.Equal(t, "★", m.Icon)
	assert.Equal(t, "Infra", m.Group, "appearance capture must not disturb group")
	assert.True(t, m.Pinned, "appearance capture must not disturb pinned")
}

func TestCaptureCWDAppearance_EmptyClearsAndDropsBareEntry(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	// A record carrying only appearance is removed entirely when both are cleared
	// (mirrors "last used wins" after a reset).
	seedCWDMapping(t, c, "/tmp/x", CWDColorMapping{Color: "#111111", Icon: "🌟"})
	c.captureCWDAppearance("/tmp/x", "", "")
	_, ok := c.getCWDColorMapping("/tmp/x")
	assert.False(t, ok, "clearing the only fields should drop the entry")

	// But a record that still has group/pinned survives an appearance clear.
	seedCWDMapping(t, c, "/tmp/y", CWDColorMapping{Color: "#111111", Group: "Work"})
	c.captureCWDAppearance("/tmp/y", "", "")
	m, ok := c.getCWDColorMapping("/tmp/y")
	assert.True(t, ok, "group keeps the entry alive after an appearance clear")
	assert.Equal(t, "", m.Color)
	assert.Equal(t, "Work", m.Group)
}

func TestSeedAppearancePlan_SeedsBlankFieldsFromRecord(t *testing.T) {
	rec := CWDColorMapping{Color: "#123456", Icon: "🚀"}
	color, icon := seedAppearancePlan(tmux.Window{}, rec, true)
	assert.Equal(t, "#123456", color, "a blank window seeds the remembered color")
	assert.Equal(t, "🚀", icon, "a blank window seeds the remembered marker")
}

func TestSeedAppearancePlan_DoesNotOverwriteOwnAppearance(t *testing.T) {
	rec := CWDColorMapping{Color: "#123456", Icon: "🚀"}
	win := tmux.Window{CustomColor: "#ffffff", Icon: "★"}
	color, icon := seedAppearancePlan(win, rec, true)
	assert.Equal(t, "", color, "a window with its own color is not reseeded")
	assert.Equal(t, "", icon, "a window with its own marker is not reseeded")
}

func TestSeedAppearancePlan_SeedsOnlyMissingField(t *testing.T) {
	rec := CWDColorMapping{Color: "#123456", Icon: "🚀"}
	win := tmux.Window{CustomColor: "#ffffff"} // has color, no icon
	color, icon := seedAppearancePlan(win, rec, true)
	assert.Equal(t, "", color, "existing color is kept")
	assert.Equal(t, "🚀", icon, "missing marker is seeded")
}

func TestSeedAppearancePlan_AlreadySeededOrNoRecordIsNoOp(t *testing.T) {
	rec := CWDColorMapping{Color: "#123456", Icon: "🚀"}

	color, icon := seedAppearancePlan(tmux.Window{AppearanceSeeded: true}, rec, true)
	assert.Equal(t, "", color, "an already-seeded window inherits nothing")
	assert.Equal(t, "", icon)

	color, icon = seedAppearancePlan(tmux.Window{}, CWDColorMapping{}, false)
	assert.Equal(t, "", color, "no remembered record means nothing to seed")
	assert.Equal(t, "", icon)
}

func TestRemoteHostAppearance_GlobMatchFirstWins(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.RemoteHosts = []config.RemoteHostRule{
		{Match: "client-gunpowder-*", Color: "#ff8800", Icon: "🔥"},
		{Match: "*", Color: "#333333", Icon: "•"}, // catch-all, must lose to the specific rule above
	}

	color, icon, _ := c.remoteHostAppearance("client-gunpowder-msg")
	assert.Equal(t, "#ff8800", color, "first matching rule wins")
	assert.Equal(t, "🔥", icon)

	// Case-insensitive host matching.
	color, _, _ = c.remoteHostAppearance("CLIENT-GUNPOWDER-arsenal")
	assert.Equal(t, "#ff8800", color, "host match is case-insensitive")

	// Falls through to the catch-all.
	color, icon, _ = c.remoteHostAppearance("random-box")
	assert.Equal(t, "#333333", color)
	assert.Equal(t, "•", icon)

	// No rules configured / empty host -> nothing.
	c.config.Sidebar.RemoteHosts = nil
	color, icon, _ = c.remoteHostAppearance("client-gunpowder-msg")
	assert.Equal(t, "", color)
	assert.Equal(t, "", icon)
	color, _, _ = c.remoteHostAppearance("")
	assert.Equal(t, "", color)
}

func TestAppearanceRecordFor_LearnedWinsRuleFillsBlanks(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)
	c.config.Sidebar.RemoteHosts = []config.RemoteHostRule{
		{Match: "client-gunpowder-*", Color: "#ff8800", Icon: "🔥", Group: "Gunpowder"},
	}
	win := tmux.Window{RemoteHost: "client-gunpowder-msg"}

	// No learned mapping: the host rule supplies color, icon, AND group.
	rec, ok := c.appearanceRecordFor("sshhost://client-gunpowder-msg", win)
	assert.True(t, ok)
	assert.Equal(t, "#ff8800", rec.Color)
	assert.Equal(t, "🔥", rec.Icon)
	assert.Equal(t, "Gunpowder", rec.Group)

	// A learned color for the hooked key wins; the rule only fills the blanks.
	seedCWDMapping(t, c, "ssh://gp-msg/root", CWDColorMapping{Color: "#00aaff"})
	rec, ok = c.appearanceRecordFor("ssh://gp-msg/root", win)
	assert.True(t, ok)
	assert.Equal(t, "#00aaff", rec.Color, "learned color beats the config rule default")
	assert.Equal(t, "🔥", rec.Icon, "rule still fills the field the mapping left empty")
	assert.Equal(t, "Gunpowder", rec.Group, "rule fills the group when the mapping has none")

	// A learned group wins over the rule's group.
	seedCWDMapping(t, c, "ssh://gp-grouped/root", CWDColorMapping{Group: "Personal"})
	rec, ok = c.appearanceRecordFor("ssh://gp-grouped/root", win)
	assert.True(t, ok)
	assert.Equal(t, "Personal", rec.Group, "learned group beats the config rule default")
}

func TestAppearanceKey_GroupOnlyRuleYieldsKey(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.RemoteHosts = []config.RemoteHostRule{
		{Match: "client-studiodome*", Group: "StudioDome"},
	}
	// A rule with only a group (no color/icon) must still produce a synthetic
	// sshhost:// key so the tab can be filed under its host's group with no hook.
	win := tmux.Window{ID: "@1", RemoteHost: "client-studiodome"}
	key, ok := c.appearanceKey(win)
	assert.True(t, ok, "a group-only host rule still yields an appearance key")
	assert.Equal(t, "sshhost://client-studiodome", key)
}

func TestAppearanceKey_HookFreeRemoteFallback(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.Sidebar.RemoteHosts = []config.RemoteHostRule{
		{Match: "client-gunpowder-*", Color: "#ff8800"},
	}

	// Remote host, no remote-cwd hook reported (no remote pane cwd) -> windowNameKey
	// fails, but a matching rule yields a synthetic sshhost:// key.
	win := tmux.Window{ID: "@1", RemoteHost: "client-gunpowder-msg"}
	key, ok := c.appearanceKey(win)
	assert.True(t, ok, "a configured host gets an appearance key with no hook")
	assert.Equal(t, "sshhost://client-gunpowder-msg", key)

	// Remote host with NO matching rule and no hook -> still no key (unchanged behavior).
	win.RemoteHost = "unconfigured-host"
	_, ok = c.appearanceKey(win)
	assert.False(t, ok, "an unconfigured hook-less remote host stays keyless")
}

// TestCWDColorsMigrate_DropsLegacyNamesKeepsColors verifies the one-time
// migration strips ONLY the retired per-directory name fields from cwd-colors.json
// on load. Color/icon are remembered again (as a seed-on-create appearance) and
// must survive the load, alongside group/pinned; entries left empty are dropped.
func TestCWDColorsMigrate_DropsLegacyNamesKeepsColors(t *testing.T) {
	// The package TestMain pins TABBY_STATE_DIR for the whole run, so write the
	// seed to the actually-resolved cwd-colors path (per-test Setenv is a no-op
	// once paths.StateDir's sync.Once has cached). Clean up after.
	path := cwdColorsPath()
	t.Cleanup(func() { os.Remove(path) })

	// Seed a legacy file: a name-only "llm" entry (dropped entirely once its name
	// is gone), a "user" entry that also carries a color (name stripped, color
	// kept -> survives), and a color/icon/group entry (all kept).
	legacy := `{
  "/Users/b/git": {"name": "squint", "nameSource": "llm"},
  "/Users/b/git/tabby": {"name": "tby tabby", "nameSource": "user", "color": "#aabbcc"},
  "/Users/b/git/infra": {"color": "#112233", "icon": "🚀", "group": "Infra"}
}`
	if err := os.WriteFile(path, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	c := newTestCoordinator(t)
	c.loadCWDColors()

	// Name-only llm entry: gone (nothing left after the name is dropped).
	_, ok := c.getCWDColorMapping("/Users/b/git")
	assert.False(t, ok, "a name-only legacy entry should be dropped once its name is stripped")

	// Name + color entry: survives with just the color (name stripped).
	m, ok := c.getCWDColorMapping("/Users/b/git/tabby")
	assert.True(t, ok, "an entry that still carries a color survives the name strip")
	assert.Equal(t, "#aabbcc", m.Color, "per-dir color is remembered again")

	// Color/icon/group entry: everything is kept.
	m, ok = c.getCWDColorMapping("/Users/b/git/infra")
	assert.True(t, ok)
	assert.Equal(t, "#112233", m.Color, "per-dir color is remembered")
	assert.Equal(t, "🚀", m.Icon, "per-dir marker is remembered")
	assert.Equal(t, "Infra", m.Group)

	// The on-disk file is rewritten without legacy name keys, but keeps color/icon.
	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), "\"name\"")
	assert.NotContains(t, string(data), "nameSource")
	assert.Contains(t, string(data), "\"color\"")
	assert.Contains(t, string(data), "\"icon\"")
}

func TestWindowNameKey_LocalAndRemote(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	// Remote window whose hook has reported in: keyed on ssh://host/topmost.
	remote := tmux.Window{
		RemoteHost: "bdm1",
		Panes: []tmux.Pane{
			{ID: "%1", Command: "ssh", Remote: true, RemoteCWD: "client-b7" + "\x1f" + "/srv/app"},
		},
	}
	key, ok := c.windowNameKey(remote)
	assert.True(t, ok)
	assert.Equal(t, "ssh://client-b7/srv/app", key)

	// Remote window with no reported cwd yet: no key (don't collide on the
	// local ssh-launch path).
	remoteNoHook := tmux.Window{
		RemoteHost: "bdm1",
		Panes:      []tmux.Pane{{ID: "%2", Command: "ssh", Remote: true, CurrentPath: "/home/user"}},
	}
	_, ok = c.windowNameKey(remoteNoHook)
	assert.False(t, ok, "a remote window with no remote-cwd report yields no key")

	// Local window outside a repo: keyed on the cwd itself.
	local := tmux.Window{Panes: []tmux.Pane{{ID: "%3", Command: "zsh", CurrentPath: t.TempDir()}}}
	key, ok = c.windowNameKey(local)
	assert.True(t, ok)
	assert.NotEmpty(t, key)
}

func TestParseAbbreviations(t *testing.T) {
	m := parseAbbreviations([]string{
		"TBY>Tabby",          // folder key is lower-cased for case-insensitive match
		"  MP > my project ", // trimmed on both sides
		"malformed-no-arrow",
		">missingcode",
		"missingfolder>",
		"", // empty
	})
	assert.Equal(t, "TBY", m["tabby"])
	assert.Equal(t, "MP", m["my project"])
	assert.Len(t, m, 2, "malformed/empty entries are skipped")
}

func TestDirAbbreviation_CaseInsensitive(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.TabNames.Abbreviations = []string{"TBY>Tabby"}

	for _, folder := range []string{"tabby", "Tabby", "TABBY"} {
		code, ok := c.dirAbbreviation(folder)
		assert.True(t, ok, "folder %q should match", folder)
		assert.Equal(t, "TBY", code)
	}
	_, ok := c.dirAbbreviation("other")
	assert.False(t, ok)
}

// dirCodeWindow builds a window whose first content pane is in <base> (under a
// throwaway parent), priming gitTopCache so windowDirCode resolves the project
// code from the directory basename without forking git. cmd selects the pane's
// command (e.g. "zsh" for a plain window, a semver like "2.1.159" for an AI tool).
func dirCodeWindow(c *Coordinator, name, base, cmd, aiTitle string) tmux.Window {
	cwd := normalizeCWD(filepath.Join("/tmp/tabby-dircode-test", base))
	c.gitTopMu.Lock()
	c.gitTopCache[cwd] = "" // not a repo -> windowProjectBasename uses the basename
	c.gitTopMu.Unlock()
	return tmux.Window{
		ID: "@x", Name: name, AITitle: aiTitle,
		Panes: []tmux.Pane{{ID: "%1", Command: cmd, CurrentPath: cwd}},
	}
}

func TestComposeTabBaseName(t *testing.T) {
	c := newTestCoordinator(t)

	cases := []struct {
		desc, base, aiTitle, want string
	}{
		// The project code is derived from the DIRECTORY (not the window name);
		// the live summary follows it, space-separated (render may wrap it).
		{"summary: single word dir", "tabby", "refactor auth", "TBY refactor auth"},
		{"summary: short dir kept whole", "foo", "do thing", "FOO do thing"},
		// No summary -> deterministic code alone.
		{"no summary: single word dir", "tabby", "", "TBY"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			w := dirCodeWindow(c, "irrelevant-window-name", tc.base, "zsh", tc.aiTitle)
			assert.Equal(t, tc.want, c.composeTabBaseName(w))
		})
	}

	// $HOME / unresolved window (no content cwd): no dir code. Falls back to the
	// summary when present, else the plain window name.
	t.Run("unresolved with summary -> summary only", func(t *testing.T) {
		w := tmux.Window{ID: "@x", Name: "~", AITitle: "fix it"}
		assert.Equal(t, "fix it", c.composeTabBaseName(w))
	})
	t.Run("unresolved no summary -> plain name", func(t *testing.T) {
		w := tmux.Window{ID: "@x", Name: "~"}
		assert.Equal(t, "~", c.composeTabBaseName(w))
	})
	t.Run("raw window id -> ~", func(t *testing.T) {
		w := tmux.Window{ID: "@x", Name: "@5"}
		assert.Equal(t, "~", c.composeTabBaseName(w))
	})

	// A user-locked name is authoritative: shown verbatim, with neither the
	// dir-code nor the AI summary. Regression guard against names being mangled
	// or overridden by the live summary.
	t.Run("locked name shows verbatim", func(t *testing.T) {
		w := dirCodeWindow(c, "API Server", "tabby", "zsh", "deploy now")
		w.NameLocked = true
		assert.Equal(t, "API Server", c.composeTabBaseName(w))
	})
	t.Run("unlocked still shows code + summary", func(t *testing.T) {
		w := dirCodeWindow(c, "API Server", "tabby", "zsh", "deploy now")
		assert.Equal(t, "TBY deploy now", c.composeTabBaseName(w))
	})
}

func TestComposeTabBaseName_RemoteHookless(t *testing.T) {
	c := newTestCoordinator(t)

	// A hook-less remote tab: RemoteHost set and filed under its remote_hosts
	// group, but the remote-cwd hook has NOT reported (no RemoteCWD on the pane).
	// The label must key on the GROUP, not the local launch dir it was fired from,
	// and show a "new" placeholder until a fresh remote summary lands — never the
	// stale local "SANE sane check".
	base := tmux.Window{
		ID:         "@r",
		Name:       "sane-check",
		Group:      "SD",
		RemoteHost: "client-studiodome",
		Panes:      []tmux.Pane{{ID: "%1", Command: "ssh", CurrentPath: "/Users/b/git/sane-check", Remote: true}},
	}

	t.Run("no summary -> group code + new", func(t *testing.T) {
		w := base
		assert.Equal(t, "SD new", c.composeTabBaseName(w))
	})

	t.Run("fresh summary -> group code + summary", func(t *testing.T) {
		w := base
		w.AITitle = "docker logs"
		assert.Equal(t, "SD docker logs", c.composeTabBaseName(w))
	})

	t.Run("no group falls back to ssh host abbreviation", func(t *testing.T) {
		w := base
		w.Group = ""
		assert.Equal(t, "CS new", c.composeTabBaseName(w))
	})

	t.Run("hook reported -> normal remote-dir code, not placeholder", func(t *testing.T) {
		w := base
		w.Panes = []tmux.Pane{{ID: "%1", Command: "ssh", Remote: true,
			RemoteCWD: "client-studiodome" + remoteCWDSep + "/srv/imgen"}}
		// firstPaneRemoteCWD now reports -> windowProjectBasename resolves "imgen".
		assert.Equal(t, "IMG do thing", c.composeTabBaseName(withAITitle(w, "do thing")))
	})
}

func withAITitle(w tmux.Window, title string) tmux.Window {
	w.AITitle = title
	return w
}

func TestComposeTabBaseName_AISummaryOnly(t *testing.T) {
	t.Run("ai window drops the dir code", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = true
		aiWin := dirCodeWindow(c, "tabby", "tabby", "2.1.159", "fixing tests")
		assert.Equal(t, "fixing tests", c.composeTabBaseName(aiWin))
	})

	t.Run("non-ai window keeps the code", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = true
		plainWin := dirCodeWindow(c, "tabby", "tabby", "nvim", "fixing tests")
		assert.Equal(t, "TBY fixing tests", c.composeTabBaseName(plainWin))
	})

	t.Run("flag off keeps the code even for ai windows", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = false
		aiWin := dirCodeWindow(c, "tabby", "tabby", "2.1.159", "fixing tests")
		assert.Equal(t, "TBY fixing tests", c.composeTabBaseName(aiWin))
	})
}

func TestWrapTabLabel(t *testing.T) {
	// Single line when it fits.
	assert.Equal(t, []string{"1. TB ok"}, wrapTabLabel("1. TB ok", 20, 20, 2))

	// Wraps at the CHARACTER (not word) across 2 lines; overflow truncates with "…".
	got := wrapTabLabel("1. INF setting sidebar", 8, 10, 2)
	assert.Equal(t, []string{"1. INF s", "etting si" + tabOverflowMarker}, got)

	// Char-wrap fills line 0 to its budget, continues on line 1.
	assert.Equal(t, []string{"1. INF s", "etting"}, wrapTabLabel("1. INF setting", 8, 10, 2))

	// Overflow past maxLines truncates the last line with "…".
	got2 := wrapTabLabel("1. AAA bbb ccc ddd eee", 6, 6, 2)
	assert.Len(t, got2, 2)
	assert.Contains(t, got2[1], tabOverflowMarker)

	// maxLines=1 behaves like single-line truncation.
	got3 := wrapTabLabel("1. INF setting sidebar", 8, 8, 1)
	assert.Len(t, got3, 1)
	assert.Contains(t, got3[0], tabOverflowMarker)
}

func TestComposeTabBaseName_ConfigOverridesAutoCode(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.TabNames.Abbreviations = []string{"ZZZ>tabby"} // override the auto "TBY"

	win := dirCodeWindow(c, "tabby", "tabby", "zsh", "refactor auth")
	assert.Equal(t, "ZZZ refactor auth", c.composeTabBaseName(win))
}

func TestComposeTabBaseName_ProjectNamesCode(t *testing.T) {
	c := newTestCoordinator(t)
	// ai.tab_summary.project_names supplies the deterministic prefix and takes
	// precedence over the auto-derived code (which would be "TMC" for teamclaude).
	c.config.AI.TabSummary.ProjectNames = []string{"tc>teamclaude"}

	win := dirCodeWindow(c, "teamclaude", "teamclaude", "zsh", "council tool")
	assert.Equal(t, "tc council tool", c.composeTabBaseName(win))
}

// TestComposeTabBaseName_WorktreeUsesLeafNotToplevel guards the regression where
// a session in a worktree SUBDIR was collapsed up to the git toplevel and
// mislabeled by the root's configured abbreviation. The code must come from the
// LEAF working directory the user is actually in.
func TestComposeTabBaseName_WorktreeUsesLeafNotToplevel(t *testing.T) {
	c := newTestCoordinator(t)
	// An abbreviation is configured for the worktree ROOT name.
	c.config.TabNames.Abbreviations = []string{"SD>publications-phase1"}

	cwd := normalizeCWD("/tmp/x/.claude/worktrees/publications-phase1/imgen")
	// Prime gitTopCache to the worktree ROOT — proving that even when a toplevel
	// IS resolvable, the leaf (imgen) wins, not the root's "SD" abbreviation.
	c.gitTopMu.Lock()
	c.gitTopCache[cwd] = "/tmp/x/.claude/worktrees/publications-phase1"
	c.gitTopMu.Unlock()

	w := tmux.Window{ID: "@x", Name: "imgen",
		Panes: []tmux.Pane{{ID: "%1", Command: "zsh", CurrentPath: cwd}}}
	assert.Equal(t, "IMG", c.composeTabBaseName(w), "leaf 'imgen' -> IMG, not root 'SD'")
}

// TestCWDColorMapping_LegacyJSONBackCompat ensures the retired name/nameSource
// fields don't break deserialization of pre-existing cwd-colors.json entries:
// unknown JSON keys are silently ignored and color/icon still load.
func TestCWDColorMapping_LegacyJSONBackCompat(t *testing.T) {
	var m CWDColorMapping
	err := json.Unmarshal([]byte(`{"color":"#aabbcc","icon":"🚀","name":"old","nameSource":"llm"}`), &m)
	assert.NoError(t, err)
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "🚀", m.Icon)
	assert.Equal(t, "", m.Group)
	assert.False(t, m.Pinned)
}

func TestComputeVisualPositions_EmptyGrouped(t *testing.T) {
	c := newTestCoordinator(t)
	c.computeVisualPositions()
	assert.Empty(t, c.windowVisualPos)
}

func TestComputeVisualPositions_SingleGroup(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 1
	c.grouped = []grouping.GroupedWindows{
		{
			Name:    "Dev",
			Windows: []tmux.Window{testWindow("w1", true), testWindow("w2", false)},
		},
	}
	c.computeVisualPositions()

	assert.Equal(t, 1, c.windowVisualPos["@w1"])
	assert.Equal(t, 2, c.windowVisualPos["@w2"])
}

func TestComputeVisualPositions_MultipleGroups(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 0
	c.grouped = []grouping.GroupedWindows{
		{Name: "Group A", Windows: []tmux.Window{testWindow("w1", true)}},
		{Name: "Group B", Windows: []tmux.Window{testWindow("w2", false), testWindow("w3", false)}},
	}
	c.computeVisualPositions()

	assert.Equal(t, 0, c.windowVisualPos["@w1"])
	assert.Equal(t, 1, c.windowVisualPos["@w2"])
	assert.Equal(t, 2, c.windowVisualPos["@w3"])
}

func TestComputeVisualPositions_RebuildsFromScratch(t *testing.T) {
	c := newTestCoordinator(t)
	c.baseIndex = 0
	c.windowVisualPos["@old"] = 99

	c.grouped = []grouping.GroupedWindows{
		{Name: "G", Windows: []tmux.Window{testWindow("@new", true)}},
	}
	c.computeVisualPositions()

	_, hasOld := c.windowVisualPos["@old"]
	assert.False(t, hasOld, "stale entries must be cleared on recompute")
	assert.Equal(t, 0, c.windowVisualPos["@new"])
}

func TestGetConfig_ReturnsConfig(t *testing.T) {
	c := newTestCoordinator(t)
	cfg := c.GetConfig()
	assert.Same(t, c.config, cfg)
	assert.Equal(t, 2, len(cfg.Groups))
}

func TestNewWindowStatusLifecycle(t *testing.T) {
	c := newTestCoordinator(t)

	initial := c.NewWindowStatus()
	assert.Equal(t, "none", initial.State)
	assert.Empty(t, initial.WindowID)

	c.SetNewWindowInFlight("Dev", "/tmp/project", "/dev/ttys999")
	inFlight := c.NewWindowStatus()
	assert.Equal(t, "inFlight", inFlight.State)
	assert.Equal(t, "Dev", inFlight.Group)
	assert.Equal(t, "/tmp/project", inFlight.WorkingDir)
	assert.Equal(t, "/dev/ttys999", inFlight.FiringTTY)
	assert.NotZero(t, inFlight.Created)

	c.SetNewWindowReady("@123")
	ready := c.NewWindowStatus()
	assert.Equal(t, "ready", ready.State)
	assert.Equal(t, "@123", ready.WindowID)
	assert.Equal(t, "Dev", ready.Group)
	assert.Equal(t, "/tmp/project", ready.WorkingDir)
	assert.Equal(t, "/dev/ttys999", ready.FiringTTY, "FiringTTY should survive in-flight -> ready transition")

	c.ClearNewWindowStatus()
	cleared := c.NewWindowStatus()
	assert.Equal(t, "none", cleared.State)
	assert.Empty(t, cleared.WindowID)
	assert.Empty(t, cleared.Group)
	assert.Empty(t, cleared.WorkingDir)
	assert.Empty(t, cleared.FiringTTY)
}

func TestWindowTransitionLifecycle(t *testing.T) {
	c := newTestCoordinator(t)

	assert.False(t, c.IsTransitionInProgress())

	err := c.BeginTransition("@2", "switch_window", "test")
	assert.NoError(t, err)
	assert.True(t, c.IsTransitionInProgress())

	c.stateMu.RLock()
	transition := c.windowTransition
	c.stateMu.RUnlock()

	assert.Equal(t, "@2", transition.TargetWindowID)
	assert.Equal(t, "switch_window", transition.Reason)
	assert.Equal(t, "test", transition.Source)
	assert.False(t, transition.StartedAt.IsZero())
	assert.WithinDuration(t, time.Now(), transition.StartedAt, 2*time.Second)

	c.CompleteTransition()
	assert.False(t, c.IsTransitionInProgress())
}

func TestWindowTransitionRejectsBeginWhileInProgress(t *testing.T) {
	c := newTestCoordinator(t)

	err := c.BeginTransition("@2", "switch_window", "test")
	assert.NoError(t, err)

	err = c.BeginTransition("@3", "switch_window", "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transition already in progress")
	assert.Contains(t, err.Error(), "target=@2")
}

func TestTeamClaudeBareEmail(t *testing.T) {
	cases := map[string]string{
		"brendan@gunpowder.tech (brendan@gunpowder.tech's Organization)": "brendan@gunpowder.tech",
		"brendan@gunpowder.tech (Gunpowder)":                             "brendan@gunpowder.tech",
		"b@debea.si":                                                     "b@debea.si",
		"  Shaked@studiodome.com  ":                                      "Shaked@studiodome.com",
	}
	for in, want := range cases {
		if got := teamClaudeBareEmail(in); got != want {
			t.Errorf("teamClaudeBareEmail(%q) = %q, want %q", in, got, want)
		}
	}
	// A personal + team pair on the same email collapses to one bare-email key,
	// so duplicate-email detection groups them (and the personal row gets PER).
	if teamClaudeBareEmail("brendan@gunpowder.tech (Gunpowder)") !=
		teamClaudeBareEmail("brendan@gunpowder.tech (brendan@gunpowder.tech's Organization)") {
		t.Errorf("personal and team accounts on the same email must share a bare-email key")
	}
}

// TestIsGenericTabName verifies the guard that keeps automatic-rename artifacts
// (notably the bare "claude" CLI name and Claude Code's semver proc title) from
// being persisted/restored as a tab identity, while leaving deliberate names
// (group-prefixed or custom) untouched.
func TestIsGenericTabName(t *testing.T) {
	// "agy"/"gemini" etc. resolve via tmux.IsAITool, which reads configured
	// ai_tools; configure them so the AI-command branch is exercised.
	tmux.ConfigureBusyDetection(nil, []string{"agy", "gemini", "codex"}, 10)

	generic := []string{
		"", "~", "/", "~/git", "@3", "@17",
		"claude", "CLAUDE", "zsh", "bash",
		"2.1.187", // Claude Code semver proc title
		"agy",     // Antigravity (configured ai_tool)
		"gemini",
	}
	for _, n := range generic {
		assert.Truef(t, isGenericTabName(n), "expected %q to be generic", n)
	}

	deliberate := []string{
		"GP|Ignite|instance-types", "SD|publications-plan",
		"squint-axe", "studio dome", "tabby", "digest-body",
	}
	for _, n := range deliberate {
		assert.Falsef(t, isGenericTabName(n), "expected %q to be a real name", n)
	}
}
