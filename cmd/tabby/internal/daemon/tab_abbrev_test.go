package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAbbreviateFolder(t *testing.T) {
	cases := []struct{ in, want string }{
		// long single words -> first letter + following consonants (dedup), max 3
		{"tabby", "TBY"},
		{"config", "CNF"},
		{"committee", "CMT"},
		{"project", "PRJ"},
		// short single words (<=4 runes) kept whole, upper-cased
		{"foo", "FOO"},
		{"src", "SRC"},
		{"node", "NODE"},
		{"v3", "V3"},
		{"x", "X"},
		// multi-word (separators / camelCase) -> initials, max 4
		{"claude-flow", "CF"},
		{"my_cool_project", "MCP"},
		{"myProjectName", "MPN"},
		{"a-b-c-d-e", "ABCD"},
		{"dot.separated.name", "DSN"},
		// whitespace / empty
		{"  tabby  ", "TBY"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, abbreviateFolder(tc.in))
		})
	}
}

func TestSplitWords(t *testing.T) {
	assert.Equal(t, []string{"claude", "flow"}, splitWords("claude-flow"))
	assert.Equal(t, []string{"my", "Project", "Name"}, splitWords("myProjectName"))
	assert.Equal(t, []string{"tabby"}, splitWords("tabby"))
	assert.Empty(t, splitWords("  "))
}
