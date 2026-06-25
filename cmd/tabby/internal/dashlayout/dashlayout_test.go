package dashlayout

import (
	"reflect"
	"testing"
)

// apply replays the swap plan on a copy of order and returns the result, so we
// can assert the plan actually produces the intended arrangement.
func apply(order []string, swaps [][2]string) []string {
	out := append([]string(nil), order...)
	idx := func(id string) int {
		for i, v := range out {
			if v == id {
				return i
			}
		}
		return -1
	}
	for _, sw := range swaps {
		i, j := idx(sw[0]), idx(sw[1])
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func TestPlanActiveMainSwaps(t *testing.T) {
	cases := []struct {
		name   string
		order  []string
		active string
		want   []string // expected final arrangement after applying the plan
	}{
		{"active already main, rest sorted", []string{"%1", "%2", "%3"}, "%1", []string{"%1", "%2", "%3"}},
		{"promote middle, stack stays id-sorted", []string{"%1", "%2", "%3", "%4"}, "%3", []string{"%3", "%1", "%2", "%4"}},
		{"promote last", []string{"%1", "%2", "%3", "%4"}, "%4", []string{"%4", "%1", "%2", "%3"}},
		{"unsorted stack gets normalized", []string{"%4", "%2", "%1", "%3"}, "%2", []string{"%2", "%1", "%3", "%4"}},
		{"two panes", []string{"%9", "%5"}, "%5", []string{"%5", "%9"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			swaps := PlanActiveMainSwaps(c.order, c.active)
			got := apply(c.order, swaps)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("plan applied = %v, want %v (swaps=%v)", got, c.want, swaps)
			}
			// First slot must always end up as the active pane.
			if got[0] != c.active {
				t.Errorf("main slot = %s, want active %s", got[0], c.active)
			}
		})
	}
}

func TestPlanActiveMainSwapsNoOp(t *testing.T) {
	// Already arranged -> no swaps emitted (avoids needless tmux churn on focus).
	if s := PlanActiveMainSwaps([]string{"%1", "%2", "%3"}, "%1"); len(s) != 0 {
		t.Errorf("already-arranged should emit no swaps, got %v", s)
	}
}

func TestPlanActiveMainSwapsDegenerate(t *testing.T) {
	if s := PlanActiveMainSwaps([]string{"%1"}, "%1"); s != nil {
		t.Errorf("single pane: want nil, got %v", s)
	}
	if s := PlanActiveMainSwaps(nil, "%1"); s != nil {
		t.Errorf("empty: want nil, got %v", s)
	}
	if s := PlanActiveMainSwaps([]string{"%1", "%2"}, ""); s != nil {
		t.Errorf("no active: want nil, got %v", s)
	}
	if s := PlanActiveMainSwaps([]string{"%1", "%2"}, "%9"); s != nil {
		t.Errorf("active absent: want nil, got %v", s)
	}
}
