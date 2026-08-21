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
