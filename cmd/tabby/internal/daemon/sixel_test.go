package daemon

import (
	"os"
	"strings"
	"testing"
)

// TestSixelDump writes a generated sixel to $TABBY_SIXEL_DUMP so an external
// decoder (sixel2png) can confirm it is a valid, decodable image. Skipped in
// normal runs.
func TestSixelDump(t *testing.T) {
	p := os.Getenv("TABBY_SIXEL_DUMP")
	if p == "" {
		t.Skip("set TABBY_SIXEL_DUMP to write the sixel")
	}
	if err := os.WriteFile(p, []byte(sixelGradientBar("#204060", "#c0d0e0", 60, 12)), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestKittyDump writes a generated kitty graphics sequence to $TABBY_KITTY_DUMP
// so it can be decoded externally (parse _G chunks, base64-decode -> raw RGB).
func TestKittyDump(t *testing.T) {
	p := os.Getenv("TABBY_KITTY_DUMP")
	if p == "" {
		t.Skip("set TABBY_KITTY_DUMP to write the kitty sequence")
	}
	if err := os.WriteFile(p, []byte(kittyGradientBar("#204060", "#c0d0e0", 60, 12)), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSixelGradientBarStructure(t *testing.T) {
	s := sixelGradientBar("#000000", "#ffffff", 20, 12)
	if !strings.HasPrefix(s, "\x1bPq") {
		t.Fatalf("missing sixel DCS intro")
	}
	if !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("missing ST terminator")
	}
	if !strings.Contains(s, `"1;1;20;12`) {
		t.Fatalf("missing raster attributes")
	}
	// Palette definitions and at least one band separator (12px => 2 bands).
	if !strings.Contains(s, "#0;2;") {
		t.Fatalf("missing colour #0 definition")
	}
	if !strings.Contains(s, "-") {
		t.Fatalf("expected a band separator for 12px height")
	}
}

func TestKittyGradientBarStructure(t *testing.T) {
	// Small image fits in one chunk (m=0).
	s := kittyGradientBar("#000000", "#ffffff", 8, 4)
	if !strings.HasPrefix(s, "\x1b_Gf=24,s=8,v=4,a=T,m=0;") {
		t.Fatalf("bad kitty header: %q", s[:40])
	}
	if !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("missing ST terminator")
	}
	// Large image must chunk: first has m=1, and a continuation "\x1b_Gm=" appears.
	big := kittyGradientBar("#000000", "#ffffff", 600, 12)
	if !strings.Contains(big, ",m=1;") {
		t.Fatalf("expected first chunk m=1 for a large image")
	}
	if !strings.Contains(big, "\x1b_Gm=") {
		t.Fatalf("expected a continuation chunk (\\x1b_Gm=...)")
	}
	if !strings.Contains(big, "m=0;") {
		t.Fatalf("expected a final chunk m=0")
	}
}

func TestTmuxPassthroughDoublesEsc(t *testing.T) {
	got := tmuxPassthrough("\x1bPq\x1b\\")
	if !strings.HasPrefix(got, "\x1bPtmux;") || !strings.HasSuffix(got, "\x1b\\") {
		t.Fatalf("passthrough envelope wrong: %q", got)
	}
	// Inner ESCs must be doubled.
	if strings.Contains(got[len("\x1bPtmux;"):len(got)-2], "\x1b\x1b") == false {
		t.Fatalf("inner ESC not doubled")
	}
}
