package daemon

import (
	"testing"

	"github.com/brendandebeasi/tabby/pkg/daemon"
	"github.com/stretchr/testify/assert"
)

func TestHandleInput_PhoneNavigationFallsThrough(t *testing.T) {
	c := newTestCoordinator(t)
	clientID := "tabby-hook"

	// Force global profile to phone
	c.activeClientWidth.Store(80) // < 100 is phone

	input := &daemon.InputPayload{
		Type:           "action",
		ResolvedAction: "next_window",
		ResolvedTarget: "@1",
	}

	// Phone clients used to DROP key-nav (guard against iOS touch-synthesized
	// M-]/M-[). That source was fixed and native nav now skips minimized windows
	// on its own, so phone nav now falls through to the normal path and proceeds
	// (returns true) instead of being suppressed.
	res := c.HandleInput(clientID, input)
	assert.True(t, res, "Expected phone navigation to fall through and proceed")
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
