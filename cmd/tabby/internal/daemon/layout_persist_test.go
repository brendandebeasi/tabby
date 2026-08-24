package daemon

import (
	"testing"
)

const testLayout1 = "abcd,80x24,0,0{39x24,0,0,1,40x24,40,0,2}"
const testLayout2 = "efgh,100x24,0,0{39x24,0,0,1,60x24,40,0,2}"

// captureMirror swaps the tmux option writers for in-memory ones and returns
// the map they write into.
func captureMirror(t *testing.T) map[string]string {
	t.Helper()
	written := map[string]string{}
	origSet, origUnset := mirrorTmuxOption, unsetTmuxOption
	mirrorTmuxOption = func(name, value string) { written[name] = value }
	unsetTmuxOption = func(name string) { delete(written, name) }
	t.Cleanup(func() {
		mirrorTmuxOption, unsetTmuxOption = origSet, origUnset
	})
	return written
}

// The per-width buckets are the only record of a window's split proportions at
// a geometry the active client is not currently using, and they used to live
// only in memory — so a daemon restart lost them and the next reconcile let
// tmux flatten the splits.
func TestSaveWindowLayoutMirrorsToTmux(t *testing.T) {
	c := newTestCoordinator(t)
	written := captureMirror(t)

	c.SaveWindowLayout("@1", 80, testLayout1)
	got := written["@tabby_layouts_@1"]
	if got == "" {
		t.Fatal("layout was not mirrored to a tmux option")
	}

	buckets := decodeLayoutBuckets(got)
	if buckets[80] != testLayout1 {
		t.Fatalf("round trip lost the layout: %#v", buckets)
	}
}

func TestSaveWindowLayoutSkipsUnchanged(t *testing.T) {
	c := newTestCoordinator(t)
	writes := 0
	origSet := mirrorTmuxOption
	mirrorTmuxOption = func(name, value string) { writes++ }
	t.Cleanup(func() { mirrorTmuxOption = origSet })

	c.SaveWindowLayout("@1", 80, testLayout1)
	c.SaveWindowLayout("@1", 80, testLayout1)
	c.SaveWindowLayout("@1", 80, testLayout1)
	if writes != 1 {
		t.Fatalf("unchanged layout re-wrote the option: %d writes, want 1", writes)
	}

	c.SaveWindowLayout("@1", 100, testLayout2)
	if writes != 2 {
		t.Fatalf("new width did not write: %d writes, want 2", writes)
	}
}

func TestForgetWindowLayoutsClearsMirror(t *testing.T) {
	c := newTestCoordinator(t)
	written := captureMirror(t)

	c.SaveWindowLayout("@1", 80, testLayout1)
	c.SaveWindowLayout("@2", 80, testLayout1)

	c.ForgetWindowLayouts("@1")
	if _, ok := written["@tabby_layouts_@1"]; ok {
		t.Fatal("ForgetWindowLayouts left the mirrored option behind")
	}
	if _, ok := written["@tabby_layouts_@2"]; !ok {
		t.Fatal("ForgetWindowLayouts cleared an unrelated window")
	}

	c.ForgetAllWindowLayouts()
	if len(written) != 0 {
		t.Fatalf("ForgetAllWindowLayouts left %d options behind", len(written))
	}
}

func TestEvictLayoutBucketsKeepsNearbyWidths(t *testing.T) {
	m := map[int]string{}
	for _, w := range []int{20, 40, 60, 80, 100, 120, 140, 160, 180} {
		m[w] = testLayout1
	}
	evictLayoutBuckets(m, 100)
	if len(m) != windowLayoutBucketCap {
		t.Fatalf("len = %d, want %d", len(m), windowLayoutBucketCap)
	}
	if _, ok := m[100]; !ok {
		t.Fatal("evicted the width being written")
	}
	if _, ok := m[20]; ok {
		t.Fatal("kept the furthest width instead of evicting it")
	}
}

func TestDecodeLayoutBucketsRejectsGarbage(t *testing.T) {
	cases := []string{"", "  ", "not json", `{"80":""}`, `{"zero":"x"}`, `{"0":"x"}`}
	for _, in := range cases {
		if got := decodeLayoutBuckets(in); got != nil {
			t.Fatalf("decodeLayoutBuckets(%q) = %#v, want nil", in, got)
		}
	}
}

// A layout naming panes that no longer exist makes select-layout fail, and a
// failing command aborts the rest of the chained reconcile — so restored
// buckets are matched against the window's live panes.
func TestLayoutPaneSetMatching(t *testing.T) {
	ids := layoutPaneIDs(testLayout1)
	if len(ids) != 2 || !ids[1] || !ids[2] {
		t.Fatalf("layoutPaneIDs = %#v, want {1,2}", ids)
	}

	live := paneIDNumbers([]panesRow{{ID: "%1"}, {ID: "%2"}})
	if !samePaneSet(ids, live) {
		t.Fatal("identical pane sets did not match")
	}
	if samePaneSet(ids, paneIDNumbers([]panesRow{{ID: "%1"}})) {
		t.Fatal("a killed pane still matched")
	}
	if samePaneSet(ids, paneIDNumbers([]panesRow{{ID: "%1"}, {ID: "%3"}})) {
		t.Fatal("a replaced pane still matched")
	}
	if samePaneSet(map[int]bool{}, live) {
		t.Fatal("an empty layout matched a live window")
	}
}
