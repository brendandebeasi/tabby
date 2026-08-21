package landing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brendandebeasi/tabby/pkg/textwidth"
)

const sampleYAML = `version: 1
source_host: bdm1
generated: 2026-08-21
entries:
  - label: tabby
    kind: local
    dir: ~/git/tabby
    color: '#bcce5a'
    icon: "\U0001F408"
    rank: 1
  - label: StudioDome
    kind: ssh
    host: client-studiodome
    color: '#b4637a'
    icon: "\U0001F30E"
    group: StudioDome
    rank: 2
  - label: hosting-questions
    kind: local
    dir: ~/git/hosting-questions
    icon: "1️⃣"
    rank: 9
  - label: Gunpowder
    kind: ssh
    host: client-gunpowder
    group: Gunpowder
    rank: 6
`

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "landing.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadSortsByRank(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"tabby", "StudioDome", "Gunpowder", "hosting-questions"}
	if len(cfg.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(cfg.Entries), len(want))
	}
	for i, w := range want {
		if cfg.Entries[i].Label != w {
			t.Errorf("entry %d = %q, want %q", i, cfg.Entries[i].Label, w)
		}
	}
}

func TestLoadMissingFileIsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if !os.IsNotExist(err) {
		t.Fatalf("got %v, want a not-exist error", err)
	}
}

func TestLoadMalformedYAMLErrors(t *testing.T) {
	if _, err := Load(writeTemp(t, "entries: [oh: dear\n")); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	body := "version: 1\nentries:\n  - label: x\n    kind: ssh\n    host: h\n    colour: '#fff'\n"
	if _, err := Load(writeTemp(t, body)); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestLoadDropsInvalidEntries(t *testing.T) {
	body := `version: 1
entries:
  - label: good
    kind: ssh
    host: h
    rank: 1
  - label: nokind
    kind: wat
    rank: 2
  - label: nodir
    kind: local
    rank: 3
  - kind: ssh
    host: unnamed
    rank: 4
`
	cfg, err := Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Entries) != 1 || cfg.Entries[0].Label != "good" {
		t.Fatalf("got %+v, want only the valid entry", cfg.Entries)
	}
}

func TestApplyFilter(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		filter string
		want   []string
	}{
		{"", []string{"tabby", "StudioDome", "Gunpowder", "hosting-questions"}},
		{"tab", []string{"tabby"}},
		{"STUDIO", []string{"StudioDome"}},               // case-insensitive
		{"client-", []string{"StudioDome", "Gunpowder"}}, // matches the target
		{"Gunpowder", []string{"Gunpowder"}},             // matches the group
		{"zzz", nil},
	}
	for _, c := range cases {
		got := applyFilter(cfg.Entries, c.filter)
		if len(got) != len(c.want) {
			t.Errorf("filter %q: got %d entries, want %d", c.filter, len(got), len(c.want))
			continue
		}
		for i, w := range c.want {
			if got[i].Label != w {
				t.Errorf("filter %q: entry %d = %q, want %q", c.filter, i, got[i].Label, w)
			}
		}
	}
}

func TestGroupEntriesOrdersByBestRank(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	groups := groupEntries(cfg.Entries)
	want := []string{"", "StudioDome", "Gunpowder"}
	if len(groups) != len(want) {
		t.Fatalf("got %d groups, want %d", len(groups), len(want))
	}
	for i, w := range want {
		if groups[i].name != w {
			t.Errorf("group %d = %q, want %q", i, groups[i].name, w)
		}
	}
	// The ungrouped bucket holds both ungrouped entries, in rank order.
	if len(groups[0].entries) != 2 || groups[0].entries[0].Label != "tabby" {
		t.Errorf("ungrouped bucket = %+v", groups[0].entries)
	}
}

func TestCommand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	ssh := Entry{Label: "x", Kind: kindSSH, Host: "client-a"}
	if got, want := ssh.Command(), "ssh 'client-a'"; got != want {
		t.Errorf("ssh command = %q, want %q", got, want)
	}
	local := Entry{Label: "y", Kind: kindLocal, Dir: "~/git/tabby"}
	if got, want := local.Command(), "cd '"+filepath.Join(home, "git/tabby")+"'"; got != want {
		t.Errorf("local command = %q, want %q", got, want)
	}
}

func TestCommandQuotesHostileNames(t *testing.T) {
	// Every metacharacter stays inside the single quotes, and the embedded
	// quote is closed, escaped, and reopened, so the shell sees one argument.
	e := Entry{Label: "z", Kind: kindLocal, Dir: "/tmp/it's here; rm -rf /"}
	if got := e.Command(); got != `cd '/tmp/it'\''s here; rm -rf /'` {
		t.Errorf("command = %q", got)
	}
}

func TestColumnsForBreakpoints(t *testing.T) {
	cases := []struct{ width, want int }{
		{40, 1}, {59, 1}, {60, 1}, {99, 1}, {100, 2}, {200, 2},
	}
	for _, c := range cases {
		if got := columnsFor(c.width); got != c.want {
			t.Errorf("columnsFor(%d) = %d, want %d", c.width, got, c.want)
		}
	}
}

// A row wider than the pane wraps, which scrolls the alt-screen buffer and
// leaves every later frame offset. The emoji-presentation icons are the case
// that breaks naive width math, so they are what this checks.
func TestRenderRowNeverExceedsWidth(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range cfg.Entries {
		for _, w := range []int{10, 20, 40, 58, 80, 120} {
			for _, sel := range []bool{false, true} {
				row := renderRow(e, w, sel)
				if got := textwidth.Display(textwidth.StripANSI(row)); got != w {
					t.Errorf("renderRow(%q, w=%d, sel=%v) drew %d columns",
						e.Label, w, sel, got)
				}
			}
		}
	}
}

func TestRenderGroupsRowsFitWidth(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	groups := groupEntries(cfg.Entries)
	for _, w := range []int{40, 80, 120, 200} {
		rows, _ := renderGroups(groups, &cfg.Entries[0], w, w <= compactMax)
		for i, r := range rows {
			if got := textwidth.Display(textwidth.StripANSI(r)); got > w {
				t.Errorf("width %d: row %d drew %d columns", w, i, got)
			}
		}
	}
}

func TestRenderGroupsReportsCursorRow(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	groups := groupEntries(cfg.Entries)
	rows, cursorRow := renderGroups(groups, &cfg.Entries[0], 80, false)
	if cursorRow < 0 || cursorRow >= len(rows) {
		t.Fatalf("cursorRow = %d, out of range for %d rows", cursorRow, len(rows))
	}
	if !strings.Contains(textwidth.StripANSI(rows[cursorRow]), "tabby") {
		t.Errorf("cursor row = %q, want the first entry", rows[cursorRow])
	}
}

func TestRenderHeaderNarrowFallsBackToPlainName(t *testing.T) {
	if strings.Contains(renderHeader(wordmarkMin-1, 0), "#") {
		t.Error("compact header should not draw the block wordmark")
	}
	if !strings.Contains(renderHeader(wordmarkMin, 0), "#") {
		t.Error("header should draw the block wordmark once it fits")
	}
}

func TestWordmarkRowsAreEqualWidth(t *testing.T) {
	for i, r := range wordmark {
		if len(r) != len(wordmark[0]) {
			t.Errorf("wordmark row %d is %d chars, row 0 is %d", i, len(r), len(wordmark[0]))
		}
	}
}

func TestReadableFg(t *testing.T) {
	cases := map[string]string{
		"#ffffff": "#000000",
		"#000000": "#ffffff",
		"#bcce5a": "#000000",
		"#7f26d9": "#ffffff",
		"250":     "255", // non-hex falls back
	}
	for bg, want := range cases {
		if got := readableFg(bg); got != want {
			t.Errorf("readableFg(%q) = %q, want %q", bg, got, want)
		}
	}
}

func TestModelCursorNavigation(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfg.Entries)
	if e := m.cursorEntry(); e == nil || e.Label != "tabby" {
		t.Fatalf("initial cursor = %+v, want tabby", e)
	}
	// Filtering to nothing must leave the cursor safe to read.
	m.filter = "zzz"
	m.refilter()
	if e := m.cursorEntry(); e != nil {
		t.Errorf("cursorEntry on empty filter = %+v, want nil", e)
	}
	m.filter = ""
	m.refilter()
	if e := m.cursorEntry(); e == nil {
		t.Error("cursorEntry after clearing filter = nil")
	}
}

// Bubbletea coalesces runes that arrive in a single read, so a fast typist or
// a paste shows up as one KeyRunes event carrying the whole string.
func TestHandleKeyAcceptsBatchedRunes(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfg.Entries)
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gun")})
	got := next.(model)
	if got.filter != "gun" {
		t.Fatalf("filter = %q, want %q", got.filter, "gun")
	}
	if len(got.filtered) != 1 || got.filtered[0].Label != "Gunpowder" {
		t.Fatalf("filtered = %+v, want just Gunpowder", got.filtered)
	}
}

func TestHandleKeyDropsControlRunes(t *testing.T) {
	m := newModel([]Entry{{Label: "a", Kind: kindLocal, Dir: "/a"}})
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb")})
	if got := next.(model).filter; got != "ab" {
		t.Fatalf("filter = %q, want %q", got, "ab")
	}
}

// Alt-modified runes are shortcuts, not text, and must not reach the filter.
func TestHandleKeyIgnoresAltRunes(t *testing.T) {
	m := newModel([]Entry{{Label: "a", Kind: kindLocal, Dir: "/a"}})
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true})
	if got := next.(model).filter; got != "" {
		t.Fatalf("filter = %q, want empty", got)
	}
}

func TestJumpGroupCycles(t *testing.T) {
	cfg, err := Load(writeTemp(t, sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(cfg.Entries)
	// Groups start at flat indices 0 (ungrouped, 2 entries), 2, 3.
	if got := m.jumpGroup(true); got != 2 {
		t.Errorf("forward from 0 = %d, want 2", got)
	}
	m.cursor = 3
	if got := m.jumpGroup(true); got != 0 {
		t.Errorf("forward from the last group should wrap to 0, got %d", got)
	}
	if got := m.jumpGroup(false); got != 2 {
		t.Errorf("back from 3 = %d, want 2", got)
	}
}
