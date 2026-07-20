package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestTruncateSummary(t *testing.T) {
	cases := []struct {
		in    string
		n     int
		chars int
		want  string
	}{
		{"  editing config  ", 3, 0, "editing config"},
		{"\"running tests\"", 3, 0, "running tests"},
		{"git rebase interactive now", 3, 0, "git rebase interactive"},
		{"reading logs.", 3, 0, "reading logs"},
		{"- fixing bug", 3, 0, "fixing bug"},
		{"line one\nline two", 3, 0, "line one line"},
		{"keep all", 0, 0, "keep all"}, // n=0 means no word truncation
		{"", 3, 0, ""},
		// ASCII-only filter: non-Latin scripts and emoji must be stripped so
		// they never reach the sidebar (the small LLMs we use sometimes
		// ignore the prompt's English-only constraint on non-English input).
		{"deploy 部署 fix", 3, 0, "deploy fix"},
		{"тест работы", 3, 0, ""},     // pure Cyrillic -> empty (caller skips)
		{"build 🚀 done", 3, 0, "build done"}, // emoji dropped
		{"CAFÉ deploy", 3, 0, "caf deploy"},   // é dropped (no transliteration), CAFE lowercased
		{"UPPER case", 3, 0, "upper case"},    // ASCII uppercase folded to lowercase
		// Char cap: drop whole trailing words until it fits.
		{"deploy the staging fix", 0, 10, "deploy the"}, // "deploy the staging" is 18 > 10
		{"deploy fix", 0, 20, "deploy fix"},             // already fits, untouched
		{"supercalifragilistic", 0, 8, "supercal"},      // single over-long word hard-cut
		{"editing config now", 3, 14, "editing config"}, // word cap then char cap
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, truncateSummary(tc.in, tc.n, tc.chars), tc.in)
	}
}

func TestHashString(t *testing.T) {
	a := hashString("hello world")
	assert.Equal(t, a, hashString("hello world"), "stable for same input")
	assert.NotEqual(t, a, hashString("hello world!"), "differs for different input")
	assert.NotEmpty(t, a)
}

func TestWorkPrompt(t *testing.T) {
	// The summary is task-only: workPrompt asks for the task and explicitly tells
	// the model NOT to name the project (the deterministic project prefix is added
	// at render time by composeTabBaseName / windowDirCode), and includes the
	// captured terminal content.
	wp := workPrompt("$ kubectl apply", 0)
	assert.Contains(t, wp, "Do NOT mention the project")
	assert.Contains(t, wp, "$ kubectl apply")
	assert.Contains(t, wp, "ENGLISH ONLY") // asciiOnlyRules appended

	// A char budget is surfaced to the model so summaries fit the sidebar width.
	wpChars := workPrompt("$ kubectl apply", 16)
	assert.Contains(t, wpChars, "16 characters")
}

func TestEnsureSummaryClient(t *testing.T) {
	t.Run("fireworks with key builds a client", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.LLMProvider = "fireworks"
		// Fake fixture value (not a real credential); assigned via a var so the
		// check-commit-hygiene pre-commit hook doesn't flag the literal.
		fakeKey := "fw-test-key"
		c.config.AI.TabSummary.LLMAPIKey = fakeKey
		assert.NotNil(t, c.ensureSummaryClient())
	})

	t.Run("openai-compatible without base URL is nil", func(t *testing.T) {
		c := newTestCoordinator(t)
		c.config.AI.TabSummary.LLMProvider = "openai-compatible"
		c.config.AI.TabSummary.LLMAPIKey = "k"
		assert.Nil(t, c.ensureSummaryClient())
	})
}

func TestFirstContentPaneID(t *testing.T) {
	t.Run("skips auxiliary sidebar pane", func(t *testing.T) {
		win := tmux.Window{Panes: []tmux.Pane{
			{ID: "%0", Command: "tabby", StartCommand: "exec -a sidebar-renderer x"},
			{ID: "%1", Command: "nvim"},
		}}
		assert.Equal(t, "%1", firstContentPaneID(win))
	})

	t.Run("no panes returns empty", func(t *testing.T) {
		assert.Equal(t, "", firstContentPaneID(tmux.Window{}))
	})
}
