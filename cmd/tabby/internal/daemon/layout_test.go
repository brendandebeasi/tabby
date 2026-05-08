package daemon

import (
	"strings"
	"testing"
)

func TestPlanWindowSizes(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		height    int
		ids       []string
		wantOps   int
		wantKinds []ResizeOpKind
	}{
		{
			name:    "no ids returns nil",
			width:   100,
			height:  40,
			ids:     nil,
			wantOps: 0,
		},
		{
			name:    "zero width returns nil",
			width:   0,
			height:  40,
			ids:     []string{"@1", "@2"},
			wantOps: 0,
		},
		{
			name:    "zero height returns nil",
			width:   100,
			height:  0,
			ids:     []string{"@1"},
			wantOps: 0,
		},
		{
			name:      "two windows produces two window ops",
			width:     120,
			height:    50,
			ids:       []string{"@1", "@2"},
			wantOps:   2,
			wantKinds: []ResizeOpKind{OpResizeWindow, OpResizeWindow},
		},
		{
			name:    "blank ids are skipped",
			width:   80,
			height:  24,
			ids:     []string{"@1", "", "  ", "@4"},
			wantOps: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := planWindowSizes(tt.width, tt.height, tt.ids)
			if len(ops) != tt.wantOps {
				t.Fatalf("ops count: got %d want %d (%v)", len(ops), tt.wantOps, ops)
			}
			for i, op := range ops {
				if op.Kind != OpResizeWindow {
					t.Errorf("op[%d].Kind: got %d want %d", i, op.Kind, OpResizeWindow)
				}
				if op.X != tt.width {
					t.Errorf("op[%d].X: got %d want %d", i, op.X, tt.width)
				}
				if op.Y != tt.height {
					t.Errorf("op[%d].Y: got %d want %d", i, op.Y, tt.height)
				}
				if !strings.HasPrefix(op.Target, "@") {
					t.Errorf("op[%d].Target: got %q want @-prefixed", i, op.Target)
				}
			}
		})
	}
}

func TestAtoiSafe(t *testing.T) {
	cases := map[string]struct {
		want int
		err  bool
	}{
		"0":         {0, false},
		"42":        {42, false},
		"  120  ":   {120, false},
		"-5":        {-5, false},
		"":          {0, true},
		"abc":       {0, true},
		"12a":       {0, true},
		"  ":        {0, true},
		"-":         {0, true}, // bare minus is not a valid int
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := atoiSafe(in)
			if want.err && err == nil {
				t.Fatalf("atoiSafe(%q): expected error, got %d", in, got)
			}
			if !want.err && err != nil {
				t.Fatalf("atoiSafe(%q): unexpected error %v", in, err)
			}
			if !want.err && got != want.want {
				t.Errorf("atoiSafe(%q) = %d; want %d", in, got, want.want)
			}
		})
	}
}

func TestFlushOpsBatchedNoOpEmpty(t *testing.T) {
	// Empty slice must not invoke tmux. Functional check: no panic, no
	// observable change. We can't easily intercept exec, but the function's
	// guard at the top is the contract — exercise it.
	flushOpsBatched(nil, "test_empty")
	flushOpsBatched([]ResizeOp{}, "test_empty_slice")
}
