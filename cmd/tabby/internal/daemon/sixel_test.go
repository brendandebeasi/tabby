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
