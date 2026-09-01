package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/config"
	"github.com/brendandebeasi/tabby/pkg/grouping"
	"github.com/brendandebeasi/tabby/pkg/tmux"
)

const zoneTestClient = "/dev/ttys000"

// escalationCoordinator returns a coordinator with the pet enabled and the
// widget-zone height cache empty.
func escalationCoordinator(t *testing.T, debugBar bool) *Coordinator {
	t.Helper()
	c := newRenderCoordinator(t)
	c.config.Widgets.Pet.Enabled = true
	c.config.Widgets.Pet.DebugBar = debugBar
	c.widgetZoneHeights.Clear()
	return c
}

// An unmeasured width must behave exactly as the code did before the cache
// existed: render the un-escalated variant and let the checks that follow
// escalate for real.
func TestPredictWidgetEscalationDefersWhenNothingIsMeasured(t *testing.T) {
	c := escalationCoordinator(t, false)
	if hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 24, 3, 40); hidePet || hideDebug {
		t.Fatalf("unmeasured width predicted (%v, %v), want (false, false)", hidePet, hideDebug)
	}
}

func TestPredictWidgetEscalationKeepsThePetWhenItFits(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 6)
	// 40 - 3 header - 10 widgets = 27 lines for 20 tabs.
	if hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 40, 3, 20); hidePet || hideDebug {
		t.Fatalf("predicted (%v, %v) for a viewport with room to spare, want (false, false)", hidePet, hideDebug)
	}
}

func TestPredictWidgetEscalationHidesThePetWhenItDoesNot(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 6)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, true, false, 1, 3)
	// 24 - 3 header - 10 widgets = 11 lines for 20 tabs: does not fit.
	hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 24, 3, 20)
	if !hidePet || hideDebug {
		t.Fatalf("predicted (%v, %v) for an overflowing viewport, want (true, false)", hidePet, hideDebug)
	}
}

// Dropping the debug bar is the gentler escalation and has to be preferred
// over hiding the pet outright.
func TestPredictWidgetEscalationDropsTheDebugBarFirst(t *testing.T) {
	c := escalationCoordinator(t, true)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 9)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, true, 4, 6)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, true, false, 1, 3)
	// 24 - 3 header: 13 rows available. With the debug bar the widgets take
	// 13 and nothing is left; without it they take 10, leaving 11 for 11 tabs.
	hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 24, 3, 11)
	if hidePet || !hideDebug {
		t.Fatalf("predicted (%v, %v), want (false, true): the debug bar goes before the pet", hidePet, hideDebug)
	}
}

// If the gentler variant has never been rendered there is no height to judge
// it by, so the prediction must step back rather than skip over it and hide
// the pet on a viewport where dropping the debug bar alone would have done.
func TestPredictWidgetEscalationWillNotSkipAnUnmeasuredStep(t *testing.T) {
	c := escalationCoordinator(t, true)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 9)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, true, false, 1, 3)
	if hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 24, 3, 20); hidePet || hideDebug {
		t.Fatalf("predicted (%v, %v) with the debug-bar variant unmeasured, want (false, false)", hidePet, hideDebug)
	}
}

func TestPredictWidgetEscalationIgnoresOtherWidths(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 6)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, true, false, 1, 3)
	if hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 45, 24, 3, 20); hidePet || hideDebug {
		t.Fatalf("a measurement at width 30 drove the prediction at width 45: (%v, %v)", hidePet, hideDebug)
	}
}

func TestPredictWidgetEscalationLeavesADisabledPetAlone(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.config.Widgets.Pet.Enabled = false
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 6)
	if hidePet, hideDebug := c.predictWidgetEscalation(zoneTestClient, 30, 24, 3, 99); hidePet || hideDebug {
		t.Fatalf("predicted (%v, %v) with the pet disabled, want (false, false)", hidePet, hideDebug)
	}
}

func widgetTestWindows() []tmux.Window {
	names := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	out := make([]tmux.Window, 0, len(names))
	for i, n := range names {
		out = append(out, testWindow(n, i == 0, "bash"))
	}
	return out
}

// The whole point of the cache is that the second frame skips the throwaway
// render. It must reach the same layout as the first one -- which did not
// have the cache to consult -- or the sidebar would flicker between frames.
func TestRenderForClientIsStableOnceTheCacheIsWarm(t *testing.T) {
	escalated := false
	for _, debugBar := range []bool{false, true} {
		for _, height := range []int{8, 12, 16, 24, 40, 60} {
			c := escalationCoordinator(t, debugBar)
			c.stateMu.Lock()
			c.windows = widgetTestWindows()
			c.grouped = []grouping.GroupedWindows{{
				Name:    "Default",
				Theme:   config.Theme{Bg: "#2c3e50", Fg: "#ecf0f1", ActiveBg: "#3498db", ActiveFg: "#ffffff"},
				Windows: c.windows,
			}}
			c.stateMu.Unlock()

			cold := c.RenderForClient("client", 30, height)
			warm := c.RenderForClient("client", 30, height)
			if cold == nil || warm == nil {
				t.Fatalf("debugBar=%v height=%d: nil payload", debugBar, height)
			}
			if cold.Content != warm.Content {
				t.Errorf("debugBar=%v height=%d: warm frame differs from cold frame\ncold:\n%s\nwarm:\n%s",
					debugBar, height, cold.Content, warm.Content)
			}
			if _, hid := c.widgetZoneHeights.Load(widgetZoneKey{clientID: "client", width: 30, hidePet: true}); hid {
				escalated = true
			}
		}
	}
	// Without this the loop above could pass while never once exercising the
	// escalation the cache exists to skip.
	if !escalated {
		t.Fatal("no viewport in the sweep was tight enough to hide the pet, so the test proved nothing")
	}
}

// A zone's height can differ between clients at the same width, and nothing
// downstream de-escalates, so one client's measurement must never talk
// another into hiding its pet.
func TestPredictWidgetEscalationIgnoresOtherClients(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, false, false, 4, 6)
	c.rememberWidgetZoneHeight(zoneTestClient, 30, true, false, 1, 3)
	if hidePet, hideDebug := c.predictWidgetEscalation("/dev/ttys001", 30, 24, 3, 20); hidePet || hideDebug {
		t.Fatalf("another client's measurement drove the prediction: (%v, %v)", hidePet, hideDebug)
	}
}

func TestWidgetZoneHeightsStayBounded(t *testing.T) {
	c := escalationCoordinator(t, false)
	c.widgetZoneHeightsN.Store(0)
	for i := range widgetZoneHeightsMax * 2 {
		c.rememberWidgetZoneHeight(zoneTestClient, i, false, false, 4, 6)
	}
	if got := c.widgetZoneHeightsN.Load(); got > widgetZoneHeightsMax {
		t.Fatalf("cache holds %d entries, want at most %d", got, widgetZoneHeightsMax)
	}
	live := 0
	c.widgetZoneHeights.Range(func(any, any) bool {
		live++
		return true
	})
	if live > widgetZoneHeightsMax {
		t.Fatalf("map holds %d entries, want at most %d", live, widgetZoneHeightsMax)
	}
}
