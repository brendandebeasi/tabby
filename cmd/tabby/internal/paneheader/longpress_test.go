package paneheader

import (
	"io"
	"log"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMain(m *testing.M) {
	debugLog = log.New(io.Discard, "", 0)
	crashLog = log.New(io.Discard, "", 0)
	os.Exit(m.Run())
}

// Long-press is a stand-in for a right-click on touch clients, which have no
// right button. Arming it on a desktop would turn every hesitant left click
// into a context menu, so it must be gated on the profile the daemon reports.
func TestLongPressArmsOnlyForPhoneProfile(t *testing.T) {
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: 0}

	for _, tc := range []struct {
		profile   string
		wantArmed bool
	}{
		{"phone", true},
		{"desktop", false},
		{"", false},
	} {
		m := rendererModel{connected: true, width: 80, clientProfile: tc.profile}
		result, cmd := m.handleMouse(press)
		got, ok := result.(rendererModel)
		if !ok {
			t.Fatalf("profile %q: handleMouse returned %T", tc.profile, result)
		}
		if got.longPressActive != tc.wantArmed {
			t.Errorf("profile %q: longPressActive=%v, want %v", tc.profile, got.longPressActive, tc.wantArmed)
		}
		// The timer is what actually delivers the long-press; a flag with no
		// tick behind it would never fire.
		if (cmd != nil) != tc.wantArmed {
			t.Errorf("profile %q: timer scheduled=%v, want %v", tc.profile, cmd != nil, tc.wantArmed)
		}
	}
}

func TestLongPressTimerFiresAtPressPosition(t *testing.T) {
	m := rendererModel{connected: true, width: 80, clientProfile: "phone"}
	result, cmd := m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 3, Y: 0,
	})
	if cmd == nil {
		t.Fatal("phone press should schedule the long-press timer")
	}
	lp, ok := cmd().(longPressMsg)
	if !ok {
		t.Fatalf("timer produced %T, want longPressMsg", cmd())
	}
	if lp.X != 3 || lp.Y != 0 {
		t.Fatalf("long-press fired at (%d,%d), want (3,0)", lp.X, lp.Y)
	}

	moved, _ := result.(rendererModel).handleMouse(tea.MouseMsg{
		Action: tea.MouseActionMotion, X: 3 + movementThreshold + 1, Y: 0,
	})
	if moved.(rendererModel).longPressActive {
		t.Error("movement past the threshold should cancel the long-press")
	}
}
