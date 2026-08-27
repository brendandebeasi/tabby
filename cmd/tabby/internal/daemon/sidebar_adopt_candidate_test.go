package daemon

import (
	"testing"
	"time"
)

// A sidebar drag made while a second, differently-sized client is attached must
// survive. The guard against adopting such a measurement used to also let the
// per-window loop snap the pane back to the stale global, so the drag was
// reverted within a second (WIDTH_SYNC_ADOPT_SKIP reason=multi_client_sizes,
// then WIDTH_SYNC_PLAN current=35 target=15).
func TestConfirmWidthAdoptCandidate(t *testing.T) {
	now := time.Now()

	t.Run("first sighting is held, not adopted", func(t *testing.T) {
		confirmed, next := confirmWidthAdoptCandidate(pendingWidthAdopt{}, "@1822", 35, 165, now)
		if confirmed {
			t.Fatal("adopted an uncorroborated measurement")
		}
		if next.windowID != "@1822" || next.width != 35 || next.windowWidth != 165 {
			t.Fatalf("candidate not carried forward: %+v", next)
		}
	})

	t.Run("same width in same geometry confirms", func(t *testing.T) {
		held := pendingWidthAdopt{windowID: "@1822", width: 35, windowWidth: 165, at: now}
		confirmed, next := confirmWidthAdoptCandidate(held, "@1822", 35, 165, now.Add(500*time.Millisecond))
		if !confirmed {
			t.Fatal("a drag corroborated by a second pass was not adopted")
		}
		if (next != pendingWidthAdopt{}) {
			t.Fatalf("candidate not cleared after adopt: %+v", next)
		}
	})

	t.Run("client oscillation never confirms", func(t *testing.T) {
		var held pendingWidthAdopt
		for _, w := range []int{32, 43, 37, 43} {
			confirmed, next := confirmWidthAdoptCandidate(held, "@1822", w, 165, now)
			if confirmed {
				t.Fatalf("adopted oscillating width %d", w)
			}
			held = next
		}
	})

	t.Run("window reflow between clients never confirms", func(t *testing.T) {
		held := pendingWidthAdopt{windowID: "@1822", width: 35, windowWidth: 165, at: now}
		if confirmed, _ := confirmWidthAdoptCandidate(held, "@1822", 35, 120, now); confirmed {
			t.Fatal("adopted a width measured after the window was reflowed")
		}
	})

	t.Run("different window never confirms", func(t *testing.T) {
		held := pendingWidthAdopt{windowID: "@1822", width: 35, windowWidth: 165, at: now}
		if confirmed, _ := confirmWidthAdoptCandidate(held, "@1864", 35, 165, now); confirmed {
			t.Fatal("adopted another window's measurement")
		}
	})

	t.Run("stale candidate never confirms", func(t *testing.T) {
		held := pendingWidthAdopt{windowID: "@1822", width: 35, windowWidth: 165, at: now}
		confirmed, next := confirmWidthAdoptCandidate(held, "@1822", 35, 165, now.Add(widthAdoptCandidateTTL+time.Second))
		if confirmed {
			t.Fatal("adopted a candidate past its TTL")
		}
		if next.at.Equal(held.at) {
			t.Fatal("stale candidate was not re-armed with the fresh timestamp")
		}
	})
}

// The renderer-report arm is what saves a drag when the user switches windows
// before the plan-time arm (which only sees the active window) ever runs.
func TestArmWidthAdoptCandidate(t *testing.T) {
	t.Run("off-global sidebar width arms", func(t *testing.T) {
		c := &Coordinator{}
		c.globalWidth = 74
		c.ArmWidthAdoptCandidate("@1822", 60)
		if c.pendingAdopt.windowID != "@1822" || c.pendingAdopt.width != 60 {
			t.Fatalf("candidate not armed: %+v", c.pendingAdopt)
		}
	})

	t.Run("same-as-global never arms", func(t *testing.T) {
		c := &Coordinator{}
		c.globalWidth = 74
		c.ArmWidthAdoptCandidate("@1822", 74)
		if c.pendingAdopt.windowID != "" {
			t.Fatalf("armed a no-op width: %+v", c.pendingAdopt)
		}
	})

	t.Run("implausible widths never arm", func(t *testing.T) {
		c := &Coordinator{}
		c.globalWidth = 74
		c.ArmWidthAdoptCandidate("@1822", 2)
		c.ArmWidthAdoptCandidate("@1822", 120)
		if c.pendingAdopt.windowID != "" {
			t.Fatalf("armed an implausible width: %+v", c.pendingAdopt)
		}
	})

	t.Run("header clients never arm", func(t *testing.T) {
		c := &Coordinator{}
		c.globalWidth = 74
		c.ArmWidthAdoptCandidate("window-header:@1822", 60)
		if c.pendingAdopt.windowID != "" {
			t.Fatalf("armed a header client: %+v", c.pendingAdopt)
		}
	})
}

// A drag confirmed by the pass AFTER the user switches away must still adopt —
// otherwise the switch discards the candidate and the dragged window snaps
// back to the stale global (the "resize then switch loses the width" bug).
func TestConfirmWidthAdoptOnSwitch(t *testing.T) {
	now := time.Now()
	armed := pendingWidthAdopt{windowID: "@1822", width: 35, windowWidth: 165, at: now}

	t.Run("switch away with width intact confirms", func(t *testing.T) {
		w, ok := confirmWidthAdoptOnSwitch(armed, "@1864", map[string]int{"@1822": 35}, now.Add(time.Second))
		if !ok || w != 35 {
			t.Fatalf("switch-away corroboration failed: ok=%v w=%d", ok, w)
		}
	})

	t.Run("no switch means no switch-confirm", func(t *testing.T) {
		if _, ok := confirmWidthAdoptOnSwitch(armed, "@1822", map[string]int{"@1822": 35}, now.Add(time.Second)); ok {
			t.Fatal("same-window pass must go through the normal confirm path")
		}
	})

	t.Run("width moved after arming never confirms", func(t *testing.T) {
		if _, ok := confirmWidthAdoptOnSwitch(armed, "@1864", map[string]int{"@1822": 40}, now.Add(time.Second)); ok {
			t.Fatal("adopted a width the window no longer measures")
		}
	})

	t.Run("dragged window gone never confirms", func(t *testing.T) {
		if _, ok := confirmWidthAdoptOnSwitch(armed, "@1864", map[string]int{}, now.Add(time.Second)); ok {
			t.Fatal("adopted a width for a closed window")
		}
	})

	t.Run("stale candidate never confirms", func(t *testing.T) {
		if _, ok := confirmWidthAdoptOnSwitch(armed, "@1864", map[string]int{"@1822": 35}, now.Add(widthAdoptCandidateTTL+time.Second)); ok {
			t.Fatal("adopted a candidate past its TTL")
		}
	})
}
