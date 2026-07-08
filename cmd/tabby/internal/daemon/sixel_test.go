package daemon

import (
	"strings"
	"testing"
)

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
