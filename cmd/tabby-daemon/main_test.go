package main

import "testing"

func TestWindowTargetRegex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{
			name:   "single quoted window",
			input:  "exec '/tmp/bin/window-header' -session '$0' -window '@1'",
			wantID: "@1",
		},
		{
			name:   "double quoted not matched",
			input:  "exec '/tmp/bin/window-header' -session '$0' -window \"@2\"",
			wantID: "",
		},
		{
			name:   "unquoted not matched",
			input:  "exec window-header -session $0 -window @3",
			wantID: "",
		},
		{
			name:   "missing window",
			input:  "exec window-header -session $0",
			wantID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := windowTargetRegex.FindStringSubmatch(tt.input)
			got := ""
			if len(matches) >= 2 {
				got = matches[1]
			}
			if got != tt.wantID {
				t.Fatalf("windowTargetRegex(%q) = %q, want %q", tt.input, got, tt.wantID)
			}
		})
	}
}
