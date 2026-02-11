package main

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
)

func TestHashContentStable(t *testing.T) {
	const input = "same content"
	if hashContent(input) != hashContent(input) {
		t.Fatalf("hashContent should be stable for identical input")
	}
	if hashContent("a") == hashContent("b") {
		t.Fatalf("hashContent should differ for different input")
	}
}

func TestGetMainPaneDirection(t *testing.T) {
	c := &Coordinator{config: &config.Config{}}

	if got := c.getMainPaneDirection(); got != "-R" {
		t.Fatalf("left sidebar should point to right pane, got %q", got)
	}

	c.config.Sidebar.Position = "right"
	if got := c.getMainPaneDirection(); got != "-L" {
		t.Fatalf("right sidebar should point to left pane, got %q", got)
	}
}
