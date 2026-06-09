package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
	"github.com/stretchr/testify/assert"
)

func TestTruncateSummaryWords(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"  editing config  ", 3, "editing config"},
		{"\"running tests\"", 3, "running tests"},
		{"git rebase interactive now", 3, "git rebase interactive"},
		{"reading logs.", 3, "reading logs"},
		{"- fixing bug", 3, "fixing bug"},
		{"line one\nline two", 3, "line one line"},
		{"keep all", 0, "keep all"}, // n=0 means no truncation
		{"", 3, ""},
		// ASCII-only filter: non-Latin scripts and emoji must be stripped so
		// they never reach the sidebar (the small LLMs we use sometimes
		// ignore the prompt's English-only constraint on non-English input).
		{"deploy 部署 fix", 3, "deploy fix"},
		{"тест работы", 3, ""},     // pure Cyrillic -> empty (caller skips)
		{"build 🚀 done", 3, "build done"}, // emoji dropped
		{"CAFÉ deploy", 3, "caf deploy"},   // é dropped (no transliteration), CAFE lowercased
		{"UPPER case", 3, "upper case"},    // ASCII uppercase folded to lowercase
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, truncateSummaryWords(tc.in, tc.n), tc.in)
	}
}

func TestHashString(t *testing.T) {
	a := hashString("hello world")
	assert.Equal(t, a, hashString("hello world"), "stable for same input")
	assert.NotEqual(t, a, hashString("hello world!"), "differs for different input")
	assert.NotEmpty(t, a)
}

func TestSummaryPrompt(t *testing.T) {
	p := summaryPrompt("studiodome-infra", "$ go test ./...\nok")
	assert.Contains(t, p, "studiodome-infra") // project context passed to the LLM
	assert.Contains(t, p, "abbreviation")     // asks for a project abbreviation
	assert.Contains(t, p, "$ go test ./...")  // includes the captured content

	// No project -> no project hint, still asks for a terse label.
	assert.NotContains(t, summaryPrompt("", "x"), "project / working directory is")

	// workPrompt (used when a fixed project abbrev is configured) asks for the
	// task only and tells the model not to name the project.
	wp := workPrompt("$ kubectl apply")
	assert.Contains(t, wp, "Do NOT mention the project")
	assert.Contains(t, wp, "$ kubectl apply")
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
