package colors

import (
	"testing"
)

func TestEnsureContrast_AlreadyMeetsRatio(t *testing.T) {
	fg := "#ffffff"
	bg := "#000000"
	ratio := 21.0
	got := EnsureContrast(fg, bg, ratio)
	if got != fg {
		t.Errorf("EnsureContrast with already-sufficient ratio should return original, got %s", got)
	}
}

func TestEnsureContrast_LightensForeground(t *testing.T) {
	fg := "#808080"
	bg := "#000000"
	ratio := 5.0
	got := EnsureContrast(fg, bg, ratio)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("EnsureContrast should return valid hex, got %s", got)
	}
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast result should meet ratio %.1f, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_DarkensForeground(t *testing.T) {
	fg := "#808080"
	bg := "#ffffff"
	ratio := 5.0
	got := EnsureContrast(fg, bg, ratio)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("EnsureContrast should return valid hex, got %s", got)
	}
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast result should meet ratio %.1f, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_FallbackToBlack(t *testing.T) {
	fg := "#cccccc"
	bg := "#ffffff"
	ratio := 10.0
	got := EnsureContrast(fg, bg, ratio)
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast result should meet ratio %.1f, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_FallbackToWhite(t *testing.T) {
	fg := "#333333"
	bg := "#000000"
	ratio := 10.0
	got := EnsureContrast(fg, bg, ratio)
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast result should meet ratio %.1f, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_InvalidForeground(t *testing.T) {
	fg := "notacolor"
	bg := "#000000"
	ratio := 5.0
	got := EnsureContrast(fg, bg, ratio)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("EnsureContrast with invalid fg should still return valid hex, got %s", got)
	}
}

func TestIsLightColor_White(t *testing.T) {
	if !IsLightColor("#ffffff") {
		t.Error("white should be light")
	}
}

func TestIsLightColor_Black(t *testing.T) {
	if IsLightColor("#000000") {
		t.Error("black should not be light")
	}
}

func TestIsLightColor_MidGray(t *testing.T) {
	if !IsLightColor("#c0c0c0") {
		t.Error("light gray should be light (luminance > 0.5)")
	}
}

func TestGetContrastRatio_WhiteOnBlack(t *testing.T) {
	ratio := GetContrastRatio("#ffffff", "#000000")
	if ratio < 20.0 {
		t.Errorf("white on black should have high contrast, got %.1f", ratio)
	}
}

func TestGetContrastRatio_SameColor(t *testing.T) {
	ratio := GetContrastRatio("#808080", "#808080")
	if ratio != 1.0 {
		t.Errorf("same color should have contrast ratio 1.0, got %.1f", ratio)
	}
}

func TestGetContrastRatio_InvalidColor(t *testing.T) {
	ratio := GetContrastRatio("notacolor", "#000000")
	if ratio < 0 {
		t.Errorf("invalid color should still return valid ratio, got %.1f", ratio)
	}
}

func TestGammaSRGB_LowValues(t *testing.T) {
	val := 0.03
	result := gammaSRGB(val)
	if result <= 0 || result >= val {
		t.Errorf("gammaSRGB(0.03) should be positive and less than input, got %.6f", result)
	}
}

func TestGammaSRGB_HighValues(t *testing.T) {
	val := 0.5
	result := gammaSRGB(val)
	if result <= 0 || result >= 1 {
		t.Errorf("gammaSRGB(0.5) should be between 0 and 1, got %.6f", result)
	}
}

func TestGammaSRGB_Boundary(t *testing.T) {
	val := 0.03928
	result := gammaSRGB(val)
	if result <= 0 {
		t.Errorf("gammaSRGB(0.03928) should be positive, got %.6f", result)
	}
}

func TestHexToRGB_ValidColors(t *testing.T) {
	tests := []struct {
		hex string
		r   int64
		g   int64
		b   int64
	}{
		{"#000000", 0, 0, 0},
		{"#ffffff", 255, 255, 255},
		{"#ff0000", 255, 0, 0},
		{"#00ff00", 0, 255, 0},
		{"#0000ff", 0, 0, 255},
		{"#808080", 128, 128, 128},
	}
	for _, tt := range tests {
		r, g, b := hexToRGB(tt.hex)
		if r != tt.r || g != tt.g || b != tt.b {
			t.Errorf("hexToRGB(%s) = (%d, %d, %d), want (%d, %d, %d)", tt.hex, r, g, b, tt.r, tt.g, tt.b)
		}
	}
}

func TestHexToRGB_InvalidFormats(t *testing.T) {
	tests := []string{"#fff", "#00", "notacolor", "#gggggg", ""}
	for _, hex := range tests {
		r, g, b := hexToRGB(hex)
		if r != -1 || g != -1 || b != -1 {
			t.Errorf("hexToRGB(%s) should return -1,-1,-1 for invalid, got (%d, %d, %d)", hex, r, g, b)
		}
	}
}

func TestLightenColorBy_EdgeCases(t *testing.T) {
	tests := []struct {
		hex    string
		amount float64
		desc   string
	}{
		{"#000000", 0.0, "black with 0 amount"},
		{"#000000", 1.0, "black with full amount"},
		{"#ffffff", 0.5, "white with 0.5 amount"},
		{"#808080", 0.25, "gray with 0.25 amount"},
	}
	for _, tt := range tests {
		result := lightenColorBy(tt.hex, tt.amount)
		if len(result) != 7 || result[0] != '#' {
			t.Errorf("lightenColorBy(%s, %.2f) %s should return valid hex, got %s", tt.hex, tt.amount, tt.desc, result)
		}
	}
}

func TestLightenColorBy_Invalid(t *testing.T) {
	result := lightenColorBy("notacolor", 0.5)
	if result != "notacolor" {
		t.Errorf("lightenColorBy with invalid color should return original, got %s", result)
	}
}

func TestDarkenColorBy_EdgeCases(t *testing.T) {
	tests := []struct {
		hex    string
		amount float64
		desc   string
	}{
		{"#ffffff", 0.0, "white with 0 amount"},
		{"#ffffff", 1.0, "white with full amount"},
		{"#000000", 0.5, "black with 0.5 amount"},
		{"#808080", 0.25, "gray with 0.25 amount"},
	}
	for _, tt := range tests {
		result := darkenColorBy(tt.hex, tt.amount)
		if len(result) != 7 || result[0] != '#' {
			t.Errorf("darkenColorBy(%s, %.2f) %s should return valid hex, got %s", tt.hex, tt.amount, tt.desc, result)
		}
	}
}

func TestDarkenColorBy_Invalid(t *testing.T) {
	result := darkenColorBy("notacolor", 0.5)
	if result != "notacolor" {
		t.Errorf("darkenColorBy with invalid color should return original, got %s", result)
	}
}

func TestRgbToHex_Clamping(t *testing.T) {
	tests := []struct {
		r, g, b int64
		desc    string
	}{
		{-10, 128, 255, "negative r"},
		{256, 128, 255, "overflow r"},
		{128, -5, 255, "negative g"},
		{128, 256, 255, "overflow g"},
		{128, 255, -1, "negative b"},
		{128, 255, 300, "overflow b"},
	}
	for _, tt := range tests {
		result := rgbToHex(tt.r, tt.g, tt.b)
		if len(result) != 7 || result[0] != '#' {
			t.Errorf("rgbToHex(%d, %d, %d) %s should return valid hex, got %s", tt.r, tt.g, tt.b, tt.desc, result)
		}
	}
}

func TestToHex_SingleDigit(t *testing.T) {
	result := toHex(5)
	if result != "05" {
		t.Errorf("toHex(5) should return '05', got '%s'", result)
	}
}

func TestToHex_DoubleDigit(t *testing.T) {
	result := toHex(255)
	if result != "ff" {
		t.Errorf("toHex(255) should return 'ff', got '%s'", result)
	}
}

func TestToHex_Zero(t *testing.T) {
	result := toHex(0)
	if result != "00" {
		t.Errorf("toHex(0) should return '00', got '%s'", result)
	}
}

func TestEnsureContrast_AdjustmentLoopLighten(t *testing.T) {
	fg := "#999999"
	bg := "#000000"
	ratio := 7.0
	got := EnsureContrast(fg, bg, ratio)
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast should meet ratio %.1f through adjustment, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_AdjustmentLoopDarken(t *testing.T) {
	fg := "#666666"
	bg := "#ffffff"
	ratio := 7.0
	got := EnsureContrast(fg, bg, ratio)
	gotRatio := GetContrastRatio(got, bg)
	if gotRatio < ratio {
		t.Errorf("EnsureContrast should meet ratio %.1f through adjustment, got %.1f", ratio, gotRatio)
	}
}

func TestEnsureContrast_InvalidBackground(t *testing.T) {
	fg := "#ffffff"
	bg := "notacolor"
	ratio := 5.0
	got := EnsureContrast(fg, bg, ratio)
	if len(got) != 7 || got[0] != '#' {
		t.Errorf("EnsureContrast with invalid bg should return valid hex, got %s", got)
	}
}
