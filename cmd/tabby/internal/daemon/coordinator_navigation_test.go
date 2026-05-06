package daemon

import (
	"testing"
	"time"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

func TestHandleInput_NavigationDebouncing(t *testing.T) {
	c := newTestCoordinator(t)
	clientID := "tabby-hook"

	// Mock next_window action
	input := &daemon.InputPayload{
		Type:           "action",
		ResolvedAction: "next_window",
		ResolvedTarget: "@1",
	}

	// First call should be handled (not debounced)
	// Note: HandleInput returns true if refresh is needed, 
	// but navigation often returns true to trigger immediate refresh.
	res1 := c.HandleInput(clientID, input)
	assert.True(t, res1)

	// Immediate second call should be debounced
	res2 := c.HandleInput(clientID, input)
	assert.False(t, res2, "Expected second navigation within 300ms to be debounced")

	// Wait 350ms
	time.Sleep(350 * time.Millisecond)

	// Third call should be handled
	res3 := c.HandleInput(clientID, input)
	assert.True(t, res3, "Expected navigation after 300ms to be handled")
}

func TestHandleInput_NavigationSuppression_GlobalActive(t *testing.T) {
	c := newTestCoordinator(t)
	clientID := "tabby-hook"

	// Force global profile to phone
	c.activeClientWidth.Store(80) // < 100 is phone

	input := &daemon.InputPayload{
		Type:           "action",
		ResolvedAction: "next_window",
		ResolvedTarget: "@1",
	}

	// Should be suppressed due to global phone profile
	res := c.HandleInput(clientID, input)
	assert.False(t, res, "Expected navigation to be suppressed when global profile is phone")
}

func TestHandleInput_NavigationSuppression_InvokingTTY(t *testing.T) {
	c := newTestCoordinator(t)
	clientID := "tabby-hook"

	// Global profile is desktop, no invoking TTY supplied — neither
	// suppress branch should fire, and the navigation should be allowed.
	//
	// Earlier revisions of this test passed PickerValue="invoking=/dev/ttys001"
	// expecting `getTTYWidth` to return 0 because `/dev/ttys001` "doesn't
	// exist" — but `tmux display-message -c <bogus-tty>` falls back to the
	// default attached client on most setups, so `getTTYWidth` returns the
	// actual phone width (75) and the test inverted into the suppress
	// branch. With no invoking TTY at all the branch is skipped
	// deterministically without needing to mock tmux.
	c.activeClientWidth.Store(150)
	input := &daemon.InputPayload{
		Type:           "action",
		ResolvedAction: "next_window",
		ResolvedTarget: "@1",
	}

	res := c.HandleInput(clientID, input)
	assert.True(t, res)
}
