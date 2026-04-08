package main

import "testing"

func TestWindowFocusRestoreTarget(t *testing.T) {
	tests := []struct {
		name      string
		active    string
		pending   string
		wantFocus string
	}{
		{
			name:      "no pending window",
			active:    "@1",
			pending:   "",
			wantFocus: "",
		},
		{
			name:      "pending matches active window",
			active:    "@1",
			pending:   "@1",
			wantFocus: "",
		},
		{
			name:      "pending new window differs from active window",
			active:    "@1",
			pending:   "@2",
			wantFocus: "@2",
		},
		{
			name:      "no active window but pending new window exists",
			active:    "",
			pending:   "@2",
			wantFocus: "@2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windowFocusRestoreTarget(tt.active, tt.pending)
			if got != tt.wantFocus {
				t.Fatalf("windowFocusRestoreTarget(%q, %q) = %q, want %q", tt.active, tt.pending, got, tt.wantFocus)
			}
		})
	}
}
