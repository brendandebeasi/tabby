package colors

import (
	"math"
	"testing"
)

func TestGetLuminance(t *testing.T) {
	tests := []struct {
		name     string
		hexColor string
		want     float64
		delta    float64 // acceptable deviation
	}{
		{"black", "#000000", 0.0, 0.001},
		{"white", "#ffffff", 1.0, 0.001},
		{"mid gray", "#808080", 0.2159, 0.01}, // ~21.6% luminance
		{"pure red", "#ff0000", 0.2126, 0.01},
		{"pure green", "#00ff00", 0.7152, 0.01},
		{"pure blue", "#0000ff", 0.0722, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetLuminance(tt.hexColor)
			if math.Abs(got-tt.want) > tt.delta {
				t.Errorf("GetLuminance(%q) = %v, want %v (delta %v)", tt.hexColor, got, tt.want, tt.delta)
			}
		})
	}
}

func TestGetContrastRatio(t *testing.T) {
	tests := []struct {
		name  string
		fg    string
		bg    string
		want  float64
		delta float64
	}{
		{"black on white (max contrast)", "#000000", "#ffffff", 21.0, 0.1},
		{"white on black (max contrast)", "#ffffff", "#000000", 21.0, 0.1},
		{"same color (no contrast)", "#808080", "#808080", 1.0, 0.1},
		{"white on mid gray", "#ffffff", "#808080", 4.0, 0.5}, // ~4:1
		{"black on mid gray", "#000000", "#808080", 5.3, 0.5}, // ~5.3:1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetContrastRatio(tt.fg, tt.bg)
			if math.Abs(got-tt.want) > tt.delta {
				t.Errorf("GetContrastRatio(%q, %q) = %v, want %v (delta %v)", tt.fg, tt.bg, got, tt.want, tt.delta)
			}
		})
	}
}

func TestDeriveTextColor(t *testing.T) {
	tests := []struct {
		name    string
		bgColor string
		want    string
	}{
		{"dark background -> white text", "#000000", "#ffffff"},
		{"light background -> black text", "#ffffff", "#000000"},
		{"dark blue -> white text", "#1a1a2e", "#ffffff"},
		{"light yellow -> black text", "#f0f0d0", "#000000"},
		{"mid gray (dark) -> white text", "#404040", "#ffffff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveTextColor(tt.bgColor)
			if got != tt.want {
				t.Errorf("DeriveTextColor(%q) = %q, want %q", tt.bgColor, got, tt.want)
			}
		})
	}
}

func TestIsLightColor(t *testing.T) {
	tests := []struct {
		name     string
		hexColor string
		want     bool
	}{
		{"black", "#000000", false},
		{"white", "#ffffff", true},
		{"dark gray", "#333333", false},
		{"light gray", "#cccccc", true},
		{"mid gray (borderline)", "#808080", false}, // luminance ~0.216 < 0.5
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsLightColor(tt.hexColor)
			if got != tt.want {
				t.Errorf("IsLightColor(%q) = %v, want %v", tt.hexColor, got, tt.want)
			}
		})
	}
}

func TestLightenColorBy(t *testing.T) {
	tests := []struct {
		name     string
		hexColor string
		amount   float64
		want     string
	}{
		{"lighten black by 50%", "#000000", 0.5, "#7f7f7f"},
		{"lighten black by 100%", "#000000", 1.0, "#ffffff"},
		{"lighten white (no change)", "#ffffff", 0.5, "#ffffff"},
		{"lighten mid gray by 50%", "#808080", 0.5, "#bfbfbf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lightenColorBy(tt.hexColor, tt.amount)
			// Compare with some tolerance for rounding
			if !colorsClose(got, tt.want, 2) {
				t.Errorf("lightenColorBy(%q, %v) = %q, want %q", tt.hexColor, tt.amount, got, tt.want)
			}
		})
	}
}

func TestDarkenColorBy(t *testing.T) {
	tests := []struct {
		name     string
		hexColor string
		amount   float64
		want     string
	}{
		{"darken white by 50%", "#ffffff", 0.5, "#7f7f7f"},
		{"darken white by 100%", "#ffffff", 1.0, "#000000"},
		{"darken black (no change)", "#000000", 0.5, "#000000"},
		{"darken mid gray by 50%", "#808080", 0.5, "#404040"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := darkenColorBy(tt.hexColor, tt.amount)
			// Compare with some tolerance for rounding
			if !colorsClose(got, tt.want, 2) {
				t.Errorf("darkenColorBy(%q, %v) = %q, want %q", tt.hexColor, tt.amount, got, tt.want)
			}
		})
	}
}

func TestHexToRGB(t *testing.T) {
	tests := []struct {
		name     string
		hexColor string
		wantR    int64
		wantG    int64
		wantB    int64
	}{
		{"black", "#000000", 0, 0, 0},
		{"white", "#ffffff", 255, 255, 255},
		{"red", "#ff0000", 255, 0, 0},
		{"green", "#00ff00", 0, 255, 0},
		{"blue", "#0000ff", 0, 0, 255},
		{"invalid (short)", "#fff", -1, -1, -1},
		{"without hash prefix (valid)", "ffffff", 255, 255, 255},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, g, b := hexToRGB(tt.hexColor)
			if r != tt.wantR || g != tt.wantG || b != tt.wantB {
				t.Errorf("hexToRGB(%q) = (%d, %d, %d), want (%d, %d, %d)",
					tt.hexColor, r, g, b, tt.wantR, tt.wantG, tt.wantB)
			}
		})
	}
}

func TestRgbToHex(t *testing.T) {
	tests := []struct {
		name string
		r    int64
		g    int64
		b    int64
		want string
	}{
		{"black", 0, 0, 0, "#000000"},
		{"white", 255, 255, 255, "#ffffff"},
		{"red", 255, 0, 0, "#ff0000"},
		{"clamp overflow", 300, 300, 300, "#ffffff"},
		{"clamp negative", -10, -10, -10, "#000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rgbToHex(tt.r, tt.g, tt.b)
			if got != tt.want {
				t.Errorf("rgbToHex(%d, %d, %d) = %q, want %q", tt.r, tt.g, tt.b, got, tt.want)
			}
		})
	}
}

func TestEnsureContrast(t *testing.T) {
	tests := []struct {
		name     string
		fg       string
		bg       string
		minRatio float64
		// We don't check exact output, just that contrast is achieved
	}{
		{"already meets WCAG AA", "#ffffff", "#000000", 4.5},
		{"needs adjustment", "#808080", "#666666", 4.5},
		{"dark on dark", "#333333", "#222222", 4.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnsureContrast(tt.fg, tt.bg, tt.minRatio)
			ratio := GetContrastRatio(got, tt.bg)
			if ratio < tt.minRatio {
				t.Errorf("EnsureContrast(%q, %q, %v) = %q with ratio %v, want >= %v",
					tt.fg, tt.bg, tt.minRatio, got, ratio, tt.minRatio)
			}
		})
	}
}

// colorsClose checks if two hex colors are close within a tolerance (per channel)
func colorsClose(a, b string, tolerance int64) bool {
	r1, g1, b1 := hexToRGB(a)
	r2, g2, b2 := hexToRGB(b)
	if r1 < 0 || r2 < 0 {
		return a == b // invalid colors, just compare strings
	}
	return abs(r1-r2) <= tolerance && abs(g1-g2) <= tolerance && abs(b1-b2) <= tolerance
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
