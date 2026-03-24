package colors

import (
	"testing"
)

func TestHexToTmuxColor_ValidColors(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want string
	}{
		{"black", "#000000", "colour16"},
		{"white", "#ffffff", "colour231"},
		{"red", "#ff0000", "colour196"},
		{"green", "#00ff00", "colour46"},
		{"blue", "#0000ff", "colour21"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HexToTmuxColor(tt.hex)
			if got != tt.want {
				t.Errorf("HexToTmuxColor(%q) = %q, want %q", tt.hex, got, tt.want)
			}
		})
	}
}

func TestHexToTmuxColor_NoHashPrefix(t *testing.T) {
	got := HexToTmuxColor("ffffff")
	if got != "colour231" {
		t.Errorf("HexToTmuxColor(\"ffffff\") = %q, want colour231", got)
	}
}

func TestHexToTmuxColor_InvalidLength(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"too short", "#fff"},
		{"too long", "#fffffff"},
		{"empty", ""},
		{"only hash", "#"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HexToTmuxColor(tt.hex)
			if got != "colour0" {
				t.Errorf("HexToTmuxColor(%q) = %q, want colour0", tt.hex, got)
			}
		})
	}
}

func TestHexToTmuxColor_InvalidHex(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"non-hex chars", "#gggggg"},
		{"mixed invalid", "#ff00gg"},
		{"spaces", "#ff 00 00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HexToTmuxColor(tt.hex)
			if got != "colour0" {
				t.Errorf("HexToTmuxColor(%q) = %q, want colour0", tt.hex, got)
			}
		})
	}
}

func TestAdjustHex_Brighten(t *testing.T) {
	tests := []struct {
		name   string
		hex    string
		amount float64
		want   string
	}{
		{"brighten black by 0.1", "#000000", 0.1, "#191919"},
		{"brighten black by 0.5", "#000000", 0.5, "#7f7f7f"},
		{"brighten gray by 0.2", "#808080", 0.2, "#b3b3b3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustHex(tt.hex, tt.amount)
			if got != tt.want {
				t.Errorf("AdjustHex(%q, %v) = %q, want %q", tt.hex, tt.amount, got, tt.want)
			}
		})
	}
}

func TestAdjustHex_Darken(t *testing.T) {
	tests := []struct {
		name   string
		hex    string
		amount float64
		want   string
	}{
		{"darken white by -0.1", "#ffffff", -0.1, "#e5e5e5"},
		{"darken white by -0.5", "#ffffff", -0.5, "#7f7f7f"},
		{"darken gray by -0.2", "#808080", -0.2, "#4d4d4d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustHex(tt.hex, tt.amount)
			if got != tt.want {
				t.Errorf("AdjustHex(%q, %v) = %q, want %q", tt.hex, tt.amount, got, tt.want)
			}
		})
	}
}

func TestAdjustHex_Clamping(t *testing.T) {
	tests := []struct {
		name   string
		hex    string
		amount float64
		want   string
	}{
		{"clamp to white", "#ffffff", 1.0, "#ffffff"},
		{"clamp to black", "#000000", -1.0, "#000000"},
		{"clamp high", "#ffffff", 0.5, "#ffffff"},
		{"clamp low", "#000000", -0.5, "#000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustHex(tt.hex, tt.amount)
			if got != tt.want {
				t.Errorf("AdjustHex(%q, %v) = %q, want %q", tt.hex, tt.amount, got, tt.want)
			}
		})
	}
}

func TestAdjustHex_NoHashPrefix(t *testing.T) {
	got := AdjustHex("808080", 0.1)
	if got != "#999999" {
		t.Errorf("AdjustHex(\"808080\", 0.1) = %q, want #999999", got)
	}
}

func TestAdjustHex_InvalidLength(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"too short", "#fff"},
		{"too long", "#fffffff"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustHex(tt.hex, 0.1)
			if got != tt.hex {
				t.Errorf("AdjustHex(%q, 0.1) = %q, want %q (unchanged)", tt.hex, got, tt.hex)
			}
		})
	}
}

func TestAdjustHex_InvalidHex(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"non-hex chars", "#gggggg"},
		{"mixed invalid", "#ff00gg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdjustHex(tt.hex, 0.1)
			if got != tt.hex {
				t.Errorf("AdjustHex(%q, 0.1) = %q, want %q (unchanged)", tt.hex, got, tt.hex)
			}
		})
	}
}

func TestAdjustHex_ZeroAmount(t *testing.T) {
	got := AdjustHex("#808080", 0)
	if got != "#808080" {
		t.Errorf("AdjustHex(\"#808080\", 0) = %q, want #808080", got)
	}
}

func TestAdjustHex_PerChannel(t *testing.T) {
	got := AdjustHex("#ff0000", -0.5)
	if got != "#7f0000" {
		t.Errorf("AdjustHex(\"#ff0000\", -0.5) = %q, want #7f0000", got)
	}
}

func TestAdjustHex_LargePositiveAmount(t *testing.T) {
	got := AdjustHex("#000000", 2.0)
	if got != "#ffffff" {
		t.Errorf("AdjustHex(\"#000000\", 2.0) = %q, want #ffffff (clamped)", got)
	}
}

func TestAdjustHex_LargeNegativeAmount(t *testing.T) {
	got := AdjustHex("#ffffff", -2.0)
	if got != "#000000" {
		t.Errorf("AdjustHex(\"#ffffff\", -2.0) = %q, want #000000 (clamped)", got)
	}
}
