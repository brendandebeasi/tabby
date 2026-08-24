package sidebar

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Long-press is a stand-in for a right-click on touch clients, which have no
// right button. Arming it on a desktop would turn every hesitant left click
// into a context menu, so it must be gated on the profile the daemon reports.
func TestSidebarLongPressArmsOnlyForPhoneProfile(t *testing.T) {
	press := tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: 3}

	for _, tc := range []struct {
		profile   string
		wantArmed bool
	}{
		{"phone", true},
		{"desktop", false},
		{"", false},
	} {
		m := &rendererModel{width: 40, height: 24, connected: true, clientProfile: tc.profile}
		got, cmd := m.handleMouse(press)
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

func TestSidebarLongPressTimerFiresAtPressPosition(t *testing.T) {
	m := &rendererModel{width: 40, height: 24, connected: true, clientProfile: "phone"}
	_, cmd := m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 6, Y: 4,
	})
	if cmd == nil {
		t.Fatal("phone press should schedule the long-press timer")
	}
	lp, ok := cmd().(longPressMsg)
	if !ok {
		t.Fatalf("timer produced %T, want longPressMsg", cmd())
	}
	if lp.X != 6 || lp.Y != 4 {
		t.Fatalf("long-press fired at (%d,%d), want (6,4)", lp.X, lp.Y)
	}
}

// Dragging the sidebar to scroll must not turn into a context menu.
func TestSidebarLongPressCancelledByMovement(t *testing.T) {
	m := &rendererModel{width: 40, height: 24, connected: true, clientProfile: "phone"}
	after, _ := m.handleMouse(tea.MouseMsg{
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 6, Y: 4,
	})
	if !after.longPressActive {
		t.Fatal("phone press should arm the long-press")
	}
	moved, _ := (&after).handleMouse(tea.MouseMsg{
		Action: tea.MouseActionMotion, X: 6, Y: 4 + movementThreshold + 1,
	})
	if moved.longPressActive {
		t.Error("movement past the threshold should cancel the long-press")
	}
}
