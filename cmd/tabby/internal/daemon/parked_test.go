package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/tmux"
)

func TestIsEffectivelyParked(t *testing.T) {
	cases := []struct {
		name string
		win  tmux.Window
		want bool
	}{
		{"not minimized", tmux.Window{Minimized: false, Panes: []tmux.Pane{{ID: "%5"}}}, false},
		{"parked synthetic %parked", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: "%parked"}}}, true},
		{"parked no panes", tmux.Window{Minimized: true}, true},
		{"parked empty pane id", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: ""}}}, true},
		{"surfaced real pane", tmux.Window{Minimized: true, Panes: []tmux.Pane{{ID: "%12"}}}, false},
	}
	for _, c := range cases {
		if got := isEffectivelyParked(c.win); got != c.want {
			t.Errorf("%s: isEffectivelyParked = %v, want %v", c.name, got, c.want)
		}
	}
}
