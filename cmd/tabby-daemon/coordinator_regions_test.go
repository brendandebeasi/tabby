package main

import (
	"strings"
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestIsTouchModeDefaults(t *testing.T) {
	t.Setenv("TABBY_TOUCH", "")
	c := &Coordinator{config: &config.Config{}}

	if c.isTouchMode(80) {
		t.Fatalf("expected wide layout to default to non-touch mode")
	}
	if c.isTouchMode(30) {
		t.Fatalf("expected narrow layout to default to non-touch mode")
	}
}

func TestIsTouchModeAutoEnv(t *testing.T) {
	t.Setenv("TABBY_TOUCH", "auto")
	c := &Coordinator{config: &config.Config{}}
	if !c.isTouchMode(30) {
		t.Fatalf("expected TABBY_TOUCH=auto to enable touch mode on narrow layout")
	}
	if c.isTouchMode(80) {
		t.Fatalf("expected TABBY_TOUCH=auto to keep wide layout non-touch")
	}
}

func TestIsTouchModeOverrideWins(t *testing.T) {
	t.Setenv("TABBY_TOUCH", "")
	c := &Coordinator{config: &config.Config{}, touchModeOverride: "0"}
	if c.isTouchMode(30) {
		t.Fatalf("expected override=0 to force non-touch mode")
	}

	c.touchModeOverride = "1"
	if !c.isTouchMode(120) {
		t.Fatalf("expected override=1 to force touch mode")
	}
}

func TestLineSpacingUsesConfig(t *testing.T) {
	c := &Coordinator{config: &config.Config{}}
	if got := c.lineSpacing(); got != "" {
		t.Fatalf("expected default line spacing empty, got %q", got)
	}

	c.config.Sidebar.LineHeight = 2
	if got := c.lineSpacing(); got != "\n\n" {
		t.Fatalf("expected two newlines, got %q", got)
	}
}

func TestTouchPadLineRespectsWidthAndMode(t *testing.T) {
	c := &Coordinator{config: &config.Config{}}
	if got := c.touchPadLine(0, "#111111"); got != "\n" {
		t.Fatalf("expected newline for non-positive width, got %q", got)
	}

	line := c.touchPadLine(5, "#111111")
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected touchPadLine output to end with newline")
	}
}
