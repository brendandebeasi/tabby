package main

import (
	"testing"
)

func TestIsSkip_SidebarRenderer(t *testing.T) {
	if !isSkip("sidebar-renderer") {
		t.Fatal("sidebar-renderer should be skipped")
	}
}

func TestIsSkip_Tabbar(t *testing.T) {
	if !isSkip("tabbar") {
		t.Fatal("tabbar should be skipped")
	}
}

func TestIsSkip_PaneBar(t *testing.T) {
	if !isSkip("pane-bar") {
		t.Fatal("pane-bar should be skipped")
	}
}

func TestIsSkip_CaseInsensitive(t *testing.T) {
	if !isSkip("SIDEBAR-RENDER") {
		t.Fatal("isSkip should be case-insensitive")
	}
}

func TestIsSkip_NormalCommand(t *testing.T) {
	if isSkip("zsh") {
		t.Fatal("zsh should not be skipped")
	}
	if isSkip("vim") {
		t.Fatal("vim should not be skipped")
	}
	if isSkip("bash") {
		t.Fatal("bash should not be skipped")
	}
}

func TestIsHeader_PaneHeader(t *testing.T) {
	if !isHeader("pane-header") {
		t.Fatal("pane-header should be a header")
	}
}

func TestIsHeader_CaseInsensitive(t *testing.T) {
	if !isHeader("PANE-HEADER") {
		t.Fatal("isHeader should be case-insensitive")
	}
}

func TestIsHeader_NormalCommand(t *testing.T) {
	if isHeader("zsh") {
		t.Fatal("zsh should not be a header")
	}
}

func TestIsUtility_Skip(t *testing.T) {
	if !isUtility("tabbar") {
		t.Fatal("tabbar should be utility")
	}
}

func TestIsUtility_Header(t *testing.T) {
	if !isUtility("pane-header") {
		t.Fatal("pane-header should be utility")
	}
}

func TestIsUtility_Normal(t *testing.T) {
	if isUtility("zsh") {
		t.Fatal("zsh should not be utility")
	}
}

func TestFilterContent_RemovesUtility(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", active: true, command: "zsh", left: 0},
		{id: "%2", active: false, command: "tabbar", left: 5},
		{id: "%3", active: false, command: "pane-header", left: 0},
		{id: "%4", active: false, command: "vim", left: 10},
	}
	content := filterContent(panes)
	if len(content) != 2 {
		t.Fatalf("filterContent should keep 2 content panes, got %d", len(content))
	}
	for _, p := range content {
		if isUtility(p.command) {
			t.Fatalf("filterContent should not include utility pane %s", p.command)
		}
	}
}

func TestFilterContent_Empty(t *testing.T) {
	if got := filterContent(nil); got != nil {
		t.Fatalf("filterContent(nil) should return nil, got %v", got)
	}
}

func TestFilterContent_AllUtility(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", command: "tabbar"},
		{id: "%2", command: "sidebar-renderer"},
	}
	content := filterContent(panes)
	if len(content) != 0 {
		t.Fatalf("expected 0 content panes, got %d", len(content))
	}
}

func TestFilterContent_AllContent(t *testing.T) {
	panes := []paneInfo{
		{id: "%1", active: true, command: "zsh"},
		{id: "%2", active: false, command: "vim"},
	}
	content := filterContent(panes)
	if len(content) != 2 {
		t.Fatalf("expected 2 content panes, got %d", len(content))
	}
}

func TestParseHex_WithHash(t *testing.T) {
	r, g, b := parseHex("#ff8040")
	if r != 0xff || g != 0x80 || b != 0x40 {
		t.Fatalf("parseHex #ff8040 = (%d,%d,%d), want (255,128,64)", r, g, b)
	}
}

func TestParseHex_WithoutHash(t *testing.T) {
	r, g, b := parseHex("000000")
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("parseHex 000000 = (%d,%d,%d), want (0,0,0)", r, g, b)
	}
}

func TestParseHex_White(t *testing.T) {
	r, g, b := parseHex("#ffffff")
	if r != 255 || g != 255 || b != 255 {
		t.Fatalf("parseHex #ffffff = (%d,%d,%d), want (255,255,255)", r, g, b)
	}
}

func TestParseHex_InvalidLength(t *testing.T) {
	r, g, b := parseHex("abc")
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("parseHex short = (%d,%d,%d), want (0,0,0)", r, g, b)
	}
}

func TestClamp_Within(t *testing.T) {
	if clamp(128) != 128 {
		t.Fatal("clamp(128) should be 128")
	}
}

func TestClamp_Below(t *testing.T) {
	if clamp(-10) != 0 {
		t.Fatalf("clamp(-10) = %d, want 0", clamp(-10))
	}
}

func TestClamp_Above(t *testing.T) {
	if clamp(300) != 255 {
		t.Fatalf("clamp(300) = %d, want 255", clamp(300))
	}
}

func TestClamp_Boundary(t *testing.T) {
	if clamp(0) != 0 || clamp(255) != 255 {
		t.Fatal("clamp boundary values incorrect")
	}
}

func TestComputeDimBG_Empty(t *testing.T) {
	if got := computeDimBG("", 0.5); got != "" {
		t.Fatalf("computeDimBG empty = %q, want empty", got)
	}
}

func TestComputeDimBG_DarkBG(t *testing.T) {
	got := computeDimBG("#1e1e1e", 0.6)
	if got == "" {
		t.Fatal("computeDimBG dark should return non-empty")
	}
	if got[0] != '#' {
		t.Fatalf("computeDimBG should return hex color, got %q", got)
	}
}

func TestComputeDimBG_LightBG(t *testing.T) {
	got := computeDimBG("#ffffff", 0.6)
	if got == "" {
		t.Fatal("computeDimBG light should return non-empty")
	}
}

func TestExtractStyleColor_FgKey(t *testing.T) {
	got := extractStyleColor("fg=#56949f,bg=#faf4ed", "fg")
	if got != "#56949f" {
		t.Fatalf("extractStyleColor fg = %q, want #56949f", got)
	}
}

func TestExtractStyleColor_BgKey(t *testing.T) {
	got := extractStyleColor("fg=#56949f,bg=#faf4ed", "bg")
	if got != "#faf4ed" {
		t.Fatalf("extractStyleColor bg = %q, want #faf4ed", got)
	}
}

func TestExtractStyleColor_Missing(t *testing.T) {
	got := extractStyleColor("fg=#56949f", "bg")
	if got != "" {
		t.Fatalf("extractStyleColor missing key = %q, want empty", got)
	}
}

func TestExtractStyleColor_Empty(t *testing.T) {
	got := extractStyleColor("", "fg")
	if got != "" {
		t.Fatalf("extractStyleColor empty style = %q, want empty", got)
	}
}

func TestDesaturateColor_Invalid(t *testing.T) {
	got := desaturateColor("notahex", 0.5, "")
	if got != "notahex" {
		t.Fatalf("desaturateColor invalid = %q, want original", got)
	}
}

func TestDesaturateColor_FullOpacity(t *testing.T) {
	got := desaturateColor("#ff0000", 1.0, "")
	if got == "" {
		t.Fatal("desaturateColor full opacity should return non-empty")
	}
}

func TestDesaturateColor_ZeroOpacity(t *testing.T) {
	got := desaturateColor("#ff0000", 0.0, "#000000")
	if got == "" {
		t.Fatal("desaturateColor zero opacity should return non-empty")
	}
}

func TestDesaturateColor_WithTargetBG(t *testing.T) {
	got := desaturateColor("#ff8040", 0.5, "#1e1e1e")
	if got == "" || got[0] != '#' {
		t.Fatalf("desaturateColor with targetBg = %q, want hex", got)
	}
}

func TestDesaturateColor_NoBGDarkColor(t *testing.T) {
	got := desaturateColor("#101010", 0.5, "")
	if got == "" {
		t.Fatal("desaturateColor dark with no bg should return non-empty")
	}
}

func TestDesaturateColor_NoBGLightColor(t *testing.T) {
	got := desaturateColor("#e0e0e0", 0.5, "")
	if got == "" {
		t.Fatal("desaturateColor light with no bg should return non-empty")
	}
}
