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

func TestTmuxLayoutChecksum(t *testing.T) {
	// Reproduce checksums from real production layouts. Each input is the
	// layout-string body (everything after the leading "csum,"); the
	// expected value is what tmux actually emitted.
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "@130 sane",
			body: "62x27,0,0[62x23,0,0{22x23,0,0,1562,39x23,23,0[39x1,23,0,1640,39x21,23,2,363]},62x3,0,24,1634]",
			want: "a009",
		},
		{
			name: "@484 squished",
			body: "62x27,0,0{22x27,0,0,1643,39x27,23,0[39x23,23,0[39x1,23,0,1641,39x21,23,2,1522],39x3,23,24,1635]}",
			want: "e1f3",
		},
		{
			name: "@546 tabby",
			body: "62x27,0,0[62x23,0,0{22x23,0,0,1621,39x23,23,0[39x1,23,0,1642,39x21,23,2,1620]},62x3,0,24,1636]",
			want: "2254",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tmuxLayoutChecksum(tt.body); got != tt.want {
				t.Errorf("tmuxLayoutChecksum() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSidebarPlusFooterLayout(t *testing.T) {
	// Should reproduce @130's layout string exactly: 62x27 window, 22-col
	// sidebar, 3-row footer bar, panes (1562, 1640, 363, 1634).
	got := buildSidebarPlusFooterLayout(62, 27, 22, 3, 1562, 1640, 363, 1634)
	want := "a009,62x27,0,0[62x23,0,0{22x23,0,0,1562,39x23,23,0[39x1,23,0,1640,39x21,23,2,363]},62x3,0,24,1634]"
	if got != want {
		t.Errorf("buildSidebarPlusFooterLayout()\n  got:  %s\n  want: %s", got, want)
	}

	// Degenerate dimensions return "".
	if buildSidebarPlusFooterLayout(0, 27, 22, 3, 1, 2, 3, 4) != "" {
		t.Error("expected empty for zero windowW")
	}
	if buildSidebarPlusFooterLayout(62, 5, 22, 3, 1, 2, 3, 4) != "" {
		t.Error("expected empty when windowH leaves no room for content")
	}
}

func TestLooksMalformedLayout(t *testing.T) {
	tests := []struct {
		name   string
		layout string
		want   bool
	}{
		{
			// @130 from production: outer is vertical split, footer 62x3 at
			// y=24 spans full window width.
			name:   "good full-width footer",
			layout: "a009,62x27,0,0[62x23,0,0{22x23,0,0,1562,39x23,23,0[39x1,23,0,1640,39x21,23,2,363]},62x3,0,24,1634]",
			want:   false,
		},
		{
			// @484 from production: outer is horizontal split, footer 39x3
			// nested under the right side — only 39 cols wide instead of 62.
			name:   "squished footer nested under content",
			layout: "e1f3,62x27,0,0{22x27,0,0,1643,39x27,23,0[39x23,23,0[39x1,23,0,1641,39x21,23,2,1522],39x3,23,24,1635]}",
			want:   true,
		},
		{
			// @546 from production: another good vertical-outer layout.
			name:   "good footer, different IDs",
			layout: "2254,62x27,0,0[62x23,0,0{22x23,0,0,1621,39x23,23,0[39x1,23,0,1642,39x21,23,2,1620]},62x3,0,24,1636]",
			want:   false,
		},
		{
			// Single full-window pane; no footer to validate.
			name:   "single pane, no footer",
			layout: "1234,80x24,0,0,1",
			want:   false,
		},
		{
			// Empty / malformed input shouldn't trip the validator.
			name:   "empty",
			layout: "",
			want:   false,
		},
		{
			name:   "no checksum prefix",
			layout: "62x27,0,0[...]",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksMalformedLayout(tt.layout); got != tt.want {
				t.Errorf("looksMalformedLayout(%q) = %v, want %v", tt.layout, got, tt.want)
			}
		})
	}
}

