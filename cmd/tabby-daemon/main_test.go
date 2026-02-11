package main

import "testing"

func TestPaneTargetRegex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{
			name:   "single quoted",
			input:  "exec '/tmp/bin/pane-header' -session '$0' -window '@1' -pane '%12'",
			wantID: "%12",
		},
		{
			name:   "double quoted not matched",
			input:  "exec '/tmp/bin/pane-header' -session '$0' -window '@1' -pane \"%34\"",
			wantID: "",
		},
		{
			name:   "unquoted not matched",
			input:  "exec pane-header -session $0 -window @1 -pane %56",
			wantID: "",
		},
		{
			name:   "missing pane",
			input:  "exec pane-header -session $0 -window @1",
			wantID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := paneTargetRegex.FindStringSubmatch(tt.input)
			got := ""
			if len(matches) >= 2 {
				got = matches[1]
			}
			if got != tt.wantID {
				t.Fatalf("paneTargetRegex(%q) = %q, want %q", tt.input, got, tt.wantID)
			}
		})
	}
}
