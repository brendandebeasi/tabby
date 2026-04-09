package main

import (
	"testing"
)

func TestTrackWindowHistory_Basic(t *testing.T) {
	c := newTestCoordinator(t)

	c.TrackWindowHistory("@1")
	c.TrackWindowHistory("@2")
	c.TrackWindowHistory("@3")

	history := c.GetWindowHistory()
	expected := []string{"@3", "@2", "@1"}
	if len(history) != len(expected) {
		t.Fatalf("history length = %d, want %d", len(history), len(expected))
	}
	for i, id := range expected {
		if history[i] != id {
			t.Errorf("history[%d] = %q, want %q", i, history[i], id)
		}
	}
}

func TestTrackWindowHistory_Dedup(t *testing.T) {
	c := newTestCoordinator(t)

	c.TrackWindowHistory("@1")
	c.TrackWindowHistory("@2")
	c.TrackWindowHistory("@3")
	c.TrackWindowHistory("@1") // revisit @1

	history := c.GetWindowHistory()
	expected := []string{"@1", "@3", "@2"}
	if len(history) != len(expected) {
		t.Fatalf("history length = %d, want %d", len(history), len(expected))
	}
	for i, id := range expected {
		if history[i] != id {
			t.Errorf("history[%d] = %q, want %q", i, history[i], id)
		}
	}
}

func TestTrackWindowHistory_Cap20(t *testing.T) {
	c := newTestCoordinator(t)

	for i := 0; i < 25; i++ {
		c.TrackWindowHistory("@" + string(rune('A'+i)))
	}

	history := c.GetWindowHistory()
	if len(history) != 20 {
		t.Errorf("history length = %d, want 20", len(history))
	}
	// Most recent should be first
	if history[0] != "@Y" {
		t.Errorf("history[0] = %q, want %q", history[0], "@Y")
	}
}

func TestTrackWindowHistory_EmptyID(t *testing.T) {
	c := newTestCoordinator(t)

	c.TrackWindowHistory("")
	history := c.GetWindowHistory()
	if len(history) != 0 {
		t.Errorf("history should be empty after tracking empty ID, got %d", len(history))
	}
}

func TestTrackWindowHistory_RevisitOrder(t *testing.T) {
	c := newTestCoordinator(t)

	// Visit 1,3,5,2,4 — history should be 4,2,5,3,1
	c.TrackWindowHistory("@1")
	c.TrackWindowHistory("@3")
	c.TrackWindowHistory("@5")
	c.TrackWindowHistory("@2")
	c.TrackWindowHistory("@4")

	history := c.GetWindowHistory()
	expected := []string{"@4", "@2", "@5", "@3", "@1"}
	if len(history) != len(expected) {
		t.Fatalf("history length = %d, want %d", len(history), len(expected))
	}
	for i, id := range expected {
		if history[i] != id {
			t.Errorf("history[%d] = %q, want %q", i, history[i], id)
		}
	}
}
