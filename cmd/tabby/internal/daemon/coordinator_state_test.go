package daemon

import (
	"encoding/json"
	"testing"
	"time"

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

func TestSetAndGetCWDColor(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/home/user/project", "#3498db")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "#3498db", m.Color)
}

func TestSetCWDColor_DeletesEntryWhenBothEmpty(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/project", "#ff0000")
	_, ok := c.getCWDColorMapping("/tmp/project")
	assert.True(t, ok)

	c.setCWDColor("/tmp/project", "")
	_, ok = c.getCWDColorMapping("/tmp/project")
	assert.False(t, ok, "entry should be removed when color and icon are both empty")
}

func TestSetAndGetCWDIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDIcon("/home/user/project", "🚀")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "🚀", m.Icon)
}

func TestSetCWDIcon_PreservesExistingColor(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/x", "#aabbcc")
	c.setCWDIcon("/tmp/x", "🌟")
	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok)
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "🌟", m.Icon)
}

func TestCaptureCWDIdentity_StoresNameGroupPinned(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/home/user/project", "  api  ", "  Work  ", true, "user")
	m, ok := c.getCWDColorMapping("/home/user/project")
	assert.True(t, ok)
	assert.Equal(t, "api", m.Name, "name should be trimmed and stored")
	assert.Equal(t, "Work", m.Group, "group should be trimmed and stored")
	assert.True(t, m.Pinned)
	assert.Equal(t, "user", m.NameSource)
}

func TestCaptureCWDIdentity_EmptyNameIsNoOp(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/tmp/p", "   ", "Work", true, "user")
	_, ok := c.getCWDColorMapping("/tmp/p")
	assert.False(t, ok, "an empty name carries nothing to capture")
}

func TestCaptureCWDIdentity_PreservesColorIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/x", "#aabbcc")
	c.setCWDIcon("/tmp/x", "🌟")
	c.captureCWDIdentity("/tmp/x", "db", "Infra", true, "user")

	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok)
	assert.Equal(t, "#aabbcc", m.Color, "capture must not disturb the saved color")
	assert.Equal(t, "🌟", m.Icon, "capture must not disturb the saved icon")
	assert.Equal(t, "db", m.Name)
	assert.Equal(t, "Infra", m.Group)
	assert.True(t, m.Pinned)
}

func TestClearCWDIdentity_RemovesIdentityKeepsColorIcon(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.setCWDColor("/tmp/x", "#aabbcc")
	c.captureCWDIdentity("/tmp/x", "db", "Infra", true, "user")

	c.clearCWDIdentity("/tmp/x")
	m, ok := c.getCWDColorMapping("/tmp/x")
	assert.True(t, ok, "color mapping should survive an identity clear")
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "", m.Name)
	assert.Equal(t, "", m.Group)
	assert.False(t, m.Pinned)
}

func TestClearCWDIdentity_DeletesEntryWhenNothingRemains(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/tmp/only-name", "api", "", false, "user")
	_, ok := c.getCWDColorMapping("/tmp/only-name")
	assert.True(t, ok)

	c.clearCWDIdentity("/tmp/only-name")
	_, ok = c.getCWDColorMapping("/tmp/only-name")
	assert.False(t, ok, "entry should be removed when no color/icon/identity remains")
}

func TestCaptureCWDIdentity_LLMDoesNotClobberUserName(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	// A user name is authoritative; a later llm capture must not overwrite it.
	c.captureCWDIdentity("/tmp/proj", "my api", "", false, "user")
	c.captureCWDIdentity("/tmp/proj", "guessed name", "", false, "llm")
	m, ok := c.getCWDColorMapping("/tmp/proj")
	assert.True(t, ok)
	assert.Equal(t, "my api", m.Name, "llm must not clobber a user name")
	assert.Equal(t, "user", m.NameSource)
}

func TestCaptureCWDIdentity_UserUpgradesLLMName(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	// A soft llm name can be set, then a user rename upgrades the source.
	c.captureCWDIdentity("/tmp/proj", "guessed", "", false, "llm")
	m, _ := c.getCWDColorMapping("/tmp/proj")
	assert.Equal(t, "llm", m.NameSource)

	c.captureCWDIdentity("/tmp/proj", "real name", "", false, "user")
	m, _ = c.getCWDColorMapping("/tmp/proj")
	assert.Equal(t, "real name", m.Name)
	assert.Equal(t, "user", m.NameSource, "a user rename upgrades the source and freezes it")
}

func TestCaptureCWDIdentity_LLMRefinesPriorLLMName(t *testing.T) {
	t.Setenv("TABBY_STATE_DIR", t.TempDir())
	c := newTestCoordinator(t)

	c.captureCWDIdentity("/tmp/proj", "first guess", "", false, "llm")
	c.captureCWDIdentity("/tmp/proj", "better guess", "", false, "llm")
	m, _ := c.getCWDColorMapping("/tmp/proj")
	assert.Equal(t, "better guess", m.Name, "an llm name may be refined by a later llm capture")
	assert.Equal(t, "llm", m.NameSource)
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

func TestComposeTabBaseName(t *testing.T) {
	c := newTestCoordinator(t)

	win := func(name, aiTitle string) tmux.Window {
		return tmux.Window{ID: "@x", Name: name, AITitle: aiTitle}
	}

	cases := []struct {
		desc, name, aiTitle, want string
	}{
		// Abbreviation is derived from the window NAME (respects renames); the
		// summary follows the code, space-separated (render may wrap it).
		{"summary: single word name", "tabby", "refactor auth", "TBY refactor auth"},
		{"summary: short name kept whole", "foo", "do thing", "FOO do thing"},
		{"summary: renamed tab with space", "API Server", "deploy now", "AS deploy now"},
		{"summary: composite multi-pane name", "api | web", "fix bug", "API | WEB fix bug"},
		// No summary -> code alone.
		{"no summary: single word", "tabby", "", "TBY"},
		{"no summary: composite name", "client | server", "", "CLN | SRV"},
		// Plain fallbacks (no code).
		{"raw window id -> ~", "@5", "", "~"},
		{"home name stays plain", "~", "", "~"},
		{"home with summary -> summary only", "~", "fix it", "fix it"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			assert.Equal(t, tc.want, c.composeTabBaseName(win(tc.name, tc.aiTitle)))
		})
	}

	// A user-locked name is authoritative: shown verbatim, with neither the
	// dir-code abbreviation nor the AI summary. Regression guard for the LLM
	// clobbering manual renames and for abbreviation mangling them.
	t.Run("locked name shows verbatim, no abbreviation or summary", func(t *testing.T) {
		w := tmux.Window{ID: "@x", Name: "API Server", AITitle: "deploy now", NameLocked: true}
		assert.Equal(t, "API Server", c.composeTabBaseName(w))
	})
	t.Run("unlocked name still shows AI summary", func(t *testing.T) {
		w := tmux.Window{ID: "@x", Name: "API Server", AITitle: "deploy now", NameLocked: false}
		assert.Equal(t, "AS deploy now", c.composeTabBaseName(w))
	})
}

func TestComposeTabBaseName_AISummaryOnly(t *testing.T) {
	// A Claude Code pane (IsAITool matches its semver process name).
	aiWin := tmux.Window{ID: "@1", Name: "tabby", AITitle: "fixing tests",
		Panes: []tmux.Pane{{ID: "%1", Command: "2.1.159"}}}
	plainWin := tmux.Window{ID: "@2", Name: "tabby", AITitle: "fixing tests",
		Panes: []tmux.Pane{{ID: "%1", Command: "nvim"}}}

	t.Run("ai window drops the dir code", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = true
		assert.Equal(t, "fixing tests", c.composeTabBaseName(aiWin))
	})

	t.Run("non-ai window keeps the code", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = true
		assert.Equal(t, "TBY fixing tests", c.composeTabBaseName(plainWin))
	})

	t.Run("flag off keeps the code even for ai windows", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.AISummaryOnly = false
		assert.Equal(t, "TBY fixing tests", c.composeTabBaseName(aiWin))
	})
}

func TestWrapTabLabel(t *testing.T) {
	// Single line when it fits.
	assert.Equal(t, []string{"1. TB ok"}, wrapTabLabel("1. TB ok", 20, 20, 2))

	// Wraps whole words across 2 lines; overflow ("sidebar") drops with a "~".
	got := wrapTabLabel("1. INF setting sidebar", 8, 10, 2)
	assert.Equal(t, []string{"1. INF", "setting~"}, got)

	// Exactly fits 2 lines, no truncation marker.
	assert.Equal(t, []string{"1. INF", "setting"}, wrapTabLabel("1. INF setting", 8, 10, 2))

	// Overflow past maxLines truncates the last line with "~".
	got2 := wrapTabLabel("1. AAA bbb ccc ddd eee", 6, 6, 2)
	assert.Len(t, got2, 2)
	assert.Contains(t, got2[1], "~")

	// maxLines=1 behaves like single-line truncation.
	got3 := wrapTabLabel("1. INF setting sidebar", 8, 8, 1)
	assert.Len(t, got3, 1)
	assert.Contains(t, got3[0], "~")
}

func TestComposeTabBaseName_ConfigOverridesAutoCode(t *testing.T) {
	c := newTestCoordinator(t)
	c.config.TabNames.Abbreviations = []string{"ZZZ>tabby"} // override the auto "TBY"

	win := tmux.Window{ID: "@1", Name: "tabby", AITitle: "refactor auth"}
	assert.Equal(t, "ZZZ refactor auth", c.composeTabBaseName(win))
}

// TestCWDColorMapping_LegacyJSONBackCompat ensures the new Name/Group/Pinned
// fields don't break deserialization of pre-existing cwd-colors.json entries
// that only carry color/icon.
func TestCWDColorMapping_LegacyJSONBackCompat(t *testing.T) {
	var m CWDColorMapping
	err := json.Unmarshal([]byte(`{"color":"#aabbcc","icon":"🚀"}`), &m)
	assert.NoError(t, err)
	assert.Equal(t, "#aabbcc", m.Color)
	assert.Equal(t, "🚀", m.Icon)
	assert.Equal(t, "", m.Name)
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
		"brendan@gunpowder.tech (Gunpowder)":                            "brendan@gunpowder.tech",
		"b@debea.si":                                                    "b@debea.si",
		"  Shaked@studiodome.com  ":                                     "Shaked@studiodome.com",
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
