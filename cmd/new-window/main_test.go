package main

import (
	"testing"
)

func TestShSingleQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "''",
		},
		{
			name:     "plain string",
			input:    "hello",
			expected: "'hello'",
		},
		{
			name:     "string with single quote",
			input:    "it's",
			expected: "'it'\\''s'",
		},
		{
			name:     "string with path",
			input:    "/home/user/my project",
			expected: "'/home/user/my project'",
		},
		{
			name:     "string with multiple single quotes",
			input:    "can't won't",
			expected: "'can'\\''t won'\\''t'",
		},
		{
			name:     "only single quote",
			input:    "'",
			expected: "''\\'''",
		},
		{
			name:     "multiple consecutive quotes",
			input:    "''",
			expected: "''\\'''\\'''",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shSingleQuote(tt.input)
			if result != tt.expected {
				t.Errorf("shSingleQuote(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFirstMatchingToken(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		prefix   string
		expected string
	}{
		{
			name:     "empty output",
			output:   "",
			prefix:   "@",
			expected: "",
		},
		{
			name:     "single line with matching prefix",
			output:   "@1",
			prefix:   "@",
			expected: "@1",
		},
		{
			name:     "multiple lines with match on second",
			output:   "some text\n@5 extra",
			prefix:   "@",
			expected: "@5",
		},
		{
			name:     "line with prefix and extra tokens",
			output:   "@2 extra stuff",
			prefix:   "@",
			expected: "@2",
		},
		{
			name:     "prefix not found",
			output:   "no match here\nstill nothing",
			prefix:   "@",
			expected: "",
		},
		{
			name:     "output with whitespace and empty lines",
			output:   "  \n\n@3\n  ",
			prefix:   "@",
			expected: "@3",
		},
		{
			name:     "pane ID prefix",
			output:   "%0\n%1",
			prefix:   "%",
			expected: "%0",
		},
		{
			name:     "first match wins",
			output:   "@1\n@2\n@3",
			prefix:   "@",
			expected: "@1",
		},
		{
			name:     "prefix with special chars",
			output:   "window:0\nwindow:1",
			prefix:   "window:",
			expected: "window:0",
		},
		{
			name:     "line with only whitespace before match",
			output:   "   @4",
			prefix:   "@",
			expected: "@4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstMatchingToken(tt.output, tt.prefix)
			if result != tt.expected {
				t.Errorf("firstMatchingToken(%q, %q) = %q, want %q", tt.output, tt.prefix, result, tt.expected)
			}
		})
	}
}
