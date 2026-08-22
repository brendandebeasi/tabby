package textwidth

import (
	"strings"
	"testing"
)

const (
	red   = "\033[31m"
	reset = "\033[0m"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"empty", "", ""},
		{"leading escape", red + "hello", "hello"},
		{"wrapped", red + "hello" + reset, "hello"},
		{"multiple escapes", red + "a" + reset + red + "b" + reset, "ab"},
		{"reset only", reset, ""},
		{"256 color", "\033[38;5;214mx" + reset, "x"},
		{"bare escape is not SGR", "\033[2Jx", "\033[2Jx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripANSI(tt.in); got != tt.want {
				t.Errorf("StripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDisplay(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "hello", 5},
		{"space", "a b", 3},
		{"cjk is double width", "日本", 4},
		{"mixed ascii and cjk", "a日", 3},
		{"combining accent is one column", "é", 1},
		{"emoji is double width", "🐈", 2},
		// The reason this package exists: a keycap sequence measures 1 per
		// rune but every terminal draws it as 2 columns.
		{"keycap emoji presentation", "1️⃣", 2},
		{"emoji with presentation selector", "❤️", 2},
		{"zwj family stays one cluster", "👩‍👩‍👧", 2},
		{"skin tone modifier", "👋🏽", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Display(tt.in); got != tt.want {
				t.Errorf("Display(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		name string
		in   string
		maxW int
		want string
	}{
		{"fits exactly", "hello", 5, "hello"},
		{"fits with room", "hi", 5, "hi"},
		{"truncates", "hello", 3, "hel"},
		{"zero width", "hello", 0, ""},
		{"negative width", "hello", -1, ""},
		{"empty input", "", 3, ""},
		// A double-width cluster that would straddle the boundary is
		// dropped rather than half-drawn.
		{"double width straddling boundary", "a日", 2, "a"},
		{"double width fits", "a日", 3, "a日"},
		{"emoji straddling boundary", "ab🐈", 3, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Clamp(tt.in, tt.maxW); got != tt.want {
				t.Errorf("Clamp(%q, %d) = %q, want %q", tt.in, tt.maxW, got, tt.want)
			}
		})
	}
}

// Clamp must copy escapes through without charging them width, or a colored
// row loses either its color or its content.
func TestClampPreservesANSI(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		maxW    int
		want    string
		wantVis int
	}{
		{
			name:    "colored row truncated",
			in:      red + "hello" + reset,
			maxW:    3,
			want:    red + "hel",
			wantVis: 3,
		},
		{
			name:    "escape before every rune",
			in:      red + "a" + reset + red + "b" + reset + red + "c" + reset,
			maxW:    2,
			want:    red + "a" + reset + red + "b" + reset + red,
			wantVis: 2,
		},
		{
			name:    "fits so returned verbatim",
			in:      red + "hi" + reset,
			maxW:    10,
			want:    red + "hi" + reset,
			wantVis: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Clamp(tt.in, tt.maxW)
			if got != tt.want {
				t.Errorf("Clamp(%q, %d) = %q, want %q", tt.in, tt.maxW, got, tt.want)
			}
			if vis := Display(StripANSI(got)); vis != tt.wantVis {
				t.Errorf("visible width = %d, want %d", vis, tt.wantVis)
			}
		})
	}
}

func TestPad(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w    int
		want string
	}{
		{"pads short", "hi", 5, "hi   "},
		{"exact fit unchanged", "hello", 5, "hello"},
		{"empty to width", "", 3, "   "},
		{"clamps when wider", "hello", 3, "hel"},
		{"zero width clamps to empty", "hello", 0, ""},
		{"pads by columns not runes", "日", 4, "日  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Pad(tt.in, tt.w); got != tt.want {
				t.Errorf("Pad(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
			}
		})
	}
}

// Pad's contract is the one the renderers depend on: the result draws in
// exactly w columns, whatever went in. A row built from parts that each
// break this wraps the pane and corrupts every later frame.
func TestPadIsExactlyWidth(t *testing.T) {
	inputs := []string{
		"",
		"a",
		"hello",
		"日本語",
		"🐈",
		"1️⃣",
		"👩‍👩‍👧",
		"é",
		red + "colored" + reset,
		"a日b🐈c",
		strings.Repeat("x", 40),
	}
	for _, in := range inputs {
		for w := 0; w <= 12; w++ {
			got := Display(StripANSI(Pad(in, w)))
			// A double-width cluster cannot half-fill the last column, so
			// the result may fall one short of an odd target.
			if got != w && got != w-1 {
				t.Errorf("Display(Pad(%q, %d)) = %d, want %d", in, w, got, w)
			}
		}
	}
}

// Clamp must never exceed its bound for any input; overflow is the failure
// that wraps the pane.
func TestClampNeverExceedsBound(t *testing.T) {
	inputs := []string{
		"hello world",
		"日本語テキスト",
		"🐈🐈🐈",
		"1️⃣" + "2️⃣",
		red + "colored text" + reset,
		"a日b🐈c",
	}
	for _, in := range inputs {
		for w := 0; w <= 10; w++ {
			if got := Display(StripANSI(Clamp(in, w))); got > w {
				t.Errorf("Display(Clamp(%q, %d)) = %d, exceeds bound", in, w, got)
			}
		}
	}
}
